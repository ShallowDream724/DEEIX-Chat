package llm

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	chat01parse "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm/chat01"
	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// Chat01 reuses the HTTP lifecycle, outbound policy and acceptance tracking;
// request encoding and response interpretation remain a separate adapter.
type chat01Adapter struct{ client *Client }

func (*chat01Adapter) Name() string { return portllm.AdapterChat01ChatCompletions }
func (a *chat01Adapter) Generate(ctx context.Context, route portllm.RouteConfig, input portllm.GenerateInput) (*portllm.GenerateOutput, error) {
	route.Endpoint = portllm.EndpointChatCompletions
	return a.client.generateOpenAICompatible(ctx, route, input)
}
func (a *chat01Adapter) GenerateStream(ctx context.Context, route portllm.RouteConfig, input portllm.GenerateInput, onEvent func(portllm.GenerateStreamEvent) error) (*portllm.GenerateOutput, error) {
	route.Endpoint = portllm.EndpointChatCompletions
	return a.client.generateStreamOpenAICompatible(ctx, route, input, onEvent)
}
func (a *chat01Adapter) ListModels(ctx context.Context, route portllm.RouteConfig) ([]portllm.ModelItem, error) {
	return a.client.listModelsOpenAICompatible(ctx, route)
}

func buildChat01RequestBody(model string, input portllm.GenerateInput, stream bool) (map[string]any, error) {
	messages := make([]map[string]any, 0, len(input.Messages))
	for _, message := range input.Messages {
		// Server tool traces are display history, never executable client calls.
		if message.Role == "tool" {
			continue
		}
		role := message.Role
		if role == "developer" {
			role = "system"
		}
		if role != "user" && role != "assistant" && role != "system" {
			return nil, fmt.Errorf("unsupported Chat01 message role")
		}
		item := map[string]any{"role": role, "content": message.Content}
		if len(message.Parts) > 0 {
			parts := make([]map[string]any, 0, len(message.Parts))
			for _, part := range message.Parts {
				if part.Kind == portllm.ContentPartText || part.Kind == portllm.ContentPartFile {
					parts = append(parts, map[string]any{"type": "text", "text": part.Text})
					continue
				}
				switch part.Kind {
				case portllm.ContentPartImage, portllm.ContentPartDocument, portllm.ContentPartAudio, portllm.ContentPartVideo, portllm.ContentPartAttachment:
				default:
					return nil, fmt.Errorf("unsupported Chat01 content type")
				}
				address := ""
				if part.Reference != nil {
					address = part.Reference.URL
				}
				if address == "" {
					if len(part.Data) == 0 {
						return nil, fmt.Errorf("Chat01 attachment transport was not prepared")
					}
					mime := strings.TrimSpace(part.MimeType)
					if mime == "" {
						mime = "application/octet-stream"
					}
					address = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
				}
				kind := "file_url"
				if part.Kind == portllm.ContentPartImage {
					kind = "image_url"
				}
				parts = append(parts, map[string]any{"type": kind, kind: map[string]any{"url": address}})
			}
			item["content"] = parts
		}
		messages = append(messages, item)
	}
	body := map[string]any{"model": model, "messages": messages, "stream": stream}
	// These are request options only; the website agent owns its tools. Never
	// forward local function definitions or provider-specific continuation state.
	for _, key := range []string{"temperature", "top_p", "max_tokens", "max_completion_tokens", "stop", "seed", "presence_penalty", "frequency_penalty", "reasoning_effort"} {
		if value, ok := input.Options[key]; ok {
			body[key] = value
		}
	}
	return body, nil
}

type chat01StreamNormalizer struct {
	parser       *chat01parse.Parser
	result       *portllm.GenerateOutput
	onEvent      func(portllm.GenerateStreamEvent) error
	tools        map[string]*chat01ToolRecord
	toolOrder    []*chat01ToolRecord
	raw          strings.Builder
	visible      strings.Builder
	reasoning    strings.Builder
	presentation chat01AnswerPresentation
}

type chat01ToolRecord struct {
	index    int
	key      string
	text     strings.Builder
	lastSent int
}

func newChat01Normalizer(result *portllm.GenerateOutput, onEvent func(portllm.GenerateStreamEvent) error) *chat01StreamNormalizer {
	result.ContentNormalized = true
	return &chat01StreamNormalizer{parser: chat01parse.NewParser(), result: result, onEvent: onEvent, tools: make(map[string]*chat01ToolRecord)}
}
func (n *chat01StreamNormalizer) event(event portllm.GenerateStreamEvent) error {
	event.ContentNormalized = true
	if n.onEvent != nil {
		return n.onEvent(event)
	}
	return nil
}
func (n *chat01StreamNormalizer) feed(text string) error {
	if text == "" {
		return nil
	}
	if n.raw.Len()+len(text) > maxUpstreamBodyBytes {
		return errors.New("Chat01 response exceeds limit")
	}
	n.raw.WriteString(text)
	return n.apply(n.parser.Feed(text))
}
func (n *chat01StreamNormalizer) apply(deltas []chat01parse.Delta) error {
	for _, delta := range deltas {
		switch string(delta.Kind) {
		case "answer":
			text := n.presentation.feed(delta.Text, false)
			n.visible.WriteString(text)
			if text != "" {
				if err := n.event(portllm.GenerateStreamEvent{Delta: text}); err != nil {
					return err
				}
			}
		case "reasoning":
			n.reasoning.WriteString(delta.Text)
			if err := n.event(portllm.GenerateStreamEvent{Reasoning: &portllm.ReasoningDelta{EventType: "reasoning.delta", ItemID: delta.BlockID, Kind: "text", Text: delta.Text}}); err != nil {
				return err
			}
		case "python", "shell", "code-fragment", "search", "tool-note":
			record, found := n.tools[delta.BlockID]
			if !found {
				record = &chat01ToolRecord{index: len(n.result.ServerToolCalls), key: "code"}
				n.tools[delta.BlockID] = record
				n.toolOrder = append(n.toolOrder, record)
				typeName := "code_interpreter"
				if string(delta.Kind) == "shell" {
					typeName = "shell"
					record.key = "command"
				}
				if string(delta.Kind) == "search" {
					typeName = "web_search"
					record.key = "record"
				}
				if string(delta.Kind) == "tool-note" {
					typeName = "chat01_activity"
					record.key = "record"
				}
				n.result.ServerToolCalls = append(n.result.ServerToolCalls, portllm.ToolCall{ToolCallID: "chat01:" + delta.BlockID, ToolType: typeName, ToolName: typeName, Status: "incomplete"})
			}
			record.text.WriteString(delta.Text)
			// A trace event is a cumulative snapshot. Grow the emission interval
			// with the record to avoid quadratic copying of long code outputs.
			if !found || delta.Complete || record.text.Len()-record.lastSent >= max(256, record.text.Len()/8) {
				if err := n.flushTool(record); err != nil {
					return err
				}
			}
		case "component":
			if delta.Complete {
				for _, block := range n.parser.Blocks() {
					if block.ID == delta.BlockID {
						fallback := chat01parse.RenderComponentFallback(block)
						if fallback != "" {
							if err := n.flushPresentation(); err != nil {
								return err
							}
							fallback = "\n\n" + fallback + "\n\n"
							n.visible.WriteString(fallback)
							if err := n.event(portllm.GenerateStreamEvent{Delta: fallback}); err != nil {
								return err
							}
						}
						break
					}
				}
			}
		}
	}
	return nil
}
func (n *chat01StreamNormalizer) flushTool(record *chat01ToolRecord) error {
	if record.lastSent == record.text.Len() && n.result.ServerToolCalls[record.index].ArgumentsJSON != "" {
		return nil
	}
	call := &n.result.ServerToolCalls[record.index]
	encoded, _ := json.Marshal(map[string]string{record.key: record.text.String(), "provider": "chat01"})
	call.ArgumentsJSON = string(encoded)
	record.lastSent = record.text.Len()
	copy := *call
	return n.event(portllm.GenerateStreamEvent{ServerToolCall: &copy})
}
func (n *chat01StreamNormalizer) flushPresentation() error {
	if tail := n.presentation.feed("", true); tail != "" {
		n.visible.WriteString(tail)
		return n.event(portllm.GenerateStreamEvent{Delta: tail})
	}
	return nil
}
func (n *chat01StreamNormalizer) finish() error {
	// Preserve already delivered content even if a disconnected consumer rejects
	// the final buffered event. A partial run must not become an empty reply.
	defer func() {
		n.result.Text = n.visible.String()
		if n.reasoning.Len() > 0 {
			if n.result.Reasoning == nil {
				n.result.Reasoning = &portllm.ReasoningOutput{}
			}
			n.result.Reasoning.Text = n.reasoning.String()
		}
	}()
	if err := n.apply(n.parser.Finish()); err != nil {
		return err
	}
	if err := n.flushPresentation(); err != nil {
		return err
	}
	for _, record := range n.toolOrder {
		if err := n.flushTool(record); err != nil {
			return err
		}
	}
	for _, block := range n.parser.Blocks() {
		if string(block.Kind) == "component" && !block.Complete {
			fallback := "\n\n" + chat01parse.RenderComponentFallback(block) + "\n\n"
			n.visible.WriteString(fallback)
			if err := n.event(portllm.GenerateStreamEvent{Delta: fallback}); err != nil {
				return err
			}
		}
	}
	n.result.Text = n.visible.String()
	if n.reasoning.Len() > 0 {
		n.result.Reasoning = &portllm.ReasoningOutput{Text: n.reasoning.String()}
		for _, block := range n.parser.Blocks() {
			if string(block.Kind) == "reasoning" && block.Text != "" {
				n.result.Reasoning.Segments = append(n.result.Reasoning.Segments, portllm.ReasoningOutput{ItemID: block.ID, Text: block.Text})
			}
		}
	}
	for _, block := range n.parser.Blocks() {
		if string(block.Kind) == "reasoning" && block.Text != "" {
			n.result.ProcessOrder = append(n.result.ProcessOrder, portllm.ProcessItemReference{ReasoningItemID: block.ID})
		} else if record := n.tools[block.ID]; record != nil {
			n.result.ProcessOrder = append(n.result.ProcessOrder, portllm.ProcessItemReference{ToolCallID: n.result.ServerToolCalls[record.index].ToolCallID})
		}
	}
	seen := make(map[string]bool)
	for _, resource := range chat01parse.ResourcesFor(n.parser.Blocks()) {
		if seen[resource.URL] {
			continue
		}
		seen[resource.URL] = true
		switch resource.Kind {
		case "file":
			hash := sha256.Sum256([]byte(resource.URL))
			n.result.GeneratedFiles = append(n.result.GeneratedFiles, portllm.GeneratedFile{ID: "chat01-url:" + hex.EncodeToString(hash[:]), FileName: resource.Name, SourceLinks: []string{resource.URL}})
		case "image":
			n.result.GeneratedImages = append(n.result.GeneratedImages, portllm.GeneratedImage{URL: resource.URL})
		case "source":
			n.result.Citations = append(n.result.Citations, resource.URL)
		}
	}
	if strings.EqualFold(strings.TrimSpace(n.raw.String()), "Attachment preparation failed") {
		return errors.New("Chat01 attachment preparation failed")
	}
	return nil
}

func parseChat01Output(body []byte) (*portllm.GenerateOutput, error) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	result := &portllm.GenerateOutput{ResponseID: getString(parsed["id"]), Usage: parseOpenAICompatibleUsageForAdapter(portllm.AdapterChat01ChatCompletions, parsed)}
	normalizer := newChat01Normalizer(result, nil)
	choices, _ := parsed["choices"].([]any)
	if len(choices) > 0 {
		message := asMap(asMap(choices[0])["message"])
		if err := normalizer.feed(getString(message["content"])); err != nil {
			return nil, err
		}
	}
	if err := normalizer.finish(); err != nil {
		return result, err
	}
	return result, nil
}

func consumeChat01Stream(reader io.Reader, result *portllm.GenerateOutput, onEvent func(portllm.GenerateStreamEvent) error) error {
	normalizer := newChat01Normalizer(result, onEvent)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxUpstreamBodyBytes)
	var lines []string
	terminal := false
	var received int
	dispatch := func() error {
		payload := strings.Join(lines, "\n")
		lines = lines[:0]
		if strings.TrimSpace(payload) == "" {
			return nil
		}
		if strings.TrimSpace(payload) == "[DONE]" {
			terminal = true
			return errStreamDone
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return err
		}
		if err := parseStreamUpstreamError(data, payload); err != nil {
			return err
		}
		if result.ResponseID == "" {
			result.ResponseID = getString(data["id"])
		}
		if data["usage"] != nil {
			result.Usage = parseOpenAICompatibleUsageForAdapter(portllm.AdapterChat01ChatCompletions, data)
		}
		choices, _ := data["choices"].([]any)
		for i, value := range choices {
			if i > 0 {
				break
			}
			choice := asMap(value)
			if finish := getString(choice["finish_reason"]); finish != "" {
				terminal = true
			}
			if delta, ok := choice["delta"]; ok {
				if err := normalizer.feed(getString(asMap(delta)["content"])); err != nil {
					return err
				}
				continue
			}
			// A final message is a snapshot, not another incremental answer.
			if message, ok := choice["message"]; ok {
				text := getString(asMap(message)["content"])
				prefix := normalizer.raw.String()
				if !strings.HasPrefix(text, prefix) {
					return errors.New("Chat01 completion snapshot conflicts with streamed content")
				}
				if err := normalizer.feed(strings.TrimPrefix(text, prefix)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var streamErr error
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		received += len(line)
		if received > maxUpstreamBodyBytes {
			streamErr = errors.New("Chat01 stream exceeds limit")
			break
		}
		if line == "" {
			if err := dispatch(); err != nil {
				if !errors.Is(err, errStreamDone) {
					streamErr = err
				}
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if streamErr == nil {
		streamErr = scanner.Err()
	}
	if streamErr == nil && len(lines) > 0 {
		if err := dispatch(); err != nil && !errors.Is(err, errStreamDone) {
			streamErr = err
		}
	}
	if err := normalizer.finish(); streamErr == nil {
		streamErr = err
	}
	if streamErr == nil && !terminal {
		streamErr = io.ErrUnexpectedEOF
	}
	return streamErr
}

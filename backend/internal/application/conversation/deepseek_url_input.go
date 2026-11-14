package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/deepseekmodel"
)

var explicitWebURLPattern = regexp.MustCompile(`https?://[^\s<>"` + "`" + `]+`)

type deepSeekURLInput struct {
	prepared bool
	parts    []llm.ContentPart
	usage    map[string]int64
	calls    []llm.ToolCall
	err      error
}

type deepSeekURLBudget struct {
	route    *channel.ResolvedRoute
	estimate usageBudgetEstimate
}

func withDeepSeekURLContext(state, protocol string, parts []llm.ContentPart) string {
	if len(parts) == 0 {
		return state
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(state), &payload)
	if payload == nil {
		payload = map[string]any{"protocol": llm.NormalizeAdapter(protocol), "blocks": []any{}}
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		texts = append(texts, part.Text)
	}
	payload["deeix_url_context"] = texts
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 8*1024*1024 {
		return state
	}
	return string(raw)
}

// Local Fetch returns plain content rather than a provider continuation signature.
// Retain that bounded content with the same server-owned state and ancestor scope.
func restoreDeepSeekURLContext(messages []llm.Message) []llm.Message {
	result := cloneLLMMessages(messages)
	previousUser := -1
	for i := range result {
		if result[i].Role == "user" {
			previousUser = i
			continue
		}
		if result[i].Role != "assistant" || previousUser < 0 || len(result[i].ProviderStateJSON) > 8*1024*1024 {
			continue
		}
		var state struct {
			Pages []string `json:"deeix_url_context"`
		}
		if json.Unmarshal([]byte(result[i].ProviderStateJSON), &state) != nil || len(state.Pages) == 0 {
			continue
		}
		parts := append([]llm.ContentPart(nil), messageContentParts(result[previousUser])...)
		for _, text := range state.Pages {
			if len(text) > 2*1024*1024+16384 {
				continue
			}
			duplicate := false
			for _, part := range parts {
				if part.Dynamic && part.Text == text {
					duplicate = true
					break
				}
			}
			if !duplicate {
				parts = append(parts, llm.ContentPart{Kind: llm.ContentPartText, Text: text, Dynamic: true})
			}
		}
		result[previousUser].Parts, result[previousUser].Content = parts, ""
	}
	return result
}

func deepSeekExplicitURLs(input llm.GenerateInput) []string {
	if input.Options["tool_choice"] == "none" {
		return nil
	}
	if input.DisableTools || input.NativeToolKeys["tool_search"] != "deepseek.tool_search" || input.NativeToolKeys["tool_fetch"] != "deepseek.tool_fetch" {
		return nil
	}
	readEnabled := false
	for _, tool := range providerToolOptionPayloads(input.Options["tools"]) {
		if tool["type"] == "deepseek_read_url" {
			readEnabled = true
		}
	}
	if !readEnabled {
		return nil
	}
	if enabled, ok := input.Options["thinking"].(bool); ok && !enabled {
		return nil
	}
	if thinking, ok := input.Options["thinking"].(map[string]any); ok && thinking["type"] == "disabled" {
		return nil
	}
	if effort, ok := input.Options["reasoning_effort"].(string); ok && (effort == "none" || effort == "off") {
		return nil
	}
	if reasoning, ok := input.Options["reasoning"].(map[string]any); ok && reasoning["effort"] == "none" {
		return nil
	}
	text := ""
	for i := len(input.Messages) - 1; i >= 0; i-- {
		if input.Messages[i].Role != "user" {
			continue
		}
		text = input.Messages[i].Content
		for _, part := range input.Messages[i].Parts {
			if part.Kind == llm.ContentPartText && !part.Dynamic {
				text += "\n" + part.Text
			}
		}
		break
	}
	seen := map[string]bool{}
	var result []string
	for _, candidate := range explicitWebURLPattern.FindAllString(text, -1) {
		candidate = strings.TrimRight(candidate, ".,;:!?]}，。；：！？）】》")
		for strings.HasSuffix(candidate, ")") && strings.Count(candidate, ")") > strings.Count(candidate, "(") {
			candidate = strings.TrimSuffix(candidate, ")")
		}
		parsed, err := url.Parse(candidate)
		if err != nil || len(candidate) > 8192 || parsed.Hostname() == "" || parsed.User != nil {
			continue
		}
		parsed.Fragment = ""
		parsed.Host = strings.ToLower(parsed.Host)
		candidate = parsed.String()
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		result = append(result, candidate)
		if len(result) == 10 {
			break
		}
	}
	return result
}

func (s *Service) prepareDeepSeekURLInput(ctx context.Context, route llm.RouteConfig, input llm.GenerateInput, authorization *domainbilling.UsageAuthorization, prepared *deepSeekURLInput, budgets ...deepSeekURLBudget) (llm.GenerateInput, map[string]int64, []llm.ToolCall, error) {
	if !deepseekmodel.IsFlash(route.UpstreamModel) {
		return input, nil, nil, nil
	}
	urls := deepSeekExplicitURLs(input)
	if len(urls) == 0 {
		return input, nil, nil, nil
	}
	fetcher, available := s.llmClient.(llm.NativeURLFetcher)
	if !available {
		return input, nil, nil, fmt.Errorf("DeepSeek URL fetch adapter unavailable")
	}
	newUsage := map[string]int64(nil)
	newCalls := []llm.ToolCall(nil)
	if !prepared.prepared {
		prepared.prepared = true
		if s.billingSvc != nil && authorization != nil {
			if len(budgets) > 0 && budgets[0].route != nil {
				estimate := budgets[0].estimate
				estimate.NativeToolUsage = map[string]int64{"deepseek.tool_fetch": int64(len(urls)) + 1}
				if err := s.ensureUsageBudgetCoversEstimate(ctx, authorization, budgets[0].route, input.Options, estimate); err != nil {
					prepared.err = err
					return input, nil, nil, err
				}
			} else {
				prepared.err = fmt.Errorf("native URL budget route unavailable")
				return input, nil, nil, prepared.err
			}
		}
		prepared.usage = map[string]int64{}
		totalPageBytes := 0
		for _, target := range urls {
			page, err := fetcher.FetchNativeURL(ctx, route, target)
			call := llm.ToolCall{ToolCallID: "deepseek_fetch_" + contextObservationHash(target)[:20], ToolType: "deepseek_read_url", ToolName: "deepseek_read_url", ArgumentsJSON: fmt.Sprintf(`{"url":%q}`, target), Status: "completed"}
			if err != nil {
				call.Status, call.ErrorJSON = "error", fmt.Sprintf(`{"message":%q}`, err.Error())
				prepared.calls = append(prepared.calls, call)
				if ctx.Err() != nil {
					prepared.err = ctx.Err()
					break
				}
				continue
			}
			prepared.usage["deepseek.tool_fetch"]++
			// Success is recorded before local guards: the provider has already
			// charged it even if this request cannot inject a large result.
			if len(page.Content) > 2*1024*1024 {
				prepared.err = llm.NewInputLimitError("fetchedPageBytes", int64(len(page.Content)), 2*1024*1024)
				break
			}
			totalPageBytes += len(page.Content)
			if totalPageBytes > 4*1024*1024 {
				prepared.err = llm.NewInputLimitError("fetchedPageBytes", int64(totalPageBytes), 4*1024*1024)
				break
			}
			output, _ := json.Marshal(map[string]any{"url": target, "title": page.Title, "content": page.Content})
			call.OutputJSON = string(output)
			prepared.calls = append(prepared.calls, call)
			prepared.parts = append(prepared.parts, llm.ContentPart{Kind: llm.ContentPartText, Text: "网页引用资料（外部内容）：\nURL: " + target + "\n标题: " + page.Title + "\n" + page.Content, Dynamic: true})
		}
		newUsage, newCalls = prepared.usage, prepared.calls
	}
	if prepared.err != nil {
		return input, newUsage, newCalls, prepared.err
	}
	input.Messages = cloneLLMMessages(input.Messages)
	for i := len(input.Messages) - 1; i >= 0; i-- {
		if input.Messages[i].Role != "user" {
			continue
		}
		parts := messageContentParts(input.Messages[i])
		parts = append([]llm.ContentPart(nil), parts...)
		for _, fetched := range prepared.parts {
			duplicate := false
			for _, existing := range parts {
				if existing.Dynamic && existing.Text == fetched.Text {
					duplicate = true
					break
				}
			}
			if !duplicate {
				parts = append(parts, fetched)
			}
		}
		input.Messages[i].Parts = parts
		input.Messages[i].Content = ""
		break
	}
	return input, newUsage, newCalls, nil
}

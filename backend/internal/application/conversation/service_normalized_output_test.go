package conversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestNormalizedAnswerPreservesThinkTagExamples(t *testing.T) {
	text := "Explain the literal tag <think>sample text</think> in this example."
	output := &llm.GenerateOutput{ContentNormalized: true, Text: text}
	recorder := &messageTraceRecorder{cfg: config.Config{ProcessTraceEnabled: true}, ephemeral: true, assistant: &model.Message{RunID: "run"}}
	if got := syncUpstreamOutputThinking(recorder, output); got != text {
		t.Fatalf("normalized answer reclassified: %q", got)
	}
	if got := outputReasoningContent(output); got != "" {
		t.Fatalf("tutorial counted as reasoning: %q", got)
	}
	if recorder.upstreamThink != nil {
		t.Fatal("tutorial produced a fake reasoning event")
	}
}

type normalizedAnswerGateway struct {
	temporaryLLMGatewayStub
	chunks []string
}

func (g *normalizedAnswerGateway) GenerateStream(_ context.Context, _ llm.RouteConfig, _ llm.GenerateInput, onEvent func(llm.GenerateStreamEvent) error) (*llm.GenerateOutput, error) {
	text := ""
	for _, chunk := range g.chunks {
		text += chunk
		if err := onEvent(llm.GenerateStreamEvent{Delta: chunk, ContentNormalized: true}); err != nil {
			return nil, err
		}
	}
	return &llm.GenerateOutput{Text: text, ContentNormalized: true}, nil
}

func TestNormalizedStreamBypassesGenericThinkingTagRouter(t *testing.T) {
	text := "Tutorial: <think>literal example</think> remains visible."
	gateway := &normalizedAnswerGateway{chunks: []string{"Tutorial: <thi", "nk>literal example</th", "ink> remains visible."}}
	received := ""
	recorder := &messageTraceRecorder{cfg: config.Config{ProcessTraceEnabled: true}, ephemeral: true, assistant: &model.Message{RunID: "run"}}
	runner := &messageGenerationRunner{service: &Service{llmClient: gateway}, traceRecorder: recorder, preferStream: true, onDelta: func(delta string) error { received += delta; return nil }, maxLLMCalls: 1, usage: &messageUsageAccumulator{}, routeConfig: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}}
	output, err := runner.generate(t.Context(), llm.GenerateInput{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if received != text || output.Text != text || recorder.upstreamThink != nil {
		t.Fatalf("normalized stream reclassified: received=%q output=%q think=%#v", received, output.Text, recorder.upstreamThink)
	}
}

func TestReasoningSegmentsReconcileToTheirOwnStreamedItems(t *testing.T) {
	recorder := &messageTraceRecorder{cfg: config.Config{ProcessTraceEnabled: true}, ephemeral: true, assistant: &model.Message{RunID: "run"}}
	recorder.appendUpstreamReasoning(messageTraceThinkKindContent, "first", reasoningPayload(&llm.ReasoningDelta{ItemID: "block_first", Kind: "text"}))
	recorder.completeUpstreamThink()
	firstID := recorder.upstreamThink.eventID
	recorder.appendUpstreamReasoning(messageTraceThinkKindContent, "second", reasoningPayload(&llm.ReasoningDelta{ItemID: "block_second", Kind: "text"}))
	recorder.completeUpstreamThink()
	secondID := recorder.upstreamThink.eventID
	output := &llm.GenerateOutput{ContentNormalized: true, Text: "answer", Reasoning: &llm.ReasoningOutput{Text: "first finalsecond final", Segments: []llm.ReasoningOutput{{ItemID: "block_first", Text: "first final"}, {ItemID: "block_second", Text: "second final"}}}}
	finalizeStreamingOutputTrace(recorder, output, "run", nil)
	events := traceEventsByType(recorder.events, "think")
	if len(events) != 2 || events[0].EventID != firstID || events[1].EventID != secondID || events[0].ContentMarkdown != "first final" || events[1].ContentMarkdown != "second final" {
		t.Fatalf("reasoning segments merged/duplicated: %#v", events)
	}
	if recorder.upstreamThink.eventID != secondID {
		t.Fatal("reconciliation changed the current reasoning item")
	}
}

func TestNonstreamReasoningSegmentsRemainSeparate(t *testing.T) {
	recorder := &messageTraceRecorder{cfg: config.Config{ProcessTraceEnabled: true}, ephemeral: true, assistant: &model.Message{RunID: "run"}}
	output := &llm.GenerateOutput{ContentNormalized: true, Text: "answer", Reasoning: &llm.ReasoningOutput{Text: "firstsecond", Segments: []llm.ReasoningOutput{{ItemID: "first", Text: "first"}, {ItemID: "second", Text: "second"}}}}
	if got := syncUpstreamOutputThinking(recorder, output); got != "answer" {
		t.Fatal(got)
	}
	events := traceEventsByType(recorder.events, "think")
	if len(events) != 2 || events[0].ContentMarkdown != "first" || events[1].ContentMarkdown != "second" {
		t.Fatalf("nonstream reasoning aggregated: %#v", events)
	}
}

func TestNonstreamProcessOrderMatchesStreamedReasoningAndTools(t *testing.T) {
	newRecorder := func() *messageTraceRecorder {
		return &messageTraceRecorder{cfg: config.Config{ProcessTraceEnabled: true}, ephemeral: true, assistant: &model.Message{RunID: "run"}}
	}
	segments := []llm.ReasoningOutput{{ItemID: "first", Text: "first thought"}, {ItemID: "second", Text: "second thought"}}
	calls := []llm.ToolCall{{ToolCallID: "chat01:first_tool", ToolType: "code_interpreter", ToolName: "code_interpreter", Status: "incomplete", ArgumentsJSON: `{"provider":"chat01","code":"print(1)"}`}, {ToolCallID: "chat01:second_tool", ToolType: "shell", ToolName: "shell", Status: "incomplete", ArgumentsJSON: `{"provider":"chat01","command":"echo 2"}`}}
	output := &llm.GenerateOutput{ContentNormalized: true, Text: "answer", Reasoning: &llm.ReasoningOutput{Text: "first thoughtsecond thought", Segments: segments}, ServerToolCalls: calls,
		ProcessOrder: []llm.ProcessItemReference{{ReasoningItemID: "first"}, {ToolCallID: "chat01:first_tool"}, {ReasoningItemID: "second"}, {ToolCallID: "chat01:second_tool"}}}
	nonstream := newRecorder()
	text, rows := syncUpstreamOutputTrace(nonstream, output, "run")
	nonstream.complete()
	if text != "answer" || len(rows) != 2 || rows[0].InputJSON != calls[0].ArgumentsJSON || rows[1].InputJSON != calls[1].ArgumentsJSON {
		t.Fatalf("lost answer or persistent rows: %q %#v", text, rows)
	}
	stream := newRecorder()
	observed := make(map[string]string)
	for index, segment := range segments {
		stream.appendUpstreamReasoning(messageTraceThinkKindContent, segment.Text, reasoningPayload(&llm.ReasoningDelta{ItemID: segment.ItemID, Kind: "text"}))
		syncStreamingServerToolTrace(stream, calls[index], "run", observed)
	}
	finalizeStreamingOutputTrace(stream, output, "run", observed)
	stream.complete()
	processOrder := func(recorder *messageTraceRecorder) string {
		var order []string
		for _, event := range recorder.events {
			if event.EventType != "think" && event.EventType != "tool" {
				continue
			}
			var payload tracePayload
			if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
				t.Fatal(err)
			}
			if event.EventType == "think" {
				order = append(order, "reasoning:"+payload.ReasoningItemID())
			} else {
				for _, call := range payload.ToolCalls {
					order = append(order, "tool:"+call.ToolCallID)
				}
			}
		}
		return strings.Join(order, ",")
	}
	expected := "reasoning:first,tool:chat01:first_tool,reasoning:second,tool:chat01:second_tool"
	if got := processOrder(nonstream); got != expected {
		t.Fatalf("nonstream order changed: %s", got)
	}
	if got := processOrder(stream); got != expected {
		t.Fatalf("stream and nonstream order differ: %s", got)
	}
	for _, event := range traceEventsByType(nonstream.events, "think") {
		if event.ContentMarkdown == output.Reasoning.Text {
			t.Fatal("aggregate reasoning replayed after ordered segments")
		}
	}
}

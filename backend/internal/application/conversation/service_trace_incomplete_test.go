package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestToolTracePreservesIncompleteAndUnknownProcessRecords(t *testing.T) {
	for _, status := range []string{"incomplete", "unknown", ""} {
		t.Run(status, func(t *testing.T) {
			summary, markdown, payload := buildToolTrace([]model.ToolCall{{ToolCallID: "chat01:block", ToolType: "code_interpreter", ToolName: "code_interpreter", Status: status, InputJSON: `{"provider":"chat01","code":"print(1)"}`}})
			if strings.Contains(summary, "已完成") || strings.Contains(markdown, "已完成") || strings.Contains(summary, "失败") || strings.Contains(markdown, "失败") {
				t.Fatalf("unsupported execution claim: %q %q", summary, markdown)
			}
			if !strings.Contains(markdown, "可查看代码和已有信息") || len(payload.ToolCalls) != 1 || !strings.Contains(payload.ToolCalls[0].InputDetail, "print(1)") || payload.ToolCalls[0].OutputDetail != "" {
				t.Fatalf("missing available process detail: %#v %s", payload, markdown)
			}
			expected := status
			if expected == "" {
				expected = "unknown"
			}
			if payload.ToolCalls[0].Status != expected {
				t.Fatalf("lost status: %#v", payload.ToolCalls[0])
			}
			if aggregate := summarizeToolTracePayload(payload); aggregate != summary {
				t.Fatalf("live/final summaries differ: %q %q", summary, aggregate)
			}
			if status == "incomplete" && !strings.Contains(summary, "结果未完整提供") {
				t.Fatalf("missing partial result notice: %s", summary)
			}
		})
	}
}

func TestIncompleteToolTraceReconcilesWithoutCompletingOrDuplicatingCall(t *testing.T) {
	recorder := &messageTraceRecorder{cfg: config.Config{ProcessTraceEnabled: true}, ephemeral: true, assistant: &model.Message{RunID: "run"}}
	observed := make(map[string]string)
	call := llm.ToolCall{ToolCallID: "chat01:block", ToolType: "shell", ToolName: "shell", Status: "incomplete", ArgumentsJSON: `{"provider":"chat01","command":"echo"}`}
	syncStreamingServerToolTrace(recorder, call, "run", observed)
	call.ArgumentsJSON = `{"provider":"chat01","command":"echo hello"}`
	syncStreamingServerToolTrace(recorder, call, "run", observed)
	finalizeStreamingOutputTrace(recorder, &llm.GenerateOutput{ServerToolCalls: []llm.ToolCall{call}}, "run", observed)
	recorder.complete()
	if len(recorder.tools.payload.ToolCalls) != 1 {
		t.Fatalf("process duplicated: %#v", recorder.tools.payload.ToolCalls)
	}
	record := recorder.tools.payload.ToolCalls[0]
	if record.Status != "incomplete" || !strings.Contains(record.InputDetail, "echo hello") || record.OutputDetail != "" {
		t.Fatalf("process altered: %#v", record)
	}
	if strings.Contains(recorder.tools.summary, "已完成") || !strings.Contains(recorder.tools.summary, "结果未完整提供") {
		t.Fatalf("final trace misstates execution: %s", recorder.tools.summary)
	}
}

func TestChat01FinalProcessSnapshotDoesNotRepeatEarlierReasoningRound(t *testing.T) {
	recorder := &messageTraceRecorder{cfg: config.Config{ProcessTraceEnabled: true}, ephemeral: true, assistant: &model.Message{RunID: "run"}}
	observed := make(map[string]string)
	calls := []llm.ToolCall{{ToolCallID: "chat01:first_code", ToolType: "code_interpreter", ToolName: "code_interpreter", Status: "incomplete", ArgumentsJSON: `{"code":"print(1)","provider":"chat01"}`}, {ToolCallID: "chat01:second_code", ToolType: "code_interpreter", ToolName: "code_interpreter", Status: "incomplete", ArgumentsJSON: `{"code":"print(2)","provider":"chat01"}`}}
	for i, call := range calls {
		recorder.appendUpstreamReasoning(messageTraceThinkKindContent, fmt.Sprintf("reason %d", i), reasoningPayload(&llm.ReasoningDelta{ItemID: fmt.Sprint(i), Kind: "text"}))
		syncStreamingServerToolTrace(recorder, call, "run", observed)
	}
	finalizeStreamingOutputTrace(recorder, &llm.GenerateOutput{ServerToolCalls: calls}, "run", observed)
	recorder.complete()
	counts := make(map[string]int)
	for _, event := range traceEventsByType(recorder.events, "tool") {
		var payload tracePayload
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		for _, call := range payload.ToolCalls {
			counts[call.ToolCallID]++
		}
	}
	if len(counts) != 2 || counts[calls[0].ToolCallID] != 1 || counts[calls[1].ToolCallID] != 1 {
		t.Fatalf("process records repeated across rounds: %#v", counts)
	}
}

func TestPartialToolTraceSummaryKeepsKnownAndUnknownStatesSeparate(t *testing.T) {
	summary, _, payload := buildToolTrace([]model.ToolCall{{ToolCallID: "one", Status: "success"}, {ToolCallID: "two", Status: "incomplete"}, {ToolCallID: "three", Status: "unknown"}})
	if !strings.Contains(summary, "已记录 3 段工具过程") || !strings.Contains(summary, "1 次已完成") || !strings.Contains(summary, "1 段结果未完整提供") || strings.Contains(summary, "3 次工具调用已完成") {
		t.Fatal(summary)
	}
	if summarizeToolTracePayload(payload) != summary {
		t.Fatal("merged summary differs")
	}
}

func TestServerSearchDoesNotAssignUnrelatedResponseCitations(t *testing.T) {
	cases := []struct {
		name, status, id, args string
		wantOutput             bool
	}{
		{name: "legacy completed search", status: "completed", id: "search_1", wantOutput: true},
		{name: "incomplete search", status: "incomplete", id: "search_2"},
		{name: "Chat01 stable identity", status: "completed", id: "chat01:block"},
		{name: "Chat01 explicit provider", status: "success", id: "block", args: `{"provider":"chat01"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := upstreamServerToolCallRows(&llm.GenerateOutput{Citations: []string{"https://example.test/final-answer-source"}, ServerToolCalls: []llm.ToolCall{{ToolCallID: tc.id, ToolType: "web_search", ToolName: "web_search", Status: tc.status, ArgumentsJSON: tc.args}}}, "run")
			if len(rows) != 1 || (rows[0].OutputJSON != "") != tc.wantOutput {
				t.Fatalf("unrelated source paired with process: %#v", rows)
			}
		})
	}
}

type temporaryServerTraceGateway struct {
	temporaryLLMGatewayStub
	output *llm.GenerateOutput
	calls  int
	stream bool
}

func (g *temporaryServerTraceGateway) GenerateStream(_ context.Context, _ llm.RouteConfig, _ llm.GenerateInput, onEvent func(llm.GenerateStreamEvent) error) (*llm.GenerateOutput, error) {
	g.calls++
	if g.stream {
		for _, call := range g.output.ServerToolCalls {
			if err := onEvent(llm.GenerateStreamEvent{ServerToolCall: &call}); err != nil {
				return g.output, err
			}
		}
	}
	return g.output, nil
}

func TestTemporaryChatRetainsServerProcessRecordsOnce(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(map[bool]string{true: "streamed", false: "final snapshot only"}[stream], func(t *testing.T) {
			gateway := &temporaryServerTraceGateway{stream: stream, output: &llm.GenerateOutput{Text: "answer", ServerToolCalls: []llm.ToolCall{{ToolCallID: "chat01:code", ToolType: "code_interpreter", ToolName: "code_interpreter", Status: "incomplete", ArgumentsJSON: `{"provider":"chat01","code":"print(42)"}`}}}}
			service := &Service{cfg: config.NewRuntime(config.Config{ProcessTraceEnabled: true}), llmClient: gateway, routeResolver: &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{"chat01": {PlatformModelName: "chat01", UpstreamModel: "agent", Protocol: "chat01_chat_completions"}}}}
			result, err := service.StreamTemporaryChat(t.Context(), TemporaryChatInput{UserID: 1, SessionID: "session", ClientRunID: "run", Model: "chat01", Messages: []TemporaryChatMessage{{Role: "user", Content: "task"}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			trace := result.AssistantMessage.ProcessTrace
			if trace == nil || trace.Tools == nil || gateway.calls != 1 {
				t.Fatalf("missing process trace/call count: %#v, calls=%d", trace, gateway.calls)
			}
			if strings.Contains(trace.Tools.Summary, "已完成") || !strings.Contains(trace.Tools.Summary, "结果未完整提供") {
				t.Fatal(trace.Tools.Summary)
			}
			payload := tracePayload{}
			if err := json.Unmarshal([]byte(trace.Tools.PayloadJSON), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.ToolCalls) != 1 || !strings.Contains(payload.ToolCalls[0].InputDetail, "print(42)") || payload.ToolCalls[0].Status != "incomplete" {
				t.Fatalf("process lost/duplicated: %#v", payload)
			}
		})
	}
}

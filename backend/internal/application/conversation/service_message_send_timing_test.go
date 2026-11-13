package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestSendLatencyTimingPreparationStagesAndDispatch(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	start := time.Unix(100, 0)
	timing := newSendLatencyTiming(context.Background(), zap.New(core), SendMessageInput{
		RequestID: "request-1", ConversationID: 42, Content: "private prompt",
	}, "run-1", start)
	timing.enter(sendPreparationRoute, start.Add(300*time.Millisecond))
	timing.enter(sendPreparationHistory, start.Add(700*time.Millisecond))
	timing.beginUpstream(start.Add(1500*time.Millisecond), 1, "openai", true)
	// Dispatch freezes preparation; later tool turns must not extend it.
	timing.enter(sendPreparationPrompt, start.Add(5*time.Second))
	timing.finishPreparation(start.Add(10*time.Second), true)
	entries := logs.All()
	if len(entries) != 1 || entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("expected one slow preparation warning, got %v", entries)
	}
	fields := entries[0].ContextMap()
	for key, want := range map[string]any{
		"request_id": "request-1", "run_id": "run-1", "prep_total_ms": int64(1500),
		"claim_ms": int64(300), "route_ms": int64(400), "file_capability_history_ms": int64(800), "failed": false,
	} {
		if fields[key] != want {
			t.Errorf("%s = %v, want %v", key, fields[key], want)
		}
	}
	if _, exists := fields["content"]; exists {
		t.Fatal("timing log must not contain message contents")
	}
}

func TestSendLatencyTimingPreparationFailureIncludesActiveStage(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	start := time.Unix(100, 0)
	timing := newSendLatencyTiming(context.Background(), zap.New(core), SendMessageInput{}, "run-2", start)
	timing.enter(sendPreparationNativeHydrate, start.Add(20*time.Millisecond))
	timing.finishPreparation(start.Add(70*time.Millisecond), true)
	entry := logs.All()[0]
	fields := entry.ContextMap()
	if entry.Level != zapcore.WarnLevel || fields["failed"] != true || fields["last_stage"] != "native_hydrate" || fields["native_hydrate_ms"] != int64(50) {
		t.Fatalf("unexpected failure timing: %v", fields)
	}
}

func TestUpstreamFirstEventTimingOncePerActualRequest(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	start := time.Unix(100, 0)
	timing := newSendLatencyTiming(context.Background(), zap.New(core), SendMessageInput{RequestID: "req"}, "run", start)
	first := timing.beginUpstream(start.Add(100*time.Millisecond), 1, "openai", true)
	first.record(start.Add(2100*time.Millisecond), "reasoning", false)
	first.record(start.Add(3*time.Second), "text", false)
	first.record(start.Add(4*time.Second), "none", true)
	// A retry/follow-up owns its own observer and clock.
	second := timing.beginUpstream(start.Add(5*time.Second), 2, "openai", false)
	second.record(start.Add(5100*time.Millisecond), "response", false)
	entries := logs.FilterMessage("conversation_upstream_first_event").All()
	if len(entries) != 2 {
		t.Fatalf("expected one observation per request, got %d", len(entries))
	}
	firstFields, secondFields := entries[0].ContextMap(), entries[1].ContextMap()
	if firstFields["wait_ms"] != int64(2000) || firstFields["elapsed_ms"] != int64(2100) || firstFields["llm_request_attempt"] != int64(1) || firstFields["first_event"] != "reasoning" || entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("unexpected first observation: %v", firstFields)
	}
	if secondFields["wait_ms"] != int64(100) || secondFields["llm_request_attempt"] != int64(2) || entries[1].Level != zapcore.DebugLevel {
		t.Fatalf("unexpected second observation: %v", secondFields)
	}
}

func TestUpstreamFirstEventTimingNoEventAndBackwardClock(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	start := time.Unix(100, 0)
	timing := newSendLatencyTiming(context.Background(), zap.New(core), SendMessageInput{}, "run", start)
	attempt := timing.beginUpstream(start, 1, "openai", true)
	attempt.record(start.Add(-time.Second), "none", true)
	entry := logs.FilterMessage("conversation_upstream_first_event").All()[0]
	fields := entry.ContextMap()
	if fields["wait_ms"] != int64(0) || fields["elapsed_ms"] != int64(0) || fields["first_event"] != "none" || fields["failed"] != true || entry.Level != zapcore.WarnLevel {
		t.Fatalf("unexpected no-event failure: %v", fields)
	}
	// Existing tests create runners without timing; observability stays optional.
	var disabled *sendLatencyTiming
	disabled.beginUpstream(start, 1, "openai", true).record(start, "none", true)
}

func TestStreamEventTimingKindMetadataDoesNotClaimThinking(t *testing.T) {
	if got := streamEventTimingKind(llm.GenerateStreamEvent{ResponseID: "resp-private"}); got != "metadata" {
		t.Fatalf("metadata event classified as %q", got)
	}
	if got := streamEventTimingKind(llm.GenerateStreamEvent{Delta: "private text"}); got != "text" {
		t.Fatalf("text event classified as %q", got)
	}
}

type latencyTimingGateway struct {
	temporaryLLMGatewayStub
	streamSupported bool
}

func (g *latencyTimingGateway) GenerateStream(_ context.Context, _ llm.RouteConfig, _ llm.GenerateInput, onEvent func(llm.GenerateStreamEvent) error) (*llm.GenerateOutput, error) {
	if !g.streamSupported {
		return nil, &llm.UpstreamError{StatusCode: 405, Message: "method not allowed"}
	}
	for _, event := range []llm.GenerateStreamEvent{{ResponseID: "private-response-id"}, {Delta: "first"}, {Delta: "second"}} {
		if err := onEvent(event); err != nil {
			return nil, err
		}
	}
	return &llm.GenerateOutput{}, nil
}

func (g *latencyTimingGateway) Generate(context.Context, llm.RouteConfig, llm.GenerateInput) (*llm.GenerateOutput, error) {
	return &llm.GenerateOutput{Text: "answer"}, nil
}

func TestGenerationLatencyTimingStreamAndFallbackAttempts(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "stream", false: "fallback"}[supported], func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			input := SendMessageInput{RequestID: "request-1"}
			runner := &messageGenerationRunner{
				service: &Service{llmClient: &latencyTimingGateway{streamSupported: supported}},
				input:   input, preferStream: true, onDelta: func(string) error { return nil },
				maxLLMCalls: 3, usage: &messageUsageAccumulator{}, routeConfig: llm.RouteConfig{Protocol: llm.AdapterOpenAIResponses},
				latencyTiming: newSendLatencyTiming(context.Background(), zap.New(core), input, "run-1", time.Now()),
			}
			if _, err := runner.generate(context.Background(), llm.GenerateInput{}, nil); err != nil {
				t.Fatal(err)
			}
			entries := logs.FilterMessage("conversation_upstream_first_event").All()
			if supported {
				if len(entries) != 1 || entries[0].ContextMap()["first_event"] != "metadata" {
					t.Fatalf("stream must record exactly its first event: %v", entries)
				}
			} else {
				if len(entries) != 2 || entries[0].ContextMap()["first_event"] != "none" || entries[0].ContextMap()["failed"] != true || entries[1].ContextMap()["llm_request_attempt"] != int64(2) || entries[1].ContextMap()["stream"] != false {
					t.Fatalf("fallback must have distinct request attempts: %v", entries)
				}
			}
		})
	}
}

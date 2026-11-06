package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

type preparationPhaseGateway struct {
	temporaryLLMGatewayStub
	check func()
	calls int
}

func (g *preparationPhaseGateway) Generate(context.Context, llm.RouteConfig, llm.GenerateInput) (*llm.GenerateOutput, error) {
	g.calls++
	g.check()
	return &llm.GenerateOutput{Text: "done"}, nil
}

func (g *preparationPhaseGateway) GenerateStream(ctx context.Context, route llm.RouteConfig, input llm.GenerateInput, _ func(llm.GenerateStreamEvent) error) (*llm.GenerateOutput, error) {
	// No upstream events are emitted: preparation must already be completed
	// even when the provider is silent or a parser buffers its entire response.
	return g.Generate(ctx, route, input)
}

func TestPreparationEndsBeforeUpstreamResponseInAllChatModes(t *testing.T) {
	for _, mode := range []string{"stream", "nonstream", "temporary", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			gateway := &preparationPhaseGateway{}
			svc := &Service{cfg: config.NewRuntime(config.Config{ProcessTraceEnabled: true}), llmClient: gateway,
				routeResolver: &textTaskRouteResolverStub{}}
			var emittedCompleted bool
			var recorder *messageTraceRecorder
			recorder = newEphemeralMessageTraceRecorder(svc, t.Context(), &model.Message{}, func(_ string, _ map[string]any) error {
				if recorder.process != nil && recorder.process.status == messageTraceStatusCompleted {
					emittedCompleted = true
				}
				return nil
			})
			recorder.recordPromptTrace(&model.MessagePromptTrace{TotalTokenEstimate: 10})
			gateway.check = func() {
				if !emittedCompleted || recorder.process.status != messageTraceStatusCompleted || recorder.process.endedAt == nil {
					t.Fatal("upstream started while preparation remained active")
				}
			}
			input := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Content: "hello"}}}
			route := llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}
			if mode == "temporary" {
				_, err := svc.runTemporaryGeneration(t.Context(), temporaryGenerationInput{
					Route: &channel.ResolvedRoute{Protocol: route.Protocol}, RouteConfig: route,
					InitialInput: input, TraceRecorder: recorder, StartedAt: time.Now(),
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				runner := &messageGenerationRunner{service: svc, traceRecorder: recorder, routeConfig: route,
					startedAt: time.Now(), usage: &messageUsageAccumulator{}, preferStream: mode != "nonstream", onDelta: func(string) error { return nil }}
				if mode == "rejected" {
					runner.contextBudget = contextBudgetPolicy{limit: 1}
				}
				_, err := runner.generate(t.Context(), input, input.Messages)
				if mode == "rejected" {
					if llm.InputLimitDetails(err) == nil || gateway.calls != 0 || emittedCompleted {
						t.Fatalf("local rejection must not announce completed preparation or call upstream: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if gateway.calls != 1 {
				t.Fatalf("got %d calls, want one", gateway.calls)
			}
		})
	}
}

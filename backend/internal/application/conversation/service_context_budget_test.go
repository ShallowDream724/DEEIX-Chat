package conversation

import (
	"errors"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func requireContextBudgetError(t *testing.T, err error, dimension string) {
	t.Helper()
	var detail *llm.InputLimitError
	if !errors.As(err, &detail) || detail.Dimension != dimension || detail.Actual <= detail.Limit {
		t.Fatalf("expected %s budget error, got %v", dimension, err)
	}
}

func TestOrdinaryContextBudgetRejectsRetainedHistoryBeforeUpstream(t *testing.T) {
	gateway := &contextToolLoopGateway{}
	service := &Service{llmClient: gateway}
	conversation := &model.Conversation{PublicID: "conv", LastResponseID: "previous"}
	gen := testRouteGenerationContext(conversation)
	gen.cfg.ContextTokenBudgetEnabled = true
	route := &channel.ResolvedRoute{
		Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://api.openai.com/v1", UpstreamModel: "test",
		ModelCapabilitiesJSON: `{"contextWindow":4096,"maxOutputTokens":1}`,
	}
	prompt := testRoutePromptPlan()
	prompt.Messages[1].Content = strings.Repeat("old history ", 8_000)
	probe := service.prepareRouteGeneration(t.Context(), routeGenerationPreparationInput{Generation: gen, Route: route, PromptPlan: prompt})
	conversation.LastPromptFingerprint = probe.statefulPrefixFingerprint
	plan := service.prepareRouteGeneration(t.Context(), routeGenerationPreparationInput{Generation: gen, Route: route, PromptPlan: prompt})
	requireContextBudgetError(t, plan.budgetDecision.err(), "contextTokens")
	if len(plan.fullLLMMessages) != len(prompt.Messages) || !plan.statefulContinuation {
		t.Fatalf("plan must retain every message even for a stateful request: %#v", plan.promptShape)
	}
	runner := &messageGenerationRunner{service: service, usage: &messageUsageAccumulator{}, routeConfig: plan.routeConfig}
	runner.contextBudget = plan.contextBudget
	runner.contextMeter = plan.contextMeter
	_, err := runner.generate(t.Context(), plan.generateInput, plan.fullLLMMessages)
	requireContextBudgetError(t, err, "contextTokens")
	if len(gateway.requests) != 0 || runner.upstreamCallStarted {
		t.Fatal("over-budget retained state reached the upstream")
	}

	prompt.Messages[3].Content = strings.Repeat("current turn ", 8_000)
	plan = service.prepareRouteGeneration(t.Context(), routeGenerationPreparationInput{Generation: gen, Route: route, PromptPlan: prompt})
	requireContextBudgetError(t, plan.budgetDecision.err(), "currentTurnTokens")
}

func TestToolFollowUpOverBudgetKeepsHistoryAndPriorUsage(t *testing.T) {
	gateway := &contextToolLoopGateway{}
	runner := &messageGenerationRunner{
		service: &Service{llmClient: gateway}, usage: &messageUsageAccumulator{},
		contextBudget: contextBudgetPolicy{limit: 200}, contextMeter: contextInputMeter{identity: "route"},
	}
	input := llm.GenerateInput{Messages: []llm.Message{
		{Role: "user", Content: strings.Repeat("old question ", 20)},
		{Role: "assistant", Content: "prior answer"},
		{Role: "user", Content: "use a tool"},
	}}
	if _, err := runner.generate(t.Context(), input, nil); err != nil {
		t.Fatalf("initial request: %v", err)
	}
	runner.usage.setObservedUsage(llm.Usage{InputTokens: 100})
	input.Messages = append(input.Messages, llm.Message{Role: "assistant", Content: strings.Repeat("tool call details ", 27)}, llm.Message{Role: "tool"})
	_, err := runner.generate(t.Context(), input, nil)
	requireContextBudgetError(t, err, "contextTokens")
	if len(gateway.requests) != 1 || runner.completedLLMCallCount != 1 || runner.usage.interruptedInputTokens() != 100 {
		t.Fatalf("refused follow-up changed prior usage or made a second call: calls=%d usage=%#v", len(gateway.requests), runner.usage.usage())
	}
}

func TestTemporaryChatCalibratesOnlyMatchingAncestors(t *testing.T) {
	gateway := &temporaryLLMGatewayStub{output: &llm.GenerateOutput{Text: "answer", Usage: llm.Usage{InputTokens: 3999}}}
	route := &channel.ResolvedRoute{
		RouteID: 1, Protocol: llm.AdapterOpenAIChatCompletions, UpstreamModel: "test",
		ModelCapabilitiesJSON: `{"contextWindow":4096,"maxOutputTokens":1}`,
	}
	otherRoute := *route
	otherRoute.RouteID = 2
	service := &Service{
		cfg:           config.NewRuntime(config.Config{ContextTokenBudgetEnabled: true}),
		routeResolver: &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{"test": route, "other": &otherRoute}},
		llmClient:     gateway,
	}
	seed := TemporaryChatInput{
		UserID: 1, SessionID: "session", ClientRunID: "first", Model: "test",
		Messages: []TemporaryChatMessage{{Role: "user", Content: "question"}},
	}
	if _, err := service.StreamTemporaryChat(t.Context(), seed, nil); err != nil {
		t.Fatal(err)
	}

	continuation := seed
	continuation.ClientRunID = "second"
	continuation.Messages = append(append([]TemporaryChatMessage(nil), seed.Messages...),
		TemporaryChatMessage{Role: "assistant", Content: "answer"}, TemporaryChatMessage{Role: "user", Content: "more details"})
	_, err := service.StreamTemporaryChat(t.Context(), continuation, nil)
	requireContextBudgetError(t, err, "contextTokens")
	if len(gateway.inputs) != 1 {
		t.Fatal("matching measured ancestor must block locally")
	}

	for _, test := range []struct {
		name string
		edit func(*TemporaryChatInput)
	}{
		{"retry", func(in *TemporaryChatInput) { in.Messages = in.Messages[:1] }},
		{"edited user", func(in *TemporaryChatInput) { in.Messages[0].Content = "edited question" }},
		{"edited assistant", func(in *TemporaryChatInput) { in.Messages[1].Content = "edited answer" }},
		{"other user", func(in *TemporaryChatInput) { in.UserID = 2 }},
		{"other session", func(in *TemporaryChatInput) { in.SessionID = "other" }},
		{"other route", func(in *TemporaryChatInput) { in.Model = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := continuation
			request.Messages = append([]TemporaryChatMessage(nil), continuation.Messages...)
			test.edit(&request)
			calls := len(gateway.inputs)
			if _, err := service.StreamTemporaryChat(t.Context(), request, nil); err != nil {
				t.Fatalf("incompatible ancestor must fall back to estimation: %v", err)
			}
			if len(gateway.inputs) != calls+1 || len(gateway.inputs[calls].Messages) != len(request.Messages) {
				t.Fatal("fallback must send the complete temporary history")
			}
		})
	}
}

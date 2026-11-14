package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type requestDateRepository struct {
	repository.ConversationRepository
	zone  string
	err   error
	calls int
}

func (r *requestDateRepository) GetUserByID(context.Context, uint) (*domainuser.User, error) {
	r.calls++
	return &domainuser.User{Timezone: r.zone}, r.err
}

func TestRequestDateUsesUserTimezoneAndStableRequestSnapshot(t *testing.T) {
	repo := &requestDateRepository{zone: "Asia/Shanghai"}
	service := &Service{repo: repo}
	resolve := service.requestDateContext(t.Context(), 7, time.Date(2026, 11, 13, 18, 30, 0, 0, time.UTC))
	if repo.calls != 0 {
		t.Fatal("profile resolved eagerly")
	}
	first := resolve()
	if !strings.Contains(first, "2026-11-14 (Saturday)") || !strings.Contains(first, "Asia/Shanghai (UTC+08:00)") {
		t.Fatalf("wrong local date: %s", first)
	}
	repo.zone = "America/Los_Angeles"
	if resolve() != first || repo.calls != 1 {
		t.Fatal("retry reloaded or changed request date")
	}
}

func TestRequestDateFallsBackExplicitlyToUTC(t *testing.T) {
	at := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	for _, zone := range []string{"", "Local", "not-a-timezone"} {
		if got := formatRequestDateContext(at, zone); !strings.Contains(got, "2026-11-14") || !strings.Contains(got, "Timezone: UTC (UTC+00:00)") {
			t.Fatalf("invalid zone inherited host time: %q", got)
		}
	}
	if got := formatRequestDateContext(at, "America/Los_Angeles"); !strings.Contains(got, "2026-11-13 (Friday)") {
		t.Fatalf("westward midnight boundary lost: %s", got)
	}
	service := &Service{repo: &requestDateRepository{zone: "Asia/Shanghai", err: errors.New("unavailable")}}
	if got := service.requestDateContext(t.Context(), 1, at)(); !strings.Contains(got, "Timezone: UTC") {
		t.Fatal(got)
	}
}

func TestRequestDateRequiresProviderEvidenceAndPreservesOriginalMessages(t *testing.T) {
	messages := []llm.Message{{Role: "system", Content: "stable policy"}, {Role: "user", Content: "today?"}}
	for _, tc := range []struct {
		route   channel.ResolvedRoute
		enabled bool
	}{
		{channel.ResolvedRoute{Protocol: llm.AdapterDeepSeekResponses}, true},
		{channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://api.deepseek.com/v1"}, true},
		{channel.ResolvedRoute{Protocol: llm.AdapterAnthropicMessages, BaseURL: "https://api.deepseek.com/anthropic"}, true},
		{channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, ModelCapabilitiesJSON: `{"nativeToolKeys":["deepseek.tool_search"]}`}, true},
		{channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, ModelCapabilitiesJSON: `{"nativeTools":[{"key":"deepseek.tool_search"}]}`}, true},
		{channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, ModelCapabilitiesJSON: `{"nativeFiles":{"provider":"deepseek"}}`}, true},
		{channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, UpstreamModel: "deepseek-flash", BaseURL: "https://api.example.com"}, false},
		{channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://api.deepseek.com.example.com"}, false},
		{channel.ResolvedRoute{Protocol: llm.AdapterChat01ChatCompletions, BaseURL: "https://api.deepseek.com"}, false},
	} {
		calls := 0
		got := withDeepSeekRequestDate(messages, &tc.route, func() string { calls++; return "current date" })
		if tc.enabled {
			if calls != 1 || len(got) != 3 || got[0].Content != "stable policy" || got[1].Role != "system" || got[2].Content != "today?" {
				t.Fatalf("bad injection: %#v", got)
			}
		} else if calls != 0 || len(got) != 2 {
			t.Fatalf("unrelated route changed: %#v", tc.route)
		}
		if len(messages) != 2 || messages[1].Content != "today?" {
			t.Fatal("original history mutated")
		}
	}
}

func TestDeepSeekDateIncludedInInitialAndFailoverRequests(t *testing.T) {
	service := &Service{}
	gen := testRouteGenerationContext(&model.Conversation{PublicID: "date-chat"})
	gen.requestDateContext = func() string { return "Current request date: 2026-11-14" }
	for _, mode := range []routeGenerationMode{routeGenerationInitial, routeGenerationFailover} {
		for _, protocol := range []string{llm.AdapterDeepSeekResponses, llm.AdapterOpenAIResponses, llm.AdapterOpenAIChatCompletions, llm.AdapterAnthropicMessages} {
			route := &channel.ResolvedRoute{Protocol: protocol, BaseURL: "https://api.deepseek.com", UpstreamModel: "deepseek-v4-pro"}
			plan := service.prepareRouteGeneration(t.Context(), routeGenerationPreparationInput{Generation: gen, Route: route, PromptPlan: testRoutePromptPlan(), Mode: mode})
			count := strings.Count(plan.generateInput.Instructions, "Current request date")
			for _, message := range plan.generateInput.Messages {
				count += strings.Count(message.Content, "Current request date")
			}
			if count != 1 || len(plan.fullLLMMessages) != 5 || plan.estimatedPromptTokens <= 0 {
				t.Fatalf("date lost or duplicated for %s: %#v", protocol, plan.generateInput)
			}
			for _, message := range plan.generateInput.Messages {
				if message.Role == "latest_reminder" {
					t.Fatal("unsupported reminder role sent")
				}
			}
		}
	}
	if shouldInlineSystemPromptToUser(channel.ResolvedRoute{Protocol: llm.AdapterDeepSeekResponses}) {
		t.Fatal("DeepSeek Responses system messages incorrectly inlined")
	}
}

func TestDeepSeekTemporaryChatIncludesDateWithoutSavingHistory(t *testing.T) {
	repo := &requestDateRepository{zone: "Asia/Shanghai"}
	gateway := &temporaryLLMGatewayStub{output: &llm.GenerateOutput{Text: "ok"}}
	service := &Service{cfg: config.NewRuntime(config.Config{}), repo: repo, llmClient: gateway,
		routeResolver: &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{"flash": {Protocol: llm.AdapterDeepSeekResponses, UpstreamModel: "deepseek-flash"}}},
	}
	result, err := service.StreamTemporaryChat(t.Context(), TemporaryChatInput{UserID: 1, Model: "flash", SessionID: "date", ClientRunID: "date-run", Messages: []TemporaryChatMessage{{Role: "user", Content: "today?"}}}, nil)
	if err != nil || result == nil || len(gateway.inputs) != 1 {
		t.Fatalf("temporary generation failed: %v", err)
	}
	request := gateway.inputs[0]
	content := request.Instructions
	for _, message := range request.Messages {
		content += message.Content
	}
	if strings.Count(content, "Current request date") != 1 || !strings.Contains(content, "Asia/Shanghai") || repo.calls != 1 {
		t.Fatalf("missing temporary date: %#v", request)
	}
}

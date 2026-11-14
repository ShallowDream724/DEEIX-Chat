package conversation

import (
	"context"
	"testing"
	"time"

	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type nativeURLTestGateway struct {
	temporaryLLMGatewayStub
	targets []string
}

type nativeURLBudgetRepo struct {
	metadataRecoveryBillingRepo
	requested int64
	limit     int64
}

func (r *nativeURLBudgetRepo) RaiseUsageBalanceReservation(_ context.Context, _ uint, _ string, amount int64) error {
	r.requested = amount
	if r.limit > 0 && amount > r.limit {
		return repository.ErrInsufficientBalance
	}
	return nil
}

func TestDeepSeekURLBudgetAddsModelTokensAndAllPlannedFetchesBeforeNetwork(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		repo := &nativeURLBudgetRepo{}
		if !allowed {
			repo.limit = 140
		}
		gateway := &nativeURLTestGateway{}
		service := &Service{llmClient: gateway, billingSvc: appbilling.NewService(repo)}
		authorization := &domainbilling.UsageAuthorization{Mode: "usage", Reservation: &domainbilling.UsageBalanceReservation{UserID: 1, RefNo: "run"}, NativeToolBillingEnabled: true, NativeToolPricing: map[string]domainbilling.NativeToolPrice{"deepseek.tool_search": {NanousdPerCall: 11}, "deepseek.tool_fetch": {NanousdPerCall: 7}}}
		route := &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, UpstreamModel: "deepseek-flash", PlatformModelName: "model", ModelCapabilitiesJSON: `{"nativeToolKeys":["deepseek.tool_search","deepseek.tool_fetch"]}`}
		var prepared deepSeekURLInput
		_, _, _, err := service.prepareDeepSeekURLInput(t.Context(), llm.RouteConfig{UpstreamModel: "deepseek-flash"}, deepSeekURLTestInput(), authorization, &prepared, deepSeekURLBudget{route: route, estimate: usageBudgetEstimate{InputTokens: 100, OutputTokens: 20}})
		// Two explicit fetches and one possible hosted fetch: 120 token cost +
		// 11 search + 3*7 fetch. A larger token budget cannot hide fetch cost.
		if repo.requested != 152 {
			t.Fatalf("combined budget=%d, want 152 (err=%v)", repo.requested, err)
		}
		if allowed && (err != nil || len(gateway.targets) != 2) {
			t.Fatalf("authorized fetch failed: %v %#v", err, gateway.targets)
		}
		if !allowed && (err == nil || len(gateway.targets) != 0) {
			t.Fatalf("network ran before budget approval: %v %#v", err, gateway.targets)
		}
	}
}

func TestDeepSeekFetchedContextContinuesWithoutAnotherNetworkRead(t *testing.T) {
	input := deepSeekURLTestInput()
	gateway := &nativeURLTestGateway{}
	service := &Service{llmClient: gateway}
	var prepared deepSeekURLInput
	_, _, _, err := service.prepareDeepSeekURLInput(t.Context(), llm.RouteConfig{UpstreamModel: "deepseek-flash"}, input, nil, &prepared)
	if err != nil {
		t.Fatal(err)
	}
	state := withDeepSeekURLContext("", llm.AdapterOpenAIChatCompletions, prepared.parts)
	messages := append(input.Messages, llm.Message{Role: "assistant", Content: "answer", ProviderStateJSON: state}, llm.Message{Role: "user", Content: "quote the first sentence"})
	restored := restoreDeepSeekURLContext(messages)
	if len(restored[0].Parts) != 3 || restored[2].Content != "quote the first sentence" {
		t.Fatalf("restored context moved to another turn: %#v", restored)
	}
	var next deepSeekURLInput
	_, usage, _, err := service.prepareDeepSeekURLInput(t.Context(), llm.RouteConfig{UpstreamModel: "deepseek-flash"}, llm.GenerateInput{Options: input.Options, NativeToolKeys: input.NativeToolKeys, Messages: restored}, nil, &next)
	if err != nil || len(gateway.targets) != 2 || len(usage) != 0 {
		t.Fatalf("follow-up re-fetched retained content: %v %#v", err, gateway.targets)
	}
}

func (g *nativeURLTestGateway) FetchNativeURL(_ context.Context, _ llm.RouteConfig, target string) (llm.NativeURLPage, error) {
	g.targets = append(g.targets, target)
	return llm.NativeURLPage{Title: "Page", Content: "Full body"}, nil
}

func deepSeekURLTestInput() llm.GenerateInput {
	return llm.GenerateInput{UserID: 1, Options: map[string]any{"thinking": true, "tools": []map[string]any{{"type": "deepseek_web_search"}, {"type": "deepseek_read_url"}}}, NativeToolKeys: map[string]string{"tool_search": "deepseek.tool_search", "tool_fetch": "deepseek.tool_fetch"}, Messages: []llm.Message{{Role: "user", Content: "Read https://example.org/a and https://example.org/a#part and [b](https://example.org/b(x))."}}}
}

func TestDeepSeekExplicitURLPreparationDeduplicatesAndAccountsOncePerTurn(t *testing.T) {
	gateway := &nativeURLTestGateway{}
	service := &Service{llmClient: gateway}
	input := deepSeekURLTestInput()
	route := llm.RouteConfig{UpstreamModel: "deepseek-flash"}
	var prepared deepSeekURLInput
	output, usage, calls, err := service.prepareDeepSeekURLInput(t.Context(), route, input, nil, &prepared)
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.targets) != 2 || gateway.targets[1] != "https://example.org/b(x)" || usage["deepseek.tool_fetch"] != 2 || len(calls) != 2 {
		t.Fatalf("fetch identity/count mismatch: %#v %#v %#v", gateway.targets, usage, calls)
	}
	if len(output.Messages[0].Parts) != 3 {
		t.Fatalf("original question and two pages not preserved: %#v", output.Messages)
	}
	output, usage, _, err = service.prepareDeepSeekURLInput(t.Context(), route, output, nil, &prepared)
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.targets) != 2 || len(usage) != 0 || len(output.Messages[0].Parts) != 3 {
		t.Fatalf("follow-up repeated fetch, charge or context: %#v %#v", gateway.targets, output)
	}
}

func TestDeepSeekExplicitURLPreparationRespectsToolsThinkingAndNoURL(t *testing.T) {
	for _, name := range []string{"no URL", "off", "read disabled", "tool choice none", "older turn URL", "Pro"} {
		t.Run(name, func(t *testing.T) {
			input := deepSeekURLTestInput()
			route := llm.RouteConfig{UpstreamModel: "deepseek-flash"}
			switch name {
			case "no URL":
				input.Messages[0].Content = "hello"
			case "off":
				input.Options["thinking"] = false
			case "read disabled":
				input.Options["tools"] = []map[string]any{{"type": "deepseek_web_search"}}
			case "tool choice none":
				input.Options["tool_choice"] = "none"
			case "older turn URL":
				input.Messages = append(input.Messages, llm.Message{Role: "assistant", Content: "done"}, llm.Message{Role: "user", Content: "next"})
			case "Pro":
				route.UpstreamModel = "deepseek-v4-pro"
			}
			gateway := &nativeURLTestGateway{}
			service := &Service{llmClient: gateway}
			var prepared deepSeekURLInput
			_, usage, _, err := service.prepareDeepSeekURLInput(t.Context(), route, input, nil, &prepared)
			if err != nil || len(gateway.targets) != 0 || len(usage) != 0 {
				t.Fatalf("unexpected fetch: %#v %#v %v", gateway.targets, usage, err)
			}
		})
	}
}

func TestTemporaryProviderStateIsPrivateToUserSessionRouteAndAncestry(t *testing.T) {
	var cache temporaryProviderStateCache
	messages := []llm.Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "answer"}}
	scope := temporaryContextObservationKey{userID: 1, session: "session", route: "route"}
	key := scope
	for _, msg := range messages {
		key.ancestry = extendTemporaryContextAncestry(key.ancestry, msg)
	}
	cache.put(key, `{"signature":"private"}`, time.Now())
	for _, name := range []string{"same", "user", "session", "route", "ancestor", "expired"} {
		t.Run(name, func(t *testing.T) {
			current := cloneLLMMessages(messages)
			candidate := scope
			now := time.Now()
			switch name {
			case "user":
				candidate.userID = 2
			case "session":
				candidate.session = "other"
			case "route":
				candidate.route = "other"
			case "ancestor":
				current[0].Content = "edited"
			case "expired":
				now = now.Add(31 * time.Minute)
			}
			cache.restore(candidate, current, now)
			if (current[1].ProviderStateJSON != "") != (name == "same") {
				t.Fatalf("state boundary failed: %#v", current)
			}
		})
	}
}

func TestDeepSeekNativeFilesRequireActualFlashAndProviderEvidence(t *testing.T) {
	for _, tc := range []struct {
		model, base, cap     string
		enabled, spreadsheet bool
	}{
		{"deepseek-flash", "https://api.deepseek.com/v1", "", true, false},
		{"deepseek-v4-pro", "https://api.deepseek.com/v1", `{"nativeFiles":{"provider":"deepseek","enabled":true}}`, false, false},
		{"deepseek-flash", "https://compat.example", `{"inputModalities":["pdf"]}`, false, false},
		{"deepseek-flash", "https://compat.example", `{"nativeFiles":{"provider":"deepseek","enabled":true,"spreadsheet":"native"}}`, true, true},
		{"deepseek-flash", "https://api.deepseek.com/v1", `{"nativeFiles":{"provider":"deepseek","enabled":false}}`, false, false},
	} {
		enabled, spreadsheet := deepSeekNativeFilesPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, UpstreamModel: tc.model, BaseURL: tc.base, ModelCapabilitiesJSON: tc.cap})
		if enabled != tc.enabled || spreadsheet != tc.spreadsheet {
			t.Fatalf("%s %s policy=%v,%v", tc.model, tc.base, enabled, spreadsheet)
		}
	}
	policy := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, UpstreamModel: "deepseek-flash", BaseURL: "https://api.deepseek.com", ModelCapabilitiesJSON: `{"inputModalities":["text"]}`})
	if policy.DeepSeekFiles || policy.PDF {
		t.Fatal("explicit text-only modality was overridden by provider defaults")
	}
}

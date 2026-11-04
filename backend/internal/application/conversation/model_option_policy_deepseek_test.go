package conversation

import (
	"testing"

	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestDeepSeekWebSearchAuthorizesSearchAndFetchWithOneDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capabilities string
		tools        []map[string]any
	}{
		{"search", `{"nativeToolKeys":["deepseek.tool_search"]}`, []map[string]any{{"type": "web_search"}}},
		{"legacy fetch", `{"nativeToolKeys":["deepseek.tool_fetch"]}`, []map[string]any{{"type": "web_fetch"}}},
		{"legacy pair", `{"nativeToolKeys":["deepseek.tool_search","deepseek.tool_fetch"]}`, []map[string]any{{"type": "web_search"}, {"type": "web_fetch"}}},
		{"legacy payload", `{"nativeTools":[{"key":"deepseek.tool_fetch","protocol":"openai_responses","type":"web_fetch","payload":{"type":"web_fetch"}}]}`, []map[string]any{{"type": "web_fetch"}}},
		{"legacy protocol override", `{"nativeTools":[{"key":"deepseek.tool_search","protocol":"openai_chat_completions","type":"web_search"}]}`, []map[string]any{{"type": "web_search"}}},
		{"provider defaults", `{"nativeToolKeys":["deepseek.tool_search"],"defaultOptions":{"tools":[{"type":"web_search"}]}}`, nil},
		{"legacy defaults", `{"nativeToolKeys":["deepseek.tool_fetch"],"defaultOptions":{"tools":[{"type":"web_fetch"}]}}`, nil},
		{"legacy default only", `{"defaultOptions":{"tools":[{"type":"web_fetch"}]}}`, nil},
		{"legacy default pair", `{"defaultOptions":{"tools":[{"type":"web_search"},{"type":"web_fetch"}]}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := map[string]any{}
			if tc.tools != nil {
				options["tools"] = tc.tools
			}
			filtered := filterModelOptions(options, llm.AdapterOpenAIResponses, modelOptionPolicyConfig{ModelCapabilitiesJSON: tc.capabilities})
			tools := providerToolOptionPayloads(filtered["tools"])
			if len(tools) != 1 || tools[0]["type"] != "web_search" {
				t.Fatalf("expected one supported web_search declaration, got %#v", tools)
			}
			keys := authorizedNativeToolKeys(llm.AdapterOpenAIResponses, filtered, tc.capabilities)
			if keys["tool_search"] != "deepseek.tool_search" || keys["tool_fetch"] != "deepseek.tool_fetch" {
				t.Fatalf("both successful operation counters must be authorized, got %#v", keys)
			}
			for _, key := range keys {
				if key != "deepseek.tool_search" && key != "deepseek.tool_fetch" {
					t.Fatalf("unexpected provider identity: %#v", keys)
				}
			}
		})
	}
}

func TestDeepSeekWebSearchOnChatCompletionsDoesNotDispatchOrReserve(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capabilities string
		tools        []map[string]any
	}{
		{"selected search", `{"nativeToolKeys":["deepseek.tool_search"]}`, []map[string]any{{"type": "web_search"}}},
		{"selected legacy fetch", `{"nativeToolKeys":["deepseek.tool_fetch"]}`, []map[string]any{{"type": "web_fetch"}}},
		{"protocol override", `{"nativeTools":[{"key":"deepseek.tool_search","protocol":"openai_chat_completions","type":"web_search"}]}`, []map[string]any{{"type": "web_search"}}},
		{"search default", `{"nativeToolKeys":["deepseek.tool_search"],"defaultOptions":{"tools":[{"type":"web_search"}]}}`, nil},
		{"legacy fetch default", `{"nativeToolKeys":["deepseek.tool_fetch"],"defaultOptions":{"tools":[{"type":"web_fetch"}]}}`, nil},
		{"legacy default pair", `{"defaultOptions":{"tools":[{"type":"web_search"},{"type":"web_fetch"}]}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := map[string]any{}
			if tc.tools != nil {
				options["tools"] = tc.tools
			}
			filtered := filterModelOptions(options, llm.AdapterOpenAIChatCompletions, modelOptionPolicyConfig{ModelCapabilitiesJSON: tc.capabilities})
			if tools := providerToolOptionPayloads(filtered["tools"]); len(tools) != 0 {
				t.Fatalf("DeepSeek tools must not reach Chat Completions: %#v", tools)
			}
			if keys := authorizedNativeToolKeys(llm.AdapterOpenAIChatCompletions, filtered, tc.capabilities); len(keys) != 0 {
				t.Fatalf("unsupported tools must not be authorized: %#v", keys)
			}
			service := &Service{billingSvc: appbilling.NewService(nil)}
			authorization := &domainbilling.UsageAuthorization{Mode: "usage", NativeToolPricing: map[string]domainbilling.NativeToolPrice{
				"deepseek.tool_search": {NanousdPerCall: 11},
				"deepseek.tool_fetch":  {NanousdPerCall: 7},
				"openai.web_search":    {NanousdPerCall: 99},
			}}
			route := &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, ModelCapabilitiesJSON: tc.capabilities}
			if err := service.ensureUsageBudgetCoversEstimate(t.Context(), authorization, route, filtered, usageBudgetEstimate{}); err != nil {
				t.Fatal(err)
			}
			if authorization.Reservation != nil || len(authorization.NativeToolKeys) != 0 {
				t.Fatalf("unsupported tools reserved fees: %#v", authorization)
			}
		})
	}
}

func TestDeepSeekWebSearchDoesNotAuthorizeUnselectedOrAmbiguousTools(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capabilities string
		options      map[string]any
	}{
		{"unselected", `{"nativeToolKeys":["deepseek.tool_search"]}`, nil},
		{"ambiguous provider", `{"nativeToolKeys":["deepseek.tool_search","openai.web_search"]}`, map[string]any{"tools": []map[string]any{{"type": "web_search"}}}},
		{"ambiguous legacy fetch", `{"nativeToolKeys":["deepseek.tool_fetch","openai.web_search"]}`, map[string]any{"tools": []map[string]any{{"type": "web_fetch"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filtered := filterModelOptions(tc.options, llm.AdapterOpenAIResponses, modelOptionPolicyConfig{ModelCapabilitiesJSON: tc.capabilities})
			if len(providerToolOptionPayloads(filtered["tools"])) != 0 || len(authorizedNativeToolKeys(llm.AdapterOpenAIResponses, filtered, tc.capabilities)) != 0 {
				t.Fatalf("unexpected tool authorization: %#v", filtered)
			}
		})
	}
}

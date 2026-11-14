package conversation

import (
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"testing"
)

func TestDeepSeekNativeWebToolsOnAllProtocols(t *testing.T) {
	capabilities := `{"nativeToolKeys":["deepseek.tool_search","deepseek.tool_fetch"]}`
	for _, protocol := range []string{llm.AdapterDeepSeekResponses, llm.AdapterOpenAIResponses, llm.AdapterOpenAIChatCompletions, llm.AdapterAnthropicMessages} {
		t.Run(protocol, func(t *testing.T) {
			filtered := filterModelOptions(map[string]any{"tools": []map[string]any{{"type": "web_search"}, {"type": "web_fetch"}}}, protocol, modelOptionPolicyConfig{ModelCapabilitiesJSON: capabilities})
			tools := providerToolOptionPayloads(filtered["tools"])
			if len(tools) != 2 || tools[0]["type"] != "deepseek_web_search" || tools[1]["type"] != "deepseek_read_url" {
				t.Fatalf("wrong native declarations: %#v", tools)
			}
			if protocol == llm.AdapterAnthropicMessages && (tools[0]["name"] != "deepseek_web_search" || tools[1]["name"] != "deepseek_read_url") {
				t.Fatalf("missing native names: %#v", tools)
			}
			keys := authorizedNativeToolKeys(protocol, filtered, capabilities)
			for counter, expected := range map[string]string{"tool_search": "deepseek.tool_search", "tool_fetch": "deepseek.tool_fetch", "tool_image_search": "deepseek.tool_image_search"} {
				if keys[counter] != expected {
					t.Fatalf("%s authorization: %#v", counter, keys)
				}
			}
		})
	}
}

func TestDeepSeekProSearchUsesResponsesWireAndExistingBillingIdentity(t *testing.T) {
	for _, capabilities := range []string{
		`{"nativeToolKeys":["deepseek.tool_search","deepseek.tool_fetch"]}`,
		`{"nativeTools":[{"key":"deepseek.tool_search","protocols":["openai_responses"],"type":"web_search","payload":{"type":"web_search"}}]}`,
		`{"nativeTools":[{"key":"deepseek.tool_search","protocols":["deepseek_responses"],"type":"deepseek_web_search","payload":{"type":"deepseek_web_search"}}]}`,
	} {
		for _, protocol := range []string{llm.AdapterDeepSeekResponses, llm.AdapterOpenAIResponses} {
			for _, wireType := range []string{"web_search", "deepseek_web_search"} {
				route := &channel.ResolvedRoute{UpstreamModel: "deepseek-v4-pro", Protocol: protocol, ModelCapabilitiesJSON: capabilities}
				options := map[string]any{"tools": []map[string]any{{"type": wireType}, {"type": "deepseek_read_url"}}}
				filtered, _ := resolveChatModelOptions(options, protocol, modelOptionPolicyConfig{ModelCapabilitiesJSON: capabilities, UpstreamModel: route.UpstreamModel})
				filtered = restrictDeepSeekToolsForRoute(route, filtered)
				tools := providerToolOptionPayloads(filtered["tools"])
				if len(tools) != 1 || tools[0]["type"] != "web_search" {
					t.Fatalf("Pro search lost: %s %s %#v", protocol, wireType, tools)
				}
				keys := authorizedNativeToolKeys(protocol, filtered, capabilities, route.UpstreamModel)
				if keys["tool_search"] != "deepseek.tool_search" || keys["tool_fetch"] != "deepseek.tool_fetch" || keys["tool_image_search"] != "" || keys["deepseek.tool_image_search"] != "" {
					t.Fatalf("wrong Pro billing authorization: %#v", keys)
				}
				if files, _ := deepSeekNativeFilesPolicy(route); files {
					t.Fatal("Pro search must not enable Files")
				}
			}
		}
	}
}

func TestDeepSeekToolModelSupportIsIndependentOfFiles(t *testing.T) {
	capabilities := `{"nativeToolKeys":["deepseek.tool_search","deepseek.tool_fetch"],"nativeFiles":{"provider":"deepseek","enabled":false}}`
	for _, tc := range []struct {
		model, protocol string
		want            int
	}{
		{"deepseek-flash", llm.AdapterAnthropicMessages, 2},
		{"deepseek-v4-flash", llm.AdapterDeepSeekResponses, 2},
		{"deepseek-v4-flash-vision-exp", llm.AdapterOpenAIChatCompletions, 2},
		{"deepseek-v4-pro", llm.AdapterDeepSeekResponses, 1},
		{"deepseek-v4-pro", llm.AdapterOpenAIChatCompletions, 0},
		{"deepseek-v4-pro", llm.AdapterAnthropicMessages, 0},
		{"unrelated-model", llm.AdapterDeepSeekResponses, 0},
	} {
		filtered := filterModelOptions(nil, tc.protocol, modelOptionPolicyConfig{ModelCapabilitiesJSON: capabilities, UpstreamModel: tc.model})
		if tools := providerToolOptionPayloads(filtered["tools"]); len(tools) != tc.want {
			t.Fatalf("%s/%s tools: %#v", tc.model, tc.protocol, tools)
		}
	}
}

func TestDeepSeekProToolOffAndProviderAmbiguityArePreserved(t *testing.T) {
	for _, tc := range []struct {
		capabilities string
		options      map[string]any
	}{
		{`{"nativeToolKeys":["deepseek.tool_search"]}`, map[string]any{"tools": []any{}}},
		{`{"nativeTools":[{"key":"deepseek.tool_search","defaultEnabled":false}]}`, nil},
		{`{"nativeToolKeys":["deepseek.tool_search","openai.web_search"]}`, map[string]any{"tools": []map[string]any{{"type": "web_search"}}}},
	} {
		filtered := filterModelOptions(tc.options, llm.AdapterDeepSeekResponses, modelOptionPolicyConfig{ModelCapabilitiesJSON: tc.capabilities, UpstreamModel: "deepseek-v4-pro"})
		if len(providerToolOptionPayloads(filtered["tools"])) != 0 || len(authorizedNativeToolKeys(llm.AdapterDeepSeekResponses, filtered, tc.capabilities, "deepseek-v4-pro")) != 0 {
			t.Fatalf("unexpected Pro authorization: %#v", filtered)
		}
	}
}

func TestDeepSeekReadURLRemainsIndependentlySelectable(t *testing.T) {
	capabilities := `{"nativeToolKeys":["deepseek.tool_fetch"]}`
	filtered := filterModelOptions(map[string]any{"tools": []map[string]any{{"type": "deepseek_read_url"}}}, llm.AdapterOpenAIChatCompletions, modelOptionPolicyConfig{ModelCapabilitiesJSON: capabilities})
	keys := authorizedNativeToolKeys(llm.AdapterOpenAIChatCompletions, filtered, capabilities)
	if keys["tool_fetch"] != "deepseek.tool_fetch" || keys["tool_search"] != "" || keys["tool_image_search"] != "" {
		t.Fatalf("read-only request authorized unrelated counters: %#v", keys)
	}
}

func TestDeepSeekWebSearchDoesNotAuthorizeUnselectedOrAmbiguousTools(t *testing.T) {
	for _, tc := range []struct {
		name, capabilities string
		options            map[string]any
	}{
		{"explicit off", `{"nativeToolKeys":["deepseek.tool_search"]}`, map[string]any{"tools": []any{}}},
		{"configured off", `{"nativeTools":[{"key":"deepseek.tool_search","defaultEnabled":false}]}`, nil},
		{"ambiguous legacy provider", `{"nativeToolKeys":["deepseek.tool_search","openai.web_search"]}`, map[string]any{"tools": []map[string]any{{"type": "web_search"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filtered := filterModelOptions(tc.options, llm.AdapterOpenAIResponses, modelOptionPolicyConfig{ModelCapabilitiesJSON: tc.capabilities})
			if len(providerToolOptionPayloads(filtered["tools"])) != 0 || len(authorizedNativeToolKeys(llm.AdapterOpenAIResponses, filtered, tc.capabilities)) != 0 {
				t.Fatalf("unexpected tool authorization: %#v", filtered)
			}
		})
	}
}

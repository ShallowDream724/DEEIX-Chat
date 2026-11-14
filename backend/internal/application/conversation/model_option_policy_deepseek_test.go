package conversation

import (
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"testing"
)

func TestDeepSeekNativeWebToolsOnAllProtocols(t *testing.T) {
	capabilities := `{"nativeToolKeys":["deepseek.tool_search","deepseek.tool_fetch"]}`
	for _, protocol := range []string{llm.AdapterOpenAIResponses, llm.AdapterOpenAIChatCompletions, llm.AdapterAnthropicMessages} {
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

package conversation

import (
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm"
)

func TestInjectMCPToolGuidanceOnlyAddsPolicy(t *testing.T) {
	messages := []llm.Message{{Role: "user", Content: "搜索 DEEIX Chat"}}
	runtime := selectedToolRuntime{
		definitions: []llm.ToolDefinition{{
			Name:        "bing_search",
			Description: "搜索网页",
			InputSchema: []byte(`{"type":"object","properties":{"query":{"type":"string"},"count":{"type":"number"}},"required":["query"]}`),
		}},
	}

	result := injectMCPToolGuidance(messages, runtime)
	if len(result) != 2 {
		t.Fatalf("expected guidance message to be injected, got %#v", result)
	}
	guidance := result[0].Content
	for _, want := range []string{"# tool_use", "declared separately via the API schema", "Use the fewest useful calls"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("expected guidance to contain %q, got %q", want, guidance)
		}
	}
	for _, unwanted := range []string{"# tools", "bing_search", "query:string", "count:number"} {
		if strings.Contains(guidance, unwanted) {
			t.Fatalf("expected guidance not to duplicate tool schema %q, got %q", unwanted, guidance)
		}
	}
}

func TestInjectMCPToolGuidanceUsesCustomPrompt(t *testing.T) {
	messages := []llm.Message{{Role: "system", Content: "base"}, {Role: "user", Content: "查一下最新信息"}}
	runtime := selectedToolRuntime{
		definitions: []llm.ToolDefinition{{
			Name:        "web_search",
			Description: "搜索网页",
			InputSchema: []byte(`{"type":"object","properties":{}}`),
		}},
		prompt: "自定义 MCP 工具调用规则",
	}

	result := injectMCPToolGuidance(messages, runtime)
	if len(result) != 3 {
		t.Fatalf("expected guidance message to be injected, got %#v", result)
	}
	if result[1].Role != "system" || result[1].Content != "自定义 MCP 工具调用规则" {
		t.Fatalf("expected custom guidance after existing system messages, got %#v", result[1])
	}
	if strings.Contains(result[1].Content, "declared separately via the API schema") {
		t.Fatalf("expected custom guidance to replace default guidance, got %q", result[1].Content)
	}
}

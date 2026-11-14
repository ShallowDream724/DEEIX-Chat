package llm

import (
	"errors"
	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/nativetool"
	"strings"
	"testing"
)

func TestNativeUsageRetainsConfirmedCountersOnFailedOrCanceledTerminalEvent(t *testing.T) {
	for _, event := range []string{"response.failed", "response.completed"} {
		t.Run(event, func(t *testing.T) {
			output := &portllm.GenerateOutput{}
			errorField := ""
			if event == "response.failed" {
				errorField = `,"error":{"message":"interrupted"}`
			}
			stream := "event: " + event + "\ndata: {\"type\":\"" + event + "\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":1,\"server_tool_use\":{\"tool_search\":2}}" + errorField + "}}\n\n"
			err := consumeOpenAIGenerateStream(portllm.EndpointResponses, portllm.AdapterOpenAIResponses, strings.NewReader(stream), output, func(portllm.GenerateStreamEvent) error { return errors.New("canceled") }, false)
			if err == nil {
				t.Fatal("expected interrupted delivery")
			}
			qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: map[string]string{"tool_search": "deepseek.tool_search"}}, portllm.RouteConfig{})
			if output.ServerSideToolUsage["deepseek.tool_search"] != 2 {
				t.Fatalf("confirmed counters lost: %#v", output.ServerSideToolUsage)
			}
			if output.Usage.InputTokens != 1 {
				t.Fatalf("terminal token usage lost: %#v", output.Usage)
			}
		})
	}
}

func TestNativeUsageUsesAuthorizedProviderSuccessfulCounts(t *testing.T) {
	parsed := map[string]any{"usage": map[string]any{"server_tool_use": map[string]any{"tool_search": float64(2), "tool_fetch": float64(0)}}}
	output := &portllm.GenerateOutput{ServerSideToolUsage: parseServerSideToolUsage(parsed)}
	qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: map[string]string{"tool_search": "deepseek.tool_search", "tool_fetch": "deepseek.tool_fetch"}}, portllm.RouteConfig{})
	if output.ServerSideToolUsage["deepseek.tool_search"] != 2 || len(output.ServerSideToolUsage) != 1 {
		t.Fatalf("unexpected usage: %#v", output.ServerSideToolUsage)
	}
}

func TestNativeUsageDeepSeekKeepsIndependentSuccessfulFetchCount(t *testing.T) {
	parsed := map[string]any{"usage": map[string]any{"server_tool_use": map[string]any{"tool_search": float64(2), "tool_fetch": float64(3)}}}
	output := &portllm.GenerateOutput{ServerSideToolUsage: parseServerSideToolUsage(parsed)}
	qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: nativetool.UsageKeysForTool("openai_responses", "deepseek.tool_search", "deepseek-v4-pro")}, portllm.RouteConfig{UpstreamModel: "deepseek-v4-pro"})
	if output.ServerSideToolUsage["deepseek.tool_search"] != 2 || output.ServerSideToolUsage["deepseek.tool_fetch"] != 3 || len(output.ServerSideToolUsage) != 2 {
		t.Fatalf("independent successful counts lost: %#v", output.ServerSideToolUsage)
	}
}

func TestNativeUsageDeepSeekProDoesNotBillCompatibilityOrImageCounters(t *testing.T) {
	output := &portllm.GenerateOutput{ServerSideToolUsage: map[string]int64{"tool_search": 2, "tool_fetch": 3, "web_search": 9, "web_search_requests": 9, "tool_image_search": 7}}
	qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: nativetool.UsageKeysForTool("openai_responses", "deepseek.tool_search", "deepseek-v4-pro")}, portllm.RouteConfig{UpstreamModel: "deepseek-v4-pro"})
	if len(output.ServerSideToolUsage) != 2 || output.ServerSideToolUsage["deepseek.tool_search"] != 2 || output.ServerSideToolUsage["deepseek.tool_fetch"] != 3 {
		t.Fatalf("Pro billed unconfirmed or unsupported counters: %#v", output.ServerSideToolUsage)
	}
}

func TestNativeUsageLegacyFlashAliasesOnlyBillSuccessfulProviderCounters(t *testing.T) {
	for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		output := &portllm.GenerateOutput{ServerSideToolUsage: map[string]int64{"tool_search": 2, "tool_fetch": 3, "tool_image_search": 1, "web_search_requests": 9}}
		keys := nativetool.UsageKeysForTool("anthropic_messages", "deepseek.tool_search", model)
		keys["web_search_requests"] = "deepseek.tool_search"
		qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: keys}, portllm.RouteConfig{UpstreamModel: model})
		if len(output.ServerSideToolUsage) != 3 || output.ServerSideToolUsage["deepseek.tool_search"] != 2 || output.ServerSideToolUsage["deepseek.tool_fetch"] != 3 || output.ServerSideToolUsage["deepseek.tool_image_search"] != 1 {
			t.Fatalf("%s counted compatibility counters: %#v", model, output.ServerSideToolUsage)
		}
	}
}

func TestNativeUsageGemini3CountsGroundingQueries(t *testing.T) {
	output := &portllm.GenerateOutput{ServerSideToolUsage: map[string]int64{"google_search": 1}, ServerToolCalls: []portllm.ToolCall{{ToolName: "google_search", ArgumentsJSON: `{"queries":["one","two","one"]}`}}}
	qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: map[string]string{"google_search": "google.google_search"}}, portllm.RouteConfig{UpstreamModel: "gemini-3-pro"})
	if output.ServerSideToolUsage["google.google_search"] != 3 {
		t.Fatalf("unexpected usage: %#v", output.ServerSideToolUsage)
	}
}

func TestNativeUsageUsesReturnedGoogleModelVersionForAlias(t *testing.T) {
	output := &portllm.GenerateOutput{ModelVersion: "gemini-3.1-pro", ServerSideToolUsage: map[string]int64{"google_search": 1}, ServerToolCalls: []portllm.ToolCall{{ToolName: "google_search", ArgumentsJSON: `{"queries":["one","two"]}`}}}
	qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: map[string]string{"google_search": "google.google_search"}}, portllm.RouteConfig{UpstreamModel: "provider-alias"})
	if output.ServerSideToolUsage["google.google_search"] != 2 {
		t.Fatalf("returned model version ignored: %#v", output.ServerSideToolUsage)
	}
}

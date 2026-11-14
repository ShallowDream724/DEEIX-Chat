package llm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func deepSeekTestWirePayload(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestDeepSeekObservedWebFixturesUseProviderSuccessfulCounters(t *testing.T) {
	for _, tc := range []struct {
		file, protocol, endpoint string
		count, input             int64
	}{
		{"web-chat.json", portllm.AdapterOpenAIChatCompletions, portllm.EndpointChatCompletions, 3, 6755},
		{"web-responses.json", portllm.AdapterOpenAIResponses, portllm.EndpointResponses, 3, 7358},
		{"web-messages.json", portllm.AdapterAnthropicMessages, "", 4, 8637},
	} {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile("testdata/deepseek/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			var output *portllm.GenerateOutput
			if tc.endpoint == "" {
				output, err = parseAnthropicResponse(body, anthropicToolClassifier{})
			} else {
				output, err = parseOpenAIGenerateOutput(tc.endpoint, tc.protocol, body, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			qualifyNativeToolUsage(output, portllm.GenerateInput{NativeToolKeys: map[string]string{"tool_search": "deepseek.tool_search"}}, portllm.RouteConfig{})
			if output.ServerSideToolUsage["deepseek.tool_search"] != tc.count || len(output.ServerSideToolUsage) != 1 || output.Usage.InputTokens != tc.input || output.ProviderStateJSON == "" {
				t.Fatalf("observed response mapping failed: %#v", output)
			}
		})
	}
}

func TestDeepSeekWebContinuationPreservesKindAndSignatureOnAllProtocols(t *testing.T) {
	chat := `{"choices":[{"message":{"role":"assistant","content":"answer","server_tool_calls":[{"index":0,"reasoning_content":"","calls":[{"id":"read1","type":"deepseek_read_url","kind":"read_link","action":{"type":"read_link","url":"https://example.org"},"status":"completed","signature":"opaque-secret"}]}]}}],"usage":{"server_tool_use":{"tool_fetch":1,"tool_image_search":2,"web_fetch_requests":9}}}`
	output, err := parseOpenAIGenerateOutput(portllm.EndpointChatCompletions, portllm.AdapterOpenAIChatCompletions, []byte(chat), false)
	if err != nil {
		t.Fatal(err)
	}
	input := portllm.GenerateInput{Messages: []portllm.Message{{Role: "assistant", Content: output.Text, ProviderStateJSON: output.ProviderStateJSON}}}
	payload, _ := buildOpenAIRequestBody(portllm.AdapterOpenAIChatCompletions, "deepseek-flash", portllm.EndpointChatCompletions, input, false)
	payload = deepSeekTestWirePayload(t, payload)
	msg := asMap(asSlice(payload["messages"])[0])
	steps := asSlice(msg["server_tool_calls"])
	if len(steps) != 1 {
		t.Fatalf("chat continuation lost: %#v", payload)
	}
	if !strings.Contains(output.ProviderStateJSON, `"kind":"read_link"`) || !strings.Contains(output.ProviderStateJSON, `"signature":"opaque-secret"`) {
		t.Fatalf("provider fields lost: %s", output.ProviderStateJSON)
	}
	if output.ServerSideToolUsage["tool_fetch"] != 1 || output.ServerSideToolUsage["tool_image_search"] != 2 {
		t.Fatalf("usage lost: %#v", output.ServerSideToolUsage)
	}

	responses := `{"output":[{"type":"web_search_call","id":"read1","kind":"read_link","status":"completed","action":{"type":"open_page","url":"https://example.org"},"signature":"opaque-secret"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`
	output, err = parseOpenAIGenerateOutput(portllm.EndpointResponses, portllm.AdapterOpenAIResponses, []byte(responses), false)
	if err != nil {
		t.Fatal(err)
	}
	items := buildResponsesAPIInput([]portllm.Message{{Role: "assistant", Content: output.Text, ProviderStateJSON: output.ProviderStateJSON}}, nil)
	if len(items) != 2 || items[0]["kind"] != "read_link" || items[0]["signature"] != "opaque-secret" {
		t.Fatalf("Responses continuation lost: %#v", items)
	}

	messages := `{"content":[{"type":"server_tool_use","id":"read1","name":"deepseek_read_url","kind":"read_link","input":{"action":"read_link","url":"https://example.org"}},{"type":"web_fetch_tool_result","tool_use_id":"read1","kind":"read_link","signature":"opaque-secret","content":{"type":"web_fetch_result","url":"https://example.org"}},{"type":"text","text":"answer"}]}`
	output, err = parseAnthropicResponse([]byte(messages), anthropicToolClassifier{})
	if err != nil {
		t.Fatal(err)
	}
	payload, err = buildAnthropicRequestBody("deepseek-flash", portllm.GenerateInput{Messages: []portllm.Message{{Role: "assistant", Content: output.Text, ProviderStateJSON: output.ProviderStateJSON}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	payload = deepSeekTestWirePayload(t, payload)
	blocks := asSlice(asMap(asSlice(payload["messages"])[0])["content"])
	if len(blocks) != 3 || asMap(blocks[1])["signature"] != "opaque-secret" {
		t.Fatalf("Messages continuation lost: %#v", payload)
	}
}

func TestDeepSeekHTTP200FailuresPreserveConfirmedUsage(t *testing.T) {
	for _, protocol := range []string{portllm.AdapterOpenAIChatCompletions, portllm.AdapterOpenAIResponses} {
		endpoint := portllm.EndpointChatCompletions
		if protocol == portllm.AdapterOpenAIResponses {
			endpoint = portllm.EndpointResponses
		}
		output, err := parseOpenAIGenerateOutput(endpoint, protocol, []byte(`{"status":"failed","error":{"message":"generation failed"},"usage":{"input_tokens":7,"server_tool_use":{"tool_search":2,"tool_fetch":3,"tool_image_search":4}}}`), false)
		if err == nil || output == nil || output.Usage.InputTokens != 7 || output.ServerSideToolUsage["tool_search"] != 2 {
			t.Fatalf("%s lost accepted usage: %#v %v", protocol, output, err)
		}
	}
	output, err := parseAnthropicResponse([]byte(`{"type":"error","error":{"message":"generation failed"},"usage":{"input_tokens":7,"server_tool_use":{"tool_fetch":3}}}`), anthropicToolClassifier{})
	if err == nil || output == nil || output.ServerSideToolUsage["tool_fetch"] != 3 {
		t.Fatalf("Messages lost accepted usage: %#v %v", output, err)
	}
}

func TestDeepSeekChatStreamFinalToolsAndErrorUsage(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"answer"}}]}` + "\n\n" + `data: {"choices":[{"finish_reason":"stop","delta":{"server_tool_calls":[{"index":0,"calls":[{"id":"r","type":"deepseek_read_url","kind":"read_link","signature":"s","status":"completed"}]}]}}],"usage":{"prompt_tokens":9,"server_tool_use":{"tool_fetch":1,"tool_image_search":2}}}` + "\n\ndata: [DONE]\n\n"
	output := &portllm.GenerateOutput{}
	if err := consumeOpenAIGenerateStream(portllm.EndpointChatCompletions, portllm.AdapterOpenAIChatCompletions, strings.NewReader(stream), output, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(output.ServerToolCalls) != 1 || !strings.Contains(output.ProviderStateJSON, `"signature":"s"`) || output.ServerSideToolUsage["tool_image_search"] != 2 {
		t.Fatalf("final native state lost: %#v", output)
	}
	errorStream := `data: {"error":{"message":"failed"},"choices":[],"usage":{"prompt_tokens":5,"server_tool_use":{"tool_fetch":2}}}` + "\n\n"
	output = &portllm.GenerateOutput{}
	if err := consumeOpenAIGenerateStream(portllm.EndpointChatCompletions, portllm.AdapterOpenAIChatCompletions, strings.NewReader(errorStream), output, nil, false); err == nil || output.Usage.InputTokens != 5 || output.ServerSideToolUsage["tool_fetch"] != 2 {
		t.Fatalf("error usage lost: %#v %v", output, err)
	}
}

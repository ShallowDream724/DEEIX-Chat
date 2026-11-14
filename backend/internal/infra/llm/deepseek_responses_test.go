package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

func TestDeepSeekResponsesReusesTextStreamAndModelTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer authorization")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"deepseek-pro","owned_by":"deepseek"}]}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if payload["model"] != "deepseek-pro" || len(asSlice(payload["input"])) != 1 {
			t.Errorf("expected Responses model/input: %#v", payload)
		}
		for _, key := range []string{"previous_response_id", "store", "background", "prompt_cache_key"} {
			if _, exists := payload[key]; exists {
				t.Errorf("unsupported OpenAI field %s leaked into DeepSeek request", key)
			}
		}
		response := `{"id":"resp_deepseek","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}}`
		if payload["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `event: response.output_text.delta`)
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, response)
	}))
	defer server.Close()
	client := NewClient(security.NewStrictOutboundPolicy(true))
	route := portllm.RouteConfig{Protocol: portllm.AdapterDeepSeekResponses, Endpoint: portllm.EndpointChatCompletions, BaseURL: server.URL, APIKey: "test-key", UpstreamModel: "deepseek-pro"}
	adapter, err := client.adapterFor(route)
	if err != nil || adapter.Name() != portllm.AdapterDeepSeekResponses {
		t.Fatalf("registered adapter lost its identity: %#v, %v", adapter, err)
	}
	input := portllm.GenerateInput{
		Messages:           []portllm.Message{{Role: "user", Content: "hello"}},
		PreviousResponseID: "resp_previous", ResponsesBackground: true, PromptCacheKey: "cache",
		Options: map[string]any{"store": true, "background": true},
	}
	for _, stream := range []bool{false, true} {
		var output *portllm.GenerateOutput
		var deltas strings.Builder
		if stream {
			output, err = client.GenerateStream(context.Background(), route, input, func(event portllm.GenerateStreamEvent) error {
				deltas.WriteString(event.Delta)
				return nil
			})
		} else {
			output, err = client.Generate(context.Background(), route, input)
		}
		if err != nil || output == nil || output.Text != "hello" || output.Usage.InputTokens != 9 || output.Usage.OutputTokens != 2 {
			t.Fatalf("stream=%v output = %#v, error = %v", stream, output, err)
		}
		if stream && deltas.String() != "hello" {
			t.Fatalf("stream delta = %q", deltas.String())
		}
	}
	models, err := client.ListModels(context.Background(), route)
	if err != nil || len(models) != 1 || models[0].ID != "deepseek-pro" {
		t.Fatalf("models = %#v, error = %v", models, err)
	}
}

func TestDeepSeekResponsesRetainsLegacySignatureStateOnExplicitRoute(t *testing.T) {
	response := `{"output":[{"type":"web_search_call","id":"read1","kind":"read_link","status":"completed","signature":"opaque-secret"}]}`
	output, err := parseOpenAIGenerateOutput(portllm.EndpointResponses, portllm.AdapterDeepSeekResponses, []byte(response), false)
	if err != nil || output.ProviderStateJSON == "" {
		t.Fatalf("parse DeepSeek signature state: %#v, %v", output, err)
	}
	input := portllm.GenerateInput{Messages: []portllm.Message{{Role: "assistant", Content: "answer", ProviderStateJSON: output.ProviderStateJSON}}}
	route := portllm.RouteConfig{Protocol: portllm.AdapterDeepSeekResponses, BaseURL: "https://relay.example", UpstreamModel: "deepseek-flash"}
	input = retainDeepSeekStateForRoute(route, input)
	items := buildResponsesAPIInput(input.Messages, nil)
	if len(items) != 2 || items[0]["signature"] != "opaque-secret" || items[0]["kind"] != "read_link" {
		t.Fatalf("explicit DeepSeek route lost compatible signed history: %#v", items)
	}
	route.Protocol = portllm.AdapterOpenAIResponses
	filtered := retainDeepSeekStateForRoute(route, input)
	if filtered.Messages[0].ProviderStateJSON != "" {
		t.Fatal("generic relay route without DeepSeek capability must not retain provider state")
	}
}

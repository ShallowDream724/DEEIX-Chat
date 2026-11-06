package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestOpenAIImageStreamFailureRetainsObservedUsage(t *testing.T) {
	const completed = `{"type":"image_generation.completed","b64_json":"aW1hZ2U=","usage":{"input_tokens":120,"input_tokens_details":{"text_tokens":20,"image_tokens":100},"output_tokens":30,"output_tokens_details":{"image_tokens":20,"text_tokens":10}}}`
	for _, protocol := range []string{portllm.AdapterOpenAIImageGenerations, portllm.AdapterOpenAIImageEdits} {
		for _, failure := range []string{"malformed SSE", "upstream error", "JSON callback cancellation"} {
			t.Run(protocol+"/"+failure, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if failure == "JSON callback cancellation" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprint(w, completed)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: %s\n\n", completed)
					if failure == "upstream error" {
						_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"message\":\"stream interrupted\"}}\n\n")
					} else {
						_, _ = fmt.Fprint(w, "data: {\"type\":")
					}
				}))
				defer server.Close()
				client := newTestClient()
				defer client.CloseIdleConnections()
				output, err := client.GenerateStream(t.Context(), portllm.RouteConfig{Protocol: protocol, BaseURL: server.URL, UpstreamModel: "gpt-image-2.6-flare"}, image26TestInput(nil), func(event portllm.GenerateStreamEvent) error {
					if failure == "JSON callback cancellation" && event.Usage != (portllm.Usage{}) {
						return context.Canceled
					}
					return nil
				})
				if err == nil || output == nil || output.Usage.InputTokens != 120 || output.Usage.OutputTokens != 30 {
					t.Fatalf("stream error lost observed usage: output=%#v err=%v", output, err)
				}
				usage := output.Usage.ImageUsage
				if !usage.Complete || usage.TextInputTokens != 20 || usage.ImageInputTokens != 100 || usage.ImageOutputTokens != 20 || usage.TextOutputTokens != 10 || output.Usage.RawUsageJSON == "" {
					t.Fatalf("stream error lost image billing breakdown: %#v", output.Usage)
				}
				if requests.Load() != 1 {
					t.Fatalf("stream failure caused %d upstream requests", requests.Load())
				}
			})
		}
	}
}

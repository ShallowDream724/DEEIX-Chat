package llm

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestResponsesGIFToolRejectsInvalidCombinationsBeforeHTTP(t *testing.T) {
	for _, test := range []struct {
		key   string
		value any
	}{
		{"partial_images", 1}, {"output_compression", 0}, {"input_image_mask", map[string]any{"file_id": "file-mask"}},
		{"frame_count", 17}, {"frame_count", 2.5}, {"frame_duration_ms", 2001}, {"model", "gpt-image-2.5-flare"},
	} {
		t.Run(test.key, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); _, _ = w.Write([]byte(`{"output":[]}`)) }))
			defer server.Close()
			client := newTestClient()
			defer client.CloseIdleConnections()
			tool := map[string]any{"type": "image_generation", "model": "gpt-image-2.6-flare", "output_format": "gif"}
			tool[test.key] = test.value
			_, err := client.Generate(t.Context(), portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: server.URL, UpstreamModel: "mainline"}, portllm.GenerateInput{Messages: []portllm.Message{{Role: "user", Content: "animate"}}, Options: map[string]any{"tools": []map[string]any{tool}}})
			if err == nil || requests.Load() != 0 {
				t.Fatalf("invalid GIF tool reached upstream: requests=%d err=%v", requests.Load(), err)
			}
		})
	}
}

func TestResponsesImagesPreserveExplicitRasterParameters(t *testing.T) {
	tool := map[string]any{"type": "image_generation", "model": "gpt-image-2.5-flare", "output_format": "webp", "partial_images": 2, "output_compression": 0, "input_image_mask": map[string]any{"file_id": "file-mask"}}
	payload, err := buildOpenAIRequestBody(portllm.AdapterOpenAIResponses, "mainline", portllm.EndpointResponses, portllm.GenerateInput{Options: map[string]any{"tools": []map[string]any{tool}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	tools, ok := payload["tools"].([]map[string]any)
	if !ok || len(tools) != 1 || tools[0]["output_compression"] != 0 || tools[0]["partial_images"] != 2 || tools[0]["input_image_mask"] == nil || payload["stream"] != true {
		t.Fatalf("static image request behavior changed: %#v", payload)
	}
}

func TestResponsesImagesInferMissingFormatFromBoundedBase64Header(t *testing.T) {
	// Include an invalid suffix: metadata classification must use the fixed
	// header and leave full byte validation to the existing artifact pipeline.
	encoded := "R0lGODlhAQABAIAA" + "invalid-image-tail"
	for _, item := range []map[string]any{
		{"type": "image_generation_call", "result": encoded},
		{"type": "image_generation_call", "result": map[string]any{"b64_json": encoded}},
	} {
		image, ok := parseResponsesGeneratedImage(item)
		if !ok || image.MIMEType != "image/gif" || image.B64JSON != encoded {
			t.Fatalf("missing GIF metadata: %#v", image)
		}
	}
	partial, ok := parseResponsesPartialImage(map[string]any{"partial_image_b64": encoded})
	if !ok || partial.MIMEType != "image/gif" {
		t.Fatalf("missing partial MIME: %#v", partial)
	}
	image, ok := parseResponsesGeneratedImage(map[string]any{"type": "image_generation_call", "output_format": "webp", "result": encoded})
	if !ok || image.MIMEType != "image/webp" {
		t.Fatal("explicit output format must retain precedence")
	}
}

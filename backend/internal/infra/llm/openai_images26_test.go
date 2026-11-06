package llm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func image26TestInput(options map[string]any) portllm.GenerateInput {
	return portllm.GenerateInput{
		Messages: []portllm.Message{{Role: "user", Parts: []portllm.ContentPart{
			{Kind: portllm.ContentPartText, Text: "Animate a waving flag"},
			{Kind: portllm.ContentPartImage, MimeType: "image/png", FileName: "reference.png", Data: []byte("reference")},
		}}},
		Options: options,
	}
}

func TestOpenAIImage26GIFBufferedGenerationAndEdit(t *testing.T) {
	var animation bytes.Buffer
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	second := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	second.SetColorIndex(0, 0, 1)
	if err := gif.EncodeAll(&animation, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{20, 20}, LoopCount: 0}); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(animation.Bytes())
	for _, protocol := range []string{portllm.AdapterOpenAIImageGenerations, portllm.AdapterOpenAIImageEdits} {
		t.Run(protocol, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Accept") == "text/event-stream" {
					t.Error("GIF request must use buffered HTTP")
				}
				var fields map[string]any
				if protocol == portllm.AdapterOpenAIImageEdits {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll() //nolint:errcheck
					if len(r.MultipartForm.File["image[]"]) != 1 {
						t.Error("static reference image missing from GIF edit")
					}
					fields = make(map[string]any)
					for key, values := range r.MultipartForm.Value {
						fields[key] = values[0]
					}
					if fields["frame_count"] != "2" || fields["frame_duration_ms"] != "205" {
						t.Errorf("missing GIF edit parameters: %#v", fields)
					}
				} else {
					if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
						t.Error(err)
						return
					}
					if fields["frame_count"] != float64(2) || fields["frame_duration_ms"] != float64(205) {
						t.Errorf("missing GIF generation parameters: %#v", fields)
					}
				}
				for _, key := range []string{"stream", "partial_images", "output_compression", "prompt_rewriter"} {
					if _, ok := fields[key]; ok {
						t.Errorf("GIF request contains incompatible %s", key)
					}
				}
				if fields["complexity"] != "medium" || fields["moderation"] != "low" || fields["safety_identifier"] != "hashed-user" {
					t.Errorf("missing common image parameters: %#v", fields)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": encoded}}})
			}))
			defer server.Close()
			client := newTestClient()
			defer client.CloseIdleConnections()
			route := portllm.RouteConfig{Protocol: protocol, BaseURL: server.URL, UpstreamModel: "gpt-image-2.6-flare"}
			input := image26TestInput(map[string]any{
				"output_format": "gif", "complexity": "medium", "frame_count": 2, "frame_duration_ms": 205,
				"output_compression": 75, "partial_images": 3, "stream": true, "prompt_rewriter": "unsupported",
				"moderation": "low", "safety_identifier": "hashed-user",
			})
			_, err := client.GenerateStream(t.Context(), route, input, nil)
			if !errors.Is(err, portllm.ErrUnsupportedStream) || requests.Load() != 0 {
				t.Fatalf("GIF must select fallback before HTTP: err=%v requests=%d", err, requests.Load())
			}
			output, err := client.Generate(t.Context(), route, input)
			if err != nil || output == nil || len(output.GeneratedImages) != 1 {
				t.Fatalf("buffered GIF generation failed: output=%#v err=%v", output, err)
			}
			generated := output.GeneratedImages[0]
			if generated.MIMEType != "image/gif" || generated.B64JSON != encoded {
				t.Fatalf("GIF MIME or bytes lost: %#v", generated)
			}
			decoded, err := base64.StdEncoding.DecodeString(generated.B64JSON)
			if err != nil {
				t.Fatal(err)
			}
			result, err := gif.DecodeAll(bytes.NewReader(decoded))
			if err != nil || len(result.Image) != 2 || result.Delay[0] != 20 || result.Delay[1] != 20 {
				t.Fatalf("animation frames or timing lost: animation=%#v err=%v", result, err)
			}
		})
	}
}

func TestOpenAIImage26GIFParameterBoundaries(t *testing.T) {
	if _, err := buildOpenAIImageGenerationRequestBody("gpt-image-2.5-sunburst", image26TestInput(map[string]any{"output_format": "gif"})); err == nil {
		t.Fatal("GIF must fail locally on models without GIF support")
	}
	for _, test := range []struct {
		name    string
		options map[string]any
		mask    bool
	}{
		{"too few frames", map[string]any{"frame_count": 1}, false},
		{"too many frames", map[string]any{"frame_count": 17}, false},
		{"fractional frames", map[string]any{"frame_count": 2.5}, false},
		{"invalid frame type", map[string]any{"frame_count": "9"}, false},
		{"too short delay", map[string]any{"frame_duration_ms": 19}, false},
		{"too long delay", map[string]any{"frame_duration_ms": 2001}, false},
		{"mask", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := map[string]any{"output_format": "gif"}
			for key, value := range test.options {
				options[key] = value
			}
			input := image26TestInput(options)
			if test.mask {
				input.ImageEditMask = &portllm.ContentPart{MimeType: "image/png", Data: []byte("mask")}
			}
			if _, _, _, err := buildOpenAIImageEditMultipartRequest("gpt-image-2.6-sunburst", input, false); err == nil {
				t.Fatal("expected invalid GIF edit parameters to fail before HTTP")
			}
		})
	}
	for _, options := range []map[string]any{
		{"output_format": "gif"},
		{"output_format": "gif", "frame_count": 2, "frame_duration_ms": 20},
		{"output_format": "gif", "frame_count": 16, "frame_duration_ms": 2000},
	} {
		payload, err := buildOpenAIImageGenerationRequestBody("gpt-image-2.6-sunburst", image26TestInput(options))
		if err != nil {
			t.Fatal(err)
		}
		if _, configured := options["frame_count"]; !configured {
			if _, sent := payload["frame_count"]; sent {
				t.Fatal("omitted GIF settings must preserve upstream defaults")
			}
		}
	}
}

func TestOpenAIImage26RasterParameterCompatibility(t *testing.T) {
	for _, model := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.6-sunburst", "gpt-image-2.6-flare-2026-11-06"} {
		for _, format := range []string{"jpeg", "webp", "png"} {
			input := image26TestInput(map[string]any{"output_format": format, "output_compression": 0, "complexity": "low", "frame_count": 9})
			payload, err := buildOpenAIImageGenerationRequestBody(model, input)
			if err != nil {
				t.Fatal(err)
			}
			_, compressed := payload["output_compression"]
			_, complexity := payload["complexity"]
			if compressed != (format == "jpeg" || format == "webp") || complexity != openAIImageModelSupports26Params(model) {
				t.Fatalf("unexpected raster params for %s/%s: %#v", model, format, payload)
			}
			if _, ok := payload["frame_count"]; ok {
				t.Fatal("GIF parameters must not reach raster generation")
			}
			body, contentType, _, err := buildOpenAIImageEditMultipartRequest(model, input, false)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer req.MultipartForm.RemoveAll() //nolint:errcheck
			if got := req.FormValue("output_compression"); (got == "0") != compressed {
				t.Fatalf("compression=0 not preserved for %s/%s: %q", model, format, got)
			}
		}
	}
}

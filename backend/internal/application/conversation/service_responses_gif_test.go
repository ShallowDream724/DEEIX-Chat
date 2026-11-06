package conversation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	infrallm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

func TestResponsesGIFRequestUsageAndSavedAttachment(t *testing.T) {
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.SetColorIndex(1, 1, 1)
	var animation bytes.Buffer
	if err := gif.EncodeAll(&animation, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{20, 30}, LoopCount: 3}); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(animation.Bytes())
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				tools, _ := request["tools"].([]any)
				if request["model"] != "mainline-model" || request["stream"] != stream || len(tools) != 1 {
					t.Errorf("Responses lifecycle or tools changed: %#v", request)
					return
				}
				tool, _ := tools[0].(map[string]any)
				if tool["model"] != "gpt-image-2.6-flare" || tool["output_format"] != "gif" || tool["frame_count"] != float64(2) || tool["frame_duration_ms"] != float64(205) || tool["complexity"] != "medium" || tool["partial_images"] != float64(0) {
					t.Errorf("GIF tool fields or authorized model lost: %#v", tool)
				}
				// Deliberately omit output_format on the image item: real GIF bytes
				// must still acquire correct MIME and survive the save/read path.
				item := map[string]any{"type": "image_generation_call", "id": "ig_1", "status": "completed", "result": encoded}
				response := map[string]any{
					"id": "resp_gif", "status": "completed", "model": "mainline-model", "output": []any{item}, "tools": []any{tool},
					"usage":      map[string]any{"input_tokens": 50, "output_tokens": 7},
					"tool_usage": map[string]any{"image_gen": map[string]any{"input_tokens": 200, "input_tokens_details": map[string]any{"text_tokens": 80, "image_tokens": 120}, "output_tokens": 400, "cached_tokens": 50, "cached_tokens_details": map[string]any{"cached_text_tokens": 20, "cached_image_tokens": 30}}},
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					added, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "item": item})
					completed, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
					_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\n", added, completed)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(response)
				}
			}))
			defer server.Close()
			s, files := newGeneratedFilesTestService(t, config.Config{})
			client := infrallm.NewClient(security.OutboundPolicy{})
			defer client.CloseIdleConnections()
			s.llmClient = client
			capabilities := `{"nativeTools":[{"key":"openai.image_generation","payload":{"model":"gpt-image-2.6-flare"}}]}`
			options := filterModelOptions(map[string]any{"tools": []any{map[string]any{"type": "image_generation", "model": "unauthorized-user-model", "output_format": "gif", "frame_count": 2, "frame_duration_ms": 205, "complexity": "medium", "partial_images": 0}}}, llm.AdapterOpenAIResponses, modelOptionPolicyConfig{Mode: modelOptionPolicyAllowlist, AllowedPathsJSON: config.DefaultModelOptionAllowedPathsJSON(), ModelCapabilitiesJSON: capabilities})
			route := llm.RouteConfig{Protocol: llm.AdapterOpenAIResponses, BaseURL: server.URL, UpstreamModel: "mainline-model"}
			runner := &messageGenerationRunner{service: s, preferStream: stream, onDelta: func(string) error { return nil }, maxLLMCalls: 1, usage: &messageUsageAccumulator{}, routeConfig: route, startedAt: time.Now()}
			output, err := runner.generate(t.Context(), llm.GenerateInput{Messages: []llm.Message{{Role: "user", Content: "Animate a waving flag"}}, Options: options}, nil)
			if err != nil || output == nil || len(output.GeneratedImages) != 1 || output.GeneratedImages[0].MIMEType != "image/gif" {
				t.Fatalf("Responses GIF result failed: output=%#v err=%v", output, err)
			}
			// Message completion uses the final provider snapshot; streaming also
			// maintains a cumulative live snapshot without counting terminal repeats.
			usage := output.Usage
			if stream && runner.usage.usage().ImageToolUsage.ImageOutputTokens != 400 {
				t.Fatalf("streamed image tool usage was duplicated: %#v", runner.usage.usage())
			}
			if usage.InputTokens != 50 || usage.OutputTokens != 7 || usage.ImageToolUsage.ImageOutputTokens != 400 || usage.ImageToolUsage.TextInputTokens != 80 || usage.ImageToolUsage.ImageInputTokens != 120 || !usage.ImageToolUsage.Complete {
				t.Fatalf("GIF usage was dropped, duplicated, or multiplied by frame count: %#v", usage)
			}
			repo := &generatedFileCompletionRepository{}
			s.repo = repo
			message := &model.Message{ID: 3}
			persisted, err := s.persistAssistantAttachmentsIfPresent(t.Context(), persistMessageGenerationInput{SendInput: SendMessageInput{UserID: 1, ConversationID: 2, PlatformModelName: "mainline-model"}, UserMessage: &model.Message{ID: 2}, AssistantMessage: message, ArtifactRoute: route, GeneratedImages: output.GeneratedImages, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens})
			if err != nil || !persisted || message.Status != "success" || message.ContentType != "image" || len(repo.attachments) != 1 || len(files.files) != 1 || requests.Load() != 1 {
				t.Fatalf("GIF message completion failed: message=%#v attachments=%#v err=%v", message, repo.attachments, err)
			}
			attachment := repo.attachments[0]
			if attachment.MimeType != "image/gif" || !strings.HasSuffix(attachment.FileName, ".gif") || !strings.Contains(message.Content, "/api/v1/files/"+attachment.FileID+"/content") {
				t.Fatalf("incorrect GIF attachment: %#v", attachment)
			}
			content, err := s.uploadSvc.OpenFileContent(t.Context(), 1, attachment.FileID)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := io.ReadAll(content.Reader)
			_ = content.Reader.Close()
			if err != nil || !bytes.Equal(stored, animation.Bytes()) {
				t.Fatal("GIF bytes changed during save/authenticated read")
			}
			decoded, err := gif.DecodeAll(bytes.NewReader(stored))
			if err != nil || len(decoded.Image) != 2 || decoded.LoopCount != 3 || decoded.Delay[0] != 20 || decoded.Delay[1] != 30 {
				t.Fatalf("GIF animation was flattened: %#v err=%v", decoded, err)
			}
		})
	}
}

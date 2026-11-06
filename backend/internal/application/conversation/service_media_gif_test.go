package conversation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	infrallm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/mediaartifact"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

// Exercise the real adapter, artifact downloader, and upload service together:
// GIF requests use buffered upstream HTTP while the media task keeps its normal
// streaming lifecycle, and persistence must retain every animation byte.
func TestStreamMediaGIFPersistsOriginalAnimation(t *testing.T) {
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.SetColorIndex(1, 1, 1)
	var animation bytes.Buffer
	if err := gif.EncodeAll(&animation, &gif.GIF{
		Image: []*image.Paletted{first, second}, Delay: []int{20, 30},
		Disposal: []byte{gif.DisposalNone, gif.DisposalBackground}, LoopCount: 3,
	}); err != nil {
		t.Fatal(err)
	}
	var reference bytes.Buffer
	if err := png.Encode(&reference, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, task := range []MediaImageTaskType{MediaImageTaskGeneration, MediaImageTaskEdit} {
		for _, outputSource := range []string{"base64", "url"} {
			t.Run(string(task)+"/"+outputSource, func(t *testing.T) {
				var posts, downloads atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						downloads.Add(1)
						w.Header().Set("Content-Type", "image/gif")
						_, _ = w.Write(animation.Bytes())
						return
					}
					posts.Add(1)
					if r.Header.Get("Accept") == "text/event-stream" {
						t.Error("GIF task sent an unsupported streaming HTTP request")
					}
					if task == MediaImageTaskEdit {
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							return
						}
						defer r.MultipartForm.RemoveAll() //nolint:errcheck
						if r.FormValue("stream") != "" || r.FormValue("output_format") != "gif" || len(r.MultipartForm.File["image[]"]) != 1 {
							t.Error("GIF edit must carry its static reference through buffered HTTP")
						}
						if r.FormValue("complexity") != "low" || r.FormValue("frame_count") != "2" || r.FormValue("frame_duration_ms") != "205" || r.FormValue("safety_identifier") != "hashed-user" || r.FormValue("moderation") != "low" {
							t.Error("default option policy dropped Image 2.6 edit settings")
						}
					} else {
						var payload map[string]any
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							t.Error(err)
							return
						}
						if payload["stream"] != nil || payload["output_format"] != "gif" {
							t.Error("GIF generation must use buffered HTTP")
						}
						if payload["complexity"] != "low" || payload["frame_count"] != float64(2) || payload["frame_duration_ms"] != float64(205) || payload["safety_identifier"] != "hashed-user" {
							t.Error("default option policy dropped Image 2.6 generation settings")
						}
					}
					entry := map[string]string{"b64_json": base64.StdEncoding.EncodeToString(animation.Bytes())}
					if outputSource == "url" {
						entry = map[string]string{"url": "http://" + r.Host + "/animation.gif"}
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{entry}})
				}))
				defer server.Close()
				store := objectstorage.NewLocal(t.TempDir())
				if _, err := store.Put(t.Context(), "source.png", bytes.NewReader(reference.Bytes()), objectstorage.PutOptions{ContentType: "image/png"}); err != nil {
					t.Fatal(err)
				}
				source := model.FileObject{FileID: "source", UserID: 1, FileName: "source.png", MimeType: "image/png", DetectedMIME: "image/png", StoragePath: "source.png", SizeBytes: int64(reference.Len())}
				repo := &mediaImageResultRepository{
					conversationRunClaimRepositoryStub: &conversationRunClaimRepositoryStub{
						runs: make(map[string]model.Run), conversation: model.Conversation{ID: 11, UserID: 1, PublicID: "conversation-11"},
					}, source: source,
				}
				runtimeCfg := config.NewRuntime(config.Config{ModelOptionAllowedPaths: config.DefaultModelOptionAllowedPathsJSON()})
				uploadSvc := appupload.NewServiceWithRuntime(runtimeCfg, mediaImageResultUploadRepository{source: source}, nil, appupload.Hooks{}, appupload.ErrorSet{}, "test")
				uploadSvc.SetObjectStoreProvider(&conversationTestStoreProvider{store: store})
				client := infrallm.NewClient(security.OutboundPolicy{})
				defer client.CloseIdleConnections()
				downloader := mediaartifact.New(security.OutboundPolicy{})
				defer downloader.CloseIdleConnections()
				protocol := llm.AdapterOpenAIImageGenerations
				if task == MediaImageTaskEdit {
					protocol = llm.AdapterOpenAIImageEdits
				}
				resolver := &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{
					"image26": {PlatformModelName: "image26", UpstreamID: 1, Protocol: protocol, BaseURL: server.URL, UpstreamModel: "gpt-image-2.6-flare", ModelCapabilitiesJSON: `{}`},
				}}
				registry := newGenerationStreamRegistry(nil, generationStreamOptions{})
				defer registry.close()
				service := &Service{cfg: runtimeCfg, repo: repo, uploadSvc: uploadSvc, llmClient: client, mediaDownloader: downloader, routeResolver: resolver, generationStreams: registry}
				input := MediaImageInput{UserID: 1, ConversationID: 11, TaskType: task, PlatformModelName: "image26", Prompt: "Animate a waving flag", ClientRunID: "gif-run", Options: map[string]any{
					"output_format": "gif", "complexity": "low", "frame_count": 2, "frame_duration_ms": 205, "safety_identifier": "hashed-user", "moderation": "low",
				}}
				if task == MediaImageTaskEdit {
					input.FileIDs = []string{"source"}
				}
				ctx, release, ok := registry.acquireRun(t.Context())
				if !ok {
					t.Fatal("acquire generation lifecycle")
				}
				defer release()
				defer registry.finish(ctx, input.ClientRunID)
				result, err := service.StreamMediaImage(ctx, input)
				if err != nil || result == nil || result.AssistantMessage.Status != "success" || len(repo.attachments) != 1 {
					t.Fatalf("GIF task failed to persist: result=%#v err=%v", result, err)
				}
				wantDownloads := int32(0)
				if outputSource == "url" {
					wantDownloads = 1
				}
				if posts.Load() != 1 || downloads.Load() != wantDownloads {
					t.Fatalf("HTTP calls: generation=%d downloads=%d", posts.Load(), downloads.Load())
				}
				attachment := repo.attachments[0]
				if attachment.MimeType != "image/gif" || !strings.HasSuffix(attachment.FileName, ".gif") {
					t.Fatalf("incorrect GIF attachment metadata: %#v", attachment)
				}
				reader, _, err := store.Open(t.Context(), attachment.StoragePath)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close() //nolint:errcheck
				stored, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(stored, animation.Bytes()) {
					t.Fatal("persisting the animation changed GIF bytes")
				}
				decoded, err := gif.DecodeAll(bytes.NewReader(stored))
				if err != nil || len(decoded.Image) != 2 || decoded.LoopCount != 3 || decoded.Delay[0] != 20 || decoded.Delay[1] != 30 {
					t.Fatalf("GIF animation metadata lost: %#v, %v", decoded, err)
				}
			})
		}
	}
}

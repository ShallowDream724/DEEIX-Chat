package conversation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	infrallm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

type imageStreamUsageRepository struct {
	*mediaImageResultRepository
	persistedInput           repository.MessageUsageUpdate
	persistedAssistant       repository.AssistantMessageCompletionUpdate
	completed                bool
	finalizationContextError error
	completionError          error
}

func (r *imageStreamUsageRepository) CompleteAssistantMessageWithAttachments(ctx context.Context, _ uint, input repository.MessageUsageUpdate, _ uint, assistant repository.AssistantMessageCompletionUpdate, attachments []model.Attachment) error {
	r.persistedInput, r.persistedAssistant, r.attachments = input, assistant, attachments
	r.completed = true
	r.finalizationContextError = ctx.Err()
	return r.completionError
}

func (r *imageStreamUsageRepository) CompleteAssistantMessageWithGeneratedAttachments(ctx context.Context, _ uint, assistant repository.AssistantMessageCompletionUpdate, attachments []model.Attachment) error {
	r.persistedAssistant, r.attachments = assistant, attachments
	r.completed = true
	r.finalizationContextError = ctx.Err()
	return r.completionError
}

func TestCanceledMediaUsesObservedUsageForNewAndReusedMessages(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse=%t", reuse), func(t *testing.T) {
			repo := &imageStreamUsageRepository{mediaImageResultRepository: &mediaImageResultRepository{}}
			service := &Service{repo: repo}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			result, err := service.completeCanceledMediaGeneration(canceledMediaGenerationInput{
				Context: ctx, UserMessage: &model.Message{ID: 1}, AssistantMessage: &model.Message{ID: 2},
				ReuseUserMessage: reuse, StartedAt: time.Now(), Billable: true, MetadataRefreshHint: "existing",
				Usage: llm.Usage{InputTokens: 100, CacheReadTokens: 20, CacheWriteTokens: 10, OutputTokens: 30, ReasoningTokens: 5, RawUsageJSON: `{"observed":true}`},
			})
			if err != nil || result == nil || repo.finalizationContextError != nil {
				t.Fatalf("cancel finalization failed: result=%#v err=%v", result, err)
			}
			if reuse {
				if result.UserMessage.TokenUsage != 0 || result.AssistantMessage.InputTokens != 100 || result.AssistantMessage.CacheReadTokens != 20 || result.AssistantMessage.CacheWriteTokens != 10 || result.AssistantMessage.TokenUsage != 165 || repo.persistedAssistant.InputTokens != 100 || repo.persistedAssistant.CacheReadTokens != 20 || repo.persistedAssistant.CacheWriteTokens != 10 {
					t.Fatalf("reused-message usage attributed incorrectly: result=%#v persisted=%#v", result, repo.persistedAssistant)
				}
			} else if result.UserMessage.TokenUsage != 130 || result.AssistantMessage.TokenUsage != 35 || repo.persistedInput.InputTokens != 100 || repo.persistedInput.CacheReadTokens != 20 || repo.persistedInput.CacheWriteTokens != 10 {
				t.Fatalf("new-message usage attributed incorrectly: result=%#v persisted=%#v", result, repo.persistedInput)
			}
			if repo.persistedAssistant.OutputTokens != 30 || repo.persistedAssistant.ReasoningTokens != 5 || result.RawUsageJSON != `{"observed":true}` {
				t.Fatal("cancellation lost observed output or audit usage")
			}
		})
	}
}

func TestStreamMediaImageFailureKeepsObservedUsageWithoutSavingArtifacts(t *testing.T) {
	for _, failure := range []string{"malformed SSE", "upstream error", "cancellation", "cancellation persistence failure"} {
		t.Run(failure, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\",\"usage\":{\"input_tokens\":120,\"input_tokens_details\":{\"text_tokens\":20,\"image_tokens\":100},\"output_tokens\":30,\"output_tokens_details\":{\"image_tokens\":20,\"text_tokens\":10}}}\n\n")
				if failure == "upstream error" {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"message\":\"stream interrupted\"}}\n\n")
				} else {
					_, _ = fmt.Fprint(w, "data: {\"type\":")
				}
			}))
			defer server.Close()
			repo := &imageStreamUsageRepository{mediaImageResultRepository: &mediaImageResultRepository{
				conversationRunClaimRepositoryStub: &conversationRunClaimRepositoryStub{runs: make(map[string]model.Run), conversation: model.Conversation{ID: 11, UserID: 1, PublicID: "conversation-11"}},
			}}
			if failure == "cancellation persistence failure" {
				repo.completionError = errors.New("persist canceled message")
			}
			client := infrallm.NewClient(security.OutboundPolicy{})
			defer client.CloseIdleConnections()
			resolver := &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{
				"image26": {PlatformModelName: "image26", UpstreamID: 1, Protocol: llm.AdapterOpenAIImageGenerations, BaseURL: server.URL, UpstreamModel: "gpt-image-2.6-flare"},
			}}
			registry := newGenerationStreamRegistry(nil, generationStreamOptions{})
			defer registry.close()
			service := &Service{cfg: config.NewRuntime(config.Config{}), repo: repo, llmClient: client, routeResolver: resolver, generationStreams: registry}
			lifecycleCtx, release, ok := registry.acquireRun(t.Context())
			if !ok {
				t.Fatal("acquire generation lifecycle")
			}
			defer release()
			defer registry.finish(lifecycleCtx, "stream-usage-run")
			ctx, cancel := context.WithCancel(lifecycleCtx)
			defer cancel()
			input := MediaImageInput{UserID: 1, ConversationID: 11, TaskType: MediaImageTaskGeneration, PlatformModelName: "image26", Prompt: "Draw a flag", ClientRunID: "stream-usage-run"}
			if failure == "cancellation" || failure == "cancellation persistence failure" {
				input.OnEvent = func(event string, _ map[string]any) error {
					if event == "usage" {
						cancel()
						return context.Canceled
					}
					return nil
				}
			}
			result, err := service.StreamMediaImage(ctx, input)
			wantStatus := "error"
			if failure == "cancellation" {
				wantStatus = "canceled"
			}
			if (err == nil) != (failure == "cancellation") || result == nil || !result.Billable || result.AssistantMessage.Status != wantStatus {
				t.Fatalf("unexpected failure result: result=%#v err=%v", result, err)
			}
			if result.UserMessage.InputTokens != 120 || result.AssistantMessage.OutputTokens != 30 || result.ImageUsage.TextInputTokens != 20 || result.ImageUsage.ImageInputTokens != 100 || result.ImageUsage.ImageOutputTokens != 20 || result.ImageUsage.TextOutputTokens != 10 || !result.ImageUsage.Complete || result.RawUsageJSON == "" {
				t.Fatalf("observed usage was lost or replaced by an estimate: %#v", result)
			}
			if requests.Load() != 1 || len(repo.attachments) != 0 {
				t.Fatalf("failure retried or saved artifacts: requests=%d attachments=%d", requests.Load(), len(repo.attachments))
			}
			if failure == "cancellation" && (!repo.completed || repo.finalizationContextError != nil || repo.persistedInput.InputTokens != 120 || repo.persistedAssistant.OutputTokens != 30 || repo.persistedAssistant.Status != "canceled") {
				t.Fatalf("canceled usage was not persisted with a live context: %#v", repo)
			}
			run := repo.runs[normalizeRunID(input.ClientRunID)]
			if run.InputTokens != 120 || run.OutputTokens != 30 {
				t.Fatalf("run usage lost: %#v", run)
			}
		})
	}
}

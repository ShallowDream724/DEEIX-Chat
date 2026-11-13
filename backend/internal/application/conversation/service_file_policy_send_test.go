package conversation

import (
	"context"
	"testing"

	appembedding "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/embedding"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	portembedding "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/embedding"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type sendFilePolicyRepository struct {
	repository.ConversationRepository
	preferenceCalls int
}

type sendFilePolicyEmbeddingRepository struct {
	repository.EmbeddingRepository
	availabilityCalls int
}

func (r *sendFilePolicyEmbeddingRepository) VectorStoreAvailable(context.Context) (bool, error) {
	r.availabilityCalls++
	return true, nil
}

func (r *sendFilePolicyRepository) GetUserSettingValue(_ context.Context, _ uint, key string) (string, error) {
	if key != "chat.file_mode" {
		panic("unexpected preference")
	}
	r.preferenceCalls++
	return "rag", nil
}

type sendFilePolicyEmbeddingClient struct{}

func (sendFilePolicyEmbeddingClient) CallAPI(context.Context, portembedding.Request) ([][]float32, error) {
	panic("file capability must not request an embedding")
}

func TestResolveMessageFilePolicyOnlyChecksAvailabilityForFilesOrKnowledgeBase(t *testing.T) {
	for _, tt := range []struct {
		name             string
		currentFiles     []string
		history          []model.Message
		knowledgeBaseIDs []string
		knowledgeEnabled bool
		wantChecks       int
	}{
		{name: "plain text"},
		{name: "disabled knowledge base", knowledgeBaseIDs: []string{"kb-1"}},
		{name: "enabled without selection", knowledgeEnabled: true},
		{name: "current attachment", currentFiles: []string{"file-current"}, wantChecks: 1},
		{name: "historical attachment", history: []model.Message{{Status: "success", Attachments: `[{"file_id":"file-history"}]`}}, wantChecks: 1},
		{name: "enabled selected knowledge base", knowledgeEnabled: true, knowledgeBaseIDs: []string{"kb-1"}, wantChecks: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{RAGEnabled: true, EmbeddingEnabled: true, RAGModel: "test-embedding", EmbeddingHost: "https://embedding.example", KnowledgeBaseEnabled: tt.knowledgeEnabled}
			runtime := config.NewRuntime(cfg)
			repo := &sendFilePolicyRepository{}
			embeddingRepo := &sendFilePolicyEmbeddingRepository{}
			service := &Service{
				cfg: runtime, repo: repo,
				embeddingSvc: appembedding.NewServiceWithRuntime(runtime, embeddingRepo, nil, sendFilePolicyEmbeddingClient{}, nil),
			}
			fileIDs := collectConversationFileIDs(tt.history, tt.currentFiles)
			mode, capability := service.resolveMessageFilePolicy(context.Background(), 7, fileIDs, tt.knowledgeBaseIDs, cfg)
			if embeddingRepo.availabilityCalls != tt.wantChecks || repo.preferenceCalls != tt.wantChecks {
				t.Fatalf("availability calls=%d, preference calls=%d, want %d each", embeddingRepo.availabilityCalls, repo.preferenceCalls, tt.wantChecks)
			}
			if tt.wantChecks == 0 {
				if mode != "auto" || capability.RAGAvailable {
					t.Fatalf("unexpected plain text policy: mode=%s capability=%#v", mode, capability)
				}
			} else if mode != "rag" || !capability.RAGAvailable {
				t.Fatalf("file policy lost configured behavior: mode=%s capability=%#v", mode, capability)
			}
		})
	}
}

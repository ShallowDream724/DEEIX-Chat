package conversation

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type generatedFileReaderStub struct {
	llmGateway
	open func(context.Context, llm.RouteConfig, llm.GeneratedFile) (*llm.GeneratedFileContent, error)
}

func (s generatedFileReaderStub) OpenGeneratedFile(ctx context.Context, route llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
	return s.open(ctx, route, file)
}

type generatedFileRepository struct {
	generatedImageUploadRepository
	files   map[string]model.FileObject
	deleted []string
}

func (r *generatedFileRepository) CreateFileObjectAndConsumeQuota(_ context.Context, file *model.FileObject, limit int64) (*model.StorageQuota, error) {
	quota, _ := r.GetOrInitUserStorageQuota(context.Background(), file.UserID, limit)
	if limit > 0 && quota.UsedBytes+file.SizeBytes > limit {
		return nil, repository.ErrStorageQuotaExceeded
	}
	r.files[file.FileID] = *file
	quota.UsedBytes += file.SizeBytes
	return quota, nil
}

func (r *generatedFileRepository) GetOrInitUserStorageQuota(_ context.Context, userID uint, limit int64) (*model.StorageQuota, error) {
	quota := &model.StorageQuota{UserID: userID, QuotaBytes: limit}
	for _, file := range r.files {
		if file.UserID == userID {
			quota.UsedBytes += file.SizeBytes
		}
	}
	return quota, nil
}

func (r *generatedFileRepository) GetLatestActiveFileObjectBySHA(_ context.Context, userID uint, sha string, size int64) (*model.FileObject, error) {
	for _, file := range r.files {
		if file.UserID == userID && file.SHA256 == sha && file.SizeBytes == size {
			return &file, nil
		}
	}
	return nil, nil
}

func (r *generatedFileRepository) GetActiveFileObjectByID(_ context.Context, userID uint, fileID string) (*model.FileObject, error) {
	file, ok := r.files[fileID]
	if !ok || file.UserID != userID {
		return nil, repository.ErrFileNotFound
	}
	return &file, nil
}

func (*generatedFileRepository) TouchFileObjectLastAccessedAt(context.Context, uint, string, time.Time) error {
	return nil
}

func (r *generatedFileRepository) DeleteFileObjectAndReleaseQuota(ctx context.Context, userID uint, fileID string, limit int64, options repository.DeleteFileObjectOptions) (*model.FileObject, *model.StorageQuota, bool, error) {
	if !options.RequireUnreferenced {
		return nil, nil, false, errors.New("cleanup must require an unreferenced file")
	}
	file, err := r.GetActiveFileObjectByID(ctx, userID, fileID)
	if err != nil {
		return nil, nil, false, err
	}
	delete(r.files, fileID)
	r.deleted = append(r.deleted, fileID)
	quota, _ := r.GetOrInitUserStorageQuota(ctx, userID, limit)
	return file, quota, true, nil
}

func newGeneratedFilesTestService(t *testing.T, cfg config.Config) (*Service, *generatedFileRepository) {
	t.Helper()
	repo := &generatedFileRepository{files: make(map[string]model.FileObject)}
	runtimeCfg := config.NewRuntime(cfg)
	uploadSvc := appupload.NewServiceWithRuntime(runtimeCfg, repo, nil, appupload.Hooks{}, appupload.ErrorSet{
		FileTooLarge: ErrFileTooLarge, StorageQuotaExceeded: ErrStorageQuotaExceeded, FileNotFound: ErrFileNotFound,
		MIMEBlocked: ErrMIMEBlocked, DangerousMIMEType: ErrDangerousMIMEType,
	}, "test")
	uploadSvc.SetObjectStoreProvider(&conversationTestStoreProvider{store: objectstorage.NewLocal(t.TempDir())})
	s := &Service{cfg: runtimeCfg, uploadSvc: uploadSvc}
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		return &llm.GeneratedFileContent{Body: io.NopCloser(strings.NewReader("123测试 " + file.ID)), MimeType: "text/plain"}, nil
	}}
	return s, repo
}

func TestGeneratedFilesSaveReuseAndAuthenticatedRead(t *testing.T) {
	s, repo := newGeneratedFilesTestService(t, config.Config{})
	input := assistantGeneratedFilesInput{UserID: 1, ConversationID: 2, AssistantMessageID: 3,
		Content: "[文件](sandbox:/mnt/data/test.txt) [网页](https://example.test/test.txt)",
		Route:   llm.RouteConfig{Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://routed.example/v1", APIKey: "test-route-key"},
		Files:   []llm.GeneratedFile{{ID: "one", FileName: "test.txt", SourceLinks: []string{"sandbox:/mnt/data/test.txt"}}},
	}
	input.Files = append(input.Files, input.Files[0]) // duplicate stream annotation
	calls := 0
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, route llm.RouteConfig, _ llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		calls++
		if route.BaseURL != input.Route.BaseURL || route.APIKey != input.Route.APIKey {
			t.Fatal("artifact download did not use the generation route")
		}
		return &llm.GeneratedFileContent{Body: io.NopCloser(strings.NewReader("123测试")), MimeType: "text/plain"}, nil
	}}
	result, err := s.saveAssistantGeneratedFiles(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(result.AttachmentRows) != 1 || len(result.AttachmentSnapshots) != 1 || len(repo.files) != 1 {
		t.Fatalf("duplicate output: calls=%d result=%#v", calls, result)
	}
	row := result.AttachmentRows[0]
	if row.MessageID != 3 || row.UserID != 1 || row.ConversationID != 2 || row.Kind == "image" {
		t.Fatalf("incorrect attachment ownership/kind: %#v", row)
	}
	want := "[文件](/api/v1/files/" + row.FileID + "/content) [网页](https://example.test/test.txt)"
	if result.Content != want || repo.files[row.FileID].Purpose != "generated_file" {
		t.Fatalf("incorrect stored result: %#v", result)
	}
	content, err := s.uploadSvc.OpenFileContent(t.Context(), 1, row.FileID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(content.Reader)
	_ = content.Reader.Close()
	if err != nil || string(data) != "123测试" {
		t.Fatalf("download bytes differ: %q %v", data, err)
	}
	if _, err := s.uploadSvc.OpenFileContent(t.Context(), 2, row.FileID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("another user could read the file: %v", err)
	}
	reused, err := s.saveAssistantGeneratedFiles(t.Context(), input)
	if err != nil || len(reused.CreatedFileIDs) != 0 || reused.AttachmentRows[0].FileID != row.FileID {
		t.Fatalf("retry should reuse stored bytes: %#v %v", reused, err)
	}
}

func TestGeneratedFileBatchFailurePreservesReusedFiles(t *testing.T) {
	s, repo := newGeneratedFilesTestService(t, config.Config{})
	input := assistantGeneratedFilesInput{UserID: 1, Files: []llm.GeneratedFile{{ID: "existing", FileName: "existing.txt"}}}
	first, err := s.saveAssistantGeneratedFiles(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	reader := s.llmClient.(generatedFileReaderStub)
	s.llmClient = generatedFileReaderStub{open: func(ctx context.Context, route llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		if file.ID == "failed" {
			return nil, errors.New("upstream download failed")
		}
		return reader.open(ctx, route, file)
	}}
	input.Files = append(input.Files, llm.GeneratedFile{ID: "new", FileName: "new.txt"}, llm.GeneratedFile{ID: "failed", FileName: "failed.txt"})
	_, err = s.saveAssistantGeneratedFiles(t.Context(), input)
	if !errors.Is(err, ErrGeneratedFileDownloadFailed) || classifyRunErrorCode(err) != "file.generated_download_failed" {
		t.Fatalf("unexpected failure: %v", err)
	}
	if len(repo.files) != 1 || len(repo.deleted) != 1 || repo.files[first.AttachmentRows[0].FileID].FileID == "" {
		t.Fatalf("cleanup deleted reused data or leaked new data: files=%v deleted=%v", repo.files, repo.deleted)
	}
}

func TestGeneratedFilesRespectConfiguredLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		cfg  config.Config
		want error
	}{
		{"single file", config.Config{MaxUploadFileBytes: 2}, ErrFileTooLarge},
		{"quota across files", config.Config{UserStorageQuotaBytes: 20}, ErrStorageQuotaExceeded},
		{"attachment count", config.Config{MaxMessageFiles: 1}, ErrTooManyMessageFiles},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, repo := newGeneratedFilesTestService(t, test.cfg)
			_, err := s.saveAssistantGeneratedFiles(t.Context(), assistantGeneratedFilesInput{UserID: 1,
				Files: []llm.GeneratedFile{{ID: "one", FileName: "1.txt"}, {ID: "two", FileName: "2.txt"}},
			})
			if !errors.Is(err, test.want) || len(repo.files) != 0 {
				t.Fatalf("limits or rollback failed: err=%v files=%v", err, repo.files)
			}
		})
	}
}

type generatedFileCompletionRepository struct {
	repository.ConversationRepository
	update      repository.AssistantMessageCompletionUpdate
	attachments []model.Attachment
	err         error
	retry       bool
}

func (r *generatedFileCompletionRepository) CompleteAssistantMessageWithAttachments(_ context.Context, _ uint, _ repository.MessageUsageUpdate, _ uint, update repository.AssistantMessageCompletionUpdate, attachments []model.Attachment) error {
	r.update, r.attachments = update, attachments
	return r.err
}

func (r *generatedFileCompletionRepository) CompleteAssistantMessageWithGeneratedAttachments(_ context.Context, _ uint, update repository.AssistantMessageCompletionUpdate, attachments []model.Attachment) error {
	r.update, r.attachments, r.retry = update, attachments, true
	return r.err
}

func TestGeneratedFilesCompleteMessageAndRetry(t *testing.T) {
	for _, retry := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			s, files := newGeneratedFilesTestService(t, config.Config{})
			repo := &generatedFileCompletionRepository{}
			if fail {
				repo.err = errors.New("transaction failed")
			}
			s.repo = repo
			message := &model.Message{ID: 3}
			completed, err := s.persistAssistantAttachmentsIfPresent(t.Context(), persistMessageGenerationInput{
				SendInput: SendMessageInput{UserID: 1, ConversationID: 2}, UserMessage: &model.Message{ID: 2}, AssistantMessage: message,
				AssistantText: "[下载](sandbox:/mnt/data/test.txt)", ReuseUserMessage: retry, OutputTokens: 25,
				GeneratedFiles: []llm.GeneratedFile{{ID: "one", FileName: "test.txt", SourceLinks: []string{"sandbox:/mnt/data/test.txt"}}},
			})
			if fail {
				if !errors.Is(err, repo.err) || completed || len(files.files) != 0 {
					t.Fatalf("failed message left files behind: %v %v", completed, err)
				}
				continue
			}
			if err != nil || !completed || repo.retry != retry || len(repo.attachments) != 1 || message.Attachments == "" || message.Status != "success" || repo.update.OutputTokens != 25 || strings.Contains(message.Content, "sandbox:") {
				t.Fatalf("completion lost content, files or usage: message=%#v repo=%#v err=%v", message, repo, err)
			}
		}
	}
}

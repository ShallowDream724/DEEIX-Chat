package conversation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

const chat01ConversationArtifactBase = "https://files.chat01.ai/python-generations/test/"
const chat01ConversationImageURL = "https://files.chat01.ai/user-ABCDEFGHIJKLMNOPQRSTUVWX__11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.png"

func conversationChat01File(name string) llm.GeneratedFile {
	raw := chat01ConversationArtifactBase + name
	digest := sha256.Sum256([]byte(raw))
	return llm.GeneratedFile{ID: "chat01-url:" + hex.EncodeToString(digest[:]), FileName: name, SourceLinks: []string{raw}}
}
func chat01GeneratedContent(data []byte, mimeType string) *llm.GeneratedFileContent {
	return &llm.GeneratedFileContent{Body: io.NopCloser(bytes.NewReader(data)), SizeBytes: int64(len(data)), MimeType: mimeType}
}

func TestChat01GeneratedFilesPartialFailurePreservesSuccessAndRetryDeduplicates(t *testing.T) {
	s, repo := newGeneratedFilesTestService(t, config.Config{})
	good, bad, after := conversationChat01File("good.txt"), conversationChat01File("expired.txt"), conversationChat01File("after.txt")
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		if file.ID == bad.ID {
			return nil, errors.New("expired CDN link")
		}
		return chat01GeneratedContent([]byte("123测试 "+file.FileName), "text/plain"), nil
	}}
	input := assistantGeneratedFilesInput{UserID: 1, ConversationID: 2, AssistantMessageID: 3, Route: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, Content: "[good](" + good.SourceLinks[0] + ") [bad](" + bad.SourceLinks[0] + ") [ordinary](https://example.org/readme.txt)", Files: []llm.GeneratedFile{good, bad, after, good}}
	result, err := s.saveAssistantGeneratedFiles(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AttachmentRows) != 2 || len(result.Failures) != 1 || len(repo.files) != 2 || len(repo.deleted) != 0 {
		t.Fatalf("partial batch lost successes: %#v files=%v", result, repo.files)
	}
	if strings.Contains(result.Content, "[good]("+good.SourceLinks[0]) || !strings.Contains(result.Content, bad.SourceLinks[0]) || !strings.Contains(result.Content, "保存失败") || !strings.Contains(result.Content, "https://example.org/readme.txt") {
		t.Fatal(result.Content)
	}
	firstIDs := []string{result.AttachmentRows[0].FileID, result.AttachmentRows[1].FileID}
	retry, err := s.saveAssistantGeneratedFiles(t.Context(), input)
	if err != nil || len(retry.CreatedFileIDs) != 0 || len(repo.files) != 2 || retry.AttachmentRows[0].FileID != firstIDs[0] || retry.AttachmentRows[1].FileID != firstIDs[1] {
		t.Fatalf("retry duplicated files: %#v %v", retry, err)
	}
	if _, _, err := s.uploadSvc.DeleteFileIfUnreferenced(t.Context(), 1, firstIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.uploadSvc.OpenFileContent(t.Context(), 1, firstIDs[0]); !errors.Is(err, ErrFileNotFound) {
		t.Fatal("deleted output remained readable", err)
	}
	recreated, err := s.saveAssistantGeneratedFiles(t.Context(), input)
	if err != nil || len(recreated.CreatedFileIDs) != 1 || len(repo.files) != 2 || recreated.AttachmentRows[0].FileID == firstIDs[0] || recreated.AttachmentRows[1].FileID != firstIDs[1] {
		t.Fatalf("explicit save retry resurrected stale identity or duplicated survivor: %#v %v", recreated, err)
	}
}

func TestChat01GeneratedImagesAndFilesShareCompletionAndInlineImagesDoNotDuplicate(t *testing.T) {
	s, files := newGeneratedFilesTestService(t, config.Config{})
	png, err := base64.StdEncoding.DecodeString(assistantImageTestPNG)
	if err != nil {
		t.Fatal(err)
	}
	good, bad := conversationChat01File("report.txt"), conversationChat01File("bad.txt")
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		if file.ID == bad.ID {
			return nil, errors.New("bad link")
		}
		if strings.HasPrefix(file.ID, "chat01-image-url:") {
			return chat01GeneratedContent(png, "image/png"), nil
		}
		return chat01GeneratedContent([]byte("123测试 report"), "text/plain"), nil
	}}
	repo := &generatedFileCompletionRepository{}
	s.repo = repo
	message := &model.Message{ID: 3}
	completed, err := s.persistAssistantAttachmentsIfPresent(t.Context(), persistMessageGenerationInput{SendInput: SendMessageInput{UserID: 1, ConversationID: 2}, UserMessage: &model.Message{ID: 2}, AssistantMessage: message, ArtifactRoute: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, AssistantText: "正文 ![image](" + chat01ConversationImageURL + ") [report](" + good.SourceLinks[0] + ")", GeneratedFiles: []llm.GeneratedFile{good, bad}, GeneratedImages: []llm.GeneratedImage{{URL: chat01ConversationImageURL}}, OutputTokens: 25})
	if err != nil || !completed || len(repo.attachments) != 2 || len(files.files) != 2 || message.Status != "success" || message.ContentType != "mixed" {
		t.Fatalf("mixed completion failed: %#v %v", message, err)
	}
	var imageID string
	for _, row := range repo.attachments {
		if row.Kind == "image" {
			imageID = row.FileID
		}
	}
	if imageID == "" || strings.Count(message.Content, "/api/v1/files/"+imageID+"/content") != 1 || strings.Contains(message.Content, chat01ConversationImageURL) || !strings.Contains(message.Content, "保存失败") || message.Attachments == "" {
		t.Fatalf("image duplicated or failure lost: %s", message.Content)
	}
}

func TestChat01ProcessImageAppendsPreviewAndInvalidImageIsLocalFailure(t *testing.T) {
	s, repo := newGeneratedFilesTestService(t, config.Config{})
	png, _ := base64.StdEncoding.DecodeString(assistantImageTestPNG)
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		if strings.Contains(file.SourceLinks[0], "invalid") {
			return chat01GeneratedContent([]byte("HTML is not an image"), "image/png"), nil
		}
		return chat01GeneratedContent(png, "image/png"), nil
	}}
	result, err := s.saveAssistantGeneratedFiles(t.Context(), assistantGeneratedFilesInput{UserID: 1, Route: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, Content: "绘图完成。", Images: []llm.GeneratedImage{{URL: chat01ConversationImageURL}, {URL: chat01ConversationArtifactBase + "invalid.png"}}})
	if err != nil || len(repo.files) != 1 || len(result.Failures) != 1 || len(result.AttachmentRows) != 1 || strings.Count(result.Content, "![") != 1 || !strings.Contains(result.Content, "绘图完成。") || !strings.Contains(result.Content, "保存失败") {
		t.Fatalf("unexpected image outcomes: %#v %v", result, err)
	}
}

func TestChat01GeneratedArtifactsUseStrictSharedQuotaAndPermitReuseWhenFull(t *testing.T) {
	s, repo := newGeneratedFilesTestService(t, config.Config{UserStorageQuotaBytes: 20})
	first, tooLarge, last := conversationChat01File("first.txt"), conversationChat01File("too-large.txt"), conversationChat01File("last.txt")
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		data := "123456789012"
		if file.ID == last.ID {
			data = "last"
		}
		return chat01GeneratedContent([]byte(data), "text/plain"), nil
	}}
	// Distinct contents must hit quota rather than content deduplication.
	baseReader := s.llmClient.(generatedFileReaderStub)
	s.llmClient = generatedFileReaderStub{open: func(ctx context.Context, route llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		if file.ID == tooLarge.ID {
			return chat01GeneratedContent([]byte("abcdefghijkl"), "text/plain"), nil
		}
		return baseReader.open(ctx, route, file)
	}}
	input := assistantGeneratedFilesInput{UserID: 1, Route: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, Files: []llm.GeneratedFile{first, tooLarge, last}}
	result, err := s.saveAssistantGeneratedFiles(t.Context(), input)
	quota, _ := repo.GetOrInitUserStorageQuota(t.Context(), 1, 20)
	if err != nil || len(result.AttachmentRows) != 2 || len(result.Failures) != 1 || !errors.Is(result.Failures[0].Cause, ErrStorageQuotaExceeded) || quota.UsedBytes != 16 {
		t.Fatalf("quota exemption or rollback: %#v quota=%#v err=%v", result, quota, err)
	}
	// A full quota can still reuse the exact already stored content.
	s2, repo2 := newGeneratedFilesTestService(t, config.Config{UserStorageQuotaBytes: 4})
	s2.llmClient = generatedFileReaderStub{open: func(context.Context, llm.RouteConfig, llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		return chat01GeneratedContent([]byte("last"), "text/plain"), nil
	}}
	fullInput := assistantGeneratedFilesInput{UserID: 1, Route: input.Route, Files: []llm.GeneratedFile{last}}
	one, err := s2.saveAssistantGeneratedFiles(t.Context(), fullInput)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s2.saveAssistantGeneratedFiles(t.Context(), fullInput)
	if err != nil || len(two.Failures) != 0 || len(two.CreatedFileIDs) != 0 || len(repo2.files) != 1 || one.AttachmentRows[0].FileID != two.AttachmentRows[0].FileID {
		t.Fatalf("full-quota retry failed: %#v %v", two, err)
	}
}

func TestChat01GeneratedArtifactLimitsArePerItemAndKeepEarlierFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want error
	}{
		{"file size", config.Config{MaxUploadFileBytes: 4}, ErrFileTooLarge},
		{"count", config.Config{MaxMessageFiles: 1}, ErrTooManyMessageFiles},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := newGeneratedFilesTestService(t, tc.cfg)
			first, second := conversationChat01File("first.txt"), conversationChat01File("second.txt")
			s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
				data := "tiny"
				if file.ID == second.ID {
					data = "too large"
				}
				return chat01GeneratedContent([]byte(data), "text/plain"), nil
			}}
			result, err := s.saveAssistantGeneratedFiles(t.Context(), assistantGeneratedFilesInput{UserID: 1, Route: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, Files: []llm.GeneratedFile{first, second}})
			if err != nil || len(result.AttachmentRows) != 1 || len(result.Failures) != 1 || !errors.Is(result.Failures[0].Cause, tc.want) || len(repo.files) != 1 {
				t.Fatalf("%#v %v", result, err)
			}
		})
	}
}

func TestChat01TemporaryGeneratedArtifactsSaveFilesWithoutMessagePersistence(t *testing.T) {
	s, files := newGeneratedFilesTestService(t, config.Config{ModelOptionPolicyMode: modelOptionPolicyAllowlist, ModelOptionAllowedPaths: config.DefaultModelOptionAllowedPathsJSON(), ModelOptionDeniedPaths: config.DefaultModelOptionDeniedPathsJSON()})
	file := conversationChat01File("temporary.txt")
	gateway := &temporaryLLMGatewayStub{output: &llm.GenerateOutput{GeneratedFiles: []llm.GeneratedFile{file}, Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}}
	s.llmClient = generatedFileReaderStub{llmGateway: gateway, open: func(context.Context, llm.RouteConfig, llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		return chat01GeneratedContent([]byte("123测试 temporary"), "text/plain"), nil
	}}
	s.routeResolver = &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{"chat01": {PlatformModelName: "chat01", UpstreamModel: "chat01", Protocol: llm.AdapterChat01ChatCompletions}}}
	noWrites := &temporaryPersistenceRepositoryStub{}
	s.repo = noWrites
	result, err := s.StreamTemporaryChat(t.Context(), TemporaryChatInput{UserID: 1, SessionID: "temporary-session", ClientRunID: "temporary-run", Model: "chat01", Messages: []TemporaryChatMessage{{Role: "user", Content: "generate a file"}}}, nil)
	if err != nil || result == nil || len(files.files) != 1 || result.AssistantMessage.Attachments == "" {
		t.Fatalf("temporary artifact lost: %#v %v", result, err)
	}
	if noWrites.traceWrites != 0 || noWrites.traceEventWrites != 0 || noWrites.toolCallWrites != 0 {
		t.Fatal("temporary conversation persisted")
	}
	if result.AssistantMessage.ID != 0 || result.AssistantMessage.PublicID == "" || result.AssistantMessage.OutputTokens != 5 {
		t.Fatalf("temporary message state changed: %#v", result.AssistantMessage)
	}
}

func TestChat01ArtifactReplacementDoesNotRewriteLongerOrUnrelatedURLs(t *testing.T) {
	source := chat01ConversationArtifactBase + "report.txt"
	content := "[output](" + source + ") [longer](" + source + "?other=1) [web](https://example.org/report.txt)"
	got, represented := rewriteChat01ArtifactLinks(content, map[string]string{source: "/api/v1/files/trusted/content"})
	if !represented["/api/v1/files/trusted/content"] || !strings.Contains(got, source+"?other=1") || !strings.Contains(got, "https://example.org/report.txt") || strings.Count(got, "/api/v1/files/trusted/content") != 1 {
		t.Fatal(got)
	}
}

func TestChat01PythonImagesShareImageBudgetAndQuotaWithNativeImages(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(assistantImageTestPNG)
	for _, name := range []string{"plot.png", "plot.txt"} {
		t.Run(name, func(t *testing.T) {
			s, repo := newGeneratedFilesTestService(t, config.Config{GeneratedImageMaxBytes: int64(len(png) - 1)})
			file := conversationChat01File(name)
			s.llmClient = generatedFileReaderStub{open: func(context.Context, llm.RouteConfig, llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
				return chat01GeneratedContent(png, "application/octet-stream"), nil
			}}
			result, err := s.saveAssistantGeneratedFiles(t.Context(), assistantGeneratedFilesInput{UserID: 1, Route: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, Files: []llm.GeneratedFile{file}})
			if err != nil || len(result.Failures) != 1 || !errors.Is(result.Failures[0].Cause, ErrFileTooLarge) || len(repo.files) != 0 {
				t.Fatalf("Python image bypassed image byte limit: %#v %v", result, err)
			}
		})
	}
	s, repo := newGeneratedFilesTestService(t, config.Config{UserStorageQuotaBytes: int64(len(png) + 3)})
	file := conversationChat01File("report.txt")
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		if strings.HasPrefix(file.ID, "chat01-image-url:") {
			return chat01GeneratedContent(png, "image/png"), nil
		}
		return chat01GeneratedContent([]byte("tiny"), "text/plain"), nil
	}}
	result, err := s.saveAssistantGeneratedFiles(t.Context(), assistantGeneratedFilesInput{UserID: 1, Route: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, Files: []llm.GeneratedFile{file}, Images: []llm.GeneratedImage{{URL: chat01ConversationImageURL}}})
	if err != nil || len(result.AttachmentRows) != 1 || len(result.Failures) != 1 || !errors.Is(result.Failures[0].Cause, ErrStorageQuotaExceeded) || len(repo.files) != 1 {
		t.Fatalf("mixed media exceeded shared quota: %#v %v", result, err)
	}
}

func TestChat01CanceledArtifactBatchCleansOnlyNewFiles(t *testing.T) {
	s, repo := newGeneratedFilesTestService(t, config.Config{})
	first, last := conversationChat01File("first.txt"), conversationChat01File("last.txt")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.llmClient = generatedFileReaderStub{open: func(_ context.Context, _ llm.RouteConfig, file llm.GeneratedFile) (*llm.GeneratedFileContent, error) {
		if file.ID == last.ID {
			cancel()
			return nil, context.Canceled
		}
		return chat01GeneratedContent([]byte("newly generated text"), "text/plain"), nil
	}}
	_, err := s.saveAssistantGeneratedFiles(ctx, assistantGeneratedFilesInput{UserID: 1, Route: llm.RouteConfig{Protocol: llm.AdapterChat01ChatCompletions}, Files: []llm.GeneratedFile{first, last}})
	if !errors.Is(err, ErrMessageGenerationCanceled) || len(repo.files) != 0 || len(repo.deleted) != 1 {
		t.Fatalf("canceled artifacts leaked: files=%v deleted=%v err=%v", repo.files, repo.deleted, err)
	}
}

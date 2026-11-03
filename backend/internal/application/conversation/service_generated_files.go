package conversation

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/textutil"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"go.uber.org/zap"
)

// Only adapter-confirmed source links are rewritten; ordinary web URLs are never
// downloaded or promoted to attachments by the conversation layer.
var generatedFileSourceLinkRE = regexp.MustCompile(`sandbox:/[^\s<>"()\[\]]+`)

type assistantGeneratedFilesInput struct {
	UserID             uint
	ConversationID     uint
	AssistantMessageID uint
	Route              llm.RouteConfig
	Content            string
	Files              []llm.GeneratedFile
}

type assistantGeneratedFilesResult struct {
	Content             string
	AttachmentRows      []model.Attachment
	AttachmentSnapshots []AttachmentInput
	CreatedFileIDs      []string
}

func mergeAssistantGeneratedFiles(existing, next []llm.GeneratedFile) []llm.GeneratedFile {
	merged := append([]llm.GeneratedFile(nil), existing...)
	indices := make(map[string]int, len(merged))
	for i, file := range merged {
		indices[file.ID] = i
	}
	for _, file := range next {
		if i, ok := indices[file.ID]; ok {
			seen := make(map[string]bool, len(merged[i].SourceLinks))
			for _, link := range merged[i].SourceLinks {
				seen[link] = true
			}
			for _, link := range file.SourceLinks {
				if !seen[link] {
					merged[i].SourceLinks = append(merged[i].SourceLinks, link)
					seen[link] = true
				}
			}
			continue
		}
		file.SourceLinks = append([]string(nil), file.SourceLinks...)
		indices[file.ID] = len(merged)
		merged = append(merged, file)
	}
	return merged
}

func (s *Service) saveAssistantGeneratedFiles(ctx context.Context, input assistantGeneratedFilesInput) (result *assistantGeneratedFilesResult, retErr error) {
	if len(input.Files) == 0 {
		return nil, nil
	}
	reader, ok := s.llmClient.(llm.GeneratedFileReader)
	if !ok || s.uploadSvc == nil {
		return nil, ErrGeneratedFileDownloadFailed
	}
	files := mergeAssistantGeneratedFiles(nil, input.Files)
	ids := make([]string, len(files))
	for i, file := range files {
		ids[i] = file.ID
	}
	if err := s.validateMessageFileCount(ids); err != nil {
		return nil, err
	}
	if err := s.uploadSvc.EnsureStorageAvailable(ctx, input.UserID); err != nil {
		return nil, err
	}
	result = &assistantGeneratedFilesResult{Content: input.Content}
	// A failed batch must not leave newly created, unreferenced files consuming
	// quota. Reused user files are never included in this cleanup list.
	createdFileIDs := make([]string, 0, len(files))
	defer func() {
		if retErr != nil {
			s.cleanupGeneratedFiles(ctx, input.UserID, createdFileIDs)
		}
	}()
	links := make(map[string]string)
	seenFiles := make(map[string]bool)
	for _, file := range files {
		if strings.TrimSpace(file.ID) == "" || strings.TrimSpace(file.FileName) == "" {
			return nil, ErrGeneratedFileDownloadFailed
		}
		content, err := reader.OpenGeneratedFile(ctx, input.Route, file)
		if err != nil {
			return nil, s.generatedFileFailure(ctx, input, "download", err)
		}
		if content == nil || content.Body == nil {
			return nil, ErrGeneratedFileDownloadFailed
		}
		uploaded, err := s.uploadSvc.UploadFile(ctx, appupload.UploadFileInput{
			UserID:       input.UserID,
			Purpose:      "generated_file",
			FileName:     file.FileName,
			MimeType:     content.MimeType,
			DeclaredSize: content.SizeBytes,
			Reader:       content.Body,
			// Leave MaxBytes unset: generated documents obey the same configured
			// file limits, MIME policy and quota transaction as ordinary uploads.
		})
		_ = content.Body.Close()
		if err != nil {
			if errors.Is(err, ErrFileTooLarge) || errors.Is(err, ErrStorageQuotaExceeded) || errors.Is(err, ErrMIMEBlocked) || errors.Is(err, ErrDangerousMIMEType) {
				return nil, err
			}
			return nil, s.generatedFileFailure(ctx, input, "save", err)
		}
		saved := uploaded.File
		if !uploaded.Reused {
			createdFileIDs = append(createdFileIDs, saved.FileID)
		}
		for _, link := range file.SourceLinks {
			if !strings.HasPrefix(link, "sandbox:/") {
				continue
			}
			target := "/api/v1/files/" + saved.FileID + "/content"
			if previous, exists := links[link]; exists && previous != target {
				// Different output files can have the same sandbox path. Keep the
				// cards downloadable, but never guess which file a link refers to.
				links[link] = ""
			} else if !exists {
				links[link] = target
			}
		}
		if seenFiles[saved.FileID] {
			continue
		}
		seenFiles[saved.FileID] = true
		kind := inferAttachmentKind(saved.DetectedMIME)
		result.AttachmentRows = append(result.AttachmentRows, model.Attachment{
			ConversationID: input.ConversationID, MessageID: input.AssistantMessageID, UserID: input.UserID,
			FileID: saved.FileID, Kind: kind, FileName: saved.FileName, MimeType: saved.DetectedMIME,
			FileSize: saved.SizeBytes, SHA256: saved.SHA256, StoragePath: saved.StoragePath,
			Status: "active", UploadedAt: time.Now(),
		})
		snapshots := attachmentsFromFiles([]model.FileObject{saved})
		snapshots[0].Kind = kind
		result.AttachmentSnapshots = append(result.AttachmentSnapshots, snapshots[0])
	}
	result.Content = generatedFileSourceLinkRE.ReplaceAllStringFunc(input.Content, func(link string) string {
		return textutil.FirstNonEmpty(links[link], link)
	})
	result.CreatedFileIDs = createdFileIDs
	return result, nil
}

func (s *Service) generatedFileFailure(ctx context.Context, input assistantGeneratedFilesInput, stage string, cause error) error {
	if ctx.Err() != nil {
		return ErrMessageGenerationCanceled
	}
	if s.logger != nil {
		fields := []zap.Field{zap.Uint("message_id", input.AssistantMessageID), zap.String("stage", stage)}
		var upstreamErr *llm.UpstreamError
		if errors.As(cause, &upstreamErr) {
			fields = append(fields, zap.Int("upstream_status", upstreamErr.StatusCode))
		}
		// Do not log upstream URLs, response bodies or credentials.
		s.logger.Warn("generated_file_save_failed", fields...)
	}
	return ErrGeneratedFileDownloadFailed
}

func (s *Service) cleanupGeneratedFiles(ctx context.Context, userID uint, fileIDs []string) {
	if len(fileIDs) == 0 || s.uploadSvc == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, fileID := range fileIDs {
		if _, _, err := s.uploadSvc.DeleteFileIfUnreferenced(cleanupCtx, userID, fileID); err != nil && s.logger != nil {
			s.logger.Warn("generated_file_cleanup_failed", zap.String("file_id", fileID))
		}
	}
}

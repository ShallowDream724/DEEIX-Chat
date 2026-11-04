package conversation

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	appcm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/contentmoderation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

// TemporaryChatAttachment 是随临时会话请求传入、且不进入持久化文件链路的附件。
type TemporaryChatAttachment struct {
	MessageIndex int
	FileName     string
	MimeType     string
	DeclaredSize int64
	Reader       io.Reader
}

type temporaryAttachmentContext struct {
	messages         []llm.Message
	documents        turnDocumentPlacement
	moderationImages []appcm.OutputImageSource
}

func (s *Service) prepareTemporaryAttachmentContext(
	ctx context.Context,
	input TemporaryChatInput,
	messages []llm.Message,
	budgets ...inputlimits.Limits,
) (temporaryAttachmentContext, error) {
	result := temporaryAttachmentContext{
		messages: append([]llm.Message(nil), messages...),
	}
	if len(input.Attachments) == 0 {
		return result, nil
	}
	if s.uploadSvc == nil {
		return result, ErrInvalidFileReference
	}

	cfg := s.cfg.Snapshot()
	limits := firstImageInputLimits(budgets)
	if len(input.Attachments) > TemporaryChatAttachmentLimit(cfg) {
		return result, ErrTooManyMessageFiles
	}
	documentsByMessage := make(map[int][]turnDocument)
	for _, item := range input.Attachments {
		file, err := s.uploadSvc.PrepareTemporaryFile(ctx, appupload.TemporaryFileInput{
			FileName:     item.FileName,
			MimeType:     item.MimeType,
			DeclaredSize: item.DeclaredSize,
			Reader:       item.Reader,
		})
		if err != nil {
			return result, err
		}
		processErr := func() error {
			defer file.Cleanup()
			switch file.FileCategory {
			case "image":
				reader, err := os.Open(file.AbsolutePath)
				if err != nil {
					return fmt.Errorf("%w: read temporary image", ErrFileNotFound)
				}
				data, readErr := io.ReadAll(io.LimitReader(reader, file.SizeBytes+1))
				closeErr := reader.Close()
				if readErr != nil || closeErr != nil {
					return fmt.Errorf("%w: read temporary image", ErrFileNotFound)
				}
				if file.SizeBytes <= 0 || int64(len(data)) != file.SizeBytes {
					return fmt.Errorf("%w: temporary image source size changed", ErrInvalidFileReference)
				}
				resized, mimeType := resizeImageIfNeeded(data, resolveImageMimeType(file.DetectedMIME), cfg.ImageMaxDimension)
				result.messages[item.MessageIndex] = addUserTurnParts(result.messages[item.MessageIndex], llm.ContentPart{
					Kind:          llm.ContentPartImage,
					MimeType:      mimeType,
					Data:          resized,
					FileName:      file.FileName,
					SourceFileIDs: []string{temporaryAttachmentContextID(file.SHA256)},
				})
				if err := validateConversationImageBudget(result.messages, limits); err != nil {
					return err
				}
				result.moderationImages = append(result.moderationImages, appcm.OutputImageSource{
					FileID:   temporaryAttachmentContextID(file.SHA256),
					Data:     resized,
					MimeType: mimeType,
					SHA256:   file.SHA256,
				})
			case "pdf", "word", "presentation", "excel", "text":
				if s.extractSvc == nil {
					return ErrInvalidFileReference
				}
				extracted, err := s.extractSvc.ExtractTemporaryFile(ctx, extraction.ExtractInput{
					File: domainconversation.FileObject{
						FileID:       file.FileID,
						FileName:     file.FileName,
						MimeType:     file.MimeType,
						DetectedMIME: file.DetectedMIME,
						FileCategory: file.FileCategory,
						SizeBytes:    file.SizeBytes,
						SHA256:       file.SHA256,
						StoragePath:  file.AbsolutePath,
					},
					PDFMaxPages:           cfg.FileFullContextPDFMaxPages,
					OCREngine:             cfg.ExtractOCREngine,
					ImageOCREnabled:       false,
					PDFOCRFallbackEnabled: cfg.ExtractPDFOCRFallbackEnabled,
				})
				if err != nil || strings.TrimSpace(extracted.Text) == "" {
					return fmt.Errorf("%w: temporary attachment extraction failed", ErrFileProcessingNotReady)
				}
				attachment := AttachmentInput{
					FileID:          temporaryAttachmentContextID(file.SHA256),
					Kind:            "file",
					FileName:        file.FileName,
					MimeType:        file.MimeType,
					DetectedMIME:    file.DetectedMIME,
					FileCategory:    file.FileCategory,
					FileSize:        file.SizeBytes,
					SHA256:          file.SHA256,
					PageCount:       extracted.PageCount,
					ExtractedText:   extracted.Text,
					Current:         item.MessageIndex == len(input.Messages)-1,
					MessageRole:     "user",
					ContextMode:     fileContextModeFull,
					ProcessingReady: true,
					ExtractStatus:   "success",
				}
				if !canUseAttachmentFullContext(attachment, cfg) {
					return ErrFileTooLargeForFullContext
				}
				// 与持久化会话一致：文件随所属轮次提供，而不是集中放在提示词开头。
				documentsByMessage[item.MessageIndex] = append(documentsByMessage[item.MessageIndex], turnDocument{
					Attachment: attachment,
					Access:     turnDocumentFull,
				})
			default:
				return ErrInvalidFileReference
			}
			return nil
		}()
		if processErr != nil {
			return result, processErr
		}
	}
	for index, message := range result.messages {
		documents := documentsByMessage[index]
		if len(documents) == 0 {
			continue
		}
		documentPart := turnDocumentsPart(documents)
		result.messages[index] = addUserTurnParts(message, documentPart)
		result.documents.TokenEstimate += estimateContentPartTokens(documentPart)
		for _, document := range documents {
			result.documents.FullAttachments = append(result.documents.FullAttachments, document.Attachment)
		}
	}
	return result, nil
}

func temporaryAttachmentContextID(sha string) string {
	normalized := strings.ToLower(strings.TrimSpace(sha))
	if len(normalized) > 32 {
		normalized = normalized[:32]
	}
	return "temporary_" + normalized
}

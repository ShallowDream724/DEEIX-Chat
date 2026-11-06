package conversation

import (
	"context"
	"io"
	"os"
	"strings"

	appcm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/contentmoderation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
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
	cleanup          func()
	messages         []llm.Message
	documents        turnDocumentPlacement
	moderationImages []appcm.OutputImageSource
}

func (s *Service) prepareTemporaryAttachmentContext(
	ctx context.Context,
	input TemporaryChatInput,
	messages []llm.Message,
	policy nativeInputPolicy,
) (result temporaryAttachmentContext, retErr error) {
	result = temporaryAttachmentContext{
		messages: append([]llm.Message(nil), messages...),
	}
	var stagedFiles []*appupload.PreparedTemporaryFile
	result.cleanup = func() {
		for _, file := range stagedFiles {
			file.Cleanup()
		}
	}
	defer func() {
		if retErr != nil {
			result.cleanup()
		}
	}()
	if len(input.Attachments) == 0 {
		return result, nil
	}
	if s.uploadSvc == nil {
		return result, ErrInvalidFileReference
	}

	cfg := s.cfg.Snapshot()
	if len(input.Attachments) > TemporaryChatAttachmentLimit(cfg) {
		return result, ErrTooManyMessageFiles
	}
	type stagedAttachment struct {
		messageIndex int
		file         *appupload.PreparedTemporaryFile
		attachment   AttachmentInput
	}
	staged := make([]stagedAttachment, 0, len(input.Attachments))
	candidates := make([]AttachmentInput, 0, len(input.Attachments))
	var imageCount, historicalImageCount int64
	for _, item := range input.Attachments {
		if item.MessageIndex < 0 || item.MessageIndex >= len(messages) || messages[item.MessageIndex].Role != "user" {
			return result, ErrInvalidFileReference
		}
		file, err := s.uploadSvc.PrepareTemporaryFile(ctx, appupload.TemporaryFileInput{
			FileName:     item.FileName,
			MimeType:     item.MimeType,
			DeclaredSize: item.DeclaredSize,
			Reader:       item.Reader,
		})
		if err != nil {
			return result, attachmentInputError(AttachmentInput{FileName: item.FileName, Current: item.MessageIndex == len(messages)-1}, err)
		}
		stagedFiles = append(stagedFiles, file)
		att := AttachmentInput{
			FileID: file.FileID, FileName: file.FileName, MimeType: file.MimeType, DetectedMIME: file.DetectedMIME,
			FileCategory: file.FileCategory, FileSize: file.SizeBytes, StoragePath: file.AbsolutePath, SHA256: file.SHA256,
			Current: item.MessageIndex == len(messages)-1, MessageRole: "user",
			Kind: inferAttachmentKind(file.DetectedMIME),
		}
		if att.FileCategory == fileCategoryImage {
			if policy.ImageUnsupported {
				return result, attachmentInputError(att, ErrFileUnsupportedByModel)
			}
			imageCount++
			if !att.Current {
				historicalImageCount++
			}
		}
		if isMediaAttachment(att) && !policy.accepts(att) {
			return result, attachmentInputError(att, ErrFileUnsupportedByModel)
		}
		if policy.accepts(att) && !isNativeInputCandidate(att) {
			return result, attachmentInputErrorWithReason(att, ErrFileProcessingNotReady, attachmentInputReadFailed)
		}
		staged = append(staged, stagedAttachment{messageIndex: item.MessageIndex, file: file, attachment: att})
		candidates = append(candidates, att)
	}
	limits := policy.InputLimits
	if limits.ImageCount > 0 && imageCount > int64(limits.ImageCount) {
		return result, attachmentInputError(AttachmentInput{Current: historicalImageCount <= int64(limits.ImageCount)},
			llm.NewInputLimitError("imageCount", imageCount, int64(limits.ImageCount)))
	}
	_, rejected := selectNativeInputsWithRejections(candidates, policy.withoutPDFPageBudget())
	if err := validateNativeSelection(candidates, policy, rejected); err != nil {
		return result, err
	}
	if policy.PDF && policy.budget().PDFPages > 0 {
		if s.extractSvc != nil {
			for i, att := range candidates {
				if nativeInputKindOf(att) == nativeInputPDF {
					pages, _ := s.extractSvc.ProbeTemporaryPDFPageCount(ctx, staged[i].file.AbsolutePath)
					candidates[i].PageCount = pages
					staged[i].attachment.PageCount = pages
				}
			}
		}
		_, rejected := selectNativeInputsWithRejections(candidates, policy)
		if err := validateNativeSelection(candidates, policy, rejected); err != nil {
			return result, err
		}
	}

	documentsByMessage := make(map[int][]turnDocument)
	imagesByMessage := make(map[int][]llm.ContentPart)
	var historicalImageBytes int64
	for _, item := range staged {
		att := item.attachment
		// Stable content identity is used in the prompt, while the staging IDs
		// above ensure each occurrence consumes the request budget.
		att.FileID = temporaryAttachmentContextID(att.SHA256)
		switch {
		case att.FileCategory == fileCategoryImage:
			data, err := readTemporaryAttachment(item.file)
			if err != nil {
				return result, attachmentInputErrorWithReason(att, err, attachmentInputReadFailed)
			}
			resized, mimeType := resizeImageIfNeeded(data, resolveImageMimeType(att.DetectedMIME), cfg.ImageMaxDimension)
			if limits.ImageSingleBytes > 0 && int64(len(resized)) > limits.ImageSingleBytes {
				return result, attachmentInputError(att, llm.NewInputLimitError("imageSingleBytes", int64(len(resized)), limits.ImageSingleBytes))
			}
			if !att.Current {
				historicalImageBytes += int64(len(resized))
			}
			imagesByMessage[item.messageIndex] = append(imagesByMessage[item.messageIndex], llm.ContentPart{
				Kind: llm.ContentPartImage, MimeType: mimeType, Data: resized, FileName: att.FileName, SourceFileIDs: []string{att.FileID},
			})
			result.moderationImages = append(result.moderationImages, appcm.OutputImageSource{
				FileID: att.FileID, Data: resized, MimeType: mimeType, SHA256: att.SHA256,
			})
		case policy.accepts(att):
			var data []byte
			if !policy.HTTPS {
				var err error
				data, err = readTemporaryAttachment(item.file)
				if err != nil {
					return result, attachmentInputErrorWithReason(att, err, attachmentInputReadFailed)
				}
			}
			part := policy.contextPart(newNativeInputPart(att, data))
			if policy.HTTPS {
				part.Reference = attachmentReference(att)
				part.Reference.TemporaryPath = item.file.AbsolutePath
			}
			att.ContextMode = fileContextModeNativeDocument
			documentsByMessage[item.messageIndex] = append(documentsByMessage[item.messageIndex], turnDocument{
				Attachment: att, Access: turnDocumentNative, Native: part,
			})
		default:
			switch att.FileCategory {
			case "pdf", "word", "presentation", "excel", "text":
			default:
				return result, attachmentInputError(att, ErrFileUnsupportedByModel)
			}
			if s.extractSvc == nil {
				return result, attachmentInputError(att, ErrFileProcessingNotReady)
			}
			extracted, err := s.extractSvc.ExtractTemporaryFile(ctx, extraction.ExtractInput{
				File: domainconversation.FileObject{
					FileID: att.FileID, FileName: att.FileName, MimeType: att.MimeType, DetectedMIME: att.DetectedMIME,
					FileCategory: att.FileCategory, SizeBytes: att.FileSize, SHA256: att.SHA256, StoragePath: att.StoragePath,
				},
				OCREngine:             cfg.ExtractOCREngine,
				PDFOCRFallbackEnabled: cfg.ExtractPDFOCRFallbackEnabled,
			})
			if err != nil || strings.TrimSpace(extracted.Text) == "" {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				return result, attachmentInputErrorWithReason(att, ErrFileProcessingNotReady, attachmentInputParseFailed)
			}
			att.PageCount, att.ExtractedText = extracted.PageCount, extracted.Text
			if err := checkAttachmentFullContext(att, cfg); err != nil {
				return result, attachmentInputError(att, err)
			}
			att.ContextMode, att.ProcessingReady, att.ExtractStatus = fileContextModeFull, true, "success"
			documentsByMessage[item.messageIndex] = append(documentsByMessage[item.messageIndex], turnDocument{
				Attachment: att, Access: turnDocumentFull,
			})
		}
	}
	for index, message := range result.messages {
		documents := documentsByMessage[index]
		var parts []llm.ContentPart
		if len(documents) != 0 {
			parts = append(parts, turnDocumentsPart(documents))
			for _, document := range documents {
				if document.Access == turnDocumentNative {
					parts = append(parts, document.Native)
					result.documents.NativeAttachments = append(result.documents.NativeAttachments, document.Attachment)
				} else {
					result.documents.FullAttachments = append(result.documents.FullAttachments, document.Attachment)
				}
			}
			for _, part := range parts {
				result.documents.TokenEstimate += estimateContentPartTokens(part)
			}
		}
		parts = append(parts, imagesByMessage[index]...)
		if len(parts) > 0 {
			result.messages[index] = addUserTurnParts(message, parts...)
		}
	}
	if err := validateConversationImageBudget(result.messages, limits); err != nil {
		return result, attachmentInputError(AttachmentInput{Current: historicalImageBytes <= limits.ImageBytes}, err)
	}
	return result, nil
}

func readTemporaryAttachment(file *appupload.PreparedTemporaryFile) ([]byte, error) {
	reader, err := os.Open(file.AbsolutePath)
	if err != nil {
		return nil, ErrFileNotFound
	}
	defer reader.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(reader, file.SizeBytes+1))
	if err != nil {
		return nil, ErrFileNotFound
	}
	if file.SizeBytes <= 0 || int64(len(data)) != file.SizeBytes {
		return nil, ErrInvalidFileReference
	}
	return data, nil
}

func temporaryAttachmentContextID(sha string) string {
	normalized := strings.ToLower(strings.TrimSpace(sha))
	if len(normalized) > 32 {
		normalized = normalized[:32]
	}
	return "temporary_" + normalized
}

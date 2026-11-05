package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/attachmentdelivery"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/google/uuid"
)

type attachmentDeliveryService interface {
	Available() bool
	IssueStored(context.Context, uint, string, string, time.Duration) (attachmentdelivery.Link, error)
	StagePrepared(context.Context, uint, string, []byte, string, string, string) (attachmentdelivery.Link, error)
	StageTemporary(context.Context, uint, io.Reader, string, string, string) (attachmentdelivery.Link, error)
	ReleaseRun(context.Context, string) error
}

func (s *Service) SetAttachmentDeliveryService(service attachmentDeliveryService) {
	s.attachmentDelivery = service
}
func (s *Service) attachmentHTTPSAvailable() bool {
	return s != nil && s.attachmentDelivery != nil && s.attachmentDelivery.Available()
}

func attachmentReference(att AttachmentInput) *llm.InputFileReference {
	return &llm.InputFileReference{FileID: strings.TrimSpace(att.FileID), SHA256: att.SHA256, SizeBytes: att.FileSize}
}

// prepareAttachmentTransport runs only after stable prompt accounting and traces.
// Never put its URLs back in the retained prompt or cache identity.
func (s *Service) prepareAttachmentTransport(ctx context.Context, protocol string, userID uint, input llm.GenerateInput) (llm.GenerateInput, func(), error) {
	if !promptPolicyForProtocol(protocol).OriginalAttachments || !s.attachmentHTTPSAvailable() {
		return input, func() {}, nil
	}
	runID := "attachment_" + uuid.NewString()
	cleanup := func() { _ = s.attachmentDelivery.ReleaseRun(context.WithoutCancel(ctx), runID) }
	input.Messages = cloneLLMMessages(input.Messages)
	latestUser := -1
	for i := range input.Messages {
		if input.Messages[i].Role == "user" {
			latestUser = i
		}
	}
	for mi := range input.Messages {
		input.Messages[mi].Parts = append([]llm.ContentPart(nil), input.Messages[mi].Parts...)
		for pi := range input.Messages[mi].Parts {
			part := &input.Messages[mi].Parts[pi]
			if part.Kind == llm.ContentPartText || part.Kind == llm.ContentPartFile {
				continue
			}
			if len(part.Data) == 0 && part.Reference == nil {
				continue
			}
			var link attachmentdelivery.Link
			var err error
			sourceID := ""
			if len(part.SourceFileIDs) > 0 {
				sourceID = part.SourceFileIDs[0]
			}
			if part.Reference != nil {
				sourceID = part.Reference.FileID
			}
			switch {
			case len(part.Data) > 0:
				link, err = s.attachmentDelivery.StagePrepared(ctx, userID, sourceID, part.Data, part.MimeType, part.FileName, runID)
			case part.Reference.TemporaryPath != "":
				var reader *os.File
				reader, err = os.Open(part.Reference.TemporaryPath)
				if err == nil {
					link, err = s.attachmentDelivery.StageTemporary(ctx, userID, reader, part.MimeType, part.FileName, runID)
					_ = reader.Close()
				}
			default:
				link, err = s.attachmentDelivery.IssueStored(ctx, userID, sourceID, runID, 0)
			}
			if err != nil {
				cleanup()
				return llm.GenerateInput{}, func() {}, attachmentInputErrorWithReason(AttachmentInput{FileID: sourceID, FileName: part.FileName, Current: mi == latestUser}, ErrFileProcessingNotReady, attachmentInputReadFailed)
			}
			expected := part.ByteSize()
			if expected > 0 && expected != link.SizeBytes {
				cleanup()
				return llm.GenerateInput{}, func() {}, ErrInvalidFileReference
			}
			ref := llm.InputFileReference{FileID: sourceID, SizeBytes: link.SizeBytes, URL: link.URL}
			if len(part.Data) > 0 {
				sum := sha256.Sum256(part.Data)
				ref.SHA256 = hex.EncodeToString(sum[:])
			} else {
				ref.SHA256 = part.Reference.SHA256
			}
			part.Reference = &ref
			part.Data = nil
		}
	}
	return input, cleanup, nil
}

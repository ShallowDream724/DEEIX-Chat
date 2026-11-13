package conversation

import (
	"context"
	"errors"
	"strings"
	"time"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/google/uuid"
)

// SaveUserMessageRevision creates a standalone user branch without generating a reply.
// revisionID is a stable client-generated UUID so a lost response can be retried safely.
func (s *Service) SaveUserMessageRevision(ctx context.Context, userID uint, sourcePublicID, content string, fileIDs []string, expectedUpdatedAt time.Time, revisionID string) (*model.Message, error) {
	sourcePublicID = strings.TrimSpace(sourcePublicID)
	parsedID, err := uuid.Parse(strings.TrimSpace(revisionID))
	if err != nil || parsedID == uuid.Nil || expectedUpdatedAt.IsZero() {
		return nil, ErrInvalidMessageContent
	}
	content = strings.TrimSpace(content)
	if content == "" && len(fileIDs) == 0 {
		return nil, ErrInvalidMessageContent
	}
	source, err := s.repo.GetMessageByPublicIDForUser(ctx, userID, sourcePublicID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrMessageNotFound
		}
		return nil, err
	}
	if source.Role != "user" {
		return nil, ErrMessageEditTargetInvalid
	}
	resolved, err := s.resolveAttachments(ctx, userID, fileIDs)
	if err != nil {
		return nil, err
	}
	if content == "" && len(resolved) == 0 {
		return nil, ErrInvalidMessageContent
	}
	now := time.Now()
	message := &model.Message{
		PublicID: normalizePublicID(parsedID.String()), ConversationID: source.ConversationID,
		UserID: userID, Role: "user", ContentType: source.ContentType, Content: content,
		BranchReason: "edit", Status: "success", EditedAt: &now,
		Attachments: marshalAttachmentSnapshots(resolved),
	}
	rows := make([]model.Attachment, 0, len(resolved))
	for _, item := range resolved {
		rows = append(rows, model.Attachment{FileID: item.FileID, Kind: normalizeAttachmentKind(item.Kind, item.MimeType), FileName: item.FileName, MimeType: item.MimeType, FileSize: item.FileSize, SHA256: item.SHA256, StoragePath: item.StoragePath, Status: "active", MetaJSON: item.MetaJSON, UploadedAt: now})
	}
	if err = s.repo.CreateUserMessageRevision(ctx, sourcePublicID, expectedUpdatedAt, message, rows); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			return nil, ErrMessageNotFound
		case errors.Is(err, repository.ErrConflict):
			return nil, ErrMessageEditStateInvalid
		case errors.Is(err, repository.ErrFileNotFound):
			return nil, ErrInvalidFileReference
		default:
			return nil, err
		}
	}
	return s.repo.GetMessageByPublicIDForUser(ctx, userID, message.PublicID)
}

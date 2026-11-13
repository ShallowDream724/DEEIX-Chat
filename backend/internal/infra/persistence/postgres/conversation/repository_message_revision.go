package conversation

import (
	"context"
	"errors"
	"strings"
	"time"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateUserMessageRevision keeps the source and its descendants immutable.
// Lock order matches message append/delete: conversation, source, then files.
func (r *Repo) CreateUserMessageRevision(ctx context.Context, sourcePublicID string, expectedUpdatedAt time.Time, message *domain.Message, attachments []domain.Attachment) error {
	if message == nil || message.UserID == 0 || message.PublicID == "" || expectedUpdatedAt.IsZero() {
		return repository.ErrInvalidInput
	}
	return translateError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conversation models.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", message.ConversationID, message.UserID).First(&conversation).Error; err != nil {
			return err
		}
		var source models.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ? AND conversation_id = ? AND user_id = ?", sourcePublicID, conversation.ID, message.UserID).First(&source).Error; err != nil {
			return err
		}
		var existing models.Message
		if err := tx.Where("public_id = ?", message.PublicID).First(&existing).Error; err == nil {
			if existing.UserID != message.UserID || existing.ConversationID != conversation.ID || existing.SourceMessageID == nil || *existing.SourceMessageID != source.ID || existing.Role != "user" || existing.Content != message.Content {
				return repository.ErrConflict
			}
			var existingRows []models.Attachment
			if err := tx.Where("message_id = ? AND status = ?", existing.ID, "active").Order("id ASC").Find(&existingRows).Error; err != nil {
				return err
			}
			if len(existingRows) != len(attachments) {
				return repository.ErrConflict
			}
			for i := range existingRows {
				if existingRows[i].FileID != attachments[i].FileID {
					return repository.ErrConflict
				}
			}
			*message = toMessageDomain(existing)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if source.Role != "user" || strings.EqualFold(source.Status, "pending") || !source.UpdatedAt.Equal(expectedUpdatedAt) {
			return repository.ErrConflict
		}
		var pending int64
		if err := tx.Model(&models.Message{}).Where("conversation_id = ? AND status = ?", conversation.ID, "pending").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return repository.ErrConflict
		}
		if err := tx.Model(&models.ConversationRun{}).Where("conversation_id = ? AND status IN ?", conversation.ID, []string{"pending", "queued", "running"}).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return repository.ErrConflict
		}
		if source.ParentMessageID != nil {
			if err := lockParentMessageForAppend(tx, *source.ParentMessageID); err != nil {
				return err
			}
		}
		if err := lockActiveFileObjectsForAttachments(tx, message.UserID, attachments); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return repository.ErrFileNotFound
			}
			return err
		}
		message.ParentMessageID = source.ParentMessageID
		message.SourceMessageID = &source.ID
		message.ParentPublicID = source.ParentPublicID
		message.SourcePublicID = source.PublicID
		entity := toMessageModel(message)
		if err := tx.Create(&entity).Error; err != nil {
			return err
		}
		message.ID = entity.ID
		if len(attachments) > 0 {
			rows := make([]models.Attachment, 0, len(attachments))
			for _, item := range attachments {
				item.ConversationID = conversation.ID
				item.MessageID = entity.ID
				item.UserID = message.UserID
				rows = append(rows, toAttachmentModel(&item))
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return tx.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{"message_count": gorm.Expr("message_count + ?", 1), "last_response_id": "", "last_prompt_fingerprint": ""}).Error
	}))
}

package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestUserMessageRevisionPreservesOldBranchAndFileReferences(t *testing.T) {
	db, repo, messages := createMessageDeleteFixture(t)
	ctx := context.Background()
	file := models.FileObject{UserID: 1, FileID: "revision-file", FileName: "report.txt", MimeType: "text/plain", Status: "active", StoragePath: "revision-file"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	for _, message := range []models.Message{messages[0], messages[2]} {
		if err := db.Create(&models.Attachment{ConversationID: message.ConversationID, MessageID: message.ID, UserID: 1, FileID: file.FileID, FileName: file.FileName, Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	source, err := repo.GetMessageByPublicIDForUser(ctx, 1, messages[2].PublicID)
	if err != nil {
		t.Fatal(err)
	}
	revision := domain.Message{PublicID: "revision_empty_files", UserID: 1, ConversationID: source.ConversationID, Role: "user", Content: "updated text", Status: "success", BranchReason: "edit"}
	if err := repo.CreateUserMessageRevision(ctx, source.PublicID, source.UpdatedAt, &revision, nil); err != nil {
		t.Fatal(err)
	}
	if revision.ParentMessageID == nil || *revision.ParentMessageID != messages[1].ID || revision.SourceMessageID == nil || *revision.SourceMessageID != source.ID {
		t.Fatalf("incorrect revision ancestry: %#v", revision)
	}
	ancestors, err := repo.ListMessageAncestors(ctx, source.ConversationID, revision.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ancestor := range ancestors {
		if ancestor.ID == source.ID || ancestor.ID == messages[3].ID {
			t.Fatalf("old turn leaked into revision ancestors: %#v", ancestors)
		}
	}
	var oldReply models.Message
	if err := db.First(&oldReply, messages[3].ID).Error; err != nil || oldReply.ParentMessageID == nil || *oldReply.ParentMessageID != source.ID {
		t.Fatalf("old reply changed: %#v, %v", oldReply, err)
	}
	var references int64
	db.Model(&models.Attachment{}).Where("file_id = ? AND status = ?", file.FileID, "active").Count(&references)
	if references != 2 {
		t.Fatalf("original and ancestor references = %d, want 2", references)
	}
	var activeFile models.FileObject
	if err := db.Where("file_id = ? AND status = ?", file.FileID, "active").First(&activeFile).Error; err != nil {
		t.Fatalf("file removed: %v", err)
	}
	var saved models.Conversation
	db.First(&saved, source.ConversationID)
	if saved.MessageCount != 7 {
		t.Fatalf("count = %d, want 7", saved.MessageCount)
	}
	// A lost response retries the same real message identity, even after a new run began.
	if err := db.Model(&models.Message{}).Where("id = ?", messages[5].ID).Update("status", "pending").Error; err != nil {
		t.Fatal(err)
	}
	retry := domain.Message{PublicID: revision.PublicID, UserID: 1, ConversationID: source.ConversationID, Role: "user", Content: "updated text", Status: "success", BranchReason: "edit"}
	if err := repo.CreateUserMessageRevision(ctx, source.PublicID, source.UpdatedAt, &retry, nil); err != nil || retry.ID != revision.ID {
		t.Fatalf("retry = %#v, %v", retry, err)
	}
	db.First(&saved, source.ConversationID)
	if saved.MessageCount != 7 {
		t.Fatalf("retry duplicated count = %d", saved.MessageCount)
	}
	retry.Content = "different payload"
	if err := repo.CreateUserMessageRevision(ctx, source.PublicID, source.UpdatedAt, &retry, nil); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("changed retry error = %v", err)
	}
}

func TestUserMessageRevisionRejectsPermissionStateAndMissingFilesAtomically(t *testing.T) {
	for _, scenario := range []string{"foreign", "pending", "stale", "file", "running"} {
		t.Run(scenario, func(t *testing.T) {
			db, repo, messages := createMessageDeleteFixture(t)
			source := messages[2]
			if err := db.First(&source, source.ID).Error; err != nil {
				t.Fatal(err)
			}
			revision := domain.Message{PublicID: "revision_rejected", UserID: 1, ConversationID: source.ConversationID, Role: "user", Content: "edited", Status: "success", BranchReason: "edit"}
			expected := source.UpdatedAt
			var attachments []domain.Attachment
			switch scenario {
			case "foreign":
				revision.UserID = 2
			case "pending":
				db.Model(&models.Message{}).Where("id = ?", source.ID).Update("status", "pending")
			case "stale":
				expected = expected.Add(-time.Second)
			case "file":
				attachments = []domain.Attachment{{FileID: "missing", Status: "active"}}
			case "running":
				db.Create(&models.ConversationRun{RunID: "revision-running", UserID: 1, ConversationID: source.ConversationID, Status: "running"})
			}
			if err := repo.CreateUserMessageRevision(context.Background(), source.PublicID, expected, &revision, attachments); err == nil {
				t.Fatal("expected rejection")
			}
			var count int64
			db.Model(&models.Message{}).Where("public_id = ?", revision.PublicID).Count(&count)
			if count != 0 {
				t.Fatalf("rejected revision was persisted")
			}
		})
	}
}

func TestUserMessageRevisionCopiesOnlyTheFinalAttachmentCollection(t *testing.T) {
	db, repo, messages := createMessageDeleteFixture(t)
	file := models.FileObject{UserID: 1, FileID: "replacement-file", FileName: "new.png", MimeType: "image/png", Status: "active", StoragePath: "new.png"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	source := messages[2]
	if err := db.First(&source, source.ID).Error; err != nil {
		t.Fatal(err)
	}
	revision := domain.Message{PublicID: "revision_attachments_only", UserID: 1, ConversationID: source.ConversationID, Role: "user", Content: "", Status: "success", BranchReason: "edit"}
	rows := []domain.Attachment{{FileID: file.FileID, FileName: file.FileName, MimeType: file.MimeType, Kind: "image", Status: "active"}}
	if err := repo.CreateUserMessageRevision(context.Background(), source.PublicID, source.UpdatedAt, &revision, rows); err != nil {
		t.Fatal(err)
	}
	var saved []models.Attachment
	db.Where("message_id = ?", revision.ID).Find(&saved)
	if len(saved) != 1 || saved[0].FileID != file.FileID || saved[0].UserID != 1 {
		t.Fatalf("final attachments = %#v", saved)
	}
}

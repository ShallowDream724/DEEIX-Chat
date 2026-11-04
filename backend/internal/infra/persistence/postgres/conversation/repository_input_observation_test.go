package conversation

import (
	"testing"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestEditedMiddleTurnInputObservationExcludesReplacedContinuation(t *testing.T) {
	db := openConversationRepositoryTestDB(t)
	if err := db.AutoMigrate(&models.Message{}, &models.ChatContextRecord{}); err != nil {
		t.Fatal(err)
	}
	root, prior, oldUser, oldAnswer := uint(1), uint(2), uint(3), uint(4)
	messages := []models.Message{
		{BaseModel: models.BaseModel{ID: root}, ConversationID: 7, UserID: 1, PublicID: "root", Role: "user"},
		{BaseModel: models.BaseModel{ID: prior}, ConversationID: 7, UserID: 1, PublicID: "prior", Role: "assistant", ParentMessageID: &root, RunID: "prior-run"},
		{BaseModel: models.BaseModel{ID: oldUser}, ConversationID: 7, UserID: 1, PublicID: "old-user", Role: "user", ParentMessageID: &prior},
		{BaseModel: models.BaseModel{ID: oldAnswer}, ConversationID: 7, UserID: 1, PublicID: "old-answer", Role: "assistant", ParentMessageID: &oldUser, RunID: "old-run"},
		{BaseModel: models.BaseModel{ID: 5}, ConversationID: 7, UserID: 1, PublicID: "edited-user", Role: "user", ParentMessageID: &prior},
		{BaseModel: models.BaseModel{ID: 6}, ConversationID: 7, UserID: 1, PublicID: "future-answer", Role: "assistant", ParentMessageID: &oldAnswer, RunID: "future-run"},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id  uint
		run string
	}{{2, "prior-run"}, {4, "old-run"}, {6, "future-run"}} {
		if err := db.Create(&models.ChatContextRecord{RecordType: chatContextRecordArtifact, ConversationID: 7, UserID: 1, MessageID: item.id, RunID: item.run, Kind: string(model.ContextArtifactInputObservation), SourceID: item.run}).Error; err != nil {
			t.Fatal(err)
		}
	}
	items, err := NewRepo(db).ListRecentContextArtifacts(t.Context(), repository.ContextArtifactListFilter{Scope: repository.HistoricalMessageScope{ConversationID: 7, UserID: 1, LeafMessageID: 5}, Kinds: []model.ContextArtifactKind{model.ContextArtifactInputObservation}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MessageID != prior {
		t.Fatalf("edited branch selected non-ancestor observation: %#v", items)
	}
}

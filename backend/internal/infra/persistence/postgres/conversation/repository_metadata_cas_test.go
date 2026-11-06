package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestUpdateConversationMetadataTitleCASProtectsManualRename(t *testing.T) {
	db := openConversationRepositoryTestDB(t)
	repo := NewRepo(db)
	ctx := context.Background()
	item := models.Conversation{UserID: 1, PublicID: "metadata_cas", Title: "Fallback", LabelsJSON: "[]", SessionKey: "metadata_cas", Status: "active"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	expectedTitle := item.Title
	if _, err := repo.UpdateConversationTitleByPublicID(ctx, item.UserID, item.PublicID, "Manual title"); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.UpdateConversationMetadata(ctx, item.ID, repository.ConversationMetadataPatch{Title: "Generated", ReplaceableTitles: []string{"Fallback"}, ExpectedTitle: &expectedTitle})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Manual title" {
		t.Fatalf("manual rename overwritten: %q", updated.Title)
	}
	// Matching title CAS still applies after an unrelated timestamp advance.
	if _, err := repo.UpdateConversationTitleByPublicID(ctx, item.UserID, item.PublicID, expectedTitle); err != nil {
		t.Fatal(err)
	}
	updated, err = repo.UpdateConversationMetadata(ctx, item.ID, repository.ConversationMetadataPatch{Title: "Generated", ReplaceableTitles: []string{"Fallback"}, ExpectedTitle: &expectedTitle})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Generated" {
		t.Fatalf("matching CAS failed: %q", updated.Title)
	}
}

func TestUpdateConversationMetadataTitleCASAllowsConcurrentConversationChanges(t *testing.T) {
	for _, change := range []string{"continue_chat", "star", "model"} {
		t.Run(change, func(t *testing.T) {
			db := openConversationRepositoryTestDB(t)
			repo := NewRepo(db)
			ctx := context.Background()
			item := models.Conversation{UserID: 1, PublicID: "metadata_" + change, Title: "Fallback", LabelsJSON: "[]", SessionKey: "metadata_" + change, Status: "active"}
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
			version := item.UpdatedAt
			expectedTitle := item.Title
			db.NowFunc = func() time.Time { return version.Add(time.Second) }
			var err error
			switch change {
			case "continue_chat":
				err = repo.IncrementMessageCount(ctx, item.ID, 1)
			case "star":
				_, err = repo.UpdateConversationStarByPublicID(ctx, item.UserID, item.PublicID, true)
			case "model":
				err = repo.UpdateConversationModel(ctx, item.ID, "new-model", "provider")
			}
			if err != nil {
				t.Fatal(err)
			}
			updated, err := repo.UpdateConversationMetadata(ctx, item.ID, repository.ConversationMetadataPatch{Title: "Generated", ReplaceableTitles: []string{"Fallback"}, ExpectedTitle: &expectedTitle})
			if err != nil {
				t.Fatal(err)
			}
			want := "Generated"
			if updated.Title != want {
				t.Fatalf("after %s: title=%q want=%q", change, updated.Title, want)
			}
		})
	}
}

func TestUpdateConversationMetadataTitleCASBlocksDifferentEligibleTitle(t *testing.T) {
	db := openConversationRepositoryTestDB(t)
	repo := NewRepo(db)
	ctx := context.Background()
	item := models.Conversation{UserID: 1, PublicID: "metadata_cas_race", Title: "new chat", LabelsJSON: "[]", SessionKey: "metadata_cas_race", Status: "active"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	expectedTitle := item.Title
	// Both strings satisfy the legacy replaceable-title predicate; only the SQL
	// title equality guard can protect the intervening manual rename here.
	if _, err := repo.UpdateConversationTitleByPublicID(ctx, item.UserID, item.PublicID, "新对话"); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.UpdateConversationMetadata(ctx, item.ID, repository.ConversationMetadataPatch{Title: "Generated", ExpectedTitle: &expectedTitle})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "新对话" {
		t.Fatalf("different eligible title overwritten: %q", updated.Title)
	}
}

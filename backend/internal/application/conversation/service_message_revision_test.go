package conversation

import (
	"context"
	"errors"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"testing"
	"time"
)

type messageRevisionRepository struct {
	repository.ConversationRepository
	source     model.Message
	files      []model.FileObject
	created    *model.Message
	rows       []model.Attachment
	writeError error
}

func (r *messageRevisionRepository) GetMessageByPublicIDForUser(_ context.Context, userID uint, publicID string) (*model.Message, error) {
	if userID != r.source.UserID {
		return nil, repository.ErrNotFound
	}
	if publicID == r.source.PublicID {
		source := r.source
		return &source, nil
	}
	if r.created != nil && publicID == r.created.PublicID {
		result := *r.created
		return &result, nil
	}
	return nil, repository.ErrNotFound
}
func (r *messageRevisionRepository) GetActiveFileObjectsByIDs(context.Context, uint, []string) ([]model.FileObject, error) {
	return r.files, nil
}
func (r *messageRevisionRepository) CreateUserMessageRevision(_ context.Context, _ string, _ time.Time, message *model.Message, rows []model.Attachment) error {
	if r.writeError != nil {
		return r.writeError
	}
	r.created = message
	r.rows = rows
	return nil
}

func TestSaveUserMessageRevisionValidatesFinalCollectionAndCreatesNoAssistant(t *testing.T) {
	for _, scenario := range []struct {
		name, content string
		ids           []string
		files         []model.FileObject
		wantError     error
	}{
		{name: "text with every attachment removed", content: "new text", ids: []string{}},
		{name: "attachment only", ids: []string{"new-file"}, files: []model.FileObject{{FileID: "new-file", FileName: "photo.png", DetectedMIME: "image/png"}}},
		{name: "empty draft", content: "  ", ids: []string{}, wantError: ErrInvalidMessageContent},
		{name: "empty references are not attachments", ids: []string{"  "}, wantError: ErrInvalidMessageContent},
		{name: "foreign or missing attachment", content: "text", ids: []string{"foreign"}, wantError: ErrInvalidFileReference},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo := &messageRevisionRepository{source: model.Message{ID: 10, UserID: 1, ConversationID: 4, PublicID: "source", Role: "user", Status: "success"}, files: scenario.files}
			service := &Service{repo: repo}
			result, err := service.SaveUserMessageRevision(context.Background(), 1, "source", scenario.content, scenario.ids, time.Now(), "4dcc136c-cc03-41fc-9ebd-eb5f50d2b0a0")
			if !errors.Is(err, scenario.wantError) {
				t.Fatalf("error = %v, want %v", err, scenario.wantError)
			}
			if scenario.wantError != nil {
				if repo.created != nil {
					t.Fatal("invalid draft reached write")
				}
				return
			}
			if result.Role != "user" || result.BranchReason != "edit" || result.Status != "success" || result.RunID != "" {
				t.Fatalf("revision = %#v", result)
			}
			if len(repo.rows) != len(scenario.files) {
				t.Fatalf("attachment rows = %#v", repo.rows)
			}
		})
	}
}

func TestSaveUserMessageRevisionRejectsForeignAndAssistantTargetsAndMapsConflicts(t *testing.T) {
	for _, scenario := range []struct {
		userID                uint
		role                  string
		writeError, wantError error
	}{
		{userID: 2, role: "user", wantError: ErrMessageNotFound},
		{userID: 1, role: "assistant", wantError: ErrMessageEditTargetInvalid},
		{userID: 1, role: "user", writeError: repository.ErrConflict, wantError: ErrMessageEditStateInvalid},
		{userID: 1, role: "user", writeError: repository.ErrFileNotFound, wantError: ErrInvalidFileReference},
	} {
		repo := &messageRevisionRepository{source: model.Message{ID: 10, UserID: 1, ConversationID: 4, PublicID: "source", Role: scenario.role}, writeError: scenario.writeError}
		service := &Service{repo: repo}
		_, err := service.SaveUserMessageRevision(context.Background(), scenario.userID, "source", "text", nil, time.Now(), "4dcc136c-cc03-41fc-9ebd-eb5f50d2b0a0")
		if !errors.Is(err, scenario.wantError) {
			t.Fatalf("error = %v, want %v", err, scenario.wantError)
		}
	}
}

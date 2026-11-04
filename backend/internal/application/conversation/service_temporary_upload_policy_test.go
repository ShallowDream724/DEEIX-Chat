package conversation

import (
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestTemporaryChatAttachmentCountUsesConfiguredBoundary(t *testing.T) {
	input := TemporaryChatInput{UserID: 1, SessionID: "s", ClientRunID: "r", Model: "m", Messages: []TemporaryChatMessage{{Role: "user", Content: "question"}}}
	for i := 0; i < 21; i++ {
		input.Attachments = append(input.Attachments, TemporaryChatAttachment{MessageIndex: 0, FileName: "notes.txt", Reader: strings.NewReader("notes")})
	}
	if err := ValidateTemporaryChatInput(input); err != ErrTooManyMessageFiles {
		t.Fatalf("default limit error = %v", err)
	}
	if err := ValidateTemporaryChatInput(input, 21); err != nil {
		t.Fatalf("configured exact boundary rejected: %v", err)
	}
	if err := ValidateTemporaryChatInput(input, 20); err != ErrTooManyMessageFiles {
		t.Fatalf("over boundary error = %v", err)
	}
	if TemporaryChatAttachmentLimit(config.Config{}) != 20 || TemporaryChatRequestLimit(config.Config{}) != 8<<20 {
		t.Fatal("empty runtime must preserve temporary defaults")
	}
}

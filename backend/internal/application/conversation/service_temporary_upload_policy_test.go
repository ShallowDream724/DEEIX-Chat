package conversation

import (
	"errors"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestTemporaryChatAttachmentCountUsesConfiguredBoundary(t *testing.T) {
	input := TemporaryChatInput{UserID: 1, SessionID: "s", ClientRunID: "r", Model: "m", Messages: []TemporaryChatMessage{{Role: "user", Content: "question"}}}
	for i := 0; i < 21; i++ {
		input.Attachments = append(input.Attachments, TemporaryChatAttachment{MessageIndex: 0, FileName: "notes.txt", Reader: strings.NewReader("notes")})
	}
	err := ValidateTemporaryChatInput(input)
	if !errors.Is(err, ErrTooManyMessageFiles) {
		t.Fatalf("default limit error = %v", err)
	}
	requireContextBudgetError(t, err, "temporaryFileCount")
	if err := ValidateTemporaryChatInput(input, 21); err != nil {
		t.Fatalf("configured exact boundary rejected: %v", err)
	}
	requireContextBudgetError(t, ValidateTemporaryChatInput(input, 20), "temporaryFileCount")
	if TemporaryChatAttachmentLimit(config.Config{}) != 20 || TemporaryChatRequestLimit(config.Config{}) != 8<<20 {
		t.Fatal("empty runtime must preserve temporary defaults")
	}
}

func TestTemporaryChatReportsResourceLimitsWithoutCropping(t *testing.T) {
	input := TemporaryChatInput{UserID: 1, SessionID: "s", ClientRunID: "r", Model: "m"}
	input.Messages = make([]TemporaryChatMessage, temporaryChatMaxMessages+1)
	requireContextBudgetError(t, ValidateTemporaryChatInput(input), "temporaryMessageCount")
	message := strings.Repeat("世", temporaryChatMaxMessageChars)
	input.Messages = []TemporaryChatMessage{{Role: "user", Content: message + "界"}}
	requireContextBudgetError(t, ValidateTemporaryChatInput(input), "temporaryTextLength")
	input.Messages = []TemporaryChatMessage{
		{Role: "user", Content: message}, {Role: "assistant", Content: message},
		{Role: "user", Content: message}, {Role: "assistant", Content: message},
		{Role: "user", Content: message},
	}
	if err := ValidateTemporaryChatInput(input); err != nil {
		t.Fatalf("exact single-message and aggregate Unicode limits: %v", err)
	}
	input.Messages = append(input.Messages, TemporaryChatMessage{Role: "assistant", Content: "answer"}, TemporaryChatMessage{Role: "user", Content: "again"})
	requireContextBudgetError(t, ValidateTemporaryChatInput(input), "temporaryTextLength")
}

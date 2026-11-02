package llm

import (
	"errors"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestGeminiInlineRequestBudget(t *testing.T) {
	for _, size := range []int{0, 99_999_999, 100_000_000} {
		if err := validateGeminiInlineRequestSize(size); err != nil {
			t.Fatalf("request of %d bytes rejected: %v", size, err)
		}
	}
	// The provider's 100 MB limit is decimal, not 100 MiB.
	for _, size := range []int{100_000_001, 100 * 1024 * 1024} {
		if err := validateGeminiInlineRequestSize(size); !errors.Is(err, portllm.ErrInputTooLarge) {
			t.Fatalf("request of %d bytes: want input limit error, got %v", size, err)
		}
	}
}

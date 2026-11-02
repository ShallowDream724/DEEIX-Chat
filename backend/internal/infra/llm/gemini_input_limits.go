package llm

import (
	"fmt"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// The inline limit applies to the complete encoded request, including history,
// images, tools and text, not just the raw files selected by the conversation layer.
// https://ai.google.dev/gemini-api/docs/generate-content/file-input-methods
const maxGeminiInlineRequestBytes = 100_000_000

func validateGeminiInlineRequestSize(size int) error {
	if size > maxGeminiInlineRequestBytes {
		return fmt.Errorf("%w: Gemini inline request exceeds %d bytes", portllm.ErrInputTooLarge, maxGeminiInlineRequestBytes)
	}
	return nil
}

package llm

import (
	"errors"
	"fmt"
	"testing"
)

func TestInputLimitDetailsPreservesMeasurementsWithoutUntrustedContext(t *testing.T) {
	err := fmt.Errorf("private upstream https://example.invalid secret: %w", NewInputLimitError("imageCount", 3, 2))
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatal("budget identity lost")
	}
	detail := InputLimitDetails(err)
	if detail == nil || detail.Dimension != "imageCount" || detail.Actual != 3 || detail.Limit != 2 {
		t.Fatalf("unexpected detail: %#v", detail)
	}
	if InputLimitDetails(NewInputLimitError("https://secret.invalid", 3, 2)) != nil {
		t.Fatal("untrusted dimension exposed")
	}
}

package conversation

import (
	"errors"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestNativeSelectionReportsExactRejectedBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		budget        nativeInputBudget
		request       int64
		pdf           bool
		actual, limit int64
	}{
		{"nativeCount", nativeInputBudget{Count: 1}, 0, false, 2, 1},
		{"nativeBytes", nativeInputBudget{TotalBytes: 150}, 0, false, 200, 150},
		{"nonPDFBytes", nativeInputBudget{NonPDFBytes: 150}, 0, false, 200, 150},
		{"requestBytes", nativeInputBudget{}, 200, false, 272, 200},
		{"pdfSingleBytes", nativeInputBudget{PDFSingleBytes: 99}, 0, true, 100, 99},
		{"pdfBytes", nativeInputBudget{PDFBytes: 150}, 0, true, 200, 150},
		{"pdfPages", nativeInputBudget{PDFPages: 1}, 0, true, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attachments := []AttachmentInput{nativeMedia("first", "audio/mpeg", 100), nativeMedia("second", "audio/mpeg", 100)}
			if tc.pdf {
				attachments = []AttachmentInput{nativePDF("first", 100, 1, true), nativePDF("second", 100, 1, true)}
			}
			policy := nativeInputPolicy{budgetConfigured: true, Budget: tc.budget, PDF: true, AudioMIMEs: openAINativeAudioMIMEs, InputLimits: inputlimits.Limits{RequestBytes: tc.request}}
			selected, rejected := selectNativeInputsWithRejections(attachments, policy)
			detail := llm.InputLimitDetails(rejected["first"])
			if detail == nil || detail.Dimension != tc.name || detail.Actual != tc.actual || detail.Limit != tc.limit {
				t.Fatalf("unexpected rejection: %#v (%v)", detail, rejected)
			}
			if !tc.pdf {
				native := nativeInputs{Policy: policy, Rejected: rejected, Parts: map[string]llm.ContentPart{}}
				for _, att := range selected {
					native.Parts[att.FileID] = llm.ContentPart{Kind: llm.ContentPartAudio}
				}
				err := validateCurrentNativeReads(attachments, native)
				if !errors.Is(err, ErrFileTooLarge) || llm.InputLimitDetails(err) == nil {
					t.Fatalf("typed reason lost before send: %v", err)
				}
			}
		})
	}
}

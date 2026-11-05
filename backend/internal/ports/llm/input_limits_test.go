package llm

import (
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
	"testing"
)

func TestAttachmentBudgetsMeasureReferencesAndRepeatedHistory(t *testing.T) {
	image := ContentPart{Kind: ContentPartImage, Reference: &InputFileReference{SizeBytes: 4}}
	file := ContentPart{Kind: ContentPartAttachment, Reference: &InputFileReference{SizeBytes: 6}}
	input := GenerateInput{Messages: []Message{{Parts: []ContentPart{image, file}}, {Parts: []ContentPart{file}}}}
	for _, tc := range []struct {
		limits    inputlimits.Limits
		dimension string
		actual    int64
	}{
		{inputlimits.Limits{AttachmentCount: 3, AttachmentBytes: 16, AttachmentSingleBytes: 6}, "", 0},
		{inputlimits.Limits{AttachmentCount: 2}, "attachmentCount", 3},
		{inputlimits.Limits{AttachmentBytes: 15}, "attachmentBytes", 16},
		{inputlimits.Limits{AttachmentSingleBytes: 5}, "attachmentSingleBytes", 6},
		{inputlimits.Limits{NativeBytes: 11}, "nativeBytes", 12},
		{inputlimits.Limits{ImageSingleBytes: 3}, "imageSingleBytes", 4},
	} {
		err := ValidateInputMedia(tc.limits, input)
		if tc.dimension == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		detail := InputLimitDetails(err)
		if detail == nil || detail.Dimension != tc.dimension || detail.Actual != tc.actual {
			t.Fatalf("want %s/%d; got %v", tc.dimension, tc.actual, err)
		}
	}
}

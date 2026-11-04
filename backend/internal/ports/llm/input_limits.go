package llm

import (
	"errors"
	"fmt"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

// ErrInputTooLarge means a request exceeds the adapter's input budget before it
// reaches the upstream. It must not be treated as an upstream availability error.
var ErrInputTooLarge = errors.New("model input exceeds request limit")

// InputLimitError contains only trusted numeric budget measurements. It never
// includes file names, request bodies, upstream endpoints or credentials.
type InputLimitError struct {
	Dimension string `json:"dimension"`
	Actual    int64  `json:"actual"`
	Limit     int64  `json:"limit"`
}

func (e *InputLimitError) Error() string {
	return fmt.Sprintf("%s: %s actual %d exceeds limit %d", ErrInputTooLarge, e.Dimension, e.Actual, e.Limit)
}
func (e *InputLimitError) Unwrap() error { return ErrInputTooLarge }

func NewInputLimitError(dimension string, actual, limit int64) error {
	return &InputLimitError{Dimension: dimension, Actual: actual, Limit: limit}
}

// InputLimitDetails returns an allowlisted public representation, even if an
// untrusted error chain happens to contain an InputLimitError.
func InputLimitDetails(err error) *InputLimitError {
	var detail *InputLimitError
	if !errors.As(err, &detail) {
		return nil
	}
	switch detail.Dimension {
	case "temporaryMessageCount", "temporaryTextLength", "temporaryFileCount", "temporaryRequestBytes":
		if detail.Limit > 0 && detail.Actual >= -1 {
			value := *detail
			return &value
		}
	case "messageFileCount", "contextTokens", "currentTurnTokens", "parsedTextBytes", "parsedTextTokens", "requestBytes", "provider.requestBytes", "imageSingleBytes", "imageCount", "imageBytes", "nativeCount", "nativeBytes", "pdfSingleBytes", "pdfBytes", "pdfPages", "nonPDFBytes", "provider.imageEncodedBytes", "provider.imageWidth", "provider.imageHeight":
		if detail.Limit > 0 && detail.Actual >= -1 {
			value := *detail
			return &value
		}
	}
	return nil
}

// ValidateInputMedia counts every occurrence in the model context. Repeated
// attachments consume the budget each time, including retained provider state.
// ImageSingleBytes applies to each prepared image before Base64 encoding.
func ValidateInputMedia(limits inputlimits.Limits, input GenerateInput) error {
	var imageCount, imageBytes, nativeCount, nativeBytes, pdfBytes, pdfPages, nonPDFBytes int64
	for _, message := range input.Messages {
		for _, part := range message.Parts {
			size := int64(len(part.Data))
			switch part.Kind {
			case ContentPartImage:
				imageCount++
				imageBytes += size
				if err := checkMediaBudget("imageSingleBytes", size, limits.ImageSingleBytes); err != nil {
					return err
				}
			case ContentPartDocument:
				nativeCount++
				nativeBytes += size
				pdfBytes += size
				if part.PageCount > 0 {
					pdfPages += int64(part.PageCount)
				}
				if err := checkMediaBudget("pdfSingleBytes", size, limits.PDFSingleBytes); err != nil {
					return err
				}
			case ContentPartAudio, ContentPartVideo:
				nativeCount++
				nativeBytes += size
				nonPDFBytes += size
			}
		}
	}
	for _, budget := range []struct {
		name          string
		actual, limit int64
	}{
		{"imageCount", imageCount, int64(limits.ImageCount)},
		{"imageBytes", imageBytes, limits.ImageBytes},
		{"nativeCount", nativeCount, int64(limits.NativeCount)},
		{"nativeBytes", nativeBytes, limits.NativeBytes},
		{"pdfBytes", pdfBytes, limits.PDFBytes},
		{"pdfPages", pdfPages, int64(limits.PDFPages)},
		{"nonPDFBytes", nonPDFBytes, limits.NonPDFBytes},
	} {
		if err := checkMediaBudget(budget.name, budget.actual, budget.limit); err != nil {
			return err
		}
	}
	return nil
}

func checkMediaBudget(name string, actual, limit int64) error {
	if limit > 0 && actual > limit {
		return NewInputLimitError(name, actual, limit)
	}
	return nil
}

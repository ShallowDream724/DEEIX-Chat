package llm

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
	_ "golang.org/x/image/webp"
)

// EffectiveInputMediaLimits resolves administrator budgets and the verified
// first-party ceilings for the actual model/window. Selection, readiness and
// send-time checks can share these effective byte/count/page values. It does not
// replace validation of encoded request size or decoded image dimensions.
func EffectiveInputMediaLimits(route RouteConfig) inputlimits.Limits {
	limits := inputlimits.Resolve("", route.Protocol, route.BaseURL, 0)
	if route.InputLimits != nil {
		limits = *route.InputLimits
	}
	rules := inputlimits.Provider(route.Protocol, route.BaseURL, route.ContextWindowTokens)
	limits.RequestBytes = smallerInputLimit(limits.RequestBytes, rules.RequestBytes)
	limits.ImageCount = int(smallerInputLimit(int64(limits.ImageCount), int64(rules.ImageCount)))
	if rules.ImageEncodedBytes > 0 {
		// An encoded Base64 string has a multiple-of-four length. This exact raw
		// bound avoids estimating it as a floating-point 4/3 multiplier.
		limits.ImageSingleBytes = smallerInputLimit(limits.ImageSingleBytes, rules.ImageEncodedBytes/4*3)
	}
	limits.PDFSingleBytes = smallerInputLimit(limits.PDFSingleBytes, rules.PDFSingleBytes)
	limits.PDFBytes = smallerInputLimit(limits.PDFBytes, rules.PDFBytes)
	limits.PDFPages = int(smallerInputLimit(int64(limits.PDFPages), int64(rules.PDFPages)))
	return limits
}

func smallerInputLimit(left, right int64) int64 {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}

// ValidateProviderInput applies service constraints that an administrator budget
// cannot relax. Call it with the full retained context, including stateful calls.
// ContextWindowTokens must describe the actual model capability; unknown Claude
// windows conservatively allow 100 images and 100 PDF pages.
func ValidateProviderInput(route RouteConfig, input GenerateInput) error {
	rules := inputlimits.Provider(route.Protocol, route.BaseURL, route.ContextWindowTokens)
	limits := inputlimits.Limits{ImageCount: rules.ImageCount, PDFSingleBytes: rules.PDFSingleBytes, PDFBytes: rules.PDFBytes, PDFPages: rules.PDFPages}
	if err := ValidateInputMedia(limits, input); err != nil {
		if rules.ConservativeUnknownWindow {
			return fmt.Errorf("%w (conservative first-party limit: model context window is unknown)", err)
		}
		return fmt.Errorf("first-party provider constraint: %w", err)
	}
	if rules.ImageEncodedBytes == 0 && rules.ImageMaxDimension == 0 {
		return nil
	}
	for _, message := range input.Messages {
		for _, part := range message.Parts {
			if part.Kind != ContentPartImage || len(part.Data) == 0 {
				continue
			}
			if err := checkMediaBudget("provider.imageEncodedBytes", int64(base64.StdEncoding.EncodedLen(len(part.Data))), rules.ImageEncodedBytes); err != nil {
				return err
			}
			if rules.ImageMaxDimension > 0 {
				config, _, err := image.DecodeConfig(bytes.NewReader(part.Data))
				if err != nil {
					return fmt.Errorf("cannot validate provider image dimensions: %w", err)
				}
				if config.Width > rules.ImageMaxDimension {
					return NewInputLimitError("provider.imageWidth", int64(config.Width), int64(rules.ImageMaxDimension))
				}
				if config.Height > rules.ImageMaxDimension {
					return NewInputLimitError("provider.imageHeight", int64(config.Height), int64(rules.ImageMaxDimension))
				}
			}
		}
	}
	return nil
}

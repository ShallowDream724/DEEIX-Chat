package llm

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func providerTestImage(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestEffectiveInputMediaLimitsCombinesBudgetsAndFirstPartyRules(t *testing.T) {
	configured := inputlimits.Limits{ImageCount: 500, ImageSingleBytes: 9_000_000, PDFPages: 500, RequestBytes: 200_000_000}
	route := RouteConfig{Protocol: AdapterAnthropicMessages, BaseURL: "https://api.anthropic.com", InputLimits: &configured}
	got := EffectiveInputMediaLimits(route)
	if got.ImageCount != 100 || got.ImageSingleBytes != 7_500_000 || got.PDFPages != 100 || got.RequestBytes != 128_000_000 {
		t.Fatalf("unknown model effective bounds: %+v", got)
	}
	route.ContextWindowTokens = 1_000_000
	got = EffectiveInputMediaLimits(route)
	if got.ImageCount != 500 || got.PDFPages != 500 {
		t.Fatalf("operator smaller limits were lost: %+v", got)
	}
	route.BaseURL = "https://thirdparty.example"
	got = EffectiveInputMediaLimits(route)
	if got != configured {
		t.Fatalf("thirdparty acquired first-party limits: %+v", got)
	}
}

func TestClaudeProviderCountsUseReliableWindowAndActualHost(t *testing.T) {
	imagePart := ContentPart{Kind: ContentPartImage, Data: providerTestImage(t, 1, 1)}
	input := GenerateInput{Messages: []Message{{Role: "user"}}}
	for range 101 {
		input.Messages[0].Parts = append(input.Messages[0].Parts, imagePart)
	}
	route := RouteConfig{Protocol: AdapterAnthropicMessages, BaseURL: "https://api.anthropic.com", ContextWindowTokens: 200_000}
	if err := ValidateProviderInput(route, input); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("200k should reject 101 images: %v", err)
	}
	route.ContextWindowTokens = 1_000_000
	if err := ValidateProviderInput(route, input); err != nil {
		t.Fatalf("1M should allow 101 images: %v", err)
	}
	route.ContextWindowTokens = 0
	if err := ValidateProviderInput(route, input); !errors.Is(err, ErrInputTooLarge) || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown should explain conservative bound: %v", err)
	}
	route.BaseURL = "https://proxy.example"
	if err := ValidateProviderInput(route, input); err != nil {
		t.Fatalf("third-party inherited first-party constraint: %v", err)
	}
}

func TestClaudeProviderPDFPagesDependOnWindow(t *testing.T) {
	input := GenerateInput{Messages: []Message{{Parts: []ContentPart{{Kind: ContentPartDocument, PageCount: 101, Data: []byte("pdf")}}}}}
	route := RouteConfig{Protocol: AdapterAnthropicMessages, BaseURL: "https://api.anthropic.com", ContextWindowTokens: 999_999}
	if err := ValidateProviderInput(route, input); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("sub1M should reject101 pages: %v", err)
	}
	route.ContextWindowTokens = 1_000_000
	if err := ValidateProviderInput(route, input); err != nil {
		t.Fatalf("1M should allow101 pages: %v", err)
	}
	input.Messages[0].Parts[0].PageCount = 601
	if err := ValidateProviderInput(route, input); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("601 pages should fail: %v", err)
	}
}

func TestClaudeProviderEncodedImageBytesAndDimensions(t *testing.T) {
	route := RouteConfig{Protocol: AdapterAnthropicMessages, BaseURL: "https://api.anthropic.com", ContextWindowTokens: 1_000_000}
	data := append(providerTestImage(t, 1, 1), make([]byte, 7_500_000)...)
	input := GenerateInput{Messages: []Message{{Parts: []ContentPart{{Kind: ContentPartImage, Data: data[:7_500_000]}}}}}
	if err := ValidateProviderInput(route, input); err != nil {
		t.Fatalf("10M encoded boundary should pass: %v", err)
	}
	input.Messages[0].Parts[0].Data = data[:7_500_001]
	if err := ValidateProviderInput(route, input); !errors.Is(err, ErrInputTooLarge) || !strings.Contains(err.Error(), "imageEncodedBytes") {
		t.Fatalf("encoded overage: %v", err)
	}
	input.Messages[0].Parts[0].Data = providerTestImage(t, 8001, 1)
	if err := ValidateProviderInput(route, input); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("8001px should fail: %v", err)
	}
	imagePart := ContentPart{Kind: ContentPartImage, Data: providerTestImage(t, 3072, 1)}
	input.Messages[0].Parts = nil
	for range 21 {
		input.Messages[0].Parts = append(input.Messages[0].Parts, imagePart)
	}
	if err := ValidateProviderInput(route, input); err != nil {
		t.Fatalf("3000px safe recommendation must not become a fabricated hard limit: %v", err)
	}
}

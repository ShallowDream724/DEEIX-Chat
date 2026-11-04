package conversation

import (
	"fmt"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestConfiguredZeroNativeBudgetsDoNotRestoreLegacyCaps(t *testing.T) {
	route := &channel.ResolvedRoute{
		UpstreamID: 17, Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://relay.example.test/v1",
		ModelCapabilitiesJSON: `{"inputModalities":["pdf"]}`,
	}
	configuration := `{"upstreams":{"17":{"nativeCount":0,"nativeBytes":0,"pdfBytes":0,"pdfSingleBytes":0,"pdfPages":0}}}`
	files := make([]AttachmentInput, 7)
	for index := range files {
		files[index] = nativePDF(fmt.Sprintf("pdf-%d", index), 3*1024*1024, 20, true)
	}
	if selected := selectNativeInputs(files, resolveNativeInputPolicy(route, configuration)); len(selected) != len(files) {
		t.Fatalf("explicitly disabled native budgets selected %d of %d files", len(selected), len(files))
	}
	route.UpstreamID = 18
	if selected := selectNativeInputs(files, resolveNativeInputPolicy(route, configuration)); len(selected) >= len(files) {
		t.Fatal("another upstream unexpectedly inherited this upstream's limits")
	}
}

func TestNativePreviewCountsBase64AgainstRequestBudget(t *testing.T) {
	route := &channel.ResolvedRoute{
		UpstreamID: 17, Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: []string{"pdf"},
	}
	configuration := `{"upstreams":{"17":{"requestBytes":30}}}`
	files := []AttachmentInput{nativePDF("older", 12, 1, true), nativePDF("newer", 12, 1, true)}
	selected := selectNativeInputs(files, resolveNativeInputPolicy(route, configuration))
	if len(selected) != 1 || selected[0].FileID != "newer" {
		t.Fatalf("two 16-byte encoded files cannot fit a 30-byte request: %+v", selected)
	}
}

func TestNativePreviewKeepsOfficialPDFLimitWhenOperatorDisablesCaps(t *testing.T) {
	route := &channel.ResolvedRoute{
		UpstreamID: 17, Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://api.openai.com/v1", CatalogInputModalities: []string{"pdf"},
	}
	configuration := `{"upstreams":{"17":{"nativeBytes":0,"pdfBytes":0,"pdfSingleBytes":0}}}`
	files := []AttachmentInput{nativePDF("too-large", 50_000_000, 1, true)}
	if selected := selectNativeInputs(files, resolveNativeInputPolicy(route, configuration)); len(selected) != 0 {
		t.Fatal("operator settings cannot disable the official per-PDF size limit")
	}
}

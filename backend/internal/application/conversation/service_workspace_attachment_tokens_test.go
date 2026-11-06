package conversation

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/extraction/engines"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestChat01WorkspaceFilesKeepTextImageAndMediaGuards(t *testing.T) {
	policy := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterChat01ChatCompletions})
	parts := []llm.ContentPart{
		policy.contextPart(newNativeInputPart(nativePDF("report", 1024, 100, true), []byte("pdf"))),
		policy.contextPart(llm.ContentPart{Kind: llm.ContentPartAudio, FileName: "audio.wav", DurationSeconds: 10_000}),
		policy.contextPart(llm.ContentPart{Kind: llm.ContentPartVideo, FileName: "video.mp4", DurationSeconds: 10_000}),
		policy.contextPart(llm.ContentPart{Kind: llm.ContentPartAttachment, FileName: "archive.zip", Reference: &llm.InputFileReference{SizeBytes: 10_000_000}}),
	}
	for _, part := range parts {
		if estimate := estimateContentPartTokens(part); estimate <= 0 || estimate > 100 {
			t.Fatalf("workspace %s must count a manifest, not pages/duration/bytes: %d", part.Kind, estimate)
		}
	}
	image := policy.contextPart(llm.ContentPart{Kind: llm.ContentPartImage})
	if estimateContentPartTokens(image) != 255 || image.WorkspaceFile {
		t.Fatal("image estimation was disabled")
	}
	input := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Parts: append(parts, image)}}}
	budget := contextBudgetPolicy{limit: 100_000}
	if err := budget.evaluate(input, estimateGenerateInputTokens(input)).err(); err != nil {
		t.Fatalf("workspace PDF was incorrectly rejected: %v", err)
	}
	if detail := llm.InputLimitDetails(llm.ValidateInputMedia(inputlimits.Limits{PDFPages: 99}, input)); detail == nil || detail.Dimension != "pdfPages" {
		t.Fatal("workspace semantics bypassed PDF page protection")
	}
	if detail := llm.InputLimitDetails(llm.ValidateInputMedia(inputlimits.Limits{AttachmentBytes: 1}, input)); detail == nil || detail.Dimension != "attachmentBytes" {
		t.Fatal("workspace semantics bypassed attachment byte protection")
	}
	input.Messages[0].Parts = append(input.Messages[0].Parts, llm.ContentPart{Kind: llm.ContentPartFile, Text: strings.Repeat("中文", 100_000)})
	if detail := llm.InputLimitDetails(budget.evaluate(input, estimateGenerateInputTokens(input)).err()); detail == nil || detail.Dimension != "currentTurnTokens" {
		t.Fatal("actual parsed text bypassed context protection")
	}
}

func TestWorkspaceEstimateDoesNotLeakAcrossRoutesOrReuseOldBaseline(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	data := []byte("%PDF-1.7 original")
	if _, err := store.Put(t.Context(), "files/report", bytes.NewReader(data), objectstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{storeProvider: &conversationTestStoreProvider{store: store}}
	attachment := nativePDF("report", int64(len(data)), 100, true)
	cache := newNativeInputCache()
	chat01 := &channel.ResolvedRoute{Protocol: llm.AdapterChat01ChatCompletions}
	google := &channel.ResolvedRoute{Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: []string{"text", "pdf"}}
	workspace := svc.resolveNativeInputs(t.Context(), chat01, []AttachmentInput{attachment}, cache).Parts["report"]
	direct := svc.resolveNativeInputs(t.Context(), google, []AttachmentInput{attachment}, cache).Parts["report"]
	if !workspace.WorkspaceFile || direct.WorkspaceFile || cache.parts["report"].WorkspaceFile {
		t.Fatal("route-specific semantics mutated shared attachment cache")
	}
	if estimateContentPartTokens(direct) != 200_000 || estimateContentPartTokens(workspace) >= 100_000 {
		t.Fatal("native document and workspace file estimates were conflated")
	}
	input := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Parts: []llm.ContentPart{direct}}}}
	old := describeContextInput("same-route", input, nil)
	old.InputTokens = 500
	input.Messages[0].Parts[0] = workspace
	current := describeContextInput("same-route", input, nil)
	if _, matched := old.estimate(current); matched {
		t.Fatal("old page-based estimate reused across estimation semantics")
	}
	meter := contextInputMeter{identity: "same-route"}
	meter.record(current, llm.Usage{InputTokens: 60_000})
	input.Messages = append(input.Messages, llm.Message{Role: "assistant", Content: "summary"}, llm.Message{Role: "user", Content: "continue"})
	if got, want := meter.estimate(input), int64(60_000)+estimateGenerateInputTokens(input)-current.EstimatedTokens; got != want {
		t.Fatalf("reported whole-input usage must override file manifest estimate: got %d want %d", got, want)
	}
}

func TestTemporaryChat01UsesSameWorkspaceEstimate(t *testing.T) {
	pdf := longTemporaryPDF(t)
	cfg := config.NewRuntime(config.Config{})
	svc := &Service{cfg: cfg,
		uploadSvc:  appupload.NewServiceWithRuntime(cfg, nil, nil, appupload.Hooks{}, appupload.ErrorSet{}, ""),
		extractSvc: extraction.NewServiceWithRuntime(cfg, extraction.EngineFactories{Builtin: engines.Builtin{}})}
	policy := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterChat01ChatCompletions})
	result, err := svc.prepareTemporaryAttachmentContext(t.Context(), TemporaryChatInput{Attachments: []TemporaryChatAttachment{
		{MessageIndex: 0, FileName: "report.pdf", MimeType: "application/pdf", Reader: bytes.NewReader(pdf)},
	}}, []llm.Message{{Role: "user", Content: "Read this report"}}, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer result.cleanup()
	var found bool
	for _, part := range result.messages[0].Parts {
		if part.Kind == llm.ContentPartDocument {
			found = part.WorkspaceFile && part.PageCount == 21 && bytes.Equal(part.Data, pdf)
		}
	}
	input := llm.GenerateInput{Messages: result.messages}
	if err := (contextBudgetPolicy{limit: 10_000}).evaluate(input, estimateGenerateInputTokens(input)).err(); !found || err != nil {
		t.Fatalf("temporary file bypassed shared estimate policy or lost bytes: found=%v error=%v", found, err)
	}
}

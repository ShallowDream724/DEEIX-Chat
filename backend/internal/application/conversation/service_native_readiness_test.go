package conversation

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	appextraction "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	appprocessing "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/processing"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type nativeReadinessRepo struct {
	repository.ConversationRepository
	owner uint
	files []model.FileObject
}

func (r nativeReadinessRepo) GetActiveFileObjectsByIDs(_ context.Context, user uint, ids []string) ([]model.FileObject, error) {
	if user != r.owner {
		return nil, nil
	}
	result := make([]model.FileObject, 0)
	for _, id := range ids {
		for _, file := range r.files {
			if file.FileID == id {
				result = append(result, file)
			}
		}
	}
	return result, nil
}

type nativeReadinessRoutes struct {
	routeResolver
	routes []*channel.ResolvedRoute
	err    error
	input  channel.ResolveRouteInput
}

func (r *nativeReadinessRoutes) PreviewRoutes(_ context.Context, input channel.ResolveRouteInput) ([]*channel.ResolvedRoute, error) {
	r.input = input
	return r.routes, r.err
}

func TestNativeReadinessChecksAllRoutesAndFileOwnership(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	data := []byte("%PDF-1.7 scan")
	if _, err := store.Put(t.Context(), "files/scan", bytes.NewReader(data), objectstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	routes := &nativeReadinessRoutes{routes: []*channel.ResolvedRoute{{Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: []string{"pdf"}}}}
	service := &Service{cfg: config.NewRuntime(config.Config{}), routeResolver: routes,
		repo:          nativeReadinessRepo{owner: 1, files: []model.FileObject{{FileID: "scan", FileCategory: "pdf", SizeBytes: int64(len(data)), PageCount: 1, StoragePath: "files/scan", MimeType: "application/pdf", ProcessingStatus: "pending"}}},
		storeProvider: &conversationTestStoreProvider{store: store},
	}
	input := NativeReadinessInput{UserID: 1, Model: "pdf-model", FileIDs: []string{"scan"}}
	result, err := service.GetNativeReadiness(t.Context(), input)
	if err != nil || len(result.Files) != 1 || !result.Files[0].Eligible || result.Files[0].Reason != "" {
		t.Fatalf("pending PDF must be natively ready: %#v, %v", result, err)
	}
	if routes.input.UserID != 1 || routes.input.Scope != channel.RouteScopeUser || routes.input.PlatformModelName != "pdf-model" {
		t.Fatalf("missing model authorization input: %#v", routes.input)
	}
	routes.routes = append(routes.routes, &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, BaseURL: "https://relay.example.com", CatalogInputModalities: []string{"pdf"}})
	result, err = service.GetNativeReadiness(t.Context(), input)
	if err != nil || result.Files[0].Eligible {
		t.Fatalf("heterogeneous routes cannot promise native readiness: %#v, %v", result, err)
	}
	input.UserID = 2
	if _, err = service.GetNativeReadiness(t.Context(), input); !errors.Is(err, ErrInvalidFileReference) {
		t.Fatalf("another user's file must be rejected, got %v", err)
	}
	input.UserID = 1
	routes.err = channel.ErrModelAccessDenied
	if _, err = service.GetNativeReadiness(t.Context(), input); !errors.Is(err, ErrModelAccessDenied) {
		t.Fatalf("model permission denial must propagate, got %v", err)
	}
}

func TestNativePendingFilesBypassExtractionAndRAG(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	data := []byte("%PDF-1.7 pending")
	if _, err := store.Put(t.Context(), "files/pending", bytes.NewReader(data), objectstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	// No processing service: touching WaitUntilReady would panic. A successful
	// hydration proves pending native files never enter that waiting path.
	service := &Service{storeProvider: &conversationTestStoreProvider{store: store}}
	att := nativePDF("pending", int64(len(data)), 1, true)
	att.ProcessingStatus, att.ExtractedText, att.EmbedStatus = "pending", "", "ready"
	native := service.resolveNativeInputs(t.Context(), &channel.ResolvedRoute{Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: []string{"pdf"}}, []AttachmentInput{att}, nil)
	hydrated, err := service.hydrateAttachmentsForSend(t.Context(), 1, markNativeAttachments([]AttachmentInput{att}, native), nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := buildConversationFileContextPlan(hydrated, "rag", config.Config{RAGEnabled: true, EmbeddingEnabled: true}, "", "", true)
	if len(plan.RAGAttachments) != 0 || len(plan.FullAttachments) != 0 || len(plan.Attachments) != 1 {
		t.Fatalf("native inputs must be excluded from extraction contexts: %#v", plan)
	}
}

func TestOrdinaryPendingFilesStillWaitForExtraction(t *testing.T) {
	service := &Service{processingSvc: appprocessing.NewServiceWithRuntime(appprocessing.Dependencies{
		Repository: failedFileProcessingRepository{file: model.FileObject{FileID: "pending", ProcessingStatus: "pending"}},
	})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.hydrateAttachmentsForSend(ctx, 1, []AttachmentInput{{FileID: "pending", FileCategory: "document", Current: true}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ordinary files must enter extraction waiting, got %v", err)
	}
}

func TestNativeReadFailureAndBudgetCannotSilentlyDropCurrentPDF(t *testing.T) {
	service := &Service{}
	att := nativePDF("missing", 100, 1, true)
	route := &channel.ResolvedRoute{Protocol: llm.AdapterGoogleGenerateContent, BaseURL: "https://generativelanguage.googleapis.com", CatalogInputModalities: []string{"pdf"}}
	if _, err := service.buildMessageRoutePrompt(t.Context(), route, messageRoutePromptInput{ConversationFiles: []AttachmentInput{att}}); !errors.Is(err, ErrFileProcessingNotReady) {
		t.Fatalf("read failure must stop generation, got %v", err)
	}
	att.FileSize, att.ExtractedText, att.ContextMode = 50_000_001, "", fileContextModeSkipped
	if _, err := service.buildMessageRoutePrompt(t.Context(), route, messageRoutePromptInput{ConversationFiles: []AttachmentInput{att}}); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("over-budget PDF without parsed fallback must stop generation, got %v", err)
	}
}

func TestCurrentMediaOverBudgetDoesNotWaitForExtraction(t *testing.T) {
	att := nativeMedia("large-audio", "audio/mpeg", 80_000_000)
	att.ContextMode = ""
	policy := resolveNativeInputPolicy(&channel.ResolvedRoute{
		Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: []string{"audio"},
	})
	if err := validateRequiredNativeInputs([]AttachmentInput{att}, nativeInputs{Policy: policy}); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("oversized media must report its budget instead of waiting for extraction: %v", err)
	}
}

func TestCurrentUnsupportedMediaStopsBeforeExtractionWait(t *testing.T) {
	for _, mime := range []string{"audio/mpeg", "video/mp4"} {
		att := nativeMedia("media", mime, 100)
		att.ContextMode = ""
		// This is the first send-stage validation, before hydration and planning.
		if err := validateRequiredNativeInputs([]AttachmentInput{att}, nativeInputs{}); !errors.Is(err, ErrFileUnsupportedByModel) {
			t.Fatalf("unsupported current %s must fail immediately, got %v", mime, err)
		}
		att.Current = false
		if err := validateRequiredNativeInputs([]AttachmentInput{att}, nativeInputs{}); !errors.Is(err, ErrFileUnsupportedByModel) {
			t.Fatalf("historical %s must have a readable input path, got %v", mime, err)
		}
	}
}

func TestNativeOfficialBudgets(t *testing.T) {
	// Isolate provider constraints from administrator defaults.
	config := `{"defaults":{"nativeBytes":0,"pdfBytes":0,"pdfSingleBytes":0,"pdfPages":0}}`
	openai := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://api.openai.com/v1", CatalogInputModalities: []string{"pdf"}}, config)
	gemini := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterGoogleGenerateContent, BaseURL: "https://generativelanguage.googleapis.com", CatalogInputModalities: []string{"pdf", "audio"}}, config)
	for _, tc := range []struct {
		name   string
		policy nativeInputPolicy
		files  []AttachmentInput
		want   int
	}{
		{"openai larger than old 8MiB", openai, []AttachmentInput{nativePDF("large", 49_999_999, 201, true)}, 1},
		{"openai strict single boundary", openai, []AttachmentInput{nativePDF("limit", 50_000_000, 1, true)}, 0},
		{"openai combined boundary", openai, []AttachmentInput{nativePDF("first", 25_000_000, 1, true), nativePDF("second", 25_000_000, 1, true)}, 2},
		{"openai combined exceeded", openai, []AttachmentInput{nativePDF("first", 25_000_001, 1, true), nativePDF("second", 25_000_000, 1, true)}, 0},
		{"gemini single boundary", gemini, []AttachmentInput{nativePDF("limit", 50_000_000, 200, true)}, 1},
		{"gemini single exceeded", gemini, []AttachmentInput{nativePDF("large", 50_000_001, 1, true)}, 0},
		{"gemini multiple PDFs use inline request budget", gemini, []AttachmentInput{nativePDF("first", 30_000_000, 100, true), nativePDF("second", 30_000_000, 100, true)}, 2},
		{"gemini has no administrator page cap by default", gemini, []AttachmentInput{nativePDF("first", 100, 101, true), nativePDF("second", 100, 100, true)}, 2},
		{"gemini unknown pages need no budget metadata", gemini, []AttachmentInput{nativePDF("unknown", 100, 0, true)}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(selectNativeInputs(tc.files, tc.policy)); got != tc.want {
				t.Fatalf("selected %d, want %d", got, tc.want)
			}
		})
	}
}

type nativePDFMetadataParser struct {
	appextraction.BuiltinParser
	pages int
	calls int
}

func (p *nativePDFMetadataParser) DetectPDFPageCount(string) int { p.calls++; return p.pages }

func TestNativeUnknownPDFPagesUseMetadataOnly(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	data := []byte("pdf-page-tree")
	if _, err := store.Put(t.Context(), "files/unknown", bytes.NewReader(data), objectstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	provider := &conversationTestStoreProvider{store: store}
	parser := &nativePDFMetadataParser{pages: 201}
	extract := appextraction.NewServiceWithRuntime(config.NewRuntime(config.Config{}), appextraction.EngineFactories{Builtin: parser})
	extract.SetObjectStoreProvider(provider)
	service := &Service{extractSvc: extract, storeProvider: provider, cfg: config.NewRuntime(config.Config{InputLimitsJSON: `{"defaults":{"pdfPages":200}}`})}
	att := nativePDF("unknown", int64(len(data)), 0, true)
	route := &channel.ResolvedRoute{Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: []string{"pdf"}}
	if got := service.resolveNativeInputs(t.Context(), route, []AttachmentInput{att}, nil); len(got.Parts) != 0 || parser.calls != 1 {
		t.Fatalf("201 pages must fail local 200-page budget after metadata-only probe: %#v, calls=%d", got, parser.calls)
	}
	parser.pages = 200
	cache := newNativeInputCache()
	got := service.resolveNativeInputs(t.Context(), route, []AttachmentInput{att}, cache)
	if len(got.Parts) != 1 || got.Parts[att.FileID].PageCount != 200 {
		t.Fatalf("200-page PDF must be readable without text/OCR: %#v", got)
	}
	calls := parser.calls
	_ = service.resolveNativeInputs(t.Context(), route, []AttachmentInput{att}, cache)
	if parser.calls != calls {
		t.Fatal("the per-send cache must reuse known page metadata")
	}
	parser.pages = 0
	if got := service.resolveNativeInputs(t.Context(), route, []AttachmentInput{att}, nil); len(got.Parts) != 0 {
		t.Fatal("unverifiable page metadata must not bypass the page budget")
	}
}

func TestNativeInlineRequestLimitIsNotAnUpstreamFailure(t *testing.T) {
	if !errors.Is(wrapUpstreamRequestError(llm.ErrInputTooLarge), ErrFileTooLarge) {
		t.Fatal("input limit must map to the file size error")
	}
	if canFailoverMessageRoute(1, 1, 5, 0, false, llm.ErrInputTooLarge) {
		t.Fatal("local input limits must not trigger route failover")
	}
}

func TestNativeReadUsesRouteBudgetBeyondOldReaderLimit(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	data := bytes.Repeat([]byte("p"), 15*1024*1024)
	if _, err := store.Put(t.Context(), "files/large", bytes.NewReader(data), objectstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	att := nativePDF("large", int64(len(data)), 1, true)
	service := &Service{storeProvider: &conversationTestStoreProvider{store: store}}
	route := &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIResponses, BaseURL: "https://api.openai.com/v1", CatalogInputModalities: []string{"pdf"}}
	native := service.resolveNativeInputs(t.Context(), route, []AttachmentInput{att}, nil)
	if len(native.Parts[att.FileID].Data) != len(data) {
		t.Fatal("a valid larger official PDF was truncated by the old 14MiB reader cap")
	}
	att.FileSize--
	native = service.resolveNativeInputs(t.Context(), route, []AttachmentInput{att}, nil)
	if err := validateRequiredNativeInputs([]AttachmentInput{att}, native); !errors.Is(err, ErrFileProcessingNotReady) {
		t.Fatal("storage bytes beyond the budgeted size must stop generation")
	}
}

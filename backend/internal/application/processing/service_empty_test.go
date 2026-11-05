package processing

import (
	"bytes"
	"context"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	extractionport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/extraction"
)

func TestMarkClaimedFileProcessingEmptyRecordsTerminalEmptyState(t *testing.T) {
	repo := &processingStateRepositoryStub{}
	service := NewServiceWithRuntime(Dependencies{
		Config:           config.NewRuntime(config.Config{}),
		Repository:       repo,
		ExtractorVersion: "pipeline-v1",
	})
	file := &domainconversation.FileObject{ID: 9, UserID: 7, FileID: "file_1", DetectedMIME: "application/pdf", FileCategory: "pdf", PageCount: 6}

	if err := service.markClaimedFileProcessingEmpty(context.Background(), file, "attempt_1", "rapidocr_ocr_empty_content"); err != nil {
		t.Fatalf("markClaimedFileProcessingEmpty() error = %v", err)
	}

	state := repo.claimedState
	if state == nil || repo.claimedAttemptID != "attempt_1" {
		t.Fatalf("claimed state was not persisted: %#v", state)
	}
	if state.ProcessingStatus != "ready" || state.ProcessingReady {
		t.Fatalf("empty file must finish the pipeline without being usable: %#v", state)
	}
	if state.ExtractStatus != domainconversation.FileSubprocessStatusEmpty {
		t.Fatalf("expected extract_status=empty, got %q", state.ExtractStatus)
	}
	if state.RAGReady || state.RAGReason != "rapidocr_ocr_empty_content" {
		t.Fatalf("expected RAG reason to carry the empty code, got %#v", state)
	}
	if state.ErrorCode != "" || state.ErrorMessage != "" {
		t.Fatalf("empty is not a failure and must not set error fields: %#v", state)
	}
	if state.PageCount != 6 || state.ExtractPages != 0 {
		t.Fatalf("original page count must survive an empty extraction: %#v", state)
	}
}

type metadataOnlyPDFParser struct{ extraction.BuiltinParser }

func (metadataOnlyPDFParser) DetectPDFPageCount(string) int { return 6 }
func (metadataOnlyPDFParser) ExtractPDFPages(string, int) (extractionport.PDFTextResult, error) {
	return extractionport.PDFTextResult{PageCount: 6, Pages: make([]extractionport.PDFTextPage, 6)}, nil
}

type metadataTestStoreProvider struct{ store objectstorage.Store }

func (p metadataTestStoreProvider) Open(context.Context) (objectstorage.Store, error) {
	return p.store, nil
}

func TestFailedPDFTextExtractionPersistsOriginalPageCount(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	if _, err := store.Put(t.Context(), "scan.pdf", bytes.NewReader([]byte("pdf-page-tree")), objectstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewRuntime(config.Config{ExtractEngine: extraction.EngineBuiltin})
	extract := extraction.NewServiceWithRuntime(cfg, extraction.EngineFactories{Builtin: metadataOnlyPDFParser{}})
	extract.SetObjectStoreProvider(metadataTestStoreProvider{store: store})
	repo := &processingStateRepositoryStub{}
	service := NewServiceWithRuntime(Dependencies{Config: cfg, Repository: repo, ExtractService: extract})
	file := &domainconversation.FileObject{ID: 9, UserID: 7, FileID: "scan", StoragePath: "scan.pdf", DetectedMIME: "application/pdf", FileCategory: "pdf"}
	if err := service.processClaimedFile(t.Context(), file, "attempt_1"); err != nil {
		t.Fatal(err)
	}
	state := repo.claimedState
	if state == nil || state.PageCount != 6 || state.ErrorCode != "pdf_no_extractable_text" || state.ProcessingReady || state.ExtractPages != 0 {
		t.Fatalf("failed text extraction must preserve independent page metadata without claiming parsed pages: %#v", state)
	}
}

func TestExtractionIsEmptyContentMatchesEveryEngineSuffix(t *testing.T) {
	for _, code := range []string{"tika_empty_content", "docling_empty_content", "mineru_empty_content", "rapidocr_ocr_empty_content", "llm_ocr_empty_content"} {
		if !extraction.IsEmptyContent(extraction.NewError(code, context.DeadlineExceeded)) {
			t.Fatalf("%s must classify as empty content", code)
		}
	}
	for _, code := range []string{"tika_unprocessable", "rapidocr_ocr_failed", "extract_failed", ""} {
		if extraction.IsEmptyContent(extraction.NewError(code, context.DeadlineExceeded)) {
			t.Fatalf("%s must not classify as empty content", code)
		}
	}
	if extraction.IsEmptyContent(nil) || extraction.IsEmptyContent(context.Canceled) {
		t.Fatal("nil and uncoded errors must not classify as empty content")
	}
}

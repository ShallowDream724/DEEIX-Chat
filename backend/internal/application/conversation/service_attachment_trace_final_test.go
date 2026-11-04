package conversation

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestFinalAttachmentTraceReportsOnlyEvidencedExclusionReasons(t *testing.T) {
	items := []AttachmentInput{
		{FileID: "trimmed", ContextMode: fileContextModeFull, ExtractedText: "content"},
		{FileID: "budget", ContextMode: fileContextModeNativeDocument},
		{FileID: "read", ContextMode: fileContextModeNativeDocument},
		{FileID: "retrieval", ContextMode: fileContextModeRAG},
		{FileID: "unsupported", ContextMode: fileContextModeUnsupported},
		{FileID: "unknown", ContextMode: fileContextModeFull},
	}
	trace := attachmentRouteTrace{items: items}
	trace.recordPlannedMessages([]llm.Message{{Parts: []llm.ContentPart{{SourceFileIDs: []string{"trimmed"}, Kind: llm.ContentPartFile, Text: "content"}}}})
	final := attachmentTraceItemsForMessages(items, nil)
	_, _, trace.payload = buildAttachmentProcessTrace("auto", final)
	trace.annotateExclusionReasons(nativeInputs{
		Selected: map[string]bool{"read": true},
		Rejected: map[string]error{"budget": llm.NewInputLimitError("nativeBytes", 200, 100), "trimmed": llm.NewInputLimitError("pdfPages", 200, 100)},
	})
	want := map[string]string{"trimmed": "context_budget", "budget": "input_budget", "read": "read_failed", "retrieval": "no_retrieved_content", "unsupported": "unsupported", "unknown": ""}
	for _, ref := range trace.payload.FileRefs {
		if ref.Reason != want[ref.FileID] {
			t.Fatalf("file %s reason = %q, want %q", ref.FileID, ref.Reason, want[ref.FileID])
		}
		if ref.FileID == "budget" && (ref.Limit == nil || ref.Limit.Actual != 200 || ref.Limit.Limit != 100) {
			t.Fatalf("budget measurements missing: %#v", ref)
		}
	}
	for _, ref := range trace.payload.FileGroupRefs.Skipped {
		if ref.Reason != want[ref.FileID] {
			t.Fatalf("group ref lost reason: %#v", ref)
		}
	}
	trace.recordPlannedMessages(nil)
	if len(trace.plannedFileIDs) != 0 {
		t.Fatal("retry planning retained previous route evidence")
	}
}

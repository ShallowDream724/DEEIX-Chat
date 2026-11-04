package conversation

import (
	"context"
	"encoding/json"
	"testing"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type contextObservationRepositoryStub struct {
	repository.ConversationRepository
	filter repository.ContextArtifactListFilter
	items  []model.ContextArtifact
}

func (r *contextObservationRepositoryStub) ListRecentContextArtifacts(_ context.Context, filter repository.ContextArtifactListFilter) ([]model.ContextArtifact, error) {
	r.filter = filter
	return r.items, nil
}

func TestContextEstimatorEditedMiddleTurnUsesPreviousAncestorMeasurement(t *testing.T) {
	previous := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Content: "first question"}}}
	observation := describeContextInput("route", previous, nil)
	observation.InputTokens = 1000
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	repo := &contextObservationRepositoryStub{items: []model.ContextArtifact{{MessageID: 2, MetadataJSON: string(raw)}}}
	service := &Service{repo: repo}
	// Edited user 5 points to assistant 2; the replaced user 3 and its answer 4
	// are absent from this ancestor path, even if they have newer observations.
	path := []model.Message{{ID: 1, Role: "user"}, {ID: 2, Role: "assistant"}, {ID: 5, Role: "user"}}
	scope := buildPromptScope(path, nil, contextCompactionPolicy{})
	estimate := service.contextInputEstimator(t.Context(), scope.historicalMessageScope(7, 1, 5), "route")
	current := previous
	current.Messages = append(append([]llm.Message(nil), previous.Messages...), llm.Message{Role: "assistant", Content: "retained prior reply"}, llm.Message{Role: "user", Content: "edited middle question"})
	want := int64(1000) + estimateGenerateInputTokens(current) - estimateGenerateInputTokens(previous)
	if got := estimate(current); got != want {
		t.Fatalf("edited input = %d want %d", got, want)
	}
	if repo.filter.Scope.LeafMessageID != 5 || repo.filter.Scope.ExcludeThroughMessageID != 0 {
		t.Fatalf("observation must query edited leaf ancestry: %#v", repo.filter)
	}
}

func TestDynamicContextTracksOnlyRenderedFileContents(t *testing.T) {
	input := userContextInput{Files: []AttachmentInput{{FileID: "full", ExtractedText: "body"}, {FileID: "empty"}},
		RAGChunks:        []model.RAGChunk{{FileID: "rag", Content: "evidence"}, {FileID: "empty-rag"}},
		ImageAnalyses:    []imageAttachmentAnalysis{{FileID: "analysis", Content: "description"}},
		UnretrievedFiles: []unretrievedAttachment{{Attachment: AttachmentInput{FileID: "metadata-only"}}},
	}
	ids := userContextSourceFileIDs(input)
	if len(ids) != 3 || ids[0] != "full" || ids[1] != "rag" || ids[2] != "analysis" {
		t.Fatalf("unexpected actual content IDs: %#v", ids)
	}
}

func TestContextObservationUsesMeasuredWholeInputAndTextDelta(t *testing.T) {
	input := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Parts: []llm.ContentPart{
		{Kind: llm.ContentPartDocument, MimeType: "application/pdf", Data: []byte("pdf bytes")},
		{Kind: llm.ContentPartText, Text: "read this"},
	}}}}
	observed := describeContextInput("route-model-config", input, nil)
	observed.InputTokens = 1200
	input.Messages = append(input.Messages, llm.Message{Role: "assistant", Content: "answer"}, llm.Message{Role: "user", Content: "continue"})
	current := describeContextInput("route-model-config", input, nil)
	got, matched := observed.estimate(current)
	if !matched || got != 1200+current.EstimatedTokens-observed.EstimatedTokens {
		t.Fatalf("expected measured baseline plus new text only, got %d matched=%v", got, matched)
	}
	for _, mutate := range []struct {
		name  string
		apply func(*llm.GenerateInput)
	}{
		{"file", func(in *llm.GenerateInput) { in.Messages[0].Parts[0].Data = []byte("changed pdf") }},
		{"options", func(in *llm.GenerateInput) { in.Options = map[string]any{"temperature": 0.5} }},
		{"system", func(in *llm.GenerateInput) { in.Instructions = "new system" }},
		{"tools", func(in *llm.GenerateInput) { in.Tools = []llm.ToolDefinition{{Name: "search"}} }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			changed := input
			changed.Messages = cloneLLMMessages(input.Messages)
			mutate.apply(&changed)
			if _, ok := observed.estimate(describeContextInput("route-model-config", changed, nil)); ok {
				t.Fatal("incompatible request reused measured input")
			}
		})
	}
	if _, ok := observed.estimate(describeContextInput("other-route-or-model", input, nil)); ok {
		t.Fatal("route/model change reused measured input")
	}
}

func TestContextObservationResponsesContinuationUsesFullContext(t *testing.T) {
	full := []llm.Message{{Role: "user", Content: "first question"}, {Role: "assistant", Content: "answer"}, {Role: "user", Content: "follow up"}}
	plain := llm.GenerateInput{Messages: full}
	continued := plain
	continued.PreviousResponseID = "response_1"
	continued.Messages = full[2:]
	want := describeContextInput("route", plain, nil)
	got := describeContextInput("route", continued, full)
	if got != want {
		t.Fatalf("continuation must describe full upstream context: got %#v want %#v", got, want)
	}
}

func TestContextUsageUsesLatestRequestIncludingCacheWithoutBillingAccumulation(t *testing.T) {
	var usage messageUsageAccumulator
	usage.beginCall(10000)
	first := llm.Usage{InputTokens: 6000, CacheReadTokens: 4000}
	usage.addObservedUsage(first)
	usage.observeContextInput(first)
	usage.finishCall(true, true)
	usage.beginCall(11000)
	latest := llm.Usage{InputTokens: 2000, CacheReadTokens: 9000}
	usage.addObservedUsage(latest)
	usage.observeContextInput(latest)
	if got := usage.latestContextInputTokens(1); got != 11000 {
		t.Fatalf("latest context = %d, want 11000", got)
	}
	if got := usage.usage(); got.InputTokens != 8000 || got.CacheReadTokens != 13000 {
		t.Fatalf("billing must still accumulate calls, got %#v", got)
	}
	usage.beginCall(12000)
	if got := usage.latestContextInputTokens(1); got != 12000 {
		t.Fatalf("unobserved new call retained stale measurement: %d", got)
	}
}

type contextToolLoopGateway struct {
	temporaryLLMGatewayStub
	requests []llm.GenerateInput
}

func (g *contextToolLoopGateway) Generate(_ context.Context, _ llm.RouteConfig, input llm.GenerateInput) (*llm.GenerateOutput, error) {
	g.requests = append(g.requests, input)
	return &llm.GenerateOutput{Text: "answer", Usage: llm.Usage{InputTokens: 100}}, nil
}

func TestMeasuredPDFRemainsInActualLocalToolFollowUpRequest(t *testing.T) {
	gateway := &contextToolLoopGateway{}
	runner := &messageGenerationRunner{service: &Service{llmClient: gateway}, usage: &messageUsageAccumulator{}, contextIdentity: "route", maxLLMCalls: 5}
	input := llm.GenerateInput{Messages: []llm.Message{
		{Role: "user", Parts: []llm.ContentPart{{Kind: llm.ContentPartDocument, MimeType: "application/pdf", FileName: "source.pdf", Data: []byte("original pdf bytes"), PageCount: 100, SourceFileIDs: []string{"pdf"}}}},
		{Role: "assistant", Content: "prior answer"},
		{Role: "user", Content: "use a local tool"},
	}}
	if _, err := runner.generate(t.Context(), input, input.Messages); err != nil {
		t.Fatal(err)
	}
	assistant := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ToolCallID: "call", ToolName: "local", ArgumentsJSON: "{}"}}}
	full := append(cloneLLMMessages(input.Messages), assistant, llm.Message{Role: "tool", ToolResults: []llm.ToolResult{{ToolCallID: "call", ToolName: "local", OutputJSON: `{"result":"found"}`}}})
	const capabilities = `{"contextWindow":32000,"maxOutputTokens":4000}`
	if budget := resolveToolResultTokenBudget(input, input.Messages, assistant, "custom-model", capabilities, 2000, runner.estimateContextInput); budget < 1000 {
		t.Fatalf("observed PDF should leave tool room, got %d", budget)
	}
	trimmed, changed := trimToolFollowUpHistory(input, full, "custom-model", capabilities, 2000, runner.estimateContextInput)
	if changed {
		t.Fatal("measured-small PDF caused history trimming")
	}
	trimmed, changed = rebalanceToolFollowUpResults(input, trimmed, "custom-model", capabilities, 2000, runner.estimateContextInput)
	if changed {
		t.Fatal("measured-small PDF caused tool truncation")
	}
	input.Messages = trimmed
	if _, err := runner.generate(t.Context(), input, trimmed); err != nil {
		t.Fatal(err)
	}
	if len(gateway.requests) != 2 || string(gateway.requests[1].Messages[0].Parts[0].Data) != "original pdf bytes" {
		t.Fatalf("second actual request lost PDF: %#v", gateway.requests)
	}
}

func TestInitialBudgetFitUsesMeasuredPDFAndRechecksCandidates(t *testing.T) {
	input := llm.GenerateInput{Messages: []llm.Message{
		{Role: "user", Parts: []llm.ContentPart{{Kind: llm.ContentPartDocument, Data: []byte("pdf"), PageCount: 100}}},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "current question"},
	}}
	observed := describeContextInput("route", input, nil)
	observed.InputTokens = 100
	runner := &messageGenerationRunner{contextIdentity: "route", contextObservations: []contextInputObservation{observed}}
	_, fit := fitGenerateInputToModelBudget(input, "custom-model", "", 2000, true, runner.estimateContextInput)
	if fit.Trimmed || fit.TokensBefore != 100 {
		t.Fatalf("measured PDF must fit unchanged: %#v", fit)
	}
	_, fallback := fitGenerateInputToModelBudget(input, "custom-model", "", 2000, true)
	if !fallback.Trimmed {
		t.Fatalf("no observation must use conservative PDF fallback: %#v", fallback)
	}
	// Removing the PDF invalidates its baseline; the remaining request can still
	// exceed budget. The fitter must test that candidate instead of assuming the
	// raw PDF estimate was freed from the measured total.
	estimate := func(candidate llm.GenerateInput) int64 {
		switch len(candidate.Messages) {
		case 5:
			return 300000
		case 3:
			return 250000
		default:
			return 100
		}
	}
	result, fit := fitGenerateInputToModelBudget(input, "custom-model", "", 2000, true, estimate)
	if len(result.Messages) != 1 || fit.Exceeded || fit.TokensAfter != 100 {
		t.Fatalf("candidate was not re-estimated: count=%d fit=%#v", len(result.Messages), fit)
	}
}

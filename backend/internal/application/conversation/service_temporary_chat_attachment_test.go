package conversation

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/extraction/engines"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/coregx/gxpdf/creator"
)

func longTemporaryPDF(t *testing.T) []byte {
	t.Helper()
	document := creator.New()
	for n := 1; n <= 21; n++ {
		page, err := document.NewPage()
		if err != nil {
			t.Fatal(err)
		}
		text := "page content"
		if n == 21 {
			text = "complete document ends here"
		}
		if err := page.AddText(text, 100, 700, creator.Helvetica, 12); err != nil {
			t.Fatal(err)
		}
	}
	var data bytes.Buffer
	if _, err := document.WriteTo(&data); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestTemporaryChatUsesEntireParsedPDFAndRejectsFullTextOverBudget(t *testing.T) {
	pdf := longTemporaryPDF(t)
	for _, tc := range []struct {
		name      string
		maxBytes  int64
		maxTokens int
		dimension string
		invalid   bool
	}{
		{name: "complete text with legacy twenty-page setting", maxBytes: 1024, maxTokens: 1024},
		{name: "bytes over limit", maxBytes: 16, dimension: "parsedTextBytes"},
		{name: "estimated tokens over limit", maxTokens: 5, dimension: "parsedTextTokens"},
		{name: "invalid document is a parse failure", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &temporaryLLMGatewayStub{}
			cfg := config.NewRuntime(config.Config{
				FileFullContextPDFMaxPages: 20,
				FileFullContextMaxBytes:    tc.maxBytes,
				FileFullContextMaxTokens:   tc.maxTokens,
			})
			service := &Service{
				cfg: cfg,
				routeResolver: &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{
					"text": {PlatformModelName: "text", UpstreamModel: "text", Protocol: llm.AdapterOpenAIChatCompletions,
						CatalogInputModalities: []string{"text"}},
				}},
				llmClient:  gateway,
				uploadSvc:  appupload.NewServiceWithRuntime(cfg, nil, nil, appupload.Hooks{}, appupload.ErrorSet{}, ""),
				extractSvc: extraction.NewServiceWithRuntime(cfg, extraction.EngineFactories{Builtin: engines.Builtin{}}),
			}
			content := pdf
			if tc.invalid {
				content = []byte("%PDF-1.7 invalid")
			}
			_, err := service.StreamTemporaryChat(t.Context(), TemporaryChatInput{
				UserID: 1, SessionID: "session", ClientRunID: "run", Model: "text",
				Messages: []TemporaryChatMessage{{Role: "user", Content: "Read the PDF"}},
				Attachments: []TemporaryChatAttachment{
					{MessageIndex: 0, FileName: "report.pdf", MimeType: "application/pdf", Reader: bytes.NewReader(content)},
				},
			}, nil)
			if tc.invalid {
				if detail := AttachmentInputErrorDetails(err); !errors.Is(err, ErrFileProcessingNotReady) || detail == nil ||
					detail.Reason != "fileParseFailed" || detail.FileName != "report.pdf" || len(gateway.inputs) != 0 {
					t.Fatalf("expected named parse failure with no upstream call, got %v (%#v), calls=%d", err, detail, len(gateway.inputs))
				}
				return
			}
			if tc.dimension != "" {
				limit := llm.InputLimitDetails(err)
				file := AttachmentInputErrorDetails(err)
				if limit == nil || limit.Dimension != tc.dimension || file == nil || file.FileName != "report.pdf" || len(gateway.inputs) != 0 {
					t.Fatalf("expected a named input limit with no upstream call, got err=%v, limit=%#v, file=%#v, calls=%d", err, limit, file, len(gateway.inputs))
				}
				return
			}
			if err != nil || len(gateway.inputs) != 1 {
				t.Fatalf("expected the whole PDF to reach the upstream, got err=%v, calls=%d", err, len(gateway.inputs))
			}
			messages := gateway.inputs[0].Messages
			if parts := messages[len(messages)-1].Parts; len(parts) < 2 || !strings.Contains(parts[0].Text, "complete document ends here") {
				t.Fatalf("the last page did not reach the upstream: %#v", parts)
			}
		})
	}
}

func TestTemporaryChatAppliesRouteToHistoricalNativeMedia(t *testing.T) {
	pdf := longTemporaryPDF(t)
	for _, tc := range []struct {
		name  string
		media bool
		count int
	}{
		{name: "historical audio remains in the prompt", media: true, count: 2},
		{name: "history cannot be discarded to fit new route", media: true, count: 1},
		{name: "text-only route cannot read history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modalities := []string{"text"}
			if tc.media {
				modalities = append(modalities, "audio", "pdf")
			}
			route := &channel.ResolvedRoute{Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: modalities}
			policy := resolveNativeInputPolicy(route)
			policy.Budget.Count = tc.count
			cfg := config.NewRuntime(config.Config{})
			service := &Service{cfg: cfg, uploadSvc: appupload.NewServiceWithRuntime(cfg, nil, nil, appupload.Hooks{}, appupload.ErrorSet{}, ""),
				extractSvc: extraction.NewServiceWithRuntime(cfg, extraction.EngineFactories{Builtin: engines.Builtin{}})}
			result, err := service.prepareTemporaryAttachmentContext(t.Context(), TemporaryChatInput{Attachments: []TemporaryChatAttachment{
				{MessageIndex: 0, FileName: "voice.mp3", MimeType: "audio/mpeg", Reader: strings.NewReader("ID3 native audio")},
				{MessageIndex: 2, FileName: "report.pdf", MimeType: "application/pdf", Reader: bytes.NewReader(pdf)},
			}}, []llm.Message{{Role: "user", Content: "listen"}, {Role: "assistant", Content: "ok"}, {Role: "user", Content: "combine with this"}}, policy)
			switch {
			case !tc.media:
				if file := AttachmentInputErrorDetails(err); !errors.Is(err, ErrFileUnsupportedByModel) || file == nil || file.Scope != "history" {
					t.Fatalf("expected named history rejection, got %v (%#v)", err, file)
				}
			case tc.count == 1:
				if limit := llm.InputLimitDetails(err); limit == nil || limit.Dimension != "nativeCount" || limit.Actual != 2 {
					t.Fatalf("expected both user files counted, got %v (%#v)", err, limit)
				}
			default:
				if err != nil {
					t.Fatalf("prepare native files: %v", err)
				}
				older, latest := result.messages[0].Parts, result.messages[2].Parts
				if len(older) != 3 || len(latest) != 3 || older[1].Kind != llm.ContentPartAudio || latest[1].Kind != llm.ContentPartDocument ||
					string(older[1].Data) != "ID3 native audio" || latest[1].PageCount != 21 {
					t.Fatalf("native parts were lost or placed in the wrong turn: older=%#v latest=%#v", older, latest)
				}
			}
		})
	}
}

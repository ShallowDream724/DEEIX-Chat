package conversation

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	domainmemory "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/memory"
	domainskill "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/skill"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestChat01RoutePromptPreservesInstructionsAndSelectedSkill(t *testing.T) {
	cfg := config.Config{DefaultSystemPrompt: "platform instruction"}
	skills := &skillPrompts{Skills: []domainskill.Skill{{ID: 1, Title: "Chosen skill", Markdown: "skill task rules"}}}
	skills.Rendered = renderSkillPrompts(skills, "")
	base := messageRoutePromptInput{Config: cfg, ProjectSystemPrompt: "project instruction", HTMLVisualPromptEnabled: true,
		DomainMessages: []model.Message{{Role: "user", Content: "question"}}, PreferencePrompt: "short language preference", SkillPrompts: skills,
		ToolRuntime:    selectedToolRuntime{definitions: []llm.ToolDefinition{{Name: "local_tool"}}},
		DynamicContext: userContextInput{Memory: []domainmemory.UserMemory{{MemoryKey: "broad", Value: "private profile memory"}}},
	}
	service := &Service{cfg: config.NewRuntime(cfg)}
	route := &channel.ResolvedRoute{Protocol: "chat01_chat_completions", ModelSystemPrompt: "model instruction"}
	plan, err := service.buildMessageRoutePrompt(t.Context(), route, base)
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, msg := range plan.Messages {
		all += systemInstructionText(msg) + "\n"
	}
	for _, required := range []string{"platform instruction", "model instruction", "project instruction", "short language preference", "skill task rules"} {
		if !strings.Contains(all, required) {
			t.Fatalf("missing %q: %s", required, all)
		}
	}
	for _, forbidden := range []string{"html-visual", "private profile memory", "Do not call tools unless", "local_tool"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("unexpected %q: %s", forbidden, all)
		}
	}
	if !strings.Contains(skills.Rendered, "Do not call tools unless") || !base.HTMLVisualPromptEnabled || len(base.ToolRuntime.definitions) != 1 {
		t.Fatal("route planning mutated original preferences")
	}
	other, err := service.buildMessageRoutePrompt(t.Context(), &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions}, base)
	if err != nil {
		t.Fatal(err)
	}
	all = ""
	for _, msg := range other.Messages {
		all += systemInstructionText(msg) + "\n"
	}
	if !strings.Contains(all, "html-visual") || !strings.Contains(all, "Do not call tools unless") {
		t.Fatal("other route lost its original format/skill preferences")
	}
}

func TestChat01ResolvesOriginalWithoutStorageReadOrExtraction(t *testing.T) {
	service := &Service{attachmentDelivery: &deliveryRecorder{}}
	att := AttachmentInput{FileID: "owned", FileName: "report.docx", FileCategory: "word", DetectedMIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", FileSize: 400, StoragePath: "unopened", SHA256: "hash", Current: true}
	native := service.resolveNativeInputs(t.Context(), &channel.ResolvedRoute{Protocol: "chat01_chat_completions"}, []AttachmentInput{att}, nil)
	if err := validateRequiredNativeInputs([]AttachmentInput{att}, native); err != nil {
		t.Fatal(err)
	}
	part := native.Parts["owned"]
	if part.Kind != llm.ContentPartAttachment || part.Reference == nil || part.ByteSize() != 400 || len(part.Data) != 0 {
		t.Fatalf("expected trusted reference: %#v", part)
	}
	if !contentPartHasContent(part) || estimateContentPartTokens(part) <= 0 {
		t.Fatal("reference disappeared from prompt/budget")
	}
	textOnly := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, CatalogInputModalities: []string{"text"}})
	if textOnly.accepts(att) {
		t.Fatal("Chat01 generic original capability leaked to another adapter")
	}
}

func TestChat01ExplicitModalitiesOverrideOriginalDefaults(t *testing.T) {
	policy := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: "chat01_chat_completions", ModelCapabilitiesJSON: `{"inputModalities":["text"]}`})
	if policy.Attachment || !policy.ImageUnsupported {
		t.Fatalf("explicit model capabilities ignored: %#v", policy)
	}
}

func TestChat01DoesNotResolveInvalidSelectedMCPTools(t *testing.T) {
	service := &Service{}
	runtime, err := service.resolveRouteToolRuntime(t.Context(), &channel.ResolvedRoute{Protocol: "chat01_chat_completions"}, []uint{1, 2, 3})
	if err != nil || len(runtime.definitions) != 0 {
		t.Fatalf("Chat01 resolved local tools: %#v %v", runtime, err)
	}
}

func TestChat01TemporaryOriginalSkipsParserAndLocalToolLoop(t *testing.T) {
	cfg := config.NewRuntime(config.Config{})
	delivery := &deliveryRecorder{}
	gateway := &temporaryLLMGatewayStub{output: &llm.GenerateOutput{Text: "read original", ToolCalls: []llm.ToolCall{{ToolCallID: "unexpected", ToolName: "unselected"}}}}
	service := &Service{cfg: cfg, attachmentDelivery: delivery, llmClient: gateway,
		routeResolver: &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{"chat01": {PlatformModelName: "chat01", UpstreamModel: "pro", Protocol: "chat01_chat_completions"}}},
		uploadSvc:     appupload.NewServiceWithRuntime(cfg, nil, nil, appupload.Hooks{}, appupload.ErrorSet{}, ""),
	}
	result, err := service.StreamTemporaryChat(t.Context(), TemporaryChatInput{UserID: 1, Model: "chat01", SessionID: "session", ClientRunID: "run", SelectedToolIDs: []uint{999}, HTMLVisualPromptEnabled: true,
		Messages: []TemporaryChatMessage{{Role: "user", Content: "read original"}}, Attachments: []TemporaryChatAttachment{{MessageIndex: 0, FileName: "note.txt", MimeType: "text/plain", Reader: bytes.NewReader([]byte("temporary original"))}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(gateway.inputs) != 1 || delivery.temporary != 1 || delivery.released != 1 {
		t.Fatalf("unexpected generation/delivery: %d %#v", len(gateway.inputs), delivery)
	}
	request := gateway.inputs[0]
	if len(request.Tools) > 0 || !request.DisableTools {
		t.Fatalf("local tools leaked: %#v", request)
	}
	originals := 0
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.Kind == llm.ContentPartAttachment {
				originals++
				if part.Reference == nil || part.Reference.URL == "" || len(part.Data) != 0 {
					t.Fatalf("missing original URL: %#v", part)
				}
			}
		}
	}
	if originals != 1 {
		t.Fatalf("expected single original, got %d", originals)
	}
}

func TestChat01HistoricalOriginalReplansForTextRoute(t *testing.T) {
	service := &Service{attachmentDelivery: &deliveryRecorder{}}
	att := AttachmentInput{FileID: "history", FileName: "old.docx", FileCategory: "word", MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", FileSize: 400, StoragePath: "unopened", SHA256: "hash", MessageRole: "user", ContextMode: fileContextModeNativeDocument, ExtractedText: "parsed historical material"}
	base := messageRoutePromptInput{UserContent: "follow up", DomainMessages: []model.Message{{Role: "user", Content: "old task", Status: "success", Attachments: `[{"file_id":"history"}]`}, {Role: "assistant", Content: "old answer", Status: "success"}, {Role: "user", Content: "follow up", Status: "success"}}, ConversationFiles: []AttachmentInput{att}}
	plan, err := service.buildMessageRoutePrompt(t.Context(), &channel.ResolvedRoute{Protocol: "chat01_chat_completions"}, base)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range plan.Messages {
		for _, part := range message.Parts {
			if part.Kind == llm.ContentPartAttachment {
				count++
			}
			if strings.Contains(part.Text, "parsed historical material") {
				t.Fatal("parsed text duplicated original")
			}
		}
	}
	if count != 1 {
		t.Fatalf("historical original omitted: %#v", plan.Messages)
	}
	plan, err = service.buildMessageRoutePrompt(t.Context(), &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, CatalogInputModalities: []string{"text"}}, base)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range plan.Messages {
		for _, part := range message.Parts {
			if isBinaryContentPart(part) {
				t.Fatal("text route retained native original")
			}
			found = found || strings.Contains(part.Text, "parsed historical material")
		}
	}
	if !found {
		t.Fatal("text route omitted parsed history")
	}
	base.ConversationFiles[0].FileCategory = "other"
	base.ConversationFiles[0].ExtractedText = ""
	_, err = service.buildMessageRoutePrompt(t.Context(), &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, CatalogInputModalities: []string{"text"}}, base)
	if !errors.Is(err, ErrFileUnsupportedByModel) {
		t.Fatalf("expected explicit unsupported historical original, got %v", err)
	}
}

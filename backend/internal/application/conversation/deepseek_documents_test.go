package conversation

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestDeepSeekDocumentsAndPDFHaveIndependentAdmission(t *testing.T) {
	files := []AttachmentInput{
		{FileName: "report.pdf", MimeType: "application/pdf"},
		{FileName: "report.docx", MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{FileName: "slides.pptx", MimeType: "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{FileName: "book.epub", MimeType: "application/epub+zip"},
		{FileName: "book.mobi", MimeType: "application/x-mobipocket-ebook"},
		{FileName: "data.xlsx", MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{FileName: "renamed.epub", MimeType: "application/zip"},
		{FileName: "video.mp4", MimeType: "video/mp4"},
		{FileName: "code.txt", MimeType: "text/plain"},
	}
	for _, tc := range []struct {
		name, protocol, model, base, capabilities string
		accepted                                  []bool
	}{
		{"documents", llm.AdapterDeepSeekResponses, "deepseek-flash", "https://proxy.example", `{"inputModalities":["text","document"]}`, []bool{false, true, true, true, true, false, false, false, false}},
		{"pdf only", llm.AdapterDeepSeekResponses, "deepseek-flash", "https://proxy.example", `{"inputModalities":["text","pdf"]}`, []bool{true, false, false, false, false, false, false, false, false}},
		{"both and native spreadsheet", llm.AdapterDeepSeekResponses, "deepseek-flash", "https://proxy.example", `{"inputModalities":["text","pdf","document"],"nativeFiles":{"spreadsheet":"native"}}`, []bool{true, true, true, true, true, true, false, false, false}},
		{"text only", llm.AdapterDeepSeekResponses, "deepseek-flash", "https://api.deepseek.com", `{"inputModalities":["text"]}`, nil},
		{"disabled files", llm.AdapterDeepSeekResponses, "deepseek-flash", "https://api.deepseek.com", `{"inputModalities":["pdf","document"],"nativeFiles":{"enabled":false}}`, nil},
		{"unsupported model", llm.AdapterDeepSeekResponses, "deepseek-v4-pro", "https://api.deepseek.com", `{"inputModalities":["pdf","document"]}`, nil},
		{"generic proxy needs explicit evidence", llm.AdapterOpenAIResponses, "deepseek-flash", "https://proxy.example", `{"inputModalities":["document"]}`, nil},
		{"legacy official", llm.AdapterOpenAIResponses, "deepseek-flash", "https://api.deepseek.com", `{"inputModalities":["pdf","attachment"]}`, []bool{true, true, true, true, true, false, false, false, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: tc.protocol, UpstreamModel: tc.model, BaseURL: tc.base, ModelCapabilitiesJSON: tc.capabilities})
			if p.Attachment {
				t.Fatal("DeepSeek was given Chat01 original attachments")
			}
			for i, file := range files {
				want := len(tc.accepted) > 0 && tc.accepted[i]
				if got := p.accepts(file); got != want {
					t.Errorf("%s: admitted=%v want=%v", file.FileName, got, want)
				}
			}
		})
	}
}

func TestDeepSeekExplicitToolProtocolSharesResponsesPolicyWithoutDuplicateCharges(t *testing.T) {
	capabilities := `{"nativeTools":[{"key":"deepseek.tool_search","protocols":["deepseek_responses","openai_responses"],"enabled":true},{"key":"deepseek.tool_fetch","protocols":["deepseek_responses"],"enabled":true}]}`
	options := map[string]any{"tools": []map[string]any{{"type": "deepseek_web_search"}, {"type": "deepseek_read_url"}}}
	filtered := filterModelOptions(options, llm.AdapterDeepSeekResponses, modelOptionPolicyConfig{ModelCapabilitiesJSON: capabilities})
	if got := len(providerToolOptionPayloads(filtered["tools"])); got != 2 {
		t.Fatalf("expected two distinct tools, got %d: %#v", got, filtered)
	}
	keys := authorizedNativeToolKeys(llm.AdapterDeepSeekResponses, filtered, capabilities)
	if keys["tool_search"] != "deepseek.tool_search" || keys["tool_fetch"] != "deepseek.tool_fetch" {
		t.Fatalf("tool authorization lost: %#v", keys)
	}
}

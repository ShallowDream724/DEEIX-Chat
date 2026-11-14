package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestDeepSeekResponsesUploadsDocumentsAndReusesFiles(t *testing.T) {
	var uploads, calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			parts, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			part, err := parts.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			purpose, _ := io.ReadAll(part)
			if part.FormName() != "purpose" || (string(purpose) != "rich_layout_recognition" && string(purpose) != "user_data") {
				t.Errorf("invalid purpose: %s", purpose)
			}
			uploads++
			fmt.Fprintf(w, `{"id":"file-api-%d"}`, uploads)
		case "/v1/responses":
			calls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			input, _ := body["input"].([]any)
			if len(input) == 0 {
				t.Error("missing input")
				http.Error(w, "missing input", http.StatusBadRequest)
				return
			}
			content := input[0].(map[string]any)["content"].([]any)
			files := 0
			for _, raw := range content {
				part := raw.(map[string]any)
				if part["type"] == "input_file" {
					files++
					if part["file_id"] == nil || part["file_data"] != nil {
						t.Errorf("not an uploaded reference: %#v", part)
					}
				}
			}
			if files != 2 {
				t.Errorf("document count=%d", files)
			}
			_, _ = io.WriteString(w, `{"id":"response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := newTestClient()
	defer client.CloseIdleConnections()
	client.SetNativeFileStore(&nativeFileMemoryStore{})
	route := portllm.RouteConfig{Protocol: portllm.AdapterDeepSeekResponses, BaseURL: server.URL, UpstreamModel: "deepseek-flash"}
	input := portllm.GenerateInput{UserID: 1, NativeFiles: true, Messages: []portllm.Message{{Role: "user", Content: "read files", Parts: []portllm.ContentPart{
		{Kind: portllm.ContentPartAttachment, FileName: "report.docx", MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Data: []byte("document"), ParsedDocument: true, SourceFileIDs: []string{"doc"}},
		{Kind: portllm.ContentPartAttachment, FileName: "book.epub", MimeType: "application/epub+zip", Data: []byte("ebook"), ParsedDocument: true, SourceFileIDs: []string{"ebook"}},
	}}}}
	for range 2 {
		out, err := client.Generate(t.Context(), route, input)
		if err != nil || out == nil || out.Text != "ok" {
			t.Fatalf("generate=%#v %v", out, err)
		}
	}
	if uploads != 2 || calls != 2 {
		t.Fatalf("file reuse failed uploads=%d calls=%d", uploads, calls)
	}
}

package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

func containerFileAnnotation(container, id, name string) map[string]any {
	return map[string]any{"type": "container_file_citation", "container_id": container, "file_id": id, "filename": name}
}

func containerFileMessage(text string, annotations ...any) map[string]any {
	return map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": annotations}}}
}

func TestResponsesGeneratedFilesCollectExplicitAnnotationsAndMergeDuplicates(t *testing.T) {
	text := "[Report](sandbox:/mnt/data/report.pdf) [Data](sandbox:/mnt/data/data.csv)"
	first := containerFileAnnotation("cntr_test", "cfile_report", "report.pdf")
	parsed := map[string]any{"output": []any{containerFileMessage(text,
		first, first, containerFileAnnotation("cntr_test", "cfile_data", "data.csv"),
		map[string]any{"type": "url_citation", "url": "https://example.com/report.pdf"},
		map[string]any{"type": "file_citation", "file_id": "file_input", "filename": "input.pdf"},
	)}}
	result := buildGenerateOutputFromParsedForAdapter(portllm.EndpointResponses, portllm.AdapterOpenAIResponses, parsed, false)
	if len(result.GeneratedFiles) != 2 {
		t.Fatalf("files = %#v", result.GeneratedFiles)
	}
	if file := result.GeneratedFiles[0]; file.ID != "openai-container:cntr_test:cfile_report" || file.FileName != "report.pdf" || len(file.SourceLinks) != 1 || file.SourceLinks[0] != "sandbox:/mnt/data/report.pdf" {
		t.Fatalf("report = %#v", file)
	}
	if file := result.GeneratedFiles[1]; len(file.SourceLinks) != 1 || file.SourceLinks[0] != "sandbox:/mnt/data/data.csv" {
		t.Fatalf("data = %#v", file)
	}
	other := buildGenerateOutputFromParsedForAdapter(portllm.EndpointResponses, portllm.AdapterOpenRouterResponses, parsed, false)
	if len(other.GeneratedFiles) != 0 {
		t.Fatalf("non-OpenAI adapter collected files: %#v", other.GeneratedFiles)
	}
}

func TestResponsesGeneratedFilesAvoidAmbiguousSameNameLinks(t *testing.T) {
	left, right := "sandbox:/mnt/data/a/report.pdf", "sandbox:/mnt/data/b/report.pdf"
	text := "[A](" + left + ") [B](" + right + ")"
	a := containerFileAnnotation("cntr_test", "cfile_a", "report.pdf")
	b := containerFileAnnotation("cntr_test", "cfile_b", "report.pdf")
	files := parseResponsesGeneratedFiles(containerFileMessage(text, a, b))
	if len(files) != 2 || len(files[0].SourceLinks) != 0 || len(files[1].SourceLinks) != 0 {
		t.Fatalf("ambiguous files = %#v", files)
	}
	a["start_index"], a["end_index"] = strings.Index(text, left), strings.Index(text, left)+len(left)
	b["start_index"], b["end_index"] = strings.Index(text, right), strings.Index(text, right)+len(right)
	files = parseResponsesGeneratedFiles(containerFileMessage(text, a, b))
	if len(files[0].SourceLinks) != 1 || files[0].SourceLinks[0] != left || len(files[1].SourceLinks) != 1 || files[1].SourceLinks[0] != right {
		t.Fatalf("span-associated files = %#v", files)
	}
}

func TestResponsesGeneratedFilesStreamingAndCompletedMerge(t *testing.T) {
	result := &portllm.GenerateOutput{}
	a := containerFileAnnotation("cntr_test", "cfile_a", "report.pdf")
	for _, event := range []map[string]any{
		{"type": "response.output_text.annotation.added", "annotation": a},
		{"type": "response.output_item.done", "item": containerFileMessage("[A](sandbox:/mnt/data/report.pdf)", a)},
		{"type": "response.completed", "response": map[string]any{"id": "resp_test", "output": []any{containerFileMessage("[B](sandbox:/mnt/data/data.csv)", containerFileAnnotation("cntr_test", "cfile_b", "data.csv"))}}},
	} {
		if err := applyResponsesStreamEvent(portllm.AdapterOpenAIResponses, "", event, "", result, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(result.GeneratedFiles) != 2 || len(result.GeneratedFiles[0].SourceLinks) != 1 || len(result.GeneratedFiles[1].SourceLinks) != 1 {
		t.Fatalf("stream files = %#v", result.GeneratedFiles)
	}
	// Background retrieval and resumed streams share the output merge path.
	mergeGenerateOutput(result, &portllm.GenerateOutput{GeneratedFiles: []portllm.GeneratedFile{{ID: result.GeneratedFiles[0].ID, FileName: "report.pdf", SourceLinks: []string{"sandbox:/mnt/data/second/report.pdf"}}}})
	if len(result.GeneratedFiles) != 2 || len(result.GeneratedFiles[0].SourceLinks) != 2 {
		t.Fatalf("merged files = %#v", result.GeneratedFiles)
	}
}

func TestGeneratedFileMergeDoesNotRestoreAmbiguousLinkFromRepeatedEvent(t *testing.T) {
	a := portllm.GeneratedFile{ID: "openai-container:cntr_test:cfile_a", FileName: "report.pdf", SourceLinks: []string{"sandbox:/mnt/data/report.pdf"}}
	b := portllm.GeneratedFile{ID: "openai-container:cntr_test:cfile_b", FileName: "report.pdf", SourceLinks: []string{"sandbox:/mnt/data/report.pdf"}}
	files := mergeGeneratedFiles(nil, a, b)
	files = mergeGeneratedFiles(files, a)
	if len(files) != 2 || len(files[0].SourceLinks) != 0 || len(files[1].SourceLinks) != 0 {
		t.Fatalf("ambiguous link restored: %#v", files)
	}
}

func TestOpenGeneratedFileStreamsContentWithRouteHeaders(t *testing.T) {
	releaseBody := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/containers/cntr_test/files/cfile_test/content" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-Provider") != "custom" {
			t.Errorf("unexpected request: %s %s auth=%q custom=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Provider"))
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-releaseBody:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "a,b\n")
	}))
	defer server.Close()
	client := NewClient(security.NewStrictOutboundPolicy(true))
	defer client.CloseIdleConnections()
	content, err := client.OpenGeneratedFile(context.Background(), portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: server.URL + "/v1", APIKey: "test-key", HeadersJSON: `{"X-Provider":"custom"}`}, portllm.GeneratedFile{ID: "openai-container:cntr_test:cfile_test"})
	close(releaseBody)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(content.Body)
	if err != nil || string(body) != "a,b\n" || content.SizeBytes != 4 || content.MimeType != "text/csv" {
		t.Fatalf("content = %#v, body = %q, err = %v", content, body, err)
	}
}

func TestOpenGeneratedFileRejectsInvalidSourcesBeforeRequest(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	client := NewClient(security.NewStrictOutboundPolicy(true))
	defer client.CloseIdleConnections()
	for _, id := range []string{"https://example.com/file", "sandbox:/mnt/data/file", "openai-container:cntr_x:cfile_x/../secret", "openai-container:cntr_x:cfile_x?token=x", "openai-container:cntr_x:file_x", "openai-container:cntr_x:cfile_x:extra"} {
		_, err := client.OpenGeneratedFile(context.Background(), portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: server.URL}, portllm.GeneratedFile{ID: id})
		if err == nil {
			t.Errorf("accepted source %q", id)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid sources reached server %d times", hits.Load())
	}
}

func TestOpenGeneratedFileReportsUpstreamErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = io.WriteString(w, `{"error":{"message":"container expired"}}`)
	}))
	defer server.Close()
	client := NewClient(security.NewStrictOutboundPolicy(true))
	defer client.CloseIdleConnections()
	_, err := client.OpenGeneratedFile(context.Background(), portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: server.URL}, portllm.GeneratedFile{ID: "openai-container:cntr_test:cfile_test"})
	var upstream *portllm.UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusGone || !strings.Contains(upstream.Message, "container expired") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenGeneratedFileRefusesRedirectsAndUntrustedLoopback(t *testing.T) {
	var targetHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetHits.Add(1); w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	client := NewClient(security.NewStrictOutboundPolicy(true))
	defer client.CloseIdleConnections()
	route := portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: redirector.URL, APIKey: "test-key", HeadersJSON: `{"X-Secret":"private"}`}
	file := portllm.GeneratedFile{ID: "openai-container:cntr_test:cfile_test"}
	if _, err := client.OpenGeneratedFile(context.Background(), route, file); !errors.Is(err, errGeneratedFileRedirect) {
		t.Fatalf("redirect error = %v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect leaked credentials to %d requests", targetHits.Load())
	}
	route.UntrustedEndpoint = true
	if _, err := client.OpenGeneratedFile(context.Background(), route, file); !errors.Is(err, security.ErrUnsafeOutboundURL) {
		t.Fatalf("untrusted endpoint error = %v", err)
	}
}

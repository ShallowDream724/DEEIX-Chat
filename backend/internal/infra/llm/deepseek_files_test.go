package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

type nativeFileMemoryStore struct {
	mu      sync.Mutex
	records map[string]portllm.NativeFileRecord
}

func (s *nativeFileMemoryStore) Load(_ context.Context, key string) (portllm.NativeFileRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key]
	return r, ok, nil
}
func (s *nativeFileMemoryStore) Claim(_ context.Context, r portllm.NativeFileRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = map[string]portllm.NativeFileRecord{}
	}
	old, ok := s.records[r.Key]
	if ok && old.LeaseUntil.After(time.Now()) {
		return false, nil
	}
	if ok {
		old.ClaimToken, old.LeaseUntil = r.ClaimToken, r.LeaseUntil
		r = old
	}
	s.records[r.Key] = r
	return true, nil
}
func (s *nativeFileMemoryStore) Save(_ context.Context, r portllm.NativeFileRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[r.Key].ClaimToken != r.ClaimToken {
		return fmt.Errorf("claim lost")
	}
	s.records[r.Key] = r
	return nil
}

func nativeFileTestInput(user uint) portllm.GenerateInput {
	return portllm.GenerateInput{UserID: user, NativeFiles: true, Messages: []portllm.Message{{Role: "user", Parts: []portllm.ContentPart{{Kind: portllm.ContentPartDocument, Data: []byte("%PDF-test"), MimeType: "application/pdf", FileName: "report.pdf", PageCount: 2, SourceFileIDs: []string{"local-file"}}}}}}
}

func TestDeepSeekFilesCacheScopesUsersCredentialsAndConcurrentClaims(t *testing.T) {
	var uploads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/files" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", 400)
			return
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		first, _ := reader.NextPart()
		if first.FormName() != "purpose" {
			t.Errorf("purpose must precede file")
		}
		purpose, _ := io.ReadAll(first)
		if string(purpose) != "rich_layout_recognition" {
			t.Errorf("wrong purpose %s", purpose)
		}
		if err = r.ParseMultipartForm(1 << 20); err == nil { /* MultipartReader already owns parsing. */
		}
		n := uploads.Add(1)
		fmt.Fprintf(w, `{"id":"file-api-%d","expires_at":%d}`, n, time.Now().Add(24*time.Hour).Unix())
	}))
	defer server.Close()
	client := newTestClient()
	client.SetNativeFileStore(&nativeFileMemoryStore{})
	route := portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: server.URL, APIKey: "first", UpstreamModel: "deepseek-flash"}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, err := client.prepareDeepSeekFiles(t.Context(), route, nativeFileTestInput(1))
			if err != nil {
				t.Error(err)
				return
			}
			if output.Messages[0].Parts[0].ProviderFileID != "file-api-1" {
				t.Error("concurrent callers did not share upload")
			}
		}()
	}
	wg.Wait()
	if uploads.Load() != 1 {
		t.Fatalf("concurrent parse count %d", uploads.Load())
	}
	if _, err := client.prepareDeepSeekFiles(t.Context(), route, nativeFileTestInput(2)); err != nil {
		t.Fatal(err)
	}
	route.APIKey = "second"
	if _, err := client.prepareDeepSeekFiles(t.Context(), route, nativeFileTestInput(1)); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 3 {
		t.Fatalf("user or credential scope crossed: uploads=%d", uploads.Load())
	}
}

func TestDeepSeekFilesAmbiguousUploadRecoversWithoutPaidRetry(t *testing.T) {
	var uploads int
	name := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			uploads++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			name = r.MultipartForm.File["file"][0].Filename
			_, _ = w.Write([]byte(`{"error":{"message":"interrupted after parsing"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "file-api-recovered", "filename": name, "purpose": "rich_layout_recognition", "bytes": 9, "expires_at": time.Now().Add(time.Hour).Unix()}}, "has_more": false})
	}))
	defer server.Close()
	client := newTestClient()
	client.SetNativeFileStore(&nativeFileMemoryStore{})
	route := portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: server.URL, UpstreamModel: "deepseek-flash"}
	if _, err := client.prepareDeepSeekFiles(t.Context(), route, nativeFileTestInput(1)); err == nil {
		t.Fatal("HTTP 200 error accepted")
	}
	output, err := client.prepareDeepSeekFiles(t.Context(), route, nativeFileTestInput(1))
	if err != nil {
		t.Fatal(err)
	}
	if uploads != 1 || output.Messages[0].Parts[0].ProviderFileID != "file-api-recovered" {
		t.Fatalf("ambiguous result reuploaded: uploads=%d output=%#v", uploads, output)
	}
}

func TestDeepSeekNativeFileBlocksUseProtocolIDs(t *testing.T) {
	part := portllm.ContentPart{Kind: portllm.ContentPartDocument, ProviderFileID: "file-api-id", FileName: "x.pdf"}
	msg := portllm.Message{Role: "user", Parts: []portllm.ContentPart{part}}
	chat := buildChatCompletionsContent(msg, nil).([]map[string]any)[0]
	if chat["type"] != "file" || chat["file_id"] != "file-api-id" || chat["file"] != nil {
		t.Fatalf("wrong chat file: %#v", chat)
	}
	responses := buildResponsesAPIContent(msg, nil)[0]
	if responses["file_id"] != "file-api-id" || responses["file_data"] != nil {
		t.Fatalf("wrong Responses file: %#v", responses)
	}
	content, _ := buildAnthropicContent(msg, nil)
	block := content.([]map[string]any)[0]
	if asMap(block["source"])["file_id"] != "file-api-id" {
		t.Fatalf("wrong Messages file: %#v", block)
	}
}

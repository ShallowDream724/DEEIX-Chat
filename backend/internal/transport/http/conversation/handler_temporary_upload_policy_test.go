package conversation

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/gin-gonic/gin"
)

func TestTemporaryChatJSONUsesConfiguredRequestBudget(t *testing.T) {
	h := &Handler{cfg: config.NewRuntime(config.Config{TemporaryChatRequestMaxBytes: 64})}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"content":"`+strings.Repeat("a", 100)+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	_, _, cleanup, ok := h.bindTemporaryChatRequest(c)
	cleanup()
	if ok || w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ok=%v status=%d, want 413", ok, w.Code)
	}
}

func TestTemporaryChatMultipartUsesConfiguredAttachmentCount(t *testing.T) {
	for _, limit := range []int{1, 2} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("payload", `{"sessionID":"s","clientRunID":"r","model":"m","messages":[{"role":"user","content":"question"}]}`); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("attachmentMessageIndexes", "[0,0]"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			part, err := writer.CreateFormFile("attachments", "notes.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(bytes.Repeat([]byte("a"), 30)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		h := &Handler{cfg: config.NewRuntime(config.Config{MaxUploadFileBytes: 20, FileDocMaxBytes: 50, TemporaryChatRequestMaxBytes: 1024, TemporaryChatMaxAttachments: limit})}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		_, files, cleanup, ok := h.bindTemporaryChatRequest(c)
		cleanup()
		if ok != (limit == 2) {
			t.Fatalf("limit=%d ok=%v status=%d", limit, ok, w.Code)
		}
		if ok && len(files) != 2 {
			t.Fatalf("got %d files", len(files))
		}
	}
}

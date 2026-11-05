package conversation

import (
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestUpstreamErrorSummaryRedactsAttachmentFetchCredential(t *testing.T) {
	message := "attachment failed: https://files.example/api/v1/attachments/fetch/private-grant_token"
	summary := upstreamErrorSummary(&llm.UpstreamError{StatusCode: 400, Message: message})
	if strings.Contains(summary, "private-grant_token") || !strings.Contains(summary, "attachments/fetch/REDACTED") {
		t.Fatalf("credential exposed: %s", summary)
	}
}

func TestUpstreamDebugSnapshotRedactsAttachmentFetchPath(t *testing.T) {
	original := &llm.UpstreamDebugSnapshot{Request: llm.UpstreamDebugRequest{Path: "/api/v1/attachments/fetch/private-grant_token", Body: `{"file_url":"https://files.example/api/v1/attachments/fetch/private-grant_token"}`}}
	sanitized := sanitizeUpstreamDebugSnapshot(original)
	if strings.Contains(sanitized.Request.Path, "private-grant_token") || strings.Contains(sanitized.Request.Body, "private-grant_token") {
		t.Fatalf("credential exposed: %#v", sanitized)
	}
	if original.Request.Path != "/api/v1/attachments/fetch/private-grant_token" {
		t.Fatal("diagnostic redaction modified actual request")
	}
}

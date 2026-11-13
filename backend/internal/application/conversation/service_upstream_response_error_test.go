package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestSuccessfulUpstreamResponseErrorSummaryUsesCause(t *testing.T) {
	var malformed any
	formatErr := json.Unmarshal([]byte("event: message"), &malformed)
	truncatedJSONErr := json.Unmarshal([]byte(`{"content":`), &malformed)
	var object map[string]any
	typeErr := json.Unmarshal([]byte(`[]`), &object)
	for _, tt := range []struct {
		name  string
		cause error
		want  string
	}{
		{name: "deadline", cause: context.DeadlineExceeded, want: "模型响应超时"},
		{name: "network timeout", cause: &net.DNSError{IsTimeout: true}, want: "模型响应超时"},
		{name: "canceled", cause: context.Canceled, want: "模型响应已取消"},
		{name: "generation canceled", cause: ErrMessageGenerationCanceled, want: "模型响应已取消"},
		{name: "socket", cause: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}, want: "模型响应中断"},
		{name: "truncated body", cause: io.ErrUnexpectedEOF, want: "模型响应中断"},
		{name: "premature eof", cause: io.EOF, want: "模型响应中断"},
		{name: "truncated json", cause: truncatedJSONErr, want: "模型响应中断"},
		{name: "incompatible format", cause: formatErr, want: "模型响应格式不兼容"},
		{name: "incompatible envelope", cause: typeErr, want: "模型响应格式不兼容"},
		{name: "unknown callback failure", cause: errors.New("callback secret-key data: private-response"), want: "模型响应失败"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &llm.UpstreamError{
				StatusCode: 200,
				Message:    "callback secret-key data: private-response",
				Body:       `{"content":"private-response: stream unsupported; invalid character 'e' looking for beginning of value"}`,
				Cause:      fmt.Errorf("consume response: %w", tt.cause),
				Debug: &llm.UpstreamDebugSnapshot{
					Request:  llm.UpstreamDebugRequest{Path: "/v1/images/generations", Body: `{"stream":true}`},
					Response: llm.UpstreamDebugResponse{StatusCode: 200, Body: "data: private-response"},
				},
			}
			err := wrapUpstreamRequestError(llm.MarkRequestAccepted(upstream))
			summary := messageErrorSummary(err)
			if !strings.HasPrefix(summary, tt.want+"（HTTP 200）") {
				t.Fatalf("summary = %q, want prefix %q", summary, tt.want)
			}
			for _, secret := range []string{"secret-key", "private-response", "data:"} {
				if strings.Contains(summary, secret) {
					t.Fatalf("summary leaked %q: %q", secret, summary)
				}
			}
			if !errors.Is(err, tt.cause) || !llm.RequestWasAccepted(err) {
				t.Fatal("summary classification changed the error cause or request acceptance")
			}
			if tt.want != "模型响应格式不兼容" {
				if MessageErrorCode(err) == MessageErrorCodeMediaImageStreamUnsupported || strings.Contains(summary, "image.stream=false") {
					t.Fatalf("transport failure received image configuration guidance: %q", summary)
				}
			}
			if (&generationAttemptObservation{}).canRetry(err, shouldFallbackToNonStreaming) {
				t.Fatal("accepted reader/parser/callback failure must not trigger a non-streaming replay")
			}
			if debug := MessageErrorDebug(err); debug == nil || debug.Response.StatusCode != 200 {
				t.Fatalf("debug response lost successful handshake status: %#v", debug)
			}
		})
	}
}

func TestSuccessfulUpstreamBusinessErrorIsNotFormatFailure(t *testing.T) {
	err := wrapUpstreamRequestError(&llm.UpstreamError{
		StatusCode: 200,
		Message:    "model capacity exhausted",
		Body:       `{"error":{"message":"model capacity exhausted"}}`,
	})
	if got := messageErrorSummary(err); got != "模型响应失败（HTTP 200）\n错误：model capacity exhausted" {
		t.Fatalf("unexpected business error summary: %q", got)
	}
}

func TestSuccessfulUpstreamErrorSummaryHidesRawResponseMessage(t *testing.T) {
	for _, body := range []string{
		`{"error":{"api_key":"secret-key","message":"private-response"}}`,
		`<html>private-response secret-key</html>`,
	} {
		err := &llm.UpstreamError{StatusCode: 200, Message: body, Body: body}
		if summary := messageErrorSummary(err); summary != "模型响应失败（HTTP 200）" {
			t.Fatalf("raw response message was exposed: %q", summary)
		}
	}
}

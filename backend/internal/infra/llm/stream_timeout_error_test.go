package llm

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestStreamIdleTimeoutPreservesTimeoutClassificationThroughDebug(t *testing.T) {
	cause := &streamIdleTimeoutError{timeout: time.Minute}
	err := portllm.MarkRequestAccepted(attachUpstreamDebug(fmt.Errorf("read stream: %w", cause), &portllm.UpstreamDebugSnapshot{
		Response: portllm.UpstreamDebugResponse{StatusCode: 200, Body: "data: partial response"},
	}))
	var networkErr net.Error
	if !errors.As(err, &networkErr) || !networkErr.Timeout() {
		t.Fatalf("idle timeout lost timeout classification: %v", err)
	}
	var upstream *portllm.UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != 200 || upstream.Debug.Response.StatusCode != 200 {
		t.Fatalf("idle timeout lost successful HTTP status: %#v", upstream)
	}
	if !errors.Is(err, cause) || !portllm.RequestWasAccepted(err) {
		t.Fatal("idle timeout lost its cause or acceptance marker")
	}
	if cause.Error() != "stream idle timeout: no data received for 1m0s" {
		t.Fatalf("idle timeout detail changed: %q", cause.Error())
	}
}

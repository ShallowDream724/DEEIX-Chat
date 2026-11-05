package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/outboundhttp"
	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

const chat01TestArtifactURL = "https://files.chat01.ai/python-generations/run-123/report.csv"
const chat01TestImageURL = "https://files.chat01.ai/user-ABCDEFGHIJKLMNOPQRSTUVWX__11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.png"

func chat01TestArtifact(raw string, image bool) portllm.GeneratedFile {
	digest := sha256.Sum256([]byte(raw))
	prefix := "chat01-url:"
	if image {
		prefix = "chat01-image-url:"
	}
	return portllm.GeneratedFile{ID: prefix + hex.EncodeToString(digest[:]), FileName: "report.csv", SourceLinks: []string{raw}}
}

func chat01ArtifactTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client := NewClient(security.NewStrictOutboundPolicy(false))
	client.untrustedClients = outboundhttp.NewPool(security.NewPublicOnlyOutboundPolicy(), 4, func(policy security.OutboundPolicy, origin, variant string) (outboundhttp.ManagedClient, error) {
		if !policy.Enforced() || origin != "" {
			t.Fatal("CDN received provider origin trust")
		}
		managed, err := newUntrustedRouteHTTPClient(policy, variant)
		if err != nil {
			return managed, err
		}
		managed.Client.Transport = transport
		return managed, nil
	})
	t.Cleanup(client.CloseIdleConnections)
	return client
}

type chat01StreamingBody struct {
	ctx    context.Context
	reads  atomic.Int32
	closed atomic.Bool
}

func (b *chat01StreamingBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return copy(p, "a,b\n"), io.EOF
}
func (b *chat01StreamingBody) Close() error { b.closed.Store(true); return nil }

func TestChat01GeneratedFileStreamsWithoutProviderCredentials(t *testing.T) {
	var body *chat01StreamingBody
	client := chat01ArtifactTestClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != chat01TestArtifactURL || req.Method != http.MethodGet {
			t.Fatal(req.Method, req.URL)
		}
		for _, key := range []string{"Authorization", "Cookie", "X-API-Key", "X-Secret", "X-Provider"} {
			if req.Header.Get(key) != "" {
				t.Fatalf("route header %s leaked", key)
			}
		}
		body = &chat01StreamingBody{ctx: req.Context()}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/csv; charset=utf-8"}}, Body: body, ContentLength: 4, Request: req}, nil
	}))
	content, err := client.OpenGeneratedFile(t.Context(), portllm.RouteConfig{Protocol: portllm.AdapterChat01ChatCompletions, BaseURL: "http://127.0.0.1/private-api", APIKey: "provider-key", ReadTimeoutMS: 9_000_000, HeadersJSON: `{"Authorization":"Bearer custom","Cookie":"session=secret","X-Secret":"private","X-Provider":"private"}`}, chat01TestArtifact(chat01TestArtifactURL, false))
	if err != nil {
		t.Fatal(err)
	}
	if body.reads.Load() != 0 || content.SizeBytes != 4 || content.MimeType != "text/csv" {
		t.Fatalf("download was buffered: %#v", content)
	}
	if deadline, ok := body.ctx.Deadline(); !ok || time.Until(deadline) > chat01ArtifactDownloadTimeout || time.Until(deadline) < chat01ArtifactDownloadTimeout-time.Second {
		t.Fatal("CDN inherited generation deadline", deadline)
	}
	data, err := io.ReadAll(content.Body)
	if err != nil || string(data) != "a,b\n" {
		t.Fatalf("%q %v", data, err)
	}
	if err := content.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if !body.closed.Load() || body.ctx.Err() == nil {
		t.Fatal("body close did not cancel download")
	}
}

func TestChat01GeneratedSourceRejectsUnverifiedURLsAndIdentity(t *testing.T) {
	var hits atomic.Int32
	client := chat01ArtifactTestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) { hits.Add(1); return nil, errors.New("unexpected request") }))
	var rejected []portllm.GeneratedFile
	for _, raw := range []string{"https://example.org/report.csv", "http://files.chat01.ai/python-generations/run/report.csv", "https://files.chat01.ai.attacker.invalid/python-generations/run/report.csv", "https://files.chat01.ai/user-uploads/report.csv", "https://files.chat01.ai/python-generations/../secret", "https://files.chat01.ai/python-generations/%2e%2e/secret", "https://user@files.chat01.ai/python-generations/run/report.csv", "https://files.chat01.ai/python-generations/run/report.csv#fragment", "sandbox:/mnt/data/report.csv", chat01TestImageURL} {
		rejected = append(rejected, chat01TestArtifact(raw, false))
	}
	wrongID := chat01TestArtifact(chat01TestArtifactURL, false)
	wrongID.ID = "chat01-url:" + strings.Repeat("0", 64)
	rejected = append(rejected, wrongID)
	duplicated := chat01TestArtifact(chat01TestArtifactURL, false)
	duplicated.SourceLinks = append(duplicated.SourceLinks, chat01TestArtifactURL)
	rejected = append(rejected, duplicated)
	for _, file := range rejected {
		if _, err := client.OpenGeneratedFile(t.Context(), portllm.RouteConfig{Protocol: portllm.AdapterChat01ChatCompletions}, file); err == nil {
			t.Errorf("accepted %#v", file)
		}
	}
	if _, err := client.OpenGeneratedFile(t.Context(), portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, BaseURL: "https://api.example.test"}, chat01TestArtifact(chat01TestArtifactURL, false)); err == nil {
		t.Fatal("Chat01 source accepted on OpenAI route")
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid sources made %d requests", hits.Load())
	}
	for _, file := range []portllm.GeneratedFile{chat01TestArtifact(chat01TestArtifactURL, false), chat01TestArtifact(chat01TestImageURL, true), chat01TestArtifact(chat01TestArtifactURL, true)} {
		if _, err := chat01ArtifactURL(file); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChat01GeneratedFileRejectsRedirectAndReportsHTTPFailure(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var hits atomic.Int32
			client := chat01ArtifactTestClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				hits.Add(1)
				headers := http.Header{"Location": []string{"https://example.org/private"}}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader("private upstream body")), Request: req}, nil
			}))
			_, err := client.OpenGeneratedFile(t.Context(), portllm.RouteConfig{Protocol: portllm.AdapterChat01ChatCompletions}, chat01TestArtifact(chat01TestArtifactURL, false))
			if status == http.StatusFound {
				if !errors.Is(err, errUntrustedRedirect) {
					t.Fatal(err)
				}
			} else {
				var upstream *portllm.UpstreamError
				if !errors.As(err, &upstream) || upstream.StatusCode != status || !strings.Contains(upstream.Message, "410") || upstream.Body != "" {
					t.Fatal(err)
				}
			}
			if hits.Load() != 1 {
				t.Fatal("redirect was followed")
			}
		})
	}
}

func TestChat01GeneratedDownloadUsesRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var body *chat01StreamingBody
	client := chat01ArtifactTestClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body = &chat01StreamingBody{ctx: req.Context()}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, ContentLength: -1, Request: req}, nil
	}))
	content, err := client.OpenGeneratedFile(ctx, portllm.RouteConfig{Protocol: portllm.AdapterChat01ChatCompletions}, chat01TestArtifact(chat01TestArtifactURL, false))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := content.Body.Read(make([]byte, 4)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_ = content.Body.Close()
	if !body.closed.Load() {
		t.Fatal("canceled body not released")
	}
}

type chat01BlockedBody struct {
	done chan struct{}
	once sync.Once
}

func (b *chat01BlockedBody) Read([]byte) (int, error) { <-b.done; return 0, io.ErrClosedPipe }
func (b *chat01BlockedBody) Close() error             { b.once.Do(func() { close(b.done) }); return nil }

func TestChat01ArtifactIdleTimeoutClosesBlockedStream(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := &chat01BlockedBody{done: make(chan struct{})}
	body := newChat01ArtifactBody(ctx, source, cancel, 10*time.Millisecond)
	defer body.Close()
	result := make(chan error, 1)
	go func() { _, err := body.Read(make([]byte, 4)); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, errChat01ArtifactIdleTimeout) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle timeout left read blocked")
	}
	if ctx.Err() == nil {
		t.Fatal("idle timeout did not cancel network request")
	}
}

func TestGeneratedDownloadTransportAvoidsFullURLTracing(t *testing.T) {
	managed, err := newRouteHTTPClient(security.NewPublicOnlyOutboundPolicy(), security.NewPublicOnlyOutboundPolicy(), "", generatedFileDownloadVariant+"30000")
	if err != nil {
		t.Fatal(err)
	}
	defer managed.CloseIdleConnections()
	if _, ok := managed.Client.Transport.(*http.Transport); !ok {
		t.Fatalf("signed artifact URLs enter tracing transport %T", managed.Client.Transport)
	}
}

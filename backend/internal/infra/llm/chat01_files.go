package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	chat01parse "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm/chat01"
	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

const (
	chat01ArtifactDownloadTimeout = 2 * time.Minute
	chat01ArtifactIdleTimeout     = 30 * time.Second
)

var errChat01ArtifactIdleTimeout = errors.New("Chat01 artifact download idle timeout")

// chat01ArtifactURL verifies provenance again at the network boundary. Only
// explicit generated-file namespaces qualify; a filename or generic CDN URL does not.
func chat01ArtifactURL(file portllm.GeneratedFile) (string, error) {
	if len(file.SourceLinks) != 1 {
		return "", fmt.Errorf("invalid Chat01 artifact source")
	}
	raw := file.SourceLinks[0]
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "files.chat01.ai") || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("invalid Chat01 artifact source")
	}
	pythonFile := strings.HasPrefix(u.Path, "/python-generations/") && path.Clean(u.Path) == u.Path && u.Path != "/python-generations/" && !strings.ContainsAny(u.Path, "\\%\x00\r\n")
	// Encoded separators/dot segments do not enlarge the accepted namespace.
	escaped := strings.ToLower(u.EscapedPath())
	pythonFile = pythonFile && !strings.Contains(escaped, "%2f") && !strings.Contains(escaped, "%5c") && !strings.Contains(escaped, "%2e")
	prefix := "chat01-url:"
	if strings.HasPrefix(file.ID, "chat01-image-url:") {
		prefix = "chat01-image-url:"
		if !pythonFile && !chat01parse.IsGeneratedImageURL(raw) {
			return "", fmt.Errorf("invalid Chat01 generated image source")
		}
	} else if !pythonFile {
		return "", fmt.Errorf("invalid Chat01 generated file source")
	}
	digest := sha256.Sum256([]byte(raw))
	if file.ID != prefix+hex.EncodeToString(digest[:]) {
		return "", fmt.Errorf("Chat01 artifact source identity mismatch")
	}
	return raw, nil
}

func (c *Client) openChat01GeneratedFile(ctx context.Context, route portllm.RouteConfig, file portllm.GeneratedFile) (*portllm.GeneratedFileContent, error) {
	raw, err := chat01ArtifactURL(file)
	if err != nil {
		return nil, err
	}
	if c.untrustedClients == nil {
		return nil, fmt.Errorf("Chat01 artifact downloader is not configured")
	}
	// Generation may legitimately take 150 minutes; fetching its finished output
	// uses the existing media download scale (60s images / 120s videos), not that deadline.
	requestCtx, cancel := context.WithTimeout(ctx, chat01ArtifactDownloadTimeout)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, raw, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	// This pool always uses PublicOnly policy with no trusted origin and rejects
	// redirects. Provider API keys and additional route headers never reach CDN.
	connectTimeout := min(normalizeConnectTimeoutMS(route.ConnectTimeoutMS), 30_000)
	variant := generatedFileDownloadVariant + strconv.Itoa(connectTimeout)
	resp, err := c.untrustedClients.Do(req, "", variant)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, &portllm.UpstreamError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("Chat01 artifact download failed: HTTP %d", resp.StatusCode)}
	}
	mimeType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return &portllm.GeneratedFileContent{Body: newChat01ArtifactBody(requestCtx, resp.Body, cancel, chat01ArtifactIdleTimeout), SizeBytes: resp.ContentLength, MimeType: mimeType}, nil
}

// The watchdog closes the actual response stream on idle, so a timed-out Read
// cannot keep a goroutine writing into a caller buffer after Read has returned.
type chat01ArtifactBody struct {
	reader       io.ReadCloser
	ctx          context.Context
	cancel       context.CancelFunc
	idle         time.Duration
	mu           sync.Mutex
	lastProgress time.Time
	timer        *time.Timer
	timedOut     bool
	closed       bool
	once         sync.Once
	done         chan struct{}
}

func newChat01ArtifactBody(ctx context.Context, reader io.ReadCloser, cancel context.CancelFunc, idle time.Duration) *chat01ArtifactBody {
	b := &chat01ArtifactBody{reader: reader, ctx: ctx, cancel: cancel, idle: idle, lastProgress: time.Now(), done: make(chan struct{})}
	b.timer = time.NewTimer(idle)
	go func() {
		for {
			select {
			case <-ctx.Done():
				_ = b.Close()
				return
			case <-b.timer.C:
				b.expire()
			case <-b.done:
				return
			}
		}
	}()
	return b
}
func (b *chat01ArtifactBody) expire() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	remaining := b.idle - time.Since(b.lastProgress)
	if remaining > 0 {
		b.timer.Reset(remaining)
		b.mu.Unlock()
		return
	}
	b.timedOut = true
	b.mu.Unlock()
	_ = b.Close()
}
func (b *chat01ArtifactBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	closed, timedOut := b.closed, b.timedOut
	b.mu.Unlock()
	if timedOut {
		return 0, errChat01ArtifactIdleTimeout
	}
	if closed {
		if err := b.ctx.Err(); err != nil {
			return 0, err
		}
		return 0, io.ErrClosedPipe
	}
	n, err := b.reader.Read(p)
	b.mu.Lock()
	if n > 0 && !b.closed {
		b.lastProgress = time.Now()
		b.timer.Reset(b.idle)
	}
	if err != nil {
		b.timer.Stop()
	}
	timedOut = b.timedOut
	b.mu.Unlock()
	if timedOut {
		return n, errChat01ArtifactIdleTimeout
	}
	return n, err
}
func (b *chat01ArtifactBody) Close() error {
	var err error
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		b.timer.Stop()
		b.mu.Unlock()
		b.cancel()
		err = b.reader.Close()
		close(b.done)
	})
	return err
}

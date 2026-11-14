package speech

import (
	"context"
	"encoding/binary"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// This is an independent wire adapter. Protocol field numbers were researched
// against VoiceKey 0f559e35; no upstream Rust implementation is bundled.
type field struct {
	number int
	value  uint64
	data   []byte
}
type proto []byte

func (p proto) v(n int, v uint64) proto {
	p = binary.AppendUvarint(p, uint64(n<<3))
	return binary.AppendUvarint(p, v)
}
func (p proto) b(n int, b []byte) proto {
	p = binary.AppendUvarint(p, uint64(n<<3|2))
	p = binary.AppendUvarint(p, uint64(len(b)))
	return append(p, b...)
}
func (p proto) s(n int, s string) proto { return p.b(n, []byte(s)) }
func parseProto(b []byte) ([]field, error) {
	if len(b) > 4<<20 {
		return nil, domain.ErrInvalid
	}
	var out []field
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, io.ErrUnexpectedEOF
		}
		b = b[n:]
		f := field{number: int(tag >> 3)}
		switch tag & 7 {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, io.ErrUnexpectedEOF
			}
			f.value = v
			b = b[n:]
		case 2:
			sz, n := binary.Uvarint(b)
			if n <= 0 || sz > uint64(len(b)-n) {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[n:]
			f.data = b[:int(sz)]
			b = b[int(sz):]
		case 1:
			if len(b) < 8 {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[4:]
		default:
			return nil, domain.ErrInvalid
		}
		out = append(out, f)
		if len(out) > 10000 {
			return nil, domain.ErrInvalid
		}
	}
	return out, nil
}
func data(f []field, n int) []byte {
	for _, v := range f {
		if v.number == n {
			return v.data
		}
	}
	return nil
}
func integer(f []field, n int) uint64 {
	for _, v := range f {
		if v.number == n {
			return v.value
		}
	}
	return 0
}

type IdentityStore interface {
	LoadIdentity(context.Context, uint, string) ([]byte, error)
	SaveIdentity(context.Context, uint, string, []byte) error
}

func (s *FileStore) LoadIdentity(ctx context.Context, user uint, provider string) ([]byte, error) {
	p, e := s.path("identity-"+provider+"-"+strconv.FormatUint(uint64(user), 10), ".identity")
	if e != nil {
		return nil, e
	}
	return readIdentity(p)
}
func (s *FileStore) LockIdentity(user uint, provider string) func() {
	var n uint64 = uint64(user)
	for _, v := range []byte(provider) {
		n = n*33 + uint64(v)
	}
	lock := &s.identityLocks[n%64]
	lock.Lock()
	return lock.Unlock
}
func (s *FileStore) SaveIdentity(ctx context.Context, user uint, provider string, b []byte) error {
	p, e := s.path("identity-"+provider+"-"+strconv.FormatUint(uint64(user), 10), ".identity")
	if e != nil {
		return e
	}
	return writeIdentity(p, b)
}

type IMEConfig struct {
	// ResultObserver is an optional controlled diagnostic hook. It receives
	// recognition-result JSON only, never transport headers or device identity.
	ResultObserver func([]byte)
	WSURL          string
	RegisterURL    string
	HTTPClient     *http.Client
	Identities     IdentityStore
}

func dialIME(ctx context.Context, endpoint string, headers http.Header, protocol string) (*websocket.Conn, func(), error) {
	cfg, e := websocket.NewConfig(endpoint, "https://localhost/")
	if e != nil {
		return nil, nil, e
	}
	cfg.Header = headers
	originScheme := "https"
	if cfg.Location.Scheme == "ws" {
		originScheme = "http"
	}
	cfg.Origin = &url.URL{Scheme: originScheme, Host: cfg.Location.Host}
	cfg.Dialer = &net.Dialer{Timeout: 10 * time.Second}
	if protocol != "" {
		cfg.Protocol = []string{protocol}
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	ws, e := cfg.DialContext(dialCtx)
	dialCancel()
	if e != nil {
		return nil, nil, e
	}
	ws.MaxPayloadBytes = 4 << 20
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			ws.Close()
		case <-done:
		}
	}()
	var once sync.Once
	return ws, func() { once.Do(func() { close(done); ws.Close() }) }, nil
}
func sendIME(ws *websocket.Conn, b []byte) error {
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return websocket.Message.Send(ws, b)
}
func recvIME(ws *websocket.Conn, timeout time.Duration) ([]byte, error) {
	_ = ws.SetReadDeadline(time.Now().Add(timeout))
	var b []byte
	e := websocket.Message.Receive(ws, &b)
	return b, e
}

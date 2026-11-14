package speech_test

import (
	"bytes"
	"context"
	"errors"
	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type billing struct {
	mu         sync.Mutex
	usages     []domain.Usage
	authorized []domain.Access
}
type blockingBilling struct {
	billing
	entered chan struct{}
}

func (b *blockingBilling) Authorize(ctx context.Context, a domain.Access) (domain.BillingTicket, error) {
	close(b.entered)
	<-ctx.Done()
	return domain.BillingTicket{}, ctx.Err()
}

func (b *billing) Authorize(ctx context.Context, a domain.Access) (domain.BillingTicket, error) {
	b.mu.Lock()
	b.authorized = append(b.authorized, a)
	b.mu.Unlock()
	return domain.BillingTicket{Reference: a.OperationID, Snapshot: []byte(`{"price":1}`)}, nil
}
func (b *billing) Record(ctx context.Context, u domain.Usage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usages = append(b.usages, u)
	return nil
}

type provider struct {
	calls atomic.Int32
	synth func(context.Context) (io.ReadCloser, string, error)
}

func (p *provider) ASRCapability() *domain.Capability {
	return &domain.Capability{ID: "doubao-ime", Streaming: true, Formats: []string{"opus"}}
}
func (p *provider) TTSCapability() *domain.Capability { return (&infra.DeepSeek{}).TTSCapability() }
func (p *provider) Transcribe(context.Context, domain.Audio) (domain.Transcript, error) {
	return domain.Transcript{}, nil
}
func (p *provider) Stream(ctx context.Context, r domain.StreamRequest, frames <-chan []byte, emit func(domain.Event) error) (domain.Transcript, error) {
	for {
		select {
		case <-ctx.Done():
			return domain.Transcript{}, ctx.Err()
		case _, ok := <-frames:
			if !ok {
				return domain.Transcript{Text: "final revision"}, nil
			}
			if e := emit(domain.Event{Type: "transcript", Text: "partial"}); e != nil {
				return domain.Transcript{}, e
			}
		}
	}
}
func (p *provider) Synthesize(ctx context.Context, in domain.Synthesis) (io.ReadCloser, string, error) {
	p.calls.Add(1)
	return p.synth(ctx)
}
func service(t *testing.T, p *provider, ttl time.Duration) (*app.Service, *billing) {
	t.Helper()
	store, e := infra.NewFileStore(t.TempDir(), 1<<20, ttl)
	if e != nil {
		t.Fatal(e)
	}
	b := &billing{}
	s := app.NewService(func() app.Config { return app.Config{SelectedInputProvider: "doubao-ime", MaxConcurrentPerUser: 1} }, map[string]domain.Provider{"deepseek": p, "doubao-ime": p}, b, b, store)
	t.Cleanup(func() { _ = s.Close() })
	return s, b
}
func waitStatus(t *testing.T, s *app.Service, id, status string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, e := s.GetTTS(context.Background(), 1, id)
		if e == nil && r.Status == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	r, e := s.GetTTS(context.Background(), 1, id)
	t.Fatalf("wanted %s got %+v %v", status, r, e)
}

type observedWriter struct {
	mu    sync.Mutex
	b     bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (w *observedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, e := w.b.Write(b)
	w.once.Do(func() { close(w.first) })
	return n, e
}
func TestSynthesisStreamsBeforeCompletionAndReusesPerUser(t *testing.T) {
	finish := make(chan struct{})
	p := &provider{synth: func(ctx context.Context) (io.ReadCloser, string, error) {
		r, w := io.Pipe()
		go func() {
			_, _ = w.Write([]byte("first"))
			select {
			case <-finish:
				_, _ = w.Write([]byte("last"))
				_ = w.Close()
			case <-ctx.Done():
				_ = w.CloseWithError(ctx.Err())
			}
		}()
		return r, "audio/wav", nil
	}}
	s, b := service(t, p, time.Hour)
	input := app.TTSInput{Provider: "deepseek", Text: "你好 hello", RequestID: "reply-1"}
	r, e := s.Synthesize(context.Background(), 1, input)
	if e != nil {
		t.Fatal(e)
	}
	writer := &observedWriter{first: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- s.Audio(context.Background(), 1, r.ID, writer, func(string) {}) }()
	select {
	case <-writer.first:
	case <-time.After(2 * time.Second):
		t.Fatal("first audio waited for whole synthesis")
	}
	input.RequestID = "reply-2"
	shared, e := s.Synthesize(context.Background(), 1, input)
	if e != nil || shared.ID != r.ID {
		t.Fatalf("inflight not shared: %+v %v", shared, e)
	}
	close(finish)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	waitStatus(t, s, r.ID, "complete")
	input.RequestID = "reply-3"
	cached, e := s.Synthesize(context.Background(), 1, input)
	if e != nil || !cached.CacheHit || p.calls.Load() != 1 {
		t.Fatalf("cache failed: %+v %v calls=%d", cached, e, p.calls.Load())
	}
	if _, e = s.GetTTS(context.Background(), 2, r.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatalf("cross-user access: %v", e)
	}
	input.Text = "different"
	if _, e = s.Synthesize(context.Background(), 1, input); !errors.Is(e, domain.ErrConflict) {
		t.Fatalf("request identity changed payload: %v", e)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.authorized) != 1 || len(b.usages) != 1 || b.usages[0].ActualUnits != 10 || b.usages[0].Ticket.Reference != r.ID {
		t.Fatalf("unexpected billing %+v %+v", b.authorized, b.usages)
	}
}
func TestUncertainAudioIsNotReusedOrAutomaticallyRetried(t *testing.T) {
	p := &provider{synth: func(context.Context) (io.ReadCloser, string, error) {
		r, w := io.Pipe()
		go func() { _, _ = w.Write([]byte("partial")); _ = w.CloseWithError(io.ErrUnexpectedEOF) }()
		return r, "audio/wav", nil
	}}
	s, _ := service(t, p, time.Hour)
	in := app.TTSInput{Provider: "deepseek", Text: "text", RequestID: "attempt-1"}
	r, e := s.Synthesize(context.Background(), 1, in)
	if e != nil {
		t.Fatal(e)
	}
	waitStatus(t, s, r.ID, "uncertain")
	if e = s.Audio(context.Background(), 1, r.ID, io.Discard, func(string) {}); !errors.Is(e, domain.ErrUncertain) {
		t.Fatalf("partial successful audio: %v", e)
	}
	in.RequestID = "attempt-2"
	same, e := s.Synthesize(context.Background(), 1, in)
	if e != nil || same.ID != r.ID || p.calls.Load() != 1 {
		t.Fatal("uncertain automatically retried")
	}
	in.RequestID = "attempt-3"
	in.Retry = true
	next, e := s.Synthesize(context.Background(), 1, in)
	if e != nil || next.ID == r.ID {
		t.Fatalf("explicit retry failed %+v %v", next, e)
	}
	waitStatus(t, s, next.ID, "uncertain")
}
func TestExpiredRequestReplayDoesNotChargeAgain(t *testing.T) {
	p := &provider{synth: func(context.Context) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewBufferString("audio")), "audio/wav", nil
	}}
	s, _ := service(t, p, time.Nanosecond)
	in := app.TTSInput{Provider: "deepseek", Text: "text", RequestID: "old"}
	old, e := s.Synthesize(context.Background(), 1, in)
	if e != nil {
		t.Fatal(e)
	}
	waitStatus(t, s, old.ID, "expired")
	replay, e := s.Synthesize(context.Background(), 1, in)
	if e != nil || replay.ID != old.ID || p.calls.Load() != 1 {
		t.Fatal("expired request replay charged again")
	}
	in.RequestID = "new"
	fresh, e := s.Synthesize(context.Background(), 1, in)
	if e != nil || fresh.ID == old.ID {
		t.Fatalf("expired cache blocked fresh operation: %+v %v", fresh, e)
	}
	waitStatus(t, s, fresh.ID, "expired")
}
func TestASRFinalTailAndCapacityReleasedOnClose(t *testing.T) {
	s, b := service(t, &provider{}, time.Hour)
	in := app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "draft"}
	session, e := s.CreateSession(context.Background(), 1, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateSession(context.Background(), 1, in); !errors.Is(e, domain.ErrLimited) {
		t.Fatal("missing per-user capacity limit")
	}
	frames := make(chan []byte, 3)
	frames <- []byte{0x98, 1}
	frames <- []byte{0x98, 2}
	frames <- []byte{0x98, 3}
	close(frames)
	var events []domain.Event
	if e = s.Stream(context.Background(), 1, session.ID, frames, func(e domain.Event) error { events = append(events, e); return nil }); e != nil {
		t.Fatal(e)
	}
	if len(events) != 3 || events[2].Text != "final revision" || !events[2].Final || events[2].Sequence != 3 {
		t.Fatalf("tail not committed exactly once: %+v", events)
	}
	if _, e = s.CreateSession(context.Background(), 1, in); e != nil {
		t.Fatal("capacity leaked", e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.usages) != 2 || b.usages[0].ActualUnits != 60 || b.usages[1].Status != "cancelled" {
		t.Fatalf("usage wrong %+v", b.usages)
	}
}
func TestCloseCancelsActiveStreamAndReleasesCapacity(t *testing.T) {
	s, _ := service(t, &provider{}, time.Hour)
	in := app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "draft"}
	session, e := s.CreateSession(context.Background(), 1, in)
	if e != nil {
		t.Fatal(e)
	}
	frames := make(chan []byte)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Stream(context.Background(), 1, session.ID, frames, func(e domain.Event) error {
			if e.Type == "ready" {
				close(ready)
			}
			return nil
		})
	}()
	<-ready
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown kept stream alive")
	}
	if e = <-done; e == nil {
		t.Fatal("cancelled recording became success")
	}
	if _, e = s.CreateSession(context.Background(), 1, in); !errors.Is(e, domain.ErrUnavailable) {
		t.Fatalf("closed service admitted request %v", e)
	}
}
func TestCloseInterruptsAuthorizationWhileSynthesisMutexHeld(t *testing.T) {
	store, e := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	b := &blockingBilling{entered: make(chan struct{})}
	p := &provider{}
	s := app.NewService(nil, map[string]domain.Provider{"deepseek": p}, b, b, store)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	attempt := make(chan error, 1)
	go func() {
		_, e := s.Synthesize(ctx, 1, app.TTSInput{Provider: "deepseek", Text: "text", RequestID: "request"})
		attempt <- e
	}()
	<-b.entered
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for authorization mutex without cancelling it")
	}
	if e = <-attempt; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

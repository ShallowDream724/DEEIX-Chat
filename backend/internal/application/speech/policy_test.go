package speech_test

import (
	"context"
	"errors"
	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
	"sync/atomic"
	"testing"
	"time"
)

type completeProvider struct {
	provider
	transcriptions atomic.Int32
	run            func(context.Context, domain.Audio) (domain.Transcript, error)
}

func (*completeProvider) ASRCapability() *domain.Capability {
	return &domain.Capability{ID: "deepseek", Formats: []string{"wav", "ogg"}, MaxDurationMS: 120000, MaxBytes: 8 << 20}
}
func (p *completeProvider) Transcribe(ctx context.Context, a domain.Audio) (domain.Transcript, error) {
	p.transcriptions.Add(1)
	if p.run != nil {
		return p.run(ctx, a)
	}
	return domain.Transcript{Text: "literal transcript", DurationMS: a.DurationMS}, nil
}
func newPolicyService(t *testing.T) (*app.Service, *billing, *completeProvider, *atomic.Pointer[app.Config]) {
	t.Helper()
	store, e := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	p := &completeProvider{}
	b := &billing{}
	current := &atomic.Pointer[app.Config]{}
	current.Store(&app.Config{SelectedInputProvider: "deepseek", MaxRecordingSeconds: 300, MaxConcurrentPerUser: 1})
	s := app.NewService(func() app.Config { return *current.Load() }, map[string]domain.Provider{"deepseek": p, "wetype": &provider{}, "doubao-ime": &provider{}}, b, b, store)
	t.Cleanup(func() { _ = s.Close() })
	return s, b, p, current
}
func TestCapabilitiesUseOnlyAdministratorSourceAndEffectiveLimit(t *testing.T) {
	s, _, _, current := newPolicyService(t)
	caps := s.Capabilities()
	if caps.SelectedInputProvider != "deepseek" || caps.MaxRecordingDurationMS != 120000 || len(caps.ASR) != 1 || caps.ASR[0].ID != "deepseek" {
		t.Fatal(caps)
	}
	current.Store(&app.Config{SelectedInputProvider: "browser", MaxRecordingSeconds: 75})
	caps = s.Capabilities()
	if caps.SelectedInputProvider != "browser" || caps.MaxRecordingDurationMS != 75000 || len(caps.ASR) != 1 || caps.ASR[0].ID != "browser" {
		t.Fatal("browser limits depended on paid channel", caps)
	}
	if _, e := s.CreateSession(t.Context(), 1, app.SessionInput{Provider: "deepseek", Format: "wav", DraftID: "draft"}); !errors.Is(e, domain.ErrProviderChanged) {
		t.Fatal("client bypassed administrator selection", e)
	}
	if _, e := s.Transcribe(t.Context(), 1, "deepseek", "new", domain.Audio{Bytes: []byte("wav"), Format: "wav", DurationMS: 1000}); !errors.Is(e, domain.ErrProviderChanged) {
		t.Fatal("direct upload bypassed administrator selection", e)
	}
}
func TestNonstreamSessionKeepsStartSnapshotAcrossAdminChangeAndReplays(t *testing.T) {
	s, b, p, current := newPolicyService(t)
	session, e := s.CreateSession(t.Context(), 1, app.SessionInput{Provider: "deepseek", Format: "wav", SampleRate: 16000, DraftID: "draft"})
	if e != nil {
		t.Fatal(e)
	}
	if session.SessionToken == "" || session.WSURL != "" || session.MaxDurationMS != 120000 || time.Until(session.ExpiresAt) < 230*time.Second {
		t.Fatal("nonstream session used streaming 30s ticket lifetime", session)
	}
	current.Store(&app.Config{SelectedInputProvider: "wetype", MaxRecordingSeconds: 10, MaxConcurrentPerUser: 1})
	audio := domain.Audio{Bytes: []byte("same audio"), Format: "wav", DurationMS: 60000}
	result, e := s.TranscribeSession(t.Context(), 1, "deepseek", "attempt", session.ID, session.SessionToken, audio)
	if e != nil || result.Text != "literal transcript" || result.SessionID != session.ID {
		t.Fatal("active recording did not retain start snapshot", result, e)
	}
	replay, e := s.TranscribeSession(t.Context(), 1, "deepseek", "attempt", session.ID, session.SessionToken, audio)
	if e != nil || replay.SessionID != session.ID || p.transcriptions.Load() != 1 {
		t.Fatal("session replay resubmitted upstream", replay, e)
	}
	if _, e = s.TranscribeSession(t.Context(), 2, "deepseek", "attempt", session.ID, session.SessionToken, audio); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("cross-user replay", e)
	}
	if _, e = s.CreateSession(t.Context(), 1, app.SessionInput{Provider: "deepseek", Format: "wav", DraftID: "new"}); !errors.Is(e, domain.ErrProviderChanged) {
		t.Fatal("new recording used old source", e)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.authorized) != 1 || b.authorized[0].Units != 120000 || len(b.usages) != 1 || b.usages[0].OperationID != session.ID || b.usages[0].ActualUnits != 60000 {
		t.Fatal("session budget was re-authorized or lost", b.authorized, b.usages)
	}
}
func TestCancelPendingNonstreamReleasesBudgetAndActiveUploadStaysUncertain(t *testing.T) {
	s, b, p, _ := newPolicyService(t)
	in := app.SessionInput{Provider: "deepseek", Format: "wav", DraftID: "draft"}
	pending, e := s.CreateSession(t.Context(), 1, in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CancelSession(t.Context(), 2, pending.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("cross-user cancel", e)
	}
	if e = s.CancelSession(t.Context(), 1, pending.ID); e != nil {
		t.Fatal(e)
	}
	active, e := s.CreateSession(t.Context(), 1, in)
	if e != nil {
		t.Fatal("cancel leaked user capacity", e)
	}
	started := make(chan struct{})
	p.run = func(ctx context.Context, a domain.Audio) (domain.Transcript, error) {
		close(started)
		<-ctx.Done()
		return domain.Transcript{}, domain.ErrUncertain
	}
	done := make(chan error, 1)
	go func() {
		_, e := s.TranscribeSession(context.Background(), 1, "deepseek", "upload", active.ID, active.SessionToken, domain.Audio{Bytes: []byte("audio"), Format: "wav", DurationMS: 1000})
		done <- e
	}()
	<-started
	if e = s.CancelSession(t.Context(), 1, active.ID); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, domain.ErrUncertain) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop accepted upload")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.usages) != 2 || b.usages[0].Status != "cancelled" || b.usages[1].Status != "uncertain" {
		t.Fatal("accepted cancellation forged refund", b.usages)
	}
}

type gatedBilling struct {
	billing
	entered chan struct{}
	allow   chan struct{}
	calls   atomic.Int32
}

func (b *gatedBilling) Authorize(ctx context.Context, a domain.Access) (domain.BillingTicket, error) {
	if b.calls.Add(1) == 1 {
		close(b.entered)
	}
	select {
	case <-ctx.Done():
		return domain.BillingTicket{}, ctx.Err()
	case <-b.allow:
	}
	return b.billing.Authorize(ctx, a)
}
func TestDirectASRConcurrentReplayCannotReserveOrRefundOwnersTicket(t *testing.T) {
	store, e := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	b := &gatedBilling{entered: make(chan struct{}), allow: make(chan struct{})}
	p := &completeProvider{}
	s := app.NewService(func() app.Config { return app.Config{SelectedInputProvider: "deepseek", MaxConcurrentPerUser: 2} }, map[string]domain.Provider{"deepseek": p}, b, b, store)
	defer s.Close()
	audio := domain.Audio{Bytes: []byte("same complete recording"), Format: "wav", DurationMS: 1000}
	first := make(chan error, 1)
	go func() { _, e := s.Transcribe(context.Background(), 1, "deepseek", "same-request", audio); first <- e }()
	<-b.entered
	if _, e = s.Transcribe(t.Context(), 1, "deepseek", "same-request", audio); !errors.Is(e, domain.ErrUncertain) {
		t.Fatal("concurrent replay did not keep pending identity", e)
	}
	if b.calls.Load() != 1 {
		t.Fatal("concurrent replay reached authorization", b.calls.Load())
	}
	b.mu.Lock()
	usageCount := len(b.usages)
	b.mu.Unlock()
	if usageCount != 0 {
		t.Fatal("replay refunded the working owner")
	}
	close(b.allow)
	if e = <-first; e != nil {
		t.Fatal(e)
	}
	if _, e = s.Transcribe(t.Context(), 1, "deepseek", "same-request", audio); e != nil {
		t.Fatal("completed replay unavailable", e)
	}
	if b.calls.Load() != 1 || p.transcriptions.Load() != 1 {
		t.Fatal("duplicate request spent again", b.calls.Load(), p.transcriptions.Load())
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.authorized) != 1 || len(b.usages) != 1 || b.usages[0].Status != "complete" {
		t.Fatal("wrong authorization/settlement ownership", b.authorized, b.usages)
	}
}

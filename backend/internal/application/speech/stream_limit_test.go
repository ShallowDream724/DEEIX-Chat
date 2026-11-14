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

type limitProvider struct {
	provider
	packets   atomic.Int32
	waitOnEnd bool
	finishErr error
}

func (p *limitProvider) Stream(ctx context.Context, _ domain.StreamRequest, frames <-chan []byte, _ func(domain.Event) error) (domain.Transcript, error) {
	for {
		select {
		case <-ctx.Done():
			return domain.Transcript{}, ctx.Err()
		case _, ok := <-frames:
			if !ok {
				if p.waitOnEnd {
					<-ctx.Done()
					return domain.Transcript{}, ctx.Err()
				}
				return domain.Transcript{Text: "last encoded syllable", Final: true}, p.finishErr
			}
			p.packets.Add(1)
		}
	}
}
func TestStreamDurationPreservesCodecTailAndClampsUsage(t *testing.T) {
	for _, tt := range []struct {
		name       string
		packets    int
		closeInput bool
		wantErr    error
		wait       bool
		upstream   error
		seconds    int
	}{
		{name: "exact", packets: 50, closeInput: true},
		{name: "webcodecs flush", packets: 51, closeInput: true},
		{name: "real 120 second webcodecs flush", packets: 6001, closeInput: true, seconds: 120},
		{name: "maximum codec tail", packets: 54, closeInput: true},
		{name: "excess tail", packets: 55, closeInput: true, wantErr: domain.ErrDurationLimit, wait: true},
		{name: "missing finish bounded", packets: 50},
		{name: "real final error", packets: 51, closeInput: true, wantErr: domain.ErrUnavailable, upstream: domain.ErrUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			seconds := tt.seconds
			if seconds == 0 {
				seconds = 1
			}
			store, e := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
			if e != nil {
				t.Fatal(e)
			}
			p := &limitProvider{waitOnEnd: tt.wait, finishErr: tt.upstream}
			billing := &billing{}
			s := app.NewService(func() app.Config {
				return app.Config{SelectedInputProvider: "doubao-ime", MaxRecordingSeconds: seconds}
			}, map[string]domain.Provider{"doubao-ime": p}, billing, billing, store)
			defer s.Close()
			session, e := s.CreateSession(t.Context(), 1, app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "limit-fixture"})
			if e != nil {
				t.Fatal(e)
			}
			frames := make(chan []byte, tt.packets)
			for i := 0; i < tt.packets; i++ {
				frames <- []byte{0x98, byte(i)}
			}
			if tt.closeInput {
				close(frames)
			}
			final := false
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			e = s.Stream(ctx, 1, session.ID, frames, func(event domain.Event) error { final = event.Final; return nil })
			if !errors.Is(e, tt.wantErr) {
				t.Fatalf("got %v want %v", e, tt.wantErr)
			}
			if final != (tt.wantErr == nil) {
				t.Fatal("incorrect final event", final)
			}
			if tt.wantErr == nil && int(p.packets.Load()) != tt.packets {
				t.Fatalf("encoded tail lost: forwarded=%d input=%d", p.packets.Load(), tt.packets)
			}
			billing.mu.Lock()
			defer billing.mu.Unlock()
			if len(billing.usages) != 1 || billing.usages[0].ActualUnits != int64(seconds*1000) {
				t.Fatal("encoding tail changed effective billed duration", billing.usages)
			}
			if (billing.usages[0].Status == "complete") != (tt.wantErr == nil) {
				t.Fatal("upstream failure disguised as normal limit", billing.usages)
			}
		})
	}
}

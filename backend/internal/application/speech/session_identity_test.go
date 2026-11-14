package speech_test

import (
	"context"
	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
	"testing"
	"time"
)

type recordingIDProvider struct {
	provider
	ids chan string
}

func (p *recordingIDProvider) Stream(ctx context.Context, in domain.StreamRequest, frames <-chan []byte, _ func(domain.Event) error) (domain.Transcript, error) {
	p.ids <- in.SessionID
	for {
		select {
		case <-ctx.Done():
			return domain.Transcript{}, ctx.Err()
		case _, ok := <-frames:
			if !ok {
				return domain.Transcript{Text: in.SessionID, Final: true}, nil
			}
		}
	}
}

func TestSameUserRecordingsHaveIndependentProviderAndBillingIDs(t *testing.T) {
	store, err := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p := &recordingIDProvider{ids: make(chan string, 2)}
	b := &billing{}
	s := app.NewService(func() app.Config { return app.Config{SelectedInputProvider: "doubao-ime", MaxConcurrentPerUser: 2} }, map[string]domain.Provider{"doubao-ime": p}, b, b, store)
	defer s.Close()
	in := app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "same-user-same-draft"}
	first, err := s.CreateSession(t.Context(), 1, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateSession(t.Context(), 1, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.WSURL == second.WSURL {
		t.Fatal("new recording reused upstream identity")
	}
	for _, session := range []app.Session{first, second} {
		frames := make(chan []byte, 1)
		frames <- []byte{0x98, 1}
		close(frames)
		if err = s.Stream(t.Context(), 1, session.ID, frames, func(event domain.Event) error {
			if event.SessionID != session.ID || (event.Final && event.Text != session.ID) {
				t.Fatal("recordings crossed", event)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got := <-p.ids; got != session.ID {
			t.Fatal("provider received wrong voice identity", got)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.usages) != 2 || b.usages[0].OperationID != first.ID || b.usages[1].OperationID != second.ID {
		t.Fatal("new recordings shared accounting identity", b.usages)
	}
}

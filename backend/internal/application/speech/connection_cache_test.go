package speech_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
)

type scopedProvider struct {
	*provider
	identity string
}

func (p *scopedProvider) CacheIdentity(string) string { return p.identity }

func TestTTSCacheSeparatesConnectionsWithoutResubmittingRequestIDs(t *testing.T) {
	store, err := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	makeProvider := func(scope string) *scopedProvider {
		return &scopedProvider{identity: scope, provider: &provider{synth: func(context.Context) (io.ReadCloser, string, error) {
			return io.NopCloser(strings.NewReader("audio")), "audio/wav", nil
		}}}
	}
	first, second := makeProvider("first-connection"), makeProvider("second-connection")
	current := first
	b := &billing{}
	s := app.NewService(nil, map[string]domain.Provider{"deepseek": &infra.DynamicProvider{Resolve: func() domain.Provider { return current }}}, b, b, store)
	defer s.Close()
	input := app.TTSInput{Provider: "deepseek", Text: "hello", RequestID: "first-request"}
	one, err := s.Synthesize(t.Context(), 1, input)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, one.ID, "complete")
	input.RequestID = "same-connection"
	reused, err := s.Synthesize(t.Context(), 1, input)
	if err != nil || reused.ID != one.ID || first.calls.Load() != 1 {
		t.Fatal("same connection did not reuse", err)
	}
	current = second
	input.RequestID = "new-connection"
	two, err := s.Synthesize(t.Context(), 1, input)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, two.ID, "complete")
	if two.ID == one.ID || second.calls.Load() != 1 {
		t.Fatal("cache leaked across connections")
	}
	input.RequestID = "first-request"
	if _, err := s.Synthesize(t.Context(), 1, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("changed credentials silently retried an existing request", err)
	}
	if second.calls.Load() != 1 {
		t.Fatal("request identity was billed again")
	}
}

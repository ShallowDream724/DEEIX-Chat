package speech_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
)

type controlledTTSProvider struct {
	provider
	capability *domain.Capability
	inputs     chan domain.Synthesis
	run        func(context.Context, domain.Synthesis) (io.ReadCloser, string, error)
}

func (p *controlledTTSProvider) TTSCapability() *domain.Capability { return p.capability }
func (p *controlledTTSProvider) Synthesize(ctx context.Context, in domain.Synthesis) (io.ReadCloser, string, error) {
	p.calls.Add(1)
	if p.inputs != nil {
		p.inputs <- in
	}
	if p.run != nil {
		return p.run(ctx, in)
	}
	return io.NopCloser(strings.NewReader("audio")), "audio/wav", nil
}

func mimoCapability() *domain.Capability {
	return &domain.Capability{
		ID: "mimo", Formats: []string{"pcm", "wav"}, Streaming: true,
		Voices: []domain.Voice{{ID: "冰糖"}, {ID: "other"}}, DefaultVoice: "冰糖",
		Speeds: []domain.Voice{{ID: "normal"}, {ID: "fast"}, {ID: "slow"}}, DefaultSpeed: "fast",
		SupportsStyle: true, DefaultStyle: "warm", MaxStyleCharacters: 32,
		MaxWeightedCharacters: 1200, MaxTextBytes: 16 << 10,
	}
}

func controlledTTSService(t *testing.T, cfg func() app.Config, providers map[string]domain.Provider) (*app.Service, *billing, *infra.FileStore) {
	t.Helper()
	store, err := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b := &billing{}
	s := app.NewService(cfg, providers, b, b, store)
	t.Cleanup(func() { _ = s.Close() })
	return s, b, store
}

func stringPointer(value string) *string { return &value }

func TestTTSSelectionExposesAndAcceptsOnlySelectedProvider(t *testing.T) {
	deepseek := &controlledTTSProvider{capability: (&infra.DeepSeek{}).TTSCapability()}
	mimo := &controlledTTSProvider{capability: mimoCapability()}
	current := &atomic.Pointer[app.Config]{}
	current.Store(&app.Config{SelectedTTSProvider: "mimo"})
	s, b, _ := controlledTTSService(t, func() app.Config { return *current.Load() }, map[string]domain.Provider{"deepseek": deepseek, "mimo": mimo})
	caps := s.Capabilities()
	if caps.SelectedTTSProvider != "mimo" || len(caps.TTS) != 1 || caps.TTS[0].ID != "mimo" {
		t.Fatal("capabilities exposed a nonselected TTS provider", caps)
	}
	if _, err := s.Synthesize(t.Context(), 1, app.TTSInput{Provider: "deepseek", Text: "hello", RequestID: "bypass"}); !errors.Is(err, domain.ErrProviderChanged) {
		t.Fatal("client bypassed administrator TTS selection", err)
	}
	accepted, err := s.Synthesize(t.Context(), 1, app.TTSInput{Text: "hello", RequestID: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, accepted.ID, "complete")
	current.Store(&app.Config{})
	caps = s.Capabilities()
	if caps.SelectedTTSProvider != "deepseek" || len(caps.TTS) != 1 || caps.TTS[0].ID != "deepseek" {
		t.Fatal("legacy configuration did not retain DeepSeek", caps)
	}
	if _, err = s.GetTTS(t.Context(), 1, accepted.ID); err != nil {
		t.Fatal("provider change made accepted audio inaccessible", err)
	}
	current.Store(&app.Config{SelectedTTSProvider: "missing"})
	if len(s.Capabilities().TTS) != 0 {
		t.Fatal("missing selected provider exposed another provider")
	}
	if _, err = s.Synthesize(t.Context(), 1, app.TTSInput{Text: "hello", RequestID: "missing"}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("missing selected provider was used", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.authorized) != 1 || b.authorized[0].Provider != "mimo" || deepseek.calls.Load() != 0 {
		t.Fatal("selection mismatch reached billing or upstream", b.authorized)
	}
}

func TestTTSDefaultsOverridesAndResolvedCacheIdentity(t *testing.T) {
	p := &controlledTTSProvider{capability: mimoCapability(), inputs: make(chan domain.Synthesis, 8)}
	s, _, _ := controlledTTSService(t, func() app.Config { return app.Config{SelectedTTSProvider: "mimo"} }, map[string]domain.Provider{"mimo": p})
	input := app.TTSInput{Text: " 你好\r\nworld ", RequestID: "default"}
	first, err := s.Synthesize(t.Context(), 1, input)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, first.ID, "complete")
	if got := <-p.inputs; got.Text != "你好\nworld" || got.Voice != "冰糖" || got.Format != "wav" || got.Speed != "fast" || got.Style != "warm" {
		t.Fatal("provider defaults were not resolved", got)
	}
	input.RequestID, input.Voice, input.Format, input.Speed, input.Style = "explicit-default", "冰糖", " WAV ", " FAST ", stringPointer(" warm ")
	reused, err := s.Synthesize(t.Context(), 1, input)
	if err != nil || reused.ID != first.ID || !reused.CacheHit || p.calls.Load() != 1 {
		t.Fatal("equivalent resolved controls did not reuse audio", reused, err)
	}
	input.RequestID, input.Style = "empty-style", stringPointer("")
	emptyStyle, err := s.Synthesize(t.Context(), 1, input)
	if err != nil || emptyStyle.ID == first.ID {
		t.Fatal("explicit empty style reused default style", emptyStyle, err)
	}
	waitStatus(t, s, emptyStyle.ID, "complete")
	if got := <-p.inputs; got.Style != "" {
		t.Fatal("explicit empty style did not suppress administrator default", got)
	}
	input.RequestID = "default"
	if _, err = s.Synthesize(t.Context(), 1, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("same request ID accepted different resolved style", err)
	}
	input.RequestID, input.Voice, input.Speed, input.Style = "override", "other", " SLOW ", stringPointer(" calm\r\nclear ")
	override, err := s.Synthesize(t.Context(), 1, input)
	if err != nil || override.ID == emptyStyle.ID {
		t.Fatal("user controls reused incompatible audio", override, err)
	}
	waitStatus(t, s, override.ID, "complete")
	if got := <-p.inputs; got.Voice != "other" || got.Speed != "slow" || got.Style != "calm\nclear" {
		t.Fatal("user controls were not passed to provider", got)
	}
	p.capability.DefaultStyle = "bright"
	input = app.TTSInput{Text: "你好\nworld", RequestID: "new-default"}
	changed, err := s.Synthesize(t.Context(), 1, input)
	if err != nil || changed.ID == first.ID {
		t.Fatal("administrator default change reused previous audio", changed, err)
	}
	waitStatus(t, s, changed.ID, "complete")
	if got := <-p.inputs; got.Style != "bright" {
		t.Fatal("administrator default change was ignored", got)
	}
	input.RequestID = "default"
	if _, err = s.Synthesize(t.Context(), 1, input); !errors.Is(err, domain.ErrConflict) || p.calls.Load() != 4 {
		t.Fatal("default change resubmitted an existing paid request", err, p.calls.Load())
	}
}

func TestTTSRejectsUnsupportedControlsAndProviderLimitsBeforeAuthorization(t *testing.T) {
	tests := []struct {
		name string
		cap  *domain.Capability
		in   app.TTSInput
	}{
		{name: "voice", cap: mimoCapability(), in: app.TTSInput{Voice: "stella"}},
		{name: "format", cap: mimoCapability(), in: app.TTSInput{Format: "mp3"}},
		{name: "speed", cap: mimoCapability(), in: app.TTSInput{Speed: "turbo"}},
		{name: "style length", cap: mimoCapability(), in: app.TTSInput{Style: stringPointer(strings.Repeat("好", 33))}},
		{name: "weighted characters", cap: mimoCapability(), in: app.TTSInput{Text: strings.Repeat("好", 601)}},
		{name: "text bytes", cap: &domain.Capability{Formats: []string{"wav"}, MaxTextBytes: 4}, in: app.TTSInput{Text: "好好"}},
		{name: "unsupported speed", cap: (&infra.DeepSeek{}).TTSCapability(), in: app.TTSInput{Speed: "fast"}},
		{name: "unsupported style", cap: (&infra.DeepSeek{}).TTSCapability(), in: app.TTSInput{Style: stringPointer("")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &controlledTTSProvider{capability: test.cap}
			s, b, _ := controlledTTSService(t, func() app.Config { return app.Config{SelectedTTSProvider: "mimo"} }, map[string]domain.Provider{"mimo": p})
			input := test.in
			input.RequestID = "invalid"
			if input.Text == "" {
				input.Text = "hello"
			}
			if _, err := s.Synthesize(t.Context(), 1, input); !errors.Is(err, domain.ErrInvalid) {
				t.Fatal("invalid input was accepted", err)
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if len(b.authorized) != 0 || p.calls.Load() != 0 {
				t.Fatal("invalid input reached billing or upstream")
			}
		})
	}
}

func TestTTSEachAudioControlSeparatesCachedSynthesis(t *testing.T) {
	p := &controlledTTSProvider{capability: mimoCapability()}
	s, _, _ := controlledTTSService(t, func() app.Config { return app.Config{SelectedTTSProvider: "mimo"} }, map[string]domain.Provider{"mimo": p})
	seen := map[string]bool{}
	for _, input := range []app.TTSInput{
		{RequestID: "default"},
		{RequestID: "voice", Voice: "other"},
		{RequestID: "speed", Speed: "slow"},
		{RequestID: "format", Format: "pcm"},
		{RequestID: "style", Style: stringPointer("bright")},
	} {
		input.Text = "same words"
		result, err := s.Synthesize(t.Context(), 1, input)
		if err != nil || seen[result.ID] {
			t.Fatal("different audio control reused previous audio", input.RequestID, result, err)
		}
		waitStatus(t, s, result.ID, "complete")
		seen[result.ID] = true
	}
	if p.calls.Load() != 5 {
		t.Fatal("distinct audio controls did not reach upstream", p.calls.Load())
	}
}

func TestTTSLegacyCapabilitiesInferVoiceAndAllowNoVoice(t *testing.T) {
	for _, test := range []struct {
		name  string
		cap   *domain.Capability
		voice string
	}{
		{name: "deepseek", cap: (&infra.DeepSeek{}).TTSCapability(), voice: "stella"},
		{name: "unspecified voice", cap: &domain.Capability{Formats: []string{"wav"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &controlledTTSProvider{capability: test.cap, inputs: make(chan domain.Synthesis, 1)}
			s, _, _ := controlledTTSService(t, nil, map[string]domain.Provider{"deepseek": p})
			result, err := s.Synthesize(t.Context(), 1, app.TTSInput{Text: "hello", RequestID: "legacy"})
			if err != nil {
				t.Fatal(err)
			}
			waitStatus(t, s, result.ID, "complete")
			if got := <-p.inputs; got.Voice != test.voice || got.Format != "wav" || got.Speed != "" || got.Style != "" {
				t.Fatal("legacy provider defaults changed", got)
			}
		})
	}
}

type metadataAudio struct {
	io.ReadCloser
	finished bool
}

func (m *metadataAudio) Read(p []byte) (int, error) {
	n, err := m.ReadCloser.Read(p)
	if err == io.EOF {
		m.finished = true
	}
	return n, err
}
func (m *metadataAudio) Metadata() domain.SynthesisMetadata {
	if !m.finished {
		return domain.SynthesisMetadata{}
	}
	return domain.SynthesisMetadata{RequestID: "upstream-request", Usage: &domain.TokenUsage{PromptTokens: 30, CompletionTokens: 80, TotalTokens: 110}}
}

func TestTTSRetainsStreamMetadataWithoutChangingCharacterBilling(t *testing.T) {
	p := &controlledTTSProvider{capability: mimoCapability(), run: func(context.Context, domain.Synthesis) (io.ReadCloser, string, error) {
		return &metadataAudio{ReadCloser: io.NopCloser(strings.NewReader("audio"))}, "audio/wav", nil
	}}
	s, b, store := controlledTTSService(t, func() app.Config { return app.Config{SelectedTTSProvider: "mimo"} }, map[string]domain.Provider{"mimo": p})
	result, err := s.Synthesize(t.Context(), 1, app.TTSInput{Text: "你好 hello", RequestID: "metadata"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, result.ID, "complete")
	record, err := store.Get(t.Context(), result.ID)
	if err != nil || record.UpstreamRequestID != "upstream-request" || record.UpstreamUsage == nil || *record.UpstreamUsage != (domain.TokenUsage{PromptTokens: 30, CompletionTokens: 80, TotalTokens: 110}) {
		t.Fatal("final stream metadata was not persisted", record, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.usages) != 1 || b.usages[0].ActualUnits != 10 || b.usages[0].Units != 10 || b.usages[0].UpstreamRequestID != "upstream-request" {
		t.Fatal("upstream tokens replaced site character billing", b.usages)
	}
}

type closingAudio struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (r *closingAudio) Read([]byte) (int, error) {
	close(r.started)
	<-r.closed
	return 0, io.EOF
}
func (r *closingAudio) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestTTSShutdownClosesBlockedAudioAndDoesNotMarkItComplete(t *testing.T) {
	body := &closingAudio{started: make(chan struct{}), closed: make(chan struct{})}
	p := &controlledTTSProvider{capability: mimoCapability(), run: func(context.Context, domain.Synthesis) (io.ReadCloser, string, error) {
		return body, "audio/wav", nil
	}}
	s, b, _ := controlledTTSService(t, func() app.Config { return app.Config{SelectedTTSProvider: "mimo"} }, map[string]domain.Provider{"mimo": p})
	result, err := s.Synthesize(t.Context(), 1, app.TTSInput{Text: "hello", RequestID: "shutdown"})
	if err != nil {
		t.Fatal(err)
	}
	<-body.started
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not close the blocked upstream body")
	}
	waitStatus(t, s, result.ID, "uncertain")
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.usages) != 1 || b.usages[0].Status != "uncertain" {
		t.Fatal("shutdown marked accepted synthesis complete", b.usages)
	}
}

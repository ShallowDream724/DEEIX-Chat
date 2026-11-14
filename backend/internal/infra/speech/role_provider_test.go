package speech

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
)

func TestRoleProviderUsesDistinctConnectionsAndFrozenSnapshots(t *testing.T) {
	makeServer := func(role, key string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/tools/"+role || r.Header.Get("Authorization") != "Bearer "+key {
				t.Error("operation used wrong connection")
			}
			if role == "asr" {
				_, _ = io.WriteString(w, `{"text":"input","duration_ms":1000}`)
			} else {
				_, _ = io.WriteString(w, "audio")
			}
		}))
	}
	asrServer, ttsServer := makeServer("asr", "input-test-key"), makeServer("tts", "output-test-key")
	defer asrServer.Close()
	defer ttsServer.Close()
	asr := NewDeepSeek(ProviderConfig{BaseURL: asrServer.URL, APIKey: "input-test-key"}, asrServer.Client())
	tts := NewDeepSeek(ProviderConfig{BaseURL: ttsServer.URL, APIKey: "output-test-key"}, ttsServer.Client())
	roles := &RoleProvider{ASR: asr, TTS: tts}
	dynamic := &DynamicProvider{ResolveSnapshot: func() (domain.Provider, bool, bool) { return roles, true, true }}
	frozen := dynamic.Snapshot()
	roles = &RoleProvider{}
	if _, err := frozen.Transcribe(context.Background(), domain.Audio{Bytes: []byte{0}, Format: "wav"}); err != nil {
		t.Fatal(err)
	}
	audio, _, err := frozen.Synthesize(context.Background(), domain.Synthesis{Text: "hello", Voice: "stella", Format: "wav"})
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Close()
	if raw, err := io.ReadAll(audio); err != nil || string(raw) != "audio" {
		t.Fatal("TTS request did not use its connection", err)
	}
	identity := frozen.(interface{ CacheIdentity(string) string }).CacheIdentity("tts")
	if identity != tts.CacheIdentity("tts") || identity == asr.CacheIdentity("asr") || strings.Contains(identity, "output-test-key") {
		t.Fatal("cache identity did not preserve the TTS connection")
	}
	changed := NewDeepSeek(ProviderConfig{BaseURL: ttsServer.URL, APIKey: "rotated-test-key"}, ttsServer.Client())
	if changed.CacheIdentity("tts") == identity {
		t.Fatal("cache shared across API keys")
	}
}

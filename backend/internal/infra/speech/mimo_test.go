package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
)

func mimoAudioEvent(pcm []byte) string {
	return fmt.Sprintf("data: {\"id\":\"mimo-request\",\"choices\":[{\"index\":0,\"delta\":{\"audio\":{\"data\":%q}}}]}\n\n", base64.StdEncoding.EncodeToString(pcm))
}

const mimoStopEvent = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"

func TestMiMoRequestAndStreamingWAVMetadata(t *testing.T) {
	pcm := []byte{1, 2, 3, 4, 5, 6}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost || r.Header.Get("api-key") != "test-only" || r.Header.Get("Authorization") != "" {
			t.Error("incorrect MiMo route or authentication")
		}
		var request struct {
			Model    string           `json:"model"`
			Stream   bool             `json:"stream"`
			Messages []mimoMessage    `json:"messages"`
			Audio    mimoAudioRequest `json:"audio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "mimo-v2.5-tts" || !request.Stream || request.Audio.Voice != "冰糖" || request.Audio.Format != "pcm16" {
			t.Errorf("unexpected synthesis payload: %+v", request)
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "user" || !strings.Contains(request.Messages[0].Content, "以较快的日常说话速度朗读，节奏紧凑，句间仅保留必要的短停顿，避免拖长字音。") || !strings.Contains(request.Messages[0].Content, MiMoDefaultStyle) || request.Messages[1].Role != "assistant" || request.Messages[1].Content != "  原文 API，不应改写。\n" {
			t.Errorf("unexpected messages: %+v", request.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, mimoAudioEvent(pcm[:3])+mimoAudioEvent(pcm[3:])+mimoStopEvent+"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":22,\"total_tokens\":33}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := NewMiMo(ProviderConfig{BaseURL: server.URL + "/v1", APIKey: "test-only"}, server.Client())
	stream, contentType, err := provider.Synthesize(context.Background(), domain.Synthesis{Text: "  原文 API，不应改写。\n", Format: "wav", Style: MiMoDefaultStyle})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	audio, err := io.ReadAll(stream)
	if err != nil || contentType != "audio/wav" {
		t.Fatalf("content type %q, error %v", contentType, err)
	}
	if len(audio) != 44+len(pcm) || string(audio[:4]) != "RIFF" || string(audio[8:16]) != "WAVEfmt " || string(audio[36:40]) != "data" || !bytes.Equal(audio[44:], pcm) {
		t.Fatalf("invalid WAV framing: %x", audio)
	}
	if binary.LittleEndian.Uint32(audio[4:]) != ^uint32(0) || binary.LittleEndian.Uint32(audio[40:]) != ^uint32(0) || binary.LittleEndian.Uint32(audio[24:]) != 24000 || binary.LittleEndian.Uint16(audio[22:]) != 1 || binary.LittleEndian.Uint16(audio[34:]) != 16 {
		t.Fatal("incorrect streaming WAV format")
	}
	metadata := stream.(domain.SynthesisMetadataSource).Metadata()
	if metadata.RequestID != "mimo-request" || metadata.Usage == nil || *metadata.Usage != (domain.TokenUsage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33}) {
		t.Fatalf("metadata not preserved: %+v", metadata)
	}
	metadata.Usage.TotalTokens = -1
	if stream.(domain.SynthesisMetadataSource).Metadata().Usage.TotalTokens != 33 {
		t.Fatal("metadata snapshot aliases mutable state")
	}
}

func TestMiMoDeliversAudioBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, mimoAudioEvent([]byte{1, 2, 3, 4}))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, mimoStopEvent+"data: [DONE]\n\n")
	}))
	defer server.Close()
	defer close(release)
	provider := NewMiMo(ProviderConfig{BaseURL: server.URL, APIKey: "test-only"}, server.Client())
	stream, _, err := provider.Synthesize(context.Background(), domain.Synthesis{Text: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	read := make(chan error, 1)
	go func() {
		first := make([]byte, 48)
		_, err := io.ReadFull(stream, first)
		if err == nil && !bytes.Equal(first[44:], []byte{1, 2, 3, 4}) {
			err = errors.New("PCM bytes changed")
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("audio waited for the final upstream response")
	}
}

type mimoTestTransport func(*http.Request) (*http.Response, error)

func (f mimoTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type mimoTrackedBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *mimoTrackedBody) Close() error { b.closed.Store(true); return nil }

type mimoByteReader struct{ io.Reader }

func (r mimoByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func mimoTestProvider(body io.ReadCloser) *MiMo {
	return NewMiMo(ProviderConfig{APIKey: "test-only"}, &http.Client{Transport: mimoTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})})
}

func TestMiMoSSESplitBoundariesAndMultilineData(t *testing.T) {
	wire := ": keepalive\r\nevent: message\r\ndata: {\"choices\":[\r\ndata: {\"delta\":{\"audio\":{\"data\":\"AQIDBA==\"}}}]}\r\n\r\n" + mimoStopEvent
	body := &mimoTrackedBody{Reader: mimoByteReader{strings.NewReader(wire)}}
	stream, _, err := mimoTestProvider(body).Synthesize(context.Background(), domain.Synthesis{Text: "测试", Voice: "Dean", Speed: "normal", Style: "清晰"})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(audio[44:], []byte{1, 2, 3, 4}) {
		t.Fatalf("fragmented SSE failed: audio=%x err=%v", audio, err)
	}
	if !body.closed.Load() {
		t.Fatal("terminal EOF did not close upstream")
	}
}

func TestMiMoSSECarriageReturnSeparators(t *testing.T) {
	wire := strings.ReplaceAll(mimoAudioEvent([]byte{1, 2})+mimoStopEvent+"data: [DONE]\n\n", "\n", "\r")
	stream, _, err := mimoTestProvider(io.NopCloser(mimoByteReader{strings.NewReader(wire)})).Synthesize(context.Background(), domain.Synthesis{Text: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	if audio, err := io.ReadAll(stream); err != nil || len(audio) != 46 {
		t.Fatalf("CR-delimited SSE failed: length=%d err=%v", len(audio), err)
	}
}

func TestMiMoRejectsIncompleteOrMalformedStreams(t *testing.T) {
	audio := mimoAudioEvent([]byte{1, 2})
	tests := map[string]string{
		"missing stop":           audio,
		"done without stop":      audio + "data: [DONE]\n\n",
		"no audio":               mimoStopEvent + "data: [DONE]\n\n",
		"odd PCM":                mimoAudioEvent([]byte{1}) + mimoStopEvent + "data: [DONE]\n\n",
		"length finish":          audio + "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\n",
		"content filter":         audio + "data: {\"choices\":[{\"finish_reason\":\"content_filter\"}]}\n\n",
		"invalid JSON":           audio + "data: {bad}\n\n",
		"invalid base64":         "data: {\"choices\":[{\"delta\":{\"audio\":{\"data\":\"invalid!\"}}}]}\n\n" + mimoStopEvent,
		"truncated event":        audio + "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}",
		"truncated after stop":   audio + mimoStopEvent + "data: {\"usage\":",
		"audio after stop":       audio + mimoStopEvent + audio,
		"upstream error":         audio + "data: {\"error\":{\"message\":\"failed\"}}\n\n",
		"named error after stop": audio + mimoStopEvent + "event: error\ndata: {\"message\":\"failed\"}\n\n",
		"invalid null object":    audio + "data: null\n\n" + mimoStopEvent,
		"negative usage":         audio + mimoStopEvent + "data: {\"usage\":{\"total_tokens\":-1}}\n\n",
		"oversized line":         "data: " + strings.Repeat("a", mimoMaxEventBytes) + "\n\n",
		"oversized multiline":    "data: " + strings.Repeat("a", mimoMaxEventBytes/2) + "\ndata: " + strings.Repeat("a", mimoMaxEventBytes/2) + "\n\n",
	}
	for name, wire := range tests {
		t.Run(name, func(t *testing.T) {
			body := &mimoTrackedBody{Reader: strings.NewReader(wire)}
			stream, _, err := mimoTestProvider(body).Synthesize(context.Background(), domain.Synthesis{Text: "测试"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(stream)
			if !errors.Is(err, domain.ErrUncertain) {
				t.Fatalf("broken stream was not uncertain: %v", err)
			}
			if !body.closed.Load() {
				t.Fatal("failed stream did not close upstream")
			}
		})
	}
}

type mimoErrorReader struct{}

func (mimoErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestMiMoTransportFailureAfterStopIsNotSuccess(t *testing.T) {
	body := &mimoTrackedBody{Reader: io.MultiReader(strings.NewReader(mimoAudioEvent([]byte{1, 2})+mimoStopEvent), mimoErrorReader{})}
	stream, _, err := mimoTestProvider(body).Synthesize(context.Background(), domain.Synthesis{Text: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(stream); !errors.Is(err, domain.ErrUncertain) {
		t.Fatalf("transport failure was accepted: %v", err)
	}
}

func TestMiMoCancellationAndCloseReleaseUpstream(t *testing.T) {
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			disconnected := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(disconnected)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, _, err := NewMiMo(ProviderConfig{BaseURL: server.URL, APIKey: "test-only"}, server.Client()).Synthesize(ctx, domain.Synthesis{Text: "测试"})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if _, err := io.ReadFull(stream, make([]byte, 44)); err != nil {
				t.Fatal(err)
			}
			read := make(chan error, 1)
			go func() { _, err := io.ReadAll(stream); read <- err }()
			if mode == "cancel" {
				cancel()
			} else {
				_ = stream.Close()
			}
			select {
			case err := <-read:
				if !errors.Is(err, domain.ErrUncertain) {
					t.Fatalf("canceled stream succeeded: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("audio reader did not unblock")
			}
			select {
			case <-disconnected:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream request remained open")
			}
		})
	}
}

func TestMiMoCustomAndExplicitEmptyStyles(t *testing.T) {
	for _, style := range []string{"", "沉稳清晰"} {
		t.Run(style, func(t *testing.T) {
			provider := NewMiMo(ProviderConfig{APIKey: "test-only"}, &http.Client{Transport: mimoTestTransport(func(r *http.Request) (*http.Response, error) {
				var payload struct {
					Messages []mimoMessage    `json:"messages"`
					Audio    mimoAudioRequest `json:"audio"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.Audio.Voice != "Milo" || !strings.Contains(payload.Messages[0].Content, "以较慢的日常说话速度朗读") || strings.Contains(payload.Messages[0].Content, MiMoDefaultStyle) {
					t.Errorf("custom delivery settings not applied: %+v", payload)
				}
				if (style == "" && strings.Contains(payload.Messages[0].Content, "朗读风格：")) || (style != "" && !strings.Contains(payload.Messages[0].Content, "朗读风格："+style)) {
					t.Errorf("explicit style changed: %q", payload.Messages[0].Content)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(mimoAudioEvent([]byte{1, 2}) + mimoStopEvent))}, nil
			})})
			stream, _, err := provider.Synthesize(context.Background(), domain.Synthesis{Text: "测试", Voice: "Milo", Speed: "slow", Style: style})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadAll(stream); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMiMoHTTPFailuresCloseResponse(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 413, 422, 429, 503, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := &mimoTrackedBody{Reader: strings.NewReader("upstream detail")}
			provider := NewMiMo(ProviderConfig{APIKey: "test-only"}, &http.Client{Transport: mimoTestTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
			})})
			stream, _, err := provider.Synthesize(context.Background(), domain.Synthesis{Text: "测试"})
			want := domain.ErrRejected
			if status == 500 {
				want = domain.ErrUncertain
			}
			if stream != nil || !errors.Is(err, want) || !body.closed.Load() {
				t.Fatalf("HTTP failure mishandled: stream=%v err=%v closed=%v", stream, err, body.closed.Load())
			}
		})
	}
}

func TestMiMoCapabilitiesDefaultsValidationAndIdentity(t *testing.T) {
	provider := NewMiMo(ProviderConfig{APIKey: "test-only"}, nil)
	cap := provider.TTSCapability()
	if cap.DefaultVoice != "冰糖" || cap.DefaultSpeed != "fast" || cap.DefaultStyle != MiMoDefaultStyle || !cap.Streaming || !cap.SupportsStyle || cap.MaxStyleCharacters != 500 || cap.MaxWeightedCharacters != 1200 || cap.MaxTextBytes != 16<<10 || len(cap.Voices) != 8 || len(cap.Speeds) != 3 || provider.ASRCapability() != nil {
		t.Fatalf("unexpected capability: %+v", cap)
	}
	for _, input := range []domain.Synthesis{
		{Text: ""}, {Text: "测试", Voice: "missing"}, {Text: "测试", Speed: "very-fast"}, {Text: "测试", Format: "pcm"},
		{Text: strings.Repeat("中", 601)}, {Text: "测试", Style: strings.Repeat("中", 501)},
	} {
		if _, _, err := provider.Synthesize(context.Background(), input); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("invalid request accepted: %+v, %v", input, err)
		}
	}
	for _, base := range []string{"https://api.xiaomimimo.com", "https://api.xiaomimimo.com/v1/", "https://api.xiaomimimo.com/v1/chat/completions"} {
		if NewMiMo(ProviderConfig{BaseURL: base, APIKey: "test-only"}, nil).CacheIdentity("tts") != provider.CacheIdentity("tts") {
			t.Fatal("equivalent endpoints have different cache identities")
		}
	}
	if NewMiMo(ProviderConfig{APIKey: "different"}, nil).CacheIdentity("tts") == provider.CacheIdentity("tts") || NewMiMo(ProviderConfig{BaseURL: "https://different.invalid", APIKey: "test-only"}, nil).CacheIdentity("tts") == provider.CacheIdentity("tts") {
		t.Fatal("credential or URL changes did not invalidate identity")
	}
}

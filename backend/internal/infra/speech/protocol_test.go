package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeepSeekHTTPContractsAndStreaming(t *testing.T) {
	seen := make(chan string, 2)
	finish := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing server-side auth")
		}
		switch r.URL.Path {
		case "/v1/tools/asr":
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
			}
			f, _, e := r.FormFile("file")
			if e != nil {
				t.Error(e)
				return
			}
			b, _ := io.ReadAll(f)
			_ = f.Close()
			if string(b) != "WAV" {
				t.Error("wrong audio upload")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"request_id":"upstream-asr-1","text":"result","duration_ms":400}`)
			seen <- "asr"
		case "/v1/tools/tts":
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = io.WriteString(w, "first")
			w.(http.Flusher).Flush()
			<-finish
			_, _ = io.WriteString(w, "last")
			seen <- "tts"
		default:
			t.Error("wrong endpoint", r.URL.Path)
		}
	}))
	defer server.Close()
	p := NewDeepSeek(ProviderConfig{BaseURL: server.URL + "/v1", APIKey: "test-secret"}, server.Client())
	r, e := p.Transcribe(context.Background(), domain.Audio{Bytes: []byte("WAV"), Format: "wav"})
	if e != nil || r.Text != "result" || r.DurationMS != 400 || r.UpstreamRequestID != "upstream-asr-1" {
		t.Fatalf("ASR %+v %v", r, e)
	}
	body, ct, e := p.Synthesize(context.Background(), domain.Synthesis{Text: "hello", Voice: "stella", Format: "wav"})
	if e != nil || ct != "audio/wav" {
		t.Fatal(ct, e)
	}
	buf := make([]byte, 5)
	if _, e = io.ReadFull(body, buf); e != nil || string(buf) != "first" {
		t.Fatalf("not streamed: %q %v", buf, e)
	}
	close(finish)
	b, e := io.ReadAll(body)
	_ = body.Close()
	if e != nil || string(b) != "last" {
		t.Fatal(string(b), e)
	}
	if <-seen != "asr" || <-seen != "tts" {
		t.Fatal("requests absent")
	}
}
func TestFileStoreQuotaAtomicUpdatesAndRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	store, e := NewFileStore(dir, 10, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewFileStore(dir, 10, time.Hour); e == nil {
		t.Fatal("parallel owner accepted")
	}
	ctx := context.Background()
	r := domain.Record{ID: "job", Status: "pending", CreatedAt: time.Now()}
	if e = store.Put(ctx, r); e != nil {
		t.Fatal(e)
	}
	r.Text = "updated"
	if e = store.Put(ctx, r); e != nil {
		t.Fatal("Windows atomic replacement failed", e)
	}
	a, _ := store.CreateAudio(ctx, "a")
	b, _ := store.CreateAudio(ctx, "b")
	if _, e = a.Write([]byte("123456")); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Write([]byte("12345")); !errors.Is(e, domain.ErrLimited) {
		t.Fatalf("concurrent quota bypass %v", e)
	}
	a.Close()
	b.Close()
	if e = store.DeleteAudio(ctx, "a"); e != nil {
		t.Fatal(e)
	}
	c, _ := store.CreateAudio(ctx, "c")
	if _, e = c.Write([]byte("1234567890")); e != nil {
		t.Fatal("quota not released", e)
	}
	c.Close()
	store.Close()
	store, e = NewFileStore(dir, 10, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	recovered, e := store.Get(ctx, "job")
	if e != nil || recovered.Status != "uncertain" || recovered.Text != "updated" {
		t.Fatalf("restart retried pending %+v %v", recovered, e)
	}
}
func TestAudioDurationFromEncodedSamples(t *testing.T) {
	wav := make([]byte, 44+32000)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 32000)
	a, e := InspectAudio(wav)
	if e != nil || a.DurationMS != 1000 {
		t.Fatal(a.DurationMS, e)
	}
	binary.LittleEndian.PutUint32(wav[4:], 0xffffffff)
	binary.LittleEndian.PutUint32(wav[40:], 0xffffffff)
	a, e = InspectAudio(wav)
	if e != nil || a.DurationMS != 1000 {
		t.Fatal("streaming WAV sentinel rejected", a.DurationMS, e)
	}
	binary.LittleEndian.PutUint16(wav[34:], 24)
	if _, e = InspectAudio(wav); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal("accepted PCM24")
	}
}
func TestOggDurationCannotBeForgedByGranule(t *testing.T) {
	page := func(seq uint32, flags byte, granule uint64, packets ...[]byte) []byte {
		header := make([]byte, 27+len(packets))
		copy(header, "OggS")
		header[5] = flags
		binary.LittleEndian.PutUint64(header[6:], granule)
		binary.LittleEndian.PutUint32(header[14:], 1)
		binary.LittleEndian.PutUint32(header[18:], seq)
		header[26] = byte(len(packets))
		for i, p := range packets {
			header[27+i] = byte(len(p))
			header = append(header, p...)
		}
		return header
	}
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = 1
	audio := append(page(0, 2, 0, head), page(1, 0, 0, []byte("OpusTags"))...)
	audio = append(audio, page(2, 4, 1920, []byte{0x98, 1}, []byte{0x98, 1})...)
	a, e := InspectAudio(audio)
	if e != nil || a.DurationMS != 40 {
		t.Fatal(a.DurationMS, e)
	}
	offset := len(page(0, 2, 0, head)) + len(page(1, 0, 0, []byte("OpusTags")))
	binary.LittleEndian.PutUint64(audio[offset+6:], 480)
	if _, e = InspectAudio(audio); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal("forged granule accepted", e)
	}
}
func TestDoubaoStreamsAndOnlySessionFinishedCommits(t *testing.T) {
	store, e := NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	var registrations atomic.Int32
	register := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registrations.Add(1)
		_, _ = io.WriteString(w, `{"device_id_str":"device-1"}`)
	}))
	defer register.Close()
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		audioFrames := 0
		sawLast := false
		for {
			raw, e := recvIME(ws, time.Second)
			if e != nil {
				return
			}
			f, e := parseProto(raw)
			if e != nil {
				t.Error(e)
				return
			}
			method := string(data(f, 5))
			event := ""
			payload := ""
			switch method {
			case "StartTask":
				event = "TaskStarted"
			case "StartSession":
				var config struct {
					Extra struct {
						Twopass   *bool `json:"enable_asr_twopass"`
						Threepass *bool `json:"enable_asr_threepass"`
					} `json:"extra"`
				}
				if e := json.Unmarshal(data(f, 6), &config); e != nil || config.Extra.Twopass == nil || !*config.Extra.Twopass || config.Extra.Threepass == nil || *config.Extra.Threepass {
					t.Error("native streaming branch must remain enabled without threepass rewriting")
					return
				}
				event = "SessionStarted"
			case "TaskRequest":
				if len(data(f, 7)) > 0 {
					expected := uint64(3)
					if audioFrames == 0 {
						expected = 1
					}
					if integer(f, 9) != expected || sawLast {
						t.Error("audio frame flags/order invalid")
						return
					}
					audioFrames++
					payload = `{"results":[{"index":0,"text":"hello","is_interim":false,"is_vad_finished":true},{"index":1,"text":" world","is_interim":true}]}`
				} else if integer(f, 9) == 9 {
					sawLast = true
				}
			case "FinishSession":
				if !sawLast || audioFrames != 3 {
					t.Error("finished session before all audio and LAST frame")
					return
				}
				event = "SessionFinished"
				payload = `{"results":[{"text":"hello world!","stream_asr_finish":true}]}`
			}
			if event != "" || payload != "" {
				response := proto{}.s(4, event).v(5, 20000000)
				if payload != "" {
					response = response.s(7, payload)
				}
				if e = sendIME(ws, response); e != nil {
					return
				}
			}
			if event == "SessionFinished" {
				return
			}
		}
	}))
	defer server.Close()
	p := NewDoubao(IMEConfig{WSURL: "ws" + strings.TrimPrefix(server.URL, "http"), RegisterURL: register.URL, HTTPClient: register.Client(), Identities: store})
	frames := make(chan []byte, 1)
	frames <- []byte{0x98, 1}
	partial := make(chan domain.Event, 8)
	done := make(chan error, 1)
	go func() {
		r, e := p.Stream(context.Background(), domain.StreamRequest{UserID: 1, SessionID: "session"}, frames, func(e domain.Event) error { partial <- e; return nil })
		if e == nil && r.Text != "hello world!" {
			e = errors.New("final revision missing")
		}
		done <- e
	}()
	select {
	case e := <-partial:
		if e.Text != "hello world" {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("partial waited for recording end")
	}
	frames <- []byte{0x98, 2}
	frames <- []byte{0x98, 3}
	close(frames)
	select {
	case <-partial:
	case <-time.After(2 * time.Second):
		t.Fatal("final snapshot not emitted")
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if _, e = p.identity(context.Background(), 1); e != nil {
		t.Fatal(e)
	}
	if registrations.Load() != 1 {
		t.Fatal("identity not stable")
	}
	if _, e = p.identity(context.Background(), 2); e != nil {
		t.Fatal(e)
	}
	if registrations.Load() != 2 {
		t.Fatal("cross-user identity reuse")
	}
}
func TestWeTypeEncryptedStreamingProtocol(t *testing.T) {
	store, e := NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	var batches atomic.Int32
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		var key []byte
		polishedOnlyFinal := strings.Contains(ws.Request().URL.RawQuery, "polished_only")
		for {
			raw, e := recvIME(ws, 2*time.Second)
			if e != nil {
				return
			}
			top, _ := parseProto(raw)
			request, _ := parseProto(data(top, 5))
			path := string(data(request, 3))
			body := data(request, 6)
			var responseBody []byte
			compression := "1"
			switch path {
			case "/timestamp":
			case "/oauth_pubkey_v2":
				plain, e := aesDecrypt([]byte(wetypeBootKey), body)
				if e != nil {
					t.Error(e)
					return
				}
				fields, _ := parseProto(plain)
				private, public, e := weTypeKeypair()
				if e != nil {
					t.Error(e)
					return
				}
				key, e = weTypeShared(private, string(data(fields, 1)))
				if e != nil {
					t.Error(e)
					return
				}
				responseBody, _ = aesEncrypt([]byte(wetypeBootKey), proto{}.s(2, public).s(4, "issued-token"))
			case "/gen_uin_v2":
				responseBody, _ = aesEncrypt(key, proto{}.v(2, 1234))
			case "/api_v2":
				plain, e := aesDecrypt(key, body)
				if e != nil {
					t.Error(e)
					return
				}
				if len(plain) > 0 {
					plain, e = snappyDecode(plain)
					if e != nil {
						t.Error(e)
						return
					}
					outer, _ := parseProto(plain)
					inner, _ := parseProto(data(outer, 1))
					audio := data(inner, 4)
					if len(audio) > 0 {
						batches.Add(1)
						if batches.Load() == 1 && !bytes.HasPrefix(audio, []byte("#!OPUS_RAW_V1")) {
							t.Error("missing raw Opus header")
						}
					}
					result := proto{}.s(4, "ordinary text")
					if integer(inner, 6) == 1 {
						if polishedOnlyFinal {
							result = proto{}
						}
						result = result.s(14, "polished text")
					}
					responseBody, _ = aesEncrypt(key, snappyEncode(proto{}.b(1, result)))
					compression = "2"
				} else {
					responseBody, _ = aesEncrypt(key, nil)
				}
			}
			reply := proto{}.v(2, 200).b(3, proto{}.s(1, "Kb-CompressionType").s(2, compression)).b(5, responseBody)
			if e = sendIME(ws, proto{}.b(4, reply)); e != nil {
				return
			}
		}
	}))
	defer server.Close()
	p := NewWeType(IMEConfig{WSURL: "ws" + strings.TrimPrefix(server.URL, "http"), Identities: store})
	frames := make(chan []byte, 6)
	for i := 0; i < 6; i++ {
		frames <- []byte{0x98, 1}
	}
	partial := make(chan domain.Event, 2)
	done := make(chan domain.Transcript, 1)
	errs := make(chan error, 1)
	go func() {
		r, e := p.Stream(context.Background(), domain.StreamRequest{UserID: 1, SessionID: "voice"}, frames, func(e domain.Event) error { partial <- e; return nil })
		done <- r
		errs <- e
	}()
	select {
	case e := <-partial:
		if e.Text != "ordinary text" {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WeType not streaming")
	}
	close(frames)
	r := <-done
	if e = <-errs; e != nil || r.Text != "ordinary text" || batches.Load() != 1 {
		t.Fatalf("WeType final %+v %v batches=%d", r, e, batches.Load())
	}
	plainOnly := NewWeType(IMEConfig{WSURL: "ws" + strings.TrimPrefix(server.URL, "http") + "?polished_only", Identities: store})
	otherFrames := make(chan []byte, 6)
	for i := 0; i < 6; i++ {
		otherFrames <- []byte{0x98, 1}
	}
	close(otherFrames)
	var partials []string
	outcome, e := plainOnly.Stream(t.Context(), domain.StreamRequest{UserID: 1, SessionID: "plain-policy"}, otherFrames, func(e domain.Event) error { partials = append(partials, e.Text); return nil })
	if !errors.Is(e, domain.ErrUncertain) || outcome.Final {
		t.Fatal("polished-only final was presented as literal ASR", outcome, e)
	}
	if len(partials) == 0 {
		t.Fatal("ordinary partial was not retained")
	}
	for _, text := range partials {
		if text != "ordinary text" {
			t.Fatal("polished rewrite leaked into ordinary partial", text)
		}
	}
}
func TestDoubaoCandidateSnapshotsAndIndexedCorrections(t *testing.T) {
	transcript := doubaoTranscript{}
	for _, native := range []string{"这", "这是语音输入测试，请保留前面的文字", "这是语音输入测试，请保留前面的文字，再追加这一句话"} {
		payload, _ := json.Marshal(map[string]any{"results": []map[string]any{{"text": native, "is_interim": true, "start_time": 0.062, "end_time": 5.202024}, {"text": native, "is_interim": true, "stream_asr_finish": false, "start_time": 0.062, "end_time": 5.202024}}})
		partial, e := transcript.update(payload)
		if e != nil || partial != native {
			t.Fatal("native partial did not progress during audio input", partial, e)
		}
		if _, e = transcript.final(); !errors.Is(e, domain.ErrUncertain) {
			t.Fatal("provisional ASR was already considered final", e)
		}
	}
	actual := []byte(`{"results":[{"text":"这是语音输入测试，请保留前面的文字，再追加这一句话。","start_time":0.062,"end_time":5.562,"is_interim":true,"stream_asr_finish":true},{"text":"这是语音输入测试请保留前面的文字再追加这一句话","start_time":0.062,"end_time":5.562,"is_interim":true,"stream_asr_finish":true,"extra":{"seq_id":4}}]}`)
	text, e := transcript.update(actual)
	if e != nil || text != "这是语音输入测试，请保留前面的文字，再追加这一句话。" {
		t.Fatal("cumulative alternatives were appended or truncated", text, e)
	}
	for _, raw := range []string{`{"results":[{"text":"这是语音输入测试。","stream_asr_finish":true,"extra":{"nonstream_result":true}},{"text":"这是语音输入测试。","extra":{"nonstream_result":true}}]}`, `{"results":[{"text":"这是语音输入测试。","is_interim":false,"is_vad_finished":true},{"text":"这是语音输入测试。","stream_asr_finish":true,"extra":{"nonstream_result":true}}]}`, `{"results":null,"extra":{"heartbeat_num":41}}`, `{"results":[{"text":"","is_interim":true}]}`} {
		text, e = transcript.update([]byte(raw))
		if e != nil || text != "这是语音输入测试，请保留前面的文字，再追加这一句话。" {
			t.Fatal("polish/heartbeat overwrote literal ASR", text, e)
		}
	}
	if final, e := transcript.final(); e != nil || final != "这是语音输入测试，请保留前面的文字，再追加这一句话。" {
		t.Fatal("native ASR final lost", final, e)
	}
	_, _ = transcript.update([]byte(`{"results":[{"text":"next unfinished utterance","is_interim":true}]}`))
	if _, e = transcript.final(); !errors.Is(e, domain.ErrUncertain) {
		t.Fatal("a new provisional tail reused an earlier native final", e)
	}
	transcript = doubaoTranscript{}
	text, e = transcript.update([]byte(`{"results":[{"text":"Hello","index":0,"is_interim":false,"is_vad_finished":true},{"text":"world","index":1,"is_interim":true}]}`))
	if e != nil || text != "Hello world" {
		t.Fatal(text, e)
	}
	text, e = transcript.update([]byte(`{"results":[{"text":"Hi","index":0,"is_interim":false,"is_vad_finished":true}]}`))
	if e != nil || text != "Hi world" {
		t.Fatal("old sentence correction lost active tail", text, e)
	}
	text, e = transcript.update([]byte(`{"results":[{"text":"world!","index":1,"is_interim":false,"is_vad_finished":true}]}`))
	if e != nil || text != "Hi world!" {
		t.Fatal(text, e)
	}
}
func TestIMEHandshakeCanBeCancelled(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	ws, closeWS, e := dialIME(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), http.Header{}, "")
	if e == nil {
		closeWS()
		ws.Close()
		t.Fatal("stalled handshake succeeded")
	}
	if ctx.Err() == nil || time.Since(start) > time.Second {
		t.Fatal("handshake ignored cancellation", e)
	}
}

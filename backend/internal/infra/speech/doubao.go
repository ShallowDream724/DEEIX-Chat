package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Doubao struct {
	cfg IMEConfig
	mu  sync.Mutex
}

func NewDoubao(cfg IMEConfig) *Doubao {
	if cfg.WSURL == "" {
		cfg.WSURL = "wss://frontier-audio-ime-ws.doubao.com/ocean/api/v1/ws"
	}
	if cfg.RegisterURL == "" {
		cfg.RegisterURL = "https://log-klink.zijieapi.com/service/2/device_register/"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Doubao{cfg: cfg}
}
func (d *Doubao) ASRCapability() *domain.Capability {
	return &domain.Capability{ID: "doubao-ime", Name: "豆包输入法", Streaming: true, Formats: []string{"opus"}, SampleRate: 16000}
}
func (d *Doubao) TTSCapability() *domain.Capability { return nil }
func (d *Doubao) Transcribe(context.Context, domain.Audio) (domain.Transcript, error) {
	return domain.Transcript{}, domain.ErrUnavailable
}
func (d *Doubao) Synthesize(context.Context, domain.Synthesis) (io.ReadCloser, string, error) {
	return nil, "", domain.ErrUnavailable
}

const doubaoUA = "com.bytedance.android.doubaoime/100406010 (Linux; U; Android 15; en_US; Pixel 8; Build/AP4A.250205.002; Cronet/TTNetVersion:94cf429a 2025-11-17 QuicVersion:1f89f732 2025-05-08)"

func (d *Doubao) identity(ctx context.Context, user uint) (string, error) {
	if locks, ok := d.cfg.Identities.(interface{ LockIdentity(uint, string) func() }); ok {
		unlock := locks.LockIdentity(user, "doubao")
		defer unlock()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfg.Identities == nil {
		return "", domain.ErrUnavailable
	}
	b, e := d.cfg.Identities.LoadIdentity(ctx, user, "doubao")
	if e == nil {
		var v struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(b, &v) == nil && v.ID != "" {
			return v.ID, nil
		}
		return "", domain.ErrInvalid
	}
	if !os.IsNotExist(e) {
		return "", e
	}
	q := url.Values{"aid": {"401734"}, "app_name": {"oime"}, "channel": {"official"}, "version_code": {"100406010"}, "version_name": {"1.4.6"}, "manifest_version_code": {"100406010"}, "update_version_code": {"100406010"}, "package": {"com.bytedance.android.doubaoime"}, "device_platform": {"android"}, "os": {"android"}, "os_api": {"35"}, "os_version": {"15"}, "device_type": {"Pixel 8"}, "device_brand": {"google"}, "device_model": {"Pixel 8"}, "resolution": {"1080*2400"}, "dpi": {"420"}, "language": {"zh"}, "timezone": {"8"}, "access": {"wifi"}, "rom": {"AP4A.250205.002"}, "rom_version": {"AP4A.250205.002"}}
	header := map[string]any{}
	for k, v := range q {
		header[k] = v[0]
	}
	cdid := uuid.NewString()
	for k, v := range map[string]any{"device_id": 0, "install_id": 0, "cdid": cdid, "openudid": strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "clientudid": uuid.NewString(), "region": "CN", "tz_name": "Asia/Shanghai", "tz_offset": 28800, "sim_region": "cn", "carrier_region": "cn", "cpu_abi": "arm64-v8a", "build_serial": "unknown", "not_request_sender": 0, "sig_hash": "", "google_aid": "", "mc": "", "serial_number": ""} {
		header[k] = v
	}
	body, _ := json.Marshal(map[string]any{"magic_tag": "ss_app_log", "header": header, "_gen_time": time.Now().UnixMilli()})
	q.Set("ssmix", "a")
	q.Set("_rticket", strconv.FormatInt(time.Now().UnixMilli(), 10))
	q.Set("cdid", cdid)
	q.Set("ac", "wifi")
	req, e := http.NewRequestWithContext(ctx, "POST", d.cfg.RegisterURL+"?"+q.Encode(), bytes.NewReader(body))
	if e != nil {
		return "", e
	}
	req.Header.Set("User-Agent", doubaoUA)
	req.Header.Set("Content-Type", "application/json")
	res, e := d.cfg.HTTPClient.Do(req)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", domain.ErrUnavailable
	}
	var v struct {
		ID     string      `json:"device_id_str"`
		Number json.Number `json:"device_id"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&v); e != nil {
		return "", e
	}
	if v.ID == "" {
		v.ID = string(v.Number)
	}
	if v.ID == "" || v.ID == "0" {
		return "", domain.ErrForbidden
	}
	saved, _ := json.Marshal(map[string]string{"id": v.ID})
	if e = d.cfg.Identities.SaveIdentity(ctx, user, "doubao", saved); e != nil {
		return "", e
	}
	return v.ID, nil
}
func doubaoRequest(method, token, payload, id string, audio []byte, kind uint64) []byte {
	p := proto{}.s(3, "ASR").s(5, method).s(8, id)
	if token != "" {
		p = p.s(2, token)
	}
	if payload != "" {
		p = p.s(6, payload)
	}
	if len(audio) > 0 {
		p = p.b(7, audio)
	}
	if kind > 0 {
		p = p.v(9, kind)
	}
	return p
}

type doubaoTranscript struct {
	sentences    map[int]string
	current      string
	currentIndex *int
}

type doubaoResult struct {
	Text           string `json:"text"`
	Index          *int   `json:"index"`
	Interim        *bool  `json:"is_interim"`
	VAD            bool   `json:"is_vad_finished"`
	StreamFinished bool   `json:"stream_asr_finish"`
	Extra          struct {
		Final bool `json:"nonstream_result"`
	} `json:"extra"`
}

func (r doubaoResult) finished() bool {
	return r.Extra.Final || r.StreamFinished || (r.Interim != nil && !*r.Interim && r.VAD)
}

func (t *doubaoTranscript) update(raw []byte) (string, error) {
	var v struct {
		Results []doubaoResult `json:"results"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	indexed := false
	for _, r := range v.Results {
		if r.Index != nil {
			indexed = true
		}
	}
	if !indexed {
		// In this protocol variant results[0] is the complete cumulative
		// snapshot. Later candidates can be old VAD sentences or new tails;
		// never let their nonstream flags suppress the aggregate or substitute
		// an auxiliary sentence when the aggregate is empty.
		if len(v.Results) > 0 && v.Results[0].Text != "" {
			t.sentences = map[int]string{}
			t.current = v.Results[0].Text
			t.currentIndex = nil
		}
		return t.text(), nil
	}
	if t.sentences == nil {
		t.sentences = map[int]string{}
	}
	for _, r := range v.Results {
		if r.Text == "" || r.Index == nil {
			continue
		}
		if r.finished() {
			idx := len(t.sentences)
			if r.Index != nil {
				idx = *r.Index
			}
			t.sentences[idx] = r.Text
			if t.currentIndex == nil || *t.currentIndex == idx {
				t.current = ""
				t.currentIndex = nil
			}
		} else {
			t.current = r.Text
			t.currentIndex = r.Index
		}
	}
	return t.text(), nil
}
func (t *doubaoTranscript) final() (string, error) {
	// Only SessionFinished invokes this method; utterance finish markers do
	// not end the recording or prevent later cumulative snapshots.
	return t.text(), nil
}
func (t *doubaoTranscript) text() string {
	keys := make([]int, 0, len(t.sentences))
	for k := range t.sentences {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		parts = append(parts, t.sentences[k])
	}
	parts = append(parts, t.current)
	return joinTranscript(parts)
}
func (d *Doubao) Stream(ctx context.Context, in domain.StreamRequest, frames <-chan []byte, emit func(domain.Event) error) (domain.Transcript, error) {
	did, e := d.identity(ctx, in.UserID)
	if e != nil {
		return domain.Transcript{}, e
	}
	u, e := url.Parse(d.cfg.WSURL)
	if e != nil {
		return domain.Transcript{}, e
	}
	q := u.Query()
	q.Set("aid", "401734")
	q.Set("device_id", did)
	u.RawQuery = q.Encode()
	ws, closeWS, e := dialIME(ctx, u.String(), http.Header{"User-Agent": {doubaoUA}, "Proto-Version": {"v2"}, "X-Custom-Keepalive": {"true"}}, "")
	if e != nil {
		return domain.Transcript{}, e
	}
	defer closeWS()
	id := in.SessionID
	expect := func(event string) error {
		for i := 0; i < 16; i++ {
			b, e := recvIME(ws, 10*time.Second)
			if e != nil {
				return e
			}
			f, e := parseProto(b)
			if e != nil {
				return e
			}
			name := string(data(f, 4))
			if strings.HasSuffix(name, "Failed") {
				return domain.ErrForbidden
			}
			if name == event {
				if integer(f, 5) != 20000000 {
					return domain.ErrUnavailable
				}
				return nil
			}
		}
		return domain.ErrInvalid
	}
	if e = sendIME(ws, doubaoRequest("StartTask", "OrnqKvSSrs", "", id, nil, 0)); e != nil {
		return domain.Transcript{}, e
	}
	if e = expect("TaskStarted"); e != nil {
		return domain.Transcript{}, e
	}
	// Keep the verified upstream streaming parameters. Its cumulative result
	// includes built-in recognition corrections; no additional rewrite is run.
	payload, _ := json.Marshal(map[string]any{"audio_info": map[string]any{"channel": 1, "format": "speech_opus", "sample_rate": 16000}, "enable_punctuation": true, "enable_speech_rejection": true, "extra": map[string]any{"app_name": "oime", "cell_compress_rate": 8, "did": did, "enable_asr_threepass": false, "enable_asr_twopass": true, "input_mode": "stream"}})
	if e = sendIME(ws, doubaoRequest("StartSession", "OrnqKvSSrs", string(payload), id, nil, 0)); e != nil {
		return domain.Transcript{}, e
	}
	if e = expect("SessionStarted"); e != nil {
		return domain.Transcript{}, e
	}
	type outcome struct {
		result domain.Transcript
		err    error
	}
	result := make(chan outcome, 1)
	receiverDone := make(chan struct{})
	defer func() { closeWS(); <-receiverDone }()
	go func() {
		defer close(receiverDone)
		t := doubaoTranscript{}
		for {
			b, e := recvIME(ws, 10*time.Minute)
			if e != nil {
				result <- outcome{err: e}
				return
			}
			f, e := parseProto(b)
			if e != nil {
				result <- outcome{err: e}
				return
			}
			if raw := data(f, 7); len(raw) > 0 {
				if d.cfg.ResultObserver != nil {
					d.cfg.ResultObserver(append([]byte(nil), raw...))
				}
				text, e := t.update(raw)
				if e != nil {
					result <- outcome{err: e}
					return
				}
				if e = emit(domain.Event{Type: "transcript", Text: text}); e != nil {
					result <- outcome{err: e}
					return
				}
			}
			event := string(data(f, 4))
			if strings.HasSuffix(event, "Failed") {
				result <- outcome{err: domain.ErrForbidden}
				return
			}
			if event == "SessionFinished" {
				if integer(f, 5) != 20000000 {
					result <- outcome{err: domain.ErrUnavailable}
					return
				}
				text, err := t.final()
				result <- outcome{result: domain.Transcript{Text: text, Final: true}, err: err}
				return
			}
		}
	}()
	var index uint64
	started := time.Now().UnixMilli()
	for {
		select {
		case <-ctx.Done():
			return domain.Transcript{}, ctx.Err()
		case out := <-result:
			return out.result, out.err
		case f, ok := <-frames:
			if !ok {
				meta := fmt.Sprintf(`{"extra":{},"timestamp_ms":%d}`, started+int64(index)*20)
				if e = sendIME(ws, doubaoRequest("TaskRequest", "", meta, id, nil, 9)); e != nil {
					return domain.Transcript{}, e
				}
				if e = sendIME(ws, doubaoRequest("FinishSession", "OrnqKvSSrs", "", id, nil, 0)); e != nil {
					return domain.Transcript{}, e
				}
				select {
				case out := <-result:
					return out.result, out.err
				case <-ctx.Done():
					return domain.Transcript{}, ctx.Err()
				case <-time.After(15 * time.Second):
					return domain.Transcript{}, domain.ErrUncertain
				}
			}
			kind := uint64(3)
			if index == 0 {
				kind = 1
			}
			meta := fmt.Sprintf(`{"extra":{},"timestamp_ms":%d}`, started+int64(index)*20)
			if e = sendIME(ws, doubaoRequest("TaskRequest", "", meta, id, f, kind)); e != nil {
				return domain.Transcript{}, e
			}
			index++
		}
	}
}

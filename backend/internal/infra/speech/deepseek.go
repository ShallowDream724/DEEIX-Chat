package speech

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

type ProviderConfig struct {
	BaseURL string
	APIKey  string
}
type DeepSeek struct {
	cfg    ProviderConfig
	client *http.Client
}

func NewDeepSeek(cfg ProviderConfig, client *http.Client) *DeepSeek {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com"
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &DeepSeek{cfg: cfg, client: client}
}
func (d *DeepSeek) CacheIdentity(operation string) string {
	base := strings.TrimSuffix(strings.TrimRight(d.cfg.BaseURL, "/"), "/v1")
	encoded, _ := json.Marshal([]string{operation, base, d.cfg.APIKey})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}
func (d *DeepSeek) ASRCapability() *domain.Capability {
	return &domain.Capability{ID: "deepseek", Name: "DeepSeek", Formats: []string{"wav", "ogg"}, MaxDurationMS: 120000, MaxBytes: 8 << 20}
}
func (d *DeepSeek) TTSCapability() *domain.Capability {
	return &domain.Capability{ID: "deepseek", Name: "DeepSeek", Formats: []string{"wav", "pcm"}, Voices: []domain.Voice{{ID: "stella", Name: "海星"}, {ID: "tide", Name: "暗潮"}}, MaxWeightedCharacters: 10000, MaxTextBytes: 64 << 10}
}
func (d *DeepSeek) request(ctx context.Context, path, ct string, body io.Reader, length int64) (*http.Response, error) {
	if d.cfg.APIKey == "" {
		return nil, domain.ErrUnavailable
	}
	base := strings.TrimRight(d.cfg.BaseURL, "/")
	base = strings.TrimSuffix(base, "/v1")
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/tools/"+path, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+d.cfg.APIKey)
	req.Header.Set("Content-Type", ct)
	req.ContentLength = length
	res, e := d.client.Do(req)
	if e != nil {
		return nil, domain.ErrUncertain
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		switch res.StatusCode {
		case 400, 401, 403, 404, 413, 429, 503:
			return nil, fmt.Errorf("%w: HTTP %d", domain.ErrRejected, res.StatusCode)
		}
		return nil, fmt.Errorf("speech upstream HTTP %d", res.StatusCode)
	}
	return res, nil
}
func (d *DeepSeek) Transcribe(ctx context.Context, a domain.Audio) (domain.Transcript, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_, e := w.CreateFormFile("file", "speech."+a.Format)
	if e != nil {
		return domain.Transcript{}, e
	}
	headerLength := b.Len()
	_ = w.Close()
	// Keep only the small multipart envelope; stream the existing audio bytes.
	envelope := b.Bytes()
	body := io.MultiReader(bytes.NewReader(envelope[:headerLength]), bytes.NewReader(a.Bytes), bytes.NewReader(envelope[headerLength:]))
	res, e := d.request(ctx, "asr", w.FormDataContentType(), body, int64(len(envelope))+int64(len(a.Bytes)))
	if e != nil {
		return domain.Transcript{}, e
	}
	defer res.Body.Close()
	var r struct {
		RequestID  string `json:"request_id"`
		Text       string `json:"text"`
		DurationMS int64  `json:"duration_ms"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&r); e != nil {
		return domain.Transcript{}, domain.ErrUncertain
	}
	if r.DurationMS <= 0 || r.DurationMS > 120000 {
		return domain.Transcript{}, domain.ErrUncertain
	}
	return domain.Transcript{Text: r.Text, DurationMS: r.DurationMS, Final: true, UpstreamRequestID: r.RequestID}, nil
}
func (d *DeepSeek) Stream(context.Context, domain.StreamRequest, <-chan []byte, func(domain.Event) error) (domain.Transcript, error) {
	return domain.Transcript{}, domain.ErrUnavailable
}
func (d *DeepSeek) Synthesize(ctx context.Context, in domain.Synthesis) (io.ReadCloser, string, error) {
	b, e := json.Marshal(struct {
		Text           string `json:"text"`
		Voice          string `json:"voice_id"`
		Format         string `json:"format"`
		FilterMarkdown bool   `json:"filter_markdown"`
	}{in.Text, in.Voice, in.Format, true})
	if e != nil {
		return nil, "", e
	}
	res, e := d.request(ctx, "tts", "application/json", bytes.NewReader(b), int64(len(b)))
	if e != nil {
		return nil, "", e
	}
	ct := "audio/wav"
	if in.Format == "pcm" {
		ct = "audio/L16;rate=24000;channels=1"
	}
	return res.Body, ct, nil
}

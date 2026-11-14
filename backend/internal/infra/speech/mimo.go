package speech

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
)

const (
	MiMoDefaultVoice  = "冰糖"
	MiMoDefaultSpeed  = "fast"
	MiMoDefaultStyle  = "自然、明亮、轻盈地朗读，亲切有活力。专业术语吐字清楚，保持自然流畅。忠实朗读原文，不增删内容。中文用普通话，英文按英文发音。"
	mimoModel         = "mimo-v2.5-tts"
	mimoPromptVersion = "faithful-reading-v1"
	mimoMaxEventBytes = 1 << 20
)

// MiMo converts the provider's PCM audio SSE events to a bounded, pull-based WAV
// stream. Reading stops the upstream at the same pace as the audio consumer.
type MiMo struct {
	cfg    ProviderConfig
	client *http.Client
}

func NewMiMo(cfg ProviderConfig, client *http.Client) *MiMo {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.xiaomimimo.com"
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &MiMo{cfg: cfg, client: client}
}

func (m *MiMo) endpoint() string {
	base := strings.TrimRight(m.cfg.BaseURL, "/")
	base = strings.TrimSuffix(base, "/v1/chat/completions")
	base = strings.TrimSuffix(base, "/v1")
	return base + "/v1/chat/completions"
}

func (m *MiMo) CacheIdentity(operation string) string {
	encoded, _ := json.Marshal([]string{operation, m.endpoint(), m.cfg.APIKey, mimoModel, mimoPromptVersion})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func (*MiMo) ASRCapability() *domain.Capability { return nil }

func (*MiMo) TTSCapability() *domain.Capability {
	return &domain.Capability{
		ID: "mimo", Name: "MiMo", Streaming: true, Formats: []string{"wav"}, SampleRate: 24000,
		Voices:       []domain.Voice{{ID: "冰糖", Name: "冰糖"}, {ID: "茉莉", Name: "茉莉"}, {ID: "苏打", Name: "苏打"}, {ID: "白桦", Name: "白桦"}, {ID: "Mia", Name: "Mia"}, {ID: "Chloe", Name: "Chloe"}, {ID: "Milo", Name: "Milo"}, {ID: "Dean", Name: "Dean"}},
		DefaultVoice: MiMoDefaultVoice,
		Speeds:       []domain.Voice{{ID: "slow", Name: "较慢"}, {ID: "normal", Name: "标准"}, {ID: "fast", Name: "较快"}},
		DefaultSpeed: MiMoDefaultSpeed, SupportsStyle: true, DefaultStyle: MiMoDefaultStyle,
		MaxWeightedCharacters: 1200, MaxTextBytes: 16 << 10, MaxStyleCharacters: 500,
	}
}

func (*MiMo) Transcribe(context.Context, domain.Audio) (domain.Transcript, error) {
	return domain.Transcript{}, domain.ErrUnavailable
}

func (*MiMo) Stream(context.Context, domain.StreamRequest, <-chan []byte, func(domain.Event) error) (domain.Transcript, error) {
	return domain.Transcript{}, domain.ErrUnavailable
}

func (m *MiMo) Synthesize(ctx context.Context, in domain.Synthesis) (io.ReadCloser, string, error) {
	if m.cfg.APIKey == "" {
		return nil, "", domain.ErrUnavailable
	}
	cap := m.TTSCapability()
	if in.Voice == "" {
		in.Voice = cap.DefaultVoice
	}
	if in.Speed == "" {
		in.Speed = cap.DefaultSpeed
	}
	var weighted int64
	for _, r := range in.Text {
		weighted++
		if unicode.Is(unicode.Han, r) {
			weighted++
		}
	}
	if strings.TrimSpace(in.Text) == "" || weighted > cap.MaxWeightedCharacters || int64(len(in.Text)) > cap.MaxTextBytes || int64(utf8.RuneCountInString(in.Style)) > cap.MaxStyleCharacters || !utf8.ValidString(in.Text) || !utf8.ValidString(in.Style) || (in.Format != "" && in.Format != "wav") {
		return nil, "", domain.ErrInvalid
	}
	validVoice := false
	for _, voice := range cap.Voices {
		validVoice = validVoice || in.Voice == voice.ID
	}
	pace, validSpeed := map[string]string{
		"slow":   "以较慢的日常说话速度朗读，吐字清晰，停顿自然，不刻意拖长字音。",
		"normal": "以标准的日常说话速度朗读，节奏自然，句间停顿适中。",
		"fast":   "以较快的日常说话速度朗读，节奏紧凑，句间仅保留必要的短停顿，避免拖长字音。",
	}[in.Speed]
	if !validVoice || !validSpeed {
		return nil, "", domain.ErrInvalid
	}
	message := "请严格按后续 assistant 消息中的原文朗读，不补充、删改、总结或回答正文内容。\n" + pace
	// The service resolves an omitted style to the capability default. An empty
	// style reaching the provider is intentional and must remain empty.
	if in.Style != "" {
		message += "\n朗读风格：" + in.Style
	}
	body, err := json.Marshal(struct {
		Model    string           `json:"model"`
		Messages []mimoMessage    `json:"messages"`
		Audio    mimoAudioRequest `json:"audio"`
		Stream   bool             `json:"stream"`
	}{mimoModel, []mimoMessage{{"user", message}, {"assistant", in.Text}}, mimoAudioRequest{in.Voice, "pcm16"}, true})
	if err != nil {
		return nil, "", err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, m.endpoint(), bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, "", err
	}
	req.Header.Set("api-key", m.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	res, err := m.client.Do(req)
	if err != nil {
		cancel()
		return nil, "", fmt.Errorf("%w: MiMo request failed", domain.ErrUncertain)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		cancel()
		switch res.StatusCode {
		case 400, 401, 403, 404, 413, 422, 429, 503:
			return nil, "", fmt.Errorf("%w: HTTP %d", domain.ErrRejected, res.StatusCode)
		default:
			return nil, "", fmt.Errorf("%w: MiMo HTTP %d", domain.ErrUncertain, res.StatusCode)
		}
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), mimoMaxEventBytes)
	scanner.Split(mimoSSELines)
	return &mimoAudioStream{body: res.Body, cancel: cancel, scanner: scanner, pending: mimoWAVHeader()}, "audio/wav", nil
}

type mimoMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type mimoAudioRequest struct {
	Voice  string `json:"voice"`
	Format string `json:"format"`
}

type mimoChunk struct {
	ID      string             `json:"id"`
	Error   json.RawMessage    `json:"error"`
	Usage   *domain.TokenUsage `json:"usage"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Audio *struct {
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

type mimoAudioStream struct {
	body       io.ReadCloser
	cancel     context.CancelFunc
	scanner    *bufio.Scanner
	pending    []byte
	event      []byte
	eventType  string
	pcmBytes   int64
	stopped    bool
	err        error
	closeOnce  sync.Once
	closeErr   error
	metadataMu sync.Mutex
	metadata   domain.SynthesisMetadata
}

func (s *mimoAudioStream) Metadata() domain.SynthesisMetadata {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	metadata := s.metadata
	if metadata.Usage != nil {
		usage := *metadata.Usage
		metadata.Usage = &usage
	}
	return metadata
}

func (s *mimoAudioStream) Close() error {
	s.closeOnce.Do(func() { s.cancel(); s.closeErr = s.body.Close() })
	return s.closeErr
}

func (s *mimoAudioStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(s.pending) == 0 && s.err == nil {
		if err := s.nextAudio(); err != nil {
			s.err = err
			_ = s.Close()
		}
	}
	if len(s.pending) > 0 {
		n := copy(p, s.pending)
		s.pending = s.pending[n:]
		return n, nil
	}
	return 0, s.err
}

func (s *mimoAudioStream) nextAudio() error {
	for s.scanner.Scan() {
		line := s.scanner.Bytes()
		if len(line) == 0 {
			eventType := s.eventType
			s.eventType = ""
			if len(s.event) == 0 {
				continue
			}
			event := s.event
			s.event = nil
			if eventType == "error" {
				return mimoStreamError("upstream error event")
			}
			if bytes.Equal(bytes.TrimSpace(event), []byte("[DONE]")) {
				return s.complete()
			}
			if err := s.decode(event); err != nil {
				return err
			}
			if len(s.pending) > 0 {
				return nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		if bytes.Equal(field, []byte("event")) {
			s.eventType = string(value)
			continue
		}
		if !bytes.Equal(field, []byte("data")) {
			continue
		}
		if len(s.event)+len(value)+1 > mimoMaxEventBytes {
			return mimoStreamError("SSE event exceeds size limit")
		}
		if len(s.event) > 0 {
			s.event = append(s.event, '\n')
		}
		s.event = append(s.event, value...)
	}
	if s.scanner.Err() != nil {
		return mimoStreamError("SSE read failed")
	}
	if len(s.event) > 0 {
		return mimoStreamError("truncated SSE event")
	}
	return s.complete()
}

func (s *mimoAudioStream) decode(event []byte) error {
	var chunk mimoChunk
	trimmed := bytes.TrimSpace(event)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return mimoStreamError("invalid SSE object")
	}
	if err := json.Unmarshal(trimmed, &chunk); err != nil {
		return mimoStreamError("invalid SSE JSON")
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return mimoStreamError("upstream error event")
	}
	if chunk.Usage != nil && (chunk.Usage.PromptTokens < 0 || chunk.Usage.CompletionTokens < 0 || chunk.Usage.TotalTokens < 0) {
		return mimoStreamError("invalid token usage")
	}
	s.metadataMu.Lock()
	if chunk.ID != "" {
		s.metadata.RequestID = chunk.ID
	}
	if chunk.Usage != nil {
		s.metadata.Usage = chunk.Usage
	}
	s.metadataMu.Unlock()
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			return mimoStreamError("unexpected audio choice")
		}
		if choice.Delta.Audio != nil && choice.Delta.Audio.Data != "" {
			if s.stopped {
				return mimoStreamError("audio follows stop")
			}
			pcm, err := base64.StdEncoding.Strict().DecodeString(choice.Delta.Audio.Data)
			if err != nil {
				return mimoStreamError("invalid PCM base64")
			}
			s.pending = append(s.pending, pcm...)
			s.pcmBytes += int64(len(pcm))
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			switch *choice.FinishReason {
			case "stop":
				s.stopped = true
			case "length", "content_filter":
				return mimoStreamError("synthesis ended with " + *choice.FinishReason)
			default:
				return mimoStreamError("unexpected finish reason")
			}
		}
	}
	return nil
}

// SSE permits LF, CRLF, and CR separators, including when CRLF spans reads.
func mimoSSELines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\r' {
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
		}
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (s *mimoAudioStream) complete() error {
	if !s.stopped {
		return mimoStreamError("missing stop event")
	}
	if s.pcmBytes == 0 || s.pcmBytes%2 != 0 {
		return mimoStreamError("incomplete PCM samples")
	}
	return io.EOF
}

func mimoStreamError(detail string) error {
	return fmt.Errorf("%w: MiMo %s", domain.ErrUncertain, detail)
}

func mimoWAVHeader() []byte {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], ^uint32(0))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], 24000)
	binary.LittleEndian.PutUint32(header[28:], 48000)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], ^uint32(0))
	return header
}

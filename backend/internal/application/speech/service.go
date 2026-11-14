package speech

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"github.com/google/uuid"
)

type Config struct {
	MaxRecordingSeconds  int
	MaxConcurrentPerUser int
	MaxConcurrent        int
	MaxAudioBytes        int64
}

const APIBasePath = "/api/v1"

type Service struct {
	cfg       func() Config
	providers map[string]domain.Provider
	policy    domain.Authorizer
	meter     domain.Meter
	store     domain.Store
	mu        sync.Mutex
	synthMu   sync.Mutex
	active    map[uint]int
	total     int
	sessions  map[string]*session
	jobs      map[string]*job
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}
type session struct {
	user          uint
	input         SessionInput
	expires       time.Time
	claimed       bool
	release       func()
	ticket        domain.BillingTicket
	wsTicket      string
	provider      domain.Provider
	maxDurationMS int64
	maxBytes      int64
	timer         *time.Timer
}
type job struct {
	done    chan struct{}
	changed chan struct{}
}
type SessionInput struct {
	Provider   string `json:"provider"`
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
	DraftID    string `json:"draft_id"`
}
type Session struct {
	ID            string `json:"id"`
	WSURL         string `json:"ws_url"`
	MaxDurationMS int64  `json:"max_duration_ms"`
	MaxBytes      int64  `json:"max_bytes"`
}
type TTSInput struct {
	Provider  string `json:"provider"`
	Text      string `json:"text"`
	Voice     string `json:"voice"`
	Format    string `json:"format"`
	RequestID string `json:"request_id"`
	Retry     bool   `json:"retry,omitempty"`
}
type TTSResult struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	MediaURL string `json:"media_url"`
	CacheHit bool   `json:"cache_hit"`
}
type Capabilities struct {
	ASR                    []domain.Capability `json:"asr"`
	TTS                    []domain.Capability `json:"tts"`
	MaxRecordingDurationMS int64               `json:"max_recording_duration_ms"`
}

func NewService(cfg func() Config, providers map[string]domain.Provider, policy domain.Authorizer, meter domain.Meter, store domain.Store) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{cfg: cfg, providers: providers, policy: policy, meter: meter, store: store, active: map[uint]int{}, sessions: map[string]*session{}, jobs: map[string]*job{}, ctx: ctx, cancel: cancel}
}
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		// Authorization may currently hold synthMu. Cancel its context before
		// waiting on that mutex, so shutdown can interrupt a blocked reserve.
		s.cancel()
		s.synthMu.Lock()
		s.mu.Lock()
		s.closed = true
		s.cancel()
		pending := map[string]*session{}
		for id, v := range s.sessions {
			if !v.claimed {
				pending[id] = v
				if v.timer != nil {
					v.timer.Stop()
				}
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
		s.synthMu.Unlock()
		for id, v := range pending {
			v.release()
			_ = s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: v.user, OperationID: id, Operation: "asr", Provider: v.input.Provider}, Ticket: v.ticket, Status: "cancelled"})
		}
		s.wg.Wait()
		if closer, ok := s.store.(io.Closer); ok {
			s.closeErr = closer.Close()
		}
	})
	return s.closeErr
}
func (s *Service) config() Config {
	c := Config{}
	if s.cfg != nil {
		c = s.cfg()
	}
	if c.MaxRecordingSeconds <= 0 {
		c.MaxRecordingSeconds = 120
	}
	if c.MaxConcurrentPerUser <= 0 {
		c.MaxConcurrentPerUser = 2
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 32
	}
	if c.MaxAudioBytes <= 0 {
		c.MaxAudioBytes = 8 << 20
	}
	return c
}
func (s *Service) provider(id string) domain.Provider {
	p := s.providers[id]
	if snapshot, ok := p.(interface{ Snapshot() domain.Provider }); ok {
		return snapshot.Snapshot()
	}
	return p
}
func (s *Service) Capabilities() Capabilities {
	c := Capabilities{ASR: []domain.Capability{}, TTS: []domain.Capability{}, MaxRecordingDurationMS: s.maxDuration(0)}
	for _, id := range []string{"deepseek", "doubao-ime", "wetype"} {
		p := s.provider(id)
		if p == nil {
			continue
		}
		if a := p.ASRCapability(); a != nil {
			v := *a
			v.MaxDurationMS = s.maxDuration(v.MaxDurationMS)
			if v.MaxBytes == 0 || v.MaxBytes > s.config().MaxAudioBytes {
				v.MaxBytes = s.config().MaxAudioBytes
			}
			c.ASR = append(c.ASR, v)
		}
		if t := p.TTSCapability(); t != nil {
			c.TTS = append(c.TTS, *t)
		}
	}
	return c
}
func (s *Service) maxDuration(hard int64) int64 {
	v := int64(s.config().MaxRecordingSeconds) * 1000
	if hard > 0 && hard < v {
		return hard
	}
	return v
}
func (s *Service) reserve(ctx context.Context, a domain.Access) (func(), domain.BillingTicket, error) {
	if s.policy == nil || s.meter == nil || s.store == nil {
		return nil, domain.BillingTicket{}, domain.ErrUnavailable
	}
	if a.UserID == 0 {
		return nil, domain.BillingTicket{}, domain.ErrForbidden
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, domain.BillingTicket{}, domain.ErrUnavailable
	}
	c := s.config()
	if s.active[a.UserID] >= c.MaxConcurrentPerUser || s.total >= c.MaxConcurrent {
		s.mu.Unlock()
		return nil, domain.BillingTicket{}, domain.ErrLimited
	}
	s.active[a.UserID]++
	s.total++
	s.wg.Add(1)
	s.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			s.mu.Lock()
			s.active[a.UserID]--
			if s.active[a.UserID] == 0 {
				delete(s.active, a.UserID)
			}
			s.total--
			s.mu.Unlock()
			s.wg.Done()
		})
	}
	authCtx, authCancel := context.WithCancel(ctx)
	stopAuth := context.AfterFunc(s.ctx, authCancel)
	defer func() { stopAuth(); authCancel() }()
	ticket, err := s.policy.Authorize(authCtx, a)
	if err != nil {
		release()
		return nil, domain.BillingTicket{}, err
	}
	return release, ticket, nil
}
func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
func userKey(user uint) string { b, _ := json.Marshal(user); return string(b) }
func WeightedCharacters(text string) int64 {
	var n int64
	for _, r := range text {
		n++
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}

func (s *Service) CreateSession(ctx context.Context, user uint, in SessionInput) (Session, error) {
	p := s.provider(in.Provider)
	if p == nil || p.ASRCapability() == nil || !p.ASRCapability().Streaming {
		return Session{}, domain.ErrUnavailable
	}
	if in.Format != "opus" || in.SampleRate != 16000 || strings.TrimSpace(in.DraftID) == "" || len(in.DraftID) > 256 {
		return Session{}, domain.ErrInvalid
	}
	id := uuid.NewString()
	max := s.maxDuration(p.ASRCapability().MaxDurationMS)
	release, ticket, err := s.reserve(ctx, domain.Access{UserID: user, OperationID: id, Operation: "asr", Provider: in.Provider, Units: max})
	if err != nil {
		return Session{}, err
	}
	wsTicket := uuid.NewString() + uuid.NewString()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		release()
		_ = s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: user, OperationID: id, Operation: "asr", Provider: in.Provider}, Ticket: ticket, Status: "cancelled"})
		return Session{}, domain.ErrUnavailable
	}
	s.sessions[id] = &session{user: user, input: in, expires: time.Now().Add(30 * time.Second), release: release, ticket: ticket, wsTicket: wsTicket, provider: p, maxDurationMS: max, maxBytes: s.config().MaxAudioBytes}
	s.mu.Unlock()
	timer := time.AfterFunc(30*time.Second, func() {
		s.mu.Lock()
		v := s.sessions[id]
		if v != nil && !v.claimed {
			delete(s.sessions, id)
		} else {
			v = nil
		}
		s.mu.Unlock()
		if v != nil {
			v.release()
			_ = s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: user, OperationID: id, Operation: "asr", Provider: in.Provider}, Ticket: v.ticket, Status: "cancelled"})
		}
	})
	s.mu.Lock()
	if v := s.sessions[id]; v != nil {
		v.timer = timer
	} else {
		timer.Stop()
	}
	s.mu.Unlock()
	return Session{ID: id, WSURL: APIBasePath + "/speech/asr/sessions/" + id + "/stream?ticket=" + wsTicket, MaxDurationMS: max, MaxBytes: s.config().MaxAudioBytes}, nil
}
func (s *Service) SessionUser(id, ticket string) (uint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.sessions[id]
	if v == nil || v.claimed || time.Now().After(v.expires) || subtle.ConstantTimeCompare([]byte(ticket), []byte(v.wsTicket)) != 1 {
		return 0, domain.ErrNotFound
	}
	return v.user, nil
}
func (s *Service) Stream(ctx context.Context, user uint, id string, frames <-chan []byte, emit func(domain.Event) error) error {
	s.mu.Lock()
	v := s.sessions[id]
	if v == nil || v.user != user || v.claimed || time.Now().After(v.expires) {
		s.mu.Unlock()
		return domain.ErrNotFound
	}
	v.claimed = true
	if v.timer != nil {
		v.timer.Stop()
	}
	s.mu.Unlock()
	defer func() { v.release(); s.mu.Lock(); delete(s.sessions, id); s.mu.Unlock() }()
	p := v.provider
	max := v.maxDurationMS
	ctx, cancel := context.WithTimeout(ctx, time.Duration(max)*time.Millisecond+20*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	bounded := make(chan []byte, 16)
	var duration, bytes int64
	var guardErr error
	var guardMu sync.Mutex
	go func() {
		defer close(bounded)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-frames:
				if !ok {
					return
				}
				guardMu.Lock()
				duration += 20
				bytes += int64(len(f))
				if !validOpus20ms(f) || duration > max || bytes > v.maxBytes {
					guardErr = domain.ErrInvalid
					guardMu.Unlock()
					cancel()
					return
				}
				guardMu.Unlock()
				select {
				case bounded <- f:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	var seq int64
	var emitMu sync.Mutex
	lastPartial := ""
	send := func(e domain.Event) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		if e.Type == "transcript" {
			if e.Text == "" || e.Text == lastPartial {
				return nil
			}
			lastPartial = e.Text
		}
		seq++
		e.SessionID = id
		e.Sequence = seq
		return emit(e)
	}
	_ = send(domain.Event{Type: "ready"})
	result, err := p.Stream(ctx, domain.StreamRequest{UserID: user, SessionID: id, Format: v.input.Format, SampleRate: v.input.SampleRate}, bounded, send)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	guardMu.Lock()
	actual := duration
	if guardErr != nil {
		err = guardErr
	}
	guardMu.Unlock()
	status := "complete"
	if err != nil {
		status = "uncertain"
	}
	meterErr := s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: user, OperationID: id, Operation: "asr", Provider: v.input.Provider, Units: max}, Ticket: v.ticket, ActualUnits: actual, Status: status})
	if err != nil {
		return err
	}
	if meterErr != nil {
		return meterErr
	}
	return send(domain.Event{Type: "final", Text: result.Text, Final: true})
}
func (s *Service) Transcribe(ctx context.Context, user uint, provider, requestID string, audio domain.Audio) (domain.Transcript, error) {
	if s.store == nil {
		return domain.Transcript{}, domain.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	p := s.provider(provider)
	if p == nil || p.ASRCapability() == nil || p.ASRCapability().Streaming {
		return domain.Transcript{}, domain.ErrUnavailable
	}
	if requestID == "" || len(requestID) > 128 || audio.DurationMS <= 0 || audio.DurationMS > s.maxDuration(p.ASRCapability().MaxDurationMS) || int64(len(audio.Bytes)) > s.config().MaxAudioBytes || len(audio.Bytes) > 8<<20 {
		return domain.Transcript{}, domain.ErrInvalid
	}
	id := digest("asr", userKey(user), provider, requestID)
	audioHash := sha256.Sum256(audio.Bytes)
	fingerprint := hex.EncodeToString(audioHash[:])
	s.mu.Lock()
	existing, e := s.store.Get(ctx, id)
	if e == nil {
		s.mu.Unlock()
		if existing.UserID != user {
			return domain.Transcript{}, domain.ErrForbidden
		}
		if existing.Fingerprint != fingerprint {
			return domain.Transcript{}, domain.ErrConflict
		}
		if existing.Status != "complete" {
			return domain.Transcript{}, domain.ErrUncertain
		}
		return domain.Transcript{SessionID: id, Text: existing.Text, DurationMS: existing.DurationMS, Sequence: 1, Final: true}, nil
	}
	if !errors.Is(e, domain.ErrNotFound) {
		s.mu.Unlock()
		return domain.Transcript{}, e
	}
	s.mu.Unlock()
	release, ticket, err := s.reserve(ctx, domain.Access{UserID: user, OperationID: id, Operation: "asr", Provider: provider, Units: audio.DurationMS})
	if err != nil {
		return domain.Transcript{}, err
	}
	defer release()
	r := domain.Record{ID: id, Fingerprint: fingerprint, UserID: user, Provider: provider, Operation: "asr", Status: "pending", CreatedAt: time.Now(), Ticket: ticket}
	s.mu.Lock()
	if _, e := s.store.Get(ctx, id); !errors.Is(e, domain.ErrNotFound) {
		s.mu.Unlock()
		return domain.Transcript{}, domain.ErrUncertain
	}
	err = s.store.Put(ctx, r)
	s.mu.Unlock()
	if err != nil {
		_ = s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: user, OperationID: id, Operation: "asr", Provider: provider, Units: audio.DurationMS}, Ticket: ticket, Status: "cancelled"})
		return domain.Transcript{}, err
	}
	result, err := p.Transcribe(ctx, audio)
	r.Status = "uncertain"
	if errors.Is(err, domain.ErrRejected) {
		r.Status = "cancelled"
	}
	if err == nil {
		r.Text = result.Text
		r.DurationMS = result.DurationMS
		r.UpstreamRequestID = result.UpstreamRequestID
		r.Status = "complete"
	}
	meterErr := s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: user, OperationID: id, Operation: "asr", Provider: provider, Units: audio.DurationMS}, Ticket: r.Ticket, ActualUnits: result.DurationMS, Status: r.Status, UpstreamRequestID: result.UpstreamRequestID})
	if meterErr != nil {
		r.Status = "uncertain"
	}
	_ = s.store.Put(context.Background(), r)
	if err != nil {
		return domain.Transcript{}, err
	}
	if meterErr != nil {
		return domain.Transcript{}, meterErr
	}
	result.SessionID = id
	result.Sequence = 1
	result.Final = true
	return result, nil
}

func (s *Service) Synthesize(ctx context.Context, user uint, in TTSInput) (TTSResult, error) {
	p := s.provider(in.Provider)
	if p == nil || p.TTSCapability() == nil {
		return TTSResult{}, domain.ErrUnavailable
	}
	in.Text = strings.TrimSpace(strings.ReplaceAll(in.Text, "\r\n", "\n"))
	if in.Voice == "" {
		in.Voice = "stella"
	}
	if in.Format == "" {
		in.Format = "wav"
	}
	units := WeightedCharacters(in.Text)
	payload, _ := json.Marshal(struct {
		Text           string `json:"text"`
		Voice          string `json:"voice_id"`
		Format         string `json:"format"`
		FilterMarkdown bool   `json:"filter_markdown"`
	}{in.Text, in.Voice, in.Format, true})
	if in.RequestID == "" || len(in.RequestID) > 128 || in.Text == "" || units > 10000 || len(payload) > 64<<10 || (in.Voice != "stella" && in.Voice != "tide") || (in.Format != "wav" && in.Format != "pcm") {
		return TTSResult{}, domain.ErrInvalid
	}
	if s.store == nil {
		return TTSResult{}, domain.ErrUnavailable
	}
	cacheKey := "cache-" + digest("tts-v1", userKey(user), in.Provider, in.Text, in.Voice, in.Format)
	requestKey := "request-" + digest(userKey(user), in.RequestID)
	s.synthMu.Lock()
	defer s.synthMu.Unlock()
	if r, err := s.store.Get(ctx, requestKey); err == nil {
		if r.UserID != user {
			return TTSResult{}, domain.ErrForbidden
		}
		if r.Fingerprint != cacheKey {
			return TTSResult{}, domain.ErrConflict
		}
		return ttsResult(r, r.Status == "complete"), nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return TTSResult{}, err
	}
	if r, err := s.store.Get(ctx, cacheKey); err == nil {
		if r.Status == "complete" || r.Status == "pending" || (!in.Retry && r.Status == "uncertain") {
			if err = s.store.Put(ctx, domain.Record{ID: requestKey, ReferenceID: r.ID, CreatedAt: time.Now()}); err != nil {
				return TTSResult{}, err
			}
			return ttsResult(r, r.Status == "complete"), nil
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return TTSResult{}, err
	}
	id := uuid.NewString()
	release, ticket, err := s.reserve(ctx, domain.Access{UserID: user, OperationID: id, Operation: "tts", Provider: in.Provider, Units: units})
	if err != nil {
		return TTSResult{}, err
	}
	r := domain.Record{ID: id, OperationID: id, Fingerprint: cacheKey, UserID: user, Provider: in.Provider, Operation: "tts", Status: "pending", Units: units, CreatedAt: time.Now(), Ticket: ticket}
	if err = s.store.Put(ctx, r); err != nil {
		release()
		_ = s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: user, OperationID: id, Operation: "tts", Provider: in.Provider, Units: units}, Ticket: ticket, Status: "cancelled"})
		return TTSResult{}, err
	}
	for _, key := range []string{cacheKey, requestKey} {
		if err = s.store.Put(ctx, domain.Record{ID: key, ReferenceID: id, CreatedAt: r.CreatedAt}); err != nil {
			r.Status = "cancelled"
			_ = s.store.Put(context.Background(), r)
			_ = s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: user, OperationID: id, Operation: "tts", Provider: in.Provider, Units: units}, Ticket: ticket, Status: "cancelled"})
			release()
			return TTSResult{}, err
		}
	}
	j := &job{done: make(chan struct{}), changed: make(chan struct{}, 1)}
	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()
	go s.generate(r, in, p, j, release)
	return ttsResult(r, false), nil
}
func ttsResult(r domain.Record, hit bool) TTSResult {
	return TTSResult{ID: r.ID, Status: r.Status, MediaURL: APIBasePath + "/speech/tts/" + r.ID + "/audio", CacheHit: hit}
}
func (s *Service) generate(r domain.Record, in TTSInput, p domain.Provider, j *job, release func()) {
	defer release()
	defer close(j.done)
	defer func() { s.mu.Lock(); delete(s.jobs, r.ID); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	defer cancel()
	w, storageErr := s.store.CreateAudio(ctx, r.ID)
	if storageErr != nil {
		r.Status = "cancelled"
		r.Error = "storage_unavailable"
		_ = s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: r.UserID, OperationID: r.ID, Operation: "tts", Provider: r.Provider, Units: r.Units}, Ticket: r.Ticket, Status: "cancelled"})
		_ = s.store.Put(context.Background(), r)
		return
	}
	body, ct, err := p.Synthesize(ctx, domain.Synthesis{Text: in.Text, Voice: in.Voice, Format: in.Format})
	if err == nil {
		defer body.Close()
		r.ContentType = ct
		err = s.store.Put(ctx, r)
		if err == nil {
			{
				buf := make([]byte, 32<<10)
				for {
					n, e := body.Read(buf)
					if n > 0 {
						maximum := int64(86400044)
						if in.Format == "pcm" {
							maximum = 86400000
						}
						if r.Bytes+int64(n) > maximum {
							err = domain.ErrInvalid
							break
						}
						wn, we := w.Write(buf[:n])
						r.Bytes += int64(wn)
						select {
						case j.changed <- struct{}{}:
						default:
						}
						if we != nil {
							err = we
							break
						}
					}
					if e != nil {
						if e != io.EOF {
							err = e
						}
						break
					}
				}
				if r.Bytes == 0 && err == nil {
					err = io.ErrUnexpectedEOF
				}
			}
		}
	}
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	r.Status = "complete"
	if err != nil {
		r.Status = "uncertain"
		if errors.Is(err, domain.ErrRejected) {
			r.Status = "cancelled"
		}
		r.Error = "synthesis_failed"
		_ = s.store.DeleteAudio(context.Background(), r.ID)
	}
	if e := s.meter.Record(context.Background(), domain.Usage{Access: domain.Access{UserID: r.UserID, OperationID: r.ID, Operation: "tts", Provider: r.Provider, Units: r.Units}, Ticket: r.Ticket, ActualUnits: r.Units, Status: r.Status}); e != nil {
		r.Status = "uncertain"
		r.Error = "settlement_failed"
		_ = s.store.DeleteAudio(context.Background(), r.ID)
	}
	_ = s.store.Put(context.Background(), r)
}
func (s *Service) GetTTS(ctx context.Context, user uint, id string) (TTSResult, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return TTSResult{}, err
	}
	if r.UserID != user || r.Operation != "tts" {
		return TTSResult{}, domain.ErrNotFound
	}
	return ttsResult(r, r.Status == "complete"), nil
}

// Audio streams a growing file with bounded memory. A failed generation ends
// playback and never exposes its partial bytes as a reusable successful result.
func (s *Service) Audio(ctx context.Context, user uint, id string, w io.Writer, onStart func(string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if r.UserID != user || r.Operation != "tts" {
		return domain.ErrNotFound
	}
	if r.Status == "expired" {
		return domain.ErrExpired
	}
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if r.Status != "complete" && j == nil {
		// Completion may have raced the first metadata read and removed its
		// in-memory job. Re-read the persisted terminal state before rejecting.
		r, err = s.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if r.Status == "expired" {
			return domain.ErrExpired
		}
		if r.Status != "complete" {
			return domain.ErrUncertain
		}
	}
	var f io.ReadCloser
	for {
		r, err = s.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if r.ContentType == "" && j != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-j.done:
				r, err = s.store.Get(ctx, id)
				if err != nil {
					return err
				}
				if r.Status != "complete" {
					return domain.ErrUncertain
				}
			case <-time.After(25 * time.Millisecond):
			}
			continue
		}
		f, err = s.store.OpenAudio(ctx, id)
		if err == nil {
			break
		}
		if j == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-j.done:
			r, err = s.store.Get(ctx, id)
			if err != nil {
				return err
			}
			if r.Status != "complete" {
				return domain.ErrUncertain
			}
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer f.Close()
	r, _ = s.store.Get(ctx, id)
	onStart(r.ContentType)
	buf := make([]byte, 32<<10)
	for {
		n, e := f.Read(buf)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if n > 0 {
			if _, err = w.Write(buf[:n]); err != nil {
				return err
			}
			if fl, ok := w.(interface{ Flush() }); ok {
				fl.Flush()
			}
		}
		if e != nil && e != io.EOF {
			return e
		}
		if e == io.EOF {
			if j == nil {
				return nil
			}
			select {
			case <-j.done:
				r, err = s.store.Get(ctx, id)
				if err != nil {
					return err
				}
				if r.Status != "complete" {
					return domain.ErrUncertain
				}
				j = nil
				continue
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
}

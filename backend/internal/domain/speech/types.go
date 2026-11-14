package speech

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

var (
	ErrUnavailable  = errors.New("speech provider unavailable")
	ErrInvalid      = errors.New("invalid speech request")
	ErrForbidden    = errors.New("speech access denied")
	ErrNotFound     = errors.New("speech resource not found")
	ErrLimited      = errors.New("speech capacity exceeded")
	ErrUncertain    = errors.New("speech upstream outcome uncertain")
	ErrConflict     = errors.New("speech request identity conflict")
	ErrBackpressure = errors.New("speech audio buffer full")
	ErrExpired      = errors.New("speech audio expired")
	ErrRejected     = errors.New("speech request rejected before audio was returned")
)

type Voice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Capability struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Streaming             bool     `json:"streaming"`
	Formats               []string `json:"formats"`
	SampleRate            int      `json:"sample_rate,omitempty"`
	MaxDurationMS         int64    `json:"max_duration_ms,omitempty"`
	MaxBytes              int64    `json:"max_bytes,omitempty"`
	Voices                []Voice  `json:"voices,omitempty"`
	MaxWeightedCharacters int64    `json:"max_weighted_characters,omitempty"`
	MaxTextBytes          int64    `json:"max_text_bytes,omitempty"`
}
type Transcript struct {
	UpstreamRequestID string `json:"-"`
	SessionID         string `json:"session_id"`
	Text              string `json:"text"`
	DurationMS        int64  `json:"duration_ms"`
	Sequence          int64  `json:"sequence"`
	Final             bool   `json:"final"`
}
type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Sequence  int64  `json:"sequence"`
	Text      string `json:"text,omitempty"`
	Final     bool   `json:"final"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}
type Audio struct {
	Bytes      []byte
	Format     string
	DurationMS int64
}
type StreamRequest struct {
	UserID     uint
	SessionID  string
	Format     string
	SampleRate int
}
type Synthesis struct {
	Text   string
	Voice  string
	Format string
}
type Provider interface {
	ASRCapability() *Capability
	TTSCapability() *Capability
	Transcribe(context.Context, Audio) (Transcript, error)
	Stream(context.Context, StreamRequest, <-chan []byte, func(Event) error) (Transcript, error)
	Synthesize(context.Context, Synthesis) (io.ReadCloser, string, error)
}

// Access is reserved before an upstream request. Units are milliseconds for ASR
// and weighted characters for TTS; they never represent language-model tokens.
type Access struct {
	UserID      uint
	OperationID string
	Operation   string
	Provider    string
	Units       int64
}
type BillingTicket struct {
	Reference string          `json:"reference"`
	Snapshot  json.RawMessage `json:"snapshot"`
}
type Usage struct {
	Access
	Ticket            BillingTicket
	ActualUnits       int64
	Status            string
	UpstreamRequestID string
}
type Authorizer interface {
	Authorize(context.Context, Access) (BillingTicket, error)
}
type Meter interface {
	Record(context.Context, Usage) error
}
type Record struct {
	UpstreamRequestID string        `json:"upstream_request_id,omitempty"`
	ID                string        `json:"id"`
	OperationID       string        `json:"operation_id"`
	UserID            uint          `json:"user_id"`
	Provider          string        `json:"provider"`
	Operation         string        `json:"operation"`
	Status            string        `json:"status"`
	ContentType       string        `json:"content_type,omitempty"`
	Text              string        `json:"text,omitempty"`
	DurationMS        int64         `json:"duration_ms,omitempty"`
	Units             int64         `json:"units"`
	Bytes             int64         `json:"bytes"`
	CreatedAt         time.Time     `json:"created_at"`
	Error             string        `json:"error,omitempty"`
	Ticket            BillingTicket `json:"billing_ticket"`
	ReferenceID       string        `json:"reference_id,omitempty"`
	Fingerprint       string        `json:"fingerprint,omitempty"`
}

// Store persists accepted/uncertain states as well as complete results, so a
// process restart cannot silently resubmit an accepted paid synthesis.
type Store interface {
	Get(context.Context, string) (Record, error)
	Put(context.Context, Record) error
	CreateAudio(context.Context, string) (io.WriteCloser, error)
	OpenAudio(context.Context, string) (io.ReadCloser, error)
	DeleteAudio(context.Context, string) error
}

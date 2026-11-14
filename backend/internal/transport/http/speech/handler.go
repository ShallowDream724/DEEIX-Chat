package speech

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
)

type Handler struct {
	service        *app.Service
	allowedOrigins func() []string
}

func NewHandler(s *app.Service, origins ...func() []string) *Handler {
	h := &Handler{service: s}
	if len(origins) > 0 {
		h.allowedOrigins = origins[0]
	}
	return h
}

type Module struct{ Handler *Handler }

func NewModule(h *Handler) *Module { return &Module{Handler: h} }

// Public stream route is authenticated exclusively by its short-lived,
// single-session random ticket. Normal API bearer tokens never enter its URL.
func (m *Module) RegisterRoutes(authRequired, public *gin.RouterGroup) {
	g := authRequired.Group("/speech")
	g.GET("/capabilities", m.Handler.Capabilities)
	g.POST("/asr/sessions", m.Handler.CreateSession)
	g.DELETE("/asr/sessions/:id", m.Handler.CancelSession)
	g.POST("/asr/transcriptions", m.Handler.Transcribe)
	g.POST("/tts", m.Handler.Synthesize)
	g.GET("/tts/:id", m.Handler.GetTTS)
	g.GET("/tts/:id/audio", m.Handler.Audio)
	public.GET("/speech/asr/sessions/:id/stream", m.Handler.Stream)
}
func describe(err error) response.Description {
	status := http.StatusBadGateway
	public := apperr.New("speech.failed", "speech service failed")
	switch {
	case errors.Is(err, domain.ErrInvalid):
		status = 400
		public = apperr.New("speech.invalid", "invalid speech request")
	case errors.Is(err, domain.ErrDurationLimit):
		status = http.StatusRequestEntityTooLarge
		public = apperr.New("speech.duration_limit", "speech recording exceeded its duration limit")
	case errors.Is(err, domain.ErrForbidden):
		status = 403
		public = apperr.New("speech.forbidden", "speech access denied")
	case errors.Is(err, domain.ErrNotFound):
		status = 404
		public = apperr.New("speech.not_found", "speech resource not found")
	case errors.Is(err, domain.ErrLimited):
		status = 429
		public = apperr.New("speech.limited", "too many concurrent speech requests")
	case errors.Is(err, domain.ErrBackpressure):
		status = 429
		public = apperr.New("speech.backpressure", "audio connection is too slow; recorded text is preserved")
	case errors.Is(err, domain.ErrUnavailable):
		status = 503
		public = apperr.New("speech.unavailable", "speech provider unavailable")
	case errors.Is(err, domain.ErrUncertain):
		status = 409
		public = apperr.New("speech.uncertain", "speech outcome requires reconciliation")
	case errors.Is(err, domain.ErrConflict):
		status = 409
		public = apperr.New("speech.conflict", "speech request identity already has different content")
	case errors.Is(err, domain.ErrProviderChanged):
		status = 409
		public = apperr.New("speech.input_provider_changed", "speech input provider changed; start a new recording")
	case errors.Is(err, domain.ErrExpired):
		status = 410
		public = apperr.New("speech.expired", "speech audio expired")
	}
	switch apperr.Code(err) {
	case "billing.insufficient_funds", "billing.period_credit_exceeded":
		return response.Describe(http.StatusPaymentRequired, err)
	case "billing.concurrency_limit_exceeded":
		return response.Describe(http.StatusTooManyRequests, err)
	case "billing.reservation_conflict":
		return response.Describe(http.StatusConflict, err)
	}
	return response.Describe(status, public)
}
func fail(c *gin.Context, err error) {
	response.RecordError(c, err)
	response.ErrorDescribed(c, describe(err))
}
func (h *Handler) Capabilities(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	response.Success(c, h.service.Capabilities())
}
func (h *Handler) CancelSession(c *gin.Context) {
	if err := h.service.CancelSession(c.Request.Context(), middleware.MustUserID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	response.Success(c, gin.H{"cancelled": true})
}
func (h *Handler) CreateSession(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var in app.SessionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, domain.ErrInvalid)
		return
	}
	r, err := h.service.CreateSession(c.Request.Context(), middleware.MustUserID(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, r)
}
func (h *Handler) Transcribe(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (8<<20)+(64<<10))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		fail(c, domain.ErrInvalid)
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		fail(c, domain.ErrInvalid)
		return
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil {
		fail(c, err)
		return
	}
	audio, err := infra.InspectAudio(b)
	if err != nil {
		fail(c, err)
		return
	}
	var r domain.Transcript
	if id := c.PostForm("session_id"); id != "" {
		r, err = h.service.TranscribeSession(c.Request.Context(), middleware.MustUserID(c), c.PostForm("provider"), c.PostForm("request_id"), id, c.PostForm("session_token"), audio)
	} else {
		r, err = h.service.Transcribe(c.Request.Context(), middleware.MustUserID(c), c.PostForm("provider"), c.PostForm("request_id"), audio)
	}
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, r)
}
func (h *Handler) Synthesize(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 66<<10)
	var in app.TTSInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, domain.ErrInvalid)
		return
	}
	r, err := h.service.Synthesize(c.Request.Context(), middleware.MustUserID(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, r)
}
func (h *Handler) GetTTS(c *gin.Context) {
	r, err := h.service.GetTTS(c.Request.Context(), middleware.MustUserID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	response.Success(c, r)
}
func (h *Handler) Audio(c *gin.Context) {
	started := false
	err := h.service.Audio(c.Request.Context(), middleware.MustUserID(c), c.Param("id"), c.Writer, func(ct string) {
		started = true
		c.Header("Content-Type", ct)
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Status(200)
	})
	if err != nil && !started {
		fail(c, err)
	}
}

type wire struct {
	data []byte
	kind byte
}

func (h *Handler) Stream(c *gin.Context) {
	id := c.Param("id")
	user, err := h.service.SessionUser(id, c.Query("ticket"))
	if err != nil {
		fail(c, err)
		return
	}
	server := websocket.Server{Handshake: func(cfg *websocket.Config, req *http.Request) error {
		origin := req.Header.Get("Origin")
		if origin == "" {
			return nil
		}
		configured := "http://" + req.Host
		if req.TLS != nil {
			configured = "https://" + req.Host
		}
		if h.allowedOrigins != nil {
			configured = strings.Join(h.allowedOrigins(), ",")
		}
		if !middleware.IsCORSOriginAllowed(origin, configured) {
			return domain.ErrForbidden
		}
		return nil
	}, Handler: func(ws *websocket.Conn) {
		defer ws.Close()
		ws.MaxPayloadBytes = 16 << 10
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		frames := make(chan []byte, 32)
		readerErr := make(chan error, 1)
		readerDone := make(chan struct{})
		codec := websocket.Codec{Unmarshal: func(b []byte, t byte, v any) error {
			p := v.(*wire)
			p.data = append([]byte(nil), b...)
			p.kind = t
			return nil
		}}
		go func() {
			defer close(readerDone)
			closed := false
			defer func() {
				if !closed {
					close(frames)
				}
				cancel()
			}()
			for {
				var msg wire
				if err := codec.Receive(ws, &msg); err != nil {
					return
				}
				if msg.kind == websocket.TextFrame {
					var control struct {
						Type string `json:"type"`
					}
					if json.Unmarshal(msg.data, &control) != nil {
						return
					}
					if control.Type == "cancel" {
						return
					}
					if control.Type == "finish" && !closed {
						close(frames)
						closed = true
						continue
					}
					return
				}
				if closed || msg.kind != websocket.BinaryFrame || len(msg.data) > 1275 {
					return
				}
				select {
				case frames <- msg.data:
				case <-ctx.Done():
					return
				default:
					readerErr <- domain.ErrBackpressure
					return
				}
			}
		}()
		emit := func(e domain.Event) error {
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			return websocket.JSON.Send(ws, e)
		}
		if err := h.service.Stream(ctx, user, id, frames, emit); err != nil {
			select {
			case readErr := <-readerErr:
				err = readErr
			default:
			}
			description := describe(err)
			response.RecordError(c, err)
			_ = emit(domain.Event{Type: "error", SessionID: id, Code: description.Code, Message: description.Message})
			// Drain a bounded tail before closing. Abruptly closing a socket
			// with unread client audio can reset TCP and discard the error event.
			cancel()
			_ = ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			<-readerDone
			for i := 0; i < 128; i++ {
				var discarded wire
				if codec.Receive(ws, &discarded) != nil {
					break
				}
			}
		}
	}}
	server.ServeHTTP(c.Writer, c.Request)
}

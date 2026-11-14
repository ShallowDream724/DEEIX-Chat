package speech

import (
	"context"
	"encoding/json"
	"errors"
	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testBilling struct{}

func (testBilling) Authorize(context.Context, domain.Access) (domain.BillingTicket, error) {
	return domain.BillingTicket{Reference: "authorization"}, nil
}
func (testBilling) Record(context.Context, domain.Usage) error { return nil }

type streamProvider struct{}
type ttsProvider struct{ streamProvider }

func (ttsProvider) TTSCapability() *domain.Capability {
	return &domain.Capability{ID: "deepseek", Formats: []string{"wav"}}
}
func (ttsProvider) Synthesize(context.Context, domain.Synthesis) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("audio bytes")), "audio/wav", nil
}

type slowProvider struct{ streamProvider }

type failingProvider struct{ streamProvider }

func (failingProvider) Stream(context.Context, domain.StreamRequest, <-chan []byte, func(domain.Event) error) (domain.Transcript, error) {
	return domain.Transcript{}, domain.ErrUnavailable
}

func (slowProvider) Stream(ctx context.Context, r domain.StreamRequest, audio <-chan []byte, emit func(domain.Event) error) (domain.Transcript, error) {
	if e := emit(domain.Event{Type: "transcript", Text: "preserved partial"}); e != nil {
		return domain.Transcript{}, e
	}
	<-ctx.Done()
	return domain.Transcript{}, ctx.Err()
}

func (streamProvider) ASRCapability() *domain.Capability {
	return &domain.Capability{ID: "doubao-ime", Streaming: true, Formats: []string{"opus"}}
}
func (streamProvider) TTSCapability() *domain.Capability { return nil }
func (streamProvider) Transcribe(context.Context, domain.Audio) (domain.Transcript, error) {
	return domain.Transcript{}, domain.ErrUnavailable
}
func (streamProvider) Synthesize(context.Context, domain.Synthesis) (io.ReadCloser, string, error) {
	return nil, "", domain.ErrUnavailable
}
func (streamProvider) Stream(ctx context.Context, r domain.StreamRequest, audio <-chan []byte, emit func(domain.Event) error) (domain.Transcript, error) {
	for {
		select {
		case <-ctx.Done():
			return domain.Transcript{}, ctx.Err()
		case _, ok := <-audio:
			if !ok {
				time.Sleep(20 * time.Millisecond)
				return domain.Transcript{Text: "final tail"}, nil
			}
			if e := emit(domain.Event{Type: "transcript", Text: "temporary"}); e != nil {
				return domain.Transcript{}, e
			}
		}
	}
}
func TestWebSocketTicketOriginsAndFinalTail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, e := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	service := app.NewService(func() app.Config { return app.Config{SelectedInputProvider: "doubao-ime"} }, map[string]domain.Provider{"doubao-ime": streamProvider{}}, testBilling{}, testBilling{}, store)
	defer service.Close()
	router := gin.New()
	public := router.Group(app.APIBasePath)
	NewModule(NewHandler(service, func() []string { return []string{"http://localhost:3100"} })).RegisterRoutes(router.Group("/unused"), public)
	server := httptest.NewServer(router)
	defer server.Close()
	session, e := service.CreateSession(context.Background(), 1, app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "draft"})
	if e != nil {
		t.Fatal(e)
	}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + session.WSURL
	if ws, e := websocket.Dial(url, "", "https://untrusted.example"); e == nil {
		ws.Close()
		t.Fatal("untrusted Origin accepted")
	}
	ws, e := websocket.Dial(url, "", "http://localhost:3100")
	if e != nil {
		t.Fatal("configured frontend Origin blocked", e)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(3 * time.Second))
	var event domain.Event
	if e = websocket.JSON.Receive(ws, &event); e != nil || event.Type != "ready" {
		t.Fatal(event, e)
	}
	if e = websocket.Message.Send(ws, []byte{0x98, 1}); e != nil {
		t.Fatal(e)
	}
	if e = websocket.JSON.Receive(ws, &event); e != nil || event.Text != "temporary" {
		t.Fatal(event, e)
	}
	if e = websocket.JSON.Send(ws, map[string]string{"type": "finish"}); e != nil {
		t.Fatal(e)
	}
	if e = websocket.JSON.Receive(ws, &event); e != nil || !event.Final || event.Text != "final tail" || event.Sequence != 3 {
		t.Fatalf("finish cancelled final tail: %+v %v", event, e)
	}
	if again, e := websocket.Dial(url, "", "http://localhost:3100"); e == nil {
		again.Close()
		t.Fatal("session ticket accepted twice")
	}
	tauri, e := service.CreateSession(context.Background(), 1, app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "desktop-draft"})
	if e != nil {
		t.Fatal(e)
	}
	desktop, e := websocket.Dial("ws"+strings.TrimPrefix(server.URL, "http")+tauri.WSURL, "", "tauri://localhost")
	if e != nil {
		t.Fatal("desktop Origin disagreed with CORS policy", e)
	}
	_ = websocket.JSON.Send(desktop, map[string]string{"type": "cancel"})
	_ = desktop.Close()
}
func TestErrorEnvelopeMasksInternalDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/error", func(c *gin.Context) { fail(c, errors.New("upstream APIKey secret, raw body")) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/error", nil))
	if response.Code != 502 || strings.Contains(response.Body.String(), "secret") {
		t.Fatal(response.Code, response.Body.String())
	}
	var body struct {
		ErrorCode string `json:"errorCode"`
		ErrorMsg  string `json:"errorMsg"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &body); e != nil || body.ErrorCode != "speech.failed" || body.ErrorMsg != "speech service failed" {
		t.Fatal(body, e)
	}
}

func TestUpstreamFailureClosesIdleWebSocket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(func() app.Config { return app.Config{SelectedInputProvider: "doubao-ime"} }, map[string]domain.Provider{"doubao-ime": failingProvider{}}, testBilling{}, testBilling{}, store)
	defer service.Close()
	router := gin.New()
	NewModule(NewHandler(service)).RegisterRoutes(router.Group("/unused"), router.Group(app.APIBasePath))
	returned := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		router.ServeHTTP(w, r)
	}))
	defer server.Close()
	session, err := service.CreateSession(context.Background(), 1, app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(server.URL, "http")+session.WSURL, "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for _, expected := range []string{"ready", "error"} {
		var event domain.Event
		if err := websocket.JSON.Receive(ws, &event); err != nil || event.Type != expected {
			t.Fatalf("want %s before closing, got %+v: %v", expected, event, err)
		}
	}
	// The client sends neither audio nor a close frame. Cleanup must interrupt
	// the blocked server reader itself after delivering the upstream error.
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream failure left the idle WebSocket handler blocked")
	}
}

func TestDurationLimitHasStablePublicCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/limit", func(c *gin.Context) { fail(c, domain.ErrDurationLimit) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/limit", nil))
	var body struct {
		Code string `json:"errorCode"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusRequestEntityTooLarge || body.Code != "speech.duration_limit" {
		t.Fatal(response.Code, body, err)
	}
}
func TestSlowUpstreamHasBoundedBufferAndExplicitBackpressure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, e := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	service := app.NewService(func() app.Config { return app.Config{SelectedInputProvider: "doubao-ime"} }, map[string]domain.Provider{"doubao-ime": slowProvider{}}, testBilling{}, testBilling{}, store)
	defer service.Close()
	router := gin.New()
	NewModule(NewHandler(service)).RegisterRoutes(router.Group("/unused"), router.Group(app.APIBasePath))
	server := httptest.NewServer(router)
	defer server.Close()
	session, e := service.CreateSession(context.Background(), 1, app.SessionInput{Provider: "doubao-ime", Format: "opus", SampleRate: 16000, DraftID: "draft"})
	if e != nil {
		t.Fatal(e)
	}
	ws, e := websocket.Dial("ws"+strings.TrimPrefix(server.URL, "http")+session.WSURL, "", server.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(3 * time.Second))
	var event domain.Event
	for i := 0; i < 2; i++ {
		if e = websocket.JSON.Receive(ws, &event); e != nil {
			t.Fatal(e)
		}
	}
	if event.Text != "preserved partial" {
		t.Fatal(event)
	}
	for i := 0; i < 100; i++ {
		if e = websocket.Message.Send(ws, []byte{0x98, 1}); e != nil {
			break
		}
	}
	if e = websocket.JSON.Receive(ws, &event); e != nil || event.Type != "error" || event.Code != "speech.backpressure" {
		t.Fatalf("unbounded or silent backpressure: %+v %v", event, e)
	}
}
func TestReturnedTTSMediaURLUsesActualVersionedRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, e := infra.NewFileStore(t.TempDir(), 1<<20, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	service := app.NewService(func() app.Config { return app.Config{SelectedInputProvider: "doubao-ime"} }, map[string]domain.Provider{"deepseek": ttsProvider{}}, testBilling{}, testBilling{}, store)
	defer service.Close()
	router := gin.New()
	auth := router.Group(app.APIBasePath, func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, uint(1)); c.Next() })
	NewModule(NewHandler(service)).RegisterRoutes(auth, router.Group(app.APIBasePath))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, app.APIBasePath+"/speech/tts", strings.NewReader(`{"provider":"deepseek","text":"hello","request_id":"attempt"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	var envelope struct {
		Data app.TTSResult `json:"data"`
	}
	if e = json.Unmarshal(response.Body.Bytes(), &envelope); e != nil || response.Code != 200 {
		t.Fatal(response.Code, response.Body.String(), e)
	}
	if !strings.HasPrefix(envelope.Data.MediaURL, "/api/v1/speech/tts/") {
		t.Fatal("wrong advertised media URL", envelope.Data.MediaURL)
	}
	audio := httptest.NewRecorder()
	router.ServeHTTP(audio, httptest.NewRequest(http.MethodGet, envelope.Data.MediaURL, nil))
	if audio.Code != 200 || audio.Body.String() != "audio bytes" || audio.Header().Get("Content-Type") != "audio/wav" {
		t.Fatal("advertised media URL not accessible", audio.Code, audio.Body.String())
	}
}

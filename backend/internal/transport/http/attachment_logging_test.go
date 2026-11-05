package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apperrorlog "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/errorlog"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type attachmentPanicRecorder struct{ records []apperrorlog.RecordInput }

func (r *attachmentPanicRecorder) Record(_ context.Context, input apperrorlog.RecordInput) {
	r.records = append(r.records, input)
}

func TestAttachmentCredentialsDoNotReachPanicDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var defaultRecoveryOutput bytes.Buffer
	originalWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &defaultRecoveryOutput
	t.Cleanup(func() { gin.DefaultErrorWriter = originalWriter })
	core, logs := observer.New(zap.InfoLevel)
	recorded := &attachmentPanicRecorder{}
	engine, err := NewEngine(config.NewRuntime(config.Config{AppName: "test"}), zap.New(core), Modules{ErrorRecorder: recorded}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("d", 43)
	address := "https://files.example/api/v1/attachments/fetch/" + token
	engine.GET("/api/v1/attachments/fetch/:token", func(c *gin.Context) {
		if c.Param("token") != token {
			t.Fatal("panic instrumentation changed the actual token")
		}
		panic("cannot fetch " + address)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/attachments/fetch/"+token, nil)
	request.Header.Set("User-Agent", address)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || len(recorded.records) != 1 {
		t.Fatalf("panic was not recorded: status=%d records=%d", response.Code, len(recorded.records))
	}
	diagnostics := defaultRecoveryOutput.String() + fmt.Sprint(logs.All()) + fmt.Sprint(recorded.records)
	if strings.Contains(diagnostics, token) {
		t.Fatalf("attachment credential reached panic diagnostics: %s", diagnostics)
	}
}

func TestAttachmentCredentialsDoNotReachOTELRequestsOrErrorEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		_ = provider.Shutdown(context.Background())
	})
	engine, err := NewEngine(config.NewRuntime(config.Config{AppName: "test"}), nil, Modules{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("e", 43)
	address := "https://files.example/api/v1/attachments/fetch/" + token
	engine.GET("/api/v1/attachments/fetch/:token", func(c *gin.Context) {
		if c.Param("token") != token || !strings.Contains(c.Request.URL.Path, token) {
			t.Fatal("tracing filter changed the actual authorization URL")
		}
		c.Status(http.StatusNoContent)
	})
	engine.GET("/api/diagnostic", func(c *gin.Context) {
		cause := errors.New("fetch failed: " + address)
		_ = c.Error(cause).SetMeta(map[string]string{"url": address})
		c.Status(http.StatusInternalServerError)
		if !strings.Contains(cause.Error(), token) {
			t.Fatal("handler error changed before handling completed")
		}
	})
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/attachments/fetch/"+token, nil))
	if spans := exporter.GetSpans(); len(spans) != 0 {
		t.Fatalf("credential-bearing fetch request was traced: %+v", spans)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/version?next="+url.QueryEscape(address), nil)
	request.Header.Set("User-Agent", address)
	engine.ServeHTTP(httptest.NewRecorder(), request)
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/diagnostic", nil))
	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Events) == 0 {
		t.Fatalf("ordinary error tracing was not retained: %+v", spans)
	}
	if recorded := fmt.Sprintf("%+v", spans); strings.Contains(recorded, token) {
		t.Fatalf("attachment credential reached OTEL: %s", recorded)
	}
}

package upload

import (
	"context"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/attachmentdelivery"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/cache/memory"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/gin-gonic/gin"
)

func TestRepeatableGETHEADHeadersAndRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.NewRuntime(config.Config{PublicAPIBaseURL: "https://api.example.test", StorageRootDir: t.TempDir()})
	svc := attachmentdelivery.NewService(cfg, nil, memory.NewAttachmentGrants(memory.New()), nil)
	link, err := svc.StageTemporary(context.Background(), 7, strings.NewReader("hello attachment"), "text/plain; charset=utf-8", "中文 report.txt", "run")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	NewModule(NewHandler(svc)).RegisterPublicRoutes(router.Group("/api/v1"))
	for _, method := range []string{http.MethodHead, http.MethodGet, http.MethodHead, http.MethodGet} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, u.Path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d", method, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Length") != "16" || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("headers: %+v", response.Header())
		}
		_, disposition, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
		if err != nil || disposition["filename"] != "中文 report.txt" {
			t.Fatalf("disposition: %v %v", disposition, err)
		}
		if method == http.MethodHead && response.Body.Len() != 0 {
			t.Fatal("HEAD sent body")
		}
		if method == http.MethodGet && response.Body.String() != "hello attachment" {
			t.Fatal(response.Body.String())
		}
	}
	if err := svc.ReleaseRun(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, u.Path, nil))
		if response.Code != http.StatusNotFound || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("revoked response %d %s", response.Code, response.Body.String())
		}
	}
}

func TestFilenameIsSafeAndUnissuedTokenIsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := attachmentdelivery.NewService(config.NewRuntime(config.Config{PublicAPIBaseURL: "https://api.example.test", StorageRootDir: t.TempDir()}), nil, memory.NewAttachmentGrants(memory.New()), nil)
	link, err := svc.StageTemporary(context.Background(), 7, strings.NewReader("x"), "application/octet-stream", "C:\\private\\report.bin", "run")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.URL)
	router := gin.New()
	NewModule(NewHandler(svc)).RegisterPublicRoutes(router.Group("/api/v1"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, u.Path, nil))
	_, disposition, _ := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if disposition["filename"] != "report.bin" {
		t.Fatal(disposition)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/attachments/fetch/"+strings.Repeat("a", 43), nil))
	if response.Code != http.StatusNotFound || response.Body.Len() != 0 {
		t.Fatal(response.Code, response.Body.String())
	}
}

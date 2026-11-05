package upload

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/attachmentdelivery"
	"github.com/gin-gonic/gin"
)

// Handler serves only capabilities issued by the attachment delivery service.
type Handler struct{ service *attachmentdelivery.Service }

func NewHandler(service *attachmentdelivery.Service) *Handler { return &Handler{service: service} }

func (h *Handler) Fetch(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	content, err := h.service.Open(c.Request.Context(), c.Param("token"), c.Request.Method == http.MethodHead)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, attachmentdelivery.ErrNotFound) {
			status = http.StatusNotFound
		}
		c.Status(status)
		return
	}
	name := filepath.Base(strings.ReplaceAll(content.FileName, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	c.Header("Content-Type", content.MIMEType)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	c.Header("Content-Length", strconv.FormatInt(content.SizeBytes, 10))
	c.Status(http.StatusOK)
	if content.Reader == nil {
		return
	}
	defer content.Reader.Close()
	// Fixed memory for all backends; never materialize an object for delivery.
	_, _ = io.CopyBuffer(c.Writer, io.LimitReader(content.Reader, content.SizeBytes), make([]byte, 32<<10))
}

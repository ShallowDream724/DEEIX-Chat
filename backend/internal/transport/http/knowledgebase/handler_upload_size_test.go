package knowledgebase

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestUploadRequestCeilingIncludesCategoryOverrides(t *testing.T) {
	h := &Handler{cfg: config.NewRuntime(config.Config{MaxUploadFileBytes: 20, FileImageMaxBytes: 10, FileDocMaxBytes: 50})}
	if got := h.maxUploadRequestBytes(); got != 50+multipartUploadOverheadBytes {
		t.Fatalf("request ceiling = %d, want %d", got, 50+multipartUploadOverheadBytes)
	}
}

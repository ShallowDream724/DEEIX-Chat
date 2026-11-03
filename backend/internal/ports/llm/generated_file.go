package llm

import (
	"context"
	"io"
)

// GeneratedFile is an explicitly cited file produced by a provider's hosted tool.
// ID is opaque to application callers and is resolved only by its provider adapter.
type GeneratedFile struct {
	ID          string
	FileName    string
	SourceLinks []string
}

// GeneratedFileContent owns a streaming response. The caller must close Body.
// SizeBytes is -1 when the provider did not report a content length.
type GeneratedFileContent struct {
	Body      io.ReadCloser
	SizeBytes int64
	MimeType  string
}

// GeneratedFileReader is optional so adapters without hosted file output do not
// need to implement a download operation.
type GeneratedFileReader interface {
	OpenGeneratedFile(context.Context, RouteConfig, GeneratedFile) (*GeneratedFileContent, error)
}

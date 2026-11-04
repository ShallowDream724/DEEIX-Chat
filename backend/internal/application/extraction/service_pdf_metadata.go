package extraction

import (
	"context"
	"strings"
)

// ProbeStoredPDFPageCount reads the PDF page tree using the existing builtin
// parser. It neither extracts text nor invokes any OCR or remote model, and does
// not depend on the background extraction job having completed.
func (s *Service) ProbeStoredPDFPageCount(ctx context.Context, storagePath string) (pages int, err error) {
	if s == nil || s.storeProvider == nil || strings.TrimSpace(storagePath) == "" {
		return 0, ErrInvalidStoredFilePath
	}
	store, err := s.storeProvider.Open(ctx)
	if err != nil {
		return 0, err
	}
	path, cleanup, err := store.Materialize(ctx, storagePath)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	return s.ProbeTemporaryPDFPageCount(ctx, path)
}

// ProbeTemporaryPDFPageCount reads metadata from an already staged local file.
func (s *Service) ProbeTemporaryPDFPageCount(ctx context.Context, path string) (pages int, err error) {
	if s == nil || s.factories.Builtin == nil || strings.TrimSpace(path) == "" {
		return 0, ErrInvalidStoredFilePath
	}
	defer func() {
		if recover() != nil {
			pages = 0
			err = ErrParserPanicked
		}
	}()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.detectPDFPageCount(path), nil
}

package extraction

import (
	"context"
	"strings"
)

// ProbeStoredPDFPageCount reads the PDF page tree using the existing builtin
// parser. It neither extracts text nor invokes any OCR or remote model, and does
// not depend on the background extraction job having completed.
func (s *Service) ProbeStoredPDFPageCount(ctx context.Context, storagePath string) (pages int, err error) {
	if s == nil || s.storeProvider == nil || s.factories.Builtin == nil || strings.TrimSpace(storagePath) == "" {
		return 0, ErrInvalidStoredFilePath
	}
	defer func() {
		if recover() != nil {
			pages = 0
			err = ErrParserPanicked
		}
	}()
	store, err := s.storeProvider.Open(ctx)
	if err != nil {
		return 0, err
	}
	path, cleanup, err := store.Materialize(ctx, storagePath)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.detectPDFPageCount(path), nil
}

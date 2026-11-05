package upload

import (
	"archive/tar"
	"bytes"
	"errors"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestCommonAttachmentsUseSamePolicyForStoredAndTemporaryUploads(t *testing.T) {
	var tarContent bytes.Buffer
	w := tar.NewWriter(&tarContent)
	if err := w.WriteHeader(&tar.Header{Name: "notes.txt", Mode: 0600, Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mime string
		content    []byte
	}{
		{"archive.zip", "application/zip", []byte("PK\x03\x04")},
		{"empty.zip", "application/zip", []byte("PK\x05\x06")},
		{"archive.7z", "application/x-7z-compressed", []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}},
		{"archive.rar", "application/vnd.rar", []byte("Rar!\x1a\x07\x00")},
		{"archive-v5.rar", "application/vnd.rar", []byte("Rar!\x1a\x07\x01\x00")},
		{"archive.tar", "application/x-tar", tarContent.Bytes()},
		{"archive.tar.gz", "application/gzip", []byte{0x1f, 0x8b, 0x08}},
		{"archive.tgz", "application/gzip", []byte{0x1f, 0x8b, 0x08}},
		{"archive.bz2", "application/x-bzip2", []byte("BZh9")},
		{"archive.xz", "application/x-xz", []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}},
		{"archive.zst", "application/zstd", []byte{0x28, 0xb5, 0x2f, 0xfd}},
		// Keep the existing net/http text sniffing behavior for legacy encodings.
		{"notes-gbk.txt", "text/plain", []byte{0xd6, 0xd0, 0xce, 0xc4}},
	}
	for _, ext := range []string{"tex", "bib", "cls", "sty", "ipynb", "jsonl", "ndjson", "tsv", "srt", "vtt", "log"} {
		tests = append(tests, struct {
			name, mime string
			content    []byte
		}{"notes." + ext, "text/plain", []byte("example UTF-8 文本\n")})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, store := newUploadTestRepo(), newUploadTestStore()
			service := newUploadTestService(repo, store)
			cfg := service.cfg.Snapshot()
			cfg.FileAllowedMIMETypes = tt.mime
			service.cfg.Store(cfg)
			stored, err := service.UploadFile(t.Context(), UploadFileInput{UserID: 1, FileName: tt.name, Reader: bytes.NewReader(tt.content)})
			if err != nil {
				t.Fatalf("stored upload: %v", err)
			}
			if stored.File.DetectedMIME != tt.mime {
				t.Fatalf("stored MIME %q, want %q", stored.File.DetectedMIME, tt.mime)
			}
			temporary, err := service.PrepareTemporaryFile(t.Context(), TemporaryFileInput{FileName: tt.name, Reader: bytes.NewReader(tt.content)})
			if err != nil {
				t.Fatalf("temporary upload: %v", err)
			}
			defer temporary.Cleanup()
			if temporary.DetectedMIME != tt.mime {
				t.Fatalf("temporary MIME %q, want %q", temporary.DetectedMIME, tt.mime)
			}
			cfg.FileAllowedMIMETypes = "application/pdf"
			service.cfg.Store(cfg)
			if _, err := service.UploadFile(t.Context(), UploadFileInput{UserID: 1, FileName: tt.name, Reader: bytes.NewReader(tt.content)}); err == nil {
				t.Fatal("stored upload bypassed allowlist")
			}
			if _, err := service.PrepareTemporaryFile(t.Context(), TemporaryFileInput{FileName: tt.name, Reader: bytes.NewReader(tt.content)}); err == nil {
				t.Fatal("temporary upload bypassed allowlist")
			}
		})
	}
}

func TestAttachmentNamesCannotPromoteBinaryOrExecutableContent(t *testing.T) {
	blocked := errors.New("blocked")
	dangerous := errors.New("dangerous")
	repo, store := newUploadTestRepo(), newUploadTestStore()
	service := NewServiceWithRuntime(config.NewRuntime(config.Config{MaxUploadFileBytes: 1024, FileAllowedMIMETypes: "application/zip,text/plain"}), repo, nil, Hooks{}, ErrorSet{MIMEBlocked: blocked, DangerousMIMEType: dangerous}, "")
	service.SetObjectStoreProvider(uploadTestStoreProvider{store: store})
	for _, name := range []string{"fake.zip", "fake.7z", "fake.tar.gz", "fake.tex", "fake.ipynb", "fake.json", "fake.js"} {
		for _, content := range [][]byte{{0, 1, 2, 3}, []byte("MZfake executable"), []byte("\x7fELFfake executable")} {
			_, err := service.PrepareTemporaryFile(t.Context(), TemporaryFileInput{FileName: name, MimeType: "application/zip", Reader: bytes.NewReader(content)})
			if !errors.Is(err, blocked) && !errors.Is(err, dangerous) {
				t.Fatalf("%s binary accepted or unexpected error: %v", name, err)
			}
			_, err = service.UploadFile(t.Context(), UploadFileInput{UserID: 1, FileName: name, MimeType: "application/zip", Reader: bytes.NewReader(content)})
			if !errors.Is(err, blocked) && !errors.Is(err, dangerous) {
				t.Fatalf("%s stored binary accepted or unexpected error: %v", name, err)
			}
		}
	}
	if got := store.objectCount(); got != 0 {
		t.Fatalf("rejected objects not cleaned up: %d", got)
	}
	if got := detectContentMIME([]byte("PK\x03\x04"), "text/plain", "renamed.tex"); got != "application/zip" {
		t.Fatalf("archive signature lost to text extension: %q", got)
	}
	for _, name := range []string{"renamed.tex", "renamed.pdf", "renamed.csv"} {
		if got := inferFileCategory("application/zip", name); got != fileCategoryUnknown {
			t.Fatalf("archive %s incorrectly categorized as %q", name, got)
		}
	}
	if !isAllowedMIME("application/vnd.rar", config.Config{FileAllowedMIMETypes: "application/x-rar-compressed"}) {
		t.Fatal("RAR alias not recognized in allowlist")
	}
	for _, sample := range []struct {
		content []byte
		want    string
	}{
		{[]byte("%PDF-1.7"), "application/pdf"},
		{[]byte("\x89PNG\r\n\x1a\n"), "image/png"},
	} {
		if got := detectContentMIME(sample.content, "application/zip", "renamed.zip"); got != sample.want {
			t.Fatalf("signature lost to archive extension: got %q, want %q", got, sample.want)
		}
	}
}

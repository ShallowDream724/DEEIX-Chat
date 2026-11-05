package filetype

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestDetectBinaryMIME(t *testing.T) {
	var tarContent bytes.Buffer
	writer := tar.NewWriter(&tarContent)
	if err := writer.WriteHeader(&tar.Header{Name: "notes.txt", Size: 0, Mode: 0600, Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{"zip", []byte("PK\x03\x04"), "application/zip"},
		{"empty zip", []byte("PK\x05\x06"), "application/zip"},
		{"split zip", []byte("PK\x07\x08"), "application/zip"},
		{"7z", []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}, "application/x-7z-compressed"},
		{"rar4", []byte("Rar!\x1a\x07\x00"), "application/vnd.rar"},
		{"rar5", []byte("Rar!\x1a\x07\x01\x00"), "application/vnd.rar"},
		{"ustar", tarContent.Bytes()[:512], "application/x-tar"},
		{"gzip", []byte{0x1f, 0x8b, 0x08}, "application/gzip"},
		{"bzip2", []byte("BZh9"), "application/x-bzip2"},
		{"xz", []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}, "application/x-xz"},
		{"zstd", []byte{0x28, 0xb5, 0x2f, 0xfd}, "application/zstd"},
		{"exe", []byte("MZPK\x03\x04"), "application/x-msdownload"},
		{"elf", []byte("\x7fELFPK\x03\x04"), "application/x-elf"},
		{"text", []byte("example archive.zip"), ""},
		{"unknown binary", []byte{0, 1, 2, 3}, ""},
		{"partial 7z", []byte{'7', 'z', 0xbc}, ""},
		{"invalid bzip level", []byte("BZh0"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectBinaryMIME(tt.content); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	invalidTar := append([]byte(nil), tarContent.Bytes()[:512]...)
	invalidTar[0]++
	if got := DetectBinaryMIME(invalidTar); got != "" {
		t.Fatalf("tar with bad checksum detected as %q", got)
	}
	if got := DetectBinaryMIME(tarContent.Bytes()[:511]); got != "" {
		t.Fatalf("truncated tar detected as %q", got)
	}
}

func TestNormalizeArchiveMIME(t *testing.T) {
	for raw, want := range map[string]string{
		" APPLICATION/X-ZIP-COMPRESSED; charset=binary ": "application/zip",
		"application/x-rar-compressed":                   "application/vnd.rar",
		"application/x-gzip":                             "application/gzip",
		"application/bzip2":                              "application/x-bzip2",
		"application/x-zstd":                             "application/zstd",
		"application/octet-stream":                       "application/octet-stream",
	} {
		if got := NormalizeArchiveMIME(raw); got != want {
			t.Fatalf("%q: got %q, want %q", raw, got, want)
		}
	}
}

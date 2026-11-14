package filetype

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
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

func TestDetectEbookMIME(t *testing.T) {
	for _, descriptor := range []bool{false, true} {
		content := epubHeaderFixture(t, descriptor)
		if got := DetectBinaryMIME(content); got != "application/epub+zip" {
			t.Fatalf("EPUB (data descriptor %t): got %q", descriptor, got)
		}
	}
	valid := epubHeaderFixture(t, false)
	for _, tt := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"compressed mimetype", func(b []byte) { b[8] = 8 }},
		{"encrypted mimetype", func(b []byte) { b[6] |= 1 }},
		{"strong encryption", func(b []byte) { b[6] |= 64 }},
		{"extra field", func(b []byte) { b[28] = 1 }},
		{"different first entry", func(b []byte) { b[30] = 'M' }},
		{"different mimetype", func(b []byte) { b[38] = 'A' }},
		{"longer filename", func(b []byte) { b[26]++ }},
		{"trailing mimetype data", func(b []byte) { b[18]++; b[22]++ }},
		{"inconsistent sizes", func(b []byte) { b[22]++ }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			content := append([]byte(nil), valid...)
			tt.mutate(content)
			if got := DetectBinaryMIME(content); got != "application/zip" {
				t.Fatalf("invalid EPUB must remain ZIP, got %q", got)
			}
		})
	}
	for length := 4; length < 58; length++ {
		if got := DetectBinaryMIME(valid[:length]); got != "application/zip" {
			t.Fatalf("truncated EPUB (%d bytes): got %q", length, got)
		}
	}
	withDescriptor := epubHeaderFixture(t, true)
	if got := DetectBinaryMIME(withDescriptor[:58]); got != "application/zip" {
		t.Fatalf("missing data descriptor: got %q", got)
	}
	binary.LittleEndian.PutUint32(withDescriptor[66:70], 21)
	if got := DetectBinaryMIME(withDescriptor); got != "application/zip" {
		t.Fatalf("incorrect data descriptor size: got %q", got)
	}

	mobi := make([]byte, 78)
	copy(mobi[60:68], "BOOKMOBI")
	if got := DetectBinaryMIME(mobi); got != "application/x-mobipocket-ebook" {
		t.Fatalf("MOBI: got %q", got)
	}
	if got := DetectBinaryMIME(mobi[:77]); got != "" {
		t.Fatalf("truncated PalmDB header: got %q", got)
	}
	mobi[60] = 'b'
	if got := DetectBinaryMIME(mobi); got != "" {
		t.Fatalf("invalid PalmDB type: got %q", got)
	}
	copy(mobi[60:68], "BOOKMOBI")
	copy(mobi, "MZ")
	if got := DetectBinaryMIME(mobi); got != "application/x-msdownload" {
		t.Fatalf("executable signature must win over MOBI marker: got %q", got)
	}
}

func epubHeaderFixture(t *testing.T, descriptor bool) []byte {
	t.Helper()
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	mimetype := []byte("application/epub+zip")
	header := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	if descriptor {
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(mimetype); err != nil {
			t.Fatal(err)
		}
	} else {
		header.CRC32 = crc32.ChecksumIEEE(mimetype)
		header.CompressedSize64 = uint64(len(mimetype))
		header.UncompressedSize64 = uint64(len(mimetype))
		entry, err := writer.CreateRaw(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(mimetype); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
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

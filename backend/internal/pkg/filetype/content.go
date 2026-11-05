package filetype

import (
	"bytes"
	"strconv"
	"strings"
)

// NormalizeArchiveMIME canonicalizes common browser/platform aliases without
// broadening the upload allowlist to generic binary content.
func NormalizeArchiveMIME(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	switch value {
	case "application/x-zip", "application/x-zip-compressed":
		return "application/zip"
	case "application/x-rar", "application/x-rar-compressed":
		return "application/vnd.rar"
	case "application/x-gzip", "application/gzipped", "application/x-gunzip":
		return "application/gzip"
	case "application/bzip2", "application/x-bzip":
		return "application/x-bzip2"
	case "application/x-zstd":
		return "application/zstd"
	default:
		return value
	}
}

// IsArchiveMIME identifies the compressed/container types recognized here.
func IsArchiveMIME(value string) bool {
	switch NormalizeArchiveMIME(value) {
	case "application/zip", "application/x-7z-compressed", "application/vnd.rar", "application/x-tar",
		"application/gzip", "application/x-bzip2", "application/x-xz", "application/zstd":
		return true
	default:
		return false
	}
}

// DetectBinaryMIME recognizes bounded file headers only. It never decompresses
// archives or trusts a file name. An empty result leaves generic sniffing to
// the caller. Executable signatures must win over archive-like suffixes.
func DetectBinaryMIME(header []byte) string {
	switch {
	case bytes.HasPrefix(header, []byte("MZ")):
		return "application/x-msdownload"
	case bytes.HasPrefix(header, []byte{0x7f, 'E', 'L', 'F'}):
		return "application/x-elf"
	case bytes.HasPrefix(header, []byte("PK\x03\x04")), bytes.HasPrefix(header, []byte("PK\x05\x06")), bytes.HasPrefix(header, []byte("PK\x07\x08")):
		return "application/zip"
	case bytes.HasPrefix(header, []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}):
		return "application/x-7z-compressed"
	case bytes.HasPrefix(header, []byte("Rar!\x1a\x07\x00")), bytes.HasPrefix(header, []byte("Rar!\x1a\x07\x01\x00")):
		return "application/vnd.rar"
	case bytes.HasPrefix(header, []byte{0x1f, 0x8b, 0x08}):
		return "application/gzip"
	case len(header) >= 4 && bytes.HasPrefix(header, []byte("BZh")) && header[3] >= '1' && header[3] <= '9':
		return "application/x-bzip2"
	case bytes.HasPrefix(header, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		return "application/x-xz"
	case bytes.HasPrefix(header, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return "application/zstd"
	case isUSTARHeader(header):
		return "application/x-tar"
	default:
		return ""
	}
}

func isUSTARHeader(header []byte) bool {
	if len(header) < 512 || !bytes.Equal(header[257:262], []byte("ustar")) ||
		(header[262] != 0 && header[262] != ' ') || header[0] == 0 {
		return false
	}
	checksum, err := strconv.ParseUint(strings.Trim(string(header[148:156]), " \x00"), 8, 64)
	if err != nil {
		return false
	}
	var sum uint64
	for index, value := range header[:512] {
		if index >= 148 && index < 156 {
			sum += ' '
		} else {
			sum += uint64(value)
		}
	}
	return sum == checksum
}

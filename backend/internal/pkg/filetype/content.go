package filetype

import (
	"bytes"
	"encoding/binary"
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
	case isEPUBHeader(header):
		return "application/epub+zip"
	case bytes.HasPrefix(header, []byte("PK\x03\x04")), bytes.HasPrefix(header, []byte("PK\x05\x06")), bytes.HasPrefix(header, []byte("PK\x07\x08")):
		return "application/zip"
	case len(header) >= 78 && bytes.Equal(header[60:68], []byte("BOOKMOBI")):
		return "application/x-mobipocket-ebook"
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

// isEPUBHeader checks the OCF identification entry, not the full publication.
// The first ZIP entry must be the unencrypted, stored mimetype file with no
// extra field. A ZIP data descriptor, when used, also fits in the sniffed header.
func isEPUBHeader(header []byte) bool {
	const contentEnd = 30 + len("mimetype") + len("application/epub+zip")
	const mimeSize = uint32(len("application/epub+zip"))
	if len(header) < contentEnd || !bytes.HasPrefix(header, []byte("PK\x03\x04")) {
		return false
	}
	flags := binary.LittleEndian.Uint16(header[6:8])
	if flags&0x2041 != 0 || binary.LittleEndian.Uint16(header[8:10]) != 0 ||
		binary.LittleEndian.Uint16(header[26:28]) != uint16(len("mimetype")) ||
		binary.LittleEndian.Uint16(header[28:30]) != 0 ||
		!bytes.Equal(header[30:contentEnd], []byte("mimetypeapplication/epub+zip")) {
		return false
	}
	compressed := binary.LittleEndian.Uint32(header[18:22])
	uncompressed := binary.LittleEndian.Uint32(header[22:26])
	if flags&8 == 0 {
		return compressed == mimeSize && uncompressed == mimeSize
	}
	if (compressed != 0 && compressed != mimeSize) || (uncompressed != 0 && uncompressed != mimeSize) {
		return false
	}
	descriptor := header[contentEnd:]
	if bytes.HasPrefix(descriptor, []byte("PK\x07\x08")) {
		descriptor = descriptor[4:]
	}
	return len(descriptor) >= 12 && binary.LittleEndian.Uint32(descriptor[4:8]) == mimeSize &&
		binary.LittleEndian.Uint32(descriptor[8:12]) == mimeSize
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

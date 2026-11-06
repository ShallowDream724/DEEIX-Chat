package llm

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

// An image tool result can omit output_format. Sniff a fixed-size header rather
// than decoding a second full image just to classify the result's metadata.
func openAIImageMIMETypeFromBase64(outputFormat, encoded string) string {
	if strings.TrimSpace(outputFormat) != "" {
		return openAIImageMIMEType(outputFormat)
	}
	encoded = strings.TrimSpace(encoded)
	if strings.HasPrefix(encoded, "data:") {
		if _, data, ok := strings.Cut(encoded, ","); ok {
			encoded = strings.TrimSpace(data)
		}
	}
	// Sixteen base64 characters contain the twelve bytes needed for all
	// supported raster signatures, including the RIFF/WEBP pair.
	encoded = encoded[:min(len(encoded), 16)]
	var header [12]byte
	n, _ := io.ReadFull(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), header[:])
	switch detected := http.DetectContentType(header[:n]); detected {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return detected
	default:
		return openAIImageMIMEType(outputFormat)
	}
}

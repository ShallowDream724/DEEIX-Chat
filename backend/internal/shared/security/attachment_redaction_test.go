package security

import (
	"net/url"
	"strings"
	"testing"
)

func TestRedactAttachmentFetchURLs(t *testing.T) {
	token := strings.Repeat("a", 43)
	address := "https://files.example/api/v1/attachments/fetch/" + token
	for _, input := range []string{
		address,
		"GET /api/v1/attachments/fetch/" + token + "?download=1",
		strings.ReplaceAll(address, "/", `\/`),
		strings.ReplaceAll(address, "/", `\u002f`),
		"next=" + url.QueryEscape(address),
		"failed: " + address + " and " + address,
	} {
		got := RedactAttachmentFetchURLs(input)
		if strings.Contains(got, token) || !strings.Contains(got, "REDACTED") {
			t.Fatalf("credential survived: %q", got)
		}
	}
	ordinary := "https://files.example/api/v1/files/123?download=1"
	if got := RedactAttachmentFetchURLs(ordinary); got != ordinary {
		t.Fatalf("ordinary URL changed: %s", got)
	}
}

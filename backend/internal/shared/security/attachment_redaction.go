package security

import (
	"regexp"
	"strings"
)

const attachmentFetchPathPrefix = "/api/v1/attachments/fetch/"

// Match URL paths in plain text, escaped JSON, and URL-encoded query values.
// Redact any token segment, including malformed tokens, before recording it.
var attachmentFetchCredentialPattern = regexp.MustCompile(`(?i)((?:/|\\/|%2f|\\u002f)api(?:/|\\/|%2f|\\u002f)v1(?:/|\\/|%2f|\\u002f)attachments(?:/|\\/|%2f|\\u002f)fetch(?:/|\\/|%2f|\\u002f))[A-Za-z0-9_%~-]+`)

// RedactAttachmentFetchURLs removes bearer credentials from attachment fetch
// URLs in diagnostic copies only. Callers must keep actual requests unchanged.
func RedactAttachmentFetchURLs(value string) string {
	return attachmentFetchCredentialPattern.ReplaceAllString(value, "${1}REDACTED")
}

// IsAttachmentFetchPath identifies credential-bearing inbound request paths so
// instrumentation can avoid collecting the original URL altogether.
func IsAttachmentFetchPath(path string) bool {
	return strings.HasPrefix(path, attachmentFetchPathPrefix)
}

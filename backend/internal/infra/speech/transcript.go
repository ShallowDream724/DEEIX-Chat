package speech

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func joinTranscript(parts []string) string {
	var out strings.Builder
	var last rune
	for _, part := range parts {
		if part == "" {
			continue
		}
		first, _ := utf8.DecodeRuneInString(part)
		word := func(r rune) bool { return unicode.Is(unicode.Latin, r) || unicode.IsDigit(r) }
		if out.Len() > 0 && word(last) && word(first) {
			out.WriteByte(' ')
		}
		out.WriteString(part)
		last, _ = utf8.DecodeLastRuneInString(part)
	}
	return out.String()
}

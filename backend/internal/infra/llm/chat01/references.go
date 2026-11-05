package chat01

import "strings"

// NormalizeReferences is a presentation helper for complete answer blocks. The
// caller supplies only verified ref-to-URL mappings. Missing internal references
// become readable placeholders; original source stays available in Blocks.
// Code literals are preserved. It does not discover, guess or fetch source URLs.
func NormalizeReferences(text string, sources map[string]string) string {
	const open = "cite"
	var out strings.Builder
	var fence byte
	fenceLength, inline := 0, 0
	lineStart := true
	for i := 0; i < len(text); {
		if lineStart {
			indent := 0
			for i+indent < len(text) && indent < 4 && text[i+indent] == ' ' {
				indent++
			}
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			} else {
				end++
			}
			if indent <= 3 && i+indent < len(text) && (text[i+indent] == '`' || text[i+indent] == '~') {
				ch := text[i+indent]
				n := run(text[i+indent:], ch)
				if n >= 3 {
					if fence == 0 {
						fence = ch
						fenceLength = n
					} else if fence == ch && n >= fenceLength && strings.TrimSpace(text[i+indent+n:i+end]) == "" {
						fence = 0
					}
					out.WriteString(text[i : i+end])
					i += end
					lineStart = true
					continue
				}
			}
			if fence != 0 || indent >= 4 || text[i] == '\t' {
				out.WriteString(text[i : i+end])
				i += end
				lineStart = true
				continue
			}
		}
		lineStart = text[i] == '\n'
		if text[i] == '\\' {
			n := min(2, len(text)-i)
			out.WriteString(text[i : i+n])
			i += n
			continue
		}
		if text[i] == '`' {
			n := run(text[i:], '`')
			if inline == 0 {
				inline = n
			} else if inline == n {
				inline = 0
			}
			out.WriteString(text[i : i+n])
			i += n
			continue
		}
		if inline == 0 && strings.HasPrefix(text[i:], open) {
			end := strings.Index(text[i+len(open):min(len(text), i+4096)], componentClose)
			if end >= 0 {
				refs := strings.Split(text[i+len(open):i+len(open)+end], "")
				seen := map[string]bool{}
				mapped := false
				for _, ref := range refs {
					if raw, ok := sources[ref]; ok && validSourceURL(raw) && !strings.ContainsAny(raw, "()<>\r\n") && !seen[raw] {
						out.WriteString("[来源](")
						out.WriteString(raw)
						out.WriteByte(')')
						seen[raw] = true
						mapped = true
					}
				}
				if !mapped {
					out.WriteString("[来源未提供链接]")
				}
				i += len(open) + end + len(componentClose)
				continue
			}
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}

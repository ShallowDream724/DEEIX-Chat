package llm

import (
	"strings"
	"unicode/utf8"

	chat01parse "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm/chat01"
)

const chat01PresentationLimit = 8192

// Presentation retains only an undecided Markdown/reference token, never a whole
// paragraph. Delimiter runs are counted separately so even unfinished code is
// streamed immediately. Oversized ambiguous tokens stay literal through EOL.
type chat01AnswerPresentation struct {
	pending          string
	started          bool
	prefix           bool
	indent           int
	literalLine      bool
	literalRemainder bool
	escaped          bool
	inline           int
	fence            byte
	fenceLength      int
	closingFence     bool
	run              byte
	runLength        int
	runAtPrefix      bool
}

func (p *chat01AnswerPresentation) feed(text string, final bool) string {
	if !p.started {
		p.started, p.prefix = true, true
	}
	p.pending += text
	var out strings.Builder
	emit := func(n int, value string) {
		raw := p.pending[:n]
		out.WriteString(value)
		for _, ch := range raw {
			if ch == '\n' {
				if p.closingFence {
					p.fence, p.fenceLength = 0, 0
				}
				p.prefix, p.indent = true, 0
				p.literalLine, p.literalRemainder, p.closingFence = false, false, false
			} else {
				if ch != ' ' && ch != '\t' && ch != '\r' {
					p.closingFence = false
				}
				if p.prefix {
					switch ch {
					case ' ':
						p.indent++
						p.literalLine = p.indent >= 4
					case '\t':
						p.literalLine, p.prefix = true, false
					default:
						p.prefix = false
					}
				}
			}
		}
		p.pending = p.pending[n:]
	}
	finishRun := func() {
		if p.runAtPrefix && p.runLength >= 3 && p.inline == 0 {
			if p.fence == 0 {
				p.fence, p.fenceLength = p.run, p.runLength
			} else if p.fence == p.run && p.runLength >= p.fenceLength {
				// A closing fence must contain only whitespace after its run.
				p.closingFence = true
			}
		} else if p.fence == 0 && p.run == '`' {
			if p.inline == 0 {
				p.inline = p.runLength
			} else if p.inline == p.runLength {
				p.inline = 0
			}
		}
		p.run, p.runLength = 0, 0
	}
	for len(p.pending) > 0 {
		s := p.pending
		if p.run != 0 {
			n := 0
			for n < len(s) && s[n] == p.run {
				n++
			}
			if n > 0 {
				p.runLength += n
				emit(n, s[:n])
			}
			if len(p.pending) == 0 && !final {
				break
			}
			finishRun()
			continue
		}
		if !utf8.FullRuneInString(s) && !final {
			break
		}
		_, width := utf8.DecodeRuneInString(s)
		if p.escaped {
			p.escaped = false
			emit(width, s[:width])
			continue
		}
		if (!p.literalLine || p.inline != 0) && ((s[0] == '`' && (p.fence == 0 || p.prefix)) || (s[0] == '~' && p.prefix && p.inline == 0)) {
			p.run, p.runAtPrefix = s[0], p.prefix
			continue
		}
		if p.fence != 0 || p.inline != 0 || p.literalLine || p.literalRemainder {
			emit(width, s[:width])
			continue
		}
		if s[0] == '\\' {
			p.escaped = true
			emit(1, s[:1])
			continue
		}
		if strings.HasPrefix(s, "") || strings.HasPrefix("", s) {
			end := strings.Index(s, "")
			lineEnd := strings.IndexByte(s, '\n')
			if end >= 0 && end+len("") <= chat01PresentationLimit && (lineEnd < 0 || end < lineEnd) {
				end += len("")
				emit(end, chat01parse.NormalizeReferences(s[:end], nil))
				continue
			}
			if len(s) < chat01PresentationLimit && !final && !strings.Contains(s, "\n") {
				break
			}
			if lineEnd >= 0 && lineEnd < chat01PresentationLimit {
				emit(lineEnd+1, s[:lineEnd+1])
				continue
			}
			if final && len(s) < chat01PresentationLimit {
				emit(len(s), s)
				continue
			}
			if len(s) >= chat01PresentationLimit {
				p.literalRemainder = true
			}
		}
		if s[0] == '[' || s[0] == '!' {
			n, label, sandbox, wait := chat01PresentationLink(s, final)
			if wait {
				break
			}
			if n > 0 {
				if !sandbox && n < len(s) && s[n] == '\n' {
					// Rejected link text may itself open a multiline code span.
					// Keep it literal while still following its code delimiters.
					p.literalRemainder = true
					continue
				}
				value := s[:n]
				if sandbox {
					value = label + "（上游返回的下载地址无效）"
				}
				emit(n, value)
				continue
			}
			if len(s) >= chat01PresentationLimit && (s[0] == '[' || strings.HasPrefix(s, "![")) {
				p.literalRemainder = true
			}
		}
		emit(width, s[:width])
	}
	if final && p.run != 0 {
		finishRun()
	}
	// A short suffix must not retain the backing storage of a large input chunk.
	if len(p.pending) == 0 {
		p.pending = ""
	} else {
		p.pending = strings.Clone(p.pending)
	}
	return out.String()
}

// A bounded recognizer for single-line inline links. Anything that cannot be
// identified as a complete link is retained verbatim, including escaped syntax.
func chat01PresentationLink(s string, final bool) (n int, label string, sandbox, wait bool) {
	start := 0
	if s[0] == '!' {
		if len(s) == 1 {
			return 0, "", false, !final
		}
		if s[1] != '[' {
			return
		}
		start = 1
	}
	limit := min(len(s), chat01PresentationLimit)
	depth, endLabel := 1, -1
	for i := start + 1; i < limit; i++ {
		if s[i] == '\n' {
			return i, "", false, false
		}
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '[' {
			depth++
		}
		if s[i] == ']' {
			depth--
			if depth == 0 {
				endLabel = i
				break
			}
		}
	}
	if endLabel < 0 {
		if final && len(s) < chat01PresentationLimit {
			return len(s), "", false, false
		}
		return 0, "", false, !final && len(s) < chat01PresentationLimit
	}
	if endLabel+1 >= limit {
		if final && len(s) < chat01PresentationLimit {
			return len(s), "", false, false
		}
		return 0, "", false, !final && len(s) < chat01PresentationLimit
	}
	if s[endLabel+1] != '(' {
		return endLabel + 1, "", false, false
	}
	depth = 1
	for i := endLabel + 2; i < limit; i++ {
		if s[i] == '\n' {
			return i, "", false, false
		}
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '(' {
			depth++
		}
		if s[i] == ')' {
			depth--
			if depth == 0 {
				sandbox = chat01PresentationSandboxDestination(s[endLabel+2 : i])
				return i + 1, s[start+1 : endLabel], sandbox, false
			}
		}
	}
	if final && len(s) < chat01PresentationLimit {
		return len(s), "", false, false
	}
	return 0, "", false, !final && len(s) < chat01PresentationLimit
}

func chat01PresentationSandboxDestination(raw string) bool {
	raw = strings.TrimSpace(raw)
	address, rest := raw, ""
	if strings.HasPrefix(raw, "<") {
		end := strings.IndexByte(raw, '>')
		if end < 0 {
			return false
		}
		address, rest = raw[1:end], raw[end+1:]
		if strings.ContainsAny(address, "<>\r\n") {
			return false
		}
	} else if end := strings.IndexAny(raw, " \t"); end >= 0 {
		address, rest = raw[:end], raw[end:]
	}
	if !strings.HasPrefix(address, "sandbox:/") || strings.ContainsAny(address, "<>\"'\r\n") {
		return false
	}
	if rest == "" {
		return true
	}
	if rest[0] != ' ' && rest[0] != '\t' {
		return false
	}
	title := strings.TrimSpace(rest)
	if title == "" {
		return true
	}
	if len(title) < 2 {
		return false
	}
	close := title[0]
	if close == '(' {
		close = ')'
	} else if close != '\'' && close != '"' {
		return false
	}
	if title[len(title)-1] != close {
		return false
	}
	for i := 1; i < len(title)-1; i++ {
		if title[i] == '\\' {
			i++
			continue
		}
		if title[i] == close {
			return false
		}
	}
	return true
}

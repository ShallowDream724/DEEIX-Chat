package chat01

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxHeading     = 512
	maxComponent   = 65536
	componentOpen  = "genui"
	componentClose = ""
)

type segment struct {
	Block
	text strings.Builder
}

// Parser is a single response's incremental scanner. It is not concurrent-safe.
// Only a potential short marker, heading, or first two code lines are deferred;
// components stream into their own block rather than an unbounded pending buffer.
type Parser struct {
	pending                     string
	pos                         int
	offset                      int64
	blocks                      []*segment
	deltas                      []Delta
	batch                       strings.Builder
	batchBlock                  *segment
	batchStart                  int64
	mode                        Kind
	tool                        Kind
	section                     int
	lineStart                   bool
	fence                       byte
	fenceLength                 int
	fenceOpening                bool
	inlineTicks                 int
	literalLine, markdownEscape bool
	component                   *segment
	processBoundary             bool
	heredoc                     string
	quote                       string
	escaped, comment            bool
	finished                    bool
}

func NewParser() *Parser { return &Parser{mode: KindAnswer, lineStart: true} }

// Feed accepts actual text deltas, not cumulative snapshots. Empty fragments
// (including transport heartbeats) have no semantic effect.
func (p *Parser) Feed(fragment string) []Delta {
	if p.finished || fragment == "" {
		return nil
	}
	return p.scan(fragment, false)
}

// Finish flushes incomplete literal markers without inventing any missing text.
// It is idempotent and does not infer tool status from transport termination.
func (p *Parser) Finish() []Delta {
	if p.finished {
		return nil
	}
	p.finished = true
	return p.scan("", true)
}

func (p *Parser) Blocks() []Block {
	out := make([]Block, len(p.blocks))
	for i, b := range p.blocks {
		out[i] = b.Block
		out[i].Text = b.text.String()
	}
	return out
}

func (p *Parser) route() Kind {
	if p.tool != "" {
		return p.tool
	}
	return p.mode
}

func (p *Parser) flush() {
	if p.batchBlock == nil {
		return
	}
	b := p.batchBlock
	p.deltas = append(p.deltas, Delta{BlockID: b.ID, Kind: b.Kind, Text: p.batch.String(), Reason: b.Reason, Section: b.Section, Start: p.batchStart, End: b.End, Complete: b.Complete, Oversized: b.Oversized})
	p.batch.Reset()
	p.batchBlock = nil
}

func (p *Parser) emit(kind Kind, n int, reason string) *segment {
	if n <= 0 {
		return nil
	}
	var b *segment
	if len(p.blocks) > 0 {
		b = p.blocks[len(p.blocks)-1]
	}
	if b == nil || b.Kind != kind || b.Reason != reason || b.Section != p.section || kind == KindComponent && b.Complete {
		p.flush()
		b = &segment{Block: Block{ID: fmt.Sprintf("ch01-%d", len(p.blocks)+1), Kind: kind, Reason: reason, Section: p.section, Start: p.offset, End: p.offset}}
		p.blocks = append(p.blocks, b)
	}
	if p.batchBlock != b {
		p.flush()
		p.batchBlock = b
		p.batchStart = p.offset
	}
	value := p.pending[p.pos : p.pos+n]
	b.text.WriteString(value)
	p.batch.WriteString(value)
	b.End += int64(n)
	p.offset += int64(n)
	p.pos += n
	p.lineStart = strings.HasSuffix(value, "\n")
	return b
}

func prefixWait(s string, final bool, candidates ...string) bool {
	if final {
		return false
	}
	for _, marker := range candidates {
		if len(s) < len(marker) && strings.HasPrefix(marker, s) {
			return true
		}
	}
	return false
}

var sectionMarkers = []string{"## Answer:", "## Thought:", "## Python:", "## Shell:", "## Browser:", "**Answer:**", "**Thought:**"}

func (p *Parser) scan(fragment string, final bool) []Delta {
	p.deltas = nil
	// Compact only the small unresolved suffix, never the already consumed reply.
	p.pending = p.pending[p.pos:] + fragment
	p.pos = 0
	for p.pos < len(p.pending) {
		s := p.pending[p.pos:]
		process := p.mode == KindReasoning
		pythonLike := p.tool == KindPython || p.tool == KindCodeFragment
		if !final && !utf8.FullRuneInString(s) {
			break
		}
		_, size := utf8.DecodeRuneInString(s)
		if p.fenceOpening {
			if s[0] == '\n' {
				p.fenceOpening = false
			}
			p.emit(KindControl, size, "code-fence")
			continue
		}
		if p.component != nil {
			// A damaged oversized component may recover at a subsequent explicit
			// section boundary. Until then its contents remain one readable record.
			if p.component.Oversized && p.lineStart {
				if prefixWait(s, final, sectionMarkers...) {
					break
				}
				if hasAnyPrefix(s, sectionMarkers) {
					p.component = nil
					continue
				}
			}
			if prefixWait(s, final, componentClose) {
				break
			}
			n := size
			complete := strings.HasPrefix(s, componentClose)
			if complete {
				n = len(componentClose)
			}
			b := p.emit(KindComponent, n, "upstream-component")
			b.Complete = complete
			b.Oversized = b.End-b.Start > maxComponent
			if complete {
				p.component = nil
			}
			continue
		}
		if p.literalLine {
			if s[0] == '\n' {
				p.literalLine = false
			}
			p.emit(p.route(), size, "")
			continue
		}
		if p.markdownEscape {
			p.markdownEscape = false
			p.emit(p.route(), size, "")
			continue
		}
		if p.inlineTicks > 0 {
			if s[0] == '`' {
				n := run(s, '`')
				if n >= maxHeading {
					p.inlineTicks = 0
					p.literalLine = true
					continue
				}
				if n == len(s) && !final {
					break
				}
				if n == p.inlineTicks {
					p.inlineTicks = 0
				}
				p.emit(p.route(), n, "")
				continue
			}
			p.emit(p.route(), size, "")
			continue
		}
		if (pythonLike || p.tool == KindShell && p.heredoc == "") && p.quote != "" {
			if p.escaped {
				p.escaped = false
				p.emit(p.tool, size, "")
				continue
			}
			if s[0] == '\\' && !(p.tool == KindShell && p.quote == "'") {
				p.escaped = true
				p.emit(p.tool, size, "")
				continue
			}
			if prefixWait(s, final, p.quote) {
				break
			}
			if strings.HasPrefix(s, p.quote) {
				n := len(p.quote)
				p.quote = ""
				p.emit(p.tool, n, "")
				continue
			}
			if s[0] == '\n' && len(p.quote) == 1 && p.tool != KindShell {
				p.quote = ""
			}
			p.emit(p.tool, size, "")
			continue
		}
		if (pythonLike || p.tool == KindShell && p.heredoc == "") && p.comment {
			if s[0] == '\n' {
				p.comment = false
			}
			p.emit(p.tool, size, "")
			continue
		}
		if p.tool == KindCodeFragment && p.lineStart && p.fence == 0 {
			// Captured clipped Python records can end with PY glued to a
			// Markdown activity title. Require a complete single-line title
			// and no expression suffix, so PY**2**3 stays ordinary code.
			if prefixWait(s, final, "PY**") {
				break
			}
			if strings.HasPrefix(s, "PY**") {
				title, wait := singleLineBold(s[2:], final)
				if wait {
					break
				}
				if title {
					p.emit(KindCodeFragment, 2, "")
					p.processBoundary = true
					continue
				}
			}
		}
		if p.lineStart && p.heredoc == "" {
			indent := 0
			for indent < len(s) && indent < 4 && s[indent] == ' ' {
				indent++
			}
			if indent == len(s) && !final && indent < 4 {
				break
			}
			if p.fence == 0 && p.tool == "" && (indent >= 4 || s[0] == '\t') {
				p.literalLine = true
				continue
			}
			if indent <= 3 && indent < len(s) && (s[indent] == '`' || s[indent] == '~') {
				n := run(s[indent:], s[indent])
				// Delimiters beyond the recognizer's limit remain literal. This
				// also bounds an adversarial never-ending run across fragments.
				if n >= maxHeading {
					p.literalLine = true
					continue
				}
				prefix := indent + n
				rest := s[prefix:]
				if rest == "" && !final {
					break
				}
				if p.fence == 0 && n >= 3 {
					p.fence = s[indent]
					p.fenceLength = n
					p.fenceOpening = p.tool != ""
					if p.tool != "" {
						p.emit(KindControl, prefix, "code-fence")
					} else {
						p.emit(p.route(), prefix, "")
					}
					continue
				}
				if p.fence == s[indent] && n >= p.fenceLength {
					end := 0
					for end < len(rest) && end < maxHeading && (rest[end] == ' ' || rest[end] == '\t' || rest[end] == '\r') {
						end++
					}
					if end >= maxHeading {
						p.literalLine = true
						continue
					}
					if end == len(rest) && !final {
						break
					}
					if end == len(rest) || rest[end] == '\n' {
						if end < len(rest) {
							end++
						}
						p.fence = 0
						if p.tool != "" {
							p.emit(KindControl, prefix+end, "code-fence")
						} else {
							p.emit(p.route(), prefix+end, "")
						}
						continue
					}
				}
			}
		}
		markers := []string{"<think>", "</think>"}
		if p.lineStart || (process && p.fence == 0 && p.heredoc == "") {
			markers = append(markers, sectionMarkers...)
		}
		// A tool fence may be truncated. A real section boundary can recover it,
		// while answer/tutorial fences, quoted strings and heredocs own their text.
		if p.heredoc == "" && (p.fence == 0 || (p.tool != "" && p.lineStart)) {
			marker := ""
			for _, v := range markers {
				if strings.HasPrefix(s, v) {
					marker = v
					break
				}
			}
			if marker != "" {
				if !process && (marker == "## Python:" || marker == "## Shell:" || marker == "## Browser:") {
					p.emit(p.route(), size, "")
					continue
				}
				p.emit(KindControl, len(marker), "section-marker")
				switch marker {
				case "<think>", "## Thought:", "**Thought:**":
					p.mode = KindReasoning
					p.tool = ""
					p.fence = 0
				case "</think>", "## Answer:", "**Answer:**":
					p.mode = KindAnswer
					p.tool = ""
					p.fence = 0
				case "## Python:":
					p.tool = KindPython
				case "## Shell:":
					p.tool = KindShell
				case "## Browser:":
					p.tool = KindSearch
				}
				if marker == "## Answer:" || marker == "**Answer:**" {
					p.section++
				}
				p.quote = ""
				p.comment = false
				continue
			}
			if prefixWait(s, final, markers...) {
				break
			}
		}
		if p.heredoc != "" && p.lineStart {
			if prefixWait(s, final, p.heredoc) {
				break
			}
			if strings.HasPrefix(s, p.heredoc) {
				rest := s[len(p.heredoc):]
				if title, wait := singleLineBold(rest, final); wait {
					break
				} else if title {
					n := len(p.heredoc)
					p.heredoc = ""
					p.emit(KindShell, n, "")
					p.processBoundary = true
					continue
				}
				if prefixWait(rest, final, "\r\n", "## Thought:", "## Answer:") || rest == "" && !final {
					break
				}
				if strings.HasPrefix(rest, "\n") || strings.HasPrefix(rest, "\r\n") || strings.HasPrefix(rest, "## Thought:") || strings.HasPrefix(rest, "## Answer:") || rest == "" && final {
					n := len(p.heredoc)
					p.heredoc = ""
					p.emit(KindShell, n, "")
					continue
				}
			}
		}
		if process && p.fence == 0 && p.heredoc == "" {
			if (!pythonLike || p.tool == KindCodeFragment) && (p.lineStart || p.processBoundary) && strings.HasPrefix(s, "**") && !strings.HasPrefix(s, "**Searched ") && !strings.HasPrefix(s, "**Used ") {
				// Headings are single-line. Never search a closing ** past a
				// newline (which previously swallowed a later real answer).
				lineEnd := strings.IndexByte(s, '\n')
				limit := len(s)
				if lineEnd >= 0 {
					limit = lineEnd
				}
				if limit > maxHeading {
					limit = maxHeading
				}
				end := -1
				if limit >= 2 {
					end = strings.Index(s[2:limit], "**")
				}
				if end >= 0 {
					p.tool = ""
					p.quote = ""
					p.emit(KindReasoning, end+4, "")
					p.processBoundary = true
					continue
				}
				if lineEnd < 0 && !final && len(s) < maxHeading {
					break
				}
			}
			if s == "*" && !final || prefixWait(s, final, "bash -lc ") {
				break
			}
			if strings.HasPrefix(s, "bash -lc ") {
				p.tool = KindShell
				p.processBoundary = false
				p.emit(KindShell, len("bash -lc "), "")
				continue
			}
			if p.tool == KindShell && strings.HasPrefix(s, "<<") {
				match := matchHeredoc(s)
				if !final && len(s) < 96 && (match == nil || len(match[0]) == len(s)) {
					break
				}
				if match != nil {
					p.heredoc = match[1]
					p.emit(KindShell, len(match[0]), "")
					continue
				}
			}
			if p.tool == KindShell && s == "<" && !final {
				break
			}
			if p.tool == "" && (p.lineStart || p.processBoundary) && s[0] != '\n' && !strings.HasPrefix(s, "##") && !strings.HasPrefix(s, "**") {
				end := strings.IndexByte(s, '\n')
				if end < 0 {
					if !final && len(s) < maxHeading {
						break
					}
					end = min(len(s), maxHeading)
				}
				if end <= maxHeading && shellHeredocCommandRE.MatchString(s[:end]) {
					if m := matchHeredoc(s[:end]); m != nil {
						p.tool = KindShell
						p.heredoc = m[1]
						p.processBoundary = false
						n := end
						if end < len(s) && s[end] == '\n' {
							n++
						}
						p.emit(KindShell, n, "")
						continue
					}
				}
				if end <= maxHeading && end < len(s) && s[end] == '\n' && codeStrength(s[:end]) > 0 {
					after := ""
					if end < len(s) && s[end] == '\n' {
						after = s[end+1:]
					}
					next := strings.IndexByte(after, '\n')
					if next < 0 {
						if !final && len(after) < maxHeading {
							break
						}
						next = min(len(after), maxHeading)
					}
					if codeStrength(after[:next]) == 2 {
						p.tool = KindCodeFragment
						p.processBoundary = false
						continue
					}
				}
				p.processBoundary = false
			}
			starts := []string{"**Searched ", "**Used ", "Worked for ", `{"uri":"skills://`, "data && python - <<", "bash -lc python - <<"}
			if prefixWait(s, final, starts...) {
				break
			}
			if strings.HasPrefix(s, "**Searched ") || strings.HasPrefix(s, "**Used ") {
				limit := min(len(s), maxHeading)
				if n := strings.IndexByte(s[:limit], '\n'); n >= 0 {
					limit = n
				}
				end := -1
				if limit >= 2 {
					end = strings.Index(s[2:limit], "**")
				}
				if end >= 0 {
					kind := KindToolNote
					if strings.HasPrefix(s, "**Searched ") {
						kind = KindSearch
					}
					p.emit(kind, end+4, "upstream-activity-summary")
					p.tool = ""
					p.quote = ""
					p.processBoundary = true
					continue
				}
				if !final && len(s) < maxHeading && !strings.Contains(s, "\n") {
					break
				}
			}
			if strings.HasPrefix(s, "Worked for ") {
				n := strings.IndexByte(s, '\n')
				if n < 0 {
					if !final && len(s) < 128 {
						break
					}
					n = min(len(s), 128)
				} else {
					n++
				}
				p.emit(KindMeta, n, "upstream-duration")
				continue
			}
			if strings.HasPrefix(s, `{"uri":"skills://`) {
				n := strings.IndexByte(s, '}')
				if n < 0 {
					if !final && len(s) < 4096 {
						break
					}
				} else {
					p.emit(KindToolNote, n+1, "upstream-resource-read")
					continue
				}
			}
			if strings.HasPrefix(s, "data && python - <<") || strings.HasPrefix(s, "bash -lc python - <<") {
				n := strings.IndexByte(s, '\n')
				if n < 0 {
					if !final && len(s) < maxHeading {
						break
					}
					n = min(len(s), maxHeading)
				} else {
					n++
				}
				if m := matchHeredoc(s[:n]); m != nil {
					p.heredoc = m[1]
					p.tool = KindShell
					p.emit(KindShell, n, "")
					continue
				}
			}
		}
		if p.fence == 0 && p.heredoc == "" && strings.HasPrefix(s, componentOpen) {
			p.component = p.emit(KindComponent, len(componentOpen), "upstream-component")
			continue
		}
		if p.fence == 0 && p.heredoc == "" && prefixWait(s, final, componentOpen) {
			break
		}
		if p.fence == 0 && p.tool == "" && p.heredoc == "" {
			if s[0] == '\\' {
				p.markdownEscape = true
				p.emit(p.route(), size, "")
				continue
			}
			if s[0] == '`' {
				n := run(s, '`')
				if n >= maxHeading {
					p.literalLine = true
					continue
				}
				if n == len(s) && !final {
					break
				}
				p.inlineTicks = n
				p.emit(p.route(), n, "")
				continue
			}
		}
		if pythonLike || p.tool == KindShell && p.heredoc == "" {
			if s[0] == '\'' || s[0] == '"' {
				triple := strings.Repeat(s[:1], 3)
				if pythonLike && prefixWait(s, final, triple) {
					break
				}
				p.quote = s[:1]
				if pythonLike && strings.HasPrefix(s, triple) {
					p.quote = triple
				}
				p.emit(p.tool, len(p.quote), "")
				continue
			}
			if s[0] == '#' {
				p.comment = true
			}
		}
		p.emit(p.route(), size, "")
	}
	p.flush()
	// Releasing the consumed chunk avoids retaining a large upstream snapshot.
	if p.pos == len(p.pending) {
		p.pending = ""
		p.pos = 0
	} else if p.pos > 0 {
		p.pending = strings.Clone(p.pending[p.pos:])
		p.pos = 0
	}
	return p.deltas
}

func hasAnyPrefix(s string, values []string) bool {
	for _, v := range values {
		if strings.HasPrefix(s, v) {
			return true
		}
	}
	return false
}
func run(s string, ch byte) int {
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	return n
}

var heredocRE = regexp.MustCompile(`<<-?[ \t]{0,16}['"]?([A-Za-z_][A-Za-z0-9_]{0,63})['"]?`)
var shellHeredocCommandRE = regexp.MustCompile(`(?:^|[;&|][ \t]*)(?:python(?:3)?[ \t]+-[ \t]*<<|cat[ \t]+[^\n]{0,400}<<)`)

func singleLineBold(s string, final bool) (bool, bool) {
	if !strings.HasPrefix(s, "**") {
		return false, prefixWait(s, final, "**")
	}
	lineEnd := strings.IndexByte(s, '\n')
	if lineEnd < 0 && !final && len(s) < maxHeading {
		return false, true
	}
	limit := min(len(s), maxHeading)
	if lineEnd >= 0 && lineEnd < limit {
		limit = lineEnd
	}
	end := strings.Index(s[2:limit], "**")
	if end >= 0 {
		return strings.TrimSpace(s[2+end+2:limit]) == "", false
	}
	return false, lineEnd < 0 && !final && len(s) < maxHeading
}

func matchHeredoc(s string) []string {
	indices := heredocRE.FindStringSubmatchIndex(s)
	if indices == nil {
		return nil
	}
	m := []string{s[indices[0]:indices[1]], s[indices[2]:indices[3]]}
	if indices[1] < len(s) {
		next := s[indices[1]]
		if next == '_' || next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' || next >= '0' && next <= '9' {
			return nil
		}
	}
	return m
}

var strongCodeRE = regexp.MustCompile(`^(?:from [\w.]+ import |import [\w.]+|(?:async )?def \w+\(|class \w+[(:]|assert |(?:with|for|if|elif|while) .+:$|(?:try|else|finally):$|except(?: .+)?:$|print\(|raise \w+|return\b|[\w]+(?:\.[\w]+|\[[^\]\n]+\])*\s*=[^=])`)
var weakCodeRE = regexp.MustCompile(`^(?:['"]?PY['"]?|.*\.py\s*<<['"]?\w+['"]?|[^\s]+\.(?:json|csv|txt)['"]\)\.(?:read|write)\w*\(.*)$`)

func codeStrength(s string) int {
	s = strings.TrimSpace(s)
	if strongCodeRE.MatchString(s) {
		return 2
	}
	if weakCodeRE.MatchString(s) {
		return 1
	}
	return 0
}

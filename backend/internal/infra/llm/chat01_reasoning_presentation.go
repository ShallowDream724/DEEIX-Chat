package llm

import (
	"encoding/json"
	"strings"
)

// Formatting is separate from classification: source blocks keep their bytes,
// while one bounded line buffer removes repeated website chrome from reasoning.
type chat01ReasoningPresentation struct {
	pending string
	literal bool
}

func (p *chat01ReasoningPresentation) feed(text string, final bool) string {
	p.pending += text
	var out strings.Builder
	for len(p.pending) > 0 {
		end := strings.IndexByte(p.pending, '\n')
		if end < 0 && !final && len(p.pending) < 512 {
			break
		}
		if end < 0 {
			if final && !p.literal && len(p.pending) < 512 {
				out.WriteString(chat01ReasoningLine(p.pending))
			} else {
				out.WriteString(p.pending)
			}
			p.pending = ""
			p.literal = !final
			break
		}
		line := p.pending[:end]
		if p.literal || len(line) >= 512 {
			out.WriteString(line)
		} else {
			out.WriteString(chat01ReasoningLine(line))
		}
		out.WriteByte('\n')
		p.literal = false
		p.pending = p.pending[end+1:]
	}
	return out.String()
}

func chat01ReasoningLine(line string) string {
	trimmed := strings.TrimSpace(line)
	var resource struct {
		URI string `json:"uri"`
	}
	if json.Unmarshal([]byte(trimmed), &resource) == nil && strings.HasPrefix(resource.URI, "skills://") {
		return "技能说明：" + strings.TrimPrefix(resource.URI, "skills://")
	}
	hadChrome := false
	for strings.HasPrefix(trimmed, "**Thinking**") {
		trimmed = strings.TrimPrefix(trimmed, "**Thinking**")
		hadChrome = true
	}
	if hadChrome {
		// The website sometimes truncates the next heading's opening. Preserve
		// its remaining words instead of inventing the missing heading text.
		if !strings.HasPrefix(trimmed, "**") {
			trimmed = strings.TrimSuffix(trimmed, "**")
		}
		line = trimmed
	}
	if strings.HasPrefix(line, "**") && strings.HasSuffix(line, "**") {
		line = strings.ReplaceAll(line, "****", "**\n\n**")
	}
	return line
}

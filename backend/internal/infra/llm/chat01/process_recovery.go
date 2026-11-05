package chat01

import (
	"regexp"
	"strings"
)

var durationLineRE = regexp.MustCompile(`^Worked for (?:[0-9]+h(?: [0-9]+m)?(?: [0-9]+s)?|[0-9]+m(?: [0-9]+s)?|[0-9]+s)$`)

// These controls occur after image-only completions, outside the think envelope.
// Whole-line matching and prior process evidence keep ordinary prose intact.
func trailingProcessControl(s string, final bool) (int, Kind, bool) {
	starts := []string{"## Answer", "## Ответ", "Worked for "}
	if prefixWait(s, final, starts...) {
		return 0, "", true
	}
	if !hasAnyPrefix(s, starts) {
		return 0, "", false
	}
	end := strings.IndexByte(s, '\n')
	if end < 0 {
		if !final && len(s) < 128 {
			return 0, "", true
		}
		end = min(len(s), 128)
	}
	line := strings.TrimSpace(s[:end])
	if line == "## Answer" || line == "## Ответ" {
		return end, KindControl, false
	}
	if durationLineRE.MatchString(line) {
		return end, KindMeta, false
	}
	return 0, "", false
}

const maxProcessRecovery = 4096

// recoverProcessHeading recognizes a clipped activity region only when a
// completed activity title is joined directly to an explicit Answer control.
// Adjacent bold spans alone are ordinary prose, never a routing instruction.
// Delay only this ambiguous suffix, never the preceding answer. There is no
// language/keyword classifier, and a literal/code boundary cancels recovery.
func recoverProcessHeading(s string, final bool) (recover, wait bool) {
	if !strings.HasPrefix(s, "**") {
		return false, s == "*" && !final
	}
	limit := min(len(s), maxProcessRecovery)
	// A truncated inspection window is final for lookahead purposes: no wait
	// branch may retain more data hoping its already-full prefix will change.
	final = final || len(s) >= maxProcessRecovery
	part := s[:limit]
	end := strings.Index(part[2:], "**")
	if end < 0 {
		return false, !final && len(part) < maxHeading && !strings.Contains(part, "\n")
	}
	end += 4
	if end > maxHeading || strings.Contains(part[:end], "\n") {
		return false, false
	}
	if end == len(part) {
		return false, !final
	}
	if prefixWait(part[end:], final, "**", "\r\n") {
		return false, true
	}
	// A heading must end its line, or be joined directly to another activity.
	if !strings.HasPrefix(part[end:], "**") && !strings.HasPrefix(part[end:], "\n") && !strings.HasPrefix(part[end:], "\r\n") {
		return false, false
	}
	for i := 0; i < len(part); {
		rest := part[i:]
		if strings.HasPrefix(rest, "`") || strings.HasPrefix(rest, "~~~") || strings.HasPrefix(rest, "<think>") || strings.HasPrefix(rest, "</think>") {
			return false, false
		}
		if strings.HasPrefix(rest, "##") {
			return false, false
		}
		if !strings.HasPrefix(rest, "**") {
			i++
			continue
		}
		close := strings.Index(rest[2:], "**")
		if close < 0 {
			break
		}
		close += 4
		if close > maxHeading || strings.Contains(rest[:close], "\n") {
			i += 2
			continue
		}
		after := rest[close:]
		if prefixWait(after, final, "## Answer:", "## Ответ:", "**") {
			return false, true
		}
		if i > end && completedActivityTitle(rest[2:close-2]) && (strings.HasPrefix(after, "## Answer:") || strings.HasPrefix(after, "## Ответ:")) {
			return true, false
		}
		i += close
	}
	return false, !final && len(s) < maxProcessRecovery
}

// These completed-activity labels are observed website process summaries. They
// are only supporting evidence next to a real protocol boundary, never a
// standalone English-vs-Chinese or "sounds like thinking" classifier.
func completedActivityTitle(title string) bool {
	for _, prefix := range []string{"Confirmed ", "Organized ", "Created ", "Prepared ", "Verified ", "Checked ", "Analyzed ", "Calculated ", "Planned ", "Presented ", "Built ", "Generated "} {
		if strings.HasPrefix(title, prefix) {
			return true
		}
	}
	return false
}

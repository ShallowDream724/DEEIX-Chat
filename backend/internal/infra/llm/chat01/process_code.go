package chat01

import (
	"regexp"
	"strings"
)

// Execution recovery is only used inside an upstream process region, at a
// line/activity boundary. Two bounded code lines must agree before an unmarked
// Python fragment can become a tool record; a clipped first line is preserved
// verbatim rather than repaired with guessed source.
const maxProcessCodeLine = 1536

var clippedComprehensionRE = regexp.MustCompile(`^[0-9][\w \t.,()[\]+*/%\-]*[)\]][ \t]+for [A-Za-z_]\w* in (?:range|enumerate|zip)\([^\n]*[)\]]$`)

// A path tail followed by a shell operator and executable, or an executable
// reading an absolute script path, is stronger evidence than prose mentioning
// Python. This also covers website records whose opening command was clipped.
var shellExecutionRE = regexp.MustCompile(`^(?:(?:/?[A-Za-z0-9_.-]+/)[^\s;&|]+[ \t]*(?:&&|;)[ \t]*(?:python(?:3)?|pdftotext|pdfinfo|ls|cat)[ \t]+|python(?:3)?[ \t]+/[A-Za-z0-9_./-]+\.py(?:[ \t]|$)|cd[ \t]+/[^\s;&|]+[ \t]*&&[ \t]*(?:python(?:3)?|ls|cat)[ \t]+)`)

func shellExecutionLine(s string) bool {
	return shellExecutionRE.MatchString(strings.TrimSpace(s))
}

func processCodeStrength(s string) int {
	if strength := codeStrength(s); strength > 0 {
		return strength
	}
	if clippedComprehensionRE.MatchString(strings.TrimSpace(s)) {
		return 1
	}
	return 0
}

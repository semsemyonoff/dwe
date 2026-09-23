package model

import (
	"strings"
	"unicode"
)

// SummaryLine returns the one-line summary of a command or group description:
// its first non-empty line, trimmed. The full text stays the long form. This is
// DWE's own contract, not a YAML field: every one-line display surface (run
// banner, `dwe commands` tree, shell completion, browser rows, llms-txt, docs
// index) shows the summary, while inspect and per-command docs show the full
// description.
//
// Callers pass the already-translated description; the registry value is never
// rewritten. CRLF and lone CR count as line breaks, and any remaining control
// character inside the line (a tab, say) becomes a space, because a tab is the
// value/description separator of shell completion. Empty or whitespace-only
// input yields "".
//
// Only a YAML literal block (`|`) produces a multi-line description; a folded
// block (`>`) joins its lines with spaces, so its "first line" is the whole
// paragraph.
func SummaryLine(desc string) string {
	desc = strings.ReplaceAll(desc, "\r\n", "\n")
	desc = strings.ReplaceAll(desc, "\r", "\n")
	for line := range strings.SplitSeq(desc, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, line)
	}
	return ""
}

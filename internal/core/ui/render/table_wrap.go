package render

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// wrapPath breaks a path on "/" boundaries so the FILE column stays within
// width. The separator stays with the preceding segment; a single segment
// wider than width is hard-split via splitDisplayWidth.
func wrapPath(s string, width int) string {
	if s == "" || width <= 0 || lipgloss.Width(s) <= width {
		return s
	}

	var out []string
	current := ""
	flush := func() {
		if current != "" {
			out = append(out, current)
			current = ""
		}
	}
	for _, part := range strings.SplitAfter(s, "/") {
		if part == "" {
			continue
		}
		for lipgloss.Width(part) > width {
			head, tail := splitDisplayWidth(part, width)
			flush()
			out = append(out, head)
			part = tail
		}
		if current == "" {
			current = part
			continue
		}
		if lipgloss.Width(current+part) <= width {
			current += part
			continue
		}
		flush()
		current = part
	}
	flush()
	return strings.Join(out, "\n")
}

func wrapText(s string, width int) string {
	if s == "" || width <= 0 {
		return s
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = wrapLine(line, width)
	}
	return strings.Join(lines, "\n")
}

// isURLToken reports whether a whitespace-delimited token is a URL that must
// not be split across lines (so it stays copyable). A scheme separator is a
// good-enough signal for the http(s) links we emit as diagnostic hints.
func isURLToken(word string) bool {
	return strings.Contains(word, "://")
}

func wrapLine(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line
	}

	words := strings.Fields(line)
	if len(words) == 0 {
		return line
	}

	var out []string
	current := ""
	for _, word := range words {
		// URLs are kept whole even when they exceed the column width — a
		// hard-split mid-URL produces an uncopyable link. They still break
		// onto their own line (on the surrounding spaces), just never mid-token.
		for !isURLToken(word) && lipgloss.Width(word) > width {
			head, tail := splitDisplayWidth(word, width)
			if current != "" {
				out = append(out, current)
				current = ""
			}
			out = append(out, head)
			word = tail
		}

		if current == "" {
			current = word
			continue
		}
		candidate := current + " " + word
		if lipgloss.Width(candidate) <= width {
			current = candidate
			continue
		}
		out = append(out, current)
		current = word
	}
	if current != "" {
		out = append(out, current)
	}
	return strings.Join(out, "\n")
}

func splitDisplayWidth(s string, width int) (string, string) {
	if width <= 0 {
		return "", s
	}

	byteIdx := 0
	for byteIdx < len(s) {
		r, size := utf8.DecodeRuneInString(s[byteIdx:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		candidate := s[:byteIdx+size]
		if lipgloss.Width(candidate) > width {
			break
		}
		byteIdx += size
	}
	if byteIdx == 0 {
		_, size := utf8.DecodeRuneInString(s)
		byteIdx = size
	}
	return s[:byteIdx], s[byteIdx:]
}

// longestUnbreakableToken reports the narrowest width the column can be
// squeezed to. It probes the wrapper rather than inspecting it: wrapping at
// width 1 forces every breakable boundary, so the widest surviving line is by
// definition unbreakable. A nil wrap means the whole cell is unbreakable.
func longestUnbreakableToken(s string, wrap func(string, int) string) int {
	if wrap == nil {
		return lipgloss.Width(s)
	}

	wrapped := wrap(s, 1)
	width := 0
	for line := range strings.SplitSeq(wrapped, "\n") {
		if w := lipgloss.Width(line); w > width {
			width = w
		}
	}
	return width
}

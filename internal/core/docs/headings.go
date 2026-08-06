package docs

import (
	"regexp"
	"strings"
	"unicode"
)

// Heading is a parsed markdown heading. Level is 1..6, Text is the inline
// heading content with markdown syntax stripped.
type Heading struct {
	Level int
	Text  string
}

// ParseDoc extracts the first H1 as the document title and returns all H2/H3
// headings as a flat list (deeper levels are intentionally omitted to keep
// the tree shallow). Fenced code blocks are skipped so `# comments` inside
// shell snippets never get treated as headings. The first H1 is consumed by
// the title — it does not appear in the headings list.
func ParseDoc(content []byte) (title string, headings []Heading) {
	inFence := false
	// splitLines rather than bufio.Scanner — see splitLinesKeepEOL for why the
	// Scanner's silent over-long-line truncation is not acceptable here.
	for _, line := range splitLines(content) {
		trim := strings.TrimSpace(line)

		// Track fenced code blocks (``` or ~~~). Anything inside is skipped.
		if IsFenceLine(trim) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		level, text := parseHeadingLine(line)
		if level == 0 {
			continue
		}
		text = stripInlineMarkdown(text)
		if text == "" {
			continue
		}
		switch {
		case level == 1 && title == "":
			title = text
		case level == 2 || level == 3:
			headings = append(headings, Heading{
				Level: level,
				Text:  text,
			})
		}
	}
	return title, headings
}

// parseHeadingLine returns (level, text) for ATX-style headings (`#`…`######`).
// Setext-style underlines are intentionally not supported; the docs in this
// repo are all ATX. Returns (0, "") for non-heading lines.
func parseHeadingLine(line string) (int, string) {
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i == 0 || i > 6 {
		return 0, ""
	}
	if i < len(line) && line[i] != ' ' && line[i] != '\t' {
		return 0, ""
	}
	text := strings.TrimSpace(line[i:])
	// Strip trailing `#` runs that some authors add for symmetry.
	text = strings.TrimRight(text, "# \t")
	return i, text
}

var (
	mdLinkRE = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdCodeRE = regexp.MustCompile("`([^`]+)`")
)

// stripInlineMarkdown removes the small subset of inline markdown that
// commonly appears inside headings — links, code spans, emphasis — so the
// tree shows plain readable text. It is intentionally lossy: the goal is a
// label, not a roundtrip. RE2 has no backreferences, so emphasis markers
// (`**`, `__`, `*`, `_`) are stripped by character pass rather than regex.
func stripInlineMarkdown(s string) string {
	s = mdLinkRE.ReplaceAllString(s, "$1")
	s = mdCodeRE.ReplaceAllString(s, "$1")
	s = stripEmphasis(s)
	return strings.TrimSpace(s)
}

// stripEmphasis removes ** __ * _ markers around words. Markers are dropped
// indiscriminately — heading text rarely contains literal asterisks or
// underscores, and a stray one would only affect the tree label, not the
// rendered document.
func stripEmphasis(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

// TitleOrFallback returns title if non-empty, otherwise a humanised version
// of filename (extension stripped, separators turned into spaces). Used by
// the TUI to render tree labels.
func TitleOrFallback(title, filename string) string {
	if title != "" {
		return title
	}
	base := filename
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	// Capitalise the first letter so labels read like titles.
	if base == "" {
		return filename
	}
	r := []rune(base)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

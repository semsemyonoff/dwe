package docs

import (
	"regexp"
	"strings"
	"unicode"
)

// Heading is a parsed markdown heading. Level is 1..6, Text is the inline
// heading content with markdown syntax stripped, and Slug is the anchor that
// addresses it (`topic#slug`).
//
// Slug is carried on the struct rather than recomputed from Text by consumers:
// Text is lossy by design (see stripInlineMarkdown) and slugging it produces an
// anchor nothing can resolve — see parseHeadingSlugLabel.
type Heading struct {
	Level int
	Text  string
	Slug  string
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

		level, slug, text := parseHeadingSlugLabel(line)
		if level == 0 || text == "" {
			continue
		}
		switch {
		case level == 1 && title == "":
			title = text
		case level == 2 || level == 3:
			headings = append(headings, Heading{
				Level: level,
				Text:  text,
				Slug:  slug,
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

// parseHeadingSlugLabel parses one heading line into its level, anchor slug and
// display label. It is the SINGLE derivation of a heading's anchor: every
// surface that advertises one (`docs show --anchors`/`--toc`, `docs search`
// rows, the docs TUI's link jumps) and the resolver that consumes it
// (SliceByAnchor) must go through here, or they drift apart and the tool hands
// out anchors it then rejects.
//
// The asymmetry is load-bearing: the slug comes from the RAW heading text and
// the label from the markdown-stripped one. stripInlineMarkdown ends in
// stripEmphasis, which drops `_` as an emphasis marker — correct for a display
// label, fatal for an anchor, since it turns the heading "`service_dirs_ensure`"
// into the slug `servicedirsensure` while the resolver (slugging raw text, where
// Slugify preserves `_`) still answers to `service_dirs_ensure`. Every builtin
// name and every snake_case config key is such a heading.
//
// Returns (0, "", "") for non-heading lines. Callers filter on whichever of slug
// or label they actually need; the two can be empty independently.
func parseHeadingSlugLabel(line string) (level int, slug, label string) {
	level, raw := parseHeadingLine(line)
	if level == 0 {
		return 0, "", ""
	}
	return level, Slugify(raw), stripInlineMarkdown(raw)
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

// stripEmphasis removes ** __ * _ markers around words.
//
// Asterisks are dropped indiscriminately — a literal `*` in heading text is
// vanishingly rare, and a stray one would only affect a label. Underscores are
// not: they follow CommonMark's intra-word rule, where a `_` run flanked by
// alphanumerics on both sides is literal text rather than an emphasis marker.
//
// The distinction is not pedantry in this doc set. Dropping `_` wholesale
// rewrote every builtin name and snake_case config key wherever a label is
// shown — `env_file` rendered as `envfile` in the docs TUI tree and in the text
// column of `docs show --toc`, naming a key that does not exist.
func stripEmphasis(s string) string {
	return stripUnderscoreEmphasis(strings.ReplaceAll(s, "*", ""))
}

func stripUnderscoreEmphasis(s string) string {
	r := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(r); {
		if r[i] != '_' {
			b.WriteRune(r[i])
			i++
			continue
		}
		// Consume the whole run so `__bold__` is judged as one marker.
		j := i
		for j < len(r) && r[j] == '_' {
			j++
		}
		if i > 0 && j < len(r) && isAlnum(r[i-1]) && isAlnum(r[j]) {
			b.WriteString(string(r[i:j]))
		}
		i = j
	}
	return b.String()
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

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

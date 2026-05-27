package docs

import (
	"bufio"
	"bytes"
	"sort"
	"strings"
	"unicode"
)

// Slugify converts heading text to a GitHub-style anchor slug.
//
// Rules: lower-case the text, keep ASCII/Unicode letters, digits, hyphens,
// and underscores, drop everything else, and turn whitespace runs into single
// hyphens. Underscores are preserved (heading text like `` `on_enable` `` must
// slug to `on_enable-and-...`, so we cannot use stripInlineMarkdown which
// strips `_` as emphasis); markdown links and backtick code spans are
// flattened to their inner text before the character pass.
func Slugify(s string) string {
	s = mdLinkRE.ReplaceAllString(s, "$1")
	s = mdCodeRE.ReplaceAllString(s, "$1")
	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t':
			b.WriteByte('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			// Drop punctuation / symbols entirely (matches GitHub).
		}
	}
	// Trim leading/trailing hyphens introduced by surrounding whitespace.
	return strings.Trim(b.String(), "-")
}

// HeadingInfo describes one H2/H3 heading in document order. Used by
// callers that want a table of contents or an anchor list without slicing.
type HeadingInfo struct {
	Level int    // 2 or 3 (H1 is consumed by the document title)
	Slug  string // GitHub-style anchor slug — what `topic#slug` matches
	Text  string // inline-markdown-stripped heading text
}

// ParseHeadingSlugs walks content and returns every H2/H3 heading in document
// order. Fenced code blocks are skipped so `#` lines inside shell snippets are
// not treated as headings. H1 is intentionally omitted — by convention it is
// the document title (one per file, slug equals the topic path).
func ParseHeadingSlugs(content []byte) []HeadingInfo {
	lines := splitLinesKeepEOL(content)

	out := make([]HeadingInfo, 0, 16)
	inFence := false
	for _, line := range lines {
		trim := strings.TrimSpace(stripEOL(line))
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lvl, text := parseHeadingLine(stripEOL(line))
		if lvl < 2 || lvl > 3 {
			continue
		}
		text = stripInlineMarkdown(text)
		slug := Slugify(text)
		if slug == "" {
			continue
		}
		out = append(out, HeadingInfo{Level: lvl, Slug: slug, Text: text})
	}
	return out
}

// SliceByAnchor returns the markdown sub-section identified by anchor.
//
// Matching is tiered, mirroring topic resolution:
//
//  1. exact slug equality
//  2. case-insensitive slug equality
//  3. slug-prefix (the heading slug starts with anchor followed by `-`) — lets
//     `#binaries` find a heading whose slug is `binaries-block` when no other
//     heading slug starts with `binaries-`
//
// The section spans from the matched heading line up to (but not including)
// the next heading at the same or shallower depth, with content inside fenced
// code blocks intentionally ignored for heading detection. Returns the matched
// slug, the slice of every level-2/3 slug in the document (for diagnostics on
// miss/ambiguity), and ok=true on a unique match.
//
// On miss or ambiguity, sliced is nil and ok is false; the candidates list is
// populated so the caller can render a useful error.
func SliceByAnchor(content []byte, anchor string) (sliced []byte, matchedSlug string, candidates []string, ok bool) {
	lines := splitLinesKeepEOL(content)

	headings := make([]heading, 0, 16)
	inFence := false
	offset := 0
	for i, line := range lines {
		lineStart := offset
		offset += len(line)

		trim := strings.TrimSpace(stripEOL(line))
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lvl, text := parseHeadingLine(stripEOL(line))
		if lvl == 0 {
			continue
		}
		slug := Slugify(text)
		if slug == "" {
			continue
		}
		headings = append(headings, heading{level: lvl, slug: slug, lineIdx: i, startOff: lineStart})
	}

	if len(headings) == 0 {
		return nil, "", nil, false
	}

	// Collect candidates (all slugs) for diagnostics — sorted, deduped.
	seen := make(map[string]struct{}, len(headings))
	for _, h := range headings {
		if _, ok := seen[h.slug]; !ok {
			seen[h.slug] = struct{}{}
			candidates = append(candidates, h.slug)
		}
	}
	sort.Strings(candidates)

	// Tier 1: exact equality.
	exactMatches := filterHeadings(headings, func(h heading) bool { return h.slug == anchor })
	if len(exactMatches) >= 1 {
		// Multiple identical slugs in one doc shouldn't happen, but if it does,
		// prefer the first occurrence.
		return sliceSection(content, exactMatches[0].startOff, nextSectionOffset(headings, exactMatches[0], lines, content)), exactMatches[0].slug, candidates, true
	}

	// Tier 2: case-insensitive equality.
	ciMatches := filterHeadings(headings, func(h heading) bool { return strings.EqualFold(h.slug, anchor) })
	if len(ciMatches) == 1 {
		h := ciMatches[0]
		return sliceSection(content, h.startOff, nextSectionOffset(headings, h, lines, content)), h.slug, candidates, true
	}

	// Tier 3: slug-prefix (anchor + "-...").
	anchorLower := strings.ToLower(anchor)
	prefixMatches := filterHeadings(headings, func(h heading) bool {
		s := strings.ToLower(h.slug)
		return strings.HasPrefix(s, anchorLower+"-")
	})
	if len(prefixMatches) == 1 {
		h := prefixMatches[0]
		return sliceSection(content, h.startOff, nextSectionOffset(headings, h, lines, content)), h.slug, candidates, true
	}

	return nil, "", candidates, false
}

// nextSectionOffset returns the byte offset where the section starting at `h`
// ends — i.e. the start of the next heading at the same or shallower depth, or
// len(content) if no such heading follows.
func nextSectionOffset(headings []heading, h heading, _ []string, content []byte) int {
	// `headings` is in document order; find h's index and scan forward.
	hIdx := -1
	for i := range headings {
		if headings[i].lineIdx == h.lineIdx && headings[i].startOff == h.startOff {
			hIdx = i
			break
		}
	}
	if hIdx < 0 {
		return len(content)
	}
	for i := hIdx + 1; i < len(headings); i++ {
		if headings[i].level <= h.level {
			return headings[i].startOff
		}
	}
	return len(content)
}

// heading is a small local type; declared at package scope so helpers can
// take it as a parameter without re-exporting.
type heading struct {
	level    int
	slug     string
	lineIdx  int
	startOff int
}

func filterHeadings(hs []heading, pred func(heading) bool) []heading {
	out := make([]heading, 0, len(hs))
	for _, h := range hs {
		if pred(h) {
			out = append(out, h)
		}
	}
	return out
}

func sliceSection(content []byte, start, end int) []byte {
	if start < 0 {
		start = 0
	}
	if end > len(content) {
		end = len(content)
	}
	if end < start {
		end = start
	}
	return content[start:end]
}

// splitLinesKeepEOL returns the input split into lines, each retaining its
// trailing newline (if any). The concatenation of the returned slices equals
// the input exactly — this is what lets sliceSection use raw byte offsets.
func splitLinesKeepEOL(b []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	// Default ScanLines drops the newline; use a custom splitter that keeps it.
	scanner.Split(scanLinesKeepEOL)
	lines := make([]string, 0, 64)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func scanLinesKeepEOL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func stripEOL(s string) string {
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

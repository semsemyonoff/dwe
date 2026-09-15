package docs

import (
	"bytes"
	"sort"
	"strings"
	"unicode"
)

// Slugify converts heading text to a GitHub-style anchor slug.
//
// Rules: lower-case the text, keep ASCII/Unicode letters, digits, hyphens,
// and underscores, drop everything else, and turn each space or tab into a
// hyphen — one per character, not one per run, matching github-slugger (so
// "a  b" slugs to "a--b"). Underscores are preserved (heading text like “ `on_enable` “ must
// slug to `on_enable-and-...`, so we cannot use stripInlineMarkdown which
// strips `_` as emphasis); markdown links and backtick code spans are
// flattened to their inner text before the character pass.
//
// Feed it the RAW heading text. Callers inside this package should not call it
// directly at all — use parseHeadingSlugLabel, which owns that rule.
func Slugify(s string) string {
	s = mdLinkRE.ReplaceAllString(s, "$1")
	s = mdCodeRE.ReplaceAllString(s, "$1")
	s = strings.ToLower(s)
	// Trim BEFORE the character pass, not the hyphens after it: surrounding
	// whitespace is the only thing whose hyphens are an artifact. A blanket
	// Trim(…, "-") also ate hyphens belonging to the heading itself, so a
	// flag heading like "`--parallel N`" slugged to `parallel-n` while every
	// other surface — GitHub, the Starlight site, the doc's own TOC link —
	// says `--parallel-n`, and `dwe docs show` could not resolve its own link.
	s = strings.TrimSpace(s)

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
	return b.String()
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
		if IsFenceLine(trim) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lvl, slug, text := parseHeadingSlugLabel(stripEOL(line))
		if lvl < 2 || lvl > 3 {
			continue
		}
		if slug == "" {
			continue
		}
		out = append(out, HeadingInfo{Level: lvl, Slug: slug, Text: text})
	}
	return out
}

// MatchSlugIndex resolves anchor against slugs in document order and returns
// the index it selects, or -1.
//
// It is the SINGLE anchor-matching policy: `SliceByAnchor` (behind
// `dwe docs show 'topic#anchor'`) and the docs TUI's link jumps both go through
// it, so a link that opens one surface opens the other. The two grew separate
// copies once and immediately drifted — the TUI took the first prefix hit where
// the resolver demanded a unique one.
//
// Tiers, in order; every tier but the first requires a UNIQUE hit and otherwise
// falls through:
//
//  1. exact slug equality — first wins, see below
//  2. case-insensitive slug equality
//  3. equality ignoring leading/trailing hyphens — lets `#parallel-n` find a
//     flag heading whose slug is `--parallel-n`. Typing the dashes is
//     unnatural, and it is what dwe advertised before Slugify stopped eating
//     them.
//  4. slug-prefix (the heading slug starts with anchor followed by `-`) — lets
//     `#binaries` find a heading whose slug is `binaries-block`
//
// Hyphen-equivalence MUST stay above slug-prefix: it is the more specific
// relation, and a document holding both "`--parallel N`" and "Parallel N
// details" would otherwise resolve `#parallel-n` to the second by prefix and
// never reach the tier meant for the first.
//
// Tier 1 keeps first-wins because duplicate slugs DO occur in this doc set —
// five heading pairs in the English tree alone repeat a name like "Validation".
// (github-slugger suffixes the repeat as `validation-1`, which dwe does not
// derive and therefore cannot resolve; a link naming one would miss.)
func MatchSlugIndex(slugs []string, anchor string) int {
	if anchor == "" {
		return -1
	}

	uniqueMatch := func(pred func(slug string) bool) int {
		found := -1
		for i, s := range slugs {
			if !pred(s) {
				continue
			}
			if found >= 0 {
				return -1 // ambiguous
			}
			found = i
		}
		return found
	}

	for i, s := range slugs {
		if s == anchor {
			return i
		}
	}
	if i := uniqueMatch(func(s string) bool { return strings.EqualFold(s, anchor) }); i >= 0 {
		return i
	}

	anchorLower := strings.ToLower(anchor)
	if trimmed := strings.Trim(anchorLower, "-"); trimmed != "" {
		if i := uniqueMatch(func(s string) bool {
			return strings.Trim(strings.ToLower(s), "-") == trimmed
		}); i >= 0 {
			return i
		}
	}

	return uniqueMatch(func(s string) bool {
		return strings.HasPrefix(strings.ToLower(s), anchorLower+"-")
	})
}

// SliceByAnchor returns the markdown sub-section identified by anchor.
// Matching is MatchSlugIndex's, over the document's H2/H3 slugs.
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
		if IsFenceLine(trim) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lvl, slug, _ := parseHeadingSlugLabel(stripEOL(line))
		if lvl == 0 || slug == "" {
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

	slugs := make([]string, len(headings))
	for i, h := range headings {
		slugs[i] = h.slug
	}
	idx := MatchSlugIndex(slugs, anchor)
	if idx < 0 {
		return nil, "", candidates, false
	}
	h := headings[idx]
	return sliceSection(content, h.startOff, nextSectionOffset(headings, h, lines, content)), h.slug, candidates, true
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
//
// Deliberately not bufio.Scanner: the content is already fully in memory, and a
// Scanner silently STOPS at the first line longer than its buffer cap (the
// error only shows up in scanner.Err(), which is easy to forget). Project docs
// are user-owned markdown, so one minified or base64 line would have truncated
// the document — breaking the concatenation invariant above, and silently
// dropping every later heading/match for the callers that share this splitter.
func splitLinesKeepEOL(b []byte) []string {
	lines := make([]string, 0, 64)
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			lines = append(lines, string(b))
			break
		}
		lines = append(lines, string(b[:i+1]))
		b = b[i+1:]
	}
	return lines
}

// splitLines is splitLinesKeepEOL with the line terminator removed, matching
// bufio.ScanLines' semantics (trailing "\n", and a "\r" immediately before it,
// are dropped) minus the token size limit.
func splitLines(b []byte) []string {
	lines := splitLinesKeepEOL(b)
	for i, l := range lines {
		lines[i] = stripEOL(l)
	}
	return lines
}

func stripEOL(s string) string {
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

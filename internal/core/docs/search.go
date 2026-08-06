package docs

import (
	"sort"
	"strings"
	"unicode"
)

// SearchHit is one result from Search: a (topic, section) pair with the
// number of times the query matched inside that section. Section is the
// nearest H2/H3 heading slug; if a match falls between the document start and
// the first H2/H3 (the lead paragraph under the H1), Section is empty.
type SearchHit struct {
	Source  string
	Path    string // topic path without .md
	Section string // anchor slug ("" for pre-first-H2 lead)
	Count   int
	Snippet string // one sanitized source line, the densest match in the section
}

// SearchOptions controls how the query string is interpreted.
type SearchOptions struct {
	// Literal matches the whole query as a single case-insensitive substring
	// instead of splitting it into tokens. It is a flag rather than a quoting
	// convention on purpose: `dwe docs search` takes exactly one argument and
	// the shell strips quotes, so `"a b"` and `a b` arrive identical.
	Literal bool
}

// snippetMaxLen caps the per-hit snippet. Long enough to carry a YAML line or
// a sentence, short enough that a 50-row result set stays readable.
const snippetMaxLen = 160

// Search searches every topic in roots, attributing matches to the nearest
// preceding H2/H3 section. Results are sorted by (Count desc, Path asc,
// Section asc) so the highest-signal sections rise to the top regardless of
// doc order.
//
// The query is split on whitespace and ALL tokens must be present for a
// section to match (AND). Ranking uses the MINIMUM per-token count rather than
// the sum: summing lets a section with 40 hits of "vars" and one of
// "interpolation" outrank the section actually about the pair, and min makes a
// repeated token ("vars vars") harmless.
//
// Matching stays case-insensitive SUBSTRING per token, not word-boundary: that
// is the documented design intent below and is required for identifiers like
// `depends_on:`. The known trade-off is that a short token matches inside a
// longer word — `uid` matches `guide`/`guides`, `env` matches `environment`.
// It is a deliberate false-positive, not an oversight.
//
// Two tiers, in order:
//
//  1. sections that contain every token;
//  2. per document, when NO section of that document satisfies (1) but the
//     document as a whole does, one hit attributed to the section carrying the
//     densest single line. Without this tier a page that explains a pair of
//     concepts in two adjacent sections is invisible to the query naming both.
//
// Tier-1 hits always sort above tier-2 hits: a section containing everything is
// a stronger answer than a page scattering it.
//
// The matcher is intentionally NOT regex: agents and humans both type literal
// identifiers ("depends_on", "RunContext.Render") far more often than they
// type regex patterns, and a regex flag can be added later without changing
// the default. Matches inside fenced code blocks are counted — searching for
// `depends_on:` should find it in YAML examples, not just prose.
//
// query may be empty: an empty query returns no hits (rather than returning
// every section with a `Count: len(content)`, which would be useless).
func Search(roots []DocRoot, query, locale string, opts SearchOptions) []SearchHit {
	tokens := searchTokens(query, opts.Literal)
	if len(tokens) == 0 {
		return nil
	}

	topics := AllTopics(roots, locale)
	sectionHits := make([]SearchHit, 0, len(topics))
	docHits := make([]SearchHit, 0, 4)

	for _, t := range topics {
		// Find the doc root that actually contains this topic.
		root := RootByName(roots, t.Source)
		if root.Name == "" {
			continue
		}
		content, _, _, err := ResolveContent(root, t.Path+".md", locale)
		if err != nil {
			continue
		}

		sections := searchInDoc(content, tokens)
		matched := false
		for _, s := range sections {
			count := minCount(s.counts)
			if count == 0 {
				continue
			}
			matched = true
			sectionHits = append(sectionHits, SearchHit{
				Source:  t.Source,
				Path:    t.Path,
				Section: s.slug,
				Count:   count,
				Snippet: sanitizeSnippet(s.bestLine),
			})
		}
		if matched || len(tokens) < 2 {
			continue
		}
		if hit, ok := docLevelHit(sections, tokens); ok {
			hit.Source = t.Source
			hit.Path = t.Path
			docHits = append(docHits, hit)
		}
	}

	sortSearchHits(sectionHits)
	sortSearchHits(docHits)
	return append(sectionHits, docHits...)
}

// searchTokens lowercases the query and splits it into the tokens that must all
// be present. Splitting uses strings.Fields, never strings.Split(q, " "): a
// double space would otherwise yield an empty token, countCaseInsensitive
// returns 0 for an empty needle, and the AND gate would zero out every result.
//
// Duplicate tokens are dropped — min-based ranking already makes them harmless,
// so removing them only saves work.
func searchTokens(query string, literal bool) []string {
	if literal {
		// Trim before the emptiness check, matching the strings.Fields path
		// below: an all-whitespace query must find nothing, not search for a
		// run of spaces and match nearly every indented markdown line.
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			return nil
		}
		return []string{strings.ToLower(trimmed)}
	}
	fields := strings.Fields(strings.ToLower(query))
	tokens := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		tokens = append(tokens, f)
	}
	return tokens
}

// sectionStats accumulates per-token match counts for one section, plus the
// single most informative line seen in it.
type sectionStats struct {
	slug     string
	counts   []int  // parallel to the token slice
	bestLine string // line with the most distinct tokens (first one wins ties)
	bestHits int    // distinct tokens on bestLine
}

// searchInDoc returns per-section match statistics in document order. The
// current section is updated each time a new H2/H3 heading is encountered
// (outside a fenced block). Matches are counted per line (case-insensitive
// substring) into the current section bucket. Sections with zero matches for
// every token are omitted.
func searchInDoc(content []byte, tokens []string) []sectionStats {
	byslug := make(map[string]*sectionStats)
	order := make([]*sectionStats, 0, 8)
	currentSlug := ""

	inFence := false
	// Reused across lines: fully overwritten each iteration and only ever read
	// back into s.counts, never retained.
	lineCounts := make([]int, 0, len(tokens))
	// splitLines, not bufio.Scanner: a Scanner stops at the first over-long
	// line and reports it only via scanner.Err(), so a single huge line in a
	// project doc would have silently truncated that document's search stats
	// and hidden every match after it.
	for _, line := range splitLines(content) {
		trim := strings.TrimSpace(line)

		if IsFenceLine(trim) {
			inFence = !inFence
		} else if !inFence {
			if lvl, text := parseHeadingLine(line); lvl == 2 || lvl == 3 {
				if slug := Slugify(stripInlineMarkdown(text)); slug != "" {
					currentSlug = slug
				}
			}
		}

		// Count matches on every line — including the heading line itself and
		// lines inside fenced code blocks. This matters because configuration
		// schemas often appear only inside YAML/code samples.
		lower := strings.ToLower(line)
		lineDistinct := 0
		lineCounts = lineCounts[:0]
		for _, tok := range tokens {
			c := countCaseInsensitiveLowered(lower, tok)
			if c > 0 {
				lineDistinct++
			}
			lineCounts = append(lineCounts, c)
		}
		if lineDistinct == 0 {
			continue
		}

		s := byslug[currentSlug]
		if s == nil {
			s = &sectionStats{slug: currentSlug, counts: make([]int, len(tokens))}
			byslug[currentSlug] = s
			order = append(order, s)
		}
		for i, c := range lineCounts {
			s.counts[i] += c
		}
		if lineDistinct > s.bestHits {
			s.bestHits = lineDistinct
			s.bestLine = trim
		}
	}

	out := make([]sectionStats, 0, len(order))
	for _, s := range order {
		out = append(out, *s)
	}
	return out
}

// docLevelHit builds the tier-2 result for a document whose tokens are all
// present but never inside a single section. The count is the min over
// document-wide per-token totals, and the hit is attributed to the section with
// the densest single line — that is both the most useful anchor to jump to and
// the source of the snippet.
func docLevelHit(sections []sectionStats, tokens []string) (SearchHit, bool) {
	if len(sections) == 0 {
		return SearchHit{}, false
	}
	totals := make([]int, len(tokens))
	for _, s := range sections {
		for i, c := range s.counts {
			totals[i] += c
		}
	}
	count := minCount(totals)
	if count == 0 {
		return SearchHit{}, false
	}

	best := 0
	for i := 1; i < len(sections); i++ {
		if sections[i].bestHits > sections[best].bestHits {
			best = i
			continue
		}
		if sections[i].bestHits == sections[best].bestHits &&
			sumCount(sections[i].counts) > sumCount(sections[best].counts) {
			best = i
		}
	}
	return SearchHit{
		Section: sections[best].slug,
		Count:   count,
		Snippet: sanitizeSnippet(sections[best].bestLine),
	}, true
}

func sortSearchHits(hits []SearchHit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Count != hits[j].Count {
			return hits[i].Count > hits[j].Count
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Section < hits[j].Section
	})
}

func minCount(counts []int) int {
	if len(counts) == 0 {
		return 0
	}
	m := counts[0]
	for _, c := range counts[1:] {
		if c < m {
			m = c
		}
	}
	return m
}

func sumCount(counts []int) int {
	total := 0
	for _, c := range counts {
		total += c
	}
	return total
}

// sanitizeSnippet makes a raw document line safe to write to a terminal as a
// TSV field. Two passes, both load-bearing:
//
//   - non-printable runes are DROPPED. The snippet is the only channel through
//     which document content reaches stdout, and a doc tree can be untrusted
//     (`--source project` inside a cloned repo), so an ESC/BEL/OSC sequence
//     embedded in a page would otherwise reach the terminal verbatim and clear
//     the screen, recolor it, or set the window title. Whitespace is exempt
//     here and normalized by the next pass instead;
//   - every whitespace run (tabs and newlines included — markdown tables carry
//     both) collapses to a single space, so the snippet can never break a
//     tab-separated row.
//
// The length is then capped at snippetMaxLen bytes, ellipsis included, so a
// snippet never wraps a terminal.
func sanitizeSnippet(line string) string {
	printable := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPrint(r) {
			return r
		}
		return -1
	}, line)
	s := strings.Join(strings.Fields(printable), " ")
	if len(s) <= snippetMaxLen {
		return s
	}
	// Leave room for the ellipsis, cut back to a rune boundary, then back to
	// the last word boundary so the snippet never ends mid-word.
	cut := snippetMaxLen - len(snippetEllipsis)
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	if sp := strings.LastIndexByte(s[:cut], ' '); sp > 0 {
		cut = sp
	}
	return strings.TrimSpace(s[:cut]) + snippetEllipsis
}

const snippetEllipsis = "…"

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// countCaseInsensitiveLowered counts non-overlapping occurrences of needle in
// s. BOTH sides must already be lowercased by the caller: the haystack is
// lowered once per line for the whole token set, the needles once per query.
func countCaseInsensitiveLowered(sLower, needleLower string) int {
	if needleLower == "" {
		return 0
	}
	return strings.Count(sLower, needleLower)
}

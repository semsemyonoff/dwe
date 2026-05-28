package docs

import (
	"bufio"
	"bytes"
	"sort"
	"strings"
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
}

// Search performs a case-insensitive literal substring search across every
// topic in roots, attributing matches to the nearest preceding H2/H3 section.
// Results are sorted by (Count desc, Path asc, Section asc) so the highest-
// signal sections rise to the top regardless of doc order.
//
// The matcher is intentionally NOT regex: agents and humans both type literal
// identifiers ("depends_on", "RunContext.Render") far more often than they
// type regex patterns, and a regex flag can be added later without changing
// the default. Matches inside fenced code blocks are counted — searching for
// `depends_on:` should find it in YAML examples, not just prose.
//
// query may be empty: an empty query returns no hits (rather than returning
// every section with a `Count: len(content)`, which would be useless).
func Search(roots []DocRoot, query, locale string) []SearchHit {
	if query == "" {
		return nil
	}
	needle := strings.ToLower(query)

	topics := AllTopics(roots, locale)
	out := make([]SearchHit, 0, len(topics))

	for _, t := range topics {
		// Find the doc root that actually contains this topic.
		var root DocRoot
		for _, r := range roots {
			if r.Name == t.Source {
				root = r
				break
			}
		}
		if root.Name == "" {
			continue
		}
		content, _, _, err := ResolveContent(root, t.Path+".md", locale)
		if err != nil {
			continue
		}
		hits := searchInDoc(content, needle)
		for slug, count := range hits {
			out = append(out, SearchHit{
				Source:  t.Source,
				Path:    t.Path,
				Section: slug,
				Count:   count,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Section < out[j].Section
	})
	return out
}

// searchInDoc returns a map of section slug → match count. The current
// section is updated each time a new H2/H3 heading is encountered (outside a
// fenced block). Matches are counted per line (case-insensitive substring),
// summed into the current section bucket. Sections with zero matches are
// omitted from the result.
func searchInDoc(content []byte, needle string) map[string]int {
	hits := make(map[string]int)
	currentSlug := ""

	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inFence := false
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)

		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
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
		if c := countCaseInsensitive(line, needle); c > 0 {
			hits[currentSlug] += c
		}
	}
	return hits
}

// countCaseInsensitive counts non-overlapping occurrences of needle in s,
// comparing case-insensitively. needle is required to already be lowercased
// by the caller (we only lower s once per line, not per call).
func countCaseInsensitive(s, needleLower string) int {
	if needleLower == "" {
		return 0
	}
	return strings.Count(strings.ToLower(s), needleLower)
}

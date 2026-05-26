package tui

import (
	"bytes"
	"regexp"
	"strings"
)

// SearchEntry is a single search result (heading).
type SearchEntry struct {
	Text   string
	Line   int
	Level  int // 1-6 for # through ######
	Source string
}

// SearchIndex builds and manages a searchable index of markdown headings.
type SearchIndex struct {
	entries []SearchEntry
}

// BuildSearchIndex scans markdown content and extracts all headings.
func BuildSearchIndex(content []byte, source string) *SearchIndex {
	lines := bytes.Split(content, []byte("\n"))
	var entries []SearchEntry

	headingRe := regexp.MustCompile(`^(#+)\s+(.+)$`)

	for i, line := range lines {
		matches := headingRe.FindSubmatch(line)
		if matches != nil {
			level := len(matches[1])
			text := strings.TrimSpace(string(matches[2]))
			entries = append(entries, SearchEntry{
				Text:   text,
				Line:   i,
				Level:  level,
				Source: source,
			})
		}
	}

	return &SearchIndex{
		entries: entries,
	}
}

// SearchResults finds all entries matching the query (case-insensitive substring).
func (si *SearchIndex) SearchResults(query string) []SearchEntry {
	if query == "" {
		return nil
	}

	lower := strings.ToLower(query)
	var results []SearchEntry

	for _, entry := range si.entries {
		if strings.Contains(strings.ToLower(entry.Text), lower) {
			results = append(results, entry)
		}
	}

	return results
}

// SearchState tracks the current search session.
type SearchState struct {
	Query   string
	Results []SearchEntry
	Current int  // Index into Results; -1 if none or closed
	IsOpen  bool
}

// NewSearchState creates a new search state.
func NewSearchState() *SearchState {
	return &SearchState{
		Current: -1,
		IsOpen:  false,
	}
}

// Open opens search with a query (builds results from index).
func (ss *SearchState) Open(query string, index *SearchIndex) {
	ss.Query = query
	ss.Results = index.SearchResults(query)
	ss.Current = 0
	ss.IsOpen = true
	if len(ss.Results) == 0 {
		ss.Current = -1
	}
}

// Close closes the search.
func (ss *SearchState) Close() {
	ss.IsOpen = false
	ss.Query = ""
	ss.Results = nil
	ss.Current = -1
}

// Next moves to the next match.
func (ss *SearchState) Next() {
	if !ss.IsOpen || len(ss.Results) == 0 {
		return
	}
	ss.Current = (ss.Current + 1) % len(ss.Results)
}

// Prev moves to the previous match.
func (ss *SearchState) Prev() {
	if !ss.IsOpen || len(ss.Results) == 0 {
		return
	}
	ss.Current = (ss.Current - 1 + len(ss.Results)) % len(ss.Results)
}

// CurrentResult returns the current match, or nil if none.
func (ss *SearchState) CurrentResult() *SearchEntry {
	if !ss.IsOpen || ss.Current < 0 || ss.Current >= len(ss.Results) {
		return nil
	}
	return &ss.Results[ss.Current]
}

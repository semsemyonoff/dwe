package tui

import (
	"testing"
)

func TestBuildSearchIndex(t *testing.T) {
	content := []byte(`# Main Title
Some text
## Section One
More content
### Subsection
Even more
## Section Two
Final section`)

	idx := BuildSearchIndex(content, "test.md")

	if len(idx.entries) != 4 {
		t.Errorf("expected 4 headings, got %d", len(idx.entries))
	}

	// Check first heading
	if idx.entries[0].Text != "Main Title" {
		t.Errorf("expected 'Main Title', got '%s'", idx.entries[0].Text)
	}
	if idx.entries[0].Level != 1 {
		t.Errorf("expected level 1, got %d", idx.entries[0].Level)
	}

	// Check second heading
	if idx.entries[1].Text != "Section One" {
		t.Errorf("expected 'Section One', got '%s'", idx.entries[1].Text)
	}
	if idx.entries[1].Level != 2 {
		t.Errorf("expected level 2, got %d", idx.entries[1].Level)
	}

	// Check subsection
	if idx.entries[2].Text != "Subsection" {
		t.Errorf("expected 'Subsection', got '%s'", idx.entries[2].Text)
	}
	if idx.entries[2].Level != 3 {
		t.Errorf("expected level 3, got %d", idx.entries[2].Level)
	}
}

func TestSearchResults(t *testing.T) {
	content := []byte(`# Configuration
## Services
## Docker
# API
## Endpoints
`)
	idx := BuildSearchIndex(content, "test.md")

	tests := []struct {
		query    string
		expected int
	}{
		{"config", 1},
		{"service", 1},
		{"docker", 1},
		{"api", 1},
		{"endpoint", 1},
		{"s", 2}, // matches "Services" and "API" is not here, so only Services
		{"e", 3}, // matches "Configuration", "Services", "Endpoints"
	}

	for _, tt := range tests {
		results := idx.SearchResults(tt.query)
		if len(results) != tt.expected {
			t.Errorf("query '%s': expected %d results, got %d", tt.query, tt.expected, len(results))
		}
	}
}

func TestSearchResultsCasInsensitive(t *testing.T) {
	content := []byte(`# MySection
## subsection
`)
	idx := BuildSearchIndex(content, "test.md")

	results := idx.SearchResults("my")
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'my', got %d", len(results))
	}

	results = idx.SearchResults("MY")
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'MY', got %d", len(results))
	}

	results = idx.SearchResults("SUBSECTION")
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'SUBSECTION', got %d", len(results))
	}
}

func TestSearchState(t *testing.T) {
	content := []byte(`# One
## Two
## Three
`)
	idx := BuildSearchIndex(content, "test.md")
	ss := NewSearchState()

	if ss.IsOpen {
		t.Errorf("expected search to be closed initially")
	}

	ss.Open("o", idx)
	if !ss.IsOpen {
		t.Errorf("expected search to be open after Open()")
	}

	// "One" and "Two" contain 'o', but "Three" does not
	if len(ss.Results) != 2 {
		t.Errorf("expected 2 results for 'o', got %d", len(ss.Results))
	}

	if ss.Current != 0 {
		t.Errorf("expected current to be 0, got %d", ss.Current)
	}

	// Test Next
	ss.Next()
	if ss.Current != 1 {
		t.Errorf("expected current to be 1 after Next(), got %d", ss.Current)
	}

	// Test wrapping
	ss.Next()
	if ss.Current != 0 {
		t.Errorf("expected current to wrap to 0, got %d", ss.Current)
	}

	// Test Prev
	ss.Prev()
	if ss.Current != 1 {
		t.Errorf("expected current to be 1 after Prev(), got %d", ss.Current)
	}

	// Test CurrentResult
	result := ss.CurrentResult()
	if result == nil {
		t.Errorf("expected CurrentResult to not be nil")
	}
	if result.Text != "Two" {
		t.Errorf("expected text 'Two', got '%s'", result.Text)
	}

	ss.Close()
	if ss.IsOpen {
		t.Errorf("expected search to be closed after Close()")
	}
	if ss.Current != -1 {
		t.Errorf("expected current to be -1 after Close(), got %d", ss.Current)
	}
}

func TestSearchStateNoResults(t *testing.T) {
	content := []byte(`# One
## Two
`)
	idx := BuildSearchIndex(content, "test.md")
	ss := NewSearchState()

	ss.Open("nonexistent", idx)
	if ss.Current != -1 {
		t.Errorf("expected current to be -1 for empty results, got %d", ss.Current)
	}

	result := ss.CurrentResult()
	if result != nil {
		t.Errorf("expected CurrentResult to be nil for empty results")
	}
}

package tui

import "strings"

// TreeFilter holds the live state of the left-panel name filter. Active is
// false while the user is browsing normally; "/" toggles it on, Esc clears.
// Query is the substring (case-insensitive) applied to dir names, file
// titles (with filename fallback), and heading text.
type TreeFilter struct {
	Active bool
	Query  string
}

// NewTreeFilter returns a closed filter.
func NewTreeFilter() *TreeFilter { return &TreeFilter{} }

// Open enters filter mode with the current query.
func (f *TreeFilter) Open() {
	f.Active = true
}

// Close exits filter mode and clears the query so the tree shows everything
// again on the next recompute.
func (f *TreeFilter) Close() {
	f.Active = false
	f.Query = ""
}

// Append adds one rune to the query.
func (f *TreeFilter) Append(r rune) { f.Query += string(r) }

// Backspace drops the trailing rune from the query.
func (f *TreeFilter) Backspace() {
	if f.Query == "" {
		return
	}
	r := []rune(f.Query)
	f.Query = string(r[:len(r)-1])
}

// Matches reports whether label matches the filter query (case-insensitive
// substring). An empty query matches everything so freshly-opened filter
// mode shows the full tree until the user types.
func (f *TreeFilter) Matches(label string) bool {
	if f.Query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(label), strings.ToLower(f.Query))
}

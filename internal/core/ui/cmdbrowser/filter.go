package cmdbrowser

import (
	"maps"

	"charm.land/bubbles/v2/list"
)

// filterState carries the in-progress filter session. The browser owns one
// instance; nil means "no filter active". Entering / leaving the filter is a
// state transition handled in browser.enterFilter / browser.exitFilter.
type filterState struct {
	query        string
	matched      []int          // indices into browser.items, sorted by rank
	matchCount   map[string]int // tree-node id → matches in subtree
	savedExpand  map[string]bool
	savedFocusID string
}

// newFilterState snapshots the current expanded set and seeds an empty query.
// Caller calls recompute to fill the matched list and per-node match counts.
func newFilterState(expanded map[string]bool, focusID string) *filterState {
	saved := make(map[string]bool, len(expanded))
	maps.Copy(saved, expanded)
	return &filterState{
		query:        "",
		matchCount:   map[string]int{},
		savedExpand:  saved,
		savedFocusID: focusID,
	}
}

// recompute re-ranks items against the current query and refreshes per-node
// match counts. An empty query matches everything (so the user can see the
// full list while typing). Ranking uses bubbles/list.DefaultFilter for parity
// with the bubbles list filter UX.
func (f *filterState) recompute(items []Item, includePrivate bool) {
	f.matched = f.matched[:0]
	for k := range f.matchCount {
		delete(f.matchCount, k)
	}
	// Build the candidate set (apply IncludePrivate filtering up-front).
	type cand struct {
		origIdx int
		hay     string
	}
	cands := make([]cand, 0, len(items))
	for i, it := range items {
		if !includePrivate && it.Private {
			continue
		}
		hay := it.ID
		if it.Description != "" {
			hay = it.ID + " " + it.Description
		}
		cands = append(cands, cand{origIdx: i, hay: hay})
	}
	if f.query == "" {
		// Pseudo-rank by insertion order — the user has not started typing yet.
		for _, c := range cands {
			f.matched = append(f.matched, c.origIdx)
		}
	} else {
		haystacks := make([]string, len(cands))
		for i, c := range cands {
			haystacks[i] = c.hay
		}
		ranks := list.DefaultFilter(f.query, haystacks)
		f.matched = make([]int, 0, len(ranks))
		for _, r := range ranks {
			f.matched = append(f.matched, cands[r.Index].origIdx)
		}
	}
	// Per-node match counts: each matched item contributes 1 to every ancestor
	// group of its ID (including the leaf's direct group).
	for _, idx := range f.matched {
		id := items[idx].ID
		f.matchCount[""]++ // root sees everything
		for {
			g := groupOf(id)
			if g == "" {
				break
			}
			f.matchCount[g]++
			id = g
		}
	}
}

// applyAutoCollapse mutates expanded to expand subtrees that contain matches
// and collapse those that don't. Used only when opts.AutoCollapseEmpty is true.
// Subtrees with M > 0 are forced expanded so the user sees the matches.
func (f *filterState) applyAutoCollapse(tm *treeModel) {
	// Expansion is engine-owned; route the auto-collapse through the by-key
	// accessor while keeping the consumer-side node-id iteration here.
	for id := range tm.nodesByID {
		if id == "" {
			continue
		}
		tm.eng.SetExpandedByKey(id, f.matchCount[id] > 0)
	}
	tm.eng.RebuildVisible(nil)
}

// restoreExpansion puts the original expanded set back on the tree (called on
// filter exit). The focused ID is restored separately by browser.exitFilter.
func (f *filterState) restoreExpansion(tm *treeModel) {
	tm.eng.RestoreExpanded(f.savedExpand)
	tm.eng.RebuildVisible(nil)
}

// hasMatch reports whether the given tree node id has any matches in its
// subtree. Used by the tree renderer to dim zero-match groups during filter.
func (f *filterState) hasMatch(nodeID string) bool { return f.matchCount[nodeID] > 0 }

// renderQueryLine returns the textinput-like prompt for the right-panel header
// while filter mode is active. The trailing block █ is the visible cursor.
func (f *filterState) renderQueryLine() string {
	return "/" + f.query + "█"
}

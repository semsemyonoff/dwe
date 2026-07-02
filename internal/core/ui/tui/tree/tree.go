// Package tree provides a generic, reusable tree behavioral engine shared by
// the command browser (cmdbrowser) and docs browser (docstui) TUI plugins.
//
// The engine owns the behavior that both trees previously duplicated by
// "mirror exactly" discipline: the visible-row set, the cursor, vertical
// scroll (topIdx), a parent map, expansion state, directional collapse/expand,
// navigation, and scroll/clip math. Each consumer keeps its own node graph,
// row rendering, payload (counts/headings), and filter UX, supplying only a
// tiny three-method adapter.
//
// Rendering is deliberately NOT in the engine (Decision 7): the engine returns
// VisibleNodes() + Cursor() + Clip(...); consumers render each row themselves
// because glyphs, counts, truncation, and styles genuinely differ. Expansion
// is keyed by a stable string Key (Decision 3/8) so it survives a node-graph
// rebuild (e.g. docs locale change). The engine has zero filter/query/count
// concepts — RebuildVisible takes an optional per-node `keep` predicate
// (Decision 6) and knows nothing about why a node is kept.
package tree

import (
	"maps"
	"slices"
	"strings"
)

// Adapter is the per-consumer contract the engine needs to walk a node graph.
// N is the consumer's concrete node type (a pointer, so it is comparable and
// its zero value nil is the "root/none" sentinel — see Engine.Cursor).
type Adapter[N comparable] interface {
	// Children returns the ordered child nodes of n.
	Children(N) []N
	// Key returns a STABLE, unique id for n that survives a SetRoots rebuild.
	// Expansion and cursor identity are keyed by it.
	Key(N) string
	// Expandable reports whether n can hold expansion state (false for leaves
	// and heading rows).
	Expandable(N) bool
}

// Engine holds the shared tree behavior. Map-keying discipline (Decision 8):
//   - expanded is keyed by Key (string) — survives rebuild.
//   - parent and byKey are rebuilt per generation in SetRoots; parent is keyed
//     by N (pointers valid within one generation).
//   - cursor is stored as N and re-resolved by Key on rebuild.
type Engine[N comparable] struct {
	a        Adapter[N]
	roots    []N
	expanded map[string]bool // keyed by Key — survives SetRoots
	cursor   N
	visible  []N
	topIdx   int
	parent   map[N]N      // child -> parent, rebuilt per generation
	byKey    map[string]N // Key -> node, rebuilt per generation (O(1) lookup)
}

// New builds an empty engine bound to the given adapter. Call SetRoots then
// RebuildVisible to populate it.
func New[N comparable](a Adapter[N]) *Engine[N] {
	return &Engine[N]{
		a:        a,
		expanded: map[string]bool{},
		parent:   map[N]N{},
		byKey:    map[string]N{},
	}
}

// SetRoots (re)builds the parent + byKey maps from a Children() walk of roots
// and re-resolves the cursor by its prior Key. If the prior cursor's Key is
// gone after the rebuild the cursor is set to the zero value (root/none) and is
// NOT auto-parked — the consumer decides the fallback (Decision 4). SetRoots
// does NOT rebuild the visible set; the caller calls RebuildVisible afterward.
func (e *Engine[N]) SetRoots(roots []N) {
	var zero N
	prevKey := ""
	if e.cursor != zero {
		prevKey = e.a.Key(e.cursor)
	}

	e.roots = roots
	e.parent = map[N]N{}
	e.byKey = map[string]N{}

	var walk func(n N)
	walk = func(n N) {
		e.byKey[e.a.Key(n)] = n
		for _, c := range e.a.Children(n) {
			e.parent[c] = n
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}

	// Re-resolve the cursor by its prior Key. Vanished => zero (no auto-park).
	if prevKey != "" {
		if n, ok := e.byKey[prevKey]; ok {
			e.cursor = n
		} else {
			e.cursor = zero
		}
	}
}

// RebuildVisible recomputes the visible-row set.
//
//   - keep == nil: emit every node reachable through expanded ancestors only
//     (cmdbrowser; it never reduces the set).
//   - keep != nil: emit a node iff keep(node) OR any descendant is kept
//     (ancestor-inclusion; docs's reduced filter set). Expansion is ignored for
//     inclusion in this mode — the renderer shows the reduced set directly.
//
// RebuildVisible does NOT move the cursor even if the cursor falls out of the
// visible set (Decision 4 / "Cursor re-park policy"). Consumers that re-park
// call ParkCursorIfHidden separately.
func (e *Engine[N]) RebuildVisible(keep func(N) bool) {
	e.visible = e.visible[:0]
	if keep == nil {
		var walk func(n N)
		walk = func(n N) {
			for _, c := range e.a.Children(n) {
				e.visible = append(e.visible, c)
				if e.expanded[e.a.Key(c)] {
					walk(c)
				}
			}
		}
		for _, r := range e.roots {
			e.visible = append(e.visible, r)
			if e.expanded[e.a.Key(r)] {
				walk(r)
			}
		}
		return
	}

	// keep != nil: ancestor-inclusion. A node is emitted iff it matches or any
	// descendant matches; emitted in pre-order to preserve hierarchy.
	matched := map[N]bool{}
	var mark func(n N) bool
	mark = func(n N) bool {
		any := false
		if keep(n) {
			matched[n] = true
			any = true
		}
		for _, c := range e.a.Children(n) {
			if mark(c) {
				matched[n] = true
				any = true
			}
		}
		return any
	}
	var emit func(n N)
	emit = func(n N) {
		if !matched[n] {
			return
		}
		e.visible = append(e.visible, n)
		for _, c := range e.a.Children(n) {
			emit(c)
		}
	}
	for _, r := range e.roots {
		mark(r)
	}
	for _, r := range e.roots {
		emit(r)
	}
}

// ParkCursorIfHidden moves the cursor onto the first visible row when the
// current cursor is absent from the visible set (and the visible set is
// non-empty). docs-only: cmdbrowser deliberately tolerates an off-screen cursor
// and resolves it later via nearestVisibleAncestor, so it never calls this.
func (e *Engine[N]) ParkCursorIfHidden() {
	var zero N
	if e.cursor != zero && slices.Contains(e.visible, e.cursor) {
		return
	}
	if len(e.visible) > 0 {
		e.cursor = e.visible[0]
	}
}

// VisibleNodes returns the current visible-row slice (engine-owned; do not
// mutate).
func (e *Engine[N]) VisibleNodes() []N { return e.visible }

// Cursor returns the focused node. The zero value (nil for a pointer N) is the
// "root/none" sentinel — cmdbrowser maps it to focusedID == "".
func (e *Engine[N]) Cursor() N { return e.cursor }

// SetCursor focuses n directly.
func (e *Engine[N]) SetCursor(n N) { e.cursor = n }

// SetCursorByKey focuses the node whose Key == key. An empty or unknown key
// sets the cursor to the zero value (root/none) and returns false; a resolved
// key returns true.
func (e *Engine[N]) SetCursorByKey(key string) bool {
	var zero N
	if key == "" {
		e.cursor = zero
		return false
	}
	if n, ok := e.byKey[key]; ok {
		e.cursor = n
		return true
	}
	e.cursor = zero
	return false
}

// indexOf returns the visible index of the cursor, or -1 when the cursor is the
// zero value or not present in the visible set.
func (e *Engine[N]) indexOf() int {
	var zero N
	if e.cursor == zero {
		return -1
	}
	for i, n := range e.visible {
		if n == e.cursor {
			return i
		}
	}
	return -1
}

// MoveUp moves the cursor one visible row up.
func (e *Engine[N]) MoveUp() { e.moveBy(-1) }

// MoveDown moves the cursor one visible row down.
func (e *Engine[N]) MoveDown() { e.moveBy(1) }

// MoveBy moves the cursor delta visible rows, clamped to the visible range in a
// single step. Prefer this over looping MoveUp/MoveDown for a coalesced wheel
// delta (the loop would be O(|delta|·visible) via repeated indexOf scans).
func (e *Engine[N]) MoveBy(delta int) { e.moveBy(delta) }

func (e *Engine[N]) moveBy(delta int) {
	var zero N
	if len(e.visible) == 0 {
		e.cursor = zero
		return
	}
	i := e.indexOf()
	if i < 0 {
		e.cursor = e.visible[0]
		return
	}
	ni := i + delta
	if ni < 0 {
		ni = 0
	} else if ni >= len(e.visible) {
		ni = len(e.visible) - 1
	}
	e.cursor = e.visible[ni]
}

// MoveHome focuses the first visible row.
func (e *Engine[N]) MoveHome() {
	if len(e.visible) > 0 {
		e.cursor = e.visible[0]
	}
}

// MoveEnd focuses the last visible row.
func (e *Engine[N]) MoveEnd() {
	if len(e.visible) > 0 {
		e.cursor = e.visible[len(e.visible)-1]
	}
}

// Collapse implements ←/h directional semantics: if the cursor is expandable
// and expanded, collapse it (and rebuild); otherwise step the cursor to its
// parent (unless the parent is a root, which has no entry in the parent map).
// Leaves / heading rows always fall through to the "step to parent" branch.
func (e *Engine[N]) Collapse() {
	var zero N
	if e.cursor == zero {
		return
	}
	if e.a.Expandable(e.cursor) && e.expanded[e.a.Key(e.cursor)] {
		delete(e.expanded, e.a.Key(e.cursor))
		e.RebuildVisible(nil)
		return
	}
	if p, ok := e.parent[e.cursor]; ok {
		e.cursor = p
	}
}

// Expand implements →/l directional semantics: if the cursor is expandable and
// collapsed (with children), expand it (and rebuild); if already expanded, step
// the cursor into the first child. Leaves / heading rows are no-ops.
func (e *Engine[N]) Expand() {
	var zero N
	if e.cursor == zero || !e.a.Expandable(e.cursor) {
		return
	}
	children := e.a.Children(e.cursor)
	if len(children) == 0 {
		return
	}
	if !e.expanded[e.a.Key(e.cursor)] {
		e.expanded[e.a.Key(e.cursor)] = true
		e.RebuildVisible(nil)
		return
	}
	e.cursor = children[0]
}

// Toggle flips the expansion of the cursor (no-op on leaves / heading rows).
// Unlike Expand, it does NOT require the node to have children: an Expandable
// node with zero children (e.g. docstui's index-only directories) still flips
// its expansion flag so its glyph reflects the state, matching the pre-engine
// behavior. The child-count guard lives only in Expand, where stepping into a
// first child is meaningless without children.
func (e *Engine[N]) Toggle() {
	var zero N
	if e.cursor == zero || !e.a.Expandable(e.cursor) {
		return
	}
	key := e.a.Key(e.cursor)
	if e.expanded[key] {
		delete(e.expanded, key)
	} else {
		e.expanded[key] = true
	}
	e.RebuildVisible(nil)
}

// IsExpanded reports whether n is currently expanded.
func (e *Engine[N]) IsExpanded(n N) bool { return e.expanded[e.a.Key(n)] }

// SetExpanded sets n's expansion flag (node-based; used for docs ancestor-
// expand). Does not rebuild the visible set.
func (e *Engine[N]) SetExpanded(n N, b bool) {
	if b {
		e.expanded[e.a.Key(n)] = true
	} else {
		delete(e.expanded, e.a.Key(n))
	}
}

// ExpandedSnapshot returns a copy of the expansion map (keyed by Key) for the
// filter's save/restore machinery.
func (e *Engine[N]) ExpandedSnapshot() map[string]bool {
	return maps.Clone(e.expanded)
}

// RestoreExpanded replaces the internal expansion map with a copy of snapshot.
// The caller then calls RebuildVisible.
func (e *Engine[N]) RestoreExpanded(snapshot map[string]bool) {
	e.expanded = maps.Clone(snapshot)
	if e.expanded == nil {
		e.expanded = map[string]bool{}
	}
}

// SetExpandedByKey sets expansion by Key directly — used by the filter's
// auto-collapse, which iterates the consumer's node id map.
func (e *Engine[N]) SetExpandedByKey(key string, b bool) {
	if b {
		e.expanded[key] = true
	} else {
		delete(e.expanded, key)
	}
}

// EnsureFocusVisible adjusts topIdx so the cursor stays within a viewport of
// the given height (the inner tree-panel height the Frame supplies). Ported
// verbatim from cmdbrowser.ensureFocusVisible.
func (e *Engine[N]) EnsureFocusVisible(height int) {
	n := len(e.visible)
	if n == 0 || height <= 0 {
		e.topIdx = 0
		return
	}
	idx := e.indexOf()
	if idx < 0 {
		e.topIdx = 0
		return
	}
	if idx < e.topIdx {
		e.topIdx = idx
	} else if idx >= e.topIdx+height {
		e.topIdx = idx - height + 1
	}
	maxTop := max(n-height, 0)
	if e.topIdx > maxTop {
		e.topIdx = maxTop
	}
	if e.topIdx < 0 {
		e.topIdx = 0
	}
}

// FocusRow moves the cursor to the visible node at the given panel-local row
// (0-based, relative to the first rendered row at topIdx). A click past the
// last visible node is a no-op. Ported verbatim from cmdbrowser.focusRow.
func (e *Engine[N]) FocusRow(row int) {
	if row < 0 {
		return
	}
	idx := e.topIdx + row
	if idx >= len(e.visible) {
		return
	}
	e.cursor = e.visible[idx]
}

// Clip slices the rendered tree (one line per visible node) to height rows
// starting at topIdx. Ported verbatim from cmdbrowser.clipToViewport. A
// zero/negative height renders no rows rather than overflowing the panel.
func (e *Engine[N]) Clip(full string, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(full, "\n")
	if len(lines) <= height {
		return full
	}
	top := min(max(e.topIdx, 0), len(lines)-height)
	return strings.Join(lines[top:top+height], "\n")
}

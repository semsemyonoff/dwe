package tree

import (
	"strings"
	"testing"
)

// fakeNode is a minimal node type used to exercise the engine with zero
// consumer coupling.
type fakeNode struct {
	key      string
	label    string
	expand   bool // Expandable() result; leaves are false
	children []*fakeNode
}

// fakeAdapter implements Adapter[*fakeNode].
type fakeAdapter struct{}

func (fakeAdapter) Children(n *fakeNode) []*fakeNode { return n.children }
func (fakeAdapter) Key(n *fakeNode) string           { return n.key }
func (fakeAdapter) Expandable(n *fakeNode) bool      { return n.expand }

// leaf builds a non-expandable node.
func leaf(key string) *fakeNode { return &fakeNode{key: key, label: key} }

// branch builds an expandable node with children.
func branch(key string, children ...*fakeNode) *fakeNode {
	return &fakeNode{key: key, label: key, expand: true, children: children}
}

// sampleTree returns:
//
//	a (branch)
//	  a1 (leaf)
//	  a2 (branch)
//	    a2x (leaf)
//	b (branch)
//	  b1 (leaf)
//	c (leaf)
func sampleTree() (roots []*fakeNode, byKey map[string]*fakeNode) {
	a1 := leaf("a1")
	a2x := leaf("a2x")
	a2 := branch("a2", a2x)
	a := branch("a", a1, a2)
	b1 := leaf("b1")
	b := branch("b", b1)
	c := leaf("c")
	roots = []*fakeNode{a, b, c}
	byKey = map[string]*fakeNode{
		"a": a, "a1": a1, "a2": a2, "a2x": a2x,
		"b": b, "b1": b1, "c": c,
	}
	return roots, byKey
}

func newSampleEngine() (*Engine[*fakeNode], map[string]*fakeNode) {
	roots, byKey := sampleTree()
	e := New(fakeAdapter{})
	e.SetRoots(roots)
	e.RebuildVisible(nil)
	return e, byKey
}

func visibleKeys(e *Engine[*fakeNode]) []string {
	out := make([]string, 0, len(e.VisibleNodes()))
	for _, n := range e.VisibleNodes() {
		out = append(out, n.key)
	}
	return out
}

func eqKeys(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestRebuildVisibleCollapsedByDefault(t *testing.T) {
	e, _ := newSampleEngine()
	if got := visibleKeys(e); !eqKeys(got, "a", "b", "c") {
		t.Fatalf("collapsed visible = %v, want [a b c]", got)
	}
}

func TestRebuildVisibleExpanded(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetExpanded(byKey["a"], true)
	e.RebuildVisible(nil)
	if got := visibleKeys(e); !eqKeys(got, "a", "a1", "a2", "b", "c") {
		t.Fatalf("after expand a: %v, want [a a1 a2 b c]", got)
	}
	e.SetExpanded(byKey["a2"], true)
	e.RebuildVisible(nil)
	if got := visibleKeys(e); !eqKeys(got, "a", "a1", "a2", "a2x", "b", "c") {
		t.Fatalf("after expand a2: %v", got)
	}
}

func TestNavClamps(t *testing.T) {
	e, _ := newSampleEngine()
	e.MoveHome()
	if e.Cursor().key != "a" {
		t.Fatalf("home cursor = %q, want a", e.Cursor().key)
	}
	e.MoveUp() // clamp at top
	if e.Cursor().key != "a" {
		t.Fatalf("up at top = %q, want a", e.Cursor().key)
	}
	e.MoveDown()
	if e.Cursor().key != "b" {
		t.Fatalf("down = %q, want b", e.Cursor().key)
	}
	e.MoveEnd()
	if e.Cursor().key != "c" {
		t.Fatalf("end = %q, want c", e.Cursor().key)
	}
	e.MoveDown() // clamp at bottom
	if e.Cursor().key != "c" {
		t.Fatalf("down at bottom = %q, want c", e.Cursor().key)
	}
}

func TestDirectionalExpandOnCollapsed(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["a"])
	// l on collapsed branch => expand (cursor stays on a)
	e.Expand()
	if !e.IsExpanded(byKey["a"]) {
		t.Fatal("a should be expanded after l")
	}
	if e.Cursor().key != "a" {
		t.Fatalf("cursor after expand = %q, want a", e.Cursor().key)
	}
	if got := visibleKeys(e); !eqKeys(got, "a", "a1", "a2", "b", "c") {
		t.Fatalf("visible after expand = %v", got)
	}
}

func TestDirectionalExpandOnExpandedStepsToFirstChild(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["a"])
	e.Expand() // expand
	e.Expand() // already expanded => step into first child
	if e.Cursor().key != "a1" {
		t.Fatalf("cursor = %q, want a1 (first child)", e.Cursor().key)
	}
}

func TestDirectionalCollapseOnExpanded(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["a"])
	e.Expand()
	e.Collapse() // expanded => collapse, cursor stays
	if e.IsExpanded(byKey["a"]) {
		t.Fatal("a should be collapsed")
	}
	if e.Cursor().key != "a" {
		t.Fatalf("cursor = %q, want a", e.Cursor().key)
	}
}

func TestDirectionalCollapseOnCollapsedStepsToParent(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetExpanded(byKey["a"], true)
	e.RebuildVisible(nil)
	e.SetCursor(byKey["a1"]) // collapsed leaf with parent a
	e.Collapse()             // step to parent
	if e.Cursor().key != "a" {
		t.Fatalf("cursor = %q, want a (parent)", e.Cursor().key)
	}
}

func TestCollapseAtRootLevelIsNoop(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["c"]) // top-level leaf, no parent in map
	e.Collapse()
	if e.Cursor().key != "c" {
		t.Fatalf("cursor = %q, want c (no-op)", e.Cursor().key)
	}
}

func TestToggle(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["a"])
	e.Toggle()
	if !e.IsExpanded(byKey["a"]) {
		t.Fatal("toggle should expand a")
	}
	e.Toggle()
	if e.IsExpanded(byKey["a"]) {
		t.Fatal("toggle should collapse a")
	}
	// Toggle on a leaf is a no-op.
	e.SetCursor(byKey["c"])
	e.Toggle()
	if e.IsExpanded(byKey["c"]) {
		t.Fatal("toggle on leaf should be no-op")
	}
}

func TestRebuildVisibleKeepPredicateAncestorInclusion(t *testing.T) {
	e, _ := newSampleEngine()
	// keep only a2x; ancestors a and a2 must be included, b/b1/c excluded.
	e.RebuildVisible(func(n *fakeNode) bool { return n.key == "a2x" })
	if got := visibleKeys(e); !eqKeys(got, "a", "a2", "a2x") {
		t.Fatalf("keep a2x = %v, want [a a2 a2x]", got)
	}
}

func TestRebuildVisibleKeepPredicateMatchesMultiple(t *testing.T) {
	e, _ := newSampleEngine()
	e.RebuildVisible(func(n *fakeNode) bool { return n.key == "b1" || n.key == "c" })
	if got := visibleKeys(e); !eqKeys(got, "b", "b1", "c") {
		t.Fatalf("keep b1,c = %v, want [b b1 c]", got)
	}
}

func TestRebuildVisibleDoesNotMoveCursor(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["c"])
	// Reduce the set so the cursor (c) is no longer visible.
	e.RebuildVisible(func(n *fakeNode) bool { return n.key == "a1" })
	if got := visibleKeys(e); !eqKeys(got, "a", "a1") {
		t.Fatalf("visible = %v, want [a a1]", got)
	}
	if e.Cursor().key != "c" {
		t.Fatalf("cursor moved to %q, RebuildVisible must NOT re-park", e.Cursor().key)
	}
}

func TestParkCursorIfHidden(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["c"])
	e.RebuildVisible(func(n *fakeNode) bool { return n.key == "a1" })
	e.ParkCursorIfHidden()
	if e.Cursor().key != "a" {
		t.Fatalf("parked cursor = %q, want a (first visible)", e.Cursor().key)
	}
	// When cursor is already visible, parking is a no-op.
	e.SetCursor(byKey["a1"])
	e.ParkCursorIfHidden()
	if e.Cursor().key != "a1" {
		t.Fatalf("park no-op failed, cursor = %q", e.Cursor().key)
	}
}

func TestParkCursorIfHiddenFromZero(t *testing.T) {
	e, _ := newSampleEngine()
	e.SetCursor(nil)
	e.ParkCursorIfHidden()
	if e.Cursor() == nil || e.Cursor().key != "a" {
		t.Fatalf("park from zero = %v, want a", e.Cursor())
	}
}

func TestSetRootsPreservesExpansionAndCursorByKey(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetExpanded(byKey["a"], true)
	e.SetCursor(byKey["a1"])
	e.RebuildVisible(nil)

	// Rebuild a fresh generation with the SAME keys (new pointers).
	roots2, byKey2 := sampleTree()
	e.SetRoots(roots2)
	e.RebuildVisible(nil)

	// Expansion survived (keyed by Key).
	if !e.IsExpanded(byKey2["a"]) {
		t.Fatal("expansion of a should survive SetRoots")
	}
	if got := visibleKeys(e); !eqKeys(got, "a", "a1", "a2", "b", "c") {
		t.Fatalf("visible after rebuild = %v", got)
	}
	// Cursor re-resolved to the new-generation a1 (different pointer).
	if e.Cursor() != byKey2["a1"] {
		t.Fatalf("cursor not re-resolved to new a1: %v", e.Cursor())
	}
	_ = roots2
}

func TestSetRootsVanishedCursorGoesZeroNoAutoPark(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetCursor(byKey["a1"])

	// New generation missing a1 entirely.
	a := branch("a") // no children now
	b := branch("b", leaf("b1"))
	c := leaf("c")
	e.SetRoots([]*fakeNode{a, b, c})
	if e.Cursor() != nil {
		t.Fatalf("vanished cursor should be zero (nil), got %v", e.Cursor())
	}
	// No auto-park: even after RebuildVisible the cursor stays nil until the
	// consumer parks it.
	e.RebuildVisible(nil)
	if e.Cursor() != nil {
		t.Fatalf("cursor must stay nil (no auto-park), got %v", e.Cursor())
	}
}

func TestSetCursorByKey(t *testing.T) {
	e, byKey := newSampleEngine()
	if !e.SetCursorByKey("b1") {
		t.Fatal("SetCursorByKey(b1) should return true")
	}
	if e.Cursor() != byKey["b1"] {
		t.Fatalf("cursor = %v, want b1", e.Cursor())
	}
	if e.SetCursorByKey("") {
		t.Fatal("empty key should return false")
	}
	if e.Cursor() != nil {
		t.Fatalf("empty key should zero cursor, got %v", e.Cursor())
	}
	e.SetCursor(byKey["a"])
	if e.SetCursorByKey("nonexistent") {
		t.Fatal("unknown key should return false")
	}
	if e.Cursor() != nil {
		t.Fatalf("unknown key should zero cursor, got %v", e.Cursor())
	}
}

func TestZeroCursorNavSafe(t *testing.T) {
	e, _ := newSampleEngine()
	e.SetCursor(nil)
	// None of these should panic; a move from nil cursor lands on visible[0].
	e.Collapse()
	e.Expand()
	e.Toggle()
	e.EnsureFocusVisible(10)
	e.MoveUp()
	if e.Cursor() == nil || e.Cursor().key != "a" {
		t.Fatalf("MoveUp from nil cursor = %v, want a", e.Cursor())
	}
	e.SetCursor(nil)
	e.MoveDown()
	if e.Cursor() == nil || e.Cursor().key != "a" {
		t.Fatalf("MoveDown from nil cursor = %v, want a", e.Cursor())
	}
}

func TestExpandedSnapshotRoundTrip(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetExpanded(byKey["a"], true)
	e.SetExpanded(byKey["a2"], true)
	snap := e.ExpandedSnapshot()
	if !snap["a"] || !snap["a2"] {
		t.Fatalf("snapshot missing keys: %v", snap)
	}
	// Snapshot is a copy — mutating the engine doesn't change it.
	e.SetExpanded(byKey["b"], true)
	if snap["b"] {
		t.Fatal("snapshot should be a copy")
	}
	// Restore wipes b and reinstates only a, a2.
	e.RestoreExpanded(snap)
	if e.IsExpanded(byKey["b"]) {
		t.Fatal("restore should drop b")
	}
	if !e.IsExpanded(byKey["a"]) || !e.IsExpanded(byKey["a2"]) {
		t.Fatal("restore should keep a, a2")
	}
}

func TestSetExpandedByKey(t *testing.T) {
	e, byKey := newSampleEngine()
	e.SetExpandedByKey("a", true)
	if !e.IsExpanded(byKey["a"]) {
		t.Fatal("SetExpandedByKey(a,true) failed")
	}
	e.SetExpandedByKey("a", false)
	if e.IsExpanded(byKey["a"]) {
		t.Fatal("SetExpandedByKey(a,false) failed")
	}
}

func TestEnsureFocusVisibleWindow(t *testing.T) {
	e, byKey := newSampleEngine()
	// Expand everything so there are 7 visible rows.
	for _, k := range []string{"a", "a2", "b"} {
		e.SetExpanded(byKey[k], true)
	}
	e.RebuildVisible(nil)
	if got := len(e.VisibleNodes()); got != 7 {
		t.Fatalf("visible count = %d, want 7", got)
	}
	// Cursor on last row, height 3 => topIdx scrolls to show it.
	e.MoveEnd()
	e.EnsureFocusVisible(3)
	if e.topIdx != 4 { // 7-3
		t.Fatalf("topIdx = %d, want 4", e.topIdx)
	}
	// Cursor on first row scrolls back to top.
	e.MoveHome()
	e.EnsureFocusVisible(3)
	if e.topIdx != 0 {
		t.Fatalf("topIdx = %d, want 0", e.topIdx)
	}
}

func TestFocusRowAndClickPastLastNoop(t *testing.T) {
	e, _ := newSampleEngine() // 3 visible: a b c
	e.FocusRow(1)
	if e.Cursor().key != "b" {
		t.Fatalf("FocusRow(1) = %q, want b", e.Cursor().key)
	}
	// Click past the last visible row is a no-op.
	prev := e.Cursor()
	e.FocusRow(99)
	if e.Cursor() != prev {
		t.Fatalf("FocusRow past last should be no-op, cursor = %v", e.Cursor())
	}
	// Negative row no-op.
	e.FocusRow(-1)
	if e.Cursor() != prev {
		t.Fatalf("FocusRow(-1) should be no-op, cursor = %v", e.Cursor())
	}
}

func TestClip(t *testing.T) {
	full := strings.Join([]string{"r0", "r1", "r2", "r3", "r4"}, "\n")
	e := New(fakeAdapter{})
	// height >= line count => unchanged.
	if got := e.Clip(full, 10); got != full {
		t.Fatalf("Clip(full,10) = %q, want full", got)
	}
	// height 0 => empty.
	if got := e.Clip(full, 0); got != "" {
		t.Fatalf("Clip(full,0) = %q, want empty", got)
	}
	// topIdx 2, height 2 => rows r2,r3.
	e.topIdx = 2
	if got := e.Clip(full, 2); got != "r2\nr3" {
		t.Fatalf("Clip window = %q, want r2\\nr3", got)
	}
	// topIdx clamped so the window never runs past the end.
	e.topIdx = 99
	if got := e.Clip(full, 2); got != "r3\nr4" {
		t.Fatalf("Clip clamped = %q, want r3\\nr4", got)
	}
}

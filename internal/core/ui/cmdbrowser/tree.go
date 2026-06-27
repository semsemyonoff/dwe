package cmdbrowser

import (
	"sort"
	"strings"
)

// treeNode is one entry in the command-group hierarchy. Nodes carry both
// their full dot-path ID and their local name so the renderer never has to
// re-split. Counts are cached; setIncludePrivate or the constructor refreshes
// them.
type treeNode struct {
	id       string // full dot path: "" for root, "services.main", etc.
	name     string // last segment
	depth    int    // root is -1; top-level groups are 0
	parent   *treeNode
	children []*treeNode

	// leaves holds indices into treeModel.items for commands whose group is
	// exactly this node's id.
	leaves []int

	countAll    int // total leaves in subtree (including private)
	countPublic int // total non-private leaves in subtree
}

// treeModel owns the group hierarchy, the expanded/focused state, and the
// cached visible-row list. It is internal to cmdbrowser; the Model wires
// keypresses into its methods.
type treeModel struct {
	items          []Item
	includePrivate bool
	root           *treeNode
	nodesByID      map[string]*treeNode
	expanded       map[string]bool
	focusedID      string
	visible        []*treeNode

	// topIdx is the index into visible of the first row rendered in the tree
	// panel. Without it an oversized tree (more group nodes than the panel can
	// hold) would overflow the bordered frame. The Frame owns geometry now, so
	// clipping is driven off the inner panel height passed to the renderer —
	// see ensureFocusVisible / clipToViewport. Ported from *Model.treeTopIdx.
	topIdx int
}

// newTreeModel builds the tree from items, applies the initial expansion
// depth, and seeds focus on the first visible row (or the root when there
// are no group nodes, e.g. only root-level commands).
func newTreeModel(items []Item, includePrivate bool, defaultDepth int) *treeModel {
	if defaultDepth < 0 {
		defaultDepth = 0
	}
	tm := &treeModel{
		items:          items,
		includePrivate: includePrivate,
		root:           &treeNode{depth: -1},
		expanded:       map[string]bool{},
		nodesByID:      map[string]*treeNode{},
	}
	tm.nodesByID[""] = tm.root
	tm.build()
	tm.recomputeCounts()
	tm.initExpansion(defaultDepth)
	tm.rebuildVisible()
	if len(tm.visible) > 0 {
		tm.focusedID = tm.visible[0].id
	}
	return tm
}

// groupOf returns the group portion of a command ID (everything before the
// last dot). Top-level commands (no dot) belong to the root group "".
func groupOf(id string) string {
	i := strings.LastIndex(id, ".")
	if i < 0 {
		return ""
	}
	return id[:i]
}

func (tm *treeModel) build() {
	for i, item := range tm.items {
		node := tm.ensureGroup(groupOf(item.ID))
		node.leaves = append(node.leaves, i)
	}
	var sortRec func(n *treeNode)
	sortRec = func(n *treeNode) {
		sort.SliceStable(n.children, func(i, j int) bool {
			return n.children[i].name < n.children[j].name
		})
		for _, c := range n.children {
			sortRec(c)
		}
	}
	sortRec(tm.root)
}

func (tm *treeModel) ensureGroup(id string) *treeNode {
	if n, ok := tm.nodesByID[id]; ok {
		return n
	}
	parent := tm.ensureGroup(groupOf(id))
	name := id
	if i := strings.LastIndex(id, "."); i >= 0 {
		name = id[i+1:]
	}
	n := &treeNode{
		id:     id,
		name:   name,
		depth:  parent.depth + 1,
		parent: parent,
	}
	parent.children = append(parent.children, n)
	tm.nodesByID[id] = n
	return n
}

func (tm *treeModel) recomputeCounts() {
	var walk func(n *treeNode) (int, int)
	walk = func(n *treeNode) (int, int) {
		all, pub := 0, 0
		for _, idx := range n.leaves {
			all++
			if !tm.items[idx].Private {
				pub++
			}
		}
		for _, c := range n.children {
			ca, cp := walk(c)
			all += ca
			pub += cp
		}
		n.countAll = all
		n.countPublic = pub
		return all, pub
	}
	walk(tm.root)
}

// initExpansion expands every node whose depth < defaultDepth. A depth of 0
// leaves everything collapsed; the first level of groups is always visible
// regardless (it is rooted under the implicit root).
func (tm *treeModel) initExpansion(defaultDepth int) {
	if defaultDepth <= 0 {
		return
	}
	for id, n := range tm.nodesByID {
		if id == "" {
			continue
		}
		if n.depth < defaultDepth {
			tm.expanded[id] = true
		}
	}
}

func (tm *treeModel) setIncludePrivate(b bool) {
	if tm.includePrivate == b {
		return
	}
	tm.includePrivate = b
	tm.recomputeCounts()
}

func (tm *treeModel) rebuildVisible() {
	tm.visible = tm.visible[:0]
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		for _, c := range n.children {
			tm.visible = append(tm.visible, c)
			if tm.expanded[c.id] {
				walk(c)
			}
		}
	}
	walk(tm.root)
}

func (tm *treeModel) focusedNode() *treeNode {
	if n, ok := tm.nodesByID[tm.focusedID]; ok {
		return n
	}
	return tm.root
}

func (tm *treeModel) indexOfFocused() int {
	for i, n := range tm.visible {
		if n.id == tm.focusedID {
			return i
		}
	}
	return -1
}

func (tm *treeModel) moveUp()   { tm.moveBy(-1) }
func (tm *treeModel) moveDown() { tm.moveBy(1) }

func (tm *treeModel) moveBy(delta int) {
	if len(tm.visible) == 0 {
		tm.focusedID = ""
		return
	}
	i := tm.indexOfFocused()
	if i < 0 {
		tm.focusedID = tm.visible[0].id
		return
	}
	ni := i + delta
	if ni < 0 {
		ni = 0
	} else if ni >= len(tm.visible) {
		ni = len(tm.visible) - 1
	}
	tm.focusedID = tm.visible[ni].id
}

func (tm *treeModel) moveHome() {
	if len(tm.visible) > 0 {
		tm.focusedID = tm.visible[0].id
	}
}

func (tm *treeModel) moveEnd() {
	if len(tm.visible) > 0 {
		tm.focusedID = tm.visible[len(tm.visible)-1].id
	}
}

// onRight implements the →/l semantics from spec §5.2: collapsed node with
// children → expand; already-expanded node → step into the first child.
func (tm *treeModel) onRight() {
	n := tm.focusedNode()
	if n == nil || n == tm.root || len(n.children) == 0 {
		return
	}
	if !tm.expanded[n.id] {
		tm.expanded[n.id] = true
		tm.rebuildVisible()
		return
	}
	tm.moveDown()
}

// onLeft implements ←/h semantics: if expanded → collapse; else move focus
// up to the parent group (root is invisible, so top-level nodes stay put).
func (tm *treeModel) onLeft() {
	n := tm.focusedNode()
	if n == nil || n == tm.root {
		return
	}
	if tm.expanded[n.id] {
		delete(tm.expanded, n.id)
		tm.rebuildVisible()
		return
	}
	if n.parent != nil && n.parent != tm.root {
		tm.focusedID = n.parent.id
	}
}

// toggleFocused toggles expansion of the focused node. No-op on leaves.
func (tm *treeModel) toggleFocused() {
	n := tm.focusedNode()
	if n == nil || n == tm.root || len(n.children) == 0 {
		return
	}
	if tm.expanded[n.id] {
		delete(tm.expanded, n.id)
	} else {
		tm.expanded[n.id] = true
	}
	tm.rebuildVisible()
}

// nearestVisibleAncestor returns the id of the nearest ancestor of id (walking
// upward via groupOf) that currently appears in the visible slice. Returns ""
// when no non-root visible ancestor exists, meaning the right panel should show
// root-level commands.
func (tm *treeModel) nearestVisibleAncestor(id string) string {
	visible := make(map[string]bool, len(tm.visible))
	for _, n := range tm.visible {
		visible[n.id] = true
	}
	g := id
	for g != "" {
		if visible[g] {
			return g
		}
		g = groupOf(g)
	}
	return ""
}

// ensureFocusVisible adjusts topIdx so the focused node stays within a
// viewport of the given height (the inner tree-panel height the Frame supplies).
// Called after every tree mutation (move / expand / collapse) and on each
// render so a resize keeps the focused row on screen. Ported from
// *Model.ensureTreeFocusVisible, driven off the passed height instead of
// reading layout from a *Model.
func (tm *treeModel) ensureFocusVisible(height int) {
	n := len(tm.visible)
	if n == 0 || height <= 0 {
		tm.topIdx = 0
		return
	}
	idx := tm.indexOfFocused()
	if idx < 0 {
		tm.topIdx = 0
		return
	}
	if idx < tm.topIdx {
		tm.topIdx = idx
	} else if idx >= tm.topIdx+height {
		tm.topIdx = idx - height + 1
	}
	maxTop := max(n-height, 0)
	if tm.topIdx > maxTop {
		tm.topIdx = maxTop
	}
	if tm.topIdx < 0 {
		tm.topIdx = 0
	}
}

// itemsForFocus returns the indices of items directly attached to the
// focused group (after IncludePrivate filtering). Right-panel rendering
// consumes this in Task 3; Task 4 wires the list.Model.
func (tm *treeModel) itemsForFocus() []int {
	n := tm.focusedNode()
	if n == nil {
		return nil
	}
	out := make([]int, 0, len(n.leaves))
	for _, idx := range n.leaves {
		if !tm.includePrivate && tm.items[idx].Private {
			continue
		}
		out = append(out, idx)
	}
	return out
}

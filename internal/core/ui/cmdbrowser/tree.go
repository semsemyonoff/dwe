package cmdbrowser

import (
	"slices"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui/tree"
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

// cmdTreeAdapter is the cmdbrowser binding for the generic tree engine. The
// engine owns navigation, scroll, expansion, and the visible set; the adapter
// only exposes the three properties the engine needs to walk the graph.
type cmdTreeAdapter struct{}

func (cmdTreeAdapter) Children(n *treeNode) []*treeNode { return n.children }
func (cmdTreeAdapter) Key(n *treeNode) string           { return n.id }
func (cmdTreeAdapter) Expandable(n *treeNode) bool      { return len(n.children) > 0 }

// treeModel owns the group hierarchy and the cached counts; it is a thin
// wrapper over the shared *tree.Engine, which owns the expanded/focused state,
// the visible-row list, and scroll geometry. It is internal to cmdbrowser; the
// browser plugin wires keypresses into the engine.
type treeModel struct {
	items          []Item
	includePrivate bool
	root           *treeNode
	nodesByID      map[string]*treeNode

	// eng is the generic behavioral engine. cmdbrowser keeps nodesByID/root for
	// payload lookup and the breadcrumb's "(root)" mapping; everything behavioral
	// (cursor, expansion, visible set, scroll) lives in the engine.
	eng *tree.Engine[*treeNode]
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
		nodesByID:      map[string]*treeNode{},
		eng:            tree.New[*treeNode](cmdTreeAdapter{}),
	}
	tm.nodesByID[""] = tm.root
	tm.build()
	tm.recomputeCounts()
	tm.initExpansion(defaultDepth)
	// The invisible root is a consumer detail; its children are the engine roots.
	tm.eng.SetRoots(tm.root.children)
	tm.eng.RebuildVisible(nil)
	if vis := tm.eng.VisibleNodes(); len(vis) > 0 {
		tm.eng.SetCursorByKey(vis[0].id)
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
// regardless (it is rooted under the implicit root). Expansion is written to
// the engine by stable Key (the node id).
func (tm *treeModel) initExpansion(defaultDepth int) {
	if defaultDepth <= 0 {
		return
	}
	for id, n := range tm.nodesByID {
		if id == "" {
			continue
		}
		if n.depth < defaultDepth {
			tm.eng.SetExpandedByKey(id, true)
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

// focusedNode returns the engine's focused node, mapping the zero/nil cursor
// (the engine's root/none sentinel) back to the invisible root so callers that
// expect a *treeNode never see nil.
func (tm *treeModel) focusedNode() *treeNode {
	if n := tm.eng.Cursor(); n != nil {
		return n
	}
	return tm.root
}

// focusedID returns the dot-path id of the focused node, or "" for the
// root/none sentinel (the engine's nil cursor). It mirrors the legacy
// focusedID field for the breadcrumb, filter, and renderer.
func (tm *treeModel) focusedID() string {
	if n := tm.eng.Cursor(); n != nil {
		return n.id
	}
	return ""
}

// focusVisible reports whether the focused node currently appears in the
// engine's visible set. Used by the filter exit/commit cursor-restoration
// logic.
func (tm *treeModel) focusVisible() bool {
	cur := tm.eng.Cursor()
	if cur == nil {
		return false
	}
	return slices.Contains(tm.eng.VisibleNodes(), cur)
}

// nearestVisibleAncestor returns the id of the nearest ancestor of id (walking
// upward via groupOf) that currently appears in the visible slice. Returns ""
// when no non-root visible ancestor exists, meaning the right panel should show
// root-level commands.
func (tm *treeModel) nearestVisibleAncestor(id string) string {
	vis := tm.eng.VisibleNodes()
	visible := make(map[string]bool, len(vis))
	for _, n := range vis {
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

// itemsForFocus returns the indices of items directly attached to the
// focused group (after IncludePrivate filtering).
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

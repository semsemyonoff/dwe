package docstui

import (
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui/tree"
)

// TreeNode is a node in the docs browser navigation tree. It represents a file,
// directory, or heading row in the tree panel. Expansion state is NOT stored
// here — it lives in the shared tree.Engine, keyed by a stable Key (see
// docsTreeAdapter.Key) so it survives a locale Rebuild.
type TreeNode struct {
	Node     *docs.Node
	Parent   *TreeNode
	Children []*TreeNode
	RootName string // which DocRoot this node came from

	// IsGroup marks the synthetic per-root group header that appears only when
	// more than one DocRoot is shown. The engine Key uses it to keep group keys
	// (keyed by RootName) distinct from the file/dir node that shares the
	// group's Path (== RootName).
	IsGroup bool

	// Heading is non-nil when this TreeNode represents a markdown heading
	// inside the parent file (rather than a file or directory). The selection
	// handler uses it to scroll the viewport to the heading after loading the
	// parent file.
	Heading *docs.Heading

	// IndexNode, when non-nil, is the docs.Node of an `index.md` file that
	// lives directly inside this directory. The directory then borrows the
	// index file's H1 as its tree label (see nodeLabel) and displays the
	// index file's content when selected (see contentNodeFor); the index.md
	// itself is folded away and never appears as a separate row. Only set on
	// directory nodes.
	IndexNode *docs.Node
}

// docsTreeAdapter is the docstui binding for the generic tree engine. The
// engine owns navigation, scroll, expansion, and the visible set; the adapter
// only exposes the three properties the engine needs to walk the graph.
type docsTreeAdapter struct{}

// Children returns the ordered child nodes of n.
func (docsTreeAdapter) Children(n *TreeNode) []*TreeNode { return n.Children }

// Key returns a stable, collision-free id that survives a locale Rebuild. The
// three node kinds are prefixed so a group node (Path == RootName) cannot
// collide with the file/dir node that shares that path, and headings stay
// unique by their sibling index (locale-stable, unlike heading text).
func (docsTreeAdapter) Key(n *TreeNode) string { return nodeKey(n) }

// Expandable reports whether n can hold expansion state. Heading rows are
// always leaves. Directories are always expandable — empty / index-only dirs
// must stay Enter/Toggle-able. Files are expandable only when they expose
// heading sub-rows.
func (docsTreeAdapter) Expandable(n *TreeNode) bool {
	if n == nil || n.Node == nil {
		return false
	}
	if n.Heading != nil {
		return false
	}
	if n.Node.IsDir {
		return true
	}
	return len(n.Children) > 0
}

// nodeKey computes the stable engine Key for n. A nil/empty value yields "",
// which the engine treats as the root/none sentinel.
func nodeKey(n *TreeNode) string {
	if n == nil || n.Node == nil {
		return ""
	}
	if n.IsGroup {
		return "group\x00" + n.RootName
	}
	if n.Heading != nil {
		return "heading\x00" + n.RootName + "\x00" + n.Node.Path + "\x00" + strconv.Itoa(headingIndex(n))
	}
	return "node\x00" + n.RootName + "\x00" + n.Node.Path
}

// TreeWidget manages the navigation tree for the docs browser. It is a thin
// wrapper over the shared *tree.Engine, which owns the cursor, expansion,
// visible-row list, and scroll geometry; the widget keeps the node graph
// (tw.root) plus the per-root projection and the filter UX.
type TreeWidget struct {
	roots  []docs.DocRoot
	locale string // Locale used to pick per-file title/headings (with English fallback).
	root   *TreeNode

	// eng is the generic behavioral engine. docstui keeps tw.root for the node
	// graph and depth computation; everything behavioral (cursor, expansion,
	// visible set, scroll) lives in the engine.
	eng *tree.Engine[*TreeNode]

	// filter, when non-nil and Active, restricts visible nodes to those whose
	// label matches the query (and their ancestors so the hierarchy renders).
	// Set via ApplyFilter; nil/empty means "show everything per expansion state".
	filter *TreeFilter
}

// projectDweForTUI returns the user-facing projection of the canonical
// dwe docs tree: drops the `internals/` subtree (not user-facing) and
// promotes `reference/` children to top-level so the TUI doesn't show a
// redundant `reference/` folder. The `Node.Path` on each surviving node is
// unchanged (still `reference/foo.md`) so ResolveContent and
// content_hashes lookups keep working.
func projectDweForTUI(children []*docs.Node) []*docs.Node {
	out := make([]*docs.Node, 0, len(children))
	for _, child := range children {
		switch child.Name {
		case "internals":
			// Hidden — internals docs are not user-facing.
			continue
		case "reference":
			out = append(out, child.Children...)
		default:
			out = append(out, child)
		}
	}
	return out
}

// NewTreeWidget builds a navigation tree from the given doc roots and locale.
func NewTreeWidget(roots []docs.DocRoot, locale string) (*TreeWidget, error) {
	tw := &TreeWidget{
		roots:  roots,
		locale: locale,
		eng:    tree.New[*TreeNode](docsTreeAdapter{}),
	}
	if err := tw.buildGraph(); err != nil {
		return nil, err
	}
	// The invisible root is a consumer detail; its children are the engine roots.
	tw.eng.SetRoots(tw.root.Children)
	// Seed group nodes expanded by default (multi-root only). This runs on the
	// INITIAL build, after SetRoots but before the first RebuildVisible, so the
	// initial visible set + first-topic load are correct. Because the engine
	// keys expansion by stable Key, the user's later collapse/expand of a group
	// then persists across locale Rebuild (which deliberately does NOT re-seed).
	if len(tw.roots) > 1 {
		for _, g := range tw.root.Children {
			if g.IsGroup {
				tw.eng.SetExpanded(g, true)
			}
		}
	}
	if len(tw.root.Children) > 0 {
		tw.eng.SetCursor(tw.root.Children[0])
	}
	tw.recomputeVisible()
	return tw, nil
}

// Rebuild discards the current tree and re-walks the roots with the given
// locale, re-parsing per-file Title/Headings so the navigation reflects the
// translated text. Expansion state is preserved automatically: the engine keys
// it by stable Key, so SetRoots carries it across the rebuild (the docs
// expansion-across-locale bugfix). The cursor uses a two-tier restore — first
// the previous row's own Key, then (for a vanished heading) its parent file's
// Key, and only then ParkCursorIfHidden falls back to the first visible row.
func (tw *TreeWidget) Rebuild(locale string) error {
	prev := tw.eng.Cursor()
	var prevKey, prevParentFileKey string
	if prev != nil && prev.Node != nil {
		prevKey = nodeKey(prev)
		if prev.Heading != nil && prev.Parent != nil {
			prevParentFileKey = nodeKey(prev.Parent)
		}
	}

	tw.locale = locale
	if err := tw.buildGraph(); err != nil {
		return err
	}
	tw.eng.SetRoots(tw.root.Children)

	// Two-tier cursor restore (a vanished heading falls back to its parent
	// file, NOT first-visible). recomputeVisible's ParkCursorIfHidden handles
	// the final fallback when neither key resolves.
	if !tw.eng.SetCursorByKey(prevKey) {
		_ = tw.eng.SetCursorByKey(prevParentFileKey)
	}
	if cur := tw.eng.Cursor(); cur != nil {
		// Make sure the restored row is reachable in the visible set.
		tw.expandAncestors(cur)
	}
	tw.recomputeVisible()
	return nil
}

func (tw *TreeWidget) buildGraph() error {
	tw.root = &TreeNode{
		Node:     &docs.Node{Name: "root", IsDir: true},
		Children: []*TreeNode{},
	}

	useGroups := len(tw.roots) > 1

	for _, root := range tw.roots {
		tree, err := docs.BuildTree(root, tw.locale)
		if err != nil {
			continue
		}
		if tree == nil || tree.Children == nil {
			continue
		}
		// TUI-only projection of the dwe built-in docs: hide the
		// `internals/` subtree and promote `reference/` children to top
		// level. Done HERE — not in BuildTree — so non-TUI consumers
		// (notably `dwe docs export --include-internals`) still see the
		// full canonical tree.
		children := tree.Children
		if root.Name == "dwe" {
			children = projectDweForTUI(tree.Children)
		}
		if useGroups {
			// The "dwe" built-in root displays as the uppercase product
			// brand "DWE"; other roots use Title case (first letter only).
			var groupName string
			if root.Name == "dwe" {
				groupName = "DWE"
			} else {
				groupName = strings.ToUpper(root.Name[:1]) + root.Name[1:]
			}
			groupNode := &docs.Node{Name: groupName, IsDir: true, Path: root.Name}
			groupTreeNode := &TreeNode{
				Node:     groupNode,
				IsGroup:  true,
				Parent:   tw.root,
				Children: []*TreeNode{},
				RootName: root.Name,
			}
			tw.root.Children = append(tw.root.Children, groupTreeNode)
			for _, child := range children {
				tw.addNodeAsChild(child, groupTreeNode, root.Name)
			}
		} else {
			for _, child := range children {
				tw.addNodeAsChild(child, tw.root, root.Name)
			}
		}
	}
	return nil
}

func (tw *TreeWidget) addNodeAsChild(node *docs.Node, parent *TreeNode, rootName string) {
	treeNode := &TreeNode{
		Node:     node,
		Parent:   parent,
		Children: []*TreeNode{},
		RootName: rootName,
	}
	parent.Children = append(parent.Children, treeNode)
	switch {
	case node.IsDir && node.Children != nil:
		for _, child := range node.Children {
			// Fold an `index.md` into the directory itself: it becomes the
			// directory's displayed content and label (see nodeLabel /
			// contentNodeFor) instead of showing up as its own row.
			if !child.IsDir && child.Name == "index.md" {
				treeNode.IndexNode = child
				continue
			}
			tw.addNodeAsChild(child, treeNode, rootName)
		}
	case !node.IsDir && len(node.Headings) > 0:
		// Expose H2/H3 headings as expandable sub-rows under the file. The
		// child node has Heading set so loadTopic can scroll to the right
		// position after loading the parent file.
		for i := range node.Headings {
			h := &node.Headings[i]
			treeNode.Children = append(treeNode.Children, &TreeNode{
				Node:     node, // share the file's Node so RootName / Path resolve
				Parent:   treeNode,
				RootName: rootName,
				Heading:  h,
			})
		}
	}
}

// recomputeVisible rebuilds the engine's visible set and re-parks the cursor.
// When a non-empty filter query is active it passes a label-match keep
// predicate (the engine's ancestor-inclusion gives the reduced set); otherwise
// — including a freshly-opened filter with an empty query — it passes nil so
// the tree respects the expansion state rather than fully expanding.
func (tw *TreeWidget) recomputeVisible() {
	if tw.filter != nil && tw.filter.Active && tw.filter.Query != "" {
		tw.eng.RebuildVisible(func(n *TreeNode) bool {
			return tw.filter.Matches(nodeLabel(n))
		})
	} else {
		tw.eng.RebuildVisible(nil)
	}
	tw.eng.ParkCursorIfHidden()
}

// ApplyFilter swaps in a new filter and recomputes visible. Pass nil to
// remove the filter entirely; pass a closed filter to keep the reference but
// behave as if no filter were set.
func (tw *TreeWidget) ApplyFilter(f *TreeFilter) {
	tw.filter = f
	tw.recomputeVisible()
}

// expandAncestors walks the parent chain of n and marks every parent as
// expanded in the engine so the node stays in the visible set when the filter
// is dropped. No-op when n is nil. Used by the filter Enter path and the
// locale Rebuild cursor restore to keep the selection reachable.
func (tw *TreeWidget) expandAncestors(n *TreeNode) {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Parent != nil {
			tw.eng.SetExpanded(cur.Parent, true)
		}
	}
}

// nodeLabel returns the user-visible label for a tree node — used by filter
// matching so users find rows by what they read, not by raw filenames.
func nodeLabel(node *TreeNode) string {
	if node == nil || node.Node == nil {
		return ""
	}
	if node.Heading != nil {
		return node.Heading.Text
	}
	if node.Node.IsDir {
		// A directory with a folded index.md reads as that file's H1.
		if node.IndexNode != nil && node.IndexNode.Title != "" {
			return node.IndexNode.Title
		}
		return node.Node.Name
	}
	if node.Node.Title != "" {
		return node.Node.Title
	}
	return node.Node.Name
}

// Cursor returns the currently focused tree node.
func (tw *TreeWidget) Cursor() *TreeNode {
	return tw.eng.Cursor()
}

// SetCursor moves the cursor to the given node.
func (tw *TreeWidget) SetCursor(node *TreeNode) {
	if node != nil {
		tw.eng.SetCursor(node)
	}
}

// IsExpanded reports whether node is currently expanded in the engine.
func (tw *TreeWidget) IsExpanded(node *TreeNode) bool {
	return tw.eng.IsExpanded(node)
}

// SetExpanded sets node's expansion flag in the engine (no visible rebuild;
// callers follow with recomputeVisible).
func (tw *TreeWidget) SetExpanded(node *TreeNode, b bool) {
	tw.eng.SetExpanded(node, b)
}

// MoveUp moves the cursor one row up in the visible tree.
func (tw *TreeWidget) MoveUp() { tw.eng.MoveUp() }

// MoveDown moves the cursor one row down in the visible tree.
func (tw *TreeWidget) MoveDown() { tw.eng.MoveDown() }

// MoveBy moves the cursor n rows (clamped once) — used for a coalesced wheel
// delta so a large jump is not an O(|n|) loop of single-row moves.
func (tw *TreeWidget) MoveBy(n int) { tw.eng.MoveBy(n) }

// MoveStart moves the cursor to the first visible row.
func (tw *TreeWidget) MoveStart() { tw.eng.MoveHome() }

// MoveEnd moves the cursor to the last visible row.
func (tw *TreeWidget) MoveEnd() { tw.eng.MoveEnd() }

// Collapse implements ←/h directional semantics: if the cursor node is
// expanded, collapse it; otherwise step the cursor up to its parent. Heading
// rows are leaves so they always fall through to the "step to parent" branch.
// Nodes at the root level (no parent above root) are no-ops.
//
// Invariant: the engine's Collapse/Expand/Toggle internally call
// RebuildVisible(nil), which ignores any active filter and rebuilds the full
// expansion-respecting set. This is safe only because h/l/Enter are unreachable
// while the filter captures input (CapturingInput routes raw keys to the
// filter's own handler). If a future change lets these fire during an active
// filter, recompute the reduced set afterward instead of relying on the engine.
func (tw *TreeWidget) Collapse() { tw.eng.Collapse() }

// Expand implements →/l directional semantics: if the cursor node is
// collapsed and has children, expand it; if already expanded, step the
// cursor into the first child. Heading rows (leaves) and nodes without
// children are no-ops.
func (tw *TreeWidget) Expand() { tw.eng.Expand() }

// Toggle flips the expanded state of the cursor when it sits on a directory
// (including empty / index-only directories, whose glyph then flips ▶ ↔ ▼ as
// it did before the engine refactor) or on a file that has heading sub-rows.
// Heading rows themselves are leaves and the call is a no-op there.
func (tw *TreeWidget) Toggle() { tw.eng.Toggle() }

// VisibleNodes returns the slice of currently visible tree rows.
func (tw *TreeWidget) VisibleNodes() []*TreeNode {
	return tw.eng.VisibleNodes()
}

// GetPath returns the file path for the given node, or empty string for nil/dir.
func (tw *TreeWidget) GetPath(node *TreeNode) string {
	if node == nil || node.Node == nil {
		return ""
	}
	return node.Node.Path
}

// IsDir reports whether node represents a directory.
func (tw *TreeWidget) IsDir(node *TreeNode) bool {
	if node == nil || node.Node == nil {
		return false
	}
	return node.Node.IsDir
}

// GetName returns the display name of node.
func (tw *TreeWidget) GetName(node *TreeNode) string {
	if node == nil || node.Node == nil {
		return ""
	}
	return node.Node.Name
}

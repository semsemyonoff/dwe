package tui

import (
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/docs"
)

type TreeNode struct {
	Node     *docs.Node
	Expanded bool
	Parent   *TreeNode
	Children []*TreeNode
	RootName string // which DocRoot this node came from

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

type TreeWidget struct {
	roots   []docs.DocRoot
	locale  string // Locale used to pick per-file title/headings (with English fallback).
	root    *TreeNode
	cursor  *TreeNode
	visible []*TreeNode

	// filter, when non-nil and Active, restricts visible nodes to those whose
	// label matches the query (and their ancestors so the hierarchy renders).
	// Set via ApplyFilter; nil/empty means "show everything per Expanded state".
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

func NewTreeWidget(roots []docs.DocRoot, locale string) (*TreeWidget, error) {
	tw := &TreeWidget{roots: roots, locale: locale}
	if err := tw.rebuild(); err != nil {
		return nil, err
	}
	return tw, nil
}

// Rebuild discards the current tree and re-walks the roots with the given
// locale, re-parsing per-file Title/Headings so the navigation reflects the
// translated text. It tries to keep the user on the same row by matching
// the previous cursor's RootName + Path (and, for heading rows, the heading
// index in the parent file); when the rebuild changes the tree shape enough
// that the previous cursor can't be located, it falls back to the first
// visible row.
func (tw *TreeWidget) Rebuild(locale string) error {
	prev := tw.cursor
	var prevPath, prevRoot string
	var prevHeadingIdx = -1
	var prevWasDir bool
	if prev != nil && prev.Node != nil {
		prevPath = prev.Node.Path
		prevRoot = prev.RootName
		prevWasDir = prev.Node.IsDir
		if prev.Heading != nil && prev.Parent != nil {
			for i, sib := range prev.Parent.Children {
				if sib == prev {
					prevHeadingIdx = i
					break
				}
			}
		}
	}

	tw.locale = locale
	tw.cursor = nil
	if err := tw.rebuild(); err != nil {
		return err
	}

	if prevPath == "" && !prevWasDir {
		return nil
	}
	if match := tw.findByPath(tw.root, prevRoot, prevPath, prevWasDir); match != nil {
		if prevHeadingIdx >= 0 && prevHeadingIdx < len(match.Children) {
			tw.cursor = match.Children[prevHeadingIdx]
			// Make sure the heading row is reachable in `visible`.
			expandAncestors(tw.cursor)
		} else {
			tw.cursor = match
			expandAncestors(tw.cursor)
		}
		tw.recomputeVisible()
	}
	return nil
}

// findByPath walks the tree and returns the file-or-directory node matching
// the (rootName, path, isDir) tuple. Heading children are skipped — callers
// re-pick a heading by index off the returned file node.
func (tw *TreeWidget) findByPath(node *TreeNode, rootName, path string, isDir bool) *TreeNode {
	if node == nil {
		return nil
	}
	if node.Node != nil && node.Heading == nil &&
		node.RootName == rootName &&
		node.Node.Path == path &&
		node.Node.IsDir == isDir {
		return node
	}
	for _, child := range node.Children {
		if got := tw.findByPath(child, rootName, path, isDir); got != nil {
			return got
		}
	}
	return nil
}

func (tw *TreeWidget) rebuild() error {
	tw.root = &TreeNode{
		Node:     &docs.Node{Name: "root", IsDir: true},
		Expanded: true,
		Children: []*TreeNode{},
	}
	tw.visible = nil

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
				Expanded: true,
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

	if tw.cursor == nil && tw.root.Children != nil {
		tw.cursor = tw.root.Children[0]
	}
	tw.recomputeVisible()
	return nil
}

func (tw *TreeWidget) addNodeAsChild(node *docs.Node, parent *TreeNode, rootName string) {
	treeNode := &TreeNode{
		Node:     node,
		Expanded: false,
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

func (tw *TreeWidget) recomputeVisible() {
	tw.visible = nil
	if tw.filter != nil && tw.filter.Active && tw.filter.Query != "" {
		matched := tw.markFilterMatches(tw.root)
		tw.emitFiltered(tw.root, matched)
		tw.ensureCursorVisible()
		return
	}
	tw.walkVisible(tw.root)
	tw.ensureCursorVisible()
}

func (tw *TreeWidget) walkVisible(node *TreeNode) {
	if node != tw.root {
		tw.visible = append(tw.visible, node)
	}
	if node.Expanded && node.Children != nil {
		for _, child := range node.Children {
			tw.walkVisible(child)
		}
	}
}

// markFilterMatches walks the tree and returns a set of nodes that should
// be visible under the active filter: a node matches itself, OR has any
// descendant that matches. This is the two-phase counterpart of the old
// in-place algorithm — the old version only included a node when one of
// its own ancestors was also added during the same pass, which silently
// dropped parent files of matching headings (the user saw a lone H2 row
// floating with no file context, or, worse, nothing at all when ancestor
// labels did not match the query either).
func (tw *TreeWidget) markFilterMatches(node *TreeNode) map[*TreeNode]bool {
	matched := map[*TreeNode]bool{}
	var walk func(*TreeNode) bool
	walk = func(n *TreeNode) bool {
		if n == nil {
			return false
		}
		any := false
		if n != tw.root && tw.filter.Matches(nodeLabel(n)) {
			matched[n] = true
			any = true
		}
		for _, c := range n.Children {
			if walk(c) {
				matched[n] = true
				any = true
			}
		}
		return any
	}
	walk(node)
	return matched
}

// emitFiltered appends matched nodes to tw.visible in pre-order so the
// rendered tree preserves the hierarchy. The root is never appended (the
// walkVisible / renderTree contract treats root as a header, not a row).
func (tw *TreeWidget) emitFiltered(node *TreeNode, matched map[*TreeNode]bool) {
	if node == nil {
		return
	}
	if node != tw.root {
		if !matched[node] {
			return
		}
		tw.visible = append(tw.visible, node)
	}
	for _, c := range node.Children {
		tw.emitFiltered(c, matched)
	}
}

// ensureCursorVisible reparks the cursor on the first visible node when the
// current cursor disappears under the active filter.
func (tw *TreeWidget) ensureCursorVisible() {
	if tw.cursor == nil {
		if len(tw.visible) > 0 {
			tw.cursor = tw.visible[0]
		}
		return
	}
	if slices.Contains(tw.visible, tw.cursor) {
		return
	}
	if len(tw.visible) > 0 {
		tw.cursor = tw.visible[0]
	}
}

// ApplyFilter swaps in a new filter and recomputes visible. Pass nil to
// remove the filter entirely; pass a closed filter to keep the reference but
// behave as if no filter were set.
func (tw *TreeWidget) ApplyFilter(f *TreeFilter) {
	tw.filter = f
	tw.recomputeVisible()
}

// expandAncestors walks the parent chain of n and marks every directory /
// file-with-headings parent as Expanded so the node stays in the visible
// set when the filter is dropped. No-op when n is nil or already at the
// root. Used by the filter Enter path to preserve the user's selection
// across filter close.
func expandAncestors(n *TreeNode) {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Parent != nil {
			cur.Parent.Expanded = true
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

func (tw *TreeWidget) Cursor() *TreeNode {
	return tw.cursor
}

func (tw *TreeWidget) SetCursor(node *TreeNode) {
	if node != nil {
		tw.cursor = node
	}
}

func (tw *TreeWidget) MoveUp() {
	if tw.cursor == nil {
		return
	}
	idx := tw.indexOfNode(tw.cursor)
	if idx > 0 {
		tw.cursor = tw.visible[idx-1]
	}
}

func (tw *TreeWidget) MoveDown() {
	if tw.cursor == nil {
		return
	}
	idx := tw.indexOfNode(tw.cursor)
	if idx < len(tw.visible)-1 {
		tw.cursor = tw.visible[idx+1]
	}
}

func (tw *TreeWidget) MoveStart() {
	if len(tw.visible) > 0 {
		tw.cursor = tw.visible[0]
	}
}

func (tw *TreeWidget) MoveEnd() {
	if len(tw.visible) > 0 {
		tw.cursor = tw.visible[len(tw.visible)-1]
	}
}

func (tw *TreeWidget) indexOfNode(node *TreeNode) int {
	for i, n := range tw.visible {
		if n == node {
			return i
		}
	}
	return -1
}

// Toggle flips the expanded state of the cursor when it sits on a directory
// or on a file that has heading sub-rows. Heading rows themselves are leaves
// and the call is a no-op there.
func (tw *TreeWidget) Toggle() {
	if tw.cursor == nil || tw.cursor.Node == nil {
		return
	}
	if !tw.cursor.Node.IsDir && len(tw.cursor.Children) == 0 {
		return
	}
	if tw.cursor.Heading != nil {
		return
	}
	tw.cursor.Expanded = !tw.cursor.Expanded
	tw.recomputeVisible()
}

func (tw *TreeWidget) VisibleNodes() []*TreeNode {
	return tw.visible
}

func (tw *TreeWidget) GetPath(node *TreeNode) string {
	if node == nil || node.Node == nil {
		return ""
	}
	return node.Node.Path
}

func (tw *TreeWidget) IsDir(node *TreeNode) bool {
	if node == nil || node.Node == nil {
		return false
	}
	return node.Node.IsDir
}

func (tw *TreeWidget) GetName(node *TreeNode) string {
	if node == nil || node.Node == nil {
		return ""
	}
	return node.Node.Name
}

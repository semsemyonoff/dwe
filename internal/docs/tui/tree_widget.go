package tui

import (
	"slices"
	"strings"

	"devbox-cli/internal/docs"
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
}

type TreeWidget struct {
	roots   []docs.DocRoot
	root    *TreeNode
	cursor  *TreeNode
	visible []*TreeNode

	// filter, when non-nil and Active, restricts visible nodes to those whose
	// label matches the query (and their ancestors so the hierarchy renders).
	// Set via ApplyFilter; nil/empty means "show everything per Expanded state".
	filter *TreeFilter
}

// projectDevboxForTUI returns the user-facing projection of the canonical
// devbox docs tree: drops the `internals/` subtree (not user-facing) and
// promotes `reference/` children to top-level so the TUI doesn't show a
// redundant `reference/` folder. The `Node.Path` on each surviving node is
// unchanged (still `reference/foo.md`) so ResolveContent and
// content_hashes lookups keep working.
func projectDevboxForTUI(children []*docs.Node) []*docs.Node {
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

func NewTreeWidget(roots []docs.DocRoot) (*TreeWidget, error) {
	tw := &TreeWidget{roots: roots}
	if err := tw.rebuild(); err != nil {
		return nil, err
	}
	return tw, nil
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
		tree, err := docs.BuildTree(root)
		if err != nil {
			continue
		}
		if tree == nil || tree.Children == nil {
			continue
		}
		// TUI-only projection of the devbox built-in docs: hide the
		// `internals/` subtree and promote `reference/` children to top
		// level. Done HERE — not in BuildTree — so non-TUI consumers
		// (notably `devbox docs export --include-internals`) still see the
		// full canonical tree.
		children := tree.Children
		if root.Name == "devbox" {
			children = projectDevboxForTUI(tree.Children)
		}
		if useGroups {
			groupName := strings.ToUpper(root.Name[:1]) + root.Name[1:]
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
		tw.walkFiltered(tw.root, false)
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

// walkFiltered emits a node when it matches the filter, plus any ancestor on
// the path to a match (so the user sees where the match lives in the tree).
// ancestorAdded tracks whether the parent chain has already been added to
// `visible`, preventing duplicates when a directory has multiple matching
// descendants.
func (tw *TreeWidget) walkFiltered(node *TreeNode, ancestorAdded bool) bool {
	selfMatch := node != tw.root && tw.filter.Matches(nodeLabel(node))
	childMatch := false
	if node.Children != nil {
		// Walk children first so we know whether to include this node as an
		// ancestor of a match.
		appendIdx := len(tw.visible)
		if node != tw.root && (selfMatch || ancestorAdded) {
			tw.visible = append(tw.visible, node)
			ancestorAdded = true
		}
		for _, child := range node.Children {
			if tw.walkFiltered(child, ancestorAdded) {
				childMatch = true
			}
		}
		if !selfMatch && !childMatch && node != tw.root {
			// We tentatively added this node but no descendant matched —
			// roll it back. Only roll back if we were the one to add it.
			if appendIdx < len(tw.visible) && tw.visible[appendIdx] == node {
				tw.visible = append(tw.visible[:appendIdx], tw.visible[appendIdx+1:]...)
			}
		}
	} else if selfMatch && node != tw.root {
		tw.visible = append(tw.visible, node)
	}
	return selfMatch || childMatch
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

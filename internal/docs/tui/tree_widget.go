package tui

import (
	"strings"

	"devbox-cli/internal/docs"
)

type TreeNode struct {
	Node     *docs.Node
	Expanded bool
	Parent   *TreeNode
	Children []*TreeNode
	RootName string // which DocRoot this node came from
}

type TreeWidget struct {
	roots   []docs.DocRoot
	root    *TreeNode
	cursor  *TreeNode
	visible []*TreeNode
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
			for _, child := range tree.Children {
				tw.addNodeAsChild(child, groupTreeNode, root.Name)
			}
		} else {
			for _, child := range tree.Children {
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
	if node.IsDir && node.Children != nil {
		for _, child := range node.Children {
			tw.addNodeAsChild(child, treeNode, rootName)
		}
	}
}

func (tw *TreeWidget) recomputeVisible() {
	tw.visible = nil
	tw.walkVisible(tw.root)
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

func (tw *TreeWidget) Toggle() {
	if tw.cursor == nil || tw.cursor.Node == nil || !tw.cursor.Node.IsDir {
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

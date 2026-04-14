package render

import (
	"fmt"
	"strings"
)

// TreeNode is a generic tree node for hierarchical display.
type TreeNode struct {
	// Label is the primary display name for this node.
	Label string
	// Tags are short annotations shown in brackets after the label (e.g. "private", "workflow").
	Tags []string
	// Desc is a human-readable description shown after "—".
	Desc string
	// Children are nested nodes rendered at the next indentation level.
	Children []*TreeNode
}

// WriteTree renders roots and their descendants with two-space indentation per level.
// Group nodes (those with children) are printed without type tags; leaf nodes carry
// their full tag list.
func (w *Writer) WriteTree(roots []*TreeNode) {
	for _, node := range roots {
		w.writeTreeNode(node, 0)
	}
}

func (w *Writer) writeTreeNode(node *TreeNode, depth int) {
	indent := strings.Repeat("  ", depth)
	var sb strings.Builder
	sb.WriteString(indent)
	sb.WriteString(node.Label)
	if len(node.Tags) > 0 {
		sb.WriteString("  [")
		sb.WriteString(strings.Join(node.Tags, ", "))
		sb.WriteString("]")
	}
	if node.Desc != "" {
		sb.WriteString(" — ")
		sb.WriteString(node.Desc)
	}
	_, _ = fmt.Fprintln(w.w, sb.String())
	for _, child := range node.Children {
		w.writeTreeNode(child, depth+1)
	}
}

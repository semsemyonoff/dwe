package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *Model) renderTwoPanel() tea.View {
	// Simple two-panel layout
	treeContent := m.renderTree()
	viewportContent := m.Viewport.View()
	statusContent := m.StatusBar.View()

	// Create borders
	border := lipgloss.NormalBorder()
	treePanelWidth := 30
	viewportWidth := m.TermWidth - treePanelWidth - 4

	treeBorderColor := "0"
	viewportBorderColor := "0"
	if m.FocusZone == FocusTree {
		treeBorderColor = "5"
	} else {
		viewportBorderColor = "5"
	}

	treePanel := lipgloss.NewStyle().
		Border(border).
		Width(treePanelWidth).
		Height(m.TermHeight - 2).
		BorderForeground(lipgloss.Color(treeBorderColor)).
		Render(treeContent)

	viewportPanel := lipgloss.NewStyle().
		Border(border).
		Width(viewportWidth).
		Height(m.TermHeight - 2).
		BorderForeground(lipgloss.Color(viewportBorderColor)).
		Render(viewportContent)

	body := lipgloss.JoinHorizontal(lipgloss.Top, treePanel, viewportPanel)

	content := strings.Join([]string{body, statusContent}, "\n")
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m *Model) renderTree() string {
	if m.Tree == nil || m.Tree.VisibleNodes() == nil {
		return ""
	}

	var sb strings.Builder
	for i, node := range m.Tree.VisibleNodes() {
		if i > 0 {
			sb.WriteString("\n")
		}

		// Calculate indentation based on depth
		depth := m.getNodeDepth(node)
		indent := strings.Repeat("  ", depth)

		// Cursor indicator
		cursor := "  "
		if node == m.Tree.Cursor() {
			cursor = "> "
		}

		// Expand/collapse indicator for directories
		if node.Node.IsDir {
			if node.Expanded {
				sb.WriteString(indent + cursor + "▼ " + node.Node.Name)
			} else {
				sb.WriteString(indent + cursor + "▶ " + node.Node.Name)
			}
		} else {
			sb.WriteString(indent + cursor + "  " + node.Node.Name)
		}
	}

	return sb.String()
}

func (m *Model) getNodeDepth(node *TreeNode) int {
	depth := 0
	current := node
	for current != nil && current.Parent != nil {
		if current.Parent.Node.Name != "root" {
			depth++
		}
		current = current.Parent
	}
	return depth
}

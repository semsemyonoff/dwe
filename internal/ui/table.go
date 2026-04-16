package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// RenderTable builds and returns a Lipgloss table string using the shared
// styleTableBorder and styleTableHeader style vars. Headers and rows are
// rendered with the package-level table styles (configurable via ApplyStyles).
//
// headers contains the column names; rows contains the data rows, each a slice
// of strings with the same length as headers.
func RenderTable(headers []string, rows [][]string) string {
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers(headers...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
			}
			return lipgloss.NewStyle()
		})

	for _, r := range rows {
		t.Row(r...)
	}

	return t.String()
}

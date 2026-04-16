package ui

import (
	"fmt"

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

// ServiceTableRow holds data for one row in the services Lipgloss table.
type ServiceTableRow struct {
	Name      string
	Container string
	Mandatory bool
	Enabled   bool
	// Running is only meaningful when Mandatory or Enabled is true.
	Running bool
}

// RenderServiceTable renders a styled Lipgloss table of services.
// Columns: NAME, CONTAINER, STATE, RUNNING.
// Row colors use semantic style vars: mandatory (bold), enabled (green), disabled (gray).
func RenderServiceTable(rows []ServiceTableRow) string {
	// Build string rows and capture per-row styles in parallel slices.
	stringRows := make([][]string, len(rows))
	rowStyles := make([]lipgloss.Style, len(rows))

	for i, r := range rows {
		var stateStr, runStr string
		var rowStyle lipgloss.Style

		switch {
		case r.Mandatory:
			stateStr = "mandatory"
			rowStyle = styleMandatory
		case r.Enabled:
			stateStr = "enabled"
			rowStyle = styleEnabled
		default:
			stateStr = "disabled"
			rowStyle = styleDisabled
		}

		if r.Mandatory || r.Enabled {
			if r.Running {
				runStr = "running"
			} else {
				runStr = "stopped"
			}
		} else {
			runStr = "—"
		}

		stringRows[i] = []string{r.Name, r.Container, stateStr, runStr}
		rowStyles[i] = rowStyle
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers("NAME", "CONTAINER", "STATE", "RUNNING").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
			}
			if row >= 0 && row < len(rowStyles) {
				return rowStyles[row]
			}
			return lipgloss.NewStyle()
		})

	for _, r := range stringRows {
		t.Row(r...)
	}

	return t.String()
}

// ToolTableRow holds data for one row in the tools Lipgloss table.
type ToolTableRow struct {
	Name      string
	Host      string
	Port      int
	Container string
	Enabled   bool
	// Running is only meaningful when Enabled is true.
	Running bool
}

// RenderToolTable renders a styled Lipgloss table of optional tools.
// Columns: NAME, HOST, PORT, STATE, RUNNING.
// Row colors use semantic style vars: enabled (green), disabled (gray).
func RenderToolTable(rows []ToolTableRow) string {
	stringRows := make([][]string, len(rows))
	rowStyles := make([]lipgloss.Style, len(rows))

	for i, r := range rows {
		var stateStr, runStr string
		var rowStyle lipgloss.Style

		if r.Enabled {
			stateStr = "enabled"
			rowStyle = styleEnabled
		} else {
			stateStr = "disabled"
			rowStyle = styleDisabled
		}

		if r.Enabled {
			if r.Running {
				runStr = "running"
			} else {
				runStr = "stopped"
			}
		} else {
			runStr = "—"
		}

		portStr := fmt.Sprintf("%d", r.Port)
		if r.Port == 0 {
			portStr = "—"
		}

		stringRows[i] = []string{r.Name, r.Host, portStr, stateStr, runStr}
		rowStyles[i] = rowStyle
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers("NAME", "HOST", "PORT", "STATE", "RUNNING").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
			}
			if row >= 0 && row < len(rowStyles) {
				return rowStyles[row]
			}
			return lipgloss.NewStyle()
		})

	for _, r := range stringRows {
		t.Row(r...)
	}

	return t.String()
}

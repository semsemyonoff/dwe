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

// rowCellStyle holds per-column styles for a single table row.
type rowCellStyle struct {
	base  lipgloss.Style // NAME, CONTAINER, HOST, PORT columns
	state lipgloss.Style // STATE column
	run   lipgloss.Style // RUNNING column
}

// RenderServiceTable renders a styled Lipgloss table of services.
// Columns: NAME, CONTAINER, STATE, RUNNING.
// Cell colors depend on the service state: disabled=gray, enabled=white,
// STATE column reflects the state value, RUNNING column is green/red/gray.
func RenderServiceTable(rows []ServiceTableRow) string {
	stringRows := make([][]string, len(rows))
	cellStyles := make([]rowCellStyle, len(rows))

	for i, r := range rows {
		var stateStr, runStr string
		var cs rowCellStyle

		switch {
		case r.Mandatory:
			stateStr = "mandatory"
			cs.base = lipgloss.NewStyle()
			cs.state = styleMandatory
		case r.Enabled:
			stateStr = "enabled"
			cs.base = lipgloss.NewStyle()
			cs.state = lipgloss.NewStyle()
		default:
			stateStr = "disabled"
			cs.base = styleDisabled
			cs.state = styleDisabled
		}

		if r.Mandatory || r.Enabled {
			if r.Running {
				runStr = "running"
				cs.run = styleEnabled
			} else {
				runStr = "stopped"
				cs.run = styleRunStopped
			}
		} else {
			runStr = "—"
			cs.run = styleDisabled
		}

		stringRows[i] = []string{r.Name, r.Container, stateStr, runStr}
		cellStyles[i] = cs
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers("NAME", "CONTAINER", "STATE", "RUNNING").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
			}
			if row < 0 || row >= len(cellStyles) {
				return lipgloss.NewStyle()
			}
			cs := cellStyles[row]
			switch col {
			case 2:
				return cs.state
			case 3:
				return cs.run
			default:
				return cs.base
			}
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
// Cell colors depend on the tool state: disabled=gray, enabled=white,
// STATE column reflects the state value, RUNNING column is green/red/gray.
func RenderToolTable(rows []ToolTableRow) string {
	stringRows := make([][]string, len(rows))
	cellStyles := make([]rowCellStyle, len(rows))

	for i, r := range rows {
		var stateStr, runStr string
		var cs rowCellStyle

		if r.Enabled {
			stateStr = "enabled"
			cs.base = lipgloss.NewStyle()
			cs.state = lipgloss.NewStyle()
			if r.Running {
				runStr = "running"
				cs.run = styleEnabled
			} else {
				runStr = "stopped"
				cs.run = styleRunStopped
			}
		} else {
			stateStr = "disabled"
			runStr = "—"
			cs.base = styleDisabled
			cs.state = styleDisabled
			cs.run = styleDisabled
		}

		portStr := fmt.Sprintf("%d", r.Port)
		if r.Port == 0 {
			portStr = "—"
		}

		stringRows[i] = []string{r.Name, r.Host, portStr, stateStr, runStr}
		cellStyles[i] = cs
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers("NAME", "HOST", "PORT", "STATE", "RUNNING").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
			}
			if row < 0 || row >= len(cellStyles) {
				return lipgloss.NewStyle()
			}
			cs := cellStyles[row]
			switch col {
			case 3:
				return cs.state
			case 4:
				return cs.run
			default:
				return cs.base
			}
		})

	for _, r := range stringRows {
		t.Row(r...)
	}

	return t.String()
}

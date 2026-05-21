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
	// Extras holds custom status-column values keyed by column name.
	// Missing keys render as "—".
	Extras map[string]string
}

// rowCellStyle holds per-column styles for a single table row.
type rowCellStyle struct {
	base  lipgloss.Style // NAME, CONTAINER, HOST, PORT columns
	state lipgloss.Style // STATE column
	run   lipgloss.Style // RUNNING column
}

// RenderServiceTable renders a styled Lipgloss table of services.
// Built-in columns: NAME, CONTAINER, STATE, RUNNING.
// extraCols, if non-nil, lists additional column names appended after the
// built-ins; each row's value is read from ServiceTableRow.Extras (missing
// key → "—"). Pass nil to render the table with only the built-in columns.
func RenderServiceTable(rows []ServiceTableRow, extraCols []string) string {
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

		row := []string{r.Name, r.Container, stateStr, runStr}
		for _, col := range extraCols {
			row = append(row, extraCell(r.Extras, col))
		}
		stringRows[i] = row
		cellStyles[i] = cs
	}

	headers := append([]string{"NAME", "CONTAINER", "STATE", "RUNNING"}, extraCols...)
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers(headers...).
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

// extraCell returns the value for col in extras, or "—" if missing.
func extraCell(extras map[string]string, col string) string {
	if v, ok := extras[col]; ok {
		return v
	}
	return "—"
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
	// Extras holds custom status-column values keyed by column name.
	// Missing keys render as "—".
	Extras map[string]string
}

// RenderToolTable renders a styled Lipgloss table of optional tools.
// Built-in columns: NAME, HOST, PORT, STATE, RUNNING.
// extraCols, if non-nil, lists additional column names appended after the
// built-ins; each row's value is read from ToolTableRow.Extras (missing
// key → "—"). Pass nil to render the table with only the built-in columns.
func RenderToolTable(rows []ToolTableRow, extraCols []string) string {
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

		row := []string{r.Name, r.Host, portStr, stateStr, runStr}
		for _, col := range extraCols {
			row = append(row, extraCell(r.Extras, col))
		}
		stringRows[i] = row
		cellStyles[i] = cs
	}

	headers := append([]string{"NAME", "HOST", "PORT", "STATE", "RUNNING"}, extraCols...)
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers(headers...).
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

// DaemonTableRow holds the data for one row in the daemons Lipgloss table.
// Values are passed in already-sanitised by the collector.
type DaemonTableRow struct {
	ID        string
	Name      string
	Container string
	Uptime    string
}

// RenderDaemonTable renders a styled Lipgloss table of running daemons.
// Columns: ID, NAME, CONTAINER, UPTIME. Empty input returns an empty string.
func RenderDaemonTable(rows []DaemonTableRow) string {
	if len(rows) == 0 {
		return ""
	}
	stringRows := make([][]string, len(rows))
	for i, r := range rows {
		name := r.Name
		if name == "" {
			name = "—"
		}
		stringRows[i] = []string{r.ID, name, r.Container, r.Uptime}
	}
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers("ID", "NAME", "CONTAINER", "UPTIME").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
			}
			return lipgloss.NewStyle()
		})
	for _, r := range stringRows {
		t.Row(r...)
	}
	return t.String()
}

// DeployStatusRow holds data for one row in the deploy status table.
// Imported from statusview, re-exported here for brevity in test utilities.
type DeployStatusRow struct {
	Service         string
	Status          string // journal.Status
	ConfigDelta     string // ConfigDelta: "ok" | "changed" | "missing"
	PrevHashShort   string
	CurrHashShort   string
	LastFailedPhase string
	LastFailedStep  string
}

// statusStyleForDelta returns the style for a config-delta cell.
func statusStyleForDelta(delta string) lipgloss.Style {
	switch delta {
	case "ok":
		return styleEnabled
	case "changed":
		return stylePartial
	case "missing":
		return styleWarn
	default:
		return lipgloss.NewStyle()
	}
}

// statusStyleForStatus returns the style for a deploy status cell.
func statusStyleForStatus(status string) lipgloss.Style {
	switch status {
	case "deployed":
		return styleEnabled
	case "partial", "in_progress":
		return stylePartial
	case "failed":
		return styleRunStopped
	case "not_deployed", "skipped":
		return styleMuted
	default:
		return lipgloss.NewStyle()
	}
}

// RenderDeployStatus renders a styled Lipgloss table of deploy status per service.
// Columns: SERVICE, STATUS, CONFIG, PREV HASH, CURR HASH, LAST FAILED.
func RenderDeployStatus(rows []DeployStatusRow) string {
	stringRows := make([][]string, len(rows))
	statusStyles := make([]string, len(rows))
	deltaStyles := make([]string, len(rows))

	for i, r := range rows {
		lastFailedStr := "—"
		if r.LastFailedPhase != "" {
			lastFailedStr = r.LastFailedPhase
			if r.LastFailedStep != "" {
				lastFailedStr += " / " + r.LastFailedStep
			}
		}

		stringRows[i] = []string{
			r.Service,
			r.Status,
			r.ConfigDelta,
			r.PrevHashShort,
			r.CurrHashShort,
			lastFailedStr,
		}
		statusStyles[i] = r.Status
		deltaStyles[i] = r.ConfigDelta
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers("SERVICE", "STATUS", "CONFIG", "PREV HASH", "CURR HASH", "LAST FAILED").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
			}
			if row < 0 || row >= len(statusStyles) {
				return lipgloss.NewStyle()
			}
			switch col {
			case 1: // STATUS
				return statusStyleForStatus(statusStyles[row])
			case 2: // CONFIG
				return statusStyleForDelta(deltaStyles[row])
			default:
				return lipgloss.NewStyle()
			}
		})

	for _, r := range stringRows {
		t.Row(r...)
	}

	return t.String()
}

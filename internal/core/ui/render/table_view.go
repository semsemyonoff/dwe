package render

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// tableView describes one table's data, per-column width behavior, and
// styling, and decides for itself whether to render as a table or fall back
// to the record layout under width pressure. See table_fit.go for
// columnSpec and the fitting algorithm, and table_record.go for the record
// layout.
type tableView struct {
	Headers []string
	Rows    [][]string
	Cols    []columnSpec
	// Style supplies semantic color only (severity, status, dirty-flag, …).
	// Table-only decoration — padding, alignment, zebra background — is
	// composed on top by renderTable/styleFunc and must not be set here, so
	// the same styling survives the switch to record mode.
	Style func(row, col int) lipgloss.Style

	// Table-mode decoration; ignored by record mode.
	Padding   int  // horizontal cell padding (diagnostics: 1, all others: 0)
	BorderRow bool // horizontal rule between data rows
	Zebra     bool // alternating row background
	// Center lists column indices to center; nil = none. A slice rather than
	// an int index so its zero value is inert — an int field would default
	// to 0 and silently center the first column of every table that forgot
	// to set it.
	Center []int
}

// Render renders v at budget columns: as a table when the columns fit
// (budget == 0 means unbounded), or as a record layout when they do not fit
// even at their floors.
func (v tableView) Render(budget int) string {
	rows, ok := v.fit(budget)
	if !ok {
		return v.renderRecords(budget)
	}
	return v.renderTable(rows)
}

// fit resolves v's rows at budget without rendering them: ok reports whether
// v renders as a table, and rows carries the fitted, wrapped cells to hand to
// renderTable. DiagnosticsByDomain calls it directly so it can decide one
// shared mode across every domain table it emits — rather than letting each
// domain decide independently — while still reusing each domain's already
// fitted rows.
func (v tableView) fit(budget int) ([][]string, bool) {
	return fitRows(v.Headers, v.Rows, budget, v.Padding, v.Cols)
}

// renderTable renders rows — already fitted and wrapped by fitRows — as a
// Lipgloss table, composing v.Style with the table-mode decoration fields.
func (v tableView) renderTable(rows [][]string) string {
	t := baseTable(v.Headers...).
		BorderRow(v.BorderRow).
		StyleFunc(v.styleFunc())
	return renderRows(t, rows)
}

// styleFunc composes the effective per-cell StyleFunc from v.Style plus the
// table-mode decoration fields (Padding, Zebra, Center). The header row
// always uses headerRowStyle(), regardless of v.Style.
func (v tableView) styleFunc() func(row, col int) lipgloss.Style {
	center := make(map[int]bool, len(v.Center))
	for _, c := range v.Center {
		center[c] = true
	}
	return func(row, col int) lipgloss.Style {
		var style lipgloss.Style
		switch {
		case row == table.HeaderRow:
			style = headerRowStyle()
		case v.Style != nil:
			style = v.Style(row, col)
		default:
			style = lipgloss.NewStyle()
		}
		style = style.Padding(0, v.Padding)
		if center[col] {
			style = style.AlignHorizontal(lipgloss.Center)
		}
		if v.Zebra && row >= 0 && row%2 == 1 {
			style = style.Background(zebraBackground)
		}
		return style
	}
}

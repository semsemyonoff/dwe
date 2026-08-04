package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// baseTable constructs a Lipgloss table builder with the shared rounded
// border and border style applied and the given headers set. Callers chain a
// StyleFunc (and, for the diagnostics table, BorderRow) before rendering.
func baseTable(headers ...string) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styles.BorderStyle()).
		Headers(headers...)
}

// headerRowStyle is the shared style for the header row of every table.
func headerRowStyle() lipgloss.Style {
	return styles.AccentStyle().Bold(true)
}

// renderRows appends each data row to the table builder and returns the
// rendered string — the common epilogue across all table renderers.
func renderRows(t *table.Table, rows [][]string) string {
	for _, r := range rows {
		t.Row(r...)
	}
	return t.String()
}

// SortedKVPairs renders a map as `key=value` pairs sorted by key and
// comma-joined. The value is stringified via format. Empty map → "".
func SortedKVPairs[V any](m map[string]V, format func(V) string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+format(m[k]))
	}
	return strings.Join(parts, ", ")
}

// Table builds and returns a Lipgloss table string using the shared
// border and accent style vars. Headers and rows are rendered with the
// package-level table styles (configurable via ApplyStyles).
//
// headers contains the column names; rows contains the data rows, each a slice
// of strings with the same length as headers. Column 0 is the record-mode
// title; every column may shrink and wrap under width pressure, since
// caller-supplied content is arbitrary prose.
func Table(headers []string, rows [][]string) string {
	cols := make([]columnSpec, len(headers))
	for i := range cols {
		role := roleField
		if i == 0 {
			role = roleTitle
		}
		cols[i] = columnSpec{Flex: true, Wrap: wrapText, Role: role}
	}
	v := tableView{Headers: headers, Rows: rows, Cols: cols}
	return v.Render(stdoutBudget())
}

// ServiceTableRow holds data for one row in the services Lipgloss table.
type ServiceTableRow struct {
	Name string
	// Icon is the resolved service icon (already type-defaulted). Prepended to
	// the NAME cell when non-empty.
	Icon      string
	Dir       string
	Container string
	// Hosts / Ports are the resolved per-developer values (declared in
	// services.yml, optionally overridden via defaults.yml / local.yml).
	// They render as built-in HOSTS / PORTS columns; the formatter shows
	// a single value bare (e.g. "app.local" / "80") and multi-entry maps
	// as `name=value` pairs sorted by name.
	Hosts     map[string]string
	Ports     map[string]int
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

// formatHostsCell renders a service's hosts map as a single table cell.
// One entry → bare value ("app.local"). Multiple → "name=value" pairs
// sorted by name, comma-joined. Empty → em-dash.
func formatHostsCell(hosts map[string]string) string {
	if len(hosts) == 0 {
		return "—"
	}
	if len(hosts) == 1 {
		for _, v := range hosts {
			return v
		}
	}
	return SortedKVPairs(hosts, func(v string) string { return v })
}

// formatPortsCell renders a service's ports map as a single table cell.
// One entry → bare value ("80"). Multiple → "name=value" pairs sorted by
// name, comma-joined. Empty → em-dash.
func formatPortsCell(ports map[string]int) string {
	if len(ports) == 0 {
		return "—"
	}
	if len(ports) == 1 {
		for _, v := range ports {
			return fmt.Sprintf("%d", v)
		}
	}
	return SortedKVPairs(ports, func(v int) string { return fmt.Sprintf("%d", v) })
}

// ServicesTable renders a styled Lipgloss table of services.
// Built-in columns: NAME, [DIR if withDirCol,] CONTAINER, HOSTS, PORTS, STATE, RUNNING.
// extraCols, if non-nil, lists additional column names appended after the
// built-ins; each row's value is read from ServiceTableRow.Extras (missing
// key → "—"). Pass nil to render the table with only the built-in columns.
// withDirCol=true inserts a DIR column between NAME and CONTAINER (apps);
// pass false for tool/infra rows that have no source directory.
//
// HOSTS / PORTS columns are populated from ServiceTableRow.Hosts / .Ports —
// dwe treats per-developer port and host overrides as a core feature, so
// these are always-visible built-in columns rather than opt-in extras.
func ServicesTable(rows []ServiceTableRow, extraCols []string, withDirCol bool) string {
	return ServicesTableAt(rows, extraCols, withDirCol, stdoutBudget())
}

// ServicesTableAt is ServicesTable at an explicit width budget (0 =
// unbounded). Callers that already know their own render width — the status
// TUI panel — use this instead of the sink-probing ServicesTable.
func ServicesTableAt(rows []ServiceTableRow, extraCols []string, withDirCol bool, width int) string {
	return servicesTableView(rows, extraCols, withDirCol).Render(width)
}

// servicesTableView builds the tableView backing ServicesTable, split out so
// tests can exercise record mode at a narrow budget without duplicating the
// column-spec construction.
func servicesTableView(rows []ServiceTableRow, extraCols []string, withDirCol bool) tableView {
	stringRows := make([][]string, len(rows))
	cellStyles := make([]rowCellStyle, len(rows))

	for i, r := range rows {
		var stateStr, runStr string
		var cs rowCellStyle

		switch {
		case r.Mandatory:
			stateStr = "mandatory"
			cs.base = lipgloss.NewStyle()
			cs.state = styles.AccentStyle().Bold(true)
		case r.Enabled:
			stateStr = "enabled"
			cs.base = lipgloss.NewStyle()
			cs.state = lipgloss.NewStyle()
		default:
			stateStr = "disabled"
			cs.base = styles.MutedStyle()
			cs.state = styles.MutedStyle()
		}

		if r.Mandatory || r.Enabled {
			if r.Running {
				runStr = "running"
				cs.run = styles.SuccessStyle()
			} else {
				runStr = "stopped"
				cs.run = styles.DangerStyle()
			}
		} else {
			runStr = "—"
			cs.run = styles.MutedStyle()
		}

		hostsStr := formatHostsCell(r.Hosts)
		portsStr := formatPortsCell(r.Ports)

		nameCell := styles.IconPrefix(r.Icon) + r.Name

		var row []string
		if withDirCol {
			dir := r.Dir
			if dir == "" {
				dir = "—"
			}
			row = []string{nameCell, dir, r.Container, hostsStr, portsStr, stateStr, runStr}
		} else {
			row = []string{nameCell, r.Container, hostsStr, portsStr, stateStr, runStr}
		}
		for _, col := range extraCols {
			row = append(row, extraCell(r.Extras, col))
		}
		stringRows[i] = row
		cellStyles[i] = cs
	}

	var headers []string
	var cols []columnSpec
	var stateCol, runCol int
	if withDirCol {
		headers = append([]string{"NAME", "DIR", "CONTAINER", "HOSTS", "PORTS", "STATE", "RUNNING"}, extraCols...)
		cols = []columnSpec{
			{Role: roleTitle}, // NAME
			{Flex: true, Wrap: wrapPath, Role: roleField}, // DIR
			{Flex: true, Wrap: wrapText, Role: roleField}, // CONTAINER
			{Flex: true, Wrap: wrapText, Role: roleField}, // HOSTS
			{Flex: true, Wrap: wrapText, Role: roleField}, // PORTS
			{Role: roleField}, // STATE
			{Role: roleField}, // RUNNING
		}
		stateCol, runCol = 5, 6
	} else {
		headers = append([]string{"NAME", "CONTAINER", "HOSTS", "PORTS", "STATE", "RUNNING"}, extraCols...)
		cols = []columnSpec{
			{Role: roleTitle}, // NAME
			{Flex: true, Wrap: wrapText, Role: roleField}, // CONTAINER
			{Flex: true, Wrap: wrapText, Role: roleField}, // HOSTS
			{Flex: true, Wrap: wrapText, Role: roleField}, // PORTS
			{Role: roleField}, // STATE
			{Role: roleField}, // RUNNING
		}
		stateCol, runCol = 4, 5
	}
	for range extraCols {
		cols = append(cols, columnSpec{Flex: true, Wrap: wrapText, Role: roleField})
	}

	v := tableView{
		Headers: headers,
		Rows:    stringRows,
		Cols:    cols,
		Style: func(row, col int) lipgloss.Style {
			if row < 0 || row >= len(cellStyles) {
				return lipgloss.NewStyle()
			}
			cs := cellStyles[row]
			switch col {
			case stateCol:
				return cs.state
			case runCol:
				return cs.run
			default:
				return cs.base
			}
		},
	}
	return v
}

// extraCell returns the value for col in extras, or "—" if missing.
func extraCell(extras map[string]string, col string) string {
	if v, ok := extras[col]; ok {
		return v
	}
	return "—"
}

// DaemonTableRow holds the data for one row in the daemons Lipgloss table.
// Values are passed in already-sanitised by the collector.
type DaemonTableRow struct {
	ID        string
	Params    string
	Container string
	Uptime    string
}

// DaemonTable renders a styled Lipgloss table of running daemons.
// Columns: ID, PARAMS, CONTAINER, UPTIME. Empty input returns an empty string.
func DaemonTable(rows []DaemonTableRow) string {
	return DaemonTableAt(rows, stdoutBudget())
}

// DaemonTableAt is DaemonTable at an explicit width budget (0 = unbounded).
// Callers that already know their own render width — the status TUI panel —
// use this instead of the sink-probing DaemonTable.
func DaemonTableAt(rows []DaemonTableRow, width int) string {
	if len(rows) == 0 {
		return ""
	}
	headers := []string{"ID", "PARAMS", "CONTAINER", "UPTIME"}
	stringRows := make([][]string, len(rows))
	for i, r := range rows {
		params := r.Params
		if params == "" {
			params = "—"
		}
		stringRows[i] = []string{r.ID, params, r.Container, r.Uptime}
	}
	cols := []columnSpec{
		{Role: roleTitle},
		{Flex: true, Wrap: wrapText}, // PARAMS: widest, most compressible column
		{},
		{},
	}
	v := tableView{Headers: headers, Rows: stringRows, Cols: cols}
	return v.Render(width)
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
		return styles.SuccessStyle()
	case "changed":
		return styles.WarningStyle()
	case "missing":
		return styles.WarningStyle()
	default:
		return lipgloss.NewStyle()
	}
}

// statusStyleForStatus returns the style for a deploy status cell.
func statusStyleForStatus(status string) lipgloss.Style {
	switch status {
	case "deployed":
		return styles.SuccessStyle()
	case "partial", "in_progress":
		return styles.WarningStyle()
	case "failed":
		return styles.DangerStyle()
	case "not_deployed", "skipped":
		return styles.MutedStyle()
	default:
		return lipgloss.NewStyle()
	}
}

// DeployStatus renders a styled Lipgloss table of deploy status per service.
// Columns: SERVICE, STATUS, CONFIG, PREV HASH, CURR HASH, LAST FAILED.
func DeployStatus(rows []DeployStatusRow) string {
	return DeployStatusAt(rows, stdoutBudget())
}

// DeployStatusAt is DeployStatus at an explicit width budget (0 =
// unbounded). Callers that already know their own render width — the status
// TUI panel — use this instead of the sink-probing DeployStatus.
func DeployStatusAt(rows []DeployStatusRow, width int) string {
	headers := []string{"SERVICE", "STATUS", "CONFIG", "PREV HASH", "CURR HASH", "LAST FAILED"}
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

	cols := []columnSpec{
		{Role: roleTitle},
		{},                           // STATUS: fixed
		{},                           // CONFIG: fixed
		{},                           // PREV HASH: fixed
		{},                           // CURR HASH: fixed
		{Flex: true, Wrap: wrapText}, // LAST FAILED
	}
	v := tableView{
		Headers: headers,
		Rows:    stringRows,
		Cols:    cols,
		Style: func(row, col int) lipgloss.Style {
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
		},
	}
	return v.Render(width)
}

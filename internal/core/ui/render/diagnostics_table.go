package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// diagnosticTextWrapWidth is the Max natural-width cap for the MESSAGE and
// HINT columns: keeps the six-column diagnostics table readable on a
// wide-but-normal terminal instead of expanding indefinitely. A column whose
// content stays under the cap takes its own (smaller) natural width; only
// content wider than the cap is clamped and wrapped down to it.
const diagnosticTextWrapWidth = 44

// diagnosticFileWrapWidth is the Max natural-width cap for the FILE column.
// Paths have no spaces, so a long relative path (e.g.
// services/api/src/vendor/.../Dockerfile) would otherwise widen the whole
// table; wrapPath breaks them on "/" boundaries instead.
const diagnosticFileWrapWidth = 40

// zebraBackground tints every other data row to improve scanability. Subtle
// adaptive shade so it reads as "different" without competing with severity
// foreground colors.
var zebraBackground = lipgloss.AdaptiveColor{Light: "#F1F5F9", Dark: "#1F2937"}

// DiagnosticRow holds data for one row in the diagnostics Lipgloss table.
type DiagnosticRow struct {
	Severity validate.Severity
	Domain   string
	Target   string
	File     string
	Message  string
	Hint     string
}

// DiagnosticsTable renders a styled Lipgloss table of validation diagnostics
// with a DOMAIN column. Used by preflight and the deploy menu, where rows span
// only the env + checks domains and the column aids scanning.
// Columns: STATUS / DOMAIN / TARGET / FILE / MESSAGE / HINT.
// Status glyphs color-coded by severity: ✓ OK (green), ⓘ info (dim), ⚠ warning (yellow), ✗ error (red).
func DiagnosticsTable(rows []DiagnosticRow) string {
	return diagnosticsTable(rows, true)
}

// DiagnosticsByDomain renders one titled table per domain (no DOMAIN column).
// Rows are expected pre-sorted (severity desc within a domain); domains are
// ordered canonically (see domainDisplayOrder) with unknown domains appended
// alphabetically. Returns "" when there are no rows so the caller can skip an
// empty bordered box. Used by `dwe validate`.
func DiagnosticsByDomain(rows []DiagnosticRow) string {
	if len(rows) == 0 {
		return ""
	}

	groups := make(map[string][]DiagnosticRow)
	var order []string
	for _, r := range rows {
		if _, seen := groups[r.Domain]; !seen {
			order = append(order, r.Domain)
		}
		groups[r.Domain] = append(groups[r.Domain], r)
	}
	sortDomainsForDisplay(order)

	var b strings.Builder
	for i, d := range order {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(domainTitle(d, groups[d]))
		b.WriteByte('\n')
		b.WriteString(diagnosticsTable(groups[d], false))
	}
	return b.String()
}

// diagnosticsTable renders the diagnostics table, optionally including the
// DOMAIN column. STATUS is always column 0 (its centering/glyph styling keys
// off that), so dropping DOMAIN shifts only the prose columns.
//
// Budget is resolved from stderr, not stdout: DiagnosticsTable is the sole
// stderr consumer among the six renderers — all three of its call sites
// (preflight, deploy menu ×2) write diagnostics to stderr.
func diagnosticsTable(rows []DiagnosticRow, showDomain bool) string {
	return diagnosticsTableView(rows, showDomain).Render(stderrBudget())
}

// diagnosticsTableView builds the tableView backing diagnosticsTable. Split
// out so a future caller (DiagnosticsByDomain) can inspect Fits/Render per
// domain without duplicating the column-spec construction.
func diagnosticsTableView(rows []DiagnosticRow, showDomain bool) tableView {
	stringRows := make([][]string, len(rows))
	severities := make([]validate.Severity, len(rows))

	for i, r := range rows {
		// File may be empty; use "—" as placeholder. Wrapping on "/" so a
		// long relative path does not widen the whole table happens via the
		// FILE column's Wrap spec below.
		fileStr := r.File
		if fileStr == "" {
			fileStr = "—"
		}

		cells := make([]string, 0, 6)
		cells = append(cells, severityGlyph(r.Severity))
		if showDomain {
			cells = append(cells, r.Domain)
		}
		cells = append(cells, r.Target, fileStr, r.Message, r.Hint)
		stringRows[i] = cells
		severities[i] = r.Severity
	}

	headers := make([]string, 0, 6)
	headers = append(headers, "STATUS")
	if showDomain {
		headers = append(headers, "DOMAIN")
	}
	headers = append(headers, "TARGET", "FILE", "MESSAGE", "HINT")

	cols := make([]columnSpec, 0, 6)
	cols = append(cols, columnSpec{Role: roleGlyph}) // STATUS
	if showDomain {
		cols = append(cols, columnSpec{Role: roleTitle}) // DOMAIN
	}
	cols = append(cols,
		columnSpec{Role: roleTitle}, // TARGET
		columnSpec{Flex: true, Max: diagnosticFileWrapWidth, Wrap: wrapPath, Role: roleTitle}, // FILE
		columnSpec{Flex: true, Max: diagnosticTextWrapWidth, Wrap: wrapText, Role: roleBody},  // MESSAGE
		columnSpec{Flex: true, Max: diagnosticTextWrapWidth, Wrap: wrapText, Role: roleField}, // HINT
	)

	return tableView{
		Headers:   headers,
		Rows:      stringRows,
		Cols:      cols,
		Padding:   1,
		BorderRow: true,
		Zebra:     true,
		// One column of horizontal padding on every cell (Padding: 1 above)
		// keeps content off the vertical border. This is load-bearing for
		// URL hints: when a link exactly fills its column, a terminal's link
		// detector would otherwise grab the adjoining "│" (encoding it as
		// %E2%94%82) and produce a dead link. The trailing pad space gives it
		// a clean boundary to stop at.
		Center: []int{0}, // STATUS
		Style: func(row, col int) lipgloss.Style {
			// Only the STATUS column carries semantic color; every other
			// column inherits the base (zero) style.
			if col != 0 || row < 0 || row >= len(severities) {
				return lipgloss.NewStyle()
			}
			return severityStyle(severities[row])
		},
	}
}

// domainDisplayOrder is the canonical ordering for per-domain tables, mirroring
// the order validators are assembled in. Domains absent from this list sort
// after these, alphabetically.
var domainDisplayOrder = []string{
	"config", "templates", "commands", "env", "i18n",
	"checks", "linters", "snapshot", "setup",
}

// domainLabels maps a diagnostic domain to a human-friendly table title. A
// domain without an entry falls back to its raw key.
var domainLabels = map[string]string{
	"config":    "Configuration",
	"templates": "Template packs",
	"commands":  "Commands",
	"env":       "Environment",
	"i18n":      "Translations",
	"checks":    "Project checks",
	"linters":   "Linters",
	"snapshot":  "Snapshots",
	"setup":     "Setup wizard",
}

// sortDomainsForDisplay orders domains by domainDisplayOrder, with unknown
// domains appended alphabetically after the known set.
func sortDomainsForDisplay(domains []string) {
	rank := make(map[string]int, len(domainDisplayOrder))
	for i, d := range domainDisplayOrder {
		rank[d] = i
	}
	sort.SliceStable(domains, func(i, j int) bool {
		ri, oki := rank[domains[i]]
		rj, okj := rank[domains[j]]
		switch {
		case oki && okj:
			return ri < rj
		case oki != okj:
			return oki // known domains sort before unknown
		default:
			return domains[i] < domains[j]
		}
	})
}

// domainTitle returns the styled per-domain section title, colored by the worst
// severity present in that domain's rows (red on any error, yellow on any
// warning, else the neutral section-title style).
func domainTitle(domain string, rows []DiagnosticRow) string {
	label, ok := domainLabels[domain]
	if !ok {
		label = domain
	}
	worst := validate.SeverityOK
	for _, r := range rows {
		if r.Severity > worst {
			worst = r.Severity
		}
	}
	switch worst {
	case validate.SeverityError:
		return styles.StyleFailed(label)
	case validate.SeverityWarning:
		return styles.StyleWarning(label)
	default:
		return styles.StyleSectionTitle(label)
	}
}

// severityGlyph returns the glyph for a severity level.
func severityGlyph(s validate.Severity) string {
	switch s {
	case validate.SeverityOK:
		return "✓"
	case validate.SeverityInfo:
		return "ⓘ"
	case validate.SeverityWarning:
		return "⚠"
	case validate.SeverityError:
		return "✗"
	default:
		return "?"
	}
}

// severityStyle returns the lipgloss.Style for a severity level.
func severityStyle(s validate.Severity) lipgloss.Style {
	switch s {
	case validate.SeverityOK:
		return styles.SuccessStyle() // green
	case validate.SeverityInfo:
		return styles.MutedStyle() // dim gray
	case validate.SeverityWarning:
		return styles.WarningStyle() // yellow
	case validate.SeverityError:
		return styles.DangerStyle() // red
	default:
		return lipgloss.NewStyle()
	}
}

// FormatDiagnostics converts a slice of Diagnostic to DiagnosticRow for rendering.
func FormatDiagnostics(diags []validate.Diagnostic, quiet bool) []DiagnosticRow {
	var rows []DiagnosticRow
	for _, d := range diags {
		// Skip OK and Info rows if --quiet is set.
		if quiet && (d.Severity == validate.SeverityOK || d.Severity == validate.SeverityInfo) {
			continue
		}
		rows = append(rows, DiagnosticRow{
			Severity: d.Severity,
			Domain:   d.Domain,
			Target:   d.Target,
			File:     d.File,
			Message:  d.Message,
			Hint:     d.Hint,
		})
	}
	return rows
}

func wrapDiagnosticText(s string) string {
	return wrapText(s, diagnosticTextWrapWidth)
}

// FormatSummary returns a summary line based on the aggregate counts.
func FormatSummary(summary validate.Summary) string {
	parts := []string{}
	if summary.Errors > 0 {
		parts = append(parts, styles.StyleFailed(fmt.Sprintf("%d %s", summary.Errors, pluralize("error", summary.Errors))))
	}
	if summary.Warnings > 0 {
		parts = append(parts, styles.StyleWarning(fmt.Sprintf("%d %s", summary.Warnings, pluralize("warning", summary.Warnings))))
	}
	if summary.Infos > 0 {
		parts = append(parts, styles.StyleInfo(fmt.Sprintf("%d %s", summary.Infos, pluralize("info", summary.Infos))))
	}
	if summary.OKs > 0 {
		parts = append(parts, styles.RenderEnabled(fmt.Sprintf("%d %s", summary.OKs, pluralize("check", summary.OKs))))
	}

	if len(parts) == 0 {
		return "validation skipped (no files found)"
	}

	return "validation result: " + strings.Join(parts, ", ")
}

func pluralize(word string, count int) string {
	if count != 1 {
		return word + "s"
	}
	return word
}

package render

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// Keep MESSAGE/HINT narrow enough that the six-column diagnostics table stays
// readable on a wide-but-normal terminal instead of expanding indefinitely.
const diagnosticTextWrapWidth = 44

// diagnosticFileWrapWidth bounds the FILE column. Paths have no spaces, so a
// long relative path (e.g. services/api/src/vendor/.../Dockerfile) would widen
// the whole table; wrapPath breaks them on "/" boundaries instead.
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
func diagnosticsTable(rows []DiagnosticRow, showDomain bool) string {
	stringRows := make([][]string, len(rows))
	cellStyles := make([]map[int]lipgloss.Style, len(rows))

	for i, r := range rows {
		statusGlyph := severityGlyph(r.Severity)
		statusStyle := severityStyle(r.Severity)

		// File may be empty; use "—" as placeholder. Wrap on "/" so a long
		// relative path does not widen the whole table.
		fileStr := r.File
		if fileStr == "" {
			fileStr = "—"
		} else {
			fileStr = wrapPath(fileStr, diagnosticFileWrapWidth)
		}

		// Message and Hint are the only unbounded prose columns. Wrap them
		// before table rendering so one long diagnostic does not widen the
		// whole table past a normal terminal.
		cells := make([]string, 0, 6)
		cells = append(cells, statusGlyph)
		if showDomain {
			cells = append(cells, r.Domain)
		}
		cells = append(cells, r.Target, fileStr, wrapDiagnosticText(r.Message), wrapDiagnosticText(r.Hint))
		stringRows[i] = cells

		// Per-column styles: only the status column is styled; others inherit base.
		cellStyles[i] = map[int]lipgloss.Style{
			0: statusStyle, // STATUS column gets the glyph style
		}
	}

	headers := make([]string, 0, 6)
	headers = append(headers, "STATUS")
	if showDomain {
		headers = append(headers, "DOMAIN")
	}
	headers = append(headers, "TARGET", "FILE", "MESSAGE", "HINT")

	t := baseTable(headers...).
		BorderRow(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			// One column of horizontal padding on every cell so content never
			// abuts the vertical border. This is load-bearing for URL hints:
			// when a link exactly fills its column, a terminal's link detector
			// would otherwise grab the adjoining "│" (encoding it as %E2%94%82)
			// and produce a dead link. The trailing pad space gives it a clean
			// boundary to stop at.
			if row == table.HeaderRow {
				h := headerRowStyle().Padding(0, 1)
				if col == 0 {
					h = h.AlignHorizontal(lipgloss.Center)
				}
				return h
			}
			style := lipgloss.NewStyle().Padding(0, 1)
			if row >= 0 && row < len(cellStyles) {
				if s, ok := cellStyles[row][col]; ok {
					style = s.Padding(0, 1)
				}
			}
			if col == 0 {
				style = style.AlignHorizontal(lipgloss.Center)
			}
			if row >= 0 && row%2 == 1 {
				style = style.Background(zebraBackground)
			}
			return style
		})

	return renderRows(t, stringRows)
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

// wrapPath breaks a path on "/" boundaries so the FILE column stays within
// width. The separator stays with the preceding segment; a single segment
// wider than width is hard-split via splitDisplayWidth.
func wrapPath(s string, width int) string {
	if s == "" || width <= 0 || lipgloss.Width(s) <= width {
		return s
	}

	var out []string
	current := ""
	flush := func() {
		if current != "" {
			out = append(out, current)
			current = ""
		}
	}
	for _, part := range strings.SplitAfter(s, "/") {
		if part == "" {
			continue
		}
		for lipgloss.Width(part) > width {
			head, tail := splitDisplayWidth(part, width)
			flush()
			out = append(out, head)
			part = tail
		}
		if current == "" {
			current = part
			continue
		}
		if lipgloss.Width(current+part) <= width {
			current += part
			continue
		}
		flush()
		current = part
	}
	flush()
	return strings.Join(out, "\n")
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

func wrapText(s string, width int) string {
	if s == "" || width <= 0 {
		return s
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = wrapLine(line, width)
	}
	return strings.Join(lines, "\n")
}

// isURLToken reports whether a whitespace-delimited token is a URL that must
// not be split across lines (so it stays copyable). A scheme separator is a
// good-enough signal for the http(s) links we emit as diagnostic hints.
func isURLToken(word string) bool {
	return strings.Contains(word, "://")
}

func wrapLine(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line
	}

	words := strings.Fields(line)
	if len(words) == 0 {
		return line
	}

	var out []string
	current := ""
	for _, word := range words {
		// URLs are kept whole even when they exceed the column width — a
		// hard-split mid-URL produces an uncopyable link. They still break
		// onto their own line (on the surrounding spaces), just never mid-token.
		for !isURLToken(word) && lipgloss.Width(word) > width {
			head, tail := splitDisplayWidth(word, width)
			if current != "" {
				out = append(out, current)
				current = ""
			}
			out = append(out, head)
			word = tail
		}

		if current == "" {
			current = word
			continue
		}
		candidate := current + " " + word
		if lipgloss.Width(candidate) <= width {
			current = candidate
			continue
		}
		out = append(out, current)
		current = word
	}
	if current != "" {
		out = append(out, current)
	}
	return strings.Join(out, "\n")
}

func splitDisplayWidth(s string, width int) (string, string) {
	if width <= 0 {
		return "", s
	}

	byteIdx := 0
	for byteIdx < len(s) {
		r, size := utf8.DecodeRuneInString(s[byteIdx:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		candidate := s[:byteIdx+size]
		if lipgloss.Width(candidate) > width {
			break
		}
		byteIdx += size
	}
	if byteIdx == 0 {
		_, size := utf8.DecodeRuneInString(s)
		byteIdx = size
	}
	return s[:byteIdx], s[byteIdx:]
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

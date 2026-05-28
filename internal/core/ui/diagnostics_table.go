package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"devbox-cli/internal/core/validate"
)

// Keep MESSAGE/HINT narrow enough that the six-column diagnostics table stays
// readable on a wide-but-normal terminal instead of expanding indefinitely.
const diagnosticTextWrapWidth = 44

// DiagnosticRow holds data for one row in the diagnostics Lipgloss table.
type DiagnosticRow struct {
	Severity validate.Severity
	Domain   string
	Target   string
	File     string
	Message  string
	Hint     string
}

// RenderDiagnosticsTable renders a styled Lipgloss table of validation diagnostics.
// Columns: STATUS / DOMAIN / TARGET / FILE / MESSAGE / HINT.
// Status glyphs color-coded by severity: ✓ OK (green), ⓘ info (dim), ⚠ warning (yellow), ✗ error (red).
func RenderDiagnosticsTable(rows []DiagnosticRow) string {
	stringRows := make([][]string, len(rows))
	cellStyles := make([]map[int]lipgloss.Style, len(rows))

	for i, r := range rows {
		statusGlyph := severityGlyph(r.Severity)
		statusStyle := severityStyle(r.Severity)

		// File may be empty; use "—" as placeholder.
		fileStr := r.File
		if fileStr == "" {
			fileStr = "—"
		}

		// Message and Hint are the only unbounded prose columns. Wrap them
		// before table rendering so one long diagnostic does not widen the
		// whole table past a normal terminal.
		stringRows[i] = []string{
			statusGlyph,
			r.Domain,
			r.Target,
			fileStr,
			wrapDiagnosticText(r.Message),
			wrapDiagnosticText(r.Hint),
		}

		// Per-column styles: only the status column is styled; others inherit base.
		cellStyles[i] = map[int]lipgloss.Style{
			0: statusStyle, // STATUS column gets the glyph style
		}
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleBorder).
		Headers("STATUS", "DOMAIN", "TARGET", "FILE", "MESSAGE", "HINT").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleAccent.Bold(true)
			}
			if row < 0 || row >= len(cellStyles) {
				return lipgloss.NewStyle()
			}
			if style, ok := cellStyles[row][col]; ok {
				return style
			}
			return lipgloss.NewStyle()
		})

	for _, r := range stringRows {
		t.Row(r...)
	}

	return t.String()
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
		return styleSuccess // green
	case validate.SeverityInfo:
		return styleMuted // dim gray
	case validate.SeverityWarning:
		return styleWarning // yellow
	case validate.SeverityError:
		return styleDanger // red
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
		for lipgloss.Width(word) > width {
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
		parts = append(parts, StyleFailed(fmt.Sprintf("%d %s", summary.Errors, pluralize("error", summary.Errors))))
	}
	if summary.Warnings > 0 {
		parts = append(parts, StyleWarning(fmt.Sprintf("%d %s", summary.Warnings, pluralize("warning", summary.Warnings))))
	}
	if summary.Infos > 0 {
		parts = append(parts, StyleInfo(fmt.Sprintf("%d %s", summary.Infos, pluralize("info", summary.Infos))))
	}
	if summary.OKs > 0 {
		parts = append(parts, RenderEnabled(fmt.Sprintf("%d %s", summary.OKs, pluralize("check", summary.OKs))))
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

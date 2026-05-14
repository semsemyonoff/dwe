package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"devbox-cli/internal/validate"
)

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

		// Message and Hint may be empty strings; keep them as-is (table will render empty cells).
		stringRows[i] = []string{statusGlyph, r.Domain, r.Target, fileStr, r.Message, r.Hint}

		// Per-column styles: only the status column is styled; others inherit base.
		cellStyles[i] = map[int]lipgloss.Style{
			0: statusStyle, // STATUS column gets the glyph style
		}
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleTableBorder).
		Headers("STATUS", "DOMAIN", "TARGET", "FILE", "MESSAGE", "HINT").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleTableHeader
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
		return styleEnabled // green
	case validate.SeverityInfo:
		return styleMuted // dim gray
	case validate.SeverityWarning:
		return styleWarn // yellow
	case validate.SeverityError:
		return styleRunStopped // red
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

// FormatSummary returns a summary line based on the aggregate counts.
func FormatSummary(summary validate.Summary) string {
	parts := []string{}
	if summary.Errors > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", summary.Errors, pluralize("error", summary.Errors)))
	}
	if summary.Warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", summary.Warnings, pluralize("warning", summary.Warnings)))
	}
	if summary.Infos > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", summary.Infos, pluralize("info", summary.Infos)))
	}
	if summary.OKs > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", summary.OKs, pluralize("check", summary.OKs)))
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

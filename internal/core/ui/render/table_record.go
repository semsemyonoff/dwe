package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// recordIndent is the left margin for body and field lines in record mode,
// and the continuation indent for a wrapped header line.
const recordIndent = 2

// recordFieldGap separates a field's label from its value: "label  value".
const recordFieldGap = 2

// renderRecords renders v's rows as labeled record blocks instead of a
// table — the fallback tableView.Render uses when fitRows reports the
// columns do not fit even at their floors. Unlike table mode, record mode
// has no lower width bound: at extreme narrowness it simply wraps more.
// budget <= 0 means unbounded (no wrapping), matching fitRows' convention.
func (v tableView) renderRecords(budget int) string {
	labelWidth := recordLabelWidth(v.Headers, v.Cols)

	var blocks []string
	for row := range v.Rows {
		if block := v.renderRecord(row, budget, labelWidth); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// recordLabelWidth is the width every roleField label is padded to, so
// "label  value" lines line up within and across every record. Computed once
// from the headers rather than per row: the column set — and therefore every
// label — is identical across rows.
func recordLabelWidth(headers []string, cols []columnSpec) int {
	width := 0
	for i, h := range headers {
		if i >= len(cols) || cols[i].Role != roleField {
			continue
		}
		if w := lipgloss.Width(strings.ToLower(h)); w > width {
			width = w
		}
	}
	return width
}

// renderRecord renders one row as a record block: a header line built from
// roleGlyph/roleTitle cells, followed by one line per roleBody cell and one
// aligned "label  value" line per roleField cell. Empty (no header, no body,
// no field columns) returns "".
func (v tableView) renderRecord(row, budget, labelWidth int) string {
	var lines []string
	if header := v.recordHeaderLine(row, budget); header != "" {
		lines = append(lines, header)
	}

	for col, spec := range v.Cols {
		cell := cellAt(v.Rows[row], col)
		// Skip cells with no content at all: they would otherwise emit a line
		// that is nothing but indent (roleBody) or a label followed by
		// nothing (roleField) — visual noise, and trailing whitespace in
		// output users copy and diff. Unlike recordTitleText this does NOT
		// skip the "—" placeholder: "ports  —" is informative ("this row has
		// none") and carries no trailing whitespace, whereas a title line
		// reading "web · —" is just noise.
		if cell == "" {
			continue
		}
		switch spec.Role {
		case roleBody:
			lines = append(lines, v.recordBodyLine(row, col, cell, spec, budget))
		case roleField:
			lines = append(lines, v.recordFieldLine(row, col, cell, spec, budget, labelWidth))
		}
	}
	return strings.Join(lines, "\n")
}

// recordHeaderLine composes the record's header line: roleGlyph cells as a
// bare, unwrapped, individually-styled prefix, followed by roleTitle cells
// joined with " · " (skipping empty and "—" cells) and wrapped together as
// plain text at budget, with continuation lines indented by recordIndent.
//
// Title cells are intentionally rendered unstyled here: the wrap helpers
// measure with lipgloss.Width but slice on rune boundaries, so a break can
// land between a style-open and its reset (pinned by
// TestSplitDisplayWidth_ANSIInputIsUnsupported) — style is therefore always
// applied after wrapping, never before. Multiple title cells sharing one
// wrapped line also cannot be attributed back to distinct per-cell styles
// once merged. The glyph is exempt because it is never wrapped: styling it
// before composition is safe.
func (v tableView) recordHeaderLine(row, budget int) string {
	glyph, hasGlyph := v.recordGlyphPrefix(row)
	title := v.recordTitleText(row)

	if !hasGlyph && title == "" {
		return ""
	}
	if title == "" {
		return glyph
	}

	width := 0
	if budget > 0 {
		width = budget
		if hasGlyph {
			width -= lipgloss.Width(glyph) + 1
		}
	}
	lines := strings.Split(wrapText(title, width), "\n")

	var b strings.Builder
	if hasGlyph {
		b.WriteString(glyph)
		b.WriteString(" ")
	}
	b.WriteString(lines[0])
	cont := strings.Repeat(" ", recordIndent)
	for _, l := range lines[1:] {
		b.WriteString("\n")
		b.WriteString(cont)
		b.WriteString(l)
	}
	return b.String()
}

// recordGlyphPrefix renders and joins every roleGlyph cell in row, skipping
// empty cells. hasGlyph is false when no glyph column produced content.
func (v tableView) recordGlyphPrefix(row int) (glyph string, hasGlyph bool) {
	var parts []string
	for col, spec := range v.Cols {
		if spec.Role != roleGlyph {
			continue
		}
		cell := cellAt(v.Rows[row], col)
		if cell == "" {
			continue
		}
		parts = append(parts, v.cellStyle(row, col).Render(cell))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " "), true
}

// recordTitleText joins every roleTitle cell in row with " · ", skipping
// empty and "—" placeholder cells.
func (v tableView) recordTitleText(row int) string {
	var parts []string
	for col, spec := range v.Cols {
		if spec.Role != roleTitle {
			continue
		}
		cell := cellAt(v.Rows[row], col)
		if cell == "" || cell == "—" {
			continue
		}
		parts = append(parts, cell)
	}
	return strings.Join(parts, " · ")
}

// recordBodyLine renders a roleBody cell as its own indented, unlabeled
// line: the raw cell is wrapped (plain text) first, then styled, then
// indented — preserving the wrap-before-style invariant even though the
// result spans multiple lines (lipgloss re-opens and resets styling on every
// line it renders, so splicing indentation after Render is safe).
func (v tableView) recordBodyLine(row, col int, cell string, spec columnSpec, budget int) string {
	width := 0
	if budget > 0 {
		width = budget - recordIndent
	}
	wrapped := cell
	if spec.Wrap != nil {
		wrapped = spec.Wrap(cell, width)
	}
	styled := v.cellStyle(row, col).Render(wrapped)
	indent := strings.Repeat(" ", recordIndent)
	return indent + strings.ReplaceAll(styled, "\n", "\n"+indent)
}

// recordFieldLine renders a roleField cell as an aligned "label  value"
// line: the header lowercased and padded to labelWidth, styled muted, then
// the wrapped-then-styled value with continuation lines aligned under the
// value's first line.
func (v tableView) recordFieldLine(row, col int, cell string, spec columnSpec, budget, labelWidth int) string {
	label := ""
	if col < len(v.Headers) {
		label = strings.ToLower(v.Headers[col])
	}
	pad := max(labelWidth-lipgloss.Width(label), 0)
	prefix := strings.Repeat(" ", recordIndent) +
		styles.MutedStyle().Render(label+strings.Repeat(" ", pad)) +
		strings.Repeat(" ", recordFieldGap)
	contPrefix := strings.Repeat(" ", recordIndent+labelWidth+recordFieldGap)

	width := 0
	if budget > 0 {
		width = budget - recordIndent - labelWidth - recordFieldGap
	}
	wrapped := cell
	if spec.Wrap != nil {
		wrapped = spec.Wrap(cell, width)
	}
	styled := v.cellStyle(row, col).Render(wrapped)
	lines := strings.Split(styled, "\n")

	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteString("\n")
		b.WriteString(contPrefix)
		b.WriteString(l)
	}
	return b.String()
}

// cellStyle returns v.Style(row, col), or the zero style when v.Style is
// nil.
func (v tableView) cellStyle(row, col int) lipgloss.Style {
	if v.Style == nil {
		return lipgloss.NewStyle()
	}
	return v.Style(row, col)
}

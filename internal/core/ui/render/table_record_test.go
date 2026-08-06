package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// diagnosticStyleRecordView builds a tableView shaped like the diagnostics
// table (STATUS glyph / TARGET+FILE title / MESSAGE body / HINT field),
// matching the worked example in the plan's Overview, with severity color on
// STATUS only — mirroring diagnosticsTable's own StyleFunc.
func diagnosticStyleRecordView(rows [][]string) tableView {
	return tableView{
		Headers: []string{"STATUS", "TARGET", "FILE", "MESSAGE", "HINT"},
		Rows:    rows,
		Cols: []columnSpec{
			{Role: roleGlyph},
			{Role: roleTitle},
			{Role: roleTitle, Wrap: wrapPath},
			{Flex: true, Wrap: wrapText, Role: roleBody},
			{Flex: true, Wrap: wrapText, Role: roleField},
		},
		Style: func(row, col int) lipgloss.Style {
			if col == 0 {
				return styles.DangerStyle()
			}
			return lipgloss.NewStyle()
		},
	}
}

func TestTableView_RenderRecords_Shape(t *testing.T) {
	pinGoldenPalette(t)

	row := []string{
		"✗", "hadolint", "images/admin/Dockerfile",
		"Non-numeric user-id may not be resolvable by host system (DL3066)",
		"https://github.com/hadolint/hadolint/wiki/DL3066",
	}
	v := diagnosticStyleRecordView([][]string{row})

	got := stripANSI(v.renderRecords(80))
	want := "✗ hadolint · images/admin/Dockerfile\n" +
		"  Non-numeric user-id may not be resolvable by host system (DL3066)\n" +
		"  hint  https://github.com/hadolint/hadolint/wiki/DL3066"
	if got != want {
		t.Errorf("renderRecords() =\n%q\nwant\n%q", got, want)
	}
}

func TestTableView_RenderRecords_BlankLineSeparatesRecords(t *testing.T) {
	pinGoldenPalette(t)

	rows := [][]string{
		{"✗", "hadolint", "a/Dockerfile", "first message", "https://example.com/a"},
		{"⚠", "shellcheck", "b.sh", "second message", "https://example.com/b"},
	}
	v := diagnosticStyleRecordView(rows)

	got := stripANSI(v.renderRecords(80))
	blocks := strings.Split(got, "\n\n")
	if len(blocks) != 2 {
		t.Fatalf("renderRecords() produced %d blocks separated by a blank line, want 2:\n%s", len(blocks), got)
	}
	if !strings.Contains(blocks[0], "hadolint") || !strings.Contains(blocks[1], "shellcheck") {
		t.Errorf("blocks out of order or content missing: %q", got)
	}
}

func TestTableView_RenderRecords_SkipsEmptyCellsAndDashTitles(t *testing.T) {
	pinGoldenPalette(t)

	row := []string{"✗", "hadolint", "—", "msg", ""}
	v := diagnosticStyleRecordView([][]string{row})

	got := stripANSI(v.renderRecords(80))
	header := strings.SplitN(got, "\n", 2)[0]
	if header != "✗ hadolint" {
		t.Errorf("header = %q, want %q (FILE=\"—\" must be skipped)", header, "✗ hadolint")
	}
	// HINT is empty, so its field line is dropped entirely: emitting it would
	// produce "  hint" followed by nothing but padding — noise plus trailing
	// whitespace in output users copy and diff.
	if strings.Contains(got, "hint") {
		t.Errorf("renderRecords() = %q, want no field line for the empty HINT cell", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line has trailing whitespace: %q", line)
		}
	}
}

// TestTableView_RenderRecords_DashFieldStillRenders pins the other half of the
// empty-cell rule: a "—" placeholder is informative ("this row has none") and
// carries no trailing whitespace, so unlike a title cell it is NOT skipped.
func TestTableView_RenderRecords_DashFieldStillRenders(t *testing.T) {
	pinGoldenPalette(t)

	row := []string{"✗", "hadolint", "a.sh", "msg", "—"}
	v := diagnosticStyleRecordView([][]string{row})

	got := stripANSI(v.renderRecords(80))
	if !strings.Contains(got, "hint") || !strings.Contains(got, "—") {
		t.Errorf("renderRecords() = %q, want the HINT field line rendered with its \"—\" placeholder", got)
	}
}

func TestTableView_RenderRecords_AllTitleRow(t *testing.T) {
	pinGoldenPalette(t)

	v := tableView{
		Headers: []string{"A", "B"},
		Rows:    [][]string{{"one", "two"}},
		Cols: []columnSpec{
			{Role: roleTitle},
			{Role: roleTitle},
		},
	}

	got := stripANSI(v.renderRecords(80))
	if got != "one · two" {
		t.Errorf("renderRecords() = %q, want %q", got, "one · two")
	}
}

func TestTableView_RenderRecords_FieldAlignment(t *testing.T) {
	pinGoldenPalette(t)

	v := tableView{
		Headers: []string{"NAME", "SHORT", "MUCHLONGER"},
		Rows:    [][]string{{"svc", "a", "b"}},
		Cols: []columnSpec{
			{Role: roleTitle},
			{Role: roleField, Wrap: wrapText},
			{Role: roleField, Wrap: wrapText},
		},
	}

	got := stripANSI(v.renderRecords(80))
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("renderRecords() = %q, want 3 lines (header + 2 fields)", got)
	}
	// Both value columns must start at the same offset regardless of their
	// own label's length, since labels are padded to the widest field label
	// in the block ("muchlonger").
	shortIdx := strings.Index(lines[1], "a")
	longIdx := strings.Index(lines[2], "b")
	if shortIdx != longIdx {
		t.Errorf("field values not aligned: %q at %d, %q at %d", lines[1], shortIdx, lines[2], longIdx)
	}
	if !strings.HasPrefix(lines[1], "  short") {
		t.Errorf("field line = %q, want it to start with the lowercased, indented label", lines[1])
	}
}

func TestTableView_RenderRecords_RaggedRow(t *testing.T) {
	pinGoldenPalette(t)

	v := tableView{
		Headers: []string{"A", "B", "C"},
		Rows:    [][]string{{"x"}},
		Cols: []columnSpec{
			{Role: roleTitle},
			{Role: roleField, Wrap: wrapText},
			{Role: roleField, Wrap: wrapText},
		},
	}

	got := v.renderRecords(80)
	if got == "" {
		t.Fatal("renderRecords() = \"\", want output for a ragged row")
	}
	if strings.Contains(got, "\x00") {
		t.Errorf("renderRecords() produced unexpected content: %q", got)
	}
}

func TestTableView_RenderRecords_VerySmallBudgetStillRenders(t *testing.T) {
	pinGoldenPalette(t)

	row := []string{
		"✗", "hadolint", "a/very/long/path/that/would/not/fit.Dockerfile",
		"a fairly long diagnostic message describing the problem",
		"https://example.com/hint",
	}
	v := diagnosticStyleRecordView([][]string{row})

	got := v.renderRecords(1)
	if got == "" {
		t.Fatal("renderRecords(1) = \"\", want non-empty output even at an extreme budget")
	}
	if !strings.Contains(got, "hadolint") {
		t.Errorf("renderRecords(1) = %q, want content preserved, not dropped", got)
	}
}

func TestTableView_RenderRecords_URLLongerThanBudgetStaysUnbroken(t *testing.T) {
	pinGoldenPalette(t)

	url := "https://github.com/hadolint/hadolint/wiki/DL3008/some/extra/path/segments"
	row := []string{"✗", "hadolint", "Dockerfile", "msg", url}
	v := diagnosticStyleRecordView([][]string{row})

	got := stripANSI(v.renderRecords(40))
	if !strings.Contains(got, url) {
		t.Errorf("renderRecords(40) = %q, want the URL kept intact as a substring", got)
	}
}

// TestTableView_RenderRecords_SemanticColorSurvives pins the record-mode
// styling contract: Style(row, col) still colors a cell, and — unlike table
// mode — no padding or background is composed on top of it.
func TestTableView_RenderRecords_SemanticColorSurvives(t *testing.T) {
	pinGoldenPalette(t)

	v := tableView{
		Headers: []string{"NAME", "MESSAGE"},
		Rows:    [][]string{{"svc", "boom"}},
		Cols: []columnSpec{
			{Role: roleTitle},
			{Role: roleBody, Wrap: wrapText},
		},
		Style: func(row, col int) lipgloss.Style {
			if col == 1 {
				return styles.DangerStyle()
			}
			return lipgloss.NewStyle()
		},
	}

	got := v.renderRecords(80)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("renderRecords() = %q, want 2 lines", got)
	}
	bodyLine := lines[1]
	if !strings.Contains(bodyLine, "\x1b[") {
		t.Errorf("body line = %q, want it to carry DangerStyle's ANSI escape", bodyLine)
	}
	if strings.Contains(bodyLine, "\x1b[48") {
		t.Errorf("body line = %q, want no background code composed on top (record mode drops table decoration)", bodyLine)
	}
	if plain := stripANSI(bodyLine); plain != "  boom" {
		t.Errorf("stripped body line = %q, want %q (2-space indent, no extra padding)", plain, "  boom")
	}
}

func TestTableView_Render_UsesRecordsWhenColumnsDoNotFitFloors(t *testing.T) {
	pinGoldenPalette(t)

	headers := []string{"STATUS", "MESSAGE"}
	rows := [][]string{{"✗", "a somewhat long diagnostic message"}}
	cols := []columnSpec{
		{Role: roleGlyph},
		{Flex: true, Wrap: wrapText, Role: roleBody},
	}
	v := tableView{Headers: headers, Rows: rows, Cols: cols}

	floors := floorsFor(headers, rows, cols)
	chrome := len(headers) + 1
	budget := sumInts(floors) + chrome - 1

	got := v.Render(budget)
	if strings.Contains(got, "╭") || strings.Contains(got, "│") {
		t.Errorf("Render() at a too-narrow budget still looks like a table: %q", got)
	}
	plain := strings.Join(strings.Fields(stripANSI(got)), " ")
	if !strings.Contains(plain, "a somewhat long diagnostic message") {
		t.Errorf("Render() = %q, want the message content preserved (word-wrapped) in record mode", got)
	}
}

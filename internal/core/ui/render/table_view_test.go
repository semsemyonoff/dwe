package render

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

func TestTableView_StyleFunc_Padding(t *testing.T) {
	v := tableView{Padding: 2}
	sf := v.styleFunc()

	got := sf(0, 0)
	if left, right := got.GetPaddingLeft(), got.GetPaddingRight(); left != 2 || right != 2 {
		t.Errorf("data cell padding = (left=%d, right=%d), want (2, 2)", left, right)
	}

	header := sf(table.HeaderRow, 0)
	if left, right := header.GetPaddingLeft(), header.GetPaddingRight(); left != 2 || right != 2 {
		t.Errorf("header cell padding = (left=%d, right=%d), want (2, 2)", left, right)
	}
}

func TestTableView_StyleFunc_ZebraOnOddRowsOnly(t *testing.T) {
	v := tableView{Zebra: true}
	sf := v.styleFunc()
	noColor := lipgloss.NoColor{}

	if bg := sf(0, 0).GetBackground(); bg != noColor {
		t.Errorf("row 0 background = %v, want none (zebra applies to odd rows)", bg)
	}
	if bg := sf(1, 0).GetBackground(); bg == noColor {
		t.Errorf("row 1 background = none, want zebraBackground")
	}
	if bg := sf(2, 0).GetBackground(); bg != noColor {
		t.Errorf("row 2 background = %v, want none", bg)
	}
	if bg := sf(table.HeaderRow, 0).GetBackground(); bg != noColor {
		t.Errorf("header background = %v, want none — zebra must not touch the header row", bg)
	}
}

func TestTableView_StyleFunc_ZebraDisabledByDefault(t *testing.T) {
	v := tableView{}
	sf := v.styleFunc()
	if bg := sf(1, 0).GetBackground(); bg != (lipgloss.NoColor{}) {
		t.Errorf("row 1 background = %v, want none when Zebra is false", bg)
	}
}

func TestTableView_StyleFunc_CenterOnlyListedColumns(t *testing.T) {
	v := tableView{Center: []int{0, 2}}
	sf := v.styleFunc()

	for _, col := range []int{0, 2} {
		if align := sf(0, col).GetAlignHorizontal(); align != lipgloss.Center {
			t.Errorf("col %d align = %v, want Center", col, align)
		}
	}
	if align := sf(0, 1).GetAlignHorizontal(); align == lipgloss.Center {
		t.Errorf("col 1 align = Center, want unset — it is not listed in Center")
	}
}

func TestTableView_StyleFunc_CenterNilCentersNothing(t *testing.T) {
	v := tableView{}
	sf := v.styleFunc()
	for col := range 3 {
		if align := sf(0, col).GetAlignHorizontal(); align == lipgloss.Center {
			t.Errorf("col %d align = Center, want unset when Center is nil", col)
		}
	}
}

func TestTableView_StyleFunc_ComposesWithSemanticStyle(t *testing.T) {
	v := tableView{
		Style: func(row, col int) lipgloss.Style {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		},
		Padding: 1,
	}
	sf := v.styleFunc()
	got := sf(0, 0)
	if got.GetForeground() != lipgloss.Color("1") {
		t.Errorf("semantic foreground lost, got %v", got.GetForeground())
	}
	if left := got.GetPaddingLeft(); left != 1 {
		t.Errorf("padding not composed on top of semantic style, got left=%d, want 1", left)
	}
}

// TestPaddingZeroZeroIdenticalToNoPadding pins the assumption that lets
// tableView call style.Padding(0, v.Padding) unconditionally, with no
// `if v.Padding > 0` guard: Padding(0, 0) must render byte-identical to a
// style with no Padding call at all.
func TestPaddingZeroZeroIdenticalToNoPadding(t *testing.T) {
	headers := []string{"NAME", "VALUE"}
	rows := [][]string{{"a", "b"}}

	withZeroPadding := baseTable(headers...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerRowStyle().Padding(0, 0)
			}
			return lipgloss.NewStyle().Padding(0, 0)
		})
	withNoPadding := baseTable(headers...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerRowStyle()
			}
			return lipgloss.NewStyle()
		})

	got := renderRows(withZeroPadding, rows)
	want := renderRows(withNoPadding, rows)
	if got != want {
		t.Errorf("Padding(0,0) output differs from no Padding call:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestTableView_Render_ReproducesBaseTableEquivalent(t *testing.T) {
	pinGoldenPalette(t)

	headers, rows := goldenTableRows()
	v := tableView{Headers: headers, Rows: rows}

	got := v.Render(0)
	want := Table(headers, rows)
	if got != want {
		t.Errorf("tableView.Render(0) != Table():\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestTableView_Fits(t *testing.T) {
	headers := []string{"STATUS", "MESSAGE"}
	rows := [][]string{{"✗", "short"}}
	cols := []columnSpec{
		{},
		{Flex: true, Wrap: wrapText},
	}
	v := tableView{Headers: headers, Rows: rows, Cols: cols}

	if !v.Fits(0) {
		t.Errorf("Fits(0) = false, want true (unbounded budget always fits)")
	}

	floors := effectiveFloors(headers, rows, cols, naturalWidths(headers, rows, cols))
	chrome := len(headers) + 1
	tooSmall := sumInts(floors) + chrome - 1
	if v.Fits(tooSmall) {
		t.Errorf("Fits(%d) = true, want false when budget is below floors", tooSmall)
	}
}

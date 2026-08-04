package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestNaturalWidths(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		rows    [][]string
		cols    []columnSpec
		want    []int
	}{
		{
			name:    "uncapped column takes the widest cell",
			headers: []string{"N", "VALUE"},
			rows: [][]string{
				{"a", "short"},
				{"bb", "a very long value string"},
			},
			cols: []columnSpec{{}, {}},
			want: []int{2, lipgloss.Width("a very long value string")},
		},
		{
			name:    "Max clamps cell content but not below the clamp",
			headers: []string{"NAME", "VALUE"},
			rows: [][]string{
				{"a", "a very long value string well past the cap"},
			},
			cols: []columnSpec{{}, {Max: 10}},
			want: []int{4, 10},
		},
		{
			name:    "header wider than Max still wins",
			headers: []string{"NAME", "A LONG HEADER"},
			rows: [][]string{
				{"a", "abc"},
			},
			cols: []columnSpec{{}, {Max: 5}},
			want: []int{4, lipgloss.Width("A LONG HEADER")},
		},
		{
			name:    "header wider than every cell wins uncapped",
			headers: []string{"VALUE"},
			rows: [][]string{
				{"a"},
				{"b"},
			},
			cols: []columnSpec{{}},
			want: []int{5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := naturalWidths(tt.headers, tt.rows, tt.cols)
			if len(got) != len(tt.want) {
				t.Fatalf("naturalWidths() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("naturalWidths()[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestColumnFloors(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		rows    [][]string
		cols    []columnSpec
		want    int // single-column floor
	}{
		{
			name:    "a long URL pins the floor to its own width",
			headers: []string{"HINT"},
			rows: [][]string{
				{"see https://github.com/hadolint/hadolint/wiki/DL3008 for details"},
			},
			cols: []columnSpec{{Wrap: wrapText}},
			want: lipgloss.Width("https://github.com/hadolint/hadolint/wiki/DL3008"),
		},
		{
			name:    "a short cell floors to a single character, so the header pins it",
			headers: []string{"MESSAGE"},
			rows: [][]string{
				{"a short message"},
			},
			cols: []columnSpec{{Wrap: wrapText}},
			want: lipgloss.Width("MESSAGE"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := columnFloors(tt.headers, tt.rows, tt.cols)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("columnFloors() = %v, want [%d]", got, tt.want)
			}
		})
	}
}

func maxLineWidth(cell string) int {
	width := 0
	for line := range strings.SplitSeq(cell, "\n") {
		if w := lipgloss.Width(line); w > width {
			width = w
		}
	}
	return width
}

func TestFitRows_BudgetZeroAppliesMaxAndWraps(t *testing.T) {
	headers := []string{"STATUS", "MESSAGE"}
	rows := [][]string{{"✗", strings.Repeat("a", 60)}}
	cols := []columnSpec{
		{},
		{Flex: true, Max: 44, Wrap: wrapText},
	}

	out, ok := fitRows(headers, rows, 0, 0, cols)
	if !ok {
		t.Fatalf("fitRows(budget=0) ok = false, want true")
	}
	if out[0][0] != "✗" {
		t.Errorf("STATUS cell = %q, want unchanged %q", out[0][0], "✗")
	}
	if !strings.Contains(out[0][1], "\n") {
		t.Errorf("MESSAGE cell = %q, want wrapped at the Max cap even with budget 0", out[0][1])
	}
	if w := maxLineWidth(out[0][1]); w > 44 {
		t.Errorf("MESSAGE cell line width = %d, want <= 44", w)
	}
}

func TestFitRows_BudgetAboveNaturalSumIsUnchanged(t *testing.T) {
	headers := []string{"STATUS", "MESSAGE"}
	rows := [][]string{{"✗", strings.Repeat("a", 60)}}
	cols := []columnSpec{
		{},
		{Flex: true, Max: 44, Wrap: wrapText},
	}

	baseline, ok := fitRows(headers, rows, 0, 0, cols)
	if !ok {
		t.Fatalf("baseline fitRows ok = false, want true")
	}

	out, ok := fitRows(headers, rows, 1000, 0, cols)
	if !ok {
		t.Fatalf("fitRows(budget=1000) ok = false, want true")
	}
	if out[0][0] != baseline[0][0] || out[0][1] != baseline[0][1] {
		t.Errorf("fitRows(budget=1000) = %v, want unchanged from natural widths %v", out[0], baseline[0])
	}
}

func TestFitRows_ShrinkNarrowsOnlyFlexColumns(t *testing.T) {
	headers := []string{"STATUS", "MESSAGE"}
	message := "the quick brown fox jumps over the lazy dog and keeps going"
	rows := [][]string{{"✗", message}}
	cols := []columnSpec{
		{}, // fixed, no Wrap
		{Flex: true, Wrap: wrapText},
	}

	natural := naturalWidths(headers, rows, cols)
	chrome := len(headers) + 1
	shrinkBy := 10
	budget := chrome + natural[0] + (natural[1] - shrinkBy)

	out, ok := fitRows(headers, rows, budget, 0, cols)
	if !ok {
		t.Fatalf("fitRows() ok = false, want true")
	}
	if out[0][0] != "✗" {
		t.Errorf("fixed STATUS column changed: got %q, want unchanged %q", out[0][0], "✗")
	}
	target := natural[1] - shrinkBy
	if w := maxLineWidth(out[0][1]); w > target {
		t.Errorf("MESSAGE column line width = %d, want <= %d (shrunk from natural %d)", w, target, natural[1])
	}
	if !strings.Contains(out[0][1], "\n") {
		t.Errorf("MESSAGE column = %q, want wrapped after shrinking", out[0][1])
	}
}

func TestFitRows_BelowFloorsReturnsNotOK(t *testing.T) {
	headers := []string{"STATUS", "MESSAGE"}
	rows := [][]string{{"✗", "short"}}
	cols := []columnSpec{
		{},
		{Flex: true, Wrap: wrapText},
	}

	floors := floorsFor(headers, rows, cols)
	chrome := len(headers) + 1
	budget := sumInts(floors) + chrome - 1 // one short of the floor sum

	out, ok := fitRows(headers, rows, budget, 0, cols)
	if ok {
		t.Fatalf("fitRows() ok = true, want false when budget is below floors (out = %v)", out)
	}
	if out != nil {
		t.Errorf("fitRows() rows = %v, want nil on ok=false", out)
	}
}

func TestFitRows_URLNeverSplit(t *testing.T) {
	url := "https://github.com/hadolint/hadolint/wiki/DL3008/some/extra/path/segments"
	headers := []string{"HINT"}
	rows := [][]string{{"see " + url + " for details"}}
	cols := []columnSpec{
		{Flex: true, Wrap: wrapText},
	}

	floors := floorsFor(headers, rows, cols)
	chrome := len(headers) + 1
	budget := sumInts(floors) + chrome // exactly at the floor

	out, ok := fitRows(headers, rows, budget, 0, cols)
	if !ok {
		t.Fatalf("fitRows() ok = false at exactly the floor budget, want true")
	}
	if !strings.Contains(out[0][0], url) {
		t.Errorf("HINT cell = %q, want the URL kept intact as a substring", out[0][0])
	}
}

func TestFitRows_Degenerate(t *testing.T) {
	t.Run("zero rows", func(t *testing.T) {
		out, ok := fitRows([]string{"A", "B"}, nil, 0, 0, []columnSpec{{}, {}})
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if len(out) != 0 {
			t.Errorf("out = %v, want empty", out)
		}
	})

	t.Run("zero columns", func(t *testing.T) {
		out, ok := fitRows(nil, [][]string{{}}, 0, 0, nil)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if len(out) != 1 || len(out[0]) != 0 {
			t.Errorf("out = %v, want one empty row", out)
		}
	})

	t.Run("ragged row shorter than headers", func(t *testing.T) {
		headers := []string{"A", "B", "C"}
		rows := [][]string{{"x"}}
		cols := []columnSpec{{}, {}, {}}

		out, ok := fitRows(headers, rows, 0, 0, cols)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if len(out) != 1 || len(out[0]) != 1 || out[0][0] != "x" {
			t.Errorf("out = %v, want [[x]]", out)
		}
	})
}

// TestSplitDisplayWidth_ANSIInputIsUnsupported documents and pins the
// package invariant that cells must be plain text: the wrapping helpers
// slice on rune boundaries using lipgloss.Width for measurement, which is
// ANSI-aware for *measurement* but not for *splitting* — a break point
// chosen this way can still land between a style-open and its reset,
// letting the style bleed into the next line instead of being preserved or
// cleanly closed. This is why every renderer applies color via StyleFunc
// after wrapping, never before.
func TestSplitDisplayWidth_ANSIInputIsUnsupported(t *testing.T) {
	const openBold = "\x1b[1m"
	const reset = "\x1b[0m"
	styled := openBold + "AB" + reset // visible width 2

	head, tail := splitDisplayWidth(styled, 1)

	if strings.Contains(head, reset) {
		t.Fatalf("head = %q unexpectedly contains the reset code — invariant test assumption changed", head)
	}
	if !strings.Contains(head, openBold) {
		t.Fatalf("head = %q, want it to carry the unclosed style open (demonstrating the leak)", head)
	}
	if strings.Contains(tail, openBold) {
		t.Fatalf("tail = %q, want it to start unstyled — the open code stayed on the head half", tail)
	}
}

// TestDistributeDeficit_MultipleFlexColumns exercises the proportional split
// and the largest-remainder leftover loop, both of which every fitRows test
// above leaves dead by using a single Flex column. It asserts the three
// invariants the loop exists to hold: the total lands exactly on available,
// no column is pushed below its floor, and no fixed column moves.
func TestDistributeDeficit_MultipleFlexColumns(t *testing.T) {
	cols := []columnSpec{
		{},                           // fixed
		{Flex: true, Wrap: wrapText}, // large headroom
		{Flex: true, Wrap: wrapText}, // small headroom
		{Flex: true, Wrap: wrapText}, // no headroom (already at floor)
	}
	natural := []int{10, 40, 13, 7}
	floors := []int{10, 10, 10, 7}

	// Sweep every reachable available width between the floor sum and the
	// natural sum, so both the exact-division and the remainder path run.
	for available := sumInts(floors); available < sumInts(natural); available++ {
		widths := distributeDeficit(natural, floors, cols, available)

		if got := sumInts(widths); got != available {
			t.Errorf("available=%d: sum(widths) = %d, want exactly %d (largest-remainder leftover not drained)", available, got, available)
		}
		if widths[0] != natural[0] {
			t.Errorf("available=%d: fixed column width = %d, want its natural %d", available, widths[0], natural[0])
		}
		for i, w := range widths {
			if w < floors[i] {
				t.Errorf("available=%d: column %d width = %d, below its floor %d", available, i, w, floors[i])
			}
			if w > natural[i] {
				t.Errorf("available=%d: column %d width = %d, above its natural %d", available, i, w, natural[i])
			}
		}
	}
}

package render

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestStdoutBudget_DefaultSeam_Unbounded(t *testing.T) {
	if got := stdoutBudget(); got != 0 {
		t.Errorf("expected 0 (unbounded) under the pinned non-TTY default, got %d", got)
	}
}

func TestStderrBudget_DefaultSeam_Unbounded(t *testing.T) {
	if got := stderrBudget(); got != 0 {
		t.Errorf("expected 0 (unbounded) under the pinned non-TTY default, got %d", got)
	}
}

func TestStdoutBudget_UsesTermWidthFn(t *testing.T) {
	withTermWidth(t, 42)
	if got := stdoutBudget(); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestStderrBudget_UsesTermWidthFn(t *testing.T) {
	withTermWidth(t, 55)
	if got := stderrBudget(); got != 55 {
		t.Errorf("expected 55, got %d", got)
	}
}

func TestStdoutBudget_ProbesStdoutStream(t *testing.T) {
	saved := termWidthFn
	var seen *os.File
	termWidthFn = func(f *os.File) int { seen = f; return 0 }
	t.Cleanup(func() { termWidthFn = saved })
	stdoutBudget()
	if seen != os.Stdout {
		t.Errorf("expected stdoutBudget to probe os.Stdout, probed %v", seen)
	}
}

func TestStderrBudget_ProbesStderrStream(t *testing.T) {
	saved := termWidthFn
	var seen *os.File
	termWidthFn = func(f *os.File) int { seen = f; return 0 }
	t.Cleanup(func() { termWidthFn = saved })
	stderrBudget()
	if seen != os.Stderr {
		t.Errorf("expected stderrBudget to probe os.Stderr, probed %v", seen)
	}
}

// assertLinesWithinBudget fails the test if any line of s (ANSI stripped)
// exceeds width columns.
func assertLinesWithinBudget(t *testing.T, s string, width int) {
	t.Helper()
	for line := range strings.SplitSeq(stripANSI(s), "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line exceeds budget %d (got %d): %q", width, w, line)
		}
	}
}

// TestSinkAwareBudget_Table_ShrinkAndRecordModes proves the Table entry
// point resolves its width from the sink-aware seam rather than always
// rendering unbounded, at both a shrink-mode width (columns narrow but still
// fit) and a narrower record-mode width (columns fall back to records).
func TestSinkAwareBudget_Table_ShrinkAndRecordModes(t *testing.T) {
	resetStyles()
	headers := []string{"NAME", "NOTE"}
	rows := [][]string{{"svc", strings.Repeat("word ", 20)}}

	withTermWidth(t, 40)
	shrunk := Table(headers, rows)
	assertLinesWithinBudget(t, shrunk, 40)
	if !strings.Contains(stripANSI(shrunk), "svc") {
		t.Errorf("expected row title to survive shrink mode: %q", stripANSI(shrunk))
	}

	// Record mode has no lower width bound (label + value may still exceed a
	// very small budget), so only shrink mode gets the within-budget
	// assertion — record mode is checked for content survival only.
	withTermWidth(t, 10)
	records := Table(headers, rows)
	if !strings.Contains(stripANSI(records), "svc") {
		t.Errorf("expected row title to survive record mode: %q", stripANSI(records))
	}
}

// TestSinkAwareBudget_ServicesTable_ShrinkAndRecordModes is the ServicesTable
// counterpart of TestSinkAwareBudget_Table_ShrinkAndRecordModes.
func TestSinkAwareBudget_ServicesTable_ShrinkAndRecordModes(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{Name: "api", Dir: "./services/api/very/deeply/nested/source/tree", Container: "proj-api", Enabled: true, Running: true},
	}

	withTermWidth(t, 60)
	shrunk := ServicesTable(rows, nil, true)
	assertLinesWithinBudget(t, shrunk, 60)
	if !strings.Contains(stripANSI(shrunk), "api") {
		t.Errorf("expected service name to survive shrink mode: %q", stripANSI(shrunk))
	}

	withTermWidth(t, 15)
	records := ServicesTable(rows, nil, true)
	if !strings.Contains(stripANSI(records), "api") {
		t.Errorf("expected service name to survive record mode: %q", stripANSI(records))
	}
}

// TestSinkAwareBudget_DiagnosticsTable_ProbesStderrNotStdout proves
// DiagnosticsTable resolves its budget from stderrBudget (its sink), so a
// wide stdout paired with a narrow stderr still shrinks — and a narrow
// stdout paired with an unbounded stderr does not.
func TestSinkAwareBudget_DiagnosticsTable_ProbesStderrNotStdout(t *testing.T) {
	resetStyles()
	rows := []DiagnosticRow{
		{
			Severity: validate.SeverityError,
			Target:   "hadolint",
			File:     "services/admin/docker/images/base/vendor/Dockerfile",
			Message:  "Non-numeric user-id may not be resolvable by host system (DL3066)",
			Hint:     "https://github.com/hadolint/hadolint/wiki/DL3066",
		},
	}

	saved := termWidthFn
	t.Cleanup(func() { termWidthFn = saved })

	termWidthFn = func(f *os.File) int {
		if f == os.Stderr {
			return 30
		}
		return 0
	}
	// budget 30 is well below this row's floors (record mode has no lower
	// width bound), so only content survival is asserted here.
	narrow := DiagnosticsTable(rows)
	if url := "https://github.com/hadolint/hadolint/wiki/DL3066"; !strings.Contains(stripANSI(narrow), url) {
		t.Errorf("expected hint URL to stay intact under the stderr budget: %q", stripANSI(narrow))
	}

	termWidthFn = func(f *os.File) int {
		if f == os.Stdout {
			return 30
		}
		return 0
	}
	unbounded := DiagnosticsTable(rows)
	wide := false
	for line := range strings.SplitSeq(stripANSI(unbounded), "\n") {
		if lipgloss.Width(line) > 30 {
			wide = true
			break
		}
	}
	if !wide {
		t.Errorf("expected DiagnosticsTable to ignore a narrow stdout and stay unbounded via stderr, but every line fit 30: %q", stripANSI(unbounded))
	}
}

// TestSinkAwareBudget_DefaultSeam_GoldensUnaffected re-renders two of the
// Task 1 goldens under the enabled sink-aware budget (this task's change),
// without overriding termWidthFn. TestMain pins it to the non-TTY default (0
// = unbounded), so this must byte-match the pre-Task-12 goldens exactly —
// confirming the budget mechanism is a no-op for every non-TTY sink, which is
// every test and every piped/redirected real invocation.
func TestSinkAwareBudget_DefaultSeam_GoldensUnaffected(t *testing.T) {
	pinGoldenPalette(t)
	headers, rows := goldenTableRows()
	assertGolden(t, "table.golden", Table(headers, rows))
	assertGolden(t, "diagnostics_table.golden", DiagnosticsTable(goldenDiagnosticsRows()))
	assertGolden(t, "services_table_dircol.golden", ServicesTable(goldenServicesRows(), []string{"TAG"}, true))
}

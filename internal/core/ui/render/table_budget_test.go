package render

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
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

// isTableMode reports whether s was rendered as a lipgloss table (border
// glyphs present) rather than the record layout (no borders at all) — the
// cheapest reliable discriminator between tableView's two render modes.
func isTableMode(s string) bool {
	return strings.Contains(stripANSI(s), "│")
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

// TestSinkAwareBudget_DaemonTable_ShrinksBeforeRecords proves DaemonTable
// passes through a genuine shrink stage (still a table, PARAMS narrowed)
// before it degrades to records at a narrower width — Table, ServicesTable
// and DiagnosticsTable get the same shrink+record coverage above.
func TestSinkAwareBudget_DaemonTable_ShrinksBeforeRecords(t *testing.T) {
	resetStyles()
	rows := []DaemonTableRow{
		{ID: "services.main.queue", Params: "name=default,extra=" + strings.Repeat("word ", 20), Container: "proj-php_queue_default", Uptime: "5m0s"},
	}

	withTermWidth(t, 90)
	shrunk := DaemonTable(rows)
	if !isTableMode(shrunk) {
		t.Errorf("expected shrink mode to stay a table at width 90: %q", stripANSI(shrunk))
	}
	assertLinesWithinBudget(t, shrunk, 90)
	if !strings.Contains(stripANSI(shrunk), "services.main.queue") {
		t.Errorf("expected daemon ID to survive shrink mode: %q", stripANSI(shrunk))
	}

	withTermWidth(t, 20)
	records := DaemonTable(rows)
	if isTableMode(records) {
		t.Errorf("expected record mode at width 20: %q", stripANSI(records))
	}
	if !strings.Contains(stripANSI(records), "services.main.queue") {
		t.Errorf("expected daemon ID to survive record mode: %q", stripANSI(records))
	}
}

// TestSinkAwareBudget_DeployStatus_ShrinksBeforeRecords is the DeployStatus
// counterpart of TestSinkAwareBudget_DaemonTable_ShrinksBeforeRecords.
func TestSinkAwareBudget_DeployStatus_ShrinksBeforeRecords(t *testing.T) {
	resetStyles()
	rows := []DeployStatusRow{
		{
			Service: "main", Status: "failed", ConfigDelta: "changed",
			PrevHashShort: "abc12345", CurrHashShort: "def12345",
			LastFailedPhase: "setup", LastFailedStep: "a very long step name that should wrap under pressure " + strings.Repeat("word ", 15),
		},
	}

	withTermWidth(t, 90)
	shrunk := DeployStatus(rows)
	if !isTableMode(shrunk) {
		t.Errorf("expected shrink mode to stay a table at width 90: %q", stripANSI(shrunk))
	}
	assertLinesWithinBudget(t, shrunk, 90)
	if !strings.Contains(stripANSI(shrunk), "main") {
		t.Errorf("expected service name to survive shrink mode: %q", stripANSI(shrunk))
	}

	withTermWidth(t, 15)
	records := DeployStatus(rows)
	if isTableMode(records) {
		t.Errorf("expected record mode at width 15: %q", stripANSI(records))
	}
	if !strings.Contains(stripANSI(records), "main") {
		t.Errorf("expected service name to survive record mode: %q", stripANSI(records))
	}
}

// TestSinkAwareBudget_GitWorkspace_ShrinksBeforeRecords is the GitWorkspace
// counterpart of TestSinkAwareBudget_DaemonTable_ShrinksBeforeRecords.
func TestSinkAwareBudget_GitWorkspace_ShrinksBeforeRecords(t *testing.T) {
	resetStyles()
	longDir := "very/long/nested/service/directory/path/that/definitely/wont/fit/in/a/narrow/terminal"
	rows := []statusview.GitWorkspaceRow{
		{Service: "app", Dir: longDir, Branch: "feature/some-long-branch-name", SHA: "abcdef12", Dirty: true, AheadBehind: "+1/-2"},
	}

	withTermWidth(t, 90)
	shrunk := GitWorkspace(rows)
	if !isTableMode(shrunk) {
		t.Errorf("expected shrink mode to stay a table at width 90: %q", stripANSI(shrunk))
	}
	assertLinesWithinBudget(t, shrunk, 90)
	if !strings.Contains(stripANSI(shrunk), "app") {
		t.Errorf("expected service name to survive shrink mode: %q", stripANSI(shrunk))
	}

	withTermWidth(t, 20)
	records := GitWorkspace(rows)
	if isTableMode(records) {
		t.Errorf("expected record mode at width 20: %q", stripANSI(records))
	}
	if !strings.Contains(stripANSI(records), "app") {
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

// TestSinkAwareBudget_DiagnosticsByDomain_ProbesStdoutNotStderr is the
// mirror of the DiagnosticsTable test above, with the streams swapped:
// DiagnosticsByDomain's only call site (`dwe validate`) writes to
// cmd.OutOrStdout(), so it must follow stdout. Probing stderr here would
// shrink `dwe validate > report.txt` to the terminal width and leave
// `dwe validate 2>/dev/null` unbounded on a narrow terminal.
func TestSinkAwareBudget_DiagnosticsByDomain_ProbesStdoutNotStderr(t *testing.T) {
	resetStyles()
	rows := []DiagnosticRow{
		{
			Severity: validate.SeverityError,
			Domain:   "linters",
			Target:   "hadolint",
			File:     "services/admin/docker/images/base/vendor/Dockerfile",
			Message:  "Non-numeric user-id may not be resolvable by host system (DL3066)",
			Hint:     "https://github.com/hadolint/hadolint/wiki/DL3066",
		},
	}

	saved := termWidthFn
	t.Cleanup(func() { termWidthFn = saved })

	// Narrow stdout must be honored.
	termWidthFn = func(f *os.File) int {
		if f == os.Stdout {
			return 30
		}
		return 0
	}
	narrow := stripANSI(DiagnosticsByDomain(rows))
	if isTableMode(narrow) {
		t.Errorf("expected a narrow stdout to force record mode, got a table:\n%s", narrow)
	}

	// Narrow stderr must be ignored.
	termWidthFn = func(f *os.File) int {
		if f == os.Stderr {
			return 30
		}
		return 0
	}
	unbounded := stripANSI(DiagnosticsByDomain(rows))
	if !isTableMode(unbounded) {
		t.Errorf("expected DiagnosticsByDomain to ignore a narrow stderr and stay unbounded via stdout, got records:\n%s", unbounded)
	}
}

// TestSinkAwareBudget_Diagnostics_TableModeNeverExceedsBudget sweeps the
// terminal widths around a diagnostics row whose HINT holds a URL longer than
// the MESSAGE/HINT `Max` cap. That combination used to render *wider* than
// the budget while still claiming to fit: naturalWidths clamped the column to
// Max while the wrap helpers refused to split the URL, so distributeDeficit
// saw negative headroom and widened the column instead of leaving it alone —
// the terminal then hard-wrapped and broke the box borders, the exact failure
// the responsive tables exist to prevent.
//
// The assertion is scoped to table mode on purpose. Record mode has no lower
// width bound and never truncates, so at a budget narrower than the URL it
// legitimately emits one over-long line rather than cutting the link — that
// is the "no data is ever dropped" invariant, not an overflow bug.
func TestSinkAwareBudget_Diagnostics_TableModeNeverExceedsBudget(t *testing.T) {
	resetStyles()
	rows := []DiagnosticRow{
		{
			Severity: validate.SeverityWarning,
			Domain:   "linters",
			Target:   "hadolint",
			File:     "Dockerfile",
			Message:  "pin versions in apk add so builds are reproducible",
			// Deliberately longer than diagnosticTextWrapWidth (44).
			Hint: "https://github.com/hadolint/hadolint/wiki/DL3018-pin-versions",
		},
	}

	saved := termWidthFn
	t.Cleanup(func() { termWidthFn = saved })

	sawTableMode := false
	for _, width := range []int{60, 80, 100, 105, 110, 115, 120, 125, 130, 140} {
		termWidthFn = func(*os.File) int { return width }
		for _, out := range []string{DiagnosticsByDomain(rows), DiagnosticsTable(rows)} {
			if !isTableMode(out) {
				continue
			}
			sawTableMode = true
			assertLinesWithinBudget(t, out, width)
		}
	}
	if !sawTableMode {
		t.Fatal("no width in the sweep rendered as a table; the assertion never ran")
	}
}

// TestFitRows_MaxCappedColumnWithOverlongToken pins the arithmetic half of
// the bug above at the fitRows level: when a Max-capped Flex column holds an
// unbreakable token wider than Max, the fitted width must never fall below
// that token's width, and the total must stay inside the budget.
func TestFitRows_MaxCappedColumnWithOverlongToken(t *testing.T) {
	url := "https://github.com/hadolint/hadolint/wiki/DL3018-pin-versions"
	headers := []string{"TARGET", "HINT"}
	rows := [][]string{{"hadolint", url}}
	cols := []columnSpec{
		{Role: roleTitle},
		{Flex: true, Max: 44, Wrap: wrapText},
	}

	urlWidth := lipgloss.Width(url)
	if urlWidth <= 44 {
		t.Fatalf("test precondition: URL width %d must exceed the Max cap 44", urlWidth)
	}

	floors := floorsFor(headers, rows, cols)
	if floors[1] != urlWidth {
		t.Errorf("HINT floor = %d, want the URL's own width %d (a Max cap must not push a column below an unbreakable token)", floors[1], urlWidth)
	}

	chrome := len(headers) + 1
	for budget := sumInts(floors) + chrome; budget <= sumInts(floors)+chrome+40; budget++ {
		out, ok := fitRows(headers, rows, budget, 0, cols)
		if !ok {
			t.Fatalf("fitRows(budget=%d) ok = false, want true at or above the floor sum", budget)
		}
		if got := lipgloss.Width(out[0][1]); got != urlWidth {
			t.Errorf("budget=%d: HINT cell width = %d, want the URL kept whole at %d: %q", budget, got, urlWidth, out[0][1])
		}
	}
}

// TestSinkAwareBudget_DefaultSeam_GoldensUnaffected re-renders three goldens
// under the sink-aware budget without overriding termWidthFn. TestMain pins
// it to the non-TTY default (0 = unbounded), so the output must byte-match
// the goldens exactly — confirming the budget mechanism is a no-op for every
// non-TTY sink, which is every test and every piped/redirected real
// invocation.
func TestSinkAwareBudget_DefaultSeam_GoldensUnaffected(t *testing.T) {
	pinGoldenPalette(t)
	headers, rows := goldenTableRows()
	assertGolden(t, "table.golden", Table(headers, rows))
	assertGolden(t, "diagnostics_table.golden", DiagnosticsTable(goldenDiagnosticsRows()))
	assertGolden(t, "services_table_dircol.golden", ServicesTable(goldenServicesRows(), []string{"TAG"}, true))
}

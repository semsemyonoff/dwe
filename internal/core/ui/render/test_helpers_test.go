package render

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// TestMain pins termWidthFn to a non-TTY default (0 = unbounded) for the
// whole suite, so the sink-probing renderers (Table, ServicesTable,
// DaemonTable, DeployStatus, GitWorkspace, DiagnosticsTable,
// DiagnosticsByDomain) behave identically whether the compiled test binary
// is run through `go test` (stdout/stderr piped, already non-TTY) or
// directly from a real terminal. Without this pin the goldens would only
// hold under `go test`.
func TestMain(m *testing.M) {
	termWidthFn = func(*os.File) int { return 0 }
	os.Exit(m.Run())
}

// withTermWidth swaps termWidthFn to report w for every stream and restores
// the non-TTY default via t.Cleanup. Used to exercise shrink/record mode,
// which the sink probe otherwise never reaches under go test.
func withTermWidth(t *testing.T, w int) {
	t.Helper()
	saved := termWidthFn
	termWidthFn = func(*os.File) int { return w }
	t.Cleanup(func() { termWidthFn = saved })
}

// floorsFor is the fit-decision floor vector fitRows would compute for
// (headers, rows, cols). Tests derive their "exactly at / one below the
// floors" budgets from it, so they stay pinned to the real algorithm rather
// than to a hand-counted number.
func floorsFor(headers []string, rows [][]string, cols []columnSpec) []int {
	probed := columnFloors(headers, rows, cols)
	natural := naturalWidths(headers, rows, cols)
	for i := range natural {
		if probed[i] > natural[i] {
			natural[i] = probed[i]
		}
	}
	return effectiveFloors(probed, cols, natural)
}

// fitsAt reports whether v renders as a table (rather than falling back to
// records) at budget — the boolean half of tableView.fit, which is all the
// mode-selection tests care about.
func fitsAt(v tableView, budget int) bool {
	_, ok := v.fit(budget)
	return ok
}

// resetStyles re-initialises the styles package palette to the built-in
// defaults for the current dark/light mode, via the styles package's public
// API.
func resetStyles() {
	styles.ApplyStyles(nil)
	styles.DefSep = "—"
}

// pinGoldenPalette pins the ANSI color profile to TrueColor and the
// background mode to dark before calling resetStyles, then restores both via
// t.Cleanup. Byte-exact golden comparisons need both pinned: resetStyles
// alone resolves the palette through lipgloss.HasDarkBackground(), and
// zebraBackground (diagnostics_table.go) is a lipgloss.AdaptiveColor — so the
// same golden would hold different ANSI values on a light versus dark
// terminal without this. Tests using it must not call t.Parallel(): it
// mutates package-level lipgloss/styles state for its duration.
func pinGoldenPalette(t *testing.T) {
	t.Helper()
	savedProfile := lipgloss.ColorProfile()
	savedDark := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(savedProfile)
		lipgloss.SetHasDarkBackground(savedDark)
		// resetStyles again once the profile is back: the styles package
		// resolves its palette at ApplyStyles time, so leaving the values
		// computed under the pinned TrueColor/dark profile would leak into
		// whatever test runs next.
		resetStyles()
	})
	resetStyles()
}

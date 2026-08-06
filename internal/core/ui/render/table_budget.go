package render

import (
	"os"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// termWidthFn resolves a stream's terminal width via styles.TermWidthOrZero.
// Test seam, following the cmdbrowser/fallback.go convention: pinned to a
// non-TTY default by TestMain (test_helpers_test.go) so the suite cannot
// flip render modes depending on whether the compiled test binary is run
// directly (a real terminal) or through `go test` (piped, non-TTY).
var termWidthFn = styles.TermWidthOrZero

// stdoutBudget resolves the width budget for every public renderer except
// DiagnosticsTable — all of them write to stdout, including
// DiagnosticsByDomain (`dwe validate` writes to cmd.OutOrStdout()).
func stdoutBudget() int { return termWidthFn(os.Stdout) }

// stderrBudget resolves the width budget for DiagnosticsTable. Its three
// call sites (preflight, deploy menu ×2) all write diagnostics to stderr,
// never stdout, so it is the sole stderr consumer. Note the sibling
// DiagnosticsByDomain is NOT one: same rows, different sink.
func stderrBudget() int { return termWidthFn(os.Stderr) }

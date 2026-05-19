package pipeline

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts that no goroutines outlive the test binary — important for
// the bubbletea program launched by PlainReporter in TTY parallel mode. A hung
// tea.Run() would otherwise be invisible to CI.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// Bubbletea v2 spawns a signal-handling goroutine even with
		// WithoutSignalHandler in some code paths; allow stragglers from
		// the muesli/cancelreader package which is documented to leak a
		// goroutine until OS pipe close (we always use bytes.Reader, so
		// this is a defensive ignore that should rarely fire).
		goleak.IgnoreTopFunction("github.com/muesli/cancelreader.(*posixCancelReader).Read"),
	)
}

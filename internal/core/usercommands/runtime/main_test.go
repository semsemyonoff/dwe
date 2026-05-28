package runtime

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts that no goroutines outlive the runtime test binary —
// important now that workflow parallel groups spawn errgroup workers.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

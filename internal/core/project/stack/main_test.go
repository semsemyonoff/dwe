package stack

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts that no goroutines outlive the test binary — guards the
// errgroup-backed git workspace collector against leaks under SetLimit
// pressure.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

package bridge

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts that no goroutines outlive the test binary: daemon accept
// loops and session pumps must all drain through Close, and fakeProcess
// scripts must be unblocked by their tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

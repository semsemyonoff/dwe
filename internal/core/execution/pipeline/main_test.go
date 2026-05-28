package pipeline

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts that no goroutines outlive the test binary.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

package notify

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts that no goroutines outlive the test binary.
// nativeBackend spawns a goroutine per notify call; this catches leaks
// from tests that swap beeepNotify and forget to release blocking calls.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

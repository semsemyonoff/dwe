package cmdctx

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
)

// TestNonInteractiveEnv_MatchesKeygate pins the truthiness set AND its
// duplicate in core/workflow/keygate, which core callers use because they
// cannot import the cli layer. The pin lives here so keygate's own test binary
// never has to import cmdctx: a value that is non-interactive for `dwe run`
// but interactive for the key gate would open a form inside CI.
func TestNonInteractiveEnv_MatchesKeygate(t *testing.T) {
	cases := map[string]bool{
		"1":     true,
		"true":  true,
		"":      false,
		"0":     false,
		"false": false,
		"yes":   false,
		"TRUE":  false,
	}
	for value, want := range cases {
		t.Setenv("DWE_NONINTERACTIVE", value)
		if got := NonInteractiveEnv(); got != want {
			t.Errorf("cmdctx.NonInteractiveEnv() with %q = %v, want %v", value, got, want)
		}
		if got := keygate.NonInteractiveEnv(); got != NonInteractiveEnv() {
			t.Errorf("keygate.NonInteractiveEnv() with %q = %v, want %v", value, got, NonInteractiveEnv())
		}
	}
}

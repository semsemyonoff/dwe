package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestLevelFrom verifies the flag/env → trace.Level mapping, including the
// rule that --debug or a truthy DWE_DEBUG wins over --verbose.
func TestLevelFrom(t *testing.T) {
	tests := []struct {
		name     string
		verbose  bool
		debug    bool
		dweDebug string
		want     trace.Level
	}{
		{"no flags", false, false, "", trace.LevelOff},
		{"verbose only", true, false, "", trace.LevelVerbose},
		{"debug flag", false, true, "", trace.LevelDebug},
		{"dwe_debug=1", false, false, "1", trace.LevelDebug},
		{"dwe_debug=true", false, false, "true", trace.LevelDebug},
		{"dwe_debug=0 off", false, false, "0", trace.LevelOff},
		{"dwe_debug=false off", false, false, "false", trace.LevelOff},
		{"dwe_debug=no off", false, false, "no", trace.LevelOff},
		{"dwe_debug=off off", false, false, "off", trace.LevelOff},
		{"dwe_debug empty off", false, false, "", trace.LevelOff},
		{"verbose + dwe_debug=1 → debug", true, false, "1", trace.LevelDebug},
		{"verbose + dwe_debug=0 → verbose", true, false, "0", trace.LevelVerbose},
		{"debug flag + dwe_debug=0 → debug", false, true, "0", trace.LevelDebug},
		{"dwe_debug arbitrary truthy", false, false, "yes", trace.LevelDebug},
		{"dwe_debug whitespace trimmed", false, false, "  0  ", trace.LevelOff},
		{"dwe_debug mixed case FALSE", false, false, "FALSE", trace.LevelOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := levelFrom(tt.verbose, tt.debug, tt.dweDebug); got != tt.want {
				t.Errorf("levelFrom(%v, %v, %q) = %v, want %v",
					tt.verbose, tt.debug, tt.dweDebug, got, tt.want)
			}
		})
	}
}

// TestDebugEnvTruthy spot-checks the DWE_DEBUG truthiness helper.
func TestDebugEnvTruthy(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "yes", "on", "anything"}
	falsey := []string{"", "0", "false", "no", "off", "OFF", "  ", " 0 "}
	for _, v := range truthy {
		if !debugEnvTruthy(v) {
			t.Errorf("debugEnvTruthy(%q) = false, want true", v)
		}
	}
	for _, v := range falsey {
		if debugEnvTruthy(v) {
			t.Errorf("debugEnvTruthy(%q) = true, want false", v)
		}
	}
}

// runRootForTrace executes the root command with the given args against a
// throwaway project so PersistentPreRunE (and thus trace.Configure) runs.
func runRootForTrace(t *testing.T, args ...string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: tracetest\n  prefix: dwe\n"), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}
	root.SetArgs(append([]string{"version"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("executing root with args %v: %v", args, err)
	}
}

// TestRootConfiguresTraceLevel verifies that the persistent flags wire through
// to trace.Configure during PersistentPreRunE. trace holds global state, so the
// subtests run sequentially and reset the level after each.
func TestRootConfiguresTraceLevel(t *testing.T) {
	// Ensure DWE_DEBUG does not leak in from the host environment.
	oldDweDebug, hadDweDebug := os.LookupEnv("DWE_DEBUG")
	_ = os.Unsetenv("DWE_DEBUG")
	t.Cleanup(func() {
		if hadDweDebug {
			_ = os.Setenv("DWE_DEBUG", oldDweDebug)
		} else {
			_ = os.Unsetenv("DWE_DEBUG")
		}
		trace.Configure(nil, trace.LevelOff)
	})

	t.Run("no flags → Off", func(t *testing.T) {
		trace.Configure(nil, trace.LevelDebug) // poison to prove it gets reset
		runRootForTrace(t)
		if trace.Enabled(trace.LevelVerbose) {
			t.Error("expected LevelOff with no flags")
		}
	})

	t.Run("--verbose → Verbose", func(t *testing.T) {
		trace.Configure(nil, trace.LevelOff)
		runRootForTrace(t, "--verbose")
		if !trace.Enabled(trace.LevelVerbose) {
			t.Error("expected Verbose enabled with --verbose")
		}
		if trace.Enabled(trace.LevelDebug) {
			t.Error("did not expect Debug with only --verbose")
		}
	})

	t.Run("-v shorthand → Verbose", func(t *testing.T) {
		trace.Configure(nil, trace.LevelOff)
		runRootForTrace(t, "-v")
		if !trace.Enabled(trace.LevelVerbose) {
			t.Error("expected Verbose enabled with -v")
		}
	})

	t.Run("--debug → Debug", func(t *testing.T) {
		trace.Configure(nil, trace.LevelOff)
		runRootForTrace(t, "--debug")
		if !trace.Enabled(trace.LevelDebug) {
			t.Error("expected Debug enabled with --debug")
		}
	})

	t.Run("DWE_DEBUG=1 (no flag) → Debug", func(t *testing.T) {
		trace.Configure(nil, trace.LevelOff)
		t.Setenv("DWE_DEBUG", "1")
		runRootForTrace(t)
		if !trace.Enabled(trace.LevelDebug) {
			t.Error("expected Debug enabled with DWE_DEBUG=1")
		}
	})

	t.Run("DWE_DEBUG=0 (no flag) → Off", func(t *testing.T) {
		trace.Configure(nil, trace.LevelDebug)
		t.Setenv("DWE_DEBUG", "0")
		runRootForTrace(t)
		if trace.Enabled(trace.LevelVerbose) {
			t.Error("expected Off with DWE_DEBUG=0 and no flag")
		}
	})

	t.Run("--verbose + DWE_DEBUG=1 → Debug", func(t *testing.T) {
		trace.Configure(nil, trace.LevelOff)
		t.Setenv("DWE_DEBUG", "1")
		runRootForTrace(t, "--verbose")
		if !trace.Enabled(trace.LevelDebug) {
			t.Error("expected Debug when --verbose combines with DWE_DEBUG=1")
		}
	})
}

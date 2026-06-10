package runio

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

// stubStdoutTTY pins the stdout tty probe for one test.
func stubStdoutTTY(t *testing.T, isTTY bool) {
	t.Helper()
	restore := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return isTTY }
	t.Cleanup(func() { stdoutIsTerminal = restore })
}

func TestColorForced(t *testing.T) {
	tests := []struct {
		name    string
		force   string
		noColor string
		want    bool
	}{
		{"unset", "", "", false},
		{"forced", "1", "", true},
		{"forced truthy string", "yes", "", true},
		{"explicit zero", "0", "", false},
		{"NO_COLOR wins", "1", "1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLICOLOR_FORCE", tt.force)
			t.Setenv("NO_COLOR", tt.noColor)
			if tt.force == "" {
				_ = os.Unsetenv("CLICOLOR_FORCE")
			}
			if tt.noColor == "" {
				_ = os.Unsetenv("NO_COLOR")
			}
			if got := ColorForced(); got != tt.want {
				t.Errorf("ColorForced() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestColorForceEnv_gates(t *testing.T) {
	clearColorVars(t)

	t.Run("parallel always forces", func(t *testing.T) {
		stubStdoutTTY(t, true)
		if env := ColorForceEnv(spec.RunContext{UnderParallel: true}); len(env) == 0 {
			t.Error("parallel sub-step must force color env")
		}
	})
	t.Run("sequential on a terminal stays auto", func(t *testing.T) {
		stubStdoutTTY(t, true)
		t.Setenv("CLICOLOR_FORCE", "1")
		if env := ColorForceEnv(spec.RunContext{}); env != nil {
			t.Errorf("terminal children auto-detect; want nil, got %v", env)
		}
	})
	t.Run("color-forced pipe forces", func(t *testing.T) {
		stubStdoutTTY(t, false)
		t.Setenv("CLICOLOR_FORCE", "1")
		env := ColorForceEnv(spec.RunContext{})
		if len(env) == 0 {
			t.Fatal("color-forced pipe must force child color env")
		}
		joined := strings.Join(env, " ")
		for _, want := range []string{"CLICOLOR_FORCE=1", "FORCE_COLOR=1", "COLORTERM=truecolor"} {
			if !strings.Contains(joined, want) {
				t.Errorf("env %v missing %q", env, want)
			}
		}
	})
	t.Run("plain pipe stays auto", func(t *testing.T) {
		stubStdoutTTY(t, false)
		if env := ColorForceEnv(spec.RunContext{}); env != nil {
			t.Errorf("unforced pipe must not coerce; want nil, got %v", env)
		}
	})
	t.Run("NO_COLOR wins over parallel", func(t *testing.T) {
		stubStdoutTTY(t, false)
		t.Setenv("NO_COLOR", "1")
		if env := ColorForceEnv(spec.RunContext{UnderParallel: true}); env != nil {
			t.Errorf("NO_COLOR must suppress forcing; got %v", env)
		}
	})
}

// clearColorVars guarantees the color-control vars are absent (t.Setenv
// registers restores; the explicit clear leaves them unset during the test).
func clearColorVars(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NO_COLOR", "CLICOLOR_FORCE", bridgeclient.EnvBridgeStdinTTY} {
		t.Setenv(name, "x")
		_ = os.Unsetenv(name)
	}
}

func TestBridgedTTYActive_gates(t *testing.T) {
	clearColorVars(t)

	set := func(t *testing.T, force, stdinTTY bool, stdoutTTY bool) {
		t.Helper()
		stubStdoutTTY(t, stdoutTTY)
		if force {
			t.Setenv("CLICOLOR_FORCE", "1")
		}
		if stdinTTY {
			t.Setenv(bridgeclient.EnvBridgeStdinTTY, "1")
		}
	}

	t.Run("bridged interactive", func(t *testing.T) {
		set(t, true, true, false)
		if !bridgedTTYActive(spec.RunContext{}) {
			t.Error("forced color + stdin-tty hint on a pipe must activate the PTY path")
		}
	})
	t.Run("no stdin hint", func(t *testing.T) {
		set(t, true, false, false)
		if bridgedTTYActive(spec.RunContext{}) {
			t.Error("piped stdin (no hint) must never get a PTY")
		}
	})
	t.Run("no color force", func(t *testing.T) {
		set(t, false, true, false)
		if bridgedTTYActive(spec.RunContext{}) {
			t.Error("unforced run must not allocate a PTY")
		}
	})
	t.Run("real terminal", func(t *testing.T) {
		set(t, true, true, true)
		if bridgedTTYActive(spec.RunContext{}) {
			t.Error("a real terminal needs no PTY wrap")
		}
	})
	t.Run("parallel excluded", func(t *testing.T) {
		set(t, true, true, false)
		if bridgedTTYActive(spec.RunContext{UnderParallel: true}) {
			t.Error("parallel sub-steps use ParallelChildIO, not the bridged path")
		}
	})
}

// TestWireChildIO_bridgedPTY runs a real child through the bridged PTY path
// and verifies it sees a terminal on stdin/stdout/stderr and that its output
// arrives in the context stdout.
func TestWireChildIO_bridgedPTY(t *testing.T) {
	clearColorVars(t)
	stubStdoutTTY(t, false)
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv(bridgeclient.EnvBridgeStdinTTY, "1")

	var out syncBuffer
	rc := spec.RunContext{Stdout: &out, Stderr: &out, Stdin: strings.NewReader("")}
	c := exec.Command("sh", "-c", "test -t 0 && echo IN=TTY || echo IN=NOTTY; test -t 1 && echo OUT=TTY || echo OUT=NOTTY; test -t 2 && echo ERR=TTY || echo ERR=NOTTY")
	cleanup := WireChildIO(rc, c)
	if err := c.Run(); err != nil {
		cleanup()
		t.Fatalf("child run: %v", err)
	}
	cleanup()

	got := out.String()
	for _, want := range []string{"IN=TTY", "OUT=TTY", "ERR=TTY"} {
		if !strings.Contains(got, want) {
			t.Errorf("child must see a tty on every stream; output:\n%s", got)
		}
	}
}

// TestWireChildIO_directWhenNoHint pins the fallback: forced color without
// the stdin-tty hint keeps plain direct wiring (no PTY, separate stderr).
func TestWireChildIO_directWhenNoHint(t *testing.T) {
	clearColorVars(t)
	stubStdoutTTY(t, false)
	t.Setenv("CLICOLOR_FORCE", "1")

	var out, errBuf bytes.Buffer
	rc := spec.RunContext{Stdout: &out, Stderr: &errBuf, Stdin: strings.NewReader("")}
	c := exec.Command("sh", "-c", "test -t 1 && echo OUT=TTY || echo OUT=NOTTY; echo errline >&2")
	cleanup := WireChildIO(rc, c)
	if err := c.Run(); err != nil {
		cleanup()
		t.Fatalf("child run: %v", err)
	}
	cleanup()

	if !strings.Contains(out.String(), "OUT=NOTTY") {
		t.Errorf("direct wiring expected (no PTY); stdout:\n%s", out.String())
	}
	if !strings.Contains(errBuf.String(), "errline") {
		t.Errorf("stderr must stay separate in direct wiring; got %q", errBuf.String())
	}
}

// TestBridgedPTY_cleanupUnblocksPromptly guards the teardown path: cleanup
// must return even though the stdin pump is parked on a blocking reader.
func TestBridgedPTY_cleanupUnblocksPromptly(t *testing.T) {
	clearColorVars(t)
	stubStdoutTTY(t, false)
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv(bridgeclient.EnvBridgeStdinTTY, "1")

	var out syncBuffer
	rc := spec.RunContext{Stdout: &out, Stderr: &out, Stdin: blockingReader{}}
	c := exec.Command("true")
	cleanup := WireChildIO(rc, c)
	if err := c.Run(); err != nil {
		t.Fatalf("child run: %v", err)
	}

	done := make(chan struct{})
	go func() { cleanup(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not return with a blocked stdin pump")
	}
}

// blockingReader blocks forever (an os.Stdin with no data, minus the file).
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

// syncBuffer is a goroutine-safe bytes.Buffer (the PTY pump writes from its
// own goroutine).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

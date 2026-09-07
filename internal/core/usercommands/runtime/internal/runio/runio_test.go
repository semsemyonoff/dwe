package runio

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

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
		if env := ColorForceEnv(spec.RunContext{UnderParallel: true}, false); len(env) == 0 {
			t.Error("parallel sub-step must force color env")
		}
	})
	t.Run("sequential on a terminal stays auto", func(t *testing.T) {
		stubStdoutTTY(t, true)
		t.Setenv("CLICOLOR_FORCE", "1")
		if env := ColorForceEnv(spec.RunContext{}, false); env != nil {
			t.Errorf("terminal children auto-detect; want nil, got %v", env)
		}
	})
	t.Run("color-forced pipe forces", func(t *testing.T) {
		stubStdoutTTY(t, false)
		t.Setenv("CLICOLOR_FORCE", "1")
		env := ColorForceEnv(spec.RunContext{}, false)
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
		if env := ColorForceEnv(spec.RunContext{}, false); env != nil {
			t.Errorf("unforced pipe must not coerce; want nil, got %v", env)
		}
	})
	t.Run("NO_COLOR wins over parallel", func(t *testing.T) {
		stubStdoutTTY(t, false)
		t.Setenv("NO_COLOR", "1")
		if env := ColorForceEnv(spec.RunContext{UnderParallel: true}, false); env != nil {
			t.Errorf("NO_COLOR must suppress forcing; got %v", env)
		}
	})
}

// TestColorForceEnv_suppressedTTY covers the third disjunct — the caller told
// us it is about to take the child's terminal away (the service runner's `-T`).
//
// The nil-stdout row is the one that pins the deliberate asymmetry with
// WantContainerTTY: this disjunct probes the RAW rc.Stdout, so an internal
// caller that never set it — and is very likely parsing the output — is not
// handed forced colour just because the process's own os.Stdout is a terminal.
func TestColorForceEnv_suppressedTTY(t *testing.T) {
	ttyOut := &fakeStream{"tty-stdout"}
	pipeOut := &fakeStream{"pipe-stdout"}

	tests := []struct {
		name      string
		stdout    io.Writer
		suppress  bool
		terminals []any
		want      bool
	}{
		{"terminal stdout, tty suppressed", ttyOut, true, []any{ttyOut, os.Stdout}, true},
		{"terminal stdout, tty not suppressed", ttyOut, false, []any{ttyOut, os.Stdout}, false},
		{"piped stdout, tty suppressed", pipeOut, true, nil, false},
		{"nil stdout, tty suppressed, process stdout is a terminal", nil, true, []any{os.Stdout}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearColorVars(t)
			stubStdoutTTY(t, false)
			stubIsTerminal(t, tt.terminals...)

			env := ColorForceEnv(spec.RunContext{Stdout: tt.stdout}, tt.suppress)
			if got := len(env) > 0; got != tt.want {
				t.Errorf("ColorForceEnv() forced = %v, want %v (env=%v)", got, tt.want, env)
			}
		})
	}

	t.Run("NO_COLOR wins over a suppressed tty", func(t *testing.T) {
		clearColorVars(t)
		stubStdoutTTY(t, false)
		stubIsTerminal(t, ttyOut)
		t.Setenv("NO_COLOR", "1")
		if env := ColorForceEnv(spec.RunContext{Stdout: ttyOut}, true); env != nil {
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

// stubIsTerminal pins the per-stream tty probe for one test: every stream
// listed reads as a terminal, everything else as a pipe.
func stubIsTerminal(t *testing.T, terminals ...any) {
	t.Helper()
	set := make(map[any]bool, len(terminals))
	for _, s := range terminals {
		set[s] = true
	}
	restore := isTerminal
	isTerminal = func(stream any) bool { return set[stream] }
	t.Cleanup(func() { isTerminal = restore })
}

// fakeStream is a comparable stand-in for a stdio stream; identity is all the
// stubbed probe needs.
type fakeStream struct{ name string }

func (*fakeStream) Write(p []byte) (int, error) { return len(p), nil }
func (*fakeStream) Read([]byte) (int, error)    { return 0, io.EOF }

// TestIsTerminal_probe pins the production probe: only an *os.File on a real
// terminal qualifies.
func TestIsTerminal_probe(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty.Open: %v", err)
	}
	t.Cleanup(func() { _ = tty.Close(); _ = ptmx.Close() })

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	tests := []struct {
		name   string
		stream any
		want   bool
	}{
		{"pty slave", tty, true},
		{"pty master", ptmx, true},
		{"pipe write end", pw, false},
		{"pipe read end", pr, false},
		{"bytes.Buffer", &bytes.Buffer{}, false},
		{"nil *os.File", (*os.File)(nil), false},
		{"untyped nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTerminal(tt.stream); got != tt.want {
				t.Errorf("isTerminal(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestWantContainerTTY walks UserInvoked × bridged-env × terminal/pipe stdout
// × terminal/pipe stdin, plus the nil-stream fallbacks and a non-*os.File
// writer. The stream probe is stubbed by identity so the matrix is
// independent of how `go test` happens to wire its own stdio.
func TestWantContainerTTY(t *testing.T) {
	ttyOut := &fakeStream{"tty-stdout"}
	pipeOut := &fakeStream{"pipe-stdout"}
	ttyIn := &fakeStream{"tty-stdin"}
	pipeIn := &fakeStream{"pipe-stdin"}
	bufOut := &bytes.Buffer{}

	tests := []struct {
		name        string
		userInvoked bool
		bridged     bool
		stdout      io.Writer
		stdin       io.Reader
		terminals   []any
		want        bool
	}{
		{"user, both terminals", true, false, ttyOut, ttyIn, []any{ttyOut, ttyIn}, true},
		{"user, piped stdout", true, false, pipeOut, ttyIn, []any{ttyIn}, false},
		{"user, piped stdin", true, false, ttyOut, pipeIn, []any{ttyOut}, false},
		{"user, both piped", true, false, pipeOut, pipeIn, nil, false},
		{"not user-invoked, both terminals", false, false, ttyOut, ttyIn, []any{ttyOut, ttyIn}, false},
		{"not user-invoked, both piped", false, false, pipeOut, pipeIn, nil, false},
		{"bridged, user, piped streams", true, true, pipeOut, pipeIn, nil, true},
		{"bridged, user, terminals", true, true, ttyOut, ttyIn, []any{ttyOut, ttyIn}, true},
		{"bridged, not user-invoked", false, true, pipeOut, pipeIn, nil, false},
		{"nil stdout, process stdout is a terminal", true, false, nil, ttyIn, []any{os.Stdout, ttyIn}, true},
		{"nil stdout, process stdout is a pipe", true, false, nil, ttyIn, []any{ttyIn}, false},
		{"nil stdin, process stdin is a terminal", true, false, ttyOut, nil, []any{ttyOut, os.Stdin}, true},
		{"nil stdin, process stdin is a pipe", true, false, ttyOut, nil, []any{ttyOut}, false},
		{"both nil, process streams are terminals", true, false, nil, nil, []any{os.Stdout, os.Stdin}, true},
		{"bytes.Buffer stdout is never a terminal", true, false, bufOut, ttyIn, []any{ttyIn}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearColorVars(t)
			stubStdoutTTY(t, false)
			if tt.bridged {
				t.Setenv("CLICOLOR_FORCE", "1")
				t.Setenv(bridgeclient.EnvBridgeStdinTTY, "1")
			}
			stubIsTerminal(t, tt.terminals...)

			rc := spec.RunContext{UserInvoked: tt.userInvoked, Stdout: tt.stdout, Stdin: tt.stdin}
			if got := WantContainerTTY(rc); got != tt.want {
				t.Errorf("WantContainerTTY() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWantContainerTTY_realStreams runs the predicate against genuine streams
// with the probe unstubbed, so the *os.File rule is exercised end to end.
func TestWantContainerTTY_realStreams(t *testing.T) {
	clearColorVars(t)
	stubStdoutTTY(t, false)

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty.Open: %v", err)
	}
	t.Cleanup(func() { _ = tty.Close(); _ = ptmx.Close() })
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	tests := []struct {
		name   string
		stdout io.Writer
		stdin  io.Reader
		want   bool
	}{
		{"pty on both ends", tty, tty, true},
		{"piped stdout", pw, tty, false},
		{"piped stdin", tty, pr, false},
		{"bytes.Buffer stdout", &bytes.Buffer{}, tty, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := spec.RunContext{UserInvoked: true, Stdout: tt.stdout, Stdin: tt.stdin}
			if got := WantContainerTTY(rc); got != tt.want {
				t.Errorf("WantContainerTTY() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBridgedTTYActive_readsProcessStdout pins that the bridge probe answers
// off the PROCESS stdout, not off rc.Stdout: the bridge shape is a property of
// how this dwe was launched, and repointing it at the RunContext would
// silently change which sessions get a fabricated PTY.
func TestBridgedTTYActive_readsProcessStdout(t *testing.T) {
	clearColorVars(t)
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv(bridgeclient.EnvBridgeStdinTTY, "1")

	rcStdout := &fakeStream{"rc-stdout"}
	stubIsTerminal(t, rcStdout) // rc.Stdout reads as a terminal throughout
	rc := spec.RunContext{Stdout: rcStdout, Stdin: rcStdout}

	stubStdoutTTY(t, true)
	if bridgedTTYActive(rc) {
		t.Error("a terminal process stdout must deactivate the bridge path even when rc.Stdout is a terminal")
	}

	stubStdoutTTY(t, false)
	if !bridgedTTYActive(rc) {
		t.Error("a piped process stdout must activate the bridge path regardless of rc.Stdout")
	}
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
	rc := spec.RunContext{Stdout: &out, Stderr: &out, Stdin: newBlockingReader(t)}
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

// blockingReader blocks until released (an os.Stdin with no data, minus the
// file). Production abandons the pump goroutine parked in Read; the test
// releases it via t.Cleanup after asserting teardown promptness, so the
// goroutine does not leak into the rest of the package run.
type blockingReader struct{ release chan struct{} }

func newBlockingReader(t *testing.T) *blockingReader {
	t.Helper()
	br := &blockingReader{release: make(chan struct{})}
	t.Cleanup(func() { close(br.release) })
	return br
}

func (b *blockingReader) Read([]byte) (int, error) {
	<-b.release
	return 0, io.EOF
}

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

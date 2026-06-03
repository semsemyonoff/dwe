package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// --- TTY auto-detect ---

// withTTYDetector swaps ttyDetector for the duration of the test and restores
// it via t.Cleanup. The fake routes per-arg so a test can simulate
// (stdinTTY, stdoutTTY) independently.
func withTTYDetector(t *testing.T, stdinTTY, stdoutTTY bool) {
	t.Helper()
	prev := ttyDetector
	ttyDetector = func(r io.Reader) bool {
		if r == os.Stdin {
			return stdinTTY
		}
		if r == os.Stdout {
			return stdoutTTY
		}
		return false
	}
	t.Cleanup(func() { ttyDetector = prev })
}

func TestStdioInteractive_matrix(t *testing.T) {
	cases := []struct {
		stdin, stdout, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("stdin=%v_stdout=%v", tc.stdin, tc.stdout)
		t.Run(name, func(t *testing.T) {
			withTTYDetector(t, tc.stdin, tc.stdout)
			if got := stdioInteractive(); got != tc.want {
				t.Errorf("stdioInteractive: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDockerExecTTYFlags(t *testing.T) {
	cases := []struct {
		stdin, stdout bool
		want          []string
	}{
		{true, true, []string{"-i", "-t"}},
		{true, false, []string{"-i"}},
		{false, true, []string{"-i"}},
		{false, false, []string{"-i"}},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("stdin=%v_stdout=%v", tc.stdin, tc.stdout)
		t.Run(name, func(t *testing.T) {
			withTTYDetector(t, tc.stdin, tc.stdout)
			got := dockerExecTTYFlags()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("dockerExecTTYFlags: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComposeRunTTYFlags(t *testing.T) {
	cases := []struct {
		stdin, stdout bool
		want          []string
	}{
		{true, true, []string{"-i"}},
		{true, false, []string{"-i", "-T"}},
		{false, true, []string{"-i", "-T"}},
		{false, false, []string{"-i", "-T"}},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("stdin=%v_stdout=%v", tc.stdin, tc.stdout)
		t.Run(name, func(t *testing.T) {
			withTTYDetector(t, tc.stdin, tc.stdout)
			got := composeRunTTYFlags()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("composeRunTTYFlags: got %v, want %v", got, tc.want)
			}
		})
	}
}

// --- shellCommandExitError / wrapExitError ---

// fakeExitError satisfies the *exec.ExitError shape just enough for errors.As
// to recognize it. We build it via a real exec.Cmd run so the embedded
// ProcessState carries a real exit code.
func mustExitError(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("could not produce *exec.ExitError: %v", err)
	}
	return exitErr
}

func TestWrapExitError_exitErrorWrappedWithCode(t *testing.T) {
	orig := mustExitError(t, 42)
	wrapped := wrapExitError(orig)

	var ec interface{ ExitCode() int }
	if !errors.As(wrapped, &ec) {
		t.Fatalf("wrapped error does not satisfy ExitCode() interface: %T", wrapped)
	}
	if ec.ExitCode() != 42 {
		t.Errorf("ExitCode: got %d, want 42", ec.ExitCode())
	}
	if u := errors.Unwrap(wrapped); u != orig {
		t.Errorf("Unwrap: got %v, want original *exec.ExitError", u)
	}
}

func TestWrapExitError_nonExitErrorPassthrough(t *testing.T) {
	plain := errors.New("docker daemon unreachable")
	got := wrapExitError(plain)
	if got != plain {
		t.Errorf("non-exit error must pass through unchanged; got %v", got)
	}
	var ec interface{ ExitCode() int }
	if errors.As(got, &ec) {
		t.Error("non-exit error must NOT satisfy ExitCode() interface")
	}
}

func TestWrapExitError_nilPassthrough(t *testing.T) {
	if got := wrapExitError(nil); got != nil {
		t.Errorf("nil must pass through; got %v", got)
	}
}

// --- dockerExecOneShot / composeRunOneShot: argv shape, silence, exit code ---

// withFakeRunInteractive captures every call to runInteractive and (optionally)
// returns a stubbed error. Restores the original via t.Cleanup.
type capturedCall struct {
	processEnv []string
	workDir    string
	name       string
	args       []string
}

func withFakeRunInteractive(t *testing.T, returnErr error) *[]capturedCall {
	t.Helper()
	prev := runInteractive
	calls := make([]capturedCall, 0, 1)
	runInteractive = func(processEnv []string, workDir, name string, args ...string) error {
		calls = append(calls, capturedCall{processEnv: processEnv, workDir: workDir, name: name, args: append([]string{}, args...)})
		return returnErr
	}
	t.Cleanup(func() { runInteractive = prev })
	return &calls
}

func TestDockerExecOneShot_argv_and_silence(t *testing.T) {
	withTTYDetector(t, false, false) // non-interactive → "-i"

	// Capture os.Stdout so we can assert the helper writes no banner.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prevStdout })

	calls := withFakeRunInteractive(t, nil)

	env := map[string]string{"FOO": "1", "BAR": "2"}
	if err := dockerExecOneShot("dwe-main", "bash", "deploy", "/app", env, "echo hi", []string{"DOCKER_HOST=tcp://x"}, "/usr/bin/docker"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_ = w.Close()
	out, _ := io.ReadAll(r)
	if len(out) > 0 {
		t.Errorf("dockerExecOneShot wrote to stdout: %q", string(out))
	}

	if len(*calls) != 1 {
		t.Fatalf("want 1 runInteractive call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.name != "/usr/bin/docker" {
		t.Errorf("name: got %q, want %q", c.name, "/usr/bin/docker")
	}
	if c.workDir != "" {
		t.Errorf("workDir: got %q, want empty (docker exec ignores compose cwd)", c.workDir)
	}
	wantArgs := []string{
		"exec", "-i",
		"-u", "deploy",
		"-w", "/app",
		"-e", "BAR=2",
		"-e", "FOO=1",
		"dwe-main", "bash", "-c", "echo hi",
	}
	if !reflect.DeepEqual(c.args, wantArgs) {
		t.Errorf("args mismatch:\n got: %v\nwant: %v", c.args, wantArgs)
	}
}

func TestDockerExecOneShot_interactive_addsTFlag(t *testing.T) {
	withTTYDetector(t, true, true)
	calls := withFakeRunInteractive(t, nil)
	if err := dockerExecOneShot("c", "bash", "", "", nil, "x", nil, "docker"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect "-i", "-t" at positions 1, 2.
	c := (*calls)[0]
	if len(c.args) < 3 || c.args[0] != "exec" || c.args[1] != "-i" || c.args[2] != "-t" {
		t.Errorf("interactive TTY flags missing: %v", c.args)
	}
}

func TestDockerExecOneShot_wrapsExitError(t *testing.T) {
	withTTYDetector(t, false, false)
	withFakeRunInteractive(t, mustExitError(t, 7))
	err := dockerExecOneShot("c", "bash", "", "", nil, "exit 7", nil, "docker")
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) {
		t.Fatalf("expected ExitCode-bearing error, got %T: %v", err, err)
	}
	if ec.ExitCode() != 7 {
		t.Errorf("ExitCode: got %d, want 7", ec.ExitCode())
	}
}

func TestComposeRunOneShot_argv_and_silence(t *testing.T) {
	withTTYDetector(t, false, false) // non-interactive → "-i","-T"

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prevStdout })

	calls := withFakeRunInteractive(t, nil)

	// Hand-build a minimal compose so we can assert on argv shape. NewCompose
	// adds project plumbing we don't want to mock — direct construction is fine
	// because composeRunOneShot only reads the exported fields.
	cmp := &docker.Compose{
		ProjectName: "dwe-app",
		Files:       []string{"compose.yaml", "compose.override.yaml"},
		GlobalArgs:  []string{"--progress", "quiet"},
		BaseDir:     "/tmp/dwe-base",
	}

	env := map[string]string{"AAA": "1"}
	if err := composeRunOneShot(cmp, "main", "sh", "1000", "/srv", env, "ls -la"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_ = w.Close()
	out, _ := io.ReadAll(r)
	if len(out) > 0 {
		t.Errorf("composeRunOneShot wrote to stdout: %q", string(out))
	}

	if len(*calls) != 1 {
		t.Fatalf("want 1 runInteractive call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.workDir != "/tmp/dwe-base" {
		t.Errorf("workDir: got %q, want %q", c.workDir, "/tmp/dwe-base")
	}
	wantArgs := []string{
		"compose",
		"-p", "dwe-app",
		"-f", "compose.yaml",
		"-f", "compose.override.yaml",
		"--progress", "quiet",
		"run", "--rm", "-i", "-T",
		"-u", "1000",
		"-w", "/srv",
		"-e", "AAA=1",
		"main", "sh", "-c", "ls -la",
	}
	if !reflect.DeepEqual(c.args, wantArgs) {
		t.Errorf("args mismatch:\n got: %v\nwant: %v", c.args, wantArgs)
	}
}

func TestComposeRunOneShot_wrapsExitError(t *testing.T) {
	withTTYDetector(t, false, false)
	withFakeRunInteractive(t, mustExitError(t, 13))
	cmp := &docker.Compose{ProjectName: "p"}
	err := composeRunOneShot(cmp, "main", "bash", "", "", nil, "exit 13")
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) {
		t.Fatalf("expected ExitCode-bearing error, got %T: %v", err, err)
	}
	if ec.ExitCode() != 13 {
		t.Errorf("ExitCode: got %d, want 13", ec.ExitCode())
	}
}

// --- runOneShotCommand: full mode × state matrix ---

// fakeStateFn returns a stub state func driven by table values.
type fakeOneShotInvocation struct {
	exec int // count of execOneShot calls
	run  int // count of runOneShot calls
	args struct {
		container, service, shell, u, workDir, command string
	}
}

func newFakeOneShotHandlers(inv *fakeOneShotInvocation) (oneShotExecFunc, oneShotRunFunc) {
	execFn := func(containerName, shell, u, workDir string, env map[string]string, command string) error {
		inv.exec++
		inv.args.container = containerName
		inv.args.shell = shell
		inv.args.u = u
		inv.args.workDir = workDir
		inv.args.command = command
		return nil
	}
	runFn := func(_ *docker.Compose, serviceName, shell, u, workDir string, env map[string]string, command string) error {
		inv.run++
		inv.args.service = serviceName
		inv.args.shell = shell
		inv.args.u = u
		inv.args.workDir = workDir
		inv.args.command = command
		return nil
	}
	return execFn, runFn
}

func makeOneShotConfig() *config.DweConfig {
	return &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"main": {
				Container: "main",
				CLI:       config.ServiceCLIConfig{Shell: "bash"},
			},
		},
	}
}

func TestRunOneShotCommand_modeStateMatrix(t *testing.T) {
	type stateOutcome struct {
		status string
		err    error
	}
	running := stateOutcome{status: "running"}
	notFound := stateOutcome{err: errContainerNotFound}
	stopped := stateOutcome{status: "exited"}

	cases := []struct {
		name             string
		mode             string
		state            stateOutcome
		wantExecCount    int
		wantRunCount     int
		wantErrSubstring string
	}{
		// exec mode
		{name: "exec/running", mode: "exec", state: running, wantExecCount: 1},
		{name: "exec/stopped", mode: "exec", state: stopped, wantErrSubstring: "not running"},
		{name: "exec/absent", mode: "exec", state: notFound, wantErrSubstring: "container not found"},

		// run mode (state probe is skipped)
		{name: "run/running", mode: "run", state: running, wantRunCount: 1},
		{name: "run/stopped", mode: "run", state: stopped, wantRunCount: 1},
		{name: "run/absent", mode: "run", state: notFound, wantRunCount: 1},

		// auto mode
		{name: "auto/running", mode: "auto", state: running, wantExecCount: 1},
		{name: "auto/absent", mode: "auto", state: notFound, wantRunCount: 1},
		{name: "auto/stopped", mode: "auto", state: stopped, wantErrSubstring: "start it first"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := &fakeOneShotInvocation{}
			execFn, runFn := newFakeOneShotHandlers(inv)
			stateFn := func(_ string) (string, error) {
				return tc.state.status, tc.state.err
			}
			cmp := &docker.Compose{ProjectName: "dwe-app"}
			flags := shellCLIFlags{mode: tc.mode, command: "echo hi"}
			err := runOneShotCommand(makeOneShotConfig(), cmp, "main", flags, stateFn, execFn, runFn)
			if tc.wantErrSubstring != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstring)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstring) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if inv.exec != tc.wantExecCount {
				t.Errorf("execOneShot calls: got %d, want %d", inv.exec, tc.wantExecCount)
			}
			if inv.run != tc.wantRunCount {
				t.Errorf("runOneShot calls: got %d, want %d", inv.run, tc.wantRunCount)
			}
			if inv.args.command != "echo hi" {
				t.Errorf("command threaded through: got %q, want %q", inv.args.command, "echo hi")
			}
		})
	}
}

func TestRunOneShotCommand_unknownService(t *testing.T) {
	inv := &fakeOneShotInvocation{}
	execFn, runFn := newFakeOneShotHandlers(inv)
	stateFn := func(string) (string, error) { return "", nil }
	err := runOneShotCommand(makeOneShotConfig(), &docker.Compose{}, "missing", shellCLIFlags{command: "x"}, stateFn, execFn, runFn)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want service-not-found error, got %v", err)
	}
}

func TestRunOneShotCommand_invalidMode(t *testing.T) {
	inv := &fakeOneShotInvocation{}
	execFn, runFn := newFakeOneShotHandlers(inv)
	stateFn := func(string) (string, error) { return "", nil }
	err := runOneShotCommand(makeOneShotConfig(), &docker.Compose{}, "main", shellCLIFlags{mode: "bogus", command: "x"}, stateFn, execFn, runFn)
	if err == nil || !strings.Contains(err.Error(), "invalid cli.mode") {
		t.Errorf("want invalid-mode error, got %v", err)
	}
}

func TestRunOneShotCommand_statePropagatesArbitraryError(t *testing.T) {
	inv := &fakeOneShotInvocation{}
	execFn, runFn := newFakeOneShotHandlers(inv)
	stateFn := func(string) (string, error) { return "", errors.New("docker daemon unreachable") }
	err := runOneShotCommand(makeOneShotConfig(), &docker.Compose{ProjectName: "p"}, "main", shellCLIFlags{mode: "auto", command: "x"}, stateFn, execFn, runFn)
	if err == nil || !strings.Contains(err.Error(), "docker daemon unreachable") {
		t.Errorf("want propagated state error, got %v", err)
	}
}

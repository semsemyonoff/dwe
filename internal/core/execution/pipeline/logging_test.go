package pipeline

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/liveui"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

func TestOpenPipelineLog_CreatesDevboxLogsDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	_, logWriter, _, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", true)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if logWriter == nil {
		t.Errorf("expected logWriter to be non-nil")
	}

	expectedPath := filepath.Join(tmpDir, ".dwe", "logs", "deploy.log")
	if logPath != expectedPath {
		t.Errorf("expected logPath=%q, got %q", expectedPath, logPath)
	}

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("expected log file to exist at %s", logPath)
	}

	legacyLogsDir := filepath.Join(tmpDir, "logs")
	if _, err := os.Stat(legacyLogsDir); !os.IsNotExist(err) {
		t.Errorf("expected legacy logs/ directory to not exist, but it does")
	}
}

func TestOpenSubStepLog_Disabled(t *testing.T) {
	w, path, err := OpenSubStepLog(t.TempDir(), "deploy", "g", "a", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil || path != "" {
		t.Errorf("disabled: want (nil, \"\"), got (%v, %q)", w, path)
	}
}

func TestOpenSubStepLog_CreatesPathAndSanitises(t *testing.T) {
	tmp := t.TempDir()
	// Pipeline / group / sub names contain unsafe characters that must be
	// replaced by sanitizeForFS — slashes, spaces, colons.
	w, path, err := OpenSubStepLog(tmp, "dep/loy", "my group:1", "../sub a", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil {
		t.Fatal("expected non-nil writer")
	}
	defer func() { _ = w.Close() }()

	wantDir := filepath.Join(tmp, ".dwe", "logs", "parallel", "dep_loy", "my_group_1")
	wantPath := filepath.Join(wantDir, "_sub_a.log")
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
	// Ensure the sanitised name did not escape the parallel root.
	rel, err := filepath.Rel(filepath.Join(tmp, ".dwe", "logs", "parallel"), path)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("sanitised path escaped parallel root: %q", rel)
	}
}

func TestSanitizeForFS(t *testing.T) {
	cases := map[string]string{
		"":            "_",
		"plain":       "plain",
		"with space":  "with_space",
		"a/b":         "a_b",
		"...":         "_",
		"../etc":      "_etc",
		"keep.dots":   "keep.dots",
		"dash-and_us": "dash-and_us",
	}
	for in, want := range cases {
		if got := sanitizeForFS(in); got != want {
			t.Errorf("sanitizeForFS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChildIO_Parallel_NoStdoutNoStderr(t *testing.T) {
	prev := stdoutIsTTY
	stdoutIsTTY = func() bool { return false }
	defer func() { stdoutIsTTY = prev }()

	var buf bytes.Buffer
	stdout, stderr, cleanup := childIO(&buf, true)
	defer cleanup()
	if stdout == os.Stdout || stderr == os.Stderr {
		t.Error("parallel mode must not return os.Stdout / os.Stderr")
	}
	// Parallel+non-TTY branch returns the writer unchanged — executeStepBody
	// wraps it in liveui.ANSIOnlyStripper → lineTee; the lineTee callback writes
	// ANSI-clean frames to the sub-step log.
	if _, err := stdout.Write([]byte("\x1b[31mred\x1b[0m\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), "red") {
		t.Errorf("expected text preserved, got %q", buf.String())
	}
}

// TestChildIO_Parallel_TTY_NoPTY verifies that parallel mode never allocates
// a pty even when stdout is a TTY. Granting the child a pty while stdin is
// the empty reader causes `docker compose exec/run` to fail with
// "cannot attach stdin to a TTY-enabled container because stdin is not a
// terminal" — see docs/plans/completed/2026-05-19-live-pipeline-progress.md.
func TestChildIO_Parallel_TTY_NoPTY(t *testing.T) {
	prev := stdoutIsTTY
	stdoutIsTTY = func() bool { return true }
	defer func() { stdoutIsTTY = prev }()

	logBuf := &syncBuf{}
	stdout, stderr, cleanup := childIO(logBuf, true)
	defer cleanup()

	// The writer returned must be the stepWriter itself, not a pty slave.
	if _, ok := stdout.(*os.File); ok {
		t.Fatalf("parallel mode must not return a *os.File pty slave; got %T", stdout)
	}
	if stdout != stderr {
		t.Error("parallel mode must return the same writer for stdout and stderr")
	}
	if stdout == os.Stdout || stderr == os.Stderr {
		t.Error("parallel mode must not return os.Stdout/os.Stderr — LiveBlock owns the terminal")
	}
}

func TestChildIO_NilStepWriter_FallsBackToOsStdio(t *testing.T) {
	// Task 6: childIO with stepWriter == nil falls back to os.Stdout / os.Stderr
	// passthrough so ad-hoc external callers (`dwe deploy run STEP`) still
	// inherit the real terminal fd. Replaces the old parallel-nil panic which
	// is no longer reachable: parallel-mode callers always supply a tee.
	stdout, stderr, cleanup := childIO(nil, false)
	defer cleanup()
	if stdout != os.Stdout || stderr != os.Stderr {
		t.Errorf("nil stepWriter must yield os.Stdout/os.Stderr passthrough")
	}
}

// TestParallelGroup_PerSubStepLogRoutesOutput exercises the executor's
// parallel branch end-to-end: each sub-step's stdout must reach its dedicated
// log file, the global pipeline log, and Reporter.SubStepOutput; nothing must
// reach a sibling sub-step's log file or os.Stdout.
func TestParallelGroup_PerSubStepLogRoutesOutput(t *testing.T) {
	tmp := t.TempDir()

	// Capture the writer that OpenPipelineLog would normally hand back. A
	// real *os.File would be safe for concurrent writes; a bytes.Buffer is
	// not, so guard it with a mutex.
	globalLog := &syncBuf{}

	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: "echo alpha-out"},
		{Name: "beta", Type: "shell", Cmd: "echo beta-out"},
	})

	opts := RunOptions{
		Steps:       []ResolvedStep{group},
		Reporter:    rep,
		Name:        "deploy",
		Config:      &config.DweConfig{Raw: map[string]any{}},
		WorkDir:     tmp,
		LogWriter:   globalLog,
		Recorder:    &mockRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alphaPath := filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "alpha.log")
	betaPath := filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "beta.log")

	alpha, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatalf("read alpha log: %v", err)
	}
	beta, err := os.ReadFile(betaPath)
	if err != nil {
		t.Fatalf("read beta log: %v", err)
	}

	if !strings.Contains(string(alpha), "alpha-out") {
		t.Errorf("alpha.log missing 'alpha-out': %q", alpha)
	}
	if strings.Contains(string(alpha), "beta-out") {
		t.Errorf("alpha.log leaked sibling output: %q", alpha)
	}
	if !strings.Contains(string(beta), "beta-out") {
		t.Errorf("beta.log missing 'beta-out': %q", beta)
	}
	if strings.Contains(string(beta), "alpha-out") {
		t.Errorf("beta.log leaked sibling output: %q", beta)
	}

	// The global pipeline log receives parallel sub-step output via
	// PlainReporter.StepOutput's writeLog side-channel (not via a direct fan-out
	// in joinWriters); content assertions on globalLog belong in plain_test.go.
	_ = globalLog

	// Reporter.SubStepOutput was called with each sub-step's line.
	sawAlpha, sawBeta := false, false
	for _, e := range rep.events {
		if e.kind != "StepOutput" {
			continue
		}
		if e.stepAddr == "p/alpha" && strings.Contains(e.reason, "alpha-out") {
			sawAlpha = true
		}
		if e.stepAddr == "p/beta" && strings.Contains(e.reason, "beta-out") {
			sawBeta = true
		}
	}
	if !sawAlpha || !sawBeta {
		t.Errorf("missing SubStepOutput events: sawAlpha=%v sawBeta=%v events=%v", sawAlpha, sawBeta, rep.events)
	}
}

// TestParallelGroup_NoOutputWithLoggingDisabled verifies that when the
// pipeline log is disabled (opts.LogWriter == nil), no per-sub-step log file
// is created, but SubStepOutput events still fire so the reporter can render.
func TestParallelGroup_DisabledLog_NoFiles_StillStreamsToReporter(t *testing.T) {
	tmp := t.TempDir()

	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: "echo a"},
	})

	opts := RunOptions{
		Steps:       []ResolvedStep{group},
		Reporter:    rep,
		Name:        "deploy",
		Config:      &config.DweConfig{Raw: map[string]any{}},
		WorkDir:     tmp,
		LogWriter:   nil,
		Recorder:    &mockRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No parallel log directory should have been created.
	if _, err := os.Stat(filepath.Join(tmp, ".dwe", "logs", "parallel")); !os.IsNotExist(err) {
		t.Errorf("expected no parallel/ log dir when logging disabled, got err=%v", err)
	}

	// SubStepOutput still fires.
	saw := false
	for _, e := range rep.events {
		if e.kind == "StepOutput" && strings.Contains(e.reason, "a") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected SubStepOutput event when log disabled, events=%v", rep.events)
	}
}

// TestExecBuiltinAction_Parallel_NoStdoutWrite verifies that a builtin running
// in parallel mode (actx.Parallel=true) writes only to actx.StepWriter and
// never directly to os.Stdout — required for non-TTY buffered reporter modes.
func TestExecBuiltinAction_Parallel_NoStdoutWrite(t *testing.T) {
	var buf bytes.Buffer
	tee := liveui.NewLineTee(func(string, bool) {})
	stepWriter := &liveui.ANSIOnlyStripper{W: liveui.JoinWriters(&buf, tee)}

	// Capture and discard os.Stdout writes to assert nothing leaks.
	origStdout := os.Stdout
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = wPipe
	defer func() { os.Stdout = origStdout; _ = rPipe.Close(); _ = wPipe.Close() }()

	cfg := &config.DweConfig{Raw: map[string]any{}}
	actx := ActionContext{
		WorkDir:    t.TempDir(),
		Cfg:        cfg,
		StepWriter: stepWriter,
		Parallel:   true,
	}
	action := config.Action{Type: "builtin", Cmd: "message", With: map[string]any{"level": "info", "text": "hi"}}
	if err := ExecAction(t.Context(), action, actx); err != nil {
		t.Fatalf("ExecAction: %v", err)
	}
	if !strings.Contains(buf.String(), "hi") {
		t.Errorf("builtin output did not reach StepWriter: %q", buf.String())
	}
	// Drain the captured os.Stdout pipe non-blockingly.
	_ = wPipe.Close()
	captured, _ := io.ReadAll(rPipe)
	if len(captured) > 0 {
		t.Errorf("parallel builtin leaked %d bytes to os.Stdout: %q", len(captured), captured)
	}
	os.Stdout = origStdout
}

// TestSequentialStep_BypassesStepOutput pins the new contract: sequential
// step output goes directly to os.Stdout (with the LiveLine paused) and to
// the on-disk log via a MultiWriter; it MUST NOT flow through
// Reporter.StepOutput, which is reserved for parallel sub-step block-row
// updates. See docs/plans/completed/2026-05-19-live-pipeline-progress.md
// for the design rationale.
func TestSequentialStep_BypassesStepOutput(t *testing.T) {
	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{
			Name: "echo", Type: "shell", Cmd: "printf 'alpha\\nbeta\\n'",
		}},
	}
	if err := Run(steps, rep, "deploy", &config.DweConfig{Raw: map[string]any{}}, nil, t.TempDir(), nil, true, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, e := range rep.events {
		if e.kind == "StepOutput" {
			t.Errorf("sequential steps must not emit StepOutput events; got %+v", e)
		}
	}
}

// TestSequentialStep_LogTeeCapturesOutput pins that sequential child stdout
// reaches the on-disk pipeline log via the MultiWriter tee in childIO. The
// global log is the only persistent record once Suspend/Resume hands the
// terminal to the child.
func TestSequentialStep_LogTeeCapturesOutput(t *testing.T) {
	rep := &mockReporter{}
	var logBuf syncBuf
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{
			Name: "echo", Type: "shell", Cmd: "printf 'unique-line\\n'",
		}},
	}
	opts := RunOptions{
		Steps:     steps,
		Reporter:  rep,
		Name:      "deploy",
		Config:    &config.DweConfig{Raw: map[string]any{}},
		WorkDir:   t.TempDir(),
		LogWriter: &logBuf,
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("RunWithOptions: %v", err)
	}
	if got := strings.Count(logBuf.String(), "unique-line"); got != 1 {
		t.Errorf("sequential child output must reach the log exactly once; got %d in:\n%s", got, logBuf.String())
	}
}

// TestSequentialStep_SuspendsAndResumesLive verifies the executor pauses
// the LiveLine footer around each sequential step body and resumes after.
// The previous design (Task 6 of the live-pipeline plan) tried to keep the
// footer visible by routing child output through StepOutput; this broke
// docker compose's interactive UI and stripped command colors.
func TestSequentialStep_SuspendsAndResumesLive(t *testing.T) {
	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	steps := buildResolvedSteps(phase, []config.DeployStep{
		noopStep("a"), noopStep("b"), noopStep("c"),
	})
	if err := Run(steps, rep, "deploy", &config.DweConfig{Raw: map[string]any{}}, nil, t.TempDir(), nil, true, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.suspendCalls != 3 || rep.resumeCalls != 3 {
		t.Errorf("expected one suspend/resume per sequential step; got suspend=%d resume=%d",
			rep.suspendCalls, rep.resumeCalls)
	}
}

// suppress unused-import warning for condition when no tests reference it.
var _ = condition.TypeShell

// syncBuf is a concurrency-safe bytes.Buffer wrapper for tests that share a
// global log writer across parallel sub-steps.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestOpenPipelineLog_ScreenDoesNotTeeToLog asserts the new split-channel
// contract: ANSI written to the screen writer never reaches the log file
// (because the old MultiWriter tee was removed).
func TestOpenPipelineLog_ScreenDoesNotTeeToLog(t *testing.T) {
	tmpDir := t.TempDir()
	screen, logFile, _, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", true)
	if err != nil {
		t.Fatalf("OpenPipelineLog: %v", err)
	}
	defer cleanup()

	// Write ANSI through the screen writer. The log file (a separate writer)
	// must remain empty because screen no longer tees.
	if _, werr := screen.Writer().Write([]byte("\x1b[31mhello\x1b[0m\n")); werr != nil {
		t.Fatalf("screen write: %v", werr)
	}
	// Ensure file content is observable by reading from disk.
	if f, ok := logFile.(*os.File); ok {
		_ = f.Sync()
	}
	data, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	if len(data) != 0 {
		t.Errorf("expected log file to be empty after screen write, got %q", data)
	}
}

// TestPlainReporter_StatusLineReachesLogFile verifies that PlainReporter
// side-writes every emit() to the dedicated log file (no fan-out duplication;
// each line lands exactly once).
func TestPlainReporter_StatusLineReachesLogFile(t *testing.T) {
	var screen, logBuf bytes.Buffer
	rep := NewPlainReporter(render.NewWriter(&screen), &logBuf, nil)
	rep.now = func() time.Time { return fixedTime }
	rep.StartPipeline("deploy", 1)
	rep.EnterPhase("deploy", config.DeployPhase{Name: "deploy", Description: "Deploy"})

	got := logBuf.String()
	if !strings.Contains(got, "Phase: deploy") {
		t.Errorf("expected log file to contain phase line, got %q", got)
	}
	// Count occurrences: exactly one line per emit.
	if n := strings.Count(got, "Phase: deploy"); n != 1 {
		t.Errorf("expected 1 occurrence of phase line in log, got %d (content=%q)", n, got)
	}
}

func TestOpenPipelineLog_DisabledReturnsNil(t *testing.T) {
	tmpDir := t.TempDir()

	w, logWriter, termOut, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", false)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w == nil {
		t.Errorf("expected writer to be non-nil when logging disabled")
	}

	if logWriter != nil {
		t.Errorf("expected logWriter to be nil when disabled")
	}

	if logPath != "" {
		t.Errorf("expected logPath to be empty when disabled, got %q", logPath)
	}

	if termOut == nil {
		t.Errorf("expected termOut to be non-nil even when disabled (io.Discard or os.Stdout)")
	}

	devboxLogsDir := filepath.Join(tmpDir, ".dwe", "logs")
	if _, err := os.Stat(devboxLogsDir); !os.IsNotExist(err) {
		t.Errorf("expected .dwe/logs directory to not exist when logging disabled")
	}
}

package pipeline

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// pinNonTTY forces the sequential branch of childIO down its non-PTY path so
// these tests observe the stepWriter directly instead of a pty round-trip.
func pinNonTTY(t *testing.T) {
	t.Helper()
	prev := stdoutIsTTY
	stdoutIsTTY = func() bool { return false }
	t.Cleanup(func() { stdoutIsTTY = prev })
}

// logLines returns the non-empty lines of a captured log.
func logLines(s string) []string {
	out := []string{}
	for l := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestSequentialStep_CRProgressRun_CollapsesToOneLine pins Part A's headline
// behaviour on the sequential path: a child redrawing a progress line with
// bare `\r` contributes one line to the pipeline log — the frame the terminal
// was left showing — not one line per redraw frame.
func TestSequentialStep_CRProgressRun_CollapsesToOneLine(t *testing.T) {
	pinNonTTY(t)

	rep := &mockReporter{}
	var logBuf syncBuf
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{
			Name: "clone", Type: "shell", Cmd: `printf 'pct-10\rpct-50\rpct-100\n'`,
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

	got := logLines(logBuf.String())
	want := []string{"pct-100"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("log must hold one committed line, got %q", got)
	}
}

// TestSequentialStep_TrailingCRFrame_SurvivesViaFlush covers the other half of
// the frame writer's contract: output that ends on a bare `\r` with no
// committing newline still reaches the global log, because the executor's
// pre-existing flushTee discipline now also drives FrameLogWriter.Flush.
func TestSequentialStep_TrailingCRFrame_SurvivesViaFlush(t *testing.T) {
	pinNonTTY(t)

	rep := &mockReporter{}
	var logBuf syncBuf
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{
			Name: "clone", Type: "shell", Cmd: `printf 'tail-10\rtail-90\r'`,
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

	got := logLines(logBuf.String())
	if len(got) != 1 || got[0] != "tail-90" {
		t.Errorf("`\\r`-terminated tail must reach the log exactly once as the last frame, got %q", got)
	}
}

// TestSequentialStep_PlainLines_Unchanged guards against the frame writer
// changing anything for ordinary `\n`-terminated output.
func TestSequentialStep_PlainLines_Unchanged(t *testing.T) {
	pinNonTTY(t)

	rep := &mockReporter{}
	var logBuf syncBuf
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{
			Name: "echo", Type: "shell", Cmd: `printf 'alpha\nbeta\ngamma\n'`,
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

	got := logLines(logBuf.String())
	want := []string{"alpha", "beta", "gamma"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("plain lines must be recorded verbatim; got %q want %q", got, want)
	}
}

// TestParallelSubStepLog_OnlyCommittedFrames pins the parallel half of the
// change: the per-sub-step file records committed lines only, while
// Reporter.StepOutput still observes every non-final redraw frame — that
// observation is what feeds entry.inProgress and, through commitTrailingTail,
// the global pipeline log.
func TestParallelSubStepLog_OnlyCommittedFrames(t *testing.T) {
	tmp := t.TempDir()
	globalLog := &syncBuf{}

	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: `printf 'frame-10\rframe-99\n'`},
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
		t.Fatalf("RunWithOptions: %v", err)
	}

	alphaPath := filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "alpha.log")
	alpha, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatalf("read alpha log: %v", err)
	}
	got := logLines(string(alpha))
	if len(got) != 1 || got[0] != "frame-99" {
		t.Errorf("per-sub-step log must hold committed lines only, got %q", got)
	}

	// The reporter still saw the non-final frame; that path is unchanged.
	sawNonFinal := false
	for _, e := range rep.events {
		if e.kind == "StepOutput" && !e.final && strings.Contains(e.reason, "frame-10") {
			sawNonFinal = true
		}
	}
	if !sawNonFinal {
		t.Errorf("Reporter.StepOutput must still receive non-final frames; events=%v", rep.events)
	}
}

// TestParallelSubStep_TrailingCRFrame_ReachesGlobalLog documents what the
// final gate costs and why it is affordable here: the `\r`-terminated tail is
// dropped from .dwe/logs/parallel/**, but the same frame reaches
// Reporter.StepOutput → commitTrailingTail → the global pipeline log, the
// second sink usercommands/runtime/runners/workflow/parallel.go does not have.
func TestParallelSubStep_TrailingCRFrame_ReachesGlobalLog(t *testing.T) {
	tmp := t.TempDir()

	scr := &bytes.Buffer{}
	globalLog := &syncBuf{}
	rep := NewPlainReporter(render.NewWriter(scr), globalLog, io.Discard)
	defer rep.Close()

	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: `printf 'sub-tail\r'`},
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
		t.Fatalf("RunWithOptions: %v", err)
	}

	if !strings.Contains(globalLog.String(), "sub-tail") {
		t.Errorf("global pipeline log must keep the `\\r`-terminated tail:\n%s", globalLog.String())
	}
	alphaPath := filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "alpha.log")
	alpha, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatalf("read alpha log: %v", err)
	}
	if strings.Contains(string(alpha), "sub-tail") {
		t.Errorf("per-sub-step log records committed lines only; got %q", alpha)
	}
}

// TestSequentialStep_ChildOutputPrecedesFinishLine is the ordering test. The
// shared .dwe/logs/<pipeline>.log now has two writers: the buffered
// FrameLogWriter carrying child output and PlainReporter's own unbuffered
// LogSanitizer carrying status lines. Only the flushTee-before-finish
// discipline keeps them in sequence, so assert the sequence itself rather than
// mere presence.
func TestSequentialStep_ChildOutputPrecedesFinishLine(t *testing.T) {
	pinNonTTY(t)

	scr := &bytes.Buffer{}
	logBuf := &syncBuf{}
	rep := NewPlainReporter(render.NewWriter(scr), logBuf, io.Discard)
	defer rep.Close()

	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{
			Name: "echo", Type: "shell", Cmd: `printf 'first\nlast-10\rlast-99\r'`,
		}},
		{Phase: phase, Step: config.DeployStep{Name: "after", Type: "shell", Cmd: "true"}},
	}
	opts := RunOptions{
		Steps:     steps,
		Reporter:  rep,
		Name:      "deploy",
		Config:    &config.DweConfig{Raw: map[string]any{}},
		WorkDir:   t.TempDir(),
		LogWriter: logBuf,
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("RunWithOptions: %v", err)
	}

	log := logBuf.String()
	idxFirst := strings.Index(log, "first")
	idxLast := strings.Index(log, "last-99")
	idxDone := strings.Index(log, "Done: p/echo")
	if idxFirst < 0 || idxLast < 0 || idxDone < 0 {
		t.Fatalf("log missing expected content (first=%d last=%d done=%d):\n%s", idxFirst, idxLast, idxDone, log)
	}
	if idxFirst >= idxLast || idxLast >= idxDone {
		t.Errorf("child output must precede the step's finish line; first=%d last=%d done=%d in:\n%s",
			idxFirst, idxLast, idxDone, log)
	}
	if strings.Contains(log, "last-10") {
		t.Errorf("redraw frame must not reach the log:\n%s", log)
	}
}

package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/shared/liveui"
	"devbox-cli/internal/shared/tpl"
)

// liveLineCapture wraps the test factory so each invocation records the
// constructed LiveLine. Used by tests that need to inspect block-row state
// after the parallel group finishes.
type liveLineCapture struct {
	lines []*liveui.LiveLine
	buf   bytes.Buffer
}

func installLiveLineCapture(t *testing.T) (*liveLineCapture, func()) {
	t.Helper()
	prevTTY := workflowParallelStdoutIsTTY
	prevFactory := newWorkflowParallelLiveLine
	cap := &liveLineCapture{}
	workflowParallelStdoutIsTTY = func() bool { return true }
	newWorkflowParallelLiveLine = func(_ string) *liveui.LiveLine {
		ll := liveui.NewLiveLine(&cap.buf, &cap.buf, true)
		ll.SetTestHooks(true, func() int { return 80 })
		cap.lines = append(cap.lines, ll)
		return ll
	}
	cleanup := func() {
		workflowParallelStdoutIsTTY = prevTTY
		newWorkflowParallelLiveLine = prevFactory
	}
	return cap, cleanup
}

func TestWorkflowRunner_Parallel_LiveLine_AllDone(t *testing.T) {
	dir := t.TempDir()
	a := makeShellLeaf("liveui.a", `true`)
	b := makeShellLeaf("liveui.b", `true`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "liveui.wf",
		Group:     "liveui",
		LocalName: "wf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "liveui.a"},
					{Command: "liveui.b"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b)

	cap, cleanup := installLiveLineCapture(t)
	defer cleanup()

	_, _, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(cap.lines) != 1 {
		t.Fatalf("expected one LiveLine constructed; got %d", len(cap.lines))
	}
	ll := cap.lines[0]
	if !ll.IsStopped() {
		t.Errorf("expected LiveLine stopped after group")
	}
	// Block-row state is cleared on EndBlock; verify the LiveLine moved
	// through the block-mode phase by checking the buffer for cursor ANSI
	// (cursor-up after each redraw).
	if !strings.Contains(cap.buf.String(), "\x1b[") {
		t.Errorf("expected ANSI cursor codes in LiveLine output; got %q", cap.buf.String())
	}
}

func TestWorkflowRunner_Parallel_LiveLine_Failed(t *testing.T) {
	dir := t.TempDir()
	ok := makeShellLeaf("liveui.ok", `true`)
	fail := makeShellLeaf("liveui.fail", `exit 1`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "liveui.failwf",
		Group:     "liveui",
		LocalName: "failwf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffFalse,
				Steps: []WorkflowStep{
					{Command: "liveui.ok"},
					{Command: "liveui.fail"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, ok, fail)

	cap, cleanup := installLiveLineCapture(t)
	defer cleanup()

	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err == nil {
		t.Fatal("expected error from failing sub-step")
	}
	// TTY mode suppresses per-sub-step status lines (already shown in live
	// block); only the summary footer and the failure-output dump remain.
	if strings.Contains(errOut, "✗ [2/2] Failed: liveui.fail") {
		t.Errorf("TTY mode should suppress per-sub-step Failed line; got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "parallel: liveui.failwf") {
		t.Errorf("expected summary footer naming the workflow; got:\n%s", errOut)
	}
	if !strings.Contains(errOut, liveui.IconFailed) {
		t.Errorf("expected ✗ glyph in summary footer; got:\n%s", errOut)
	}
	if len(cap.lines) != 1 {
		t.Fatalf("expected one LiveLine; got %d", len(cap.lines))
	}
	if !cap.lines[0].IsStopped() {
		t.Errorf("expected LiveLine stopped")
	}
}

func TestWorkflowRunner_Parallel_LiveLine_Skipped(t *testing.T) {
	dir := t.TempDir()
	a := makeShellLeaf("liveui.runa", `true`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "liveui.skipwf",
		Group:     "liveui",
		LocalName: "skipwf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "liveui.runa", When: "false"},
					{Command: "liveui.runa"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a)

	cap, cleanup := installLiveLineCapture(t)
	defer cleanup()

	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// TTY mode suppresses per-sub-step status lines (skipped sub-steps are
	// shown finalised in the live block via SetBlockRowFinal). Only the
	// summary footer remains.
	if strings.Contains(errOut, "◎ [1/2] Skipped: liveui.runa") {
		t.Errorf("TTY mode should suppress per-sub-step Skipped line; got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "parallel: liveui.skipwf") {
		t.Errorf("expected summary footer; got:\n%s", errOut)
	}
	if !strings.Contains(errOut, liveui.IconDone) {
		t.Errorf("expected ✓ glyph in summary footer (no failures); got:\n%s", errOut)
	}
	if len(cap.lines) != 1 {
		t.Fatalf("expected one LiveLine; got %d", len(cap.lines))
	}
}

// TestWorkflowRunner_Parallel_ContextCancel_StopsCleanly mimics the SIGINT
// path: signal.NotifyContext in internal/cli/command/ cancels the parent context;
// errgroup propagates that into running sub-steps via exec.CommandContext.
// This test cancels mid-block and verifies the runner returns promptly with
// the LiveLine stopped — no leaked goroutines.
func TestWorkflowRunner_Parallel_ContextCancel_StopsCleanly(t *testing.T) {
	dir := t.TempDir()
	slow := makeShellLeaf("cancel.slow", `exec sleep 30`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "cancel.wf",
		Group:     "cancel",
		LocalName: "wf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "cancel.slow"},
					{Command: "cancel.slow"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, slow)

	cap, cleanup := installLiveLineCapture(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	var outBuf, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:         wf,
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{Params: map[string]any{}},
		Registry:    reg,
		ProjectRoot: dir,
		Stdout:      &outBuf,
		Stderr:      &errBuf,
	}
	start := time.Now()
	err := (&WorkflowRunner{}).Run(ctx, rc)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error after cancel")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "signal: killed") && !strings.Contains(err.Error(), "context canceled") {
		// Sub-step shell may surface either context.Canceled or
		// "signal: killed" wrapping depending on the cmd.Cancel SIGTERM path.
		t.Logf("note: error path was %q (acceptable)", err.Error())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancel did not stop the group promptly (%v)", elapsed)
	}
	if len(cap.lines) != 1 {
		t.Fatalf("expected one LiveLine; got %d", len(cap.lines))
	}
	if !cap.lines[0].IsStopped() {
		t.Errorf("expected LiveLine stopped after cancel")
	}
}

// TestWorkflowRunner_Parallel_LiveLine_CarriageReturnProgress is a regression
// test for the case where a sub-step's child writes a progress bar via
// carriage-return frames (curl, wget, docker pull, …). The lineTee callback
// must refresh the block row on every frame (final + non-final) so the latest
// progress is visible — not only newline-terminated lines. The buffer/log
// only commit on final frames so transient progress does not bloat logs.
func TestWorkflowRunner_Parallel_LiveLine_CarriageReturnProgress(t *testing.T) {
	dir := t.TempDir()
	// printf emits "header\n" (one final frame) then 3 \r-frames; the LAST
	// non-final frame must drive the live block row. Failure of the sub-step
	// after the progress is so the captured per-sub-step dump can be inspected.
	progressLeaf := makeShellLeaf("prog.run",
		`printf 'header\n'; printf 'p10\r'; printf 'p55\r'; printf 'p99\r'; exit 1`)
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "prog.wf",
		Group:     "prog",
		LocalName: "wf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffFalse,
				Steps: []WorkflowStep{
					{Command: "prog.run"},
					{Command: "prog.run"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, progressLeaf)

	cap, cleanup := installLiveLineCapture(t)
	defer cleanup()

	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err == nil {
		t.Fatal("expected sub-steps to fail; got nil")
	}
	// The latest \r-frame ("p99") must have reached the rendered block row.
	if !strings.Contains(cap.buf.String(), "p99") {
		t.Errorf("expected latest progress frame 'p99' in LiveLine output; got:\n%s", cap.buf.String())
	}
	// Transient progress frames must NOT pollute the failure dump — only the
	// newline-terminated "header" line should appear between separator bars.
	if !strings.Contains(errOut, "header") {
		t.Errorf("expected 'header' (final frame) in dump; got:\n%s", errOut)
	}
	for _, transient := range []string{"p10", "p55", "p99"} {
		if strings.Contains(errOut, transient) {
			t.Errorf("transient frame %q must not appear in failure dump; got:\n%s", transient, errOut)
		}
	}
}

// TestWorkflowRunner_Parallel_LiveLine_AllDone_SuppressesPerStepDone verifies
// that in TTY mode the workflow does NOT re-print "✓ [i/N] Done: ..." for
// every sub-step (those rows are already visible in the live block); a single
// green-✓ summary footer is printed instead.
func TestWorkflowRunner_Parallel_LiveLine_AllDone_SuppressesPerStepDone(t *testing.T) {
	dir := t.TempDir()
	a := makeShellLeaf("sumok.a", `true`)
	b := makeShellLeaf("sumok.b", `true`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "sumok.wf",
		Group:     "sumok",
		LocalName: "wf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "sumok.a"},
					{Command: "sumok.b"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b)

	_, cleanup := installLiveLineCapture(t)
	defer cleanup()

	_, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(errOut, "Done: sumok.a") || strings.Contains(errOut, "Done: sumok.b") {
		t.Errorf("TTY mode must not duplicate per-sub-step Done lines; got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "parallel: sumok.wf") {
		t.Errorf("expected summary footer 'parallel: sumok.wf'; got:\n%s", errOut)
	}
	if !strings.Contains(errOut, liveui.IconDone) {
		t.Errorf("expected ✓ glyph in summary footer; got:\n%s", errOut)
	}
}

func TestWorkflowRunner_Parallel_NonTTY_NoLiveLineWrites(t *testing.T) {
	// Default workflowParallelStdoutIsTTY returns false in `go test` (no TTY).
	// Verify the post-Wait text emit path is the SOLE output channel — block
	// rows produce no ANSI to either Stdout/Stderr.
	dir := t.TempDir()
	a := makeShellLeaf("nontty.a", `true`)
	b := makeShellLeaf("nontty.b", `true`)

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "nontty.wf",
		Group:     "nontty",
		LocalName: "wf",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "nontty.a"},
					{Command: "nontty.b"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, a, b)
	out, errOut, err := runParallelWorkflowCtx(t, dir, reg, wf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(out, "\x1b[") || strings.Contains(errOut, "\x1b[") {
		t.Errorf("expected no ANSI escape codes in non-TTY mode; out=%q err=%q", out, errOut)
	}
	for _, want := range []string{"✓ [1/2] Done: nontty.a", "✓ [2/2] Done: nontty.b"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("expected %q in stderr; got:\n%s", want, errOut)
		}
	}
}

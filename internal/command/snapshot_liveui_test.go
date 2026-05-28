package command

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"devbox-cli/internal/shared/liveui"
	"devbox-cli/internal/snapshot"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/runtime"
)

// withSnapshotLiveLineFactory swaps both the live-line factory and the
// TTY-detection seam for the duration of a test. The cleanup restores both.
func withSnapshotLiveLineFactory(t *testing.T, buf io.Writer, isTTY bool) {
	t.Helper()
	prevOutputs := snapshotLiveOutputs
	prevFactory := newSnapshotObserverLiveLine
	snapshotLiveOutputs = func() (io.Writer, io.Writer, bool) {
		if !isTTY {
			return io.Discard, buf, false
		}
		return buf, buf, true
	}
	newSnapshotObserverLiveLine = func(label string) *liveui.LiveLine {
		termOut, screen, ttyOn := snapshotLiveOutputs()
		live := liveui.NewLiveLine(termOut, screen, ttyOn)
		live.SetTestHooks(true, func() int { return 120 })
		live.SetText(label)
		return live
	}
	t.Cleanup(func() {
		snapshotLiveOutputs = prevOutputs
		newSnapshotObserverLiveLine = prevFactory
		ui.ClearHuhHooks()
	})
}

func TestNewSnapshotLiveObserver_DisabledFlag(t *testing.T) {
	withSnapshotLiveLineFactory(t, &bytes.Buffer{}, true)
	got := newSnapshotLiveObserver("snapshot create: test", true, []model.WorkflowStep{{Command: "a"}})
	if got != nil {
		t.Fatalf("expected nil observer when disabled=true, got %T", got)
	}
}

func TestNewSnapshotLiveObserver_NonTTY(t *testing.T) {
	withSnapshotLiveLineFactory(t, &bytes.Buffer{}, false)
	got := newSnapshotLiveObserver("snapshot create: test", false, []model.WorkflowStep{{Command: "a"}})
	if got != nil {
		t.Fatalf("expected nil observer when stdout is not a TTY, got %T", got)
	}
}

func TestNewSnapshotLiveObserver_HappyPath_PaintsRows(t *testing.T) {
	var buf bytes.Buffer
	withSnapshotLiveLineFactory(t, &buf, true)
	steps := []model.WorkflowStep{
		{Command: "step.a"},
		{Command: "step.b"},
	}
	obs := newSnapshotLiveObserver("snapshot create: test", false, steps)
	if obs == nil {
		t.Fatalf("expected non-nil observer on TTY")
	}

	obs.OnStepStart(0, len(steps), steps[0])
	obs.OnStepEnd(0, steps[0], runtime.StepResult{Status: runtime.StepStatusDone})
	obs.OnStepStart(1, len(steps), steps[1])
	obs.OnStepEnd(1, steps[1], runtime.StepResult{
		Status: runtime.StepStatusFailed,
		Err:    errors.New("boom"),
	})
	obs.Close()

	out := buf.String()
	// Labels should appear; on failure the error string is appended.
	if !strings.Contains(out, "step.a") {
		t.Errorf("expected step.a label in output, got: %q", out)
	}
	if !strings.Contains(out, "step.b") {
		t.Errorf("expected step.b label in output, got: %q", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("expected failure error message in output, got: %q", out)
	}
}

func TestNewSnapshotLiveObserver_SkippedShowsReason(t *testing.T) {
	var buf bytes.Buffer
	withSnapshotLiveLineFactory(t, &buf, true)
	steps := []model.WorkflowStep{{Command: "step.skipme"}}
	obs := newSnapshotLiveObserver("snapshot create: test", false, steps)
	if obs == nil {
		t.Fatalf("expected non-nil observer on TTY")
	}

	obs.OnStepEnd(0, steps[0], runtime.StepResult{
		Status:     runtime.StepStatusSkipped,
		SkipReason: "when: false",
	})
	obs.Close()

	out := buf.String()
	if !strings.Contains(out, "when: false") {
		t.Errorf("expected skip reason in output, got: %q", out)
	}
}

func TestSnapshotLiveObserver_PauseDepth_HuhAndSuspendCompose(t *testing.T) {
	var buf bytes.Buffer
	withSnapshotLiveLineFactory(t, &buf, true)
	steps := []model.WorkflowStep{{Command: "step"}}
	closer := newSnapshotLiveObserver("test", false, steps)
	if closer == nil {
		t.Fatalf("expected observer on TTY")
	}
	o, ok := closer.(*snapshotLiveObserver)
	if !ok {
		t.Fatalf("expected *snapshotLiveObserver, got %T", closer)
	}
	defer o.Close()

	// Initial depth is zero.
	if got := o.pauseDepth.Load(); got != 0 {
		t.Fatalf("initial pauseDepth=%d, want 0", got)
	}

	// SuspendForExec → depth=1 (real pause).
	o.SuspendForExec()
	if got := o.pauseDepth.Load(); got != 1 {
		t.Fatalf("after SuspendForExec pauseDepth=%d, want 1", got)
	}

	// huh-prompt before hook (nested) → depth=2 (no extra Pause call).
	o.pause()
	if got := o.pauseDepth.Load(); got != 2 {
		t.Fatalf("after nested pause pauseDepth=%d, want 2", got)
	}

	// huh-prompt after hook → depth=1 (still paused, no Resume yet).
	o.resume()
	if got := o.pauseDepth.Load(); got != 1 {
		t.Fatalf("after nested resume pauseDepth=%d, want 1", got)
	}

	// ResumeAfterExec → depth=0 (real resume).
	o.ResumeAfterExec()
	if got := o.pauseDepth.Load(); got != 0 {
		t.Fatalf("after ResumeAfterExec pauseDepth=%d, want 0", got)
	}
}

func TestSnapshotLiveObserver_Close_ClearsHuhHooks(t *testing.T) {
	var buf bytes.Buffer
	withSnapshotLiveLineFactory(t, &buf, true)
	steps := []model.WorkflowStep{{Command: "step"}}
	obs := newSnapshotLiveObserver("test", false, steps)
	if obs == nil {
		t.Fatalf("expected observer")
	}
	before, after := ui.SnapshotHuhHooks()
	if before == nil || after == nil {
		t.Fatalf("expected huh hooks to be installed; got before==nil:%v after==nil:%v", before == nil, after == nil)
	}
	obs.Close()
	before, after = ui.SnapshotHuhHooks()
	if before != nil || after != nil {
		t.Errorf("expected hooks cleared after Close; got before==nil:%v after==nil:%v", before == nil, after == nil)
	}
}

func TestSnapshotStepLabel(t *testing.T) {
	cases := []struct {
		name string
		step model.WorkflowStep
		want string
	}{
		{"command", model.WorkflowStep{Command: "do.thing"}, "do.thing"},
		{"named", model.WorkflowStep{Name: "alias", Command: "do.thing"}, "alias"},
		{"confirm", model.WorkflowStep{Confirm: "are you sure?"}, "confirm: are you sure?"},
		{"parallel", model.WorkflowStep{Parallel: &model.WorkflowParallel{Steps: []model.WorkflowStep{{Command: "a"}, {Command: "b"}}}}, "parallel (2 sub-steps)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := snapshotStepLabel(tc.step); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSnapshotLiveObserver_ImplementsContracts(t *testing.T) {
	// Compile-time checks live in snapshot_liveui.go; this sanity-runs them
	// at test time too in case the file is split.
	var _ runtime.WorkflowStepObserver = (*snapshotLiveObserver)(nil)
	var _ runtime.StepIOSuspender = (*snapshotLiveObserver)(nil)
	var _ snapshot.StepObserverCloser = (*snapshotLiveObserver)(nil)
}

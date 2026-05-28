package runtime

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/shared/tpl"
	"devbox-cli/internal/usercommands/model"
)

// recordedEvent captures one observer callback for sequence assertions.
type recordedEvent struct {
	kind   string // "start", "end", "suspend", "resume"
	idx    int
	total  int
	status StepStatus
	reason string
	errMsg string
}

// fakeObserver records observer + suspender callbacks in arrival order. Each
// callback also appends to a per-test sequence slice so suspend/resume
// ordering relative to start/end can be asserted.
type fakeObserver struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (o *fakeObserver) OnStepStart(idx, total int, step model.WorkflowStep) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, recordedEvent{kind: "start", idx: idx, total: total})
}

func (o *fakeObserver) OnStepEnd(idx int, step model.WorkflowStep, result StepResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ev := recordedEvent{kind: "end", idx: idx, status: result.Status, reason: result.SkipReason}
	if result.Err != nil {
		ev.errMsg = result.Err.Error()
	}
	o.events = append(o.events, ev)
}

// observerWithoutSuspender wraps a fakeObserver so the type assertion to
// StepIOSuspender fails. This proves the runner's comma-ok guard skips the
// suspend/resume window for observers that don't implement the interface.
type observerWithoutSuspender struct {
	inner *fakeObserver
}

func (o *observerWithoutSuspender) OnStepStart(idx, total int, step model.WorkflowStep) {
	o.inner.OnStepStart(idx, total, step)
}
func (o *observerWithoutSuspender) OnStepEnd(idx int, step model.WorkflowStep, result StepResult) {
	o.inner.OnStepEnd(idx, step, result)
}

// observerWithSuspender adds suspend/resume to the fake observer.
type observerWithSuspender struct {
	inner *fakeObserver
}

func (o *observerWithSuspender) OnStepStart(idx, total int, step model.WorkflowStep) {
	o.inner.OnStepStart(idx, total, step)
}
func (o *observerWithSuspender) OnStepEnd(idx int, step model.WorkflowStep, result StepResult) {
	o.inner.OnStepEnd(idx, step, result)
}
func (o *observerWithSuspender) SuspendForExec() {
	o.inner.mu.Lock()
	defer o.inner.mu.Unlock()
	o.inner.events = append(o.inner.events, recordedEvent{kind: "suspend"})
}
func (o *observerWithSuspender) ResumeAfterExec() {
	o.inner.mu.Lock()
	defer o.inner.mu.Unlock()
	o.inner.events = append(o.inner.events, recordedEvent{kind: "resume"})
}

// runWithObserver executes a workflow with the given observer and returns the
// stderr buffer plus the run error.
func runWithObserver(t *testing.T, reg *Registry, wf *CommandDef, obs WorkflowStepObserver, opts ...func(*RunContext)) (string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:          wf,
		Params:       map[string]any{},
		Context:      map[string]any{},
		Render:       &tpl.RenderContext{Params: map[string]any{}},
		Registry:     reg,
		Stdout:       &outBuf,
		Stderr:       &errBuf,
		StepObserver: obs,
	}
	for _, opt := range opts {
		opt(&rc)
	}
	err := (&WorkflowRunner{}).Run(context.Background(), rc)
	return errBuf.String(), err
}

// summarize returns a compact text form of the event sequence for diff-friendly
// failure messages, e.g. "start(0) suspend resume end(0:done)".
func summarize(events []recordedEvent) string {
	parts := make([]string, 0, len(events))
	for _, e := range events {
		switch e.kind {
		case "start":
			parts = append(parts, fmt.Sprintf("start(%d)", e.idx))
		case "end":
			tag := "?"
			switch e.status {
			case StepStatusDone:
				tag = "done"
			case StepStatusFailed:
				tag = "failed"
			case StepStatusSkipped:
				tag = "skipped"
			}
			parts = append(parts, fmt.Sprintf("end(%d:%s)", e.idx, tag))
		default:
			parts = append(parts, e.kind)
		}
	}
	return strings.Join(parts, " ")
}

func TestWorkflowObserver_HappyPath_TwoSteps(t *testing.T) {
	s1 := makeShellLeaf("obs.a", `true`)
	s2 := makeShellLeaf("obs.b", `true`)
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{{Command: "obs.a"}, {Command: "obs.b"}},
	}
	reg := buildWorkflowRegistry(wf, s1, s2)

	fake := &fakeObserver{}
	obs := &observerWithSuspender{inner: fake}

	_, err := runWithObserver(t, reg, wf, obs)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := summarize(fake.events)
	want := "start(0) suspend resume end(0:done) start(1) suspend resume end(1:done)"
	if got != want {
		t.Errorf("event sequence mismatch:\n got: %s\nwant: %s", got, want)
	}

	suspendCount, resumeCount := 0, 0
	for _, e := range fake.events {
		switch e.kind {
		case "suspend":
			suspendCount++
		case "resume":
			resumeCount++
		}
	}
	if suspendCount != 2 || resumeCount != 2 {
		t.Errorf("expected 2 suspend + 2 resume; got suspend=%d resume=%d",
			suspendCount, resumeCount)
	}
}

func TestWorkflowObserver_WhenFalse_Skipped(t *testing.T) {
	s1 := makeShellLeaf("obs.x", `true`)
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{
			{Command: "obs.x", When: "false"},
		},
	}
	reg := buildWorkflowRegistry(wf, s1)

	fake := &fakeObserver{}
	obs := &observerWithSuspender{inner: fake}

	_, err := runWithObserver(t, reg, wf, obs)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := summarize(fake.events), "end(0:skipped)"; got != want {
		t.Errorf("sequence mismatch:\n got: %s\nwant: %s", got, want)
	}
	if fake.events[0].reason != "when: false" {
		t.Errorf("skip reason: got %q want %q", fake.events[0].reason, "when: false")
	}
}

func TestWorkflowObserver_FilesGateSkip(t *testing.T) {
	missingPath := t.TempDir() + "/nope.txt"
	leaf := &CommandDef{
		Type:      CommandTypeShell,
		ID:        "obs.gate",
		Group:     "obs",
		LocalName: "gate",
		Cmd:       `true`,
		Files: map[string]FileSpec{
			"input": {
				Path:     missingPath,
				Access:   FileAccessRead,
				Required: true,
			},
		},
	}
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{
			{Name: "gate-step", Command: "obs.gate"},
		},
	}
	reg := buildWorkflowRegistry(wf, leaf)

	fake := &fakeObserver{}
	obs := &observerWithSuspender{inner: fake}

	overrides := map[string]config.SubStepOverride{
		"gate-step": {
			FilesGate: &filesgate.FilesGate{
				Command: "obs.gate",
				Require: filesgate.RequireList{IDs: []string{"input"}},
				State:   filesgate.StateReadable,
			},
		},
	}

	_, err := runWithObserver(t, reg, wf, obs, func(rc *RunContext) {
		rc.Config = &config.DevboxConfig{}
		rc.WorkflowSubStepOverrides = overrides
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := summarize(fake.events), "end(0:skipped)"; got != want {
		t.Errorf("sequence mismatch:\n got: %s\nwant: %s", got, want)
	}
	if !strings.HasPrefix(fake.events[0].reason, "files_gate:") {
		t.Errorf("skip reason should start with files_gate:, got %q", fake.events[0].reason)
	}
}

func TestWorkflowObserver_HardFailure_FreezesRowBeforeReturn(t *testing.T) {
	s1 := makeShellLeaf("obs.fail", `exit 7`)
	s2 := makeShellLeaf("obs.never", `true`)
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{{Command: "obs.fail"}, {Command: "obs.never"}},
	}
	reg := buildWorkflowRegistry(wf, s1, s2)

	fake := &fakeObserver{}
	obs := &observerWithSuspender{inner: fake}

	_, err := runWithObserver(t, reg, wf, obs)
	if err == nil {
		t.Fatal("expected error from failing step")
	}
	got := summarize(fake.events)
	want := "start(0) suspend resume end(0:failed)"
	if got != want {
		t.Errorf("sequence mismatch:\n got: %s\nwant: %s", got, want)
	}
	// Resume must fire before the failed OnStepEnd so the live UI is no
	// longer suspended when the row freezes.
	var lastResume, lastEnd int
	for i, e := range fake.events {
		if e.kind == "resume" {
			lastResume = i
		}
		if e.kind == "end" {
			lastEnd = i
		}
	}
	if lastResume >= lastEnd {
		t.Errorf("resume must precede end; events=%v", fake.events)
	}
}

func TestWorkflowObserver_ContinueOnError(t *testing.T) {
	s1 := makeShellLeaf("obs.boom", `exit 1`)
	s2 := makeShellLeaf("obs.ok", `true`)
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{
			{Command: "obs.boom", ContinueOnError: true},
			{Command: "obs.ok"},
		},
	}
	reg := buildWorkflowRegistry(wf, s1, s2)

	fake := &fakeObserver{}
	obs := &observerWithSuspender{inner: fake}

	_, err := runWithObserver(t, reg, wf, obs)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := summarize(fake.events)
	want := "start(0) suspend resume end(0:failed) start(1) suspend resume end(1:done)"
	if got != want {
		t.Errorf("sequence mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestWorkflowObserver_WithoutSuspender_NoPauseEvents(t *testing.T) {
	s1 := makeShellLeaf("obs.a", `true`)
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{{Command: "obs.a"}},
	}
	reg := buildWorkflowRegistry(wf, s1)

	fake := &fakeObserver{}
	obs := &observerWithoutSuspender{inner: fake}

	_, err := runWithObserver(t, reg, wf, obs)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := summarize(fake.events), "start(0) end(0:done)"; got != want {
		t.Errorf("sequence mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestWorkflowObserver_NilObserver_NoCallsNoPanic(t *testing.T) {
	s1 := makeShellLeaf("obs.a", `true`)
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{{Command: "obs.a"}},
	}
	reg := buildWorkflowRegistry(wf, s1)

	// Explicit nil observer — runner must skip the StepIOSuspender type
	// assertion and the observer hook calls without panicking.
	_, err := runWithObserver(t, reg, wf, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestWorkflowObserver_ConfirmStep_AutoAcceptedNonInteractive(t *testing.T) {
	// In NonInteractive mode runConfirmStep returns nil immediately, so the
	// observer sees a clean start/end(done) pair for the confirm step.
	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{{Confirm: "Proceed?"}},
	}
	reg := buildWorkflowRegistry(wf)

	fake := &fakeObserver{}
	obs := &observerWithSuspender{inner: fake}

	_, err := runWithObserver(t, reg, wf, obs, func(rc *RunContext) {
		rc.NonInteractive = true
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// confirm steps are NOT sequential command steps, so no suspend/resume.
	if got, want := summarize(fake.events), "start(0) end(0:done)"; got != want {
		t.Errorf("sequence mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestWorkflowObserver_ConfirmStep_Aborted(t *testing.T) {
	// CI=1 makes render.Writer.Confirm auto-yes; clear it for this test.
	t.Setenv("CI", "")
	t.Setenv("DEVBOX_NONINTERACTIVE", "")

	wf := &CommandDef{
		Type: CommandTypeWorkflow, ID: "obs.wf",
		Steps: []WorkflowStep{{Confirm: "Proceed?"}},
	}
	reg := buildWorkflowRegistry(wf)

	fake := &fakeObserver{}
	obs := &observerWithSuspender{inner: fake}

	// IsInteractiveFn returns false for a strings.Reader stdin, so the
	// runner falls through to render.Writer.Confirm which reads "n" and
	// returns false → aborted by user.
	_, err := runWithObserver(t, reg, wf, obs, func(rc *RunContext) {
		rc.Stdin = strings.NewReader("n\n")
	})
	if err == nil || !strings.Contains(err.Error(), "aborted by user") {
		t.Fatalf("expected aborted error, got %v", err)
	}
	if got, want := summarize(fake.events), "start(0) end(0:failed)"; got != want {
		t.Errorf("sequence mismatch:\n got: %s\nwant: %s", got, want)
	}
}

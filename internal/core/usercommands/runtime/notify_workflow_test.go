package runtime

import (
	"bytes"
	"context"
	"testing"

	"devbox-cli/internal/shared/tpl"
)

// TestWorkflowRunner_SubStep_SkipNotifyIsSet verifies that workflow sub-steps
// suppress notifications even when their underlying command has notify: true.
// The workflow command itself is not opted in, so zero events fire.
func TestWorkflowRunner_SubStep_SuppressesNotifyTrueLeaf(t *testing.T) {
	rec := installRecordingNotifier(t)

	leaf := &CommandDef{
		ID:     "leaf.cmd",
		Type:   CommandTypeShell,
		Files:  map[string]FileSpec{},
		Cmd:    "true",
		Notify: true, // would fire if invoked top-level
	}

	wf := &CommandDef{
		ID:   "wf.parent",
		Type: CommandTypeWorkflow,
		Steps: []WorkflowStep{
			{Command: "leaf.cmd"},
		},
	}

	reg := buildWorkflowRegistry(leaf, wf)

	var out, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:      wf,
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &out,
		Stderr:   &errBuf,
	}
	r := &WorkflowRunner{}
	if err := r.Run(context.Background(), rc); err != nil {
		t.Fatalf("workflow run: %v", err)
	}

	// The workflow command itself was not invoked through RunCommand
	// (the test calls WorkflowRunner.Run directly), so neither layer
	// should record any event — the leaf's SkipNotify=true must suppress
	// its own notification.
	if got := len(rec.snapshot()); got != 0 {
		t.Errorf("want 0 events, got %d", got)
	}
}

// TestWorkflowRunner_SubStep_SetsSkipNotify directly inspects the inner
// RunContext built by the workflow runner for each sub-step and asserts
// SkipNotify=true is set, regardless of parent's SkipNotify value.
func TestWorkflowRunner_SubStep_SetsSkipNotify(t *testing.T) {
	leaf := &CommandDef{
		ID:    "leaf.cmd",
		Type:  CommandTypeShell,
		Files: map[string]FileSpec{},
		Cmd:   "true",
	}
	wf := &CommandDef{
		ID:   "wf.parent",
		Type: CommandTypeWorkflow,
		Steps: []WorkflowStep{
			{Command: "leaf.cmd"},
		},
	}
	reg := buildWorkflowRegistry(leaf, wf)

	var captured RunContext
	prev := TestSnapshotRC
	TestSnapshotRC = func(rc RunContext) {
		if rc.Cmd != nil && rc.Cmd.ID == "leaf.cmd" {
			captured = rc
		}
	}
	t.Cleanup(func() { TestSnapshotRC = prev })

	var out, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:      wf,
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &out,
		Stderr:   &errBuf,
	}
	r := &WorkflowRunner{}
	if err := r.Run(context.Background(), rc); err != nil {
		t.Fatalf("workflow run: %v", err)
	}
	if captured.Cmd == nil {
		t.Fatal("snapshot never captured the leaf sub-step")
	}
	if !captured.SkipNotify {
		t.Errorf("workflow sub-step SkipNotify = false; want true")
	}
}

// TestWorkflowRunner_SubStep_TopLevelNotifyFires uses RunCommand for the
// outer workflow so the top-level notifier fires, but the inner leaf with
// notify: true must NOT add a second event.
func TestRunCommand_WorkflowParent_NotifyOnlyOnce(t *testing.T) {
	rec := installRecordingNotifier(t)

	leaf := &CommandDef{
		ID:     "leaf.cmd",
		Type:   CommandTypeShell,
		Files:  map[string]FileSpec{},
		Cmd:    "true",
		Notify: true,
	}

	wf := &CommandDef{
		ID:     "wf.parent",
		Type:   CommandTypeWorkflow,
		Notify: true,
		Steps: []WorkflowStep{
			{Command: "leaf.cmd"},
		},
	}

	reg := buildWorkflowRegistry(leaf, wf)

	var out, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:      wf,
		Render:   &tpl.RenderContext{},
		Registry: reg,
		Stdout:   &out,
		Stderr:   &errBuf,
	}
	if err := RunCommand(context.Background(), rc); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}

	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want exactly 1 event (top-level only), got %d", len(evs))
	}
	if evs[0].Operation != "command:wf.parent" {
		t.Errorf("Operation = %q, want command:wf.parent", evs[0].Operation)
	}
}

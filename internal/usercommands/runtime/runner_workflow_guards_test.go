package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
)

// runWorkflowUnderParallel runs a workflow with UnderParallel=true.
func runWorkflowUnderParallel(t *testing.T, reg *Registry, wf *CommandDef, skipConfirm, nonInteractive bool) (string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:            wf,
		Params:         map[string]any{},
		Context:        map[string]any{},
		Render:         &tpl.RenderContext{Params: map[string]any{}},
		Registry:       reg,
		Stdout:         &outBuf,
		Stderr:         &errBuf,
		UnderParallel:  true,
		SkipConfirm:    skipConfirm,
		NonInteractive: nonInteractive,
	}
	err := (&WorkflowRunner{}).Run(context.Background(), rc)
	return errBuf.String(), err
}

// -----------------------------------------------------------------------------
// Nested-parallel guard
// -----------------------------------------------------------------------------

func TestWorkflowRunner_NestedParallel_UnderParallelRejected(t *testing.T) {
	leaf := makeShellLeaf("wf.leaf", "true")
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.outer",
		Group:     "wf",
		LocalName: "outer",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.leaf"},
					{Command: "wf.leaf"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, leaf)
	_, err := runWorkflowUnderParallel(t, reg, wf, false, false)
	if err == nil {
		t.Fatal("expected nested-parallel error")
	}
	if !errors.Is(err, ErrWorkflowNestedParallel) {
		t.Errorf("expected ErrWorkflowNestedParallel; got %v", err)
	}
}

func TestWorkflowRunner_NestedParallel_NotUnderParallelRuns(t *testing.T) {
	leaf := makeShellLeaf("wf.leaf", "true")
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.outer",
		Group:     "wf",
		LocalName: "outer",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.leaf"},
					{Command: "wf.leaf"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(wf, leaf)
	_, _, err := runParallelWorkflowCtx(t, t.TempDir(), reg, wf)
	if err != nil {
		t.Fatalf("expected no error without UnderParallel; got %v", err)
	}
}

func TestWorkflowRunner_NestedParallel_WrappedByParentGroup(t *testing.T) {
	// Inner workflow has a parallel block; outer workflow runs it as a parallel
	// sub-step. runParallelGroup sets UnderParallel=true on the sub RunContext,
	// so when the inner WorkflowRunner.Run is invoked it should fail with the
	// nested-parallel sentinel, wrapped by the parent group's "workflow sub-step %q" wrapper.
	leaf := makeShellLeaf("wf.leaf", "true")
	inner := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.inner",
		Group:     "wf",
		LocalName: "inner",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.leaf"},
					{Command: "wf.leaf"},
				},
			}},
		},
	}
	outer := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.outer",
		Group:     "wf",
		LocalName: "outer",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				FailFast: ffFalse,
				Steps: []WorkflowStep{
					{Command: "wf.inner"},
					{Command: "wf.leaf"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(outer, inner, leaf)
	_, _, err := runParallelWorkflowCtx(t, t.TempDir(), reg, outer)
	if err == nil {
		t.Fatal("expected nested-parallel error in outer group")
	}
	if !errors.Is(err, ErrWorkflowNestedParallel) {
		t.Errorf("expected wrapped ErrWorkflowNestedParallel; got %v", err)
	}
	if !strings.Contains(err.Error(), "workflow sub-step") {
		t.Errorf("expected parent-group wrap prefix; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Transitive confirmation guard — runConfirmStep
// -----------------------------------------------------------------------------

func TestWorkflowRunner_ConfirmStep_UnderParallelRejected(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.confirm",
		Group:     "wf",
		LocalName: "confirm",
		Steps: []WorkflowStep{
			{Confirm: "Are you sure?"},
		},
	}
	reg := buildWorkflowRegistry(wf)
	_, err := runWorkflowUnderParallel(t, reg, wf, false, false)
	if err == nil {
		t.Fatal("expected confirm-in-parallel error")
	}
	if !errors.Is(err, ErrConfirmInsideParallel) {
		t.Errorf("expected ErrConfirmInsideParallel; got %v", err)
	}
}

func TestWorkflowRunner_ConfirmStep_UnderParallel_NonInteractiveBypass(t *testing.T) {
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.confirm",
		Group:     "wf",
		LocalName: "confirm",
		Steps: []WorkflowStep{
			{Confirm: "Continue?"},
		},
	}
	reg := buildWorkflowRegistry(wf)
	if _, err := runWorkflowUnderParallel(t, reg, wf, false, true); err != nil {
		t.Fatalf("NonInteractive should bypass guard; got %v", err)
	}
}

func TestWorkflowRunner_ConfirmStep_UnderParallel_SkipConfirmBypass(t *testing.T) {
	wf := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.confirm",
		Group:     "wf",
		LocalName: "confirm",
		Steps: []WorkflowStep{
			{Confirm: "Are you sure?"},
		},
	}
	reg := buildWorkflowRegistry(wf)
	if _, err := runWorkflowUnderParallel(t, reg, wf, true, false); err != nil {
		t.Fatalf("SkipConfirm should bypass guard; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Transitive confirmation guard — ConfirmCommand
// -----------------------------------------------------------------------------

func TestConfirmCommand_UnderParallelRejected(t *testing.T) {
	cmd := &CommandDef{
		ID:           "test.confirming",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          "true",
	}
	rc := RunContext{
		Cmd:           cmd,
		Render:        &tpl.RenderContext{},
		UnderParallel: true,
	}
	err := ConfirmCommand(rc)
	if err == nil {
		t.Fatal("expected confirm-in-parallel error")
	}
	if !errors.Is(err, ErrConfirmInsideParallel) {
		t.Errorf("expected ErrConfirmInsideParallel; got %v", err)
	}
}

func TestConfirmCommand_UnderParallel_SkipConfirmBypass(t *testing.T) {
	cmd := &CommandDef{
		ID:           "test.confirming",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          "true",
	}
	rc := RunContext{
		Cmd:           cmd,
		Render:        &tpl.RenderContext{},
		UnderParallel: true,
		SkipConfirm:   true,
	}
	if err := ConfirmCommand(rc); err != nil {
		t.Fatalf("SkipConfirm should bypass guard; got %v", err)
	}
}

func TestConfirmCommand_UnderParallel_NonInteractiveBypass(t *testing.T) {
	cmd := &CommandDef{
		ID:           "test.confirming",
		Type:         CommandTypeShell,
		Confirmation: true,
		Cmd:          "true",
	}
	rc := RunContext{
		Cmd:            cmd,
		Render:         &tpl.RenderContext{},
		UnderParallel:  true,
		NonInteractive: true,
	}
	if err := ConfirmCommand(rc); err != nil {
		t.Fatalf("NonInteractive should bypass guard; got %v", err)
	}
}

func TestConfirmCommand_NonConfirmingCommandIgnoresGuard(t *testing.T) {
	// Non-confirming command run under parallel must NOT trigger the guard.
	cmd := &CommandDef{
		ID:   "test.plain",
		Type: CommandTypeShell,
		Cmd:  "true",
	}
	rc := RunContext{
		Cmd:           cmd,
		Render:        &tpl.RenderContext{},
		UnderParallel: true,
	}
	if err := ConfirmCommand(rc); err != nil {
		t.Fatalf("non-confirming command should not be guarded; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Transitive confirmation through parallel sub-step → workflow → confirm
// -----------------------------------------------------------------------------

func TestWorkflowRunner_Parallel_TransitiveConfirmRejected(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	// Inner workflow has a confirm step.
	inner := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.inner",
		Group:     "wf",
		LocalName: "inner",
		Steps: []WorkflowStep{
			{Confirm: "Inner confirm?"},
		},
	}
	// Outer workflow calls inner from a parallel group.
	outer := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.outer",
		Group:     "wf",
		LocalName: "outer",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.inner"},
					{Command: "wf.inner"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(outer, inner)
	_, _, err := runParallelWorkflowCtx(t, t.TempDir(), reg, outer)
	if err == nil {
		t.Fatal("expected transitive confirm rejection")
	}
	if !errors.Is(err, ErrConfirmInsideParallel) {
		t.Errorf("expected ErrConfirmInsideParallel; got %v", err)
	}
}

func TestWorkflowRunner_Parallel_TransitiveConfirmationCommandRejected(t *testing.T) {
	// Inner workflow's final step references a `confirmation: true` command.
	// Task 5 preflight cannot see this because the IMMEDIATE sub-step is the
	// workflow (Confirmation=false). The runtime guard in ConfirmCommand catches it.
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	confirming := &CommandDef{
		Type:         CommandTypeShell,
		ID:           "wf.confirming",
		Group:        "wf",
		LocalName:    "confirming",
		Confirmation: true,
		Cmd:          "true",
	}
	innerWF := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.inner",
		Group:     "wf",
		LocalName: "inner",
		Steps: []WorkflowStep{
			{Command: "wf.confirming"},
		},
	}
	outer := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.outer",
		Group:     "wf",
		LocalName: "outer",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.inner"},
					{Command: "wf.inner"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(outer, innerWF, confirming)
	_, _, err := runParallelWorkflowCtx(t, t.TempDir(), reg, outer)
	if err == nil {
		t.Fatal("expected transitive confirmation-command rejection")
	}
	if !errors.Is(err, ErrConfirmInsideParallel) {
		t.Errorf("expected ErrConfirmInsideParallel; got %v", err)
	}
}

func TestWorkflowRunner_Parallel_TransitiveConfirmWithSkipConfirm(t *testing.T) {
	// With SkipConfirm=true the transitive confirmation passes through.
	confirming := &CommandDef{
		Type:         CommandTypeShell,
		ID:           "wf.confirming",
		Group:        "wf",
		LocalName:    "confirming",
		Confirmation: true,
		Cmd:          "true",
	}
	innerWF := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.inner",
		Group:     "wf",
		LocalName: "inner",
		Steps: []WorkflowStep{
			{Command: "wf.confirming"},
		},
	}
	outer := &CommandDef{
		Type:      CommandTypeWorkflow,
		ID:        "wf.outer",
		Group:     "wf",
		LocalName: "outer",
		Steps: []WorkflowStep{
			{Parallel: &WorkflowParallel{
				Steps: []WorkflowStep{
					{Command: "wf.inner"},
					{Command: "wf.inner"},
				},
			}},
		},
	}
	reg := buildWorkflowRegistry(outer, innerWF, confirming)

	var outBuf, errBuf bytes.Buffer
	rc := RunContext{
		Cmd:         outer,
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{Params: map[string]any{}},
		Registry:    reg,
		ProjectRoot: t.TempDir(),
		Stdout:      &outBuf,
		Stderr:      &errBuf,
		SkipConfirm: true,
	}
	if err := (&WorkflowRunner{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("SkipConfirm should bypass transitive guard; got %v\nstderr:\n%s", err, errBuf.String())
	}
}

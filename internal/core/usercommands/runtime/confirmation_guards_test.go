package runtime

import (
	"errors"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

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

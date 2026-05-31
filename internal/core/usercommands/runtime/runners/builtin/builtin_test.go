package builtin

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/runtime/spec"
)

// builtinRC builds a minimal RunContext targeted at the builtin Runner with the
// given builtin name and parallel/skip flags.
func builtinRC(name string, underParallel, skipConfirm, nonInteractive bool) spec.RunContext {
	return spec.RunContext{
		Cmd: &model.CommandDef{
			ID:   "test." + name,
			Type: model.CommandTypeBuiltin,
			Cmd:  name,
		},
		Config:         &config.DevboxConfig{},
		Stdout:         &bytes.Buffer{},
		Stderr:         &bytes.Buffer{},
		UnderParallel:  underParallel,
		SkipConfirm:    skipConfirm,
		NonInteractive: nonInteractive,
	}
}

func TestBuiltinRunner_ConfirmUnderParallel_Rejected(t *testing.T) {
	r := &Runner{}
	err := r.Run(context.Background(), builtinRC("confirm", true, false, false))
	if !errors.Is(err, spec.ErrConfirmInsideParallel) {
		t.Fatalf("expected ErrConfirmInsideParallel, got %v", err)
	}
}

func TestBuiltinRunner_ConfirmUnderParallel_SkipConfirmBypass(t *testing.T) {
	r := &Runner{}
	// SkipConfirm=true should let confirm pass the parallel guard. The builtin
	// itself short-circuits to a no-op when SkipConfirm is set, so no prompt.
	err := r.Run(context.Background(), builtinRC("confirm", true, true, false))
	if errors.Is(err, spec.ErrConfirmInsideParallel) {
		t.Fatalf("SkipConfirm should bypass the parallel guard for confirm, got %v", err)
	}
}

func TestBuiltinRunner_DaemonLogsUnderParallel_Rejected(t *testing.T) {
	r := &Runner{}
	err := r.Run(context.Background(), builtinRC("docker_daemon_logs", true, false, false))
	if !errors.Is(err, spec.ErrConfirmInsideParallel) {
		t.Fatalf("expected ErrConfirmInsideParallel for docker_daemon_logs, got %v", err)
	}
}

func TestBuiltinRunner_DaemonLogsUnderParallel_SkipConfirmDoesNotBypass(t *testing.T) {
	// Asymmetry: SkipConfirm only bypasses the parallel guard for `confirm`,
	// never for docker_daemon_logs (no auto-skip for a foreground tail).
	r := &Runner{}
	err := r.Run(context.Background(), builtinRC("docker_daemon_logs", true, true, true))
	if !errors.Is(err, spec.ErrConfirmInsideParallel) {
		t.Fatalf("expected docker_daemon_logs to still reject under parallel with SkipConfirm, got %v", err)
	}
}

func TestBuiltinRunner_DaemonLogsNotUnderParallel_PassesGuard(t *testing.T) {
	// Outside a parallel group, the guard does not apply. The builtin may
	// still error trying to talk to docker, but it must NOT be the parallel
	// guard error.
	r := &Runner{}
	err := r.Run(context.Background(), builtinRC("docker_daemon_logs", false, false, false))
	if errors.Is(err, spec.ErrConfirmInsideParallel) {
		t.Fatalf("guard must not fire outside parallel: %v", err)
	}
}

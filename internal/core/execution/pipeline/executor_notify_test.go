package pipeline

import (
	"context"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
)

// TestExecCommandAction_SetsSkipNotify verifies the pipeline executor
// always propagates SkipNotify=true into the inner RunContext so
// pipeline-invoked commands never fire desktop notifications.
func TestExecCommandAction_SetsSkipNotify(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:     "noop.cmd",
		Type:   usercommands.CommandTypeShell,
		Cmd:    "true",
		Notify: true, // would fire if top-level
	})

	var captured runtime.RunContext
	prev := runtime.TestSnapshotRC
	runtime.TestSnapshotRC = func(rc runtime.RunContext) { captured = rc }
	t.Cleanup(func() { runtime.TestSnapshotRC = prev })

	cfg := &config.DweConfig{Raw: map[string]any{}}
	for _, parallel := range []bool{false, true} {
		captured = runtime.RunContext{}
		actx := ActionContext{
			WorkDir:     t.TempDir(),
			Cfg:         cfg,
			Reg:         reg,
			SkipConfirm: true,
			Parallel:    parallel,
		}
		if err := ExecAction(context.Background(), config.Action{Type: "command", Cmd: "noop.cmd"}, actx); err != nil {
			t.Fatalf("ExecAction (parallel=%v): %v", parallel, err)
		}
		if !captured.SkipNotify {
			t.Errorf("parallel=%v: SkipNotify = false; want true on every pipeline-invoked command", parallel)
		}
	}
}

// TestExecCommandAction_LeavesUserInvokedFalse pins the DEFAULT in
// execCommandAction: with a zero-value ActionContext — which is every pipeline
// caller — UserInvoked stays false. UserInvoked is what makes a service runner
// ask for a container TTY, and a pipeline step is never the user's top-level
// invocation, so a `type: command` step must never hand the container a
// terminal, no matter that pipeline.childIO may have fabricated a PTY for the
// step's own stdout. A future reader who makes execCommandAction derive the
// flag from anything other than ActionContext must fail here.
func TestExecCommandAction_LeavesUserInvokedFalse(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:   "noop.cmd",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
	})

	var captured runtime.RunContext
	prev := runtime.TestSnapshotRC
	runtime.TestSnapshotRC = func(rc runtime.RunContext) { captured = rc }
	t.Cleanup(func() { runtime.TestSnapshotRC = prev })

	cfg := &config.DweConfig{Raw: map[string]any{}}
	for _, parallel := range []bool{false, true} {
		captured = runtime.RunContext{UserInvoked: true} // must be overwritten by the real build
		actx := ActionContext{
			WorkDir:     t.TempDir(),
			Cfg:         cfg,
			Reg:         reg,
			SkipConfirm: true,
			Parallel:    parallel,
		}
		if err := ExecAction(context.Background(), config.Action{Type: "command", Cmd: "noop.cmd"}, actx); err != nil {
			t.Fatalf("ExecAction (parallel=%v): %v", parallel, err)
		}
		if captured.UserInvoked {
			t.Errorf("parallel=%v: UserInvoked = true; want false on every pipeline-invoked command", parallel)
		}
	}
}

// TestExecCommandAction_UserInvokedPropagates covers the one caller that does
// set the flag. `dwe reset step` runs a single step at the terminal with
// confirm prompts on and the real os.Stdout, so a `type: command` step pointing
// at an interactive service_exec (a psql, a tinker) must keep its container
// terminal — without this the command silently became non-interactive.
//
// The parallel row is the guard on the other half: ActionContext.UserInvoked
// must never survive into a parallel sub-step, which cannot own the terminal.
func TestExecCommandAction_UserInvokedPropagates(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:   "noop.cmd",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
	})

	var captured runtime.RunContext
	prev := runtime.TestSnapshotRC
	runtime.TestSnapshotRC = func(rc runtime.RunContext) { captured = rc }
	t.Cleanup(func() { runtime.TestSnapshotRC = prev })

	cfg := &config.DweConfig{Raw: map[string]any{}}
	for _, tc := range []struct {
		parallel bool
		want     bool
	}{
		{parallel: false, want: true},
		{parallel: true, want: false},
	} {
		// Seed the opposite of what the row expects, so a row can never pass
		// because the command was not dispatched at all.
		captured = runtime.RunContext{UserInvoked: !tc.want}
		actx := ActionContext{
			WorkDir:     t.TempDir(),
			Cfg:         cfg,
			Reg:         reg,
			SkipConfirm: true,
			UserInvoked: true,
			Parallel:    tc.parallel,
		}
		if err := ExecAction(context.Background(), config.Action{Type: "command", Cmd: "noop.cmd"}, actx); err != nil {
			t.Fatalf("ExecAction (parallel=%v): %v", tc.parallel, err)
		}
		if captured.UserInvoked != tc.want {
			t.Errorf("parallel=%v: UserInvoked = %v; want %v", tc.parallel, captured.UserInvoked, tc.want)
		}
	}
}

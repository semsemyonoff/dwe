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

// TestExecCommandAction_LeavesUserInvokedFalse pins the omission in
// execCommandAction: it never sets UserInvoked, so the zero value stands.
// UserInvoked is what makes a service runner ask for a container TTY, and a
// pipeline step is never the user's top-level invocation — a `type: command`
// step must therefore never hand the container a terminal, no matter that
// pipeline.childIO may have fabricated a PTY for the step's own stdout.
// This test is the sole guard on that omission: it is the intended behaviour
// change, so a future reader who "fixes" it by setting UserInvoked in
// execCommandAction must fail here.
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

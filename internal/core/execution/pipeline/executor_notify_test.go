package pipeline

import (
	"context"
	"testing"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/usercommands/runtime"
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

	cfg := &config.DevboxConfig{Raw: map[string]any{}}
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

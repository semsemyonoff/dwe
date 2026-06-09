package pipeline

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestExecShellAction_EchoesCommand asserts execShellAction echoes the resolved
// `sh -c <cmd>` invocation at Verbose (and Debug) and stays silent at LevelOff.
func TestExecShellAction_EchoesCommand(t *testing.T) {
	cases := []struct {
		name     string
		level    trace.Level
		wantEcho bool
	}{
		{"verbose", trace.LevelVerbose, true},
		{"debug", trace.LevelDebug, true},
		{"off", trace.LevelOff, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			trace.Configure(&buf, tc.level)
			defer trace.Configure(nil, trace.LevelOff)

			actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}}
			if err := execShellAction(context.Background(), config.Action{Type: "shell", Cmd: "true"}, actx); err != nil {
				t.Fatalf("execShellAction: %v", err)
			}

			gotEcho := strings.Contains(buf.String(), "$ sh -c true")
			if gotEcho != tc.wantEcho {
				t.Fatalf("level=%v: echo present=%v, want %v (buf=%q)", tc.level, gotEcho, tc.wantEcho, buf.String())
			}
		})
	}
}

// TestExecDweAction_EchoesCommand asserts execDweAction echoes the nested
// `dwe <args>` form at Verbose. A pre-cancelled context makes runChildCmd's
// cmd.Run() return immediately (exec checks ctx before launching) so the test
// never re-executes the test binary as a child dwe process.
func TestExecDweAction_EchoesCommand(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // child process never launches

	actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}}
	// Error is expected (context canceled); we only assert the echo fired first.
	_ = execDweAction(ctx, config.Action{Type: "dwe", Cmd: "deploy run"}, actx)

	if !strings.Contains(buf.String(), "$ dwe deploy run") {
		t.Fatalf("dwe echo missing: %q", buf.String())
	}
}

// TestExecDweAction_SilentAtOff asserts no echo at LevelOff.
func TestExecDweAction_SilentAtOff(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelOff)
	defer trace.Configure(nil, trace.LevelOff)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}}
	_ = execDweAction(ctx, config.Action{Type: "dwe", Cmd: "deploy run"}, actx)

	if buf.Len() != 0 {
		t.Fatalf("dwe echo leaked at LevelOff: %q", buf.String())
	}
}

// TestExecCommandAction_EchoesCommand asserts execCommandAction echoes the
// resolved user command (`dwe <id>`) at Verbose and is silent at LevelOff.
func TestExecCommandAction_EchoesCommand(t *testing.T) {
	cases := []struct {
		name     string
		level    trace.Level
		wantEcho bool
	}{
		{"verbose", trace.LevelVerbose, true},
		{"off", trace.LevelOff, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			trace.Configure(&buf, tc.level)
			defer trace.Configure(nil, trace.LevelOff)

			reg := usercommands.NewEmptyRegistry()
			reg.AddCommandForTest(&usercommands.CommandDef{
				ID:   "noop.cmd",
				Type: usercommands.CommandTypeShell,
				Cmd:  "true",
			})
			actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}, Reg: reg}
			if err := execCommandAction(context.Background(), config.Action{Type: "command", Cmd: "noop.cmd"}, actx); err != nil {
				t.Fatalf("execCommandAction: %v", err)
			}

			gotEcho := strings.Contains(buf.String(), "$ dwe noop.cmd")
			if gotEcho != tc.wantEcho {
				t.Fatalf("level=%v: echo present=%v, want %v (buf=%q)", tc.level, gotEcho, tc.wantEcho, buf.String())
			}
		})
	}
}

// TestExecCommandAction_NoEchoWhenUnresolved asserts that an unknown command
// errors before any echo (the echo is placed after successful resolution).
func TestExecCommandAction_NoEchoWhenUnresolved(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	reg := usercommands.NewEmptyRegistry()
	actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}, Reg: reg}
	if err := execCommandAction(context.Background(), config.Action{Type: "command", Cmd: "missing.cmd"}, actx); err == nil {
		t.Fatal("expected error for unresolved command")
	}
	if buf.Len() != 0 {
		t.Fatalf("echoed before resolution succeeded: %q", buf.String())
	}
}

// TestParallelGroup_TraceAttributesEchoToSubStepLog exercises Task 3 routing
// end-to-end: in a parallel group at Verbose, each sub-step's command echo lands
// in its own sub-step log (via the ctx-attached writerLinePrinter) and never in
// a sibling's.
func TestParallelGroup_TraceAttributesEchoToSubStepLog(t *testing.T) {
	trace.Configure(nil, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	tmp := t.TempDir()
	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: "echo alpha-out"},
		{Name: "beta", Type: "shell", Cmd: "echo beta-out"},
	})

	opts := RunOptions{
		Steps:       []ResolvedStep{group},
		Reporter:    rep,
		Name:        "deploy",
		Config:      &config.DweConfig{Raw: map[string]any{}},
		WorkDir:     tmp,
		LogWriter:   &syncBuf{},
		Recorder:    &mockRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alpha := readFile(t, filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "alpha.log"))
	beta := readFile(t, filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "beta.log"))

	if !strings.Contains(alpha, "$ sh -c 'echo alpha-out'") {
		t.Errorf("alpha.log missing its own command echo: %q", alpha)
	}
	if strings.Contains(alpha, "echo beta-out") {
		t.Errorf("alpha.log leaked sibling command echo: %q", alpha)
	}
	if !strings.Contains(beta, "$ sh -c 'echo beta-out'") {
		t.Errorf("beta.log missing its own command echo: %q", beta)
	}
	if strings.Contains(beta, "echo alpha-out") {
		t.Errorf("beta.log leaked sibling command echo: %q", beta)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

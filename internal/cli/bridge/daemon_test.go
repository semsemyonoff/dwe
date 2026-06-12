package bridge

import (
	"bytes"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	corebridge "github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/shared/lock"

	"github.com/spf13/cobra"
)

// execBridge runs the bridge subtree with args and returns the combined
// output and execution error.
func execBridge(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewCmd("advanced", &cmdctx.RootFlags{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestDaemonCmdIsHidden(t *testing.T) {
	cmd := NewCmd("advanced", &cmdctx.RootFlags{})
	var daemon *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "daemon" {
			daemon = sub
		}
	}
	if daemon == nil {
		t.Fatal("bridge subtree has no daemon subcommand")
	}
	if !daemon.Hidden {
		t.Error("bridge daemon must be hidden (internal entry)")
	}
}

func TestDaemonCmdRequiresProjectRoot(t *testing.T) {
	_, err := execBridge(t, "daemon")
	if err == nil || !strings.Contains(err.Error(), "project-root") {
		t.Fatalf("err = %v, want missing --project-root", err)
	}
}

func TestDaemonCmdRejectsRelativeProjectRoot(t *testing.T) {
	_, err := execBridge(t, "daemon", "--project-root", "relative/path")
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v, want absolute-path error", err)
	}
}

func TestDaemonCmdExitsCleanlyWhenAlreadyRunning(t *testing.T) {
	// The pidfile flock is the single-instance guarantee (design D6): with
	// it held, the spawned-twice daemon must exit 0 without touching the
	// listeners or loading the project.
	root := t.TempDir()
	bridgeDir := corebridge.DefaultBridgeDir(root)
	held, err := lock.Acquire(corebridge.PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	out, err := execBridge(t, "daemon", "--project-root", root)
	if err != nil {
		t.Fatalf("err = %v, want clean exit when daemon already runs", err)
	}
	if !strings.Contains(out, "already running") {
		t.Errorf("output %q missing the already-running notice", out)
	}
}

func TestDaemonCmdFailsWithoutProjectConfig(t *testing.T) {
	// No workspace.yml under --project-root: the daemon must fail before
	// binding anything (its auto-stop watcher needs the project identity).
	root := t.TempDir()
	_, err := execBridge(t, "daemon", "--project-root", root)
	if err == nil || !strings.Contains(err.Error(), "loading config") {
		t.Fatalf("err = %v, want config load failure", err)
	}
}

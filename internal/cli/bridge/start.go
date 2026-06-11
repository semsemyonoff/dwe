package bridge

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	corebridge "github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"

	"github.com/spf13/cobra"
)

// ensureDaemonFn is the same-package seam for the daemon ensure. Tests
// override it: the production corebridge.Ensure spawns a detached process
// via os.Executable() — re-executing the test binary (the documented
// recursion hazard).
var ensureDaemonFn = corebridge.Ensure

// bridgeStartJSON is the `bridge start --output json` payload.
type bridgeStartJSON struct {
	Started        bool `json:"started"`
	AlreadyRunning bool `json:"already_running"`
}

// newStartCmd builds `dwe bridge start`: the manual daemon ensure of the
// design D6 lifecycle table (lifecycle commands ensure it automatically).
func newStartCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the bridge daemon for this project",
		Long: `Start the host bridge daemon for this project (idempotent).

Lifecycle commands (deploy run, run, restart, services --apply) manage the
daemon automatically; this command exists to revive a manually stopped daemon
without touching the stack.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, flags)
		},
	}
}

func runStart(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}
	if !corebridge.AnyBridgeEnabled(cfg) {
		return cmdctx.Err("bridge_not_enabled", "no enabled service has the host bridge enabled").
			WithHint("set bridge.enabled: true in workspace/services/<name>/service.yml (type: app services default to enabled)")
	}
	started, err := ensureDaemonFn(corebridge.EnsureConfig{ProjectRoot: flags.ProjectRoot()})
	if err != nil {
		return cmdctx.ErrWrap("bridge_start_failed", err)
	}
	data := bridgeStartJSON{Started: started, AlreadyRunning: !started}
	return cmdctx.WriteData(flags, cmd, data, func(d bridgeStartJSON) string {
		if d.Started {
			return "bridge daemon started"
		}
		return "bridge daemon already running"
	})
}

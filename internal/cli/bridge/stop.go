package bridge

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	corebridge "github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"

	"github.com/spf13/cobra"
)

// stopDaemonFn is the same-package seam for the SIGTERM-by-pidfile stop —
// in tests the pidfile flock holder is the test process itself, and a real
// SIGTERM would kill the run.
var stopDaemonFn = corebridge.StopDaemon

// bridgeStopJSON is the `bridge stop --output json` payload.
type bridgeStopJSON struct {
	Signaled      bool `json:"signaled"`
	BridgeEnabled bool `json:"bridge_enabled"`
}

// newStopCmd builds `dwe bridge stop`: SIGTERM to the daemon by pidfile
// (design D6); a clean no-op when no daemon is running.
func newStopCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the bridge daemon for this project",
		Long: `Stop the host bridge daemon for this project (SIGTERM; idempotent).

The daemon also stops automatically with the stack (whole-stack stop / reset)
and once zero project containers remain. Note: in-container dwe commands stop
working until the daemon is started again.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd, flags)
		},
	}
}

func runStop(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}
	signaled, err := stopDaemonFn(corebridge.DefaultBridgeDir(flags.ProjectRoot()))
	if err != nil {
		return cmdctx.ErrWrap("bridge_stop_failed", err)
	}
	data := bridgeStopJSON{Signaled: signaled, BridgeEnabled: corebridge.AnyBridgeEnabled(cfg)}
	return cmdctx.WriteData(flags, cmd, data, renderStopText)
}

func renderStopText(d bridgeStopJSON) string {
	switch {
	case d.Signaled:
		return "bridge daemon signaled to stop"
	case !d.BridgeEnabled:
		return "bridge daemon is not running (no enabled service has the host bridge enabled)"
	default:
		return "bridge daemon is not running"
	}
}

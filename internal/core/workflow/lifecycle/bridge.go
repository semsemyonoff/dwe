package lifecycle

import (
	"github.com/semsemyonoff/dwe/internal/core/bridge"
)

// BridgePrepareFunc is a package-level seam (same pattern as PreflightFunc)
// for the bridge prepare hook: overlay regenerate-or-delete, shim
// materialization, and daemon ensure/cycle (design D8/D6). RunRun invokes it
// after the deployment gate and post-pull reload, before the phases run
// compose up. Tests inject a recorder so no daemon is spawned.
var BridgePrepareFunc = bridge.Prepare

// BridgeStopDaemonFunc is the seam for stopping the bridge daemon on a
// whole-stack stop (design D6). Per-service stop never touches it.
var BridgeStopDaemonFunc = bridge.StopDaemon

package lifecycle

import (
	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/execution/preflight"
)

// preflightRun is the same-package test seam for reset.go. Tests in
// package lifecycle override it to a no-op so the cobra command path can be
// exercised without docker/git probes.
var preflightRun = preflight.Run

// bridgeStopDaemonFn is the same-package seam for stopping the bridge daemon
// on a whole-stack `reset run` (design D6). The per-service variant
// (`reset run --service`) never touches the daemon. Whole-stack `stop` goes
// through the workflow package's own seam instead (lifecyclepkg.RunStop).
var bridgeStopDaemonFn = bridge.StopDaemon

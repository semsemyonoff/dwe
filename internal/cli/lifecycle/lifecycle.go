package lifecycle

import (
	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/execution/preflight"
)

// preflightRun is the same-package test seam for every preflight call in this
// package (reset.go and stop.go). Tests override it to a no-op so a cobra
// command path can be exercised without docker/git probes: the env probes shell
// out to `docker` under a 5s deadline, so on a loaded CI runner a real probe
// can be killed and fail preflight, and a test asserting anything that happens
// AFTER preflight (locks, prompt-cache invalidation) then fails for an unrelated
// reason. Call preflight.Run directly only where there is deliberately no seam.
var preflightRun = preflight.Run

// bridgeStopDaemonFn is the same-package seam for stopping the bridge daemon
// on a whole-stack `reset run` (design D6). The per-service variant
// (`reset run --service`) never touches the daemon. Whole-stack `stop` goes
// through the workflow package's own seam instead (lifecyclepkg.RunStop).
var bridgeStopDaemonFn = bridge.StopDaemon

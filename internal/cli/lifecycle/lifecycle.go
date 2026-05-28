package lifecycle

import "devbox-cli/internal/core/execution/preflight"

// preflightRun is the same-package test seam for reset.go. Tests in
// package lifecycle override it to a no-op so the cobra command path can be
// exercised without docker/git probes.
var preflightRun = preflight.Run

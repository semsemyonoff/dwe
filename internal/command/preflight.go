package command

import "devbox-cli/internal/preflight"

// preflightRun is the same-package test seam for reset.go. Tests in
// package command override it to a no-op so the cobra command path can be
// exercised without docker/git probes.
var preflightRun = preflight.Run

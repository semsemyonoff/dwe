package deploy

import "github.com/semsemyonoff/dwe/internal/cli/cmdctx"

// newNotifier is the same-package test seam. Production calls go through
// cmdctx.NewNotifier; tests in package deploy override newNotifier to swap
// in a recording fake without mutating the cmdctx-level var (which would
// be cross-package mutation).
var newNotifier = cmdctx.NewNotifier

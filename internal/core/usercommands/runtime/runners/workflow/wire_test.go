// Package workflow_test (external) pulls in the root runtime package solely
// for its side-effecting init() that wires the workflow callback seams
// (RunCommandFn, BuildRunContextFn, ComputeFilePathsProbeFn). Without this
// blank import the workflow test binary would build without the root pkg in
// scope, leaving the seams nil and causing every sub-step dispatch to panic.
//
// External test package avoids the would-be cycle (root runtime imports
// workflow); test binaries build as a separate compilation unit.
package workflow_test

import (
	_ "devbox-cli/internal/core/usercommands/runtime"
)

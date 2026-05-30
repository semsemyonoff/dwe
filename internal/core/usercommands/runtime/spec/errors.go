package spec

import "errors"

// ErrConfirmInsideParallel is returned when an interactive confirmation is
// reached inside a parallel group. Preflight catches direct cases; this
// sentinel catches transitive cases (workflow containing a confirm step or
// referencing a confirmation: true command from within a parallel sub-step).
//
// Defined in spec/ so every runner subpackage (and the root runtime package's
// confirmation helper) can return / wrap this sentinel without introducing a
// cycle through the factory in runtime/runner.go.
var ErrConfirmInsideParallel = errors.New("interactive confirmation is not allowed inside a parallel group")

// ErrWorkflowNestedParallel is returned when a workflow containing a
// `parallel:` block is invoked from another parallel context (pipeline or
// workflow). Only one LiveBlock owner is allowed per terminal.
//
// Defined in spec/ so external callers (`internal/core/usercommands`,
// pipeline tests) can keep importing it via the root runtime alias without
// pulling in the workflow runner subpackage directly.
var ErrWorkflowNestedParallel = errors.New("nested workflow parallel block is not supported in v1")

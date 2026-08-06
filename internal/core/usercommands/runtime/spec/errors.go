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

// ErrArgvAppendEmpty is the skip signal for a command whose argv_append_from
// expression produced no items. The expression succeeded — there is simply
// nothing to process — so running the declared argv anyway would change the
// command's meaning (`ruff check` with an empty file list lints the whole
// tree, the opposite of the intent).
//
// Contract, defined once here because "skip with a message" is not a
// mechanism on its own:
//   - The runner returns it from Run/BuildCommand instead of executing.
//   - runtime.RunCommand translates it into a stderr note and a nil error, so
//     the process exits 0 and a pipeline `type: command` step Finishes rather
//     than failing. That also means the step journals as success — a step whose
//     list can become non-empty later needs a files_gate or check:.
//   - messages.success is NOT emitted: nothing succeeded, nothing ran.
//   - the desktop notification is suppressed, matching the declined-confirmation
//     precedent — the command did not do the work it would notify about.
//   - `--output json` is unaffected: command execution streams the child's
//     output and has no JSON envelope to carry a skip state.
//
// Defined in spec/ so runner subpackages can return it and the root runtime
// package can recognise it without an import cycle.
var ErrArgvAppendEmpty = errors.New("argv_append_from produced no items")

// ErrWorkflowNestedParallel is returned when a workflow containing a
// `parallel:` block is invoked from another parallel context (pipeline or
// workflow). Only one LiveBlock owner is allowed per terminal.
//
// Defined in spec/ so external callers (`internal/core/usercommands`,
// pipeline tests) can keep importing it via the root runtime alias without
// pulling in the workflow runner subpackage directly.
var ErrWorkflowNestedParallel = errors.New("nested workflow parallel block is not supported in v1")

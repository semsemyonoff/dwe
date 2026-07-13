package pipeline

import (
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// StepForcesRun reports whether a resolved step forces execution despite a
// matching deployment hash — the single "always re-run" lever consulted by
// deploy's already-up-to-date early gate and its per-step skip decider (the
// hasCheck → Run branch in journal.Decide).
//
// True for:
//   - steps with a check: directive (the check must re-validate idempotency);
//   - predicate-body builtin steps (a predicate used as a step body is an
//     assertion and must re-assert on every deploy);
//   - parallel groups containing such a sub-step (one level — deeper nesting
//     is schema-rejected).
//
// files_gate and when: are deliberately outside this predicate: files_gate
// keeps its own journal-bypass handling at the call sites, and when: stays a
// pure conditional (a conditional assertion stays conditional).
func StepForcesRun(rs ResolvedStep) bool {
	if stepBodyForcesRun(rs.Step) {
		return true
	}
	return rs.Parallel != nil && slices.ContainsFunc(rs.Parallel.Steps, StepForcesRun)
}

// stepBodyForcesRun classifies a single leaf step. Predicate-body detection
// requires Type == "builtin" — a step of another type whose command text
// happens to match a builtin name (e.g. a shell step running "shell") must
// not force execution.
func stepBodyForcesRun(step config.DeployStep) bool {
	if step.Check != nil {
		return true
	}
	if step.Type != "builtin" {
		return false
	}
	kind, ok := builtin.KindOf(step.Cmd)
	return ok && kind == builtin.KindPredicate
}

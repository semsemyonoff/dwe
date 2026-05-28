package journal

// Decision is the result of evaluating whether a step should run or skip.
type Decision int

// Decision constants.
const (
	Run Decision = iota
	Skip
)

// String returns a human-readable representation of the decision.
func (d Decision) String() string {
	switch d {
	case Run:
		return "run"
	case Skip:
		return "skip"
	default:
		return "unknown"
	}
}

// Decide returns whether a step should Run or Skip based on its previous state,
// current action hash, and whether it has a check.
//
// The decision table is:
//   - prev absent → Run
//   - prev.Status=ok + action_hash matches + no check → Skip
//   - prev.Status=ok + action_hash matches + has check → Run (check still runs to re-validate)
//   - prev.Status=ok + action_hash differs → Run (state is stale)
//   - prev.Status in {failed,partial,in_progress} → Run (resume from error)
//
// Note: when: is intentionally NOT a parameter. The caller evaluates when:
// before consulting Decide; if when: is false, the step skips via the existing
// path and never reaches Decide. This ensures when: is always re-evaluated
// on every deploy, regardless of state.
func Decide(prev *StepState, currentActionHash string, hasCheck bool) Decision {
	// No previous state → run
	if prev == nil {
		return Run
	}

	// Previous run failed or is in progress → run (resume)
	if prev.Status != StatusOk {
		return Run
	}

	// Previous status is ok, but action hash differs → run (state is stale)
	if prev.ActionHash != currentActionHash {
		return Run
	}

	// Previous status is ok and action hash matches.
	// If step has a check, must run so the check re-validates idempotency.
	if hasCheck {
		return Run
	}

	// Previous status is ok, action hash matches, and no check → skip.
	return Skip
}

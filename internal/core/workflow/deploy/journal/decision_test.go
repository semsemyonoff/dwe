package journal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDecide_PreAbsent tests that a missing previous state always results in Run.
func TestDecide_PreAbsent(t *testing.T) {
	cases := []struct {
		name             string
		currentHash      string
		hasCheck         bool
		expectedDecision Decision
	}{
		{"no check, no hash", "", false, Run},
		{"with check, no hash", "", true, Run},
		{"no check, with hash", "abc123", false, Run},
		{"with check, with hash", "abc123", true, Run},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := Decide(nil, tc.currentHash, tc.hasCheck)
			assert.Equal(t, tc.expectedDecision, decision)
		})
	}
}

// TestDecide_StatusOkHashMatchNoCheck tests the Skip case.
func TestDecide_StatusOkHashMatchNoCheck(t *testing.T) {
	prev := &StepState{
		Status:     StatusOk,
		ActionHash: "abc123",
	}

	decision := Decide(prev, "abc123", false)
	assert.Equal(t, Skip, decision)
}

// TestDecide_StatusOkHashMatchWithCheck tests that checks always run.
func TestDecide_StatusOkHashMatchWithCheck(t *testing.T) {
	prev := &StepState{
		Status:     StatusOk,
		ActionHash: "abc123",
	}

	decision := Decide(prev, "abc123", true)
	assert.Equal(t, Run, decision)
}

// TestDecide_StatusOkHashDiffers tests that a hash mismatch forces a Run.
func TestDecide_StatusOkHashDiffers(t *testing.T) {
	cases := []struct {
		name             string
		prevHash         string
		currentHash      string
		hasCheck         bool
		expectedDecision Decision
	}{
		{"no check, different hash", "abc123", "xyz789", false, Run},
		{"with check, different hash", "abc123", "xyz789", true, Run},
		{"empty previous hash", "", "xyz789", false, Run},
		{"empty current hash", "abc123", "", false, Run},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := &StepState{
				Status:     StatusOk,
				ActionHash: tc.prevHash,
			}

			decision := Decide(prev, tc.currentHash, tc.hasCheck)
			assert.Equal(t, tc.expectedDecision, decision)
		})
	}
}

// TestDecide_StatusOkBothEmptyHash tests the edge case where both hashes are empty.
// Empty hashes should match and behave like the match case.
func TestDecide_StatusOkBothEmptyHash(t *testing.T) {
	prev := &StepState{
		Status:     StatusOk,
		ActionHash: "",
	}

	// No check → Skip
	decision := Decide(prev, "", false)
	assert.Equal(t, Skip, decision)

	// With check → Run
	decision = Decide(prev, "", true)
	assert.Equal(t, Run, decision)
}

// TestDecide_StatusFailed tests that failed steps always run.
func TestDecide_StatusFailed(t *testing.T) {
	cases := []struct {
		name             string
		currentHash      string
		hasCheck         bool
		expectedDecision Decision
	}{
		{"no check, no hash", "", false, Run},
		{"with check, no hash", "", true, Run},
		{"no check, with hash", "abc123", false, Run},
		{"with check, with hash (same)", "abc123", true, Run},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := &StepState{
				Status:     StatusFailed,
				ActionHash: "abc123",
			}

			decision := Decide(prev, tc.currentHash, tc.hasCheck)
			assert.Equal(t, tc.expectedDecision, decision)
		})
	}
}

// TestDecide_StatusPartial tests that partial steps always run.
func TestDecide_StatusPartial(t *testing.T) {
	prev := &StepState{
		Status:     StatusPartial,
		ActionHash: "abc123",
	}

	decision := Decide(prev, "abc123", false)
	assert.Equal(t, Run, decision)

	decision = Decide(prev, "abc123", true)
	assert.Equal(t, Run, decision)
}

// TestDecide_StatusInProgress tests that in-progress steps always run.
func TestDecide_StatusInProgress(t *testing.T) {
	prev := &StepState{
		Status:     StatusInProgress,
		ActionHash: "abc123",
	}

	decision := Decide(prev, "abc123", false)
	assert.Equal(t, Run, decision)

	decision = Decide(prev, "abc123", true)
	assert.Equal(t, Run, decision)
}

// TestDecide_DecisionString tests the String method.
func TestDecide_DecisionString(t *testing.T) {
	cases := []struct {
		decision Decision
		expected string
	}{
		{Run, "run"},
		{Skip, "skip"},
		{Decision(99), "unknown"},
	}

	for _, tc := range cases {
		assert.Equal(t, tc.expected, tc.decision.String())
	}
}

// TestDecide_ExhaustiveTable is a comprehensive table-driven test covering
// all rows of the decision table plus edge cases.
func TestDecide_ExhaustiveTable(t *testing.T) {
	cases := []struct {
		name              string
		prev              *StepState
		currentActionHash string
		hasCheck          bool
		expectedDecision  Decision
		description       string
	}{
		// Absent previous state
		{
			"absent-no-check-no-hash",
			nil,
			"",
			false,
			Run,
			"prev absent → Run",
		},
		{
			"absent-no-check-with-hash",
			nil,
			"abc123",
			false,
			Run,
			"prev absent → Run",
		},
		{
			"absent-with-check-no-hash",
			nil,
			"",
			true,
			Run,
			"prev absent → Run",
		},
		{
			"absent-with-check-with-hash",
			nil,
			"abc123",
			true,
			Run,
			"prev absent → Run",
		},

		// Status ok, hashes match, no check → Skip
		{
			"ok-match-no-check",
			&StepState{Status: StatusOk, ActionHash: "abc123"},
			"abc123",
			false,
			Skip,
			"prev.Status=ok + action_hash matches + no check → Skip",
		},

		// Status ok, hashes match, with check → Run
		{
			"ok-match-with-check",
			&StepState{Status: StatusOk, ActionHash: "abc123"},
			"abc123",
			true,
			Run,
			"prev.Status=ok + action_hash matches + has check → Run (re-validate)",
		},

		// Status ok, hashes differ → Run
		{
			"ok-differ-no-check",
			&StepState{Status: StatusOk, ActionHash: "abc123"},
			"xyz789",
			false,
			Run,
			"prev.Status=ok + action_hash differs → Run (state stale)",
		},
		{
			"ok-differ-with-check",
			&StepState{Status: StatusOk, ActionHash: "abc123"},
			"xyz789",
			true,
			Run,
			"prev.Status=ok + action_hash differs → Run (state stale)",
		},

		// Status failed → Run
		{
			"failed-match-no-check",
			&StepState{Status: StatusFailed, ActionHash: "abc123"},
			"abc123",
			false,
			Run,
			"prev.Status=failed → Run (resume)",
		},
		{
			"failed-differ-no-check",
			&StepState{Status: StatusFailed, ActionHash: "abc123"},
			"xyz789",
			false,
			Run,
			"prev.Status=failed → Run (resume)",
		},

		// Status partial → Run
		{
			"partial-match-no-check",
			&StepState{Status: StatusPartial, ActionHash: "abc123"},
			"abc123",
			false,
			Run,
			"prev.Status=partial → Run (resume)",
		},

		// Status in_progress → Run
		{
			"in-progress-match-no-check",
			&StepState{Status: StatusInProgress, ActionHash: "abc123"},
			"abc123",
			false,
			Run,
			"prev.Status=in_progress → Run (resume)",
		},

		// Edge cases
		{
			"empty-hashes-match-no-check",
			&StepState{Status: StatusOk, ActionHash: ""},
			"",
			false,
			Skip,
			"empty hashes match → Skip (if no check)",
		},
		{
			"empty-hashes-match-with-check",
			&StepState{Status: StatusOk, ActionHash: ""},
			"",
			true,
			Run,
			"empty hashes match + check → Run",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := Decide(tc.prev, tc.currentActionHash, tc.hasCheck)
			assert.Equal(t, tc.expectedDecision, decision, tc.description)
		})
	}
}

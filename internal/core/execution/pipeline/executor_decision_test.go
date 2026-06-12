package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// runForDecision runs the supplied steps with the given trace level and returns
// the captured diagnostic output. The SkipDecider always forces Run unless
// overridden via the decider arg (nil → Run).
func runForDecision(t *testing.T, lvl trace.Level, steps []ResolvedStep, decider SkipDecider) string {
	t.Helper()
	var buf bytes.Buffer
	trace.Configure(&buf, lvl)
	t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

	if decider == nil {
		decider = func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run }
	}
	opts := RunOptions{
		Steps:       steps,
		Reporter:    &mockReporter{},
		Name:        "test",
		Config:      &config.DweConfig{Raw: map[string]any{}},
		WorkDir:     t.TempDir(),
		Recorder:    &mockRecorder{},
		SkipDecider: decider,
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("RunWithOptions: %v", err)
	}
	return buf.String()
}

// TestDecision_StepWhenFalse asserts a step skipped by a false step-level when:
// emits a Decision at Verbose and is silent at LevelOff.
func TestDecision_StepWhenFalse(t *testing.T) {
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{{
		Phase:       phase,
		Step:        noopStep("guarded"),
		RuntimeWhen: &condition.Condition{Type: condition.TypeShell, Cmd: "exit 1"},
	}}

	if got := runForDecision(t, trace.LevelVerbose, steps, nil); !strings.Contains(got, "skip step p/guarded — when false") {
		t.Fatalf("verbose: missing step when decision: %q", got)
	}
	if got := runForDecision(t, trace.LevelOff, steps, nil); got != "" {
		t.Fatalf("off: decision leaked: %q", got)
	}
}

// TestDecision_PhaseWhenFalse asserts a false phase-level when: emits both the
// phase skip and the per-step phase-when skip decisions at Verbose.
func TestDecision_PhaseWhenFalse(t *testing.T) {
	phase := config.DeployPhase{Name: "p"}
	cond := &condition.Condition{Type: condition.TypeShell, Cmd: "exit 1"}
	steps := []ResolvedStep{
		{Phase: phase, Step: noopStep("a"), PhaseWhen: cond},
		{Phase: phase, Step: noopStep("b"), PhaseWhen: cond},
	}

	got := runForDecision(t, trace.LevelVerbose, steps, nil)
	if !strings.Contains(got, "skip phase p — when false") {
		t.Errorf("missing phase skip decision: %q", got)
	}
	if !strings.Contains(got, "skip step p/a — phase when false") {
		t.Errorf("missing step phase-when decision for a: %q", got)
	}
	if !strings.Contains(got, "skip step p/b — phase when false") {
		t.Errorf("missing step phase-when decision for b: %q", got)
	}

	if got := runForDecision(t, trace.LevelOff, steps, nil); got != "" {
		t.Fatalf("off: decision leaked: %q", got)
	}
}

// TestDecision_SkipDeciderStateSkip asserts a journal-driven skip emits the
// "state: already deployed" decision at Verbose, and that a Run decision is
// emitted when the decider says Run.
func TestDecision_SkipDeciderStateSkip(t *testing.T) {
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{{Phase: phase, Step: noopStep("only")}}

	skipAll := func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Skip }
	if got := runForDecision(t, trace.LevelVerbose, steps, skipAll); !strings.Contains(got, "skip step p/only — state: already deployed") {
		t.Fatalf("verbose: missing state-skip decision: %q", got)
	}

	runAll := func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run }
	if got := runForDecision(t, trace.LevelVerbose, steps, runAll); !strings.Contains(got, "run step p/only") {
		t.Fatalf("verbose: missing run decision: %q", got)
	}

	if got := runForDecision(t, trace.LevelOff, steps, skipAll); got != "" {
		t.Fatalf("off: decision leaked: %q", got)
	}
}

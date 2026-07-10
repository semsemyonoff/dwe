package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// TestExecuteStepBody_ShellTimeout_FailsWithTimeoutMessage proves a shell step
// whose body outlives its per-step timeout is aborted (not waited out) and
// fails with the timeout message.
func TestExecuteStepBody_ShellTimeout_FailsWithTimeoutMessage(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	phase := config.DeployPhase{Name: "p"}
	step := config.DeployStep{Name: "slow", Type: "shell", Cmd: "sleep 5"}
	rs := ResolvedStep{Phase: phase, Step: step, Timeout: 100 * time.Millisecond}

	start := time.Now()
	err := RunWithOptions(newRunOpts(t, rep, rec, []ResolvedStep{rs}))
	elapsed := time.Since(start)

	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("step took %s, want well under the 5s sleep — the subprocess should be terminated on timeout, not waited out", elapsed)
	}

	failed := findEvent(rep.events, "FailStep")
	if failed == nil {
		t.Fatal("expected a FailStep event")
	}
	wantMsg := `step "slow" timed out after 100ms`
	if failed.err == nil || failed.err.Error() != wantMsg {
		t.Errorf("FailStep error = %v, want %q", failed.err, wantMsg)
	}
}

// TestExecuteStepBody_BuiltinTimeout_HTTPCheckRespectsCtx proves the timeout
// bounds a ctx-aware builtin body (not just shell/dwe subprocesses): http_check
// retries against a server that never returns the expected status, and the
// step's own deadline interrupts the inter-attempt sleep via ctx.Done().
func TestExecuteStepBody_BuiltinTimeout_HTTPCheckRespectsCtx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	rep := &mockReporter{}
	rec := &mockRecorder{}
	phase := config.DeployPhase{Name: "p"}
	step := config.DeployStep{
		Name: "wait-http",
		Type: "builtin",
		Cmd:  "http_check",
		With: map[string]any{
			"url":      ts.URL,
			"retries":  50,
			"interval": "400ms",
		},
	}
	rs := ResolvedStep{Phase: phase, Step: step, Timeout: 150 * time.Millisecond}

	start := time.Now()
	err := RunWithOptions(newRunOpts(t, rep, rec, []ResolvedStep{rs}))
	elapsed := time.Since(start)

	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("step took %s, want well under the retry budget — ctx should cancel http_check", elapsed)
	}

	failed := findEvent(rep.events, "FailStep")
	if failed == nil {
		t.Fatal("expected a FailStep event")
	}
	wantMsg := `step "wait-http" timed out after 150ms`
	if failed.err == nil || failed.err.Error() != wantMsg {
		t.Errorf("FailStep error = %v, want %q", failed.err, wantMsg)
	}
}

// TestExecuteStepBody_TimeoutZeroOrAbsent_RunsUnchanged proves the opt-in
// contract: an absent (zero) timeout, and a generously large one, never
// interfere with a normal, fast-completing step.
func TestExecuteStepBody_TimeoutZeroOrAbsent_RunsUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"absent (zero)", 0},
		{"generously large", 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &mockReporter{}
			rec := &mockRecorder{}
			phase := config.DeployPhase{Name: "p"}
			step := config.DeployStep{Name: "quick", Type: "shell", Cmd: "sleep 0.05"}
			rs := ResolvedStep{Phase: phase, Step: step, Timeout: tt.timeout}

			if err := RunWithOptions(newRunOpts(t, rep, rec, []ResolvedStep{rs})); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			last := rep.events[len(rep.events)-1]
			if last.kind != "FinishPipeline" || !last.success {
				t.Errorf("expected FinishPipeline success=true, got kind=%s success=%v", last.kind, last.success)
			}
		})
	}
}

// TestParallelGroup_OneSubStepTimesOut_SiblingCompletes proves each parallel
// sub-step honors its own Timeout independently: a timing-out sub-step fails
// the group without preventing its sibling from finishing.
func TestParallelGroup_OneSubStepTimesOut_SiblingCompletes(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", false, 0, []config.DeployStep{
		{Name: "slow", Type: "shell", Cmd: "sleep 5"},
		{Name: "fast", Type: "shell", Cmd: "true"},
	})
	group.Parallel.Steps[0].Timeout = 100 * time.Millisecond

	err := RunWithOptions(newRunOpts(t, rep, rec, []ResolvedStep{group}))
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent (group had a failing sub-step), got %v", err)
	}

	var failedSlow, finishedFast bool
	var failErr error
	for _, e := range rep.events {
		if e.kind == "FailStep" && e.stepAddr == "p/slow" {
			failedSlow = true
			failErr = e.err
		}
		if e.kind == "FinishStep" && e.stepAddr == "p/fast" {
			finishedFast = true
		}
	}
	if !failedSlow {
		t.Error("expected the slow sub-step to fail (timeout)")
	}
	if !finishedFast {
		t.Error("expected the fast sub-step to complete despite the sibling timing out")
	}
	wantMsg := `step "slow" timed out after 100ms`
	if failErr == nil || failErr.Error() != wantMsg {
		t.Errorf("FailStep error = %v, want %q", failErr, wantMsg)
	}
}

// TestExecuteStepBody_OuterCancel_NotReportedAsTimeout proves an outer context
// cancellation (e.g. Ctrl+C) is never mislabeled as a step timeout, even when
// the step declares a (much longer) positive Timeout.
func TestExecuteStepBody_OuterCancel_NotReportedAsTimeout(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	phase := config.DeployPhase{Name: "p"}
	step := config.DeployStep{Name: "slow", Type: "shell", Cmd: "sleep 5"}
	rs := ResolvedStep{Phase: phase, Step: step, Timeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	opts := newRunOpts(t, rep, rec, []ResolvedStep{rs})
	opts.Context = ctx

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := RunWithOptions(opts)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("step took %s, want prompt cancellation", elapsed)
	}

	failed := findEvent(rep.events, "FailStep")
	if failed == nil {
		t.Fatal("expected a FailStep event")
	}
	if failed.err == nil || strings.Contains(failed.err.Error(), "timed out after") {
		t.Errorf("FailStep error must not be mislabeled a timeout: %v", failed.err)
	}
}

// findEvent returns a pointer to the first event of the given kind, or nil.
func findEvent(events []reporterEvent, kind string) *reporterEvent {
	for i := range events {
		if events[i].kind == kind {
			return &events[i]
		}
	}
	return nil
}

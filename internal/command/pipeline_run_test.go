package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"devbox-cli/internal/config"
)

// --- mockReporter records all reporter events for assertion ---

type reporterEvent struct {
	kind     string
	phaseKey string
	phase    config.DeployPhase
	stepAddr string
	step     config.DeployStep
	index    int
	total    int
	reason   string
	err      error
	name     string
	total0   int
	success  bool
}

type mockReporter struct {
	events []reporterEvent
}

func (m *mockReporter) StartPipeline(name string, totalSteps int) {
	m.events = append(m.events, reporterEvent{kind: "StartPipeline", name: name, total0: totalSteps})
}
func (m *mockReporter) EnterPhase(phaseKey string, phase config.DeployPhase) {
	m.events = append(m.events, reporterEvent{kind: "EnterPhase", phaseKey: phaseKey, phase: phase})
}
func (m *mockReporter) SkipPhase(phaseKey string, phase config.DeployPhase, reason string) {
	m.events = append(m.events, reporterEvent{kind: "SkipPhase", phaseKey: phaseKey, phase: phase, reason: reason})
}
func (m *mockReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	m.events = append(m.events, reporterEvent{kind: "StartStep", stepAddr: stepAddr, step: step, index: index, total: total})
}
func (m *mockReporter) SkipStep(stepAddr string, step config.DeployStep, index int, total int, reason string) {
	m.events = append(m.events, reporterEvent{kind: "SkipStep", stepAddr: stepAddr, step: step, index: index, total: total, reason: reason})
}
func (m *mockReporter) FinishStep(stepAddr string, step config.DeployStep, index int, total int) {
	m.events = append(m.events, reporterEvent{kind: "FinishStep", stepAddr: stepAddr, step: step, index: index, total: total})
}
func (m *mockReporter) FailStep(stepAddr string, step config.DeployStep, index int, total int, err error) {
	m.events = append(m.events, reporterEvent{kind: "FailStep", stepAddr: stepAddr, step: step, index: index, total: total, err: err})
}
func (m *mockReporter) FinishPipeline(success bool) {
	m.events = append(m.events, reporterEvent{kind: "FinishPipeline", success: success})
}
func (m *mockReporter) SuspendForExec() {
	m.events = append(m.events, reporterEvent{kind: "SuspendForExec"})
}
func (m *mockReporter) ResumeAfterExec() {
	m.events = append(m.events, reporterEvent{kind: "ResumeAfterExec"})
}

// kindSeq returns the sequence of event kinds.
func (m *mockReporter) kindSeq() []string {
	kinds := make([]string, len(m.events))
	for i, e := range m.events {
		kinds[i] = e.kind
	}
	return kinds
}

// eventAt returns the event at index i, panicking if out of range.
func (m *mockReporter) eventAt(i int) reporterEvent {
	return m.events[i]
}

// --- helpers ---

// noopStep returns a step that runs a no-op shell command.
func noopStep(name string) config.DeployStep {
	return config.DeployStep{Name: name, Run: "true"}
}

// buildResolvedSteps creates resolved steps from phase+step pairs.
func buildResolvedSteps(phase config.DeployPhase, steps []config.DeployStep) []resolvedStep {
	result := make([]resolvedStep, len(steps))
	for i, s := range steps {
		result[i] = resolvedStep{phase: phase, step: s}
	}
	return result
}

// --- tests ---

func TestRunPipeline_EmptySteps(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	err := runPipeline(nil, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantKinds := []string{"StartPipeline", "FinishPipeline"}
	gotKinds := rep.kindSeq()
	if fmt.Sprint(gotKinds) != fmt.Sprint(wantKinds) {
		t.Errorf("event kinds: got %v, want %v", gotKinds, wantKinds)
	}
	if !rep.events[1].success {
		t.Error("FinishPipeline should be called with success=true")
	}
}

func TestRunPipeline_SingleStep_Success(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "init"}
	step := noopStep("setup")
	steps := buildResolvedSteps(phase, []config.DeployStep{step})

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantKinds := []string{"StartPipeline", "EnterPhase", "StartStep", "SuspendForExec", "ResumeAfterExec", "FinishStep", "FinishPipeline"}
	if fmt.Sprint(rep.kindSeq()) != fmt.Sprint(wantKinds) {
		t.Errorf("event kinds: got %v, want %v", rep.kindSeq(), wantKinds)
	}

	enterPhase := rep.eventAt(1)
	if enterPhase.phaseKey != "init" {
		t.Errorf("EnterPhase phaseKey = %q, want %q", enterPhase.phaseKey, "init")
	}

	startStep := rep.eventAt(2)
	if startStep.stepAddr != "init/setup" {
		t.Errorf("StartStep stepAddr = %q, want %q", startStep.stepAddr, "init/setup")
	}
	if startStep.index != 1 || startStep.total != 1 {
		t.Errorf("StartStep index/total = %d/%d, want 1/1", startStep.index, startStep.total)
	}

	finishStep := rep.eventAt(5)
	if finishStep.stepAddr != "init/setup" {
		t.Errorf("FinishStep stepAddr = %q, want %q", finishStep.stepAddr, "init/setup")
	}
}

func TestRunPipeline_MultipleSteps_CorrectIndexing(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := buildResolvedSteps(phase, []config.DeployStep{
		noopStep("a"),
		noopStep("b"),
		noopStep("c"),
	})

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect StartStep events and check indices.
	var starts []reporterEvent
	for _, e := range rep.events {
		if e.kind == "StartStep" {
			starts = append(starts, e)
		}
	}
	if len(starts) != 3 {
		t.Fatalf("want 3 StartStep events, got %d", len(starts))
	}
	for i, s := range starts {
		wantIndex := i + 1
		if s.index != wantIndex || s.total != 3 {
			t.Errorf("StartStep[%d]: index=%d total=%d, want index=%d total=3", i, s.index, s.total, wantIndex)
		}
	}
}

func TestRunPipeline_StepFailure_ReporterCalled(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "init"}
	steps := buildResolvedSteps(phase, []config.DeployStep{
		{Name: "fail", Run: "exit 1"},
	})

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}

	// FailStep must appear after SuspendForExec+ResumeAfterExec.
	kinds := rep.kindSeq()
	findKind := func(k string) int {
		for i, ev := range kinds {
			if ev == k {
				return i
			}
		}
		return -1
	}
	suspendIdx := findKind("SuspendForExec")
	resumeIdx := findKind("ResumeAfterExec")
	failIdx := findKind("FailStep")
	if suspendIdx == -1 || resumeIdx == -1 || failIdx == -1 {
		t.Fatalf("missing events: kinds=%v", kinds)
	}
	if suspendIdx >= resumeIdx || resumeIdx >= failIdx {
		t.Errorf("event order wrong: Suspend=%d Resume=%d Fail=%d", suspendIdx, resumeIdx, failIdx)
	}
	if rep.eventAt(failIdx).err == nil {
		t.Error("FailStep should carry non-nil error")
	}
	// FinishPipeline must be called with success=false after step failure.
	fpIdx := findKind("FinishPipeline")
	if fpIdx == -1 {
		t.Error("FinishPipeline should be called even on failure (deferred cleanup)")
	} else if rep.eventAt(fpIdx).success {
		t.Error("FinishPipeline should be called with success=false on step failure")
	}
}

func TestRunPipeline_StepSkippedByRuntimeWhen(t *testing.T) {
	// Use dir-empty on a non-empty directory so the when evaluates to false → skip.
	workDir := t.TempDir()
	// Write a file so the dir is NOT empty.
	if err := os.WriteFile(filepath.Join(workDir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}

	// "dir-empty <workDir>" → false because workDir is not empty → step is skipped.
	steps := []resolvedStep{
		{phase: phase, step: noopStep("do-thing"), runtimeWhen: "dir-empty " + workDir},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var skipEvents []reporterEvent
	for _, e := range rep.events {
		if e.kind == "SkipStep" {
			skipEvents = append(skipEvents, e)
		}
	}
	if len(skipEvents) != 1 {
		t.Fatalf("want 1 SkipStep event, got %d (kinds: %v)", len(skipEvents), rep.kindSeq())
	}
	if skipEvents[0].reason != "when: dir-empty "+workDir {
		t.Errorf("SkipStep reason = %q, want 'when: dir-empty %s'", skipEvents[0].reason, workDir)
	}
}

func TestRunPipeline_PhaseSkipped_AllStepsSkipped(t *testing.T) {
	// dir-empty on a non-empty dir → phaseWhen evaluates to false → whole phase skipped.
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "cond-phase"}

	phaseWhenExpr := "dir-empty " + workDir
	steps := []resolvedStep{
		{phase: phase, step: noopStep("step-a"), phaseWhen: phaseWhenExpr},
		{phase: phase, step: noopStep("step-b"), phaseWhen: phaseWhenExpr},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// EnterPhase → SkipPhase → StartStep × 2 → SkipStep × 2 → FinishPipeline
	kinds := rep.kindSeq()
	skipPhaseCount := 0
	skipStepCount := 0
	for _, k := range kinds {
		if k == "SkipPhase" {
			skipPhaseCount++
		}
		if k == "SkipStep" {
			skipStepCount++
		}
	}
	if skipPhaseCount != 1 {
		t.Errorf("want 1 SkipPhase, got %d (kinds: %v)", skipPhaseCount, kinds)
	}
	if skipStepCount != 2 {
		t.Errorf("want 2 SkipStep events, got %d (kinds: %v)", skipStepCount, kinds)
	}
	// Verify SkipStep reasons include "phase when:"
	for _, e := range rep.events {
		if e.kind == "SkipStep" && !containsStr(e.reason, "phase when:") {
			t.Errorf("SkipStep reason %q should contain 'phase when:'", e.reason)
		}
	}
}

func TestRunPipeline_PostStepHook_CalledAfterSuccess(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "env"}
	steps := buildResolvedSteps(phase, []config.DeployStep{noopStep("render-env")})

	hookCalled := false
	hooks := map[string]func() error{
		"render-env": func() error {
			hookCalled = true
			return nil
		},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, hooks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hookCalled {
		t.Error("post-step hook was not called after successful step")
	}
}

func TestRunPipeline_PostStepHook_NotCalledOnFailure(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "env"}
	steps := buildResolvedSteps(phase, []config.DeployStep{{Name: "render-env", Run: "exit 1"}})

	hookCalled := false
	hooks := map[string]func() error{
		"render-env": func() error {
			hookCalled = true
			return nil
		},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, hooks)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}
	if hookCalled {
		t.Error("post-step hook should not be called after step failure")
	}
}

func TestRunPipeline_ServiceStep_PhaseKeyIncludesService(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []resolvedStep{
		{phase: phase, step: noopStep("migrate"), service: "main"},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var enterPhase *reporterEvent
	for i, e := range rep.events {
		if e.kind == "EnterPhase" {
			ev := rep.events[i]
			enterPhase = &ev
		}
	}
	if enterPhase == nil {
		t.Fatal("EnterPhase event not found")
	}
	if enterPhase.phaseKey != "main/setup" {
		t.Errorf("EnterPhase phaseKey = %q, want %q", enterPhase.phaseKey, "main/setup")
	}

	var startStep *reporterEvent
	for i, e := range rep.events {
		if e.kind == "StartStep" {
			ev := rep.events[i]
			startStep = &ev
		}
	}
	if startStep == nil {
		t.Fatal("StartStep event not found")
	}
	if startStep.stepAddr != "main/setup/migrate" {
		t.Errorf("StartStep stepAddr = %q, want %q", startStep.stepAddr, "main/setup/migrate")
	}
}

func TestRunPipeline_SuspendResumeWrapsExec(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "p"}
	steps := buildResolvedSteps(phase, []config.DeployStep{noopStep("s")})

	if err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil); err != nil {
		t.Fatal(err)
	}

	kinds := rep.kindSeq()
	suspendIdx, resumeIdx, startIdx, finishIdx := -1, -1, -1, -1
	for i, k := range kinds {
		switch k {
		case "StartStep":
			startIdx = i
		case "SuspendForExec":
			suspendIdx = i
		case "ResumeAfterExec":
			resumeIdx = i
		case "FinishStep":
			finishIdx = i
		}
	}
	if startIdx == -1 || suspendIdx == -1 || resumeIdx == -1 || finishIdx == -1 {
		t.Fatalf("missing events: %v", kinds)
	}
	if startIdx >= suspendIdx || suspendIdx >= resumeIdx || resumeIdx >= finishIdx {
		t.Errorf("event order wrong: Start=%d Suspend=%d Resume=%d Finish=%d", startIdx, suspendIdx, resumeIdx, finishIdx)
	}
}

func TestRunPipeline_PostDeploySkippedOnFailure(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phase1 := config.DeployPhase{Name: "setup"}
	phase2 := config.DeployPhase{Name: "post-deploy"}

	steps := []resolvedStep{
		{phase: phase1, step: config.DeployStep{Name: "fail-step", Run: "exit 1"}},
		{phase: phase2, step: noopStep("notify")},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}

	// post-deploy phase must never be entered.
	for _, e := range rep.events {
		if e.kind == "EnterPhase" && e.phaseKey == "post-deploy" {
			t.Error("post-deploy phase was entered after a prior step failed")
		}
		if e.kind == "StartStep" && e.step.Name == "notify" {
			t.Error("post-deploy step was started after a prior step failed")
		}
	}

	// FailStep must be recorded for the failing step.
	failFound := false
	for _, e := range rep.events {
		if e.kind == "FailStep" && e.step.Name == "fail-step" {
			failFound = true
		}
	}
	if !failFound {
		t.Error("FailStep event not recorded for failing step")
	}
}

func TestRunPipeline_UntrackedPhase_ExcludedFromTotal(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	tracked := config.DeployPhase{Name: "setup", Untracked: false}
	untracked := config.DeployPhase{Name: "post-deploy", Untracked: true}

	steps := []resolvedStep{
		{phase: tracked, step: noopStep("a")},
		{phase: tracked, step: noopStep("b")},
		{phase: untracked, step: noopStep("info")},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// StartPipeline total must only count tracked steps (2, not 3).
	startPipeline := rep.events[0]
	if startPipeline.kind != "StartPipeline" {
		t.Fatalf("first event kind = %q, want StartPipeline", startPipeline.kind)
	}
	if startPipeline.total0 != 2 {
		t.Errorf("StartPipeline totalSteps = %d, want 2 (untracked phase excluded)", startPipeline.total0)
	}
}

func TestRunPipeline_UntrackedPhase_StepsReceiveZeroIndex(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	tracked := config.DeployPhase{Name: "setup", Untracked: false}
	untracked := config.DeployPhase{Name: "post-deploy", Untracked: true}

	steps := []resolvedStep{
		{phase: tracked, step: noopStep("a")},
		{phase: untracked, step: noopStep("info")},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect StartStep events.
	var startSteps []reporterEvent
	for _, e := range rep.events {
		if e.kind == "StartStep" {
			startSteps = append(startSteps, e)
		}
	}
	if len(startSteps) != 2 {
		t.Fatalf("want 2 StartStep events, got %d", len(startSteps))
	}

	// Tracked step: index=1, total=1.
	if startSteps[0].stepAddr != "setup/a" {
		t.Errorf("StartStep[0] addr = %q, want setup/a", startSteps[0].stepAddr)
	}
	if startSteps[0].index != 1 || startSteps[0].total != 1 {
		t.Errorf("tracked StartStep: index=%d total=%d, want 1/1", startSteps[0].index, startSteps[0].total)
	}

	// Untracked step: index=0, total=0.
	if startSteps[1].stepAddr != "post-deploy/info" {
		t.Errorf("StartStep[1] addr = %q, want post-deploy/info", startSteps[1].stepAddr)
	}
	if startSteps[1].index != 0 || startSteps[1].total != 0 {
		t.Errorf("untracked StartStep: index=%d total=%d, want 0/0", startSteps[1].index, startSteps[1].total)
	}
}

func TestRunPipeline_TrackedIndexContinuous(t *testing.T) {
	// Three tracked steps across two phases: indices should be 1, 2, 3.
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phase1 := config.DeployPhase{Name: "phase1", Untracked: false}
	phase2 := config.DeployPhase{Name: "phase2", Untracked: false}

	steps := []resolvedStep{
		{phase: phase1, step: noopStep("a")},
		{phase: phase1, step: noopStep("b")},
		{phase: phase2, step: noopStep("c")},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var starts []reporterEvent
	for _, e := range rep.events {
		if e.kind == "StartStep" {
			starts = append(starts, e)
		}
	}
	if len(starts) != 3 {
		t.Fatalf("want 3 StartStep events, got %d", len(starts))
	}
	for i, s := range starts {
		wantIndex := i + 1
		if s.index != wantIndex || s.total != 3 {
			t.Errorf("StartStep[%d]: index=%d total=%d, want %d/3", i, s.index, s.total, wantIndex)
		}
	}
}

func TestRunPipeline_ConfirmStep_SuspendNotSkipped(t *testing.T) {
	// A confirm builtin step must always call SuspendForExec/ResumeAfterExec so
	// the reporter can yield the terminal to stdin. skipConfirm=true lets the
	// builtin return immediately without blocking stdin.
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phase := config.DeployPhase{Name: "pre"}
	steps := []resolvedStep{
		{phase: phase, step: config.DeployStep{Name: "confirm", Builtin: "confirm"}},
	}

	err := runPipeline(steps, rep, "test", cfg, nil, t.TempDir(), nil, true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(rep.kindSeq(), "SuspendForExec") {
		t.Errorf("SuspendForExec must be called for confirm step, kinds: %v", rep.kindSeq())
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

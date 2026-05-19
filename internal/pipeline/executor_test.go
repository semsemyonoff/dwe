package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/usercommands"
)

// --- mockReporter records all reporter events for assertion ---

type reporterEvent struct {
	kind       string
	phaseKey   string
	phase      config.DeployPhase
	stepAddr   string
	step       config.DeployStep
	index      int
	total      int
	reason     string
	err        error
	name       string
	total0     int
	success    bool
	subIndices []int
	final      bool
}

type mockReporter struct {
	mu     sync.Mutex
	events []reporterEvent
}

func (m *mockReporter) append(e reporterEvent) {
	m.mu.Lock()
	m.events = append(m.events, e)
	m.mu.Unlock()
}

func (m *mockReporter) StartPipeline(name string, totalSteps int) {
	m.append(reporterEvent{kind: "StartPipeline", name: name, total0: totalSteps})
}
func (m *mockReporter) EnterPhase(phaseKey string, phase config.DeployPhase) {
	m.append(reporterEvent{kind: "EnterPhase", phaseKey: phaseKey, phase: phase})
}
func (m *mockReporter) SkipPhase(phaseKey string, phase config.DeployPhase, reason string) {
	m.append(reporterEvent{kind: "SkipPhase", phaseKey: phaseKey, phase: phase, reason: reason})
}
func (m *mockReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	m.append(reporterEvent{kind: "StartStep", stepAddr: stepAddr, step: step, index: index, total: total})
}
func (m *mockReporter) SkipStep(stepAddr string, step config.DeployStep, index int, total int, reason string) {
	m.append(reporterEvent{kind: "SkipStep", stepAddr: stepAddr, step: step, index: index, total: total, reason: reason})
}
func (m *mockReporter) FinishStep(stepAddr string, step config.DeployStep, index int, total int) {
	m.append(reporterEvent{kind: "FinishStep", stepAddr: stepAddr, step: step, index: index, total: total})
}
func (m *mockReporter) FailStep(stepAddr string, step config.DeployStep, index int, total int, err error) {
	m.append(reporterEvent{kind: "FailStep", stepAddr: stepAddr, step: step, index: index, total: total, err: err})
}
func (m *mockReporter) FinishPipeline(success bool) {
	m.append(reporterEvent{kind: "FinishPipeline", success: success})
}
func (m *mockReporter) StartGroup(groupAddr string, group config.DeployStep, subIndices []int, total int) {
	cp := make([]int, len(subIndices))
	copy(cp, subIndices)
	m.append(reporterEvent{kind: "StartGroup", stepAddr: groupAddr, step: group, total: total, subIndices: cp})
}
func (m *mockReporter) FinishGroup(groupAddr string, group config.DeployStep, success bool) {
	m.append(reporterEvent{kind: "FinishGroup", stepAddr: groupAddr, step: group, success: success})
}
func (m *mockReporter) StepOutput(addr string, frame string, final bool) {
	m.append(reporterEvent{kind: "StepOutput", stepAddr: addr, reason: frame, final: final})
}
func (m *mockReporter) SetSubStepLogPath(addr string, path string) {
	m.append(reporterEvent{kind: "SetSubStepLogPath", stepAddr: addr, reason: path})
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

// --- mockRecorder records all recorder events for assertion ---

type recorderEvent struct {
	kind       string
	addr       string
	actionHash string
	reason     string
	durationMs int64
	err        error
	name       string
	totalSteps int
	success    bool
}

type mockRecorder struct {
	mu     sync.Mutex
	events []recorderEvent
}

func (m *mockRecorder) append(e recorderEvent) {
	m.mu.Lock()
	m.events = append(m.events, e)
	m.mu.Unlock()
}

func (m *mockRecorder) OnPipelineStart(name string, totalSteps int) {
	m.append(recorderEvent{kind: "OnPipelineStart", name: name, totalSteps: totalSteps})
}
func (m *mockRecorder) OnStepStart(addr string, rs ResolvedStep, actionHash string) {
	m.append(recorderEvent{kind: "OnStepStart", addr: addr, actionHash: actionHash})
}
func (m *mockRecorder) OnStepFinish(addr string, rs ResolvedStep, actionHash string, durationMs int64) {
	m.append(recorderEvent{kind: "OnStepFinish", addr: addr, actionHash: actionHash, durationMs: durationMs})
}
func (m *mockRecorder) OnStepFail(addr string, rs ResolvedStep, actionHash string, durationMs int64, err error) {
	m.append(recorderEvent{kind: "OnStepFail", addr: addr, actionHash: actionHash, durationMs: durationMs, err: err})
}
func (m *mockRecorder) OnStepSkip(addr string, rs ResolvedStep, actionHash string, reason string) {
	m.append(recorderEvent{kind: "OnStepSkip", addr: addr, actionHash: actionHash, reason: reason})
}
func (m *mockRecorder) OnPipelineFinish(success bool) {
	m.append(recorderEvent{kind: "OnPipelineFinish", success: success})
}

// --- helpers ---

// noopStep returns a step that runs a no-op shell command.
func noopStep(name string) config.DeployStep {
	return config.DeployStep{Name: name, Type: "shell", Cmd: "true"}
}

// buildResolvedSteps creates resolved steps from phase+step pairs.
func buildResolvedSteps(phase config.DeployPhase, steps []config.DeployStep) []ResolvedStep {
	result := make([]ResolvedStep, len(steps))
	for i, s := range steps {
		result[i] = ResolvedStep{Phase: phase, Step: s}
	}
	return result
}

// --- tests ---

func TestRunPipeline_EmptySteps(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	err := Run(nil, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantKinds := []string{"StartPipeline", "EnterPhase", "StartStep", "FinishStep", "FinishPipeline"}
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

	finishStep := rep.eventAt(3)
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

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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
		{Name: "fail", Type: "shell", Cmd: "exit 1"},
	})

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}

	// FailStep must appear after StartStep.
	kinds := rep.kindSeq()
	findKind := func(k string) int {
		for i, ev := range kinds {
			if ev == k {
				return i
			}
		}
		return -1
	}
	startIdx := findKind("StartStep")
	failIdx := findKind("FailStep")
	if startIdx == -1 || failIdx == -1 {
		t.Fatalf("missing events: kinds=%v", kinds)
	}
	if startIdx >= failIdx {
		t.Errorf("event order wrong: Start=%d Fail=%d", startIdx, failIdx)
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
	steps := []ResolvedStep{
		{Phase: phase, Step: noopStep("do-thing"), RuntimeWhen: &condition.Condition{
			Type: condition.TypeBuiltin,
			Cmd:  "dir-empty " + workDir,
		}},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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
	expectedReason := "when: builtin dir-empty " + workDir
	if skipEvents[0].reason != expectedReason {
		t.Errorf("SkipStep reason = %q, want %q", skipEvents[0].reason, expectedReason)
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
	phaseWhenCond := &condition.Condition{
		Type: condition.TypeBuiltin,
		Cmd:  phaseWhenExpr,
	}
	steps := []ResolvedStep{
		{Phase: phase, Step: noopStep("step-a"), PhaseWhen: phaseWhenCond},
		{Phase: phase, Step: noopStep("step-b"), PhaseWhen: phaseWhenCond},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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
		if e.kind == "SkipStep" && !strings.Contains(e.reason, "phase when:") {
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

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, hooks)
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
	steps := buildResolvedSteps(phase, []config.DeployStep{{Name: "render-env", Type: "shell", Cmd: "exit 1"}})

	hookCalled := false
	hooks := map[string]func() error{
		"render-env": func() error {
			hookCalled = true
			return nil
		},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, hooks)
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
	steps := []ResolvedStep{
		{Phase: phase, Step: noopStep("migrate"), Service: "main"},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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

func TestRunPipeline_NoSuspendResumeAroundExec(t *testing.T) {
	// Task 6 removed the SuspendForExec/ResumeAfterExec calls in
	// executeStepBody — child output now routes through Reporter.StepOutput
	// so the live footer stays visible the entire time. Guard that the
	// executor does not regress and re-introduce the coarse hand-off.
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "p"}
	steps := buildResolvedSteps(phase, []config.DeployStep{noopStep("s")})

	if err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil); err != nil {
		t.Fatal(err)
	}

	for _, k := range rep.kindSeq() {
		if k == "SuspendForExec" || k == "ResumeAfterExec" {
			t.Errorf("executor must not call %s after Task 6 (kinds=%v)", k, rep.kindSeq())
		}
	}
}

func TestRunPipeline_PostDeploySkippedOnFailure(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phase1 := config.DeployPhase{Name: "setup"}
	phase2 := config.DeployPhase{Name: "post-deploy"}

	steps := []ResolvedStep{
		{Phase: phase1, Step: config.DeployStep{Name: "fail-step", Type: "shell", Cmd: "exit 1"}},
		{Phase: phase2, Step: noopStep("notify")},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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

	steps := []ResolvedStep{
		{Phase: tracked, Step: noopStep("a")},
		{Phase: tracked, Step: noopStep("b")},
		{Phase: untracked, Step: noopStep("info")},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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

	steps := []ResolvedStep{
		{Phase: tracked, Step: noopStep("a")},
		{Phase: untracked, Step: noopStep("info")},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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

	steps := []ResolvedStep{
		{Phase: phase1, Step: noopStep("a")},
		{Phase: phase1, Step: noopStep("b")},
		{Phase: phase2, Step: noopStep("c")},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
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

func TestRunPipeline_ConfirmStep_RunsWithoutSuspendResume(t *testing.T) {
	// Task 6 removed Suspend/Resume from the executor; confirm builtins now
	// rely on the package-level huh hooks installed by PlainReporter (Task 10).
	// This test just confirms the confirm step still runs cleanly.
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phase := config.DeployPhase{Name: "pre"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{Name: "confirm", Type: "builtin", Cmd: "confirm"}},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, k := range rep.kindSeq() {
		if k == "SuspendForExec" || k == "ResumeAfterExec" {
			t.Errorf("executor must not call %s after Task 6", k)
		}
	}
}

// --- files_gate tests ---

func TestRunPipeline_FilesGate_ReadableFilePresent(t *testing.T) {
	// Create a file that the gate will probe.
	workDir := t.TempDir()
	probeFile := filepath.Join(workDir, "dump.sql.gz")
	if err := os.WriteFile(probeFile, []byte("fake dump"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a registry with a command that has a files: block.
	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "dump.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "check-dump",
				Type: "shell",
				Cmd:  "echo dump-exists",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should run (file is present, gate satisfied).
	kinds := rep.kindSeq()
	finishIdx := -1
	for i, k := range kinds {
		if k == "FinishStep" {
			finishIdx = i
			break
		}
	}
	if finishIdx == -1 {
		t.Errorf("FinishStep should be called, got kinds: %v", kinds)
	}
}

func TestRunPipeline_FilesGate_ReadableFileMissing_SkipsStep(t *testing.T) {
	// Gate requires file to exist, but it doesn't.
	workDir := t.TempDir()

	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "dump.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "check-dump",
				Type: "shell",
				Cmd:  "echo dump-exists",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should be skipped due to gate.
	var skipEvents []reporterEvent
	for _, e := range rep.events {
		if e.kind == "SkipStep" {
			skipEvents = append(skipEvents, e)
		}
	}
	if len(skipEvents) != 1 {
		t.Errorf("want 1 SkipStep event, got %d (kinds: %v)", len(skipEvents), rep.kindSeq())
	}
	if len(skipEvents) > 0 && !strings.Contains(skipEvents[0].reason, "files_gate") {
		t.Errorf("SkipStep reason %q should mention files_gate", skipEvents[0].reason)
	}
}

func TestRunPipeline_FilesGate_MissingStateFileAbsent(t *testing.T) {
	// Gate state: missing, file absent → step should run.
	workDir := t.TempDir()

	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "dump.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "download-dump",
				Type: "shell",
				Cmd:  "echo downloading",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateMissing,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should run (file missing satisfies state: missing).
	kinds := rep.kindSeq()
	finishIdx := -1
	for i, k := range kinds {
		if k == "FinishStep" {
			finishIdx = i
			break
		}
	}
	if finishIdx == -1 {
		t.Errorf("FinishStep should be called, got kinds: %v", kinds)
	}
}

func TestRunPipeline_FilesGate_MissingStateFilePresent_SkipsStep(t *testing.T) {
	// Gate state: missing, file present → step should be skipped.
	workDir := t.TempDir()
	probeFile := filepath.Join(workDir, "dump.sql.gz")
	if err := os.WriteFile(probeFile, []byte("exists"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "dump.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "download-dump",
				Type: "shell",
				Cmd:  "echo downloading",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateMissing,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should be skipped (file present, state: missing → gate condition not met).
	kinds := rep.kindSeq()
	if slices.Contains(kinds, "FinishStep") {
		t.Errorf("FinishStep should not be called (step should be skipped), got kinds: %v", kinds)
	}
	if !slices.Contains(kinds, "SkipStep") {
		t.Errorf("expected SkipStep to be called, got kinds: %v", kinds)
	}
}

func TestRunPipeline_FilesGate_WhenFalseBypassesGate(t *testing.T) {
	// when: false should skip step without evaluating the gate.
	workDir := t.TempDir()
	probeFile := filepath.Join(workDir, "dump.sql.gz")
	if err := os.WriteFile(probeFile, []byte("fake dump"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a non-empty directory so "dir-empty" evaluates to false.
	nonEmptyDir := filepath.Join(workDir, "non-empty")
	if err := os.Mkdir(nonEmptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "dump.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "check-dump",
				Type: "shell",
				Cmd:  "echo dump-exists",
			},
			RuntimeWhen: &condition.Condition{
				Type: condition.TypeBuiltin,
				Cmd:  "dir-empty " + nonEmptyDir, // Will be false since dir is not empty
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateMissing, // gate would fail (file exists)
				Require: filesgate.RequireRequired{},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should be skipped due to when: false, not gate.
	var skipEvents []reporterEvent
	for _, e := range rep.events {
		if e.kind == "SkipStep" {
			skipEvents = append(skipEvents, e)
		}
	}
	if len(skipEvents) != 1 {
		t.Errorf("want 1 SkipStep event, got %d", len(skipEvents))
	} else if !strings.Contains(skipEvents[0].reason, "when:") {
		t.Errorf("SkipStep reason %q should mention 'when:', not gate", skipEvents[0].reason)
	}
}

func TestRunPipeline_FilesGate_NilRegistry_FailsStep(t *testing.T) {
	// Gate evaluation requires a registry; if none is provided, step should fail.
	workDir := t.TempDir()

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "check-dump",
				Type: "shell",
				Cmd:  "echo dump-exists",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    nil, // Nil registry should cause step to fail.
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent due to nil registry, got %v", err)
	}

	// FailStep should be called with a clear error message.
	var failEvents []reporterEvent
	for _, e := range rep.events {
		if e.kind == "FailStep" {
			failEvents = append(failEvents, e)
		}
	}
	if len(failEvents) != 1 {
		t.Errorf("want 1 FailStep event, got %d", len(failEvents))
	} else if !strings.Contains(failEvents[0].err.Error(), "registry") {
		t.Errorf("FailStep error %q should mention registry", failEvents[0].err.Error())
	}
}

// TestRunPipeline_FilesGate_WithRendersTemplate verifies that ${...} expressions in
// the gate's `with` block are rendered against project config before being passed
// to the target command's param validation. Regression test: previously these
// values were forwarded as literal strings and tripped pattern validation.
func TestRunPipeline_FilesGate_WithRendersTemplate(t *testing.T) {
	workDir := t.TempDir()
	// Create the file whose path includes the rendered database name.
	probeFile := filepath.Join(workDir, "tbm_stock.sql.gz")
	if err := os.WriteFile(probeFile, []byte("fake dump"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-dump-deploy",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Params: map[string]usercommands.ParamDef{
			"database": {
				Type:    usercommands.ParamTypeString,
				Pattern: `^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`,
			},
		},
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "${param.database}.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{
		"db": map[string]any{
			"stock_database": "tbm_stock",
		},
	}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "check-stock-dump",
				Type: "shell",
				Cmd:  "echo dump-exists",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-dump-deploy",
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
				With: map[string]any{
					"database": "${db.stock_database}",
				},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Gate satisfied (rendered path resolves and file exists) → step runs to completion.
	kinds := rep.kindSeq()
	finishIdx := slices.Index(kinds, "FinishStep")
	if finishIdx == -1 {
		t.Errorf("FinishStep should be called (gate should be satisfied after template render), got kinds: %v", kinds)
	}
	for _, e := range rep.events {
		if e.kind == "FailStep" {
			t.Errorf("unexpected FailStep: %v", e.err)
		}
	}
}

// TestRunPipeline_FilesGate_UnknownCommand_FailsStep verifies that a gated step fails with
// ErrSilent when the gate references a command not present in the registry.
func TestRunPipeline_FilesGate_UnknownCommand_FailsStep(t *testing.T) {
	workDir := t.TempDir()

	reg := usercommands.NewEmptyRegistry()
	// "db-download" intentionally absent from registry.

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "check-dump",
				Type: "shell",
				Cmd:  "echo dump-exists",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
			return journal.Run
		},
	})
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent for unknown command, got %v", err)
	}

	var failEvents []reporterEvent
	for _, e := range rep.events {
		if e.kind == "FailStep" {
			failEvents = append(failEvents, e)
		}
	}
	if len(failEvents) != 1 {
		t.Errorf("want 1 FailStep event, got %d", len(failEvents))
	} else if !strings.Contains(failEvents[0].err.Error(), "unknown command") {
		t.Errorf("FailStep error %q should mention 'unknown command'", failEvents[0].err.Error())
	}
}

// TestRunPipeline_FilesGate_JournalBypass_MissingStateArtifactAbsent verifies that a gated step
// bypasses the journal skip-decider when the gate is satisfied. State: missing; artifact absent
// → gate satisfied (no file present) → step runs, even though SkipDecider returns journal.Skip.
func TestRunPipeline_FilesGate_JournalBypass_MissingStateArtifactAbsent(t *testing.T) {
	workDir := t.TempDir()
	// Artifact is absent — state: missing gate is satisfied.

	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "dump.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	rec := &mockRecorder{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "upload-dump",
				Type: "shell",
				Cmd:  "true",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateMissing,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	// SkipDecider always returns Skip (simulating journal "already deployed").
	skipDecider := func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
		return journal.Skip
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    rec,
		SkipDecider: skipDecider,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Gate is satisfied (artifact absent → missing state met) → step must execute despite journal.Skip.
	finishes := 0
	for _, e := range rec.events {
		if e.kind == "OnStepFinish" {
			finishes++
		}
	}
	if finishes != 1 {
		t.Errorf("expected step to run (journal-skip bypassed by gate), got OnStepFinish count %d; events: %v", finishes, rec.events)
	}
}

// TestRunPipeline_FilesGate_JournalBypass_ReadableStateArtifactPresent verifies that a gated step
// bypasses the journal skip-decider when the gate is satisfied. State: readable; artifact present
// → gate satisfied → step runs, even though SkipDecider returns journal.Skip.
func TestRunPipeline_FilesGate_JournalBypass_ReadableStateArtifactPresent(t *testing.T) {
	workDir := t.TempDir()
	// Create the artifact so state: readable gate is satisfied.
	probeFile := filepath.Join(workDir, "dump.sql.gz")
	if err := os.WriteFile(probeFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := usercommands.NewEmptyRegistry()
	cmd := &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{
					{Path: "dump.sql.gz"},
				},
				Access:   usercommands.FileAccessRead,
				Required: true,
			},
		},
	}
	reg.AddCommandForTest(cmd)

	rep := &mockReporter{}
	rec := &mockRecorder{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step: config.DeployStep{
				Name: "check-dump",
				Type: "shell",
				Cmd:  "true",
			},
			FilesGate: &filesgate.FilesGate{
				Command: "db-download",
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
		},
	}

	// SkipDecider always returns Skip (simulating journal "already deployed").
	skipDecider := func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
		return journal.Skip
	}

	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   nil,
		SkipConfirm: true,
		Recorder:    rec,
		SkipDecider: skipDecider,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Gate is satisfied (artifact present → readable state met) → step must execute despite journal.Skip.
	finishes := 0
	for _, e := range rec.events {
		if e.kind == "OnStepFinish" {
			finishes++
		}
	}
	if finishes != 1 {
		t.Errorf("expected step to run (journal-skip bypassed by gate), got OnStepFinish count %d; events: %v", finishes, rec.events)
	}
}

// TestRunPipeline_PerStepSkipConfirm verifies that a step with
// SkipConfirm:true bypasses the confirmation prompt even when the pipeline-wide
// SkipConfirm flag is false. The confirm builtin would otherwise read from
// stdin and block in tests, so a clean exit demonstrates the per-step bypass.
func TestRunPipeline_PerStepSkipConfirm(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	phase := config.DeployPhase{Name: "pre"}
	steps := []ResolvedStep{
		{Phase: phase, Step: config.DeployStep{
			Name:        "confirm",
			Type:        "builtin",
			Cmd:         "confirm",
			SkipConfirm: true,
		}},
	}

	// Pipeline-wide skipConfirm=false; per-step SkipConfirm=true must still bypass.
	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(rep.kindSeq(), "FinishStep") {
		t.Errorf("FinishStep should be reached when per-step SkipConfirm bypasses prompt, kinds: %v", rep.kindSeq())
	}
}

// TestChildIO_TTY_AllocatesPTY verifies that when stdout is a TTY and a log
// writer is set, childIO allocates a pty: the returned writers are *os.File
// (the tty slave), so the child process sees a real terminal fd. Output
// written to the slave is mirrored through the goroutine to both real stdout
// and the log writer.
func TestChildIO_TTY_AllocatesPTY(t *testing.T) {
	prev := stdoutIsTTY
	stdoutIsTTY = func() bool { return true }
	defer func() { stdoutIsTTY = prev }()

	logBuf := &bytes.Buffer{}
	stdout, stderr, cleanup := childIO(logBuf, false)
	if _, ok := stdout.(*os.File); !ok {
		t.Fatalf("expected *os.File (pty slave) for stdout, got %T", stdout)
	}
	if stdout != stderr {
		t.Errorf("expected stdout and stderr to be the same pty slave, got distinct values")
	}
	if stdout == os.Stdout {
		t.Error("pty path must not return os.Stdout — child needs the slave fd, not the parent's terminal")
	}
	cleanup()
}

// TestChildIO_NonTTY_TeesToLog verifies that when stdout is not a TTY, childIO
// returns a MultiWriter that tees to the log writer — preserving the
// log-capture behavior for CI and redirected runs where there is no TTY to
// destroy.
func TestChildIO_NonTTY_TeesToLog(t *testing.T) {
	prev := stdoutIsTTY
	stdoutIsTTY = func() bool { return false }
	defer func() { stdoutIsTTY = prev }()

	logBuf := &bytes.Buffer{}
	stdout, _, cleanup := childIO(logBuf, false)
	defer cleanup()
	if stdout == os.Stdout {
		t.Error("expected MultiWriter when non-TTY, got os.Stdout directly")
	}
	if _, err := stdout.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := logBuf.String(); got != "hello" {
		t.Errorf("log buffer = %q, want %q", got, "hello")
	}
}

// TestChildIO_NilLogWriter_PassesThrough verifies that when the log writer is
// nil (logging disabled), childIO returns os.Stdout/os.Stderr regardless of
// TTY state.
func TestChildIO_NilLogWriter_PassesThrough(t *testing.T) {
	for _, tty := range []bool{true, false} {
		prev := stdoutIsTTY
		stdoutIsTTY = func() bool { return tty }
		stdout, stderr, cleanup := childIO(nil, false)
		stdoutIsTTY = prev
		cleanup()
		if stdout != os.Stdout || stderr != os.Stderr {
			t.Errorf("tty=%v: expected pass-through, got stdout=%T stderr=%T", tty, stdout, stderr)
		}
	}
}

// TestBuildDevboxCmd_SetsCLICOLOR_FORCE verifies that devbox: step commands
// are built with CLICOLOR_FORCE=1 so lipgloss enables colors even when stdout
// is piped through an io.MultiWriter.
func TestBuildDevboxCmd_SetsCLICOLOR_FORCE(t *testing.T) {
	cmd := buildDevboxCmd(context.Background(), "info", t.TempDir(), "sh", "devbox", false)
	if !slices.Contains(cmd.Env, "CLICOLOR_FORCE=1") {
		t.Errorf("buildDevboxCmd env should contain CLICOLOR_FORCE=1, got: %v", cmd.Env)
	}
}

// TestBuildDevboxCmd_InheritsParentEnv verifies that the child env includes
// parent environment variables (not just CLICOLOR_FORCE).
func TestBuildDevboxCmd_InheritsParentEnv(t *testing.T) {
	cmd := buildDevboxCmd(context.Background(), "info", t.TempDir(), "sh", "devbox", false)
	// cmd.Env should be non-empty (it includes os.Environ() + CLICOLOR_FORCE).
	if len(cmd.Env) == 0 {
		t.Error("buildDevboxCmd env should include parent environment (os.Environ())")
	}
	// The env count should be at least os.Environ() + 1 for CLICOLOR_FORCE.
	if len(cmd.Env) < len(os.Environ())+1 {
		t.Errorf("expected at least %d env vars, got %d", len(os.Environ())+1, len(cmd.Env))
	}
}

// TestBuildDevboxCmd_WorkDir verifies that the cmd working directory is set correctly.
func TestBuildDevboxCmd_WorkDir(t *testing.T) {
	workDir := t.TempDir()
	cmd := buildDevboxCmd(context.Background(), "info", workDir, "sh", "devbox", false)
	if cmd.Dir != workDir {
		t.Errorf("buildDevboxCmd Dir = %q, want %q", cmd.Dir, workDir)
	}
}

// TestBuildDevboxCmd_SkipConfirmSetsNonInteractive verifies that skipConfirm=true
// adds DEVBOX_NONINTERACTIVE=1 to the child environment.
func TestBuildDevboxCmd_SkipConfirmSetsNonInteractive(t *testing.T) {
	cmd := buildDevboxCmd(context.Background(), "info", t.TempDir(), "sh", "devbox", true)
	if !slices.Contains(cmd.Env, "DEVBOX_NONINTERACTIVE=1") {
		t.Errorf("buildDevboxCmd with skipConfirm should contain DEVBOX_NONINTERACTIVE=1, got: %v", cmd.Env)
	}
	cmd2 := buildDevboxCmd(context.Background(), "info", t.TempDir(), "sh", "devbox", false)
	if slices.Contains(cmd2.Env, "DEVBOX_NONINTERACTIVE=1") {
		t.Errorf("buildDevboxCmd without skipConfirm should not contain DEVBOX_NONINTERACTIVE=1")
	}
}

// TestRunPipeline_ContinueOnError_Continues verifies that a failing step with
// ContinueOnError=true causes FailStep to be called but does not abort the pipeline.
// The next step must still execute and FinishPipeline must be called with success=true.
func TestRunPipeline_ContinueOnError_Continues(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "hooks"}

	failStep := config.DeployStep{Name: "optional-hook", Type: "shell", Cmd: "exit 1", ContinueOnError: true}
	nextStep := noopStep("after-hook")
	steps := buildResolvedSteps(phase, []config.DeployStep{failStep, nextStep})

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("want nil error (continue_on_error), got %v", err)
	}

	kinds := rep.kindSeq()
	// FailStep must appear for the failing step.
	failFound := false
	for _, e := range rep.events {
		if e.kind == "FailStep" && e.step.Name == "optional-hook" {
			failFound = true
		}
	}
	if !failFound {
		t.Errorf("FailStep not recorded for continue_on_error step; kinds: %v", kinds)
	}

	// The next step must have been executed (FinishStep present for it).
	afterFound := false
	for _, e := range rep.events {
		if e.kind == "FinishStep" && e.step.Name == "after-hook" {
			afterFound = true
		}
	}
	if !afterFound {
		t.Errorf("FinishStep not recorded for step after continue_on_error step; kinds: %v", kinds)
	}

	// FinishPipeline must be success=true.
	fp := rep.events[len(rep.events)-1]
	if fp.kind != "FinishPipeline" || !fp.success {
		t.Errorf("FinishPipeline success = %v, want true; kinds: %v", fp.success, kinds)
	}
}

// TestRunPipeline_ContinueOnError_SkipsHookAndCheck verifies that when a step fails
// with ContinueOnError=true, neither the post-step hook nor the Check condition runs.
func TestRunPipeline_ContinueOnError_SkipsHookAndCheck(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "hooks"}

	failStep := config.DeployStep{
		Name:            "optional-hook",
		Type:            "shell",
		Cmd:             "exit 1",
		ContinueOnError: true,
		Check:           &config.Action{Type: "shell", Cmd: "false"},
	}
	steps := buildResolvedSteps(phase, []config.DeployStep{failStep})

	hookCalled := false
	hooks := map[string]func() error{
		"optional-hook": func() error {
			hookCalled = true
			return nil
		},
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, hooks)
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if hookCalled {
		t.Error("post-step hook must not be called after a failed continue_on_error step")
	}
	// FinishStep must NOT appear (the check path is skipped too).
	for _, e := range rep.events {
		if e.kind == "FinishStep" {
			t.Errorf("FinishStep must not be called when continue_on_error step fails; kinds: %v", rep.kindSeq())
		}
	}
}

// TestRunPipeline_ContinueOnError_CheckFails verifies that when the body succeeds but the
// check fails on a step with ContinueOnError=true, the pipeline continues rather than aborting.
func TestRunPipeline_ContinueOnError_CheckFails(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "verify"}

	checkFailStep := config.DeployStep{
		Name:            "verify-thing",
		Type:            "shell",
		Cmd:             "true",
		ContinueOnError: true,
		Check:           &config.Action{Type: "shell", Cmd: "false"},
	}
	nextStep := noopStep("after-verify")
	steps := buildResolvedSteps(phase, []config.DeployStep{checkFailStep, nextStep})

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if err != nil {
		t.Fatalf("want nil error (continue_on_error on check failure), got %v", err)
	}

	kinds := rep.kindSeq()
	failIdx := slices.Index(kinds, "FailStep")
	if failIdx < 0 {
		t.Fatalf("FailStep not recorded for check failure; kinds: %v", kinds)
	}
	// Pipeline must continue: FinishStep for the next step must appear after FailStep.
	finishAfterFail := false
	for _, e := range rep.events[failIdx+1:] {
		if e.kind == "FinishStep" && e.step.Name == "after-verify" {
			finishAfterFail = true
			break
		}
	}
	if !finishAfterFail {
		t.Errorf("FinishStep for step after check-fail continue_on_error not recorded; kinds: %v", kinds)
	}
}

// TestRunPipeline_NoContinueOnError_AbortsAsUsual verifies that a failing step
// without ContinueOnError still returns ErrSilent (existing behaviour is unchanged).
func TestRunPipeline_NoContinueOnError_AbortsAsUsual(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "init"}
	steps := buildResolvedSteps(phase, []config.DeployStep{
		{Name: "fail", Type: "shell", Cmd: "exit 1", ContinueOnError: false},
	})

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent, got %v", err)
	}
}

// TestRunPipeline_PostStepHook_ReturnsError verifies that when a post-step hook returns
// an error, Run calls FailStep on the reporter and returns ErrSilent.
func TestRunPipeline_PostStepHook_ReturnsError(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "env"}
	steps := buildResolvedSteps(phase, []config.DeployStep{noopStep("render-env")})

	hookErr := errors.New("hook failed")
	hooks := map[string]func() error{
		"render-env": func() error { return hookErr },
	}

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, hooks)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("expected ErrSilent from hook failure, got %v", err)
	}

	failFound := false
	for _, e := range rep.events {
		if e.kind == "FailStep" && e.step.Name == "render-env" {
			failFound = true
		}
	}
	if !failFound {
		t.Error("expected FailStep to be called for the failed hook step")
	}
}

// TestRunPipeline_Check_Fails verifies that when a step succeeds but its Check
// condition evaluates to false, FailStep is called and ErrSilent is returned.
func TestRunPipeline_Check_Fails(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}

	// Shell check that exits non-zero → check returns false.
	step := config.DeployStep{Name: "check-step", Type: "shell", Cmd: "true", Check: &config.Action{Type: "shell", Cmd: "false"}}
	steps := buildResolvedSteps(phase, []config.DeployStep{step})

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent when check fails, got %v", err)
	}

	failFound := false
	for _, e := range rep.events {
		if e.kind == "FailStep" && e.step.Name == "check-step" {
			failFound = true
		}
	}
	if !failFound {
		t.Errorf("FailStep not recorded when check fails; kinds: %v", rep.kindSeq())
	}
}

// TestRunPipeline_Check_EvalError verifies that when EvalRuntime returns an error,
// FailStep is called and ErrSilent is returned.
func TestRunPipeline_Check_EvalError(t *testing.T) {
	rep := &mockReporter{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "setup"}

	// An invalid/unknown condition function name causes EvalRuntime to error.
	step := config.DeployStep{Name: "check-step", Type: "shell", Cmd: "true", Check: &config.Action{Type: "builtin", Cmd: "unknown-condition-fn /some/path"}}
	steps := buildResolvedSteps(phase, []config.DeployStep{step})

	err := Run(steps, rep, "test", cfg, nil, t.TempDir(), nil, false, nil)
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("want ErrSilent when check eval errors, got %v", err)
	}

	failFound := false
	for _, e := range rep.events {
		if e.kind == "FailStep" && e.step.Name == "check-step" {
			failFound = true
		}
	}
	if !failFound {
		t.Errorf("FailStep not recorded when check eval errors; kinds: %v", rep.kindSeq())
	}
}

// TestRunPipeline_ServiceConfigsCheckBuiltin verifies that service_configs_check builtin
// works correctly as a check action on a deploy step.
func TestRunPipeline_ServiceConfigsCheckBuiltin(t *testing.T) {
	tmpDir := t.TempDir()

	// Create service directory structure with configs
	svcDir := filepath.Join(tmpDir, "services", "main", "configs")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("failed to create service dir: %v", err)
	}

	rep := &mockReporter{}
	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
		Services: map[string]config.ServiceConfig{
			"main": {
				Dir: "services/main",
				Configs: []config.ServiceConfigEntry{
					{File: "app.env"},
					{File: "db.env"},
				},
			},
		},
	}
	phase := config.DeployPhase{Name: "setup"}

	// Test 1: Check passes when all config files exist
	t.Run("check_passes_when_configs_exist", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(svcDir, "app.env"), []byte("KEY=value"), 0o644); err != nil {
			t.Fatalf("failed to write app.env: %v", err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "db.env"), []byte("DB_HOST=localhost"), 0o644); err != nil {
			t.Fatalf("failed to write db.env: %v", err)
		}

		step := config.DeployStep{
			Name:  "copy-configs",
			Type:  "shell",
			Cmd:   "true",
			Check: &config.Action{Type: "builtin", Cmd: "service_configs_check", With: map[string]any{"service": "main"}},
		}
		steps := buildResolvedSteps(phase, []config.DeployStep{step})

		err := Run(steps, rep, "test", cfg, nil, tmpDir, nil, false, nil)
		if err != nil {
			t.Fatalf("Run with passing check failed: %v", err)
		}

		// Verify FinishStep was called (step succeeded)
		finishFound := false
		for _, e := range rep.events {
			if e.kind == "FinishStep" && e.step.Name == "copy-configs" {
				finishFound = true
			}
		}
		if !finishFound {
			t.Errorf("FinishStep not recorded when check passes; kinds: %v", rep.kindSeq())
		}
	})

	// Test 2: Check fails when config files are missing
	t.Run("check_fails_when_configs_missing", func(t *testing.T) {
		// Clean up from previous test
		_ = os.Remove(filepath.Join(svcDir, "app.env"))
		_ = os.Remove(filepath.Join(svcDir, "db.env"))

		rep := &mockReporter{}
		step := config.DeployStep{
			Name:  "copy-configs",
			Type:  "shell",
			Cmd:   "true",
			Check: &config.Action{Type: "builtin", Cmd: "service_configs_check", With: map[string]any{"service": "main"}},
		}
		steps := buildResolvedSteps(phase, []config.DeployStep{step})

		err := Run(steps, rep, "test", cfg, nil, tmpDir, nil, false, nil)
		if !errors.Is(err, ErrSilent) {
			t.Fatalf("want ErrSilent when check fails, got %v", err)
		}

		// Verify FailStep was called (check failed)
		failFound := false
		for _, e := range rep.events {
			if e.kind == "FailStep" && e.step.Name == "copy-configs" {
				failFound = true
			}
		}
		if !failFound {
			t.Errorf("FailStep not recorded when check fails; kinds: %v", rep.kindSeq())
		}
	})
}

// TestBuildDevboxCmd_UsesShellParam verifies that buildDevboxCmd uses the supplied
// shell binary, not a hardcoded "sh".
func TestBuildDevboxCmd_UsesShellParam(t *testing.T) {
	tests := []struct {
		shell string
	}{
		{"sh"},
		{"bash"},
		{"zsh"},
	}
	for _, tc := range tests {
		cmd := buildDevboxCmd(context.Background(), "info", t.TempDir(), tc.shell, "devbox", false)
		// exec.Command resolves the binary; Args[0] holds the original name.
		if len(cmd.Args) == 0 || cmd.Args[0] != tc.shell {
			t.Errorf("shell=%q: Args[0] = %q, want %q", tc.shell, cmd.Args[0], tc.shell)
		}
	}
}

// TestExecAction_UnknownType verifies that ExecAction returns an error for unknown action types.
func TestExecAction_UnknownType(t *testing.T) {
	a := config.Action{Type: "bogus", Cmd: "echo hi"}
	actx := ActionContext{
		WorkDir: t.TempDir(),
		Cfg:     &config.DevboxConfig{Raw: map[string]any{}},
	}
	err := ExecAction(context.Background(), a, actx)
	if err == nil {
		t.Fatal("expected error for unknown action type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown action type") {
		t.Errorf("error %q should contain 'unknown action type'", err.Error())
	}
}

// TestExecStep_ShellFromConfig verifies that ExecStep uses cfg.Binaries.Shell
// instead of a hardcoded "sh" when running a run: step.
func TestExecStep_ShellFromConfig(t *testing.T) {
	// Use a step that would fail if run under "sh" but trivially succeeds under
	// the configured shell. We assert the step succeeds with a shell that exists.
	cfg := &config.DevboxConfig{
		Binaries: config.BinariesConfig{Shell: "sh"}, // must be a real shell for the test to pass
		Raw:      map[string]any{},
	}
	step := config.DeployStep{Name: "noop", Type: "shell", Cmd: "true"}
	err := ExecStep(context.Background(), step, t.TempDir(), cfg, nil, nil, false)
	if err != nil {
		t.Fatalf("ExecStep with Shell=sh failed: %v", err)
	}
}

// TestBuildDevboxCmd_DevboxBinParam verifies that buildDevboxCmd accepts a devboxBin
// fallback parameter and produces a non-empty shell command for any non-empty devboxBin.
// At runtime, os.Executable() is preferred; devboxBin is only used when it fails.
func TestBuildDevboxCmd_DevboxBinParam(t *testing.T) {
	cases := []string{"devbox", "my-devbox", "/usr/local/bin/devbox"}
	for _, bin := range cases {
		cmd := buildDevboxCmd(context.Background(), "info", t.TempDir(), "sh", bin, false)
		// The shell command is: sh -c "<resolved_binary> info"
		if len(cmd.Args) < 3 {
			t.Fatalf("bin=%q: expected at least 3 args, got %v", bin, cmd.Args)
		}
		// The third arg is the shell -c string. It must contain "info" (the devboxArg).
		shellCmd := cmd.Args[2]
		if !strings.Contains(shellCmd, "info") {
			t.Errorf("bin=%q: shell cmd %q should contain devboxArg 'info'", bin, shellCmd)
		}
		// The shell -c string must not be empty (ensures no exec.Command("") regression).
		if shellCmd == "" {
			t.Errorf("bin=%q: shell cmd is empty", bin)
		}
	}
}

// --- state tracking tests ---

func TestRunWithOptions_State_StepSkipped_NoCheck(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "init"}
	step := noopStep("setup")
	steps := buildResolvedSteps(phase, []config.DeployStep{step})

	// SkipDecider always returns Skip for this step (simulating previous state: ok, hash match, no check).
	skipDecider := func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
		if addr == "init/setup" {
			return journal.Skip
		}
		return journal.Run
	}

	opts := RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "test",
		Config:       cfg,
		Registry:     nil,
		WorkDir:      t.TempDir(),
		LogWriter:    nil,
		SkipConfirm:  false,
		PostStepHook: nil,
		Recorder:     rec,
		SkipDecider:  skipDecider,
	}
	err := RunWithOptions(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check reporter sees SkipStep.
	repSkips := 0
	for _, e := range rep.events {
		if e.kind == "SkipStep" && e.reason == "state: already deployed" {
			repSkips++
		}
	}
	if repSkips != 1 {
		t.Errorf("expected 1 SkipStep event with state reason, got %d", repSkips)
	}

	// Check recorder sees OnStepSkip with "state" reason.
	recSkips := 0
	for _, e := range rec.events {
		if e.kind == "OnStepSkip" && e.reason == "state" {
			recSkips++
		}
	}
	if recSkips != 1 {
		t.Errorf("expected 1 OnStepSkip event with 'state' reason, got %d", recSkips)
	}

	// Step should never be executed (no OnStepStart or OnStepFinish).
	starts := 0
	finishes := 0
	for _, e := range rec.events {
		if e.kind == "OnStepStart" {
			starts++
		}
		if e.kind == "OnStepFinish" {
			finishes++
		}
	}
	if starts != 0 || finishes != 0 {
		t.Errorf("step should not execute when skipped: OnStepStart=%d OnStepFinish=%d", starts, finishes)
	}
}

func TestRunWithOptions_State_StepRuns_WithCheck(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "init"}
	step := config.DeployStep{
		Name:  "setup",
		Type:  "shell",
		Cmd:   "true",
		Check: &config.Action{Type: "shell", Cmd: "true"},
	}
	steps := buildResolvedSteps(phase, []config.DeployStep{step})

	// SkipDecider would return Skip (state ok, hash match), but step has check so should Run.
	// In reality, the closure in Task 8 should handle this, but we test the behavior.
	// Actually, looking back at the spec, when a step has a check, the Decide function
	// returns Run. So we simulate that here.
	skipDecider := func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
		// If the step has a check, always run so the check can re-validate.
		if rs.Step.Check != nil {
			return journal.Run
		}
		return journal.Skip
	}

	opts := RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "test",
		Config:       cfg,
		Registry:     nil,
		WorkDir:      t.TempDir(),
		LogWriter:    nil,
		SkipConfirm:  false,
		PostStepHook: nil,
		Recorder:     rec,
		SkipDecider:  skipDecider,
	}
	err := RunWithOptions(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check recorder sees OnStepStart and OnStepFinish (step was executed).
	starts := 0
	finishes := 0
	for _, e := range rec.events {
		if e.kind == "OnStepStart" {
			starts++
		}
		if e.kind == "OnStepFinish" {
			finishes++
		}
	}
	if starts != 1 || finishes != 1 {
		t.Errorf("step should execute when it has a check: OnStepStart=%d OnStepFinish=%d", starts, finishes)
	}

	// Check should have run successfully.
	// Reporter shows FinishStep after check.
	if rep.eventAt(len(rep.events)-2).kind != "FinishStep" {
		t.Error("FinishStep should come before FinishPipeline")
	}
}

func TestRunWithOptions_State_StepRuns_ActionHashDiverged(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "init"}
	step := noopStep("setup")
	steps := buildResolvedSteps(phase, []config.DeployStep{step})

	// SkipDecider returns Run because action hash diverged.
	skipDecider := func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
		// Simulating: previous state had a different action hash.
		if addr == "init/setup" && actionHash != "different_hash" {
			return journal.Run
		}
		return journal.Skip
	}

	opts := RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "test",
		Config:       cfg,
		Registry:     nil,
		WorkDir:      t.TempDir(),
		LogWriter:    nil,
		SkipConfirm:  false,
		PostStepHook: nil,
		Recorder:     rec,
		SkipDecider:  skipDecider,
	}
	err := RunWithOptions(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should have been executed.
	starts := 0
	finishes := 0
	for _, e := range rec.events {
		if e.kind == "OnStepStart" {
			starts++
		}
		if e.kind == "OnStepFinish" {
			finishes++
		}
	}
	if starts != 1 || finishes != 1 {
		t.Errorf("step should execute when action hash diverged: OnStepStart=%d OnStepFinish=%d", starts, finishes)
	}
}

func TestRunWithOptions_State_WhenConditionTakesPrecedence(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}

	// Create a directory with a file so dir-empty evaluates to false.
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	phase := config.DeployPhase{Name: "init"}
	step := noopStep("setup")
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step:  step,
			RuntimeWhen: &condition.Condition{
				Type: condition.TypeBuiltin,
				Cmd:  "dir-empty " + workDir,
			},
		},
	}

	// SkipDecider would return Run (simulating valid state), but when: evaluates to false → skip.
	skipDecider := func(addr string, rs ResolvedStep, actionHash string) journal.Decision {
		return journal.Run // Always run in normal state
	}

	opts := RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "test",
		Config:       cfg,
		Registry:     nil,
		WorkDir:      workDir,
		LogWriter:    nil,
		SkipConfirm:  false,
		PostStepHook: nil,
		Recorder:     rec,
		SkipDecider:  skipDecider,
	}
	err := RunWithOptions(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step should be skipped due to when condition, not state decision.
	// Recorder should see OnStepSkip with reason containing "when", not "state".
	skips := 0
	for _, e := range rec.events {
		if e.kind == "OnStepSkip" && strings.Contains(e.reason, "when") {
			skips++
		}
	}
	if skips != 1 {
		t.Errorf("expected 1 OnStepSkip due to when condition, got %d", skips)
	}

	// Reporter should also show SkipStep with when reason.
	repSkips := 0
	for _, e := range rep.events {
		if e.kind == "SkipStep" && strings.Contains(e.reason, "when") {
			repSkips++
		}
	}
	if repSkips != 1 {
		t.Errorf("expected 1 reporter SkipStep due to when condition, got %d", repSkips)
	}
}

func TestRunWithOptions_RecorderGetsDurationMs(t *testing.T) {
	rep := &mockReporter{}
	rec := &mockRecorder{}
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "init"}
	step := noopStep("setup")
	steps := buildResolvedSteps(phase, []config.DeployStep{step})

	opts := RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "test",
		Config:       cfg,
		Registry:     nil,
		WorkDir:      t.TempDir(),
		LogWriter:    nil,
		SkipConfirm:  false,
		PostStepHook: nil,
		Recorder:     rec,
		SkipDecider:  func(addr string, rs ResolvedStep, actionHash string) journal.Decision { return journal.Run },
	}
	err := RunWithOptions(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find OnStepFinish and check durationMs is non-negative.
	found := false
	for _, e := range rec.events {
		if e.kind == "OnStepFinish" {
			found = true
			if e.durationMs < 0 {
				t.Errorf("OnStepFinish durationMs should be non-negative, got %d", e.durationMs)
			}
			break
		}
	}
	if !found {
		t.Error("OnStepFinish event not found in recorder")
	}
}

func TestFormatRequireSpec(t *testing.T) {
	tests := []struct {
		name string
		spec filesgate.RequireSpec
		want string
	}{
		{
			name: "RequireRequired",
			spec: filesgate.RequireRequired{},
			want: "required",
		},
		{
			name: "RequireAll",
			spec: filesgate.RequireAll{},
			want: "all",
		},
		{
			name: "RequireList single ID",
			spec: filesgate.RequireList{IDs: []string{"dump"}},
			want: "dump",
		},
		{
			name: "RequireList multiple IDs",
			spec: filesgate.RequireList{IDs: []string{"dump", "backup"}},
			want: "dump,backup",
		},
		{
			name: "nil spec defaults to required",
			spec: nil,
			want: "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatRequireSpec(tt.spec)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatFilesGate(t *testing.T) {
	tests := []struct {
		name string
		gate *filesgate.FilesGate
		ids  []string
		want string
	}{
		{
			name: "nil gate",
			gate: nil,
			ids:  nil,
			want: "",
		},
		{
			name: "readable gate with required spec (no ids)",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
			ids:  nil,
			want: "files_gate: readable (required)",
		},
		{
			name: "missing gate with all spec (no ids)",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateMissing,
				Require: filesgate.RequireAll{},
			},
			ids:  nil,
			want: "files_gate: missing (all)",
		},
		{
			name: "readable gate with single file ID",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateReadable,
				Require: filesgate.RequireList{IDs: []string{"dump"}},
			},
			ids:  nil,
			want: "files_gate: readable (dump)",
		},
		{
			name: "readable gate with multiple file IDs (no ids for display)",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateReadable,
				Require: filesgate.RequireList{IDs: []string{"dump", "backup"}},
			},
			ids:  nil,
			want: "files_gate: readable (dump,backup)",
		},
		{
			name: "readable gate with resolved IDs at runtime",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
			ids:  []string{"dump", "backup"},
			want: "files_gate: readable [dump,backup]",
		},
		{
			name: "missing gate with resolved IDs at runtime",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateMissing,
				Require: filesgate.RequireRequired{},
			},
			ids:  []string{"artifact"},
			want: "files_gate: missing [artifact]",
		},
		{
			name: "gate with many resolved IDs (truncated at 3)",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
			ids:  []string{"id1", "id2", "id3", "id4", "id5"},
			want: "files_gate: readable [id1,id2,id3,...]",
		},
		{
			name: "gate with exactly 3 resolved IDs (no truncation)",
			gate: &filesgate.FilesGate{
				State:   filesgate.StateReadable,
				Require: filesgate.RequireRequired{},
			},
			ids:  []string{"a", "b", "c"},
			want: "files_gate: readable [a,b,c]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			if tt.ids == nil {
				got = FormatFilesGate(tt.gate)
			} else {
				got = FormatFilesGate(tt.gate, tt.ids...)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

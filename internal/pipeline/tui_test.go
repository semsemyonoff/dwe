package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/stopwatch"

	"devbox-cli/internal/config"
)

// applyMsg is a helper that calls model.Update and returns the updated tuiModel.
func applyMsg(m tuiModel, msg any) tuiModel {
	updated, _ := m.Update(msg)
	return updated.(tuiModel)
}

// testModel returns a properly initialized tuiModel for use in tests.
func testModel() tuiModel {
	return newTUIModel("203")
}

// --- tuiStartPipelineMsg ---

func TestTUIModel_StartPipeline(t *testing.T) {
	m := testModel()
	m = applyMsg(m, tuiStartPipelineMsg{name: "deploy", total: 10})
	if m.pipelineName != "deploy" {
		t.Errorf("expected pipelineName %q, got %q", "deploy", m.pipelineName)
	}
	if m.totalSteps != 10 {
		t.Errorf("expected totalSteps 10, got %d", m.totalSteps)
	}
}

// --- tuiEnterPhaseMsg ---

func TestTUIModel_EnterPhase(t *testing.T) {
	m := testModel()
	m = applyMsg(m, tuiEnterPhaseMsg{phaseKey: "init", phase: config.DeployPhase{Name: "init"}})
	if m.currentPhase != "init" {
		t.Errorf("expected currentPhase %q, got %q", "init", m.currentPhase)
	}
}

func TestTUIModel_EnterPhase_ClearsCurrentStep(t *testing.T) {
	m := testModel()
	m.currentStep = "init/some-step"
	m = applyMsg(m, tuiEnterPhaseMsg{phaseKey: "setup", phase: config.DeployPhase{Name: "setup"}})
	if m.currentStep != "" {
		t.Errorf("expected currentStep cleared, got %q", m.currentStep)
	}
}

// --- tuiSkipPhaseMsg ---

func TestTUIModel_SkipPhase_NoStateChange(t *testing.T) {
	m := testModel()
	m.currentPhase = "init"
	m.totalSteps = 5
	m = applyMsg(m, tuiSkipPhaseMsg{phaseKey: "init", reason: "when: dir-empty"})
	// SkipPhase is intentionally a no-op on visible state (phase label unchanged).
	if m.currentPhase != "init" {
		t.Errorf("expected currentPhase unchanged, got %q", m.currentPhase)
	}
	if m.totalSteps != 5 {
		t.Errorf("expected totalSteps unchanged, got %d", m.totalSteps)
	}
}

// --- tuiStartStepMsg ---

func TestTUIModel_StartStep(t *testing.T) {
	m := testModel()
	step := config.DeployStep{Name: "migrate"}
	m = applyMsg(m, tuiStartStepMsg{addr: "main/setup/migrate", step: step, index: 3, total: 7})
	if m.currentStep != "main/setup/migrate" {
		t.Errorf("expected currentStep %q, got %q", "main/setup/migrate", m.currentStep)
	}
	if m.stepIndex != 3 {
		t.Errorf("expected stepIndex 3, got %d", m.stepIndex)
	}
	if m.stepTotal != 7 {
		t.Errorf("expected stepTotal 7, got %d", m.stepTotal)
	}
	if len(m.recentSteps) != 1 {
		t.Fatalf("expected 1 recent step, got %d", len(m.recentSteps))
	}
	if m.recentSteps[0].status != "running" {
		t.Errorf("expected status %q, got %q", "running", m.recentSteps[0].status)
	}
	if m.recentSteps[0].addr != "main/setup/migrate" {
		t.Errorf("expected addr %q, got %q", "main/setup/migrate", m.recentSteps[0].addr)
	}
}

// --- tuiFinishStepMsg ---

func TestTUIModel_FinishStep(t *testing.T) {
	step := config.DeployStep{Name: "migrate"}
	m := testModel()
	m = applyMsg(m, tuiStartStepMsg{addr: "main/setup/migrate", step: step, index: 3, total: 7})
	m = applyMsg(m, tuiFinishStepMsg{addr: "main/setup/migrate", index: 3, total: 7})

	if m.completedCount != 1 {
		t.Errorf("expected completedCount 1, got %d", m.completedCount)
	}
	if m.currentStep != "" {
		t.Errorf("expected currentStep cleared, got %q", m.currentStep)
	}
	if len(m.recentSteps) != 1 {
		t.Fatalf("expected 1 recent step, got %d", len(m.recentSteps))
	}
	if m.recentSteps[0].status != "done" {
		t.Errorf("expected status %q, got %q", "done", m.recentSteps[0].status)
	}
}

// --- tuiSkipStepMsg ---

func TestTUIModel_SkipStep(t *testing.T) {
	step := config.DeployStep{Name: "migrate"}
	m := testModel()
	m = applyMsg(m, tuiStartStepMsg{addr: "init/migrate", step: step, index: 2, total: 5})
	m = applyMsg(m, tuiSkipStepMsg{addr: "init/migrate", index: 2, total: 5, reason: "when: dir-empty"})

	if m.completedCount != 1 {
		t.Errorf("expected completedCount 1, got %d", m.completedCount)
	}
	if m.currentStep != "" {
		t.Errorf("expected currentStep cleared, got %q", m.currentStep)
	}
	if m.recentSteps[0].status != "skipped" {
		t.Errorf("expected status %q, got %q", "skipped", m.recentSteps[0].status)
	}
}

// --- tuiFailStepMsg ---

func TestTUIModel_FailStep(t *testing.T) {
	step := config.DeployStep{Name: "migrate"}
	m := testModel()
	m = applyMsg(m, tuiStartStepMsg{addr: "main/setup/migrate", step: step, index: 4, total: 6})
	m = applyMsg(m, tuiFailStepMsg{
		addr:  "main/setup/migrate",
		index: 4, total: 6,
		err: errors.New("exit status 1"),
	})

	if m.completedCount != 1 {
		t.Errorf("expected completedCount 1, got %d", m.completedCount)
	}
	if m.currentStep != "" {
		t.Errorf("expected currentStep cleared, got %q", m.currentStep)
	}
	if m.recentSteps[0].status != "failed" {
		t.Errorf("expected status %q, got %q", "failed", m.recentSteps[0].status)
	}
	if m.recentSteps[0].errMsg != "exit status 1" {
		t.Errorf("expected errMsg %q, got %q", "exit status 1", m.recentSteps[0].errMsg)
	}
}

func TestTUIModel_FailStep_NilError(t *testing.T) {
	step := config.DeployStep{Name: "step"}
	m := testModel()
	m = applyMsg(m, tuiStartStepMsg{addr: "a/b", step: step, index: 1, total: 1})
	m = applyMsg(m, tuiFailStepMsg{addr: "a/b", index: 1, total: 1, err: nil})

	if m.recentSteps[0].errMsg != "" {
		t.Errorf("expected empty errMsg for nil error, got %q", m.recentSteps[0].errMsg)
	}
}

// --- tuiFinishPipelineMsg ---

func TestTUIModel_FinishPipeline_Success(t *testing.T) {
	m := testModel()
	m.pipelineName = "deploy"
	updated, cmd := m.Update(tuiFinishPipelineMsg{success: true})
	result := updated.(tuiModel)

	if !result.done {
		t.Error("expected done=true")
	}
	if !result.success {
		t.Error("expected success=true")
	}
	if result.currentStep != "" {
		t.Errorf("expected currentStep cleared, got %q", result.currentStep)
	}
	if cmd == nil {
		t.Error("expected Quit cmd, got nil")
	}
}

func TestTUIModel_FinishPipeline_Failure(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(tuiFinishPipelineMsg{success: false})
	result := updated.(tuiModel)
	if result.done != true {
		t.Error("expected done=true")
	}
	if result.success != false {
		t.Error("expected success=false")
	}
}

// --- spinner sub-model forwarding ---

func TestTUIModel_SpinnerTickForwarded(t *testing.T) {
	m := testModel()
	initialView := m.spinner.View()
	// Send a spinner tick; it should advance the spinner's internal frame.
	tickMsg := m.spinner.Tick()
	m = applyMsg(m, tickMsg)
	// After one tick the spinner frame advances; View may differ.
	// We mainly verify no panic and a non-nil cmd is returned.
	_ = m.spinner.View()
	_ = initialView
}

func TestTUIModel_SpinnerTick_ReturnsCmdForNext(t *testing.T) {
	m := testModel()
	tickMsg := m.spinner.Tick()
	_, cmd := m.Update(tickMsg)
	if cmd == nil {
		t.Error("expected non-nil Cmd after spinner tick to schedule next tick")
	}
}

// --- stopwatch sub-model forwarding ---

func TestTUIModel_StopwatchStartStop_Forwarded(t *testing.T) {
	m := testModel()
	startMsg := stopwatch.StartStopMsg{}
	m2 := applyMsg(m, startMsg)
	// We mainly verify the message is handled without panic.
	_ = m2.stopwatch.Elapsed()
}

func TestTUIModel_StopwatchTick_Forwarded(t *testing.T) {
	m := testModel()
	// Manually construct a tick — in real use it arrives after Init/Start.
	tickMsg := stopwatch.TickMsg{}
	_ = applyMsg(m, tickMsg) // should not panic
}

// --- progress sub-model forwarding ---

func TestTUIModel_ProgressFrameMsg_Forwarded(t *testing.T) {
	m := testModel()
	frameMsg := progress.FrameMsg{}
	_ = applyMsg(m, frameMsg) // should not panic
}

// --- recentSteps cap ---

func TestTUIModel_RecentSteps_Cap(t *testing.T) {
	m := testModel()
	for i := range maxRecentSteps + 2 {
		step := config.DeployStep{Name: "step"}
		addr := fmt.Sprintf("phase/step%d", i)
		m = applyMsg(m, tuiStartStepMsg{addr: addr, step: step, index: i + 1, total: 10})
	}
	if len(m.recentSteps) != maxRecentSteps {
		t.Errorf("expected recentSteps capped at %d, got %d", maxRecentSteps, len(m.recentSteps))
	}
}

// --- completedCount accumulates across steps ---

func TestTUIModel_CompletedCount(t *testing.T) {
	m := testModel()
	m.totalSteps = 5
	step := config.DeployStep{Name: "s"}

	// done
	m = applyMsg(m, tuiStartStepMsg{addr: "p/s1", step: step, index: 1, total: 5})
	m = applyMsg(m, tuiFinishStepMsg{addr: "p/s1", index: 1, total: 5})
	// skipped
	m = applyMsg(m, tuiStartStepMsg{addr: "p/s2", step: step, index: 2, total: 5})
	m = applyMsg(m, tuiSkipStepMsg{addr: "p/s2", index: 2, total: 5, reason: "when"})
	// failed
	m = applyMsg(m, tuiStartStepMsg{addr: "p/s3", step: step, index: 3, total: 5})
	m = applyMsg(m, tuiFailStepMsg{addr: "p/s3", index: 3, total: 5, err: errors.New("oops")})

	if m.completedCount != 3 {
		t.Errorf("expected completedCount 3, got %d", m.completedCount)
	}
}

// --- View content ---

func TestTUIModel_View_PipelineName(t *testing.T) {
	m := testModel()
	m.pipelineName = "deploy"
	m.totalSteps = 5
	view := m.View()
	if !strings.Contains(view.Content, "Deploy") {
		t.Error("expected pipeline name in view")
	}
}

func TestTUIModel_View_PipelineName_Reset(t *testing.T) {
	m := testModel()
	m.pipelineName = "reset"
	m.totalSteps = 3
	view := m.View()
	if !strings.Contains(view.Content, "Reset") {
		t.Error("expected reset pipeline name in view, got: " + view.Content)
	}
	if strings.Contains(view.Content, "Deploy") {
		t.Error("reset pipeline must not display Deploy header")
	}
}

func TestTUIModel_View_CurrentPhase(t *testing.T) {
	m := testModel()
	m.currentPhase = "main/setup"
	view := m.View()
	if !strings.Contains(view.Content, "main/setup") {
		t.Error("expected phase name in view")
	}
}

func TestTUIModel_View_CurrentStep(t *testing.T) {
	m := testModel()
	m.currentStep = "main/setup/migrate"
	m.stepIndex = 3
	m.stepTotal = 7
	view := m.View()
	if !strings.Contains(view.Content, "main/setup/migrate") {
		t.Error("expected step addr in view")
	}
	if !strings.Contains(view.Content, "3/7") {
		t.Error("expected step progress in view")
	}
}

func TestTUIModel_View_ProgressBar(t *testing.T) {
	m := testModel()
	m.totalSteps = 10
	m.completedCount = 5
	view := m.View()
	// Progress count should appear
	if !strings.Contains(view.Content, "5/10") {
		t.Error("expected '5/10' in view")
	}
}

func TestTUIModel_View_RecentSteps(t *testing.T) {
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "init/render-env", status: "done"},
		{addr: "main/setup/migrate", status: "running"},
	}
	view := m.View()
	if !strings.Contains(view.Content, "init/render-env") {
		t.Error("expected done step in view")
	}
	if !strings.Contains(view.Content, "main/setup/migrate") {
		t.Error("expected running step in view")
	}
}

func TestTUIModel_View_EmptyState(t *testing.T) {
	m := testModel()
	view := m.View()
	// Should not panic and should return a valid (possibly empty) view.
	_ = view.Content
}

func TestTUIModel_View_ElapsedTimer(t *testing.T) {
	m := testModel()
	m.pipelineName = "deploy"
	// With zero elapsed the timer should show 00:00.
	view := m.View()
	if !strings.Contains(view.Content, "00:00") {
		t.Errorf("expected '00:00' elapsed timer in view, got: %s", view.Content)
	}
}

// --- formatElapsed ---

func TestFormatElapsed_Zero(t *testing.T) {
	if got := formatElapsed(0); got != "00:00" {
		t.Errorf("formatElapsed(0): got %q, want %q", got, "00:00")
	}
}

func TestFormatElapsed_Seconds(t *testing.T) {
	if got := formatElapsed(42 * time.Second); got != "00:42" {
		t.Errorf("formatElapsed(42s): got %q, want %q", got, "00:42")
	}
}

func TestFormatElapsed_Minutes(t *testing.T) {
	if got := formatElapsed(90 * time.Second); got != "01:30" {
		t.Errorf("formatElapsed(90s): got %q, want %q", got, "01:30")
	}
}

func TestFormatElapsed_Hours(t *testing.T) {
	if got := formatElapsed(3661 * time.Second); got != "61:01" {
		t.Errorf("formatElapsed(3661s): got %q, want %q", got, "61:01")
	}
}

// --- stepIcon ---

func TestStepIcon_Done(t *testing.T) {
	if got := stepIcon("done"); got != "✓" {
		t.Errorf("expected ✓, got %q", got)
	}
}

func TestStepIcon_Skipped(t *testing.T) {
	if got := stepIcon("skipped"); got != "◎" {
		t.Errorf("expected ◎, got %q", got)
	}
}

func TestStepIcon_Failed(t *testing.T) {
	if got := stepIcon("failed"); got != "✗" {
		t.Errorf("expected ✗, got %q", got)
	}
}

func TestStepIcon_Running(t *testing.T) {
	if got := stepIcon("running"); got != "·" {
		t.Errorf("expected ·, got %q", got)
	}
}

func TestStepIcon_Unknown(t *testing.T) {
	if got := stepIcon(""); got != "·" {
		t.Errorf("expected · for unknown status, got %q", got)
	}
}

// --- Init returns a Cmd ---

func TestTUIModel_Init_ReturnsCmd(t *testing.T) {
	m := testModel()
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected Init to return a non-nil Cmd for spinner/stopwatch")
	}
}

// --- newTUIModel sub-model initialization ---

func TestNewTUIModel_SpinnerInitialized(t *testing.T) {
	m := newTUIModel("203")
	// A properly initialized spinner should not return "(error)" in its View.
	v := m.spinner.View()
	if v == "(error)" {
		t.Errorf("spinner not initialized: View() returned %q", v)
	}
}

func TestNewTUIModel_StopwatchZeroElapsed(t *testing.T) {
	m := newTUIModel("203")
	if m.stopwatch.Elapsed() != 0 {
		t.Errorf("expected stopwatch elapsed=0 initially, got %v", m.stopwatch.Elapsed())
	}
}

func TestNewTUIModel_ProgressRendersBar(t *testing.T) {
	m := newTUIModel("203")
	bar := m.progress.ViewAs(0.5)
	if bar == "" {
		t.Error("expected non-empty progress bar at 50%")
	}
}

// --- Interface compliance ---

// Verify spinner is properly typed (compile-time check).
var _ spinner.Model = newTUIModel("").spinner

// Verify TUIReporter satisfies the Reporter interface at compile time.
var _ Reporter = (*TUIReporter)(nil)

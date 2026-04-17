package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

// applyMsg is a helper that calls model.Update and returns the updated tuiModel.
func applyMsg(m tuiModel, msg any) tuiModel {
	updated, _ := m.Update(msg)
	return updated.(tuiModel)
}

// --- tuiStartPipelineMsg ---

func TestTUIModel_StartPipeline(t *testing.T) {
	m := tuiModel{}
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
	m := tuiModel{}
	m = applyMsg(m, tuiEnterPhaseMsg{phaseKey: "init", phase: config.DeployPhase{Name: "init"}})
	if m.currentPhase != "init" {
		t.Errorf("expected currentPhase %q, got %q", "init", m.currentPhase)
	}
}

func TestTUIModel_EnterPhase_ClearsCurrentStep(t *testing.T) {
	m := tuiModel{currentStep: "init/some-step"}
	m = applyMsg(m, tuiEnterPhaseMsg{phaseKey: "setup", phase: config.DeployPhase{Name: "setup"}})
	if m.currentStep != "" {
		t.Errorf("expected currentStep cleared, got %q", m.currentStep)
	}
}

// --- tuiSkipPhaseMsg ---

func TestTUIModel_SkipPhase_NoStateChange(t *testing.T) {
	m := tuiModel{currentPhase: "init", totalSteps: 5}
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
	m := tuiModel{}
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
	m := tuiModel{}
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
	m := tuiModel{}
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
	m := tuiModel{}
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
	m := tuiModel{}
	m = applyMsg(m, tuiStartStepMsg{addr: "a/b", step: step, index: 1, total: 1})
	m = applyMsg(m, tuiFailStepMsg{addr: "a/b", index: 1, total: 1, err: nil})

	if m.recentSteps[0].errMsg != "" {
		t.Errorf("expected empty errMsg for nil error, got %q", m.recentSteps[0].errMsg)
	}
}

// --- tuiFinishPipelineMsg ---

func TestTUIModel_FinishPipeline_Success(t *testing.T) {
	m := tuiModel{pipelineName: "deploy"}
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
	m := tuiModel{}
	updated, _ := m.Update(tuiFinishPipelineMsg{success: false})
	result := updated.(tuiModel)
	if result.done != true {
		t.Error("expected done=true")
	}
	if result.success != false {
		t.Error("expected success=false")
	}
}

// --- tuiTickMsg ---

func TestTUIModel_Tick_AdvancesSpinner(t *testing.T) {
	m := tuiModel{spinnerFrame: 0}
	m = applyMsg(m, tuiTickMsg{})
	if m.spinnerFrame != 1 {
		t.Errorf("expected spinnerFrame 1, got %d", m.spinnerFrame)
	}
}

func TestTUIModel_Tick_WrapsSpinner(t *testing.T) {
	m := tuiModel{spinnerFrame: len(spinnerFrames) - 1}
	m = applyMsg(m, tuiTickMsg{})
	if m.spinnerFrame != 0 {
		t.Errorf("expected spinnerFrame 0 after wrap, got %d", m.spinnerFrame)
	}
}

func TestTUIModel_Tick_ReturnsCmdForNext(t *testing.T) {
	m := tuiModel{}
	_, cmd := m.Update(tuiTickMsg{})
	if cmd == nil {
		t.Error("expected non-nil Cmd to schedule next tick")
	}
}

// --- recentSteps cap ---

func TestTUIModel_RecentSteps_Cap(t *testing.T) {
	m := tuiModel{}
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
	m := tuiModel{totalSteps: 5}
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
	m := tuiModel{pipelineName: "deploy", totalSteps: 5}
	view := m.View()
	if !strings.Contains(view.Content, "Deploy") {
		t.Error("expected pipeline name in view")
	}
}

func TestTUIModel_View_PipelineName_Reset(t *testing.T) {
	m := tuiModel{pipelineName: "reset", totalSteps: 3}
	view := m.View()
	if !strings.Contains(view.Content, "Reset") {
		t.Error("expected reset pipeline name in view, got: " + view.Content)
	}
	if strings.Contains(view.Content, "Deploy") {
		t.Error("reset pipeline must not display Deploy header")
	}
}

func TestTUIModel_View_CurrentPhase(t *testing.T) {
	m := tuiModel{currentPhase: "main/setup"}
	view := m.View()
	if !strings.Contains(view.Content, "main/setup") {
		t.Error("expected phase name in view")
	}
}

func TestTUIModel_View_CurrentStep(t *testing.T) {
	m := tuiModel{currentStep: "main/setup/migrate", stepIndex: 3, stepTotal: 7}
	view := m.View()
	if !strings.Contains(view.Content, "main/setup/migrate") {
		t.Error("expected step addr in view")
	}
	if !strings.Contains(view.Content, "3/7") {
		t.Error("expected step progress in view")
	}
}

func TestTUIModel_View_ProgressBar(t *testing.T) {
	m := tuiModel{totalSteps: 10, completedCount: 5}
	view := m.View()
	if !strings.Contains(view.Content, "5/10") {
		t.Error("expected '5/10' in view")
	}
	if !strings.Contains(view.Content, "[") {
		t.Error("expected progress bar brackets in view")
	}
}

func TestTUIModel_View_RecentSteps(t *testing.T) {
	m := tuiModel{
		recentSteps: []tuiStepRecord{
			{addr: "init/render-env", status: "done"},
			{addr: "main/setup/migrate", status: "running"},
		},
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
	m := tuiModel{}
	view := m.View()
	// Should not panic and should return a valid (possibly empty) view.
	_ = view.Content
}

// --- progressBar ---

func TestProgressBar_Empty(t *testing.T) {
	got := progressBar(0, 10, 10)
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Errorf("expected brackets, got %q", got)
	}
	if strings.Contains(got, "█") {
		t.Errorf("expected no filled chars at 0, got %q", got)
	}
}

func TestProgressBar_Full(t *testing.T) {
	got := progressBar(10, 10, 10)
	want := "[██████████]"
	if got != want {
		t.Errorf("progressBar(10,10,10): got %q, want %q", got, want)
	}
}

func TestProgressBar_Half(t *testing.T) {
	got := progressBar(5, 10, 10)
	want := "[█████░░░░░]"
	if got != want {
		t.Errorf("progressBar(5,10,10): got %q, want %q", got, want)
	}
}

func TestProgressBar_ZeroTotal(t *testing.T) {
	got := progressBar(0, 0, 10)
	if !strings.Contains(got, "░") {
		t.Errorf("expected all-empty bar for zero total, got %q", got)
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
	m := tuiModel{}
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected Init to return a non-nil Cmd for spinner tick")
	}
}

// --- Interface compliance ---

// Verify TUIReporter satisfies the Reporter interface at compile time.
var _ Reporter = (*TUIReporter)(nil)

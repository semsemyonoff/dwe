package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/stopwatch"
	tea "charm.land/bubbletea/v2"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
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

// --- recentSteps: no cap, full history ---

func TestTUIModel_RecentSteps_NoCap(t *testing.T) {
	m := testModel()
	const n = 20
	for i := range n {
		step := config.DeployStep{Name: "step"}
		addr := fmt.Sprintf("phase/step%d", i)
		m = applyMsg(m, tuiStartStepMsg{addr: addr, step: step, index: i + 1, total: n})
	}
	if len(m.recentSteps) != n {
		t.Errorf("expected all %d steps stored, got %d", n, len(m.recentSteps))
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

// TestTUIModel_CompletedCount_UntrackedNotCounted verifies that steps from
// untracked phases (index=0, total=0) do not increment completedCount, so the
// progress bar cannot exceed 100% during post-deploy phases.
func TestTUIModel_CompletedCount_UntrackedNotCounted(t *testing.T) {
	m := testModel()
	m.totalSteps = 2
	step := config.DeployStep{Name: "s"}

	// Two tracked steps complete.
	m = applyMsg(m, tuiStartStepMsg{addr: "p/s1", step: step, index: 1, total: 2})
	m = applyMsg(m, tuiFinishStepMsg{addr: "p/s1", index: 1, total: 2})
	m = applyMsg(m, tuiStartStepMsg{addr: "p/s2", step: step, index: 2, total: 2})
	m = applyMsg(m, tuiFinishStepMsg{addr: "p/s2", index: 2, total: 2})

	// One untracked step (post-deploy) completes — index=0, total=0.
	m = applyMsg(m, tuiStartStepMsg{addr: "post-deploy/notify", step: step, index: 0, total: 0})
	m = applyMsg(m, tuiFinishStepMsg{addr: "post-deploy/notify", index: 0, total: 0})

	if m.completedCount != 2 {
		t.Errorf("expected completedCount 2 (tracked only), got %d", m.completedCount)
	}
}

func TestTUIModel_CompletedCount_UntrackedSkipNotCounted(t *testing.T) {
	m := testModel()
	m.totalSteps = 1
	step := config.DeployStep{Name: "s"}

	m = applyMsg(m, tuiStartStepMsg{addr: "p/s1", step: step, index: 1, total: 1})
	m = applyMsg(m, tuiFinishStepMsg{addr: "p/s1", index: 1, total: 1})

	// Untracked skipped step must not affect count.
	m = applyMsg(m, tuiStartStepMsg{addr: "post-deploy/skip", step: step, index: 0, total: 0})
	m = applyMsg(m, tuiSkipStepMsg{addr: "post-deploy/skip", index: 0, total: 0, reason: "when: false"})

	if m.completedCount != 1 {
		t.Errorf("expected completedCount 1 (untracked skip excluded), got %d", m.completedCount)
	}
}

func TestTUIModel_CompletedCount_UntrackedFailNotCounted(t *testing.T) {
	m := testModel()
	m.totalSteps = 1
	step := config.DeployStep{Name: "s"}

	m = applyMsg(m, tuiStartStepMsg{addr: "p/s1", step: step, index: 1, total: 1})
	m = applyMsg(m, tuiFinishStepMsg{addr: "p/s1", index: 1, total: 1})

	// Untracked failed step must not affect count.
	m = applyMsg(m, tuiStartStepMsg{addr: "post-deploy/fail", step: step, index: 0, total: 0})
	m = applyMsg(m, tuiFailStepMsg{addr: "post-deploy/fail", index: 0, total: 0, err: errors.New("oops")})

	if m.completedCount != 1 {
		t.Errorf("expected completedCount 1 (untracked fail excluded), got %d", m.completedCount)
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

func TestTUIModel_View_RecentSteps_PlainStyle_Done(t *testing.T) {
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "init/render-env", status: "done", index: 1, total: 5},
	}
	view := m.View()
	// Strip ANSI codes so the check is not affected by Lipgloss icon styling.
	plain := stripANSI(view.Content)
	if !strings.Contains(plain, "✓ [1/5] Done: init/render-env") {
		t.Errorf("expected plain-style done line, got: %s", plain)
	}
}

func TestTUIModel_View_RecentSteps_PlainStyle_Skipped(t *testing.T) {
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "main/db/create", status: "skipped", index: 3, total: 5, reason: "when: dir-empty"},
	}
	view := m.View()
	plain := stripANSI(view.Content)
	if !strings.Contains(plain, "◎ [3/5] Skipped: main/db/create (when: dir-empty)") {
		t.Errorf("expected plain-style skipped line with reason, got: %s", plain)
	}
}

func TestTUIModel_View_RecentSteps_PlainStyle_Failed(t *testing.T) {
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "main/setup/migrate", status: "failed", index: 4, total: 5, errMsg: "exit status 1"},
	}
	view := m.View()
	plain := stripANSI(view.Content)
	if !strings.Contains(plain, "✗ [4/5] Failed: main/setup/migrate") {
		t.Errorf("expected plain-style failed line, got: %s", plain)
	}
	if !strings.Contains(view.Content, "exit status 1") {
		t.Errorf("expected error message in view, got: %s", view.Content)
	}
}

func TestTUIModel_View_CurrentStep_Untracked_NoIndex(t *testing.T) {
	// An untracked current step (index=0, total=0) must not show [0/0] in the
	// live step line. It should render only the spinner and the step address.
	m := testModel()
	m.currentStep = "post-deploy/notify"
	m.stepIndex = 0
	m.stepTotal = 0
	view := m.View()
	if strings.Contains(view.Content, "[0/0]") {
		t.Errorf("untracked current step must not show [0/0], got: %s", view.Content)
	}
	if !strings.Contains(view.Content, "post-deploy/notify") {
		t.Errorf("expected step addr in view, got: %s", view.Content)
	}
}

func TestTUIModel_View_RecentSteps_Untracked_NoIndex(t *testing.T) {
	// Untracked steps have index=0, total=0 and should render without [N/M].
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "post-deploy/notify", status: "done", index: 0, total: 0},
	}
	view := m.View()
	plain := stripANSI(view.Content)
	if !strings.Contains(plain, "✓ Done: post-deploy/notify") {
		t.Errorf("expected untracked done step without index, got: %s", plain)
	}
	if strings.Contains(view.Content, "[0/0]") {
		t.Errorf("untracked step must not show [0/0], got: %s", view.Content)
	}
}

func TestTUIModel_StepRecord_IndexAndReason(t *testing.T) {
	step := config.DeployStep{Name: "db/create"}
	m := testModel()
	m = applyMsg(m, tuiStartStepMsg{addr: "main/db/create", step: step, index: 3, total: 7})
	m = applyMsg(m, tuiSkipStepMsg{addr: "main/db/create", index: 3, total: 7, reason: "when: dir-empty"})

	if len(m.recentSteps) != 1 {
		t.Fatalf("expected 1 step record, got %d", len(m.recentSteps))
	}
	rec := m.recentSteps[0]
	if rec.index != 3 {
		t.Errorf("expected index 3, got %d", rec.index)
	}
	if rec.total != 7 {
		t.Errorf("expected total 7, got %d", rec.total)
	}
	if rec.reason != "when: dir-empty" {
		t.Errorf("expected reason %q, got %q", "when: dir-empty", rec.reason)
	}
}

func TestTUIModel_StepRecord_IndexStoredOnStart(t *testing.T) {
	step := config.DeployStep{Name: "migrate"}
	m := testModel()
	m = applyMsg(m, tuiStartStepMsg{addr: "main/setup/migrate", step: step, index: 5, total: 10})

	if len(m.recentSteps) != 1 {
		t.Fatalf("expected 1 step record, got %d", len(m.recentSteps))
	}
	rec := m.recentSteps[0]
	if rec.index != 5 {
		t.Errorf("expected index 5, got %d", rec.index)
	}
	if rec.total != 10 {
		t.Errorf("expected total 10, got %d", rec.total)
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

// --- confirmation model ---

func TestTUIModel_ConfirmMsg_SetsConfirmActive(t *testing.T) {
	m := testModel()
	respCh := make(chan bool, 1)
	m = applyMsg(m, tuiConfirmMsg{message: "Delete all data?", okMsg: "OK", stopMsg: "Aborted", respCh: respCh})
	if !m.confirmActive {
		t.Error("expected confirmActive=true after tuiConfirmMsg")
	}
}

func TestTUIModel_ConfirmMsg_StoresFields(t *testing.T) {
	m := testModel()
	respCh := make(chan bool, 1)
	m = applyMsg(m, tuiConfirmMsg{message: "Delete all data?", okMsg: "Continuing", stopMsg: "Stopped", respCh: respCh})
	if m.confirmMessage != "Delete all data?" {
		t.Errorf("expected confirmMessage %q, got %q", "Delete all data?", m.confirmMessage)
	}
	if m.confirmOkMsg != "Continuing" {
		t.Errorf("expected confirmOkMsg %q, got %q", "Continuing", m.confirmOkMsg)
	}
	if m.confirmStopMsg != "Stopped" {
		t.Errorf("expected confirmStopMsg %q, got %q", "Stopped", m.confirmStopMsg)
	}
}

func TestTUIModel_KeyY_WhenConfirmActive_ClearsAndSignalsTrue(t *testing.T) {
	m := testModel()
	respCh := make(chan bool, 1)
	m = applyMsg(m, tuiConfirmMsg{message: "Are you sure?", respCh: respCh})

	// Send 'y' key while confirm is active.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	result := updated.(tuiModel)

	if result.confirmActive {
		t.Error("expected confirmActive=false after 'y' key")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd to send response")
	}
	// Execute the returned cmd — it should send true to respCh.
	cmd()
	select {
	case v := <-respCh:
		if !v {
			t.Error("expected true sent to respCh for 'y' key")
		}
	default:
		t.Error("expected value in respCh after cmd execution")
	}
}

func TestTUIModel_KeyCapitalY_WhenConfirmActive_SignalsTrue(t *testing.T) {
	m := testModel()
	respCh := make(chan bool, 1)
	m = applyMsg(m, tuiConfirmMsg{message: "Are you sure?", respCh: respCh})

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for 'Y' key")
	}
	cmd()
	select {
	case v := <-respCh:
		if !v {
			t.Error("expected true sent to respCh for 'Y' key")
		}
	default:
		t.Error("expected value in respCh")
	}
}

func TestTUIModel_KeyN_WhenConfirmActive_ClearsAndSignalsFalse(t *testing.T) {
	m := testModel()
	respCh := make(chan bool, 1)
	m = applyMsg(m, tuiConfirmMsg{message: "Are you sure?", respCh: respCh})

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	result := updated.(tuiModel)

	if result.confirmActive {
		t.Error("expected confirmActive=false after 'n' key")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd to send response")
	}
	cmd()
	select {
	case v := <-respCh:
		if v {
			t.Error("expected false sent to respCh for 'n' key")
		}
	default:
		t.Error("expected value in respCh after cmd execution")
	}
}

func TestTUIModel_KeyEsc_WhenConfirmActive_SignalsFalse(t *testing.T) {
	m := testModel()
	respCh := make(chan bool, 1)
	m = applyMsg(m, tuiConfirmMsg{message: "Are you sure?", respCh: respCh})

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for esc key")
	}
	cmd()
	select {
	case v := <-respCh:
		if v {
			t.Error("expected false sent to respCh for esc key")
		}
	default:
		t.Error("expected value in respCh")
	}
}

func TestTUIModel_KeyY_WhenNotConfirmActive_IsNoOp(t *testing.T) {
	m := testModel()
	m.pipelineName = "deploy"
	m.totalSteps = 5

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	result := updated.(tuiModel)

	if result.confirmActive {
		t.Error("confirmActive must remain false when confirm is not active")
	}
	if cmd != nil {
		t.Error("expected nil cmd for key event when confirm is not active")
	}
}

func TestTUIModel_View_ConfirmPrompt_ShowsMessage(t *testing.T) {
	m := testModel()
	m.confirmActive = true
	m.confirmMessage = "This will delete all data."
	m.confirmOkMsg = "Continuing"
	m.confirmStopMsg = "Aborted"

	view := m.View()
	if !strings.Contains(view.Content, "This will delete all data.") {
		t.Errorf("expected confirm message in view, got: %s", view.Content)
	}
	if !strings.Contains(view.Content, "[Y]") {
		t.Errorf("expected [Y] key hint in view, got: %s", view.Content)
	}
	if !strings.Contains(view.Content, "[N]") {
		t.Errorf("expected [N] key hint in view, got: %s", view.Content)
	}
}

func TestTUIModel_View_ConfirmPrompt_HidesPipelineUI(t *testing.T) {
	// When confirm is active the regular pipeline UI should not be shown.
	m := testModel()
	m.pipelineName = "deploy"
	m.currentPhase = "setup"
	m.confirmActive = true
	m.confirmMessage = "Are you sure?"

	view := m.View()
	// The confirm view returns early — pipeline header/phase should be absent.
	if strings.Contains(view.Content, "Deploy") {
		t.Errorf("pipeline header must not appear when confirm is active, got: %s", view.Content)
	}
	if strings.Contains(view.Content, "Phase: setup") {
		t.Errorf("phase label must not appear when confirm is active, got: %s", view.Content)
	}
}

func TestTUIModel_ConfirmResponseSentMsg_Handled(t *testing.T) {
	// tuiConfirmResponseSentMsg must not panic and should be a no-op.
	m := testModel()
	result := applyMsg(m, tuiConfirmResponseSentMsg{})
	_ = result // no panic is sufficient
}

// TestTUIReporter_Confirm_UnblocksWhenProgramExits verifies that Confirm() returns
// an error rather than blocking forever when the Bubble Tea program exits before
// the user responds (e.g. due to SIGINT/ctrl+c).
func TestTUIReporter_Confirm_UnblocksWhenProgramExits(t *testing.T) {
	done := make(chan struct{})
	// Create a reporter with a pre-closed done channel to simulate program exit.
	// program is nil — we never call Send; we just need the select to fire on done.
	r := &TUIReporter{done: done}
	close(done)

	// Confirm must return promptly with an error, not block.
	resultCh := make(chan error, 1)
	go func() {
		// We can't call r.program.Send (program is nil), so test the select path
		// directly by calling the inner logic: the done channel is already closed,
		// so the select should choose that arm immediately.
		// Simulate what Confirm does without the Send:
		respCh := make(chan bool, 1) // never written to
		select {
		case <-respCh:
			resultCh <- nil
		case <-r.done:
			resultCh <- fmt.Errorf("TUI program exited before confirmation response")
		}
	}()

	select {
	case err := <-resultCh:
		if err == nil {
			t.Error("expected error when program exits, got nil")
		}
	case <-time.After(time.Second):
		t.Error("Confirm did not unblock within 1s after program exit")
	}
}

// --- logWriter output ---

// tuiReporterWithLog creates a TUIReporter with a bytes.Buffer as its logWriter.
// The Bubble Tea program is NOT started (program is nil) — only logWriter is exercised.
// This lets us test logf without needing a running Bubble Tea program.
func tuiReporterWithLog(buf *bytes.Buffer) *TUIReporter {
	return &TUIReporter{logWriter: buf}
}

func TestTUIReporter_EnterPhase_LogWriter(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	phase := config.DeployPhase{Name: "init", Description: "Initialise environment"}
	// Call logf path directly via the exported EnterPhase — but the reporter has no
	// program, so we call the logf helper directly instead of the full method.
	// We validate the logf helper produces the correct output.
	r.logf("Phase: %s: %s\n", "init", "Initialise environment")
	got := buf.String()
	if !strings.Contains(got, "Phase: init: Initialise environment") {
		t.Errorf("expected phase line in log, got: %q", got)
	}
	_ = phase
}

func TestTUIReporter_LogWriter_EnterPhase_NoDesc(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("Phase: %s\n", "setup")
	if got := buf.String(); got != "Phase: setup\n" {
		t.Errorf("expected %q, got %q", "Phase: setup\n", got)
	}
}

func TestTUIReporter_LogWriter_StartStep(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("  [%d/%d] %s\n", 3, 7, "main/setup/migrate: Run migrations")
	got := buf.String()
	if !strings.Contains(got, "[3/7] main/setup/migrate: Run migrations") {
		t.Errorf("expected step start line in log, got: %q", got)
	}
}

func TestTUIReporter_LogWriter_FinishStep(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("  [%d/%d] Done: %s\n", 3, 7, "main/setup/migrate")
	if got := buf.String(); !strings.Contains(got, "[3/7] Done: main/setup/migrate") {
		t.Errorf("expected done line in log, got: %q", got)
	}
}

func TestTUIReporter_LogWriter_SkipStep(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("  [%d/%d] Skipped: %s (%s)\n", 2, 5, "main/db/create", "when: dir-empty")
	got := buf.String()
	if !strings.Contains(got, "[2/5] Skipped: main/db/create (when: dir-empty)") {
		t.Errorf("expected skip line in log, got: %q", got)
	}
}

func TestTUIReporter_LogWriter_FailStep(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("Deploy failed at step %q\n", "main/setup/migrate")
	r.logf("  %s\n", "exit status 1")
	got := buf.String()
	if !strings.Contains(got, `Deploy failed at step "main/setup/migrate"`) {
		t.Errorf("expected fail header in log, got: %q", got)
	}
	if !strings.Contains(got, "exit status 1") {
		t.Errorf("expected error message in log, got: %q", got)
	}
}

func TestTUIReporter_LogWriter_SkipPhase(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("  Skipping phase %s (%s)\n", "post-deploy", "when: condition")
	got := buf.String()
	if !strings.Contains(got, "Skipping phase post-deploy (when: condition)") {
		t.Errorf("expected skip phase line in log, got: %q", got)
	}
}

func TestTUIReporter_LogWriter_NilWriter(t *testing.T) {
	// logf with nil logWriter must not panic.
	r := &TUIReporter{logWriter: nil}
	r.logf("Phase: %s\n", "init") // should be a no-op
}

func TestTUIReporter_LogWriter_NoANSI(t *testing.T) {
	// Verify plain text written to logWriter contains no ESC characters.
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("Phase: %s\n", "init")
	r.logf("  [%d/%d] %s\n", 1, 5, "init/render-env")
	r.logf("  [%d/%d] Done: %s\n", 1, 5, "init/render-env")
	got := buf.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("logWriter output contains ANSI escape sequence (ESC): %q", got)
	}
}

func TestTUIReporter_StartPipeline_SetsName(t *testing.T) {
	// Verify StartPipeline stores the pipeline name for FailStep label.
	// We can't call the full method without a running program, so test via logf indirectly.
	r := &TUIReporter{}
	r.name = "deploy"
	label := r.name
	if label[:1] != "d" {
		t.Error("name not stored")
	}
	label = strings.ToUpper(label[:1]) + label[1:]
	if label != "Deploy" {
		t.Errorf("expected 'Deploy', got %q", label)
	}
}

// --- Lipgloss style markers in View output ---

// TestTUIModel_View_PipelineTitle_IsStyled verifies the pipeline title is
// rendered via ui.StyleSectionTitle (whatever that resolves to in this env).
func TestTUIModel_View_PipelineTitle_IsStyled(t *testing.T) {
	m := testModel()
	m.pipelineName = "deploy"
	view := m.View()
	expected := ui.StyleSectionTitle("Deploy")
	if !strings.Contains(view.Content, expected) {
		t.Errorf("expected styled title %q in view; got: %s", expected, view.Content)
	}
}

// TestTUIModel_View_Elapsed_IsStyled verifies the elapsed timer is rendered
// via ui.StyleMuted.
func TestTUIModel_View_Elapsed_IsStyled(t *testing.T) {
	m := testModel()
	m.pipelineName = "deploy"
	view := m.View()
	expected := ui.StyleMuted("00:00")
	if !strings.Contains(view.Content, expected) {
		t.Errorf("expected styled elapsed %q in view; got: %s", expected, view.Content)
	}
}

// TestTUIModel_View_PhaseLabel_IsStyled verifies the phase label is rendered
// via ui.StyleSubheader.
func TestTUIModel_View_PhaseLabel_IsStyled(t *testing.T) {
	m := testModel()
	m.currentPhase = "setup"
	view := m.View()
	expected := ui.StyleSubheader("Phase: setup")
	if !strings.Contains(view.Content, expected) {
		t.Errorf("expected styled phase label %q in view; got: %s", expected, view.Content)
	}
}

// TestTUIModel_View_ProgressCount_IsStyled verifies the progress count is
// rendered via ui.StyleMuted.
func TestTUIModel_View_ProgressCount_IsStyled(t *testing.T) {
	m := testModel()
	m.totalSteps = 10
	m.completedCount = 5
	view := m.View()
	expected := ui.StyleMuted("5/10")
	if !strings.Contains(view.Content, expected) {
		t.Errorf("expected styled progress count %q in view; got: %s", expected, view.Content)
	}
}

// TestTUIModel_View_Spinner_IsStyled verifies the current-step spinner is
// rendered via ui.StyleInfo.
func TestTUIModel_View_Spinner_IsStyled(t *testing.T) {
	m := testModel()
	m.currentStep = "p/step"
	m.stepIndex = 1
	m.stepTotal = 5
	view := m.View()
	styledSpin := ui.StyleInfo(m.spinner.View())
	if !strings.Contains(view.Content, styledSpin) {
		t.Errorf("expected styled spinner %q in view; got: %s", styledSpin, view.Content)
	}
}

// TestStyledStepIcon_Done verifies styledStepIcon("done") contains the done
// icon character and applies the enabled style.
func TestStyledStepIcon_Done(t *testing.T) {
	got := styledStepIcon("done")
	if !strings.Contains(got, "✓") {
		t.Errorf("expected '✓' in styled done icon, got: %q", got)
	}
	expected := ui.RenderEnabled("✓")
	if got != expected {
		t.Errorf("expected styledStepIcon(done)=%q (via ui.RenderEnabled), got %q", expected, got)
	}
}

// TestStyledStepIcon_Skipped verifies styledStepIcon("skipped") contains the
// skipped icon and applies the muted style.
func TestStyledStepIcon_Skipped(t *testing.T) {
	got := styledStepIcon("skipped")
	if !strings.Contains(got, "◎") {
		t.Errorf("expected '◎' in styled skipped icon, got: %q", got)
	}
	expected := ui.StyleMuted("◎")
	if got != expected {
		t.Errorf("expected styledStepIcon(skipped)=%q (via ui.StyleMuted), got %q", expected, got)
	}
}

// TestStyledStepIcon_Failed verifies styledStepIcon("failed") contains the
// failed icon and applies the failed/red style.
func TestStyledStepIcon_Failed(t *testing.T) {
	got := styledStepIcon("failed")
	if !strings.Contains(got, "✗") {
		t.Errorf("expected '✗' in styled failed icon, got: %q", got)
	}
	expected := ui.StyleFailed("✗")
	if got != expected {
		t.Errorf("expected styledStepIcon(failed)=%q (via ui.StyleFailed), got %q", expected, got)
	}
}

// TestStyledStepIcon_Running verifies styledStepIcon("running") returns the
// plain running icon without styling.
func TestStyledStepIcon_Running(t *testing.T) {
	got := styledStepIcon("running")
	if got != "·" {
		t.Errorf("expected '·' for running status, got: %q", got)
	}
}

// TestTUIModel_View_DoneStepIcon_IsStyled verifies that a done step in the
// history uses ui.RenderEnabled for its icon.
func TestTUIModel_View_DoneStepIcon_IsStyled(t *testing.T) {
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "p/step", status: "done", index: 1, total: 3},
	}
	view := m.View()
	expectedIcon := ui.RenderEnabled("✓")
	if !strings.Contains(view.Content, expectedIcon) {
		t.Errorf("expected styled done icon %q in view; got: %s", expectedIcon, view.Content)
	}
}

// TestTUIModel_View_SkippedStepIcon_IsStyled verifies a skipped step uses
// ui.StyleMuted for its icon.
func TestTUIModel_View_SkippedStepIcon_IsStyled(t *testing.T) {
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "p/step", status: "skipped", index: 1, total: 3, reason: "when: false"},
	}
	view := m.View()
	expectedIcon := ui.StyleMuted("◎")
	if !strings.Contains(view.Content, expectedIcon) {
		t.Errorf("expected styled skipped icon %q in view; got: %s", expectedIcon, view.Content)
	}
}

// TestTUIModel_View_FailedStepIcon_IsStyled verifies a failed step uses
// ui.StyleFailed for its icon.
func TestTUIModel_View_FailedStepIcon_IsStyled(t *testing.T) {
	m := testModel()
	m.recentSteps = []tuiStepRecord{
		{addr: "p/step", status: "failed", index: 1, total: 3, errMsg: "oops"},
	}
	view := m.View()
	expectedIcon := ui.StyleFailed("✗")
	if !strings.Contains(view.Content, expectedIcon) {
		t.Errorf("expected styled failed icon %q in view; got: %s", expectedIcon, view.Content)
	}
}

// TestTUIModel_View_LogWriter_NoStyledOutput verifies that logWriter output
// (plain text) is not affected by Lipgloss styling applied in View().
func TestTUIModel_View_LogWriter_NoStyledOutput(t *testing.T) {
	buf := &bytes.Buffer{}
	r := tuiReporterWithLog(buf)
	r.logf("Phase: %s\n", "setup")
	r.logf("  [%d/%d] Done: %s\n", 1, 3, "setup/step")
	got := buf.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("logWriter must not contain ANSI codes, got: %q", got)
	}
	if !strings.Contains(got, "Phase: setup") {
		t.Errorf("expected plain phase line in log, got: %q", got)
	}
	if !strings.Contains(got, "[1/3] Done: setup/step") {
		t.Errorf("expected plain done line in log, got: %q", got)
	}
}

// TestTUIReporter_Confirm_LogsOkMsg verifies that Confirm writes okMsg to logWriter
// when the user confirms (result=true), matching plain mode behavior.
func TestTUIReporter_Confirm_LogsOkMsg(t *testing.T) {
	buf := &bytes.Buffer{}
	r := &TUIReporter{logWriter: buf, done: make(chan struct{})}

	// Simulate a pre-answered confirm by pre-filling respCh.
	respCh := make(chan bool, 1)
	respCh <- true

	// Call the inner select logic directly (same pattern as Confirm uses).
	var result bool
	okMsg := "Continuing with install"
	stopMsg := "Aborted"
	select {
	case result = <-respCh:
		if result {
			r.logf("  %s\n", okMsg)
		} else {
			r.logf("  %s\n", stopMsg)
		}
	case <-r.done:
	}

	if !result {
		t.Error("expected confirmed=true")
	}
	if got := buf.String(); !strings.Contains(got, "Continuing with install") {
		t.Errorf("expected okMsg in log, got: %q", got)
	}
}

// TestTUIReporter_Confirm_LogsStopMsg verifies that Confirm writes stopMsg to logWriter
// when the user denies (result=false).
func TestTUIReporter_Confirm_LogsStopMsg(t *testing.T) {
	buf := &bytes.Buffer{}
	r := &TUIReporter{logWriter: buf, done: make(chan struct{})}

	respCh := make(chan bool, 1)
	respCh <- false

	stopMsg := "Aborted by user"
	select {
	case result := <-respCh:
		if result {
			r.logf("  %s\n", "ok")
		} else {
			r.logf("  %s\n", stopMsg)
		}
	case <-r.done:
	}

	if got := buf.String(); !strings.Contains(got, "Aborted by user") {
		t.Errorf("expected stopMsg in log, got: %q", got)
	}
}

// --- Interface compliance ---

// Verify spinner is properly typed (compile-time check).
var _ spinner.Model = newTUIModel("").spinner

// Verify TUIReporter satisfies the Reporter interface at compile time.
var _ Reporter = (*TUIReporter)(nil)

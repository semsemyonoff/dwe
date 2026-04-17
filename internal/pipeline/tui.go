package pipeline

import (
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"devbox-cli/internal/config"
)

// spinnerFrames is the character sequence for the step-in-progress spinner.
var spinnerFrames = []string{"⠋", "⠙", "⠸", "⠴", "⠦", "⠧", "⠇", "⠏"}

// maxRecentSteps is the maximum number of step records shown in the TUI.
const maxRecentSteps = 5

// tuiStepRecord holds the display state for a single pipeline step.
type tuiStepRecord struct {
	addr   string
	status string // "running" | "done" | "skipped" | "failed"
	errMsg string
}

// --- Internal Bubble Tea messages ---

type tuiStartPipelineMsg struct {
	name  string
	total int
}

type tuiEnterPhaseMsg struct {
	phaseKey string
	phase    config.DeployPhase
}

type tuiSkipPhaseMsg struct {
	phaseKey string
	reason   string
}

type tuiStartStepMsg struct {
	addr  string
	step  config.DeployStep
	index int
	total int
}

type tuiSkipStepMsg struct {
	addr   string
	index  int
	total  int
	reason string
}

type tuiFinishStepMsg struct {
	addr  string
	index int
	total int
}

type tuiFailStepMsg struct {
	addr  string
	index int
	total int
	err   error
}

type tuiFinishPipelineMsg struct {
	success bool
}

type tuiTickMsg struct{}

// tuiModel is the Bubble Tea model for the TUI reporter.
// It maintains all pipeline state required to render the progress UI.
type tuiModel struct {
	pipelineName   string
	totalSteps     int
	completedCount int // steps that are done, skipped, or failed
	currentPhase   string
	currentStep    string
	stepIndex      int
	stepTotal      int
	spinnerFrame   int
	recentSteps    []tuiStepRecord // capped at maxRecentSteps
	done           bool
	success        bool
}

// Init starts the spinner tick.
func (m tuiModel) Init() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
		return tuiTickMsg{}
	})
}

// Update handles all TUI state transitions.
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tuiStartPipelineMsg:
		m.pipelineName = msg.name
		m.totalSteps = msg.total
		return m, nil

	case tuiEnterPhaseMsg:
		m.currentPhase = msg.phaseKey
		m.currentStep = ""
		return m, nil

	case tuiSkipPhaseMsg:
		// No visible state change; phase label stays unchanged.
		return m, nil

	case tuiStartStepMsg:
		m.currentStep = msg.addr
		m.stepIndex = msg.index
		m.stepTotal = msg.total
		m.addRecentStep(tuiStepRecord{addr: msg.addr, status: "running"})
		return m, nil

	case tuiSkipStepMsg:
		m.stepIndex = msg.index
		m.stepTotal = msg.total
		m.completedCount++
		m.updateLastStep(msg.addr, "skipped", "")
		if m.currentStep == msg.addr {
			m.currentStep = ""
		}
		return m, nil

	case tuiFinishStepMsg:
		m.stepIndex = msg.index
		m.stepTotal = msg.total
		m.completedCount++
		m.updateLastStep(msg.addr, "done", "")
		if m.currentStep == msg.addr {
			m.currentStep = ""
		}
		return m, nil

	case tuiFailStepMsg:
		m.stepIndex = msg.index
		m.stepTotal = msg.total
		m.completedCount++
		errMsg := ""
		if msg.err != nil {
			errMsg = msg.err.Error()
		}
		m.updateLastStep(msg.addr, "failed", errMsg)
		if m.currentStep == msg.addr {
			m.currentStep = ""
		}
		return m, nil

	case tuiFinishPipelineMsg:
		m.done = true
		m.success = msg.success
		m.currentStep = ""
		return m, tea.Quit

	case tuiTickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		return m, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
			return tuiTickMsg{}
		})
	}
	return m, nil
}

// View renders the current pipeline state.
func (m tuiModel) View() tea.View {
	var b strings.Builder
	b.WriteString("\n")

	if m.pipelineName != "" {
		fmt.Fprintf(&b, "  Deploy: %s\n", m.pipelineName)
	}
	if m.currentPhase != "" {
		fmt.Fprintf(&b, "  Phase:  %s\n", m.currentPhase)
	}
	if m.currentStep != "" {
		spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		fmt.Fprintf(&b, "  %s [%d/%d] %s\n",
			spinner, m.stepIndex, m.stepTotal, m.currentStep)
	}

	if m.totalSteps > 0 {
		bar := progressBar(m.completedCount, m.totalSteps, 20)
		fmt.Fprintf(&b, "  %s %d/%d\n", bar, m.completedCount, m.totalSteps)
	}

	if len(m.recentSteps) > 0 {
		b.WriteString("\n")
		for _, s := range m.recentSteps {
			icon := stepIcon(s.status)
			fmt.Fprintf(&b, "  %s %s\n", icon, s.addr)
		}
	}

	return tea.NewView(b.String())
}

// addRecentStep appends a record, dropping the oldest entry if over the cap.
func (m *tuiModel) addRecentStep(s tuiStepRecord) {
	m.recentSteps = append(m.recentSteps, s)
	if len(m.recentSteps) > maxRecentSteps {
		m.recentSteps = m.recentSteps[len(m.recentSteps)-maxRecentSteps:]
	}
}

// updateLastStep finds the most-recent entry with addr and updates its status.
func (m *tuiModel) updateLastStep(addr, status, errMsg string) {
	for i := len(m.recentSteps) - 1; i >= 0; i-- {
		if m.recentSteps[i].addr == addr {
			m.recentSteps[i].status = status
			m.recentSteps[i].errMsg = errMsg
			return
		}
	}
}

// progressBar returns a fixed-width ASCII progress bar.
//
//	progressBar(3, 10, 20) → "[██████░░░░░░░░░░░░░░] 3/10"
func progressBar(done, total, width int) string {
	if total <= 0 {
		return "[" + strings.Repeat("░", width) + "]"
	}
	filled := min((done*width)/total, width)
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

// stepIcon maps a step status to a single display character.
func stepIcon(status string) string {
	switch status {
	case "done":
		return "✓"
	case "skipped":
		return "◎"
	case "failed":
		return "✗"
	default: // "running" or unknown
		return "·"
	}
}

// TUIReporter implements Reporter using a Bubble Tea program.
//
// The program runs in a background goroutine; pipeline event methods send
// messages to it synchronously via Program.Send. The terminal is released
// before external child processes (SuspendForExec) and reclaimed afterward
// (ResumeAfterExec). For phases marked ui:plain the terminal is released for
// the entire phase duration.
//
// TUI frames are written only to the terminal via the Bubble Tea renderer and
// never reach the log writer.
type TUIReporter struct {
	program      *tea.Program
	wg           sync.WaitGroup
	mu           sync.Mutex
	suspendCount int  // reference count; terminal released when > 0
	inPlainPhase bool // whether the current phase has ui:plain
}

// NewTUIReporter creates and starts a TUIReporter. The Bubble Tea program
// begins running immediately in a background goroutine.
func NewTUIReporter() *TUIReporter {
	r := &TUIReporter{}
	m := tuiModel{}
	r.program = tea.NewProgram(m)
	r.wg.Go(func() {
		_, _ = r.program.Run()
	})
	return r
}

// StartPipeline sends the pipeline header to the TUI model.
func (r *TUIReporter) StartPipeline(name string, totalSteps int) {
	r.program.Send(tuiStartPipelineMsg{name: name, total: totalSteps})
}

// EnterPhase updates the current phase display. If the incoming phase has
// ui:plain the terminal is released so subsequent step output prints normally;
// if the previous phase was plain the terminal is restored first.
func (r *TUIReporter) EnterPhase(phaseKey string, phase config.DeployPhase) {
	r.mu.Lock()
	wasPlain := r.inPlainPhase
	r.inPlainPhase = phase.UI == "plain"
	isNowPlain := r.inPlainPhase
	r.mu.Unlock()

	// Restore terminal before sending the Enter event so the TUI can render
	// the phase transition.
	if wasPlain {
		r.doResume()
	}

	r.program.Send(tuiEnterPhaseMsg{phaseKey: phaseKey, phase: phase})

	// Then release terminal for a plain phase.
	if isNowPlain {
		r.doSuspend()
	}
}

// SkipPhase forwards the skip event to the TUI model.
func (r *TUIReporter) SkipPhase(phaseKey string, phase config.DeployPhase, reason string) {
	r.program.Send(tuiSkipPhaseMsg{phaseKey: phaseKey, reason: reason})
}

// StartStep updates the current step display.
func (r *TUIReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	r.program.Send(tuiStartStepMsg{addr: stepAddr, step: step, index: index, total: total})
}

// SkipStep marks the step as skipped.
func (r *TUIReporter) SkipStep(stepAddr string, _ config.DeployStep, index int, total int, reason string) {
	r.program.Send(tuiSkipStepMsg{addr: stepAddr, index: index, total: total, reason: reason})
}

// FinishStep marks the step as done.
func (r *TUIReporter) FinishStep(stepAddr string, _ config.DeployStep, index int, total int) {
	r.program.Send(tuiFinishStepMsg{addr: stepAddr, index: index, total: total})
}

// FailStep marks the step as failed.
func (r *TUIReporter) FailStep(stepAddr string, _ config.DeployStep, index int, total int, err error) {
	r.program.Send(tuiFailStepMsg{addr: stepAddr, index: index, total: total, err: err})
}

// FinishPipeline sends the done signal and waits for the Bubble Tea program
// to exit cleanly.
func (r *TUIReporter) FinishPipeline(success bool) {
	// Restore terminal if the last phase was plain.
	r.mu.Lock()
	wasPlain := r.inPlainPhase
	r.inPlainPhase = false
	r.mu.Unlock()
	if wasPlain {
		r.doResume()
	}

	r.program.Send(tuiFinishPipelineMsg{success: success})
	r.wg.Wait()
}

// SuspendForExec releases the terminal so an external child process can use it.
// Calls are reference-counted; the terminal is released on the first suspend
// and restored only after the matching number of resumes.
func (r *TUIReporter) SuspendForExec() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doSuspendLocked()
}

// ResumeAfterExec reclaims the terminal after an external child process exits.
func (r *TUIReporter) ResumeAfterExec() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doResumeLocked()
}

// doSuspend suspends without holding the lock.
func (r *TUIReporter) doSuspend() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doSuspendLocked()
}

// doResume resumes without holding the lock.
func (r *TUIReporter) doResume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doResumeLocked()
}

// doSuspendLocked must be called with r.mu held.
func (r *TUIReporter) doSuspendLocked() {
	if r.suspendCount == 0 {
		_ = r.program.ReleaseTerminal()
	}
	r.suspendCount++
}

// doResumeLocked must be called with r.mu held.
func (r *TUIReporter) doResumeLocked() {
	if r.suspendCount > 0 {
		r.suspendCount--
		if r.suspendCount == 0 {
			_ = r.program.RestoreTerminal()
		}
	}
}

package pipeline

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/stopwatch"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

// tuiStepRecord holds the display state for a single pipeline step.
type tuiStepRecord struct {
	addr   string
	status string // "running" | "done" | "skipped" | "failed"
	errMsg string
	index  int    // tracked step index (0 for untracked phase steps)
	total  int    // tracked step total (0 for untracked phase steps)
	reason string // skip reason
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

// tuiConfirmMsg asks the TUI model to display a confirmation prompt.
// The pipeline goroutine blocks on respCh until the user responds.
type tuiConfirmMsg struct {
	message string
	okMsg   string
	stopMsg string
	respCh  chan bool
}

// tuiConfirmResponseSentMsg is a no-op message returned by the command that
// sends a confirmation response, so Bubble Tea has a valid Msg to process.
type tuiConfirmResponseSentMsg struct{}

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
	recentSteps    []tuiStepRecord // full history, no cap
	done           bool
	success        bool

	// confirmation prompt state
	confirmActive  bool
	confirmMessage string
	confirmOkMsg   string
	confirmStopMsg string
	confirmRespCh  chan bool

	// bubbles sub-models
	spinner   spinner.Model
	progress  progress.Model
	stopwatch stopwatch.Model
}

// newTUIModel creates a properly initialized tuiModel with bubbles sub-models.
// color is an ANSI 256-color string (e.g. "203") for the progress bar fill.
func newTUIModel(barColor string) tuiModel {
	fillColor := lipgloss.Color(barColor)
	return tuiModel{
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		progress:  progress.New(progress.WithColors(fillColor), progress.WithoutPercentage()),
		stopwatch: stopwatch.New(stopwatch.WithInterval(time.Second)),
	}
}

// Init starts the spinner tick and the stopwatch.
func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.stopwatch.Init(),
	)
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
		m.addRecentStep(tuiStepRecord{addr: msg.addr, status: "running", index: msg.index, total: msg.total})
		return m, nil

	case tuiSkipStepMsg:
		m.stepIndex = msg.index
		m.stepTotal = msg.total
		m.completedCount++
		m.updateLastStep(msg.addr, "skipped", "", msg.reason)
		if m.currentStep == msg.addr {
			m.currentStep = ""
		}
		return m, nil

	case tuiFinishStepMsg:
		m.stepIndex = msg.index
		m.stepTotal = msg.total
		m.completedCount++
		m.updateLastStep(msg.addr, "done", "", "")
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
		m.updateLastStep(msg.addr, "failed", errMsg, "")
		if m.currentStep == msg.addr {
			m.currentStep = ""
		}
		return m, nil

	case tuiFinishPipelineMsg:
		m.done = true
		m.success = msg.success
		m.currentStep = ""
		return m, tea.Quit

	case tuiConfirmMsg:
		m.confirmActive = true
		m.confirmMessage = msg.message
		m.confirmOkMsg = msg.okMsg
		m.confirmStopMsg = msg.stopMsg
		m.confirmRespCh = msg.respCh
		return m, nil

	case tea.KeyMsg:
		if m.confirmActive {
			ch := m.confirmRespCh
			switch msg.String() {
			case "y", "Y":
				m.confirmActive = false
				m.confirmRespCh = nil
				return m, func() tea.Msg {
					ch <- true
					return tuiConfirmResponseSentMsg{}
				}
			case "n", "N", "esc":
				m.confirmActive = false
				m.confirmRespCh = nil
				return m, func() tea.Msg {
					ch <- false
					return tuiConfirmResponseSentMsg{}
				}
			}
		}
		return m, nil

	case tuiConfirmResponseSentMsg:
		return m, nil

	// Forward sub-model messages.
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case stopwatch.TickMsg:
		var cmd tea.Cmd
		m.stopwatch, cmd = m.stopwatch.Update(msg)
		return m, cmd

	case stopwatch.StartStopMsg:
		var cmd tea.Cmd
		m.stopwatch, cmd = m.stopwatch.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return m, cmd
	}
	return m, nil
}

// View renders the current pipeline state.
func (m tuiModel) View() tea.View {
	var b strings.Builder
	b.WriteString("\n")

	// Confirmation prompt takes over the display.
	if m.confirmActive {
		fmt.Fprintf(&b, "  ⚠  %s\n", m.confirmMessage)
		b.WriteString("  Press [Y] to confirm or [N] to cancel\n")
		return tea.NewView(b.String())
	}

	if m.pipelineName != "" {
		label := strings.ToUpper(m.pipelineName[:1]) + m.pipelineName[1:]
		elapsed := formatElapsed(m.stopwatch.Elapsed())
		fmt.Fprintf(&b, "  %-34s%s\n", label, elapsed)
	}
	if m.currentPhase != "" {
		fmt.Fprintf(&b, "  Phase: %s\n", m.currentPhase)
	}

	if m.totalSteps > 0 {
		percent := float64(m.completedCount) / float64(m.totalSteps)
		bar := m.progress.ViewAs(percent)
		fmt.Fprintf(&b, "  %s  %d/%d\n", bar, m.completedCount, m.totalSteps)
	}

	if m.currentStep != "" {
		spin := m.spinner.View()
		fmt.Fprintf(&b, "  %s [%d/%d] %s\n",
			spin, m.stepIndex, m.stepTotal, m.currentStep)
	}

	if len(m.recentSteps) > 0 {
		b.WriteString("\n")
		for _, s := range m.recentSteps {
			icon := stepIcon(s.status)
			switch s.status {
			case "done":
				if s.index > 0 {
					fmt.Fprintf(&b, "  %s [%d/%d] Done: %s\n", icon, s.index, s.total, s.addr)
				} else {
					fmt.Fprintf(&b, "  %s Done: %s\n", icon, s.addr)
				}
			case "skipped":
				if s.index > 0 {
					if s.reason != "" {
						fmt.Fprintf(&b, "  %s [%d/%d] Skipped: %s (%s)\n", icon, s.index, s.total, s.addr, s.reason)
					} else {
						fmt.Fprintf(&b, "  %s [%d/%d] Skipped: %s\n", icon, s.index, s.total, s.addr)
					}
				} else {
					if s.reason != "" {
						fmt.Fprintf(&b, "  %s Skipped: %s (%s)\n", icon, s.addr, s.reason)
					} else {
						fmt.Fprintf(&b, "  %s Skipped: %s\n", icon, s.addr)
					}
				}
			case "failed":
				if s.index > 0 {
					fmt.Fprintf(&b, "  %s [%d/%d] Failed: %s\n", icon, s.index, s.total, s.addr)
				} else {
					fmt.Fprintf(&b, "  %s Failed: %s\n", icon, s.addr)
				}
				if s.errMsg != "" {
					fmt.Fprintf(&b, "    %s\n", s.errMsg)
				}
			default: // "running"
				if s.index > 0 {
					fmt.Fprintf(&b, "  %s [%d/%d] %s\n", icon, s.index, s.total, s.addr)
				} else {
					fmt.Fprintf(&b, "  %s %s\n", icon, s.addr)
				}
			}
		}
	}

	return tea.NewView(b.String())
}

// formatElapsed formats a duration as MM:SS for the TUI timer display.
func formatElapsed(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	mins := totalSeconds / 60
	secs := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d", mins, secs)
}

// addRecentStep appends a record to the full step history (no cap).
func (m *tuiModel) addRecentStep(s tuiStepRecord) {
	m.recentSteps = append(m.recentSteps, s)
}

// updateLastStep finds the most-recent entry with addr and updates its status, errMsg, and reason.
func (m *tuiModel) updateLastStep(addr, status, errMsg, reason string) {
	for i := len(m.recentSteps) - 1; i >= 0; i-- {
		if m.recentSteps[i].addr == addr {
			m.recentSteps[i].status = status
			m.recentSteps[i].errMsg = errMsg
			m.recentSteps[i].reason = reason
			return
		}
	}
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
// never reach the log writer. Plain-text lifecycle events are mirrored to
// logWriter (if non-nil) in the same format as PlainReporter so log files
// contain readable output with no escape sequences.
type TUIReporter struct {
	program      *tea.Program
	wg           sync.WaitGroup
	mu           sync.Mutex
	suspendCount int       // reference count; terminal released when > 0
	inPlainPhase bool      // whether the current phase has ui:plain
	logWriter    io.Writer // receives plain-text lifecycle events (may be nil)
	name         string    // pipeline name set by StartPipeline (for FailStep label)
}

// NewTUIReporter creates and starts a TUIReporter. The Bubble Tea program
// begins running immediately in a background goroutine.
// The progress bar color is read from ui.ProgressBarColor() at the time of creation.
// logWriter, if non-nil, receives plain-text lifecycle events so log files
// match PlainReporter format without escape sequences.
func NewTUIReporter(logWriter io.Writer) *TUIReporter {
	r := &TUIReporter{logWriter: logWriter}
	m := newTUIModel(ui.ProgressBarColor())
	r.program = tea.NewProgram(m)
	r.wg.Go(func() {
		_, _ = r.program.Run()
	})
	return r
}

// logf writes a plain-text line to the logWriter if one is set.
func (r *TUIReporter) logf(format string, args ...any) {
	if r.logWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(r.logWriter, format, args...)
}

// StartPipeline sends the pipeline header to the TUI model.
func (r *TUIReporter) StartPipeline(name string, totalSteps int) {
	r.name = name
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

	// Mirror to log file.
	label := "Phase: " + phaseKey
	if phase.Description != "" {
		label += ": " + phase.Description
	}
	r.logf("%s\n", label)

	// Then release terminal for a plain phase.
	if isNowPlain {
		r.doSuspend()
	}
}

// SkipPhase forwards the skip event to the TUI model.
func (r *TUIReporter) SkipPhase(phaseKey string, phase config.DeployPhase, reason string) {
	r.program.Send(tuiSkipPhaseMsg{phaseKey: phaseKey, reason: reason})
	r.logf("  Skipping phase %s (%s)\n", phaseKey, reason)
}

// StartStep updates the current step display.
func (r *TUIReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	r.program.Send(tuiStartStepMsg{addr: stepAddr, step: step, index: index, total: total})
	label := stepAddr
	if step.Description != "" {
		label += ": " + step.Description
	}
	r.logf("  [%d/%d] %s\n", index, total, label)
}

// SkipStep marks the step as skipped.
func (r *TUIReporter) SkipStep(stepAddr string, _ config.DeployStep, index int, total int, reason string) {
	r.program.Send(tuiSkipStepMsg{addr: stepAddr, index: index, total: total, reason: reason})
	r.logf("  [%d/%d] Skipped: %s (%s)\n", index, total, stepAddr, reason)
}

// FinishStep marks the step as done.
func (r *TUIReporter) FinishStep(stepAddr string, _ config.DeployStep, index int, total int) {
	r.program.Send(tuiFinishStepMsg{addr: stepAddr, index: index, total: total})
	r.logf("  [%d/%d] Done: %s\n", index, total, stepAddr)
}

// FailStep marks the step as failed.
func (r *TUIReporter) FailStep(stepAddr string, _ config.DeployStep, index int, total int, err error) {
	r.program.Send(tuiFailStepMsg{addr: stepAddr, index: index, total: total, err: err})
	label := r.name
	if label == "" {
		label = "pipeline"
	}
	label = strings.ToUpper(label[:1]) + label[1:]
	r.logf("%s failed at step %q\n", label, stepAddr)
	if err != nil {
		r.logf("  %s\n", err.Error())
	}
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

// Confirm sends a TUI confirmation prompt and blocks until the user responds.
// It is used by the confirm builtin in TUI mode instead of the stdin-based prompt.
// The TUI must not be suspended when this is called (i.e. the caller must not
// have called SuspendForExec for this step).
func (r *TUIReporter) Confirm(message, okMsg, stopMsg string) (bool, error) {
	respCh := make(chan bool, 1)
	r.program.Send(tuiConfirmMsg{
		message: message,
		okMsg:   okMsg,
		stopMsg: stopMsg,
		respCh:  respCh,
	})
	result := <-respCh
	return result, nil
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

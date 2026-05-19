package pipeline

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// Icons used in step output lines.
const (
	iconDone    = "✓"
	iconFailed  = "✗"
	iconSkipped = "◎"
	iconRunning = "·"
)

// timestampLayout is the per-line clock prefix format (YY-MM-DD HH:MM:SS).
const timestampLayout = "06-01-02 15:04:05"

// PlainReporter implements Reporter with line-by-line text output.
// Every emitted line is prefixed with a gray "[YY-MM-DD HH:MM:SS] "
// timestamp followed by the colored message.
//
// Output format:
//
//	[ts] Phase: <phaseKey>[: <description>]
//	[ts]   · [N/M] <stepAddr>[: <description>]
//	[ts]   ✓ [N/M] Done: <stepAddr>
//	[ts]   ◎ [N/M] Skipped: <stepAddr> (<reason>)
//	[ts] ✗ Deploy failed at step "<stepAddr>"
//	[ts]   <error message>
//	[ts] ✓ Done (1m 23s)
//
// SuspendForExec and ResumeAfterExec are no-ops: plain text output does not
// need to yield or reclaim the terminal.
// subStepEntry holds buffered output for a single parallel sub-step in the
// non-TTY PlainReporter path. The group association lets FinishStep/FailStep
// update the parent group's aggregate counters.
type subStepEntry struct {
	groupAddr string
	buf       bytes.Buffer
}

// groupEntry tracks per-parallel-group aggregate state for the FinishGroup
// summary line (success counts + elapsed).
type groupEntry struct {
	startTime time.Time
	total     int
	ok        int
	failed    int
	skipped   int
}

// parallelOutputTopBar is the separator emitted before a buffered sub-step's
// captured output, and parallelOutputBotBar follows it.
const (
	parallelOutputTopBar = "  ───── output ─────"
	parallelOutputBotBar = "  ──────────────────"
)

// PlainReporter is the default pipeline Reporter implementation.
// It writes status icons (✓ ✗ ◎ ·) and elapsed-time footers via a render.Writer.
// In TTY mode it drives a per-group bubbletea live view for parallel sub-steps;
// in non-TTY mode it buffers sub-step output and dumps it between separator lines
// on FinishStep/FailStep/SkipStep. All methods are safe for concurrent use.
type PlainReporter struct {
	mu        sync.Mutex // guards every write to w and any future shared state
	w         *render.Writer
	name      string           // pipeline name set by StartPipeline (e.g. "deploy", "reset")
	startTime time.Time        // recorded by StartPipeline for elapsed time in FinishPipeline
	now       func() time.Time // injectable clock; defaults to time.Now

	// tty selects the parallel-group rendering branch. false = Task 8 buffered
	// non-TTY path; true = Task 9 live bubbletea view.
	tty bool

	// subs holds buffered output for parallel sub-steps that have not yet
	// completed. Keyed by full sub-step address.
	subs map[string]*subStepEntry

	// groups holds per-parallel-group aggregate state for FinishGroup.
	groups map[string]*groupEntry

	// ttyProg is the bubbletea program rendering the currently active
	// parallel group's live view (TTY mode only); nil between groups.
	ttyProg *tea.Program
	// ttyDone is closed when ttyProg.Run() returns.
	ttyDone chan struct{}
	// ttyGroupAddr is the address of the currently active group; "" between
	// groups. ttySubs maps sub-step address → row state used to print the
	// post-group summary after the live view exits.
	ttyGroupAddr string
	ttySubs      map[string]*ttySubStepEntry
}

// ttySubStepEntry tracks per-sub-step state needed to print the summary
// lines after the bubbletea live view ends.
type ttySubStepEntry struct {
	idx    int
	total  int
	name   string
	status subStepStatus // statusOk / statusFailed / statusSkipped
	reason string
	err    error
	finish time.Time
}

// NewPlainReporter creates a PlainReporter that writes to w.
func NewPlainReporter(w *render.Writer) *PlainReporter {
	return &PlainReporter{w: w, now: time.Now, tty: stdoutIsTTY()}
}

// StartPipeline stores the pipeline name and records the start time for
// elapsed time reporting. It does not print a header; the current deploy/reset
// output has no pipeline banner.
func (r *PlainReporter) StartPipeline(name string, _ int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.name = name
	r.startTime = r.now()
}

// EnterPhase prints the phase label line:
//
//	[ts] Phase: <phaseKey>[: <description>]
//
// Untracked phases produce no output.
func (r *PlainReporter) EnterPhase(phaseKey string, phase config.DeployPhase) {
	if phase.Untracked {
		return
	}
	label := "Phase: " + phaseKey
	if phase.Description != "" {
		label += ": " + phase.Description
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emit(render.Blue, label)
}

// SkipPhase prints a warning when an entire phase is skipped:
//
//	[ts] Skipping phase <phaseKey> (<reason>)
//
// Untracked phases produce no output.
func (r *PlainReporter) SkipPhase(phaseKey string, phase config.DeployPhase, reason string) {
	if phase.Untracked {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emit(render.Yellow, fmt.Sprintf("  Skipping phase %s (%s)", phaseKey, reason))
}

// StartStep prints the step-start info line:
//
//	[ts]   · [N/M] <stepAddr>[: <description>]
//
// Untracked steps (index == 0, total == 0) produce no output. In TTY mode a
// parallel sub-step's StartStep is suppressed (the live view already shows the
// row in running state).
func (r *PlainReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	if index == 0 && total == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tty && r.ttyProg != nil {
		if _, ok := r.ttySubs[stepAddr]; ok {
			return
		}
	}
	label := stepAddr
	if step.Description != "" {
		label += ": " + step.Description
	}
	if index > 0 {
		r.emit(render.Blue, fmt.Sprintf("  %s [%d/%d] %s", iconRunning, index, total, label))
	} else {
		r.emit(render.Blue, fmt.Sprintf("  %s %s", iconRunning, label))
	}
}

// SkipStep prints a warning when a step is skipped due to a when condition:
//
//	[ts]   ◎ [N/M] Skipped: <stepAddr> (<reason>)
//
// Untracked steps (index == 0, total == 0) produce no output. A parallel
// sub-step skip discards any (likely empty) buffered output without dumping.
func (r *PlainReporter) SkipStep(stepAddr string, _ config.DeployStep, index int, total int, reason string) {
	if index == 0 && total == 0 {
		return
	}
	r.mu.Lock()
	if r.tty && r.ttyProg != nil {
		if entry, ok := r.ttySubs[stepAddr]; ok {
			entry.status = statusSkipped
			entry.reason = reason
			entry.finish = r.now()
			prog := r.ttyProg
			r.mu.Unlock()
			prog.Send(subStepSkipMsg{addr: stepAddr, reason: reason})
			return
		}
	}
	defer r.mu.Unlock()
	if index > 0 {
		r.emit(render.Yellow, fmt.Sprintf("  %s [%d/%d] Skipped: %s (%s)", iconSkipped, index, total, stepAddr, reason))
	} else {
		r.emit(render.Yellow, fmt.Sprintf("  %s Skipped: %s (%s)", iconSkipped, stepAddr, reason))
	}
	r.dropSubStepLocked(stepAddr, statusSkipped)
}

// FinishStep prints a success line when a step completes:
//
//	[ts]   ✓ [N/M] Done: <stepAddr>
//
// Untracked steps (index == 0, total == 0) produce no output. When the step
// is a parallel sub-step (registered by StartGroup) in non-TTY mode, any
// buffered output captured via SubStepOutput is dumped between separator bars
// after the status line.
func (r *PlainReporter) FinishStep(stepAddr string, _ config.DeployStep, index int, total int) {
	if index == 0 && total == 0 {
		return
	}
	r.mu.Lock()
	if r.tty && r.ttyProg != nil {
		if entry, ok := r.ttySubs[stepAddr]; ok {
			entry.status = statusOk
			entry.finish = r.now()
			prog := r.ttyProg
			r.mu.Unlock()
			prog.Send(subStepDoneMsg{addr: stepAddr, ok: true})
			return
		}
	}
	defer r.mu.Unlock()
	if index > 0 {
		r.emit(render.Green, fmt.Sprintf("  %s [%d/%d] Done: %s", iconDone, index, total, stepAddr))
	} else {
		r.emit(render.Green, fmt.Sprintf("  %s Done: %s", iconDone, stepAddr))
	}
	r.flushSubStepLocked(stepAddr, statusOk)
}

// FailStep prints error lines when a step fails:
//
//	[ts] ✗ <Name> failed at step "<stepAddr>"
//	[ts]   <error message>
//
// The label is derived from the pipeline name set by StartPipeline (e.g.
// "deploy" → "Deploy failed…", "reset" → "Reset failed…"). Falls back to
// "Pipeline" if StartPipeline was not called.
func (r *PlainReporter) FailStep(stepAddr string, _ config.DeployStep, index int, total int, err error) {
	r.mu.Lock()

	// Parallel sub-step failure in TTY mode: defer the formatted error block
	// to the post-group summary so it does not collide with the live view.
	if r.tty && r.ttyProg != nil {
		if entry, ok := r.ttySubs[stepAddr]; ok {
			entry.status = statusFailed
			entry.err = err
			entry.finish = r.now()
			msg := subStepDoneMsg{addr: stepAddr, ok: false}
			if err != nil {
				msg.err = err.Error()
			}
			prog := r.ttyProg
			r.mu.Unlock()
			prog.Send(msg)
			return
		}
	}
	defer r.mu.Unlock()

	// Parallel sub-step failure in non-TTY mode: print compact status line,
	// dump captured output between separator bars, then the error.
	if !r.tty {
		if _, isSub := r.subs[stepAddr]; isSub {
			if index > 0 {
				r.emit(render.Red, fmt.Sprintf("  %s [%d/%d] Failed: %s", iconFailed, index, total, stepAddr))
			} else {
				r.emit(render.Red, fmt.Sprintf("  %s Failed: %s", iconFailed, stepAddr))
			}
			r.flushSubStepLocked(stepAddr, statusFailed)
			if err != nil {
				r.emit(render.Red, "  "+err.Error())
			}
			return
		}
	}

	label := r.name
	if label == "" {
		label = "pipeline"
	}
	label = strings.ToUpper(label[:1]) + label[1:]
	r.emit(render.Red, fmt.Sprintf("%s %s failed at step %q", iconFailed, label, stepAddr))
	if err != nil {
		r.emit(render.Red, "  "+err.Error())
	}
}

// FinishPipeline prints a Done message with elapsed time on success:
//
//	[ts] ✓ Done (1m 23s)
//
// On failure it is silent; the failure is already reported by FailStep.
func (r *PlainReporter) FinishPipeline(success bool) {
	if !success {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	elapsed := formatElapsed(r.now().Sub(r.startTime))
	_, _ = fmt.Fprintf(r.w.Writer(), "%s%s %s Done%s %s(%s)%s\n",
		r.timestampPrefix(),
		render.Green, iconDone, render.Reset,
		render.Gray, elapsed, render.Reset,
	)
}

// emit writes a single line with the timestamp prefix and a colored body.
// Format: "<gray>[ts]<reset> <color>msg<reset>\n".
func (r *PlainReporter) emit(color, msg string) {
	_, _ = fmt.Fprintf(r.w.Writer(), "%s%s%s%s\n", r.timestampPrefix(), color, msg, render.Reset)
}

// timestampPrefix returns the gray "[YY-MM-DD HH:MM:SS] " prefix for the
// current line. Always ends with a trailing space.
func (r *PlainReporter) timestampPrefix() string {
	return fmt.Sprintf("%s[%s]%s ", render.Gray, r.now().Format(timestampLayout), render.Reset)
}

// formatElapsed formats a duration as a human-readable elapsed time string.
// Examples: "5s", "1m 23s", "2h 5m".
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// SuspendForExec is a no-op for PlainReporter.
func (r *PlainReporter) SuspendForExec() {}

// ResumeAfterExec is a no-op for PlainReporter.
func (r *PlainReporter) ResumeAfterExec() {}

// StartGroup prints a single header line announcing a parallel group:
//
//	[ts]   · Parallel group: <groupAddr> (<n> steps)
//
// Per-sub-step lifecycle events still flow through StartStep / FinishStep /
// FailStep / SkipStep; in non-TTY mode this also pre-registers a per-sub-step
// output buffer and records the group's start time / total for the
// FinishGroup summary line.
func (r *PlainReporter) StartGroup(groupAddr string, group config.DeployStep, subIndices []int, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	label := groupAddr
	if group.Description != "" {
		label += ": " + group.Description
	}
	r.emit(render.Blue, fmt.Sprintf("  %s Parallel group: %s (%d steps)", iconRunning, label, len(subIndices)))

	if r.groups == nil {
		r.groups = make(map[string]*groupEntry)
	}
	r.groups[groupAddr] = &groupEntry{
		startTime: r.now(),
		total:     len(subIndices),
	}

	phasePrefix := groupAddr
	if i := strings.LastIndex(groupAddr, "/"); i >= 0 {
		phasePrefix = groupAddr[:i]
	}

	if r.tty {
		r.startTTYView(groupAddr, group, subIndices, phasePrefix, total)
		return
	}

	if r.subs == nil {
		r.subs = make(map[string]*subStepEntry)
	}
	if group.Parallel != nil {
		for _, sub := range group.Parallel.Steps {
			subAddr := phasePrefix + "/" + sub.Name
			r.subs[subAddr] = &subStepEntry{groupAddr: groupAddr}
		}
	}
}

// startTTYView builds the parallelGroupModel, registers sub-step entries, and
// launches a bubbletea program in a goroutine. Caller must hold r.mu.
func (r *PlainReporter) startTTYView(groupAddr string, group config.DeployStep, subIndices []int, phasePrefix string, total int) {
	r.ttySubs = make(map[string]*ttySubStepEntry)
	views := make([]subStepView, 0, len(subIndices))
	if group.Parallel != nil {
		for i, sub := range group.Parallel.Steps {
			subAddr := phasePrefix + "/" + sub.Name
			idx := 0
			if i < len(subIndices) {
				idx = subIndices[i]
			}
			r.ttySubs[subAddr] = &ttySubStepEntry{
				idx:   idx,
				total: total,
				name:  sub.Name,
			}
			views = append(views, subStepView{
				addr:   subAddr,
				idx:    idx,
				total:  total,
				name:   sub.Name,
				status: subStatusRunning,
			})
		}
	}
	model := newParallelGroupModel(groupAddr, views)
	prog := tea.NewProgram(model,
		tea.WithOutput(r.w.Writer()),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
		tea.WithInput(bytes.NewReader(nil)),
	)
	r.ttyProg = prog
	r.ttyGroupAddr = groupAddr
	r.ttyDone = make(chan struct{})
	go func() {
		defer close(r.ttyDone)
		_, _ = prog.Run()
	}()
}

// FinishGroup prints a one-line footer for a parallel group. success is true
// when every sub-step succeeded (after accounting for continue_on_error). In
// non-TTY mode the footer also carries aggregate ok/failed/skipped counts and
// the elapsed time since StartGroup.
func (r *PlainReporter) FinishGroup(groupAddr string, _ config.DeployStep, success bool) {
	r.mu.Lock()
	if r.tty && r.ttyProg != nil && r.ttyGroupAddr == groupAddr {
		prog := r.ttyProg
		done := r.ttyDone
		r.mu.Unlock()
		// Tell the live view to exit and wait without holding r.mu so the
		// tea program can finish rendering through r.w.Writer.
		prog.Send(groupDoneMsg{})
		<-done
		r.mu.Lock()
		r.ttyProg = nil
		r.ttyDone = nil
		r.ttyGroupAddr = ""
		// Tally counts from ttySubs and emit summary lines.
		var ok, failed, skipped int
		for _, e := range r.ttySubs {
			switch e.status {
			case statusOk:
				ok++
			case statusFailed:
				failed++
			case statusSkipped:
				skipped++
			}
		}
		// Print per-sub-step result lines in declaration order.
		for _, e := range orderedTTYSubs(r.ttySubs) {
			switch e.status {
			case statusOk:
				if e.idx > 0 {
					r.emit(render.Green, fmt.Sprintf("  %s [%d/%d] Done: %s", iconDone, e.idx, e.total, e.name))
				} else {
					r.emit(render.Green, fmt.Sprintf("  %s Done: %s", iconDone, e.name))
				}
			case statusFailed:
				if e.idx > 0 {
					r.emit(render.Red, fmt.Sprintf("  %s [%d/%d] Failed: %s", iconFailed, e.idx, e.total, e.name))
				} else {
					r.emit(render.Red, fmt.Sprintf("  %s Failed: %s", iconFailed, e.name))
				}
				if e.err != nil {
					r.emit(render.Red, "  "+e.err.Error())
				}
			case statusSkipped:
				if e.idx > 0 {
					r.emit(render.Yellow, fmt.Sprintf("  %s [%d/%d] Skipped: %s (%s)", iconSkipped, e.idx, e.total, e.name, e.reason))
				} else {
					r.emit(render.Yellow, fmt.Sprintf("  %s Skipped: %s (%s)", iconSkipped, e.name, e.reason))
				}
			case statusPending:
				if e.idx > 0 {
					r.emit(render.Yellow, fmt.Sprintf("  %s [%d/%d] Cancelled: %s", iconSkipped, e.idx, e.total, e.name))
				} else {
					r.emit(render.Yellow, fmt.Sprintf("  %s Cancelled: %s", iconSkipped, e.name))
				}
			}
		}
		// Footer: success/failure with aggregate counts + elapsed.
		icon := iconDone
		color := render.Green
		verb := "done"
		if !success {
			icon = iconFailed
			color = render.Red
			verb = "failed"
		}
		msg := fmt.Sprintf("  %s Parallel group %s: %s", icon, verb, groupAddr)
		if g, gok := r.groups[groupAddr]; gok {
			elapsed := formatElapsed(r.now().Sub(g.startTime))
			msg += fmt.Sprintf(" (%d ok, %d failed, %d skipped of %d, %s)",
				ok, failed, skipped, g.total, elapsed)
			delete(r.groups, groupAddr)
		}
		r.emit(color, msg)
		r.ttySubs = nil
		r.mu.Unlock()
		return
	}
	defer r.mu.Unlock()

	icon := iconDone
	color := render.Green
	verb := "done"
	if !success {
		icon = iconFailed
		color = render.Red
		verb = "failed"
	}

	msg := fmt.Sprintf("  %s Parallel group %s: %s", icon, verb, groupAddr)
	if !r.tty {
		if g, ok := r.groups[groupAddr]; ok {
			elapsed := formatElapsed(r.now().Sub(g.startTime))
			msg += fmt.Sprintf(" (%d ok, %d failed, %d skipped of %d, %s)",
				g.ok, g.failed, g.skipped, g.total, elapsed)
			delete(r.groups, groupAddr)
		}
		// Clean up any sub-step entries that never received a terminal event
		// (e.g. cancelled by FailFast before executeStepBody was entered).
		for addr, e := range r.subs {
			if e.groupAddr == groupAddr {
				delete(r.subs, addr)
			}
		}
	}
	r.emit(color, msg)
}

// orderedTTYSubs returns the ttySubs entries sorted by declaration index so
// the post-group summary prints in a stable order matching the YAML.
func orderedTTYSubs(m map[string]*ttySubStepEntry) []*ttySubStepEntry {
	out := make([]*ttySubStepEntry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	// Insertion sort on idx — N is tiny.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].idx > out[j].idx; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// SubStepOutput appends a single line of captured sub-step output to the
// non-TTY mode's per-sub-step buffer. Never writes to the terminal directly;
// the buffer is dumped between separator bars by FinishStep / FailStep. In
// TTY mode (Task 9) this is a no-op; the live view consumes events
// independently.
//
// If subAddr is unknown (e.g. StartGroup was skipped due to an upstream bug
// or a sequential test calls SubStepOutput) the entry is created on the fly
// so we never panic.
func (r *PlainReporter) SubStepOutput(subAddr string, line string) {
	r.mu.Lock()
	if r.tty {
		prog := r.ttyProg
		r.mu.Unlock()
		if prog != nil {
			prog.Send(subStepOutputMsg{addr: subAddr, line: line})
		}
		return
	}
	defer r.mu.Unlock()
	if r.subs == nil {
		r.subs = make(map[string]*subStepEntry)
	}
	entry, ok := r.subs[subAddr]
	if !ok {
		entry = &subStepEntry{}
		r.subs[subAddr] = entry
	}
	entry.buf.WriteString(line)
	entry.buf.WriteByte('\n')
}

// subStepStatus categorises a sub-step's outcome for group counter updates.
type subStepStatus int

const (
	// statusPending is the zero value: sub-step has not yet received a terminal
	// event. This can happen when a FailFast cancellation races ahead of the
	// sub-step completing, or when a template when: filters the sub-step at
	// plan time but it is still registered in the reporter map.
	statusPending subStepStatus = iota
	statusOk
	statusFailed
	statusSkipped
)

// flushSubStepLocked dumps the captured output for a parallel sub-step (if
// any) between separator bars, updates the group counters, and frees the
// buffer. Caller must hold r.mu. No-op in TTY mode or for non-parallel
// addresses.
func (r *PlainReporter) flushSubStepLocked(addr string, status subStepStatus) {
	if r.tty {
		return
	}
	entry, ok := r.subs[addr]
	if !ok {
		return
	}
	if entry.buf.Len() > 0 {
		r.emit(render.Gray, parallelOutputTopBar)
		_, _ = fmt.Fprint(r.w.Writer(), entry.buf.String())
		r.emit(render.Gray, parallelOutputBotBar)
	}
	r.updateGroupCounterLocked(entry.groupAddr, status)
	delete(r.subs, addr)
}

// dropSubStepLocked discards a parallel sub-step's buffered output without
// dumping it (used for skipped sub-steps where no output is expected) and
// updates the group counters. Caller must hold r.mu.
func (r *PlainReporter) dropSubStepLocked(addr string, status subStepStatus) {
	if r.tty {
		return
	}
	entry, ok := r.subs[addr]
	if !ok {
		return
	}
	r.updateGroupCounterLocked(entry.groupAddr, status)
	delete(r.subs, addr)
}

func (r *PlainReporter) updateGroupCounterLocked(groupAddr string, status subStepStatus) {
	if groupAddr == "" {
		return
	}
	g, ok := r.groups[groupAddr]
	if !ok {
		return
	}
	switch status {
	case statusOk:
		g.ok++
	case statusFailed:
		g.failed++
	case statusSkipped:
		g.skipped++
	}
}

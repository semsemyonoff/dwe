package pipeline

import (
	"fmt"
	"strings"
	"sync"
	"time"

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
// subStepEntry holds buffered output for a single parallel sub-step.
// The group association lets FinishStep/FailStep update the parent group's
// aggregate counters.
//
// flushed is set to true after FinishStep/FailStep/SkipStep has dumped and
// cleared the buffer. If SubStepOutput is called after that point (e.g. from
// lineTee.Flush() on a trailing non-newline-terminated line), the output is
// written directly to the terminal rather than being silently dropped.
type subStepEntry struct {
	groupAddr string
	buf       strings.Builder
	flushed   bool
}

// groupEntry tracks per-parallel-group aggregate state for the FinishGroup
// summary line (success counts + elapsed).
type groupEntry struct {
	startTime time.Time
	total     int
	ok        int
	failed    int
	skipped   int
	cancelled int
}

// parallelOutputTopBar is the separator emitted before a buffered sub-step's
// captured output, and parallelOutputBotBar follows it.
const (
	parallelOutputTopBar = "  ───── output ─────"
	parallelOutputBotBar = "  ──────────────────"
)

// PlainReporter is the default pipeline Reporter implementation.
// It writes status icons (✓ ✗ ◎ ·) and elapsed-time footers via a render.Writer.
// Parallel sub-step output is buffered per sub-step and dumped between separator
// lines on FinishStep/FailStep. All methods are safe for concurrent use.
type PlainReporter struct {
	mu        sync.Mutex // guards every write to w and any future shared state
	w         *render.Writer
	name      string           // pipeline name set by StartPipeline (e.g. "deploy", "reset")
	startTime time.Time        // recorded by StartPipeline for elapsed time in FinishPipeline
	now       func() time.Time // injectable clock; defaults to time.Now

	// subs holds buffered output for parallel sub-steps that have not yet
	// completed. Keyed by full sub-step address.
	subs map[string]*subStepEntry

	// groups holds per-parallel-group aggregate state for FinishGroup.
	groups map[string]*groupEntry
}

// NewPlainReporter creates a PlainReporter that writes to w.
func NewPlainReporter(w *render.Writer) *PlainReporter {
	return &PlainReporter{w: w, now: time.Now}
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
// Untracked steps (index == 0, total == 0) produce no output.
func (r *PlainReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	if index == 0 && total == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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
// is a parallel sub-step (registered by StartGroup), any buffered output
// captured via SubStepOutput is dumped between separator bars after the
// status line.
func (r *PlainReporter) FinishStep(stepAddr string, _ config.DeployStep, index int, total int) {
	if index == 0 && total == 0 {
		return
	}
	r.mu.Lock()
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
//
// Parallel sub-step failures (those with a registered subStepEntry) print a
// compact "Failed: <addr>" line, dump captured output between separator bars,
// then emit the error message; the per-pipeline failure banner is reserved
// for top-level (non-sub-step) failures.
func (r *PlainReporter) FailStep(stepAddr string, _ config.DeployStep, index int, total int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

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
// FailStep / SkipStep; this also pre-registers a per-sub-step output buffer
// and records the group's start time / total for the FinishGroup summary line.
func (r *PlainReporter) StartGroup(groupAddr string, group config.DeployStep, subIndices []int, _ int) {
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

// FinishGroup prints a one-line footer for a parallel group. success is true
// when every sub-step succeeded (after accounting for continue_on_error). The
// footer carries aggregate ok/failed/skipped counts and the elapsed time
// since StartGroup.
func (r *PlainReporter) FinishGroup(groupAddr string, _ config.DeployStep, success bool) {
	r.mu.Lock()
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
	if g, ok := r.groups[groupAddr]; ok {
		// Count sub-steps that never received a terminal event (cancelled
		// by FailFast before executeStepBody ran). Flushed entries have
		// already been counted via flushSubStepLocked/dropSubStepLocked;
		// only non-flushed entries are genuinely cancelled.
		for addr, e := range r.subs {
			if e.groupAddr == groupAddr {
				if !e.flushed {
					g.cancelled++
				}
				delete(r.subs, addr)
			}
		}
		elapsed := formatElapsed(r.now().Sub(g.startTime))
		if g.cancelled > 0 {
			msg += fmt.Sprintf(" (%d ok, %d failed, %d skipped, %d cancelled of %d, %s)",
				g.ok, g.failed, g.skipped, g.cancelled, g.total, elapsed)
		} else {
			msg += fmt.Sprintf(" (%d ok, %d failed, %d skipped of %d, %s)",
				g.ok, g.failed, g.skipped, g.total, elapsed)
		}
		delete(r.groups, groupAddr)
	} else {
		// Defensive: clean up subs even when group entry is absent.
		for addr, e := range r.subs {
			if e.groupAddr == groupAddr {
				delete(r.subs, addr)
			}
		}
	}
	r.emit(color, msg)
}

// SubStepOutput appends a single line of captured sub-step output to the
// per-sub-step buffer. Never writes to the terminal directly; the buffer is
// dumped between separator bars by FinishStep / FailStep.
//
// If subAddr is unknown (e.g. StartGroup was skipped due to an upstream bug
// or a sequential test calls SubStepOutput) the entry is created on the fly
// so we never panic.
func (r *PlainReporter) SubStepOutput(subAddr string, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subs == nil {
		r.subs = make(map[string]*subStepEntry)
	}
	entry, ok := r.subs[subAddr]
	if !ok {
		entry = &subStepEntry{}
		r.subs[subAddr] = entry
	}
	if entry.flushed {
		// The sub-step already completed and its buffer was dumped by
		// FinishStep/FailStep. This call originates from lineTee.Flush()
		// delivering a trailing non-newline-terminated line. Write
		// directly so it is not silently dropped.
		_, _ = fmt.Fprintln(r.w.Writer(), line)
		return
	}
	entry.buf.WriteString(line)
	entry.buf.WriteByte('\n')
}

// subStepStatus categorises a sub-step's outcome for group counter updates.
type subStepStatus int

const (
	// statusPending is the zero value: sub-step has not yet received a terminal
	// event. This can happen when a FailFast cancellation races ahead of the
	// sub-step completing.
	statusPending subStepStatus = iota
	statusOk
	statusFailed
	statusSkipped
)

// flushSubStepLocked dumps the captured output for a parallel sub-step (if
// any) between separator bars, updates the group counters, and marks the entry
// as flushed. The entry is kept in r.subs (not deleted) so that a subsequent
// SubStepOutput call from lineTee.Flush() delivering a trailing non-newline-
// terminated line can detect the flushed state and write directly rather than
// silently dropping the content. FinishGroup cleans up flushed entries.
// Caller must hold r.mu. No-op for non-parallel addresses.
func (r *PlainReporter) flushSubStepLocked(addr string, status subStepStatus) {
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
	entry.buf.Reset()
	entry.flushed = true
}

// dropSubStepLocked discards a parallel sub-step's buffered output without
// dumping it (used for skipped sub-steps where no output is expected),
// updates the group counters, and marks the entry as flushed. The entry is
// kept in r.subs; FinishGroup cleans up flushed entries. Caller must hold r.mu.
func (r *PlainReporter) dropSubStepLocked(addr string, status subStepStatus) {
	entry, ok := r.subs[addr]
	if !ok {
		return
	}
	r.updateGroupCounterLocked(entry.groupAddr, status)
	entry.buf.Reset()
	entry.flushed = true
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

package pipeline

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
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
// subStepEntry holds buffered output for a single parallel sub-step.
// The group association lets FinishStep/FailStep update the parent group's
// aggregate counters.
//
// flushed is set to true after FinishStep/FailStep/SkipStep has dumped and
// cleared the buffer. If StepOutput is called after that point (e.g. from
// lineTee.Flush() on a trailing non-newline-terminated frame), the output is
// written directly to the terminal rather than being silently dropped.
//
// inProgress holds the most recent non-final (`\r`) frame for the sub-step,
// or the trailing tail emitted by lineTee.Flush at end-of-stream. It is
// display state only — never committed by StepOutput itself; the central
// commitTrailingTail helper (Task 9) is responsible for flushing it on
// step-finish events.
type subStepEntry struct {
	groupAddr   string
	buf         strings.Builder
	inProgress  string
	flushed     bool
	logPath     string // absolute path of per-sub-step log file; "" when disabled
	blockRowIdx int    // 0-based row index inside the LiveLine block
	subIdx      int    // 1-based sub-step index within the parallel group
	subTotal    int    // total sub-steps in the parallel group
	subName     string // sub-step display name
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
	logFile   io.Writer        // optional ANSI-stripped side-channel to the global pipeline log file
	termOut   io.Writer        // raw terminal stream for cursor ANSI (LiveLine in later tasks); io.Discard when non-TTY
	ttyMode   bool             // true when termOut is a real TTY (LiveLine block features enabled)
	name      string           // pipeline name set by StartPipeline (e.g. "deploy", "reset")
	startTime time.Time        // recorded by StartPipeline for elapsed time in FinishPipeline
	now       func() time.Time // injectable clock; defaults to time.Now

	// subs holds buffered output for parallel sub-steps that have not yet
	// completed. Keyed by full sub-step address.
	subs map[string]*subStepEntry

	// groups holds per-parallel-group aggregate state for FinishGroup.
	groups map[string]*groupEntry

	// currentStepAddr tracks the most recently started sequential step (or
	// the group address while a parallel group is active). footerPrefix is the
	// footer text set by StartStep ("[N/M] label"), reused by StepOutput to
	// show non-final sequential frames in the footer. inBlockMode is true
	// between StartGroup and FinishGroup; blockGroupAddr holds the active
	// group's address.
	currentStepAddr string
	footerPrefix    string
	inBlockMode     bool
	blockGroupAddr  string

	// live owns the sticky single-line footer. It is always non-nil; when
	// termOut is io.Discard (non-TTY) the LiveLine is constructed disabled
	// and every public call is a no-op except Println, which writes the
	// data line straight to the screen writer (matching the legacy path).
	live *LiveLine
}

// NewPlainReporter creates a PlainReporter.
//
// screen is the status-line writer (typically wrapping os.Stdout). logFile is
// the raw global pipeline log file (or nil when logging is disabled); the
// reporter wraps it with logSanitizer internally so the file on disk receives
// ANSI-stripped, `\r`-normalised content. termOut is the raw terminal stream
// reserved for cursor/spinner ANSI sequences in later live-progress tasks; it
// is io.Discard when stdout is not a TTY.
func NewPlainReporter(screen *render.Writer, logFile io.Writer, termOut io.Writer) *PlainReporter {
	if termOut == nil {
		termOut = io.Discard
	}
	var wrapped io.Writer
	if logFile != nil {
		wrapped = &logSanitizer{w: logFile}
	}
	tty := termOut != io.Discard
	r := &PlainReporter{
		w:       screen,
		logFile: wrapped,
		termOut: termOut,
		ttyMode: tty,
		now:     time.Now,
	}
	r.live = NewLiveLine(termOut, screen.Writer(), tty)
	// Register package-level prompt hooks so huh-based prompts (RunConfirm,
	// RunSelector, RunMultiSelect) pause/resume the LiveLine automatically.
	// Only one PlainReporter is expected per process; nested deploys are not
	// supported by this design. Close() clears the hooks on shutdown.
	ui.SetHuhHooks(r.live.Pause, r.live.Resume)
	return r
}

// Close releases reporter resources. Idempotent: calls LiveLine.Stop (a no-op
// when FinishPipeline already stopped the ticker via stopOnce) and clears the
// package-level huh hooks installed by NewPlainReporter. Callers should defer
// Close after the deploy/reset/lifecycle log cleanup so the hooks come down
// even on panic or early return.
func (r *PlainReporter) Close() {
	r.live.Stop()
	ui.ClearHuhHooks()
}

// StartPipeline stores the pipeline name and records the start time for
// elapsed time reporting. It does not print a header; the current deploy/reset
// output has no pipeline banner.
func (r *PlainReporter) StartPipeline(name string, _ int) {
	r.mu.Lock()
	r.name = name
	r.startTime = r.now()
	r.mu.Unlock()
	// SetText before Start so the initial footer paint already shows the
	// pipeline label rather than the spinner alone.
	r.live.SetText("Starting " + name + "...")
	r.live.Start()
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
	r.emit(render.Blue, label)
	r.mu.Unlock()
	footer := phaseKey
	if phase.Description != "" {
		footer += ": " + phase.Description
	}
	r.live.SetText(footer)
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
	label := stepAddr
	if step.Description != "" {
		label += ": " + step.Description
	}
	footer := label
	if index > 0 {
		footer = fmt.Sprintf("[%d/%d] %s", index, total, label)
	}
	r.mu.Lock()
	r.currentStepAddr = stepAddr
	r.footerPrefix = footer
	if index > 0 {
		r.emit(render.Blue, fmt.Sprintf("  %s [%d/%d] %s", iconRunning, index, total, label))
	} else {
		r.emit(render.Blue, fmt.Sprintf("  %s %s", iconRunning, label))
	}
	r.mu.Unlock()
	r.live.SetText(footer)
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
	r.commitTrailingTail(stepAddr)
	if entry, isSub := r.subs[stepAddr]; isSub && entry.groupAddr != "" && r.inBlockMode && r.ttyMode {
		r.live.SetBlockRow(entry.blockRowIdx, formatSkippedRow(entry.subIdx, entry.subTotal, entry.subName, reason))
	}
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
	r.commitTrailingTail(stepAddr)
	if entry, isSub := r.subs[stepAddr]; isSub && entry.groupAddr != "" && r.inBlockMode && r.ttyMode {
		r.live.SetBlockRow(entry.blockRowIdx, formatDoneRow(entry.subIdx, entry.subTotal, entry.subName))
	}
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
	r.commitTrailingTail(stepAddr)

	if entry, isSub := r.subs[stepAddr]; isSub && entry.groupAddr != "" {
		if r.inBlockMode && r.ttyMode {
			r.live.SetBlockRow(entry.blockRowIdx, formatFailedRow(entry.subIdx, entry.subTotal, entry.subName))
		}
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
	// Stop the LiveLine on BOTH success and failure paths. The defer is
	// registered before the early-return guard so the previous bug — where
	// a failed pipeline left the spinner ticker running until process exit
	// — cannot reoccur. stopOnce makes a redundant Close-time Stop a no-op.
	defer r.live.Stop()
	if !success {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	elapsed := formatElapsed(r.now().Sub(r.startTime))
	line := fmt.Sprintf("%s%s %s Done%s %s(%s)%s",
		r.timestampPrefix(),
		render.Green, iconDone, render.Reset,
		render.Gray, elapsed, render.Reset,
	)
	r.live.Println(line)
	if r.logFile != nil {
		_, _ = fmt.Fprintf(r.logFile, "[%s] %s Done (%s)\n", r.now().Format(timestampLayout), iconDone, elapsed)
	}
}

// emit writes a single line with the timestamp prefix and a colored body.
// Format: "<gray>[ts]<reset> <color>msg<reset>\n".
//
// The full line goes to the screen writer; a clean ts+msg copy is side-written
// to the log file (when configured) via logSanitizer.
func (r *PlainReporter) emit(color, msg string) {
	line := fmt.Sprintf("%s%s%s%s", r.timestampPrefix(), color, msg, render.Reset)
	r.live.Println(line)
	if r.logFile != nil {
		_, _ = fmt.Fprintf(r.logFile, "[%s] %s\n", r.now().Format(timestampLayout), msg)
	}
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

// SetSubStepLogPath records the per-sub-step log file path for subAddr. Called
// by the executor after OpenSubStepLog succeeds in runParallelSubStep. The
// path drives the TTY buffer-dump policy in flushSubStepLocked: when a
// sub-step succeeds (or is skipped) and a path is known, the dump is
// suppressed and a "Full log: <path>" pointer line is emitted instead.
func (r *PlainReporter) SetSubStepLogPath(subAddr string, path string) {
	if path == "" {
		return
	}
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
	entry.logPath = path
}

// formatRunningRow formats a block-mode row for a sub-step that is still
// executing. The leading glyph is the static "·" running marker — never ✓/✗/◎
// during execution, regardless of how many final=true frames arrive (the
// terminal glyphs are reserved for FinishStep/FailStep/SkipStep transitions).
func formatRunningRow(subIdx, subTotal int, subName, frame string) string {
	if frame == "" {
		return fmt.Sprintf("  %s [%d/%d] %s", iconRunning, subIdx, subTotal, subName)
	}
	return fmt.Sprintf("  %s [%d/%d] %s: %s", iconRunning, subIdx, subTotal, subName, frame)
}

func formatDoneRow(subIdx, subTotal int, subName string) string {
	return fmt.Sprintf("  %s [%d/%d] Done: %s", iconDone, subIdx, subTotal, subName)
}

func formatFailedRow(subIdx, subTotal int, subName string) string {
	return fmt.Sprintf("  %s [%d/%d] Failed: %s", iconFailed, subIdx, subTotal, subName)
}

func formatSkippedRow(subIdx, subTotal int, subName, reason string) string {
	return fmt.Sprintf("  %s [%d/%d] Skipped: %s (%s)", iconSkipped, subIdx, subTotal, subName, reason)
}

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
	r.inBlockMode = true
	r.blockGroupAddr = groupAddr
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
	subTotal := 0
	if group.Parallel != nil {
		subTotal = len(group.Parallel.Steps)
	}
	if group.Parallel != nil {
		for i, sub := range group.Parallel.Steps {
			subAddr := phasePrefix + "/" + sub.Name
			r.subs[subAddr] = &subStepEntry{
				groupAddr:   groupAddr,
				blockRowIdx: i,
				subIdx:      i + 1,
				subTotal:    subTotal,
				subName:     sub.Name,
			}
		}
	}

	// In TTY mode, reserve N rows for the parallel block and paint each row
	// with the initial running-state line so the user immediately sees the
	// group's shape. SetBlockRow is a no-op when LiveLine is disabled, so
	// non-TTY mode remains unaffected.
	if r.ttyMode && subTotal > 0 {
		r.live.StartBlock(subTotal)
		if group.Parallel != nil {
			for i, sub := range group.Parallel.Steps {
				r.live.SetBlockRow(i, formatRunningRow(i+1, subTotal, sub.Name, ""))
			}
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
	if r.blockGroupAddr == groupAddr {
		r.inBlockMode = false
		r.blockGroupAddr = ""
		// Freeze the block rows in scrollback and return LiveLine to
		// single-line mode. No-op when LiveLine is disabled.
		if r.ttyMode {
			r.live.EndBlock()
		}
	}

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

// StepOutput streams a single frame of captured step output to the reporter.
//
// For SEQUENTIAL steps (no associated groupAddr): final=true writes the frame
// directly to the screen and to the global pipeline log (single copy each);
// final=false stores the frame in inProgress as ephemeral display state, to be
// flushed by commitTrailingTail at step-finish time.
//
// For PARALLEL sub-steps (registered by StartGroup so groupAddr != ""):
// final=true appends to the per-sub-step buffer (dumped between separator bars
// by FinishStep/FailStep) AND writes to the global pipeline log once;
// final=false stores in inProgress (display state only).
//
// If addr is unknown the entry is created on the fly so we never panic.
func (r *PlainReporter) StepOutput(addr string, frame string, final bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subs == nil {
		r.subs = make(map[string]*subStepEntry)
	}
	entry, ok := r.subs[addr]
	if !ok {
		entry = &subStepEntry{}
		r.subs[addr] = entry
	}
	if entry.flushed {
		// Sub-step already completed and its buffer was dumped by
		// FinishStep/FailStep. This call originates from lineTee.Flush()
		// delivering a trailing non-newline-terminated frame. Route through
		// LiveLine so the cursor invariant is maintained.
		r.live.Println(frame)
		if final {
			r.writeLog(frame)
		}
		return
	}
	if !final {
		entry.inProgress = frame
		// Ephemeral row update: for parallel sub-steps refresh the block row;
		// for sequential steps update the footer text so live progress shows.
		if entry.groupAddr != "" && r.inBlockMode && r.ttyMode {
			r.live.SetBlockRow(entry.blockRowIdx, formatRunningRow(entry.subIdx, entry.subTotal, entry.subName, frame))
		} else if entry.groupAddr == "" && r.ttyMode {
			text := r.footerPrefix
			if frame != "" {
				text += ": " + frame
			}
			r.live.SetText(text)
		}
		return
	}
	if entry.groupAddr == "" {
		// Sequential step: route through LiveLine to preserve cursor invariant.
		r.live.Println(frame)
		r.writeLog(frame)
		entry.inProgress = ""
		return
	}
	// Parallel sub-step: buffer for dump, side-write log once. Update the
	// block row to the latest committed frame but keep the running-state
	// glyph (·) — ✓ is reserved for FinishStep.
	entry.buf.WriteString(frame)
	entry.buf.WriteByte('\n')
	entry.inProgress = ""
	r.writeLog(frame)
	if r.inBlockMode && r.ttyMode {
		r.live.SetBlockRow(entry.blockRowIdx, formatRunningRow(entry.subIdx, entry.subTotal, entry.subName, frame))
	}
}

// commitTrailingTail flushes any trailing non-terminated tail captured in
// inProgress for addr. Called as the FIRST step at every step-finish event
// (FinishStep / FailStep / SkipStep) so that the tail is preserved exactly
// once in screen + log regardless of whether the buffer is later dumped.
//
// For SEQUENTIAL steps the tail is written directly to screen + log.
// For PARALLEL sub-steps the tail is appended to the per-sub-step buffer
// AND written to the global log; the dump policy (executed by callers after
// this returns) replays from the buffer only, never re-logging.
//
// Caller must hold r.mu.
func (r *PlainReporter) commitTrailingTail(addr string) {
	if r.subs == nil {
		return
	}
	entry, ok := r.subs[addr]
	if !ok || entry.inProgress == "" {
		return
	}
	tail := entry.inProgress
	entry.inProgress = ""
	if entry.groupAddr == "" {
		r.live.Println(tail)
		r.writeLog(tail)
		return
	}
	entry.buf.WriteString(tail)
	entry.buf.WriteByte('\n')
	r.writeLog(tail)
}

// writeLog side-writes a single committed step-output frame to the global
// pipeline log file. No-op when logging is disabled.
func (r *PlainReporter) writeLog(frame string) {
	if r.logFile == nil {
		return
	}
	_, _ = fmt.Fprintln(r.logFile, frame)
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
	// Buffer-dump policy (Task 9):
	//   - non-TTY: always dump (existing behaviour).
	//   - TTY + FAILED: always dump (user needs full history to diagnose).
	//   - TTY + succeeded/skipped + logPath: suppress dump, emit "Full log:".
	//   - TTY + succeeded/skipped + no logPath: dump (only on-screen record).
	suppress := r.ttyMode && status != statusFailed && entry.logPath != ""
	switch {
	case suppress:
		r.emit(render.Gray, "  Full log: "+entry.logPath)
	case entry.buf.Len() > 0:
		r.emit(render.Gray, parallelOutputTopBar)
		// Replay buffered lines via live.Println so the LiveLine block redraws
		// correctly in TTY mode. The buffer already contains \n-terminated
		// frames; split and emit each line. dumpSubStepBufferLocked is PURE
		// SCREEN REPLAY — no log writes (each line was already writeLog'd at
		// its commit point in StepOutput / commitTrailingTail).
		body := strings.TrimSuffix(entry.buf.String(), "\n")
		for line := range strings.SplitSeq(body, "\n") {
			r.live.Println(line)
		}
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

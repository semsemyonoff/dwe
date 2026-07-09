package test

import (
	"fmt"
	"io"
	"sync"

	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
	"github.com/semsemyonoff/dwe/internal/shared/liveui"
)

// phaseLabels maps a coarse envtest.ProgressPhase to the English label shown in
// the aggregated status view. The test CLI's surface is intentionally
// non-localized (matching the rest of `dwe test`), so these stay plain English.
var phaseLabels = map[envtest.ProgressPhase]string{
	envtest.PhasePreparing:        "preparing…",
	envtest.PhaseValidating:       "validating…",
	envtest.PhaseDeploying:        "deploying…",
	envtest.PhaseDeployRetry:      "deploy retry…",
	envtest.PhaseRunningSteps:     "running steps…",
	envtest.PhaseCollectingReport: "collecting report…",
	envtest.PhaseTearingDown:      "tearing down…",
}

// phaseLabel returns the display label for a phase, falling back to the raw
// enum string for any phase not in the map (forward-compatible).
func phaseLabel(p envtest.ProgressPhase) string {
	if l, ok := phaseLabels[p]; ok {
		return l
	}
	return string(p)
}

// runLiveStatus is the aggregated, compact status view painted while `dwe test
// run --parallel N` (effective N>1) fans scenarios out. It wraps a single
// liveui.LiveLine in block mode: scenario i maps statically to block row i for
// the first `visibleRows` scenarios; any overflow scenario reports start/finish
// via framed Println lines instead. The footer shows a running counter.
//
// It is used in text mode only (never JSON — a nil display is passed there).
// When the LiveLine is disabled (non-TTY stdout / piped / CI) EVERY scenario
// falls back to flat `scenario <name>: started` / `scenario <name>: <status>`
// Println lines, because the block-row methods are silent no-ops in that mode.
//
// Every public method holds s.mu for its full body. This serializes not only
// the footer counter but also the underlying live writes: in disabled mode
// LiveLine.Println/PrintlnDiag write to the shared writer WITHOUT the LiveLine
// mutex, so concurrent goroutines would otherwise race on the destination
// buffer. Enabled-mode live methods take their own mutex; nesting s.mu →
// live.mu is deadlock-free (LiveLine never calls back into runLiveStatus).
type runLiveStatus struct {
	live  *liveui.LiveLine
	names []string
	// visibleRows is the number of reserved block rows. Scenarios 0..visibleRows-1
	// own a row; scenarios at or beyond it are "overflow" (framed Println). Only
	// meaningful when enabled.
	visibleRows int
	enabled     bool

	mu      sync.Mutex
	running int
	total   int
}

// newRunLiveStatus builds the display. It does NOT start the LiveLine — call
// start() after any test hooks are installed. termSize is consulted only for
// the height clamp (visibleRows); the LiveLine handles its own width detection.
// out receives the block/footer frames and flat data lines; diag receives
// warnings (stderr in production).
func newRunLiveStatus(names []string, isTTY bool, termSize func() (int, int), out, diag io.Writer) *runLiveStatus {
	live := liveui.NewLiveLine(out, out, isTTY)
	live.SetDiagWriter(diag)

	visibleRows := len(names)
	if isTTY {
		_, h := termSize()
		visibleRows = min(visibleRows, max(h-3, 1))
	}

	return &runLiveStatus{
		live:        live,
		names:       names,
		visibleRows: visibleRows,
		enabled:     isTTY,
		total:       len(names),
	}
}

// start paints the initial frame: starts the LiveLine ticker and, in enabled
// mode, reserves the block rows and seeds each visible row as pending (queued).
func (s *runLiveStatus) start() {
	s.live.Start()
	if !s.enabled {
		return
	}
	s.live.StartBlock(s.visibleRows)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setFooterLocked()
	for i := range s.visibleRows {
		s.live.SetBlockRowPending(i, s.names[i])
	}
}

// isRow reports whether scenario i owns a block row (enabled mode + within the
// visible window). Overflow scenarios and every scenario in disabled mode fall
// back to flat Println lines.
func (s *runLiveStatus) isRow(i int) bool {
	return s.enabled && i >= 0 && i < s.visibleRows
}

// rowLabel is the "<name>  <phase>" row label (two spaces separate the name
// from the coarse phase text, which already carries its trailing ellipsis).
func (s *runLiveStatus) rowLabel(i int, phase string) string {
	return s.names[i] + "  " + phase
}

// setFooterLocked refreshes the footer counter. Caller holds s.mu. No-op in
// disabled mode (SetText is a no-op there anyway).
func (s *runLiveStatus) setFooterLocked() {
	if !s.enabled {
		return
	}
	s.live.SetText(fmt.Sprintf("running %d/%d scenarios…", s.running, s.total))
}

// Started marks scenario i as begun: its row transitions pending → running
// (starting the per-row stopwatch) with the initial "preparing…" label, or a
// flat "scenario <name>: started" line for overflow/disabled scenarios.
func (s *runLiveStatus) Started(i int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running++
	s.setFooterLocked()
	if s.isRow(i) {
		s.live.SetBlockRowRunning(i, s.rowLabel(i, phaseLabel(envtest.PhasePreparing)))
		return
	}
	s.live.Println(fmt.Sprintf("scenario %s: started", s.names[i]))
}

// Phase relabels scenario i's row to reflect the coarse phase it just entered.
// Overflow/disabled scenarios produce no per-phase output (too noisy for the
// flat fallback).
func (s *runLiveStatus) Phase(i int, p envtest.ProgressPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isRow(i) {
		return
	}
	s.live.SetBlockRowRunning(i, s.rowLabel(i, phaseLabel(p)))
}

// Finished finalizes scenario i: its row freezes to ✓/✗ with a "passed" /
// `failed — step "X"` label, or a flat "scenario <name>: <status>" line for
// overflow/disabled scenarios. The running counter drops by one.
func (s *runLiveStatus) Finished(i int, o scenarioOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running > 0 {
		s.running--
	}
	s.setFooterLocked()
	if s.isRow(i) {
		kind, label := finalStatusLabel(o)
		s.live.SetBlockRowFinal(i, kind, label)
		return
	}
	s.live.Println(fmt.Sprintf("scenario %s: %s", o.Name, o.Status))
}

// Warn routes a per-scenario warning to the diagnostics writer (stderr in
// production) framed above the block so the sticky view stays intact.
func (s *runLiveStatus) Warn(i int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := "?"
	if i >= 0 && i < len(s.names) {
		name = s.names[i]
	}
	s.live.PrintlnDiag(fmt.Sprintf("[%s] warning: %s", name, msg))
}

// Close returns the LiveLine to single-line mode (leaving the finalized glyph
// rows in scrollback) and stops the ticker. Call before rendering the final
// text report. Idempotent via LiveLine.Stop.
func (s *runLiveStatus) Close() {
	if s.enabled {
		s.live.EndBlock()
	}
	s.live.Stop()
}

// finalStatusLabel maps a completed outcome to its block-row kind + label.
// Anything other than passed renders as a ✗ row; a known failing step is quoted
// into the label. A prep/validate StatusError renders as "error" (not "failed")
// so the block view agrees with the flat/overflow lines and the final text
// report, all of which surface the raw status.
func finalStatusLabel(o scenarioOutcome) (liveui.BlockRowKind, string) {
	switch o.Status {
	case envtest.StatusPassed:
		return liveui.BlockRowDone, o.Name + "  passed"
	case envtest.StatusError:
		return liveui.BlockRowFailed, o.Name + "  error"
	}
	if o.FailedStep != "" {
		return liveui.BlockRowFailed, fmt.Sprintf("%s  failed — step %q", o.Name, o.FailedStep)
	}
	return liveui.BlockRowFailed, o.Name + "  failed"
}

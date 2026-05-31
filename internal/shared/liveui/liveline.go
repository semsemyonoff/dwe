package liveui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/devbox/internal/shared/render"
)

// Icon glyphs used by block-row finalisation and by pipeline reporter text
// output. Exported so both PlainReporter and the workflow runner reference a
// single source of truth.
const (
	IconDone    = "✓"
	IconFailed  = "✗"
	IconSkipped = "◎"
	IconRunning = "·"
)

// liveLineTickPeriod is the redraw period for the LiveLine ticker (10 Hz).
const liveLineTickPeriod = 100 * time.Millisecond

const liveLineDefaultWidth = 80

// BlockRowKind enumerates the final-state icons used by SetBlockRowFinal.
type BlockRowKind int

const (
	// BlockRowDone marks a sub-step that finished successfully (green ✓).
	BlockRowDone BlockRowKind = iota
	// BlockRowFailed marks a sub-step that returned an error (red ✗).
	BlockRowFailed
	// BlockRowSkipped marks a sub-step skipped by a when condition or
	// files_gate (yellow ◎).
	BlockRowSkipped
)

// blockRow holds the per-row state used by the LiveLine block renderer. A
// row is either running (icon == "" — the spinner glyph + live elapsed are
// composed at render time) or finalized (icon is the frozen glyph and
// elapsed holds the wall-clock duration captured at finalisation).
type blockRow struct {
	label     string
	icon      string
	iconColor string
	startTime time.Time
	elapsed   time.Duration
	finalized bool
}

// LiveLine owns the bottom-of-cursor footer rendered during pipeline execution.
// It exposes a split-channel writer model:
//
//   - termOut receives cursor-control ANSI and spinner frames (raw os.Stdout in
//     production, io.Discard in non-TTY mode).
//   - screen receives data lines emitted by [LiveLine.Println] (also os.Stdout).
//
// In production both writers target the same fd; the split lets tests assert
// channel-separation invariants directly. All public methods are safe for
// concurrent use; Stop MUST be called from outside any LiveLine callback.
type LiveLine struct {
	termOut io.Writer
	screen  io.Writer
	enabled atomic.Bool // set once at construction; Stop() flips it to false

	mu       sync.Mutex
	spinner  spinner.Model
	text     string
	paused   bool
	started  bool
	stopped  bool
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	// Block-mode state: when blockRows > 0 the LiveLine owns N reserved rows
	// above the footer used by parallel-group sub-step displays. blockSlots
	// holds the structured per-row state (icon, color, label, stopwatch).
	blockRows  int
	blockSlots []blockRow

	// footerStart is the wall-clock time captured by Start(). The footer
	// renders [<elapsed>] between the spinner and the text using this
	// value, so the user sees a live pipeline stopwatch tick second-by-
	// second alongside the current step name.
	footerStart time.Time

	// testHooks lets tests drive the ticker deterministically and to override
	// width detection without poking at the terminal.
	testHooks *liveLineTestHooks
}

// liveLineTestHooks is the unexported wiring used by tests to make LiveLine
// deterministic: tick() advances the ticker explicitly, widthFn substitutes
// terminal width, and the noTicker flag suppresses the background ticker
// goroutine so tests can drive frames synchronously.
type liveLineTestHooks struct {
	noTicker bool
	widthFn  func() int
}

// NewLiveLine returns a LiveLine writing cursor ANSI to termOut and data lines
// to screen. When enabled is false every public method is a no-op except
// Println, which writes "line\n" straight to screen (the non-TTY path).
func NewLiveLine(termOut, screen io.Writer, enabled bool) *LiveLine {
	if termOut == nil {
		termOut = io.Discard
	}
	if screen == nil {
		screen = io.Discard
	}
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle()
	ll := &LiveLine{
		termOut: termOut,
		screen:  screen,
		spinner: sp,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	ll.enabled.Store(enabled)
	return ll
}

// Start begins the ticker (when enabled) and paints the initial footer row.
// Idempotent: subsequent calls are no-ops.
func (l *LiveLine) Start() {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	if l.started || l.stopped {
		l.mu.Unlock()
		return
	}
	l.started = true
	l.footerStart = time.Now()
	// Initial footer: hide cursor, paint spinner + text, newline brings cursor
	// to the row below the footer (Invariant #9).
	l.writeTerm("\x1b[?25l" + l.renderFooterLocked() + "\n")
	noTicker := l.testHooks != nil && l.testHooks.noTicker
	l.mu.Unlock()
	if noTicker {
		// Tests drive ticks explicitly via tick(); still close doneCh on Stop.
		go func() {
			<-l.stopCh
			close(l.doneCh)
		}()
		return
	}
	go l.tickLoop()
}

func (l *LiveLine) tickLoop() {
	defer close(l.doneCh)
	t := time.NewTicker(liveLineTickPeriod)
	defer t.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-t.C:
			l.advance()
		}
	}
}

// Tick drives one redraw frame deterministically. Test-only helper exported
// so tests in other packages (e.g. internal/core/execution/pipeline) can drive the LiveLine
// without leaking internals through reflection.
func (l *LiveLine) Tick() { l.advance() }

// SetTestHooks installs the test hooks for deterministic testing. Test-only.
// Pass noTicker=true to suppress the background ticker so the test can drive
// frames via Tick. widthFn substitutes terminal width detection.
func (l *LiveLine) SetTestHooks(noTicker bool, widthFn func() int) {
	l.testHooks = &liveLineTestHooks{noTicker: noTicker, widthFn: widthFn}
}

func (l *LiveLine) advance() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.enabled.Load() || !l.started || l.stopped || l.paused {
		return
	}
	// Synthesise a TickMsg the spinner accepts; discard the returned Cmd.
	newSp, _ := l.spinner.Update(spinner.TickMsg{Time: time.Now(), ID: l.spinner.ID()})
	l.spinner = newSp
	l.redrawLocked()
}

// redrawLocked repaints the LiveLine-owned rows. Caller holds l.mu.
//
// Single-line mode: up one row, clear, paint footer, newline (cursor below).
// Block mode: up blockRows+1 rows to top of block, paint each block row, then
// footer, each terminated by \n (cursor ends below footer).
func (l *LiveLine) redrawLocked() {
	if l.blockRows > 0 {
		l.writeTerm(fmt.Sprintf("\x1b[%dA", l.blockRows+1))
		for i := range l.blockRows {
			l.writeTerm("\r\x1b[2K" + l.renderBlockRowLocked(i) + "\n")
		}
		l.writeTerm("\r\x1b[2K" + l.renderFooterLocked() + "\n")
		return
	}
	l.writeTerm("\x1b[1A\r\x1b[2K" + l.renderFooterLocked() + "\n")
}

// Stop hides the footer and joins the ticker. Idempotent — safe to call
// multiple times via stopOnce. MUST be called from outside any LiveLine
// callback (Invariant #5).
func (l *LiveLine) Stop() {
	if !l.enabled.Load() {
		return
	}
	l.stopOnce.Do(func() {
		// Snapshot started under mu; if Start was never called we still must
		// close stopCh + doneCh to keep the contract symmetric.
		l.mu.Lock()
		started := l.started
		l.mu.Unlock()

		close(l.stopCh)
		if started {
			<-l.doneCh
		} else {
			// No ticker was started, but goroutine-less consumers expect
			// doneCh closed so a hypothetical waiter does not block.
			close(l.doneCh)
		}

		l.mu.Lock()
		if l.started && !l.paused {
			if l.blockRows > 0 {
				// Erase block + footer rows (defensive: callers should
				// EndBlock first, but Stop must not leave artifacts).
				n := l.blockRows + 1
				l.writeTerm(fmt.Sprintf("\x1b[%dA", n))
				for range n {
					l.writeTerm("\r\x1b[2K\n")
				}
				l.writeTerm(fmt.Sprintf("\x1b[%dA", n))
				l.writeTerm("\x1b[?25h")
			} else {
				// Erase footer + show cursor. Cursor remains on the cleared row.
				l.writeTerm("\x1b[1A\r\x1b[2K\x1b[?25h")
			}
		} else if l.started && l.paused {
			// Paused already erased the footer; just show the cursor.
			l.writeTerm("\x1b[?25h")
		}
		l.enabled.Store(false)
		l.stopped = true
		l.mu.Unlock()
	})
}

// Pause erases the live-owned rows and leaves the cursor at the top of the
// cleared area so child process output can flow from there.
//
// Single-line mode: erases the footer row only (cursor at col 0 of that row).
// Block mode: erases all N block rows + footer (N+1 rows total); cursor at the
// first block row. INTENTIONAL EXCEPTION to Invariant #9: huh prompts render
// starting at the current cursor row, so leaving the cursor below would
// produce a blank gap above the prompt. Resume restores the invariant.
func (l *LiveLine) Pause() {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.stopped || l.paused {
		return
	}
	if l.blockRows > 0 {
		// Erase all N block rows + footer; reposition cursor at first block row.
		n := l.blockRows + 1
		l.writeTerm(fmt.Sprintf("\x1b[%dA", n))
		for range n {
			l.writeTerm("\r\x1b[2K\n")
		}
		l.writeTerm(fmt.Sprintf("\x1b[%dA", n))
	} else {
		l.writeTerm("\x1b[1A\r\x1b[2K")
	}
	l.writeTerm("\x1b[?25h")
	l.paused = true
}

// Resume repaints the live-owned rows starting at the current cursor position
// (wherever child output left it) and advances the cursor below the footer,
// restoring Invariant #9.
//
// Block mode: repaints all N block rows then the footer. After this call the
// block rows are anchored at the cursor's pre-Resume position and redrawLocked
// will track them correctly.
func (l *LiveLine) Resume() {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.stopped || !l.paused {
		return
	}
	l.paused = false
	if l.blockRows > 0 {
		for i := range l.blockRows {
			l.writeTerm("\r\x1b[2K" + l.renderBlockRowLocked(i) + "\n")
		}
	}
	l.writeTerm("\x1b[?25l" + l.renderFooterLocked() + "\n")
}

// SetText updates the footer text. The next redraw / Println picks it up.
// On the disabled path SetText is a no-op.
func (l *LiveLine) SetText(s string) {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	l.text = s
	l.mu.Unlock()
}

// StartBlock physically reserves `rows` rows above the footer for sub-step
// content. The footer moves down by `rows` rows. Each block row starts in
// the running state (spinner glyph + empty label + zero elapsed) — populate
// with [LiveLine.SetBlockRowRunning] / [LiveLine.SetBlockRowFinal].
// Calling StartBlock when a block is already active or when LiveLine is
// disabled/stopped is a no-op.
func (l *LiveLine) StartBlock(rows int) {
	if !l.enabled.Load() || rows <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.stopped || l.blockRows > 0 {
		return
	}
	// Erase current footer (cursor at footer row col 0).
	l.writeTerm("\x1b[1A\r\x1b[2K")
	// Reserve `rows` rows by writing newlines; terminal scrolls if needed.
	l.writeTerm(strings.Repeat("\n", rows))
	l.blockRows = rows
	l.blockSlots = make([]blockRow, rows)
	// Paint footer at the new bottom position; \n places cursor below footer.
	l.writeTerm(l.renderFooterLocked() + "\n")
}

// SetBlockRowRunning marks row idx as running with the given label. The
// per-row stopwatch is started on the first running call and the row's
// icon switches back to the spinner glyph if it was previously finalised.
// Out-of-range idx is silently ignored.
func (l *LiveLine) SetBlockRowRunning(idx int, label string) {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.blockRows == 0 || idx < 0 || idx >= l.blockRows {
		return
	}
	slot := &l.blockSlots[idx]
	if slot.startTime.IsZero() {
		slot.startTime = time.Now()
	}
	slot.label = label
	slot.finalized = false
	slot.icon = ""
	slot.iconColor = ""
	slot.elapsed = 0
	if l.started && !l.stopped && !l.paused {
		l.redrawLocked()
	}
}

// SetBlockRowFinal transitions row idx to a final state. The kind picks
// the icon and colour:
//
//   - BlockRowDone:    green ✓
//   - BlockRowFailed:  red ✗
//   - BlockRowSkipped: yellow ◎
//
// The row's stopwatch freezes at the wall-clock duration since the running
// state started (or 0 if SetBlockRowRunning was never called). Out-of-
// range idx is silently ignored.
func (l *LiveLine) SetBlockRowFinal(idx int, kind BlockRowKind, label string) {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.blockRows == 0 || idx < 0 || idx >= l.blockRows {
		return
	}
	slot := &l.blockSlots[idx]
	if !slot.startTime.IsZero() {
		slot.elapsed = time.Since(slot.startTime)
	}
	slot.label = label
	slot.icon, slot.iconColor = finalGlyph(kind)
	slot.finalized = true
	if l.started && !l.stopped && !l.paused {
		l.redrawLocked()
	}
}

// finalGlyph maps a BlockRowKind to its icon glyph and ANSI colour escape.
func finalGlyph(kind BlockRowKind) (icon, color string) {
	switch kind {
	case BlockRowFailed:
		return IconFailed, render.Red
	case BlockRowSkipped:
		return IconSkipped, render.Yellow
	default:
		return IconDone, render.Green
	}
}

// EndBlock returns LiveLine to single-line mode. The N block-content rows
// remain in scrollback (they hold the per-sub-step final glyphs). The old
// live footer that sat below them is ERASED so the user never sees a
// frozen spinner-mid-frame next to the last-started sub-step's text — a
// fresh single-line footer is painted in its place using current state.
func (l *LiveLine) EndBlock() {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.stopped || l.blockRows == 0 {
		return
	}
	// Cursor is at the row below the current footer (which sits at row
	// `prevFooter`). Move up onto the footer row, clear it, and paint the
	// fresh single-line footer there — \n lands the cursor below it
	// (Invariant #9).
	l.writeTerm("\x1b[1A\r\x1b[2K" + l.renderFooterLocked() + "\n")
	l.blockRows = 0
	l.blockSlots = nil
}

// termWidth returns the current terminal column count. Test hooks take
// priority; otherwise os.Stdout is queried. Falls back to liveLineDefaultWidth
// on error (non-TTY, io.Pipe, etc.). Caller need not hold l.mu.
func (l *LiveLine) termWidth() int {
	if l.testHooks != nil && l.testHooks.widthFn != nil {
		return l.testHooks.widthFn()
	}
	cols, _, err := term.GetSize(os.Stdout.Fd())
	if err == nil && cols > 0 {
		return cols
	}
	return liveLineDefaultWidth
}

// renderBlockRowLocked formats block row idx as
// "  <icon> [<elapsed>] <label>", truncating to terminal width. Running
// rows use the spinner frame coloured blue; finalised rows use the
// stored icon + colour. Caller holds l.mu.
func (l *LiveLine) renderBlockRowLocked(idx int) string {
	slot := l.blockSlots[idx]
	var iconText string
	if slot.finalized {
		iconText = slot.iconColor + slot.icon + render.Reset
	} else {
		iconText = render.Blue + l.spinner.View() + render.Reset
	}
	var elapsed time.Duration
	if slot.finalized {
		elapsed = slot.elapsed
	} else if !slot.startTime.IsZero() {
		elapsed = time.Since(slot.startTime)
	}
	elapsedText := render.Gray + "[" + FormatElapsed(elapsed) + "]" + render.Reset
	content := fmt.Sprintf("  %s %s %s", iconText, elapsedText, slot.label)
	w := l.termWidth()
	if lipgloss.Width(content) > w {
		content = truncateToWidth(content, w)
	}
	return content
}

// Println writes a persistent data line ABOVE the footer. In disabled mode it
// writes "line\n" straight to screen. In enabled mode the sequence is:
//
//  1. termOut: cursor-up + clear-footer-line
//  2. screen:  line + "\n" (data lands on the former footer row)
//  3. termOut: repaint footer + "\n" (cursor below the new footer)
//
// All three steps run under the LiveLine mutex so concurrent Println calls
// produce well-formed output.
func (l *LiveLine) Println(rawLine string) {
	if !l.enabled.Load() {
		// After Stop() enabled flips to false; writes are safe no-ops on
		// termOut and still need to land on screen if a caller writes "late".
		// We still write to screen to preserve the disabled-mode contract.
		_, _ = io.WriteString(l.screen, rawLine+"\n")
		return
	}
	l.mu.Lock()
	if !l.started || l.stopped {
		// Behave like disabled: write to screen only.
		l.mu.Unlock()
		_, _ = io.WriteString(l.screen, rawLine+"\n")
		return
	}
	if l.paused {
		// Footer is already erased; just write the line.
		_, _ = io.WriteString(l.screen, rawLine+"\n")
		l.mu.Unlock()
		return
	}
	if l.blockRows > 0 {
		n := l.blockRows + 1
		// Up to the top of owned area.
		l.writeTerm(fmt.Sprintf("\x1b[%dA", n))
		// Clear each owned row by walking down with \r\x1b[2K\n.
		for range n {
			l.writeTerm("\r\x1b[2K\n")
		}
		// Back to top of cleared area.
		l.writeTerm(fmt.Sprintf("\x1b[%dA", n))
		// Data line lands on the topmost cleared row.
		_, _ = io.WriteString(l.screen, rawLine+"\n")
		// Repaint block rows then footer; \n advances cursor below the footer.
		for i := range l.blockRows {
			l.writeTerm(l.renderBlockRowLocked(i) + "\n")
		}
		l.writeTerm(l.renderFooterLocked() + "\n")
		l.mu.Unlock()
		return
	}
	// Up to the footer row, clear it; write data on that row; repaint footer.
	l.writeTerm("\x1b[1A\r\x1b[2K")
	_, _ = io.WriteString(l.screen, rawLine+"\n")
	l.writeTerm(l.renderFooterLocked() + "\n")
	l.mu.Unlock()
}

// renderFooterLocked returns the formatted footer string (no trailing
// newline). Format: "<spinner-frame> [<pipeline-elapsed>] <text>". The
// elapsed segment is the wall-clock duration since Start() recorded the
// pipeline start time. Caller must hold l.mu.
func (l *LiveLine) renderFooterLocked() string {
	w := l.termWidth()
	frame := render.Blue + l.spinner.View() + render.Reset
	var elapsedText string
	if !l.footerStart.IsZero() {
		elapsedText = render.Gray + "[" + FormatElapsed(time.Since(l.footerStart)) + "]" + render.Reset
	}
	frameW := lipgloss.Width(frame)
	elapsedW := lipgloss.Width(elapsedText)
	text := l.text
	// Reserve 2 spaces: one after the spinner, one after the elapsed segment.
	avail := max(w-frameW-elapsedW-2, 0)
	if lipgloss.Width(text) > avail {
		text = truncateToWidth(text, avail)
	}
	if elapsedText == "" {
		return fmt.Sprintf("%s %s", frame, text)
	}
	return fmt.Sprintf("%s %s %s", frame, elapsedText, text)
}

// truncateToWidth shortens s to fit within w display columns using lipgloss
// Width as the measure. It is a simple byte walk: precise enough for our
// status strings, none of which contain combining characters.
func truncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	runes := []rune(s)
	for i := len(runes); i > 0; i-- {
		candidate := string(runes[:i])
		if lipgloss.Width(candidate) <= w {
			return candidate
		}
	}
	return ""
}

// writeTerm is the single termOut writer; caller must hold l.mu.
func (l *LiveLine) writeTerm(s string) {
	_, _ = io.WriteString(l.termOut, s)
}

// FormatElapsed formats a duration as a human-readable elapsed time string.
// Examples: "5s", "1m 23s", "2h 5m".
func FormatElapsed(d time.Duration) string {
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

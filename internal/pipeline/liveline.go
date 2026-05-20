package pipeline

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
)

// liveLineTickPeriod is the redraw period for the LiveLine ticker (10 Hz).
const liveLineTickPeriod = 100 * time.Millisecond

const liveLineDefaultWidth = 80

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
	// above the footer used by parallel-group sub-step displays.
	blockRows    int
	blockContent []string

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

// tick drives one redraw frame deterministically. Test-only.
func (l *LiveLine) tick() {
	l.advance()
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

// Pause erases the footer and leaves the cursor at column 0 of the now-empty
// former-footer row. INTENTIONAL EXCEPTION to Invariant #9: huh prompts render
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
	l.writeTerm("\x1b[1A\r\x1b[2K\x1b[?25h")
	l.paused = true
}

// Resume repaints the footer at the current cursor row and advances the cursor
// below it, restoring Invariant #9.
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
// content. The footer moves down by `rows` rows. Block content starts empty
// and is populated via [LiveLine.SetBlockRow]. Calling StartBlock when a block
// is already active or when LiveLine is disabled/stopped is a no-op.
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
	l.blockContent = make([]string, rows)
	// Paint footer at the new bottom position; \n places cursor below footer.
	l.writeTerm(l.renderFooterLocked() + "\n")
}

// SetBlockRow updates the content for block row idx and triggers an immediate
// redraw. Out-of-range idx is silently ignored.
func (l *LiveLine) SetBlockRow(idx int, content string) {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.stopped || l.paused || l.blockRows == 0 {
		if l.blockRows > 0 && idx >= 0 && idx < l.blockRows {
			l.blockContent[idx] = content
		}
		return
	}
	if idx < 0 || idx >= l.blockRows {
		return
	}
	l.blockContent[idx] = content
	l.redrawLocked()
}

// EndBlock freezes the current block (rows persist in scrollback) and paints a
// fresh single-line footer below. Subsequent operations behave as single-line.
func (l *LiveLine) EndBlock() {
	if !l.enabled.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.stopped || l.blockRows == 0 {
		return
	}
	// Cursor is at the row below the current (about-to-be-frozen) footer.
	// Paint a fresh single-line footer here; \n lands cursor below it.
	l.writeTerm(l.renderFooterLocked() + "\n")
	l.blockRows = 0
	l.blockContent = nil
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

// renderBlockRowLocked formats block row idx, truncating to terminal width.
// Caller holds l.mu.
func (l *LiveLine) renderBlockRowLocked(idx int) string {
	w := l.termWidth()
	content := l.blockContent[idx]
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

// renderFooterLocked returns the formatted footer string (no trailing newline).
// Caller must hold l.mu.
func (l *LiveLine) renderFooterLocked() string {
	w := l.termWidth()
	frame := l.spinner.View()
	frameW := lipgloss.Width(frame)
	text := l.text
	avail := max(w-frameW-1, 0) // 1 for the space between frame and text
	if lipgloss.Width(text) > avail {
		text = truncateToWidth(text, avail)
	}
	return fmt.Sprintf("%s %s", frame, text)
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

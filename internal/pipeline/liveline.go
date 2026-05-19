package pipeline

import (
	"fmt"
	"io"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
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
	enabled bool

	mu       sync.Mutex
	spinner  spinner.Model
	text     string
	width    int
	paused   bool
	started  bool
	stopped  bool
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

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
	return &LiveLine{
		termOut: termOut,
		screen:  screen,
		enabled: enabled,
		spinner: sp,
		width:   liveLineDefaultWidth,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start begins the ticker (when enabled) and paints the initial footer row.
// Idempotent: subsequent calls are no-ops.
func (l *LiveLine) Start() {
	if !l.enabled {
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
	if !l.enabled || !l.started || l.stopped || l.paused {
		l.mu.Unlock()
		return
	}
	// Synthesise a TickMsg the spinner accepts; discard the returned Cmd.
	newSp, _ := l.spinner.Update(spinner.TickMsg{Time: time.Now(), ID: l.spinner.ID()})
	l.spinner = newSp
	// Redraw single-line footer: up one row, clear, paint, newline.
	l.writeTerm("\x1b[1A\r\x1b[2K" + l.renderFooterLocked() + "\n")
	l.mu.Unlock()
}

// Stop hides the footer and joins the ticker. Idempotent — safe to call
// multiple times via stopOnce. MUST be called from outside any LiveLine
// callback (Invariant #5).
func (l *LiveLine) Stop() {
	if !l.enabled {
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
			// Erase footer + show cursor. Cursor remains on the cleared row.
			l.writeTerm("\x1b[1A\r\x1b[2K\x1b[?25h")
		} else if l.started && l.paused {
			// Paused already erased the footer; just show the cursor.
			l.writeTerm("\x1b[?25h")
		}
		l.enabled = false
		l.stopped = true
		l.mu.Unlock()
	})
}

// Pause erases the footer and leaves the cursor at column 0 of the now-empty
// former-footer row. INTENTIONAL EXCEPTION to Invariant #9: huh prompts render
// starting at the current cursor row, so leaving the cursor below would
// produce a blank gap above the prompt. Resume restores the invariant.
func (l *LiveLine) Pause() {
	if !l.enabled {
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
	if !l.enabled {
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
	if !l.enabled {
		return
	}
	l.mu.Lock()
	l.text = s
	l.mu.Unlock()
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
	if !l.enabled {
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
	// Up to the footer row, clear it; write data on that row; repaint footer.
	l.writeTerm("\x1b[1A\r\x1b[2K")
	_, _ = io.WriteString(l.screen, rawLine+"\n")
	l.writeTerm(l.renderFooterLocked() + "\n")
	l.mu.Unlock()
}

// renderFooterLocked returns the formatted footer string (no trailing newline).
// Caller must hold l.mu.
func (l *LiveLine) renderFooterLocked() string {
	w := l.width
	if l.testHooks != nil && l.testHooks.widthFn != nil {
		w = l.testHooks.widthFn()
	}
	if w <= 0 {
		w = liveLineDefaultWidth
	}
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

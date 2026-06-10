package liveui

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// termGrid is a tiny ANSI-aware virtual terminal used by cursor-invariant
// tests. It implements io.Writer so LiveLine can write both its termOut
// cursor sequences AND screen data lines into the SAME grid in actual write
// order, mirroring how a real terminal consumes a single byte stream.
//
// Supported sequences:
//   - `\r`           — cursor to column 0
//   - `\n`           — cursor down one row; scroll if at bottom row
//   - `\x1b[?25l/h`  — show/hide cursor (state only, no rendering side effect)
//   - `\x1b[2K`      — erase line at current row
//   - `\x1b[<N>A`    — cursor up N rows
//   - `\x1b[<N>B`    — cursor down N rows
//   - `\x1b[1A`      — same (N defaults to 1)
//
// Anything else is treated as printable text and lands at the cursor position.
type termGrid struct {
	rows       int
	cols       int
	grid       [][]rune
	row        int
	col        int
	cursorHide bool
}

func newTermGrid(rows, cols int) *termGrid {
	g := &termGrid{rows: rows, cols: cols}
	g.grid = make([][]rune, rows)
	for i := range g.grid {
		g.grid[i] = make([]rune, cols)
		for j := range g.grid[i] {
			g.grid[i][j] = ' '
		}
	}
	return g
}

func (g *termGrid) Write(p []byte) (int, error) {
	s := string(p)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\x1b' && i+1 < len(s) && s[i+1] == '[':
			// CSI sequence: collect parameters until final byte.
			j := i + 2
			start := j
			for j < len(s) {
				b := s[j]
				if (b >= '0' && b <= '9') || b == ';' || b == '?' {
					j++
					continue
				}
				break
			}
			if j >= len(s) {
				i = len(s)
				continue
			}
			params := s[start:j]
			final := s[j]
			g.applyCSI(params, final)
			i = j + 1
		case c == '\r':
			g.col = 0
			i++
		case c == '\n':
			g.row++
			if g.row >= g.rows {
				// scroll up
				copy(g.grid, g.grid[1:])
				last := make([]rune, g.cols)
				for k := range last {
					last[k] = ' '
				}
				g.grid[g.rows-1] = last
				g.row = g.rows - 1
			}
			g.col = 0
			i++
		default:
			// Printable run until next control byte.
			j := i
			for j < len(s) && s[j] != '\x1b' && s[j] != '\r' && s[j] != '\n' {
				j++
			}
			g.putRunes([]rune(s[i:j]))
			i = j
		}
	}
	return len(p), nil
}

func (g *termGrid) applyCSI(params string, final byte) {
	switch final {
	case 'A':
		n := atoiDefault(params, 1)
		g.row -= n
		if g.row < 0 {
			g.row = 0
		}
	case 'B':
		n := atoiDefault(params, 1)
		g.row += n
		if g.row >= g.rows {
			g.row = g.rows - 1
		}
	case 'K':
		// 2K erases the entire line; we treat any K param as erase-line.
		for k := range g.grid[g.row] {
			g.grid[g.row][k] = ' '
		}
	case 'l':
		if params == "?25" {
			g.cursorHide = true
		}
	case 'h':
		if params == "?25" {
			g.cursorHide = false
		}
	}
}

func (g *termGrid) putRunes(rs []rune) {
	for _, r := range rs {
		if g.col >= g.cols {
			// No wrap: clip.
			return
		}
		if g.row < 0 || g.row >= g.rows {
			return
		}
		g.grid[g.row][g.col] = r
		g.col++
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return def
	}
	return n
}

func (g *termGrid) line(row int) string {
	return strings.TrimRight(string(g.grid[row]), " ")
}

func (g *termGrid) cursor() (int, int) { return g.row, g.col }

// ansiSeqRe is used by tests that inspect raw termOut bytes for ANSI content.
var ansiSeqRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSIBytes(b []byte) string {
	return ansiSeqRe.ReplaceAllString(string(b), "")
}

// newTestLiveLine builds a LiveLine with the no-ticker test hook so the test
// drives redraws via l.Tick().
func newTestLiveLine(termOut, screen io.Writer, enabled bool) *LiveLine {
	l := NewLiveLine(termOut, screen, enabled)
	l.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 80 }}
	return l
}

func TestLiveLine_DisabledIsNoOp(t *testing.T) {
	var term, scr bytes.Buffer
	l := NewLiveLine(&term, &scr, false)
	l.Start()
	l.SetText("hello")
	l.Println("data")
	l.Pause()
	l.Resume()
	l.Stop()

	require.Empty(t, term.String(), "termOut must be empty when disabled")
	require.Equal(t, "data\n", scr.String())
}

func TestLiveLine_ChannelSeparation(t *testing.T) {
	var term, scr bytes.Buffer
	l := newTestLiveLine(&term, &scr, true)
	l.SetText("phase: running")
	l.Start()
	l.Println("hello")
	l.Println("world")
	l.Tick()
	l.Stop()

	// termOut must contain only ANSI + spinner glyphs — no data lines.
	stripped := stripANSIBytes(term.Bytes())
	require.NotContains(t, stripped, "hello")
	require.NotContains(t, stripped, "world")

	// screen must contain only data lines — no ANSI.
	require.Equal(t, "hello\nworld\n", scr.String())
	require.NotContains(t, scr.String(), "\x1b")
}

// TestLiveLine_PrintlnDiagRoutesToDiagWriter asserts PrintlnDiag sends the line
// text to the configured diagnostics writer (stderr in production), keeps it off
// the screen (stdout), and still frames the footer via termOut. This backs the
// "verbose/debug output goes to stderr only" contract while a pipeline owns the
// screen.
func TestLiveLine_PrintlnDiagRoutesToDiagWriter(t *testing.T) {
	var term, scr, diag bytes.Buffer
	l := newTestLiveLine(&term, &scr, true)
	l.SetDiagWriter(&diag)
	l.SetText("phase: running")
	l.Start()
	l.Println("data-line")       // screen
	l.PrintlnDiag("$ docker ps") // diag only
	l.Tick()
	l.Stop()

	require.Equal(t, "data-line\n", scr.String(), "diagnostic line must not reach the screen")
	require.Equal(t, "$ docker ps\n", diag.String(), "diagnostic line must reach the diag writer")
	require.NotContains(t, diag.String(), "\x1b", "diag writer must receive only the line text, no ANSI")

	// termOut framed both lines but carries neither line's text.
	stripped := stripANSIBytes(term.Bytes())
	require.NotContains(t, stripped, "docker ps")
	require.NotContains(t, stripped, "data-line")
}

// TestLiveLine_PrintlnDiagFallsBackToScreen asserts that without a configured
// diag writer, PrintlnDiag behaves like Println (writes to the screen).
func TestLiveLine_PrintlnDiagFallsBackToScreen(t *testing.T) {
	var term, scr bytes.Buffer
	l := NewLiveLine(&term, &scr, false) // disabled: straight-to-data-writer path
	l.PrintlnDiag("$ docker ps")
	require.Equal(t, "$ docker ps\n", scr.String())

	// Active live mode with no diag writer: the line still falls back to the
	// screen (data) writer and must not bleed into the term (footer) channel.
	var term2, scr2 bytes.Buffer
	l2 := newTestLiveLine(&term2, &scr2, true)
	l2.SetText("phase: running")
	l2.Start()
	l2.PrintlnDiag("$ docker ps")
	l2.Stop()
	require.Equal(t, "$ docker ps\n", scr2.String())
	require.NotContains(t, stripANSIBytes(term2.Bytes()), "docker ps")
}

func TestLiveLine_CursorInvariantSingle(t *testing.T) {
	g := newTermGrid(8, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("running step-A")
	l.Start()

	// After Start: footer at row 0, cursor at row 1 col 0.
	row, col := g.cursor()
	require.Equal(t, 1, row, "cursor must be below footer after Start")
	require.Equal(t, 0, col)
	require.Contains(t, g.line(0), "running step-A")

	l.Println("output line 1")
	// After Println: data at row 0, footer at row 1, cursor at row 2.
	row, col = g.cursor()
	require.Equal(t, 2, row)
	require.Equal(t, 0, col)
	require.Equal(t, "output line 1", g.line(0))
	require.Contains(t, g.line(1), "running step-A")
	require.Equal(t, "", g.line(2))

	l.SetText("running step-B")
	l.Tick()
	// After tick: footer updates in place at row 1; cursor at row 2.
	row, _ = g.cursor()
	require.Equal(t, 2, row)
	require.Contains(t, g.line(1), "running step-B")

	l.Println("output line 2")
	row, _ = g.cursor()
	require.Equal(t, 3, row)
	require.Equal(t, "output line 2", g.line(1))
	require.Contains(t, g.line(2), "running step-B")

	l.Stop()
	// After Stop: footer erased; cursor on former-footer row.
	require.Equal(t, "", g.line(2))
}

func TestLiveLine_PauseExceptionAndResume(t *testing.T) {
	// Pause INTENTIONALLY leaves the cursor on the cleared former-footer row
	// (NOT below). huh-based prompts render in place from the current cursor;
	// leaving the cursor below would leave a blank gap above the prompt.
	g := newTermGrid(8, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("paused step")
	l.Start()
	require.Contains(t, g.line(0), "paused step")
	row, col := g.cursor()
	require.Equal(t, 1, row)
	require.Equal(t, 0, col)

	l.Pause()
	// After Pause: cursor on the cleared former-footer row (row 0).
	row, col = g.cursor()
	require.Equal(t, 0, row, "Pause exception: cursor on cleared former-footer row")
	require.Equal(t, 0, col)
	require.Equal(t, "", g.line(0))

	l.Resume()
	// After Resume: invariant restored — cursor below newly painted footer.
	row, _ = g.cursor()
	require.Equal(t, 1, row)
	require.Contains(t, g.line(0), "paused step")

	l.Stop()
}

func TestLiveLine_StopIdempotent(t *testing.T) {
	var term, scr bytes.Buffer
	l := newTestLiveLine(&term, &scr, true)
	l.Start()
	l.Stop()
	// Second Stop is a no-op (stopOnce).
	l.Stop()
	// Late Println is safe (no panic) and writes to screen.
	l.Println("late")
	require.Contains(t, scr.String(), "late\n")
}

func TestLiveLine_GoleakBaseline(t *testing.T) {
	// The Stop() path joins the ticker goroutine; TestMain's
	// goleak.VerifyTestMain catches leaks, so this test mostly documents the
	// contract: Start → Stop must return goroutine count to baseline.
	before := runtime.NumGoroutine()
	var term, scr bytes.Buffer
	l := NewLiveLine(&term, &scr, true)
	l.Start()
	time.Sleep(20 * time.Millisecond) // let the real ticker run a beat
	l.Stop()
	// Give the goroutine a moment to fully unwind.
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()
	require.LessOrEqual(t, after, before+1, "Stop must reclaim ticker goroutine")
}

func TestLiveLine_Concurrency(t *testing.T) {
	var term, scr bytes.Buffer
	l := newTestLiveLine(&term, &scr, true)
	l.Start()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			l.Println(fmt.Sprintf("data-%d", i))
		})
	}
	for i := range 100 {
		wg.Go(func() {
			l.SetText(fmt.Sprintf("text-%d", i))
		})
	}
	for range 10 {
		wg.Go(func() {
			l.Tick()
		})
	}
	wg.Wait()
	l.Stop()

	// screen should contain exactly 100 newline-terminated data lines.
	lines := strings.Split(strings.TrimRight(scr.String(), "\n"), "\n")
	require.Len(t, lines, 100)
	for _, line := range lines {
		require.Regexp(t, `^data-\d+$`, line)
	}
}

func TestLiveLine_SetTextNoOpWhenDisabled(t *testing.T) {
	var term, scr bytes.Buffer
	l := NewLiveLine(&term, &scr, false)
	l.SetText("ignored")
	require.Empty(t, term.String())
	require.Empty(t, scr.String())
}

func TestLiveLine_StartBlockReservesRows(t *testing.T) {
	g := newTermGrid(10, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("group")
	l.Start()
	// After Start: footer at row 0, cursor at row 1.
	row, col := g.cursor()
	require.Equal(t, 1, row)
	require.Equal(t, 0, col)

	l.StartBlock(3)
	// After StartBlock(3): rows 0-2 are block rows (blank), row 3 is footer,
	// cursor below footer at row 4.
	row, col = g.cursor()
	require.Equal(t, 4, row, "cursor must be below footer (blockRows + 1 rows down)")
	require.Equal(t, 0, col)
	require.Equal(t, "", g.line(0))
	require.Equal(t, "", g.line(1))
	require.Equal(t, "", g.line(2))
	require.Contains(t, g.line(3), "group")

	l.Stop()
}

func TestLiveLine_SetBlockRowPaintsContent(t *testing.T) {
	g := newTermGrid(10, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("group")
	l.Start()
	l.StartBlock(3)
	l.SetBlockRowRunning(0, "sub-a running")
	l.SetBlockRowRunning(1, "sub-b running")
	l.SetBlockRowRunning(2, "sub-c running")

	// Rows are rendered as "  <spinner> [<elapsed>] <label>"; label suffix
	// is what we care about here. ANSI codes are consumed by termGrid.
	require.Contains(t, g.line(0), "sub-a running")
	require.Contains(t, g.line(1), "sub-b running")
	require.Contains(t, g.line(2), "sub-c running")
	require.Contains(t, g.line(3), "group")
	row, _ := g.cursor()
	require.Equal(t, 4, row, "cursor below footer after SetBlockRow redraw")

	// Out-of-range calls are silent no-ops.
	l.SetBlockRowRunning(-1, "ignored")
	l.SetBlockRowRunning(99, "ignored")
	require.Contains(t, g.line(0), "sub-a running")

	l.Stop()
}

func TestLiveLine_PrintlnInBlockMode(t *testing.T) {
	g := newTermGrid(10, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("group")
	l.Start()
	l.StartBlock(3)
	l.SetBlockRowRunning(0, "a")
	l.SetBlockRowRunning(1, "b")
	l.SetBlockRowRunning(2, "c")

	l.Println("scrollback line")
	// Data line lands at row 0, block shifts down to rows 1-3, footer at row 4,
	// cursor at row 5.
	require.Equal(t, "scrollback line", g.line(0))
	require.Contains(t, g.line(1), "a")
	require.Contains(t, g.line(2), "b")
	require.Contains(t, g.line(3), "c")
	require.Contains(t, g.line(4), "group")
	row, _ := g.cursor()
	require.Equal(t, 5, row, "cursor below new footer after Println in block mode")

	l.Stop()
}

func TestLiveLine_EndBlockErasesOldFooter(t *testing.T) {
	g := newTermGrid(10, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("group")
	l.Start()
	l.StartBlock(2)
	l.SetBlockRowFinal(0, BlockRowDone, "sub-a done")
	l.SetBlockRowFinal(1, BlockRowFailed, "sub-b failed")
	// State: rows 0-1 block, row 2 footer, cursor row 3.

	l.EndBlock()
	// EndBlock erases the old live footer at row 2 and paints a fresh
	// single-line footer there. The block rows (rows 0-1) persist as
	// scrollback; the old footer is GONE so the user never sees a frozen
	// spinner-mid-frame.
	require.Contains(t, g.line(0), "sub-a done")
	require.Contains(t, g.line(1), "sub-b failed")
	require.Contains(t, g.line(2), "group", "new single-line footer at the row the old footer occupied")
	row, _ := g.cursor()
	require.Equal(t, 3, row, "cursor below new single-line footer")

	// Subsequent Println goes through single-line path.
	l.Println("after block")
	require.Equal(t, "after block", g.line(2))
	require.Contains(t, g.line(3), "group")
	row, _ = g.cursor()
	require.Equal(t, 4, row)

	l.Stop()
}

func TestLiveLine_BlockToSingleAndBackToBlock(t *testing.T) {
	g := newTermGrid(20, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("phase")
	l.Start()

	l.StartBlock(2)
	l.SetBlockRowFinal(0, BlockRowDone, "first-a")
	l.SetBlockRowFinal(1, BlockRowDone, "first-b")
	l.EndBlock()
	// First block's frozen rows persist at rows 0-1; the fresh single-line
	// footer takes the slot the previous live footer occupied (row 2).

	l.Println("between blocks")
	// "between blocks" lands at row 2 (former footer slot), new footer at row 3.

	l.StartBlock(2)
	l.SetBlockRowRunning(0, "second-a")
	l.SetBlockRowRunning(1, "second-b")

	require.Contains(t, g.line(0), "first-a")
	require.Contains(t, g.line(1), "first-b")
	require.Equal(t, "between blocks", g.line(2))
	require.Contains(t, g.line(3), "second-a")
	require.Contains(t, g.line(4), "second-b")
	require.Contains(t, g.line(5), "phase")

	l.EndBlock()
	l.Stop()
}

func TestLiveLine_BlockTickRedraws(t *testing.T) {
	g := newTermGrid(10, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("group")
	l.Start()
	l.StartBlock(2)
	l.SetBlockRowRunning(0, "row-a")
	l.SetBlockRowRunning(1, "row-b")

	l.Tick()
	// Tick advances spinner; block content + footer must remain intact.
	require.Contains(t, g.line(0), "row-a")
	require.Contains(t, g.line(1), "row-b")
	require.Contains(t, g.line(2), "group")
	row, _ := g.cursor()
	require.Equal(t, 3, row)

	l.Stop()
}

func TestLiveLine_BlockChannelSeparation(t *testing.T) {
	var term, scr bytes.Buffer
	l := newTestLiveLine(&term, &scr, true)
	l.SetText("group")
	l.Start()
	l.StartBlock(2)
	l.SetBlockRowRunning(0, "alpha")
	l.SetBlockRowRunning(1, "beta")
	l.Println("data line")
	l.EndBlock()
	l.Stop()

	// Block row content goes to termOut (cursor-and-paint channel); data lines
	// to screen. Verify the channel split is honored in block mode.
	require.Equal(t, "data line\n", scr.String(), "screen receives only data lines")
	stripped := stripANSIBytes(term.Bytes())
	require.Contains(t, stripped, "alpha")
	require.Contains(t, stripped, "beta")
	require.NotContains(t, stripped, "data line")
}

func TestLiveLine_BlockNoOpWhenDisabled(t *testing.T) {
	var term, scr bytes.Buffer
	l := NewLiveLine(&term, &scr, false)
	l.StartBlock(3)
	l.SetBlockRowRunning(0, "ignored")
	l.EndBlock()
	require.Empty(t, term.String())
	require.Empty(t, scr.String())
}

func TestLiveLine_StartBlockTwiceIsNoOp(t *testing.T) {
	g := newTermGrid(10, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("phase")
	l.Start()
	l.StartBlock(2)
	// Second StartBlock while a block is active must not reserve more rows.
	l.StartBlock(5)
	l.SetBlockRowRunning(0, "x")
	l.SetBlockRowRunning(1, "y")
	require.Contains(t, g.line(0), "x")
	require.Contains(t, g.line(1), "y")
	require.Contains(t, g.line(2), "phase")
	l.Stop()
}

func TestLiveLine_BlockViewportScroll(t *testing.T) {
	// Small grid forces scroll when StartBlock reserves more rows than fit.
	// Verify no panic and final state is sensible.
	g := newTermGrid(4, 80)
	l := newTestLiveLine(g, g, true)
	l.SetText("g")
	l.Start()
	l.StartBlock(3)
	l.SetBlockRowRunning(0, "a")
	l.SetBlockRowRunning(1, "b")
	l.SetBlockRowRunning(2, "c")
	// Footer must be visible somewhere in the grid; cursor must be in range.
	row, _ := g.cursor()
	require.GreaterOrEqual(t, row, 0)
	require.Less(t, row, g.rows)
	l.Stop()
}

func TestLiveLine_TruncateRespectsWidth(t *testing.T) {
	var term, scr bytes.Buffer
	l := NewLiveLine(&term, &scr, true)
	l.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 20 }}
	l.SetText(strings.Repeat("x", 100))
	l.Start()
	l.Stop()
	// Inspect the visible footer text from termOut (strip ANSI).
	visible := stripANSIBytes(term.Bytes())
	// Visible width must not exceed configured width (allow trailing newline).
	for line := range strings.SplitSeq(visible, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 20, "footer truncated to width: %q", line)
	}
}

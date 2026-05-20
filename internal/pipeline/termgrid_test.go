package pipeline

import (
	"strings"
)

// termGrid is a tiny ANSI-aware virtual terminal used by cursor-invariant
// tests. It implements io.Writer so LiveLine can write both its termOut
// cursor sequences AND screen data lines into the SAME grid in actual write
// order, mirroring how a real terminal consumes a single byte stream.
//
// Mirror of internal/liveui/liveline_test.go's helper, retained here so the
// pipeline package's own tests can keep their cursor-invariant assertions
// without depending on liveui's _test.go test helpers (which are not exported
// outside the liveui test binary).
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

package render

import "github.com/charmbracelet/lipgloss"

// columnRole selects how a column is presented in record mode.
type columnRole int

const (
	roleField columnRole = iota // "label  value" line (default)
	roleTitle                   // joined into the record's header line
	roleBody                    // own line, no label
	roleGlyph                   // bare prefix on the header line, no separator
)

// columnSpec declares how one column behaves under width pressure. A column
// with a nil Wrap cannot shrink, so Flex is meaningless for it.
type columnSpec struct {
	Flex bool                     // may shrink; fixed columns keep natural width
	Max  int                      // natural-width cap on cell content (0 = uncapped)
	Wrap func(string, int) string // wrapText for prose, wrapPath for paths; nil = never wraps
	Role columnRole
}

// cellAt returns row[col], or "" when the row is shorter than col — rows are
// allowed to be ragged (Table() accepts caller-supplied data).
func cellAt(row []string, col int) string {
	if col < 0 || col >= len(row) {
		return ""
	}
	return row[col]
}

// naturalWidths computes each column's unconstrained width: the wider of its
// header and its widest cell, with cell content capped at Max when Max > 0.
// The cap applies only to cell content — clamping the header too would let
// natural drop below the column's floor and make deficit distribution see
// negative headroom. The Max cap can still land below the column's floor when
// a cell holds an unbreakable token wider than Max; fitRows raises natural
// back to the floor for exactly that reason.
func naturalWidths(headers []string, rows [][]string, cols []columnSpec) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		width := lipgloss.Width(h)
		cellMax := 0
		for _, row := range rows {
			if w := lipgloss.Width(cellAt(row, i)); w > cellMax {
				cellMax = w
			}
		}
		if i < len(cols) && cols[i].Max > 0 && cellMax > cols[i].Max {
			cellMax = cols[i].Max
		}
		if cellMax > width {
			width = cellMax
		}
		widths[i] = width
	}
	return widths
}

// columnFloors computes the narrowest width each column can be squeezed to
// without splitting something that must stay whole (a header, a URL token, a
// single wide rune). It is not clamped by Max: a token wider than Max still
// cannot be broken below its own width.
func columnFloors(headers []string, rows [][]string, cols []columnSpec) []int {
	floors := make([]int, len(headers))
	for i, h := range headers {
		var wrap func(string, int) string
		if i < len(cols) {
			wrap = cols[i].Wrap
		}
		floor := lipgloss.Width(h)
		for _, row := range rows {
			if w := longestUnbreakableToken(cellAt(row, i), wrap); w > floor {
				floor = w
			}
		}
		floors[i] = floor
	}
	return floors
}

// fitRows re-wraps rows to fitted column widths. ok=false means the columns
// do not fit even at their floors — the caller falls back to the record
// layout.
//
// budget == 0 disables terminal-driven shrinking, NOT wrapping: columns still
// take their Max-clamped natural widths and every cell is still wrapped at
// those widths. Only deficit distribution and the floor fallback are skipped.
func fitRows(headers []string, rows [][]string, budget, padding int, cols []columnSpec) ([][]string, bool) {
	probed := columnFloors(headers, rows, cols)
	natural := naturalWidths(headers, rows, cols)
	// A Max cap cannot push a column below its unbreakable-token width: the
	// wrap helpers never split such a token (a URL, a single wide rune), so
	// Lipgloss lays the column out at the token's width no matter what width
	// we compute. Raising natural to the floor keeps the fit arithmetic in
	// step with what actually renders — otherwise a Max-capped column holding
	// an over-long token yields negative headroom in distributeDeficit, which
	// *widens* the column and lets the table overflow the budget.
	for i := range natural {
		if probed[i] > natural[i] {
			natural[i] = probed[i]
		}
	}
	chrome := len(headers) + 1 + 2*padding*len(headers)

	widths := natural
	if budget != 0 && sumInts(natural)+chrome > budget {
		floors := effectiveFloors(probed, cols, natural)
		if sumInts(floors)+chrome > budget {
			return nil, false
		}
		widths = distributeDeficit(natural, floors, cols, budget-chrome)
	}

	out := make([][]string, len(rows))
	for i, row := range rows {
		outRow := make([]string, len(row))
		for j, cell := range row {
			var wrap func(string, int) string
			if j < len(cols) {
				wrap = cols[j].Wrap
			}
			if wrap == nil {
				outRow[j] = cell
				continue
			}
			w := 0
			if j < len(widths) {
				w = widths[j]
			}
			outRow[j] = wrap(cell, w)
		}
		out[i] = outRow
	}
	return out, true
}

// effectiveFloors returns, per column, the width used for the fits-or-not
// decision and as the lower clamp during deficit distribution: the probed
// floor (from columnFloors) for Flex columns (which may shrink), or the
// natural width for fixed columns (which never do).
func effectiveFloors(probed []int, cols []columnSpec, natural []int) []int {
	floors := make([]int, len(natural))
	for i := range natural {
		if i < len(cols) && cols[i].Flex {
			floors[i] = probed[i]
			continue
		}
		floors[i] = natural[i]
	}
	return floors
}

// distributeDeficit narrows Flex columns proportional to their headroom
// (natural − floor) so the total column width fits within available. Fixed
// columns keep their natural width. Precondition: sum(floors) <= available,
// which the caller has already verified.
func distributeDeficit(natural, floors []int, cols []columnSpec, available int) []int {
	widths := make([]int, len(natural))
	copy(widths, natural)

	deficit := sumInts(natural) - available
	if deficit <= 0 {
		return widths
	}

	headroom := make([]int, len(natural))
	totalHeadroom := 0
	for i := range natural {
		if i < len(cols) && cols[i].Flex {
			// max(…, 0): fitRows already raises natural to the probed floor,
			// so this cannot be negative — clamped anyway so a future caller
			// passing floors > natural under-distributes rather than silently
			// widening the column past its natural width.
			headroom[i] = max(natural[i]-floors[i], 0)
			totalHeadroom += headroom[i]
		}
	}
	if totalHeadroom <= 0 {
		return widths
	}

	reduceBy := make([]int, len(natural))
	distributed := 0
	for i, h := range headroom {
		if h <= 0 {
			continue
		}
		r := deficit * h / totalHeadroom
		reduceBy[i] = r
		distributed += r
	}

	// Largest-remainder leftover: floor division under-allocates by up to
	// len(flex columns)-1. Hand the remainder out one unit at a time to
	// columns with unused headroom, so the total reduction lands exactly on
	// deficit without ever shrinking a column past its floor.
	remaining := deficit - distributed
	for remaining > 0 {
		progressed := false
		for i, h := range headroom {
			if remaining == 0 {
				break
			}
			if reduceBy[i] < h {
				reduceBy[i]++
				remaining--
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}

	for i := range widths {
		widths[i] = natural[i] - reduceBy[i]
	}
	return widths
}

func sumInts(vs []int) int {
	total := 0
	for _, v := range vs {
		total += v
	}
	return total
}

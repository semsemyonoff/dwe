package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// rectBase builds a w×h rectangular base string of 'x' cells (no ANSI), so
// dimension and dimming assertions have a known, style-free starting point.
func rectBase(w, h int) string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = strings.Repeat("x", w)
	}
	return strings.Join(rows, "\n")
}

func TestOverlayStack_PushPopTopEmpty(t *testing.T) {
	var s overlayStack

	if !s.Empty() {
		t.Fatal("new stack should be empty")
	}
	if _, ok := s.Top(); ok {
		t.Fatal("Top on empty stack should report false")
	}
	if _, ok := s.Pop(); ok {
		t.Fatal("Pop on empty stack should report false")
	}

	first := Overlay{Content: "first", Width: 5, Height: 1}
	second := Overlay{Content: "second", Width: 6, Height: 1}
	s.Push(first)
	s.Push(second)

	if s.Empty() {
		t.Fatal("stack with two overlays should not be empty")
	}

	// Mutual exclusivity: only the top overlay is ever visible.
	top, ok := s.Top()
	if !ok || top.Content != "second" {
		t.Fatalf("Top = %q, %v; want \"second\", true", top.Content, ok)
	}

	popped, ok := s.Pop()
	if !ok || popped.Content != "second" {
		t.Fatalf("Pop = %q, %v; want \"second\", true", popped.Content, ok)
	}

	// Popping the top reveals the one beneath — still exactly one visible.
	top, ok = s.Top()
	if !ok || top.Content != "first" {
		t.Fatalf("after pop Top = %q, %v; want \"first\", true", top.Content, ok)
	}

	if _, ok := s.Pop(); !ok {
		t.Fatal("Pop of last overlay should report true")
	}
	if !s.Empty() {
		t.Fatal("stack should be empty after popping all overlays")
	}
}

func TestCenterOffset(t *testing.T) {
	tests := []struct {
		name  string
		body  Region
		ov    Overlay
		wantX int
		wantY int
	}{
		{
			name:  "even body even overlay",
			body:  Region{X: 0, Y: 0, Width: 80, Height: 24},
			ov:    Overlay{Width: 40, Height: 10},
			wantX: 20, wantY: 7,
		},
		{
			name:  "odd body floors offset",
			body:  Region{X: 0, Y: 0, Width: 79, Height: 23},
			ov:    Overlay{Width: 40, Height: 10},
			wantX: 19, wantY: 6, // (79-40)/2 = 19, (23-10)/2 = 6
		},
		{
			name:  "origin offset is added",
			body:  Region{X: 2, Y: 1, Width: 60, Height: 20},
			ov:    Overlay{Width: 20, Height: 6},
			wantX: 2 + 20, wantY: 1 + 7, // 2+(60-20)/2, 1+(20-6)/2
		},
		{
			name:  "overlay wider than body clamps to origin",
			body:  Region{X: 3, Y: 4, Width: 10, Height: 5},
			ov:    Overlay{Width: 40, Height: 20},
			wantX: 3, wantY: 4,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			x, y := centerOffset(tc.body, tc.ov)
			if x != tc.wantX || y != tc.wantY {
				t.Fatalf("centerOffset = (%d, %d); want (%d, %d)", x, y, tc.wantX, tc.wantY)
			}
		})
	}
}

// TestComposite_PreservesDimensions verifies the composited frame has the same
// cell dimensions as the base across width buckets — the overlay must not change
// the body region's total size.
func TestComposite_PreservesDimensions(t *testing.T) {
	for _, w := range []int{60, 79, 80, 99, 100} {
		h := 20
		base := rectBase(w, h)
		ov := Overlay{Content: "AAAA\nAAAA\nAAAA", Width: 4, Height: 3}
		out := Composite(base, ov, Region{X: 0, Y: 0, Width: w, Height: h})

		if got := lipgloss.Height(out); got != h {
			t.Errorf("width %d: composite height = %d; want %d", w, got, h)
		}
		for i, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("width %d: row %d width = %d; want %d", w, i, got, w)
			}
		}
	}
}

// TestComposite_DimsBody asserts the body beneath the overlay is dimmed — the
// plain (style-free) base gains styling escapes once composited.
func TestComposite_DimsBody(t *testing.T) {
	base := rectBase(20, 6)
	if strings.Contains(base, "\x1b") {
		t.Fatal("precondition: plain base must contain no ANSI escapes")
	}
	ov := Overlay{Content: "AA\nAA", Width: 2, Height: 2}
	out := Composite(base, ov, Region{X: 0, Y: 0, Width: 20, Height: 6})

	if !strings.Contains(out, "\x1b") {
		t.Fatal("composited body should be dimmed (styling escapes added)")
	}
	// The visible body characters survive the dimming.
	if !strings.Contains(out, "x") {
		t.Fatal("dimming must preserve the body content")
	}
}

// TestComposite_StyledBasePreserved feeds a styled base through Composite and
// asserts the cell dimensions are preserved (ANSI-width safety) and the styled
// content survives compositing.
func TestComposite_StyledBasePreserved(t *testing.T) {
	cell := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render("x")
	rows := make([]string, 6)
	for i := range rows {
		rows[i] = strings.Repeat(cell, 20)
	}
	base := strings.Join(rows, "\n")
	if got := lipgloss.Width(base); got != 20 {
		t.Fatalf("precondition: styled base width = %d; want 20", got)
	}

	ov := Overlay{Content: "AA\nAA", Width: 2, Height: 2}
	out := Composite(base, ov, Region{X: 0, Y: 0, Width: 20, Height: 6})

	if got := lipgloss.Height(out); got != 6 {
		t.Errorf("styled composite height = %d; want 6", got)
	}
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got != 20 {
			t.Errorf("styled composite row %d width = %d; want 20", i, got)
		}
	}
	// The overlay content is composited over the base.
	if !strings.Contains(out, "A") {
		t.Fatal("overlay content should appear in the composite")
	}
}

// TestComposite_ClampsOversizedOverlay guards the never-overflow invariant: an
// overlay larger than the body region (e.g. a stale help modal built for a
// previous, larger geometry, or an oversized plugin overlay) must be clamped so
// the composited frame keeps the body's cell dimensions rather than growing past
// the terminal bounds.
func TestComposite_ClampsOversizedOverlay(t *testing.T) {
	w, h := 40, 10
	base := rectBase(w, h)

	bigRows := make([]string, 17) // taller than the 10-row body
	for i := range bigRows {
		bigRows[i] = strings.Repeat("A", 60) // wider than the 40-col body
	}
	ov := Overlay{Content: strings.Join(bigRows, "\n"), Width: 60, Height: 17}

	out := Composite(base, ov, Region{X: 0, Y: 0, Width: w, Height: h})

	if got := lipgloss.Height(out); got != h {
		t.Errorf("clamped composite height = %d; want %d", got, h)
	}
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got != w {
			t.Errorf("clamped composite row %d width = %d; want %d", i, got, w)
		}
	}
}

// TestOverlayClickPolicy pins the Stage 0 outside-click policy default and keeps
// the documented Stage-2 seam constant referenced.
func TestOverlayClickPolicy(t *testing.T) {
	if !overlayClicksOutsideSwallowed {
		t.Fatal("Stage 0 policy: clicks outside the modal are swallowed, not dismissed")
	}
}

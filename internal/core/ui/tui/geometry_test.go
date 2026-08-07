package tui

import "testing"

// widthBuckets are the canonical render widths exercised across the framework:
// odd (79, 99) and even (60, 80, 100) so remainder policies are stressed.
var widthBuckets = []int{60, 79, 80, 99, 100}

func TestNewGeometry_InnerIsOuterMinusChrome(t *testing.T) {
	t.Parallel()
	const h = 26
	for _, w := range widthBuckets {
		t.Run(itoa(w), func(t *testing.T) {
			t.Parallel()
			g := newGeometry(w, h)

			// Outer is the terminal minus the status line.
			if g.Outer.Width != w {
				t.Errorf("outer width = %d, want %d", g.Outer.Width, w)
			}
			if g.Outer.Height != h-statusLineRows {
				t.Errorf("outer height = %d, want %d", g.Outer.Height, h-statusLineRows)
			}

			// Inner is outer minus border + padding on every side.
			wantInnerW := g.Outer.Width - 2*(borderSize+hPadding)
			wantInnerH := g.Outer.Height - 2*(borderSize+vPadding)
			if g.Inner.Width != wantInnerW {
				t.Errorf("inner width = %d, want %d", g.Inner.Width, wantInnerW)
			}
			if g.Inner.Height != wantInnerH {
				t.Errorf("inner height = %d, want %d", g.Inner.Height, wantInnerH)
			}
			if g.Inner.X != borderSize+hPadding {
				t.Errorf("inner X = %d, want %d", g.Inner.X, borderSize+hPadding)
			}
			if g.Inner.Y != borderSize+vPadding {
				t.Errorf("inner Y = %d, want %d", g.Inner.Y, borderSize+vPadding)
			}
		})
	}
}

func TestNewGeometry_OverlayExcludesStatusLine(t *testing.T) {
	t.Parallel()
	const h = 26
	for _, w := range widthBuckets {
		t.Run(itoa(w), func(t *testing.T) {
			t.Parallel()
			g := newGeometry(w, h)

			// The overlay coordinate space is the inner body region.
			if g.Overlay != g.Inner {
				t.Errorf("overlay = %+v, want inner %+v", g.Overlay, g.Inner)
			}

			// The overlay must never reach into the status line row.
			statusTop := g.Status.Y
			if g.Status.Y != h-statusLineRows {
				t.Errorf("status Y = %d, want %d", g.Status.Y, h-statusLineRows)
			}
			if bottom := g.Overlay.Y + g.Overlay.Height; bottom > statusTop {
				t.Errorf("overlay bottom = %d reaches status line at %d", bottom, statusTop)
			}
		})
	}
}

func TestNewGeometry_ClampsDegenerateSize(t *testing.T) {
	t.Parallel()
	g := newGeometry(0, 0)
	for _, r := range []Region{g.Outer, g.Inner, g.Term} {
		if r.Width < 0 || r.Height < 0 {
			t.Errorf("negative region produced: %+v", r)
		}
	}
}

func TestTooNarrow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		w, h int
		want bool
	}{
		{"exactly minimum", minWidth, minHeight, false},
		{"one below width", minWidth - 1, minHeight, true},
		{"one below height", minWidth, minHeight - 1, true},
		{"both above", minWidth + 50, minHeight + 50, false},
		{"both below", minWidth - 1, minHeight - 1, true},
		{"zero", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tooNarrow(tc.w, tc.h); got != tc.want {
				t.Errorf("tooNarrow(%d, %d) = %v, want %v", tc.w, tc.h, got, tc.want)
			}
		})
	}
}

func TestLayoutPanels_WidthsSumExactly(t *testing.T) {
	t.Parallel()
	layouts := []struct {
		name    string
		weights []int
	}{
		{"single", []int{1}},
		{"two equal", []int{1, 1}},
		{"two skewed", []int{2, 7}},
		{"three mixed", []int{1, 2, 3}},
	}
	for _, w := range widthBuckets {
		for _, lay := range layouts {
			t.Run(itoa(w)+"_"+lay.name, func(t *testing.T) {
				t.Parallel()
				body := Region{X: 0, Y: 1, Width: w, Height: 20}
				got := layoutPanels(body, lay.weights)

				if len(got) != len(lay.weights) {
					t.Fatalf("got %d regions, want %d", len(got), len(lay.weights))
				}

				sum := 0
				x := body.X
				for i, r := range got {
					if r.Y != body.Y || r.Height != body.Height {
						t.Errorf("panel %d Y/Height = %d/%d, want %d/%d", i, r.Y, r.Height, body.Y, body.Height)
					}
					if r.X != x {
						t.Errorf("panel %d X = %d, want contiguous %d", i, r.X, x)
					}
					if r.Width <= 0 {
						t.Errorf("panel %d width = %d, want positive", i, r.Width)
					}
					x += r.Width
					sum += r.Width
				}
				if sum != body.Width {
					t.Errorf("panel widths sum = %d, want exactly %d", sum, body.Width)
				}
			})
		}
	}
}

func TestLayoutPanels_RemainderLandsOnLastPanel(t *testing.T) {
	t.Parallel()
	// Width 79, weights {2,7}: 79*2/9 = 17 (floor), so panel 0 = 17 and the
	// last panel absorbs 79-17 = 62. A naive per-panel floor would give
	// 17 + 61 = 78, leaking a column.
	body := Region{X: 0, Y: 0, Width: 79, Height: 10}
	got := layoutPanels(body, []int{2, 7})
	if got[0].Width != 17 {
		t.Errorf("panel 0 width = %d, want 17 (proportional floor)", got[0].Width)
	}
	if got[1].Width != 62 {
		t.Errorf("panel 1 width = %d, want 62 (remainder absorbed)", got[1].Width)
	}
}

func TestLayoutPanels_RespectsBodyOffset(t *testing.T) {
	t.Parallel()
	body := Region{X: 5, Y: 3, Width: 40, Height: 8}
	got := layoutPanels(body, []int{1, 1})
	if got[0].X != 5 {
		t.Errorf("first panel X = %d, want body offset 5", got[0].X)
	}
	if got[len(got)-1].X+got[len(got)-1].Width != body.X+body.Width {
		t.Errorf("last panel right edge = %d, want %d", got[len(got)-1].X+got[len(got)-1].Width, body.X+body.Width)
	}
}

// itoa is a tiny local helper so test files pull in no extra imports.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

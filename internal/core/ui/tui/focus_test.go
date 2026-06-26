package tui

import "testing"

func panels(ids ...PanelID) []Panel {
	ps := make([]Panel, len(ids))
	for i, id := range ids {
		ps[i] = Panel{ID: id, Weight: 1}
	}
	return ps
}

func TestFocusManager_StartsOnFirstPanel(t *testing.T) {
	t.Parallel()
	f := newFocusManager(panels("a", "b", "c"))
	if got := f.Active(); got != "a" {
		t.Errorf("Active() = %q, want a (first panel focused at construction)", got)
	}
}

func TestFocusManager_NextWraps(t *testing.T) {
	t.Parallel()
	f := newFocusManager(panels("a", "b", "c"))
	want := []PanelID{"b", "c", "a", "b"}
	for i, w := range want {
		f.Next()
		if got := f.Active(); got != w {
			t.Errorf("after %d Next() = %q, want %q", i+1, got, w)
		}
	}
}

func TestFocusManager_PrevWraps(t *testing.T) {
	t.Parallel()
	f := newFocusManager(panels("a", "b", "c"))
	want := []PanelID{"c", "b", "a", "c"}
	for i, w := range want {
		f.Prev()
		if got := f.Active(); got != w {
			t.Errorf("after %d Prev() = %q, want %q", i+1, got, w)
		}
	}
}

func TestFocusManager_SetKnownAndUnknown(t *testing.T) {
	t.Parallel()
	f := newFocusManager(panels("a", "b", "c"))
	if !f.Set("c") {
		t.Error("Set(c) = false, want true for a known panel")
	}
	if got := f.Active(); got != "c" {
		t.Errorf("Active() = %q after Set(c), want c", got)
	}
	if f.Set("nope") {
		t.Error("Set(nope) = true, want false for an unknown panel")
	}
	if got := f.Active(); got != "c" {
		t.Errorf("Active() = %q after Set(nope), want c (unchanged)", got)
	}
}

func TestFocusManager_ZeroPanels(t *testing.T) {
	t.Parallel()
	f := newFocusManager(nil)
	if got := f.Active(); got != "" {
		t.Errorf("Active() = %q with zero panels, want empty", got)
	}
	// Cycling and Set must be safe no-ops.
	f.Next()
	f.Prev()
	if f.Set("anything") {
		t.Error("Set on zero-panel manager = true, want false")
	}
	if got := f.Active(); got != "" {
		t.Errorf("Active() = %q after no-op cycling, want empty", got)
	}
	// BorderFor must not panic and returns the unfocused style.
	_ = f.BorderFor("anything")
}

func TestFocusManager_SinglePanelCyclingNoop(t *testing.T) {
	t.Parallel()
	f := newFocusManager(panels("only"))
	f.Next()
	if got := f.Active(); got != "only" {
		t.Errorf("Next() on single panel changed focus to %q, want only", got)
	}
	f.Prev()
	if got := f.Active(); got != "only" {
		t.Errorf("Prev() on single panel changed focus to %q, want only", got)
	}
}

func TestFocusManager_BorderForActiveVsInactive(t *testing.T) {
	t.Parallel()
	f := newFocusManager(panels("a", "b"))

	wantFocused := focusedBorder().GetBorderTopForeground()
	wantUnfocused := unfocusedBorder().GetBorderTopForeground()
	if wantFocused == wantUnfocused {
		t.Fatal("focused and unfocused border colors are identical; cannot distinguish focus")
	}

	if got := f.BorderFor("a").GetBorderTopForeground(); got != wantFocused {
		t.Errorf("BorderFor(active a) color = %v, want focused %v", got, wantFocused)
	}
	if got := f.BorderFor("b").GetBorderTopForeground(); got != wantUnfocused {
		t.Errorf("BorderFor(inactive b) color = %v, want unfocused %v", got, wantUnfocused)
	}
	// An unknown ID is never the active panel → unfocused.
	if got := f.BorderFor("nope").GetBorderTopForeground(); got != wantUnfocused {
		t.Errorf("BorderFor(unknown) color = %v, want unfocused %v", got, wantUnfocused)
	}
}

package tui

import "charm.land/lipgloss/v2"

// focusManager tracks which of a plugin's declared panels currently holds focus
// and supplies the per-panel border style ([focusedBorder] vs [unfocusedBorder])
// the frame draws around it. It is the single source of focus truth so the frame
// and the (future) action handlers never disagree about the active panel.
//
// Panel order is the order the plugin returns from [Plugin.Panels]; Next/Prev
// cycle through that order and wrap. The manager is provisional (Stage 0) like the
// rest of the spike.
type focusManager struct {
	// panels is the ordered set of focusable panel IDs, as declared by the plugin.
	panels []PanelID
	// active is the index into panels of the focused panel. It is always in range
	// [0, len(panels)) when panels is non-empty, and ignored when panels is empty.
	active int
}

// newFocusManager builds a focus manager over the plugin's declared panels, with
// the first panel focused. An empty panel set yields a no-op manager (cycling and
// BorderFor are safe but inert); the caller ([newFrame], Task 7) validates that a
// real plugin declares at least one panel before launch.
func newFocusManager(panels []Panel) *focusManager {
	ids := make([]PanelID, len(panels))
	for i, p := range panels {
		ids[i] = p.ID
	}
	return &focusManager{panels: ids}
}

// Active returns the focused panel's ID. With zero panels it returns the empty
// PanelID (no panel can be focused).
func (f *focusManager) Active() PanelID {
	if len(f.panels) == 0 {
		return ""
	}
	return f.panels[f.active]
}

// Next moves focus to the following panel, wrapping past the last back to the
// first. With zero or one panel it is a no-op.
func (f *focusManager) Next() {
	if len(f.panels) < 2 {
		return
	}
	f.active = (f.active + 1) % len(f.panels)
}

// Prev moves focus to the preceding panel, wrapping past the first back to the
// last. With zero or one panel it is a no-op.
func (f *focusManager) Prev() {
	if len(f.panels) < 2 {
		return
	}
	f.active = (f.active - 1 + len(f.panels)) % len(f.panels)
}

// Set focuses the panel with the given ID. An unknown ID (including the empty
// PanelID when no panel matches) leaves focus unchanged and reports false.
func (f *focusManager) Set(id PanelID) bool {
	for i, p := range f.panels {
		if p == id {
			f.active = i
			return true
		}
	}
	return false
}

// BorderFor returns the border style for the given panel: [focusedBorder] for the
// active panel, [unfocusedBorder] for every other panel (including an unknown ID,
// which is never the active panel). The Width/Height are set by the caller.
func (f *focusManager) BorderFor(id PanelID) lipgloss.Style {
	if len(f.panels) > 0 && id == f.panels[f.active] {
		return focusedBorder()
	}
	return unfocusedBorder()
}

package cmdbrowser

import (
	"charm.land/bubbles/v2/viewport"
)

// inspectState owns the viewport overlay shown while the user is inspecting a
// single command. The Model holds a *inspectState; nil means the overlay is
// closed. inspectIdx remembers which item is being shown so Enter can return
// the right Result.
type inspectState struct {
	vp         viewport.Model
	inspectIdx int // index into Model.items
}

// newInspectState builds a viewport sized to fit inside the right panel.
// Width is clamped to min(rightPanelWidth-4, 80) per the spec; height is the
// right-panel body height. Caller passes the already-rendered Inspect string.
func newInspectState(width, height int, content string, idx int) *inspectState {
	w := min(width-4, 80)
	w = max(w, 20)
	h := max(height, 5)
	vp := viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
	vp.SetContent(content)
	return &inspectState{vp: vp, inspectIdx: idx}
}

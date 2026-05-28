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

// newInspectState builds a viewport at the given (width, height). The render
// closure is called with the final content width so the builder word-wraps to
// the actual viewport — passing a pre-rendered string wrapped to the terminal
// would clip on the right edge. A nil render (or one that returns "") falls
// back to a placeholder. The caller is responsible for sizing — see
// Model.inspectViewportSize.
func newInspectState(width, height int, render func(width int) string, idx int) *inspectState {
	w := max(width, 10)
	h := max(height, 3)
	vp := viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
	applyViewportStyles(&vp)
	content := ""
	if render != nil {
		content = render(w)
	}
	if content == "" {
		content = "(no inspect content available)"
	}
	vp.SetContent(content)
	return &inspectState{vp: vp, inspectIdx: idx}
}

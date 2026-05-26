package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
)

type ViewportWidget struct {
	v       viewport.Model
	content string
	width   int
	height  int
	yOffset int
}

func NewViewportWidget(width, height int) *ViewportWidget {
	v := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	v.SetContent("")
	return &ViewportWidget{
		v:       v,
		width:   width,
		height:  height,
		yOffset: 0,
	}
}

func (w *ViewportWidget) SetContent(content string) {
	w.content = content
	w.v.SetContent(content)
	w.yOffset = 0
}

func (w *ViewportWidget) SetDimensions(width, height int) {
	w.width = width
	w.height = height
	// Viewport model dimensions are set via constructor/methods
	// This is a simple wrapper; full integration deferred to later tasks
}

func (w *ViewportWidget) ScrollUp() {
	if w.yOffset > 0 {
		w.yOffset--
	}
}

func (w *ViewportWidget) ScrollDown() {
	lines := strings.Count(w.content, "\n")
	if w.yOffset < lines-w.height {
		w.yOffset++
	}
}

func (w *ViewportWidget) ScrollStart() {
	w.yOffset = 0
}

func (w *ViewportWidget) ScrollEnd() {
	lines := strings.Count(w.content, "\n")
	if lines > w.height {
		w.yOffset = lines - w.height
	}
}

func (w *ViewportWidget) View() string {
	return w.v.View()
}

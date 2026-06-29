package docstui

import (
	"charm.land/bubbles/v2/viewport"
)

// ViewportWidget wraps bubbles/viewport to display rendered markdown content
// in the right panel of the docs browser.
type ViewportWidget struct {
	v       viewport.Model
	content string
	width   int
	height  int
}

// NewViewportWidget returns a new ViewportWidget sized to the given dimensions.
func NewViewportWidget(width, height int) *ViewportWidget {
	v := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	v.SetContent("")
	return &ViewportWidget{
		v:      v,
		width:  width,
		height: height,
	}
}

// SetContent replaces the viewport's rendered content.
func (w *ViewportWidget) SetContent(content string) {
	w.content = content
	w.v.SetContent(content)
}

// SetDimensions resizes the viewport to the given width and height.
func (w *ViewportWidget) SetDimensions(width, height int) {
	w.width = width
	w.height = height
	w.v.SetWidth(width)
	w.v.SetHeight(height)
}

// ScrollUp scrolls the viewport up by one line.
func (w *ViewportWidget) ScrollUp() {
	w.v.ScrollUp(1)
}

// ScrollDown scrolls the viewport down by one line.
func (w *ViewportWidget) ScrollDown() {
	w.v.ScrollDown(1)
}

// ScrollStart jumps to the top of the content.
func (w *ViewportWidget) ScrollStart() {
	w.v.GotoTop()
}

// ScrollEnd jumps to the bottom of the content.
func (w *ViewportWidget) ScrollEnd() {
	w.v.GotoBottom()
}

// PageUp scrolls the viewport up by one full visible height.
func (w *ViewportWidget) PageUp() {
	w.v.PageUp()
}

// PageDown scrolls the viewport down by one full visible height.
func (w *ViewportWidget) PageDown() {
	w.v.PageDown()
}

// View renders the visible portion of the content as a string.
func (w *ViewportWidget) View() string {
	return w.v.View()
}

// ScrollToLine scrolls the viewport so that the given line index sits at the
// top. Used by heading navigation.
func (w *ViewportWidget) ScrollToLine(line int) {
	if line < 0 {
		line = 0
	}
	w.v.SetYOffset(line)
}

// Content returns the raw content set on the viewport. Used by heading
// navigation to locate the line containing a heading's text.
func (w *ViewportWidget) Content() string {
	return w.content
}

// YOffset returns the index of the first visible content line. Used by the
// scrollbar and by diagram focus tracking.
func (w *ViewportWidget) YOffset() int {
	return w.v.YOffset()
}

// VisibleHeight returns the number of content rows the viewport shows at once.
func (w *ViewportWidget) VisibleHeight() int {
	return w.v.Height()
}

// TotalLines returns the total number of content lines (the full document
// height in rendered rows), used to size the scrollbar thumb.
func (w *ViewportWidget) TotalLines() int {
	return w.v.TotalLineCount()
}

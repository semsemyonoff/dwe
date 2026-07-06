package docstui

import (
	"fmt"
)

// StatusBarWidget builds the middle-zone status context string for the docs browser.
type StatusBarWidget struct {
	path     string
	language string

	// focused is the 1-based index of the diagram currently under the reading
	// cursor (the one `o`/`y`/`E` act on); 0 = none. total is the diagram count in
	// the current topic. They render "📊 N/M" whenever a topic has diagrams — shown
	// regardless of render state, so the user always knows which diagram's source
	// `y` will copy even when rendering is disabled (mmdc missing).
	focused int
	total   int

	// rendered is how many diagrams the prefetch pool has finished rendering; shown
	// as a transient "⏳ R/M" suffix while rendering is still in progress.
	rendered int

	// flash is a transient confirmation message (e.g. "copied to clipboard")
	// shown ahead of the normal status until a timer clears it. Empty = none.
	flash string
}

// NewStatusBarWidget returns a new StatusBarWidget with default en language.
func NewStatusBarWidget() *StatusBarWidget {
	return &StatusBarWidget{
		path:     "",
		language: "en",
		focused:  0,
		total:    0,
		rendered: 0,
	}
}

// SetPath updates the displayed topic path.
func (w *StatusBarWidget) SetPath(path string) {
	w.path = path
}

// SetLanguage updates the displayed language tag.
func (w *StatusBarWidget) SetLanguage(lang string) {
	w.language = lang
}

// SetProgress updates the diagram prefetch progress: rendered is how many
// diagrams the pool has finished. The diagram count (total) is owned by
// SetDiagram (sourced from DiagramState), so it is not set here.
func (w *StatusBarWidget) SetProgress(rendered int) {
	w.rendered = rendered
}

// SetDiagram updates the focused-diagram indicator: focused is the 1-based index
// of the diagram under the cursor (0 = none) and total is the diagram count in
// the current topic. Driven each frame from the browser's DiagramState so the
// indicator tracks the cursor regardless of which action moved it.
func (w *StatusBarWidget) SetDiagram(focused, total int) {
	w.focused = focused
	w.total = total
}

// SetFlash sets a transient confirmation message shown ahead of the status.
func (w *StatusBarWidget) SetFlash(msg string) {
	w.flash = msg
}

// ClearFlash removes the transient confirmation message.
func (w *StatusBarWidget) ClearFlash() {
	w.flash = ""
}

// View renders the status bar content: optional transient flash + path +
// optional focused-diagram indicator (+ prefetch progress) + locale tag. When a
// flash is active it leads so it stays visible even if the frame truncates a
// long path.
func (w *StatusBarWidget) View() string {
	status := w.path
	if w.total > 0 {
		// Always show which diagram is focused (o/y/E act on it). While the prefetch
		// pool is still rendering, append the transient "⏳ R/M" progress suffix.
		status += fmt.Sprintf("  📊 %d/%d", w.focused, w.total)
		if w.rendered > 0 && w.rendered < w.total {
			status += fmt.Sprintf("  ⏳ %d/%d", w.rendered, w.total)
		}
	}
	if w.language != "" {
		status += fmt.Sprintf("  [%s]", w.language)
	}
	if w.flash != "" {
		if status == "" {
			return w.flash
		}
		return w.flash + "  ·  " + status
	}
	return status
}

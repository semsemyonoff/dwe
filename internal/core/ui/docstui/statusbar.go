package docstui

import (
	"fmt"
)

// StatusBarWidget builds the middle-zone status context string for the docs browser.
type StatusBarWidget struct {
	path     string
	language string
	rendered int
	total    int

	// flash is a transient confirmation message (e.g. "copied to clipboard")
	// shown ahead of the normal status until a timer clears it. Empty = none.
	flash string
}

// NewStatusBarWidget returns a new StatusBarWidget with default en language.
func NewStatusBarWidget() *StatusBarWidget {
	return &StatusBarWidget{
		path:     "",
		language: "en",
		rendered: 0,
		total:    0,
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

// SetProgress updates the diagram prefetch progress counters.
func (w *StatusBarWidget) SetProgress(rendered, total int) {
	w.rendered = rendered
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
// optional diagram progress + locale tag. When a flash is active it leads so
// it stays visible even if the frame truncates a long path.
func (w *StatusBarWidget) View() string {
	status := w.path
	if w.rendered > 0 && w.total > 0 {
		status += fmt.Sprintf("  📊 %d/%d", w.rendered, w.total)
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

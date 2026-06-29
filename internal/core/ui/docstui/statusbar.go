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

// View renders the status bar content: path + optional diagram progress + locale tag.
func (w *StatusBarWidget) View() string {
	status := w.path
	if w.rendered > 0 && w.total > 0 {
		status += fmt.Sprintf("  📊 %d/%d", w.rendered, w.total)
	}
	if w.language != "" {
		status += fmt.Sprintf("  [%s]", w.language)
	}
	return status
}

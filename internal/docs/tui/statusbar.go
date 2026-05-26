package tui

import (
	"fmt"
)

type StatusBarWidget struct {
	path     string
	language string
	rendered int
	total    int
}

func NewStatusBarWidget() *StatusBarWidget {
	return &StatusBarWidget{
		path:     "",
		language: "en",
		rendered: 0,
		total:    0,
	}
}

func (w *StatusBarWidget) SetPath(path string) {
	w.path = path
}

func (w *StatusBarWidget) SetLanguage(lang string) {
	w.language = lang
}

func (w *StatusBarWidget) SetProgress(rendered, total int) {
	w.rendered = rendered
	w.total = total
}

func (w *StatusBarWidget) View() string {
	status := w.path
	if w.rendered > 0 && w.total > 0 {
		status += fmt.Sprintf("  📊 %d/%d", w.rendered, w.total)
	}
	if w.language != "" {
		status += fmt.Sprintf("  [%s]", w.language)
	}
	status += "  ?:help  q:quit"
	return status
}

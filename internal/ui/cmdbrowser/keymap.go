package cmdbrowser

import "charm.land/bubbles/v2/key"

// keymap holds the bindings handled by the Model across all focus modes. The
// short-help / full-help renderers consult this struct to surface the
// currently-active bindings (see Model.helpView).
type keymap struct {
	Up          key.Binding
	Down        key.Binding
	Left        key.Binding
	Right       key.Binding
	Home        key.Binding
	End         key.Binding
	PgUp        key.Binding
	PgDn        key.Binding
	Space       key.Binding
	Tab         key.Binding
	Enter       key.Binding
	Cancel      key.Binding
	CtrlC       key.Binding
	Filter      key.Binding
	Inspect     key.Binding
	SkipConfirm key.Binding
	Backspace   key.Binding
}

func defaultKeymap() keymap {
	return keymap{
		Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Left:        key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse/back")),
		Right:       key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand/forward")),
		Home:        key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "first")),
		End:         key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "last")),
		PgUp:        key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll up")),
		PgDn:        key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll down")),
		Space:       key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
		Tab:         key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch panel")),
		Enter:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
		Cancel:      key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "quit")),
		CtrlC:       key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Inspect:     key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "inspect")),
		SkipConfirm: key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "skip confirm")),
		Backspace:   key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "delete")),
	}
}

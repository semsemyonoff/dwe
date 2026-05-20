package cmdbrowser

import "charm.land/bubbles/v2/key"

// keymap holds the bindings handled by the Model. Future tasks add filter /
// inspect / skip-confirm bindings; v1 (Task 2) only ships navigation +
// cancel + (preliminary) confirm so the skeleton compiles.
type keymap struct {
	Up      key.Binding
	Down    key.Binding
	Left    key.Binding
	Right   key.Binding
	Home    key.Binding
	End     key.Binding
	Space   key.Binding
	Tab     key.Binding
	Enter   key.Binding
	Cancel  key.Binding
	CtrlC   key.Binding
	Confirm key.Binding
}

func defaultKeymap() keymap {
	return keymap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Left:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse/back")),
		Right:   key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand/forward")),
		Home:    key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "first")),
		End:     key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "last")),
		Space:   key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
		Tab:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch panel")),
		Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
		Cancel:  key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "quit")),
		CtrlC:   key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		Confirm: key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "skip confirm")),
	}
}

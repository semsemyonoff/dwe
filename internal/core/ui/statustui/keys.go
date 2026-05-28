package statustui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	NextTab key.Binding
	PrevTab key.Binding
	Tab1    key.Binding
	Tab2    key.Binding
	Tab3    key.Binding
	Tab4    key.Binding
	Tab5    key.Binding
	Reload  key.Binding
	Help    key.Binding
	Quit    key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.NextTab,
		k.PrevTab,
		k.Reload,
		k.Quit,
	}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{
			k.NextTab,
			k.PrevTab,
			k.Tab1,
			k.Tab2,
		},
		{
			k.Tab3,
			k.Tab4,
			k.Tab5,
			k.Reload,
		},
		{
			k.Help,
			k.Quit,
		},
	}
}

func defaultKeyMap() keyMap {
	return keyMap{
		NextTab: key.NewBinding(
			key.WithKeys("tab", "right"),
			key.WithHelp("tab/→", "next tab"),
		),
		PrevTab: key.NewBinding(
			key.WithKeys("shift+tab", "left"),
			key.WithHelp("shift+tab/←", "prev tab"),
		),
		Tab1: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "services"),
		),
		Tab2: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "deploy"),
		),
		Tab3: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "topology"),
		),
		Tab4: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "git"),
		),
		Tab5: key.NewBinding(
			key.WithKeys("5"),
			key.WithHelp("5", "daemons"),
		),
		Reload: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "reload"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
	}
}

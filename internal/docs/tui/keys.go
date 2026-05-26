package tui

import "charm.land/bubbles/v2/key"

type KeyMap struct {
	Up           key.Binding
	Down         key.Binding
	Left         key.Binding
	Right        key.Binding
	PageUp       key.Binding
	PageDown     key.Binding
	Start        key.Binding
	End          key.Binding
	Enter        key.Binding
	Tab          key.Binding
	Help         key.Binding
	Quit         key.Binding
	SearchStart  key.Binding
	SearchNext   key.Binding
	SearchPrev   key.Binding
	DiagramNext  key.Binding
	DiagramPrev  key.Binding
	DiagramOpen  key.Binding
	DiagramCopy  key.Binding
	LanguageCycle key.Binding
	ShowEnglish  key.Binding
	Reload       key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:           key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
		Down:         key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
		Left:         key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/←", "collapse")),
		Right:        key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "expand")),
		PageUp:       key.NewBinding(key.WithKeys("pgup", "b"), key.WithHelp("b/PgUp", "page up")),
		PageDown:     key.NewBinding(key.WithKeys("pgdn", "f"), key.WithHelp("f/PgDn", "page down")),
		Start:        key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "start")),
		End:          key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "end")),
		Enter:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Tab:          key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "focus")),
		Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:         key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		SearchStart:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		SearchNext:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next match")),
		SearchPrev:   key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "prev match")),
		DiagramNext:  key.NewBinding(key.WithKeys("]"), key.WithHelp("]d", "next diagram")),
		DiagramPrev:  key.NewBinding(key.WithKeys("["), key.WithHelp("[d", "prev diagram")),
		DiagramOpen:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view")),
		DiagramCopy:  key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy")),
		LanguageCycle: key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "language")),
		ShowEnglish:  key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "English")),
		Reload:       key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reload")),
	}
}

func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.Help,
		k.Quit,
	}
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Start, k.End},
		{k.Left, k.Right, k.Enter, k.Tab},
		{k.SearchStart, k.SearchNext, k.SearchPrev},
		{k.DiagramPrev, k.DiagramNext, k.DiagramOpen, k.DiagramCopy},
		{k.LanguageCycle, k.ShowEnglish, k.Reload},
		{k.Help, k.Quit},
	}
}

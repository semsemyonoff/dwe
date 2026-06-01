package widgets

import (
	"errors"
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// ErrCancelled is returned by RunSelector when the user presses q, Esc, or Ctrl-C.
var ErrCancelled = errors.New("cancelled")

// selectMinHeight matches multiSelectMinHeight: huh's default of 10 truncates
// the visible area for projects with many services or commands.
const selectMinHeight = 15

// SelectorItem represents one option in the interactive list.
type SelectorItem struct {
	Label       string // display name, e.g. "main", "services.main.migrate"
	Description string // secondary text, e.g. "app-main", "Run migrations"
	Status      string // state indicator: "enabled", "disabled", or ""
}

// runSelectFormFn is the underlying form runner; swappable in tests.
var runSelectFormFn = defaultRunSelectForm

func defaultRunSelectForm(title string, opts []huh.Option[int]) (int, error) {
	var idx int
	height := max(len(opts)+5, selectMinHeight)
	field := huh.NewSelect[int]().
		Options(opts...).
		Title(title).
		Description("enter: select · q/esc: quit without choosing · /: filter").
		Value(&idx).
		Height(height)

	keymap := huh.NewDefaultKeyMap()
	quitFull := key.NewBinding(key.WithKeys("ctrl+c", "esc", "q"), key.WithHelp("q/esc", "quit"))
	quitNarrow := key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit"))
	filterAwareQuit := newFilterAwareQuit(func(b key.Binding) { keymap.Quit = b }, quitFull, quitNarrow)

	err := huh.NewForm(huh.NewGroup(field)).
		WithTheme(styles.Theme()).
		WithKeyMap(keymap).
		WithShowHelp(true).
		WithProgramOptions(tea.WithFilter(filterAwareQuit)).
		Run()
	return idx, err
}

// buildSelectorOptions converts SelectorItems to huh options whose key includes
// label, description, and a status icon.
//
// Status mapping: "enabled" → ✓, "disabled" → ○, other non-empty → literal text.
// The returned option value is the original index in items so the caller can
// index back into the items slice without a second lookup.
func buildSelectorOptions(items []SelectorItem) []huh.Option[int] {
	opts := make([]huh.Option[int], len(items))
	for i, item := range items {
		key := item.Label
		if item.Description != "" {
			key += "  " + item.Description
		}
		switch item.Status {
		case "enabled":
			key += "  ✓"
		case "disabled":
			key += "  ○"
		default:
			if item.Status != "" {
				key += "  " + item.Status
			}
		}
		opts[i] = huh.NewOption(key, i)
	}
	return opts
}

// RunSelector displays an interactive list selector and returns the index of
// the chosen item in the original items slice. Returns ErrCancelled if the
// user presses q, Esc, or Ctrl-C (huh.ErrUserAborted is translated).
func RunSelector(title string, items []SelectorItem) (int, error) {
	if len(items) == 0 {
		return -1, fmt.Errorf("selector: no items to display")
	}
	before, after := snapshotHuhHooks()
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	opts := buildSelectorOptions(items)
	idx, err := runSelectFormFn(title, opts)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return -1, ErrCancelled
		}
		return -1, err
	}
	return idx, nil
}

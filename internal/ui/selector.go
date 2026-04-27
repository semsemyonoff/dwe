package ui

import (
	"errors"
	"fmt"

	huh "charm.land/huh/v2"
)

// ErrCancelled is returned by RunSelector when the user presses Esc or Ctrl-C.
var ErrCancelled = errors.New("cancelled")

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
	field := huh.NewSelect[int]().Options(opts...).Title(title).Value(&idx)
	return idx, huh.NewForm(huh.NewGroup(field)).WithTheme(Theme()).WithShowHelp(false).Run()
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
// user presses Esc or Ctrl-C (huh.ErrUserAborted is translated).
func RunSelector(title string, items []SelectorItem) (int, error) {
	if len(items) == 0 {
		return -1, fmt.Errorf("selector: no items to display")
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

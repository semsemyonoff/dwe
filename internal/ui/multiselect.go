package ui

import (
	"errors"

	huh "charm.land/huh/v2"
)

// MultiSelectItem is one option in the interactive multi-select form.
type MultiSelectItem struct {
	Key         string // identifier returned in the result
	Label       string // display name shown in the form
	Description string // secondary text shown beside the label
	Locked      bool   // if true, item is excluded from the form (mandatory/always-on)
	Selected    bool   // pre-checked state for toggleable items
}

// MultiSelectResult holds the keys returned after the user submits the form.
// Locked keys are always included regardless of the form outcome.
// Kept contains the keys of items the user left checked (toggleable only).
type MultiSelectResult struct {
	Kept   []string // keys of toggleable items the user left checked
	Locked []string // keys of all locked items (always present)
}

// runMultiSelectFormFn is the underlying form runner; swappable in tests.
var runMultiSelectFormFn = defaultRunMultiSelectForm

func defaultRunMultiSelectForm(title string, opts []huh.Option[string]) ([]string, error) {
	var keys []string
	field := huh.NewMultiSelect[string]().Options(opts...).Title(title).Value(&keys)
	err := huh.NewForm(huh.NewGroup(field)).WithTheme(Theme()).WithShowHelp(false).Run()
	return keys, err
}

// partitionMultiSelect splits items into locked and toggleable slices, preserving
// the relative order of each group. Exported for testing via the internal package.
func partitionMultiSelect(items []MultiSelectItem) (locked, toggleable []MultiSelectItem) {
	for _, item := range items {
		if item.Locked {
			locked = append(locked, item)
		} else {
			toggleable = append(toggleable, item)
		}
	}
	return locked, toggleable
}

// buildMultiSelectOptions converts toggleable MultiSelectItems to huh options.
// Options whose Selected field is true are pre-checked via huh.Option.Selected().
func buildMultiSelectOptions(items []MultiSelectItem) []huh.Option[string] {
	opts := make([]huh.Option[string], len(items))
	for i, item := range items {
		key := item.Label
		if item.Description != "" {
			key += "  " + item.Description
		}
		opt := huh.NewOption(key, item.Key)
		if item.Selected {
			opt = opt.Selected(true)
		}
		opts[i] = opt
	}
	return opts
}

// lockedKeys extracts the Key field from a slice of locked MultiSelectItems.
func lockedKeys(items []MultiSelectItem) []string {
	if len(items) == 0 {
		return nil
	}
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = item.Key
	}
	return keys
}

// RunMultiSelect displays an interactive multi-select form and returns which items
// the user kept checked (Kept) alongside all locked item keys (Locked).
//
// Locked items are never shown in the form. If all items are locked or there are
// no toggleable items, the form is skipped entirely. ErrCancelled is returned when
// the user presses Esc or Ctrl-C.
func RunMultiSelect(title string, items []MultiSelectItem) (MultiSelectResult, error) {
	locked, toggleable := partitionMultiSelect(items)
	lk := lockedKeys(locked)

	if len(toggleable) == 0 {
		return MultiSelectResult{Kept: nil, Locked: lk}, nil
	}

	opts := buildMultiSelectOptions(toggleable)
	kept, err := runMultiSelectFormFn(title, opts)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return MultiSelectResult{}, ErrCancelled
		}
		return MultiSelectResult{}, err
	}
	return MultiSelectResult{Kept: kept, Locked: lk}, nil
}

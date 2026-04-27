package ui

import (
	"errors"

	huh "charm.land/huh/v2"
)

// runConfirmFormFn is the underlying form runner; swappable in tests.
var runConfirmFormFn = defaultRunConfirmForm

func defaultRunConfirmForm(title, affirmative, negative string) (bool, error) {
	var result bool
	field := huh.NewConfirm().
		Title(title).
		Affirmative(affirmative).
		Negative(negative).
		Value(&result)
	err := huh.NewForm(huh.NewGroup(field)).WithTheme(Theme()).WithShowHelp(false).Run()
	return result, err
}

// RunConfirm displays an interactive yes/no confirmation form and returns the
// user's choice. ErrCancelled is returned when the user presses Esc or Ctrl-C.
func RunConfirm(title, affirmative, negative string) (bool, error) {
	result, err := runConfirmFormFn(title, affirmative, negative)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, ErrCancelled
		}
		return false, err
	}
	return result, nil
}

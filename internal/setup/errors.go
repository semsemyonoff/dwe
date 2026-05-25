package setup

import "errors"

// ErrWizardCanceled is returned when the user cancels the wizard via Ctrl-C or Esc.
// The command layer maps this to exit code 130.
var ErrWizardCanceled = errors.New("wizard canceled")

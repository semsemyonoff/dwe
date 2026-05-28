package setup

import "errors"

// ErrWizardCanceled is returned when the user cancels the wizard via Ctrl-C or Esc.
// The command layer treats this as a clean exit (returns nil, exit code 0).
var ErrWizardCanceled = errors.New("wizard canceled")

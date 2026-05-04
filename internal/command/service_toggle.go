package command

import (
	"devbox-cli/internal/ui"
)

// runMultiSelect is a package-level wrapper for ui.RunMultiSelect.
// Tests in this package swap it to inject fake multi-select behaviour.
var runMultiSelect = ui.RunMultiSelect

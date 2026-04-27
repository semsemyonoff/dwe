package command

import (
	"devbox-cli/internal/ui"
)

// runMultiSelect is a package-level wrapper for ui.RunMultiSelect.
// Tests in this package swap it to inject fake multi-select behaviour.
var runMultiSelect = ui.RunMultiSelect

// diffServiceSelection computes which services to enable and which to disable.
// rows is the current service state; kept is the set of keys the user left checked.
// Mandatory rows are always skipped — they cannot be toggled.
func diffServiceSelection(rows []serviceRow, kept []string) (toEnable, toDisable []string) {
	keptSet := make(map[string]bool, len(kept))
	for _, k := range kept {
		keptSet[k] = true
	}
	for _, row := range rows {
		if row.Mandatory {
			continue
		}
		if !row.Enabled && keptSet[row.Name] {
			toEnable = append(toEnable, row.Name)
		} else if row.Enabled && !keptSet[row.Name] {
			toDisable = append(toDisable, row.Name)
		}
	}
	return toEnable, toDisable
}

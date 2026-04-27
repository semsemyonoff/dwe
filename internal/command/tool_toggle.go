package command

// diffToolSelection computes which tools to enable and which to disable.
// rows is the current tool state; kept is the set of keys the user left checked.
// Tools have no mandatory concept, so all rows are eligible for toggling.
func diffToolSelection(rows []toolRow, kept []string) (toEnable, toDisable []string) {
	keptSet := make(map[string]bool, len(kept))
	for _, k := range kept {
		keptSet[k] = true
	}
	for _, row := range rows {
		if !row.Enabled && keptSet[row.Name] {
			toEnable = append(toEnable, row.Name)
		} else if row.Enabled && !keptSet[row.Name] {
			toDisable = append(toDisable, row.Name)
		}
	}
	return toEnable, toDisable
}

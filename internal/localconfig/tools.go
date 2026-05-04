package localconfig

import (
	"fmt"
)

// ToolSelection is the minimal tool state needed for diff logic.
type ToolSelection struct {
	Name    string
	Enabled bool
}

// DiffToolSelection computes which tools to enable and which to disable.
// selections is the current tool state; kept is the set of keys the user left checked.
// Tools have no mandatory concept, so all entries are eligible for toggling.
func DiffToolSelection(selections []ToolSelection, kept []string) (toEnable, toDisable []string) {
	keptSet := make(map[string]bool, len(kept))
	for _, k := range kept {
		keptSet[k] = true
	}
	for _, sel := range selections {
		if !sel.Enabled && keptSet[sel.Name] {
			toEnable = append(toEnable, sel.Name)
		} else if sel.Enabled && !keptSet[sel.Name] {
			toDisable = append(toDisable, sel.Name)
		}
	}
	return toEnable, toDisable
}

// ValidateToolToggle returns an error if the tool name is not in the known tools set.
func ValidateToolToggle(knownTools map[string]bool, name string) error {
	if !knownTools[name] {
		return fmt.Errorf("tool %q not found", name)
	}
	return nil
}

// ApplyToolTogglesToYAML validates and applies all tool toggles to the local
// config map in-memory. Either every change is applied or none are.
func ApplyToolTogglesToYAML(knownTools map[string]bool, local map[string]any, toEnable, toDisable []string) error {
	for _, name := range toEnable {
		if err := ValidateToolToggle(knownTools, name); err != nil {
			return err
		}
	}
	for _, name := range toDisable {
		if err := ValidateToolToggle(knownTools, name); err != nil {
			return err
		}
	}

	toolsMap, ok := local["tools"].(map[string]any)
	if !ok {
		toolsMap = make(map[string]any)
		local["tools"] = toolsMap
	}
	for _, name := range toEnable {
		SetLocalEntryEnabled(toolsMap, name, true)
	}
	for _, name := range toDisable {
		SetLocalEntryEnabled(toolsMap, name, false)
	}
	return nil
}

// SetToolEnabledInYAML sets a single tool's enabled state in the local config map.
func SetToolEnabledInYAML(knownTools map[string]bool, local map[string]any, name string, enabled bool) error {
	if err := ValidateToolToggle(knownTools, name); err != nil {
		return err
	}
	toolsMap, ok := local["tools"].(map[string]any)
	if !ok {
		toolsMap = make(map[string]any)
		local["tools"] = toolsMap
	}
	SetLocalEntryEnabled(toolsMap, name, enabled)
	return nil
}

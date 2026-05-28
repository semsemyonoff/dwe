package localconfig

import (
	"fmt"

	"devbox-cli/internal/core/project/config"
)

// ServiceSelection is the minimal service state needed for diff and validation logic.
type ServiceSelection struct {
	Name      string
	Enabled   bool
	Mandatory bool
}

// DiffServiceSelection computes which services to enable and which to disable.
// selections is the current service state; kept is the set of keys the user left checked.
// Mandatory entries are always skipped — they cannot be toggled.
func DiffServiceSelection(selections []ServiceSelection, kept []string) (toEnable, toDisable []string) {
	keptSet := make(map[string]bool, len(kept))
	for _, k := range kept {
		keptSet[k] = true
	}
	for _, sel := range selections {
		if sel.Mandatory {
			continue
		}
		if !sel.Enabled && keptSet[sel.Name] {
			toEnable = append(toEnable, sel.Name)
		} else if sel.Enabled && !keptSet[sel.Name] {
			toDisable = append(toDisable, sel.Name)
		}
	}
	return toEnable, toDisable
}

// ValidateServiceToggle returns an error if the service is unknown or mandatory.
func ValidateServiceToggle(cfg *config.DevboxConfig, name string) error {
	svc, ok := cfg.Services[name]
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	if svc.Required {
		return fmt.Errorf("service %q is required and cannot be toggled", name)
	}
	return nil
}

// ApplyServiceTogglesToYAML validates and applies all service toggles to the
// local config map in-memory. Either every change is applied or none are.
func ApplyServiceTogglesToYAML(cfg *config.DevboxConfig, local map[string]any, toEnable, toDisable []string) error {
	for _, name := range toEnable {
		if err := ValidateServiceToggle(cfg, name); err != nil {
			return err
		}
	}
	for _, name := range toDisable {
		if err := ValidateServiceToggle(cfg, name); err != nil {
			return err
		}
	}

	svcMap, ok := local["services"].(map[string]any)
	if !ok {
		svcMap = make(map[string]any)
		local["services"] = svcMap
	}
	for _, name := range toEnable {
		SetLocalEntryEnabled(svcMap, name, true)
	}
	for _, name := range toDisable {
		SetLocalEntryEnabled(svcMap, name, false)
	}
	return nil
}

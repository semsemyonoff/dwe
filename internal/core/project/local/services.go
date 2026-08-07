package local

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
func ValidateServiceToggle(cfg *config.DweConfig, name string) error {
	svc, ok := cfg.Services[name]
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	if svc.Required {
		return fmt.Errorf("service %q is required and cannot be toggled", name)
	}
	return nil
}

// ServiceTogglesOverlay validates all service toggles and builds the minimal
// local.yml overlay encoding them: {services: {<name>: {enabled: <bool>}}}.
// Either every name validates and an overlay is returned, or the first
// validation error is surfaced and no overlay is produced (all-or-nothing).
//
// Callers MUST apply the overlay onto a LOADED document node via
// ApplyOverlayToNode, or the developer's comments/formatting and any other
// local.yml keys are dropped. Returns a nil overlay only when there is nothing
// to toggle (both slices empty); callers treat a nil/empty overlay as a no-op.
func ServiceTogglesOverlay(cfg *config.DweConfig, toEnable, toDisable []string) (map[string]any, error) {
	for _, name := range toEnable {
		if err := ValidateServiceToggle(cfg, name); err != nil {
			return nil, err
		}
	}
	for _, name := range toDisable {
		if err := ValidateServiceToggle(cfg, name); err != nil {
			return nil, err
		}
	}
	if len(toEnable) == 0 && len(toDisable) == 0 {
		return nil, nil
	}

	services := make(map[string]any, len(toEnable)+len(toDisable))
	for _, name := range toEnable {
		services[name] = map[string]any{"enabled": true}
	}
	for _, name := range toDisable {
		services[name] = map[string]any{"enabled": false}
	}
	return map[string]any{"services": services}, nil
}

// Package spec implements plan-time validation for files_gate directives.
package spec

import (
	"fmt"

	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/usercommands/registry"
)

// Issue represents a validation issue with a files_gate directive.
type Issue struct {
	Message string
}

// Validate checks a files_gate directive at plan time.
// ref is the step reference (type, cmd, with); fg is the files_gate directive.
// Returns a list of validation issues; empty list means valid.
func Validate(reg *registry.Registry, ref filesgate.StepRef, fg *filesgate.FilesGate) []Issue {
	var issues []Issue

	if fg == nil {
		return issues
	}

	// State must be set.
	if fg.State == "" {
		issues = append(issues, Issue{Message: "files_gate.state is required"})
		return issues // Fail-fast; can't proceed without state.
	}

	// Command defaults to step.cmd when step.type == "command".
	cmd := fg.Command
	if cmd == "" {
		if ref.Type == "command" {
			cmd = ref.Cmd
		} else {
			issues = append(issues, Issue{Message: "files_gate.command is required when step type is not 'command'"})
			return issues // Fail-fast.
		}
	}

	if reg == nil {
		issues = append(issues, Issue{Message: "command registry not available; skipping files_gate validation"})
		return issues
	}

	// Command must exist in registry.
	def, err := reg.Get(cmd)
	if err != nil {
		issues = append(issues, Issue{Message: fmt.Sprintf("files_gate references unknown command %q", cmd)})
		return issues // Fail-fast; can't proceed without the command.
	}

	// Command must have files: block.
	if len(def.Files) == 0 {
		issues = append(issues, Issue{Message: fmt.Sprintf("command %q has no files: block", cmd)})
		return issues // Fail-fast.
	}

	// Expand require spec.
	_, err = ResolveRequireIDs(fg.Require, def.Files)
	if err != nil {
		issues = append(issues, Issue{Message: err.Error()})
		return issues // Fail-fast.
	}

	// Validate that with (plus defaults) covers all required params.
	// fg.With takes precedence; fall back to ref.With (inherited from the step) when gate.with is unset.
	effectiveWith := ref.With
	if fg.With != nil {
		effectiveWith = fg.With
	}
	for paramName, paramDef := range def.Params {
		if !paramDef.Required {
			continue
		}
		// Check if param is provided in with.
		withValue, withProvided := effectiveWith[paramName]
		if withProvided {
			// Coerce to string for empty check (handles map[string]any).
			withStr := fmt.Sprintf("%v", withValue)
			if withStr != "" && withStr != "<nil>" {
				continue // Param is provided and non-empty.
			}
		}
		// Check if param has a default or default_from.
		if paramDef.Default != "" || paramDef.DefaultFrom != "" {
			continue // Param has a fallback.
		}
		// Required param is not satisfied.
		issues = append(issues, Issue{Message: fmt.Sprintf("required parameter %q must be provided in files_gate.with or have a default in the command definition", paramName)})
	}

	return issues
}

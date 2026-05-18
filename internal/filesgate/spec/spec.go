// Package spec implements plan-time validation for files_gate directives.
package spec

import (
	"fmt"

	"devbox-cli/internal/config"
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
func Validate(cfg *config.DevboxConfig, reg *registry.Registry, ref filesgate.StepRef, fg *filesgate.FilesGate) []Issue {
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
		issues = append(issues, Issue{Message: fmt.Sprintf("files_gate.require: %v", err)})
		return issues // Fail-fast.
	}

	// TODO: Validate that with (plus defaults) covers all required params.
	// This requires param resolution validation using the same logic as runtime.

	return issues
}

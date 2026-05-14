// Package commands provides command file validation.
package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/usercommands/loader"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/validate"
)

// Validator validates command files for syntax and cross-references.
type Validator struct{}

// ID returns the validator's unique ID within its domain.
func (v *Validator) ID() string {
	return "commands"
}

// Domain returns the domain this validator belongs to.
func (v *Validator) Domain() string {
	return "commands"
}

// Run validates all command files and returns a list of diagnostics.
func (v *Validator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	// Discover command files
	baseDir := filepath.Join(ctx.ProjectRoot, "devbox", "commands")
	paths, err := loader.DiscoverCommandFiles(baseDir)
	if err != nil {
		// If the commands directory doesn't exist, that's OK; just no commands
		// Any other error is a problem
		if !errors.Is(err, os.ErrNotExist) {
			return []validate.Diagnostic{
				{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   "commands",
					File:     "devbox/commands",
					Line:     0,
					Message:  fmt.Sprintf("failed to discover command files: %v", err),
					Hint:     "check that devbox/commands directory is readable",
				},
			}
		}
		// Directory doesn't exist; emit OK diagnostic
		return []validate.Diagnostic{
			{
				Severity: validate.SeverityOK,
				Domain:   "commands",
				Target:   "commands",
				File:     "",
				Line:     0,
				Message:  "no command files",
				Hint:     "",
			},
		}
	}

	// Parse all command files
	var parsedFiles []*model.CommandFile
	for _, path := range paths {
		cf, err := loader.LoadCommandFile(path, baseDir)
		if err != nil {
			relFile, _ := filepath.Rel(ctx.ProjectRoot, path)
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   "commands",
				File:     relFile,
				Line:     0,
				Message:  fmt.Sprintf("failed to parse: %v", err),
				Hint:     "check YAML syntax and structure",
			})
			continue
		}
		parsedFiles = append(parsedFiles, cf)
	}

	// Build registry from parsed files
	reg, err := registry.BuildRegistryFromParsed(parsedFiles)
	if err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "commands",
			Target:   "commands",
			File:     "",
			Line:     0,
			Message:  fmt.Sprintf("duplicate command IDs: %v", err),
			Hint:     "check that each command has a unique ID across all command files",
		})
		return diags
	}

	// Run cross-reference validation
	issues := reg.Diagnostics()
	for _, issue := range issues {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "commands",
			Target:   fmt.Sprintf("commands:%s", issue.CommandID),
			File:     "",
			Line:     0,
			Message:  fmt.Sprintf("step[%d]: %s", issue.StepIndex, issue.Message),
			Hint:     "check that workflow steps reference valid command IDs",
		})
	}

	// If no errors, emit OK diagnostic
	if len(diags) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "commands",
			Target:   "commands",
			File:     "",
			Line:     0,
			Message:  fmt.Sprintf("%d command files valid", len(parsedFiles)),
			Hint:     "",
		})
	}

	return diags
}

// All returns all command validators.
func All() []validate.Validator {
	return []validate.Validator{
		&Validator{},
	}
}

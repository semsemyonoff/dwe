// Package commands provides command file validation.
package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
		// Directory doesn't exist; emit Info diagnostic (optional directory)
		return []validate.Diagnostic{
			{
				Severity: validate.SeverityInfo,
				Domain:   "commands",
				Target:   "commands",
				File:     "",
				Line:     0,
				Message:  "no command files",
				Hint:     "",
			},
		}
	}

	// Parse all command files WITHOUT running cf.Validate() — we want to surface
	// categorised diagnostics for known semantic errors below before falling
	// back to the raw Validate() message for anything else.
	var parsedFiles []*model.CommandFile
	for _, path := range paths {
		cf, err := loader.ParseCommandFile(path, baseDir)
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

	// Emit categorised structural diagnostics for workflow steps (parallel rules)
	// before the cross-reference pass. Track which commands were categorised so
	// that the cmd.Validate() fallback below only covers commands we did NOT
	// specifically categorise — preventing unrelated semantic errors in the same
	// file from being silently swallowed.
	//
	// Key: "<filePath>/<commandName>"; value: true when categorised diags fired.
	categorisedCmds := make(map[string]bool)
	for _, cf := range parsedFiles {
		relFile, _ := filepath.Rel(ctx.ProjectRoot, cf.FilePath)
		for _, name := range sortedCommandNames(cf) {
			cmd := cf.Commands[name]
			if cmd.Type != model.CommandTypeWorkflow {
				continue
			}
			structural := workflowStructuralDiagnostics(cmd, relFile)
			if len(structural) > 0 {
				categorisedCmds[cf.FilePath+"/"+name] = true
				diags = append(diags, structural...)
			}
		}
	}

	// Fallback: surface cmd.Validate() failures that are not already covered by
	// the categorised structural diagnostics above. Running per-command (rather
	// than per-file cf.Validate()) ensures a categorised error in one command
	// does not hide unrelated semantic errors in other commands of the same file.
	//
	// For commands that DID produce categorised parallel-structural diagnostics,
	// we still run cmd.Validate() — it may surface unrelated field violations
	// (e.g. workdir: or cmd: set on a workflow) that the structural walker does
	// not cover. We only suppress the fallback when the error is step-level
	// (the error string contains ": step["), meaning it duplicates something the
	// structural diagnostics already reported more clearly.
	for _, cf := range parsedFiles {
		relFile, _ := filepath.Rel(ctx.ProjectRoot, cf.FilePath)
		for _, name := range sortedCommandNames(cf) {
			if strings.TrimSpace(name) == "" {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   "commands",
					File:     relFile,
					Line:     0,
					Message:  "command name must not be empty or whitespace",
					Hint:     "give every command a non-empty YAML key",
				})
				continue
			}
			cmd := cf.Commands[name]
			if err := cmd.Validate(); err != nil {
				// Skip step-level errors already covered by structural parallel
				// diagnostics (e.g. nested-parallel, confirm-in-parallel). Non-step
				// errors (workdir, cmd, service, etc.) are surfaced regardless.
				if categorisedCmds[cf.FilePath+"/"+name] && strings.Contains(err.Error(), ": step[") {
					continue
				}
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   fmt.Sprintf("commands:%s", cmd.ID),
					File:     relFile,
					Line:     0,
					Message:  err.Error(),
					Hint:     "fix the reported field combination",
				})
			}
		}
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
			Message:  fmt.Sprintf("%s: %s", issue.Path, issue.Message),
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

func sortedCommandNames(cf *model.CommandFile) []string {
	names := make([]string, 0, len(cf.Commands))
	for name := range cf.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// workflowStructuralDiagnostics emits categorised diagnostics for known
// workflow-step rule violations: nested parallel, confirm inside parallel,
// with on a parallel container, and parallel.steps with fewer than two
// sub-steps. It walks recursively so violations at any depth are reported
// with a path-qualified location.
func workflowStructuralDiagnostics(cmd model.CommandDef, relFile string) []validate.Diagnostic {
	var out []validate.Diagnostic
	target := fmt.Sprintf("commands:%s", cmd.ID)
	registry.WalkWorkflowSteps(cmd.Steps, "step", func(path string, step model.WorkflowStep) {
		if step.Parallel == nil {
			return
		}
		if strings.Contains(path, ".parallel.steps[") {
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("%s: nested parallel is not supported in v1", path),
				Hint:     "flatten the parallel group or wrap the inner work in a separate workflow command",
			})
			return
		}
		if len(step.With) > 0 {
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("%s: with may not be combined with parallel", path),
				Hint:     "move `with:` onto each sub-step instead of the parallel container",
			})
		}
		if len(step.Parallel.Steps) < 2 {
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("%s.parallel.steps: must contain at least 2 sub-steps", path),
				Hint:     "remove the parallel block or add another sub-step",
			})
		}
		for j, sub := range step.Parallel.Steps {
			subPath := fmt.Sprintf("%s.parallel.steps[%d]", path, j)
			if sub.Confirm != "" {
				out = append(out, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   target,
					File:     relFile,
					Message:  fmt.Sprintf("%s: confirm is not allowed inside a parallel group", subPath),
					Hint:     "move confirm steps outside the parallel block",
				})
			}
			if sub.Parallel == nil && sub.Confirm == "" && sub.Command == "" {
				out = append(out, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   target,
					File:     relFile,
					Message:  fmt.Sprintf("%s: command is required", subPath),
					Hint:     "set `command:` to a registered command ID",
				})
			}
		}
	})
	return out
}

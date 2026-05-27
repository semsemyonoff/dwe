// Package commands provides command file validation.
package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"devbox-cli/internal/config"
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
	// categorisedDaemonFields tracks per-field daemon suppression for the
	// model-error fallback below. Keys are "<filePath>/<commandName>"; values
	// are the set of daemon field markers (service, container_template,
	// on_already_running, stop_timeout, controls) already surfaced richly.
	categorisedDaemonFields := make(map[string]map[string]bool)
	for _, cf := range parsedFiles {
		relFile, _ := filepath.Rel(ctx.ProjectRoot, cf.FilePath)
		for _, name := range sortedCommandNames(cf) {
			cmd := cf.Commands[name]
			switch cmd.Type {
			case model.CommandTypeWorkflow:
				structural := workflowStructuralDiagnostics(cmd, relFile)
				if len(structural) > 0 {
					categorisedCmds[cf.FilePath+"/"+name] = true
					diags = append(diags, structural...)
				}
			case model.CommandTypeDaemon:
				dDiags, fields := daemonStructuralDiagnostics(cmd, relFile)
				if len(dDiags) > 0 {
					diags = append(diags, dDiags...)
				}
				if len(fields) > 0 {
					categorisedDaemonFields[cf.FilePath+"/"+name] = fields
				}
			}

			// Emit param diagnostics.
			pDiags := paramStructuralDiagnostics(cmd, relFile, ctx.Cfg)
			if len(pDiags) > 0 {
				categorisedCmds[cf.FilePath+"/"+name] = true
				diags = append(diags, pDiags...)
			}

			diags = append(diags, notifyDaemonDiagnostics(cmd, relFile)...)
		}
	}

	// Fallback: surface cmd.Validate() failures that are not already covered by
	// the categorised structural diagnostics above. Running per-command (rather
	// than per-file cf.Validate()) ensures a categorised error in one command
	// does not hide unrelated semantic errors in other commands of the same file.
	//
	// For commands that DID produce categorised structural diagnostics, we still
	// run cmd.Validate() — it may surface unrelated field violations that the
	// structural walkers do not cover. We suppress fallback errors that are
	// step-level (": step["), param-level ("params."), or already categorized
	// daemon field errors.
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
				// Daemon path: unwrap errors.Join and drop only constituents whose
				// matching field has been categorised. Non-categorised constituents
				// (e.g. `cmd is not valid for type=daemon`) still surface.
				if cmd.Type == model.CommandTypeDaemon {
					fields := categorisedDaemonFields[cf.FilePath+"/"+name]
					for _, e := range unwrapJoined(err) {
						if isSuppressedDaemonErr(e, fields) {
							continue
						}
						diags = append(diags, validate.Diagnostic{
							Severity: validate.SeverityError,
							Domain:   "commands",
							Target:   fmt.Sprintf("commands:%s", cmd.ID),
							File:     relFile,
							Message:  e.Error(),
							Hint:     "fix the reported daemon field",
						})
					}
					continue
				}
				// Skip errors already covered by categorised structural diagnostics:
				// - step-level errors from workflow validation
				// - param-level errors from param validation
				if categorisedCmds[cf.FilePath+"/"+name] {
					errMsg := err.Error()
					if strings.Contains(errMsg, ": step[") || strings.Contains(errMsg, "params.") {
						continue
					}
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

	// Reserved-id check: warn when a command's computed top-level ID shadows a
	// reserved `devbox commands` subcommand (e.g. "list"). Group-qualified IDs
	// (e.g. "services.list") are not reserved. Daemon source commands are
	// dropped from byID during registry expansion and are never runnable, so
	// their IDs do not create real conflicts and are skipped here.
	for _, cf := range parsedFiles {
		relFile, _ := filepath.Rel(ctx.ProjectRoot, cf.FilePath)
		for _, name := range sortedCommandNames(cf) {
			cmd := cf.Commands[name]
			if cmd.Type == model.CommandTypeDaemon {
				continue
			}
			if !loader.IsReservedTopLevelID(cmd.ID) {
				continue
			}
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "commands",
				Target:   fmt.Sprintf("commands:%s", cmd.ID),
				File:     relFile,
				Line:     0,
				Message: fmt.Sprintf(
					"command id %q conflicts with the reserved subcommand \"devbox commands %s\".\nThe command will only be reachable from the interactive browser (devbox commands).",
					cmd.ID, cmd.ID),
				Hint: "rename the command or move it under a group (e.g. \"tools." + cmd.ID + "\")",
			})
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

	// Notify on direct parallel sub-steps (registry-aware, info-level).
	diags = append(diags, notifyParallelSubStepDiagnostics(reg)...)

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

// paramStructuralDiagnostics emits categorized diagnostics for param validation violations.
// It checks widget/options rules and validates default/default_from values against
// resolved options (if cfg is available). Returns diagnostics or nil if no violations.
func paramStructuralDiagnostics(cmd model.CommandDef, relFile string, cfg *config.DevboxConfig) []validate.Diagnostic {
	var out []validate.Diagnostic
	target := fmt.Sprintf("commands:%s", cmd.ID)

	for pname, pdef := range cmd.Params {
		paramTarget := fmt.Sprintf("%s:params.%s", target, pname)

		// Check Widget is a valid enum value if set.
		if pdef.Widget != "" {
			switch pdef.Widget {
			case model.WidgetInput, model.WidgetSelect, model.WidgetMultiselect, model.WidgetConfirm:
				// Valid.
			default:
				out = append(out, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   paramTarget,
					File:     relFile,
					Message:  fmt.Sprintf("params.%s.widget: must be one of input, select, multiselect, confirm (got %q)", pname, pdef.Widget),
					Hint:     "fix the widget value or remove it to use automatic inference",
				})
				continue
			}
		}

		effective := pdef.EffectiveWidget()

		// Widget = select/multiselect requires non-empty options.
		if effective == model.WidgetSelect || effective == model.WidgetMultiselect {
			if pdef.Options == nil || (len(pdef.Options.Static) == 0 && pdef.Options.From == "") {
				out = append(out, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   paramTarget,
					File:     relFile,
					Message:  fmt.Sprintf("params.%s.widget: widget %s requires non-empty options", pname, effective),
					Hint:     "add a static list or a reference to defaults.yml/local.yml via options: ${...}",
				})
				continue
			}
		}

		// Widget = input/confirm must have empty options.
		if effective == model.WidgetInput || effective == model.WidgetConfirm {
			if pdef.Options != nil && (len(pdef.Options.Static) > 0 || pdef.Options.From != "") {
				out = append(out, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   paramTarget,
					File:     relFile,
					Message:  fmt.Sprintf("params.%s.widget: widget %s does not accept options", pname, effective),
					Hint:     "remove the options field or change widget to select/multiselect",
				})
				continue
			}
		}

		// Pattern + Options is not allowed.
		if pdef.Pattern != "" && pdef.Options != nil && (len(pdef.Options.Static) > 0 || pdef.Options.From != "") {
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   paramTarget,
				File:     relFile,
				Message:  fmt.Sprintf("params.%s: pattern and options are mutually exclusive", pname),
				Hint:     "use either pattern (regex validation) or options (choice list), not both",
			})
			continue
		}

		// Separator only valid on multiselect.
		if pdef.Separator != "" && effective != model.WidgetMultiselect {
			out = append(out, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "commands",
				Target:   paramTarget,
				File:     relFile,
				Message:  fmt.Sprintf("params.%s.separator: only valid for multiselect widgets", pname),
				Hint:     "remove separator or change widget to multiselect",
			})
			continue
		}

		// Static options: check for duplicate values.
		if pdef.Options != nil && len(pdef.Options.Static) > 0 {
			seen := make(map[string]bool)
			for _, item := range pdef.Options.Static {
				if seen[item.Value] {
					out = append(out, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "commands",
						Target:   paramTarget,
						File:     relFile,
						Message:  fmt.Sprintf("params.%s.options: duplicate option value %q", pname, item.Value),
						Hint:     "remove the duplicate entry",
					})
					break
				}
				seen[item.Value] = true
			}
		}

		// Validate default literal against static options.
		if pdef.Default != "" && pdef.Options != nil && len(pdef.Options.Static) > 0 {
			found := false
			for _, item := range pdef.Options.Static {
				if item.Value == pdef.Default {
					found = true
					break
				}
			}
			if !found {
				out = append(out, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "commands",
					Target:   paramTarget,
					File:     relFile,
					Message:  fmt.Sprintf("params.%s.default: %q not found in static options", pname, pdef.Default),
					Hint:     "fix the default value or add it to options",
				})
			}
		}

		// Validate default_from + default against resolved options (only if cfg available).
		// Only check for select/multiselect since other widgets can't have resolved options.
		if cfg != nil && (pdef.DefaultFrom != "" || pdef.Default != "") &&
			(effective == model.WidgetSelect || effective == model.WidgetMultiselect) &&
			pdef.Options != nil && pdef.Options.From != "" {

			// Determine the effective default value and its source label.
			effectiveDefault := pdef.Default
			defaultSource := "default"
			canCheck := true
			if pdef.DefaultFrom != "" {
				// Resolve the dot-path to its actual value; skip the check if unresolvable.
				rawVal, ok := config.ResolvePath(cfg.Raw, pdef.DefaultFrom)
				if !ok || rawVal == nil {
					canCheck = false
				} else {
					effectiveDefault = fmt.Sprint(rawVal)
					defaultSource = fmt.Sprintf("default_from %q", pdef.DefaultFrom)
				}
			}

			if canCheck && effectiveDefault != "" {
				// Try to resolve the options from the config.
				resolved, ok := config.ResolvePath(cfg.Raw, pdef.Options.From)
				if ok && resolved != nil {
					optionValues := extractOptionValues(resolved)
					if len(optionValues) > 0 {
						if !slices.Contains(optionValues, effectiveDefault) {
							out = append(out, validate.Diagnostic{
								Severity: validate.SeverityError,
								Domain:   "commands",
								Target:   paramTarget,
								File:     relFile,
								Message:  fmt.Sprintf("params.%s: %s %q not found in resolved options ${%s}", pname, defaultSource, effectiveDefault, pdef.Options.From),
								Hint:     "check that defaults.yml/local.yml provides this value in the options list",
							})
						}
					}
				}
			}
		}
	}

	return out
}

// extractOptionValues converts a resolved options value to a list of string values.
// Handles: []string, []any of scalars, []any of maps with "value" key, or map[string]any.
func extractOptionValues(v any) []string {
	var out []string

	switch typed := v.(type) {
	case []string:
		out = typed
	case []any:
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			} else if m, ok := item.(map[string]any); ok {
				if val, ok := m["value"]; ok {
					if s, ok := val.(string); ok {
						out = append(out, s)
					}
				}
			}
		}
	case map[string]any:
		// Extract keys as option values.
		for key := range typed {
			out = append(out, key)
		}
		sort.Strings(out)
	}

	return out
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

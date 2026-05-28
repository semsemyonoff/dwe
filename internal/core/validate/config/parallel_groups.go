package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/registry"
	"devbox-cli/internal/core/validate"
)

// parallelGroupsValidator emits diagnostics for parallel-group misconfigurations
// in deploy.yml (project + per-service). The matching lifecycle and reset
// validators below share the same walker.
type parallelGroupsValidator struct{}

func (v *parallelGroupsValidator) ID() string     { return "deploy" }
func (v *parallelGroupsValidator) Domain() string { return "config" }

func (v *parallelGroupsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}
	reg, _ := ctx.CommandRegistry.(*registry.Registry)

	var diags []validate.Diagnostic
	deployPath := filepath.Join(ctx.ProjectRoot, "devbox", "deploy.yml")
	diags = append(diags, validateParallelPhases(reg, ctx.Cfg.Deploy.Phases, "config.deploy", relPath(ctx.ProjectRoot, deployPath))...)

	if len(ctx.Cfg.Services) > 0 {
		svcDeploys, err := config.LoadServiceDeployConfigs(ctx.ProjectRoot, ctx.Cfg.Services)
		if err == nil {
			for svcName, svcDeploy := range svcDeploys {
				svcPath := filepath.Join(ctx.ProjectRoot, "devbox", "deploy", svcName+".yml")
				target := fmt.Sprintf("config.service-deploy[%s]", svcName)
				diags = append(diags, validateParallelPhases(reg, svcDeploy.Phases, target, relPath(ctx.ProjectRoot, svcPath))...)
			}
		}
	}

	return diags
}

type lifecycleParallelGroupsValidator struct{}

func (v *lifecycleParallelGroupsValidator) ID() string     { return "lifecycle" }
func (v *lifecycleParallelGroupsValidator) Domain() string { return "config" }

func (v *lifecycleParallelGroupsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}
	reg, _ := ctx.CommandRegistry.(*registry.Registry)
	lifecyclePath := filepath.Join(ctx.ProjectRoot, "devbox", "lifecycle.yml")
	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		return nil
	}

	var diags []validate.Diagnostic
	file := relPath(ctx.ProjectRoot, lifecyclePath)
	if lifecycleCfg.Run != nil {
		diags = append(diags, validateParallelPhases(reg, lifecycleCfg.Run.Phases, "config.lifecycle.run", file)...)
	}
	if lifecycleCfg.Stop != nil {
		diags = append(diags, validateParallelPhases(reg, lifecycleCfg.Stop.Phases, "config.lifecycle.stop", file)...)
	}
	return diags
}

type resetParallelGroupsValidator struct{}

func (v *resetParallelGroupsValidator) ID() string     { return "reset" }
func (v *resetParallelGroupsValidator) Domain() string { return "config" }

func (v *resetParallelGroupsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}
	reg, _ := ctx.CommandRegistry.(*registry.Registry)
	resetPath := filepath.Join(ctx.ProjectRoot, "devbox", "reset.yml")
	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		return nil
	}
	return validateParallelPhases(reg, resetCfg.Phases, "config.reset", relPath(ctx.ProjectRoot, resetPath))
}

// validateParallelPhases walks the given phases and emits one diagnostic per
// parallel-group misconfiguration. Each rule mirrors a sentinel in
// internal/core/execution/pipeline/resolve.go; users see structured errors before they hit
// `devbox deploy` / `devbox reset`.
func validateParallelPhases(reg *registry.Registry, phases []config.DeployPhase, baseTarget, file string) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for phaseIdx, phase := range phases {
		// Collect name counts across leaf steps + group steps + every parallel
		// sub-step in this phase. The group step's own name must be counted so
		// that a leaf "foo" + group named "foo", or a group named "foo" with a
		// sub-step also named "foo", are detected as duplicates.
		nameCounts := map[string]int{}
		for _, step := range phase.Steps {
			if step.Parallel != nil {
				if step.Name != "" {
					nameCounts[step.Name]++
				}
				for _, sub := range step.Parallel.Steps {
					if sub.Name != "" {
						nameCounts[sub.Name]++
					}
				}
				continue
			}
			if step.Name != "" {
				nameCounts[step.Name]++
			}
		}
		reportedDup := map[string]bool{}

		for stepIdx, step := range phase.Steps {
			if step.Parallel == nil {
				continue
			}
			target := fmt.Sprintf("%s.phases[%d].steps[%d].parallel", baseTarget, phaseIdx, stepIdx)

			// Leaf-only directives co-occurring with parallel. Defensive — the
			// loader's UnmarshalYAML normally rejects this, but a config loaded
			// outside the loader (or a future loader bypass) would slip through.
			if conflicts := groupLeafConflicts(step); len(conflicts) > 0 {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   target,
					File:     file,
					Message:  fmt.Sprintf("parallel group cannot co-occur with leaf-only directive(s): %s", strings.Join(conflicts, ", ")),
					Hint:     "move leaf-only directives into the sub-steps; the group itself is a container only",
				})
			}

			// Group size: < 2 is invalid.
			if len(step.Parallel.Steps) < 2 {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   target,
					File:     file,
					Message:  fmt.Sprintf("parallel group must declare at least two sub-steps (got %d)", len(step.Parallel.Steps)),
					Hint:     "use a leaf step if only one item",
				})
			}

			for subIdx, sub := range step.Parallel.Steps {
				subTarget := fmt.Sprintf("%s.steps[%d]", target, subIdx)

				if sub.Parallel != nil {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   subTarget,
						File:     file,
						Message:  "nested parallel groups are not supported",
						Hint:     "flat parallel groups only in v1",
					})
				}

				if sub.Name == "" {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   subTarget,
						File:     file,
						Message:  "parallel sub-step must have a name",
						Hint:     "add a `name:` to identify this sub-step in logs and journal",
					})
				} else if nameCounts[sub.Name] > 1 && !reportedDup[sub.Name] {
					reportedDup[sub.Name] = true
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   subTarget,
						File:     file,
						Message:  fmt.Sprintf("duplicate step name in phase: %q", sub.Name),
						Hint:     "rename to a unique value within the phase",
					})
				}

				skipConfirm := step.SkipConfirm || sub.SkipConfirm
				if !skipConfirm && sub.Parallel == nil && isInteractiveSub(reg, sub) {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   subTarget,
						File:     file,
						Message:  "interactive prompt in parallel sub-step requires skip_confirm: true",
						Hint:     "set skip_confirm: true or restructure",
					})
				}

				if reg != nil && sub.Type == "command" && sub.Cmd != "" {
					if def, err := reg.Get(sub.Cmd); err == nil && def != nil && def.Type == model.CommandTypeServiceRun && hasTTYComposeArg(def.ComposeArgs) {
						diags = append(diags, validate.Diagnostic{
							Severity: validate.SeverityWarning,
							Domain:   "config",
							Target:   subTarget,
							File:     file,
							Message:  "service_run sub-step uses TTY-allocating compose args",
							Hint:     "TTY allocation is not available in parallel sub-steps; the child may exit non-zero with 'cannot allocate tty'",
						})
					}
				}
			}
		}

		// Second pass: emit diagnostics for leaf steps whose names collide with
		// another leaf step or with a parallel sub-step in the same phase. These
		// collisions are already present in nameCounts but skipped by the first
		// loop (which continues past non-parallel steps).
		for stepIdx, step := range phase.Steps {
			if step.Parallel != nil {
				continue
			}
			if step.Name == "" || nameCounts[step.Name] <= 1 || reportedDup[step.Name] {
				continue
			}
			reportedDup[step.Name] = true
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   fmt.Sprintf("%s.phases[%d].steps[%d]", baseTarget, phaseIdx, stepIdx),
				File:     file,
				Message:  fmt.Sprintf("duplicate step name in phase: %q", step.Name),
				Hint:     "rename to a unique value within the phase",
			})
		}
	}
	return diags
}

// groupLeafConflicts returns the set of leaf-only fields that are populated on
// a parallel-group step in violation of the group/leaf split.
func groupLeafConflicts(step config.DeployStep) []string {
	var out []string
	if step.Type != "" {
		out = append(out, "type")
	}
	if step.Cmd != "" {
		out = append(out, "cmd")
	}
	if len(step.With) > 0 {
		out = append(out, "with")
	}
	if step.Check != nil {
		out = append(out, "check")
	}
	if step.FilesGate != nil {
		out = append(out, "files_gate")
	}
	if step.ContinueOnError {
		out = append(out, "continue_on_error")
	}
	return out
}

// isInteractiveSub returns true if the sub-step would require an interactive
// prompt at runtime. Mirrors pipeline.checkInteractive but unexported there.
// Registry-nil tolerated: command-target lookups are skipped.
func isInteractiveSub(reg *registry.Registry, sub config.DeployStep) bool {
	if sub.Type == "builtin" && sub.Cmd == "confirm" {
		return true
	}
	if sub.Type != "command" || reg == nil || sub.Cmd == "" {
		return false
	}
	return commandTargetIsInteractive(reg, sub.Cmd, map[string]bool{})
}

func commandTargetIsInteractive(reg *registry.Registry, cmdID string, visited map[string]bool) bool {
	if visited[cmdID] {
		return false
	}
	visited[cmdID] = true
	def, err := reg.Get(cmdID)
	if err != nil || def == nil {
		return false
	}
	if def.Confirmation {
		return true
	}
	if def.Type != model.CommandTypeWorkflow {
		return false
	}
	for _, ws := range def.Steps {
		if ws.Confirm != "" {
			return true
		}
		if ws.Command != "" && commandTargetIsInteractive(reg, ws.Command, visited) {
			return true
		}
	}
	return false
}

// hasTTYComposeArg detects the docker compose --tty / -t / -it / -ti flags
// which cannot work in a parallel sub-step (no PTY is allocated).
func hasTTYComposeArg(args []string) bool {
	for _, a := range args {
		switch a {
		case "-t", "--tty", "-it", "-ti":
			return true
		}
	}
	return false
}

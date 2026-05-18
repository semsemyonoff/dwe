package pipeline

import (
	"fmt"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/filesgate/spec"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/registry"
)

// ResolvePhaseSteps resolves steps for a single phase, evaluating when conditions.
// service is empty for orchestrator phases.
//
// Phase-level when is evaluated first:
//   - Go template: evaluated at plan time; entire phase is excluded when false.
//   - Runtime condition: propagated to each step that does not already carry its
//     own runtime when condition. The phase condition is stored in PhaseWhen and
//     evaluated before each such step at execution time.
//
// reg (registry) is used to validate files_gate directives if present.
// When reg is nil, files_gate validation is skipped; this is tolerated for
// internal-tool and test callers. Runtime callers (deploy run, reset run, etc.)
// MUST pass a non-nil registry.
func ResolvePhaseSteps(cfg *config.DevboxConfig, reg *registry.Registry, phase config.DeployPhase, service string) ([]ResolvedStep, error) {
	var phaseRuntimeWhen *condition.Condition
	if phase.When != nil {
		if phase.When.IsRuntime() {
			phaseRuntimeWhen = phase.When
		} else if phase.When.Type == condition.TypeTemplate {
			ok, err := tpl.EvalCondition(phase.When.Expr, cfg)
			if err != nil {
				prefix := phase.Name
				if service != "" {
					prefix = service + "/" + prefix
				}
				return nil, fmt.Errorf("evaluating when condition for phase %s: %w", prefix, err)
			}
			if !ok {
				return nil, nil
			}
		}
	}

	var result []ResolvedStep
	for _, step := range phase.Steps {
		var stepRuntimeWhen *condition.Condition
		if step.When != nil {
			if step.When.IsRuntime() {
				stepRuntimeWhen = step.When
				if step.Type == "builtin" {
					if err := builtin.Validate(step.Cmd, step.With); err != nil {
						prefix := phase.Name + "/" + step.Name
						if service != "" {
							prefix = service + "/" + prefix
						}
						return nil, fmt.Errorf("step %s: invalid builtin: %w", prefix, err)
					}
				}
				// Validate files_gate if present and registry is available.
				if step.FilesGate != nil && reg != nil {
					ref := filesgate.StepRef{Type: step.Type, Cmd: step.Cmd, With: step.With}
					issues := spec.Validate(reg, ref, step.FilesGate)
					if len(issues) > 0 {
						prefix := phase.Name + "/" + step.Name
						if service != "" {
							prefix = service + "/" + prefix
						}
						return nil, fmt.Errorf("step %s: %s", prefix, issues[0].Message)
					}
				}
				result = append(result, ResolvedStep{Phase: phase, Step: step, Service: service, RuntimeWhen: stepRuntimeWhen, PhaseWhen: phaseRuntimeWhen, FilesGate: step.FilesGate})
				continue
			} else if step.When.Type == condition.TypeTemplate {
				ok, err := tpl.EvalCondition(step.When.Expr, cfg)
				if err != nil {
					prefix := phase.Name + "/" + step.Name
					if service != "" {
						prefix = service + "/" + prefix
					}
					return nil, fmt.Errorf("evaluating when condition for step %s: %w", prefix, err)
				}
				if !ok {
					continue
				}
			}
		}
		if step.Type == "builtin" {
			if err := builtin.Validate(step.Cmd, step.With); err != nil {
				prefix := phase.Name + "/" + step.Name
				if service != "" {
					prefix = service + "/" + prefix
				}
				return nil, fmt.Errorf("step %s: invalid builtin: %w", prefix, err)
			}
		}
		// Validate files_gate if present and registry is available.
		if step.FilesGate != nil && reg != nil {
			ref := filesgate.StepRef{Type: step.Type, Cmd: step.Cmd, With: step.With}
			issues := spec.Validate(reg, ref, step.FilesGate)
			if len(issues) > 0 {
				prefix := phase.Name + "/" + step.Name
				if service != "" {
					prefix = service + "/" + prefix
				}
				return nil, fmt.Errorf("step %s: %s", prefix, issues[0].Message)
			}
		}
		result = append(result, ResolvedStep{Phase: phase, Step: step, Service: service, RuntimeWhen: stepRuntimeWhen, PhaseWhen: phaseRuntimeWhen, FilesGate: step.FilesGate})
	}
	return result, nil
}

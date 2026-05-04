package pipeline

import (
	"fmt"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
)

// ResolvePhaseSteps resolves steps for a single phase, evaluating when conditions.
// service is empty for orchestrator phases.
//
// Phase-level when is evaluated first:
//   - Go template: evaluated at plan time; entire phase is excluded when false.
//   - Runtime condition: propagated to each step that does not already carry its
//     own runtime when condition. The phase condition is stored in PhaseWhen and
//     evaluated before each such step at execution time.
func ResolvePhaseSteps(cfg *config.DevboxConfig, phase config.DeployPhase, service string) ([]ResolvedStep, error) {
	phaseRuntimeWhen := ""
	if phase.When != "" {
		if condition.IsRuntime(phase.When) {
			phaseRuntimeWhen = phase.When
		} else {
			ok, err := tpl.EvalCondition(phase.When, cfg)
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
		if step.When != "" {
			if condition.IsRuntime(step.When) {
				if step.Builtin != "" {
					if err := builtin.Validate(step.Builtin, step.With); err != nil {
						prefix := phase.Name + "/" + step.Name
						if service != "" {
							prefix = service + "/" + prefix
						}
						return nil, fmt.Errorf("step %s: invalid builtin: %w", prefix, err)
					}
				}
				result = append(result, ResolvedStep{Phase: phase, Step: step, Service: service, RuntimeWhen: step.When, PhaseWhen: phaseRuntimeWhen})
				continue
			}
			ok, err := tpl.EvalCondition(step.When, cfg)
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
		if step.Builtin != "" {
			if err := builtin.Validate(step.Builtin, step.With); err != nil {
				prefix := phase.Name + "/" + step.Name
				if service != "" {
					prefix = service + "/" + prefix
				}
				return nil, fmt.Errorf("step %s: invalid builtin: %w", prefix, err)
			}
		}
		result = append(result, ResolvedStep{Phase: phase, Step: step, Service: service, PhaseWhen: phaseRuntimeWhen})
	}
	return result, nil
}

package pipeline

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin"
	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// Exported sentinel errors for parallel-group plan-time validation. Callers
// (validate package, command layer) match these with errors.Is.
var (
	// ErrNestedParallel is returned when a parallel group contains a sub-step
	// that itself declares a parallel block. Nested parallel groups are not
	// supported in v1.
	ErrNestedParallel = errors.New("nested parallel groups are not supported")
	// ErrUnnamedSubStep is returned when a parallel sub-step is missing the
	// required `name` field. Names are required because the journal and
	// skip-decider key by (phase, step.Name).
	ErrUnnamedSubStep = errors.New("parallel sub-step must have a name")
	// ErrInteractiveInParallel is returned when a parallel sub-step would
	// require an interactive prompt at runtime without skip_confirm set.
	ErrInteractiveInParallel = errors.New("interactive prompt in parallel sub-step requires skip_confirm: true")
	// ErrEmptyParallelSteps is returned when a parallel group declares fewer
	// than two sub-steps. Single-element groups should be expressed as leaf
	// steps.
	ErrEmptyParallelSteps = errors.New("parallel group must declare at least two sub-steps")
	// ErrDuplicateStepName is returned when two steps in the same phase share
	// the same name (including sub-steps across multiple parallel groups).
	// The journal keys entries by (phase, step.Name); collisions cause
	// incorrect resume behaviour.
	ErrDuplicateStepName = errors.New("duplicate step name in phase")
	// ErrSubStepOverridesInvalid is returned when a step's sub_step_overrides
	// block fails plan-time validation: target is not a workflow, key does not
	// match any sub-step, key targets a nested workflow, or files_gate inside
	// the override is invalid against the sub-step's command.
	ErrSubStepOverridesInvalid = errors.New("sub_step_overrides invalid")
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
// reg (registry) is used to validate files_gate directives if present, and to
// walk workflow command references for interactive-prompt detection inside
// parallel groups. When reg is nil, both checks are skipped; this is tolerated
// for internal-tool and test callers. Runtime callers (deploy run, reset run,
// etc.) MUST pass a non-nil registry.
func ResolvePhaseSteps(cfg *config.DweConfig, reg *registry.Registry, phase config.DeployPhase, service string) ([]ResolvedStep, error) {
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
		if step.Parallel != nil {
			resolvedGroup, err := resolveParallelStep(cfg, reg, phase, service, step, phaseRuntimeWhen)
			if err != nil {
				return nil, err
			}
			if resolvedGroup == nil {
				continue
			}
			result = append(result, *resolvedGroup)
			continue
		}

		rs, ok, err := resolveLeafStep(cfg, reg, phase, service, step, phaseRuntimeWhen)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		result = append(result, rs)
	}

	// Enforce name uniqueness within the phase across all leaf steps and
	// parallel sub-steps. Required by the journal key model.
	if err := checkUniqueStepNames(result, phase, service); err != nil {
		return nil, err
	}
	return result, nil
}

// resolveLeafStep resolves a single leaf-style DeployStep. Returns ok=false when
// the step's template `when:` evaluates to false (step is filtered out).
func resolveLeafStep(cfg *config.DweConfig, reg *registry.Registry, phase config.DeployPhase, service string, step config.DeployStep, phaseRuntimeWhen *condition.Condition) (ResolvedStep, bool, error) {
	stepRuntimeWhen, keep, err := resolveStepWhen(cfg, step, stepPrefix(phase, service, step.Name))
	if err != nil {
		return ResolvedStep{}, false, err
	}
	if !keep {
		return ResolvedStep{}, false, nil
	}
	if step.Type == "builtin" {
		// Engine-synthetic phases (underscore-prefixed) may use KindInternal builtins.
		// User-authored phase names cannot start with "_" (rejected at loader time).
		bodyCtx := builtin.CtxUserYAML
		if strings.HasPrefix(phase.Name, "_") {
			bodyCtx = builtin.CtxInternal
		}
		if err := builtin.Validate(step.Cmd, step.With, bodyCtx); err != nil {
			return ResolvedStep{}, false, fmt.Errorf("step %s: invalid builtin: %w", stepPrefix(phase, service, step.Name), err)
		}
	}
	if step.Check != nil && step.Check.Type == "builtin" {
		if err := builtin.Validate(step.Check.Cmd, step.Check.With, builtin.CtxPredicate); err != nil {
			return ResolvedStep{}, false, fmt.Errorf("step %s check: invalid builtin: %w", stepPrefix(phase, service, step.Name), err)
		}
	}
	if step.FilesGate != nil && reg != nil {
		ref := filesgate.StepRef{Type: step.Type, Cmd: step.Cmd, With: step.With}
		issues := spec.Validate(cfg, reg, ref, step.FilesGate)
		if len(issues) > 0 {
			msgs := make([]string, len(issues))
			for i, iss := range issues {
				msgs[i] = iss.Message
			}
			return ResolvedStep{}, false, fmt.Errorf("step %s: %s", stepPrefix(phase, service, step.Name), strings.Join(msgs, "; "))
		}
	}
	if len(step.SubStepOverrides) > 0 {
		if err := validateSubStepOverrides(cfg, reg, phase, service, step); err != nil {
			return ResolvedStep{}, false, err
		}
	}
	return ResolvedStep{
		Phase:       phase,
		Step:        step,
		Service:     service,
		RuntimeWhen: stepRuntimeWhen,
		PhaseWhen:   phaseRuntimeWhen,
		FilesGate:   step.FilesGate,
	}, true, nil
}

// resolveParallelStep recursively resolves a parallel-group DeployStep. Returns
// (nil, nil) when the group's template `when:` evaluates to false.
func resolveParallelStep(cfg *config.DweConfig, reg *registry.Registry, phase config.DeployPhase, service string, step config.DeployStep, phaseRuntimeWhen *condition.Condition) (*ResolvedStep, error) {
	prefix := stepPrefix(phase, service, step.Name)
	if len(step.Parallel.Steps) < 2 {
		return nil, fmt.Errorf("step %s: %w", prefix, ErrEmptyParallelSteps)
	}

	stepRuntimeWhen, keep, err := resolveStepWhen(cfg, step, prefix)
	if err != nil {
		return nil, err
	}
	if !keep {
		return nil, nil
	}

	subs := make([]ResolvedStep, 0, len(step.Parallel.Steps))
	for _, sub := range step.Parallel.Steps {
		if sub.Parallel != nil {
			return nil, fmt.Errorf("step %s/%s: %w", prefix, sub.Name, ErrNestedParallel)
		}
		if sub.Name == "" {
			return nil, fmt.Errorf("step %s: %w", prefix, ErrUnnamedSubStep)
		}
		// Inherit skip_confirm from the group (monotonic OR; a sub-step's
		// `skip_confirm: false` cannot un-set an inherited true).
		if step.SkipConfirm {
			sub.SkipConfirm = true
		}
		// Reject interactive prompts in sub-steps unless skip_confirm is set.
		// skip_confirm only suppresses the confirm builtin's auto-yes path.
		// Other interactive builtins (e.g. docker_daemon_logs) have no
		// auto-skip path and must be rejected regardless of skip_confirm.
		if !sub.SkipConfirm {
			if err := checkInteractive(reg, sub, map[string]bool{}); err != nil {
				return nil, fmt.Errorf("step %s/%s: %w", prefix, sub.Name, err)
			}
		} else if sub.Type == "builtin" && builtin.IsInteractive(sub.Cmd) && sub.Cmd != "confirm" {
			return nil, fmt.Errorf("step %s/%s: %w", prefix, sub.Name, ErrInteractiveInParallel)
		}
		rs, ok, err := resolveLeafStep(cfg, reg, phase, service, sub, phaseRuntimeWhen)
		if err != nil {
			return nil, err
		}
		if !ok {
			// A template `when: false` on a sub-step filters it out. After
			// filtering, the group must still have at least two sub-steps.
			continue
		}
		subs = append(subs, rs)
	}
	if len(subs) < 2 {
		// Line 152 already rejects groups with fewer than 2 declared sub-steps, so
		// reaching here means template when: evaluation filtered out sub-steps.
		return nil, fmt.Errorf("step %s: %w (%d of %d sub-step(s) remain after when: filtering)",
			prefix, ErrEmptyParallelSteps, len(subs), len(step.Parallel.Steps))
	}

	max := step.Parallel.MaxConcurrent
	if max <= 0 {
		max = runtime.NumCPU()
	}
	if max > len(subs) {
		max = len(subs)
	}
	failFast := true
	if step.Parallel.FailFast != nil {
		failFast = *step.Parallel.FailFast
	}

	return &ResolvedStep{
		Phase:       phase,
		Step:        step,
		Service:     service,
		RuntimeWhen: stepRuntimeWhen,
		PhaseWhen:   phaseRuntimeWhen,
		Parallel: &ResolvedParallel{
			MaxConcurrent: max,
			FailFast:      failFast,
			Steps:         subs,
		},
	}, nil
}

// resolveStepWhen evaluates a step's `when:` at plan time. A runtime condition
// is returned as runtimeWhen (to attach to the resolved step) with keep=true; a
// template condition is evaluated immediately and keep reports whether the step
// survives filtering (keep=false when it evaluates to false). prefix names the
// step for error messages. when: nil or a non-template/non-runtime condition
// yields (nil, true, nil).
func resolveStepWhen(cfg *config.DweConfig, step config.DeployStep, prefix string) (runtimeWhen *condition.Condition, keep bool, err error) {
	if step.When == nil {
		return nil, true, nil
	}
	if step.When.IsRuntime() {
		return step.When, true, nil
	}
	if step.When.Type == condition.TypeTemplate {
		ok, evalErr := tpl.EvalCondition(step.When.Expr, cfg)
		if evalErr != nil {
			return nil, false, fmt.Errorf("evaluating when condition for step %s: %w", prefix, evalErr)
		}
		if !ok {
			return nil, false, nil
		}
	}
	return nil, true, nil
}

// checkInteractive rejects a sub-step that would require an interactive prompt
// at runtime. Sources:
//   - type=command targeting a CommandDef with Confirmation: true
//   - type=builtin cmd=confirm
//   - type=command targeting a workflow whose steps transitively contain a
//     non-empty WorkflowStep.Confirm or a Command reference to a confirming
//     command/workflow.
//
// When reg is nil, all registry-dependent checks are skipped (same nil-tolerant
// pattern as files_gate validation).
func checkInteractive(reg *registry.Registry, step config.DeployStep, visited map[string]bool) error {
	if step.Type == "builtin" && builtin.IsInteractive(step.Cmd) {
		return ErrInteractiveInParallel
	}
	if step.Type != "command" || reg == nil {
		return nil
	}
	return checkCommandInteractive(reg, step.Cmd, visited)
}

func checkCommandInteractive(reg *registry.Registry, cmdID string, visited map[string]bool) error {
	if visited[cmdID] {
		return nil
	}
	visited[cmdID] = true
	def, err := reg.Get(cmdID)
	if err != nil || def == nil {
		// Unknown command: not our concern to flag here; other validators
		// surface unresolved references.
		return nil
	}
	if def.Confirmation {
		return ErrInteractiveInParallel
	}
	if def.Type == model.CommandTypeBuiltin && builtin.IsInteractive(def.Cmd) {
		return ErrInteractiveInParallel
	}
	if def.Type != model.CommandTypeWorkflow {
		return nil
	}
	for _, ws := range def.Steps {
		if ws.Confirm != "" {
			return ErrInteractiveInParallel
		}
		if ws.Command != "" {
			if err := checkCommandInteractive(reg, ws.Command, visited); err != nil {
				return err
			}
		}
		if ws.Parallel != nil {
			for _, sub := range ws.Parallel.Steps {
				if sub.Confirm != "" {
					return ErrInteractiveInParallel
				}
				if sub.Command != "" {
					if err := checkCommandInteractive(reg, sub.Command, visited); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func checkUniqueStepNames(steps []ResolvedStep, phase config.DeployPhase, service string) error {
	seen := make(map[string]bool)
	for _, rs := range steps {
		if rs.Parallel != nil {
			if rs.Step.Name != "" {
				if seen[rs.Step.Name] {
					return fmt.Errorf("phase %s: %w: %q", stepPrefix(phase, service, ""), ErrDuplicateStepName, rs.Step.Name)
				}
				seen[rs.Step.Name] = true
			}
			for _, sub := range rs.Parallel.Steps {
				if seen[sub.Step.Name] {
					return fmt.Errorf("phase %s: %w: %q", stepPrefix(phase, service, ""), ErrDuplicateStepName, sub.Step.Name)
				}
				seen[sub.Step.Name] = true
			}
			continue
		}
		if rs.Step.Name == "" {
			continue
		}
		if seen[rs.Step.Name] {
			return fmt.Errorf("phase %s: %w: %q", stepPrefix(phase, service, ""), ErrDuplicateStepName, rs.Step.Name)
		}
		seen[rs.Step.Name] = true
	}
	return nil
}

// validateSubStepOverrides validates a step's sub_step_overrides map against
// the target workflow's leaf sub-steps. Rules:
//   - step must have type=command (others have no workflow to override)
//   - reg must resolve the referenced command to a workflow
//   - each override key must match a leaf sub-step name in the workflow
//   - keys must not target nested workflows (sub-step whose Command is itself
//     a workflow); v1 only supports one level of override
//   - each override.files_gate is validated against the sub-step's command
//     using the same filesgate/spec rules as step-level files_gate
//
// A nil registry is tolerated (test / internal-tool callers): only the
// dependent registry checks are skipped.
func validateSubStepOverrides(cfg *config.DweConfig, reg *registry.Registry, phase config.DeployPhase, service string, step config.DeployStep) error {
	prefix := stepPrefix(phase, service, step.Name)
	if step.Type != "command" {
		return fmt.Errorf("step %s: %w: only steps with type=command can declare sub_step_overrides", prefix, ErrSubStepOverridesInvalid)
	}
	if reg == nil {
		return nil
	}
	def, err := reg.Get(step.Cmd)
	if err != nil {
		return fmt.Errorf("step %s: %w: unknown command %q", prefix, ErrSubStepOverridesInvalid, step.Cmd)
	}
	if def.Type != model.CommandTypeWorkflow {
		return fmt.Errorf("step %s: %w: command %q is not a workflow", prefix, ErrSubStepOverridesInvalid, step.Cmd)
	}

	// Build a name → sub-step(s) map of all leaf sub-steps directly declared
	// by the workflow (top-level Command steps + parallel-leaf Command steps).
	// Multiple sub-steps may share a name when neither sets an explicit
	// `name:` and the same command appears more than once; we only flag the
	// ambiguity when an override key actually targets that name.
	// Nested workflows are flagged when an override key matches a step whose
	// Command itself targets another workflow.
	leaves := make(map[string][]model.WorkflowStep)
	for _, ws := range def.Steps {
		if ws.Command != "" {
			name := ws.StepName()
			if name != "" {
				leaves[name] = append(leaves[name], ws)
			}
		}
		if ws.Parallel != nil {
			for _, sub := range ws.Parallel.Steps {
				if sub.Command == "" {
					continue
				}
				name := sub.StepName()
				if name == "" {
					continue
				}
				leaves[name] = append(leaves[name], sub)
			}
		}
	}

	// Determinism: walk keys in sorted order so plan errors are stable across runs.
	keys := make([]string, 0, len(step.SubStepOverrides))
	for k := range step.SubStepOverrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		matches, ok := leaves[name]
		if !ok || len(matches) == 0 {
			return fmt.Errorf("step %s: %w: sub_step_overrides[%q] does not match any sub-step of workflow %q",
				prefix, ErrSubStepOverridesInvalid, name, step.Cmd)
		}
		if len(matches) > 1 {
			return fmt.Errorf("step %s: %w: sub_step_overrides[%q] is ambiguous (%d sub-steps share this name in workflow %q); set explicit name: on the workflow sub-steps to disambiguate",
				prefix, ErrSubStepOverridesInvalid, name, len(matches), step.Cmd)
		}
		ws := matches[0]
		// Reject targeting a sub-step whose Command itself is a workflow —
		// the override cannot reach inside the nested workflow.
		subDef, subErr := reg.Get(ws.Command)
		if subErr == nil && subDef != nil && subDef.Type == model.CommandTypeWorkflow {
			return fmt.Errorf("step %s: %w: sub_step_overrides[%q] targets workflow %q; nested workflow overrides are not supported in v1",
				prefix, ErrSubStepOverridesInvalid, name, ws.Command)
		}
		ov := step.SubStepOverrides[name]
		if ov.FilesGate != nil {
			if subErr != nil || subDef == nil {
				return fmt.Errorf("step %s: %w: sub_step_overrides[%q] files_gate target command %q not found",
					prefix, ErrSubStepOverridesInvalid, name, ws.Command)
			}
			// Use the sub-step's With (rendered later at runtime) as the
			// inherited base. The override may set its own files_gate.with.
			refWith := make(map[string]any, len(ws.With))
			for k, v := range ws.With {
				refWith[k] = v
			}
			ref := filesgate.StepRef{Type: "command", Cmd: ws.Command, With: refWith}
			issues := spec.Validate(cfg, reg, ref, ov.FilesGate)
			if len(issues) > 0 {
				msgs := make([]string, len(issues))
				for i, iss := range issues {
					msgs[i] = iss.Message
				}
				return fmt.Errorf("step %s: %w: sub_step_overrides[%q]: %s",
					prefix, ErrSubStepOverridesInvalid, name, strings.Join(msgs, "; "))
			}
		}
	}
	return nil
}

func stepPrefix(phase config.DeployPhase, service, stepName string) string {
	prefix := phase.Name
	if service != "" {
		prefix = service + "/" + prefix
	}
	if stepName != "" {
		prefix = prefix + "/" + stepName
	}
	return prefix
}

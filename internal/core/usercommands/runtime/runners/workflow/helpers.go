package workflow

import (
	"fmt"
	"io"
	"strings"

	"github.com/semsemyonoff/devbox/internal/core/execution/filesgate"
	fgspec "github.com/semsemyonoff/devbox/internal/core/execution/filesgate/spec"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/runtime/spec"
)

// dumpSubStepOutput writes a sub-step's captured output between labelled
// separator bars on w. The top bar names the sub-step so multi-failure dumps
// stay attributable; ANSI escape sequences in output are forwarded verbatim
// so the child's colours survive the round-trip. No-op when output is empty.
func dumpSubStepOutput(w io.Writer, command, output string) {
	if output == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "  ───── output: %s ─────\n", command)
	_, _ = fmt.Fprint(w, output)
	if !strings.HasSuffix(output, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintln(w, "  ──────────────────")
}

// evalSubStepOverrideGate probes the files_gate override (if any) registered
// against this workflow sub-step. Returns (skip=true, reason, nil) when the
// gate is not satisfied and the sub-step should be skipped without running.
// Returns (false, "", nil) when no override applies or the gate is satisfied.
// Returns a non-nil error only for configuration failures the user should see.
//
// The override is intentionally consumed once per sub-step: the inner
// RunContext built by runCommandStep does NOT propagate the map, so an inner
// workflow does not see the outer pipeline-step's overrides.
func evalSubStepOverrideGate(rc spec.RunContext, step model.WorkflowStep) (skip bool, reason string, err error) {
	if len(rc.WorkflowSubStepOverrides) == 0 {
		return false, "", nil
	}
	name := step.StepName()
	if name == "" {
		return false, "", nil
	}
	ov, ok := rc.WorkflowSubStepOverrides[name]
	if !ok || ov.FilesGate == nil {
		return false, "", nil
	}
	if rc.Registry == nil {
		return false, "", fmt.Errorf("sub_step_overrides[%q]: registry required to evaluate files_gate", name)
	}

	targetCmd := ov.FilesGate.Command
	if targetCmd == "" {
		targetCmd = step.Command
	}
	def, err := rc.Registry.Get(targetCmd)
	if err != nil {
		return false, "", fmt.Errorf("sub_step_overrides[%q]: command %q: %w", name, targetCmd, err)
	}
	if len(def.Files) == 0 {
		return false, "", fmt.Errorf("sub_step_overrides[%q]: command %q has no files: block", name, targetCmd)
	}

	gateWith := ov.FilesGate.With
	if len(gateWith) == 0 {
		gateWith = make(map[string]any, len(step.With))
		for k, v := range step.With {
			gateWith[k] = v
		}
	}

	if rc.Config == nil {
		return false, "", fmt.Errorf("sub_step_overrides[%q]: config required to evaluate files_gate", name)
	}
	probeCtx, err := BuildRunContextFn(rc.Config, rc.Registry, def, gateWith, rc.ProjectRoot)
	if err != nil {
		return false, "", fmt.Errorf("sub_step_overrides[%q]: build context: %w", name, err)
	}

	ids, err := fgspec.ResolveRequireIDs(ov.FilesGate.Require, def.Files)
	if err != nil {
		return false, "", fmt.Errorf("sub_step_overrides[%q]: %w", name, err)
	}
	probeResults, err := ComputeFilePathsProbeFn(probeCtx, ids)
	if err != nil {
		return false, "", fmt.Errorf("sub_step_overrides[%q]: probe: %w", name, err)
	}

	var offending []string
	switch ov.FilesGate.State {
	case filesgate.StateReadable:
		for _, id := range ids {
			if !probeResults[id].Resolved {
				offending = append(offending, id)
			}
		}
	case filesgate.StateMissing:
		for _, id := range ids {
			if probeResults[id].Resolved {
				offending = append(offending, id)
			}
		}
	default:
		return false, "", fmt.Errorf("sub_step_overrides[%q]: invalid state %q", name, ov.FilesGate.State)
	}

	if len(offending) == 0 {
		return false, "", nil
	}
	reason = fmt.Sprintf("files_gate: %s [%s]", ov.FilesGate.State, strings.Join(offending, ","))
	return true, reason, nil
}

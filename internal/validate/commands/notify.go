package commands

// notify.go implements validator rules for the `notify:` field on CommandDef.
//
// Two phases:
//
//  1. Per-file (no registry): notify: true + type: daemon → error.
//
//  2. Registry-aware: for every direct parallel sub-step whose resolved target
//     command has notify: true, emit info-level. This is purely educational —
//     the runtime SkipNotify guard already suppresses transitive notifications,
//     so the validator does NOT walk transitive containment (workflow A →
//     workflow B → parallel with notify-cmd). Static graph traversal across
//     workflows isn't worth the maintenance cost when the runtime guard is
//     already correct.

import (
	"fmt"

	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/validate"
)

// notifyDaemonDiagnostics emits an error when notify: true is set on a
// type: daemon command. Called from the per-file pass in commands.go.
func notifyDaemonDiagnostics(cmd model.CommandDef, relFile string) []validate.Diagnostic {
	if !cmd.Notify || cmd.Type != model.CommandTypeDaemon {
		return nil
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityError,
		Domain:   "commands",
		Target:   fmt.Sprintf("commands:%s", cmd.ID),
		File:     relFile,
		Message:  fmt.Sprintf("%s: notify is not allowed on type: daemon commands", cmd.ID),
		Hint:     "daemons have no completion event; remove notify or change the command type",
	}}
}

// notifyParallelSubStepDiagnostics emits info-level diagnostics for every
// direct parallel sub-step whose resolved target command has notify: true.
// Requires the registry to resolve sub-step command IDs.
func notifyParallelSubStepDiagnostics(reg *registry.Registry) []validate.Diagnostic {
	var out []validate.Diagnostic
	for _, cmd := range reg.ListAll("") {
		if cmd.Type != model.CommandTypeWorkflow {
			continue
		}
		parentID := cmd.ID
		registry.WalkWorkflowSteps(cmd.Steps, "step", func(path string, step model.WorkflowStep) {
			if step.Parallel == nil {
				return
			}
			for j, sub := range step.Parallel.Steps {
				if sub.Command == "" {
					continue
				}
				target, err := reg.Get(sub.Command)
				if err != nil || target == nil {
					continue
				}
				if !target.Notify {
					continue
				}
				out = append(out, validate.Diagnostic{
					Severity: validate.SeverityInfo,
					Domain:   "commands",
					Target:   fmt.Sprintf("commands:%s", parentID),
					Message: fmt.Sprintf(
						"%s.parallel.steps[%d]: notify on a direct sub-step inside a parallel block is ignored at runtime",
						path, j),
					Hint: "the runtime suppresses notifications for any command invoked from inside another command — make it the top-level command if you want a notification",
				})
			}
		})
	}
	return out
}

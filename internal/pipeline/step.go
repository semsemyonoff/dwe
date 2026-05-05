package pipeline

import (
	"fmt"
	"sort"
	"strings"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
)

// ResolvedStep holds a pipeline step together with the phase it belongs to,
// after when-condition filtering.
//
// Service is non-empty for steps that belong to a per-service deploy pipeline.
// RuntimeWhen is non-nil when the step's When condition is a runtime expression
// (builtin predicate or shell cmd). Such conditions are NOT evaluated at plan-resolution
// time — they are checked immediately before the step executes.
// PhaseWhen carries the phase-level runtime when condition; evaluated once per phase.
type ResolvedStep struct {
	Phase       config.DeployPhase
	Step        config.DeployStep
	Service     string               // non-empty for per-service steps (e.g. "main")
	RuntimeWhen *condition.Condition // step-level runtime when condition; nil otherwise
	PhaseWhen   *condition.Condition // phase-level runtime when condition; nil otherwise
}

// StepAddress returns the full address of a step for display and lookup:
//   - orchestrator steps: "<phase>/<step>"
//   - service steps:      "<service>/<phase>/<step>"
func (rs ResolvedStep) StepAddress() string {
	if rs.Service != "" {
		return rs.Service + "/" + rs.Phase.Name + "/" + rs.Step.Name
	}
	return rs.Phase.Name + "/" + rs.Step.Name
}

// stepBadge returns the display badge for a step based on its type.
func stepBadge(step config.DeployStep) string {
	switch step.Type {
	case "command":
		return "[command]"
	case "builtin":
		return "[builtin]"
	case "devbox":
		return "[devbox]"
	case "shell":
		return "[shell]"
	default:
		return "[" + step.Type + "]"
	}
}

// StepCommand returns the resolved command or action string for plan display.
//   - command: steps — "<devboxBin> commands run <id> [--set key=value...]"
//   - builtin: steps — builtin description from registry (e.g. "builtin: confirm(...)")
//   - devbox: steps  — "<devboxBin> <args>"
//   - shell: steps   — raw shell command
//
// devboxBin is the configured binary name (from BinariesConfig.Devbox, e.g. "devbox").
// It is used only for display — actual execution uses os.Executable() with this as fallback.
func StepCommand(step config.DeployStep, devboxBin string) string {
	switch step.Type {
	case "command":
		parts := []string{devboxBin, "commands", "run", strings.TrimSpace(step.Cmd)}
		if len(step.With) > 0 {
			keys := make([]string, 0, len(step.With))
			for k := range step.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts = append(parts, "--set", k+"="+fmt.Sprintf("%v", step.With[k]))
			}
		}
		return strings.Join(parts, " ")
	case "builtin":
		return builtin.Describe(step.Cmd, step.With)
	case "devbox":
		return devboxBin + " " + strings.TrimSpace(step.Cmd)
	case "shell":
		return strings.TrimSpace(step.Cmd)
	default:
		return step.Type + ": " + strings.TrimSpace(step.Cmd)
	}
}

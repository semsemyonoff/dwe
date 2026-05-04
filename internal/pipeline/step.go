package pipeline

import (
	"fmt"
	"sort"
	"strings"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/config"
)

// ResolvedStep holds a pipeline step together with the phase it belongs to,
// after when-condition filtering.
//
// Service is non-empty for steps that belong to a per-service deploy pipeline.
// RuntimeWhen is non-empty when the step's When condition is a runtime expression
// (builtin predicate or cmd:). Such conditions are NOT evaluated at plan-resolution
// time — they are checked immediately before the step executes.
// PhaseWhen carries the phase-level runtime when condition; evaluated once per phase.
type ResolvedStep struct {
	Phase       config.DeployPhase
	Step        config.DeployStep
	Service     string // non-empty for per-service steps (e.g. "main")
	RuntimeWhen string // step-level runtime when condition; empty otherwise
	PhaseWhen   string // phase-level runtime when condition; evaluated once per phase
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
	switch {
	case step.Command != "":
		return "[command]"
	case step.Builtin != "":
		return "[builtin]"
	case step.Devbox != "":
		return "[devbox]"
	default:
		return "[run]"
	}
}

// StepCommand returns the resolved command or action string for plan display.
//   - command: steps — "<devboxBin> commands run <id> [--set key=value...]"
//   - builtin: steps — builtin description from registry (e.g. "builtin: confirm(...)")
//   - devbox: steps  — "<devboxBin> <args>"
//   - run: steps     — raw shell command
//
// devboxBin is the configured binary name (from BinariesConfig.Devbox, e.g. "devbox").
// It is used only for display — actual execution uses os.Executable() with this as fallback.
func StepCommand(step config.DeployStep, devboxBin string) string {
	switch {
	case step.Command != "":
		parts := []string{devboxBin, "commands", "run", strings.TrimSpace(step.Command)}
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
	case step.Builtin != "":
		return builtin.Describe(step.Builtin, step.With)
	case step.Devbox != "":
		return devboxBin + " " + strings.TrimSpace(step.Devbox)
	default:
		return strings.TrimSpace(step.Run)
	}
}

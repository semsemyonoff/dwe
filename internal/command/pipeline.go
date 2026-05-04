package command

import (
	"fmt"
	"io"

	pipeline "devbox-cli/internal/pipeline"
)

// printDeployPlanShell emits executable shell commands for each step.
// Prepends "set -e" so the pipeline aborts on any step failure.
// After the implicit .env generation step, ". .env" is emitted so variables
// are available to all subsequent steps in the generated script.
func printDeployPlanShell(steps []pipeline.ResolvedStep, w io.Writer) {
	_, _ = fmt.Fprintln(w, "set -e")
	lastService := ""
	lastPhaseKey := ""
	for _, rs := range steps {
		if rs.Service != "" && rs.Service != lastService {
			_, _ = fmt.Fprintf(w, "\n# === service: %s ===\n", rs.Service)
			lastService = rs.Service
		}
		phaseKey := rs.Service + "/" + rs.Phase.Name
		if phaseKey != lastPhaseKey {
			if rs.Phase.When != "" {
				_, _ = fmt.Fprintf(w, "# phase %s [when: %s]\n", rs.Phase.Name, rs.Phase.When)
			}
			lastPhaseKey = phaseKey
		}
		if rs.RuntimeWhen != "" {
			_, _ = fmt.Fprintf(w, "# when: %s\n", rs.RuntimeWhen)
		}
		switch {
		case rs.Step.Builtin != "" && rs.Step.ContinueOnError:
			// Builtins are in-process Go; delegate to the CLI step runner so the
			// generated script remains executable and behaviorally equivalent.
			_, _ = fmt.Fprintf(w, "./bin/devbox deploy step %s || true\n", rs.StepAddress())
		case rs.Step.Builtin != "":
			_, _ = fmt.Fprintf(w, "./bin/devbox deploy step %s\n", rs.StepAddress())
		case rs.Step.ContinueOnError:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step)+" || true")
		default:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step))
		}
		if rs.Step.Name == implicitEnvStep.Name {
			_, _ = fmt.Fprintln(w, ". .env")
		}
		if rs.Step.Check != "" {
			_, _ = fmt.Fprintf(w, "# check: %s\n", rs.Step.Check)
		}
	}
}

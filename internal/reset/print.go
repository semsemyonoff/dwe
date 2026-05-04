package reset

import (
	"fmt"
	"io"

	"devbox-cli/internal/pipeline"
)

// PrintPlanShell emits shell commands for the reset plan.
// Unlike the deploy plan shell output, there is no implicit .env step.
func PrintPlanShell(steps []pipeline.ResolvedStep, w io.Writer) {
	_, _ = fmt.Fprintln(w, "set -e")
	lastPhaseKey := ""
	for _, rs := range steps {
		if rs.Phase.Name != lastPhaseKey {
			if rs.Phase.When != "" {
				_, _ = fmt.Fprintf(w, "# phase %s [when: %s]\n", rs.Phase.Name, rs.Phase.When)
			}
			lastPhaseKey = rs.Phase.Name
		}
		if rs.RuntimeWhen != "" {
			_, _ = fmt.Fprintf(w, "# when: %s\n", rs.RuntimeWhen)
		}
		switch {
		case rs.Step.Builtin != "" && rs.Step.ContinueOnError:
			_, _ = fmt.Fprintf(w, "./bin/devbox reset step %s || true\n", rs.StepAddress())
		case rs.Step.Builtin != "":
			_, _ = fmt.Fprintf(w, "./bin/devbox reset step %s\n", rs.StepAddress())
		case rs.Step.ContinueOnError:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step)+" || true")
		default:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step))
		}
		if rs.Step.Check != "" {
			_, _ = fmt.Fprintf(w, "# check: %s\n", rs.Step.Check)
		}
	}
}

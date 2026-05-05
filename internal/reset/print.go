package reset

import (
	"fmt"
	"io"

	"devbox-cli/internal/pipeline"
)

// PrintPlanShell emits shell commands for the reset plan.
// Unlike the deploy plan shell output, there is no implicit .env step.
// devboxBin is the configured binary name used in emitted commands (e.g. "devbox").
func PrintPlanShell(steps []pipeline.ResolvedStep, w io.Writer, devboxBin string) {
	_, _ = fmt.Fprintln(w, "set -e")
	lastPhaseKey := ""
	for _, rs := range steps {
		if rs.Phase.Name != lastPhaseKey {
			if rs.Phase.When != nil {
				_, _ = fmt.Fprintf(w, "# phase %s [when: %s]\n", rs.Phase.Name, pipeline.FormatCondition(rs.Phase.When))
			}
			lastPhaseKey = rs.Phase.Name
		}
		if rs.RuntimeWhen != nil {
			_, _ = fmt.Fprintf(w, "# when: %s\n", pipeline.FormatCondition(rs.RuntimeWhen))
		}
		switch {
		case rs.Step.Type == "builtin" && rs.Step.ContinueOnError:
			_, _ = fmt.Fprintf(w, "%s reset step %s || true\n", devboxBin, rs.StepAddress())
		case rs.Step.Type == "builtin":
			_, _ = fmt.Fprintf(w, "%s reset step %s\n", devboxBin, rs.StepAddress())
		case rs.Step.ContinueOnError:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step, devboxBin)+" || true")
		default:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step, devboxBin))
		}
		if rs.Step.Check != nil {
			_, _ = fmt.Fprintf(w, "# check: %s\n", pipeline.FormatAction(rs.Step.Check))
		}
	}
}

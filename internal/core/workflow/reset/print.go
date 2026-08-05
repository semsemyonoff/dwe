package reset

import (
	"fmt"
	"io"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
)

// PrintPlanShell emits shell commands for the reset plan.
// Unlike the deploy plan shell output, there is no implicit .env step.
// dweBin is the configured binary name (e.g. "dwe")).
func PrintPlanShell(steps []pipeline.ResolvedStep, w io.Writer, dweBin string) {
	_, _ = fmt.Fprintln(w, "set -e")
	lastPhaseKey := ""
	for _, rs := range steps {
		if rs.Phase.Name != lastPhaseKey {
			// DisplayPhaseWhen, not the raw Phase.When: the raw form still
			// carries the literal ${vars.*} text, while the phase actually
			// runs the rendered command.
			if when := rs.DisplayPhaseWhen(); when != nil {
				_, _ = fmt.Fprintf(w, "# phase %s [when: %s]\n", rs.Phase.Name, pipeline.FormatCondition(when))
			}
			lastPhaseKey = rs.Phase.Name
		}
		if rs.RuntimeWhen != nil {
			_, _ = fmt.Fprintf(w, "# when: %s\n", pipeline.FormatCondition(rs.RuntimeWhen))
		}
		switch {
		case rs.Step.Type == "builtin" && rs.Step.ContinueOnError:
			_, _ = fmt.Fprintf(w, "%s reset step %s || true\n", dweBin, rs.StepAddress())
		case rs.Step.Type == "builtin":
			_, _ = fmt.Fprintf(w, "%s reset step %s\n", dweBin, rs.StepAddress())
		case rs.Step.ContinueOnError:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step, dweBin)+" || true")
		default:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step, dweBin))
		}
		if check := rs.DisplayCheck(); check != "" {
			_, _ = fmt.Fprintf(w, "# check: %s\n", check)
		}
	}
}

package deploy

import (
	"fmt"
	"io"

	"github.com/semsemyonoff/devbox/internal/core/execution/pipeline"
)

// PrintPlanShell emits executable shell commands for each step.
// Prepends "set -e" so the pipeline aborts on any step failure.
// After the implicit .env generation step, ". .env" is emitted so variables
// are available to all subsequent steps in the generated script.
// devboxBin is the configured binary name used in emitted commands (e.g. "devbox").
func PrintPlanShell(steps []pipeline.ResolvedStep, w io.Writer, devboxBin string) {
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
			if rs.Phase.When != nil {
				_, _ = fmt.Fprintf(w, "# phase %s [when: %s]\n", rs.Phase.Name, pipeline.FormatCondition(rs.Phase.When))
			}
			lastPhaseKey = phaseKey
		}
		if rs.RuntimeWhen != nil {
			_, _ = fmt.Fprintf(w, "# when: %s\n", pipeline.FormatCondition(rs.RuntimeWhen))
		}
		switch {
		case rs.Step.Type == "builtin" && rs.Step.ContinueOnError:
			_, _ = fmt.Fprintf(w, "%s deploy step %s || true\n", devboxBin, rs.StepAddress())
		case rs.Step.Type == "builtin":
			_, _ = fmt.Fprintf(w, "%s deploy step %s\n", devboxBin, rs.StepAddress())
		case rs.Step.ContinueOnError:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step, devboxBin)+" || true")
		default:
			_, _ = fmt.Fprintln(w, pipeline.StepCommand(rs.Step, devboxBin))
		}
		if rs.Step.Name == ImplicitEnvStep.Name {
			_, _ = fmt.Fprintln(w, ". .env")
		}
		if rs.Step.Check != nil {
			_, _ = fmt.Fprintf(w, "# check: %s\n", pipeline.FormatAction(rs.Step.Check))
		}
	}
}

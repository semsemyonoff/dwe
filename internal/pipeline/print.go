package pipeline

import (
	"fmt"

	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

// PrintPlanTable prints the plan in human-readable table format.
// devboxBin is forwarded to StepCommand for display (e.g. "devbox" or a custom configured name).
func PrintPlanTable(steps []ResolvedStep, w *render.Writer, devboxBin string) {
	out := w.Writer()
	lastPhaseKey := ""
	lastService := ""
	for _, rs := range steps {
		phaseKey := rs.Phase.Name
		if rs.Service != "" {
			phaseKey = rs.Service + "/" + rs.Phase.Name
		}

		if phaseKey != lastPhaseKey {
			if rs.Service != "" && rs.Service != lastService {
				_, _ = fmt.Fprintln(out, ui.RenderSubheader("service: "+rs.Service))
				lastService = rs.Service
			}
			phaseLine := rs.Phase.Name
			if rs.Service != "" {
				phaseLine = rs.Service + "/" + rs.Phase.Name
			}
			if rs.Phase.Description != "" {
				phaseLine += ": " + rs.Phase.Description
			}
			if rs.Phase.When != nil {
				phaseLine += " [when: " + FormatCondition(rs.Phase.When) + "]"
			}
			indent := ""
			if rs.Service != "" {
				indent = "  "
			}
			_, _ = fmt.Fprintln(out, ui.RenderSubheader(indent+phaseLine))
			lastPhaseKey = phaseKey
		}

		indent := "  "
		detailIndent := "        "
		if rs.Service != "" {
			indent = "    "
			detailIndent = "          "
		}

		badge := stepBadge(rs.Step)
		name := rs.Step.Name
		desc := rs.Step.Description
		cmd := StepCommand(rs.Step, devboxBin)

		if desc != "" {
			_, _ = fmt.Fprintln(out, ui.RenderDefinition(badge+" "+name, desc, len(indent), ""))
		} else {
			_, _ = fmt.Fprintln(out, indent+badge+" "+name)
		}
		if cmd != "" {
			_, _ = fmt.Fprintln(out, detailIndent+cmd)
		}
		if rs.RuntimeWhen != nil {
			_, _ = fmt.Fprintln(out, detailIndent+"[when: "+FormatCondition(rs.RuntimeWhen)+"]")
		}
		if rs.Step.Check != "" {
			_, _ = fmt.Fprintln(out, detailIndent+"[check: "+rs.Step.Check+"]")
		}
		if rs.Step.ContinueOnError {
			_, _ = fmt.Fprintln(out, detailIndent+"[continue_on_error]")
		}
	}
}

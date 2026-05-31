package pipeline

import (
	"fmt"
	"io"

	"github.com/semsemyonoff/devbox/internal/core/ui/render"
	sharedrender "github.com/semsemyonoff/devbox/internal/shared/render"
)

// PrintPlanTable prints the plan in human-readable table format.
// devboxBin is forwarded to StepCommand for display (e.g. "devbox" or a custom configured name).
//
// Parallel groups are rendered with a header line and indented sub-steps. Indices in the
// form [N/total] are shown for the group (as a range, e.g. [12-14/25]) and each sub-step.
// Sequential leaf steps are rendered without an index prefix (legacy format).
func PrintPlanTable(steps []ResolvedStep, w *sharedrender.Writer, devboxBin string) {
	out := w.Writer()

	trackedTotal := computeTrackedTotal(steps)

	lastPhaseKey := ""
	lastService := ""
	trackedIndex := 0
	for _, rs := range steps {
		phaseKey := rs.Phase.Name
		if rs.Service != "" {
			phaseKey = rs.Service + "/" + rs.Phase.Name
		}

		if phaseKey != lastPhaseKey {
			if rs.Service != "" && rs.Service != lastService {
				_, _ = fmt.Fprintln(out, render.Subheader("service: "+rs.Service))
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
			_, _ = fmt.Fprintln(out, render.Subheader(indent+phaseLine))
			lastPhaseKey = phaseKey
		}

		indent := "  "
		detailIndent := "        "
		if rs.Service != "" {
			indent = "    "
			detailIndent = "          "
		}

		if rs.Parallel != nil {
			n := len(rs.Parallel.Steps)
			startIdx := trackedIndex + 1
			endIdx := trackedIndex + n
			failFast := rs.Parallel.FailFast
			groupName := rs.Step.Name
			if groupName == "" {
				groupName = "(unnamed)"
			}
			indexRange := ""
			if !rs.IsUntracked() && trackedTotal > 0 {
				indexRange = fmt.Sprintf("[%d-%d/%d] ", startIdx, endIdx, trackedTotal)
			}
			header := fmt.Sprintf("%s%s[parallel group: %s (%d steps, max_concurrent=%d, fail_fast=%v)]",
				indent, indexRange, groupName, n, rs.Parallel.MaxConcurrent, failFast)
			_, _ = fmt.Fprintln(out, header)
			if rs.Step.Description != "" {
				_, _ = fmt.Fprintln(out, detailIndent+rs.Step.Description)
			}
			if rs.RuntimeWhen != nil {
				_, _ = fmt.Fprintln(out, detailIndent+"[when: "+FormatCondition(rs.RuntimeWhen)+"]")
			}

			subIndent := indent + "  "
			subDetailIndent := detailIndent + "  "
			for _, sub := range rs.Parallel.Steps {
				if !rs.IsUntracked() {
					trackedIndex++
				}
				idxPrefix := ""
				if !rs.IsUntracked() && trackedTotal > 0 {
					idxPrefix = fmt.Sprintf("[%d/%d] ", trackedIndex, trackedTotal)
				}
				printLeafStep(out, sub, subIndent, subDetailIndent, idxPrefix, devboxBin)
			}
			continue
		}

		if !rs.IsUntracked() {
			trackedIndex++
		}
		printLeafStep(out, rs, indent, detailIndent, "", devboxBin)
	}
}

// printLeafStep renders a single (non-parallel) ResolvedStep. The optional indexPrefix
// (e.g. "[12/25] ") is inserted between the indent and the badge for parallel sub-steps;
// sequential leaf callers pass "".
func printLeafStep(out io.Writer, rs ResolvedStep, indent, detailIndent, indexPrefix, devboxBin string) {
	badge := stepBadge(rs.Step)
	name := rs.Step.Name
	desc := rs.Step.Description
	cmd := StepCommand(rs.Step, devboxBin)

	if desc != "" {
		_, _ = fmt.Fprintln(out, render.Definition(indexPrefix+badge+" "+name, desc, len(indent), ""))
	} else {
		_, _ = fmt.Fprintln(out, indent+indexPrefix+badge+" "+name)
	}
	if cmd != "" {
		_, _ = fmt.Fprintln(out, detailIndent+cmd)
	}
	if rs.RuntimeWhen != nil {
		_, _ = fmt.Fprintln(out, detailIndent+"[when: "+FormatCondition(rs.RuntimeWhen)+"]")
	}
	if rs.FilesGate != nil {
		_, _ = fmt.Fprintln(out, detailIndent+"["+FormatFilesGate(rs.FilesGate)+"]")
	}
	if rs.Step.Check != nil {
		_, _ = fmt.Fprintln(out, detailIndent+"[check: "+FormatAction(rs.Step.Check)+"]")
	}
	if rs.Step.ContinueOnError {
		_, _ = fmt.Fprintln(out, detailIndent+"[continue_on_error]")
	}
}

// computeTrackedTotal counts steps for the plan-table index display, mirroring the
// executor's trackedTotal computation: parallel groups contribute len(sub-steps);
// steps marked untracked (at phase or step level) are excluded.
func computeTrackedTotal(steps []ResolvedStep) int {
	total := 0
	for _, rs := range steps {
		if rs.IsUntracked() {
			continue
		}
		if rs.Parallel != nil {
			total += len(rs.Parallel.Steps)
			continue
		}
		total++
	}
	return total
}

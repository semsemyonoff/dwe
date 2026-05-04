package command

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
	pipeline "devbox-cli/internal/pipeline"
)

// TestPrintResetPlanShell_ContinueOnError checks that steps with ContinueOnError=true
// are emitted with "|| true" in the reset shell plan, and normal steps are not.
func TestPrintResetPlanShell_ContinueOnError(t *testing.T) {
	phase := config.DeployPhase{Name: "cleanup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: config.DeployStep{Name: "normal", Run: "echo ok"}},
		{Phase: phase, Step: config.DeployStep{Name: "optional-builtin", Builtin: "confirm", ContinueOnError: true}},
	}

	var buf strings.Builder
	printResetPlanShell(steps, &buf)
	out := buf.String()

	if !strings.Contains(out, "|| true") {
		t.Errorf("continue_on_error builtin step must have '|| true' in reset plan; got:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "echo ok") && strings.Contains(line, "|| true") {
			t.Errorf("normal step line must not contain '|| true': %q", line)
		}
	}
}

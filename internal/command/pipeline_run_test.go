package command

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
	pipeline "devbox-cli/internal/pipeline"
)

// TestPrintDeployPlanShell_ContinueOnError checks that steps with ContinueOnError=true
// are emitted with "|| true" in the deploy shell plan, and normal steps are not.
func TestPrintDeployPlanShell_ContinueOnError(t *testing.T) {
	phase := config.DeployPhase{Name: "hooks"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: config.DeployStep{Name: "normal", Run: "echo hello"}},
		{Phase: phase, Step: config.DeployStep{Name: "optional", Run: "echo bye", ContinueOnError: true}},
	}

	var buf strings.Builder
	printDeployPlanShell(steps, &buf)
	out := buf.String()

	if strings.Contains(out, "echo hello || true") {
		t.Error("normal step should not have '|| true'")
	}
	if !strings.Contains(out, "echo bye || true") {
		t.Errorf("continue_on_error step must have '|| true'; got:\n%s", out)
	}
	// Normal step must appear without || true.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "echo hello") && strings.Contains(line, "|| true") {
			t.Errorf("normal step line must not contain '|| true': %q", line)
		}
	}
}

// TestPrintResetPlanShell_ContinueOnError checks that steps with ContinueOnError=true
// are emitted with "|| true" in the reset shell plan.
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

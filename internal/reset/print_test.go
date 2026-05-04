package reset_test

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pipeline"
	"devbox-cli/internal/reset"
)

func TestPrintPlanShell_Empty(t *testing.T) {
	var buf bytes.Buffer
	reset.PrintPlanShell(nil, &buf, "devbox")
	out := buf.String()
	if !strings.Contains(out, "set -e") {
		t.Errorf("expected 'set -e' in output, got: %q", out)
	}
}

func TestPrintPlanShell_WithBuiltin(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "init"},
			Step:  config.DeployStep{Name: "ensure-dirs", Builtin: "service_dirs_ensure"},
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "devbox")
	out := buf.String()
	if !strings.Contains(out, "devbox reset step") {
		t.Errorf("expected builtin to delegate to CLI, got: %q", out)
	}
}

func TestPrintPlanShell_WithPhaseWhen(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "setup", When: "env.SKIP != true"},
			Step:  config.DeployStep{Name: "migrate", Run: "php artisan migrate"},
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "devbox")
	out := buf.String()
	if !strings.Contains(out, "# phase setup") {
		t.Errorf("expected phase comment in output, got: %q", out)
	}
}

func TestPrintPlanShell_WithRuntimeWhen(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase:       config.DeployPhase{Name: "setup"},
			Step:        config.DeployStep{Name: "migrate", Run: "php artisan migrate"},
			RuntimeWhen: "cmd:some-check",
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "devbox")
	out := buf.String()
	if !strings.Contains(out, "# when:") {
		t.Errorf("expected runtime when comment in output, got: %q", out)
	}
}

func TestPrintPlanShell_WithCheck(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "setup"},
			Step:  config.DeployStep{Name: "migrate", Run: "php artisan migrate", Check: "php artisan migrate:status"},
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "devbox")
	out := buf.String()
	if !strings.Contains(out, "# check:") {
		t.Errorf("expected check comment in output, got: %q", out)
	}
}

func TestPrintPlanShell_ContinueOnError(t *testing.T) {
	phase := config.DeployPhase{Name: "cleanup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: config.DeployStep{Name: "normal", Run: "echo ok"}},
		{Phase: phase, Step: config.DeployStep{Name: "optional-builtin", Builtin: "confirm", ContinueOnError: true}},
	}

	var buf strings.Builder
	reset.PrintPlanShell(steps, &buf, "devbox")
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

func TestPrintPlanShell_NoEnvSourceStep(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{Phase: phaseWith("cleanup"), Step: cmdStep("remove-dirs", "rm -rf services/main/src")},
		{Phase: phaseWith("cleanup"), Step: commandStep("reset-db", "services.main.db.create")},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "devbox")
	out := buf.String()
	if strings.Contains(out, ". .env") {
		t.Errorf("reset plan should not source .env, got:\n%s", out)
	}
}

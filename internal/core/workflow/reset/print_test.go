package reset_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

func TestPrintPlanShell_Empty(t *testing.T) {
	var buf bytes.Buffer
	reset.PrintPlanShell(nil, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "set -e") {
		t.Errorf("expected 'set -e' in output, got: %q", out)
	}
}

func TestPrintPlanShell_WithBuiltin(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "init"},
			Step:  config.DeployStep{Name: "ensure-dirs", Type: "builtin", Cmd: "service_dirs_ensure"},
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "dwe reset step") {
		t.Errorf("expected builtin to delegate to CLI, got: %q", out)
	}
}

func TestPrintPlanShell_WithPhaseWhen(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "setup", When: parseWhenString("env.SKIP != true")},
			Step:  config.DeployStep{Name: "migrate", Type: "shell", Cmd: "php artisan migrate"},
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "# phase setup") {
		t.Errorf("expected phase comment in output, got: %q", out)
	}
}

func TestPrintPlanShell_PrefersRenderedPhaseWhen(t *testing.T) {
	// The shell plan must show the rendered phase condition, not the raw
	// ${vars.*} text — the same divergence the table renderer already avoids.
	steps := []pipeline.ResolvedStep{
		{
			Phase:     config.DeployPhase{Name: "setup", When: parseWhenString("cmd:test -f ${vars.marker}")},
			PhaseWhen: parseWhenString("cmd:test -f /tmp/marker"),
			Step:      config.DeployStep{Name: "migrate", Type: "shell", Cmd: "php artisan migrate"},
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "/tmp/marker") {
		t.Errorf("expected rendered phase condition in output, got: %q", out)
	}
	if strings.Contains(out, "${vars.marker}") {
		t.Errorf("raw phase condition leaked into output: %q", out)
	}
}

func TestPrintPlanShell_WithRuntimeWhen(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase:       config.DeployPhase{Name: "setup"},
			Step:        config.DeployStep{Name: "migrate", Type: "shell", Cmd: "php artisan migrate"},
			RuntimeWhen: parseWhenString("cmd:some-check"),
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "# when:") {
		t.Errorf("expected runtime when comment in output, got: %q", out)
	}
}

func TestPrintPlanShell_WithCheck(t *testing.T) {
	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "setup"},
			Step: config.DeployStep{
				Name:  "migrate",
				Type:  "shell",
				Cmd:   "php artisan migrate",
				Check: &config.Action{Type: "shell", Cmd: "php artisan migrate:status"},
			},
		},
	}
	var buf bytes.Buffer
	reset.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "# check:") {
		t.Errorf("expected check comment in output, got: %q", out)
	}
}

func TestPrintPlanShell_ContinueOnError(t *testing.T) {
	phase := config.DeployPhase{Name: "cleanup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: config.DeployStep{Name: "normal", Type: "shell", Cmd: "echo ok"}},
		{Phase: phase, Step: config.DeployStep{Name: "optional-builtin", Type: "builtin", Cmd: "confirm", ContinueOnError: true}},
	}

	var buf strings.Builder
	reset.PrintPlanShell(steps, &buf, "dwe")
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
	reset.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if strings.Contains(out, ". .env") {
		t.Errorf("reset plan should not source .env, got:\n%s", out)
	}
}

// TestPrintPlanShell_redactsSecret is the reset twin of the deploy printer
// pin: the shell preview shows *** where a step references a secret.
func TestPrintPlanShell_redactsSecret(t *testing.T) {
	const plaintext = "w0rkflow-reset-print-secret"
	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)
	trace.RegisterRedaction([]string{plaintext})

	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{
			Phase: phaseWith("probe"),
			Step:  cmdStep("greet", "echo "+plaintext),
		},
	}
	reset.PrintPlanShell(steps, &buf, "dwe")

	out := buf.String()
	if strings.Contains(out, plaintext) {
		t.Errorf("shell plan carries the plaintext:\n%s", out)
	}
	if !strings.Contains(out, "echo ***") {
		t.Errorf("shell plan = %q, want the cmd redacted", out)
	}
}

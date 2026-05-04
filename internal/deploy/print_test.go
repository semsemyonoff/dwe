package deploy_test

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/pipeline"
	"devbox-cli/internal/render"
)

// --- PrintPlanShell tests ---

func TestPrintPlanShell_startsWithSetE(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
	}
	deploy.PrintPlanShell(steps, &buf)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if lines[0] != "set -e" {
		t.Errorf("first line = %q, want set -e", lines[0])
	}
}

func TestPrintPlanShell_cmdStepAsIs(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
		{Phase: phaseWith("setup"), Step: cmdStep("create-dirs", "mkdir -p services/main/src")},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()
	if !strings.Contains(out, "mkdir -p services/main/src") {
		t.Errorf("shell output missing cmd step, got: %q", out)
	}
}

func TestPrintPlanShell_commandStepAsDevboxRun(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: phaseWith("start"), Step: commandStep("migrate", "services.main.migrate")},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()
	if !strings.Contains(out, "./bin/devbox commands run services.main.migrate") {
		t.Errorf("shell output missing command step, got: %q", out)
	}
}

func TestPrintPlanShell_sourcesEnvAfterImplicitStep(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
		{Phase: phaseWith("setup"), Step: cmdStep("create-dirs", "mkdir -p services/main/src")},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")

	if lines[0] != "set -e" {
		t.Errorf("lines[0] = %q, want set -e", lines[0])
	}

	renderIdx := -1
	sourceIdx := -1
	mkdirIdx := -1
	for i, l := range lines {
		switch l {
		case pipeline.StepCommand(deploy.ImplicitEnvStep):
			renderIdx = i
		case ". .env":
			sourceIdx = i
		case "mkdir -p services/main/src":
			mkdirIdx = i
		}
	}
	if renderIdx < 0 {
		t.Fatalf("render-env command not found in output:\n%s", out)
	}
	if sourceIdx < 0 {
		t.Fatalf("'. .env' not found in output:\n%s", out)
	}
	if mkdirIdx < 0 {
		t.Fatalf("mkdir command not found in output:\n%s", out)
	}
	if renderIdx >= sourceIdx {
		t.Errorf("render-env (line %d) should appear before '. .env' (line %d)", renderIdx, sourceIdx)
	}
	if sourceIdx >= mkdirIdx {
		t.Errorf("'. .env' (line %d) should appear before mkdir (line %d)", sourceIdx, mkdirIdx)
	}
}

func TestPrintPlanShell_noEnvSourceForNonImplicitSteps(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: phaseWith("start"), Step: commandStep("up", "up")},
		{Phase: phaseWith("init"), Step: commandStep("migrate", "services.main.migrate")},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()
	if strings.Contains(out, ". .env") {
		t.Errorf("expected no '. .env' sourcing when implicit step is absent, got: %q", out)
	}
}

func TestPrintPlanShell_showsCheckComment(t *testing.T) {
	var buf bytes.Buffer

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
		{
			Phase: config.DeployPhase{Name: "setup"}, Step: checkStep("copy-configs", "./bin/devbox deploy config main --mode replace", "file-exists services/main/configs/.env"),
		},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()

	if !strings.Contains(out, "# check: file-exists services/main/configs/.env") {
		t.Errorf("shell plan missing check comment, got:\n%s", out)
	}
}

func TestPrintPlanShell_checkCommentAfterCommand(t *testing.T) {
	var buf bytes.Buffer

	step := checkStep("copy-configs", "echo copy", "file-exists services/main/configs/.env")
	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
		{Phase: config.DeployPhase{Name: "setup"}, Step: step},
	}
	deploy.PrintPlanShell(steps, &buf)
	lines := strings.Split(buf.String(), "\n")

	cmdIdx, checkIdx := -1, -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "echo copy" {
			cmdIdx = i
		}
		if strings.Contains(l, "# check:") {
			checkIdx = i
		}
	}
	if cmdIdx == -1 {
		t.Fatal("command line not found in shell output")
	}
	if checkIdx == -1 {
		t.Fatal("check comment not found in shell output")
	}
	if checkIdx <= cmdIdx {
		t.Errorf("check comment (line %d) should appear after command (line %d)", checkIdx, cmdIdx)
	}
}

func TestPrintPlanShell_showsPhaseWhenComment(t *testing.T) {
	var buf bytes.Buffer

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
		{
			Phase: config.DeployPhase{Name: "setup", When: "dir-empty services/main/src"}, Step: cmdStep("create-dirs", "mkdir"), PhaseWhen: "dir-empty services/main/src",
		},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()

	if !strings.Contains(out, "# phase setup [when: dir-empty services/main/src]") {
		t.Errorf("expected phase when comment in shell output, got:\n%s", out)
	}
}

func TestPrintPlanShell_stepWhenNotDuplicatedWhenSameAsPhase(t *testing.T) {
	var buf bytes.Buffer

	phase := config.DeployPhase{Name: "setup", When: "dir-empty services/main/src"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: cmdStep("create-dirs", "mkdir"), PhaseWhen: "dir-empty services/main/src"},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()

	if strings.Count(out, "# when:") != 0 {
		t.Errorf("step-level when comment should not appear for phase-only condition, got:\n%s", out)
	}
	if !strings.Contains(out, "# phase setup [when: dir-empty services/main/src]") {
		t.Errorf("phase when comment missing:\n%s", out)
	}
}

func TestPrintPlanShell_runtimeWhenComment(t *testing.T) {
	var buf bytes.Buffer

	phase := config.DeployPhase{Name: "setup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: runtimeWhenStep("install", "make -f Makefile app-install", "dir-empty services/main/src"), RuntimeWhen: "dir-empty services/main/src"},
		{Phase: phase, Step: cmdStep("always", "echo always")},
	}
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()

	if !strings.Contains(out, "# when: dir-empty services/main/src") {
		t.Errorf("expected '# when:' comment in shell output, got:\n%s", out)
	}
	if strings.Count(out, "# when:") != 1 {
		t.Errorf("expected exactly 1 when comment, got:\n%s", out)
	}
}

func TestPrintPlanShell_ContinueOnError(t *testing.T) {
	phase := config.DeployPhase{Name: "hooks"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: config.DeployStep{Name: "normal", Run: "echo hello"}},
		{Phase: phase, Step: config.DeployStep{Name: "optional", Run: "echo bye", ContinueOnError: true}},
	}

	var buf strings.Builder
	deploy.PrintPlanShell(steps, &buf)
	out := buf.String()

	if strings.Contains(out, "echo hello || true") {
		t.Error("normal step should not have '|| true'")
	}
	if !strings.Contains(out, "echo bye || true") {
		t.Errorf("continue_on_error step must have '|| true'; got:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "echo hello") && strings.Contains(line, "|| true") {
			t.Errorf("normal step line must not contain '|| true': %q", line)
		}
	}
}

// --- pipeline.PrintPlanTable tests ---

func TestPrintPlanTable_showsPhaseHeader(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env", Description: "Environment"}, Step: deploy.ImplicitEnvStep},
		{Phase: config.DeployPhase{Name: "setup", Description: "Setup phase"}, Step: cmdStep("create-dirs", "mkdir")},
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "env: Environment") {
		t.Errorf("expected 'env: Environment' in output, got: %s", out)
	}
	if !strings.Contains(out, "setup: Setup phase") {
		t.Errorf("expected 'setup: Setup phase' in output, got: %s", out)
	}
}

func TestPrintPlanTable_showsStepBadgeAndName(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "start"}, Step: commandStep("up", "up")},
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[command]") {
		t.Errorf("expected '[command]' badge in table output, got: %s", out)
	}
	if !strings.Contains(out, "up") {
		t.Errorf("expected step name 'up' in table output, got: %s", out)
	}
}

func TestPrintPlanTable_showsImplicitStepFirst(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("step1", "echo 1")),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "render-env") {
		t.Errorf("expected implicit render-env step in table output, got: %s", out)
	}
}

func TestPrintPlanTable_showsCheck(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "setup"},
			Step:  checkStep("copy-configs", "./bin/devbox deploy config main --mode replace", "file-exists services/main/configs/.env"),
		},
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[check: file-exists services/main/configs/.env]") {
		t.Errorf("plan table missing check annotation, got:\n%s", out)
	}
}

func TestPrintPlanTable_samePhaseNotRepeated(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	phase := config.DeployPhase{Name: "setup", Description: "Setup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: cmdStep("step1", "cmd1")},
		{Phase: phase, Step: cmdStep("step2", "cmd2")},
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	count := strings.Count(out, "setup: Setup")
	if count != 1 {
		t.Errorf("phase header 'setup: Setup' appeared %d times, want 1", count)
	}
}

func TestPrintPlanTable_showsPhaseWhenInHeader(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{Name: "setup", Description: "Setup", When: "dir-empty services/main/src"}, Step: cmdStep("create-dirs", "mkdir"),
		},
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[when: dir-empty services/main/src]") {
		t.Errorf("expected phase when annotation in header, got:\n%s", out)
	}
}

func TestPrintPlanTable_showsRuntimeWhenAnnotation(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	phase := config.DeployPhase{Name: "setup", Description: "Setup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: runtimeWhenStep("install", "make app-install", "dir-empty services/main/src"), RuntimeWhen: "dir-empty services/main/src"},
		{Phase: phase, Step: cmdStep("always", "echo always")},
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[when: dir-empty services/main/src]") {
		t.Errorf("expected runtime when annotation in output, got:\n%s", out)
	}
	if strings.Contains(out, "[when: ]") {
		t.Errorf("unexpected empty when annotation in output:\n%s", out)
	}
}

func TestPrintPlanTable_serviceStepsIndented(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env", Description: "Environment"}, Step: deploy.ImplicitEnvStep},
		{Phase: config.DeployPhase{Name: "setup", Description: "Setup"}, Step: cmdStep("create-dirs", "mkdir"), Service: "main"},
		{Phase: config.DeployPhase{Name: "start", Description: "Start"}, Step: commandStep("up", "up")},
	}
	pipeline.PrintPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "service: main") {
		t.Errorf("expected 'service: main' header, got:\n%s", out)
	}
	if !strings.Contains(out, "main/setup") {
		t.Errorf("expected 'main/setup' phase, got:\n%s", out)
	}
}

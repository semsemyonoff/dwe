package deploy_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// --- PrintPlanShell tests ---

func TestPrintPlanShell_startsWithSetE(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
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
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "mkdir -p services/main/src") {
		t.Errorf("shell output missing cmd step, got: %q", out)
	}
}

func TestPrintPlanShell_commandStepAsDweRun(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: phaseWith("start"), Step: commandStep("migrate", "services.main.migrate")},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()
	if !strings.Contains(out, "dwe commands run services.main.migrate") {
		t.Errorf("shell output missing command step, got: %q", out)
	}
}

func TestPrintPlanShell_sourcesEnvAfterImplicitStep(t *testing.T) {
	var buf bytes.Buffer
	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "env"}, Step: deploy.ImplicitEnvStep},
		{Phase: phaseWith("setup"), Step: cmdStep("create-dirs", "mkdir -p services/main/src")},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
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
		case pipeline.StepCommand(deploy.ImplicitEnvStep, "dwe"):
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
	deploy.PrintPlanShell(steps, &buf, "dwe")
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
			Phase: config.DeployPhase{Name: "setup"}, Step: checkStep("copy-configs", "./bin/dwe deploy config main --mode replace", "file-exists services/main/configs/.env"),
		},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()

	if !strings.Contains(out, "# check: builtin file-exists services/main/configs/.env") {
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
	deploy.PrintPlanShell(steps, &buf, "dwe")
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
			Phase: config.DeployPhase{Name: "setup", When: parseWhenString("dir-empty services/main/src")}, Step: cmdStep("create-dirs", "mkdir"), PhaseWhen: parseWhenString("dir-empty services/main/src"),
		},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()

	if !strings.Contains(out, "# phase setup [when: builtin dir-empty services/main/src]") {
		t.Errorf("expected phase when comment in shell output, got:\n%s", out)
	}
}

// TestPrintPlanShell_phaseWhenPrintsRenderedForm pins that the shell plan
// prints the RENDERED phase condition (PhaseWhen) rather than the raw
// Phase.When it was rendered from — the same stale-literal divergence the
// human and JSON plan renderers avoid via DisplayPhaseWhen.
func TestPrintPlanShell_phaseWhenPrintsRenderedForm(t *testing.T) {
	var buf bytes.Buffer

	steps := []pipeline.ResolvedStep{
		{
			Phase:     config.DeployPhase{Name: "setup", When: parseWhenString("dir-empty ${vars.src}")},
			Step:      cmdStep("create-dirs", "mkdir"),
			PhaseWhen: parseWhenString("dir-empty services/main/src"),
		},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()

	if !strings.Contains(out, "# phase setup [when: builtin dir-empty services/main/src]") {
		t.Errorf("expected rendered phase when comment, got:\n%s", out)
	}
	if strings.Contains(out, "${vars.src}") {
		t.Errorf("shell plan printed the unrendered phase when, got:\n%s", out)
	}
}

func TestPrintPlanShell_stepWhenNotDuplicatedWhenSameAsPhase(t *testing.T) {
	var buf bytes.Buffer

	phase := config.DeployPhase{Name: "setup", When: parseWhenString("dir-empty services/main/src")}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: cmdStep("create-dirs", "mkdir"), PhaseWhen: parseWhenString("dir-empty services/main/src")},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()

	if strings.Count(out, "# when:") != 0 {
		t.Errorf("step-level when comment should not appear for phase-only condition, got:\n%s", out)
	}
	if !strings.Contains(out, "# phase setup [when: builtin dir-empty services/main/src]") {
		t.Errorf("phase when comment missing:\n%s", out)
	}
}

func TestPrintPlanShell_runtimeWhenComment(t *testing.T) {
	var buf bytes.Buffer

	phase := config.DeployPhase{Name: "setup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: runtimeWhenStep("install", "make -f Makefile app-install", "dir-empty services/main/src"), RuntimeWhen: parseWhenString("dir-empty services/main/src")},
		{Phase: phase, Step: cmdStep("always", "echo always")},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()

	if !strings.Contains(out, "# when: builtin dir-empty services/main/src") {
		t.Errorf("expected '# when:' comment in shell output, got:\n%s", out)
	}
	if strings.Count(out, "# when:") != 1 {
		t.Errorf("expected exactly 1 when comment, got:\n%s", out)
	}
}

func TestPrintPlanShell_ContinueOnError(t *testing.T) {
	phase := config.DeployPhase{Name: "hooks"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: config.DeployStep{Name: "normal", Type: "shell", Cmd: "echo hello"}},
		{Phase: phase, Step: config.DeployStep{Name: "optional", Type: "shell", Cmd: "echo bye", ContinueOnError: true}},
	}

	var buf strings.Builder
	deploy.PrintPlanShell(steps, &buf, "dwe")
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
	pipeline.PrintPlanTable(steps, w, "dwe")
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
	pipeline.PrintPlanTable(steps, w, "dwe")
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
	steps, err := deploy.ResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pipeline.PrintPlanTable(steps, w, "dwe")
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
			Step:  checkStep("copy-configs", "./bin/dwe deploy config main --mode replace", "file-exists services/main/configs/.env"),
		},
	}
	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	if !strings.Contains(out, "[check: builtin file-exists services/main/configs/.env]") {
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
	pipeline.PrintPlanTable(steps, w, "dwe")
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
			Phase: config.DeployPhase{Name: "setup", Description: "Setup", When: parseWhenString("dir-empty services/main/src")}, Step: cmdStep("create-dirs", "mkdir"),
		},
	}
	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	if !strings.Contains(out, "[when: builtin dir-empty services/main/src]") {
		t.Errorf("expected phase when annotation in header, got:\n%s", out)
	}
}

func TestPrintPlanTable_showsRuntimeWhenAnnotation(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	phase := config.DeployPhase{Name: "setup", Description: "Setup"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: runtimeWhenStep("install", "make app-install", "dir-empty services/main/src"), RuntimeWhen: parseWhenString("dir-empty services/main/src")},
		{Phase: phase, Step: cmdStep("always", "echo always")},
	}
	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	if !strings.Contains(out, "[when: builtin dir-empty services/main/src]") {
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
	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	if !strings.Contains(out, "service: main") {
		t.Errorf("expected 'service: main' header, got:\n%s", out)
	}
	if !strings.Contains(out, "main/setup") {
		t.Errorf("expected 'main/setup' phase, got:\n%s", out)
	}
}

func TestPrintPlanTable_binarySubstitution(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "start"}, Step: commandStep("migrate", "services.main.migrate")},
	}
	pipeline.PrintPlanTable(steps, w, "my-dwe")
	out := buf.String()

	if !strings.Contains(out, "my-dwe") {
		t.Errorf("expected 'my-dwe' binary name in table output, got:\n%s", out)
	}
}

func TestPrintPlanTable_showsUnresolvedTemplateAnnotation(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "setup"}, Step: cmdStep("greet", "echo ${HOME}")},
	}
	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	if !strings.Contains(out, "[unresolved: ${HOME}]") {
		t.Errorf("expected unresolved-template annotation in table output, got:\n%s", out)
	}
}

func TestPrintPlanTable_noUnresolvedAnnotationForPlainCommand(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "setup"}, Step: cmdStep("greet", "echo hello")},
	}
	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	if strings.Contains(out, "[unresolved:") {
		t.Errorf("did not expect unresolved-template annotation, got:\n%s", out)
	}
}

func TestPrintPlanShell_noUnresolvedAnnotation(t *testing.T) {
	var buf bytes.Buffer

	steps := []pipeline.ResolvedStep{
		{Phase: phaseWith("setup"), Step: cmdStep("greet", "echo ${HOME}")},
	}
	deploy.PrintPlanShell(steps, &buf, "dwe")
	out := buf.String()

	if strings.Contains(out, "[unresolved:") {
		t.Errorf("shell format must stay executable, no annotation expected, got:\n%s", out)
	}
	if !strings.Contains(out, "echo ${HOME}") {
		t.Errorf("shell output missing raw command, got:\n%s", out)
	}
}

func TestPrintPlanTable_parallelGroupRendersHeaderAndSubSteps(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	phase := config.DeployPhase{Name: "init", Description: "Init"}
	steps := []pipeline.ResolvedStep{
		{Phase: phase, Step: cmdStep("pre", "echo pre")},
		{
			Phase: phase,
			Step:  config.DeployStep{Name: "db-dumps"},
			Parallel: &pipeline.ResolvedParallel{
				MaxConcurrent: 4,
				FailFast:      true,
				Steps: []pipeline.ResolvedStep{
					{Phase: phase, Step: commandStep("download-main", "services.main.db.dump-download")},
					{Phase: phase, Step: commandStep("download-stock", "services.stock.db.dump-download")},
					{Phase: phase, Step: commandStep("download-price", "services.price.db.dump-download")},
				},
			},
		},
		{Phase: phase, Step: cmdStep("post", "echo post")},
	}

	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	// Group header: total = 1 (pre) + 3 (sub-steps) + 1 (post) = 5; group occupies [2-4/5]
	if !strings.Contains(out, "[2-4/5]") {
		t.Errorf("expected group index range '[2-4/5]' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "parallel group: db-dumps") {
		t.Errorf("expected 'parallel group: db-dumps' header, got:\n%s", out)
	}
	if !strings.Contains(out, "3 steps") || !strings.Contains(out, "max_concurrent=4") || !strings.Contains(out, "fail_fast=true") {
		t.Errorf("expected group meta '(3 steps, max_concurrent=4, fail_fast=true)' in header, got:\n%s", out)
	}
	for _, want := range []string{"[2/5]", "[3/5]", "[4/5]"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected sub-step index %s in output, got:\n%s", want, out)
		}
	}
	for _, want := range []string{"download-main", "download-stock", "download-price"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected sub-step name %q in output, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "dwe commands run services.main.db.dump-download") {
		t.Errorf("expected resolved sub-step command in output, got:\n%s", out)
	}

	// Group header must appear before sub-steps; sub-steps in declaration order.
	idxHeader := strings.Index(out, "parallel group: db-dumps")
	idxMain := strings.Index(out, "download-main")
	idxStock := strings.Index(out, "download-stock")
	idxPrice := strings.Index(out, "download-price")
	if idxHeader >= idxMain || idxMain >= idxStock || idxStock >= idxPrice {
		t.Errorf("expected header < main < stock < price ordering, got header=%d main=%d stock=%d price=%d", idxHeader, idxMain, idxStock, idxPrice)
	}
}

func TestPrintPlanTable_parallelGroupOnly(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	phase := config.DeployPhase{Name: "init"}
	steps := []pipeline.ResolvedStep{
		{
			Phase: phase,
			Step:  config.DeployStep{Name: "group"},
			Parallel: &pipeline.ResolvedParallel{
				MaxConcurrent: 2,
				FailFast:      false,
				Steps: []pipeline.ResolvedStep{
					{Phase: phase, Step: cmdStep("a", "echo a")},
					{Phase: phase, Step: cmdStep("b", "echo b")},
				},
			},
		},
	}

	pipeline.PrintPlanTable(steps, w, "dwe")
	out := buf.String()

	if !strings.Contains(out, "[1-2/2]") {
		t.Errorf("expected '[1-2/2]' group range, got:\n%s", out)
	}
	if !strings.Contains(out, "fail_fast=false") {
		t.Errorf("expected 'fail_fast=false' in header, got:\n%s", out)
	}
	if !strings.Contains(out, "[1/2]") || !strings.Contains(out, "[2/2]") {
		t.Errorf("expected sub-step indices [1/2] and [2/2], got:\n%s", out)
	}
}

func TestPrintPlanShell_binarySubstitution(t *testing.T) {
	var buf bytes.Buffer

	steps := []pipeline.ResolvedStep{
		{Phase: config.DeployPhase{Name: "start"}, Step: commandStep("migrate", "services.main.migrate")},
		{Phase: config.DeployPhase{Name: "setup"}, Step: config.DeployStep{Name: "ensure", Type: "builtin", Cmd: "service_dirs_ensure"}},
	}
	deploy.PrintPlanShell(steps, &buf, "podman-dwe")
	out := buf.String()

	if !strings.Contains(out, "podman-dwe commands run services.main.migrate") {
		t.Errorf("expected 'podman-dwe commands run' in shell output, got:\n%s", out)
	}
	if !strings.Contains(out, "podman-dwe deploy step") {
		t.Errorf("expected 'podman-dwe deploy step' for builtin in shell output, got:\n%s", out)
	}
}

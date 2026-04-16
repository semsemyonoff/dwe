package command

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// makeDeployCfg returns a DevboxConfig with the given deploy phases.
func makeDeployCfg(phases []config.DeployPhase) *config.DevboxConfig {
	return &config.DevboxConfig{
		Deploy: config.DeployConfig{Phases: phases},
		Raw:    map[string]any{"__configPath": "/tmp/devbox.yml"},
	}
}

// phaseWith builds a DeployPhase with the given name and steps.
func phaseWith(name string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", Steps: steps}
}

// cmdStep builds a cmd-type step.
func cmdStep(name, cmd string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description"}
}

// commandStep builds a command-type step.
func commandStep(name, id string) config.DeployStep {
	return config.DeployStep{Name: name, Command: id, Description: name + " description"}
}

// commandStepWith builds a command-type step with param overrides.
func commandStepWith(name, id string, with map[string]any) config.DeployStep {
	return config.DeployStep{Name: name, Command: id, With: with, Description: name + " description"}
}

// whenStep builds a cmd-type step with a when condition.
func whenStep(name, cmd, when string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description", When: when}
}

// phaseWithWhen builds a DeployPhase with a when condition.
func phaseWithWhen(name, when string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", When: when, Steps: steps}
}

// --- resolveDeployPlan tests ---

func TestResolveDeployPlan_implicitEnvStepAlwaysFirst(t *testing.T) {
	cfg := makeDeployCfg(nil)
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("want 1 step (implicit), got %d", len(steps))
	}
	if steps[0].step.Name != "render-env" {
		t.Errorf("first step name = %q, want render-env", steps[0].step.Name)
	}
	if steps[0].step.Devbox != "render env -o .env" {
		t.Errorf("first step devbox = %q, want render env -o .env", steps[0].step.Devbox)
	}
}

func TestResolveDeployPlan_emptyPhasesOnlyImplicit(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Errorf("want 1 (implicit only), got %d", len(steps))
	}
}

func TestResolveDeployPlan_noWhenAlwaysIncluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("create-dirs", "mkdir -p services/main/src"),
			commandStep("up", "up"),
		),
	})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + 2 steps
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[1].step.Name != "create-dirs" {
		t.Errorf("steps[1].name = %q, want create-dirs", steps[1].step.Name)
	}
	if steps[2].step.Name != "up" {
		t.Errorf("steps[2].name = %q, want up", steps[2].step.Name)
	}
}

func TestResolveDeployPlan_truthyWhenIncluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("debug-step", "echo debug", "{{.Runtime.UseHTTPS}}"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{
		UseHTTPS: true,
	}
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + 1 step
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].step.Name != "debug-step" {
		t.Errorf("expected debug-step included, got %q", steps[1].step.Name)
	}
}

func TestResolveDeployPlan_falsyWhenExcluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("always", "echo always"),
			whenStep("conditional", "echo only-if-debug", "{{.Runtime.UseHTTPS}}"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{
		UseHTTPS: false,
	}
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + always (conditional excluded)
	if len(steps) != 2 {
		t.Fatalf("want 2 steps (implicit + always), got %d: %v", len(steps), steps)
	}
	for _, rs := range steps {
		if rs.step.Name == "conditional" {
			t.Error("conditional step should have been excluded")
		}
	}
}

func TestResolveDeployPlan_multiplePhasesPreserveOrder(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("s1", "cmd1"), cmdStep("s2", "cmd2")),
		phaseWith("init", commandStep("m1", "services.main.cmd1"), commandStep("m2", "services.main.cmd2")),
	})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + 4 steps
	if len(steps) != 5 {
		t.Fatalf("want 5 steps, got %d", len(steps))
	}
	wantNames := []string{"render-env", "s1", "s2", "m1", "m2"}
	for i, want := range wantNames {
		if steps[i].step.Name != want {
			t.Errorf("steps[%d].name = %q, want %q", i, steps[i].step.Name, want)
		}
	}
}

// --- stepBadge tests ---

func TestStepBadge_cmdStep(t *testing.T) {
	s := config.DeployStep{Run: "echo hello"}
	if got := stepBadge(s); got != "[run]" {
		t.Errorf("got %q, want [run]", got)
	}
}

func TestStepBadge_commandStep(t *testing.T) {
	s := config.DeployStep{Command: "services.main.migrate"}
	if got := stepBadge(s); got != "[command]" {
		t.Errorf("got %q, want [command]", got)
	}
}

// --- stepCommand tests ---

func TestStepCommand_cmdReturnsRaw(t *testing.T) {
	s := config.DeployStep{Run: "mkdir -p services/main/src"}
	if got := stepCommand(s); got != "mkdir -p services/main/src" {
		t.Errorf("got %q, want raw cmd", got)
	}
}

func TestStepCommand_commandReturnsDevboxRunCmd(t *testing.T) {
	s := config.DeployStep{Command: "services.main.migrate"}
	if got := stepCommand(s); got != "./bin/devbox commands run services.main.migrate" {
		t.Errorf("got %q, want './bin/devbox commands run services.main.migrate'", got)
	}
}

// --- printDeployPlanShell tests ---

func TestPrintDeployPlanShell_startsWithSetE(t *testing.T) {
	var buf bytes.Buffer
	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env"}, step: implicitEnvStep},
	}
	printDeployPlanShell(steps, &buf)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if lines[0] != "set -e" {
		t.Errorf("first line = %q, want set -e", lines[0])
	}
}

func TestPrintDeployPlanShell_cmdStepAsIs(t *testing.T) {
	var buf bytes.Buffer
	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env"}, step: implicitEnvStep},
		{phase: phaseWith("setup"), step: cmdStep("create-dirs", "mkdir -p services/main/src")},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()
	if !strings.Contains(out, "mkdir -p services/main/src") {
		t.Errorf("shell output missing cmd step, got: %q", out)
	}
}

func TestPrintDeployPlanShell_commandStepAsDevboxRun(t *testing.T) {
	var buf bytes.Buffer
	steps := []resolvedStep{
		{phase: phaseWith("start"), step: commandStep("migrate", "services.main.migrate")},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()
	if !strings.Contains(out, "./bin/devbox commands run services.main.migrate") {
		t.Errorf("shell output missing command step, got: %q", out)
	}
}

func TestPrintDeployPlanShell_sourcesEnvAfterImplicitStep(t *testing.T) {
	var buf bytes.Buffer
	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env"}, step: implicitEnvStep},
		{phase: phaseWith("setup"), step: cmdStep("create-dirs", "mkdir -p services/main/src")},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")

	if lines[0] != "set -e" {
		t.Errorf("lines[0] = %q, want set -e", lines[0])
	}

	// The render-env command must appear before ". .env", and ". .env" before the setup cmd.
	renderIdx := -1
	sourceIdx := -1
	mkdirIdx := -1
	for i, l := range lines {
		switch l {
		case stepCommand(implicitEnvStep):
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

func TestPrintDeployPlanShell_noEnvSourceForNonImplicitSteps(t *testing.T) {
	var buf bytes.Buffer
	steps := []resolvedStep{
		{phase: phaseWith("start"), step: commandStep("up", "up")},
		{phase: phaseWith("init"), step: commandStep("migrate", "services.main.migrate")},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()
	if strings.Contains(out, ". .env") {
		t.Errorf("expected no '. .env' sourcing when implicit step is absent, got: %q", out)
	}
}

// --- printDeployPlanTable tests ---

func TestPrintDeployPlanTable_showsPhaseHeader(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env", Description: "Environment"}, step: implicitEnvStep},
		{phase: config.DeployPhase{Name: "setup", Description: "Setup phase"}, step: cmdStep("create-dirs", "mkdir")},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "env: Environment") {
		t.Errorf("expected 'env: Environment' in output, got: %s", out)
	}
	if !strings.Contains(out, "setup: Setup phase") {
		t.Errorf("expected 'setup: Setup phase' in output, got: %s", out)
	}
}

func TestPrintDeployPlanTable_showsStepBadgeAndName(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "start"}, step: commandStep("up", "up")},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[command]") {
		t.Errorf("expected '[command]' badge in table output, got: %s", out)
	}
	if !strings.Contains(out, "up") {
		t.Errorf("expected step name 'up' in table output, got: %s", out)
	}
}

func TestPrintDeployPlanTable_showsImplicitStepFirst(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("step1", "echo 1")),
	})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	// The implicit step's command should appear
	if !strings.Contains(out, "render-env") {
		t.Errorf("expected implicit render-env step in table output, got: %s", out)
	}
}

// --- findStep tests ---

func TestFindStep_findsExistingStep(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("create-dirs", "mkdir -p services/main/src")),
		phaseWith("init", commandStep("migrate", "services.main.migrate")),
	})

	phase, step, err := findStep(cfg, "init/migrate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if phase.Name != "init" {
		t.Errorf("phase.Name = %q, want init", phase.Name)
	}
	if step.Name != "migrate" {
		t.Errorf("step.Name = %q, want migrate", step.Name)
	}
	if step.Command != "services.main.migrate" {
		t.Errorf("step.Command = %q, want services.main.migrate", step.Command)
	}
}

func TestFindStep_stepNotFound(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("create-dirs", "mkdir -p services/main/src")),
	})

	_, _, err := findStep(cfg, "setup/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing step, got nil")
	}
}

func TestFindStep_phaseNotFound(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("create-dirs", "mkdir -p services/main/src")),
	})

	_, _, err := findStep(cfg, "nonexistent/create-dirs")
	if err == nil {
		t.Fatal("expected error for missing phase, got nil")
	}
}

func TestFindStep_invalidAddress(t *testing.T) {
	cfg := makeDeployCfg(nil)

	cases := []string{"noslash", "/noname", "nophase/", ""}
	for _, addr := range cases {
		_, _, err := findStep(cfg, addr)
		if err == nil {
			t.Errorf("address %q: expected error, got nil", addr)
		}
	}
}

// --- stepCommand dry-run output tests ---

func TestDeployStep_dryRunPrintsCmdCommand(t *testing.T) {
	step := cmdStep("create-dirs", "mkdir -p services/main/src")
	got := stepCommand(step)
	if got != "mkdir -p services/main/src" {
		t.Errorf("dry-run cmd output = %q, want raw command", got)
	}
}

func TestDeployStep_dryRunPrintsCommandRef(t *testing.T) {
	step := commandStep("migrate", "services.main.migrate")
	got := stepCommand(step)
	if got != "./bin/devbox commands run services.main.migrate" {
		t.Errorf("dry-run command output = %q, want './bin/devbox commands run services.main.migrate'", got)
	}
}

// --- findStep + when condition tests ---

func TestFindStep_whenFalseIsSkippedByCallerLogic(t *testing.T) {
	// Verify the when field is preserved on the returned step so the caller can evaluate it.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("conditional", "echo hi", "{{.Runtime.UseHTTPS}}"),
		),
	})
	_, step, err := findStep(cfg, "setup/conditional")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if step.When == "" {
		t.Error("expected step to carry When field, got empty")
	}
}

func TestResolveDeployPlan_invalidWhenTemplate(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("bad", "echo hi", "{{ unclosed"),
		),
	})
	_, err := resolveDeployPlan(cfg)
	if err == nil {
		t.Error("expected error for malformed when template, got nil")
	}
}

// --- stepCommand for all types ---

func TestStepCommand_allTypes(t *testing.T) {
	cases := []struct {
		step config.DeployStep
		want string
	}{
		{config.DeployStep{Run: "echo hello"}, "echo hello"},
		{config.DeployStep{Command: "services.main.migrate"}, "./bin/devbox commands run services.main.migrate"},
		{config.DeployStep{Command: "  services.main.migrate  "}, "./bin/devbox commands run services.main.migrate"},
		{config.DeployStep{Run: "  mkdir -p x  "}, "mkdir -p x"},
		{config.DeployStep{Devbox: "docker down"}, "./bin/devbox docker down"},
		{config.DeployStep{Builtin: "service_configs_copy", With: map[string]any{"service": "main", "mode": "replace"}},
			`builtin: service_configs_copy(service=main, mode=replace)`},
		{config.DeployStep{Builtin: "docker_remove_project_volumes"},
			`builtin: docker_remove_project_volumes()`},
		{config.DeployStep{Builtin: "remove_paths", With: map[string]any{"paths": []any{"services/"}}},
			`builtin: remove_paths(paths=[services/])`},
	}
	for _, tc := range cases {
		got := stepCommand(tc.step)
		if got != tc.want {
			t.Errorf("stepCommand(%+v) = %q, want %q", tc.step, got, tc.want)
		}
	}
}

func TestStepCommand_commandWithWith(t *testing.T) {
	step := config.DeployStep{
		Command: "services.main.migrate",
		With:    map[string]any{"db": "mydb"},
	}
	got := stepCommand(step)
	want := "./bin/devbox commands run services.main.migrate --set db=mydb"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Verify stepBadge for all step types.
func TestStepBadge_allTypes(t *testing.T) {
	if got := stepBadge(config.DeployStep{Run: "x"}); got != "[run]" {
		t.Errorf("got %q want [run]", got)
	}
	if got := stepBadge(config.DeployStep{Command: "x"}); got != "[command]" {
		t.Errorf("got %q want [command]", got)
	}
	if got := stepBadge(config.DeployStep{Devbox: "docker down"}); got != "[devbox]" {
		t.Errorf("got %q want [devbox]", got)
	}
	if got := stepBadge(config.DeployStep{Builtin: "service_configs_copy"}); got != "[builtin]" {
		t.Errorf("got %q want [builtin]", got)
	}
}

// --- check field tests ---

// checkStep builds a cmd-type step with a check postcondition.
func checkStep(name, cmd, check string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description", Check: check}
}

// TestDeployStep_checkPreservedInConfig verifies the check field round-trips through YAML.
func TestDeployStep_checkPreservedInConfig(t *testing.T) {
	step := config.DeployStep{
		Name:  "copy-configs",
		Check: "file-exists services/main/configs/.env",
	}
	if step.Check != "file-exists services/main/configs/.env" {
		t.Errorf("Check = %q, want file-exists ...", step.Check)
	}
}

// TestPrintDeployPlanTable_showsCheck verifies [check: ...] appears in plan table output.
func TestPrintDeployPlanTable_showsCheck(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []resolvedStep{
		{
			phase: config.DeployPhase{Name: "setup"},
			step:  checkStep("copy-configs", "./bin/devbox deploy config main --mode replace", "file-exists services/main/configs/.env"),
		},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[check: file-exists services/main/configs/.env]") {
		t.Errorf("plan table missing check annotation, got:\n%s", out)
	}
}

// TestPrintDeployPlanShell_showsCheckComment verifies # check: ... appears in shell output.
func TestPrintDeployPlanShell_showsCheckComment(t *testing.T) {
	var buf bytes.Buffer

	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env"}, step: implicitEnvStep},
		{
			phase: config.DeployPhase{Name: "setup"},
			step:  checkStep("copy-configs", "./bin/devbox deploy config main --mode replace", "file-exists services/main/configs/.env"),
		},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()

	if !strings.Contains(out, "# check: file-exists services/main/configs/.env") {
		t.Errorf("shell plan missing check comment, got:\n%s", out)
	}
}

// TestPrintDeployPlanShell_checkCommentAfterCommand verifies check appears after the step command.
func TestPrintDeployPlanShell_checkCommentAfterCommand(t *testing.T) {
	var buf bytes.Buffer

	step := checkStep("copy-configs", "echo copy", "file-exists services/main/configs/.env")
	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env"}, step: implicitEnvStep},
		{phase: config.DeployPhase{Name: "setup"}, step: step},
	}
	printDeployPlanShell(steps, &buf)
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

// Ensure findStep returns first matching step in order.
func TestFindStep_firstMatchReturned(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("a", "echo a"),
			cmdStep("b", "echo b"),
			cmdStep("c", "echo c"),
		),
	})
	for _, name := range []string{"a", "b", "c"} {
		_, step, err := findStep(cfg, fmt.Sprintf("setup/%s", name))
		if err != nil {
			t.Fatalf("findStep setup/%s: %v", name, err)
		}
		if step.Name != name {
			t.Errorf("step.Name = %q, want %q", step.Name, name)
		}
	}
}

func TestPrintDeployPlanTable_samePhaseNotRepeated(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	phase := config.DeployPhase{Name: "setup", Description: "Setup"}
	steps := []resolvedStep{
		{phase: phase, step: cmdStep("step1", "cmd1")},
		{phase: phase, step: cmdStep("step2", "cmd2")},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	// Phase header should appear only once
	count := strings.Count(out, "setup: Setup")
	if count != 1 {
		t.Errorf("phase header 'setup: Setup' appeared %d times, want 1", count)
	}
}

// --- copyConfigFile / updateEnvFile tests ---

func TestCopyConfigFile_defaultSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	if err := os.WriteFile(src, []byte("KEY=newvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Destination already exists with different value.
	if err := os.WriteFile(dest, []byte("KEY=oldvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "default"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "KEY=oldvalue\n" {
		t.Errorf("default mode: dest was overwritten, got %q", string(got))
	}
}

func TestCopyConfigFile_defaultCreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "subdir", "dest.env")

	if err := os.WriteFile(src, []byte("KEY=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "default"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "KEY=value\n" {
		t.Errorf("default mode: expected src content, got %q", string(got))
	}
}

func TestCopyConfigFile_replaceOverwrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	if err := os.WriteFile(src, []byte("KEY=newvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("KEY=oldvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "replace"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "KEY=newvalue\n" {
		t.Errorf("replace mode: expected new value, got %q", string(got))
	}
}

func TestCopyConfigFile_updateMergesNewKeys(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	srcContent := "EXISTING=srcval\nNEW_KEY=newval\n"
	destContent := "EXISTING=destval\n"

	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(destContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	result := string(got)

	// Existing key must keep dest value.
	if strings.Contains(result, "EXISTING=srcval") {
		t.Errorf("update mode: EXISTING was overwritten with src value: %q", result)
	}
	if !strings.Contains(result, "EXISTING=destval") {
		t.Errorf("update mode: EXISTING dest value not preserved: %q", result)
	}
	// New key must be appended.
	if !strings.Contains(result, "NEW_KEY=newval") {
		t.Errorf("update mode: NEW_KEY not appended: %q", result)
	}
}

func TestCopyConfigFile_updatePreservesExistingValues(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	// Source has old defaults; dest has user-customized values — none should change.
	srcContent := "DB_HOST=db\nAPP_KEY=\n"
	destContent := "DB_HOST=mydb\nAPP_KEY=base64:secret\nEXTRA=custom\n"

	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(destContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	result := string(got)

	if !strings.Contains(result, "DB_HOST=mydb") {
		t.Errorf("update mode: DB_HOST was changed from user value: %q", result)
	}
	if !strings.Contains(result, "APP_KEY=base64:secret") {
		t.Errorf("update mode: APP_KEY was changed from user value: %q", result)
	}
	if !strings.Contains(result, "EXTRA=custom") {
		t.Errorf("update mode: EXTRA was removed: %q", result)
	}
}

func TestCopyConfigFile_updateCreatesWhenDestMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	srcContent := "KEY=value\n"
	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != srcContent {
		t.Errorf("update mode (dest missing): expected src content, got %q", string(got))
	}
}

func TestDeployConfigCmd_pathTraversalRejected(t *testing.T) {
	dir := t.TempDir()

	devboxYML := `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
`
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write services.yml with a malicious filename that escapes the configs directory.
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	servicesYML := `
services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    dir_internal: /var/www/app
    configs:
      - ../../etc/passwd
`
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write source file at expected src path.
	srcDir := filepath.Join(dir, "configs", "services", "main")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "../../etc/passwd"), []byte("KEY=value\n"), 0o644); err != nil {
		_ = err
	}

	flags := &rootFlags{configPath: devboxPath}
	cmd := newDeployConfigCmd(flags)
	err := cmd.RunE(cmd, []string{"main"})
	if err == nil {
		t.Error("expected path traversal error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "escapes") {
		t.Errorf("expected 'escapes' in error message, got: %v", err)
	}
}

func TestEnvLineKey(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"KEY=value", "KEY"},
		{"KEY=", "KEY"},
		{"KEY=val=ue", "KEY"},
		{"# comment", ""},
		{"", ""},
		{"   ", ""},
		{"  KEY = value", "KEY"},
	}
	for _, tc := range cases {
		got := builtin.EnvLineKey(tc.line)
		if got != tc.want {
			t.Errorf("envLineKey(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestParseEnvKeys(t *testing.T) {
	data := []byte("# comment\nFOO=1\nBAR=2\n\nBAZ=3\n")
	keys := builtin.ParseEnvKeys(data)
	for _, k := range []string{"FOO", "BAR", "BAZ"} {
		if !keys[k] {
			t.Errorf("parseEnvKeys: expected key %q", k)
		}
	}
	if keys["# comment"] {
		t.Error("parseEnvKeys: comment line should not be a key")
	}
}

// --- runtime when condition integration tests ---

// runtimeWhenStep builds a cmd-type step with a runtime when condition (builtin predicate).
func runtimeWhenStep(name, cmd, when string) config.DeployStep {
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description", When: when}
}

func TestResolveDeployPlan_runtimeWhenPassesThrough(t *testing.T) {
	// A step with a builtin predicate when should NOT be filtered at plan time —
	// it passes through as a resolvedStep with runtimeWhen set.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			runtimeWhenStep("install", "make app-install", "dir-empty services/main/src"),
		),
	})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have implicit step + install step (not filtered out).
	if len(steps) != 2 {
		t.Fatalf("want 2 steps (implicit + install), got %d", len(steps))
	}
	install := steps[1]
	if install.step.Name != "install" {
		t.Errorf("step name = %q, want install", install.step.Name)
	}
	if install.runtimeWhen != "dir-empty services/main/src" {
		t.Errorf("runtimeWhen = %q, want dir-empty services/main/src", install.runtimeWhen)
	}
}

func TestResolveDeployPlan_cmdWhenPassesThrough(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			runtimeWhenStep("check", "echo run", "cmd: test -f marker"),
		),
	})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].runtimeWhen != "cmd: test -f marker" {
		t.Errorf("runtimeWhen = %q, want cmd: test -f marker", steps[1].runtimeWhen)
	}
}

func TestResolveDeployPlan_templateWhenFalseFiltered(t *testing.T) {
	// Template when still filtered at plan time when false.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("debug-only", "echo debug", "{{.Runtime.UseHTTPS}}"),
			cmdStep("always", "echo always"),
		),
	})
	// Runtime.UseHTTPS not set → false → debug-only filtered out.
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + always = 2
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].step.Name != "always" {
		t.Errorf("step name = %q, want always", steps[1].step.Name)
	}
}

func TestResolveDeployPlan_runtimeWhenHasEmptyRuntimeWhenForTemplateStep(t *testing.T) {
	// Steps with no when or template when should have empty runtimeWhen.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("plain", "echo plain"),
		),
	})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, rs := range steps {
		if rs.runtimeWhen != "" {
			t.Errorf("step %q: runtimeWhen = %q, want empty", rs.step.Name, rs.runtimeWhen)
		}
	}
}

// --- phase-level when tests ---

func TestResolvePhaseSteps_phaseFalsyTemplateWhenExcludesAllSteps(t *testing.T) {
	// Phase with a false template when — all steps excluded.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWithWhen("setup", "{{.Runtime.UseHTTPS}}",
			cmdStep("step1", "cmd1"),
			cmdStep("step2", "cmd2"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{UseHTTPS: false}

	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only implicit step, phase excluded.
	if len(steps) != 1 {
		t.Fatalf("want 1 step (implicit only), got %d", len(steps))
	}
}

func TestResolvePhaseSteps_phaseTruthyTemplateWhenIncludesSteps(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWithWhen("setup", "{{.Runtime.UseHTTPS}}",
			cmdStep("step1", "cmd1"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{UseHTTPS: true}

	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + step1
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
}

func TestResolvePhaseSteps_phaseRuntimeWhenPropagatedToSteps(t *testing.T) {
	// Phase with a runtime when — stored in phaseWhen on each step; runtimeWhen stays empty.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWithWhen("setup", "dir-empty services/main/src",
			cmdStep("create-dirs", "mkdir -p services/main/src"),
			cmdStep("install", "make install"),
		),
	})

	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + 2 steps (not filtered — runtime condition)
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	// Both steps should carry the phase condition in phaseWhen, not runtimeWhen.
	for _, rs := range steps[1:] {
		if rs.phaseWhen != "dir-empty services/main/src" {
			t.Errorf("step %q: phaseWhen = %q, want dir-empty services/main/src", rs.step.Name, rs.phaseWhen)
		}
		if rs.runtimeWhen != "" {
			t.Errorf("step %q: runtimeWhen = %q, want empty (no step-level condition)", rs.step.Name, rs.runtimeWhen)
		}
	}
}

func TestResolvePhaseSteps_stepOwnRuntimeWhenTakesPriority(t *testing.T) {
	// Step with its own runtime when overrides the phase condition.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWithWhen("setup", "dir-empty services/main/src",
			cmdStep("plain", "echo plain"),
			runtimeWhenStep("install", "make install", "dir-empty services/main/src/special"),
		),
	})

	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	plain := steps[1]
	install := steps[2]

	// plain has no step-level condition — phase condition goes into phaseWhen.
	if plain.phaseWhen != "dir-empty services/main/src" {
		t.Errorf("plain: phaseWhen = %q, want phase condition", plain.phaseWhen)
	}
	if plain.runtimeWhen != "" {
		t.Errorf("plain: runtimeWhen = %q, want empty", plain.runtimeWhen)
	}
	// install has its own step condition in runtimeWhen; phaseWhen still carries the phase condition.
	if install.runtimeWhen != "dir-empty services/main/src/special" {
		t.Errorf("install: runtimeWhen = %q, want step's own condition", install.runtimeWhen)
	}
	if install.phaseWhen != "dir-empty services/main/src" {
		t.Errorf("install: phaseWhen = %q, want phase condition", install.phaseWhen)
	}
}

func TestPrintDeployPlanTable_showsPhaseWhenInHeader(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []resolvedStep{
		{
			phase: config.DeployPhase{Name: "setup", Description: "Setup", When: "dir-empty services/main/src"},
			step:  cmdStep("create-dirs", "mkdir"),
		},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[when: dir-empty services/main/src]") {
		t.Errorf("expected phase when annotation in header, got:\n%s", out)
	}
}

func TestPrintDeployPlanShell_showsPhaseWhenComment(t *testing.T) {
	var buf bytes.Buffer

	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env"}, step: implicitEnvStep},
		{
			phase:     config.DeployPhase{Name: "setup", When: "dir-empty services/main/src"},
			step:      cmdStep("create-dirs", "mkdir"),
			phaseWhen: "dir-empty services/main/src",
		},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()

	if !strings.Contains(out, "# phase setup [when: dir-empty services/main/src]") {
		t.Errorf("expected phase when comment in shell output, got:\n%s", out)
	}
}

func TestPrintDeployPlanShell_stepWhenNotDuplicatedWhenSameAsPhase(t *testing.T) {
	// Phase condition in phaseWhen — should appear as phase comment only, not as step "# when:".
	var buf bytes.Buffer

	phase := config.DeployPhase{Name: "setup", When: "dir-empty services/main/src"}
	steps := []resolvedStep{
		{phase: phase, step: cmdStep("create-dirs", "mkdir"), phaseWhen: "dir-empty services/main/src"},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()

	// Phase comment should appear once; step-level "# when:" should not appear.
	if strings.Count(out, "# when:") != 0 {
		t.Errorf("step-level when comment should not appear for phase-only condition, got:\n%s", out)
	}
	if !strings.Contains(out, "# phase setup [when: dir-empty services/main/src]") {
		t.Errorf("phase when comment missing:\n%s", out)
	}
}

func TestPrintDeployPlanTable_showsRuntimeWhenAnnotation(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	phase := config.DeployPhase{Name: "setup", Description: "Setup"}
	steps := []resolvedStep{
		{phase: phase, step: runtimeWhenStep("install", "make app-install", "dir-empty services/main/src"), runtimeWhen: "dir-empty services/main/src"},
		{phase: phase, step: cmdStep("always", "echo always")},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[when: dir-empty services/main/src]") {
		t.Errorf("expected runtime when annotation in output, got:\n%s", out)
	}
	// Step without runtimeWhen should not show annotation.
	if strings.Contains(out, "[when: ]") {
		t.Errorf("unexpected empty when annotation in output:\n%s", out)
	}
}

func TestPrintDeployPlanShell_runtimeWhenComment(t *testing.T) {
	var buf bytes.Buffer

	phase := config.DeployPhase{Name: "setup"}
	steps := []resolvedStep{
		{phase: phase, step: runtimeWhenStep("install", "make -f Makefile app-install", "dir-empty services/main/src"), runtimeWhen: "dir-empty services/main/src"},
		{phase: phase, step: cmdStep("always", "echo always")},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()

	if !strings.Contains(out, "# when: dir-empty services/main/src") {
		t.Errorf("expected '# when:' comment in shell output, got:\n%s", out)
	}
	// Step without runtimeWhen should not have comment.
	if strings.Count(out, "# when:") != 1 {
		t.Errorf("expected exactly 1 when comment, got:\n%s", out)
	}
}

// --- config-check command tests ---

// writeConfigCheckFixture creates the minimal file layout for config-check tests:
//
//	<dir>/devbox.yml          — config with services.main.configs list
//	<dir>/services/main/configs/<files>  — optional; call writeConfigFile to add them
func writeConfigCheckFixture(t *testing.T, configFiles []string) (dir string, flags *rootFlags) {
	t.Helper()
	dir = t.TempDir()

	devboxYML := `schema_version: "1"
project:
  name: laravel
  prefix: devbox
`
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devboxYML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}

	var sb strings.Builder
	for _, f := range configFiles {
		sb.WriteString("\n      - " + f)
	}
	cfgList := sb.String()
	servicesYML := `services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    configs:` + cfgList + "\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}

	return dir, &rootFlags{configPath: devboxPath}
}

func writeConfigFile(t *testing.T, dir, name string) {
	t.Helper()
	dest := filepath.Join(dir, "services", "main", "configs", name)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, []byte("KEY=value\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDeployConfigCheckCmd_allPresentReturnsNil(t *testing.T) {
	dir, flags := writeConfigCheckFixture(t, []string{".env"})
	writeConfigFile(t, dir, ".env")

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Errorf("expected nil when all configs present, got: %v", err)
	}
}

func TestDeployConfigCheckCmd_missingFileReturnsError(t *testing.T) {
	_, flags := writeConfigCheckFixture(t, []string{".env"})
	// Intentionally do NOT create the config file.

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err == nil {
		t.Error("expected error when config file missing, got nil")
	}
}

func TestDeployConfigCheckCmd_partialMissingReturnsError(t *testing.T) {
	dir, flags := writeConfigCheckFixture(t, []string{".env", "other.conf"})
	writeConfigFile(t, dir, ".env")
	// other.conf intentionally missing

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err == nil {
		t.Error("expected error when some configs missing, got nil")
	}
}

func TestDeployConfigCheckCmd_allMultiplePresentReturnsNil(t *testing.T) {
	dir, flags := writeConfigCheckFixture(t, []string{".env", "other.conf"})
	writeConfigFile(t, dir, ".env")
	writeConfigFile(t, dir, "other.conf")

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Errorf("expected nil when all configs present, got: %v", err)
	}
}

func TestDeployConfigCheckCmd_unknownServiceReturnsError(t *testing.T) {
	_, flags := writeConfigCheckFixture(t, []string{".env"})

	cmd := newDeployConfigCheckCmd(flags)
	err := cmd.RunE(cmd, []string{"nonexistent"})
	if err == nil {
		t.Error("expected error for unknown service, got nil")
	}
}

func TestDeployConfigCheckCmd_emptyConfigsListReturnsNil(t *testing.T) {
	_, flags := writeConfigCheckFixture(t, []string{})

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Errorf("expected nil for empty configs list, got: %v", err)
	}
}

// --- stepAddress tests ---

func TestStepAddress_orchestratorStep(t *testing.T) {
	rs := resolvedStep{
		phase: config.DeployPhase{Name: "start"},
		step:  cmdStep("up", "make up"),
	}
	if got := rs.stepAddress(); got != "start/up" {
		t.Errorf("stepAddress() = %q, want start/up", got)
	}
}

func TestStepAddress_serviceStep(t *testing.T) {
	rs := resolvedStep{
		phase:   config.DeployPhase{Name: "init"},
		step:    commandStep("migrate", "services.main.migrate"),
		service: "main",
	}
	if got := rs.stepAddress(); got != "main/init/migrate" {
		t.Errorf("stepAddress() = %q, want main/init/migrate", got)
	}
}

// --- per-service deploy integration tests ---

// writeServiceDeployFixture creates the full file layout for service deploy tests.
func writeServiceDeployFixture(t *testing.T, orchestratorYML, serviceDeployYML string) string {
	t.Helper()
	dir := t.TempDir()
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(`schema_version: "1"
project:
  name: laravel
  prefix: devbox
`), 0o644); err != nil {
		t.Fatal(err)
	}

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}

	servicesYML := `services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
`
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatal(err)
	}

	if orchestratorYML != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "deploy.yml"), []byte(orchestratorYML), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if serviceDeployYML != "" {
		deployDir := filepath.Join(devboxDir, "deploy")
		if err := os.MkdirAll(deployDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deployDir, "main.yml"), []byte(serviceDeployYML), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return devboxPath
}

func TestResolveDeployPlan_deployServicesInlines(t *testing.T) {
	orchestrator := `phases:
  - name: services
    deploy_services: true
    description: Deploy services
  - name: start
    description: Start
    steps:
      - name: up
        run: make up
`
	serviceDeploy := `phases:
  - name: setup
    description: Setup main
    steps:
      - name: create-dirs
        run: mkdir -p services/main/src
  - name: init
    description: Init main
    steps:
      - name: migrate
        command: services.main.migrate
`
	devboxPath := writeServiceDeployFixture(t, orchestrator, serviceDeploy)
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("resolveDeployPlan: %v", err)
	}

	// Expected: implicit + create-dirs + migrate + up = 4
	if len(steps) != 4 {
		var names []string
		for _, s := range steps {
			names = append(names, s.stepAddress())
		}
		t.Fatalf("want 4 steps, got %d: %v", len(steps), names)
	}

	// Service steps should have service="main"
	if steps[1].service != "main" {
		t.Errorf("steps[1].service = %q, want main", steps[1].service)
	}
	if steps[1].stepAddress() != "main/setup/create-dirs" {
		t.Errorf("steps[1] address = %q, want main/setup/create-dirs", steps[1].stepAddress())
	}
	if steps[2].stepAddress() != "main/init/migrate" {
		t.Errorf("steps[2] address = %q, want main/init/migrate", steps[2].stepAddress())
	}
	// Orchestrator step should have empty service
	if steps[3].service != "" {
		t.Errorf("steps[3].service = %q, want empty", steps[3].service)
	}
	if steps[3].stepAddress() != "start/up" {
		t.Errorf("steps[3] address = %q, want start/up", steps[3].stepAddress())
	}
}

func TestResolveServiceDeployPlan_singleService(t *testing.T) {
	serviceDeploy := `phases:
  - name: setup
    steps:
      - name: create-dirs
        run: mkdir -p services/main/src
  - name: init
    steps:
      - name: migrate
        command: services.main.migrate
`
	devboxPath := writeServiceDeployFixture(t, "", serviceDeploy)
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	steps, err := resolveServiceDeployPlan(cfg, "main")
	if err != nil {
		t.Fatalf("resolveServiceDeployPlan: %v", err)
	}

	// implicit + create-dirs + migrate = 3
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[1].service != "main" {
		t.Errorf("steps[1].service = %q, want main", steps[1].service)
	}
}

func TestFindStep_threePartAddress(t *testing.T) {
	serviceDeploy := `phases:
  - name: init
    steps:
      - name: migrate
        command: services.main.migrate
        description: Run migrations
`
	devboxPath := writeServiceDeployFixture(t, "", serviceDeploy)
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	phase, step, err := findStep(cfg, "main/init/migrate")
	if err != nil {
		t.Fatalf("findStep: %v", err)
	}
	if phase.Name != "init" {
		t.Errorf("phase.Name = %q, want init", phase.Name)
	}
	if step.Command != "services.main.migrate" {
		t.Errorf("step.Command = %q, want services.main.migrate", step.Command)
	}
}

func TestPrintDeployPlanTable_serviceStepsIndented(t *testing.T) {
	var buf bytes.Buffer
	w := render.NewWriter(&buf)

	steps := []resolvedStep{
		{phase: config.DeployPhase{Name: "env", Description: "Environment"}, step: implicitEnvStep},
		{phase: config.DeployPhase{Name: "setup", Description: "Setup"}, step: cmdStep("create-dirs", "mkdir"), service: "main"},
		{phase: config.DeployPhase{Name: "start", Description: "Start"}, step: commandStep("up", "up")},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "service: main") {
		t.Errorf("expected 'service: main' header, got:\n%s", out)
	}
	if !strings.Contains(out, "main/setup") {
		t.Errorf("expected 'main/setup' phase, got:\n%s", out)
	}
}

// --- command-reference step tests ---

func TestResolveDeployPlan_commandStepIncluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("init",
			commandStep("migrate", "services.main.migrate"),
			commandStepWith("seed", "services.main.seed", map[string]any{"env": "test"}),
		),
	})
	steps, err := resolveDeployPlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// implicit + 2 steps
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[1].step.Command != "services.main.migrate" {
		t.Errorf("steps[1].Command = %q, want services.main.migrate", steps[1].step.Command)
	}
	if steps[2].step.Command != "services.main.seed" {
		t.Errorf("steps[2].Command = %q, want services.main.seed", steps[2].step.Command)
	}
	if steps[2].step.With["env"] != "test" {
		t.Errorf("steps[2].With[env] = %q, want test", steps[2].step.With["env"])
	}
}

func TestStepCommand_commandWithMultipleWithSorted(t *testing.T) {
	step := config.DeployStep{
		Command: "services.main.migrate",
		With:    map[string]any{"z": "last", "a": "first", "m": "mid"},
	}
	got := stepCommand(step)
	want := "./bin/devbox commands run services.main.migrate --set a=first --set m=mid --set z=last"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStepCommand_commandWithEmptyWith(t *testing.T) {
	step := config.DeployStep{Command: "services.main.migrate", With: map[string]any{}}
	got := stepCommand(step)
	want := "./bin/devbox commands run services.main.migrate"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestExecBuiltinStep_validatesBeforeRun verifies that execBuiltinStep enforces
// the same builtin validation contract as full-pipeline plan resolution, so that
// `devbox deploy step` / `devbox reset step` cannot bypass it.
func TestExecBuiltinStep_validatesBeforeRun(t *testing.T) {
	cases := []struct {
		name    string
		step    config.DeployStep
		wantErr string
	}{
		{
			name:    "unknown builtin name",
			step:    config.DeployStep{Builtin: "typo_name"},
			wantErr: `invalid builtin "typo_name"`,
		},
		{
			name:    "remove_paths missing paths param",
			step:    config.DeployStep{Builtin: "remove_paths", With: map[string]any{}},
			wantErr: `invalid builtin "remove_paths"`,
		},
		{
			name:    "remove_paths with root-equivalent path",
			step:    config.DeployStep{Builtin: "remove_paths", With: map[string]any{"paths": []any{"."}}},
			wantErr: `invalid builtin "remove_paths"`,
		},
		{
			name: "service_configs_copy with invalid mode",
			step: config.DeployStep{
				Builtin: "service_configs_copy",
				With:    map[string]any{"service": "main", "mode": "bogus"},
			},
			wantErr: `invalid builtin "service_configs_copy"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := execBuiltinStep(tc.step, t.TempDir(), &config.DevboxConfig{}, nil, false)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

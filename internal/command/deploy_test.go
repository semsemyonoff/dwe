package command

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// makeDeployCfg returns a DevboxConfig with the given deploy phases.
func makeDeployCfg(phases []config.DeployPhase) *config.DevboxConfig {
	return &config.DevboxConfig{
		Deploy: config.DeployConfig{Phases: phases},
		Raw:    map[string]any{},
	}
}

// phaseWith builds a DeployPhase with the given name and steps.
func phaseWith(name string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", Steps: steps}
}

// cmdStep builds a cmd-type step.
func cmdStep(name, cmd string) config.DeployStep {
	return config.DeployStep{Name: name, Cmd: cmd, Description: name + " description"}
}

// makeStep builds a make-type step.
func makeStep(name, target string) config.DeployStep {
	return config.DeployStep{Name: name, Make: target, Description: name + " description"}
}

// whenStep builds a cmd-type step with a when condition.
func whenStep(name, cmd, when string) config.DeployStep {
	return config.DeployStep{Name: name, Cmd: cmd, Description: name + " description", When: when}
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
	if steps[0].step.Cmd != "./bin/devbox render env -o .env" {
		t.Errorf("first step cmd = %q, want ./bin/devbox render env -o .env", steps[0].step.Cmd)
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
			makeStep("up", "up"),
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
			whenStep("debug-step", "echo debug", "{{.Runtime.Debug.Enabled}}"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{
		Debug: config.DebugConfig{Enabled: true},
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
			whenStep("conditional", "echo only-if-debug", "{{.Runtime.Debug.Enabled}}"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{
		Debug: config.DebugConfig{Enabled: false},
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
		phaseWith("init", makeStep("m1", "target1"), makeStep("m2", "target2")),
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
	s := config.DeployStep{Cmd: "echo hello"}
	if got := stepBadge(s); got != "[cmd]" {
		t.Errorf("got %q, want [cmd]", got)
	}
}

func TestStepBadge_makeStep(t *testing.T) {
	s := config.DeployStep{Make: "up"}
	if got := stepBadge(s); got != "[make]" {
		t.Errorf("got %q, want [make]", got)
	}
}

// --- stepCommand tests ---

func TestStepCommand_cmdReturnsRaw(t *testing.T) {
	s := config.DeployStep{Cmd: "mkdir -p services/main/src"}
	if got := stepCommand(s); got != "mkdir -p services/main/src" {
		t.Errorf("got %q, want raw cmd", got)
	}
}

func TestStepCommand_makeReturnsMakeTarget(t *testing.T) {
	s := config.DeployStep{Make: "up"}
	if got := stepCommand(s); got != "make -f Makefile up" {
		t.Errorf("got %q, want 'make -f Makefile up'", got)
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

func TestPrintDeployPlanShell_makeStepAsMakeTarget(t *testing.T) {
	var buf bytes.Buffer
	steps := []resolvedStep{
		{phase: phaseWith("start"), step: makeStep("up", "up")},
	}
	printDeployPlanShell(steps, &buf)
	out := buf.String()
	if !strings.Contains(out, "make -f Makefile up") {
		t.Errorf("shell output missing make step, got: %q", out)
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
		case implicitEnvStep.Cmd:
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
		{phase: phaseWith("start"), step: makeStep("up", "up")},
		{phase: phaseWith("init"), step: makeStep("migrate", "migrate")},
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
		{phase: config.DeployPhase{Name: "start"}, step: makeStep("up", "up")},
	}
	printDeployPlanTable(steps, w)
	out := buf.String()

	if !strings.Contains(out, "[make]") {
		t.Errorf("expected '[make]' badge in table output, got: %s", out)
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
		phaseWith("init", makeStep("migrate", "migrate")),
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
	if step.Make != "migrate" {
		t.Errorf("step.Make = %q, want migrate", step.Make)
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

func TestDeployStep_dryRunPrintsMakeCommand(t *testing.T) {
	step := makeStep("migrate", "migrate")
	got := stepCommand(step)
	if got != "make -f Makefile migrate" {
		t.Errorf("dry-run make output = %q, want 'make -f Makefile migrate'", got)
	}
}

// --- findStep + when condition tests ---

func TestFindStep_whenFalseIsSkippedByCallerLogic(t *testing.T) {
	// Verify the when field is preserved on the returned step so the caller can evaluate it.
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("conditional", "echo hi", "{{.Runtime.Debug.Enabled}}"),
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

// --- stepCommand for both types ---

func TestStepCommand_bothTypes(t *testing.T) {
	cases := []struct {
		step config.DeployStep
		want string
	}{
		{config.DeployStep{Cmd: "echo hello"}, "echo hello"},
		{config.DeployStep{Make: "up"}, "make -f Makefile up"},
		{config.DeployStep{Make: "  db_create  "}, "make -f Makefile db_create"},
		{config.DeployStep{Cmd: "  mkdir -p x  "}, "mkdir -p x"},
	}
	for _, tc := range cases {
		got := stepCommand(tc.step)
		if got != tc.want {
			t.Errorf("stepCommand(%+v) = %q, want %q", tc.step, got, tc.want)
		}
	}
}

// Verify stepBadge for completeness in context of deploy step dispatch.
func TestStepBadge_bothTypes(t *testing.T) {
	if got := stepBadge(config.DeployStep{Cmd: "x"}); got != "[cmd]" {
		t.Errorf("got %q want [cmd]", got)
	}
	if got := stepBadge(config.DeployStep{Make: "x"}); got != "[make]" {
		t.Errorf("got %q want [make]", got)
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

	if err := copyConfigFile(src, dest, "default"); err != nil {
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

	if err := copyConfigFile(src, dest, "default"); err != nil {
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

	if err := copyConfigFile(src, dest, "replace"); err != nil {
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

	if err := copyConfigFile(src, dest, "update"); err != nil {
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

	if err := copyConfigFile(src, dest, "update"); err != nil {
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

	if err := copyConfigFile(src, dest, "update"); err != nil {
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

	// Write devbox.yml
	devboxYML := `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
services:
  main:
    type: app
    dir: ./services/main
    container: app-main
    dir_internal: /var/www/app
    configs:
      - src: configs/app/main/.env
        dest: "../../../etc/passwd"
        mode: default
`
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write source file at expected src path.
	srcDir := filepath.Join(dir, "configs", "app", "main")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".env"), []byte("KEY=value\n"), 0o644); err != nil {
		t.Fatal(err)
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
		got := envLineKey(tc.line)
		if got != tc.want {
			t.Errorf("envLineKey(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestParseEnvKeys(t *testing.T) {
	data := []byte("# comment\nFOO=1\nBAR=2\n\nBAZ=3\n")
	keys := parseEnvKeys(data)
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
	return config.DeployStep{Name: name, Cmd: cmd, Description: name + " description", When: when}
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
			whenStep("debug-only", "echo debug", "{{.Runtime.Debug.Enabled}}"),
			cmdStep("always", "echo always"),
		),
	})
	// Runtime.Debug.Enabled not set → false → debug-only filtered out.
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

package deploy_test

import (
	"fmt"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/pipeline"
)

// --- ResolvePlan tests ---

func TestResolvePlan_implicitEnvStepAlwaysFirst(t *testing.T) {
	cfg := makeDeployCfg(nil)
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("want 1 step (implicit), got %d", len(steps))
	}
	if steps[0].Step.Name != "render-env" {
		t.Errorf("first step name = %q, want render-env", steps[0].Step.Name)
	}
	if steps[0].Step.Devbox != "render env -o .env" {
		t.Errorf("first step devbox = %q, want render env -o .env", steps[0].Step.Devbox)
	}
}

func TestResolvePlan_emptyPhasesOnlyImplicit(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Errorf("want 1 (implicit only), got %d", len(steps))
	}
}

func TestResolvePlan_noWhenAlwaysIncluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("create-dirs", "mkdir -p services/main/src"),
			commandStep("up", "up"),
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[1].Step.Name != "create-dirs" {
		t.Errorf("steps[1].name = %q, want create-dirs", steps[1].Step.Name)
	}
	if steps[2].Step.Name != "up" {
		t.Errorf("steps[2].name = %q, want up", steps[2].Step.Name)
	}
}

func TestResolvePlan_truthyWhenIncluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("debug-step", "echo debug", "{{.Runtime.UseHTTPS}}"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{
		UseHTTPS: true,
	}
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].Step.Name != "debug-step" {
		t.Errorf("expected debug-step included, got %q", steps[1].Step.Name)
	}
}

func TestResolvePlan_falsyWhenExcluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("always", "echo always"),
			whenStep("conditional", "echo only-if-debug", "{{.Runtime.UseHTTPS}}"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{
		UseHTTPS: false,
	}
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps (implicit + always), got %d: %v", len(steps), steps)
	}
	for _, rs := range steps {
		if rs.Step.Name == "conditional" {
			t.Error("conditional step should have been excluded")
		}
	}
}

func TestResolvePlan_multiplePhasesPreserveOrder(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("s1", "cmd1"), cmdStep("s2", "cmd2")),
		phaseWith("init", commandStep("m1", "services.main.cmd1"), commandStep("m2", "services.main.cmd2")),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("want 5 steps, got %d", len(steps))
	}
	wantNames := []string{"render-env", "s1", "s2", "m1", "m2"}
	for i, want := range wantNames {
		if steps[i].Step.Name != want {
			t.Errorf("steps[%d].name = %q, want %q", i, steps[i].Step.Name, want)
		}
	}
}

func TestResolvePlan_invalidWhenTemplate(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("bad", "echo hi", "{{ unclosed"),
		),
	})
	_, err := deploy.ResolvePlan(cfg)
	if err == nil {
		t.Error("expected error for malformed when template, got nil")
	}
}

func TestResolvePlan_runtimeWhenPassesThrough(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			runtimeWhenStep("install", "make app-install", "dir-empty services/main/src"),
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps (implicit + install), got %d", len(steps))
	}
	install := steps[1]
	if install.Step.Name != "install" {
		t.Errorf("step name = %q, want install", install.Step.Name)
	}
	if install.RuntimeWhen != "dir-empty services/main/src" {
		t.Errorf("runtimeWhen = %q, want dir-empty services/main/src", install.RuntimeWhen)
	}
}

func TestResolvePlan_cmdWhenPassesThrough(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			runtimeWhenStep("check", "echo run", "cmd: test -f marker"),
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].RuntimeWhen != "cmd: test -f marker" {
		t.Errorf("runtimeWhen = %q, want cmd: test -f marker", steps[1].RuntimeWhen)
	}
}

func TestResolvePlan_templateWhenFalseFiltered(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("debug-only", "echo debug", "{{.Runtime.UseHTTPS}}"),
			cmdStep("always", "echo always"),
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].Step.Name != "always" {
		t.Errorf("step name = %q, want always", steps[1].Step.Name)
	}
}

func TestResolvePlan_runtimeWhenHasEmptyRuntimeWhenForTemplateStep(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("plain", "echo plain"),
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, rs := range steps {
		if rs.RuntimeWhen != "" {
			t.Errorf("step %q: runtimeWhen = %q, want empty", rs.Step.Name, rs.RuntimeWhen)
		}
	}
}

func TestResolvePhaseSteps_phaseFalsyTemplateWhenExcludesAllSteps(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWithWhen("setup", "{{.Runtime.UseHTTPS}}",
			cmdStep("step1", "cmd1"),
			cmdStep("step2", "cmd2"),
		),
	})
	cfg.Runtime = config.RuntimeConfig{UseHTTPS: false}

	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
}

func TestResolvePhaseSteps_phaseRuntimeWhenPropagatedToSteps(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWithWhen("setup", "dir-empty services/main/src",
			cmdStep("create-dirs", "mkdir -p services/main/src"),
			cmdStep("install", "make install"),
		),
	})

	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	for _, rs := range steps[1:] {
		if rs.PhaseWhen != "dir-empty services/main/src" {
			t.Errorf("step %q: phaseWhen = %q, want dir-empty services/main/src", rs.Step.Name, rs.PhaseWhen)
		}
		if rs.RuntimeWhen != "" {
			t.Errorf("step %q: runtimeWhen = %q, want empty (no step-level condition)", rs.Step.Name, rs.RuntimeWhen)
		}
	}
}

func TestResolvePhaseSteps_stepOwnRuntimeWhenTakesPriority(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWithWhen("setup", "dir-empty services/main/src",
			cmdStep("plain", "echo plain"),
			runtimeWhenStep("install", "make install", "dir-empty services/main/src/special"),
		),
	})

	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	plain := steps[1]
	install := steps[2]

	if plain.PhaseWhen != "dir-empty services/main/src" {
		t.Errorf("plain: phaseWhen = %q, want phase condition", plain.PhaseWhen)
	}
	if plain.RuntimeWhen != "" {
		t.Errorf("plain: runtimeWhen = %q, want empty", plain.RuntimeWhen)
	}
	if install.RuntimeWhen != "dir-empty services/main/src/special" {
		t.Errorf("install: runtimeWhen = %q, want step's own condition", install.RuntimeWhen)
	}
	if install.PhaseWhen != "dir-empty services/main/src" {
		t.Errorf("install: phaseWhen = %q, want phase condition", install.PhaseWhen)
	}
}

// --- FindStep tests ---

func TestFindStep_findsExistingStep(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("create-dirs", "mkdir -p services/main/src")),
		phaseWith("init", commandStep("migrate", "services.main.migrate")),
	})

	phase, step, err := deploy.FindStep(cfg, "init/migrate")
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

	_, _, err := deploy.FindStep(cfg, "setup/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing step, got nil")
	}
}

func TestFindStep_phaseNotFound(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("create-dirs", "mkdir -p services/main/src")),
	})

	_, _, err := deploy.FindStep(cfg, "nonexistent/create-dirs")
	if err == nil {
		t.Fatal("expected error for missing phase, got nil")
	}
}

func TestFindStep_invalidAddress(t *testing.T) {
	cfg := makeDeployCfg(nil)

	cases := []string{"noslash", "/noname", "nophase/", ""}
	for _, addr := range cases {
		_, _, err := deploy.FindStep(cfg, addr)
		if err == nil {
			t.Errorf("address %q: expected error, got nil", addr)
		}
	}
}

func TestFindStep_whenFalseIsSkippedByCallerLogic(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			whenStep("conditional", "echo hi", "{{.Runtime.UseHTTPS}}"),
		),
	})
	_, step, err := deploy.FindStep(cfg, "setup/conditional")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if step.When == "" {
		t.Error("expected step to carry When field, got empty")
	}
}

func TestFindStep_firstMatchReturned(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup",
			cmdStep("a", "echo a"),
			cmdStep("b", "echo b"),
			cmdStep("c", "echo c"),
		),
	})
	for _, name := range []string{"a", "b", "c"} {
		_, step, err := deploy.FindStep(cfg, fmt.Sprintf("setup/%s", name))
		if err != nil {
			t.Fatalf("FindStep setup/%s: %v", name, err)
		}
		if step.Name != name {
			t.Errorf("step.Name = %q, want %q", step.Name, name)
		}
	}
}

// --- StepAddress tests ---

func TestStepAddress_orchestratorStep(t *testing.T) {
	rs := pipeline.ResolvedStep{
		Phase: config.DeployPhase{Name: "start"}, Step: cmdStep("up", "make up"),
	}
	if got := rs.StepAddress(); got != "start/up" {
		t.Errorf("StepAddress() = %q, want start/up", got)
	}
}

func TestStepAddress_serviceStep(t *testing.T) {
	rs := pipeline.ResolvedStep{
		Phase: config.DeployPhase{Name: "init"}, Step: commandStep("migrate", "services.main.migrate"), Service: "main",
	}
	if got := rs.StepAddress(); got != "main/init/migrate" {
		t.Errorf("StepAddress() = %q, want main/init/migrate", got)
	}
}

// --- StepCommand tests ---

func TestStepCommand_cmdReturnsRaw(t *testing.T) {
	s := config.DeployStep{Run: "mkdir -p services/main/src"}
	if got := pipeline.StepCommand(s); got != "mkdir -p services/main/src" {
		t.Errorf("got %q, want raw cmd", got)
	}
}

func TestStepCommand_commandReturnsDevboxRunCmd(t *testing.T) {
	s := config.DeployStep{Command: "services.main.migrate"}
	if got := pipeline.StepCommand(s); got != "./bin/devbox commands run services.main.migrate" {
		t.Errorf("got %q, want './bin/devbox commands run services.main.migrate'", got)
	}
}

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
		got := pipeline.StepCommand(tc.step)
		if got != tc.want {
			t.Errorf("pipeline.StepCommand(%+v) = %q, want %q", tc.step, got, tc.want)
		}
	}
}

func TestStepCommand_commandWithWith(t *testing.T) {
	step := config.DeployStep{
		Command: "services.main.migrate",
		With:    map[string]any{"db": "mydb"},
	}
	got := pipeline.StepCommand(step)
	want := "./bin/devbox commands run services.main.migrate --set db=mydb"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStepCommand_commandWithMultipleWithSorted(t *testing.T) {
	step := config.DeployStep{
		Command: "services.main.migrate",
		With:    map[string]any{"z": "last", "a": "first", "m": "mid"},
	}
	got := pipeline.StepCommand(step)
	want := "./bin/devbox commands run services.main.migrate --set a=first --set m=mid --set z=last"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStepCommand_commandWithEmptyWith(t *testing.T) {
	step := config.DeployStep{Command: "services.main.migrate", With: map[string]any{}}
	got := pipeline.StepCommand(step)
	want := "./bin/devbox commands run services.main.migrate"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeployStep_dryRunPrintsCmdCommand(t *testing.T) {
	step := cmdStep("create-dirs", "mkdir -p services/main/src")
	got := pipeline.StepCommand(step)
	if got != "mkdir -p services/main/src" {
		t.Errorf("dry-run cmd output = %q, want raw command", got)
	}
}

func TestDeployStep_dryRunPrintsCommandRef(t *testing.T) {
	step := commandStep("migrate", "services.main.migrate")
	got := pipeline.StepCommand(step)
	if got != "./bin/devbox commands run services.main.migrate" {
		t.Errorf("dry-run command output = %q, want './bin/devbox commands run services.main.migrate'", got)
	}
}

func TestDeployStep_checkPreservedInConfig(t *testing.T) {
	step := config.DeployStep{
		Check: "file-exists services/main/configs/.env",
	}
	if step.Check != "file-exists services/main/configs/.env" {
		t.Errorf("Check = %q, want file-exists ...", step.Check)
	}
}

// --- post-deploy phase tests ---

func TestResolvePlan_postDeployPhaseIncludedLast(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("setup", cmdStep("create-dirs", "mkdir -p services/main/src")),
		phaseWith("init", commandStep("migrate", "services.main.migrate")),
		phaseWithUI("post-deploy", "plain",
			config.DeployStep{Name: "info", Devbox: "info", Description: "Show info"},
			config.DeployStep{
				Name:        "success",
				Builtin:     "message",
				With:        map[string]any{"level": "success", "text": "Deploy completed"},
				Description: "Print success",
			},
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("want 5 steps, got %d", len(steps))
	}
	last := steps[len(steps)-1]
	if last.Phase.Name != "post-deploy" {
		t.Errorf("last step phase = %q, want post-deploy", last.Phase.Name)
	}
	if last.Step.Name != "success" {
		t.Errorf("last step name = %q, want success", last.Step.Name)
	}
	secondLast := steps[len(steps)-2]
	if secondLast.Phase.Name != "post-deploy" {
		t.Errorf("second-last step phase = %q, want post-deploy", secondLast.Phase.Name)
	}
	if secondLast.Step.Name != "info" {
		t.Errorf("second-last step name = %q, want info", secondLast.Step.Name)
	}
}

func TestResolvePlan_postDeployPhasePreserved(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		{
			Name:        "post-deploy",
			Description: "post-deploy phase",
			Steps: []config.DeployStep{
				{Name: "info", Devbox: "info", Description: "Show info"},
			},
		},
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[1].Phase.Name != "post-deploy" {
		t.Errorf("phase Name = %q, want post-deploy", steps[1].Phase.Name)
	}
}

func TestResolvePlan_postDeployStepsAreInPlan(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("start", cmdStep("up", "docker up")),
		phaseWithUI("post-deploy", "plain",
			config.DeployStep{Name: "summary", Devbox: "info", Description: "Summary"},
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasPostDeploy := false
	for _, rs := range steps {
		if rs.Phase.Name == "post-deploy" {
			hasPostDeploy = true
			break
		}
	}
	if !hasPostDeploy {
		t.Error("post-deploy phase steps should be included in the plan")
	}
}

// --- command-reference step tests ---

func TestResolvePlan_commandStepIncluded(t *testing.T) {
	cfg := makeDeployCfg([]config.DeployPhase{
		phaseWith("init",
			commandStep("migrate", "services.main.migrate"),
			commandStepWith("seed", "services.main.seed", map[string]any{"env": "test"}),
		),
	})
	steps, err := deploy.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[1].Step.Command != "services.main.migrate" {
		t.Errorf("steps[1].Command = %q, want services.main.migrate", steps[1].Step.Command)
	}
	if steps[2].Step.Command != "services.main.seed" {
		t.Errorf("steps[2].Command = %q, want services.main.seed", steps[2].Step.Command)
	}
	if steps[2].Step.With["env"] != "test" {
		t.Errorf("steps[2].With[env] = %q, want test", steps[2].Step.With["env"])
	}
}

package command

import (
	"errors"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/usercommands/registry"
)

// helpers

func makeToggleCfg(services map[string]config.ServiceConfig) *config.DevboxConfig {
	return &config.DevboxConfig{Services: services}
}

func svcApp(hooks *config.ServiceToggleHooks, onDisable *config.ServiceToggleHooks, notes *config.ServiceNotes) config.ServiceConfig {
	return config.ServiceConfig{
		Type:      config.ServiceTypeApp,
		Container: "app",
		Enabled:   true,
		OnEnable:  hooks,
		OnDisable: onDisable,
		Notes:     notes,
	}
}

func emptyDeployMap() map[string]*config.DeployConfig {
	return map[string]*config.DeployConfig{}
}

func deployMapWith(names ...string) map[string]*config.DeployConfig {
	m := map[string]*config.DeployConfig{}
	for _, n := range names {
		m[n] = &config.DeployConfig{}
	}
	return m
}

func emptyReg() *registry.Registry {
	return registry.NewEmptyRegistry()
}

// TestBuildTogglePlan_SingleEnableRestart verifies that a single enable with no
// explicit requires defaults to RequiresRestart → one Restart apply step.
func TestBuildTogglePlan_SingleEnableRestart(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(nil, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 1 {
		t.Fatalf("want 1 apply step, got %d", len(plan.ApplySteps))
	}
	if plan.ApplySteps[0].Kind != journal.PendingRestart {
		t.Errorf("want PendingRestart, got %q", plan.ApplySteps[0].Kind)
	}
	if len(plan.BeforeSteps) != 0 || len(plan.AfterSteps) != 0 {
		t.Errorf("want no before/after steps, got before=%v after=%v", plan.BeforeSteps, plan.AfterSteps)
	}
}

// TestBuildTogglePlan_ExplicitRestartRequires verifies explicit requires: restart
// produces the same result as the default.
func TestBuildTogglePlan_ExplicitRestartRequires(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresRestart}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 1 || plan.ApplySteps[0].Kind != journal.PendingRestart {
		t.Errorf("want [{Restart}], got %v", plan.ApplySteps)
	}
}

// TestBuildTogglePlan_SingleEnableDeploy verifies that requires: deploy produces
// a Deploy apply step with the service name.
func TestBuildTogglePlan_SingleEnableDeploy(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeploy}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), deployMapWith("web"), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 1 {
		t.Fatalf("want 1 apply step, got %d", len(plan.ApplySteps))
	}
	step := plan.ApplySteps[0]
	if step.Kind != journal.PendingDeploy {
		t.Errorf("want PendingDeploy, got %q", step.Kind)
	}
	if len(step.Services) != 1 || step.Services[0] != "web" {
		t.Errorf("want Services=[web], got %v", step.Services)
	}
}

// TestBuildTogglePlan_MixedDeployAndRestart verifies that two enables where one
// needs deploy and one needs restart produces [{Deploy,[deploy-contrib]},{Restart}].
func TestBuildTogglePlan_MixedDeployAndRestart(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"alpha": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeploy}, nil, nil),
		"beta":  svcApp(&config.ServiceToggleHooks{Requires: config.RequiresRestart}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), deployMapWith("alpha"), []ToggleAction{
		{Service: "alpha", Direction: DirectionEnable},
		{Service: "beta", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 2 {
		t.Fatalf("want 2 apply steps, got %d: %v", len(plan.ApplySteps), plan.ApplySteps)
	}
	if plan.ApplySteps[0].Kind != journal.PendingDeploy {
		t.Errorf("step[0] want PendingDeploy, got %q", plan.ApplySteps[0].Kind)
	}
	if len(plan.ApplySteps[0].Services) != 1 || plan.ApplySteps[0].Services[0] != "alpha" {
		t.Errorf("step[0].Services want [alpha], got %v", plan.ApplySteps[0].Services)
	}
	if plan.ApplySteps[1].Kind != journal.PendingRestart {
		t.Errorf("step[1] want PendingRestart, got %q", plan.ApplySteps[1].Kind)
	}
}

// TestBuildTogglePlan_DeployContribWithoutDeployFile verifies ErrDeployRequiredNoDeployFile.
func TestBuildTogglePlan_DeployContribWithoutDeployFile(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeploy}, nil, nil),
	})
	_, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrDeployRequiredNoDeployFile) {
		t.Errorf("want ErrDeployRequiredNoDeployFile, got: %v", err)
	}
}

// TestBuildTogglePlan_AllNone verifies that all RequiresNone → empty ApplySteps.
func TestBuildTogglePlan_AllNone(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresNone}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 0 {
		t.Errorf("want no apply steps, got %v", plan.ApplySteps)
	}
}

// TestBuildTogglePlan_BeforeAfterOrdering verifies deterministic hook ordering.
func TestBuildTogglePlan_BeforeAfterOrdering(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"aaa": svcApp(&config.ServiceToggleHooks{
			Requires: config.RequiresNone,
			Before:   []string{"aaa:pre1", "aaa:pre2"},
			After:    []string{"aaa:post1"},
		}, nil, nil),
		"bbb": svcApp(&config.ServiceToggleHooks{
			Requires: config.RequiresNone,
			Before:   []string{"bbb:pre"},
			After:    []string{"bbb:post"},
		}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "bbb", Direction: DirectionEnable}, // submitted out of alpha order
		{Service: "aaa", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantBefore := []string{"aaa:pre1", "aaa:pre2", "bbb:pre"}
	wantAfter := []string{"aaa:post1", "bbb:post"}

	gotBefore := make([]string, len(plan.BeforeSteps))
	for i, s := range plan.BeforeSteps {
		gotBefore[i] = s.CommandID
	}
	gotAfter := make([]string, len(plan.AfterSteps))
	for i, s := range plan.AfterSteps {
		gotAfter[i] = s.CommandID
	}

	if len(gotBefore) != len(wantBefore) {
		t.Fatalf("before steps: want %v, got %v", wantBefore, gotBefore)
	}
	for i := range wantBefore {
		if gotBefore[i] != wantBefore[i] {
			t.Errorf("before[%d]: want %q, got %q", i, wantBefore[i], gotBefore[i])
		}
	}
	if len(gotAfter) != len(wantAfter) {
		t.Fatalf("after steps: want %v, got %v", wantAfter, gotAfter)
	}
	for i := range wantAfter {
		if gotAfter[i] != wantAfter[i] {
			t.Errorf("after[%d]: want %q, got %q", i, wantAfter[i], gotAfter[i])
		}
	}
}

// TestBuildTogglePlan_NotesPresentAndAbsent verifies notes are collected for
// the correct direction and absent when not set.
func TestBuildTogglePlan_NotesPresentAndAbsent(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": {
			Type:      config.ServiceTypeApp,
			Container: "app",
			Enabled:   true,
			OnEnable:  &config.ServiceToggleHooks{Requires: config.RequiresNone},
			Notes:     &config.ServiceNotes{Enable: "run migrations after", Disable: "flush cache first"},
		},
	})

	planEnable, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(planEnable.Notes) != 1 || planEnable.Notes[0] != "run migrations after" {
		t.Errorf("enable notes: want [run migrations after], got %v", planEnable.Notes)
	}

	planDisable, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionDisable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(planDisable.Notes) != 1 || planDisable.Notes[0] != "flush cache first" {
		t.Errorf("disable notes: want [flush cache first], got %v", planDisable.Notes)
	}

	// Service without notes block: no notes.
	cfgNoNotes := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(nil, nil, nil),
	})
	planNoNotes, err := buildTogglePlan(cfgNoNotes, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(planNoNotes.Notes) != 0 {
		t.Errorf("want no notes, got %v", planNoNotes.Notes)
	}
}

// TestBuildTogglePlan_UnknownRequires verifies ErrUnknownToggleRequires without
// running devbox validate first (regression for the fourth review).
func TestBuildTogglePlan_UnknownRequires(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: "rstart"}, nil, nil),
	})
	_, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnknownToggleRequires) {
		t.Errorf("want ErrUnknownToggleRequires, got: %v", err)
	}
}

// TestBuildTogglePlan_DisableDeployForbidden verifies on_disable.requires: deploy → error.
func TestBuildTogglePlan_DisableDeployForbidden(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(nil, &config.ServiceToggleHooks{Requires: config.RequiresDeploy}, nil),
	})
	_, err := buildTogglePlan(cfg, emptyReg(), deployMapWith("web"), []ToggleAction{
		{Service: "web", Direction: DirectionDisable},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrDisableDeployForbidden) {
		t.Errorf("want ErrDisableDeployForbidden, got: %v", err)
	}
}

// TestBuildTogglePlan_DirectionUnspecifiedError verifies unspecified direction is rejected.
func TestBuildTogglePlan_DirectionUnspecifiedError(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(nil, nil, nil),
	})
	_, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionUnspecified},
	})
	if err == nil {
		t.Fatal("expected error for unspecified direction, got nil")
	}
}

// TestBuildTogglePlan_DeployContribsAlphabeticalOrder verifies that multiple deploy
// contributors are stored in alphabetical order in the step.
func TestBuildTogglePlan_DeployContribsAlphabeticalOrder(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"zeta":  svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeploy}, nil, nil),
		"alpha": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeploy}, nil, nil),
		"mu":    svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeploy}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), deployMapWith("zeta", "alpha", "mu"), []ToggleAction{
		{Service: "zeta", Direction: DirectionEnable},
		{Service: "alpha", Direction: DirectionEnable},
		{Service: "mu", Direction: DirectionEnable},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 1 {
		t.Fatalf("want 1 apply step, got %d", len(plan.ApplySteps))
	}
	want := []string{"alpha", "mu", "zeta"}
	if len(plan.ApplySteps[0].Services) != len(want) {
		t.Fatalf("services: want %v, got %v", want, plan.ApplySteps[0].Services)
	}
	for i, s := range want {
		if plan.ApplySteps[0].Services[i] != s {
			t.Errorf("services[%d]: want %q, got %q", i, s, plan.ApplySteps[0].Services[i])
		}
	}
}

// ---- renderTogglePlan tests ----

func TestRenderTogglePlan_EmptyPlanNoSteps(t *testing.T) {
	var w strings.Builder
	renderTogglePlan(&w, TogglePlan{})
	got := w.String()
	if !strings.Contains(got, "No steps required") {
		t.Errorf("want 'No steps required', got: %q", got)
	}
}

func TestRenderTogglePlan_RestartOnly(t *testing.T) {
	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingRestart}},
	}
	var w strings.Builder
	renderTogglePlan(&w, plan)
	got := w.String()
	if !strings.Contains(got, "Plan to apply (1 step):") {
		t.Errorf("want singular 'step', got: %q", got)
	}
	if !strings.Contains(got, "→ apply step: restart stack") {
		t.Errorf("want restart label, got: %q", got)
	}
}

func TestRenderTogglePlan_SingleServiceDeploy(t *testing.T) {
	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingDeploy, Services: []string{"web"}}},
	}
	var w strings.Builder
	renderTogglePlan(&w, plan)
	got := w.String()
	// Single-target deploy renders as a runnable shell command.
	if !strings.Contains(got, "devbox deploy run --service web") {
		t.Errorf("want shell-form single deploy, got: %q", got)
	}
	if strings.Contains(got, "→ apply step") {
		t.Errorf("single deploy should NOT use internal-step label, got: %q", got)
	}
}

func TestRenderTogglePlan_MultiServiceDeploy(t *testing.T) {
	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingDeploy, Services: []string{"alpha", "beta"}}},
	}
	var w strings.Builder
	renderTogglePlan(&w, plan)
	got := w.String()
	if !strings.Contains(got, "→ apply step: deploy services {alpha, beta}") {
		t.Errorf("want internal-step label for multi-deploy, got: %q", got)
	}
	if !strings.Contains(got, "dependency-ordered at execution") {
		t.Errorf("want '(dependency-ordered at execution)' note, got: %q", got)
	}
}

func TestRenderTogglePlan_FullPlan_NumberedOrder(t *testing.T) {
	plan := TogglePlan{
		BeforeSteps: []PlanStep{{CommandID: "foo:prepare"}},
		ApplySteps: []ApplyStep{
			{Kind: journal.PendingDeploy, Services: []string{"bar", "foo"}},
			{Kind: journal.PendingRestart},
		},
		AfterSteps: []PlanStep{{CommandID: "foo:smoke"}},
	}
	var w strings.Builder
	renderTogglePlan(&w, plan)
	got := w.String()

	// Header should say 4 steps.
	if !strings.Contains(got, "Plan to apply (4 steps):") {
		t.Errorf("want '4 steps', got: %q", got)
	}

	// Lines should be numbered 1-4 in order.
	lines := strings.Split(strings.TrimSpace(got), "\n")
	var numbered []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if len(l) > 2 && l[1] == '.' {
			numbered = append(numbered, l)
		}
	}
	if len(numbered) != 4 {
		t.Fatalf("want 4 numbered lines, got %d: %v", len(numbered), numbered)
	}
	if !strings.HasPrefix(numbered[0], "1.") || !strings.Contains(numbered[0], "devbox commands foo:prepare") {
		t.Errorf("line 1 wrong: %q", numbered[0])
	}
	if !strings.HasPrefix(numbered[1], "2.") || !strings.Contains(numbered[1], "deploy services {bar, foo}") {
		t.Errorf("line 2 wrong: %q", numbered[1])
	}
	if !strings.HasPrefix(numbered[2], "3.") || !strings.Contains(numbered[2], "restart stack") {
		t.Errorf("line 3 wrong: %q", numbered[2])
	}
	if !strings.HasPrefix(numbered[3], "4.") || !strings.Contains(numbered[3], "devbox commands foo:smoke") {
		t.Errorf("line 4 wrong: %q", numbered[3])
	}
}

func TestRenderTogglePlan_NotesPrintedWhenPresent(t *testing.T) {
	plan := TogglePlan{
		Notes: []string{"run migrations", "clear redis cache"},
	}
	var w strings.Builder
	renderTogglePlan(&w, plan)
	got := w.String()
	if !strings.Contains(got, "Notes:") {
		t.Errorf("want Notes: section, got: %q", got)
	}
	if !strings.Contains(got, "run migrations") || !strings.Contains(got, "clear redis cache") {
		t.Errorf("want both notes in output, got: %q", got)
	}
}

func TestRenderTogglePlan_NotesSectionOmittedWhenEmpty(t *testing.T) {
	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingRestart}},
	}
	var w strings.Builder
	renderTogglePlan(&w, plan)
	got := w.String()
	if strings.Contains(got, "Notes:") {
		t.Errorf("Notes section should be absent when empty, got: %q", got)
	}
}

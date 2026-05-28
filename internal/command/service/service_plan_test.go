package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/command/deploy"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/registry"
	"devbox-cli/internal/core/usercommands/runtime"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/core/workflow/lifecycle"

	"github.com/spf13/cobra"
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

func emptyDeployMap() map[string]*config.ServiceDeployConfig {
	return map[string]*config.ServiceDeployConfig{}
}

func deployMapWith(names ...string) map[string]*config.ServiceDeployConfig {
	m := map[string]*config.ServiceDeployConfig{}
	for _, n := range names {
		m[n] = &config.ServiceDeployConfig{}
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
	}, nil)
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
	}, nil)
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
	}, nil)
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

// TestBuildTogglePlan_DeployOrRestart_NeverDeployed verifies that
// requires: deploy-or-restart resolves to a Deploy step when the journal
// has no record of the service being deployed.
func TestBuildTogglePlan_DeployOrRestart_NeverDeployed(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeployOrRestart}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), deployMapWith("web"), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 1 || plan.ApplySteps[0].Kind != journal.PendingDeploy {
		t.Fatalf("deploy-or-restart on never-deployed must produce Deploy step, got %+v", plan.ApplySteps)
	}
}

// TestBuildTogglePlan_DeployOrRestart_AlreadyDeployed verifies that
// requires: deploy-or-restart resolves to a Restart step when the service
// is already in the deployed set.
func TestBuildTogglePlan_DeployOrRestart_AlreadyDeployed(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeployOrRestart}, nil, nil),
	})
	plan, err := buildTogglePlan(cfg, emptyReg(), deployMapWith("web"), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	}, map[string]bool{"web": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.ApplySteps) != 1 || plan.ApplySteps[0].Kind != journal.PendingRestart {
		t.Fatalf("deploy-or-restart on already-deployed must produce Restart step, got %+v", plan.ApplySteps)
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
	}, nil)
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
	}, nil)
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
	}, nil)
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
	reg := registry.NewEmptyRegistry()
	for _, id := range []string{"aaa:pre1", "aaa:pre2", "aaa:post1", "bbb:pre", "bbb:post"} {
		reg.AddCommandForTest(makeShellCmd(id))
	}
	plan, err := buildTogglePlan(cfg, reg, emptyDeployMap(), []ToggleAction{
		{Service: "bbb", Direction: DirectionEnable}, // submitted out of alpha order
		{Service: "aaa", Direction: DirectionEnable},
	}, nil)
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
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(planEnable.Notes) != 1 || planEnable.Notes[0] != "run migrations after" {
		t.Errorf("enable notes: want [run migrations after], got %v", planEnable.Notes)
	}

	planDisable, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionDisable},
	}, nil)
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
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(planNoNotes.Notes) != 0 {
		t.Errorf("want no notes, got %v", planNoNotes.Notes)
	}
}

// TestBuildTogglePlan_NotesTemplate verifies that notes support Go template
// expansion against the merged config (.name / .svc / .project / .raw).
func TestBuildTogglePlan_NotesTemplate(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": {
			Type:      config.ServiceTypeApp,
			Container: "myapp",
			Enabled:   true,
			OnEnable:  &config.ServiceToggleHooks{Requires: config.RequiresNone},
			Notes:     &config.ServiceNotes{Enable: "remember to run migrations on {{ .svc.Container }} (service: {{ .name }})"},
		},
	})

	plan, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Notes) != 1 {
		t.Fatalf("want 1 note, got %d", len(plan.Notes))
	}
	want := "remember to run migrations on myapp (service: web)"
	if plan.Notes[0] != want {
		t.Errorf("note = %q, want %q", plan.Notes[0], want)
	}
}

// TestBuildTogglePlan_NotesTemplateError verifies that a bad template surfaces
// inline rather than disappearing silently.
func TestBuildTogglePlan_NotesTemplateError(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": {
			Type:      config.ServiceTypeApp,
			Container: "myapp",
			Enabled:   true,
			OnEnable:  &config.ServiceToggleHooks{Requires: config.RequiresNone},
			// Unterminated action — text/template will reject at parse time.
			Notes: &config.ServiceNotes{Enable: "broken {{ .name"},
		},
	})

	plan, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Notes) != 1 || !strings.Contains(plan.Notes[0], "note template error") {
		t.Errorf("expected inline template error marker, got %v", plan.Notes)
	}
}

// TestBuildTogglePlan_DisableDeployOrRestart_RejectedOnDeployedService verifies
// that on_disable.requires: deploy-or-restart is rejected even when the
// service is currently deployed (where Resolve would otherwise collapse it to
// restart). Runtime must mirror the static validator, which rejects this on
// every disable regardless of journal state.
func TestBuildTogglePlan_DisableDeployOrRestart_RejectedOnDeployedService(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(nil, &config.ServiceToggleHooks{Requires: config.RequiresDeployOrRestart}, nil),
	})
	_, err := buildTogglePlan(cfg, emptyReg(), deployMapWith("web"), []ToggleAction{
		{Service: "web", Direction: DirectionDisable},
	}, map[string]bool{"web": true})
	if !errors.Is(err, ErrDisableDeployForbidden) {
		t.Errorf("want ErrDisableDeployForbidden, got %v", err)
	}
}

// TestBuildTogglePlan_DeployOrRestart_RejectedWithoutDeployFile verifies that
// requires: deploy-or-restart without a deploy.yml is rejected even when the
// service is already deployed (where Resolve would collapse to restart). The
// runtime guard mirrors the static validator's deploy.yml requirement on raw
// `deploy-or-restart`.
func TestBuildTogglePlan_DeployOrRestart_RejectedWithoutDeployFile(t *testing.T) {
	cfg := makeToggleCfg(map[string]config.ServiceConfig{
		"web": svcApp(&config.ServiceToggleHooks{Requires: config.RequiresDeployOrRestart}, nil, nil),
	})
	_, err := buildTogglePlan(cfg, emptyReg(), emptyDeployMap(), []ToggleAction{
		{Service: "web", Direction: DirectionEnable},
	}, map[string]bool{"web": true})
	if !errors.Is(err, ErrDeployRequiredNoDeployFile) {
		t.Errorf("want ErrDeployRequiredNoDeployFile, got %v", err)
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
	}, nil)
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
	}, nil)
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
	}, nil)
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
	}, nil)
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

// ---- executeTogglePlan tests ----

// makeExecuteDeps builds an ExecuteDeps suitable for unit tests. The caller
// provides record-calls stubs for RunDeploy/RunRestart/RunUserCmd; the rest
// of the fields are wired to a temp directory + empty seams.
func makeExecuteDeps(
	t *testing.T,
	runDeploy func(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts deploy.Opts) error,
	runRestart func(rctx lifecycle.RunContext) error,
	runUserCmd func(ctx context.Context, rc runtime.RunContext) error,
) (ExecuteDeps, string) {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yml")

	if runDeploy == nil {
		runDeploy = func(context.Context, *cobra.Command, *cmdctx.RootFlags, deploy.Opts) error { return nil }
	}
	if runRestart == nil {
		runRestart = func(lifecycle.RunContext) error { return nil }
	}
	if runUserCmd == nil {
		runUserCmd = func(context.Context, runtime.RunContext) error { return nil }
	}

	cmd := &cobra.Command{}
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))

	return ExecuteDeps{
		Cmd:        cmd,
		BaseDir:    dir,
		StatePath:  statePath,
		RunDeploy:  runDeploy,
		RunRestart: runRestart,
		RunUserCmd: runUserCmd,
	}, dir
}

// TestExecuteTogglePlan_EmptyPlanIsNoop verifies an empty plan returns nil immediately.
func TestExecuteTogglePlan_EmptyPlanIsNoop(t *testing.T) {
	deps, _ := makeExecuteDeps(t, nil, nil, nil)
	err := executeTogglePlan(context.Background(), deps, TogglePlan{}, ExecuteOptions{})
	if err != nil {
		t.Errorf("empty plan: want nil, got %v", err)
	}
}

// TestExecuteTogglePlan_FullPlanOrder verifies before → apply → after ordering.
func TestExecuteTogglePlan_FullPlanOrder(t *testing.T) {
	var order []string

	deps, _ := makeExecuteDeps(t,
		func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, opts deploy.Opts) error {
			order = append(order, "deploy")
			return nil
		},
		func(_ lifecycle.RunContext) error {
			order = append(order, "restart")
			return nil
		},
		func(_ context.Context, rc runtime.RunContext) error {
			order = append(order, "hook:"+rc.Cmd.ID)
			return nil
		},
	)

	// We need a registry entry for the hook commands.
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(makeShellCmd("foo:pre"))
	reg.AddCommandForTest(makeShellCmd("foo:post"))
	deps.CmdReg = reg
	deps.Cfg = &config.DevboxConfig{}

	plan := TogglePlan{
		BeforeSteps: []PlanStep{{CommandID: "foo:pre"}},
		ApplySteps:  []ApplyStep{{Kind: journal.PendingRestart}},
		AfterSteps:  []PlanStep{{CommandID: "foo:post"}},
	}
	contributors := []Contributor{{Service: "foo", Requires: config.RequiresRestart}}

	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"hook:foo:pre", "restart", "hook:foo:post"}
	if len(order) != len(want) {
		t.Fatalf("order: want %v, got %v", want, order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d]: want %q, got %q", i, w, order[i])
		}
	}
}

// TestExecuteTogglePlan_SkipHooks runs only apply steps when SkipHooks is true.
func TestExecuteTogglePlan_SkipHooks(t *testing.T) {
	restartCalled := false
	hookCalled := false
	deps, _ := makeExecuteDeps(t,
		nil,
		func(_ lifecycle.RunContext) error { restartCalled = true; return nil },
		func(_ context.Context, _ runtime.RunContext) error { hookCalled = true; return nil },
	)
	plan := TogglePlan{
		BeforeSteps: []PlanStep{{CommandID: "foo:pre"}},
		ApplySteps:  []ApplyStep{{Kind: journal.PendingRestart}},
		AfterSteps:  []PlanStep{{CommandID: "foo:post"}},
	}
	contributors := []Contributor{{Service: "foo", Requires: config.RequiresRestart}}
	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{
		SkipHooks:    true,
		Contributors: contributors,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !restartCalled {
		t.Error("restart should have been called")
	}
	if hookCalled {
		t.Error("hooks should NOT have been called when SkipHooks=true")
	}
}

// TestExecuteTogglePlan_ApplyFailureShortCircuits verifies that after-hooks are not
// run and pending stays intact on apply-step failure.
func TestExecuteTogglePlan_ApplyFailureShortCircuits(t *testing.T) {
	afterHookCalled := false
	deps, dir := makeExecuteDeps(t,
		nil,
		func(_ lifecycle.RunContext) error { return fmt.Errorf("restart failed") },
		func(_ context.Context, _ runtime.RunContext) error { afterHookCalled = true; return nil },
	)

	// Pre-seed pending so we can verify it's not cleared.
	statePath := filepath.Join(dir, "state.yml")
	if err := journal.AddPendingOp(statePath, journal.PendingOp{Kind: journal.PendingRestart}, "hash1"); err != nil {
		t.Fatalf("pre-seed pending: %v", err)
	}

	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingRestart}},
		AfterSteps: []PlanStep{{CommandID: "foo:post"}},
	}
	contributors := []Contributor{{Service: "foo", Requires: config.RequiresRestart}}
	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{
		SkipHooks:    false,
		Contributors: contributors,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if afterHookCalled {
		t.Error("after-hook must NOT run on apply failure")
	}

	// Verify pending is still intact.
	state, loadErr := journal.Load(statePath)
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if state.Pending == nil {
		t.Error("pending should remain intact on apply failure")
	}
}

// TestExecuteTogglePlan_SuccessClearsPendingAtomically verifies that on success,
// ONE ClearPendingOps call clears contributor-owned pending and leaves unrelated
// entries intact.
func TestExecuteTogglePlan_SuccessClearsPendingAtomically(t *testing.T) {
	deps, dir := makeExecuteDeps(t, nil, nil, nil)
	statePath := filepath.Join(dir, "state.yml")

	// Pre-seed: [{Deploy, ["a", "x"]}, {Restart}] — "x" is from another session.
	if err := journal.AddPendingOps(statePath, []journal.PendingOp{
		{Kind: journal.PendingDeploy, Services: []string{"a", "x"}},
		{Kind: journal.PendingRestart},
	}, "hash1"); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	plan := TogglePlan{
		ApplySteps: []ApplyStep{
			{Kind: journal.PendingDeploy, Services: []string{"a"}},
			{Kind: journal.PendingRestart},
		},
	}
	// Contributors: "a" needs deploy, "b" needs restart.
	contributors := []Contributor{
		{Service: "a", Requires: config.RequiresDeploy},
		{Service: "b", Requires: config.RequiresRestart},
	}
	if err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Reload and verify: "a" removed from deploy op (x remains), restart op cleared.
	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Pending == nil {
		t.Fatal("pending should still exist (x is unrelated)")
	}
	deployOp := state.Pending.Find(journal.PendingDeploy)
	if deployOp == nil {
		t.Fatal("deploy op should remain (x is unrelated)")
	}
	if len(deployOp.Services) != 1 || deployOp.Services[0] != "x" {
		t.Errorf("deploy op services: want [x], got %v", deployOp.Services)
	}
	if state.Pending.Find(journal.PendingRestart) != nil {
		t.Error("restart op should have been cleared")
	}
}

// TestExecuteTogglePlan_AllDeploySingleStep verifies that a deploy step calls
// RunDeploy once with the full services list.
func TestExecuteTogglePlan_AllDeploySingleStep(t *testing.T) {
	var gotServices []string
	deps, _ := makeExecuteDeps(t,
		func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, opts deploy.Opts) error {
			gotServices = opts.Services
			return nil
		},
		nil, nil,
	)
	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingDeploy, Services: []string{"a", "b"}}},
	}
	contributors := []Contributor{
		{Service: "a", Requires: config.RequiresDeploy},
		{Service: "b", Requires: config.RequiresDeploy},
	}
	if err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotServices) != 2 || gotServices[0] != "a" || gotServices[1] != "b" {
		t.Errorf("RunDeploy services: want [a b], got %v", gotServices)
	}
}

// TestExecuteTogglePlan_MixedBatchOrder verifies deploy runs before restart in a
// mixed batch.
func TestExecuteTogglePlan_MixedBatchOrder(t *testing.T) {
	var order []string
	deps, _ := makeExecuteDeps(t,
		func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, _ deploy.Opts) error {
			order = append(order, "deploy")
			return nil
		},
		func(_ lifecycle.RunContext) error { order = append(order, "restart"); return nil },
		nil,
	)
	plan := TogglePlan{
		ApplySteps: []ApplyStep{
			{Kind: journal.PendingDeploy, Services: []string{"a"}},
			{Kind: journal.PendingRestart},
		},
	}
	contributors := []Contributor{
		{Service: "a", Requires: config.RequiresDeploy},
		{Service: "b", Requires: config.RequiresRestart},
	}
	if err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "deploy" || order[1] != "restart" {
		t.Errorf("mixed batch order: want [deploy restart], got %v", order)
	}
}

// TestExecuteTogglePlan_MixedBatchPartialFailure verifies pending stays intact when
// deploy succeeds but restart fails.
func TestExecuteTogglePlan_MixedBatchPartialFailure(t *testing.T) {
	deployCalled := false
	deps, dir := makeExecuteDeps(t,
		func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, _ deploy.Opts) error {
			deployCalled = true
			return nil // deploy succeeds
		},
		func(_ lifecycle.RunContext) error { return fmt.Errorf("restart failed") },
		nil,
	)
	statePath := filepath.Join(dir, "state.yml")
	// Pre-seed both ops.
	if err := journal.AddPendingOps(statePath, []journal.PendingOp{
		{Kind: journal.PendingDeploy, Services: []string{"a"}},
		{Kind: journal.PendingRestart},
	}, "hash1"); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	plan := TogglePlan{
		ApplySteps: []ApplyStep{
			{Kind: journal.PendingDeploy, Services: []string{"a"}},
			{Kind: journal.PendingRestart},
		},
	}
	contributors := []Contributor{
		{Service: "a", Requires: config.RequiresDeploy},
		{Service: "b", Requires: config.RequiresRestart},
	}
	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors})
	if err == nil {
		t.Fatal("expected error from restart failure")
	}
	if !deployCalled {
		t.Error("deploy should have been called")
	}
	// Verify pending is intact — no partial clear.
	state, loadErr := journal.Load(statePath)
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if state.Pending == nil {
		t.Fatal("pending should be intact on partial failure")
	}
	if state.Pending.Find(journal.PendingDeploy) == nil {
		t.Error("deploy pending should remain")
	}
	if state.Pending.Find(journal.PendingRestart) == nil {
		t.Error("restart pending should remain")
	}
}

// TestExecuteTogglePlan_Opts_SuppressPendingClear verifies that executeTogglePlan
// always passes SuppressPendingClear:true to RunDeploy so the inner deploy helper does
// not race-clear pending state that the outer toggle executor owns.
func TestExecuteTogglePlan_Opts_SuppressPendingClear(t *testing.T) {
	var capturedOpts deploy.Opts
	deps, _ := makeExecuteDeps(t,
		func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, opts deploy.Opts) error {
			capturedOpts = opts
			return nil
		},
		nil, nil,
	)
	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingDeploy, Services: []string{"x"}}},
	}
	contributors := []Contributor{{Service: "x", Requires: config.RequiresDeploy}}
	if err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !capturedOpts.SuppressPendingClear {
		t.Error("RunDeploy must be called with SuppressPendingClear:true so the toggle executor owns the pending clear")
	}
}

// TestExecuteTogglePlan_MixedBatchPartialFailure_SimulatedRealDeploy verifies that even
// when RunDeploy would normally clear pending state (simulating real runDeployHelper
// behaviour), pending stays fully intact when the subsequent restart step fails.
func TestExecuteTogglePlan_MixedBatchPartialFailure_SimulatedRealDeploy(t *testing.T) {
	// baseDir is set after makeExecuteDeps returns; the closure reads it via
	// pointer so the assignment is visible when the stub is invoked.
	var baseDir string
	deps, dir := makeExecuteDeps(t,
		func(_ context.Context, _ *cobra.Command, _ *cmdctx.RootFlags, opts deploy.Opts) error {
			// Simulate what runDeployHelper does when SuppressPendingClear is false:
			// clear deploy pending.  This should NOT happen when the flag is set,
			// but we still clear here to show the regression test catches it if the
			// flag is ever accidentally dropped.
			if !opts.SuppressPendingClear {
				// Replicate the inner clear — this is the bug path.
				_ = journal.ClearPendingForServices(
					filepath.Join(baseDir, "state.yml"), journal.PendingDeploy, opts.Services,
				)
			}
			return nil // deploy succeeds
		},
		func(_ lifecycle.RunContext) error { return fmt.Errorf("restart failed") },
		nil,
	)
	baseDir = dir
	statePath := filepath.Join(dir, "state.yml")
	if err := journal.AddPendingOps(statePath, []journal.PendingOp{
		{Kind: journal.PendingDeploy, Services: []string{"a"}},
		{Kind: journal.PendingRestart},
	}, "hash1"); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	plan := TogglePlan{
		ApplySteps: []ApplyStep{
			{Kind: journal.PendingDeploy, Services: []string{"a"}},
			{Kind: journal.PendingRestart},
		},
	}
	contributors := []Contributor{
		{Service: "a", Requires: config.RequiresDeploy},
		{Service: "b", Requires: config.RequiresRestart},
	}
	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors})
	if err == nil {
		t.Fatal("expected error from restart failure")
	}
	state, loadErr := journal.Load(statePath)
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if state.Pending == nil {
		t.Fatal("pending should be intact on partial failure")
	}
	if state.Pending.Find(journal.PendingDeploy) == nil {
		t.Error("deploy pending should remain: SuppressPendingClear must have been set on RunDeploy call")
	}
	if state.Pending.Find(journal.PendingRestart) == nil {
		t.Error("restart pending should remain")
	}
}

// TestExecuteTogglePlan_ContributorsRequiredError verifies the guard fires when
// Contributors is empty but plan has apply steps.
func TestExecuteTogglePlan_ContributorsRequiredError(t *testing.T) {
	deps, _ := makeExecuteDeps(t, nil, nil, nil)
	plan := TogglePlan{
		ApplySteps: []ApplyStep{{Kind: journal.PendingRestart}},
	}
	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{})
	if err == nil {
		t.Fatal("expected error for missing Contributors, got nil")
	}
}

// TestExecuteTogglePlan_BeforeHookFailureShortCircuits verifies that a before-hook
// failure stops execution before the apply phase.
func TestExecuteTogglePlan_BeforeHookFailureShortCircuits(t *testing.T) {
	restartCalled := false
	deps, _ := makeExecuteDeps(t,
		nil,
		func(_ lifecycle.RunContext) error { restartCalled = true; return nil },
		func(_ context.Context, rc runtime.RunContext) error {
			return fmt.Errorf("hook failed: %s", rc.Cmd.ID)
		},
	)
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(makeShellCmd("foo:pre"))
	deps.CmdReg = reg
	deps.Cfg = &config.DevboxConfig{}

	plan := TogglePlan{
		BeforeSteps: []PlanStep{{CommandID: "foo:pre"}},
		ApplySteps:  []ApplyStep{{Kind: journal.PendingRestart}},
	}
	contributors := []Contributor{{Service: "foo", Requires: config.RequiresRestart}}
	err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{Contributors: contributors})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if restartCalled {
		t.Error("restart should NOT run after before-hook failure")
	}
}

// makeShellCmd is a test helper that returns a minimal shell CommandDef with the given ID.
func makeShellCmd(id string) *model.CommandDef {
	return &model.CommandDef{ID: id, Type: model.CommandTypeShell, Cmd: "echo ok"}
}

// TestExecuteTogglePlan_NoneContributorsNoLockAcquired verifies that a plan with
// only RequiresNone contributors writes no pending and acquires no lock.
func TestExecuteTogglePlan_NoneContributorsNoLockAcquired(t *testing.T) {
	deps, dir := makeExecuteDeps(t, nil, nil, nil)
	statePath := filepath.Join(dir, "state.yml")

	// Empty apply steps (all RequiresNone)
	plan := TogglePlan{}
	if err := executeTogglePlan(context.Background(), deps, plan, ExecuteOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// State file should not have been created (no lock, no write).
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("state file should not exist for all-RequiresNone plan")
	}
}

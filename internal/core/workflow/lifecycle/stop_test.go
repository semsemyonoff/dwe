package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

func TestRunStop_MissingLifecycleYML(t *testing.T) {
	// Stub RunPhasesFunc: default stop config contains a type:devbox step
	// (docker down) whose os.Executable() call would recursively re-run the test binary.
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	ctx := StopContext{ConfigPath: cfgPath}
	if err := RunStop(ctx); err != nil {
		t.Fatalf("RunStop with missing lifecycle.yml should succeed (built-in default), got: %v", err)
	}
}

func TestRunStop_MissingStopSection(t *testing.T) {
	// Stub RunPhasesFunc for the same reason as TestRunStop_MissingLifecycleYML.
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	ctx := StopContext{ConfigPath: cfgPath}
	if err := RunStop(ctx); err != nil {
		t.Fatalf("RunStop with no stop: section should succeed (auto-reap only), got: %v", err)
	}
}

func TestRunStop_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: \"Goodbye!\"\n  phases:\n    - name: down\n      steps:\n        - name: noop\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	ctx := StopContext{ConfigPath: cfgPath}
	if err := RunStop(ctx); err != nil {
		t.Errorf("unexpected error on happy path: %v", err)
	}
}

func TestRunStop_ClearsPendingRestart_KeepsPendingDeploy(t *testing.T) {
	// No lifecycle.yml → default stop config (type:devbox step) → stub required.
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	// Seed both kinds of pending so we can verify the kind-scoped clear.
	if err := journal.AddPendingOps(statePath, []journal.PendingOp{
		{Kind: journal.PendingRestart},
		{Kind: journal.PendingDeploy, Services: []string{"web"}},
	}, "stub"); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	if err := RunStop(StopContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunStop: %v", err)
	}

	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading state after stop: %v", err)
	}
	if state.Pending == nil {
		t.Fatal("pending must not be wiped; deploy op should survive stop")
	}
	if state.Pending.Find(journal.PendingRestart) != nil {
		t.Errorf("pending restart must be cleared after full-stack stop, got %+v", state.Pending)
	}
	if state.Pending.Find(journal.PendingDeploy) == nil {
		t.Errorf("pending deploy must survive stop (artifact state outlasts runtime), got %+v", state.Pending)
	}
}

func TestEnsureStopConfig_NilConfig(t *testing.T) {
	out, defaulted := EnsureStopConfig(nil)
	if out == nil {
		t.Fatal("EnsureStopConfig(nil) returned nil")
	}
	if !defaulted {
		t.Error("EnsureStopConfig(nil) must return defaulted=true")
	}
	if out.Phases[0].Name != AutoReapPhaseName {
		t.Errorf("expected phase name %q, got %q", AutoReapPhaseName, out.Phases[0].Name)
	}
	if out.FinalMessage == "" {
		t.Error("expected default final message")
	}
}

func TestEnsureStopConfig_NilStop(t *testing.T) {
	out, defaulted := EnsureStopConfig(&config.LifecycleConfig{Stop: nil})
	if !defaulted {
		t.Error("EnsureStopConfig(cfg with nil Stop) must return defaulted=true")
	}
	if out.Phases[0].Name != AutoReapPhaseName {
		t.Fatalf("expected reap phase first, got %+v", out.Phases)
	}
}

func TestEnsureStopConfig_PrependsToUserPhases(t *testing.T) {
	user := config.DeployPhase{Name: "user-down"}
	in := &config.LifecycleConfig{
		Stop: &config.LifecycleStopConfig{
			FinalMessage: "Bye",
			Phases:       []config.DeployPhase{user},
		},
	}
	out, defaulted := EnsureStopConfig(in)
	if defaulted {
		t.Error("EnsureStopConfig with user stop: section must return defaulted=false")
	}
	if len(out.Phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(out.Phases))
	}
	if out.Phases[0].Name != AutoReapPhaseName {
		t.Errorf("reap phase must be first, got %q", out.Phases[0].Name)
	}
	if out.Phases[1].Name != "user-down" {
		t.Errorf("user phase must follow, got %q", out.Phases[1].Name)
	}
	if out.FinalMessage != "Bye" {
		t.Errorf("expected final message preserved, got %q", out.FinalMessage)
	}
	// Ensure the caller's slice was not aliased / mutated.
	if len(in.Stop.Phases) != 1 {
		t.Errorf("EnsureStopConfig must not mutate the input slice; got len=%d", len(in.Stop.Phases))
	}
}

func TestEnsureStopConfig_DefaultsFinalMessage(t *testing.T) {
	in := &config.LifecycleConfig{
		Stop: &config.LifecycleStopConfig{FinalMessage: "", Phases: nil},
	}
	out, _ := EnsureStopConfig(in)
	if out.FinalMessage == "" {
		t.Error("expected fallback final message when empty")
	}
}

func TestDefaultStopConfig_Shape(t *testing.T) {
	d := DefaultStopConfig()
	if d.FinalMessage != "Project is stopped. Have a nice day!" {
		t.Errorf("FinalMessage = %q", d.FinalMessage)
	}
	if len(d.Phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(d.Phases))
	}
	ph := d.Phases[0]
	if ph.Name == AutoReapPhaseName {
		t.Error("DefaultStopConfig must NOT include the auto-reap phase")
	}
	if len(ph.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(ph.Steps))
	}
	step := ph.Steps[0]
	if step.Type != "dwe" || step.Cmd != "docker down" {
		t.Errorf("step = {type:%q cmd:%q}, want {type:devbox cmd:docker down}", step.Type, step.Cmd)
	}
}

func TestEnsureStopConfig_NilConfig_PhasesMatchDefault(t *testing.T) {
	out, _ := EnsureStopConfig(nil)
	d := DefaultStopConfig()
	// After prepending auto-reap, non-reap phases must equal DefaultStopConfig phases.
	nonReap := out.Phases[1:]
	if len(nonReap) != len(d.Phases) {
		t.Fatalf("non-reap phase count = %d, want %d", len(nonReap), len(d.Phases))
	}
	for i, ph := range d.Phases {
		if nonReap[i].Name != ph.Name {
			t.Errorf("phase[%d].Name = %q, want %q", i, nonReap[i].Name, ph.Name)
		}
	}
}

func TestRunStop_MissingLifecycleYML_FiresOnDefaultUsed(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	var called []DefaultedPipeline
	ctx := StopContext{
		ConfigPath: cfgPath,
		OnDefaultUsed: func(p DefaultedPipeline) {
			called = append(called, p)
		},
	}
	if err := RunStop(ctx); err != nil {
		t.Fatalf("RunStop: %v", err)
	}
	if len(called) != 1 || called[0] != DefaultedStop {
		t.Errorf("OnDefaultUsed calls = %v, want [%q]", called, DefaultedStop)
	}
}

func TestRunStop_WithStopSection_DoesNotFireOnDefaultUsed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: bye\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	var called []DefaultedPipeline
	ctx := StopContext{
		ConfigPath: cfgPath,
		OnDefaultUsed: func(p DefaultedPipeline) {
			called = append(called, p)
		},
	}
	if err := RunStop(ctx); err != nil {
		t.Fatalf("RunStop: %v", err)
	}
	if len(called) != 0 {
		t.Errorf("OnDefaultUsed must not fire when stop: section is present, got %v", called)
	}
}

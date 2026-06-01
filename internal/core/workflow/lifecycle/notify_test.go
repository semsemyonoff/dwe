package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/notify"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
)

type recordingNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (r *recordingNotifier) Notify(_ context.Context, ev notify.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingNotifier) snapshot() []notify.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notify.Event, len(r.events))
	copy(out, r.events)
	return out
}

// pointHomeAtTempDir isolates the HOME-relative userconfig path for the
// test process so global userconfig reads can't accidentally pick up
// the developer's real ~/.config/devbox/config.
func pointHomeAtTempDir(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// installRecordingNotifier swaps the package-level newNotifier with a
// recorder for the duration of the test and returns the recorder.
func installRecordingNotifier(t *testing.T) *recordingNotifier {
	t.Helper()
	rec := &recordingNotifier{}
	orig := newNotifier
	newNotifier = func(_ *userpkg.Config) notifier { return rec }
	t.Cleanup(func() { newNotifier = orig })
	return rec
}

func TestRunRun_FiresNotifyOnSuccess(t *testing.T) {
	pointHomeAtTempDir(t)
	rec := installRecordingNotifier(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "ready")

	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}

	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Kind != notify.OpRun {
		t.Errorf("Kind = %v, want OpRun", ev.Kind)
	}
	if ev.Outcome != notify.OutcomeSuccess {
		t.Errorf("Outcome = %v, want OutcomeSuccess", ev.Outcome)
	}
	if ev.Project != "test" {
		t.Errorf("Project = %q, want %q", ev.Project, "test")
	}
	if ev.Err != nil {
		t.Errorf("Err = %v, want nil", ev.Err)
	}
}

func TestRunRun_FiresNotifyOnFailure(t *testing.T) {
	pointHomeAtTempDir(t)
	rec := installRecordingNotifier(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)
	// Write a lifecycle.yml with a YAML parse error to trigger a load failure.
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte("run: [\ninvalid yaml\n"), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	err := RunRun(RunContext{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error from invalid lifecycle.yml, got nil")
	}

	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Outcome != notify.OutcomeFailure {
		t.Errorf("Outcome = %v, want OutcomeFailure", ev.Outcome)
	}
	if ev.Err == nil {
		t.Error("Err should be populated on failure")
	}
	if ev.Project != "test" {
		t.Errorf("Project = %q, want %q (main cfg loaded before lifecycle.yml)", ev.Project, "test")
	}
}

func TestRunRun_EarlyConfigFailure_PopulatesEmptyProject(t *testing.T) {
	pointHomeAtTempDir(t)
	rec := installRecordingNotifier(t)
	dir := t.TempDir()
	// No devbox.yml — RunRun fails at the first config.LoadConfig step.
	cfgPath := filepath.Join(dir, "devbox.yml")

	if err := RunRun(RunContext{ConfigPath: cfgPath}); err == nil {
		t.Fatal("expected error, got nil")
	}

	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Project != "" {
		t.Errorf("Project = %q, want empty (cfg never loaded)", evs[0].Project)
	}
	if evs[0].Outcome != notify.OutcomeFailure {
		t.Errorf("Outcome = %v, want OutcomeFailure", evs[0].Outcome)
	}
}

func TestRunRun_SkipNotify_NoEvent(t *testing.T) {
	pointHomeAtTempDir(t)
	rec := installRecordingNotifier(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "ready")

	if err := RunRun(RunContext{ConfigPath: cfgPath, SkipNotify: true}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}

	if got := len(rec.snapshot()); got != 0 {
		t.Errorf("want 0 events with SkipNotify=true, got %d", got)
	}
}

func TestRunRestart_PropagatesSkipNotify(t *testing.T) {
	pointHomeAtTempDir(t)
	rec := installRecordingNotifier(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yaml := `stop:
  final_message: "Stopped."
  phases:
    - name: s
      steps:
        - name: noop
          type: shell
          cmd: "true"
run:
  final_message: "Ready."
  phases:
    - name: s
      steps:
        - name: noop
          type: shell
          cmd: "true"
`
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("write lifecycle.yml: %v", err)
	}

	if err := RunRestart(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRestart: %v", err)
	}

	if got := len(rec.snapshot()); got != 0 {
		t.Errorf("RunRestart should fire 0 notifications, got %d", got)
	}
}

package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"devbox-cli/internal/core/notify"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/shared/tpl"
	"devbox-cli/internal/userconfig"
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

func installRecordingNotifier(t *testing.T) *recordingNotifier {
	t.Helper()
	rec := &recordingNotifier{}
	orig := newNotifier
	newNotifier = func(_ *userconfig.Config) notifier { return rec }
	t.Cleanup(func() { newNotifier = orig })
	return rec
}

// countingUserconfigLoad swaps the userconfigLoadFunc seam with a counter.
func countingUserconfigLoad(t *testing.T) *int32 {
	t.Helper()
	var n int32
	orig := userconfigLoadFunc
	userconfigLoadFunc = func(projectRoot string) (*userconfig.Config, error) {
		atomic.AddInt32(&n, 1)
		return orig(projectRoot)
	}
	t.Cleanup(func() { userconfigLoadFunc = orig })
	return &n
}

func basicShellCmd(notifyFlag bool) *CommandDef {
	return &CommandDef{
		ID:     "test.notify_cmd",
		Type:   CommandTypeShell,
		Files:  map[string]FileSpec{},
		Cmd:    "true",
		Notify: notifyFlag,
	}
}

func failingShellCmd(notifyFlag bool) *CommandDef {
	return &CommandDef{
		ID:     "test.failing_cmd",
		Type:   CommandTypeShell,
		Files:  map[string]FileSpec{},
		Cmd:    "exit 1",
		Notify: notifyFlag,
	}
}

func TestRunCommand_NotifyTrue_Success_FiresNotification(t *testing.T) {
	rec := installRecordingNotifier(t)
	rc := RunContext{
		Cmd:    basicShellCmd(true),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	if err := RunCommand(context.Background(), rc); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Kind != notify.OpCommand {
		t.Errorf("Kind = %v, want OpCommand", evs[0].Kind)
	}
	if evs[0].Outcome != notify.OutcomeSuccess {
		t.Errorf("Outcome = %v, want OutcomeSuccess", evs[0].Outcome)
	}
	if !strings.HasPrefix(evs[0].Operation, "command:") {
		t.Errorf("Operation = %q, want command:* prefix", evs[0].Operation)
	}
}

func TestRunCommand_NotifyTrue_Failure_FiresNotification(t *testing.T) {
	rec := installRecordingNotifier(t)
	rc := RunContext{
		Cmd:    failingShellCmd(true),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	if err := RunCommand(context.Background(), rc); err == nil {
		t.Fatal("expected error, got nil")
	}
	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Outcome != notify.OutcomeFailure {
		t.Errorf("Outcome = %v, want OutcomeFailure", evs[0].Outcome)
	}
	if evs[0].Err == nil {
		t.Error("Err should be populated on failure")
	}
}

func TestRunCommand_NotifyTrue_SkipNotify_NoEvent(t *testing.T) {
	rec := installRecordingNotifier(t)
	rc := RunContext{
		Cmd:        basicShellCmd(true),
		SkipNotify: true,
		Stdout:     &bytes.Buffer{},
		Stderr:     &bytes.Buffer{},
	}
	if err := RunCommand(context.Background(), rc); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if got := len(rec.snapshot()); got != 0 {
		t.Errorf("want 0 events with SkipNotify=true, got %d", got)
	}
}

func TestRunCommand_NotifyFalse_NoEvent(t *testing.T) {
	rec := installRecordingNotifier(t)
	rc := RunContext{
		Cmd:    basicShellCmd(false),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	if err := RunCommand(context.Background(), rc); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if got := len(rec.snapshot()); got != 0 {
		t.Errorf("want 0 events with Notify=false, got %d", got)
	}
}

func TestRunCommand_NotifyFalse_NoUserconfigLoad(t *testing.T) {
	installRecordingNotifier(t)
	counter := countingUserconfigLoad(t)
	rc := RunContext{
		Cmd:    basicShellCmd(false),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	if err := RunCommand(context.Background(), rc); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if got := atomic.LoadInt32(counter); got != 0 {
		t.Errorf("userconfig.Load invocations = %d, want 0 for Notify=false", got)
	}
}

// TestRunCommand_NotifyTrue_PreRunFailure verifies the notifier fires when
// an early step (NewRunner here, via an unsupported type) returns an error.
func TestRunCommand_NotifyTrue_PreRunFailure(t *testing.T) {
	rec := installRecordingNotifier(t)
	rc := RunContext{
		Cmd: &CommandDef{
			ID:     "test.bad_type",
			Type:   CommandType("nonsense"),
			Files:  map[string]FileSpec{},
			Notify: true,
		},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	err := RunCommand(context.Background(), rc)
	if err == nil {
		t.Fatal("expected error from bogus type, got nil")
	}
	var bad *ErrUnsupportedType
	if !errors.As(err, &bad) {
		t.Fatalf("err type = %T, want *ErrUnsupportedType", err)
	}
	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 event from pre-run failure, got %d", len(evs))
	}
	if evs[0].Outcome != notify.OutcomeFailure {
		t.Errorf("Outcome = %v, want OutcomeFailure", evs[0].Outcome)
	}
}

// TestRunCommand_NilCmd_ReturnsErrorNoNotify verifies the contract that
// RunCommand defensively rejects a nil Cmd: it returns ErrNilCmd before
// touching any field that would nil-deref, and the notifier path never
// fires (the nil-Cmd guard runs ahead of every Notify dispatch).
func TestRunCommand_NilCmd_ReturnsErrorNoNotify(t *testing.T) {
	rec := installRecordingNotifier(t)
	rc := RunContext{
		Cmd:    nil,
		Render: &tpl.RenderContext{},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	err := RunCommand(context.Background(), rc)
	if !errors.Is(err, ErrNilCmd) {
		t.Fatalf("RunCommand(nil Cmd) = %v, want ErrNilCmd", err)
	}
	if got := len(rec.snapshot()); got != 0 {
		t.Errorf("want 0 notify events for nil Cmd, got %d", got)
	}
}

// TestRunCommand_NotifyTrue_CommandAborted_NoEvent verifies that an explicit
// user confirmation refusal (commandAbortedError) does not fire a notification.
func TestRunCommand_NotifyTrue_CommandAborted_NoEvent(t *testing.T) {
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = origIsInteractive })
	ui.IsInteractiveFn = func(io.Reader) bool { return false }

	rec := installRecordingNotifier(t)
	cmd := &CommandDef{
		ID:           "test.abort_cmd",
		Type:         CommandTypeShell,
		Files:        map[string]FileSpec{},
		Cmd:          "true",
		Notify:       true,
		Confirmation: true,
	}
	rc := RunContext{
		Cmd:    cmd,
		Render: &tpl.RenderContext{},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Stdin:  bytes.NewBufferString("n\n"),
	}
	err := RunCommand(context.Background(), rc)
	if err == nil {
		t.Fatal("expected error when user aborts")
	}
	var aborted *commandAbortedError
	if !errors.As(err, &aborted) {
		t.Errorf("err type = %T, want *commandAbortedError", err)
	}
	if got := len(rec.snapshot()); got != 0 {
		t.Errorf("want 0 notify events on command abort, got %d", got)
	}
}

func TestRunCommand_NotifyTrue_PopulatesProjectName(t *testing.T) {
	rec := installRecordingNotifier(t)
	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "demo"
	rc := RunContext{
		Cmd:    basicShellCmd(true),
		Config: cfg,
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	if err := RunCommand(context.Background(), rc); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	evs := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Project != "demo" {
		t.Errorf("Project = %q, want %q", evs[0].Project, "demo")
	}
}

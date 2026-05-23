package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"devbox-cli/internal/notify"
	"devbox-cli/internal/userconfig"

	"github.com/spf13/cobra"
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

// swapNewNotifier installs a recording fake for the package-level
// newNotifier seam and returns the recorder + a restore function.
func swapNewNotifier(t *testing.T) *recordingNotifier {
	t.Helper()
	rec := &recordingNotifier{}
	prev := newNotifier
	newNotifier = func(_ *userconfig.Config) notifier { return rec }
	t.Cleanup(func() { newNotifier = prev })
	return rec
}

// pointHomeAtTempDir isolates os.UserConfigDir resolution for the test
// process so global userconfig reads can't accidentally pick up the
// developer's real config.
func pointHomeAtTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("AppData", filepath.Join(dir, "AppData"))
	return dir
}

// TestDeployRunCmd_NotifierFiresOnEarlyConfigLoadFailure guards the
// panic-safe ordering in the hookpoint contract: the notifier defer is
// installed BEFORE main config load, so a bogus config path still
// produces a failure notification with Project == "".
func TestDeployRunCmd_NotifierFiresOnEarlyConfigLoadFailure(t *testing.T) {
	pointHomeAtTempDir(t)
	rec := swapNewNotifier(t)

	// configPath points at a non-existent file → LoadConfig fails.
	flags := &rootFlags{configPath: filepath.Join(t.TempDir(), "does-not-exist.yml")}

	err := deployRunCmd(&cobra.Command{}, flags, "", false, false, true, false)
	if err == nil {
		t.Fatal("expected deployRunCmd to fail when config missing")
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 notify event, got %d", len(events))
	}
	ev := events[0]
	if ev.Kind != notify.OpDeploy {
		t.Errorf("Kind = %v, want OpDeploy", ev.Kind)
	}
	if ev.Outcome != notify.OutcomeFailure {
		t.Errorf("Outcome = %v, want OutcomeFailure", ev.Outcome)
	}
	if ev.Err == nil {
		t.Error("Event.Err should carry the deploy error")
	}
	if ev.Project != "" {
		t.Errorf("Project = %q, want empty (main config never loaded)", ev.Project)
	}
	if ev.Operation != "deploy" {
		t.Errorf("Operation = %q, want \"deploy\"", ev.Operation)
	}
}

// TestDeployRunCmd_MalformedUserConfigDoesNotBlockDeploy verifies the
// hookpoint contract: a parser error in the user config is logged and
// degrades to a nil cfg, but deploy still proceeds (and fails for its
// own reasons, with the notification still firing).
func TestDeployRunCmd_MalformedUserConfigDoesNotBlockDeploy(t *testing.T) {
	home := pointHomeAtTempDir(t)
	// Write a malformed global userconfig (dotted key rejected by parser).
	globalDir := filepath.Join(home, ".config", "devbox")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(globalDir, "config")
	if err := os.WriteFile(bad, []byte("notify.enabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// On macOS os.UserConfigDir uses ~/Library/Application Support, so
	// also write a malformed file there to cover both OS resolutions.
	macDir := filepath.Join(home, "Library", "Application Support", "devbox")
	if err := os.MkdirAll(macDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(macDir, "config"), []byte("notify.enabled = true\n"), 0o600)
	}

	rec := swapNewNotifier(t)
	flags := &rootFlags{configPath: filepath.Join(t.TempDir(), "does-not-exist.yml")}

	err := deployRunCmd(&cobra.Command{}, flags, "", false, false, true, false)
	if err == nil {
		t.Fatal("expected deployRunCmd to fail when devbox config missing")
	}
	// The error must come from the devbox config load path, not the
	// userconfig parser — userconfig failures are downgraded to warnings.
	if msg := err.Error(); !strings.Contains(msg, "loading config") {
		t.Errorf("expected 'loading config' error, got %q", msg)
	}
	if got := len(rec.snapshot()); got != 1 {
		t.Fatalf("expected exactly 1 notify event, got %d", got)
	}
	if ev := rec.snapshot()[0]; ev.Outcome != notify.OutcomeFailure {
		t.Errorf("Outcome = %v, want OutcomeFailure", ev.Outcome)
	}
}

// TestDeployRunCmd_OutcomeFromErrMapping verifies the helper used by
// the defer correctly maps nil/non-nil errors.
func TestDeployRunCmd_OutcomeFromErrMapping(t *testing.T) {
	if got := notify.OutcomeFromErr(nil); got != notify.OutcomeSuccess {
		t.Errorf("OutcomeFromErr(nil) = %v, want OutcomeSuccess", got)
	}
	if got := notify.OutcomeFromErr(context.Canceled); got != notify.OutcomeFailure {
		t.Errorf("OutcomeFromErr(err) = %v, want OutcomeFailure", got)
	}
}

// TestDeployRunCmd_NotifierSeamDefaultProducesRealNotifier sanity-
// checks that the default newNotifier returns a real *notify.Notifier
// (not a nil interface) so production hookpoints never panic on
// .Notify.
func TestDeployRunCmd_NotifierSeamDefaultProducesRealNotifier(t *testing.T) {
	got := newNotifier(nil)
	if got == nil {
		t.Fatal("default newNotifier returned nil interface")
	}
	// Should be a no-op on nil cfg — calling Notify must not panic.
	got.Notify(context.Background(), notify.Event{Kind: notify.OpDeploy})
}

// TestDeployCancelledError_ExitCode verifies that deployCancelledError
// returns exit code 0 so fang suppresses the "Error:" line and exits
// cleanly when the user deliberately cancels.
func TestDeployCancelledError_ExitCode(t *testing.T) {
	e := &deployCancelledError{}
	if got := e.ExitCode(); got != 0 {
		t.Errorf("ExitCode() = %d, want 0", got)
	}
	if e.Error() != "deploy cancelled" {
		t.Errorf("Error() = %q, want \"deploy cancelled\"", e.Error())
	}
}

// TestDeployRunCmd_SuppressedConditions guards against regressions in the
// three branches where the notifier defer deliberately skips firing:
//   - lockHeldError  — another process holds the deploy lock
//   - deployCancelledError — user explicitly cancelled the interactive dialog
//   - isNoop — project already up-to-date, nothing ran
//
// The suppression guard in deployRunCmd is:
//
//	if errors.As(err, new(*lockHeldError)) || errors.As(err, new(*deployCancelledError)) || isNoop { return }
//
// Full integration paths (cross-process lock contention, deployed-state
// journal, interactive cancellation) require test infrastructure beyond
// unit scope; these tests verify the error-type matching that drives the
// guard so that any refactoring of the error types keeps the guard intact.
func TestDeployRunCmd_SuppressedConditions(t *testing.T) {
	t.Run("lockHeldError matches suppression guard", func(t *testing.T) {
		var err error = &lockHeldError{operation: "deploy", pid: 999}
		if !errors.As(err, new(*lockHeldError)) {
			t.Error("errors.As should match *lockHeldError")
		}
		wrapped := fmt.Errorf("outer: %w", err)
		if !errors.As(wrapped, new(*lockHeldError)) {
			t.Error("errors.As should match *lockHeldError through wrapping")
		}
	})
	t.Run("deployCancelledError matches suppression guard", func(t *testing.T) {
		var err error = &deployCancelledError{}
		if !errors.As(err, new(*deployCancelledError)) {
			t.Error("errors.As should match *deployCancelledError")
		}
		wrapped := fmt.Errorf("outer: %w", err)
		if !errors.As(wrapped, new(*deployCancelledError)) {
			t.Error("errors.As should match *deployCancelledError through wrapping")
		}
	})
	t.Run("normal error does not match suppression guard", func(t *testing.T) {
		err := errors.New("some deploy failure")
		if errors.As(err, new(*lockHeldError)) || errors.As(err, new(*deployCancelledError)) {
			t.Error("ordinary error should not trigger suppression guard")
		}
	})
	t.Run("lockHeldError ExitCode is 2", func(t *testing.T) {
		lhe := &lockHeldError{operation: "deploy", pid: 12345}
		if got := lhe.ExitCode(); got != 2 {
			t.Errorf("lockHeldError.ExitCode() = %d, want 2", got)
		}
	})
}

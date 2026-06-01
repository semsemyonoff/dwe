package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/notify"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/shared/lock"

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
	return append([]notify.Event(nil), r.events...)
}

// swapNewNotifier replaces the same-package notifier seam with a recorder
// and restores it via t.Cleanup.
func swapNewNotifier(t *testing.T) *recordingNotifier {
	t.Helper()
	rec := &recordingNotifier{}
	prev := newNotifier
	newNotifier = func(_ *userpkg.Config) cmdctx.Notifier { return rec }
	t.Cleanup(func() { newNotifier = prev })
	return rec
}

// pointHomeAtTempDir isolates HOME so userconfig reads don't pick up the
// developer's real ~/.config/devbox/config.
func pointHomeAtTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

// TestDeployRunCmd_NotifierFiresOnEarlyConfigLoadFailure guards the
// notifier-defer-before-config-load contract: an unreadable devbox.yml
// must still produce a failure notification (with Project=="").
func TestDeployRunCmd_NotifierFiresOnEarlyConfigLoadFailure(t *testing.T) {
	pointHomeAtTempDir(t)
	rec := swapNewNotifier(t)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(t.TempDir(), "does-not-exist.yml")}
	err := deployRunCmd(&cobra.Command{}, flags, "", false, false, true, false, false)
	if err == nil {
		t.Fatal("expected deploy to fail when config missing")
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Kind != notify.OpDeploy {
		t.Errorf("Kind = %v, want OpDeploy", ev.Kind)
	}
	if ev.Outcome != notify.OutcomeFailure {
		t.Errorf("Outcome = %v, want OutcomeFailure", ev.Outcome)
	}
	if ev.Err == nil {
		t.Error("Event.Err must carry the underlying error")
	}
	if ev.Project != "" {
		t.Errorf("Project = %q, want empty (config never loaded)", ev.Project)
	}
}

// TestDeployRunCmd_MalformedUserConfigDoesNotBlockDeploy: a parser error
// in the global userconfig must degrade to nil cfg + slog.Warn, never
// short-circuit deploy.
func TestDeployRunCmd_MalformedUserConfigDoesNotBlockDeploy(t *testing.T) {
	home := pointHomeAtTempDir(t)
	globalDir := filepath.Join(home, ".config", "devbox")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config"), []byte("notify.enabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := swapNewNotifier(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(t.TempDir(), "does-not-exist.yml")}

	err := deployRunCmd(&cobra.Command{}, flags, "", false, false, true, false, false)
	if err == nil {
		t.Fatal("expected deploy to fail when devbox config missing")
	}
	if msg := err.Error(); !strings.Contains(msg, "loading config") {
		t.Errorf("err = %q, want one mentioning 'loading config'", msg)
	}
	if got := len(rec.snapshot()); got != 1 {
		t.Fatalf("got %d events, want 1", got)
	}
}

// TestDeployCancelledError_ExitCode locks down the contract that an
// explicit user-cancel returns exit code 0 (fang suppresses "Error:").
func TestDeployCancelledError_ExitCode(t *testing.T) {
	e := &deployCancelledError{}
	if got := e.ExitCode(); got != 0 {
		t.Errorf("ExitCode = %d, want 0", got)
	}
	if e.Error() != "deploy cancelled" {
		t.Errorf("Error = %q, want \"deploy cancelled\"", e.Error())
	}
}

// TestDeployRunCmd_NotifySuppressionGuard verifies the error-type matching
// that drives the notifier-defer suppression check. Lock-held and explicit-
// cancel must both be matchable through wrapping; ordinary errors must not.
func TestDeployRunCmd_NotifySuppressionGuard(t *testing.T) {
	t.Run("lock held matches", func(t *testing.T) {
		err := error(&lock.ProjectLockHeldError{Operation: "deploy", PID: 999})
		if !errors.As(err, new(*lock.ProjectLockHeldError)) {
			t.Error("direct match failed")
		}
		if !errors.As(fmt.Errorf("wrap: %w", err), new(*lock.ProjectLockHeldError)) {
			t.Error("match through wrapping failed")
		}
	})
	t.Run("deploy cancelled matches", func(t *testing.T) {
		err := error(&deployCancelledError{})
		if !errors.As(err, new(*deployCancelledError)) {
			t.Error("direct match failed")
		}
		if !errors.As(fmt.Errorf("wrap: %w", err), new(*deployCancelledError)) {
			t.Error("match through wrapping failed")
		}
	})
	t.Run("ordinary error does not match", func(t *testing.T) {
		err := errors.New("some deploy failure")
		if errors.As(err, new(*lock.ProjectLockHeldError)) || errors.As(err, new(*deployCancelledError)) {
			t.Error("guard must not fire on ordinary errors")
		}
	})
}

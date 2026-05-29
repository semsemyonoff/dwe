package deploy

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"devbox-cli/internal/core/ui/widgets"
)

// swapMissingDepsConfirm replaces deployMissingDepsConfirmFn for the duration
// of a test and captures the title argument passed to it.
func swapMissingDepsConfirm(t *testing.T, ok bool, retErr error) *string {
	t.Helper()
	var capturedTitle string
	prev := deployMissingDepsConfirmFn
	deployMissingDepsConfirmFn = func(title, _, _ string) (bool, error) {
		capturedTitle = title
		return ok, retErr
	}
	t.Cleanup(func() { deployMissingDepsConfirmFn = prev })
	return &capturedTitle
}

func newConfirmTestCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})
	return cmd, &errBuf
}

func TestConfirmMissingDeps_InteractiveCancelButton(t *testing.T) {
	title := swapMissingDepsConfirm(t, false, nil)
	cmd, _ := newConfirmTestCmd()

	err := confirmMissingDeps(cmd, []string{"web"}, []string{"db", "cache"}, true)
	if err == nil {
		t.Fatal("expected deployCancelledError, got nil")
	}
	if !errors.As(err, new(*deployCancelledError)) {
		t.Errorf("err = %T, want *deployCancelledError", err)
	}
	if !strings.Contains(*title, "missing: db, cache") {
		t.Errorf("title = %q, want it to list missing deps", *title)
	}
}

func TestConfirmMissingDeps_InteractiveEsc(t *testing.T) {
	swapMissingDepsConfirm(t, false, widgets.ErrCancelled)
	cmd, _ := newConfirmTestCmd()

	err := confirmMissingDeps(cmd, []string{"web"}, []string{"db"}, true)
	if err == nil {
		t.Fatal("expected deployCancelledError, got nil")
	}
	if !errors.As(err, new(*deployCancelledError)) {
		t.Errorf("err = %T, want *deployCancelledError", err)
	}
}

func TestConfirmMissingDeps_InteractiveAccept(t *testing.T) {
	swapMissingDepsConfirm(t, true, nil)
	cmd, _ := newConfirmTestCmd()

	if err := confirmMissingDeps(cmd, []string{"web"}, []string{"db"}, true); err != nil {
		t.Fatalf("expected nil (proceed), got %v", err)
	}
}

func TestConfirmMissingDeps_InteractiveOtherErrorPropagates(t *testing.T) {
	sentinel := errors.New("form blew up")
	swapMissingDepsConfirm(t, false, sentinel)
	cmd, _ := newConfirmTestCmd()

	err := confirmMissingDeps(cmd, []string{"web"}, []string{"db"}, true)
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want it to wrap sentinel", err)
	}
	if errors.As(err, new(*deployCancelledError)) {
		t.Error("non-cancellation error must not be a deployCancelledError")
	}
}

// Non-interactive path must NOT call the confirm seam and must emit the info
// line on stderr.
func TestConfirmMissingDeps_NonInteractiveLogsAndProceeds(t *testing.T) {
	called := false
	prev := deployMissingDepsConfirmFn
	deployMissingDepsConfirmFn = func(_, _, _ string) (bool, error) {
		called = true
		return false, nil
	}
	t.Cleanup(func() { deployMissingDepsConfirmFn = prev })

	cmd, errBuf := newConfirmTestCmd()

	if err := confirmMissingDeps(cmd, []string{"web"}, []string{"db"}, false); err != nil {
		t.Fatalf("expected nil (proceed), got %v", err)
	}
	if called {
		t.Error("confirm seam must not be invoked in non-interactive mode")
	}
	out := errBuf.String()
	if !strings.Contains(out, "declare after: deps not in this run") {
		t.Errorf("stderr = %q, want info line", out)
	}
	if !strings.Contains(out, "[db]") {
		t.Errorf("stderr = %q, want missing deps listed", out)
	}
}

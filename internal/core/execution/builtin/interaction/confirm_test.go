package interaction

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"devbox-cli/internal/core/execution/builtin/spec"
	"devbox-cli/internal/core/ui/widgets"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/render"
)

// newTestConfirmCtx returns an spec.ExecContext for use in confirm builtin tests.
func newTestConfirmCtx(confirmFunc func(string, string, string) (bool, error)) spec.ExecContext {
	return spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(&bytes.Buffer{}),
		ConfirmFunc: confirmFunc,
	}
}

// TestConfirmBuiltin_SkipConfirm verifies that SkipConfirm=true skips the prompt entirely.
func TestConfirmBuiltin_SkipConfirm(t *testing.T) {
	called := false
	ctx := newTestConfirmCtx(func(msg, okMsg, stopMsg string) (bool, error) {
		called = true
		return false, nil
	})
	ctx.SkipConfirm = true

	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err != nil {
		t.Errorf("expected nil error with SkipConfirm=true, got %v", err)
	}
	if called {
		t.Error("ConfirmFunc must not be called when SkipConfirm=true")
	}
}

// TestConfirmBuiltin_ConfirmFunc_Confirmed verifies that a confirmed=true response returns nil.
func TestConfirmBuiltin_ConfirmFunc_Confirmed(t *testing.T) {
	ctx := newTestConfirmCtx(func(msg, okMsg, stopMsg string) (bool, error) {
		return true, nil
	})
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err != nil {
		t.Errorf("expected nil error when confirmed=true, got %v", err)
	}
}

// TestConfirmBuiltin_ConfirmFunc_Denied verifies that confirmed=false returns "aborted by user".
func TestConfirmBuiltin_ConfirmFunc_Denied(t *testing.T) {
	ctx := newTestConfirmCtx(func(msg, okMsg, stopMsg string) (bool, error) {
		return false, nil
	})
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err == nil {
		t.Fatal("expected error when confirmed=false")
	}
	if err.Error() != "aborted by user" {
		t.Errorf("expected 'aborted by user', got %q", err.Error())
	}
}

// TestConfirmBuiltin_ConfirmFunc_Error verifies that a ConfirmFunc error is propagated.
func TestConfirmBuiltin_ConfirmFunc_Error(t *testing.T) {
	sentinel := errors.New("channel closed")
	ctx := newTestConfirmCtx(func(msg, okMsg, stopMsg string) (bool, error) {
		return false, sentinel
	})
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// TestConfirmBuiltin_ConfirmFunc_ReceivesParams verifies the correct params are passed to ConfirmFunc.
func TestConfirmBuiltin_ConfirmFunc_ReceivesParams(t *testing.T) {
	var gotMsg, gotOk, gotStop string
	ctx := newTestConfirmCtx(func(msg, okMsg, stopMsg string) (bool, error) {
		gotMsg = msg
		gotOk = okMsg
		gotStop = stopMsg
		return true, nil
	})

	with := map[string]any{
		"message":  "This will reset the database.",
		"ok_msg":   "Resetting",
		"stop_msg": "Cancelled",
	}
	_ = Confirm{}.Run(context.Background(), with, ctx)

	if gotMsg != "This will reset the database." {
		t.Errorf("expected message %q, got %q", "This will reset the database.", gotMsg)
	}
	if gotOk != "Resetting" {
		t.Errorf("expected ok_msg %q, got %q", "Resetting", gotOk)
	}
	if gotStop != "Cancelled" {
		t.Errorf("expected stop_msg %q, got %q", "Cancelled", gotStop)
	}
}

// TestConfirmBuiltin_ConfirmFunc_DefaultParams verifies defaults are used when with is nil.
func TestConfirmBuiltin_ConfirmFunc_DefaultParams(t *testing.T) {
	var gotMsg string
	ctx := newTestConfirmCtx(func(msg, okMsg, stopMsg string) (bool, error) {
		gotMsg = msg
		return true, nil
	})
	_ = Confirm{}.Run(context.Background(), nil, ctx)
	if gotMsg != "Are you sure?" {
		t.Errorf("expected default message %q, got %q", "Are you sure?", gotMsg)
	}
}

// TestConfirmBuiltin_NoConfirmFunc_SkipsWhenSkipConfirmSet verifies plain mode skips on SkipConfirm.
func TestConfirmBuiltin_NoConfirmFunc_SkipsWhenSkipConfirmSet(t *testing.T) {
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(&bytes.Buffer{}),
		SkipConfirm: true,
		ConfirmFunc: nil,
	}
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err != nil {
		t.Errorf("expected nil with SkipConfirm=true and no ConfirmFunc, got %v", err)
	}
}

// TestConfirmBuiltin_TTY_UsesRunConfirmWrapper verifies the huh path via injected wrapper.
func TestConfirmBuiltin_TTY_UsesRunConfirmWrapper(t *testing.T) {
	orig := runConfirm
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = orig
		widgets.IsInteractiveFn = origIsInteractive
	})

	// Force TTY detection to return true.
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

	var called bool
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		called = true
		return true, nil
	}

	out := &bytes.Buffer{}
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(out),
		Stdin:       bytes.NewBufferString(""), // non-nil, but IsInteractiveFn is faked
	}
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected runConfirm to be called in TTY mode")
	}
}

// TestConfirmBuiltin_TTY_ErrCancelled verifies that ErrCancelled (Esc/Ctrl-C) maps to "aborted by user".
func TestConfirmBuiltin_TTY_ErrCancelled(t *testing.T) {
	orig := runConfirm
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = orig
		widgets.IsInteractiveFn = origIsInteractive
	})

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		return false, widgets.ErrCancelled
	}

	out := &bytes.Buffer{}
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(out),
		Stdin:       bytes.NewBufferString(""),
	}
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err == nil || err.Error() != "aborted by user" {
		t.Errorf("expected 'aborted by user' for ErrCancelled, got %v", err)
	}
}

// TestConfirmBuiltin_TTY_Denied verifies that runConfirm returning false aborts.
func TestConfirmBuiltin_TTY_Denied(t *testing.T) {
	orig := runConfirm
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = orig
		widgets.IsInteractiveFn = origIsInteractive
	})

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		return false, nil
	}

	out := &bytes.Buffer{}
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(out),
		Stdin:       bytes.NewBufferString(""),
	}
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err == nil || err.Error() != "aborted by user" {
		t.Errorf("expected 'aborted by user', got %v", err)
	}
}

// TestConfirmBuiltin_NonTTY_StdinY verifies that piped "y" input succeeds via fallback.
func TestConfirmBuiltin_NonTTY_StdinY(t *testing.T) {
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = origIsInteractive })

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }

	out := &bytes.Buffer{}
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(out),
		Stdin:       bytes.NewBufferString("y\n"),
	}
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err != nil {
		t.Errorf("expected nil for y input, got %v", err)
	}
}

// TestConfirmBuiltin_NonTTY_StdinN verifies that piped "n" input aborts via fallback.
func TestConfirmBuiltin_NonTTY_StdinN(t *testing.T) {
	origIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = origIsInteractive })

	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }

	out := &bytes.Buffer{}
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(out),
		Stdin:       bytes.NewBufferString("n\n"),
	}
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err == nil || err.Error() != "aborted by user" {
		t.Errorf("expected 'aborted by user' for n input, got %v", err)
	}
}

// TestConfirmBuiltin_PipedStdin_RoutesToFallback verifies that a non-os.File stdin
// (e.g. bytes.Buffer from "echo y | devbox ...") always uses the non-TTY fallback.
func TestConfirmBuiltin_PipedStdin_RoutesToFallback(t *testing.T) {
	// IsInteractiveFn returns false for bytes.Buffer (not *os.File), so this
	// test exercises the real IsInteractiveFn behavior with a bytes.Buffer.
	out := &bytes.Buffer{}
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(out),
		Stdin:       bytes.NewBufferString("y\n"),
	}
	err := Confirm{}.Run(context.Background(), nil, ctx)
	if err != nil {
		t.Errorf("expected nil for piped y input, got %v", err)
	}
}

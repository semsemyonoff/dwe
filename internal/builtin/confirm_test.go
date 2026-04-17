package builtin

import (
	"bytes"
	"errors"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// newTestConfirmCtx returns an ExecContext for use in confirm builtin tests.
func newTestConfirmCtx(confirmFunc func(string, string, string) (bool, error)) ExecContext {
	return ExecContext{
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

	err := confirmBuiltin{}.Run(nil, ctx)
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
	err := confirmBuiltin{}.Run(nil, ctx)
	if err != nil {
		t.Errorf("expected nil error when confirmed=true, got %v", err)
	}
}

// TestConfirmBuiltin_ConfirmFunc_Denied verifies that confirmed=false returns "aborted by user".
func TestConfirmBuiltin_ConfirmFunc_Denied(t *testing.T) {
	ctx := newTestConfirmCtx(func(msg, okMsg, stopMsg string) (bool, error) {
		return false, nil
	})
	err := confirmBuiltin{}.Run(nil, ctx)
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
	err := confirmBuiltin{}.Run(nil, ctx)
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
	_ = confirmBuiltin{}.Run(with, ctx)

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
	_ = confirmBuiltin{}.Run(nil, ctx)
	if gotMsg != "Are you sure?" {
		t.Errorf("expected default message %q, got %q", "Are you sure?", gotMsg)
	}
}

// TestConfirmBuiltin_NoConfirmFunc_SkipsWhenSkipConfirmSet verifies plain mode skips on SkipConfirm.
func TestConfirmBuiltin_NoConfirmFunc_SkipsWhenSkipConfirmSet(t *testing.T) {
	ctx := ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: "/tmp",
		Output:      render.NewWriter(&bytes.Buffer{}),
		SkipConfirm: true,
		ConfirmFunc: nil,
	}
	err := confirmBuiltin{}.Run(nil, ctx)
	if err != nil {
		t.Errorf("expected nil with SkipConfirm=true and no ConfirmFunc, got %v", err)
	}
}

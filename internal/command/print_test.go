package command

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"devbox-cli/internal/ui"
)

// TestPrintConfirmCmd_TTY_Confirmed verifies that the huh path is taken when TTY
// and runConfirm returns true (no exit, no error).
func TestPrintConfirmCmd_TTY_Confirmed(t *testing.T) {
	origRC := runConfirm
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = origRC
		ui.IsInteractiveFn = origIsInteractive
	})

	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		return true, nil
	}

	root := NewRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetIn(bytes.NewBufferString(""))
	root.SetArgs([]string{"print", "confirm", "--ok-msg", "Done", "--stop-msg", "Stopped", "Proceed?"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Done") {
		t.Errorf("expected 'Done' in output; got %q", outBuf.String())
	}
}

// TestPrintConfirmCmd_TTY_Denied verifies that the huh path is taken when TTY
// and runConfirm returns false (os.Exit(1) called, so we test via --continue flag).
func TestPrintConfirmCmd_TTY_Denied_Continue(t *testing.T) {
	origRC := runConfirm
	origIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() {
		runConfirm = origRC
		ui.IsInteractiveFn = origIsInteractive
	})

	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }
	runConfirm = func(title, affirmative, negative string) (bool, error) {
		return false, nil
	}

	root := NewRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetIn(bytes.NewBufferString(""))
	root.SetArgs([]string{"print", "confirm", "--continue", "--stop-msg", "Stopped", "Proceed?"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error with --continue flag: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Stopped") {
		t.Errorf("expected 'Stopped' in output; got %q", outBuf.String())
	}
}

// TestPrintConfirmCmd_NonTTY_Y verifies that the stdin Y/N fallback is used when
// stdin is a bytes.Buffer (not a TTY) and input is "y".
func TestPrintConfirmCmd_NonTTY_Y(t *testing.T) {
	root := NewRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetIn(bytes.NewBufferString("y\n"))
	root.SetArgs([]string{"print", "confirm", "--ok-msg", "Continuing", "Proceed?"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Continuing") {
		t.Errorf("expected 'Continuing' in output; got %q", outBuf.String())
	}
}

// TestPrintConfirmCmd_NonTTY_N verifies that the stdin Y/N fallback is used when
// stdin is a bytes.Buffer and input is "n" (with --continue to avoid os.Exit(1)).
func TestPrintConfirmCmd_NonTTY_N(t *testing.T) {
	root := NewRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetIn(bytes.NewBufferString("n\n"))
	root.SetArgs([]string{"print", "confirm", "--continue", "--stop-msg", "Stopping", "Proceed?"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error with --continue flag: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Stopping") {
		t.Errorf("expected 'Stopping' in output; got %q", outBuf.String())
	}
}

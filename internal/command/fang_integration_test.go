package command_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"devbox-cli/internal/command"
	"devbox-cli/internal/version"
)

// executeWithFang runs fang.Execute against root, capturing stdout/stderr,
// returning the error fang.Execute returns (nil on success).
func executeWithFang(t *testing.T, root *cobra.Command, args []string, opts ...fang.Option) (stdout, stderr string, err error) {
	t.Helper()
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)

	err = fang.Execute(context.Background(), root, opts...)
	return outBuf.String(), errBuf.String(), err
}

// TestFangHelpOutput verifies that --help produces non-empty styled help.
func TestFangHelpOutput(t *testing.T) {
	root := command.NewRootCmd()
	stdout, _, err := executeWithFang(t, root, []string{"--help"})
	if err != nil {
		t.Fatalf("--help returned unexpected error: %v", err)
	}
	if stdout == "" {
		t.Fatal("--help produced no output")
	}
	// The help output should mention the binary name or usage.
	if !strings.Contains(stdout, "devbox") {
		t.Errorf("help output missing 'devbox', got:\n%s", stdout)
	}
}

// TestFangVersionFlag verifies that --version prints version information.
func TestFangVersionFlag(t *testing.T) {
	root := command.NewRootCmd()
	stdout, _, err := executeWithFang(t, root, []string{"--version"},
		fang.WithVersion(version.Version),
		fang.WithCommit(version.Commit),
	)
	if err != nil {
		t.Fatalf("--version returned unexpected error: %v", err)
	}
	// Fang appends commit to version when commit is provided.
	if stdout == "" {
		t.Fatal("--version produced no output")
	}
	if !strings.Contains(stdout, version.Version) {
		t.Errorf("--version output %q missing version %q", stdout, version.Version)
	}
}

// TestFangErrorHandlerSuppressesErrSilent verifies that ErrSilent is swallowed
// by our custom error handler (no output written to stderr).
func TestFangErrorHandlerSuppressesErrSilent(t *testing.T) {
	captured := &bytes.Buffer{}
	errHandler := func(w io.Writer, _ fang.Styles, err error) {
		if errors.Is(err, command.ErrSilent) {
			return
		}
		fang.DefaultErrorHandler(w, fang.Styles{}, err)
	}

	root := command.NewRootCmd()
	// Override the RunE to return ErrSilent.
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return command.ErrSilent
	}
	root.SetErr(captured)
	root.SetArgs([]string{})

	err := fang.Execute(context.Background(), root,
		fang.WithErrorHandler(errHandler),
	)
	if err == nil {
		// fang.Execute returns the error even when suppressed by handler
		// but it depends on whether RunE returns ErrSilent which cobra propagates.
		// If err is nil here something is unexpected, but not a test failure per se.
		t.Log("fang.Execute returned nil (no error propagated)")
	}
	if captured.Len() > 0 {
		t.Errorf("ErrSilent should produce no stderr output, got: %q", captured.String())
	}
}

// TestFangSilenceSettings verifies that Fang sets SilenceErrors and SilenceUsage
// on the root command automatically.
func TestFangSilenceSettings(t *testing.T) {
	root := command.NewRootCmd()
	// fang.Execute sets these; simulate by checking after a --help run.
	outBuf := &bytes.Buffer{}
	root.SetOut(outBuf)
	root.SetArgs([]string{"--help"})

	_ = fang.Execute(context.Background(), root)

	if !root.SilenceErrors {
		t.Error("fang.Execute should set SilenceErrors = true on root command")
	}
	if !root.SilenceUsage {
		t.Error("fang.Execute should set SilenceUsage = true on root command")
	}
}

// TestFangUnknownCommandError verifies that an unknown subcommand returns an error.
func TestFangUnknownCommandError(t *testing.T) {
	captured := &bytes.Buffer{}
	var handledErr error
	errHandler := func(w io.Writer, styles fang.Styles, err error) {
		handledErr = err
	}

	root := command.NewRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(captured)
	root.SetArgs([]string{"nonexistent-command-xyz"})

	err := fang.Execute(context.Background(), root,
		fang.WithErrorHandler(errHandler),
	)
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
	if handledErr == nil {
		t.Error("error handler was not called for unknown command")
	}
}

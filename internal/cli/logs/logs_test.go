package logs_test

import (
	"bytes"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	cmdlogs "devbox-cli/internal/cli/logs"

	"github.com/spf13/cobra"
)

// newTestRoot builds a minimal cobra root command with the logs subcommand
// attached. It does NOT run PersistentPreRunE — callers that need project
// resolution should use cli.NewRootCmd() instead.
func newTestRoot(flags *cmdctx.RootFlags) *cobra.Command {
	root := &cobra.Command{
		Use:          "devbox",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(cmdlogs.NewCmd("", flags))
	return root
}

func execCmd(t *testing.T, root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestLogsCmd_NoArgs verifies that invoking logs without a service name
// returns an error (cobra.ExactArgs(1) enforcement).
func TestLogsCmd_NoArgs(t *testing.T) {
	flags := &cmdctx.RootFlags{Output: "text"}
	root := newTestRoot(flags)
	_, _, err := execCmd(t, root, "logs")
	if err == nil {
		t.Error("expected error when no service argument provided, got nil")
	}
}

// TestLogsCmd_OneArg verifies that invoking logs with exactly one argument
// is accepted by cobra (arg validation passes, stub RunE returns nil).
func TestLogsCmd_OneArg(t *testing.T) {
	flags := &cmdctx.RootFlags{Output: "text"}
	root := newTestRoot(flags)
	_, _, err := execCmd(t, root, "logs", "myservice")
	if err != nil {
		t.Errorf("unexpected error with one arg: %v", err)
	}
}

// TestLogsCmd_TooManyArgs verifies that more than one positional arg is rejected.
func TestLogsCmd_TooManyArgs(t *testing.T) {
	flags := &cmdctx.RootFlags{Output: "text"}
	root := newTestRoot(flags)
	_, _, err := execCmd(t, root, "logs", "svcA", "svcB")
	if err == nil {
		t.Error("expected error with two positional args, got nil")
	}
}

// TestLogsCmd_FlagsRegistered verifies the expected flags exist on the command.
func TestLogsCmd_FlagsRegistered(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	root := newTestRoot(flags)

	var logsCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "logs" {
			logsCmd = c
			break
		}
	}
	if logsCmd == nil {
		t.Fatal("logs command not found in root")
	}

	for _, flagName := range []string{"tail", "since", "follow"} {
		if logsCmd.Flags().Lookup(flagName) == nil {
			t.Errorf("expected flag --%s to be registered", flagName)
		}
	}

	// Verify -f shorthand for --follow.
	if logsCmd.Flags().ShorthandLookup("f") == nil {
		t.Error("expected -f shorthand for --follow")
	}
}

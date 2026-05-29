package logs_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		Use:           "devbox",
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

// writeLogsTestConfig creates a temp dir with devbox.yml and optional service folders.
// services maps service name → container template string.
func writeLogsTestConfig(t *testing.T, dir string, svcs map[string]string) string {
	t.Helper()
	content := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: devbox\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	for name, container := range svcs {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", svcDir, err)
		}
		yml := "type: app\ncontainer: " + container + "\n"
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(yml), 0o644); err != nil {
			t.Fatalf("write service.yml: %v", err)
		}
	}
	return filepath.Join(dir, "devbox.yml")
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
// is accepted by cobra (arg validation passes, stub RunE returns nil for an
// unknown service path that will be wired in Task 3).
func TestLogsCmd_OneArg(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeLogsTestConfig(t, dir, map[string]string{"myapp": "myapp"})
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)
	_, _, err := execCmd(t, root, "logs", "myapp")
	if err != nil {
		t.Errorf("unexpected error with one arg for known service: %v", err)
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

// TestResolveLogsTarget_UnknownService verifies that requesting a service not
// in the project returns a service_unknown CodedError with a hint listing
// available services.
func TestResolveLogsTarget_UnknownService(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeLogsTestConfig(t, dir, map[string]string{
		"myapp": "myapp-container",
	})
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	_, _, err := cmdlogs.ResolveLogsTarget(flags, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
	var coded *cmdctx.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("expected *cmdctx.CodedError, got %T: %v", err, err)
	}
	if coded.Code != "service_unknown" {
		t.Errorf("error code = %q, want %q", coded.Code, "service_unknown")
	}
	if !strings.Contains(coded.Hint, "myapp") {
		t.Errorf("hint should list known service names; got: %q", coded.Hint)
	}
	if detail, ok := coded.Details["requested"]; !ok || detail != "nonexistent" {
		t.Errorf("details[requested] = %v, want %q", detail, "nonexistent")
	}
}

// TestResolveLogsTarget_KnownService verifies that a known service resolves to
// the expected Docker container name (project prefix + container template).
func TestResolveLogsTarget_KnownService(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeLogsTestConfig(t, dir, map[string]string{
		"myapp": "myapp-container",
	})
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	containerName, cfg, err := cmdlogs.ResolveLogsTarget(flags, "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Container name = project.FullName() + "-" + container template.
	// From the fixture: prefix=devbox, name=test → fullName=devbox-test.
	want := cfg.Project.FullName() + "-myapp-container"
	if containerName != want {
		t.Errorf("container name = %q, want %q", containerName, want)
	}
}

// TestResolveLogsTarget_NoServicesHint verifies that when a project has no
// services, the hint in the error still renders without crashing.
func TestResolveLogsTarget_NoServicesHint(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeLogsTestConfig(t, dir, nil)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	_, _, err := cmdlogs.ResolveLogsTarget(flags, "anything")
	if err == nil {
		t.Fatal("expected error for project with no services")
	}
	var coded *cmdctx.CodedError
	if !errors.As(err, &coded) || coded.Code != "service_unknown" {
		t.Errorf("expected service_unknown CodedError, got %v", err)
	}
}

package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"

	"github.com/spf13/cobra"
)

// bridgeContainerEnv pins the bridged-invocation environment for one test.
func bridgeContainerEnv(t *testing.T, service string) {
	t.Helper()
	t.Setenv(bridgeclient.EnvInvokedFrom, bridgeclient.InvokedFromContainer)
	t.Setenv(bridgeclient.EnvBridgeService, service)
}

// setupBridgeProject scaffolds a project with one bridged and one host-only
// command and returns the workspace.yml path.
func setupBridgeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "workspace", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const tools = `
commands:
  marked:
    type: shell
    cmd: echo bridged-ok
    bridge:
      enabled: true
  unmarked:
    type: shell
    cmd: echo host-only
  flow:
    type: workflow
    bridge:
      enabled: true
    steps:
      - command: tools.unmarked
`
	if err := os.WriteFile(filepath.Join(cmdDir, "tools.yml"), []byte(tools), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestBridgeGate_DirectRunRejectedInContainer(t *testing.T) {
	bridgeContainerEnv(t, "main")
	cfgPath := setupBridgeProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	stubInteractive(t, false)

	_, err := runBare(t, flags, []string{"tools.unmarked"})
	var ce *cmdctx.CodedError
	if !errors.As(err, &ce) || ce.Code != "command_not_bridged" {
		t.Fatalf("want command_not_bridged CodedError, got %v", err)
	}
	if ce.Hint == "" {
		t.Error("rejection must carry a remediation hint")
	}
}

func TestBridgeGate_BridgedCommandRunsInContainer(t *testing.T) {
	bridgeContainerEnv(t, "main")
	cfgPath := setupBridgeProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	stubInteractive(t, false)

	out, err := runBare(t, flags, []string{"tools.marked"})
	if err != nil {
		t.Fatalf("bridged command must run, got error: %v", err)
	}
	if !strings.Contains(out, "bridged-ok") {
		t.Errorf("expected child output in:\n%s", out)
	}
}

// TestBridgeGate_WorkflowStepsBypassGate pins the load-bearing distinction
// from Hidden: a bridged workflow executes its non-bridged sub-commands
// host-side — the gate covers the container invocation surface, never
// executability. Folding BridgeHidden into Hidden would auto-skip this step.
func TestBridgeGate_WorkflowStepsBypassGate(t *testing.T) {
	bridgeContainerEnv(t, "main")
	cfgPath := setupBridgeProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	stubInteractive(t, false)

	out, err := runBare(t, flags, []string{"tools.flow"})
	if err != nil {
		t.Fatalf("bridged workflow must run: %v", err)
	}
	if !strings.Contains(out, "host-only") {
		t.Errorf("non-bridged sub-command must still execute inside the workflow:\n%s", out)
	}
}

func TestBridgeGate_HostRunsUnmarked(t *testing.T) {
	cfgPath := setupBridgeProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	stubInteractive(t, false)

	out, err := runBare(t, flags, []string{"tools.unmarked"})
	if err != nil {
		t.Fatalf("host invocation must stay unrestricted, got: %v", err)
	}
	if !strings.Contains(out, "host-only") {
		t.Errorf("expected child output in:\n%s", out)
	}
}

func TestBridgeGate_ListFiltersInContainer(t *testing.T) {
	bridgeContainerEnv(t, "main")
	cfgPath := setupBridgeProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	stubInteractive(t, false)

	out, err := runBare(t, flags, nil)
	if err != nil {
		t.Fatalf("list fallback: %v", err)
	}
	if !strings.Contains(out, "tools.marked") {
		t.Errorf("bridged command missing from container listing:\n%s", out)
	}
	if strings.Contains(out, "tools.unmarked") {
		t.Errorf("host-only command leaked into container listing:\n%s", out)
	}
}

func TestBridgeGate_InspectRejectedInContainer(t *testing.T) {
	bridgeContainerEnv(t, "main")
	cfgPath := setupBridgeProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	cmd := NewCmd("", flags)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--inspect", "tools.unmarked"})
	cmd.SetOut(os.NewFile(0, os.DevNull))
	cmd.SetErr(os.NewFile(0, os.DevNull))
	err := cmd.Execute()
	var ce *cmdctx.CodedError
	if !errors.As(err, &ce) || ce.Code != "command_not_bridged" {
		t.Fatalf("inspect from container must reject, got %v", err)
	}
}

func TestBridgeGate_CompletionFiltersInContainer(t *testing.T) {
	bridgeContainerEnv(t, "main")
	cfgPath := setupBridgeProject(t)
	// Root short-circuits CompletionConfigPath's project discovery.
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: filepath.Dir(cfgPath)}

	fn := registryIDCompletion(flags, false)
	completions, directive := fn(&cobra.Command{}, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v", directive)
	}
	joined := strings.Join(completions, "\n")
	if !strings.Contains(joined, "tools.marked") {
		t.Errorf("bridged command missing from completions:\n%s", joined)
	}
	if strings.Contains(joined, "tools.unmarked") {
		t.Errorf("host-only command leaked into completions:\n%s", joined)
	}

	// The inspect escape hatch must NOT bypass the container gate either.
	fn = registryIDCompletion(flags, true)
	completions, _ = fn(&cobra.Command{}, nil, "")
	if strings.Contains(strings.Join(completions, "\n"), "tools.unmarked") {
		t.Error("inspect completions leaked a host-only command in container context")
	}
}

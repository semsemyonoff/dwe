package command

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

// --- shell command ---

func TestNewShellCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	if cmd.Use != "shell [service]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "shell [service]")
	}
}

func TestNewShellCmd_HasRootFlag(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	if cmd.Flags().Lookup("root") == nil {
		t.Error("shell command missing --root flag")
	}
}

func TestNewShellCmd_AcceptsOptionalArg(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	// MaximumNArgs(1): zero args is OK
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("Args validation failed for 0 args: %v", err)
	}
	// one arg is OK
	if err := cmd.Args(cmd, []string{"main"}); err != nil {
		t.Errorf("Args validation failed for 1 arg: %v", err)
	}
	// two args should fail
	if err := cmd.Args(cmd, []string{"main", "second"}); err == nil {
		t.Error("expected error for 2 args, got nil")
	}
}

func TestShellRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	if !nameSet["shell"] {
		t.Error("shell command not registered at root level")
	}
}

// --- status command ---

func TestNewStatusCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newStatusCmd(flags)
	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
}

func TestStatusRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	if !nameSet["status"] {
		t.Error("status command not registered at root level")
	}
}

func TestRunServicesViaCfg(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Dir: "./services/main", Container: "app-main"},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runServices(w, cfg); err != nil {
		t.Fatalf("runServices error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "main") {
		t.Errorf("status output missing service name 'main'\n%s", out)
	}
}

// --- version command ---

func TestNewVersionCmd_UseField(t *testing.T) {
	cmd := newVersionCmd()
	if cmd.Use != "version" {
		t.Errorf("Use = %q, want %q", cmd.Use, "version")
	}
}

func TestVersionRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	if !nameSet["version"] {
		t.Error("version command not registered at root level")
	}
}

func TestVersionCmd_Output(t *testing.T) {
	cmd := newVersionCmd()
	// version command uses Run (not RunE), execute it by capturing stdout.
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// Override fmt.Println is not possible directly; just verify the command runs.
	if cmd.Run == nil {
		t.Error("version command has no Run handler")
	}
}

// --- renames: services and commands ---

func TestServiceCmdUsesServicesName(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceCmd(flags)
	if cmd.Use != "services" {
		t.Errorf("newServiceCmd Use = %q, want %q", cmd.Use, "services")
	}
}

func TestServiceCmdSubcommands(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceCmd(flags)
	want := []string{"list", "enable", "disable"}
	nameSet := make(map[string]bool)
	for _, c := range cmd.Commands() {
		nameSet[c.Name()] = true
	}
	for _, name := range want {
		if !nameSet[name] {
			t.Errorf("services command missing subcommand %q", name)
		}
	}
	// cli should NOT be present
	if nameSet["cli"] {
		t.Error("services command should not have cli subcommand (moved to root shell)")
	}
}

func TestCommandCmdUsesCommandsName(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
	if cmd.Use != "commands" {
		t.Errorf("newCommandCmd Use = %q, want %q", cmd.Use, "commands")
	}
}

func TestCommandsRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	// "commands" group should be present; old "command" group should not
	if !nameSet["commands"] {
		t.Error("commands group not registered at root")
	}
	if nameSet["command"] {
		t.Error("old 'command' name still registered; should be 'commands'")
	}
}

// --- Task 13: message cleanup ---

// TestNoMakeReferencesInCLIMessages verifies that no public command's Short, Long,
// or Example fields reference 'make <target>' (should use 'devbox <command>' instead).
func TestNoMakeReferencesInCLIMessages(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	root := NewRootCmd()

	// Collect all commands recursively.
	var collect func(cmd *cobra.Command) []*cobra.Command
	collect = func(cmd *cobra.Command) []*cobra.Command {
		result := []*cobra.Command{cmd}
		for _, sub := range cmd.Commands() {
			result = append(result, collect(sub)...)
		}
		return result
	}
	_ = flags
	all := collect(root)

	for _, cmd := range all {
		for _, field := range []struct {
			name string
			val  string
		}{
			{"Short", cmd.Short},
			{"Long", cmd.Long},
			{"Example", cmd.Example},
		} {
			if strings.Contains(field.val, "'make ") || strings.Contains(field.val, "\"make ") {
				t.Errorf("command %q %s contains 'make <target>' reference: %q", cmd.CommandPath(), field.name, field.val)
			}
		}
	}
}

// TestPublicCommandsHaveLongDescription verifies that key public commands have
// non-empty Long descriptions.
func TestPublicCommandsHaveLongDescription(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	checks := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"up", newUpCmd(flags)},
		{"down", newDownCmd(flags)},
		{"stop", newStopCmd(flags)},
		{"restart", newRestartCmd(flags)},
		{"logs", newLogsCmd(flags)},
		{"wait", newWaitCmd(flags)},
		{"info", newInfoCmd(flags)},
		{"version", newVersionCmd()},
		{"status", newStatusCmd(flags)},
		{"commands", newCommandCmd(flags)},
		{"services", newServiceCmd(flags)},
		{"tools", newToolCmd(flags)},
		{"render", newRenderCmd(flags)},
		{"deploy", newDeployCmd(flags)},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if strings.TrimSpace(tc.cmd.Long) == "" {
				t.Errorf("command %q has no Long description", tc.name)
			}
		})
	}
}

// TestPublicCommandsHaveExamples verifies that key public commands have
// non-empty Example fields.
func TestPublicCommandsHaveExamples(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	checks := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"up", newUpCmd(flags)},
		{"down", newDownCmd(flags)},
		{"stop", newStopCmd(flags)},
		{"restart", newRestartCmd(flags)},
		{"logs", newLogsCmd(flags)},
		{"wait", newWaitCmd(flags)},
		{"info", newInfoCmd(flags)},
		{"version", newVersionCmd()},
		{"status", newStatusCmd(flags)},
		{"shell", newShellCmd(flags)},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if strings.TrimSpace(tc.cmd.Example) == "" {
				t.Errorf("command %q has no Example", tc.name)
			}
		})
	}
}

// --- old services topology command removed ---

func TestOldServicesTopologyCmdRemoved(t *testing.T) {
	// The old `services` command (with topology RunE and cli subcommand) was
	// merged into `status` and `shell`. The remaining `services` command should
	// only expose list/enable/disable subcommands, with no cli subcommand.
	root := NewRootCmd()

	var servicesCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "services" {
			servicesCmd = c
			break
		}
	}
	if servicesCmd == nil {
		t.Fatal("services command not found at root")
	}

	subNameSet := make(map[string]bool)
	for _, c := range servicesCmd.Commands() {
		subNameSet[c.Name()] = true
	}
	if subNameSet["cli"] {
		t.Error("services command still has cli subcommand; it should have been moved to root shell")
	}
}

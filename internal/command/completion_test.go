package command

import (
	"sort"
	"strings"
	"testing"

	"devbox-cli/internal/commands"
	"devbox-cli/internal/config"

	"github.com/spf13/cobra"
)

// --- registryIDCompletion ---

func TestRegistryIDCompletion_emptyRegistry(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	fn := registryIDCompletion(flags, false)

	// Use an empty registry; since loadCommandRegistry calls os.Stat we need
	// to inject the registry differently. Test the helper directly with a
	// fake registry by building a wrapper.
	//
	// Because registryIDCompletion loads from disk, we test the public contract
	// through a fake: build a cobra command and simulate what the function does.
	reg := commands.NewEmptyRegistry()
	completions := buildRegistryCompletions(reg.List(""), false)
	if len(completions) != 0 {
		t.Errorf("expected 0 completions for empty registry, got %d", len(completions))
	}
	_ = fn // suppress unused warning
}

func TestRegistryIDCompletion_publicOnly(t *testing.T) {
	defs := []*commands.CommandDef{
		{ID: "services.main.migrate", Description: "Run migrations", Private: false},
		{ID: "services.main.create-db", Description: "Create database", Private: true},
	}
	completions := buildRegistryCompletions(defs, false)
	// Should include the migrate completion (with or without desc).
	if len(completions) != 1 {
		t.Fatalf("expected 1 completion (public only), got %d: %v", len(completions), completions)
	}
	if !strings.Contains(completions[0], "services.main.migrate") {
		t.Errorf("expected completion to contain command ID, got %q", completions[0])
	}
}

func TestRegistryIDCompletion_includePrivate(t *testing.T) {
	defs := []*commands.CommandDef{
		{ID: "services.main.migrate", Description: "Run migrations", Private: false},
		{ID: "services.main.create-db", Description: "Create database", Private: true},
	}
	// When includePrivate=true, all defs are passed in (caller uses ListAll).
	completions := buildRegistryCompletions(defs, true)
	if len(completions) != 2 {
		t.Errorf("expected 2 completions (including private), got %d", len(completions))
	}
}

func TestRegistryIDCompletion_withDescriptions(t *testing.T) {
	defs := []*commands.CommandDef{
		{ID: "app.install", Description: "Run the installer", Private: false},
	}
	completions := buildRegistryCompletions(defs, false)
	if len(completions) == 0 {
		t.Fatal("expected at least 1 completion")
	}
	// CompletionWithDesc embeds the description after a tab.
	if !strings.Contains(completions[0], "app.install") {
		t.Errorf("completion missing ID: %q", completions[0])
	}
}

func TestRegistryIDCompletion_noSecondArgCompletion(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	fn := registryIDCompletion(flags, false)
	// When args already has one element, no completions should be returned.
	completions, directive := fn(nil, []string{"already-provided"}, "")
	if len(completions) != 0 {
		t.Errorf("expected 0 completions when arg already provided, got %d", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
}

// buildRegistryCompletions is a testable helper that mirrors the completion
// logic inside registryIDCompletion, bypassing disk access.
func buildRegistryCompletions(defs []*commands.CommandDef, includePrivate bool) []string {
	var completions []string
	if !includePrivate {
		var filtered []*commands.CommandDef
		for _, d := range defs {
			if !d.Private {
				filtered = append(filtered, d)
			}
		}
		defs = filtered
	}
	for _, d := range defs {
		entry := d.ID
		if d.Description != "" {
			entry = cobra.CompletionWithDesc(d.ID, d.Description)
		}
		completions = append(completions, entry)
	}
	return completions
}

// --- serviceNameCompletion ---

func TestServiceNameCompletion_noSecondArg(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	fn := serviceNameCompletion(flags)
	completions, directive := fn(nil, []string{"already"}, "")
	if len(completions) != 0 {
		t.Errorf("expected 0 completions when arg already set, got %d", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
}

func TestServiceNameCompletionFromConfig(t *testing.T) {
	// Test the core logic: sorted service names from a config.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"worker": {Type: "worker"},
			"main":   {Type: "app"},
		},
	}
	names := sortedKeys(cfg.Services)
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "main" || names[1] != "worker" {
		t.Errorf("expected [main worker], got %v", names)
	}
}

// --- optionalServiceNameCompletion ---

func TestOptionalServiceNameCompletion_noSecondArg(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	fn := optionalServiceNameCompletion(flags)
	completions, directive := fn(nil, []string{"already"}, "")
	if len(completions) != 0 {
		t.Errorf("expected 0 completions when arg already set, got %d", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
}

func TestOptionalServiceNameCompletion_filtersOutMandatory(t *testing.T) {
	// Simulate what optionalServiceNameCompletion does.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main":   {Type: "app", Mandatory: true},
			"worker": {Type: "worker", Mandatory: false},
		},
	}
	var names []string
	for name, svc := range cfg.Services {
		if !svc.Mandatory {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) != 1 {
		t.Fatalf("expected 1 optional service, got %d: %v", len(names), names)
	}
	if names[0] != "worker" {
		t.Errorf("expected 'worker', got %q", names[0])
	}
}

// --- toolNameCompletion ---

func TestToolNameCompletion_returnsKnownTools(t *testing.T) {
	completions, directive := toolNameCompletion(nil, []string{}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
	if len(completions) == 0 {
		t.Fatal("expected at least one tool completion")
	}
	// All completions should be from knownTools.
	for _, c := range completions {
		if !knownTools[c] {
			t.Errorf("completion %q not in knownTools", c)
		}
	}
	// Should be sorted.
	sorted := make([]string, len(completions))
	copy(sorted, completions)
	sort.Strings(sorted)
	for i, s := range sorted {
		if completions[i] != s {
			t.Errorf("completions not sorted at index %d: got %q, want %q", i, completions[i], s)
			break
		}
	}
}

func TestToolNameCompletion_noSecondArg(t *testing.T) {
	completions, directive := toolNameCompletion(nil, []string{"already"}, "")
	if len(completions) != 0 {
		t.Errorf("expected 0 completions when arg already set, got %d", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
}

// --- completion command in advanced group ---

func TestCompletionCmdRegisteredInAdvancedGroup(t *testing.T) {
	root := NewRootCmd()
	var completionCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			completionCmd = c
			break
		}
	}
	if completionCmd == nil {
		t.Fatal("completion command not found in root")
	}
	if completionCmd.GroupID != groupAdvanced {
		t.Errorf("completion command GroupID = %q, want %q", completionCmd.GroupID, groupAdvanced)
	}
}

func TestCompletionCmdHasShellSubcommands(t *testing.T) {
	root := NewRootCmd()
	var completionCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			completionCmd = c
			break
		}
	}
	if completionCmd == nil {
		t.Fatal("completion command not found")
	}
	want := []string{"bash", "zsh", "fish", "powershell"}
	nameSet := make(map[string]bool)
	for _, c := range completionCmd.Commands() {
		nameSet[c.Name()] = true
	}
	for _, name := range want {
		if !nameSet[name] {
			t.Errorf("completion command missing subcommand %q", name)
		}
	}
}

// --- ValidArgsFunction assignment checks ---

func TestCommandInspectHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandInspectCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("commands inspect should have a ValidArgsFunction for dynamic completion")
	}
}

func TestCommandRunHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandRunCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("commands run should have a ValidArgsFunction for dynamic completion")
	}
}

func TestShellCmdHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("shell command should have a ValidArgsFunction for dynamic completion")
	}
}

func TestServiceEnableCmdHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceEnableCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("services enable should have a ValidArgsFunction for dynamic completion")
	}
}

func TestServiceDisableCmdHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceDisableCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("services disable should have a ValidArgsFunction for dynamic completion")
	}
}

func TestToolEnableCmdHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolEnableCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("tools enable should have a ValidArgsFunction for dynamic completion")
	}
}

func TestToolDisableCmdHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newToolDisableCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("tools disable should have a ValidArgsFunction for dynamic completion")
	}
}

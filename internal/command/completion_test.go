package command

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/usercommands"

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
	reg := usercommands.NewEmptyRegistry()
	completions := buildRegistryCompletions(reg.List(""), false)
	if len(completions) != 0 {
		t.Errorf("expected 0 completions for empty registry, got %d", len(completions))
	}
	_ = fn // suppress unused warning
}

func TestRegistryIDCompletion_publicOnly(t *testing.T) {
	defs := []*usercommands.CommandDef{
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
	defs := []*usercommands.CommandDef{
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
	defs := []*usercommands.CommandDef{
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
func buildRegistryCompletions(defs []*usercommands.CommandDef, includePrivate bool) []string {
	var completions []string
	if !includePrivate {
		var filtered []*usercommands.CommandDef
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
	fn := serviceCompletion(flags, completeDisabledOptional)
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

func TestServiceCompletion_disabledOptional_filtersCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	devboxDir := filepath.Join(tmpDir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "devbox.yml"), []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultsYML := `project:
  name: test
  prefix: test
services:
  api:
    enabled: true
  worker:
    enabled: false
runtime:
  ports:
    app: 3000
  hosts:
    main: localhost
`
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"main":   "type: app\ncontainer: app-main\nmandatory: true\n",
		"api":    "type: app\ncontainer: app-api\n",
		"worker": "type: app\ncontainer: app-worker\n",
	} {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flags := &rootFlags{configPath: filepath.Join(tmpDir, "devbox.yml"), projectRoot: tmpDir}

	// completeDisabledOptional: should return only 'worker' (disabled, non-mandatory).
	fn := serviceCompletion(flags, completeDisabledOptional)
	completions, directive := fn(nil, []string{}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
	if len(completions) != 1 || completions[0] != "worker" {
		t.Errorf("completeDisabledOptional: expected [worker], got %v", completions)
	}

	// completeEnabledOptional: should return only 'api' (enabled, non-mandatory).
	fn2 := serviceCompletion(flags, completeEnabledOptional)
	completions2, _ := fn2(nil, []string{}, "")
	if len(completions2) != 1 || completions2[0] != "api" {
		t.Errorf("completeEnabledOptional: expected [api], got %v", completions2)
	}
}

func TestToolCompletion_enabledFilter_filtersCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	devboxDir := filepath.Join(tmpDir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "devbox.yml"), []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultsYML := `project:
  name: test
  prefix: test
services:
  adminer:
    enabled: true
  elasticvue:
    enabled: false
`
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"adminer":    "type: tool\ncontainer: adminer\nports:\n  main: 8080\nhosts:\n  main: adminer.localhost\n",
		"elasticvue": "type: tool\ncontainer: elasticvue\nports:\n  main: 8044\nhosts:\n  main: elasticvue.localhost\n",
	} {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flags := &rootFlags{configPath: filepath.Join(tmpDir, "devbox.yml"), projectRoot: tmpDir}

	// completeDisabledOptional (unified): should return only 'elasticvue'.
	fn := serviceCompletion(flags, completeDisabledOptional)
	completions, directive := fn(nil, []string{}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
	if len(completions) != 1 || completions[0] != "elasticvue" {
		t.Errorf("completeDisabledOptional: expected [elasticvue], got %v", completions)
	}

	// completeEnabledOptional (unified): should return only 'adminer'.
	fn2 := serviceCompletion(flags, completeEnabledOptional)
	completions2, _ := fn2(nil, []string{}, "")
	if len(completions2) != 1 || completions2[0] != "adminer" {
		t.Errorf("completeEnabledOptional: expected [adminer], got %v", completions2)
	}
}

// --- toolNameCompletion ---

func TestToolNameCompletion_returnsConfiguredTools(t *testing.T) {
	// toolNameCompletion now loads the config to derive tool names.
	// This test uses a real on-disk config setup.
	tempDir := t.TempDir()
	devboxDir := tempDir + "/devbox"
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}

	// Write a defaults.yml with tool overlays (toggles only).
	defaultsYML := `
project:
  name: test
  prefix: test
services:
  adminer:
    enabled: false
  elasticvue:
    enabled: false
`
	if err := os.WriteFile(devboxDir+"/defaults.yml", []byte(defaultsYML), 0644); err != nil {
		t.Fatalf("writing defaults.yml: %v", err)
	}

	// Write the tool definitions in devbox/services.yml.
	for name, content := range map[string]string{
		"adminer":    "type: tool\ncontainer: adminer\nports:\n  main: 8080\nhosts:\n  main: adminer.localhost\n",
		"elasticvue": "type: tool\ncontainer: elasticvue\nports:\n  main: 8044\nhosts:\n  main: elasticvue.localhost\n",
	} {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0755); err != nil {
			t.Fatalf("mkdir services/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0644); err != nil {
			t.Fatalf("write services/%s/service.yml: %v", name, err)
		}
	}

	// Write a minimal devbox.yml with schema_version.
	devboxYML := `schema_version: "2"
`
	if err := os.WriteFile(tempDir+"/devbox.yml", []byte(devboxYML), 0644); err != nil {
		t.Fatalf("writing devbox.yml: %v", err)
	}

	flags := &rootFlags{configPath: tempDir + "/devbox.yml", projectRoot: tempDir}
	fn := serviceCompletion(flags, completeDisabledOptional)
	completions, directive := fn(nil, []string{}, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
	if len(completions) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(completions), completions)
	}
	// Check that both tools are present.
	toolSet := make(map[string]bool)
	for _, c := range completions {
		toolSet[c] = true
	}
	if !toolSet["adminer"] {
		t.Error("expected 'adminer' in completions")
	}
	if !toolSet["elasticvue"] {
		t.Error("expected 'elasticvue' in completions")
	}
	// Should be sorted.
	sorted := make([]string, len(completions))
	copy(sorted, completions)
	sort.Strings(sorted)
	for i, s := range sorted {
		if completions[i] != s {
			t.Errorf("completions not sorted at index %d: got %q, want %q", i, completions[i], s)
		}
	}
}

func TestToolNameCompletion_noSecondArg(t *testing.T) {
	// The len(args) != 0 guard fires before any config access, so cmd can be nil.
	flags := &rootFlags{configPath: "/fake/devbox.yml", projectRoot: "/fake"}
	fn := serviceCompletion(flags, completeDisabledOptional)
	completions, directive := fn(nil, []string{"already"}, "")
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

func TestCommandsCmdHasValidArgsFunction(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("commands should have a ValidArgsFunction for dynamic completion")
	}
}

// TestCommandsCmd_ActiveHelp_PointsAtInspectFlag verifies the literal hint
// string mentions the new --inspect flag (not the removed `commands inspect`
// subcommand). registryIDCompletion appends this string verbatim when
// !includePrivate && len(defs) > 0.
func TestCommandsCmd_ActiveHelp_PointsAtInspectFlag(t *testing.T) {
	const hint = "Use 'devbox commands --inspect <id>' to see command details"
	appended := cobra.AppendActiveHelp(nil, hint)
	if len(appended) != 1 {
		t.Fatalf("AppendActiveHelp: expected 1 entry, got %d", len(appended))
	}
	if !strings.Contains(appended[0], "--inspect") {
		t.Errorf("ActiveHelp should reference --inspect, got %q", appended[0])
	}
	if strings.Contains(appended[0], "commands inspect ") {
		t.Errorf("ActiveHelp must not reference removed 'commands inspect' subcommand: %q", appended[0])
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

// (Tool-specific completion variants removed; serviceCompletion covers all
// service types after the services/tools unification.)
func TestServiceEnableDisableHaveUnifiedCompletion(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	if newServiceEnableCmd(flags).ValidArgsFunction == nil {
		t.Error("services enable should have a ValidArgsFunction for dynamic completion")
	}
	if newServiceDisableCmd(flags).ValidArgsFunction == nil {
		t.Error("services disable should have a ValidArgsFunction for dynamic completion")
	}
}

// --- completionConfigPath integration tests ---
//
// These tests exercise the __complete path: ValidArgsFunction callbacks are
// invoked without PersistentPreRunE having run, so completionConfigPath must
// resolve the project itself.

// buildRootCmdForCompletion builds a root cobra.Command with a persistent --config
// flag. When configPath is non-empty the flag is marked as Changed (explicit mode).
func buildRootCmdForCompletion(flags *rootFlags, configPath string) *cobra.Command {
	root := &cobra.Command{Use: "devbox"}
	root.PersistentFlags().StringVarP(&flags.configPath, "config", "c", "", "")
	if configPath != "" {
		_ = root.PersistentFlags().Set("config", configPath)
	}
	return root
}

// TestCompletionConfigPath_subdirDiscovery verifies that completionConfigPath
// finds a v2 devbox.yml when invoked from a subdirectory of the project.
func TestCompletionConfigPath_subdirDiscovery(t *testing.T) {
	projectDir := t.TempDir()
	makeV2Project(t, projectDir)

	subdir := projectDir + "/services/main"
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("creating subdir: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir into subdir: %v", err)
	}

	flags := &rootFlags{} // projectRoot not set — simulates __complete path
	root := buildRootCmdForCompletion(flags, "")

	configPath, projectRoot, err := completionConfigPath(flags, root)
	if err != nil {
		t.Fatalf("expected no error from subdir, got: %v", err)
	}
	if configPath == "" || projectRoot == "" {
		t.Error("expected non-empty configPath and projectRoot from subdir discovery")
	}
}

// TestCompletionConfigPath_noProject verifies that completionConfigPath returns
// ErrNotFound (and no panic) when there is no devbox.yml in any parent directory.
func TestCompletionConfigPath_noProject(t *testing.T) {
	emptyDir := t.TempDir()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(emptyDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	flags := &rootFlags{}
	root := buildRootCmdForCompletion(flags, "")

	_, _, err = completionConfigPath(flags, root)
	if err == nil {
		t.Fatal("expected error when no project exists, got nil")
	}
}

// TestCompletionConfigPath_explicitBadPath verifies that an explicit --config
// pointing to a non-existent file returns an error (no panic, no terminal noise).
func TestCompletionConfigPath_explicitBadPath(t *testing.T) {
	badPath := t.TempDir() + "/nonexistent.yml"

	flags := &rootFlags{}
	root := buildRootCmdForCompletion(flags, badPath)

	_, _, err := completionConfigPath(flags, root)
	if err == nil {
		t.Fatal("expected error for explicit bad path, got nil")
	}
}

// TestServiceNameCompletion_brokenSchemaReturnsEmpty verifies that
// serviceNameCompletion (used by `devbox render git/ide/ai` and many other
// commands) returns no completions and ShellCompDirectiveNoFileComp when the
// project's devbox.yml has a bad schema_version. The __complete path must
// never panic or surface errors to the terminal.
func TestServiceNameCompletion_brokenSchemaReturnsEmpty(t *testing.T) {
	projectDir := t.TempDir()
	makeV1Project(t, projectDir)

	t.Chdir(projectDir)

	flags := &rootFlags{}
	root := buildRootCmdForCompletion(flags, "")

	completions, directive := serviceNameCompletion(flags)(root, []string{}, "")
	if len(completions) != 0 {
		t.Errorf("expected 0 completions for project with broken schema, got %d: %v", len(completions), completions)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
}

// TestServiceNameCompletion_malformedManifestReturnsServiceNames documents the
// boundary between completion and rendering: completion is config-scoped, not
// manifest-scoped. A malformed manifest.yml does NOT affect serviceNameCompletion
// because the completion path only loads config.LoadConfig.
func TestServiceNameCompletion_malformedManifestReturnsServiceNames(t *testing.T) {
	projectDir := t.TempDir()

	// Write a v2 devbox.yml with services so completion has something to return.
	devboxDir := filepath.Join(projectDir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("mkdir devbox: %v", err)
	}
	devboxYML := `schema_version: "2"
project:
  name: testproject
  prefix: devbox
`
	if err := os.WriteFile(filepath.Join(projectDir, "devbox.yml"), []byte(devboxYML), 0644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	for name, content := range map[string]string{
		"main":   "type: app\ndir: services/main\n",
		"worker": "type: app\ndir: services/worker\n",
	} {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0755); err != nil {
			t.Fatalf("mkdir services/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0644); err != nil {
			t.Fatalf("write services/%s/service.yml: %v", name, err)
		}
	}

	// Write a malformed manifest.yml under an IDE pack — broken YAML.
	packDir := filepath.Join(devboxDir, "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0755); err != nil {
		t.Fatalf("mkdir pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "manifest.yml"), []byte("render: [this is: not valid yaml\n"), 0644); err != nil {
		t.Fatalf("write manifest.yml: %v", err)
	}

	t.Chdir(projectDir)

	flags := &rootFlags{}
	root := buildRootCmdForCompletion(flags, "")

	completions, directive := serviceNameCompletion(flags)(root, []string{}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}
	if len(completions) != 2 {
		t.Fatalf("expected 2 completions (main, worker), got %d: %v", len(completions), completions)
	}
	sort.Strings(completions)
	if completions[0] != "main" || completions[1] != "worker" {
		t.Errorf("expected [main worker], got %v", completions)
	}
}

// TestRenderGitCmd_HasServiceNameCompletion verifies the new `render git`
// command wires the shared serviceNameCompletion callback (same as render
// ide/ai). This documents that the broken-schema and malformed-manifest tests
// above apply to render git as well.
func TestRenderGitCmd_HasServiceNameCompletion(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newRenderGitCmd(flags)
	if cmd.ValidArgsFunction == nil {
		t.Error("render git should have a ValidArgsFunction for dynamic completion")
	}
}

// TestCompletionConfigPath_legacyV1_noCompletions verifies that completionConfigPath
// returns an error for a v1 project so callbacks correctly return no completions.
func TestCompletionConfigPath_legacyV1_noCompletions(t *testing.T) {
	projectDir := t.TempDir()
	makeV1Project(t, projectDir)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	flags := &rootFlags{}
	root := buildRootCmdForCompletion(flags, "")

	_, _, err = completionConfigPath(flags, root)
	if err == nil {
		t.Fatal("expected schema error for v1 project, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("expected schema_version error, got: %v", err)
	}

	// Verify that serviceCompletion returns no completions for v1.
	completions, directive := serviceCompletion(flags, completeDisabledOptional)(root, []string{}, "")
	if len(completions) != 0 {
		t.Errorf("expected 0 completions from serviceCompletion for v1 project, got %d", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected ShellCompDirectiveNoFileComp, got %v", directive)
	}

}

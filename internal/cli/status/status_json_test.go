// golden-normalize: before diffing against golden files, timestamps in
// "deployed_at" and "started_at" fields are replaced with "<TS>" so golden
// files remain stable across runs.
package status

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/statustui"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"

	"github.com/spf13/cobra"
)

// buildStatusJSONRoot constructs a minimal cobra root for JSON mode tests.
// flags.Output should be set to "json" (and optionally Pretty=true) before use.
func buildStatusJSONRoot(flags *cmdctx.RootFlags) *cobra.Command {
	root := &cobra.Command{Use: "dwe", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVarP(&flags.ConfigPath, "config", "c", "", "")
	root.AddGroup(&cobra.Group{ID: "environment", Title: "Environment Commands:"})
	root.AddCommand(NewCmd("environment", flags))
	return root
}

// runStatusJSON executes the status command with JSON flags and returns stdout.
func runStatusJSON(t *testing.T, configPath string, flags *cmdctx.RootFlags, subArgs ...string) string {
	t.Helper()
	root := buildStatusJSONRoot(flags)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&bytes.Buffer{})
	args := append([]string{"-c", configPath, "status"}, subArgs...)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return buf.String()
}

// normalizeTimestamps replaces RFC3339 timestamp values in JSON output with
// the placeholder "<TS>" so golden files stay stable.
func normalizeTimestamps(s string) string {
	result := s
	for _, key := range []string{"deployed_at", "started_at", "project_deployed_at", "finished_at"} {
		result = replaceJSONStringValue(result, key, "<TS>")
	}
	return result
}

// replaceJSONStringValue replaces all occurrences of "key":"<value>" in a
// JSON string with "key":"replacement".
func replaceJSONStringValue(s, key, replacement string) string {
	search := `"` + key + `":"`
	result := s
	for {
		idx := strings.Index(result, search)
		if idx < 0 {
			break
		}
		start := idx + len(search)
		end := strings.Index(result[start:], `"`)
		if end < 0 {
			break
		}
		result = result[:start] + replacement + result[start+end:]
	}
	return result
}

func updateOrCheckGolden(t *testing.T, goldenPath, got string) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run with UPDATE_GOLDEN=1 to create it)", goldenPath, err)
	}
	want := strings.TrimRight(string(raw), "\n")
	if got != want {
		t.Errorf("JSON output mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

// statusFixtureWithTrackedDeploy creates a fixture where 'main' is an enabled
// service with a valid per-service deploy.yml, making it tracked by
// LoadTrackedServices. Also writes a state file showing main as deployed.
func statusFixtureWithTrackedDeploy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	devboxYML := `schema_version: "2"
project:
  name: test
  prefix: devbox
`
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// defaults.yml enables main so it appears in ResolvePlan's enabled set.
	defaultsYML := `services:
  main:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Service directory.
	mainSvcDir := filepath.Join(devboxDir, "services", "main")
	if err := os.MkdirAll(mainSvcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainSvcYML := "type: app\ncontainer: app-main\nrequired: true\n"
	if err := os.WriteFile(filepath.Join(mainSvcDir, "service.yml"), []byte(mainSvcYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Per-service deploy.yml in the correct location (devbox/services/main/deploy.yml).
	mainDeployYML := `phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: echo hello
`
	if err := os.WriteFile(filepath.Join(mainSvcDir, "deploy.yml"), []byte(mainDeployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project-level deploy.yml with deploy_services: true.
	projDeployYML := `phases:
  - name: services
    deploy_services: true
`
	if err := os.WriteFile(filepath.Join(devboxDir, "deploy.yml"), []byte(projDeployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// State file showing main as deployed.
	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	state := &journal.ProjectState{
		Services: map[string]*journal.ServiceState{
			"main": {Status: journal.StatusDeployed},
		},
	}
	if err := journal.Save(statePath, state); err != nil {
		t.Fatal(err)
	}

	return filepath.Join(dir, "devbox.yml")
}

func TestStatusCmd_JSONMode_CompositeGolden(t *testing.T) {
	configPath := statusFixture(t)
	flags := &cmdctx.RootFlags{Output: "json"}

	got := strings.TrimRight(normalizeTimestamps(runStatusJSON(t, configPath, flags)), "\n")

	// Verify it's valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot: %s", err, got)
	}

	updateOrCheckGolden(t, "testdata/status.json.golden", got)
}

func TestStatusCmd_JSONMode_AppsSubcommandGolden(t *testing.T) {
	configPath := statusFixture(t)
	flags := &cmdctx.RootFlags{Output: "json"}

	got := strings.TrimRight(normalizeTimestamps(runStatusJSON(t, configPath, flags, "apps")), "\n")

	// Verify it's valid JSON with "apps" key.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot: %s", err, got)
	}
	if _, ok := parsed["apps"]; !ok {
		t.Errorf("expected 'apps' key in JSON output, got: %s", got)
	}

	updateOrCheckGolden(t, "testdata/status_apps.json.golden", got)
}

func TestStatusCmd_JSONMode_DeployDetailGolden(t *testing.T) {
	configPath := statusFixtureWithTrackedDeploy(t)
	flags := &cmdctx.RootFlags{Output: "json"}

	got := strings.TrimRight(normalizeTimestamps(runStatusJSON(t, configPath, flags, "deploy", "main")), "\n")

	// Verify it's valid JSON with "deploy" key.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot: %s", err, got)
	}
	if _, ok := parsed["deploy"]; !ok {
		t.Errorf("expected 'deploy' key in JSON output, got: %s", got)
	}

	updateOrCheckGolden(t, "testdata/status_deploy_detail.json.golden", got)
}

func TestStatusCmd_JSONMode_NoTUI_WhenJSONSet(t *testing.T) {
	// Must NOT use t.Parallel() — mutates package-level TUI seams.
	configPath := statusFixture(t)

	oldRunStatusTUIFn := runStatusTUIFn
	oldIsTerminalFn := isTerminalFn
	oldEnvTERM := os.Getenv("TERM")
	defer func() {
		runStatusTUIFn = oldRunStatusTUIFn
		isTerminalFn = oldIsTerminalFn
		_ = os.Setenv("TERM", oldEnvTERM)
	}()

	// Panicking stub: TUI must NOT be invoked when --output json is set.
	runStatusTUIFn = func(ctx context.Context, d statustui.Deps) error {
		t.Fatal("TUI should not be invoked when --output json is active")
		return nil
	}
	// Simulate a full TTY to ensure the JSON flag overrides TTY detection.
	isTerminalFn = func(fd uintptr) bool { return true }
	if err := os.Setenv("TERM", "xterm-256color"); err != nil {
		t.Fatalf("failed to set TERM: %v", err)
	}

	flags := &cmdctx.RootFlags{Output: "json"}
	out := runStatusJSON(t, configPath, flags)

	// Verify output is valid JSON (not empty or TUI output).
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
		t.Errorf("output should be valid JSON when --output json, got: %q", out)
	}
}

func TestStatusCmd_JSONMode_PrettyIndented(t *testing.T) {
	configPath := statusFixture(t)
	flags := &cmdctx.RootFlags{Output: "json", Pretty: true}

	out := runStatusJSON(t, configPath, flags)

	if !strings.Contains(out, "\n  ") {
		t.Errorf("pretty JSON should contain indented lines; got: %q", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
		t.Errorf("pretty output should be valid JSON: %v\ngot: %q", err, out)
	}
}

func TestStatusCmd_JSONMode_SubcommandsHaveCorrectKeys(t *testing.T) {
	tests := []struct {
		subcmd  string
		wantKey string
		fixture func(*testing.T) string
	}{
		{"apps", "apps", statusFixture},
		{"tools", "tools", statusFixture},
		{"infra", "infra", statusFixtureWithInfra},
		{"daemons", "daemons", statusFixture},
		{"deploy", "deploy", statusFixture},
		{"topology", "topology", statusFixture},
		{"git", "git", statusFixture},
	}

	for _, tt := range tests {
		t.Run(tt.subcmd, func(t *testing.T) {
			configPath := tt.fixture(t)
			flags := &cmdctx.RootFlags{Output: "json"}

			out := runStatusJSON(t, configPath, flags, tt.subcmd)

			var parsed map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
				t.Fatalf("output is not valid JSON: %v\ngot: %s", err, out)
			}
			if _, ok := parsed[tt.wantKey]; !ok {
				t.Errorf("expected %q key in JSON output, got: %s", tt.wantKey, out)
			}
			// Subcommands should emit only their own section.
			if len(parsed) != 1 {
				t.Errorf("subcommand %q should emit only 1 root key, got %d: %v", tt.subcmd, len(parsed), out)
			}
		})
	}
}

func TestStatusCmd_JSONMode_DeployDetailUnknownService_Errors(t *testing.T) {
	configPath := statusFixtureWithTrackedDeploy(t)
	flags := &cmdctx.RootFlags{Output: "json"}

	root := buildStatusJSONRoot(flags)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"-c", configPath, "status", "deploy", "nonexistent"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for unknown service in JSON mode")
	}
	if !strings.Contains(err.Error(), "not tracked") {
		t.Errorf("expected 'not tracked' in error, got: %v", err)
	}
}

func TestStatusCmd_JSONMode_CompositeNoSectionFlags(t *testing.T) {
	configPath := statusFixture(t)
	flags := &cmdctx.RootFlags{Output: "json"}

	root := buildStatusJSONRoot(flags)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"-c", configPath, "status", "--no-apps", "--no-tools"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := buf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot: %s", err, out)
	}
	if _, ok := parsed["apps"]; ok {
		t.Errorf("--no-apps should omit 'apps' key from JSON output, got: %s", out)
	}
	if _, ok := parsed["tools"]; ok {
		t.Errorf("--no-tools should omit 'tools' key from JSON output, got: %s", out)
	}
}

// TestStatusCmd_JSONMode_BuildDeployDetail verifies detail JSON includes phases.
func TestStatusCmd_JSONMode_BuildDeployDetail(t *testing.T) {
	configPath := statusFixtureWithTrackedDeploy(t)
	dir := filepath.Dir(configPath)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	// Write a richer state so the detail JSON includes phases.
	state := &journal.ProjectState{
		Services: map[string]*journal.ServiceState{
			"main": {
				Status:     journal.StatusDeployed,
				ConfigHash: "abc123",
				Phases: map[string]*journal.PhaseState{
					"setup": {
						Status: journal.StatusOk,
						Steps: map[string]*journal.StepState{
							"noop": {
								Status:     journal.StatusOk,
								ActionHash: "xyzxyz",
								DurationMs: 42,
							},
						},
					},
				},
			},
		},
	}
	if err := journal.Save(statePath, state); err != nil {
		t.Fatalf("saving state: %v", err)
	}

	flags := &cmdctx.RootFlags{Output: "json"}
	out := runStatusJSON(t, configPath, flags, "deploy", "main")

	var result struct {
		Deploy struct {
			Service string                     `json:"service"`
			Status  string                     `json:"status"`
			Phases  map[string]json.RawMessage `json:"phases"`
		} `json:"deploy"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot: %s", err, out)
	}
	if result.Deploy.Service != "main" {
		t.Errorf("expected service 'main', got: %q", result.Deploy.Service)
	}
	if result.Deploy.Status != "deployed" {
		t.Errorf("expected status 'deployed', got: %q", result.Deploy.Status)
	}
	if _, ok := result.Deploy.Phases["setup"]; !ok {
		t.Errorf("expected 'setup' phase in output, got: %s", out)
	}
}

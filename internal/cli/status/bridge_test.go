package status

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/bridge"
)

// init stubs the bridge-ensure seam for the whole test binary: the status
// fixtures contain required (→ enabled) app services, which are
// bridge-enabled by default, and the real bridge.Ensure would spawn a
// detached daemon via os.Executable() — re-executing the test binary (the
// documented recursion hazard). Bridge tests install recorders instead.
func init() {
	bridgeEnsureFn = func(bridge.EnsureConfig) (bool, error) { return false, nil }
}

// statusFixtureBridgeOff builds a project whose only enabled service opts
// out of the bridge, so AnyBridgeEnabled is false.
func statusFixtureBridgeOff(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgYAML := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"
	if err := writeFile(t, filepath.Join(dir, "workspace.yml"), cfgYAML); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "workspace", "services", "main")
	svcYML := "type: app\ncontainer: app-main\nrequired: true\nbridge:\n  enabled: false\n"
	if err := writeFile(t, filepath.Join(svcDir, "service.yml"), svcYML); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "workspace.yml")
}

// writeFile creates parent dirs and writes content (0644).
func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// recordBridgeEnsure swaps the ensure seam for a recorder returning err.
func recordBridgeEnsure(t *testing.T, err error) *[]bridge.EnsureConfig {
	t.Helper()
	var calls []bridge.EnsureConfig
	prev := bridgeEnsureFn
	t.Cleanup(func() { bridgeEnsureFn = prev })
	bridgeEnsureFn = func(cfg bridge.EnsureConfig) (bool, error) {
		calls = append(calls, cfg)
		return false, err
	}
	return &calls
}

// TestStatusCmd_TopLevel_EnsuresBridgeDaemon: the top-level status performs
// the best-effort daemon ensure (design D6) — the fixture's required app is
// bridge-enabled by default.
func TestStatusCmd_TopLevel_EnsuresBridgeDaemon(t *testing.T) {
	calls := recordBridgeEnsure(t, nil)
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("ensure called %d times, want 1", len(*calls))
	}
	if want := filepath.Dir(configPath); (*calls)[0].ProjectRoot != want {
		t.Errorf("ensure ProjectRoot = %q, want %q", (*calls)[0].ProjectRoot, want)
	}
}

// TestStatusCmd_TopLevel_EnsureErrorIsSwallowed: a failing ensure must never
// fail status and must not leak into the human output (it is traced under
// --debug only).
func TestStatusCmd_TopLevel_EnsureErrorIsSwallowed(t *testing.T) {
	recordBridgeEnsure(t, errors.New("spawn refused"))
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status must not fail on a bridge ensure error, got: %v", err)
	}
	if out := buf.String(); bytes.Contains(buf.Bytes(), []byte("spawn refused")) {
		t.Errorf("ensure error leaked into output:\n%s", out)
	}
}

// TestStatusCmd_Subcommands_DoNotEnsure mirrors the prompt-cache contract:
// only the top-level status performs the ensure, section subcommands stay
// fully passive.
func TestStatusCmd_Subcommands_DoNotEnsure(t *testing.T) {
	calls := recordBridgeEnsure(t, nil)
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status", "apps"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("ensure called %d times by a subcommand, want 0", len(*calls))
	}
}

// TestStatusCmd_NoBridgedServices_NoEnsure: with every service's bridge off,
// the gate must skip the ensure entirely (no daemon for nothing).
func TestStatusCmd_NoBridgedServices_NoEnsure(t *testing.T) {
	calls := recordBridgeEnsure(t, nil)
	configPath := statusFixtureBridgeOff(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("ensure called %d times with bridge fully off, want 0", len(*calls))
	}
}

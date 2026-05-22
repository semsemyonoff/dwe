package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestValidateDependsOnTypes_rejectsTool confirms the loader-side gate rejects
// any depends_on target that resolves to a tool-typed service. The check runs
// on every LoadConfig path, not just `devbox validate`.
func TestValidateDependsOnTypes_rejectsTool(t *testing.T) {
	services := map[string]ServiceConfig{
		"app":     {Type: ServiceTypeApp, Container: "app", DependsOn: []string{"adminer"}},
		"adminer": {Type: ServiceTypeTool, Container: "adminer"},
	}
	err := validateDependsOnTypes(services)
	if err == nil {
		t.Fatal("expected ErrDependsOnTool")
	}
	if !errors.Is(err, ErrDependsOnTool) {
		t.Fatalf("err = %v, want wraps ErrDependsOnTool", err)
	}
}

// TestValidateDependsOnTypes_allowsApp confirms depends_on between apps is
// legal, and depends_on on infra is legal as well.
func TestValidateDependsOnTypes_allowsAppAndInfra(t *testing.T) {
	services := map[string]ServiceConfig{
		"main": {Type: ServiceTypeApp, Container: "main", DependsOn: []string{"db", "worker"}},
		"db":   {Type: ServiceTypeInfra, Container: "db"},
		"worker": {Type: ServiceTypeApp, Container: "worker"},
	}
	if err := validateDependsOnTypes(services); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateDependsOnTypes_unknownTargetTolerated confirms that unknown
// depends_on targets are NOT this gate's responsibility — TopoSortServices
// surfaces them at plan time.
func TestValidateDependsOnTypes_unknownTargetTolerated(t *testing.T) {
	services := map[string]ServiceConfig{
		"main": {Type: ServiceTypeApp, Container: "main", DependsOn: []string{"missing"}},
	}
	if err := validateDependsOnTypes(services); err != nil {
		t.Fatalf("unknown depends_on target should be tolerated here, got %v", err)
	}
}

// TestValidateServiceDeployFiles_rejectsNonApp verifies a deploy file owned by
// a tool/infra service produces ErrDeployFileForNonApp.
func TestValidateServiceDeployFiles_rejectsNonApp(t *testing.T) {
	dir := t.TempDir()
	deployDir := filepath.Join(dir, "devbox", "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "adminer.yml"), []byte("phases: []\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	services := map[string]ServiceConfig{
		"adminer": {Type: ServiceTypeTool},
	}
	err := ValidateServiceDeployFiles(dir, services)
	if err == nil {
		t.Fatal("expected ErrDeployFileForNonApp")
	}
	if !errors.Is(err, ErrDeployFileForNonApp) {
		t.Fatalf("err = %v, want wraps ErrDeployFileForNonApp", err)
	}
}

// TestValidateServiceDeployFiles_rejectsUnknownStem verifies deploy files that
// name no declared service are rejected (same sentinel — "silently wrong"
// drift the pre-release policy forbids).
func TestValidateServiceDeployFiles_rejectsUnknownStem(t *testing.T) {
	dir := t.TempDir()
	deployDir := filepath.Join(dir, "devbox", "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "ghost.yml"), []byte("phases: []\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	err := ValidateServiceDeployFiles(dir, map[string]ServiceConfig{})
	if err == nil {
		t.Fatal("expected ErrDeployFileForNonApp")
	}
	if !errors.Is(err, ErrDeployFileForNonApp) {
		t.Fatalf("err = %v, want wraps ErrDeployFileForNonApp", err)
	}
}

// TestValidateServiceDeployFiles_acceptsApp confirms a deploy file for an
// app-typed service is the happy path.
func TestValidateServiceDeployFiles_acceptsApp(t *testing.T) {
	dir := t.TempDir()
	deployDir := filepath.Join(dir, "devbox", "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "main.yml"), []byte("phases: []\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	services := map[string]ServiceConfig{
		"main": {Type: ServiceTypeApp},
	}
	if err := ValidateServiceDeployFiles(dir, services); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateServiceDeployFiles_missingDirOK verifies the absence of the
// deploy directory is silent (not every project has one).
func TestValidateServiceDeployFiles_missingDirOK(t *testing.T) {
	if err := ValidateServiceDeployFiles(t.TempDir(), map[string]ServiceConfig{}); err != nil {
		t.Fatalf("missing deploy dir should be tolerated, got %v", err)
	}
}

// TestComposeFiles_grouped_tool_infra_app verifies the explicit grouping
// order: base → tools (sorted) → infra (sorted) → apps (sorted). This order is
// part of the public surface — overlay precedence depends on it.
func TestComposeFiles_grouped_tool_infra_app(t *testing.T) {
	cfg := &DevboxConfig{
		Compose: ComposeConfig{Base: "compose.yaml"},
		Services: map[string]ServiceConfig{
			"zzz_tool": {Type: ServiceTypeTool, Enabled: true, Compose: []string{"tool-z.yml"}},
			"aaa_tool": {Type: ServiceTypeTool, Enabled: true, Compose: []string{"tool-a.yml"}},
			"db":       {Type: ServiceTypeInfra, Enabled: true, Compose: []string{"infra-db.yml"}},
			"cache":    {Type: ServiceTypeInfra, Enabled: true, Compose: []string{"infra-cache.yml"}},
			"web":      {Type: ServiceTypeApp, Enabled: true, Compose: []string{"app-web.yml"}},
			"api":      {Type: ServiceTypeApp, Enabled: true, Compose: []string{"app-api.yml"}},
		},
	}
	want := []string{
		"compose.yaml",
		"tool-a.yml",
		"tool-z.yml",
		"infra-cache.yml",
		"infra-db.yml",
		"app-api.yml",
		"app-web.yml",
	}
	got := cfg.ComposeFiles()
	if len(got) != len(want) {
		t.Fatalf("ComposeFiles() len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// ComposeFilesAll() must produce the same ordering when every service is enabled.
	all := cfg.ComposeFilesAll()
	for i := range want {
		if all[i] != want[i] {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, all[i], want[i])
		}
	}
}

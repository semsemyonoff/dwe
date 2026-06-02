package stack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// testTool is the legacy tool shape used by stack tests pre-unification. It
// is converted into a ServiceConfig with Type=tool by makeServicesCfg.
type testTool struct {
	Enabled   bool
	Container string
	Host      string
	Port      int
	Status    []config.StatusColumn
}

// makeServicesCfg builds a minimal DweConfig for stack tests. Optional
// tools are merged into services with Type=ServiceTypeTool.
func makeServicesCfg(services map[string]config.ServiceConfig, tools map[string]testTool, _ any, _ any) *config.DweConfig {
	merged := make(map[string]config.ServiceConfig, len(services)+len(tools))
	for k, v := range services {
		if v.Type == "" {
			v.Type = config.ServiceTypeApp
		}
		merged[k] = v
	}
	for k, v := range tools {
		svc := config.ServiceConfig{
			Type:      config.ServiceTypeTool,
			Container: v.Container,
			Enabled:   v.Enabled,
			Status:    v.Status,
		}
		if v.Port != 0 {
			svc.Ports = map[string]config.ServicePortSpec{"main": {Port: v.Port}}
		}
		if v.Host != "" {
			svc.Hosts = map[string]string{"main": v.Host}
		}
		merged[k] = svc
	}
	return &config.DweConfig{Services: merged}
}

func makeMinimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgYAML := `project:
  name: test
  prefix: dwe
services:
  main:
    type: app
    dir: ./services/main
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerYML := "project_name: dwe-test\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(dockerYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

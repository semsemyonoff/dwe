package stack

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
)

func makeServicesCfg(services map[string]config.ServiceConfig, tools config.ToolsConfig, ports config.RuntimePorts, hosts config.RuntimeHosts) *config.DevboxConfig {
	return &config.DevboxConfig{
		Services: services,
		Tools:    tools,
		Runtime: config.RuntimeConfig{
			Ports: ports,
			Hosts: hosts,
		},
	}
}

func makeMinimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	devboxYML := `project:
  name: test
  prefix: devbox
services:
  main:
    type: app
    dir: ./services/main
`
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerYML := "project_name: devbox-test\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "docker.yml"), []byte(dockerYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

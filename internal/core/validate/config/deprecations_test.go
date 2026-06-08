package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// writeFile writes content to path under root, creating parent dirs.
func writeProjectFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func runDeprecationsValidator(t *testing.T, root string, cfg *config.DweConfig) []validate.Diagnostic {
	t.Helper()
	return (&deprecationsValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: cfg})
}

// findDiag returns the first diagnostic whose Message contains sub, or nil.
func findDiag(diags []validate.Diagnostic, sub string) *validate.Diagnostic {
	for i := range diags {
		if strings.Contains(diags[i].Message, sub) {
			return &diags[i]
		}
	}
	return nil
}

func TestDeprecationsValidator_ServiceYML_ConfigsAndMountpoint(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "workspace/services/main/service.yml", `type: app
dir: services/main
configs:
  - file: .env
    mountpoint: src/.env
`)
	cfg := cfgWithServices(map[string]config.ServiceConfig{"main": {Dir: "services/main"}})

	diags := runDeprecationsValidator(t, root, cfg)

	configsDiag := findDiag(diags, "configs: is deprecated")
	require.NotNil(t, configsDiag, "expected a configs: deprecation warning")
	require.Equal(t, validate.SeverityWarning, configsDiag.Severity)
	require.Equal(t, "workspace/services/main/service.yml", configsDiag.File)
	require.Equal(t, 3, configsDiag.Line, "configs: is on line 3")
	require.Contains(t, configsDiag.Hint, "render.config")

	mpDiag := findDiag(diags, "mountpoint: is deprecated")
	require.NotNil(t, mpDiag, "expected a mountpoint: deprecation warning")
	require.Equal(t, validate.SeverityWarning, mpDiag.Severity)
	require.Equal(t, 5, mpDiag.Line, "mountpoint: is on line 5")

	// No errors — deprecations are non-fatal.
	require.Equal(t, 0, countSeverity(diags, validate.SeverityError))
}

func TestDeprecationsValidator_DeployYML_CopyBuiltins(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "workspace/services/main/service.yml", `type: app
dir: services/main
`)
	writeProjectFile(t, root, "workspace/deploy.yml", `phases:
  - name: configs
    steps:
      - name: copy
        type: builtin
        cmd: service_configs_copy
        with:
          service: main
        check:
          type: builtin
          cmd: service_configs_check
          with:
            service: main
`)
	cfg := cfgWithServices(map[string]config.ServiceConfig{"main": {Dir: "services/main"}})

	diags := runDeprecationsValidator(t, root, cfg)

	copyDiag := findDiag(diags, "service_configs_copy is a deprecated copy builtin")
	require.NotNil(t, copyDiag, "expected service_configs_copy deprecation warning")
	require.Equal(t, validate.SeverityWarning, copyDiag.Severity)
	require.Equal(t, "workspace/deploy.yml", copyDiag.File)
	require.Equal(t, 6, copyDiag.Line, "service_configs_copy cmd is on line 6")

	checkDiag := findDiag(diags, "service_configs_check is a deprecated copy builtin")
	require.NotNil(t, checkDiag, "expected service_configs_check deprecation warning")
	require.Equal(t, validate.SeverityWarning, checkDiag.Severity)
	require.Equal(t, 11, checkDiag.Line, "service_configs_check cmd is on line 11")

	require.Equal(t, 0, countSeverity(diags, validate.SeverityError))
}

func TestDeprecationsValidator_PerServicePipelines(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "workspace/services/main/service.yml", "type: app\ndir: services/main\n")
	writeProjectFile(t, root, "workspace/services/main/deploy.yml", `phases:
  - name: configs
    steps:
      - type: builtin
        cmd: service_configs_copy
        with:
          service: main
`)
	writeProjectFile(t, root, "workspace/services/main/reset.yml", `phases:
  - name: cleanup
    steps:
      - type: builtin
        cmd: service_configs_check
        with:
          service: main
`)
	cfg := cfgWithServices(map[string]config.ServiceConfig{"main": {Dir: "services/main"}})

	diags := runDeprecationsValidator(t, root, cfg)

	dep := findDiag(diags, "service_configs_copy is a deprecated copy builtin")
	require.NotNil(t, dep)
	require.Equal(t, "workspace/services/main/deploy.yml", dep.File)

	rst := findDiag(diags, "service_configs_check is a deprecated copy builtin")
	require.NotNil(t, rst)
	require.Equal(t, "workspace/services/main/reset.yml", rst.File)
}

func TestDeprecationsValidator_Clean_NoWarnings(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "workspace/services/main/service.yml", `type: app
dir: services/main
render:
  config:
    template: laravel
generated:
  app_key:
    file: configs/.env
    pattern: '^APP_KEY=(.*)$'
`)
	writeProjectFile(t, root, "workspace/deploy.yml", `phases:
  - name: configs
    steps:
      - type: builtin
        cmd: service_configs_render
        with:
          service: main
`)
	cfg := cfgWithServices(map[string]config.ServiceConfig{"main": {Dir: "services/main"}})

	diags := runDeprecationsValidator(t, root, cfg)

	require.Equal(t, 0, countSeverity(diags, validate.SeverityWarning), "modern config should produce no deprecation warnings")
	require.Equal(t, 0, countSeverity(diags, validate.SeverityError))
}

func TestDeprecationsValidator_MissingFiles_Silent(t *testing.T) {
	root := t.TempDir()
	// No service.yml / deploy.yml on disk, but a service is declared in cfg.
	cfg := cfgWithServices(map[string]config.ServiceConfig{"main": {Dir: "services/main"}})

	diags := runDeprecationsValidator(t, root, cfg)
	require.Empty(t, diags, "missing pipeline/service files must not produce diagnostics")
}

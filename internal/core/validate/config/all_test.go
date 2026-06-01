package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestAllValidators(t *testing.T) {
	// Create a temporary project with various config files
	tmpDir := t.TempDir()

	// Create workspace.yml
	cfgYAML := `project:
  name: test-project
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "workspace.yml"), []byte(cfgYAML), 0644))

	// Create workspace directory
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.Mkdir(workspaceDir, 0755))

	// Create per-folder service
	svcDir := filepath.Join(workspaceDir, "services", "app")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: app\ndir: /app\ncontainer: app\n"), 0644))

	// Create docker.yml
	dockerYml := `project_name: test-project
`
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(dockerYml), 0644))

	// Create info.yml
	infoYml := `sections:
  - name: info
    items:
      - type: info
        title: Test Info
        items:
          - type: definition
            label: Key
            title: Value
`
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "info.yml"), []byte(infoYml), 0644))

	// Create styles.yml  (minimal)
	stylesYml := `palette: {}
`
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "styles.yml"), []byte(stylesYml), 0644))

	// Create lifecycle.yml
	lifecycleYml := `run:
  phases:
    - name: setup
      steps:
        - name: info
          type: builtin
          cmd: message
          with:
            level: info
            text: Starting...
`
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(lifecycleYml), 0644))

	// Create deploy.yml
	deployYml := `phases:
  - name: setup
    steps:
      - name: message
        type: builtin
        cmd: message
        with:
          level: info
          text: Deploying...
`
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(deployYml), 0644))

	// Create reset.yml
	resetYml := `phases:
  - name: cleanup
    steps:
      - name: message
        type: builtin
        cmd: message
        with:
          level: info
          text: Cleaning up...
`
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "reset.yml"), []byte(resetYml), 0644))

	// Run all validators
	ctx := validate.Context{
		ProjectRoot: tmpDir,
		ConfigPath:  filepath.Join(tmpDir, "workspace.yml"),
	}

	// Create a mini registry and run all validators
	validators := All()
	var allDiags []validate.Diagnostic
	for _, v := range validators {
		allDiags = append(allDiags, v.Run(ctx)...)
	}

	// Should have multiple OK diagnostics
	okCount := 0
	for _, d := range allDiags {
		if d.Severity == validate.SeverityOK || d.Severity == validate.SeverityInfo {
			okCount++
		}
	}
	require.Greater(t, okCount, 0, "should have at least one successful validation")

	// Should have no errors in this well-formed project
	for _, d := range allDiags {
		if d.Severity == validate.SeverityError {
			t.Errorf("unexpected error: %s / %s / %s", d.Target, d.File, d.Message)
		}
	}
}

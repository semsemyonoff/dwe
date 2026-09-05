package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// writeExportsProject writes a project whose workspace.yml is the given body
// and returns its root.
func writeExportsProject(t *testing.T, workspace string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspace), 0o644))
	return root
}

func runExportsValidator(t *testing.T, root string) []validate.Diagnostic {
	t.Helper()
	configPath := filepath.Join(root, "workspace.yml")
	cfg, err := devconfig.LoadConfig(configPath)
	require.NoError(t, err)
	return (&exportsValidator{}).Run(validate.Context{
		ProjectRoot: root,
		ConfigPath:  configPath,
		Cfg:         cfg,
	})
}

// TestExportsValidator_FromShapes pins the motivating case (a typo in from:
// renders NAME= today with no diagnostic anywhere) and the three hint shapes.
func TestExportsValidator_FromShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rule    string
		wantHin string
	}{
		{
			name: "no default renders empty",
			rule: `    - name: DB_PASSWORD
      from: vars.db.passwrod
`,
			wantHin: "renders empty",
		},
		{
			name: "with default the default always wins",
			rule: `    - name: DB_PASSWORD
      from: vars.db.passwrod
      default: secret
`,
			wantHin: "the default is always used",
		},
		{
			name: "required fails the render",
			rule: `    - name: DB_PASSWORD
      from: vars.db.passwrod
      required: true
`,
			wantHin: "`dwe render env` fails on it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeExportsProject(t, `project:
  name: test
vars:
  db:
    password: s3cret
exports:
  env:
`+tt.rule)
			diags := runExportsValidator(t, root)
			require.Len(t, diags, 1)
			d := diags[0]
			require.Equal(t, validate.SeverityWarning, d.Severity)
			require.Equal(t, "config", d.Domain)
			require.Equal(t, "config.exports", d.Target)
			require.Equal(t, "workspace.yml", d.File)
			require.Equal(t, `exports.env[DB_PASSWORD]: from "vars.db.passwrod" does not resolve in the merged config`, d.Message)
			require.Contains(t, d.Hint, tt.wantHin)
		})
	}
}

func TestExportsValidator_ResolvingRulesAreSilent(t *testing.T) {
	t.Parallel()
	root := writeExportsProject(t, `project:
  name: test
vars:
  db:
    password: s3cret
    empty:
  features:
    metrics: true
exports:
  env:
    - name: DB_PASSWORD
      from: vars.db.password
    - name: METRICS
      from: vars.features.metrics
      when: vars.features.metrics
      format: bool
    - name: LITERAL
      default: fixed
`)
	require.Empty(t, runExportsValidator(t, root))
}

// A key that is present but holds nil is not a finding: the path exists, so an
// empty render is what the author declared.
func TestExportsValidator_NilValuedKeyIsSilent(t *testing.T) {
	t.Parallel()
	root := writeExportsProject(t, `project:
  name: test
vars:
  db:
    password:
exports:
  env:
    - name: DB_PASSWORD
      from: vars.db.password
`)
	require.Empty(t, runExportsValidator(t, root))
}

func TestExportsValidator_UnresolvableWhen(t *testing.T) {
	t.Parallel()
	root := writeExportsProject(t, `project:
  name: test
vars:
  features:
    metrics: true
exports:
  env:
    - name: METRICS
      from: vars.features.metrics
      when: vars.features.metrcis
`)
	diags := runExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityWarning, diags[0].Severity)
	require.Equal(t,
		`exports.env[METRICS]: when "vars.features.metrcis" does not resolve in the merged config`,
		diags[0].Message,
	)
	require.Contains(t, diags[0].Hint, "always skipped")
}

// A rule declared in local.yml is reported at local.yml, not at workspace.yml.
func TestExportsValidator_FileFollowsDeclaringLayer(t *testing.T) {
	t.Parallel()
	root := writeExportsProject(t, `project:
  name: test
vars:
  db:
    password: s3cret
`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "local.yml"), []byte(`exports:
  env:
    - name: DB_PASSWORD
      from: vars.db.passwrod
`), 0o644))

	diags := runExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Equal(t, filepath.Join("workspace", "local.yml"), diags[0].File)
}

// An empty ConfigPath (the preflight/menu Context shape) falls back to
// workspace.yml under the project root.
func TestExportsValidator_EmptyConfigPathFallsBackToWorkspaceYml(t *testing.T) {
	t.Parallel()
	root := writeExportsProject(t, `project:
  name: test
exports:
  env:
    - name: DB_PASSWORD
      from: vars.db.passwrod
`)
	cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	require.NoError(t, err)

	diags := (&exportsValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: cfg})
	require.Len(t, diags, 1)
	require.Equal(t, "workspace.yml", diags[0].File)
}

func TestExportsValidator_NilConfigIsSilent(t *testing.T) {
	t.Parallel()
	require.Empty(t, (&exportsValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()}))
}

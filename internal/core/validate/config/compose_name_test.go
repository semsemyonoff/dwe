package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// writeComposeNameProject scaffolds a project whose workspace.yml declares a
// base compose file, writes that compose file with the given body, and
// optionally a workspace/docker.yml. It returns the project root.
func writeComposeNameProject(t *testing.T, workspaceYML, composeBody, dockerYML string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(composeBody), 0o644))
	if dockerYML != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "docker.yml"), []byte(dockerYML), 0o644))
	}
	return root
}

// writeComposeNameProjectExtra is like writeComposeNameProject but also wires a
// project-wide compose overlay via workspace/local.yml (`compose.extra`), so the
// `-f` chain is [docker-compose.yml, override.yml] — exercising compose's
// last-`-f`-wins top-level `name:` precedence.
func writeComposeNameProjectExtra(t *testing.T, baseBody, extraBody string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(composeNameWorkspaceYML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(baseBody), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "override.yml"), []byte(extraBody), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	localYML := "compose:\n  extra:\n    - override.yml\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "local.yml"), []byte(localYML), 0o644))
	return root
}

func runComposeNameValidator(t *testing.T, root string) []validate.Diagnostic {
	t.Helper()
	cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	require.NoError(t, err)
	return (&composeProjectNameValidator{}).Run(validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
		Cfg:         cfg,
	})
}

const composeNameWorkspaceYML = `schema_version: "2"
project:
  name: shop
  prefix: dwe
compose:
  base: docker-compose.yml
`

// TestComposeProjectNameValidator covers name-divergence detection, docker.yml
// precedence, interpolation skips, and compose multi-file last-`-f`-wins
// semantics. Resolved project name is always "dwe-shop" (project.name=shop,
// prefix=dwe) unless a case sets dockerYML. A case with extra != "" wires a
// project-wide overlay (override.yml) after the base via local.yml.
func TestComposeProjectNameValidator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		base       string // docker-compose.yml body
		extra      string // override.yml body; "" → single-file project
		dockerYML  string // workspace/docker.yml; "" → none (mutually exclusive with extra)
		wantWarn   bool
		wantFile   string   // expected Diagnostic.File when wantWarn
		wantMsgHas []string // substrings expected in the warning message
	}{
		{
			name: "divergent name warns", base: "name: legacy_shop\nservices: {}\n",
			wantWarn: true, wantFile: "docker-compose.yml",
			wantMsgHas: []string{"legacy_shop", "dwe-shop"},
		},
		{name: "matching name is silent", base: "name: dwe-shop\nservices: {}\n"},
		{name: "no top-level name is silent", base: "services: {}\n"},
		{name: "interpolated name skipped", base: "name: ${COMPOSE_PROJECT_NAME}\nservices: {}\n"},
		{
			name: "docker.yml project_name wins",
			base: "name: dwe-shop\nservices: {}\n", dockerYML: "project_name: prod_shop\n",
			wantWarn: true, wantFile: "docker-compose.yml",
			wantMsgHas: []string{"dwe-shop", "prod_shop"},
		},
		{
			// Later -f corrects the base name → effective name aligns → no warn.
			name: "later file overrides name (no warn)",
			base: "name: legacy\nservices: {}\n", extra: "name: dwe-shop\nservices: {}\n",
		},
		{
			// Effective name is the overlay's; warning points at the overlay.
			name: "later file diverges (warn on overlay)",
			base: "name: dwe-shop\nservices: {}\n", extra: "name: other\nservices: {}\n",
			wantWarn: true, wantFile: "override.yml", wantMsgHas: []string{"other"},
		},
		{
			// Overlay declares no name → earlier divergent name still effective.
			name: "later file without name keeps earlier warning",
			base: "name: legacy\nservices: {}\n", extra: "services: {}\n",
			wantWarn: true, wantFile: "docker-compose.yml", wantMsgHas: []string{"legacy"},
		},
		{
			// Overlay's interpolated name wins the override but is unresolvable.
			name: "later interpolated name silent",
			base: "name: legacy\nservices: {}\n", extra: "name: ${COMPOSE_PROJECT_NAME}\nservices: {}\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var root string
			if tc.extra != "" {
				root = writeComposeNameProjectExtra(t, tc.base, tc.extra)
			} else {
				root = writeComposeNameProject(t, composeNameWorkspaceYML, tc.base, tc.dockerYML)
			}
			diags := runComposeNameValidator(t, root)
			if !tc.wantWarn {
				require.Empty(t, diags)
				return
			}
			d := hasDiag(t, diags, validate.SeverityWarning, "silently overridden")
			require.Equal(t, "config.compose_project_name", d.Target)
			require.Equal(t, tc.wantFile, d.File)
			for _, s := range tc.wantMsgHas {
				require.Contains(t, d.Message, s)
			}
		})
	}
}

func TestComposeProjectNameValidator_NilCfgIsSilent(t *testing.T) {
	t.Parallel()
	diags := (&composeProjectNameValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags)
}

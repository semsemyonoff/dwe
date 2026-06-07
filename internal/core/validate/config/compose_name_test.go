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

func TestComposeProjectNameValidator_DivergentNameWarns(t *testing.T) {
	t.Parallel()
	// Resolved name is "dwe-shop"; the compose file declares "legacy_shop".
	root := writeComposeNameProject(t, composeNameWorkspaceYML, "name: legacy_shop\nservices: {}\n", "")
	diags := runComposeNameValidator(t, root)

	d := hasDiag(t, diags, validate.SeverityWarning, "silently overridden")
	require.Contains(t, d.Message, "legacy_shop")
	require.Contains(t, d.Message, "dwe-shop")
	require.Equal(t, "config.compose_project_name", d.Target)
	require.Equal(t, "docker-compose.yml", d.File)
}

func TestComposeProjectNameValidator_MatchingNameIsSilent(t *testing.T) {
	t.Parallel()
	root := writeComposeNameProject(t, composeNameWorkspaceYML, "name: dwe-shop\nservices: {}\n", "")
	diags := runComposeNameValidator(t, root)
	require.Empty(t, diags)
}

func TestComposeProjectNameValidator_NoTopLevelNameIsSilent(t *testing.T) {
	t.Parallel()
	root := writeComposeNameProject(t, composeNameWorkspaceYML, "services: {}\n", "")
	diags := runComposeNameValidator(t, root)
	require.Empty(t, diags)
}

func TestComposeProjectNameValidator_InterpolatedNameSkipped(t *testing.T) {
	t.Parallel()
	root := writeComposeNameProject(t, composeNameWorkspaceYML, "name: ${COMPOSE_PROJECT_NAME}\nservices: {}\n", "")
	diags := runComposeNameValidator(t, root)
	require.Empty(t, diags)
}

func TestComposeProjectNameValidator_DockerYMLProjectNameWins(t *testing.T) {
	t.Parallel()
	// docker.yml project_name overrides FullName(); the compose name: must match it.
	root := writeComposeNameProject(t,
		composeNameWorkspaceYML,
		"name: dwe-shop\nservices: {}\n",
		"project_name: prod_shop\n",
	)
	diags := runComposeNameValidator(t, root)
	d := hasDiag(t, diags, validate.SeverityWarning, "silently overridden")
	require.Contains(t, d.Message, "dwe-shop")  // declared
	require.Contains(t, d.Message, "prod_shop") // resolved via docker.yml
}

func TestComposeProjectNameValidator_NilCfgIsSilent(t *testing.T) {
	t.Parallel()
	diags := (&composeProjectNameValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags)
}

// Multi-file last-wins: the later `-f` file's top-level name: is what compose
// would use, so a divergent base name corrected by a later overlay must NOT warn.
func TestComposeProjectNameValidator_LaterFileOverridesNameNoWarn(t *testing.T) {
	t.Parallel()
	// base declares "legacy", overlay corrects to the resolved "dwe-shop".
	root := writeComposeNameProjectExtra(t, "name: legacy\nservices: {}\n", "name: dwe-shop\nservices: {}\n")
	diags := runComposeNameValidator(t, root)
	require.Empty(t, diags)
}

// The warning points at the LAST file that declares a divergent name.
func TestComposeProjectNameValidator_LaterFileDivergesWarnsOnOverlay(t *testing.T) {
	t.Parallel()
	// base matches, overlay diverges → effective name is the overlay's.
	root := writeComposeNameProjectExtra(t, "name: dwe-shop\nservices: {}\n", "name: other\nservices: {}\n")
	diags := runComposeNameValidator(t, root)
	d := hasDiag(t, diags, validate.SeverityWarning, "silently overridden")
	require.Contains(t, d.Message, "other")
	require.Equal(t, "override.yml", d.File)
}

// A later file without a top-level name: does not override the earlier name, so
// the earlier divergent name still warns (pointing at the file that declared it).
func TestComposeProjectNameValidator_LaterFileNoNameKeepsEarlierWarning(t *testing.T) {
	t.Parallel()
	root := writeComposeNameProjectExtra(t, "name: legacy\nservices: {}\n", "services: {}\n")
	diags := runComposeNameValidator(t, root)
	d := hasDiag(t, diags, validate.SeverityWarning, "silently overridden")
	require.Contains(t, d.Message, "legacy")
	require.Equal(t, "docker-compose.yml", d.File)
}

// A later file with an interpolated name overrides the effective name but is
// unresolvable, so we cannot prove divergence → stay silent.
func TestComposeProjectNameValidator_LaterInterpolatedNameSilent(t *testing.T) {
	t.Parallel()
	root := writeComposeNameProjectExtra(t, "name: legacy\nservices: {}\n", "name: ${COMPOSE_PROJECT_NAME}\nservices: {}\n")
	diags := runComposeNameValidator(t, root)
	require.Empty(t, diags)
}

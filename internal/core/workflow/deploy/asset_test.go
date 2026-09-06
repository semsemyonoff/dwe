package deploy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
)

// TestDefaultDeployYAML_RoundTrip is the pin that lets an embedded asset stand
// in for a marshaller: the asset, read back through the REAL strict loader
// (yamlstrict + DeployStep.UnmarshalYAML + validatePhaseSteps), must reproduce
// DefaultDeployConfig() exactly. Edit either side without the other and this
// fails.
func TestDefaultDeployYAML_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy.yml")
	require.NoError(t, os.WriteFile(path, deploy.DefaultDeployYAML(), 0o644))

	loaded, err := config.LoadProjectDeployConfig(path)
	require.NoError(t, err, "the emitted asset must load through the strict deploy.yml loader")
	require.Equal(t, deploy.DefaultDeployConfig(), loaded)
}

// The asset declares log: true explicitly rather than relying on
// LoadProjectDeployConfig's defaultLog, so an ejected file behaves the same
// whichever loader reads it.
func TestDefaultDeployYAML_DeclaresLogExplicitly(t *testing.T) {
	require.Contains(t, string(deploy.DefaultDeployYAML()), "\nlog: true\n")
}

// The emitted document is meant to be edited by a human, so its comments are
// part of the payload — a regression that reduced the asset to marshalled
// output would drop them silently.
func TestDefaultDeployYAML_CarriesComments(t *testing.T) {
	doc := string(deploy.DefaultDeployYAML())

	require.True(t, strings.HasPrefix(doc, "# workspace/deploy.yml"),
		"asset must open with its header comment, got: %.60q", doc)
	require.Contains(t, doc, "dwe deploy eject", "header must name the command that emits it")
	require.Contains(t, doc, "full replacement", "header must warn that an active file replaces the whole pipeline")

	// A comment on each phase, not just the file header.
	for _, phase := range deploy.DefaultDeployConfig().Phases {
		idx := strings.Index(doc, "- name: "+phase.Name)
		require.NotEqual(t, -1, idx, "phase %q missing from asset", phase.Name)
		require.Contains(t, doc[:idx], "  #", "phase %q has no explanatory comment above it", phase.Name)
	}
}

// A fresh copy per call: the accessor hands out bytes over an embedded string,
// and a caller writing through them must not corrupt the next caller's asset.
func TestDefaultDeployYAML_FreshCopy(t *testing.T) {
	a := deploy.DefaultDeployYAML()
	require.NotEmpty(t, a)
	a[0] = 'X'
	require.NotEqual(t, a, deploy.DefaultDeployYAML())
}

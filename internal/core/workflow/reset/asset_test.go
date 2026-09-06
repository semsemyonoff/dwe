package reset_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"
)

// TestDefaultResetYAML_RoundTrip is the pin that lets an embedded asset stand in
// for a marshaller: the asset, read back through the REAL strict loader
// (yamlstrict + DeployStep.UnmarshalYAML + validatePhaseSteps), must reproduce
// DefaultResetConfig() exactly. Edit either side without the other and this
// fails.
func TestDefaultResetYAML_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset.yml")
	require.NoError(t, os.WriteFile(path, reset.DefaultResetYAML(), 0o644))

	loaded, err := config.LoadResetConfig(path)
	require.NoError(t, err, "the emitted asset must load through the strict reset.yml loader")

	// The one asymmetry, normalised explicitly rather than simplified away:
	// DefaultResetConfig() leaves Log nil, while LoadResetConfig fills an absent
	// (and here, an explicit `log: false`) key with &false. Both describe the
	// same behaviour; only the in-memory shape differs. Do not "fix" this by
	// dropping log: from the asset — see TestDefaultResetYAML_DeclaresLogExplicitly.
	want := reset.DefaultResetConfig()
	require.Nil(t, want.Log, "constructor is expected to leave Log nil; if that changed, drop this normalisation")
	logOff := false
	want.Log = &logOff

	require.Equal(t, want, loaded)
}

// The asset declares log: false explicitly rather than relying on
// LoadResetConfig's defaultLog, so an ejected file behaves the same whichever
// loader reads it — loadProjectDeployConfigDecode applies defaultLog only when
// the key is absent, and deploy's loader defaults it the other way.
func TestDefaultResetYAML_DeclaresLogExplicitly(t *testing.T) {
	require.Contains(t, string(reset.DefaultResetYAML()), "\nlog: false\n")
}

// The emitted document is meant to be edited by a human, so its comments are
// part of the payload — a regression that reduced the asset to marshalled
// output would drop them silently.
func TestDefaultResetYAML_CarriesComments(t *testing.T) {
	doc := string(reset.DefaultResetYAML())

	require.True(t, strings.HasPrefix(doc, "# workspace/reset.yml"),
		"asset must open with its header comment, got: %.60q", doc)
	require.Contains(t, doc, "dwe reset eject", "header must name the command that emits it")
	require.Contains(t, doc, "full replacement", "header must warn that an active file replaces the whole pipeline")

	// The explanation of why docker_remove_project_volumes carries no
	// continue_on_error is exactly what an author editing the ejected file
	// needs; it lives in defaults.go and must survive into the asset.
	require.Contains(t, doc, "continue_on_error", "the remove-volumes rationale must survive into the asset")

	// A comment on each phase, not just the file header — so the window under
	// test is the text BETWEEN the previous phase and this one. Asserting on
	// doc[:idx] would pass for every phase after the first as soon as the first
	// one has a comment.
	prev := 0
	for _, phase := range reset.DefaultResetConfig().Phases {
		idx := strings.Index(doc, "- name: "+phase.Name)
		require.NotEqual(t, -1, idx, "phase %q missing from asset", phase.Name)
		require.Greater(t, idx, prev, "phases must appear in DefaultResetConfig order")
		require.Contains(t, doc[prev:idx], "  #", "phase %q has no explanatory comment above it", phase.Name)
		prev = idx
	}
}

// A fresh copy per call: the accessor hands out bytes over an embedded string,
// and a caller writing through them must not corrupt the next caller's asset.
func TestDefaultResetYAML_FreshCopy(t *testing.T) {
	a := reset.DefaultResetYAML()
	require.NotEmpty(t, a)
	a[0] = 'X'
	require.NotEqual(t, a, reset.DefaultResetYAML())
}

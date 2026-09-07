package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// The ide/ai/git dry-runs must exercise the same data `dwe render <kind>`
// produces: those outputs are git-tracked, so their templates see the
// ENC[age:…] marker, never the decrypted value.
func TestSanitizedCfg_carriesMarkerEvenWithIdentity(t *testing.T) {
	root := t.TempDir()

	id, err := secrets.Keygen()
	require.NoError(t, err)
	t.Setenv(secrets.EnvKey, id.Export())
	t.Setenv(secrets.EnvKeyFile, "")

	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	require.NoError(t, err)

	configPath := filepath.Join(root, "workspace.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(
		"schema_version: \"2\"\nproject:\n  name: test-project\nsecrets:\n  recipient: "+id.Recipient()+
			"\nvars:\n  token: "+marker+"\n"), 0o644))

	// The decrypted config the validate command hands over.
	loaded, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "s3cr3t-value", loaded.Vars["token"])

	got := sanitizedCfg(validate.Context{ConfigPath: configPath, Cfg: loaded})
	require.Equal(t, marker, got.Vars["token"])
	raw, ok := got.Raw["vars"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, marker, raw["token"])
}

func TestSanitizedCfg_fallsBackToContextConfig(t *testing.T) {
	loaded := &config.DweConfig{Vars: map[string]any{"token": "plain"}}

	// No config path at all (scoped runs may not carry one).
	require.Same(t, loaded, sanitizedCfg(validate.Context{Cfg: loaded}))

	// An unloadable path is the caller's problem, not a template diagnostic.
	root := t.TempDir()
	bad := filepath.Join(root, "workspace.yml")
	require.NoError(t, os.WriteFile(bad, []byte("project: [not-a-mapping\n"), 0o644))
	require.Same(t, loaded, sanitizedCfg(validate.Context{ConfigPath: bad, Cfg: loaded}))
}

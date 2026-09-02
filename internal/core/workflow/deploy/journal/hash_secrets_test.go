package journal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/stretchr/testify/require"
)

// loadWithSecret writes a one-layer project whose vars.token is the given
// plaintext encrypted to recipient, then loads it through the real loader.
func loadWithSecret(t *testing.T, plaintext, recipient string) *config.DweConfig {
	t.Helper()
	marker, err := secrets.Encrypt(plaintext, recipient)
	require.NoError(t, err)

	dir := t.TempDir()
	wsPath := filepath.Join(dir, "workspace.yml")
	body := "project:\n  name: demo\nsecrets:\n  recipient: " + recipient + "\nvars:\n  token: " + marker + "\n"
	require.NoError(t, os.WriteFile(wsPath, []byte(body), 0o644))

	cfg, err := config.LoadConfig(wsPath)
	require.NoError(t, err)
	require.Equal(t, plaintext, cfg.Vars["token"], "the loader must have decrypted the marker")
	return cfg
}

// TestConfigHashes_seePlaintextNotCiphertext pins decision 6: age output is
// non-deterministic, so hashing the ciphertext would invalidate `already
// up-to-date` on every re-encrypt of an unchanged value. Both hash functions
// canonicalMap the vars block, so both are pinned here.
func TestConfigHashes_seePlaintextNotCiphertext(t *testing.T) {
	id, err := secrets.Keygen()
	require.NoError(t, err)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv(secrets.EnvKey, id.Export())
	r := id.Recipient()

	// Two independent encryptions of the SAME plaintext: different ciphertext,
	// same secret.
	first := loadWithSecret(t, "same-plaintext", r)
	second := loadWithSecret(t, "same-plaintext", r)
	changed := loadWithSecret(t, "different-plaintext", r)

	svcCfg := config.ServiceConfig{Container: "app"}
	tracked := []string{}

	t.Run("ServiceConfigHash", func(t *testing.T) {
		h1 := ServiceConfigHash(svcCfg, nil, first.Vars)
		h2 := ServiceConfigHash(svcCfg, nil, second.Vars)
		h3 := ServiceConfigHash(svcCfg, nil, changed.Vars)
		require.Equal(t, h1, h2, "re-encrypting the same plaintext must not change the hash")
		require.NotEqual(t, h1, h3, "changing the plaintext must change the hash")
	})

	t.Run("ProjectConfigHash", func(t *testing.T) {
		h1 := ProjectConfigHash(first, nil, nil, tracked)
		h2 := ProjectConfigHash(second, nil, nil, tracked)
		h3 := ProjectConfigHash(changed, nil, nil, tracked)
		require.Equal(t, h1, h2, "re-encrypting the same plaintext must not change the hash")
		require.NotEqual(t, h1, h3, "changing the plaintext must change the hash")
	})
}

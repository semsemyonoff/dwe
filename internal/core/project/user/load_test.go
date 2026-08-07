package user

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// withUserConfigDir points $HOME at a temp dir so Load reads its
// global config from <tempdir>/.config/dwe/config. Returns the
// <tempdir>/.config path so callers can write into the dwe/ subdir.
func withUserConfigDir(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	homePrev, homeWasSet := os.LookupEnv("HOME")
	require.NoError(t, os.Setenv("HOME", dir))
	cfgDir := filepath.Join(dir, ".config")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))

	cleanup := func() {
		if homeWasSet {
			_ = os.Setenv("HOME", homePrev)
		} else {
			_ = os.Unsetenv("HOME")
		}
	}
	return cfgDir, cleanup
}

// clearNotifyEnv unsets every DWE_NOTIFY_* and DWE_LANGUAGE env var
// so tests are hermetic. Returns a restore func.
func clearNotifyEnv(t *testing.T) func() {
	t.Helper()
	keys := []string{
		envNotifyEnabled, envNotifyRunEnabled, envNotifyDeployEnabled,
		envNotifyCommandsEnabled, envNotifyChannels, envLanguage,
	}
	prev := make(map[string]string)
	wasSet := make(map[string]bool)
	for _, k := range keys {
		v, ok := os.LookupEnv(k)
		prev[k], wasSet[k] = v, ok
		_ = os.Unsetenv(k)
	}
	return func() {
		for _, k := range keys {
			if wasSet[k] {
				_ = os.Setenv(k, prev[k])
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
}

func TestDefaults(t *testing.T) {
	c := Defaults()
	assert.True(t, c.NotifyEnabled)
	assert.True(t, c.NotifyRunEnabled)
	assert.True(t, c.NotifyDeployEnabled)
	assert.True(t, c.NotifyCommandsEnabled)
	assert.Equal(t, []string{"native"}, c.NotifyChannels)
}

func TestLoad_AllMissing(t *testing.T) {
	defer clearNotifyEnv(t)()
	_, cleanup := withUserConfigDir(t)
	defer cleanup()
	projectRoot := t.TempDir()
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.True(t, cfg.NotifyEnabled)
	assert.Equal(t, []string{"native"}, cfg.NotifyChannels)
}

func TestLoad_GlobalOnly(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "dwe", "config"), "notify_run_enabled = false\n")
	projectRoot := t.TempDir()
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.False(t, cfg.NotifyRunEnabled)
	assert.True(t, cfg.NotifyDeployEnabled)
}

func TestLoad_ProjectOnly(t *testing.T) {
	defer clearNotifyEnv(t)()
	_, cleanup := withUserConfigDir(t)
	defer cleanup()
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".dwe", "config"), "notify_deploy_enabled = false\n")
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.False(t, cfg.NotifyDeployEnabled)
	assert.True(t, cfg.NotifyRunEnabled)
}

func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "dwe", "config"), "notify_run_enabled = false\n")
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".dwe", "config"), "notify_run_enabled = true\n")
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.True(t, cfg.NotifyRunEnabled)
}

func TestLoad_EnvOverridesAll(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "dwe", "config"), "notify_enabled = true\n")
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".dwe", "config"), "notify_enabled = true\n")
	require.NoError(t, os.Setenv(envNotifyEnabled, "false"))
	require.NoError(t, os.Setenv(envNotifyChannels, "native,telegram"))
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.False(t, cfg.NotifyEnabled)
	assert.Equal(t, []string{"native", "telegram"}, cfg.NotifyChannels)
}

func TestLoad_GlobalParseErrorBubbles(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "dwe", "config"), "notify_enabled = nope\n")
	_, err := Load(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid boolean")
}

func TestLoad_ProjectParseErrorBubbles(t *testing.T) {
	defer clearNotifyEnv(t)()
	_, cleanup := withUserConfigDir(t)
	defer cleanup()
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".dwe", "config"), "notify.enabled = true\n")
	_, err := Load(projectRoot)
	require.Error(t, err)
}

func TestLoad_EnvInvalidBoolean(t *testing.T) {
	defer clearNotifyEnv(t)()
	_, cleanup := withUserConfigDir(t)
	defer cleanup()
	require.NoError(t, os.Setenv(envNotifyEnabled, "definitely"))
	_, err := Load(t.TempDir())
	require.Error(t, err)
}

func TestLoad_LanguageGlobalOnly(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "dwe", "config"), "language = ru\n")
	projectRoot := t.TempDir()
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.Equal(t, "ru", cfg.Language)
}

func TestLoad_LanguageProjectOnly(t *testing.T) {
	defer clearNotifyEnv(t)()
	_, cleanup := withUserConfigDir(t)
	defer cleanup()
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".dwe", "config"), "language = de\n")
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.Equal(t, "de", cfg.Language)
}

func TestLoad_LanguageProjectOverridesGlobal(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "dwe", "config"), "language = ru\n")
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".dwe", "config"), "language = de\n")
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.Equal(t, "de", cfg.Language)
}

func TestLoad_LanguageEnvOverridesAll(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "dwe", "config"), "language = ru\n")
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".dwe", "config"), "language = de\n")
	require.NoError(t, os.Setenv(envLanguage, "fr"))
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.Equal(t, "fr", cfg.Language)
}

func TestLoad_LanguageDefaultEmpty(t *testing.T) {
	defer clearNotifyEnv(t)()
	_, cleanup := withUserConfigDir(t)
	defer cleanup()
	cfg, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Language)
}

func TestNotifyEnabledFor(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		kind string
		want bool
	}{
		{"nil receiver", nil, "deploy", false},
		{"master off", &Config{NotifyEnabled: false, NotifyDeployEnabled: true}, "deploy", false},
		{"deploy on", &Config{NotifyEnabled: true, NotifyDeployEnabled: true}, "deploy", true},
		{"deploy off", &Config{NotifyEnabled: true, NotifyDeployEnabled: false}, "deploy", false},
		{"run on", &Config{NotifyEnabled: true, NotifyRunEnabled: true}, "run", true},
		{"command on", &Config{NotifyEnabled: true, NotifyCommandsEnabled: true}, "command", true},
		{"unknown kind", &Config{NotifyEnabled: true, NotifyDeployEnabled: true, NotifyRunEnabled: true, NotifyCommandsEnabled: true}, "bogus", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.cfg.NotifyEnabledFor(tc.kind))
		})
	}
}

package userconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile is a tiny helper.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// withUserConfigDir redirects os.UserConfigDir() at the env level. On
// Linux it reads $XDG_CONFIG_HOME; on macOS it reads $HOME and appends
// "Library/Application Support". We set both for portability across CI
// runners.
func withUserConfigDir(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	// Linux path.
	xdgPrev, xdgWasSet := os.LookupEnv("XDG_CONFIG_HOME")
	homePrev, homeWasSet := os.LookupEnv("HOME")
	// macOS: os.UserConfigDir uses $HOME + "/Library/Application Support".
	// To make HOME-based resolution land in dir, point HOME at a temp
	// dir whose "Library/Application Support" subdir is what we use.
	macHome := filepath.Join(dir, "machome")
	macCfg := filepath.Join(macHome, "Library", "Application Support")
	require.NoError(t, os.MkdirAll(macCfg, 0o755))
	require.NoError(t, os.Setenv("HOME", macHome))
	require.NoError(t, os.Setenv("XDG_CONFIG_HOME", macCfg))

	cleanup := func() {
		if xdgWasSet {
			_ = os.Setenv("XDG_CONFIG_HOME", xdgPrev)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		if homeWasSet {
			_ = os.Setenv("HOME", homePrev)
		} else {
			_ = os.Unsetenv("HOME")
		}
	}
	return macCfg, cleanup
}

// clearNotifyEnv unsets every DEVBOX_NOTIFY_* env var so tests are
// hermetic. Returns a restore func.
func clearNotifyEnv(t *testing.T) func() {
	t.Helper()
	keys := []string{
		envNotifyEnabled, envNotifyRunEnabled, envNotifyDeployEnabled,
		envNotifyCommandsEnabled, envNotifyChannels,
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
	writeFile(t, filepath.Join(cfgDir, "devbox", "config"), "notify_run_enabled = false\n")
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
	writeFile(t, filepath.Join(projectRoot, ".devbox", "config"), "notify_deploy_enabled = false\n")
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.False(t, cfg.NotifyDeployEnabled)
	assert.True(t, cfg.NotifyRunEnabled)
}

func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "devbox", "config"), "notify_run_enabled = false\n")
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".devbox", "config"), "notify_run_enabled = true\n")
	cfg, err := Load(projectRoot)
	require.NoError(t, err)
	assert.True(t, cfg.NotifyRunEnabled)
}

func TestLoad_EnvOverridesAll(t *testing.T) {
	defer clearNotifyEnv(t)()
	cfgDir, cleanup := withUserConfigDir(t)
	defer cleanup()
	writeFile(t, filepath.Join(cfgDir, "devbox", "config"), "notify_enabled = true\n")
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".devbox", "config"), "notify_enabled = true\n")
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
	writeFile(t, filepath.Join(cfgDir, "devbox", "config"), "notify_enabled = nope\n")
	_, err := Load(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid boolean")
}

func TestLoad_ProjectParseErrorBubbles(t *testing.T) {
	defer clearNotifyEnv(t)()
	_, cleanup := withUserConfigDir(t)
	defer cleanup()
	projectRoot := t.TempDir()
	writeFile(t, filepath.Join(projectRoot, ".devbox", "config"), "notify.enabled = true\n")
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

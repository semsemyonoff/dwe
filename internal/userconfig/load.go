package userconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Env var names recognised by Load. Each maps to the same-named flat key
// without the DEVBOX_ prefix.
const (
	envNotifyEnabled         = "DEVBOX_NOTIFY_ENABLED"
	envNotifyRunEnabled      = "DEVBOX_NOTIFY_RUN_ENABLED"
	envNotifyDeployEnabled   = "DEVBOX_NOTIFY_DEPLOY_ENABLED"
	envNotifyCommandsEnabled = "DEVBOX_NOTIFY_COMMANDS_ENABLED"
	envNotifyChannels        = "DEVBOX_NOTIFY_CHANNELS"
)

// Load resolves the effective Config by applying:
//  1. embedded defaults
//  2. global file at <os.UserConfigDir()>/devbox/config (missing → skip)
//  3. project file at <projectRoot>/.devbox/config (missing → skip)
//  4. environment variables (highest precedence)
//
// Missing files are silently skipped. Parse errors and os.UserConfigDir
// resolution errors bubble up.
func Load(projectRoot string) (*Config, error) {
	cfg := Defaults()

	globalDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("userconfig: resolve global config dir: %w", err)
	}
	globalPath := filepath.Join(globalDir, "devbox", "config")
	if err := loadFile(globalPath, cfg); err != nil {
		return nil, err
	}

	if projectRoot != "" {
		projectPath := filepath.Join(projectRoot, ".devbox", "config")
		if err := loadFile(projectPath, cfg); err != nil {
			return nil, err
		}
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadFile(path string, cfg *Config) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("userconfig: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := parse(f, cfg); err != nil {
		return fmt.Errorf("userconfig: parsing %s: %w", path, err)
	}
	return nil
}

func applyEnv(cfg *Config) error {
	type boolKey struct {
		env string
		set func(bool)
	}
	bools := []boolKey{
		{envNotifyEnabled, func(b bool) { cfg.NotifyEnabled = b }},
		{envNotifyRunEnabled, func(b bool) { cfg.NotifyRunEnabled = b }},
		{envNotifyDeployEnabled, func(b bool) { cfg.NotifyDeployEnabled = b }},
		{envNotifyCommandsEnabled, func(b bool) { cfg.NotifyCommandsEnabled = b }},
	}
	for _, b := range bools {
		v, ok := os.LookupEnv(b.env)
		if !ok {
			continue
		}
		parsed, err := parseBool(strings.TrimSpace(v), 0, b.env)
		if err != nil {
			return fmt.Errorf("userconfig: env %s: invalid boolean %q", b.env, v)
		}
		b.set(parsed)
	}
	if v, ok := os.LookupEnv(envNotifyChannels); ok {
		cfg.NotifyChannels = parseList(v)
	}
	return nil
}

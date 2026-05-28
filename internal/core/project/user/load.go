package user

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
	envLanguage              = "DEVBOX_LANGUAGE"
	envMermaidTheme          = "DEVBOX_MERMAID_THEME"
)

// Load resolves the effective Config by applying:
//  1. embedded defaults
//  2. global file at ~/.config/devbox/config (missing → skip)
//  3. project file at <projectRoot>/.devbox/config (missing → skip)
//  4. environment variables (highest precedence)
//
// Missing files are silently skipped. Parse errors and home-dir
// resolution errors bubble up. The global path is identical across
// platforms — no platform-native location, no XDG fallback.
func Load(projectRoot string) (*Config, error) {
	cfg := Defaults()

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("userconfig: resolve home dir: %w", err)
	}
	globalPath := filepath.Join(home, ".config", "devbox", "config")
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
		parsed, err := parseBoolEnv(strings.TrimSpace(v), b.env)
		if err != nil {
			return err
		}
		b.set(parsed)
	}
	if v, ok := os.LookupEnv(envNotifyChannels); ok {
		cfg.NotifyChannels = parseList(v)
	}
	if v, ok := os.LookupEnv(envLanguage); ok {
		cfg.Language = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv(envMermaidTheme); ok {
		t, valid := normalizeMermaidTheme(v)
		if !valid {
			return fmt.Errorf("userconfig: invalid %s %q (want auto|dark|light)", envMermaidTheme, v)
		}
		cfg.MermaidTheme = t
	}
	return nil
}

// Package userconfig loads user-level Devbox preferences from a flat
// key=value file. It merges defaults, a global config at
// ~/.config/devbox/config (same path on every OS), an optional
// per-project override at .devbox/config, and environment variables.
package userconfig

import (
	"strings"
)

// Config holds user-level Devbox preferences. The MVP only covers the
// notification subsystem; reserved fields are decoded but not exposed yet
// so adding future channels does not require a schema migration.
type Config struct {
	NotifyEnabled         bool
	NotifyRunEnabled      bool
	NotifyDeployEnabled   bool
	NotifyCommandsEnabled bool
	NotifyChannels        []string

	// Reserved for future channels. Parsed but not yet wired.
	notifyTelegramToken string
	notifyTelegramChat  string
	notifyWebhookURLs   []string

	// Language holds the user's preferred language code. Empty string means
	// unset; locale resolution will fall through to $LANG / "en".
	Language string

	// MermaidTheme overrides the theme used when rendering mermaid diagrams
	// in the `devbox docs` TUI. Valid values: "auto" (default — follow the
	// terminal background), "dark", "light".
	MermaidTheme string

	// Binaries maps binary names to their absolute paths. Populated from
	// binary_<name>=<path> lines in the config file. Used by linter validators
	// to override default binary paths.
	Binaries map[string]string
}

// Defaults returns a Config initialised to the documented defaults.
// All four Notify*Enabled flags ship true so flipping notify_enabled to
// false cleanly mutes everything (master-switch behavior).
func Defaults() *Config {
	return &Config{
		NotifyEnabled:         true,
		NotifyRunEnabled:      true,
		NotifyDeployEnabled:   true,
		NotifyCommandsEnabled: true,
		NotifyChannels:        []string{"native"},
		MermaidTheme:          "auto",
		Binaries:              make(map[string]string),
	}
}

// NotifyEnabledFor reports whether notifications should fire for the
// given operation kind. The string-keyed form keeps the notify package
// from import-cycling back to userconfig.
//
// Recognised kinds: "deploy", "run", "command". Anything else returns
// false (defensive).
func (c *Config) NotifyEnabledFor(kind string) bool {
	if c == nil || !c.NotifyEnabled {
		return false
	}
	switch kind {
	case "deploy":
		return c.NotifyDeployEnabled
	case "run":
		return c.NotifyRunEnabled
	case "command":
		return c.NotifyCommandsEnabled
	default:
		return false
	}
}

// BinaryOverride returns the user-configured override path for a binary name
// and a boolean indicating whether an override was found. The returned path
// is trimmed of whitespace. If no override is configured, returns empty string
// and false.
func (c *Config) BinaryOverride(name string) (string, bool) {
	if c == nil || c.Binaries == nil {
		return "", false
	}
	path, ok := c.Binaries[name]
	return strings.TrimSpace(path), ok
}

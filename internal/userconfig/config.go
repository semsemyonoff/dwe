// Package userconfig loads user-level Devbox preferences from a flat
// key=value file. It merges defaults, the platform-native global config
// (resolved via os.UserConfigDir()), an optional per-project override at
// .devbox/config, and environment variables.
package userconfig

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

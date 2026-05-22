package userconfig

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

var keyLineRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\s*=\s*.*$`)

// parse reads a flat key=value config from r and merges into cfg in place.
// Empty source merges no keys (defaults survive). Parser errors are
// returned with the "userconfig: " prefix, lowercase, no trailing
// punctuation, so callers can wrap with fmt.Errorf("userconfig: parsing
// %s: %w", path, err).
func parse(r io.Reader, cfg *Config) error {
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Inline comments are not allowed — would conflict with values
		// containing literal '#' characters.
		if idx := strings.Index(trimmed, "#"); idx > 0 {
			return fmt.Errorf("userconfig: inline comments not supported at line %d", line)
		}
		keyRaw, valRaw, hasEq := strings.Cut(trimmed, "=")
		key := strings.TrimSpace(keyRaw)
		val := strings.TrimSpace(valRaw)
		if hasEq && strings.Contains(key, ".") {
			return fmt.Errorf("userconfig: dotted keys not allowed at line %d; use _ separators", line)
		}
		if !keyLineRe.MatchString(trimmed) {
			return fmt.Errorf("userconfig: malformed line %d", line)
		}
		if err := apply(cfg, key, val, line); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("userconfig: read: %w", err)
	}
	return nil
}

func apply(cfg *Config, key, val string, line int) error {
	switch key {
	case "notify_enabled":
		b, err := parseBool(val, line, key)
		if err != nil {
			return err
		}
		cfg.NotifyEnabled = b
	case "notify_run_enabled":
		b, err := parseBool(val, line, key)
		if err != nil {
			return err
		}
		cfg.NotifyRunEnabled = b
	case "notify_deploy_enabled":
		b, err := parseBool(val, line, key)
		if err != nil {
			return err
		}
		cfg.NotifyDeployEnabled = b
	case "notify_commands_enabled":
		b, err := parseBool(val, line, key)
		if err != nil {
			return err
		}
		cfg.NotifyCommandsEnabled = b
	case "notify_channels":
		cfg.NotifyChannels = parseList(val)
	case "notify_telegram_token":
		cfg.notifyTelegramToken = val
	case "notify_telegram_chat":
		cfg.notifyTelegramChat = val
	case "notify_webhook_urls":
		cfg.notifyWebhookURLs = parseList(val)
	default:
		// Unknown keys are warnings — forward-compat with future channel
		// keys when an older MVP-only binary reads a richer config.
		slog.Warn("userconfig: unknown key", "key", key, "line", line)
	}
	return nil
}

func parseBool(val string, line int, key string) (bool, error) {
	switch strings.ToLower(val) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("userconfig: invalid boolean %q for key %s at line %d", val, key, line)
	}
}

// parseBoolEnv is parseBool for environment variable sources; it produces a
// clean error without a file-line suffix.
func parseBoolEnv(val, envKey string) (bool, error) {
	switch strings.ToLower(val) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("userconfig: invalid boolean %q for env var %s", val, envKey)
	}
}

func parseList(val string) []string {
	if strings.TrimSpace(val) == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

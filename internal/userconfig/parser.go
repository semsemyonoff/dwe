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
// Empty source merges no keys (defaults survive). Parser errors are returned
// without a package prefix; loadFile wraps them with path context.
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
		keyRaw, valRaw, hasEq := strings.Cut(trimmed, "=")
		key := strings.TrimSpace(keyRaw)
		val := strings.TrimSpace(valRaw)
		// Space-hash in the value portion signals an inline comment attempt.
		// Bare '#' without a preceding space is allowed (e.g. URL fragments
		// like https://example.com#section).
		if hasEq && (strings.Contains(valRaw, " #") || strings.Contains(valRaw, "\t#")) {
			return fmt.Errorf("inline comments not supported at line %d", line)
		}
		if hasEq && strings.Contains(key, ".") {
			return fmt.Errorf("dotted keys not allowed at line %d; use _ separators", line)
		}
		if !keyLineRe.MatchString(trimmed) {
			return fmt.Errorf("malformed line %d", line)
		}
		if err := apply(cfg, key, val, line); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
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
	case "language":
		cfg.Language = val
	case "mermaid_theme":
		t, ok := normalizeMermaidTheme(val)
		if !ok {
			return fmt.Errorf("invalid mermaid_theme %q at line %d (want auto|dark|light)", val, line)
		}
		cfg.MermaidTheme = t
	default:
		// Handle binary_* keys for overriding binary paths (linters, etc.)
		if binName, ok := strings.CutPrefix(key, "binary_"); ok {
			if strings.TrimSpace(val) == "" {
				return fmt.Errorf("invalid binary_%s at line %d: path must not be empty", binName, line)
			}
			cfg.Binaries[binName] = val
			return nil
		}
		// Unknown keys are warnings — forward-compat with future channel
		// keys when an older MVP-only binary reads a richer config.
		slog.Warn("userconfig: unknown key", "key", key, "line", line)
	}
	return nil
}

// normalizeMermaidTheme returns the canonical theme value for "auto", "dark",
// "light" (case-insensitive, whitespace-trimmed). The empty string maps to
// "auto" so both files and env vars can signal "default" the same way. The
// caller composes the source-specific error (file line vs env var name) on
// ok==false; we don't bake "at line N" into the helper because the env
// path has no line number.
func normalizeMermaidTheme(val string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "", "auto":
		return "auto", true
	case "dark":
		return "dark", true
	case "light":
		return "light", true
	default:
		return "", false
	}
}

func parseBool(val string, line int, key string) (bool, error) {
	switch strings.ToLower(val) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q for key %s at line %d", val, key, line)
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

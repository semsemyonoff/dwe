package setup

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// hostnameRegex matches RFC 1123 short hostnames: labels [a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])? separated by dots.
var hostnameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// Preset types for answer validation.
const (
	PresetPort     = "port"
	PresetHostname = "hostname"
	PresetPath     = "path"
	PresetNonEmpty = "non-empty"
)

// Port range constants used for validation across the setup package.
const (
	minPort = 1
	maxPort = 65535
)

// ValidateAndCoerce validates a raw string answer against a question's validate spec
// and returns the properly typed value. Each preset defines both the validation rules
// and the typed return value (e.g., port → int).
func ValidateAndCoerce(q Question, raw string) (any, error) {
	if q.Validate == nil {
		// No validation spec; return raw string unchanged.
		return raw, nil
	}

	if q.Validate.Preset != "" {
		switch q.Validate.Preset {
		case PresetPort:
			return coercePort(raw)
		case PresetHostname:
			return coerceHostname(raw)
		case PresetPath:
			return coercePath(raw)
		case PresetNonEmpty:
			return coerceNonEmpty(raw)
		default:
			// Unknown preset; this should have been caught by the validator.
			// Be defensive and treat it as a string.
			return raw, nil
		}
	}

	if q.Validate.Regex != "" {
		return coerceRegex(q.Validate.Regex, raw)
	}

	// No preset or regex; return raw string.
	return raw, nil
}

// coercePort validates that raw is a valid port number (1..65535) and returns it as int.
func coercePort(raw string) (any, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}
	if port < minPort || port > maxPort {
		return nil, fmt.Errorf("port %d out of range (1..65535)", port)
	}
	return port, nil
}

// coerceHostname validates that raw is a valid RFC 1123 short hostname and returns it as string.
// RFC 1123 short names: labels separated by dots, each label is [a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?
func coerceHostname(raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("hostname cannot be empty")
	}

	if !hostnameRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid hostname: %q", trimmed)
	}

	return trimmed, nil
}

// coercePath validates that raw is a valid path (non-empty, no NUL byte) and returns it as string.
func coercePath(raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("path cannot be empty")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return nil, fmt.Errorf("path cannot contain NUL character")
	}
	return trimmed, nil
}

// coerceNonEmpty validates that raw is non-empty (after trimming) and returns it as string.
func coerceNonEmpty(raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("value cannot be empty")
	}
	return trimmed, nil
}

// coerceRegex validates that raw matches the provided regex pattern and returns it as string.
func coerceRegex(pattern string, raw string) (any, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		// Pattern should have been validated at load time, but be defensive.
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}
	if !re.MatchString(raw) {
		return nil, fmt.Errorf("value %q does not match pattern %q", raw, pattern)
	}
	return raw, nil
}

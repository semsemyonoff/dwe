package spec

import (
	"bufio"
	"strings"
)

// ParseEnvKeys returns a set of KEY names found in env file content.
func ParseEnvKeys(data []byte) map[string]bool { return parseEnvKeys(data) }

func parseEnvKeys(data []byte) map[string]bool {
	keys := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if key := envLineKey(scanner.Text()); key != "" {
			keys[key] = true
		}
	}
	return keys
}

// ParseEnvEntries returns a map of KEY=VALUE pairs found in env file content.
// Values are trimmed and shell-style quoted strings ("..." or '...') have their
// surrounding quotes stripped, mirroring deploy.SourceDotEnv's behavior. A bare
// "KEY=" or quoted-empty value yields an empty-string value.
func ParseEnvEntries(data []byte) map[string]string {
	entries := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val := strings.TrimSpace(value)
		if n := len(val); n >= 2 && val[0] == val[n-1] && (val[0] == '"' || val[0] == '\'') {
			val = val[1 : n-1]
		}
		entries[key] = val
	}
	return entries
}

// EnvLineKey returns the KEY part of a "KEY=VALUE" env line.
// Returns "" for blank lines and comment lines.
func EnvLineKey(line string) string { return envLineKey(line) }

func envLineKey(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	key, _, _ := strings.Cut(line, "=")
	return strings.TrimSpace(key)
}

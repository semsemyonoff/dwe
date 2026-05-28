// Package daemon provides cross-cutting helpers for the declarative `type: daemon`
// command system: container-name resolution, standardised label construction,
// and `docker ps --filter` argv builders.
//
// This package is consumed by `internal/core/execution/builtin`, `internal/core/project/stack` (status),
// `internal/core/validate/commands`, and `internal/cli` (completion + inspect).
// It deliberately has no dependencies on `internal/core/usercommands` or
// `internal/core/project/config` so any caller can import it without dragging in the
// command/runtime trees.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Standard label keys applied to every daemon container.
const (
	LabelProject      = "devbox.project"
	LabelDaemonID     = "devbox.daemon.id"
	LabelDaemonParams = "devbox.daemon.params"
)

// Sentinel errors.
var (
	// ErrContainerNameInvalid is returned by ResolveContainerName when the
	// final container name (after project prefixing) does not match the
	// authoritative regex. This is the security boundary for argv → docker
	// `--name`: even if a malformed `pattern:` in YAML permits bad characters,
	// this gate fails closed.
	ErrContainerNameInvalid = errors.New("daemon: container name invalid")

	// ErrDaemonAlreadyRunning is returned by docker_daemon_start when a
	// container with the resolved name is already running. Used by
	// on_already_running: noop to swallow the race.
	ErrDaemonAlreadyRunning = errors.New("daemon: already running")

	// ErrDaemonNotRunning is returned by docker_daemon_logs when no container
	// with the resolved name is currently running.
	ErrDaemonNotRunning = errors.New("daemon: not running")
)

// containerNameRe is the authoritative gate for daemon container names.
// Anchored, first char must be alnum or underscore, remaining chars allow
// dots and dashes. Matches docker's accepted container-name shape.
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`)

// ResolveContainerName takes an already-rendered container template
// (`${param.X}` substitutions already performed upstream by renderBuiltinWith)
// and produces the final container name prefixed by the project's full name.
// The final string is validated against the authoritative regex; any failure
// returns ErrContainerNameInvalid wrapped with context.
//
// projectFullName may be empty; in that case the rendered template is used as
// the container name verbatim (still subject to the regex).
func ResolveContainerName(projectFullName, renderedTemplate string) (string, error) {
	if renderedTemplate == "" {
		return "", fmt.Errorf("%w: empty container template", ErrContainerNameInvalid)
	}
	var name string
	if projectFullName != "" {
		name = projectFullName + "-" + renderedTemplate
	} else {
		name = renderedTemplate
	}
	if !containerNameRe.MatchString(name) {
		return "", fmt.Errorf("%w: %q", ErrContainerNameInvalid, name)
	}
	return name, nil
}

// StandardLabels returns the `--label key=value` argv pairs for a daemon
// container. The devbox.daemon.params value is encoded via encoding/json so
// values containing quotes, backslashes, or control characters round-trip
// cleanly through `docker inspect` and downstream parsing.
//
// Pairs are emitted as separate argv elements: `--label`, `k=v`, `--label`,
// `k=v`, ... Never shell-concatenated.
//
// Key order is deterministic so callers (and tests) can rely on argv shape.
func StandardLabels(projectFullName, daemonID string, params map[string]any) []string {
	if params == nil {
		params = map[string]any{}
	}
	// Sort param keys for deterministic JSON output. encoding/json sorts map
	// keys already, but we copy into a sorted intermediate to keep the
	// behaviour explicit and to allow future swap of the encoder.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(params))
	for _, k := range keys {
		ordered[k] = params[k]
	}
	paramsJSON, err := json.Marshal(ordered)
	if err != nil {
		// json.Marshal of a map[string]any with simple scalar values cannot
		// realistically fail; fall back to an empty object so we never panic
		// at argv-build time.
		paramsJSON = []byte("{}")
	}
	return []string{
		"--label", LabelProject + "=" + projectFullName,
		"--label", LabelDaemonID + "=" + daemonID,
		"--label", LabelDaemonParams + "=" + string(paramsJSON),
	}
}

// FilterArgsByLabels returns `--filter label=...` argv pairs scoped to a
// specific project + daemon ID. Always filters on BOTH labels: a bare
// daemon.id filter would leak running daemon names across projects sharing
// the same ID (e.g. `services.main.queue` is a common name).
//
// Pairs are emitted as separate argv elements; callers append them to a
// `docker ps` invocation.
func FilterArgsByLabels(projectFullName, daemonID string) []string {
	args := []string{"--filter", "label=" + LabelProject + "=" + projectFullName}
	if daemonID != "" {
		args = append(args, "--filter", "label="+LabelDaemonID+"="+daemonID)
	} else {
		args = append(args, "--filter", "label="+LabelDaemonID)
	}
	return args
}

// DecodeLabels handles both label encodings docker has used:
// modern map[string]string and legacy comma-separated string.
func DecodeLabels(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err == nil {
		return m
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return ParseLegacyLabelString(s)
}

// ParseLegacyLabelString parses a "k=v,k=v" label string from old Docker
// versions. It tracks JSON object depth and quoted strings so that brace/comma
// characters inside a JSON string value (e.g. devbox.daemon.params={"n":"a},b"})
// are not treated as depth changes or entry separators.
func ParseLegacyLabelString(s string) map[string]string {
	out := make(map[string]string)
	depth := 0
	inString := false
	escaped := false
	start := 0
	addEntry := func(part string) {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch s[i] {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch s[i] {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				addEntry(s[start:i])
				start = i + 1
			}
		}
	}
	addEntry(s[start:])
	return out
}

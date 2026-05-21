// Package builtin implements engine-internal pipeline actions.
//
// A builtin is an action described in a pipeline YAML step as:
//
//   - name: some-step
//     type: builtin
//     cmd: <name>
//     with:
//     key: value
//
// Unlike type: shell, type: command (registry), and type: devbox (CLI), a builtin is
// executed directly in Go — no subprocess is spawned. This makes destructive
// and file-system operations safe, auditable, and visible in plan output.
//
// Canonical builtins:
//   - confirm                      — interactive user confirmation prompt
//   - message                      — output styled text
//   - service_configs_copy         — copy service template configs into the hub
//   - service_configs_check        — verify service config files exist
//   - service_dirs_ensure          — ensure service hub directories exist
//   - docker_remove_project_volumes — remove all Docker volumes for the project
//   - docker_wait_healthy          — wait until containers are healthy
//   - remove_paths                 — delete declared paths inside the project root
package builtin

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// ExecContext holds the runtime context passed to every builtin execution.
type ExecContext struct {
	// Config is the merged devbox configuration.
	Config *config.DevboxConfig
	// DockerConfig is the resolved docker policy. Callers must pre-normalise
	// missing-file (os.ErrNotExist) to &config.DockerConfig{} so builtins can
	// pass it directly to docker.NewCompose without nil-checks.
	DockerConfig *config.DockerConfig
	// ProjectRoot is the absolute path to the project root (directory of devbox.yml).
	ProjectRoot string
	// Output is the render writer for styled terminal output. All builtin
	// output flows through this single channel — the executor wires it to
	// the per-step lineTee so frames reach Reporter.StepOutput like every
	// other child line.
	Output *render.Writer
	// Stdin is the reader used for interactive prompts. Defaults to os.Stdin when nil.
	Stdin io.Reader
	// SkipConfirm suppresses all confirm builtins (e.g. when --yes is passed).
	SkipConfirm bool
	// ConfirmFunc is an optional callback for interactive confirmation.
	// When non-nil, the confirm builtin uses it instead of reading from stdin.
	// Returns (true, nil) if confirmed, (false, nil) if denied.
	ConfirmFunc func(msg, okMsg, stopMsg string) (bool, error)
}

// Builtin is an engine-internal pipeline action.
type Builtin interface {
	// Validate checks that the with parameters are valid before the pipeline runs.
	Validate(with map[string]any) error
	// Describe returns a short human-readable description used in plan output.
	Describe(with map[string]any) string
	// Run executes the builtin action. ctx propagates cancellation; long-running
	// loops (e.g. health polling) should select on ctx.Done() to abort promptly.
	Run(ctx context.Context, with map[string]any, ectx ExecContext) error
}

var registry = map[string]Builtin{
	"confirm":                       confirmBuiltin{},
	"message":                       messageBuiltin{},
	"service_configs_copy":          serviceConfigsCopyBuiltin{},
	"service_configs_check":         serviceConfigsCheckBuiltin{},
	"service_dirs_ensure":           serviceDirsEnsureBuiltin{},
	"docker_remove_project_volumes": dockerRemoveProjectVolumesBuiltin{},
	"docker_wait_healthy":           dockerWaitHealthyBuiltin{},
	"docker_daemon_start":           daemonStartBuiltin{},
	"docker_daemon_logs":            daemonLogsBuiltin{},
	"docker_daemon_stop":            daemonStopBuiltin{},
	"daemons_reap":                  daemonsReapBuiltin{},
	"remove_paths":                  removePathsBuiltin{},
}

// Get returns the named builtin, or false if unknown.
func Get(name string) (Builtin, bool) {
	b, ok := registry[name]
	return b, ok
}

// interactiveBuiltins is the single source of truth for which builtins require
// interactive terminal access at runtime. Both the pipeline plan-time guard and
// the workflow runtime dispatch consult this set to reject these builtins
// inside parallel groups.
var interactiveBuiltins = map[string]bool{
	"confirm":            true,
	"docker_daemon_logs": true,
}

// IsInteractive reports whether the named builtin requires interactive
// terminal access (stdin or a foreground TTY) and therefore cannot run inside
// a parallel group. Future interactive builtins register here.
func IsInteractive(name string) bool {
	return interactiveBuiltins[name]
}

// Validate checks that name is a known builtin and that with params are valid.
func Validate(name string, with map[string]any) error {
	b, ok := registry[name]
	if !ok {
		known := knownNames()
		return fmt.Errorf("unknown builtin %q (known: %s)", name, strings.Join(known, ", "))
	}
	return b.Validate(with)
}

// Describe returns a human-readable description for plan display.
func Describe(name string, with map[string]any) string {
	b, ok := registry[name]
	if !ok {
		return fmt.Sprintf("builtin: %s", name)
	}
	return b.Describe(with)
}

// Run executes the named builtin with the given parameters and context.
// ctx propagates cancellation to long-running builtins.
func Run(ctx context.Context, name string, with map[string]any, ectx ExecContext) error {
	b, ok := registry[name]
	if !ok {
		return fmt.Errorf("unknown builtin %q", name)
	}
	return b.Run(ctx, with, ectx)
}

func knownNames() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// getStringParam returns the string value of key from with, or defaultVal if absent/nil.
func getStringParam(with map[string]any, key, defaultVal string) string {
	if with == nil {
		return defaultVal
	}
	v, ok := with[key]
	if !ok || v == nil {
		return defaultVal
	}
	return fmt.Sprintf("%v", v)
}

// getStringSlice returns a string slice from with[key].
// Accepts []any, []string, or a single string value.
func getStringSlice(with map[string]any, key string) ([]string, error) {
	if with == nil {
		return nil, nil
	}
	v, ok := with[key]
	if !ok || v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result, nil
	case []string:
		return val, nil
	case string:
		if val == "" {
			return nil, nil
		}
		return []string{val}, nil
	default:
		return nil, fmt.Errorf("param %q: expected string or list, got %T", key, v)
	}
}

// getBoolParam returns the boolean value of key from with, or defaultVal if
// absent/nil. Accepts bool or string ("true"/"false") values.
func getBoolParam(with map[string]any, key string, defaultVal bool) bool {
	if with == nil {
		return defaultVal
	}
	v, ok := with[key]
	if !ok || v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "true", "yes", "1":
			return true
		case "false", "no", "0", "":
			return false
		}
	}
	return defaultVal
}

// getStringMap returns a string map from with[key], rendering values via
// fmt.Sprint when they are not already strings. Returns nil when key absent.
func getStringMap(with map[string]any, key string) (map[string]string, error) {
	if with == nil {
		return nil, nil
	}
	v, ok := with[key]
	if !ok || v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		if ms, ok := v.(map[string]string); ok {
			return ms, nil
		}
		return nil, fmt.Errorf("param %q: expected map, got %T", key, v)
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = fmt.Sprint(val)
	}
	return out, nil
}

// getMapAny returns a map[string]any from with[key]. Returns nil when absent.
func getMapAny(with map[string]any, key string) (map[string]any, error) {
	if with == nil {
		return nil, nil
	}
	v, ok := with[key]
	if !ok || v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("param %q: expected map, got %T", key, v)
	}
	return m, nil
}

// sortedKeys returns the sorted keys of a string map for deterministic iteration.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// getDurationParam returns the time.Duration value of key from with, or defaultVal if absent/nil.
// Accepts string values parseable by time.ParseDuration.
func getDurationParam(with map[string]any, key string, defaultVal time.Duration) (time.Duration, error) {
	s := getStringParam(with, key, "")
	if s == "" {
		return defaultVal, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("param %q: invalid duration %q: %w", key, s, err)
	}
	return d, nil
}

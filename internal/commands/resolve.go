package commands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
)

// ResolveParams resolves parameter values for a command invocation.
//
// For each declared parameter:
//  1. Use the value from provided (caller-supplied) if present and non-empty.
//  2. Fall back to ParamDef.DefaultFrom (dot-path into cfg.Raw); a missing
//     path or an empty resolved value is treated as not-found and continues
//     to the next step.
//  3. Fall back to ParamDef.Default (literal string).
//  4. If Required and still no value, return an error.
//
// Values are type-coerced according to ParamDef.Type before being returned.
func ResolveParams(defs map[string]ParamDef, provided map[string]string, cfg *config.DevboxConfig) (map[string]any, error) {
	result := make(map[string]any, len(defs))
	for name, def := range defs {
		raw, ok := provided[name]
		if !ok || raw == "" {
			// Try default_from dot-path. Empty resolved value is treated as not-found
			// so the literal default below acts as a true safety net.
			if def.DefaultFrom != "" && cfg != nil {
				if v, found := config.ResolvePath(cfg.Raw, def.DefaultFrom); found {
					s := fmt.Sprintf("%v", v)
					if s != "" {
						raw = s
						ok = true
					}
				}
			}
		}
		if !ok || raw == "" {
			// Fall back to literal default.
			if def.Default != "" {
				raw = def.Default
				ok = true
			}
		}
		if (!ok || raw == "") && def.Required {
			return nil, fmt.Errorf("param %q is required but was not provided", name)
		}
		// Coerce to declared type.
		coerced, err := coerceParam(name, raw, def.Type)
		if err != nil {
			return nil, err
		}
		// Validate pattern for string/path params when a pattern is declared.
		if def.Pattern != "" && (def.Type == ParamTypeString || def.Type == ParamTypePath || def.Type == "") {
			if raw != "" {
				re, err := regexp.Compile(def.Pattern)
				if err != nil {
					return nil, fmt.Errorf("param %q: invalid pattern %q: %w", name, def.Pattern, err)
				}
				if loc := re.FindStringIndex(raw); loc == nil || loc[0] != 0 || loc[1] != len(raw) {
					return nil, fmt.Errorf("param %q: value %q does not match required pattern %q", name, raw, def.Pattern)
				}
			}
		}
		result[name] = coerced
	}
	return result, nil
}

// coerceParam converts a raw string value to the Go type implied by pt.
// An empty raw string coerces to the zero value of the type.
func coerceParam(name, raw string, pt ParamType) (any, error) {
	switch pt {
	case ParamTypeBool:
		if raw == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("param %q: cannot parse %q as bool", name, raw)
		}
		return b, nil
	case ParamTypeInt:
		if raw == "" {
			return 0, nil
		}
		i, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("param %q: cannot parse %q as int", name, raw)
		}
		return i, nil
	default:
		// ParamTypeString and ParamTypePath are kept as strings.
		return raw, nil
	}
}

// ResolveContext resolves context values for a command invocation.
//
// For each declared context entry the value is looked up via ContextDef.From
// (a dot-path into cfg.Raw).  When Required is true and the path resolves to
// nil or an empty string, an error is returned.
func ResolveContext(defs map[string]ContextDef, cfg *config.DevboxConfig) (map[string]any, error) {
	result := make(map[string]any, len(defs))
	for name, def := range defs {
		var val any
		if def.From != "" && cfg != nil {
			if v, found := config.ResolvePath(cfg.Raw, def.From); found {
				val = v
			}
		}
		if def.Required && isEmpty(val) {
			return nil, fmt.Errorf("context %q (from %q) is required but resolved to empty", name, def.From)
		}
		result[name] = val
	}
	return result, nil
}

// isEmpty returns true for nil or empty string.
// Used to test whether a required context value is considered missing.
func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

// BuildEnv constructs the environment variable map for a command execution.
//
// The map is assembled in the following priority order (later wins):
//  1. env vars declared via context entries (ContextDef.Env → resolved value)
//  2. env vars declared via params (ParamDef.Env → resolved value)
//  3. env vars declared via files (FileSpec.Env → resolved path)
//  4. command-level env map (CommandDef.Env entries, kept as raw template strings)
//
// Entries with an empty Env key are silently skipped.
// The command-level env values are stored as-is (callers render templates later).
//
// Returns an error if an env name is declared twice in any of the sources (context, params, files, or command-level env).
// This is defensive — load-time validation already rejects such conflicts, but this guards against programmatic constructions.
func BuildEnv(cmd *CommandDef, params map[string]any, ctx map[string]any, files map[string]tpl.ResolvedFile) (map[string]string, error) {
	env := make(map[string]string)
	sources := make(map[string]string) // Track which source declared each env var for conflict detection.

	// 1. Context env vars.
	for name, def := range cmd.Context {
		if def.Env == "" {
			continue
		}
		if v, ok := ctx[name]; ok {
			if existing, exists := sources[def.Env]; exists {
				return nil, fmt.Errorf("env conflict: %q declared by %s and context.%s", def.Env, existing, name)
			}
			env[def.Env] = fmt.Sprintf("%v", v)
			sources[def.Env] = "context." + name
		}
	}

	// 2. Param env vars.
	for name, def := range cmd.Params {
		if def.Env == "" {
			continue
		}
		if v, ok := params[name]; ok {
			if existing, exists := sources[def.Env]; exists {
				return nil, fmt.Errorf("env conflict: %q declared by %s and params.%s", def.Env, existing, name)
			}
			env[def.Env] = fmt.Sprintf("%v", v)
			sources[def.Env] = "params." + name
		}
	}

	// 3. File env vars.
	for id, spec := range cmd.Files {
		if spec.Env == "" {
			continue
		}
		if file, ok := files[id]; ok {
			if existing, exists := sources[spec.Env]; exists {
				return nil, fmt.Errorf("env conflict: %q declared by %s and files.%s", spec.Env, existing, id)
			}
			env[spec.Env] = file.Path
			sources[spec.Env] = "files." + id
		}
	}

	// 4. Command-level env map (raw, may contain ${...} expressions).
	for k, v := range cmd.Env {
		if k != "" {
			if existing, exists := sources[k]; exists {
				return nil, fmt.Errorf("env conflict: %q declared by %s and env block", k, existing)
			}
			env[k] = v
			sources[k] = "env block"
		}
	}

	return env, nil
}

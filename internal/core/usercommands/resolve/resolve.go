// Package resolve contains parameter, context, and environment resolution helpers.
package resolve

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// Params resolves parameter values for a command invocation.
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
func Params(defs map[string]model.ParamDef, provided map[string]string, cfg *config.DweConfig) (map[string]any, error) {
	result := make(map[string]any, len(defs))
	for name, def := range defs {
		raw, ok := provided[name]
		if !ok || raw == "" {
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
			if def.Default != "" {
				raw = def.Default
				ok = true
			}
		}
		if (!ok || raw == "") && def.Required {
			return nil, fmt.Errorf("param %q is required but was not provided", name)
		}
		coerced, err := coerceParam(name, raw, def.Type)
		if err != nil {
			return nil, err
		}
		if def.Pattern != "" && (def.Type == model.ParamTypeString || def.Type == model.ParamTypePath || def.Type == "") {
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

// ParamDefaults returns the raw string values that would prefill a parameter form.
//
// For each declared parameter:
//  1. Use provided[name] (treated as missing when empty — matches Params semantics).
//  2. Fall back to DefaultFrom (dot-path into cfg.Raw); a missing path or empty
//     resolved value falls through.
//  3. Fall back to Default (literal string).
//
// Required-checks, type coercion, and pattern validation are intentionally
// skipped — those are enforced by the form (per-field Validate) and by Params
// at run time. Missing or pattern-mismatched values are returned as-is (or as
// the empty string), never as an error.
func ParamDefaults(defs map[string]model.ParamDef, provided map[string]string, cfg *config.DweConfig) map[string]string {
	result := make(map[string]string, len(defs))
	for name, def := range defs {
		raw, ok := provided[name]
		if !ok || raw == "" {
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
			if def.Default != "" {
				raw = def.Default
			}
		}
		result[name] = raw
	}
	return result
}

// coerceParam converts a raw string value to the Go type implied by pt.
// An empty raw string coerces to the zero value of the type.
func coerceParam(name, raw string, pt model.ParamType) (any, error) {
	switch pt {
	case model.ParamTypeBool:
		if raw == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("param %q: cannot parse %q as bool", name, raw)
		}
		return b, nil
	case model.ParamTypeInt:
		if raw == "" {
			return 0, nil
		}
		i, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("param %q: cannot parse %q as int", name, raw)
		}
		return i, nil
	default:
		return raw, nil
	}
}

// Context resolves context values for a command invocation.
//
// For each declared context entry the value is looked up via ContextDef.From
// (a dot-path into cfg.Raw).  When Required is true and the path resolves to
// nil or an empty string, an error is returned.
func Context(defs map[string]model.ContextDef, cfg *config.DweConfig) (map[string]any, error) {
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
// Entries come from four sources, and a name declared by two of them is an
// error naming both sites — never a silent override:
//  1. context entries (ContextDef.Env → resolved value)
//  2. params (ParamDef.Env → resolved value)
//  3. files (FileSpec.Env → resolved path)
//  4. command-level env map (CommandDef.Env, kept as raw template strings)
func BuildEnv(cmd *model.CommandDef, params map[string]any, ctx map[string]any, files map[string]tpl.ResolvedFile) (map[string]string, error) {
	env := make(map[string]string)
	sources := make(map[string]string)

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

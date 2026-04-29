package tpl

import (
	"bytes"
	"fmt"
	"os/user"
	"regexp"
	"runtime"
	"strings"
	"text/template"

	"devbox-cli/internal/condition"
)

// ResolvedFile represents a single resolved file path artifact.
// Minimal DTO to avoid import cycles from tpl → commands.
type ResolvedFile struct {
	Path string
}

// RenderContext holds all data available during command template evaluation.
type RenderContext struct {
	// Raw is the merged devbox config map (devbox.yml + defaults.yml + local.yml).
	Raw map[string]any
	// Params holds resolved command parameter values (keyed by param name).
	Params map[string]any
	// Context holds resolved context values (keyed by context name).
	Context map[string]any
	// Files holds resolved file paths (keyed by file id).
	Files map[string]ResolvedFile
	// Host provides runtime host information.
	Host HostInfo
}

// HostInfo holds runtime host values injected into templates.
type HostInfo struct {
	UID string
	GID string
}

// CurrentHostInfo returns the UID/GID to use inside containers.
// On macOS, Docker Desktop runs containers in a Linux VM where host UIDs
// (e.g. 501) don't exist in the container's /etc/passwd. The convention
// is to use 1000:1000, matching the UID/GID baked into the image at build
// time. On Linux, the actual host UID/GID is returned so file permissions
// match.
func CurrentHostInfo() HostInfo {
	h := HostInfo{UID: "1000", GID: "1000"}
	if runtime.GOOS == "darwin" {
		return h
	}
	u, err := user.Current()
	if err != nil {
		return h
	}
	h.UID = u.Uid
	h.GID = u.Gid
	return h
}

// varPattern matches ${identifier} and ${dot.path} expressions.
var varPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_.]*)\}`)

// CompileVarSyntax rewrites ${...} expressions into Go template calls.
//
// Simple vars and dot-paths are rewritten using the resolve helper:
//
//	${project.name}  →  {{ resolve .Raw "project.name" }}
//	${name}          →  {{ resolve .Raw "name" }}
//
// The special namespaces "param" and "context" are routed to their maps:
//
//	${param.key}     →  {{ resolveMap .Params "key" }}
//	${context.key}   →  {{ resolveMap .Context "key" }}
//
// The special namespace "files" resolves file artifacts:
//
//	${files.id.path} →  {{ resolveFile .Files "id" "path" }}
//
// The special namespace "host" resolves runtime helpers:
//
//	${host.uid}      →  {{ .Host.UID }}
//	${host.gid}      →  {{ .Host.GID }}
//
// Literal Go template expressions ({{ }}) are left unchanged.
// A literal dollar sign can be written as $$ (passed through as-is, not rewritten).
func CompileVarSyntax(input string) string {
	return varPattern.ReplaceAllStringFunc(input, func(match string) string {
		// Extract the path between ${ and }
		inner := match[2 : len(match)-1]

		// Route by namespace
		head, tail, hasTail := strings.Cut(inner, ".")

		switch head {
		case "files":
			if hasTail {
				// files.<id>.<subkey>
				id, subkey, hasSubkey := strings.Cut(tail, ".")
				if hasSubkey {
					return fmt.Sprintf(`{{ resolveFile .Files %q %q }}`, id, subkey)
				}
				// files.<id> with no subkey: route through resolveFile to avoid
				// accidentally resolving against the raw config map. resolveFile
				// returns "" for an unknown subkey, which is the correct no-op.
				return fmt.Sprintf(`{{ resolveFile .Files %q "" }}`, id)
			}
		case "host":
			if hasTail {
				switch tail {
				case "uid":
					return "{{ .Host.UID }}"
				case "gid":
					return "{{ .Host.GID }}"
				}
			}
			// Unknown host sub-key: fall through to generic resolve
		case "param":
			if hasTail {
				return fmt.Sprintf(`{{ resolveMap .Params %q }}`, tail)
			}
		case "context":
			if hasTail {
				return fmt.Sprintf(`{{ resolveMap .Context %q }}`, tail)
			}
		}

		// Default: resolve against .Raw config map
		return fmt.Sprintf(`{{ resolve .Raw %q }}`, inner)
	})
}

// RenderCommand compiles ${...} syntax in expr, then evaluates the resulting
// Go template against data.
func RenderCommand(expr string, data *RenderContext) (string, error) {
	compiled := CompileVarSyntax(expr)
	if !strings.Contains(compiled, "{{") {
		return compiled, nil
	}
	fm := commandFuncMap()
	t, err := template.New("").Funcs(fm).Parse(compiled)
	if err != nil {
		return "", fmt.Errorf("parse command template %q: %w", expr, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute command template %q: %w", expr, err)
	}
	return buf.String(), nil
}

// commandFuncMap returns the FuncMap used in command template evaluation.
// It extends the base FuncMap with resolve, resolveMap, and resolveFile helpers.
func commandFuncMap() template.FuncMap {
	fm := FuncMap()
	fm["resolve"] = resolveRaw
	fm["resolveMap"] = resolveMap
	fm["resolveFile"] = resolveFile
	return fm
}

// resolveRaw resolves a dot-path in a raw config map.
// Returns "" when the path is not found.
func resolveRaw(raw map[string]any, dotPath string) any {
	if raw == nil {
		return ""
	}
	val := resolveMapPath(raw, dotPath)
	if val == nil {
		return ""
	}
	return val
}

// resolveMap resolves a key in a flat string→any map.
// Returns "" when the key is not found.
func resolveMap(m map[string]any, key string) any {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		return v
	}
	return ""
}

// resolveFile resolves a subkey in a ResolvedFile from the Files map.
// Returns "" when the file id or subkey is not found.
func resolveFile(files map[string]ResolvedFile, id string, subkey string) any {
	if files == nil {
		return ""
	}
	f, ok := files[id]
	if !ok {
		return ""
	}
	switch subkey {
	case "path":
		return f.Path
	default:
		return ""
	}
}

// resolveMapPath walks a dot-separated path in a nested map.
func resolveMapPath(m map[string]any, path string) any {
	if path == "" || m == nil {
		return nil
	}
	head, tail, _ := strings.Cut(path, ".")
	v, ok := m[head]
	if !ok {
		return nil
	}
	if tail == "" {
		return v
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return resolveMapPath(sub, tail)
}

// EvalCommandCondition evaluates a workflow when-expression, which may contain
// ${...} template syntax (unlike deploy/lifecycle conditions which use Go templates).
//
// Evaluation flow:
// 1. Empty expr → true
// 2. Render ${...} and {{ }} against RenderContext
// 3. Classify the rendered result (cmd:, builtin predicate, or literal)
// 4. For literals, apply boolean-value fast-path (true/1 → true; false/0/"" → false)
// 5. For predicates/commands, delegate to condition.EvalBuiltin/EvalCmd
//
// Returns wrapped errors prefixed with "eval when %q" for render errors or
// unknown/malformed predicates (so typos fail loudly, matching deploy behavior).
func EvalCommandCondition(expr string, ctx *RenderContext, projectRoot string) (bool, error) {
	if expr == "" {
		return true, nil
	}

	// Render ${...} and {{ }} against command template context
	rendered, err := RenderCommand(expr, ctx)
	if err != nil {
		return false, fmt.Errorf("eval when %q: %w", expr, err)
	}

	// After rendering, check for literal boolean values first (from ${param.*} or ${context.*})
	switch strings.TrimSpace(rendered) {
	case "", "false", "0":
		return false, nil
	case "true", "1":
		return true, nil
	}

	// Not a literal boolean; classify as cmd: or builtin predicate
	kind, payload := condition.Classify(rendered)

	switch kind {
	case condition.KindCmd:
		return condition.EvalCmd(payload, projectRoot)
	case condition.KindBuiltin:
		ok, err := condition.EvalBuiltin(payload, projectRoot)
		if err != nil {
			return false, fmt.Errorf("eval when %q: %w", expr, err)
		}
		return ok, nil
	default: // KindTemplate — unreachable post-render, but defend against it
		return false, fmt.Errorf("eval when %q: unexpected residual template", expr)
	}
}

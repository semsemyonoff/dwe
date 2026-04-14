package tpl

import (
	"bytes"
	"fmt"
	"os/user"
	"regexp"
	"strings"
	"text/template"
)

// RenderContext holds all data available during command template evaluation.
type RenderContext struct {
	// Raw is the merged devbox config map (devbox.yml + defaults.yml + local.yml).
	Raw map[string]any
	// Params holds resolved command parameter values (keyed by param name).
	Params map[string]any
	// Context holds resolved context values (keyed by context name).
	Context map[string]any
	// Host provides runtime host information.
	Host HostInfo
}

// HostInfo holds runtime host values injected into templates.
type HostInfo struct {
	UID string
	GID string
}

// CurrentHostInfo returns the UID/GID of the current OS user.
// On lookup failure, both fields fall back to "0".
func CurrentHostInfo() HostInfo {
	h := HostInfo{UID: "0", GID: "0"}
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
// It extends the base FuncMap with resolve and resolveMap helpers.
func commandFuncMap() template.FuncMap {
	fm := FuncMap()
	fm["resolve"] = resolveRaw
	fm["resolveMap"] = resolveMap
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

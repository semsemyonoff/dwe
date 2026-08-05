package tpl

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/shared/hostid"
)

// ResolvedFile represents a single resolved file path artifact.
// Minimal DTO to avoid import cycles from tpl → commands.
type ResolvedFile struct {
	Path string
}

// RenderContext holds all data available during command template evaluation.
type RenderContext struct {
	// Raw is the merged dwe config map (workspace.yml + defaults.yml + local.yml).
	Raw map[string]any
	// Params holds resolved command parameter values (keyed by param name).
	Params map[string]any
	// Context holds resolved context values (keyed by context name).
	Context map[string]any
	// Files holds resolved file paths (keyed by file id).
	Files map[string]ResolvedFile
	// Host provides runtime host information.
	Host HostInfo
	// Snapshot holds snapshot variables (name, path, description, variant,
	// created_at) when rendering inside a snapshot workflow scope. Visibility
	// is gated by SnapshotScope — see SnapshotScope and validateSnapshotScope.
	Snapshot map[string]any
	// Generated holds the current service's harvested generated values
	// (field name → value) replayed during a config render pass. Populated
	// from the generated-value store (.dwe/generated.yml). Absent keys
	// resolve to "" — consistent with all other ${...} resolvers.
	Generated map[string]string
	// Args holds the pass-through arguments a user supplied after `--`
	// (`dwe cmd site.test -- --run x.test.ts`), already merged with the
	// command's args.default / args.prefix policy. Referenced as ${args}.
	//
	// The EXECUTION paths never read this field through the template engine.
	// A `cmd:` template has its ${args} slot rewritten to "$@" and the arguments
	// handed to `sh -c` as positional parameters; an `argv:` element equal to
	// ${args} is spliced element-wise. Both render with Args cleared, so a raw
	// `{{ .Args }}` cannot interpolate caller bytes into a command — see
	// runio.RenderShellCommand / RenderArgvWithArgs.
	//
	// What renderArgs (the ${args} resolver) serves is the remaining, non-executing
	// references — a `messages.*` line, an `env:` value, a `workdir` — where the
	// value lands in display text or a single exec argument with no shell to
	// re-parse it, and a plain space-joined form is correct. Empty Args renders
	// as the empty string.
	Args []string
	// SnapshotScope governs which ${snapshot.*} keys are allowed at compile
	// time. Zero value (SnapshotScopeNone) makes any ${snapshot.*} reference
	// a compile error.
	SnapshotScope SnapshotScope
}

// SnapshotScope controls which ${snapshot.*} keys are valid at compile time.
type SnapshotScope int

const (
	// SnapshotScopeNone is the default scope outside any snapshot workflow.
	// Any ${snapshot.*} reference is a compile error.
	SnapshotScopeNone SnapshotScope = iota
	// SnapshotScopeCreate is the scope of a `create:` workflow. All keys
	// except created_at are valid; created_at is rejected because it
	// doesn't exist yet at create time.
	SnapshotScopeCreate
	// SnapshotScopeRestoreOrRemove is the scope of `restore:` and `remove:`
	// workflows. All keys (including created_at) are valid.
	SnapshotScopeRestoreOrRemove
)

// String returns a stable identifier for the scope (used in synthetic command IDs).
func (s SnapshotScope) String() string {
	switch s {
	case SnapshotScopeCreate:
		return "create"
	case SnapshotScopeRestoreOrRemove:
		return "restore"
	default:
		return "none"
	}
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
	return HostInfo(hostid.Current())
}

// varPattern matches ${identifier} and ${dot.path} expressions.
var varPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_.]*)\}`)

// VarPattern is the exported ${...} matcher used by CompileVarSyntax. Capture
// group 1 is the inner dot-path. The static usage scanner (varsusage) reuses it
// so its notion of a ${vars.x} reference stays byte-identical to what the
// renderer actually rewrites — no internal whitespace, no leading digit.
var VarPattern = varPattern

// KnownVarHeads is the set of ${...} head namespaces CompileVarSyntax will
// rewrite into a template call. It is the union of the merged-config root
// keys (mirroring internal/core/project/config's allowedRootKeys — kept in
// sync by a cross-check test in that package, since tpl must not import
// config) and the special namespaces switched on below (files, host, param,
// context, snapshot, generated, args). A ${...} whose head is NOT in this set
// is left as a literal string instead of being rewritten — see the
// unknown-head branch of CompileVarSyntax for why.
//
// __configPath is deliberately excluded: it is an internal key the config
// loader injects, not part of the authoring contract, so a reference to it
// renders as a literal rather than leaking loader internals.
var KnownVarHeads = []string{
	// mirrors config.allowedRootKeys
	"schema_version",
	"project",
	"runtime",
	"state",
	"exports",
	"compose",
	"ui",
	"docs",
	"services",
	"vars",
	"update",
	"bridge",
	"stop",
	// special namespaces CompileVarSyntax switches on directly
	"files",
	"host",
	"param",
	"context",
	"snapshot",
	"generated",
	"args",
}

// knownVarHeadSet is the membership index over KnownVarHeads.
var knownVarHeadSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(KnownVarHeads))
	for _, h := range KnownVarHeads {
		m[h] = struct{}{}
	}
	return m
}()

// CompileVarSyntax rewrites ${...} expressions into Go template calls.
//
// Vars and dot-paths whose head is in KnownVarHeads are rewritten using the
// resolve helper:
//
//	${project.name}  →  {{ resolve .Raw "project.name" }}
//	${vars.db.user}  →  {{ resolve .Raw "vars.db.user" }}
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
// The special namespace "generated" resolves harvested generated values for
// the current service (config render pass):
//
//	${generated.app_key} → {{ resolveGenerated .Generated "app_key" }}
//
// A ${...} whose head is NOT in KnownVarHeads (a shell-style ${HOME}, a typo,
// an unrelated $-braced token) is left unchanged rather than collapsing to
// "" — the correctness argument for this is documented on KnownVarHeads.
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
		case "snapshot":
			if hasTail {
				return fmt.Sprintf(`{{ resolveMap .Snapshot %q }}`, tail)
			}
		case "generated":
			if hasTail {
				return fmt.Sprintf(`{{ resolveGenerated .Generated %q }}`, tail)
			}
		case "args":
			// ${args} is a whole-namespace reference with no sub-key — there is
			// nothing to index into. Anything with a tail (${args.0}) falls
			// through to the generic .Raw resolve — "args" has no matching key
			// in .Raw, so it yields "".
			if !hasTail {
				return "{{ renderArgs .Args }}"
			}
		}

		// Default: resolve against .Raw config map, but only when the head is
		// a known namespace. An unknown head (a shell-style ${VAR}, a typo, or
		// a stale top-level dot-path from before the strict root landed) is
		// left as a literal ${...} instead of silently collapsing to "" — see
		// KnownVarHeads.
		if _, ok := knownVarHeadSet[head]; ok {
			return fmt.Sprintf(`{{ resolve .Raw %q }}`, inner)
		}
		return match
	})
}

// validateSnapshotScope walks ${snapshot.<key>} references in expr and rejects
// any use that the active scope forbids. CompileVarSyntax itself stays pure;
// this pre-scan runs once before compile from RenderCommand.
func validateSnapshotScope(expr string, scope SnapshotScope) error {
	for _, m := range varPattern.FindAllStringSubmatch(expr, -1) {
		inner := m[1]
		head, tail, hasTail := strings.Cut(inner, ".")
		if head != "snapshot" || !hasTail {
			continue
		}
		switch scope {
		case SnapshotScopeNone:
			return fmt.Errorf("template uses ${snapshot.%s} outside a snapshot workflow", tail)
		case SnapshotScopeCreate:
			if tail == "created_at" {
				return fmt.Errorf("template uses ${snapshot.created_at} in create scope (not yet known at create time)")
			}
		}
	}
	return nil
}

// CompileCommand syntactically validates a command-template expression
// without executing it. Returns an error when ${snapshot.*} appears in a
// non-snapshot scope, ${...} expansion, or `{{ }}` template parsing fails.
// Useful for static validators that want to surface typos without
// exercising runtime data or shell predicates.
//
// scope is the snapshot scope the expression will run under at runtime.
// For surfaces that are never snapshot-scoped (e.g. user-command `hide:`),
// pass SnapshotScopeNone so any ${snapshot.*} reference is rejected at
// validate time instead of exploding at runtime.
func CompileCommand(expr string, scope SnapshotScope) error {
	if expr == "" {
		return nil
	}
	if err := validateSnapshotScope(expr, scope); err != nil {
		return err
	}
	compiled := CompileVarSyntax(expr)
	if !strings.Contains(compiled, "{{") {
		return nil
	}
	fm := commandFuncMap()
	if _, err := template.New("").Funcs(fm).Parse(compiled); err != nil {
		return fmt.Errorf("parse command template %q: %w", expr, err)
	}
	return nil
}

// RenderCommand compiles ${...} syntax in expr, then evaluates the resulting
// Go template against data.
func RenderCommand(expr string, data *RenderContext) (string, error) {
	scope := SnapshotScopeNone
	if data != nil {
		scope = data.SnapshotScope
	}
	if err := validateSnapshotScope(expr, scope); err != nil {
		return "", err
	}
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
	fm["resolveGenerated"] = resolveGenerated
	fm["renderArgs"] = renderArgs
	return fm
}

// renderArgs joins pass-through arguments for the ${args} references that do
// NOT drive process execution — a `messages.success` line, an `env:` value, a
// `workdir`. Those land in a display string or a single exec argument, with no
// shell to re-parse them, so a plain space-joined form is both correct and safe.
//
// The execution paths never reach this function. A `cmd:` template has its
// ${args} slot rewritten to "$@" before rendering, with the arguments passed as
// positional parameters (runio.RenderShellCommand); an `argv:` element equal to
// ${args} is spliced element-wise (runio.RenderArgvWithArgs). That split is the
// security boundary: shell-quoting the arguments and interpolating them into the
// program text — what this function used to do — is safe only in an unquoted
// argument position, and a command author writing `"${args}"` (the natural shell
// habit) reopened command substitution.
//
// Empty args render as the empty string.
func renderArgs(args []string) string {
	return strings.Join(args, " ")
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

// resolveGenerated resolves a generated-value field for the current service.
// Returns "" when the field is absent — consistent with the other lenient
// ${...} resolvers (resolve/resolveMap).
func resolveGenerated(m map[string]string, key string) any {
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
		ok, err := condition.EvalCmd(payload, projectRoot)
		if err != nil {
			return false, fmt.Errorf("eval when %q: %w", expr, err)
		}
		return ok, nil
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

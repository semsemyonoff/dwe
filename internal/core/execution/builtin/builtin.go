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
// Unlike type: shell, type: command (registry), and type: dwe (CLI), a builtin is
// executed directly in Go — no subprocess is spawned. This makes destructive
// and file-system operations safe, auditable, and visible in plan output.
//
// The registry is composed in buildRegistry from the per-domain subpackages
// (interaction/, services/, containers/, fs/, env/, source/) plus a handful of
// root-level predicates. Each spec.Entry carries its Kind and a one-line
// Summary; Inventory returns the whole set, which is the single source the
// documentation surfaces (dwe docs llms-txt) read. Deliberately not duplicated
// as a list here — a second copy drifts, and this one already had.
package builtin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/containers"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/env"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/fs"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/interaction"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/services"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/source"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
)

// Builtin is an engine-internal pipeline action.
type Builtin = spec.Builtin

// ExecContext holds the runtime context passed to every builtin execution.
type ExecContext = spec.ExecContext

// Kind classifies how a builtin may be invoked.
type Kind = spec.Kind

// CallerContext identifies who is invoking a builtin, used for kind/context gating.
type CallerContext = spec.CallerContext

// Kind aliases re-exported from spec so existing callers keep compiling.
const (
	KindAction    = spec.KindAction
	KindPredicate = spec.KindPredicate
	KindInternal  = spec.KindInternal
)

// CallerContext aliases re-exported from spec so existing callers keep compiling.
const (
	CtxUserYAML  = spec.CtxUserYAML
	CtxPredicate = spec.CtxPredicate
	CtxInternal  = spec.CtxInternal
)

var registry = buildRegistry()

func buildRegistry() map[string]spec.Entry {
	r := map[string]spec.Entry{
		// KindPredicate: read-only checks for check: positions and validate.yml
		"shell":               {Impl: Shell{}, Kind: spec.KindPredicate, Summary: "run an arbitrary sh -c command; exit status 0 is true"},
		"tcp_reachable":       {Impl: TCPReachable{}, Kind: spec.KindPredicate, Summary: "TCP host:port reachability check"},
		"http_check":          {Impl: HTTPCheck{}, Kind: spec.KindPredicate, Summary: "HTTP GET status/body check with retries"},
		"config_keys_present": {Impl: ConfigKeysPresent{}, Kind: spec.KindPredicate, Summary: "verify merged-config dot-paths resolve to non-empty values"},
	}
	for _, src := range []map[string]spec.Entry{
		containers.Builtins(),
		services.Builtins(),
		fs.Builtins(),
		env.Builtins(),
		interaction.Builtins(),
		source.Builtins(),
	} {
		for k, v := range src {
			if _, dup := r[k]; dup {
				panic("duplicate builtin name: " + k)
			}
			r[k] = v
		}
	}
	return r
}

// InventoryEntry is one registered builtin's static metadata, without its
// implementation. It is the enumeration surface for documentation generators;
// the execution layer must not be imported by the docs subsystem, so the
// inventory is collected in cli/ and passed down.
type InventoryEntry struct {
	Name    string
	Kind    Kind
	Summary string
}

// Inventory returns every registered builtin sorted by name, including
// KindInternal ones (a reader needs to know why docker_daemon_start is
// rejected in user-authored YAML). Callers filter by Kind as needed.
func Inventory() []InventoryEntry {
	out := make([]InventoryEntry, 0, len(registry))
	for name, entry := range registry {
		out = append(out, InventoryEntry{Name: name, Kind: entry.Kind, Summary: entry.Summary})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the named builtin if it exists and is compatible with ctx.
// Returns (nil, false) when the name is unknown or when the builtin's kind
// is not compatible with the caller context.
func Get(name string, ctx CallerContext) (Builtin, bool) {
	entry, ok := registry[name]
	if !ok {
		return nil, false
	}
	if !kindAllowed(entry.Kind, ctx) {
		return nil, false
	}
	return entry.Impl, true
}

// kindAllowed reports whether a builtin of the given kind may be invoked from ctx.
//
// Rules:
//   - KindAction: allowed in CtxUserYAML (step body) and CtxPredicate (check: position).
//     Actions may be read-only (e.g. service_configs_check) and are safe in check: position.
//   - KindPredicate: allowed in CtxPredicate (check: position, validate.yml) and in
//     CtxUserYAML (step body). A predicate used as a step body is an assertion:
//     false fails the step with the predicate's message. Because CtxUserYAML is
//     shared with user-command type: builtin definitions, predicates are legal as
//     user commands too — intentional (commands and pipelines share the registry).
//   - KindInternal: only in CtxInternal (engine-synthetic phases or daemon-generated commands).
func kindAllowed(k spec.Kind, ctx spec.CallerContext) bool {
	switch k {
	case spec.KindAction, spec.KindPredicate:
		return ctx == spec.CtxUserYAML || ctx == spec.CtxPredicate
	case spec.KindInternal:
		return ctx == spec.CtxInternal
	}
	return false
}

// kindMismatchHint returns a human-readable hint when a builtin's kind is
// incompatible with the given caller context.
func kindMismatchHint(name string, k spec.Kind, _ spec.CallerContext) string {
	switch k {
	case spec.KindInternal:
		return fmt.Sprintf("builtin %q is engine-internal and cannot be called from user-authored YAML; it is invoked automatically by the engine", name)
	case spec.KindPredicate:
		return fmt.Sprintf("builtin %q is a predicate and can only be used in user-authored YAML (step bodies as assertions, check: positions, validate.yml cmd: entries), not from this context", name)
	case spec.KindAction:
		return fmt.Sprintf("builtin %q is an action and cannot be called from this context", name)
	}
	return fmt.Sprintf("builtin %q cannot be invoked from this context", name)
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

// KindOf returns the kind of the named builtin, or (0, false) when the name is
// unknown. Unlike Get, it performs no caller-context gating — callers such as
// the pipeline's always-run helper use it to classify a step body
// (KindPredicate body = assertion, forces execution past deploy's skip gates).
func KindOf(name string) (Kind, bool) {
	entry, ok := registry[name]
	if !ok {
		return 0, false
	}
	return entry.Kind, true
}

// Validate checks that name is a known builtin compatible with ctx and that with params are valid.
func Validate(name string, with map[string]any, ctx CallerContext) error {
	entry, ok := registry[name]
	if !ok {
		known := knownNames()
		return fmt.Errorf("unknown builtin %q (known: %s)", name, strings.Join(known, ", "))
	}
	if !kindAllowed(entry.Kind, ctx) {
		return fmt.Errorf("%s", kindMismatchHint(name, entry.Kind, ctx))
	}
	return entry.Impl.Validate(with)
}

// Describe returns a human-readable description for plan display.
// Unlike Get/Validate/Run, Describe is display-only and does not enforce kind/context gating.
func Describe(name string, with map[string]any) string {
	entry, ok := registry[name]
	if !ok {
		return fmt.Sprintf("builtin: %s", name)
	}
	return entry.Impl.Describe(with)
}

// Run executes the named builtin with the given parameters and context.
// ctx propagates cancellation to long-running builtins.
// callerCtx enforces kind/context compatibility before execution.
func Run(ctx context.Context, name string, with map[string]any, ectx ExecContext, callerCtx CallerContext) error {
	entry, ok := registry[name]
	if !ok {
		return fmt.Errorf("unknown builtin %q", name)
	}
	if !kindAllowed(entry.Kind, callerCtx) {
		return fmt.Errorf("%s", kindMismatchHint(name, entry.Kind, callerCtx))
	}
	return entry.Impl.Run(ctx, with, ectx)
}

func knownNames() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

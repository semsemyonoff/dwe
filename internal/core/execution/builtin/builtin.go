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
// Canonical builtins (registry is composed in buildRegistry from per-domain subpackages):
//
//	interaction/ (KindAction)
//	- confirm                       — interactive user confirmation prompt
//	- message                       — output styled text
//
//	services/ (KindAction)
//	- service_configs_copy          — copy service template configs into the hub
//	- service_configs_check         — verify service config files exist
//	- service_dirs_ensure           — ensure service hub directories exist
//
//	containers/ (KindAction unless noted)
//	- docker_remove_project_volumes — remove all Docker volumes for the project
//	- docker_wait_healthy           — wait until containers are healthy
//	- containers_running            — (KindPredicate) fast "is running" check
//	- docker_daemon_start           — start a named daemon container (docker compose run -d)
//	- docker_daemon_logs            — tail daemon container logs foreground (interactive)
//	- docker_daemon_stop            — stop a named daemon container (idempotent)
//	- docker_stop_remove_container  — (KindInternal) stop and remove a named container; per-service reset baseline
//	- daemons_reap                  — (KindInternal) stop all project daemon containers; auto-injected as _auto_reap_daemons
//
//	fs/ (KindAction)
//	- remove_paths                  — delete declared paths inside the project root
//	fs/ (KindPredicate)
//	- file_exists                   — check whether a file exists
//
//	env/ (KindPredicate)
//	- env_keys_present              — verify env-file keys are defined
//	- executable_in_path            — verify an executable resolves on PATH
//
//	root (KindPredicate)
//	- shell                         — exit-code predicate via /bin/sh
//	- tcp_reachable                 — TCP host:port reachability predicate
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
		"shell":         {Impl: Shell{}, Kind: spec.KindPredicate},
		"tcp_reachable": {Impl: TCPReachable{}, Kind: spec.KindPredicate},
	}
	for _, src := range []map[string]spec.Entry{
		containers.Builtins(),
		services.Builtins(),
		fs.Builtins(),
		env.Builtins(),
		interaction.Builtins(),
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
//   - KindPredicate: only in CtxPredicate. These builtins have boolean exit-code semantics
//     and MUST NOT appear in step bodies — they don't produce side-effect output.
//   - KindInternal: only in CtxInternal (engine-synthetic phases or daemon-generated commands).
func kindAllowed(k spec.Kind, ctx spec.CallerContext) bool {
	switch k {
	case spec.KindAction:
		return ctx == spec.CtxUserYAML || ctx == spec.CtxPredicate
	case spec.KindPredicate:
		return ctx == spec.CtxPredicate
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
		return fmt.Sprintf("builtin %q is a predicate and can only be used in check: positions or validate.yml cmd: entries, not as a step body action", name)
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

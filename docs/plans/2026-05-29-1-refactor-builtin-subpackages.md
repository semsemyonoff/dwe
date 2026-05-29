# Refactor `internal/core/execution/builtin` into Subpackages

## Overview

Reduce 20 flat .go files (+17 test files) in `internal/core/execution/builtin` by extracting domain-aligned subpackages. The current package mixes registry plumbing, 19 builtin implementations across 4 domains, and shared helpers in a single namespace.

**Problem**: Adding a new docker/daemon builtin has no obvious home; navigating the package requires scrolling through unrelated files; the 380-line `builtin.go` mixes interface + helpers + registry + public API.

**Goal**: After refactor, root keeps only 2 cross-cutting predicate builtins (`shell`, `tcp_reachable`) + the public API. Five domain subpackages each own a coherent cluster:
- `spec/` — shared contract (interface, Kind, ExecContext, helpers); cycle-breaker
- `containers/` — 8 docker/daemon builtins (incl. volumes)
- `services/` — 3 service_* builtins
- `fs/` — 2 filesystem builtins (`file_exists`, `remove_paths`)
- `env/` — 2 env-related predicates (`env_keys_present`, `executable_in_path`)
- `interaction/` — 2 user-prompt builtins (`confirm`, `message`)

**Behavior changes**: none. All 19 registered builtin names (`docker_daemon_start`, `service_configs_copy`, etc.) preserved verbatim. Kind classification unchanged. IsInteractive set unchanged.

## Context (from discovery)

- **Package layout**: 20 .go files all flat under `internal/core/execution/builtin/`. Naming convention already groups by domain (`docker_*`, `service_*`, `containers_*`).
- **Registry shape**: literal `map[string]registryEntry` in `builtin.go` mapping name → (implementation, Kind).
- **Internal coupling** (verified via plan-review deep inspection): each builtin's Validate/Describe/Run is self-contained, BUT there are 3 utility-helper cross-references:
  - `env_keys_present.go:54` calls `ParseEnvEntries` defined in `configs_copy.go` (creates an env → services edge after split — must be resolved in Task 1)
  - `daemons_reap.go:74` uses `defaultStopTimeout` from `daemon_stop.go:19` (intra-`containers/` after split — safe)
  - `daemon_logs.go:52` calls `isDaemonRunning` from `daemon_start.go:210` (intra-`containers/` after split — safe)
- **External callers**: `internal/core/execution/pipeline`, `internal/core/validate/checks`, `internal/core/workflow/lifecycle` (via auto_reap). All use root-package types (`builtin.Builtin`, `builtin.ExecContext`, `builtin.CtxUserYAML`) → preserved via type aliases.
- **Pre-release project**: no backwards-compat constraints internally; YAML-facing builtin names ARE user-facing → preserve exactly.

## Development Approach

- **Testing approach**: Regular (code first, tests follow with the code). 17 existing _test.go files act as safety net — they move alongside their implementations.
- **No behavior changes**: this is a structural refactor. Every test that passed before MUST pass after.
- **Per-task atomicity**: each task ends with `make test` green before the next begins.
- Small, focused changes — extract one subpkg per task.

## Testing Strategy

- **Unit tests**: existing 17 _test.go files cover behavior. Each test moves alongside its implementation file. No new tests required unless task adds a behavior (none do).
- **e2e tests**: not applicable — no UI; CLI integration covered by existing test suite.
- **Smoke test**: after each task, `make test` and `make lint` must be green.
- **Manual verification (final task)**: build `bin/devbox`, run a deploy in a fixture project under `next/<project>/` to confirm runtime behavior unchanged.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## Solution Overview

**Architecture**: spec → 5 domain subpackages → root.

- `spec/` is a leaf package: defines `Builtin` interface, `Kind`, `CallerContext`, `ExecContext`, `Entry`, plus the exported helpers (`GetStringParam` etc.).
- `containers/`, `services/`, `fs/`, `env/`, `interaction/` each import `spec/` and expose `func Builtins() map[string]spec.Entry`.
- Root `builtin/` imports `spec/` + all 5 domain subpkgs. The registry is built by merging each subpkg's `Builtins()` with the 2 root entries (shell, tcp_reachable).
- Root re-exports `spec` types via aliases so existing callers (`builtin.Builtin`, `builtin.ExecContext`, `builtin.CtxUserYAML`) keep compiling.

**Cycle-break logic**: subpackages import `spec/` because their `Run` methods take `spec.ExecContext` in the signature and their `Builtins()` providers construct `spec.Entry{Impl: X{}, Kind: ...}` literals. Root `builtin/` imports `spec/` + all 5 domain subpkgs to assemble the registry. Subpackages have no inter-dependencies, with one shared-helper exception: the env-file parsers (`ParseEnvEntries` etc.) live in `spec/envfile.go` because both `services/` and `env/` need them.

**Why `spec.Entry` exports its fields**: subpackages need `spec.Entry{Impl: ..., Kind: ...}` struct-literal syntax to populate their `Builtins()` provider, which requires exported field names. The root registry map itself stays unexported; external callers go through `Get`/`Run`/`Validate`/`Describe`. No code reads `Entry` fields directly outside the assembly point.

**Type renaming**: every implementation struct gets an exported name when moved (`daemonStartBuiltin` → `containers.DaemonStart`, `confirmBuiltin` → `interaction.Confirm`, `fileExistsBuiltin` → `fs.FileExists`). Inside `spec.Entry` they appear by their new exported type. Doc comments updated to lead with new type name (revive enforces this).

**Why shell and tcp_reachable stay in root**: both are generic single-purpose predicates with no natural domain (shell isn't fs/env/net/interaction; tcp_reachable is the only net predicate — a 1-file `net/` subpkg adds noise). Keeping them at root avoids two singleton subpackages.

**Why volumes goes in containers/**: `docker_remove_project_volumes` operates on Docker-managed volumes — conceptually within the docker resource family alongside daemons and containers. A standalone `volumes/` subpkg with 1 file would be pure noise.

## Technical Details

### `spec.Entry` shape
```go
package spec

type Entry struct {
    Impl Builtin
    Kind Kind
}
```

### Root registry assembly
```go
package builtin

import (
    "devbox-cli/internal/core/execution/builtin/containers"
    "devbox-cli/internal/core/execution/builtin/env"
    "devbox-cli/internal/core/execution/builtin/fs"
    "devbox-cli/internal/core/execution/builtin/interaction"
    "devbox-cli/internal/core/execution/builtin/services"
    "devbox-cli/internal/core/execution/builtin/spec"
)

type Builtin = spec.Builtin
type Kind = spec.Kind
type CallerContext = spec.CallerContext
type ExecContext = spec.ExecContext

const (
    KindAction    = spec.KindAction
    KindPredicate = spec.KindPredicate
    KindInternal  = spec.KindInternal
)

const (
    CtxUserYAML  = spec.CtxUserYAML
    CtxPredicate = spec.CtxPredicate
    CtxInternal  = spec.CtxInternal
)

var registry = buildRegistry()

func buildRegistry() map[string]spec.Entry {
    r := map[string]spec.Entry{
        "shell":         {Shell{},         spec.KindPredicate},
        "tcp_reachable": {TCPReachable{},  spec.KindPredicate},
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
```

The duplicate-name panic preserves an invariant the current single-literal map enforced via Go compile-error. Paired with `TestNoDuplicateRegistryNames` in Task 8 it's cheap insurance against accidental future collisions when a name moves between subpackages.

### Subpackage provider shapes

**containers** (8 entries — includes `docker_remove_project_volumes`):
```go
package containers

import "devbox-cli/internal/core/execution/builtin/spec"

func Builtins() map[string]spec.Entry {
    return map[string]spec.Entry{
        "docker_daemon_start":           {DaemonStart{},          spec.KindInternal},
        "docker_daemon_logs":            {DaemonLogs{},           spec.KindInternal},
        "docker_daemon_stop":            {DaemonStop{},           spec.KindInternal},
        "docker_stop_remove_container":  {StopRemoveContainer{},  spec.KindInternal},
        "daemons_reap":                  {DaemonsReap{},          spec.KindInternal},
        "containers_running":            {ContainersRunning{},    spec.KindPredicate},
        "docker_wait_healthy":           {WaitHealthy{},          spec.KindAction},
        "docker_remove_project_volumes": {RemoveProjectVolumes{}, spec.KindAction},
    }
}
```

**services** (3 entries), **fs** (2 entries: `file_exists`, `remove_paths`), **env** (2 entries: `env_keys_present`, `executable_in_path`), **interaction** (2 entries: `confirm`, `message`) follow the same shape.

### Type rename mapping

| Old (unexported) | New | Subpkg |
|---|---|---|
| `shellBuiltin` | `Shell` | root |
| `tcpReachableBuiltin` | `TCPReachable` | root |
| `daemonStartBuiltin` | `DaemonStart` | containers |
| `daemonLogsBuiltin` | `DaemonLogs` | containers |
| `daemonStopBuiltin` | `DaemonStop` | containers |
| `daemonsReapBuiltin` | `DaemonsReap` | containers |
| `containersRunningBuiltin` | `ContainersRunning` | containers |
| `dockerStopRemoveContainerBuiltin` | `StopRemoveContainer` | containers |
| `dockerWaitHealthyBuiltin` | `WaitHealthy` | containers |
| `dockerRemoveProjectVolumesBuiltin` | `RemoveProjectVolumes` | containers |
| `serviceConfigsCopyBuiltin` | `ConfigsCopy` | services |
| `serviceConfigsCheckBuiltin` | `ConfigsCheck` | services |
| `serviceDirsEnsureBuiltin` | `DirsEnsure` | services |
| `fileExistsBuiltin` | `FileExists` | fs |
| `removePathsBuiltin` | `RemovePaths` | fs |
| `envKeysPresentBuiltin` | `KeysPresent` | env |
| `executableInPathBuiltin` | `ExecutableInPath` | env |
| `confirmBuiltin` | `Confirm` | interaction |
| `messageBuiltin` | `Message` | interaction |

Stutter avoidance: `env.KeysPresent` (not `env.EnvKeysPresent`); `containers.RemoveProjectVolumes` (not `containers.DockerRemoveProjectVolumes` — `Docker` would re-state the obvious context).

### File renames

| Old | New | Notes |
|---|---|---|
| `containers_stop_remove.go` | `containers/stop_remove.go` | drop `containers_` prefix |
| `env_keys_present.go` | `env/keys_present.go` | drop `env_` prefix |
| `volumes.go` | `containers/volumes.go` | moved into containers/ |
| `paths.go` | `fs/paths.go` | semantic name preserved |

### Receivers + doc comments (Go convention)

When each `*Builtin` struct is renamed:
- Method receivers update to use the type's initial letter as convention: `func (b daemonStartBuiltin) Run(...)` → `func (d DaemonStart) Run(...)`. Single-letter receivers are idiomatic in Go.
- Doc comments lead with the new type name: `// daemonStartBuiltin runs ...` → `// DaemonStart runs ...`. `revive` enforces this (`exported` rule).
- `Builtins()` provider function in each subpkg gets a doc: `// Builtins returns the <domain> builtin entries keyed by their registered name.`

## What Goes Where

- **Implementation Steps**: all in this codebase (file moves, type renames, registry assembly).
- **Post-Completion**: manual smoke test in a fixture project to verify runtime; no external system changes.

## Implementation Steps

### Task 1: Create `spec/` subpackage with interface, helpers, and shared env-file utilities

**Files:**
- Create: `internal/core/execution/builtin/spec/spec.go`
- Create: `internal/core/execution/builtin/spec/helpers.go`
- Create: `internal/core/execution/builtin/spec/envfile.go` (NEW: shared env-file parsing — resolves the env→services coupling)
- Create: `internal/core/execution/builtin/spec/helpers_test.go`
- Modify: `internal/core/execution/builtin/configs_copy.go` (now sources `ParseEnvEntries` etc. from spec)
- Modify: `internal/core/execution/builtin/env_keys_present.go` (now sources `ParseEnvEntries` from spec)
- Modify: `internal/core/execution/builtin/env_keys_present_test.go` (test calls move to `spec.ParseEnvEntries`)

- [x] create `spec/spec.go` with `Builtin` interface, `Kind`/`CallerContext` enums, `ExecContext` struct, `Entry` struct — copied verbatim from current `builtin.go` (only the package declaration and import paths change)
- [x] create `spec/helpers.go` with `GetStringParam`, `GetBoolParam`, `GetStringSlice`, `GetStringMap`, `GetMapAny`, `GetDurationParam`, `SortedKeys` — exported names (helpers cross package boundary now)
- [x] create `spec/envfile.go` with the env-file parsers currently in `configs_copy.go`: exported `ParseEnvEntries`, `ParseEnvKeys`, `EnvLineKey` + their private siblings (`parseEnvKeys`, `envLineKey`). `updateEnvFile` stays with `configs_copy.go` (services-internal — only callsite is `configs_copy.go:114`; no cross-cluster use). Reason: `env_keys_present.go` (going to `env/`) reuses `ParseEnvEntries` from `configs_copy.go` (going to `services/`). Putting the parsers in `spec/` lets both `services/` and `env/` import them without an env→services edge.
- [x] **co-locate the 6 existing env-parser tests** with their implementation: move `TestEnvLineKey_*` and `TestParseEnvKeys_*` (6 subtests, `configs_copy_test.go:17-61`) to `spec/envfile_test.go`. Without this, Task 4 would carry tests-for-spec-code into `services/`. Apply same logic that put `TestParseEnvEntries` in `spec/envfile_test.go`.
- [x] update `configs_copy.go` to call `spec.ParseEnvEntries` etc. (it stays in root for now; moves to `services/` in Task 4 — the change here is the helper rename only)
- [x] update `env_keys_present.go` similarly
- [x] update `env_keys_present_test.go` (test calls `ParseEnvEntries` on line 25 — update to `spec.ParseEnvEntries`)
- [x] move only `TestGetStringSlice_*` (the 8 subtests at `builtin_test.go:228+`) to `spec/helpers_test.go` — they're the only dedicated helper tests today. Registry/Get/Run/Validate tests stay in root. Other helpers (`getStringParam`, `getBoolParam`, `getDurationParam`, `getStringMap`, `getMapAny`, `sortedKeys`) have no dedicated tests today — acceptable.
- [x] run `make test` — must pass before Task 2

### Task 2: Reshape root `builtin.go` to use `spec` types + helper rename across ALL remaining root files

**Files (all 14 root builtin files need helper-rename in this task, because Task 2 deletes the root-level helpers):**
- Modify: `internal/core/execution/builtin/builtin.go`
- Modify: `internal/core/execution/builtin/builtin_test.go` (`TestKindCategorization` reads `entry.kind` — must become `entry.Kind`)
- Modify: all root .go builtin files: `confirm.go`, `message.go`, `shell.go`, `volumes.go`, `file_exists.go`, `paths.go`, `env_keys_present.go`, `executable_in_path.go`, `tcp_reachable.go`, `configs_copy.go`, `configs_check.go`, `dirs_ensure.go`, `daemon_start.go`, `daemon_logs.go`, `daemon_stop.go`, `daemons_reap.go`, `containers_running.go`, `containers_stop_remove.go`, `wait_healthy.go` (every file that uses `getStringParam`, `getBoolParam`, etc., or references `ExecContext`)
- Modify: corresponding `*_test.go` for shell + tcp_reachable (the only 2 root-renamed builtins in this task)

- [x] in `builtin.go`: delete the interface/Kind/CallerContext/ExecContext definitions (now in spec); delete the helper functions (now in spec); add type aliases (`type Builtin = spec.Builtin`, etc.) and const aliases (`const KindAction = spec.KindAction`, etc.)
- [x] in `builtin.go`: change registry value type from local `registryEntry` to `spec.Entry`; update `Get`, `Run`, `Validate`, `Describe` to use `spec.Entry` access (field names `Impl`/`Kind`)
- [x] in `builtin.go`: rename `kindAllowed`, `kindMismatchHint`, `knownNames` to accept `spec.Kind` / `spec.CallerContext` parameters; keep them as private helpers in root
- [x] in `builtin.go` buildRegistry: duplicate-name guard deferred to Task 3+ when subpackages start contributing entries (the root literal map is still a single Go map literal so duplicates remain a compile-error today).
- [x] in `builtin_test.go:151`: `TestKindCategorization` reads `entry.kind` (lowercase) from the registry map — update to `entry.Kind` (exported per `spec.Entry`)
- [x] rename `shellBuiltin` → `Shell` and `tcpReachableBuiltin` → `TCPReachable`; update receivers (idiomatic single-letter: `s Shell`, `t TCPReachable`); update doc comments to lead with new type name
- [x] **mandatory: update EVERY remaining root .go file's helper call sites**: `getStringParam` → `spec.GetStringParam`, `getBoolParam` → `spec.GetBoolParam`, `getStringSlice` → `spec.GetStringSlice`, `getStringMap` → `spec.GetStringMap`, `getMapAny` → `spec.GetMapAny`, `getDurationParam` → `spec.GetDurationParam`, `sortedKeys` → `spec.SortedKeys`. This includes files that move to subpkgs in Tasks 3-7 — they get renamed here so root compiles after Task 2's helper deletion. Each subpkg task then just re-changes `spec.GetStringParam` → `spec.GetStringParam` (no-op since already qualified). Do NOT defer this to extraction tasks — Task 2 deletes the root helpers and every caller must be updated synchronously.
- [x] update every root builtin file's reference to `ExecContext` → `spec.ExecContext` (same scope as above)
- [x] update `*_test.go` for shell + tcp_reachable to use new exported type names (`&Shell{}`, `&TCPReachable{}`)
- [x] run `make test` — must pass before Task 3
- [x] run `make lint` — must pass before Task 3

### Task 3: Extract `containers/` subpackage (8 docker builtins incl. volumes)

**Files:**
- Create: `internal/core/execution/builtin/containers/containers.go`
- Move + modify: `daemon_start.go` → `containers/daemon_start.go`
- Move + modify: `daemon_logs.go` → `containers/daemon_logs.go`
- Move + modify: `daemon_stop.go` → `containers/daemon_stop.go`
- Move + modify: `daemons_reap.go` → `containers/daemons_reap.go`
- Move + modify: `containers_running.go` → `containers/containers_running.go`
- Move + modify: `containers_stop_remove.go` → `containers/stop_remove.go` (file renamed)
- Move + modify: `wait_healthy.go` → `containers/wait_healthy.go`
- Move + modify: `volumes.go` → `containers/volumes.go` (`docker_remove_project_volumes` belongs to Docker resource family)
- Move + modify: `daemon_test.go`, `daemons_reap_test.go`, `containers_stop_remove_test.go`, `wait_healthy_test.go`, `volumes_test.go` → `containers/`
- Modify: `internal/core/execution/builtin/builtin.go` (compose registry from `containers.Builtins()`)

- [x] create `containers/containers.go` with `func Builtins() map[string]spec.Entry` returning the 8 docker entries (table above) + a leading doc comment
- [x] move 8 implementation files to `containers/` directory; change `package builtin` → `package containers`; add `import "devbox-cli/internal/core/execution/builtin/spec"`
- [x] rename types in each moved file: `daemonStartBuiltin` → `DaemonStart`, `dockerRemoveProjectVolumesBuiltin` → `RemoveProjectVolumes`, etc. (full table above)
- [x] update receivers (idiomatic short letters matching new type initials: `d DaemonStart`, `v RemoveProjectVolumes`, etc.)
- [x] update doc comments on each renamed type and exported method (revive `exported` rule enforces "comment must start with name")
- [x] update receiver method signatures: `ectx ExecContext` → `ectx spec.ExecContext`
- [x] update helper calls inside moved files: `getStringParam` → `spec.GetStringParam`, etc.
- [x] rename file `containers_stop_remove.go` → `stop_remove.go` (the type inside is `StopRemoveContainer`)
- [x] move corresponding `*_test.go` files; change package; update type instantiations (`&DaemonStart{}` etc.); update helper imports
- [x] in root `builtin.go`: append `for k, v := range containers.Builtins() { r[k] = v }` in `buildRegistry()`; import `containers` package
- [x] run `make test ./internal/core/execution/builtin/...` — must pass before Task 4
- [x] run `make lint` — must pass before Task 4

### Task 4: Extract `services/` subpackage (3 service_* builtins)

**Files:**
- Create: `internal/core/execution/builtin/services/services.go`
- Move + modify: `configs_copy.go`, `configs_check.go`, `dirs_ensure.go` → `services/`
- Move + modify: `configs_copy_test.go`, `configs_check_test.go`, `dirs_ensure_test.go` → `services/`
- Modify: `internal/core/execution/builtin/builtin.go`

- [x] create `services/services.go` with `func Builtins() map[string]spec.Entry` returning the 3 service entries + leading doc comment
- [x] move 3 implementation files; change package decl; add spec import; rename types (`serviceConfigsCopyBuiltin` → `ConfigsCopy`, etc.)
- [x] update receivers to short single-letter (`c ConfigsCopy`, etc.) + doc comments leading with new type name
- [x] update helper/ExecContext references to `spec.*`
- [x] move 3 test files; update package and type instantiations
- [x] in root `builtin.go`: add `for k, v := range services.Builtins() { r[k] = v }`; import services package
- [x] run `make test ./internal/core/execution/builtin/...` — must pass before Task 5
- [x] run `make lint` — must pass before Task 5

### Task 5: Extract `fs/` subpackage (`file_exists`, `remove_paths`)

**Files:**
- Create: `internal/core/execution/builtin/fs/fs.go`
- Move + modify: `file_exists.go` → `fs/file_exists.go`
- Move + modify: `paths.go` → `fs/paths.go`
- Move + modify: `file_exists_test.go`, `paths_test.go` → `fs/`
- Modify: `internal/core/execution/builtin/builtin.go`

- [x] create `fs/fs.go` with `func Builtins() map[string]spec.Entry` returning `{"file_exists": {FileExists{}, spec.KindPredicate}, "remove_paths": {RemovePaths{}, spec.KindAction}}` + doc comment
- [x] move 2 implementation files; change package decl; add spec import; rename types (`fileExistsBuiltin` → `FileExists`, `removePathsBuiltin` → `RemovePaths`)
- [x] update receivers + doc comments
- [x] update helper/ExecContext references to `spec.*`
- [x] move 2 test files; update package and instantiations
- [x] in root `builtin.go`: add `for k, v := range fs.Builtins() { r[k] = v }`; import fs package
- [x] run `make test ./internal/core/execution/builtin/...` — must pass before Task 6
- [x] run `make lint` — must pass before Task 6

### Task 6: Extract `env/` subpackage (`env_keys_present`, `executable_in_path`)

**Files:**
- Create: `internal/core/execution/builtin/env/env.go`
- Move + modify: `env_keys_present.go` → `env/keys_present.go` (drop `env_` prefix — directory carries it)
- Move + modify: `executable_in_path.go` → `env/executable_in_path.go`
- Move + modify: `env_keys_present_test.go` → `env/keys_present_test.go`
- Move + modify: `executable_in_path_test.go` → `env/`
- Modify: `internal/core/execution/builtin/builtin.go`

- [x] create `env/env.go` with `func Builtins() map[string]spec.Entry` returning `{"env_keys_present": {KeysPresent{}, spec.KindPredicate}, "executable_in_path": {ExecutableInPath{}, spec.KindPredicate}}` + doc comment
- [x] move 2 implementation files; rename `env_keys_present.go` → `keys_present.go`; change package decl; add spec import
- [x] rename types: `envKeysPresentBuiltin` → `KeysPresent` (stutter avoidance — package name `env` carries the prefix), `executableInPathBuiltin` → `ExecutableInPath`
- [x] update receivers + doc comments
- [x] update helper/ExecContext references to `spec.*`
- [x] move 2 test files; update package and instantiations; rename test file alongside its impl
- [x] in root `builtin.go`: add `for k, v := range env.Builtins() { r[k] = v }`; import env package
- [x] run `make test ./internal/core/execution/builtin/...` — must pass before Task 7
- [x] run `make lint` — must pass before Task 7

### Task 7: Extract `interaction/` subpackage (`confirm`, `message`)

**Files:**
- Create: `internal/core/execution/builtin/interaction/interaction.go`
- Move + modify: `confirm.go` → `interaction/confirm.go`
- Move + modify: `message.go` → `interaction/message.go`
- Move + modify: `confirm_test.go`, `message_test.go` → `interaction/`
- Modify: `internal/core/execution/builtin/builtin.go`

- [x] create `interaction/interaction.go` with `func Builtins() map[string]spec.Entry` returning `{"confirm": {Confirm{}, spec.KindAction}, "message": {Message{}, spec.KindAction}}` + doc comment
- [x] move 2 implementation files; change package decl; add spec import
- [x] rename types: `confirmBuiltin` → `Confirm`, `messageBuiltin` → `Message`
- [x] update receivers (`c Confirm`, `m Message`) + doc comments
- [x] update helper/ExecContext references to `spec.*`
- [x] move 2 test files; update package and instantiations
- [x] in root `builtin.go`: add `for k, v := range interaction.Builtins() { r[k] = v }`; import interaction package
- [x] update `interactiveBuiltins` map in root `builtin.go` if it references the now-moved `confirm` builtin — only the registered NAME is referenced (`"confirm"`), which is unchanged, so the map itself stays as-is
- [x] run `make test ./internal/core/execution/builtin/...` — must pass before Task 8
- [x] run `make lint` — must pass before Task 8

### Task 8: Verify external callers and integration

**Files:**
- Modify: `internal/core/execution/builtin/builtin_test.go` (extend `TestKindCategorization`, add new tests)
- Read-only verification of: `internal/core/execution/pipeline/`, `internal/core/validate/checks/`, `internal/core/workflow/lifecycle/`

- [ ] grep for `builtin.Builtin` / `builtin.ExecContext` / `builtin.CtxUserYAML` / `builtin.Get` / `builtin.Run` / `builtin.Validate` / `builtin.Describe` / `builtin.IsInteractive` across `internal/` — verify all callers still compile with the type aliases
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`
- [ ] verify all 19 builtin names registered: add a one-shot test `TestRegistryHasAllNames` in root `builtin_test.go` that asserts `len(registry) == 19` and each expected name is present
- [ ] verify IsInteractive still returns true for `confirm` and `docker_daemon_logs`, false for others (extend or add `TestIsInteractive`)
- [ ] **extend existing `TestKindCategorization` to cover all 19 names** — the current test covers only 18 entries (it is missing `docker_stop_remove_container`, which should be `KindInternal`). Update the test data + header comment to "19-entry registry categorization". The refactor is the right moment to close this pre-existing gap surfaced by Task 8.
- [ ] add `TestNoDuplicateRegistryNames` in root `builtin_test.go`: call each subpkg's `Builtins()` directly, build a `[]map[string]spec.Entry` slice, and assert pairwise key disjointness. Together with the panic guard in `buildRegistry()` this prevents accidental future collisions.

### Task 9: Build verification + manual smoke test

- [ ] run `make build` — produces `bin/devbox`
- [ ] in a fixture project (e.g. `next/<some-project>/`), run `bin/devbox deploy run --dry-run` (or comparable) to confirm pipeline executor still resolves builtins
- [ ] run `bin/devbox stop` to verify `_auto_reap_daemons` synthetic phase still finds `daemons_reap`
- [ ] check `make lint` final pass
- [ ] check `make test-race` if not already in `make test`

### Task 10: Update documentation + finalize

**Files:**
- Modify: `docs/internals/packages.md` (per-package section for builtin)
- Modify: `AGENTS.md` / `CLAUDE.md` if "Key Patterns" mentions builtin internals
- Move: this plan file → `docs/plans/completed/`

- [ ] update `docs/internals/packages.md` section on `internal/core/execution/builtin/` to describe spec + 5 domain subpackages (containers, services, fs, env, interaction) + root remnants (shell, tcp_reachable)
- [ ] check `AGENTS.md` Key Patterns for any references to internal builtin types or registry shape that need updating
- [ ] verify all checkboxes above are `[x]`
- [ ] move plan file: `mkdir -p docs/plans/completed && mv docs/plans/2026-05-29-1-refactor-builtin-subpackages.md docs/plans/completed/`

## Post-Completion

**Manual verification**:
- Pull a recent existing devbox project (e.g. one in the monorepo) and run `bin/devbox` (no args) to confirm root command + interactive builtins (confirm) still work.
- Run `bin/devbox deploy run` in a project that uses `service_configs_copy` builtin to confirm the copy step still functions.

**External system updates**: none — this is a pure internal refactor with no API surface change for consumers of the binary.

**Follow-up plans** (separate files):
- `docs/plans/2026-05-29-2-refactor-ui-subpackages.md` — same pattern for `core/ui`
- `docs/plans/2026-05-29-3-refactor-snapshot-subpackages.md` — `meta/` + `archive/` extraction
- `docs/plans/2026-05-29-4-refactor-runtime-subpackages.md` — `spec/` + `runners/*` extraction

# dwe prompt: hide from help, service tag, stack-state icon

## Overview

Refines `dwe prompt` along three axes:

1. **Hide from help listing.** `dwe prompt` is a shell-integration command (consumed by starship et al.), not a user-facing one. Setting `Hidden: true` on the cobra command removes it from `dwe --help` output and from tab completion. `dwe prompt`, `dwe prompt --check`, and `dwe prompt --help` continue to work.
2. **Service tag.** When `cwd` is under `<root>/workspace/services/<name>/...`, render `[<name>]` after the project name. Zero IO — derived from a string-prefix check of `cwd` vs `root`. Output becomes `{▪} project [service] <deploy-icon> <stack-icon>`.
3. **Stack-state icon.** Add `●` running / `◐` partial / `○` stopped next to the existing deploy-state icon. Backed by a hybrid stale-while-revalidate cache at `<root>/.dwe/prompt-cache.yml` (TTL 2 min). On stale cache, `dwe prompt` calls `docker ps -q --filter label=com.docker.compose.project=<name>` with a 150ms hard timeout and rewrites the cache **subject to a no-downgrade rule** (see Solution Overview). Multiple writers feed the cache:
   - Lifecycle commands that bring stack up (`dwe run`, `dwe restart` without args) write `"running"`.
   - `dwe stop` without `--service` and `dwe reset run` (project-wide teardown — runs `docker down`) write `"stopped"`.
   - Scoped commands (`dwe restart [service]`, `dwe deploy run --service`, `dwe stop --service`, `dwe reset run --service`) **invalidate** the cache by removing the file — they touch a single container and don't know aggregate state.
   - `dwe service enable/disable --apply` writes `"running"` after the full `executeTogglePlan` completes (deploy + restart + pending-clear + after-hooks).
   - `dwe deploy run` (no `--service`) **invalidates** the cache (file removed). Rationale: `dwe deploy run` has an "already up-to-date" no-op short-circuit (`deploy.go:623`) which returns nil without touching containers. Writing `"running"` on noop would lie if the user manually stopped containers. Invalidation lets the next prompt refresh (or next `dwe status`) reflect ground truth.
   - `dwe status` opportunistically writes the precisely-aggregated state via `stack.HealthState(h)`.
   - `dwe snapshot restore` and `dwe snapshot rollback` remove the cache file (post-restore state is arbitrary).
   - `dwe prompt`'s own sync refresh **only writes `"running"`**, never `"stopped"`. Rationale: a successful `docker ps` returning zero rows with the wrong label filter (templated `docker.yml.project_name`) is indistinguishable from a real stopped stack. Permitting prompt refresh to write `"stopped"` would (a) downgrade a correct `"running"` left by lifecycle/status, OR (b) write a wrong `"stopped"` to an absent cache (after invalidation). Restricting prompt refresh to `"running"`-only delegates the stopped case exclusively to authoritative writers (lifecycle / status), which know the real compose project name.

   On total failure (no cache + refresh fail) the stack icon is omitted silently.

The two icons are independent: deploy-state reflects journal (`.dwe/deploy/state.yml`), stack-state reflects live Docker. They can disagree (e.g. `deployed ✓` but containers stopped manually → `✓ ○`).

## Context (from discovery)

- **Project**: Go CLI `dwe` (Dev Workspace Engine). Hot-path command `dwe prompt` is dispatched before cobra in `cmd/dwe/main.go:25-28`.
- **`shared/prompt` package** (`internal/shared/prompt/prompt.go`): standalone — does not import cobra, lipgloss, or core config loaders. Reads only `workspace.yml`, `.dwe/deploy/state.yml`, `workspace/styles.yml` via raw `yaml.Unmarshal`. Constants `sgrReset`, `defaultAccent`, etc. are package-local. `palette` has accent/success/warning/danger; muted is missing. `findRoot` deliberately does not resolve symlinks (documented at line 285).
- **Stack health API** (`internal/core/project/stack/health.go:10-34`): `AggregateHealth(rows []render.ServiceTableRow) Health` returns `HealthStopped | HealthPartial | HealthRunning`. Rows are typically only available inside `core/workflow/lifecycle` or built from a `StatusInput` in `cli/status/` — they are NOT exposed at the `cli/lifecycle/` call sites (which only see `error` from `lifecyclepkg.Run*`).
- **Lifecycle entrypoints**:
  - `internal/cli/lifecycle/run.go` (`dwe run`)
  - `internal/cli/lifecycle/stop.go` (`dwe stop` [optional `--service`])
  - `internal/cli/lifecycle/restart.go` (`dwe restart`)
  - `internal/cli/lifecycle/reset.go` (`dwe reset run` [optional `--service`])
  - `internal/cli/deploy/deploy.go` (`dwe deploy run`)
  - `internal/cli/service/service_toggle.go` (`dwe service enable/disable --apply` → invokes `singleToggleRunDeploy`/`singleToggleRunRestart`)
  - `internal/cli/snapshot/restore.go` (`dwe snapshot restore` + `dwe snapshot rollback` — both invalidate rather than write). `unpack.go` only extracts archives and is not a mutation point.
- **Compose project name resolution** (`internal/core/project/config/docker.go:196-246`): `ResolveComposeProjectName(baseDir, cfg)` is authoritative — reads `workspace/docker.yml`'s `project_name` (often templated as `${project.prefix}_${project.name}`); falls back to `cfg.Project.Name`. `shared/prompt` does NOT load `docker.yml` and uses only `workspace.yml`'s `project.name` — limitation documented below.
- **Visibility hook** (`internal/cli/root.go:295-297`): `allowedWithoutProject` already whitelists `dwe prompt`. `Hidden:true` does not affect that.
- **Test pattern**: package-local `*_test.go`, table-driven, fixtures in `testdata/`. Existing tests at `internal/shared/prompt/prompt_test.go` and `internal/cli/prompt/prompt_test.go`.
- **Service folder contract** (CLAUDE.md): `workspace/services/<name>/` — folder name == map key, no `name:` field.
- **Layering** (CLAUDE.md): `cli/` → `core/` → `shared/`. New `shared/promptcache` MUST be leaf — zero `core/` imports. The mapping from `stack.Health` → cache state string lives in `core/project/stack/promptstate.go` (a pure function returning a string), so `cli/status/` and lifecycle callers translate to a string before invoking `promptcache.Write(root, state string)`.

## Development Approach

- **Testing approach**: Regular (code first, then tests in same task) — matches existing repo pattern (table-driven `*_test.go` next to code).
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` (or focused `go test ./internal/...`) after each change; the `embedded-docs` sync makes `go test ./...` directly unreliable on a fresh tree
- maintain output stability for the `{▪} <name>` prefix — everything after that is optional and may change

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above).
- **e2e tests**: this project has no UI-based e2e framework — CLI integration is covered by existing `*_test.go` constructing commands in-process.
- The lifecycle integration check is satisfied by reading back `.dwe/prompt-cache.yml` after running each command against a temp workspace via `--config`; no Docker is required because the cache write happens unconditionally on the `err == nil` success path (writes are best-effort regardless of Docker state).
- Real `docker ps` invocations in `shared/prompt.refreshStack` use a DI-able function (`dockerPsFunc`) so unit tests can stub them without spawning processes.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Three additive changes layered on top of the existing `shared/prompt` package, plus one new leaf package, one new pure-function file in `core/project/stack`, and eight call-site wirings.

```
cmd/dwe/main.go
   └── shared/prompt.Run                    ─── hot path (unchanged dispatch)
         ├── findRoot()                     ─── existing
         ├── detectService(cwd, root)       ─── NEW (zero IO, string prefix)
         ├── readProjectName(root)          ─── existing
         ├── readStatus(root)               ─── existing (deploy-state)
         ├── readPalette(root)              ─── extended (add muted)
         ├── readStack(root, projectName)   ─── NEW
         │     ├── readCache(path)          ─── try cache, check TTL
         │     ├── refreshStack(...)        ─── sync docker ps, 150ms timeout
         │     └── writeCache(path, state)  ─── atomic tmp+rename, ONLY on refresh.ok
         └── render(...)                    ─── extended (service tag + stack icon)

core/project/stack/promptstate.go
   └── HealthState(h Health) string         ─── NEW pure mapping, no shared/ deps

shared/promptcache/promptcache.go           ─── NEW leaf, no core/ imports
   └── Write(root, state string) error

cli/lifecycle/run.go                        ─── Write("running")
cli/lifecycle/restart.go                    ─── Write("running") | Remove() with [service]
cli/lifecycle/stop.go                       ─── Write("stopped") | Remove() with --service
cli/lifecycle/reset.go                      ─── Write("stopped") project-wide (teardown!) | Remove() with --service
cli/deploy/deploy.go                        ─── Remove() (deploy may no-op; let prompt-refresh or status reflect reality)
cli/service/service_plan.go                 ─── Write("running") at executeTogglePlan end
cli/snapshot/restore.go                     ─── Remove() after restore AND rollback
cli/status/status.go (top-level RunE only)  ─── Write(stack.HealthState(h)) after loadStatusContext, before JSON/TUI/plain branching
```

### Cache writer contract

Different callers know different things. We pick the safest action at each site — write when confident, invalidate when scoped, never lie:

| Site | Knows | Action |
|---|---|---|
| `dwe run` | success means stack up | write `"running"` |
| `dwe restart` (no service arg) | success means stack up | write `"running"` |
| `dwe restart [service]` | only one container affected | **invalidate** (`os.Remove`) |
| `dwe stop` (no `--service`) | success means stack down | write `"stopped"` |
| `dwe stop --service <n>` | only one container affected | **invalidate** |
| `dwe deploy run` (no `--service`) | may no-op (`already up-to-date`) | **invalidate** — let next prompt refresh / status determine reality |
| `dwe deploy run --service <n>` | only one container affected | **invalidate** |
| `dwe reset run` (no `--service`) | runs `docker down` + removes volumes (teardown) | write `"stopped"` |
| `dwe reset run --service <n>` | one container torn down | **invalidate** |
| `dwe service enable/disable --apply` | full `executeTogglePlan` succeeded | write `"running"` (only after the entire plan including pending-clear and after-hooks completes) |
| `dwe status` (top-level) | has aggregated `stack.Health` | write exact `stack.HealthState(h)` (`running`/`partial`/`stopped`) |
| `dwe status <subcommand>` | section-scoped | **no write** |
| `dwe snapshot restore`, `dwe snapshot rollback` | post-restore state is arbitrary | **invalidate** |
| `dwe prompt` (sync refresh) | only knows label-filtered container count | write `"running"` if count > 0; **never write `"stopped"`** (stopped is exclusively authoritative-writer territory) |

This avoids the fiction of re-aggregating health at the `cli/lifecycle/` layer (where service rows aren't available without a full status pipeline replay). Accurate `partial` state is delivered by the next `dwe status` invocation (which opportunistically updates the cache). The no-downgrade rule on prompt refresh prevents a wrong compose-name label filter from poisoning a correct "running" cached by an authoritative writer — see "Cache-poisoning mitigation" below.

### Cache-poisoning mitigation

The prompt refresh can never definitively say "stopped" — a zero-result from `docker ps` is indistinguishable between:
- a genuinely stopped stack, and
- a wrong label filter (templated `docker.yml.project_name` not loaded by prompt).

**Rule**: prompt's `refreshStack` writes the cache iff `refresh.ok=true` AND `state == stackRunning` (count > 0). It never writes `"stopped"`. Lifecycle commands (which know the real compose name) and `dwe status` (which probes per-service via the proper resolver) are the only writers that can ever stamp `"stopped"`.

Trade-offs (all accepted):
- **A real `docker stop` done outside dwe** is not reflected in the prompt until `dwe status` runs (or 2 min later if the cache was running, the icon stays running). The natural recovery: `dwe status`.
- **First prompt invocation of the day on a genuinely stopped project** (no cache, docker daemon healthy) sees `docker ps` count 0 → refresh returns stopped → prompt refresh declines to write → no stack icon shown until a lifecycle command runs. The prompt simply omits the icon during cold-start of a stopped project. This is correct in the templated-compose-name case (the cache would be wrong if we wrote anything) and acceptable in the simple case.
- **`dwe deploy run` no-op** doesn't write running — invalidation forces a refresh which may write running (if containers are actually up) or nothing (if they're not). Correct in all scenarios.

### Key design decisions

- **TTL 2 min**: keeps `docker ps` invocations rare during active shell use.
- **150ms hard timeout**: prompt stays invisible-slow even when Docker daemon is dead. Cache fallback covers warm-cache cases. **`writeCache` only fires when refresh succeeds** — a timeout/error does NOT poison the cache with `stopped`.
- **Concurrent prompts from N terminals**: each shell may invoke `dwe prompt` on every keystroke (starship). When the cache is stale, N shells may concurrently shell out to `docker ps`. Each call costs ~30-80ms warm + ~150ms timeout cold. Not gated. Accepted because: (a) docker daemon handles this load trivially, (b) cache-fresh path is lockless reads, (c) writes are atomic via `os.CreateTemp` + `os.Rename` in the same dir so concurrent writers cannot corrupt the file. The "lock-file single-flight" alternative was considered and rejected as over-engineering — see Post-Completion for the rationale.
- **Hardcoded `"docker"` binary in prompt**: `shared/prompt` deliberately does NOT load `docker.yml`. Users with `binaries.docker: podman` lose the prompt-driven refresh safety net but keep accurate cache writes via lifecycle commands and `dwe status`.
- **Compose project name from `workspace.yml.project.name` in prompt** (NOT `ResolveComposeProjectName`): projects with `workspace/docker.yml`'s `project_name` set (especially templated, e.g. `${project.prefix}_${project.name}`) will have prompt refresh fail to match labels — `docker ps` returns zero containers. Combined with the "prompt refresh never writes stopped" rule, this means prompt refresh simply writes nothing in these projects. The icon stays correct as long as lifecycle commands and `dwe status` (which use the real compose name) write the cache; the 2-min idle refresh is effectively a no-op. Documented limitation.
- **`--check` mode untouched**: exit-only contract — no service tag, no stack probe, no cache writes.
- **Cache writes are best-effort everywhere**: neither prompt's refresh nor lifecycle commands fail when cache I/O fails. The cache is observability, not correctness.
- **Layering**: `shared/promptcache` is leaf (string state in, string state out). `core/project/stack.HealthState(Health) string` does the enum-to-string mapping in `core/`. Callers compose `promptcache.Write(root, stack.HealthState(h))`. No new edge in the dependency graph.

## Technical Details

### Cache file shape (`.dwe/prompt-cache.yml`)

```yaml
updated_at: 2026-06-03T12:34:56Z   # RFC3339 UTC
state: running                      # running | partial | stopped
```

Unknown `state` values or unparseable timestamps are treated as no-cache.

### `core/project/stack/promptstate.go` (new file)

```go
// HealthState maps a Health enum value to the prompt-cache state string
// ("running" | "partial" | "stopped"). Unknown values return "stopped".
// Pure function, zero IO, zero deps beyond the Health type.
func HealthState(h Health) string
```

This lives in `core/` so callers in `cli/status/` and the future `cli/lifecycle/` (if any partial-aware sites are added later) can use it without bringing `core/` into `shared/promptcache`.

### `shared/promptcache` API

```go
package promptcache

const (
    StateRunning = "running"
    StatePartial = "partial"
    StateStopped = "stopped"
)

// Write atomically updates <root>/.dwe/prompt-cache.yml with the given state.
// state MUST be one of StateRunning/StatePartial/StateStopped — invalid values
// return an error and DO NOT write. Best-effort: callers should log errors but
// not propagate them as command failures. Creates <root>/.dwe/ if missing.
func Write(root, state string) error
```

No `core/` imports. No `Health` enum knowledge. Just string validation + atomic write.

### `shared/prompt` additions

```go
type stackKind int
const (
    stackNone stackKind = iota
    stackRunning
    stackPartial
    stackStopped
)

// detectService returns the service folder name when cwd is under
// <root>/workspace/services/<name>[/...], or "" otherwise. Does NOT resolve
// symlinks (mirrors findRoot's policy).
func detectService(cwd, root string) string

// readStack reads the cache; if fresh (<2min) returns it; if stale or missing,
// invokes refreshStack and on success writes the cache. composeProject is
// workspace.yml's project.name (limitations: see Solution Overview).
// `now` is injected for test determinism.
func readStack(root, composeProject string, now time.Time) stackKind

// refreshStack runs docker ps with a 150ms hard timeout. Returns (state, true)
// on success, (stackNone, false) on any failure. NEVER returns stackPartial —
// prompt refresh is binary running/stopped.
func refreshStack(ctx context.Context, composeProject string) (stackKind, bool)
```

`dockerPsFunc` is a package-level variable defaulting to a real `exec.CommandContext` invocation; tests override it.

### Render contract update

Output remains a single line:
```
{▪} <name> [<service>] <deploy-icon> <stack-icon>\n
```

Each of `[<service>]`, `<deploy-icon>`, `<stack-icon>` is independently optional. Spaces only appear between present elements. Backward-compat guarantee: the `{▪} <name>` prefix is stable; downstream parsers that match more than the prefix may need re-tuning.

Stack-icon colors:

| State | Glyph | Color |
|---|---|---|
| running | `●` | `palette.success` |
| partial | `◐` | `palette.warning` |
| stopped | `○` | `palette.muted` (new) |

`palette.muted` default: `#6B7280`. Override via `workspace/styles.yml` `colors.muted`.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): user-visible verification scenarios — confirming the prompt renders correctly in a real shell with starship, and behavior on a stopped Docker daemon and templated compose project names.

## Implementation Steps

### Task 1: Hide `dwe prompt` from help listing

**Files:**
- Modify: `internal/cli/prompt/prompt.go`
- Modify: `internal/cli/prompt/prompt_test.go`

- [x] add `Hidden: true` to the `cobra.Command` literal in `NewCmd`
- [x] keep `Long` description (still visible via `dwe prompt --help`)
- [x] add test asserting `cmd.Hidden == true`
- [x] add test verifying `dwe prompt --help` still emits help (cobra's auto-help still works on hidden commands — `cmd.Help()` returns non-empty output)
- [x] verify `internal/cli/root_test.go:44` test still passes (iterates via `Commands()`, which sees hidden commands)
- [x] run `go test ./internal/cli/prompt/... ./internal/cli/...` — must pass before next task

### Task 2: Add `detectService` and `muted` palette to `shared/prompt`

**Files:**
- Modify: `internal/shared/prompt/prompt.go`
- Modify: `internal/shared/prompt/prompt_test.go`

- [x] add `detectService(cwd, root string) string` — derives service name from string-prefix check (`<root>/workspace/services/<name>` followed by `/` or end-of-path). Returns empty when not under services or exactly at `workspace/services` with no child. Add doc comment noting "does not resolve symlinks (mirrors findRoot)."
- [x] add `defaultMuted = "#6B7280"` constant
- [x] extend `stylesStub.Colors` with `Muted string`
- [x] extend `palette` struct with `muted color` and `readPalette` to resolve it via `resolveColor(stub.Colors.Muted, defaultMuted)`
- [x] update `render` to emit `[<service>]` (sanitized via existing `sanitizeName`) between project name and deploy-icon when service != ""
- [x] thread `service` through `runFromDir` from `detectService(cwd, root)`
- [x] add table-driven `TestDetectService` covering: cwd == root, cwd at `workspace/`, cwd at `workspace/services/`, cwd at `workspace/services/foo`, cwd at `workspace/services/foo/src/api`, cwd not under root (defensive), trailing slashes, name with control chars (still extracted; sanitization happens at render)
- [x] update render tests to cover combinations: with/without service, NO_COLOR on/off, control chars in service name
- [x] run `go test ./internal/shared/prompt/...` — must pass before next task

### Task 3: Add cache reader (no refresh yet) and stack-icon render

**Files:**
- Modify: `internal/shared/prompt/prompt.go`
- Modify: `internal/shared/prompt/prompt_test.go`

- [x] add `stackKind` enum (`stackNone | stackRunning | stackPartial | stackStopped`) with `icon()` and `color(palette)` methods
- [x] add `promptCacheStub` for YAML decode: `updated_at` (`time.Time`), `state` (string)
- [x] add `cacheTTL = 2 * time.Minute` constant
- [x] add `readCache(path string) (state stackKind, updatedAt time.Time, ok bool)` — returns ok=false on I/O error, parse error, or unknown state string
- [x] add `readStack(root, composeProject string, now time.Time) stackKind` that reads cache and returns it if fresh; otherwise returns `stackNone` (refresh added in Task 4)
- [x] update `render` to emit stack-icon when `stack != stackNone`
- [x] thread `stack` through `runFromDir`
- [x] write `TestReadCache_Valid`, `TestReadCache_Missing`, `TestReadCache_BadState`, `TestReadCache_BadYAML`, `TestReadCache_BadTimestamp`
- [x] write `TestReadStack_FreshCache_UsesValue`, `TestReadStack_StaleCache_NoRefresh_Yet` (returns `stackNone` in this task, updated in Task 4)
- [x] write render tests for all stack-icon variants × with/without service × NO_COLOR
- [x] run `go test ./internal/shared/prompt/...` — must pass before next task

### Task 4: Add docker-ps refresh with 150ms timeout

**Files:**
- Modify: `internal/shared/prompt/prompt.go`
- Modify: `internal/shared/prompt/prompt_test.go`

- [ ] add `refreshTimeout = 150 * time.Millisecond` constant
- [ ] add package-level `dockerPsFunc = realDockerPs` for test injection; `realDockerPs(ctx, project) ([]byte, error)` shells out to `docker ps -q --filter label=com.docker.compose.project=<project>`
- [ ] add `refreshStack(ctx, composeProject) (stackKind, bool)` — invokes `dockerPsFunc`, counts non-empty lines, returns `stackRunning` if count > 0 else `stackStopped`; on context timeout or exec error returns `(stackNone, false)`
- [ ] add atomic `writeCache(path string, state stackKind, now time.Time) error` using `os.CreateTemp` in the same dir + `os.Rename`. Skip write when `state == stackNone`. Best-effort: returns error but caller ignores it.
- [ ] update `readStack` to call `refreshStack` when cache is stale or missing. **INVARIANTS**:
  - `writeCache` is invoked ONLY when `refreshStack` returns `ok=true`. On refresh failure: return stale cache value if any, else `stackNone`. Never write `stackNone` to disk.
  - **`refreshStack` write rule**: writes the cache ONLY when refreshed state is `stackRunning`. When refreshed state is `stackStopped` (zero containers matched), the cache is NOT written. Return values for the current render in the zero-result case:
    - stale cache exists → return the stale cached state (treat zero-result as "indistinguishable from wrong label; trust the prior authoritative writer")
    - no prior cache → return `stackNone` (omit the stack icon)
    Stopped writes are reserved exclusively for authoritative writers (lifecycle commands + `dwe status`).
- [ ] update `TestReadStack_StaleCache_NoRefresh_Yet` → `TestReadStack_StaleCache_RefreshOk` (cache rewritten with new state and timestamp)
- [ ] add `TestReadStack_StaleCache_RefreshFail_FallbackToStale`
- [ ] add `TestReadStack_NoCache_RefreshFail_ReturnsNone`
- [ ] add `TestReadStack_NoCache_RefreshFail_DoesNotPoisonCache` (verify no file is created)
- [ ] add `TestReadStack_StaleRunningCache_RefreshReturnsZero_ReturnsStaleNoWrite` (prior cache=running, docker ps returns 0 → no write, returns `stackRunning` from stale cache)
- [ ] add `TestReadStack_StalePartialCache_RefreshReturnsZero_ReturnsStaleNoWrite` (same for partial)
- [ ] add `TestReadStack_NoCache_RefreshReturnsZero_ReturnsNoneNoWrite` (no prior cache, docker ps returns 0 → no file created, returns `stackNone`)
- [ ] add `TestReadStack_NoCache_RefreshReturnsRunning_Writes` (no prior cache, docker ps returns count > 0 → cache file created with state=running)
- [ ] add `TestReadStack_StaleRunningCache_RefreshReturnsRunning_RefreshesTimestamp` (running→running allowed; timestamp updates)
- [ ] add `TestReadStack_StaleStoppedCache_RefreshReturnsRunning_Promotes` (stopped→running allowed)
- [ ] add `TestRefreshStack_TimeoutReturnsNone` (stub `dockerPsFunc` to block on `<-ctx.Done()`)
- [ ] add `TestRefreshStack_OneRunningContainer_ReturnsRunning` (stub returns "abc123\n")
- [ ] add `TestRefreshStack_NoContainers_ReturnsStopped` (stub returns "")
- [ ] add `TestWriteCache_PanicDuringWrite_OriginalFileUntouched` (write then recover from panic; verify the existing cache file content is unchanged — half-written `.tmp` may remain but does not affect correctness)
- [ ] add `TestWriteCache_LeftoverTmp_DoesNotBreakNextWrite` (pre-create a stale `.tmp` and verify the next `writeCache` still succeeds)
- [ ] run `go test ./internal/shared/prompt/...` — must pass before next task

### Task 5: Create `internal/shared/promptcache` package (leaf)

**Files:**
- Create: `internal/shared/promptcache/promptcache.go`
- Create: `internal/shared/promptcache/promptcache_test.go`

- [ ] declare package `promptcache` with string constants `StateRunning`, `StatePartial`, `StateStopped` (no enum type)
- [ ] implement `Write(root, state string) error`:
  - validates state ∈ {`StateRunning`, `StatePartial`, `StateStopped`} — return error otherwise
  - ensures `<root>/.dwe/` exists (`os.MkdirAll` with 0755)
  - encodes `{updated_at: time.Now().UTC().Format(time.RFC3339), state: <state>}` to YAML
  - atomic write: `os.CreateTemp(<root>/.dwe/, "prompt-cache-*.tmp")` + write + close + `os.Rename`
- [ ] implement `Remove(root string) error`:
  - removes `<root>/.dwe/prompt-cache.yml` if it exists
  - returns `nil` on `os.ErrNotExist` (idempotent)
  - returns the underlying error otherwise
  - this is the public invalidation API used by sibling CLI packages (snapshot/restore, lifecycle scoped operations)
- [ ] write `TestWrite_CreatesFile_WithRightShape` (read back YAML and verify both keys)
- [ ] write `TestWrite_CreatesDweDirIfMissing`
- [ ] write `TestWrite_RejectsInvalidState` (e.g. `"unknown"`, empty string)
- [ ] write `TestWrite_Atomic_OnRenameFailure_OriginalUntouched` (pre-seed cache, force tmp rename to fail via permission; verify original content intact)
- [ ] write `TestWrite_ConcurrentSafeAtomicRename` (10 goroutines calling Write simultaneously; verify the file always parses and ends with a valid state)
- [ ] write `TestRemove_DeletesExistingFile`
- [ ] write `TestRemove_IdempotentWhenAbsent`
- [ ] verify **zero `internal/core/*` imports** in `promptcache.go` (`grep -E "internal/core" promptcache.go` must be empty)
- [ ] run `go test ./internal/shared/promptcache/...` — must pass before next task

### Task 6: Add `core/project/stack.HealthState`

**Files:**
- Create: `internal/core/project/stack/promptstate.go`
- Create: `internal/core/project/stack/promptstate_test.go`

- [ ] implement `HealthState(h Health) string` — `HealthRunning→"running"`, `HealthPartial→"partial"`, `HealthStopped→"stopped"`, default→`"stopped"`. Pure function. Uses string literals matching `promptcache.State*` (could also import `shared/promptcache` for the constants, but to keep `core/` independent of `shared/promptcache` we duplicate the three literal strings — they are part of the on-disk schema and trivial)
- [ ] write `TestHealthState_AllMappings` table-driven with all four cases (3 known + 1 unknown)
- [ ] run `go test ./internal/core/project/stack/...` — must pass before next task

### Task 7: Wire cache writes / invalidations into mutating commands

**Files:**
- Modify: `internal/cli/lifecycle/run.go`
- Modify: `internal/cli/lifecycle/stop.go`
- Modify: `internal/cli/lifecycle/restart.go`
- Modify: `internal/cli/lifecycle/reset.go`
- Modify: `internal/cli/deploy/deploy.go`
- Modify: `internal/cli/service/service_plan.go` (hook at the end of `executeTogglePlan`, not in `service_toggle.go`)
- Modify: `internal/cli/snapshot/restore.go` (both `runSnapshotRestore` and `runSnapshotRollback` — they live here, NOT in `unpack.go` which only extracts archives)
- Modify: respective `*_test.go`

Helper placement: invalidation is exposed via `promptcache.Remove(root)` (added in Task 5). Callers invoke `_ = promptcache.Write(root, promptcache.StateRunning)` or `_ = promptcache.Remove(root)` directly — no per-package helper needed (an unexported helper in `internal/cli/lifecycle/` could not be imported by sibling cli packages anyway).

Per-site actions (matching the writer matrix in Solution Overview):

- [ ] `run.go`: after `RunRun` returns nil → `_ = promptcache.Write(root, promptcache.StateRunning)`
- [ ] `restart.go`:
  - project-wide branch (no service arg, line ~91): `_ = promptcache.Write(root, promptcache.StateRunning)`
  - per-service branch (`RestartService` path, line ~119): `_ = promptcache.Remove(root)`
- [ ] `stop.go`:
  - no `--service` (full stack stop): `_ = promptcache.Write(root, promptcache.StateStopped)`
  - `--service <n>`: `_ = promptcache.Remove(root)`
- [ ] `reset.go`:
  - project-wide `resetRunCmd` (post `journal.Remove(statePath)` success): `_ = promptcache.Write(root, promptcache.StateStopped)` (teardown — `docker down` + volume removal)
  - per-service `resetServiceRunCmd`: `_ = promptcache.Remove(root)`
- [ ] `deploy/deploy.go`:
  - `runDeployRun` no `--service`: `_ = promptcache.Remove(root)` (deploy may no-op via "already up-to-date" path at `deploy.go:623`; invalidation lets the next prompt refresh or `dwe status` reflect ground truth)
  - `runDeployRun --service <n>` (line ~162): `_ = promptcache.Remove(root)`
- [ ] `service/service_plan.go`: at the very end of `executeTogglePlan` (after pending-clear AND after-hooks, only when the whole plan returned nil): `_ = promptcache.Write(root, promptcache.StateRunning)`. Do NOT hook into `singleToggleRunDeploy` or `singleToggleRunRestart` — those are sub-steps that can succeed while a later phase fails.
- [ ] `snapshot/restore.go`:
  - on successful `runSnapshotRestore`: `_ = promptcache.Remove(root)`
  - on successful `runSnapshotRollback`: `_ = promptcache.Remove(root)`

Tests:

- [ ] `TestRun_WritesRunning_OnSuccess`
- [ ] `TestStop_NoFlag_WritesStopped`
- [ ] `TestStop_WithService_InvalidatesCache` (pre-seed cache, run stop --service, assert file absent)
- [ ] `TestRestart_NoArg_WritesRunning_OnSuccess`
- [ ] `TestRestart_WithService_InvalidatesCache`
- [ ] `TestReset_ProjectWide_WritesStopped` (not running!)
- [ ] `TestReset_PerService_InvalidatesCache`
- [ ] `TestDeployRun_NoService_InvalidatesCache` (deploy is invalidate-only; verify cache file is absent after deploy regardless of noop/real-work)
- [ ] `TestDeployRun_WithService_InvalidatesCache`
- [ ] `TestExecuteTogglePlan_FullSuccess_WritesRunning` (verify ordering: write happens AFTER pending-clear and after-hooks complete)
- [ ] `TestExecuteTogglePlan_FailureAfterDeploy_DoesNotWrite` (deploy succeeds, after-hook fails → no cache write)
- [ ] `TestSnapshotRestore_InvalidatesCache` (pre-seed cache, run restore, assert file absent)
- [ ] `TestSnapshotRollback_InvalidatesCache`
- [ ] `TestRun_CacheWriteFailure_DoesNotFailCommand` — chmod `.dwe/` to 0500 to simulate; skip on root via `if os.Geteuid() == 0 { t.Skip("requires non-root") }`. Assert command still exits 0.
- [ ] **Integration test** (new file `internal/shared/prompt/integration_test.go`): seed `.dwe/prompt-cache.yml` via `promptcache.Write(root, StateRunning)`, then invoke `prompt.runFromDir(...)` with a stub `dockerPsFunc` (any value — cache is fresh) and assert the rendered output contains the `●` glyph in the correct position. Repeat for `partial`/`stopped`. Repeat with cache invalidated (file removed) + stub `dockerPsFunc` returning `"abc\n"` → assert running icon. This is the end-to-end "writer → reader" loop the prior review flagged as missing.
- [ ] run `go test ./internal/cli/lifecycle/... ./internal/cli/deploy/... ./internal/cli/service/... ./internal/cli/snapshot/... ./internal/shared/prompt/...` — must pass before next task

### Task 8: Wire opportunistic accurate cache writes into top-level `dwe status`

**Files:**
- Modify: `internal/cli/status/status.go`
- Modify: `internal/cli/status/status_test.go`

The top-level `dwe status` RunE (`internal/cli/status/status.go:202+`) branches into JSON, TUI, or plain output. `stack.RenderHealth` is only called on the plain-text branch (line 253). So we cannot hook the cache write next to `RenderHealth` — it would miss JSON and TUI users.

- [ ] in the top-level `RunE`, after `loadStatusContext(flags, ...)` returns successfully and BEFORE the JSON/TUI/plain branching, compute the aggregated Health once:
  ```go
  in := sc.statusInput()
  health := stack.HealthFromStatusInput(in)   // new helper, see below
  _ = promptcache.Write(sc.ProjectRoot, stack.HealthState(health))
  ```
- [ ] refactor `internal/core/project/stack/status.go`:
  - extract the topology-vs-rows selection logic currently inline in `selectHealthIndicator` (status.go:162-177) into a new exported function `HealthFromStatusInput(in StatusInput) Health` in `internal/core/project/stack/health.go`
  - extract the `Health → glyph` formatting (the `switch health` block) into a small unexported `formatHealthIndicator(health Health) string` in `status.go`
  - rewrite `HealthIndicator(in StatusInput) string` to call `formatHealthIndicator(HealthFromStatusInput(in))` — same observable behavior
  - delete the inline aggregation from `selectHealthIndicator` (or remove the function entirely if it has no other callers)
- [ ] add unit tests for `HealthFromStatusInput` mirroring the existing `TestAggregateHealth_*` cases, plus:
  - `TestHealthFromStatusInput_TopoTakesPrecedenceOverRows` (topo has runtime statuses → rows are ignored)
  - `TestHealthFromStatusInput_RowsFallbackWhenTopoEmpty`
  - `TestHealthFromStatusInput_RowsFallbackWhenTopoOnlyDisabled`
- [ ] do NOT add the cache write to per-section status subcommands (`dwe status deploy`, `dwe status apps`, etc.) — only the top-level `dwe status` performs full aggregation
- [ ] do NOT add to `dwe logs` or any other read-only command — `status` is special because it already computes the answer
- [ ] add `TestStatus_TopLevel_PlainPath_WritesAccurateState_Running` (stub `IsRunning` to always-true; run plain branch; assert `.dwe/prompt-cache.yml` content == `running`)
- [ ] add `TestStatus_TopLevel_JsonPath_WritesAccurateState_Partial` (stub partial; run with `--output json`; assert cache content == `partial`)
- [ ] add `TestStatus_TopLevel_PlainPath_WritesStopped`
- [ ] add `TestStatus_SubCommand_DoesNotWriteCache` (run `dwe status deploy`; verify `.dwe/prompt-cache.yml` is unchanged / absent)
- [ ] add `TestStatus_CacheWriteFailure_DoesNotFailCommand` (skip-as-root pattern)
- [ ] run `go test ./internal/cli/status/... ./internal/core/project/stack/...` — must pass before next task

### Task 9: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] verify writer matrix matches the Solution Overview table — spot-check each call site by reading the code and tracing what state is written/removed
- [ ] verify the no-downgrade rule holds: write a test workspace with a running cache, simulate stale + zero-result docker ps, confirm cache is NOT overwritten
- [ ] verify edge cases: cwd outside services/, malformed cache file, dead docker daemon (mocked timeout), NO_COLOR, TERM=dumb, --check bypass, --help bypass
- [ ] verify cross-package contracts: `grep -r "internal/core" internal/shared/promptcache/` returns nothing; `grep -r "internal/shared" internal/core/project/stack/` returns nothing
- [ ] run full test suite: `make test`
- [ ] run lint: `make lint`
- [ ] manually exercise once: `make build` then run `bin/dwe prompt` inside a dwe project; verify output renders correctly in raw form (`bin/dwe prompt | cat -A`) and in a real shell with starship if available
- [ ] manually exercise `bin/dwe prompt --help` (should show help block, not the prompt segment)
- [ ] manually exercise `bin/dwe --help` (should NOT list `prompt` in the command listing)
- [ ] manually exercise `bin/dwe reset run --yes` in a test project: verify the cache shows `stopped` afterward (was `running` before), not `running`

### Task 10: Update documentation

`docs/reference/cli/dwe_prompt.md` is auto-generated by `dwe docs generate` and skips hidden commands unless `--include-hidden` is used. After making `dwe prompt` hidden, the auto-gen will stop emitting it unless we explicitly include hidden commands. The durable hand-maintained doc is `docs/reference/cli/starship.md`, plus the command's `Long` text inside the cobra definition.

**Files:**
- Modify: `internal/cli/prompt/prompt.go` (update the `Long` string to describe new output format and stack/service tag)
- Modify: `docs/reference/cli/starship.md` (hand-maintained — the canonical doc for prompt integration)
- Possibly remove: `docs/reference/cli/dwe_prompt.md` — verify whether `dwe docs generate` will remove it on next regen (it will, since the command is now hidden); accept the removal or pass `--include-hidden` to keep it. Decide at implementation time. The Long text update is the load-bearing one.
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md` (CLAUDE.md is a symlink — do not overwrite the symlink or its target structurally; only append to relevant sections)

- [ ] update the `Long` text in `internal/cli/prompt/prompt.go` to document `{▪} project [service] <deploy> <stack>\n` and the four-segment contract
- [ ] update `docs/reference/cli/starship.md` with:
  - new output format examples (with and without service tag, with and without stack icon)
  - cache: location (`.dwe/prompt-cache.yml`), TTL (2 min), TTL-driven refresh via `docker ps`
  - writer map (lifecycle / status / snapshot / prompt-refresh) with semantics
  - the no-downgrade rule on prompt refresh and what it means for the user
  - known limitations:
    - custom docker binary (`binaries.docker`) bypasses prompt refresh
    - templated compose project name (`docker.yml` `project_name`) bypasses prompt refresh — fresh state only after lifecycle command or `dwe status`
    - manual `docker stop` outside dwe is not detected by prompt refresh (no-downgrade rule) — run `dwe status` to refresh
- [ ] in `docs/internals/packages.md`, update `internal/shared/prompt/` section (cache reads, docker exec, no-downgrade rule) and add `internal/shared/promptcache/` section. Note in `internal/core/project/stack/` the new `HealthState` and `HealthFromStatusInput` functions.
- [ ] in `AGENTS.md` "Critical Patterns" section, add a one-liner about the prompt-cache contract (who writes / invalidates / refreshes; layering)
- [ ] run `make build` to regenerate `internal/core/docs/embedded/` and `content_hashes_gen.go`
- [ ] check whether `docs/reference/cli/dwe_prompt.md` is regenerated or removed; if removed and we want to keep it, run `dwe docs generate --include-hidden` (or document this as the new behavior)
- [ ] verify embedded-docs tests pass: `go test ./internal/core/docs/...`
- [ ] check Russian translation hashes in `docs/i18n/` (commit `13769541` shows the convention for stale-hash refresh — only relevant if `docs/reference/render/` or `docs/internals/packages.md` have RU translations that need re-hashing)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification:**

- Use starship (or equivalent) to render `dwe prompt` output in real terminals. Verify glyph alignment with various terminal fonts (e.g. JetBrains Mono, Iosevka). The muted `○` may look visually different from `●` in some fonts.
- Stop Docker / Colima and observe prompt latency — must remain under ~200ms total wall time even when refresh times out.
- Cold-start scenario: with Docker daemon starting up (typical first prompt of the day), `docker ps` may take 800ms–2s. The 150ms timeout fires, refresh returns `(stackNone, false)`, no cache write, prompt renders without stack icon. Once docker is responsive AND a lifecycle command runs OR `dwe status` is invoked, the cache populates and the icon reappears on the next prompt. Verify this UX path manually.
- Concurrent prompt scenario: open 5–10 terminals in the same project, type quickly. Verify (a) prompt always renders something without visible latency on cache-fresh path, (b) cache file is never corrupt (parse `.dwe/prompt-cache.yml` repeatedly while load-testing — should always be valid YAML).
- Templated compose name scenario: create a test project with `workspace/docker.yml`'s `project_name: "${project.prefix}_${project.name}"`. Run `dwe deploy run` → cache invalidated. Run `dwe status` → cache populated with the correct aggregated state (status uses `ResolveComposeProjectName` and per-container probes). Verify prompt shows the correct icon (`●` when running). Wait 2 min so prompt refresh fires: `docker ps` with the wrong label filter returns zero, refresh.ok=true, refresh.state=stopped → the new "prompt-refresh never writes stopped" rule prevents the cache from being downgraded. Icon stays correct. Verify that `dwe stop` (which uses the same authoritative compose name) correctly writes `"stopped"` to the cache.
- Custom-binary scenario: set `binaries.docker: podman` (or any non-`docker` value). Verify prompt-driven refresh fails silently (no error to stderr, no crash). Lifecycle command writes still work.

**External system updates:**

- None. `dwe prompt` is shell-integration code; no downstream consumers in this repo or in shipped configs depend on the absence of the new optional render elements. The `{▪} <name>` prefix is the only stability guarantee.

**Rejected alternatives** (for future reference):

- **Single-flight lock file for concurrent prompt refresh**: considered to avoid N-fold `docker ps` amplification when N terminals race through stale cache simultaneously. Rejected because (a) docker daemon handles concurrent `docker ps` calls trivially, (b) the lock-file dance adds I/O on every prompt invocation (the common case), (c) atomicity of the cache write is already guaranteed by tmp+rename. If amplification becomes a measurable problem in practice, revisit.
- **Loading `docker.yml` in `shared/prompt`** to resolve compose project name correctly: rejected because the template engine (`${...}` resolution) lives in `core/project/config/` and would balloon `shared/prompt`'s dependency surface significantly for what is meant to be a minimal, fast hot path. The accurate cache values from lifecycle commands and `dwe status` are sufficient during active use; the 2-min idle window for templated-name users is the documented limitation.
- **Threading an `OnSuccess(stack.Health)` callback into `lifecyclepkg.Run*`**: considered to deliver accurate partial-state writes from lifecycle commands. Rejected because (a) it expands the public surface of `core/workflow/lifecycle`, (b) the same accuracy is delivered for free by `dwe status` (any user inspecting state will refresh the cache), (c) deterministic `running`/`stopped` is correct in the overwhelming majority of cases for these commands.

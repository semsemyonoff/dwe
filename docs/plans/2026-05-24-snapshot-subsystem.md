# Snapshot subsystem

## Overview

Add a `devbox snapshot` subsystem that captures the state of a dev environment (databases, indices, service branches, devbox local config) into a named directory under `./snapshots/<name>/`, and can restore or roll back to it. Designed around the workflow "I'm on a feature, a hotfix comes in — save, switch to clean DB, fix, return to feature."

Core principles (from spec §2):

- Core knows nothing about specific data stores — the user defines `create` / `restore` workflows in `devbox/snapshot.yml` that call existing user commands (`db.dump`, `opensearch.snapshot`, etc).
- One snapshot lives unpacked in `./snapshots/<name>/`; `pack` / `unpack` are separate commands for sharing.
- Restore is **drop + restore** — no DB prefixing, no name substitution.
- `baseline` is just an ordinary name (no reserved semantics).
- Restore is soft — runs the `restore` workflow and swaps devbox files; it does **not** invoke `reset` or recreate containers.
- No code generation, no marker files, no schema migrations. Manifest carries no `schema_version` field — per CLAUDE.md project policy.

## Context (from discovery)

Architectural map (verified against `docs/internals/packages.md` + source):

| Concern | Existing pattern to reuse | Files |
|---|---|---|
| Strict-YAML loader | `LoadDeployConfig` / `LoadLifecycleConfig` / `LoadResetConfig` with `KnownFields(true)` | `internal/config/devbox.go` (loadPipelineConfig); add `internal/config/snapshot.go` |
| **Workflow step shape** | `usercommands/model.WorkflowStep` at `types.go:276` — `Command`, `With`, `When`, `Confirm`, `ContinueOnError`, `Parallel` | reuse directly; do **not** mirror `config.DeployStep` (different shape, typed `condition.Condition`) |
| **Workflow executor** | `usercommands.RunCommand(ctx, RunContext)` dispatched on `CommandDef{Type:"workflow"}` runs `model.WorkflowStep`s through `runner_workflow.go` with `when:`/`confirm`/`parallel`/`continue_on_error` already implemented | snapshot workflow becomes a synthetic `*model.CommandDef` constructed at runtime; do **not** route through `pipeline.RunWithOptions` |
| Lock | `lock.Acquire(path)` (POSIX flock) — file is intentionally left on disk after Release; do **not** delete conflict lock files (inode-race invariant, `lock.go:120-125`) | `internal/lock/lock.go` — no API change; multi-lock acquisition is a caller pattern, not a new function |
| Render-context threading | `RenderContext` fields are `Raw`, `Params`, `Context`, `Host`; built fresh in `build_context.go:32,56` and again per sub-step in `runner_workflow.go:202` | add `Snapshot map[string]any` + `SnapshotScope` enum to `tpl.RenderContext`; propagate from parent `rc.Render` at `runner_workflow.go:202` |
| Validate domain | `env.All(cfg)` and `checks.AllForStage(...)` returning `[]validate.Validator`; assembled by `buildRegistry` in `internal/command/validate.go:369` | add `snapshot.All(...)` registered there |
| Cobra group | Top-level groups added in `internal/command/root.go`; deploy/reset as references | add `internal/command/snapshot.go` |
| Completion contract | `ValidArgsFunction` must call `completionConfigPath(flags, cmd)` before any config work (CLAUDE.md "Completion helpers"); return `ShellCompDirectiveNoFileComp` on error | `internal/command/completion.go` provides the helper |
| Atomic write-temp + rename | `journal/state.go` Save: temp file in same dir, chmod, rename | mirror in `internal/snapshot/manifest.go` and `current.go` |
| Path safety for tar | `pathsafe.ContainedRel(absRoot, absChild)` at `pathsafe.go:45` rejects `..`, absolute, and escaping paths | required for `unpack` |
| Notifications | `internal/notify` hookpoints in `command/deploy`, `lifecycle/RunRun`, `usercommands/runtime/RunCommand`; `notify.Op` currently has `OpDeploy`/`OpRun`/`OpCommand` (`types.go:12-19`) | snapshot create/restore/rollback emit notifications at command boundary using **`OpCommand`** with an operation label like `"snapshot:create"` — avoids adding a new `Op` constant and userconfig key just for snapshot. If snapshot notifications grow distinct config knobs later, promote to `OpSnapshot` then. |

Affected packages (new + touched):

- **New**: `internal/config/snapshot.go`, `internal/snapshot/` (paths, manifest, current pointer, name validation, artifact scan, synthetic-command builder, pack/unpack), `internal/validate/snapshot/`, `internal/command/snapshot.go`, `docs/reference/config/snapshot.md`
- **Touched**: `internal/tpl/render_command.go` (add `Snapshot` field + scope enum; route `${snapshot.*}`), `internal/usercommands/runtime/runner_workflow.go` (propagate snapshot fields into sub-step `RenderContext` at line 202), `internal/usercommands/runtime/build_context.go` (propagate snapshot fields when building leaf contexts at lines 32 and 56), `internal/command/root.go` (register group), `internal/command/validate.go` (`buildRegistry` registers snapshot domain), `docs/internals/packages.md` (document new packages and their invariants)

## Development Approach

- **Testing approach**: Regular (code first, then tests). Match the codebase: table-driven `*_test.go` next to code, `testdata/` for fixtures.
- Complete each task fully before moving to the next.
- **Every task includes new/updated tests** — listed as separate checklist items.
- All tests must pass before starting the next task. Run with `-race` in the final verification task.
- Run `make lint` after each task that touches Go code.
- Update this plan if scope shifts mid-implementation.

## Testing Strategy

- **Unit tests**: table-driven for each loader, render, manifest read/write, name validator, scan, archive safety.
- **Integration tests**: end-to-end snapshot create→restore→remove against a temp project fixture under `internal/snapshot/testdata/`, exercising the real `usercommands.RunCommand` with a fake user command (e.g. `type: shell` writing a marker file).
- **Render-context threading test (mandatory)**: a fake user command (`type: shell` — `type: script` does **not** render the script's file contents; only its invocation `workdir`/`env`/`script` path are rendered per `runner_script.go:165`) whose `run:` is `echo "${snapshot.name} ${snapshot.path}"`; the snapshot create flow invokes it and the test asserts the captured stdout matches what the runtime constructed. Equivalent fallback: `type: script` with an env block like `SNAPSHOT_PATH: ${snapshot.path}` and a script body that echoes `$SNAPSHOT_PATH` — both approaches prove the threading, pick `type: shell` for the canonical test because it's tighter.
- **Lock interaction**: integration test acquires `deploy.lock`, then verifies `devbox snapshot create` fails with a clear "deploy is running (pid N)" message (and vice versa: snapshot.lock held → deploy lifecycle blocks).
- **Template scope gate**: validator and tpl-level tests assert `${snapshot.*}` outside snapshot scope is rejected at compile, and `${snapshot.created_at}` in the `create` scope is rejected (it doesn't exist yet at create time).
- **Archive safety**: malicious-tar fixtures exercise rejection of `../escape`, absolute paths, symlink/hardlink/device entries, oversize entries, and file-count overflow.
- **goleak**: `internal/snapshot/snapshot_test.go` uses `goleak.VerifyTestMain` since workflow execution may spawn goroutines for parallel groups.
- No e2e UI tests in this project.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add new tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Update this file if scope shifts.

## Implementation Steps

### Task 1: `snapshot.yml` loader and types

- [x] add `internal/config/snapshot.go` with `SnapshotConfig`:
  - `Dir string` (default `./snapshots`)
  - `RollbackTarget string`
  - `RequireMatchingConfig bool`
  - `Pack { Exclude []string }`
  - `Create`, `Restore`, `Remove` of type `SnapshotWorkflow{ Description string; Steps []model.WorkflowStep; Variants map[string]SnapshotWorkflow }` — **reuse `usercommands/model.WorkflowStep` directly** (not a parallel schema)
  - **drop** `prompt_baseline_on_first_restore` from the spec; per CLAUDE.md "one canonical form" rule, we don't carry declared-but-unimplemented fields
- [x] implement `LoadSnapshotConfig(baseDir string) (*SnapshotConfig, error)` using `yaml.NewDecoder` + `KnownFields(true)`; return `(nil, nil)` cleanly when the file is absent; caller decides whether absence is fatal per subcommand
- [x] validate at load time: variant names match `[a-z0-9][a-z0-9._-]{0,30}`; reject nested `parallel` containers per existing `model.WorkflowStep` rules (which the model already enforces)
- [x] write table-driven tests covering: valid full config, missing file, unknown field rejection, variant naming, malformed YAML, every step shape supported by `model.WorkflowStep` (command, confirm, parallel, when, continue_on_error)
- [x] run `go test ./internal/config/... && make lint` — must pass before task 2

### Task 2: Snapshot core package — paths, manifest, current pointer

- [x] create `internal/snapshot/` with `paths.go` exposing canonical paths: `SnapshotsDir(baseDir, cfg)` → `./snapshots`, `SnapshotDir(baseDir, cfg, name)`, `CurrentPointer(baseDir)` → `.devbox/snapshots/current`, `LockPath(baseDir)` → `.devbox/snapshots/snapshot.lock`, `PreRestoreBackup(baseDir)` → `.devbox/snapshots/.pre-restore-backup`
- [x] add `manifest.go` with `Manifest` struct matching spec §8 minus `schema_version`: `name`, `created_at time.Time`, `description`, `project{name, config_hash}`, `devbox_version`, `variant`, `artifacts []ArtifactInfo{ path string; size int64; sha256 string }`, `devbox_files`, `last_create`, `last_restore`; provide `LoadManifest(path)` and `SaveManifest(path, m)` using write-temp + rename **in the same directory as the final file** (cross-filesystem rename is not atomic — required for POSIX atomicity guarantee)
- [x] inject `now func() time.Time` into the manifest constructor for testability; default to `time.Now`
- [x] add `current.go` with atomic `ReadCurrent(baseDir)`, `WriteCurrent(baseDir, name)`, `ClearCurrent(baseDir)` using the same write-temp + rename pattern
- [x] add `name.go` with `ValidateName(s)` enforcing `[a-z0-9][a-z0-9._-]{0,62}`
- [x] add `scan.go` with `ScanArtifacts(snapDir)` walking the snapshot directory excluding `manifest.yml` and `devbox/`, computing size + sha256 for each file via **streaming `io.Copy(hasher, f)` (never `io.ReadAll`)** so that multi-GB dumps don't OOM; size must be typed `int64`; **reject symlink entries** (`fi.Mode()&os.ModeSymlink != 0`) with a clear error — workflows are not allowed to produce symlinks in the snapshot dir
- [x] write tests for: name validation, manifest round-trip with injected `now`, atomic write (interrupt simulation via temp file leftover handling), artifact scan over a synthetic fixture, symlink rejection, sha256 streaming on a >100 MB synthetic file (skip if running under `-short`)
- [x] run `go test ./internal/snapshot/... && make lint` — must pass before task 3

### Task 3: Lock interaction — acquire-both pattern, no API change

The plan does **not** add `AcquireExclusive`. Per `lock.go:120-125`, lock files are intentionally left on disk after Release with the last writer's PID; PID-only conflict probes false-positive when the previous holder is alive, and deleting conflict files violates the inode-race invariant. Instead we use deterministic multi-lock acquisition with the existing API:

- [x] **place the helper at `internal/lock/project.go`** (not in `internal/snapshot/`) — deploy lifecycle commands must call it too, and putting it in `internal/snapshot/` would force `internal/command/{deploy,run,stop,restart,reset}.go` to import the snapshot package just for lock orchestration. `internal/lock/` already has no upward dependencies, so it's the right home. `lock.go` itself stays unchanged; this is a new file beside it.
- [x] add `lock.AcquireProjectLocks(baseDir string) (releaseFn func(), err error)` that:
  1. Acquires `<baseDir>/.devbox/deploy/deploy.lock` first (alphabetical ordering: `deploy` < `snapshot`)
  2. Acquires `<baseDir>/.devbox/snapshots/snapshot.lock` second
  3. Returns a release function that unlocks in reverse order
  4. On failure to acquire the second lock, releases the first before returning the error
- [x] error path: when either lock is held by another live process, surface the `lock.HeldError` PID with the operation name in the message ("deploy operation in progress: pid N" or "snapshot operation in progress: pid N"); reuse the existing `lockHeldError` shape from `internal/command/deploy.go` (lift it to a shared spot if needed)
- [x] every snapshot mutating subcommand wraps its `RunE` body with `AcquireProjectLocks` + deferred release. **`pack` is included** (it writes `<name>.tar.gz` + `.sha256` and reads snapshot contents that `remove`/`create`/`restore` can be mutating in parallel — without the lock, packing a snapshot mid-`remove` produces a corrupt or truncated archive). Full mutating list: `create`, `restore`, `rollback`, `remove`, **`pack`**, `unpack`. [deferred — snapshot subcommands land in tasks 6–9 and will call `lock.AcquireProjectLocks` directly]
- [x] **deploy lifecycle commands also acquire `snapshot.lock` (in the same deterministic order)** so the mutual exclusion is symmetric — touch `internal/command/deploy.go`, `command/run.go`, `command/stop.go`, `command/restart.go`, `command/reset.go`. **Critical ordering invariant**: replace the existing `lock.Acquire(lockPath)` call at the same source point — *after* `preflightRun` (see `deploy.go:226` then `deploy.go:234`). Never wrap the whole `RunE` before preflight: preflight runs validators and user `type: command` checks that must not hold operation locks (otherwise preflight failures stall the project), and the existing invariant is that the lock is held only across actual mutation. The change is a one-line swap (`lock.Acquire(lockPath)` → `lock.AcquireProjectLocks(workDir)`) and a corresponding adjustment to the defer. [run/stop/restart go through `lifecycle.RunRun`/`RunStop` which had no prior lock; project-lock acquisition added there immediately after `PreflightFunc` so the post-preflight invariant holds.]
- [x] write tests: clean acquire when both locks free; second-lock failure releases the first; deploy-held → snapshot-create fails with PID in message; snapshot-held → deploy-run fails with PID in message; **pack-while-create-held fails with PID in message**; **pack-while-remove-held fails with PID in message** (proves pack can't race remove); sequential snapshot ops queue correctly; preflight in a deploy command runs without holding the locks (set up by intentionally failing preflight and asserting snapshot ops can still run during the failure) [snapshot-side tests (create/pack/remove) covered in tasks 7–9 when those commands exist; lock helper + deploy-side tests landed here]
- [x] run `go test ./internal/lock/... ./internal/snapshot/... ./internal/command/... && make lint` — must pass before task 4

### Task 4: Template namespace `${snapshot.*}` with scope enum

- [x] in `internal/tpl/render_command.go`, extend `RenderContext`:
  - add `Snapshot map[string]any`
  - add `SnapshotScope SnapshotScope` where `SnapshotScope` is an enum: `SnapshotScopeNone` (zero value, default — `${snapshot.*}` is a compile error), `SnapshotScopeCreate` (`name`/`path`/`description`/`variant` valid, `created_at` rejected — doesn't exist yet), `SnapshotScopeRestoreOrRemove` (all keys valid)
- [x] **API shape decision**: keep `CompileVarSyntax(input string) string` unchanged (it has many callers and its current contract is pure-syntactic rewriting with no error). Instead, add a pre-scan helper `validateSnapshotScope(input string, scope SnapshotScope) error` in the same file. `RenderCommand` (at `render_command.go:135`) calls `validateSnapshotScope(expr, data.SnapshotScope)` **before** calling `CompileVarSyntax`. The scan walks `${...}` expressions textually (cheap; same regex `CompileVarSyntax` already uses), and for any `${snapshot.<key>}` it returns an error when the scope rules forbid it. This keeps `CompileVarSyntax` untouched, localizes the scope check at the one entry point where `RenderContext` is available, and gives callers without a `RenderContext` (if any) unchanged behavior.
- [x] inside `CompileVarSyntax`, route `${snapshot.<key>}` to resolve from `.Snapshot` (same template-rewrite pattern as `${param.*}`/`${context.*}`); at this point the scope check has already run in `RenderCommand` so the compile step itself is unconditional
- [x] expose `BuildSnapshotVars(name, path, description, variant string, createdAt time.Time) map[string]any` helper in `internal/snapshot/vars.go` for callers to construct the map; `created_at` is included unconditionally (the scope enum, not the map, gates visibility)
- [x] existing callers in deploy/lifecycle/reset get `SnapshotScope: SnapshotScopeNone` (zero value) — no behavior change required
- [x] write tests: `${snapshot.name}` resolves in Create scope; same expression in None scope produces a compile error; `${snapshot.created_at}` in Create scope produces a compile error; `${snapshot.created_at}` resolves in RestoreOrRemove scope; missing key within an active scope returns empty string (consistent with `${param.*}`)
- [x] run `go test ./internal/tpl/... && make lint` — must pass before task 5

### Task 5: Workflow execution — synthetic `CommandDef` + render-context propagation

The architectural shift: a snapshot workflow IS a `type: workflow` user command, just constructed at runtime from `snapshot.yml` instead of read from `devbox/commands/*.yml`. This reuses the entire existing executor (`usercommands.RunCommand` → `runner_workflow.go`) and avoids a parallel pipeline.

- [x] add `internal/snapshot/exec.go` with `RunWorkflow(ctx context.Context, p ExecParams) error` where:
  ```
  ExecParams {
    Cfg       *config.DevboxConfig
    Registry  *registry.Registry
    BaseDir   string
    Workflow  *config.SnapshotWorkflow
    Vars      map[string]any   // from BuildSnapshotVars
    Scope     tpl.SnapshotScope // Create | RestoreOrRemove
    Stdout    io.Writer
    Stderr    io.Writer
  }
  ```
- [x] inside, build a synthetic `*model.CommandDef` — note that `CommandDef` carries workflow steps **directly** as `Steps []WorkflowStep` (`types.go:461`); there is no `model.Workflow` wrapper type:
  ```
  cmd := &model.CommandDef{
      ID:    "<internal>.snapshot." + scope.String(),
      Type:  "workflow",
      Steps: workflow.Steps,
  }
  if err := cmd.Validate(); err != nil { /* abort with clear error */ }
  ```
- [x] build the top-level `runtime.RunContext` via a new helper `BuildSnapshotRunContext` (mirrors `BuildRunContext` but injects `Snapshot` + `SnapshotScope` into `Render`); `RunContext.Render` becomes the parent that `runner_workflow.go:202` will copy from
- [x] **render-context propagation**: in `runner_workflow.go` where the sub-step `RenderContext` is constructed (line 202), copy `rc.Render.Snapshot` and `rc.Render.SnapshotScope` into the new sub-step `renderCtx`. Without this propagation, `${snapshot.*}` in `with:` values of leaf user commands resolves to empty under the current behavior.
- [x] same propagation in `build_context.go` at the two `RenderContext` constructions (lines 32 and 56) — the early `with:` rendering and the post-resolve render context. Snapshot vars must be available to leaf commands invoked by `usercommands.RunCommand` directly (not just via workflow).
- [x] add `SelectWorkflow(cfg *config.SnapshotConfig, kind string, variant string) (*SnapshotWorkflow, error)` with semantics: empty variant → default block; non-empty variant on create → `Variants[variant]` or error if missing; non-empty variant on restore → `Variants[variant]` with fallback to default block if missing
- [x] write tests with a fake `type: shell` user command whose `run:` is `echo "${snapshot.path}"` (script contents are not rendered — see Testing Strategy note above):
  - end-to-end: `RunWorkflow` with a single-step workflow invokes the fake shell command, assert captured stdout equals the path constructed by the caller
  - same with a 2-level workflow (workflow → sub-step) to prove propagation through `runner_workflow.go:202`
  - `when: file-exists ${snapshot.path}/x` short-circuits when file missing
  - missing variant on create errors; missing variant on restore falls back to default
- [x] run `go test ./internal/snapshot/... ./internal/usercommands/... ./internal/tpl/... && make lint` — must pass before task 6

### Task 6: Cobra command tree — read-only subcommands

- [x] add `internal/command/snapshot.go` with `newSnapshotCmd(flags *rootFlags)` returning the `snapshot` group; register on root in `internal/command/root.go`
- [x] **every subcommand declares its `Args:` validator** — never `len(args)` in `RunE`:
  - `list`, `current`, `rollback` → `cobra.NoArgs`
  - `create`, `restore`, `inspect`, `remove`, `pack`, `unpack` → `cobra.ExactArgs(1)` [deferred: only `list`/`current`/`inspect` exist in this task; mutating subcommands land with their `Args` validators in tasks 7–9]
- [x] implement `devbox snapshot list [--json]` — reads `./snapshots/`, loads each `manifest.yml`, renders a table (name, created_at, size, description); `--json` emits structured output on stdout
- [x] implement `devbox snapshot current` — prints the current pointer (empty if cleared) and the matching manifest summary
- [x] implement `devbox snapshot inspect <name|tar-path> [--json]` — loads manifest, prints name, config-hash with current-config comparison (`DIVERGED` marker), artifacts list, last_create / last_restore; for a tar path, reads manifest from inside the archive without unpacking (safe in-archive manifest reader rejects symlink/hardlink/device entries and caps the manifest payload)
- [x] **shell completion**: `snapshotNameCompletion` registered on `inspect` here, reusable by `restore`/`remove`/`pack` in later tasks; follows the CLAUDE.md completion contract (calls `completionConfigPath` first, returns `ShellCompDirectiveNoFileComp` on any error)
- [x] follow the `loadStatusContext`-style per-`RunE` helper pattern (no child `PersistentPreRunE` — would silently drop the root's hook per CLAUDE.md)
- [x] **stdout vs stderr discipline**: structured/table output to `cmd.OutOrStdout()`; progress, info, errors to `cmd.ErrOrStderr()`; renderers in `internal/snapshot/` return data (`ListSnapshots`/`ReadManifestFromTar`), the command layer writes — matches the project's "section renderer signature contract"
- [x] write Cobra tests: list (empty + two snapshots), current (cleared + set), inspect against fixture (dir + tar + DIVERGED), JSON output schema stability, completion handler returns candidates without project resolution, Args validators reject extra/missing args
- [x] run `go test ./internal/command/... && make lint` — must pass before task 7

### Task 7: `create` subcommand with variants

- [ ] implement `devbox snapshot create <name> [-d <desc>] [--using=<variant>] [-y]` with `Args: cobra.ExactArgs(1)`
- [ ] load snapshot config (error if `create:` missing); validate name via `snapshot.ValidateName`; call `AcquireProjectLocks(baseDir)` from Task 3
- [ ] if `./snapshots/<name>/` exists, confirm overwrite in TTY or fail without `-y`
- [ ] mkdir snapshot dir + `<snap>/devbox/`; copy `devbox/local.yml` and `.devbox/deploy/state.yml` into `<snap>/devbox/`
- [ ] select workflow via `SelectWorkflow(cfg, "create", variant)`; build `BuildSnapshotVars(name, absPath, desc, variant, now())`; run via `snapshot.RunWorkflow` with `Scope: SnapshotScopeCreate`
- [ ] scan artifacts (streaming sha256, int64 size, reject symlinks), compute sha256, write manifest atomically (write-temp + rename in same dir), update current pointer atomically
- [ ] on workflow failure: keep directory, write manifest with `last_create.status = "failed"` and `failed_step`, do not touch current pointer, exit code 1
- [ ] **signal handling**: SIGINT during create propagates `ctx.Done()` to the workflow; the immediate child process receives SIGTERM via `bindCancel` (`runner_host.go:52`) — current runners signal **only the direct child**, not its process group, so any grandchildren the user's command spawns won't be signaled unless that command propagates the signal itself. Then defer releases locks → snapshot dir is kept with `last_create.status = "interrupted"` → exit code 130. (If process-group termination becomes necessary, that's a separate `internal/usercommands/runtime` task — out of scope here.)
- [ ] **notifications**: emit `internal/notify` events on success/failure/interrupt using `notify.OpCommand` with operation label `"snapshot:create"` (sub-steps already suppress notifications via `SkipNotify: true` in `runner_workflow.go:229`)
- [ ] write integration test: end-to-end create with a fake user command (`type: shell` writing a marker file using `${snapshot.path}`), then assert manifest contents (sha256 of the marker file is correct), current pointer is updated, and the lock files are released
- [ ] write test: overwrite confirmation with `-y` (no prompt) and without (mock TTY)
- [ ] write test: variant missing → error before any filesystem mutation, no directory created
- [ ] write test: snapshot vars round-trip — fake `type: shell` command whose `run:` is `echo "${snapshot.name} ${snapshot.path}"`; test reads its captured stdout and asserts the values match what `BuildSnapshotVars` constructed. (Do **not** use `type: script` here: script file contents are not rendered, only invocation env/workdir/path per `runner_script.go:165`.)
- [ ] run `go test ./... && make lint` — must pass before task 8

### Task 8: `restore` and `rollback` subcommands

- [ ] implement `devbox snapshot restore <name> [-y]` with `Args: cobra.ExactArgs(1)`: acquire project locks; load + verify manifest (project name match; `config_hash` compare → warn always, block when `require_matching_config: true`; when manifest `config_hash` is empty (snapshot predates any deploy), treat as match — never block); confirm in TTY unless `-y`
- [ ] backup current `devbox/local.yml` and `.devbox/deploy/state.yml` into `.devbox/snapshots/.pre-restore-backup/` atomically (write-temp + rename); overwrite the previous backup
- [ ] restore devbox files from `<snap>/devbox/` over the working copies
- [ ] select restore workflow: `SelectWorkflow(cfg, "restore", manifest.Variant)`; on missing variant block, fall back to default; run via `snapshot.RunWorkflow` with `Scope: SnapshotScopeRestoreOrRemove`
- [ ] on success: update current pointer to `<name>` atomically; write `last_restore.status = "ok"` to manifest
- [ ] on failure or SIGINT: leave current pointer untouched; write `last_restore.status` in `{"failed","partial","interrupted"}` with `failed_step`; emit a hint about `.pre-restore-backup/` for manual recovery; appropriate exit code (1 or 130)
- [ ] implement `devbox snapshot rollback [-y]` with `Args: cobra.NoArgs`: reads `rollback_target` from config (fail if config absent or target field empty); dispatches to the restore code path; fails clearly if target snapshot doesn't exist
- [ ] **notifications**: emit notify events on restore success/failure/interrupt via `notify.OpCommand` with operation labels `"snapshot:restore"` and `"snapshot:rollback"` (rollback dispatches restore internally but tags its own label for user visibility)
- [ ] write integration tests: round-trip create → modify-file → restore → assert restored state; rollback path; `config_hash` divergence with `require_matching_config: true` blocks (exit 1, no filesystem change); empty `config_hash` is never blocked; missing variant on restore falls back to default; SIGINT mid-restore leaves `.pre-restore-backup/` populated and current pointer untouched
- [ ] run `go test ./... && make lint` — must pass before task 9

### Task 9: `remove`, `pack`, `unpack` with archive safety

- [ ] implement `devbox snapshot remove <name> [-y]` with `Args: cobra.ExactArgs(1)`: acquire locks; confirm; if `remove:` workflow defined run it (Scope: `SnapshotScopeRestoreOrRemove`); `os.RemoveAll(snapshotDir)`; clear current pointer atomically if it pointed here
- [ ] implement `devbox snapshot pack <name> [--out=<path>] [--exclude=<glob>...]`: **acquire project locks** (`lock.AcquireProjectLocks` from Task 3) before reading the snapshot dir — pack must not race a concurrent `remove`/`create`/`restore`; write `./snapshots/<name>.tar.gz` and a `.sha256` sidecar; honor `pack.exclude` from config; CLI `--exclude` flags append to the config list (do not replace)
- [ ] implement `devbox snapshot unpack <tar-path> [--as=<name>] [-y]`: **acquire project locks** before writing to `./snapshots/`; with the following **archive-safety contract** (in `internal/snapshot/archive.go`):
  1. **Path validation — pre-checks on raw header, then join, then `ContainedRel`** (do **not** rely on `filepath.Join` + `ContainedRel` alone; `Join` normalizes `..` away before `ContainedRel` ever sees the original header):
     - reject `filepath.IsAbs(header.Name)` — absolute paths
     - reject `header.Name == ""` or `header.Name == "." ` or `header.Name == ".."`
     - reject any name where `filepath.IsLocal(header.Name) == false` (Go 1.20+; catches absolute, root-relative, and `..`-containing names in one call)
     - then compute `full := filepath.Join(targetRoot, header.Name)` and validate via `pathsafe.ContainedRel(targetRoot, full)` as a belt-and-braces check
  2. **Type allowlist**: accept only `tar.TypeReg` and `tar.TypeDir`. Reject `TypeSymlink`, `TypeLink` (hardlink), `TypeChar`, `TypeBlock`, `TypeFifo`, `TypeXGlobalHeader` (and any other typeflag) with an error naming the rejected entry. Symlinks and hardlinks can both escape the target dir even after path sanitization.
  3. **Resource caps**: enforce package-level constants `maxUnpackBytes = 50 << 30` (50 GiB) and `maxUnpackFiles = 100_000`; abort with a clear error on overflow (zip-bomb defense). Use `io.LimitReader` wrapping the decompressor. Caps are constants for now — not surfaced in `SnapshotConfig`. If a real need for tuning shows up, promote to config in a follow-up.
  4. **No follow on existing dirs**: when creating a directory, use `os.MkdirAll` with 0o755; when writing a file, use `os.OpenFile(O_CREATE|O_WRONLY|O_EXCL, 0o644)` to refuse overwriting any pre-existing file inside the target.
  5. **Atomic target install**: extract into a sibling temp dir (`./snapshots/.unpack-<random>/`), then `os.Rename` to the final `<name>/`; on any error during extraction, `os.RemoveAll` the temp dir
- [ ] verify `.sha256` sidecar if present (mismatch → exit 1 with no extraction attempted); when absent, warn on stderr "no checksum sidecar — integrity not verified" but proceed
- [ ] after extraction, read the manifest; warn if `project.name` differs from the current project; confirm overwrite if target dir exists
- [ ] write tests: remove deletes dir + clears pointer; pack roundtrip with checksum verification; unpack rejects sha256 mismatch without any filesystem mutation; **malicious tar fixtures** under `internal/snapshot/testdata/archives/`:
  - `escape.tar.gz` with entry `../etc/passwd` → rejected
  - `absolute.tar.gz` with entry `/etc/passwd` → rejected
  - `symlink.tar.gz` with a symlink entry → rejected (TypeSymlink)
  - `hardlink.tar.gz` with a hardlink entry → rejected (TypeLink)
  - `device.tar.gz` with a char/block device entry → rejected
  - `oversize.tar.gz` with declared total > cap → rejected with cap-exceeded message
  - `bomb.tar.gz` with 100 001 small files → rejected with file-count message
  - each malicious fixture: assert no files written outside the temp staging dir, temp dir is cleaned up
- [ ] run `go test ./internal/snapshot/... ./... && make lint` — must pass before task 10

### Task 10: Validate domain `snapshot.*`

- [ ] create `internal/validate/snapshot/all.go` exporting `All(cfg *config.DevboxConfig, snapCfg *config.SnapshotConfig, snapCfgErr error, baseDir string, reg *registry.Registry) []validate.Validator`
- [ ] implement validators from spec §12:
  - `snapshot.config_loadable` — silent if file absent; error if load failed
  - `snapshot.create_defined` — info, if `create:` block missing (create will refuse to run)
  - `snapshot.restore_defined` — info, if `restore:` block missing
  - `snapshot.variant_pairing` — warn, if `create.variants[X]` exists but `restore.variants[X]` does not (and no default `restore`)
  - `snapshot.rollback_target_exists` — warn, if `rollback_target` field set but no snapshot with that name
  - `snapshot.<name>.manifest_valid` — error, if manifest is missing or unparseable
  - `snapshot.<name>.artifacts_exist` — error, if any manifest-listed artifact is missing on disk
  - `snapshot.<name>.checksums` — runs only when `--verify` is passed on the validate command (see flag wiring below); warn on mismatch
  - `snapshot.<name>.last_create_failed` — info, if last create was partial/failed/interrupted
- [ ] add a tpl-scope-check validator that scans `devbox/snapshot.yml` step `when:` and `with:` expressions: `${snapshot.created_at}` in `create:` blocks → error; `${snapshot.*}` referenced outside snapshot blocks would already be caught at compile via the scope enum, but a validate-time check gives an earlier signal
- [ ] **register in `internal/command/validate.go:369`** (`buildRegistry`) — load `snapCfg` via `LoadSnapshotConfig(baseDir)` next to the existing `LoadValidateConfig` call and thread it through: `for _, v := range valsnap.All(cfg, snapCfg, snapCfgErr, baseDir, cmdReg) { reg.Register(v) }`. This is the **only** registry-assembly seam — not `internal/validate/validate.go`.
- [ ] **add `devbox validate snapshot [<name>]` Cobra subcommand** mirroring the existing `validate env` / `validate checks` / `validate config` / `validate commands` pattern (`internal/command/validate.go` builds these as sibling cobras under `newValidateCmd`): the subcommand passes `scope = ["snapshot"]` (or `["snapshot", "<name>"]` for a single snapshot) to `Registry.Run`. Update the help text block at `validate.go:65-71` to list the new subcommand. Without this, users can only invoke snapshot validators via the bare `devbox validate`, never scoped.
- [ ] add `--verify` flag local to `devbox validate snapshot` (not propagated to other subcommands or the parent — checksum verification is only meaningful for the snapshot domain); thread it through `buildRegistry` via a new parameter or `validate.Context.VerifyChecksums bool`; the `snapshot.<name>.checksums` validator consults it and self-skips when off (mirrors how `env`/`checks` validators handle their conditional execution)
- [ ] write table-driven tests per validator with fixtures under `internal/validate/snapshot/testdata/`; one full-command-flow test asserts `devbox validate snapshot` exits with the right severity-gated code when snapshot validators emit errors/warnings; one test asserts `--verify` toggles checksum validation; one test asserts `devbox validate snapshot <name>` filters to a single snapshot's validators
- [ ] run `go test ./internal/validate/... ./internal/command/... && make lint` — must pass before task 11

### Task 11: Documentation

- [ ] add `docs/reference/config/snapshot.md` covering: file location, top-level keys, `create` / `restore` / `remove` workflow blocks (referring to the existing `model.WorkflowStep` doc for step syntax — do **not** duplicate), `variants`, `pack`, lifecycle, restore safety semantics, `${snapshot.*}` namespace and scope rules, manifest contents
- [ ] add a worked example at the top showing UC-3 (the main switch-between-tasks flow)
- [ ] regenerate the CLI reference via `devbox docs generate` once subcommands compile
- [ ] update `docs/internals/packages.md` to add `internal/snapshot` and `internal/validate/snapshot` with their invariants:
  - atomic IO contract (write-temp + rename in same dir)
  - lock-acquisition order (`deploy.lock` → `snapshot.lock`)
  - render-context propagation point (`runner_workflow.go:202` + `build_context.go`)
  - archive-safety contract (path validation, type allowlist, resource caps)
  - manifest-without-schema_version policy
- [ ] **document the deploy-side touch**: update the deploy/lifecycle section of `packages.md` to note that lifecycle commands now also acquire `snapshot.lock`
- [ ] skim cross-references back to deploy/state docs; confirm accuracy

### Task 12: Verify acceptance criteria

- [ ] all UC-1..UC-6 from spec §4 reproducible via integration tests or a documented manual transcript
- [ ] lock interaction verified both directions (deploy held → snapshot fails with PID; snapshot held → deploy fails with PID)
- [ ] template scope gate verified: `${snapshot.*}` in None scope rejected at compile; `${snapshot.created_at}` in Create scope rejected; both work in their valid scopes
- [ ] **snapshot vars threading verified end-to-end**: integration test with a fake user command prints `${snapshot.name}` / `${snapshot.path}` and the test asserts on the captured stdout
- [ ] archive-safety verified: every malicious-tar fixture is rejected; no filesystem mutation outside staging
- [ ] `make test` (full suite) passes
- [ ] `go test -race ./...` passes
- [ ] `go vet ./...` clean
- [ ] `make lint` clean
- [ ] confirm no `schema_version` fields anywhere (loader, manifest, validate config)
- [ ] confirm `prompt_baseline_on_first_restore` is absent (was dropped in Task 1)
- [ ] confirm `internal/lock/lock.go` is unchanged (no `AcquireExclusive`, no PID-only conflict probes, lock files still left on disk after Release)

## Technical Details

### `devbox/snapshot.yml` shape (canonical, from spec §6 minus `prompt_baseline_on_first_restore`)

```yaml
dir: ./snapshots
rollback_target: baseline
require_matching_config: false
pack:
  exclude: ["**/*.tmp", ".cache/**"]

create:
  description: Capture current env
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }
  variants:
    db-only:
      steps:
        - command: db.dump
          with: { out: ${snapshot.path}/db/main.sql.gz }

restore:
  description: Restore env from snapshot
  steps:
    - command: db.restore
      when: file-exists ${snapshot.path}/db/main.sql.gz
      with: { in: ${snapshot.path}/db/main.sql.gz }

remove:
  steps:
    - command: db.drop-snapshot-db
      when: file-exists ${snapshot.path}/db/main.sql.gz
```

The `steps:` shape is `[]model.WorkflowStep` — the existing type from `usercommands/model`. We do not invent a parallel schema; snapshot workflows are user-command workflows executed at runtime from a different source file.

### Manifest shape (canonical, no `schema_version`)

```yaml
name: feature-x-wip
created_at: 2026-05-24T11:02:00Z
description: WIP feature X
project:
  name: tbm-next
  config_hash: def67890                     # empty string if no deploy has run yet
devbox_version: 0.42.0
variant: ""
artifacts:
  - path: db/main.sql.gz
    size: 1287654321        # int64
    sha256: abc...
devbox_files:
  local_yml: devbox/local.yml
  deploy_state: devbox/deploy-state.yml
last_create:
  at: 2026-05-24T11:02:00Z
  status: ok                # ok | partial | failed | interrupted
  failed_step: ""
last_restore:
  at: 2026-05-24T15:42:00Z
  status: ok
  duration_ms: 12340
  failed_step: ""
```

### Filesystem layout

```
<project>/
  devbox/snapshot.yml
  snapshots/
    <name>/
      manifest.yml
      devbox/{local.yml, deploy-state.yml}
      <user artifacts>
    <name>.tar.gz
    <name>.tar.gz.sha256
  .devbox/snapshots/
    current
    snapshot.lock
    .pre-restore-backup/{local.yml, deploy-state.yml}
    .unpack-<random>/                         # transient unpack staging
```

### Template namespace + scope (from spec §9)

| Variable | None | Create | RestoreOrRemove |
|---|---|---|---|
| `${snapshot.name}` | error | ✓ | ✓ |
| `${snapshot.path}` | error | ✓ | ✓ |
| `${snapshot.description}` | error | ✓ | ✓ |
| `${snapshot.variant}` | error | ✓ | ✓ |
| `${snapshot.created_at}` | error | **error** (doesn't exist yet) | ✓ |

### Lock acquisition order

All project-mutating commands acquire locks in this fixed order:

1. `<baseDir>/.devbox/deploy/deploy.lock`
2. `<baseDir>/.devbox/snapshots/snapshot.lock`

Release is reverse. This is enforced by the shared `lock.AcquireProjectLocks(baseDir)` helper at `internal/lock/project.go` — never call `lock.Acquire` on these paths directly from command code. Deploy lifecycle commands (`deploy`, `run`, `stop`, `restart`, `reset`) and all mutating snapshot commands (`create`, `restore`, `rollback`, `remove`, `pack`, `unpack`) both use this helper.

**Ordering relative to preflight**: lifecycle commands acquire the project locks *after* `preflightRun` succeeds, never before. This preserves the existing invariant from `deploy.go:226→234` that preflight validators (which may invoke user `type: command` checks) do not hold operation locks. Snapshot mutating commands do not run preflight, so they acquire locks at the top of their `RunE`.

### Exit codes

| Code | When |
|---|---|
| 0 | Success |
| 1 | Workflow failure, manifest corruption, archive rejection, missing required config block |
| 64 | Usage error (bad name, missing argument, malformed YAML at the CLI surface) |
| 75 | `EX_TEMPFAIL` — lock held by another live process |
| 130 | SIGINT during a long-running workflow (128 + 2) |

### Stdout vs stderr discipline

- Structured/table/JSON output → `cmd.OutOrStdout()`
- Progress, info, warnings, errors, confirmation prompts → `cmd.ErrOrStderr()`
- Renderers in `internal/snapshot/` return strings; the command layer writes — matches CLAUDE.md "section renderer signature contract"

### Atomicity invariants

- Manifest write: temp file in **same directory** as final file → `chmod 0o644` → `rename`. Cross-filesystem rename is not atomic — same dir is required.
- Current pointer write: same pattern; never partial-write a pointer.
- Pre-restore backup write: same pattern; overwrites the previous backup.
- On create failure or interrupt: snapshot directory is **kept** with `last_create.status` set; current pointer **not** touched.
- On restore failure or interrupt: current pointer **not** touched; `.pre-restore-backup/` retains the pre-restore devbox files for manual recovery.
- Unpack: extraction into staging temp dir, then atomic `rename` to final name; on any error during extraction the staging dir is `RemoveAll`'d.

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification**:

- Run UC-3 (the main flow) by hand in a real project: snapshot a WIP DB, switch git branch, restore baseline, switch back, restore the WIP — confirm timings feel acceptable on a 1+ GB Postgres dump.
- Manually pack a snapshot, copy to another machine, unpack, restore — confirm sha256 verification and project-name warning fire as expected.
- Confirm restore failure path on a deliberately broken `db.restore` user command leaves `.pre-restore-backup/` populated and `current` unchanged.
- Send SIGINT mid-create — confirm `last_create.status = "interrupted"` and locks released.

**External system updates**:

- The sibling demo projects in the monorepo should grow an example `devbox/snapshot.yml` with `db.dump` / `db.restore` user commands so the feature is discoverable. Separate PR after the CLI side is merged.
- No third-party services involved. No deployment config changes.

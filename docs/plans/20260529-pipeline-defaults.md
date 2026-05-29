# Built-in defaults for deploy / reset / lifecycle pipelines

## Overview

Make `devbox/deploy.yml`, `devbox/reset.yml`, and `devbox/lifecycle.yml` truly optional. When absent, devbox uses a built-in default pipeline that covers the common-case project (single stack: `docker up --wait` for run/deploy, `docker down` + volume/dir cleanup for reset). Projects with custom flows still author their own YAML — that is full replacement, no merging.

**Problem solved.** Today three pipelines are silently or loudly mandatory:

- `lifecycle.yml` — `lifecycle/run.go:184` hard-errors `"no lifecycle.yml — see devbox/lifecycle.example.yml"`.
- `reset.yml` — `reset/plan.go:33` hard-errors on `os.ErrNotExist`.
- `deploy.yml` — `cli/deploy/deploy.go:377` treats missing as empty, so `devbox deploy run` silently runs only the implicit `render env` step and produces no containers. Worst of both worlds: no error, but no useful work.

Stop already has a fallback (`EnsureStopConfig`) — but that fallback only reaps daemons, it does not `docker down`, so `devbox stop` without `lifecycle.yml` silently leaves containers running.

**Design from brainstorm:**

- Composition: full replacement. User YAML wins entirely when present; defaults fire only when the file is absent or the relevant section is `nil`. One preserved system exception: `auto-reap` phase prepended to every stop pipeline (today's invariant; not part of the default).
- Defaults live in Go constructors next to existing `EnsureStopConfig` / `DefaultInfoConfig` pattern.
- One stderr info line per command when the default fires: `Using built-in default <deploy|reset|run|stop> pipeline (no devbox/<file>.yml on disk).` Suppressed in `--output json`.

## Context (from discovery)

- **Project**: Go CLI (`devbox`) for orchestrating local Docker dev environments. Composition root in `cli/root.go`; workflow logic in `internal/core/workflow/{deploy,reset,lifecycle}/`; config types and loaders in `internal/core/project/config/devbox.go`.
- **Existing precedent**:
  - `EnsureStopConfig` in `internal/core/workflow/lifecycle/autoreap.go:37` already synthesises a stop pipeline when the file/section is absent, and unconditionally prepends an `auto-reap` phase even to user-provided stop pipelines. Inline literal `defaultStopMessage` lives in the same file.
  - `DefaultInfoConfig()` in the info subsystem follows the same pattern (referenced in CLAUDE.md "info.yml auto-blocks" section).
- **Render writer**: `internal/shared/render/output.go:71` exposes `(w *Writer) Info(msg string)` — direct match for the info-line UX.
- **Load path for deploy.yml**: `cli/deploy/deploy.go:377` already does `errors.Is(err, os.ErrNotExist)` → `projectDeploy = nil`, but `cfg.Deploy` is independently set by `LoadConfig` at `internal/core/project/config/devbox.go:1232` (file present → assign; absent → empty `&ProjectDeployConfig{}` at line 1237-1238). The resolver `deploy/plan.go:39` reads `cfg.Deploy.Phases` from the merged config. So the default must be wired at the resolver call site OR by replacing the empty placeholder in `LoadConfig`. The CLI layer also still needs a `defaulted` signal for the info line — so the cleanest split is: keep `LoadConfig` as-is (empty placeholder when missing), and wrap with `EnsureDeployConfig` at the CLI layer, returning `(*Config, defaulted bool)`.
- **Load path for reset.yml**: `reset/plan.go:33` and `:62` (`LoadAndResolvePlan` and `FindStep`) both call `config.LoadResetConfig` and propagate any error including `ErrNotExist`. Wrap at the same site.
- **Load path for lifecycle.yml**: `lifecycle/run.go:180` and `:243` both load `LoadLifecycleConfig` and hard-error on `ErrNotExist` and on `lifecycleCfg.Run == nil`. `lifecycle/stop.go:81` already routes through `EnsureStopConfig`.
- **Auto-reap phase**: lives in `autoreap.go:16` as a closed Go-constructed phase named `_auto_reap_daemons`. Always prepended in `EnsureStopConfig`; never overridable. Stays unchanged.
- **JSON mode plumbing**: `cmdctx.RootFlags.Output` (per CLAUDE.md "JSON output mode" key pattern). Info line must be gated behind `flags.Output != "json"`.

## Development Approach

- **Testing approach**: Regular (code first, then tests) per project convention — see CLAUDE.md "Testing Guidelines" which favours table-driven tests next to code.
- Complete each task fully before moving to the next.
- Make small, focused changes — one pipeline per task family.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task.
- Per CLAUDE.md "Project Status & Compatibility Policy": no `schema_version`, no migration paths, no deprecation shims. Pre-release, free to rename / restructure.

## Testing Strategy

- **Unit tests**: required for every task.
- **Integration tests**: call-site tests in `cli/deploy/`, `workflow/reset/`, `workflow/lifecycle/` use existing test helpers (see `testhelpers_test.go` in those packages).
- **End-to-end smoke**: bare-minimum-project tests live with the CLI commands that exercise them.
- **Golden line tests**: info-line wording asserted exactly per command.
- No project-wide e2e harness exists; rely on the in-package integration tests and CLI command tests already present.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## Solution Overview

Four `Default*Config()` constructors return canonical default pipelines as Go structs. Four `Ensure*Config(...)` wrappers route nil/empty inputs through the matching `Default*`, returning `(cfg, defaulted bool)`. Call sites (CLI for deploy, workflow packages for reset/lifecycle) load YAML as today, then immediately funnel through `Ensure*`. When `defaulted == true` and `flags.Output != "json"`, the CLI prints one stderr info line before plan/run output via the shared `cmdctx.EmitDefaultNotice` helper. `EnsureStopConfig`'s signature changes (one production caller, three existing tests — updated in one PR per CLAUDE.md "rename freely"); its inline `defaultStopMessage` literal moves into `DefaultStopConfig()`. The lifecycle layer surfaces the `defaulted` signal through both `RunContext.OnDefaultUsed` AND `StopContext.OnDefaultUsed` (separate context types — see Task 5 for the propagation in `RunRestart`).

## Technical Details

### Default pipeline contracts

`DefaultDeployConfig() *config.ProjectDeployConfig`:
```
phases:
  - name: services
    description: Deploy all enabled services (resolved by dependency order)
    deploy_services: true
  - name: start
    description: Start containers and wait for health
    steps:
      - name: up
        type: devbox
        cmd: "docker up --wait"
        description: Start all containers and wait until healthy
        untracked: true
  - name: post-deploy
    description: Post-deploy summary (runs only if all prior phases succeeded)
    untracked: true
    steps:
      - name: info
        type: devbox
        cmd: "info"
        description: Show environment summary
      - name: success
        type: builtin
        cmd: message
        with:
          level: success
          text: Deploy completed successfully
```

`DefaultResetConfig() *config.ProjectDeployConfig`:
```
phases:
  - name: pre
    description: Confirm destructive reset
    untracked: true
    steps:
      - name: confirm
        type: builtin
        cmd: confirm
        with:
          message: "This will stop containers, remove project volumes, and delete generated data."
        description: Confirm before proceeding with destructive operations
  - name: stop
    description: Stop all containers
    steps:
      - name: down
        type: devbox
        cmd: "docker down"
        description: Stop and remove all project containers
  - name: cleanup
    description: Remove project volumes and generated service data
    steps:
      - name: remove-volumes
        type: builtin
        cmd: docker_remove_project_volumes
        description: Remove all Docker volumes belonging to this project
      - name: remove-services
        type: builtin
        cmd: remove_paths
        with:
          paths:
            - services/
        description: Remove generated service hub directories
```

`DefaultRunConfig() *config.LifecycleRunConfig`:
```
update:
  mode: off
show_info: true
final_message: "Project is ready for work!"
phases:
  - name: start
    description: Start containers and wait for health
    steps:
      - name: up
        type: devbox
        cmd: "docker up --wait"
        description: Start all containers and wait until healthy
```

`DefaultStopConfig() *config.LifecycleStopConfig`:
```
final_message: "Project is stopped. Have a nice day!"
phases:
  - name: stop
    description: Stop and remove containers
    steps:
      - name: down
        type: devbox
        cmd: "docker down"
        description: Stop all containers
```
(`auto-reap` is prepended by `EnsureStopConfig`, not part of `DefaultStopConfig`.)

### Ensure wrapper signatures

Single signature per pipeline — no sibling functions. Per CLAUDE.md "rename / restructure freely; fix all call sites in one PR". Bool return named `defaulted` (adjective form, std-lib idiom — `ok`, `found`, `defaulted`):

```go
// internal/core/workflow/deploy/defaults.go
// Returns a freshly-allocated default — callers may mutate safely.
func DefaultDeployConfig() *config.ProjectDeployConfig
func EnsureDeployConfig(loaded *config.ProjectDeployConfig) (cfg *config.ProjectDeployConfig, defaulted bool)

// internal/core/workflow/reset/defaults.go
func DefaultResetConfig() *config.ProjectDeployConfig
func EnsureResetConfig(loaded *config.ProjectDeployConfig) (cfg *config.ProjectDeployConfig, defaulted bool)

// internal/core/workflow/lifecycle/defaults.go
func DefaultRunConfig() *config.LifecycleRunConfig
func DefaultStopConfig() *config.LifecycleStopConfig
func EnsureRunConfig(loaded *config.LifecycleConfig) (cfg *config.LifecycleRunConfig, defaulted bool)

// internal/core/workflow/lifecycle/autoreap.go (signature CHANGE — existing tests + 1 production caller updated in one PR)
func EnsureStopConfig(loaded *config.LifecycleConfig) (cfg *config.LifecycleStopConfig, defaulted bool)
```

All `Default*Config()` constructors return freshly-allocated structs (no shared package-level singletons). One-line doc comment on each: `// DefaultXConfig returns a freshly-allocated default X pipeline. Callers may mutate the result safely.`

`defaulted` semantics:
- Deploy/Reset: `loaded == nil` OR `len(loaded.Phases) == 0` → `true`. (Empty-phase deploy is today's silent-noop bug; treated as "no user pipeline".)
- Run: `loaded == nil` OR `loaded.Run == nil` → `true`.
- Stop: `loaded == nil` OR `loaded.Stop == nil` → `true`.

### Typed pipeline name for the lifecycle callback

In `internal/core/workflow/lifecycle/defaults.go`, declare a typed string for the callback to prevent free-string typos at the CLI:

```go
type DefaultedPipeline string

const (
    DefaultedRun  DefaultedPipeline = "run"
    DefaultedStop DefaultedPipeline = "stop"
)
```

Used as `OnDefaultUsed func(DefaultedPipeline)` in `RunContext` (see below).

### Info-line emission

Two delivery mechanisms depending on where the YAML load happens:

**Deploy** — load is already at CLI layer (`cli/deploy/deploy.go:377`). After `EnsureDeployConfig`, the CLI directly emits via `render.NewWriter(cmd.ErrOrStderr()).Info(...)` when `defaulted && flags.Output != "json"`. The Ensure'd config is then **assigned back to `cfg.Deploy`** so downstream resolvers (which read `cfg.Deploy.Phases` in `deploy/plan.go:39`) see the default. See Task 2 for the grep audit confirming this is the only consumer.

**Reset** — load is at workflow layer (`reset/plan.go:33`). `LoadAndResolvePlan` returns `defaulted` through a new return value; CLI command emits the info line on receipt.

**Lifecycle (run / stop / restart)** — load is deep inside the workflow (`run.go:180`, `stop.go:81`). The workflow exposes **two separate context types** (`RunContext` for `RunRun`/`RunRestart`, `StopContext` for `RunStop`), so the callback field must be added to BOTH — they are NOT a shared type, and `RunRestart` hand-copies fields into `stopCtx` at `run.go:295-301`. Use the existing extension pattern (sibling to `ShowInfo func() error` / `SkipNotify bool` in `RunContext` at `run.go:42-56`, and the existing `Translator`/`Locale` shape in `StopContext` at `stop.go:21-35`):

```go
type RunContext struct {
    // ... existing fields ...
    // OnDefaultUsed is called once per pipeline whose YAML section was absent
    // (either run or stop) and was substituted with a built-in default. CLI
    // uses this to emit the info line on stderr.
    OnDefaultUsed func(DefaultedPipeline)
}

type StopContext struct {
    // ... existing fields ...
    OnDefaultUsed func(DefaultedPipeline) // mirrors RunContext; populated by CLI or by RunRestart
}
```

CLI populates `RunContext.OnDefaultUsed` (run path) and `StopContext.OnDefaultUsed` (direct-stop path). Workflow calls it from `RunRun` after `EnsureRunConfig` (with `DefaultedRun`), from `RunStop` after `EnsureStopConfig` (with `DefaultedStop`). `RunRestart` MUST propagate the callback by copying `ctx.OnDefaultUsed → stopCtx.OnDefaultUsed` at `run.go:295` (alongside the existing `Ctx`/`ConfigPath`/`Yes`/`SkipPreflight`/`ErrOut` copies); without this propagation the stop-leg callback silently drops.

### Shared CLI emit helper

To avoid duplicating `render.NewWriter(cmd.ErrOrStderr()).Info(...)` across three CLI packages, extract to `internal/cli/cmdctx/defaultnotice.go`:

```go
// EmitDefaultNotice writes a single info line announcing that a built-in
// default pipeline was used. No-op when output is JSON.
func EmitDefaultNotice(cmd *cobra.Command, flags *RootFlags, pipeline string, file string) {
    if flags.Output == "json" {
        return
    }
    render.NewWriter(cmd.ErrOrStderr()).Info(
        fmt.Sprintf("Using built-in default %s pipeline (no devbox/%s.yml on disk).", pipeline, file),
    )
}
```

Call sites:
- Deploy CLI: `cmdctx.EmitDefaultNotice(cmd, flags, "deploy", "deploy")`.
- Reset CLI: `cmdctx.EmitDefaultNotice(cmd, flags, "reset", "reset")`.
- Lifecycle CLI: callback wraps the helper, mapping `DefaultedRun`→`"run"`, `DefaultedStop`→`"stop"`, file always `"lifecycle"`.

Info-line text (identical across mechanisms):
```
Using built-in default <deploy|reset|run|stop> pipeline (no devbox/<deploy|reset|lifecycle>.yml on disk).
```

### Load-site error handling pattern

All four call sites use the explicit-switch idiom — only `os.ErrNotExist` is swallowed; everything else propagates wrapped (golang-error-handling):

```go
loaded, err := config.LoadXConfig(path)
switch {
case errors.Is(err, os.ErrNotExist):
    loaded = nil
case err != nil:
    return fmt.Errorf("load <pipeline> config: %w", err)
}
cfg, defaulted := EnsureXConfig(loaded)
```

### Restart behaviour

`RunRestart` in `lifecycle/run.go:294` delegates to stop then run, both of which check the same `lifecycle.yml`. When the file is absent, both pipelines fire their default and the `OnDefaultUsed` callback fires twice (once with `"stop"`, once with `"run"`). Two info lines on stderr in the no-file case is the intended UX per brainstorm.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): nothing external — defaults are internal-only.

## Implementation Steps

### Task 1: Hidden-mandatory audit pass (scope-lock before implementation)

Run BEFORE the four pipeline tasks so any newly-discovered hidden-mandatory file is folded into this plan instead of surfacing late.

**Files:**
- Create: `docs/plans/20260529-pipeline-defaults-audit.md` (audit checklist + findings)
- Modify: this plan file (add follow-up tasks with `➕` prefix if the audit surfaces new in-scope work)

- [x] build a bare-minimum project fixture: only `devbox/devbox.yml` (minimal valid config) + one service folder `devbox/services/demo/service.yml`
- [x] run each top-level command against the fixture and record the outcome (✅ succeeds / ⚠️ silent partial / ❌ hard error) in the audit doc:
  - `devbox` (root, no subcommand)
  - `devbox deploy plan / run / menu`
  - `devbox reset plan / run`
  - `devbox run`, `devbox stop`, `devbox restart`
  - `devbox docker up / down / logs / ps / ...` (every subcommand)
  - `devbox snapshot create / restore / list / remove / pack / unpack`
  - `devbox setup` (and deploy menu setup checks branch)
  - `devbox validate` and every subdomain (`config`, `templates`, `commands`, `env`, `checks`, `linters`, `i18n`, `snapshot`, `setup`, `diag`)
  - `devbox status` (apps / tools / infra / deploy)
  - `devbox info`
  - `devbox render env / ide / ai / git`
  - `devbox docs` (list / get / llms-txt / generate)
  - `devbox logs <service>`
  - `devbox version`, `devbox completion`, `devbox prompt`
- [x] for the three known-bad commands (`deploy run` silent noop, `reset run` hard error, `run`/`restart` hard error), record the today-failure mode explicitly — these are the regression baselines for Tasks 2–5
- [x] for every other ⚠️ or ❌: add a `➕` follow-up task in this plan describing the file involved, whether a default fits the same pattern, and where the wrapper lives. If it does NOT fit (genuinely required file, or error-improvement-only), note it and either close the follow-up or scope it out
- [x] for genuinely-required files (e.g. `devbox.yml` itself), verify the error message is actionable; file a follow-up if not
- [x] commit audit doc; this task is done only when the audit checklist is complete and any `➕` follow-ups are added below
- [x] no test changes in this task — discovery only. Run `make test` to confirm nothing broke from fixture creation; must pass before Task 2

### Task 2: Shared CLI emit helper + deploy defaults

**Files:**
- Create: `internal/cli/cmdctx/defaultnotice.go`
- Create: `internal/cli/cmdctx/defaultnotice_test.go`
- Create: `internal/core/workflow/deploy/defaults.go`
- Create: `internal/core/workflow/deploy/defaults_test.go`
- Modify: `internal/cli/deploy/deploy.go`
- Modify: `internal/cli/deploy/deploy_test.go` (or nearest existing test file for the run/plan paths)

- [x] create `internal/cli/cmdctx/defaultnotice.go` with `EmitDefaultNotice(cmd *cobra.Command, flags *RootFlags, pipeline string, file string)` per spec in Technical Details — gated on `flags.Output != "json"`, writes via `render.NewWriter(cmd.ErrOrStderr()).Info(...)`
- [x] write unit tests for `EmitDefaultNotice` using `cmd.SetErr(&bytes.Buffer{})`: text-mode emits exact info line; JSON mode emits nothing; table-driven over (pipeline, file) pairs covering all four use cases
- [x] grep all readers of `cfg.Deploy` / `cfg.Deploy.Phases` across `internal/` to confirm the reconciliation strategy. Expected callers per discovery so far: `internal/core/workflow/deploy/plan.go:39` and `:67` only. Record the full list in the audit doc and pick one strategy:
  - **(a)** Keep `LoadConfig` writing empty `&ProjectDeployConfig{}` on missing file; CLI calls `EnsureDeployConfig` and **overwrites `cfg.Deploy` with the Ensure'd value** before `ResolvePlan` runs. Preferred (no layering changes — workflow → config import direction preserved).
  - **(b)** Move Ensure into `LoadConfig`. Requires putting `DefaultDeployConfig` somewhere `config/` can import — would force the default into `internal/core/project/config/`, away from the other defaults. Rejected.
- [x] create `internal/core/workflow/deploy/defaults.go` with `DefaultDeployConfig()` returning the contract above (services / start / post-deploy phases). One-line doc comment: returns freshly-allocated default, caller may mutate
- [x] add `EnsureDeployConfig(loaded *config.ProjectDeployConfig) (*config.ProjectDeployConfig, bool)` — `loaded == nil` OR `len(loaded.Phases) == 0` → `DefaultDeployConfig(), true`; otherwise return input verbatim with `false`
- [x] in `internal/cli/deploy/deploy.go` around line 377: apply the load-site error switch pattern (`errors.Is(ErrNotExist)` → nil; other errors wrapped `fmt.Errorf("load deploy config: %w", err)`); then `projectDeploy, defaulted := deploy.EnsureDeployConfig(projectDeploy)`; immediately overwrite `cfg.Deploy = projectDeploy` with a 2-line "why" comment: `// Reconcile: downstream resolvers in workflow/deploy read cfg.Deploy.Phases directly. Overwrite here so the default propagates through ResolvePlan.`; call `cmdctx.EmitDefaultNotice(cmd, flags, "deploy", "deploy")` when `defaulted` BEFORE plan/run execution
- [x] inspect `deploy menu` / `deploy plan` / `deploy run` subcommands in `internal/cli/deploy/` and apply the same Ensure + reconcile + helper-call treatment in each that loads the deploy config independently
- [x] write table-driven unit test for `DefaultDeployConfig` shape (one test): phase names, step names, types, cmds, `untracked` flags, `deploy_services` on `services` phase
- [x] write table-driven unit test for `EnsureDeployConfig` (one test, three rows): `{nil → default,true}`, `{empty Phases → default,true}`, `{populated → input,false}`
- [x] write integration test in `internal/cli/deploy/` using `cmd.SetErr(&bytes.Buffer{})`: bare-minimum project (no `devbox/deploy.yml`) → `deploy plan` succeeds, plan output contains `docker up --wait`, stderr buffer contains the exact info line. **This test must fail on `main` without the fix** (today's silent noop produces zero container-start steps)
- [x] write integration test: `deploy plan --output json` with no `deploy.yml` → stderr buffer has no info line, JSON envelope clean
- [x] write integration test: project WITH `devbox/deploy.yml` → stderr buffer empty (no info line), user's pipeline used verbatim
- [x] run `make test` — must pass before Task 3

### Task 3: Reset defaults — constructor, wrapper, call-site wiring

**Files:**
- Create: `internal/core/workflow/reset/defaults.go`
- Create: `internal/core/workflow/reset/defaults_test.go`
- Modify: `internal/core/workflow/reset/plan.go`
- Modify: `internal/core/workflow/reset/plan_test.go`
- Modify: `internal/cli/reset/reset.go` (or wherever `LoadAndResolvePlan` is invoked — verify via grep)

- [ ] create `internal/core/workflow/reset/defaults.go` with `DefaultResetConfig()` returning pre / stop / cleanup phases per contract. One-line doc comment: returns freshly-allocated default
- [ ] add `EnsureResetConfig(loaded *config.ProjectDeployConfig) (*config.ProjectDeployConfig, bool)` — nil OR empty `Phases` → default+true
- [ ] in `reset/plan.go:33` (`LoadAndResolvePlan`): apply the load-site error switch pattern (`errors.Is(ErrNotExist)` → `resetCfg = nil`; other errors wrapped `fmt.Errorf("load reset config: %w", err)`), then `resetCfg, defaulted := EnsureResetConfig(resetCfg)`
- [ ] in `reset/plan.go:62` (`FindStep`): same load-site switch pattern; lookup proceeds against default phases when no user file exists (so step addresses like `cleanup/remove-volumes` resolve against the default — this is now a user-visible feature)
- [ ] change `LoadAndResolvePlan` return signature to `(*config.ProjectDeployConfig, []pipeline.ResolvedStep, bool, error)` where the bool is `defaulted`. Update all callers in one PR per CLAUDE.md "rename freely" policy
- [ ] in the CLI command(s) that call `LoadAndResolvePlan` (find via grep), call `cmdctx.EmitDefaultNotice(cmd, flags, "reset", "reset")` when `defaulted` BEFORE plan/run output
- [ ] write table-driven unit test for `DefaultResetConfig` shape: phase names (pre/stop/cleanup), confirm step, `docker down`, `docker_remove_project_volumes` + `remove_paths: [services/]`
- [ ] write table-driven unit test for `EnsureResetConfig` (three rows): nil/empty/populated
- [ ] write integration test for `reset plan` / `reset run` against bare-minimum project (no `devbox/reset.yml`) using `cmd.SetErr(&bytes.Buffer{})`: success exit, default phases visible in plan, stderr buffer contains the info line. **Must fail on `main` today** (today's `LoadResetConfig` ErrNotExist propagates as hard error)
- [ ] write integration test for `reset run --step cleanup/remove-volumes` against bare-minimum project → `FindStep` resolves against default, step runs successfully
- [ ] write integration test for `--output json` reset path → no info line, clean JSON
- [ ] write integration test for `reset run --service <name>` against bare-minimum project — verify the documented per-service reset path (CLAUDE.md "Compose-bypass for per-service stop/reset" pattern) is unaffected by the orchestrator-default change. Per-service reset reads `services/<name>/reset.yml`, NOT the orchestrator reset, so the orchestrator default should not fire here. Confirm this in code before writing the test
- [ ] run `make test` — must pass before Task 4

### Task 4: Lifecycle run default + RunContext.OnDefaultUsed callback

**Files:**
- Create: `internal/core/workflow/lifecycle/defaults.go`
- Create: `internal/core/workflow/lifecycle/defaults_test.go`
- Modify: `internal/core/workflow/lifecycle/run.go` (add `OnDefaultUsed` to `RunContext` at line 42-56; update `RunRun` at `:180`/`:243`)
- Modify: `internal/core/workflow/lifecycle/run_test.go`
- Modify: `internal/cli/lifecycle/run.go` (cobra adapter — populates `OnDefaultUsed`)

- [ ] create `internal/core/workflow/lifecycle/defaults.go` with: `DefaultedPipeline` typed string + `DefaultedRun` / `DefaultedStop` constants; `DefaultRunConfig()` returning `update.mode = off`, `show_info = true`, `final_message = "Project is ready for work!"`, single `start` phase with `docker up --wait`. One-line doc comment on the constructor: returns freshly-allocated default
- [ ] add `EnsureRunConfig(loaded *config.LifecycleConfig) (*config.LifecycleRunConfig, bool)` to `defaults.go` — `loaded == nil` OR `loaded.Run == nil` → default+true; otherwise return `loaded.Run, false`
- [ ] extend `RunContext` in `run.go:42-56` with `OnDefaultUsed func(DefaultedPipeline)` (sibling to existing `ShowInfo`/`SkipNotify`); document the same way the existing fields are documented
- [ ] in `lifecycle/run.go:180`: apply the load-site error switch pattern (`errors.Is(ErrNotExist)` → `lifecycleCfg = nil`; other errors wrapped `fmt.Errorf("load lifecycle config: %w", err)`); remove the `"no lifecycle.yml — see devbox/lifecycle.example.yml"` hard-error; pass through `EnsureRunConfig`; remove the subsequent `lifecycleCfg.Run == nil` hard-error block; when `defaulted && ctx.OnDefaultUsed != nil`, call `ctx.OnDefaultUsed(lifecycle.DefaultedRun)`
- [ ] in `lifecycle/run.go:243` (second load — restart path): same treatment, same callback invocation
- [ ] drop any remaining references to `devbox/lifecycle.example.yml` in this file's error messages
- [ ] in `internal/cli/lifecycle/run.go` (cobra adapter; find via grep — likely also covers `restart`): populate `RunContext.OnDefaultUsed = func(p lifecycle.DefaultedPipeline) { cmdctx.EmitDefaultNotice(cmd, flags, string(p), "lifecycle") }` — single one-liner per adapter, delegating to the shared helper from Task 2
- [ ] write table-driven unit test for `DefaultRunConfig` shape: `update.mode == "off"`, `show_info == true`, exact `final_message`, single phase named `start` with one `up` step (`type: devbox`, `cmd: "docker up --wait"`)
- [ ] write table-driven unit test for `EnsureRunConfig` (three rows): `{nil → default,true}`, `{LifecycleConfig{Run: nil, Stop: &…} → default,true}`, `{populated → input,false}`. The middle row covers the partial-section case in one assertion
- [ ] write integration test for `devbox run` against bare-minimum project using `cmd.SetErr(&bytes.Buffer{})`: stub the `docker up --wait` execution using the existing pipeline test seam (review `preflight_test.go` / `run_test.go` for the pattern); exit 0, stderr buffer contains the info line. **Must fail on `main` today** (today's hard-error path)
- [ ] write integration test for `--output json` on `run` → no info line in stderr buffer, JSON envelope clean
- [ ] write integration test for project WITH partial `lifecycle.yml` (only `stop:` populated, no `run:`) → `run` uses default (info line emitted via `OnDefaultUsed(DefaultedRun)`); `stop` uses user config (no `OnDefaultUsed(DefaultedStop)` fired). Task 5 covers the symmetric opposite
- [ ] run `make test` — must pass before Task 5

### Task 5: Lifecycle stop default — collapse EnsureStopConfig signature

**Files:**
- Modify: `internal/core/workflow/lifecycle/defaults.go` (extend from Task 4)
- Modify: `internal/core/workflow/lifecycle/autoreap.go`
- Modify: `internal/core/workflow/lifecycle/stop.go` (extend `StopContext`, update `RunStop`)
- Modify: `internal/core/workflow/lifecycle/run.go` (update `RunRestart`'s `stopCtx` construction at `:295`)
- Modify: `internal/core/workflow/lifecycle/stop_test.go`
- Modify: `internal/core/workflow/lifecycle/restart_test.go`
- Modify: `internal/cli/lifecycle/stop.go` (cobra adapter — populates `StopContext.OnDefaultUsed`)
- Modify: `internal/cli/lifecycle/restart.go` (cobra adapter — populates `RunContext.OnDefaultUsed` only; workflow propagates into `stopCtx`)

- [ ] add `DefaultStopConfig() *config.LifecycleStopConfig` to `defaults.go`: `final_message = "Project is stopped. Have a nice day!"`, single `stop` phase with `down` step (`type: devbox`, `cmd: "docker down"`) — NO auto-reap (that stays in `EnsureStopConfig`). One-line doc comment: returns freshly-allocated default
- [ ] **change `EnsureStopConfig` signature** to `func EnsureStopConfig(cfg *config.LifecycleConfig) (*config.LifecycleStopConfig, bool)` returning `(result, defaulted)`. Update all callers in one PR (only 1 production caller: `lifecycle/stop.go:86`)
- [ ] refactor `EnsureStopConfig` body: when `cfg == nil || cfg.Stop == nil`, take both `Phases` and `FinalMessage` from `DefaultStopConfig()` (no user value to preserve when `Stop == nil`), prepend `autoReapPhase()` to the phases, return `(result, true)`; when `cfg.Stop != nil`, prepend `autoReapPhase()` to user phases as today, return `(result, false)`
- [ ] delete the now-redundant inline `defaultStopMessage` constant from `autoreap.go` — its value lives in `DefaultStopConfig()`
- [ ] in `lifecycle/stop.go:86`: update `stopCfg := EnsureStopConfig(lifecycleCfg)` → `stopCfg, defaulted := EnsureStopConfig(lifecycleCfg)`; if `defaulted && ctx.OnDefaultUsed != nil`, call `ctx.OnDefaultUsed(lifecycle.DefaultedStop)`. Also apply the load-site error switch pattern at `stop.go:81` (the existing code already partially does this — tighten if needed)
- [ ] extend `StopContext` in `stop.go:21-35` with `OnDefaultUsed func(DefaultedPipeline)` (mirroring the `RunContext` extension from Task 4). These are SEPARATE struct types in the lifecycle package — not shared. Document the field with the same wording as in `RunContext`
- [ ] **in `RunRestart` at `run.go:295`**: extend the hand-copied field list constructing `stopCtx` to include `OnDefaultUsed: ctx.OnDefaultUsed` alongside the existing `Ctx`, `ConfigPath`, `Yes`, `SkipPreflight`, `ErrOut`. Without this line the stop-leg callback silently drops and the "exactly twice" restart assertion below fails
- [ ] in `internal/cli/lifecycle/stop.go` (cobra adapter): populate `StopContext.OnDefaultUsed` with the same `cmdctx.EmitDefaultNotice` one-liner as Task 4 (closure captures `cmd` + `flags`; calls `EmitDefaultNotice(cmd, flags, string(p), "lifecycle")`)
- [ ] in `internal/cli/lifecycle/restart.go` (cobra adapter for `RunRestart`): populate `RunContext.OnDefaultUsed` only — the workflow's `RunRestart` propagates it into `stopCtx` per the change above
- [ ] update existing tests in `stop_test.go` for the new `EnsureStopConfig` signature:
  - `TestEnsureStopConfig_NilConfig`: assert returned `defaulted == true`
  - `TestEnsureStopConfig_NilStop`: assert returned `defaulted == true`
  - `TestEnsureStopConfig_PrependsToUserPhases`: assert returned `defaulted == false`
- [ ] add regression test: `EnsureStopConfig(nil)`'s non-autoreap phases deep-equal `DefaultStopConfig().Phases` (verifies the refactor preserves observable shape)
- [ ] write table-driven unit test for `DefaultStopConfig` shape: exact `final_message`, single `stop` phase with `down` step, NO `_auto_reap_daemons` phase
- [ ] write integration test for `devbox stop` against bare-minimum project using `cmd.SetErr(&bytes.Buffer{})`: stderr buffer contains the info line, `docker down` resolved in plan; **must show today's stop reaches `docker down` ONLY via this fix** (verify against `main` that today's `stop` plan against a bare-minimum project contains ONLY `_auto_reap_daemons`, no `docker down`)
- [ ] write **symmetric partial test** (counterpart to Task 4): project WITH `lifecycle.yml` containing only `run:` (no `stop:`) → `stop` uses default, info line emitted; `run` uses user config, no info line
- [ ] write integration test for `devbox restart` against bare-minimum project: capture stderr with `cmd.SetErr(&bytes.Buffer{})`; assert callback fires **exactly twice** — once with `DefaultedStop` (from `RunStop` leg) AND once with `DefaultedRun` (from `RunRun` leg). Verify via a counting-callback that records each `DefaultedPipeline` value. This test guards the `RunRestart` propagation at `run.go:295` — without `OnDefaultUsed: ctx.OnDefaultUsed` in the `stopCtx` literal, only one of the two callbacks fires. stderr buffer contains exactly two info lines; plan resolves end-to-end
- [ ] run `make test` — must pass before Task 6

### Task 6: Documentation

**Files:**
- Modify: `docs/reference/config/deploy.md`
- Modify: `docs/reference/config/lifecycle.md`
- Modify: relevant reset doc (likely `docs/reference/config/lifecycle.md` or a separate reset section — check the file structure)
- Modify: `docs/internals/packages.md`
- Modify: `CLAUDE.md` (note: AGENTS.md is the canonical file — `CLAUDE.md` is a symlink per CLAUDE.md; edit `AGENTS.md`)

- [ ] update `docs/reference/config/deploy.md`: state that `devbox/deploy.yml` is optional; when absent, devbox uses a built-in default pipeline (`services` → `start: docker up --wait` → `post-deploy: info + success`); state that the info line is emitted when the default fires
- [ ] update `docs/reference/config/lifecycle.md`: same statement for both `run:` and `stop:` sections — when section absent (or file absent), default fires; spell out the default contracts
- [ ] add or update the reset reference doc to state that `devbox/reset.yml` is optional; spell out default contract (confirm → docker down → remove volumes + remove services/)
- [ ] update `docs/internals/packages.md` for `internal/core/workflow/{deploy,reset,lifecycle}`: mention `Default*Config` + `Ensure*Config` as the single source of truth for defaults, and the `auto-reap` system phase exception
- [ ] update `AGENTS.md` "Key Patterns": add a "Pipeline defaults" bullet describing the `Default*`/`Ensure*` pattern (paired Go constructors returning freshly-allocated structs + tuple-return `(cfg, defaulted bool)` wrappers), the full-replacement composition model, the `cmdctx.EmitDefaultNotice` shared CLI helper, the `lifecycle.DefaultedPipeline` typed enum + `RunContext.OnDefaultUsed func(DefaultedPipeline)` callback, and the auto-reap exception. Note that `cfg.Deploy` is post-Ensure overwritten by the CLI deploy entrypoint (so downstream resolvers see the default when `deploy.yml` is absent)
- [ ] regenerate embedded docs: `make build` (per CLAUDE.md "Build, Test, and Development Commands" — required after `docs/reference/` edits so the binary's embedded docs match)
- [ ] verify `git diff --exit-code internal/core/docs/content_hashes_gen.go` is clean after `make build` (per CLAUDE.md "Content-hash manifest fallback" — the CI guard)
- [ ] run `make test` to confirm doc-subsystem golden tests still pass

### Task 7: Verify acceptance criteria

- [ ] verify a project containing only `devbox.yml` + one `devbox/services/<name>/service.yml` successfully runs all of: `devbox deploy plan`, `devbox deploy run` (mocked or against a real local docker), `devbox reset plan`, `devbox reset run`, `devbox run`, `devbox stop`, `devbox restart` — no other devbox YAML present
- [ ] verify each of those commands prints the info line on stderr when the default fires
- [ ] verify each stays silent when the user's file is on disk
- [ ] verify `--output json` mode does NOT emit the info line and does NOT pollute the JSON stream for any of the above
- [ ] verify the refactored `EnsureStopConfig` (new signature, returning `(*config.LifecycleStopConfig, bool)`) and the three updated tests (`TestEnsureStopConfig_NilConfig`, `_NilStop`, `_PrependsToUserPhases`) all pass
- [ ] run `make test` — full suite clean
- [ ] run `make lint` — clean
- [ ] verify audit checklist in `docs/plans/20260529-pipeline-defaults-audit.md` shows ✅ for every command (or has follow-up tasks closed for ⚠️/❌); any `➕` follow-ups added to this plan after Task 1 must be complete

### Task 8: Finalise

- [ ] move this plan to `docs/plans/completed/20260529-pipeline-defaults.md`
- [ ] keep the audit doc next to the completed plan in `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- Smoke-test against a real project that previously had `lifecycle.yml` / `reset.yml` / `deploy.yml` — confirm no behavioural regression when user YAML is on disk.
- Smoke-test against a freshly scaffolded project (just `devbox.yml` + one service) — confirm the full default-driven workflow boots a stack via `devbox run`.

**External system updates:**
- None. Defaults are entirely internal to the CLI binary. Sibling projects in the monorepo (per CLAUDE.md "Live user projects sitting alongside this CLI in the monorepo") that already ship their own pipeline YAML are unaffected.

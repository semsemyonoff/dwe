# Internal Package Restructure

## Overview

Reorganize `internal/` so that each package is responsible for one clearly named domain. The current layout concentrates business logic inside `internal/command/` (the cobra adapter), forcing readers and agents to scroll through cobra plumbing to find deploy/reset/lifecycle/status logic. The target layout extracts domain logic into dedicated packages, renames `internal/commands` to `internal/usercommands` and splits it into subpackages, and reduces `internal/command` to thin cobra wiring.

Goals:

- `internal/command` imports domain packages, never the reverse.
- Replace `internal/commands` with `internal/usercommands` (model/loader/registry/resolve/runtime).
- New domain packages: `deploy`, `reset`, `lifecycle`, `stack`, `envfile`, `localconfig`.
- Move generic pipeline execution into `internal/pipeline` (joins the existing reporter).
- Extract `.env` rendering into `internal/envfile` so it can be reused by the `render env` command, by docker auto-generation, and (optionally) by `localconfig`'s toggle flow without dragging cobra in.
- Update `AGENTS.md` (and its `CLAUDE.md` symlink) inside this repo to describe the new layout.

## Context (from discovery)

Current `internal/` layout (devbox-cli):

```
internal/builtin   internal/command   internal/commands   internal/condition
internal/config    internal/docker    internal/git        internal/pipeline
internal/render    internal/tpl       internal/ui         internal/version
```

Files containing domain logic that need to leave `internal/command`:

- `internal/command/deploy.go` — `resolveDeployPlan`, `resolveServiceDeployPlan`, `resolveServicesDeploy`, `findStep`, `sourceDotEnv`, `isRegularFile`
- `internal/command/reset.go` — `resolveResetPlan`, `loadAndResolveResetPlan`, `findResetStep`, `printResetPlanShell`
- `internal/command/lifecycle.go` — `runLifecyclePhases`
- `internal/command/run.go` / `stop.go` / `restart.go` — `resolveUpdateMode`, `runRun`, `runStop`, `runRestart` core bodies
- `internal/command/pipeline.go` — `runPipeline`, `execStep`, `execBuiltinStep`, `execCommandStep`, `resolvePhaseSteps`, `openPipelineLog`, `ansiStripper`, `buildDevboxCmd`, `stepBadge`, `stepCommand`, plan printing helpers
- `internal/command/status.go` — `aggregateHealth(*FromTopo)`, `fetchComposeTopology`, `parseTopologyFromFiles`, `composeNodeStatuses`, `disabledNodes`, `augmentWithDisabled`, `removeHiddenNodes`, `buildNodeCategories`, `buildComposeArgs`, `resolveProjectAndDocker`, `runStatus` core
- `internal/command/service.go` / `service_toggle.go` / `tool_toggle.go` — `loadLocalYAML`, `writeLocalYAML`, `setLocalEntryEnabled`, `applyServiceTogglesBatch`, `setServiceEnabled`, `validateServiceToggle`, `diffServiceSelection`, `diffToolSelection`
- `internal/command/env.go` — `buildEnvContent`, `regenEnv`, `hostUID`, `hostGID`, `isTruthy`, `formatValue`, plus `runRenderEnv` (cobra-shaped). Everything except `newRenderCmd` / `newRenderEnvCmd` / `runRenderEnv` is reusable infrastructure that becomes `internal/envfile`.

Packages that match the target layout already (no work beyond verifying file split):
`builtin`, `config`, `condition`, `docker`, `git`, `render`, `tpl`, `ui`, `version`.

`internal/commands` (~30 files, ~390 funcs) needs renaming to `internal/usercommands` and subdividing into `model/`, `loader/`, `registry/`, `resolve/`, `runtime/`.

Plans live at `devbox-cli/docs/plans/` (this repo). Build/test commands: `make build`, `make test`, `make lint` (run from `devbox-cli/`).

## Development Approach

- **Testing approach**: Regular — this is a refactor against an existing test suite. Each task moves production + test files together and must end with `make test` green before the next task starts.
- One package extraction per task. Keep the old location compiling via thin re-exports only if a follow-up task in this plan removes them; otherwise update all callers in the same task.
- Imports are updated mechanically (search `internal/command` → callers, update path). No behavior changes.
- Public API of moved code: keep names, just change package. Exported identifiers stay exported. Unexported helpers used across the new package boundary become exported (with their first letter capitalized) or are co-located in the same new package.
- **CRITICAL: every task MUST update tests** alongside the code it moves. Tests stay next to the code they cover.
- **CRITICAL: all tests must pass before starting next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` and `make lint` after each task. Run `./bin/devbox docs generate` after the cobra surface is touched to confirm no command was lost.

## Testing Strategy

- **Unit tests**: every moved file brings its `*_test.go` along. Update import paths and package declarations only — do not rewrite tests.
- **Integration smoke**: after each task, run `make build && ./bin/devbox info` and `./bin/devbox status --help` to confirm the binary still wires up.
- **No e2e suite**: this Go module has no Playwright/Cypress layer; tests = `go test ./...`.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep the plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): all package moves, import rewrites, doc updates inside this repo.
- **Post-Completion** (no checkboxes): nothing external — this refactor is fully self-contained.

## Implementation Steps

### Task 1: Move pipeline executor into `internal/pipeline`

Move generic executor pieces **and** the generic table printer. Verified by grep: `printDeployPlanTable` does not reference `implicitEnvStep` — it's a structural pretty-printer over `[]resolvedStep`. Only `printDeployPlanShell` injects `. .env` after the implicit env step; that one is deploy-specific and stays out of `pipeline`.

- [x] move `runPipeline`, `resolvePhaseSteps`, `execStep`, `execBuiltinStep`, `execCommandStep`, `openPipelineLog`, `ansiStripper`, `buildDevboxCmd`, `stepBadge`, `stepCommand`, `resolvedStep`, `printDeployPlanTable` from `internal/command/pipeline.go` into new files under `internal/pipeline/` (`executor.go`, `resolve.go`, `step.go`, `logging.go`, `print.go`)
- [x] rename the moved table printer to `pipeline.PrintPlanTable(steps []ResolvedStep, w *render.Writer)` — generic for any pipeline domain (deploy, reset, lifecycle)
- [x] do **not** move `printDeployPlanShell` (deploy-specific) or `printResetPlanShell` (reset-specific); those stay/move to their domain packages in Tasks 2 and 3
- [x] export `resolvedStep` as `pipeline.ResolvedStep` with **all fields exported** so deploy/reset can construct it (deploy needs to prepend the implicit env step) and the printers/runtime can read it:
  ```go
  type ResolvedStep struct {
      Phase       config.DeployPhase
      Step        config.DeployStep
      Service     string // non-empty for per-service steps (e.g. "main")
      RuntimeWhen string // step.When mirror
      PhaseWhen   string // phase-level runtime when, evaluated once per phase
  }
  func (rs ResolvedStep) StepAddress() string
  ```
  No constructor function needed — exported struct fields keep callers (deploy/reset) terse: `pipeline.ResolvedStep{Phase: …, Step: deploy.ImplicitEnvStep}`
- [x] move `internal/command/pipeline_run_test.go` to `internal/pipeline/executor_test.go` and adjust package declaration + imports
- [x] update `internal/command/deploy.go`, `reset.go`, `lifecycle.go` callers to use `pipeline.Run`, `pipeline.ResolvePhaseSteps`, `pipeline.OpenPipelineLog`, `pipeline.PrintPlanTable`, etc.
- [x] export only what compile errors prove necessary outside `pipeline`. Expected exports: `Run`, `ResolvePhaseSteps`, `OpenPipelineLog`, `PrintPlanTable`, `ResolvedStep`, `StepCommand` (used by deploy/reset shell printers and dry-run adapters). Keep `stepBadge` (only `PrintPlanTable` reads it), `buildDevboxCmd` (only `execStep` calls it), `ansiStripper`, `execStep` / `execBuiltinStep` / `execCommandStep` (called via `Run`) **unexported** unless a caller outside `pipeline` actually needs them — exporting unused helpers grows the cross-package API for no reason
- [x] run `make test` — must pass before Task 2
- [x] run `make lint` — must pass before Task 2

### Task 2: Create `internal/deploy` package

- [ ] create `internal/deploy/plan.go` with `ResolvePlan`, `FindStep`, `SourceDotEnv`, `IsRegularFile`, and the deploy-specific `ImplicitEnvStep` (all exported — the cobra adapter in `internal/command/deploy.go` keeps the run/config-check entry points and calls these helpers; orchestration does **not** move into `deploy` in this task)
- [ ] create `internal/deploy/service_plan.go` with `ResolveServicePlan`, `ResolveServicesPlan`
- [ ] create `internal/deploy/print.go` with `PrintPlanShell` only — the deploy-aware shell printer that emits `. .env` after `ImplicitEnvStep`. The plan table view is rendered by `pipeline.PrintPlanTable` (Task 1), called directly from the cobra adapter
- [ ] create `internal/deploy/step.go` for any deploy-specific step helpers separated from generic pipeline.go
- [ ] move corresponding `*_test.go` cases from `internal/command/deploy_test.go` into `internal/deploy/*_test.go` (split file along the function boundary)
- [ ] keep `internal/command/deploy.go` as cobra adapters only (`newDeployCmd`, `newDeployPlanCmd`, `newDeployRunCmd`, `newDeployStepCmd`, `newDeployConfigCmd`, `newDeployConfigCheckCmd`) calling into `deploy.*` and `pipeline.PrintPlanTable`
- [ ] update imports across the codebase
- [ ] run `make test` — must pass before Task 3
- [ ] run `make lint` — must pass before Task 3

### Task 3: Create `internal/reset` package

- [ ] create `internal/reset/plan.go` with `ResolvePlan`, `LoadAndResolvePlan`, `FindStep`, `PrintPlanShell` (moved from `internal/command/reset.go`)
- [ ] reset's plan table view uses the generic `pipeline.PrintPlanTable` directly from the cobra adapter — no dependency on `internal/deploy` (the previous draft routed reset through `deploy.PrintPlanTable`, which would be an unnecessary domain-to-domain edge)
- [ ] create `internal/reset/step.go` for reset-specific step helpers if any
- [ ] split `internal/command/reset_test.go` (if present) and any reset cases inside shared test files into `internal/reset/*_test.go`
- [ ] reduce `internal/command/reset.go` to cobra adapters only (`newResetCmd`, `newResetPlanCmd`, `newResetRunCmd`, `newResetStepCmd`, `newResetConfigCheckCmd`)
- [ ] update imports
- [ ] run `make test` — must pass before Task 4
- [ ] run `make lint` — must pass before Task 4

### Task 4: Create `internal/lifecycle` package

`runRun` calls `runInfo(cmd, flags)` for the post-up info dashboard, and `runRestart` calls `runStop`+`runRun`. Both are cobra-shaped. To keep `lifecycle` free of `internal/command` imports, the moved functions accept callbacks for the cobra-specific bits.

- [ ] create `internal/lifecycle/phases.go` with `RunLifecyclePhases` (from `internal/command/lifecycle.go`)
- [ ] create `internal/lifecycle/run.go` with:
  - `ResolveUpdateMode(cfg *config.LifecycleRunConfig, noUpdate bool, updateFlag string) string`
  - `RunRun(ctx RunContext) error` where `RunContext` (defined in this package) carries `WorkDir`, `ConfigPath`, `NoUpdate`, `UpdateMode`, `Yes`, and an injected `ShowInfo func() error` callback used in place of the direct `runInfo` call
- [ ] create `internal/lifecycle/stop.go` with `RunStop(ctx StopContext) error` (or the same `RunContext` minus update fields)
- [ ] add `RunRestart(ctx RunContext) error` to `run.go` — implementation calls `RunStop` then `RunRun` with `NoUpdate=true`; the caller still supplies `ShowInfo`
- [ ] in `internal/command/run.go` / `restart.go` / `stop.go`, build the context, set `ShowInfo: func() error { return runInfo(cmd, flags) }`, and call into `lifecycle.*`
- [ ] move tests: `internal/command/lifecycle_test.go`, `lifecycle_phases_test.go`, `run_test.go`, `stop_test.go`, `restart_test.go` lifecycle-related cases → `internal/lifecycle/*_test.go`; tests inject a stub `ShowInfo` to avoid dragging the cobra info renderer in
- [ ] reduce `internal/command/run.go`, `stop.go`, `restart.go`, `lifecycle.go` to cobra adapters that call `lifecycle.*`
- [ ] update imports
- [ ] run `make test` — must pass before Task 5
- [ ] run `make lint` — must pass before Task 5

### Task 5: Create `internal/stack` package

- [ ] create `internal/stack/rows.go` for service-row construction helpers used by status
- [ ] create `internal/stack/health.go` with `AggregateHealth`, `AggregateHealthFromTopo`, `HasRuntimeStatuses`, `StackHealth` type (move from `internal/command/status.go`)
- [ ] create `internal/stack/topology.go` with `FetchComposeTopology`, `ParseTopologyFromFiles`, `ComposeNodeStatuses`, `DisabledNodes`, `AugmentWithDisabled`, `RemoveHiddenNodes`, `BuildNodeCategories`, `BuildComposeArgs`
- [ ] create `internal/stack/status.go` with `RunStatus` core (rendering still goes through `ui.Render*` and `render.Writer`)
- [ ] move `internal/command/status_test.go` and `status_extra_test.go` cases into `internal/stack/*_test.go`
- [ ] reduce `internal/command/status.go` to `newStatusCmd` cobra wiring that calls `stack.RunStatus`
- [ ] update imports
- [ ] run `make test` — must pass before Task 6
- [ ] run `make lint` — must pass before Task 6

### Task 6: Create `internal/envfile` package

Extract `.env` rendering so cobra is not the only entry point. Unblocks `localconfig` (Task 7), docker auto-generation, and `devbox render env` from sharing one implementation.

- [ ] create `internal/envfile/render.go` with:
  - `BuildContent(cfg *config.DevboxConfig) (string, error)` (moved from `buildEnvContent`)
  - `IsTruthy(v any) bool` (moved from `isTruthy`)
  - `FormatValue(v any, format string) string` (moved from `formatValue`)
  - `HostUID() string`, `HostGID() string` (moved from `hostUID`, `hostGID`)
- [ ] create `internal/envfile/write.go` with:
  - `Write(cfg *config.DevboxConfig, outputPath string) error` — writes the rendered content to the given path (mkdir parents, atomic-ish replace)
  - `Regenerate(configPath string) (string, error)` — reloads config from `configPath`, writes `.env` next to it, returns the absolute path; replaces `regenEnv(configPath, baseDir)` (`baseDir` is implied by `configPath`'s directory)
- [ ] no cobra, no UI, no prompts; `envfile` imports only `internal/config` and stdlib
- [ ] move env-rendering tests from `internal/command/env_test.go` to `internal/envfile/render_test.go` and `write_test.go`; keep cobra-shaped `runRenderEnv` tests in `internal/command/env_test.go`
- [ ] reduce `internal/command/env.go` to cobra wiring only: `newRenderCmd`, `newRenderEnvCmd`, `runRenderEnv` (which now calls `envfile.Write` after loading config)
- [ ] update `internal/command/service.go` and `internal/command/tools.go` callers of `regenEnv(configPath, baseDir)` to call `envfile.Regenerate(configPath)`; the Task 7 toggle orchestrators inherit this call site
- [ ] update any docker auto-generation path (`internal/docker` or `internal/command/docker.go`) that triggers `.env` regeneration to call `envfile.Regenerate`
- [ ] run `make test` — must pass before Task 7
- [ ] run `make lint` — must pass before Task 7

### Task 7: Create `internal/localconfig` package

`localconfig` mutates `devbox/local.yml` only. It does **not** call `envfile.Regenerate` — keep the "load → mutate → write → print → regenerate .env" composition in the cobra adapter so `localconfig` stays reusable by any future caller that doesn't want a side-effect on `.env`. Dependency rule: `localconfig` imports `internal/config` (for `DevboxConfig` validation types) and stdlib; nothing else.

- [ ] create `internal/localconfig/local_yaml.go` with `LoadLocalYAML`, `WriteLocalYAML`, `SetLocalEntryEnabled` (move from `internal/command/service.go`)
- [ ] create `internal/localconfig/services.go` with the **pure** mutators only: `ApplyServiceTogglesToYAML(local map[string]any, toEnable, toDisable []string)`, `SetServiceEnabledInYAML(local map[string]any, name string, enabled bool)`, `ValidateServiceToggle(cfg *config.DevboxConfig, name string, enabled bool) error`, `DiffServiceSelection(selections []ServiceSelection, kept []string) (toEnable, toDisable []string)`
- [ ] create `internal/localconfig/tools.go` with the equivalent pure tool toggle helpers (`DiffToolSelection(selections []ToolSelection, kept []string) (toEnable, toDisable []string)`, `ApplyToolTogglesToYAML`, `SetToolEnabledInYAML`, `ValidateToolToggle`)
- [ ] **Selection-row ownership decision**: define minimal input structs in `localconfig`:
  ```go
  type ServiceSelection struct { Name string; Enabled bool; Mandatory bool }
  type ToolSelection    struct { Name string; Enabled bool }
  ```
  These contain only the fields the diff/validate logic reads. Presentation-rich types (`serviceRow`, `toolRow` with `Type`/`Dir`/`Container`/`Port`/`Host`) stay in `internal/command/services.go` because they are pure UI shape; callers project at the call site (`localconfig.DiffServiceSelection(rowsToSelections(rows), kept)`). `ui.ServiceTableRow` / `ui.ToolTableRow` (the Lipgloss table inputs) stay in `internal/ui/`.
- [ ] keep the high-level "load → mutate → write → print → regenerate .env" orchestration (`applyServiceTogglesBatch`, `setServiceEnabled`, and tool equivalents) in `internal/command/service.go` / `tools.go`. The orchestrator becomes a thin wrapper: project rows → selections, read with `localconfig.LoadLocalYAML`, mutate with `localconfig.ApplyServiceTogglesToYAML`, write with `localconfig.WriteLocalYAML`, then call `envfile.Regenerate(flags.configPath)` (introduced in Task 6)
- [ ] move related tests (`service_toggle_test.go`, `tool_toggle_test.go`, the diff/validate cases from `services_test.go` / `tools_test.go`) into `internal/localconfig/*_test.go`
- [ ] keep cobra-shaped integration tests (covering toggle + envfile regeneration end-to-end) in `internal/command/`
- [ ] update imports
- [ ] run `make test` — must pass before Task 8
- [ ] run `make lint` — must pass before Task 8

### Task 8: Rename `internal/commands` → `internal/usercommands` (flat move)

- [ ] `git mv internal/commands internal/usercommands`
- [ ] update package declaration `package commands` → `package usercommands` across all files (production + tests)
- [ ] update every importer (`internal/command/*.go`, `internal/builtin/*.go`, `internal/pipeline/*.go`, `cmd/devbox/main.go`) — search for `"<modulepath>/internal/commands"` and replace with `internal/usercommands`
- [ ] update local identifier references where callers use `commands.X` → `usercommands.X` (or keep an alias `import commands "...usercommands"` only if a follow-up step in this task removes it)
- [ ] run `make build && make test` — must pass before Task 9
- [ ] run `make lint` — must pass before Task 9

### Task 9: Split `internal/usercommands` into subpackages

- [ ] create `internal/usercommands/model/` and move pure types from `types.go`: `CommandType`, `ParamType`, `UserMode`, `ExecMode`, `CommandDef`, `ParamDef`, `ContextDef`, `FileSpec` and friends (`FileCandidate`, `FileAccess`, `FileSort`, `FileOnError`), `ScriptDef`, `WorkflowStep`, `CommandMessages`, `CommandFile`, `GroupMeta`, plus their validation methods (`validation.go`)
- [ ] create `internal/usercommands/loader/` and move `DiscoverCommandFiles`, `LoadCommandFile`, `parseCommandFile`, `ComputeGroup`, `ComputeCommandID` (from `loader.go`)
- [ ] create `internal/usercommands/registry/` and move `Registry`, `LoadRegistry`, `GroupNode`, group-tree construction, cross-registry validation (from `registry.go`)
- [ ] create `internal/usercommands/resolve/` and move `ResolveParams`, `ResolveContext`, `BuildEnv`, `ComputeFilePaths`, `PrepareFileEffects`, render/relative path helpers (from `resolve.go`, `resolve_files.go`)
- [ ] create `internal/usercommands/runtime/` and move `RunContext`, `Runner` interface, `NewRunner`, `RunCommand`, `ConfirmCommand`, `emitCommandMessage`, stdio helpers, `RunContext.Compose`, plus `HostRunner`, `DevboxRunner`, `ServiceExecRunner`, `ServiceRunRunner`, `ScriptRunner`, `WorkflowRunner`
- [ ] keep `internal/usercommands/usercommands.go` as a facade re-exporting **every** public symbol that callers outside `internal/usercommands` currently use, so no caller needs to import the new subpackages directly. Required re-exports (verified by grepping `commands.X` across `internal/` and `cmd/` before this task):
  - **Types** (alias via `type X = subpkg.X`): `CommandFile`, `CommandDef`, `CommandMessages`, `ParamDef`, `ContextDef`, `ScriptDef`, `WorkflowStep`, `GroupNode`, `Registry`, `RunContext`, `Runner`
  - **Constants** (alias via `const X = subpkg.X`): `CommandTypeCommand`, `CommandTypeDevbox`, `CommandTypeScript`, `CommandTypeServiceExec`, `CommandTypeServiceRun`, `CommandTypeWorkflow`, `ParamTypeString`, `ExecModeExecOrRun`
  - **Functions / constructors**: `LoadRegistry`, `NewEmptyRegistry`, `NewRunner`, `RunCommand`, `ResolveContext`, `ResolveParams`
  - run `grep -rEoh '\bcommands\.[A-Z][A-Za-z0-9_]*' internal cmd | sort -u` after the rename in Task 8 and **before** this task; every symbol in that list must appear in `usercommands.go`. Anything missed will surface as a compile error during this task — add it to the facade rather than rewriting the caller
- [ ] move each subpackage's `*_test.go` files alongside the production code; adjust package declarations and imports
- [ ] handle import cycles: if `runtime/` needs `model/`, `resolve/`, `registry/` that's fine (one-way); if a cycle appears (e.g. resolve ↔ runtime), keep the cycling helper in `runtime/` per the design rule "all runners together"
- [ ] update external importers — many call sites should keep working through `usercommands.*` aliases
- [ ] run `make build && make test` — must pass before Task 10
- [ ] run `make lint` — must pass before Task 10
- [ ] run `./bin/devbox docs generate` and verify `docs/reference/` regenerates without missing commands

### Task 10: Slim `internal/command` to cobra adapters and update docs

- [ ] verify each file under `internal/command/` contains only cobra wiring + small adapter functions; flag any leftover business logic for relocation (a follow-up checkbox here, or a ➕ task)
- [ ] update `AGENTS.md` package list under "Project Structure & Module Organization" to describe the new layout (deploy/reset/lifecycle/stack/envfile/localconfig + usercommands subpackages); confirm `CLAUDE.md` symlink still points to it
- [ ] regenerate reference: `./bin/devbox docs generate`
- [ ] run `make test` && `make lint`
- [ ] run `make build` and smoke-test: `./bin/devbox info`, `./bin/devbox status --help`, `./bin/devbox deploy plan --help`

### Task 11: Verify acceptance criteria

- [ ] verify `internal/command` imports domain packages but no domain package imports `internal/command` (`go list -deps ./internal/...` and grep)
- [ ] verify `internal/envfile` imports only `internal/config` and stdlib (no cobra, no ui, no usercommands)
- [ ] verify `internal/localconfig` imports only `internal/config` and stdlib (no envfile, no command)
- [ ] verify `internal/usercommands/runtime` is the only subpackage that holds runners
- [ ] verify `internal/commands` no longer exists
- [ ] run full test suite: `go test ./...`
- [ ] run linter: `make lint` — all issues fixed
- [ ] verify reference docs regenerate cleanly

## Technical Details

**Import-cycle guards.** Each new package depends only on lower-level packages:

```
command → deploy, reset, lifecycle, stack, localconfig, envfile, usercommands, pipeline, render, ui
deploy, reset, lifecycle → pipeline, config, usercommands, builtin, render, ui
pipeline → config, usercommands, builtin, render, condition, tpl
stack → config, docker, ui, render
envfile → config (stdlib only beyond that)
localconfig → config (yaml only; deliberately does NOT import envfile)
usercommands/runtime → usercommands/model, /resolve, /registry, builtin, condition, tpl, docker
usercommands/resolve, registry, loader → usercommands/model, tpl, condition, config
usercommands/model → (no internal deps beyond stdlib)
```

`localconfig → envfile` is intentionally forbidden: toggle orchestrators in `internal/command` call `localconfig.*` then `envfile.Regenerate` themselves, keeping `localconfig` reusable for any future caller that wants YAML mutation without `.env` side-effects.

**Sequencing note: `usercommands` does not exist until Task 8.** The dependency table above describes the *target* state. During Tasks 1–7 the new packages (`pipeline`, `deploy`, `reset`, `lifecycle`, `stack`, `envfile`, `localconfig`) import `internal/commands` (the current name) wherever the table shows `usercommands`. Task 8 (`git mv internal/commands internal/usercommands` + package rename) does a global find-and-replace across all those packages in a single pass. Don't try to import `internal/usercommands` before Task 8 — it doesn't exist.

If a function pulls in a higher-level package (e.g. a deploy helper that calls `command.X`), invert the dependency by passing the value in or moving the helper.

**Test file movement rules.** Each test file follows the production file it covers. When a single source file in `internal/command` covers multiple new domains (e.g. `deploy_test.go` covers both deploy plan resolution and command wiring), split it: keep cobra-adapter cases in `internal/command/`, move resolution/plan cases to the domain package.

**Facade strategy for `internal/usercommands`.** `usercommands.go` re-exports **every** public symbol currently used outside `internal/commands` (verified by `grep -rEoh '\bcommands\.[A-Z][A-Za-z0-9_]*' internal cmd | sort -u` before Task 9). External callers keep importing one path. Internal callers under `internal/usercommands/*` import the subpackages (`model`, `loader`, `registry`, `resolve`, `runtime`) directly. The full list of required re-exports — types, constants, and functions — lives in Task 9 alongside the implementation steps; treat that list as authoritative and re-run the grep before the task to catch anything that has shifted since the plan was written.

**Renames to expect.**
- `commands.Registry` → `usercommands.Registry` (alias) or `registry.Registry` (subpackage import)
- `commands.RunContext` → `usercommands.RunContext` (alias) or `runtime.RunContext`
- `command.runPipeline` → `pipeline.Run`
- `command.resolveDeployPlan` → `deploy.ResolvePlan`
- `command.resolveResetPlan` → `reset.ResolvePlan`
- `command.runLifecyclePhases` → `lifecycle.RunPhases`
- `command.aggregateHealth` → `stack.AggregateHealth`
- `command.loadLocalYAML` → `localconfig.LoadLocalYAML`
- `command.buildEnvContent` → `envfile.BuildContent`
- `command.regenEnv` → `envfile.Regenerate` (signature changes from `(configPath, baseDir)` to `(configPath)`; `.env` is written next to `configPath`)

## Post-Completion

Nothing external. This refactor is fully internal to `devbox-cli/`. After merging, normal CI (`make test`, `make lint`, `./bin/devbox docs generate`) is sufficient verification.

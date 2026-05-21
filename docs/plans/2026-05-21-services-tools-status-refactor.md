# Refactor of top-level commands: `services`, `tools`, `status`

## Overview

Currently:

- `services {status, list}` and `tools {status, list}` duplicate read-only output and the interactive toggle in two nodes each.
- Read-only stack overview is smeared across `status`, `services status`, `tools status`.
- `status <svc>` conflates "stack overview" with "per-service deploy detail" under the same command.
- Tool definitions live in the main `devbox.yml` while service definitions live in a separate `devbox/services.yml` — asymmetric without reason.

Goals:

1. **Read-only vs mutating split**: `status` owns all read-only views; `services` / `tools` own mutating actions only.
2. **Sectioned `status`**: default prints all sections (health + services + tools + deploy + topology + git); each section also addressable as `status <section>`; sections excludable via `--no-<section>`.
3. **Per-service deploy detail** moves under `status deploy <svc>` (was top-level `status <svc>`).
4. **Symmetric configs**: tools move to `devbox/tools.yml`, mirroring `devbox/services.yml`; main `devbox.yml` keeps only the `tools.<name>.enabled` overlay.
5. **Custom status columns** declarable per-service / per-tool via `status:` block (template rendering against merged config).
6. **Git workspace** becomes a first-class status section.

Pilot phase — no deprecation aliases, breaking changes ship immediately.

## Context (from discovery)

### Current command surface

| Command | File | Notes |
|---|---|---|
| `status` (no-arg) | `internal/command/status.go:17–80` | Calls `stack.RunStatus(w, in)` — orchestrates health + deploy + services + tools + topology. |
| `status <svc>` | same file | One positional arg routes to `stack.RenderServiceDeployDetail`. |
| `services` (group) | `internal/command/service.go:21–42` | Hosts `status`, `list`, `enable`, `disable`. |
| `services status` | `service.go:45–69` | Read-only table; calls `runServiceList()` non-interactively. |
| `services list` | `service.go:72–169` | TTY → multi-select form via `ui.RunMultiSelect`; non-TTY → table fallback. |
| `services {enable,disable} [name]` | `service.go:274–351` | Optional selector via `ui.RunSelector`. |
| `tools` (group) | `internal/command/tools.go:20–42` | Mirrors `services`. |
| `tools {status, list, enable, disable}` | `tools.go:44–261` | Same pattern as services. |

### Current `status` rendering

- Orchestrator: `internal/stack/status.go:35–42` — `RunStatus(w, StatusInput)`.
- `StatusInput` fields: `Cfg`, `IsRunning`, `Topo`, `TopoStatus`, `State` (journal), `SvcDeploys`, `Tracked` — assembled in `internal/command/status.go:54–66`.
- Section renderers in `stack/status.go`: `RenderHealth`, `RenderDeployStatus`, `RenderServices`, `RenderTools`, `RenderTopology`. **No git workspace section today.**
- Per-service deploy detail: `internal/stack/deploystatus.go:114–157` `RenderServiceDeployDetail`.

### Tools live in main config today

- `ToolConfig` struct: `internal/config/devbox.go:579–585` (`Enabled`, `Container`, `Host`, `Port`, `Compose`).
- `ToolsConfig` map alias: line 591.
- Loaded as part of 3-layer `deepMerge()` inside `LoadConfig`: `internal/config/devbox.go:759–857`.
- No separate `LoadToolsConfig`.
- Toggle write: `internal/localconfig/tools.go:48–72` `ApplyToolTogglesToYAML` → `local["tools"][name]["enabled"]`.

### Services already use the symmetric pattern

- Definitions in `devbox/services.yml`, loaded by `LoadServicesConfig` (`config/devbox.go:828–957`) using plain `yaml.Unmarshal` (**lenient** — unknown fields silently ignored). The new `LoadToolsConfig` will use strict `KnownFields(true)` per spec; tightening the services loader is intentionally out of scope.
- Enabled state resolved from 3-layer merge via `ResolvePath(merged, "services."+name+".enabled")`.
- `ServiceConfig` struct: `config/devbox.go:433–450`.
- Toggle write: `internal/localconfig/services.go`.

### Template engine entry point

- `internal/tpl/engine.go:12–40` — `Render(expr, data)`, hermetic (no env / FS / network).
- `FuncMap()` returns per-call clone of sprout-backed map plus `appURL`.
- Per-row rendering for status columns will pass merged-config data (`.ServiceCfg`, `.Tool`, `.Globals`, top-level config keys).

### Existing tests

- `internal/command/services_test.go` (550 lines), `service_toggle_test.go` (258 lines) — table-driven, helper-based, `ui.IsInteractiveFn` override for TTY simulation.
- `internal/command/tools_test.go`, `tool_toggle_test.go` — mirror services tests.
- `internal/stack/status_test.go`, `deploystatus_test.go`, `health_test.go`, `topology_test.go` — cover `RunStatus` + section renderers + deploy view.
- `internal/ui/table_test.go` — `RenderServiceTable`, `RenderToolTable`, `RenderDeployStatus`.

Style across the package: table-driven, plain `if err != nil` checks, minimal testify usage.

## Development Approach

- **Testing approach**: Regular (code first, tests after — same task).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
  - unit tests for new and modified functions
  - cover success and error scenarios
- **CRITICAL: all tests must pass before starting the next task** (`make test`).
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run `make lint` after each task — must be clean before next.
- No backward-compatibility aliases; this is a pilot, hard break is acceptable.
- Keep tasks shippable independently where possible so we can reorder under review pressure.

## Testing Strategy

- **Unit tests**: required per task (see Development Approach).
- **E2E tests**: this project has no UI-based e2e suite. Manual verification of CLI flows belongs in `## Post-Completion`.
- Lipgloss-rendered tables use snapshot-style assertions on plain-text output (existing `ui/table_test.go` pattern) — follow it for new columns and the git section.
- **`t.Parallel()` discipline**: do NOT call `t.Parallel()` in subtests that override package-level seams (`ui.IsInteractiveFn`, `runMultiSelect`, `runParamForm`, etc.) — those vars are shared mutable state.
- **Race detector**: parallel git collection (Task 3) is the main risk surface; Task 7 runs `go test -race ./...`.
- **Goroutine leaks**: `internal/stack/gitworkspace_test.go` uses `goleak.VerifyNone(t)` per test; package gets `goleak.VerifyTestMain(m)` to catch leaks under errgroup limit pressure.
- **Cobra command-tree isolation**: every test executing a cobra command builds a fresh root (`newRootCmd()` or equivalent) — cobra accumulates flag state across `Execute()` calls.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase — code changes, tests, documentation updates that ship with the PR.
- **Post-Completion** (no checkboxes): manual smoke tests, screenshots for the PR description, deploy / external verification.
- **Checkbox placement**: only in `### Task N:` sections.

---

## Implementation Steps

### Task 1: Split tools into `devbox/tools.yml` (load + merge + raw injection + types)

- [x] Add `StatusColumn` struct in `internal/config/devbox.go` (`Name string`, `Value string`); embed `[]StatusColumn` field `Status` on `ServiceConfig` and on `ToolConfig`.
- [x] Reshape `ToolConfig`: definition fields (`Container`, `Host`, `Port`, `Compose`, `Status`) are loaded from `tools.yml`; `Enabled` becomes a resolved field set programmatically from the 3-layer merge (same pattern as `ServiceConfig.Enabled` at `internal/config/devbox.go:440`, `yaml:"-"`). Drop the `yaml:"enabled"` tag on `ToolConfig` — `enabled:` only exists in overlays, never in `tools.yml`.
- [x] Implement `LoadToolsConfig(path string) (ToolsConfig, error)` in `internal/config/` — **strict** `yaml.Decoder.KnownFields(true)` per the spec (user-edited YAML; unknown fields should be an error). File path default: `devbox/tools.yml` relative to the project root. Missing file → empty `ToolsConfig`, no error (symmetric with the missing-`services.yml` behavior). **Note**: today's `LoadServicesConfig` (`devbox.go:884`) is **lenient** (plain `yaml.Unmarshal`); the spec calls for strict tools loader, and we intentionally do not tighten the services loader in this plan (separate breaking change — out of scope).
- [x] **Refactor `LoadConfig` to expose raw per-layer maps**. Today the 3 layers are merged inline and only the merged map survives. We need each layer's raw map (or at least their `tools` subtree) addressable by source filename for the cross-layer validator. Concretely (positions reference `internal/config/devbox.go` as of HEAD):
  1. Read each layer file into its own `map[string]any` (`rawDevbox`, `rawDefaults`, `rawLocal`), tracking source path per layer.
  2. Run `LoadToolsConfig(devbox/tools.yml)` → declared tool set + definitions (`tools` variable).
  3. **Validate each raw layer map's `tools.*` subtree against the declared tool set** (see next bullet) before any merge happens.
  4. Run the existing 3-layer `deepMerge` to produce `merged` (lines 760–784).
  5. **Keep the existing `yaml.Marshal(merged)` + `yaml.Unmarshal(data, &cfg)` step (`devbox.go:786–793`)** — do not skip it. It populates every other typed field (`cfg.Project`, `cfg.Runtime`, `cfg.Binaries`, …) from the merged map. After this step `cfg.Tools` will be populated with zero-value entries from any overlay `tools.<name>.enabled` block; step 6 overrides them.
  6. **Authoritative assignment**: `cfg.Tools = tools`. Critical — the unmarshal in step 5 produces zero-value `ToolConfig` structs for any overlay-only entry (empty `Container`/`Host`/`Port`) which would fail `validateConfigKeys`. `tools.yml` is the only source of truth for typed tool definitions post-refactor.
  7. Resolve `Tool.Enabled` for each declared tool from `ResolvePath(merged, "tools."+name+".enabled")`, defaulting to `false`. Write the resolved flag back into `cfg.Tools[name]`.
  8. Run `injectToolsIntoRaw(merged, cfg.Tools)` (new function, see below).
  9. Continue with the existing `injectServicesIntoRaw` / `detectLegacyComposeOverlays` / `validateConfigKeys` pipeline (lines 846–865).
- [x] **Critical: add `injectToolsIntoRaw`** mirroring `injectServicesIntoRaw` at `devbox.go:1023–1025`. Without it, raw dot-paths like `tools.adminer.port`, `tools.mailpit.host` regress and break `exports.env` rules, command `default_from:`, `${tools.*}` template expressions in `docker.yml`, and `info.yml` references. Inject the resolved definition fields (`container`, `host`, `port`, `compose`, `enabled`) for every tool defined in `tools.yml` into `merged["tools"][name][...]`.
- [x] **Cross-layer overlay validator** (`validateToolsOverlay(rawLayers, declaredTools)`):
  - Per `docs/reference/config/devbox.md:60` and `:157`, tool definitions currently live in `defaults.yml`, NOT main `devbox.yml`.
  - After the split, **all three layers** (`devbox.yml`, `devbox/defaults.yml`, `devbox/local.yml`) may legally contain only `tools.<name>.enabled` — anything else is stale.
  - Validator iterates each raw layer's `tools.*` subtree, errors on any field other than `enabled:` with the **specific source filename** (`devbox/defaults.yml: tools.adminer.container: tool definitions belong in devbox/tools.yml`). Layer-aware error reporting is the entire reason for the LoadConfig refactor above — do not collapse to a post-merge check.
  - Also reject overlay references to tool names not in `declaredTools` (typo guard); same per-layer source attribution.
- [x] Update `internal/localconfig/tools.go` `ApplyToolTogglesToYAML` so `knownTools` is sourced from `tools.yml` definitions, not the merged map. (Already satisfied: callers compute `knownTools` from `cfg.Tools`, which is now populated authoritatively from `tools.yml`.)
- [x] **Fixture sweep**: grep all `testdata/` directories for tool definitions in `defaults.yml` / `devbox.yml` fixtures and migrate them into sibling `tools.yml` files. (Migrated: `internal/validate/config/testdata/devbox-v2-good/` and `devbox-v2-bad-keys/`; inline test YAMLs in `internal/config/devbox_test.go`, `internal/command/tool_toggle_test.go`, `internal/command/completion_test.go`, `internal/validate/config/all_test.go`.)
- [x] Update `docs/reference/config/devbox.md` to remove the `tools:` block from the defaults section and point at the new `tools.yml`. Create a **minimal** `docs/reference/config/tools.md` stub so the new error messages can reference a real URL.
- [x] Write tests for `LoadToolsConfig`: success, strict-decode rejection of unknown fields, missing file (empty result), malformed YAML.
- [x] Write tests for the cross-layer overlay validator: unknown field under `tools.<name>` errors in each of the three layers with file path + key; unknown tool name errors; bare `enabled: true` passes in any layer.
- [x] Write tests for `injectToolsIntoRaw` end-to-end: after `LoadConfig`, `ResolvePath(cfg.Raw, "tools.adminer.port")` returns the value from `tools.yml`; `tools.adminer.enabled` reflects the merged overlay.
- [x] Write tests for `LoadConfig` end-to-end: tools resolved from `tools.yml` + overlay `enabled` from each layer; precedence order; missing `tools.yml`; `tools.yml` present but no overlay (defaults to disabled).
- [x] Update existing tool-related tests that previously embedded tool definitions in `devbox.yml` / `defaults.yml` fixtures.
- [x] Run `make test` — must pass before Task 2.

### Task 2: Render custom `status:` columns for services and tools

- [x] Add a helper in `internal/stack/` (e.g. `BuildCustomColumns(cfg, kind)`) that computes ordered column names. **Deterministic ordering**: iterate items alphabetically by name (matching the existing `buildServiceRows` / `BuildToolRows` sort). The column order is "first appearance during this deterministic iteration" — guaranteed reproducible across runs.
- [x] Per-row render: evaluate each declared `status[].value` via `tpl.Render` (the hermetic entry point), **never** `tpl.RenderCommand` — that adds `resolve` / `resolveMap` / `resolveFile` which are command-scope only and would break the no-FS contract.
- [x] **Template data shape (explicit, case-sensitive)** — Go templates are case-sensitive and `cfg.Raw` keys are lowercase YAML identifiers (`binaries`, `services`, `tools`, `runtime`, `project`, …). The render data is a `map[string]any` with these top-level keys:
  - `ServiceCfg` (services rows only) — the typed merged `ServiceConfig` for this svc. Access struct fields with their Go PascalCase names: `{{ .ServiceCfg.Container }}`, `{{ .ServiceCfg.Dir }}`.
  - `Tool` (tools rows only) — the typed merged `ToolConfig` for this tool: `{{ .Tool.Host }}`, `{{ .Tool.Port }}`.
  - `Globals` — `cfg.Raw["globals"]` if present, else `nil` (template-safe via `{{ if .Globals }}…{{ end }}`). The value is whatever the user put in `globals:` — typically a `map[string]any` accessed with the user's own (lowercase) keys: `{{ .Globals.baseImageTag }}`.
  - `Raw` — the full `cfg.Raw` map (after `injectToolsIntoRaw` / `injectServicesIntoRaw`). Drill in with the original lowercase YAML keys for any other top-level access: `{{ .Raw.runtime.ports.app }}`, `{{ .Raw.project.name }}`, `{{ .Raw.tools.mailpit.host }}`. Do **not** merge cfg.Raw's lowercase keys at the data root — that would create case-mismatch foot-guns next to the PascalCase synthetic keys.
  - **Do not** add ad-hoc capitalised aliases (`.Project`, `.Runtime`, `.Tools`) — they don't exist; users go through `.Raw.*`.
  - If a public accessor for `cfg.Raw` doesn't exist yet, add one rather than reaching into private fields.
- [x] On `tpl.Render` error: cell becomes `—`; collect errors per-row in the helper's return value (do not write to any writer from inside the helper). Warning aggregation is **Task 4's responsibility** — Task 2 stays self-contained.
- [x] **Scope decision**: Task 2 introduces *only* the helpers and the `ui` table-row extensions. It does **not** modify `stack.RenderServices` / `stack.RenderTools` signatures, and it does **not** wire custom columns into the existing `RunStatus` orchestrator. Reasons:
  - Changing those signatures while `RunStatus` still calls them breaks the build mid-task (`RunStatus` has no warning-output contract today).
  - Task 4 deletes the single `RunStatus` entrypoint anyway in favour of section-level calls — that's the natural place to introduce `(string, []error)` returns and the `cmd.ErrOrStderr()` summary line.
  - As a consequence, custom columns do not appear in `devbox status` output until Task 4 ships. Tests in Task 2 cover the helpers + ui in isolation; end-to-end visibility is acceptance-checked in Task 4.
- [x] **Exact signatures introduced in Task 2** — `ui` packages render only; the new helpers in `stack` are pure data transforms; no stderr writing anywhere:
  - `stack.BuildCustomColumns(cfg *config.DevboxConfig, kind Kind) []string` — returns deterministic ordered column names (alphabetical-iteration + first-encounter, see above). `Kind` is `KindService` / `KindTool` (small enum).
  - `stack.RenderCustomCells(defs []config.StatusColumn, data map[string]any) (map[string]string, []error)` — pure per-row helper: evaluates each declared `value` via `tpl.Render`, returns the cell values keyed by column name and the slice of evaluation errors. Failing cells are omitted (callers map missing key → `—`).
  - Extend existing `ui.ServiceTableRow` (at `internal/ui/table.go:36`) and `ui.ToolTableRow` (`table.go:126`) with `Extras map[string]string` field — DO NOT introduce parallel `statusview.ServiceRow` / `ToolRow` types. These row structs are the working view-model already used by `collectServiceRows` / `collectToolRows` in `stack/status.go`.
  - Change `ui.RenderServiceTable(rows []ServiceTableRow, extraCols []string) string` and `ui.RenderToolTable(rows []ToolTableRow, extraCols []string) string` to accept the ordered extra-column list; for each row, emit `Extras[col]` or `—` if missing.
  - Update **all** existing callers of `ui.RenderServiceTable` / `ui.RenderToolTable` to pass `nil` for `extraCols` so the change is a no-op until Task 4 plumbs the real list:
    - `internal/stack/status.go:55` (`RenderServices`)
    - `internal/stack/status.go:62` (`RenderTools`)
    - `internal/command/service.go:212` (the `services status` subcommand — being removed in Task 5, but Task 2 ships first and must compile)
    - `internal/command/tools.go:180` (the `tools status` subcommand — same caveat)
    - `internal/ui/table_test.go` test cases
  - Existing `stack.RenderServices` / `stack.RenderTools` signatures unchanged in Task 2.
- [x] Tests (helpers in isolation, no command-layer wiring yet):
  - `BuildCustomColumns`: zero custom columns (empty slice), single service declaring columns, multiple services with overlapping and disjoint column sets — superset ordering verified, **stable across `t.Run` repetitions** (asserts the alphabetical-iteration determinism).
  - `RenderCustomCells`: success path (cell values populated), per-cell template failure (key omitted, error in slice), multiple failures aggregated.
  - `ui.RenderServiceTable` / `ui.RenderToolTable` with `nil` extraCols (output identical to pre-change), with extraCols + populated `Extras` (cell rendered), with extraCols + missing key (cell `—`).
  - Hermetic-contract sanity — confirm `tpl.Render` cannot access env / FS / network through `status[].value` (sample `{{ env "X" }}` errors as today).
  - **No** end-to-end test against the full `status` command in Task 2 — that lands in Task 4 where the orchestrator changes.
- [x] Run `make test` — must pass before Task 3.

### Task 3: Add git workspace section

- [x] Add view-model type `GitWorkspaceRow` in `internal/command/statusview/`.
- [x] Create `internal/stack/gitworkspace.go` with `CollectGitWorkspace(ctx, cfg) []statusview.GitWorkspaceRow`.
- [x] Concurrency contract: pre-allocated indexed slice, `errgroup.WithContext` + `SetLimit(8)`, goroutines always return `nil`, `exec.CommandContext` for cancellation.
- [x] Per-service repo boundary check via `hasOwnGitDir` (`.git` is dir or regular file). No shellout when absent.
- [x] Pure parser `parsePorcelainV2(out []byte)` separable from the collector.
- [x] Porcelain v2 header parsing (`branch.head` / `branch.oid` / `branch.ab`); non-`#` records → dirty.
- [x] Skip services with empty `Dir`. Edge-case matrix implemented (blank-nil-err vs error rows).
- [x] `ui.RenderGitWorkspace([]statusview.GitWorkspaceRow) string` added.
- [x] Tests: pure parser unit tests (clean, dirty, ahead/behind, detached, initial OID, malformed ab, line separators).
- [x] Tests: collector integration via `t.TempDir()` + `git init` (skipped if no git on PATH).
- [x] Tests: error-vs-blank distinction (blank row no-`.git`, dir-missing error, shellout error).
- [x] Tests: boundary check — service dir is non-git inside an outer `git init`-ed parent; verifies zero shellouts via test-seam counter.
- [x] Tests: `goleak.VerifyNone(t)` per collector test + package `goleak.VerifyTestMain` in `internal/stack/main_test.go`.
- [x] Run `make test` — passed before Task 4.

### Task 4: Restructure `status` as a group with subcommands, `--no-*` flags, and `status deploy <svc>`

(Tasks 4 and 5 are merged: the deploy subcommand cannot be tested in isolation from the group restructure, since cobra wiring + per-command `RunE` + `Args` validators + completion are one cohesive change.)

- [ ] Convert `newStatusCmd` in `internal/command/status.go` into a group node. Its `RunE` (zero args) renders the **default** view (all sections), including the new health indicator and git workspace. Set `Args: cobra.NoArgs` on the group node — the positional `[service]` form is gone.
- [ ] Add subcommands: `status services`, `status tools`, `status deploy`, `status topology`, `status git`. Each renders only its own section (no health indicator in individual subcommands).
- [ ] Add `--no-services`, `--no-tools`, `--no-deploy`, `--no-topology`, `--no-git` boolean flags on the default `status` command. Each flag suppresses the named section; combining multiple flags is supported. Flags live on the group's `Flags()` (local), not `PersistentFlags()` — subcommands don't need them and shouldn't accept them.
- [ ] **Shared init — DO NOT use `PersistentPreRunE` on the `status` group.** The root `PersistentPreRunE` resolves the project + validates schema (see CLAUDE.md `isValidateCommand` pattern); a child `PersistentPreRunE` *replaces* the parent's entirely (cobra does not chain). Instead, add a memoized helper `loadStatusContext(cmd, flags) (*statusContext, error)` that lazily loads config + journal + tracked services + topology once per command execution; call it from each subcommand's `RunE` (and from the default `status` RunE). Memoization can be a package-private `sync.Once` keyed off the cobra command pointer, or a plain pointer cached on a context value via `context.WithValue`. Keep it simple — these subcommands run once per process.
- [ ] Define a typed `Section` enum + ordered list as the single source of truth for both the default orchestrator order and the `--no-*` flag set. Adding a future section means touching one slice plus one flag — they can't drift apart.
- [ ] Replace single `stack.RunStatus` entrypoint with section-level calls from `internal/command/status.go`, iterating the `Section` enum honoring `--no-*`. **This task owns all signature migrations** for the section renderers (currently writer-based; see `stack/status.go:45/52/59/67`, `stack/deploystatus.go:19`). Convert each to a pure string-returning function so the orchestrator can compose output, write stdout via `cmd.OutOrStdout()`, and separately write warnings via `cmd.ErrOrStderr()`. Section call shapes (exact, post-Task 4):
  - **Health** (default view only): `stack.RenderHealth(in StatusInput) string` (today: `RenderHealth(w *render.Writer, in StatusInput)` at `status.go:45`).
  - **Services**: `stack.RenderServices(in StatusInput) (string, []error)` (today: `RenderServices(w *render.Writer, in StatusInput)` at `status.go:52`). Inside: build extraCols via `BuildCustomColumns(cfg, KindService)`; for each row, fill `row.Extras` via `RenderCustomCells` (collecting errors); call `ui.RenderServiceTable(rows, extraCols)`. Caller writes string to stdout; if `len(errs) > 0`, writes one `warning: N custom status expression(s) failed to render` to `cmd.ErrOrStderr()`.
  - **Tools**: `stack.RenderTools(in StatusInput) (string, []error)` (today: `RenderTools(w *render.Writer, in StatusInput)` at `status.go:59`) — symmetric to services.
  - **Deploy**: `stack.RenderDeployStatus(in StatusInput) string` (today: `RenderDeployStatus(w *render.Writer, in StatusInput)` at `deploystatus.go:19`).
  - **Topology**: `stack.RenderTopology(in StatusInput) string` (today: `RenderTopology(w *render.Writer, in StatusInput)` at `status.go:67`).
  - **Git**: two-step (asymmetric, intentional — git per-row errors live on the row):
    1. `rows := stack.CollectGitWorkspace(ctx, cfg)`
    2. `out := ui.RenderGitWorkspace(rows)` → write to stdout.
    3. count `Err != nil` in rows; if positive, write `warning: git status failed for N service(s)` to `cmd.ErrOrStderr()`.
  - **Delete `stack.RunStatus`** once all sections are wired through the new orchestrator — no callers should remain.
  - Update any tests under `internal/stack/` that asserted writer-based output to assert against the returned string instead (`deploystatus_test.go`, `status_test.go`, `health_test.go`).
- [ ] **`status deploy` wiring**:
  - `status deploy` with no args → renders the existing deploy table via `stack.RenderDeployStatus`.
  - `status deploy <svc>` → calls `stack.RenderServiceDeployDetail(w, state, tracked, svc)` (existing renderer, unchanged signature).
  - `Args: cobra.MaximumNArgs(1)` — not a hand-rolled `len(args)` check inside `RunE`. Gives the standard cobra error message ("accepts at most 1 arg(s), received N") for free.
  - `ValidArgsFunction` for `<svc>` completion — list tracked services. Use `completionConfigPath(flags, cmd)` (per CLAUDE.md, the `__complete` path bypasses `PersistentPreRunE`); return empty + `cobra.ShellCompDirectiveNoFileComp` on any error.
- [ ] All output goes through `cmd.OutOrStdout()`; warnings via `cmd.ErrOrStderr()`. Audit any leftover `fmt.Println` / `os.Stdout` references in the affected files.
- [ ] Update existing `status` tests to cover the new structure:
  - default order;
  - each `--no-*` flag suppresses its section;
  - each subcommand renders its section alone;
  - `NoArgs` rejects stray positional args on the group;
  - `status deploy` shows the deploy table;
  - `status deploy <svc>` shows the per-service detail;
  - `status deploy <unknown>` errors clearly;
  - `status deploy a b` rejected by `MaximumNArgs`;
  - completion returns expected services for a fixture project.
- [ ] Build a fresh `newRootCmd()` per test (cobra accumulates flag state across `Execute()` calls — see golang-spf13-cobra skill).
- [ ] Run `make test` — must pass before Task 5.

### Task 5: Refactor `services` and `tools` to mutating-only commands

- [ ] Remove subcommands `services status`, `services list`, `tools status`, `tools list` outright (no aliases, pilot phase).
- [ ] Convert bare `devbox services` and `devbox tools` to open the existing multi-select toggle form (TTY only). Mandatory services are pre-checked and disabled (cannot be unchecked) — same data structure used today by `services list`. Set `Args: cobra.NoArgs` on both group commands.
- [ ] Non-TTY behavior: return an error with hint via `cmd.ErrOrStderr()`: `services: interactive toggle requires a TTY; use 'devbox status services' for read-only view`. Symmetric message for tools. Return a sentinel error type so the cobra main loop yields exit code 1 (or wire into the existing `ExitCode` interface if a specific code is wanted).
- [ ] All-mandatory short-circuit: if no togglable items remain, error before opening the form: `nothing to toggle, see 'devbox status services'`. Symmetric for tools (no all-mandatory case in tools today, but keep the check consistent so future "mandatory tool" concept doesn't bite us).
- [ ] Keep `services {enable, disable} <name>` and `tools {enable, disable} <name>` as today; tighten semantics:
  - `services enable <mandatory>` → no-op + warning to `cmd.ErrOrStderr()` (`already mandatory`), exit 0.
  - `services disable <mandatory>` → error (`cannot disable mandatory service`), non-zero exit.
  - Tools: same shape; tools have no mandatory concept, so these branches don't fire — keep checks defensive only.
- [ ] Update completion: `services enable <name>` lists optional disabled services; `services disable <name>` lists optional enabled services. Tools symmetric. Reuse existing `pickService*` / `pickTool*` filters. Routes through `completionConfigPath` like other completions.
- [ ] Update existing `services_test.go` / `service_toggle_test.go` / `tools_test.go` / `tool_toggle_test.go` to drop coverage of removed subcommands and add coverage for: bare `services` / `tools` TTY toggle, non-TTY error, all-mandatory error, mandatory enable warning, mandatory disable error.
- [ ] **`t.Parallel()` caveat**: tests that override package-level seams (`ui.IsInteractiveFn`, `runMultiSelect`, etc.) MUST NOT call `t.Parallel()` — same constraint as the existing TUI test pattern documented in CLAUDE.md.
- [ ] Run `make test` — must pass before Task 6.

### Task 6: Documentation, regenerated CLI reference, AGENTS.md sync

- [ ] Expand the `docs/reference/config/tools.md` stub created in Task 1 into the full reference page — mirrors `services.md`: definitions in `devbox/tools.yml`, overlay `enabled:` in defaults/local, optional `status:` block, template-data contract for `status[].value`.
- [ ] Update `docs/reference/config/services.md` — add the `status:` block subsection.
- [ ] Update `docs/reference/config/devbox.md` — tools section now points to `tools.yml`; main config keeps only the enable overlay.
- [ ] Update `docs/reference/config/state.md` and any other doc referencing `devbox status <svc>` to point at `devbox status deploy <svc>`.
- [ ] Regenerate `docs/reference/cli/` via `devbox docs generate --scope cli` (or `make docs` if that target exists — check `Makefile`).
- [ ] Update `AGENTS.md` (the canonical file; `CLAUDE.md` is a symlink — do NOT edit `CLAUDE.md` directly):
  - Update `internal/command/` description to reflect the new `status` group structure and mutating-only `services` / `tools`.
  - Update `internal/config/` description to mention `LoadToolsConfig`, `StatusColumn`, and the symmetric tools/services pattern.
  - Update `internal/stack/` description to mention `CollectGitWorkspace`.
  - Update `internal/ui/` description to mention `RenderGitWorkspace` and the dynamic-column extensions to `RenderServiceTable` / `RenderToolTable`.
- [ ] No new tests in this task — docs only. Run `make build` to ensure `devbox docs generate` succeeds.
- [ ] Run `make test` — sanity check before final verification.

### Task 7: Verify acceptance criteria

- [ ] Verify CLI surface matches the spec in §2 of Overview: bare `services` / `tools`, all `status` subcommands, all `--no-*` flags, `status deploy <svc>`.
- [ ] Run `make test` — full suite passes.
- [ ] Run `go test -race ./...` — no data races. Risk surface is parallel git collection in Task 3; per-index slice writes should keep this clean but the race detector is the final word.
- [ ] If `internal/stack/gitworkspace_test.go` doesn't already include `goleak.VerifyNone` per-test, add a package-level `TestMain` with `goleak.VerifyTestMain(m)` to guard against goroutine leaks under the errgroup limit.
- [ ] Verify `injectToolsIntoRaw` regression coverage: `ResolvePath(cfg.Raw, "tools.<name>.port")` still resolves after the split — exports / commands / docker.yml templates that depend on `${tools.*}` keep working end-to-end (run a real `devbox render env` against a fixture and diff against the pre-split output).
- [ ] Run `make lint` — clean; if any new lint warnings appear, fix the underlying issue, do not `//nolint` away.
- [ ] Run `make build` — binary builds without warnings.
- [ ] Spot-check the regenerated `docs/reference/cli/` output for hand-edited drift.
- [ ] Verify removed commands return cobra's "unknown command" error (no leftover wiring).
- [ ] Verify completion: `devbox status deploy <TAB>` lists tracked services; `devbox services enable <TAB>` lists optional disabled services; `devbox services disable <TAB>` lists optional enabled services.
- [ ] Smoke-check `devbox status --no-services --no-tools --no-deploy --no-topology --no-git` prints only the health indicator (or fails fast if you want a different policy — decide once and document).

### Task 8: Final documentation

- [ ] Re-read `AGENTS.md` end-to-end after the Task 6 edits to catch any stale cross-references introduced by the refactor.
- [ ] If any new patterns emerged worth recording (e.g. shared `PersistentPreRunE` for grouped commands), add a short note under `## Key Patterns` in `AGENTS.md`.

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

### `tools.yml` schema (new)

```yaml
# devbox/tools.yml
tools:
  mailpit:
    container: mailpit
    host: mailpit.local
    port: 1080
    compose: docker-compose.tools.yml
    status:
      - name: ENDPOINT
        value: "http://{{ .Tool.Host }}:{{ .Tool.Port }}"
  adminer:
    container: adminer
    host: adminer.local
    port: 8080
    compose: docker-compose.tools.yml
```

The overlay (today in `devbox/defaults.yml`, optionally also in `local.yml`) keeps only the toggle:

```yaml
# devbox/defaults.yml (and/or devbox/local.yml)
tools:
  mailpit:
    enabled: true
  adminer:
    enabled: false
```

### Custom `status:` block schema

The `Value` is a hermetic Go template evaluated via `tpl.Render`. Go templates are case-sensitive; see Task 2 for the full template-data contract. Quick reference:

| Path | Source | Casing rule |
|---|---|---|
| `.ServiceCfg.<Field>` (services rows) | typed `ServiceConfig` for the row's service | PascalCase Go field names |
| `.Tool.<Field>` (tools rows) | typed `ToolConfig` for the row's tool | PascalCase Go field names |
| `.Globals.<key>` | `cfg.Raw["globals"]` | lowercase YAML keys (user's own block) |
| `.Raw.<key>...` | full `cfg.Raw` map | lowercase YAML keys (`.Raw.tools.mailpit.host`) |

There are no `.Project` / `.Runtime` / `.Tools` aliases at the data root — use `.Raw.project.*`, `.Raw.runtime.*`, `.Raw.tools.*` instead.

Examples using fields that actually exist on the typed config:

```yaml
# devbox/services.yml
services:
  main:
    type: app
    container: app-main
    dir: src/main
    status:
      - name: CONTAINER
        value: "{{ .ServiceCfg.Container }}"
      - name: TAG
        value: "{{ .Globals.baseImageTag }}"   # requires a globals: block in the project
```

```yaml
# devbox/tools.yml — status block referencing existing fields
tools:
  mailpit:
    container: mailpit
    host: mailpit.local
    port: 1080
    status:
      - name: ENDPOINT
        value: "http://{{ .Tool.Host }}:{{ .Tool.Port }}"
```

Free-form user data (e.g. a `version` column) requires the user to add a top-level `globals:` block (or any other free-form top-level key) and reference it from the template — there is no built-in `Version` field on `ServiceConfig` or `ToolConfig`.

### Status section ordering (default `devbox status`)

1. Health indicator (`● running` / `◐ partial` / `○ stopped`) — one line.
2. Services table.
3. Tools table.
4. Deploy table.
5. Topology tree.
6. Git workspace table.

Health line appears **only** in the default command; subcommands skip it.

### `status` flag matrix

| Flag | Effect |
|---|---|
| `--no-services` | suppress services section |
| `--no-tools` | suppress tools section |
| `--no-deploy` | suppress deploy section |
| `--no-topology` | suppress topology section |
| `--no-git` | suppress git workspace section |

Flags are stackable. Health indicator always shows in the default view.

### Git porcelain v2 parsing

Single shellout per service: `git -C <dir> status -b --porcelain=v2`.

Header lines we consume:

- `# branch.head <name>` — branch name (or `(detached)`).
- `# branch.oid <oid>` — full commit SHA, take first 8 chars.
- `# branch.ab +N -N` — ahead/behind counts.

Any non-`#` line means the working tree is dirty.

### Out of scope

- `--json` / `--format yaml` for `status`.
- `cmdbrowser` integration for new subcommands.
- Changes to journal format, hashing, deploy pipeline.
- Reusing `services.<svc>.type` for service-vs-tool distinction (stays as render-category marker).
- Deprecation aliases — none.

### Risks & Mitigations (from Go-skill review)

| Risk | Mitigation |
|---|---|
| **`PersistentPreRunE` parent-replacement trap.** A child `PersistentPreRunE` on the `status` group would silently drop the root's project-resolution hook (cobra does not chain hooks). | Task 4: use a memoized `loadStatusContext` helper called from each subcommand's `RunE`. No `PersistentPreRunE` on the status group. |
| **Raw-map regression for `${tools.*}` dot-paths.** Removing tool definitions from the merged raw map without an `injectToolsIntoRaw` equivalent breaks `exports.env`, `default_from:`, `docker.yml` `${...}` expressions, and `info.yml` references. | Task 1: add `injectToolsIntoRaw` mirroring the existing `injectServicesIntoRaw` at `internal/config/devbox.go:1023`. Acceptance regression run in Task 7. |
| **Stale tool definitions in any of the 3 layers.** Tool definitions live in `devbox/defaults.yml` today (per `docs/reference/config/devbox.md:60`), not main `devbox.yml`. A post-merge validator on the merged map would lose the source-file location. | Task 1: validator runs **per-layer pre-merge** so errors point at the right file; rejects any field other than `enabled:` under `tools.<name>` in `devbox.yml`, `devbox/defaults.yml`, and `devbox/local.yml`. |
| **`tpl.RenderCommand` would break the hermetic contract** by exposing `resolve` / `resolveMap` / `resolveFile` (FS access). | Task 2: use plain `tpl.Render` only. Test with `{{ env "X" }}` to confirm it stays blocked. |
| **Non-deterministic column order.** Go map iteration is randomised; "first appearance" of a custom column would shuffle per run. | Task 2: iterate items alphabetically (matches existing `buildServiceRows` sort). Test stability across repeated runs. |
| **errgroup-cancels-siblings on first error** in the git collector would mean one failing service kills all rows. | Task 3: goroutines always return `nil`; per-row errors land on `row.Err`. One aggregate warning at end. |
| **Goroutine leaks** under `errgroup.SetLimit(8)` if a shellout hangs. | Task 3: `exec.CommandContext(ctx, ...)` propagates cancellation. `goleak.VerifyNone` in collector tests; `goleak.VerifyTestMain` for the package in Task 7. |
| **Direct `os.Stdout` / `os.Stderr` writes** would make tests unable to capture output. | All tasks: route through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`. Audit covered by Task 4 (status), enforced in Task 2/3/5 reviews. |
| **Cobra flag-state accumulation** across `Execute()` calls on a reused root command produces false-positive test passes. | All command tests: build a fresh `newRootCmd()` per test. Reinforced in Task 4 / Task 5 test bullets. |
| **`t.Parallel()` on tests overriding package-level seams** races on the shared var. CLAUDE.md already documents this for `runParamForm` / `confirmRun` / `runUserCommand`. | Task 5: same constraint for new tests overriding `ui.IsInteractiveFn` / `runMultiSelect`. |
| **View-model boundary leak**: putting `GitWorkspaceRow` in `internal/stack/` would force `internal/ui/` to import stack-level types, blurring the "ui never loads YAML" invariant in CLAUDE.md. | Task 3: view-model lives in `internal/command/statusview/`; stack collects, ui renders. |
| **`status` / `--no-*` enum drift**: future section added but `--no-*` flag forgotten. | Task 4: typed `Section` enum + ordered list as single source of truth. |
| **Template casing mismatch**: `cfg.Raw` keys are lowercase YAML; templates using PascalCase like `.Project` / `.Runtime` / `.Tools` silently render empty. | Task 2: explicit data contract — synthetic typed keys are PascalCase (`.ServiceCfg`, `.Tool`, `.Globals`, `.Raw`); raw drill-down uses lowercase via `.Raw.project.*` / `.Raw.tools.*`. No ad-hoc aliases. |
| **`LoadConfig` load-sequence ambiguity**: the per-layer tool-overlay validator needs each layer's raw map *and* the declared tool set *before* the merge happens, but `LoadConfig` today merges inline. | Task 1: explicit 7-step sequence — read raw layers, load `tools.yml`, validate per-layer, merge, resolve `Enabled`, inject into raw, then the existing pipeline. |
| **`git -C` walks to parent repo**: a service dir inside the project's own git repo would report the project's status, not "not a repo" — misleading per-service rows. | Task 3: pre-check `<dir>/.git` before shelling out. No `.git` ⇒ all cells `—`, no `git` invocation. Parent-repo discovery is intentionally out of scope. |
| **Forward reference to commands that don't exist yet**: Task 2 originally said it wires into `status services` / `status tools` subcommands, but those are created in Task 4. | Task 2 reworded to cover helpers + the existing `RunStatus` / `RenderServices` / `RenderTools` paths only. Task 4 reuses the same helpers when wiring the new subcommands. |
| **Zero-value `ToolConfig` entries from overlay-only `tools.<name>.enabled` blocks**: once `Enabled` is `yaml:"-"`, the top-level `yaml.Unmarshal` of the merged map still produces map entries with empty `Container` / `Host` / `Port`, which `validateConfigKeys` rejects. | Task 1: explicit `cfg.Tools = tools` assignment from `LoadToolsConfig` result (step 6 of the `LoadConfig` refactor) — `tools.yml` is the only source of truth for typed tool definitions post-refactor. |
| **Skipping the existing `yaml.Marshal(merged)` / `yaml.Unmarshal(..., &cfg)` step** in the `LoadConfig` rewrite would null out every other typed field (`Project`, `Runtime`, `Binaries`, …). | Task 1 step 5: explicitly retain the existing merge-to-typed step at `devbox.go:786–793`; only `cfg.Tools` is overridden afterwards. |
| **stderr discipline leaking into `stack` / `ui`**: passing `cmd.ErrOrStderr()` into render functions would force layering decisions on packages that are meant to be output-format-agnostic. | Pinned signatures: `ui.Render*(...) string` (rendering only, no error return); `stack.RenderServices` / `RenderTools` return `(string, []error)` (after Task 4); `stack.CollectGitWorkspace` returns rows with per-row `Err`; `internal/command` is the single writer of stderr. Documented in Tasks 2, 3, and 4. |
| **Task 2 changing `stack.RenderServices` / `RenderTools` signatures while `RunStatus` still calls them** would break the build mid-task (RunStatus has no warning-output contract). | Task 2 scope reduced to helpers + `ui` row extensions (Extras + extraCols) only. Existing `RenderServices` / `RenderTools` signatures unchanged in Task 2. Task 4 — which deletes `RunStatus` and introduces section-level orchestration — owns the signature change to `(string, []error)`. Custom columns are not visible in `devbox status` output until Task 4 ships. |
| **Forging parallel `statusview.ServiceRow` / `ToolRow` types** when `ui.ServiceTableRow` / `ui.ToolTableRow` already exist would create two view models. | Task 2: extend existing `ui.ServiceTableRow` (`internal/ui/table.go:36`) and `ui.ToolTableRow` (`table.go:126`) with `Extras map[string]string`. Only `statusview.GitWorkspaceRow` is a new type (no existing equivalent). |
| **Stale callers of `ui.RenderServiceTable` / `ui.RenderToolTable`** outside `stack/status.go` (`internal/command/service.go:212`, `internal/command/tools.go:180`) would break the build when Task 2 adds the `extraCols` parameter. | Task 2 enumerates **all** callers and updates them to pass `nil` for `extraCols`. Two of those call sites disappear in Task 5 (removed subcommands), but Task 2 ships first and must compile. |
| **Writer-based section renderers vs string-composing orchestrator**: today every `stack.Render*` writes inline to `*render.Writer`; the new orchestrator needs strings it can compose, count warnings against, and route to stdout/stderr separately. Calling "unchanged" misses that all five functions need signature migration. | Task 4 explicitly migrates every section renderer (`RenderHealth`, `RenderServices`, `RenderTools`, `RenderDeployStatus`, `RenderTopology`) from writer-based to string-returning, and updates the corresponding `internal/stack/*_test.go` assertions. |

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification**:

- Run `devbox status` in a real devbox project; verify all six sections render in order with sane data.
- Toggle a service via bare `devbox services`; confirm mandatory services appear pre-checked and disabled.
- Run `devbox services` over SSH without a TTY (e.g. piping input) and confirm the error message.
- Run `devbox status deploy <svc>` for a deployed service; verify per-phase/step breakdown.
- Run `devbox status --no-git --no-topology`; confirm only health + services + tools + deploy print.
- Declare a `status:` block on a service with a deliberately broken template; confirm the cell shows `—` and exactly one warning prints to stderr.
- Verify shell completion: `devbox status deploy <TAB>` lists tracked services.

**External system updates**:

- Update any consumer scripts that invoke `devbox status <svc>` (now `devbox status deploy <svc>`) or the removed `services status` / `services list` / `tools status` / `tools list`.
- Update onboarding docs and team READMEs that screenshot the old CLI surface.

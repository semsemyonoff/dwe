# Service Lifecycle Improvements + Completion Install

## Overview

Seven related directions, sequenced so each builds on stable foundations:

1. **Completion install** — `devbox completion install/uninstall [shell]` replaces the manual `devbox completion <shell> > /path/...` ritual.
2. **Per-service folder restructure** — `devbox/services/<name>/{service,deploy,reset}.yml`. Foundation for everything that follows.
3. **Deploy for all service types** — drop the app-only gate; full `devbox deploy run` enumerates every **enabled** service that has a `deploy.yml` (today's enabled-filter preserved; only the type filter is dropped). Explicit `--service <name>` accepts any service with a `deploy.yml` regardless of enabled state. **Sub-feature**: new optional top-level `after: [<service>...]` field in `devbox/services/<name>/deploy.yml` declares deploy-time ordering between services (separate from runtime `depends_on:`). Full deploy topo-sorts by `after:` (end-to-end per service, no phase interleaving); `--service <name>` does NOT cascade but warns if declared `after:` deps aren't deployed in the journal. Cycles + missing references → load-time error; reference to a service without `deploy.yml` or to a disabled service → warning.
4. **Lifecycle hooks** — `on_enable` / `on_disable` in `service.yml` with `requires` (`none|restart|deploy`) + `before`/`after` user-command refs; `services enable/disable` prints a plan and optionally executes it.
5. **Per-service stop** — `devbox stop [service]` accepts a single service; uses direct `docker stop <container>` (bypassing compose) so it works even after the service has been disabled and dropped from the rendered compose project. Prerequisite for §6.
6. **`reset run --service <name>`** — per-service reset; per-service `reset.yml`; on_disable.before hook integration; uses §5 to stop the container.
7. **Pending state in journal** — track outstanding restart/deploy after toggle-without-apply; banner in `devbox status`.

**Context**: pre-release, no back-compat (see CLAUDE.md "Project Status & Compatibility Policy"). Internal call-site cascades from renames are fine — fix in one PR.

**Non-goals**: moving user-commands inside `devbox/services/<name>/`; `devbox restart --only <name>`; ConfigHash-based heuristic for upgrade-pending until `deploy`.

## Context (from discovery)

- **Services loader (today)**: `internal/config/devbox.go:1146` `LoadServicesConfig` reads a single `devbox/services.yml`; `ServiceConfig` struct at `internal/config/devbox.go:586`; map lives on `DevboxConfig.Services` (`devbox.go:104`).
- **Deploy loader (today)**: `LoadServiceDeployConfigs` at `internal/config/devbox.go:1838` reads `devbox/deploy/<name>.yml`. App-only gate: `ValidateServiceDeployFiles` at `devbox.go:1801`; sentinels `ErrDeployFileForNonApp` / `ErrDeployTargetNotApp` at lines 504-505.
- **Service plan resolver**: `internal/deploy/service_plan.go:15` `ResolveServicePlan` (checks `svc.IsApp()` line 31); `ResolveServicesPlan` line 58 (filters `svc.IsApp()` line 69).
- **Reset**: `LoadResetConfig` at `internal/config/devbox.go:1664`; command at `internal/command/reset.go:21` with subcommands `plan`, `run`, `step`. No per-service reset path.
- **Completion**: Cobra built-in registered in `internal/command/root.go:174`. Helper `completionConfigPath()` at `internal/command/completion.go:25` (already used to make `__complete` safe).
- **Service toggle**: `internal/command/service_toggle.go` exists as a stub with `runMultiSelect = ui.RunMultiSelect` injectable wrapper. Local.yml writing + `.env` regen is **not yet implemented** — must be built as part of Task 13.
- **Journal state**: `internal/deploy/journal/state.go:60` `ProjectState{SchemaVersion, Project, Services}`. Atomic `Save()` at line 115 (write-temp + rename, 0o755/0o644).
- **Status view**: `internal/command/status.go:155` `newStatusCmd()` registers subcommands `apps`, `tools`, `infra`, `daemons`, `deploy`, `topology`, `git` (lines 188-194).
- **User-command types**: `internal/usercommands/model/types.go:21` `CommandType` enum includes `CommandTypeShell` (line 25) and `CommandTypeScript` (line 27).
- **Config validators**: `internal/validate/config/all.go:8` `All()` registers 17 validators (lines 10-27); not yet split per-file.

## Development Approach

- **Testing approach**: Regular (code first, then tests, within the same task).
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** — listed as separate checklist items, not bundled with implementation.
- **CRITICAL: all tests must pass before starting the next task**.
- **CRITICAL: update this plan when scope changes during implementation** (add ➕ for new tasks, ⚠️ for blockers).
- One canonical form per concept — no `schema_version` bumps, no dual loaders, no migration shims (CLAUDE.md).

## Testing Strategy

- **Unit tests**: required for every task (loaders, validators, planners, journal mutations, command-level argument parsing).
- **Table-driven tests** preferred for parsing/validation/plan-builder/journal-mutation surfaces (matches repo convention; see CLAUDE.md "Testing Guidelines").
- **CLI integration**: command tests via cobra `SetArgs` / `SetOut` / `SetErr` / `SetIn`. Build a **fresh command tree per test** — cobra accumulates flag state across `Execute()` calls on the same instance.
- Stdin-driven prompts (Task 13) tested by injecting `cmd.SetIn(strings.NewReader("y\n"))` plus a seamed `isTerminal` predicate.
- Fixtures under each package's `testdata/`.
- No e2e UI tests in this repo.

## Engineering Conventions (apply to every task)

These cut across tasks; verify each task PR conforms before mark-complete.

- **I/O discipline**: every cobra command writes via `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`, reads via `cmd.InOrStdin()`. No direct `os.Stdout` / `os.Stderr` / `fmt.Println` in command code. Plan/banner renderers accept `io.Writer` parameters (CLAUDE.md "section renderer signature contract").
- **Error wrapping**: loaders, validators and runtime helpers wrap underlying errors with `fmt.Errorf("doing X: %w", err)` (lowercase message, no trailing punctuation). User-facing top-level error printed by cobra root.
- **Sentinel errors**: declared `Err*` package-level vars (e.g. `ErrServiceNoDeployFile`) when callers need to branch on identity via `errors.Is`. Plain `fmt.Errorf` when the error is purely informational.
- **Single handling rule**: every error is either returned OR logged once, never both. Validators emit diagnostics (not logs) so they don't double-report.
- **Zero-value safety**: enums declare an explicit unspecified sentinel at the zero value; struct types are designed so `var x T` is usable (or document required constructor).
- **Defensive copies**: exported accessors that return slices/maps backed by internal state return a `slices.Clone` / `maps.Clone` copy (e.g. `PendingApply.ServiceNames()`).
- **Cobra args**: positional argument count enforced by `Args:` validators (`cobra.MaximumNArgs`, `cobra.ExactArgs`), never `len(args)` checks inside `RunE`.
- **Completion-safe code paths**: any new `ValidArgsFunction` that needs **project config** (service names, command IDs, anything derived from `LoadDevboxConfig`) must guard against `__complete` by calling `completionConfigPath(flags, cmd)` per CLAUDE.md "Key Patterns" and returning `cobra.ShellCompDirectiveNoFileComp` on any error. Completions that are static (shell names for `completion install`, fixed enum values) do NOT need project resolution and should NOT call `completionConfigPath` — completion subcommands are usable outside a project.
- **Project locks**: all mutating operations go through `lock.AcquireProjectLocks(baseDir)` (CLAUDE.md "Project-lock seam"). Never call `lock.Acquire` directly.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Keep plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable in this codebase.
- **Post-Completion** (no checkboxes): items requiring external action — live-project migration verification, manual smoke testing.

## Implementation Steps

### Task 1: `devbox completion install [shell]`

- [x] add `installCompletionCmd` to `internal/command/completion.go` with `Args: cobra.MaximumNArgs(1)` and positional optional `shell` arg (`bash|zsh|fish|powershell`); resolve from `$SHELL` basename when omitted; sentinel `ErrUnsupportedShell` for unresolved/unsupported shells, error message lists the supported set
- [x] add `ValidArgsFunction` returning the four shell names + `cobra.ShellCompDirectiveNoFileComp`
- [x] add `--dry-run` and `--path <dir>` local flags on the install command
- [x] implement per-platform target path resolver:
  - zsh → `~/.zsh/completions/_devbox`
  - bash → `~/.local/share/bash-completion/completions/devbox`
  - fish → `~/.config/fish/completions/devbox.fish`
  - powershell → `<dir(profile path)>/devbox-completion.ps1`. PowerShell on macOS/Linux is the cross-platform `pwsh` (PowerShell 7+). **`$PROFILE` is a PowerShell automatic variable, not an environment variable** — a Go process cannot read it via `os.Getenv`. Resolution order:
    1. if `--path <dir>` is provided → use it directly (skip pwsh entirely)
    2. else invoke `pwsh -NoProfile -Command "$PROFILE.CurrentUserAllHosts"` with a short timeout (~3s) and trim stdout as the profile file path; use its parent dir
    3. else fall back to the documented `pwsh` default on macOS/Linux: `~/.config/powershell/profile.ps1` — use its parent `~/.config/powershell/`. **`os.MkdirAll` will create the parent if missing** (twenty-second review — consistent with the general "create parent dir at 0o755" rule above; no separate "parent doesn't exist" branch). If `pwsh` is not installed but the user nevertheless wants the completion file written (e.g. plans to install pwsh next), this fallback Just Works
    4. **error only when `HOME` itself is unset / unreadable** → `cannot resolve home directory; pass --path <dir> explicitly` and exit 2. Removing `pwsh` from PATH is no longer a failure mode (case (c) below)
  - target file is `<dir>/devbox-completion.ps1`; print a snippet for the user to add to their profile: `. "<absolute path to devbox-completion.ps1>"`. Like the zsh fpath hint, do NOT auto-edit the profile
  - tests: seam the `pwsh` invocation (package-level `resolvePowerShellProfile = func() (string, error) {...}` so tests can override). Cover: (a) `--path` overrides everything, (b) `pwsh` returns a valid path, (c) `pwsh` missing → fallback to `~/.config/powershell/profile.ps1`, parent created via `os.MkdirAll`, install succeeds, (d) `HOME` unresolvable → exit-2 error
  - create parent dir at 0o755 via `os.MkdirAll` if missing
- [x] generate completion content via the same cobra `Gen*Completion` calls used by `devbox completion <shell>`, into a `bytes.Buffer`; then atomic write: `os.CreateTemp(parentDir, ".devbox-completion-*")` (same dir → POSIX atomic rename), `Write`, `Chmod(0o644)`, `Close`, then `os.Rename(tmp, target)` (POSIX `rename(2)` atomically replaces an existing destination — devbox targets only macOS and Linux). On any failure call `os.Remove(tmp)` and wrap with `%w`
- [x] idempotency: install twice → same content, no error, single file (POSIX atomic-replace handles this directly; no platform branching needed)
- [x] when target shell is zsh and `~/.zshrc` lacks a line referencing the install dir in `fpath`, print the exact snippet to add to `cmd.ErrOrStderr()` (do NOT edit rc files automatically). Use a seamed `homeDir` / `readFile` so this is testable
- [x] all user-facing output via `cmd.OutOrStdout()` (success message, dry-run preview) and `cmd.ErrOrStderr()` (snippet hint, warnings)
- [x] `--dry-run`: print the resolved target path + first ~10 lines of the generated content to `cmd.OutOrStdout()`; no filesystem write
- [x] register the new subcommand under the existing completion group
- [x] write tests (table-driven where natural, fresh root command per test):
  - shell auto-detection from `$SHELL` env (use `t.Setenv`)
  - explicit positional arg overrides `$SHELL`
  - unsupported shell → `errors.Is(err, ErrUnsupportedShell)`
  - `--dry-run` writes no file (verify target absent after run)
  - `--path` override resolved per-shell
  - idempotency: install twice (pre-create target with stale content) → second call replaces cleanly; target ends with current content; tmp gone
  - zsh fpath hint emitted to stderr when `~/.zshrc` lacks the dir
  - zsh fpath hint suppressed when `~/.zshrc` already contains the dir
  - powershell snippet printed to stderr with the absolute completion-script path; `$PROFILE` itself is NOT modified
- [x] run `go test ./internal/command/...` - must pass before next task

### Task 2: `devbox completion uninstall [shell]`

- [x] add `uninstallCompletionCmd` mirroring install: `Args: cobra.MaximumNArgs(1)`, same `ValidArgsFunction`, same `--path` override, no `--dry-run` (single file delete)
- [x] delete the file the matching `install` would have written; `os.Remove` with `errors.Is(err, fs.ErrNotExist)` → info message to `cmd.OutOrStdout()` + exit 0; any other error wrapped and returned
- [x] do NOT touch rc files (document in command long help)
- [x] write tests (table-driven, fresh command per test):
  - deletes installed file (verify absent after)
  - missing file is idempotent (no error, exit 0)
  - `--path` override
  - unsupported shell → `errors.Is(err, ErrUnsupportedShell)`
- [x] update `docs/reference/cli/index.md` how-to snippet referencing `install`/`uninstall`; note that Homebrew taps should use `generate_completions_from_executable` instead
- [x] run `go test ./internal/command/...` - must pass before next task

### Task 3: Per-folder service loader (`LoadServiceFolder` / `LoadServices`)

**Semantics inventory** — today's `LoadServicesConfig` (`internal/config/devbox.go:1146-1339`) does much more than YAML decode. Every responsibility below MUST be preserved across the folder boundary, or bad service shapes silently slip through and `extends:` inheritance breaks. Use this as an acceptance checklist:

1. Raw-shape pre-validation (decode to `map[string]any` to inspect keys before typed decode)
2. Required `type` field per service; validated via `ServiceType.Validate()`
3. `extends:` only allowed for app type — sentinel error otherwise (pre-allowlist check at line 1180-1181)
4. Per-type field allowlist via `allowedFieldsFor(svcType)` (line 1185-1193)
5. `ports:` / `hosts:` shape validation — must be maps; port values are integers in `[1, 65535]` (line 1195-1216)
6. Strict typed decode `KnownFields(true)` AFTER shape pre-validation
7. Topological sort of `extends:` chains (parent-first, line 1236-1239)
8. Post-toposort `extends:` symmetry — child.Type MUST equal parent.Type (line 1246-1258)
9. Defensive field inheritance + merging: shallow-clone slices/maps; dedup env/dirs across parent/child; pointer copies for `Render.IDE/AI/Git` flags (line 1260-1336)

- [x] in `internal/config/devbox.go` add `LoadServiceFolder(baseDir, name) (*ServiceConfig, error)` that performs responsibilities 1-6 above against `devbox/services/<name>/service.yml`. Missing `service.yml` in an existing dir → load error wrapped: `fmt.Errorf("loading service %q definition: %w", name, err)`. The `extends:` resolution (7-9) is deferred to `LoadServices` because it's cross-service
- [x] file lifecycle: `LoadServiceFolder` opens + `defer file.Close()` per call. The `defer` is per-function (not per-loop iteration) because `LoadServices` invokes it as a function call per service — keeps file handles bounded (golang-safety "defer in loops" pattern)
- [x] add `LoadServices(baseDir) (map[string]ServiceConfig, error)` that:
  - enumerates `devbox/services/*/` (dir entries only)
  - **missing `devbox/services/` directory entirely → return an empty map and nil error** (preserves today's `LoadConfig` tolerance at `internal/config/devbox.go:903-910`, which treats absent services as an empty map rather than a hard failure). A project without services is still valid
  - calls `LoadServiceFolder` for each (responsibilities 1-6)
  - applies cross-service responsibilities 7-9 (toposort + symmetry + inheritance merge) over the assembled map
  - on per-folder decode/validation failure, collects into `errors.Join` so the caller sees every broken folder, not just the first
- [x] folder-name = map-key invariant: `LoadServices` uses the folder name as the key; no `name:` field inside `service.yml`
- [x] **fixture migration is part of THIS task**. Replace `LoadServicesConfig` call sites with `LoadServices`, delete `LoadServicesConfig`, AND convert every `testdata/` fixture under `internal/config/`, `internal/command/`, `internal/deploy/`, `internal/stack/`, `internal/validate/` to the new `services/<name>/service.yml` layout. (Otherwise the "tests must pass before next task" gate is impossible to satisfy.) Live monorepo project migration is out of scope for this plan — this PR touches only the CLI repository
- [x] write tests: happy path (3 services), missing `service.yml` error, unknown YAML field rejected, empty `services/` directory returns empty map, **absent `devbox/services/` directory returns empty map + nil error** (tolerance regression), non-dir entries ignored, `extends:` toposort with a 3-level chain, `extends:` cross-type rejected, `errors.Join` surfaces multiple broken folders
- [x] run `go test ./internal/config/...` - must pass before next task

### Task 4: Per-folder deploy loader path migration + `after:` ordering field

- [x] update `LoadServiceDeployConfigs` (`internal/config/devbox.go:1838`) to read `devbox/services/<name>/deploy.yml` instead of `devbox/deploy/<name>.yml`; presence-based gating logic preserved
- [x] enumerate sources from `LoadServices` result (not from a `devbox/deploy/` directory scan)
- [x] **new top-level `after:` field on `DeployConfig`** for deploy-time ordering between services (distinct from runtime `depends_on:`):
  ```go
  type DeployConfig struct {
      After  []string      `yaml:"after,omitempty"`  // service names this service deploys after; default: []
      Phases []DeployPhase `yaml:"phases"`
      // ... existing fields ...
  }
  ```
  Semantics: omitted/empty = no deploy ordering constraint. Default is NOT auto-derived from `depends_on:` — they're different concepts (runtime container ordering vs deploy step ordering). If users want deploy ordering, they declare `after:` explicitly. Live monorepo projects will add `after:` declarations in their own follow-up PRs — out of scope for this CLI-only plan
- [x] **scope-limit at the LOAD path with context-specific loaders** (seventeenth review fix — the previous "make `LoadDeployConfig` reject `After`" approach broke service deploys because today `LoadServiceDeployConfigs` calls `LoadDeployConfig` internally at `internal/config/devbox.go:1844`, and `deploy.FindStep` calls it too at `internal/deploy/plan.go:90`. Same loader, two contexts, opposite rules — needs a split):
  - new sentinel `ErrAfterFieldNotAllowed` in `internal/config/`
  - **internal shared decode** `loadDeployConfigDecode(path string) (*DeployConfig, error)` — does the strict YAML decode + the existing shape validation that today's `LoadDeployConfig` does. Permits `After`; no context check. NOT exported
  - **`ParseDeployConfigForValidation(path string) (*DeployConfig, error)`** (exported, eighteenth review) — thin wrapper around `loadDeployConfigDecode` for the validator's use. Returns the parsed config regardless of which file scope it came from, so the Task 6 scope-limit validator can inspect `cfg.After != nil` and emit per-file diagnostics with proper file/key location info. The validator MUST use this function instead of the strict loaders (`LoadProjectDeployConfig`, `LoadResetConfig`, `LoadServiceResetConfig`) — those reject `after:` BEFORE returning a config, leaving the validator nothing to inspect. Naming with the `ForValidation` suffix makes the bypass intent obvious at the call site so reviewers can spot misuse
  - **`LoadProjectDeployConfig(path string) (*DeployConfig, error)`** — replaces today's `LoadDeployConfig` for the project-wide `devbox/deploy.yml` case. Wraps `loadDeployConfigDecode`; if result has `cfg.After != nil` → wrapped `ErrAfterFieldNotAllowed`. Called by the project-deploy code path
  - **`LoadServiceDeployConfig(path string) (*DeployConfig, error)`** — the per-service variant. Wraps `loadDeployConfigDecode`; PERMITS `After`. Called by:
    - `LoadServiceDeployConfigs` (the bulk loader from Task 4)
    - `deploy.FindStep` (the `devbox deploy step` path, updated below in this task)
  - **`LoadResetConfig(path string) (*DeployConfig, error)`** (project-wide reset, today at `internal/config/devbox.go:1664`): wraps `loadDeployConfigDecode`; if `cfg.After != nil` → wrapped `ErrAfterFieldNotAllowed`
  - **`LoadServiceResetConfig(...)`** (per-service reset, added by Task 5): same as `LoadResetConfig` — reset has no ordering peers in either scope, so `After` is rejected. Cross-referenced in Task 5
  - **callers updated in this task**: every existing call site of `LoadDeployConfig` is moved to either `LoadProjectDeployConfig` (project-wide deploy command path) or `LoadServiceDeployConfig` (service deploy path including `LoadServiceDeployConfigs` and `FindStep`). Grep for `LoadDeployConfig` to find them all; the old name is deleted so no caller can accidentally bypass the context split
  - The Task 6 validator rules still exist (nicer diagnostics during `devbox validate` with file/key locations; doesn't bail on first offender), but the loader split is now the runtime gate so invalid configs never reach `runDeployHelper` / reset paths via a non-validate command
  - Tests for the loader split:
    - `LoadProjectDeployConfig` with `after:` → `errors.Is(err, ErrAfterFieldNotAllowed)`; without `after:` → success
    - **`LoadServiceDeployConfig` with `after:` → success** (the regression case the seventeenth review identified — service deploys with `after:` must load cleanly)
    - `LoadResetConfig` / `LoadServiceResetConfig` with `after:` → `errors.Is(err, ErrAfterFieldNotAllowed)`
- [x] **new helper `deploy.TopoSortByAfter(deploys map[string]*config.DeployConfig, services map[string]config.ServiceConfig) ([]string, error)`** in `internal/deploy/` (distinct from existing `config.TopoSortServices` which sorts by `depends_on:`). The second parameter is the full services map (NOT just the deploy-having subset) so the helper can distinguish "unknown service" from "service exists but has no deploy.yml":
  - returns service names in deploy-order (dependencies-first)
  - alphabetical tie-break for nodes at the same topo level (deterministic)
  - **runtime graph validation** (sixteenth review — validator-only enforcement is insufficient because lifecycle commands don't run `devbox validate`):
    - **self-reference**: if any `deploys[name].After` contains `name` itself → return sentinel `ErrDeploySelfReference` wrapped with the service name
    - **unknown service**: if any `deploys[name].After` contains a name `x` where `x` is not in `services` AT ALL (not just missing deploy.yml — the service itself doesn't exist) → return sentinel `ErrDeployUnknownAfterRef` wrapped with both names
    - **missing-deploy ancestor**: if any `deploys[name].After` contains a name `x` where `x` is in `services` but `deploys[x] == nil` (service exists, no deploy.yml) → silently drop the edge from the sort graph (matches the validator's Warning semantics; ordering constraint to a non-deployable service is a no-op, not an error)
    - **cycle**: returns sentinel `ErrDeployCycle` on cycle; error message includes the cycle path (`"deploy ordering cycle: main → postgres → redis → main"`)
  - These three sentinels (`ErrDeploySelfReference`, `ErrDeployUnknownAfterRef`, `ErrDeployCycle`) ensure `runDeployHelper` / `ResolveServicesPlan` / `ResolveServicesPlanSubset` cannot silently sort a malformed graph. The Task 6 validator still runs first when `devbox validate` is invoked; this runtime check is the second gate
  - tests asserting each sentinel fires when called directly on a malformed deploy-map (no validator in the test path)
- [x] **`deploy.FindStep` direct file load** (fourteenth review): the `devbox deploy step <service>/<phase>/<step>` command path goes through `deploy.FindStep` (`internal/deploy/plan.go:85-105`) which currently calls `config.LoadDeployConfig(filepath.Join(baseDir, "devbox", "deploy", serviceName+".yml"))` — a second site that hardcodes the old path. Two changes here:
  1. update the `filepath.Join` to `filepath.Join(baseDir, "devbox", "services", serviceName, "deploy.yml")`
  2. switch the call from `LoadDeployConfig` to **`LoadServiceDeployConfig`** (per the loader split above) — otherwise `devbox deploy step` would reject service deploys that have `after:` declared
  Grep for any other `filepath.Join(... "devbox", "deploy" ...)` occurrences and fix them in this task too. Without this fix, `devbox deploy step` breaks after the folder migration AND rejects valid service deploys
- [x] write tests:
  - `LoadServiceDeployConfigs`: deploy file present → loaded, missing → nil (no error), strict decoder rejects unknown fields, only services with `deploy.yml` end up in the result map
  - **`deploy.FindStep` resolves a step from `devbox/services/<name>/deploy.yml`** (regression for the `devbox deploy step` path — assert against a fixture under the new layout)
  - **`DeployConfig.After` decode**: present → parsed as slice; absent → nil/empty; unknown nested fields rejected by strict decoder
  - **`deploy.TopoSortByAfter` correctness**: linear chain `c after [b], b after [a]` → `[a, b, c]`; diamond `c after [a, b]`, `d after [c]` → `[a, b, c, d]` with `a` < `b` by alphabetical tie-break; no `after:` declared anywhere → alphabetical service list
  - **`deploy.TopoSortByAfter` runtime graph validation** (sixteenth review, called directly without `devbox validate`):
    - cycle → `errors.Is(err, ErrDeployCycle)` with the cycle path in the message
    - `deploys["a"].After = ["a"]` → `errors.Is(err, ErrDeploySelfReference)`
    - `deploys["a"].After = ["nonexistent"]` (not in `services` map) → `errors.Is(err, ErrDeployUnknownAfterRef)`
    - `deploys["a"].After = ["b"]` where `b` is in `services` but `deploys["b"] == nil` → no error, edge silently dropped (helper returns `[a]` only — Warning semantics, matches validator)
- [x] run `go test ./internal/config/... ./internal/deploy/...` - must pass before next task

### Task 5: Per-service reset loader (`LoadServiceResetConfig`)

Note: `LoadResetConfig` returns `*DeployConfig` (not `*ResetConfig`) — reset pipelines share the deploy config shape via the shared `loadPipelineConfig` helper. Match that.

- [x] add `LoadServiceResetConfig(baseDir, name) (*DeployConfig, error)` reading `devbox/services/<name>/reset.yml`; missing file → `(nil, nil)`; strict-decode same as project-level reset (call into the existing `loadPipelineConfig` to reuse all the strict/lenient policy)
- [x] add `LoadServiceResetConfigs(baseDir) (map[string]*DeployConfig, error)` enumerating folders the same way as `LoadServices`; expose on `DevboxConfig` if needed for validation
- [x] **`after:` rejection** (sixteenth review): after the existing decode, if `cfg.After != nil` → return wrapped `ErrAfterFieldNotAllowed` (defined in Task 4). Reset has no ordering peers; the field is invalid here. Matches the same load-path rejection added to `LoadDeployConfig` / `LoadResetConfig` in Task 4
- [x] write tests: present file → parsed, missing file → nil, unknown fields rejected, **`after:` field → `errors.Is(err, ErrAfterFieldNotAllowed)`**
- [x] run `go test ./internal/config/...` - must pass before next task

### Task 6: Per-folder validators + deploy `after:` validation

- [x] in `internal/validate/config/`, add a `services_folder` validator that scans `devbox/services/*/`: missing `service.yml` → Error; files other than `service.yml`/`deploy.yml`/`reset.yml` → Warning ("unknown file in service folder")
- [x] update existing service-shape validators to consume the per-folder loader result
- [x] **add a `deploy_after` validator** in `internal/validate/config/` covering the new `after:` field (Task 4). It needs the full set of loaded `DeployConfig` + `ServiceConfig` maps to do cross-file checks:
  | Rule | Severity | Message |
  |---|---|---|
  | `after:` contains the service's own name (self-reference) | Error | `service %q after: references itself` |
  | `after:` references a service name that does not exist in `devbox/services/*/` at all | Error | `service %q after: references unknown service %q` |
  | `after:` references a service that exists but has no `deploy.yml` | Warning | `service %q after: references %q which has no deploy.yml; ordering constraint will be ignored` |
  | `after:` references a service that exists, has `deploy.yml`, but is disabled in `local.yml` | Warning | `service %q after: references %q which is disabled; the constraint will not trigger because %q will not deploy` |
  | Cross-file: cycle in the `after:` graph across all deploy configs | Error (single diagnostic) | `deploy ordering cycle: a → b → c → a` (uses the cycle path returned by `deploy.TopoSortByAfter`) |
  | `after:` present in `devbox/deploy.yml` (project-wide deploy) | Error | `"after" is only valid in devbox/services/<name>/deploy.yml; remove from project-wide deploy.yml` |
  | `after:` present in `devbox/reset.yml` (project-wide reset) | Error | `"after" is only valid in devbox/services/<name>/deploy.yml; remove from reset.yml` |
  | `after:` present in `devbox/services/<name>/reset.yml` (per-service reset) | Error | `"after" is only valid in devbox/services/<name>/deploy.yml; remove from %s/reset.yml` |
  - cycle detection: call `deploy.TopoSortByAfter(deploys, cfg.Services)` once (over the per-service-deploy map only, NOT project deploy or any reset configs); on `errors.Is(err, deploy.ErrDeployCycle)` emit ONE Error diagnostic with the cycle path embedded; on `errors.Is(err, deploy.ErrDeploySelfReference)` or `ErrDeployUnknownAfterRef`, emit corresponding diagnostics (these duplicate the per-rule checks above but provide a safety net if the rules are bypassed); on success no diagnostic. Don't emit per-service diagnostics for cycle participation — one is sufficient
  - the scope-limit rules above are enforced per-file by calling **`config.ParseDeployConfigForValidation(path)`** (Task 4's parse-only entry point) for each non-service-deploy file — NOT `LoadProjectDeployConfig` / `LoadResetConfig` / `LoadServiceResetConfig`, which reject `after:` at load and would deny the validator the chance to emit per-file diagnostics. Then check `cfg.After != nil && len(cfg.After) > 0` and emit the matching diagnostic. Using a separate parse-only function keeps the loader rejection (runtime gate) and the validator scope check (user-friendly gate) independent — bypassing one doesn't bypass the other
- [x] register the new validator in `internal/validate/config/all.go` `All()`
- [x] write tests:
  - missing `service.yml` error diagnostic, stray file warning, well-formed folder produces no diagnostics
  - `after:` self-reference → Error
  - `after:` to unknown service → Error
  - `after:` to a service without `deploy.yml` → Warning
  - `after:` to a disabled service → Warning
  - cycle across three services → ONE Error with the full cycle path
  - well-formed `after:` graph → no diagnostics
  - **`after:` in `devbox/deploy.yml` → Error** (fifteenth review scope-limit regression)
  - **`after:` in `devbox/reset.yml` → Error**
  - **`after:` in `devbox/services/<name>/reset.yml` → Error**
- [x] run `go test ./internal/validate/...` - must pass before next task

### Task 7: Drop app-only deploy gate

- [ ] remove sentinels `ErrDeployFileForNonApp` / `ErrDeployTargetNotApp` and `ValidateServiceDeployFiles` from `internal/config/devbox.go`
- [ ] remove `svc.IsApp()` checks in `internal/deploy/service_plan.go:31` (`ResolveServicePlan`) and line 69 (`ResolveServicesPlan`); replace with "service has a loaded `deploy.yml`" condition (lookup in the deploy-configs map). **`ResolveServicesPlan` (full deploy) preserves today's `svc.Enabled` filter** at `service_plan.go:67` — the enumeration becomes "every **enabled** service that has a `deploy.yml`", NOT every service with `deploy.yml`. Today's enabled-gate is correct and intentional: a disabled service is excluded from the rendered compose project, so deploying it would orphan its container artifacts. Drop only the type filter (`IsApp`), keep the enabled filter (seventeenth review fix). For `--service <name>` (explicit), enabled state is NOT checked — explicit intent overrides; this matches today's `ResolveServicePlan` behavior
- [ ] in `ResolveServicePlan`, if explicit `--service <name>` references a service without `deploy.yml`, return a new sentinel `ErrServiceNoDeployFile` wrapped with the service name: `fmt.Errorf("%w: %s", ErrServiceNoDeployFile, name)`. Caller tests assert via `errors.Is`
- [ ] grep for any remaining `IsApp` deploy-related call sites and clean up; keep `IsApp` itself if it's used elsewhere (e.g. status grouping)
- [ ] write tests: deploy run over a mixed (app + tool + infra) project deploys every **enabled** service with `deploy.yml` (NOT disabled ones); **disabled service with `deploy.yml` is skipped by full deploy** (seventeenth review enabled-filter regression); `--service <infra>` with deploy.yml works whether enabled or disabled (explicit overrides); `--service <tool>` without deploy.yml returns an error satisfying `errors.Is(err, ErrServiceNoDeployFile)`
- [ ] update `docs/reference/config/deploy.md`: replace "apps" with "services with a deploy.yml"; add an infra example (e.g. minio create-bucket); document the new `after:` field (semantics, full vs `--service` behavior, validator rules, that it's distinct from runtime `depends_on:`)
- [ ] run `go test ./internal/deploy/... ./internal/config/... ./internal/command/...` - must pass before next task

### Task 8: Journal `PendingApply` data model

- [ ] in `internal/deploy/journal/state.go`, add `Pending *PendingApply` field to `ProjectState` with **YAML** tag `yaml:"pending,omitempty"` — the state file is encoded by `yaml.v3` and every existing field in this file uses `yaml:` tags (see `state.go:31-56`). A `json:` tag would be silently ignored by the encoder, breaking `omitempty` and leaving the field unwritten. Pointer nullity (`Pending == nil`) is the "no pending action" sentinel — zero-value safe by construction, no `PendingNone` enum value needed
- [ ] add `PendingApply` as a **list of ops** (tenth review correctness fix — single `{Kind, Services}` collapses mixed batches and silently drops the required restart op when the deploy portion is later applied):
  ```go
  type PendingApply struct {
      Operations []PendingOp `yaml:"operations"`
      CreatedAt  time.Time   `yaml:"created_at,omitempty"`
      ConfigHash string      `yaml:"config_hash"`
  }

  type PendingOp struct {
      Kind     PendingKind `yaml:"kind"`               // PendingRestart or PendingDeploy
      Services []string    `yaml:"services,omitempty"` // populated for deploy; empty/ignored for restart (restart is stack-wide)
  }
  ```
  Invariants: `len(Operations) > 0` whenever `Pending != nil` (collapse to nil when last op removed); at most one op per `Kind` (deduped on insert — see `AddPendingOp`).
- [ ] add `PendingKind` string enum with explicit zero-value sentinel:
  ```go
  const (
      PendingKindUnspecified PendingKind = ""   // zero value; never stored on a non-nil PendingApply
      PendingRestart         PendingKind = "restart"
      PendingDeploy          PendingKind = "deploy"
  )
  ```
- [ ] `(*PendingApply).Find(kind PendingKind) *PendingOp` returns the op for that kind or nil (helper for banner renderer + clear helpers)
- [ ] `(*PendingOp).ServiceNames() []string` returns `slices.Clone(op.Services)` so callers cannot mutate journal state
- [ ] add **package-level helpers** matching the existing `journal.RemoveService(path, name)` pattern (`state.go:174`) — load + mutate + atomic save in one call:
  ```go
  func AddPendingOp(path string, op PendingOp, configHash string) error
  func AddPendingOps(path string, ops []PendingOp, configHash string) error
  func ClearPending(path string) error
  func ClearPendingForKind(path string, kind PendingKind) error
  func ClearPendingForServices(path string, kind PendingKind, services []string) error
  func ClearPendingOps(path string, clears []PendingClear) error
  func ReplaceServiceWithPending(path string, serviceName string, op PendingOp, configHash string) error
  ```
  with `PendingClear`:
  ```go
  type PendingClear struct {
      Kind     PendingKind
      Services []string  // for PendingDeploy: subset to remove; for PendingRestart: ignored (whole op removed)
  }
  ```
  Semantics:
  - `AddPendingOp(path, op, hash)`: rejects `op.Kind == PendingKindUnspecified`. Looks for an existing op of the same kind in `Pending.Operations`:
    - if found: for `PendingDeploy` merge service names (dedup + sort); for `PendingRestart` Services is ignored (no-op merge — restart is stack-wide)
    - if not found: append the new op (preserving insert order). Resulting `Operations` invariant: at most one entry per kind
    - refresh `CreatedAt`/`ConfigHash` on every successful call
  - **`AddPendingOps(path, ops, hash)`** (twenty-first review): atomic batch variant. Loads state ONCE, applies every op via the same merge rules above to an in-memory copy, then saves ONCE via the existing atomic temp+rename. Either every op is persisted or none — there is no half-applied state on disk. Rejects if ANY op has `PendingKindUnspecified`. **Empty/nil `ops` slice → no-op, no save, no error** (twenty-sixth review — matches `ClearPendingOps` empty-slice semantics). This means callers can pass a slice unconditionally, including the all-`RequiresNone` toggle case where the slice ends up empty — no pending record is written. Used by Task 13's mutation flow to write the toggle batch's pending entries in one shot
  - **`ReplaceServiceWithPending(path, serviceName, op, hash)`** (twenty-third review): atomic combination of `RemoveService` + `AddPendingOp` for the per-service reset path. Loads state ONCE, deletes `state.Services[serviceName]`, applies the pending op via the same merge rules as `AddPendingOp`, recomputes aggregates (same logic as today's `RemoveService` at `state.go:174-194`), saves ONCE atomically. Either both the removal and the pending write persist, or neither does — eliminates the per-service reset partial-failure mode where the deployed state is cleared but no banner appears. Rejects `PendingKindUnspecified`
  - **`ClearPendingOps(path, clears)`** (twenty-fourth review): atomic batch clear. Loads state ONCE, applies every `PendingClear` entry to an in-memory copy using the same per-kind/per-service removal rules as `ClearPendingForServices` / `ClearPendingForKind`, then saves ONCE atomically. Either every clear persists or none. Used by Task 12's toggle-executor success path to clear all contributor-owned pending in a single save. Rejects `Kind == PendingKindUnspecified` for any entry. Empty `clears` slice → no-op (no save)
  - `ClearPending(path)`: sets `Pending = nil` unconditionally. **Only callers that actually applied EVERY pending op should use this** — full-project `devbox reset run` is the sole user in this plan. The toggle executor MUST NOT use `ClearPending` (twenty-fifth review fix — the helper guidance previously listed the toggle executor as a potential caller, but Task 12's apply-success path is required to use `ClearPendingOps` for atomic per-batch clear; unconditional `ClearPending` would erase unrelated-session pending entries the toggle did not apply)
  - `ClearPendingForKind(path, kind)`: removes every op of that kind from `Operations`; if `Operations` becomes empty, sets `Pending = nil`. Used by standalone `devbox restart` (clears restart op without touching deploy op — mixed pending survives as `[{deploy,...}]`) and by `devbox deploy run` (no `--service`) for the deploy kind
  - `ClearPendingForServices(path, kind, services)`: locates the op of that kind; for `PendingDeploy` removes the named services from `op.Services`; if `op.Services` becomes empty, removes the op entirely; if `Operations` becomes empty, sets `Pending = nil`. For `PendingRestart` the services arg is ignored and the entire restart op is removed (restart is binary present/absent). Used by `devbox deploy run --service <name>`. The toggle executor uses the batch `ClearPendingOps` helper instead — never the single-call form
  - All seven: `Load(path)` → mutate → `Save(path)` (atomic temp+rename, same path as `RemoveService`). On missing state file, `AddPendingOp` / `AddPendingOps` create a fresh `ProjectState`; `Clear*` and `ClearPendingOps` are no-ops on missing file; `ReplaceServiceWithPending` on missing state file treats the remove as no-op and proceeds with the add
- [ ] do NOT also expose mutation methods on `*ProjectState` — pick one API surface. Tests load state from disk via `Load(path)` to assert pending shape after each helper call
- [ ] write tests (table-driven, each asserts state by reloading via `Load(path)` after the helper call so persistence is exercised):
  - first `AddPendingOp({Restart})` creates `Operations: [{Restart}]`
  - `AddPendingOp({Deploy, ["a"]})` after a restart op → `Operations: [{Restart}, {Deploy,[a]}]` (two ops, both kinds preserved — tenth review mixed-batch regression)
  - `AddPendingOp({Deploy, ["b"]})` when a deploy op already holds `["a"]` → op merges to `{Deploy, ["a","b"]}` (sorted, deduped); still one deploy op
  - `AddPendingOp({Restart})` when a restart op already exists → no duplicate; Operations length unchanged
  - `ClearPendingForKind(path, PendingRestart)` on `[{Restart}, {Deploy,[a]}]` → `[{Deploy,[a]}]` (deploy survives; mixed pending correctly partial-cleared)
  - `ClearPendingForKind(path, PendingRestart)` on deploy-only pending → no-op
  - `ClearPending(path)` resets unconditionally
  - `ClearPendingForServices(path, PendingDeploy, ["a"])` on `[{Deploy,["a","b"]}]` → `[{Deploy,["b"]}]` (subset clear)
  - `ClearPendingForServices` removing the last service in a deploy op → op removed; if Operations empties → `Pending == nil`
  - `ClearPendingForServices(path, PendingDeploy, ["x"])` on `[{Restart}]` → no-op (wrong kind)
  - `ClearPendingForServices(path, PendingRestart, anything)` on `[{Restart}]` → restart op removed (services ignored)
  - `ClearPendingForServices` for a name not present in the op → no-op for that name (idempotent over the set difference)
  - `AddPendingOp(path, PendingOp{Kind: PendingKindUnspecified}, ...)` returns an error and writes nothing
  - `AddPendingOp` on a missing state file creates one (no error)
  - **`AddPendingOps` atomicity**: passing a slice with one valid + one `PendingKindUnspecified` op → error, state file unchanged (verifies all-or-nothing)
  - **`AddPendingOps` happy path**: two ops (deploy + restart) in one call → after reload, both ops present; `Operations` length 2; single save to disk (mock the writer if needed to assert call count)
  - **`AddPendingOps` empty slice**: → no-op, no save, no error; pre-existing state (if any) unchanged (twenty-sixth review — supports the all-`RequiresNone` toggle case)
  - **`ReplaceServiceWithPending` atomicity**: pre-seed `state.Services["a"].Status = StatusDeployed`; call `ReplaceServiceWithPending(path, "a", {Deploy, ["a"]}, hash)`; reload → `state.Services["a"]` is gone AND `Pending.Operations` contains `{Deploy, ["a"]}`; single save call
  - **`ReplaceServiceWithPending` write-failure regression**: inject I/O error on the atomic save; reload → state.Services["a"] still present, no pending entry written (twenty-third review fix for the per-service reset partial-failure)
  - **`ClearPendingOps` atomicity (happy path)**: pre-seed pending `[{Deploy, ["a","b"]}, {Restart}]`; call `ClearPendingOps(path, [{Deploy, ["a"]}, {Restart, nil}])` → reload sees `[{Deploy, ["b"]}]` (deploy op partially cleared, restart op gone); single save call
  - **`ClearPendingOps` write-failure regression**: inject I/O error on save → reload sees the original `[{Deploy, ["a","b"]}, {Restart}]` unchanged (twenty-fourth review fix for the toggle-executor partial-clear bug)
  - **`ClearPendingOps` empty slice**: → no-op, no save, no error
  - `Clear*` on a missing state file is a no-op (no error)
  - `(*PendingOp).ServiceNames()` returns a copy (mutating the returned slice doesn't affect the journal)
- [ ] run `go test ./internal/deploy/journal/...` - must pass before next task

### Task 9: `ServiceToggleHooks` schema + types in `service.yml`

- [ ] in `internal/config/devbox.go` extend `ServiceConfig` with explicit YAML tags (yaml.v3 lowercases field names by default, but the project consistently uses explicit tags — match that):
  ```go
  OnEnable  *ServiceToggleHooks `yaml:"on_enable,omitempty"`
  OnDisable *ServiceToggleHooks `yaml:"on_disable,omitempty"`
  Notes     *ServiceNotes       `yaml:"notes,omitempty"`
  ```
- [ ] **update `allowedFieldsFor` (`internal/config/devbox.go:544`) to add `on_enable`, `on_disable`, `notes` to every service type that can be toggled** (app, tool, infra — i.e. everything except types where toggling is meaningless; check the existing per-type sets). Without this, the pre-strict allowlist check at `devbox.go:1185-1193` will reject valid hook config with `ErrServiceFieldNotAllowed` before strict decode even runs. This is the critical step the third review flagged
- [ ] add `ServiceToggleHooks` with explicit nested tags:
  ```go
  type ServiceToggleHooks struct {
      Requires ToggleRequires `yaml:"requires,omitempty"`
      Before   []string       `yaml:"before,omitempty"`
      After    []string       `yaml:"after,omitempty"`
  }
  ```
- [ ] add `ServiceNotes` with explicit nested tags:
  ```go
  type ServiceNotes struct {
      Enable  string `yaml:"enable,omitempty"`
      Disable string `yaml:"disable,omitempty"`
  }
  ```
- [ ] add `ToggleRequires` string enum with explicit zero-value sentinel:
  ```go
  const (
      RequiresUnspecified ToggleRequires = ""   // zero value when field omitted in YAML
      RequiresNone        ToggleRequires = "none"
      RequiresRestart     ToggleRequires = "restart"
      RequiresDeploy      ToggleRequires = "deploy"
  )
  ```
  Helper `(ToggleRequires).OrDefault() ToggleRequires` returns `RequiresRestart` only when `RequiresUnspecified`. Note: the validator rejects unknown values BEFORE runtime defaulting (so `requires: rstart` is a validate-time error, not silently `restart`); **and `buildTogglePlan` in Task 11 ALSO rejects unknown values at runtime** so the same protection holds for callers that didn't run `devbox validate` first
- [ ] add `ToggleRequires.IsKnown() bool` returning true for `{RequiresUnspecified, RequiresNone, RequiresRestart, RequiresDeploy}` — used by both the validator and the runtime guard in Task 11
- [ ] ensure strict-decode (folder loader from Task 3 already uses `KnownFields(true)`) rejects typos in hook field names — combined with the allowlist update above, the gate is: allowlist accepts `on_enable`/`on_disable`/`notes` keys → strict decode rejects typos within those blocks (`on_enable.requirss`)
- [ ] write tests:
  - parse `service.yml` with full hooks block for each service type that should accept hooks
  - parse with partial (`requires` only); parse with absent (zero values)
  - **`allowedFieldsFor` regression**: load a service.yml with `on_enable:` succeeds (would have failed with `ErrServiceFieldNotAllowed` before the allowlist update)
  - unknown YAML field inside `on_enable:` (e.g. `requirss:`) rejected by strict decode
- [ ] run `go test ./internal/config/...` - must pass before next task

### Task 10: Lifecycle hooks validator

- [ ] new validator file in `internal/validate/config/` (e.g. `service_hooks.go`) registering scope `config.services.hooks`
- [ ] rules (all emitted with file/key location):
  - `on_disable.requires == "deploy"` → Error: `"deploy" is not allowed for on_disable; valid: none, restart`
  - `requires` unknown value → Error: `unknown requires value %q; valid: none, restart, deploy` (omit `deploy` from the list for `on_disable`)
  - **`on_enable.requires == "deploy"` on a service that has no `devbox/services/<name>/deploy.yml`** → Error: `service %q declares on_enable.requires: deploy but has no deploy.yml; either add deploy.yml or use requires: restart` (eleventh review — unifies `requires: deploy` semantics: it means "this service needs a deploy", which is only meaningful if the service can actually be deployed)
  - `before[i]`/`after[i]` referencing unknown user-command ID → Error: `references unknown command %q`
  - `before[i]`/`after[i]` referencing command whose `type` is not in `{shell, script}` → Error: `command %q has type %q; only shell/script commands can be used in hooks`
  - `mandatory: true` AND any hooks present → Warning: `hooks on mandatory service will never fire (mandatory services cannot be toggled)`
- [ ] ref-resolution happens at validate-time using the loaded user-commands registry; YAML-load does NOT resolve refs
- [ ] register in `internal/validate/config/all.go` `All()`
- [ ] write tests as a single table-driven `TestServiceHooksValidator` with one row per rule (positive + negative); each row asserts the exact `Diagnostic.Code` / `Severity` / `Hint` shape, not just count
- [ ] run `go test ./internal/validate/...` - must pass before next task

### Task 11: Toggle plan builder + renderer

- [ ] new file `internal/command/service_plan.go`: `buildTogglePlan(cfg *config.DevboxConfig, registry *registry.Registry, svcDeploys map[string]*config.DeployConfig, toggles []ToggleAction) (TogglePlan, error)` — returns an error so runtime guards have a clear exit path
- [ ] **`svcDeploys` parameter** is what the runtime guard below (`ErrDeployRequiredNoDeployFile`) needs to check `deploy.yml` presence. `DevboxConfig` itself doesn't store the per-service deploy configs (see `internal/config/devbox.go:90`); the caller (Task 13 / Task 14) loads them once via `config.LoadServiceDeployConfigs(baseDir, cfg.Services)` and passes the map. This keeps `buildTogglePlan` pure (no filesystem access) and makes the dep explicit. `baseDir` is derivable from `cfg.Raw["__configPath"]` if needed elsewhere, but for the plan builder, the loaded map is sufficient
- [ ] `ToggleDirection` string enum with explicit zero-value sentinel:
  ```go
  const (
      DirectionUnspecified ToggleDirection = ""   // zero value; rejected by buildTogglePlan
      DirectionEnable      ToggleDirection = "enable"
      DirectionDisable     ToggleDirection = "disable"
  )
  ```
  `buildTogglePlan` returns an error (or panics in test build) when any `ToggleAction.Direction == DirectionUnspecified`
- [ ] `ToggleAction{Service string, Direction ToggleDirection}`
- [ ] `TogglePlan{BeforeSteps []PlanStep, ApplySteps []ApplyStep, AfterSteps []PlanStep, Notes []string}` — **`ApplySteps` is an ordered slice** (changed from single `*ApplyStep`) so mixed batches can express "deploy these, then restart the stack" as two operations:
  ```go
  type ApplyStep struct {
      Kind     PendingKind  // PendingRestart or PendingDeploy
      Services []string     // deduped + ALPHABETICAL ORDER as a stable display key. Execution order is determined by `deploy.ResolveServicesPlanSubset` (Task 12), which topo-sorts via `deploy.TopoSortByAfter` (the new `after:`-based helper from Task 4) before producing the resolved step list. Storing the deploy target list as a set (rendered alphabetically) and letting the resolver topo-sort means `--print-plan` and the executor agree on WHAT runs, even if they disagree on rendered order vs execution order. See the render note below for how `renderTogglePlan` makes this explicit.
  }
  ```
  Empty `ApplySteps` (length 0) means no apply work; the apply phase is skipped
- [ ] **coverage rule** (eleventh review — unified semantics: every deploy contributor has `deploy.yml`, enforced by Task 10 validator + Task 11 runtime guard `ErrDeployRequiredNoDeployFile`. With that invariant, per-service deploy works in every batch shape):
  - **All-restart batch** → `ApplySteps = [{Kind: PendingRestart, Services: nil}]`. Restart is stack-wide; service list is empty
  - **All-deploy batch** → `ApplySteps = [{Kind: PendingDeploy, Services: sorted deploy-contributors}]`. `runDeployHelper(opts.Services = step.Services)` covers exactly the deploy contributors
  - **Mixed `restart + deploy` batch** → **two ops, in order**: `ApplySteps = [{Kind: PendingDeploy, Services: sorted deploy-contributors}, {Kind: PendingRestart, Services: nil}]`. The deploy step is per-service (NOT full-project) — targets exactly the deploy contributors. The restart step covers restart-only contributors (which may legitimately lack `deploy.yml`). Since the validator/runtime guard guarantees every deploy contributor has `deploy.yml`, the per-service deploy never fails on a missing file
  - **Key invariant from the unification**: a toggle-plan deploy step ALWAYS has `Services != nil`. The toggle layer never emits a "full project deploy" — that's reserved for the standalone `devbox deploy run` (no `--service`) command path. This makes the executor's per-contributor pending clear correct without special cases (Fix for the eleventh review's second finding: no "full deploy applies pre-existing pending but per-contributor clear misses it" mismatch, because the deploy step only applies the contributors)
- [ ] **pending writes for mixed batches**: build a 2-element ops slice `[{PendingDeploy, [deploy-contributors]}, {PendingRestart}]` and write via ONE atomic `journal.AddPendingOps(statePath, ops, configHash)` call (Task 8 + twenty-first/twenty-second review — never split into two `AddPendingOp` calls, which can leave half-applied pending on partial I/O failure). Banner renders both lines: "deploy required for: ..." AND "restart required". Pending is cleared per-contributor by the executor (see Task 12)
- [ ] **renderTogglePlan output** (twelfth review — every line must be either a runnable command or an unambiguous internal-step label, never invented CLI syntax). The CLI `devbox deploy run --service <name>` accepts only a single name (Task 12 keeps the surface unchanged for `devbox deploy run`); a multi-target deploy is an internal apply step, not a shell command. Render rules:
  - lines that ARE runnable commands → printed as their exact shell form:
    ```
    1. devbox commands foo:prepare
    3. devbox restart
    4. devbox commands foo:smoke
    ```
  - the multi-target deploy apply step → printed as an internal-step label with `→` prefix to visually distinguish it from a shell command. The service list is rendered as a **target set** (alphabetical), with an explicit note that execution order is topo-sorted (fourteenth review — clarifies the alphabetical vs topo split):
    ```
    2. → apply step: deploy services {bar, foo} (dependency-ordered at execution)
    ```
  - single-target deploy may use the shell form (it's runnable as-is): `2. devbox deploy run --service foo`
  - example mixed plan (two contributors, foo deploys + bar restarts):
    ```
    Plan to apply (4 steps):
      1. devbox commands foo:prepare
      2. → apply step: deploy services {foo} (dependency-ordered at execution)
      3. → apply step: restart stack
      4. devbox commands foo:smoke
    ```
  Both forms are clearly distinguishable so a user copy-pasting `devbox …` lines never invokes invalid syntax, and the executor narrates each `→ apply step` as it runs
- [ ] **render-or-execute symmetry, with explicit ordering note**: `--print-plan` shows the exact same sequence of apply steps the executor will run, including the `→ apply step` lines for internal ops. Within a single multi-target deploy step, the rendered service set is alphabetical (stable display) while execution is topo-ordered via `deploy.TopoSortByAfter` (Task 4) — the parenthetical "(dependency-ordered at execution)" calls this out so users debugging a `b before a` dependency don't suspect a bug
- [ ] **runtime validation of `requires` values** (closes the gap the fourth review flagged: `devbox validate` is not always run before `services enable/disable`). For every toggled service, when reading `OnEnable.Requires` / `OnDisable.Requires`:
  - if `!requires.IsKnown()` (the helper from Task 9) → return sentinel `ErrUnknownToggleRequires` wrapped with the offending value and service name: `fmt.Errorf("%w: service %q has requires=%q", ErrUnknownToggleRequires, svc, val)`. Callers (Task 13, Task 14, Task 16) propagate; cobra root reports
  - if `Direction == DirectionDisable` and `requires == RequiresDeploy` → return `ErrDisableDeployForbidden` similarly. (`on_disable.requires: deploy` is forbidden — validator catches it statically, runtime catches always)
  - if `requires == RequiresDeploy` and `svcDeploys[serviceName] == nil` (the deploy-configs map passed as the new third parameter to `buildTogglePlan`) → return sentinel `ErrDeployRequiredNoDeployFile` wrapped with the service name. This is the eleventh review's unified-semantics enforcement; the thirteenth review's first finding spelled out the missing parameter
  - this duplicates the validator's checks deliberately: validate catches early, runtime catches always. The repo policy "invalid config shape must be rejected, not interpreted" demands the runtime catch
- [ ] **dedup / collapse rules across multiple toggled services** (matches the coverage rule above):
  - all `RequiresNone` → `ApplySteps = nil` (empty)
  - all `RequiresRestart` → `ApplySteps = [{Restart}]` (single step)
  - all `RequiresDeploy` → `ApplySteps = [{Deploy, sorted_contributors}]` (single step, services merged + deduped)
  - **mixed `restart + deploy` → `ApplySteps = [{Deploy, sorted_deploy_contributors}, {Restart}]` (two steps, per-service deploy first then stack-wide restart per the unified coverage rule)** — superseding the previous "one deploy" rule (eighth review bug) AND the intermediate "full-deploy nil services" rule (eleventh review bug). Deploy step always carries its target list
- [ ] before/after ordering: stable sort by service name; all before-hooks precede the apply phase; all after-hooks follow it. Apply steps within the phase are ordered by the rules above (deploy before restart in mixed)
- [ ] `renderTogglePlan(w io.Writer, plan TogglePlan)` — produces the numbered plan + Notes section; section omitted when empty
- [ ] write tests:
  - single enable with restart-only hook → `ApplySteps = [{Restart}]`
  - single enable with deploy hook → `ApplySteps = [{Deploy, [svc]}]`
  - **two enables (one restart + one deploy, deploy contributor has deploy.yml) → `ApplySteps = [{Deploy, [deploy-contrib]}, {Restart}]`** (per-service deploy, NOT nil services — replaces the stale "Deploy nil" assertion from the eighth/ninth review passes)
  - **deploy contributor without deploy.yml → `buildTogglePlan` returns `errors.Is(err, ErrDeployRequiredNoDeployFile)`** (eleventh review unified-semantics regression)
  - all-none → `ApplySteps = nil` (empty)
  - before/after ordering is deterministic
  - notes printed only when present
  - **runtime guards (regression for the fourth review)**: `requires: rstart` → `errors.Is(err, ErrUnknownToggleRequires)` without any prior `devbox validate` call; `on_disable.requires: deploy` → `errors.Is(err, ErrDisableDeployForbidden)`
- [ ] run `go test ./internal/command/...` - must pass before next task

### Task 12: Toggle plan executor

- [ ] add `executeTogglePlan(ctx context.Context, deps ExecuteDeps, plan TogglePlan, opts ExecuteOptions) error` in `internal/command/service_plan.go`
- [ ] `ExecuteOptions` (tenth review — executor needs to know its contributors to clear pending correctly):
  ```go
  type ExecuteOptions struct {
      SkipHooks      bool
      NonInteractive bool
      Contributors   []Contributor  // toggle batch with resolved requires; populated by Task 13/15 callers
  }
  type Contributor struct {
      Service  string
      Requires ToggleRequires // resolved (None / Restart / Deploy)
  }
  ```
  `Contributors` is REQUIRED for non-empty plans — the executor uses it for the post-success pending clear (subset per kind). Empty `Contributors` is allowed only when `len(plan.ApplySteps) == 0` (nothing to clear). The executor validates this invariant at entry
- [ ] explicit dependency struct (so tests can stub every seam without ad-hoc globals — the dependency boundary the sixth review asked for):
  ```go
  type ExecuteDeps struct {
      // Required for delegating to existing runtimes:
      Cmd       *cobra.Command  // passed through to runDeployHelper for cmd.OutOrStdout/InOrStdin/etc.
      Flags     *rootFlags      // passed through to runDeployHelper
      BaseDir   string          // for AcquireProjectLocks inside runDeployHelper / RunRestart
      StatePath string          // for journal.AddPending / ClearPending
      Cfg       *config.DevboxConfig
      CmdReg    *registry.Registry  // user-commands registry for before/after hook lookup

      // Test seams — signatures match the real callees exactly so production
      // wiring is `Deps.RunRestart = lifecycle.RunRestart` (no adapter needed):
      RunDeploy  func(ctx context.Context, cmd *cobra.Command, flags *rootFlags, opts DeployOpts) error
      RunRestart func(rctx lifecycle.RunContext) error                                  // matches lifecycle/run.go:273 — note: takes its own context inside RunContext, no separate ctx arg
      RunUserCmd func(ctx context.Context, rc runtime.RunContext) error                 // matches usercommands/runtime/runner.go:153 — RunContext (not ExecContext)
  }
  ```
  Production callers (Task 13 / Task 14) construct `ExecuteDeps` by assigning these fields directly to `runDeployHelper`, `lifecycle.RunRestart`, and `runtime.RunCommand` — no shim layer. Tests pass stubs with the same shape that record invocations.
- [ ] **call-site construction**: `executeTogglePlan` builds the `lifecycle.RunContext` and `runtime.RunContext` values from `ExecuteDeps.Cfg` + `ExecuteDeps.BaseDir` + per-hook command ID immediately before each call. The shape of those context structs is fixed by the callees — read `internal/lifecycle/run.go:40` (`type RunContext struct`) and `internal/usercommands/runtime/runner.go:32` (`type RunContext struct`) to populate the right fields. Toggle executor is non-interactive (`NonInteractive: true`, `SkipConfirm: true`, `SkipNotify: true` on the user-command RunContext, matching the existing `type: command` check pattern in CLAUDE.md "preflight" bullet)
- [ ] guard at top: `if len(plan.ApplySteps) == 0 && len(plan.BeforeSteps) == 0 && len(plan.AfterSteps) == 0 { return nil }` (defensive — empty plan is a no-op, never an error)
- [ ] orchestrate via existing runtimes: user-commands via `internal/usercommands/runtime.RunCommand`; restart via the existing `lifecycle.RunRestart(...)` entry point; deploy via a new same-package helper we extract from `deployRunCmd` (see next bullet)
- [ ] **deploy-orchestrator extraction — stays in `internal/command/`, NOT moved to `internal/deploy/`**:
  - Today `internal/command/deploy.go:178` `deployRunCmd` is deeply wired to command-layer concerns: `render.Stdout()` (line 93, 240, 691, 697), `newNotifier(ucfg)` (line 196), `ui.RunSelector` (line 430, 469), and the exit-code-bearing errors `lockHeldError` / `deployCancelledError` (lines 159-176). Moving the body to `internal/deploy/` would drag every one of those concerns across the package boundary or require inventing 4-5 injectable interfaces just for one caller — neither is justified by current need
  - Instead, refactor in place: extract the orchestration body of `deployRunCmd` (everything after flag parsing) into a same-package helper `runDeployHelper(ctx, cmd *cobra.Command, flags *rootFlags, opts DeployOpts) error` in `internal/command/deploy.go`. `DeployOpts{Services []string, Force, Resume, NonInteractive, SkipPreflight bool}` — **slice, not single string**, because multi-toggle batches (Task 14) can produce a deploy apply step covering N services
  - Semantics of `DeployOpts.Services`:
    - `nil` / empty → deploy every **enabled** service with `deploy.yml` (matches today's `devbox deploy run` with no `--service`, which filters by `svc.Enabled` at `service_plan.go:67` — preserved per Task 7 + seventeenth review)
    - one entry → equivalent to today's `--service <name>`
    - multiple entries → deploy each of the named services (each must have `deploy.yml`; otherwise `errors.Is(err, ErrServiceNoDeployFile)` for the first offender). Implementation: in `runDeployHelper`, when `len(opts.Services) > 0`, call `deploy.ResolveServicesPlanSubset(cfg, reg, deploys, opts.Services)` (defined below — handles dedup + `after:` topo-sort + env-step prepend). When `len(opts.Services) == 0`, call `deploy.ResolveServicesPlan` — which itself is updated in this task to use `deploy.TopoSortByAfter(deploys, cfg.Services)` instead of `config.TopoSortServices` (deploy ordering uses the new `after:` field, not the runtime `depends_on:` field). The full-services map is passed so the runtime graph validation can distinguish unknown vs no-deploy-yml. Pre-release: this is a behavior change for projects that relied on `depends_on:` for deploy order; the change is documented in `docs/reference/config/deploy.md` (Task 18) so live projects can add `after:` declarations as part of their own update PRs — out of scope for this CLI-only plan. **Runtime graph validation errors (cycle / self-reference / unknown ref) propagate out of `runDeployHelper`** so a plain `devbox deploy run` against a malformed config fails clearly rather than silently sorting a bad graph (sixteenth review)
    - **subset-mode journal-check warning** (applies to ANY non-empty `opts.Services`, single or multi — fifteenth review's third finding): before resolving the plan, iterate every name in `opts.Services`. For each, look up its declared `after:` list in `deploys[name]`. Collect the set of "missing deps" — names that satisfy ALL of:
      - referenced by some service's `after:` list, AND
      - NOT in `opts.Services` (i.e. won't be deployed by THIS run), AND
      - have a `deploy.yml` (services without `deploy.yml` were warned by the validator and don't gate ordering at runtime), AND
      - `state.Services[dep].Status != StatusDeployed` (not currently deployed)
      If the missing-deps set is non-empty:
      - in TTY (`isTerminal()` true) → write `services [{a, b}] declare after: deps not in this run; missing deploys: {x, y} — proceed anyway? [y/N]` to `cmd.OutOrStdout()`, read from `cmd.InOrStdin()`; on `n`/empty → return `deployCancelledError{}` (exit 0)
      - in non-TTY → write an info line summarizing the missing-deps set to `cmd.ErrOrStderr()` and proceed
      - never block (the user explicitly asked for this subset); just inform/confirm
      - `--force` skips the prompt (proceed silently) — matches existing `--force` semantics for the resume gate
      - **NOT applied** when `len(opts.Services) == 0` (full project deploy already covers the full enabled deploy scope — every enabled service with `deploy.yml`. Disabled services with `deploy.yml` are intentionally excluded per the Task 7 + seventeenth review enabled-filter; the validator already warned at config-load time about any `after:` reference to a disabled service, so the journal-check would be redundant here)
    - **ImplicitEnvStep dedup + deploy-`after:` ordering** (eighth + ninth + new feature): `deploy.ResolveServicePlan` (`internal/deploy/service_plan.go:15-20`) prepends a `pipeline.ResolvedStep` containing `ImplicitEnvStep` to every result. A naive concat across N services would produce N env steps and re-render env N times. Additionally, a subset deploy must respect the `after:` ordering or it can deploy `b` before its declared dependency `a`. Fix: add `deploy.ResolveServicesPlanSubset(cfg, reg, deploys map[string]*config.DeployConfig, names []string) ([]pipeline.ResolvedStep, error)` in `internal/deploy/` that:
      1. validates every name has `deploy.yml` (`deploys[name] != nil`) — else wrap `ErrServiceNoDeployFile`
      2. builds the SUBSET deploy-map containing ONLY the requested names (no transitive `after:` ancestors — per `--service` semantics that does NOT cascade). Calls `deploy.TopoSortByAfter(subset, cfg.Services)` to order them by `after:` (new Task 4 helper), dependency-first with alphabetical tie-break, with `after:` references to services outside the requested set dropped from the graph as inputs to the sort. Runtime graph validation errors (cycle/self-ref/unknown) propagate
      3. resolves each in sorted order via `ResolveServicePlan`, **strips the leading `ImplicitEnvStep`** from each result
      4. prepends ONE `ImplicitEnvStep` at position 0 before the concatenated tails
      Result: exactly one env step at position 0, followed by per-service phase steps in `after:` dependency order. `runDeployHelper` calls this when `len(opts.Services) > 0`. Keep `ResolveServicePlan` (single-service) unchanged so its env-step prepend stays correct for that path
    - **Transitive closure for `after:` in subset deploys** (matches the rule in step 2 above; restated for emphasis): subset deploys do NOT auto-include `after:` ancestors per `--service` cascade-free semantics. The topo sort runs over JUST the requested names; `after:` references pointing outside the requested set are dropped from the sort input. The journal-check (see "subset-mode journal-check warning" bullet below) is how the user learns their declared deps may not be ready
  - **multi-service state/journal semantics** (thirteenth review — today's deploy.go has single-service assumptions in hashing, skip scope, project-hash stamping, and tracked-services gate; the multi-service path needs explicit rules):
    - **Hashing**: today (`internal/command/deploy.go:325-335`) the hash backfill runs only when `serviceName != ""` and only for that one name. For multi-service: iterate `opts.Services`, ensure each has a `serviceHashes[name]` entry, backfill via `LoadServiceDeployConfigs(baseDir, {name: cfg.Services[name]})` + `journal.ServiceConfigHash(...)` per missing entry. Same logic as today but in a loop
    - **Skip scope**: today (`deploy.go:340-356`) scope state is computed from either `state.Project` (full) or `state.Services[serviceName]` (single). For multi-service: compute a scope view that's "deployed AND hash-matches" iff **every** name in `opts.Services` satisfies it; if any one is stale/failed, the whole batch runs. Don't short-circuit on the first match
    - **Aggregated `scopeStatus` / `scopeLastRunStatus`** (fourteenth review — the resume gate at `deploy.go:461-475` is keyed on these two values, currently single-service): for multi-service, derive the same two values by reducing across `opts.Services`:
      - `scopeStatus` = `journal.StatusFailed` if any targeted service is `StatusFailed`; else `StatusPartial` if any is `StatusPartial`; else `StatusDeployed` if every targeted service is `StatusDeployed`; else zero value (none deployed yet)
      - `scopeLastRunStatus` = `journal.StatusFailed` if any targeted service's `LastRun.Status == StatusFailed`; else `StatusInProgress` if any is `StatusInProgress`; else the most-recent terminal status across the set (deterministic precedence: failed > in_progress > deployed)
      - This preserves `prevIncomplete` behavior across the batch: one failed targeted service still triggers the resume / `--force` / `--resume` prompt in interactive mode AND still blocks non-interactive runs that lack `--force` / `--resume`. Don't downgrade the safety of the existing single-service path
    - **Tracked-services gate**: today (`deploy.go:407-415`) `allTrackedDeployed` runs only when `serviceName == ""`. For multi-service: NOT applicable — the batch's scope is explicitly `opts.Services`, not the project. Skip this check entirely when `len(opts.Services) > 0`
    - **Project-hash stamping**: today (`deploy.go:564`) `FileRecorder` stamps project hash iff `serviceName == ""`. For multi-service: do NOT stamp project hash — same as single-service rationale (the explicit env step ran, but project-level steps did not). Pass `false` for the project-stamp arg when `len(opts.Services) > 0`
    - **Per-service stamping on success**: each name in `opts.Services` gets its `state.Services[name].Status = StatusDeployed` and `ConfigHash = serviceHashes[name]` stamped on success — same as single-service today, just in a loop. On partial failure (e.g. service `b` fails after `a` succeeded), `a`'s state is stamped, `b`'s state reflects failure; the batch as a whole returns the error. The pipeline runner already records per-step results; verify the recorder handles this multi-service grouping correctly or extend it
    - **Already-deployed subset**: if `opts.Services = ["a", "b"]` and `a` is already deployed (hash matches), the skip-decider may legitimately skip `a`'s steps while `b` runs. The existing per-step skip logic handles this; the batch-level scope check above must only short-circuit when EVERY name is already deployed
  - **multi-service tests** for `runDeployHelper` (table-driven):
    - all targeted services never deployed → full run
    - all targeted services already deployed + hash match → batch-level skip
    - mixed already-deployed + new → run only the new (per-step skip)
    - one targeted service fails mid-batch → its state reflects failure, prior successes stamped, error returned
    - `len(opts.Services) > 0` never stamps project hash (verify `state.Project.ConfigHash` unchanged after a multi-service run)
    - **prevIncomplete aggregation (fourteenth review)**: pre-seed `state.Services["a"].Status = StatusFailed`; call `runDeployHelper(opts = {Services: ["a", "b"], NonInteractive: true})` without `--force` / `--resume` → returns error (resume gate fired because `scopeStatus` aggregated to `StatusFailed` across the batch). Same call with `--force` proceeds. Same call interactive prompts via `ui.RunSelector`
    - The CLI surface for `devbox deploy run` is unchanged: still single `--service <name>` (translated into a one-element `opts.Services` slice). The slice exists purely for the toggle executor's batch case
  - The toggle executor (also in `internal/command/`) calls `runDeployHelper(ctx, cmd, flags, opts)` for each `ApplyStep` of `Kind == PendingDeploy` in `plan.ApplySteps`, with `NonInteractive: true` and `opts.Services` populated directly from `step.Services`. Per the unified coverage rule (Task 11), `step.Services` is ALWAYS non-empty for toggle-emitted deploy steps — toggle layer never triggers full-project deploys. `render.Stdout()`, `newNotifier`, `ui.RunSelector`, and the exit-code errors are all accessible without crossing package boundaries
  - Lock acquisition + preflight + notifier lifecycle stay INSIDE `runDeployHelper` — same ownership as today's cobra path. Toggle executor inherits all behavior unchanged
  - `lifecycle.RunRestart` is fine as-is in `internal/lifecycle/` (lighter dependency surface). The asymmetry is real and accepted: deploy is command-layer-shaped, restart is lifecycle-layer-shaped — forcing symmetry would invent abstractions YAGNI tells us to avoid
- [ ] write tests for the extracted `runDeployHelper` (table-driven over `opts.Services` shapes: nil/empty → full project; one element → today's `--service`; two elements → batch — each must have `deploy.yml` or `errors.Is(err, ErrServiceNoDeployFile)`. Also vary `SkipPreflight` set/unset). Existing test patterns from `deploy_test.go` translate by calling `runDeployHelper` directly instead of `cmd.Execute()`. The thin cobra wrapper gets one smoke test
- [ ] **lock ownership model** (twenty-sixth review — corrected):
  - the toggle executor does NOT hold a lock across the apply steps. `lifecycle.RunRestart` and `runDeployHelper` each acquire the project locks themselves as the cobra command would — calling them with locks already held would deadlock (the file lock is non-reentrant)
  - before/after user-command hooks also run unlocked — they may take arbitrary time and shouldn't hold the lock against concurrent inspect commands
  - **BUT the executor DOES acquire its own short lock for its own journal mutation** on apply success: wrap the single `journal.ClearPendingOps(statePath, clears)` call (the success-path clear from Task 12) in a fresh `lock.AcquireProjectLocks(deps.BaseDir)` scope. This is a brief acquire-mutate-release, fully released before the function returns. Matches the CLAUDE.md "project-lock seam" — journal writes are project state and must run under the lock — and avoids deadlock because the apply steps have already returned (their locks released) before this acquisition
  - net pattern: lock-free for apply-step orchestration; short locked-and-released window for the journal clear on success
- [ ] **preflight**: the underlying apply step (restart/deploy) runs its own preflight as it does today; the executor does NOT run a separate preflight around the whole plan
- [ ] **no hidden "unlocked" variants here.** The toggle executor delegates to in-package helpers (`lifecycle.RunRestart` for restart, `runDeployHelper` for deploy). The only "locked vs unlocked" split in this plan is the per-service stop helper in Task 15, which is internal because reset (Task 16) needs to call it under an already-held lock
- [ ] error wrapping: apply-step failure returned as `fmt.Errorf("applying %s (step %d/%d): %w", step.Kind, i+1, len(plan.ApplySteps), err)`; before/after hook failures wrapped with the command ID
- [ ] **apply-phase semantics** (revised for the eighth review's mixed-batch fix):
  - iterate `plan.ApplySteps` in order; for each step dispatch by `step.Kind`: `PendingRestart` → `deps.RunRestart(...)`; `PendingDeploy` → `deps.RunDeploy(... opts.Services = step.Services)`
  - if any step fails: stop iteration, do NOT run after-hooks, do NOT clear pending. Return wrapped error
  - if ALL steps succeed: clear pending via **one atomic `journal.ClearPendingOps` batch call** (twenty-fourth review — a per-contributor loop of `ClearPendingForServices` + a final `ClearPendingForKind` is non-atomic; a mid-loop I/O failure leaves the journal partially cleared, hiding only part of the completed batch). The single call MUST run under its own `lock.AcquireProjectLocks(deps.BaseDir)` scope (twentieth review — journal mutations are project state). The toggle's earlier lock from Task 13 was already released before the apply phase to avoid the non-reentrant deadlock; this is a fresh acquisition. Build the `clears` slice from `opts.Contributors`:
    - collect every `c.Service` with `Requires == RequiresDeploy` into a single `PendingClear{Kind: PendingDeploy, Services: [sorted deploy contributors]}` entry (only if at least one such contributor)
    - if any `c.Requires == RequiresRestart` → append one `PendingClear{Kind: PendingRestart}` entry (Services ignored; the helper removes the restart op as a whole)
    - `Requires == RequiresNone` → no entry (contributor wrote nothing to clear)
    - call `journal.ClearPendingOps(statePath, clears)` once
  - NEVER call `journal.ClearPending(statePath)` from the toggle executor — it would erase pending entries from other sessions that this toggle didn't apply
- [ ] **pending-clear cheat sheet** (consolidated — service-aware clears everywhere):
  | Caller | Helper sequence | Rationale |
  |---|---|---|
  | Toggle executor (Task 12) on success | ONE `ClearPendingOps(path, clears)` with `clears` built from Contributors (deploy contributors collapsed into one `PendingClear` entry + one restart `PendingClear` if applicable) | Atomic — either all contributor-owned pending is cleared or none. Leaves unrelated entries (other sessions) intact |
  | `devbox deploy run` (no `--service`) | `ClearPendingForKind(path, PendingDeploy)` | Full-project deploy redeploys every **enabled** service with `deploy.yml` (per the enabled-filter preserved in Task 7 + seventeenth review). Restart-only contributors without `deploy.yml` are in a DIFFERENT op (`{Restart}`) — not affected. Subset clear by kind is the precise semantic. Disabled services with `deploy.yml` are intentionally outside the deploy scope; any pending entry for one would have been written by a `--service` invocation or by reset, not by a full-deploy contributor — so this clear cannot strand them |
  | `devbox deploy run --service <name>` | `ClearPendingForServices(path, PendingDeploy, []string{name})` | Single-service deploy only applied that one; must NOT clear pending for other deploy contributors and must NOT touch the restart op |
  | `devbox restart` (no args) | `ClearPendingForKind(path, PendingRestart)` | Stack-wide restart covers every restart-kind contributor; no-op against any deploy op |
  | Project-wide `devbox reset run` | `ClearPending(path)` | Whole journal wiped; clearing everything is consistent with the reset semantic |
  | Per-service `devbox reset run --service <name>` (Task 16) | ONE `journal.ReplaceServiceWithPending(path, name, {PendingDeploy, [name]}, hash)` call — atomic: removes the service's deployed state AND writes the deploy pending op in one save | Reset re-adds deploy pending for that one service; user must run deploy to re-provision. The atomic helper replaces the previous two-call sequence (`RemoveService` + `AddPendingOp`) so a write failure can't leave deployed state cleared with no banner |
- [ ] write tests with stubbed runtimes (table-driven over plan shapes):
  - full plan executes before → ApplySteps in order → after
  - `SkipHooks=true` runs only ApplySteps
  - empty `ApplySteps` runs only before + after (no lock acquired)
  - any-step failure short-circuits remaining apply steps AND after-hooks; pending stays intact
  - all-steps success: ONE atomic `ClearPendingOps` call with `clears` derived from Contributors (deploy contributors collapsed into one `PendingClear` entry; one restart `PendingClear` if any contributor was restart). NEVER unconditional `ClearPending`. NEVER a loop of `ClearPendingForServices` (twenty-fourth review — partial-clear bug on I/O failure)
  - **executor success leaves unrelated pre-existing pending intact**: pre-seed pending = `[{Deploy, [a, x]}]`; toggle batch Contributors = `[{a, Deploy}]`; on success pending becomes `[{Deploy, [x]}]` (subset-clear regression)
  - **mixed-batch executor success leaves unrelated entries intact**: pre-seed pending = `[{Deploy, [x]}]` from a prior session; toggle batch Contributors = `[{a, Deploy}, {b, Restart}]` (with `a` having deploy.yml); apply runs `[{Deploy, [a]}, {Restart}]`; on success pending becomes `[{Deploy, [x]}]` — only `a` is removed from the deploy op (`x` from the prior session is untouched), and the restart op is cleared by the contributor's restart flag. Single `ClearPendingOps` call, single save
  - **atomic clear regression (twenty-fourth review)**: inject I/O failure on the `ClearPendingOps` save; pre-seed pending `[{Deploy, [a, b]}, {Restart}]`; toggle batch Contributors `[{a, Deploy}, {b, Deploy}, {c, Restart}]`; on apply success the clear fails → reload sees the original `[{Deploy, [a, b]}, {Restart}]` unchanged, error returned. Verifies no partial clear leaks past the apply success
  - **tenth review correctness regression**: pre-seed pending empty; toggle batch is mixed `[{a, Deploy}, {b, Restart}]`; user declines `--apply` → step 5 (Task 13) wrote pending = `[{Deploy, [a]}, {Restart}]`. User later runs `devbox deploy run` (no flags) → that path uses `ClearPendingForKind(PendingDeploy)` (per cheat sheet), pending becomes `[{Restart}]`. Banner still shows restart required. NO silent loss of the restart op — the bug the tenth review identified
  - before-hook failure short-circuits apply
  - **all-deploy single-step: `ApplySteps = [{Kind: PendingDeploy, Services: ["a", "b"]}]` invokes `RunDeploy` once with `opts.Services == ["a", "b"]`**
  - **all-restart single-step: `ApplySteps = [{Kind: PendingRestart}]` invokes `RunRestart` once with no service list**
  - **mixed batch two-step: `ApplySteps = [{Deploy, ["a"]}, {Restart}]` invokes `RunDeploy(opts.Services=["a"])` THEN `RunRestart`, in order; pending cleared only after both succeed** (eighth review correctness + eleventh review unified per-service semantics)
  - **mixed batch partial failure: deploy succeeds, restart fails → pending stays as `[{Deploy, [a]}, {Restart}]` (both ops intact — no clear runs on apply-phase failure), error returned** (covers the "restart-only without deploy.yml leaks pending" gap the eighth review identified, corrected for the list-of-ops model from the tenth review)
  - **`ResolveServicesPlanSubset` produces one env step + per-service tails** (env-dedup regression): with `names = ["a", "b"]` where `ResolveServicePlan` would each prepend its own `ImplicitEnvStep`, the merged result has exactly ONE env step at position 0, NOT two
  - **`ResolveServicesPlanSubset` respects `TopoSortByAfter` ordering** (ninth review regression + new `after:` feature): with `b.deploy.yml: after: [a]` and caller passing `names = ["b", "a"]` (reverse `after` order), the resolved plan tail places `a`'s phase steps BEFORE `b`'s. Identical ordering to what full-project deploy produces for the same `after:` graph
  - **`ResolveServicesPlanSubset` ignores `after:` references to services NOT in the subset** (transitive-closure rule): with `b.deploy.yml: after: [a]` and caller passing `names = ["b"]` only, the resolved plan contains only `b` — `a` is not auto-included. Journal-check warning is the user's signal for that case
  - **`ResolveServicesPlanSubset` with a name lacking `deploy.yml`** → wrapped `ErrServiceNoDeployFile`
  - **single-`--service` journal-check warning**: pre-seed `state.Services["postgres"]` absent or `StatusFailed`; `runDeployHelper(opts = {Services: ["main"]})` where `main.deploy.yml: after: [postgres]` → in seamed-TTY mode prompts; in non-TTY emits stderr info and proceeds; `--force` skips the prompt
  - **multi-service journal-check warning** (fifteenth review): `opts.Services = ["main", "worker"]`, both declare `after: [postgres]`, postgres has no successful deploy → ONE summarized warning naming both requesters and the missing dep
  - **subset journal-check ignores `after:` deps that ARE in `opts.Services`**: `opts.Services = ["postgres", "main"]` where `main after: [postgres]` and postgres is undeployed → no warning (postgres is being deployed in this same run)
  - **subset journal-check with all `after:` deps deployed → no warning, no prompt**
  - **full project deploy (empty `opts.Services`) → journal-check skipped entirely** (the full enabled deploy scope is in scope; disabled services are intentionally excluded by the enabled-filter and the validator already warned about `after:` references to disabled services at load time)
  - **runtime graph validation propagates to `runDeployHelper`** (sixteenth review — no `devbox validate` in the test path): with `a.deploy.yml: after: [a]` (self-reference) call `runDeployHelper(opts = {Services: nil})` directly → `errors.Is(err, ErrDeploySelfReference)`. Same for cycle and unknown-ref. Verifies the runtime gate fires without going through the validator
- [ ] run `go test ./internal/command/...` - must pass before next task (both the extracted `runDeployHelper` and the toggle executor live here now)

### Task 13: Wire `services enable <name>` / `disable <name>` end-to-end

- [ ] in `internal/command/service_toggle.go`, implement the single-service enable/disable flow if not yet implemented: helpers to write `local.yml` + regenerate `.env` (use existing helpers in `internal/localconfig` and `internal/envfile`). Keep these as discrete functions so `--print-plan` can build the plan WITHOUT calling them
- [ ] add flags: `--apply` (execute plan without prompt), `--print-plan` (pure preview; NO mutation), `--skip-hooks`
- [ ] **`--print-plan` is a pure dry-run**: build the hypothetical post-toggle service state in memory, call `buildTogglePlan`, render to stdout, exit 0. Do NOT write `local.yml`, do NOT regenerate `.env`, do NOT touch journal. Cobra long help and tests must call this out explicitly so the flag name is not surprising
- [ ] **mutation flow (everything except `--print-plan`)**. Steps 1, 2, and 5 mutate project state and MUST run under `lock.AcquireProjectLocks(baseDir)`. **Pre-state capture (step 0) is the FIRST thing inside the lock** (twenty-first review — capturing before the lock would race with another process's valid concurrent write, and rollback would restore stale bytes over it). Lock is released BEFORE step 6 because the apply step delegates to `runDeployHelper` / `lifecycle.RunRestart` which acquire the lock themselves (non-reentrant — see Task 12):
  ```
  acquire lock
    0. CAPTURE pre-state: read current bytes of local.yml and .env
       (per file: *[]byte where nil = file did not exist, &bytes = file was present)
    1. write local.yml
    2. regenerate .env
    3. buildTogglePlan(cfg, registry, svcDeploys, toggles)  ← in-memory; safe under lock
       on guard failure (ErrUnknownToggleRequires / ErrDisableDeployForbidden / ErrDeployRequiredNoDeployFile):
         → rollback steps 1-2 using captured pre-state
         → release lock → return wrapped error
    4. renderTogglePlan to cmd.OutOrStdout()  ← user-facing; no state mutation
    5. write pending entries ATOMICALLY via journal.AddPendingOps(statePath, ops, configHash)
       (single helper call from Task 8; either every op persists or none)
       on AddPendingOps failure:
         → rollback steps 1-2 using captured pre-state
         → release lock → return wrapped error
  release lock
    6. decide apply path (delegates to runDeployHelper / RunRestart which lock themselves)
  ```
  - **rollback semantics** for step 3 or step 5 guard/write failure (load-bearing — explicit about missing-file restoration):
    - On failure: for each file (local.yml, .env), if captured pre-state was "did not exist" → `os.Remove(path)` (ignore `fs.ErrNotExist`); if it was "present with bytes X" → atomic restore (`os.CreateTemp` in same dir → write → rename) of the original bytes
    - Do NOT write back an empty file when the original was absent — that would create a stale empty `.env` that subsequent commands treat as valid-but-empty config, which is a silent behavior change worse than the guard failure itself
    - Rollback runs UNDER the same lock — the user observing project state mid-failure sees either the pre-toggle state or the toggled state, never partial
    - For step 5 failure: the journal stays in its pre-toggle state because `AddPendingOps` is atomic (Task 8 — either every op persists or none); no journal cleanup needed beyond what `AddPendingOps` already guarantees
  - **building the ops slice for step 5**: derive from `opts.Contributors` exactly once before calling `AddPendingOps`. Deploy contributors collapse into one `{Deploy, [c.Service for c with RequiresDeploy, sorted]}` op; restart contributors collapse into a single `{Restart}` op if any present. All-`RequiresNone` batches produce an empty slice; `AddPendingOps` handles empty as a no-op (no save, no error — Task 8), so the call is safe to make unconditionally. The per-kind merge happens once inside `AddPendingOps` instead of N round-trips through `AddPendingOp`. Preserves mixed-batch information (one deploy op + one restart op) so a later `devbox deploy run` doesn't silently drop the restart requirement (tenth review)
  - **lock-release timing for apply success/decline**: the apply path (step 6) is what may decline / fail / succeed. On apply success the executor acquires its own lock for the clear-pending call (see Task 12). Crucial: do NOT hold the toggle lock across step 6; that would deadlock against the deploy/restart paths
- [ ] TTY detection: seamed package-level var `isTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }` (tests override). Read prompt response from `cmd.InOrStdin()` via `bufio.NewReader`
- [ ] apply decision matrix (after steps 1-5 above; pending is already written when `len(plan.ApplySteps) > 0`):
  | Flag / mode | Action |
  |---|---|
  | `--apply` | `executeTogglePlan(NonInteractive=true)`. On success the executor calls (per Task 12) ONE atomic `journal.ClearPendingOps(statePath, clears)` where `clears` is built from `opts.Contributors` (deploy contributors collapsed into one `PendingClear` entry + one `PendingClear{PendingRestart}` if any contributor was restart). NEVER a per-contributor loop; NEVER unconditional `ClearPending`. On failure: pending stays (already written in step 5); return wrapped error so the cobra root reports it |
  | TTY, no `--apply` | prompt `Run them now? [y/N] `; on `y`/`yes` → `executeTogglePlan` (same clear-on-success). On `n` / empty / anything else → leave pending in place, exit 0 |
  | non-TTY, no `--apply` | leave pending in place; print `to apply: rerun with --apply` to `cmd.OutOrStdout()`; exit 0 |
- [ ] this matches the Pending state lifecycle diagram (Technical Details below). Writing pending BEFORE apply closes the gap where `local.yml` was mutated but a failed apply would have left no banner
- [ ] `requires: none` for every toggled service → `len(plan.ApplySteps) == 0` so step 5 writes no pending; nothing to defer
- [ ] mandatory service → existing reject path stays (toggle blocked before any of this runs)
- [ ] write tests via cobra `SetArgs` (table-driven where natural):
  - `--print-plan` writes nothing (local.yml unchanged, .env unchanged, no journal entry) and prints the plan
  - `--apply` success → executes, pending cleared (verify: pre-existing pending of matching kind disappears; no new pending recorded)
  - **`--apply` failure → mutation persisted, pending STILL recorded** (regression for the gap the second review flagged)
  - TTY no-flags + user types `y` → executes, pending cleared on success
  - **TTY no-flags + user types `n` → mutation persisted, pending recorded**
  - non-TTY no-flags → mutation persisted, pending recorded, plan printed
  - **`buildTogglePlan` returns `ErrUnknownToggleRequires` → local.yml and .env restored to pre-toggle contents, no pending written** (step-3 rollback regression)
  - **rollback when `.env` did NOT exist before the toggle → `.env` is removed, not left as empty file** (missing-file rollback regression)
  - **`AddPendingOps` failure (step 5) → local.yml and .env restored to pre-toggle contents, journal unchanged, error returned** (twenty-first review step-5 rollback regression). Inject failure by seamed journal writer that returns an I/O error on the atomic save
  - **rollback when `local.yml` did NOT exist before the toggle → `local.yml` is removed, not left as empty file**
  - `--skip-hooks` apply path executes only the apply step
  - `requires: none` writes no pending and produces an empty apply step
  - **all-`RequiresNone` batch**: ops slice is empty; `AddPendingOps` is invoked (the call is unconditional) and is a no-op (no save, no journal record created — twenty-sixth review regression for the empty-record-creation bug)
- [ ] run `go test ./internal/command/...` - must pass before next task

### Task 14: Multi-select TUI toggle

- [ ] in the existing multi-select flow (`runMultiSelect = ui.RunMultiSelect` in `service_toggle.go`), build one aggregated `TogglePlan` from all toggled services using `buildTogglePlan(cfg, registry, svcDeploys, toggles)` — same `svcDeploys` map loaded as in Task 13 (load once per command invocation, not per toggle)
- [ ] apply same flags (`--apply`, `--print-plan`, `--skip-hooks`) and same TTY/non-TTY semantics as Task 13
- [ ] **same mutation flow as Task 13, including atomic pending writes** (twenty-second review): multi-select reuses Task 13's full mutation flow — acquire lock, capture pre-state, write local.yml + .env, build plan, render plan, **single `journal.AddPendingOps(statePath, ops, configHash)` batch call** (NOT per-contributor `AddPendingOp` in a loop, which would leave the journal partially mutated if the second write failed mid-batch — the exact bug Task 13 fixed). Build the ops slice from all toggled contributors at once: deploy contributors collapse into one `{PendingDeploy, [sorted services]}` op; restart contributors collapse into a single `{PendingRestart}` op if any present; pass the resulting 0/1/2-element slice to `AddPendingOps`. Same rollback semantics on guard or write failure (restore local.yml/.env to captured pre-state). Mixed batches naturally write both ops in one atomic save — restart op survives an isolated `devbox deploy run` later
- [ ] write tests:
  - **mixed `restart + deploy` batch**: `plan.ApplySteps = [{Deploy, [deploy-contrib]}, {Restart}]` (two ops, per-service deploy — coverage rule from Task 11). With `--apply`: `RunDeploy(opts.Services=[deploy-contrib])` runs first, THEN `RunRestart`; pending written as `[{Deploy, [deploy-contrib]}, {Restart}]`; on success the executor's per-contributor clear removes BOTH ops naturally. Restart-only contributors WITHOUT `deploy.yml` are covered by the explicit second restart op; deploy-contributor `deploy.yml` requirement is enforced by Task 10/12
  - **mixed batch partial failure**: `RunDeploy` succeeds, `RunRestart` fails → pending stays `[{Deploy, [deploy-contrib]}, {Restart}]` intact; executor returns wrapped error; user sees both banner lines
  - **mixed batch deferred apply (tenth review regression)**: user declines `--apply`; pending = `[{Deploy, [a]}, {Restart}]`. Later user runs `devbox deploy run` (full project) → uses `ClearPendingForKind(PendingDeploy)`; pending becomes `[{Restart}]`. Restart banner STILL shown. No silent loss
  - multi-toggle where one service resolves to `requires: none` and another to `restart` → `ApplySteps = [{Restart}]`; pending = `[{Restart}]` (single op; `none` contributor doesn't write anything)
  - **all-deploy multi-toggle apply with `--apply`**: two services both with `requires: deploy` → `ApplySteps = [{Deploy ["a","b"]}]`; `RunDeploy` called once with `opts.Services == ["a", "b"]` (sorted); pending = `[{Deploy, ["a","b"]}]`; on success per-contributor clear removes both names; deploy op collapses; Pending becomes `nil`
  - **atomic pending write regression (twenty-second review)**: inject I/O failure in the seamed journal writer during `AddPendingOps`; multi-select toggle of `[{a, Deploy}, {b, Restart}]` → local.yml + .env restored from pre-state, journal unchanged, error returned. Verifies multi-select uses the batch helper (a per-contributor loop would have left the deploy op persisted before the restart op failed)
  - all-`none` batch → no apply step, no pending
  - `before`/`after` ordering deterministic across services
- [ ] run `go test ./internal/command/...` - must pass before next task

### Task 15: `devbox stop [service]` — per-service stop

**Problem**: today `internal/command/stop.go` declares `cobra.NoArgs` and routes everything through `lifecycle.RunStop()`, which always stops the whole stack via the rendered compose file list. Task 16 (per-service reset) needs to stop a single container — and crucially, it must work **after** the service was disabled, when `config.ComposeFiles()` (`internal/config/devbox.go`) has already filtered the disabled service's compose file out of the rendered project. Calling `docker compose stop <name>` would then fail because the service no longer belongs to the compose project.

**Resolution**: stop the container directly via `docker stop <containerName>`, bypassing compose. This mirrors the pattern already in `internal/builtin/daemon_stop.go` (`docker stop -t <secs> <fullName>`, idempotent on missing container).

- [ ] relax `internal/command/stop.go` from `cobra.NoArgs` to `cobra.MaximumNArgs(1)`
- [ ] no-arg path: unchanged (full-stack `lifecycle.RunStop`)
- [ ] one-arg path: validate `<name>` exists in `cfg.Services` regardless of enabled state (use the Task 3 `LoadServices` result via `composeFiles(true)`-style "include disabled" enumeration). If missing → sentinel `ErrUnknownService` wrapped with name; reuse from existing code if a sentinel already exists
- [ ] resolve container name: render the service's container template via `daemon.ResolveContainerName(projectFullName, renderedTemplate)`; the template comes from `svc.Container` (with normal `${...}` rendering)
- [ ] add a new helper in `internal/docker/` (e.g. `StopContainer(ctx, dockerBin, name string, timeoutSec int) error`) that issues `docker stop -t <timeoutSec> <containerName>` directly (no compose). Idempotent on missing container: parse stderr / check exit status the same way `daemon_stop.go` does and treat "No such container" as success
- [ ] timeout: reuse the same default constant `daemonStopBuiltin` uses today (10s). If the daemon-stop value is hardcoded inline, extract it to an exported package constant (`docker.DefaultStopTimeoutSec`) so the two call sites share a single source. Do NOT invent a new `cfg.Docker.StopTimeout` config field — there isn't one today and the spec doesn't justify adding one
- [ ] **preflight**: per-service stop runs `preflight.Run(ctx, cfg, cmdRegistry, baseDir, "stop", skip, errOut)` BEFORE acquiring locks, matching the CLAUDE.md "preflight + env/checks domains" invariant for lifecycle commands. The env `ports_free` probe self-skips on `Stage == "stop"`, so this is mostly a docker-bin / docker-daemon / git-bin check — cheap but it MUST run for consistency with the rest of stop/restart/run. `--skip-preflight` is honored the same way as on the other four lifecycle commands
- [ ] project lock: acquire `lock.AcquireProjectLocks(baseDir)` after preflight for the one-arg path (it mutates Docker state); release after the stop call
- [ ] `ValidArgsFunction` for tab-completion of the positional arg: return enabled+disabled service names + `cobra.ShellCompDirectiveNoFileComp`; guard via `completionConfigPath` per CLAUDE.md
- [ ] **API exposed for Task 16 (the only place we accept a locked/unlocked split)** — explicit dependency struct so preflight has what it needs:
  ```go
  type StopServiceDeps struct {
      Cfg           *config.DevboxConfig
      CmdRegistry   *registry.Registry   // required for type: command preflight checks
      BaseDir       string
      ErrOut        io.Writer            // preflight diagnostics writer
      SkipPreflight bool                 // honors the --skip-preflight flag
  }

  // Public entry point used by `devbox stop <name>`.
  func StopService(ctx context.Context, deps StopServiceDeps, name string) error

  // Package-internal core, used by reset (Task 16) which already ran preflight
  // and holds the project locks. Same deps struct minus SkipPreflight (caller
  // already decided that upstream).
  func stopServiceLocked(ctx context.Context, deps StopServiceDeps, name string) error
  ```
  - `StopService`: calls `preflight.Run(ctx, deps.Cfg, deps.CmdRegistry, deps.BaseDir, "stop", deps.SkipPreflight, deps.ErrOut)` → on success `lock.AcquireProjectLocks(deps.BaseDir)` → `stopServiceLocked(...)` → defer release
  - `stopServiceLocked`: resolves container name, calls `docker.StopContainer(...)`, returns
  - The stop cobra command (`internal/command/stop.go`) loads cfg + registry the same way the other lifecycle commands do (CLAUDE.md "Grouped commands: no child `PersistentPreRunE`"), constructs `StopServiceDeps`, and calls `StopService`
  - This split is what the review asked for: callers either own the lifecycle (`StopService` for the standalone `devbox stop <name>` command) or own preflight + locks themselves and reach for the inner helper (`stopServiceLocked` for reset). No reentrant-lock magic, no caller flags
- [ ] write tests (table-driven, fresh command per test):
  - no-arg path delegates to `lifecycle.RunStop` (stub)
  - one-arg with known enabled service → resolves container + calls `StopContainer` with the right name
  - one-arg with known **disabled** service → still resolves container + calls `StopContainer` (compose-bypassed path)
  - one-arg with unknown service → `errors.Is(err, ErrUnknownService)`
  - `StopContainer` returns nil when docker reports "No such container" (idempotent)
  - `StopContainer` returns wrapped error on any other docker failure
- [ ] update `docs/reference/cli/stop.md` (or wherever the stop reference lives — regenerate via `devbox docs generate`)
- [ ] run `go test ./internal/command/... ./internal/docker/...` - must pass before next task

### Task 16: `reset run --service <name>`

- [ ] add `--service <name>` local flag on the existing `reset run` command (`internal/command/reset.go`)
- [ ] validation (twelfth review — order matters because step 4 writes a pending entry whose remediation must be runnable):
  - service must exist in `devbox/services/<name>/` → otherwise sentinel `ErrUnknownService`
  - **service MUST have `devbox/services/<name>/deploy.yml`** → otherwise sentinel `ErrServiceNoDeployFile` (the same sentinel Task 7 uses) wrapped with the service name and a hint: `per-service reset clears deployed state and requires a subsequent deploy; service %q has no deploy.yml, so its deployed state cannot be re-provisioned. Use full 'devbox reset run' instead.` This prevents the per-service reset path from creating a `PendingDeploy` entry whose suggested remediation (`devbox deploy run --service <name>`) would itself fail
  - mandatory services are **allowed** (per the earlier spec; mandatory protects from disable, not from reset)
- [ ] confirmation: in TTY without `--yes` prompt; for mandatory services append: `service is mandatory; reset will clear its deployed state and require a subsequent deploy. continue?`
- [ ] execution (per-service path):
  1. if service is currently enabled AND `on_disable.before` exists → run those user-commands first (use `usercommands/runtime.RunCommand`)
  2. `stopServiceLocked(ctx, deps, name)` (unqualified — reset.go is already in package `command`, same as Task 15's helper). `deps` is the `StopServiceDeps` struct from Task 15 with `Cfg`, `CmdRegistry`, `BaseDir`, `ErrOut` already in scope. Works whether the service is currently enabled or disabled because it bypasses compose and goes through `docker stop <container>` directly. Reset acquires the project locks once (covering steps 2-4 below), so it MUST call the locked variant rather than `StopService` (which would try to re-acquire the lock and deadlock)
  3. if `devbox/services/<name>/reset.yml` exists → execute it (reuse the same phase/step runner as project-wide reset)
  4. **single atomic journal update** (twenty-third review — replaces the previous two-call `RemoveService` then `AddPendingOp` sequence which could leave deployed state cleared with no banner if step 5 failed): `journal.ReplaceServiceWithPending(statePath, name, journal.PendingOp{Kind: journal.PendingDeploy, Services: []string{name}}, currentConfigHash)` — package helper from Task 8 that loads ONCE, removes `state.Services[name]`, adds the deploy pending op, saves ONCE atomically. Either both mutations persist or neither. Safe to suggest `devbox deploy run --service <name>` because the validation above already enforced `deploy.yml` presence; banner says "deploy required for: <name>"
- [ ] project-wide path (no `--service`) unchanged
- [ ] **preflight**: reset runs stop-stage preflight once at the top (before on_disable hooks); the `stopServiceLocked` call in step 2 does NOT re-run it
- [ ] **project lock**: acquired ONCE around steps 2-4 via `lock.AcquireProjectLocks(baseDir)` (step 4's `journal.ReplaceServiceWithPending` is a journal mutation and must run under the lock, same rule as the toggle executor's pending writes in Task 13). On_disable.before hooks (step 1) run outside the lock — they're user commands that may take arbitrary time and shouldn't hold the lock against concurrent inspect commands. Using `stopServiceLocked` (not `StopService`) inside the lock avoids double-acquiring the non-reentrant file lock
- [ ] write tests:
  - per-service reset with reset.yml present + absent
  - mandatory service confirmation path
  - enabled service runs hooks / disabled service skips hooks
  - **disabled service stop succeeds** (compose-bypass regression)
  - journal entry removed
  - pending entry written with kind=deploy
  - **service without `deploy.yml` → `errors.Is(err, ErrServiceNoDeployFile)` BEFORE any side effect; no container stop, no hook run, no journal mutation** (twelfth review regression — prevents writing impossible pending)
- [ ] update `docs/reference/config/reset.md` with per-service reset section and the symmetry table from the spec
- [ ] run `go test ./internal/command/... ./internal/deploy/journal/...` - must pass before next task

### Task 17: Pending banner in `devbox status`

- [ ] put the renderer in `internal/ui/` alongside the existing string-returning renderers (`internal/ui/info.go:RenderInfo`, `internal/stack/deploystatus.go:RenderDeployStatus`) — NOT in `internal/command/statusview/` (that package owns view-model types and should not gain rendering/journal behavior). Signature: `func RenderPendingBanner(p *journal.PendingApply) string` — empty string when `p == nil` or `len(p.Operations) == 0`. Iterates `p.Operations` and renders ONE line per op:
  - `PendingDeploy` with services → `"⚠ Pending: deploy required for: a, b\n  Run: devbox deploy run"` (uses defensive-copy `op.ServiceNames()`)
  - `PendingRestart` → `"⚠ Pending: restart required\n  Run: devbox restart"`
  - Mixed pending (both ops present) → both lines, one after the other. The list-of-ops model from Task 8 is what makes per-op rendering possible
- [ ] command-layer composes the banner: `internal/command/status.go` (and each of its subcommands `apps`, `tools`, `infra`, `deploy`) call `ui.RenderPendingBanner(state.Pending)` and write the result to `cmd.OutOrStdout()` BEFORE the main table. Empty string → no-op (no blank line)
- [ ] clear-pending hooks (verify integration points):
  - successful `devbox restart` (no args) → `journal.ClearPendingForKind(statePath, journal.PendingRestart)` (no-op when kind is deploy)
  - successful **full-project** `devbox deploy run` (no `--service`) → `journal.ClearPendingForKind(statePath, journal.PendingDeploy)` (clears ONLY the deploy op; any pending restart op must survive because a full deploy does not perform a stack-wide restart of services it didn't touch — eleventh review fix, matches the cheat sheet at Task 12)
  - successful `devbox deploy run --service <name>` → `journal.ClearPendingForServices(statePath, journal.PendingDeploy, []string{name})` (subset clear — does NOT erase pending for other services. Ninth review's primary fix)
  - successful project-wide `devbox reset run` (no `--service`) → `journal.ClearPending(statePath)` (whole journal wiped)
- [ ] write tests:
  - banner with restart-kind, banner with deploy-kind, no banner when `Pending == nil`
  - `devbox restart` success clears only restart-kind (deploy-kind survives)
  - full `devbox deploy run` success clears ONLY the deploy op; a pre-existing restart op survives (regression for the eleventh review's first finding — full deploy must not erase restart pending)
  - **`devbox deploy run --service a` with pending `{deploy, [a, b]}` → pending becomes `{deploy, [b]}`; banner still shown for `b`** (ninth review's primary regression)
  - **`devbox deploy run --service a` with pending `{deploy, [a]}` → pending becomes `nil`; banner removed** (auto-collapse path)
  - The clear-on-success integration tests for restart live with the restart code path, not the status renderer
- [ ] run `go test ./internal/ui/... ./internal/command/... ./internal/lifecycle/... ./internal/deploy/journal/...` - must pass before next task (restart clearing is wired in `internal/lifecycle`, deploy clearing is wired in `runDeployHelper` in `internal/command`, banner renderer lives in `internal/ui`, journal helpers in `internal/deploy/journal`)

### Task 18: Docs sweep

- [ ] `docs/reference/cli/completion.md` — regenerate via `devbox docs generate` (covers install/uninstall)
- [ ] `docs/reference/cli/stop.md` — regenerate via `devbox docs generate` (covers per-service stop)
- [ ] `docs/reference/cli/index.md` (or README) — short how-to for `completion install`
- [ ] `docs/reference/config/services.md` (or `devbox.md`) — document per-service folder layout; remove single-file `services.yml` references
- [ ] `docs/reference/config/deploy.md` — already updated in Task 7; verify
- [ ] `docs/reference/config/reset.md` — already updated in Task 16; verify project + per-service flow
- [ ] `docs/reference/config/state.md` — document `Pending` field + lifecycle (write on toggle-without-apply, clear on restart/deploy/project-reset)
- [ ] `docs/internals/packages.md` — update `internal/config` description (per-folder layout, new loaders), `internal/deploy/journal` (Pending), `internal/command/service_plan.go` (new file)
- [ ] CLAUDE.md "Key Patterns" — add a bullet on per-service folder symmetry, one on the pending-state lifecycle, and one on the compose-bypass rationale for per-service stop
- [ ] no tests for this task (pure docs)
- [ ] run `make build` + `devbox docs generate` to ensure CLI reference matches

### Task 19: Verify acceptance criteria

- [ ] all sections of the spec implemented; cross-reference each numbered direction (§1-§6) + per-service stop
- [ ] all validator rules from spec §7 emit expected diagnostics
- [ ] `make test` passes (full suite)
- [ ] `make lint` passes with no new warnings
- [ ] `make build` produces working binary; spot-check:
  - `devbox completion install --dry-run zsh`
  - `devbox services enable <some-service> --print-plan`
  - `devbox stop <some-enabled-service>` and `devbox stop <some-disabled-service>` (compose-bypass regression)
  - `devbox reset run --service <some-service> --yes`
- [ ] grep confirms no remaining references to old paths: `devbox/services.yml`, `devbox/deploy/`, `ErrDeployFileForNonApp`, `ErrDeployTargetNotApp`, `IsApp()` in deploy-related code

## Technical Details

### Pending state lifecycle

```
                                      ┌──────────────────────────┐
   services enable/disable        ───►│  AddPendingOps           │
   (any mode except --print-plan,     │  (one atomic call,       │
   requires != none — toggle          │   deploy ops merged into │
   builds 0/1/2-op slice once,        │   one entry, restart op  │
   one save)                          │   deduped — Task 13/15)  │
   reset run --service <name>     ───►│  ReplaceServiceWithPending│
   (one atomic call: clears state +   │  (combines remove +      │
   writes pending in one save)        │   AddPendingOp — Task 16)│
                                      └────────┬─────────────────┘
                                               │
              ┌─────────────────┬──────────────┼──────────────┬─────────────────┐
              │                 │              │              │                 │
   ┌──────────▼─────┐ ┌─────────▼────┐ ┌───────▼────┐ ┌───────▼────────┐ ┌──────▼────────┐
   │ devbox restart │ │ deploy run   │ │ deploy run │ │ reset run      │ │ toggle exec   │
   │ (no args)      │ │ (no --service)│ │ --service N│ │ (project-wide) │ │ (this batch)  │
   └──────────┬─────┘ └─────────┬────┘ └───────┬────┘ └───────┬────────┘ └──────┬────────┘
              │                 │              │              │                  │
  ClearPending     ClearPending      ClearPending    ClearPending       ClearPendingOps
  ForKind(restart) ForKind(deploy)   ForServices     (everything;       (one atomic call;
  (removes only    (removes the      (deploy, [N])   journal wiped)     clears = deploy
   the restart op; deploy op;        (subset clear;                     contributors merged
   any deploy op   any restart op    other deploys                      + restart entry if
   survives)       survives)         and the restart                    any — Task 12/24th
                                     op survive)                        review)
```

Each `devbox` operation writes and clears exactly the pending ops it actually applied — no broader, no narrower. Every write and every batch clear is a single atomic save (list-of-ops `Pending.Operations` shape from Task 8 + the `AddPendingOps` / `ClearPendingOps` / `ReplaceServiceWithPending` batch helpers).

### Symmetry table

```
devbox/devbox.yml         ↔  devbox/services/<name>/service.yml
devbox/deploy.yml         ↔  devbox/services/<name>/deploy.yml
devbox/reset.yml          ↔  devbox/services/<name>/reset.yml

devbox deploy run                   ← deployed state ON (project)
devbox deploy run --service <name>  ← deployed state ON (one)
devbox reset run                    ← deployed state OFF (project)
devbox reset run --service <name>   ← deployed state OFF (one)
devbox services enable <name>       ← part of stack ON
devbox services disable <name>      ← part of stack OFF
```

Two independent axes: "is it part of the stack" (`local.yml`) × "are artifacts provisioned" (journal).

## Post-Completion

**End-to-end smoke tests against a fixture project** (manual, in this repo):
- `devbox services enable <infra-service> --apply` against a `testdata/` fixture (covers Task 7 + Task 13 + Task 17 banner integration end-to-end).
- `devbox stop <enabled-service>` and `devbox stop <disabled-service>` (covers Task 15 compose-bypass path).
- `devbox reset run --service <name>` followed by `devbox deploy run --service <name>` (covers Task 16 + banner clear).

**Live monorepo project migration** is out of scope for this plan. Once this CLI release lands, the live projects sitting alongside the CLI in the monorepo will need separate update PRs to migrate `devbox/services.yml` + `devbox/deploy/<name>.yml` to the per-service folder layout, and to add `after:` declarations where the new deploy-ordering field replaces implicit `depends_on:`-based deploy order. None of that is implemented by this plan.

**Shell completion install** (manual):
- Run `devbox completion install` on zsh, bash, and fish hosts; verify completion fires after starting a new shell.
- On zsh, verify the `fpath` hint appears the first time when `~/.zshrc` lacks the entry.

**External consumers**: none (pre-release, no external users per CLAUDE.md).

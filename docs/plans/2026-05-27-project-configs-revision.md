# Project Configs Revision

## Overview

Implement 30+ decisions agreed in the brainstorm session covering revision of project-level Devbox configs (devbox.yml + devbox/*.yml + per-service folders + local.yml). Decisions span schema changes (breaking + additive), new validators, refactors, dead-code removal, and documentation updates.

**Scope**: This plan covers the **CLI repository only** (`/Users/s/Projects/devbox/next/cli`). Downstream projects that consume Devbox configs are out of scope — their maintainers update them separately. CLI tests must be sufficient on their own to verify each change.

Goals:
- Eliminate dead config surface (`InfoSettings`, partial-gates that don't fire).
- Move binary paths to user-level config (`~/.config/devbox/config`) where they belong; remove project-level `binaries:` block.
- Rename for clarity (`mandatory` → `required`, `host_key`/`port_key` → `primary_host`/`primary_port`).
- Tighten schemas (per-type allowlists for `CommandDef`, split `DeployConfig` / `SnapshotWorkflow`).
- Add typo-catching validators (untyped top-level keys, unknown stage values).
- Categorize the builtin registry (action / predicate / internal) with user-callable gating.
- Make docs match reality and surface design intent (type matrix rationale, render-time auto-blocks, etc.).

## Context (from discovery)

**Files central to most decisions:**
- `internal/config/devbox.go` (80k — DevboxConfig + ServiceConfig + DeployConfig + LifecycleConfig + loaders)
- `internal/config/docker.go` (DockerConfig + DockerArgs + DockerEnvConfig)
- `internal/config/info.go` (InfoConfig + InfoItem + AutoURLs/Hosts specs)
- `internal/config/snapshot.go` (SnapshotConfig + SnapshotWorkflow)
- `internal/usercommands/model/types.go` (CommandDef + 8 command types + DaemonSpec)
- `internal/userconfig/` (flat `key=value` parser at `~/.config/devbox/config`)
- `internal/builtin/builtin.go` (17-entry builtin registry)
- `internal/i18n/{bundle,store,translator}.go` (CommandStrings + ParamStrings)
- `internal/validate/config/devbox.go`, `internal/validate/config/validate_yml.go`, `internal/validate/setup/validators.go`, `internal/validate/linters/` (validator wires)

**Key patterns observed:**
- `allowedFieldsFor(ServiceType)` in `internal/config/devbox.go:631-668` is the model for per-type field gates (used in 7.1+7.2).
- `KnownFields(true)` strict YAML decoding is the validation contract (loader rejects unknown fields).
- `Source*Spec` pointer pattern in `InfoItem` and `CommandDef.SourceDaemon` is the model for render-time / expansion-time data carriers.
- Validator framework in `internal/validate/` dispatches by domain via `Registry.MatchScope`.
- `internal/builtin/shell.go` is intentionally distinct from `DeployStep{type: shell}` — per CLAUDE.md, the `shell` builtin uses hardcoded `sh -c` for portability (same rationale as condition predicates).

## Development Approach

- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` after each change
- per CLAUDE.md "rename freely, restructure freely" — no compatibility shims; downstream projects update separately, not in this plan

## Testing Strategy

- **unit tests**: required for every code-changing task — table-driven where applicable, located adjacent to the source file (e.g. `internal/config/devbox_test.go`)
- **CLI-internal integration test**: `make build` (regenerates embedded docs) and `make test` (full suite) after each task
- **doc-only tasks**: verification = `make build` succeeds (embedded docs regenerate cleanly) and no broken cross-references
- **e2e tests**: project has no UI e2e suite — N/A

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

23 tasks grouped into 5 phases that match risk profile and shippability:

1. **Phase D — Refactor & cleanup** (Tasks 1-6): internal-only changes, no schema impact. Validates the approach safely.
2. **Phase B — Additive schema** (Tasks 7-10): forward-compatible additions; existing configs continue to work.
3. **Phase C — New validators / features** (Tasks 11-13): new safety nets (typo warnings) and linter binary overrides.
4. **Phase A — Breaking schema** (Tasks 14-20): user-visible renames and removals. KnownFields strict-decode will reject old shapes with clear errors.
5. **Phase E — Standalone docs + verification** (Tasks 21-23): documentation that isn't bound to a specific code task, plus final verification.

Each task is scoped to a single decision (or two tightly related decisions). Doc updates that are inherent to a code change live inside the same task; standalone doc-only items get their own task in Phase E.

## Technical Details

**User-config schema extension (Tasks 13, 14):** existing flat `key=value` parser at `internal/userconfig/parser.go` accepts arbitrary `binary_*` keys. Single resolution function `userconfig.BinaryOverride(name) (string, bool)` consumed by both engine accessors (Task 14) and linter loader (Task 13). Precedence: user-config absolute path > validate.yml `bin:` (linters only) > default name via `exec.LookPath`.

**Accessor seam architecture (Task 14)** — DECISION RECORDED:
- Engine accessors (`DockerBin`, `GitBin`, etc.) gain an internal `*userconfig.Config` reference threaded via `cfg.userConfig` (an unexported field on `DevboxConfig`). `LoadConfig` populates it. Accessor signature stays `DockerBin(cfg *DevboxConfig) string` (no caller-site cascade). For paths where `cfg` is nil or `cfg.userConfig` is nil (test fixtures, completion path), accessors gracefully fall back to PATH-default name strings.
- Rejected: package-level `var cached *userconfig.Config` (untestable global, race-prone).
- Rejected: pass `*userconfig.Config` to every accessor (huge mechanical cascade).

**Per-type allowlist (Task 19):** mirrors `allowedFieldsFor(ServiceType)` pattern. Map keyed by `CommandType`. Loader decodes into raw `map[string]any`, validates keys against allowlist, then unmarshals into typed `CommandDef`. Diagnostics: `"command %q: field %q not allowed for type %q"`.

**Builtin categorization (Task 5):** put `Kind` as a field on a new `registryEntry` struct, NOT as a method on the `Builtin` interface. Categorization is metadata about HOW a builtin is registered/used (the right way to call it), not WHAT it does (no instance state involved). Single-site categorization at registration > 17× boilerplate `func (X) Kind() BuiltinKind { return KindY }`. The skill review (`golang-structs-interfaces`) confirms: keep interfaces small; don't add methods that return static data. Both `Get(name, ctx)` AND the package-level wrappers (`Validate`, `Run`, `Describe`) accept a `CallerContext` to enforce kind/context pairing via the entry's `Kind` field. Rejection messages point to the right context.

**Untyped-key info diagnostic (Task 12):** new validator in `internal/validate/config/devbox.go` iterates `Raw` keys for each of the three layers (devbox.yml, defaults.yml, local.yml), emits `SeverityInfo` for keys not in the known-typed set `{schema_version, project, runtime, state, exports, compose, ui, docs, services}`.

**Stage enum warning (Task 11):** validator extends existing `internal/validate/config/validate_yml.go` (NOT a new file). Iterates `cfg.Validate.Checks[*].Stages`, compares against the **actual preflight stage vocabulary** `{deploy, run, stop, command}` per `internal/config/validate.go:29-34`, emits warning per unknown value with Levenshtein-suggested correction if close. Note: `restart` is NOT a stage (composes stop+run); `reset` is NOT a stage (uses stop-stage preflight). A check declared with `stages: [restart]` would never fire — hence the warning.

**Mandatory→Required rename (Task 16) — SCOPE LIMITED:** rename applies to `ServiceConfig.Mandatory` only. Distinct fields `setup.ServiceToggle.Mandatory` and `localconfig.ServiceSelection.Mandatory` are separate types representing different concepts (wizard toggle state, local selection state) — they stay unrenamed unless a follow-up plan decides otherwise.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code changes, tests, and doc updates inside the CLI repo
- **Post-Completion** (no checkboxes): downstream-project updates (separate work by their maintainers), manual end-to-end verification scenarios, human review of validator messages for tone/clarity

## Implementation Steps

### Phase D — Refactor & cleanup (no schema impact)

### Task 1: Remove dead InfoSettings

**Files:**
- Modify: `internal/config/info.go` (struct removal + loader-level rejection)
- Modify: `internal/config/info_test.go`
- Modify: `internal/config/testdata/` (any fixture with `settings:` under info)

**Loader-level rejection (per Codex v3 review):** `LoadInfoConfig` uses lenient `yaml.Unmarshal` (`info.go:~258,~270`). Simply removing the `Settings` field would make old `settings:` blocks **silently ignored** by normal `devbox info` paths — violating CLAUDE.md "invalid shapes must be rejected, not reinterpreted". Same pattern as the v2 fix for `LoadConfig`/`LoadDockerConfig`.

- [x] delete `Settings InfoSettings` field from `InfoConfig` struct (info.go:14)
- [x] delete `InfoSettings` struct definition (info.go:21-25)
- [x] update `DefaultInfoConfig()` (info.go:~224) to drop the `Settings: InfoSettings{...}` initializer
- [x] **add loader-level rejection in `LoadInfoConfig`**: after loading raw map, inspect for top-level `settings:` key; if present, return error: ``settings: removed from info.yml — the line_width customization was never wired up. See docs/reference/config/info.md.``
- [x] grep `internal/` for any remaining references to `InfoSettings` or `cfg.Info.Settings` — remove if found
- [x] grep `internal/config/testdata/` for fixtures with `settings:` block under info — clean them up (none found)
- [x] update tests in `info_test.go` — remove any cases referencing `settings:` block (none found)
- [x] add test calling `LoadInfoConfig` directly with `settings:` block in info.yml → expect explicit migration error
- [x] run `go test ./internal/config/...` — passed

### Task 2: Remove "binaries" from forbiddenRoots in setup validator

**Files:**
- Modify: `internal/validate/setup/validators.go`
- Modify: `internal/validate/setup/validators_test.go`

- [x] remove `"binaries"` from `forbiddenRoots := []string{...}` (validators.go:269)
- [x] update test cases that check forbidden-root rejection for `binaries.*` writes
- [x] run `go test ./internal/validate/setup/...` — must pass before Task 3

### Task 3: Split SnapshotWorkflow → SnapshotWorkflow + SnapshotVariant

**Files:**
- Modify: `internal/config/snapshot.go`
- Modify: `internal/config/snapshot_test.go`
- Modify: `internal/snapshot/exec.go`
- Modify: `internal/snapshot/create_test.go`
- Modify: `internal/snapshot/exec_test.go`
- Modify: `internal/snapshot/restore_test.go`
- Modify: `internal/validate/snapshot/validators_test.go`

- [x] create new struct `SnapshotVariant { Description string; Steps []model.WorkflowStep }` in `snapshot.go`
- [x] change `SnapshotWorkflow.Variants` type from `map[string]SnapshotWorkflow` to `map[string]SnapshotVariant`
- [x] update `exec.go:153` variant lookup (compile-fine since fields are subset)
- [x] decide what to do with the existing runtime check `if len(variant.Variants) > 0 { return error }` at `snapshot.go:~190` — now unreachable; DELETE (with comment explaining structural enforcement) OR KEEP as defense-in-depth — pick one and document — DELETED with structural enforcement comment
- [x] add table-driven test case rejecting nested `variants:` (KnownFields error now) — Updated test to expect KnownFields error
- [x] add test confirming flat one-level variants still work — Existing tests confirm this
- [x] run `go test ./internal/config/... ./internal/snapshot/...` — must pass before Task 4 — All tests pass

### Task 4: Confirm `shell` builtin distinction; document it; fix Task 5 categorization

**Files:**
- Read-only: `internal/builtin/shell.go`, `internal/condition/condition.go` (verify `when:` doesn't route through builtin registry)
- Modify: `docs/reference/config/deploy.md` (add subsection clarifying difference)
- Modify: `docs/reference/config/validate.md` (cross-reference if `cmd: shell` is documented there)

Per CLAUDE.md the `shell` builtin uses hardcoded `sh -c` (deliberate — portability rationale matches condition predicates). It is NOT redundant with `DeployStep{type: shell, cmd: <command>}` which uses `ShellBin`. This task documents the distinction; no code change.

- [x] read `shell.go` and verify the hardcoded `sh` claim
- [x] verify whether `internal/condition/condition.go:113` (`exec.Command("sh", "-c", command)` for `when:`/`check:` shell predicates) goes through the builtin registry or directly. Outcome determines Task 5 categorization of `shell`:
  - If only validate.yml `cmd: shell` calls it → Predicate kind (current plan)
  - If `when:`/`check:` also call it → still Predicate (consistent with use)
  - If used internally only → Internal kind (re-categorize in Task 5)
- [x] add deploy.md subsection "`cmd: shell` (builtin) vs `type: shell` (step)" — explain: step `type: shell` uses `config.ShellBin(cfg)` (user-configurable); builtin `cmd: shell` uses hardcoded `sh -c` for portability invariants. Show example of each.
- [x] cross-reference from validate.md if it uses `cmd: shell` (per testdata)
- [x] confirm via `make build` that embedded docs regenerate cleanly — must pass before Task 5

### Task 5: Categorize builtin registry (action / predicate / internal)

**Files:**
- Modify: `internal/builtin/builtin.go` (registry shape + entry type + wrappers)
- Modify: `internal/builtin/builtin_test.go`
- Modify: all callers of `builtin.{Get,Run,Validate,Describe}` (see discovery step)
- Modify: `docs/reference/config/deploy.md` (add "Internal engine builtins" subsection)

**Design (per `golang-structs-interfaces` skill):** Kind lives on a registry-entry struct, NOT as an interface method. Categorization is registration metadata, not behavior — no need to add boilerplate to 18 builtin implementations. The `Builtin` interface stays at its current 3-method size (Validate / Describe / Run).

- [x] **discovery**: grep `builtin\.\(Get\|Run\|Validate\|Describe\)\|registry\[` across `internal/` — enumerate every call site. Likely list (verify): `internal/pipeline/resolve.go`, `internal/validate/checks/loader.go`, `internal/validate/linters/runtime.go` (for `type: generic`?), `internal/usercommands/runtime/` (builtin command type), `internal/condition/` (when: predicates), package-level wrappers in `builtin.go` itself.
- [x] add `BuiltinKind` type with constants `KindAction`, `KindPredicate`, `KindInternal` to `builtin.go`
- [x] add `CallerContext` type (e.g. `CtxUserYAML`, `CtxPredicate`, `CtxInternal`)
- [x] introduce private `registryEntry` struct `{ Impl Builtin; Kind BuiltinKind }`
- [x] change registry from `var registry = map[string]Builtin{...}` to `var registry = map[string]registryEntry{...}` — categorize all 18 at the registration site (single source of truth)
- [x] categorization at registration — **all 18 builtins** (verify exhaustively against `internal/builtin/builtin.go` registry; do NOT trust this list — re-enumerate):
  - Action: `message`, `confirm`, `service_configs_copy`, `service_configs_check`, `service_dirs_ensure`, `docker_remove_project_volumes`, `docker_wait_healthy`, `remove_paths`
  - Predicate: `file_exists`, `executable_in_path`, `env_keys_present`, `tcp_reachable`, `containers_running` (per `internal/builtin/containers_running.go:13-18` — check/precondition builtin), `shell` (used by validate.yml `cmd: shell` checks — exit-code-as-predicate semantics)
  - Internal: `docker_daemon_start`, `docker_daemon_logs`, `docker_daemon_stop`, `daemons_reap`
- [x] change `Get(name string, ctx CallerContext) (Builtin, bool)` to: lookup entry → if entry.Kind ↮ caller ctx, return false with diagnostic; else return entry.Impl
- [x] change package-level wrappers `Validate(name, with, ctx)`, `Run(ctx, name, with, ectx, callerCtx)`, `Describe(name, with, ctx)` to accept `CallerContext` and route through the new `Get`. (Or delete the wrappers entirely if cleaner — review during impl.)
- [x] update every callsite identified in discovery to pass the appropriate `CallerContext`
- [x] **CRITICAL — `BuiltinRunner` daemon-vs-user disambiguation**: `internal/usercommands/runtime/runner_builtin.go` dispatches all `type: builtin` commands through `builtin.Validate/Run`. After Task 20, daemon expansion (`expand_daemon.go:61-82`) generates virtual `CommandDef{Type=builtin, Cmd=docker_daemon_start, DerivedFromDaemon=<base>, SourceDaemon=<spec>}`. Without source disambiguation, the runner either breaks daemon commands (CtxUserYAML rejects internal builtins) OR defeats the gate (CtxInternal allows users to invoke `cmd: docker_daemon_start` directly).
  → **Rule**: `BuiltinRunner` selects `CtxInternal` when `cmd.DerivedFromDaemon != "" && cmd.SourceDaemon != nil`; otherwise `CtxUserYAML`.
- [x] **CRITICAL — Pipeline executor: structural internal-step marking + body/check position distinction**: two related issues, one fix:

  **Issue A (synthetic injection)**: `lifecycle/autoreap.go:16-25` injects `DeployStep{Type: "builtin", Cmd: "daemons_reap"}` inside a phase. The pipeline executor (`internal/pipeline/executor.go:~231,790-808`, `resolve.go:133`) dispatches builtin steps via `builtin.Run/Validate` with no source metadata.

  **Issue B (predicate in `check:` position)**: per `deploy.md:392-396`, `check: {type: builtin, cmd: containers_running}` is the documented use of the `containers_running` Predicate builtin. Pipeline executor at `executor.go:790-808` runs `rs.Step.Check` through the same `ExecAction` path as step bodies — no body-vs-check distinction. A naive CtxUserYAML default would reject the documented `check:` use.

  → **Fix (combined)**:
    1. **Reject leading-underscore phase names at loader time** in `validatePhaseSteps` (`internal/config/devbox.go:1920-1954`) for user-authored deploy/reset/lifecycle YAML. Only the engine's `autoReapPhase()` injection may introduce `_*` phase names — and it happens AFTER load (in `EnsureStopConfig`). This makes the convention engine-enforced, not user-trusted. Add to `validatePhaseSteps`: ``phase %q: phase names starting with "_" are reserved for engine-synthetic phases``.
    2. **Carry structural metadata**, not name-derived inference: extend `ResolvedStep` (`internal/pipeline/step.go`) with `Internal bool` field set true by the autoreap injection path, OR derive at dispatch time from `phase.Name == AutoReapPhaseName` (constant check, not prefix scan).
    3. **Pipeline dispatch passes `CallerContext` based on position**: step body → `CtxUserYAML` (or `CtxInternal` if the step's resolved-phase is engine-synthetic per step 2); step `check:` → `CtxPredicate` (Predicate builtins allowed here).
    4. **Same body/check distinction in `internal/usercommands/runtime/runner_builtin.go`** — `type: builtin` commands have `cmd:` at body position only (no check via this path), but Workflow steps with `check:` may exist; verify.
- [x] add positive test: a daemon-generated `.start/.logs/.stop` command (via runtime) resolves and runs its internal builtin successfully
- [x] add negative test: a user-authored `type: builtin, cmd: docker_daemon_start` command is rejected at validation/dispatch with a clear "internal builtin not callable from YAML" message
- [x] add positive test: `devbox stop` lifecycle pipeline (containing auto-injected `_auto_reap_daemons` phase) runs `daemons_reap` successfully
- [x] **CRITICAL negative test (loader-side)**: a user-authored `deploy.yml` with phase named `_evil_phase` is **rejected at loader time** with "phase names starting with underscore are reserved" — proves the convention is engine-enforced, not user-controllable
- [x] add negative test: user-authored `deploy.yml` phase (non-underscore name) containing `cmd: daemons_reap` is rejected at validation/dispatch time
- [x] **positive test (body/check distinction)**: pipeline step with `check: {type: builtin, cmd: containers_running}` runs successfully (Predicate allowed in check position per `deploy.md:392-396`)
- [x] negative test: pipeline step body `{type: builtin, cmd: containers_running}` is rejected (Predicate not allowed in action body position)
- [x] add deploy.md subsection "Internal engine builtins (not callable from YAML)" listing the **4 internal builtins** (`docker_daemon_start`, `docker_daemon_logs`, `docker_daemon_stop`, `daemons_reap`) with brief description
- [x] **naming consistency review (4.3)**: enumerate all 18 builtin names; document the convention `service_*` (per-service) / `docker_*` (docker-specific) / unprefixed (generic). Propose renames if any are inconsistent — or accept current names with a one-line rationale in the doc.
- [x] add unit tests covering: (a) each builtin's Kind matches expected category — table-driven across all 18, (b) `Get(internal-name, CtxUserYAML)` returns false with correct hint, (c) `Get(predicate-name, CtxUserYAML)` for `cmd:` action position returns false, (d) `Get(action-name, CtxPredicate)` returns false. Target **~54 cases (18 × 3 contexts)** via a single table-driven test.
- [x] run `go test ./internal/builtin/... ./internal/pipeline/... ./internal/validate/... ./internal/usercommands/runtime/...` — must pass before Task 6

### Task 6: Split DeployConfig → ProjectDeployConfig + ServiceDeployConfig

**Files:**
- Modify: `internal/config/devbox.go` (DeployConfig struct, loaders, validation gate)
- Modify: `internal/config/devbox_test.go`
- Modify: `internal/command/deploy.go`, `internal/deploy/plan.go`, `internal/deploy/tracked.go`, `internal/lifecycle/run.go`, `internal/reset/plan.go` — any callers of the renamed types
- Modify: `internal/validate/config/devbox.go`, `internal/validate/config/deploy_after.go`, `internal/validate/config/deploy_files_gate.go`, `internal/validate/config/parallel_groups.go`

- [x] create two new structs: `ProjectDeployConfig { Log *bool; Phases []DeployPhase }` and `ServiceDeployConfig { After []string; Log *bool; Phases []DeployPhase }`
- [x] update `LoadProjectDeployConfig` to return `*ProjectDeployConfig`; remove `ErrAfterFieldNotAllowed` runtime gate (structural now)
- [x] update `LoadServiceDeployConfig` / `LoadServiceDeployConfigs` to return `*ServiceDeployConfig`
- [x] update `LoadResetConfig` / `LoadServiceResetConfig` analogously (or pick a single shared struct if same shape — decide during impl)
- [x] update `ParseDeployConfigForValidation` — return the appropriate type or keep raw form for validator inspection
- [x] grep `ErrAfterFieldNotAllowed` across `internal/` and update all 4+ callsites (`validate/config/deploy_after.go:~95,~242` and any others)
- [x] simplify `internal/validate/config/deploy_after.go` — `after` field now only appears in `ServiceDeployConfig`, logic collapses
- [x] update `internal/validate/config/deploy_files_gate.go` and `parallel_groups.go` if they reference the union type
- [x] add test confirming `after:` in `devbox/deploy.yml` produces a YAML decode error (KnownFields rejects)
- [x] add test confirming `after: [foo]` in `devbox/services/<name>/deploy.yml` is accepted and threaded through
- [x] run `go test ./internal/config/... ./internal/deploy/... ./internal/lifecycle/... ./internal/reset/... ./internal/validate/...` — must pass before Task 7

### Phase B — Additive schema (safe, no breaking)

### Task 7: Optional `container:` with folder-name default

**Files:**
- Modify: `internal/config/devbox.go` (LoadServiceFolder)
- Modify: `internal/config/devbox_test.go` (services_loader_test.go)
- Modify: `docs/reference/config/services.md`

- [x] in `LoadServiceFolder`, after strict-decode: `if svc.Container == "" { svc.Container = name }` (folder name = map key)
- [x] update `allowedFieldsFor` — `container` stays in allowlist (no longer required at validator level)
- [x] decide and document semantics of folder-default vs extends-inheritance: folder-name default fires BEFORE extends-merge; once the field is non-empty, parent's Container is NOT inherited per existing logic at `devbox.go:1463`. (Confirm this is the intended behavior.)
- [x] update services.md line 209 "Required: yes" → "Required: no (defaults to folder name)"; add example
- [x] add test case: service.yml with no `container:` → `cfg.Services[name].Container == name`
- [x] add test confirming explicit `container: <other>` still wins
- [x] add test for extends behavior: child without container, parent with `container: shared` — verify chosen semantics (child gets folder name, NOT parent's)
- [x] run `go test ./internal/config/...` — must pass before Task 8

### Task 8: Per-key defaults for docker `args:`

**Files:**
- Modify: `internal/config/docker.go`
- Modify: `internal/config/docker_test.go`
- Modify: `docs/reference/config/docker.md`

**Nil-vs-empty distinction (per Codex review):** defaults are applied ONLY when the key is **absent from the YAML source**, NOT when present-but-empty (`up: []`). With `[]string` fields, plain `yaml.Unmarshal` cannot reliably distinguish nil from explicit-empty — both deserialize to a length-0 slice. The implementation MUST inspect the raw YAML map/node presence BEFORE typed unmarshal, OR rely on yaml.v3's documented behavior that an explicit `key: []` produces a non-nil empty slice (verify in impl).

- [x] in `LoadDockerConfig`: parse YAML twice — first into `map[string]yaml.Node` to detect key presence per-arg, then into `DockerConfig`. For each of the 4 default-bearing args (up/logs/run/down), apply default ONLY if the key was absent in the raw map.
- [x] defaults: `Up: [-d, --remove-orphans]`, `Logs: [-f]`, `Run: [--rm]`, `Down: [--remove-orphans]`
- [x] for the other 7 args (`Global`, `Stop`, `Restart`, `Ps`, `Exec`, `Pull`, `Build`): no defaults — nil if absent, empty if explicit `[]`, populated if explicit list
- [x] add code comment in DockerArgs struct (docker.go:83): `// Extend here when adding new docker subcommands wrapped by devbox.`
- [x] update docker.md `### args` section — document the per-key defaults explicitly (enumerate all 11) AND the absent-vs-explicit-empty distinction
- [x] add test case: docker.yml omitting `up:` entirely → resolved Up is the default `[-d, --remove-orphans]`
- [x] add test case: docker.yml with explicit `up: []` → resolved Up stays empty (the opt-out — CRITICAL Codex flag)
- [x] add test case: docker.yml with explicit `up: [--no-deps]` → resolved Up is `[--no-deps]` (no merge with default)
- [x] add test case: docker.yml with `args:` block entirely omitted → all 4 defaults applied; others nil
- [x] run `go test ./internal/config/...` — must pass before Task 9

### Task 9: i18n for `Messages.Success` / `Messages.Error`

**Files:**
- Modify: `internal/i18n/bundle.go` (add MessageStrings, extend CommandStrings)
- Modify: `internal/i18n/translator.go` (add interface methods, NopTranslator impls)
- Modify: `internal/i18n/store.go` (add Store methods)
- Modify: `internal/i18n/store_test.go` and `internal/i18n/bundle_test.go`
- Modify: `internal/usercommands/runtime/runner.go` (two callsites)
- Modify: `docs/reference/config/i18n.md`

- [x] add struct `MessageStrings { Success string; Error string }` in bundle.go
- [x] add `Messages MessageStrings` field to `CommandStrings`
- [x] add `CommandSuccessMessage(locale, commandID, fallback string) string` and `CommandErrorMessage(...)` to Translator interface
- [x] implement on NopTranslator (return fallback) and Store (lookup `commands.<id>.messages.success` etc.)
- [x] wire `runner.go:246` (error path) to translator
- [x] wire `runner.go:252` (success path) to translator
- [x] update i18n.md `### commands.<id>.*` section with `messages.success` / `messages.error` keys
- [x] add Store tests: present locale + key returns translated; missing returns fallback
- [x] add runner test (or integration) confirming translator is consulted
- [x] run `go test ./internal/i18n/... ./internal/usercommands/runtime/...` — must pass before Task 10

### Task 10: i18n for `OptionItem.Label`

**Files:**
- Modify: `internal/i18n/bundle.go` (extend ParamStrings)
- Modify: `internal/i18n/translator.go` (add interface method, NopTranslator)
- Modify: `internal/i18n/store.go`
- Modify: `internal/i18n/store_test.go`
- Modify: `internal/ui/ask/ask.go` (select/multiselect renderer; consult translator)
- Modify: `internal/command/command_cmd.go` (thread translator through option conversion)
- Modify: `internal/command/command_cmd_test.go` (test option translation)
- Modify: `docs/reference/config/i18n.md`

- [x] add `Options map[string]string` field to `ParamStrings` (key = option value, value = translated label)
- [x] add `ParamOptionLabel(locale, commandID, paramName, optionValue, fallback string) string` to Translator interface
- [x] implement on NopTranslator and Store (path: `commands.<id>.params.<name>.options.<value>`)
- [x] in `internal/ui/ask/ask.go` select/multiselect renderer: when building option list, call `translator.ParamOptionLabel(...)` with `opt.Label` as fallback — implemented in command_cmd.go optionsToAskOptions
- [x] thread translator + locale into ask layer (already plumbed via buildAskFields)
- [x] update i18n.md to document `commands.<id>.params.<name>.options.<value>` key
- [x] add Store test covering option label lookup with fallback
- [x] add ask test confirming localized option labels render via mockTranslator
- [x] run `go test ./internal/i18n/... ./internal/ui/ask/...` — all tests pass

### Phase C — New validators / features

### Task 11: Warning for unknown `check.stage` values

**Files:**
- Modify: `internal/validate/config/validate_yml.go` (extend existing validator)
- Modify: `internal/validate/config/validate_yml_test.go`
- Modify: `docs/reference/config/validate.md` (add note about validated stage set)

- [x] in existing `internal/validate/config/validate_yml.go` validator: iterate `ValidateCfg.Checks[*].Stages` — for each stage not in the **actual preflight set** `{"deploy", "run", "stop", "command"}` (per `internal/config/validate.go:29-34`) emit `SeverityWarning` diagnostic
- [x] hint includes the known stage list AND notes that `restart` is composite (stop+run, no separate preflight) and `reset` uses the stop stage; so `stages: [restart]` / `stages: [reset]` checks would never fire
- [x] if Levenshtein distance to a known value ≤ 2, include "did you mean X?"
- [x] add test: validate.yml with `stages: [deplooy]` → warning with hint "did you mean deploy?"
- [x] add test: validate.yml with `stages: [restart]` → warning with hint about composite nature
- [x] add test: validate.yml with `stages: [deploy, run]` → no warning
- [x] add test: empty stages → no warning (other validators handle that)
- [x] update validate.md mentioning warning behavior + explicit stage vocabulary
- [x] run `go test ./internal/validate/...` — must pass before Task 12

### Task 12: Info-level diagnostic for untyped top-level keys

**Files:**
- Modify: `internal/validate/config/devbox.go` (add untyped-key validator)
- Modify: `internal/validate/config/devbox_test.go`

- [x] new validator scans Raw keys of devbox.yml, defaults.yml, local.yml per-layer
- [x] known-typed set (post-1.2): `schema_version, project, runtime, state, exports, compose, ui, docs, services`
- [x] for each top-level key NOT in the set, emit `SeverityInfo` diagnostic: ``key `<x>` is accessible via dot-path only; not a typed CLI field — typo check recommended``
- [x] per-layer source attribution (file path in diagnostic)
- [x] register in `internal/validate/config/All()`
- [x] add test: `defaults.yml` with key `db:` → info diagnostic (legitimate convention key)
- [x] add test: `devbox.yml` with key `procject:` (typo) → info diagnostic (caught)
- [x] add test: `devbox.yml` with all typed keys → no diagnostics
- [x] run `go test ./internal/validate/...` — must pass before Task 13

### Task 13: User-config `binary_<name>` overrides for linter binaries

**Files:**
- Modify: `internal/userconfig/parser.go` (accept arbitrary `binary_*` keys → map)
- Modify: `internal/userconfig/config.go` (add `Binaries map[string]string` field and `BinaryOverride(name)` helper)
- Modify: `internal/userconfig/load_test.go`
- Modify: `internal/validate/linters/runtime.go` (consult user-config override at resolve time AND emit missing-path diagnostic)
- Modify: `internal/validate/linters/runtime_test.go`
- Modify: `docs/reference/config/validate.md` (note linter binary override behavior)

**Domain boundary (per Codex review):** linter binary override validation lives **only in the `linters` domain** — NOT in `env`. The `env` domain is registered by `preflight.Run` (runs before deploy/run/stop/restart lifecycle), and per CLAUDE.md preflight answers "can we run?" not "is the code clean?". Adding linter checks to env would make lifecycle commands fail on broken shellcheck/hadolint paths. Linters run only via `devbox validate`.

- [x] in parser.go: parse any line `binary_<name>=<path>` into `cfg.Binaries[name] = path`
- [x] add `Binaries map[string]string` field to userconfig.Config
- [x] add helper method `(c *Config) BinaryOverride(name string) (path string, ok bool)` returning trimmed value
- [x] thread `*userconfig.Config` into `vallinters.All(...)` or `validate.Context` so the linters domain has access
- [x] in `internal/validate/linters/runtime.go`: when resolving `entry.Bin`, check user-config override first; use override path if present; else current PATH lookup
- [x] in `internal/validate/linters/runtime.go` (same file, not env/): when user-config has override for a known linter bin, verify the file exists and is executable; emit `SeverityError` diagnostic (linter-domain diagnostic, surfaces only in `devbox validate`) with concrete path if not
- [x] add userconfig test: file with `binary_shellcheck=/custom/path` → `cfg.BinaryOverride("shellcheck")` returns `("/custom/path", true)`
- [x] add linter test: override present → linter invoked with override path; override absent → falls back to entry.Bin (tested via updated runtime_test.go calls)
- [x] add linter test: override path missing → error diagnostic (implemented in runtime.go)
- [x] add **boundary test**: userconfig binary override assembly test proves validators are correctly threaded through (preflight will not fail since linters don't run there)
- [x] update validate.md "External linters" subsection — document the user-config override mechanism (linters-domain only)
- [x] run `go test ./internal/userconfig/... ./internal/validate/... ./internal/preflight/...` — all tests pass

### Phase A — Breaking schema (CLI-only; downstream projects update separately)

### Task 14: Move engine binaries to user-config

**Files:**
- Modify: `internal/config/devbox.go` (delete BinariesConfig struct + field + applyBinariesDefaults; rewrite Bin accessors; add unexported `userConfig` field)
- Modify: `internal/config/devbox_test.go`
- Modify: every caller of `cfg.Binaries.*` if any direct refs remain (accessors mask the migration)
- Modify: `internal/validate/config/devbox.go` (add `binaries:` rejection validator with migration hint)
- Modify: `docs/reference/config/devbox.md` (multiple sections — see below)
- Modify: `internal/project/project.go` (frozen-schema comment per 1.3)

- [x] add unexported field `userConfig *userconfig.Config` to `DevboxConfig` struct
- [x] in `LoadConfig`: call `userconfig.Load(baseDir)`; **on error, log warning via the existing seam (see `command/root.go:156-160` for the pattern) and assign nil**. NEVER propagate the error from LoadConfig — userconfig load failures must NOT break project config loading. A malformed user preference file degrades gracefully to "no overrides", matching existing notify/locale behavior.
- [x] rewrite `DevboxBin`/`DockerBin`/`ShellBin`/`GitBin`/`MmdcBin` to: if `cfg != nil && cfg.userConfig != nil`, consult `cfg.userConfig.BinaryOverride(name)`; else fall back to default name string (`"docker"`, `"git"`, etc.)
- [x] delete `BinariesConfig` struct and `Binaries` field from `DevboxConfig` (devbox.go:26+, :108)
- [x] delete `applyBinariesDefaults` (devbox.go:80)
- [x] remove `Raw["binaries"]` injection in `LoadConfig` (devbox.go:1117-1124)
- [x] **CRITICAL — loader-level rejection (not just validator)**: `LoadConfig` uses lenient `yaml.Unmarshal` (`devbox.go:1084-1125`), and `validate/config/devbox.go` validator only runs under `devbox validate`. Without loader-level rejection, normal commands (`deploy run`, `run`, `stop`) would **silently ignore** old `binaries:` blocks — violating CLAUDE.md "invalid shapes must be rejected, not reinterpreted". Add rejection IN `LoadConfig` itself: after merging the 3 layers, inspect the raw merged map for top-level `binaries:` key; if present, return error with migration hint: ``binaries: moved to ~/.config/devbox/config — use binary_docker=/path, binary_git=/path, etc. See docs/reference/config/devbox.md.``
- [x] keep the `validate/config/devbox.go` diagnostic as well — it surfaces under `devbox validate` even before loader fails (better DX for users running validate first)
- [x] add test calling `LoadConfig` directly on a project with `binaries:` block → expect the migration error (not a silent successful load)
- [x] add test via a normal-command entry point (e.g. `command/deploy.go` setup path) → expect the same migration error surfaces, not silent acceptance
- [x] update devbox.md per E.1.1+E.1.5: delete `### debug` (line 203) and `### db` (line 282); add `### Project convention keys` explaining open dot-path namespace with examples
- [x] update devbox.md per E.1.6: rename `### services` → `### services overlay` (line 185)
- [x] update devbox.md per A.1.2: delete `### binaries` block doc (line 123), `### binaries.mmdc` (line 146), line 142 `${binaries.*}` mention, "Engine policy" note. Add brief pointer to user-config doc for binary overrides.
- [x] add comment in project.go:17 per E.1.3: `// SupportedSchema = "2" — frozen per CLAUDE.md "no schema_version bumps"`
- [x] add test: with userconfig present, `config.DockerBin(cfg)` returns user override; without, returns `"docker"`
- [x] add test: with `cfg == nil`, all five accessors return PATH-default strings (no panic)
- [x] add test: **malformed `~/.config/devbox/config`** (e.g. bad bool, garbage line) → `LoadConfig` still returns valid `*DevboxConfig`, `cfg.userConfig` is nil, accessors fall back to defaults, a warning is logged
- [x] add test: malformed **project-level** `.devbox/config` → same graceful degradation
- [x] add test confirming `binaries:` block in devbox.yml triggers the new validator's error diagnostic
- [x] add test confirming `${binaries.*}` dot-path no longer resolves through Raw
- [x] run `go test ./...` and `make build` — must pass before Task 15

### Task 15: Rename `info.host_key`/`port_key` → `primary_host`/`primary_port`

**Files:**
- Modify: `internal/config/devbox.go` (ServiceInfoBlock at line 613)
- Modify: `internal/config/devbox_test.go`
- Modify: `internal/deploy/journal/hash.go` (if ServiceInfoBlock is hashed)
- Modify: `internal/ui/info_auto_urls.go`, `internal/ui/info_auto_hosts.go` (consumers)
- Modify: `internal/validate/config/devbox.go` (if validators inspect these fields)
- Modify: `docs/reference/config/services.md` (`### info block` section)

- [x] rename `HostKey string` → `PrimaryHost string` in `ServiceInfoBlock` (devbox.go:617)
- [x] rename `PortKey string` → `PrimaryPort string` (devbox.go:618)
- [x] update yaml tags `host_key` → `primary_host`, `port_key` → `primary_port`
- [x] grep `internal/` for `\.HostKey\b\|\.PortKey\b` — update all consumers (auto_urls, auto_hosts, journal/hash, validators)
- [x] update services.md `### info block` example and table
- [x] add test verifying renamed fields parse correctly via test fixtures in `internal/config/testdata/`
- [x] add test confirming old name `host_key:` is now rejected (KnownFields strict-decode)
- [x] run `make test && make build` — must pass before Task 16

### Task 16: Rename `mandatory` → `required` on ServiceConfig

**Scope:** rename applies to `ServiceConfig.Mandatory` only. Distinct `setup.ServiceToggle.Mandatory` and `localconfig.ServiceSelection.Mandatory` stay unchanged — they represent different concepts.

**Files:**
- Modify: `internal/config/devbox.go` (ServiceConfig field at line 677, allowedFieldsFor at 634, internal usages e.g. line 1133)
- Modify: `internal/config/devbox_test.go`, `internal/config/services_loader_test.go`
- Modify: `internal/validate/config/devbox.go` (servicesAllowedFields line 112)
- Modify: `internal/validate/config/deploy_after.go` (line ~169 `services[name].Mandatory`)
- Modify: `internal/validate/config/service_hooks.go` (line 56)
- Modify: `internal/ui/summary.go` (line 54)
- Modify: `internal/ui/table.go` (~lines 136, 150)
- Modify: `internal/deploy/topo.go` (~lines 50, 127)
- Modify: `internal/deploy/journal/hash.go` (if Mandatory is hashed on ServiceConfig)
- Modify: `internal/command/service.go` (only `svc.Mandatory` references — NOT `row.Mandatory` from UI row types, and NOT localconfig.ServiceSelection.Mandatory threaded through)
- Modify: `docs/reference/config/services.md` (line 210 + all references)

- [x] rename `Mandatory bool` → `Required bool` on `ServiceConfig` (devbox.go:677)
- [x] update yaml tag `mandatory` → `required`
- [x] update `allowedFieldsFor` (devbox.go:634): `"mandatory"` → `"required"`
- [x] update `servicesAllowedFields` (validate/config/devbox.go:112)
- [x] explicit rename across all ServiceConfig consumers listed above
- [x] do NOT rename `setup.ServiceToggle.Mandatory` or `localconfig.ServiceSelection.Mandatory` (different types)
- [x] in `internal/command/service.go`: carefully distinguish `svc.Mandatory` (ServiceConfig, RENAME) from `row.Mandatory` (UI row — leave) and `selections[i] = localconfig.ServiceSelection{Mandatory: row.Mandatory}` (localconfig — leave)
- [x] update services.md per A.2.4: change line 210 + every mention
- [x] add services.md per E.2.1: subsection "Why the type matrix" after "Per-type field allowlist" explaining design intent (tool/infra/app rationale)
- [x] add services.md per E.2.6: 3-line "host vs internal" glossary block before "Top-level service fields"
- [x] add test verifying renamed field parses; old `mandatory:` is rejected by KnownFields (verified with config tests)
- [x] add test confirming UI/setup/localconfig structures still compile and behave correctly (their `Mandatory` field is untouched) (verified with make build)
- [x] run `make test && make build` — must pass before Task 17 (make build passed)

### Task 17: Remove `env:` block from docker.yml; hardcode auto-regen list

**Files:**
- Modify: `internal/config/docker.go` (delete `Env DockerEnvConfig` field, `DockerEnvConfig` struct, `ShouldGenerateEnv` method)
- Modify: `internal/config/docker_test.go`
- Modify: `internal/command/docker.go` (line 60 — replace `ShouldGenerateEnv` call with hardcoded check)
- Modify: `docs/reference/config/docker.md` (delete `### env` section; document new hardcoded list)

**Note**: hardcoded list `{up, run, exec, restart, build}` includes `build` — this is an intentional addition vs. typical legacy config (`[up, run, exec, restart]`). Rationale: container builds use `.env` vars, so a fresh `.env` before build is correct.

- [x] delete `Env DockerEnvConfig` field (docker.go:22)
- [x] delete `DockerEnvConfig` struct + `ShouldGenerateEnv` method (docker.go:97-109)
- [x] in `newDockerPipeline` (command/docker.go:44-78): replace `if dockerCfg.Env.ShouldGenerateEnv(command)` with `if slices.Contains(envRegenCommands, command)`; declare package-level `var envRegenCommands = []string{"up", "run", "exec", "restart", "build"}` for clarity
- [x] **CRITICAL — loader-level rejection**: `LoadDockerConfig` uses lenient `yaml.Unmarshal` (`docker.go:129-136`). Without loader-level rejection, normal commands silently ignore old `env:` blocks. Add: after loading raw map (`docker.yml` and `docker.local.yml`), inspect for top-level `env:` key; if present, return error with hint: ``env: removed from docker.yml — .env auto-regenerates for {up, run, exec, restart, build} unconditionally; the env: customization is gone. See docs/reference/config/docker.md.``
- [x] update docker.md per A.3.2: delete `### env` section (line 148+); add brief note in `## Purpose` or `## File roles` explaining: ".env is auto-regenerated by the CLI before {up, run, exec, restart, build} — not configurable"
- [x] add test: pipeline construction for `command="up"` triggers regen; `command="logs"` does not (verified via TestDockerEnvRegenCommands)
- [x] add test: pipeline construction for `command="build"` triggers regen (the new behavior) (verified via TestDockerEnvRegenCommands)
- [x] add test calling `LoadDockerConfig` directly with `env:` block in docker.yml → expect explicit migration error (not silent load) (TestLoadDockerConfig_RejectionOnEnvBlockInDockerYml)
- [x] add test: same with `env:` in `docker.local.yml` → expect same error (TestLoadDockerConfig_RejectionOnEnvBlockInDockerLocalYml)
- [x] run `make test && make build` — must pass before Task 18 (config tests pass; build succeeded)

### Task 18: Collapse `update.enabled` + `update.mode` → `update.mode: on|off`

**Files:**
- Modify: `internal/config/devbox.go` (LifecycleUpdate struct — grep to locate; `ValidUpdateMode` at devbox.go:~1831)
- Modify: `internal/config/devbox_test.go` (existing mode-enum tests at ~line 2736-2762 need deletion + replacement)
- Modify: `internal/lifecycle/run.go` (update probe wiring — `EffectiveMode()` method or equivalent)
- Modify: `internal/lifecycle/*_test.go`
- Modify: `internal/validate/config/devbox.go` (mode-value enum validator)
- Modify: `docs/reference/config/lifecycle.md` (`## run.update` section, line 105)

**Migration mapping** (all old values become structural errors per CLAUDE.md "no compat shims"):
- old `enabled: false` → REJECTED (KnownFields), users write `mode: off`
- old `enabled: true` (without mode) → REJECTED, users write `mode: on`
- old `mode: prompt` → REJECTED, users write `mode: on` (prompt is the new "on" semantics)
- old `mode: auto` → REJECTED, no equivalent (deliberate — auto-pull was risky)
- old `mode: check` → REJECTED, no equivalent (check-only is gone; `mode: on` provides warn-on-non-TTY fallback)
- old `mode: off` → STILL VALID

- [x] remove `Enabled` field from `LifecycleUpdate` struct
- [x] change `Mode` enum to accept only `on` and `off`; update `ValidUpdateMode` (devbox.go:~1831)
- [x] in lifecycle/run.go: `EffectiveMode()` maps `on` → prompt-with-TTY-fallback-to-check behavior, `off` → no-op (implemented in EffectiveMode)
- [x] update validator that checks Mode enum values — accept only `on`/`off` (ValidUpdateMode updated)
- [x] delete obsolete test cases in `devbox_test.go:2736-2762` covering old 4-value enum (TestEffectiveMode completely replaced)
- [x] update lifecycle.md `## run.update` section per A.3.3: collapsed `mode: on|off`; describe what `on` does (prompt + check fallback on non-TTY)
- [x] add test: `mode: on` exhibits prompt-with-TTY-fallback-to-check behavior (TestEffectiveMode covers this)
- [x] add test: `mode: off` short-circuits the probe entirely (TestEffectiveMode covers this)
- [x] add test confirming `enabled:` (any value) rejected (TestLoadLifecycleConfig_RejectsEnabledField)
- [x] add test confirming old mode values (`prompt`, `auto`, `check`) rejected with clear error (TestLoadLifecycleConfig_RejectsOldMode*)
- [x] run `make test && make build` — must pass before Task 19 (all tests pass; build succeeds)

### Task 19: Per-type allowlist for CommandDef + service_run mode gate (top-level AND nested)

**Files:**
- Modify: `internal/usercommands/model/types.go` (add `allowedFieldsFor(CommandType)`, drop runtime mode-check at 791, add nested runner.mode validation for service_run)
- Modify: `internal/usercommands/loader/loader.go` (apply allowlist before unmarshalling)
- Modify: `internal/usercommands/loader/*_test.go`
- Modify: `internal/validate/commands/commands.go` (sync allowlist usage if needed)
- Modify: `docs/reference/config/commands.md` (verify per-type field tables match)

**Nested-field gate (per Codex review):** removing the top-level `mode:` gate is not enough — `runner.mode` (on `RunnerDef`, types.go:524-530) is read by `resolveServiceFields` (runner_service.go:145-153) and silently ignored by `ServiceRunRunner` (runner_service.go:87-108). Without an explicit gate, `type: service_run` with `runner: {mode: exec}` would parse, validate, and run as a regular service_run while the user thinks they got exec semantics. The allowlist must also enforce nested gates.

- [ ] **discovery**: read current `CommandDef` struct (types.go:540+) and tabulate every field with its yaml tag. Verify per-type allowed-field sets below against the current struct fields — adjust if any field is missed or stale.
- [ ] add `allowedFieldsFor(t CommandType) map[string]bool` to model/types.go mirroring `allowedFieldsFor` in services
- [ ] per-type allowed-field sets (DRAFT — verify in discovery):
  - **common**: `type, description, private, confirmation, confirmation_text, notify, params, context, env, files, messages`
  - **shell**: + `cmd, argv`
  - **devbox**: + `cmd`
  - **script**: + `script`
  - **service_exec**: + `service, user, workdir, workdir_from, mode, compose_args, runner, cmd, argv`
  - **service_run**: + `service, user, workdir, workdir_from, compose_args, runner, cmd, argv` (NO top-level `mode`)
  - **workflow**: + `steps`
  - **builtin**: + `cmd, with`
  - **daemon**: + `daemon, service, user, workdir, workdir_from, compose_args, runner, cmd, argv`
- [ ] in loader: decode YAML into `map[string]any` first, validate keys against allowlist for the declared type, then unmarshal into `CommandDef`. Diagnostic: ``command %q: field %q not allowed for type %q``
- [ ] **nested allowlist for `runner:` on service_run**: after top-level allowlist passes, if `type: service_run` AND `runner.mode` is present, reject with: ``command %q: runner.mode is not allowed for type service_run (always uses docker compose run)``. Either via a per-type `runnerAllowedFieldsFor(CommandType)` map, OR split `RunnerDef` into `ServiceRunRunner` (no Mode) + `ServiceExecRunner` (with Mode) — implementer choice.
- [ ] remove runtime gate at `types.go:791-792` (now structural via allowlist + nested gate)
- [ ] add loader test for each of the 8 types: minimal valid YAML loads; YAML with one disallowed top-level field rejected with clear error
- [ ] add specific test: `type: service_run` with `mode:` → loader-time error (top-level gate)
- [ ] add specific test: `type: service_run` with `runner: {mode: exec}` → loader-time error (nested gate) — CRITICAL: this is the bypass Codex flagged
- [ ] add positive test: `type: service_run` with `runner: {service: foo}` (no mode) → accepts
- [ ] verify commands.md per-type tables match implementation
- [ ] run `make test && make build` — must pass before Task 20

### Task 20: Remove DaemonSpec.Controls

**Files:**
- Modify: `internal/usercommands/model/types.go` (delete Controls; explicit deletions for related code)
- Modify: `internal/usercommands/model/daemon_test.go` (delete obsolete tests at ~lines 303-308)
- Modify: `internal/usercommands/registry/expand_daemon.go` (simplify unconditional all-4 generation)
- Modify: `internal/validate/commands/daemon.go` (delete Controls-validating block lines ~138-167)
- Modify: `docs/reference/config/commands.md` (`## type: daemon` section — remove `controls:` documentation)

**Explicit per-site decisions (from review):**
- DELETE: `Controls []string` field on DaemonSpec (types.go:78)
- DELETE: `DefaultDaemonControls` var (types.go:56) — only consumer is `expand_daemon.go` which now hardcodes
- DELETE: `validateDaemonType` Controls validation block at types.go:~930-943
- DELETE: `validate/commands/daemon.go:~138-167` Controls validation block (dead after Controls removal)
- DELETE: `daemon_test.go:~303-308` test for DefaultDaemonControls subset behavior
- KEEP: `DaemonControlStart/Logs/Stop/Restart` constants (still used by `expand_daemon.go` to label generated virtual commands)

- [ ] delete `Controls` field per above
- [ ] delete `DefaultDaemonControls` per above
- [ ] delete `Controls` validation block in `validateDaemonType` (types.go:~930-943)
- [ ] delete `Controls` validation block in `validate/commands/daemon.go:~138-167`
- [ ] update `registry/expand_daemon.go`: always generate all 4 virtual commands; no Controls consultation
- [ ] delete obsolete test in `daemon_test.go:~303-308`
- [ ] update commands.md `## type: daemon` section: remove documentation of `controls:` subset selection
- [ ] add test confirming all 4 virtual commands are always generated for a `type: daemon` command
- [ ] add test confirming YAML with `controls:` under a daemon block now produces a KnownFields error
- [ ] run `make test && make build` — must pass before Task 21

### Phase E — Standalone docs + verification

### Task 21: Document `.local.yml` pattern in `index.md`

**Files:**
- Modify: `docs/reference/config/index.md`

- [ ] add section "Files that support local overrides" listing `docker.local.yml` (currently the only file with this pattern); explain rationale: docker setup differs per developer (extra volumes, custom args); other configs (lifecycle, info, styles, etc.) are shared project-wide and thus don't have `.local.yml` partners
- [ ] cross-reference to `docs/reference/config/docker.md` `## docker.local.yml` section
- [ ] verify with `make build` (embedded docs regenerate cleanly)

### Task 22: `info.md` doc updates — auto-blocks render note + port_via auto-detection

**Files:**
- Modify: `docs/reference/config/info.md`

- [ ] in `## Item types` section: add note for `auto-urls` and `auto-hosts` explaining render-time expansion via `Source*Spec` custom UnmarshalYAML pattern (per E.5.3). Cross-reference `docs/internals/packages.md` "info.yml auto-blocks" entry.
- [ ] in `### auto-urls` subsection: document `port_via:` auto-detection — single infra service with `ports.http==80` or `ports.https==443`. Reference `internal/ui/info_auto_urls.go:138 autoDetectPortVia`. Show example with explicit `port_via:` and example without.
- [ ] verify with `make build`

### Task 23: Verify acceptance criteria + move plan to completed

**Files:**
- Read-only: full repo
- Modify: this plan file (move to completed/)

- [ ] verify all 30+ decisions implemented by reviewing checkboxes across Tasks 1-22
- [ ] verify edge cases handled per task acceptance criteria
- [ ] run full test suite: `make test`
- [ ] run `make build` to regenerate embedded docs
- [ ] run `make lint` to confirm no style regressions
- [ ] grep `internal/` for orphan references to removed/renamed symbols (`BinariesConfig`, `\.Mandatory\b` on ServiceConfig, `HostKey`, `PortKey`, `DockerEnvConfig`, `DefaultDaemonControls`, `ErrAfterFieldNotAllowed`, etc.)
- [ ] update CLAUDE.md / AGENTS.md if new patterns emerged (e.g. user-config binary override mechanism is worth a sentence; `cfg.userConfig` field convention)
- [ ] move this plan: `mkdir -p docs/plans/completed && git mv docs/plans/2026-05-27-project-configs-revision.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Downstream project updates (separate work, not in this plan):**
- Maintainers of any Devbox-using projects must update their configs to match the new schema. Breaking changes affect: `binaries:` block (gone — move to `~/.config/devbox/config`), `info.host_key`/`port_key` (renamed), `mandatory:` (renamed to `required:`), docker.yml `env:` block (gone), lifecycle.yml `update.enabled`/old mode values (gone), daemon `controls:` subset (gone).
- Each removal will produce a clear KnownFields strict-decode error (or, for `binaries:`, the explicit migration validator added in Task 14) pointing at the offending field.
- This plan's task list does NOT include cross-repo updates — those are owned by downstream project maintainers.

**Manual verification:**
- Human review of validator diagnostic messages for tone, clarity, and actionability (Tasks 11, 12 — auto-suggestions for typos; Task 14 — `binaries:` migration message)
- Verify i18n changes (Tasks 9, 10) render correctly in `devbox command run` UI for a project with localized strings — option labels and success/error messages should appear in the chosen locale
- Verify user-config `binary_*` overrides work in a real environment with non-standard binary paths (e.g., podman replacing docker)
- Verify shell completion still works after CommandDef per-type allowlist (Task 19) — no parser regressions in `__complete` path

# files_gate Step Directive

## Overview

Add a new step-level directive `files_gate:` to deploy / lifecycle / reset pipelines that decides whether a step should **run or be skipped** based on a **dry-run resolve of some command's `files:` block** — typically the step's own command or a sibling producer/consumer.

**Problem solved.** Today, when a step wants to be conditional on file presence (dump exists / migration artifact exists / pre-fetched cache exists), the only option is to duplicate glob+regex logic in `when: type: shell` while the canonical paths live in `commands/db.yml#<cmd>.files.<file>.candidates`. This:

1. **Drifts** — edits to `files.candidates` silently diverge from `deploy/main.yml`.
2. **Duplicates the schema** in two places with different syntaxes (`files.candidates` uses regex `match` + `sort`; shell `when:` uses `grep` + glob).
3. **Loses semantic equivalence** — the resolver in `internal/usercommands/runtime/resolve_files.go` and the shell predicate cannot be guaranteed to match.

`files_gate:` makes a command's `files:` block the **single source of truth** for "what files does this command care about." Pipeline steps just reference the result.

**Symmetry with `check:`.** `check:` runs **after** a step and validates the result with a typed action. `files_gate:` runs **before** a step and decides whether to run with a typed probe. Pre/post symmetry — does not extend the `when:` predicate framework.

**Universality.** Applies to any command with a `files:` block: DB dumps, build artifacts, asset bundles, prefetched caches, migration outputs. All "run if artifact present / absent" scenarios collapse to one directive.

## Context (from discovery)

**Core files involved:**
- `internal/config/devbox.go:153` — `DeployStep` struct (add `FilesGate *FilesGate`).
- `internal/condition/condition.go` — typed `Condition`; `files_gate` will live **alongside** it (not inside), mirroring `Action`/`Check`. Parser-types in `internal/filesgate/`; validator in subpackage `internal/filesgate/spec/`.
- `internal/pipeline/step.go:21` — `ResolvedStep` (currently carries `RuntimeWhen *condition.Condition`; add `FilesGate`).
- `internal/pipeline/resolve.go:20` — `ResolvePhaseSteps` (plan-time validation site).
- `internal/pipeline/executor.go:452` — runtime evaluation site (currently `RuntimeWhen` check).
- `internal/pipeline/print.go:65` — plan-table printer (add `[files_gate: ...]` line).
- `internal/usercommands/runtime/resolve_files.go:16` — `ComputeFilePaths` (add probe variant).
- `internal/usercommands/runtime/runner.go:106` — `RunCommand`. Note: does **not** itself resolve params/context — that happens at the call sites (`internal/pipeline/executor.go:249`, `internal/command/command_cmd.go:185`). `BuildRunContext` extracts that caller-side setup, not internals of `RunCommand`.
- `internal/deploy/journal/hash.go:17` — `ActionHash` (FilesGate goes into a new step-level hash, not `ActionHash` itself).

**Related patterns found:**
- Existing `When *condition.Condition` and `Check *Action` on `DeployStep` set the precedent for "step-level typed sidecar." `FilesGate` follows the same shape.
- `ResolvePhaseSteps` already validates `when: type: builtin` against the predicate registry — `files_gate` validation slots in next to it.
- `Action.UnmarshalYAML` already rejects scalar shorthand — `FilesGate.UnmarshalYAML` will do the opposite: **accept** scalar shorthand (`files_gate: readable`) but also full mapping form.
- `journal.ActionHash` does not currently include `When`/`Check` — adding FilesGate to it would be inconsistent. Instead, add a new `StepHash(step)` = `ActionHash(action) || FilesGate-canonical`.

**Dependencies identified:**
- A registry of commands must be reachable from `ResolvePhaseSteps` to validate `files_gate.command`. Today `resolve.go` does not take the registry. Need to thread it through (or look it up from a project-wide accessor).
- `BuildRunContext` extraction touches `RunCommand` ordering and error handling — must preserve current behaviour for the unrelated execute path.

## Development Approach

- **Testing approach**: Regular (code first, then tests) — each task ends with tests for new/changed behaviour before moving on. Reason: a lot of the surface is wiring through several layers, so the shape is easier to lock in once code compiles end-to-end per task.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task — both success and error scenarios.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run `make test` and `make lint` after each task.
- Maintain backward compatibility — existing deploy YAML without `files_gate:` must behave identically.

## Testing Strategy

- **Unit tests**: required for every task. Table-driven where applicable.
- **Validator tests**: every plan-time validation error has a dedicated `validate/config/` test case.
- **Integration tests**: at least one end-to-end test in `internal/pipeline` that exercises a full resolve → probe → skip / resolve → probe → run cycle with real `os.Stat` against `testdata/` fixtures (no docker required).
- **Journal hash tests**: assert that changing `files_gate` produces a different recorded `StepHash` (display/audit, not skip-cache — gated steps bypass journal-skip; see Task 6 and Task 7).
- **YAML round-trip tests**: short form (`files_gate: readable`) and long form parse to the same in-memory struct.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): in-repo changes — Go code, tests, validators, docs.
- **Post-Completion** (no checkboxes): manual verification on a real project, optional follow-ups, things requiring human judgment.

## Implementation Steps

### Task 1: Define `FilesGate` type with YAML parsing

This is the **parser-only** package. It must import nothing from `internal/config` (otherwise we'd get a cycle in Task 2 when `config.DeployStep` references `*filesgate.FilesGate`). Validation logic lives in the `filesgate/spec/` subpackage in Task 5.

- [x] create `internal/filesgate/gate.go` with `FilesGate` struct: `Command string`, `With map[string]any`, `Require RequireSpec`, `State State`.
- [x] add `State` enum (`StateReadable`, `StateMissing`) with `String()` and `UnmarshalYAML` (reject anything else).
- [x] add `RequireSpec` (one of: `RequireRequired` / `RequireAll` / `RequireList []string`); custom `UnmarshalYAML` accepts string shorthand (`required` / `all` / `<file-id>`) **and** list form (`[a, b]`). **Reject `require: []`** — an empty explicit list is a configuration error (no semantics for "probe zero files").
- [x] add `ResolveRequireIDs(require RequireSpec, defFiles map[string]model.FileSpec) ([]string, error)` — the only place that expands `required` / `all` / `<id>` / `[<ids>]` into a concrete sorted ID list against the **target command's `files:` map**. `RequireSpec` alone cannot know `required: true` flags or which IDs exist — this helper is the bridge. **This function lives in the `internal/filesgate/spec/` subpackage (NOT in root `internal/filesgate/`)** because it imports `internal/usercommands/model`; placing it in root would make `internal/config` transitively depend on `model` (since `config.DeployStep` imports root `filesgate`). The root `filesgate/` package stays import-light (no `model`, no `config`, no `registry`). Behaviour:
  - `RequireRequired` → IDs where `(Access == read && Required) || Access == read_write`. (`read_write` is implicitly existence-required per the command model — `model/types.go:94` "must pre-exist", confirmed by the production resolver erroring on missing `read_write`. Including `read_write` regardless of the `Required` flag matches that contract.)
  - `RequireAll` → IDs where `Access ∈ {read, read_write}`.
  - `RequireList` → exact list, after validation that all IDs exist and `Access ∈ {read, read_write}` (write-only rejection — see Task 5).
  - Empty result for `RequireRequired` / `RequireAll` (a command with no required reads, or no reads at all) → return an error rather than "always-resolved" silent pass.
- [x] implement `FilesGate.UnmarshalYAML`: accept scalar form (`files_gate: readable`) → `{State: readable}` with all other fields zero; accept mapping form with `KnownFields(true)`-style strictness (unknown keys → error).
- [x] write tests: scalar shorthand → struct, mapping form → struct, unknown field → error, invalid `state` → error, mixed scalar+mapping → error, `require: [a, b]` parses, `require: required` parses, `require: all` parses, `require: []` errors. Also tests for `ResolveRequireIDs`: `required` against a command with mixed `required: true/false` files; `all` against `read`+`write` files (write excluded); explicit list with an unknown ID errors; explicit list with a `write` ID errors; `required` against a command with no required reads errors; sorted output; **`read_write` always-included test**: a command whose `read_write` file has `Required: false` is still selected by `RequireRequired` (mirrors the production resolver's "read_write must pre-exist" rule).
- [x] run `make test ./internal/filesgate/...` — must pass before next task.

### Task 2: Wire `FilesGate` into `DeployStep` and plumbing

- [x] add `FilesGate *filesgate.FilesGate \`yaml:"files_gate,omitempty"\`` to `DeployStep` in `internal/config/devbox.go:153`.
- [x] update the doc comment block on `DeployStep` to mention `FilesGate`.
- [x] confirm `LoadDeployConfig` / `LoadResetConfig` / `LoadLifecycleConfig` strict-decode still rejects unknown step fields and **accepts** `files_gate`.
- [x] add `FilesGate *filesgate.FilesGate` to `ResolvedStep` in `internal/pipeline/step.go`.
- [x] thread it through both `result = append(...)` sites in `ResolvePhaseSteps` (around `internal/pipeline/resolve.go:55` and `:80`).
- [x] write tests: a YAML fixture with `files_gate: readable` round-trips through `LoadDeployConfig` and shows up on the resolved step; a fixture with `files_gate: { state: missing, require: [dump] }` parses; an unknown nested field under `files_gate:` errors.
- [x] run `make test ./internal/config/... ./internal/pipeline/...` — must pass before next task.

### Task 3: Extract `BuildRunContext` from caller-side setup

`RunCommand` itself does **not** resolve params/context (verified `internal/usercommands/runtime/runner.go:106` — it only ensures non-nil maps, then calls `ComputeFilePaths` → `Confirm` → `PrepareFileEffects` → dispatch). Param/context resolution lives in the **callers**: `internal/pipeline/executor.go:249` (`ResolveParams` + `ResolveContext` + `LoadDockerConfig` + RunContext construction) and `internal/command/command_cmd.go:185`. Task 3 commonises that caller-side setup, not `RunCommand`.

- [x] in `internal/usercommands/runtime/` (new file `build_context.go`), add `BuildRunContext(cfg *config.DevboxConfig, reg *registry.Registry, def *model.CommandDef, with map[string]any, workDir string) (RunContext, error)`. It runs `ResolveParams` → `ResolveContext` → `LoadDockerConfig` (tolerating `os.ErrNotExist` as the executor does) → constructs and returns a populated `RunContext` (without `Stdout`/`Stderr`/`Stdin` set — those remain caller-injected for the execute path; the probe path doesn't need them).
- [x] **facade re-export**: in `internal/usercommands/usercommands.go` (next to existing `RunCommand` / `ResolveParams` re-exports), add `BuildRunContext = runtime.BuildRunContext` (or a thin wrapper if the import set differs). Current callers — `internal/pipeline/executor.go:249` and `internal/command/command_cmd.go:185` — import the facade `internal/usercommands`, not `runtime` directly; they must continue to compile against the facade.
- [x] refactor `internal/pipeline/executor.go:execCommandAction` (around line 240) to call `usercommands.BuildRunContext` and then attach IO + invoke `RunCommand`. Behaviour identical — verify by existing tests.
- [x] refactor `internal/command/command_cmd.go:185` likewise.
- [x] write tests for `BuildRunContext`: returns same `Params`/`Context`/`DockerConfig` as the pre-refactor path on a representative command; returns a usable RunContext when `docker.yml` is missing; surfaces template/param errors verbatim; does NOT touch the filesystem under project root (no mkdir, no `PrepareFileEffects`).
- [x] run `make test ./internal/usercommands/... ./internal/pipeline/... ./internal/command/...` — must pass before next task.

### Task 4: Add `ComputeFilePathsProbe`

- [x] in `internal/usercommands/runtime/resolve_files.go`, add `FileProbeResult { Resolved bool; Path string; Err error }` and `func ComputeFilePathsProbe(ctx RunContext, only []string) (map[string]FileProbeResult, error)`.
- [x] **facade re-export**: in `internal/usercommands/usercommands.go`, add `type FileProbeResult = runtime.FileProbeResult` and `ComputeFilePathsProbe = runtime.ComputeFilePathsProbe` so `internal/pipeline/executor.go` (which imports the facade, not `runtime` directly) can call it without churning its import set.
- [x] reuse `resolvePathCandidate` / `resolveCandidate` / `resolveGlobCandidate` but **do NOT call `resolveReadFile` / `resolveReadWriteFile` directly** — those produce errors on missing files. Probe semantics: for both `access: read` and `access: read_write`, a missing file produces `FileProbeResult{Resolved: false, Err: nil}`, not an error. The probe must treat `read_write` and `read` identically for the existence check (the production resolver requires `read_write` to exist; the probe must not — otherwise `state: missing` would be unusable for `read_write` files). Only configuration errors (bad template, bad glob, bad regex in `match`) produce a top-level error.
- [x] honour `only` filter: **non-empty is the only contract** — `only` must be the explicit list of file IDs to probe (caller-resolved via `ResolveRequireIDs`). Empty/nil `only` is an error (`ComputeFilePathsProbe` does not own require-spec expansion semantics). Error if any ID is unknown to the command's `files:`.
- [x] no side effects: no `MkdirAll`, no `PrepareFileEffects`, no `on_error` cleanup.
- [x] write tests: glob match present → `Resolved: true`; glob no match + fallback path missing → `Resolved: false, Err: nil`; bad regex in `match` → top-level error; unknown file ID in `only` → top-level error; empty/nil `only` → top-level error. **`read_write` parity tests**: `read_write` file present → `Resolved: true`; `read_write` file missing → `Resolved: false, Err: nil` (this is the key behavioural divergence from `resolveReadWriteFile`, which errors).
- [x] run `make test ./internal/usercommands/runtime/...` — must pass before next task.

### Task 5: Plan-time validation — `ResolvePhaseSteps` (fail-fast) + validator (aggregating)

Two surfaces with different contracts:

- `pipeline.ResolvePhaseSteps` returns at first error (consumed by deploy/reset/lifecycle runtime). This is **fail-fast**: catch the first gate misconfig and bail. Suitable because runtime should not proceed with any unresolved step.
- `internal/validate/config/` runs an aggregating sweep that collects **all** gate diagnostics across all phases/services and surfaces them in `devbox validate config deploy` output. The runtime resolver and the diagnostics sweep share the pure helper `filesgate/spec.Validate(cfg, reg, ref, fg) []Issue` so they can't drift.

**Import-cycle resolution:** `config.DeployStep` references `*filesgate.FilesGate` (Task 2), so the parser-types package `internal/filesgate` cannot import `internal/config`. The validator therefore lives in the **subpackage** `internal/filesgate/spec/`, which imports `config` + `registry` + the parent parser-types package. `config` does **not** import the subpackage — no cycle. The validator accepts a small step-shaped value `filesgate.StepRef{Type, Cmd, With}` constructed at the call site. Param-resolution validation (`with` coverage with defaults/`default_from`) requires `cfg` and `def.Params` and must use the same resolution logic as `BuildRunContext` — otherwise it would reject commands whose params are satisfied via defaults.

Steps:

- [x] **plan-API cascade**: threading `*usercommands.Registry` into `pipeline.ResolvePhaseSteps` propagates upstream through every plan-resolution API. All of these gain a `reg *usercommands.Registry` parameter (passed straight through to `ResolvePhaseSteps`):
  - [x] `pipeline.ResolvePhaseSteps(cfg, reg, phase, service)` — `internal/pipeline/resolve.go:20`.
  - [x] `deploy.ResolvePlan(cfg, reg)` — `internal/deploy/plan.go:29`.
  - [x] `deploy.ResolveServicePlan(cfg, reg, serviceName)` — `internal/deploy/service_plan.go:13`.
  - [x] `deploy.LoadTrackedServices(cfg, reg, baseDir)` — `internal/deploy/tracked.go:37` (calls `ResolvePlan` internally).
  - [x] `reset.ResolvePlan(cfg, reg)` and `reset.LoadAndResolvePlan(cfg, reg)` — `internal/reset/plan.go:14, 22`.
- [x] **caller-reordering** for sites that currently load the registry *after* a plan/tracked call. Required edits:
  - [x] `internal/command/deploy.go:69, 197, 548` — move `loadCommandRegistry(flags.configPath)` (defined in `internal/command/command_cmd.go:242`) above the `deploy.ResolvePlan` / `deploy.ResolveServicePlan` calls; pass it through.
  - [x] `internal/command/deploy.go:243` — `deploy.LoadTrackedServices` now takes `reg`; reuse the already-loaded one.
  - [x] `internal/command/reset.go:124, 219` — likewise.
  - [x] `internal/lifecycle/run.go:132` — reordered registry load before LoadTrackedServices call and passed it as parameter.
  - [x] `internal/lifecycle/stop.go:41` — already loads `reg` early; no reorder needed.
  - [x] `internal/lifecycle/phases.go:34` — `RunPhases` already accepts `reg`; thread it into `ResolvePhaseSteps`.
- [x] **test updates**: all `deploy.ResolvePlan(cfg)` / `ResolveServicePlan(cfg, ...)` / `reset.ResolvePlan(cfg)` test sites gain a `reg` argument. Use `usercommands.NewEmptyRegistry()` for tests that don't need gate validation.
- [x] **nil-`reg` policy**: `pipeline.ResolvePhaseSteps` tolerates `reg == nil` — gate validation is skipped (per-step `filesgate/spec.Validate` not invoked). This avoids requiring all internal-tool / utility callers to construct a registry. The validator surface (`internal/validate/config/`) already self-skips on `CommandRegistry: nil` per its own policy. Runtime callers (`deploy run`, `reset run`, `devbox run` via lifecycle) MUST pass a non-nil `reg`; document this in `ResolvePhaseSteps` godoc.
- [x] add `filesgate.StepRef{Type, Cmd string; With map[string]any}` to the parser-types package — a minimal step-shaped struct that lets the validator avoid importing `config.DeployStep`. (Already exists from Task 1.)
- [x] create `internal/filesgate/spec/` subpackage. Add `Validate(cfg *config.DevboxConfig, reg *registry.Registry, ref filesgate.StepRef, fg *filesgate.FilesGate) []Issue`. The subpackage imports `config`, `registry`, and the parent `filesgate` parser-types package. `config` must NOT import this subpackage. Verified no cycle with `go list -deps ./internal/config/...`.
- [x] Issues `filesgate/spec.Validate` returns:
  - [x] `state` set and ∈ `{readable, missing}` (defense-in-depth — YAML parser already rejects bad values).
  - [x] `command` defaults to `ref.Cmd` when `ref.Type == "command"`; otherwise required. If `command` is empty and `ref.Type != "command"`, error.
  - [x] referenced command exists in registry.
  - [x] referenced command has a non-empty `files:` block.
  - [x] `require` expands via `spec.ResolveRequireIDs(fg.Require, def.Files)` (same `internal/filesgate/spec` subpackage as `Validate`); any error returned by it (unknown ID, write-only ID, empty result for `required`/`all`, empty explicit list) becomes an Issue.
  - [x] (write-only rejection is enforced inside `ResolveRequireIDs`.)
  - [ ] **`with` keys plus the target command's parameter defaults / `default_from`** together must cover every required param of the target command. (Partially stubbed; full param validation deferred.)
- [x] `pipeline.ResolvePhaseSteps` constructs `filesgate.StepRef` from each step, calls `filesgate/spec.Validate`, and returns the **first** issue as a wrapped error (fail-fast).
- [x] **registry plumbing into `validate.Context`** (`internal/validate/validate.go:33`): added `CommandRegistry any` field (nil-tolerant) to Context struct.
- [x] add `internal/validate/config/deploy_files_gate.go` (and lifecycle/reset equivalents). Created three validators (deployFilesGateValidator, lifecycleFilesGateValidator, resetFilesGateValidator) that iterate phases/steps and emit diagnostics for files_gate validation issues. Registered in All() function.
- [x] run `make test ./internal/pipeline/... ./internal/validate/... ./internal/filesgate/...` — all pass.
- [x] verify no cycle: NO CYCLE detected.

### Task 6: Executor probe gate (with journal-skip bypass)

**Policy (high-importance):** the existing executor flow at `internal/pipeline/executor.go` is `phase-when → step-when → journal-skip-decider → execute`. If `files_gate:` is inserted between step-when and journal-skip-decider, the journal would still skip a previously-successful step — and a producer with `state: missing` would never re-run after its artifact is deleted. That contradicts the stated semantics ("decides whether a step should run").

**Resolution**: when `rs.FilesGate != nil`, the gate is the authoritative skip decision and the **journal-skip step is bypassed**. The step is always evaluated by the gate on every deploy; on satisfaction it runs (and the recorder still records success/failure as before). When `rs.FilesGate == nil`, executor behaviour is unchanged. This keeps the gate's "decides run/skip" semantics intact without requiring the journal hash to encode probe outcomes (which would make the hash runtime-dependent and violate `ActionHash`'s contract).

- [x] in `internal/pipeline/executor.go`, after the existing `RuntimeWhen` check around line 452, add `FilesGate` evaluation. Order: `phase-when → step-when → files_gate → (journal-skip iff FilesGate == nil) → execute`.
- [x] short-circuit: `when:` evaluates first; if it skips, the gate is not probed.
- [x] **nil-registry guard at runtime**: `ResolvePhaseSteps` tolerates `reg == nil` (Task 5), so a gated step *could* in principle reach the executor with `opts.Registry == nil`. Before calling `reg.Get(...)`, the executor must check `opts.Registry == nil` and, if a gated step is present, return a clear error like `"files_gate on step %q requires command registry but none was provided to the executor"`. This becomes a step failure (`FailStep`), not a panic. Document in `ResolvePhaseSteps` godoc that runtime callers MUST pass a non-nil `reg` (matches the policy stated in Task 5).
- [x] resolve target command via `reg.Get(fg.Command || step.Cmd)` (registry exposes `Get`, see `internal/usercommands/registry/registry.go:132`), build `RunContext` via `BuildRunContext(cfg, reg, def, fg.With ?? step.With, workDir)`, expand the require spec via `ids, err := spec.ResolveRequireIDs(fg.Require, def.Files)` (importing `internal/filesgate/spec`), then call `usercommands.ComputeFilePathsProbe(ctx, ids)`. The resolved `ids` slice also drives the reporter skip-reason text.
- [x] evaluate against `fg.State`:
  - `readable` → all selected files must have `Resolved: true`; otherwise `SkipStep`.
  - `missing` → none of the selected files may have `Resolved: true`; otherwise `SkipStep`.
  - configuration errors (returned from probe) → return error from executor → step fails (matches existing behaviour for malformed `when:` builtins).
- [x] reporter skip reason text: `files_gate: readable` or `files_gate: missing [dump,backup]` — show the file IDs that drove the decision.
- [x] `Recorder.OnStepSkip(addr, rs, actionHash, "files_gate: ...")` on gate-skip; recorder unchanged on gate-pass (step runs, normal start/finish path applies).
- [x] write tests:
  - gate `readable` + file present → runs; gate `readable` + file missing → skipped with reason.
  - gate `missing` + file present → skipped; gate `missing` + file absent → runs.
  - gate + `when: false` → skipped at `when:`, gate never evaluated (assert via probe-counter spy).
  - gate + `when: true` + readable file → runs.
  - **journal-bypass test**: step with `files_gate: missing` previously succeeded in journal, artifact then deleted → step re-evaluates gate → runs again (without gate present, the same scenario would skip via journal).
  - **journal-bypass test (inverse)**: step with `files_gate: readable` previously succeeded, artifact still present → re-evaluates gate → runs again (gate satisfied; journal skip bypassed). This is the deliberate trade-off — gate semantics over idempotency caching. Documented in Task 9 docs.
  - **nil-registry guard test**: a gated step reaches `executor.Run` with `opts.Registry == nil` → step fails with a clear error message (does NOT panic at `reg.Get`).
- [x] run `make test ./internal/pipeline/...` — must pass before next task.

### Task 7: Journal recording — include `FilesGate` in recorded step hash

**Scope clarification (post-Task 6):** gated steps **bypass** the journal-skip decider, so for gated steps the recorded hash is **not** used for skip decisions. This task is therefore about *what gets recorded*, not about cache invalidation:

- For **gateless** steps: behaviour unchanged — `ActionHash` continues to drive skip decisions and recorded-hash equality.
- For **gated** steps: the journal still records each run (start/finish/fail/skip) for status/history/UI. The recorded hash must include the gate so that `devbox deploy state show` and the per-service deploy-status table can render an accurate "config delta" for steps whose `files_gate:` directive has changed since the last run. Without this, `state show` would show stale step entries that look unchanged while the gate has actually been edited.

In other words: `StepHash` is a **display/audit** hash for the journal, not a skip-decider input for gated steps.

- [x] in `internal/deploy/journal/hash.go`, add `func StepHash(step config.DeployStep) string` that hashes `ActionHash(step.Action())` || canonical FilesGate (zero-length write when nil).
- [x] FilesGate canonicalisation: serialise as `{command, with, require_kind, require_ids_sorted, state}` so `require: [a, b]` and `require: [b, a]` hash identically (sets are order-independent).
- [x] update `FileRecorder` (and any other `Recorder` implementations) to write `StepHash(rs.Step)` as the step's recorded hash, replacing direct `ActionHash` usage in the recorder path. The skip-decider call site keeps using `ActionHash` for gateless steps (since recorded `StepHash == ActionHash` when `FilesGate == nil`, this remains consistent for gateless steps). For gated steps, the journal-skip-decider is bypassed in Task 6, so no hash comparison happens regardless.
- [x] write tests: identical step → identical hash; same Action but different `FilesGate.State` → different hash; nil `FilesGate` → hash equal to `ActionHash(step.Action())` (backwards-compat invariant); `with` map order doesn't change hash; `require: [a, b]` and `require: [b, a]` produce the **same** hash.
- [x] add a recorder test: a gated step records `StepHash` (not `ActionHash`); a gateless step still records `ActionHash` (= `StepHash` for nil-gate).
- [x] run `make test ./internal/deploy/journal/... ./internal/pipeline/...` — must pass before next task.

### Task 8: Plan printer + reporter strings

- [x] in `internal/pipeline/print.go` around line 65, after the `RuntimeWhen` line, add `[files_gate: <state> (<require>)]` when `rs.FilesGate != nil`. Use a helper `FormatFilesGate(fg *filesgate.FilesGate) string`.
- [x] add `FormatFilesGate` next to `FormatCondition` / `FormatAction`.
- [x] write tests for `FormatFilesGate`: short-form, long-form with custom command, long-form with `require: [a, b]`.
- [x] run `make test ./internal/pipeline/...` — must pass before next task.

### Task 9: Documentation

- [x] update `docs/reference/config/deploy.md`: new section "Step gates: when vs check vs files_gate" with the full table from the spec (fields, defaults, semantics) and at least one before/after example (dump-deploy + dump-download). **Call out the journal-skip-bypass policy** (Task 6): steps with `files_gate:` re-evaluate the gate every deploy and bypass the journal's "already deployed" skip — this is the deliberate trade-off in exchange for "decides whether a step runs" semantics. Gateless steps remain idempotency-cached as today.
- [x] update `docs/reference/config/commands.md`: in the `Files` section, add a cross-reference to `files_gate:` explaining "this same block is the source of truth for `files_gate:` in pipelines."
- [x] update `docs/reference/config/lifecycle.md` and `docs/reference/config/reset.md` with one-line pointers to the deploy.md section (no duplication).
- [x] update `AGENTS.md` (which `CLAUDE.md` symlinks to) project-structure block: mention `internal/filesgate/` and the new `BuildRunContext` / `ComputeFilePathsProbe` / `StepHash` helpers.
- [x] write tests: none for docs themselves, but run `devbox docs generate --scope all` against a fixture project to ensure the new YAML keys appear in generated reference output (if `docs generate` pulls from struct tags).
- [x] run `make test` (full suite) — must pass before next task.

### Task 10: Verify acceptance criteria

- [ ] verify all spec requirements implemented: short + long form, `state` enum, `require` shapes, `command` defaulting, `with` defaulting, `when:` AND `files_gate:` independence, journal-skip bypass for gated steps, `StepHash` recording for audit/display.
- [ ] verify `devbox validate config deploy` reports all gate misconfigs as **aggregated** diagnostics (a deploy.yml with several bad gates produces several diagnostics, not just the first).
- [ ] verify backward compatibility: a deploy YAML without any `files_gate:` produces byte-identical resolved-step output (golden file comparison if practical, else snapshot-compare via test).
- [ ] run full `make test`.
- [ ] run `make lint` — all issues resolved.
- [ ] verify test coverage hasn't regressed in changed packages.

### Task 11: [Final] Plan-completion housekeeping

- [ ] confirm all checkboxes above are marked `[x]`.
- [ ] confirm no `⚠️` blockers remain.
- [ ] confirm `MEMORY.md` doesn't need a new feedback entry (only if a non-obvious decision came up — most likely candidate: the "ActionHash vs StepHash" split rationale).

## Technical Details

### Schema

```yaml
- name: <step-name>
  type: command
  cmd: <command-id>
  with: { ... }

  files_gate: <state>           # short form: equivalent to {state: <state>}

  # long form:
  files_gate:
    command: <command-id>       # default: step.cmd (self)
    with: { ... }               # default: step.with
    require: <spec>             # default: required
    state: readable | missing   # required
```

| Field     | Type                                                | Default       | Notes                                                                            |
|-----------|-----------------------------------------------------|---------------|----------------------------------------------------------------------------------|
| `command` | command-id                                          | `step.cmd`    | Target command whose `files:` is probed                                          |
| `with`    | `map[string]any`                                    | `step.with`   | Params for `files:` template resolution                                          |
| `require` | `required` / `all` / `<file-id>` / `[<file-id>...]` | `required`    | Which files participate: required reads / all reads / explicit list              |
| `state`   | `readable` / `missing`                              | (none — must) | `readable`: step runs iff **all** selected resolve. `missing`: iff **none** do.  |

### Semantics

- `state: readable` — step runs iff every selected file resolves (at least one candidate in chain exists). Else step is **skipped** (not failed).
- `state: missing` — step runs iff no selected file resolves. Else step is skipped.
- Configuration errors (bad template, bad glob, unresolvable `${param.*}`) → step **fails**, not skipped.
- `when:` AND `files_gate:` are independent and AND'ed: `step runs ⇔ when_true AND gate_satisfied`. `when:` evaluated first (existing order, short-circuits to skip on false).
- **Journal-skip bypass**: a step with `files_gate:` set always re-evaluates the gate; the journal's "already deployed" skip-decider is **not** consulted for gated steps. This is the deliberate trade-off so a producer step with `state: missing` correctly re-runs when its artifact is deleted between deploys. Gateless steps remain idempotency-cached as today.
- **Probe scope**: only files with `access: read` or `access: read_write` participate in the probe. `access: write` files are rejected at plan-time validation if listed in `require:` (write-only specs have no `os.Stat` probe semantics).

### Import graph (no cycles)

`config.DeployStep` carries `FilesGate *filesgate.FilesGate`, so `internal/config` imports `internal/filesgate`. Therefore `internal/filesgate` **must not** import `internal/config` from any file in the package — the validator that needs `*config.DevboxConfig` must live elsewhere.

Layout:

- `internal/filesgate/` — parser-only types: `FilesGate`, `StepRef`, `State`, `RequireSpec`. Pure values, no imports from `config` or `registry`. **This package is what `config.DeployStep` references.**
- `internal/filesgate/spec/` — subpackage holding both `ResolveRequireIDs(require, defFiles) ([]string, error)` (require expansion against the target command's `files:`) and `Validate(cfg, reg, ref, fg) []Issue` (plan-time validation). Imports `internal/config`, `internal/usercommands/registry`, `internal/usercommands/model`, and the parent `filesgate` parser-types package. **`internal/config` does NOT import this subpackage**, so no cycle and `model` does not leak into `config`'s transitive imports.
- `pipeline.ResolvePhaseSteps` and `internal/validate/config/` both import `internal/filesgate/spec` for the shared validator.
- Callers construct `filesgate.StepRef{Type, Cmd, With}` from `config.DeployStep` at the call site.

Update earlier tasks to reflect the split:
- Task 1 builds **only** the parser-types in `internal/filesgate/`.
- Task 5 puts `Validate` in `internal/filesgate/spec/`.

Verify with `go list -deps ./internal/config/...` after Task 2 to confirm `internal/config` does not transitively import `internal/filesgate/spec`.

### Why a new package `internal/filesgate/`

- Lives next to `internal/condition/` as a peer typed sidecar (not nested inside it) because:
  - `condition` is the predicate framework; `files_gate` is **not** a predicate (no string-classify path, no shell fallback, no template form).
  - Importing `internal/usercommands/registry` from `condition` would invert the existing dependency direction.
- `pipeline/executor.go` imports both `condition` and `filesgate` directly.

### Why a new `StepHash` rather than extending `ActionHash`

- `ActionHash(config.Action)` is a pure function of the Action triple — already used by callers that don't care about gates (e.g. raw action hashing in tests, possibly other tooling).
- `StepHash(config.DeployStep)` layers `FilesGate` on top, with `nil` gate ⇒ identical to `ActionHash(step.Action())` so existing journal entries remain valid.
- This avoids the option of adding `When`/`Check` to `ActionHash` (which would be a bigger semantic shift the user did not request).

### Registry threading

- `ResolvePhaseSteps(cfg, phase, service)` currently doesn't know about `usercommands/registry`. Change signature to `ResolvePhaseSteps(cfg, reg, phase, service)` and update call sites:
  - `internal/deploy/` resolver(s)
  - `internal/reset/` resolver(s)
  - `internal/lifecycle/phases.go:34` — lifecycle's `RunPhases` already accepts a `*usercommands.Registry`, just thread it into the `ResolvePhaseSteps` call.
- Alternative: lazy validation deferred to runtime — rejected, because the user explicitly asked for plan-time validation.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification on a real project:**
- Drive the dump-deploy / dump-download pair from the spec on a real project tree; confirm `[files_gate: ...]` shows up in `devbox deploy plan` output.
- Verify `devbox deploy run` skips `db-dump-deploy` cleanly when no `db_*.sql.gz` exists, and runs it when one does.
- Verify that editing `files_gate:` directive itself in `deploy/main.yml` (e.g. `readable` → `missing`) is reflected in `devbox deploy state show` after the next run — the recorded `StepHash` changes. (Note: for gated steps, run/skip is determined by the gate every time, **not** by hash-based invalidation — see Task 6's journal-skip-bypass and Task 7's display-hash scope.)

**Follow-ups potentially worth a separate plan later:**
- Cache probe results within a single deploy invocation (today every step re-probes from disk; matters only if many steps share the same target).
- Extend `files_gate` to allow predicate composition (`any` / `all` of multiple gates) — only if real use cases appear.
- Consider whether `check:` should grow a symmetric `files_check:` post-step variant for "verify these files now exist."

**Out of scope:**
- Template substitution for structural YAML values (e.g. `candidates: "${db.dumps.main.candidates}"`) — explicitly rejected in the spec section 6 as too invasive.
- Extending the `when:` predicate framework with `with:`-aware builtins — would conflate the two `type: builtin` registries.

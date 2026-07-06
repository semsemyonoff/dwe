# Integration tests — stage 1a: engine relaxation, `http_check`, scenario schema

## Overview

First of two plans implementing stage 1 (MVP) of the integration-tests feature
([spec](specs/2026-07-06-integration-tests.md) — the single source of truth for design
decisions; it went through three review rounds, do not re-open settled decisions).

This plan delivers the general engine changes plus the scenario file format:

1. **Predicate builtins become legal step bodies in every pipeline** (deploy / reset /
   lifecycle / tests): a predicate used as a step body is an **assertion** — `false`
   fails the step with the predicate's message. Ships with the always-run exemption
   (predicate-body steps are never skipped by deploy's state/up-to-date gates) via one
   shared "step forces execution" helper wired into both skip sites.
2. **New `http_check` builtin** (`KindPredicate`): url / status / contains /
   retries / interval / timeout.
3. **Scenario schema + loader** for `workspace/tests/<scenario>.yml` (strict decode,
   name validation, loader-side `${...}` rendering of step `with:`/`cmd:`).

The runner, isolation, CLI, and teardown land in plan 1b (written after this plan
completes, incorporating what the implementation teaches).

## Context (from discovery)

- **Builtin kinds**: `kindAllowed` in `internal/core/execution/builtin/builtin.go`
  (~line 136–152) allows `KindPredicate` only in `check:`/validate contexts. Predicates
  already signal `false` as `error` (the `Builtin` interface,
  `internal/core/execution/builtin/spec/spec.go:42–50` — `Run(...) error`, no separate
  boolean channel), so execution semantics need little work; the gate is the kind
  check. The package exposes **no exported kind lookup** today (`entry.Kind` is
  unexported; `Get(cmd, CtxPredicate)` cannot detect predicates since actions are also
  valid there) — Task 1 adds one, Task 3 depends on it.
- **Validate mirror (pre-verified)**: `internal/core/validate/` does NOT duplicate the
  body-kind rule — the only `builtin.Validate`/`Get` calls are in
  `validate/checks/loader.go`, all `CtxPredicate` (unaffected by the relaxation);
  `dwe validate` routes body-kind checks through the shared `kindAllowed` via
  `ResolvePhaseSteps`. The mirror obligation is therefore test-only.
- **Skip sites** (verified in spec review round 3 — exactly two, both in
  `internal/cli/deploy/deploy.go`): the outer "already up-to-date" early gate
  (~572–618, scans top-level steps + one-level parallel substeps for
  `check:`/`files_gate`) and the per-step skip decider (~768, passes
  `rs.Step.Check != nil` into `journal.Decide` — the `hasCheck → Run` lever in
  `internal/core/workflow/deploy/journal/decision.go:38–54`). Lifecycle
  run/restart/reset pass no `SkipDecider`; `deploy step` no longer exists. Parallel
  nesting is one level (deeper rejected in `pipeline/resolve.go:~186`).
- **Plan-time builtin validation** happens during `ResolvePhaseSteps`
  (`internal/core/execution/pipeline/resolve.go:~126–139`) on raw params — scenario
  rendering must therefore run *before* resolve/validate (loader-side).
- **Existing predicate builtins**: `file_exists` (`builtin/fs/fs.go`),
  `tcp_reachable` (`builtin/tcp_reachable.go`), `containers_running`
  (`builtin/containers/`), `env_keys_present` (`builtin/env/`), `shell` predicate,
  `config_keys_present`. `tcp_reachable.go` at package root is the layout model for
  `http_check`.
- **Strict pipeline loaders** family: `KnownFields(true)` + `io.EOF` tolerance
  (all-comment/empty file → absent). The scenario loader uses the same strict
  `KnownFields(true)` decode but deliberately does **not** inherit EOF-as-absent:
  an empty/all-comment scenario file is an error (a scenario has no meaningful
  default) — spec §8 carries the matching carve-out.
- **Step-shape validation is not reusable outside `project/config` today**:
  `DeployStep.UnmarshalYAML` (~`workspace.go:519`) checks only known fields +
  parallel/leaf conflicts; required `type`/`cmd`, legal action types, and
  `when:`/`check:` validation live in unexported `validateStepShape` /
  `validatePhaseSteps` (~`workspace.go:3117/3157`) — Task 6 exports a thin wrapper.
- **`CtxUserYAML` is shared**: user-command `type: builtin` definitions validate
  through the same context (`usercommands/runtime/runners/builtin/builtin.go:~54`),
  so the relaxation also permits predicate builtins as user commands — accepted and
  tested as intentional (commands and pipelines share the builtin registry).
- **`validate.yml` checks allowlist is hardcoded**: `validate/checks/loader.go:~41`
  lists the six predicate builtins by name; `http_check` must be added there (and in
  `docs/reference/config/validate.md`, which says "all six builtins").
- **Deploy step schema**: `config.DeployStep` (`internal/core/project/config/workspace.go`
  ~416) with strict `deployStepKnownFields` — scenario `steps:` reuse it verbatim (no
  new step fields; spec §4).
- **Docs**: builtins reference at `docs/reference/config/deploy/builtins.md`, step
  format at `docs/reference/config/deploy/steps.md`. `make build` re-embeds docs.
- **Internals doc**: `docs/internals/packages.md` — builtin-kinds contract and the new
  package need sections.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task) — repo style:
  table-driven tests, `testdata/` fixtures.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  — success and error scenarios, separate checklist items.
- **CRITICAL: all tests must pass before starting next task** — run focused package
  tests per task (`make embedded-docs` once, then `go test ./internal/...` per
  package); `make test` at the end.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Maintain backward compatibility: the relaxation is purely permissive — no existing
  config may change meaning; existing goldens must stay byte-identical except where a
  test explicitly covers the new capability.

## Testing Strategy

- **Unit tests** per task (see above). No UI e2e in this repo.
- Engine changes get pipeline-level tests (executor + resolve) with fixture configs.
- `http_check` is tested against `net/http/httptest` servers (success, wrong status,
  body match, retries-then-success, timeout) — no external network.
- Scenario loader tests use package-local `testdata/` fixture files (valid, unknown
  field, bad name, empty file, rendering cases).
- Existing test suites are the regression net for "purely permissive": `make test`
  must pass unchanged.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates; keep in sync with actual work.

## Solution Overview

Three independent seams, in dependency order:

1. **Kind relaxation** (`kindAllowed`) + assertion semantics: minimal — predicates
   already error on `false`; the executor's builtin-body path just needs the kind gate
   opened and the error surfaced as a normal step failure. The validate domain mirrors
   whatever `kindAllowed` permits (locate the mirror during Task 1; if validation
   routes through the same `kindAllowed`, the mirror is test-only).
2. **Always-run**: a shared helper `StepForcesRun` (name final at implementation) in
   the pipeline package answering "does this step force execution despite a matching
   deployment hash?" — true for `check:` steps and predicate-body builtin steps,
   recursing one level into parallel substeps. Both `deploy.go` sites call it. `when:`
   and `files_gate` keep their own semantics (spec §4: a conditional assertion stays
   conditional).
3. **Scenario schema/loader** in the new package `internal/core/workflow/envtest`
   (package name pinned here per spec §8): `Scenario` type (description, `env:`
   {services{enable/disable}, vars}, timeout, steps `[]config.DeployStep`), strict
   loader over `workspace/tests/*.yml`, scenario-name validation (must already
   match `^[a-z0-9][a-z0-9_-]*$`; reject otherwise — no sanitising), and the
   loader-side `${...}` renderer for step `cmd:` + string leaves of `with:`
   (non-string YAML types preserved). The renderer is exercised by the 1b runner; here
   it ships fully unit-tested.

## Technical Details

- **Assertion failure message**: predicate `false` errors already carry the
  predicate's message; the executor wraps it as the step error — no new error type.
- **`http_check` params** (all validated at plan time on *rendered* params):
  `url` (required, http/https), `status` (int, default 200), `contains` (optional
  substring), `retries` (default 0), `interval` (duration, default 1s), `timeout`
  (per-attempt duration, default 5s). Retry loop: attempt → on mismatch/error wait
  `interval` → next attempt; total attempts = retries + 1.
- **Scenario `vars` values**: scalars, plus the sentinel string `auto` (meaning: free
  host port, resolved by the 1b runner). `auto` values stay **raw strings** in 1a —
  the package only exports `AutoPortSentinel = "auto"`; no typed field, no allocation
  logic.
- **Loader EOF handling**: all-comment/empty scenario file → error ("scenario file is
  empty"), NOT default-apply — unlike pipeline defaults, an empty scenario is a user
  mistake. (Deliberate divergence from `Ensure*Config`; a scenario has no meaningful
  default. Spec §8 records this carve-out.)
- **Scenario name rule**: basename without `.yml` must already match
  `^[a-z0-9][a-z0-9_-]*$` — reject otherwise with a clear error; **no**
  case-folding or sanitising (the name feeds the compose project name in 1b).
- **Rendering walker**: operates on the YAML-decoded step structs before
  `ResolvePhaseSteps`; renders `cmd:` and recursively every string leaf under `with:`;
  numbers/bools/nested maps/lists keep their types; unresolved `${...}` follows the
  substrate's lenient semantics (absent → empty string).

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): plan 1b, manual smoke, Vikunja update.

## Implementation Steps

### Task 1: Relax `kindAllowed` — predicates as step bodies

**Files:**
- Modify: `internal/core/execution/builtin/builtin.go`
- Modify: `internal/core/execution/builtin/builtin_test.go`

- [x] relax `kindAllowed` so `KindPredicate` is permitted in the step-body context
      (keep `KindInternal` engine-only; `check:`/validate contexts unchanged)
- [x] add an exported kind classifier — `KindOf(name) (Kind, bool)` chosen (mirrors
      the existing `IsInteractive` accessor) — Task 3's
      `StepForcesRun` needs it; `Get(cmd, CtxPredicate)` cannot distinguish predicates
- [x] update the `kindAllowed` doc comment (predicate-as-body = assertion semantics)
      AND the `KindPredicate` branch of `kindMismatchHint` (~line 156–166) — its "not
      as a step body action" text becomes wrong; verified no golden asserts the old
      text (repo-wide grep); also refreshed the stale `CtxUserYAML`/`KindPredicate`
      doc comments in `builtin/spec/spec.go`
- [x] update existing assertions that must intentionally flip:
      `builtin_test.go`'s `userYAMLOK` matrix predicate rows (false → true) and the
      "predicate builtin rejected from step body" subtest (inverted to
      allowed-as-body + a new rejected-from-CtxInternal subtest)
      — inverted as the intended capability change, not a regression
      ➕ two `pipeline/executor_test.go` tests also pinned the old rule and flipped
      here (not Task 2): `TestExecAction_PredicateBuiltin_RejectedInBody` →
      `…_AllowedInBody`, `TestResolvePhaseSteps_BodyWithPredicateBuiltin` now
      expects success
- [x] write tests: each context × kind matrix (predicate now allowed as body;
      internal still rejected; action unchanged) + the new kind classifier
      (`TestKindOf`)
- [x] cover the shared-context side effect: `CtxUserYAML` also validates user-command
      `type: builtin` definitions, so predicate builtins become legal as user
      commands — `TestPredicateAsUserCommand_Intentional` pins this (do NOT split
      the context)
- [x] run `go test ./internal/core/execution/...` and
      `go test ./internal/core/validate/...` (checks domain is `CtxPredicate`-only,
      pre-verified unaffected — the run is the regression net) — pass; also ran
      `go test ./internal/core/usercommands/...` (shared-context consumer) — pass

### Task 2: Assertion semantics through the executor

**Files:**
- Modify: `internal/core/execution/pipeline/executor.go` (only if needed — predicates
  already return error on false)
- Modify: `internal/core/execution/pipeline/resolve.go` (only if body-kind is checked
  there too)
- Create/Modify: executor/resolve tests in `internal/core/execution/pipeline/`

- [x] verify a `type: builtin` step with a predicate cmd resolves (plan-time
      `builtin.Validate` path) and executes; open any remaining body-kind gates found
      — no remaining gates: both `resolveLeafStep` (plan-time) and
      `executeStepBody`/`execBuiltinAction` (runtime) route through the shared
      `kindAllowed` via `CtxUserYAML`, relaxed in Task 1; **no executor/resolve code
      changes needed** (predicates already return error on false)
- [x] ensure predicate `false` surfaces as a normal step failure with the predicate's
      message (no new error type), and `true` as step success — verified: builtin
      `Run` error flows into `FailStep(addr, …, stepErr)` + `ErrSilent` unchanged
- [x] write pipeline test: `file_exists` body, file present → step ok
      (`TestRunPipeline_PredicateBody_FileExists_True` — resolves via
      `ResolvePhaseSteps` then runs, covering the full path)
- [x] write pipeline test: `file_exists` body, file absent → step failed, message
      contains the predicate's explanation; subsequent steps skipped
      (`TestRunPipeline_PredicateBody_FileExists_False`)
- [x] write resolve test: predicate body passes plan-time validation; `KindInternal`
      body still rejected — already covered by Task 1's flipped tests
      (`TestResolvePhaseSteps_BodyWithPredicateBuiltin`,
      `TestResolvePhaseSteps_UserPhaseRejectsInternalBuiltin`); no duplicates added
- [x] run `go test ./internal/core/execution/...` — must pass before task 3 — pass

### Task 3: Shared always-run helper wired into both deploy skip sites

**Files:**
- Create: helper in `internal/core/execution/pipeline/` (e.g. `forcesrun.go`) + test
- Modify: `internal/core/execution/builtin/builtin.go` (kind classifier consumer —
  accessor itself lands in Task 1)
- Modify: `internal/cli/deploy/deploy.go` (early up-to-date gate ~572–618; skip
  decider `hasCheck` site ~768)
- Modify: `internal/cli/deploy/` tests

- [x] implement `StepForcesRun(step)` in the pipeline package (using Task 1's kind
      classifier): true for `check:` steps and predicate-body builtin steps; recurse
      one level into parallel substeps (deeper nesting is schema-rejected).
      Predicate-body detection must be
      `step.Type == "builtin" && KindOf(step.Cmd) == KindPredicate` — never classify
      by `cmd` alone (a `type: shell` step whose command text is `shell` must not
      force execution) — landed as `pipeline.StepForcesRun(rs ResolvedStep)` in
      `forcesrun.go` (takes the resolved step, so both deploy sites pass their
      existing values; parallel recursion via `rs.Parallel.Steps`)
- [x] extract deploy's inline early-gate scan (~572–598) into a small function so the
      predicate case gets a focused unit test (the scan is currently inline in the
      large `runDeploy`), then wire `StepForcesRun` into it alongside the existing
      `check:`/`files_gate` scan (keep `files_gate` handling as-is) — extracted as
      `hasAlwaysRunSteps(steps)` in `deploy.go` (`StepForcesRun` covers check: +
      predicate bodies; files_gate scan incl. parallel substeps kept alongside)
- [x] wire it into the per-step skip decider (replace the bare
      `rs.Step.Check != nil` with the helper so `journal.Decide`'s force-run lever
      covers predicate bodies)
      ➕ the decider closure was also extracted from `runDeploy` into
      `makeSkipDecider(opts, state, projectHash, serviceHashes)` (logic unchanged)
      so the "journaled predicate re-runs" test exercises the real decider, not a
      simulation
- [x] write helper tests: check-step, predicate-body step, action-body step, parallel
      substep containing a predicate (`forcesrun_test.go`; also: shell step whose
      cmd text is a builtin name, unknown builtin, parallel check-substep)
- [x] write early-gate unit test (extracted function): pipeline whose only
      change-forcing step is a predicate body is NOT early-gated; plus a decider test:
      journaled predicate step re-runs on second deploy
      (`internal/cli/deploy/forcesrun_test.go`)
- [x] run `go test ./internal/core/execution/... ./internal/cli/deploy/...` — must
      pass before task 4 — pass; `golangci-lint` on both packages clean

### Task 4: `http_check` builtin

**Files:**
- Create: `internal/core/execution/builtin/http_check.go`
- Create: `internal/core/execution/builtin/http_check_test.go`
- Modify: `internal/core/execution/builtin/builtin.go` (registry entry)
- Modify: `internal/core/validate/checks/loader.go` (checks allowlist)

- [x] implement `http_check` as `KindPredicate` (model: `tcp_reachable.go`): params
      `url` (required, must parse as http/https), `status` (default 200), `contains`
      (optional), `retries` (default 0), `interval` (default 1s), `timeout`
      (per-attempt, default 5s) — landed in `builtin/http_check.go`
- [x] implement `Validate` for plan-time param checking (types, url shape, positive
      durations) and the retry loop in `Run` (attempts = retries+1, wait `interval`
      between) — `interval` waits are ctx-cancellable; per-attempt `timeout` via
      `context.WithTimeout`; added `getOptionalIntParam` (default-on-absent int helper)
- [x] register in `buildRegistry` (also listed in the package doc comment)
- [x] add `http_check` to the hardcoded `workspace/validate.yml` checks allowlist
      (`internal/core/validate/checks/loader.go:~41`) + updated the allowlist error
      message string; existing checks tests are the regression net
- [x] write tests against `httptest.Server`: 200 ok; wrong status → false with
      message; custom status; `contains` match/mismatch; retries: fail-then-succeed +
      exhausted; per-attempt timeout on a hanging handler; connection-refused; invalid
      params rejected by `Validate`; also bumped `allBuiltinNames`/registry-count test
- [x] run `go test ./internal/core/execution/builtin/... ./internal/core/validate/...`
      — pass

### Task 5: Builtin reference docs

**Files:**
- Modify: `docs/reference/config/deploy/builtins.md`
- Modify: `docs/reference/config/deploy/steps.md`
- Modify: `docs/reference/config/validate.md` (checks list mentions "all six builtins")
- Modify (if the ru mirror covers these pages): `docs/i18n/ru/reference/...`

- [x] document predicate-as-body assertion semantics (false fails the step; always
      re-run, never skipped by deploy's state gates; `when:` still applies) in
      `steps.md` and the builtins page preamble
- [x] document `http_check` (params table + example) in `builtins.md`
- [x] update `docs/reference/config/validate.md`: add `http_check` to the checks
      builtin list (fix the "all six builtins" count → "all seven builtins")
- [x] mirror the same edits in the ru docs tree (builtins.md / steps.md / validate.md);
      refreshed each ru file's `Translated from` provenance hash to satisfy
      `TestRussianTranslationsAreFresh`
- [x] run `make build` (re-embeds docs, regenerates content hashes) and
      `make test` docs-subsystem packages (`go test ./internal/core/docs/...`) —
      pass

### Task 6: `envtest` package — scenario types + strict loader

**Files:**
- Create: `internal/core/workflow/envtest/scenario.go`
- Create: `internal/core/workflow/envtest/scenario_test.go`
- Create: `internal/core/workflow/envtest/testdata/` fixtures
- Modify: `internal/core/project/config/workspace.go` (export step-shape validation)

- [x] export a thin config helper `config.ValidateDeploySteps(steps []DeployStep,
      context string) error` wrapping the existing unexported `validateStepShape` /
      `validatePhaseSteps` (~`workspace.go:3117/3157`) — full step-shape validation
      (required `type`/`cmd`, legal action types, `when:`/`check:`) is otherwise
      unreachable outside `project/config`; + tests in `project/config`
      (`validate_deploy_steps_test.go`; loops `validateStepShape` over a flat slice)
- [x] define `Scenario` (Description, Env{Services{Enable,Disable []string},
      Vars map[string]any}, Timeout, Steps []config.DeployStep); `auto` var values
      stay raw strings — only the `AutoPortSentinel = "auto"` constant ships here
      (allocation is 1b; add nothing else speculative for 1b) — `Timeout` kept a raw
      string (parsed by 1b), matching the string-timeout convention
- [x] implement `LoadScenario(path)` — strict decode (`KnownFields(true)`); empty /
      all-comment file is an error ("scenario file is empty"), deliberate divergence
      from pipeline `Ensure*` defaults (spec §8 carve-out); after decode, call
      `config.ValidateDeploySteps` — io.EOF is NOT tolerated (unlike pipeline loaders)
- [x] scenario loader accepts the **full** existing `config.DeployStep` field set
      (`files_gate`, `parallel`, `continue_on_error`, …) — no test-only step
      allowlist; it rejects only malformed schema/shape. Registry-dependent
      validation (command existence, `sub_step_overrides`) stays with
      `ResolvePhaseSteps`/the 1b runner
- [x] implement `ListScenarios(baseDir)` over `workspace/tests/*.yml`; scenario name
      = basename without `.yml`, must already match `^[a-z0-9][a-z0-9_-]*$` — reject
      otherwise with a clear error; no case-folding or sanitising (absent dir →
      empty list, no error; `.yaml` accepted too)
- [x] write table tests: valid fixture; unknown top-level field; unknown step field;
      missing `type`; missing `cmd`; unknown `type`; invalid `when`; invalid
      filename; empty file; `auto` var kept raw; enable/disable lists
- [x] run `go test ./internal/core/workflow/envtest/... ./internal/core/project/config/...`
      — must pass before task 7 — pass; `golangci-lint` on both packages clean

### Task 7: Loader-side `${...}` rendering of step `cmd:`/`with:`

**Files:**
- Create: `internal/core/workflow/envtest/render.go`
- Create: `internal/core/workflow/envtest/render_test.go`

- [x] implement `RenderSteps(steps, cfg)` — renders `cmd:` and every string leaf under
      `with:` through the `${...}` substrate (`internal/shared/tpl`) against
      `cfg.Raw`; non-string YAML types (ints, bools, nested maps/lists) preserved
      untouched; absent path → empty string (substrate's lenient semantics) —
      landed in `render.go`; also recurses one level into parallel substeps (same
      cmd/with rendering) and nil-config-safe
- [x] order contract in doc comment: rendering runs BEFORE `ResolvePhaseSteps` so
      plan-time `builtin.Validate` sees rendered params (spec §4)
- [x] write tests: `${vars.x}` in `cmd:`; string leaf in nested `with:` map; int/bool
      `with:` values untouched; absent var → empty string; step without `with:`
      — plus string leaves in `with:` lists, parallel substeps, and nil config
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 8 —
      pass; `golangci-lint` clean

### Task 8: Verify acceptance criteria

- [x] verify against spec §4: predicate bodies legal everywhere, assertion semantics,
      always-run at both deploy gates, `http_check` shape, scenario schema matches the
      spec example (minus 1b-owned behaviour) — confirmed present: `kindAllowed`
      relaxation + `KindOf` classifier; `pipeline.StepForcesRun` wired into both deploy
      gates (`hasAlwaysRunSteps` early gate + `makeSkipDecider`); `http_check`
      `KindPredicate` builtin with url/status/contains/retries/interval/timeout;
      `envtest.LoadScenario`/`ListScenarios`/`RenderSteps` + `AutoPortSentinel` +
      `config.ValidateDeploySteps`
- [x] verify backward compatibility: no existing golden/test changed except ones
      explicitly extended for the new capability — `make test` passes with the full
      suite green (existing goldens byte-identical; the relaxation is purely permissive)
- [x] run full suite: `make test` — pass
- [x] run `make lint` — 0 issues

### Task 9: [Final] Internals documentation + plan close-out

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md` (only if a critical-pattern entry is warranted)

- [ ] update `docs/internals/packages.md`: builtin-kinds contract (predicate-as-body +
      always-run helper and its two deploy call sites), new `envtest` package section
      (scenario schema, loader strictness divergence, render-before-resolve contract)
- [ ] add/adjust an AGENTS.md critical-pattern bullet only if the always-run helper
      contract is load-bearing enough to trap future contributors (judge at
      implementation time; default: packages.md only)
- [ ] run `make build` (embeds updated internals docs)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*No checkboxes — external follow-ups.*

- **Plan 1b** (runner + isolation + CLI + teardown + user-facing docs
  `docs/reference/config/tests.md`): write via `/planning:make` after this plan
  completes, folding in implementation learnings (final helper shape, loader API).
- **Manual smoke** (optional until 1b): add a predicate-body assertion step to a real
  project's `deploy.yml` and observe it re-running on every deploy.
- **Vikunja task 170**: comment when stage 1a lands (engine prerequisite done).

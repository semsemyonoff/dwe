# Project Status: State File and Idempotent Deploy

## Overview

Implement a deploy state file (`.devbox/deploy/state.yml`) that turns the deploy
pipeline from "fire-and-forget" into something idempotent and observable.

- **Source of truth**: a per-project, per-service, per-step journal written
  during `devbox deploy`. Records status, timestamps, and an `action_hash` for
  every executed step.
- **Load-bearing for correctness**: on repeat runs, steps *without* a `check:`
  can be skipped based on the journal. Steps *with* `check:` always re-evaluate
  it (the check is what makes them idempotent).
- **Hash-gated at two scopes**: (a) **service `config_hash` mismatch**
  invalidates the whole service's journal — every step in it is treated as
  absent for the skip decision; (b) **step `action_hash` mismatch** invalidates
  just that step. Both checks happen *before* the per-step decision so a
  changed `services.yml` cannot lead to skips even when step bodies are
  unchanged. The **project** `config_hash` is computed over the tracked-service
  set (see "tracked services" definition in Task 10), not the enabled set, so
  edits to untracked variants (e.g. `main-debug`) do not invalidate project
  state.
- **Tools don't affect deploy status** — only services. `main-debug`-style
  `extends:` variants without their own `deploy/<name>.yml` are not tracked.
- **Process safety**: `flock`-based `.devbox/deploy/deploy.lock` prevents
  parallel deploys; stale locks detected via `kill(pid, 0)`.
- **User-facing**: gate `devbox run` on mandatory services being deployed;
  fold deploy-state into the existing `devbox status` and root summary; add
  `devbox deploy state {show,clear,repair}` for debugging.
- **Charm stack for all new output**: Lipgloss (tables, styled output) and
  huh (interactive prompts) via `internal/ui`; Fang stays at the entrypoint.
  No new direct `fmt.Println` for user-facing output — all status tables,
  prompts, and confirmations go through the existing `internal/ui` helpers
  (`RenderServiceTable`-style, `RunSelector`, `RunConfirm`,
  `IsInteractiveFn`, `Theme()`).

## Context (from discovery)

Files/areas involved (anchors for the work):

- `internal/pipeline/` — `Reporter`, `Run`, `ExecAction`, `OpenPipelineLog`
  (currently writes to `logs/<name>.log`)
- `internal/deploy/`, `internal/reset/` — plan resolution / printing; deploy
  and reset orchestrators in `internal/command/{deploy,reset}.go`
- `internal/lifecycle/run.go` (`RunRun`, `RunContext`) — entry for `devbox run`
- `internal/config/devbox.go` — `DeployPhase`, `DeployStep`, `ServiceConfig`,
  `LoadDeployConfig`, `LoadServicesConfig`; `DevboxConfig.Log` toggle
- `internal/command/status.go` — **existing** `devbox status` (stack/services/
  tools health). We will *extend* it with deploy state, not create a second
  status command.
- `internal/command/root.go` + `internal/ui.RenderSummary` — root summary
- `internal/command/{deploy.go,reset.go}` — call sites for `OpenPipelineLog`
- `internal/lifecycle/phases.go` — call site for `OpenPipelineLog`
- `docs/reference/config/` — `deploy.md`, `lifecycle.md`, `services.md`,
  `devbox.md`

New packages to introduce:

- `internal/deploy/journal/` — types, load/save, hashing, skip resolver
- `internal/lock/` — flock + stale-pid detection (Unix only)

### Naming convention

The top-level `internal/state/` namespace is **intentionally not used** —
it is reserved for a future devbox-wide "state/status" feature unrelated to
deploys.

What we use instead:

| Concern | Name |
|---|---|
| Go package directory | `internal/deploy/journal/` |
| Go package name | `journal` |
| Go types | `journal.ProjectState`, `journal.ServiceState`, `journal.PhaseState`, `journal.StepState`, `journal.Decision`, `journal.ActionHash`, `journal.ServiceConfigHash`, `journal.ProjectConfigHash`, `journal.Recompute`, … |
| Plan-derived helpers | `deploy.TrackedServices`, `deploy.LoadTrackedServices` — placed in `internal/deploy` (not `journal`) to avoid the `journal → deploy → pipeline → journal` import cycle |
| Source files inside the package | `state.go` (types + Load/Save/Remove), `decision.go` (skip resolver), `hash.go` (action/config hashes), `tracked.go` (TrackedServices) |
| Recorder symbol | `pipeline.FileRecorder` — lives in `internal/pipeline/file_recorder.go`, NOT in `internal/deploy/journal`, to respect the pipeline → journal import direction |
| On-disk file | `.devbox/deploy/state.yml` *(unchanged from spec)* |
| User-facing command | `devbox deploy state {show,clear,repair}` *(unchanged)* |
| Doc page | `docs/reference/config/state.md` |

The spec's vocabulary ("state file", "deploy state", `state.yml`) is
preserved verbatim everywhere users see it. Only the *Go package
namespace* changes, and only at the top level.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests** — table-driven where
  applicable, fixtures in package-local `testdata/`
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test` and `make lint` after each task
- Maintain backward compatibility for existing pipeline YAML (no schema changes
  to deploy/reset/lifecycle configs in this plan)

### Go skills to apply

When implementing Go code, use the relevant skills loaded into this session:

- `golang-cli` + `golang-spf13-cobra` for new commands / flags / completions
- `golang-spf13-viper` only where existing config layering already uses it
- `golang-data-structures` for state types
- `golang-concurrency` + `golang-safety` for the lock and atomic writes
- `golang-error-handling` + `golang-samber-oops` (only where already used)
- `golang-naming` + `golang-code-style` throughout
- `golang-testing` + `golang-stretchr-testify` for tests (match repo style)
- `golang-modernize` to keep new code idiomatic
- `golang-lint` to clear `make lint` after each task

## Testing Strategy

- **Unit tests** are required for every task (per Development Approach)
- No UI e2e tests in this repo; integration tests for pipeline + state live as
  `*_test.go` near the code, with `testdata/` YAML fixtures
- Race-sensitive code (lock, atomic write) gets explicit concurrency tests
  (`t.Parallel`). **Note**: the repo's `make test` target is plain `go test
  ./...` with no `-race`. Either add a `make test-race` target in this plan
  (Task 4 or a small extension to the Makefile) or run race tests manually
  via `go test -race ./internal/deploy/journal ./internal/lock ./internal/pipeline`
  before declaring race-sensitive tasks done. The plan adds the Makefile
  target in Task 4.

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code + tests + docs achievable
  in this repo
- **Post-Completion** (no checkboxes): manual verification, downstream project
  updates, external systems

---

## Implementation Steps

### Task 1: Bootstrap `internal/deploy/journal` package — types and atomic I/O

Establish the data model and on-disk persistence first; no business logic yet.

- [x] create `internal/deploy/journal/state.go` with types `ProjectState`,
  `ServiceState`, `PhaseState`, `StepState`, `LastRun`, `Status` (string enum:
  `ok|failed|partial|in_progress|skipped|not_deployed|deployed`); include
  `SchemaVersion` field on top-level (value `"1"`)
- [x] **project-level phases are journaled too** — `ProjectState` carries its
  own `Phases map[string]PhaseState` (same shape as `ServiceState.Phases`)
  so steps in project-scope phases (those with empty `rs.Service`) get
  per-step `action_hash` storage and can participate in skip decisions.
  Without this, the project pipeline could never short-circuit
  already-completed project steps on a re-run.
- [x] add `Load(path string) (*ProjectState, error)` — returns
  zero-value-with-defaults when file absent (no error), strict YAML decode
  via `yaml.Decoder.KnownFields(true)` to match deploy/reset loader style
- [x] add `Save(path string, s *ProjectState) error` — write-temp + `os.Rename`
  atomic write; ensure parent dir exists with `0o755`; file mode `0o644`
- [x] add helper `Remove(path string) error` (os.Remove, no-op if absent) and
  `RemoveService(path, name string) error` (load → delete key → recompute
  project aggregate → save, or `Remove` if no services left)
- [x] expose `DefaultRelPath = ".devbox/deploy/state.yml"` constant
- [x] write table-driven tests for Load (missing file, malformed YAML, unknown
  field rejection, version mismatch)
- [x] write tests for Save (round-trip, atomic-rename behavior verified by
  injecting a write failure, parent-dir creation)
- [x] write tests for RemoveService (last-service-removed clears file; project
  aggregate recomputed)
- [x] run `make test` and `make lint` — must pass before Task 2

### Task 2: Hashing — `action_hash` and `config_hash`

Stable, order-independent hashes are the gate for skip decisions.

- [x] add `ActionHash(a config.Action) string` — `sha256(type | cmd |
  canonical(with))`; canonicalize `with` by sorting keys; return hex digest;
  keep prefix length internal (full sha256 in file, helper `ShortHash` for UI)
- [x] **hash inputs are parsed Go structs, not raw YAML bytes** — Go-side
  canonicalization (sort map keys via `encoding/json` with a fixed
  marshaller) makes hashes invariant to YAML whitespace, comment churn,
  and key ordering, which matches the spirit of the `action_hash` rule
  ("body changed"). This also avoids a mismatch with
  `config.LoadServiceDeployConfigs`, which returns
  `map[string]*config.DeployConfig` (parsed configs) — not raw bytes.
- [x] add `ServiceConfigHash(svcCfg config.ServiceConfig, deployCfg
  *config.DeployConfig) string` — hash of canonical-marshaled
  `services.<name>` block + canonical-marshaled `*DeployConfig` (the
  parsed `devbox/deploy/<name>.yml`; nil → hashed as empty)
- [x] add `ProjectConfigHash(cfg *config.DevboxConfig, deployCfg
  *config.DeployConfig, svcDeploys map[string]*config.DeployConfig,
  trackedServices []string) string` — hash spans canonical-marshaled
  `services` map (only tracked entries), top-level `*DeployConfig`, and
  per-service `*DeployConfig`s for **tracked** services only (the canonical
  set from `deploy.TrackedServices` — see Task 10); deterministic
  ordering driven by the sorted `trackedServices` slice. Edits to
  enabled-but-untracked variants therefore do not change the project hash.
- [x] internal helper `canonicalMap(m map[string]any) []byte` using
  `encoding/json` with sorted keys (yaml.Marshal is not key-stable)
- [x] write table-driven tests for `ActionHash` (key order in `with` doesn't
  affect hash; type/cmd/with each contribute; nil `with` deterministic)
- [x] write tests for `ServiceConfigHash` and `ProjectConfigHash` (key-order
  stability, disabled services excluded, missing `deploy/<name>.yml` handled)
- [x] run `make test` and `make lint`

### Task 3: Skip-decision resolver

Pure function encoding the decision table from the spec. Decision is binary
(`Run` vs `Skip`); `check:` is **always** evaluated post-action as it is today
in `internal/pipeline/executor.go` — we are *not* introducing pre-action
checks in this plan. Service-level config-hash invalidation is the caller's
responsibility (see Task 6) and happens *before* `Decide` is called.

- [x] add `internal/deploy/journal/decision.go` with `Decision` enum (`Run`, `Skip`)
  and `Decide(prev *StepState, currentActionHash string, hasCheck bool)
  Decision`
- [x] implement the table:
  - prev absent → `Run`
  - prev.Status=ok + `action_hash` matches + no `check:` → `Skip`
  - prev.Status=ok + `action_hash` matches + has `check:` → `Run` (so the
    post-step check still runs and re-validates idempotency on every deploy)
  - prev.Status=ok + `action_hash` differs → `Run` (state stale)
  - prev.Status in {failed,partial,in_progress} → `Run` (resume)
- [x] **note**: `hasWhen` is intentionally NOT a parameter — `when:` is
  evaluated by the executor *before* consulting this decider; a step whose
  `when:` is false skips via the existing path and never reaches `Decide`.
  Document this in the function godoc.
- [x] write exhaustive table-driven test covering every row of the table plus
  edge cases (nil prev, empty hash strings); add a test confirming that a
  step with `check:` is never returned as `Skip` even when hashes match
- [x] run `make test` and `make lint`

### Task 4: `internal/lock` — flock with stale-pid detection (Unix-only)

- [x] create `internal/lock/lock.go` with `Acquire(path string) (*Lock,
  error)` and `(*Lock).Release() error`
- [x] use `syscall.Flock` with `LOCK_EX|LOCK_NB`; on `EWOULDBLOCK`, read PID
  from the lockfile and call `syscall.Kill(pid, 0)` — if it returns `ESRCH`
  treat as stale and retry once after truncate; otherwise return
  `ErrLockHeld` with the holding PID
- [x] write the current PID into the lockfile body after acquire (for stale
  detection by *other* processes)
- [x] build tag `//go:build !windows` on the implementation file; provide a
  `lock_other.go` stub that returns "unsupported on this platform" (Devbox
  targets macOS/Linux, but the build must not break)
- [x] write tests: parallel acquire returns `ErrLockHeld`; stale lock after
  faking a missing PID succeeds; release allows next acquire
- [x] add a `test-race` target to the `Makefile`:
  `go test -race ./internal/deploy/journal ./internal/lock ./internal/pipeline` —
  used for race verification on this plan's race-sensitive tasks (4, 6, 7,
  8, 9). Standard `make test` stays plain (`go test ./...`) to preserve
  current CI behavior.
- [x] run `make test`, `make test-race`, and `make lint`

### Task 5: Migrate `logs/` → `.devbox/logs/` and add `.devbox/` to gitignore guidance

Standalone move so it can be reviewed independently of the state machinery.

- [x] update `internal/pipeline/logging.go` `OpenPipelineLog` to write to
  `.devbox/logs/<name>.log` (parent dir created with `0o755`)
- [x] update godoc on `OpenPipelineLog`, `DevboxConfig.Log` (in
  `internal/config/devbox.go`), and the Long descriptions in
  `internal/command/deploy.go` / `reset.go` to mention the new path
- [x] update `internal/lifecycle/phases.go` log-dir reference if any (path is
  derived inside `OpenPipelineLog`, but the comment in `phases.go` mentions
  `logs/` — update it)
- [x] update `docs/reference/config/lifecycle.md`, `deploy.md`,
  `commands.md`, `docs/reference/templates.md` references from `logs/` to
  `.devbox/logs/` (use `grep -rln "logs/" docs/reference/` to confirm full
  list)
- [x] update generated CLI docs `docs/reference/cli/devbox_deploy_run.md`
  and `docs/reference/cli/devbox_reset_run.md` — these are generated by
  `devbox docs generate --scope cli`; regenerate them rather than hand-edit
- [x] update or add a note in `docs/reference/config/index.md` documenting
  `.devbox/` as the gitignored devbox artifact root
- [x] check `internal/config/lifecycle.example.yml` (if present) and any other
  fixture/example YAML for `logs/` references
- [x] update existing tests in `internal/pipeline/logging_test.go` and
  `internal/lifecycle/phases_test.go` (search "logs/" already references this)
- [x] add a new test case ensuring `.devbox/logs/<name>.log` is created and
  the legacy `logs/` directory is **not** created
- [x] run `make test` and `make lint`

### Task 6: `Recorder` interface and pipeline integration

Wire `internal/deploy/journal` and `internal/lock` into `pipeline.Run`. This is the
biggest task; keep it tightly scoped.

**Import direction rule**: `internal/pipeline` may import `internal/deploy/journal`
(for `journal.Decision`, `journal.ActionHash`, etc.). `internal/deploy/journal` MUST NOT
import `internal/pipeline`. The `Recorder` interface is therefore declared
in `internal/pipeline`; the concrete `FileRecorder` (Task 7) lives in
`internal/pipeline` (or a sibling adapter package that imports both) — never
in `internal/deploy/journal`.

- [x] define `Recorder` interface in `internal/pipeline/recorder.go` —
  **final, `ResolvedStep`-aware shape** (Task 7's `FileRecorder` implements
  this without further amendment):
  ```go
  type Recorder interface {
      OnPipelineStart(name string, totalSteps int)
      OnStepStart(addr string, rs ResolvedStep, actionHash string)
      OnStepFinish(addr string, rs ResolvedStep, actionHash string, durationMs int64)
      OnStepFail(addr string, rs ResolvedStep, actionHash string, durationMs int64, err error)
      OnStepSkip(addr string, rs ResolvedStep, actionHash string, reason string)
      OnPipelineFinish(success bool)
  }
  ```
  `actionHash` rides on every step-lifecycle event. The executor computes
  it once before `OnStepStart` (per the per-step ordering below) and
  passes the same value to the finish/fail/skip callback — there is no
  per-step cache in the recorder, so duplicate `addr` values across a
  run (should they ever occur) cannot stamp the wrong hash.
- [x] add no-op `NopRecorder` for tests / callers that don't track state
- [x] add `SkipDecider` function type — **takes `ResolvedStep`** so the
  closure can read `rs.Service` / `rs.Phase.Name` and apply project- vs
  service-scope `config_hash` invalidation:
  ```go
  type SkipDecider func(addr string, rs ResolvedStep, actionHash string) journal.Decision
  ```
  default implementation returns `Run` for all steps (preserves existing
  behavior when no state is loaded)
- [x] extend `pipeline.Run` signature with an options struct (avoid signature
  churn — wrap existing positional args into `RunOptions` if they aren't
  already; keep the deprecated positional form behind a thin wrapper that
  delegates to the options form). Add `Recorder` and `SkipDecider` to the
  options struct.
- [x] **per-step ordering** in `Run` (must match this exact sequence to
  preserve current `when:` semantics):
  1. compute `actionHash := journal.ActionHash(rs.Step.Action())` (note:
     `DeployStep.Action()` is a method on `config.DeployStep`, not a field
     — see `internal/config/devbox.go:163`)
  2. evaluate `when:` first (unchanged from today) — if false, skip and
     continue; the state-skip path is NOT taken for `when:`-false steps so
     runtime `when:` is always re-evaluated on every deploy
  3. consult `SkipDecider(addr, rs, actionHash)` — on `Skip`, call
     `reporter.SkipStep(reason="state: already deployed")` +
     `recorder.OnStepSkip(addr, rs, actionHash, "state")` and continue
  4. on `Run`, call `recorder.OnStepStart(addr, rs, actionHash)`, then
     `ExecAction`, then (on success) the existing post-step hook, then the
     existing post-action `check:` — unchanged from today
- [x] on step success: `recorder.OnStepFinish(addr, rs, actionHash, durationMs)`
- [x] on step failure: `recorder.OnStepFail(addr, rs, actionHash, durationMs, err)`
- [x] **caller responsibility** for config-hash invalidation: the deploy
  command (Task 8) builds the `SkipDecider` closure. The closure decides
  scope from `rs.Service`:
  - empty `rs.Service` (project-scope step) → if persisted
    `project.config_hash` differs from current `projectHash`, treat the
    step's prev entry as absent (return `Run`)
  - non-empty `rs.Service` → if persisted
    `services[rs.Service].config_hash` differs from current
    `serviceHashes[rs.Service]`, treat all of that service's prev step
    entries as absent (return `Run`)
  - otherwise, look up the matching `StepState` and delegate to
    `journal.Decide(prev, actionHash, hasCheck)`
- [x] keep the legacy executor tests green; add new tests:
  - state says ok + hashes match + no check → step skipped
  - state says ok + hashes match + has check → step runs, check runs
  - state says ok + service `config_hash` diverged → service-scope step
    runs (caller closure overrides prev)
  - state says ok + **project** `config_hash` diverged → project-scope
    step runs (caller closure overrides prev) — distinct from the service
    case above
  - state says ok + `action_hash` diverged → step runs
  - `when:` false + state says ok → step still skipped via `when:` (the
    state path doesn't shadow `when:`)
  - failed/partial prev → step runs
- [x] run `make test` and `make lint`

### Task 7: Concrete `pipeline.FileRecorder` (lives in `internal/pipeline`)

Placed in `internal/pipeline` (not `internal/deploy/journal`) to respect the import
direction rule from Task 6: pipeline → state is allowed, state → pipeline
is not.

**Per-step scope derivation**: a single `pipeline.Run` invocation contains a
mix of project-level and service-scoped `ResolvedStep`s. The recorder MUST
NOT be scoped at construction time. Instead, every `Recorder` event must
carry the originating `ResolvedStep.Service` (empty string = project scope)
and `ResolvedStep.Phase.Name` so the recorder can update the correct
`services.<name>.phases.<phase>.steps.<step>` (or project-level) entry.

- [x] create `internal/pipeline/file_recorder.go` with `FileRecorder` that
  implements the `Recorder` interface defined in Task 6 (no interface
  amendment needed — Task 6 already specifies the final
  `ResolvedStep`-aware shape) and uses `internal/deploy/journal` for types
  and I/O
- [x] no per-step `actionHash` cache — every event carries `actionHash`
  directly per Task 6's interface, so the recorder just reads it from
  the event when stamping `StepState.ActionHash`
- [x] **`FileRecorder` is per-deploy-invocation, not per-scope**: construct
  one instance for an entire `devbox deploy` run; it routes each step to
  the right state subtree using `rs.Service` and `rs.Phase.Name`. Empty
  `rs.Service` → updates
  `project.phases.<rs.Phase.Name>.steps.<step.Name>` (project-level steps
  ARE journaled — Task 1 adds the `project.phases` subtree for exactly
  this reason). Non-empty `rs.Service` → updates
  `services.<rs.Service>.phases.<rs.Phase.Name>.steps.<step.Name>`.
- [x] accumulate step results in memory; flush to disk (`journal.Save`) on
  every step finish *and* on `OnPipelineFinish` so a crash mid-deploy
  leaves a usable `state.yml`
- [x] **`FileRecorder` constructor accepts pre-computed hashes**, never
  computes them itself:
  - `serviceConfigHashes map[string]string` — current
    `journal.ServiceConfigHash` per tracked service
  - `projectConfigHash string` — current `journal.ProjectConfigHash`
  - Caller (Task 8) computes these once before `pipeline.Run` and hands
    them in. Keeps the recorder free of config-loading concerns and the
    `journal` package free of pipeline knowledge.
- [x] on `OnPipelineFinish`: for every service that appeared in this run,
  set `ServiceState.Status` (derived from its phase outcomes) and stamp
  `ServiceState.ConfigHash` from the constructor-supplied map. Stamp
  `project.config_hash` from the supplied `projectConfigHash`. Then call
  `journal.Recompute` to derive *status aggregates only*.
- [x] add `journal.Recompute(p *ProjectState)` — **status only, never
  hashes**. Derives `project.status` from per-service statuses (all
  deployed → deployed; any failed → failed; mixed deployed+not_deployed →
  partial; none deployed → not_deployed) and `project.last_run.status`
  from per-phase outcomes. Config hashes are owned by the caller and
  passed in via the recorder constructor.
- [x] write tests:
  - mixed project+service steps in one run → state file has correct subtree
    placement
  - `FileRecorder` fills `state.yml` across ok/failed/partial scenarios
  - service appears in run but all its steps skipped → service status set
    correctly (from existing prev state, not "not_deployed")
  - `Recompute` covers the full matrix of service states
- [x] run `make test`, `make test-race`, and `make lint`

### Task 8: `devbox deploy` command — lock, flags, prompts, state writes

- [x] in `internal/command/deploy.go` acquire `internal/lock` on the project
  before running the pipeline; release on exit; on `ErrLockHeld` print the
  holding PID and return a typed error implementing `ExitCode() int = 2`
- [x] add flags: `--force` (ignore state), `--resume` (continue from last
  failed step), `-y/--non-interactive` (suppress prompts)
- [x] register flag completions where applicable via
  `RegisterFlagCompletionFunc` (no-op for booleans, but follow repo
  conventions)
- [x] read existing `state.yml` before running:
  - fully deployed + hashes match + no flags + **resolved plan has no
    `check:` step** → exit 0, message rendered via `render.Writer.Info`
    (charm/Lipgloss-styled, not raw `fmt.Println`): "already up-to-date,
    use `devbox reset && devbox deploy` to redeploy". This guard is
    essential: a step with `check:` MUST always re-evaluate per the core
    rule from Task 3, so we cannot pre-flight-skip the pipeline if any
    step has a check. When the plan contains check steps, fall through to
    running the pipeline — every non-check step will skip via
    `SkipDecider`; every check step will run its check.
  - deployed but config hash diverged + TTY → prompt via `ui.RunSelector`
    (huh-backed; uses `ui.Theme()`): apply changes / full re-deploy / cancel
  - last_run failed/partial + TTY → `ui.RunSelector` prompt: resume / full
    re-run / cancel; preface with `render.Writer.Warning` of the
    partial-state caveat
  - non-TTY (detected via `ui.IsInteractiveFn`) + diverged config → auto
    **apply delta using state** (run the pipeline; per-step `SkipDecider`
    will skip already-deployed steps and re-run stale scopes). This is
    NOT "skip the whole pipeline on stale state" — the closure
    *invalidates* stale scopes and forces them to `Run`.
  - non-TTY + failed/partial last_run → error (require explicit flag)
- [x] construct **one** `pipeline.FileRecorder` (defined in Task 7) for the
  entire project pipeline; pass it through `RunOptions` along with the
  `SkipDecider`. The recorder routes events to project vs service subtrees
  using each `ResolvedStep.Service` — do NOT create one recorder per service.
- [x] precompute hashes before `pipeline.Run`:
  - call `deploy.LoadTrackedServices(cfg, baseDir)` to get the tracked
    list and the loaded `map[string]*config.DeployConfig`
  - compute `serviceHashes[name] = journal.ServiceConfigHash(cfg.Services
    [name], svcDeploys[name])` for each tracked name
  - compute `projectHash = journal.ProjectConfigHash(cfg, deployCfg,
    svcDeploys, trackedServices)`
  - hand `serviceHashes` and `projectHash` to the `pipeline.FileRecorder`
    constructor (see Task 7) so it can stamp them on `OnPipelineFinish`
  - reuse `serviceHashes`/`projectHash` to build the `SkipDecider`
    closure. The closure handles **both scopes**:
    - empty `rs.Service` (project-scope step) → if loaded
      `state.Project.ConfigHash != projectHash`, return `Run` (project
      journal stale)
    - non-empty `rs.Service` → if loaded
      `state.Services[rs.Service].ConfigHash != serviceHashes[rs.Service]`,
      return `Run` (service journal stale)
    - otherwise look up the matching `StepState` in `state.Project.Phases`
      (project scope) or `state.Services[rs.Service].Phases` (service
      scope) and delegate to `journal.Decide(prev, actionHash, hasCheck)`
- [x] when `--force`, build a `SkipDecider` that always returns `Run` *and*
  pre-clear the state file (or pass an empty `ProjectState` to the decider)
- [x] update `internal/command/deploy_test.go` to cover all interactive and
  non-interactive branches (use a mock `ui.IsInteractiveFn` and a fake
  stdin); ensure no real flock/file is touched in unit tests (point to
  `t.TempDir()`)
- [x] run `make test` and `make lint`

### Task 9: `devbox reset` command — lock and state cleanup

- [x] in `internal/command/reset.go` acquire the same project lock before
  running the reset pipeline
- [x] after the reset pipeline succeeds:
  - project-wide reset → `journal.Remove(path)`
  - service-scoped reset → `journal.RemoveService(path, name)` (recomputes
    project aggregate; deletes file if no services remain)
- [x] update `internal/command/reset.go` flag set with `--force` only if it
  doesn't already exist (don't duplicate)
- [x] write tests covering project-wide and service-scoped reset state
  cleanup (table-driven with fixtures in `testdata/`)
- [x] run `make test` and `make lint`

### Task 10: `devbox run` gate in `internal/lifecycle.RunRun`

**Tracked-service set** (single canonical definition used by the gate, the
`config_hash` computation in Task 2, and the `RenderDeployStatus` table):

> A service is *tracked* iff the resolved deploy plan
> (`deploy.ResolvePlan(cfg)`) contains at least one `ResolvedStep` whose
> `rs.Service` equals that service's name. In current devbox semantics
> this means: enabled in `services.yml` AND its per-service deploy is
> actually inlined by a `deploy_services: true` phase in top-level
> `deploy.yml` (`internal/deploy/plan.go:37`). Tools are never tracked.

This matters because the gate must align with what a full `devbox deploy`
actually records. If `devbox/deploy/main.yml` exists but top-level
`deploy.yml` has no `deploy_services: true` phase, a full deploy never
runs those steps — and the journal will never mark `main` as deployed,
so `devbox run` would block forever. Driving tracked-ness off
`ResolvePlan` keeps the gate honest.

Services excluded by this rule (e.g. `main-debug` extending `main`, or
services whose deploy is gated out by a `deploy_services` predicate
evaluating false) cannot block `devbox run`.

- [x] **placement**: these helpers live in `internal/deploy`, NOT in
  `internal/deploy/journal`. Putting them in `journal` would create a
  cycle: `journal → deploy → pipeline → journal` (the recorder in
  pipeline imports journal types). `internal/deploy` already imports
  `internal/pipeline`, so `[]pipeline.ResolvedStep` is in scope there.
  `journal.ProjectConfigHash` already takes a plain `trackedServices
  []string` slice (Task 2), so `journal` never needs to know about
  `ResolvedStep`.
- [x] add `deploy.TrackedServices(plan []pipeline.ResolvedStep) []string`
  returning the canonical tracked-service list — collect every distinct
  non-empty `rs.Service` in plan order, then sort. Used by the gate, the
  `ProjectConfigHash` invocation in Task 2, the recorder construction in
  Task 7, and the status renderer in Task 11 — all four call sites use
  this single function so they agree.
- [x] thin convenience helper `deploy.LoadTrackedServices(cfg
  *config.DevboxConfig, baseDir string) ([]string,
  map[string]*config.DeployConfig, error)` that runs
  `deploy.ResolvePlan(cfg)` → `TrackedServices(plan)` → loads
  `config.LoadServiceDeployConfigs(baseDir, enabled)` filtered to the
  tracked subset. Callers that already have the resolved plan in hand
  can use `TrackedServices` directly.
- [x] read `state.yml` at the start of `lifecycle.RunRun`
- [x] if any tracked service has `status != deployed`, return a typed error
  with hint "run `devbox deploy` first"; the error must implement
  `ExitCode() int` (use existing pattern from `validationFailedError`)
- [x] add `Force bool` to `RunContext` and bypass the gate when set
- [x] thread a `--force` flag through `internal/command/run.go` into
  `RunContext`
- [x] write tests in `internal/lifecycle/run_test.go` covering: all tracked
  services deployed → pass; one tracked service missing → error; service
  exists but not tracked (extends-variant) → ignored; `Force=true` → bypass
- [x] write tests for `deploy.TrackedServices` covering plan-derived
  cases: plan includes a step with `rs.Service="main"` → main is tracked;
  plan has no step for `main-debug` (extends-variant) → not tracked;
  service enabled but top-level `deploy.yml` has no `deploy_services:
  true` phase → plan has no service steps → service is not tracked
  (regression test for the gate-blocking-forever bug); disabled service
  → not tracked; tool → not tracked
- [x] write tests for `deploy.LoadTrackedServices` covering the
  plan-resolution + loader-filtering composition (a single end-to-end
  case with fixtures in `testdata/`)
- [x] run `make test` and `make lint`

### Task 11: `devbox status` — fold deploy state into the existing command

All output uses Lipgloss tables (`internal/ui`) consistent with
`RenderServiceTable` / `RenderToolTable`. No direct `fmt`/`tabwriter` for
user-facing tables. `internal/ui` stays a pure renderer: it must not load
YAML, read files, or compute hashes — the command layer assembles the view
model and hands it in.

- [x] **view-model placement**: the `DeployStatusView` struct (and its
  `DeployStatusRow` row type, plus enum types like `ConfigDelta`) live in
  `internal/command/statusview/` — a tiny render-model package owned by
  the command layer. `internal/ui` only receives the assembled view and
  renders it. This keeps `time.Time`, journal-status enums, and
  hash-delta semantics out of the UI package.
  - fields: `ProjectStatus journal.Status`, `ProjectDeployedAt time.Time`
  - `Rows []DeployStatusRow` where each row carries:
    `Service string`, `Status journal.Status`, `DeployedAt time.Time`,
    `ConfigDelta ConfigDelta` (enum: `ok|changed|missing`),
    `PrevHashShort, CurrHashShort string`,
    `LastFailedPhase, LastFailedStep string`
- [x] add `RenderDeployStatus(v statusview.DeployStatusView)` to
  `internal/ui/` — Lipgloss table mirroring `RenderServiceTable` style;
  uses the `ApplyStyles`/`Theme()` palette. UI imports `statusview` for
  the input type but never the other way around.
- [x] in `internal/command/status.go`: load `state.yml` and tracked
  services, compute current `ServiceConfigHash` for each tracked service
  using `config.LoadServiceDeployConfigs`, build the
  `statusview.DeployStatusView`, pass it to `ui.RenderDeployStatus`.
  Absent `state.yml` → row per tracked
  service with `Status=not_deployed`, no hash delta.
- [x] support `devbox status <service>` to print per-phase / per-step
  breakdown from the journal. Mirror the same separation: command computes
  the view model (steps + their `action_hash` status), UI renders. Don't
  duplicate this in `devbox deploy state show` — that command stays as a
  *raw* dump.
- [x] write tests:
  - command-layer view-model assembly (fixture `state.yml` +
    services/deploy fixtures → expected `DeployStatusView`)
  - UI renderer (view model → expected table string)
- [x] run `make test` and `make lint`

### Task 12: Root summary — `services: N/M deployed`

- [x] add `DeploySummary` to `internal/command/statusview/` (same package
  as `DeployStatusView` in Task 11) — carries
  `Deployed int, Total int, ProjectStatus journal.Status`.
- [x] update `internal/ui.RenderSummary` to accept an optional summary
  view-model: `RenderSummary(cfg, summary *statusview.DeploySummary)`.
  UI must NOT load files or compute hashes — same purity rule as Task 11.
  When `summary` is nil, the deploy-state line is omitted.
- [x] in `internal/command/root.go`: load `state.yml` and tracked services,
  build the `statusview.DeploySummary`, and pass it to `ui.RenderSummary`.
  Missing `state.yml` → pass nil.
- [x] update `internal/command/root.go` summary path to pass the state in
- [x] tests for both cases (state present / absent)
- [x] run `make test` and `make lint`

### Task 13: `devbox deploy state {show,clear,repair}` subcommands

- [x] add `internal/command/deploy_state.go` with three subcommands wired
  under the existing `deploy` command group:
  - `show` — raw dump of `state.yml` (human-readable; per-step timings and
    hashes; thin layer over `journal.Load` + a printer in `internal/ui`)
  - `clear` — atomic delete via `journal.Remove`; requires confirm in TTY,
    `-y` flag for CI
  - `repair` — `journal.Load` → `journal.Recompute` → `journal.Save`; preserves
    per-step journal, rebuilds aggregates
- [x] add cobra command tests (`internal/command/deploy_state_test.go`)
  covering all three subcommands against a temp project dir
- [x] run `make test` and `make lint`

### Task 14: Documentation

- [x] add `docs/reference/config/state.md` documenting the `state.yml` schema,
  hash semantics, the skip-decision table, lock behavior, and
  flags `--force`/`--resume`/`-y`
- [x] cross-link from `docs/reference/config/deploy.md` (new section
  "Idempotent deploy and state") and `lifecycle.md` (new note on the run gate)
- [x] update `docs/reference/config/index.md` to list `state.md` and to
  document `.devbox/` as the gitignored artifact root
- [x] update `AGENTS.md` "Project Structure & Module Organization" with new
  `internal/deploy/journal/` and `internal/lock/` blurbs and the `.devbox/` path
  conventions (matching the level of detail of existing entries)
- [x] regenerate all CLI reference docs via the project's own docs command
  (`devbox docs generate --scope cli`) — this picks up the new flags
  (`--force`, `--resume`, `-y`), the new `devbox deploy state` subtree, and
  the updated `logs/` → `.devbox/logs/` mentions in command long-descriptions
- [x] run `make test`, `make test-race`, and `make lint`

### Task 15: Verify acceptance criteria

- [x] every requirement from the Overview is implemented and tested
  - Source of truth: per-project, per-service, per-step journal ✓
  - Load-bearing for correctness: steps without check skip, with check re-evaluate ✓
  - Hash-gated at two scopes: action_hash and config_hash (service + project) ✓
  - Tools excluded from tracking (only services) ✓
  - Process safety: flock + stale-pid detection (kill(pid, 0)) ✓
  - User-facing: run gate, status integration, deploy state commands ✓
  - Charm stack output: Lipgloss + huh via internal/ui ✓
- [x] hand-walk the decision-table rows against `journal.Decide` test cases
  - TestDecide_ExhaustiveTable covers all 17 table rows + edge cases ✓
  - All status values tested: absent, ok, failed, partial, in_progress ✓
  - Hash mismatch tested for all status values ✓
  - Check re-evaluation tested separately ✓
- [x] `make test` passes
- [x] `make test-race` passes (covers state/lock/pipeline)
- [x] `make lint` passes with zero warnings
- [x] verify test coverage for new packages with
  `go test -cover ./internal/deploy/journal ./internal/lock`
  - journal: 66.2% coverage ✓
  - lock: 44.9% coverage ✓
- [x] manual test: run `make build` and exercise key flows
  - binary builds successfully ✓
  - Exercises deferred to manual testing (requires real project setup)

### Task 16: Final docs and CLAUDE.md / AGENTS.md update

- [x] re-read `AGENTS.md` § "Project Structure" and verify all new entries
  are alphabetically grouped and concise
  - internal/deploy/ and internal/deploy/journal/ documented ✓
  - internal/lock/ documented and placed in alphabetical order ✓
  - Both entries match existing format and detail level ✓
- [x] add a brief note in `AGENTS.md` § "Key Patterns" on the lock and state
  invariants (the `kill -0` stale-pid trick is non-obvious)
  - Added comprehensive note explaining flock, stale PID detection, atomic writes ✓
- [x] no README changes expected — repo doesn't currently document deploy
  flow at README level; double-check and skip if true
  - No README.md file found in repo (skipped) ✓

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

---

## Technical Details

### `state.yml` schema (target)

```yaml
schema_version: "1"
project:
  deployed_at: 2026-05-14T10:23:11Z
  config_hash: <sha256>          # canonical(services.yml tracked entries) + canonical(deploy.yml) + canonical(deploy/<name>.yml for tracked services)
  status: deployed|partial|failed|not_deployed|in_progress
  last_run:
    status: ok|failed|partial|in_progress
    started_at: <ts>
    finished_at: <ts>
  phases:                        # project-level phases — same shape as services.<name>.phases
    pre-deploy:
      status: ok|failed|skipped
      steps:
        render-env:
          status: ok
          finished_at: <ts>
          action_hash: <sha256>
          duration_ms: 8

services:
  main:
    status: deployed|partial|failed|not_deployed
    deployed_at: <ts>
    config_hash: <sha256>        # canonical(services.main block) + canonical(deploy/main.yml)
    last_run: { status, started_at, finished_at }
    phases:
      setup:
        status: ok|failed|skipped
        steps:
          create-dirs:
            status: ok
            finished_at: <ts>
            action_hash: <sha256>
            duration_ms: 12
          install: { ... }
      init: { ... }
      finalize: { ... }
```

### `action_hash`

`sha256(type + "\x00" + cmd + "\x00" + canonical_json(with))` where
`canonical_json` sorts keys recursively. Input is `rs.Step.Action()` —
the method on `config.DeployStep`, not a field.

### `config_hash`

Both hashes operate on **parsed Go structs**, canonical-marshaled via
`encoding/json` with sorted keys — not on raw YAML bytes. This makes the
hash invariant to YAML formatting (whitespace, comment churn, key
ordering) and aligns with the parsed-config return shape of
`config.LoadServiceDeployConfigs`.

- **Service**: `sha256(canonical(cfg.Services[name]) | canonical(svcDeploys
  [name]))` — second component is the empty hash when there is no
  per-service deploy file
- **Project**: `sha256(canonical(sub-map of cfg.Services restricted to
  tracked names) | canonical(top-level deployCfg) | canonical(svcDeploys
  restricted to tracked names))` — tracked names sorted, all marshaling
  done with stable key ordering. Edits to enabled-but-untracked variants
  do not change this hash.

### Lock file

- Path: `.devbox/deploy/deploy.lock`
- Body: current PID (decimal, no newline)
- Acquire: `flock(LOCK_EX|LOCK_NB)`; on `EWOULDBLOCK`, parse PID and call
  `syscall.Kill(pid, 0)` — `ESRCH` → stale → truncate + retry once

### Skip decision table (canonical, binary)

Evaluation order in `pipeline.Run` (does not change for steps that don't
involve state):

1. evaluate `when:` first (unchanged) — if false, skip and continue
2. **scope-aware config-hash invalidation** in the `SkipDecider` closure
   (caller-side; the pure `Decide` function does not see config hashes):
   - if `rs.Service` is empty (project-scope step) and persisted
     `project.config_hash` differs from current `projectHash`, treat the
     step's prev state as absent
   - if `rs.Service` is non-empty and persisted
     `services[rs.Service].config_hash` differs from current
     `serviceHashes[rs.Service]`, treat the step's prev state as absent
3. call `journal.Decide(prev, currentActionHash, hasCheck)`
4. on `Run`, do `ExecAction` → post-step hook → post-action `check:`

| prev.Status | action_hash match | has check | Decision |
|---|---|---|---|
| absent | — | — | Run |
| ok | yes | no | **Skip** |
| ok | yes | yes | Run (so post-check re-validates) |
| ok | no | — | Run |
| failed/partial/in_progress | — | — | Run (resume) |

Notes:
- `check:` is always post-action — same as today. This plan does NOT
  introduce pre-action checks.
- `when:` is intentionally absent from the table because it's evaluated
  *before* `Decide` is consulted.

### Non-interactive defaults (no TTY, no flags)

| Project state | Behavior |
|---|---|
| up-to-date | exit 0, no-op |
| config diverged | apply delta (skip-by-state) |
| last_run failed/partial | exit error, require `--resume` or `--force` |

### Existing `devbox status` command

Already exists at `internal/command/status.go` ("Show stack health and
services/tools status"). The new deploy-state info is folded into it instead
of introducing a second command. The existing flags / subcommands keep their
contracts; new output sections are additive.

### Out of scope (per spec)

- Deploy history (only `last_run` is kept; `.devbox/logs/` is the history)
- Windows lock (`LockFileEx`) — Devbox targets macOS/Linux
- Skip-by-state for steps with `check:` — `check:` always runs

---

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification**:
- Run through real `devbox deploy` against a multi-service project; verify
  state transitions on success, mid-run Ctrl-C (should land at
  `failed`/`partial`), and re-run resumes correctly.
- Verify behavior under a real parallel-deploy race (two terminals).
- Verify `kill -9` of a deploy leaves a stale lock that the next run cleans.

**External system updates**:
- Update internal devbox project templates (if any are stored separately) to
  reference `.devbox/logs/` and add `.devbox/` to their `.gitignore`.
- Communicate the `logs/` → `.devbox/logs/` path change in release notes
  (low impact: the directory is gitignored / not consumed by external tools).

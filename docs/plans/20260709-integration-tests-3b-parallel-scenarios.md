# Integration tests — stage 3b: parallel scenario execution + aggregated live reporter

## Overview

Fifth and final plan in the integration-tests feature
([spec](specs/2026-07-06-integration-tests.md) — the single source of truth for design
decisions; do not re-open settled decisions). Stages 1a/1b (MVP), 2 (reports & clean),
and 3a (timeouts, validator, isolation scanner) are complete and landed
([1a](completed/20260706-integration-tests-1a-engine-and-scenario-schema.md),
[1b](completed/20260706-integration-tests-1b-runner-isolation-cli.md),
[stage 2](completed/20260708-integration-tests-stage2-reports-clean.md),
[3a](completed/20260708-integration-tests-3a-timeouts-validator-isolation.md)).

Stage 3b delivers the remaining spec §7 stage-3 features:

1. **Parallel scenario execution** — `dwe test run --parallel N` (default 1) runs up to N
   scenarios concurrently. Isolation already holds per scenario (per-scenario flock, copy
   dirs, per-run-id manifests/compose projects); the one genuine gap is the intra-process
   port-allocation race, closed by a process-wide port lease set.
2. **Aggregated live reporter** — at effective parallelism > 1, one sticky block-row per
   scenario (spinner + name + coarse phase + elapsed; ✓/✗ on completion) via the existing
   `liveui.LiveLine` block mode, fed by a new coarse `Progress` seam on
   `envtest.RunRequest`. Per-scenario pipeline/deploy output goes to the per-copy run log
   only.

Brainstorm-settled decisions (2026-07-09, do not re-open):

- **N=1 (default) keeps today's streaming output byte-identical.** The aggregated view
  engages ONLY when effective parallelism = `min(N, len(names))` is > 1 — one scenario
  with `--parallel 8` still takes the current sequential/streaming path.
- **Row-per-scenario** (not per-worker-slot) block display, built on the existing
  LiveLine block mode — no new multi-row liveui component.
- **Port fix = process-wide lease set** in `envtest.AllocatePorts` (never released —
  short-lived process). Cross-process races stay on `ports_free` preflight + the one
  deploy retry (spec §9 accepts this).
- **Orchestration lives in the CLI** (`internal/cli/test/run.go`, `errgroup.SetLimit`),
  preserving the existing `scenarioRunner` seam; `envtest` gains only the UI-free
  `Progress` callback.

## Context (from discovery)

- `internal/cli/test/run.go` — `runTestRun` loops sequentially over resolved names,
  one `runner.RunScenario(ctx, req)` per scenario; outcomes collected into
  `[]scenarioOutcome`, rendered via `cmdctx.WriteData` (text or JSON), exit code derived
  from statuses (0/1/2). `jsonReporterFactory` already builds a fully silent per-scenario
  reporter (screen = `io.Discard`, subprocess output → run log only).
- `internal/core/workflow/envtest/runner.go` — `RunScenario` is self-contained and
  concurrency-safe across distinct scenarios: per-scenario flock (`LockPath`), per-scenario
  copy dir (`RunDir`), per-run-id manifest + compose project. Seams: `execDwe`,
  `allocatePorts`, `newTeardownDeps`, `collectReport`, `clock`. `warn` is nil-guarded.
- `internal/core/workflow/envtest/ports.go` — `AllocatePorts(n)` guarantees uniqueness
  only within one batch (listeners all held open, then closed). Two concurrent batches in
  one process can return the same port — the TOCTOU this plan closes.
- `internal/shared/liveui/liveline.go` — block mode already exists and is
  concurrency-safe: `StartBlock(rows)` / `SetBlockRowRunning(idx, label)` /
  `SetBlockRowFinal(idx, kind, label)` / `EndBlock()`. A row renders as
  `  <icon> [<elapsed>] <label>` — icon and per-row stopwatch are built in, so the CLI's
  label is just `name  phase…`. `SetBlockRowRunning` starts the stopwatch on first call;
  there is NO way today to set a label without starting the stopwatch (needed for queued
  rows — small additive method below). Disabled (non-TTY) mode: every method is a no-op
  except `Println`, which writes plain lines. `PrintlnDiag` + `SetDiagWriter(os.Stderr)`
  frames a line above the footer while landing the bytes on stderr. Width via
  `termWidth()`; LiveLine has no height detection — the CLI clamps row count itself via
  `term.GetSize` (`github.com/charmbracelet/x/term`, already a liveui dep).
- `golang.org/x/sync v0.21.0` (errgroup) is already in `go.mod`.
- CLI tests use the `newRunner` var seam + `fakeRunner` (`run_test.go`); envtest tests
  build `&Runner{...}` literals with stubbed seams (documented test-recursion hazard: the
  `execDwe` seam MUST be stubbed).
- Docs: `docs/reference/config/tests.md` (+ `docs/i18n/ru/reference/config/tests.md`),
  `docs/guides/integration-tests.md` (+ `docs/i18n/ru/guides/integration-tests.md`),
  `docs/internals/packages.md` §§ envtest / cli/test / liveui.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task) — repo style:
  table-driven tests, package-local `testdata/` fixtures, injectable seams (no real Docker,
  no real `dwe` subprocess in any test).
- Complete each task fully before the next; small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** — success and error scenarios as
  separate checklist items.
- **CRITICAL: all tests pass before starting the next task.** Focused package tests per
  task; full `make test` in the verification task.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Backward compatibility: the N=1 path (flag absent or `--parallel 1`) must stay
  byte-identical — existing CLI goldens/tests must pass unmodified.

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach).
- No UI e2e framework in this repo; the aggregated display is pinned by LiveLine-level
  goldens using the existing `SetTestHooks(noTicker, widthFn)` determinism hooks, the same
  way pipeline block-mode tests do.
- Real-Docker smoke is Post-Completion (manual), as in all prior stages.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep the plan in sync with actual work.

## Solution Overview

### `envtest`: coarse progress seam (UI-free)

`RunRequest` gains `Progress func(phase ProgressPhase)` (nil → no-op, mirroring `Warn`).
`ProgressPhase` is a typed string enum fired at the natural points of `RunScenario`, when
the phase actually starts:

| Phase | Fired |
|---|---|
| `PhasePreparing` | after the kept-run guard + config load, immediately before `CopyTree` |
| `PhaseValidating` | before the `dwe validate` subprocess |
| `PhaseDeploying` | before the `dwe deploy run --silent` subprocess |
| `PhaseDeployRetry` | before `retryDeployWithFreshPorts` (only on the one retry) |
| `PhaseRunningSteps` | before `runSteps` |
| `PhaseCollectingReport` | inside `finish()`, only when report collection actually runs |
| `PhaseTearingDown` | inside `teardown()`, only when teardown actually runs (not under `--keep`) |

The CLI maps phase → display label; envtest stays UI-free (layering preserved).

### `envtest`: process-wide port lease set

`ports.go` gains a package-level `sync.Mutex` + `map[int]struct{}` of every port ever
returned by `AllocatePorts` in this process. Harvesting skips a leased port (close that
listener, listen again); each returned port is registered. Leases are never released —
`dwe test` is short-lived and allocates at most dozens of ports. A bounded attempt count
(per batch) guards against a pathological loop and returns an error when exhausted. This
closes the intra-process race completely; the deploy retry automatically gets fresh ports
(the failed attempt's ports stay leased). Cross-process TOCTOU remains covered by the
copy's `ports_free` preflight + the one retry (spec §9).

### `liveui`: one small additive method

`SetBlockRowPending(idx, label)` — sets a block row's label WITHOUT starting its
stopwatch (queued scenarios must not accrue elapsed time while waiting for a worker).
The pending render state is carried by an **explicit `pending bool` field on `blockRow`**
— set only by `SetBlockRowPending`, cleared by `SetBlockRowRunning`/`SetBlockRowFinal`.
Render: a `pending` row shows a gray `IconRunning` dot instead of the blue spinner and
omits the elapsed bracket. Do NOT infer pending from `startTime.IsZero() && !finalized`:
the workflow parallel runner (`internal/core/usercommands/runtime/runners/workflow/parallel.go`)
keeps queued rows never-started for real durations (`SetBlockRowRunning` fires inside
each worker goroutine after an errgroup slot is granted, limit ≈ CPU count), so an
inferred state would silently change its queued-row rendering — and no existing test
captures a queued-row frame to catch it. With the explicit flag, existing consumers
(which never call `SetBlockRowPending`) render byte-identically by construction. All
nine liveline invariants untouched.

### CLI: orchestration + aggregated display

`--parallel N` (default 1); `N < 1` → typed `cmdctx.Err` (exit 2). Effective parallelism
`min(N, len(names))`; when ≤ 1 the existing sequential loop runs untouched. Otherwise:

- `errgroup.Group` with `SetLimit(effective)`; one goroutine per scenario in original
  order. **Goroutines ALWAYS return nil** — a `RunScenario` error is a per-scenario
  outcome (StatusError), never a group cancellation of siblings. Results land in
  `slots[i]` by original index; after `Wait`, nil slots (scenarios never started due to
  Ctrl+C) are compacted out — text and JSON output are deterministic regardless of
  completion order.
- Each goroutine checks `ctx.Err()` first (cancelled → leave slot nil, matching today's
  sequential `break`); in-flight scenarios cancel via the shared ctx and their teardown
  still runs on a fresh context inside the runner.
- Per-scenario reporters are silent: `jsonReporterFactory` is renamed
  `silentReporterFactory` and used by BOTH JSON mode (as today) and the parallel text
  path — deploy/pipeline output goes to the copy's run log only.
- **Aggregated display** (`internal/cli/test/livestatus.go`, text mode only — never in
  JSON mode): wraps one `liveui.LiveLine` (`NewLiveLine(os.Stdout, os.Stdout, isTTY)`,
  `SetDiagWriter(os.Stderr)`, `Start`, `StartBlock(visibleRows)`).
  `visibleRows = min(len(names), termHeight−3)` (floor 1) via `term.GetSize`; scenario i
  maps statically to row i when `i < visibleRows`, overflow scenarios get no row and
  report start/finish via framed `Println` lines instead. Rows: pending label = name
  (via `SetBlockRowPending`), running label = `name  <phase>…` (via `SetBlockRowRunning`
  on each Progress event), final = `SetBlockRowFinal(idx, Done/Failed, "name  passed")`
  or `"name  failed — step \"X\""`. Footer via `SetText`: `running k/n scenarios…`.
  On completion of all scenarios: `EndBlock()` + `Stop()` — the finalized glyph rows stay
  in scrollback, then the standard text report renders below (same renderer as
  sequential mode). Non-TTY text mode: the display must **branch explicitly** on
  disabled mode in `Started`/`Finished` for EVERY scenario (not just overflow rows) and
  emit flat `scenario <name>: started` / `scenario <name>: <status>` lines via `Println`
  — in disabled mode `SetBlockRowRunning`/`SetBlockRowFinal` are silent no-ops
  (`liveline.go:375-378, 408-411`), so relying on the block methods would leave piped/CI
  runs completely quiet until the final report (the per-scenario reporters are silent
  too).
- **Warnings at N>1**: per-scenario warn closure prefixes `[<scenario>] warning: …` and
  routes through the display's `PrintlnDiag` — bytes land on stderr (existing contract),
  framing keeps the block intact. JSON mode keeps warnings suppressed as today.
- Phase labels (English, matching the test CLI's existing non-localized surface):
  preparing…, validating…, deploying…, deploy retry…, running steps…,
  collecting report…, tearing down….

## Technical Details

- `ProgressPhase` constants live in `envtest` (e.g. `PhasePreparing ProgressPhase =
  "preparing"`) so tests can assert firing order with plain string comparisons.
- Lease-set attempt bound: `allocateAttemptsPerBatch = 100 + 20*n` (generous; the
  ephemeral port space is ~16k on macOS, dozens leased at most) — exhaustion returns
  `envtest: allocating free port: exhausted N attempts (M ports leased)`.
- The display type owns a small mutex for its own counters (done/total for the footer);
  LiveLine methods are already concurrency-safe.
- Display constructor takes seams for testability: `isTTY bool`, `termSize func() (w, h
  int)` — production wires `term.IsTerminal(os.Stdout.Fd())` / `term.GetSize`; tests
  inject fixed values and drive frames via `LiveLine.SetTestHooks` + `Tick`.
- errgroup: plain `errgroup.Group` (NOT `WithContext` — sibling scenarios must not cancel
  each other); the outer signal ctx is passed to each `RunScenario` directly.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): manual smoke on a real project, Vikunja update.

## Implementation Steps

### Task 1: `envtest` — `ProgressPhase` + `RunRequest.Progress` seam

**Files:**
- Modify: `internal/core/workflow/envtest/runner.go`
- Modify: `internal/core/workflow/envtest/runner_test.go`

- [x] add `ProgressPhase` string type + the seven constants (`PhasePreparing`,
      `PhaseValidating`, `PhaseDeploying`, `PhaseDeployRetry`, `PhaseRunningSteps`,
      `PhaseCollectingReport`, `PhaseTearingDown`)
- [x] add `Progress func(phase ProgressPhase)` to `RunRequest` (doc comment: coarse,
      UI-free, nil = no-op); nil-guard it at the top of `RunScenario` exactly like `warn`
- [x] fire the phases at the points in the Solution Overview table — `PhaseCollectingReport`
      only inside the `collectReport != nil && !req.Keep && status != StatusPassed` branch,
      `PhaseTearingDown` only when teardown actually runs (not under Keep)
- [x] write test: passed scenario fires exactly `preparing, validating, deploying,
      running_steps, tearing_down` in order (stubbed seams, no report/retry)
- [x] write test: failed deploy fires `…deploying, collecting_report, tearing_down`;
      port-conflict retry path additionally fires `deploy_retry` between them
- [x] write test: `--keep` run fires no `tearing_down`/`collecting_report`; nil Progress
      does not panic anywhere
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 2

### Task 2: `envtest` — process-wide port lease set

**Files:**
- Modify: `internal/core/workflow/envtest/ports.go`
- Modify: `internal/core/workflow/envtest/ports_test.go`

- [x] add package-level `leaseMu sync.Mutex` + `leasedPorts map[int]struct{}`; in
      `AllocatePorts`, skip a harvested port already in the set (close that listener,
      listen again) and register every returned port; keep the whole-batch
      listeners-open-until-done behavior for intra-batch uniqueness
- [x] add the bounded attempt count with the exhaustion error; document the
      never-released process-lifetime lease rationale in the func comment
- [x] add an unexported `resetLeases()` test helper (clears the global map under the
      mutex) so tests can isolate lease-set state via `t.Cleanup(resetLeases)` — the
      global must not leak between sibling tests in the package (mirrors the envtest
      seam philosophy: `execDwe`, `allocatePorts`, `clock`)
- [x] write test: two sequential `AllocatePorts` batches are disjoint
- [x] write test: concurrent `AllocatePorts` calls from multiple goroutines return
      pairwise-disjoint ports (run under `-race`)
- [x] write test: exhaustion error path — deterministically, by pre-leasing the ports a
      stub listener sequence would return (or by pre-filling the lease map) +
      `t.Cleanup(resetLeases)`
- [x] run `go test -race ./internal/core/workflow/envtest/...` — must pass before task 3

### Task 3: `liveui` — additive `SetBlockRowPending` + pending render state

**Files:**
- Modify: `internal/shared/liveui/liveline.go`
- Modify: `internal/shared/liveui/liveline_test.go`

- [x] add `pending bool` to `blockRow`; add `SetBlockRowPending(idx int, label string)`:
      sets the row label + `pending = true`, leaves `startTime` zero and `finalized`
      false; out-of-range/disabled → no-op (mirror `SetBlockRowRunning` guards); redraw
      when live
- [x] clear `pending` in `SetBlockRowRunning` and `SetBlockRowFinal`; in
      `renderBlockRowLocked`, render a `pending` row with a gray `IconRunning` dot
      instead of the blue spinner and without the elapsed bracket — render is keyed on
      the explicit flag ONLY (never inferred from `startTime.IsZero()`), so existing
      consumers' never-started rows (workflow parallel runner queue) stay byte-identical
- [x] confirm `SetBlockRowRunning` after `SetBlockRowPending` starts the stopwatch then
      (first-call `startTime.IsZero()` branch — should already hold, add a test)
- [x] write test: a never-started row WITHOUT `SetBlockRowPending` still renders exactly
      as today (blue spinner + `[0s]`) — pins the workflow-parallel-runner queued-row
      rendering the flag exists to protect
- [x] write test: pending row renders dot + label, no elapsed; transitions
      pending → running → final render correctly (drive via SetTestHooks + Tick)
- [x] write test: out-of-range idx and disabled-mode no-ops
- [x] run `go test ./internal/shared/liveui/...` — must pass before task 4

### Task 4: CLI — `--parallel` flag + errgroup orchestration (no display yet)

**Files:**
- Modify: `internal/cli/test/run.go`
- Modify: `internal/cli/test/run_test.go`

- [x] add `--parallel N` int flag (default 1) with help text; `N < 1` → typed
      `cmdctx.Err("invalid_parallel", …)` before anything runs (exit 2)
- [x] rename `jsonReporterFactory` → `silentReporterFactory` (comment: used by JSON mode
      AND the parallel text path); update references
- [x] compute `effective = min(parallel, len(names))`; `effective <= 1` → existing
      sequential loop, byte-identical (guard every new behavior behind `effective > 1`)
- [x] BEFORE restructuring `runTestRun`: add exact full-match stdout/stderr tests
      (`require.Equal` on complete output, not substrings — the existing run tests only
      substring-match and would not catch an extra line/newline/reporter change) for the
      `effective <= 1` paths: default all-passed, failed scenario, prep-error, `--keep`
      + report-dir lines, no-scenarios, and JSON; these must pass unchanged after the
      restructure
- [x] parallel path: `errgroup.Group` + `SetLimit(effective)`; goroutine per scenario in
      original order writing `slots[i]`; goroutines always return nil (per-scenario errors
      become StatusError outcomes exactly as the sequential path does); first statement in
      each goroutine: `if ctx.Err() != nil { return nil }`; compact nil slots after Wait;
      parallel text mode uses `silentReporterFactory` and per-scenario warn prefixed
      `[<name>] ` (interim sink: mutex-serialized writes to stderr — concurrent bare
      `Fprintln` to the shared writer would be a data race until task 5 routes warnings
      through the mutex-guarded `PrintlnDiag`)
- [x] JSON mode at N>1: unchanged silent factory, suppressed warnings, ordered payload
      (no display — enforced by construction in task 5)
- [x] make `fakeRunner`'s call recording concurrency-safe (mutex around
      `calls`/`composeEnvAtCall` appends) — parallel tests drive it from multiple
      goroutines and task 7 runs `make test-race`
- [x] write test: outcomes order matches original name order when fake runners complete
      in reverse order (fake with per-scenario release channels)
- [x] write test: peak concurrency ≤ N (fake counts in-flight with atomic; assert ≤ limit
      and > 1 to prove parallelism happened)
- [x] write test: cancelled ctx before dispatch → unstarted scenarios absent from
      outcomes; in-flight fake honoring ctx returns and its outcome IS present
- [x] write test: `--parallel 0` / negative → exit 2; `--parallel 8` with one scenario
      takes the sequential path — exact-match output equals the no-flag run, and the
      fake asserts `RunRequest.ReporterFactory` is nil (no silent factory) and no
      display/Progress was installed
- [x] write test: JSON shape at N>1 — ordered scenarios array, clean stdout
- [x] run `go test ./internal/cli/test/...` — must pass before task 5

### Task 5: CLI — aggregated live display + wiring

**Files:**
- Create: `internal/cli/test/livestatus.go`
- Create: `internal/cli/test/livestatus_test.go`
- Modify: `internal/cli/test/run.go`
- Modify: `internal/cli/test/run_test.go`

- [x] implement `runLiveStatus` type: constructor takes scenario names + seams
      (`isTTY bool`, `termSize func() (int, int)`, output/diag writers); builds the
      LiveLine, `Start` + `StartBlock(visibleRows)` with
      `visibleRows = max(1, min(len(names), termHeight−3))`; seeds every visible row via
      `SetBlockRowPending(i, name)`
- [x] methods: `Started(i)` (row → running with `name  preparing…`; overflow → framed
      `Println("scenario <name>: started")`), `Phase(i, envtest.ProgressPhase)` (label
      update via the phase→label map), `Finished(i, outcome)` (SetBlockRowFinal
      Done/Failed + `name  passed` / `name  failed — step "X"`; overflow → framed final
      line; update footer `running k/n scenarios…` under the display's own mutex),
      `Warn(i, msg)` (`PrintlnDiag("[<name>] warning: " + msg)`), `Close()`
      (`EndBlock` + `Stop`)
- [x] wire into the parallel text path of `runTestRun`: display created only when
      `flags.Output != "json"`; `RunRequest.Progress` closure maps to `display.Phase`;
      per-scenario warn routes to `display.Warn`; `Close()` before `cmdctx.WriteData`
      renders the final report
- [x] non-TTY degradation: `Started`/`Finished` branch EXPLICITLY on disabled mode and
      emit the flat `Println` lines for EVERY scenario, not only overflow rows — the
      block-row methods are silent no-ops when LiveLine is disabled, so without the
      branch a piped/CI run shows nothing until the final report
- [x] write golden tests via `SetTestHooks(noTicker, widthFn)` + injected `termSize`:
      initial pending rows, phase-transition relabel, ✓/✗ finalization, footer counter,
      height clamp with overflow scenarios (final lines framed via Println)
- [x] write test: non-TTY with ALL scenarios within `visibleRows` — start and final flat
      lines are emitted for every scenario (pins the explicit disabled-mode branch;
      would fail if `Started`/`Finished` relied on the no-op block methods)
- [x] write test: `Warn` bytes land on the diag writer (stderr seam), not stdout
- [x] write test (run_test.go): end-to-end parallel run with fake runner firing Progress
      phases → display receives them (seam-injected display or capture via writers)
- [x] run `go test ./internal/cli/test/...` — must pass before task 6

### Task 6: Documentation

**Files:**
- Modify: `docs/reference/config/tests.md`
- Modify: `docs/i18n/ru/reference/config/tests.md`
- Modify: `docs/guides/integration-tests.md`
- Modify: `docs/i18n/ru/guides/integration-tests.md`
- Modify: `docs/internals/packages.md`

- [x] reference page (+ ru mirror): `--parallel N` flag — semantics (default 1, effective
      = min(N, scenario count), aggregated compact view at N>1, full streaming at N=1),
      isolation recap (ports auto-remapped per scenario, per-scenario flock), exit codes
      unchanged
- [x] reference page (+ ru mirror): shared package-cache contention note — parallel
      scenarios reusing one `shared: true` cache volume (composer/npm) may contend
      (slowdowns, package-manager lock files); recommend not parallelizing scenarios with
      heavy cold-cache installs; Docker daemon load at N deploys is the user's call
- [x] guide (+ ru mirror): short "running scenarios in parallel" section with an example
- [x] `docs/internals/packages.md`: §§ envtest (ProgressFn contract + firing points, port
      lease set, never-released rationale), cli/test (orchestration in CLI,
      goroutines-never-return-error rule, aggregation only at effective N>1,
      silentReporterFactory dual use, warn routing), liveui (SetBlockRowPending +
      explicit-pending-flag render state)
- [x] `docs/internals/packages.md`: update the EXISTING `jsonReporterFactory` mention in
      the `internal/cli/test/` section to `silentReporterFactory`
- [x] run `make build` (embedded docs sync) — docs tests in `make test` must pass

### Task 7: Verify acceptance criteria

- [ ] verify spec §7 stage 3 remaining features: parallel execution with disjoint ports;
      aggregated per-scenario status rows; N=1 unchanged
- [ ] verify every brainstorm-settled decision listed in this plan's Overview (four
      bullets) and Solution Overview is implemented as recorded
- [ ] verify N=1 byte-identity: the exact full-match output tests added in task 4 pass
      unchanged, and no pre-existing `internal/cli/test` test was modified other than
      additively
- [ ] run `make test` — full suite green
- [ ] run `make test-race` — the new concurrency paths (errgroup fan-out, lease set,
      display mutex) are race-clean
- [ ] run `make lint` — clean

### Task 8: Close out

- [ ] update `AGENTS.md` critical-patterns stage bullet (integration-tests) with the
      stage-3b contracts: Progress seam is UI-free and nil-guarded; port lease set is
      process-lifetime by design; parallel goroutines must never return errors into the
      errgroup (sibling cancellation hazard); aggregated display only at effective N>1 —
      N=1 stays byte-identical; `silentReporterFactory` serves both JSON and parallel
      text modes
- [ ] `make build` if AGENTS.md/docs changed in this task
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*No checkboxes — external follow-ups.*

- **Manual smoke** (needs a real project + Docker, interactive): on the beetDeck test
  project, `dwe test run --parallel 2` with 2-3 scenarios — verify block rows tick,
  finalize correctly, logs stay per-copy, no port collisions, Ctrl+C tears down in-flight
  runs; verify `dwe test run` (no flag) output is unchanged.
- **Vikunja task 170**: comment when stage 3b lands — the integration-tests spec is now
  fully implemented (all stages complete).

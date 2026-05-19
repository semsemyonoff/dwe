# Parallel step groups in pipeline

## Overview

Add a `parallel:` step-group container to the pipeline (deploy / lifecycle / reset). A group runs N independent sub-steps concurrently via `errgroup` with a configurable semaphore. Each sub-step is a regular `DeployStep` with every existing directive intact (`type`, `cmd`, `with`, `when`, `check`, `files_gate`, `continue_on_error`, `skip_confirm`). Concurrency lives **only in the orchestrator** — the step definition and its runners are unchanged in semantics.

The group is a content-neutral primitive: "run these N independent steps at once, wait, aggregate". It is not a builtin for any specific use case (dumps, migrations, composer, etc.).

### Problem solved

The executor today runs steps strictly sequentially. Correct as a default (declaration order is the only dependency contract), but it serialises independent IO-bound work:

- TBM pilot dump download: 3 dumps ≈ 2–3 min sequential vs. max-of ≈ 50 s
- Reset: per-volume removals, prune
- Deploy: composer install on N services, migrations on 4 independent DBs

### Integration

- YAML: a new `parallel:` block on a `DeployStep` is **mutually exclusive** with the leaf-only fields `type`, `cmd`, `with`, `check`, `files_gate`, `continue_on_error`. Allowed at group level: `name`, `description`, `when` (evaluated once before launching sub-steps), `skip_confirm` (inherited by sub-steps). Validated on `UnmarshalYAML`, with an explicit unknown-field allow-list to compensate for yaml.v3's `KnownFields(true)` bypass inside custom unmarshallers.
- Resolve: `ResolvePhaseSteps` recurses into `parallel.steps`, applies the same plan-time validation (builtin, files_gate, when classification), produces a `ResolvedStep.Parallel *ResolvedParallel`.
- Executor: top-level loop branches on `rs.Parallel`; the parallel path uses `errgroup.WithContext` + buffered channel semaphore. Sub-steps use the same per-step pipeline (when → files_gate → journal-skip → ExecAction → check) but are dispatched concurrently.
- Reporter: extended with `StartGroup` / `FinishGroup` / `SubStepOutput`. `PlainReporter` gains a thread-safe non-TTY path (buffered, dump-on-finish) and a TTY path via a new `internal/pipeline/parallel_view.go` bubbletea model.
- Logging: each sub-step gets its own log file under `.devbox/logs/parallel/<pipeline>/<group>/<sub>.log`. The global pipeline log keeps receiving everything (interleaved).
- Cancellation: requires propagating `context.Context` through the runner layer so `exec.CommandContext` can kill children on fail-fast or SIGINT. Sequential callers pass `context.Background()`.

### Non-goals for v1

- Nested `parallel:` inside `parallel:` — rejected at plan time.
- Per-substep interactive prompts (`confirmation: true` without `skip_confirm`) — rejected at plan time.
- DAG planning / `depends_on` between sub-steps — flat groups only.
- Auto-parallelisation flag (`--parallel`) without YAML opt-in — explicit only.
- Per-step "io / cpu / network" resource categories — single global `max_concurrent`.

## Context (from discovery)

### Files / components involved

Touched packages (confirmed against current code):

- `internal/config/devbox.go:155` — `DeployStep` (no custom UnmarshalYAML today; needs one)
- `internal/pipeline/step.go:24` — `ResolvedStep` struct
- `internal/pipeline/resolve.go:28` — `ResolvePhaseSteps(cfg, reg, phase, service)`
- `internal/pipeline/executor.go:344` — `RunWithOptions(opts)` main loop, `trackedTotal` counter at lines 356–410
- `internal/pipeline/reporter.go:22` — `Reporter` interface
- `internal/pipeline/plain.go:39` — `PlainReporter` (sole impl)
- `internal/pipeline/logging.go:37` — `OpenPipelineLog`; `childIO` at line 51; `stdoutIsTTY()` at line 29; PTY setup `pty.InheritSize` at line 70; `ExecAction` at line 126
- `internal/pipeline/recorder.go:10` — `Recorder` interface
- `internal/pipeline/file_recorder.go:17` — `FileRecorder` (flushes on every event via `journal.Save`)
- `internal/pipeline/print.go:12` — `PrintPlanTable(steps, w, devboxBin)`
- `internal/usercommands/runtime/runner.go:20` — `Runner` interface (`Run(ctx RunContext) error` — **no `context.Context` today**)
- `internal/usercommands/runtime/runner_host.go:22,115` — `DevboxRunner`, `HostRunner`
- `internal/usercommands/runtime/runner_service.go:63,104` — `ServiceExecRunner`, `ServiceRunRunner`
- `internal/usercommands/runtime/runner_script.go:33` — `ScriptRunner`
- `internal/usercommands/runtime/runner_workflow.go:28` — `WorkflowRunner`
- `internal/validate/config/` — deploy / lifecycle / reset validators (need parallel-group rules)
- `docs/reference/config/deploy.md`, `docs/reference/config/lifecycle.md` — schema docs

### Dependencies identified

`go.mod` already has:
- `charm.land/bubbletea/v2 v2.0.5`
- `charm.land/bubbles/v2 v2.1.0`
- `charm.land/lipgloss/v2 v2.0.3`
- `golang.org/x/sync v0.20.0` — **currently indirect**; importing `golang.org/x/sync/errgroup` in `internal/pipeline/executor.go` will promote it to a **direct** dependency. Run `go mod tidy` after Task 6 to reflect this in `go.mod`.

New dependencies introduced by this plan:
- `go.uber.org/goleak` (Task 9) — **external** test-only dependency for `goleak.VerifyTestMain`. Not in the standard library. Added as a `_test` import; if the team prefers to avoid the dependency, an alternative is a hand-rolled "count goroutines before/after" helper.

No new production dependencies beyond promoting `x/sync` to direct.

### Related patterns

- `condition.Condition` (typed alternative to legacy string-based `when:`) is the precedent for typed YAML structs with `UnmarshalYAML` shape-enforcement — mirror that style for `ParallelGroup`.
- `filesgate.FilesGate` shows how to keep a side-feature contained in its own parser-types package and run plan-time validation in a sub-package to avoid import cycles.

## Development Approach

- **Testing approach**: Regular (code first, then tests). Each task lands implementation + tests in the same task; tests are required deliverables, not optional.
- Each task ends with **all package tests passing** before the next task starts.
- Small, focused changes. No premature abstraction (e.g., no generic "parallel runner" interface beyond what the executor needs).
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task — unit tests for new and modified functions, success + error scenarios.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file if scope changes** — add ➕ tasks for discovered work, ⚠️ for blockers.
- Maintain backward compatibility: sequential pipelines must behave identically (same reporter calls, same journal state, same logs).
- gofmt + goimports; `make lint` clean before finishing.

## Testing Strategy

- **Unit tests**: required for every task. Table-driven where applicable (parser variants, validation rules, resolver inputs).
- **Concurrency tests**: `go test -race` on `internal/pipeline/...` once executor changes land. Cover fail-fast cancellation, semaphore cap, all-success, mixed continue-on-error.
- **TUI tests**: unit-test the `parallelGroupModel.Update` state transitions directly (without running `tea.Program`). One smoke test that runs the program with a recorded message sequence and asserts non-empty render is acceptable.
- **No e2e UI**: the CLI has no Playwright/Cypress harness — covered by integration tests in `internal/command/*_test.go` patterns where appropriate.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (checkboxes): code, tests, doc updates achievable in this repo.
- **Post-Completion** (no checkboxes): manual smoke runs on a real project, observability tweaks, follow-up tickets.

## Implementation Steps

### Task 1: Schema types and YAML parsing

Add `ParallelGroup` type and wire it onto `DeployStep` with `UnmarshalYAML` enforcing mutual exclusion and a hand-rolled unknown-field check. **Important yaml.v3 gotcha**: a custom `UnmarshalYAML` that calls `value.Decode(&alias)` **bypasses the parent decoder's `KnownFields(true)`** — unknown fields on `DeployStep` would silently round-trip. We must hand-validate the set of known keys.

Field categorisation on `DeployStep`:
- **Leaf-only** (rejected on a group step): `type`, `cmd`, `with`, `check`, `files_gate`, `continue_on_error`. These are sub-step concerns.
- **Group-only** (rejected on a leaf step): `parallel`.
- **Either** (allowed on both): `name`, `description`, `when`, `skip_confirm`. `when` on a group is evaluated once before any sub-step launches; if false the group is skipped wholesale (group counts as one step for phase-when purposes — see Executor task for details). `skip_confirm` on a group is **OR-merged** into every sub-step at resolve time — a sub-step inherits the group's `true` but cannot un-set it with its own `false` (monotonic).

Implementation:

- [x] add `type ParallelGroup struct { MaxConcurrent int; FailFast *bool; Steps []DeployStep }` in `internal/config/devbox.go` (yaml tags `max_concurrent`, `fail_fast`, `steps`)
- [x] implement `func (p *ParallelGroup) UnmarshalYAML(value *yaml.Node) error` with the **same allow-list pattern** as `DeployStep`: walk `value.Content` pairwise; reject any key not in `{max_concurrent, fail_fast, steps}` with the strict-decode-style error. Without this, typos like `max_concurent:` inside `parallel:` would silently pass because `value.Decode(&alias)` in the parent's `UnmarshalYAML` decodes nested mappings with `KnownFields(false)`. Decode into a private alias (`type rawParallelGroup ParallelGroup`) to avoid recursion.
- [x] add `Parallel *ParallelGroup` field on `DeployStep` (yaml `parallel,omitempty`)
- [x] implement `func (s *DeployStep) UnmarshalYAML(value *yaml.Node) error` that:
  - rejects non-`MappingNode` inputs with a clear error
  - walks `value.Content` pairwise to collect the set of declared keys
  - validates every declared key against an **explicit allow-list** (`name`, `description`, `type`, `cmd`, `with`, `when`, `check`, `files_gate`, `continue_on_error`, `skip_confirm`, `parallel`). Unknown keys → error matching the strict-decode style ("field X not found in type config.DeployStep"). This restores `KnownFields(true)` semantics that the custom unmarshal would otherwise lose.
  - decodes into a private alias type (`type rawDeployStep DeployStep`) to avoid `UnmarshalYAML` recursion
  - when `parallel` is present, rejects co-occurrence with **any** leaf-only field (`type`, `cmd`, `with`, `check`, `files_gate`, `continue_on_error`); clear error message naming the offending field
  - when `parallel` is absent and **none** of `type`/`cmd` is present, lets resolve/validate complain (current behaviour for invalid leaf steps)
  - **does NOT enforce `parallel.steps` length** — that check is deferred to `ResolvePhaseSteps` so it can return the `ErrEmptyParallelSteps` sentinel that the validator (Task 11) matches with `errors.Is`. The loader cannot import the resolve-package sentinel without creating a cycle, and shouldn't define its own duplicate. Single source of truth: resolve.
- [x] write table-driven tests in `internal/config/devbox_test.go`:
  - leaf step still parses (regression)
  - pure parallel parses
  - `parallel + type`, `parallel + cmd`, `parallel + with`, `parallel + check`, `parallel + files_gate`, `parallel + continue_on_error` each rejected (one row per field)
  - `parallel + when`, `parallel + skip_confirm`, `parallel + description`, `parallel + name` **accepted** (these are valid at group level)
  - empty / single-element `parallel.steps` **parses successfully** at the loader (the loader does not enforce length); the length check fires from `ResolvePhaseSteps` in Task 2 — verify there, not here
  - **unknown field on `DeployStep` rejected** (e.g. `typo_field: value`) — direct regression for the `KnownFields` bypass
  - **unknown field on `ParallelGroup` rejected** (e.g. `parallel: { max_concurent: 2, steps: [...] }`) — separate regression for the nested-mapping bypass; verify the error names the offending key (`max_concurent`)
  - nested parallel **parses successfully here** (validation is plan-time)
  - `ParallelGroup.FailFast` round-trips the tristate (`nil` / `true` / `false`)
- [x] run `go test ./internal/config/...` — must pass before next task

### Task 2: Plan-time resolve and validation

Recursively resolve sub-steps and reject configurations that the runtime cannot honour. Validation errors are exported sentinels so the `validate` package (Task 11) can match with `errors.Is` instead of string-matching.

- [ ] add `ResolvedParallel struct { MaxConcurrent int; FailFast bool; Steps []ResolvedStep }` in `internal/pipeline/step.go` and a `Parallel *ResolvedParallel` field on `ResolvedStep`
- [ ] define exported sentinel errors in `internal/pipeline/resolve.go`: `ErrNestedParallel`, `ErrUnnamedSubStep`, `ErrInteractiveInParallel`, `ErrEmptyParallelSteps`, `ErrDuplicateStepName` — wrap with context using `fmt.Errorf("...: %w", ErrXxx)` at the call site so callers retain detail while staying `errors.Is`-matchable
- [ ] extend `ResolvePhaseSteps` in `internal/pipeline/resolve.go` so that when a step has a non-nil `Parallel`:
  - recursively resolve each sub-step (template `when` evaluated, builtin/files_gate validated, runtime `when` classified)
  - default `MaxConcurrent` to `min(runtime.NumCPU(), len(steps))` when `≤ 0`; cap to `len(steps)` when larger
  - default `FailFast` to `true` when `nil`
  - **propagate group `skip_confirm` into each sub-step**: OR the group's `SkipConfirm` into each sub-step's `SkipConfirm` at resolve time (one assignment per sub-step; the executor then sees plain leaf-style sub-steps and needs no group lookup). The sub-step's own `skip_confirm: false` cannot un-set the inherited true — inheritance is monotonic OR, not override. Document this in the YAML reference.
  - return a single `ResolvedStep` with `Parallel` populated and **no** leaf-step fields
- [ ] enforce the rules — each returns the matching sentinel wrapped with offending step name:
  - sub-step has its own non-nil `Parallel` → `ErrNestedParallel`
  - sub-step has empty `Name` → `ErrUnnamedSubStep`
  - **sub-step names must be unique within the enclosing phase across all groups + leaf steps** → `ErrDuplicateStepName`. *Reason*: `FileRecorder` keys journal entries by `(phase, step.Name)` only (`internal/pipeline/file_recorder.go:167-265`), and the deploy skip-decider looks up steps by `rs.Step.Name` (`internal/command/deploy.go:465,479`). Two parallel groups in one phase both containing a sub-step named `download` would collide. Validate at plan time rather than re-keying the journal.
  - **interactive prompts in sub-steps**: reject any of the following without `skip_confirm: true` set on the sub-step (or inherited from the group) → `ErrInteractiveInParallel`. The DeployStep `type` is one of `shell|devbox|command|builtin` (`internal/config/action.go:45`); workflows are reached via `type: command` referencing a `CommandDef` whose `Type == CommandTypeWorkflow`:
    - sub-step `type: command` whose target `CommandDef.Confirmation == true` in `commands.yml`
    - sub-step `type: builtin` with `cmd: confirm` (the `confirm` builtin reads stdin)
    - sub-step `type: command` whose target `CommandDef.Type == CommandTypeWorkflow` and the workflow contains, **transitively**, any of:
      - a `WorkflowStep` with non-empty `Confirm` string (`internal/usercommands/model/types.go:240-242`) — this is the confirm-step shape, NOT `cmd: confirm`
      - a `WorkflowStep.Command` reference to a command whose `CommandDef.Confirmation == true`
      - a `WorkflowStep.Command` reference to another workflow (recurse; guard cycles with a visited set keyed by command ID)
      Skip the recursion entirely when registry is nil — same nil-tolerance pattern as gates.
  - `parallel.steps` < 2 → `ErrEmptyParallelSteps` (also covers the empty case from Task 1)
  - registry-nil tolerance preserved (gate / command / workflow-walk checks skipped, mirroring current behaviour)
- [ ] write table-driven tests in `internal/pipeline/resolve_test.go`: happy path with 2 sub-steps; defaulting of `MaxConcurrent` / `FailFast`; explicit `max_concurrent` capping; rejection of nested parallel; rejection of unnamed sub-step; rejection of duplicate sub-step names within a phase (cross-group collision, leaf+group collision); rejection of interactive confirmation via `confirmation: true`, via `builtin confirm`, and via workflow `WorkflowStep.Confirm` non-empty plus via recursive `WorkflowStep.Command` reference into a confirming command/workflow; acceptance when `skip_confirm: true` is set on either the sub-step or the parent group; **assert** that group-level `skip_confirm: true` flips each sub-step's resolved `SkipConfirm` to true (covers the OR inheritance). Each rejection test must assert `errors.Is(err, ErrXxx)`.
- [ ] run `go test ./internal/pipeline/...` (excluding executor tests that haven't been added yet) — must pass before next task

### Task 3: Context propagation through runners, builtins, and executor entry

Add `context.Context` as the **first parameter** to `Runner.Run`, `Builtin.Run`, and `ExecAction` so `exec.CommandContext` can be wired with proper cancellation. Storing `context.Context` in a struct is an anti-pattern; we pass it explicitly. Sequential callers pass `context.Background()` — behaviour unchanged.

Cancellation must reach **three** layers, not just one: shell-out runners (host/devbox/service/script), Go-side builtins (most notably `docker_wait_healthy` which polls in a loop), and the executor's SIGINT path.

#### 3.1 Runner layer (shell-out children)

- [ ] change the `Runner` interface in `internal/usercommands/runtime/runner.go` from `Run(rc RunContext) error` to `Run(ctx context.Context, rc RunContext) error`
- [ ] update every implementation — `HostRunner.Run`, `DevboxRunner.Run`, `ServiceExecRunner.Run`, `ServiceRunRunner.Run`, `ScriptRunner.Run`, `WorkflowRunner.Run` — and replace every `exec.Command(...)` with `exec.CommandContext(ctx, ...)`
- [ ] **also thread `ctx` through `BuildCommand`** (exported on each runner — e.g. `HostRunner.BuildCommand(rc) (*exec.Cmd, error)` at `runner_host.go:60`). The `*exec.Cmd` it returns is later started by `Run`; if `BuildCommand` constructs it with `exec.Command` (no ctx) then cancellation has no handle. Two acceptable shapes — pick one and apply consistently:
  - **(a) preferred** add `ctx context.Context` as the first parameter of every `BuildCommand`; constructors use `exec.CommandContext(ctx, ...)` and set `Cancel` / `WaitDelay`
  - **(b)** keep `BuildCommand` ctx-free and have `Run` attach ctx via a small `bindContext(cmd, ctx)` helper that sets `cmd.Cancel` / `cmd.WaitDelay` and rewrites `cmd.Cmd` if needed. Simpler-looking but `exec.CommandContext` is the idiomatic constructor; (a) is preferred.
- [ ] **update the package-level `RunCommand` entry points** so callers thread ctx through. Two functions both named `RunCommand`:
  - `internal/usercommands/runtime/runner.go:106` — internal entry: `func RunCommand(rc RunContext) error` → `func RunCommand(ctx context.Context, rc RunContext) error`
  - `internal/usercommands/usercommands.go:214` — exported facade re-exporting the above; same signature change
  Both must forward `ctx` into `Runner.Run`. `execCommandAction` in `internal/pipeline/logging.go` currently calls `usercommands.RunCommand(rctx)` — update to `usercommands.RunCommand(ctx, rctx)` once `ExecAction` takes ctx (3.3).
- [ ] set `cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }` and `cmd.WaitDelay = 5 * time.Second` on every spawned `*exec.Cmd` so cancellation sends SIGTERM first (giving children a chance to clean up), then SIGKILL after the delay. Required for graceful shutdown of `composer install`, `mariadb`, etc. Without `WaitDelay`, stdio-reader goroutines can leak after force-kill.

#### 3.2 Builtin layer (Go-side work)

- [ ] change `Builtin.Run(with map[string]any, ctx ExecContext) error` in `internal/builtin/builtin.go` to `Run(ctx context.Context, with map[string]any, ectx ExecContext) error` (rename the existing parameter to `ectx` to avoid shadowing `context.Context`); update every builtin implementation: `service_dirs_ensure`, `service_configs_copy`, `service_configs_check`, `message`, `confirm`, `docker_remove_project_volumes`, `docker_wait_healthy`, `remove_paths`
- [ ] in `docker_wait_healthy` (`internal/builtin/wait_healthy.go`), thread `ctx` into the poll loop — replace any `time.Sleep` with `select { case <-time.After(...): case <-ctx.Done(): return ctx.Err() }` so cancellation aborts the wait promptly
- [ ] in `docker_remove_project_volumes` and `remove_paths` (which iterate over many filesystem/docker operations), check `ctx.Err()` between iterations and return early on cancel
- [ ] update the dispatch helper `builtin.Run(name, with, ctx)` (`internal/builtin/builtin.go:104`) to take `ctx context.Context` as the new first parameter and forward it to the implementation's `Run`

#### 3.3 Executor entry (SIGINT plumbing)

- [ ] today `internal/pipeline/executor.go` installs **per-action** `signal.Notify` goroutines inside `execShellAction:156` and `execDevboxAction:193` that forward signals to the running child. These become redundant once `exec.CommandContext` + `cmd.Cancel` propagate cancellation. **Remove them** to avoid double-signalling and races; add a single `signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)` at the top of `RunWithOptions` (if `opts.Context` is nil) and use the returned context as the parent for all sub-execution. Document that callers who already wrap their own `signal.NotifyContext` should pass it via `opts.Context` to avoid double-wrapping.
- [ ] update `ExecAction` in `internal/pipeline/logging.go` to accept `ctx context.Context` as the first parameter and thread it into every `exec.CommandContext` (including the now-simplified `execShellAction` / `execDevboxAction`) and into builtin dispatch

#### 3.4 Callers and tests

- [ ] update every caller (`internal/command/`, `internal/lifecycle/`, `internal/deploy/`, `internal/reset/`, and the existing executor-level call sites) to thread `ctx` through; sequential callers start with `context.Background()`. The executor's parallel path overrides it in Task 6.
- [ ] update mocks/stubs in `internal/builtin/builtin_test.go`, `internal/usercommands/runtime/*_test.go` (specifically the many `*_BuildCommand_*` test files at `runner_host_test.go`, `runner_service_test.go`), `internal/pipeline/executor_test.go` to match the new signatures — `BuildCommand` tests will need to pass `context.Background()` as the new first arg
- [ ] write tests:
  - `internal/usercommands/runtime/runner_host_test.go`: cancelled context → runner returns promptly; child process is killed (use `sleep 30` under a 1 s deadline + `context.WithCancel`); SIGTERM-then-SIGKILL ordering verified with a trap-and-log shell script (PID writes to file before signal, file inspection after)
  - `internal/builtin/wait_healthy_test.go`: cancelled context aborts the poll loop within one tick interval (use `testing/synctest` to keep it deterministic)
- [ ] run `go test ./...` — must pass before next task (sequential semantics unchanged)

### Task 4: Concurrent-safe recorder and journal writes

The executor's parallel branch will call `Recorder.OnStepStart` / `OnStepFinish` / `OnStepFail` from N goroutines. `FileRecorder` writes `journal.Save` on each event → needs serialisation.

- [ ] add `sync.Mutex` to `FileRecorder` (`internal/pipeline/file_recorder.go`) and lock around every state-mutating method (`OnStepStart`, `OnStepFinish`, `OnStepFail`, `OnStepSkip`, `OnPipelineFinish`)
- [ ] document on the `Recorder` interface (`internal/pipeline/recorder.go`) that **implementations must be safe to call from multiple goroutines** once parallel groups land; update the `NopRecorder` accordingly (no-op, trivially safe — add a comment)
- [ ] add a stress test in `internal/pipeline/file_recorder_test.go`: launch 32 goroutines each calling `OnStepStart` + `OnStepFinish` with distinct step addresses; `journal.Load(path)` after must show all 32 entries (no lost writes, no truncated YAML)
- [ ] run `go test -race ./internal/pipeline/...` — must pass before next task

### Task 5: Reporter interface extension

Add group lifecycle and per-substep output streaming. PlainReporter gets thread-safety primitives but the new methods stay no-ops for now (filled in by Task 7/8).

- [ ] extend `Reporter` interface in `internal/pipeline/reporter.go` with:
  - `StartGroup(groupAddr string, group config.DeployStep, subIndices []int, total int)`
  - `FinishGroup(groupAddr string, group config.DeployStep, success bool)`
  - `SubStepOutput(subAddr string, line string)`
- [ ] add a `sync.Mutex` to `PlainReporter` and lock around `w.*` writes in **every** existing method (StartPipeline, EnterPhase, SkipPhase, StartStep, SkipStep, FinishStep, FailStep, FinishPipeline) — needed because parallel sub-step events will arrive from goroutines
- [ ] implement Task-5 stubs for the three new methods in `PlainReporter`: `StartGroup` / `FinishGroup` print a single header/footer line; `SubStepOutput` is a no-op (real impl in Task 8)
- [ ] update any test reporter (`mockReporter` in `executor_test.go`, etc.) to satisfy the extended interface; record the new events
- [ ] write tests in `internal/pipeline/plain_test.go` (or nearest) that the new methods are concurrency-safe: 16 goroutines hammering `StartStep`/`FinishStep`/`SubStepOutput` simultaneously must not race (`go test -race`)
- [ ] run `go test -race ./internal/pipeline/...` — must pass before next task

### Task 6: Executor — `executeParallelGroup`

Wire concurrency into the executor. This is the core of the feature.

- [ ] in `internal/pipeline/executor.go`, factor the per-step body of `RunWithOptions` into a private `executeSequentialStep(ctx context.Context, opts RunOptions, rs ResolvedStep, idx int) error` helper so the same logic can be invoked per sub-step. Keep the existing top-level loop, but branch:
  ```go
  for _, rs := range opts.Steps {
      if rs.Parallel == nil {
          executeSequentialStep(ctx, opts, rs, idx)
      } else {
          executeParallelGroup(ctx, opts, rs, indices)
      }
  }
  ```
  (Go 1.22+ scopes the loop variable per iteration; **no `i, sub := i, sub` shadow needed** inside `eg.Go`.)
- [ ] implement `executeParallelGroup(parentCtx context.Context, opts RunOptions, rs ResolvedStep, ...)` using `errgroup.WithContext(parentCtx)` plus `eg.SetLimit(rs.Parallel.MaxConcurrent)` — replaces a manual `chan struct{}` semaphore and is the idiomatic Go 1.20+ form
- [ ] **group-level `when` handling**: before `StartGroup`, evaluate the group's `When` exactly once using the same evaluator as a leaf step (template at plan time → already resolved; runtime classifier → eval here). On false:
  - emit the **existing reporter contract sequence** for the group as a unit: `StartStep(groupAddr, …)` followed by `SkipStep(groupAddr, …, reason)`. The reporter ordering contract in `internal/pipeline/reporter.go:11-17` requires `StartStep` before any of `FinishStep|FailStep|SkipStep`, and the current executor consistently emits both even on skips (`executor.go:417-418, 430-431, 533-534, 544-545`). Group skips must follow the same shape — do **not** invent a new contract slot. `PlainReporter`'s existing sequential SkipStep handler already prints `· [N/M] …` then `◎ [N/M] Skipped…` and works unchanged for a group address.
  - **emit per-sub-step `Recorder.OnStepSkip` events** so the journal correctly records each sub-step as skipped with reason `"parent group when=false"` (without these, on the next run the journal would treat sub-steps as never-attempted and the SkipDecider could mis-decide). Recorder events are independent of reporter events and have no StartStep prerequisite — OnStepSkip alone is sufficient.
  - skip the entire group (do not call `StartGroup` / spawn anything / call `FinishGroup`)
  - increment the step counter by `len(rs.Parallel.Steps)` (the group's pre-assigned `subIndices` consumed deterministically as skipped); the single `StartStep`/`SkipStep` pair uses the group's leading index (e.g. `[12/25]` for a group occupying `12-14`)
  On true, proceed with `StartGroup`.
- [ ] **phase-level `when=false` covering a parallel group**: the existing phase-skip path (`executor.go:389-398`) sets `phaseSkipped = true` and emits `SkipPhase` once, then for each step in the phase the main loop emits `StartStep`+`SkipStep`. For a `rs.Parallel != nil` element in a skipped phase, the existing StartStep+SkipStep already follow the reporter contract — keep them as-is — but the per-sub-step `Recorder.OnStepSkip` events are missing (same journal-correctness bug as group-when). Add explicit handling: when `phaseSkipped && rs.Parallel != nil`, after the existing StartStep+SkipStep, also iterate `rs.Parallel.Steps` and emit `Recorder.OnStepSkip` with reason `"parent phase when=false"` for each, and advance `trackedIndex` by `len(rs.Parallel.Steps)` (not 1) so the `[N/M]` counter stays correct downstream.
- [ ] **fix `trackedTotal` computation** (`executor.go:356-361`): today it does `trackedTotal++` per top-level `rs := range opts.Steps`. Because `ResolvePhaseSteps` returns a single `ResolvedStep` for an entire parallel group, the current loop counts a group as 1 step regardless of how many sub-steps it holds. Replace with a helper that walks each top-level `rs`: if `rs.Phase.Untracked` skip; else if `rs.Parallel != nil` add `len(rs.Parallel.Steps)`; else add 1. Use this `trackedTotal` for both `StartPipeline` and every `(stepIndex, stepTotal)` tuple in reporter calls.
- [ ] step-counter handling: pre-assign sub-step indices in declaration order **before** spawning; report them through `StartGroup(addr, step, subIndices, trackedTotal)` — note: **`trackedTotal`, not `len(opts.Steps)`**, since opts.Steps counts a group as one element. Each entry in `subIndices` is the per-sub-step `trackedIndex`, computed by reserving `len(rs.Parallel.Steps)` consecutive indices for the group before spawning.
- [ ] per sub-step: `phase-when` and **`group-when`** are inherited from the group; do **not** re-evaluate. Run `step-when` → `files_gate` probe → `SkipDecider` (only if `FilesGate == nil`) → `ExecAction` → `check`. Same ordering as the existing sequential path.
- [ ] cancellation: when `FailFast` is true and any sub-step errors, `errgroup` cancels `ctx`; the other goroutines observe `ctx.Done()` and `exec.CommandContext` kills their children (SIGTERM via `cmd.Cancel` from Task 3, SIGKILL after `WaitDelay`).
- [ ] error aggregation: when `FailFast` is false, collect all sub-step errors under a `sync.Mutex`-guarded slice and return `errors.Join(...)`. **Wrap each error with sub-step address before joining**: `fmt.Errorf("parallel sub-step %q: %w", subAddr, err)` — otherwise the joined error loses which sub-step failed. A sub-step with `ContinueOnError: true` does not count as a failure for the group.
- [ ] emit `FinishGroup(addr, step, success)` after `eg.Wait()`
- [ ] update `RunOptions` to accept an optional `Context context.Context` field (default `context.Background()`); the parallel path uses it as `parentCtx`; sequential path is unchanged
- [ ] write tests in `internal/pipeline/executor_test.go`:
  - happy path: 3 sub-steps run, all FinishStep events emitted, FinishGroup emitted with `success=true`
  - **group `when=false`**: group is skipped wholesale — **StartStep then SkipStep** for the group (reporter contract preserved), zero StartGroup/FinishGroup, three OnStepSkip recorder events (one per sub-step, reason `"parent group when=false"`); step counter advances by 3 (the StartStep/SkipStep pair uses the leading index of the reserved block, e.g. `[12/25]`)
  - **phase `when=false` covering a group**: SkipPhase once, **StartStep then SkipStep** for the group, three OnStepSkip recorder events (reason `"parent phase when=false"`); `trackedIndex` advances by `len(group.Steps)` (verify by inspecting the index passed to a following non-skipped step)
  - **`trackedTotal` correctness**: a phase with 2 leaf steps + a parallel group of 3 sub-steps reports `total=5` on `StartPipeline` and every `[N/5]` index call (not `total=3`, which would be the wrong "groups-as-one" count)
  - **group-inherited `skip_confirm`**: a `confirm` builtin sub-step under a `skip_confirm: true` group runs to completion without prompting (proves the OR merge from Task 2 reached the runtime)
  - semaphore cap: `MaxConcurrent=1` serialises 3 sub-steps (assert via a shared counter that never exceeds 1 in flight — use atomic + barriers)
  - fail-fast: 1 of 3 sub-steps fails immediately → remaining 2 are cancelled (assert FinishGroup `success=false` and at most 1 FinishStep emitted). Use `testing/synctest` (Go 1.24+) for deterministic cancellation timing, falling back to bounded `time.After` if the helper proves awkward.
  - fail-fast disabled: 1 of 3 fails → other 2 complete; FinishGroup `success=false`; joined error string contains every failing sub-step's address
  - `continue_on_error` on the failing sub-step → group still success
  - files_gate on sub-step gates correctly (skips the sub-step, group continues)
  - SkipDecider applied per sub-step when gate is nil
- [ ] run `go test -race ./internal/pipeline/...` — must pass before next task

### Task 7: Per-substep log routing

Each sub-step gets its own log file; the global pipeline log keeps interleaved output.

- [ ] add `OpenSubStepLog(workDir, pipelineName, groupName, subStepName string, enabled bool) (io.WriteCloser, string, error)` in `internal/pipeline/logging.go` — derives `.devbox/logs/parallel/<pipeline>/<group>/<sub>.log` (sanitise names via existing helpers or a new `sanitizeForFS`)
- [ ] in `executeParallelGroup`, before each sub-step's `ExecAction`, open its sub-log; pass a **nil-filtered multi-writer** as the `LogWriter` in the sub-step's `ActionContext`. Introduce a small helper `joinWriters(ws ...io.Writer) io.Writer` in `internal/pipeline/logging.go` that drops nil entries and returns:
  - `io.Discard` if all entries are nil (defensive — should not happen in parallel mode since `lineTee` is always non-nil)
  - the single non-nil writer if exactly one remains
  - `io.MultiWriter(...)` over the non-nil set otherwise
  Required because (a) `globalLogWriter` from `OpenPipelineLog` is `nil` when pipeline logging is disabled (`logging.go:37`), and (b) `subLogWriter` is `nil` when sub-step logging is disabled (same `enabled` flag flow). `io.MultiWriter` does not tolerate nil writers — it panics at first `Write`. `lineTee` is always non-nil (it's what feeds the reporter buffer). The helper guarantees `lineTee` is always included.
- [ ] guarantee the sub-log file is closed on every exit path (success, fail, skip, cancel) — use a per-substep helper to avoid `defer` in loop
- [ ] add `SubStepOutput` plumbing: in parallel mode, also tee stdout/stderr **line-by-line** to the reporter via `SubStepOutput(subAddr, line)`. Reuse the existing ANSI stripper; introduce a small `lineReader(io.Reader, func(string))` helper that splits on `\n`
- [ ] **route builtin output through the same plumbing**: builtins write through `ExecContext.Output *render.Writer` (`internal/builtin/builtin.go:38-44`), not `childIO`. Add an `Output io.Writer` field on `ActionContext` (or wire through the existing `LogWriter`) and have `internal/pipeline/logging.go`'s builtin dispatch construct an `ExecContext.Output` that wraps the sub-step's tee writer. Without this, sub-step builtins (`docker_wait_healthy`, `message`, etc.) would print to the host terminal directly and bypass the reporter buffer entirely.
- [ ] **`childIO` must take a parallel-mode branch that never writes to `os.Stdout` / `os.Stderr`**. Today's `childIO` returns `(os.Stdout, os.Stderr, noop)` when `logWriter == nil` and a `MultiWriter(os.Stdout, …)` even when `logWriter != nil`. In parallel mode that direct `os.Stdout` write would (a) interleave with the non-TTY reporter's buffered dump, breaking output ordering, and (b) overwrite the TTY bubbletea live view. Required behaviour in parallel mode:
  - stdout/stderr go **only** to the writers the executor supplies (per-substep log file + line tee that feeds `Reporter.SubStepOutput`)
  - no path that writes to `os.Stdout` / `os.Stderr` directly
  - PTY is never allocated (terminal display is the reporter's job)
  Add an explicit `Parallel bool` field on `ActionContext`; in `childIO`, when `actx.Parallel` is true, ignore `os.Stdout`/`os.Stderr` and route only to the supplied tee. Document that in parallel mode `LogWriter` is **required** (executor passes one unconditionally; assert at the top of `childIO`).
- [ ] write tests in `internal/pipeline/logging_test.go`:
  - `OpenSubStepLog` creates the right path and sanitises unsafe characters
  - per-substep log contains the substep's output and nothing from siblings (covers both shell and builtin sub-steps)
  - `message` builtin output for a parallel sub-step ends up in the per-substep buffer and the per-substep log file, not on the host terminal
  - global pipeline log contains all sub-step output (interleaving acceptable)
  - PTY is not allocated when `Parallel=true`, even on TTY stdout
  - **regression**: under `Parallel=true`, `childIO` never returns `os.Stdout` / `os.Stderr` (use a small wrapper that captures `os.Stdout`/`os.Stderr` and asserts no writes during the sub-step's execution); `LogWriter == nil` while `Parallel=true` is a programmer error and panics or returns an obvious error
  - **`joinWriters` helper**: all-nil → `io.Discard`; one non-nil → that writer; multiple → MultiWriter; **regression case**: `joinWriters(nil, nil, lineTee)` writes to `lineTee` without panicking (this is the disabled-logging scenario that motivated the helper)
- [ ] run `go test ./internal/pipeline/...` — must pass before next task

### Task 8: Non-TTY PlainReporter mode (buffered + dump-on-finish)

Realise the non-TTY path: buffer sub-step output in memory and per-file log; on `FinishStep`/`FailStep` of a sub-step, dump the buffer between separator lines. Order of completion is allowed to vary.

- [ ] add a per-substep buffer map to `PlainReporter` (`map[subAddr]*bytes.Buffer`) protected by the mutex from Task 5; initialise the map lazily in `StartGroup` (writing to a nil map panics — guard explicitly)
- [ ] `SubStepOutput(subAddr, line)` appends to the buffer (do **not** write to terminal directly); if the entry is missing (e.g. `StartGroup` was skipped due to an upstream bug), create it on the fly rather than panic
- [ ] `StartStep` for a parallel sub-step prints the standard `· [N/M] ...` line immediately (so users see what was launched)
- [ ] `FinishStep` / `FailStep` / `SkipStep` for a parallel sub-step prints the status line **followed by** the buffered output between `───── output ─────` / `──────────────────` separators, then frees the buffer
- [ ] `FinishGroup` prints a one-line summary with elapsed time and success/fail counts
- [ ] introduce a `mode` flag on `PlainReporter` (TTY vs non-TTY) decided once at construction via `stdoutIsTTY()`; this task implements only the non-TTY branch
- [ ] write tests in `internal/pipeline/plain_test.go`:
  - given `SubStepOutput` lines followed by `FinishStep`, the writer receives the expected formatted block with separators
  - sub-steps completing in interleaved order each get their own correct block
  - `FailStep` block includes the captured error after the buffer dump
  - `SkipStep` block prints reason and does not dump buffered output (none expected)
- [ ] run `go test ./internal/pipeline/...` — must pass before next task

### Task 9: TTY PlainReporter mode (bubbletea live view)

The TTY path renders a live spinner block for the active group, then dumps post-factum after `FinishGroup`.

- [ ] new file `internal/pipeline/parallel_view.go` with:
  - `parallelGroupModel` struct: per-substep state (`spinner.Model`, last-line, status `running|done|failed|skipped`, index, name)
  - `tea.Msg` types: `subStepOutputMsg{addr, line}`, `subStepDoneMsg{addr, ok, err}`, `subStepSkipMsg{addr, reason}`, `groupDoneMsg`
  - `Init() tea.Cmd` starts spinners
  - `Update(msg tea.Msg) (tea.Model, tea.Cmd)` handles the messages
  - `View() string` renders with `lipgloss/v2` (one line per sub-step: `<spinner> [idx/total] <name>: <last-line>`)
- [ ] integrate in `PlainReporter` TTY branch:
  - `StartGroup` launches a goroutine running `tea.NewProgram(model).Run()`; output events feed it via a channel (`Send`)
  - `FinishGroup` sends `groupDoneMsg`, waits for `Run()` to return, then prints the per-substep summary lines (✓/✗/◎ with elapsed)
  - the global per-substep log still receives raw output; tea view only displays the latest line
  - synchronise the bubbletea program lifecycle with the reporter mutex (acquire only when manipulating shared state — do not hold it across `Run()`)
- [ ] interactive children are forbidden in parallel mode (no PTY, no `huh.Confirm`); rely on the Task 2 validation. `SuspendForExec`/`ResumeAfterExec` are unused in the parallel path (sequential path keeps them).
- [ ] write tests in `internal/pipeline/parallel_view_test.go`:
  - `parallelGroupModel.Update` transitions: starting → output → done; starting → skip; starting → fail
  - `View()` renders non-empty content for each state
  - (optional) `teatest`-style smoke that pipes a recorded message sequence through the program and asserts the final render — only if it doesn't bloat dependencies; otherwise stick to model-level tests
- [ ] add `goleak.VerifyTestMain(m, …)` to `internal/pipeline/main_test.go` (create if absent) — the bubbletea program runs in its own goroutine and must exit cleanly on `groupDoneMsg`; without goleak a hung tea-loop is invisible to CI. `go.uber.org/goleak` is a **new external test-only dependency** (not stdlib); add it via `go get -t go.uber.org/goleak` and reflect in `go.mod` / `go.sum`.
- [ ] run `go test ./internal/pipeline/...` — must pass before next task

### Task 10: Plan output rendering

`devbox deploy plan` should render parallel groups so the structure is visible.

- [ ] extend `PrintPlanTable` in `internal/pipeline/print.go` to handle `rs.Parallel != nil`:
  - header line: `[parallel group: <name> (<n> steps, max_concurrent=<m>, fail_fast=<bool>)]`
  - indent and render each sub-step underneath using the same per-step formatter (command, [when], [files_gate], [check], [continue_on_error])
  - index display: contiguous range like `[12-14/25]` for the group, then individual `[12/25]` / `[13/25]` / `[14/25]` for the sub-step lines
- [ ] update `internal/deploy/print.go` if it has its own print-path layer; otherwise it inherits `PrintPlanTable`
- [ ] write a golden-style test in `internal/pipeline/print_test.go` (or table-driven on the string output): given a resolved plan with a mix of sequential + parallel, the rendered string contains the expected lines in order
- [ ] run `go test ./internal/pipeline/... ./internal/deploy/...` — must pass before next task

### Task 11: Validator coverage

Surface the new rules in `devbox validate` so users see them before `deploy`.

- [ ] in `internal/validate/config/`, extend the deploy validator (and lifecycle, reset where applicable) to emit diagnostics for:
  - nested `parallel:` (error, hint: "flat parallel groups only in v1") — match `errors.Is(_, ErrNestedParallel)`
  - `parallel.steps` < 2 (error, hint: "use a leaf step if only one item") — `ErrEmptyParallelSteps`
  - sub-step missing `name` (error) — `ErrUnnamedSubStep`
  - **duplicate sub-step names within a phase** (error, hint: "rename to a unique value within the phase") — `ErrDuplicateStepName`
  - interactive prompts without `skip_confirm: true` (error, hint: "set skip_confirm: true or restructure"), covering all three sources from Task 2: `confirmation: true` in target command, `builtin confirm`, and `workflow` recursively containing a confirm step — all flagged via `ErrInteractiveInParallel`
  - sub-step uses `service_run` with TTY-allocating compose args (warning, hint: "TTY allocation is not available in parallel sub-steps")
  - group co-occurring with leaf-only directives `check` / `files_gate` / `continue_on_error` (error, surfaced through Task 1's `UnmarshalYAML` but also flagged here in case YAML decoded outside the loader)
- [ ] ensure validators self-skip cleanly when registry is nil (existing pattern)
- [ ] write table-driven tests in `internal/validate/config/deploy_test.go` (and equivalents) covering each new rule
- [ ] run `go test ./internal/validate/...` — must pass before next task

### Task 12: Verify acceptance criteria and update docs

Finalise: docs, lint, full suite, real-project smoke.

- [ ] add a "Parallel step groups" section to `docs/reference/config/deploy.md` covering: YAML schema, defaults, semantics (fail_fast, cancellation, journal-per-substep), restrictions (no nesting, no confirmation, no PTY), reporter behaviour (TTY vs non-TTY), cancellation contract (SIGTERM → SIGKILL via `cmd.Cancel` + `WaitDelay`)
- [ ] cross-reference from `docs/reference/config/lifecycle.md` and `docs/reference/config/reset.md` (the feature applies to all three pipelines)
- [ ] update `AGENTS.md` package summaries to mention `ParallelGroup` / `ResolvedParallel` / new reporter methods / `internal/pipeline/parallel_view.go`
- [ ] verify SIGINT propagation end-to-end: from a separate terminal, `kill -INT <pid>` of an in-progress parallel deploy; confirm (a) `signal.NotifyContext` in `RunWithOptions` cancels the parent `ctx`, (b) each shell-out child receives SIGTERM (via `cmd.Cancel`), then SIGKILL after `WaitDelay`, (c) the `docker_wait_healthy` builtin aborts its poll loop within one tick, (d) the executor returns `context.Canceled` cleanly without orphaned `docker compose` / `sleep` processes (verify with `pgrep -P $$` before and after)
- [ ] run `make lint` — all issues must be fixed
- [ ] run `make test` and `go test -race ./internal/pipeline/...` — full suite green
- [ ] verify all task checkboxes above are marked `[x]`

## Technical Details

### YAML schema

```yaml
phases:
  - name: init
    steps:
      - name: db-dumps
        parallel:
          max_concurrent: 4              # optional; default = min(NumCPU, len(steps))
          fail_fast: true                # optional; default true
          steps:                         # required, ≥ 2
            - name: download-main
              type: command
              cmd: services.main.db.dump-download
              files_gate: { ... }
            - name: download-stock
              type: command
              cmd: services.stock.db.dump-download
            - name: download-price
              type: command
              cmd: services.price.db.dump-download
```

### Go types

```go
// internal/config/devbox.go
type DeployStep struct {
    Name            string               `yaml:"name"`
    Description     string               `yaml:"description,omitempty"`
    Type            string               `yaml:"type,omitempty"`
    Cmd             string               `yaml:"cmd,omitempty"`
    With            map[string]any       `yaml:"with,omitempty"`
    When            *condition.Condition `yaml:"when,omitempty"`
    Check           *Action              `yaml:"check,omitempty"`
    FilesGate       *filesgate.FilesGate `yaml:"files_gate,omitempty"`
    ContinueOnError bool                 `yaml:"continue_on_error,omitempty"`
    SkipConfirm     bool                 `yaml:"skip_confirm,omitempty"`
    Parallel        *ParallelGroup       `yaml:"parallel,omitempty"`
}

type ParallelGroup struct {
    MaxConcurrent int          `yaml:"max_concurrent,omitempty"`
    FailFast      *bool        `yaml:"fail_fast,omitempty"`
    Steps         []DeployStep `yaml:"steps"`
}

// internal/pipeline/step.go
type ResolvedStep struct {
    // existing fields ...
    Parallel *ResolvedParallel
}

type ResolvedParallel struct {
    MaxConcurrent int
    FailFast      bool
    Steps         []ResolvedStep
}
```

### Reporter extension

```go
type Reporter interface {
    // existing methods unchanged ...

    StartGroup(groupAddr string, group config.DeployStep, subIndices []int, total int)
    FinishGroup(groupAddr string, group config.DeployStep, success bool)
    SubStepOutput(subAddr string, line string)
}
```

Implementations MUST be safe for concurrent calls to `StartStep` / `FinishStep` / `FailStep` / `SkipStep` / `SubStepOutput` (sub-step events arrive from N goroutines). `PlainReporter` uses a single `sync.Mutex`.

### Concurrency model

> **Illustrative core only.** The sketch below shows the errgroup/cancel/error-aggregation skeleton. It deliberately omits group-`when` evaluation, phase-skip handling for groups, and recorder bookkeeping — those are spelled out in Task 6. Task 6 is the source of truth; if the sketch and Task 6 disagree, Task 6 wins.

```go
// internal/pipeline/executor.go (sketch — Go 1.26 — core dispatch only)
func executeParallelGroup(parentCtx context.Context, opts RunOptions, rs ResolvedStep, subIndices []int, trackedTotal int) error {
    // Caller is responsible for:
    //   - evaluating rs.Step.When (group-when) and skipping wholesale on false
    //   - emitting per-sub-step Recorder.OnStepSkip when phase or group when=false
    //   - reserving subIndices (contiguous trackedIndex block of len(rs.Parallel.Steps))

    eg, ctx := errgroup.WithContext(parentCtx)
    eg.SetLimit(rs.Parallel.MaxConcurrent) // idiomatic replacement for chan-based semaphore

    opts.Reporter.StartGroup(groupAddr, rs.Step, subIndices, trackedTotal) // trackedTotal, NOT len(opts.Steps)

    var (
        groupErrs []error
        errsMu    sync.Mutex
    )

    for i, sub := range rs.Parallel.Steps {
        // No `i, sub := i, sub` shadow needed — Go 1.22+ scopes loop vars per iteration.
        eg.Go(func() error {
            if ctx.Err() != nil {
                return ctx.Err() // cancelled before slot acquired
            }
            subAddr := fmt.Sprintf("%s/%s", groupAddr, sub.Step.Name)
            err := executeSequentialStep(ctx, opts, sub, subIndices[i], trackedTotal)
            if err == nil || sub.Step.ContinueOnError {
                return nil
            }
            wrapped := fmt.Errorf("parallel sub-step %q: %w", subAddr, err)
            if rs.Parallel.FailFast {
                return wrapped // errgroup cancels ctx, kills siblings via cmd.Cancel
            }
            errsMu.Lock()
            groupErrs = append(groupErrs, wrapped)
            errsMu.Unlock()
            return nil
        })
    }

    var err error
    if rs.Parallel.FailFast {
        err = eg.Wait()
    } else {
        _ = eg.Wait() // never returns first-error in this mode
        err = errors.Join(groupErrs...)
    }

    opts.Reporter.FinishGroup(groupAddr, rs.Step, err == nil)
    return err
}
```

Notes:
- `errgroup.SetLimit` replaces the manual `chan struct{}` semaphore (Go 1.20+).
- Loop-var shadow `i, sub := i, sub` is **unnecessary** in Go 1.22+ — current module is Go 1.26.
- Each failing sub-step is wrapped with its address so `errors.Join` preserves identity.
- `cmd.Cancel` + `cmd.WaitDelay` (Task 3) deliver graceful SIGTERM → SIGKILL to children on `ctx` cancellation.
- `trackedTotal` is computed by the top-level loop (Task 6) — a group contributes `len(rs.Parallel.Steps)`, not 1. Do **not** use `len(opts.Steps)` here.
- Group-when and phase-when handling, plus per-sub-step `Recorder.OnStepSkip` for whole-group skips, live in the top-level loop **before** this function is ever called.

### Journal semantics

- Each sub-step is journaled independently. `journal.StepHash(step)` is computed from the sub-step itself; the group name is not part of the hash. This lets users reorder / add sub-steps without invalidating siblings.
- The group itself is **not** journaled (no extra hash beyond the union of its sub-steps).
- **Name uniqueness is enforced at plan time** because `FileRecorder` keys journal entries by `(phase, step.Name)` only (`internal/pipeline/file_recorder.go:167-265`) and the deploy skip-decider looks up steps by `rs.Step.Name` (`internal/command/deploy.go:465,479`). Two parallel groups in one phase with sub-steps named `download` would collide on save and would cause one to incorrectly skip on resume. Plan-time rejection (`ErrDuplicateStepName`, Task 2) avoids needing to re-key the journal by group path — a much smaller diff than reworking the storage layout.

### Open risks tracked as ⚠️ during implementation

- Concurrent journal writes (Task 4 mitigates via mutex; verify with race tests)
- `composer install` etc. ignoring SIGINT — out of scope; documented as a v1 limitation
- TTY-allocating sub-commands (`docker compose run -it`) — flagged as a `validate` warning (Task 11). At runtime they are **not refused**; `childIO` simply does not allocate a PTY in parallel mode (Task 7), so the child runs with pipe-backed stdio. The child may then exit non-zero with a "cannot allocate tty" message; that surfaces as a normal sub-step failure subject to `continue_on_error` / `fail_fast`. Documented as a v1 limitation — full pre-flight refusal would require parsing arbitrary `compose_args`, which is out of scope.

## Post-Completion

*Items requiring manual intervention or external systems — informational only*

**Manual verification on a real project**:
- Run the TBM-pilot deploy with a parallel group of 3 dump downloads; confirm the live spinner block on TTY and the buffered-dump output on `CI=1`.
- Trigger SIGINT mid-group; verify all in-flight children are killed and per-substep log files contain truncated-but-readable output.
- Run with `fail_fast: false` and one intentionally-failing sub-step; verify the other sub-steps complete and the group reports failure.
- Inspect `.devbox/deploy/state.yml` after a parallel run; verify each sub-step has a distinct journal entry with the right `step_hash` and status.

**External system updates**:
- Update consuming projects' deploy YAMLs that have been waiting for this feature (TBM pilot at minimum).
- Mention the feature in the next devbox release notes / CHANGELOG.

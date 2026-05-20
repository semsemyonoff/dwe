# Workflow `parallel:` sub-steps

## Overview

Add a `parallel:` block to the `workflow` command type so a workflow can fan out a group of sub-steps concurrently — mirroring the existing pipeline `parallel:` schema, with the same restrictions and the same live-UI affordances.

**Problem solved.** Today parallel execution is available **only** in pipeline contexts (`deploy.yml` / `lifecycle.yml` / `reset.yml`). Users can't:

- run "do X for all services" ad-hoc from the CLI (`devbox commands run services.all.composer-install`),
- reuse a parallel pattern across multiple pipelines without duplicating the block,
- compose workflow A that calls workflow B where B should run concurrently.

The current workaround — hiding the fan-out in a `type: shell` script — defeats devbox's observability (no per-sub-step logs, no live UI, no cancellation contract).

**Approach in one sentence.** Mirror the pipeline `parallel:` schema as `WorkflowStep.Parallel`; promote `LiveLine`/`LiveBlock` out of `internal/pipeline/` into a shared `internal/liveui/` package; the workflow runner self-constructs its own LiveLine for the duration of any parallel block, so the same live-block UI renders whether the workflow is invoked ad-hoc from the CLI OR as a sequential step inside a pipeline (pipeline's LiveLine is already paused during sequential-step bodies via `Reporter.SuspendForExec`, leaving the terminal to the workflow's LiveLine).

**Pipeline-embedded composition.** When a pipeline sequential step's `cmd:` resolves to a workflow that contains a `parallel:` block, the user sees the workflow's parallel block rows rendered INSIDE the surrounding pipeline step — not as separate pipeline `[N/M]` items. The pipeline-step counter does NOT advance per sub-step; only the single pipeline step covering the workflow is tracked. Sub-step rows use within-workflow indices (`[1/4]`, `[2/4]`, etc.). The composition works for ANY sequential pipeline context (deploy, lifecycle, reset).

**Forbidden composition.** A workflow that contains a `parallel:` block cannot be invoked as a sub-step of a pipeline `parallel:` group OR a workflow `parallel:` group (no nested block UIs on the same terminal — there is only one LiveBlock owner). This is detected at runtime via a `RunContext.UnderParallel` marker propagated by both engines.

## Context (from discovery)

### Files / components involved

- `internal/usercommands/runtime/runner_workflow.go:29-65` — `WorkflowRunner.Run`. Loop iterates `rc.Cmd.Steps`, dispatches to `runConfirmStep` (line 48) or `runCommandStep` (line 54). Branches will gain a third case for `step.Parallel`.
- `internal/usercommands/model/types.go:235-249` — `WorkflowStep` struct. Current fields: `Command`, `With`, `Confirm`, `When`, `ContinueOnError`. `Validate()` at lines 253-267 enforces exactly one of Command/Confirm, no With on confirm, etc. — we extend it for `Parallel`.
- `internal/usercommands/registry/registry.go:55-101` — `Registry.Get` is a plain map read with no mutex; the registry is immutable after `LoadRegistry`. Concurrent reads from N goroutines are safe.
- `internal/tpl/render_command.go:22-33` and `internal/usercommands/runtime/build_context.go:56-61` — `RenderContext` is constructed per-step via `tpl.RenderContext{Raw, Params, Context, Files, Host}`. `runner_workflow.go:126-133` builds a fresh `renderCtx` per workflow sub-step, so the parallel goroutines won't share mutable template state.
- `internal/pipeline/executor.go:805` — `executeParallelGroup`. Template for the workflow-side dispatch: errgroup + `SetLimit(MaxConcurrent)`, FailFast tristate, cancellation via `context.Context`.
- `internal/pipeline/logging.go:148` — `OpenSubStepLog(workDir, pipelineName, groupName, subStepName, enabled)`. Reusable as-is for workflow per-sub-step logs (`pipelineName="workflow"`, `groupName=<workflow-id>`).
- `internal/pipeline/liveline.go` — LiveLine + LiveBlock; currently bound to `internal/pipeline/`. Public methods we need from workflow: `StartBlock`, `SetBlockRowRunning`, `SetBlockRowFinal`, `EndBlock`, `Pause`, `Resume`. Type `BlockRowKind` and constants `BlockRowDone`/`BlockRowFailed`/`BlockRowSkipped` ditto.
- `internal/pipeline/plain.go` — single existing consumer of LiveLine. Glyph constants (`iconDone`, `iconFailed`, `iconSkipped`, `iconRunning`) and `finalGlyph()` live here / in `liveline.go`. After promotion, plain.go imports from `internal/liveui/`.
- `internal/command/command_cmd.go:125-217` — cobra `commands run` entrypoint. Currently constructs a `RunContext` and calls `usercommands.RunCommand(cmd.Context(), rctx)`. **No `signal.NotifyContext`** — Ctrl-C is handled by cobra's default cancellation. For workflow-parallel we need explicit `SIGINT`/`SIGTERM` propagation so children get SIGTERM via `cmd.Cancel` + `WaitDelay`.

### Related patterns found

- Pipeline parallel: `ResolvedParallel{MaxConcurrent, FailFast, Steps}` (in `internal/pipeline/resolve.go`) + executor's `executeParallelGroup` (`executor.go:805`). Tristate `FailFast *bool` already mirrored in `config.ParallelGroup`. The workflow's `WorkflowParallel` will use the same `*bool` shape for parity.
- Pipeline reporter ↔ LiveLine glue: `PlainReporter.StartGroup` calls `r.live.StartBlock(N)` and pushes initial running labels per sub-step; per-sub-step `FinishStep`/`FailStep`/`SkipStep` calls `SetBlockRowFinal` with `BlockRowDone`/`BlockRowFailed`/`BlockRowSkipped`. Workflow runner will inline a similar choreography without a Reporter abstraction (no need — workflow has no phases or status journal).
- Sequential vs parallel routing in pipeline (post `a9cf9a9`): sequential steps go through `Reporter.SuspendForExec`/`ResumeAfterExec` and write directly to `os.Stdout` (with PTY when TTY) while a `logSanitizer`-wrapped tee captures the log. Parallel sub-steps route through `ansiOnlyStripper` → `lineTee` and DO NOT allocate a PTY. The workflow runner adopts the parallel-mode pattern for its sub-steps.
- `OpenSubStepLog` returns nil writers when logging is disabled; workflow callers must use `joinWriters` for the same reason pipeline does.

### Dependencies identified

- `golang.org/x/sync/errgroup` (already in go.mod via pipeline).
- `charm.land/bubbles/v2/spinner`, `charm.land/lipgloss/v2`, `github.com/charmbracelet/x/term` (LiveLine deps; already direct after the previous live-pipeline work).
- `signal.NotifyContext` from stdlib — to be added to `commands run` entrypoint.

## Development Approach

- **Testing approach**: Regular (implementation first, then tests in the same task).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task — tests are a required deliverable, not optional.
- **CRITICAL: all tests must pass before starting the next task** — `go test ./...` + `make lint`. No exceptions.
- **CRITICAL: update this plan file when scope changes during implementation** — append discovered tasks with ➕ and blockers with ⚠️.
- Maintain backward compatibility: existing workflows (no `parallel:` field) keep behaving exactly as before.

## Testing Strategy

- **Unit tests**: required for every task — model validation, loader parsing, runner dispatch, LiveLine integration, signal handling.
- **Integration tests**: a workflow with a parallel group of trivial commands runs them concurrently and writes the expected per-sub-step logs.
- **Concurrency**: at least one test runs under `go test -race` (workflow runner). The promoted `liveui` package keeps its existing race-clean tests.
- **No e2e**: this project has no Playwright/Cypress harness; final verification is manual (see Post-Completion).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, and doc updates achievable within this repo.
- **Post-Completion** (no checkboxes): manual real-terminal smoke testing — run `devbox commands run services.all.composer-install` against the tbm project and visually verify behaviour.

## Implementation Steps

### Task 1: Promote LiveLine + output-processing helpers to `internal/liveui/`

The workflow runner needs more than just LiveLine — it also needs the same `lineTee` (frame-aware `\r`/`\n` splitter), `ansiOnlyStripper`, `logSanitizer`, and `joinWriters` helpers that pipeline uses to capture per-sub-step output without interleaving. All five form a coherent "shared live-output infrastructure" set.

- [x] create `internal/liveui/` package directory
- [x] move `internal/pipeline/liveline.go` → `internal/liveui/liveline.go`; rename `package pipeline` → `package liveui`; keep currently-exported names exported (`LiveLine`, `NewLiveLine`, `BlockRowKind`, `BlockRowDone`, `BlockRowFailed`, `BlockRowSkipped`, `StartBlock`, `SetBlockRowRunning`, `SetBlockRowFinal`, `EndBlock`, `Pause`, `Resume`, `Start`, `Stop`, `SetText`, `Println`)
- [x] move the glyph constants used by `finalGlyph` (`iconDone`, `iconFailed`, `iconSkipped`, `iconRunning`) to `internal/liveui/`. Export them as `IconDone`/`IconFailed`/`IconSkipped`/`IconRunning` so both `pipeline/plain.go` and `usercommands/runtime/` reference a single source of truth.
- [x] move `formatElapsed` into `internal/liveui/` (used by both block-row stopwatch and pipeline `FinishPipeline` footer — promote, do not duplicate)
- [x] move `lineTee`, `ansiOnlyStripper`, `logSanitizer`, `joinWriters`, and `ansiOnlyRe` from `internal/pipeline/logging.go` into a new file `internal/liveui/output.go`. Export them as `LineTee`/`NewLineTee`, `ANSIOnlyStripper`, `LogSanitizer`, `JoinWriters` (workflow runner needs them too — replicating would diverge over time)
- [x] update `internal/pipeline/plain.go`, `internal/pipeline/executor.go`, and `internal/pipeline/logging.go` to import the new package; replace internal references with exported names; `OpenPipelineLog` / `OpenSubStepLog` stay in `internal/pipeline/logging.go` for now (they're path-aware and pipeline-named — workflow will pass `pipelineName="workflow"`)
- [x] move `internal/pipeline/liveline_test.go` → `internal/liveui/liveline_test.go`; rename package; the `termGrid` test helper moves with it (a minimal copy of `termGrid` remains in `internal/pipeline/termgrid_test.go` to keep pipeline package tests independent of liveui's _test helpers)
- [x] move the `lineTee`/`ansiOnlyStripper`/`logSanitizer` tests from `internal/pipeline/logging_test.go` into a new `internal/liveui/output_test.go`; the goroutine-leak `TestMain` (`goleak.VerifyTestMain`) moves with them so liveui's test suite keeps leak protection
- [x] run `go build ./...` — must succeed
- [x] run `go test -race ./internal/pipeline/... ./internal/liveui/...` — must pass
- [x] run `make lint` — 0 issues

### Task 2: Add `WorkflowParallel` struct + `WorkflowStep.Parallel` field

Mirror the pipeline `ParallelGroup` shape and wire it into `WorkflowStep`. Tristate `FailFast` so `true` is the default and YAML can override.

- [x] in `internal/usercommands/model/types.go`, add `WorkflowParallel` struct: `MaxConcurrent int (yaml:"max_concurrent")`, `FailFast *bool (yaml:"fail_fast")`, `Steps []WorkflowStep (yaml:"steps")`
- [x] add `Parallel *WorkflowParallel (yaml:"parallel,omitempty")` field to `WorkflowStep`
- [x] extend `WorkflowStep.Validate()` (currently `types.go:253-267`):
  - exactly one of `{Command, Confirm, Parallel}` must be set
  - `Parallel` is mutually exclusive with `With` (parallel container holds no params; each sub-step carries its own `With`)
  - `When` and `ContinueOnError` ARE valid on the parallel container (group-level)
  - validate sub-steps: every sub-step must be a leaf (`Command` set), NOT another parallel, NOT a confirm
  - `Parallel.Steps` must have length ≥ 2
- [x] write unit tests in `internal/usercommands/model/types_test.go` covering:
  - valid parallel step parses and validates
  - parallel + command coexistence rejected
  - parallel + confirm coexistence rejected
  - parallel + with on container rejected
  - parallel.steps with len 0/1 rejected
  - nested parallel (sub-step has its own `Parallel`) rejected
  - confirm inside parallel.steps rejected
  - `FailFast` default vs explicit false vs explicit true (parse to `*bool` correctly)
- [x] run `go test ./internal/usercommands/model/...` — must pass
- [x] run `make lint` — 0 issues

### Task 3: Loader / parser — accept `parallel:` in workflow YAML

The YAML decoder already handles arbitrary fields via reflection; the work is mostly in plan-time validation hooks that run AFTER struct decode.

- [x] verify that the strict-decode workflow loader (`internal/usercommands/loader/`) accepts the new field — `WorkflowStep` with `Parallel` set parses without "unknown field" errors
- [x] add plan-time validation passes that call `WorkflowStep.Validate()` recursively on parallel sub-steps (already recursive via `WorkflowStep.Validate()` walking `Parallel.Steps`; no extra walker needed)
- [x] write loader unit tests with fixture YAML files in `testdata/` covering: valid parallel workflow, nested-parallel-rejected, confirm-in-parallel-rejected, parallel-with-with-rejected (added inline YAML fixtures in `loader_test.go` — same pattern other loader tests use, no `testdata/` directory required)
- [x] run `go test ./internal/usercommands/loader/...` — must pass

### Task 4: Validator — `devbox validate commands` diagnostics (recursive)

Surface the same plan-time rules through `devbox validate commands` with proper severity, file, and hint columns. **Critically**, cross-reference validation MUST walk recursively into `WorkflowStep.Parallel.Steps`: today `Registry.Validate` / `Diagnostics` (`registry.go:181`) only inspects top-level workflow steps, so unknown commands inside `parallel.steps` would slip past validation and surface as runtime errors. Add path-aware diagnostics.

**Loader / validator contract** — the existing `validate/commands` (`commands.go:69`) calls `loader.LoadCommandFile` which runs strict YAML decode **and** `cf.Validate()`. The decoded-but-not-validated form is what feeds custom diagnostics; if `cf.Validate()` rejects (e.g. for nested-parallel, confirm-in-parallel, with-on-container) before the validator gets the chance to produce a categorised diagnostic, the user sees a generic loader error instead of `nested-parallel-not-supported`. To preserve diagnostic quality for the new parallel rules, split the parser path:

- expose a new `loader.ParseCommandFile(path) (model.CommandFile, error)` that runs strict YAML decode but does NOT call `cf.Validate()`. **Critically, it MUST still populate all derived/metadata fields** that downstream consumers depend on: `FilePath`, `GroupID`, `LocalName`, `Group`, `ID` per command. Without this, `registry.BuildRegistryFromParsed` and the path-aware diagnostic walker emit `"commands."` with empty group/ID, defeating the whole point of the split. The implementation is a straightforward extract from `LoadCommandFile`: refactor `LoadCommandFile` as `cf, err := ParseCommandFile(path); if err != nil { return cf, err }; return cf, cf.Validate()`
- `loader.LoadCommandFile` keeps its existing behaviour (parse + validate) — runtime callers (registry build at startup, `RunCommand`) continue using it and get safety
- `validate/commands` switches to `ParseCommandFile`, runs its own structured checks (the recursive walker below + per-rule predicates), and presents diagnostics with appropriate severity:
  - YAML decode errors and strict-field (unknown field) errors from `ParseCommandFile` itself → **error** severity, preserving the underlying message (these are real failures that should make `devbox validate` exit non-zero)
  - Structured-rule violations the validator recognises (nested parallel, confirm-in-parallel, etc.) → **error** severity with categorised message + actionable hint
  - `cf.Validate()` errors the validator did NOT specifically categorise → **error** severity with the raw `Validate()` message (so `devbox validate` still flags the problem; the user just doesn't get a polished category)
  - **No `info`-severity loader-fallback** — info diagnostics are reserved for "explanatory but non-failing" cases (e.g. template-pack override notices). Loader/validator failures are always actionable errors

- [x] in `internal/usercommands/registry/`, refactor the workflow-step cross-reference traversal into a recursive walker:
  - signature: `walkWorkflowSteps(steps []model.WorkflowStep, parentPath string, visit func(path string, step model.WorkflowStep))`
  - `parentPath` is the path prefix WITHOUT the trailing index, e.g. `"step"` for the root call or `"step[2].parallel.steps"` for nested
  - for each step at index `i`: compute `path := fmt.Sprintf("%s[%d]", parentPath, i)`; call `visit(path, step)`
  - if the step has `Parallel != nil`, recurse via `walkWorkflowSteps(step.Parallel.Steps, path+".parallel.steps", visit)`
  - initial call from the validator: `walkWorkflowSteps(cmd.Steps, "step", visit)`. Resulting paths: top-level `step[0]`, nested `step[2].parallel.steps[0]`, and (structurally) deeper-nested `step[2].parallel.steps[0].parallel.steps[1]` — though Task 2's `Validate()` already rejects depth ≥ 2 at decode time, the walker handles arbitrary depth defensively
- [x] update `Registry.Validate` / `Diagnostics` callers to use the walker for command-reference resolution; emit diagnostics with the path-qualified location string in the `Target` or `Message` column
- [x] in `internal/validate/commands/`, add diagnostics emitted by the workflow validator: nested parallel, confirm in parallel, with on parallel container, `parallel.steps` < 2, **unknown command referenced from `parallel.steps`** (with full path like `commands.<id>.steps[2].parallel.steps[0]`)
- [x] each diagnostic includes a `Hint:` line pointing the user at the constraint (e.g., "nested parallel is not supported in v1; flatten the group", "unknown command — check the spelling or define it in commands/")
- [x] write validator unit tests (table-driven, named subtests):
  - nested parallel diagnostic fires with correct path
  - confirm in parallel diagnostic fires with correct path
  - **unknown command inside parallel.steps** → diagnostic with path `step[i].parallel.steps[j]` (regression test for Issue 2 in this review)
  - valid parallel workflow produces no diagnostics
- [x] run `go test ./internal/validate/... ./internal/usercommands/registry/...` — must pass

### Task 5: Workflow runner — branch + `runParallelGroup` + per-sub-step log capture (no live UI)

Add the parallel dispatch path. **Task 5 is verifiable WITHOUT live UI**: all status / failure dumps emit textually to `rc.Stderr` from the post-Wait pass. Task 7 layers `liveui.LiveLine` block-row callbacks on top (no other behavioural change), so this task stays independently testable in non-TTY mode.

- [x] in `internal/usercommands/runtime/runner_workflow.go`, change the step loop to switch on step kind: `step.Parallel != nil` → `runParallelGroup`; `step.Confirm != ""` → `runConfirmStep`; else → `runCommandStep`. **Container-level `ContinueOnError`** is honoured at the workflow-loop call site (mirroring the existing per-step pattern), NOT inside `runParallelGroup`: `if err := runParallelGroup(...); err != nil { if step.ContinueOnError { /* warn + continue */ } else { return err } }`. `runParallelGroup`'s signature is `(ctx, rc, group, containerStepIdx) error` — no `continueOnError` parameter
- [x] add `runParallelGroup(ctx context.Context, rc RunContext, group *model.WorkflowParallel, containerStepIdx int) error` that:
  - resolves `maxConcurrent` (default `min(runtime.NumCPU(), len(group.Steps))`)
  - resolves `failFast` (default `true` when `FailFast == nil`)
  - **preflight: when + confirmation scan** — before launching ANY goroutine, walk `group.Steps` and build a `whenDecisions := make([]subDecision, n)` where `type subDecision struct { skip bool; err error }`. For each `sub`:
    1. If `sub.When == ""` → `whenDecisions[i] = subDecision{skip: false}`. Otherwise evaluate it ONCE via `evalWorkflowStepWhen(sub.When, rc)`. **`when:` evaluation can run shell commands (`cmd:` predicates via `EvalCommandCondition`)** — side effects + non-determinism mean we MUST evaluate exactly once per sub-step per group execution. Store the result: `whenDecisions[i] = subDecision{skip: !ok, err: evalErr}`. On `evalErr != nil` AND `failFast=true` we could early-return, but the cleaner path is to defer the error to the post-launch error pipeline (treated as a sub-step failure with the same `emit` routing) so that fail_fast=false aggregates it with other errors
    2. If `whenDecisions[i].skip == false` AND `whenDecisions[i].err == nil`, look up `rc.Registry.Get(sub.Command)`. If `def.Confirmation` is true AND `!rc.SkipConfirm` AND `!rc.NonInteractive` → return a clear error: `parallel sub-step %q requires confirmation; rerun with --yes or set DEVBOX_NONINTERACTIVE=1`

    **Semantics chosen (Option B / lenient)**: a `when: false` sub-step that references a `confirmation: true` command does NOT cause preflight rejection — its `when:` would have skipped the sub-step at runtime anyway, so requiring `--yes` would be unnecessarily strict.

    **Cached decisions are passed to goroutines** so the per-sub-step goroutine reads `whenDecisions[i]` instead of re-evaluating `sub.When`. This avoids double-execution of `cmd:`-style predicates — important for predicates with side effects (e.g. a `test -f` command that touches a flag file). Preflight is the SOLE evaluation point for `sub.When` within the parallel group. The runtime guards in Task 6 catch transitive confirmation cases preflight cannot see (separate concern)
  - opens an `errgroup.WithContext(ctx)` with `eg.SetLimit(maxConcurrent)`
  - declares a unified error sink: `emit := func(wrapped error) error { if failFast { return wrapped }; errsMu.Lock(); errs = append(errs, wrapped); errsMu.Unlock(); return nil }` — **every error path inside a goroutine routes through `emit`**, including log-open errors, sub.When eval errors, and sub-step exec errors. This guarantees no error is silently swallowed by `_ = eg.Wait()` in the fail_fast=false branch
  - declares `results := make([]subResult, n)` where `subResult{sub, err, output, skipped}` is set inside each goroutine (each goroutine writes to its own `results[i]` index — no race, no mutex needed for slice access). The failure dump (output between separators) is emitted SEQUENTIALLY from `runParallelGroup` after `eg.Wait()` returns, never from inside a goroutine. Avoids `bytes.Buffer`/`os.Stderr` interleaving and removes any need for a `stderr` mutex
  - propagates SIGTERM via `context.Context` cancellation (already wired through `runCommandStep` → `RunCommand` → `exec.CommandContext`)
- [x] **per-sub-step `when:` is NOT re-evaluated inside the goroutine** — the goroutine reads `whenDecisions[i]` (populated by preflight). If `whenDecisions[i].err != nil` → goroutine routes it via `emit(fmt.Errorf("workflow sub-step %q: when: %w", sub.Command, whenDecisions[i].err))`. If `whenDecisions[i].skip == true` → set `results[i].skipped = true` and return nil. Else proceed to `runCommandStep`. **Extract the evaluator as a small helper `evalWorkflowStepWhen(expr string, rc RunContext) (bool, error)`** so the sequential main loop and the preflight share one implementation. **No `live.*` calls in this task** — the skipped state is rendered textually by the post-Wait emit pass
- [x] per-sub-step output capture:
  - per-goroutine `gRC := rc; gRC.UnderParallel = true` (value-copy)
  - open per-sub-step log: `subFile, _, openErr := pipeline.OpenSubStepLog(rc.ProjectRoot, "workflow", sanitizedWorkflowID, sub.Command, true)`. On `openErr != nil`: route via `emit(fmt.Errorf("workflow sub-step %q: open log: %w", sub.Command, openErr))` and return nil. `defer subFile.Close()` per-goroutine
  - per-sub-step buffer (for failure dump): `var buf bytes.Buffer` (per-goroutine, not shared with siblings; stored into `results[i].output` after sub-step finishes)
  - per-sub-step `lineTee` whose callback (a) appends frame to `buf` and (b) writes frame to `subFile` via `fmt.Fprintln`. **No live-UI callback in this task** — Task 7 adds that as a third callback branch
  - override `gRC.Stdout = &liveui.ANSIOnlyStripper{W: tee}`, `gRC.Stderr = gRC.Stdout` so the sub-step's `runCommandStep` writes only into this per-goroutine writer
- [x] **per-sub-step error wrapping + results assignment**: wrap as `fmt.Errorf("workflow sub-step %q: %w", sub.Command, err)` before passing to `emit`. **CRITICAL: assign `results[i].err = wrapped` BEFORE calling `emit(wrapped)` on every error path** (when-eval failure, log-open failure, runCommandStep failure). Otherwise the post-Wait emit pass reads `results[i].err == nil` for a sub-step that failed in setup and prints `✓ Done` for it. The single rule: `emit` receives the error AFTER `results[i].err` has been set
- [x] **post-Wait emit pass** (sequential, no race): after `eg.Wait()` returns, iterate `results` in order; emit textual status lines to `stderr(rc)`:
  - skipped: `◎ [i/N] Skipped: <sub.Command> (when=false)`
  - succeeded: `✓ [i/N] Done: <sub.Command>` (optional — keep verbose unless noisy)
  - failed: `✗ [i/N] Failed: <sub.Command>` + `───── output ─────` + `result.output` + `──────────────────`

  Single-writer (the runParallelGroup goroutine) — no mutex, no race
- [x] write unit tests (table-driven where applicable, `t.Run` named subtests, run with `-race`):
  - parallel group of 3 trivial commands runs concurrently (assert via shared atomic counter — start counter at 0, each sub-step increments and waits until counter ≥ 3 before returning; if any sub-step times out the test fails, proving they actually overlap)
  - `fail_fast: true` cancels siblings when one fails (sibling sees `ctx.Done()` and exits with `context.Canceled`)
  - `fail_fast: false` runs all and aggregates errors via `errors.Join`; each error has the `"workflow sub-step %q: %w"` wrap
  - `fail_fast: false` + `OpenSubStepLog` failure on one sub-step — the open error appears in the joined error (regression test for "log-open errors swallowed" from prior review)
  - `continue_on_error: true` on a sub-step suppresses the cancellation; group still succeeds overall
  - **container-level** `continue_on_error: true` — group fails but the workflow loop swallows the err and continues to the next workflow step (test at the workflow-loop level, not inside runParallelGroup)
  - per-sub-step `Stdout` isolation: two sub-steps print interleaved lines via `time.Sleep` between writes — the captured per-sub-step buffers each contain only their own output, with no interleaving (regression test for "dump race" from prior review)
  - sub-step `when:` false skips that sub-step only; the rest run; the post-Wait emit shows `◎ Skipped` for the skipped one and `✓ Done` for the others; the joined error is nil
  - group-level `when:` false skips the entire group (handled at workflow loop, like existing per-step `when:`)
  - **confirmation preflight (direct)**: a parallel group whose sub-step references a `confirmation: true` user-command — without `--yes`, `runParallelGroup` returns the preflight error BEFORE launching any goroutine; with `--yes`, the group proceeds (the Task 6 runtime guard then ensures any *transitive* confirmation request still fails clearly, not via huh-prompt)
  - **confirmation preflight + when=false (lenient semantics)**: a parallel group with a `when: false` sub-step that references a `confirmation: true` user-command — preflight MUST skip the confirmation check for that sub-step (so the group proceeds even without `--yes`); other sub-steps' confirmation checks still apply. Regression test for Issue 3 in this review
- [x] add (or extend) `internal/usercommands/runtime/main_test.go` with `goleak.VerifyTestMain` so the runtime package gains the same goroutine-leak guarantee that `internal/pipeline` and `internal/liveui` have
- [x] run `go test -race ./internal/usercommands/runtime/...` — must pass

### Task 6: Parallel-context guards — nested parallel + transitive confirmation

Two runtime guards triggered by `RunContext.UnderParallel == true`:

A. **Nested parallel guard** — a workflow with a `parallel:` block cannot run while another `parallel:` block already owns the terminal (LiveBlock conflict). Covers:
   1. Pipeline parallel → workflow with parallel
   2. Workflow parallel → workflow with parallel

B. **Transitive confirmation guard** — Task 5's preflight only catches DIRECT references to commands with `confirmation: true`. It cannot see transitive cases: a parallel sub-step references workflow A which contains a `confirm:` step OR calls another `confirmation: true` command. Without runtime guards in `runConfirmStep` and `ConfirmCommand`, those code paths reach huh-prompt with shared stdin while a LiveBlock owns the terminal (deadlock / UI corruption). Runtime guards are the SAFETY net; preflight is UX.

#### Propagate the `UnderParallel` marker

- [x] add `RunContext.UnderParallel bool` to the `RunContext` struct in `internal/usercommands/runtime/runner.go`. **Leave `BuildRunContext` unchanged** — it has no parent `RunContext` parameter, so threading a value through its signature is invasive. Instead, the marker is set explicitly at each call site that needs it (three places below)
- [x] **pipeline call site** — `internal/pipeline/executor.go:execCommandAction`: after `rctx, err := usercommands.BuildRunContext(...)`, set `rctx.UnderParallel = actx.Parallel` before passing `rctx` to `usercommands.RunCommand`. `actx.Parallel` is `true` for sub-steps of `executeParallelGroup` and `false` for sequential steps, so the marker propagates correctly into the inner workflow
- [x] **`runCommandStep` forward** (`runner_workflow.go:99-152`) — the existing implementation builds a fresh `subCtx` for each sub-step with explicit field assignments (around `:124`). Add `UnderParallel: rc.UnderParallel` to that struct literal. Without this, the marker set by Task 5's `gRC.UnderParallel = true` is dropped when `runCommandStep` constructs its subCtx for the inner `RunCommand` call, and workflow-parallel→workflow-parallel slips past the guard
- [x] **workflow parallel call site** — `runner_workflow.go:runParallelGroup` sets `gRC.UnderParallel = true` on the cloned RunContext (per-goroutine value-copy from Task 5). The `runCommandStep` forward (above) then carries it into the inner `RunCommand`

#### Nested-parallel guard (sentinel + check)

- [x] define a sentinel error: `var ErrWorkflowNestedParallel = errors.New("nested workflow parallel block is not supported in v1")` in `runner_workflow.go`. The guard wraps it with the offending workflow ID: `fmt.Errorf("%w: workflow %q", ErrWorkflowNestedParallel, rc.Cmd.ID)`. Sentinel-based so tests use `errors.Is(err, ErrWorkflowNestedParallel)` rather than string match
- [x] in `WorkflowRunner.Run`: if `rc.UnderParallel` AND any `step.Parallel != nil` → return the wrapped sentinel — emit BEFORE launching any sub-step, so the failure surfaces at the parent parallel group's sub-step level cleanly

#### Transitive confirmation guard (safety net — independent of Task 5 preflight)

- [x] define a sentinel error: `var ErrConfirmInsideParallel = errors.New("interactive confirmation is not allowed inside a parallel group")` in `runner_workflow.go` (or a shared `errors.go` if it grows)
- [x] in `runConfirmStep` (`runner_workflow.go:68`) — **placement matters**: the existing function early-returns when `ctx.NonInteractive || isNonInteractive()` (the env-based check via `isNonInteractive()` is what makes `DEVBOX_NONINTERACTIVE=1` skip the prompt even when `rc.NonInteractive` wasn't explicitly populated). Place the new guard **AFTER** that early-return, so env-based non-interactive runs still proceed without false rejection. Concretely:
  ```go
  func runConfirmStep(ctx RunContext, message string) error {
      if ctx.SkipConfirm || ctx.NonInteractive || isNonInteractive() {
          return nil // existing auto-skip
      }
      // NEW guard — after the existing early-return, so env-based
      // NonInteractive bypasses the guard naturally.
      if ctx.UnderParallel {
          return fmt.Errorf("%w: confirm step %q in workflow %q", ErrConfirmInsideParallel, message, ctx.Cmd.ID)
      }
      // ... existing huh prompt logic ...
  }
  ```
- [x] in `ConfirmCommand` (`internal/usercommands/runtime/confirmation.go:17`) — **placement matters**: the existing function early-returns for non-confirming commands via a `ctx.Cmd == nil || !ctx.Cmd.Confirmation` check. Place the new guard **AFTER** that check so it only fires for commands that actually want to prompt; placing it at function entry would reject every non-confirming command run from a parallel context AND could nil-deref `ctx.Cmd.ID`. Concretely, the function shape becomes:
  ```go
  func ConfirmCommand(rc RunContext) error {
      if rc.Cmd == nil || !rc.Cmd.Confirmation { return nil }
      if rc.SkipConfirm || rc.NonInteractive { return nil }
      // NEW guard — after the early-returns above, so we know:
      //   rc.Cmd != nil (safe to deref Cmd.ID)
      //   the command WILL prompt unless we stop it
      if rc.UnderParallel {
          return fmt.Errorf("%w: command %q requires confirmation", ErrConfirmInsideParallel, rc.Cmd.ID)
      }
      // ... existing huh prompt logic ...
  }
  ```
  This catches transitive cases — a parallel sub-step references a workflow that calls a `confirmation: true` command, where Task 5's preflight cannot see the transitive call
- [x] both guards intentionally short-circuit AFTER `!SkipConfirm && !NonInteractive` checks (either inline as one combined `if`, or via the early-returns shown above for `ConfirmCommand`) so `--yes` / `DEVBOX_NONINTERACTIVE=1` still allow the parallel group to run with confirmations auto-skipped. Identical semantics to the preflight

#### Tests

- [x] **explicit non-coverage**: `rc.UnderParallel=false` (sequential pipeline step → workflow with parallel) is the supported case and must NOT trigger any guard. Cover with a positive integration test
- [x] unit tests (table-driven, named subtests):
  - workflow with parallel block invoked with `UnderParallel=true` returns an error matching `errors.Is(err, ErrWorkflowNestedParallel)`
  - workflow with parallel block invoked with `UnderParallel=false` runs normally
  - workflow A (parallel) → sub-step references workflow B (parallel) → B fails with the nested-parallel sentinel; A's parallel group wraps it as `"workflow sub-step %q: %w"` (Task 5 wrapping rule preserves `errors.Is`)
- [x] transitive-confirmation unit tests (regression for this review's Issue 1):
  - parallel sub-step → workflow with a `confirm:` step → workflow's `runConfirmStep` returns `ErrConfirmInsideParallel` when `!SkipConfirm`
  - parallel sub-step → workflow whose final step references a `confirmation: true` user-command → `ConfirmCommand` returns `ErrConfirmInsideParallel` when `!SkipConfirm`
  - same setups with `SkipConfirm=true` or `NonInteractive=true` — both guards pass through (no prompt reached, no error)
  - Task 5's preflight does NOT catch these cases (registry lookup of the immediate sub.Command returns `Confirmation=false` because the confirmation lives transitively in the workflow's body); the runtime guards are what makes the parallel group safe
- [x] integration test through pipeline: a `deploy.yml` `parallel:` group's sub-step referencing a workflow-with-parallel — assert the deploy fails at this step with `errors.Is(err, ErrWorkflowNestedParallel)`
- [x] POSITIVE integration test: a `deploy.yml` SEQUENTIAL step referencing a workflow-with-parallel — assert it runs successfully and the sub-step rows render
- [x] run `go test -race ./internal/usercommands/runtime/... ./internal/pipeline/...` — must pass

### Task 7: Live UI integration — workflow self-constructs LiveLine per parallel block

Layers `liveui.LiveLine` on top of Task 5's text-only parallel runner. Adds three things:

1. LiveLine construction + lifecycle (StartBlock / EndBlock / Stop) inside `runParallelGroup`
2. Per-sub-step `lineTee` callback gains a third branch that updates the block row via `SetBlockRowRunning` with the latest frame
3. Sub-step transitions call `SetBlockRowFinal` with the appropriate `BlockRowDone` / `BlockRowFailed` / `BlockRowSkipped` kind (replacing nothing — Task 5's post-Wait text emit still fires so non-TTY mode is unchanged)

Behavioural contract:

- **ad-hoc** (`devbox commands run …`): no parent LiveLine; workflow's own LiveLine renders on the terminal directly. Stopped when the parallel block ends; subsequent sequential steps write to stdout as before.
- **pipeline-sequential → workflow with parallel**: the pipeline's PlainReporter already paused its LiveLine via `Reporter.SuspendForExec` when the sequential step body began. The workflow's LiveLine has the terminal to itself for the parallel block. After `EndBlock` + `Stop`, the workflow returns, the pipeline executor calls `Reporter.ResumeAfterExec`, and the pipeline footer repaints. The workflow's block rows persist as scrollback INSIDE the pipeline step's output region — no pipeline-counter advance for sub-steps.

No huh prompts are possible inside a parallel block (confirm sub-steps are rejected at plan-time; sub-step commands with `confirmation: true` require `--yes`), so the workflow's LiveLine does NOT register `ui.SetHuhHooks` — it would otherwise need a save/restore dance to avoid clobbering pipeline's hooks.

- [x] in `runParallelGroup` (Task 5), self-construct a LiveLine scoped to the block:
  - detect TTY: `termOut := os.Stdout` when `term.IsTerminal(os.Stdout.Fd())`, else `io.Discard`
  - construct: `live := liveui.NewLiveLine(termOut, os.Stdout, termOut != io.Discard)`
  - `live.Start()` immediately, `defer live.Stop()` at function exit
  - `live.StartBlock(len(group.Steps))` before launching goroutines
- [x] per sub-step lifecycle:
  - on start (inside goroutine): `live.SetBlockRowRunning(idx, fmt.Sprintf("[%d/%d] %s", idx+1, n, sub.Command))`
  - on success: `live.SetBlockRowFinal(idx, liveui.BlockRowDone, fmt.Sprintf("[%d/%d] Done: %s", idx+1, n, sub.Command))`
  - on failure: `live.SetBlockRowFinal(idx, liveui.BlockRowFailed, fmt.Sprintf("[%d/%d] Failed: %s", idx+1, n, sub.Command))`
  - on skip (per-sub-step `when:` false): `live.SetBlockRowFinal(idx, liveui.BlockRowSkipped, fmt.Sprintf("[%d/%d] Skipped: %s (%s)", idx+1, n, sub.Command, reason))`
- [x] after `eg.Wait()`: `live.EndBlock()` (deferred `Stop()` then handles the rest)
- [x] non-TTY mode (workflow's LiveLine constructed with `enabled=false`): block-row methods are no-ops, fall back to the buffered-dump path from Task 5 so CI captures readable output
- [x] block-row labels use WITHIN-WORKFLOW indices `[1/N]`, `[2/N]`, …, NOT pipeline-wide indices — workflow sub-steps are not pipeline steps and must not advance the pipeline counter
- [x] in `internal/command/command_cmd.go` (`commands run` cobra command), install signal handling AND release it correctly:
  - `ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)`
  - **`defer stop()` immediately after** — without it the signal handler leaks for the rest of the process (relevant for daemonized invocations / long-lived shells)
  - pass `ctx` (not `cmd.Context()`) into `usercommands.RunCommand(ctx, rctx)`
  - do NOT construct a LiveLine here; the workflow runner self-manages
- [x] write unit tests with a stub LiveLine (via `installLiveLineCapture` test helper that swaps `newWorkflowParallelLiveLine` to a buffer-backed LiveLine with `SetTestHooks(noTicker=true)`):
  - parallel group drives `StartBlock` / `SetBlockRowRunning` / `SetBlockRowFinal` / `EndBlock` / `Stop` in the expected order
  - failed sub-step uses `BlockRowFailed`
  - skipped sub-step uses `BlockRowSkipped`
  - non-TTY mode falls back to buffered dumps and writes the captured lines between separator bars
- [x] CLI-entrypoint coverage: the cobra command's signal-handling block is plain plumbing (`signal.NotifyContext` + pass `ctx` to `RunCommand`); the meaningful contract — cancellation propagating into a running parallel group — is exercised by `TestWorkflowRunner_Parallel_ContextCancel_StopsCleanly` at the runtime level, so a full cobra-driven invocation test adds no incremental coverage
- [x] pipeline-composition test: Task 6 already added `TestPipelineComposition_SequentialStep_WorkflowParallel_RunsSuccessfully` (positive path) and `TestPipelineParallel_WithWorkflowParallelSubStep_Rejected` (nested-parallel rejection); both pass under `-race`
- [x] SIGINT-equivalent test: `TestWorkflowRunner_Parallel_ContextCancel_StopsCleanly` cancels the parent context mid-block, verifies the runner returns promptly, and asserts the captured LiveLine is `IsStopped()` afterwards
- [x] run `go test ./internal/usercommands/runtime/... ./internal/command/...` — must pass

### Task 8: Documentation

- [x] add a "Parallel sub-steps" section to `docs/reference/config/commands.md` under "Type: workflow"; include schema, the four constraints (≥2 sub-steps, no nested parallel, no confirm in sub-steps, no `with:` on the container), and cross-link to `deploy.md#parallel-step-groups` (the engine is described once there)
- [x] update `AGENTS.md`: add the new `internal/liveui/` package to the package layout list (one short line) and mention `WorkflowParallel` next to `WorkflowStep` in the `internal/usercommands/model/` description
- [x] do NOT touch `docs/plans/completed/` — ralphex moves this plan file when the run finishes

### Task 9: Verify acceptance criteria

- [ ] verify the schema works end-to-end with a tiny `commands/services/dummy.yml` fixture that declares a workflow with a parallel block of two sleep-style commands; assert they run concurrently (elapsed < sum-of-individual-durations)
- [ ] verify nested parallel is rejected at validate-time (`devbox validate commands` shows the diagnostic)
- [ ] verify confirm inside parallel is rejected at validate-time
- [ ] verify `with:` on a parallel container is rejected at validate-time
- [ ] verify `fail_fast: true` cancels siblings cleanly (children receive SIGTERM via cmd.Cancel; no orphaned processes)
- [ ] verify `--yes` / `DEVBOX_NONINTERACTIVE=1` propagates and silences confirmation-required sub-step commands (the spec's chosen approach: rely on existing skip-confirm inheritance, no new field)
- [ ] verify workflow-with-parallel inside a pipeline `parallel:` group is rejected with the runtime guard from Task 6
- [ ] **verify pipeline-sequential composition**: a pipeline whose `cmd:` step resolves to a workflow with `parallel:` runs successfully — the workflow's block rows render inside the pipeline step, the pipeline-step counter advances by exactly one (not by the sub-step count), and the block rows persist as scrollback above the resumed pipeline footer. Use a `termGrid`-backed integration test.
- [ ] verify a regular sequential `devbox deploy` (no workflow-parallel involvement) still behaves identically to pre-change baseline (no regression from Task 1's LiveLine extraction)
- [ ] run `make test` — all packages green
- [ ] run `make lint` — 0 issues
- [ ] run `make build` — binary built
- [ ] run a manual smoke (covered in Post-Completion)

## Technical Details

### Schema

```yaml
commands:
  services.all.composer-install:
    type: workflow
    description: Install composer deps for every service in parallel
    steps:
      - command: db.start                 # leaf step (sequential)
      - parallel:                         # NEW: group of parallel sub-steps
          max_concurrent: 4
          fail_fast: true
          steps:
            - command: services.main.composer-install
            - command: services.catalog.composer-install
            - command: services.sales.composer-install
            - command: services.customer.composer-install
      - command: services.main.migrate    # back to sequential
```

### Go types

```go
type WorkflowStep struct {
    // existing leaf-step fields:
    Command         string            `yaml:"command,omitempty"`
    With            map[string]string `yaml:"with,omitempty"`
    Confirm         string            `yaml:"confirm,omitempty"`
    When            string            `yaml:"when,omitempty"`
    ContinueOnError bool              `yaml:"continue_on_error,omitempty"`

    // NEW: when non-nil, this step is a parallel container.
    // Mutually exclusive with Command, Confirm, With.
    // When and ContinueOnError remain valid (group-level).
    Parallel *WorkflowParallel `yaml:"parallel,omitempty"`
}

type WorkflowParallel struct {
    MaxConcurrent int            `yaml:"max_concurrent,omitempty"`
    FailFast      *bool          `yaml:"fail_fast,omitempty"`
    Steps         []WorkflowStep `yaml:"steps"`
}
```

### Runner branch

```go
for i, step := range rc.Cmd.Steps {
    if step.When != "" { /* existing condition eval */ }
    switch {
    case step.Parallel != nil:
        if err := runParallelGroup(ctx, rc, step.Parallel, i); err != nil {
            // Container-level continue_on_error mirrors per-step handling.
            if step.ContinueOnError {
                fmt.Fprintf(stderr(rc), "workflow step[%d] parallel group failed (continue_on_error): %v\n", i, err)
                continue
            }
            return err
        }
    case step.Confirm != "":
        if err := runConfirmStep(rc, step.Confirm); err != nil { /* existing */ }
    default:
        if err := runCommandStep(ctx, rc, i, step); err != nil { /* existing */ }
    }
}
```

### `runParallelGroup` skeleton

The workflow runner self-constructs a LiveLine scoped to the parallel block. It pauses no other LiveLine and registers no huh hooks (interactive prompts inside parallel are forbidden at plan-time).

**Skeleton shown is the Task-5 state (text-only emit, no LiveLine).** Task 7 layers the LiveLine block-row updates on top — see the "Task 7 delta" patch below the main skeleton.

```go
var ErrWorkflowNestedParallel = errors.New("nested workflow parallel block is not supported in v1")

type subResult struct {
    sub     model.WorkflowStep
    err     error
    output  string // captured per-sub-step buffer
    skipped bool
}

// runParallelGroup is the Task-5 implementation: parallel execution + per-
// sub-step log capture + post-Wait textual emit to rc.Stderr. No live UI.
// Task 7 patches in LiveLine block-row callbacks (see delta below).
func runParallelGroup(parentCtx context.Context, rc RunContext, group *model.WorkflowParallel, idx int) error {
    n := len(group.Steps)
    maxC := group.MaxConcurrent
    if maxC <= 0 {
        maxC = min(runtime.NumCPU(), n)
    }
    failFast := true
    if group.FailFast != nil {
        failFast = *group.FailFast
    }

    // Preflight: evaluate when ONCE per sub-step (`cmd:` predicates can have
    // side effects, so re-evaluation is unsafe) and check confirmations for
    // sub-steps that would actually run.
    type subDecision struct {
        skip bool
        err  error
    }
    whenDecisions := make([]subDecision, n)
    for i, sub := range group.Steps {
        if sub.When == "" { continue }
        ok, err := evalWorkflowStepWhen(sub.When, rc)
        whenDecisions[i] = subDecision{skip: !ok, err: err}
    }
    if !rc.SkipConfirm && !rc.NonInteractive {
        for i, sub := range group.Steps {
            d := whenDecisions[i]
            if d.err != nil { continue }   // surface inside goroutine via emit
            if d.skip       { continue }   // lenient: when=false → no preflight
            def, err := rc.Registry.Get(sub.Command)
            if err != nil { return fmt.Errorf("parallel preflight: %w", err) }
            if def.Confirmation {
                return fmt.Errorf("parallel sub-step %q requires confirmation; rerun with --yes or set DEVBOX_NONINTERACTIVE=1", sub.Command)
            }
        }
    }

    eg, gctx := errgroup.WithContext(parentCtx)
    eg.SetLimit(maxC)
    var errs []error
    var errsMu sync.Mutex
    results := make([]subResult, n)

    // Single emit helper — every error path inside a goroutine routes
    // through it so failFast / aggregate semantics are uniform regardless
    // of where the error originates (open-log, when-eval, exec).
    emit := func(wrapped error) error {
        if failFast { return wrapped }
        errsMu.Lock()
        errs = append(errs, wrapped)
        errsMu.Unlock()
        return nil
    }

    workflowID := sanitizeForFS(rc.Cmd.ID)

    for i, sub := range group.Steps {
        i, sub := i, sub // explicit shadow for Go <1.22; redundant on 1.22+
        eg.Go(func() error {
            results[i].sub = sub

            // Use the cached when-decision; do NOT re-evaluate sub.When.
            // The preflight already ran it once; `cmd:` predicates may have
            // side effects, so we must not run them again.
            if d := whenDecisions[i]; d.err != nil {
                wrapped := fmt.Errorf("workflow sub-step %q: when: %w", sub.Command, d.err)
                results[i].err = wrapped
                return emit(wrapped)
            } else if d.skip {
                results[i].skipped = true
                return nil
            }

            // Per-goroutine RunContext clone — isolates Stdout/Stderr from
            // siblings; UnderParallel forwarded into subCtx by runCommandStep.
            gRC := rc
            gRC.UnderParallel = true

            subFile, _, openErr := pipeline.OpenSubStepLog(rc.ProjectRoot, "workflow", workflowID, sub.Command, true)
            if openErr != nil {
                wrapped := fmt.Errorf("workflow sub-step %q: open log: %w", sub.Command, openErr)
                results[i].err = wrapped
                return emit(wrapped)
            }
            if subFile != nil { defer func() { _ = subFile.Close() }() }

            var buf bytes.Buffer
            tee := liveui.NewLineTee(func(frame string, final bool) {
                buf.WriteString(frame)
                buf.WriteByte('\n')
                if subFile != nil { _, _ = fmt.Fprintln(subFile, frame) }
            })
            defer tee.Flush()
            gRC.Stdout = &liveui.ANSIOnlyStripper{W: tee}
            gRC.Stderr = gRC.Stdout

            err := runCommandStep(gctx, gRC, idx, sub)
            tee.Flush()
            results[i].output = buf.String()

            if err != nil {
                wrapped := fmt.Errorf("workflow sub-step %q: %w", sub.Command, err)
                results[i].err = wrapped
                if sub.ContinueOnError { return nil }
                return emit(wrapped)
            }
            return nil
        })
    }

    var groupErr error
    if failFast {
        groupErr = eg.Wait()
    } else {
        _ = eg.Wait()
        if len(errs) > 0 { groupErr = errors.Join(errs...) }
    }

    // Post-Wait emit — sequential, single-writer to rc.Stderr, no race.
    for i, r := range results {
        switch {
        case r.skipped:
            fmt.Fprintf(stderr(rc), "  ◎ [%d/%d] Skipped: %s (when=false)\n", i+1, n, r.sub.Command)
        case r.err != nil:
            fmt.Fprintf(stderr(rc), "  ✗ [%d/%d] Failed: %s\n", i+1, n, r.sub.Command)
            fmt.Fprintln(stderr(rc), "  ───── output ─────")
            fmt.Fprint(stderr(rc), r.output)
            fmt.Fprintln(stderr(rc), "  ──────────────────")
        default:
            fmt.Fprintf(stderr(rc), "  ✓ [%d/%d] Done: %s\n", i+1, n, r.sub.Command)
        }
    }

    return groupErr
}
```

The container-level `ContinueOnError` on the parallel step is NOT handled here — the workflow loop's existing per-step handler swallows the returned error if `step.ContinueOnError == true`, identical to its behaviour for sequential leaf steps.

### Task 7 delta — LiveLine block-row callbacks layered on top

Task 7 adds three insertions to the function above. The text-emit pass at the bottom STAYS — it serves non-TTY mode and acts as a permanent scrollback record under the (transient) live block.

1. **LiveLine lifecycle** — at the top of `runParallelGroup`, after the preflight, before the `errgroup` setup:
   ```go
   termOut := io.Writer(io.Discard)
   if term.IsTerminal(os.Stdout.Fd()) { termOut = os.Stdout }
   live := liveui.NewLiveLine(termOut, os.Stdout, termOut != io.Discard)
   live.SetText(fmt.Sprintf("parallel: %s", rc.Cmd.ID))
   live.Start()
   defer live.Stop()
   live.StartBlock(n)
   defer live.EndBlock()
   ```

2. **lineTee third callback branch** — inside the lineTee closure, add a live-row update on each final frame:
   ```go
   if final {
       live.SetBlockRowRunning(i, fmt.Sprintf("[%d/%d] %s: %s", i+1, n, sub.Command, frame))
   }
   ```

3. **Block-row finalisation** — call `SetBlockRowFinal` at each terminal transition inside the goroutine. Skipped:
   ```go
   live.SetBlockRowFinal(i, liveui.BlockRowSkipped, fmt.Sprintf("[%d/%d] Skipped: %s (when=false)", i+1, n, sub.Command))
   ```
   Done (success):
   ```go
   live.SetBlockRowRunning(i, fmt.Sprintf("[%d/%d] %s", i+1, n, sub.Command))   // before runCommandStep
   live.SetBlockRowFinal(i, liveui.BlockRowDone, fmt.Sprintf("[%d/%d] Done: %s", i+1, n, sub.Command)) // after success
   ```
   Failed:
   ```go
   live.SetBlockRowFinal(i, liveui.BlockRowFailed, fmt.Sprintf("[%d/%d] Failed: %s", i+1, n, sub.Command)) // after error
   ```

When LiveLine is disabled (non-TTY), all `live.*` calls are no-ops by contract — the runner falls back to the Task-5 text-emit path with no extra work.

### Cancellation contract

- `signal.NotifyContext(SIGINT, SIGTERM)` installed in `commands run` (Task 7) cancels the parent context.
- `runParallelGroup` passes the errgroup-derived `gctx` into `runCommandStep` → `RunCommand` → child runner → `exec.CommandContext`, which already has `cmd.Cancel = SIGTERM` + `cmd.WaitDelay = 5s` per existing `bindCancel`/`bindCancelTerm` (matches pipeline behaviour).
- `fail_fast: true` + first error → errgroup cancels gctx → siblings see `gctx.Done()` and exit; children get SIGTERM via `cmd.Cancel`.

### Per-sub-step log files

`OpenSubStepLog(rc.ProjectRoot, "workflow", sanitizedWorkflowID, sanitizedSubName, true)`

Output → `.devbox/logs/parallel/workflow/<workflow-id>/<sub-name>.log`. Same shape as `OpenSubStepLog(... "deploy" ...)` for pipeline.

### `RunContext` sharing invariant across parallel goroutines

The workflow parallel implementation depends on a non-obvious invariant: **`RunContext.Render` (the `*tpl.RenderContext`) MUST be reconstructed fresh per sub-step, not shared across goroutines.** Today this holds because `runner_workflow.go:126-133` builds a fresh `renderCtx` inside `runCommandStep` before calling `RunCommand` — so even if the parallel runner clones `RunContext` shallowly and reads the same `Render` pointer, the inner call replaces it before any mutation (`runtime/runner.go:130` does `rc.Render.Files = paths`, but on the FRESH renderCtx, not the shared parent one).

If a future refactor removes the per-sub-step `renderCtx` rebuild, this becomes a data race (`RunCommand` writes `rc.Render.Files` through the shared pointer). The integration test for parallel + `files:` directive (`-race`-clean) pins the current behaviour; any change to `runCommandStep` or `RunCommand` that touches `rc.Render.*` MUST keep the per-call freshness or this plan's design needs revisiting.

The per-goroutine `subRC` clone we add in Task 5 protects `Stdout`/`Stderr`/`UnderParallel` — those are value-typed fields, so a value-copy of `RunContext` isolates them per goroutine. Pointer-typed fields (`Render`, `Config`, `Registry`, `Cmd`, `DockerConfig`) are intentionally shared as read-only references.

### Skip-confirm propagation (spec §4.3 approach A)

When a sub-step references a command with `confirmation: true`, the existing `runConfirmStep` / `ConfirmCommand` paths honour `rc.NonInteractive` and `rc.SkipConfirm`. The CLI's `--yes` flag (and `DEVBOX_NONINTERACTIVE=1` env) already sets these in `command_cmd.go` when building the RunContext. We do NOT add a new `parallel.skip_confirm` field; running a parallel workflow with confirmation-required sub-steps requires `--yes`.

### Workflow-in-pipeline-parallel guard (spec §7.4)

`RunContext.UnderParallel bool` propagated by:

- the pipeline executor when dispatching parallel sub-steps (set on the user-command RunContext built inside `execCommandAction` when `ActionContext.Parallel == true`)
- the workflow runner itself when dispatching its own parallel sub-steps (set on the cloned `subRC` passed to each goroutine's `runCommandStep`)

`WorkflowRunner.Run` short-circuits with a clear error if any step has `Parallel != nil` AND `rc.UnderParallel == true`. Plan-time validation is intentionally NOT added (cross-package coupling is messy; the runtime guard is sufficient and fires before any sub-step launches).

### Composition matrix

| Parent context | Sub-step shape | Result |
|---|---|---|
| ad-hoc CLI (`commands run`) | workflow without `parallel:` | sequential workflow, plain stdout (unchanged) |
| ad-hoc CLI (`commands run`) | workflow with `parallel:` | workflow self-constructs LiveLine, renders block, stops; clean exit |
| pipeline **sequential** step → workflow | without `parallel:` | pipeline `Reporter.SuspendForExec` pauses pipeline footer; workflow runs sequentially writing to stdout; `ResumeAfterExec` repaints (unchanged) |
| pipeline **sequential** step → workflow | with `parallel:` | pipeline footer paused; workflow self-constructs LiveLine on the freed terminal; block rows render with within-workflow `[i/N]` indices; block rows persist as scrollback INSIDE the pipeline step's output region; workflow stops LiveLine; pipeline `ResumeAfterExec` repaints. **No pipeline-counter advance for sub-steps**. |
| pipeline **parallel** sub-step → workflow | without `parallel:` and without any confirmation | sequential workflow runs inside one parallel sub-step's lineTee — unchanged |
| pipeline **parallel** sub-step → workflow | with `parallel:` | **REJECTED** by `ErrWorkflowNestedParallel` guard; sub-step fails with clear error |
| workflow **parallel** sub-step → workflow | with `parallel:` | **REJECTED** by `ErrWorkflowNestedParallel` guard (workflow runner sets `UnderParallel` on `subRC`); sub-step fails |
| parallel sub-step (any flavour) | direct command with `confirmation: true` and `!SkipConfirm` | **REJECTED** by Task 5 preflight before launch; user told to use `--yes` |
| parallel sub-step (any flavour) | workflow containing a `confirm:` step, `!SkipConfirm` | **REJECTED** at runtime by `runConfirmStep` guard → `ErrConfirmInsideParallel` |
| parallel sub-step (any flavour) | workflow that calls (directly or transitively) a `confirmation: true` command, `!SkipConfirm` | **REJECTED** at runtime by `ConfirmCommand` guard → `ErrConfirmInsideParallel` (preflight does NOT catch this — the confirmation is transitive) |
| parallel sub-step (any flavour) | any of the above WITH `--yes` / `DEVBOX_NONINTERACTIVE=1` | runs; confirmations auto-skipped at each `ConfirmCommand` / `runConfirmStep` |

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual smoke test** (against the tbm project):

- `devbox commands run services.all.composer-install` — observe 4 parallel block rows with spinners + stopwatch + colors (blue spinner → green ✓); no leftover spinner line in scrollback after exit.
- `devbox commands run services.all.composer-install --yes` — confirmation-required sub-steps run without prompts.
- Hit Ctrl-C mid-parallel-group — group cancels cleanly, all child `docker compose run` processes get SIGTERM and exit; `docker ps` shows no orphans.
- Run with non-TTY (`devbox commands run services.all.composer-install 2>&1 | tee out.log`) — output is buffered dumps between `───── output ─────` separators, no `\r`-spam.

**Pipeline-composition smoke** (the most important new behaviour):

- Add `cmd: services.all.composer-install` as a SEQUENTIAL step inside `deploy.yml` (e.g. inside the `main/setup` phase). Run `devbox deploy`. Verify:
  - the pipeline footer pauses when the step starts (Reporter.SuspendForExec)
  - the workflow's 4 block rows render below the paused pipeline footer position, with their own spinners + stopwatch + within-workflow `[1/4]…[4/4]` indices
  - after the workflow completes, the pipeline footer repaints (Reporter.ResumeAfterExec) and the pipeline-step counter advances by ONE (not four)
  - the block rows persist as scrollback inside the pipeline step's output region — they look identical to whatever an inline pipeline-`parallel:` block would have shown, but indices are workflow-scoped
- Add `cmd: services.all.composer-install` as a sub-step of a pipeline `parallel:` group → run `devbox deploy` → expect a clean failure: `workflow "services.all.composer-install" cannot run a parallel block when invoked from another parallel group`.

**Per-sub-step log sanity**:

- `cat .devbox/logs/parallel/workflow/services.all.composer-install/services.main.composer-install.log` — clean ANSI-stripped + `\r→\n`-normalised content.

**Regression checks**:

- A sequential workflow (no `parallel:` field anywhere) still runs as before — no behavioural change.
- `devbox deploy` with no workflow-parallel involvement still works end-to-end (LiveLine extraction in Task 1 must not regress pipeline behaviour).

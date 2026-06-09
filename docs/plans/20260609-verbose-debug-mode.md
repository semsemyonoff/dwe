# Verbose / Debug Mode for dwe CLI

> Revision 2 — incorporates plan-review findings: corrected hook points (raw-docker, decisions, live-view push), defined parallel-attribution mechanism (ctx-override over a safe global-printer baseline), tightened Verbose-vs-Debug scope (probe echoes → Debug), and pinned the slog-install policy. All file/line references below were verified against the tree.

## Overview

Add two orthogonal diagnostic flags to the `dwe` CLI:

- `-v, --verbose` (persistent bool) — echo executed commands (docker/compose lifecycle, raw `docker stop/restart/rm`, `sh -c`, nested `dwe`, git) plus key pipeline **decisions** (step run/skip + reason, `when:`/condition results, preflight results).
- `--debug` (persistent bool) + `DWE_DEBUG` env equivalent (flag wins on conflict) — a structured **firehose**: everything `--verbose` shows, plus read-only docker probe commands, lifecycle meta, subprocess timings/exit codes, config-resolution internals, full compose env, cache hits/misses (via `log/slog` at Debug).

`--debug` is a superset of `--verbose`; the flags combine. **All diagnostic output goes to stderr only — stdout stays untouched**, so `--output json` remains a clean machine-readable contract (`-v --output json` is valid: JSON on stdout, diagnostics on stderr).

The transport is a new leaf package `internal/shared/trace`, configured once at startup (mirroring `slog`'s global-default pattern). It is live-view-aware: while the pipeline's sticky footer is active, trace lines are printed *above* it through `LiveLine.Println` (the safe, mutex-guarded screen path) instead of being dumped raw to stderr.

**Problem it solves:** today `dwe` output is purely user-facing rendering with no diagnostic channel. Users cannot see which docker commands run, why a step was skipped, or what the engine decided. This adds that visibility without polluting normal or JSON output.

## Context (from discovery)

Files/components involved (verified against the tree, with line anchors):

- **Flags / root:** `internal/cli/cmdctx/flags.go` (`RootFlags`), `internal/cli/root.go` (`initRootCmd`).
- **Docker compose chokepoint:** `internal/shared/docker/compose.go` — `Exec()` (L142, user-facing → Verbose), `output()` (L249) and `RunningServices()` (L281) (read-only `ps` probes → **Debug**); `formatCommand()` (L157) + `quoteArg()` (L165) to lift into `trace`.
- **Raw docker chokepoint:** `internal/shared/docker/stop.go` — `runDirect(ctx, dockerBin, label, …, args…)` (L22) holds the single `exec.CommandContext` (L26); used by `StopContainer` (L49), `RestartContainer` (L63), `RemoveContainer` (L74). `internal/cli/lifecycle/{stop,restart}.go` call these via the `stopContainerFn`/`restartContainerFn` seams — **no raw exec there**.
- **Git chokepoint:** `internal/shared/git/git.go` — `execRunner.Run(ctx, dir, args…)` (L35) is the single `exec.CommandContext` (L36).
- **Execution engine:** `internal/core/execution/pipeline/executor.go` — `ActionContext` (L95, carries `StepWriter`/`LogWriter`), `ExecAction` (L171), `execShellAction` (L208), `execDweAction` (L217), `execBuiltinAction` (L230), `execCommandAction` (L254), `RunWithOptions` (L425, sees only the `Reporter` interface), skip-emit reason sites (`SkipPhase` L496, `SkipStep` phase-when L535, `skipStateStep` "state: already deployed" L581, files-gate `FormatFilesGate` L677), `SkipDecider` consults at L610/L689.
- **Reporter / live view:** `internal/core/execution/pipeline/plain.go` — `PlainReporter` owns `live *liveui.LiveLine` (L119); `StartPipeline` (L180), `FinishPipeline` (L350); `r.live.Println` is used throughout (L367/380/615/671/740) and is safe during block/parallel mode. `internal/shared/liveui/liveline.go` — `LiveLine.Println` (L503). `LogSanitizer` (plain.go L136) wraps the **log file**, not the screen.
- **Decisions:** `internal/core/workflow/deploy/journal/decision.go` — `Decide(prev, hash, hasCheck) Decision` (L38) is a **pure enum function** (no reason, no address); `SkipDecider` wired at `internal/cli/deploy/deploy.go:737`. `internal/core/execution/preflight/` (`preflight.Run`).

Related patterns:

- `RootFlags` passed by pointer via `NewCmd(groupID, flags)`; JSON mode gated in `root.go` (`NO_COLOR=1`, `SilenceErrors/Usage`).
- No production `slog.SetDefault` exists — existing `slog.Warn`/`Error` (e.g. `root.go:274/281`, config load, notifications) reach Go's default stderr handler at Info+; `Debug` is currently invisible.
- Layering: `internal/shared/*` are leaves; `core/` imports `shared/`; only `cli/` is the composition root. `trace` is a leaf — `docker`(leaf), `git`(leaf), the execution engine, and preflight import it; only `cli/` calls `Configure`/installs the slog handler.

## Development Approach

- **Testing approach: Regular** — implement the code in each task, then write table-driven tests before moving on.
- Complete each task fully before the next; small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** (success + error/edge cases) as separate checklist items.
- **CRITICAL: all tests must pass before starting the next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- Build with `make build` (not `go build`) after any `docs/` edit; run tests via `make test*` (embedded-docs dependency). For focused Go work, `make embedded-docs` once, then `go test ./internal/...` (and `-race` for `trace` + routing).
- Backward compatibility: with no flags, level is `LevelOff` and every trace call is a near-free early return (zero overhead in the normal run); the slog handler is **not** installed, so existing `Warn`/`Error` behavior is unchanged.
- **Before touching each area, read the matching section of `docs/internals/packages.md`** (docker, execution engine, cmdctx, liveui, CLI cross-cutting / JSON output).

## Testing Strategy

- **Unit tests:** required for every task. `trace` gets dedicated table-driven tests (levels, gating, routing precedence, quoting). Emit-site tests assert the echo/decision appears exactly when the level is on and is absent at `LevelOff`.
- **Routing tests** (Task 3): assert pipeline trace lines route through the reporter's `LiveLine.Println` printer (not raw stderr) and that parallel sub-steps attribute to the sub-step writer.
- **Golden/regression:** `-v --output json` and `--debug --output json` keep stdout a single valid JSON document; diagnostics only on stderr.
- **No e2e suite** in this repo — covered by Go tests + a manual smoke in Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## Solution Overview

A single diagnostic source (`trace`) resolves its output destination by a **three-level precedence** at each emit:

1. **Context-carried printer** (`trace.WithLinePrinter(ctx, p)`) — set by the executor's parallel path to the current sub-step's writer, so concurrent sub-steps attribute correctly. Overrides everything.
2. **Global active printer** (`trace.SetPrinter(p) (restore func())`) — set by `PlainReporter.StartPipeline` to a printer wrapping `r.live.Println`, restored in `FinishPipeline`. Save/restore stack handles nested `dwe` pipelines (sequential, so no concurrent mutation). This is the **safe baseline**: `LiveLine.Println` is safe even during block/parallel mode, so any pipeline emit that lacks a ctx printer still prints framed above the footer (just not per-sub-step-attributed).
3. **Configured fallback writer** (`trace.Configure(w, lvl)`, `w = os.Stderr`) — used outside any pipeline (e.g. `dwe status -v`) and in JSON mode (live view off).

This resolves the parallel-attribution gap without an interface change: `PlainReporter` (which owns `r.live`) does the `SetPrinter`; `RunWithOptions` never needs the concrete printer. The ctx override is the *attribution refinement* on top of the always-safe global baseline.

`--debug` additionally installs an `slog.Handler` **via `slog.SetDefault`, only when level == LevelDebug**, whose `Handle` formats each record through the same precedence (ctx → global → fallback). Because it is installed only at Debug, existing `Warn`/`Error` output is unaffected when no flags are set. `--verbose` does **not** touch slog.

## Technical Details

`internal/shared/trace` public surface:

```go
type Level int
const ( LevelOff Level = iota; LevelVerbose; LevelDebug )

func Configure(w io.Writer, lvl Level)          // once, from root; atomic level + fallback writer
func Enabled(l Level) bool                        // cheap guard for expensive formatting

func Command(ctx context.Context, name string, args ...string)  // Verbose+
func Decision(ctx context.Context, format string, a ...any)       // Verbose+
func Debugf(ctx context.Context, format string, a ...any)          // Debug-only

type LinePrinter interface{ PrintLine(s string) }
func SetPrinter(p LinePrinter) (restore func())   // global active printer, save/restore stack (mutex)
func WithLinePrinter(ctx context.Context, p LinePrinter) context.Context  // ctx override

func FormatCommand(args []string) string          // lifted quoteArg/formatCommand; copy-pasteable
```

- Level held in `atomic.Int32`; the global printer slot guarded by `sync.Mutex` (only `SetPrinter` mutates it; parallel sub-steps use ctx, never the global slot → no concurrent mutation).
- `LevelOff` → every emit method returns immediately after one atomic load (no allocation).
- Emit methods resolve printer: `printerFrom(ctx)` → global → fallback writer; then format (`Command` builds `"$ " + FormatCommand(append([name], args...))`).
- Sites without a ctx (compose `Exec`/`output`, raw `runDirect`) pass `context.Background()` → resolve to global/fallback (safe under live view). Sites with ctx (executor actions, git, `RunningServices`) pass real ctx → parallel-attributed.
- `docker/compose.go`'s `Exec` error wrap reuses `trace.FormatCommand` (single quoting source); the local `formatCommand`/`quoteArg` are removed. **The error-wrap golden expectations in `compose_test.go` must stay byte-identical to `trace.FormatCommand` output.**

`DWE_DEBUG` truthiness: enabled when set and not in `{"", "0", "false", "no", "off"}` (case-insensitive).

Startup (`initRootCmd`): parse flags → `lvl := levelFrom(verbose, debug, os.Getenv("DWE_DEBUG"))` (debug-flag OR truthy `DWE_DEBUG` → Debug; else verbose → Verbose; else Off) → `trace.Configure(os.Stderr, lvl)` → if `lvl == LevelDebug`, `slog.SetDefault(traceSlogHandler)`.

## What Goes Where

- **Implementation Steps** (`[ ]`): package, flags, routing, emit sites, slog, docs, tests — all in this repo.
- **Post-Completion** (no checkboxes): manual smoke of `dwe run -v` / `--debug` against a real project; visual check the live-view footer is not garbled; parallel-group attribution check.

## Implementation Steps

### Task 1: Create the `trace` leaf package (sink + routing + quoting)

**Files:**
- Create: `internal/shared/trace/trace.go`
- Create: `internal/shared/trace/trace_test.go`

- [x] create `trace.go` with `Level` enum, `atomic.Int32` level, mutex-guarded global printer slot, ctx key for the override printer
- [x] implement `Configure`, `Enabled`, ctx-aware `Command`/`Decision`/`Debugf`, `SetPrinter`/`restore`, `WithLinePrinter`, `LinePrinter`, `FormatCommand` (lift `quoteArg`/`formatCommand` from `docker/compose.go`)
- [x] implement printer resolution precedence: ctx override → global → configured fallback writer; `LevelOff` is a zero-alloc early return
- [x] write tests: each method emits only at/above its level; `LevelOff` silent; `Enabled` gating; precedence (ctx beats global beats fallback); `SetPrinter` save/restore + nested restore ordering; `FormatCommand` quoting (port existing quoting cases)
- [x] write error/edge tests: nil writer / nil printer safety; concurrent emit-with-ctx-printer while global printer is set (run `-race`)
- [x] run tests — must pass before next task

### Task 2: Wire flags and `Configure` in root

**Files:**
- Modify: `internal/cli/cmdctx/flags.go`
- Modify: `internal/cli/root.go`
- Modify: nearest existing root/flags test (e.g. `internal/cli/root_test.go`)

- [x] add `Verbose bool` and `Debug bool` to `cmdctx.RootFlags`
- [x] register persistent flags in `initRootCmd`: `-v/--verbose`, `--debug` (no `-d`); help text per flag
- [x] compute level (`--debug` OR truthy `DWE_DEBUG` → Debug; else `--verbose` → Verbose; flag beats env) and call `trace.Configure(os.Stderr, lvl)` (slog install deferred to Task 8)
- [x] write tests: flags set level; `DWE_DEBUG=1` (no flag) → Debug; `DWE_DEBUG=0` → unchanged (off unless `--verbose`); `--verbose`+`DWE_DEBUG=1` → Debug; no flags → Off
- [x] run tests — must pass before next task

### Task 3: Live-view routing + parallel attribution

**Files:**
- Modify: `internal/core/execution/pipeline/plain.go`
- Modify: `internal/core/execution/pipeline/executor.go` (parallel sub-step spawn path)
- Modify: `internal/core/execution/pipeline/*_test.go`

- [x] add a `LinePrinter` to `PlainReporter` whose `PrintLine` calls `r.live.Println`; `SetPrinter` it in `StartPipeline` (L180) and `restore()` in `FinishPipeline` (L350)
- [x] in the parallel sub-step path, derive a per-sub-step `LinePrinter` over the sub-step's `StepWriter`/`LogWriter` and attach it via `trace.WithLinePrinter(ctx, p)` for that goroutine's ctx
- [x] confirm `r.live.Println` is the screen path (no `LogSanitizer`); add explicit ANSI stripping for trace lines only if a manual check shows bleed
- [x] write tests (using direct `trace.Command`/`Decision` calls + a fake `LiveLine`/capture printer): pipeline emit routes through the reporter printer, not raw stderr; parallel sub-step ctx routes to the sub-step writer; `restore` returns to previous destination; nested pipelines save/restore correctly
- [x] run tests — must pass before next task

### Task 4: Command echo at the docker compose chokepoint

**Files:**
- Modify: `internal/shared/docker/compose.go`
- Modify: `internal/shared/docker/compose_test.go`

- [x] call `trace.Command(context.Background(), c.BinName(), args...)` before `cmd.Run()` in `Exec()` (Verbose)
- [x] call `trace.Command` at **Debug gating** before the probe runs in `output()` and `RunningServices()` (read-only `ps` probes — keep them out of Verbose to avoid `dwe status -v` spam): emit only when `trace.Enabled(trace.LevelDebug)` (or via a Debug-only emit helper)
- [x] update `Exec()` error wrap to use `trace.FormatCommand`; remove local `formatCommand`/`quoteArg`
- [x] write tests: `Exec` echoes once at Verbose just before run; `output`/`RunningServices` echo only at Debug, never at Verbose; `LevelOff` silent; error-wrap golden stays byte-identical to `trace.FormatCommand`
- [x] run tests — must pass before next task

### Task 5: Command echo for raw docker (stop/restart/rm) and git

**Files:**
- Modify: `internal/shared/docker/stop.go`
- Modify: `internal/shared/docker/stop_test.go`
- Modify: `internal/shared/git/git.go`
- Modify: `internal/shared/git/git_test.go`

- [x] emit `trace.Command(ctx, dockerBin, args...)` in `runDirect()` (one site covers Stop/Restart/Remove) at Verbose, before `cmd.Run()`
- [x] emit `trace.Command(ctx, args[0], args[1:]...)` in `execRunner.Run()` (git.go:35) at Verbose, before `cmd.Run()`
- [x] write tests: stop/restart/rm and git echo at Verbose (ctx threaded), silent at `LevelOff`; echo present even when the command fails
- [x] run tests — must pass before next task

### Task 6: Command echo in the execution engine (shell / dwe / command actions)

**Files:**
- Modify: `internal/core/execution/pipeline/executor.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`

- [x] in `ExecAction` (or each `exec*Action`), emit `trace.Command(ctx, …)` for `execShellAction` (`sh -c …`), `execDweAction` (nested `dwe …`), `execCommandAction` (resolved user command) before dispatch, using the action's `ctx` (so parallel sub-steps attribute via Task 3)
- [x] write tests: each action type echoes at Verbose; a parallel group attributes each sub-step's echo to its sub-step writer (exercises Task 3 routing end-to-end); silent at `LevelOff`
- [x] run tests — must pass before next task

### Task 7: Decision emits (executor reason sites + preflight)

**Files:**
- Modify: `internal/core/execution/pipeline/executor.go` (skip-emit reason sites + SkipDecider outcome)
- Modify: `internal/core/execution/preflight/` (`preflight.Run`)
- Modify: corresponding `*_test.go`

- [x] emit `trace.Decision(ctx, …)` at the executor skip/run reason sites: `SkipPhase`, phase-when `SkipStep`, step-level `when` skip, `skipStateStep` "state: already deployed", files-gate skip (`FormatFilesGate`), parallel-group `when` skip, and the `SkipDecider` Run/Skip outcome — these have the reason string + `addr` in scope. `skipStateStep`/`evalFilesGate` now thread `ctx`. **Did not** touch `journal/decision.go` (pure function)
- [x] emit `trace.Decision` in `preflight.Run` (skip-bypass notice + running notice + pass/fail summary with error/warning/info counts and blocking flag — not per-info noise)
- [x] write tests: skip/run reasons printed at Verbose (`when:`, phase `when:`, `state:`); preflight running/result decisions printed; all silent at `LevelOff`
- [x] run tests — must pass before next task

### Task 8: Debug firehose (timings/env/config/cache) + slog handler

**Files:**
- Modify: `internal/cli/root.go` (install slog handler at Debug)
- Create: `internal/shared/trace/slog.go` (+ `slog_test.go`) — `slog.Handler` formatting through the trace precedence
- Modify: `internal/shared/docker/compose.go`, `internal/shared/docker/stop.go` (timing + exit code)
- Modify: config-load / cache sites as needed (prefer existing `slog.Debug`)

- [x] implement an `slog.Handler` whose `Handle` formats records through the active trace printer/fallback; `Enabled` returns true for all records (it is installed **only** at Debug) — `internal/shared/trace/slog.go`, routes ctx → global → fallback; WithAttrs/WithGroup supported
- [x] in `root.go`, `slog.SetDefault(handler)` only when `lvl == LevelDebug` (leave Go's default otherwise so existing `Warn`/`Error` is unchanged) — via `installSlogHandler(lvl)`
- [x] wrap `cmd.Run()` in `compose.go` and `runDirect` to emit `trace.Debugf` with duration + exit code; emit compose env/cwd under `if trace.Enabled(LevelDebug)` — `exitCodeString`/`debugEnv`/`cwdLabel` helpers; only dwe-injected ProcessEnv overrides are shown (never the full inherited env)
- [x] surface lifecycle meta (default-config notices), config-load summary, and existing `slog.Debug` (notify/docs/journal) via `trace.Debugf`/`slog.Debug` routed through the handler — `EmitDefaultNotice` Debugf; `LoadConfig` "config loaded" summary slog.Debug. **Scope note:** pending-ops/snapshot-scope and a dedicated cache hit/miss site were left to the existing `slog.Debug` routing rather than adding bespoke sites (prompt hot path deliberately avoids trace/cobra overhead)
- [x] write tests: at Debug, timings + env + a sample `slog.Debug` record appear on the diagnostic channel; at Verbose/Off they do not; **no-regression: with no flags, a `slog.Warn` still reaches stderr** (handler not installed) — slog_test.go, compose/stop timing tests, root `installSlogHandler` + no-regression tests, config-load summary test
- [x] run tests — must pass before next task

### Task 9: JSON-mode regression

**Files:**
- Create/Modify: `internal/cli/*_test.go`

- [ ] add a test running a read-only command with `-v --output json` and `--debug --output json`: assert stdout is a single valid JSON document; diagnostics only on stderr
- [ ] verify the error envelope remains the final stderr structure when an error occurs with `-v`
- [ ] run tests — must pass before next task

### Task 10: Verify acceptance criteria

- [ ] verify Overview requirements: `-v`/`--debug`/`DWE_DEBUG`, superset, combinability, stderr-only, JSON-clean, probe echoes Debug-only
- [ ] verify edge cases: no flags → zero overhead + slog handler not installed; live-view not garbled; parallel attribution; nested `dwe` routing
- [ ] run full suite: `make test` and `make test-race` (trace + routing)
- [ ] run `make build` and `dwe --help` to confirm flag help renders
- [ ] verify coverage of `trace` and the emit sites

### Task 11: Documentation and finalize

**Files:**
- Modify: `docs/guides/troubleshooting.md` (add a "Verbose & debug output" section — single source, avoids duplicating the existing triage guide)
- Modify: `docs/i18n/ru/guides/troubleshooting.md` (mirror; `ru` is the only translated guide set)
- Modify: `AGENTS.md` (Critical Patterns: short `trace` routing-precedence entry; edit `AGENTS.md`, **not** the `CLAUDE.md` symlink)
- Modify: `internal/cli/root.go` flag help wording if needed

- [ ] write the "Verbose & debug output" section: when `-v` vs `--debug`/`DWE_DEBUG`, what each surfaces, stderr-only + JSON behavior, where to look
- [ ] mirror into `docs/i18n/ru/guides/troubleshooting.md`
- [ ] add the `trace` Critical-Patterns entry to `AGENTS.md`
- [ ] run `make build` (sync embedded docs) then `make test`
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification:**
- Run `dwe run -v` and `dwe run --debug` against a real multi-service project; confirm command echoes + decisions are readable and the live-view sticky footer is not garbled.
- Run a parallel pipeline group with `-v`; confirm each sub-step's command echo lands in its own sub-step log / stays attributed.
- `dwe status -v` should NOT spam `docker compose ps` probes (those are Debug-only); `dwe status --debug` should show them.
- `dwe status -v --output json | jq .` parses; diagnostics appear on stderr only.

**External system updates:**
- None — self-contained CLI change; no consuming projects or deployment config affected.

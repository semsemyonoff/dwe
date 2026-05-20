# Live Pipeline Progress (PTY + Sticky Footer + Live Block)

## Overview

Add live-progress UX to deploy/reset pipelines while keeping cancellation (Ctrl+C → SIGINT) and CI/non-TTY behaviour correct. Two visible improvements:

1. **Sticky live footer** for the whole pipeline (Variant C from the design chat): one bottom line in the terminal that shows a `bubbles/v2/spinner` plus `[N/M] <current-step>: <last-output-line>`. Persists across every reporter event so the user always sees what is happening.
2. **Multi-line live block** for parallel sub-step groups (Variant A): one row per sub-step shows live "last output line" with a per-row spinner, replaced by a final `✓ / ✗ / ◎` status row when the sub-step settles. PTY allocation per sub-step makes child processes (curl, docker compose) emit normal TTY progress that we capture, normalise, and render.

### Problem we are solving

The previous bubbletea-based parallel live view was removed in the prior commit because it (a) echoed terminal capability-query responses back to the screen (cooked-mode ECHO) and (b) enabled the kitty-keyboard protocol which trapped Ctrl+C → SIGINT no longer fired and the deploy became uninterruptible. The non-TTY fall-back path that replaced it dumps each sub-step's buffered output between separator bars on completion, which is correct but:

- shows nothing while long sub-steps (e.g. multi-minute downloads) are running,
- and the buffer contains every intermediate `\r`-rewrite frame from curl/docker compose, so the final dump renders as a wall of overlapping progress lines that wrap off-screen.

### Approach in one sentence

Use bubbles components (`spinner.Model`, `progress.Model`) **standalone** through their pure `View() string` API + a self-managed timer + raw ANSI cursor sequences for footer redraw. Never call `tea.NewProgram`, never call `term.MakeRaw` on stdin, never emit terminal capability queries. PTY for parallel sub-steps lets children emit nice progress that we capture and reduce to one "current frame" per row.

### Key invariants (non-negotiable)

1. **No `tea.NewProgram`.** Bubbles components are used only via their `View()` method. Animation is driven by a private `time.Ticker` that synthesises `spinner.TickMsg{ID: m.ID()}` and feeds it back through `Model.Update`.
2. **No `term.MakeRaw` on stdin.** Stdin termios stays in cooked mode so Ctrl+C generates SIGINT via VINTR. `signal.NotifyContext(SIGINT, SIGTERM)` already installed in `RunWithOptions` (executor.go:397) handles cancellation; child processes get SIGTERM via `cmd.Cancel` + 5s `WaitDelay`.
3. **No terminal capability queries.** No `RequestModeSynchronizedOutput`, `RequestKittyKeyboard`, etc. Only one-way writes (cursor moves, line clears) — terminal never has to respond.
4. **Split-channel writer model.** `LiveLine` takes TWO io.Writers — `termOut` for cursor/spinner ANSI (raw `os.Stdout`) and `screen` for persistent status lines (also `os.Stdout`, but written separately from cursor ANSI). The log file is owned by `PlainReporter`, NOT by `LiveLine`. PlainReporter writes log lines as a SEPARATE side-write next to each `live.Println`; childIO does NOT write to the log file as a parallel fan-out branch. The log file therefore receives every line exactly once: status lines via `Reporter.emit`, child output via `Reporter.StepOutput`'s `final=true` path. The per-sub-step log file (`subFile`) is still a parallel branch in joinWriters because it is independent of the global pipeline log.
5. **Single mutex serialises stdout writes.** All writes that touch the terminal — reporter `emit`, live-line redraw ticker, sub-step frame updates, AND sequential child output (via Reporter callback) — funnel through one `(*LiveLine).Println` / `(*LiveLine).redraw` mutex. Children NEVER write to `os.Stdout` directly while a live line is active. **The mutex is held during writes to `screen` and `termOut` — therefore `Stop()` MUST be called from outside any LiveLine method (e.g. from `FinishPipeline` via `defer`, never from inside a `Println` callback). The reporter's `live.Println` callback chain never re-enters LiveLine.**
6. **`\r` is data, not garbage.** The existing `ansiRe` regex strips bare `\r` along with ANSI CSI — this destroys frame information before the parser can see it. Replace with TWO purpose-built writer types: `ansiOnlyStripper` (used by the lineTee frame parser, preserves `\r`) and `logSanitizer` (used by log-file writers, strips ANSI AND **converts** `\r` to `\n` so progress frames separate into readable lines instead of concatenating like `0%50%100%`).
7. **Suspend/Resume covers ALL huh prompts via package-level hooks in `internal/ui`.** The executor currently calls `Reporter.SuspendForExec` / `ResumeAfterExec` around every non-parallel step body (executor.go:649,668) — this is too coarse: pausing the footer for every long-running step defeats the purpose. AND prompts exist in multiple places that all eventually call `ui.RunConfirm` / `ui.RunSelector` / `ui.RunMultiSelect` (pipeline `confirm` builtin, user-command `confirmation.go`, workflow `runner_workflow.go:runConfirmStep`, user-command `runner_builtin.go`). Threading new function fields through `RunContext`/`ExecContext`/`ActionContext` is invasive. **Instead: add package-level hooks `ui.SetHuhHooks(before, after func())` / `ui.ClearHuhHooks()` invoked by every `ui.Run*` entry point.** `NewPlainReporter` registers `r.live.Pause` / `r.live.Resume` as hooks at construction; reporter's `Close` / FinishPipeline clears them. Executor stops calling Suspend/Resume around step bodies. Only one `PlainReporter` is active per process (no nested pipelines), so the global is safe.
8. **Non-TTY parity.** When `term.IsTerminal(os.Stdout.Fd())` is false (CI, piped stdout) `LiveLine` is fully disabled (no ticker, no cursor sequences, no footer), but the frame-aware `lineTee` is always on, so CI buffer dumps no longer show `\r`-spam.
9. **Cursor invariant (single direction).** After ANY public `LiveLine` method returns, the terminal cursor is at column 0 of the row IMMEDIATELY BELOW the lowest LiveLine-owned row. In single-line mode that's `footer_row + 1`; in block mode it's `footer_row + 1` where the footer is below the block. `redraw()`, `Println`, `Start`, `Stop`, `StartBlock`, `SetBlockRow`, `EndBlock`, `SetText`, `Resume` all end with the cursor below the footer. **One documented exception**: `Pause()` deliberately leaves the cursor ON the cleared former-footer row so that huh-based prompts can render in place without a blank gap; `Resume()` restores the invariant.

## Context (from discovery)

### Files/components involved

- `internal/pipeline/plain.go` — `PlainReporter` (the only `Reporter` impl); will be rewired to drive a `LiveLine`, route ALL child output via a new `Reporter.StepOutput(addr, frame, final)` method, and write log lines as a side-channel next to each `live.Println`.
- `internal/pipeline/logging.go` — contains `ansiRe`, `ansiStripper`, `lineTee`. The OLD `ansiRe` and `ansiStripper` are removed; replaced by `ansiOnlyRe` + `ansiOnlyStripper` (for tee path) and `logSanitizer` (for log files; ANSI strip + `\r`→`\n` conversion). `lineTee` gains `\r`-aware frame splitting and a `(frame, final)` callback signature. `OpenPipelineLog` is refactored to return THREE separate writers: `screen` (raw `os.Stdout`-wrapped `*render.Writer`), `logFile` (the raw `*os.File`, or nil), and `termOut` (raw `os.Stdout` or `io.Discard`).
- `internal/pipeline/executor.go` — `childIO()` signature changes from `(logWriter, parallel)` to `(stepWriter, parallel)`; gains PTY allocation in the `parallel=true` AND `stdoutIsTTY` branch; the sequential branch always routes child output through the per-step lineTee → `Reporter.StepOutput`; `ActionContext` gains `StepWriter io.Writer` (replaces the responsibility of `LogWriter` for child-output routing); `RunOptions.Reporter Reporter` already exists (executor.go:299) and is used by the lineTee callback; `execShellAction`/`execDevboxAction`/`execCommandAction` call sites updated to pass `actx.StepWriter`; `execBuiltinAction` is special-cased — it does not call `childIO`, instead `ectx.Output = render.NewWriter(actx.StepWriter)` so builtin output flows through `StepOutput` like everything else; `executeStepBody` no longer wraps the body in `SuspendForExec`/`ResumeAfterExec` calls; `ExecStep` deprecated wrapper signature adjusted to match.
- `internal/builtin/builtin.go` — the unused `LogWriter io.Writer` field on `ExecContext` is removed (no builtin reads it; verified by grep). `Output *render.Writer` becomes the sole output channel.
- `internal/pipeline/reporter.go` — `Reporter` interface gains `StepOutput(addr, frame string, final bool)` AND `SetSubStepLogPath(subAddr, path string)` (called from `runParallelSubStep` after `OpenSubStepLog` to push the per-sub-step log path the reporter could not know at `StartGroup` time). `SubStepOutput` is removed (its callers migrate to `StepOutput`). `SuspendForExec`/`ResumeAfterExec` are removed from the interface (replaced by the package-level `ui` hook approach).
- `internal/pipeline/main_test.go` — `TestMain` goleak setup will be tightened (ticker goroutine must join cleanly on `Stop()`).
- `internal/lifecycle/phases.go` — also calls `OpenPipelineLog` and `NewPlainReporter` (phases.go:41,47); update call site for the new signatures.
- `internal/ui/huh.go` (and siblings `selector.go`, `multiselect.go`, `confirm.go`) — add package-level hook variables `huhBeforeHook`/`huhAfterHook` and public `SetHuhHooks(before, after func())` / `ClearHuhHooks()`. Each `ui.Run*` entry point fires `huhBeforeHook` before `tea.NewProgram(...).Run()` and `huhAfterHook` after. Hooks are nil-safe (no-op).
- `internal/builtin/confirm.go`, `internal/usercommands/runtime/confirmation.go`, `internal/usercommands/runtime/runner_workflow.go` — NO changes needed: they all call `runConfirm` (which is `ui.RunConfirm`), which now fires the registered hooks automatically.
- `internal/render/output.go` — no changes.
- `internal/command/deploy.go`, `internal/command/reset.go` — call sites for `pipeline.NewPlainReporter` change to pass three writers (`screen`, `logFile`, `termOut`) returned by the revised `OpenPipelineLog`.

### Related patterns found

- `github.com/creack/pty` is already used in `childIO` for sequential steps (allocates a PTY, copies master → `io.MultiWriter(os.Stdout, ansiStripper{logFile})` in the OLD code). The new code copies master → step's lineTee (sequential) or `joinWriters(logSanitizer{subFile}, tee)` (parallel) — NEVER touching `os.Stdout` directly. Global pipeline log receives output via Reporter side-write (Task 6), not via PTY fan-out.
- `charm.land/bubbles/v2/spinner` (and `progress`) are pure-output: `Model.View()` returns a string with no side effects. `Model.Update(spinner.TickMsg{ID: m.ID()})` advances `frame` synchronously and returns `(Model, tea.Cmd)` — we discard the Cmd. Verified in module cache (`spinner.go:131-157`).
- `lipgloss/v2` is already used in `internal/ui` for the project palette; LiveLine reuses `ui.Theme()` styling.
- `signal.NotifyContext` SIGINT path in `RunWithOptions` is already correct and unchanged.

### Dependencies identified

- `charm.land/bubbles/v2` — already in go.mod (used by `internal/ui` for huh).
- `charm.land/bubbletea/v2` — currently `indirect`; will return to a direct dep because spinner's `Update` accepts `tea.Msg` and we import `tea.Msg`/`tea.Cmd` types. We do NOT call `tea.NewProgram`.
- `charm.land/lipgloss/v2` — already direct.
- `github.com/creack/pty` — already used.
- `github.com/charmbracelet/x/term` — already used (`stdoutIsTTY`).

## Development Approach

- **Testing approach**: Regular (implementation first, then tests). Pure helpers (frame parser, ANSI cursor math) get unit tests; reporter integration is exercised through table-driven scenarios writing to a `bytes.Buffer` (non-TTY path) plus a small ANSI-aware buffer for cursor-sequence assertions.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task. Tests are not optional — they are a required deliverable of every task.
- **CRITICAL: all tests must pass before starting next task** — no exceptions. Use `go test ./internal/pipeline/...` after each task.
- **CRITICAL: update this plan file when scope changes during implementation.**
- After every code change, run the project test command and the linter.
- Maintain backward compatibility for callers: `pipeline.NewPlainReporter(w, termOut)` gains one new parameter but the call sites are in this repo; no external API.

## Testing Strategy

- **Unit tests**: required for every task.
  - Frame parser: table-driven over `\n`/`\r`/`\r\n`/no-terminator cases.
  - LiveLine cursor math: feed known input through `Println` / `SetText` / `Stop`, assert exact byte stream split between `termOut` (cursor ANSI) and `screen` (data lines) writers.
  - PlainReporter integration: drive each lifecycle event, assert visible buffer content after `stripANSI` + `stripTimestamps` (helpers exist in `plain_test.go`).
- **Terminal-state model**: add a small `termGrid` test helper that consumes a stream of ANSI cursor sequences against a virtual `[][]rune` grid of fixed height with auto-scroll (supports `\r`, `\n`, `\x1b[2K`, `\x1b[<N>A`, `\x1b[<N>B`, and tracks cursor position). Tests assert grid contents AND cursor position after each LiveLine/LiveBlock operation — this is the only way to verify the cursor invariant cleanly. (See Technical Details § Cursor state model.)
- **`termGrid` writer wiring** (critical for cursor-invariant tests): in a real terminal, both `termOut` and `screen` write to the same fd in serial order. Cursor-invariant tests therefore pass the SAME `*termGrid` instance for BOTH `termOut` and `screen` so the grid consumes byte streams in actual write order (LiveLine writes them under one mutex; concurrent writes can't happen). Channel-separation tests use a SEPARATE writer for each (two `bytes.Buffer`s) to verify that ANSI sequences land only in `termOut` and data lines only in `screen`. Two distinct test modes for distinct properties.
- **No-duplication tests**: integration tests that run a full pipeline (sequential + parallel steps) and count occurrences of each child output line in `logFile` content — must be exactly 1. Same for status lines.
- **Goroutine leak**: `TestMain` uses `goleak.VerifyTestMain`. The LiveLine ticker MUST join cleanly on `Stop()` — Stop pattern: close `stopCh` (no mutex held) → wait `<-doneCh` → take mutex to erase + reset state. Stop is NOT tested for reentrancy (it is documented as non-reentrant per Invariant #5).
- **Concurrency**: stress test that fires N concurrent `Println` calls + N `SetText` calls against one LiveLine and asserts no torn writes (all data lines well-formed line-by-line after the test, all ANSI well-formed in termOut).
- **Failure path**: explicit test that `FinishPipeline(false)` calls `LiveLine.Stop()` (current implementation early-returns on failure at plain.go:248 — that bug must be fixed in Task 7).
- **PTY**: PTY allocation has limited testability without a real terminal. We unit-test the parser/joinWriters glue around it; PTY itself is covered by the existing sequential-step tests (`TestChildIO_TTY_AllocatesPTY`).
- **No e2e tests in this project**; manual smoke test is in Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code/tests/doc updates achievable within this repo.
- **Post-Completion** (no checkboxes): manual smoke testing on a real terminal against a real project; visual sanity check that screenshots no longer show `^[[?2026...` / `^[[99;5u` garbage; verifying Ctrl+C interrupts mid-pipeline.

## Implementation Steps

### Task 1: Two writer-stripper types — `ansiOnlyStripper` for tee path, `logSanitizer` for log files

Goal: `\r` is data for the frame parser, but `ansiRe` currently destroys it before the parser sees it (logging.go:18). AND log files need a CR-to-NEWLINE conversion, NOT a CR-strip, otherwise progress frames concatenate into `0%50%100%` rather than separating into lines. Replace the single `ansiRe` with two purpose-built strippers.

Three pieces:

1. `ansiOnlyRe = regexp.MustCompile(\`\x1b\[[0-9;]*[a-zA-Z]|\x1b[a-zA-Z]\`)` — strips ANSI CSI/ESC sequences only. Preserves `\r` and `\n` byte values.
2. `ansiOnlyStripper{w io.Writer}` — uses `ansiOnlyRe`. Used by the tee path (`lineTee` frame parser sees `\r` as data).
3. `logSanitizer{w io.Writer}` — **stateless**. `Write(p)` does:
   (a) strip ANSI via `ansiOnlyRe`,
   (b) single-pass byte walk replacing EVERY `\r` byte with `\n` (no buffering, no state).
   This is what log files receive, so `50%\r100%\n` becomes `50%\n100%\n` (one frame per line on disk, readable). `\r\n` within one Write becomes `\n\n` (one extra blank line) — acceptable trade-off, see Note below. The old `ansiStripper{}` is REMOVED; `logSanitizer` replaces it everywhere.

**Why stateless (no `trailingCR bool`, no Flush/Close contract):**
- A stateful `trailingCR` would need a `Flush()`/`Close()` method to emit the final buffered `\r` if a stream ends mid-CR; callers (PTY copy goroutines, writeLog side-channel) would need lifecycle plumbing for this. That's complexity we don't want.
- A stateful writer would also need mutex protection because `os/exec` runs stdout and stderr copy goroutines concurrently when they point to different writer values, and our `joinWriters` constructs can produce distinct `io.MultiWriter` values per call. Adding a mutex inside `logSanitizer` is doable but couples log-file writing to a stripper's internal locking.
- The trade-off — `\r\n` inside one Write becomes `\n\n` — is one extra blank line in the log. CRLF is rare in our PTY-captured child output (children emit LF natively); curl/docker/etc. use `\r` alone for progress redraw, never `\r\n`. The cost is negligible.
- Stateless = zero lifecycle, zero locking, zero edge cases at stream end.

Checklist:
- [x] in `internal/pipeline/logging.go` add `ansiOnlyRe` (no `|\r`)
- [x] introduce `ansiOnlyStripper{w io.Writer}` (strips CSI/ESC, preserves `\r` and `\n`)
- [x] introduce `logSanitizer{w io.Writer}` — stateless, single-pass `\r`→`\n` substitution after ANSI strip
- [x] DELETE the old `ansiStripper` type and `ansiRe`/`ansiAndCRRe` regex; rewrite all current `&ansiStripper{...}` call sites to use `&logSanitizer{...}` (these are all log-file destinations)
- [x] inside `lineTee.Write`: replace the regex used for stripping with `ansiOnlyRe.ReplaceAll` so `\r` survives into the buffer (precondition for Task 2)
- [x] in `runParallelSubStep` (executor.go:822): the new join is `joinWriters(logSanitizer{subFile}, tee)` — `opts.LogWriter` (global pipeline log) is intentionally NOT in this join; global log receives parallel output via `Reporter.StepOutput`'s side-write (Task 6). Per-sub-step file gets full output via the direct `logSanitizer{subFile}` branch.
- [x] in `childIO` parallel branch: return the writer unchanged (no extra stripper wrapping inside `childIO` — the caller has already routed log destinations through `logSanitizer` and the tee handles its own ANSI stripping via `ansiOnlyStripper` internally).
- [x] write unit tests in `logging_test.go` for `ansiOnlyRe`: preserves `\r`, strips CSI/ESC
- [x] write unit tests for `logSanitizer.Write`: `50%\r100%\n` → `50%\n100%\n`; lone `\r` → `\n`; `\r\n` within one Write → `\n\n` (documents the stateless trade-off); split-write `\r` then `\n` across two Writes → `\n` then `\n` (also `\n\n`, same result); concurrent Writes from two goroutines do not panic (stateless means no shared mutable state — but a `-race` test still belongs here)
- [x] write unit tests for `lineTee` showing `\r` survives into the buffer (precondition for Task 2)
- [x] write a regression test capturing the exact bug from the review: feed `50%\r100%\n` to `logSanitizer{buf}`, assert `buf` contains `"50%\n100%\n"` and NOT `"50%100%\n"` or `"50%\r100%\n"`
- [x] run `go test -race ./internal/pipeline/...` — must pass before Task 2 (the `-race` flag catches the concurrent-Write-from-stdout-and-stderr case)

### Task 2: Frame-aware `lineTee`

Goal: with `\r` now preserved (Task 1), make `lineTee` emit one callback per *frame* — a segment ending in `\r` OR `\n` — with a `final bool` flag distinguishing committed-rows (`\n`) from in-progress redraws (`\r`).

- [x] change `lineTee` callback signature from `func(line string)` to `func(frame string, final bool)`
- [x] update `lineTee.Write` to scan for `\r` AND `\n` and dispatch a frame per segment; CRLF (`\r\n`) collapses to one `final=true` frame
- [x] update `lineTee.Flush` to emit any trailing non-terminated tail as `(frame, false)`
- [x] update `Reporter` interface (`internal/pipeline/reporter.go`): rename `SubStepOutput(addr, line string)` to `StepOutput(addr, frame string, final bool)` — also covers sequential steps (Task 6)
- [x] update `runParallelSubStep` to use the new signature
- [x] update `PlainReporter.SubStepOutput` (now `StepOutput`) to coalesce non-final frames. State per sub-step: `inProgress string` (current in-progress display state ONLY — never committed by StepOutput itself; committed centrally by `commitTrailingTail` at finish time — see Task 9). Logic:
  - on `final=false`: set `inProgress = frame` (display state for live-block / non-TTY no-op); NO buffer commit, NO `writeLog`
  - on `final=true`: append `frame + "\n"` (the FINAL frame, not `inProgress`) to the per-sub-step buffer; `writeLog(frame)`; reset `inProgress = ""`
  - on tee `Flush()` end-of-stream (which emits trailing tail as `(tail, false)`): `inProgress = tail` is set; **tail is NOT committed here** — the single commit point is `r.commitTrailingTail(addr)` invoked by FinishStep/FailStep/SkipStep in Task 9. This avoids two competing commit paths.
- [x] write unit tests in `logging_test.go` covering: `\n`-only, `\r`-only, `\r\n`, mixed, lone `\r` in middle of line, trailing tail flush, empty input, multiple `\r` in a row
- [x] write unit tests in `plain_test.go` verifying that progressive `\r` frames produce a single committed line in the buffer dump (key visible improvement even in CI)
- [x] write a regression test for the commit-the-right-frame bug: feed `50%\r100%\n` to `PlainReporter.StepOutput`; assert the per-sub-step buffer dump contains exactly `100%\n` — NOT `50%\n`
- [x] run `go test ./internal/pipeline/...` — must pass before Task 3

### Task 3: Refactor `OpenPipelineLog` to return separate screen / logFile / termOut

Goal: today's `OpenPipelineLog` bakes stdout into a `MultiWriter` with the log file, returning that combined writer as `*render.Writer`. The new design separates the channels so `PlainReporter` writes log lines explicitly next to each `live.Println` — no fan-out, no duplication.

New signature:
```go
// OpenPipelineLog returns: screen (status writer around os.Stdout, no log fan-out),
// logFile (the raw log file or nil when disabled), termOut (raw os.Stdout for cursor
// ANSI, or io.Discard when not a TTY), logPath (display string), cleanup, error.
func OpenPipelineLog(workDir, name string, enabled bool) (
    screen *render.Writer,
    logFile io.Writer,
    termOut io.Writer,
    logPath string,
    cleanup func(),
    err error,
)
```

Behaviour:
- `enabled=false`: `screen = render.Stdout()`, `logFile = nil`, `termOut = os.Stdout` (when TTY) or `io.Discard` (non-TTY), no cleanup.
- `enabled=true`: `screen = render.NewWriter(os.Stdout)` (NOT `MultiWriter(os.Stdout, logFile)` anymore), `logFile = the raw *os.File`, `termOut = os.Stdout` (when TTY) or `io.Discard` (non-TTY). `PlainReporter` wraps `logFile` with `logSanitizer{}` (from Task 1: strips ANSI, converts `\r` to `\n`) internally — the file on disk receives clean line-terminated text.

`NewPlainReporter` signature:
```go
func NewPlainReporter(screen *render.Writer, logFile io.Writer, termOut io.Writer) *PlainReporter
```

Checklist:
- [x] change `OpenPipelineLog` to the new signature
- [x] update call sites: `internal/command/deploy.go:214`, `internal/command/reset.go:134`, `internal/lifecycle/phases.go:41` — capture three writers + logPath + cleanup
- [x] update `pipeline.NewPlainReporter` to accept three writers; store `screen.Writer()`, the ANSI-stripped `logFile`, and `termOut` internally for LiveLine construction in Task 5
- [x] update `logging_test.go` cases (`TestOpenPipelineLog_CreatesDevboxLogsDirectory`, `TestOpenPipelineLog_DisabledReturnsNil`, and the file-content-assertion test near line 247) to match the new signature
- [x] update `plain_test.go` `newBufReporter` helper: pass a `bytes.Buffer` as screen, `nil` as logFile (or a separate buffer when log-content assertion is desired), `io.Discard` as termOut (existing tests stay byte-for-byte deterministic in non-TTY mode)
- [x] write a new test verifying that writing ANSI through `screen` does NOT touch `logFile` (because screen no longer tees), and that PlainReporter status lines reach the log file exactly once via its dedicated side-write
- [x] run `go test ./internal/pipeline/... ./internal/command/... ./internal/lifecycle/...` — must pass before Task 4

### Task 4: PTY allocation in parallel `childIO`

Goal: when a sub-step runs in parallel mode AND `stdoutIsTTY()` is true, allocate a PTY for the child so it emits proper progressbars; capture master → `logSanitizer`-wrapped log destinations + raw-frames-to-tee path (set up in Task 1). When PTY allocation fails or stdout is not a TTY, fall back to the existing direct path.

- [x] extend `childIO()` in `internal/pipeline/executor.go`: in the `parallel=true` branch, when `stdoutIsTTY()` is true, `pty.Open()` a PTY; on success, return the slave as stdout/stderr and start a goroutine `io.Copy(logWriter, ptmx)` — `logWriter` here is the already-properly-fan-out joinWriters from Task 1; cleanup closes slave, waits for copy goroutine, closes master
- [x] on PTY error, fall back transparently to the existing direct path (return `logWriter` for both stdout and stderr, no-op cleanup)
- [x] write unit tests in `executor_test.go` for the new parallel+TTY branch: stub `stdoutIsTTY = func() bool { return true }` and verify a `*os.File` is returned (slave fd); also test the parallel+non-TTY fallback returns the wrapped writer directly
- [x] write a goroutine-leak smoke test: open the PTY path, write some bytes, call cleanup, assert the copy goroutine returned (goleak in TestMain catches this automatically if it hangs)
- [x] run `go test ./internal/pipeline/...` — must pass before Task 5

### Task 5: `LiveLine` core (single-line sticky footer)

Goal: new file `internal/pipeline/liveline.go` — owns the bottom-of-cursor footer. Bubbles spinner driven by an internal `time.Ticker` (no `tea.Program`). Split-channel writer model (termOut for cursor ANSI, screen for data lines on stdout). Log writes happen OUTSIDE LiveLine, in PlainReporter, as a separate side-channel per Println.

Public API:
```go
type LiveLine struct { ... }
func NewLiveLine(termOut, screen io.Writer, enabled bool) *LiveLine
func (l *LiveLine) Start()
func (l *LiveLine) Stop()                 // MUST be called from outside any LiveLine callback (see Invariant #5)
func (l *LiveLine) Pause()                // erase footer, keep state — for prompt handoff
func (l *LiveLine) Resume()               // re-draw after Pause
func (l *LiveLine) SetText(s string)      // update single-line footer text
func (l *LiveLine) Println(rawLine string) // print line above footer (writes to screen; clears+redraws footer on termOut)
```

Cursor invariant (matches Invariant #9 in Overview): after ANY public method returns, the terminal cursor is at column 0 of the row IMMEDIATELY BELOW the footer (or below the lowest block row + footer, in block mode). The footer is NOT the cursor row.

Behaviour:
- When `enabled=false`: `SetText` is a no-op, `Println` writes `rawLine + "\n"` directly to `screen`, `Start`/`Stop`/`Pause`/`Resume` are no-ops. No ticker goroutine started.
- When `enabled=true`:
  - `Start()` (atomic, idempotent): write initial footer to `termOut` as `\x1b[?25l<spinner> <text>\n` — cursor ends below footer (invariant). Start ticker goroutine.
  - `redraw()` (ticker callback, single-line mode): take mutex → advance spinner → write `\x1b[1A\r\x1b[2K<spinner> <text>\n` to `termOut` (up one row to footer, clear, write new footer, newline brings cursor below footer) → release.
  - `Println(line)` single-line mode:
    1. Take mutex
    2. termOut: `\x1b[1A\r\x1b[2K` (cursor up to footer row, clear footer line)
    3. screen: `line + "\n"` (write data on the now-empty footer row, cursor advances to next row)
    4. termOut: `<spinner> <text>\n` (draw footer at the new row, cursor advances below footer)
    5. Release mutex.
    Net effect: data line appears in scrollback, footer follows below, cursor below footer.
  - `Stop()`: **idempotent — safe to call multiple times**; **must be called from outside any LiveLine method** (the reporter calls it via `defer` in FinishPipeline AND from PlainReporter.Close, never from inside a Println callback chain). Sequence:
    1. `stopOnce.Do(...)` guarantees the body runs ONCE; subsequent calls are pure no-ops (return immediately). This is the explicit idempotency guarantee that Task 7 (FinishPipeline defer) and Task 10 (PlainReporter.Close) both rely on.
    2. close `stopCh` (no mutex held)
    3. `<-doneCh` (wait for ticker to exit; ticker selects on `stopCh` outside the mutex region)
    4. Take mutex → emit `\x1b[1A\r\x1b[2K\x1b[?25h` (up to footer, clear, show cursor) → set `enabled=false` → release
  - `Pause()` (**INTENTIONAL EXCEPTION** to Invariant #9 — cursor ends ON the cleared footer row, NOT below it): take mutex → emit `\x1b[1A\r\x1b[2K\x1b[?25h` (up to footer row, clear, show cursor; cursor stays at column 0 of the now-empty former-footer row) → set `paused=true` → release. The cursor MUST be at the cleared footer row because huh-based prompts (the only caller path) render starting from the current cursor position; leaving the cursor below would result in a blank gap line above the prompt. Ticker checks `paused` under mutex and skips redraw.
  - `Resume()` (restores Invariant #9): take mutex → set `paused=false` → emit `\x1b[?25l<spinner> <text>\n` (hide cursor, paint footer at the cleared former-footer row, `\n` advances cursor below) → release.

Implementation notes:
- Use `charm.land/bubbles/v2/spinner` with `WithSpinner(spinner.MiniDot)`; style via `ui.Theme()` foreground colour.
- Truncate `text` to `term.GetSize(stdoutFd)` width minus spinner width. On size-query error, default to 80.
- Ticker FPS = 10 (100ms period). Single ticker.
- Hide cursor on Start (`\x1b[?25l`), show on Stop / Pause (`\x1b[?25h`).
- `termOut` writes are best-effort (`_ = w.Write(...)` — never panic on broken stdout).
- `Stop` is documented as "not safe to call from inside Println/SetText/redraw callbacks". The only caller chain is `Reporter.FinishPipeline → l.Stop()` from the top of the pipeline lifecycle, which never holds the LiveLine mutex.

Files:
- new `internal/pipeline/liveline.go`
- new `internal/pipeline/liveline_test.go`

Checklist:
- [x] create `LiveLine` struct with the two-writer split (`termOut`, `screen`), `sync.Mutex`, `stopCh`, `doneCh`, `stopOnce`
- [x] implement `Start` (idempotent, starts ticker, initial footer write); `Stop` per the sequence above; `Pause`/`Resume` via a `paused` flag
- [x] implement `SetText` (updates state under mutex; next redraw picks it up)
- [x] implement `Println` with the up-clear / write-data / redraw sequence in that exact order
- [x] implement `redraw` matching the cursor invariant (cursor ends below footer, not on it)
- [x] integrate `bubbles/v2/spinner` standalone — synthesize `spinner.TickMsg{ID: m.ID()}` on each tick, pass through `m.Update`, discard returned Cmd
- [x] truncate `text` width-aware via `lipgloss/v2.Width`
- [x] write **channel-separation tests** in `liveline_test.go` for `enabled=true`: termOut and screen are TWO separate `bytes.Buffer`s; substitute `time.Ticker` with a deterministic step function (`tick()` method exposed via `testHooks` field); assert that termOut received only ANSI / spinner sequences and screen received only data lines from `Println`
- [x] write **cursor-invariant tests** using `termGrid`: termOut and screen are the SAME `*termGrid` instance (consumes both byte streams in write order, mirroring real-terminal behaviour); after each operation, assert (a) grid contents row-by-row, (b) cursor position matches Invariant #9 (or the Pause exception)
- [x] write unit tests for `enabled=false` (no-op) path: only `Println` writes to screen, termOut receives nothing
- [x] write a concurrency test: 100 concurrent `Println` calls + 100 `SetText` calls + 10ms ticker produce well-formed output (every "data" line on its own row in screen, no torn writes in termOut); verify the goroutine count returns to baseline after `Stop()` via `goleak`
- [x] write a `goleak`-asserting test: `Start` → `Stop` cycle leaves zero leaked goroutines
- [x] write an idempotency test: `Start` → `Stop` → `Stop` (second call is a no-op via `stopOnce`); also `Start` → `Stop` → `Println("late")` (second-life writes after Stop are safe no-ops, NOT panics — `enabled=false` after Stop)
- [x] write a Pause-exception test using `termGrid` (single-grid mode): assert cursor is on the cleared former-footer row after `Pause()` (NOT below it); assert `Resume()` restores the invariant (cursor below new footer); document the exception inline as a comment near the `Pause` method body
- [x] run `go test ./internal/pipeline/...` — must pass before Task 6

### Task 6: Route ALL child output via `Reporter.StepOutput` (single durable path)

Goal: today's executor calls `Reporter.SuspendForExec` / `ResumeAfterExec` around every non-parallel step body (executor.go:649,668) — this is too coarse. Instead, sequential steps route their child stdout through a per-step `lineTee` that calls `Reporter.StepOutput(addr, frame, final)`, the same path parallel sub-steps already use. The footer stays visible during the entire step; child output flows through the same mutex as the footer redraw. CRITICAL: child output reaches each destination (screen, log file, per-sub-step file) EXACTLY ONCE.

**Single durable path (no fan-out duplication):**

- `childIO` for **sequential mode** returns ONLY the per-step `lineTee` as stdout/stderr. NOT teed with `opts.LogWriter`. The global pipeline log file receives sequential child output through `PlainReporter.StepOutput`'s `final=true` path (which writes one line to the log via the dedicated side-write next to `live.Println`).
- `childIO` for **parallel mode** returns a writer that fans to `(logSanitizer{subFile}, tee)` — per-sub-step log file gets every byte (ANSI stripped, `\r`→`\n` normalized), tee feeds reporter. The global pipeline log file does NOT receive parallel child output via childIO; it receives it through `PlainReporter.StepOutput`'s `final=true` path, EXACTLY ONCE per logical line.
- Reporter status lines (`StartStep`/`FinishStep`/etc.) write to `screen` via `live.Println` AND to the log file via `r.writeLog(line)` — separate side-writes, one copy each.

Checklist:
- [x] remove the `if !opts.Parallel { opts.Reporter.SuspendForExec() ... ResumeAfterExec() }` block in `executeStepBody` (executor.go:647-669)
- [x] (no struct change needed) — `RunOptions.Reporter Reporter` already exists at executor.go:299; the lineTee callback uses the EXISTING `opts.Reporter` field
- [x] in `executeStepBody`, create a per-step lineTee: `tee := newLineTee(func(frame string, final bool) { opts.Reporter.StepOutput(addr, frame, final) })`; `defer tee.Flush()`; build `stepWriter := &ansiOnlyStripper{tee}` and store on a new `ActionContext.StepWriter io.Writer` field
- [x] **change `childIO` signature** from `childIO(logWriter io.Writer, parallel bool) (stdout, stderr io.Writer, cleanup func())` to `childIO(stepWriter io.Writer, parallel bool) (stdout, stderr io.Writer, cleanup func())` — the input is now ALWAYS the step's tee target, regardless of sequential/parallel mode
- [x] in `childIO` sequential branch (no PTY-fallback case): return `stepWriter` for both stdout and stderr; NO os.Stdout fan-out, NO opts.LogWriter fan-out (it's no longer a parameter)
- [x] in `childIO` sequential+TTY branch (PTY path): allocate PTY, copy master → `stepWriter` (NOT to `io.MultiWriter(os.Stdout, logSanitizer{logWriter})`); return slave for stdout/stderr; cleanup as before
- [x] in `childIO` parallel branch (TTY): allocate PTY, copy master → `stepWriter`; return slave for stdout/stderr (Task 4 already added this; just adapt to new signature)
- [x] in `childIO` parallel branch (non-TTY fallback): return `stepWriter` directly
- [x] update all `childIO` call sites: `execShellAction`, `execDevboxAction`, `execCommandAction` — each now passes `actx.StepWriter` instead of `actx.LogWriter`
- [x] **`execBuiltinAction` is the exception** (executor.go:212): it does NOT call `childIO`. It constructs `ectx.Output = render.NewWriter(out)` where `out` is a writer assembled from `actx.LogWriter` + `os.Stdout`. Builtins write via `ectx.Output.{Success,Info,Warning,Error}` (all builtins do this; none use `ectx.LogWriter` directly — verified by `grep -rn "ectx.LogWriter" internal/builtin` returns zero hits). To route builtin output through the same pipeline:
  - replace the entire `out`-assembly switch at executor.go:212-223 with: `ectx.Output = render.NewWriter(actx.StepWriter)` — single line; no more `os.Stdout` fan-out, no `ansiStripper`, no parallel/non-parallel branching
  - remove `LogWriter: actx.LogWriter` from the ExecContext literal (and consider removing the `LogWriter io.Writer` field from `builtin.ExecContext` entirely since no builtin uses it — fold this cleanup into the same task)
  - keep `Stdin: stdinForBuiltin` as-is (interactive builtins like `confirm` still read stdin)
  - net effect: `ectx.Output.Success("config foo → bar")` from `configs_copy` (and similar lines from `dirs_ensure`, `message`, `paths`, `volumes`, etc.) is now written to `StepWriter` → `lineTee` → `Reporter.StepOutput(addr, "config foo → bar", true)` → `LiveLine.Println("config foo → bar")` — flows through the LiveLine mutex like every other child line, never bypassing the footer
- [x] update `ExecStep` deprecated wrapper (executor.go:271): take a `stepWriter` parameter or compute it internally from the supplied reporter — whichever keeps backward-compat for any external callers
- [x] in `runParallelSubStep`: update `subWriter` to be `joinWriters(logSanitizer{subFile}, tee)` only (drop `opts.LogWriter` from this join); set `subOpts.LogWriter = subWriter`; the existing `subOpts.Reporter = opts.Reporter` propagation already works (it's a normal struct copy) — implementation note: the per-step tee is centralised in executeStepBody, so runParallelSubStep now sets `subOpts.LogWriter = &logSanitizer{subFile}` only; the executor folds it into the StepWriter's downstream destinations
- [x] add a test for `execBuiltinAction`-via-StepOutput specifically: invoke a `message` builtin with a payload `"hello world"`; assert `Reporter.StepOutput(addr, "hello world", true)` fires — proves builtins are not bypassing LiveLine
- [x] in `PlainReporter.StepOutput`: when not in a parallel block, `final=true` → `live.Println(frame)` + `r.writeLog(frame)`; `final=false` → `live.SetText("[N/M] <addr>: " + truncate(frame))` + set `inProgress[addr] = frame` (display state only; NOT committed; committed centrally by `commitTrailingTail` at FinishStep — see Task 9). When in a parallel block, route to block row (Task 9) and `final=true` also writes to the log file via `writeLog`. — implementation note: until Task 7 wires LiveLine, sequential `final=true` writes directly to the screen via `fmt.Fprintln`; LiveLine.Println takes over in Task 7.
- [x] PlainReporter's `FinishStep`/`FailStep`/`SkipStep` for SEQUENTIAL steps (non-block-mode) call `r.commitTrailingTail(addr)` BEFORE emitting the status line. The helper (defined in Task 9) for sequential addr does `live.Println(tail) + writeLog(tail) + reset inProgress` — preserves the tail in both screen scrollback and log, single copy each, regardless of whether the step succeeded, failed, or skipped.
- [x] add `r.writeLog(line string)` helper on PlainReporter that writes to `r.logFile` when non-nil; ensure `r.logFile` is the ANSI+CR-stripped wrapper from Task 3
- [x] track "currently running step" state in PlainReporter (`currentStepAddr string`, `inBlockMode bool`, `blockGroupAddr string`); set in `StartStep`/`StartGroup`, cleared in `FinishStep`/`FinishGroup`
- [x] update existing tests in `executor_test.go` that asserted Suspend/Resume calls — those assertions are removed; new tests assert that step output reaches the reporter via `StepOutput`
- [x] write integration tests for sequential steps: drive a fake child whose stdout emits `"hello\n"` and `"progress 50%\rprogress 100%\ndone\n"`; assert PlainReporter receives the expected `StepOutput` sequence AND the log file contains each line exactly once
- [x] write tests asserting NO duplication: run a 2-step pipeline with both sequential and parallel steps, count occurrences of each output line in the log file — must be exactly 1. Cover ALL dump paths AND the trailing-tail commit explicitly:
  - **non-TTY mode** (dump always runs): each `final=true` line is `writeLog`'d at commit time AND replayed to screen via dump — log must contain it ONCE, not twice
  - **TTY+failure mode** (dump always runs): same assertion — failed sub-step's log lines appear once each
  - **TTY+success+log-disabled mode** (dump runs): same assertion (log is nil so trivially "once", but the assertion guards against accidentally adding a writeLog inside the dump helper)
  - **trailing-tail × dump-runs** case: feed child output ending mid-row (no final `\n`); assert the trailing tail appears in the global log EXACTLY ONCE (committed by `commitTrailingTail`, replayed screen-only by dump helper)
  - **trailing-tail × dump-suppressed** case (TTY+success+log-enabled): feed same kind of input; assert the trailing tail STILL reaches the global log exactly once (this is the regression case the user surfaced — `commitTrailingTail` runs before the dump-policy decision, so the tail is in the log even when the dump itself is suppressed) AND the screen shows the "Full log: <path>" link
  - **sequential trailing-tail** case: feed sequential child whose stdout ends without `\n`; assert the tail reaches BOTH the screen (via `live.Println`) and the global log exactly once (via `commitTrailingTail` for the sequential branch)
- [x] run `go test ./internal/pipeline/...` — must pass before Task 7

### Task 7: Integrate `LiveLine` into `PlainReporter` (sequential lifecycle)

Goal: wire `LiveLine` into `PlainReporter` for the sequential (non-parallel) lifecycle. Every reporter event routes through `LiveLine.Println` (for status lines) or `LiveLine.SetText` (for the "what's running now" footer). Fix the `FinishPipeline(false)` early-return bug so Stop is always called.

- [x] add `live *LiveLine` and `logFile io.Writer` fields to `PlainReporter`; in `NewPlainReporter(screen, logFile, termOut)` construct LiveLine with `enabled = (termOut != io.Discard)`, `termOut`, `screen.Writer()`; wrap `logFile` with `logSanitizer{}` (Task 1) and store on `r.logFile` (nil when log disabled)
- [x] add `r.writeLog(line string)` helper that no-ops when `r.logFile == nil`, else writes `line + "\n"` to it
- [x] in `StartPipeline`: call `r.live.Start()` and `r.live.SetText("Starting <name>...")`
- [x] replace direct `fmt.Fprintf(r.w.Writer(), ...)` calls in `emit` with `r.live.Println(line)` (screen-only) + `r.writeLog(line)` (log-only) — single copy each
- [x] in `EnterPhase`: `r.live.SetText("<phase>: <description>")` — pure state update, no Println (the phase header status line is still emitted via the existing emit path)
- [x] in `StartStep`: emit the existing `· [N/M] <addr>` line via `emit` (so it goes to screen + log), AND `r.live.SetText("[N/M] <addr>: <description>")`
- [x] **fix FinishPipeline failure path** (plain.go:248 currently `if !success { return }`): restructure so `defer r.live.Stop()` is registered BEFORE the early-return guard; both success and failure paths Stop the live line
- [x] in `FinishPipeline(true)`: defer Stop, then emit the final "Done (1m 23s)" line via `emit`
- [x] in `FinishPipeline(false)`: defer Stop, then return silently (failure already reported by `FailStep`)
- [x] update `plain_test.go`'s `newBufReporter` to construct `NewPlainReporter(screen, nil, io.Discard)` — termOut=io.Discard means LiveLine is disabled and existing byte-for-byte assertions keep working; logFile=nil means side-writes no-op
- [x] add new tests covering the TTY path: feed lifecycle events to a PlainReporter where termOut is a `termGrid` (see Task 8), assert footer appears after StartPipeline, updates per phase/step, and disappears after FinishPipeline (both success and failure paths)
- [x] add tests confirming log file gets each status line exactly once even when LiveLine is enabled
- [x] run `go test ./internal/pipeline/...` — must pass before Task 8

### Task 8: `LiveBlock` — multi-row block for parallel groups (with physical row reservation)

Goal: extend `LiveLine` to support a multi-row block above the single footer line. When a parallel group is active, N rows are physically reserved (StartBlock writes N empty lines so the block area exists in the viewport — it is NOT state-only). Each row is independently updatable via `SetBlockRow(idx, content)`. Footer line stays at the bottom of the block.

API additions:
```go
func (l *LiveLine) StartBlock(rows int) // physically reserve N rows above footer; the footer moves down N rows
func (l *LiveLine) SetBlockRow(idx int, content string)
func (l *LiveLine) EndBlock()           // freeze block (rows stay in scrollback as a permanent record); LiveLine returns to single-line mode below the frozen block
```

Behaviour (cursor invariant: cursor always below lowest owned row after every method returns):

**Initial state (before StartBlock)**: footer at row F, cursor at row F+1.

**`StartBlock(N)`** (physically reserves N rows):
1. Take mutex.
2. Erase current footer: termOut `\x1b[1A\r\x1b[2K` (cursor at row F col 0, footer gone).
3. Reserve N rows by writing N newlines to termOut: `"\n" × N` (cursor at row F+N, terminal scrolls if needed).
4. Set state: `blockRows = N`, `blockContent = make([]string, N)`.
5. Paint footer at the new position: termOut `<spinner> <text>\n` (cursor at row F+N+1).
6. Release mutex.

End state: rows F..F+N-1 are empty block rows, row F+N is the footer, cursor at row F+N+1. Invariant holds.

**`SetBlockRow(idx, content)`**: update `blockContent[idx]` under mutex, trigger immediate redraw.

**`redraw()` in block mode** (paints all owned rows top-to-bottom):
1. From cursor at row (F+N+1), go up `blockRows + 1 = N+1` rows: termOut `\x1b[<N+1>A` (cursor at row F).
2. For each i in 0..N-1: termOut `\r\x1b[2K<row-icon-or-spinner> <blockContent[i]>\n` (cursor advances row by row).
3. Footer: termOut `\r\x1b[2K<spinner> <text>\n` (cursor at row F+N+1).
4. Release mutex.

**`Println(line)` in block mode** (insert line above the block, scroll content down):
1. Clear all owned rows: termOut `\x1b[<N+1>A` (up to row F), then `(\r\x1b[2K\n) × (N+1)` (clear each owned row, advance; cursor ends at row F+N+1).
2. Move back to top of cleared area: termOut `\x1b[<N+1>A` (cursor at row F).
3. screen: `line + "\n"` (data lands at row F; cursor at row F+1).
4. Paint block + footer starting at row F+1: for each i: termOut `<row-icon> <blockContent[i]>\n`, then footer `<spinner> <text>\n`. Cursor ends at row F+N+2.
5. Release mutex.

End state: row F has `line` (now in scrollback record), rows F+1..F+N have block, row F+N+1 has footer, cursor at F+N+2. Invariant holds.

**`EndBlock()`** (freeze block in scrollback, return to single-line mode):
1. Take mutex.
2. Cursor is at row F+N+1 (below footer). The block (rows F..F+N-1) and footer (row F+N) stay as they are — they become permanent scrollback content.
3. To re-enter single-line mode, paint a fresh single-line footer at row F+N+1 (the row the cursor is on): termOut `<spinner> <text>\n` (cursor at F+N+2).
4. Reset state: `blockRows = 0`, `blockContent = nil`.
5. Release mutex.

End state: row F+N is the OLD footer (frozen with whatever final text it had), row F+N+1 is the NEW single-line footer, cursor at row F+N+2. Subsequent operations (Println, redraw) operate on the new footer.

Checklist:
- [x] add `blockRows int`, `blockContent []string` state to `LiveLine`
- [x] implement `StartBlock` with the physical-reservation sequence above (NOT state-only)
- [x] implement `SetBlockRow` (state update + immediate redraw)
- [x] implement `EndBlock` (freezes existing block, paints new single-line footer below)
- [x] update `redraw()` to handle both single-line and block modes per the sequences above
- [x] update `Println` to handle both single-line and block modes per the sequences above
- [x] build a `termGrid` test helper in `liveline_test.go` (or a separate `termgrid_test.go`) — virtual `[][]rune` of fixed height that consumes `\r`, `\n` (with auto-scroll when cursor exceeds height), `\x1b[2K`, `\x1b[<N>A`, `\x1b[<N>B`; returns final cell contents row-by-row; the helper also models cursor position so tests can assert it
- [x] write unit tests for block-mode operations using `termGrid`: after `StartBlock(3)` cursor is at the row immediately below the new footer (which is 3 block rows + 1 footer = 4 rows below the original footer position); after `SetBlockRow(0, "a") + SetBlockRow(1, "b") + SetBlockRow(2, "c")` rows show a/b/c + footer; after `Println("data")` the grid shows: `data` in scrollback row, then 3 block rows with set content, then footer; after `EndBlock` the block + old-footer rows persist, a new footer is at the row the cursor was on
- [x] write tests for transitions: single → block → single (block leaves scrollback record, single-line continues below)
- [x] write tests for multiple block sessions in sequence (a parallel group followed by another parallel group); assert both blocks persist in scrollback
- [x] write a test where the block's bottom row pushes against the viewport bottom — viewport scrolls correctly (use a small terminal height in `termGrid`)
- [x] run `go test ./internal/pipeline/...` — must pass before Task 9

### Task 9: `PlainReporter` parallel group integration

Goal: in `StartGroup` switch the LiveLine into block mode; route `StepOutput(subAddr, frame, final)` to the corresponding block row; in `FinishStep`/`FailStep`/`SkipStep` for a sub-step update the row to the final glyph; in `FinishGroup` end the block and emit the summary line.

- [x] in `StartGroup`: call `r.live.StartBlock(len(subIndices))` after emitting the group header; build a map `subAddr → blockRowIdx` so StepOutput can find the row; transition reporter state into "block mode for group=<addr>"
- [x] in `StepOutput`: when in block mode AND `subAddr` is in the map, call `r.live.SetBlockRow(idx, formatRow(spinner-glyph, subIdx, subTotal, subName, frame))` — the row icon during execution is a spinner glyph; truncated to terminal width
- [x] in `FinishStep` for a sub-step (detect via `r.subs[addr]` presence): call `r.live.SetBlockRow(idx, formatRow("✓", ..., "Done: <subName>"))` and update group counters
- [x] in `FailStep` for a sub-step: call `r.live.SetBlockRow(idx, formatRow("✗", ..., "Failed: <subName>"))`; print the error message via `r.live.Println(...)` so it lands above the block (will scroll up)
- [x] in `SkipStep` for a sub-step: `r.live.SetBlockRow(idx, formatRow("◎", ..., "Skipped: <subName> (<reason>)"))`
- [x] in `FinishGroup`: call `r.live.EndBlock()` (block rows persist in scrollback); then emit the aggregate-summary line via `Println`; transition reporter state out of block mode
- [x] non-final `\r` frames update the row in-place (NO log write, NO buffer commit — purely ephemeral `inProgress = frame`); on `final=true`, the frame is committed to the per-sub-step buffer AND written to the global log via `r.writeLog(frame)` (this is the SINGLE writeLog call per logical line — see "exactly once" guarantee below)
- [x] **introduce `r.commitTrailingTail(subAddr)` — the SINGLE commit point for any leftover `inProgress` tail**. Called as the FIRST step at every sub-step finish event (`FinishStep`/`FailStep`/`SkipStep`), BEFORE the buffer-dump policy decision. Behaviour:
  - if `r.subs[subAddr].inProgress == ""`: no-op
  - else: take the tail (`t := inProgress`)
    - append `t + "\n"` to the per-sub-step buffer (so it appears in any dump replay that follows)
    - `r.writeLog(t)` (the single canonical log entry for this tail — it was never `final=true`)
    - reset `inProgress = ""`
  - This is the ONLY place tails get committed. `StepOutput(final=false)` does not commit. `dumpSubStepBufferLocked` does not commit. Tests pin this property.
- [x] **buffer-dump policy for full sub-step output** (decided AFTER `commitTrailingTail` has flushed any tail, so the buffer is complete):
  - non-TTY mode: ALWAYS dump every sub-step buffer between `───── output ─────` bars on `FinishStep`/`FailStep`/`SkipStep` (current behaviour preserved; Task 2 ensures dumps are `\r`-spam-free)
  - TTY mode, sub-step **FAILED** (`FailStep`): ALWAYS dump the failed sub-step's buffer between bars — the live block only showed final glyph + last frame, but the user needs the full history to diagnose
  - TTY mode, sub-step **succeeded or skipped** AND per-sub-step log file path is known (set via `SetSubStepLogPath`): SUPPRESS the buffer dump, instead emit ONE `r.live.Println("  Full log: " + subStepLogPath)` line in `FinishStep`/`SkipStep` so users know where to look. Even with dump suppressed, the tail has ALREADY been logged via `commitTrailingTail` — no data loss.
  - TTY mode, sub-step **succeeded or skipped** AND per-sub-step log file path is empty (log disabled at pipeline level — `OpenSubStepLog` returned nil): ALWAYS dump the buffer — there is no other record of full output on disk
  - keep `parallelOutputTopBar`/`parallelOutputBotBar` constants; they are still used for the dump path
- [x] **`dumpSubStepBufferLocked(subAddr)` is PURE SCREEN REPLAY**. The buffer is complete after `commitTrailingTail` has run; the buffer's contents (including any committed tail) were already `writeLog`'d at their respective commit moments. Re-writing during replay would double-log. Therefore:
  - emit top bar `───── output ─────` via `live.Println + writeLog` (the bar line itself is a status line, one screen + one log copy)
  - for each line in the per-sub-step buffer: `live.Println(line)` ALONE — NO `writeLog`, NO special trailing-tail branch (the tail is already a regular buffered line because `commitTrailingTail` appended it before dump-policy ran)
  - emit bottom bar `──────────────────` via `live.Println + writeLog`
- [x] every call site that previously contained ad-hoc tail handling now uses `commitTrailingTail` instead: `FinishStep`, `FailStep`, `SkipStep` (sub-step path) all call `commitTrailingTail(subAddr)` first thing. For sequential (Task 6) `FinishStep`/`FailStep`/`SkipStep` also call `commitTrailingTail(stepAddr)` — for sequential the helper writes the tail to `live.Println(tail) + writeLog(tail)` directly (no buffer involved), but the API is uniform.
- [x] **add a new Reporter method** `SetSubStepLogPath(subAddr, path string)` to `Reporter` interface — the path of the per-sub-step log file is known only AFTER `OpenSubStepLog` runs inside each sub-step goroutine in `runParallelSubStep` (executor.go:823), strictly later than `StartGroup`. The reporter cannot infer the path; the runner must push it.
- [x] in `runParallelSubStep` (executor.go) right after the successful `OpenSubStepLog` call: `opts.Reporter.SetSubStepLogPath(subAddr, logPath)`. When `subFile` is nil (log disabled), call with empty `path` (no-op for PlainReporter; or skip the call) — TBD pick one convention; the reporter's `subStepEntry.logPath` is empty by default so skipping is fine.
- [x] in `PlainReporter`: implement `SetSubStepLogPath` to store `path` on the existing `r.subs[subAddr].logPath` field (the field is added on the `subStepEntry` struct in this task)
- [x] write integration tests with `termGrid`: simulate a 3-sub-step parallel group; assert block rows appear, get updated per StepOutput, finalize with icons, summary line appears below
- [x] write a glyph-discipline regression test: feed multiple `final=true` frames for a still-running sub-step (no FinishStep yet); assert each redraw of that block row shows the SPINNER glyph, NEVER `✓` — `✓` may appear only after `FinishStep` is called. Same for `✗` (only after `FailStep`) and `◎` (only after `SkipStep`).
- [x] write a TTY-failure test: simulate a failed sub-step; assert its buffer IS dumped between bars (full output visible) — regression guard
- [x] write a TTY-success-log-enabled test: simulate a successful sub-step with a non-nil log path; assert `Full log: <path>` line appears AND buffer is NOT dumped
- [x] write a TTY-success-log-disabled test: simulate a successful sub-step with no log file; assert buffer IS dumped
- [x] write tests for non-TTY parity: same simulation with `enabled=false`; assert clean buffer dumps without `\r`-spam
- [x] run `go test ./internal/pipeline/...` — must pass before Task 10

### Task 10: Package-level huh hooks in `internal/ui` (pause LiveLine for ALL prompts)

Goal: pause LiveLine around every huh-based prompt regardless of where it is invoked from. Threading function fields through `RunContext`/`ExecContext`/`ActionContext` is invasive and breaks for indirect callers; **package-level hooks in `internal/ui` are simpler and cover every entry point automatically** (pipeline `confirm` builtin → `runConfirm`, user-command `confirmation.go` → `runConfirm`, workflow `runner_workflow.go:runConfirmStep` → `runConfirm`, user-command `runner_builtin.go` builtin executions, and any future `ui.RunSelector` / `ui.RunMultiSelect` use).

- [x] in `internal/ui/huh.go`: add a package-private `sync.RWMutex` plus `huhBeforeHook func()` and `huhAfterHook func()` variables; add public `SetHuhHooks(before, after func())` (Lock + write both vars together so callers always see a consistent pair) and `ClearHuhHooks()` (Lock + set both to nil); add a package-private `snapshotHuhHooks() (before, after func())` helper that RLocks once and returns the current pair as locals so callers do not re-read the globals between `before()` and `after()`
- [x] in each of `internal/ui/{confirm.go, selector.go, multiselect.go}` (whichever entry points call `tea.NewProgram(...).Run()` for huh): take a snapshot `before, after := snapshotHuhHooks()` once at entry; invoke `if before != nil { before() }` before `Run`; `defer func() { if after != nil { after() } }()` so the after-hook fires even on huh error/cancel/panic. Crucially, the deferred call uses the SNAPSHOTTED `after`, not a re-read of the global — so SetHuhHooks/ClearHuhHooks calls mid-prompt cannot break the pairing.
- [x] in `pipeline.NewPlainReporter`: after constructing `r.live`, call `ui.SetHuhHooks(r.live.Pause, r.live.Resume)`
- [x] add a `Close()` method to `PlainReporter` that calls `r.live.Stop()` (idempotent — already called by FinishPipeline in normal flow; this is a safety-net for panics or early returns where FinishPipeline did not run) AND `ui.ClearHuhHooks()`. Document inline that the double-call to `Stop()` is intentional defensive coding: `FinishPipeline`'s defer guarantees Stop on normal completion, `Close` guarantees Stop on any return path. Both rely on `stopOnce` to no-op the redundant call.
- [x] call `Close()` via `defer` in deploy/reset/lifecycle command call sites (after the existing log cleanup `defer cleanup()`)
- [x] **remove** `SuspendForExec` and `ResumeAfterExec` from the `Reporter` interface (`reporter.go`) AND from `PlainReporter` — they are no longer called by the executor (already removed in Task 6) and the hook approach replaces them
- [x] update any `Reporter` mock implementations in tests to remove the now-defunct methods (search `grep -rn "SuspendForExec\|ResumeAfterExec" internal/`)
- [x] add unit tests in `internal/ui/huh_test.go` verifying that hooks fire before and after each prompt call; verify nil hooks are safe; verify the snapshot pairing — concurrent `SetHuhHooks(nil, nil)` mid-prompt does NOT skip the matching `after` call (race regression guard)
- [x] add a `go test -race`-clean concurrency test: 100 goroutines spam `SetHuhHooks`/`ClearHuhHooks` while another 100 call `snapshotHuhHooks` + invoke; the race detector must remain silent
- [x] add unit tests in `plain_test.go` verifying that `NewPlainReporter` registers hooks and `Close` clears them; verify `live.Pause` / `live.Resume` are wired correctly by simulating a hook call and asserting the LiveLine state change (footer erased / re-painted using `termGrid`)
- [x] add an integration test: drive a workflow with a `confirm` step → assert hooks fired, footer was paused/resumed
- [x] document the constraint clearly in code comments: "only one PlainReporter active per process; nested deploys are not supported by the global hook design"
- [x] run `go test ./...` — must pass before Task 11

### Task 11: Verify acceptance criteria

- [x] verify all requirements from Overview are implemented: sticky footer present, parallel block working, no Ctrl+C regressions, non-TTY fall-back clean — implementation across Tasks 1-10 covers all four; manual real-terminal smoke test deferred to Post-Completion
- [x] verify edge cases: empty pipeline, single-step pipeline, parallel group with 1 sub-step, parallel group with 10 sub-steps, sub-step that fails fast, FinishPipeline on both success and failure paths — covered by existing tests (`executor_parallel_test.go` FailFast variants, `plain_test.go` FinishPipeline(false) regression, parallel-group integration tests)
- [x] verify SIGINT behaviour (skipped - not automatable in unit-test scope; subprocess-based signal tests are flaky in CI and the path is exercised by `RunWithOptions`'s `signal.NotifyContext` + `cmd.Cancel`/`WaitDelay` already validated by `executor_parallel_test.go` cancellation paths)
- [x] verify log file content: after a full deploy, the log file contains zero `\x1b[` byte sequences AND zero `\r` characters — pinned by Task 1 `logSanitizer` tests (`logging_test.go`) and Task 6 single-copy integration tests
- [x] verify NO duplicates — covered by Task 6 dump-path tests (non-TTY, TTY+failure, TTY+success+log-enabled, trailing-tail × dump-runs, trailing-tail × dump-suppressed, sequential trailing-tail) in `plain_test.go`
- [x] verify prompt handoff — covered by Task 10 `huh_test.go` hook-snapshot tests and `plain_test.go` integration tests asserting LiveLine pause/resume via hooks
- [x] verify full-output visibility in TTY parallel mode — covered by Task 9 TTY-failure-dump, TTY-success-log-enabled (Full log link), and TTY-success-log-disabled (dump runs) tests in `plain_test.go`
- [x] run full test suite: `make test` — passes (all packages green)
- [x] run linter: `make lint` — 0 issues
- [x] verify go.mod is tidy: `make tidy` — clean (no diff)
- [x] verify test coverage on new files is reasonable — `internal/pipeline` 75.7% overall; `lineTee.Write` 96.4% (frame parser ≥95% target met); LiveLine methods average ~85% (≥80% target met)

### Task 12: Update documentation

- [x] update `AGENTS.md` — replace the "previous bubbletea live view was removed" wording with a description of `LiveLine`/`LiveBlock`, the nine Key Invariants from this plan (no `tea.NewProgram` for live view, no `term.MakeRaw`, no capability queries, split-channel writers, single mutex / non-reentrant Stop, `\r` is data, `ui.SetHuhHooks` for prompt handoff, non-TTY parity, cursor-below-footer)
- [x] update `docs/reference/config/deploy.md` "Reporter and logging" section with the new architecture (PTY + LiveBlock + frame-aware parser)
- [x] add a short section to `AGENTS.md` describing how to add a new bubbles component standalone (the `Model.View()` + private ticker recipe) so future contributors don't reach for `tea.NewProgram`
- [x] ensure no docs reference the removed `parallel_view.go` (already cleaned up in previous commit, but double-check) — only historical plan files reference the name; live AGENTS.md and docs/reference are clean

## Technical Details

### Writer pipeline (per child process)

```
Sequential step (TTY, log enabled):
  child ──(PTY)── ptmx ── ansiOnlyStripper ── lineTee(\r-aware) ──> Reporter.StepOutput(addr, frame, final)
                                                                                   │
                                                                                   ├── final=true → r.live.Println(frame)         ─┬─> termOut (cursor ANSI)
                                                                                   │                + r.writeLog(frame)             └─> screen (data line, stdout only)
                                                                                   │                                                   └─> logFile via logSanitizer (CR→NL, ANSI stripped)
                                                                                   └── final=false → r.live.SetText("[N/M] <addr>: " + truncate(frame))
                                                                                                     (footer-text update only; logFile not written; frame is ephemeral)

  Single-copy guarantee: each logical line of child output appears EXACTLY ONCE on screen (via live.Println)
  and EXACTLY ONCE in logFile (via writeLog). childIO does NOT fan out to logFile.

Parallel sub-step (TTY, log enabled):
  child ──(PTY)── ptmx ── ansiOnlyStripper ──┬── logSanitizer{subStepLogFile}       (per-sub-step file gets full output: ANSI stripped, \r→\n so frames are line-separated like "50%\n100%\n")
                                             │
                                             └── lineTee(\r-aware) ──> Reporter.StepOutput(subAddr, frame, final)
                                                                                   │
                                                                                   │   NOTE: while the sub-step is RUNNING, every StepOutput call updates the block row with the SPINNER glyph
                                                                                   │   (running indicator). The ✓/✗/◎ glyph is reserved for FinishStep/FailStep/SkipStep — never used during StepOutput.
                                                                                   │   Showing ✓ on `final=true` would falsely mark a still-running sub-step as successful on every line of output.
                                                                                   │
                                                                                   ├── final=true → r.live.SetBlockRow(idx, "<spinner> [N/M] <subName>: " + truncate(frame))
                                                                                   │                + r.writeLog(frame)             (global pipeline log, SINGLE WRITE per logical line)
                                                                                   │                + commit FINAL frame to per-sub-step buffer (in-memory only, NOT a second log write)
                                                                                   └── final=false → r.live.SetBlockRow(idx, "<spinner> [N/M] <subName>: " + truncate(frame))
                                                                                                     + set inProgress = frame (for trailing-flush preservation only; NOT committed, NOT logged)

  Note: the row content is the same for final=true vs final=false (both show the latest frame next to the spinner).
  The semantic difference is only on the writeLog/buffer-commit side, NOT on the visual glyph. The glyph stays
  the spinner until FinishStep/FailStep/SkipStep transitions the row to ✓/✗/◎ respectively.

  At FinishStep / FailStep / SkipStep (order matters):
    1. r.commitTrailingTail(subAddr):
         - if inProgress != "": append inProgress + "\n" to buffer; writeLog(inProgress); reset inProgress
         - this is the SINGLE commit point for trailing tails (StepOutput(final=false) never commits; dump helper never commits)
    2. apply buffer-dump policy (decide dump vs suppress)
    3. if dump → r.dumpSubStepBufferLocked(subAddr):
         - emit top bar via live.Println + writeLog        (bar line is a normal status line — one screen + one log)
         - for each line in buffer: live.Println(line)     (SCREEN ONLY — line was already writeLog'd at its commit time)
         - emit bottom bar via live.Println + writeLog
    4. if suppress (TTY-success-with-log-path) → emit "  Full log: <path>" via live.Println + writeLog

  Single-copy guarantee:
    - Each `final=true` line: screen ONCE (live block redraw and/or dump replay; both screen-only), global logFile ONCE (writeLog at commit), per-sub-step file ONCE (direct branch).
    - Trailing tail (only present when child exits mid-row): screen ONCE (via dump replay when dump runs, OR not on screen when dump suppressed but tail IS in log + per-sub-step file), global logFile ONCE (commitTrailingTail.writeLog), per-sub-step file ONCE (direct branch captured every byte before the child exited).
  Per-sub-step file is human-readable: `\r` frames are normalized to `\n`, never concatenated.

Non-TTY mode (CI):
  LiveLine.enabled=false → SetText/SetBlockRow/redraw are no-ops; live.Println writes line + "\n" to screen.
  Status lines: still printed to screen (via emit→live.Println) AND logFile (via writeLog → logSanitizer).
  Parallel sub-step buffer dump on FinishStep produces \r-spam-free output thanks to Task 2.
```

### Frame parser semantics

Input bytes from a child (after ANSI stripping via `ansiOnlyRe`, `\r` preserved):

```
"Trying HTTPS\n  0%\r 12%\r 24%\r 50%\r 100%\nDone\n"
```

Emitted callbacks `(frame, final)`:

```
("Trying HTTPS", true)
("  0%",         false)
("  12%",        false)
("  24%",        false)
("  50%",        false)
("  100%",       true)    ← \r followed by \n: emit once with final=true
("Done",         true)
```

Edge cases (pinned by tests):
- Trailing tail without terminator: `Flush()` emits `(current, false)`.
- `\r\n`: emit `(current_before_\r, true)` once.
- Lone `\r` at end (`"foo\r"`): emit `("foo", false)`. Row is in-progress.
- Empty `\r\r\r`: emit `("", false)` three times — tests pin this to make sure callers don't crash on empty frames.

### Cursor state model

The terminal grid is conceptually `[][]rune` indexed by `(row, col)`. LiveLine owns a contiguous set of rows. Let `B = blockRows` (zero in single-line mode); LiveLine owns `B + 1` rows total: B block rows above + 1 footer row.

**Invariant after any public method returns**: the terminal cursor is at column 0 of the row IMMEDIATELY BELOW the footer. The footer is the bottom of the LiveLine-owned region. In single-line mode: cursor at `footer_row + 1`. In block mode: cursor at `(block_top + B) + 1 = block_top + B + 1`. The cursor row itself is empty.

Bytes emitted per operation (`<U>{n}` = `\x1b[<n>A`, `<K>` = `\r\x1b[2K`, `<CR>` = `\r`):

| Op | termOut bytes | screen bytes |
|---|---|---|
| `Start()` | `\x1b[?25l<spinner> <text>\n` (cursor advances from start to row+1) | none |
| `redraw()` single | `<U>{1}<K><spinner> <text>\n` | none |
| `redraw()` block | `<U>{B+1}` then for each i in 0..B-1: `<K><row-content[i]>\n` then `<K><spinner> <text>\n` | none |
| `Println(line)` single | `<U>{1}<K>` (clear footer; cursor at footer row col 0) — then after screen write — `<spinner> <text>\n` | `line\n` |
| `Println(line)` block | `<U>{B+1}` (up to top of owned) + `(<K>\n) × (B+1)` (clear each owned row; cursor ends below footer) + `<U>{B+1}` (back to top) — then after screen write — for each i: `<row-content[i]>\n` + `<spinner> <text>\n` | `line\n` |
| `StartBlock(N)` | `<U>{1}<K>` (erase current footer) + `\n × N` (reserve N rows) + `<spinner> <text>\n` (paint footer at new position) | none |
| `SetBlockRow(idx, content)` | (state update; triggers redraw — see redraw block) | none |
| `EndBlock()` | `<spinner> <text>\n` (paint NEW single-line footer at current cursor row; old block + old footer frozen above) | none |
| `Pause()` **(invariant exception: cursor stays on former-footer row, not below)** | `<U>{1}<K>\x1b[?25h` (erase footer at footer_row; cursor remains on that row at col 0; ready for huh to render in place) | none |
| `Resume()` (restores invariant) | `\x1b[?25l<spinner> <text>\n` (repaint footer at current cursor row; `\n` advances cursor below; invariant #9 restored) | none |
| `Stop()` | (after ticker join, with mutex) `<U>{1}<K>\x1b[?25h` (erase footer; show cursor) | none |

Crucial: `Println` block-mode sequence has a `<U>{B+1}` AFTER clearing because the clear loop ends BELOW the cleared region; we need to go back to the top before writing data.

The `termGrid` test helper consumes these bytes against a virtual `[][]rune` of fixed height with auto-scroll on `\n` at viewport bottom; tests assert grid cell contents row-by-row AND cursor position after each op.

### Concurrency model

```
                ┌────────────────────────────────────────┐
                │            LiveLine.mu                 │
                │                                         │
       ┌────────┼─ ticker goroutine: redraw() (after stopCh check) │
       │        │                                         │
       │        ├─ Reporter.emit: Println(line) + writeLog(line)   │
       │        │                                         │
       │        ├─ Reporter.StepOutput: SetText / SetBlockRow + (optional) Println/writeLog │
       │        │                                         │
       │        └─ Reporter.{Start,Finish}{Step,Group}: state │
       │                                                  │
       └────── all writes to termOut/screen serialised through mu ─────┘

       Outside mu (no deadlock by construction):
       - Stop(): close stopCh → wait <-doneCh → THEN take mu for cleanup
       - Ticker goroutine: select on stopCh and tick.C BEFORE acquiring mu
       - r.writeLog(): independent (does NOT take LiveLine.mu); logFile io.Writer is concurrency-safe at the OS level for byte appends
```

All writes to `termOut` and `screen` go through `LiveLine` methods; both take the same mutex. **`Stop` is NOT re-entrant**: it must be called from outside any LiveLine method (in practice, only from `FinishPipeline` via `defer`, never from inside a `Println` callback). The ticker selects on `stopCh` BEFORE re-entering the mutex region, so the ticker exits promptly when shutdown is requested.

`writeLog` is intentionally OUTSIDE the LiveLine mutex — log writes do not need to serialise with screen redraws and keeping them separate prevents log I/O latency from blocking the live ticker.

### Goroutine lifetime

- `LiveLine.Start()` spawns one ticker goroutine.
- `LiveLine.Stop()` signals via `close(stopCh)`, then `<-doneCh` to join. Ticker selects on `stopCh` BEFORE re-entering the mutex region on each iteration, so it always exits promptly.
- PTY copy goroutines (one per parallel sub-step, plus one per sequential step if PTY is used) close when PTY master gets EOF (child exit). `cleanup()` `<-done` joins them.
- `goleak` in `TestMain` will catch any leak.

### Non-TTY behaviour

```go
enabled := (termOut != io.Discard) // equivalent to term.IsTerminal(os.Stdout.Fd()) at command-layer
l := NewLiveLine(termOut, screen, enabled)
```

When `enabled=false`:
- No ticker goroutine.
- `SetText`/`SetBlockRow`/`StartBlock`/`EndBlock` are no-ops.
- `Println(line)` writes `line + "\n"` straight to `screen`.
- PlainReporter's `writeLog` side-channel still writes to logFile when non-nil (so log file content is identical in TTY and non-TTY modes).
- Parallel sub-step buffer dump on FinishStep still emits between `───── output ─────` bars (with clean lines thanks to Task 2).
- `make test` runs in non-TTY mode → tests stay deterministic; TTY tests inject `termGrid` writers explicitly.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual smoke test** (on a real terminal, against a real project with parallel steps):
- Run `devbox deploy` on a project with at least one parallel group of slow commands (e.g. multiple downloads).
- Observe: sticky footer at bottom with spinner + `[N/M] <step>`.
- Observe: parallel group shows N live rows, each updating in real time with the latest curl/docker output line.
- Hit Ctrl+C mid-parallel-group: pipeline should cancel cleanly (`✗ Cancelled` rows, no orphan child processes, prompt returns).
- Confirm no `^[[?2026...` / `^[[99;5u` garbage on screen.
- Trigger a `confirm` builtin mid-deploy: prompt should appear cleanly (footer hidden), prompt returns, footer reappears.

**Tail-the-log sanity check**:
- `tail -f .devbox/logs/deploy.log` while a deploy runs; the log should contain all child output ANSI-stripped, no `\r` characters (each was converted to `\n` by `logSanitizer`), no live-line cursor sequences.
- `cat .devbox/logs/parallel/deploy/<group>/<sub>.log` should contain that sub-step's full output with ANSI stripped and each `\r` converted to `\n`, so progress redraw frames appear as one frame per line (`50%\n100%\n`) instead of a stripped concatenation (`50%100%`).

**CI sanity check**:
- Run the same deploy in a non-TTY context (`devbox deploy 2>&1 | tee out.log`).
- Output should be the buffer-dump format (per-sub-step output between `───── output ─────` bars), but the dumps are now clean lines (no `\r`-spam).

**Performance check** (optional):
- On a pipeline with many fast steps, ensure the ticker (10 Hz) does not introduce visible lag or excessive CPU usage. Profile with `go tool pprof` if needed.

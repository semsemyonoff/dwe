# Fix: a `\r\n` split across a read boundary writes a blank line

## Overview

`LineTee` collapses CRLF into a single `final=true` frame **only when `\r` and `\n`
land in the same buffer scan**. A write that ends on `\r` produces a pair — a
non-empty non-final frame followed by a final frame that carries nothing — so every
consumer that commits on `final` records a blank line where the content line belongs.

The line is not lost, it is **replaced by an empty one**. In CI, where the log file
is the only output, the swallowed line is exactly the one being read.

Reproduced against the current code (no PTY needed, two `Write` calls suffice).
`PLAIN` is `NewLineTee`, `ANSI` is `NewLineTeePreserveANSI`:

```
PLAIN "foo\r\n"            -> [("foo",true)]                ok
PLAIN "foo\r" + "\n"       -> [("foo",false) ("",true)]     bug
PLAIN "foo\r" + "\x1b[K\n" -> [("foo",false) ("",true)]     bug (ANSI removed by the per-write strip)
PLAIN "foo\r\x1b[" + "K\n" -> [("foo",false) ("",true)]     bug (ANSI sequence split across the boundary)
PLAIN "50%\r" + "60%\r"    -> [("50%",false) ("60%",false)] unaffected
ANSI  "foo\r" + "\n"       -> [("foo",false) ("",true)]     bug
ANSI  "foo\r" + "\x1b[K\n" -> [("foo",false) ("\x1b[K",true)]  bug, and the final frame is NOT byte-empty
ANSI  "foo\r\x1b[" + "K\n" -> [("foo",false) ("\x1b[K",true)]  same
ANSI  "foo\r\x1b[K\n"      -> [("foo",false) ("\x1b[K",true)]  bug even in the INTACT form
```

The last row is not a split at all: under `preserveANSI` the erase sequence survives
into the buffer, so `\r` and `\n` never meet in one scan and the workflow runner's sink
records a blank line for `foo\r\x1b[K\n` written as a single `printf`. On that consumer
the change is a plain fix, not a widening.

The last two rows are why the fix measures emptiness **after an ANSI strip** rather
than in bytes: `NewLineTeePreserveANSI` skips both strips, so the closing frame of a
split `\r\x1b[K\n` reaches the callback as `"\x1b[K"`, and the consumer's own strip
turns it into the blank line. `\r\x1b[K` is the ordinary redraw idiom, so a byte-level
rule would leave the common shape broken on exactly the consumer whose log is the
only output.

Three consumers are hit:

- `internal/core/usercommands/runtime/runners/workflow/parallel.go` (the sub-step
  callback, `NewLineTeePreserveANSI`) — a blank line in the failure dump and in
  `.dwe/logs/parallel/workflow/<workflow-id>/<sub>.log`;
- `internal/core/execution/pipeline/executor.go` (the parallel-branch callback,
  `NewLineTee`) — the same in the per-sub-step log, plus `StepOutput(addr, "", true)`;
- `internal/core/execution/pipeline/plain.go`, `PlainReporter.StepOutput` — that
  final frame reaches `entry.buf.WriteString("")` and `r.writeLog("")`, so the blank
  line also lands in the **global** `.dwe/logs/<pipeline>.log` and in the scrollback
  dump.

Benefit: log lines that survive a read boundary intact, with no change to the live
view and no new state in either consumer callback.

## Context (from discovery)

- `internal/shared/liveui/output.go` — `LineTee.Write` / `LineTee.Flush` (the fix),
  `FrameLogWriter.onFrame` (already carries the same guard one level up), `ANSIOnlyRe`.
- `internal/shared/liveui/output_test.go` — existing `LineTee` frame-parsing tests
  (all single-write, so none of their expectations move) and
  `TestSubStepLog_RoutedViaLineTee_SplitOSCClean`, the template for a
  consumer-shaped test.
- `internal/shared/liveui/logframe_test.go` — `TestFrameLogWriter_SplitWrites` already
  covers `{"a\r", "\nb\n"} → "a\nb\n"`; it stays green after the change but for a
  different reason and must be re-run, not reasoned about.
- `internal/core/execution/pipeline/logframe_wiring_test.go` — end-to-end pins for
  this exact surface (`TestParallelSubStepLog_OnlyCommittedFrames`,
  `TestParallelSubStep_UnterminatedTail_ReachesGlobalLog`); the new consumer-level
  regression test belongs here.
- `internal/shared/liveui/liveline.go` — the package doc carries **nine numbered
  non-negotiable invariants**; read them before touching anything here.
- Consumers are read-only context: `parallel.go`, `executor.go`, `plain.go`. Neither
  callback changes; `executor.go` gains a comment only.

## Development Approach

- **testing approach**: TDD — the unit test *is* the reproduction, so the table goes
  in first and fails before the fix lands.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility

## Testing Strategy

- **unit tests**: `internal/shared/liveui/output_test.go` — a table covering the intact
  and the split form of every shape under both constructors, plus a consumer-shaped
  double of the `parallel.go` callback (the `preserveANSI` one, which has no test
  elsewhere).
- **integration test**: `internal/core/execution/pipeline/logframe_wiring_test.go` —
  one real executor run whose child splits the CRLF across two writes, asserting both
  the per-sub-step log and the global pipeline log.
- **e2e tests**: the project has none for this surface. The live check is manual and
  listed under Post-Completion.
- run with `make test` (never bare `go test ./...` — the embedded-docs tree is
  generated and gitignored). For focused iteration, `go test ./internal/shared/liveui/`
  is fine once `make embedded-docs` has run.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**Fix it in `LineTee`; do not touch the consumers.**

The rule is stated over **frames**, not bytes: remember a frame that was closed by a
lone `\r`, and when the next frame to emit is a **final frame that carries no text**,
emit the remembered one in its place. That is the guard already written in
`FrameLogWriter.onFrame`, hoisted one level down so all three consumers inherit it.
No lookahead, no "consume the `\n`", no byte-position bookkeeping.

"Carries no text" is decided by one helper, `frameIsBlank`, used on both sides of the
rule so there is no `preserveANSI` special case in the transitions:

- a plain `LineTee` frame is already double-stripped, so the helper is a `== ""`
  comparison and no regex runs;
- a `preserveANSI` frame is blank when `ANSIOnlyRe` strips it to nothing.

Why not in the consumers: the callback in `executor.go` carries an explicit
prohibition — *"Do NOT give this callback pending-frame state to 'fix' it: that would
be a new composite flush hook on a path that already has one"* — and it is correct.
A stream ending on a bare `\r` would leave a held frame unreleased, and `flushTee` is
called early on several paths there. The prohibition stands; the state belongs one
level down, where a single `Flush` already owns the lifecycle.

### Rejected alternatives

- **Byte-level trigger** — *"the buffer ended exactly on `\r` **and** the next write
  starts with `\n`, so consume that `\n` and re-emit"*. Same code, same risk, but it
  misses every shape where anything sits between the CR and the LF: a split ANSI
  sequence (`"foo\r\x1b["` + `"K\n"`), and — under `preserveANSI`, the workflow
  runner's constructor — an intact one (`"foo\r"` + `"\x1b[K\n"`). Measured above.
- **Byte-level emptiness** (`frame == ""` instead of `frameIsBlank`) — fixes the plain
  tee but leaves the `preserveANSI` consumer, whose log is the only output in CI,
  broken for the most common redraw idiom.

### Accepted trade

`foo\r\x1b[K\n` means *erase the line, then newline* on a real terminal, i.e. a blank
line; this rule records `foo`. ANSI is stripped before the frame is inspected, so DWE
cannot distinguish a split CRLF from an erase-then-newline and resolves the ambiguity
in favour of the content line. The same holds one frame further out, for
`foo\r` + `\x1b[K\r` + `\n`: an ANSI-only non-final frame does not evict the held
frame, so that also records `foo`.

This is not a new trade, it is the one already shipping, and the change makes two paths
agree with it rather than introducing it:

- **the intact form already behaves this way.** Measured on current code, a plain tee
  fed `"foo\r\x1b[K\n"` in one write emits `[("foo",true)]` — the per-write strip
  removes the erase sequence and the CRLF collapses. Deciding blankness before the
  strip would make the split form disagree with the intact form, which is the whole
  bug being fixed.
- **`FrameLogWriter` already resolves it that way.** Its `onFrame` keeps pending across
  a blank non-final frame and substitutes it into a blank final frame, so the
  sequential path records `foo` for `foo\r\x1b[K\r\n` today. Treating an ANSI-only
  frame as evicting would split the parallel path away from the sequential one.

It remains the same class of deliberate approximation `FrameLogWriter` documents for
`abc\rX\n` → `X`: frame collapsing, not terminal emulation. The cost is a tool that
clears its progress line immediately before exiting — its last frame is recorded
instead of the blank line it left on screen.

## Technical Details

### State

```go
type LineTee struct {
	cb           func(frame string, final bool)
	preserveANSI bool
	mu           sync.Mutex
	buf          bytes.Buffer
	pending      string // frame closed by a lone `\r`, awaiting its `\n`
	hasPending   bool
}
```

### `frameIsBlank`

A method on `LineTee` (it needs `preserveANSI`): returns true when the frame carries
no text. For a plain tee that is `frame == ""` — the frame has already been stripped
twice, so no regex runs on the hot path. For a `preserveANSI` tee it is
`ANSIOnlyRe.ReplaceAllString(frame, "") == ""`, evaluated only when `frame != ""`.

### Transitions in `Write`

Computed under `t.mu`, **before** the existing `Unlock` around `cb`:

- `final == false`: if the frame is **not** blank → `pending, hasPending = frame, true`.
  A blank non-final frame (a bare `\r` with nothing but escape bytes before it) leaves
  pending alone — the cursor returned to column 0 but the screen still shows the
  previous frame. That is `FrameLogWriter.onFrame`'s existing rule, inherited
  deliberately: making an ANSI-only frame evict instead would split this path away from
  the sequential one, which already resolves `foo\r\x1b[K\r\n` to `foo`. The frame is
  emitted immediately, exactly as today, so the live view loses nothing and invariant
  #6 (*a carriage return is data, not a line terminator*) is untouched.
- `final == true`: if the frame is blank **and** `hasPending` → emit `pending` instead;
  then `pending, hasPending = "", false` either way. Under `preserveANSI` the
  substituted frame carries its own escape bytes and the blank frame's are dropped —
  a trailing erase-line sequence loses nothing visually.
- The in-scan CRLF collapse path is not touched: it produces a non-blank final frame
  and simply clears pending.

Terminal-equivalence check for `"foo\r" + "\r" + "\n"`: pending survives the bare `\r`,
the `\n` commits `foo` — which is what a real terminal leaves on the line.

### `Flush`

`Flush` **never sets pending**: it clears `pending`/`hasPending` first — on the
empty-buffer early return as well — and then delivers the tail as `(tail, false)` the
way it does today.

Reason the clear is unconditional: `executor.go` calls `flushTee` at three sites (the
defer, post-body, post-check), and `PlainReporter.FlushOutput` runs between the second
and third. With a conditional clear, a body ending on `foo\r` would leave pending
alive, `FlushOutput` would commit `foo`, and a check whose first byte is `\n` would
re-emit `("foo",true)` — a **duplicated** line in `entry.buf` and in the global log.
A duplicate in place of a blank is not a trade worth making.

### Consequences

- Consumers see the frame **twice**: `("foo",false)` then `("foo",true)`. Verified
  against both callbacks — `parallel.go` repaints `SetBlockRowRunning` with identical
  text and then returns at its `if !final && !atEOF` guard, `executor.go` → `StepOutput`
  stores it in `entry.inProgress` and repaints the block row, then commits once on the
  final frame. Repaint in both, duplicate lines in neither.
- Locking: the new fields are read and written only inside `Write` / `Flush` while
  `t.mu` is held, before it is released around `cb`. No new locks; the documented
  "one source per `LineTee`" contract is unchanged.
- The substitution in `FrameLogWriter.onFrame` becomes unreachable and is deleted.
  The reason is narrower than "the two state machines are identical" and must be
  written down as such: between flushes the transitions do coincide (set on a
  non-blank non-final frame, untouched by a blank one, cleared on any final frame), so
  `line == "" && f.hasPending` can no longer be reached from `Write`. The one
  divergence is inside `FrameLogWriter.Flush`, where `tee.Flush()` emits `(tail,false)`
  and sets `f.pending` while `t.pending` has just been cleared — a window opened and
  closed under `f.mu` with no frame able to arrive. Its `!final` half — collapsing a
  redraw run `50%\r100%\n` down to `100%` — is a different job and stays.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): the `LineTee` change, its tests, the
  consumer-level regression test, the dead-guard removal, and every doc/CHANGELOG edit.
- **Post-Completion** (no checkboxes): the manual no-regression run against a real
  parallel workflow.

## Implementation Steps

### Task 1: Pin both CRLF forms with a failing table test

**Files:**
- Modify: `internal/shared/liveui/output_test.go`

- [x] read the nine invariants in the package doc at the top of
      `internal/shared/liveui/liveline.go` before writing anything
- [x] add `TestLineTee_SplitCRLF_ReemitsHeldFrame`, table-driven over a slice of
      writes per case, reusing the existing `collectFrames` / `equalFrames` helpers.
      Rows marked **fails now** are the reproduction; the rest are controls that pass
      today and must keep passing:

      | constructor | writes | want | |
      | --- | --- | --- | --- |
      | plain | `"foo\r\n"` | `[(foo,true)]` | control |
      | plain | `"foo\r"`, `"\n"` | `[(foo,false) (foo,true)]` | **fails now** |
      | plain | `"foo\r"`, `"\x1b[K\n"` | `[(foo,false) (foo,true)]` | **fails now** |
      | plain | `"foo\r\x1b["`, `"K\n"` | `[(foo,false) (foo,true)]` | **fails now** |
      | plain | `"50%\r"`, `"60%\r"` | `[(50%,false) (60%,false)]` | control — progress bar |
      | plain | `"foo\r"`, `"\r"`, `"\n"` | `[(foo,false) ("",false) (foo,true)]` | **fails now** — pending survives a bare CR |
      | plain | `"foo\r"`, `"\n\n"` | `[(foo,false) (foo,true) ("",true)]` | **fails now** — a genuine blank line after |
      | plain | `"10"`, `"%\r100%"`, `"\n"` | `[(10%,false) (100%,true)]` | control — buffer not empty at the CR |
      | preserveANSI | `"foo\r"`, `"\n"` | `[(foo,false) (foo,true)]` | **fails now** |
      | preserveANSI | `"foo\r"`, `"\x1b[K\n"` | `[(foo,false) (foo,true)]` | **fails now** — final frame is `"\x1b[K"` today |
      | preserveANSI | `"foo\r\x1b["`, `"K\n"` | `[(foo,false) (foo,true)]` | **fails now** — same |
      | preserveANSI | `"50%\r"`, `"60%\r"` | `[(50%,false) (60%,false)]` | control |
      | plain | `"foo\r"`, `"\x1b[K\r"`, `"\n"` | `[(foo,false) ("",false) (foo,true)]` | **fails now** — the accepted trade, pinned deliberately |
      | preserveANSI | `"foo\r"`, `"\x1b[K\r"`, `"\n"` | `[(foo,false) ("\x1b[K",false) (foo,true)]` | **fails now** — same, and the held frame survives an ANSI-only frame |

- [x] add `TestLineTee_Flush_ClearsHeldFrame` with both `Flush` paths — a control today,
      a pin on the unconditional clear afterwards:
      - `"foo\r"`, `Flush()`, `"\n"` → `[(foo,false) ("",true)]` — the **empty-buffer
        early return** (after `"foo\r"` the tee buffer is drained)
      - `"foo\r"`, `"bar"`, `Flush()`, `"\n"` → `[(foo,false) (bar,false) ("",true)]` —
        the tail path, pinning that `Flush`'s own `cb(tail,false)` does not re-arm
        pending
- [x] run `go test ./internal/shared/liveui/` and record the observed frames for the
      **fails now** rows — that output is the reproduction
- [x] confirm no pre-existing test in the package fails at this point

**Observed reproduction** (`go test ./internal/shared/liveui/`, before Task 2 —
exactly the 10 **fails now** rows fail, every control row and both
`TestLineTee_Flush_ClearsHeldFrame` sub-tests pass, and no pre-existing test in the
package fails):

| constructor | writes | got | want |
| --- | --- | --- | --- |
| plain | `"foo\r"`, `"\n"` | `[(foo,false) ("",true)]` | `[(foo,false) (foo,true)]` |
| plain | `"foo\r"`, `"\x1b[K\n"` | `[(foo,false) ("",true)]` | `[(foo,false) (foo,true)]` |
| plain | `"foo\r\x1b["`, `"K\n"` | `[(foo,false) ("",true)]` | `[(foo,false) (foo,true)]` |
| plain | `"foo\r"`, `"\r"`, `"\n"` | `[(foo,false) ("",false) ("",true)]` | `[(foo,false) ("",false) (foo,true)]` |
| plain | `"foo\r"`, `"\n\n"` | `[(foo,false) ("",true) ("",true)]` | `[(foo,false) (foo,true) ("",true)]` |
| plain | `"foo\r"`, `"\x1b[K\r"`, `"\n"` | `[(foo,false) ("",false) ("",true)]` | `[(foo,false) ("",false) (foo,true)]` |
| preserveANSI | `"foo\r"`, `"\n"` | `[(foo,false) ("",true)]` | `[(foo,false) (foo,true)]` |
| preserveANSI | `"foo\r"`, `"\x1b[K\n"` | `[(foo,false) ("\x1b[K",true)]` | `[(foo,false) (foo,true)]` |
| preserveANSI | `"foo\r\x1b["`, `"K\n"` | `[(foo,false) ("\x1b[K",true)]` | `[(foo,false) (foo,true)]` |
| preserveANSI | `"foo\r"`, `"\x1b[K\r"`, `"\n"` | `[(foo,false) ("\x1b[K",false) ("",true)]` | `[(foo,false) ("\x1b[K",false) (foo,true)]` |

### Task 2: Hold a `\r`-closed frame in LineTee and re-emit it on a blank final frame

**Files:**
- Modify: `internal/shared/liveui/output.go`

- [x] add `pending string` + `hasPending bool` to `LineTee`
- [x] add the unexported `frameIsBlank(frame string) bool` method: `frame == ""` for a
      plain tee (no regex), `ANSIOnlyRe` strips to empty for a `preserveANSI` tee,
      evaluated only when `frame != ""`
- [x] in `Write`, between computing `frame`/`final` and the `t.mu.Unlock()` that
      precedes `t.cb(...)`, apply the transitions from Technical Details: a non-blank
      non-final frame sets pending; a blank non-final frame leaves it alone; a final
      frame substitutes pending when the frame is blank, and clears pending always
      (the transitions live in the new `holdOrSubstitute` method, called with `t.mu`
      held, so `Write`'s scan loop keeps its shape)
- [x] emit the substituted value, not the raw `frame`
- [x] in `Flush`, clear `pending`/`hasPending` before anything else, including on the
      empty-buffer early return
- [x] run `go test ./internal/shared/liveui/` — every Task 1 row passes and every
      pre-existing `LineTee` / `FrameLogWriter` test stays green, in particular
      `TestLineTee_FrameParsing_Mixed`, `TestLineTee_FrameParsing_MultipleConsecutiveCRs`,
      `TestLineTee_FrameParsing_TrailingTail` and `TestFrameLogWriter_SplitWrites`
- [x] run `go test ./internal/core/usercommands/runtime/runners/workflow/` — the
      `preserveANSI` consumer's own live-line tests live there
- [x] run `go test -race ./internal/shared/liveui/`

### Task 3: Prove the fix at the workflow-runner callback shape

**Files:**
- Modify: `internal/shared/liveui/output_test.go`

- [x] add `TestSubStepLog_RoutedViaLineTee_SplitCRLF`, modelled on the existing
      `TestSubStepLog_RoutedViaLineTee_SplitOSCClean`, using
      `NewLineTeePreserveANSI` plus the `parallel.go` callback shape (strip the frame
      with `ANSIOnlyRe`, write it to the sink only when `final`) — that callback is
      covered by `internal/core/usercommands/runtime/runners/workflow/liveui_test.go`
      for redraws, tails and ANSI-only tails, but by nothing for a split CRLF, and it
      is the consumer the `frameIsBlank` widening exists for
- [x] feed `"foo\r"` then `"\x1b[K\n"` and assert the sink holds `"foo\n"`, no blank
      line
- [x] add the intact form `"foo\r\x1b[K\n"` to the same test so both paths produce
      byte-identical sink content
- [x] run `go test ./internal/shared/liveui/`

**Non-vacuity check** (not required by this task, but cheap here): re-run against the
pre-fix `output.go` (`git show 2675f4d9:internal/shared/liveui/output.go`) — **both**
sub-tests fail with `sub-step log = "\n"`, the intact form included, confirming the
`preserveANSI` consumer was broken for the plain `\r\x1b[K\n` redraw idiom even
without a split.

### Task 4: Pin the fix end-to-end through the executor

**Files:**
- Modify: `internal/core/execution/pipeline/logframe_wiring_test.go`

- [x] add `TestParallelSubStepLog_SplitCRLF_NoBlankLine` next to
      `TestParallelSubStepLog_OnlyCommittedFrames`, reusing its harness
      (`buildParallelGroupStep` + `RunWithOptions`)
- [x] make the child split the CRLF across two writes — `printf 'frame-99\r'; sleep 0.1;
      printf '\n'` — since a single `printf 'a\r\n'` collapses in one buffer scan and
      cannot reproduce the bug
- [x] **do NOT assert through `logLines`** — that helper drops empty and
      whitespace-only lines (`logframe_wiring_test.go`, `strings.TrimSpace(l) != ""`),
      so it cannot distinguish a missing line from a blanked one, and it would mask a
      `"frame-99\n\n"`-shaped regression outright. Compare **raw file content**: the
      per-sub-step log `.dwe/logs/parallel/deploy/g/alpha.log` must equal
      `"frame-99\n"` byte for byte. (Pre-fix that file is exactly `"\n"` — the
      non-final frame only reaches `entry.inProgress`, which the blank final frame then
      clears — so the raw comparison is what states the contract, not a rescue of an
      assertion `logLines` would have passed.)
- [x] note in the test that the byte-equality holds only while trace output is silent:
      `executor.go` routes `trace.WithLinePrinter` into the same `stepWriter`, so a
      `-v` / `--debug` environment would add command echoes to that file
- [x] for the global pipeline log, build a **real** `PlainReporter` — the
      `mockReporter` used by `TestParallelSubStepLog_OnlyCommittedFrames` never writes
      there, because only `PlainReporter.writeLog` does. Copy the construction from the
      sibling `TestParallelSubStep_UnterminatedTail_ReachesGlobalLog`:
      `NewPlainReporter(render.NewWriter(scr), globalLog, io.Discard)`
- [x] assert that log by **containment**, not equality — it also carries timestamped
      phase and step lines. Pre-fix the signature is a bare empty line plus a missing
      `frame-99`; assert `strings.Contains(global, "\nframe-99\n")` — that one does fail
      pre-fix — plus the absence of any `"\n\n"`. Do **not** anchor the negative on
      `"p/alpha\n\n"`: the blank line comes from `PlainReporter.writeLog("")` and lands
      right after the parallel-group header, while the only `p/alpha` line is the later
      `Done:` emit, so such a string could never match. Still not `logLines`
- [x] **verify the test is not vacuous**: run it once with the Task 2 change reverted
      (`git stash` the `output.go` hunk) and confirm it FAILS on the blank line. A
      100 ms gap normally forces two reads, but nothing guarantees the copy goroutine
      is scheduled between the two `printf`s; if the writes coalesce the test passes
      without exercising the split at all. It never fails spuriously — the risk is
      silent vacuity, and the deterministic coverage lives in Tasks 1–3
- [x] note that conclusion in the test's doc comment so a future reader knows the
      timing is load-bearing
- [x] run `go test ./internal/core/execution/pipeline/` and
      `go test -race -count=5 -run TestParallelSubStepLog ./internal/core/execution/pipeline/`

**Non-vacuity result** (Task 2's `output.go` swapped for `git show
2675f4d9:internal/shared/liveui/output.go`): all three assertions fail —
`.dwe/logs/parallel/deploy/g/alpha.log` is exactly `"\n"`, the global log has no
`frame-99` at all, and its blank line lands directly after the
`· [1/1] p/alpha` group header (confirming the plan's note that anchoring the
negative on `p/alpha\n\n` could never have matched).

### Task 5: Remove the now-unreachable guard in FrameLogWriter

**Files:**
- Modify: `internal/shared/liveui/output.go`

- [x] delete the `if line == "" && f.hasPending { line = f.pending }` substitution from
      `FrameLogWriter.onFrame` and the comment block explaining it
- [x] replace it with a short comment carrying the actual reason from Consequences:
      `LineTee` resolves a split CRLF before it reaches this callback, so a blank final
      frame here is a genuine blank line — and the one place the two pending slots
      diverge (`FrameLogWriter.Flush`, where `tee.Flush()` sets `f.pending` after
      `t.pending` was cleared) is a window under `f.mu` no frame can enter
- [x] name the precondition in that comment: `NewFrameLogWriter` builds its tee with
      `NewLineTee`, the **plain** constructor, where `frame != ""` and
      `!frameIsBlank(frame)` are the same predicate. A `preserveANSI` `FrameLogWriter`
      would diverge — `f` would set pending on `"\x1b[K"` where `t` would not — and
      regress silently
- [x] leave the `!final` pending logic untouched — that is redraw collapsing, a
      different job
- [x] run `go test ./internal/shared/liveui/` — `TestFrameLogWriter_SplitWrites` in
      `logframe_test.go` must still pass, notably `{"a\r", "\nb\n"} → "a\nb\n"`; if it
      does not, the transitions do not coincide and the deletion is wrong
- [x] run `go test -race ./internal/shared/liveui/`

### Task 6: Update the in-code contracts

**Files:**
- Modify: `internal/shared/liveui/output.go`
- Modify: `internal/core/execution/pipeline/executor.go`

- [x] update the `LineTee` doc comment: it currently claims CRLF collapses "within one
      buffer scan" — state that a CRLF split across writes is handled too, but by
      re-emitting the held frame rather than collapsing, that the frame is therefore
      delivered to the callback **twice** (non-final, then final), and that blankness is
      measured after an ANSI strip so the `preserveANSI` constructor behaves the same
- [x] record the accepted trade from Solution Overview on that comment: `foo\r\x1b[K\n`
      is an erase-then-newline on a real terminal and this records `foo` — the same
      class of deliberate approximation `FrameLogWriter` already documents
- [x] document on `Flush` that it clears the held frame unconditionally, with the
      reason (an early flush followed by more input must not re-emit a tail the
      consumer has already committed)
- [x] **add** a split-CRLF note to the `FrameLogWriter` doc comment rather than editing
      one — that comment covers only final / pending / `Flush`, and the type's sole
      split-CRLF prose is the inline comment Task 5 already deletes
- [x] the `onFrame` and `Flush` doc comments cite `output.go:196-199`, `:224-226` and
      `:225`; those line numbers are already stale and this change moves them further —
      drop them rather than re-deriving them
- [x] in `executor.go`, extend the "Do NOT give this callback pending-frame state"
      comment: the prohibition **stands**, and the split-CRLF case it used to describe
      is now resolved in `LineTee` — without this, the next reader concludes the bug is
      still live and re-opens it
- [x] no behaviour changes in this task; run
      `go test ./internal/shared/liveui/ ./internal/core/execution/pipeline/`

### Task 7: Update the repository docs and CHANGELOG

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `CHANGELOG.md`

- [x] in `docs/internals/packages.md`, § `internal/shared/liveui/`: add the
      `LineTee`-level hold-and-re-emit (including the after-strip blankness rule and
      the double delivery) **before** the sentence "Do not 'fix' either callback by
      giving it pending-frame state — that would be a new composite flush hook", and
      say why that prohibition survives it — the state lives where a single `Flush`
      owns the lifecycle
- [x] the same paragraph's mechanic (1) says "`LineTee.Flush` delivers its
      un-terminated tail as `(tail, false)`, i.e. *into* the pending slot, so
      `FrameLogWriter.Flush` calls `tee.Flush()` FIRST…" — there are now **two**
      pending slots; name which belongs to which type and state that `LineTee.Flush`
      clears its own unconditionally
- [x] do **not** look for a description of the deleted `FrameLogWriter` substitution
      there — the paragraph documents the *redraw-eviction* rule, which Task 5 keeps,
      and says nothing about the `line == "" && f.hasPending` branch. Nothing to remove
- [x] add an entry under `## [Unreleased]` → `### Fixed` in `CHANGELOG.md`: a line
      whose `\r\n` was split across a read boundary was recorded as an empty line in
      pipeline and workflow logs and in failure dumps
- [x] run `make build` — `docs/internals/` is synced into the **gitignored**
      `internal/core/docs/embedded/` tree and `internal/core/docs/content_hashes_gen.go`
      is regenerated; only the latter is tracked and it must be committed, or the tree
      is dirty on every subsequent `make build`
- [x] run `git status` and confirm `content_hashes_gen.go` is the only generated file
      to stage
- [x] no upgrade-guide entry — nothing breaks; no `docs/reference/` change — this is
      internal log mechanics with no user-facing schema

### Task 8: Verify acceptance criteria

- [x] every shape in the Overview reproduction produces identical committed output in
      its intact and its split form, **under both constructors**
- [x] the progress-bar path is unchanged (`"50%\r"` + `"60%\r"`, both constructors)
- [x] the nine `liveui` invariants are re-read and none is violated — in particular #6
      (`\r` is data): a CR still emits its frame immediately as non-final
- [x] neither consumer callback gained state: `git diff` touches no file under
      `internal/core/usercommands/runtime/runners/workflow/`, and in
      `internal/core/execution/pipeline/` only a comment and the new test
- [x] run the full suite: `make test`
- [x] run `make test-race`
- [x] run `make lint`

**Verification result** (throwaway test in `internal/shared/liveui/`, deleted after the
run — the permanent coverage is Tasks 1/3/4):

- intact vs split committed output, every Overview shape, both constructors —
  `foo\r\n`, `foo\r` + `\x1b[K\n`, `foo\r\x1b[` + `K\n` and `foo\r` + `\x1b[K\r` + `\n`
  all commit exactly `"foo\n"` in both forms; the progress rows commit nothing in both.
  No pair disagrees.
- progress path unchanged: `"50%\r"` then `"60%\r"` yields
  `[(50%,false) (60%,false)]` under `NewLineTee` and `NewLineTeePreserveANSI` alike —
  one non-final frame per redraw, invariant #6 intact. The other eight invariants are
  untouched by the change: no `tea.NewProgram`, no `term.MakeRaw`, no capability query,
  no change to the termOut/screen/diag split, still one mutex (the new fields live under
  the existing `t.mu`), no prompt-handoff or footer-teardown change, no non-TTY
  divergence, no cursor-position change.
- consumer callbacks: `git diff 7a6c1c2b..HEAD --stat` touches no file under
  `internal/core/usercommands/runtime/runners/workflow/`, and under
  `internal/core/execution/pipeline/` only `executor.go` (a comment-only hunk) and
  `logframe_wiring_test.go` (the new test).
- `make test` green, `make test-race` green (plus `go test -race
  ./internal/shared/liveui/`, which the race target does not cover), `make lint` — 0 issues.

### Task 9: [Final] Update documentation

- [x] re-read the `CHANGELOG.md` entry against what actually shipped — the wording
      matches the shipped behaviour (blank line in the pipeline log, the per-sub-step
      parallel logs and the failure dump; the `\r\x1b[K\n` idiom fixed on the workflow
      runner even in its intact form; live view unchanged). One correction: the entry
      named `.dwe/logs/parallel/<workflow>/<sub>.log`, which is neither real path —
      the workflow runner writes `.dwe/logs/parallel/workflow/<workflow-id>/<sub>.log`
      and the executor `.dwe/logs/parallel/<pipeline>/<group>/<sub>.log` — so it now
      says "the per-sub-step logs under `.dwe/logs/parallel/`", which covers both
- [x] no `AGENTS.md` change — this adds no new cross-cutting trap, and the `liveui`
      bullet there already points at `packages.md`
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification:**

- Forcing a read boundary to land between `\r` and `\n` is not something the host can
  arrange on demand outside a test, so the unit table stays the reproduction. The live
  run is a **no-regression** check only: run a parallel workflow whose sub-steps emit
  `\r` redraws (a `docker pull` is the easy source), then confirm
  - the live block rows still animate per redraw frame, and
  - `.dwe/logs/parallel/workflow/<workflow-id>/<sub>.log` (and the executor's
    `.dwe/logs/parallel/<pipeline>/<group>/<sub>.log`) plus the global
    `.dwe/logs/<pipeline>.log` hold one line per committed line, with no blank lines
    and no duplicates.
- Frequency in real-world output has never been measured. Most output in the corpus is
  `\n` without `\r`, so a hit needs either a CRLF-emitting tool or a progress bar whose
  read boundary falls on the CR.

**External system updates:**

- None. No config schema, flag, or command surface changes.

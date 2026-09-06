# Pipeline eject + log frame dedup

## Overview

Two independent improvements that ship on one branch, in this order:

**Part A — collapse redraw frames in pipeline log files.** Every `\r` a child
process writes to redraw a progress line currently becomes its own line in
`.dwe/logs/<pipeline>.log`. Measured on a live workspace: `deploy.log` is 1001
lines, of which ~601 are redrawn CR frames from git's clone progress and 216 are
repeated `[+] up 2/3` frames from compose. One clone step occupies more than half
the file, which makes the log useless for the thing it exists for — reading what
happened after a failed deploy. Part A removes the CR frames (~601 of ~817 noise
lines). The compose repeats are deliberately **out of scope** (see Non-goals).

**Part B — `dwe deploy eject` and `dwe reset eject`.** Today the only way to see
the built-in deploy or reset pipeline is to re-type it by hand from the source.
That is why three local projects carry a `workspace/deploy.yml` that is 100 %
commented-out scaffold with zero effect. It also leaves a gap this release just
opened: `dwe validate` now *reports* such a file (`has no active content (all
comments or empty) — built-in default pipeline is active`) without offering an
action in response. `eject` is that action — it prints, or writes, the built-in
pipeline as an authorable, commented `deploy.yml` / `reset.yml`.

Both parts are user-observable and therefore both need a `CHANGELOG.md` entry.
Neither is a breaking change.

### Non-goals

Stated explicitly so they are not re-litigated during implementation:

- **Part A does not emulate a terminal.** `abc\rX\n` renders as `Xbc` on a real
  terminal (the shorter overwrite leaves the tail of the previous frame visible);
  the log will record `X`. Faithful reproduction needs a terminal emulator with a
  scrollback window. The same reason defers the compose `[+] up 2/3` repeats:
  those are believed to be CUU (cursor-up) sequences rather than CR frames, and
  nobody has captured the raw PTY stream to confirm it. Do not guess at CUU
  handling in this plan's scope.
- **`eject` never emits the *effective* pipeline of the project** — only the
  built-in default, which is a constant. It has no `--service` flag and does not
  inline per-service pipelines.
- **`eject` does not cover lifecycle.** The effective `stop` pipeline always
  carries the engine-synthetic `_auto_reap_daemons` phase, and
  `validatePhaseSteps` rejects a user-authored phase whose name starts with `_`.
  An emitted `lifecycle.yml` would therefore not load back — the emit would be a
  file dwe itself refuses.
- **No new marshalling code in `internal/core/project/config`.** See the design
  decision in Solution Overview.

## Context (from discovery)

Files and facts established before writing this plan:

- `internal/shared/liveui/output.go` — `LogSanitizer` (`:72-85`) is a stateless
  writer that strips ANSI and rewrites `\r\n` → `\n` and lone `\r` → `\n`. The
  lone-`\r` rewrite is the direct cause of Part A's noise, and it is deliberate
  in today's design (`:58-71` documents it as "record each redraw frame on its
  own line"). `LineTee` (`:112-226`) already parses the same byte stream into
  frames with a `final bool` flag and has an idempotent `Flush()`.
- `internal/core/execution/pipeline/executor.go` — two log paths:
  - sequential (`:864-876`): `stepWriter = io.MultiWriter(os.Stdout,
    &liveui.LogSanitizer{W: opts.LogWriter})`;
  - parallel (`:835-863`): a `LineTee` whose callback writes **every** frame,
    `final=false` included, to the per-sub-step log via `fmt.Fprintln(subLog,
    frame)` (`:845-847`).
  - the `flushTee` mechanism (`:830-834`, `:850`, defer at `:877-879`, eager
    calls at `:931-932` and `:975-976`) already guarantees "flush before any
    end-of-step reporter event"; the reasoning is written out at `:857-863`.
  - `:840-844` already records that a stateless per-write sanitiser cannot
    reassemble ANSI sequences split across a PTY read boundary, while the
    `LineTee` double-strip can.
- `internal/core/execution/pipeline/plain.go:148-176` — `NewPlainReporter` wraps
  its own log handle in `LogSanitizer` for **reporter status lines**. Those are
  always whole lines; this path is deliberately untouched.
- `internal/core/usercommands/runtime/runners/workflow/parallel.go:216-234` —
  the in-repo precedent for Part A's parallel change: a `LineTee` callback that
  already returns early on `!final` before writing to the sub-step file. It gates
  the buffer too, and its `tee.Flush()` at `:234` delivers the tail as
  `final=false`, so that path loses a `\r`-terminated tail entirely.
- `internal/core/execution/pipeline/plain.go:659-668`, `:700-718`, `:730-735` —
  the second sink the pipeline has and the workflow runner has not: a non-final
  frame is parked in `entry.inProgress` and `commitTrailingTail` writes it to the
  global pipeline log at step finish. This is why gating the per-sub-step file on
  `final` does not lose the tail here.
- `internal/core/execution/pipeline/executor.go:110-146`, `:233`, `:286`,
  `:321` — `stepWriter` is also handed to builtins as `ActionContext.StepWriter`,
  so a builtin may write to it from a goroutine. Combined with `LogSanitizer`'s
  advertised "no mutex, safe for concurrent writes" contract
  (`liveui/output.go:69-71`), the replacement writer needs its own lock — and
  `LineTee` releases `t.mu` around every `cb` call (`output.go:196-199`,
  `:224-226`), so the lock cannot be borrowed from it.
- `internal/core/validate/config/workspace.go:1225-1245` — the validator treats
  inert as **two** conditions, not one: `PipelineStateDefaultFallback` *and*
  `len(cfg.Phases) == 0`, with the comment "a file carrying only `log: false` is
  every bit as inert as an all-comment one". Part B's refusal message has to
  match both or it contradicts the diagnostic it is meant to answer.
- `internal/core/project/config/workspace.go` has **no**
  `LoadProjectDeployConfigWithState`. The state-carrying loaders are
  `LoadResetConfigWithState` (`:3310`), `LoadLifecycleConfigWithState`
  (`:3123`), and `ParseDeployConfigForValidationWithState` (`:3270`) — the last
  returning the lenient `*DeployConfig` shape that permits `after:`, which is
  the validator's shape and not the one `deploy.yml` loads through.
- `loadProjectDeployConfigDecode` calls `validatePhaseSteps(cfg.Phases, true)`
  at `workspace.go:3197` for **both** deploy and reset, so `deploy_services` is
  in fact accepted in `reset.yml` at load time despite the doc comment at
  `:3299` claiming otherwise. The only real difference between
  `LoadProjectDeployConfig` and `LoadResetConfig` is `defaultLog`.
- `internal/cli/secrets/files.go:338-352` — `writeOutputFile(path, data, mode,
  force)` **already implements Part B's write policy**: refuse with
  `cmdctx.Err("secrets_output_exists", "<path> already exists")` plus
  `WithHint("pass --force to overwrite it, or choose another --out PATH")`,
  a non-regular-file guard, and an explicit `Chmod`. Its flags come from
  `addFileFlags` (`:118-121`), which also carries the `--out`-not-`-o`
  rationale, and its `--out -` form means stdout. `internal/cli/docs/export.go:47`
  is a third `--force` overwrite site.
- `internal/cli/cmdctx/` has nine files: `completion.go`, `defaultnotice.go`,
  `flags.go`, `locks.go`, `noninteractive.go`, `notify.go`, `output.go`,
  `pipelinelog.go`, `services_completion.go`. `pipelinelog.go` (`WarnSilentLog`)
  is the helper adjacent to Part A.
- `internal/cli/deploy/menu.go:54-62`, `:195-309`, `:496` — bare `dwe deploy`
  opens an interactive menu (`run` / `run --service` / `plan` / `plan --service`
  / wizard / exit), not a help dump. Whether `eject` joins it is a decision this
  plan has to record either way.
- `internal/shared/liveui/output_test.go:195-218` and `:231-242` — two existing
  tests whose **comments** describe the pre-Part-A world:
  `TestSubStepLog_RoutedViaLineTee_SplitOSCClean` simulates "write each assembled
  frame (both final and non-final) as a line to the sub-step log", and
  `TestLogSanitizer_ProgressFrames_BecomeSeparateLines` is captioned "the
  regression test for the live-progress bug". Both keep passing (they pin
  `LogSanitizer`, which survives for the reporter path) but their captions stop
  being true of the pipeline log.
- `internal/core/workflow/deploy/defaults.go:8-55` — `DefaultDeployConfig()`: 3
  phases, 3 steps, no parallel group, no `Action`, no `condition.Condition`,
  `Log: &true`, every step sets both `Type` and `Cmd`.
- `internal/core/workflow/reset/defaults.go:6-66` — `DefaultResetConfig()`: 3
  phases, 4 steps, same shape, but **`Log` is left nil**.
- `internal/core/project/config/workspace.go`:
  - `:3281-3284` `LoadProjectDeployConfig` → `loadProjectDeployConfigDecode(path,
    defaultLog=true)`;
  - `:3302-3305` `LoadResetConfig` → same decoder with `defaultLog=false`, so a
    reset file with no `log:` key loads as `Log: &false` — which is **not**
    `DeepEqual` to `DefaultResetConfig()`'s nil;
  - `:3094-3109` `PipelineFileState` — `PipelineStateAuthored` vs
    `PipelineStateDefaultFallback` (empty / all-comment file);
  - `:493-508` `deployStepKnownFields` — the hand-mirrored allow-list a custom
    `UnmarshalYAML` needs, and one of the two lists a marshaller would have to
    stay in sync with.
- `internal/cli/deploy/deploy.go:54-56` and
  `internal/cli/lifecycle/reset.go:56-58` (inside `NewResetCmd`, `:48-60`) — the
  two `AddCommand` blocks the new subcommands attach to. They live in different
  packages.
- `internal/cli/deploy/deploy.go:235-268` — `newDeployPlanCmd`, whose
  `--format table|shell` slot is the reason `eject` is not a third format
  (see Solution Overview).
- `internal/cli/docs/llmstxt.go:54` and `:120` — the `--out PATH` precedent:
  flag name (never `--output`, which would shadow the root flag's `-o`
  shorthand) and an unconditional `os.WriteFile` overwrite.
- `internal/cli/bridgepolicy.go:29-55` — `bridgeAllowedTopLevel` contains
  `commands, status, info, logs, docs, prompt, version, help, vars,
  __complete*`. **`deploy` and `reset` are absent**, so both new subcommands are
  blocked from a bridged container automatically. No bridge work is required;
  this is recorded so the reviewer does not have to re-derive it.
- `internal/core/docs/lang_test.go:396-460` — `TestRussianTranslationsAreFresh`
  walks the embedded `i18n/ru/**` and fails when a translation's
  `> Translated from: <relPath> @ <hash>` header no longer matches
  `ContentHashes`, which `scripts/gen-docs-content-hashes.sh` regenerates during
  `make build`. Editing an English page without re-stamping its Russian mirror
  is a hard CI failure.
- Docs touch points found, each with a mirror under `docs/i18n/ru/`:
  - `docs/reference/config/deploy/index.md:118` — the `log` field row,
    currently promising only "ANSI codes stripped";
  - `docs/reference/config/deploy/index.md:235-244` — the `## Related commands`
    list, which enumerates every deploy and reset subcommand;
  - `docs/reference/config/deploy/examples.md` — **the page Part A falsifies
    verbatim**, in three places: `:190` ("a `logSanitizer`-wrapped tee captures
    an ANSI-stripped copy to the on-disk log" — the sequential wiring Task 2
    replaces), `:192` ("`\r` frames are normalised to one-frame-per-line in log
    files via `logSanitizer` (ANSI stripped, `\r\n` collapsed to one `\n`, lone
    `\r` to `\n`)"), and `:194` ("Per-sub-step log files … (ANSI stripped,
    `\r`→`\n`)" — false once the parallel callback gates on `final`). `:191` is
    the PTY / `Reporter.StepOutput` paragraph and is **not** affected; `:197`
    ("CI dumps have no `\r`-spam") stays true;
  - `docs/reference/config/reset.md` — has **no** `## Related commands` section
    (only `## Project-wide reset` and `## Per-service reset`), so Task 9 creates
    one rather than appending;
  - `docs/reference/concepts/pipelines.md:149` — describes the parallel path's
    frame-aware parser.

## Development Approach

- **testing approach**: Regular (code first, then tests within the same task).
- complete each task fully before moving to the next.
- **every task adds or updates tests** for the code it changes; tests are listed
  as their own checklist items, never bundled into an implementation item.
- **all tests pass before the next task starts.**
- run tests with `make test` — `go test ./...` sees an empty, gitignored
  `internal/core/docs/embedded/` tree on a fresh checkout and the docs-subsystem
  tests fail. For focused iteration, `make embedded-docs` once, then
  `go test ./internal/...` on the package under change is fine; finish with
  `make test`.
- after editing anything under `docs/`, run `make build` before `make test` —
  it re-syncs `internal/core/docs/embedded/` and regenerates
  `internal/core/docs/content_hashes_gen.go`.
- **commit discipline**: Part A and Part B go in separate commits so either can
  be reverted alone. A known trap from earlier branches on this release: several
  commits that each append tests to the end of the *same* file produce a
  conflict on `git revert`. Put each part's new tests in their **own new test
  file** rather than appending to an existing one.
- update this plan file when scope changes.

## Testing Strategy

- **unit tests**: required for every task, as above.
- **e2e tests**: this repository has no UI-based e2e suite; the equivalent
  end-to-end surface is `internal/cli/**` command tests driving cobra with
  `SetArgs`, plus the golden-file comparisons in `internal/core/ui/`. Part B's
  command tasks carry command-level tests of exactly that shape.
- **the round-trip test in Part B is the load-bearing test of that part** — it
  is what makes an embedded asset safe as a substitute for a marshaller. It must
  drive the real strict loaders, not a hand-rolled decode.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep this plan in sync with the work actually done

## Solution Overview

### Part A — a frame-aware log writer

Add a stateful writer to `internal/shared/liveui/output.go`, next to
`LogSanitizer`, built on the existing `LineTee`:

- a frame delivered with `final=true` is written to the file as one line;
- a frame delivered with `final=false` is held as **pending** and is *evicted*
  by the next frame. This is exactly terminal semantics: in `A\rB\n` the screen
  shows `B`, so the log records `B`;
- `Flush()` writes a surviving pending frame. That covers output that ended on a
  bare `\r` with no committing newline — a killed clone, a tool that never
  terminates its last progress line.

Three mechanics are load-bearing and each has a wrong-by-default answer, so they
are spelled out here rather than left to the implementer:

1. **Flush ordering.** `LineTee.Flush` delivers the un-terminated tail as
   `final=false` (`output.go:225`), i.e. *into* the pending slot. The composite
   `Flush()` must therefore call `tee.Flush()` **first**, then emit the pending
   frame, then clear it. Idempotency does not come for free the way it does for
   `LineTee` (whose buffer simply empties) — it needs that explicit clear.
2. **Concurrency, and exactly one lock shape works.** The writer being replaced
   advertises "no mutex, safe for concurrent writes from multiple goroutines"
   (`output.go:69-71`), and `stepWriter` reaches builtins as
   `ActionContext.StepWriter`, so the pending-frame state does need protecting.
   But `LineTee` calls its callback **synchronously inside `Write`**, with only
   its own mutex released (`output.go:197-199`). So: the new writer takes its own
   mutex for the whole body of `Write` and of `Flush`, and the callback runs
   **already lock-held** and must not re-acquire it. Locking inside the callback
   instead lets two goroutines evict each other's pending slot out of order,
   which is the guarantee the lock exists for; taking a non-reentrant
   `sync.Mutex` in both places self-deadlocks on the very first frame. Say so in
   a comment on the callback.
3. **Short writes.** `stepWriter` is `io.MultiWriter(os.Stdout, …)`
   (`executor.go:872`), and `io.MultiWriter` returns `ErrShortWrite` unless every
   member returns `len(p)`. `LogSanitizer.Write` returns `len(p)` deliberately
   (`output.go:84`); the replacement must do the same, or a step fails with
   `ErrShortWrite` and its child output goes nowhere.

Wiring is deliberately minimal. In the sequential branch `stepWriter` is already
built per step and the file already has the `flushTee` variable, its defer and
its two eager pre-finish calls. Assigning `flushTee = <writer>.Flush` in that
branch inherits the whole ordering discipline without introducing a new
contract. The parallel branch needs one change: write to `subLog` only when
`final` is set — the same guard
`usercommands/runtime/runners/workflow/parallel.go:216-234` already applies to
the workflow runner's own sub-step logs.

Be precise about what that guard costs, because it is easy to state backwards:
`LineTee.Flush` delivers the tail as `(tail, false)` (`output.go:225`), so the
guard drops a `\r`-terminated tail from the **per-sub-step file** in both
implementations alike. The pipeline path does not lose it, because it has a
second sink the workflow runner has not: the same frame also reaches
`Reporter.StepOutput` → `entry.inProgress` (`plain.go:659-668`) →
`commitTrailingTail` → `writeLog` (`plain.go:700-718`, `:730-735`), so the tail
lands in the global `.dwe/logs/<pipeline>.log` and in the failure buffer dump.
In `parallel.go` the tail reaches only `live.SetBlockRowRunning` and is gone.
That — a second sink, not a different guard — is the whole difference.

Side benefit worth naming in the commit message: `LineTee` strips ANSI twice
(once per write, once over the assembled frame) whereas `LogSanitizer` strips
once, statelessly. Escape sequences split across a PTY read boundary — a known
limitation recorded at `executor.go:840-844` — start being cleaned correctly on
the sequential path too.

`plain.go`'s reporter-status-line sanitiser is untouched: those writes are whole
lines, and giving them buffered state would let a status line interleave with a
pending frame.

### Part B — reference asset instead of a marshaller

**Surface: two new subcommands, not a third `plan --format`.** `dwe deploy plan`
prints the *resolved instance* — rendered steps, inlined per-service pipelines,
evaluated conditions, the `--service` filter, redacted secrets. `deploy.yml` is
the *source*. The `--format shell` slot already means "the resolved plan as a
script", so `--format yaml` beside it would read as "the resolved plan as data",
which is not what is being built. A separate subcommand also lets the
refuse-to-overwrite rule be the subcommand's own policy rather than a hole in
`plan`'s behaviour on the very same project.

**Implementation: an embedded, commented `.yml` asset per pipeline**, living
next to its `Default*Config()` constructor, plus a test that pins the asset to
that constructor. Rationale:

- zero marshalling code inside `internal/core/project/config`, the strictest
  package in the repository, and zero risk of drifting from `UnmarshalYAML` /
  `deployStepKnownFields`, which nothing cross-checks;
- the built-in defaults are small and static — no parallel group, no `Action`,
  no `condition.Condition` — so the expensive general case a marshaller exists
  for never arises;
- unlike a marshaller, an asset carries **comments**, and a commented file is
  precisely what a human wants to receive before editing it.

The pin is a round-trip test: write the asset to a temp file, load it through
the **real** strict loader (`config.LoadProjectDeployConfig` /
`config.LoadResetConfig`, i.e. through `yamlstrict` and `validatePhaseSteps`),
and compare the result with `Default*Config()`. One asymmetry must be handled
explicitly rather than papered over: `DefaultDeployConfig()` sets `Log: &true`
while `DefaultResetConfig()` leaves `Log` nil, and `LoadResetConfig` fills nil
with `&false`. Both assets therefore declare `log:` explicitly (`true` for
deploy, `false` for reset) — which is also better for an ejected file — and the
test normalises a nil `Log` to the loader's documented default before comparing.

Note that "no marshalling code in `config`" is not the same as "no code in
`config`": Part B does add one small **state-carrying loader** there
(`LoadProjectDeployConfigWithState`), because none exists for the
`ProjectDeployConfig` shape today and the only deploy-side alternative,
`ParseDeployConfigForValidationWithState`, returns the lenient `after:`-tolerant
validator shape. That is a sibling of the existing `LoadResetConfigWithState`,
not a new representation of the config, so it does not carry the drift risk the
marshaller would.

**Write policy.** With no `--out` the command prints to stdout and touches
nothing. With an explicit `--out PATH` it **refuses when the target exists**
unless `--force` is given. This deviates on purpose from `docs llms-txt --out`, which overwrites
silently: that writes a generated artifact, this writes a source file a human
edits. It matches `dwe secrets`, which already refuses-unless-`--force` through
`writeOutputFile` (`internal/cli/secrets/files.go:338-352`) — that existing
implementation, not `llms-txt`, is the model to follow for the error code shape,
the hint wording and the non-regular-file guard.

The refusal message must call a file inert on the **same two conditions the
validator uses** (`internal/core/validate/config/workspace.go:1225-1245`):
`PipelineStateDefaultFallback` *or* zero phases. Keying only on
`PipelineFileState` would let a `deploy.yml` holding just `log: false` be called
authored by `eject` while `dwe validate` calls it inert — breaking the very loop
this part exists to close. A file that **fails to load** (a syntax error, an
unknown field) is a third case: the command still refuses, and it refuses as
"there is a file here" rather than propagating the parse error as if it were a
write failure.

`-o json` is accepted and ignored, exactly as already decided for
`docs llms-txt`: the document is itself the payload. No preflight and no project
locks — the command writes an authoring file, not stack state.

The write policy (path resolution, exists check, `--force`, message wording,
error type) lives in **one shared helper in `internal/cli/cmdctx`**, because the
two subcommands sit in different packages and two copies of an overwrite policy
are two things nothing cross-checks.

That helper is **not written from scratch**: `internal/cli/secrets/files.go:338`
already is it, for two commands. Adding a second implementation would make three
copies with three error codes and three hint strings — the exact outcome the
shared helper exists to prevent. So `writeOutputFile` **moves** into `cmdctx`
with the error code as a parameter, and `secrets.writeOutputFile` becomes a thin
wrapper that passes `secrets_output_exists`. Every existing secrets message,
code and guard is preserved byte-for-byte, so no secrets test changes.

Two decisions recorded so an implementer does not have to guess:

- **`--out -` means stdout**, matching the sibling surface. The bare command
  already writes to stdout, so this is not a second way to ask for the same
  thing — it is what stops `--out -` from creating a file literally named `-`.
- **`eject` does not join the interactive `dwe deploy` menu**
  (`internal/cli/deploy/menu.go:54-62`, `:195-309`). That menu offers
  deploy-execution actions; `eject` is an authoring action on a config file, and
  adding it would also churn `menuItemDefs` and its tests at `:496` for no
  discovery win — the command is reachable from `dwe deploy --help` and from the
  `dwe validate` diagnostic that sends people to it.

**Success-path output.** Writing a file prints one confirmation line naming the
path, on stderr, gated behind `flags.Output != "json"` per the JSON-output
contract in `AGENTS.md`; stdout stays empty on the `--out` path so it is never
half a document. Under `--output json` the command emits a small
`{path, pipeline}` object through `cmdctx.WriteData` rather than nothing, which
is what the rest of the tree does for a write. The stdout-emit path prints the
document and nothing else, in json mode too — there the document is the payload.

## Technical Details

### Part A frame semantics

| input bytes | frames delivered by `LineTee` | lines written to the log |
| --- | --- | --- |
| `10%\r50%\r100%\n` | `(10%,false) (50%,false) (100%,true)` | `100%` |
| `10%\r50%\r` then step end | `(10%,false) (50%,false)` + `Flush` | `50%` |
| `a\nb\n` | `(a,true) (b,true)` | `a`, `b` |
| `a\r\nb\n` | `(a,true) (b,true)` | `a`, `b` |
| `abc\rX\n` | `(abc,false) (X,true)` | `X` (terminal shows `Xbc` — documented limitation) |
| `` (nothing) | none | nothing |

### Part B command surface

```
dwe deploy eject [--out PATH] [--force]
dwe reset  eject [--out PATH] [--force]
```

- no `--out`: the asset goes to stdout verbatim, including comments;
- `--out -`: the same, explicitly — matches `dwe secrets`, and keeps `-` from
  becoming a filename;
- `--out ""` is rejected rather than silently writing to `""`;
- there is **no implicit default path**: writing requires an explicit `--out`.
  The canonical target (`workspace/deploy.yml`, `workspace/reset.yml`) is named
  in the help text and the docs, not baked in as a fallback — with the bare
  command already meaning stdout, a "default" would have no invocation that
  reaches it;
- `--out` is resolved and guarded by the promoted `resolveFilePath`: absolute
  via `filepath.Abs` (so a relative path is relative to the cwd, as in
  `dwe secrets`), and inside the project root it additionally passes
  `pathsafe.ContainedRel` and `CheckNoSymlinks`;
- on a successful write: one confirmation line on stderr naming the path. Under
  `--output json` that line is suppressed and the command emits
  `{"path": …, "pipeline": "deploy"|"reset"}` through `cmdctx.WriteData`
  instead — silence would be unlike every other write command in the tree
  (`dwe secrets` emits `{from,to}`, `files.go:259-260`);
- `--out -` combined with `--output json` behaves like the bare command: the raw
  document on stdout, the flag ignored. Note that `dwe secrets` **rejects** that
  combination (`checkStreamMode`, `files.go:263-273`) — do not import that guard
  while moving the helpers, it belongs to a command whose payload is JSON;
- exit codes and error envelopes follow the existing typed `cmdctx.Err` /
  `ErrWrap` convention so `--output json` still yields a `{"error":{…}}`
  envelope on stderr.

## What Goes Where

- **Implementation Steps** — code, tests, docs, CHANGELOG.
- **Post-Completion** — the follow-ups this branch deliberately does not carry.

## Implementation Steps

### Task 1: Add the frame-collapsing log writer to `liveui`

**Files:**
- Modify: `internal/shared/liveui/output.go`
- Modify: `internal/shared/liveui/output_test.go` (comment wording only)
- Create: `internal/shared/liveui/logframe_test.go`

- [x] add a stateful frame-collapsing writer next to `LogSanitizer` in
      `internal/shared/liveui/output.go`, constructed over a `LineTee`: a
      `final=true` frame is written as one line; a `final=false` frame replaces
      the pending frame; `Flush()` writes a surviving pending frame
      (`FrameLogWriter` / `NewFrameLogWriter`)
- [x] make `Flush()` call `tee.Flush()` **first**, then emit the pending frame,
      then clear it — `LineTee.Flush` delivers the tail as `final=false`
      (`output.go:225`), i.e. into the pending slot, and the explicit clear is
      what makes the composite idempotent
- [x] give the pending state its **own mutex, held across the whole `Write` and
      `Flush` body**, with the `LineTee` callback running lock-held and never
      re-acquiring it — `LineTee` calls the callback synchronously inside `Write`
      (`output.go:197-199`), so a non-reentrant `sync.Mutex` taken in both places
      deadlocks on the first frame, while locking only inside the callback lets
      two goroutines evict each other's pending slot. Put the reason in a comment
      on the callback
- [x] return `len(p)` from `Write` regardless of what the underlying writer
      consumed, as `LogSanitizer.Write` does at `output.go:84` — `stepWriter` is
      an `io.MultiWriter` (`executor.go:872`) and anything else is `ErrShortWrite`
- [x] document on the type why lone `\r` is collapsed rather than expanded,
      naming the `abc\rX\n` limitation and that the compose-repeat case is out of
      scope — this comment is the counterpart to `LogSanitizer`'s `:58-71` block,
      which documents the opposite choice for its own callers
- [x] write table-driven tests in the new `logframe_test.go` covering every row
      of the frame-semantics table above (progress run ending in `\n`, run ending
      in a bare `\r` plus `Flush`, plain lines, CRLF, shorter-overwrite, empty
      input)
- [x] write tests for split writes: a frame delivered across two `Write` calls,
      and an ANSI escape sequence split across the same boundary (the double-strip
      behaviour inherited from `LineTee`)
- [x] write tests for `Flush` idempotency (two consecutive `Flush` calls emit the
      pending frame exactly once), for `Flush` on an empty writer emitting
      nothing, and for `Write` returning `len(p)` on a short underlying writer
- [x] write a concurrent-write test in the shape of the existing
      `TestLogSanitizer_ConcurrentWrites_NoPanic` (`output_test.go:301`), aimed at
      `make test-race`
- [x] re-word the two stale captions in `output_test.go` — `:195-218`
      (`TestSubStepLog_RoutedViaLineTee_SplitOSCClean` describes writing "both
      final and non-final" frames to the sub-step log) and `:231-242`
      (`TestLogSanitizer_ProgressFrames_BecomeSeparateLines` calls itself "the
      regression test for the live-progress bug") — so each says which path it
      still pins. Both tests keep passing; only the prose is wrong. Comment-only,
      so this stays revert-safe
- [x] run tests - must pass before task 2 (`make lint`, `make test`,
      `go test -race ./internal/shared/liveui/` all green)

### Task 2: Route both executor log paths through the frame writer

**Files:**
- Modify: `internal/core/execution/pipeline/executor.go`
- Create: `internal/core/execution/pipeline/logframe_wiring_test.go`

- [x] in the sequential branch (`executor.go:864-876`) replace
      `&liveui.LogSanitizer{W: opts.LogWriter}` with the new frame writer and set
      `flushTee` to its `Flush`, so the existing defer (`:877-879`) and the eager
      pre-finish calls (`:931-932`, `:975-976`) cover it
- [x] in the parallel branch (`executor.go:845-847`) write to `subLog` only when
      `final` is set, leaving `opts.Reporter.StepOutput` receiving every frame as
      before (the live block row still needs the non-final frames)
- [x] cite `usercommands/runtime/runners/workflow/parallel.go:216-234` in the
      code comment as the existing precedent, and record what the guard costs:
      because `tee.Flush()` delivers the tail as `final=false`, both
      implementations drop a `\r`-terminated tail from the per-sub-step file. The
      pipeline path keeps it only because the same frame also reaches
      `Reporter.StepOutput` → `commitTrailingTail` → the global pipeline log
      (`plain.go:659-668`, `:700-718`); `parallel.go` has no second sink. Do not
      add pending-frame state to the parallel callback to "fix" this — that would
      be a new composite flush hook and is not this plan's scope
- [x] extend the comment block at `:815-829` so it states which writer each path
      now uses and why the sequential path gained state, and fix the stale
      example in `childIO`'s doc at `:51-70`, which spells the sequential shape
      as `io.MultiWriter(os.Stdout, logSanitizer{logFile})` at `:56-58`
      (the identical stale example on `ActionContext.StepWriter` was fixed too)
- [x] write a test that runs a step whose child emits a CR progress run and
      asserts the global pipeline log holds one line, not one per frame
- [x] write a test for the parallel path asserting the per-sub-step log holds only
      committed lines while the reporter still observed the non-final frames
      (that observation is what feeds the tail into the global log)
- [x] write a test that a **sequential** step ending on a bare `\r` still has its
      last frame in the global `.dwe/logs/<pipeline>.log` (the `flushTee` →
      pending-frame path). If a parallel assertion is wanted too, assert the tail
      in the **global** log via `commitTrailingTail` — not in
      `.dwe/logs/parallel/**`, where the `final` gate legitimately drops it
- [x] write an **ordering** test over the shared `.dwe/logs/<pipeline>.log`: after
      this change that one file handle has a buffered writer (the frame writer)
      and an unbuffered one (`PlainReporter`'s own `LogSanitizer`,
      `plain.go:152-155`) writing to it, and only the pre-existing
      `flushTee`-before-finish discipline keeps them in sequence. Assert a step's
      last child line precedes the reporter's finish line for that step — "still
      pass unchanged" and "appears exactly once" do not cover interleaving
- [x] confirm `internal/core/execution/pipeline/logging_test.go` and
      `plain_test.go` still pass unchanged — in particular
      `TestPlainReporter_LogFile_StatusLines_ExactlyOnce` and
      `TestPlainReporter_StatusLineReachesLogFile`, which pin the reporter path
      this task must not disturb
- [x] run tests - must pass before task 3 (`make lint`, `make test`,
      `go test -race ./internal/core/execution/pipeline/ ./internal/shared/liveui/`
      all green)

### Task 3: Commit Part A and record it in docs and CHANGELOG

**Files:**
- Modify: `docs/reference/config/deploy/index.md`
- Modify: `docs/reference/config/deploy/examples.md`
- Modify: `docs/reference/concepts/pipelines.md`
- Modify: `docs/i18n/ru/reference/config/deploy/index.md`
- Modify: `docs/i18n/ru/reference/config/deploy/examples.md`
- Modify: `docs/i18n/ru/reference/concepts/pipelines.md`
- Modify: `CHANGELOG.md`

- [x] amend the `log` field row at `docs/reference/config/deploy/index.md:118`:
      the file receives one line per committed line, redraw frames are collapsed
      to the last frame, and ANSI codes are stripped
- [x] state the `abc\rX\n` limitation once, in the same place, so it is
      discoverable from the field that promises the behaviour
- [x] state the one behavioural loss in the same row: today each `\r` frame
      reaches disk immediately, so `tail -f .dwe/logs/deploy.log` shows clone
      progress live; after this change those frames never reach the file at all
- [x] rewrite the three falsified lines in
      `docs/reference/config/deploy/examples.md`: `:190` (the
      `logSanitizer`-wrapped sequential tee), `:192` (the old `\r` normalisation)
      and `:194` (per-sub-step logs described as `\r`→`\n`). Leave `:191` (PTY /
      `Reporter.StepOutput`) and `:197` ("CI dumps have no `\r`-spam") alone;
      both stay true
- [x] check whether `docs/reference/concepts/pipelines.md:149` (the parallel
      frame parser paragraph) now describes both paths and update it if it does
      not — it did not; it now names both routes and the collapse rule
- [x] update the Russian mirrors of all three pages and re-stamp their
      `> Translated from: … @ <hash>` headers
- [x] add a `### Changed` entry under `## [Unreleased]` in `CHANGELOG.md` naming
      the measured before/after, the documented limitation and the loss of live
      `tail -f` progress
- [x] run `make build` (re-syncs embedded docs, regenerates
      `content_hashes_gen.go`), then `make test` — `TestRussianTranslationsAreFresh`
      is the gate that catches a missed re-stamp
- [x] commit Part A as its own commit before starting Part B

### Task 4: Embed the default deploy pipeline as an asset

**Files:**
- Create: `internal/core/workflow/deploy/default_deploy.yml`
- Modify: `internal/core/workflow/deploy/defaults.go`
- Create: `internal/core/workflow/deploy/asset_test.go`

- [x] author `default_deploy.yml` as the commented, human-editable form of
      `DefaultDeployConfig()` (`defaults.go:8-56`), declaring `log: true`
      explicitly and carrying a short comment per phase explaining what it does
- [x] expose it from `defaults.go` via `//go:embed` behind an accessor that
      returns the bytes, with a doc comment pointing at the round-trip test as
      the reason the asset and the constructor cannot drift
      (`DefaultDeployYAML()`, returning a fresh copy per call)
- [x] write the round-trip test: write the asset to `t.TempDir()`, load it with
      `config.LoadProjectDeployConfig`, compare against `DefaultDeployConfig()`
      using `require.Equal` (testify is already a dependency and prints a
      readable struct diff — do **not** add `go-cmp` for this, and do not use a
      bare `reflect.DeepEqual`, whose failure across a 3-phase struct is
      unreadable)
- [x] add a comment to `defaults.go` next to the `With: map[string]any{…}`
      literals recording that the `map[string]any` / `[]any` shapes are what
      yaml.v3 decodes into, and that writing a `[]string` there would break the
      round-trip test for a reason unrelated to the asset
- [x] write a test asserting the asset's own header comment survives in the
      returned bytes (the emitted file must be commented — a marshaller-shaped
      regression would silently drop them); it also asserts a per-phase comment,
      the explicit `log: true` and the fresh-copy contract
- [x] run tests - must pass before task 5 (`make lint`, `make test` green)

### Task 5: Embed the default reset pipeline as an asset

**Files:**
- Create: `internal/core/workflow/reset/default_reset.yml`
- Modify: `internal/core/workflow/reset/defaults.go`
- Create: `internal/core/workflow/reset/asset_test.go`

- [x] author `default_reset.yml` as the commented form of `DefaultResetConfig()`
      (`defaults.go:6-66`), declaring `log: false` explicitly, and keep the
      existing explanatory comment about `docker_remove_project_volumes` not
      taking `continue_on_error` (`defaults.go:45-50`) — it is exactly the kind of
      thing an author editing the ejected file needs
- [x] expose it via `//go:embed` behind the same accessor shape as Task 4
      (`DefaultResetYAML()`, returning a fresh copy per call)
- [x] write the round-trip test using `config.LoadResetConfig` and
      `require.Equal`, normalising the `Log` asymmetry explicitly:
      `DefaultResetConfig()` leaves `Log` nil while the loader fills `&false`
- [x] add a comment in the test naming that asymmetry, so a future reader does
      not "simplify" the normalisation away, and the same `[]any` decode-shape
      note as Task 4 next to `reset/defaults.go:57-59`
      (`With: {"paths": []any{"services/"}}`)
- [x] ➕ note as a separate one-line fix (or a follow-up if it grows): the doc
      comment on `LoadResetConfig` at `workspace.go:3299` claims reset pipelines
      "must not contain deploy_services phases", but the decoder passes
      `allowDeployServices=true` for reset as well (`:3197`). Do **not** write a
      test asserting the documented-but-absent restriction —
      **confirmed and deferred to Task 12**, which already carries the one-line
      comment fix; no test asserts the absent restriction
- [x] run tests - must pass before task 6 (`make lint`, `make test` green)

### Task 6: Promote the existing output-file writer into `cmdctx`

**Files:**
- Create: `internal/cli/cmdctx/outputfile.go`
- Create: `internal/cli/cmdctx/outputfile_test.go`
- Modify: `internal/cli/secrets/files.go`

- [x] move **both** `resolveFilePath` (`internal/cli/secrets/files.go:284-318`)
      and `writeOutputFile` (`:338-370`) into `cmdctx`. They are a pair — every
      secrets caller runs the first for `filepath.Abs`, `pathsafe.ContainedRel`,
      `CheckNoSymlinks` and the empty-path rejection before the second writes —
      and moving only the writer would leave `eject`'s path discipline undefined
      (`cmdctx.ResolveFilePath` / `cmdctx.WriteOutputFile`, the latter taking a
      `cmdctx.OutputFile` struct rather than six positional parameters)
- [x] parameterise the code **prefix**, not one code: the pair hardcodes four
      codes across five sites — `secrets_path_invalid`, `secrets_output_exists`
      (`:342`), `secrets_output_invalid` (`:347`) and
      `secrets_output_write_failed` (`:353`, `:363`, `:367`). Take a prefix
      string and build `<prefix>_path_invalid` / `_output_exists` /
      `_output_invalid` / `_output_write_failed`, so `secrets` passing `secrets`
      reproduces all four verbatim and `eject` gets its own namespace instead of
      emitting `secrets_*` codes in its JSON envelope
- [x] generalise the chmod condition: today it is `existed && mode ==
      plaintextMode` (`:361`) against a secrets-package constant (`:40`). Make it
      a caller-supplied "tighten the mode on an existing file" boolean; `secrets`
      passes what `plaintextMode` decided, `eject` passes false (an ejected
      pipeline is not sensitive) (`OutputFile.TightenMode`)
- [x] reduce `secrets.resolveFilePath` / `secrets.writeOutputFile` to thin
      wrappers passing the `secrets` prefix, so every existing secrets message,
      code and test stays byte-for-byte identical
- [x] `resolveFilePath` calls `isUnder` (`files.go:384`), which `displayPath`
      (`:397-398`) also uses — export it alongside the move or keep a copy in
      `secrets`; it is a compile error either way, noted so it is not mistaken for
      a scope surprise (exported as `cmdctx.PathIsUnder`; `secrets.isUnder` is
      gone and `displayPath` calls the exported one)
- [x] add an optional "why the existing file matters" note to the refusal, fed by
      the caller, so `eject` can say the existing file is inert and that the
      built-in pipeline is what runs today — the helper itself loads nothing
      (`OutputFile.ExistsNote`, appended to the refusal message after an em dash)
- [x] have the caller-side inert check cover **both** conditions the validator
      uses (`internal/core/validate/config/workspace.go:1225-1245`):
      `PipelineStateDefaultFallback` *or* zero phases; a `deploy.yml` holding only
      `log: false` is inert to `validate` and must be inert here too
      (`cmdctx.InertPipelineNote(state, phases, name)` — shared by both eject
      commands so the two conditions cannot drift apart between them)
- [x] write tests for: target absent → written; target present without force →
      refused, file untouched byte-for-byte; target present with force →
      overwritten; target is a directory or a symlink **with `--force`** → the
      non-regular-file guard fires (without `--force` those surface as "already
      exists", because the guard sits after the early return at `:341-350`);
      unwritable directory → error surfaced, not swallowed
- [x] write a test asserting the refusal message names the path and distinguishes
      an inert existing file from an authored one, including the `log:`-only case
      (driven through the real `config.LoadResetConfigWithState`, so the note
      tracks the loader rather than a hand-built state)
- [x] confirm the existing secrets tests pass **unmodified** — that is the check
      that the move preserved behaviour (no file under
      `internal/cli/secrets/*_test.go` was touched)
- [x] run tests - must pass before task 7 (`make lint` 0 issues, `make test`
      green)

### Task 7: Add `dwe deploy eject`

**Files:**
- Modify: `internal/cli/deploy/deploy.go`
- Create: `internal/cli/deploy/eject.go`
- Create: `internal/cli/deploy/eject_test.go`
- Modify: `internal/core/project/config/workspace.go`
- Create: `internal/core/project/config/deploy_state_test.go`

- [x] add `LoadProjectDeployConfigWithState` to
      `internal/core/project/config/workspace.go` next to `LoadResetConfigWithState`
      (`:3310`), delegating to `loadProjectDeployConfigDecode(path, true)`. It does
      not exist today, and the only deploy-side alternative
      (`ParseDeployConfigForValidationWithState`, `:3270`) returns the lenient
      `*DeployConfig` shape that tolerates `after:` — the validator's shape, not
      the one `deploy.yml` actually loads through
- [x] write a test for the new loader in its own file: authored file → phases plus
      `PipelineStateAuthored`; all-comment file → `PipelineStateDefaultFallback`;
      `log: false`-only file → authored state but zero phases; absent file →
      `os.ErrNotExist`
- [x] add `newDeployEjectCmd` in `eject.go` and register it at
      `deploy.go:54-56`, with `Args: cobra.NoArgs` and a `Long:` that states it
      emits the **built-in default**, not the project's effective pipeline
- [x] add `--out PATH` (never `--output`, which would shadow the root flag's
      `-o`) and `--force`; treat `--out -` as stdout, reject `--out ""`, and name
      the canonical `workspace/deploy.yml` in the help text rather than making it
      an unreachable default
- [x] with no `--out` (or `--out -`), write the asset to the command's stdout
      writer and return; no preflight, no locks, no config load on this path
- [x] with `--out`, resolve the path through the promoted `resolveFilePath`,
      derive the existing file's state via the new loader, and delegate to the
      promoted writer, passing the `deploy_eject` code prefix. When the existing
      file **fails to load** (syntax error, unknown field), still refuse — as "a
      file is already here", never by propagating the parse error as if it were a
      write failure
- [x] print the success confirmation to stderr, gated on
      `flags.Output != "json"`; do **not** add the command to the interactive
      `dwe deploy` menu (`menu.go:54-62`) — decision and reason are recorded in
      Solution Overview
- [x] use `cmd.MarkFlagFilename("out", "yml", "yaml")` rather than a custom
      `ValidArgsFunction`; cobra already file-completes a string flag, and a
      custom function that touched project state would pull in the
      `cmdctx.CompletionConfigPath` obligation for no gain
- [x] write command tests: stdout emit contains the phase names and its comments;
      `--out -` emits to stdout and creates no file; `--out` into an empty dir
      writes a file that `config.LoadProjectDeployConfig` loads; `--out` onto an
      existing file refuses and leaves it unchanged; `--out` onto an unparseable
      file refuses with the same "already here" shape; `--out --force` overwrites
- [x] write three json tests: `-o json` on the stdout path emits the raw document
      with no envelope; `-o json` on the `--out` path writes the file and emits
      `{path, pipeline}` with no stderr confirmation line; `-o json --out -`
      behaves like the bare command
- [x] write a test asserting the refusal's error code is namespaced to this
      command, not `secrets_*` — that is what the code-prefix parameter in Task 6
      exists for
- [x] run tests - must pass before task 8

### Task 8: Add `dwe reset eject`

**Files:**
- Modify: `internal/cli/lifecycle/reset.go`
- Create: `internal/cli/lifecycle/eject.go`
- Create: `internal/cli/lifecycle/eject_test.go`

- [x] add `newResetEjectCmd` in `eject.go` and register it at
      `reset.go:56-58`, mirroring Task 7's flags and behaviour (`--out`,
      `--out -`, `--force`, stderr confirmation gated on json) against the reset
      asset and `workspace/reset.yml`, using the already-existing
      `config.LoadResetConfigWithState` for the state check
- [x] keep the two commands' user-facing wording parallel — the difference
      between them should be the pipeline name and nothing else
- [x] write the same command-test set as Task 7, loading the written file with
      `config.LoadResetConfig`
- [x] write a test that the written file loads with logging **off**, which is
      what a user gets from an ejected reset pipeline. Note in the test that the
      value comes from the asset's explicit `log: false` and not from the loader's
      `defaultLog` — `loadProjectDeployConfigDecode` applies `defaultLog` only
      when `cfg.Log == nil` (`workspace.go:3200-3203`) — which is exactly why the
      asset must keep the key: drop it and the file's behaviour silently depends
      on which loader reads it
- [x] run tests - must pass before task 9 (`make lint` 0 issues, `make test`
      green)

### Task 9: Document both subcommands

**Files:**
- Modify: `docs/reference/config/deploy/index.md`
- Modify: `docs/reference/config/reset.md`
- Modify: `docs/i18n/ru/reference/config/deploy/index.md`
- Modify: `docs/i18n/ru/reference/config/reset.md`

- [x] add `dwe deploy eject` and `dwe reset eject` to the `## Related commands`
      list at `docs/reference/config/deploy/index.md:235-244`, stating the
      built-in-default scope, the stdout-vs-`--out` split and the refuse-unless-
      `--force` rule
- [x] create a `## Related commands` section in
      `docs/reference/config/reset.md` — it has none today (only
      `## Project-wide reset` and `## Per-service reset`) — and tie it to the
      `dwe validate` report about an inert pipeline file so a reader arriving from
      that diagnostic finds the action
- [x] state explicitly on both pages that there is no lifecycle equivalent, and
      why (`_auto_reap_daemons` would not load back)
- [x] update both Russian mirrors and re-stamp their `> Translated from:` headers
- [x] run `make build` then `make test`
- [x] run tests - must pass before task 10 (`make build`, `make test` green;
      `TestRussianTranslationsAreFresh` re-run uncached)

### Task 10: CHANGELOG entry for Part B

**Files:**
- Modify: `CHANGELOG.md`

- [x] add an `### Added` entry under `## [Unreleased]` for both subcommands,
      naming the built-in-default scope, the overwrite policy and its deliberate
      difference from `docs llms-txt --out`, and the absence of a lifecycle
      variant
- [x] verify the Part A entry from Task 3 is still accurate after the final
      implementation and adjust the measured numbers if they moved — it is:
      both executor routes, the pending-frame eviction, the `abc\rX\n`
      limitation, the out-of-scope compose repeats and the lost live `tail -f`
      all match what shipped; the measured 1001 / ~601 numbers are unchanged
- [x] note that this task's `### Added` block and Task 3's `### Changed` block
      land in different subsections of `## [Unreleased]` on purpose — the plan's
      own revert rule about several commits appending to the same file applies to
      `CHANGELOG.md` too, and separate subsections are what keeps the two commits
      from conflicting (the Part B entry ends `### Added`, the Part A one opens
      `### Changed`, so the two commits touch disjoint hunks)
- [x] run `make test` — the CHANGELOG is release-notes input
      (`scripts/changelog-release-notes.sh`), so it gets the same gate every other
      docs-touching task in this plan carries

### Task 11: Verify acceptance criteria

- [ ] `dwe deploy eject` with no `--out` prints a commented pipeline; feeding
      that output back through `config.LoadProjectDeployConfig` yields
      `DefaultDeployConfig()`
- [ ] the same holds for `dwe reset eject` against `DefaultResetConfig()`
- [ ] `--out` refuses an existing file and leaves it byte-for-byte unchanged;
      `--force` overwrites; the refusal names the file as inert both for an
      all-comment file and for one carrying only `log: false`, matching what
      `dwe validate` says about the same file
- [ ] `--out -` writes to stdout and creates no file named `-`
- [ ] under `--output json` the `--out` path writes the file and prints no
      confirmation line; the stdout path emits the raw document with no envelope
- [ ] the existing `dwe secrets` output-file tests pass unmodified after the
      helper move
- [ ] a pipeline log from a step with a CR progress run holds one line per
      committed line, and the last frame of a `\r`-terminated run survives
- [ ] reporter status lines still appear exactly once in the log file
- [ ] confirm `deploy` and `reset` are still absent from `bridgeAllowedTopLevel`,
      so neither subcommand is reachable from a bridged container
- [ ] `make lint` clean
- [ ] `make test` green (this repo has no separate e2e command; the CLI command
      tests added above are the end-to-end surface)
- [ ] `make test-race` green — the frame writer's own lock is the reason this
      run is not optional here
- [ ] `make build` produces a binary whose `dwe deploy --help` and
      `dwe reset --help` list the new subcommand

### Task 12: [Final] Update documentation

- [ ] do **not** touch `AGENTS.md`. It is 40914 B against the 40960 B
      `agentsMdBudget` pinned by `TestAgentsMdBudget`
      (`internal/cli/docs/agentsmd_test.go:28`, `:63-67`) — 46 bytes of headroom,
      where an existing Critical Patterns bullet runs 600-1500 B. The repo's own
      rule ("new invariants go into `packages.md` and gain at most a pointer
      here") resolves this in favour of `packages.md` only; adding a pointer would
      require trimming an unrelated bullet in the same commit, which is not this
      branch's business
- [ ] add the per-package notes to `docs/internals/packages.md`: the frame writer
      under `internal/shared/liveui/` (including its three mechanics — flush
      ordering, own lock, `len(p)` return), the assets and their round-trip pin
      under Core — Workflow, and the promoted output-file helper under the CLI
      section, noting that `secrets` now delegates to it
- [ ] fix the stale doc comment on `LoadResetConfig`
      (`internal/core/project/config/workspace.go:3299`), which claims reset
      pipelines must not contain `deploy_services` phases while the decoder passes
      `allowDeployServices=true` for them (`:3197`) — a one-line comment fix, or a
      recorded follow-up if closing the gap for real turns out to be behavioural
- [ ] verify `README.md` needs no change (no new top-level command)
- [ ] run `make build` and `make test` once more after the docs edits
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes.*

**Manual verification:**

- Run `dwe deploy run` in a workspace with a git-clone step (the measured case
  had 1001 log lines) and compare the new `deploy.log` line count against the
  recorded baseline. The compose `[+] up 2/3` repeats are expected to **remain** —
  their removal is deliberately out of scope, and seeing them survive is the
  confirmation that only CR frames were collapsed.
- Run a pipeline with a `parallel:` group and confirm the per-sub-step logs under
  `.dwe/logs/parallel/**` lost their redraw frames while the live block rows
  still animated during the run.
- Eject into one of the three local projects that carry an inert
  `workspace/deploy.yml`, confirm the refusal names the file as inert, then
  `--force` and confirm `dwe validate` stops reporting the inert-pipeline state.

**Follow-ups deliberately not in this branch:**

- Capturing a raw PTY stream from a compose `up` to confirm whether the repeated
  `[+] up 2/3` frames are CUU sequences, which is the prerequisite for handling
  them at all.
- Reconciling the workflow runner's sub-step logs with the pipeline's. Both drop
  a `\r`-terminated tail from the per-sub-step file, but the pipeline path
  recovers it in the global log through `commitTrailingTail` while
  `usercommands/runtime/runners/workflow/parallel.go:216-234` loses it outright,
  and `docs/reference/config/commands/types.md:527` documents that behaviour.
  Giving the workflow runner an equivalent second sink is a separate change.
- The agent-skill sweep: `skills/dwe/**` and the scaffold `AGENTS.md.tmpl` need a
  line about the new subcommands. That audit is scheduled as its own piece of
  work together with the release documentation, and is intentionally not started
  here.
- Deciding whether `dwe init` should stop scaffolding an all-comment
  `deploy.yml` now that the built-in pipeline can be ejected on demand. That is a
  template change plus a `testdata/golden_default.txt` regeneration and was
  scoped out.

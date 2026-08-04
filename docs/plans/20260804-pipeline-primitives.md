# Plan B — Pipeline primitives (remove the copy-paste, fix its causes)

## Overview

A survey of five real DWE workspaces (podlapka, AlbFetcharr, beetDeck, alto, cueBreaker —
19 services, ~130 pipeline steps, 39 command files) found three patterns that are copied
near-verbatim between projects. In each case the copying is not stylistic — it works
around a missing capability:

| Pattern | Spread | Why it is copied |
|---|---|---|
| `source.clone` private command + `when:`/`check:` gate | **5/5**, 11 instances, one shape | the only idempotent clone recipe there is |
| `when:` + `check:` expressing the same predicate inverted | **21 of 26** `when:` steps | no way to say "the check is just the inverse" |
| `quality.staged` — ~40 lines of bash rebuilding `docker compose exec` | **4/5**, near-identical | `service_exec` cannot express a *computed* argv |

This plan fixes the two underlying causes (`argv_append_from`, `check: auto`) and adds the
one primitive stable enough to be worth freezing (`source_clone`). Deliberately **not**
added: `archive_fetch`/`archive_unpack` — 4/5 copy it, but the source is infrastructure
specific (SFTP on a private host) and freezing it into the binary would bake one shop's
setup into a general-purpose tool. It stays a recipe (Plan C).

**Depends on Plan A** (`docs/plans/20260804-agent-feedback-loop.md`), Tasks 1–2:
resolve-time rendering of `${…}` across the whole step, **including the string leaves of
`with:`** — that is what lets `source_clone` receive `with: {repo: "${vars.source.repo}"}`
at all. (Plan A's first draft rendered only `cmd:` at exec time; the correction is why this
dependency is now stated on `with:`.)

**Honest accounting of what `source_clone` is worth**, since two of its three original
justifications get consumed by other work: Plan A makes `${vars.*}` interpolate in a plain
`type: shell` clone, and Task 3 below collapses the `when:`/`check:` pair for that shell
step too. What remains is `pathsafe` containment, the "non-empty non-git destination"
error, and one line saved across 11 call sites. That may well be enough — but it should be
argued on those grounds. **Task 5 is therefore the most droppable task in this plan**, and
that is stated deliberately rather than discovered mid-implementation.

## Context (from discovery)

- `internal/core/usercommands/model/types.go:836/839` — `CommandDef.Cmd` and
  `CommandDef.Argv` are shared across command types and mutually exclusive
  (validated at lines 1049–1062, 1133–1138). `service_exec` fields (`Service`, `User`,
  `Workdir`, `WorkdirFrom`, `Mode`, `ComposeArgs`) live in the same struct (lines 844–857).
- `ArgsSpec` (`Args *ArgsSpec`, line 843) is the **caller-supplied** pass-through added on
  this branch (`${args}` after `--`). `argv_append_from` is its computed sibling and must
  not be confused with it: `${args}` comes from the invoker, `argv_append_from` is derived
  by the command itself. Both may appear on one command; ordering must be defined.
- `${args}` is rewritten to `"$@"` **before** rendering and passed as positional
  parameters (branch commits `2a1d6b73`, `0429a6e9`) precisely so caller bytes never reach
  the shell program. `argv_append_from` must honour the same principle: appended items are
  argv elements, never spliced into a shell string.
- `internal/core/execution/builtin/builtin.go:97` — `buildRegistry()` merges per-package
  maps (`containers`, `services`, `fs`, `env`, `interaction`), each exposing `Builtins()`,
  and panics on duplicate names. `spec.Entry{Impl, Kind}` with
  `KindAction | KindPredicate | KindInternal`; `kindAllowed` (line 147) permits
  Action **and** Predicate in `CtxUserYAML` and `CtxPredicate`.
- `internal/core/execution/pipeline/resolve.go:138` — `check:` builtins are validated
  against `builtin.CtxPredicate` at resolve time.
- `internal/core/execution/pipeline/forcesrun.go` — `StepForcesRun` returns true when
  `step.Check != nil` (plus predicate-body builtins, plus one level into parallel groups).
  It feeds deploy's up-to-date early gate and the per-step skip decider
  (`hasCheck → Run` in `journal/decision.go`). **`check: auto` must land in the journal as
  a real check**, or an auto-check step would silently stop forcing re-runs.
- `internal/shared/pathsafe/` — the existing guard for paths that must stay inside the
  project. `source_clone` writes to a caller-supplied directory and must go through it.
- Observed shape of the gate being replaced (identical in 4 projects, per-service variant
  in the fifth):
  ```yaml
  when:  { type: shell,   cmd: "[ ! -e services/backend/src/.git ]" }
  check: { type: builtin, cmd: shell, with: { cmd: "test -e services/backend/src/.git" } }
  ```
- Observed `check:` forms across all five workspaces: **26 of 27** are
  `builtin shell` + `test …`. **This is NOT a discoverability problem** — an earlier draft
  said so and was wrong. There are **two disjoint `type: builtin` registries**, documented
  under the literal heading "Two `type: builtin` registries" in
  `docs/reference/config/conditions.md`:
  - `when: {type: builtin}` → `condition.EvalBuiltin`
    (`internal/core/execution/condition/condition.go`): `dir-exists`, `dir-missing`,
    `dir-empty`, `dir-not-empty`, `file-exists`, `file-missing`, `generated-missing`
  - `check: {type: builtin}` → `builtin.Get(name, CtxPredicate)` over `buildRegistry()`:
    `shell`, `tcp_reachable`, `http_check`, `config_keys_present`, `file_exists` (different
    name **and** different shape — `with: {path}`), `env_keys_present`,
    `executable_in_path`, `containers_running`

  The intersection is **empty**. Authors write `test -n "$(ls -A …)"` in `check:` because
  `dir-not-empty` is *unavailable there*, not because they failed to find it. This kills
  the "negate the predicate" branch of `check: auto` (see Technical Details) and must be
  corrected in Plan C, whose Task 7 planned to write the wrong claim into the skill and
  `llms-txt`.

## Critical constraints for the executor (traps — read before every task)

1. **`make build` / `make test`, never bare `go test ./...`** (generated embedded docs).
2. **Adding a new `CommandDef` field requires the strict-decode allowlists to agree.**
   Command files are loaded with `KnownFields(true)`; a new key that the model knows but a
   validator's allowlist does not will fail every fixture using it.
3. **`argv_append_from` executes a host shell command — the OUTPUT contract.** It must run
   through the same `config.ShellBin` path and cancellation binding as other host
   execution, and its output must be treated as **data**, never re-parsed as shell. One
   argv element per output line; trailing newline ignored; empty output is a *legal* result
   meaning "nothing to append".
3a. **The INPUT contract — this is where the closed injection hole can reopen.** The field
   holds a host shell *program*, so whatever renders its text decides whether caller bytes
   can reach it. Non-negotiable:
   - render it through **`runio.withoutArgs(rc)`**, never a plain `rc`. The comment at
     `runtime/internal/runio/args.go:44-49` states the reason: leaving `Args` visible lets
     the raw Go-template form `{{ index .Args 0 }}` interpolate a caller-controlled string
     straight into program text — "the exact hole the `"$@"` transport exists to close".
     Neither `RenderShellCommand` nor `RenderArgvWithArgs` renders without it; a new field
     is easy to miss.
   - reject a literal `${args}` inside `argv_append_from` at load time, the same way
     `--filter=${args}` is already rejected in `CommandDef.Validate`.
   - decide and record whether `${param.*}` is allowed here. `ParamDef.Pattern`
     (`types.go:579-582`) is optional, so by default a param is a free-form caller string.
     If allowed, require an anchored `pattern:` on any param reachable from this field.
   - regression test in the exact shape of the `2a1d6b73` exploit:
     `dwe cmd x -- '$(touch /tmp/pwned)'` against a command carrying `argv_append_from`.
3b. **Trust boundary: `argv_append_from` runs on the HOST even for a `service_exec`
   command invoked from a container.** `commands` is in `bridgeAllowedTopLevel`
   (`internal/cli/bridgepolicy.go:30`) and a command opts in per-command via
   `bridge: {enabled: true}`. The daemon's env hardening does **not** help here — an
   injection would be in program *text*, not in the environment. With 3a implemented the
   vector is closed; without it, it is open. Document the host-side execution explicitly so
   command authors see the boundary, and cover it with a test.
4. **Empty append must not silently change the command's meaning.** `ruff check` with an
   empty file list lints the whole tree — the opposite of the intent. Define and test the
   empty-list behaviour explicitly (skip the step, not "run with no args").
5. **`check: auto` must produce a real resolved check**, so `StepForcesRun` and
   `journal.Decide`'s `hasCheck → Run` lever keep working unchanged.
6. **Inversion is a logical negation, not a string edit.** For a shell condition, wrap:
   `sh -c '! ( <original> )'`. For a predicate builtin, negate the boolean result — do not
   attempt to map to a "paired opposite" predicate name (`dir-empty` → `dir-not-empty`
   looks tempting and is wrong for edge cases such as a missing directory).
7. **`source_clone` is `KindAction`, not `KindPredicate`** — it mutates. Its gate is
   internal, which is exactly what removes the caller's `when:`/`check:` pair.
8. **Never let `source_clone` write outside the project** — route the destination through
   `internal/shared/pathsafe/`.
9. **Builtin names are global**; `buildRegistry` panics on duplicates. Pick `source_clone`
   deliberately and add it in its own sub-package following the `services`/`fs` shape.
10. **Docs are mirrored** (`docs/i18n/ru/`), and `make build` resyncs the embedded tree.

## Development Approach

- **testing approach**: **regular** (code first, then tests) for all tasks here — these are
  execution-engine capabilities, not diagnostics. (Plan A carries the TDD-for-validators
  rule; it does not apply to this plan.) **One exception**: the three load-time rejections
  in Task 3 (`auto` without `when:`, with `builtin`-`when:`, with `template`-`when:`) and
  the injection regression in Task 2 are the highest-value artefacts of this plan — write
  those tests first.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility — every existing workspace must keep working untouched

## Testing Strategy

- **unit tests**: required for every task
- **e2e tests**: no UI suite in this project. `dwe test` scenarios are the closest
  analogue; Task 6 must confirm `make test` stays green across `usercommands`, `pipeline`
  and `builtin` packages.
- table-driven tests for schema validation and argv assembly
- for the builtin, follow the established per-package test shape in
  `internal/core/execution/builtin/services/`

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**`argv_append_from`** — a new `CommandDef` field holding a host shell expression whose
stdout lines are appended to `Argv` as individual elements. This lets a container command
receive a host-computed file list without the author rebuilding `docker compose exec` by
hand. Chosen over a `stdin_from:` variant because it keeps argv as argv (no shell quoting
inside the container, no dependency on `xargs` being present in every image).

**`check: auto`** — an explicit opt-in that resolves to the logical inverse of the step's
`when:`. Explicit rather than implicit: `when:` ("should this run now") and `check:`
("is this already done") coincide in 21 of 26 observed steps but are not the same
question, so inferring silently would change the meaning of the 5 steps that have `when:`
and deliberately no `check:`.

**`source_clone`** — a `KindAction` builtin taking `repo`, `dir` and optional `branch`,
with the idempotency gate built in (destination already a git checkout → skip with a
message). Replaces an 11-instance pattern *and* its surrounding `when:`/`check:` pair.

## Technical Details

**Field placement.** `ArgvAppendFrom string \`yaml:"argv_append_from"\`` goes next to
`Argv` in `CommandDef`. Validity: only for commands that declare `Argv` (appending to a
shell `cmd:` string would reintroduce exactly the injection surface commit `2a1d6b73`
closed). A command declaring `argv_append_from` with `cmd:` is a load-time error.

**Ordering with `${args}`.** Final argv = declared `Argv` (with `${args}` expanded in
place) followed by the `argv_append_from` items. Documented and pinned by test.

**Empty-append semantics.** Empty stdout → the step/command is **skipped** with a message
("nothing to process"), exit 0. Rationale in constraint 4. Hard-coding this is a one-way
door only in appearance: adding `on_empty: skip|run|fail` later is purely additive, so the
decision does not need re-litigating at implementation review.

**Inversion — scoped to `when: {type: shell}` only.** Three load-time errors, each with a
reason the message must state:

- `check: auto` **without** `when:` — nothing to invert.
- `check: auto` with `when: {type: builtin}` — the two registries are disjoint (see
  Context), so there is no `*config.Action` that expresses "NOT `dir-empty foo`", and
  `resolve.go:139` would reject the name against `CtxPredicate` anyway.
- `check: auto` with `when: {type: template}` — **this one silently produces an
  always-failing step**. `resolveStepWhen` (`resolve.go:281-297`) evaluates template
  conditions at *plan* time and drops the step when false, so any step that survives to
  execution had `when == true`; its inverse is therefore always false and the check always
  fails.

Both new checks belong next to the existing shape validation in `validateStepShape`
(`internal/core/project/config/workspace.go:3196-3205`), which already sees `step.Check`
and `step.When`.

**Inversion form** (must be chosen and pinned, since the two candidates are not
equivalent):

- `{type: shell, cmd: "! ( … )"}` runs through `execShellAction` with **user-overridable**
  `config.ShellBin` and `cmd.Dir = actx.WorkDir`. `docs/reference/config/deploy/steps.md:66`
  explicitly recommends *against* this form for conditions, because `cmd: shell` exists so
  they evaluate "regardless of the project's `ShellBin` setting" — a project on fish breaks.
- `{type: builtin, cmd: shell, with: {cmd: "! ( … )"}}` gets a hard `sh` (matching
  `condition.EvalCmd`), but `Shell.Run` (`builtin/shell.go`) **does not set `c.Dir`** and
  runs in the process CWD, while `condition.EvalCmd` sets `cmd.Dir = projectRoot`. Running
  `dwe deploy` from a subdirectory would then evaluate different relative paths than the
  `when:` it was derived from. It also inherits the default 10s timeout, which `when:` has
  no equivalent of.

Pick the builtin form and fix `Shell.Run` to honour `ectx.ProjectRoot` as a prerequisite
step; pin cwd, shell and timeout by test.

**Textual wrapping is not free.** `! ( … )` breaks on a trailing comment
(`test -e x  # cloned?` → the closing paren is swallowed → syntax error, not inversion),
on a trailing `&`, and on an unterminated heredoc. Mitigate by wrapping across newlines
(`! (\n<cmd>\n)`) and test the comment case explicitly.

**What is *not* a problem** (verified, worth recording so it is not re-litigated): a `when:`
that errors rather than returning false never reaches the check — when `when == false` the
step is skipped and `check:` is not executed at all (`executor.go:733-746`), and
`condition.EvalCmd` treats any non-zero exit (including 127) as `false`, reserving errors
for "could not start". The plan's reasoning that inversion is a logical negation, and that
inferring `check:` without opt-in would be unsafe, holds.

**`source_clone` contract.**
`with: {repo, dir, branch?, depth?}` — `repo` and `dir` required; `dir` resolved relative
to the project root and validated by `pathsafe`. Behaviour: destination contains `.git` →
skip (message, success); destination absent or empty → clone; destination non-empty and not
a git checkout → error. `depth:` included only if it costs nothing; drop it under YAGNI if
it complicates the first implementation.

## What Goes Where

- **Implementation Steps** (`[ ]`): schema, runtime, builtin, tests, docs in this repo.
- **Post-Completion** (no checkboxes): migrating the five real workspaces onto the new
  primitives — those live outside this repo and are the owner's call.

## Implementation Steps

### Task 1: `argv_append_from` — schema and load-time validation

**Files:**
- Modify: `internal/core/usercommands/model/types.go` (both the struct **and**
  `allowedFieldsFor` at line 121 — the allowlist lives in this same file; there is no
  separate validator for command-file keys)
- Modify: `internal/core/usercommands/model/types_test.go`
- Modify: `internal/cli/command/inspect.go` (+ its tests)

- [ ] add `ArgvAppendFrom string` with tag `argv_append_from` next to `Argv`
- [ ] reject at load time: `argv_append_from` together with `cmd:` (argv-only feature),
      and `argv_append_from` on command types that do not build an argv
- [ ] extend `allowedFieldsFor` **per type** — it is type-scoped, so add the key to
      `CommandTypeShell`, `CommandTypeServiceExec` and `CommandTypeServiceRun`
- [ ] decide `CommandTypeDaemon` explicitly: it is the fourth type accepting `argv`
      (`types.go:190`), so the "any command with argv" rule silently includes it.
      Recommendation is to exclude it — a daemon expands into four synthetic commands and
      "skip on empty list" there means "silently do not start the daemon"
- [ ] surface the field in `dwe cmd -i`: the typed JSON struct (`inspect.go:29`, filled at
      :134/:144) and the human output (:301-302 / :332-333). An executable field invisible
      to inspect is worse than usual here — inspect is the documented way for an agent to
      learn what a command does before running it
- [ ] write table-driven tests for accept/reject combinations
      (`argv` + append → ok; `cmd` + append → error; append alone → error; daemon → per the
      decision above)
- [ ] run tests — must pass before task 2

### Task 2: `argv_append_from` — host execution and argv assembly

**Files:**
- Modify: `internal/core/usercommands/runtime/internal/runio/args.go`
- Modify: `internal/core/usercommands/runtime/runners/service/exec.go` (line 218)
- Modify: `internal/core/usercommands/runtime/runners/host/host.go` (line 41 — the **second**
  caller of `RenderArgvWithArgs`, for `type: shell` with `argv:`; `runners/service/run.go:23`
  reuses `buildServiceArgv` and needs no separate edit)
- Modify: corresponding `*_test.go`

- [ ] render the expression through `runio.withoutArgs(rc)` and reject a literal `${args}`
      at load time (constraint 3a — do this **before** wiring execution, so the unsafe
      shape never exists even transiently)
- [ ] execute the expression on the host via `config.ShellBin` with the run context's
      cancellation, capturing stdout only (stderr streams to the user)
- [ ] split stdout into argv elements one per line, ignoring a trailing newline; treat
      output bytes as data, never re-parse as shell
- [ ] append after the declared `Argv` (with `${args}` already expanded in place)
- [ ] name the skip mechanism explicitly (sentinel error, distinct runner outcome, or early
      return) and define what it does to `messages.success`, `notify:`, the pipeline
      reporter's Skip-vs-Finish accounting and `--output json` — "skip with a message" is
      not yet a mechanism
- [ ] write tests: multi-line output → separate argv elements; paths containing spaces stay
      single elements; empty output → skip, exit 0; expression failure → command fails with
      the expression's stderr surfaced
- [ ] write the injection regression test from constraint 3a
      (`dwe cmd x -- '$(touch /tmp/pwned)'` must not execute the substitution)
- [ ] write a test pinning `${args}` + `argv_append_from` ordering
- [ ] run tests — must pass before task 3

### Task 3: `check: auto` — schema and inversion

**Files:**
- Modify: `internal/core/project/config/action.go` (lines 26-29 currently reject a scalar
  outright: "action must be a mapping … not a scalar string". `*Action` is used **only**
  for `DeployStep.Check` — `workspace.go:423` — so the relaxation is well contained)
- Modify: `internal/core/project/config/workspace.go` (`validateStepShape`, 3196-3205)
- Modify: `internal/core/execution/builtin/shell.go` (honour `ectx.ProjectRoot` — see
  Technical Details; prerequisite for the chosen inversion form)
- Modify: `internal/core/execution/pipeline/resolve.go`
- Modify: `internal/core/execution/pipeline/resolve_test.go`

- [ ] accept the scalar form `check: auto` alongside the existing mapping form, decoding it
      into a **sentinel Action at load time** so `step.Check != nil` holds everywhere
      (`StepForcesRun` at `forcesrun.go:37`, `deployStepToMap` at `hash.go:416`, and the
      `dwe reset step` path all inspect the raw config)
- [ ] reject at load time with a reason in the message: `auto` without `when:`; `auto` with
      `when: {type: builtin}` (disjoint registries); `auto` with `when: {type: template}`
      (would always fail — see Technical Details)
- [ ] rewrite the sentinel into a real `*config.Action` at resolve time using the chosen
      inversion form, wrapping across newlines rather than inline
- [ ] write tests: shell inversion works; a `when.cmd` with a **trailing comment** inverts
      correctly (the naive inline wrap turns this into a syntax error); the three load-time
      rejections each fire with their own message
- [ ] write tests pinning cwd, shell binary and timeout of the derived check against the
      `when:` it came from
- [ ] run tests — must pass before task 4

### Task 4: `check: auto` — journal and force-run integration

**Files:**
- Modify: `internal/cli/lifecycle/reset.go` (lines 715-722)
- Modify: `internal/core/execution/pipeline/forcesrun_test.go`
- Modify: `internal/core/workflow/deploy/journal/hash_test.go`
- Modify: `internal/cli/deploy/deploy.go` tests as needed

- [ ] handle `dwe reset step`: it takes a `config.DeployStep` straight from config and calls
      `pipeline.ExecAction(ctx, *step.Check, actx)` **bypassing `ResolvePhaseSteps` and
      never evaluating `when:`** — so an unrewritten sentinel would reach it. Either resolve
      it there too, or refuse with a message saying there is no `when:` on this path to
      invert
- [ ] verify `StepForcesRun` returns true for a step whose check came from `auto` (it does,
      given the load-time sentinel) and add the test that pins it
- [ ] verify the `hasCheck → Run` path in the skip decider treats it identically
- [ ] pin two useful invariants with tests: `journal.StepHash` (`hash.go:36-53`) hashes only
      action + files_gate and **not** `Check`, so migrating a workspace from an explicit
      inverse check to `check: auto` does not shift step hashes; and `deployStepToMap`
      (`hash.go:395-422`) reads the **raw** config where `auto` stays `auto`, so it is
      stable
- [ ] write a test covering an auto-check step inside a `parallel:` group (one level)
- [ ] write a regression test that a step with `when:` and **no** `check:` still does not
      force a run (the 5 observed steps that rely on this)
- [ ] run tests — must pass before task 5

### Task 5: `source_clone` builtin

**Files:**
- Create: `internal/core/execution/builtin/source/source.go`
- Create: `internal/core/execution/builtin/source/clone.go`
- Create: `internal/core/execution/builtin/source/clone_test.go`
- Modify: `internal/core/execution/builtin/builtin.go`
- Modify: `internal/core/execution/builtin/builtin_test.go` (line 336 asserts
  `len(registry) == len(allBuiltinNames)` plus a per-name kind table — the suite goes red
  without it)

- [ ] create the sub-package exposing `Builtins()` following the `services`/`fs` shape and
      register it in `buildRegistry()` as `KindAction`
- [ ] implement `with: {repo, dir, branch?}`: required-field validation, destination
      resolved against the project root and checked with `pathsafe` — specifically
      `ContainedRel` + `CheckNoSymlinks` (and `EnsureRealUnder` if needed), the pattern
      already used in `execution/templates/config/config.go:255-279`
- [ ] resolve the git binary via the nil-safe accessor `config.GitBin(...)` — AGENTS.md
      forbids reading `cfg.Binaries.*` directly
- [ ] force a non-interactive posture (`GIT_TERMINAL_PROMPT=0`, and an `GIT_ASKPASS`
      decision): all five workspaces clone from a private host, and a credential prompt
      inside a deploy is a hang. In sequential mode the builtin is handed `os.Stdin`; in
      parallel it is nil, but git can still reach for `/dev/tty`
- [ ] implement the idempotency gate: `.git` present → skip with message (success), **including
      when the existing checkout is on a different branch** — record that as intended;
      absent/empty → clone; non-empty non-git → error naming the path
- [ ] write tests: fresh clone, re-run is a no-op, different-branch checkout is a no-op,
      non-empty non-git destination errors, path escaping the project root is rejected,
      missing required field is rejected
- [ ] write a test asserting the builtin is reachable from `CtxUserYAML` and rejected from
      `CtxPredicate` positions if that is the intended kind boundary
- [ ] run tests — must pass before task 6

### Task 6: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] rebuild the three observed copy-paste patterns using the new capabilities and confirm
      each shrinks: the clone step loses its `when:`/`check:` pair; a staged-lint command
      expresses its computed file list without rebuilding `docker compose exec`
- [ ] confirm every existing fixture and workspace-shaped test still passes untouched
      (backward compatibility is the acceptance bar here)
- [ ] run full test suite: `make test`
- [ ] run `make lint`
- [ ] verify test coverage meets project standard

### Task 7: [Final] Update documentation

- [ ] document `argv_append_from` in `docs/reference/config/commands/types.md` and
      `directives.md` (the argv/args field tables), including the empty-result semantics,
      its ordering relative to `${args}`, and the host-execution trust boundary from
      constraint 3b
- [ ] document `check: auto` in **both** condition pages —
      `docs/reference/config/conditions.md` and
      `docs/reference/config/deploy/conditions.md` — stating plainly that it applies only to
      `when: {type: shell}` and why the other two kinds are rejected
- [ ] document the `source_clone` builtin in `docs/reference/config/deploy/builtins.md`
- [ ] update `skills/dwe/references/authoring-commands.md` and
      `pipelines-and-orchestration.md`, which describe exactly these schemas (Plan C Task 7
      edits the skill for different content — these two need this plan's changes)
- [ ] update the Russian mirrors under `docs/i18n/ru/`
- [ ] run `make build` to resync embedded docs and content hashes
- [ ] update `AGENTS.md` Critical Patterns for the argv/injection boundary if warranted
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Workspace migration** (outside this repo, owner's call):
- replace the `source.clone` private command + gate with the `source_clone` builtin in the
  five workspaces (11 call sites); the private command and `workspace/commands/source.yml`
  can then be deleted
- collapse `when:`/`check:` pairs to `check: auto` where the check is genuinely the inverse
  **and the `when:` is `type: shell`** (23 of the 26 observed `when:` are shell, so most
  candidates qualify; the `dir-empty` ones do not — review each, since the coincidence is
  empirical, not definitional)
- rewrite `quality.staged` in the four projects that carry it once `argv_append_from` is
  available; the ~40 lines of bash should reduce to a declared command

**Known non-goals**:
- `archive_fetch` / `archive_unpack` stay recipes — see Overview for the reasoning
- the `check:`-accepts-predicates discoverability gap is Plan C, not new syntax here

# Service runner runtime defaults: workdir, container TTY, exec mode

## Overview

Three runtime defaults of the service command runners (`type: service_exec`,
`type: service_run`, `type: daemon`) are wrong or missing, and every project
compensates for them by hand in YAML:

1. **workdir has no fallback.** The runner reads only `cli.user` out of a
   service's `cli:` block. `dwe shell` already resolves
   `cli.workdir → work_dir_internal → dir_internal`; the command runner does
   not, so a command without an explicit `workdir:` lands in the image's
   `WORKDIR` while `dwe shell` into the same service lands somewhere else.
2. **the container TTY is not decided by the runner at all.** Whether the
   container process gets a terminal is a side effect of whoever happened to
   wire the runner's stdio. The runner never says what it wants.
3. **`mode:` defaults to `exec-or-fail`,** while 206 of the 224 service
   commands measured across nine local workspaces declare `exec-or-run` by
   hand.

The change gives the runner an explicit answer for all three. It is a
user-visible behaviour change with **no deployment-hash change** — a
`type: command` pipeline step keeps its old hash and will report
`already up-to-date`, so nothing re-runs on its own after the upgrade.

## Context (from discovery)

Files/components involved:

- `internal/core/usercommands/runtime/runners/service/exec.go` —
  `resolveServiceFields` (l.110), `lookupServiceCLIUser` (l.163),
  `buildDockerComposeCmd` (l.234), mode switch (l.41-71)
- `internal/core/usercommands/runtime/runners/service/run.go` — `RunRunner`,
  shares both helpers with `ExecRunner`
- `internal/core/usercommands/runtime/internal/runio/runio.go` —
  `colorForceActive` (l.66), `ColorForceEnv` (l.78), `StdoutOf`/`StderrOf`/
  `StdinOrOS` (l.102-124), `bridgedTTYActive` (l.194), `bridgedTTYChildIO`
  (l.212), `WireChildIO` (l.281), `stdoutIsTerminal` seam (l.59)
- `internal/core/usercommands/runtime/spec/runner.go` — the `RunContext` struct
- `internal/core/execution/pipeline/executor.go` — `childIO` (l.71-100),
  `execCommandAction` (l.273-315)
- `internal/core/usercommands/runtime/runners/workflow/step.go` — sub-step
  `RunContext` construction
- `internal/cli/command/runbyid.go` — the `dwe cmd <id>` entry point
- `internal/core/execution/builtin/containers/daemon_start.go` — a second,
  independent workdir/user resolution (l.49-62, l.117-136)
- `internal/cli/shell/exec.go` — `resolveShellOptions` (l.205-257), the workdir
  chain being mirrored; `ttyMode` helpers (l.19-109)
- `internal/core/project/config/workspace.go` — `ServiceCLIConfig` (l.1391-1410)
- `internal/core/usercommands/model/types.go` — `DefaultExecMode` (l.282),
  `UserModeInternal` (l.252)

Related patterns found:

- `user: internal` (`model.UserModeInternal`) is the existing precedent for
  "emit no flag AND skip the config fallback".
- `docker compose exec` only allocates a container TTY when **its own** streams
  are terminals — stated in the comment at `runio.go:204`.
- `pipeline.childIO` opens a real PTY for a sequential step when dwe's stdout is
  a terminal and hands it to the runner as `rc.Stdout`/`rc.Stderr`.

Dependencies identified:

- The two facts above compose into the finding that shaped the design: **the
  container gets a TTY inside `dwe deploy run` because the pipeline fabricated
  one**, not because the runner asked. A naive `isatty(rc.Stdout)` auto-detect
  inside the runner would be a near no-op. The predicate must be *"is this a
  top-level user invocation"*, not *"is there a terminal"*.
- `runio` lives under `runtime/internal/` and is **not** importable from
  `internal/core/execution/builtin/`. Anything the daemon builtin must share has
  to live in `internal/core/project/config`, a leaf both already import.
- The host bridge daemon force-sets `DWE_NONINTERACTIVE=1` on every forked
  `dwe`, so no TTY predicate may key off `NonInteractive`.

## Development Approach

- **testing approach**: Regular (code first, then tests) — the change is a set
  of small predicates inside existing well-covered functions, and the argv
  assertions are easiest to write against the real builder.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` after each change — never bare `go test ./...`, because
  `internal/core/docs/embedded/` is generated and gitignored
- backward compatibility, stated precisely — a command keeps byte-identical
  argv only if it declares an explicit `mode:`, an explicit `workdir:` **and**
  a TTY flag in its *effective* compose flags (`docker.yml`'s `args.exec` /
  `args.run` plus its own `compose_args`). A flag list carrying only
  unrelated flags (`--name`, `--rm`) does **not** protect against the `-T`
  injection, and saying otherwise would be false: that is the whole point of
  keeping detach and naming orthogonal to TTY detection

### Branch and commit layout

All work happens in one feature branch cut from `release/0.6.0`, landing as
**three commits in this order**:

| commit | content | tasks |
| --- | --- | --- |
| **A** | workdir fallback chain + daemon symmetry | 3–6 |
| **B** | container TTY auto-detect + paired colour forcing | 7–12 |
| **C** | `mode:` default flip + aggregate docs and CHANGELOG | 13–15 |

Commit B is the only one that changes behaviour for the majority of the
corpus, so it **must be revertable without untangling A or C**. Four
consequences bind the implementation:

- doc edits are split **by section** — A touches only the workdir sections,
  B only the `compose_args` / TTY sections, C only the mode-resolution
  section — so reverting B does not conflict with the already-landed C;
- `internal/core/docs/content_hashes_gen.go` is **git-tracked** (only
  `internal/core/docs/embedded/` is gitignored) and `make build` rewrites it.
  All three commits edit `docs/reference/config/commands/types.md`, and both B
  and C edit `docs/internals/packages.md` — **both** files are hashed, so the
  revert of B conflicts on two lines, not one. The conflict is mechanical, not
  semantic:
  the revert procedure is `git revert B`, then `make build` to regenerate the
  hash. `docs/i18n/ru/**` is **not** hashed
  (`scripts/gen-docs-content-hashes.sh` covers only `docs/reference`,
  `docs/guides`, `docs/internals` and `README.md`), so the RU mirrors add no
  conflict surface;
- **every B-specific invariant is documented in commit B, not commit C.** The
  `UserInvoked` contract and the paired colour-forcing rule go into
  `docs/internals/packages.md` as part of B; commit C's `packages.md` edit
  covers only the A and C contracts. Otherwise reverting B leaves the
  canonical internal guidance describing mechanisms that no longer exist, and
  no amount of regenerating `content_hashes_gen.go` removes stale prose. The
  same applies to any `AGENTS.md` pointer bullet;
- the single aggregate CHANGELOG entry is written in commit C. Reverting B
  therefore also requires editing that entry; this is called out in the entry
  itself so a future reverter does not miss it. A single aggregate entry is a
  deliberate choice — the user experiences one combined behaviour change — and
  it is what makes the revert a three-step procedure rather than one command;
- commit C must not write any test that hard-codes B's argv. See task 13.

⚠️ **REHEARSED, AND THE CLAIM BELOW IS TOO OPTIMISTIC.** A `git revert` of the
landed commit B produces **three** conflicts, not one:

- `internal/core/docs/content_hashes_gen.go` — mechanical, resolved by
  `make build`, as predicted;
- `internal/core/execution/pipeline/executor_test.go` — commits A, B and C all
  add tests to this file and the hunks are adjacent despite the placement rule;
- `internal/core/usercommands/runtime/runners/service/service_test.go` — same
  shape: A's workdir-chain table, B's `-T` argv table and C's mode tests sit
  next to each other.

Neither test conflict is semantically hard — the resolution is "keep A's and
C's tests, drop B's" — but the revert is a manual merge, not a three-step
recipe. The section-splitting discipline worked for the prose files
(`types.md`, `packages.md`, `AGENTS.md` all reverted cleanly); it does not
carry to test files, where new tests naturally cluster at the end.

If a one-command revert of B is a hard requirement, the fix is to give each
commit its OWN test file (e.g. `executor_tty_test.go`, `service_tty_test.go`)
rather than appending to the shared one. That was not foreseen when the plan
was written and is recorded here rather than acted on.

So the revert of B is: `git revert <B>` → `make build` → resolve two test-file
conflicts by dropping B's tests → drop the TTY clause from the CHANGELOG entry
→ `make test`. Task 18 rehearses exactly this on a
scratch branch, and checks the resulting `packages.md` for orphaned prose
rather than only checking that the build and tests are green.

**Plan-file bookkeeping.** This plan file is git-tracked, and tasks 1, 2, 16
and 17 write their measurements into it. Those updates are **not** part of
commits A, B or C — commit them separately as `docs:` chores so the three
behaviour commits stay clean revert units.

Conventional-commit subjects:

| commit | subject |
| --- | --- |
| A | `fix(commands): resolve service workdir through the cli/service chain` |
| B | `feat(commands)!: decide the container TTY in the service runner` |
| C | `feat(commands)!: default service_exec mode to exec-or-run` |

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: this project has no UI e2e suite. The equivalent end-to-end
  layer is the per-workspace scenario harness (`dwe test`) and a manual
  interactive matrix; both are explicit plan tasks (16, 17), not optional
  follow-ups.
- **structural coverage gap, stated up front**: the container-TTY behaviour is
  invisible to the scenario harness. A scenario is always non-interactive, so
  it observes `notty`/`pipe` both before and after the change. A green harness
  run gives false confidence exactly where the risk is highest, which is why
  the manual matrix in tasks 2 and 16 is mandatory.
- **known trap**: dwe exports `UID=1000` into the container while a macOS host
  shell's `id -u` is 501. Pin assertions against `${host.uid}`, never against
  the host shell.
- commands: `make test` (or `make test-race`) and `make lint`.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**Workdir (commit A).** One chain, first non-empty wins, shared by
`service_exec`, `service_run` and `daemon`:

1. `workdir: internal` (or `runner.workdir: internal`) → emit no `--workdir`
   flag **and** skip the fallback entirely; stop.
2. `runner.workdir_from` → `workdir_from` (existing "config beats literal"
   rule, unchanged)
3. `runner.workdir` → `workdir`
4. **new:** `services.<svc>.cli.workdir`
5. **new:** `services.<svc>.work_dir_internal`
6. **new:** `services.<svc>.dir_internal`
7. no `--workdir` flag — the image's `WORKDIR` applies

Steps 4–6 are exactly what `resolveShellOptions` already does, so `dwe shell`
and a service command finally agree on where they land.

The daemon path does **not** implement rung 2 today: `daemon_start.go:124`
reads `if workdir == "" && workdirFrom != ""`, i.e. the literal wins, the
inverse of the service runner. Its nil handling also differs — a `workdir_from`
dot-path resolving to nil is a hard error there and an empty string on the
service path. `docs/reference/config/commands/types.md:702,708` already claims
the service-runner precedence applies to daemons, so the docs are false today.
Commit A fixes the code rather than re-publishing the false claim in a new
shape.

**Container TTY (commit B).** A new `spec.RunContext.UserInvoked bool` carries
the one fact the runner cannot otherwise recover: whether the user launched
this command themselves, or one runtime invoked another. The zero value
`false` means "suppress the TTY", so forgetting to set it at a future entry
point can only ever be conservative. A single predicate,
`runio.WantContainerTTY`, turns that into an argv decision, and
`buildDockerComposeCmd` appends `-T` when the answer is no.

**Exec mode (commit C).** `model.DefaultExecMode` flips to `ExecModeExecOrRun`.

### Key design decisions and rationale

- **A new field rather than reusing `SkipNotify`.** `SkipNotify` is already
  documented as "always set true when one runtime invokes another" and would
  answer the question correctly today. It is not reused because notifications
  and terminal allocation are unrelated concerns: the next caller that flips
  `SkipNotify` for a notification reason would silently move the TTY with it.
- **`false` is the safe zero value.** Every internal caller that builds a
  `RunContext` gets "no TTY" without doing anything, which is the behaviour a
  pipeline, a `check:`, a `files_gate` probe and a validation check all want.
- **The predicate is not `isatty`.** See Context — the pipeline fabricates a
  PTY, so a terminal probe answers "yes" precisely where the change is
  supposed to bite.
- **The bridge arm short-circuits the terminal probe.** `bridgedTTYChildIO`
  fabricates its PTY inside `WireChildIO`, i.e. *after* `BuildCommand` has
  already decided the argv, so `isTerminal(rc.Stdout)` lies on the bridge.
- **Only TTY flags in `compose_args` suppress the auto-detect.** `-d`,
  `--name`, `--rm` stay orthogonal; making any explicit flag disable TTY
  detection would couple unrelated knobs.
- **No new `tty:` schema field.** There is no dedicated knob for forcing a
  container TTY inside a pipeline; the deliberately awkward
  `compose_args: ["--no-tty=false"]` is the way to ask, and no command in the
  measured corpus wants it.
- **Colour forcing is a mandatory paired change, not a follow-up.** Suppressing
  the TTY in a deploy would otherwise turn every command's output grey.

### Blast radius (re-measured in task 1; nine local workspaces, 238 service commands)

Measured by parsing every `workspace/commands/**/*.yml` and
`workspace/services/*/service.yml` and applying the planned classifiers. The
corpus is 238 `service_exec`/`service_run` commands — larger than the 224 of the
first sweep, which did not reach the per-service command files under
`workspace/commands/services/`.

- **TTY**: **124 of 238 (52%)** move from a container TTY to `-T`. The
  effective-flag recount changed the method but not the shape of the number:
  **no** command in the corpus has a non-empty `compose_args` without a TTY flag
  (so the "no `compose_args` at all" proxy happened to be exact), and **no**
  workspace sets `docker.yml` `args.exec` / `args.run` at all. The refinement
  stays in the implementation because it is the correct rule, but it buys
  nothing on today's corpus. The remaining 114 all carry `compose_args: ["-T"]`.
- **workdir**: 31 commands have no explicit `workdir` / `workdir_from`. 21 are
  unaffected (`type: infra` services, or a service with no
  `workspace/services/` folder, or a templated `service: ${param.service}` that
  resolves only at runtime — cueBreaker's `execcontract.assert`). **10 change
  cwd**, and 4 of those are the deliberate probe commands added while taking
  the baseline (`probe.*` / `runner-probe.cwd` in AlbFetcharr, alto, beetDeck,
  ficbird) — they exist to observe exactly this. The 6 user-facing ones are the
  laravel and magento log commands, which survive only because their paths are
  absolute. Unchanged from the first sweep.
- **mode**: 7 of 238 declare no `mode:` and take the new default.
- **daemons**: **exactly one** in the entire corpus
  (`laravel:workspace/commands/services/main.yml:queue`), and it declares both
  `workdir` and `user` explicitly. Neither its cwd nor its uid moves. **Task 1's
  stop condition is satisfied**: no file-writing daemon changes uid, so the
  `cli.user` half of task 5 lands as planned.
- **wrapper commands** (a host-side command re-entering `dwe cmd <id>`): 7
  call sites, of which **2 matter**. `magento:varnish.enable` and
  `varnish.disable` (`type: shell`) invoke `services.magento.config.set` and
  `services.magento.cache.flush`, both `service_exec` with no `compose_args` —
  so a user typing the outer command at a terminal now gets `-T` on the inner
  one. Neither inner command is interactive, so the practical impact is nil,
  but this is the pattern to watch. The other five are inert:
  AlbFetcharr's `library.restore` targets two `type: shell` commands, and
  ficbird's `docs.sync-openapi` targets `admin.gen-api`, which already declares
  `compose_args: ["-T"]`.

`dwe deploy run` is not the only caller that loses the container TTY. Four
other sites build a `RunContext`, and three of them run under a real terminal
today, so a container command they invoke gets a TTY today:

| site | what it is |
| --- | --- |
| `internal/core/workflow/snapshot/exec.go:115` | snapshot workflow steps |
| `internal/cli/lifecycle/reset.go:633` | `on_disable.before` reset hooks |
| `internal/cli/service/service_plan.go:527` | `dwe services enable/disable` hooks |
| `internal/core/validate/checks/loader.go:212` | `type: command` preflight checks |

All four are **left at the zero value on purpose**: none of them is a command
the user typed, and the fourth is a probe whose output is parsed. The snapshot
case is the most user-facing of the four and gets its own cell in the manual
matrix (tasks 2 and 16).

Note that `internal/cli/lifecycle/run.go` and `restart.go` also mention a
`RunContext`, but it is `lifecyclepkg.RunContext` — an unrelated type. They are
not call sites for this change.

Redundant `mode:` and `compose_args: ["-T"]` lines are **not** cleaned up in
this work. That is a separate later pass, and `compose_args: ["-T"]` must be
swept last and only where a scenario covers the command — losing a TTY on an
interactive bridged command is a silent failure.

## Technical Details

**New helpers** in `internal/core/project/config`:

```go
// matches on the Container field, NOT the services map key, so
// `service: app-main` finds the service whose folder is `main`.
// Iterates sorted keys: cfg.Services is a map, and two services may
// legally share a Container value, so first-match-wins must be stable.
func ServiceByContainer(cfg *DweConfig, container string) (ServiceConfig, bool)

// cli.workdir -> work_dir_internal -> dir_internal
func ContainerWorkdirFallback(cfg *DweConfig, container string) string
```

The sorted iteration is not a style preference: the result now feeds a
`--workdir` argv rather than only a `--user` flag, and randomized
`cfg.Services` iteration is already a named flaky-golden trap in this
repository.

`lookupServiceCLIUser` in `exec.go` collapses onto `ServiceByContainer`; its
duplicated loop over `cfg.Services` goes away.

**New field** on `spec.RunContext`:

```go
// UserInvoked marks an invocation the user launched themselves, as opposed
// to one runtime invoking another. It gates container TTY allocation only.
// The zero value (false) suppresses the TTY, so a new entry point that
// forgets to set it can only be conservative.
UserInvoked bool
```

**A process boundary the field cannot cross by itself.** A `type: dwe` step
starts a *fresh dwe process*: `pipeline/executor.go:160-170` (`buildDweCmd`,
which forwards `os.Environ()` plus `CLICOLOR_FORCE=1`, and `DWE_NONINTERACTIVE`
only when `skipConfirm`) and the user-command `DweRunner` in
`runners/host/dwe.go:30-59` do the same. Neither carries `RunContext`
provenance. If that subprocess runs `dwe cmd <id>`, an unconditional
assignment in `runCommandByID` marks it `UserInvoked` — and in a sequential
pipeline that child inherits `childIO`'s fabricated PTY, so `WantContainerTTY`
answers *yes* and B silently fails to suppress the TTY on a supported path.

The same hole exists through a **documented** contract: `type: shell` and
`type: script` deliberately export `DWE_BIN` into the child environment
(`runners/host/host.go:119`, `runners/script/script.go:149`) precisely so
project code can call dwe again. A pipeline-invoked shell command running
`"$DWE_BIN" cmd <id>` re-enters with no provenance either.

Enumerating the spawn sites does not converge. `execShellAction`
(`executor.go:216-220`) never assigns `cmd.Env` at all — the child inherits
`os.Environ()` verbatim. Others build one but start from `os.Environ()`
(`envtest/runner.go:225`, `buildDweCmd` at `executor.go:165`,
`host/dwe.go:44`, `host/host.go:81`, `script/script.go:195`), so a
process-global marker reaches all of them; the point is that the *set* of
mechanisms is not enumerable reliably. A `type: shell` step is at least as common as a
`type: dwe` one, and `type: host` / `type: script` reach the same place. Any
list of sites is a list we will be wrong about again.

So the marker is **process-global, not per-spawn**: `DWE_NESTED_RUNTIME=1` set
once via `os.Setenv` by the process that is about to run a pipeline or dispatch
a command runtime, after which every descendant inherits it regardless of how
it was spawned. Two read/write points:

- `runCommandByID` — **read first**, `UserInvoked = !markerSet`, then set the
  marker so anything this command spawns is nested;
- `pipeline.ExecAction` (`executor.go:177`) — set the marker unconditionally on
  entry. **Not** `Run` (`executor.go:388`), the deprecated wrapper no
  production caller uses. `RunWithOptions` (`executor.go:446`) is where the
  five pipeline callers land (`deploy.go:838`, `lifecycle/reset.go:251,487`,
  `workflow/lifecycle/phases.go:75`, `envtest/runner.go:747`), but
  `ExecAction` is strictly wider: `dwe reset step` calls it directly
  (`lifecycle/reset.go:744,761`), bypassing `RunWithOptions` entirely. Placing
  the marker on the narrow one leaves that path unmarked; placing it on
  `ExecAction` costs nothing, because `execCommandAction` sets `UserInvoked`
  false explicitly anyway.

Three constraints on it:

- the host bridge must **not** set it — a bridged `dwe cmd` *is* a user
  invocation, and that is the whole reason `bridgedTTYChildIO` exists;
- it must join the daemon's strip set (`bridgeclient.StripEnv`) so a marker set
  inside a container cannot leak across the trust boundary and silently kill
  the TTY on every bridged command;
- the runners that *do* build an explicit `cmd.Env` must not drop it. `DweRunner`
  assigns `cmd.Env` only when the rendered env or colour vector is non-empty
  (`host/dwe.go:44-56`), and `host.go:81` has the same shape — but since both
  start from `os.Environ()`, a process-global marker survives them unchanged.
  Verify that rather than assume it; the failure mode is a runner that builds
  its environment from scratch instead.

Set to `true` in exactly **two** files, and nowhere else:

- `internal/cli/command/runbyid.go` — `runCommandByID` documents itself as
  "the single execution path for both `dwe commands <id>` and the TUI run
  flow", so those are one site, not two. The host bridge is **also** this same
  site: `internal/core/bridge/exec.go:56` re-execs `dwe <argv…>` as a plain
  subprocess, which lands right back here, and so does a `type: dwe` step —
  which is why the assignment is `!nestedMarkerSet` rather than a bare `true`.
  The `DWE_NONINTERACTIVE` warning belongs as a comment *in this file*: the
  daemon force-sets `DWE_NONINTERACTIVE=1` on every forked `dwe`, so the
  predicate must never key off `NonInteractive`.
- `runners/workflow/step.go` — **inherits** the parent's value for sequential
  sub-steps, and yields `false` inside a `parallel:` block. Implement this as
  `rc.UserInvoked && !rc.UnderParallel` at the read site rather than stamping a
  second field in `parallel.go:199`, so there is one place to read and one
  place to reason about.

`pipeline/executor.go`'s `execCommandAction` never sets it. That omission *is*
the intended behaviour change.

**The predicate**, in `runio` so both runners share one copy:

```
WantContainerTTY(rc) =
    rc.UserInvoked &&
    ( bridgedTTYActive(rc) || isTerminal(StdoutOf(rc)) && isTerminal(StdinOrOS(rc)) )
```

Two implementation constraints on that line. `RunContext.Stdout` is an
`io.Writer` and `Stdin` an `io.Reader`, both nil-able — the defaults come from
`StdoutOf` / `StdinOrOS` — so probing the raw fields returns `false` for the
top-level `dwe cmd` case whenever a caller leaves them nil, which is the one
case the feature exists for. And "is a terminal" needs an `*os.File` type
assertion plus `Fd()`; any other writer is not a terminal.

**Injection point**: `buildDockerComposeCmd`, immediately after `composeArgs`
are appended — if `!WantContainerTTY(rc)` and the effective flag vector carries
no TTY flag (defined below), append `-T`. `docker_daemon_start` is not touched
by this: it is `run -d`, detached.

**The classifier reads the *effective* flag vector, not `compose_args`.**
`buildDockerComposeCmd` appends `compose.CommandArgs["exec"]` /
`["run"]` — sourced from `docker.yml`'s `args:` block — **before** the rendered
`compose_args` (`exec.go:257-270`). A project that puts `--no-tty=false` or
`-d` there is invisible to a classifier that only inspects `compose_args`, and
would get a contradicting `-T` appended after its explicit request. Both the
TTY classifier and the `!detached` colour guard therefore run over
`CommandArgs[subcommand] + composeArgs`.

"Carries a TTY flag" needs a real classifier, not a literal comparison. The
compose flag is `-T, --no-tty` — **lowercase** on both `exec` and `run`
(verified against the installed Docker Compose), and pflag is case-sensitive,
so a `--no-TTY` matcher would recognise nothing that any project can actually
have written. Both spellings are boolean, so all of these are valid forms:
`-T`, `-T=false`, `--no-tty`, `--no-tty=false`, `--no-tty=true`. The classifier
matches: exact `-T`, any `-T=<value>`, and any argument whose name part is
`--no-tty` (compared case-insensitively, so a project that wrote `--no-TTY` and
is already broken at least does not also get a duplicate flag).

**Any occurrence hands control to the author, whatever its value.** A
`--no-tty=false` inside a pipeline therefore *does* force a container TTY. That
is a deliberate escape hatch, not an oversight — but it means the docs must not
claim forcing a TTY inside a pipeline is impossible. They must say instead:
there is no dedicated schema field for it, and `compose_args: ["--no-tty=false"]`
is the explicit, deliberately awkward way to ask.

**Paired colour change**: `colorForceActive` gains a third disjunct.

```
UnderParallel
  || (ColorForced && !stdoutIsTerminal)
  || (ttySuppressed && isTerminal(rc.Stdout))
```

The third term fires for a sequential deploy step, where `rc.Stdout` is
`childIO`'s fabricated PTY, and deliberately does **not** fire for
`dwe cmd foo | grep`, where `rc.Stdout` is a pipe — so piped output stays
uncoloured and parseable.

It must also not fire for a **detached** child. `-d` / `--detach` is valid on
both `compose exec` and `compose run`, and the design deliberately keeps detach
orthogonal to TTY detection — so an internal invocation with
`compose_args: ["-d"]` would get `-T` *and*, under the naive third term,
`CLICOLOR_FORCE` / `FORCE_COLOR` / `COLORTERM` injected at `exec.go:289-305`,
even though its output never reaches `rc.Stdout` at all. A colour-aware
detached process would then write ANSI escapes into the Docker logs forever.
The third term therefore reads "TTY suppressed **and not detached** and
`rc.Stdout` is a terminal".

`colorForceActive` takes the suppression decision as an argument rather than
re-deriving it, which changes the signature of the exported
`runio.ColorForceEnv`. That function has **four** production callers, three of
them host-side, so all four are part of commit B (see task 9).

The existing `runio.stdoutIsTerminal` seam is *joined by* a "is this writer a
terminal" probe — it is **not** replaced. Its two existing callers,
`colorForceActive` (l.67) and `bridgedTTYActive` (l.197), both mean the
process's own `os.Stdout`, not `rc.Stdout`; `bridgedTTYActive` in particular is
load-bearing for the bridge shape. Repointing either at `rc.Stdout` silently
changes bridge behaviour.

**Mode default**: `model.DefaultExecMode = ExecModeExecOrRun` (`types.go:282`).
The single application site is `exec.go:41-43`.

The observable difference is confined to **one** state: the probe succeeds and
reports the container stopped. Then `exec-or-fail` returns a clean dwe error
and `exec-or-run` warns and runs an ephemeral `run --rm`.

There is **no** difference on a probe *error*, and the plan must not claim one.
`exec-or-fail` only errors when `checkErr == nil && !running` (`exec.go:57-61`),
so a failed probe falls through to `exec`; `exec-or-run` sets `running = true`
on a probe error (`exec.go:63-67`) and also ends at `exec`. An unreachable
Docker daemon already surfaces a raw compose failure under both modes today.

**The flip reaches pipeline `check:` actions.** A step's `check:` is a full
`config.Action` dispatched through the same `ExecAction` switch
(`executor.go:918-940` → `executor.go:177-190`), with no restriction on the
referenced command's type. So a `type: command` check pointing at a
`service_exec` command that omits `mode:` changes from "fail when the service
is stopped" to "spin up an ephemeral container, and possibly pass". A check is
a postcondition and is supposed to be side-effect-free; this makes one class of
check side-effecting. Commit A independently changes the same check command's
cwd. Both need an inventory and a test at the executor level, not only at the
runner level — see tasks 13 and 4.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs, and the four
  measurement and verification passes that can be run from this machine
  (tasks 1, 2, 16 and 17).
- **Post-Completion** (no checkboxes): the forced redeploy across workspaces
  and the upgrade-guide paragraph, both of which belong to other work.

## Implementation Steps

### Task 1: Re-measure the blast radius across local workspaces

The service command counts are known; daemons were never counted, and commit A
changes their `workdir` and `user` resolution. This must be known before the
code lands, not after.

**Files:**
- Modify: `docs/plans/20260901-service-exec-runtime-defaults.md` (record the
  numbers in the Blast radius section)

- [x] recount the TTY blast radius by **effective flags**, not by "has a
      `compose_args` list": a command moves when neither its `compose_args` nor
      its service's `docker.yml` `args.exec` / `args.run` carries a TTY flag.
      The current figure of 119/224 counts only commands with no `compose_args`
      at all and is a lower bound
- [x] count the **wrapper** case the blast radius does not yet name: a
      `type: dwe` or `type: shell` command whose text invokes
      `dwe cmd|commands <id>` against a `service_exec` command. Under the
      process-global marker, a user typing the *outer* command at a terminal now
      gets `-T` on the inner one — correct by the design's own rules, and
      exactly how an interactive wrapper (`db.cli` → `db.psql`) breaks. If any
      exist, add one as a sixth cell of the manual TTY matrix in tasks 2 and 16
- [x] enumerate every `type: daemon` command across the local workspaces
- [x] for each, record whether it declares `workdir` / `workdir_from` / `user`
- [x] for those declaring none, record whether the target service has
      `cli.workdir`, `work_dir_internal` or `dir_internal` set — those are the
      ones whose cwd changes
- [x] separately record daemons whose target service has `cli.user` set —
      those change the uid they run as, which changes the ownership of files
      they write
- [x] write the counts into the Blast radius section of this plan
- [x] apply the stop condition: if any daemon that **writes files** would
      change uid under the new `cli.user` fallback, do not land that half
      blind — either exclude the `cli.user` fallback from the daemon path in
      commit A (keeping only the workdir chain) or fix the affected service's
      `cli.user`. Record the decision here with a ⚠️ note naming the daemon.
      A changed uid changes the ownership of everything the daemon writes, and
      that is not recoverable by re-running anything.

**Result.** The corpus holds exactly one daemon,
`laravel:workspace/commands/services/main.yml:queue`, and it declares both
`workdir` and `user` explicitly — so neither its cwd nor its uid moves. No
daemon writes files under a changing uid. **Stop condition clear: task 5 lands
both halves as planned.** Full numbers are in the Blast radius section.

### Task 2: Capture the "before" snapshot of the container TTY matrix

Commit B is invisible to the scenario harness, so the only evidence that it did
the right thing is a before/after comparison taken by hand. The "before" half
must be captured while the tree is still clean.

**Files:**
- Modify: `docs/plans/20260901-service-exec-runtime-defaults.md` (record the
  observations)

- [x] pick a live project and a command that needs a terminal (shell, REPL, or
      an interactive installer); if none exists, add a throwaway
      `type: service_exec` command whose `cmd:` prints `tty` and
      `ls -la /proc/self/fd/0 /proc/self/fd/1 /proc/self/fd/2`
- [x] cell 1 — run it as `dwe cmd <id>` from a real terminal; record the three
      fds and whether output is coloured
- [x] cell 2 — run it as a `type: command` step inside `dwe deploy run`; record
      the same
- [x] cell 3 — run it over the host bridge from inside a container; record the
      same
- [x] cell 4 — run `dwe cmd <id> | cat`; record the same
- [x] cell 5 — run it as a step of a snapshot workflow (`dwe snapshot …`) from
      a real terminal; record the same. This is the most user-facing of the
      four sites deliberately left at the zero value, and the only one likely
      to be noticed
- [x] paste the results into this plan under a "TTY matrix — before" heading
      — five cells, or six if task 1 found a wrapper command

**Harness.** Measured in a throwaway project (`/tmp/ttyprobe`: one alpine
service `box`, a `probe.tty` `service_exec` command declaring **no**
`compose_args`, a `wrap.tty` `type: shell` wrapper, a one-step snapshot
workflow, and a `type: command` deploy step). No developer project was touched.
The binary is `bin/dwe` built from this branch **before any code change**, so
the snapshot is the exact pre-change behaviour of the binary the change lands
in. A real terminal is simulated with `script -q /dev/null`, which allocates a
genuine PTY.

**TTY matrix — before** (`v0.5.0-31-gab5bc2bd`)

| # | invocation | container fds | `tty(1)` | colour forcing |
| --- | --- | --- | --- | --- |
| 1 | `dwe cmd probe.tty` at a terminal | `stdin=tty stdout=tty stderr=tty` | `/dev/pts/0` | none |
| 2 | `type: command` step in `dwe deploy run`, **host stdout piped** | `stdin=pipe stdout=pipe stderr=pipe` | `not a tty` | none |
| 2a | the same **at a real terminal** | `stdin=tty stdout=tty stderr=tty` | `/dev/pts/0` | **none** |
| 3 | bridged, `docker exec -it … dwe cmd probe.tty` | `stdin=tty stdout=tty stderr=tty` | `/dev/pts/1` | **`CLICOLOR_FORCE=1 FORCE_COLOR=1`** |
| 4 | `dwe cmd probe.tty \| cat` | `stdin=pipe stdout=pipe stderr=pipe` | `not a tty` | none |
| 5 | snapshot workflow step at a terminal | `stdin=tty stdout=tty stderr=tty` | `/dev/pts/0` | none |
| 6 | wrapper: `dwe cmd wrap.tty` at a terminal (`type: shell` re-entering `dwe cmd probe.tty`) | `stdin=tty stdout=tty stderr=tty` | `/dev/pts/0` | none |

Three things this pins that the design was only reasoning about:

- **Cell 4 confirms the finding the whole design rests on.** With dwe's own
  streams piped, the container already gets `pipe` on all three fds — compose
  degrades on its own. So today's container TTY inside a pipeline comes from
  `childIO`'s fabricated PTY, not from any decision the runner makes.
- **Cell 6 makes the wrapper regression concrete, not theoretical.** A
  `type: shell` command the user typed at a terminal today hands its nested
  `dwe cmd` a real `/dev/pts`. Under the process-global marker that becomes
  `-T`. An interactive wrapper (`db.cli` → `db.psql`) breaks here, and this is
  the row that will show it.
- **Cell 3 shows colour forcing is already active on the bridge** and cells 1,
  5, 6 show it is *not* active on a terminal — which is exactly the asymmetry
  the paired `colorForceActive` change has to preserve.

**Cell 2a settles the premise: it holds.** Inside `dwe deploy run` launched
from a real terminal, the container gets `/dev/pts/0` on all three fds today.
So the plan's TTY blast radius stands as measured — those 124 of 238 commands
really do move from a container TTY to `-T`, and commit B really is the
riskiest of the three items.

Two details in that row are worth carrying into the implementation:

- Cell 2 (the same step with dwe's stdout redirected) reads `pipe`. The
  difference between cell 2 and 2a is entirely `childIO`'s `stdoutIsTTY()` gate
  (`executor.go:81`), which confirms the fabricated PTY — not the runner — is
  what hands the container a terminal today.
- Cell 2a shows `CLICOLOR_FORCE=<unset> FORCE_COLOR=<unset>`. Colour is **not**
  forced on that path today, because the container has a real TTY and needs no
  help. The moment commit B appends `-T`, that child sees a pipe *and* gets no
  forcing — grey output in every deploy. This is the empirical case for the
  paired `colorForceActive` change being mandatory rather than a nicety.

**Harness reuse.** `/tmp/ttyprobe` is left in place for task 16's "after"
snapshot (stack stopped, bridge daemon stopped). Its composition is described
above, so it can be rebuilt from scratch if it is gone by then. The deploy gate
skips the pipeline once it is up to date — task 16 must pass `--force`, which is
how cell 2a was taken.

### Task 3: Add the shared service-lookup helpers to `project/config`

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/project/config/workspace_test.go`

- [x] add `ServiceByContainer(cfg *DweConfig, container string) (ServiceConfig, bool)`,
      matching on the `Container` field rather than the services map key,
      iterating **sorted** keys so a `Container` collision resolves stably, and
      returning `false` for a nil config or an empty container name
- [x] add `ContainerWorkdirFallback(cfg *DweConfig, container string) string`
      implementing `cli.workdir → work_dir_internal → dir_internal`
- [x] document on both helpers why the lookup is by `Container` and not by map
      key (`service: app-main` must resolve to the folder `main`)
- [x] write tests for `ServiceByContainer`: match by container, the
      container-differs-from-key case, **two services sharing one `Container`
      resolving to the same service on repeated runs**, no match, nil config,
      empty name
- [x] write tests for `ContainerWorkdirFallback`: each of the three rungs wins
      in turn, all three empty returns `""`, unknown container returns `""`
- [x] run `make test` — must pass before task 4

⚠️ `make lint` reports 19 pre-existing `modernize` findings (`errorsastype`,
`stringscut`, `reflecttypeassert`) in files untouched by this work — the linter
is newer than the code. Not fixed here; out of scope for this plan.

### Task 4: Extend `resolveServiceFields` with the workdir chain and the `internal` sentinel

**Files:**
- Modify: `internal/core/usercommands/runtime/runners/service/exec.go`
- Modify: `internal/core/usercommands/runtime/runners/service/service_test.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`

- [x] **first**, build the fake-docker harness this task and task 13 need in
      package `pipeline`. `execCommandAction` calls `usercommands.RunCommand`
      directly (`executor.go:314`) with no seam, and `runtime.TestSnapshotRC`
      only *observes* — execution continues into a real `docker compose`, which
      is why existing pipeline command-action tests dodge it with
      `type: shell`, `cmd: "true"` (`executor_notify_test.go:15-45`).
      **Mechanism: the PATH seam.** Write a stub named `docker` into a temp dir
      that appends its argv to a log file and exits 0, then
      `t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))`.
      `exec.Command` resolves via `LookPath` against the *process* env at
      construction, so the runner later overwriting `cmd.Env` with
      `docker.MergeEnv(...)` does not defeat it, and the existing
      struct-literal `&config.DweConfig{...}` fixtures work unchanged.
      `t.Setenv` forbids `t.Parallel` here, same as the marker tests in task 10.
      Do **not** try `cfg.Binaries["docker"]` — `DweConfig` has no such field;
      the override lives in the unexported `userConfig` and is only populated by
      `config.LoadConfig`, so a struct-literal fixture always resolves the real
      `docker`
- [x] note for scope: the repo already has this pattern via the heavier
      `.dwe/config` route (`installFakeDocker`,
      `internal/cli/lifecycle/reset_test.go:360`, and `logs/logs_test.go:85-94`)
      — read it before writing the stub, but no pipeline test loads config from
      disk today, so the PATH seam is the cheaper fit
- [x] mind `goleak.VerifyTestMain` in package `pipeline` (`main_test.go:10`):
      these stub-docker tests execute a real child through
      `childIO`/`WireChildIO`, so their cleanups must complete inside the test
      or the whole package goes red on a leaked PTY-copy goroutine
- [x] note also: the rung tests below need **no** stub. `service_test.go`
      never calls a runner's `.Run(` — every test asserts on `BuildCommand`'s
      `*exec.Cmd`. Only the two package-`pipeline` tests (this task's check-path
      test and task 13's executor test) actually execute a child
- [x] treat a rendered `workdir` (or `runner.workdir`) equal to `internal` as
      the opt-out sentinel: emit no `--workdir` flag and skip the fallback
      entirely, mirroring `model.UserModeInternal`
- [x] after the existing `workdir_from` → `workdir` resolution, fall back to
      `config.ContainerWorkdirFallback` when the result is still empty
- [x] replace `lookupServiceCLIUser`'s hand-rolled loop with
      `config.ServiceByContainer`, keeping the existing `cli.user` fallback
      behaviour byte-identical
- [x] update the `resolveServiceFields` doc comment to state the full
      seven-step chain in order
- [x] write a table test covering all seven rungs, including: the `internal`
      sentinel at both the top level and inside `runner:`, a service whose
      `Container` differs from its map key, and a service with
      `work_dir_internal` but **no** `cli.workdir` — a configuration that
      exists in no local workspace, which is why unit coverage is the only
      coverage this rung will ever get
- [x] write tests asserting `service_run` inherits the same chain through the
      shared helper
- [x] write a test **in package `pipeline`** (`executor_test.go`) covering a
      `service_exec` command reached as a `check:` action — a check goes through
      the same `ExecAction` switch, so the new cwd applies there too, and a
      check whose command uses a relative path silently changes meaning. It
      cannot live in `service_test.go`: that file is package `service`, and
      importing `pipeline` from it is an import cycle. Declare an explicit
      `mode: exec` on the fixture command so this test needs no container
      probe — the probe seam is commit C's problem (task 13), not commit A's.
      Drive it through the stub-docker harness above and assert on the recorded
      argv
- [x] run `make test` — must pass before task 5

### Task 5: Give `docker_daemon_start` the same workdir chain and `cli.user` fallback

The docs already promise that a daemon's `user` and `workdir` follow
`service_run` semantics. They do not today; this closes a documented-but-false
claim.

**Files:**
- Modify: `internal/core/execution/builtin/containers/daemon_start.go`
- Modify: `internal/core/execution/builtin/containers/daemon_test.go`

- [x] **first**, extract the resolution into a pure function —
      `resolveDaemonWorkdirUser(cfg, service, user, workdir, workdirFrom) (string, string, error)`
      feeding `startArgsInput`. Today it sits inline in `Run`
      (`daemon_start.go:117-137`), which shells out to docker, and
      `daemon_test.go` only ever exercises the pure `buildStartExtraArgs`.
      Without the extraction this task's two test checkboxes have no surface
- [x] flip the daemon's `workdir_from` vs `workdir` precedence at l.124 from
      `if workdir == "" && workdirFrom != ""` to the service runner's rule —
      `workdir_from` wins — and align the nil handling (a dot-path resolving to
      nil yields `""` and falls through, rather than hard-erroring). The docs
      at `types.md:702,708` already promise this; today's code contradicts them
- [x] apply the same `internal` sentinel and the same
      `config.ContainerWorkdirFallback` chain after that resolution
- [x] add the `cli.user` fallback via `config.ServiceByContainer`, matching the
      service runner's precedence exactly — unless task 1 recorded a
      file-writing daemon whose uid would move, in which case follow the
      decision recorded there
- [x] write tests for the workdir chain on the daemon path, including the
      sentinel, the container-differs-from-key case, and `workdir_from`
      beating a literal `workdir` (the precedence that just flipped)
- [x] write tests for the `cli.user` fallback, including an explicit `user:`
      winning over `cli.user` and `user: internal` suppressing both
- [x] pin uid assertions against `${host.uid}`, never against the host shell's
      `id -u`
- [x] run `make test` — must pass before task 6

### Task 6: Document the workdir chain and land commit A

Doc edits here touch **only** the workdir sections, so a later revert of
commit B cannot conflict with them.

**Files:**
- Modify: `docs/reference/config/commands/types.md`
- Modify: `docs/i18n/ru/reference/config/commands/types.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [x] rewrite the workdir-resolution section for the seven-step chain,
      including the `internal` sentinel and its symmetry with `user: internal`
- [x] replace the mermaid diagram, which currently draws the old two-step fork
- [x] state explicitly that `workdir: internal` outranks `workdir_from` — for
      that one value it inverts the published "`workdir_from` wins" rule
      (`types.md:708`), and an undocumented inversion is a trap
- [x] update the daemon field list so it describes what the daemon now actually
      does for `user` and `workdir`
- [x] state the daemon nil-handling change made in task 5: a `workdir_from`
      dot-path that resolved to nothing used to hard-fail the daemon
      (`daemon_start.go:129-131`) and now falls through to the new chain, so it
      starts in a different directory instead of erroring. An error turning
      into a different-directory success is exactly what users must read about — the existing claim at l.702/708 becomes
      true only after task 5
- [x] mirror both edits into the RU translation
- [x] run `make build` so the embedded docs tree is re-synced
- [x] run `make test && make lint` — must pass
- [x] commit as commit **A**

➕ The RU mirror carries a `> Translated from: … @ <hash>` header pinned by
`TestRussianTranslationsAreFresh`; it was refreshed to the new English content
hash as part of this task.

[decision] `make lint` still reports exactly the 19 pre-existing `modernize`
findings in untouched files (recorded in task 3). No new findings — this task
changed no Go source beyond the regenerated `content_hashes_gen.go`.

[deviation] The task text says "commit as commit **A**". The execution harness
commits once per task, so tasks 3–6 landed as four separate commits; squashing
them into commit **A** is the orchestrator's job, not this task's.

### Task 7: Add `RunContext.UserInvoked`

**Files:**
- Modify: `internal/core/usercommands/runtime/spec/runner.go`

- [x] add the `UserInvoked bool` field with a doc comment stating: what it
      means, that the zero value suppresses the TTY and is therefore the safe
      default for any new entry point, that it gates container TTY allocation
      only, and why `SkipNotify` was not reused despite answering the same
      question today
- [x] confirm no existing construction site needs changing (the zero value is
      correct everywhere until task 10)
- [x] no tests in this task: the field carries no behaviour yet. Tasks 8, 10
      and 11 own its coverage — do not write a test that only asserts a struct
      field exists
- [x] run `make test` — must pass before task 8

### Task 8: Add `runio.WantContainerTTY` and a second terminal probe

**Files:**
- Modify: `internal/core/usercommands/runtime/internal/runio/runio.go`
- Modify: `internal/core/usercommands/runtime/internal/runio/runio_test.go`

- [x] add an injectable "is this writer/reader a terminal" probe (`*os.File`
      assertion plus `Fd()`; anything else is not a terminal) **alongside**
      `stdoutIsTerminal` — do not repoint the existing seam
- [x] leave `colorForceActive` (l.67) and `bridgedTTYActive` (l.197) probing
      the process's own `os.Stdout`; both mean the process stream, not
      `rc.Stdout`, and `bridgedTTYActive` is load-bearing for the bridge shape
- [x] add `WantContainerTTY(rc spec.RunContext) bool` implementing
      `rc.UserInvoked && (bridgedTTYActive(rc) || isTerminal(StdoutOf(rc)) && isTerminal(StdinOrOS(rc)))`,
      resolving through `StdoutOf` / `StdinOrOS` so a nil `Stdout` or `Stdin`
      falls back to the process streams instead of reading as "not a terminal"
- [x] document on it why the bridge arm short-circuits the terminal probe:
      `bridgedTTYChildIO` fabricates its PTY inside `WireChildIO`, after
      `BuildCommand` has already fixed the argv, so the probe would lie
- [x] document why the predicate is not a plain terminal probe: the pipeline
      fabricates a PTY in `childIO`, so a probe answers "yes" exactly where the
      change must bite
- [x] write a table test over `UserInvoked` × bridged-env × terminal/pipe
      stdout × terminal/pipe stdin, plus rows for a nil `Stdout`, a nil
      `Stdin`, and a non-`*os.File` writer such as a `bytes.Buffer`
- [x] write a test pinning that `bridgedTTYActive` still answers off the
      process stdout, so a later seam cleanup cannot quietly repoint it
- [x] run `make test` — must pass before task 9

### Task 9: Inject `-T` in `buildDockerComposeCmd` and extend colour forcing

Both halves land in one task because shipping the first without the second
turns every command's output in a deploy grey.

**Files:**
- Modify: `internal/core/usercommands/runtime/runners/service/exec.go`
- Modify: `internal/core/usercommands/runtime/internal/runio/runio.go`
- Modify: `internal/core/usercommands/runtime/runners/host/host.go`
- Modify: `internal/core/usercommands/runtime/runners/host/dwe.go`
- Modify: `internal/core/usercommands/runtime/runners/script/script.go`
- Modify: `internal/core/usercommands/runtime/runners/service/service_test.go`
- Modify: `internal/core/usercommands/runtime/internal/runio/runio_test.go`
- Modify: `internal/core/usercommands/runtime/runners/host/host_test.go`
- Modify: `internal/core/usercommands/runtime/runners/script/script_test.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`

- [x] update task 4's check-path argv assertion for the injected `-T`. It was
      written in commit A and this commit invalidates it, so the fix-up must
      land **here** — a revert of B has to restore it along with everything
      else. (Alternative, if you prefer A to be self-sufficient: have task 4
      assert only the `--workdir` subsequence and skip this checkbox.)
- [x] write the TTY-flag classifier: exact `-T`, any `-T=<value>`, and any
      argument whose name part is `--no-tty` compared **case-insensitively**.
      The compose flag is lowercase `--no-tty` on both `exec` and `run`, and
      pflag is case-sensitive, so an uppercase-only matcher recognises nothing
      a project can have written
- [x] run the classifier — and the `detached` probe — over the **effective**
      flag vector for the chosen subcommand, `compose.CommandArgs["exec"|"run"]`
      concatenated with the rendered `compose_args`. Those defaults come from
      `docker.yml`'s `args:` block and are appended first (`exec.go:257-270`),
      so a `--no-tty=false` or `-d` declared there is otherwise invisible
- [x] in `buildDockerComposeCmd`, immediately after `composeArgs` are appended,
      append `-T` when `!WantContainerTTY(rc)` and the classifier finds no TTY
      flag. Any occurrence hands control to the author regardless of its value,
      so `--no-tty=false` is the deliberate force-a-TTY escape hatch
- [x] state in a comment that only TTY flags suppress the auto-detect, and that
      `-d` / `--name` / `--rm` stay orthogonal on purpose
- [x] change the signature to
      `ColorForceEnv(rc spec.RunContext, forceOnSuppressedTTY bool) []string`
      and add the third disjunct to `colorForceActive`:
      `(forceOnSuppressedTTY && isTerminal(rc.Stdout))`. `runio` cannot see
      `compose_args`, so the caller computes the flag — the runner passes
      `ttySuppressed && !detached`. The `!detached` half is load-bearing: `-d`
      is valid on both `exec` and `run`, and a detached child's output never
      reaches `rc.Stdout`, so forcing colour there writes ANSI escapes into the
      Docker logs permanently
- [x] update **all four** call sites: `service/exec.go:297` passes the computed
      value; `host/host.go:79`, `host/dwe.go:43` and `script/script.go:200`
      pass `false` — a host-side child has no container TTY to suppress, and
      nobody should "helpfully" derive a value for them
- [x] document in the code that the new disjunct probes **raw `rc.Stdout`**
      while `WantContainerTTY` resolves through `StdoutOf`/`StdinOrOS`. The
      asymmetry is deliberate and load-bearing: it is what keeps a nil-`Stdout`
      internal caller from getting forced colour, and a later "consistency"
      cleanup unifying them would inject ANSI into parsed output
- [x] write argv tests: `-T` present/absent across `UserInvoked`, bridged env,
      and each classifier form already in `compose_args` — `-T`, `-T=false`,
      `--no-tty`, `--no-tty=false`, `--no-TTY` (case-insensitive match) — plus
      an unrelated flag such as `--name` that must **not** suppress the
      auto-detect
- [x] write a test proving `compose_args: ["-d"]` still gets `-T` (detach is
      orthogonal) but does **not** get the forced-colour variables, for both
      `service_exec` and `service_run`
- [x] write tests for the same two flags arriving through `DockerConfig.Args`
      (`args.exec` / `args.run`) rather than `compose_args`: a `--no-tty=false`
      there must suppress the injection, and a `-d` there must suppress the
      forced colour
- [x] write tests in `host_test.go` and `script_test.go` asserting the Host
      runner, the Dwe runner and the Script runner keep their existing colour
      behaviour after the signature change — `runio_test.go` is in package
      `runio` and cannot import runners that import it, and `service_test.go`
      cannot reach host or script code
- [x] write argv tests for `service_run` proving it inherits the same behaviour
- [x] write colour tests: the third disjunct fires for a terminal-like
      `rc.Stdout` with the TTY suppressed, does **not** fire for a piped
      `rc.Stdout` (so `dwe cmd foo | grep` stays uncoloured), and does **not**
      fire for a nil `rc.Stdout` — the row that pins the raw-vs-`StdoutOf`
      asymmetry
- [x] run `make test` — must pass before task 10

### Task 10: Set `UserInvoked` at the two entry points

**Files:**
- Modify: `internal/cli/command/runbyid.go`
- Modify: `internal/core/execution/pipeline/executor.go` (`ExecAction` — the
  marker write point; `buildDweCmd` is verify-only, it already inherits
  `os.Environ()`)
- Modify: `internal/shared/bridgeclient/env.go` (strip set)
- Modify: `internal/shared/bridgeclient/client_test.go` (`TestStripEnv`)
- Modify: `internal/core/usercommands/runtime/runners/workflow/step.go`
- Modify: `internal/cli/command/runbyid_test.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`
- Modify: `internal/core/usercommands/runtime/runners/workflow/workflow_test.go`

- [x] define the marker constant next to the other `DWE_*` env names and set it
      **process-globally** with `os.Setenv`, not per-spawn: `execShellAction`
      (`executor.go:216-220`) never assigns `cmd.Env` at all, and the set of
      spawn mechanisms is not reliably enumerable
- [x] in `runCommandByID`, read the marker **before** setting it:
      `UserInvoked = !markerSet`, then `os.Setenv` so everything this command
      spawns is classified as nested
- [x] set it unconditionally on entry to **`pipeline.ExecAction`**
      (`executor.go:177`) — not in the deprecated `Run` wrapper
      (`executor.go:388`), where it would be a silent no-op, and not only in
      `RunWithOptions` (`executor.go:446`), which `dwe reset step` bypasses by
      calling `ExecAction` directly (`lifecycle/reset.go:744,761`). This one
      place covers `type: shell`, `type: dwe`, `type: host` and `type: script`
      steps on both routes
- [x] record the deliberate gap: `files_gate` commands and shell `when:`
      predicates evaluated **before the first `ExecAction` of the process** run
      unmarked, so a `dwe cmd` re-entry from one of those is classified
      user-invoked. Later ones are marked — the marker is process-global and
      never cleared, and `evalFilesGate` runs per step (`executor.go:769`) — so
      do not pin a test on "gates are always unmarked". That is accepted —
      the failure mode is today's behaviour (no `-T`), i.e. conservative — but
      it is a decision, not an oversight
- [x] pin the read predicate as `os.Getenv(marker) != ""`, not `os.LookupEnv` —
      the tests clear the marker with `t.Setenv(marker, "")`, which `LookupEnv`
      would still report as set
- [x] verify the runners that build an explicit `cmd.Env` still pass it through:
      `DweRunner` (`host/dwe.go:44-56`) and `host.go:81` both start from
      `os.Environ()`, so the marker survives — confirm rather than assume
- [x] extend the existing `TestStripEnv` (`bridgeclient/client_test.go:461`)
      with a marker row — that is where a strip regression belongs
- [x] add the marker to the daemon's strip set in `bridgeclient.StripEnv`, so a
      marker set inside a container cannot cross the trust boundary and kill
      the TTY on every bridged command
- [x] set `UserInvoked = !nestedMarkerSet` in `runCommandByID` — it is
      documented as the single execution path for both `dwe commands <id>` and
      the TUI run flow, so this one assignment covers both, and the marker is
      what keeps a `type: dwe` step from re-entering as a "user" invocation
- [x] split the re-entry assertion in two, because the child is a **separate
      process** whose in-process `UserInvoked` a test cannot observe, and a real
      re-exec resolves `resolveDweBin` → `os.Executable()` → the *test binary*
      (the documented recursion hazard):
      (a) **propagation**, in package `pipeline`: after `RunWithOptions`,
      `buildDweCmd`'s `cmd.Env` carries the marker, and a `type: shell` step
      running `sh -c 'printenv DWE_NESTED_RUNTIME'` prints it through
      `StepWriter`;
      (b) **consumption**, in package `command`: with the marker pre-set via
      `t.Setenv`, the captured `RunContext` has `UserInvoked == false`. The
      capture is installed by `(*orchestratorStubs).installRunner()`
      (`runbyid_test.go:97-104`); `stubOrchestratorSeams`
      (`runbyid_test.go:19-37`) only saves and restores the four seams, and
      already forbids `t.Parallel` — which matches the `t.Setenv` constraint
- [x] write the `type: shell`/`DWE_BIN` propagation case explicitly — `DWE_BIN`
      is exported on purpose (`host.go:119`, `script.go:149`) so project code
      can call dwe again, and this is the path a per-spawn marker would miss
- [x] write a test using `t.Setenv` proving `runCommandByID` reads the marker
      before writing it, so a top-level invocation is not marked nested by its
      own assignment
- [x] clear the marker with `t.Setenv(marker, "")` in **every** new test that
      touches it: `runCommandByID`'s `os.Setenv` is process-global and never
      cleared, and roughly 30 existing tests in `runbyid_test.go` call it — the
      bridge test asserting `UserInvoked == true` would otherwise fail on test
      order alone. Note that `t.Setenv` forbids `t.Parallel` in that test
- [x] write a test proving the bridge path does **not** set the marker, so a
      bridged `dwe cmd` stays a user invocation
- [x] add a comment there stating that the host bridge reaches this same line
      (`bridge/exec.go:56` re-execs `dwe <argv…>` as a plain subprocess), and
      that the predicate must therefore never key off `NonInteractive` — the
      daemon force-sets `DWE_NONINTERACTIVE=1` on every forked `dwe`
- [x] in the workflow runner, read it as `rc.UserInvoked && !rc.UnderParallel`
      at the sub-step construction site in `step.go`, so a sequential sub-step
      inherits and a `parallel:` sub-step yields `false` — do not stamp a second
      field in `parallel.go:199`
- [x] write a test asserting a sequential workflow sub-step inherits the
      parent's value in both directions
- [x] write a test asserting a `parallel:` sub-step gets `false` even when the
      parent is `true`
- [x] run `make test` — must pass before task 11

### Task 11: Pin that the pipeline never sets `UserInvoked`

The omission in `execCommandAction` is the behaviour change, so it needs a test
that fails if someone "fixes" it later.

**Files:**
- Modify: `internal/core/execution/pipeline/executor_notify_test.go`

- [x] write a test asserting `execCommandAction` builds a `RunContext` with
      `UserInvoked == false`, sequential and parallel alike, driving the
      `runtime.TestSnapshotRC` seam and sitting next to its existing twin
      `TestExecCommandAction_SetsSkipNotify`
- [x] name in the test comment why: a `type: command` step must never hand the
      container a terminal, and this is the sole guard
- [x] run `make test` — must pass before task 12

### Task 12: Document the TTY behaviour and land commit B

Doc edits here touch **only** the `compose_args` / TTY sections.

**Files:**
- Modify: `docs/reference/config/commands/types.md`
- Modify: `docs/i18n/ru/reference/config/commands/types.md`
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md` (any B-specific pointer bullet belongs here, not in C)
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [x] if a pointer bullet about the TTY contract is warranted in `AGENTS.md`,
      add it **in this commit** — a B-specific pointer landing in C would
      survive a revert of B. Budget: the file is 39 186 B against
      `agentsMdBudget = 40*1024` (`internal/cli/docs/agentsmd_test.go:28`),
      i.e. 1 774 B of headroom shared with task 15's bullet. Existing bullets
      run ~550 B; keep this one under 800 B or the budget test goes red after
      the commit. Two more gates on the same file: `agentsMdMaxLineLen = 600`
      (`agentsmd_test.go:33`, enforced by `TestAgentsMdCriticalPatternsLineLength`
      over runes, `## Critical Patterns` only) — one
      sentence per line; and `TestAgentsMdPointersResolve`, which requires every
      `§ <target>` to resolve — a backticked package path must match a
      `` - `path` `` bullet in `packages.md`, a bare title must match a `## `
      heading — so point this bullet at B's own new section (added in this same
      commit)
- [x] document the B-specific invariants in `packages.md` **in this commit**,
      not in commit C: the `UserInvoked` contract (who sets it, why `false` is
      the safe zero value, why `SkipNotify` was not reused), the
      `DWE_NESTED_RUNTIME` marker and its strip-set requirement, and the paired
      colour-forcing rule with its `!detached` guard. Keeping them here is what
      makes a revert of B remove the prose along with the mechanism
- [x] rewrite the `compose_args` section: it currently recommends `-T`, which
      the runner now supplies on its own; say what still needs an explicit flag
- [x] add the TTY rule in prose — a top-level `dwe cmd` on a terminal gets a
      container TTY, everything else (`deploy run`, workflows' parallel blocks,
      `check:` probes, piped output) gets `-T`; an explicit `-T` / `--no-tty`
      (lowercase, as compose spells it) in the effective flags wins; unrelated
      flags do not
- [x] state that there is no dedicated schema field for forcing a container TTY
      inside a pipeline, and that `compose_args: ["--no-tty=false"]` is the
      explicit, deliberately awkward way to ask — do **not** write that it is
      impossible, because the classifier hands control to any TTY flag
      regardless of its value
- [x] mirror into the RU translation
- [x] run `make build`, then `make test && make lint` — must pass
- [x] commit as commit **B**, as a single self-contained revert unit

➕ `packages.md` gained its own top-level `## Container TTY Contract` section,
placed between `## Shared — Leaf Infrastructure` and `## Entrypoint`.

[decision] A new `## ` heading rather than prose folded into an existing
package bullet: the contract spans `cli/command/`, `execution/pipeline/`,
`usercommands/runtime/` and `shared/bridgeclient/`, so it belongs to no single
layer — and a standalone section is what lets commit C add the A/C contracts to
the same file in a separate hunk, keeping a `git revert` of B mechanical.

[decision] Placed at the end of the layer walk-through (before `## Entrypoint`)
rather than mid-file, so the insertion cannot collide with a C-era edit inside
`## Core — User Commands`.

➕ `AGENTS.md` gained a **Container TTY** pointer bullet after **Container
command policy**, pointing at `§ Container TTY Contract`. It adds 742 B
(39 186 → 39 928 B against the 40 960 B budget), leaving 1 032 B for task 15's
bullet. Longest new line is 290 runes against the 600-rune cap.

➕ `types.md`: the `compose_args` section no longer recommends `-T` (the runner
supplies it) and a new `### Container TTY` section — plus a
`#### Overriding the decision` sub-section — documents the rule, the effective
flag vector, the case-insensitive `--no-tty` match, the paired colour forcing
with its `-d` exclusion, and `compose_args: ["--no-tty=false"]` as the explicit
force-a-TTY escape hatch. The `### Mode resolution` section (commit C) and the
workdir sections (commit A) were left untouched.

➕ The RU mirror's `> Translated from: … @ <hash>` header was refreshed to the
new English content hash (`bed7d209fbf6` → `7338d8291752`), which is what
`TestRussianTranslationsAreFresh` compares.

[decision] `make lint` still reports exactly the 19 pre-existing `modernize`
findings in untouched files. No new findings — this task changed no Go source
beyond the regenerated `content_hashes_gen.go`.

[deviation] The task text says "commit as commit **B**". The execution harness
commits once per task, so tasks 7–12 landed as separate commits; squashing them
into commit **B** is the orchestrator's job, not this task's.

### Task 13: Flip the exec mode default

**Files:**
- Modify: `internal/core/usercommands/model/types.go`
- Modify: `internal/core/usercommands/runtime/runners/service/exec.go`
- Modify: `internal/core/usercommands/model/types_test.go`
- Modify: `internal/core/usercommands/runtime/runners/service/service_test.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`

- [x] **first**, introduce the probe seam this task's tests depend on:
      `isContainerRunning` (`exec.go:325`) is unexported and shells out to
      `docker compose ps`, which is why every existing test in
      `service_test.go` uses `ExecModeExec` or `ExecModeRun` and never the
      default. Add `var containerRunningFn = isContainerRunning`, call through
      it, and restore it with `t.Cleanup` in tests. Without this seam the three
      probe-dependent checkboxes below cannot be written at all
- [x] set `DefaultExecMode = ExecModeExecOrRun` and update its doc comment
- [x] update the `ExecModeExecOrFail` doc comment: it is no longer the default,
      and it is the only mode that pre-probes for a clean dwe error
- [x] fix the two stale "default" statements in `exec.go`: the `ExecRunner`
      doc comment (l.26, "exec-or-fail (default): refuses…") and the error
      string at l.59, whose advice to "set `mode: exec-or-run`" inverts once
      that is the default — it should point at `mode: exec-or-fail` as the
      opt-in, or drop the suggestion. The package doc (l.1-6) lists the modes
      but claims no default; do not go hunting for text that is not there
- [x] update the existing test asserting the old default
- [x] write a test proving a command with no `mode:` takes the `exec-or-run`
      branch, falls back to an ephemeral run when the container is stopped, and
      emits the warning
- [x] write a test proving an explicit `mode: exec-or-fail` still refuses with
      the dwe error, so opting back in works
- [x] write a test pinning that **both** modes select `exec` after a probe
      *error* — that is today's behaviour on both branches
      (`exec.go:57-61` and `exec.go:63-67`), and the flip must not be
      documented as changing it
- [x] add an executor-level test **in package `pipeline`**
      (`executor_test.go` — `service_test.go` cannot import `pipeline` without
      an import cycle): a step whose `check:` is a `type: command` action
      pointing at a mode-less `service_exec` command, with the service stopped.
      Reuse the stub-docker harness from task 4 — a stub `compose ps` printing
      nothing is what makes "stopped" reachable from package `pipeline`, since
      the `containerRunningFn` seam is unexported and lives in package
      `service`. It must show the new default turning a postcondition into a
      container-creating action, so the consequence is pinned rather than
      discovered in a project
- [x] keep this test `-T`-agnostic like the rest of task 13 — it runs in the
      pipeline, where `UserInvoked` is false and commit B's `-T` is present
- [x] inventory the `check:` actions across the local workspaces that reference
      a `service_exec` command without an explicit `mode:`; record the count
      here and add explicit `mode: exec-or-fail` to any check that must not
      create a container — **count: 0**, so no workspace edit was needed. The
      cut happens one step earlier than expected: across the 9 local workspaces
      there are 38 `check:` actions in total (34 `type: builtin`, 3
      `type: shell`, 1 `auto`) and **none** is `type: command`. `type: command`
      is used 104 times, but always as a step's own type, never inside a
      `check:`/`when:` action. The other side of the intersection does exist —
      7 of the 228 `service_exec` commands declare no `mode:` — but none of
      them is reachable from a check
- [x] **constraint for revert safety**: these tests assert the exec-vs-run
      *branch* and the warning text, never a full argv slice. `service_test.go`
      by now contains commit B's `-T` assertions, and every context here has
      `UserInvoked == false`, so any full-argv assertion written in C would
      hard-code B's output and fail the moment B is reverted — the exact
      failure task 18 exists to catch
- [x] run `make test` — must pass before task 14

### Task 14: Document the mode default

Doc edits here touch **only** the mode-resolution section.

**Files:**
- Modify: `docs/reference/config/commands/types.md`
- Modify: `docs/i18n/ru/reference/config/commands/types.md`
- Modify: `docs/guides/author-project-commands.md`
- Modify: `docs/i18n/ru/guides/author-project-commands.md`
- Modify: `skills/dwe/references/authoring-commands.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [x] update the `mode` row in the fields table
- [x] rewrite the mode-resolution prose — the paragraph beginning "Pick
      `exec-or-fail` (the default)…" now contradicts the shipped default, and
      the guidance inverts: declare `exec-or-fail` for tools that depend on
      persistent container state
- [x] state that the observable difference is confined to one state — the probe
      succeeds and reports the container stopped. Do **not** claim a
      probe-error consequence: both modes already end at `exec` when the probe
      fails, so an unreachable Docker daemon behaves identically before and
      after
- [x] state that a `type: command` `check:` referencing a mode-less
      `service_exec` command becomes container-creating, and that such checks
      should declare `mode: exec-or-fail`
- [x] mirror both files into the RU translations
- [x] update the `mode: exec-or-run` comment in the authoring reference so it
      no longer implies the value must be written out, and fix line 48 of the
      same file — "Needs `service:` + `mode:` + `workdir_from:`" is wrong on two
      counts after this work: commit A makes `workdir_from` optional and this
      commit makes `mode` optional
- [x] decide about the `mode: exec-or-run` line in the worked example at
      `docs/reference/config/commands/index.md:143` and its RU mirror (`:145`):
      it is not wrong after the flip, just redundant. Either drop it or state
      here that it stays deliberately, so the next reader does not re-open it
- [x] run `make build && make test` — must pass before task 15

### Task 15: Record the invariants and land commit C

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `CHANGELOG.md`
- Modify: `AGENTS.md` (A/C pointer bullets only — no B content; see task 12)
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [x] document in `packages.md` **only the A and C contracts**: the two new
      `project/config` helpers with the by-`Container` lookup rule and the
      sorted-iteration requirement, and the exec-mode default with its reach
      into `check:` actions. The B contracts already landed in commit B (task
      12) precisely so that reverting B removes them. Commit A's contract
      living here is a deliberate asymmetry: C is the last commit and carries
      no revert requirement, unlike B
- [x] place this prose in a **separate `§` section** from commit B's block in
      `packages.md`. Both commits edit that one file, and appending adjacent to
      B's text would make `git revert B` conflict there too — contradicting
      task 18's "nothing beyond the three touch-ups"
- [x] add **one** `## [Unreleased]` CHANGELOG entry, prefixed `**Breaking:**`
      per the existing convention in that file, covering all three changes as a
      single aggregate behaviour change: the workdir chain, the TTY rule and
      the mode default. Do **not** write a probe-error clause — there is no
      probe-error behaviour change, and task 14 spells out why
- [x] state in the entry that the `workdir: internal` sentinel means a service
      command whose container workdir is literally the relative path `internal`
      changes behaviour — the one exception to this work's
      already-explicit-declarations-are-unchanged promise
- [x] note inside that entry that the TTY half lands as its own commit and that
      reverting it requires editing this entry
- [x] state in the entry that a full forced redeploy is needed and that the
      deployment hash will not signal it
- [x] add at most a pointer bullet to `AGENTS.md` for the A and C contracts —
      the write-up itself stays in `packages.md`, and anything B-specific
      already landed in task 12. Together with that bullet you have 1 774 B of
      headroom against `agentsMdBudget`; keep this one under 800 B, respect
      `agentsMdMaxLineLen = 600` (`agentsmd_test.go:33`), and make its
      `§ <target>` resolve to C's own `packages.md` section so
      `TestAgentsMdPointersResolve` stays green. Place it **non-adjacent** to
      commit B's bullet, for the same revert reason as the `packages.md` rule
- [x] run `make build && make test && make lint` — must pass
- [x] commit as commit **C**

[deviation] "commit as commit **C**" — the harness commits once per task, so
tasks 13–15 landed as separate commits; squashing them into commit **C** is the
orchestrator's job, same as for **B**.

[decision] The new `packages.md` section sits between `## Core — User Commands`
and `## Core — Validation` — topically adjacent to the runner it describes and
separated from commit B's `## Container TTY Contract` (which sits between
`## Shared` and `## Entrypoint`) by six whole sections, so `git revert B` cannot
conflict with it. The `AGENTS.md` bullet sits after **Per-service folder
symmetry**, likewise far from B's **Container TTY** bullet.

[decision] The section deliberately carries no `§ Container TTY Contract`
cross-reference. An earlier draft ended the `containerRunningFn` bullet with
one; it would dangle the moment B is reverted, so the same point is now made
without naming B's section.

[measured] `AGENTS.md` after the bullet: 40 688 B against the 40 960 B budget
(272 B headroom); the bullet itself is 760 B over four lines, longest 292 runes
against the 600-rune cap. `make build && make test` green; `make lint` reports
exactly the 19 pre-existing `modernize` findings in untouched files, unchanged.

### Task 16: Capture the "after" TTY matrix and compare

**Files:**
- Modify: `docs/plans/20260901-service-exec-runtime-defaults.md`

- [x] re-run every cell captured in task 2 against the built binary (five, or
      six if a wrapper command was found)
- [x] cell 1 — `dwe cmd <id>` from a real terminal: expect `/dev/pts` on all
      three fds, unchanged from the "before" snapshot
- [x] cell 2 — the same command as a `type: command` step inside
      `dwe deploy run`: expect a pipe **and colour still present**; this pair is
      the entire point of the colour change, and a grey result is a failure,
      not a cosmetic difference
- [x] cell 3 — the same command over the host bridge from inside a container:
      expect `/dev/pts`, unchanged from the "before" snapshot
- [x] cell 4 — `dwe cmd <id> | cat`: expect a pipe and no colour
- [x] cell 5 — a snapshot workflow step from a real terminal: expect a pipe.
      This one **moves** relative to the "before" snapshot and is expected to;
      confirm the output is still readable and coloured
- [x] paste the results under a "TTY matrix — after" heading and mark any cell
      that moved unexpectedly with ⚠️

**TTY matrix — after** (`v0.5.0-36-g456f5926`, same `/tmp/ttyprobe` harness,
same `script -q /dev/null` PTY simulation)

| # | invocation | container fds | `tty(1)` | colour forcing | vs. before |
| --- | --- | --- | --- | --- | --- |
| 1 | `dwe cmd probe.tty` at a terminal | `stdin=tty stdout=tty stderr=tty` | `/dev/pts/0` | none | unchanged |
| 2 | `type: command` step in `dwe deploy run --force`, host stdout piped | `stdin=pipe stdout=pipe stderr=pipe` | `not a tty` | none | unchanged |
| 2a | the same **at a real terminal** | `stdin=pipe stdout=pipe stderr=pipe` | `not a tty` | **`CLICOLOR_FORCE=1 FORCE_COLOR=1`** | **moved, by design** |
| 3 | bridged, `docker exec -it … dwe cmd probe.tty` | `stdin=tty stdout=tty stderr=tty` | `/dev/pts/1` | `CLICOLOR_FORCE=1 FORCE_COLOR=1` | unchanged |
| 4 | `dwe cmd probe.tty \| cat` | `stdin=pipe stdout=pipe stderr=pipe` | `not a tty` | none | unchanged |
| 5 | snapshot workflow step at a terminal | `stdin=pipe stdout=pipe stderr=pipe` | `not a tty` | **`CLICOLOR_FORCE=1 FORCE_COLOR=1`** | **moved, by design** |
| 6 | wrapper `dwe cmd wrap.tty` at a terminal — inner `dwe cmd probe.tty` | `stdin=pipe stdout=pipe stderr=pipe` | `not a tty` | **`CLICOLOR_FORCE=1 FORCE_COLOR=1`** | **moved, known regression** |

No cell moved unexpectedly. The three that moved are exactly the three the
design predicted, and each moved together with its colour forcing:

- **Cells 2a and 5 are the payoff.** The container now sees a pipe where it used
  to see `childIO`'s fabricated `/dev/pts/0`, and `CLICOLOR_FORCE=1
  FORCE_COLOR=1` appears in the same step. This is the pair the plan called
  mandatory rather than cosmetic; had the forcing not landed, every deploy step
  and every snapshot step would have gone grey.
- **Cell 6 is the wrapper regression, now observed rather than predicted.** The
  outer `type: shell` command keeps its terminal (`outer stdout=tty`); the
  nested `dwe cmd` inherits the process-global marker and gets `-T`. An
  interactive wrapper (`db.cli` → `db.psql`) loses its terminal here and must
  declare a TTY flag explicitly. This is the documented cost of the
  process-global marker, and cell 6 is the evidence for the release note.
- **Cell 2 stays uncoloured on purpose.** Task 16's checkbox reads "expect a
  pipe **and colour still present**", which is imprecise: with dwe's own stdout
  redirected there is no terminal anywhere in the chain, so forcing colour would
  write ANSI into a file. `ColorForceEnv`'s `isTerminal(rc.Stdout)` disjunct is
  what distinguishes cell 2 from cell 2a, and both readings are correct.

**Harness torn down** after the capture: stack stopped, bridge daemon stopped,
`/tmp/ttyprobe` removed.

### Task 17: Re-run the per-workspace scenario baseline

**Files:**
- Modify: `docs/plans/20260901-service-exec-runtime-defaults.md`

- [x] run the existing scenario suite in each of the five workspaces that
      already have a green baseline — plus three more; `ficbird` was skipped
      (active development there) and `tbm` was skipped (VPN-gated, 60–90 min per
      scenario)
- [x] for the mode change, exercise it while the target container is
      **stopped** — that is the only state in which the mode is observable
- [x] record which workspaces are green and note that only the mode change is
      observable here, so a green run says nothing about the TTY or workdir
      halves
- [x] investigate and fix any regression before proceeding

**Scenario results — beetDeck** (5 scenarios, binary `v0.5.0-36-g456f5926`)

| scenario | result | reading |
| --- | --- | --- |
| `smoke-deploy` | passed (1m10s) | unchanged |
| `core-only` | passed (1m09s) | unchanged |
| `mcp-smoke` | passed | unchanged |
| `fail-demo` | failed at its deliberate closed-port step | unchanged — that is the scenario's purpose |
| `runner-defaults` | **failed at the mode pin** | **the intended detection** |

`runner-defaults` is a purpose-built detector this workspace already carried:
it stops the backend container and asserts that a `service_exec` command
declaring **no** `mode:` leaves no marker behind, i.e. that the default refuses.
Commit C inverts exactly that. The run printed the runner's own fallback
warning — `service "backend" is not running — falling back to ephemeral
"docker compose run --rm"` — created `…-backend-run-…`, wrote the marker, and
the pin failed as designed. This is the only live-workspace evidence for the
mode flip, and it is positive evidence, not an absence of failures.

Two further readings from the same run, both **unchanged**, which is what
commits A and B needed to show here:

- the cwd pin still reports `pwd=/workspace/src uid=1000 gid=1000` for a
  command declaring no `workdir`/`workdir_from`/`user` — the new chain resolves
  to the same path this project already used (its header explains why: four
  sources agree on `/workspace/src`, so this pin is a record, not a detector);
- the TTY pin still reports `stdin=pipe stdout=pipe` for a command declaring no
  `compose_args` — a scenario is non-interactive, so the auto-detect reaches
  the same answer the old inherit-the-default path reached.

**The workspace's own detector now needs inverting**, and that edit belongs to
beetDeck, not to this repo: `workspace/tests/runner-defaults.yml`'s last step
must assert the marker is **present**, and the surrounding prose (which states
"today the default refuses") must be rewritten. Left untouched here so the
change lands with whoever owns that workspace; until then beetDeck's baseline
is red by design, not by regression.

**Scenario results — the remaining workspaces** (same binary; each run on an
isolated `dwe test` copy, no live stack touched)

| workspace | scenario | result | reading |
| --- | --- | --- | --- |
| AlbFetcharr | `exec-semantics` | failed at `mode-default-must-not-fall-back-to-run` | **mode-flip detection** |
| alto | `exec-runner-defaults` | failed at `no ephemeral fallback happened` | **mode-flip detection** |
| cueBreaker | `exec-contract` | failed at `no ephemeral container ran` (11/12 green) | **mode-flip detection** |
| laravel | `smoke`, `tools`, `debug` | all three passed | no surface for the change |
| podlapka | `full` | failed in the pre-scenario deploy | environmental |
| magento | `smoke` | failed at `stack-up` | environmental |

**Four independent detections of the mode flip.** beetDeck, AlbFetcharr, alto
and cueBreaker each carry a purpose-built probe declaring no `mode:`, each stops
its target container, and each asserts the old refusing default. All four failed
at exactly that assertion, and all four logged the runner's own warning —
`service "<name>" is not running — falling back to ephemeral "docker compose
run --rm"` — followed by the ephemeral container being created. alto's probe
went further and printed a container hostname differing from the earlier exec
run's, proving the body executed in a throwaway container rather than the
original one. This is positive evidence, not an absence of failures.

**One genuine TTY detection.** cueBreaker's scenario asserts
`[ ! -t 0 ] && [ ! -t 1 ] && [ ! -t 2 ]` on three pipeline-invoked steps; all
three passed, so commit B did not leak a terminal into a pipeline step. No
workspace has a probe that can observe the user-invoked-vs-pipeline distinction
in the other direction — that is what the `/tmp/ttyprobe` matrix in task 16 is
for.

**No workspace can discriminate the workdir chain.** Every scenario reported the
same cwd as before, and every scenario header says why: in these projects the
old "pass no `--workdir`" path and the new chain resolve to the *same* directory
(`cli.workdir` equals `work_dir_internal` equals the compose `working_dir`), or
the target service declares none of the three. Commit A is shown non-breaking
here; it is **not** validated by these runs. The unit table in task 18 is the
only place it is.

**The two environmental failures were both proven environmental, not assumed.**

- podlapka died in the pre-scenario deploy: `glitchtip` exits because its
  database does not exist. A control run on the released `0.5.0` binary failed
  identically at the same step. The project-side cause is real and worth its own
  fix — `glitchtip` was added on 2026-08-28, is `enabled: true` in the
  developer's `local.yml` (which `dwe test` seeds into the copy), has no
  `deploy.yml` and no pipeline phase, and its own header documents a manual
  `dwe cmd db.ensure-glitchtip` bootstrap that no step performs. Every test copy
  has been broken since that date.
- magento died on a port conflict: `dwe-ficbird-redis` holds `127.0.0.1:6379`.
  `dwe test`'s port remapping never moved it, because `db`, `valkey` and
  `opensearch` are compose-only services with no `workspace/services/<name>/`
  folder, so `AllocatePorts` has no entry to remap; the single automatic retry
  re-picked the same port. Verified directly: those three folders do not exist,
  and ficbird's live redis holds the port. A pre-existing `dwe test` isolation
  gap, unrelated to this branch.

**Where the change has no surface at all.** laravel declares `mode:` on all 34
of its `service_exec` commands (31 `exec-or-run`, 3 `exec-or-fail`) and
`workdir_from:` on 30 of 36, so its three green scenarios show only that nothing
broke. magento is the same: every `service_exec` declares `mode: exec-or-run`,
and its `db`/`valkey`/`opensearch` targets have no service folder and therefore
no chain to inherit.

**The wrapper regression was inventoried in magento, the project most likely to
have one.** Four `type: shell`/`type: script` sites re-enter dwe. Two
(`varnish.enable`/`disable`) call non-interactive inner commands with `--yes`
and are wired as service hooks that were never interactive. Two go through
`dwe docker exec`, a different code path that passes `-i -T` explicitly, and the
one genuinely stdin-dependent case — `gunzip -c dump.gz | dwe docker exec … 
mariadb` — is unaffected because commit B only ever appends `-T` and never
touches `-i`. No command in that project loses anything that matters.

**Four more workspace scenarios now need inverting**, alongside beetDeck's:
AlbFetcharr's `exec-semantics`, alto's `exec-runner-defaults` and cueBreaker's
`exec-contract` all pin the old refusing default. Each of their headers already
names this outcome as the expected signal that the default changed, so the edit
is anticipated — but it is a workspace-side edit, not a repo one.

**Not covered.** `ficbird` (active development), `ficbird-main` (a second
checkout of the same project, sharing images and external volumes with it) and
`tbm` (every scenario is a VPN-gated 60–90 min full deploy).

### Task 18: Verify acceptance criteria

- [x] verify all seven workdir rungs behave as described in Solution Overview,
      including the `internal` sentinel on both the service and daemon paths
- [x] verify a command that declares an explicit `mode:`, an explicit
      `workdir:` **and** a TTY flag in its effective compose flags sees
      byte-identical argv before and after — that is the precise compatibility
      promise; a `compose_args` list without a TTY flag is not covered by it
- [x] rehearse the full revert of commit B on a scratch branch:
      `git revert <B>` → resolve the one mechanical conflict in
      `content_hashes_gen.go` by running `make build` → drop the TTY clause
      from the CHANGELOG entry → `make test`. Nothing beyond those three
      touch-ups may be needed; if C's tests fail, the argv constraint in task
      13 was violated. Note `executor_test.go` is now touched by all three
      commits (task 4, task 9's fix-up, task 13), so keep C's executor test in a
      separate hunk from B's fix-up — same non-adjacency rule as `packages.md`
      and `AGENTS.md`, or the revert picks up a fourth conflict
- [x] on that same scratch branch, grep `docs/internals/packages.md` and
      `AGENTS.md` for `UserInvoked`, `DWE_NESTED_RUNTIME`, `WantContainerTTY`,
      `no-tty`, `TTY` and the colour-forcing rule — the revert must have taken
      all of that prose with it, including any pointer bullet whose wording does
      not contain a symbol name. A green build with orphaned guidance still
      counts as a failed revert
- [x] verify no deployment hash changed — confirm a `type: command` step still
      reports `already up-to-date` after the upgrade, which is what makes the
      forced redeploy necessary
- [x] run the full suite: `make test-race`
- [x] run `make lint`

**How each criterion was verified.**

- **Seven rungs.** `TestExecRunner_BuildCommand_WorkdirChain` enumerates all of
  them as one table — sentinel (bare and inside `runner:`), sentinel outranking
  `workdir_from`, `workdir_from` over the literal, literal over `cli.workdir`,
  `cli.workdir` over `work_dir_internal`, `work_dir_internal` over
  `dir_internal`, `dir_internal` last, plus the container-differs-from-map-key
  case and two no-flag cases. `TestRunRunner_BuildCommand_WorkdirChain` pins
  that `service_run` inherits it, and
  `TestResolveDaemonWorkdirUser_workdirChain` mirrors the same table on the
  daemon path, including the two nil-`workdir_from` fall-throughs that used to
  be a hard error there.

- **Byte-identical argv** [measured, cross-tree]. Not argued from the code: a
  worktree was checked out at `aea8d3cc` (the commit before A), the same
  throwaway pin was compiled in **both** trees, and the printed argv and colour
  env were diffed. Four cases — `-T`, `--no-tty`, `service_run`, and one adding
  an explicit `user:` — each against a service declaring `cli.workdir`,
  `work_dir_internal` **and** `dir_internal`, so a chain consulted by mistake
  would produce a visibly different `--workdir`. Result: `IDENTICAL`, e.g.

  ```
  ["docker" "compose" "-p" "dwe-laravel" "exec" "-T" "--user" "www-data" \
   "--workdir" "/literal" "app-main" "sh" "-c" "ls -la"]
  COLORENV []
  ```

  The empty `COLORENV` matters as much as the argv: an explicit TTY flag also
  suppresses the colour forcing, so such a command's environment is unchanged
  too. The pin was deleted after the comparison.

- **Revert rehearsal.** Done, and it contradicted the recipe above — see the
  correction recorded further down; the revert is a manual merge over three
  conflicts, not three touch-ups. The prose half of the criterion did hold:
  `packages.md`, `AGENTS.md` and `types.md` reverted cleanly with no orphaned
  guidance.

- **Deployment hash unchanged** [measured, live workspace]. In beetDeck,
  `dwe deploy run` on the new binary skipped all 19 tracked steps as
  `already deployed`, including the four `type: command` steps
  (`fetch-library`, `unpack-library`, `install-python-deps`, `install-deps`),
  and `dwe status` reports `PREV HASH == CURR HASH` for all three services.
  A byte-for-byte diff of `dwe deploy plan -o json` between the pre-change and
  post-change binaries is also empty (3 530 B both). This is the positive
  evidence for the forced-redeploy requirement: the upgrade is invisible to the
  journal, so nothing prompts the user.

- **`make test-race`** green across the suite; **`make lint`** reports 19
  findings, and cross-checking every `.go` file this branch touches against that
  list returns nothing — all 19 are the pre-existing `modernize` findings in
  untouched files.

**Unrelated observation, recorded because it was found here.** In beetDeck,
`dwe deploy run` prints `Phase: start: Start containers and wait for health` and
then finishes in 0s without running the phase's untracked `docker up --wait`
step — the stack stays down while the command reports success. This is **not
caused by this branch**: the pre-change binary (`aea8d3cc`) behaves identically
on the same project, and `dwe docker up --wait` invoked directly starts all five
containers. beetDeck has no project `deploy.yml`, so this is the built-in
default pipeline. Worth a separate look; out of scope here.

### Task 19: [Final] Update documentation

- [x] re-read the changed doc sections end to end for internal contradictions,
      especially anywhere `-T` or `exec-or-fail` is still described as advice
- [x] confirm `make build` was run last, so `internal/core/docs/embedded/` is
      not stale in the built binary
- [x] draft the upgrade-guide paragraph (why a full forced redeploy is needed,
      why the deployment hash will not show it) and leave it in this plan for
      the work that owns that page — do not create the page here
- [ ] move this plan to `docs/plans/completed/` — **deliberately not done**:
      task 17 covered one of five workspaces, so the plan is not finished. Move
      it once the remaining four have been re-run.

**Doc consistency pass.** Every `-T` and `exec-or-fail` mention across
`docs/reference/config/commands/types.md`, `docs/guides/author-project-commands.md`
and both RU mirrors was re-read. `-T` is described as redundant rather than
recommended; `exec-or-fail` appears only as an opt-in for state-dependent tools
and as the `check:` guidance, never as the default. No contradiction found.

**Upgrade-guide paragraph (draft — belongs in `docs/guides/upgrading.md`, filed
by the work that owns that page).**

> **Run a forced redeploy after upgrading to this release.** Three runtime
> defaults changed for `service_exec` / `service_run` commands: where the
> container working directory comes from when a command declares no `workdir:`,
> whether the container gets a terminal, and what happens when the target
> container is stopped. None of the three changes a single byte of your
> configuration — and the deployment journal hashes configuration, not engine
> behaviour. So `dwe deploy run` will report every step `already up-to-date` and
> skip it, and your workspace will keep running the old semantics under the new
> binary until you force the pipeline through:
>
> ```sh
> dwe deploy run --force
> ```
>
> Two things to check afterwards. A command you invoke interactively through a
> `type: shell` wrapper (a `db.cli` that calls `dwe cmd db.psql`) no longer gets
> a terminal inside the container — declare `compose_args: ["--no-tty=false"]`
> on the inner command if it needs one. And any command referenced from a step's
> `check:` should declare `mode: exec-or-fail`, since the new default would let
> a postcondition create a container.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes,
informational only*

**Manual verification:**

- The TTY matrix in tasks 2 and 16 is the only evidence for the riskiest change
  in this set. If a cell cannot be captured, say so explicitly rather than
  treating a green `make test` as coverage.

**External system updates:**

- **Forced full redeploy in every workspace after the merge.** `dwe deploy run`
  will not do it on its own: none of the three changes alters the deployment
  hash, so a `type: command` step with changed semantics reports
  `already up-to-date` and is skipped. Until a forced redeploy runs, a
  workspace is running the old semantics with a new binary.
- **Upgrade guide.** The paragraph drafted in task 19 belongs to the work that
  owns `docs/guides/upgrading.md`; it is written here but filed there.
- **Four workspace scenarios must be inverted.** beetDeck's `runner-defaults`,
  AlbFetcharr's `exec-semantics`, alto's `exec-runner-defaults` and cueBreaker's
  `exec-contract` all assert the old refusing default and are red by design
  after commit C. Each edit belongs to its workspace: flip the pin to expect the
  marker, and rewrite the header prose that says the default refuses.
- **Three workspaces still unexercised:** `ficbird` and `ficbird-main` (active
  development; the two share images and external volumes) and `tbm` (VPN-gated,
  60–90 min per scenario).
- **Two unrelated project-side gaps found while running the suite**, both
  pre-existing and both proven so: podlapka's `glitchtip` needs a database
  bootstrap that no pipeline step performs, so every `dwe test` copy has failed
  since 2026-08-28; and `dwe test` does not remap host ports for compose-only
  services that have no `workspace/services/<name>/` folder, which is what let
  magento's `valkey` collide with a foreign live stack on 6379.
- **Redundant-declaration cleanup.** Roughly 500 lines of now-redundant `mode:`
  and `compose_args: ["-T"]` across the workspaces are a separate later pass.
  `compose_args: ["-T"]` must be swept last, and only where a scenario covers
  the command — losing a TTY on an interactive bridged command is a silent
  failure.

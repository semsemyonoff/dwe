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

So the revert of B is: `git revert <B>` → `make build` → drop the TTY clause
from the CHANGELOG entry → `make test`. Task 18 rehearses exactly this on a
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

### Blast radius (measured across nine local workspaces, 224 service commands)

- **workdir**: 24 commands have no `workdir:`. 10 target `type: infra`
  services (unaffected), 3 target a service with no `workspace/services/`
  folder (unaffected), 6 genuinely change cwd — laravel and magento log
  commands, which survive only because their paths are absolute. Daemons were
  never counted; task 1 fixes that before commit A lands.
- **TTY**: 119 of 224 (53%) carry no `compose_args` at all — but this figure is
  a **lower bound and must be recomputed** (task 1). The set that moves is
  "commands whose *effective* flags carry no TTY flag", which also includes
  every command whose `compose_args` holds only unrelated flags, and excludes
  every service whose `docker.yml` `args.exec` / `args.run` already supplies
  one. Counting by "has a `compose_args` list at all" measures the wrong thing.
  Those 119 move from a
  container TTY to `-T` inside `dwe deploy run`. This is the riskiest item of
  the three.
- **mode**: 206 of 224 already declare `exec-or-run`; 2 commands in one
  workspace rely on the strict default.

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

- [ ] recount the TTY blast radius by **effective flags**, not by "has a
      `compose_args` list": a command moves when neither its `compose_args` nor
      its service's `docker.yml` `args.exec` / `args.run` carries a TTY flag.
      The current figure of 119/224 counts only commands with no `compose_args`
      at all and is a lower bound
- [ ] count the **wrapper** case the blast radius does not yet name: a
      `type: dwe` or `type: shell` command whose text invokes
      `dwe cmd|commands <id>` against a `service_exec` command. Under the
      process-global marker, a user typing the *outer* command at a terminal now
      gets `-T` on the inner one — correct by the design's own rules, and
      exactly how an interactive wrapper (`db.cli` → `db.psql`) breaks. If any
      exist, add one as a sixth cell of the manual TTY matrix in tasks 2 and 16
- [ ] enumerate every `type: daemon` command across the local workspaces
- [ ] for each, record whether it declares `workdir` / `workdir_from` / `user`
- [ ] for those declaring none, record whether the target service has
      `cli.workdir`, `work_dir_internal` or `dir_internal` set — those are the
      ones whose cwd changes
- [ ] separately record daemons whose target service has `cli.user` set —
      those change the uid they run as, which changes the ownership of files
      they write
- [ ] write the counts into the Blast radius section of this plan
- [ ] apply the stop condition: if any daemon that **writes files** would
      change uid under the new `cli.user` fallback, do not land that half
      blind — either exclude the `cli.user` fallback from the daemon path in
      commit A (keeping only the workdir chain) or fix the affected service's
      `cli.user`. Record the decision here with a ⚠️ note naming the daemon.
      A changed uid changes the ownership of everything the daemon writes, and
      that is not recoverable by re-running anything.

### Task 2: Capture the "before" snapshot of the container TTY matrix

Commit B is invisible to the scenario harness, so the only evidence that it did
the right thing is a before/after comparison taken by hand. The "before" half
must be captured while the tree is still clean.

**Files:**
- Modify: `docs/plans/20260901-service-exec-runtime-defaults.md` (record the
  observations)

- [ ] pick a live project and a command that needs a terminal (shell, REPL, or
      an interactive installer); if none exists, add a throwaway
      `type: service_exec` command whose `cmd:` prints `tty` and
      `ls -la /proc/self/fd/0 /proc/self/fd/1 /proc/self/fd/2`
- [ ] cell 1 — run it as `dwe cmd <id>` from a real terminal; record the three
      fds and whether output is coloured
- [ ] cell 2 — run it as a `type: command` step inside `dwe deploy run`; record
      the same
- [ ] cell 3 — run it over the host bridge from inside a container; record the
      same
- [ ] cell 4 — run `dwe cmd <id> | cat`; record the same
- [ ] cell 5 — run it as a step of a snapshot workflow (`dwe snapshot …`) from
      a real terminal; record the same. This is the most user-facing of the
      four sites deliberately left at the zero value, and the only one likely
      to be noticed
- [ ] paste the results into this plan under a "TTY matrix — before" heading
      — five cells, or six if task 1 found a wrapper command

### Task 3: Add the shared service-lookup helpers to `project/config`

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/project/config/workspace_test.go`

- [ ] add `ServiceByContainer(cfg *DweConfig, container string) (ServiceConfig, bool)`,
      matching on the `Container` field rather than the services map key,
      iterating **sorted** keys so a `Container` collision resolves stably, and
      returning `false` for a nil config or an empty container name
- [ ] add `ContainerWorkdirFallback(cfg *DweConfig, container string) string`
      implementing `cli.workdir → work_dir_internal → dir_internal`
- [ ] document on both helpers why the lookup is by `Container` and not by map
      key (`service: app-main` must resolve to the folder `main`)
- [ ] write tests for `ServiceByContainer`: match by container, the
      container-differs-from-key case, **two services sharing one `Container`
      resolving to the same service on repeated runs**, no match, nil config,
      empty name
- [ ] write tests for `ContainerWorkdirFallback`: each of the three rungs wins
      in turn, all three empty returns `""`, unknown container returns `""`
- [ ] run `make test` — must pass before task 4

### Task 4: Extend `resolveServiceFields` with the workdir chain and the `internal` sentinel

**Files:**
- Modify: `internal/core/usercommands/runtime/runners/service/exec.go`
- Modify: `internal/core/usercommands/runtime/runners/service/service_test.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`

- [ ] **first**, build the fake-docker harness this task and task 13 need in
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
- [ ] note for scope: the repo already has this pattern via the heavier
      `.dwe/config` route (`installFakeDocker`,
      `internal/cli/lifecycle/reset_test.go:360`, and `logs/logs_test.go:85-94`)
      — read it before writing the stub, but no pipeline test loads config from
      disk today, so the PATH seam is the cheaper fit
- [ ] mind `goleak.VerifyTestMain` in package `pipeline` (`main_test.go:10`):
      these stub-docker tests execute a real child through
      `childIO`/`WireChildIO`, so their cleanups must complete inside the test
      or the whole package goes red on a leaked PTY-copy goroutine
- [ ] note also: the rung tests below need **no** stub. `service_test.go`
      never calls a runner's `.Run(` — every test asserts on `BuildCommand`'s
      `*exec.Cmd`. Only the two package-`pipeline` tests (this task's check-path
      test and task 13's executor test) actually execute a child
- [ ] treat a rendered `workdir` (or `runner.workdir`) equal to `internal` as
      the opt-out sentinel: emit no `--workdir` flag and skip the fallback
      entirely, mirroring `model.UserModeInternal`
- [ ] after the existing `workdir_from` → `workdir` resolution, fall back to
      `config.ContainerWorkdirFallback` when the result is still empty
- [ ] replace `lookupServiceCLIUser`'s hand-rolled loop with
      `config.ServiceByContainer`, keeping the existing `cli.user` fallback
      behaviour byte-identical
- [ ] update the `resolveServiceFields` doc comment to state the full
      seven-step chain in order
- [ ] write a table test covering all seven rungs, including: the `internal`
      sentinel at both the top level and inside `runner:`, a service whose
      `Container` differs from its map key, and a service with
      `work_dir_internal` but **no** `cli.workdir` — a configuration that
      exists in no local workspace, which is why unit coverage is the only
      coverage this rung will ever get
- [ ] write tests asserting `service_run` inherits the same chain through the
      shared helper
- [ ] write a test **in package `pipeline`** (`executor_test.go`) covering a
      `service_exec` command reached as a `check:` action — a check goes through
      the same `ExecAction` switch, so the new cwd applies there too, and a
      check whose command uses a relative path silently changes meaning. It
      cannot live in `service_test.go`: that file is package `service`, and
      importing `pipeline` from it is an import cycle. Declare an explicit
      `mode: exec` on the fixture command so this test needs no container
      probe — the probe seam is commit C's problem (task 13), not commit A's.
      Drive it through the stub-docker harness above and assert on the recorded
      argv
- [ ] run `make test` — must pass before task 5

### Task 5: Give `docker_daemon_start` the same workdir chain and `cli.user` fallback

The docs already promise that a daemon's `user` and `workdir` follow
`service_run` semantics. They do not today; this closes a documented-but-false
claim.

**Files:**
- Modify: `internal/core/execution/builtin/containers/daemon_start.go`
- Modify: `internal/core/execution/builtin/containers/daemon_test.go`

- [ ] **first**, extract the resolution into a pure function —
      `resolveDaemonWorkdirUser(cfg, service, user, workdir, workdirFrom) (string, string, error)`
      feeding `startArgsInput`. Today it sits inline in `Run`
      (`daemon_start.go:117-137`), which shells out to docker, and
      `daemon_test.go` only ever exercises the pure `buildStartExtraArgs`.
      Without the extraction this task's two test checkboxes have no surface
- [ ] flip the daemon's `workdir_from` vs `workdir` precedence at l.124 from
      `if workdir == "" && workdirFrom != ""` to the service runner's rule —
      `workdir_from` wins — and align the nil handling (a dot-path resolving to
      nil yields `""` and falls through, rather than hard-erroring). The docs
      at `types.md:702,708` already promise this; today's code contradicts them
- [ ] apply the same `internal` sentinel and the same
      `config.ContainerWorkdirFallback` chain after that resolution
- [ ] add the `cli.user` fallback via `config.ServiceByContainer`, matching the
      service runner's precedence exactly — unless task 1 recorded a
      file-writing daemon whose uid would move, in which case follow the
      decision recorded there
- [ ] write tests for the workdir chain on the daemon path, including the
      sentinel, the container-differs-from-key case, and `workdir_from`
      beating a literal `workdir` (the precedence that just flipped)
- [ ] write tests for the `cli.user` fallback, including an explicit `user:`
      winning over `cli.user` and `user: internal` suppressing both
- [ ] pin uid assertions against `${host.uid}`, never against the host shell's
      `id -u`
- [ ] run `make test` — must pass before task 6

### Task 6: Document the workdir chain and land commit A

Doc edits here touch **only** the workdir sections, so a later revert of
commit B cannot conflict with them.

**Files:**
- Modify: `docs/reference/config/commands/types.md`
- Modify: `docs/i18n/ru/reference/config/commands/types.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [ ] rewrite the workdir-resolution section for the seven-step chain,
      including the `internal` sentinel and its symmetry with `user: internal`
- [ ] replace the mermaid diagram, which currently draws the old two-step fork
- [ ] state explicitly that `workdir: internal` outranks `workdir_from` — for
      that one value it inverts the published "`workdir_from` wins" rule
      (`types.md:708`), and an undocumented inversion is a trap
- [ ] update the daemon field list so it describes what the daemon now actually
      does for `user` and `workdir`
- [ ] state the daemon nil-handling change made in task 5: a `workdir_from`
      dot-path that resolved to nothing used to hard-fail the daemon
      (`daemon_start.go:129-131`) and now falls through to the new chain, so it
      starts in a different directory instead of erroring. An error turning
      into a different-directory success is exactly what users must read about — the existing claim at l.702/708 becomes
      true only after task 5
- [ ] mirror both edits into the RU translation
- [ ] run `make build` so the embedded docs tree is re-synced
- [ ] run `make test && make lint` — must pass
- [ ] commit as commit **A**

### Task 7: Add `RunContext.UserInvoked`

**Files:**
- Modify: `internal/core/usercommands/runtime/spec/runner.go`

- [ ] add the `UserInvoked bool` field with a doc comment stating: what it
      means, that the zero value suppresses the TTY and is therefore the safe
      default for any new entry point, that it gates container TTY allocation
      only, and why `SkipNotify` was not reused despite answering the same
      question today
- [ ] confirm no existing construction site needs changing (the zero value is
      correct everywhere until task 10)
- [ ] no tests in this task: the field carries no behaviour yet. Tasks 8, 10
      and 11 own its coverage — do not write a test that only asserts a struct
      field exists
- [ ] run `make test` — must pass before task 8

### Task 8: Add `runio.WantContainerTTY` and a second terminal probe

**Files:**
- Modify: `internal/core/usercommands/runtime/internal/runio/runio.go`
- Modify: `internal/core/usercommands/runtime/internal/runio/runio_test.go`

- [ ] add an injectable "is this writer/reader a terminal" probe (`*os.File`
      assertion plus `Fd()`; anything else is not a terminal) **alongside**
      `stdoutIsTerminal` — do not repoint the existing seam
- [ ] leave `colorForceActive` (l.67) and `bridgedTTYActive` (l.197) probing
      the process's own `os.Stdout`; both mean the process stream, not
      `rc.Stdout`, and `bridgedTTYActive` is load-bearing for the bridge shape
- [ ] add `WantContainerTTY(rc spec.RunContext) bool` implementing
      `rc.UserInvoked && (bridgedTTYActive(rc) || isTerminal(StdoutOf(rc)) && isTerminal(StdinOrOS(rc)))`,
      resolving through `StdoutOf` / `StdinOrOS` so a nil `Stdout` or `Stdin`
      falls back to the process streams instead of reading as "not a terminal"
- [ ] document on it why the bridge arm short-circuits the terminal probe:
      `bridgedTTYChildIO` fabricates its PTY inside `WireChildIO`, after
      `BuildCommand` has already fixed the argv, so the probe would lie
- [ ] document why the predicate is not a plain terminal probe: the pipeline
      fabricates a PTY in `childIO`, so a probe answers "yes" exactly where the
      change must bite
- [ ] write a table test over `UserInvoked` × bridged-env × terminal/pipe
      stdout × terminal/pipe stdin, plus rows for a nil `Stdout`, a nil
      `Stdin`, and a non-`*os.File` writer such as a `bytes.Buffer`
- [ ] write a test pinning that `bridgedTTYActive` still answers off the
      process stdout, so a later seam cleanup cannot quietly repoint it
- [ ] run `make test` — must pass before task 9

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

- [ ] update task 4's check-path argv assertion for the injected `-T`. It was
      written in commit A and this commit invalidates it, so the fix-up must
      land **here** — a revert of B has to restore it along with everything
      else. (Alternative, if you prefer A to be self-sufficient: have task 4
      assert only the `--workdir` subsequence and skip this checkbox.)
- [ ] write the TTY-flag classifier: exact `-T`, any `-T=<value>`, and any
      argument whose name part is `--no-tty` compared **case-insensitively**.
      The compose flag is lowercase `--no-tty` on both `exec` and `run`, and
      pflag is case-sensitive, so an uppercase-only matcher recognises nothing
      a project can have written
- [ ] run the classifier — and the `detached` probe — over the **effective**
      flag vector for the chosen subcommand, `compose.CommandArgs["exec"|"run"]`
      concatenated with the rendered `compose_args`. Those defaults come from
      `docker.yml`'s `args:` block and are appended first (`exec.go:257-270`),
      so a `--no-tty=false` or `-d` declared there is otherwise invisible
- [ ] in `buildDockerComposeCmd`, immediately after `composeArgs` are appended,
      append `-T` when `!WantContainerTTY(rc)` and the classifier finds no TTY
      flag. Any occurrence hands control to the author regardless of its value,
      so `--no-tty=false` is the deliberate force-a-TTY escape hatch
- [ ] state in a comment that only TTY flags suppress the auto-detect, and that
      `-d` / `--name` / `--rm` stay orthogonal on purpose
- [ ] change the signature to
      `ColorForceEnv(rc spec.RunContext, forceOnSuppressedTTY bool) []string`
      and add the third disjunct to `colorForceActive`:
      `(forceOnSuppressedTTY && isTerminal(rc.Stdout))`. `runio` cannot see
      `compose_args`, so the caller computes the flag — the runner passes
      `ttySuppressed && !detached`. The `!detached` half is load-bearing: `-d`
      is valid on both `exec` and `run`, and a detached child's output never
      reaches `rc.Stdout`, so forcing colour there writes ANSI escapes into the
      Docker logs permanently
- [ ] update **all four** call sites: `service/exec.go:297` passes the computed
      value; `host/host.go:79`, `host/dwe.go:43` and `script/script.go:200`
      pass `false` — a host-side child has no container TTY to suppress, and
      nobody should "helpfully" derive a value for them
- [ ] document in the code that the new disjunct probes **raw `rc.Stdout`**
      while `WantContainerTTY` resolves through `StdoutOf`/`StdinOrOS`. The
      asymmetry is deliberate and load-bearing: it is what keeps a nil-`Stdout`
      internal caller from getting forced colour, and a later "consistency"
      cleanup unifying them would inject ANSI into parsed output
- [ ] write argv tests: `-T` present/absent across `UserInvoked`, bridged env,
      and each classifier form already in `compose_args` — `-T`, `-T=false`,
      `--no-tty`, `--no-tty=false`, `--no-TTY` (case-insensitive match) — plus
      an unrelated flag such as `--name` that must **not** suppress the
      auto-detect
- [ ] write a test proving `compose_args: ["-d"]` still gets `-T` (detach is
      orthogonal) but does **not** get the forced-colour variables, for both
      `service_exec` and `service_run`
- [ ] write tests for the same two flags arriving through `DockerConfig.Args`
      (`args.exec` / `args.run`) rather than `compose_args`: a `--no-tty=false`
      there must suppress the injection, and a `-d` there must suppress the
      forced colour
- [ ] write tests in `host_test.go` and `script_test.go` asserting the Host
      runner, the Dwe runner and the Script runner keep their existing colour
      behaviour after the signature change — `runio_test.go` is in package
      `runio` and cannot import runners that import it, and `service_test.go`
      cannot reach host or script code
- [ ] write argv tests for `service_run` proving it inherits the same behaviour
- [ ] write colour tests: the third disjunct fires for a terminal-like
      `rc.Stdout` with the TTY suppressed, does **not** fire for a piped
      `rc.Stdout` (so `dwe cmd foo | grep` stays uncoloured), and does **not**
      fire for a nil `rc.Stdout` — the row that pins the raw-vs-`StdoutOf`
      asymmetry
- [ ] run `make test` — must pass before task 10

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

- [ ] define the marker constant next to the other `DWE_*` env names and set it
      **process-globally** with `os.Setenv`, not per-spawn: `execShellAction`
      (`executor.go:216-220`) never assigns `cmd.Env` at all, and the set of
      spawn mechanisms is not reliably enumerable
- [ ] in `runCommandByID`, read the marker **before** setting it:
      `UserInvoked = !markerSet`, then `os.Setenv` so everything this command
      spawns is classified as nested
- [ ] set it unconditionally on entry to **`pipeline.ExecAction`**
      (`executor.go:177`) — not in the deprecated `Run` wrapper
      (`executor.go:388`), where it would be a silent no-op, and not only in
      `RunWithOptions` (`executor.go:446`), which `dwe reset step` bypasses by
      calling `ExecAction` directly (`lifecycle/reset.go:744,761`). This one
      place covers `type: shell`, `type: dwe`, `type: host` and `type: script`
      steps on both routes
- [ ] record the deliberate gap: `files_gate` commands and shell `when:`
      predicates evaluated **before the first `ExecAction` of the process** run
      unmarked, so a `dwe cmd` re-entry from one of those is classified
      user-invoked. Later ones are marked — the marker is process-global and
      never cleared, and `evalFilesGate` runs per step (`executor.go:769`) — so
      do not pin a test on "gates are always unmarked". That is accepted —
      the failure mode is today's behaviour (no `-T`), i.e. conservative — but
      it is a decision, not an oversight
- [ ] pin the read predicate as `os.Getenv(marker) != ""`, not `os.LookupEnv` —
      the tests clear the marker with `t.Setenv(marker, "")`, which `LookupEnv`
      would still report as set
- [ ] verify the runners that build an explicit `cmd.Env` still pass it through:
      `DweRunner` (`host/dwe.go:44-56`) and `host.go:81` both start from
      `os.Environ()`, so the marker survives — confirm rather than assume
- [ ] extend the existing `TestStripEnv` (`bridgeclient/client_test.go:461`)
      with a marker row — that is where a strip regression belongs
- [ ] add the marker to the daemon's strip set in `bridgeclient.StripEnv`, so a
      marker set inside a container cannot cross the trust boundary and kill
      the TTY on every bridged command
- [ ] set `UserInvoked = !nestedMarkerSet` in `runCommandByID` — it is
      documented as the single execution path for both `dwe commands <id>` and
      the TUI run flow, so this one assignment covers both, and the marker is
      what keeps a `type: dwe` step from re-entering as a "user" invocation
- [ ] split the re-entry assertion in two, because the child is a **separate
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
- [ ] write the `type: shell`/`DWE_BIN` propagation case explicitly — `DWE_BIN`
      is exported on purpose (`host.go:119`, `script.go:149`) so project code
      can call dwe again, and this is the path a per-spawn marker would miss
- [ ] write a test using `t.Setenv` proving `runCommandByID` reads the marker
      before writing it, so a top-level invocation is not marked nested by its
      own assignment
- [ ] clear the marker with `t.Setenv(marker, "")` in **every** new test that
      touches it: `runCommandByID`'s `os.Setenv` is process-global and never
      cleared, and roughly 30 existing tests in `runbyid_test.go` call it — the
      bridge test asserting `UserInvoked == true` would otherwise fail on test
      order alone. Note that `t.Setenv` forbids `t.Parallel` in that test
- [ ] write a test proving the bridge path does **not** set the marker, so a
      bridged `dwe cmd` stays a user invocation
- [ ] add a comment there stating that the host bridge reaches this same line
      (`bridge/exec.go:56` re-execs `dwe <argv…>` as a plain subprocess), and
      that the predicate must therefore never key off `NonInteractive` — the
      daemon force-sets `DWE_NONINTERACTIVE=1` on every forked `dwe`
- [ ] in the workflow runner, read it as `rc.UserInvoked && !rc.UnderParallel`
      at the sub-step construction site in `step.go`, so a sequential sub-step
      inherits and a `parallel:` sub-step yields `false` — do not stamp a second
      field in `parallel.go:199`
- [ ] write a test asserting a sequential workflow sub-step inherits the
      parent's value in both directions
- [ ] write a test asserting a `parallel:` sub-step gets `false` even when the
      parent is `true`
- [ ] run `make test` — must pass before task 11

### Task 11: Pin that the pipeline never sets `UserInvoked`

The omission in `execCommandAction` is the behaviour change, so it needs a test
that fails if someone "fixes" it later.

**Files:**
- Modify: `internal/core/execution/pipeline/executor_notify_test.go`

- [ ] write a test asserting `execCommandAction` builds a `RunContext` with
      `UserInvoked == false`, sequential and parallel alike, driving the
      `runtime.TestSnapshotRC` seam and sitting next to its existing twin
      `TestExecCommandAction_SetsSkipNotify`
- [ ] name in the test comment why: a `type: command` step must never hand the
      container a terminal, and this is the sole guard
- [ ] run `make test` — must pass before task 12

### Task 12: Document the TTY behaviour and land commit B

Doc edits here touch **only** the `compose_args` / TTY sections.

**Files:**
- Modify: `docs/reference/config/commands/types.md`
- Modify: `docs/i18n/ru/reference/config/commands/types.md`
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md` (any B-specific pointer bullet belongs here, not in C)
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [ ] if a pointer bullet about the TTY contract is warranted in `AGENTS.md`,
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
- [ ] document the B-specific invariants in `packages.md` **in this commit**,
      not in commit C: the `UserInvoked` contract (who sets it, why `false` is
      the safe zero value, why `SkipNotify` was not reused), the
      `DWE_NESTED_RUNTIME` marker and its strip-set requirement, and the paired
      colour-forcing rule with its `!detached` guard. Keeping them here is what
      makes a revert of B remove the prose along with the mechanism
- [ ] rewrite the `compose_args` section: it currently recommends `-T`, which
      the runner now supplies on its own; say what still needs an explicit flag
- [ ] add the TTY rule in prose — a top-level `dwe cmd` on a terminal gets a
      container TTY, everything else (`deploy run`, workflows' parallel blocks,
      `check:` probes, piped output) gets `-T`; an explicit `-T` / `--no-tty`
      (lowercase, as compose spells it) in the effective flags wins; unrelated
      flags do not
- [ ] state that there is no dedicated schema field for forcing a container TTY
      inside a pipeline, and that `compose_args: ["--no-tty=false"]` is the
      explicit, deliberately awkward way to ask — do **not** write that it is
      impossible, because the classifier hands control to any TTY flag
      regardless of its value
- [ ] mirror into the RU translation
- [ ] run `make build`, then `make test && make lint` — must pass
- [ ] commit as commit **B**, as a single self-contained revert unit

### Task 13: Flip the exec mode default

**Files:**
- Modify: `internal/core/usercommands/model/types.go`
- Modify: `internal/core/usercommands/runtime/runners/service/exec.go`
- Modify: `internal/core/usercommands/model/types_test.go`
- Modify: `internal/core/usercommands/runtime/runners/service/service_test.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`

- [ ] **first**, introduce the probe seam this task's tests depend on:
      `isContainerRunning` (`exec.go:325`) is unexported and shells out to
      `docker compose ps`, which is why every existing test in
      `service_test.go` uses `ExecModeExec` or `ExecModeRun` and never the
      default. Add `var containerRunningFn = isContainerRunning`, call through
      it, and restore it with `t.Cleanup` in tests. Without this seam the three
      probe-dependent checkboxes below cannot be written at all
- [ ] set `DefaultExecMode = ExecModeExecOrRun` and update its doc comment
- [ ] update the `ExecModeExecOrFail` doc comment: it is no longer the default,
      and it is the only mode that pre-probes for a clean dwe error
- [ ] fix the two stale "default" statements in `exec.go`: the `ExecRunner`
      doc comment (l.26, "exec-or-fail (default): refuses…") and the error
      string at l.59, whose advice to "set `mode: exec-or-run`" inverts once
      that is the default — it should point at `mode: exec-or-fail` as the
      opt-in, or drop the suggestion. The package doc (l.1-6) lists the modes
      but claims no default; do not go hunting for text that is not there
- [ ] update the existing test asserting the old default
- [ ] write a test proving a command with no `mode:` takes the `exec-or-run`
      branch, falls back to an ephemeral run when the container is stopped, and
      emits the warning
- [ ] write a test proving an explicit `mode: exec-or-fail` still refuses with
      the dwe error, so opting back in works
- [ ] write a test pinning that **both** modes select `exec` after a probe
      *error* — that is today's behaviour on both branches
      (`exec.go:57-61` and `exec.go:63-67`), and the flip must not be
      documented as changing it
- [ ] add an executor-level test **in package `pipeline`**
      (`executor_test.go` — `service_test.go` cannot import `pipeline` without
      an import cycle): a step whose `check:` is a `type: command` action
      pointing at a mode-less `service_exec` command, with the service stopped.
      Reuse the stub-docker harness from task 4 — a stub `compose ps` printing
      nothing is what makes "stopped" reachable from package `pipeline`, since
      the `containerRunningFn` seam is unexported and lives in package
      `service`. It must show the new default turning a postcondition into a
      container-creating action, so the consequence is pinned rather than
      discovered in a project
- [ ] keep this test `-T`-agnostic like the rest of task 13 — it runs in the
      pipeline, where `UserInvoked` is false and commit B's `-T` is present
- [ ] inventory the `check:` actions across the local workspaces that reference
      a `service_exec` command without an explicit `mode:`; record the count
      here and add explicit `mode: exec-or-fail` to any check that must not
      create a container
- [ ] **constraint for revert safety**: these tests assert the exec-vs-run
      *branch* and the warning text, never a full argv slice. `service_test.go`
      by now contains commit B's `-T` assertions, and every context here has
      `UserInvoked == false`, so any full-argv assertion written in C would
      hard-code B's output and fail the moment B is reverted — the exact
      failure task 18 exists to catch
- [ ] run `make test` — must pass before task 14

### Task 14: Document the mode default

Doc edits here touch **only** the mode-resolution section.

**Files:**
- Modify: `docs/reference/config/commands/types.md`
- Modify: `docs/i18n/ru/reference/config/commands/types.md`
- Modify: `docs/guides/author-project-commands.md`
- Modify: `docs/i18n/ru/guides/author-project-commands.md`
- Modify: `skills/dwe/references/authoring-commands.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [ ] update the `mode` row in the fields table
- [ ] rewrite the mode-resolution prose — the paragraph beginning "Pick
      `exec-or-fail` (the default)…" now contradicts the shipped default, and
      the guidance inverts: declare `exec-or-fail` for tools that depend on
      persistent container state
- [ ] state that the observable difference is confined to one state — the probe
      succeeds and reports the container stopped. Do **not** claim a
      probe-error consequence: both modes already end at `exec` when the probe
      fails, so an unreachable Docker daemon behaves identically before and
      after
- [ ] state that a `type: command` `check:` referencing a mode-less
      `service_exec` command becomes container-creating, and that such checks
      should declare `mode: exec-or-fail`
- [ ] mirror both files into the RU translations
- [ ] update the `mode: exec-or-run` comment in the authoring reference so it
      no longer implies the value must be written out, and fix line 48 of the
      same file — "Needs `service:` + `mode:` + `workdir_from:`" is wrong on two
      counts after this work: commit A makes `workdir_from` optional and this
      commit makes `mode` optional
- [ ] decide about the `mode: exec-or-run` line in the worked example at
      `docs/reference/config/commands/index.md:143` and its RU mirror (`:145`):
      it is not wrong after the flip, just redundant. Either drop it or state
      here that it stays deliberately, so the next reader does not re-open it
- [ ] run `make build && make test` — must pass before task 15

### Task 15: Record the invariants and land commit C

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `CHANGELOG.md`
- Modify: `AGENTS.md` (A/C pointer bullets only — no B content; see task 12)
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`)

- [ ] document in `packages.md` **only the A and C contracts**: the two new
      `project/config` helpers with the by-`Container` lookup rule and the
      sorted-iteration requirement, and the exec-mode default with its reach
      into `check:` actions. The B contracts already landed in commit B (task
      12) precisely so that reverting B removes them. Commit A's contract
      living here is a deliberate asymmetry: C is the last commit and carries
      no revert requirement, unlike B
- [ ] place this prose in a **separate `§` section** from commit B's block in
      `packages.md`. Both commits edit that one file, and appending adjacent to
      B's text would make `git revert B` conflict there too — contradicting
      task 18's "nothing beyond the three touch-ups"
- [ ] add **one** `## [Unreleased]` CHANGELOG entry, prefixed `**Breaking:**`
      per the existing convention in that file, covering all three changes as a
      single aggregate behaviour change: the workdir chain, the TTY rule and
      the mode default. Do **not** write a probe-error clause — there is no
      probe-error behaviour change, and task 14 spells out why
- [ ] state in the entry that the `workdir: internal` sentinel means a service
      command whose container workdir is literally the relative path `internal`
      changes behaviour — the one exception to this work's
      already-explicit-declarations-are-unchanged promise
- [ ] note inside that entry that the TTY half lands as its own commit and that
      reverting it requires editing this entry
- [ ] state in the entry that a full forced redeploy is needed and that the
      deployment hash will not signal it
- [ ] add at most a pointer bullet to `AGENTS.md` for the A and C contracts —
      the write-up itself stays in `packages.md`, and anything B-specific
      already landed in task 12. Together with that bullet you have 1 774 B of
      headroom against `agentsMdBudget`; keep this one under 800 B, respect
      `agentsMdMaxLineLen = 600` (`agentsmd_test.go:33`), and make its
      `§ <target>` resolve to C's own `packages.md` section so
      `TestAgentsMdPointersResolve` stays green. Place it **non-adjacent** to
      commit B's bullet, for the same revert reason as the `packages.md` rule
- [ ] run `make build && make test && make lint` — must pass
- [ ] commit as commit **C**

### Task 16: Capture the "after" TTY matrix and compare

**Files:**
- Modify: `docs/plans/20260901-service-exec-runtime-defaults.md`

- [ ] re-run every cell captured in task 2 against the built binary (five, or
      six if a wrapper command was found)
- [ ] cell 1 — `dwe cmd <id>` from a real terminal: expect `/dev/pts` on all
      three fds, unchanged from the "before" snapshot
- [ ] cell 2 — the same command as a `type: command` step inside
      `dwe deploy run`: expect a pipe **and colour still present**; this pair is
      the entire point of the colour change, and a grey result is a failure,
      not a cosmetic difference
- [ ] cell 3 — the same command over the host bridge from inside a container:
      expect `/dev/pts`, unchanged from the "before" snapshot
- [ ] cell 4 — `dwe cmd <id> | cat`: expect a pipe and no colour
- [ ] cell 5 — a snapshot workflow step from a real terminal: expect a pipe.
      This one **moves** relative to the "before" snapshot and is expected to;
      confirm the output is still readable and coloured
- [ ] paste the results under a "TTY matrix — after" heading and mark any cell
      that moved unexpectedly with ⚠️

### Task 17: Re-run the per-workspace scenario baseline

**Files:**
- Modify: `docs/plans/20260901-service-exec-runtime-defaults.md`

- [ ] run the existing scenario suite in each of the five workspaces that
      already have a green baseline
- [ ] for the mode change, exercise it while the target container is
      **stopped** — that is the only state in which the mode is observable
- [ ] record which workspaces are green and note that only the mode change is
      observable here, so a green run says nothing about the TTY or workdir
      halves
- [ ] investigate and fix any regression before proceeding

### Task 18: Verify acceptance criteria

- [ ] verify all seven workdir rungs behave as described in Solution Overview,
      including the `internal` sentinel on both the service and daemon paths
- [ ] verify a command that declares an explicit `mode:`, an explicit
      `workdir:` **and** a TTY flag in its effective compose flags sees
      byte-identical argv before and after — that is the precise compatibility
      promise; a `compose_args` list without a TTY flag is not covered by it
- [ ] rehearse the full revert of commit B on a scratch branch:
      `git revert <B>` → resolve the one mechanical conflict in
      `content_hashes_gen.go` by running `make build` → drop the TTY clause
      from the CHANGELOG entry → `make test`. Nothing beyond those three
      touch-ups may be needed; if C's tests fail, the argv constraint in task
      13 was violated. Note `executor_test.go` is now touched by all three
      commits (task 4, task 9's fix-up, task 13), so keep C's executor test in a
      separate hunk from B's fix-up — same non-adjacency rule as `packages.md`
      and `AGENTS.md`, or the revert picks up a fourth conflict
- [ ] on that same scratch branch, grep `docs/internals/packages.md` and
      `AGENTS.md` for `UserInvoked`, `DWE_NESTED_RUNTIME`, `WantContainerTTY`,
      `no-tty`, `TTY` and the colour-forcing rule — the revert must have taken
      all of that prose with it, including any pointer bullet whose wording does
      not contain a symbol name. A green build with orphaned guidance still
      counts as a failed revert
- [ ] verify no deployment hash changed — confirm a `type: command` step still
      reports `already up-to-date` after the upgrade, which is what makes the
      forced redeploy necessary
- [ ] run the full suite: `make test-race`
- [ ] run `make lint`

### Task 19: [Final] Update documentation

- [ ] re-read the changed doc sections end to end for internal contradictions,
      especially anywhere `-T` or `exec-or-fail` is still described as advice
- [ ] confirm `make build` was run last, so `internal/core/docs/embedded/` is
      not stale in the built binary
- [ ] draft the upgrade-guide paragraph (why a full forced redeploy is needed,
      why the deployment hash will not show it) and leave it in this plan for
      the work that owns that page — do not create the page here
- [ ] move this plan to `docs/plans/completed/`

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
- **Redundant-declaration cleanup.** Roughly 500 lines of now-redundant `mode:`
  and `compose_args: ["-T"]` across the workspaces are a separate later pass.
  `compose_args: ["-T"]` must be swept last, and only where a scenario covers
  the command — losing a TTY on an interactive bridged command is a silent
  failure.

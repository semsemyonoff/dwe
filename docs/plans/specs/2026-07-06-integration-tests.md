# Integration tests (`dwe test`)

Status: specification (pre-planning). Each stage below is scoped to become its own
implementation plan via `/planning:make`. This document defines direction, goals, target
architecture, and staged breakdown — not the per-stage implementation detail.

## 1. Context

A dwe project's deploy pipeline is only ever exercised against the developer's one and
only working environment. There is no way to answer "does a clean deploy still work with
these config changes?" without tearing down (or risking) the environment you work in.
The task asks for integration tests that:

- run the full deploy in an **isolated environment** with configurable parameters and
  verify it succeeds and nothing is broken;
- never collide with the working environment — with one deliberate exception: shared
  package-cache volumes (composer, npm, …) are reused;
- can also exercise individual project commands (e.g. "create a DB dump, verify the file
  appears and is named correctly") — deploy is the priority, command tests second;
- are described in a **universal, declarative way** so the mechanism works for any
  project and any stack.

### What the codebase already provides

The isolation and execution building blocks largely exist:

- **Compose project name** fully partitions containers, networks, and non-shared volumes.
  Single derivation point: `config.ResolveComposeProjectName` (`internal/core/project/config/docker.go`),
  fed by `project.name`/`project.prefix` (or a `docker.yml` `project_name:` template).
  Container lookup is label-based (`com.docker.compose.project`), never name-guessed.
- **All filesystem state is project-local** — journal, locks, generated store,
  prompt cache, logs, bridge files all live under `<root>/.dwe/`; `.env` sits next to
  `workspace.yml`. A copied tree is fully independent on the filesystem.
- **Per-tree overrides** flow through the `local.yml` layer (vars, ports, enabled
  services, project prefix). There is no per-run `--set`/env override for deploy — the
  generated-`local.yml`-in-a-copy approach is the supported path.
- **Pipeline engine** (`internal/core/execution/pipeline`, entry `RunWithOptions`) with
  step types `shell` / `command` / `dwe` / `builtin`, `when:` conditions, and a builtin
  registry that already includes assertion-shaped predicates: `tcp_reachable`,
  `containers_running`, `docker_wait_healthy`, `file_exists`, `shell`,
  `env_keys_present`. Caveat: `KindPredicate` builtins are currently legal only in
  `check:`/validate contexts (`kindAllowed`, `internal/core/execution/builtin/builtin.go`),
  not as step bodies — this feature relaxes that (see §4). There is **no HTTP check
  builtin** today.
- **User commands** are runnable programmatically (`usercommands.LoadRegistryFromConfigPath`
  → `BuildRunContext` → `RunCommand`); pipeline `type: command` steps already resolve
  commands by ID through the registry, including `private` ones (`Registry.Get` does not
  filter them). `hide` commands are different: the pipeline executor and the runtime
  skip them — test-only commands therefore use `private`, not `hide`.
- **Preflight** already detects host-port conflicts (`ports_free`) and distinguishes
  "our own compose project" from foreign ones.

What is genuinely missing: an orchestrator that produces an isolated copy, rewrites the
identity/ports layer, drives a real deploy in it, runs a declarative scenario against it,
and reliably tears everything down.

## 2. Goals

- A new `dwe test` command family: run declarative integration-test **scenarios** defined
  in the project, each in a fresh, fully isolated environment.
- Scenario = one YAML file under `workspace/tests/` = one isolated environment = one
  clean-slate deploy with that scenario's parameters (vars overrides, enabled/disabled
  services), followed by ordered assertion/command steps.
- Test **exactly the real user path**: the deploy inside the copy is a real
  `dwe deploy run` subprocess (preflight, locks, journal, config render — everything as
  in production use), not a re-composed in-process approximation.
- Assertions reuse the existing pipeline step model. Two engine additions, both general
  (not test-only): predicate builtins become legal step bodies in **every** pipeline
  (predicate-as-assertion: `false` fails the step), and a new `http_check` builtin.
- Deterministic teardown by default (containers, volumes, copy directory), `--keep` to
  preserve the environment for debugging, `dwe test clean` to sweep leftovers.
- Command tests come for free: scenario steps of `type: command` run the project's user
  commands — including `private` ones, so projects can keep test-only commands out of
  the main flow (`hide` commands stay skipped by pipelines, per the existing contract).

### Non-goals

- Not a unit-test framework for application code — this tests the *environment*
  (deploy, orchestration, project commands), not the app's own test suite (which a
  scenario may still invoke as a step if desired).
- No isolated Docker daemon (no DinD). Isolation is by compose project name + ports +
  volume naming on the shared daemon; image and package caches are deliberately shared.
- No cross-scenario state reuse or incremental re-deploys: every scenario starts from a
  clean copy. (Parallel scenario execution is a later stage; the model allows it.)
- No snapshot/restore integration in the first iteration.
- Isolation covers dwe-managed state (files, containers, volumes, networks, ports).
  Arbitrary host-side side effects of a project's own deploy steps (e.g. a step that
  edits global host files) cannot be sandboxed and are documented as out of scope.

## 3. UX

```
dwe test run [scenario...]   # all scenarios, or the named ones; sequential
    --keep                   # skip teardown; print project name, copy path, cleanup hint
    --timeout <dur>          # override scenario timeout
    --output json | --pretty # machine-readable report (cmdctx.WriteData)
dwe test list                # scenarios with descriptions (+ JSON mode)
dwe test clean               # destroy orphaned test environments (manifest-driven)
```

- **Exit codes**: `0` all scenarios passed; `1` at least one scenario failed;
  `2` configuration/environment error (scenario could not even be prepared).
- **Locks**: `dwe test` does **not** take the original project's project locks — it
  never mutates the working environment. The copy acquires its own `.dwe/` locks via the
  subprocess deploy. This is a deliberate, documented deviation from the lifecycle-command
  pattern (`AcquireProjectLocksOrReport`). Concurrency within one tree is guarded
  **atomically** by a test-private per-scenario flock —
  `.dwe/tests/locks/<scenario>.lock` via the existing `lock.Acquire` (same pattern as the
  bridge daemon's private pidfile flock; never `AcquireProjectLocks`): a second
  simultaneous run of the same scenario fails fast with a held-lock error instead of
  racing on the copy directory.
- **Output**: the standard live pipeline reporter per scenario (same look as deploy),
  then a summary line: `2 passed, 1 failed (redis-off: step "app answers")`. JSON mode
  silences live output per the usual contract.
- **Non-interactive by construction**: the deploy subprocess runs as
  `dwe deploy run --silent` with `DWE_NONINTERACTIVE=1` (`--silent` because desktop
  notifications are installed regardless of interactivity otherwise). Environment
  hygiene is **process-wide**: `dwe test` scrubs its *own* process environment at
  startup — the entire `COMPOSE_*` prefix is stripped; `DOCKER_*` is deliberately
  preserved (the user's daemon/context selection must apply). This is *not* the bridge
  daemon's dangerous-env strip set (`PATH`/`LD_*`/… — that set hardens a
  container→host trust boundary; here there is none). The scrub happens exactly once,
  before any goroutines, live UI, or subprocesses start — never mid-run. Scrubbing once
  at the source covers everything downstream from a single point: the deploy subprocess
  *and* scenario-step subprocesses (`type: shell`/`type: dwe` steps inherit the
  runner's environment verbatim — the pipeline executor leaves `exec.Cmd.Env` nil /
  starts from `os.Environ()`). Command `confirmation:` gates are suppressed (auto-yes)
  in scenario steps — the environment is disposable.

## 4. Scenario schema

`workspace/tests/<scenario>.yml`. File name = scenario name (no `name:` field —
symmetric with `workspace/services/<name>/`). Scenario names must normalise to valid
compose project-name fragments (lowercase alphanumerics, `-`, `_`); the loader rejects
names that don't. Strict decode (`KnownFields(true)`), matching the pipeline-loader
surface.

```yaml
description: "Deploy with redis disabled"

env:                        # how this environment differs from the defaults
  services:
    disable: [redis]        # and/or enable: [...]
  vars:
    app.http_port: auto     # auto = allocate a free host port before deploy
    db.port: auto
    db.password: "test-pw"  # any vars override; lands in the copy's local.yml

timeout: 15m                # wall-clock budget for the whole scenario

steps:                      # ordered, run AFTER the implicit deploy; deploy.yml step format
  - name: "app answers"
    type: builtin
    cmd: http_check
    with: { url: "http://localhost:${vars.app.http_port}/up", status: 200, contains: "OK" }

  - name: "create dump"
    type: command           # regular user command; private commands allowed
    cmd: db:dump

  - name: "dump exists"
    type: builtin
    cmd: file_exists
    with: { path: "dumps/db-latest.sql.gz" }
```

Decisions:

- **Deploy is implicit and always first.** The runner executes `dwe deploy run` in the
  copy before `steps:`. A scenario with no `steps:` is valid and already useful ("deploy
  with these parameters succeeds and containers come up healthy").
- **`steps:` are ordinary pipeline steps** — `shell` / `command` / `dwe` / `builtin`,
  with `when:` — resolved and executed by the existing engine, in the existing
  deploy-step schema (no new step fields: there is no `service:`/`dir:` on steps today
  and tests do not add them; in-container execution goes through `type: command`, and
  predicate paths resolve against the workdir = copy root). A failing step fails the
  scenario; remaining steps are skipped.
- **Predicate builtins become valid step bodies — everywhere.** Today `KindPredicate`
  builtins (`file_exists`, `tcp_reachable`, `containers_running`, `env_keys_present`,
  the `shell` predicate, …) are rejected as step bodies by `kindAllowed`
  (`internal/core/execution/builtin/builtin.go`). This feature relaxes the engine
  generally: a predicate used as a step body is evaluated as an **assertion** — `false`
  fails the step with the predicate's message. Applies to all pipelines (deploy / reset /
  lifecycle / tests); no existing config changes meaning. Two coupled contracts ship
  with it: (a) **always-run exemption** — a predicate-body step is never skipped by
  deploy's *state/up-to-date gates* (`when:` conditions and `files_gate` keep their own
  skip semantics — a conditional assertion stays conditional). Exactly two such gates
  exist (verified: lifecycle run/restart/reset pass no `SkipDecider`; `deploy step` is
  gone): the per-step journal decision (the `hasCheck → Run` lever in
  `workflow/deploy/journal/decision.go`) and deploy's outer "already up-to-date" early
  gate (`internal/cli/deploy/deploy.go`), which today scans only for
  `check:`/`files_gate` steps and would otherwise skip the whole pipeline. One shared
  "step forces execution" helper (covering `check:` and predicate bodies; one-level
  parallel-substep recursion — deeper nesting is rejected by the schema) feeds both
  sites — an assertion that asserts only on the first deploy is worse than none;
  (b) the matching validator (`internal/core/validate`) and builtin-docs updates.
- **Step template rendering is a scenario-loader concern, not an engine change.** The
  pipeline engine passes builtin `with:` params and shell `cmd:` **verbatim** (only
  individual builtins render their own fields). The scenario loader therefore renders
  `${...}` in `with:`/`cmd:` against the copy's `cfg.Raw` at scenario-resolve time —
  the same `tpl` substrate user commands already use — and builtin validation runs on
  the **rendered** params. Deploy/reset/lifecycle pipelines keep their verbatim
  semantics untouched (rendering them globally would be a breaking change for steps
  that rely on shell-level `${...}`).
- **New builtin `http_check`** (predicate kind — a step body via the relaxation above,
  or a `check:`): `url`, expected `status`, optional `contains:`, plus
  `retries`/`interval`/`timeout` — services often need a few seconds after `up` before
  answering. Complements `tcp_reachable` for web stacks.
- **`env.vars: auto`** is the only magic value: before deploy, the runner allocates a
  free host port and writes the concrete number into the copy's `local.yml`. Scenario
  steps see it through the loader-side rendering above: `${vars.app.http_port}` resolves
  to the allocated value. **Prerequisite (documented prominently):** `auto` can only
  rewrite ports that the project routes through vars (`${...}` in compose files / dwe
  `ports:` fed from vars). A project with literal host ports cannot be tested alongside
  its running working environment — the copy's preflight `ports_free` fails fast with
  guidance to move ports onto vars.
- **Paths resolve against the copy** (`file_exists` and friends run with workdir = copy
  root) — assertions inspect the test environment's artifacts, never the original tree.

## 5. Isolation model

**Tree copy.** The runner copies the project into `<root>/.dwe/tests/runs/<scenario>/`.
In-project placement is deliberate: the path is guaranteed to be inside Docker
Desktop's/OrbStack's file-sharing scope for bind mounts, because the working environment
already runs from there. Copy contents: git-aware selection — tracked + untracked but not
ignored (`git ls-files -co --exclude-standard`); always excluding `.dwe/`, `.env`,
`.git/`. Paths listed by git but absent from the worktree (uncommitted deletions) are
skipped — the worktree state wins, so a locally deleted file is deleted in the test too.
Local workspace-config edits are therefore part of the test, while cloned
service sources, `node_modules`, and other ignored artifacts are not — the deploy inside
the copy recreates them from scratch, which is exactly the "deploy as a new developer
would" property. The developer's `workspace/local.yml` is conventionally gitignored and
thus NOT copied — it is instead **seeded** into the generated `local.yml` (below), so
values that only exist locally (required vars, locally enabled services) still reach the
test deploy. Without git, fall back to a full copy with the same exclusions plus a
warning. Copies never nest (`.dwe/` is excluded from copying).

**Compose project name.** The test identity is `<prefix>-t-<scenario>-<runid>` — a short
run id (e.g. 6 hex chars) is part of the name because the scenario name alone is not a
uniqueness boundary: two clones of the same project on one Docker daemon would otherwise
collide. The runner writes the identity into **two** generated files in the copy:

- `workspace/docker.local.yml` with an explicit `project_name:` — the authoritative
  override (it merges *after* `docker.yml`, so it wins over any custom `project_name:`
  template the project carries). Caveat handled explicitly: `LoadDockerConfig` never
  reads the local layer when `docker.yml` itself is absent — if the copy has no
  `docker.yml`, the runner generates `docker.yml` (with the `project_name:`) instead,
  and that generated file must be **semantics-neutral**: explicit empty `args` for
  every compose command — `LoadDockerConfig` applies `applyCommandDefaults` only to
  *absent* keys, an explicit `[]` opts out (pinned by existing tests), so this exactly
  preserves missing-file behaviour. In this branch the generated `docker.yml` must also
  be the **only** docker policy file in the copy: a stray copied
  `workspace/docker.local.yml` (inert in the original, where no base file existed)
  would suddenly activate and apply its args/`process_env`/volumes — the runner
  removes/overrides it. In the normal branch the generated `docker.local.yml` replaces
  any copied one;
- `local.yml` — **seeded from the original tree's `local.yml`** (it is gitignored, so
  the copy doesn't carry it), then overlaid with the scenario's `env:` (vars, service
  toggles), then the test identity (`project.prefix`) and `update: { mode: off }` (no
  self-update prompts inside test runs). Precedence: original local values < scenario
  `env:` < test identity. Two seed exclusions: `compose.extra` and
  `services.<name>.compose.extra` are **stripped** from the seed (with a warning) —
  they typically reference gitignored overlay files the copy does not contain, so
  keeping them would fail the copy's config load; a fresh-developer deploy has no local
  compose overlays anyway. Scope of that guarantee: these are the only *schema-known*
  `local.yml` file lists that `LoadConfig` path-validates — arbitrary path-valued
  `vars:` can still point at files the copy skipped (residual; surfaces as a normal
  step failure, not a config-load failure). Seeded port-feeding vars still point at the
  working environment's ports — scenarios are expected to set those to `auto`
  (preflight fail-fast otherwise, per §4).

Containers, networks, and non-shared volumes (`<projectName>_<volume>`) partition
automatically. Names are normalised to the compose project-name charset. Cleanup
targeting is **exact-identity via run manifests only** — never name-pattern matching
against the `com.docker.compose.project` label (the codebase invariant "label-based,
never name-guessed" extends to: never *pattern*-guessed either; a foreign project whose
name happens to match the test pattern must be untouchable). Caveat (documented +
stage-3 preflight): resources with explicit names in raw compose files —
`container_name:`, named networks/volumes, `external: true` — bypass project-name
scoping entirely.

**Ports.** `auto` vars per §4. The copy's preflight `ports_free` check stays enabled and
catches allocation races (see §9); on a port conflict at deploy time the runner retries
once with freshly allocated ports. Scope caveat: `ports_free` only sees ports declared
in dwe service config (`services.<name>.ports`) — host ports hardcoded in raw compose
files bypass both `auto` and the check (documented; stage-3 isolation preflight warns).

**`shared: true` volumes** resolve to their verbatim names and are reused as-is — this is
the package-cache exception the task requires. Documented caveat: a `shared` volume
holding real data (not a cache) is visible to tests.

**State.** Journal, locks, generated store, prompt cache, logs: all under the copy's
`.dwe/`, disjoint from the original. `.env` is regenerated inside the copy by the deploy
itself. Shared by design (documented): the Docker daemon with its image/build caches,
and `~/.config/dwe` (user-level preferences and binary overrides apply to test runs too
— "all state is project-local" holds for *runtime* state only).

## 6. Execution flow (per scenario)

1. **Prepare** — acquire the per-scenario flock (`.dwe/tests/locks/<scenario>.lock`,
   §3 — atomic guard against a concurrent run of the same scenario); validate the
   scenario file; git-aware copy; generate the copy's `local.yml` (seeded from the
   original, then scenario `env:`, then test identity + `update: off`) and the docker
   identity file (`docker.local.yml`, or `docker.yml` when the project has none);
   write a **durable run manifest** into the *original* project
   (`.dwe/tests/manifests/<scenario>-<runid>.yml`: compose project name, copy path,
   bridge dir, report path) *before* anything touches Docker — this is what lets
   `dwe test clean` sweep a run whose process died mid-way; run `dwe validate` in the
   copy as a cheap fail-fast before the expensive deploy.
2. **Deploy** — subprocess `dwe deploy run --silent` with cwd = copy,
   `DWE_NONINTERACTIVE=1`, scrubbed environment (§3). Output streams to the run log;
   the console shows a step-status line.
3. **Steps** — in-process `pipeline.RunWithOptions` against the copy's config: workdir =
   copy, the copy's command registry, no journal-backed `SkipDecider` (test steps always
   run; deploy's skip/decide logic does not apply).
4. **Teardown** — deferred; runs on success, failure, and Ctrl+C, driven by the run
   manifest: `docker compose down --remove-orphans` (**no `-v`** — with `-v`, a raw
   compose file that references a `shared: true` cache volume as a non-external named
   volume would get the user's shared cache deleted, violating the headline
   requirement), then remove the test project's volumes with the
   `docker_remove_project_volumes` semantics — prefix-filtered by the test project
   name, `shared:` volumes survive, exactly as in `dwe reset`. Compose down needs the
   copy's compose file set, hence it runs *before* the copy is removed (the manifest
   records everything needed). Then: reap remaining project-labelled containers
   (daemons), stop any host-side bridge daemon started from the copy (SIGTERM by the
   copy's `.dwe/bridge/daemon.pid`), remove the copy directory, delete the manifest,
   release the scenario flock. `--keep` skips teardown, keeps the manifest (so
   `dwe test clean` can find the run later), and prints the project name, copy path,
   and a cleanup hint.
5. **Report** — per scenario: passed/failed, failing step, duration. On failure, before
   teardown, artifacts are collected into the **original** project under
   `.dwe/tests/reports/<scenario>/`: the copy's deploy pipeline log, `docker compose ps`
   output, and container log tails. The environment is destroyed but the debugging
   material survives (and is CI-artifact friendly).

**Timeout** — one deadline context per scenario; on expiry the current step/subprocess is
killed, the scenario is marked failed, teardown still runs.

**JSON mode** — final object
`{scenarios: [{name, status, failed_step, duration, report_dir}], summary}` via
`cmdctx.WriteData`.

## 7. Stages

Each stage is independently plannable via `/planning:make`. Stage 1 alone covers the
task's priority (deploy tests) and its second ask (command tests, via `type: command`
steps).

| # | Stage | Depends on | Summary |
|---|-------|-----------|---------|
| 1 | MVP | — | **Engine prerequisite:** predicate-builtins-as-step-bodies relaxation (`kindAllowed` + the shared always-run helper wired into both the journal decision and deploy's early gate + validate mirror + builtin docs) and the `http_check` builtin — general engine changes, land first. Then: `dwe test run` / `dwe test list`; scenario loader (strict, name normalisation, loader-side `${...}` rendering of step `with:`/`cmd:`); per-scenario flock; git-aware copy (worktree-absent paths skipped); generated `local.yml` (seeded from original minus `compose.extra` + scenario `env:` + identity, `auto` port allocation, `update: off`) + docker identity file (`docker.local.yml`, or semantics-neutral `docker.yml` when absent); run manifest; process-wide env scrub; implicit subprocess deploy (`--silent`); in-process `steps:` execution on the pipeline engine (incl. private commands, suppressed confirmations); deterministic manifest-driven teardown (no `-v`; prefix-filtered volume removal, shared survive) + `--keep`; per-scenario timeout; live reporter + summary; docs page + i18n. If detailed planning shows this is too big for one plan, split: (1a) engine relaxation + scenario schema/loader + `http_check`, (1b) runner + isolation + CLI. |
| 2 | Reports & cleanup | 1 | Failure artifacts into `.dwe/tests/reports/<scenario>/` (pipeline log, `compose ps`, container log tails); `dwe test clean` — strictly manifest-driven destruction; a `com.docker.compose.project` label scan may only *report* suspicious leftovers, never destroy by name pattern; `--output json` for `run`/`list`/`clean`. |
| 3 | Polish | 1, 2 | Per-step timeouts; parallel scenario execution (ports already disjoint); `workspace/tests/` validator domain in `dwe validate` (schema + step validation, command-reference checks); **compose isolation preflight** — warn/fail on constructs that bypass project-name scoping (`container_name:`, explicitly named networks/volumes, `external: true`, host ports not modelled in dwe `ports`). |

## 8. Cross-cutting concerns (every stage)

- **Strict loader surface** — the scenario loader uses the strict pipeline-loader
  decode (`KnownFields(true)`), with one deliberate carve-out from that family: it does
  **not** inherit EOF-as-absent — an empty/all-comment scenario file is an error, not
  "apply the default" (a scenario has no meaningful default, unlike pipeline configs).
- **JSON output contract** — every read-only surface (`list`) and the `run` report route
  through `cmdctx.WriteData` / typed `cmdctx.Err*`; live output stays silent in JSON mode.
- **Display strings** localised via the i18n store helpers; scenario `description:` is
  user content and stays verbatim.
- **Docs**: new `docs/reference/config/tests.md` (schema) + a task-oriented guide
  (writing deploy tests for an arbitrary stack); `dwe docs` embedding via `make build`.
- **No new top-level `workspace.yml` keys** — scenarios live in their own files; no
  `allowedRootKeys` change. New builtin `http_check` registers as `KindPredicate`; the
  predicate-as-body relaxation updates the builtins reference docs
  (`docs/reference/config/deploy/`) and `docs/internals/packages.md` (§ builtin kinds).
- **Container policy** — `test` is host-only: not added to `bridgeAllowedTopLevel`
  (a container must not be able to spawn host deploys).
- **`docs/internals/packages.md`** gains sections for the new packages
  (`internal/cli/test/`, `internal/core/workflow/envtest/` — final package name decided
  in stage-1 planning) and records the no-original-locks contract.

## 9. Open questions / risks

- **Port allocation TOCTOU** — a freshly allocated free port can be taken between
  allocation and `compose up`. Mitigated by the copy's preflight `ports_free` check plus
  one automatic retry with re-allocated ports; not fully eliminable on a shared host.
- **Copy fidelity** — git submodules, symlinks, and files outside the repo are not
  covered by `git ls-files`; stage 1 documents the limitation (submodule support only if
  cheap; symlinks copied as-is where possible).
- **Copy is not atomic** — `dwe test` takes no lock on the original project, so files
  edited while `git ls-files` + copy run can yield a mixed snapshot. Accepted (a test
  run over a tree being actively edited is inherently racy); documented.
- **`.git/` is excluded from the copy** — deploy or scenario steps that invoke `git`
  against the project root will fail or resolve differently inside the copy. Documented
  limitation; an opt-in `copy: { git: true }` scenario switch is a possible later
  extension if real projects need it.
- **Explicitly named compose resources** — `container_name:`, named networks/volumes,
  and `external: true` in raw compose files bypass project-name scoping and can collide
  with (or worse, attach to) the working environment. Teardown deliberately avoids
  `compose down -v` for this reason (§6) — but a test run *attaching* to a foreign named
  resource is still possible until the stage-3 isolation preflight lands; documented
  from stage 1.
- **Bridge daemon lifecycle** — a deploy in the copy may start a host-side bridge daemon
  whose pid/socket live under the copy's `.dwe/bridge/`. Teardown must stop it before
  removing the copy directory, or the daemon is orphaned. Needs an explicit
  stop-by-pid-file step in teardown.
- **Host side effects of user deploy steps** — arbitrary `shell` deploy steps can touch
  global host state: absolute paths, `..`/`~` paths, bind mounts outside the project,
  writes to shared services. Not sandboxable. Documented as out of scope (§2 non-goals)
  with these concrete examples; projects wanting testability keep deploy steps
  project-scoped.
- **Disk churn & watcher churn** — every run re-copies the tree and re-clones service
  sources inside the copy; copies under `.dwe/tests/runs/` may also be picked up by IDE
  indexers / file watchers / backup tools that don't ignore `.dwe/`. Acceptable for
  correctness-first (documented); if it hurts, later work can add copy-on-write via
  `cp -c`/reflink and a `--run-root` override (the internal API keeps the run root
  pluggable from stage 1).
- **Concurrent same-scenario runs in one tree** — the run id makes Docker-side identity
  unique, but the copy path `.dwe/tests/runs/<scenario>/` is per-scenario: the
  per-scenario flock (§3) makes a second simultaneous run fail fast atomically (a
  manifest-existence check alone would be TOCTOU-racy). Parallelism inside one
  `dwe test run` invocation (stage 3) uses distinct scenarios and is safe.

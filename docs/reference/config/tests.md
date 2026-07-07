# workspace/tests/

Declarative integration-test scenarios (`dwe test`).

## Contents

- [Purpose](#purpose)
- [File layout](#file-layout)
- [Scenario schema](#scenario-schema)
  - [`description`](#description)
  - [`env.services`](#envservices)
  - [`env.vars` and `auto` ports](#envvars-and-auto-ports)
  - [`timeout`](#timeout)
  - [`steps`](#steps)
- [What a scenario run does](#what-a-scenario-run-does)
- [Isolation model](#isolation-model)
- [`.dwe/tests/` layout](#dwetests-layout)
- [Teardown](#teardown)
- [`dwe test run`](#dwe-test-run)
- [`dwe test list`](#dwe-test-list)
- [Exit codes](#exit-codes)
- [JSON output](#json-output)
- [Documented limitations](#documented-limitations)
- [Related commands](#related-commands)

## Purpose

A dwe project's deploy pipeline is normally only ever exercised against the developer's one working environment — there's no way to answer "does a clean deploy still work with these config changes?" without touching (or risking) the environment you work in.

`dwe test` runs the project's deploy pipeline — and any follow-up assertions or project commands you declare — inside a fresh, fully isolated, disposable copy of the project. Each scenario is one YAML file under `workspace/tests/`: one isolated environment, one clean-slate deploy with that scenario's parameters, followed by ordered steps using the same step schema as `deploy.yml`.

Isolation targets dwe-managed state only (compose project, ports, volumes, files under `.dwe/`) — see [Documented limitations](#documented-limitations) for what it doesn't cover.

## File layout

`workspace/tests/<scenario>.yml` — one file per scenario. The scenario **name is the file basename**, not a field (symmetric with `workspace/services/<name>/service.yml`): a file at `workspace/tests/redis-off.yml` defines a scenario named `redis-off`.

Scenario names must already be valid compose-project-name fragments: lowercase alphanumerics, `-`, `_`, no leading separator (`^[a-z0-9][a-z0-9_-]*$`). Names are never sanitised — a non-matching filename is a hard error at discovery time, for every file under `workspace/tests/*.yml`/`*.yaml`.

The loader is strict (`KnownFields(true)`, matching the pipeline-loader family), with one deliberate divergence from that family: an empty or all-comment scenario file is an **error**, not an absent-and-defaulted document — a scenario has no meaningful default.

An absent `workspace/tests/` directory is not an error: `dwe test list` reports no scenarios and `dwe test run` (no args) runs nothing.

## Scenario schema

```yaml
description: "Deploy with redis disabled"

env:
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
    type: command            # regular user command; private commands allowed
    cmd: db:dump

  - name: "dump exists"
    type: builtin
    cmd: file_exists
    with: { path: "dumps/db-latest.sql.gz" }
```

### `description`

Human-readable summary shown verbatim by `dwe test list` — not translated (display strings elsewhere in dwe are localised, but scenario descriptions are user content and stay as written, spec §8).

### `env.services`

```yaml
env:
  services:
    enable: [worker]
    disable: [redis]
```

Force-enables or force-disables named services in the copy, on top of whatever `workspace/local.yml` currently has enabled. Maps to `services.<name>.enabled: true/false` in the generated `local.yml` (below) — same effect as `dwe services enable/disable --apply`, just scoped to the copy.

### `env.vars` and `auto` ports

```yaml
env:
  vars:
    app.http_port: auto
    db.password: "test-pw"
```

Each key is a dot-path relative to `vars.` (`app.http_port` → `vars: { app: { http_port: … } }` in the copy's `local.yml`), overriding whatever the project or the developer's own `local.yml` set.

`auto` is the one magic value: before deploy, the runner allocates a free host port and writes the concrete number in its place. Scenario steps see the allocated value through `${vars.app.http_port}` (loader-side rendering, below).

`auto` is **not** required just to isolate a service's host port: every host port an enabled service declares under `services.<name>.ports` is remapped to a free port automatically (see **Automatic host-port isolation** below). `env.vars: { …: auto }` is for the residual case — a compose file that interpolates a host port from a var the service config does not declare — where you want the runner to allocate and inject that value; the step then reads it via `${vars.<path>}`.

### `timeout`

Wall-clock budget for the whole scenario (deploy + all steps), e.g. `15m`. Parsed with `time.ParseDuration`. Precedence: `dwe test run --timeout` flag (overrides every scenario) > this field > a 30-minute default. On expiry, the in-flight subprocess or step is killed, the scenario is marked failed, and teardown still runs (under its own fresh deadline, not the expired one).

### `steps`

Ordinary pipeline steps, in the same schema `deploy.yml` uses — `type: shell` / `command` / `dwe` / `builtin`, with `when:` — resolved and executed by the same engine. There is no `service:`/`dir:` field on steps (deploy doesn't have one either); a step that needs to run against a service goes through `type: command` and lets the user command own that detail. A scenario with no `steps:` is valid and already useful: "deploy with these parameters succeeds and containers come up healthy."

`type: command` steps dispatch through the project's regular command registry, **including `private` commands** — so a project can define test-only commands (e.g. `db:dump` above) and keep them out of the everyday command listing while still exercising them from a scenario. `hide` commands are different: pipelines skip them per the existing contract, so a test-only command should be `private`, not `hide`.

Any `KindPredicate` builtin (`file_exists`, `tcp_reachable`, `containers_running`, `env_keys_present`, the `shell` predicate, and the `http_check` builtin below) may be used directly as a step body, where it behaves as an **assertion**: `false` fails the step with the predicate's own message, `true` succeeds silently. This is a general engine capability (applies to `deploy.yml`/`reset.yml`/`lifecycle.yml` steps too), not test-only — see the [builtins reference](deploy/builtins.md#predicate-builtins-as-step-bodies-assertion-semantics) for the full mechanics, including the always-run exemption that keeps an assertion from being skipped by deploy's up-to-date gates.

`http_check` (new builtin, predicate kind) complements `tcp_reachable` for web stacks that need a few seconds after `up` before answering:

```yaml
- name: "app answers"
  type: builtin
  cmd: http_check
  with:
    url: "http://localhost:${vars.app.http_port}/up"
    status: 200
    contains: "OK"          # optional body substring
    retries: 10             # optional; default per builtin
    interval: 1s
    timeout: 2s
```

See the [builtins reference](deploy/builtins.md#http_check) for the full parameter list and retry semantics.

**`${...}` rendering is a scenario-loader concern, not an engine change.** The pipeline engine passes builtin `with:` params and shell `cmd:` verbatim — only individual builtins render their own fields. The scenario loader renders `${...}` in step `with:`/`cmd:` against the copy's resolved config **before** the steps run (the same `${...}` substrate user commands and config templates use), so `${vars.app.http_port}` resolves to the concrete allocated port. Paths in builtin params (e.g. `file_exists` `path:`) resolve relative to the copy's root — assertions always inspect the test environment, never the original tree.

A failing step fails the scenario; remaining steps are skipped.

## What a scenario run does

Per scenario, in order:

1. Acquire a per-scenario flock, load and validate the scenario file.
2. Copy the project into an isolated tree.
3. Generate the copy's `local.yml` (seed + this scenario's `env:` + a fresh identity) and a docker identity file, then write a durable run manifest.
4. Run `dwe validate` in the copy (cheap fail-fast).
5. Run `dwe deploy run --silent` in the copy — the real deploy pipeline, not a re-composed approximation.
6. Run `steps:` in-process against the copy's config.
7. Tear everything down (unless `--keep`).

## Isolation model

**Tree copy.** The runner copies the project into `.dwe/tests/runs/<scenario>/` (inside the project root, so it stays within Docker Desktop's/OrbStack's file-sharing scope for bind mounts). Copy selection is git-aware: tracked + untracked-but-not-ignored files (`git ls-files -co --exclude-standard`), always excluding `.dwe/`, `.env`, and `.git/`. A path git lists but that's absent from the worktree (an uncommitted deletion) is skipped — the worktree wins, so a locally deleted file stays deleted in the copy too. Symlinks are recreated as symlinks; permissions are preserved. Without git (or on a git failure), the runner falls back to a full directory copy with the same exclusions, plus a warning that gitignored artifacts are now included.

The developer's own `workspace/local.yml` is gitignored and therefore **not copied** — instead it's **seeded** into the generated `local.yml` (below), so locally-required vars and locally-enabled services still reach the test deploy.

**Compose identity.** Each run gets `<base>-t-<scenario>-<run-id>` as its compose project name (`<base>` = `project.prefix` if set, else `project.name`; `<run-id>` is 6 random hex chars — the scenario name alone isn't a uniqueness boundary across two simultaneous clones of the same project). This partitions containers, networks, and non-shared volumes automatically; container lookup stays label-based (`com.docker.compose.project`), never name-guessed. The identity is written into the copy two ways:

- if the copy has `workspace/docker.yml` → a generated `workspace/docker.local.yml` with just `project_name:` (the local layer always wins);
- if the copy has **no** `workspace/docker.yml` → a generated `workspace/docker.yml` with `project_name:` plus an explicit empty `[]` for every `args:` key, so the generated file stays semantics-neutral (no `docker.yml` means zero-value args config today — an `args:`-less generated file would silently opt every command into its defaults, which would be a behavior change). Any stray copied `docker.local.yml` is removed in this branch, since it would otherwise activate for the first time.

**Generated `local.yml` precedence (low → high):**

1. seed = the original project's `local.yml` (absent file → empty), with `compose.extra` and `services.<name>.compose.extra` **stripped** (each strip warns — those reference gitignored overlay files the copy doesn't contain);
2. this scenario's `env.vars` / `env.services`;
3. identity: `project: { prefix: <compose project name> }` and `update: { mode: "off" }` (no self-update prompts inside a disposable test run).

**Automatic host-port isolation.** Every host port declared under `services.<name>.ports` by a service that will be enabled in the copy is remapped to a freshly allocated free port, written into the generated `local.yml` as a `services.<name>.ports.<x>` override (the original port's scheme is preserved). Because `ports_free` preflight reads that same field — and a project that sources its compose host bindings from `services.<name>.ports` (directly, or via an `exports.env` entry `from: services.<name>.ports.<x>`) binds from it too — the preflight and the actual bind move together, so a scenario runs alongside the working environment with no port config. Any `env.vars: { …: auto }` ports are allocated in the same batch. All ports come from one allocation pass (all listeners opened before any is closed, guaranteeing intra-batch uniqueness); the copy's `ports_free` preflight still catches host-level races, and on a deploy failure with any allocated port present the runner re-allocates every port and retries the deploy **exactly once** before failing the scenario.

**`shared: true` volumes** resolve to their verbatim names and are reused as-is — the deliberate package-cache exception (composer, npm, …). A `shared` volume holding real (non-cache) data is therefore visible to every test run too.

**State.** Journal, locks, generated-value store, prompt cache, logs — everything lives under the copy's own `.dwe/`, disjoint from the original. `.env` is regenerated inside the copy by the deploy itself. The Docker daemon (image/build caches) and `~/.config/dwe` (user-level preferences, binary overrides) are shared by design — "all state is project-local" holds for *runtime* state, not for the daemon or user-level config.

## `.dwe/tests/` layout

All paths below are relative to the **original** project root (never the copy):

| Path | Purpose |
|------|---------|
| `.dwe/tests/runs/<scenario>/` | The disposable copy for a scenario's current/last run |
| `.dwe/tests/locks/<scenario>.lock` | Per-scenario flock (never the project-wide `deploy.lock`/`snapshot.lock`) |
| `.dwe/tests/manifests/<scenario>-<run-id>.yml` | Durable run manifest — written **before** any Docker interaction |
| `.dwe/tests/reports/<scenario>/` | Reserved for stage-2 failure artifacts (not populated yet) |

The manifest (`scenario`, `run_id`, `compose_project`, `copy_path`, `bridge_dir`, `report_dir`, `created_at`) is the sole input to teardown: a run that dies mid-way (crash, `--keep`, a killed process) is still fully describable from its manifest and copy contents alone, without touching the working environment or guessing at names.

## Teardown

Runs by default after every scenario (pass/fail/timeout/Ctrl+C), driven only by the manifest, in order: `docker compose down --remove-orphans` (**never `-v`** — a shared cache volume referenced as a plain named volume in a raw compose file must never be deleted) → reap any remaining containers labelled with the manifest's exact `com.docker.compose.project` value → remove the test project's own volumes (prefix-filtered by compose project name; `shared:` volumes survive, same semantics as `dwe reset`) → stop any bridge daemon the deploy started in the copy → remove the copy directory → delete the manifest → release the flock. Each step is best-effort — a failure is logged and later steps still run.

`--keep` skips every step above, leaves the manifest and copy in place, and prints the compose project name, the copy path, and a cleanup hint. A subsequent `dwe test run` of the **same** scenario name fails fast (a kept run's manifest still exists) rather than silently deleting the kept environment out from under you — clean it up manually, or wait for stage-2 `dwe test clean`.

## `dwe test run`

```
dwe test run [scenario...]
    --keep                    # skip teardown; print project name, copy path, cleanup hint
    --timeout <duration>      # override every scenario's own timeout (e.g. 15m)
```

No arguments runs every scenario under `workspace/tests/*.yml`, in sorted name order. Named arguments run exactly those scenarios (an unknown name fails before anything runs, exit code 2). Scenarios run sequentially. Ctrl+C (SIGINT/SIGTERM) cancels the scenario currently running, tears it down, and skips the rest — already-completed scenarios are still reported.

Output is the standard live pipeline reporter per scenario (the same look as `dwe deploy run`), followed by a summary line, e.g.:

```
2 passed, 1 failed (redis-off: step "app answers")
```

`dwe test` requires a project — unlike read-only docs commands, it is not usable outside one.

## `dwe test list`

```
dwe test list
```

Lists every scenario under `workspace/tests/*.yml` with its `description:`, verbatim. An absent `workspace/tests/` directory lists nothing and is not an error.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Every scenario passed |
| `1` | At least one scenario failed (deploy failure, step failure, or timeout) |
| `2` | A scenario — or the run itself — could not even be prepared (unknown scenario name, scenario load/parse error, flock held by a concurrent run, a kept prior run's manifest still present) |

## JSON output

```
dwe test run --output json
dwe test list --output json
```

```json
{
  "scenarios": [
    {"name": "redis-off", "status": "passed", "failed_step": "", "duration_ms": 4213, "report_dir": ""}
  ],
  "summary": "1 passed, 0 failed"
}
```

`status` is one of `passed`, `failed`, `error` (`error` = the scenario could not be prepared — copy/config/manifest/validate failure; distinct from a deploy or step failure, which is `failed`). `report_dir` is reserved for stage-2 failure artifacts and stays empty in stage 1. As with every other read-only/report surface, live pipeline output and the summary line are silenced in JSON mode — the file log under `.dwe/logs/` still records everything.

## Documented limitations

- **`.git/` is excluded from the copy.** A deploy or scenario step that shells out to `git` against the project root will fail or behave differently inside the copy.
- **Named compose resources bypass isolation.** `container_name:`, explicitly named networks/volumes, and `external: true` in raw compose files ignore the compose project-name scoping and can collide with — or attach to — the working environment. This is why teardown never uses `compose down -v`.
- **Host ports not modelled in `services.<name>.ports` aren't isolated.** The automatic remap and the `ports_free` preflight only see ports declared via `services.<name>.ports`; a host port hardcoded straight in a raw compose file (`8080:8080`) bypasses both. Declare it under `services.<name>.ports`, or route the compose interpolation through a var set with `env.vars: { …: auto }`.
- **Host side effects of a project's own deploy/scenario steps aren't sandboxed.** A `shell` step touching absolute paths, `~`, or bind mounts outside the project affects the real host, same as it would from a real deploy. `dwe test` isolates dwe-managed state (files, containers, volumes, networks, ports) — not arbitrary side effects a step chooses to have.
- **The copy is not atomic.** Nothing locks the original project while `git ls-files` and the copy run; editing files during a test run can produce a mixed snapshot.
- **`~/.config/dwe` and the Docker daemon's image/build caches are shared**, by design (see [Isolation model](#isolation-model)).

## Related commands

- `dwe deploy run` — the real command run inside each scenario's copy
- `dwe validate` — the fail-fast check run before deploy inside each copy
- `dwe reset run` — shares the volume-removal semantics teardown reuses
- [deploy.yml / reset.yml](deploy/index.md) — the step schema `steps:` reuses (types, `with:`, `when:`)
- [Builtins](deploy/builtins.md) — `http_check` and predicate-as-assertion step bodies
- [Conditions and Actions](conditions.md) — typed conditions/actions available to `when:` and step bodies

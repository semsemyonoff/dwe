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
- [Failure reports](#failure-reports)
- [`dwe test run`](#dwe-test-run)
  - [`--parallel N`](#--parallel-n)
- [`dwe test list`](#dwe-test-list)
- [`dwe test clean`](#dwe-test-clean)
- [`dwe validate tests`](#dwe-validate-tests)
- [Compose isolation scanner](#compose-isolation-scanner)
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

A step may also set its own `timeout:` (e.g. `timeout: 5s` on an `http_check` step) — the same general, opt-in engine field documented in [Step fields](deploy/index.md#step-fields); absent or `0` leaves the step unbounded.

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
    retries: 10             # optional; defaults to 0
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
| `.dwe/tests/reports/<scenario>/` | Failure artifacts from the scenario's most recent non-passing, non-`--keep` run (see [Failure reports](#failure-reports)) |

The manifest (`scenario`, `run_id`, `compose_project`, `copy_path`, `bridge_dir`, `report_dir`, `created_at`) is the sole input to teardown: a run that dies mid-way (crash, `--keep`, a killed process) is still fully describable from its manifest and copy contents alone, without touching the working environment or guessing at names.

## Teardown

Runs by default after every scenario (pass/fail/timeout/Ctrl+C), driven only by the manifest, in order: `docker compose down --remove-orphans` (**never `-v`** — a shared cache volume referenced as a plain named volume in a raw compose file must never be deleted) → reap any remaining containers labelled with the manifest's exact `com.docker.compose.project` value → remove the test project's own volumes (prefix-filtered by compose project name; `shared:` volumes survive, same semantics as `dwe reset`) → stop any bridge daemon the deploy started in the copy → remove the copy directory → delete the manifest → release the flock. Each step is best-effort — a failure is logged and later steps still run.

`--keep` skips every step above, leaves the manifest and copy in place, and prints the compose project name, the copy path, and a cleanup hint. A subsequent `dwe test run` of the **same** scenario name fails fast (a kept run's manifest still exists) rather than silently deleting the kept environment out from under you — clean it up manually, or run [`dwe test clean`](#dwe-test-clean).

## Failure reports

When a scenario does **not** pass (deploy failure, step failure, or timeout) and `--keep` was not used, the runner **attempts to collect** a failure report into `.dwe/tests/reports/<scenario>/` **before** teardown destroys the environment — so the debugging material survives. The directory is cleared and rewritten on every non-passing run (the latest failure is what you debug); a passing scenario or a `--keep` run never touches it.

| File | Source |
|------|--------|
| `pipeline.log` | copy of the scenario's pipeline log (`.dwe/logs/test.log` inside the disposable copy) |
| `compose-ps.txt` | `docker compose ps --all` against the copy (`--all`, so a service that crashed or exited during deploy still shows up — the running-only default would drop exactly the service a failure report exists to surface) |
| `container-logs.txt` | `docker compose logs --no-color --tail 200` for the copy's containers, combined into one file |

Collection is best-effort throughout and runs under its own fresh timeout (never the scenario's own, possibly-expired, deadline): a missing pipeline log, a docker command that partially fails, or the collector itself timing out is warned and never changes the scenario's pass/fail outcome. If the copy's docker config can't be loaded (the same condition that can break deploy itself), collection falls back to `docker ps -a` / `docker logs`, filtered by the run's exact `com.docker.compose.project` label, so a report is still produced even when compose is unusable.

The report path is surfaced in `dwe test run`'s text output (a `report: <dir>` line under a non-passing scenario) and in its JSON output as `report_dir` (empty for a passing scenario, a `--keep` run, or when the report directory could not be created).

## `dwe test run`

```
dwe test run [scenario...]
    --keep                    # skip teardown; print project name, copy path, cleanup hint
    --timeout <duration>      # override every scenario's own timeout (e.g. 15m)
    --skip-isolation-check    # downgrade blocking isolation findings to warnings
    --parallel N              # run up to N scenarios concurrently (default 1)
```

No arguments runs every scenario under `workspace/tests/*.yml`, in sorted name order. Named arguments run exactly those scenarios (an unknown name fails before anything runs, exit code 2). By default scenarios run sequentially. Ctrl+C (SIGINT/SIGTERM) cancels the scenario(s) currently running, tears them down, and skips the rest — already-completed scenarios are still reported.

Output is the standard live pipeline reporter per scenario (the same look as `dwe deploy run`), followed by a summary line, e.g.:

```
2 passed, 1 failed (redis-off: step "app answers")
```

`dwe test` requires a project — unlike read-only docs commands, it is not usable outside one.

### `--parallel N`

`--parallel N` (default `1`) runs up to N scenarios concurrently. **Effective parallelism is `min(N, scenario count)`** — `--parallel 8` with two scenarios runs two workers; `--parallel 8` with one scenario runs one. Ordering of the output (text summary and JSON `scenarios` array) is always the original name order, independent of completion order.

- **`--parallel 1` (the default) is byte-identical to today.** When effective parallelism is `1` — the flag is absent, set to `1`, or there are fewer scenarios than requested workers — the sequential streaming path runs unchanged: the standard live pipeline reporter per scenario, exactly as `dwe deploy run` looks.
- **At effective parallelism > 1 the streaming output is replaced by a compact aggregated view.** One sticky row per scenario shows a spinner, the scenario name, a coarse phase (`preparing…`, `validating…`, `deploying…`, `deploy retry…`, `running steps…`, `collecting report…`, `tearing down…`), and an elapsed stopwatch; on completion the row finalizes to `✓ <name> passed` or `✗ <name> failed — step "…"`. A footer tracks `running k/n scenarios…`. The per-scenario deploy/pipeline output is **not** streamed to the terminal — it goes to that copy's own run log only (`.dwe/tests/runs/<scenario>/.dwe/logs/test.log`), and a failing scenario's [failure report](#failure-reports) is collected as usual. Warnings are prefixed `[<scenario>] warning: …` and printed to stderr without disturbing the block.
- **Piped / non-TTY runs** (CI) degrade to flat `scenario <name>: started` / `scenario <name>: <status>` lines per scenario instead of the live block — the summary and exit code are unchanged.
- **JSON mode** (`--output json`) is unaffected by `--parallel`: the payload shape is identical and, as with every read-only surface, live output and warnings are silenced regardless of parallelism.

Exit codes are unchanged (see [Exit codes](#exit-codes)): any scenario that could not be prepared → `2`, else any failed scenario → `1`, else `0`.

**Isolation holds unchanged under parallelism.** Each scenario already runs in its own copy dir, under its own per-scenario flock, with its own per-run-id compose project and manifest, and with every host port auto-remapped to a freshly allocated free port (see [Isolation model](#isolation-model)). Port allocation is additionally process-wide race-safe: a lease set guarantees two concurrent scenarios in the same `dwe test run` never receive the same host port (cross-process races between separate invocations stay covered by each copy's `ports_free` preflight plus the one deploy retry).

**Shared package-cache contention.** Scenarios that reuse the same `shared: true` cache volume (a composer or npm cache, say) can contend when run in parallel — package managers take lock files, and simultaneous cold-cache installs against one volume can slow each other down or trip a manager's own locking. Prefer not to parallelize scenarios that each perform a heavy cold-cache install against a shared cache; scenarios with warm caches or disjoint caches parallelize cleanly. The Docker daemon load of N simultaneous deploys (image pulls, builds, container starts) is your call — pick N to match the host.

## `dwe test list`

```
dwe test list
```

Lists every scenario under `workspace/tests/*.yml` with its `description:`, verbatim. An absent `workspace/tests/` directory lists nothing and is not an error.

## `dwe test clean`

```
dwe test clean [scenario...]
    --dry-run                 # report what would be swept without tearing anything down
```

Removes test environments left behind by `dwe test run --keep` or an interrupted/crashed run. `clean` is strictly **manifest-driven**: it enumerates `.dwe/tests/manifests/*.yml` and reuses the exact same `Teardown` a normal scenario run performs at the end of `dwe test run` — nothing is ever destroyed by guessing a compose project name from a pattern.

With no arguments, every manifested scenario is swept. Passing scenario names restricts the sweep to those. A scenario whose flock (`.dwe/tests/locks/<scenario>.lock`) is currently held by a live `dwe test run` is **skipped**, never torn down — `clean` never contests a run in progress. `--dry-run` reports what would be swept without tearing anything down (each scenario's flock is still acquired-and-released to correctly classify a live run as skipped rather than sweepable).

If a manifest's teardown does not complete cleanly (e.g. a container or volume removal step failed partway through), that entry is reported as **failed**, never as swept — `Teardown` is best-effort and may have removed some resources (even the manifest itself) before hitting the failure, so counting it as swept would hide a real leftover from the next run.

A best-effort, report-only scan additionally lists Docker compose projects matching this project's test-name prefix (`<base>-t-`, the same base `dwe test run` uses) that have **no manifest at all** — these are reported as orphans and are **never** destroyed automatically; remove them by hand once you've confirmed they're safe to drop. If the project's own root config can't be loaded, the orphan scan is skipped (warned) but every manifested environment is still swept — a broken or mid-edit config must not block a recovery sweep.

### JSON output

```
dwe test clean --output json
```

```json
{
  "dry_run": false,
  "swept": [{"scenario": "smoke", "compose_project": "myapp-t-smoke-a1b2c3", "copy_path": ".dwe/tests/runs/smoke"}],
  "skipped": [{"scenario": "redis-off", "compose_project": "myapp-t-redis-off-9f1e2d", "copy_path": ".dwe/tests/runs/redis-off", "reason": "live"}],
  "failed": [],
  "orphans": [{"compose_project": "myapp-t-old-9f8e7d", "note": "no manifest — remove manually"}]
}
```

As with `run`/`list`, live output (per-entry warnings) is silenced on stderr in JSON mode.

## `dwe validate tests`

```
dwe validate tests
```

A `dwe validate` domain (`Domain() == "tests"`) that statically checks every `workspace/tests/*.yml` scenario file without touching Docker — validate-only, **never** wired into preflight. Run it in CI before `dwe test run` to catch scenario authoring mistakes without spinning up a disposable copy.

Per file, in order:

- **load** — `LoadScenario` (strict `KnownFields(true)`, empty file rejected); this also covers **name normalisation** (`ValidateScenarioName` against the file basename), so a bad filename surfaces here.
- **`timeout:`** — `time.ParseDuration` on the scenario's own `timeout:` field; a parse failure is an error.
- **`env.services`** — every `enable`/`disable` entry must name a service that exists in the project's merged config; an unknown name is an error.
- **`steps`** — all steps are rendered and resolved **as one whole phase**, exactly like a real run (`pipeline.ResolvePhaseSteps` over a single synthetic phase) — this catches step schema errors, invalid builtin `with:` params, broken `when:` conditions, and duplicate top-level step names (a per-step check would miss the last one, since uniqueness is a whole-phase invariant). Rendering substitutes any `env.vars` entry whose value is the literal `auto` with a valid placeholder host port, so `${vars.db.port}` renders to a valid int and a `tcp_reachable`/`http_check` step validates normally — a genuinely bad param (e.g. `status: nope`) still errors even next to a templated `url:`. A var populated only **post-deploy** (a `${generated.*}` secret, or a var the deploy itself creates) is absent at validate time and may produce a spurious diagnostic; give it a project-level default to avoid this — the validator sees pre-deploy config, the real run sees post-deploy config.
- **`type: command` steps** — each command ID is looked up in the project's command registry; an unknown ID is an error.
- **compose isolation** — see [Compose isolation scanner](#compose-isolation-scanner) below; findings are emitted once per project as warnings (never errors — the tiered fail/warn policy applies only to `dwe test run`, not to static validation).

## Compose isolation scanner

`dwe test`'s isolation model (above) partitions containers, networks, and non-shared volumes by compose project name — but a handful of raw-compose constructs bypass that scoping entirely and can collide with, or attach to, the working environment. The scanner (`config.ScanComposeIsolation`) parses the project's active compose files (`cfg.ComposeFiles()`) for these constructs and flags them:

| Construct | Kind | Severity |
|-----------|------|----------|
| `container_name:` (any occurrence) | `container_name` | **Blocking** — a literal container name collides directly with the working environment |
| A literal host port (single, e.g. `"8080:80"`, or range, e.g. `"8080-8090:80-90"`) not modelled via `services.<name>.ports` | `raw_host_port` | **Blocking** — bypasses both the automatic port remap and `ports_free` |
| `external: true` volume/network | `external_volume` / `external_network` | Warning — a shared-resource hazard, not a hard collision |
| An explicit `name:` on a volume/network | `named_volume` / `named_network` | Warning — same hazard class as `external:` |

`${...}`-interpolated or env-var host-port tokens and container-port-only entries (random host port) are not flagged — only a literal port number or range. IPv6-bracketed hosts (`[::1]:8080:80`) are out of scope and are not flagged (rare in dev compose). A `container_name:` finding is always emitted regardless of which compose service it's on — mapping a compose service back to a dwe service key isn't reliable, so false positives are cleared with `--skip-isolation-check` rather than suppressed at the source.

The scanner itself has no opinion on severity beyond the intrinsic `Blocking` flag — each caller decides what to do with a finding:

- **`dwe test run`** — runs the scan against the disposable copy right before the `dwe validate` subprocess. Every finding is printed as a warning. A **blocking** finding fails the scenario immediately (teardown still runs; no deploy subprocess is spawned) unless `--skip-isolation-check` is passed, in which case every finding — blocking or not — is a warning only and the run proceeds. See the [ports prerequisite](../guides/integration-tests.md#ports-are-isolated-automatically) for how to avoid a `raw_host_port` finding in the first place: model the port under `services.<name>.ports`.
- **`dwe validate tests`** — emits every finding as a warning, regardless of `Blocking`; static validation never fails a build over an isolation hazard, it only surfaces it early.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Every scenario passed (`run`); sweep completed, including nothing to sweep (`clean` — skipped-live entries and orphans do not affect this) |
| `1` | At least one scenario failed — deploy failure, step failure, or timeout (`run`); at least one manifest's teardown did not complete cleanly (`clean`) |
| `2` | A scenario — or the run itself — could not even be prepared: unknown scenario name, scenario load/parse error, flock held by a concurrent run, a kept prior run's manifest still present (`run`) |

`clean` maps a hard error (e.g. an unreadable manifests directory) to a non-zero exit through the standard CLI error envelope, separately from the `0`/`1` sweep-outcome codes above.

## JSON output (`run` / `list`)

```
dwe test run --output json
dwe test list --output json
```

```json
{
  "scenarios": [
    {"name": "redis-off", "status": "passed", "duration_seconds": 4.213},
    {"name": "cache-on", "status": "failed", "failed_step": "http_check", "duration_seconds": 2.101, "report_dir": ".dwe/tests/reports/cache-on"}
  ],
  "summary": "1 passed, 1 failed"
}
```

`status` is one of `passed`, `failed`, `error` (`error` = the scenario could not be prepared — copy/config/manifest/validate failure; distinct from a deploy or step failure, which is `failed`). `failed_step` and `report_dir` are omitted when empty (a passing scenario has neither). `report_dir` is the [failure report](#failure-reports) directory for a non-passing scenario; omitted for a passing scenario, a `--keep` run, or when collection could not create the report directory. As with every other read-only/report surface, live pipeline output and the summary line are silenced in JSON mode — the file log under `.dwe/logs/` still records everything.

## Documented limitations

- **`.git/` is excluded from the copy.** A deploy or scenario step that shells out to `git` against the project root will fail or behave differently inside the copy.
- **Named compose resources bypass isolation.** `container_name:`, explicitly named networks/volumes, and `external: true` in raw compose files ignore the compose project-name scoping and can collide with — or attach to — the working environment. This is why teardown never uses `compose down -v`. The [compose isolation scanner](#compose-isolation-scanner) detects these constructs and, for `container_name:` and literal host ports, fails the scenario before deploy (downgradeable with `--skip-isolation-check`) — it does not make them safe, it surfaces them before they cause a collision.
- **Host ports not modelled in `services.<name>.ports` aren't isolated.** The automatic remap and the `ports_free` preflight only see ports declared via `services.<name>.ports`; a host port hardcoded straight in a raw compose file (`8080:8080`) bypasses both. Declare it under `services.<name>.ports`, or route the compose interpolation through a var set with `env.vars: { …: auto }`. The isolation scanner flags this as a **blocking** `raw_host_port` finding.
- **Host side effects of a project's own deploy/scenario steps aren't sandboxed.** A `shell` step touching absolute paths, `~`, or bind mounts outside the project affects the real host, same as it would from a real deploy. `dwe test` isolates dwe-managed state (files, containers, volumes, networks, ports) — not arbitrary side effects a step chooses to have.
- **The copy is not atomic.** Nothing locks the original project while `git ls-files` and the copy run; editing files during a test run can produce a mixed snapshot.
- **`~/.config/dwe` and the Docker daemon's image/build caches are shared**, by design (see [Isolation model](#isolation-model)).

## Related commands

- `dwe deploy run` — the real command run inside each scenario's copy
- `dwe validate` — the fail-fast check run before deploy inside each copy
- `dwe validate tests` — static scenario validation without Docker (see [above](#dwe-validate-tests))
- `dwe reset run` — shares the volume-removal semantics teardown reuses
- [deploy.yml / reset.yml](deploy/index.md) — the step schema `steps:` reuses (types, `with:`, `when:`)
- [Builtins](deploy/builtins.md) — `http_check` and predicate-as-assertion step bodies
- [Conditions and Actions](conditions.md) — typed conditions/actions available to `when:` and step bodies

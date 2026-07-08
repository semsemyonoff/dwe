# Writing integration tests

`dwe test` runs your project's deploy pipeline — and whatever assertions or commands you add — inside a fresh, throwaway copy of the project. Nothing you do here touches the environment you're working in. This guide is the **authoring workflow**; the field-by-field schema lives in [`../reference/config/tests.md`](../reference/config/tests.md).

## Ports are isolated automatically

`dwe test` gives each isolated copy its own host ports, so a scenario runs *alongside* your working environment — and any other project holding those ports. Every host port your enabled services declare under `services.<name>.ports` is remapped to a freshly allocated free port in the copy, automatically — you route nothing through vars and write no port config in the scenario. Both the `ports_free` preflight and — for projects that source their compose bindings from `services.<name>.ports` (directly, or via an `exports.env` entry `from: services.<name>.ports.<x>`) — the actual container bind read the port from that same place, so the remap moves them together.

A scenario step that needs a remapped port references it the normal way: `${services.<name>.ports.<x>}`.

The one case this does *not* cover is a host port hardcoded straight in a raw compose file (`8080:8080`) that your dwe service config never models — it bypasses both the remap and the `ports_free` preflight. Either declare it under `services.<name>.ports` so `dwe test` can see and reassign it, or route the compose interpolation through a var and set that var per scenario with `env.vars: { …: auto }` (the runner allocates a free port and writes it into the copy's `vars:`; the step then reads `${vars.<path>}`).

## Your first scenario

Create `workspace/tests/smoke.yml`:

```yaml
description: "Clean deploy comes up healthy"

steps:
  - name: "app answers"
    type: builtin
    cmd: http_check
    with:
      url: "http://localhost:${services.app.ports.http}/health"
      status: 200
```

Run it:

```shell
dwe test run smoke
```

This copies your project into an isolated tree, generates a fresh `local.yml` with freshly allocated free host ports, runs `dwe validate` then a real `dwe deploy run` inside the copy, checks the endpoint, and tears the whole thing down. A scenario with no `steps:` at all is already a useful test — "deploy with these parameters succeeds."

## Testing a variant of your stack

`env:` describes how this scenario's environment differs from your defaults — service toggles and var overrides:

```yaml
description: "Deploy with redis disabled — cache falls back to in-memory"

env:
  services:
    disable: [redis]

steps:
  - name: "app still answers without redis"
    type: builtin
    cmd: http_check
    with:
      url: "http://localhost:${services.app.ports.http}/health"
      status: 200
```

Each scenario file is one isolated environment — write one per meaningful variant (a service disabled, a feature flag var flipped, a different port layout) rather than trying to parametrize a single file.

## Asserting on more than "the port answers"

Steps use the exact same schema as `workspace/deploy.yml`: `type: shell`, `type: dwe`, `type: command`, `type: builtin`, with `when:` conditions. Any predicate builtin — `file_exists`, `tcp_reachable`, `containers_running`, `env_keys_present`, `http_check` — works directly as a step body and behaves as a pass/fail assertion, not just inside `check:`:

```yaml
steps:
  - name: "containers are up"
    type: builtin
    cmd: containers_running
    with: { services: [app, db] }

  - name: "app answers"
    type: builtin
    cmd: http_check
    with: { url: "http://localhost:${vars.app.http_port}/health", status: 200, contains: "ok" }
```

`${...}` in a step's `with:`/`cmd:` resolves against the copy's config before the step runs, and `file_exists`-style paths resolve relative to the copy root — assertions always look at the disposable environment, never your working tree.

## Testing a project command with `type: command`

The second use case from the spec — "create a DB dump, verify the file appears" — is just a `type: command` step calling a regular user command, followed by an assertion:

```yaml
steps:
  - name: "create dump"
    type: command
    cmd: db:dump

  - name: "dump file exists"
    type: builtin
    cmd: file_exists
    with: { path: "dumps/db-latest.sql.gz" }
```

`type: command` steps can call **`private` commands**, so you can keep test-only commands (e.g. a command that seeds fixture data, or dumps to a fixed filename instead of a timestamped one) out of the everyday `dwe commands` listing:

```yaml
# workspace/commands/testing.yml
group: testing
commands:
  - id: db:dump
    private: true
    type: shell
    cmd: "docker compose exec -T db pg_dump -U app app > dumps/db-latest.sql.gz"
```

(`hide` commands are skipped by pipelines entirely, so they don't work here — use `private` for test-only commands you still want runnable from a scenario.)

## Debugging a failing scenario

When a scenario fails (deploy failure, step failure, or timeout), the runner **attempts** to collect a failure report into `.dwe/tests/reports/<scenario>/` — automatically, before teardown runs, so it survives the environment being torn down. Collection is best-effort: if it can't create the report directory the path is left empty, and individual artifacts may be partial or blank when a capture fails — but the run always reports the failure regardless.

```shell
dwe test run smoke   # fails

ls .dwe/tests/reports/smoke/
# pipeline.log        — the scenario's deploy/steps pipeline log
# compose-ps.txt       — docker compose ps --all inside the copy
# container-logs.txt   — combined container logs (last 200 lines each)
```

This is the report you'd attach to a CI failure, or read locally without touching Docker at all. It's overwritten on each non-passing run, so it always reflects the latest failure.

When the report isn't enough and you need to inspect the live environment itself, rerun with `--keep`:

```shell
dwe test run --keep smoke
```

Teardown is skipped (and no failure report is collected, since the environment itself is kept); `dwe test run` prints the compose project name and the copy's path so you can `cd` in, inspect containers with `docker compose -p <project> ps`, or open a shell inside a service. The manifest stays on disk too, so a second `dwe test run smoke` refuses to start until you clean up.

Clean up a kept run — or anything orphaned by a crashed run — with `dwe test clean`:

```shell
dwe test clean --dry-run    # see what would be removed, without touching anything
dwe test clean smoke        # tear down just this scenario's kept/orphaned environment
dwe test clean               # sweep every manifested environment
```

`clean` is manifest-driven and never guesses at a compose project name: it only ever tears down environments it has a manifest for, skips any scenario whose flock is currently held by a live run, and separately (report-only) lists Docker compose projects that look like `dwe test` output but have no manifest — those it flags for you to remove by hand rather than destroying automatically.

## Bounding a slow or hanging step

Any step can carry its own `timeout:` — the same opt-in engine field `deploy.yml` steps have. Useful when a scenario step is a `tcp_reachable`/`http_check` assertion (or a `shell` command) that should fail fast instead of running to the scenario's own overall `timeout:`:

```yaml
steps:
  - name: "app answers quickly"
    type: builtin
    cmd: http_check
    timeout: 5s
    with:
      url: "http://localhost:${services.app.ports.http}/health"
      status: 200
```

Absent or `timeout: 0` leaves the step unbounded. The timeout only bounds a step body that honors Go's `context` cancellation — subprocess steps (`type: shell`/`type: dwe`) and ctx-aware builtins are covered; a step blocked on interactive input is not force-interrupted (irrelevant here, since scenario runs are always non-interactive). See [Step fields](../reference/config/deploy/index.md#step-fields) for the full contract.

## Catching mistakes before you run anything

`dwe validate tests` statically checks every `workspace/tests/*.yml` file — scenario name, `timeout:` parse, `env.services` references, step schema (including builtin `with:` params and `when:` conditions), and `type: command` references — without touching Docker or spinning up a copy. It's cheap enough to run on every CI push, ahead of the much slower `dwe test run`:

```shell
dwe validate tests
```

It also surfaces [compose isolation](#resolving-an-isolation-failure) hazards as warnings, so you can fix them before a `dwe test run` ever blocks on one.

## Resolving an isolation failure

`dwe test run` scans the copy's compose files for constructs that bypass compose's project-name scoping — `container_name:` and literal (non-templated) host ports are **blocking**; `external:`/explicitly-`name:`d volumes and networks are warnings only. A blocking finding fails the scenario before deploy even starts (teardown still runs), with a message naming the offending construct:

```
isolation check failed: container_name "myapp-db" in docker-compose.yml (run with --skip-isolation-check to downgrade to a warning)
```

Fix it at the source when you can:

- **Literal host port** (`8080:8080` in a raw compose file) — move it onto `services.<name>.ports` so `dwe test` can see and remap it automatically (see [Ports are isolated automatically](#ports-are-isolated-automatically) above), or route the compose interpolation through a var set with `env.vars: { …: auto }`.
- **`container_name:`** — drop it; compose already names containers deterministically from the project + service name, and a fixed `container_name:` is what causes the collision in the first place.

When the finding is a false positive, or fixing it isn't practical right now, downgrade every finding to a warning and proceed:

```shell
dwe test run --skip-isolation-check smoke
```

See [Compose isolation scanner](../reference/config/tests.md#compose-isolation-scanner) for the full list of flagged constructs and the fail/warn tiering.

## Running the whole suite

```shell
dwe test run                                # every scenario under workspace/tests/*.yml, sorted
dwe test run smoke db-dump                  # just these two, by name
dwe test list                               # scenario names + descriptions
dwe test run --timeout 5m                   # override every scenario's own timeout: field
dwe test run --skip-isolation-check smoke   # downgrade blocking isolation findings to warnings
```

Exit codes make this CI-friendly out of the box: `0` all passed, `1` at least one failed, `2` a scenario couldn't even be prepared (bad name, held lock, scenario file error). `--output json` gives a machine-readable report of the same result.

## Cross-links

- [`../reference/config/tests.md`](../reference/config/tests.md) — full scenario schema, isolation model, `.dwe/tests/` layout, teardown order, `dwe validate tests`, the compose isolation scanner, exit codes, documented limitations.
- [`../reference/config/deploy/index.md`](../reference/config/deploy/index.md#step-fields) — the full step-field table, including the general `timeout:` field.
- [`../reference/config/deploy/builtins.md`](../reference/config/deploy/builtins.md) — every builtin available to `steps:`, including `http_check` and predicate-as-assertion semantics.
- [`author-project-commands.md`](author-project-commands.md) — authoring the `type: command` steps a scenario can call, including `private` commands.
- [`preflight-checks.md`](preflight-checks.md) — the `ports_free` check that enforces the vars-routed-ports prerequisite.

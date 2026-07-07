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

## Debugging a failing scenario with `--keep`

When a scenario fails and the live output isn't enough, rerun with `--keep`:

```shell
dwe test run --keep smoke
```

Teardown is skipped; `dwe test run` prints the compose project name and the copy's path so you can `cd` in, inspect containers with `docker compose -p <project> ps`, or open a shell inside a service. The manifest stays on disk too, so a second `dwe test run smoke` refuses to start until you clean up manually — this is deliberate, so a kept debugging environment can never be silently deleted out from under you.

## Running the whole suite

```shell
dwe test run                 # every scenario under workspace/tests/*.yml, sorted
dwe test run smoke db-dump   # just these two, by name
dwe test list                # scenario names + descriptions
dwe test run --timeout 5m    # override every scenario's own timeout: field
```

Exit codes make this CI-friendly out of the box: `0` all passed, `1` at least one failed, `2` a scenario couldn't even be prepared (bad name, held lock, scenario file error). `--output json` gives a machine-readable report of the same result.

## Cross-links

- [`../reference/config/tests.md`](../reference/config/tests.md) — full scenario schema, isolation model, `.dwe/tests/` layout, teardown order, exit codes, documented limitations.
- [`../reference/config/deploy/builtins.md`](../reference/config/deploy/builtins.md) — every builtin available to `steps:`, including `http_check` and predicate-as-assertion semantics.
- [`author-project-commands.md`](author-project-commands.md) — authoring the `type: command` steps a scenario can call, including `private` commands.
- [`preflight-checks.md`](preflight-checks.md) — the `ports_free` check that enforces the vars-routed-ports prerequisite.

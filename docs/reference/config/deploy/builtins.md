# Available builtins

Builtins are engine-internal Go functions invoked from a step via `type: builtin`. They run in-process with access to the merged config and the same registry used by `type: builtin` declarative commands.

## Predicate builtins as step bodies (assertion semantics)

A **predicate** builtin — one that answers a yes/no question about the world (`file_exists`, `executable_in_path`, `tcp_reachable`, `http_check`, `containers_running`, `env_keys_present`, `config_keys_present`, and the `shell` builtin) — may be used directly as a step body, not only inside a `check:`/`when:` block. Used as a body, a predicate is an **assertion**:

- A **true** result (the check passes) makes the step succeed.
- A **false** result (the check fails) **fails the step** with the predicate's own message, halting the pipeline like any other step failure. No new error type is introduced — the predicate's explanation becomes the step error.

Predicate-body steps are **always re-run**: deploy's "already up-to-date" gate and per-step action-hash skip never skip a step whose body is a predicate (the same always-run treatment `check:` steps get). An assertion has no meaningful cached result, so it re-evaluates on every deploy.

A `when:` guard still applies as normal — a predicate-body step with a `when:` that evaluates false is skipped without asserting (a conditional assertion stays conditional).

```yaml
- name: assert-app-reachable
  type: builtin
  cmd: http_check
  with:
    url: http://localhost:8080/health
    status: 200
    contains: '"ok"'
    retries: 10
    interval: 2s
```

This capability is purely permissive: predicates that previously were legal only inside `check:`/`when:` are now also legal as bodies, in every pipeline (`deploy.yml`, `reset.yml`, `lifecycle.yml`, and test scenarios) and as `type: builtin` user commands. Existing configs are unaffected.

## Contents

- [Catalogue](#catalogue)
- [`service_dirs_ensure`](#service_dirs_ensure)
- [`service_configs_render`](#service_configs_render)
- [`service_configs_render_check`](#service_configs_render_check)
- [`service_generated_harvest`](#service_generated_harvest)
- [`service_configs_copy`](#service_configs_copy)
- [`service_configs_check`](#service_configs_check)
- [`message`](#message)
- [`confirm`](#confirm)
- [`docker_remove_project_volumes`](#docker_remove_project_volumes)
- [`docker_wait_healthy`](#docker_wait_healthy)
- [`containers_running`](#containers_running)
- [`http_check`](#http_check)
- [`remove_paths`](#remove_paths)
- [Internal engine builtins (not callable from user YAML)](#internal-engine-builtins-not-callable-from-user-yaml)
- [Naming convention](#naming-convention)

## Catalogue

| Builtin | Purpose |
|---------|---------|
| `service_dirs_ensure` | Create service hub directories |
| `service_configs_render` | Render config files from a template pack into the service hub (replays generated values) |
| `service_configs_render_check` | Verify rendered config targets exist; pairing it as a `check:` re-runs the render every deploy |
| `service_generated_harvest` | Harvest the service's `generated:` fields into the generated-value store (write-if-absent) |
| `service_configs_copy` | **⚠️ Deprecated** — copy template config files into the service hub |
| `service_configs_check` | **⚠️ Deprecated** — verify that copied config files exist in the service hub |
| `message` | Print a styled message at info/success/warning/error level |
| `confirm` | Interactive Y/n prompt (skipped under `--yes`) |
| `docker_remove_project_volumes` | Remove all volumes whose name is prefixed with the compose project name |
| `docker_wait_healthy` | Wait for Docker containers to reach healthy state |
| `containers_running` | Fast "is running" check (no polling, no timeout, no healthcheck required) |
| `http_check` | Assert an HTTP endpoint returns an expected status (and optional body substring), with retries |
| `remove_paths` | Delete project-relative paths from the filesystem |

## `service_dirs_ensure`

Creates service hub directories.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service folder name from `workspace/services/<name>/` |
| `mode` | string | `skip` | `skip`, `error`, or `recreate` |

Resolved dir list: `[src]` + `ServiceConfig.Dirs` (from the service's `service.yml`). Each entry must be a non-empty relative path that does not escape the service `dir`.

The `configs/` directory is **not** in this list — `service_configs_copy` creates it lazily when a `configs:` block is declared on the service. If you need it created eagerly (or wiped under `recreate`), add `configs` to `dirs:` explicitly.

Mode behavior:

| Mode | Dir missing | Dir exists | Non-dir at path |
|------|-------------|------------|----------------|
| `skip` | create | no-op | error |
| `error` | create | error | error |
| `recreate` | create | remove + create | error |

Safety: `src` always uses `skip` semantics in `recreate` mode (never removes source code). All other dirs — including `configs` if listed in `dirs:` — are wiped under `recreate`.

## `service_configs_render`

Renders a service's config files from a config template pack (`workspace/templates/config/<pack>/`) into the service hub dir (`svc.Dir`), replaying any harvested `${generated.<name>}` values from the generated-value store (`.dwe/generated.yml`). This is the render-based successor to the deprecated `service_configs_copy`.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key |
| `mode` | string | `replace` | Only `replace` (overwrite) is supported |

Config rendering is **opt-in**: a service with no resolvable config pack is a no-op. Pair this step with a `service_configs_render_check` `check:` so it re-runs every deploy. See [render config](../../render/config.md) for the substrate, pack resolution, and full deploy flow.

```yaml
- name: render-configs
  type: builtin
  cmd: service_configs_render
  with:
    service: main
  check:
    type: builtin
    cmd: service_configs_render_check
    with:
      service: main
```

## `service_configs_render_check`

Verifies that every config-pack render target for the service exists on disk. Its primary purpose is structural: pairing it as the `check:` of a `service_configs_render` step forces that step to re-run on every deploy (the `hasCheck → Run` lever in `journal/decision.go` bypasses the action-hash skip), so template edits and store clears always take effect — exactly mirroring `service_configs_copy` + `service_configs_check`.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key |

A service with no resolvable config pack has nothing to check and is a no-op. Returns an error listing any missing rendered files, which fails the check.

## `service_generated_harvest`

Reads each of the service's declared `generated:` fields from its on-disk file, extracts the value via the field's regex (capture group 1), and **write-if-absent** stores it into the generated-value store (`.dwe/generated.yml`). The store is saved atomically when a new value is written.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key |

"Harvest, not mint": the service's own generator (e.g. `php artisan key:generate`) writes the secret; DWE only reads it back and replays it on later renders. Write-if-absent means a value already in the store is preserved, so a redeploy is a no-op. A service with no `generated:` fields is a no-op. A missing file, a pattern that matches no line, a pattern with no capture group, or a captured empty value are surfaced as errors — never silently skipped. See the [`generated` block](../services/fields.md#generated-block) and [render config](../../render/config.md).

## `service_configs_copy`

> **⚠️ Deprecated.** Superseded by [`service_configs_render`](#service_configs_render) + [`service_generated_harvest`](#service_generated_harvest). It keeps working but `dwe validate` emits a warning and a single runtime deprecation notice fires per copy step. See [render config](../../render/config.md) for the replacement.

Copies template config files from `configs/services/<service>/` into `services/<service>/configs/`. Creates the destination `configs/` directory if it does not exist — this is the canonical path for `configs/` creation (the `service_dirs_ensure` builtin does not create it).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key |
| `mode` | string | `replace` | `default`, `update`, or `replace` |

Mode behaviour:

| Mode | Dest missing | Dest exists |
|------|--------------|-------------|
| `default` | write | no-op |
| `replace` | write | overwrite unconditionally |
| `update` | write | merge new `KEY=VALUE` lines without changing existing keys (env-file aware) |

When the corresponding `configs[]` entry has a `mountpoint`, the builtin also touches an empty file at `<service-dir>/<mountpoint>` so Docker Desktop virtiofs can place a nested file bind mount over it.

## `service_configs_check`

> **⚠️ Deprecated.** The `check:` companion of the deprecated [`service_configs_copy`](#service_configs_copy). For render-based configs use [`service_configs_render_check`](#service_configs_render_check) instead.

Verifies that all template config files declared in the service's `service.yml` exist in the service hub after a `service_configs_copy` step. Use as a `check:` action to assert that configs were successfully deployed.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key |

Returns an error listing any missing files, which causes the check to fail. Typically used as the post-check of a `service_configs_copy` step:

```yaml
- name: copy-configs
  type: builtin
  cmd: service_configs_copy
  with:
    service: main
    mode: replace
  check:
    type: builtin
    cmd: service_configs_check
    with:
      service: main
```

## `message`

Prints a message to deploy output.

| Parameter | Type | Description |
|-----------|------|-------------|
| `level` | string | `info`, `success`, `warning`, or `error` (required) |
| `text` | string | Message text (required); supports [Go template expressions](../../templates.md) against `DweConfig` |

```yaml
- name: done
  type: builtin
  cmd: message
  with:
    level: success
    text: "Deploy of {{ .Project.Name }} completed"
```

## `confirm`

Prompts the user for confirmation before continuing. Skipped when `--yes` flag is set or when the step is being confirmed via `ExecContext.SkipConfirm`. On a TTY uses huh.Confirm; on a piped/CI stdin falls back to plain Y/n.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `message` | string | `Are you sure?` | Prompt text |
| `ok_msg` | string | `Continuing` | Success message after confirmation |
| `stop_msg` | string | `Aborted` | Error message after rejection / Esc |

## `docker_remove_project_volumes`

Removes all Docker volumes whose name starts with `<project_name>_` (resolved from `docker.yml` against the merged config). No parameters. Aborts if the resolved project name is empty.

## `docker_wait_healthy`

Waits for Docker containers to reach a healthy state. Polls the active Docker Compose stack until all specified containers become `healthy`, or until the timeout elapses.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `timeout` | string duration | `60s` | Maximum time to wait; must be positive (e.g. `120s`, `2m`) |
| `interval` | string duration | `2s` | Poll interval; must be positive |
| `services` | list of strings | all | Restrict to specific compose service names; default = all containers in the active stack |

**Example: wait for all containers**

```yaml
- name: wait
  type: builtin
  cmd: docker_wait_healthy
  with:
    timeout: 120s
    interval: 2s
```

**Example: wait for specific services**

```yaml
- name: wait-app
  type: builtin
  cmd: docker_wait_healthy
  with:
    timeout: 60s
    interval: 1s
    services:
      - app-main
      - db
```

**Behavior:**

- If no containers are found in the active stack (or matching the service filter), logs a warning and returns success. Idempotent for pipelines that run before `up`.
- If a container is `unhealthy` or the timeout elapses before all containers become `healthy`, returns an error and stops the pipeline.
- Containers with no healthcheck (status `none`) are treated as always healthy and skipped.
- The active stack is determined by the current overlay set (default, enabled services, enabled tools). This builtin respects `ComposeFiles()`, not `ComposeFilesAll()`.

## `containers_running`

Fast "is running" check for compose services. Unlike `docker_wait_healthy` it does not poll for readiness, does not honour a timeout, and does not require services to declare a healthcheck — a `docker compose ps --status=running --services` call returns the set of currently-running services, and the builtin compares that set with the requested list.

A *transient* probe failure — the `docker compose ps` call itself erroring (**any** non-nil failure to run, e.g. a non-zero exit right at the `docker up --wait` boundary when the compose CLI / daemon is momentarily busy even though every container is already up) — is retried a bounded number of times with a short backoff before the step fails; a cancelled context is the sole exception and short-circuits the remaining retries immediately. This is not readiness polling: a probe that succeeds but reports a service as not-running fails on the first attempt. When every retry fails, the underlying `docker compose ps` stderr is surfaced in the error, so the failure is diagnosable.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `services` | list of strings | required | Compose service names that must be currently running. Empty list is rejected. |

**Example: gate a pipeline step on a running service**

```yaml
- name: stack-up
  type: dwe
  cmd: "docker up"
  check:
    type: builtin
    cmd: containers_running
    with:
      services: [app-main, db]
```

**When to choose this over `docker_wait_healthy`:**

- The service has no healthcheck — `docker_wait_healthy` would either skip it (treated as healthy) or hang waiting for a status that never arrives.
- You need a precondition for a follow-up step (e.g. `service_exec`) and want a clear "container X is not running" error instead of a compose stderr trace.
- The pipeline runs immediately after `docker up` and you just want to confirm the stack came up, without paying a polling round-trip.

If services are missing, the builtin fails with `services not running: <comma-separated list>`.

## `http_check`

Predicate builtin (`KindPredicate`) that performs an HTTP `GET` and asserts the response. It reports success when the endpoint returns the expected status code and — when `contains:` is set — a body that includes the given substring. On failure it retries up to `retries` times, waiting `interval` between attempts; each individual attempt is bounded by `timeout`.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `url` | string | required | Target URL. Must parse as an absolute `http`/`https` URL with a host. |
| `status` | int | `200` | Expected HTTP status code. |
| `contains` | string | — | Optional substring that must appear in the response body. When empty, the body is not read. |
| `retries` | int | `0` | Additional attempts after the first on mismatch/error. Total attempts = `retries + 1`. Must be `>= 0`. |
| `interval` | string duration | `1s` | Wait between attempts. Must be `>= 0`. Cancellable via context. |
| `timeout` | string duration | `5s` | Per-attempt timeout (not total). Must be `> 0`. |

Used as a step body it is an [assertion](#predicate-builtins-as-step-bodies-assertion-semantics): a passing check succeeds the step, a failing check fails the pipeline with a message like `http_check http://localhost:8080/health: expected status 200, got 503 (after 31 attempts)`. It can equally be used inside a `check:`/`when:` block or as a `validate.yml` check entry.

**Example: wait for a health endpoint after `up`**

```yaml
- name: wait-app-http
  type: builtin
  cmd: http_check
  with:
    url: http://localhost:8080/health
    status: 200
    contains: '"status":"ok"'
    retries: 30
    interval: 2s
    timeout: 3s
```

**Behavior:**

- Attempts run in sequence: attempt → on mismatch/error wait `interval` → next attempt, up to `retries + 1` total attempts.
- A non-`status` response, a body missing the `contains` substring, a connection refusal, or a per-attempt timeout all count as a failed attempt.
- `interval` waits and per-attempt requests honour context cancellation, so an interrupted pipeline stops promptly.
- Invalid params (missing/malformed `url`, negative `retries`/`interval`, non-positive `timeout`) are rejected at plan time by `Validate`, before the pipeline runs.

## `remove_paths`

Removes paths from the filesystem.

| Parameter | Type | Description |
|-----------|------|-------------|
| `paths` | list of strings | Project-relative paths to remove. Each must be relative and must not escape the project root; absolute paths and `..` traversal are rejected at validate time. |

## Internal engine builtins (not callable from user YAML)

The following builtins are reserved for the engine. They cannot appear in user-authored `deploy.yml`, `reset.yml`, or `lifecycle.yml` files — attempting to use them produces a load-time error.

| Builtin | Description |
|---------|-------------|
| `docker_daemon_start` | Start a named daemon container via `docker compose run -d`. Invoked by the `.start` virtual command generated from `type: daemon` commands. |
| `docker_daemon_logs` | Tail daemon container logs in the foreground. Invoked by the `.logs` virtual command generated from `type: daemon` commands. |
| `docker_daemon_stop` | Stop a named daemon container (idempotent). Invoked by the `.stop` virtual command generated from `type: daemon` commands. |
| `docker_stop_remove_container` | Stop and remove a named container (`docker stop` + `docker rm -f`, both idempotent on missing container). Invoked by the synthetic `container` phase that `dwe reset run --service <name>` prepends to every per-service reset pipeline. Params: `container_template` (string, required; name template, resolved via the project prefix), `stop_timeout` (duration string, optional, default `10s`). On stop failure, propagates the error and does NOT attempt removal. |
| `daemons_reap` | Stop all project daemon containers. Auto-injected by the engine as the `_auto_reap_daemons` phase at the start of every `stop` lifecycle pipeline. |

## Naming convention

`docker_*` builtins are Docker-specific; `service_*` builtins operate on per-service folders; unprefixed names are generic. The internal builtins follow the same `docker_*` / unprefixed pattern.

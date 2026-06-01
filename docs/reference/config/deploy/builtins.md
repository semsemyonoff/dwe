# Available builtins

Builtins are engine-internal Go functions invoked from a step via `type: builtin`. They run in-process with access to the merged config and the same registry used by `type: builtin` declarative commands.

## Contents

- [Catalogue](#catalogue)
- [`service_dirs_ensure`](#service_dirs_ensure)
- [`service_configs_copy`](#service_configs_copy)
- [`service_configs_check`](#service_configs_check)
- [`message`](#message)
- [`confirm`](#confirm)
- [`docker_remove_project_volumes`](#docker_remove_project_volumes)
- [`docker_wait_healthy`](#docker_wait_healthy)
- [`containers_running`](#containers_running)
- [`remove_paths`](#remove_paths)
- [Internal engine builtins (not callable from user YAML)](#internal-engine-builtins-not-callable-from-user-yaml)
- [Naming convention](#naming-convention)

## Catalogue

| Builtin | Purpose |
|---------|---------|
| `service_dirs_ensure` | Create service hub directories |
| `service_configs_copy` | Copy template config files into the service hub |
| `service_configs_check` | Verify that template config files exist in the service hub |
| `message` | Print a styled message at info/success/warning/error level |
| `confirm` | Interactive Y/n prompt (skipped under `--yes`) |
| `docker_remove_project_volumes` | Remove all volumes whose name is prefixed with the compose project name |
| `docker_wait_healthy` | Wait for Docker containers to reach healthy state |
| `containers_running` | Fast "is running" check (no polling, no timeout, no healthcheck required) |
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

## `service_configs_copy`

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

Fast "is running" check for compose services. Unlike `docker_wait_healthy` it does not poll, does not honour a timeout, and does not require services to declare a healthcheck — a single `docker compose ps --status=running --services` call returns the set of currently-running services, and the builtin diffs that against the requested list.

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

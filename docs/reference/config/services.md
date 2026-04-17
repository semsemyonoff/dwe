# services.yml

Service declarations for the devbox project.

## Purpose

`devbox/services.yml` is the authoritative source for per-service structural config: container name, host directory, internal workdir, optional extensions, CLI execution defaults, config files, and additional hub directories.

It is loaded separately by `LoadServicesConfig()` and is not merged with the 3-layer config.

## Load behavior

- Loaded once at startup alongside the 3-layer config merge.
- Service inheritance via `extends:` is resolved after loading (parent fields copied into child, then child fields override).
- The `dirs` field is deduplicated across parent and child (parent first).

## Structure

```yaml
services:
  <service-key>:
    type: app
    container: <container-name>
    mandatory: true|false
    dir: ./services/<name>              # host-side hub directory
    dir_internal: /workspace            # container mount point
    work_dir_internal: /workspace/src   # workdir for exec/run
    extends: <parent-service-key>       # inherit parent fields
    depends_on:
      - <other-service-key>
    compose:
      - compose/services/<name>/overlay.yml
    configs:
      - <file>                          # shorthand: file copied to service configs dir
      - file: <src>
        mountpoint: <dest>              # explicit source and container path
    dirs:
      - logs
      - home
      - runtime
    cli:
      mode: auto|exec|run
      shell: bash
      user: www-data
      workdir: /workspace/src
      env:
        - KEY=value
```

## Field reference

### Top-level service fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | no | Service type (currently always `app`) |
| `container` | string | yes | Docker container name |
| `mandatory` | bool | no | If true, service is always active; cannot be disabled via `defaults.yml` |
| `dir` | string | yes (non-extends) | Path to the service hub directory on the host |
| `dir_internal` | string | no | Container mount point for the hub |
| `work_dir_internal` | string | no | Default working directory for `exec`/`run` inside container |
| `extends` | string | no | Inherit fields from another service key |
| `depends_on` | list | no | Ordered dependency on other services (affects deploy order) |
| `compose` | list | no | Additional compose overlay files active when service is enabled |

### `configs` field

Lists config files that are copied into the service hub during deploy.

```yaml
configs:
  - .env                        # shorthand: copies src to configs/.env, mounts at default
  - file: .env
    mountpoint: src/.env        # explicit destination inside container
```

| Field | Description |
|-------|-------------|
| `file` (or shorthand string) | Source file name (relative to `configs/app/<service>/`) |
| `mountpoint` | Path inside the service container where the file is mounted |

### `dirs` field

Additional directories to create inside the service hub directory beyond the mandatory `src` and `configs`.

```yaml
dirs:
  - logs
  - home
  - runtime
```

- Paths are relative to the service `dir` (e.g. `./services/main/logs`).
- Mandatory dirs (`src`, `configs`) are always created and are not listed here.
- When a service `extends` another, the child's `dirs` are appended to the parent's (deduplicated, parent first).
- Used by the `service_dirs_ensure` builtin during deploy.

### `cli` block

Controls how `devbox shell` and CLI execution behave for this service.

```yaml
cli:
  mode: auto        # auto | exec | run
  shell: bash
  user: www-data
  workdir: /workspace/src
  env:
    - XDEBUG_CONFIG="cli_color=1"
```

| Field | Default | Description |
|-------|---------|-------------|
| `mode` | `auto` | `auto` = detect best mode; `exec` = `docker exec`; `run` = `docker run` |
| `shell` | `bash` | Shell binary to invoke inside the container |
| `user` | current UID | User to run as inside the container |
| `workdir` | `work_dir_internal` | Working directory for the shell session |
| `env` | — | Extra env vars injected into the shell session |

CLI flags override `cli` config. Priority order (highest first): `--root`/`--user`/`--shell` flags → `cli` config → built-in defaults.

## Inheritance via `extends`

A child service inherits all fields from the named parent. The child then overrides only the fields it declares.

```yaml
services:
  main:
    container: app-main
    mandatory: true
    dir: ./services/main
    dirs: [logs, home, runtime]
    cli:
      shell: bash
      user: www-data

  main-debug:
    extends: main            # inherits dir, dirs, cli, etc.
    container: app-main-debug
    mandatory: false
    compose:
      - compose/services/main/debug.yml
    cli:
      env:
        - XDEBUG_CONFIG="cli_color=1"
```

`main-debug` gets `dir`, `dirs`, and base `cli` fields from `main`, and adds its own `compose` overlay and extra env.

## Example: full service definition

```yaml
services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    configs:
      - .env
    dirs:
      - logs
      - home
      - runtime
    cli:
      mode: auto
      shell: bash
      user: www-data
      workdir: /workspace/src
```

## Common pitfalls

- **Editing `dir` in `extends` child** — a child that sets `dir` completely replaces the parent's `dir` (not merged). This is intentional for services that live in a different host directory.
- **Absolute paths in `dirs`** — dirs entries must be relative paths. Absolute paths or paths containing `..` are rejected by `service_dirs_ensure` as a security check.
- **Missing `container` in child** — if a child extends a parent and does not set `container`, it inherits the parent's container name, which is almost certainly wrong for an optional service.
- **`cli.env` list vs map** — `cli.env` is a list of `KEY=value` strings, not a YAML map.

## Related commands

- `devbox shell [service]` — open shell in service container
- `devbox services list` — list all services with status
- `devbox services enable/disable <service>` — toggle optional services
- `devbox deploy run` — runs the full deploy pipeline, including `service_dirs_ensure` in the setup phase

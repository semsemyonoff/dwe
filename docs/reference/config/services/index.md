# Service configuration (`workspace/services/<name>/service.yml`)

Service declarations for the DWE project.

## Contents

- [Purpose](#purpose)
- [Service types](#service-types)
- [Per-type field allowlist](#per-type-field-allowlist)
- [Load behavior](#load-behavior)
- [Structure](#structure)
- [Pages](#pages)
- [Related commands](#related-commands)

## Purpose

Service definitions live under `workspace/services/`, one folder per service. Each service is declared in `workspace/services/<name>/service.yml`, where `<name>` becomes the service's key in the resolved `DweConfig.Services` map. Each entry declares its container name, ports, hosts, compose overlay, and optional structural fields. The `type:` discriminator selects which fields are legal for the entry.

On load, each `workspace/services/*/` subdirectory is enumerated and its `service.yml` is parsed in strict mode: the file must declare a `type`, every field is checked against the type's allowlist, `extends:` is rejected on non-app entries, and the shapes of `ports` / `hosts` are validated. Cross-service `extends:` chains are then resolved in topological order and parent fields are merged into each child. A missing `workspace/services/` directory yields an empty service set (not an error). Per-developer toggles (`enabled:`, `ports:`, `hosts:`) live in the 3-layer overlay; structural fields belong exclusively in `workspace/services/<name>/service.yml`.

## Service types

Every entry under `services:` requires a `type:` key. Three values are supported:

| `type:` | Semantics | Deploy lifecycle | `depends_on:` target | App-only fields |
|---------|-----------|------------------|----------------------|-----------------|
| `app`   | Service with source code under `dir:`; renders IDE/AI/git templates; runs through `dwe deploy`. | yes (a `workspace/services/<name>/deploy.yml` may exist) | yes | yes (`dir`, `dir_internal`, `work_dir_internal`, `configs`, `dirs`, `extends`, `cli`, `render`, `generated`) |
| `tool`  | Ephemeral utility container (adminer, mailpit, redis-insight). Cannot be a dependency target of any service. | no | **no** | no |
| `infra` | Backing service (db, cache, queue, search). Can be a dependency target of `app` / other `infra`. | no | yes | no |

Locked rules:

- `extends:` is **app-only**. A `tool` / `infra` entry with `extends:` is rejected at load.
- `depends_on:` may not reference a `type: tool` entry. This is enforced at load (`ErrDependsOnTool`), not only at validate time.
- `workspace/services/<name>/deploy.yml` is supported for **any service type** (app, tool, infra). Full deploy (`dwe deploy run`) enumerates every **enabled** service that has a `deploy.yml`; `dwe deploy run --service <name>` works for any service type with a deploy file regardless of enabled state.
- `ports:` is always `map[string]int` and `hosts:` is always `map[string]string`. There is no `port:` / `host:` scalar shorthand. A single-port entry writes `ports: { http: 8025 }`.
- Type semantics partition `docker compose` file emission to `tool → infra → app` order.

`type: infra` services may be optional (`required: false`) — they take a `compose:` overlay and are toggleable via `dwe services enable|disable <name>` like apps and tools. Required infra (`required: true`, typical for backing services like databases, caches, and queues) is always-on and not toggleable. Optional infra fits semantically request-path or data-path components that are not strictly required for every developer (e.g. a Varnish cache in front of nginx, or a MinIO S3-storage backend used only when no external S3 is configured).

### Why the type matrix

The per-type allowlists reflect the distinct roles services play in the dev environment:

- **`app`**: owns source code and a running container. Supports mounts, templates, and deploy orchestration (`dir`, `extends`, `configs`, `depends_on`).
- **`tool`**: standalone utility container (database UI, observability frontend, etc.). No source ownership; cannot depend on services or be a `depends_on` target for other services.
- **`infra`**: supporting container (database, cache, broker, reverse proxy). Can be a `depends_on` target but not own source; lighter footprint than app.

This separation ensures that build and deploy logic is explicit (defined only in `type: app`), and that infrastructure services remain independently testable without dragging in application code.

## Per-type field allowlist

| Field             | `app` | `tool` | `infra` |
|-------------------|:-----:|:------:|:-------:|
| `type`            |   ✓   |   ✓    |    ✓    |
| `container`       |   ✓   |   ✓    |    ✓    |
| `required`        |   ✓   |   ✓    |    ✓    |
| `compose`         |   ✓   |   ✓    |    ✓    |
| `ports`           |   ✓   |   ✓    |    ✓    |
| `hosts`           |   ✓   |   ✓    |    ✓    |
| `icon`            |   ✓   |   ✓    |    ✓    |
| `info`            |   ✓   |   ✓    |    ✓    |
| `depends_on`      |   ✓   |   —    |    ✓    |
| `status`          |   ✓   |   ✓    |    ✓    |
| `dir`             |   ✓   |   —    |    —    |
| `dir_internal`    |   ✓   |   —    |    —    |
| `work_dir_internal` | ✓   |   —    |    —    |
| `configs`         |   ✓   |   —    |    —    |
| `dirs`            |   ✓   |   —    |    —    |
| `extends`         |   ✓   |   —    |    —    |
| `cli`             |   ✓   |   —    |    —    |
| `render`          |   ✓   |   —    |    —    |
| `generated`       |   ✓   |   —    |    —    |
| `on_enable`       |   ✓   |   ✓    |    ✓    |
| `on_disable`      |   ✓   |   ✓    |    ✓    |
| `notes`           |   ✓   |   ✓    |    ✓    |
| `bridge`          |   ✓   |   ✓    |    ✓    |

A disallowed field is a hard load error (`ErrServiceFieldNotAllowed`). Validation aggregates per-file violations via `errors.Join` so a single parse pass surfaces every issue at once.

## Load behavior

- Each `workspace/services/<name>/service.yml` is strict-decoded — unknown and per-type-disallowed fields are hard errors. Errors from all folders are reported together, so every broken folder surfaces at once, not just the first.
- Service inheritance via `extends:` is resolved in topological order (parents before children) so multi-level chains (`C → B → A`) merge correctly regardless of map iteration order. Cycles and unknown parents are reported as load errors. `extends:` is **app-only**.
- For each child, only zero-value fields are inherited from the parent; child fields take precedence on conflicts. Inherited slices and maps are copied defensively, so mutating a child never corrupts the parent.
- The `dirs` field is deduplicated across parent and child (parent first, child appended). `cli.env` is recursively merged: parent provides defaults, child wins on key conflicts.
- After loading, `enabled` is resolved from the 3-layer merge (`services.<name>.enabled`); required services force `enabled: true`.
- Overlays under `services.<name>` may set **only** `enabled:`, `ports:`, and `hosts:`. Any other field there is a layer-aware overlay error — structural fields (`container`, `dir`, `configs`, `compose`, `extends`, …) belong in `workspace/services/<name>/service.yml`. The overlay validator also enforces shape: `ports:` must be a map of name → integer in `1..65535`; `hosts:` must be a map of name → string.
- `ports:` and `hosts:` are **deep-merged by entry name** on top of the declared map: a per-developer override under `workspace/local.yml` only touches the listed keys; declared entries the overlay does not mention are preserved. New entries may also be introduced via overlay. This is a first-class DWE feature: developers routinely need to remap a port that clashes with something already bound on their host, or switch their `*.local` hostname, without editing the shared `workspace/services/<name>/service.yml`.
- Each resolved service (including the post-overlay `ports` / `hosts` nested maps) is injected into `DweConfig.Raw["services"]` so dot-paths like `services.main.ports.http` and `services.adminer.hosts.web` resolve in export rules, `docker.yml` templates, command `default_from:`, and `info.yml` references.
- Port values are bounded `1..65535` at load time (both in `service.yml` and in overlay layers).

Example: a developer whose host already binds 8027 remaps adminer locally without touching the shared config —

```yaml
# workspace/local.yml (not tracked by git)
services:
  adminer:
    ports:
      http: 9027        # overrides declared 8027
  main:
    hosts:
      api: api.dev.local   # adds a new entry; web stays as declared
```

## Structure

Each service lives in its own folder under `workspace/services/`. The folder name becomes the service key.

```
workspace/services/
  main/
    service.yml
    deploy.yml          # optional; enables deploy lifecycle for this service
  db/
    service.yml
  varnish/
    service.yml
  adminer/
    service.yml
```

```yaml
# workspace/services/main/service.yml
# type: app — owns source under dir:, has deploy lifecycle, renders templates
type: app
container: app-main
required: true
dir: ./services/main
dir_internal: /workspace
work_dir_internal: /workspace/src
extends: <parent-app-key>           # app-only
depends_on: [db, redis]             # may target app or infra (never tool)
icon: "📦"
hosts:
  web: app.localhost
ports:
  http: 80
info:
  title: "Main Application"
  primary_host: web
  primary_port: http
  paths:
    - name: "API Documentation"
      path: /api/docs
      icon: "📖"
compose:
  - compose/services/main/overlay.yml
configs:
  - file: .env
    mountpoint: src/.env
dirs: [logs, home, runtime]
cli:
  mode: auto|exec|run
  shell: bash
  user: www-data
  workdir: /workspace/src
  env:
    - KEY=value
render:
  ide:    { enabled: true, template: <pack> }
  ai:     { enabled: true, template: <pack> }
  git:    { enabled: true, template: <pack> }
```

```yaml
# workspace/services/db/service.yml
# type: infra — backing service, may be a depends_on target
type: infra
container: db
required: true                      # always-on backing service
ports:
  mysql: 13306
```

```yaml
# workspace/services/varnish/service.yml
# type: infra (optional) — toggleable via `dwe services enable varnish`
# Note: container field is omitted here — it defaults to the folder name "varnish"
type: infra
compose:
  - compose/services/varnish/overlay.yml
ports:
  http: 6081
```

```yaml
# workspace/services/adminer/service.yml
# type: tool — ephemeral utility container, never a depends_on target
type: tool
container: adminer
icon: "🔧"
compose:
  - compose/tools/adminer.yml
ports:
  http: 8027
hosts:
  web: db.localhost
info:
  title: Adminer
```

## Pages

- [Field reference](fields.md) — every top-level field plus the `ports`, `hosts`, `icon`, `info`, `configs`, `dirs`, `cli`, `status`, and `render` blocks
- [Inheritance via `extends`](extends.md) — toposort, resolution rules, app-only guard, worked example
- [Examples and toggle lifecycle](examples.md) — full service definition, `on_enable` / `on_disable` / `notes`, common pitfalls

## Related commands

- `dwe shell [service]` — open a shell in any enabled service container (the `cli:` defaults block is `type: app` only; tool/infra use built-in bash/auto defaults).
- `dwe status` — composite read-only view: apps + tools + infra sections, each with custom `status:` columns.
- `dwe status apps` / `dwe status tools` / `dwe status infra` — per-type tables.
- `dwe services` — interactive multi-select toggle for every optional service across all types.
- `dwe services enable <name>` / `dwe services disable <name>` — toggle by name (type looked up internally).
- `dwe deploy run` — runs the full deploy pipeline; enumerates all enabled services that have a `workspace/services/<name>/deploy.yml` (any service type).
- `dwe reset run --service <name>` — resets a single service: stops and removes the container, deletes the service `dir:` if declared and present, runs per-service `reset.yml` if present, marks service as requiring a subsequent deploy. Volumes are not auto-removed (opt in via `docker_remove_project_volumes`).

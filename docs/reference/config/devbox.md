# devbox.yml / defaults.yml / local.yml

The three layers of the merged devbox config.

## Contents

- [Merge overview](#merge-overview)
- [What belongs in each layer](#what-belongs-in-each-layer)
- [Dot-path resolution](#dot-path-resolution)
  - [Where service fields come from](#where-service-fields-come-from)
- [devbox.yml](#devboxyml)
  - [Field reference](#field-reference)
- [devbox/defaults.yml](#devboxdefaultsyml)
  - [`tools`](#tools)
  - [`services`](#services)
  - [`debug`](#debug)
  - [`runtime`](#runtime)
  - [`state`](#state)
  - [`exports.env`](#exportsenv)
  - [`db`](#db)
  - [`compose`](#compose)
  - [`ide`](#ide)
- [devbox/local.yml](#devboxlocalyml)
- [Common pitfalls](#common-pitfalls)
- [Related commands](#related-commands)

## Merge overview

```mermaid
flowchart TB
  L1["1 · devbox.yml<br/>tracked · structural skeleton"]
  L2["2 · devbox/defaults.yml<br/>tracked · versioned defaults"]
  L3["3 · devbox/local.yml<br/>gitignored · per-user overrides"]
  R[(Effective DevboxConfig<br/>+ DevboxConfig.Raw)]

  L1 -- "merged into" --> L2
  L2 -- "overridden by<br/>(local wins)" --> L3
  L3 -- "deepMerge result" --> R

  R --> ENV[devbox render env → .env]
  R --> DASH[devbox info]
  R --> RES[ResolvePath dot-paths<br/>exports, docker.yml,<br/>commands, info templates]
```

Read top-to-bottom: each arrow is "next layer applied on top". `local.yml` sits at the end, so any key it sets shadows the same key from `defaults.yml` or `devbox.yml`. Keys absent from `local.yml` fall through to `defaults.yml`, then to `devbox.yml`, then to the typed Go zero value.

The three files share a single namespace — the same key in different layers is the same setting. Layer 1 establishes structure, Layer 2 fills in defaults, Layer 3 overrides for the local machine. None of the three is required to declare every key; missing keys simply fall through to whatever lower layer set them, with type-zero values as the ultimate fallback.

`devbox/local.yml` is optional: when absent, the merge silently skips layer 3.

## What belongs in each layer

| Concern | Layer |
|---------|-------|
| Project name and prefix | `devbox.yml` |
| Schema version | `devbox.yml` |
| Binary overrides (`binaries:`) | `devbox.yml` only (engine policy, not layered) |
| Port defaults | `defaults.yml` |
| Host defaults | `defaults.yml` |
| Tool definitions and enabled state | `defaults.yml` |
| Optional service defaults (enabled/disabled) | `defaults.yml` |
| Export rules (`exports.env`) | `defaults.yml` |
| IDE config defaults | `defaults.yml` |
| `db` block defaults | `defaults.yml` |
| Active state | `local.yml` |
| Personal port overrides | `local.yml` |
| Personal credentials (`db.user`, `db.password`) | `local.yml` |
| Enabling debug / optional services | `local.yml` |

Service definitions themselves live in [`devbox/services.yml`](services.md), which is loaded separately and not part of this merge.

## Dot-path resolution

The CLI stores the merged result in two places: a typed `DevboxConfig` struct (with fields like `DevboxConfig.Runtime.Ports`, accessed via map keys like `.Runtime.Ports.app`) and a plain `DevboxConfig.Raw` map. The Raw map drives dot-path resolution.

A dot-path is a `.`-separated key chain that navigates the merged YAML map. Examples:

- `runtime.ports.app` → `80`
- `tools.adminer.enabled` → `false`
- `services.main.container` → `"app-main"` (injected from `services.yml`)

Dot-paths are consumed by:

- export rules in `defaults.yml` (`from:`, `when:`)
- `${...}` template expressions in `docker.yml` (`project_name`)
- `${...}` template expressions in declarative commands (`devbox/commands/`)
- `{{ ... }}` Go templates in `info.yml` (via the typed struct, not Raw)

### Where service fields come from

`services.<name>.*` paths in the merged map are populated by `LoadConfig`. After parsing the 3 layers it loads `devbox/services.yml`, resolves `enabled` against the merged map (mandatory services force `enabled: true`), then injects each service's `type`, `container`, `mandatory`, `enabled`, `dir`, `dir_internal`, `work_dir_internal`, `compose`, and `configs` into `raw["services"]`. Export rules can therefore use both `services.main.container` and `services.second.enabled` without separate awareness of `services.yml`.

## devbox.yml

**Purpose**: Project identity and structural skeleton. Tracked by git. Rarely changes after initial setup.

**Load order**: Layer 1 (base).

**Example**:
```yaml
schema_version: "2"

project:
  name: laravel
  prefix: devbox
```

### Field reference

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | string | Config schema version. Must be `"2"` — the CLI rejects v1 projects with a clear error. |
| `project.name` | string | Short project identifier (used in container names, `.env`) |
| `project.prefix` | string | Prefix for Docker project name and container labels |
| `binaries.devbox` | string | Devbox binary name for nested calls and plan display. Default: `devbox`. |
| `binaries.docker` | string | Docker binary name used for all `docker compose` execution. Default: `docker`. Override with `podman` or any OCI-compatible binary. |
| `binaries.shell` | string | Shell used for host-side script and lifecycle step execution. Default: `sh`. |

`project.prefix` and `project.name` combine to form the Docker Compose project name via the template in `docker.yml` (`${project.prefix}-${project.name}`).

### `binaries` block

The optional `binaries:` block overrides the executables devbox shells out to. It is read from `devbox.yml` only — values set in `defaults.yml` or `local.yml` are silently ignored.

```yaml
binaries:
  devbox: devbox          # nested devbox calls and plan display
  docker: docker          # all docker compose execution
  shell: sh               # host-side script / lifecycle step execution
```

All three keys are optional; any key omitted uses its default. Partial overrides are safe:

```yaml
binaries:
  docker: podman          # only substitute docker; devbox and shell stay at defaults
```

The effective values are accessible as `${binaries.devbox}`, `${binaries.docker}`, and `${binaries.shell}` in template expressions (commands, docker.yml project_name, export rules).

> **Engine policy, not user state.** The `binaries:` block controls which executables the CLI itself invokes — it is part of the project's engine contract, not per-user configuration. Commit it in `devbox.yml`; do not put it in `local.yml`.

---

## devbox/defaults.yml

**Purpose**: Versioned defaults for the entire project. Tracked by git. Provides all runtime configuration that is not structural identity.

**Load order**: Layer 2 (merged on top of `devbox.yml`).

**Sections**:

### `tools`

Declares optional tool containers. Each tool is identified by a key (e.g., `adminer`, `redis_insight`). Tool keys must be identifier-safe (`^[A-Za-z_][A-Za-z0-9_]*$` — no dashes, dots, or leading digits — so they work in Go templates with dot syntax).

```yaml
tools:
  adminer:
    enabled: false
    container: adminer
    host: adminer.localhost
    port: 8080
    compose: compose/tools/adminer.yml
  redis_insight:
    enabled: true
    container: redis-insight
    host: redis.localhost
    port: 5540
    compose: compose/tools/redis_insight.yml
  mailpit:
    enabled: true
    container: mailpit
    host: mail.localhost
    port: 8025
    compose: compose/tools/mailpit.yml
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | yes | Whether the tool is active. Set in `defaults.yml`; override in `local.yml` via `devbox tools enable/disable`. |
| `container` | string | yes | Docker image or container name. |
| `host` | string | yes | Virtual hostname for the tool (used in reverse proxy, exported to templates and env). |
| `port` | int | yes | Container port (non-zero). Exposed to templates and env for direct-container access; tool URLs use this host and the app port via reverse proxy. |
| `compose` | string | yes | Relative path to the docker-compose overlay file that defines the tool service. Used to build the active compose file list. |

All five fields are required; any tool entry (enabled or disabled) missing a field causes a config-load error. This catches mistakes early — a tool visible in `tools status` must be fully defined.

Tool host/port do not go in the generic `runtime.hosts` / `runtime.ports` maps. Access them via `.Tools.<key>.Host` and `.Tools.<key>.Port` in templates, or via raw dot-paths like `tools.<key>.host` / `tools.<key>.port` in export rules.

Adding a new tool requires only a YAML edit in `defaults.yml` — no Go code changes needed.

### `services`

Toggle optional services (services defined in `services.yml` with `mandatory: false`).

```yaml
services:
  main-debug:
    enabled: false
  second:
    enabled: false
```

Mandatory services (e.g. `main`) are always active and have no toggle here.

### `debug`

```yaml
debug:
  idekey: PHPSTORM
```

| Field | Description |
|-------|-------------|
| `debug.idekey` | Xdebug IDE key exported as `XDEBUG_IDEKEY` in `.env` |

### `runtime`

All runtime settings that affect `.env` generation and the info dashboard. This section holds **non-tool runtime roles only** (app, database, redis, etc.); tool-specific host/port values live under `tools:<name>` instead.

```yaml
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
  hosts:
    main: laravel.localhost
    second: second.localhost
  spx:
    path: ""
```

| Field | Description |
|-------|-------------|
| `runtime.use_https` | Whether URLs use HTTPS (exported as `USE_HTTPS`) |
| `runtime.ports.*` | Non-tool port mappings, keyed by role name (e.g., `app`, `db`, `redis`). Each key must be identifier-safe. Exported individually to `.env`. |
| `runtime.hosts.*` | Non-tool hostnames, keyed by service/role name (e.g., `main`, `second`). Each key must be identifier-safe. |
| `runtime.spx.path` | SPX profiler URL path (empty = disabled) |

**Tool host/port:** Do not add tool-specific keys here. Each tool's `host` and `port` live under `tools.<toolname>.host` and `tools.<toolname>.port` in the `tools:` section (see above).

### `state`

```yaml
state: ""
```

Active state name. Empty string means no state. Exported as `STATE` in `.env`. Override in `local.yml` (e.g. `state: staging`).

### `exports.env`

Declarative export rules that drive `.env` generation. Each rule maps a dot-path in the merged config to an env variable name. Tool paths use the format `tools.<toolname>.<field>` (e.g., `tools.adminer.port`, `tools.redis_insight.host`).

```yaml
exports:
  env:
    - name: APP_PORT
      from: runtime.ports.app
      format: int
    - name: TOOL_ADMINER_ENABLED
      from: tools.adminer.enabled
      format: bool
      when: tools.adminer.enabled
    - name: TOOL_ADMINER_PORT
      from: tools.adminer.port
      format: int
      when: tools.adminer.enabled
```

| Rule field | Type | Description |
|------------|------|-------------|
| `name` | string | Env variable name in `.env` |
| `from` | string | Dot-path into the merged config |
| `default` | string | Fallback value when path is absent |
| `required` | bool | Error if path absent and no default |
| `format` | string | `string` (default), `bool`, `int` |
| `when` | string | Dot-path; rule skipped when value is falsy |
| `comment` | string | Written as `# comment` above the variable |

#### Implicit system variables

`devbox render env` always emits three variables before any rule runs, regardless of `exports.env`:

| Variable | Source | Notes |
|----------|--------|-------|
| `PROJECT` | `project.name` | Used by Docker labels and Make targets |
| `UID` | host UID | Hard-coded to `1000` on macOS, real UID on Linux/WSL — keeps container builds deterministic across hosts |
| `GID` | host GID | Same logic as `UID` |

These are managed by the CLI; do not redeclare them as export rules.

### `db`

Database settings for the project. The keys are not interpreted by the CLI directly — they are exposed via dot-paths and consumed by export rules in `defaults.yml` (`DB_DATABASE`, `DB_USER`, `DB_PASSWORD`, etc.) and by declarative commands.

```yaml
db:
  database: laravel
  second_database: laravel_second
  user: root
  password: root
  backup_dir: backups/db
```

| Field | Description |
|-------|-------------|
| `db.database` | Primary database name (exported as `DB_DATABASE`) |
| `db.second_database` | Optional secondary database name, referenced by per-service `db.create` commands when the `second` service is enabled |
| `db.user` | Database user (exported as `DB_USER`) |
| `db.password` | Database password (exported as `DB_PASSWORD`) |
| `db.backup_dir` | Project-relative directory for SQL dumps used by `db.dump-create` / `db.dump-deploy` commands |

Add new fields here whenever a command or export rule needs project-wide DB metadata; the YAML map is open-ended.

### `compose`

Compose file configuration used by the Docker control plane.

```yaml
compose:
  base: compose.yaml
```

| Field | Description |
|-------|-------------|
| `compose.base` | Base compose file (always included) |

Service overlays live under `services:<name>.compose` (a list of file paths per service). Tool overlays live under `tools:<name>.compose` (a single file path per tool) — see the `tools:` section above.

---

## devbox/local.yml

**Purpose**: Per-user overrides. Gitignored, never committed. Template in `devbox/local.example.yml`.

**Load order**: Layer 3 (merged last — highest precedence).

**Example overrides**:
```yaml
state: staging

tools:
  redis_insight:
    enabled: false

runtime:
  use_https: true
  ports:
    app: 8080

services:
  main-debug:
    enabled: true

db:
  user: myuser
  password: mypassword

debug:
  idekey: VSCODE
```

To override a tool's host or port (rare — normally set in `defaults.yml`), you would edit the tool entry directly in `local.yml`. However, the recommended practice is to keep tool definitions in `defaults.yml` and use `local.yml` only to toggle `enabled`.

If `local.yml` does not exist, layer 3 is silently skipped.

## Common pitfalls

- **Editing `defaults.yml` for personal settings** — changes are tracked and affect every team member. Personal overrides always go in `local.yml`.
- **Committing `local.yml`** — it is gitignored for a reason (may contain credentials).
- **Setting `state:` in `defaults.yml`** — state is inherently per-user, put it in `local.yml`.
- **Scalar collision** — if `defaults.yml` sets `state: ""` and `local.yml` sets `state: staging`, the effective value is `staging`. If `local.yml` omits `state`, the `defaults.yml` value wins.
- **Lists replace, maps merge** — maps are deep-merged: redeclaring `runtime.ports` in `local.yml` only overrides the keys you list, the rest fall through from `defaults.yml`. Lists, by contrast, are replaced wholesale: setting `args.global: ["--ansi", "always"]` in `local.yml` discards every entry the lower layers had, so include the full list you want.

## Related commands

- `devbox render env -o .env` — regenerate `.env` from the merged config
- `devbox info` — show dashboard (uses merged config + `info.yml`)
- `devbox services list` — show services with enabled/disabled status
- `devbox tools list` — show tools with enabled/disabled status
- `devbox compose argv` — show the effective compose command with all flags (useful for debugging dot-path resolution into `docker.yml`)

# devbox.yml / defaults.yml / local.yml

The three layers of the merged devbox config.

## devbox.yml

**Purpose**: Project identity and structural skeleton. Tracked by git. Rarely changes after initial setup.

**Load order**: Layer 1 (base).

**Example**:
```yaml
schema_version: "1"

project:
  name: laravel
  prefix: devbox
```

### Field reference

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | string | Config schema version (currently `"1"`) |
| `project.name` | string | Short project identifier (used in container names, `.env`) |
| `project.prefix` | string | Prefix for Docker project name and container labels |

`project.prefix` and `project.name` combine to form the Docker Compose project name via the template in `docker.yml` (`${project.prefix}-${project.name}`).

---

## devbox/defaults.yml

**Purpose**: Versioned defaults for the entire project. Tracked by git. Provides all runtime configuration that is not structural identity.

**Load order**: Layer 2 (merged on top of `devbox.yml`).

**Sections**:

### `tools`

Controls which optional tool containers are active.

```yaml
tools:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
```

Tool keys must correspond to overlay entries in `compose.overlays`. The CLI uses enabled tools to build the active compose file list.

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

All runtime settings that affect `.env` generation and the info dashboard.

```yaml
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
    adminer: 8080
    redis_insight: 5540
    mailpit: 8025
  hosts:
    main: laravel.localhost
    second: second.localhost
    adminer: adminer.localhost
    redis_insight: redis.localhost
    mailpit: mail.localhost
  spx:
    path: ""
```

| Field | Description |
|-------|-------------|
| `runtime.use_https` | Whether URLs use HTTPS (exported as `USE_HTTPS`) |
| `runtime.ports.*` | Host port mappings (exported individually to `.env`) |
| `runtime.hosts.*` | Hostnames for nginx virtual hosting |
| `runtime.spx.path` | SPX profiler URL path (empty = disabled) |

### `state`

```yaml
state: ""
```

Active state name. Empty string means no state. Exported as `STATE` in `.env`. Override in `local.yml` (e.g. `state: staging`).

### `exports.env`

Declarative export rules that drive `.env` generation. Each rule maps a dot-path in the merged config to an env variable name.

```yaml
exports:
  env:
    - name: APP_PORT
      from: runtime.ports.app
      format: int
    - name: TOOL_ADMINER
      from: tools.adminer.enabled
      format: bool
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

### `db`

Database credentials for the project.

```yaml
db:
  database: laravel
  second_database: laravel_second
  password: root
  user: root
```

These are referenced in export rules and available via dot-paths.

### `compose`

Compose file configuration used by the Docker control plane.

```yaml
compose:
  base: compose.yaml
  overlays:
    adminer: compose/tools/adminer.yml
    redis_insight: compose/tools/redis_insight.yml
    mailpit: compose/tools/mailpit.yml
```

| Field | Description |
|-------|-------------|
| `compose.base` | Base compose file (always included) |
| `compose.overlays` | Map of overlay key → file path; overlay is active when the corresponding tool/service is enabled |

### `ide`

IDE config generation settings.

```yaml
ide:
  vscode:
    enabled: true
  jetbrains:
    enabled: false
  devcontainer:
    enabled: true
```

Used by `devbox render ide` to determine which editor configs to generate.

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

If `local.yml` does not exist, layer 3 is silently skipped.

## Common pitfalls

- Editing `defaults.yml` for personal settings — use `local.yml` instead.
- Committing `local.yml` — it is gitignored for a reason (may contain credentials).
- Setting `state:` in `defaults.yml` — state is inherently per-user, put it in `local.yml`.
- Forgetting that lists replace rather than merge — if you override `runtime.ports` you must include all ports you care about.

## Related commands

- `devbox render env -o .env` — regenerate `.env` from the merged config
- `devbox info` — show dashboard (uses merged config + `info.yml`)
- `devbox services list` — show services with enabled/disabled status
- `devbox tools list` — show tools with enabled/disabled status

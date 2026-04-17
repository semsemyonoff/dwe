# docker.yml / docker.local.yml

Compose execution policy for the devbox project.

## Purpose

`devbox/docker.yml` controls how `devbox docker` builds and executes `docker compose` commands: the project name, per-subcommand args, process environment, and automatic `.env` generation triggers.

It is loaded separately by `LoadDockerConfig()` and is not merged with the 3-layer config.

Local overrides go in `devbox/docker.local.yml` (gitignored). Template in `devbox/docker.local.example.yml`.

## devbox docker vs devbox compose

| Command | Purpose |
|---------|---------|
| `devbox docker <subcommand>` | Public lifecycle API. Policy args applied. Use in Makefiles, deploy steps, and YAML commands. |
| `devbox compose raw <args...>` | Low-level diagnostic pass-through. No policy args. Use for debugging only. |
| `devbox compose files` | Show active compose file list (diagnostic). |
| `devbox compose argv` | Show full effective argv including policy args (diagnostic). |

Only `devbox docker` subcommands are allowed in Makefiles, YAML command definitions, and deploy steps. Direct `docker compose` calls bypass policy and must not appear in any automation.

## Structure

```yaml
project_name: "${project.prefix}-${project.name}"

args:
  global: ["--ansi", "always", "--progress", "tty"]
  up: ["-d", "--remove-orphans"]
  logs: ["-f"]
  run: ["--rm"]

process_env:
  DOCKER_CLI_HINTS: "false"

env:
  auto_generate: true
  commands: [up, run, exec, restart]

topology:
  hidden: [redis-insight-setup]

resources:
  volumes:
    composer_cache:
      name: devbox_composer_cache
      shared: true
      ensure_before: [up, deploy]
```

## Field reference

### `project_name`

```yaml
project_name: "${project.prefix}-${project.name}"
```

The Docker Compose project name passed as `-p <name>` to every compose invocation. Supports `${dot.path}` template references into the merged devbox config. Default resolves to `devbox-laravel`.

Override locally:
```yaml
# docker.local.yml
project_name: "my-custom-project"
```

### `args`

Per-subcommand arg lists. Each key is a docker subcommand name; `global` applies to every invocation before the subcommand-specific args.

```yaml
args:
  global: ["--ansi", "always", "--progress", "tty"]
  up: ["-d", "--remove-orphans"]
  logs: ["-f"]
  run: ["--rm"]
```

Available subcommand keys: `global`, `up`, `down`, `stop`, `restart`, `logs`, `ps`, `exec`, `run`, `wait`.

When overriding in `docker.local.yml`, the list replaces the tracked default entirely (lists do not merge):

```yaml
# docker.local.yml — remove --progress tty (unsupported in some terminals)
args:
  global: ["--ansi", "always"]
```

### `process_env`

Environment variables passed to every `docker compose` child process. Does not affect the container environment — only the compose CLI process itself.

```yaml
process_env:
  DOCKER_CLI_HINTS: "false"
```

Useful for suppressing Docker CLI noise that appears even when output is piped.

### `env`

Controls automatic `.env` generation before specific subcommands.

```yaml
env:
  auto_generate: true
  commands: [up, run, exec, restart]
```

| Field | Description |
|-------|-------------|
| `auto_generate` | When true, CLI regenerates `.env` before the listed commands |
| `commands` | Subcommands that trigger auto-generation |

When a listed command runs, `devbox render env -o .env` executes implicitly before compose. Disable for CI environments where `.env` is pre-generated:

```yaml
# docker.local.yml
env:
  auto_generate: false
```

### `topology`

```yaml
topology:
  hidden: [redis-insight-setup]
```

| Field | Description |
|-------|-------------|
| `hidden` | Compose service names excluded from the topology tree and health checks |

Useful for init containers that run once and exit — hiding them prevents `devbox docker wait` from waiting on them.

### `resources`

Declares Docker resources that must exist before certain commands.

```yaml
resources:
  volumes:
    composer_cache:
      name: devbox_composer_cache
      shared: true
      ensure_before: [up, deploy]
```

| Field | Description |
|-------|-------------|
| `volumes.<key>.name` | Docker volume name |
| `volumes.<key>.shared` | If true, volume is shared across projects (not prefixed with project name) |
| `volumes.<key>.ensure_before` | Subcommands/actions that trigger volume creation if missing |

## docker.local.yml

Local overrides for the docker policy. Gitignored. Use `devbox/docker.local.example.yml` as a starting template.

Common overrides:

```yaml
# Override project name
project_name: "personal-laravel"

# Remove --progress tty (unsupported in some terminals)
args:
  global: ["--ansi", "always"]

# Disable auto .env generation (pre-generated in CI)
env:
  auto_generate: false

# Suppress Docker hints
process_env:
  DOCKER_CLI_HINTS: "false"
```

## Common pitfalls

- **Direct `docker compose` in Makefiles or YAML** — always use `devbox docker`. Direct calls bypass policy args, project name, and `.env` auto-generation.
- **Adding compose flags in Make recipes** — flags belong in `docker.yml` args section, not in Make. Make lifecycle targets call `devbox docker` with no flags.
- **Overriding args partially** — `args.up` in `docker.local.yml` replaces the tracked list, not appends to it. Include all flags you need.
- **Disabling `auto_generate` globally** — if you disable it, you must regenerate `.env` manually before compose commands that depend on it.

## Related commands

- `devbox docker up|down|stop|restart|logs|ps|exec|run|wait` — lifecycle commands
- `devbox compose files` — show active compose file list
- `devbox compose argv` — show full effective argv
- `devbox render env` — manually regenerate `.env`

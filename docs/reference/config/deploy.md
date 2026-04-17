# deploy.yml / reset.yml

Deploy and reset pipeline declarations.

## Purpose

`devbox/deploy.yml` declares the orchestrator deploy pipeline. `devbox/reset.yml` declares the destructive reset pipeline. Per-service deploy pipelines live in `devbox/deploy/<service>.yml`.

All three are loaded separately by `LoadDeployConfig()` and are not merged with the 3-layer config.

## File roles

| File | Role |
|------|------|
| `devbox/deploy.yml` | Top-level orchestrator: lists phases in order, references service pipelines |
| `devbox/deploy/<svc>.yml` | Per-service phases and steps (inlined by orchestrator at `deploy_services: true`) |
| `devbox/reset.yml` | Separate reset pipeline, executed via `devbox reset run` |

## Structure

```yaml
phases:
  - name: <phase-name>
    description: Human-readable description
    when: "<condition>"          # optional: skip phase if false
    untracked: true              # optional: suppress step output for this phase
    deploy_services: true        # orchestrator marker (deploy.yml only)
    steps:
      - name: <step-name>
        description: Human-readable description
        when: "<condition>"      # optional: skip step if false
        check: "<check-expr>"    # optional: skip step if check passes (idempotency)
        <type>: <value>
        with:                    # parameters (for builtin: and command: types)
          key: value
```

## Phase fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Unique phase key within the pipeline |
| `description` | string | optional | Shown in `deploy plan` output |
| `when` | string | — | Condition expression; phase skipped if falsy |
| `untracked` | bool | false | If true, phase steps are excluded from the step counter and produce no system output |
| `deploy_services` | bool | false | Orchestrator marker: CLI inlines per-service pipelines here in dependency order |

## Step fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique step key within the phase |
| `description` | string | Shown in `deploy plan` output |
| `when` | string | Condition expression; step skipped if falsy |
| `check` | string | Check expression; step skipped if check passes (idempotency gate) |

Exactly one execution type field must be set per step.

## Step execution types

### `run: <shell command>`

Executes a shell command via `sh -c`. Full shell semantics apply: environment variable expansion, globbing, pipes, redirection, and `&&`/`||` operators all work as expected.

```yaml
- name: chmod-scripts
  run: chmod +x scripts/deploy.sh
```

### `devbox: "<subcommand>"`

Invokes a devbox CLI subcommand. The binary path is resolved automatically.

```yaml
- name: up
  devbox: "docker up"

- name: info
  devbox: "info"

- name: render-ide
  devbox: "render ide main"
```

### `command: <id>`

Dispatches a declarative command by ID from the command registry (`devbox/commands/`).

```yaml
- name: composer-install
  command: services.main.composer-install

- name: db-create
  command: services.main.db.create
  with:
    database: laravel_test
```

### `builtin: <name>`

Executes an engine-internal Go function. Builtins run in-process and have access to the full config.

```yaml
- name: create-dirs
  builtin: service_dirs_ensure
  with:
    service: main
    mode: skip

- name: success-msg
  builtin: message
  with:
    level: success
    text: "Deploy completed"
```

## Available builtins

### `service_dirs_ensure`

Creates service hub directories.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key from `services.yml` |
| `mode` | string | `skip` | `skip`, `error`, or `recreate` |

Resolved dir list: `[src, configs]` + `ServiceConfig.Dirs` (from `services.yml`).

Mode behavior:

| Mode | Dir missing | Dir exists | Non-dir at path |
|------|-------------|------------|----------------|
| `skip` | create | no-op | error |
| `error` | create | error | error |
| `recreate` | create | remove + create | error |

Safety: `src` and `configs` always use `skip` semantics in `recreate` mode (never removes source code).

### `service_configs_copy`

Copies template config files into the service hub.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key |
| `mode` | string | `default` | `default`, `update`, or `replace` |

### `message`

Prints a message to deploy output.

| Parameter | Type | Description |
|-----------|------|-------------|
| `level` | string | `info`, `success`, `warning`, or `error` |
| `text` | string | Message text; supports Go template expressions against `DevboxConfig` |

```yaml
- name: done
  builtin: message
  with:
    level: success
    text: "Deploy of {{ .Project.Name }} completed"
```

### `confirm`

Prompts the user for confirmation before continuing. Skipped when `--yes` flag is set.

| Parameter | Type | Description |
|-----------|------|-------------|
| `message` | string | Prompt text |

### `docker_remove_project_volumes`

Removes all Docker volumes belonging to the project (uses project name label).

### `remove_paths`

Removes paths from the filesystem.

| Parameter | Type | Description |
|-----------|------|-------------|
| `paths` | list | Host-relative paths to remove |

## Conditions (`when` / `check`)

`when:` expressions are evaluated against the merged DevboxConfig. Common patterns:

```yaml
when: "dir-empty services/main/src"    # true if directory exists and is empty
when: "{{ .Services.Second.Enabled }}" # template expression against config
```

`check:` expressions determine idempotency. Step is skipped if the check passes:

```yaml
check: "cmd: ./bin/devbox deploy config-check main"  # skip if command exits 0
```

## Post-deploy semantics

The `post-deploy` phase (by convention, the last phase in `deploy.yml`) runs only if all prior phases succeed. This is not magic — it follows the existing behavior where deploy aborts on first failure. Name the final summary phase `post-deploy` and it naturally benefits from this.

Use `untracked: true` on the `post-deploy` phase to suppress system step messages (the builtin steps produce their own output via the message level):

```yaml
- name: post-deploy
  description: Post-deploy summary
  untracked: true
  steps:
    - name: info
      devbox: "info"
    - name: success
      builtin: message
      with:
        level: success
        text: Deploy completed successfully
```

## `deploy_services` marker

In `deploy.yml`, a phase with `deploy_services: true` is a placeholder. The CLI replaces it with the inlined per-service pipelines at runtime, ordered by dependency (`depends_on` in `services.yml`). Only enabled services are included.

```yaml
phases:
  - name: services
    deploy_services: true
    description: Deploy all enabled services
```

## Example: orchestrator pipeline

```yaml
# devbox/deploy.yml
phases:
  - name: services
    deploy_services: true
    description: Deploy all enabled services

  - name: start
    description: Start containers
    steps:
      - name: up
        devbox: "docker up"
      - name: wait-healthy
        devbox: "docker wait"

  - name: post-deploy
    description: Post-deploy summary
    untracked: true
    steps:
      - name: info
        devbox: "info"
      - name: success
        builtin: message
        with:
          level: success
          text: Deploy completed successfully
```

## Example: per-service pipeline

```yaml
# devbox/deploy/main.yml
phases:
  - name: setup
    description: Create dirs and install
    when: "dir-empty services/main/src"
    steps:
      - name: create-dirs
        builtin: service_dirs_ensure
        with:
          service: main
      - name: install
        command: app.install
      - name: copy-configs
        builtin: service_configs_copy
        with:
          service: main
          mode: replace

  - name: init
    description: Initialize application
    steps:
      - name: db-create
        command: services.main.db.create
      - name: composer-install
        command: services.main.composer-install
      - name: migrate
        command: services.main.migrate

  - name: finalize
    description: Generate IDE config
    steps:
      - name: render-ide
        devbox: "render ide main"
```

## Common pitfalls

- **Using `make:` step type** — removed. Replace with `command:` referencing a YAML command definition.
- **Direct `docker compose` in `run:`** — use `devbox:` with a `docker` subcommand instead. Docker policy (project name, args) is applied automatically.
- **Missing `with:` for builtin parameters** — builtins require `with:` for their parameters; passing them as top-level step fields does not work.

## Related commands

- `devbox deploy plan` — show resolved pipeline (with inlined service phases)
- `devbox deploy run` — execute deploy pipeline
- `devbox reset plan` — show reset pipeline
- `devbox reset run [--yes]` — execute reset pipeline

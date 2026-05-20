# deploy.yml / reset.yml

Deploy and reset pipeline declarations.

## Contents

- [Purpose](#purpose)
- [File roles](#file-roles)
- [Structure](#structure)
- [Top-level fields](#top-level-fields)
- [Phase fields](#phase-fields)
- [Step fields](#step-fields)
- [Step execution types](#step-execution-types)
  - [`type: shell`](#type-shell)
  - [`type: devbox`](#type-devbox)
  - [`type: command`](#type-command)
  - [`type: builtin`](#type-builtin)
- [Available builtins](#available-builtins)
  - [`service_dirs_ensure`](#service_dirs_ensure)
  - [`service_configs_copy`](#service_configs_copy)
  - [`service_configs_check`](#service_configs_check)
  - [`message`](#message)
  - [`confirm`](#confirm)
  - [`docker_remove_project_volumes`](#docker_remove_project_volumes)
  - [`remove_paths`](#remove_paths)
- [Conditions and checks](#conditions-and-checks)
  - [`when:` (pre-condition)](#when-pre-condition)
  - [`check:` (post-condition)](#check-post-condition)
  - [`files_gate:` (pre-condition for files)](#files_gate-pre-condition-for-files)
- [Post-deploy semantics](#post-deploy-semantics)
- [`deploy_services` marker](#deploy_services-marker)
- [Example: orchestrator pipeline](#example-orchestrator-pipeline)
- [Example: per-service pipeline](#example-per-service-pipeline)
- [Parallel step groups](#parallel-step-groups)
- [Common pitfalls](#common-pitfalls)
- [Related commands](#related-commands)

## Purpose

`devbox/deploy.yml` declares the orchestrator deploy pipeline. `devbox/reset.yml` declares the destructive reset pipeline. Per-service deploy pipelines live in `devbox/deploy/<service>.yml`.

All three are loaded separately by `LoadDeployConfig()` and are not merged with the 3-layer config.

## File roles

| File | Loader | Role |
|------|--------|------|
| `devbox/deploy.yml` | `LoadDeployConfig` | Top-level orchestrator: lists phases in order, references service pipelines |
| `devbox/deploy/<svc>.yml` | `LoadServiceDeployConfigs` | Per-service phases and steps (inlined by orchestrator at `deploy_services: true`) |
| `devbox/reset.yml` | `LoadResetConfig` | Separate reset pipeline, executed via `devbox reset run`. `deploy_services` phases are rejected. |

```mermaid
flowchart TB
  D[devbox/deploy.yml] -->|phase: deploy_services| INL{Inline enabled services}

  subgraph svc["devbox/deploy/&lt;service&gt;.yml — one file per service"]
    direction TB
    S1["mandatory service<br/>(always inlined)"]
    S2["optional service A<br/>(inlined when enabled)"]
    S3["optional service B<br/>(inlined when enabled)"]
    SN["…N services"]
  end

  svc --> INL
  INL -->|topo-sorted by depends_on| PLAN[Resolved plan]
  PLAN --> RUN[(PlainReporter — ✓ ✗ ◎ ·<br/>.devbox/logs/deploy.log)]

  R[devbox/reset.yml] --> RPLAN[Resolved plan] --> RUN2[(PlainReporter)]
```

Every service declared in `services.yml` may contribute its own `devbox/deploy/<name>.yml`. At plan time the orchestrator filters that set down to enabled services (mandatory ones are always enabled) and inlines them in topological `depends_on` order. Services without a deploy file are silently skipped — not every service needs one.

## Structure

```yaml
log: true                          # optional: tee output to .devbox/logs/<pipeline>.log

phases:
  # Normal phase: supports when, untracked, and steps
  - name: <phase-name>
    description: Human-readable description
    when:                          # optional: pre-condition (typed condition)
      type: builtin|shell|template
      cmd: <string>                # for builtin/shell
      expr: <string>               # for template
    untracked: true                # optional: suppress step output for this phase
    steps:
      - name: <step-name>
        description: Human-readable description
        type: shell|devbox|command|builtin  # execution type (required)
        cmd: <value>               # command payload (required)
        when:                      # optional: pre-condition (typed condition)
          type: builtin|shell|template
          cmd: <string>            # for builtin/shell
          expr: <string>           # for template
        check:                     # optional: post-condition (typed action)
          type: shell|devbox|command|builtin
          cmd: <value>
          with:                    # optional: parameters
            key: value
        continue_on_error: true    # optional: failure does not abort the pipeline
        skip_confirm: true         # optional: bypass confirmation prompts for this step
        files_gate: readable       # short form: state must be readable|missing
        # or long form:
        files_gate:
          state: readable|missing  # required
          command: <cmd-id>        # default: step.cmd (only valid for type: command)
          require: required|all|[id1, id2]  # default: required
          with:                    # default: step.with
            key: value
        with:                      # parameters (for command and builtin types)
          key: value

  # deploy_services phase (deploy.yml only): no steps or when allowed
  - name: services
    description: Human-readable description
    deploy_services: true          # orchestrator marker; mutually exclusive with steps and when
```

## Top-level fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `log` | bool | `deploy.yml`: `true`; `reset.yml`: `false` | Tee devbox status messages and child stdout/stderr to `.devbox/logs/<pipeline>.log` (ANSI codes stripped). |
| `phases` | list | — | Ordered list of phases. |

## Phase fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Unique phase key within the pipeline |
| `description` | string | optional | Shown in `deploy plan` output |
| `when` | typed condition | — | Pre-condition; phase skipped if falsy (see [Conditions and checks](#conditions-and-checks)). Not allowed on `deploy_services` phases. |
| `untracked` | bool | false | If true, phase steps are excluded from the step counter and produce no system output |
| `deploy_services` | bool | false | Orchestrator marker: CLI inlines per-service pipelines here in dependency order. A `deploy_services` phase must not contain `steps` or a `when` condition — both are hard errors at load time. |

## Step fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique step key within the phase |
| `description` | string | Shown in `deploy plan` output |
| `type` | enum | Execution type: one of `shell`, `devbox`, `command`, `builtin` (required) |
| `cmd` | string | Command payload (required); content depends on `type` |
| `with` | mapping | Parameters passed to command or builtin (optional; required for most builtins) |
| `when` | typed condition | Pre-condition evaluated before the step runs; step skipped if falsy |
| `check` | typed action | Post-condition evaluated after the step succeeds; pipeline aborts when the action fails. Skipped when `continue_on_error: true` and the step failed. |
| `files_gate` | typed gate | Pre-condition based on file existence/absence from a command's `files:` block. Step skipped if unsatisfied. See [`files_gate:` (pre-condition for files)](#files_gate-pre-condition-for-files). |
| `continue_on_error` | bool | When `true`, a failed step is reported via `FailStep` (red ✗) but the pipeline does not abort. The post-step `check` and the next-step hook are skipped for the failed step. Useful for optional hook phases — see [lifecycle.yml](lifecycle.md). **Behavior change:** when the step body succeeds but `check:` fails, and `continue_on_error: true`, the step is reported as failed and the pipeline continues to the next step (symmetric with body-failure semantics). |
| `skip_confirm` | bool | When `true`, bypasses confirmation prompts for this step only — equivalent to a per-step `-y` / `--yes`. Propagates to the step body and its `check:` action. ORed with the pipeline-wide skip-confirm flag, so the step is non-interactive whenever either is set. Useful when most of the pipeline is interactive but one step (e.g. a `confirm` builtin guarding an idempotent action, or a command that re-prompts internally) should always proceed. |

## Step execution types

### `type: shell`

Executes a shell command via `sh -c`. Full shell semantics apply: environment variable expansion, globbing, pipes, redirection, and `&&`/`||` operators all work as expected.

```yaml
- name: chmod-scripts
  type: shell
  cmd: chmod +x scripts/deploy.sh
```

### `type: devbox`

Invokes a devbox CLI subcommand. The binary path is resolved automatically.

```yaml
- name: up
  type: devbox
  cmd: "docker up"

- name: info
  type: devbox
  cmd: "info"

- name: render-ide
  type: devbox
  cmd: "render ide main"
```

### `type: command`

Dispatches a declarative command by ID from the command registry (`devbox/commands/`).

```yaml
- name: composer-install
  type: command
  cmd: services.main.composer-install

- name: db-create
  type: command
  cmd: services.main.db.create
  with:
    database: laravel_test
```

### `type: builtin`

Executes an engine-internal Go function. Builtins run in-process and have access to the full config. The same registry is reachable from declarative commands via [`type: builtin` in `commands/`](commands.md#type-builtin) — pipelines and commands share one set of builtins.

```yaml
- name: create-dirs
  type: builtin
  cmd: service_dirs_ensure
  with:
    service: main
    mode: skip

- name: success-msg
  type: builtin
  cmd: message
  with:
    level: success
    text: "Deploy completed"
```

## Available builtins

The full registry lives in `internal/builtin/`. Eight builtins ship today:

| Builtin | Purpose |
|---------|---------|
| `service_dirs_ensure` | Create service hub directories |
| `service_configs_copy` | Copy template config files into the service hub |
| `service_configs_check` | Verify that template config files exist in the service hub |
| `message` | Print a styled message at info/success/warning/error level |
| `confirm` | Interactive Y/n prompt (skipped under `--yes`) |
| `docker_remove_project_volumes` | Remove all volumes whose name is prefixed with the compose project name |
| `docker_wait_healthy` | Wait for Docker containers to reach healthy state |
| `remove_paths` | Delete project-relative paths from the filesystem |

### `service_dirs_ensure`

Creates service hub directories.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service` | string | required | Service key from `services.yml` |
| `mode` | string | `skip` | `skip`, `error`, or `recreate` |

Resolved dir list: `[src, configs]` + `ServiceConfig.Dirs` (from `services.yml`). Each entry must be a non-empty relative path that does not escape the service `dir`.

Mode behavior:

| Mode | Dir missing | Dir exists | Non-dir at path |
|------|-------------|------------|----------------|
| `skip` | create | no-op | error |
| `error` | create | error | error |
| `recreate` | create | remove + create | error |

Safety: `src` and `configs` always use `skip` semantics in `recreate` mode (never removes source code or templated configs).

### `service_configs_copy`

Copies template config files from `configs/services/<service>/` into `services/<service>/configs/`.

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

### `service_configs_check`

Verifies that all template config files declared in `services.yml` exist in the service hub after a `service_configs_copy` step. Use as a `check:` action to assert that configs were successfully deployed.

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

### `message`

Prints a message to deploy output.

| Parameter | Type | Description |
|-----------|------|-------------|
| `level` | string | `info`, `success`, `warning`, or `error` (required) |
| `text` | string | Message text (required); supports [Go template expressions](../templates.md) against `DevboxConfig` |

```yaml
- name: done
  type: builtin
  cmd: message
  with:
    level: success
    text: "Deploy of {{ .Project.Name }} completed"
```

### `confirm`

Prompts the user for confirmation before continuing. Skipped when `--yes` flag is set or when the step is being confirmed via `ExecContext.SkipConfirm`. On a TTY uses huh.Confirm; on a piped/CI stdin falls back to plain Y/n.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `message` | string | `Are you sure?` | Prompt text |
| `ok_msg` | string | `Continuing` | Success message after confirmation |
| `stop_msg` | string | `Aborted` | Error message after rejection / Esc |

### `docker_remove_project_volumes`

Removes all Docker volumes whose name starts with `<project_name>_` (resolved from `docker.yml` against the merged config). No parameters. Aborts if the resolved project name is empty.

### `docker_wait_healthy`

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

### `remove_paths`

Removes paths from the filesystem.

| Parameter | Type | Description |
|-----------|------|-------------|
| `paths` | list of strings | Project-relative paths to remove. Each must be relative and must not escape the project root; absolute paths and `..` traversal are rejected at validate time. |

## Conditions and checks

### `when:` (pre-condition)

`when:` is a **pre-condition** evaluated before a phase or step runs. A falsy result skips the phase/step without executing it. It is a **typed condition** with three forms:

**Builtin predicates** — test filesystem state using the predicate registry:

```yaml
when:
  type: builtin
  cmd: "dir-empty services/main/src"
```

Available predicates: `dir-exists`, `dir-missing`, `dir-empty`, `dir-not-empty`, `file-exists`, `file-missing`. These are distinct from the *engine builtins* (`service_configs_copy`, etc.) used in step bodies and `check:` actions; see [conditions.md](conditions.md) for the full distinction. The predicate registry uses hardcoded `sh -c` for POSIX portability regardless of the project's configured shell.

**Shell commands** — execute a shell command; exit 0 = true, non-zero = false:

```yaml
when:
  type: shell
  cmd: "test -f services/main/src/vendor/autoload.php"
```

Shell commands also use hardcoded `sh -c` (not `ShellBin`) for portability.

**Template expressions** — Go template syntax evaluated at plan time against the merged `DevboxConfig`:

```yaml
when:
  type: template
  expr: "{{ .Services.second.Enabled }}"
```

Template conditions do not support `check:` in the same step (no side effects at plan time). They are purely for idempotency checks like "skip this phase if the feature is not enabled" where the result is known before execution. See [Templates](../templates.md) for the full template surface (helpers, sprout registries, `appURL`).

### `check:` (post-condition)

`check:` is a **post-action** evaluated after a step succeeds. It is a **typed action** — the same `type:` / `cmd:` / `with:` shape as step bodies, but its success/failure determines whether the step is reported as passed or failed.

Use `check:` to assert that a step had its intended effect — e.g. that a migration produced a certain file, that a service became reachable, or that configs were copied successfully.

**Example: verify configs were deployed**

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

**Example: verify a shell command produces expected output**

```yaml
- name: run-migration
  type: command
  cmd: services.main.migrate
  check:
    type: shell
    cmd: "test -f services/main/src/migrations/.done"
```

**Behavior of `continue_on_error` with `check:`:**

- When a step body fails and `continue_on_error: true` is set, `check:` is **not** evaluated. The step is reported as failed and the pipeline continues.
- When a step body succeeds but `check:` fails, the step is reported as failed. If `continue_on_error: true`, the pipeline continues; otherwise it aborts. **This is a behavior change from prior versions**, where check failure always aborted regardless of `continue_on_error`.

### `files_gate:` (pre-condition for files)

`files_gate:` probes for the **existence or absence** of files before running a step. Unlike `when:` (which is a generic predicate) or `check:` (which validates after success), `files_gate:` decides whether to run based on **the same `files:` block declared in a command definition** — making the command's file spec the single source of truth.

**Use case:** skip a deployment step if the artifact already exists, or run it only when a pre-fetched cache is present. Example: "dump the database only if a dump file doesn't already exist" (producer step with `state: missing`), or "load the cache only if it was pre-fetched" (consumer step with `state: readable`).

**Short form:**

```yaml
- name: db-dump
  type: command
  cmd: services.main.db.dump
  files_gate: readable              # runs iff dump file exists
```

**Long form:**

```yaml
- name: db-load
  type: command
  cmd: services.main.db.load
  files_gate:
    state: missing                  # required: runs iff dump file does NOT exist
    command: services.main.db.dump  # optional: target command (default: step.cmd)
    require: all                    # optional: which files to probe (default: required)
    with:                           # optional: params for file resolution (default: step.with)
      database: test_db
```

**Field reference:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `state` | `readable` \| `missing` | (required) | `readable`: runs iff **all** selected files resolve (file exists). `missing`: runs iff **none** do. |
| `command` | string | `step.cmd` | Target command ID whose `files:` block is probed. When not specified, the step's own `cmd` is used (self-probe). |
| `require` | string \| list | `required` | Which files participate in the probe: `required` (files marked `required: true` or `read_write`), `all` (all readable files), or explicit list `[id1, id2]`. |
| `with` | mapping | `step.with` | Parameter overrides for file resolution templates. Merged with `step.with` for the targeted command. |

**Semantics:**

- **No file errors → skip**, not fail. If `state: readable` and no files match, the step is skipped (not failed). Configuration errors (bad template, bad glob, missing params) do produce an error and fail the step.
- **AND'ed with `when:`** — both must be satisfied for the step to run. If `when:` is false, the gate is never evaluated (short-circuits). If `when:` is true but the gate is unsatisfied, the step is skipped.
- **Journal-skip bypass** — steps with `files_gate:` **always re-evaluate the gate on every deploy**, bypassing the journal's "already deployed" skip optimization. This ensures a producer step with `state: missing` re-runs after its artifact is deleted between deploys. This is the deliberate trade-off: gate re-evaluation vs. idempotency caching. (Gateless steps remain idempotency-cached as before.)
- **Probe scope** — only files with `access: read` or `access: read_write` participate. Files with `access: write` only are rejected at plan-time validation if listed in the gate's `require:` spec.

**Before and after example:**

*Without `files_gate:` — duplicated glob+regex logic:*

```yaml
# Deploy step: hard-coded shell condition duplicating the command's file logic
- name: dump-download
  type: command
  cmd: services.main.db.dump-download
  when:
    type: shell
    cmd: "test -f services/main/.backups/dump_*.sql.gz"  # duplicated glob logic
```

*With `files_gate:` — single source of truth:*

```yaml
# Deploy step: references the command's canonical file spec
- name: dump-download
  type: command
  cmd: services.main.db.dump-download
  files_gate: readable                # probes the dump_*.sql.gz from command definition
```

The command definition once:

```yaml
# devbox/commands/services/main/db.yml
commands:
  dump-download:
    type: shell
    files:
      dump:
        access: read
        candidates:
          - glob: "services/main/.backups/dump_*.sql.gz"
            sort: modtime_desc
        required: true
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
      type: devbox
      cmd: "info"
    - name: success
      type: builtin
      cmd: message
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
        type: devbox
        cmd: "docker up"
      - name: wait-healthy
        type: builtin
        cmd: docker_wait_healthy

  - name: post-deploy
    description: Post-deploy summary
    untracked: true
    steps:
      - name: info
        type: devbox
        cmd: "info"
      - name: success
        type: builtin
        cmd: message
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
    when:
      type: builtin
      cmd: "dir-empty services/main/src"
    steps:
      - name: create-dirs
        type: builtin
        cmd: service_dirs_ensure
        with:
          service: main
      - name: install
        type: command
        cmd: app.install
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

  - name: init
    description: Initialize application
    steps:
      - name: db-create
        type: command
        cmd: services.main.db.create
      - name: composer-install
        type: command
        cmd: services.main.composer-install
      - name: migrate
        type: command
        cmd: services.main.migrate

  - name: finalize
    description: Generate IDE config
    steps:
      - name: render-ide
        type: devbox
        cmd: "render ide main"
```

## Parallel step groups

A step may declare a `parallel:` block instead of a leaf body (`type` + `cmd`). The orchestrator runs the inner sub-steps concurrently via `errgroup` + a semaphore, waits for all of them, and aggregates results. Same model applies to `lifecycle.yml` and `reset.yml`.

```yaml
phases:
  - name: init
    steps:
      - name: db-dumps
        when: ...                  # optional; evaluated once before the group
        skip_confirm: true         # optional; OR-merged into every sub-step
        parallel:
          max_concurrent: 4        # optional; default = min(NumCPU, len(steps))
          fail_fast: true          # optional; default true
          steps:                   # required, >= 2
            - name: download-main
              type: command
              cmd: services.main.db.dump-download
              files_gate: { ... }
            - name: download-stock
              type: command
              cmd: services.stock.db.dump-download
            - name: download-price
              type: command
              cmd: services.price.db.dump-download
```

### Schema

Group-level keys allowed on a step with `parallel:`:

| Field | Type | Notes |
|---|---|---|
| `name` | string | Required. Group name (used in plan output and reporter headers). |
| `description` | string | Optional. |
| `when` | condition | Evaluated **once** before launching sub-steps. On false the whole group is skipped and each sub-step is recorded as skipped in the journal. |
| `skip_confirm` | bool | OR-merged into every sub-step at resolve time. A sub-step's own `skip_confirm: false` cannot un-set the inherited true (monotonic). |
| `parallel.max_concurrent` | int | Default `min(runtime.NumCPU(), len(steps))`; capped to `len(steps)` when larger. |
| `parallel.fail_fast` | bool (tristate) | Default `true`. When true, the first sub-step failure cancels siblings via `context`. When false, all sub-steps run to completion and errors are joined. |
| `parallel.steps` | []DeployStep | Required, >= 2 entries. |

Leaf-only directives are **rejected** on a group step: `type`, `cmd`, `with`, `check`, `files_gate`, `continue_on_error`. The YAML loader returns a strict-decode error naming the offending field. Unknown fields under `parallel:` itself (e.g. typo `max_concurent`) are likewise rejected.

### Defaults and validation

| Rule | Diagnostic |
|---|---|
| Nested `parallel:` (sub-step has its own `parallel:`) | error — flat groups only in v1 |
| `parallel.steps` < 2 | error — use a leaf step if only one item |
| Sub-step missing `name` | error |
| Duplicate sub-step names within a phase (across groups + leaf steps) | error — the journal keys entries by `(phase, name)` |
| Interactive prompt in sub-step without `skip_confirm: true` (sub-step or inherited from group) | error — covers `confirmation: true` in the target command, `builtin: confirm`, and workflows recursively containing a confirm-step |
| `service_run` sub-step with TTY-allocating compose args | warning — TTY is never allocated in parallel mode |

Validation runs at `devbox validate` and at plan resolution; either path catches misconfigurations before execution.

### Execution semantics

- **Cancellation**: `errgroup.WithContext` carries the parent `context.Context` through every runner (`HostRunner`, `DevboxRunner`, `ServiceExecRunner`, `ServiceRunRunner`, `ScriptRunner`, `WorkflowRunner`) and every builtin. Shell-out children are spawned with `exec.CommandContext` and bound to `cmd.Cancel = SIGTERM` + `cmd.WaitDelay = 5s` — on cancel, the child receives SIGTERM first (graceful shutdown), then SIGKILL after the delay. The Go-side builtin `docker_wait_healthy` aborts its poll loop on `ctx.Done()` within one tick.
- **SIGINT**: `RunWithOptions` installs a `signal.NotifyContext(SIGINT, SIGTERM)` on the parent context once per pipeline run. A user Ctrl-C cancels the context, which propagates to every active sub-step's child process. No orphaned `docker compose` / `sleep` processes after a clean shutdown.
- **`fail_fast: true`**: first failing sub-step (not counting `continue_on_error: true` ones) cancels the group; remaining sub-steps observe `ctx.Done()` and their children are killed. The group's error is the first failure wrapped with its sub-step address.
- **`fail_fast: false`**: all sub-steps run to completion. Errors are wrapped per-sub-step (`parallel sub-step "phase/group/sub": <err>`) and combined via `errors.Join`.
- **Per-sub-step `when` / `files_gate` / journal-skip**: each sub-step still runs through the same `step-when → files_gate → journal-skip (only when gate is nil) → ExecAction → check` pipeline. Group-`when` is evaluated **once**; per-sub-step `when` is **also** evaluated inside the goroutine.
- **Journal**: each sub-step is journaled independently under `(phase, sub-step.Name)`. The group itself is not journaled. `journal.StepHash(step)` is computed from the sub-step alone, so reordering or adding sub-steps does not invalidate siblings.

### Reporter and logging

- **Live view (TTY)**: the reporter drives a `LiveLine` sticky footer (`bubbles/v2/spinner` + pipeline `[<elapsed>]` stopwatch + `[N/M] <step>` text) for the whole pipeline, and switches to a `LiveBlock` while a parallel group is active. Each block row shows `<spinner-or-final-glyph> [<sub-elapsed>] [<pipelineIdx>/<pipelineTotal>] <sub-name>`, with the per-row spinner replaced by ✓/✗/◎ (in green/red/yellow) when the sub-step settles. The pipeline-wide `[N/M]` index in block rows lets parallel sub-steps blend into the surrounding step counter rather than restarting from `[1/3]`. The live view is rendered via the bubbles `Model.View()` API plus a private `time.Ticker` — `tea.NewProgram` is NOT used (so the terminal stays in cooked mode, Ctrl+C still raises SIGINT via VINTR, and no capability queries or kitty-keyboard sequences are emitted).
- **Sequential vs parallel routing**:
  - Sequential step bodies pause the LiveLine via `Reporter.SuspendForExec` and the child writes to the host terminal directly (with a PTY when stdout is a TTY). Colors, cursor positioning and interactive UX work as on a bare shell; a `logSanitizer`-wrapped tee captures an ANSI-stripped copy to the on-disk log. `ResumeAfterExec` repaints the footer after the child exits.
  - Parallel sub-steps do NOT allocate a PTY — granting a child a PTY while stdin is the empty reader makes `docker compose exec/run` fail with "cannot attach stdin to a TTY-enabled container". Sub-step output flows through `ansiOnlyStripper` → `lineTee` → `Reporter.StepOutput` so the block row shows the latest `\n`-frame; the host terminal is owned by the LiveBlock.
- **Frame-aware parser**: the `\r`-aware `lineTee` parses each parallel sub-step's stream into `(frame, final)` callbacks; only the latest frame is shown on the live row, and `\r` frames are normalised to one-frame-per-line in log files via `logSanitizer` (ANSI stripped, `\r\n` collapsed to one `\n`, lone `\r` to `\n`).
- **Buffer dump policy**: on sub-step finish, the buffered full output of the sub-step is replayed between `───── output ─────` separators when the sub-step failed, or when no log file is enabled. On TTY success with logging enabled, the dump is suppressed and a `Full log: <path>` line is emitted instead. Non-TTY mode always dumps (and dumps are clean lines thanks to the frame parser).
- **Per-sub-step log files**: `.devbox/logs/parallel/<pipeline>/<group>/<sub>.log` captures the sub-step's full output (ANSI stripped, `\r`→`\n`). The global pipeline log (`.devbox/logs/<pipeline>.log`) receives every status line and every committed child line exactly once.
- **EndBlock semantics**: when a parallel group finishes, `LiveLine.EndBlock` ERASES the live footer that sat below the block (which would otherwise be frozen in scrollback showing a spinner mid-frame next to the last-started sub-step text) and paints a fresh single-line footer in its place. The finalised block rows (✓/✗/◎ + frozen elapsed) persist as scrollback.
- **Prompt handoff**: every `ui.Run*` huh prompt fires package-level hooks registered by `NewPlainReporter` (`ui.SetHuhHooks(live.Pause, live.Resume)`), so the footer is erased before the prompt renders and repainted after it returns. Sequential step bodies use the same `live.Pause`/`live.Resume` via `Reporter.SuspendForExec`/`ResumeAfterExec`.
- **Non-TTY parity**: when `term.IsTerminal(os.Stdout.Fd())` is false (CI, piped stdout) the live view is fully disabled (no ticker, no cursor sequences, no footer) but the frame-aware parser is still on, so CI dumps have no `\r`-spam.

### Plan output

`devbox deploy plan` renders parallel groups with a contiguous index range and indented sub-step lines:

```
[12-14/25] [parallel group: db-dumps (3 steps, max_concurrent=3, fail_fast=true)]
  [12/25]  download-main      command services.main.db.dump-download [files_gate]
  [13/25]  download-stock     command services.stock.db.dump-download
  [14/25]  download-price     command services.price.db.dump-download
```

### Restrictions (v1)

- No nested `parallel:` inside `parallel:`.
- No interactive confirmation in sub-steps (`confirmation: true`, `builtin: confirm`, workflow with `WorkflowStep.Confirm`). Set `skip_confirm: true` or restructure.
- No DAG / `depends_on` between sub-steps. Flat groups only.
- No auto-parallelisation flag — explicit YAML opt-in only.
- No PTY in sub-steps. `service_run` with `-it`-style compose args will fail at the child with "cannot allocate tty" (surfaces as a normal sub-step failure subject to `continue_on_error` / `fail_fast`).

## Common pitfalls

- **Missing `with:` for builtin parameters** — builtins require `with:` for their parameters; passing them as top-level step fields does not work.
- **`deploy_services` in `reset.yml`** — rejected at load time. The reset pipeline does not iterate services; if you need per-service cleanup, declare it explicitly in the reset phases.
- **Forgetting `log: false` for noisy reset runs** — reset defaults to `log: false`, deploy defaults to `log: true`. Set the field explicitly when you want behaviour different from the default.
- **Using `continue_on_error` to mask real failures in core phases** — it is meant for hook phases (pre/post). A failed `docker up` should always abort.
- **Confusing `when:` and `check:`** — `when:` is evaluated before the step runs (pre-condition); `check:` is evaluated after success (post-action). `when:` uses the typed `type: builtin|shell|template` / `cmd:` shape; `check:` uses the typed `type: shell|devbox|command|builtin` shape.
- **Duplicating file-probe logic in `when:` instead of using `files_gate:`** — If a step should run conditionally based on whether a file exists, use `files_gate:` instead of hard-coding globs in a shell `when:` condition. That way, edits to the command's `files:` definition automatically apply to the step's probe logic — they stay in sync. The `files_gate:` references the command's canonical file spec.

## Idempotent deploy and state

By default, `devbox deploy run` tracks the outcome and hash of every executed step in `.devbox/deploy/state.yml`. On the next deploy run, steps that succeeded with unchanged `action_hash` values are **skipped** (unless they have a `check:` action, which always runs to re-validate idempotency).

This makes deploys idempotent: re-running an unchanged project is fast (unchanged steps are skipped), while editing a step body automatically re-triggers it. Edits to `services.yml` or `deploy.yml` invalidate the affected scope and force those steps to re-run.

Key behaviors:

- **Step hash change** → step re-runs
- **Service config change** → all service steps re-run
- **Project config change** → all project-level steps re-run
- **Has `check:` action** → step always runs (even if hash matches), so the check re-validates idempotency
- **Has `files_gate:`** → gate is re-evaluated on every deploy; the journal skip is bypassed so the gate decision is always current (the journal still records the step for audit/status display using `step_hash` which includes the gate config)
- **Previous step failed** → step re-runs on next deploy (allows `--resume` to continue from the failure)

Use `devbox deploy state show` to inspect the journal, `devbox deploy state clear` to reset it, and `devbox deploy state repair` to fix corrupted aggregates.

See [state.md](state.md) for full details on hashing, skip decisions, and recovery from mid-deploy crashes.

## Related commands

- `devbox deploy plan` — show resolved pipeline (with inlined service phases)
- `devbox deploy run` — execute deploy pipeline with state tracking
- `devbox deploy step <phase> <step>` — run a single step (debugging)
- `devbox deploy state show` — inspect deploy state journal
- `devbox deploy state clear` — reset deploy state
- `devbox deploy state repair` — rebuild state aggregates
- `devbox reset plan` — show reset pipeline
- `devbox reset run [--yes]` — execute reset pipeline
- See also [lifecycle.yml](lifecycle.md) — `run` / `stop` pipelines reuse the same phase/step grammar with optional update probe and hook phases.

# lifecycle.yml

Run / stop pipeline declarations driving `devbox run`, `devbox stop`, and `devbox restart`.

## Contents

- [Purpose](#purpose)
- [Pipeline shape](#pipeline-shape)
- [Structure](#structure)
- [`run.update`](#runupdate)
- [`run.show_info` / `run.final_message`](#runshow_info--runfinal_message)
- [`stop.final_message`](#stopfinal_message)
- [`log` (file logging)](#log-file-logging)
- [Hook phases](#hook-phases)
- [Minimal example](#minimal-example)
- [Validation](#validation)
- [Parallel step groups](#parallel-step-groups)
- [Common pitfalls](#common-pitfalls)
- [Related commands](#related-commands)

## Purpose

`devbox/lifecycle.yml` declares two pipelines:

- **`run:`** — executed by `devbox run` (and `devbox restart`, after stop). Wraps the standard `docker up` + `docker wait` sequence with optional update probe and pre/post hook phases.
- **`stop:`** — executed by `devbox stop` (and the first half of `devbox restart`). Wraps `docker down` with optional pre/post hook phases.

It is loaded separately by `LoadLifecycleConfig()` and is **not** merged with the 3-layer config.

The file is optional for all commands that use it.

When `lifecycle.yml` is absent or a section is absent, Devbox substitutes a built-in default pipeline and prints one info line to stderr: `Using built-in default <run|stop> pipeline (override with devbox/lifecycle.yml).` The info line is suppressed in `--output json` mode.

**Default `run:` pipeline** (fires when `lifecycle.yml` is absent or has no `run:` section):

| Field | Value |
|-------|-------|
| `update.mode` | `off` (no git probe) |
| `show_info` | `true` |
| `final_message` | `Project is ready for work!` |
| Phases | Single `start` phase: one `type: devbox` step with `cmd: "docker up --wait"` |

**Default `stop:` pipeline** (fires when `lifecycle.yml` is absent or has no `stop:` section):

| Field | Value |
|-------|-------|
| `final_message` | `Project is stopped. Have a nice day!` |
| Phases | Auto-reap phase (see below) + single `stop` phase: one `type: devbox` step with `cmd: "docker down"` |

Whenever a `stop:` pipeline runs (default or user-defined), the `_auto_reap_daemons` phase is prepended automatically; it has no opt-out and is visible in plan output for transparency. It stops any background daemons started via [`type: daemon`](commands/types.md#type-daemon) commands.

`devbox docker up` and `devbox docker down` are thin Docker Compose passthroughs and never use this pipeline; raw `docker compose stop` / `restart` remain accessible via `devbox docker stop` / `devbox docker restart`.

## Pipeline shape

```mermaid
flowchart LR
  subgraph run["devbox run"]
    direction LR
    U[update probe] --> PRE[pre hooks] --> UP[docker up] --> WAIT[docker wait] --> POST[post hooks] --> INFO[info] --> MSG1[final_message]
  end
  subgraph stop["devbox stop"]
    direction LR
    SPRE[pre hooks] --> DOWN[docker down] --> SPOST[post hooks] --> MSG2[final_message]
  end
  subgraph restart["devbox restart"]
    direction LR
    R1[stop pipeline] --> R2[run pipeline<br/>--no-update]
  end
```

`docker up` is issued as a `type: devbox` step with `cmd: "docker up"` inside the `start` phase. Container health waiting uses a `type: builtin` step with `cmd: docker_wait_healthy`. They are not magical — the pipeline executor invokes them like any other step, so they pick up policy from `docker.yml`.

## Structure

```yaml
run:
  update:
    mode: on            # on | off
  show_info: true
  final_message: "Project is ready for work!"
  log: false            # tee status + child stdout/stderr to .devbox/logs/run.log
  phases:
    - name: <phase>
      description: <text>
      when:             # optional: typed condition (see deploy/conditions.md)
        type: builtin|shell|template
        cmd: <string>
        expr: <string>
      steps:
        - name: <step>
          type: shell|devbox|command|builtin
          cmd: <value>
          with:         # optional: parameters
            key: value

stop:
  final_message: "Project is stopped. Have a nice day!"
  log: false            # tee status + child stdout/stderr to .devbox/logs/stop.log
  phases:
    - name: <phase>
      description: <text>
      when:             # optional: typed condition
        type: builtin|shell|template
        cmd: <string>
        expr: <string>
      steps:
        - name: <step>
          type: shell|devbox|command|builtin
          cmd: <value>
          with:         # optional: parameters
            key: value
```

Phases and steps use the same shape as [deploy.yml](deploy/index.md): `name`, `description`, `when`, `untracked`, `steps[]`, plus per-step `type` / `cmd` / `with`, `when`, `check`, `files_gate`, `continue_on_error`. See the deploy reference for the complete step grammar, including [`files_gate:` (pre-condition for files)](deploy/conditions.md#files_gate-pre-condition-for-files).

`deploy_services: true` is **not** allowed in lifecycle pipelines.

## `run.update`

The optional update probe runs before any phase. It can fetch from the upstream remote, detect drift, and (depending on `mode`) pull `--ff-only`. A successful pull triggers in-process reload of `DevboxConfig`, `LifecycleConfig`, and the command registry before phases execute.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `update.mode` | string | `on` (when the `update:` block is present) | One of `on`, `off`. Writing the `update:` key is itself the opt-in. |

Mode behaviour:

| Mode | Fetches | Pulls | Behaviour when behind |
|------|---------|-------|------------------------|
| `on` | yes | with consent | Prompts before pulling; on non-TTY, checks for upstream drift and warns (check semantics). |
| `off` | no | no | Probe disabled (same as `--no-update` flag). |

Layered precedence at runtime: `--no-update` flag > `--update <mode>` flag > `EffectiveMode()` from YAML.

When the probe finds a dirty tree, no upstream, or a fetch failure it warns and continues — the run pipeline is never blocked by the probe.

## `run.show_info` / `run.final_message`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `show_info` | bool | `false` | Append a `devbox info` render after the last phase. |
| `final_message` | string | `Project is ready for work!` | Success message printed at the very end. |

## Required service deployment gate

`devbox run` automatically gates on required services being deployed. Before the run pipeline starts, the command checks that all **tracked** services (those appearing in the resolved deploy plan) have `status: deployed` in the state file.

If any tracked service is not yet deployed, `devbox run` exits with an error: "run `devbox deploy run` first". This prevents running against a partially-initialized environment — bypassing the gate would just hand `docker compose up` a service whose volumes/configs/database have never been provisioned, and the run would fail almost immediately with an unrelated error. Always deploy first.

For more details, see [state/index.md](state/index.md).

## `stop.final_message`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `final_message` | string | `Project is stopped. Have a nice day!` | Success message printed at the very end. |

## `log` (file logging)

Top-level field on both `run:` and `stop:`. Defaults to `false` for lifecycle pipelines (in contrast to `deploy.yml`, where the default is `true`).

When enabled, devbox status messages and child-process stdout/stderr are teed to `.devbox/logs/<name>.log` (with ANSI codes stripped) — `.devbox/logs/run.log` for run, `.devbox/logs/stop.log` for stop.

```yaml
run:
  log: true     # tee to .devbox/logs/run.log
```

## Hook phases

Hook phases are conventional names (`pre` / `post`) used to wrap the standard start/stop work. Add `continue_on_error: true` on each step so a failed hook does not abort the main lifecycle:

```yaml
run:
  phases:
    - name: pre
      description: Before-run hooks (continue on failure)
      steps:
        - name: before-run
          type: command
          cmd: project.before-run
          continue_on_error: true

    - name: start
      description: Start containers and wait for health
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
        - name: wait
          type: builtin
          cmd: docker_wait_healthy

    - name: post
      description: After-run hooks (continue on failure)
      steps:
        - name: after-run
          type: command
          cmd: project.after-run
          continue_on_error: true
```

`continue_on_error: true` causes the failure to be reported via `FailStep` (red ✗), but execution moves to the next step and the post-step `check` is not evaluated.

## Minimal example

```yaml
# devbox/lifecycle.yml
run:
  update:
    mode: on
  show_info: true
  final_message: "Project is ready for work!"
  phases:
    - name: start
      description: Start containers and wait for health
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
        - name: wait
          type: builtin
          cmd: docker_wait_healthy

stop:
  final_message: "Project is stopped. Have a nice day!"
  phases:
    - name: stop
      description: Stop and remove containers
      steps:
        - name: down
          type: devbox
          cmd: "docker down"
```

## Validation

`LoadLifecycleConfig()` enforces:

- Each step in `run.phases` and `stop.phases` has a `type:` field with one of `shell`, `devbox`, `command`, `builtin`.
- `update.mode`, when set, is one of `on`, `off`. Old values (`prompt`, `auto`, `check`) are rejected with a clear error.
- `update.enabled` is not allowed (removed in favor of `mode: off` to disable the probe).
- `deploy_services: true` is rejected (only valid in `deploy.yml`).
- `final_message` and `log` are normalized to defaults when absent.

## Parallel step groups

Lifecycle phases use the same `parallel:` step-group container as `deploy.yml`. A step may declare `parallel: { max_concurrent, fail_fast, steps }` instead of a leaf body, and the inner sub-steps run concurrently with the same cancellation, journal, and reporter semantics. See [deploy → Parallel step groups](deploy/examples.md#parallel-step-groups) for the schema, defaults, validation rules, and execution model.

## Common pitfalls

- **Forgetting `continue_on_error: true` on hook steps** — without it, a failing pre-stop hook aborts the entire stop sequence and containers are never stopped.
- **Using `update: {}` with `enabled: true`** — the `enabled` field is no longer supported. Writing the `update:` key is itself the opt-in; use `mode: off` to disable the probe, or omit the `update:` key entirely.
- **Adding `deploy_services` phases** — they are deploy-only. Lifecycle pipelines call services via `type: command` references instead.
- **Editing `lifecycle.yml` to use direct `docker compose` calls** — the public API is `type: devbox` with `cmd: "docker up"`. Direct `docker compose` calls bypass policy from `docker.yml`.

## Related commands

- `devbox run` — execute the run pipeline (with optional update probe)
- `devbox run --no-update` — skip the update probe
- `devbox run --update <mode>` — override the configured mode
- `devbox stop` — execute the stop pipeline
- `devbox restart` — `stop`, then `run --no-update`
- `devbox docker up` / `devbox docker down` — raw Docker Compose passthrough (does not use this pipeline)

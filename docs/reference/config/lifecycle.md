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
- [Common pitfalls](#common-pitfalls)
- [Related commands](#related-commands)

## Purpose

`devbox/lifecycle.yml` declares two pipelines:

- **`run:`** — executed by `devbox run` (and `devbox restart`, after stop). Wraps the standard `docker up` + `docker wait` sequence with optional update probe and pre/post hook phases.
- **`stop:`** — executed by `devbox stop` (and the first half of `devbox restart`). Wraps `docker down` with optional pre/post hook phases.

It is loaded separately by `LoadLifecycleConfig()` and is **not** merged with the 3-layer config.

The file is optional. When absent, `devbox run` / `devbox stop` / `devbox restart` are unavailable and only the lower-level commands (`devbox up`, `devbox down`) work.

`devbox up` and `devbox down` remain thin Docker Compose passthroughs and never use this pipeline; raw `docker compose stop` / `restart` remain accessible via `devbox docker stop` / `devbox docker restart`.

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

`docker up` and `docker wait` are issued as `type: devbox` steps with `cmd: "docker up"` / `cmd: "docker wait"` inside the `start` phase. They are not magical — the pipeline executor invokes them like any other step, so they pick up policy from `docker.yml`.

## Structure

```yaml
run:
  update:
    enabled: true
    mode: prompt        # prompt | auto | check | off
  show_info: true
  final_message: "Project is ready for work!"
  log: false            # tee status + child stdout/stderr to logs/run.log
  phases:
    - name: <phase>
      description: <text>
      when:             # optional: typed condition (see deploy.md)
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
  log: false            # tee status + child stdout/stderr to logs/stop.log
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

Phases and steps use the same shape as [deploy.yml](deploy.md): `name`, `description`, `when`, `untracked`, `steps[]`, plus per-step `type` / `cmd` / `with`, `when`, `check`, `continue_on_error`. See the deploy reference for the complete step grammar.

`deploy_services: true` is **not** allowed in lifecycle pipelines.

## `run.update`

The optional update probe runs before any phase. It can fetch from the upstream remote, detect drift, and (depending on `mode`) pull `--ff-only`. A successful pull triggers in-process reload of `DevboxConfig`, `LifecycleConfig`, and the command registry before phases execute.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `update.enabled` | bool | `true` when the `update:` block is present | Master switch. Writing the `update:` key is itself the opt-in. |
| `update.mode` | string | `prompt` | One of `prompt`, `auto`, `check`, `off`. |

Mode behaviour:

| Mode | Fetches | Pulls | Behaviour when behind |
|------|---------|-------|------------------------|
| `prompt` | yes | with consent | Asks before pulling; on non-TTY, behaves like `check`. |
| `auto` | yes | yes | Pulls without asking. |
| `check` | yes | no | Warns; never modifies the working tree. |
| `off` | no | no | Probe disabled (same as `--no-update` flag). |

Layered precedence at runtime: `--no-update` flag > `--update <mode>` flag > `EffectiveMode()` from YAML.

When the probe finds a dirty tree, no upstream, or a fetch failure it warns and continues — the run pipeline is never blocked by the probe.

## `run.show_info` / `run.final_message`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `show_info` | bool | `false` | Append a `devbox info` render after the last phase. |
| `final_message` | string | `Project is ready for work!` | Success message printed at the very end. |

## `stop.final_message`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `final_message` | string | `Project is stopped. Have a nice day!` | Success message printed at the very end. |

## `log` (file logging)

Top-level field on both `run:` and `stop:`. Defaults to `false` for lifecycle pipelines (in contrast to `deploy.yml`, where the default is `true`).

When enabled, devbox status messages and child-process stdout/stderr are teed to `logs/<name>.log` (with ANSI codes stripped) — `logs/run.log` for run, `logs/stop.log` for stop.

```yaml
run:
  log: true     # tee to logs/run.log
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
          type: devbox
          cmd: "docker wait"

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
    enabled: true
    mode: prompt
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
          type: devbox
          cmd: "docker wait"

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

A fuller example with hook phases lives in `devbox/lifecycle.example.yml`.

## Validation

`LoadLifecycleConfig()` enforces:

- Each step in `run.phases` and `stop.phases` has a `type:` field with one of `shell`, `devbox`, `command`, `builtin`.
- `update.mode`, when set, is one of `prompt`, `auto`, `check`, `off`.
- `deploy_services: true` is rejected (only valid in `deploy.yml`).
- `final_message` and `log` are normalized to defaults when absent.

## Common pitfalls

- **Forgetting `continue_on_error: true` on hook steps** — without it, a failing pre-stop hook aborts the entire stop sequence and containers are never stopped.
- **Using `update: {}` to disable the probe** — empty block opts in (Enabled defaults to `true`). Use `mode: off`, `enabled: false`, or omit the `update:` key entirely.
- **Adding `deploy_services` phases** — they are deploy-only. Lifecycle pipelines call services via `type: command` references instead.
- **Editing `lifecycle.yml` to use direct `docker compose` calls** — the public API is `type: devbox` with `cmd: "docker up"`. Direct `docker compose` calls bypass policy from `docker.yml`.
- **Using the old syntax** — `run:`, `devbox:`, `command:`, `builtin:` are no longer valid. Use `type:` / `cmd:` instead.

## Related commands

- `devbox run` — execute the run pipeline (with optional update probe)
- `devbox run --no-update` — skip the update probe
- `devbox run --update <mode>` — override the configured mode
- `devbox stop` — execute the stop pipeline
- `devbox restart` — `stop`, then `run --no-update`
- `devbox up` / `devbox down` — raw Docker Compose passthrough (does not use this pipeline)

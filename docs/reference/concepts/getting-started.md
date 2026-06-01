# Getting started

A first walk-through of DWE: enter a project, run the deploy pipeline, start the stack, and read the info dashboard.

## Contents

- [Enter a project](#enter-a-project)
- [First `dwe deploy`](#first-dwe-deploy)
- [First `dwe run`](#first-dwe-run)
- [First `dwe info`](#first-dwe-info)
- [Where to next](#where-to-next)

## Enter a project

A DWE project is any directory with a `workspace.yml` at its root. The CLI auto-discovers it by walking up from the current working directory.

Minimum project skeleton:

```text
my-project/
├── workspace.yml
└── workspace/
    ├── defaults.yml
    └── services/
        └── web/
            └── service.yml
```

`workspace.yml` declares the project identity:

```yaml
project:
  name: my-project
  prefix: myprefix
```

`workspace/defaults.yml` carries runtime defaults and the optional-service toggle map:

```yaml
runtime:
  use_https: false

services:
  web:
    enabled: true
```

`workspace/services/web/service.yml` declares one service:

```yaml
type: app
description: Web application

container: my-project-web

compose:
  files:
    - compose/services/web.yml

dirs:
  base: services/web
  src: services/web/src

ports:
  http:
    name: HTTP
    container: 80
    host: 8080

hosts:
  primary:
    name: Primary
    value: my-project.localhost
```

Change into the project and confirm the CLI recognises it:

```sh
cd my-project
dwe validate
```

`dwe validate` runs the project-readiness checks: env probes, declarative checks, and the per-domain validators (services, deploy, info, styles, …). It exits non-zero if any check fails. See [`validate.yml`](../config/validate.md) for the check catalogue.

## First `dwe deploy`

The deploy pipeline installs, configures, and migrates application services. It is declarative — `workspace/deploy.yml` lists phases and steps. If the file is absent, DWE uses a built-in default pipeline that inlines every enabled service's own `workspace/services/<name>/deploy.yml`, runs `docker up --wait`, and prints the info dashboard.

Preview the resolved plan before running:

```sh
dwe deploy plan
```

`deploy plan` is read-only. It loads the orchestrator and every enabled service pipeline, resolves templates and `extends:` chains, applies the topological order from `after:`, and prints the final phase / step tree without executing anything.

Execute the deploy:

```sh
dwe deploy run
```

The run reports phase and step status with `✓ ✗ ◎ ·` markers and tees output to `.dwe/logs/deploy.log`. State is journalled in `.dwe/deploy/state.yml` so that repeat runs skip steps whose `action_hash` and inputs are unchanged — see [State and locks](state-and-locks.md).

A minimal `workspace/deploy.yml` looks like:

```yaml
log: true

phases:
  - name: services
    deploy_services: true

  - name: start
    steps:
      - name: docker-up
        type: dwe
        cmd: docker up --wait
```

The `deploy_services: true` marker tells the orchestrator to inline every enabled service's `workspace/services/<name>/deploy.yml` at this point in topological order. The `start` phase then brings the stack up via Docker Compose. See [`deploy.yml`](../config/deploy/index.md) for every supported step type and builtin.

## First `dwe run`

`dwe run` drives the runtime lifecycle defined in `workspace/lifecycle.yml`:

```sh
dwe run
```

Execution order: optional Git update probe → before-run hooks → `docker compose up` → `docker compose wait` → after-run hooks → optional info display → final ready message.

Use `--no-update` to skip the Git update probe on a clean checkout, or `--update on` to force it:

```sh
dwe run --no-update
dwe run --update on
```

The precedence rule is `--no-update` > `--update` > `lifecycle.yml.update.mode` — see [`lifecycle.yml`](../config/lifecycle.md).

Stop the stack with `dwe stop` (runs `before-stop` hooks → `docker compose down` → `after-stop` hooks). Restart with `dwe restart` (stop + run with `--no-update`).

## First `dwe info`

The info dashboard reads `workspace/info.yml` and renders project-wide context: the project header, URLs and hosts of enabled services, command groups, and any custom sections.

```sh
dwe info
```

By default DWE runs `info` automatically at the end of `run` and `deploy run`. The standalone command is the same data, on demand.

`info.yml` supports `type: auto-urls` and `type: auto-hosts` items that expand at render time from each enabled service's `ports:` and `hosts:` maps — so the dashboard stays in sync with the service overlays without manual edits. See [`info.yml`](../config/info.md).

## Where to next

- [Architecture](architecture.md) — how `cli/`, `core/`, and `shared/` fit together; what is embedded vs read at runtime.
- [Project layout](project-layout.md) — what each folder under `workspace/` is for, and what gets generated under `.dwe/`.
- [Pipelines](pipelines.md) — the phase / step / condition execution model that deploy, reset, and lifecycle share.
- [Docker integration](docker.md) — compose file assembly, project-name derivation, lifecycle-bypass cases.
- [State and locks](state-and-locks.md) — what `state.yml` records, how `deploy.lock` and `snapshot.lock` serialise mutations.
- [Configuration reference](../config/index.md) — the field-level reference once you know the shape of the system.
- Run `dwe docs` for the same content in an interactive browser, or `dwe docs llms-txt` for a compact AI-agent index.

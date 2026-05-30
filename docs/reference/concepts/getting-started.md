# Getting started

A first walk-through of Devbox: build the binary, enter a project, run the deploy pipeline, start the stack, and read the info dashboard.

## Contents

- [Install](#install)
- [Enter a project](#enter-a-project)
- [First `devbox deploy`](#first-devbox-deploy)
- [First `devbox run`](#first-devbox-run)
- [First `devbox info`](#first-devbox-info)
- [Where to next](#where-to-next)

## Install

Devbox ships as a single Go binary. Build from this repository:

```sh
make build
```

`make build` runs `go mod tidy`, syncs `docs/` into the embedded tree, regenerates `internal/core/docs/content_hashes_gen.go`, builds `./cmd/devbox`, and writes `bin/devbox`. The binary is self-contained: docs, translations, and the default pipelines are embedded.

Run it from the repository root:

```sh
./bin/devbox version
```

For project-wide use, copy the binary onto your `$PATH`:

```sh
install -m 0755 bin/devbox /usr/local/bin/devbox
```

Devbox needs `docker` (and `docker compose`) on the path; `git` and `bash` are used for hook rendering and shell steps. See [`devbox.yml`](../config/devbox.md#binary-overrides) for binary overrides if those tools live in non-standard locations.

## Enter a project

A Devbox project is any directory with a `devbox.yml` at its root. The CLI auto-discovers it by walking up from the current working directory.

Minimum project skeleton:

```text
my-project/
├── devbox.yml
└── devbox/
    ├── defaults.yml
    └── services/
        └── web/
            └── service.yml
```

`devbox.yml` declares the project identity:

```yaml
schema_version: "2"

project:
  name: my-project
  prefix: devbox
```

`devbox/defaults.yml` carries runtime defaults and the optional-service toggle map:

```yaml
runtime:
  use_https: false

services:
  web:
    enabled: true
```

`devbox/services/web/service.yml` declares one service:

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
devbox validate
```

`devbox validate` runs the project-readiness checks: env probes, declarative checks, and the per-domain validators (services, deploy, info, styles, …). It exits non-zero if any check fails. See [`validate.yml`](../config/validate.md) for the check catalogue.

## First `devbox deploy`

The deploy pipeline installs, configures, and migrates application services. It is declarative — `devbox/deploy.yml` lists phases and steps. If the file is absent, Devbox uses a built-in default pipeline that inlines every enabled service's own `devbox/services/<name>/deploy.yml`, runs `docker up --wait`, and prints the info dashboard.

Preview the resolved plan before running:

```sh
devbox deploy plan
```

`deploy plan` is read-only. It loads the orchestrator and every enabled service pipeline, resolves templates and `extends:` chains, applies the topological order from `after:`, and prints the final phase / step tree without executing anything.

Execute the deploy:

```sh
devbox deploy run
```

The run reports phase and step status with `✓ ✗ ◎ ·` markers and tees output to `.devbox/logs/deploy.log`. State is journalled in `.devbox/deploy/state.yml` so that repeat runs skip steps whose `action_hash` and inputs are unchanged — see [State and locks](state-and-locks.md).

A minimal `devbox/deploy.yml` looks like:

```yaml
log: true

phases:
  - name: services
    deploy_services: true

  - name: start
    steps:
      - name: docker-up
        type: devbox
        cmd: docker up --wait
```

The `deploy_services: true` marker tells the orchestrator to inline every enabled service's `devbox/services/<name>/deploy.yml` at this point in topological order. The `start` phase then brings the stack up via Docker Compose. See [`deploy.yml`](../config/deploy/index.md) for every supported step type and builtin.

## First `devbox run`

`devbox run` drives the runtime lifecycle defined in `devbox/lifecycle.yml`:

```sh
devbox run
```

Execution order: optional Git update probe → before-run hooks → `docker compose up` → `docker compose wait` → after-run hooks → optional info display → final ready message.

Use `--no-update` to skip the Git update probe on a clean checkout, or `--update on` to force it:

```sh
devbox run --no-update
devbox run --update on
```

The precedence rule is `--no-update` > `--update` > `lifecycle.yml.update.mode` — see [`lifecycle.yml`](../config/lifecycle.md).

Stop the stack with `devbox stop` (runs `before-stop` hooks → `docker compose down` → `after-stop` hooks). Restart with `devbox restart` (stop + run with `--no-update`).

## First `devbox info`

The info dashboard reads `devbox/info.yml` and renders project-wide context: the project header, URLs and hosts of enabled services, command groups, and any custom sections.

```sh
devbox info
```

By default Devbox runs `info` automatically at the end of `run` and `deploy run`. The standalone command is the same data, on demand.

`info.yml` supports `type: auto-urls` and `type: auto-hosts` items that expand at render time from each enabled service's `ports:` and `hosts:` maps — so the dashboard stays in sync with the service overlays without manual edits. See [`info.yml`](../config/info.md).

## Where to next

- [Architecture](architecture.md) — how `cli/`, `core/`, and `shared/` fit together; what is embedded vs read at runtime.
- [Project layout](project-layout.md) — what each folder under `devbox/` is for, and what gets generated under `.devbox/`.
- [Pipelines](pipelines.md) — the phase / step / condition execution model that deploy, reset, and lifecycle share.
- [Docker integration](docker.md) — compose file assembly, project-name derivation, lifecycle-bypass cases.
- [State and locks](state-and-locks.md) — what `state.yml` records, how `deploy.lock` and `snapshot.lock` serialise mutations.
- [Configuration reference](../config/index.md) — the field-level reference once you know the shape of the system.
- Run `devbox docs` for the same content in an interactive browser, or `devbox docs llms-txt` for a compact AI-agent index.

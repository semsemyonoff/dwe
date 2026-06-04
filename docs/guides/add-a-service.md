# Adding a service

Your project already has a `web` app, and now you want to ship a `worker` next to it — a background process that pops jobs off a queue. This guide walks through the minimum scaffolding to add a new service, where each file lives, and how to verify the result before you commit.

The same recipe applies to adding a database, an admin UI, or any other container the team should share — the only thing that changes is the `type:`.

## Pick a service type

Every service declares one of three types. Choose the one that matches the role:

| `type:` | Use it for | Example |
|---------|-----------|---------|
| `app` | A service whose source code lives in the repo, gets built, and may render IDE/AI/git config. | `web`, `worker`, `api` |
| `infra` | A backing container without source. Other services may `depends_on` it. | `db`, `redis`, `minio` |
| `tool` | A standalone utility container — DB UI, mail catcher, observability frontend. Cannot be a `depends_on` target. | `adminer`, `mailpit` |

If you can't tell, default to `app` when the repo owns code for it and `infra` when it's an off-the-shelf image. A worker that runs your own code is an `app`.

Reference: [`../reference/config/services/index.md`](../reference/config/services/index.md) for the full per-type field allowlist.

## Lay out the folder

Each service lives in its own folder under `workspace/services/`. The folder name **is** the service key — there is no `name:` field.

```
workspace/services/
  web/                  # already exists
    service.yml
  worker/               # new
    service.yml
```

A minimal `service.yml` for a worker app:

```yaml
# workspace/services/worker/service.yml
type: app
container: app-worker
dir: ./services/worker
dir_internal: /workspace
icon: "⚙️"
```

That is enough to load: no ports, no hosts (a worker doesn't serve traffic), and `container:` becomes the Docker container name. If you omit `container:`, it defaults to the folder name.

For an `infra` service with a port:

```yaml
# workspace/services/queue/service.yml
type: infra
required: true        # always-on backing service
container: queue
ports:
  amqp: 5672
```

See [`../reference/config/services/fields.md`](../reference/config/services/fields.md) for every field and what it controls.

## Inherit shared fields with `extends:`

If your new app shares most of its setup with an existing app (same base image, same mounts, same shell), use `extends:` to inherit:

```yaml
# workspace/services/worker/service.yml
type: app
extends: web              # inherit dir_internal, dirs, cli, configs, render…
container: app-worker
required: false           # web is required: true; worker is optional
dir: ./services/worker    # child sets its own source dir
```

What propagates and what doesn't:

- **Inherited** when the child omits them: `dir_internal`, `work_dir_internal`, `dirs`, `configs`, `cli`, `render`, `compose`.
- **Never inherited**: `container`, `required`, `depends_on`. Each child must declare these explicitly.
- **List fields** (`dirs`, `cli.env`) — parent provides defaults, child adds entries.
- **`compose:`** — child's list wholly replaces parent's; not merged.

`extends:` is **app-only**. It is rejected at load on `tool` and `infra` entries.

Reference: [`../reference/config/services/extends.md`](../reference/config/services/extends.md).

## Wire the compose overlay

`service.yml` declares the DWE-shaped metadata. The actual container — image, command, mounts, networks — lives in a Docker Compose overlay file that you point to from `service.yml`:

```yaml
# workspace/services/worker/service.yml
type: app
container: app-worker
dir: ./services/worker
compose:
  - compose/services/worker/overlay.yml
```

```yaml
# compose/services/worker/overlay.yml
services:
  app-worker:
    image: ${DWE_PROJECT_PREFIX}/worker:latest
    build:
      context: ./services/worker
      dockerfile: Dockerfile
    command: ["node", "worker.js"]
    depends_on:
      - queue
    volumes:
      - ./services/worker:/workspace
```

The compose file is a normal Docker Compose YAML — DWE does not re-implement Compose, it composes overlays. Use `dwe compose files` to see the full overlay order DWE will merge.

## Register the toggle in `defaults.yml`

If the service is optional (not `required: true`), declare its default-enabled state in `workspace/defaults.yml`:

```yaml
# workspace/defaults.yml
services:
  worker:
    enabled: true     # on for everyone by default; individuals can override in local.yml
```

A developer who wants the worker off on their machine runs `dwe services disable worker`, which writes `services.worker.enabled: false` to `workspace/local.yml`.

Required services (`required: true` in `service.yml`) are always on and not toggleable; skip this step for them.

Reference: [`../reference/config/workspace.md`](../reference/config/workspace.md).

## Optional: deploy steps for the service

If your service needs setup at deploy time — install dependencies, run migrations, generate assets — add a `deploy.yml` next to `service.yml`:

```yaml
# workspace/services/worker/deploy.yml
steps:
  - id: install-deps
    type: shell
    cmd: |
      $DWE_BIN shell worker -c "npm install"
  - id: run-migrations
    type: shell
    when: "${services.queue.enabled}"
    cmd: |
      $DWE_BIN shell worker -c "npm run migrate"
```

These steps run on `dwe deploy` for every enabled service that has a `deploy.yml`, in the order the deploy pipeline computes. Steps may be skipped via the journal when the relevant config hash hasn't changed — that is automatic.

Per-service `deploy.yml` is valid for any service type (app / tool / infra), not just apps. Use it sparingly: simple containers that just `up` don't need one.

Reference: [`../reference/config/deploy/index.md`](../reference/config/deploy/index.md).

## Optional: render packs (IDE / AI / git)

For `type: app` services, DWE can render per-service IDE config, AGENTS.md guidance, and `.gitignore` snippets from shared template packs. Opt in from `service.yml`:

```yaml
# workspace/services/worker/service.yml
render:
  ide: { enabled: true, template: node }
  ai:  { enabled: true, template: node }
  git: { enabled: true, template: node }
```

The `template:` value names a pack under `workspace/templates/{ide,ai,git}/<pack>/`. Run `dwe render ide` (and `dwe render ai`, `dwe render git`) to preview what each renderer will produce.

If you don't need any of these, omit the `render:` block entirely.

See [`shared-ide-and-agent-config.md`](shared-ide-and-agent-config.md) for the full template-pack workflow and pack resolution chain.

## Verify

After writing the files, validate before deploying:

```shell
dwe validate config services
```

This runs the service-specific schema checks: required fields per type, allowed fields per type, valid port ranges, well-formed `extends:` chain, no `depends_on` cycle.

If validation passes, enable and deploy the service:

```shell
dwe services enable worker         # if it was disabled in local.yml
dwe deploy run --service worker    # run just this service's deploy steps
dwe run                            # start the stack
dwe status                         # confirm the new service is up
```

`dwe deploy run --service <name>` targets a single service. Useful when iterating — you don't need to wait for the full pipeline to re-run other services' steps.

If something fails, check `dwe logs worker` and see [`troubleshooting.md`](troubleshooting.md) for the common failure modes.

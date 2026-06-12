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

## Optional: render packs (config / IDE / AI / git)

For `type: app` services, DWE can render per-service files from shared template packs: runtime **config files** (`.env`, `env.php`, …), IDE config, AGENTS.md guidance, and `.gitignore` snippets. Opt in from `service.yml`:

```yaml
# workspace/services/worker/service.yml
render:
  config: { enabled: true, template: node }
  ide:    { enabled: true, template: node }
  ai:     { enabled: true, template: node }
  git:    { enabled: true, template: node }
```

The `template:` value names a pack under `workspace/templates/{config,ide,ai,git}/<pack>/`. Run `dwe render config` (and `dwe render ide`, `dwe render ai`, `dwe render git`) to preview what each renderer will produce.

The **config** pack is the odd one out and worth calling out:

- It writes straight into the service's mounted hub dir (its `dir`), mode replace — these are the files the container actually reads at runtime, not editor/agent metadata.
- Its templates use the `${...}` shorthand (e.g. `DB_HOST=${services.db.hosts.main}`), the same form as export rules — not the raw `{{ }}` substrate the ide/ai/git packs use.
- It supports **generated-once secrets** (Laravel `APP_KEY`, Magento `crypt.key`, …): the *service* mints the value, DWE harvests it into `.dwe/generated.yml` and replays it on every later render via `${generated.<name>}`. This is the render-based successor to the deprecated `service_configs_copy` mechanism.
- Config render also runs automatically as a `dwe run` preamble and as a deploy step; `dwe render config` is mainly for previewing and for the one-off `--harvest` pass.

If you don't need any of these, omit the `render:` block entirely.

See [`shared-ide-and-agent-config.md`](shared-ide-and-agent-config.md) for the IDE/AI/git template-pack workflow, and [`../reference/render/config.md`](../reference/render/config.md) for the config-pack substrate, the generated-value store, and the pipeline builtins.

## Verify

After writing the files, validate before deploying:

```shell
dwe validate config services
```

This runs the service-specific schema checks: required fields per type, allowed fields per type, valid port ranges, well-formed `extends:` chain, no `depends_on` cycle.

If validation passes, enable and start the service:

```shell
dwe services enable worker    # if it was disabled in local.yml
dwe run                       # start the stack
dwe status                    # confirm the new service is up
```

If you added a `deploy.yml` for `worker`, you can run only that service's deploy steps before starting the stack:

```shell
dwe deploy run --service worker    # only valid when worker has a deploy.yml
dwe run
dwe status
```

`dwe deploy run --service <name>` errors when the service has no `deploy.yml` — it is not a substitute for `dwe run` and cannot be used to start a service that has no deploy pipeline.

If something fails, check `dwe logs worker` and see [`troubleshooting.md`](troubleshooting.md) for the common failure modes.

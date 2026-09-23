# Add a service or tool

Load when the user wants to add an app / database / cache / search / proxy / tool, extend a base service into a variant, or add an optional toggle. You edit YAML; the user runs every mutating command.

Order: pick the type → folder (= key) → container → toggle → extras → validate → handoff.

## 1. Pick the type

- **`app`** — owns source (`dir`), render packs, `extends`, `cli`, `generated`. A per-service `deploy.yml` / `reset.yml` is valid for any type.
- **`tool`** — a side GUI / utility (dbgate, mailpit, redis-insight). No source.
- **`infra`** — a backing service others depend on (proxy, db, varnish); may own the public HTTP port (`port_via` for `dwe info`).

```shell
dwe docs show config/services/index    --lang en
dwe docs show config/services/fields   --lang en --anchors   # ports-field, hosts-field, cli-block, render-block, generated-block, bridge-block
dwe docs show config/services/examples --lang en
```

## 2. Folder = key

`workspace/services/<name>/service.yml` is required; the folder name **is** the key. Required fields: `type:` + `container:` (compose service name). Common: `icon:`, `ports:` (named map), `hosts:` (named map), `compose:` (overlays this service activates), `compose_after:` (overlays emitted after every service group — patches over other services' own overlays), `required: true` or a toggle (§ 4).

```yaml
# tool
type: tool
container: mailpit
icon: "📬"
compose: [compose/tools/mailpit.yml]
ports: { http: 8025 }
hosts: { web: mail.localhost }
```

```yaml
# infra, always-on, in the compose base
type: infra
container: nginx
icon: "🌐"
required: true
ports: { http: 80 }
```

```yaml
# app (extras in § 3)
type: app
container: app-main
icon: "🐘"
required: true
dir: ./services/main
dir_internal: /workspace
work_dir_internal: /workspace/src
hosts: { web: app.localhost }
```

Model host ports under `services.<name>.ports` (or route them through a `vars:` path an `exports.env` rule exports) — those are what `ports_free` preflight reads and what `dwe test` remaps per copy. A port reaching compose any other way collides across test copies (`integration-tests.md` § 5).

## 3. app-only extras

`dir` / `dir_internal` / `work_dir_internal` (mount the whole hub — `SKILL.md` § Rules); `dirs: [...]` (extra hub subdirs); `render.config.template:` (config pack) and `generated:` (harvested secrets) — both in `render-and-vars.md`; `cli: {mode, shell, user, workdir, env}` (how `service_exec` and `dwe shell` enter the container). Any type: `bridge: {enabled: true}` (opt the container into the host bridge so `dwe cmd` / `vars` work from inside it).

## 4. Container, toggle, variant

The container lives in a compose file, not in `service.yml`: the **base** (`compose.yaml`, or whatever the root config's `compose.base` names — usually set in `workspace/defaults.yml`) for `required` infra, or **overlays** the service's `compose:` list activates (convention: `compose/tools/<name>.yml`, `compose/services/<name>.yml`, `compose/services/<svc>/<variant>.yml`). Overlays consume `.env` vars and patch the proxy vhost. Assembly: `dwe docs show config/docker --lang en`, `concepts/docker`.

An overlay may also patch **neighbouring** services — a tool's overlay adding env and mounts to an app, so one toggle switches a whole feature — with no `depends_on` on the optional service in the base; merge rules and file-order traps: `dwe docs show guides/observability-otel#2-the-overlay --lang en`. An overlay that patches app services belongs in `compose_after:`, not `compose:`, so it lands after the apps' own overlays and wins whole-value merges (`command:`, `environment:` keys). A one-off helper the tool needs (a lookup script, a CLI) is a `profiles:`-gated compose service in the same overlay, targeted by `type: service_run` — no service folder; `service_run` passes `--entrypoint ""`, so `argv:` names the interpreter (the guide's PHP recipe; the script itself ships in dwe's `examples/otel/`).

- **Optional** service: omit `required:`, add `services.<name>.enabled: false|true` to `workspace/defaults.yml`. Required services are not listed there.
- **Variant** via `extends: <parent>` (a `main-debug` reusing the parent's image / source / render, adding an overlay and a `cli.env` tweak). Deepest-extends-wins on render collisions; a child sharing the parent's hub is a render alias. `dwe docs show config/services/extends --lang en`.
- **Toggle hooks**: `on_enable:` / `on_disable:` with `requires: none|restart|deploy|deploy-or-restart` (the deploy forms are `on_enable`-only) and `before:` / `after:` command IDs. `dwe docs show config/services/examples --lang en`.

## 5. Validate (read) and hand off

```shell
dwe validate config --output json   # service.yml schema, missing compose files, healthchecks without start_period
dwe validate --output json
```

- Added or changed a service (`service.yml`, source, render, deploy steps) → `dwe deploy run` (not `--service <name>` for a new service — `SKILL.md` § Picking the apply command).
- Only the compose base / overlays → `dwe run`.
- Pure toggle of an existing service → `dwe services enable|disable <name> --apply`.

Cross-links: rendered config / persisted secret → `render-and-vars.md`; per-service `deploy.yml` → `pipelines-and-orchestration.md`; fresh repo end to end → `populate-init-repo.md`.

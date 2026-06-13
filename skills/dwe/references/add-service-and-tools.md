# Add a service or tool

Load this file when the user wants to add an app / database / cache / search / proxy / tool to a DWE project, extend a base service into a variant, or add an optional service toggle. The agent edits YAML; the **user** runs every mutating command.

Order: pick the type → create the folder (= key) → give it a container → wire the toggle → optional extras → validate (read) → handoff.

## 1. Pick the type

Every service is `type:` one of:

- **`app`** — owns source (`dir`), render packs, its own `deploy.yml`, `extends`, `cli`, `generated`. The thing you're building.
- **`tool`** — a side GUI/utility (dbgate, mailpit, redis-insight). No source, no `depends_on` target role.
- **`infra`** — a backing service others depend on (nginx proxy, db, varnish). Can own the public HTTP port and be a `port_via` candidate for `dwe info`.

Read the type overview and full field reference before writing:

```
dwe docs show config/services/index --lang en
dwe docs show config/services/fields --lang en
dwe docs show config/services/examples --lang en
```

Scope the field reference with anchors instead of reading the whole body:

```
dwe docs show config/services/fields --anchors --lang en
```

Useful anchors: `ports-field`, `hosts-field`, `cli-block`, `render-block`, `renderconfig-block`, `generated-block`, `bridge-block`.

## 2. Create the folder = key

One folder per service: `workspace/services/<name>/service.yml`. The **folder name IS the map key** — there is no `name:` field. `service.yml` is required.

Required: `type:` + `container:` (the compose service name). Common optional fields: `icon:` (shown in `dwe info`), `ports:` (named map), `hosts:` (named map), `compose:` (overlay files this service activates), and either `required: true` (always-on) or a toggle in `defaults.yml` (step 5).

**tool** skeleton (base on the real laravel `mailpit`/`dbgate`):

```yaml
type: tool
container: mailpit
icon: "📬"
compose:
  - compose/tools/mailpit.yml
ports:
  http: 8025
hosts:
  web: mail.localhost
```

**infra** skeleton (base on the real laravel `nginx` — `required`, owns the HTTP port, no overlay because it's in the compose base):

```yaml
type: infra
container: nginx
icon: "🌐"
required: true
ports:
  http: 80
```

**app** skeleton (base on the real laravel `main` — trimmed; full fields in step 3):

```yaml
type: app
container: app-main
icon: "🐘"
required: true
dir: ./services/main
dir_internal: /workspace
work_dir_internal: /workspace/src
hosts:
  web: laravel.localhost
```

Verify each field's meaning at `dwe docs show config/services/fields --lang en` (don't guess `ports`/`hosts` shape — see the `ports-field` / `hosts-field` anchors).

## 3. app-only extras

These are valid **only** on `type: app`:

- `dir:` (host source dir, bind-mounted), `dir_internal:`, `work_dir_internal:` (container working dir).
- `dirs: [...]` — extra hub subdirs to create (e.g. `home`, `runtime`).
- `render.config.template:` — names the config pack that writes runtime files (the `.env`, `env.php`, …). See `render-and-vars.md`.
- `generated:` — secrets minted once and replayed across renders (e.g. an app key). See `render-and-vars.md`.
- `cli: { mode, shell, user, workdir }` — how `service_exec` commands enter the container.
- `bridge: { enabled: true }` — opt the container into the host bridge so `dwe cmd`/`vars`/diagnostics work from inside it.

Anchors for the schema: `dwe docs show config/services/fields#cli-block --lang en`, `#renderconfig-block`, `#generated-block`, `#bridge-block`.

## 4. Give it a container

A service's real container does NOT live in `service.yml`. It lives in a compose file:

- **compose base** — the always-present compose file (e.g. `compose.yaml`). Use for `required: true` infra like nginx/db.
- **compose overlays** — separate files activated per service. Naming convention (no `docker-compose.` prefix):
  - tools → `compose/tools/<name>.yml`
  - per-service variants → `compose/services/<svc>/<variant>.yml`
  - other per-service overlays → `compose/services/<name>.yml`

The `service.yml` `compose:` list names which overlay(s) the service activates (the laravel `mailpit` activates `compose/tools/mailpit.yml`). Overlays typically consume generated `.env` vars and patch the proxy (e.g. add an nginx vhost).

Schema + assembly order:

```
dwe docs show config/docker --lang en
dwe docs show concepts/docker --lang en
```

Editing the compose base/overlays applies via `dwe run` (not `deploy run`) — but adding a NEW service that also needs source/render/install applies via `dwe deploy run` (step 9).

## 5. Optional vs required

- **Optional service** — omit `required:` from `service.yml` and add a toggle to `workspace/defaults.yml` under `services.<name>.enabled` (the laravel `defaults.yml` toggles `dbgate`/`mailpit`/`main-debug` to `false`). Free-form values still go under `vars:` — see step 5 anchor in `render-and-vars.md`.
- **Required service** — set `required: true` on the `service.yml`. Required services are NOT listed in the `defaults.yml` `services` overlay.

Toggling an already-defined service later is a pure toggle → handoff `dwe services enable|disable <name> --apply` (step 9). Adding the definition itself → `dwe deploy run`.

Schema: `dwe docs show config/services/index --lang en`.

## 6. Variant via `extends:`

A debug/storybook/variant service `extends: <parent>` to reuse the parent's image/source/render and add only deltas (an extra compose overlay, a `cli.env` tweak). Base on the real laravel `main-debug`:

```yaml
type: app
container: app-main-debug
icon: "🐞"
extends: main
compose:
  - compose/services/main/debug.yml
cli:
  env:
    - XDEBUG_CONFIG="cli_color=1"
```

Render collisions resolve deepest-extends-wins; a child sharing the parent's hub dir is a render alias (don't give it its own render). Schema:

```
dwe docs show config/services/extends --lang en
```

## 7. Toggle automation (optional)

To run hooks when a service is enabled/disabled, add `on_enable:` / `on_disable:` to its `service.yml`. Base on the real magento `varnish`:

```yaml
on_enable:
  requires: restart        # or deploy-or-restart
  after:
    - services.magento.varnish.enable
on_disable:
  requires: restart
  before:
    - services.magento.varnish.disable
```

`requires:` picks how the toggle applies; `before:`/`after:` list command IDs to run around the toggle write. Schema: `dwe docs show config/services/fields --lang en` (toggle-hooks section — list with `--anchors`).

## 8. Validate (read)

Safe, lock-free, runs even when reporting errors:

```
dwe validate config services --output json
dwe validate --output json
```

Fix any reported issue (edit the YAML), then re-validate.

## 9. Handoff (the user runs it)

Pick the apply command by what you changed:

- **Added or changed a service** (new `service.yml`, source, render, deploy steps) → user runs `dwe deploy run` (it inlines every enabled service's `deploy.yml` and ends with `docker up --wait`). Do NOT use `deploy run --service <name>` for a brand-new service — it requires that service's own `deploy.yml` and skips the final stack up.
- **Only edited the compose base/overlays** → user runs `dwe run`.
- **Pure toggle** of an already-defined service → user runs `dwe services enable|disable <name> --apply`.

Edit the YAML yourself → show the diff → tell the user the exact command → wait for them to run it. Never run a mutating command, and never `docker compose` / `dwe docker up` directly.

Cross-links:
- Service needs a rendered config file or a persisted secret → `render-and-vars.md`.
- Service needs a per-service `deploy.yml` (clone source, install, provision) → `pipelines-and-orchestration.md`.
- Adding a service to a fresh repo from a git URL end to end → `populate-init-repo.md`.

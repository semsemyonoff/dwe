# Service examples and toggle lifecycle

Full service definition, `on_enable` / `on_disable` / `notes` semantics, and common pitfalls.

## Contents

- [Full service definition](#full-service-definition)
- [Toggle lifecycle](#toggle-lifecycle)
- [Common pitfalls](#common-pitfalls)

## Full service definition

```yaml
# devbox/services/main/service.yml
type: app
container: app-main
required: true
dir: ./services/main
dir_internal: /workspace
work_dir_internal: /workspace/src
icon: "📦"
info:
  title: "Main Application"
  paths:
    - name: "API Documentation"
      path: /api/docs
      icon: "📖"
ports:
  http: 80
hosts:
  web: app.localhost
configs:
  - .env
dirs:
  - logs
  - home
  - runtime
cli:
  mode: auto
  shell: bash
  user: www-data
  workdir: /workspace/src
render:
  ide:
    enabled: true
  ai:
    enabled: true
```

## Toggle lifecycle

The `on_enable`, `on_disable`, and `notes` blocks control what happens when a service is toggled via `devbox services enable/disable`.

### `on_enable` and `on_disable` schema

```yaml
on_enable:
  requires: none | restart | deploy   # what to trigger after writing local.yml
  before: [command-id]                # user commands run before the toggle is written
  after: [command-id]                 # user commands run after the toggle is written
on_disable:
  requires: none | restart            # deploy is not allowed on disable
  before: [command-id]
  after: [command-id]
```

| Field | Default | Description |
|-------|---------|-------------|
| `requires` | `restart` | What must happen for the change to take effect. `none` → write local.yml only; `restart` → trigger `devbox restart`; `deploy` → trigger `devbox deploy run --service <name>`. `deploy` is forbidden on `on_disable`. |
| `before` | — | User command IDs (from `devbox/commands/`) to run before the toggle write. Each must be `type: shell` or `type: script`. |
| `after` | — | User command IDs to run after the toggle write. Same type constraint applies. |

Hook commands run with `--yes` (non-interactive), stdout discarded, stderr captured for error messages.

### `notes` schema

```yaml
notes:
  enable: "Run migrations after enabling this service."
  disable: "Safe to disable while the stack is running."
```

Notes are shown in the plan output (`devbox services enable/disable --print-plan`) to guide the operator through manual follow-up steps.

### Toggle plan and `--apply`

`devbox services enable <name>` (without `--apply`) writes `local.yml` and records a pending op in the deploy state journal. The pending op is displayed by `devbox status` until cleared. `--apply` executes the plan immediately (runs hooks, triggers restart or deploy as declared by `requires`).

## Common pitfalls

- **Editing `dir` in `extends` child** — a child that sets `dir` completely replaces the parent's `dir` (not merged). This is intentional for services that live in a different host directory.
- **Absolute paths in `dirs`** — dirs entries must be relative paths. Absolute paths or paths containing `..` are rejected by `service_dirs_ensure` as a security check.
- **Missing `container` in child** — `container` is **not** inherited via `extends:`. A child without an explicit `container` defaults to its folder name (the same default that applies to any service). Declare `container` explicitly when the folder name is not the right container name.
- **Forgetting `depends_on:` on a child** — not inherited. A child that needs a dependency must declare it explicitly. (`compose:` is inherited from the parent when the child omits it — see [Inheritance resolution rules](extends.md#resolution-rules).)
- **`render:` block under a `tool` / `infra` service** — the `render:` block is app-only. Tool / infra entries that declare it fail to load. To attach a template pack to a non-app service it must first be retyped to `app` (with the prerequisite `dir:`).
- **Pre-existing non-symlink at a managed symlink path** — if `CLAUDE.md` (or another `symlinks[].link` path) already exists as a regular file, `devbox render ai` refuses to overwrite it and exits with an error: `refuse to overwrite non-symlink file at <path>; remove it or disable via render.ai.enabled: false`. Delete the file first, or set `render.ai.enabled: false` for that service.

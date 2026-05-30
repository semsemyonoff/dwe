# Service field reference

Every field allowed in `devbox/services/<name>/service.yml`, plus the nested blocks (`ports`, `hosts`, `icon`, `info`, `configs`, `dirs`, `cli`, `status`, `render`).

## Contents

- [Top-level service fields](#top-level-service-fields)
- [`ports` field](#ports-field)
- [`hosts` field](#hosts-field)
- [`icon` field](#icon-field)
- [`info` block](#info-block)
- [`configs` field](#configs-field)
- [`dirs` field](#dirs-field)
- [`cli` block](#cli-block)
- [`status` block](#status-block)
- [`render` block](#render-block)

**Host vs internal terminology:** Fields ending in `*_internal` or using the suffix convention (like `dir` for host, `dir_internal` for container) refer to paths: host side runs on your machine, internal side is the container mount point. Apply the same distinction to ports and hostnames: `ports.http` binds a container port to your host; `hosts.main` is the hostname the container resolves as.

## Top-level service fields

| Field | Type | Required | Allowed for | Description |
|-------|------|----------|-------------|-------------|
| `type` | string | yes | app / tool / infra | Discriminator — selects the field allowlist for this entry. |
| `container` | string | no (defaults to folder name) | all | Docker container name. Omit to use the service folder name as the container name. |
| `required` | bool | no | all | When true, the service is always enabled; the overlay cannot disable it. |
| `compose` | list | no | all | Additional compose overlay files activated when the service is enabled. |
| `ports` | `map[string]int` | no | all | Named container ports. See [`ports` field](#ports-field). |
| `hosts` | `map[string]string` | no | all | Named hostnames. See [`hosts` field](#hosts-field). |
| `icon` | string | no | all | Visual indicator emoji or symbol used in the `devbox info` dashboard. If omitted, a type default is used: `type: app` → 📦, `type: tool` → 🔧, `type: infra` → 🧱. See [`icon` field](#icon-field). |
| `info` | block | no | all | Display metadata for the info dashboard — title override, host/port key selection, and sub-paths. See [`info` block](#info-block). |
| `depends_on` | list | no | app / infra | Ordered dependency on other services (affects deploy order). A `type: tool` target is rejected at load. |
| `status` | list | no | all | Custom columns for the per-type `devbox status apps` / `tools` / `infra` table — see [`status` block](#status-block). |
| `on_enable` | block | no | app / tool / infra | Lifecycle hooks to run when the service is enabled. See [Examples — toggle lifecycle](examples.md#toggle-lifecycle). |
| `on_disable` | block | no | app / tool / infra | Lifecycle hooks to run when the service is disabled. |
| `notes` | block | no | app / tool / infra | Human-readable hints shown in the `services enable/disable` plan output. |
| `dir` | string | yes (non-extends) | **app** | Path to the service hub directory on the host. |
| `dir_internal` | string | no | **app** | Container mount point for the hub. |
| `work_dir_internal` | string | no | **app** | Default working directory for `exec`/`run` inside the container. |
| `extends` | string | no | **app** | Inherit fields from another `type: app` entry. Cross-type extends is rejected. See [Inheritance](extends.md). |
| `configs` | list | no | **app** | See [`configs` field](#configs-field). |
| `dirs` | list | no | **app** | Extra hub-relative directories — see [`dirs` field](#dirs-field). |
| `cli` | block | no | **app** | `devbox shell` defaults — see [`cli` block](#cli-block). |
| `render` | block | no | **app** | Nested template-render policy — see [`render` block](#render-block). |

## `ports` field

`ports:` is always a map from a port name to a container port. Single-port services need a chosen name (recommendation: `http` for web, `tcp` for raw TCP, role-specific like `mysql` / `amqp` for infra). Port values are defined in `devbox/services/<name>/service.yml`; `devbox/local.yml` overlays may remap individual entries — see the deep-merge behavior in [Load behavior](index.md#load-behavior).

```yaml
# devbox/services/rabbitmq/service.yml
type: infra
container: rabbitmq
ports:
  amqp: 5672
  admin: 15672
```

Values are bounded `1..65535` at load time. Scalar shapes (`ports: 80`, `ports: "80"`) are rejected with `ErrServicePortsShape`.

## `hosts` field

`hosts:` is always a map from a host name to a hostname. Symmetric with `ports`. A single hostname is conventionally `web`.

```yaml
# devbox/services/main/service.yml
type: app
hosts:
  web: app.localhost
```

Host values are defined in `devbox/services/<name>/service.yml`; `devbox/local.yml` overlays may remap individual entries — see the deep-merge behavior in [Load behavior](index.md#load-behavior).

## `icon` field

An optional emoji or Unicode symbol displayed next to the service name in the `devbox info` dashboard when rendering `auto-urls` blocks.

```yaml
# devbox/services/main/service.yml
type: app
icon: "📦"
```

If omitted, a type-based default is used:

| `type` | Default icon |
|--------|--------------|
| `app` | 📦 |
| `tool` | 🔧 |
| `infra` | 🧱 |

Icons are treated as opaque user content — ZWJ-joined emoji (family glyphs, profession modifiers, skin-tone variations) are supported but not validated for length. The icon appears only in the `devbox info` output; it is not used elsewhere.

> **⚠️ Avoid emoji with `Emoji_Presentation=No`.** Codepoints like `🛢` (U+1F6E2), `🗄` (U+1F5C4), and `⚙` (U+2699) are "text-default" — they only render as colour emoji when followed by VS-16 (U+FE0F), and many terminal + font combinations on macOS and Linux ignore that hint and draw them at 1 cell instead of 2. Lipgloss measures them at 2 cells, so the status / info tables under-fill and every column to the right of the icon shifts.
>
> `devbox validate` flags these icons (warning, scope `config.icons`) and suggests safe replacements from a curated map. **At render time the runtime drops ambiguous icons entirely** rather than letting them break column alignment — they will simply not appear in the dashboard, status table, or toggle menu. The same caveat applies to icons set under `info.paths[].icon` and to user-defined `auto-hosts` / `auto-urls` icons in `devbox/info.yml`.
>
> Prefer codepoints that are emoji by default — e.g. `📦`, `🧱`, `🐳`, `📚`, `💾`, `🔧`, `🧰` — or stick to single-width ASCII / box-drawing symbols.

## `info` block

Optional metadata for rendering this service in the `devbox info` dashboard.

```yaml
# devbox/services/main/service.yml
type: app
info:
  title: "Main Application"
  primary_host: web
  primary_port: http
  paths:
    - name: "API Documentation"
      path: /api/docs
      icon: "📖"
    - name: "Profiler"
      path: /?SPX_KEY=dev
      icon: "⚡"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `title` | string | title-case(folder-name) | Display name for this service in the dashboard (e.g., `"Main Application"`). Replaces the folder-name-derived default. |
| `primary_host` | string | `web` | Which key from `hosts` to surface in the main URL row (e.g., `console` for a multi-host service). |
| `primary_port` | string | `http` | Which key from `ports` to surface in the main URL row (e.g., `console` for a multi-port service). |
| `paths` | list | — | Ordered list of sub-paths under the main URL. See [`info.paths` entries](#infopaths-entries) below. |

### `info.paths` entries

Each entry in the `paths` list declares a named sub-path relative to the service's main URL.

```yaml
paths:
  - name: "API Documentation"
    path: /api/docs
    icon: "📖"
  - name: "Profiler"
    path: /?SPX_KEY=dev
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Display name for the path (e.g., `"API Documentation"`). Must be non-empty and unique within the service's `paths` list. |
| `path` | string | yes | URL path relative to the service's main host (must start with `/`). Example: `/api/docs`, `/admin`, `/?SPX_KEY=dev`. |
| `icon` | string | no | Optional emoji or symbol prepended to the path name. Defaults to `🔗` if omitted. |

Services without an `info` block are still included in `auto-urls` dashboard blocks (if their `include` types match) and render their main URL; they simply do not contribute custom title or sub-paths.

## `configs` field

Lists config files that are copied into the service hub during deploy.

```yaml
configs:
  - .env                        # shorthand: copies src to configs/.env, mounts at default
  - file: .env
    mountpoint: src/.env        # explicit destination inside container
```

| Field | Description |
|-------|-------------|
| `file` (or shorthand string) | Source file name (relative to `configs/services/<service>/`) |
| `mountpoint` | Path relative to the service `dir` (e.g. `src/.env`) where the file is touched after copying. Used by the `service_configs_copy` builtin to create a stub for Docker Desktop virtiofs nested file bind mounts. Optional. |

The source directory `configs/services/<service>/` is owned by the project and committed to git; the destination `services/<service>/configs/` is created during deploy and is gitignored.

## `dirs` field

Additional directories to create inside the service hub directory beyond the mandatory `src`.

```yaml
dirs:
  - logs
  - home
  - runtime
```

- Paths are relative to the service `dir` (e.g. `./services/main/logs`).
- The `src/` dir is always created and not listed here; it is also protected (skip semantics) in `recreate` mode so source code is never wiped.
- The `configs/` dir is **not** mandatory — it is created lazily by `service_configs_copy` when a `configs:` block is declared. If you need it created eagerly or wiped under `recreate`, list it explicitly here.
- When a service `extends` another, the child's `dirs` are appended to the parent's (deduplicated, parent first).
- Used by the `service_dirs_ensure` builtin during deploy.

## `cli` block

Controls how `devbox shell` and CLI execution behave for this service.

```yaml
cli:
  mode: auto        # auto | exec | run
  shell: bash
  user: www-data
  workdir: /workspace/src
  env:
    - XDEBUG_CONFIG="cli_color=1"
```

| Field | Default | Description |
|-------|---------|-------------|
| `mode` | `auto` | `auto` = exec when running, run when absent, error when stopped; `exec` = always `docker exec` (error if not running); `run` = always `docker compose run --rm` |
| `shell` | `bash` | Shell binary to invoke inside the container |
| `user` | current UID | User to run as inside the container |
| `workdir` | `work_dir_internal` (then `dir_internal`) | Working directory for the shell session |
| `env` | — | Extra env vars injected into the shell session |

CLI flags override `cli` config. Priority order (highest first): `--root`/`--user`/`--shell`/`--env` flags → `cli` config → built-in defaults.

### `cli.env` map vs list form

`cli.env` accepts either YAML map or list-of-`KEY=VALUE` form; both produce the same internal map and are interchangeable.

```yaml
# Map form
cli:
  env:
    XDEBUG_CONFIG: "cli_color=1"
    PHP_IDE_CONFIG: serverName=devbox

# List form
cli:
  env:
    - XDEBUG_CONFIG="cli_color=1"
    - PHP_IDE_CONFIG=serverName=devbox
```

The list form is convenient when copy-pasting from a `.env` file; the map form is friendlier for inheriting and overriding individual keys via `extends:`.

## `status` block

Optional list of custom columns appended to the type-specific status table — `devbox status apps` for `type: app`, `devbox status tools` for `type: tool`, `devbox status infra` for `type: infra` (and the default `devbox status` composite). Each entry declares a column name and a hermetic Go template that is evaluated per row against the merged config.

```yaml
# devbox/services/main/service.yml
type: app
container: app-main
dir: ./services/main
status:
  - name: CONTAINER
    value: "{{ .ServiceCfg.Container }}"
  - name: TAG
    value: "{{ .Globals.baseImageTag }}"
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Column header (uppercased in the rendered table). |
| `value` | string | yes | Go template evaluated via `tpl.Render`. Hermetic — no env / FS / network access. |

**Template data contract** (Go templates are case-sensitive):

| Path | Source | Casing |
|------|--------|--------|
| `.ServiceCfg.<Field>` | typed `ServiceConfig` for this row's service | PascalCase Go field names (`.ServiceCfg.Container`, `.ServiceCfg.Dir`) |
| `.Globals.<key>` | `cfg.Raw["globals"]` if present, else `nil` | lowercase YAML keys (`.Globals.baseImageTag`) |
| `.Raw.<key>...` | full `cfg.Raw` map | lowercase YAML keys (`.Raw.services.main.ports.http`, `.Raw.project.name`) |

The data root has only those three keys — there are no `.Project` / `.Runtime` aliases at the root. Drill into `.Raw.project.*`, `.Raw.runtime.*`, `.Raw.services.*` instead.

**Failure handling**: a template that errors out renders as `—` in the table and contributes to a single aggregated warning (`warning: N custom status expression(s) failed to render`) on stderr. The command still exits 0.

**Column ordering**: within a single type-keyed status section (apps / tools / infra), when multiple services declare overlapping columns the column order in the rendered table is "first appearance" during deterministic alphabetical iteration over services of that type. Services declaring fewer columns leave missing cells as `—`.

## `render` block

Nested block controlling whether and how rendering generates files for this service from template packs. Contains three sub-blocks: `ide`, `ai`, and `git`, each with the same structure.

### `render.ide` block

Controls whether and how IDE rendering generates config files for this service from template packs.

```yaml
render:
  ide:
    enabled: true          # opt in to IDE rendering for this service
    template: main-debug   # use custom template pack
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `true` for `type: app`; `false` otherwise | Include this service in IDE rendering. `devbox render ide` respects this setting; see **Activation** below. |
| `template` | — | Optional custom template pack directory name. Must be a single directory key under `devbox/templates/ide/` (no path separators, no `..`, no absolute paths, no leading `.`). If omitted, rendering falls back to service-name-specific then global packs. Explicit packs are strict: a typo will fail rather than silently using a fallback. |

#### IDE activation rules

IDE rendering requires **both** activation and policy conditions:

1. **Project activation**: The service must be enabled at the project level (via the 3-layer config merge; required services are always enabled).
2. **IDE policy**: The `render.ide.enabled` setting must be `true`.

A service is rendered only if both are satisfied. Disabling either suppresses rendering.

**Default policy**: `type: app` services default to `render.ide.enabled: true` (opt-out); `tool` / `infra` cannot carry a `render:` block at all (per-type field allowlist). To render IDE files for a non-app service, the service must be retyped — there is no per-pack escape hatch.

#### Template pack resolution

`devbox render ide` searches for template packs in this order; the first match is used:

1. `devbox/templates/ide/<template>/` (if `template` is set) — **strict**: pack must exist or rendering fails
2. `devbox/templates/ide/<service-name>/` (if `template` is not set)
3. `devbox/templates/ide/default/` (final fallback)

When an explicit `template:` is specified and the pack is not found, rendering fails with an error (catches typos). When no explicit template is set and the implicit chain exhausts without finding a pack, rendering is skipped with a warning.

Once a pack is selected, the command reads the pack's `manifest.yml` to determine what files to render and what symlinks to create. The manifest declares:

- `render`: source template files (must end in `.tmpl`) and their destination paths
- `symlinks`: relative symlinks to create inside the service directory (optional)

All destinations are relative to the service directory (e.g. `services/main/`). Nested paths are allowed (e.g. `.devcontainer/devcontainer.json`).

This manifest-based model lets you add support for any IDE or tool (`.cursor/`, `.zed/`, `.envrc`, etc.) without modifying the code — add the template files and declare them in `manifest.yml`.

#### Collision resolution

When multiple services share the same `dir` (e.g., `main` and `main-debug` both pointing to `./services/main`), only the most-derived service (deepest in the `extends` chain) renders IDE files. The others are reported as skipped with a collision warning.

The explicit positional form `devbox render ide <service>` treats the argument as a **hub anchor**: it is validated as a real service, but then resolved through the same collision policy. So `devbox render ide main` actually renders `main-debug` whenever `main-debug` is enabled — useful from per-service deploy pipelines, which pass the canonical service name and expect the variant-aware result.

```yaml
# devbox/services/main/service.yml
type: app
dir: ./services/main
# render.ide.enabled defaults to true
```

```yaml
# devbox/services/main-debug/service.yml
type: app
extends: main      # same dir as parent
dir: ./services/main
render:
  ide:
    template: main-debug  # use a different template pack
# IDE files go to ./services/main/ with content from main-debug pack
# (main-debug wins because it extends main)
```

In this example, `devbox render ide` produces files in `./services/main/` using the `main-debug` template pack, and emits a warning that `main` was skipped due to collision.

#### Worked example: template pack layout

This example shows how template packs are organized and the resulting files generated in the service directory.

**Project structure:**

```
devbox/services/
  main/
    service.yml
  main-debug/
    service.yml
devbox/templates/ide/
  default/
    manifest.yml
    .devcontainer/devcontainer.json.tmpl
    .vscode/settings.json.tmpl
  main-debug/
    manifest.yml
    .devcontainer/devcontainer.json.tmpl
    .vscode/settings.json.tmpl
    .vscode/launch.json.tmpl
```

**Service definitions:**

```yaml
# devbox/services/main/service.yml
type: app
dir: ./services/main
# render.ide.enabled defaults to true; renders using default pack
```

```yaml
# devbox/services/main-debug/service.yml
type: app
extends: main
container: app-main-debug
dir: ./services/main
render:
  ide:
    template: main-debug  # override to use main-debug pack
```

**After `devbox render ide`:**

```
services/main/
  .devcontainer/
    devcontainer.json    ← rendered from main-debug/.devcontainer/devcontainer.json.tmpl
  .vscode/
    settings.json        ← rendered from main-debug/.vscode/settings.json.tmpl
    launch.json          ← rendered from main-debug/.vscode/launch.json.tmpl
```

Note that `main` is skipped due to collision (same `dir` as `main-debug`), so only `main-debug`'s template pack is rendered.

### `render.ai` block

Controls whether and how agentic documentation rendering generates hub-level docs for this service from template packs.

```yaml
render:
  ai:
    enabled: true          # opt in to agent docs rendering for this service
    template: custom-docs  # use custom template pack
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `true` (for all service types) | Include this service in `devbox render ai` output. When `true`, agent-oriented documentation is generated in the service hub. |
| `template` | — | Optional custom template pack directory name. Must be a single directory key under `devbox/templates/ai/` (no path separators, no `..`, no absolute paths, no leading `.`). If omitted, rendering falls back to service-name-specific then default packs. Explicit packs are strict: a typo will fail rather than silently using a fallback. |

#### Agent docs activation rules

Agent docs rendering requires **both** activation and policy conditions:

1. **Project activation**: The service must be enabled at the project level (via the 3-layer config merge; required services are always enabled).
2. **Agent docs policy**: The `render.ai.enabled` setting must be `true`.

A service is rendered only if both are satisfied. Disabling either suppresses rendering.

**Default policy**: All services default to `render.ai.enabled: true` (opt-out). Set `render.ai.enabled: false` to suppress agent docs generation for a service.

#### Template pack resolution

`devbox render ai` searches for template packs in this order; the first match is used:

1. `devbox/templates/ai/<template>/` (if `template` is set) — **strict**: pack must exist or rendering fails
2. `devbox/templates/ai/<service-name>/` (if `template` is not set)
3. `devbox/templates/ai/default/` (final fallback)

When an explicit `template:` is specified and the pack is not found, rendering fails with an error (catches typos). When no explicit template is set and the implicit chain exhausts without finding a pack, rendering is skipped with a warning.

Once a pack is selected, the command reads the pack's `manifest.yml` to determine what files to render and what symlinks to create. The manifest declares:

- `render`: source template files (must end in `.tmpl`) and their destination paths
- `symlinks`: relative symlinks to create inside the service hub (must reference outputs from `render`)

All destinations are relative to the service hub directory (e.g. `services/main/`). Nested paths are allowed (e.g. `.claude/CLAUDE.md`).

#### Collision resolution

When multiple services share the same `dir` (e.g., `main` and `main-debug` both pointing to `./services/main`), only the canonical hub owner — the **least-derived** service (shallowest in the `extends` chain) — renders agent docs. The rationale: agent docs describe the hub's identity, and when a child `extends` a parent and shares its `dir`, the parent owns the hub; the child is a runtime variant of the same workspace. The losing variants are reported as skipped with a collision warning.

The explicit positional form `devbox render ai <service>` treats the argument as a **hub anchor** (same as `render ide`): the argument is validated as a real service, then resolved through the collision policy. So `devbox render ai main-debug` still renders `main` whenever both are enabled — the variant resolves to the canonical hub owner.

(Note: this differs from `devbox render ide`, where the *deepest* extends chain wins because IDE configs are about per-variant overrides.)

#### Worked example: template pack layout

This example shows how agent template packs are organized and the resulting files generated in the service directory.

**Project structure:**

```
devbox/services/
  main/
    service.yml
devbox/templates/ai/
  default/
    manifest.yml
    AGENTS.md.tmpl
    .claude/CLAUDE.md.tmpl
```

**Manifest (`devbox/templates/ai/default/manifest.yml`):**

```yaml
render:
  - from: AGENTS.md.tmpl
    to: AGENTS.md
  - from: .claude/CLAUDE.md.tmpl
    to: .claude/CLAUDE.md

symlinks:
  - link: CLAUDE.md
    to: AGENTS.md
```

**Template example (`AGENTS.md.tmpl`):**

```markdown
# {{.Service}} Service Hub

This is the {{.Service}} service running inside a devbox-managed hub.
The application source code is at `src/`.

Service container: {{.ServiceCfg.Container}}
Workspace root: {{.ServiceCfg.DirInternal}}
```

**Service definition (`devbox/services/main/service.yml`):**

```yaml
type: app
dir: ./services/main
# render.ai.enabled defaults to true; renders using default pack
```

**After `devbox render ai`:**

```
services/main/
  AGENTS.md          ← rendered from AGENTS.md.tmpl
  CLAUDE.md          ← symlink to AGENTS.md
  .claude/
    CLAUDE.md        ← rendered from .claude/CLAUDE.md.tmpl
```

### `render.git` block

Controls whether and how shell git hooks are rendered into the service's `src/.git/hooks/` directory from template packs.

```yaml
render:
  git:
    enabled: true          # opt in to git hooks rendering for this service
    template: custom-hooks # use custom template pack
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `true` for `type: app`; `false` otherwise | Include this service in `devbox render git` output. Mirrors the `render.ide` default policy. |
| `template` | — | Optional custom template pack directory name. Must be a single directory key under `devbox/templates/git/` (no path separators, no `..`, no absolute paths, no leading `.`). If omitted, rendering falls back to service-name-specific then `default` packs. Explicit packs are strict: a typo will fail rather than silently using a fallback. |

`extends` inheritance for `render.git.enabled` and `render.git.template` follows the same rules as `render.ide` and `render.ai`: child explicit values override the parent's; omitted values inherit. Collision resolution on shared `dir` uses **deepest-extends-wins** (same as `render.ide`).

Hooks are written to `<svc.Dir>/src/.git/hooks/<basename>` with mode `0755`. Services whose `src/.git` is missing (no git checkout) or is a file (worktree/submodule pointer) are skipped with a warning. See [render git](../../render/git.md) for the full reference, manifest schema, and examples.

# devbox/tools.yml

Tool definitions for the devbox project.

## Contents

- [Purpose](#purpose)
- [Load behavior](#load-behavior)
- [Structure](#structure)
- [Field reference](#field-reference)
- [`status` block](#status-block)
- [Overlay: enabling and disabling](#overlay-enabling-and-disabling)
- [Common pitfalls](#common-pitfalls)
- [Related commands](#related-commands)

## Purpose

`devbox/tools.yml` is the authoritative source for per-tool definitions: container name, virtual host, port, compose overlay, and optional custom status columns. It mirrors [`devbox/services.yml`](services.md) — definitions live here, while the per-user `enabled:` toggle lives in the 3-layer merge (`devbox/defaults.yml` for project defaults, `devbox/local.yml` for personal overrides).

## Load behavior

- Loaded once at startup by `LoadToolsConfig()`. Parsed with strict decoding (`yaml.Decoder.KnownFields(true)`): unknown fields under `tools.<name>` are hard errors. Missing `devbox/tools.yml` is fine — the tool set is just empty.
- After load, `enabled` for each declared tool is resolved from the 3-layer merge (`tools.<name>.enabled`, defaulting to `false`).
- The resolved definition (`container`, `host`, `port`, `compose`, `enabled`) is injected into `DevboxConfig.Raw["tools"]` so dot-paths like `tools.adminer.port` work in export rules, `docker.yml` templates, command `default_from:`, and `info.yml` references.
- The 3-layer overlay is **strict**: only `enabled:` is allowed under `tools.<name>` in `devbox.yml` / `devbox/defaults.yml` / `devbox/local.yml`. Any other field there is a layer-aware config-load error.
- Tool keys must be identifier-safe (`^[A-Za-z_][A-Za-z0-9_]*$`).

## Structure

```yaml
tools:
  <tool-key>:
    container: <container-name>
    host: <virtual-hostname>
    port: <container-port>
    compose: <relative-compose-overlay-path>
    status:
      - name: <COLUMN-HEADER>
        value: "<go-template>"
```

## Field reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `container` | string | yes | Docker image or container name. |
| `host` | string | yes | Virtual hostname for the tool (used by reverse proxy / hosts file). |
| `port` | int | yes | Container port (non-zero). |
| `compose` | string | no | Relative path to a docker-compose overlay file activated when the tool is enabled. |
| `status` | list | no | Custom columns rendered in `devbox status tools` — see below. |

`enabled` is **not** a field in `tools.yml`. It is resolved from the 3-layer merge only.

## `status` block

Optional list of custom columns appended to the `devbox status tools` table. Each entry declares a column name and a hermetic Go template evaluated per row against the merged config.

```yaml
tools:
  mailpit:
    container: mailpit
    host: mail.localhost
    port: 8025
    status:
      - name: ENDPOINT
        value: "http://{{ .Tool.Host }}:{{ .Tool.Port }}"
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Column header (uppercased in the rendered table). |
| `value` | string | yes | Go template evaluated via `tpl.Render`. Hermetic — no env / FS / network access. |

**Template data contract** (Go templates are case-sensitive):

| Path | Source | Casing |
|------|--------|--------|
| `.Tool.<Field>` | typed `ToolConfig` for this row's tool | PascalCase Go field names (`.Tool.Host`, `.Tool.Port`, `.Tool.Container`) |
| `.Globals.<key>` | `cfg.Raw["globals"]` if present, else `nil` | lowercase YAML keys (`.Globals.baseImageTag`) |
| `.Raw.<key>...` | full `cfg.Raw` map | lowercase YAML keys (`.Raw.runtime.use_https`, `.Raw.tools.mailpit.host`) |

The data root has only those three keys — there are no `.Project` / `.Runtime` / `.Tools` aliases at the root. Drill into `.Raw.project.*`, `.Raw.runtime.*`, `.Raw.tools.*` instead.

**Failure handling**: a template that errors out renders as `—` in the table and contributes to a single aggregated warning (`warning: N custom status expression(s) failed to render`) on stderr. The command still exits 0.

**Column ordering**: when multiple tools declare overlapping columns, the column order in the rendered table is "first appearance" during deterministic alphabetical iteration over tools. Tools declaring fewer columns leave missing cells as `—`.

## Overlay: enabling and disabling

The toggle lives in the 3-layer merge — typically `devbox/defaults.yml`, with optional per-user overrides in `devbox/local.yml`:

```yaml
# devbox/defaults.yml
tools:
  adminer:
    enabled: false
  mailpit:
    enabled: true
```

```yaml
# devbox/local.yml — personal override
tools:
  adminer:
    enabled: true
```

Any field other than `enabled:` under `tools.<name>` in any of the three overlay layers is a config-load error (e.g. `devbox/defaults.yml: tools.adminer.container: tool definitions belong in devbox/tools.yml`).

References to a tool name that is not declared in `tools.yml` are also rejected (typo guard).

## Common pitfalls

- **Editing `enabled` in `tools.yml`** — `enabled` lives in the overlay (`defaults.yml` / `local.yml`), not in `tools.yml`. Strict-decode rejects it.
- **Adding a tool definition in `defaults.yml`** — definitions belong in `tools.yml`. The overlay validator points at the offending file with the exact dot-path.
- **Identifier-unsafe keys** — tool names must match `^[A-Za-z_][A-Za-z0-9_]*$` so they work with Go template dot syntax (`{{ .Tool.<...> }}`) and dot-path resolution (`tools.<name>.port`).
- **Empty `container` / `host` / `port`** — these are required fields. Half-defined entries are rejected at load time so `tools enable <name>` cannot flip a broken entry into an enabled state.

## Related commands

- `devbox status tools` — read-only tools table (with any custom `status:` columns)
- `devbox tools` — interactive multi-select toggle (TTY only)
- `devbox tools enable/disable <name>` — toggle individual tools (writes to `devbox/local.yml`)
- `devbox render env` — regenerate `.env`; rules reference `tools.<name>.*` dot-paths
- `devbox info` — dashboard surfaces enabled tool URLs

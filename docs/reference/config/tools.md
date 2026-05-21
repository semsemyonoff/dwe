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

`devbox/tools.yml` is the authoritative source for per-tool **structural** definitions: container name, compose overlay, and optional custom status columns. Tool **host** and **port** live alongside service roles in the 3-layer `runtime.hosts.<tool>` / `runtime.ports.<tool>` — so a developer can override them in `devbox/local.yml` without touching `tools.yml`. The per-user `enabled:` toggle similarly lives in the 3-layer merge (`devbox/defaults.yml` for project defaults, `devbox/local.yml` for personal overrides).

## Load behavior

- Loaded once at startup by `LoadToolsConfig()`. Parsed with strict decoding (`yaml.Decoder.KnownFields(true)`): unknown fields under `tools.<name>` are hard errors. Missing `devbox/tools.yml` is fine — the tool set is just empty.
- After load, `enabled` for each declared tool is resolved from the 3-layer merge (`tools.<name>.enabled`, defaulting to `false`). `host` and `port` are resolved from `runtime.hosts.<name>` and `runtime.ports.<name>` in the merged map.
- The resolved `enabled`, `container`, and `compose` fields are injected into `DevboxConfig.Raw["tools"][<name>]` so dot-paths like `tools.adminer.container` resolve in export rules, `docker.yml` templates, command `default_from:`, and `info.yml` references. `host` and `port` are reachable via the canonical `runtime.hosts.<name>` / `runtime.ports.<name>` dot-paths only — they are **not** mirrored under `tools.<name>`.
- The 3-layer overlay is **strict**: only `enabled:` is allowed under `tools.<name>` in `devbox.yml` / `devbox/defaults.yml` / `devbox/local.yml`. Any other field there is a layer-aware config-load error. Tool host/port belong in `runtime.{hosts,ports}.<name>`, not `tools.<name>`.
- Tool keys must be identifier-safe (`^[A-Za-z_][A-Za-z0-9_]*$`).

## Structure

```yaml
# devbox/tools.yml — structural definitions only
tools:
  <tool-key>:
    container: <container-name>
    compose: <relative-compose-overlay-path>
    status:
      - name: <COLUMN-HEADER>
        value: "<go-template>"
```

```yaml
# devbox/defaults.yml — host/port and toggle, overrideable in devbox/local.yml
tools:
  <tool-key>:
    enabled: true
runtime:
  hosts:
    <tool-key>: <virtual-hostname>
  ports:
    <tool-key>: <container-port>
```

## Field reference

| Field | Where | Type | Required | Description |
|-------|-------|------|----------|-------------|
| `container` | `tools.yml` | string | yes | Docker image or container name. |
| `compose` | `tools.yml` | string | no | Relative path to a docker-compose overlay file activated when the tool is enabled. |
| `status` | `tools.yml` | list | no | Custom columns rendered in `devbox status tools` — see below. |
| `runtime.hosts.<tool>` | `defaults.yml` / `local.yml` | string | yes | Virtual hostname for the tool (used by reverse proxy / hosts file). |
| `runtime.ports.<tool>` | `defaults.yml` / `local.yml` | int | yes | Container port (non-zero). |
| `tools.<tool>.enabled` | `defaults.yml` / `local.yml` | bool | no | Per-user toggle. Defaults to `false`. |

The host and port keys live in the same shared namespace as service roles (`runtime.ports.app`, `runtime.hosts.main`, …) — there is no separate tools sub-tree.

## `status` block

Optional list of custom columns appended to the `devbox status tools` table. Each entry declares a column name and a hermetic Go template evaluated per row against the merged config.

```yaml
# devbox/tools.yml
tools:
  mailpit:
    container: mailpit
    status:
      - name: ENDPOINT
        value: "http://{{ .Tool.Host }}:{{ .Tool.Port }}"
```

`.Tool.Host` and `.Tool.Port` resolve from `runtime.hosts.mailpit` / `runtime.ports.mailpit` in the 3-layer merge.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Column header (uppercased in the rendered table). |
| `value` | string | yes | Go template evaluated via `tpl.Render`. Hermetic — no env / FS / network access. |

**Template data contract** (Go templates are case-sensitive):

| Path | Source | Casing |
|------|--------|--------|
| `.Tool.<Field>` | typed `ToolConfig` for this row's tool | PascalCase Go field names (`.Tool.Host`, `.Tool.Port`, `.Tool.Container`) |
| `.Globals.<key>` | `cfg.Raw["globals"]` if present, else `nil` | lowercase YAML keys (`.Globals.baseImageTag`) |
| `.Raw.<key>...` | full `cfg.Raw` map | lowercase YAML keys (`.Raw.runtime.use_https`, `.Raw.runtime.hosts.mailpit`) |

The data root has only those three keys — there are no `.Project` / `.Runtime` / `.Tools` aliases at the root. Drill into `.Raw.project.*`, `.Raw.runtime.*`, `.Raw.tools.*` instead.

**Failure handling**: a template that errors out renders as `—` in the table and contributes to a single aggregated warning (`warning: N custom status expression(s) failed to render`) on stderr. The command still exits 0.

**Column ordering**: when multiple tools declare overlapping columns, the column order in the rendered table is "first appearance" during deterministic alphabetical iteration over tools. Tools declaring fewer columns leave missing cells as `—`.

## Overlay: enabling and disabling

The toggle, host, and port all live in the 3-layer merge — typically project-wide in `devbox/defaults.yml`, with optional per-user overrides in `devbox/local.yml`:

```yaml
# devbox/defaults.yml — project defaults
tools:
  adminer:
    enabled: false
  mailpit:
    enabled: true
runtime:
  hosts:
    adminer: adminer.localhost
    mailpit: mail.localhost
  ports:
    adminer: 8080
    mailpit: 8025
```

```yaml
# devbox/local.yml — personal override
tools:
  adminer:
    enabled: true
runtime:
  ports:
    adminer: 18080    # personal port override, never committed
```

Any field other than `enabled:` under `tools.<name>` in any of the three overlay layers is a config-load error (e.g. `devbox/defaults.yml: tools.adminer.container: tool definitions belong in devbox/tools.yml`). Tool host/port go in `runtime.{hosts,ports}.<name>`, not `tools.<name>`.

References to a tool name that is not declared in `tools.yml` are also rejected (typo guard).

## Common pitfalls

- **Editing `enabled`, `host`, or `port` in `tools.yml`** — `enabled` lives under `tools.<name>` in the overlay; `host` / `port` live under `runtime.{hosts,ports}.<name>`. Strict-decode rejects all three inside `tools.yml`.
- **Adding a tool definition (`container:`, etc.) in `defaults.yml`** — structural fields belong in `tools.yml`. The overlay validator points at the offending file with the exact dot-path.
- **Identifier-unsafe keys** — tool names must match `^[A-Za-z_][A-Za-z0-9_]*$` so they work with Go template dot syntax (`{{ .Tool.<...> }}`) and dot-path resolution (`runtime.ports.<name>`).
- **Missing `runtime.hosts.<tool>` or `runtime.ports.<tool>`** — both are required for any declared tool, enabled or not. Half-defined entries are rejected at load time so `tools enable <name>` cannot flip a broken entry into an enabled state.
- **Looking up `tools.<name>.port` in templates or export rules** — that dot-path is no longer populated. Use `runtime.ports.<name>` (and `runtime.hosts.<name>`).

## Related commands

- `devbox status tools` — read-only tools table (with any custom `status:` columns)
- `devbox tools` — interactive multi-select toggle (TTY only)
- `devbox tools enable/disable <name>` — toggle individual tools (writes to `devbox/local.yml`)
- `devbox render env` — regenerate `.env`; rules reference `tools.<name>.*` dot-paths
- `devbox info` — dashboard surfaces enabled tool URLs

# info.yml

Info dashboard configuration.

## Contents

- [Purpose](#purpose)
- [Structure](#structure)
- [Top-level fields](#top-level-fields)
- [Section fields](#section-fields)
- [Item types](#item-types)
  - [`definition`](#definition)
  - [`info`](#info)
  - [`warning`](#warning)
  - [`auto-urls`](#auto-urls)
  - [`auto-hosts`](#auto-hosts)
  - [`subgroup`](#subgroup)
  - [`separator`](#separator)
- [Decorative items](#decorative-items)
- [Template expressions](#template-expressions)
  - [Available template data](#available-template-data)
  - [Template functions](#template-functions)
  - [`when` conditions](#when-conditions)
- [`footer`](#footer)
- [Fallback when info.yml is absent](#fallback-when-infoyml-is-absent)
- [Example: full info.yml](#example-full-infoyml)
- [Common pitfalls](#common-pitfalls)
- [Related commands](#related-commands)

## Purpose

`devbox/info.yml` declares the content of the `devbox info` dashboard: sections, items, conditional visibility, and template expressions. It is rendered by `ui.RenderInfo()` using Lipgloss.

Loaded separately by `LoadInfoConfig()`. Not merged with the 3-layer config.

## Structure

```yaml
sections:
  - id: <section-id>
    title: "Optional Section Title" # shown as a bordered box header
    items:
      - type: <item-type>
        <item-fields>

footer: true
```

## Top-level fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `sections` | list | — | Ordered list of section definitions. |
| `footer` | bool | `false` | When `true`, a closing table-header line is rendered after all sections. |

## Section fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `id` | string | — | Unique identifier for the section |
| `title` | string | — | Optional header rendered above the item list |
| `items` | list | — | Ordered list of item definitions |
| `hide_on_empty` | bool | `false` | Skip the section entirely (no title, no frame) when no item survives when-filtering. Note: subgroups have a different default (`true`). |

## Item types

| Type | Renders as | Required fields |
|------|-----------|-----------------|
| `definition` | `Label — Value` row, with optional icon | `name`, `value` |
| `info` | Info-coloured text line | `text` |
| `warning` | Warning-coloured text line | `text` |
| `auto-urls` | Dynamically generated URLs organized by service | — |
| `auto-hosts` | Dynamically generated hostnames from services | — |
| `subgroup` | Container with optional title and nested items | `items` |
| `separator` | Blank line spacer | — |

All items accept an optional `when:` (Go template expression). Items with a falsy `when` are dropped from the rendered output. All items support an optional `decorative` boolean flag (see [Decorative items](#decorative-items)).

> **Note on `auto-urls` and `auto-hosts` rendering:** `auto-urls` and `auto-hosts` items expand at render time (when `devbox info` is executed), not at YAML load time. The expansion happens inside the info renderer by consulting the current configuration and iterating services in deploy order. This design ensures the dashboard always reflects the latest service definitions and state. See [`docs/internals/packages.md`](../../internals/packages.md) for architectural details on the render-time expansion via the `Source*Spec` pattern.

### `definition`

A label + value pair, rendered as `Label — Value`.

```yaml
- type: definition
  name: Project
  value: "{{ .Project.FullName }}"
  icon: "🔗"
  indent: 2
  when: "{{ .State }}"
```

| Field | Description |
|-------|-------------|
| `name` | Label text |
| `value` | Value text (plain string or template expression) |
| `icon` | Optional emoji or symbol prepended to value. Prefer codepoints with `Emoji_Presentation=Yes` (e.g. `📦`, `🐳`, `💾`); text-default codepoints like `🛢`, `🗄`, `⚙` are flagged by `devbox validate` and dropped at render time to keep columns aligned — see [`icon` field](services.md#icon-field) in the services reference for the full caveat. |
| `indent` | Optional leading whitespace count. Default for definition items is `2`; pass `0` to flush left. Negative values are rejected. |
| `when` | Condition; item hidden if falsy |

### `info`

An informational text line.

```yaml
- type: info
  text: '127.0.0.1	{{ (index .Services "main").Host "web" }}'
  indent: 0
  when: '{{ (index .Services "adminer").Enabled }}'
```

| Field | Description |
|-------|-------------|
| `text` | Message text (plain string or template expression) |
| `indent` | Optional leading whitespace count |
| `when` | Condition; item hidden if falsy |

### `warning`

A warning text line (rendered in warning color).

```yaml
- type: warning
  text: "Please add this to your /etc/hosts file:"
```

| Field | Description |
|-------|-------------|
| `text` | Warning text (plain string or template expression) |
| `when` | Condition; item hidden if falsy |

### `auto-urls`

Dynamically generates a list of service URLs from the project's configured services. Services declare their hosts and ports in `devbox/services/<name>/service.yml`; `auto-urls` renders them with optional filtering and customization.

```yaml
- type: auto-urls
  include: [app, tool]
  hide: [varnish]
  hide_paths:
    main: ["SPX profiler"]
  port_via: nginx
  when: "{{ .Services }}"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `include` | list | `[app, tool]` | Service types to include: any combination of `app`, `tool`, `infra`. |
| `hide` | list | — | Service folder keys to exclude entirely. Unknown keys are silently ignored. |
| `hide_paths` | map | — | Exclude individual sub-paths by service key and path name (e.g. `main: ["SPX profiler"]` hides the path named "SPX profiler" under the service "main"). |
| `port_via` | string | auto-detected | Override which service to use as the front proxy for generating main URLs. When empty, auto-detection searches for a single enabled `type: infra` service declaring either `ports.http: 80` (http traffic) or `ports.https: 443` (https traffic). Explicitly named services are required to exist; missing services produce an error. Auto-detection returns no proxy when zero candidates or multiple candidates are found (in that case, only direct `localhost:<port>` URLs render). |
| `when` | string | — | Condition; item hidden if falsy. |

**`port_via` auto-detection examples:**

With auto-detection (default — no `port_via:` field):
```yaml
# Auto-detects if exactly one infra service has ports.http: 80
- type: auto-urls
  include: [app, tool]
```

In this case, if a service named `nginx` has `ports: {http: 80}` and it is of type `infra` and enabled, it is auto-selected. App and tool services will then render as `proxied URL | localhost:port` (if they declare their own ports) or just `proxied URL` (if host-only). Other services without auto-detected proxy render as `localhost:port` only.

With explicit `port_via:` override:
```yaml
# Always use named service as proxy, even if it's not type: infra
- type: auto-urls
  include: [app, tool]
  port_via: api_gateway
```

When `port_via` is explicitly set, that service must exist or an error is produced at render time. A named service is used for proxy URL construction regardless of its `type:`.

When auto-detection finds zero or multiple infra services with the target port, **no proxy is selected** and services render using only their direct ports (or with just hosts if no port exists):
```yaml
# No eligible infra service found → app/tool with only localhost:<port> URLs
- type: auto-urls
  include: [app, tool]
```

Services contribute to `auto-urls` via their `info:` block in `service.yml` (see [services.md](services.md) for the schema). Each service may declare:
- `title` — override the service header (defaults to title-cased folder name)
- `primary_host` — which `hosts` entry to surface as the main URL (default: `web`)
- `primary_port` — which `ports` entry to surface (default: `http`)
- `paths` — ordered list of sub-paths under the main URL

Services without an `info` block are included in the `include` types but render only their main URL if hosts and ports exist.

**URL assembly rules:**
- `hosts[primary_host]` **and** `ports[primary_port]` (direct binding) → `<proxied URL> | <direct URL>`
- only `hosts[primary_host]` → `<proxied URL>` (if `port_via` available)
- only `ports[primary_port]` → `http://localhost:<port>`
- neither → row silently omitted

`<proxied URL>` uses the `port_via` service's ports for scheme/port selection; `<direct URL>` uses the service's own port. Ports `:80` and `:443` are omitted from output.

### `auto-hosts`

Dynamically generates a list of all hostnames from services for `/etc/hosts` configuration.

```yaml
- type: auto-hosts
  include: [app, tool, infra]
  ip: 127.0.0.1
  hide: [varnish]
  when: "{{ .Services }}"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `include` | list | `[app, tool, infra]` | Service types to include: any combination of `app`, `tool`, `infra`. |
| `ip` | string | `127.0.0.1` | IP address to associate with all hostnames. Values are not validated for IP format here; `devbox validate` emits a warning if parsing fails. |
| `hide` | list | — | Service folder keys to exclude entirely. Unknown keys are silently ignored. |
| `when` | string | — | Condition; item hidden if falsy. |

Renders every `hosts` entry from included services in a two-column table (`IP  Hostname`), preserving deploy order, deduplicating hostnames, and filtering out `localhost`.

### `subgroup`

A container item that groups related items and optionally displays a title.

```yaml
- type: subgroup
  title: "Tools"
  hide_on_empty: false
  items:
    - type: definition
      name: Adminer
      icon: "🛢"
      value: '{{ appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
      when: '{{ (index .Services "adminer").Enabled }}'
    - type: definition
      name: RedisInsight
      icon: "📊"
      value: '{{ appURL ((index .Services "redis_insight").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
      when: '{{ (index .Services "redis_insight").Enabled }}'
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `title` | string | — | Optional header for the subgroup (plain string or template expression). When empty, the subgroup is rendered without a heading. |
| `items` | list | — | Required. Ordered list of child item definitions. Can contain any item type, including nested subgroups. |
| `when` | string | — | Condition; when falsy the entire subgroup (including all children) is skipped. When truthy, each child item is evaluated for its own `when` condition. |
| `hide_on_empty` | bool | `true` | Skip the subgroup entirely when no child item survives when-filtering. (Opposite of section default; subgroups default to `true`.) |
| `decorative` | bool | `false` | When `true`, the subgroup never counts as content for the parent's `hide_on_empty` check, even if it produces output. |

Subgroups can be nested arbitrarily.

### `separator`

A blank line used to space content within a section.

```yaml
- type: separator
```

No fields. Useful when adjacent items need visual breathing room without introducing a new section.

## Decorative items

By default, items fall into two categories: **content** items that count toward section visibility, and **decorative** items that do not.

| Type | Default `decorative` |
|------|----------------------|
| `definition` | `false` |
| `info` | `false` |
| `warning` | `false` |
| `subgroup` | `false` |
| `separator` | `true` |

The `decorative` flag on any item type overrides its default:

```yaml
- type: warning
  text: "Only informational"
  decorative: true    # Makes this warning not count as content
```

```yaml
- type: separator
  decorative: false   # Makes this separator count as content, keeping the section visible
```

When `hide_on_empty: true` on a section or subgroup, the block is skipped entirely if no item survives both `when` filtering and the content-vs-decorative check. A block with only decorative items (or no items) may still render if it has a title and `hide_on_empty: false`.

## Template expressions

All `text`, `value`, and `when` fields support Go template syntax evaluated against `DevboxConfig`.

### Available template data

| Expression | Type | Description |
|------------|------|-------------|
| `{{ .Project.Name }}` | string | Project name |
| `{{ .Project.FullName }}` | string | Combined prefix + name |
| `{{ .State }}` | string | Active state (empty if none) |
| `{{ .Runtime.UseHTTPS }}` | bool | HTTPS enabled. |
| `{{ .Runtime.SPX.Path }}` | string | SPX profiler path. |
| `{{ (index .Services "main").Enabled }}` | bool | Whether the service `main` is enabled (required services are always true). |
| `{{ (index .Services "main").Container }}` | string | Container name on the `main` service. |
| `{{ (index .Services "main").Port "http" }}` | int | Named port lookup. `Port(name)` is a method on `ServiceConfig` (returns `0` if absent). |
| `{{ (index .Services "main").Host "web" }}` | string | Named host lookup. `Host(name)` returns `""` if absent. |
| `{{ .AppServices }}` / `{{ .ToolServices }}` / `{{ .InfraServices }}` | `map[string]ServiceConfig` | Filtered subsets by `type:` — handy for `{{ range }}` over a single category. |

### Template functions

Info templates have access to the standard devbox template surface: the `appURL` domain helper plus the sprout registries (`std`, `strings`, `numeric`, `slices`, `maps`, `regexp`, `conversion`, `time`, `filesystem`, `semver`). See [Templates](../templates.md) for the full helper reference.

Example using `appURL`:

```yaml
value: '{{ appURL ((index .Services "main").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
# → http://laravel.localhost (or https://… when use_https is true)
```

### `when` conditions

`when` fields accept any template expression that evaluates to a truthy/falsy value. Empty string, `false`, and `0` are falsy; anything else is truthy.

```yaml
when: "{{ .State }}"                                   # show only when state is non-empty
when: '{{ (index .Services "adminer").Enabled }}'      # show only when adminer is enabled
when: "{{ .Runtime.SPX.Path }}"                        # show only when SPX path is set
```

## `footer`

```yaml
footer: true
```

When true, a footer line is rendered below all sections (typically shows help hint).

## Fallback when info.yml is absent

When `devbox/info.yml` does not exist, a built-in default configuration is used. It renders two sections:

1. **URLs** section with an `auto-urls` item (default `include: [app, tool]`; no filtering)
2. **Hosts** section with a warning and an `auto-hosts` item (default `include: [app, tool, infra]`)

This allows projects without an `info.yml` to immediately see a sensible dashboard showing all services' connectivity, constructed entirely from the service definitions in `devbox/services/*/service.yml`. Services contribute details via their `info:` blocks (title, paths, host/port keys). No `info.yml` editing is required to get started.

To customize the dashboard, create a `devbox/info.yml` with your own `sections` and `items`. The built-in default is not used if the file exists, even if it contains no `auto-urls` or `auto-hosts` items.

## Example: full info.yml

```yaml
sections:
  - id: devbox_info
    items:
      - type: subgroup
        title: Devbox
        hide_on_empty: false
        items:
          - type: definition
            name: Project
            value: "{{ .Project.FullName }}"
          - type: definition
            name: State
            value: "{{ .State }}"
            when: "{{ .State }}"

  - id: urls
    title: URLs
    items:
      # Automatically render all app and tool services with their hosts/ports
      - type: auto-urls
        include: [app, tool]
        hide: [varnish]
        hide_paths:
          main: ["SPX profiler"]
        port_via: nginx

  - id: credentials
    title: Credentials
    items:
      - type: subgroup
        title: Database
        hide_on_empty: true
        items:
          - type: definition
            name: User
            value: "{{ .Project.Name }}_user"
      - type: subgroup
        title: API Key
        hide_on_empty: true
        items:
          - type: warning
            text: "Check .env for sensitive credentials"

  - id: hosts
    title: Hosts
    items:
      - type: warning
        text: "Add these to your /etc/hosts:"
      # Automatically render all service hostnames
      - type: auto-hosts
        include: [app, tool, infra]

footer: true
```

## Common pitfalls

- **Bare `when:` values without template syntax** — `when: .State` is not valid; must be `when: "{{ .State }}"`.
- **Missing quotes around template expressions** — YAML parses `{{ ... }}` as a flow mapping if unquoted. Always quote template strings.
- **Service lookup syntax** — Go's text/template requires `index` for map access by string key: `(index .Services "main")` returns a `ServiceConfig`. From there, struct fields are PascalCase (`.Container`, `.Enabled`) and ports / hosts use the `Port` / `Host` accessor methods with the port/host name as argument: `(index .Services "main").Port "http"`. Parentheses around the index expression are required so the method dispatches on the returned `ServiceConfig`.
- **Using config keys not in DevboxConfig struct** — only fields exposed on the typed `DevboxConfig` struct are available in templates. Custom keys added to `defaults.yml` are in `Raw` but not in template data unless explicitly exposed.
- **`appURL` argument order** — the order is `host`, `port`, `useHTTPS`, then optional `path`. Swapping port and useHTTPS produces incorrect URLs silently. When linking a tool routed via the main reverse proxy, combine the tool's hostname with the main service's port: `appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS`.
- **`hide_on_empty` with decorative items** — By default, content items like `definition`, `info`, and `warning` count toward section visibility, but `separator` does not. Use the `decorative` flag to override: set `decorative: true` on a content item to exclude it from the visibility calculation, or set `decorative: false` on a separator to make it count as content. A section with `hide_on_empty: true` is fully hidden if no content item (after `when` filtering) survives.
- **Footer rendering with `hide_on_empty`** — When `footer: true`, the footer is only rendered if at least one section produced output. If all sections are hidden via `hide_on_empty`, the footer is also suppressed.

## Related commands

- `devbox info` — render the full dashboard
- `devbox` (no args) — shows compact summary (not from `info.yml`, uses `ui.RenderSummary`)

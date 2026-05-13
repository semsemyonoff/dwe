# info.yml

Info dashboard configuration.

## Contents

- [Purpose](#purpose)
- [Structure](#structure)
- [Top-level fields](#top-level-fields)
- [Section fields](#section-fields)
- [Item types](#item-types)
  - [`subheader`](#subheader)
  - [`definition`](#definition)
  - [`info`](#info)
  - [`warning`](#warning)
  - [`separator`](#separator)
- [Template expressions](#template-expressions)
  - [Available template data](#available-template-data)
  - [Template functions](#template-functions)
  - [`when` conditions](#when-conditions)
- [`footer`](#footer)
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

> The struct exposes a `settings.line_width` knob, but the current Lipgloss-based renderer ignores it — terminal width is detected automatically. The field is kept reserved; do not rely on it.

## Section fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier for the section |
| `title` | string | Optional header rendered above the item list |
| `items` | list | Ordered list of item definitions |

## Item types

| Type | Renders as | Required fields |
|------|-----------|-----------------|
| `subheader` | Coloured sub-section label inside a section | `text` |
| `definition` | `Label — Value` row, with optional icon | `name`, `value` |
| `info` | Info-coloured text line | `text` |
| `warning` | Warning-coloured text line | `text` |
| `separator` | Blank line spacer | — |

All items accept an optional `when:` (Go template expression). Items with a falsy `when` are dropped from the rendered output.

### `subheader`

A styled sub-section label within a section.

```yaml
- type: subheader
  text: "Main"
  when: "{{ .SomeCondition }}"
```

| Field | Description |
|-------|-------------|
| `text` | Label text (plain string or template expression) |
| `when` | Condition; item hidden if falsy |

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
| `icon` | Optional emoji or symbol prepended to value |
| `indent` | Optional leading whitespace count. Default for definition items is `2`; pass `0` to flush left. Negative values are rejected. |
| `when` | Condition; item hidden if falsy |

### `info`

An informational text line.

```yaml
- type: info
  text: "127.0.0.1\t{{ .Runtime.Hosts.Main }}"
  indent: 0
  when: "{{ .Tools.Adminer.Enabled }}"
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

### `separator`

A blank line used to space content within a section.

```yaml
- type: separator
```

No fields. Useful when two adjacent subheaders or definitions need visual breathing room without introducing a new section.

## Template expressions

All `text`, `value`, and `when` fields support Go template syntax evaluated against `DevboxConfig`.

### Available template data

| Expression | Type | Description |
|------------|------|-------------|
| `{{ .Project.Name }}` | string | Project name |
| `{{ .Project.FullName }}` | string | Combined prefix + name |
| `{{ .State }}` | string | Active state (empty if none) |
| `{{ .Runtime.UseHTTPS }}` | bool | HTTPS enabled |
| `{{ .Runtime.Ports.App }}` | int | App port |
| `{{ .Runtime.Ports.DB }}` | int | DB port |
| `{{ .Runtime.Hosts.Main }}` | string | Main app hostname |
| `{{ .Runtime.Hosts.Adminer }}` | string | Adminer hostname |
| `{{ .Runtime.Hosts.RedisInsight }}` | string | Redis Insight hostname |
| `{{ .Runtime.Hosts.Mailpit }}` | string | Mailpit hostname |
| `{{ .Runtime.SPX.Path }}` | string | SPX profiler path |
| `{{ .Tools.Adminer.Enabled }}` | bool | Adminer tool enabled |
| `{{ .Tools.RedisInsight.Enabled }}` | bool | Redis Insight enabled |
| `{{ .Tools.Mailpit.Enabled }}` | bool | Mailpit enabled |
| `{{ .Tools.AnyEnabled }}` | bool | Any optional tool enabled |

### Template functions

Info section templates have access to a base set of helper functions. These are the same registries used by the `message` builtin and workflow `${...}` expressions.

#### Domain helpers (info-scope)

| Function | Signature | Description |
|----------|-----------|-------------|
| `appURL` | `appURL host port useHTTPS [path]` | Build a URL from host, port, HTTPS flag, and optional path. The port is omitted when it matches the scheme default (80 for http, 443 for https). |

Example:
```yaml
value: "{{ appURL .Runtime.Hosts.Main .Runtime.Ports.App .Runtime.UseHTTPS }}"
```

Renders as `http://laravel.localhost` or `https://laravel.localhost` depending on `use_https`.

#### Sprout registries

In addition to the domain helper, the following registries from [go-sprout](https://docs.atom.codes/sprout/registries/) are available:

| Registry | Use | Description |
|----------|-----|-------------|
| `std` | `default`, `ternary`, `empty`, `coalesce` | Utilities: defaults, conditionals, emptiness checks |
| `strings` | `hasSuffix`, `hasPrefix`, `lower`, `upper`, `trim`, etc. | String manipulation |
| `numeric` | `add`, `max`, `min`, `mul`, `div`, etc. | Numeric operations |
| `slices` | `first`, `last`, `slice`, `join`, `reverse`, etc. | List/array operations |
| `maps` | `keys`, `values`, `has`, `pick`, etc. | Map/object operations |
| `regexp` | `regexMatch`, `regexReplace`, `regexSplit` | Regular expression matching |
| `conversion` | `toInt`, `toFloat`, `toString`, `toBool`, etc. | Type conversion |
| `time` | `now`, `date`, `dateFormat`, etc. | Date/time operations |
| `filesystem` | `pathBase`, `pathDir`, `pathExt`, `pathClean`, `osBase`, `osDir` | Path manipulation (forward-slash and OS-specific) |
| `semver` | `semverCompare`, `semverSort` | Semantic version operations |

**Hermetic design**: No env access, no filesystem reads, no network, no crypto/random — templates are isolated by construction.

Common patterns for info templates:

| Task | Example |
|------|---------|
| Current date | `{{ now \| date "2006-01-02" }}` |
| Current datetime | `{{ now \| date "2006-01-02_15-04-05" }}` |
| Path basename | `{{ some_path \| pathBase }}` |
| Path directory | `{{ some_path \| pathDir }}` |
| Default value | `{{ .Value \| default "N/A" }}` |
| Conditional | `{{ if eq .State "ready" }}Ready{{ else }}Not ready{{ end }}` |

### `when` conditions

`when` fields accept any template expression that evaluates to a truthy/falsy value. Empty string, `false`, and `0` are falsy; anything else is truthy.

```yaml
when: "{{ .State }}"                    # show only when state is non-empty
when: "{{ .Tools.Adminer.Enabled }}"   # show only when adminer is enabled
when: "{{ .Runtime.SPX.Path }}"        # show only when SPX path is set
```

## `footer`

```yaml
footer: true
```

When true, a footer line is rendered below all sections (typically shows help hint).

## Example: full info.yml

```yaml
sections:
  - id: devbox_info
    items:
      - type: subheader
        text: Devbox
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
      - type: subheader
        text: Main
      - type: definition
        name: URL
        icon: "🔗"
        value: "{{ appURL .Runtime.Hosts.Main .Runtime.Ports.App .Runtime.UseHTTPS }}"
      - type: subheader
        text: Tools
        when: "{{ .Tools.AnyEnabled }}"
      - type: definition
        name: Adminer
        icon: "🛢"
        value: '{{ appURL .Runtime.Hosts.Adminer .Runtime.Ports.App .Runtime.UseHTTPS }}'
        when: "{{ .Tools.Adminer.Enabled }}"

  - id: hosts
    title: Hosts
    items:
      - type: warning
        text: "Add this to your /etc/hosts:"
      - type: info
        text: "127.0.0.1\t{{ .Runtime.Hosts.Main }}"

footer: true
```

## Common pitfalls

- **Bare `when:` values without template syntax** — `when: .State` is not valid; must be `when: "{{ .State }}"`.
- **Missing quotes around template expressions** — YAML parses `{{ ... }}` as a flow mapping if unquoted. Always quote template strings.
- **Using config keys not in DevboxConfig struct** — only fields exposed on the typed `DevboxConfig` struct are available in templates. Custom keys added to `defaults.yml` are in `Raw` but not in template data unless explicitly exposed.
- **`appURL` argument order** — the order is `host`, `port`, `useHTTPS`, then optional `path`. Swapping port and useHTTPS produces incorrect URLs silently.

## Related commands

- `devbox info` — render the full dashboard
- `devbox` (no args) — shows compact summary (not from `info.yml`, uses `ui.RenderSummary`)

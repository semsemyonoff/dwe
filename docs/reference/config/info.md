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
  - [`subgroup`](#subgroup)
  - [`separator`](#separator)
- [Decorative items](#decorative-items)
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
| `subgroup` | Container with optional title and nested items | `items` |
| `separator` | Blank line spacer | — |

All items accept an optional `when:` (Go template expression). Items with a falsy `when` are dropped from the rendered output. All items support an optional `decorative` boolean flag (see [Decorative items](#decorative-items)).

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
      value: '{{ appURL .Runtime.Hosts.Adminer .Runtime.Ports.App .Runtime.UseHTTPS }}'
      when: "{{ .Tools.Adminer.Enabled }}"
    - type: definition
      name: RedisInsight
      icon: "📊"
      value: '{{ appURL .Runtime.Hosts.RedisInsight .Runtime.Ports.App .Runtime.UseHTTPS }}'
      when: "{{ .Tools.RedisInsight.Enabled }}"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `title` | string | — | Optional header for the subgroup (plain string or template expression). When empty, the subgroup is rendered without a heading. |
| `items` | list | — | Required. Ordered list of child item definitions. Can contain any item type, including nested subgroups. |
| `when` | string | — | Condition; subgroup dropped from output if falsy. Child items are still evaluated independently for their own `when` conditions. |
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

Info templates have access to the standard devbox template surface: the `appURL` domain helper plus the sprout registries (`std`, `strings`, `numeric`, `slices`, `maps`, `regexp`, `conversion`, `time`, `filesystem`, `semver`). See [Templates](../templates.md) for the full helper reference.

Example using `appURL`:

```yaml
value: "{{ appURL .Runtime.Hosts.Main .Runtime.Ports.App .Runtime.UseHTTPS }}"
# → http://laravel.localhost (or https://… when use_https is true)
```

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
      - type: subgroup
        title: Main
        hide_on_empty: false
        items:
          - type: definition
            name: URL
            icon: "🔗"
            value: "{{ appURL .Runtime.Hosts.Main .Runtime.Ports.App .Runtime.UseHTTPS }}"
      - type: subgroup
        title: Tools
        when: "{{ .Tools.AnyEnabled }}"
        hide_on_empty: true
        items:
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
- **`hide_on_empty` with decorative items** — By default, content items like `definition`, `info`, and `warning` count toward section visibility, but `separator` does not. Use the `decorative` flag to override: set `decorative: true` on a content item to exclude it from the visibility calculation, or set `decorative: false` on a separator to make it count as content. A section with `hide_on_empty: true` is fully hidden if no content item (after `when` filtering) survives.
- **Footer rendering with `hide_on_empty`** — When `footer: true`, the footer is only rendered if at least one section produced output. If all sections are hidden via `hide_on_empty`, the footer is also suppressed.

## Related commands

- `devbox info` — render the full dashboard
- `devbox` (no args) — shows compact summary (not from `info.yml`, uses `ui.RenderSummary`)

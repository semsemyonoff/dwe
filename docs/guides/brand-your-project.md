# Brand your project

Make `dwe` look like *your* project. Customize the ASCII header that greets developers on first invocation, set a palette that matches your team's identity, and curate the `dwe info` dashboard so every URL, hostname, and credential a new joiner needs lives one command away.

Two files cover the entire surface: `workspace/styles.yml` for visual identity (header + colors + separator) and `workspace/info.yml` for the dashboard content. Both are optional — DWE ships sensible defaults — and both are loaded independently of the layered project config.

## Sections

- [Pick a header](#pick-a-header)
- [Color palette](#color-palette)
- [Separator character](#separator-character)
- [Curate the info dashboard](#curate-the-info-dashboard)
- [Item types you'll reach for](#item-types-youll-reach-for)
- [Conditional visibility](#conditional-visibility)
- [A note on icons and emoji](#a-note-on-icons-and-emoji)

## Pick a header

`workspace/styles.yml` controls the branded header rendered by `dwe` (no args) and `dwe info`. The brand identity line (`{▪} DWE · <project> · <version>`) always renders; everything else is layered on top.

```yaml
# workspace/styles.yml
header:
  lines:
    - "Welcome to"
    - "DWE Laravel"
  font: doom
  tagline: "Local dev, container-orchestrated."
```

- `header.lines` is rendered as ASCII art via FIGlet. Two short lines usually look better than one long one — banner fonts wrap awkwardly at narrow widths.
- `header.font` accepts standard FIGlet font names: `doom`, `banner`, `big`, `block`, `slant`, and similar. `doom` is the built-in default.
- `header.tagline` is a single muted line printed below the brand line. Skip it if you want a tighter header.

The ASCII block is always colored with the `accent` token — there is no separate `header.color`. Change the accent and the header re-tints to match.

See [styles.yml reference — header](../reference/config/styles.md#header) for the full schema.

## Color palette

DWE uses **seven semantic color tokens**. Every UI surface — tables, status sections, command browser, `--help` output — paints itself from this palette.

| Token | What it paints |
|-------|----------------|
| `accent` | Brand line, ASCII header, section titles, focused borders, table headers, active pagination |
| `success` | OK / running / enabled states, success notifications |
| `warning` | Warning diagnostics, partial / degraded states |
| `danger` | Error diagnostics, failed notifications |
| `muted` | Counts, separators, dimmed rows, tree glyphs, help descriptions |
| `border` | Default (unfocused) panel and table borders |
| `text` | Body text — leave empty to let the terminal pick the foreground color |

Override any subset. Tokens you leave out (or leave empty) fall back to the built-in defaults, which differ between light and dark backgrounds:

```yaml
colors:
  accent:  "#A78BFA"   # purple brand
  success: "#10B981"   # teal-leaning green
  muted:   "#94A3B8"
```

A few things to know up front:

- **Hex strings only.** Bare ANSI 256 codes or color names are not accepted.
- **One override applies in both modes.** There is no `light:` / `dark:` sub-tree — pick a hex that reads well on both backgrounds, or rely on the built-in light/dark defaults.
- **Terminal background is detected once at startup.** Editing `workspace/styles.yml` and re-running `dwe` is the supported way to retheme; the running process does not hot-reload.

For a monochrome look, set `accent` and `success` to the same hue family and let `muted` / `border` provide contrast.

See [styles.yml reference — colors](../reference/config/styles.md#colors) and [Light / dark resolution](../reference/config/styles.md#light--dark-resolution) for the full token reference and built-in defaults.

## Separator character

The `separator:` key controls the character between label and value in definition rows (`Project · laravel`):

```yaml
separator: "·"
```

Common alternatives: `"·"` (middle dot, the default), `"—"` (em dash), `":"` (colon — terse but reads as a heading break). Pick once and forget it.

## Curate the info dashboard

`workspace/info.yml` declares what `dwe info` shows: sections, items, conditional rows, template expressions. It is loaded separately from the 3-layer config and is *not* merged across layers — there is exactly one `info.yml` per project.

The minimal shape:

```yaml
# workspace/info.yml
sections:
  - id: <section-id>
    title: "Optional Section Title"
    items:
      - type: <item-type>
        # ... item fields

footer: true
```

Sections render in declaration order. The optional `footer: true` adds a closing table-header line below the last section.

If `workspace/info.yml` is absent, DWE renders a built-in default with a `URLs` section (auto-generated from service hosts and ports) and a `Hosts` section (the `/etc/hosts` lines a developer needs to add). The default is enough for many projects to ship without ever editing `info.yml`.

See [info.yml reference](../reference/config/info.md) for the full schema.

## Item types you'll reach for

Seven types cover everything the dashboard renders:

| Type | Renders as | Required fields |
|------|-----------|-----------------|
| `definition` | `Label — Value` row with optional icon | `name`, `value` |
| `info` | Info-coloured text line | `text` |
| `warning` | Warning-coloured text line | `text` |
| `auto-urls` | Service URLs auto-generated from `service.yml` | — |
| `auto-hosts` | Hostnames auto-generated from `service.yml` | — |
| `subgroup` | Container with optional title + nested items | `items` |
| `separator` | Blank-line spacer | — |

The two `auto-*` types are the heart of a maintained `info.yml`: rather than hard-coding every URL and host, point them at your service definitions and let DWE iterate. New service → new row, automatically.

A worked example combining the common types:

```yaml
sections:
  - id: project
    items:
      - type: subgroup
        title: DWE
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
      - type: auto-urls
        include: [app, tool]
        hide: [varnish]
        port_via: nginx

  - id: hosts
    title: Hosts
    items:
      - type: warning
        text: "Add these to your /etc/hosts:"
      - type: auto-hosts
        include: [app, tool, infra]

footer: true
```

For the per-item field reference, see [info.yml — item types](../reference/config/info.md#item-types).

## Conditional visibility

Every item accepts a `when:` Go template expression. Items with a falsy result are dropped from the rendered output; sections and subgroups can be auto-hidden when empty via `hide_on_empty:`.

```yaml
- type: definition
  name: SPX
  value: "{{ .Runtime.SPX.Path }}"
  when: "{{ .Runtime.SPX.Path }}"        # only when SPX is configured

- type: definition
  name: Adminer
  value: '{{ appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
  when: '{{ (index .Services "adminer").Enabled }}'   # only when the service is enabled
```

Three common gotchas:

- Always quote template expressions. YAML otherwise parses `{{ ... }}` as a flow mapping.
- Service field access uses Go's `text/template` syntax — `(index .Services "main").Host "web"`, with parentheses around `index`.
- `.Project`, `.Services`, `.Runtime`, and `.State` are exposed at the top level. Custom keys you add to `defaults.yml` live under `.Cfg.Raw`.

See [info.yml — template expressions](../reference/config/info.md#template-expressions) for the full template surface and the `appURL` helper signature.

## A note on icons and emoji

`definition` items support an `icon:` field that prepends a glyph to the value. Use it sparingly — a sprinkle of icons makes a dashboard readable; a wall of them is noise.

The one technical rule: **prefer codepoints with `Emoji_Presentation=Yes`** (e.g. `📦`, `🐳`, `💾`). Text-default codepoints like `🛢` (U+1F6E2), `🗄` (U+1F5C4), and `⚙` (U+2699) are dropped at render time to keep table columns aligned, because terminal width measurements disagree across font + terminal combinations. `dwe validate` flags them when you add one.

For the full caveat with character-by-character explanations, see [`icon` field — emoji caveat](../reference/config/services/fields.md#icon-field).

## See also

- [styles.yml reference](../reference/config/styles.md) — full schema, color tokens, light/dark resolution
- [info.yml reference](../reference/config/info.md) — full schema, item types, template expressions
- [services/fields reference — icon field](../reference/config/services/fields.md#icon-field) — emoji safety
- [shared-ide-and-agent-config](shared-ide-and-agent-config.md) — share template packs the same way you share branding

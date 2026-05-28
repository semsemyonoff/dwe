# styles.yml

UI styles configuration: ASCII header, semantic color palette, and separator.

## Contents

- [Purpose](#purpose)
- [Structure](#structure)
- [Field reference](#field-reference)
  - [`header`](#header)
  - [`colors`](#colors)
  - [`separator`](#separator)
- [Omitting the file](#omitting-the-file)
- [Light / dark resolution](#light--dark-resolution)
- [Customizing colors](#customizing-colors)
- [Migrating from the old palette](#migrating-from-the-old-palette)
- [Common pitfalls](#common-pitfalls)
- [Related commands](#related-commands)

## Purpose

`devbox/styles.yml` controls the visual appearance of the `devbox` CLI: the
branded header shown at startup, the seven semantic color tokens used across
every UI surface (tables, status sections, command browser, Fang help output),
and the separator character used in definition lists.

It is loaded by `LoadStylesConfig()` and applied at startup via
`ui.ApplyStyles()`. Omitting the file entirely produces identical built-in
defaults.

## Structure

```yaml
header:
  lines:
    - "Welcome to"
    - "Devbox Laravel"
  font: doom
  tagline: "Local dev, container-orchestrated."

colors:
  accent:  "#2EC3EB"
  success: "#22C55E"
  warning: "#F59E0B"
  danger:  "#EF4444"
  muted:   "#9AA3BB"
  border:  "#334155"
  text:    ""

separator: "·"
```

## Field reference

### `header`

Controls the branded header displayed by `devbox` (no args) and `devbox info`.
The brand identity line (`{▪} Devbox · <project> · <version>`) always renders;
the optional tagline and ASCII art are layered on top when configured.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `header.lines` | list of strings | _(none)_ | Text lines rendered as ASCII art under the brand line |
| `header.font` | string | `doom` | go-figure font name (doom, banner, big, block, slant, …) |
| `header.tagline` | string | _(none)_ | Single tagline line rendered in muted color below the brand line |

The ASCII block is colored using the `accent` token. There is no separate
`header.color` — color always derives from the semantic palette.

### `colors`

Seven semantic color tokens. Values are raw hex strings (e.g. `"#2EC3EB"`).
Empty / missing entries fall back to the built-in light or dark default for
that token (see [Light / dark resolution](#light--dark-resolution)).

| Token | Used for |
|-------|----------|
| `accent` | Branded surfaces — brand line, ASCII header, section titles, focused borders, table headers, filter matches, active pagination, `{▪}` logomark inner square |
| `success` | OK / running / enabled states; success notifications; `[--yes ON]` indicator |
| `warning` | Warning diagnostics; partial / degraded states |
| `danger`  | Error diagnostics; failed notifications |
| `muted`   | Secondary text — counts, separators, dimmed list rows, tree glyphs, inactive pagination, command/flag descriptions |
| `border`  | Default (unfocused) panel and table borders |
| `text`    | Default body text. Empty means "let the terminal pick the foreground color" — recommended in nearly all cases |

The same seven tokens drive Fang's `--help` rendering: title / command / flag /
program use `accent`, description / argument use `muted`. There is no longer a
separate `colors.help` sub-tree.

### `separator`

```yaml
separator: "·"
```

Character used between label and value in definition items (e.g.
`Project · laravel`).

## Omitting the file

If `devbox/styles.yml` does not exist, `LoadStylesConfig()` returns a
zero-value struct and `ui.ApplyStyles()` falls back to built-in defaults.
The CLI works identically — no error is produced.

## Light / dark resolution

Each token has a built-in light and dark hex default. At startup,
`ApplyStyles` calls `lipgloss.HasDarkBackground()` once and resolves every
token to a single hex string for the rest of the process. The resolved value
is exposed via `ui.Color*()` accessors and bridges three styling layers: v1
lipgloss (`internal/core/ui/`), v2 lipgloss (`internal/core/ui/cmdbrowser/`), and Fang's
`ColorScheme`.

| Token | Light default | Dark default |
|-------|---------------|--------------|
| `accent`  | `#0EA5E9` | `#2EC3EB` |
| `success` | `#16A34A` | `#22C55E` |
| `warning` | `#D97706` | `#F59E0B` |
| `danger`  | `#DC2626` | `#EF4444` |
| `muted`   | `#64748B` | `#9AA3BB` |
| `border`  | `#CBD5E1` | `#334155` |
| `text`    | _(empty — terminal default)_ | _(empty — terminal default)_ |

A user-provided non-empty value overrides both modes — overrides are not
per-mode. Re-running `ApplyStyles` after a profile change is the supported way
to retheme during a session.

## Customizing colors

Override any subset of tokens; unset tokens keep their light/dark defaults.

```yaml
colors:
  accent:  "#A78BFA"   # purple brand
  success: "#10B981"   # teal-leaning green
  muted:   "#94A3B8"
```

For a monochrome look, set `accent` and `success` to the same hue family and
let `muted`/`border` provide contrast.

## Migrating from the old palette

Earlier versions exposed 19 palette keys plus a nested `colors.help` block.
They collapse into the seven semantic tokens as follows:

| Old key | New token |
|---------|-----------|
| `label`, `section_title`, `subheader`, `info`, `table_header`, `focus_border`, `filter_match`, `pagination_active`, `mandatory` | `accent` |
| `enabled` | `success` |
| `partial` | `warning` |
| `description`, `tree_count`, `tree_arrow`, `pagination_inactive`, `disabled` | `muted` |
| `table_border` | `border` |
| `warning`, `muted` | _(unchanged — same key names)_ |
| `colors.help.*` (entire block) | _(removed — derived from `accent` + `muted`)_ |
| `header.color` | _(removed — always `accent`)_ |

Running `devbox validate` surfaces a rename hint per unknown key found in
`styles.yml`. Normal `devbox` commands load styles silently and do not warn.

## Common pitfalls

- **Using ANSI 256 codes instead of hex** — the schema now expects hex strings
  (`"#2EC3EB"`). Bare numeric codes are not valid.
- **Forgetting quotes on hex values** — YAML usually tolerates `#2EC3EB`
  unquoted (it is not a comment after a key), but quoting is the safer habit.
- **Per-mode overrides** — there is no `light:` / `dark:` sub-key. A user
  override applies in both modes; pick a hex that reads well on both
  backgrounds, or rely on the built-in defaults.
- **Old keys silently ignored at runtime** — the loader is lenient. Use
  `devbox validate` to catch stale keys before they become invisible no-ops.

## Related commands

- `devbox` (no args) — shows brand header + compact summary
- `devbox info` — shows full info dashboard with styled output
- `devbox validate` — surfaces rename warnings for legacy palette keys

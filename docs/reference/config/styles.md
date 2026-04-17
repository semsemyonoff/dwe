# styles.yml

UI styles configuration: ASCII header, color palette, and separator.

## Purpose

`devbox/styles.yml` controls the visual appearance of the `devbox` CLI: the ASCII art header shown at startup, the ANSI 256-color palette used throughout the UI, and the separator character used in definition lists.

It is loaded separately by `LoadStylesConfig()` and applied at startup via `ui.ApplyStyles()`. Omitting the file entirely produces identical built-in defaults.

## Structure

```yaml
header:
  lines:
    - "Welcome to"
    - "Devbox Laravel"
  font: doom
  color: red

colors:
  label: "203"
  section_title: "167"
  subheader: "209"
  muted: "245"
  warning: "214"
  info: "210"
  enabled: "2"
  disabled: "245"
  mandatory: "203"
  partial: "214"
  table_border: "167"
  table_header: "203"
  help:
    title: "203"
    command: "167"
    flag: "2"
    program: "209"
    description: "250"
    argument: "245"

separator: "—"
```

## Field reference

### `header`

Controls the ASCII art header displayed by `devbox` (no args) and `devbox info`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `header.lines` | list of strings | project name | Text lines rendered in ASCII art |
| `header.font` | string | `doom` | go-figure font name |
| `header.color` | string | `red` | Named ANSI color for ASCII art rendering |

Available font names come from the `go-figure` library (doom, banner, big, block, slant, etc.).

### `colors`

ANSI 256-color palette codes (as quoted strings). Used by the Lipgloss-based `internal/ui` package.

| Key | Usage |
|-----|-------|
| `label` | Definition label text (e.g. "Project", "URL") |
| `section_title` | Section header text (e.g. "── URLs ──") |
| `subheader` | Sub-section headers within a section |
| `muted` | Secondary/muted text (counts, separators, state labels) |
| `warning` | Warning messages |
| `info` | Informational messages |
| `enabled` | Status: enabled / running / active |
| `disabled` | Status: disabled / off |
| `mandatory` | Status: mandatory (always active) |
| `partial` | Status: partially running or stopped |
| `table_border` | Border lines in Lipgloss tables |
| `table_header` | Header text in Lipgloss tables |

#### `colors.help`

Fang/cobra help rendering colors. These override the default Fang color scheme.

| Key | Usage |
|-----|-------|
| `help.title` | Section headers in `--help` output (USAGE, COMMANDS, etc.) |
| `help.command` | Command names in help listings |
| `help.flag` | Flag names |
| `help.program` | Program name in usage line |
| `help.description` | Command/flag descriptions |
| `help.argument` | Argument placeholders (e.g. `[command]`) |

Color values are ANSI 256-color codes (0–255) as quoted strings. Use an ANSI 256-color chart to pick values.

### `separator`

```yaml
separator: "—"
```

Character used between label and value in definition items (e.g. `Project — laravel`).

## Omitting the file

If `devbox/styles.yml` does not exist, `LoadStylesConfig()` returns a zero-value struct and `ui.ApplyStyles()` falls back to built-in defaults. The CLI works identically — no error is produced.

## Customizing colors

To use a different palette, look up ANSI 256-color codes and set the appropriate keys. Example (monochrome/grayscale):

```yaml
colors:
  label: "250"
  section_title: "245"
  subheader: "248"
  muted: "240"
  warning: "220"
  info: "252"
  enabled: "2"
  disabled: "240"
  mandatory: "252"
  partial: "220"
  table_border: "245"
  table_header: "252"
```

## Common pitfalls

- **Using ANSI color names instead of codes** — `color` in `header` accepts named colors (`red`, `blue`, etc.) because it uses go-figure. All `colors.*` fields are numeric ANSI 256 codes as strings.
- **Forgetting quotes on numeric values** — YAML parses `203` as an integer; `"203"` is a string. The config loader expects strings for color codes.
- **Changing `separator` to a multi-character string** — the separator is inserted between label and value with a single space on each side. Multi-character separators work but may affect alignment.

## Related commands

- `devbox` (no args) — shows ASCII header + compact summary
- `devbox info` — shows full info dashboard with styled output

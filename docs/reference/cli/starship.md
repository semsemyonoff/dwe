# Starship Integration

Use `dwe prompt` to render a compact, project-aware segment inside your [Starship](https://starship.rs/) prompt.

## Overview

`dwe prompt` prints a single line for the current shell's working directory:

```
{▪} my-project ✓
```

- `{▪}` — the DWE logomark; the inner square is coloured with the project's `accent` token from `workspace/styles.yml`
- `my-project` — `project.name` from `workspace.yml`, falling back to the directory basename
- `✓`/`⟳`/`⚠`/`✗` — optional status icon coloured from the project's `success`/`warning`/`danger` tokens, omitted when no deploy state exists

When the shell is outside any DWE project, the command exits with code `1` and prints nothing — Starship hides the segment via its `when =` predicate.

`dwe prompt` is the hot path for shell prompts: it bypasses cobra, config validation, and lipgloss entirely. Cold-start budget is well under 50 ms on a modern machine.

## Installation

Add the following block to `~/.config/starship.toml`:

```toml
[custom.dwe]
command = "dwe prompt"
when    = "dwe prompt --check"
format  = "[$output]($style) "
style   = "bold"
description = "DWE project status"
```

The `when = "dwe prompt --check"` predicate is silent and exit-only: Starship runs it once per prompt to decide whether the segment is shown. The `command` form does the actual render and emits a coloured segment to stdout.

## Customisation

`dwe prompt` only colours the logomark and the status icon — the braces, project name, and surrounding whitespace are plain. That leaves the rest free for Starship's `style` and `format` to re-style without fighting embedded ANSI:

```toml
[custom.dwe]
command = "dwe prompt"
when    = "dwe prompt --check"
format  = "via [$output]($style) "
style   = "dimmed cyan"
```

The colour escapes inside the segment use `\x1b[39m` (default foreground only) so they do not reset surrounding attributes.

## Status icons

Evaluated in this precedence order — first match wins:

| Order | Condition | Icon | Colour token |
| --- | --- | --- | --- |
| 1 | deploy `status: failed` | `✗` | `danger` |
| 2 | deploy `status: partial` | `⚠` | `warning` |
| 3 | pending changes (`pending` block present) | `⟳` | `warning` |
| 4 | deploy `status: deployed` | `✓` | `success` |
| 5 | no state / `not_deployed` / parse error | _(omitted)_ | — |

Failed and partial outrank pending so the prompt surfaces broken state first — you need to know things are wrong *before* thinking about applying pending changes.

## Behaviour in non-color terminals

`dwe prompt` follows the [NO_COLOR](https://no-color.org/) spec: if the `NO_COLOR` environment variable is set to *any* value (including the empty string), all ANSI escapes are suppressed and the output is plain runes only.

```
{▪} my-project ✓
```

## Known limitations

- **Light/dark auto-detect**: the prompt always uses the dark variant of the palette. Most terminals are dark; light-terminal support can be added later via `COLORFGBG` if there is demand.
- **No `-c` flag**: `dwe prompt` always walks up from `$PWD`. This is intentional — the shell prompt reflects the shell's current directory, not an arbitrary project pointer.
- **Shell-specific quoting**: sh, bash, and zsh accept the `command` / `when` strings as written. Fish users may need to adjust quoting in `starship.toml` if their Starship config wraps commands differently.

## Before / after

Without the segment:

```
~/code/my-project ❯
```

With the segment:

```
{▪} my-project ✓ ~/code/my-project ❯
```

(The DWE logomark and status icon are coloured in real terminals.)

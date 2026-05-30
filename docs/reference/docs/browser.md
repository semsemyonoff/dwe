# Interactive TUI Browser

`devbox docs` opens an interactive terminal UI to browse all documentation.

## Usage

```bash
devbox docs
```

## Requirements

- TTY (terminal) — non-interactive use (pipes, agents) must use [`devbox docs show <topic>`](commands.md#devbox-docs-show-topic) or [`devbox docs list`](commands.md#devbox-docs-list) instead.

## Navigation

| Key | Action |
|-----|--------|
| `j` / `↓`, `k` / `↑` | Move down/up |
| `h` / `←`, `l` / `→` | Collapse/expand folders in the tree |
| `f` / `PgDn`, `b` / `PgUp` | Page down/up in the content pane |
| `g`, `G` | Jump to start/end |
| `Tab` | Switch focus between tree (left) and content (right) |
| `Enter` | Open a topic |
| `/` | Filter the tree by substring (case-insensitive match on dir names, file titles, and headings) |
| `]`, `[` | Jump to next/previous mermaid diagram |
| `o` | Open the focused mermaid diagram in the system viewer |
| `y` | Copy mermaid source via clipboard (OSC 52) |
| `L` | Cycle through available languages for the current topic |
| `e` | Jump to English original (when viewing a translation) |
| `r` | Reload the current topic (picks up live edits in project docs) |
| `q` / `Esc` / `Ctrl+C` | Quit |

A footer panel renders the full keymap so the bindings are always one glance away.

## Layout

- **Left pane:** Collapsible file tree of all topics (Devbox built-ins and Project docs if present)
- **Right pane:** Rendered markdown content with syntax highlighting
- **Status bar:** Current topic path, mermaid progress, active language, and keyboard hints

## See also

- [Non-interactive commands](commands.md) — `show`, `list`, `export`, `llms-txt`, `cache clear`
- [Translations and language behavior](translations.md) — how `L` and `e` resolve translations

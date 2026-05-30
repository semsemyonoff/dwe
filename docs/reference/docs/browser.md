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
| `j` / `k` | Move up/down in the left tree pane |
| `h` / `l` | Collapse/expand folders |
| `Tab` | Switch focus between tree (left) and content (right) |
| `Enter` | Open a topic |
| `/` | Search headings (within the current document) |
| `n` / `N` | Jump to next/previous search result |
| `]d` / `[d` | Navigate to next/previous mermaid diagram |
| `d` / `Enter` on diagram | View diagram inline (if supported) or open in system viewer |
| `o` | Always open diagram in system viewer |
| `y` | Copy diagram source code via clipboard (OSC 52) |
| `L` | Cycle through available languages for the current topic |
| `e` | Jump to English original (when viewing a translation) |
| `r` | Reload the current topic (picks up live edits in project docs) |
| `?` | Show help |
| `q` | Quit |

## Layout

- **Left pane:** Collapsible file tree of all topics (Devbox built-ins and Project docs if present)
- **Right pane:** Rendered markdown content with syntax highlighting
- **Status bar:** Current topic path, mermaid progress, active language, and keyboard hints

## See also

- [Non-interactive commands](commands.md) — `show`, `list`, `export`, `llms-txt`, `cache clear`
- [Translations and language behavior](translations.md) — how `L` and `e` resolve translations

# Interactive TUI Browser

`dwe docs` opens an interactive terminal UI to browse all documentation.

## Usage

```bash
dwe docs
```

## Requirements

- TTY (terminal) — non-interactive use (pipes, agents) must use [`dwe docs show <topic>`](commands.md#dwe-docs-show-topic) or [`dwe docs list`](commands.md#dwe-docs-list) instead.

## Navigation

| Key | Action |
|-----|--------|
| `j` / `↓`, `k` / `↑` | Move down/up |
| `h` / `←` | Collapse the focused folder, or step to its parent when already collapsed |
| `l` / `→` | Expand the focused folder, or step into its first child when already expanded |
| `f` / `PgDn`, `b` / `PgUp` | Page down/up in the content pane |
| `g`, `G` | Jump to start/end |
| `Tab` / `Shift+Tab` | Cycle focus forward/backward between tree (left) and content (right) |
| `Enter` | Open a topic |
| `/` | Filter the tree by substring (case-insensitive match on dir names, file titles, and headings) |
| `]`, `[` | Jump to next/previous mermaid diagram |
| `o` | Open the focused mermaid diagram in the system viewer |
| `y` | Copy mermaid source via clipboard (OSC 52) |
| `L` | Cycle through available languages for the current topic |
| `e` | Jump to English original (when viewing a translation) |
| `Ctrl+R` | Reload the current topic (picks up live edits in project docs) |
| `?` | Toggle a modal listing the keys active in the current mode |
| `q` / `Esc` / `Ctrl+C` | Quit |

Press `?` to open a help modal generated from the bindings active in the current mode — the modal lists exactly the keys above without occupying a permanent footer.

## Mouse

The browser also responds to the mouse:

- **Single-click** a tree row to move the cursor there and focus that pane.
- **Double-click** a tree row to open it (same as `Enter`).
- **Wheel** scrolls the focused pane.
- **Click** the `? help` hint to open the help modal.

## Layout

- **Left pane:** Collapsible file tree of all topics (DWE built-ins and Project docs if present)
- **Right pane:** Rendered markdown content with syntax highlighting
- **Status bar:** Current topic path, mermaid progress, active language, and keyboard hints

## See also

- [Non-interactive commands](commands.md) — `show`, `list`, `export`, `llms-txt`, `cache clear`
- [Translations and language behavior](translations.md) — how `L` and `e` resolve translations

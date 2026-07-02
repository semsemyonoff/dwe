# `ui` configuration

The optional `ui:` block in `workspace.yml` configures the interactive command browser used by `dwe commands` when invoked without an exact command ID.

The browser runs on the shared `tui` framework: two bordered panels (tree on the left, command list on the right), a bottom status line (brand · breadcrumb · `? help`), and a `?`-triggered modal help that lists the bindings active in the current mode. Panel focus cycles with `Tab` / `Shift+Tab`; the focused panel is highlighted by its border. The mouse is supported (see [Mouse](#mouse)).

The block is loaded with the same lenient loader as the rest of `workspace.yml`: an absent block and unknown keys are silently ignored at load time. The dedicated `ui` validator (run via `dwe validate`) surfaces unknown keys as warnings and invalid values (e.g. a negative depth) as errors.

## Schema

```yaml
ui:
  commands:
    default_expanded_depth: 1        # int, default 1
    auto_collapse_empty: true        # bool, default true
    show_type_badges: true           # bool, default true
```

### `ui.commands.default_expanded_depth`

Controls how many tree levels are expanded by default when the command browser opens. Negative values are clamped to `0` by the accessor and rejected as an error by the validator.

Like `auto_collapse_empty` and `show_type_badges`, this field uses a `*int` pointer so the loader can distinguish an absent key (nil → use the spec default of `1`) from an explicit value. Setting `default_expanded_depth: 0` means all-collapsed (no groups open on entry); omitting the key restores the default of `1` (only the top-level groups are expanded). Any positive integer expands to that depth.

### `ui.commands.auto_collapse_empty`

When `true` (default), fuzzy-filter sessions automatically collapse and dim subtrees with zero matches; the previous expansion state is restored on `Esc`.

### `ui.commands.show_type_badges`

When `true` (default), the right-hand command list shows a colour-coded type badge (`shell`, `script`, `workflow`, `service_exec`, `service_run`, `builtin`, `dwe`) next to each command ID. Set `false` to suppress badges on narrow or monochrome terminals.

## Pointer semantics — omit vs explicit zero

All three fields (`default_expanded_depth`, `auto_collapse_empty`, `show_type_badges`) use pointer types (`*int` / `*bool`) so the loader can distinguish an absent key (nil → use the spec default) from an explicit value. Plain `int`/`bool` would conflate these two states because an absent key and an explicit `0`/`false` both deserialize to the zero value.

Practical implications:
- Setting `default_expanded_depth: 0` collapses all groups on entry; omitting the key gives the default of `1`.
- Setting `auto_collapse_empty: false` or `show_type_badges: false` is a deliberate opt-out; omitting either key restores its default of `true`.

## Example

```yaml
# workspace.yml
ui:
  commands:
    default_expanded_depth: 2
    auto_collapse_empty: true
    show_type_badges: false
```

Omit the block entirely to accept all defaults; existing `workspace.yml` files without a `ui:` block keep behaving identically.

## Hotkeys

Focus moves between the two panels with `Tab` / `Shift+Tab`. Navigation and select bindings are dispatched to the focused panel; tree-only verbs (`→/l`, `←/h`) are inert in the list, and the run-mode verbs (`e`, `y`) are absent outside run mode. The `?` modal lists exactly the bindings active in the current mode.

| Binding | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | cycle focus between the tree and the list panel |
| `↑/↓`, `k/j` | move the cursor in the focused panel |
| `→`, `l` | expand the focused group, or step into its first child (tree only; no-op in the list) |
| `←`, `h` | collapse the focused group, or step to its parent (tree only; no-op in the list) |
| `Home` / `End` | jump to the first / last row of the focused panel |
| `PgUp` / `PgDn` | scroll the focused panel one viewport |
| `Enter` | on a tree group: toggle expansion; on a list item: confirm the selection (run, inspect, or edit, depending on entry point) |
| `/` | enter inline filter mode |
| `i` | open the inspect overlay for the highlighted command |
| `y` | toggle skip-confirm (run mode only); the status line shows `[--yes ON]` |
| `e` | confirm the highlighted command and force the param form open (run mode only) |
| `?` | toggle the modal help |
| `Esc`, `q`, `Ctrl+C` | exit the browser |

While the **inline filter** is active (`/`), typed characters refine the fuzzy query — the query line renders inside the tree panel, the tree narrows live with updated `M/N` match counts, `Enter` commits the filtered expansion, and `Esc` restores the prior expansion state. Action letters (`i`, `e`, `y`, …) are typed into the query, not dispatched, while filtering.

While the **inspect overlay** is open (`i`), it captures input: `↑/↓`, `k/j`, `PgUp/PgDn`, `Home`/`End` scroll the centred viewport, `Enter` confirms (returns the same `Result` as `Enter` from the list), and `Esc` closes the overlay.

The `e` (edit-parameters) and `y` (skip-confirm) bindings are only registered in run mode (the default when `--inspect` / `-i` is not set); in inspect and the vars-browser edit mode they are absent from both the keymap and the `?` help.

## Fallback ladder

The browser inspects the terminal at startup and degrades gracefully. Below the two-panel minimum (or on a size-read failure) it drops straight to the flat `huh.NewSelect` list — there is no in-TUI single-panel mode.

| Condition | Behaviour |
| --- | --- |
| non-TTY | the call site short-circuits with the existing `no exact command ID given; pass a full command ID or run in an interactive terminal` error; the browser is never reached (and `Run` defensively cancels if it is) |
| TTY, `width < 80` or `height < 15` | delegate to the flat `huh.NewSelect` list (the pre-browser UX) |
| TTY, `width ∈ [80, 99]` | two-panel frame without `(N)` group counts and without type badges |
| TTY, `width ≥ 100` | full two-panel frame with badges and counts (the breadcrumb is always shown in the bottom status line) |

`NO_COLOR=1` is honoured automatically via lipgloss/bubbletea: badges render as plain text and the focus marker uses bold instead of colour.

## Mouse

The frame enables mouse support:

- **Single click** in a panel moves the cursor to the clicked row and sets focus to that panel; it never toggles a group or runs a command.
- **Double click** acts as `Enter` on the clicked row — it toggles a tree group or confirms a list item.
- **Wheel** scrolls the panel under the pointer (the tree, the command list, or an open inspect overlay) without changing focus — it works regardless of which panel is focused.
- Clicking the `? help` hint in the status line toggles the modal help; clicks inside an open modal are swallowed.

## Related

- [`workspace.md`](workspace.md) — top-level configuration overview
- [`commands/`](commands/index.md) — user command definitions
- [`styles.md`](styles.md) — palette keys consumed by the type badges

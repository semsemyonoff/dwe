# `ui` configuration

The optional `ui:` block in `devbox.yml` configures the interactive command browser used by `devbox commands` when invoked without an exact command ID.

The block is loaded with the same lenient loader as the rest of `devbox.yml`: an absent block and unknown keys are silently ignored at load time. The dedicated `ui` validator (run via `devbox validate`) surfaces unknown keys as warnings and invalid values (e.g. a negative depth) as errors.

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

When `true` (default), the right-hand command list shows a colour-coded type badge (`shell`, `script`, `workflow`, `service_exec`, `service_run`, `builtin`, `devbox`) next to each command ID. Set `false` to suppress badges on narrow or monochrome terminals.

## Pointer semantics — omit vs explicit zero

All three fields (`default_expanded_depth`, `auto_collapse_empty`, `show_type_badges`) use pointer types (`*int` / `*bool`) so the loader can distinguish an absent key (nil → use the spec default) from an explicit value. Plain `int`/`bool` would conflate these two states because an absent key and an explicit `0`/`false` both deserialize to the zero value.

Practical implications:
- Setting `default_expanded_depth: 0` collapses all groups on entry; omitting the key gives the default of `1`.
- Setting `auto_collapse_empty: false` or `show_type_badges: false` is a deliberate opt-out; omitting either key restores its default of `true`.

## Example

```yaml
# devbox.yml
schema_version: "2"

ui:
  commands:
    default_expanded_depth: 2
    auto_collapse_empty: true
    show_type_badges: false
```

Omit the block entirely to accept all defaults; existing `devbox.yml` files without a `ui:` block keep behaving identically.

## Hotkeys

The command browser has four focus modes. The visible bindings change with focus; the dynamic help footer (`?`) lists the active subset.

| Mode | Binding | Action |
| --- | --- | --- |
| left (tree) | `↑/↓`, `k/j` | move cursor within the tree |
| left | `→`, `l` | expand focused group |
| left | `←`, `h` | collapse focused group, or move to its parent |
| left | `Space` | toggle expansion |
| left | `Home` / `End` | jump to first / last visible row |
| left | `Enter` | drill into focused group (expand if needed and move focus right) |
| left | `Tab` | move focus to the right list |
| left | `/` | enter filter mode |
| left | `?` | toggle long help |
| left | `Esc`, `q`, `Ctrl+C` | exit the browser |
| right (list) | `↑/↓`, `k/j` | move cursor within the list |
| right | `Enter` | confirm selection (run or inspect, depending on entry point) |
| right | `i` | open inspect overlay for the highlighted command |
| right | `y` | toggle skip-confirm (run mode only); footer shows `[--yes ON]` |
| right | `←`, `Tab` | return focus to the tree |
| right | `/` | enter filter mode |
| right | `Esc`, `q` | exit the browser |
| filter | (typing) | refine the fuzzy query; `M/N` counts update in the tree |
| filter | `↑/↓`, `Enter`, `i`, `y` | as in right mode, against the filtered result list |
| filter | `Esc` | exit filter and restore the prior expansion state |
| inspect | `↑/↓`, `k/j`, `PgUp/PgDn` | scroll the inspect viewport |
| inspect | `Enter` | confirm (returns the same `Result` as `Enter` from the list) |
| inspect | `Esc` | close the overlay; focus returns to the right list |

The `y` skip-confirm binding is only active in run mode (the default when `--inspect` / `-i` is not set); under inspect mode it is removed from the keymap.

## Fallback ladder

The browser inspects the terminal at startup and degrades gracefully:

| Condition | Behaviour |
| --- | --- |
| non-TTY | the call site short-circuits with the existing `no exact command ID given; pass a full command ID or run in an interactive terminal` error; the browser is never reached |
| TTY, `width < 60` or `height < 15` | delegate to the flat `huh.NewSelect` list (today's pre-browser UX) |
| TTY, `width ∈ [60, 79]` | single-panel mode with `── group ──` pseudo-headers; no tree, no badges |
| TTY, `width ∈ [80, 99]` | two-panel mode without `(N)` group counts and without type badges |
| TTY, `width ≥ 100` | full two-panel mode with badges, counts, and breadcrumb |

`NO_COLOR=1` is honoured automatically via lipgloss/bubbletea: badges render as plain text and the focus marker uses bold instead of colour.

## Related

- [`devbox.md`](devbox.md) — top-level configuration overview
- [`commands/`](commands/index.md) — user command definitions
- [`styles.md`](styles.md) — palette keys consumed by the type badges

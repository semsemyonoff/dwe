# Milestone: Unified TUI Framework & Redesign

Status: specification (pre-planning). Each stage below is scoped to become its own
implementation plan via `/planning:make`. This document defines direction, goals, target
architecture, and staged breakdown — not the per-stage implementation detail.

## 1. Context

`dwe` currently ships three independent full-screen bubbletea/v2 TUIs plus a family of
`huh`-based interactive forms. Each TUI re-implements the same visual and interaction
scaffolding, and none share an input or layout layer.

| Surface | Path | ~LOC | Layout | Help | Tree | Mouse | Stack |
|---------|------|------|--------|------|------|-------|-------|
| Command browser | `internal/core/ui/cmdbrowser/` | 1500 | title(top) + tree + list + help footer | persistent footer | own widget | none | lipgloss v2 |
| Docs browser | `internal/core/docs/tui/` | 900 | title(top) + tree + viewport + status + help footer | persistent footer | own widget | none | lipgloss v2 |
| Status dashboard | `internal/core/ui/statustui/` | 400 | title(top) + tabs + viewport + status bar | `?` toggles inline | n/a (tabs) | none | **lipgloss v1** |

Interactive forms (`huh/v2`) are partly centralised behind `internal/core/ui/ask/` and
`internal/core/ui/widgets/`, but three sites bypass the wrapper and call `huh.NewForm`
directly (deploy menu ×2, setup wizard port overrides, setup wizard service toggles) to
install custom keymaps the wrapper cannot express. `deploy/menu.go` carries ~80 lines of
duplicated select-menu plumbing.

The non-interactive live pipeline view (`internal/shared/liveui/`) is intentionally
out of scope — it is not an interactive program and has its own contract.

### Problems

1. **Duplicated chrome** — title bar, help footer rendering (`·`-separated wrapping),
   tree clipping, and focus-driven border styling are structurally identical across the
   three TUIs but copy-pasted, not shared.
2. **Two independent tree widgets** — cmdbrowser and docs each have a custom
   expandable-tree-with-filter widget with zero shared code.
3. **Stack drift** — `statustui` is on lipgloss v1 while the rest are on v2.
4. **No modal help** — help is a persistent footer everywhere; no `?`-invoked modal.
5. **No mouse support** anywhere — zero `tea.MouseMsg` handling.
6. **Imperative keymaps** — every TUI uses `key.Matches` chains with hand-written help
   text; nothing is action-driven, so help cannot be generated and bindings are fixed.
7. **Form keymap overrides don't scale** — custom keymaps require dropping out of the
   shared `ask` wrapper into raw `huh.NewForm`.

## 2. Goals

- A single reusable TUI framework that owns layout, overlays, action-based keymaps,
  focus, and mouse — consumed by all three TUIs as content plugins.
- A redesigned frame: brand/title moved to a bottom status line; help promoted to a
  `?`-invoked modal; project-styled panel borders retained.
- First-class mouse support (wheel + click) implemented once in the framework.
- A single, generic tree widget shared by command and docs browsers.
- Unified, scalable form keymap overrides; the three raw `huh` sites collapse back into
  the `ask` wrapper; deploy-menu duplication removed.
- The whole charm stack standardised on v2 (migrate `statustui` off lipgloss v1).

### Non-goals

- Rewriting `huh` forms onto native bubbletea (forms stay keyboard-only; their loop is
  not migrated into the framework).
- Touching the non-interactive `liveui` pipeline view.
- Drag-to-resize, text selection, or other advanced mouse interactions beyond
  wheel-scroll and click in this milestone.

## 3. Design references

Prior art whose interaction model and visual philosophy we adopt — modal help invoked by
`?`, a single bottom status line instead of a permanent help footer, mouse support, panel
layouts, and theme-able chrome:

- **lazygit** — panel-based layout, `?` keybinding menu, mouse support, fully
  keybinding-driven.
- **k9s** — bottom status/command line, `?` help, theme/skin support.
- **gh-dash** — same charm/bubbletea stack; sectioned views with generated keybinding
  help.
- **Helix** — modal editor with a bottom status line and `?`/menu-driven discoverability.
- **revdiff** — sibling project with a compatible interaction model (overlay manager,
  action-based keymap, mouse hit-testing) we can draw on directly.

## 4. Target architecture

### New package `internal/core/ui/tui/`

The framework skeleton. Lives under `core/ui/` (not `shared/`) because it depends on the
`core/ui/styles` palette — it is sink-layer infrastructure for the CLI's interactive
surfaces, consistent with the existing `core/ui` placement.

- **`Frame`** — owns the screen layout (body region + bottom status line) and composites
  overlays on top of the body. The status line carries: brand + project (left), a
  plugin-supplied context string (middle), and a `? help` hint (right). Overlays are
  centred over the body and never cover the status line.
- **Overlay manager** — mutually-exclusive modals (help, inspect, future), ANSI-aware
  centred compositing over the body, and click hit-testing within modal bounds (clicks
  outside a modal are swallowed, not dismissed).
- **Action keymap registry** — maps `action → binding + description + section`. The help
  modal is generated from the active registry. Plugins register their own actions; the
  framework dispatches key/mouse input to action handlers. Lays the groundwork for
  re-bindable keys and muscle-memory aliases.
- **Focus manager** — tracks the active panel and applies project-styled borders with a
  focus highlight.
- **Mouse router** — wheel (debounced so a trackpad flick does not thrash rendering),
  click → hit-zone classification, and routing of clicks into the overlay manager.

### Frame layout

```
┌─ panel (project-styled border, focus highlight) ─┬─ panel ──────────────────┐
│                                                  │                          │
│  BODY — content plugin                           │                          │
│  (tree + list  /  tree + viewport  /  tabs +     │                          │
│   viewport). Borders & focus owned by Frame.     │                          │
│                                                  │                          │
│                                                  │                          │
│                                                  │                          │
└──────────────────────────────────────────────────┴──────────────────────────┘
 {▪} dwe · project · <context from plugin>                              ? help
 └─ brand + project (left) ──┴── plugin context (middle) ──┴── help hint (right)
```

Press `?` → help modal, centred over the BODY (status line stays visible):

```
┌─ panel ──────────────────────────────┬─ panel ───────────────────────────────┐
│              ╭──────────────── Help ──────────────╮                           │
│  BODY        │  Navigation   ↑/↓ · j/k   move     │                           │
│  (dimmed     │  Panels       tab         focus    │                           │
│  beneath     │  Filter       /           search   │                           │
│  the         │  Inspect      i           details  │                           │
│  overlay)    │  Quit         q · esc      close    │                           │
│              ╰─ generated from the action keymap ──╯                          │
└────────────────────────────────────────┴──────────────────────────────────────┘
 {▪} dwe · project · <context>                                            ? help
```

- Brand/title lives only in the bottom status line — there is **no** top title bar and
  **no** permanent help footer.
- The overlay is composited over the body and never covers the status line; clicks
  outside the modal are swallowed, not dismissed.
- Help contents are generated from the active action keymap, so they always reflect the
  effective (and eventually re-bound) bindings.

### Content-plugin contract

A plugin provides: the **body view**, a **context string** (middle of the status line),
and its **set of actions** (with handlers). Command browser, docs browser, and status
dashboard become three plugins. The framework owns everything else.

### Shared generic tree widget `internal/core/ui/tui/tree`

A generic expandable tree with filtering and visible-node rendering, parameterised over a
pluggable node type. Absorbs both current custom tree implementations.

### Stack standardisation

All TUIs on lipgloss v2; `statustui` migrates off v1 as part of its stage.

## 5. Stages

Each stage is independently plannable via `/planning:make`. Ordering reflects
dependencies; stage 7 (forms) is independent and may run early or in parallel.

| # | Stage | Depends on | Summary |
|---|-------|-----------|---------|
| 0 | Framework skeleton | — | `tui` package: `Frame` (body + bottom status line), overlay manager, `?`-modal help generated from the action registry, focus manager, action-keymap registry mechanism. Built standalone with a test harness. Highest-risk core — built in isolation. |
| 1 | Keybinding design | 0 | Decide the full action taxonomy, default bindings, mouse semantics, help sections, and backwards-compatible aliases for muscle memory across all three TUIs and forms. Deliverable: a keymap reference doc + encoded default registry. Gate before any migration. |
| 2 | Mouse layer in `Frame` | 0, 1 | Wheel (debounced), click hit-testing, overlay click routing — implemented once in `Frame` so every plugin inherits it. |
| 3 | Generic tree widget | 0 | Extract the shared expandable-tree-with-filter into `tui/tree` with a pluggable node type. |
| 4 | Pilot: command browser | 0–3 | Migrate `cmdbrowser` onto `Frame` + tree widget + action keymap + help modal + bottom status line + mouse. End-to-end validation of the framework and delivery of the redesign on the flagship surface. |
| 5 | Docs browser | 4 | Migrate `docs/tui` onto `Frame` + shared tree + help modal + mouse + bottom status line. |
| 6 | Status dashboard | 4 | Migrate `statustui` onto `Frame` (+ lipgloss v1 → v2), tabs as a body plugin, help modal, tab clicks. |
| 7 | Forms unification | — (independent) | Single `RunHuhForm` helper; `ask.RunOptions` gains scalable keymap overrides; migrate the three raw `huh` sites (deploy menu, port overrides, service toggles) back into `ask`; remove `deploy/menu.go` duplication. |

## 6. Cross-cutting concerns (every stage)

- **Golden tests** for layouts and help-modal contents; deterministic frame rendering via
  the existing test-hook patterns.
- **i18n** of help/section strings (the `workspace/i18n` YAML namespace), consistent with
  the display-string localisation contract.
- **Non-TTY / narrow-terminal fallbacks** preserved (current behaviour: narrow terminals
  fall back to a plain selector; non-TTY errors before launch).
- **`docs/internals/packages.md`** updated with the new `tui` package contract and the
  per-surface plugin notes.

## 7. Open questions / risks

- **Pilot choice** — `cmdbrowser` is assumed (most loaded surface, best-matched to the
  reference interaction model). Revisit if docs/status proves a simpler first migration.
- **`huh` overlay coexistence** — forms run their own loop; confirm pause/resume
  interplay (`widgets.RunWithPromptHooks`) still holds when a form is launched from within
  a framework-hosted TUI.
- **Border vs. overlay geometry** — project-styled panel borders are retained; verify the
  overlay centring math accounts for bordered panels.
- **Render performance** — wheel debounce and overlay compositing should be benchmarked on
  large trees / long docs to avoid flicker.

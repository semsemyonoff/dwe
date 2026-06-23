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

**Layering rule.** `core/ui/` is a sink layer (per `docs/internals/packages.md` — only
`internal/cli/**` is meant to import it). Migrating the docs browser onto this framework
formalises a `core/docs → core/ui` dependency, which `docs/internals/packages.md` does not
currently sanction. This milestone explicitly resolves that by **relocating the docs TUI
to `internal/core/ui/docstui/`** (peer of `cmdbrowser`/`statustui`), keeping `core/docs`
free of `core/ui` imports and leaving the `tui` package consumed only from `core/ui/*` +
`cli/`. The internals doc is updated to record the rule. `Frame` itself depends only on
the `styles.Color*()` string accessors and `charm.land/lipgloss/v2` — never on exported
v1 `lipgloss.Style` values (the existing styling bridge stays load-bearing).

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

A plugin is a **managed child model**, not just a view function. "Body view + context
string + actions" is the visible surface, but the real interface must cover everything the
three existing TUIs already do, or the framework will become shared chrome while each
surface keeps private input/async/sizing/cleanup logic (defeating the unification). The
`Plugin` interface — pinned by the end of stage 0 — covers:

- **Lifecycle** — `Init() tea.Cmd`, and a cleanup/cancel hook (docs has a file watcher and
  prefetch; status has async tab loads — both need deterministic teardown on exit).
- **Resize** — the plugin receives **inner** body dimensions (post-border), and declares
  whether it has a narrow-terminal fallback layout; the framework owns the outer geometry.
- **Message routing** — the framework forwards unmatched `tea.Msg` to the plugin so its own
  async messages survive (`topicLoadedMsg`, `FileChangedMsg`, `ProgressMsg`, spinner ticks,
  tab-load/reload messages). The framework must not swallow plugin messages.
- **Result semantics** — a typed result on exit (cmdbrowser returns a selected
  action/index; docs and status mostly return cancellation/error). The framework's run
  loop returns the plugin's result unchanged.
- **Panels & focus** — the plugin declares its panels (regions) so the focus manager and
  mouse hit-testing have something concrete to target; the framework cannot own focus
  meaningfully otherwise.
- **Status segments** — the plugin supplies the middle status-line context (and may update
  it reactively), not just a static string.
- **Overlays** — the plugin can **request** framework overlays (inspect, filter, future),
  not only render beneath them.
- **Action handlers** — the plugin registers its actions into the keymap registry; the
  framework dispatches key/mouse input to handlers.

Command browser, docs browser, and status dashboard become three plugins; the framework
owns layout, overlays, focus, mouse, and the run loop.

### Generic tree widget `internal/core/ui/tui/tree`

A generic expandable tree with filtering and visible-node rendering, parameterised over a
pluggable node type. **Not extracted up front** — the cmdbrowser pilot builds its tree
against the framework first, and the generic `tui/tree` is factored out during the docs
migration, when a second real consumer is in hand. The docs tree carries metadata the
command tree does not (heading levels, multiple roots, per-node localization, stale-
translation state, file/folder folding); designing the generic node type without that
second consumer risks overfitting to cmdbrowser or missing required fields.

### Charm-stack standardisation — scope

The interactive TUI **chrome** (the `tui` framework + all three plugins) is wholly on
lipgloss/bubbles/bubbletea v2. `statustui` migrates off lipgloss v1 as part of its stage.
**This milestone does not commit to removing v1 from the entire dependency graph** —
`internal/core/ui/render/` still builds status content with v1 lipgloss tables, and
porting that is a larger, separable effort. Scope here is: no v1 in the interactive TUI
packages; `render/`'s v1 usage is explicitly out of scope and called out as follow-up.

### Panel geometry model

Defined once in `Frame`, not per plugin — lipgloss v2's width/height-around-borders
semantics already forced local fixes in cmdbrowser, so the framework owns the single
source of truth:

- **Outer frame size** = terminal width × (height − status line). **Inner content size** =
  outer minus border/padding; plugins only ever see inner dimensions.
- Border ownership is the framework's; plugins never draw the panel border.
- The **overlay coordinate space** is the body region (overlays centre over inner body,
  never over the status line). Centring math accounts for bordered panels.
- Golden frame tests at width buckets **60 / 79 / 80 / 99 / 100** and both odd/even widths
  lock the geometry against regressions.

### Terminal & mouse policy

- **alt-screen** — framework-hosted TUIs run in the alt-screen; the framework owns enter/exit.
- **Mouse enable mode** — fixed explicitly (wheel + click reporting via the v2 mouse model;
  full-motion `tea.WithMouseAllMotion` is **not** used). Mouse is opt-in per program so a
  plugin can decline. Decided concretely in the mouse stage.
- **Capability fallbacks (framework-owned, not per plugin)** — non-TTY errors before
  launch; `TERM=dumb` and terminals without mouse degrade to keyboard-only; narrow/tiny
  terminals fall back to the existing plain selector. Ownership lives in `Frame`, so every
  plugin inherits identical behaviour.
- **Wheel debounce** = per-frame **coalescing** of deltas, never blind dropping — a fast
  trackpad burst sums into one scroll, a slow wheel still registers each tick. Tested both ways.

### Form interop rule

Forms (`huh/v2`) keep their own loop. Launching a form from **inside** an active
alt-screen bubbletea program is materially different from `widgets.RunWithPromptHooks`
wrapping a whole-program launch. This milestone's rule: **framework-hosted TUIs do not
launch huh forms inline.** A surface that needs a form exits the TUI, runs the form via the
unified `ask` path, and (if applicable) re-enters — no nested alt-screen pause/resume. If a
future surface genuinely needs an inline form, that is a separate design, not assumed here.

## 5. Stages

Each stage is independently plannable via `/planning:make`. Ordering reflects
dependencies. Stages 5a and 6 are independent and may run early or in parallel.

| # | Stage | Depends on | Summary |
|---|-------|-----------|---------|
| 0 | Framework skeleton (spike) | — | `tui` package: `Frame` (body + bottom status line), overlay manager, `?`-modal help, focus manager, panel-geometry model, and a **provisional** action-keymap registry. Pins the `Plugin` interface (lifecycle / resize / message routing / result / panels / overlays / actions). Built standalone with a test harness. **Registry API is explicitly not locked** — it is shaped here but finalised in stage 1, so stage 0 is a spike, not a frozen contract. |
| 1 | Keybinding & action design | 0 | Decide the full action taxonomy, default bindings, mouse semantics, help sections, and backwards-compatible aliases for muscle memory across all three TUIs and forms. **Locks the registry API** that stage 0 prototyped. Deliverable: a keymap reference doc + encoded default registry. Gate before any migration. |
| 2 | Mouse layer in `Frame` | 0, 1 | Wheel (per-frame coalescing), click hit-testing, overlay click routing, mouse-enable mode + per-program opt-in — implemented once in `Frame` so every plugin inherits it. |
| 3 | Pilot: command browser | 0–2 | Migrate `cmdbrowser` onto `Frame` + action keymap + help modal + bottom status line + mouse. Builds a **cmdbrowser-local tree** (not yet generic). End-to-end validation of the framework + redesign on the flagship surface. Preserves existing result/action semantics, vars-browser edit mode, and force-param-form behaviour. |
| 4 | Docs browser + generic tree | 3 | Relocate the docs TUI to `internal/core/ui/docstui/` and migrate it onto `Frame` + help modal + mouse + bottom status line. **Extract the generic `tui/tree`** now that two consumers' needs are known (headings, multi-root, localization, stale-translation, folding); refactor cmdbrowser's tree onto it. Preserve docs watcher/prefetch behaviour. |
| 5a | Status dashboard → lipgloss v2 | — | Migrate `statustui` off lipgloss v1 with **no** framework redesign and **no** layout change — pure dependency migration, isolated from async/reload behaviour. Scoped to interactive chrome (see § Charm-stack scope). |
| 5b | Status dashboard → `Frame` | 3, 5a | Tabs as a body plugin, help modal, tab clicks, bottom status line. Preserve reload + scroll-offset (`YOffset`) preservation. |
| 6 | Forms unification | — (independent) | Single `RunHuhForm` helper; `ask.RunOptions` gains scalable keymap overrides; migrate the three raw `huh` sites (deploy menu, port overrides, service toggles) back into `ask`; remove `deploy/menu.go` duplication. |

## 6. Cross-cutting concerns (every stage)

- **Test matrix** — golden frame tests at the width buckets above (odd/even); help-modal
  contents; mouse routing into the correct hit-zone; async-message preservation (plugin
  messages survive the framework's update loop); i18n help text; capability fallbacks
  (non-TTY, no-mouse, narrow). Deterministic rendering via the existing test-hook patterns.
- **Migration compatibility** — each migration preserves the surface's existing observable
  behaviour: cmdbrowser result/action semantics + vars-browser edit mode + force-param-form;
  docs watcher/prefetch; status reload + `YOffset` preservation. Regression-tested per stage.
- **i18n** of help/section strings — a dedicated framework i18n key namespace in the
  `workspace/i18n` YAML namespace, consistent with the display-string localisation contract.
- **Non-TTY / narrow-terminal fallbacks** owned by `Frame`, not per plugin (current
  behaviour preserved: narrow → plain selector; non-TTY → error before launch).
- **JSON / plain output unaffected** — these surfaces are interactive-only; non-interactive
  output paths are untouched.
- **`docs/internals/packages.md`** updated with the new `tui` package contract, the
  `core/ui` layering rule (incl. the `docstui` relocation), and per-surface plugin notes.

## 7. Open questions / risks

- **`Plugin` interface completeness** — the contract is pinned in stage 0, but the first
  real migration (stage 3) is the true test. Accept that stage 3 may feed one revision back
  into the interface before it is frozen for stages 4–5b.
- **Overlay/border geometry** — more than centring math: lipgloss v2 width/height-around-
  borders semantics already needed local fixes in cmdbrowser. The outer-vs-inner model is
  pinned once in `Frame` and locked by golden tests at 60/79/80/99/100 columns.
- **`huh` form interop** — resolved by rule (framework-hosted TUIs do not launch inline
  forms; § Form interop). Risk is only if a surface is later found to genuinely need an
  inline form — treat as a separate design.
- **Mouse wheel feel** — naive debounce drops intentional scrolls; mitigated by per-frame
  coalescing, but needs real-device testing (trackpad burst vs slow wheel).
- **statustui v2 migration size** — underestimated if "whole stack on v2" is read as
  removing v1 everywhere. Bounded here to interactive chrome; `core/ui/render/`'s v1
  lipgloss tables are explicit follow-up, not part of this milestone.
- **Render performance** — wheel coalescing and overlay compositing benchmarked on large
  trees / long docs to avoid flicker.

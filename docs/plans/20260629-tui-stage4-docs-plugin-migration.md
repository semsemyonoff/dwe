# TUI Stage 4 — Plan 1: Docs Browser Relocation + Frame Plugin Migration

## Overview

Migrate the docs browser TUI onto the shared `tui` framework (`internal/core/ui/tui`),
the same `Frame`/`Plugin` machinery the cmdbrowser pilot (Stage 3) already runs on. Two
sub-stages, ideally two reviewable commits:

- **4.1 Relocation** — move `internal/core/docs/tui/` → `internal/core/ui/docstui/` and
  rename `package tui` → `package docstui` (it now imports the framework package also named
  `tui`). Pure move, no behavior change. This formalises the `core/ui` layering rule from
  the milestone spec §4: `core/docs` keeps no `core/ui` import, and the `tui` framework is
  consumed only from `core/ui/*` + `cli/`.
- **4.2 Plugin migration** — re-express the docs model as a `tui.Plugin` hosted on the
  `Frame`: project-styled bordered panels, a bottom status line, `?`-invoked modal help, and
  mouse support — replacing the old title bar + persistent help footer. Preserve every
  existing behavior (live-reload watcher, prefetch diagram progress, filter, locale cycling,
  diagram open/copy, heading navigation, scroll).

This is **Plan 1 of Stage 4**. The generic `tui/tree` extraction (sub-stage 4.3) is a
**separate future plan (Plan 2)** and is explicitly OUT of scope here. This plan only
*aligns* the docs tree's Frame-facing surface with cmdbrowser's so Plan 2's extraction is a
clean lift — it introduces no shared tree code.

**Intended executor: Sonnet.** Large in volume, low in novelty — mechanical relocation plus
pattern-following against the cmdbrowser Stage-3 reference. The two genuinely risky areas
(call them out explicitly while implementing) are **async preservation** (watcher + prefetch)
and **panel geometry / golden tests**.

## Context (from discovery)

**Spec:** `docs/plans/specs/2026-06-23-tui-framework-milestone.md` — Stage 4 row + §4
(target architecture), §6 (cross-cutting test matrix), §7 (open risks).

**Reference implementation to mirror throughout:** `internal/core/ui/cmdbrowser/` — the
Stage-3 pilot already migrated onto the framework. Key files:
- `cmdbrowser/plugin.go` — the `tui.Plugin` implementation (Init/Close/Resize/Update/
  ViewPanel/Panels/StatusContext/Actions/HandleAction/PendingOverlay/Result/CapturingInput).
- `cmdbrowser/run.go` — the `Run` wrapper over `tui.Run`, TTY gate, error mapping.
- `cmdbrowser/tree.go` — the Frame-facing tree surface to mirror: `topIdx` field,
  `ensureFocusVisible(height int)`, `focusRow(row int)`.
- `cmdbrowser/filter.go` — inline-capture filter pattern (`CapturingInput()`).
- `cmdbrowser/inspect.go` — `Overlay{CapturesInput:true}` pattern (reference only; docs has
  no inspect overlay).
- `cmdbrowser/plugin_golden_test.go` — golden frame tests via `tui.RenderFrame` /
  `tui.BuildHelp` (`internal/core/ui/tui/testsupport.go`).

**Framework contracts:** `internal/core/ui/tui/plugin.go` (Plugin interface, PanelClickMsg,
FocusChangedMsg, FocusRequestMsg, OverlayClosedMsg, Overlay), `tui/run.go` (`Run`,
`RunOptions`, `ErrNotTTY`, `ErrTooNarrow`; **takes NO context**), `tui/actions.go` +
`tui/registry.go` (action taxonomy: stdlib `ActionNavUp/Down/Left/Right/Top/Bottom/PageUp/
PageDown/Select/Reload/Filter/Inspect`; built-ins `ActionHelp`=`?`, `ActionQuit`=`q/ctrl+c`
+esc alias, `ActionFocusNext`=`tab`, `ActionFocusPrev`=`shift+tab`; `RegisterStandard`).
Framework `minWidth=40`, `minHeight` via `tooNarrow`. `layoutPanels` is pure-proportional
(weight share, last panel absorbs remainder; **no per-panel min-width floor**).

**Current docs TUI** (`internal/core/docs/tui/`, fully lipgloss **v2** already — no v1
migration needed):
- `model.go` (~27k) — `NewModel` (:131), `Init` (:611), `Update` (:626), `View` (:844),
  `quit` (:414) closes Prefetch+Watcher. Holds Tree, Viewport, StatusBar, Filter, Help,
  DiagramState, FocusZone (FocusTree/FocusViewport), heading-line index.
- `view.go` (~15k) — `renderTwoPanel` (:50); geometry `leftWidth`=`max(w/6,20)`,
  `rightWidth`, `bodyHeight` (:473+). Renders title bar, two bordered panels, status line,
  2-row help footer.
- `tree_widget.go` — `TreeWidget` + `TreeNode` (pointer-cursor identity, multi-root groups,
  heading sub-rows, `index.md` folding, built-in `ApplyFilter` two-phase
  `markFilterMatches`). No `topIdx`/scroll-clip/mouse today.
- `tree_filter.go`, `keys.go`, `statusbar.go`, `viewport.go`.
- `watcher.go` — `FileChangedMsg` (:12), `eventPump` goroutine, `Close` cancels ctx.
- `prefetch.go` — `ProgressMsg` (:27, carries `Generation`), worker pool
  `MaxPrefetchWorkers=2`, per-topic cancel, `Close`.
- `diagram.go` + `diagram_inline.go` — mermaid inline rendering (body-internal).
- `heading_anchors.go` — H2/H3 anchor indexing (body-internal).
- `osc52.go` — clipboard copy via `y` (body-internal).
- **Caller:** `internal/cli/docs/docs.go:157` builds `tea.NewProgram(model, …)` directly;
  the package has **no exported `Run`** today.

**Verified import facts:**
- The **only** real code importer of `core/docs/tui` is `cli/docs/docs.go`. The match in
  `cmdbrowser/inspect.go` is a **comment** ("docs-browser scrollbar"); its `tui.` refs are
  the framework `core/ui/tui`, not docs. The other hits are docs markdown
  (`docs/internals/*.md`).

**Patterns / conventions:** table-driven tests next to code; `make build` after `docs/`
edits (embedded-docs sync); `make test` only (never bare `go test ./...` — embedded docs are
gitignored/generated). Display-string localization via the typed `store.*` / `i18n.Translator`
contract; framework help strings live in the `tui.help.*` i18n namespace (allowlisted in
`KnownUIKeys`).

## Development Approach

- **Testing approach: Regular** (migrate/port code in each task, then add/adapt tests in the
  same task). For a behavior-preserving migration the production code moves first; tests
  follow to lock the ported behavior.
- Complete each task fully before moving to the next; small, focused changes.
- **Every task MUST include new/updated tests** for the code it changes (separate checklist
  items, not bundled with implementation).
- **All tests must pass before starting the next task** (`make test`).
- Keep this plan file in sync with actual work; update scope notes if reality deviates.
- Behavior-preservation is the prime directive: the docs browser must look and behave
  identically except for the deliberate chrome redesign (bottom status line + `?`-modal +
  project-styled borders + mouse).

## Testing Strategy

- **Unit tests**: required every task. Most existing docs tests (`model_test.go`,
  `scroll_test.go`, `prefetch_test.go`, `watcher_test.go`, `tree_widget_test.go`,
  `tree_index_test.go`, `diagram_test.go`, `osc52_test.go`, `heading_anchors_test.go`) move
  with the package and are adapted to the plugin shape — their coverage of
  watcher/prefetch/scroll/filter/heading behavior must be preserved.
- **Golden frame tests**: render the docs plugin through the framework `Frame` at width
  buckets **60 / 79 / 80 / 99 / 100** (odd + even), via `tui.RenderFrame` / `tui.BuildHelp`
  (`internal/core/ui/tui/testsupport.go`) — mirror `cmdbrowser/plugin_golden_test.go`.
- **No project e2e suite** (this is a CLI/TUI Go project) — golden + unit tests are the
  regression net.
- Cross-cutting checks from spec §6: help-modal contents; mouse routing into tree vs
  viewport hit-zones; async-message preservation (FileChangedMsg + ProgressMsg survive the
  Frame update loop); i18n help text incl. Diagrams/Locales sections; capability fallbacks
  (non-TTY → `ErrNotTTY`, `<40` → `ErrTooNarrow` mapped).

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- Update the plan if implementation deviates from scope.

## Solution Overview

The docs model becomes a `tui.Plugin` (`browser`) hosted on the framework `Frame`. The
`Frame` owns the terminal envelope (alt-screen, mouse mode), the bordered panel geometry, the
bottom status line (brand/project left, plugin context middle, `? help` right), the
`?`-invoked help modal generated from the action registry, and focus/mouse routing. The
plugin owns body content only: a **tree** panel (weight 1) and a **viewport** panel (weight
5), rendered via `ViewPanel(id, inner)`; the existing diagram-inlining, heading-anchor, and
osc52 logic stay intact and are rewired into `Update`/`HandleAction`/the viewport render.

The old chrome — title bar, 2-row help footer, hand-rolled status line, panel borders, and
the whole `renderTwoPanel`/`View` path — is deleted. The old status-line content (current
path + 📊 N/M diagram progress + `[lang]`) becomes `StatusContext()`.

Because `tui.Run` threads no context, `docstui.Run(ctx, opts)` keeps the cobra context,
derives a cancellable child, hands it to the plugin, and the plugin cancels it in `Close()`
alongside `Watcher.Close()` + `Prefetch.Close()`. `Plugin.Init()` batches the watcher
subscription + initial topic load + prefetch-progress subscription; the framework forwards
`FileChangedMsg`/`ProgressMsg` to `Plugin.Update`, preserving prefetch generation filtering
and per-topic cancel exactly.

## Key Design Decisions (locked via brainstorm)

1. **Panels**: `{ID:"tree", Weight:1}` + `{ID:"viewport", Weight:5}` — pure proportional,
   **drop** the `max(w/6,20)` floor (framework `layoutPanels` has no per-panel min-width;
   cmdbrowser uses `{2,7}` the same way). **Caveat**: docs tree labels are long H1 headings
   (unlike cmdbrowser's short IDs), and dropping the old 20-col floor makes the tree's inner
   width ≈6 cells at the 60-col bucket. Treat `{1,5}` as the starting weight; **validate H1
   truncation against the 60/79 goldens (Task 11) and bump toward `{2,7}`/`{2,5}` if labels
   are unreadable** — record the final weight here.
2. **Focus**: `tab` is the framework `ActionFocusNext` built-in; with two panels it
   reproduces the current Tab tree↔viewport toggle. The plugin tracks the active panel via
   `tui.FocusChangedMsg` and routes nav keys (up/down → tree move + topic-load when tree
   focused, else viewport scroll) — mirror cmdbrowser.
3. **Async + context**: `docstui.Run(ctx, opts)` owns the context; plugin cancels it in
   `Close()`. `Init` batches watcher + initial load + prefetch-progress. `FileChangedMsg` /
   `ProgressMsg` flow Frame → `Plugin.Update`. Preserve generation filtering + per-topic
   cancel.
4. **Chrome → status line + `?`-modal**: delete title bar + 2-row help footer; old status
   line → `StatusContext()`; Frame owns borders + focus highlight.
5. **Keymap** → stage-1 taxonomy: nav `k/j/b/f/g/G` → stdlib; `h/l`+`enter` →
   collapse/expand/select (plugin-interpreted); `/` → `ActionFilter`; `?` `q/esc/ctrl+c` →
   built-ins. **Reload = `ctrl+r`** (stdlib `ActionReload`; accept the change from old `r` —
   no `r` alias, no custom binding). Docs-custom actions: `[ ] o y` (diagram
   prev/next/open/copy) under a **Diagrams** section; `L e` (locale cycle / English) under a
   **Locales** section.
6. **Filter**: current tree filter becomes inline-capture (`CapturingInput()==true` while
   filtering, raw keys → `Update`), mirroring cmdbrowser; still filters the tree via the
   existing `ApplyFilter`.
7. **Result**: quit-only; `Result()` returns nil; `docstui.Run` returns error only (cancel →
   `widgets.ErrCancelled`, panic → wrapped), like cmdbrowser.
8. **Narrow terminal**: inherit framework floor (`minWidth=40`); **no** 80-col pre-gate, **no**
   flat fallback. `<40` → `tui.Run` returns `ErrTooNarrow` → `docstui.Run` maps it to a clean
   "terminal too small" error.
9. **Future-proofing for Plan 2** (generic `tui/tree`, NOT here): when adding Frame
   integration to the docs `TreeWidget` (scroll-clip by inner height + mouse click focus),
   **mirror cmdbrowser `treeModel`'s surface and names exactly** — `topIdx`,
   `ensureFocusVisible(height int)`, `focusRow(row int)`. **No** shared/generic tree code in
   this plan. The two trees stay structurally different internally (docs: pointer-cursor +
   headings + multi-root + index folding + built-in filter; cmdbrowser: string-id + counts +
   leaves) — reconciliation is Plan 2's job.
10. **Initial sizing — command-capable first load; NO re-render on resize** (Codex reviews,
    critical). Ground truth from the real code (do not repeat the pass-1 misreading that the
    resize handler re-renders):
    - The old `WindowSizeMsg` handler (`model.go:630-637`) **does NOT re-render markdown**: it
      updates `TermWidth/ContentWidth`, calls `Viewport.SetDimensions(...)`, returns `m, nil`.
      Glamour content is hard-wrapped at render time and is **not re-flowed on resize**; the
      content re-renders at a new width only on the next *load* event (topic switch / `reload`
      / `FileChanged` / locale change), which uses the then-current `ContentWidth`. (The
      `loadTopic` at `model.go:650` is the `FileChangedMsg` branch, NOT resize.)
    - The old first render takes its width from the **caller-supplied** terminal size at
      construction (`model.go:145,189-191`; `loadTopic` reads `m.ContentWidth` `model.go:270`).
      The framework supplies geometry only LATER (Frame starts at zero geometry; `tui.Run`
      reads size only to gate; `RenderFrame` applies one `WindowSizeMsg`).
    - **Framework constraint**: `Plugin.Resize(body Region)` is **void** (cannot return a
      `tea.Cmd`); `ViewPanel` returns a string (also no Cmd). Only `Plugin.Update` can return
      a Cmd, and the Frame **forwards `WindowSizeMsg` to `Update`** (verified). `Resize`
      receives the overall body inner region, NOT the per-panel viewport inner width;
      `layoutPanels` is unexported (`geometry.go:122`) so docstui cannot call it.
    - **Required design**: (a) do NOT render at construction — **fire the first `loadTopic`
      once from `Update(tea.WindowSizeMsg)`**, guarded by a `firstLoadDone` flag, at the
      computed **viewport-panel inner width**; (b) compute that inner width in the plugin from
      the cached body region + the plugin's own `{1,5}` weights (last-panel remainder, minus
      4 for border+padding per the framework inner-width rule) — **pin it with a test asserting
      equality with the `inner.Width` the Frame passes to `ViewPanel(panelViewport, inner)`**
      at each width bucket; if replication proves fragile, add a tiny exported `tui` geometry
      helper (spec §7 explicitly allows feeding one framework revision back); (c) on resize,
      `ViewPanel(viewport, inner)` sizes the viewport widget to `inner` each render (cmdbrowser
      pattern) and `Update(WindowSizeMsg)` updates the tracked `ContentWidth` for the *next*
      load — **never trigger a re-render/`loadTopic` from resize** (no load-storm, no YOffset
      reset; matches old behavior).
11. **Mermaid theme resolution must survive** (Codex review, high): `NewModel` resolves the
    user's `auto`/`light`/`dark` config through `resolveMermaidTheme` (`model.go:137`), which
    probes the terminal background via `lipgloss.HasDarkBackground` for `auto`; `diagramTheme`
    treats only the resolved `"light"` as light. The migrated constructor MUST call
    `resolveMermaidTheme(opts.MermaidTheme)` — storing `opts.MermaidTheme` raw would make the
    default `auto` render every diagram as dark (wrong cache keys + open/copy for
    light-background users). Because `HasDarkBackground` is non-deterministic, golden tests
    must force a concrete resolved theme (see Task 11).

## Technical Details

- **Body-internal logic survives intact, just rewired**: `diagram_inline.go`,
  `heading_anchors.go`, `osc52.go` operate on viewport content and never touch the Frame.
  Rewire into `Plugin.Update` / `HandleAction` (for `[ ] o y`) and the viewport panel render.
  No framework changes.
- **Section renderer contract**: `ViewPanel` returns a string; the plugin never returns a
  `tea.View` and never draws panel borders (Frame owns them).
- **Status segments**: `StatusContext()` returns the middle zone string (path + diagram
  progress + locale); brand/project (left) and `? help` (right) are Frame-owned.
- **i18n**: register help/section display strings in the `tui.help.*` namespace; add the
  Diagrams/Locales section labels and docs action descriptions to the `KnownUIKeys`
  allowlist; thread `Translator`/`Locale` through `docstui.Run` → `tui.RunOptions` (mirror
  cmdbrowser `Options.Translator/Locale`).

## What Goes Where

- **Implementation Steps** (`[ ]`): relocation, plugin code, tests, internals-doc updates.
- **Post-Completion** (no checkboxes): manual smoke-test of live-reload + diagram open in a
  real terminal; visual confirmation of the redesigned chrome and mouse feel.

## Implementation Steps

### Task 1: Relocate `core/docs/tui` → `core/ui/docstui` (sub-stage 4.1)

**Files:**
- Move (git mv): every file under `internal/core/docs/tui/` → `internal/core/ui/docstui/`
- Modify: all moved `*.go` files — `package tui` → `package docstui`
- Modify: `internal/cli/docs/docs.go` (import path + package selector)
- Modify: `docs/internals/packages.md`, `docs/internals/tui-keymap.md` (path references)

- [x] `git mv internal/core/docs/tui internal/core/ui/docstui` (preserve history); confirm
      `internal/core/docs/tui/` no longer exists
- [x] rename the package declaration in every moved file: `package tui` → `package docstui`
      (incl. `*_test.go`)
- [x] update `internal/cli/docs/docs.go` to import `internal/core/ui/docstui` and use the
      `docstui.` selector for `NewModel` and any other referenced symbols
- [x] grep the whole tree for stale `core/docs/tui` references
      (`grep -rn "core/docs/tui" internal/ docs/`) and fix code refs; update prose path
      mentions in `packages.md` / `tui-keymap.md`
- [x] confirm no NEW `core/docs → core/ui` import is introduced *yet* (the relocated package
      is `core/ui/docstui`; `core/docs` itself must remain free of `core/ui` imports —
      `grep -rn "core/ui" internal/core/docs/`)
- [x] `make build` (regenerate embedded docs) — must succeed
- [x] `make test` — all moved tests pass unchanged (behavior identical); this task adds no
      new tests (pure relocation, existing tests are the regression net)

### Task 2: `docstui` plugin skeleton (`tui.Plugin` shell)

**Files:**
- Create: `internal/core/ui/docstui/plugin.go`
- Create: `internal/core/ui/docstui/plugin_test.go`
- Modify: `internal/core/ui/docstui/model.go` (expose state the plugin reuses)

- [x] define a **separate** `type browser struct` implementing `tui.Plugin` that **reuses
      `Model`'s data fields** (embed or hold a `*Model` for its Tree/Viewport/Filter/
      DiagramState/heading-index/loaded-topic state). **Do NOT rename `Model`→`browser` and do
      NOT delete/alter the old `Model.Init`/`Update`/`View`/`quit`** — they stay untouched and
      compilable through the Task 6–9 coexistence window and are removed only in Task 10
      (mirrors how cmdbrowser kept a distinct `browser` and deleted `model.go` last)
- [x] implement `Panels()` → `[]tui.Panel{{ID:"tree",Title:"Contents",Weight:1},
      {ID:"viewport",Title:"",Weight:5}}`; define `panelTree`/`panelViewport` `tui.PanelID`
      consts (mirror cmdbrowser)
- [x] implement trivial contract methods: `Resize(body tui.Region)` (cache inner body size),
      `Result() any` (nil), `CapturingInput() bool` (false for now), `PendingOverlay()`
      (none for now), `StatusContext()` (stub), `Actions`/`HandleAction` (empty for now),
      `Init`/`Close`/`Update`/`ViewPanel` (minimal stubs that compile)
- [x] add a compile-time assertion `var _ tui.Plugin = (*browser)(nil)`
- [x] write tests: `Panels()` shape (ids/weights), `Result()` nil, `CapturingInput()` false,
      plugin satisfies `tui.Plugin`
- [x] `make test` — must pass before Task 3

### Task 3: Tree panel render + Frame-facing tree surface (mirror cmdbrowser)

**Files:**
- Modify: `internal/core/ui/docstui/plugin.go` (ViewPanel for tree)
- Modify: `internal/core/ui/docstui/tree_widget.go` (add Frame surface)
- Modify: `internal/core/ui/docstui/tree_widget_test.go`

- [x] add to `TreeWidget` the cmdbrowser-parallel Frame surface: `topIdx int` field,
      `ensureFocusVisible(height int)`, `focusRow(row int)` — names/semantics mirroring
      `cmdbrowser/tree.go` exactly (clip visible rows to inner panel height; click→cursor
      without toggling expansion; no-op past last row) [future-proofing for Plan 2]
- [x] implement `ViewPanel(panelTree, inner)` — render the visible tree rows clipped to
      `inner.Height` via `topIdx`/`ensureFocusVisible`, cursor highlight on the focused row;
      reuse the existing row-label/indent rendering (no border — Frame owns it)
- [x] preserve the filter header row behavior decision for Task 7 (here render unfiltered
      tree only)
- [x] write tests: `ensureFocusVisible` clamping (focus above/below window, short/tall
      panel), `focusRow` mapping incl. out-of-range no-op, tree `ViewPanel` golden-ish string
      at a fixed inner size
- [x] `make test` — must pass before Task 4

### Task 4: Viewport panel render (content + scrollbar + diagrams + headings)

**Files:**
- Modify: `internal/core/ui/docstui/plugin.go` (ViewPanel for viewport)
- Modify: `internal/core/ui/docstui/viewport.go`, `diagram_inline.go` (call into from
  ViewPanel)
- Modify: `internal/core/ui/docstui/scroll_test.go`

- [x] implement `ViewPanel(panelViewport, inner)` — render the markdown viewport content
      into `inner`, including the existing scrollbar overdraw, diagram-inline placeholders,
      and heading-anchor-aware content; size the viewport to `inner` (no border)
- [x] size the viewport widget to the panel `inner` region **inside `ViewPanel(panelViewport,
      inner)`** every render (`Viewport.SetDimensions(inner.Width, inner.Height)` — cmdbrowser
      pattern), dropping the old `viewportInnerWidth/Height` chrome math
- [x] **do NOT re-render markdown on resize** (Decision #10): a resize only resizes the
      viewport *window*; the hard-wrapped content stays at its render width and re-renders only
      on the next load event (topic switch / reload / FileChanged / locale) — exactly the old
      `WindowSizeMsg` handler (`model.go:630-637`). This preserves `YOffset` (no reload) and
      avoids a drag-resize render storm
- [x] preserve diagram active-index sync on scroll (`syncActiveDiagram`) and heading line
      index usage
- [x] write/adapt tests: viewport content renders at given inner dims; scrollbar present;
      diagram placeholder lines appear; heading-line index intact (adapt `scroll_test.go`);
      **a resize to a new width does NOT trigger a reload and preserves `YOffset`** (window
      resizes, content width unchanged until the next load)
- [x] `make test` — must pass before Task 5

### Task 5: Action registry + `HandleAction` (keymap mapping)

**Files:**
- Create: `internal/core/ui/docstui/actions.go`
- Modify: `internal/core/ui/docstui/plugin.go`
- Create: `internal/core/ui/docstui/actions_test.go`
- Remove later (Task 10): `internal/core/ui/docstui/keys.go` (old `key.Binding` keymap)

- [ ] implement `Actions(reg *tui.Registry) error` — `RegisterStandard(reg, ActionNavUp,
      NavDown, PageUp, PageDown, Top, Bottom, Select, Filter, Reload)` (reload thus = `ctrl+r`),
      then register docs-custom actions: `diagram.prev`(`[`), `diagram.next`(`]`),
      `diagram.open`(`o`), `diagram.copy`(`y`) in a **Diagrams** section; `locale.cycle`(`L`),
      `locale.english`(`e`) in a **Locales** section; `tree.collapse`(`h`/`left`),
      `tree.expand`(`l`/`right`) — surface collision errors before launch
- [ ] **INTENTIONAL behavior change** (document it as such, do NOT let it slip in silently):
      the old docs tree toggled expansion on BOTH `h` and `l` (`model.go:792-799` → both call
      `Tree.Toggle`). The migration adopts cmdbrowser's **directional** semantics for
      cross-surface unification — `h`/`←` collapse (or step to parent when already collapsed),
      `l`/`→` expand (or step into first child when already expanded). Record this in
      `tui-keymap.md` (Task 12) as a deliberate unification change
- [ ] add an active-panel field to `browser` (default `panelTree`) so `HandleAction` can
      route nav by focus from this task on; the `tui.FocusChangedMsg` handler that *updates*
      it lands in Task 8 (Task 5 tests set the field directly)
- [ ] implement `HandleAction(a tui.Action) (tea.Cmd, bool)` dispatching each action to the
      existing behavior, honoring the active-panel field (nav routes to tree vs viewport);
      `enter` → expand/load + focus viewport (emit `tui.FocusRequestMsg{Panel:panelViewport}`
      as a `tea.Cmd`); return `false` for unhandled
- [ ] confirm `tab`/`shift+tab`/`?`/`q`/`esc`/`ctrl+c` are NOT registered by the plugin (they
      are framework built-ins)
- [ ] write tests: `Actions` registers without collision; `HandleAction` returns handled/cmd
      for each action; nav routing differs by focused panel; reload bound to `ctrl+r`;
      **directional `h`/`l` semantics** (`h` on expanded collapses, `h` on collapsed steps to
      parent; `l` on collapsed expands, `l` on expanded steps into first child) — locking the
      intentional change away from the old toggle-on-both
- [ ] `make test` — must pass before Task 6

### Task 6: Async lifecycle — Init batch, message routing, Close teardown ⚠️ RISKY

**Files:**
- Modify: `internal/core/ui/docstui/plugin.go` (Init/Update/Close + plugin-owned ctx)
- Modify: `internal/core/ui/docstui/model.go` (move teardown out of old `quit`)
- Modify: `internal/core/ui/docstui/watcher_test.go`, `prefetch_test.go`

- [ ] give the plugin a plugin-owned `context.Context`+`cancel` (derived from the ctx passed
      into `docstui.Run` in Task 10; until then accept it via the constructor)
- [ ] implement `Init() tea.Cmd` batching: watcher subscription (`waitForFileChange`) +
      prefetch-progress subscription. **`Init` does NOT issue the initial topic load** (Frame
      geometry is still zero there — Decision #10)
- [ ] fire the first `loadTopic` **from `Update(tea.WindowSizeMsg)`** (the only command-capable
      hook; `Resize` is void), guarded by a `firstLoadDone` flag so it fires exactly once, at
      the **computed viewport-panel inner width** (from the cached body region + `{1,5}`
      weights — Decision #10); update tracked `ContentWidth` on every `WindowSizeMsg` for
      subsequent loads
- [ ] add a width-replication pin test: the plugin's computed viewport inner width == the
      `inner.Width` the Frame passes to `ViewPanel(panelViewport, inner)` at widths 60/79/80/
      99/100 (guards against drift from `layoutPanels`)
- [ ] drop the old `m.initCmd`/construction-time `loadTopic` path (`model.go:189-191`) — the
      first render must use the framework-supplied width, never `viewportInnerWidth(0)`
- [ ] implement `Update(msg tea.Msg) tea.Cmd` to handle `FileChangedMsg` (reload current
      topic if path matches) and `ProgressMsg` (drop stale generations, re-inline diagrams,
      re-subscribe) — these arrive via the framework forwarding unmatched messages; preserve
      generation filtering + per-topic cancel exactly
- [ ] implement `Close() error` → `cancel()` + `Watcher.Close()` + `Prefetch.Close()` (move
      the body of the old `quit`); ensure idempotent/­nil-safe
- [ ] write tests: FileChangedMsg triggers reload of matching path only; stale-generation
      ProgressMsg is dropped; `Close` cancels ctx and closes watcher+prefetch (no goroutine
      leak — reuse existing watcher/prefetch test seams); **the initial topic load fires
      exactly once from the first non-zero-width `Update(WindowSizeMsg)` and renders at the
      computed viewport inner width (assert content width / heading-index count matches that
      width, NOT a zero/construction width); a second `WindowSizeMsg` does NOT re-fire the
      load**
- [ ] `make test` — must pass before Task 7

### Task 7: Inline filter capture mode (`CapturingInput`)

**Files:**
- Modify: `internal/core/ui/docstui/plugin.go`, `tree_filter.go`
- Modify: `internal/core/ui/docstui/plugin_test.go` (or new `filter_test.go`)

- [ ] make `/` (`ActionFilter`) enter filter mode; `CapturingInput()` returns true while
      filtering; in capture mode raw keys route to `Update` and edit the query
      (printable/backspace/enter/esc), mirroring `cmdbrowser/filter.go`
- [ ] on each query edit call the existing `ApplyFilter` and re-render the tree (filter
      header row "/ query (N)" shown in the tree panel); `enter` commits (keep selection,
      expand ancestors), `esc` cancels and restores; return focus to tree via
      `FocusRequestMsg` where needed
- [ ] ensure `CapturingInput()` returns false outside filter mode (restores registry
      dispatch)
- [ ] write tests: entering/editing/committing/cancelling filter; `CapturingInput()`
      transitions; filtered visible-set matches `ApplyFilter`; ancestor expansion on commit
- [ ] `make test` — must pass before Task 8

### Task 8: Mouse wiring (PanelClick / FocusChanged / wheel)

**Files:**
- Modify: `internal/core/ui/docstui/plugin.go`
- Modify: `internal/core/ui/docstui/plugin_test.go`

- [ ] handle `tui.FocusChangedMsg` → update the plugin's active-panel tracking (tree vs
      viewport) so nav routing follows focus
- [ ] handle `tui.PanelClickMsg`: click in tree → `focusRow(Y)` (move cursor, no toggle);
      click in viewport → no-op or position-aware (match current behavior; viewport has no
      click target today)
- [ ] confirm wheel scroll reaches the focused panel (tree move vs viewport scroll) via the
      framework wheel routing — no per-plugin wheel mode needed
- [ ] write tests: FocusChangedMsg switches active panel + nav routing; PanelClickMsg on tree
      moves cursor to the clicked row (and is a no-op past the last row)
- [ ] `make test` — must pass before Task 9

### Task 9: Status line context + i18n keys / translator threading

**Files:**
- Modify: `internal/core/ui/docstui/plugin.go` (StatusContext)
- Modify: `internal/core/ui/docstui/statusbar.go`
- Modify: translations source (`translations/en.yml` or the project's i18n source) +
  `KnownUIKeys` allowlist
- Modify: `internal/core/ui/docstui/plugin_test.go` / i18n test

- [ ] implement `StatusContext()` returning the middle-zone string: current path + 📊 N/M
      diagram progress + `[lang]` (port the old status-line content; drop brand/help — Frame
      owns those)
- [ ] add `tui.help.*` i18n keys for the Diagrams/Locales section labels and docs action
      descriptions; add them to the `KnownUIKeys` allowlist; thread `Translator`/`Locale`
      from the constructor into the registry/help (mirror cmdbrowser i18n threading)
- [ ] keep storage/hashing English; only display strings localize
- [ ] write tests: `StatusContext()` content (path/progress/lang); i18n keys resolve (en
      fallback) and appear in `tui.BuildHelp` output for the docs sections
- [ ] `make test` — must pass before Task 10

### Task 10: `docstui.Run` + rewire caller + delete old chrome

**Files:**
- Create: `internal/core/ui/docstui/run.go`
- Modify: `internal/cli/docs/docs.go`
- Modify/Delete: `internal/core/ui/docstui/view.go` (delete `renderTwoPanel` + chrome),
  `model.go` (delete old `Init`/`Update`/`View`/`quit` chrome paths), `keys.go` (delete old
  keymap), `statusbar.go` (trim to what StatusContext needs)
- Create: `internal/core/ui/docstui/run_test.go`

- [ ] define the `Options` struct explicitly, enumerating EVERY input the old
      `NewModel(ctx, roots, locale, translator, renderer, termWidth, termHeight, projectRoot,
      title, mermaidTheme)` constructor took PLUS the side-channel `MmdcNotice` field the old
      caller set (`internal/cli/docs/docs.go:149-153`; prepended to every loaded topic —
      `model.go:53-58,271`): `Roots, Renderer, ProjectRoot, MermaidTheme, Title, Locale,
      Translator, MmdcNotice`. **Exclude** `termWidth/termHeight` — the Frame owns sizing.
      Do NOT drop `MmdcNotice` (the "mmdc not installed" banner is real user-facing behavior)
- [ ] in `newBrowser`/the constructor, set `Theme = resolveMermaidTheme(opts.MermaidTheme)`
      (NOT `opts.MermaidTheme` raw) — preserve the `auto`→`HasDarkBackground` probe and the
      `light`/`dark` hard overrides (`model.go:137`, `diagram_inline.go` `resolveMermaidTheme`/
      `diagramTheme`); add a test seam so the `auto` probe is overridable for deterministic
      tests (Decision #11)
- [ ] implement `Run(ctx context.Context, opts Options) error` wrapping `tui.Run(newBrowser(
      ctx, opts), tui.RunOptions{Brand, Project, Mouse:true, Translator, Locale})`; map
      errors: cancel/kill → `widgets.ErrCancelled`, panic → wrapped, `ErrTooNarrow` → clean
      "terminal too small" error, `ErrNotTTY` → propagate (mirror `cmdbrowser/run.go` +
      `statustui` error mapping)
- [ ] **intentional behavior note**: the old caller used `tea.NewProgram(model,
      tea.WithContext(ctx))`; `tui.Run` does NOT thread ctx into the program. External
      parent-ctx cancellation no longer force-kills the program — confirm nothing depends on
      it (`ctrl+c`/`q` still terminate via bubbletea's own signal handling, and `Close`
      tears down watcher/prefetch on every exit path). Document this drop in the plan if any
      hidden dependency surfaces
- [ ] rewire `internal/cli/docs/docs.go:157` to call `docstui.Run(ctx, opts)` instead of
      building `tea.NewProgram`; remove the now-dead program construction + error handling
      there (it moves into `Run`)
- [ ] delete the old chrome: `renderTwoPanel`/`View`/title-bar/help-footer/old status-line
      rendering, the old `key.Binding` keymap (`keys.go`), and the old `Init`/`Update`/`quit`
      now superseded by the Plugin methods
- [ ] confirm `tui.Run` takes no ctx — the ctx lives only in `docstui.Run`/the plugin
- [ ] write tests: `Run` error mapping (non-TTY, too-narrow, cancel, panic) using the
      `tui`/`docstui` test seams; caller compiles and routes through `Run`; **assert the
      `MmdcNotice` banner still prepends to a loaded topic when set in `Options`**; assert
      `ctrl+c`/`q` terminate cleanly without relying on parent-ctx cancellation
- [ ] `make build` (embedded docs) + `make test` — must pass before Task 11

### Task 11: Golden frame tests + regression suite

**Files:**
- Create: `internal/core/ui/docstui/plugin_golden_test.go`
- Create/Modify: `internal/core/ui/docstui/testdata/` golden frames
- Modify: any adapted existing tests

- [ ] add golden frame tests rendering the docs plugin via `tui.RenderFrame` /
      `tui.BuildHelp` at width buckets **60/79/80/99/100** (odd+even), focused-tree and
      focused-viewport, filter-open, and help-modal-open — mirror
      `cmdbrowser/plugin_golden_test.go`
- [ ] add regression tests for: mouse routing into tree vs viewport hit-zones;
      async-message preservation (FileChangedMsg + ProgressMsg survive the Frame update
      loop); help-modal contents incl. Diagrams/Locales sections; capability fallback
      (non-TTY → `ErrNotTTY`, `<40` → `ErrTooNarrow` mapped)
- [ ] regenerate goldens deterministically: construct the plugin for golden frames with the
      watcher + prefetch **disabled/nil (or a frozen prefetch generation)** so the rendered
      frame cannot depend on async worker-pool timing; **force a concrete resolved mermaid
      theme** (override the `auto`→`HasDarkBackground` probe seam — Decision #11) and pin the
      lipgloss colour profile (mirror `cmdbrowser_test.go`'s NoTTY pinning) so glamour
      markdown + width wrapping render identically across environments
- [ ] **seed an already-loaded/rendered topic at the bucket width before snapshotting** —
      `tui.RenderFrame` applies one `WindowSizeMsg` and **discards the returned `tea.Cmd`
      (`testsupport.go:44`), so the async first-load never completes before the snapshot**. Use
      a test helper that synchronously sets the rendered content+indices at the bucket width
      (or manually run the load Cmd and feed the resulting `topicLoadedMsg`), so goldens never
      depend on async first-load; commit `testdata/`
- [ ] `make test` — must pass before Task 12

### Task 12: Update internals documentation

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `docs/internals/tui-keymap.md`

- [ ] add the `docstui` package contract to `packages.md`: the `core/ui` layering rule (the
      relocation keeps `core/docs` free of `core/ui` imports; `tui` consumed only from
      `core/ui/*` + `cli/`), the plugin's panels/focus/async notes, and the
      plugin-owned-context detail
- [ ] update `tui-keymap.md` for the docs keymap mapping (reload→`ctrl+r`, Diagrams/Locales
      sections, filter as inline capture, focus via `tab`, and the **intentional `h`/`l`
      toggle→directional** change for cross-surface unification)
- [ ] `make build` to re-sync embedded docs; `make test` (docs-subsystem tests) — must pass
- [ ] (no separate code tests — documentation task; the build/test run is the gate)

### Task 13: Verify acceptance criteria

- [ ] docs browser behaves identically: live-reload (watcher), prefetch diagram progress,
      filter, locale cycling, diagram open/copy, heading navigation, scroll — all preserved
- [ ] redesigned chrome present: bottom status line, `?`-modal help, project-styled borders,
      mouse (wheel + click)
- [ ] `cli/docs/docs.go` routes through `docstui.Run(ctx, opts)`; `core/docs` has no
      `core/ui` import (`grep -rn "core/ui" internal/core/docs/` is empty)
- [ ] run full suite: `make test` (and `make build` first for embedded docs)
- [ ] run `make lint` — clean
- [ ] verify golden tests cover all five width buckets and the help/filter/focus states

### Task 14: Finalize documentation + archive plan

- [ ] update `CLAUDE.md`/`AGENTS.md` Critical Patterns only if a new load-bearing contract
      emerged (e.g. the `docstui` plugin + core/ui layering note) — keep it tight
- [ ] confirm `docs/internals/packages.md` + `tui-keymap.md` reflect the final shape
- [ ] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed` first)

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only.*

**Manual verification:**
- Smoke-test in a real terminal: open `dwe docs`, confirm live-reload on editing a `docs/`
  file, prefetch diagram progress in the status line, diagram open (`o`) and copy (`y`),
  locale cycling (`L`/`e`), filter (`/`), heading navigation, and mouse wheel + click on tree
  rows.
- Visual confirmation of the redesigned chrome (bottom status line, `?`-modal, project-styled
  borders, focus highlight) at a few terminal widths incl. the narrow `<40` "too small" path.

**Follow-up (separate plans):**
- **Plan 2 (Stage 4.3)**: extract the generic `tui/tree` and refactor both the docs and
  cmdbrowser trees onto it — enabled by the mirrored tree surfaces this plan introduces.
- Stage 5 (statustui → v2 → Frame) remains independent and unaffected.

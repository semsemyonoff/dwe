# TUI Stage 3 — Pilot: Command Browser on the Frame

## Overview

Stage 3 of the Unified TUI Framework milestone
(`docs/plans/specs/2026-06-23-tui-framework-milestone.md`, § 5 row 3 + § 4). It
migrates the flagship interactive surface — the command browser
(`internal/core/ui/cmdbrowser/`) — onto the `tui` framework
(`internal/core/ui/tui/`) built in Stages 0–2:

- `Frame` (body panels + bottom status line) replaces the hand-rolled title
  bar + persistent help footer.
- The registry-generated `?`-modal help replaces the hand-written footer.
- Mouse (wheel + click + double-click), built once in `Frame` (Stage 2), is
  wired into the browser via the `panelLocal` seam.
- The browser becomes a `tui.Plugin` (a managed child model), not a standalone
  `tea.Model`.

It builds a **cmdbrowser-local tree** against the framework — the generic
`tui/tree` extraction is deferred to Stage 4 (per spec). Two framework
revisions, explicitly sanctioned by the spec ("Stage 3 may feed one revision
back into the interface"), land here:

1. A **unified input-capture** mechanism so the inline filter can take raw keys
   without a dimming overlay, and so a `CapturesInput` overlay (inspect) routes
   navigation keys to the plugin.
2. **Translator/locale wiring** into `Frame` (Stage 0 used a `NopTranslator`).

**Hard compatibility requirement:** the external contract — `Run(title, items,
opts) (Result, error)` and the `Result`/`Action`/`Mode` types — must not change.
Result/action semantics, the vars-browser edit mode (`ModeEdit`), and
force-param-form behaviour are preserved and regression-tested.

## Context (from discovery)

- **Framework (done, Stages 0–2)** — `internal/core/ui/tui/`:
  - `Plugin` interface (`plugin.go`): `Init/Close/Resize/Update/ViewPanel/
    Panels/StatusContext/Actions/HandleAction/PendingOverlay/Result`. PINNED,
    not frozen.
  - `Run(p Plugin, opts RunOptions) (any, error)` (`run.go`) — owns the
    TTY/size gate, alt-screen, mouse mode, teardown, and **already wraps
    `widgets.RunWithPromptHooks` internally** (so the cmdbrowser layer must NOT
    wrap again). On a normal `tea.Quit` it returns `plugin.Result(), nil`
    (`run.go:176`); only an interrupted/killed program maps to
    `widgets.ErrCancelled`. So cancellation-by-quit (exit with no selection)
    stays cmdbrowser's responsibility via the `ActionUnknown` → `ErrCancelled`
    guard in `Run`.
  - Registry (`registry.go`, `actions.go`): action→`Binding` with `Section`;
    built-ins `help`/`quit`/`focus.next`/`focus.prev` auto-registered;
    `RegisterStandard` for stdlib actions; help generated from `Sections()`.
  - Overlay (`overlay.go`): LIFO stack, centred compositing with base dimming,
    `Overlay.CapturesInput`. `routeWhileCapturing` (`frame.go:478`) already
    routes ALL keys to the plugin except `ctrl+c`/`esc` — but is **not yet
    called** from `Frame.Update` (the modal-open branch currently swallows
    everything except esc/?/q).
  - Focus (`focus.go`): Tab/Shift+Tab cycling + focused/unfocused borders.
  - Mouse (Stage 2): wheel coalescing (`coalesceWindow=16ms`, provisional),
    click zone classification (`hittest.go`), `double-click`→`select`.
    `panelLocal` (`frame.go:450`) translates absolute click → panel-local
    coords but is **not yet forwarded to the plugin** (Stage 3 seam).
  - Provisional timing constants flagged for Stage 3 tuning:
    `coalesceWindow` (16ms), `doubleClickWindow` (400ms) — `frame.go:61–70`.
  - `Frame` has `tr i18n.Translator` + `locale` fields but **no frameOption to
    set them** (Stage 0 hard-codes `NopTranslator` + fixed locale).
  - Test harness: `stub_test.go` (`stubPlugin`, `newTestFrame`, `key`,
    `stripANSI`, `assertGolden`, `UPDATE_GOLDEN=1`); golden buckets 60/79/80/99/100.
- **Current cmdbrowser (to migrate)** — `internal/core/ui/cmdbrowser/`:
  - `run.go` — `Run` + `Result/Action/Mode/Item/Options` types (PRESERVE).
    Current fallback threshold: `width < minTwoPanelWidth || height < 15` →
    `runFallback`. Double-wraps `RunWithPromptHooks` (to be dropped).
  - `model.go` (~786 LOC) — the root `tea.Model`: layout math
    (`leftWidth/rightWidth/bodyHeight/listHeight/...`), title bar, two-panel +
    single-panel rendering, help footer (`helpEntries`), `handleKey` chains,
    breadcrumb. **Deleted** at the end; its responsibilities move to `Frame`
    (geometry/chrome) and the new plugin.
  - `model_modes.go` (~225 LOC) — filter + inspect overlay state machines.
  - `tree.go` / `tree_render.go` / `filter.go` — cmdbrowser-local tree
    (`treeModel`, `treeNode`), filtering, auto-collapse. KEEP, but stop knowing
    layout/chrome (render into a passed `inner Region`).
  - `list_delegate.go` / `styles.go` / `palette.go` — list item delegate, badge
    styles, v1→v2 lipgloss palette bridge. Minimal change (v1 removal is an
    out-of-scope follow-up per spec).
  - `keymap.go` — key bindings (replaced by registry actions).
  - `fallback.go` — `runFallback` → huh selector. KEEP; threshold raised to <80.
  - Single-panel (60–79) logic lives **in `model.go`** (there is no
    `single_panel.go`; `single_panel_test.go` tests it). DROPPED (Variant A).
  - Callers: `internal/cli/command/list.go:~199`, `internal/cli/vars/browser.go:~55`.
- **i18n**: namespace `tui.help.title` / `tui.help.section.<name>` /
  `tui.help.action.<id>` reserved by Stage 0; no real keys yet. Built-in UI
  strings live in `internal/shared/i18n/translations/en.yml` (embedded; `ui:`
  block, stored without the `ui.` prefix per `store.go`), allowlisted in
  `internal/shared/i18n/known_keys.go` (`KnownUIKeys`, backs the
  `unknown_ui_key` validator). The built-in bundle ships **en only** — there is
  no built-in `ru.yml`; ru comes from a user workspace overlay or an injected
  `Store` in tests. There is no `workspace/i18n/` dir in this repo.

### Decisions (from brainstorm — do NOT re-litigate)

1. **Moderate refactor** — wrap as `Plugin` + cleanly split `model.go`; not a
   thin wrap, not a from-scratch rewrite.
2. **Narrow mode = Variant A** — always two panels at ≥80; below 80 **or**
   non-TTY → huh fallback (raise <60 threshold to <80); drop the in-TUI 60–79
   single-panel layout; no dynamic panel-count support in the framework;
   fallback owned by `cmdbrowser.Run` before `tui.Run`.
3. **Filter = plugin-owned inline** — query line inside the tree panel, live
   filtering underneath, no dimming overlay (needs unified input-capture).
4. **Inspect = `CapturesInput` overlay** — centred + dimmed; navigation keys
   route to the plugin's viewport (needs `routeWhileCapturing` wired in).
5. **Per-mode actions** — `e`/`y` registered only in `ModeRun`; absent from help
   and inert in `ModeEdit`/`ModeInspect` automatically.
6. **Status line middle** = breadcrumb of current group + `[--yes ON]` when
   skip-confirm is on (reactive; `StatusContext` called every render).
7. **Mouse** — single click = move cursor/set focus only (group does NOT
   toggle); double click = toggle group / run list item; wheel = scroll focused
   panel; help-hint click toggles help; clicks in/out of modal are swallowed.

## Development Approach

- **testing approach**: Regular (code first, then tests) — matches the existing
  cmdbrowser/tui style (table-driven + golden tests).
- **Cross-package test harness (Task 1 prerequisite):** the existing
  `newTestFrame`/`stubPlugin`/`buildHelpOverlay`/`assertGolden` helpers are
  package-private to `tui` (`*_test.go`) and unreachable from `cmdbrowser`, and
  `tui` cannot import `cmdbrowser` (cycle — `cmdbrowser` now imports `tui`). The
  `tui.Run` capability seams (`isTTY`/`size`/`input`/`output`/`runProgram`) are
  also unexported. So Task 1 adds a small EXPORTED harness in the `tui` package
  (a normal `.go` file, e.g. `testsupport.go`): `RenderFrame(p Plugin, opts
  RunOptions, w, h int) (string, error)` (note `RunOptions` is the existing
  exported struct — there is no `RunOption`; `frameOption` is unexported) and
  `BuildHelp(p Plugin, tr i18n.Translator, locale string, w, h int) (Overlay,
  error)` (`buildHelpOverlay` requires width/height, `help.go:56`). For the
  `cmdbrowser.Run` integration test, prefer a `cmdbrowser`-LOCAL `runTUI` seam
  (a package var wrapping `tui.Run`, swappable in tests) over exporting
  `tui.Run`'s capability seams — less production-API surface.
  Method-level plugin tests (ViewPanel/HandleAction/StatusContext/Update with
  the exported `PanelClickMsg`) need no Frame and are testable directly.
- complete each task fully before the next; small focused changes.
- **CRITICAL: every task includes new/updated tests** (success + error/edge);
  tests are required deliverables, listed as separate checklist items.
- **CRITICAL: all tests pass before starting the next task.**
- Build/test via **`make test`** / `make build` (NOT raw `go test`/`go build` —
  embedded docs are generated/gitignored); lint via `make lint`. For focused
  loops: `make embedded-docs` once, then
  `go test ./internal/core/ui/tui/... ./internal/core/ui/cmdbrowser/...`.
- Code/config comments in English. Respect CLAUDE.md layering: `core/ui` is a
  sink layer; `tui` depends only on `styles.Color*()` accessors + lipgloss v2.
- maintain backward compatibility (external `Run`/`Result` contract unchanged).

## Testing Strategy

- **unit tests**: required every task (see Development Approach).
- **golden tests**: full-frame plugin renders at width buckets **80 / 99 / 100**
  (odd/even) via the EXPORTED `tui.RenderFrame` harness (Task 1) — note <80 now
  routes to fallback, so buckets 60/79 become fallback-routing assertions, not
  frame goldens. Regenerate with `UPDATE_GOLDEN=1`. Strip ANSI for byte-stable
  goldens (cmdbrowser's own ANSI-strip helper, or one added to the harness).
- **no e2e harness** in this project — interactive TUI verification is golden +
  table-driven unit tests plus manual smoke (Post-Completion).
- Determinism via existing test hooks (`isTerminalFn`/`terminalSizeFn` seams,
  `runProgram` seam, injectable `frameClock`).

## Progress Tracking

- mark completed items with `[x]` immediately when done.
- add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- keep this plan in sync with actual work; update if scope changes.

## Solution Overview

The browser is reshaped into a `tui.Plugin`:

- **Two static panels** — `tree` (left) and `list` (right). Weights approximate
  today's split (`leftWidth ≈ 2·w/9`, so weights `{2, 7}`; verify against the
  current `leftWidth`/`rightWidth` at buckets 80/99/100 and adjust). `Frame`
  owns borders, focus highlight, Tab cycling, geometry, and the status line.
- **`ViewPanel`** renders each panel's body into the inner `Region` `Frame`
  computed. The tree and list render into their inner regions; no panel border
  drawn by the plugin.
- **Actions** registered per mode; `Frame` dispatches matched keys to
  `HandleAction` (and built-ins help/quit/focus itself).
- **Filter** is a plugin-internal capture mode: `CapturingInput()` returns true
  while filtering, `Frame` forwards raw keys, the query line renders in the tree
  panel and the tree filters live.
- **Inspect** is a `CapturesInput` overlay requested via `PendingOverlay()`;
  navigation scrolls the viewport, enter selects, esc closes.
- **Mouse**: `Frame` sets focus + emits a panel-local click message the plugin
  uses to move its cursor/selection; double-click → `select`; wheel → nav on the
  focused panel.
- **`cmdbrowser.Run`** keeps its signature: applies defaults, gates non-TTY and
  width<80 → `runFallback`, else calls `tui.Run(browser, RunOptions{...})` and
  type-asserts `any` → `Result`. No second `RunWithPromptHooks` wrap.

### Key design decisions & rationale

- **Two framework revisions, not one** — unified input-capture (filter +
  inspect) and translator wiring. Both are within the spec's "one revision back"
  allowance and are needed by the first real consumer. Recorded in
  `packages.md` and the Plugin contract notes.
- **`CapturingInput()` added to `Plugin`** — the minimal contract addition that
  lets a plugin take raw input without an overlay (filter). Inspect reuses the
  existing `Overlay.CapturesInput` + `routeWhileCapturing` (already written,
  just wired in).
- **Panel-local click delivery via a framework message** — `Frame` translates
  the click with `panelLocal` and forwards a typed `PanelClickMsg{Panel, X, Y}`
  through `plugin.Update`, so the plugin moves its cursor before any
  double-click `select` fires. Keeps mouse routing in `Frame` and avoids a new
  imperative Plugin method.
- **Fallback owned by `cmdbrowser.Run`, not `Frame`** — it must return the same
  `Result` type and reuse `runFallback`; `Frame`'s `ErrTooNarrow` stays a
  defensive secondary.

## Technical Details

- **New Plugin method**: `CapturingInput() bool`. Added to the interface;
  `stubPlugin` returns `false`; all plugins updated.
- **`Frame.handleKey` capture branch** (no overlay): when
  `f.plugin.CapturingInput()` is true, bypass the registry and forward the key
  to `plugin.Update`, reserving only `ctrl+c` as hard-quit (esc/enter handled by
  the plugin). Placed before the registry-match block.
- **`Frame.handleKey` modal-open branch**: when `Top().CapturesInput` is true,
  route via `routeWhileCapturing` (`captureSwallowToPlugin`→`plugin.Update`,
  `captureHardQuit`→`tea.Quit`, `captureClose`→pop overlay). Non-capturing
  overlays keep today's esc/?/q-only policy.
- **`PanelClickMsg`** (new exported framework message): `{Panel PanelID; X, Y int}`.
  Emitted by `handleClick` on a single `zonePanel` click (after `focus.Set`),
  forwarded to `plugin.Update`. Double-click continues via
  `MatchMouse("double-click")`→`HandleAction(ActionSelect)`.
- **`RunOptions` gains `Translator i18n.Translator` + `Locale string`**; `Run`
  maps them to new `withTranslator`/`withLocale` frameOptions; `newGeometry`
  unaffected.
- **Translator/locale carrier into `cmdbrowser.Run`** — the external signature
  `Run(title, items, opts) (Result, error)` is frozen, so the only non-breaking
  carrier is new fields on `cmdbrowser.Options`: `Translator i18n.Translator` +
  `Locale string`. `cmdbrowser` currently imports no `i18n`; `applyDefaults`/
  `DefaultOptions()` and existing test call sites pass none, so nil-safety is
  required (nil → `i18n.NopTranslator`/`TranslatorOrNop`). Both callers
  (`internal/cli/command/list.go:~192` already has `translator`/`locale` in
  scope; `internal/cli/vars/browser.go:~49`) populate the new fields.
- **Framework padding delta** — `Frame.renderBody` applies `Padding(0,
  hPadding)` (`hPadding=1`) inside the border, so `contentRegion` subtracts
  `borderSize+hPadding = 2` per side: plugin inner width = **outer − 4**, where
  the current cmdbrowser inner = frame − 2 (border only). Every width-bucket
  threshold (badges/counts) and the `leftWidth` min-18 clamp must be recomputed
  against framework INNER widths, not raw terminal width.
- **`browser` plugin struct** holds: `items`, `opts`, `tree *treeModel`,
  `list list.Model`, `delegate *cmdDelegate`, `focus`-independent panel state,
  `filter *filterState`, `inspect *inspectState`, `skipConfirm bool`,
  `result Result`, `cancelled bool`, current inner regions per panel,
  translator/locale (passed to `tui.RunOptions` for the help modal; the
  breadcrumb noun stays hardcoded English).
- **Active panel** — nav CANNOT be panel-agnostic: tree and list are distinct
  widgets with different movement, and Tab/Shift+Tab are framework built-ins
  handled entirely in `handleBuiltin` (`frame.go:326-329`) that never reach the
  plugin. So `Frame` MUST forward focus to the plugin: a new exported
  `FocusChangedMsg{Panel PanelID}` emitted to `plugin.Update` on every focus
  change (Tab/Shift+Tab AND click). The plugin's initial active panel matches
  the focus manager's index-0 panel (`tree`). This is mandatory (not optional)
  and lands in Task 2.
- **Result extraction**: `tui.Run` returns `any`; `cmdbrowser.Run` does
  `res, _ := out.(Result)`; `ActionUnknown`/`ErrCancelled` handled as today.

## What Goes Where

- **Implementation Steps** (`[ ]`): framework revisions, plugin build, Run
  rewiring, i18n keys, tests, `packages.md` — all in this repo.
- **Post-Completion** (no checkboxes): manual on-device mouse/scroll smoke
  (trackpad burst vs slow wheel), real-terminal narrow/non-TTY checks, timing
  constant feel-tuning sign-off.

## Implementation Steps

### Task 1: Frame revision — unified input-capture + translator wiring

**Files:**
- Modify: `internal/core/ui/tui/plugin.go`
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/run.go`
- Create: `internal/core/ui/tui/testsupport.go` (exported cross-package harness)
- Modify: `internal/core/ui/tui/stub_test.go`
- Modify: `internal/core/ui/tui/frame_test.go`

- [x] add exported cross-package harness in `testsupport.go` (normal `.go`,
      documented as for cross-package tests): `RenderFrame(p Plugin, opts
      RunOptions, w, h int) (string, error)` (builds a frame, applies a
      `WindowSizeMsg`, returns the composited `View` string) and
      `BuildHelp(p Plugin, tr i18n.Translator, locale string, w, h int)
      (Overlay, error)` (pass dims through to `buildHelpOverlay`). Use the
      existing exported `RunOptions` (not a nonexistent `RunOption`). (The
      `cmdbrowser`-local `runTUI` package var is created in Task 11 where
      `cmdbrowser.Run` is rewired and a test consumes it — adding it now would be
      an unused var flagged by lint, since `cmdbrowser` does not import `tui`
      until the plugin lands.)
- [x] add `CapturingInput() bool` to the `Plugin` interface (doc: true while the
      plugin takes raw input without an overlay; `Frame` suspends registry
      dispatch, reserving only ctrl+c).
- [x] in `Frame.handleKey`, add a capture branch BEFORE the registry match: if
      `f.overlay.Empty()` and `f.plugin.CapturingInput()`, forward the key to
      `plugin.Update` (drain overlay after); only `ctrl+c` → `tea.Quit`.
- [x] in `Frame.handleKey` modal-open branch, when `Top().CapturesInput` is
      true, route via `routeWhileCapturing` (swallow→`plugin.Update`,
      hardQuit→`tea.Quit`, close→`overlay.Pop()` + reset `lastClick`); keep the
      esc/?/q-only policy for non-capturing overlays.
- [x] add `withTranslator(i18n.Translator)` + `withLocale(string)` frameOptions
      and apply them in `newFrame`; add `Translator`/`Locale` to `RunOptions`
      and map them in `Run` (fall back to the existing `NopTranslator`/fixed
      locale when zero).
- [x] update EVERY concrete `Plugin` implementation to add `CapturingInput()`
      (configurable; default false) — not just `stubPlugin` but also
      `mousePlugin` and any other manual test plugins in `frame_test.go`
      (`frame.go`/`*_test.go` won't compile until all satisfy the new method).
- [x] write tests: capture branch forwards action-letter keys raw to the plugin
      (not dispatched) and reserves ctrl+c; `CapturesInput` overlay routes
      navigation keys (arrows/pgup/home) to the plugin and esc closes it;
      translator/locale flow into the help overlay.
- [x] run tests — must pass before next task.

### Task 2: Frame revision — panel-local click + focus delivery to plugin

**Files:**
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/hittest.go` (only if a helper is needed)
- Modify: `internal/core/ui/tui/stub_test.go`
- Modify: `internal/core/ui/tui/frame_test.go`

- [x] add exported `PanelClickMsg{Panel PanelID; X, Y int}`; in `handleClick`
      `zonePanel`, ALWAYS `focus.Set(id)`, but emit `PanelClickMsg` only when the
      click is inside `contentRegion(outer)` — `zonePanel` covers the panel's
      OUTER region incl. the border (`hittest.go:10`), and `panelLocal`
      (`frame.go:450`) subtracts the inner origin, so a border click yields
      negative/out-of-content coords. Look up the outer region via
      `panelRects()`/`layoutPanels` (`frame.go:637-649`; add a small helper if
      needed); compute local coords with `panelLocal`; forward through
      `plugin.Update` (then `drainOverlay`). When NOT a confirmed double-click.
- [x] add exported `FocusChangedMsg{Panel PanelID}` emitted to `plugin.Update` on
      EVERY focus change — Tab/Shift+Tab (`handleBuiltin` focus branches,
      `frame.go:326-329`) AND click `focus.Set`. Mandatory: the plugin cannot
      learn Tab-driven focus changes otherwise (built-ins never reach it).
- [x] ensure double-click still dispatches `MatchMouse("double-click")` →
      `HandleAction` AFTER the single-click cursor move (first click of the pair
      moves the cursor, second is detected as double).
- [x] extend `stubPlugin` to record `PanelClickMsg`/`FocusChangedMsg` and add
      `CapturingInput()` (from Task 1) if not already present. (Done via
      `mousePlugin` — the dedicated click/focus test plugin; `stubPlugin` already
      records all Update messages in `gotMsgs` and has `CapturingInput()`.)
- [x] write tests: content-area click forwards correct panel + local row to
      plugin and sets focus; a BORDER click sets focus but emits NO
      `PanelClickMsg` (no negative coords); Tab emits `FocusChangedMsg`;
      double-click in a cell moves cursor then dispatches select; click on
      help-hint toggles help; click in/out of modal swallowed. (Help-hint and
      modal-swallow already covered by `TestFrame_ClickRouting`.)
- [x] run tests — must pass before next task.

### Task 3: cmdbrowser plugin skeleton (`tui.Plugin` shell)

**Files:**
- Create: `internal/core/ui/cmdbrowser/plugin.go`
- Create: `internal/core/ui/cmdbrowser/plugin_test.go`

- [x] add `Translator i18n.Translator` + `Locale string` to `cmdbrowser.Options`
      (nil-safe: nil → `i18n.NopTranslator`/`TranslatorOrNop`); `DefaultOptions`
      leaves them zero.
- [x] define `type browser struct{...}` (fields per Technical Details) and a
      `newBrowser(title, items, opts) *browser` constructor (reads translator/
      locale from `opts`) that builds the `treeModel`, `list.Model`, and delegate
      (lifted from `newModel`).
- [x] implement `Panels()` → `[]Panel{{ID:"tree",Weight:2},{ID:"list",Weight:7}}`
      (verify weights vs current `leftWidth`/`rightWidth`), `Init()` (returns
      nil as today), `Close()` (nil — no async resources), `Result()` (returns
      `b.result`), and `CapturingInput()` (true iff `b.filter != nil`).
- [x] implement `Resize(body Region)` caching per-panel inner regions (replaces
      `applyLayout` geometry that `Frame` now owns).
- [x] implement `StatusContext()` returning breadcrumb of the focused tree group
      + `[--yes ON]` when `skipConfirm`. The breadcrumb noun
      (`command`/`commands` vs `var`/`vars`) stays HARDCODED English via the
      relocated `itemNoun` (it is hardcoded today — model.go:713 — not
      translated; keep it so for this pilot).
- [x] add a compile-time `var _ tui.Plugin = (*browser)(nil)` assertion.
- [x] write tests: `Panels()` shape/weights; `Result()`/`CapturingInput()`
      defaults; `StatusContext()` breadcrumb + skip-confirm indicator.
- [x] run tests — must pass before next task.

### Task 4: Tree panel — render into inner region via ViewPanel

**Files:**
- Modify: `internal/core/ui/cmdbrowser/tree.go`
- Modify: `internal/core/ui/cmdbrowser/tree_render.go`
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/tree_render_test.go`

- [x] change `treeModel` rendering to accept an inner `Region` (width/height)
      instead of reading layout from a `Model`; keep clipping
      (`treeTopIdx`/viewport) logic but drive it off the passed height.
      (`renderRegion`/`clipToViewport`/`ensureFocusVisible` + `topIdx` ported off
      `*Model`.)
- [x] implement `browser.ViewPanel("tree", inner)` calling the tree renderer
      (normal vs with-counts based on `inner.Width`). NOTE the framework padding
      delta (inner = outer − 4, not − 2): recompute the counts threshold against
      framework inner width rather than raw terminal width (current behaviour
      shows counts at ≥100 terminal cols). (`treeCountsMinWidth=18` — the inner
      tree width at terminal 99–100.)
- [x] move the filter-aware tree render path (`renderFilter`) behind the same
      entry so it is used when `b.filter != nil`.
- [x] write tests: tree renders within a given inner region (no overflow);
      counts shown/hidden by width; focused-node glyph correct.
- [x] run tests — must pass before next task.

### Task 5: List panel — render into inner region via ViewPanel

**Files:**
- Modify: `internal/core/ui/cmdbrowser/list_delegate.go`
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/list_delegate_test.go`

- [ ] implement `browser.ViewPanel("list", inner)`: size the `list.Model` to the
      inner region, set delegate badge/param-count visibility by `inner.Width`
      (badges shown at the ≥100-equivalent width; recompute the threshold against
      framework inner width — inner = outer − 4), render the list + breadcrumb
      header.
- [ ] keep `origIdx` mapping so the focused list item resolves to the original
      `items` index (preserves `Result.Idx` across filtering/reorder).
- [ ] sync the list contents to the focused tree group (port `refreshList`).
- [ ] write tests: list renders within inner region; badge/param visibility by
      width; `origIdx` round-trips to the original index.
- [ ] run tests — must pass before next task.

### Task 6: Actions registry — per-mode registration + HandleAction

**Files:**
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Create: `internal/core/ui/cmdbrowser/actions.go`
- Create: `internal/core/ui/cmdbrowser/actions_test.go`
- (NOTE: `keymap.go` is NOT deleted here — `model.go` AND the `*Model` methods
  in `model_modes.go` still reference `keys keymap`/`defaultKeymap()`/`m.keys`;
  it is deleted with them in Task 11.)

- [ ] implement `browser.Actions(reg)`: always register `nav.up/down/left/right`,
      `nav.top/bottom`, `nav.page-up/down`, `select` (enter), `filter` (`/`),
      `inspect` (`i`) via `RegisterStandard`/explicit bindings; in `ModeRun`
      ALSO register two new plugin actions `cmd.skip-confirm` (`y`) and
      `cmd.force-form` (`e`); register neither in `ModeEdit`/`ModeInspect`. Do
      NOT register Tab/?/q/esc (framework built-ins).
- [ ] set each `Binding.Section` to one of `Navigation` / `Panels` / `Actions` /
      `General` so the help modal groups them.
- [ ] implement `browser.HandleAction(a)`: nav/select/filter/inspect routed to
      the focused panel (tree vs list movement, expand/collapse on select for a
      group, run for a list item); `cmd.skip-confirm` toggles `skipConfirm`;
      `cmd.force-form` sets `ForceParamForm` + selects the current item. Define
      list-panel `nav.left`/`nav.right` (h/l): focus is now Tab-only (the old
      left-arrow "return to tree" in `updateRight`, model.go:281-284, is gone) —
      decide whether they are no-ops in the list or keep a left→tree affordance;
      record the chosen UX here.
- [ ] do NOT delete `keymap.go` yet — `model.go` AND `model_modes.go`'s `*Model`
      methods (all deleted in Task 11) still use
      `keys keymap`/`defaultKeymap()`/`m.keys`. The new registry actions live in
      `actions.go`; `keymap.go` is removed in Task 11 alongside them.
- [ ] write tests: action set differs by mode (ModeRun has e/y, ModeEdit does
      not); `HandleAction` select on a group toggles, on a list item runs;
      skip-confirm toggles and surfaces in `StatusContext`; force-form sets the
      flag + selects.
- [ ] run tests — must pass before next task.

### Task 7: Inline filter capture mode

**Files:**
- Modify: `internal/core/ui/cmdbrowser/filter.go`
- Modify: `internal/core/ui/cmdbrowser/model_modes.go`
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/filter_test.go`

- [ ] reparent the filter state machine onto `*browser` (the current
      `enterFilter`/`updateFilter` are `*Model` methods that reference `m.focus`/
      `focusFilter`; rewrite as `*browser` methods — do NOT edit them in place,
      since `Model` is deleted in Task 11).
- [ ] on `filter` action, enter capture mode: snapshot expanded/focus state,
      set `b.filter` (so `CapturingInput()` is true), render the query line
      inside the tree panel.
- [ ] handle captured keys in `browser.Update`: printable chars extend the
      query; backspace deletes; live `applyAutoCollapse` + match counts; `esc`
      restores the snapshot and clears `b.filter`; `enter` commits (keep
      expansion, focus nearest visible ancestor) and clears `b.filter`.
- [ ] ensure action-letters (`i`/`j`/`k`/`e`/`y`/`q`) are TYPED while capturing,
      not dispatched (relies on Task 1 capture branch).
- [ ] write tests: entering filter sets `CapturingInput()`; action-letters
      extend the query; live filter narrows the tree with correct match counts;
      esc restores prior state; enter commits.
- [ ] run tests — must pass before next task.

### Task 8: Inspect overlay (CapturesInput)

**Files:**
- Modify: `internal/core/ui/cmdbrowser/inspect.go`
- Modify: `internal/core/ui/cmdbrowser/model_modes.go`
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/model_modes_test.go`

- [ ] reparent the inspect state machine onto `*browser` (the current
      `openInspect`/`updateInspect` are `*Model` methods referencing `m.focus`/
      `focusInspect`; rewrite as `*browser` methods — `Model` is deleted in
      Task 11).
- [ ] on `inspect` action, build the inspect viewport content via
      `Item.Inspect(width)` and stash it so `PendingOverlay()` returns
      `Overlay{Content, Width, Height, CapturesInput:true}` (centred over the
      body, dimmed by `Frame`).
- [ ] handle captured keys while the inspect overlay is open (routed via Task 1):
      arrows/pgup/pgdn/home/end scroll the viewport; `enter` selects the item
      (set `Result{Idx, Action}` + `tea.Quit`); `esc` closes the overlay.
- [ ] track overlay-open state so `CapturingInput()` does not double-trigger
      (overlay capture is handled by the modal-open branch, inline filter by the
      no-overlay branch — keep them mutually exclusive).
- [ ] write tests: inspect requests a `CapturesInput` overlay; navigation
      scrolls the viewport; enter selects with the correct `Idx`/`Action`; esc
      closes and restores focus.
- [ ] run tests — must pass before next task.

### Task 9: Mouse wiring in the plugin

**Files:**
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/plugin_test.go`

- [ ] handle `tui.PanelClickMsg` in `browser.Update`: in the tree panel move the
      cursor to the clicked local row (no toggle); in the list panel move the
      selection to the clicked item; no run on single click.
- [ ] handle `tui.FocusChangedMsg` (if added in Task 2) to track the active
      panel for nav/scroll routing.
- [ ] ensure wheel (delivered as `nav.up`/`nav.down` via `HandleAction`) scrolls
      the focused panel (tree cursor/`treeTopIdx`; list viewport); double-click
      `select` toggles a group / runs a list item (already via `HandleAction`).
- [ ] confirm/tune `coalesceWindow` (16ms) and `doubleClickWindow` (400ms) — set
      final values in `frame.go` and note them; leave a Post-Completion note for
      on-device sign-off.
- [ ] write tests: `PanelClickMsg` moves tree cursor / list selection to the
      clicked row; wheel scrolls the focused panel; double-click select on group
      vs list item.
- [ ] run tests — must pass before next task.

### Task 10: i18n keys + translator threading

**Files:**
- Modify: `internal/shared/i18n/translations/en.yml` (the embedded built-in bundle)
- Modify: `internal/shared/i18n/known_keys.go` (`KnownUIKeys` allowlist)
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/cli/command/list.go`
- Modify: `internal/cli/vars/browser.go`
- Create/Modify: i18n test (e.g. `internal/core/ui/cmdbrowser/*_test.go`)

- [ ] add real keys under the `ui:` block of
      `internal/shared/i18n/translations/en.yml` (keys are `ui.*`, stored WITHOUT
      the `ui.` prefix per store.go) for `tui.help.title`,
      `tui.help.section.{navigation,panels,actions,general}`, and
      `tui.help.action.<id>` for every action the browser registers (incl.
      `cmd.skip-confirm`/`cmd.force-form`). English only — the built-in bundle
      ships en; there is no built-in `ru.yml`.
- [ ] extend `KnownUIKeys` in `internal/shared/i18n/known_keys.go` with the new
      `tui.help.*` keys (it must stay in sync with `translations/en.yml`; the
      `unknown_ui_key` validator keys on it, else user `tui.help.*` overlays warn).
- [ ] thread the translator + locale from the call sites into
      `cmdbrowser.Options{Translator, Locale}` (the CLI already resolves
      `rflags.I18n`/locale — reuse it); `cmdbrowser.Run` maps them onto
      `tui.RunOptions{Translator, Locale}`. Keep storage/hashing English per the
      localisation contract.
- [ ] the breadcrumb noun stays HARDCODED English (`itemNoun`) — it is not
      translated today; localizing it is out of scope for this pilot (no noun/
      status keys added to `en.yml`/`KnownUIKeys`). Only `tui.help.*` keys are
      added.
- [ ] write tests: help modal shows localized section/action strings; the ru
      case INJECTS a `Store` with a ru bundle (not a repo file) and asserts
      localized strings; ModeRun help includes e/y, ModeEdit help omits them.
- [ ] run `make build` (syncs/embeds) then tests — must pass before next task.

### Task 11: cmdbrowser.Run rewiring + delete model.go

**Files:**
- Modify: `internal/core/ui/cmdbrowser/run.go`
- Modify: `internal/core/ui/cmdbrowser/fallback.go`
- Modify: `internal/core/ui/cmdbrowser/model_modes.go` (delete dead `*Model` methods)
- Delete: `internal/core/ui/cmdbrowser/model.go`
- Delete: `internal/core/ui/cmdbrowser/keymap.go` (referenced by `model.go` AND
  the `*Model` methods in `model_modes.go` via `m.keys` — both go in this task)
- Modify: `internal/core/ui/cmdbrowser/cmdbrowser_test.go`

- [ ] BEFORE deleting `model.go`, relocate the package-level symbols it holds
      that survive: `actionForMode` (model.go:320), `breadcrumb`/`itemNoun`
      (model.go:698/713), and the `focus` type + `focusLeft/Right/Filter/Inspect`
      consts (model.go:19-26) — move onto/near `*browser` (or a small shared
      file). (`listItem` lives in list_delegate.go and `groupOf` in tree.go —
      those already survive.)
- [ ] rewrite `Run`: `applyDefaults`; empty-items guard; non-TTY → `runFallback`
      (defensive `ErrCancelled` only if fallback unreachable); read size; if
      `err != nil || width < 80 || height < 15` → `runFallback`; else
      `out, err := runTUI(newBrowser(...), tui.RunOptions{Brand, Project,
      Mouse:true, Translator, Locale})`, then `res, _ := out.(Result)`, where
      `runTUI` is the package-local seam (defaults to `tui.Run`, swapped in
      tests). (Note: `tui.Run` re-reads size and re-gates on its own
      `minWidth/minHeight=40/10` — harmless double-gate; the cmdbrowser
      <80/height<15 check is the real fallback boundary.)
- [ ] DROP the second `widgets.RunWithPromptHooks` wrapper (now owned by
      `tui.Run`); map `tui.Run`'s `widgets.ErrCancelled` straight through; keep
      the `ActionUnknown` → `ErrCancelled` guard via `res.Action`.
- [ ] raise the fallback threshold constant to 80 (rename
      `minTwoPanelWidth`/introduce a clear constant); the single-panel (60–79)
      layout code paths vanish with `model.go`.
- [ ] delete `model.go`, `keymap.go`, AND the now-dead `*Model` filter/inspect
      methods left in `model_modes.go` (reparented onto `*browser` in Tasks 7/8);
      update/remove tests tied to `Model` internals (`single_panel_test.go`,
      `panel_height_test.go`, `border_width_test.go`, `model_edit_test.go` — port
      still-relevant assertions to the plugin).
- [ ] confirm callers `internal/cli/command/list.go` and
      `internal/cli/vars/browser.go` compile unchanged against the same `Run`
      signature/`Result` type (only new `Options` fields are populated).
- [ ] write tests: width<80 and non-TTY route to `runFallback`; ≥80 drives the
      plugin via the `runTUI` seam (stub it to return a chosen `Result`/error)
      and `Run` returns the plugin's `Result` unchanged; `ActionUnknown`/cancel
      → `ErrCancelled`. (Full-frame rendering is covered by `tui.RenderFrame`
      goldens in Task 12.)
- [ ] run tests — must pass before next task.

### Task 12: Golden frames + regression suite

**Files:**
- Create: `internal/core/ui/cmdbrowser/plugin_golden_test.go`
- Create: `internal/core/ui/cmdbrowser/testdata/*.golden`
- Modify: existing cmdbrowser tests as needed

- [ ] add golden full-frame renders of the `browser` plugin via the exported
      `tui.RenderFrame` harness (Task 1) at width buckets 80 / 99 / 100
      (odd/even) for `ModeRun` and `ModeEdit`; strip ANSI for stability;
      regenerate with `UPDATE_GOLDEN=1`.
- [ ] add help-modal golden/content tests per mode via `tui.BuildHelp` (ModeRun
      shows e/y; ModeEdit does not).
- [ ] add regression tests for `Result` semantics across all modes
      (`Idx`/`Action`/`SkipConfirm`/`ForceParamForm`), `0≤Idx<len(items)`, and
      `ActionUnknown` → `ErrCancelled`.
- [ ] add a fallback-routing test asserting 60/79 + non-TTY do NOT enter the
      frame.
- [ ] run `make test` (full suite) — must pass before next task.

### Task 13: Update internals documentation

**Files:**
- Modify: `docs/internals/packages.md`

- [ ] record that `cmdbrowser` is now a `tui.Plugin` (per-surface plugin notes:
      two panels, inline filter capture, inspect overlay, mouse).
- [ ] document the two Frame revisions: unified input-capture
      (`Plugin.CapturingInput` + the no-overlay capture branch) and
      `CapturesInput` overlays routing navigation via `routeWhileCapturing`;
      `PanelClickMsg`/`FocusChangedMsg`; translator/locale `RunOptions`.
- [ ] mark the `tui.help.*` i18n namespace as now in use (keys added); note the
      Plugin interface revision (`CapturingInput`) as a deliberate contract
      change before freezing for Stages 4–5b.
- [ ] run `make build` (re-embeds docs) then `make test` — must pass before next
      task.

### Task 14: Verify acceptance criteria

- [ ] verify all Overview requirements: Frame + action keymap + `?`-modal help +
      bottom status line + mouse on the command browser; cmdbrowser-local tree
      retained (no generic extraction).
- [ ] verify preserved behaviour: result/action semantics, `ModeEdit`
      vars-browser, force-param-form, filter live-narrow, inspect scroll.
- [ ] verify Variant A: ≥80 two panels; <80/non-TTY fallback; no single-panel
      TUI mode remains.
- [ ] run full suite: `make test` (and `make test-race` if practical).
- [ ] run `make lint`; confirm no v1 lipgloss added to `tui`; `core/ui` layering
      intact.

### Task 15: [Final] Update documentation + archive plan

- [ ] update `README.md` / user docs only if user-facing behaviour changed
      (help is now `?`-modal; status line replaces the title bar) — keep
      `docs/reference`/`docs/guides` aligned if they screenshot the browser.
- [ ] update CLAUDE.md / `AGENTS.md` only if a new load-bearing pattern emerged
      (e.g. the `CapturingInput` contract) — prefer `packages.md` for detail.
- [ ] move this plan to `docs/plans/completed/`.

## Post-Completion
*Items requiring manual intervention or external systems — informational only.*

**Manual verification:**
- On-device mouse smoke: trackpad burst vs slow wheel scroll feel; single vs
  double click on tree groups and list items; help-hint click. Sign off the
  final `coalesceWindow` / `doubleClickWindow` values.
- Real-terminal narrow (<80) and non-TTY (piped) runs drop to the huh selector
  cleanly and return the same `Result`.
- Visual check of the redesigned frame: bottom status line (brand · project ·
  breadcrumb · `? help`), `?`-modal help in en + ru, focus borders on Tab.

**External system updates:**
- None — the external `cmdbrowser.Run` contract is unchanged, so consuming CLI
  commands need no updates beyond passing the translator/locale already
  available at the call sites.

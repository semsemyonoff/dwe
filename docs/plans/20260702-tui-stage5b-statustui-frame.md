# TUI Stage 5b: statustui onto the shared Frame (tui.Plugin)

## Overview

Migrate the status dashboard (`internal/core/ui/statustui`) onto the shared TUI framework
(`internal/core/ui/tui` `Frame`) as a `tui.Plugin`, joining `cmdbrowser` (Stage 3) and
`docstui` (Stage 4). This is **Stage 5b** of the Unified TUI Framework milestone
(`docs/plans/specs/2026-06-23-tui-framework-milestone.md`, § Stages row 5b).

After migration the **Frame** owns the outer border, the bottom status line (brand/project
left, help hint right), the `?`-invoked help modal, alt-screen, and mouse. The status
dashboard becomes a **single-panel** plugin whose body is a **tab strip (top content row) +
viewport (rest)**. Tabs, tab content, loading spinner, and the reload/`YOffset` machinery
stay owned by the plugin as body content/state.

**Depends on:** Stage 5a (`docs/plans/20260702-tui-stage5a-statustui-lipgloss-v2.md`) —
statustui must already be on lipgloss v2 before it hosts inside the v2 Frame.

**Problem it solves:** removes the last bespoke full-screen TUI chrome (title bar +
persistent help footer + hand-rolled status bar), unifies help into the framework `?`-modal,
adds mouse support (tab clicks + wheel scroll), and standardizes the status surface on the
same action-registry / overlay / focus model as the other two browsers (spec § Goals).

**Key benefit:** all three full-screen TUIs are now Frame plugins; the status surface
inherits mouse, modal help, and consistent chrome for free, and its ~400 LOC of duplicated
title/footer/status plumbing collapses into plugin body rendering + a small action set.

## Context (from discovery)

- **Files/components involved:**
  - `internal/core/ui/statustui/tui.go` — current bubbletea model (title bar, tab strip,
    divider, viewport, status bar, loading/too-small views, reload + `YOffset` logic).
  - `internal/core/ui/statustui/keys.go` — current `keyMap` (tab/shift+tab/1-5/r/?/q).
  - `internal/core/ui/statustui/run.go` — current `tea.NewProgram` launcher + `mapRunError`
    + `defer cancel()` context teardown.
  - `internal/core/ui/statustui/{load,run,tui}_test.go` — existing behavior/golden tests.
  - `internal/cli/status/status.go` — caller (`runStatusTUIFn = statustui.Run` seam,
    `shouldUseTUI`, `renderDefaultStatus` plain-text fallback).
- **Reference implementations (model closely on these):**
  - `internal/core/ui/docstui/plugin.go`, `run.go`, `actions.go`, `statusbar.go` — the
    Stage 4 migration; single closest analog (Plugin methods, `Actions`/`HandleAction`,
    `handlePanelClick`, `WheelMsg`, `StatusContext`, `Close`, run wiring, ErrTooNarrow map).
  - `docs/plans/completed/20260629-tui-stage4-docs-plugin-migration.md` — the Stage 4 plan
    (task shape, test matrix, i18n + docs updates).
  - `internal/core/ui/tui/plugin.go` — the `Plugin` interface + message types
    (`PanelClickMsg`, `WheelMsg`, `FocusChangedMsg`, `OverlayClosedMsg`, `Overlay`, `Panel`).
  - `internal/core/ui/tui/testsupport.go` — `RenderFrame` / `BuildHelp` golden harness.
  - `internal/core/ui/tui/actions.go` — `RegisterStandard`, `ActionReload` (`ctrl+r`).
- **Related patterns found:**
  - Frame built-ins: `?`=help, `q`/`ctrl+c`=quit, `esc`=close-overlay-only,
    `tab`/`shift+tab`=focus.next/prev. Built-ins are NOT registered by the plugin.
  - Plugins render each panel body as a **string** via `ViewPanel(id, inner)`; only the
    Frame returns a `tea.View`. Plugins never draw the border.
  - The Frame forwards ALL non-key messages (and unmatched keys) to `Plugin.Update`, so
    async messages survive — this is what lets the reload machinery stay verbatim.
  - i18n `tui.help.*` keys live in `internal/shared/i18n/translations/en.yml`; allowlisted
    in `internal/shared/i18n/known_keys.go`; validated by
    `internal/core/validate/i18n/unknown_ui_key.go`. `tui.help.action.reload` already exists.
- **Dependencies identified:**
  - Tab content comes from `internal/core/ui/render/` (lipgloss v1, OUT OF SCOPE) — its
    strings sit in the viewport unchanged.
  - `statustui` already lives under `core/ui`, so (unlike docs) NO package relocation is
    needed; importing `core/ui/tui` is within the sink layer.

## Development Approach

- **Testing approach:** Regular (code first, then tests) — but with a **hard preservation
  contract**: the reload + `YOffset` behavior and tab semantics are locked by porting the
  existing `tui_test.go` assertions onto the plugin, plus new golden-frame tests. Where a
  behavior is being deliberately preserved (reload/YOffset), write/port its test in the same
  task that touches it.
- Complete each task fully; all tests green before the next.
- **CRITICAL — single most important invariant:** the reload machinery
  (`tabsLoadedMsg` / `loadGen` / `reloadGen` / `reloadActive` / `reloadYOffset`) and
  `setActiveTab`'s `reloadGen`-reset + `GotoTop` must be preserved **verbatim** inside
  `Plugin.Update`. Do NOT refactor it during the migration.
- **CRITICAL — compile-clean coexistence (task ordering):** the package MUST compile and
  pass tests after EVERY task. The legacy `*model` is the **live launch path** until `run.go`
  is rewritten (Task 7): the old `run.go` does `tea.NewProgram(newModel(...))`, which needs
  `*model` to satisfy `tea.Model` via its existing `View()` + `Update(tea.Msg)(tea.Model,tea.Cmd)`,
  which in turn reference `keys.go` (`m.keys`, `defaultKeyMap`), `renderTitleBar`, and
  `renderStatusBar`. Therefore the new plugin is built **alongside** the intact legacy model
  (the plugin holds the model state as a **field**, e.g. `m *model` — NOT embedded, so the
  legacy `tea.Model` methods are not promoted onto the plugin and the two launch surfaces stay
  independent). **All legacy deletions** (`View()`, the legacy `Update`, the `var _ tea.Model`
  assertion, `keys.go`, `renderTitleBar`, the status-bar chrome) are **deferred to the Task 7
  cutover**, where `run.go` flips to launching the plugin and the now-unreferenced legacy path
  is removed in the same compile-clean step. Tasks 1–6 only ADD plugin code; they delete nothing
  from the legacy launch path.
- **CRITICAL:** every task includes new/updated tests as separate checklist items.
- Run `make test` before the stage is done; `make build` after doc edits (embedded docs).

## Testing Strategy

- **Unit tests:** required per task. Table-driven where it fits (actions dispatch, tab-click
  X→index mapping, wheel routing).
- **Golden frame tests:** at width buckets **60 / 79 / 80 / 99 / 100** (odd + even) using
  `tui.RenderFrame` / `tui.BuildHelp` (mirror `docstui/plugin_golden_test.go`). Cover the
  normal view, the help modal (Tabs section present, reload=`ctrl+r`, no `r`), and the
  loading view.
- **Preservation tests:** port the existing `tui_test.go` reload/`YOffset`/tab-switch/
  loading assertions onto the plugin path (drive `Plugin.Update` directly).
- **Caller test:** `status.go` narrow-terminal fallback — inject `tui.ErrTooNarrow` via the
  `runStatusTUIFn` seam and assert `renderDefaultStatus` fires.
- **e2e tests:** none (internal TUI).
- Deterministic rendering: add a `runStatusTUI` seam for the Frame launcher (mirror
  `docstui`'s `runDocsTUI`). Note the old `isTerminalFn` / `terminalSizeFn` seams are removed
  in Task 7 (`tui.Run` owns the TTY + size gates) — don't build new tests on them.
- Commands: `make embedded-docs` once, then `go test ./internal/core/ui/statustui/... ./internal/cli/status/...`;
  full gate `make test`.

## Progress Tracking

- Mark `[x]` immediately on completion.
- Add ➕ for newly discovered tasks; ⚠️ for blockers.
- If the migration feeds a revision back into the `Plugin` interface (spec § 7 allows one),
  record it here and note the `tui/plugin.go` change.

## Solution Overview

**Architecture.** `statustui` exposes a `*browser`-style plugin type implementing
`tui.Plugin`. It wraps the existing model state (tabs, active index, viewport, spinner,
reload generations) and delegates rendering to `ViewPanel`.

- **Panels:** exactly one panel (`Panels()` → `[]tui.Panel{{ID: panelMain, Weight: 1}}`).
- **Body layout inside the panel:** the plugin renders `tabStrip` on the top content row and
  the `viewport` below it (the old `renderTabStrip` + divider + `viewport.View()` minus the
  title bar and status bar, which the Frame now owns). Loading / reloading render as centered
  body content inside `ViewPanel`, NOT as a separate full-screen `View`.
- **Status line:** `StatusContext()` returns the middle segment — health indicator +
  "loaded X ago" + loading/reloading state (the current `renderStatusBar` `leftParts`). The
  Frame supplies brand/project (left) and `? help` (right). The old title bar and status bar
  chrome are DELETED.
- **Actions/keymap:** a plugin-local **Tabs** section — `left`/`h` prev-tab, `right`/`l`
  next-tab, `1`–`5` jump — plus stdlib `ActionReload` (`ctrl+r`). `tab`/`shift+tab` remain
  framework focus built-ins (harmless no-ops on a single panel).
- **Mouse:** `PanelClickMsg` with `Y==0` → tab-strip click → map `X` to tab index by
  measuring rendered label widths → `setActiveTab`. `WheelMsg` over the panel → viewport
  scroll by `Delta` notches.
- **Lifecycle:** `Close()` cancels the run context (stops in-flight `buildTabs` goroutines),
  replacing run.go's `defer cancel()`.
- **Fallback:** narrow terminal → `tui.Run` returns `tui.ErrTooNarrow`; `statustui.Run`
  returns it up; `status.go` catches it and calls `renderDefaultStatus` (plain text).

**Key design decisions & rationale:**
- **Single panel, tabs as body content** — tabs are stacked (strip + shared viewport), not
  side-by-side bordered regions, so they do not map to Frame panels. One panel keeps the
  Frame model honest and leaves tab-switching as plugin actions.
- **`tab` dropped for tab-switch (intentional)** — `tab`/`shift+tab` are framework focus
  built-ins; on a single-panel surface they no-op. Tab navigation moves to `left`/`right`
  (+`h`/`l`) and `1`–`5`. This is a deliberate muscle-memory change, mirroring Stage 4
  dropping docs' `r`→`ctrl+r`. Documented in `tui-keymap.md`.
- **Reload → `ctrl+r`** — unify with docs (stdlib `ActionReload`); drop `r`.
- **Reload/`YOffset` preserved verbatim** — the Frame forwards non-key messages to
  `Plugin.Update`, so the async load + offset-restore state machine needs no change; not
  refactoring it is the safest path and satisfies spec § Migration compatibility.
- **Narrow → plain text (not a blocking error)** — strictly better UX than today's
  "too small (60×16)" screen, and mirrors cmdbrowser's plain-selector fallback. Status
  already has `renderDefaultStatus`, so the fallback is free.

**Accepted minor wart:** the Frame help modal lists Navigation `focus.next`/`focus.prev`
even though they no-op on a single panel. Acceptable — not a blocker; noted in docs.

## Technical Details

- **New/renamed files** (mirror docstui's split; exact split at implementer's discretion):
  - `plugin.go` — the `Plugin` implementation (Panels, Resize, Update, ViewPanel,
    StatusContext, PendingOverlay→`{},false`, Result→`nil`, CapturingInput→`false`, Close).
  - `actions.go` — action IDs (`actionTabPrev`, `actionTabNext`, `actionTab1`..`actionTab5`),
    the `Tabs` section constant, `Actions(reg)` registration, `HandleAction`.
  - `mouse.go` (or in plugin.go) — `handlePanelClick`, tab-strip X→index mapping, `WheelMsg`.
  - `run.go` — rewritten (Task 7 cutover) to launch via `tui.Run`; `mapRunError` folded into
    ErrTooNarrow handling; add `runStatusTUI` seam.
  - `tui.go` — the `model` struct keeps its state fields + helpers (`setActiveTab`,
    `buildTabsCmd`, tab-strip render, the `leftParts` builder); the legacy launch surface
    (`View()`, the legacy `Update`, `renderTitleBar()`, the status-bar chrome, the `tea.Model`
    assertion) survives **until the Task 7 cutover**, then is deleted. Plugin methods (in
    `plugin.go`) operate on the held `*model` and reuse those helpers.
- **Deps gains i18n fields:** `statustui.Deps` currently has NO `Translator`/`Locale` fields
  (verified). Add `Translator i18n.Translator` and `Locale string` to `Deps`; the caller
  (`status.go`) populates them from `flags.I18n` (a `*i18n.Store`, which implements
  `i18n.Translator`) and `flags.Locale`. `run.go` threads them into `RunOptions`. A nil
  Translator falls back to `i18n.NopTranslator` and empty Locale to `"en"` (as `tui.Run` does).
- **Plugin methods → mapping:**
  | Method | Behavior |
  |---|---|
  | `Init() tea.Cmd` | `m.loadGen++; tea.Batch(spinner.Tick, buildTabsCmd(ctx, deps, loadGen))` (unchanged) |
  | `Close() error` | cancel the run context (stop in-flight `buildTabs`); return nil |
  | `Resize(body Region)` | cache inner body; set viewport width/height from `inner` (minus tab-strip + divider rows) |
  | `Update(msg) tea.Cmd` | keep `tabsLoadedMsg`/spinner/window handling **verbatim**; handle `PanelClickMsg`/`WheelMsg`/`FocusChangedMsg` |
  | `ViewPanel(id, inner)` | tab strip + divider + `viewport.View()`; centered spinner while loading |
  | `Panels()` | one panel, weight 1 |
  | `StatusContext()` | health indicator + "loaded X ago" + loading/reloading (old leftParts) |
  | `Actions(reg)` | `RegisterStandard(reg, ActionReload)` + Tabs section (prev/next/1-5) |
  | `HandleAction(a)` | switch on tab actions + reload; return `(cmd, true)` when handled |
  | `PendingOverlay()` | `tui.Overlay{}, false` (no overlays) |
  | `Result()` | `nil` (quit-only) |
  | `CapturingInput()` | `false` (no inline filter) |
- **Tab-strip click math:** reuse the same label-rendering used by `ViewPanel` so the click
  hit-zones exactly match what is drawn (measure each rendered tab segment width incl. the
  `▌ ▐` decorations and inter-tab gaps; walk `X` against cumulative widths → index). Ignore
  clicks past the last tab or on the leading pad. Clicks with `Y>0` (viewport body) have no
  per-row target (status rows are not selectable) — no-op.
- **Wheel:** `WheelMsg{Panel: panelMain, Delta}` → `viewport.ScrollBy(Delta * step)` (pick a
  small multi-line step consistent with docstui's viewport wheel step). Wheel never changes
  focus.
- **Actions registration order** (drives help section order after built-ins): register
  `ActionReload` (General) then the Tabs section, or vice-versa — pick the order that reads
  best in the help modal and lock it with the help golden.
- **run.go rewrite** (mirror `docstui/run.go`):
  ```
  var runStatusTUI = tui.Run
  func Run(ctx, deps) error {
      runCtx, cancel := context.WithCancel(ctx)
      plugin := newPlugin(runCtx, cancel, deps)   // Close() calls cancel
      _, err := runStatusTUI(plugin, tui.RunOptions{Brand: brand, Project: deps.ProjectName, Mouse: true, Translator: tr, Locale: loc})
      if errors.Is(err, tui.ErrTooNarrow) { return err }  // caller falls back
      return err  // ErrCancelled/panic passed through; nil on clean quit
  }
  ```
  - **Thread `deps.ProjectName` into `RunOptions.Project`** (field exists at `tui/run.go:50`;
    it feeds the status-line left segment). The old title bar carried
    `dwe · <project> · Status`; without this the project name is silently dropped.
  - Note: `tui.Run` does not thread `ctx` into the tea program (see docstui note), so
    `Close()` — deferred by the launch helper on every exit path — is what cancels `runCtx`.
  - `tui.Run` also owns the TTY gate: a non-TTY returns `tui.ErrNotTTY`. We do NOT special-case
    it in `run.go` — `shouldUseTUI` already gates TTY upstream in `status.go` (so the TUI is
    never launched on a non-TTY), matching docstui. Only `ErrTooNarrow` needs handling.
- **Brand / project string:** source the brand/project the same way docstui does (from
  `RunOptions.Brand` + the Frame's project segment); the old `render.LogoMarkPlain() + " dwe · " + project + " · Status"`
  title becomes the Frame's brand/project + a `Status`-flavored context if desired.
- **status.go fallback wiring** (around current `status.go:261`):
  ```
  if err := runStatusTUIFn(cmd.Context(), deps); err != nil {
      if errors.Is(err, tui.ErrTooNarrow) { return renderDefaultStatus(cmd, sc, noFlags) }
      return err
  }
  return nil
  ```
  `shouldUseTUI` (TTY / `--no-tui` / section flags / `TERM=dumb`) is unchanged; the new
  branch only adds the width fallback.

## What Goes Where

- **Implementation Steps** (`[ ]`): plugin implementation, actions, mouse, run wiring,
  caller fallback, i18n, tests, and internal-doc updates — all in this repo.
- **Post-Completion** (no checkboxes): manual on-terminal verification (tab clicks, wheel,
  `?` help, narrow-terminal fallback, dark/light theme).

## Implementation Steps

### Task 1: Scaffold the statustui plugin (Plugin interface skeleton)

**Files:**
- Modify: `internal/core/ui/statustui/tui.go`
- Create: `internal/core/ui/statustui/plugin.go`
- Create: `internal/core/ui/statustui/plugin_test.go`

- [x] define the plugin type holding the model state as a **field** (`m *model`, NOT embedded
      — see the coexistence rule; embedding would promote the legacy `tea.Model` methods).
      The legacy `*model` (View/legacy Update/keys.go) stays intact and launchable this whole task.
- [x] implement trivial methods: `Panels()` (one panel, weight 1), `Result()`→nil,
      `PendingOverlay()`→`{},false`, `CapturingInput()`→false
- [x] implement `Close()` to call the stored `cancel` (context teardown)
- [x] implement `Init()` preserving the current `loadGen++ / Batch(spinner.Tick, buildTabsCmd)`
- [x] **stub the remaining Plugin methods with zero-value bodies so the assertion compiles
      NOW**: `Resize`, `Update`→`nil`, `ViewPanel`→`""`, `StatusContext`→`""`,
      `Actions`→`nil`, `HandleAction`→`(nil,false)`. Tasks 2–6 fill in the real bodies.
      (The old `*model` methods `Update(tea.Msg)(tea.Model,tea.Cmd)` / `View() tea.View` do
      NOT satisfy `tui.Plugin`, so embedding alone will not compile — explicit stubs are required.)
- [x] add compile-time assertion `var _ tui.Plugin = (*plugin)(nil)`
- [x] write tests: Panels shape, Result/PendingOverlay/CapturingInput values, Close cancels ctx
- [x] run tests — must pass before next task

### Task 2: Body rendering — ViewPanel (tab strip + viewport + loading)

**Files:**
- Modify: `internal/core/ui/statustui/tui.go`
- Modify: `internal/core/ui/statustui/plugin.go`
- Modify: `internal/core/ui/statustui/plugin_test.go`

- [x] implement `Resize(body tui.Region)` on the plugin: cache inner body (the legacy model's
      own sizing path is left untouched — it is still the live launch path until Task 7).
      Do NOT call the legacy `viewportHeight` (it measures `renderStatusBar`, which the Frame
      replaces); compute the plugin's viewport size from `inner` minus tab-strip + divider rows,
      applying dimensions in `ViewPanel` via `viewport.SetDimensions(inner.Width, innerBodyHeight)`
      (docstui pattern: width recomputed on `WindowSizeMsg`, dims applied in `ViewPanel`)
- [x] implement `ViewPanel(id, inner)` on the plugin: size the viewport to the inner body, then
      render tab strip (top row) + divider + `viewport.View()`; centered spinner while
      `loading`/`reloading` (no separate full-screen view). Reuse the model's existing tab-strip
      render helper (do NOT delete it — the legacy `View()` still calls it until Task 7)
- [x] **do NOT delete any legacy code this task** — `View()`, `renderTitleBar()`, the status-bar
      chrome, `keys.go`, and the `var _ tea.Model` assertion all stay (they keep the legacy launch
      path compiling); their removal is the Task 7 cutover
- [x] write golden tests via `tui.RenderFrame` at buckets 60/79/80/99/100 (odd+even):
      normal view + loading view (drives the plugin through the Frame, not the legacy model)
- [x] run tests — must pass before next task

### Task 3: StatusContext (middle status-line segment)

**Files:**
- Modify: `internal/core/ui/statustui/plugin.go`
- Modify: `internal/core/ui/statustui/plugin_test.go`

- [ ] implement `StatusContext()` returning the old `renderStatusBar` leftParts: health
      indicator + "loaded X ago", plus "loading…"/"reloading…" states
- [ ] ensure it is reactive (recomputed each render from current state, like docstui)
- [ ] write tests: loading state, reloading state, loaded-with-timestamp, empty/nil-cfg
- [ ] run tests — must pass before next task

### Task 4: Actions & HandleAction (Tabs section + reload)

**Files:**
- Create: `internal/core/ui/statustui/actions.go`
- Modify: `internal/core/ui/statustui/plugin.go`
- Create: `internal/core/ui/statustui/actions_test.go`

- [ ] define action IDs: `actionTabPrev`, `actionTabNext`, `actionTab1`..`actionTab5`; the
      `Tabs` section label constant
- [ ] implement `Actions(reg)`: `RegisterStandard(reg, tui.ActionReload)` + register Tabs:
      prev=`["left","h"]`, next=`["right","l"]`, jumps 1..5=`["1"]`..`["5"]`
- [ ] implement `HandleAction(a)` on the plugin (operating on the held `m *model`): prev/next
      wrap via `m.setActiveTab((active±1+n)%n)`; jumps via `m.setActiveTab(k)`; reload triggers
      the existing reload path (loadGen++/reloadGen/reloadActive/reloadYOffset/buildTabsCmd);
      return `(cmd, true)` when handled
- [ ] confirm `setActiveTab` still resets `reloadGen` and calls `GotoTop` (verbatim)
- [ ] **do NOT delete `keys.go` this task** — the legacy `model.Update` still references
      `m.keys`/`defaultKeyMap` and is the live launch path until Task 7. `keys.go` is deleted at
      the Task 7 cutover, together with the legacy `Update`
- [ ] write table-driven tests: each action → expected active index / reload trigger;
      wrap-around at ends; jump out-of-range ignored
- [ ] write a help-modal golden (`tui.BuildHelp`) asserting Tabs section present,
      reload=`ctrl+r`, no `r` binding
- [ ] run tests — must pass before next task

### Task 5: Preserve reload + YOffset through the Frame Update loop

**Files:**
- Modify: `internal/core/ui/statustui/plugin.go`
- Modify: `internal/core/ui/statustui/plugin_test.go`

- [ ] implement `Update(msg)`: keep the reload/`YOffset` state machine **verbatim** — the
      `tabsLoadedMsg` handling (stale-gen drop, tabs assign, loadedAt/healthIndicator, YOffset
      restore when `reloadGen==gen && reloadActive==active`, else `GotoTop`) and `spinner.TickMsg`
- [ ] the **plugin's** `WindowSizeMsg` handling must NOT copy the legacy sizing (old
      `tui.go:141–149`), which called `viewportHeight()`→`lipgloss.Height(renderStatusBar())`
      and sized from raw `m.width-2` (ignores Frame border/panel chrome). Instead store the
      Frame-supplied width and let `ViewPanel`/`Resize` own viewport dimensions (docstui pattern:
      recompute width on `WindowSizeMsg`, apply `SetDimensions` in `ViewPanel`). The legacy
      `model.Update`'s own `WindowSizeMsg` block is left intact (live launch path until Task 7).
      "Verbatim" applies ONLY to the reload/`YOffset`/`setActiveTab` logic — never to sizing.
- [ ] ensure unmatched messages still delegate to `viewport.Update` for scroll
- [ ] port the existing `tui_test.go` reload/YOffset assertions to drive `Plugin.Update`
      directly (reload preserves YOffset on the same tab; switching tabs resets to top)
- [ ] port loading→loaded transition + stale-generation-drop tests
- [ ] run tests — must pass before next task

### Task 6: Mouse — tab clicks & wheel scroll

**Files:**
- Modify: `internal/core/ui/statustui/plugin.go` (or new `mouse.go`)
- Modify: `internal/core/ui/statustui/plugin_test.go`

- [ ] handle `tui.PanelClickMsg`: `Y==0` → map `X` to tab index by measuring rendered
      tab-label segment widths (reuse the tab-strip render so hit-zones match); `setActiveTab`
- [ ] ignore clicks past the last tab / on leading pad; `Y>0` clicks are no-ops
- [ ] handle `tui.WheelMsg{Panel: panelMain, Delta}` → `viewport.ScrollBy(Delta*step)`;
      wheel never changes focus
- [ ] handle `tui.FocusChangedMsg` if needed (single panel → effectively inert; keep minimal)
- [ ] write table-driven tests: click X across each tab boundary → correct index; click past
      end → no change; wheel Delta → expected YOffset delta
- [ ] run tests — must pass before next task

### Task 7: Cutover — rewrite run.go to launch the plugin + delete the legacy launch path

This is the single compile-clean cutover point (see the coexistence rule). The launch flips
from the legacy `*model` to the plugin, and every legacy-only symbol is deleted in the SAME
task so the package compiles at the end.

**Files:**
- Modify: `internal/core/ui/statustui/run.go`
- Modify: `internal/core/ui/statustui/tui.go`
- Delete: `internal/core/ui/statustui/keys.go`
- Modify: `internal/core/ui/statustui/run_test.go`

- [ ] add `Translator i18n.Translator` and `Locale string` fields to `statustui.Deps`
      (`tui.go`) — the source for `RunOptions` (they do not exist today)
- [ ] add `var runStatusTUI = tui.Run` seam
- [ ] rewrite `Run(ctx, deps)`: create `runCtx, cancel`; build the plugin (holds the model +
      `cancel`, `Close`→cancel); resolve `tr := deps.Translator` (nil → `i18n.NopTranslator{}`);
      call `runStatusTUI(plugin, tui.RunOptions{Brand, Project: deps.ProjectName, Mouse: true, Translator: tr, Locale: deps.Locale})`
- [ ] return `tui.ErrTooNarrow` up unchanged; pass through cancel/panic; nil on clean quit
      (replace the old `mapRunError`; `widgets.RunWithPromptHooks` is inside `tui.Run` — do NOT
      wrap again)
- [ ] remove the now-dead `isTerminalFn` / `terminalSizeFn` seams from `run.go` (`tui.Run` owns
      the TTY + size gates; these are no longer called). Update any test referencing them.
- [ ] **now delete the legacy launch path** (nothing references it after the run.go flip):
      `model.View()`, the legacy `model.Update(tea.Msg)(tea.Model,tea.Cmd)`, `renderTitleBar()`,
      the status-bar chrome, the `var _ tea.Model = (*model)(nil)` assertion (`tui.go:80`), and
      `keys.go` (`keyMap`/`defaultKeyMap`). Keep the `model` struct fields + the helpers the
      plugin reuses (`setActiveTab`, `buildTabsCmd`, tab-strip render, `leftParts`)
- [ ] `go build ./internal/core/ui/statustui/...` — must compile with no legacy references
- [ ] write tests via the `runStatusTUI` seam: ErrTooNarrow passthrough; clean-quit→nil;
      Close cancels the context on every exit path
- [ ] run tests — must pass before next task

### Task 8: Caller fallback in cli/status (narrow → plain text)

**Files:**
- Modify: `internal/cli/status/status.go`
- Modify: `internal/cli/status/status_test.go`

- [ ] populate the new `Deps` i18n fields where `deps` is built (≈status.go:249):
      `Translator: flags.I18n` (a `*i18n.Store`, which implements `i18n.Translator`),
      `Locale: flags.Locale` (`flags` is in scope in the RunE closure)
- [ ] wrap the `runStatusTUIFn(cmd.Context(), deps)` call (≈status.go:261): on
      `errors.Is(err, tui.ErrTooNarrow)` return `renderDefaultStatus(cmd, sc, noFlags)`
- [ ] leave `shouldUseTUI` (TTY / `--no-tui` / sections / `TERM=dumb`) unchanged
- [ ] write a test: inject `tui.ErrTooNarrow` via `runStatusTUIFn` seam → assert plain-text
      `renderDefaultStatus` output is produced (not an error)
- [ ] write/confirm a test: non-error TUI return → no plain fallback; other errors propagate
- [ ] run tests — must pass before next task

### Task 9: i18n keys for the new status actions

**Files:**
- Modify: `internal/shared/i18n/translations/en.yml`
- Modify: `internal/shared/i18n/known_keys.go`
- Modify (verify): `internal/shared/i18n/coverage_test.go`

- [ ] add `tui.help.section.tabs` and **enumerate every action key** the registry derives
      (help key = `tui.help.action.<actionID>`, per `help.go:73,77`): `tui.help.action.tab.prev`,
      `tui.help.action.tab.next`, and the **five** jump keys `tui.help.action.tab.1` …
      `tui.help.action.tab.5` (match the exact action-ID *string values* from Task 4, not the
      Go constant names). Reuse the existing `tui.help.action.reload` (do NOT re-add it).
- [ ] give each jump key a static English description (e.g. "Services", "Deploy", "Topology",
      "Git", "Daemons") — the registry `Binding.Desc` is fixed, unlike the old per-tab keymap
- [ ] add every new key to `KnownUIKeys` in `known_keys.go` — `coverage_test.go` enforces a
      **strict bidirectional** en.yml ⟺ `KnownUIKeys` match (missing OR extra key fails), so
      the two lists must be exactly in sync
- [ ] confirm `internal/core/validate/i18n/unknown_ui_key.go` needs no change (allowlist is
      `known_keys.go`); keep storage/hashing English
- [ ] run `go test ./internal/shared/i18n/...` (coverage/known-key tests) — must pass
- [ ] run tests — must pass before next task

### Task 10: Golden frame + help + integration test sweep

**Files:**
- Modify: `internal/core/ui/statustui/plugin_test.go` (or a dedicated `plugin_golden_test.go`)

- [ ] add/confirm golden frame tests at 60/79/80/99/100 (odd+even): normal view, loading
      view, help modal (Tabs + `ctrl+r`, no `r`) — mirror `docstui/plugin_golden_test.go`
- [ ] add an async-preservation test: `tabsLoadedMsg` delivered through the Frame Update
      loop updates the plugin (drive via `tui.RenderFrame` + injected msg, or Frame test seam)
- [ ] remove obsolete assertions in old `tui_test.go` that referenced the deleted title bar /
      status bar / too-small full-screen view; keep behavior assertions ported in Task 5
- [ ] run `go test ./internal/core/ui/statustui/...` — all green

### Task 11: Update internal docs

**Files:**
- Modify: `docs/internals/tui-keymap.md`
- Modify: `docs/internals/packages.md`

- [ ] `tui-keymap.md` § 1.3 "Status dashboard": replace the old (`tab`/`shift+tab`/`1-5`/`r`)
      table with `left`/`h` prev, `right`/`l` next, `1`–`5` jump, `ctrl+r` reload; note
      `tab`/`shift+tab` are framework focus no-ops on the single-panel surface; document the
      intentional removal of `tab`=next-tab (mirror the docs `r`→`ctrl+r` note); note the
      accepted help-modal wart (focus.next/prev listed but inert)
- [ ] `packages.md`: fold statustui into the existing `tui.Plugin` framework write-up —
      statustui joins cmdbrowser/docstui as a Frame consumer; single-panel + tabs-as-body-
      content model; narrow→`renderDefaultStatus` fallback; reload/YOffset preserved
- [ ] run `make build` so `internal/core/docs/embedded/` copies are regenerated (not stale)
- [ ] run `go test ./internal/core/docs/...` (docs subsystem) — must pass

### Task 12: Verify acceptance criteria
- [ ] all `Plugin` methods implemented; `var _ tui.Plugin` assertion compiles
- [ ] reload + `YOffset` preserved (Task 5 tests green); tab semantics preserved
- [ ] mouse: tab clicks + wheel work (Task 6 tests green)
- [ ] narrow terminal → plain text fallback (Task 8 test green)
- [ ] help modal shows Tabs + `ctrl+r`, no `r` (Task 4/10 goldens green)
- [ ] no v1 lipgloss reintroduced; `render/` untouched; `liveui` untouched
- [ ] run full suite: `make test`
- [ ] run `make lint` — clean

### Task 13: [Final] Wrap up
- [ ] update CLAUDE.md only if a genuinely new load-bearing pattern emerged (else leave the
      `tui.Plugin` bullet to cover it — statustui is now listed as a Frame consumer)
- [ ] confirm `docs/internals/*` embedded copies are current (`make build` ran in Task 11)
- [ ] move both Stage 5 plans to `docs/plans/completed/` (5a already there if landed first)

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification (real terminal):**
- `dwe status` on a normal terminal: `?` opens the help modal (Tabs section, `ctrl+r`);
  `left`/`right`/`h`/`l` and `1`–`5` switch tabs; `r` no longer switches/reloads.
- Mouse: click a tab in the strip → switches; wheel over the body → scrolls the viewport;
  clicking a tab does not require focus (single panel).
- Reload (`ctrl+r`) on a scrolled tab preserves scroll offset; switching tabs resets to top.
- Narrow terminal (< framework minimum): prints the plain-text status instead of a blocking
  "too small" screen (same output as `--no-tui`).
- Dark and light terminal backgrounds render correct theme colors.
- Non-TTY (`dwe status | cat`): plain text (unchanged, gated by `shouldUseTUI`).

**Follow-up (out of scope, tracked by the milestone):**
- `internal/core/ui/render/`'s lipgloss v1 tables remain (spec § Charm-stack scope).
- Stage 6 (forms unification) and Stage 7 (in-TUI form overlay) are independent of this stage.
- If Stage 5b revealed a `Plugin` interface gap, the spec (§ 7) permits one revision — record
  it and confirm cmdbrowser/docstui still satisfy the revised contract.

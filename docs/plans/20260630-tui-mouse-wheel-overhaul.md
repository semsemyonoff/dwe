# TUI Mouse-Wheel Overhaul — Immediate, Pointer-Routed, Overlay-Aware Scrolling

## Overview

Rework mouse-wheel handling in the `internal/core/ui/tui` framework and its plugins
(`cmdbrowser`, `docstui`) so wheel scrolling feels direct and predictable. Three
defects are fixed together:

1. **Latency / runaway scroll.** Today a wheel event does not scroll immediately: it
   adds a delta to an accumulator and arms a one-shot `tea.Tick` (`coalesceWindow =
   16ms`). The accumulated steps are dispatched only when the tick fires
   (`wheelFlushMsg`). This adds a per-scroll delay, produces step-jumps, and — because a
   trailing burst of OS-generated wheel events keeps re-arming the tick — makes the
   scroll feel impossible to stop. **Fix:** dispatch the scroll synchronously on every
   wheel event and delete the accumulator/tick machinery. The bubbletea renderer already
   coalesces repaints per frame, so a burst of N events becomes N cheap updates and one
   repaint — without the artificial delay.

2. **No wheel scrolling inside overlays.** While any overlay is open the wheel is
   swallowed (`frame.go` `handleMouse` returns early on `!f.overlay.Empty()`), so the
   inspect modal (command description / vars-usage) cannot be scrolled with the wheel —
   only with the keyboard. **Fix:** when the top overlay captures input, forward the
   wheel event to the plugin so its embedded viewport scrolls.

3. **Wheel routed by focus, not by pointer.** A wheel turn is mapped to a generic
   `ActionNavUp/Down` and dispatched to the **focused** panel, so you cannot scroll the
   panel the pointer is over without first focusing it, and the per-step amount is the
   same for every panel type. **Fix:** route the wheel to the panel **under the pointer**
   (hit-zone), leave focus unchanged, and let each plugin choose a per-panel scroll
   amount (a viewport scrolls several lines per notch; a tree/list moves the cursor one
   row per notch). This is delivered via a new framework message `WheelMsg{Panel, Delta}`
   routed through the existing `Plugin.Update` — no new method is added to the `Plugin`
   interface (it stays frozen; only a new `tea.Msg` type is introduced, exactly like the
   existing `PanelClickMsg` / `FocusChangedMsg` / `OverlayClosedMsg`).

## Context (from discovery)

- **Framework mouse layer** lives entirely in `internal/core/ui/tui/frame.go`:
  - `handleMouse` (`frame.go:476`) — wheel accumulates into `wheelAccum`/`wheelArmed`
    and arms `tea.Tick(coalesceWindow)`; overlay-open and `CapturingInput()` both
    swallow the wheel (`frame.go:483`).
  - `case wheelFlushMsg:` in `Update` (`frame.go:256`) — dispatches `abs(wheelAccum)`
    Nav steps via `registry.MatchMouse("wheel-up"/"wheel-down")` → `plugin.HandleAction`.
  - Wheel-state resets are scattered across `drainOverlay`, `handleBuiltin(ActionHelp)`,
    and the filter-transition branch of `handleKey`.
  - `handleClick` already classifies hits with `classifyHit` and forwards
    `PanelClickMsg`/`FocusChangedMsg`; double-click still uses `registry.MatchMouse(
    "double-click")` (kept).
- **Exported framework messages** (`plugin.go`): `PanelClickMsg{Panel,X,Y}`,
  `FocusChangedMsg{Panel}`, `OverlayClosedMsg{}`. `WheelMsg` joins them.
- **Registry mouse bindings** (`actions.go` `standardBindings`, `registry.go`
  `MatchMouse`): `wheel-up`→NavUp, `wheel-down`→NavDown, `double-click`→Select. After
  this change wheel no longer dispatches through `MatchMouse`; only `double-click` does.
- **cmdbrowser** (`internal/core/ui/cmdbrowser/`): panels are `panelTree` + `panelList`
  (both cursor-based; no viewport panel). The inspect modal is a `CapturesInput` overlay
  wrapping a `bubbles/v2/viewport` (`inspect.go`); `updateInspect` (`plugin.go:580`)
  currently takes only `tea.KeyPressMsg` and re-marks `inspectPending` after a scroll so
  the Frame refreshes the snapshot (`refreshCapturingOverlay`). Tree cursor moves via
  `tree.eng.MoveUp/MoveDown` + `afterTreeMove`.
- **docstui** (`internal/core/ui/docstui/`): panels are `panelTree` + `panelViewport`.
  `navVertical` (`actions.go:158`) routes by `b.active`: viewport → `Viewport.ScrollUp/
  ScrollDown` (one line), tree → `Tree.MoveUp/MoveDown` + `afterTreeMove` (loads the
  topic). `ViewportWidget` (`viewport.go`) exposes `ScrollUp/ScrollDown` (one line each).
- **bubbles/v2 viewport** (`viewport.go@v2.1.0`) handles `tea.MouseWheelMsg` natively
  when `MouseWheelEnabled` (default true), scrolling `MouseWheelDelta` (default 3) lines
  — so forwarding the raw wheel message to the inspect viewport scrolls it with no extra
  code.
- **Generic tree engine** (`internal/core/ui/tui/tree/tree.go`): `MoveUp()`,
  `MoveDown()`, plus the private `moveBy(delta)`; `EnsureFocusVisible(height)`.
- **Docs/contracts to keep in sync**: `docs/internals/tui-keymap.md` §2.3/§6 (mouse
  vocabulary + frame-owned behaviors), `docs/internals/packages.md` (`tui` section), and
  the `tui.Plugin` bullet in `AGENTS.md`/`CLAUDE.md` (lists the exported framework
  messages and the frozen-interface rule).

## Development Approach

- **testing approach**: **Regular (code-first)** — consistent with TUI Stages 2–4. The
  rendered output does not change (mouse is an input layer); golden frames stay
  byte-stable. Implement each component, then add tests in the same task.
- complete each task fully before moving to the next
- make small, focused changes; the package must build after every task
- **every task includes new/updated tests** (success + edge) as SEPARATE checklist items
- **all tests must pass before starting the next task**
- update this plan when scope changes during implementation
- backward compatibility: keyboard navigation (`j/k`, arrows, page/home/end) is
  unchanged — only wheel routing changes. The `Plugin` interface is unchanged (new
  message only), so no plugin breaks structurally; both plugins (`cmdbrowser`,
  `docstui`) gain a `WheelMsg` case but ignoring it would simply restore "wheel does
  nothing" rather than crash. `statustui` is NOT a `tui.Plugin` consumer (it runs its own
  `tea.NewProgram`; Stage 5b not yet done) — it is out of scope and needs no change.

## Testing Strategy

- **unit tests**: required every task.
  - Frame: a dedicated mouse test plugin records the messages forwarded to `Update`
    (extend the existing `mousePlugin` in `frame_test.go`). Assert: a wheel over a panel
    forwards exactly one `WheelMsg{Panel,Delta}` immediately (no tick, no `HandleAction`);
    wheel over help-hint / blank space is swallowed; horizontal wheel is ignored; a wheel
    turn does **not** change focus; with a capturing overlay open the raw
    `tea.MouseWheelMsg` is forwarded and the overlay snapshot is refreshed; with a
    non-capturing overlay (help) or an active inline filter the wheel is swallowed.
  - cmdbrowser: `WheelMsg{panelTree}` moves the tree cursor and runs `afterTreeMove`;
    `WheelMsg{panelList}` moves the list selection; both leave `b.active` unchanged; a
    `tea.MouseWheelMsg` while `b.inspect != nil` scrolls the inspect viewport and re-marks
    `inspectPending`.
  - docstui: `WheelMsg{panelViewport}` scrolls the viewport by the multi-line step;
    `WheelMsg{panelTree}` moves the tree cursor and returns the topic-load Cmd; neither
    changes `b.active`.
- **golden**: existing `frame_*.golden`, `help_default.golden`, and the cmdbrowser/docstui
  goldens are NOT regenerated — rendering is unchanged. A byte-stability assertion is the
  regression guard.
- **determinism**: no `tea.Tick` and no wall-clock are introduced for wheel; tests inject
  synthetic `tea.MouseWheelMsg` / `WheelMsg` directly. (The double-click path keeps its
  injected `frameClock`, untouched here.)
- **no e2e**: these packages have no UI e2e harness.
- test commands: focused `go test ./internal/core/ui/tui/... ./internal/core/ui/cmdbrowser/...
  ./internal/core/ui/docstui/...`; full `make test`; `make lint`; `make build` after the
  `docs/internals/*.md` edits so the embedded copy + content hashes regenerate.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep this plan in sync with actual work done

## Solution Overview

The wheel path is rebuilt around three principles — **immediate**, **pointer-routed**,
**overlay-aware** — with the per-panel scroll policy pushed into the plugins.

1. **Immediate dispatch (no tick).** `handleMouse` acts on each `tea.MouseWheelMsg`
   synchronously and returns the resulting Cmd. The accumulator (`wheelAccum`,
   `wheelArmed`), the `wheelFlushMsg` type, the `case wheelFlushMsg` in `Update`, the
   `coalesceWindow` constant, and every wheel-state reset are deleted. The wheel no longer
   passes through `registry.MatchMouse` (the `wheel-up`/`wheel-down` `Binding.Mouse`
   entries are removed); `double-click` stays registry-bound.

2. **Pointer routing via `WheelMsg`.** With no overlay and no inline-filter capture,
   `handleMouse` classifies the pointer with `classifyHit(...)`. A `zonePanel` hit
   forwards `WheelMsg{Panel: id, Delta: ±1}` to `plugin.Update` — **without** touching
   `focus`. `zoneHelpHint`, `zoneNone`, and horizontal wheel are swallowed. The plugin
   owns the scroll amount per panel.

3. **Overlay-aware routing.** When the top overlay `CapturesInput`, the raw
   `tea.MouseWheelMsg` is forwarded to `plugin.Update` and `refreshCapturingOverlay` swaps
   in the re-rendered snapshot (mirroring the captured-key path). A non-capturing overlay
   (help) and an active inline filter swallow the wheel, matching the keyboard policy.

4. **Per-plugin scroll policy.**
   - cmdbrowser: `WheelMsg{panelTree}` → one tree-cursor step + `afterTreeMove`;
     `WheelMsg{panelList}` → one list-selection step. Inspect overlay scroll is handled by
     forwarding the raw wheel message into the embedded viewport (native 3-line delta).
   - docstui: `WheelMsg{panelViewport}` → scroll the viewport `wheelViewportStep` lines;
     `WheelMsg{panelTree}` → one tree-cursor step + topic load. Focus is never changed by
     the wheel.

### Key design decisions (encode, do not re-litigate)

- **`Plugin` interface stays frozen.** `WheelMsg` is a new `tea.Msg` routed through the
  existing `Update(tea.Msg)`, not a new interface method — same shape as `PanelClickMsg`.
- **No coalescing layer.** Per-frame repaint coalescing is the bubbletea renderer's job;
  the framework dispatches one scroll per wheel event. This is the direct cause-and-effect
  model that fixes the "can't stop / laggy" feel.
- **Scroll amount lives in the plugin, not the frame.** The frame is content-agnostic; it
  cannot know a panel is a viewport vs a tree. `WheelMsg.Delta` is the signed notch count
  (±1 per event); the plugin multiplies by its per-panel step.
- **Focus is independent of the wheel.** Wheeling a panel scrolls it but does not focus
  it; clicking still focuses (unchanged).
- **Overlay wheel reuses the capturing-overlay snapshot refresh** already built for keys —
  no new overlay plumbing.

## Technical Details

- **`WheelMsg`** (new, `internal/core/ui/tui/plugin.go`):
  ```go
  // WheelMsg is delivered to the plugin when the mouse wheel turns over one of its
  // panels. Panel is the panel under the pointer (NOT the focused panel); Delta is
  // -1 for an upward notch and +1 for a downward notch. The plugin decides how far to
  // scroll based on the panel type. A wheel turn never changes focus.
  type WheelMsg struct {
      Panel PanelID
      Delta int
  }
  ```
- **`Frame.handleMouse` (`tea.MouseWheelMsg` arm), new shape:**
  1. If `top, ok := f.overlay.Top(); ok`:
     - `top.CapturesInput` → `cmd := f.plugin.Update(m); f.refreshCapturingOverlay(); return f, cmd`.
     - else (non-capturing overlay) → `return f, nil`.
  2. If `f.plugin.CapturingInput()` → `return f, nil`.
  3. `delta`: `MouseWheelUp` → `-1`, `MouseWheelDown` → `+1`, default (horizontal) →
     `return f, nil`.
  4. `zone, id := classifyHit(f.geo, f.panelRects(), f.helpHintRegion(), nil, m.X, m.Y)`;
     if `zone != zonePanel` → `return f, nil`.
  5. `cmd := f.plugin.Update(WheelMsg{Panel: id, Delta: delta}); f.drainOverlay(); return f, cmd`.
- **Deletions in `frame.go`:** `wheelAccum`, `wheelArmed` fields; `wheelFlushMsg` type;
  `case wheelFlushMsg` in `Update`; `coalesceWindow` const; the three wheel-state reset
  blocks in `drainOverlay`, `handleBuiltin(ActionHelp)`, and `handleKey`'s filter
  transition (leave the `lastClick` resets — those belong to double-click).
- **`actions.go` / `registry.go`:** remove the `wheel-up`/`wheel-down` `Binding.Mouse`
  values from `standardBindings`; keep `double-click`. `MatchMouse` stays (used by
  double-click). Update the table doc comment.
- **cmdbrowser (`plugin.go`):**
  - `Update`: add `case tui.WheelMsg:` → `b.handleWheel(m); return nil` and
    `case tea.MouseWheelMsg:` (only meaningful while `b.inspect != nil`) →
    `b.inspect.vp, cmd = b.inspect.vp.Update(m); b.inspectPending = true; return cmd`.
  - `handleWheel(msg tui.WheelMsg)`: `if b.filter != nil { return }`; `panelTree` →
    `b.tree.eng.MoveUp()/MoveDown()` per `Delta` sign then `b.afterTreeMove()`;
    `panelList` → move list selection one row per notch (`b.list.CursorUp()/CursorDown()`
    or `Select` with clamping). Do not touch `b.active`.
- **docstui (`plugin.go`/`actions.go`/`viewport.go`):**
  - `Update`: add `case tui.WheelMsg:` → `return b.handleWheel(m)`.
  - `handleWheel(msg tui.WheelMsg) tea.Cmd`: `if b.CapturingInput() { return nil }`;
    `panelViewport` → scroll `wheelViewportStep` lines in the wheel direction (add
    `ViewportWidget.ScrollBy(n int)` or loop `ScrollUp/ScrollDown`), then
    `b.syncActiveDiagram()`, return nil; `panelTree` → `b.Tree.MoveUp()/MoveDown()` then
    `return b.afterTreeMove()`. Do not touch `b.active`.
  - `wheelViewportStep = 3` constant (documented; matches the bubbles viewport default).
  - Audit docstui for any other `CapturesInput` overlay; if a scrollable one exists, its
    wheel arrives as the forwarded raw `tea.MouseWheelMsg` (handle in `Update`). Help is
    non-capturing (swallowed) — no change needed.

## What Goes Where

- **Implementation Steps** (`[ ]`): all `tui` + `cmdbrowser` + `docstui` code and tests,
  the `docs/internals/*.md` + `AGENTS.md` updates, and the embedded-docs regeneration.
- **Post-Completion** (no checkboxes): real-terminal feel sign-off (trackpad burst vs
  slow notch wheel; overlay scroll; pointer-vs-focus) on a real surface — there is no demo
  binary, so this is eyeballed by running `dwe commands` / `dwe docs`.

## Implementation Steps

### Task 1: Add `WheelMsg`; remove the wheel accumulator/tick; immediate pointer routing

**Files:**
- Modify: `internal/core/ui/tui/plugin.go`
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/actions.go`
- Modify: `internal/core/ui/tui/registry.go`
- Modify: `internal/core/ui/tui/frame_test.go`
- Modify: `internal/core/ui/tui/actions_test.go`
- Modify: `internal/core/ui/tui/registry_test.go`
- Modify: `internal/core/ui/cmdbrowser/actions_test.go` (delete the removed-wheel-binding assertions in the same task)

- [x] add the exported `WheelMsg{Panel PanelID; Delta int}` type to `plugin.go` with a doc
  comment (pointer-not-focus; Delta sign convention; plugin owns the scroll amount)
- [x] rewrite the `tea.MouseWheelMsg` arm of `handleMouse`: no-overlay + non-capturing
  path classifies via `classifyHit` and forwards `WheelMsg{Panel,Delta}` immediately on a
  `zonePanel` hit; help-hint/blank/horizontal are swallowed; **focus is not changed**
- [x] delete the accumulator machinery: `wheelAccum`/`wheelArmed` fields, `wheelFlushMsg`
  type, `case wheelFlushMsg` in `Update`, `coalesceWindow` const, and the wheel-state
  resets in `drainOverlay` / `handleBuiltin(ActionHelp)` / `handleKey` filter transition
  (keep the `lastClick` double-click resets)
- [x] remove the `wheel-up`/`wheel-down` `Binding.Mouse` defaults from `standardBindings`
  (keep `double-click`); update the table doc comment; leave `MatchMouse` in place for
  double-click
- [x] **narrow the registry mouse vocabulary to match the new dispatch contract** (the
  registry self-documents that any accepted-but-undispatched event is "dead state" —
  `registry.go:158-164`): drop `mouseWheelUp`/`mouseWheelDown` so `validMouseEvent`
  (`registry.go:208-213`) accepts only `double-click`, update the `Register` rejection
  message (`registry.go:167`) and the locked-vocabulary doc comments (`registry.go:158-167`,
  `:199-206`). After this, `wheel-up`/`wheel-down` are no longer registrable `Binding.Mouse`
  values — consistent with the frame no longer dispatching them
- [x] sweep stale wheel comments in `frame.go` so they describe immediate dispatch, not
  the deleted accumulator/tick: the `Update` doc ("wheel events accumulate for burst
  coalescing", ~`frame.go:223-225`), the `handleMouse` doc (~`:471-475`), and the
  `drainOverlay` doc ("clears any pending wheel accumulation", ~`:446-450`); also the
  `mousePlugin` comment in `frame_test.go` (~`:996`)
- [x] **delete the now-uncompilable wheel-coalescing tests in the same edit** (the package
  must build within this task): remove `TestFrame_WheelCoalescing` plus its `flush()`
  helper and the real-tick assertion (~`frame_test.go:1117/1213`) — all reference the
  deleted `wheelAccum`/`wheelArmed`/`wheelFlushMsg`
- [x] extend the `mousePlugin` test helper to record messages forwarded to `Update`
  (e.g. capture `WheelMsg` values) alongside the existing `HandleAction` counts
- [x] write tests: wheel over a panel forwards exactly one `WheelMsg{Panel,Delta}`
  immediately with the correct sign, returns no tick Cmd, and calls no `HandleAction`;
  wheel over help-hint and over blank space are swallowed; horizontal wheel is ignored; a
  wheel turn leaves `focus.Active()` unchanged
- [x] **rewrite/invert the registry tests for the narrowed vocabulary** (these break the
  build/suite otherwise): in `actions_test.go` invert `TestStandardBindings_MouseDefaults`
  so NavUp/NavDown expect an empty `Mouse` (fold into the "no mouse default" case); in
  `registry_test.go` drop the two wheel cases from `TestRegistry_MatchMouse_StdlibDefaults`,
  switch `TestRegistry_MouseCollisionRejected` (`:287`) from `Mouse:"wheel-up"` to
  `"double-click"`, and move `wheel-up`/`wheel-down` into the **rejected** set of the
  invalid-vocabulary test (`:311-322`) so they now assert a vocabulary error
- [x] **also fix the cmdbrowser registry test in THIS task** (it asserts the removed wheel
  defaults and would otherwise fail the full suite between tasks — keep the per-task "all
  tests pass" gate honest): delete the `MatchMouse("wheel-up")==ActionNavUp` /
  `"wheel-down"==ActionNavDown` assertions in `internal/core/ui/cmdbrowser/actions_test.go`
  (~`:50-55`)
- [x] run the affected suites — `go test ./internal/core/ui/tui/... ./internal/core/ui/cmdbrowser/...`
  — must pass before next task

### Task 2: Overlay-aware wheel routing in `Frame`

**Files:**
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/frame_test.go`

- [x] in `handleMouse`'s `tea.MouseWheelMsg` arm, add the overlay branch BEFORE hit-test:
  a `CapturesInput` top overlay → forward the raw `tea.MouseWheelMsg` to `plugin.Update`
  and call `refreshCapturingOverlay`; a non-capturing top overlay → swallow; an active
  inline filter (`plugin.CapturingInput()`, no overlay) → swallow
- [x] give the `mousePlugin` test helper a capturing-overlay mode (a `PendingOverlay` with
  `CapturesInput: true`) and a recorder for raw `tea.MouseWheelMsg` forwards
- [x] write tests: with a capturing overlay open, a `tea.MouseWheelMsg` is forwarded to the
  plugin and `refreshCapturingOverlay` runs (top overlay replaced, stack depth unchanged);
  with a non-capturing overlay (help) the wheel is swallowed; with the inline filter active
  the wheel is swallowed
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 3: cmdbrowser — pointer wheel for tree/list + inspect-overlay wheel scroll

**Files:**
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/actions.go` (stale doc comment only)
- Modify: `internal/core/ui/cmdbrowser/plugin_test.go`
- Modify: `internal/core/ui/cmdbrowser/inspect_test.go`
- (the `cmdbrowser/actions_test.go` wheel-assertion deletion is done in Task 1, with the
  registry change that causes it)

- [x] add `case tui.WheelMsg:` to `browser.Update` → `b.handleWheel(m); return nil`, and
  `case tea.MouseWheelMsg:` → scroll the inspect viewport (`b.inspect.vp.Update(m)`,
  re-mark `b.inspectPending`) when `b.inspect != nil`, else ignore. (`viewport.Update`
  always returns a nil Cmd — discarding it is fine.)
- [x] implement `handleWheel(msg tui.WheelMsg)`: drop while `b.filter != nil` (a
  belt-and-suspenders guard — the Frame already swallows the wheel during capture);
  `panelTree` → one tree-cursor step per notch (`tree.eng.MoveUp/MoveDown`) +
  `afterTreeMove`; `panelList` → one list-selection step per notch
  (`b.list.CursorUp/CursorDown`); never mutate `b.active`
- [x] sweep the stale comment at `plugin.go:226-228` ("Wheel scroll is delivered as
  nav.up/nav.down through HandleAction") to describe the `WheelMsg` path; fix the stale
  wheel note in `actions.go` (~`:29-34`) that ties NavUp/NavDown's mouse default to wheel
- [x] **rewrite `TestBrowser_WheelScrollsFocusedPanel`** (~`plugin_test.go:343`) to inject
  `tui.WheelMsg{Panel,Delta}` instead of driving `ActionNavDown` via `HandleAction`, and
  to assert focus (`b.active`) is unchanged
- [x] write tests: `WheelMsg{panelTree}` up/down moves the tree cursor and triggers the
  topic/refresh side effects, `b.active` unchanged; `WheelMsg{panelList}` moves the list
  selection, `b.active` unchanged; a `WheelMsg` while the filter is active is a no-op
- [x] write tests: a `tea.MouseWheelMsg` routed to `updateInspect`/`Update` scrolls the
  inspect viewport (YOffset advances) and sets `inspectPending`
- [x] run `go test ./internal/core/ui/cmdbrowser/...` — must pass before next task

### Task 4: docstui — pointer wheel with per-panel step (viewport multi-line, tree cursor)

**Files:**
- Modify: `internal/core/ui/docstui/plugin.go`
- Modify: `internal/core/ui/docstui/actions.go`
- Modify: `internal/core/ui/docstui/viewport.go`
- Modify: `internal/core/ui/docstui/plugin_test.go`
- Modify: `internal/core/ui/docstui/scroll_test.go`

- [x] add `ViewportWidget.ScrollBy(n int)` (or equivalent) scrolling `n` lines in either
  direction with clamping; add the `wheelViewportStep = 3` constant (documented)
- [x] add `case tui.WheelMsg:` to `browser.Update` → `return b.handleWheel(m)`; implement
  `handleWheel`: drop while `CapturingInput()` (belt-and-suspenders — the Frame already
  swallows the wheel during capture); `panelViewport` → scroll `wheelViewportStep` lines +
  `syncActiveDiagram`; `panelTree` → one tree-cursor step + `afterTreeMove` (topic load);
  never mutate `b.active`
- [x] sweep the stale comment in `handlePanelClick` (~`plugin.go:556-559`, "wheel scroll
  arrives as ActionNavUp/Down via HandleAction") to describe the `WheelMsg` path
- [x] audit docstui for any scrollable `CapturesInput` overlay; if present, handle the
  forwarded raw `tea.MouseWheelMsg` in `Update` (help is non-capturing — no change)
- [x] write tests: `WheelMsg{panelViewport}` scrolls the viewport by `wheelViewportStep`
  lines (YOffset delta), `b.active` unchanged; `WheelMsg{panelTree}` moves the tree cursor
  and returns the topic-load Cmd, `b.active` unchanged; a `WheelMsg` while filtering is a
  no-op
- [x] run `go test ./internal/core/ui/docstui/...` — must pass before next task

### Task 5: Update reference docs — keymap, packages.md, AGENTS.md

**Files:**
- Modify: `docs/internals/tui-keymap.md`
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md` (`CLAUDE.md` is a symlink — do not edit it directly)

- [x] `tui-keymap.md` §2.3/§6: describe the new wheel mechanics — immediate dispatch (no
  coalescing/tick), pointer-routed via `WheelMsg{Panel,Delta}` (not focus), per-panel
  scroll amount owned by the plugin, and wheel-in-capturing-overlay forwarding; note that
  `double-click` remains the only registry-bound mouse action
- [x] `packages.md` `internal/core/ui/tui/` section: record `WheelMsg` as a new exported
  framework message (joining `PanelClickMsg`/`FocusChangedMsg`/`OverlayClosedMsg`), the
  removal of the wheel accumulator/tick, the hit-zone wheel routing, and the
  capturing-overlay wheel forward; keep the `core/ui` layering + `docstui` relocation notes
- [x] update the `tui.Plugin` bullet in `AGENTS.md`: add `WheelMsg{Panel,Delta}` to the
  exported-framework-message list and state the wheel is pointer-routed and focus-neutral;
  reaffirm the `Plugin` interface is unchanged (new message only, frozen interface intact)
- [x] no code test (documentation); correctness verified by `make build` in Task 6

### Task 6: Verify acceptance criteria

- [ ] verify every Overview fix is implemented and exercised by tests: immediate dispatch
  (no tick), overlay wheel scroll, pointer routing via `WheelMsg`, per-panel step,
  focus-neutral wheel
- [ ] verify the packages build: `go build ./internal/core/ui/...`
- [ ] verify existing goldens are byte-stable (rendering unchanged): `frame_*.golden`,
  `help_default.golden`, and the cmdbrowser/docstui goldens pass without regeneration
- [ ] verify the `Plugin` interface is unchanged (no new method); no unintended new
  importers of `tui`
- [ ] run focused suites: `go test ./internal/core/ui/tui/... ./internal/core/ui/cmdbrowser/...
  ./internal/core/ui/docstui/...`
- [ ] run full suite: `make test`
- [ ] run `make lint` — clean (gofmt/goimports/golangci-lint)
- [ ] run `make build` so the embedded copy of the edited `docs/internals/*.md` regenerates
  (commit the regenerated `internal/core/docs/content_hashes_gen.go`)

### Task 7: [Final] Finalize documentation + archive plan

- [ ] confirm `tui-keymap.md` §6, the `packages.md` `tui` section, and the `AGENTS.md`
  `tui.Plugin` bullet read coherently together (no stale "accumulator/tick" or
  "wheel→MatchMouse" wording remaining)
- [ ] update `AGENTS.md` Critical Patterns only if a new load-bearing contract emerged
  (likely just the `WheelMsg`/pointer-routing note added in Task 5)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification** (deferred / on-device):
- Run `dwe commands` and `dwe docs` in a real terminal and confirm: a wheel notch scrolls
  immediately with no lag and stops as soon as input stops; the inspect modal (command
  description / vars usage) scrolls with the wheel; wheeling a panel scrolls it without
  moving focus; a viewport scrolls several lines per notch while a tree/list moves one row.
- Tune `wheelViewportStep` if the per-notch viewport amount feels off on a real device.

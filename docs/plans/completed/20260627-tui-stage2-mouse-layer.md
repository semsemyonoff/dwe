# TUI Stage 2 — Mouse Layer in `Frame`

## Overview

Light up the mouse layer the Stage 0 skeleton stubbed and the Stage 1 design locked.
Implement, **once in `internal/core/ui/tui/Frame`** so every plugin inherits it: the
mouse-enable mode + per-program opt-in, per-frame wheel coalescing, click hit-testing,
double-click → select, and overlay click routing. Per the milestone spec
(`docs/plans/specs/2026-06-23-tui-framework-milestone.md`, Stage 2 row) the vocabulary
(`wheel-up`/`wheel-down`/`click`/`double-click`) and the registry-bound defaults
(`wheel→Nav`, `double-click→Select`) plus the frame-owned behaviors (click-on-panel→focus,
click-on-help-hint→Help, click-outside-modal→swallow) are already locked in
`docs/internals/tui-keymap.md` §6 — this stage **wires** them.

This stage is **stub-only / code-thin**, the same shape as Stage 1: there is NO production
consumer (the command browser migrates in Stage 3), so the mouse layer is exercised entirely
through the `tui` package's own tests against the existing stub plugin. NONE of the three
existing TUIs are touched.

Problem it solves: today there is zero `tea.MouseMsg` handling anywhere in the codebase. The
framework lit up the alt-screen and registry in Stage 0/1 but left mouse inert
(`frame.go` ignores `tea.MouseMsg`; `View` hardcodes `tea.MouseModeNone`). This stage turns
the framework into a mouse-capable host so the Stage 3 pilot inherits wheel/click for free.

## Context (from discovery)

Files/components involved (all under `internal/core/ui/tui/`):

- **`frame.go`** — `Frame.Update` already has a `case tea.MouseMsg:` that returns `f, nil`
  (`frame.go:142`, `// Stage 2` marker); `View` hardcodes `v.MouseMode = tea.MouseModeNone`
  and reads `f.opts.mouse` only to silence the linter (`frame.go:293-294`, `// Stage 2`).
  `handleBuiltin(ActionHelp)` toggles the help overlay; `f.focus.Set(id)` exists. The
  modal-input key policy (overlay open → only help-close/quit/esc act, everything else
  swallowed) is in `handleKey` and is the template the mouse path mirrors.
- **`registry.go`** — `Binding.Mouse string` is a documented Stage 2 seam (locked vocabulary,
  "Unused by dispatch this stage"). `Registry.Match(key)` reads the `keys` map; there is no
  mouse lookup yet. `Registry.Binding(a Action) (Binding, bool)` exists.
- **`actions.go`** — `standardBindings` table (keys + section, "framework-supplied defaults,
  plugin-handled"); `ActionNavUp`/`ActionNavDown`/`ActionSelect` are present with empty
  `Mouse` fields. `RegisterStandard` registers them opt-in.
- **`overlay.go`** — `centerOffset(body Region, ov Overlay) (x, y int)` is the exact centring
  math the hit-test reuses for modal bounds. `overlayBaseLayerID`/`overlayModalLayerID` +
  `overlayClicksOutsideSwallowed = true` document the click-outside policy; the layer IDs
  were left for an optional `Compositor.Hit` path we are NOT taking (see Solution Overview).
- **`geometry.go`** — `Geometry{Outer, Inner, Status, Overlay}`, `layoutPanels(body, weights)`
  (the panel→outer-region split), `contentRegion`. `Geometry.Status.Y == Outer.Height` is the
  status-line row index; `Geometry.Overlay == Inner` is the modal coordinate space.
- **`plugin.go`** — `Plugin` interface (PINNED, not frozen — spec §7), `Overlay{Content,
  Width, Height, CapturesInput}`, `Panel{ID, Title, Weight}`, `PanelID`. The interface is
  **not modified** this stage (decision below).
- **`run.go`** — `RunOptions.Mouse` → `frameOptions.mouse` via `withMouse`; inert.
- **bubbletea v2** (`charm.land/bubbletea/v2@v2.0.7`): `tea.MouseModeCellMotion` (`tea.go:290`
  — click+release+wheel, NO motion); messages `tea.MouseWheelMsg` / `tea.MouseClickMsg` /
  `tea.MouseReleaseMsg` / `tea.MouseMotionMsg` (`mouse.go`); `Mouse struct {X, Y int; Button
  MouseButton; Mod KeyMod}`; buttons `MouseLeft`, `MouseWheelUp`, `MouseWheelDown`;
  `tea.Tick(d, func(time.Time) tea.Msg) tea.Cmd`.
- **Locked design**: `docs/internals/tui-keymap.md` §2.3 + §6 (vocabulary + default bindings +
  frame-owned behaviors). The code is authoritative; the doc is kept in sync this stage.

Related patterns: the Stage 1 `routeWhileCapturing` pure-helper + deferred-integration shape
(a contract is locked and unit-tested now; the full `frame.Update` wiring lands with the
Stage 3 consumer) is the exact model for the deferred plugin-click-forward seam here.

Dependencies identified: no new imports beyond `time` (already transitively present) for the
clock seam. No new importers of `tui`; `core/docs` untouched.

## Development Approach

- **testing approach**: **Regular (code-first)** — same rationale as Stage 0/1: thin code over
  an API being locked now; golden-first against just-stabilized rendering is wasteful (and the
  rendering does not even change — mouse is an input layer). Implement each component, then add
  tests in the same task.
- complete each task fully before moving to the next
- make small, focused changes; the package must build after every task
- **every task includes new/updated tests** (success + edge) as SEPARATE checklist items
- **all tests must pass before starting the next task**
- update this plan when scope changes during implementation
- backward compatibility: trivially preserved — no existing TUI is modified; only the `tui`
  package internals + two `docs/internals/*.md` files change. The `Plugin` interface is
  unchanged, so no consumer can break.

## Testing Strategy

- **unit tests**: required every task. `classifyHit`, `Registry.MatchMouse`, the wheel
  accumulator state machine, double-click detection, and the capability gate are pure /
  seam-injected and table-tested.
- **frame-level tests**: drive `Frame.Update` with synthetic `tea.MouseClickMsg` /
  `tea.MouseWheelMsg` / `wheelFlushMsg` and assert routing (help opens, focus changes, modal
  swallows) — matching the existing `frame_test.go` style (no real terminal; `View().MouseMode`
  is inspected directly, as `frame_test.go` / `run_test.go` already do).
- **golden**: the existing `frame_*.golden` / `help_default.golden` are NOT regenerated — the
  rendered output is unchanged (mouse is input-only). A test that asserts they stay byte-stable
  is the regression guard.
- **determinism**: wheel coalescing is tested by injecting `wheelFlushMsg` directly (no real
  wait); double-click timing uses an injected `frameClock` (no wall-clock).
- **no e2e**: this package has no UI e2e harness (matches Stage 0/1).
- test commands: focused `go test ./internal/core/ui/tui/...` (no embedded-docs gate for this
  package); full `make test`; lint `make lint`; `make build` after the `docs/internals/*.md`
  edits so the embedded copy + content hashes regenerate.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep this plan in sync with actual work done

## Solution Overview

The mouse layer is a pure input-routing addition to `Frame` — no rendering changes. Five
pieces:

1. **Enable gate** — `View` sets `v.MouseMode = tea.MouseModeCellMotion` only when
   `f.opts.mouse && f.mouseCapable()`, else `tea.MouseModeNone`. `mouseCapable()` is a thin
   `TERM != "dumb"` check via an injectable `termEnv` seam. CellMotion (not AllMotion) is the
   spec's fixed choice: click + wheel reporting, no motion spam. Opt-in is per-program via the
   existing `RunOptions.Mouse`.

2. **Registry mouse lookup** — `Binding.Mouse` is populated for the stdlib Nav/Select actions
   (`wheel-up`/`wheel-down`/`double-click`) and a new `Registry.MatchMouse(event)` resolves a
   mouse event string → `Action` by scanning bindings. This keeps the mouse→action mapping in
   the registry (in sync with keys, rebind-ready) rather than hardcoded in `frame.go`. `"click"`
   stays frame-owned (focus / help-hint), NOT a registry binding.

3. **Hit-test** — a pure `classifyHit(...)` over `Geometry` + the panel outer regions + the
   help-hint region + the active overlay's centred bounds, returning a `hitZone` (+ `PanelID`
   for a panel hit). We use **Region math, not `lipgloss.Compositor.Hit`**: the compositor only
   knows the base/modal layers, so it cannot classify panels or the help-hint zone — Region math
   is needed regardless, and the modal inside/outside test is trivially the centred overlay
   bounds. The `overlay.go` layer IDs stay as forward-looking documentation.

4. **Wheel coalescing** — an accumulator (`wheelAccum` signed delta, `wheelArmed` flag) + a
   `tea.Tick(coalesceWindow, wheelFlushMsg{})` flush. The first wheel event arms the tick;
   subsequent events within the window only sum; the flush dispatches exactly `abs(wheelAccum)`
   Nav steps and resets. Semantics: sum, never drop (a trackpad burst → one render of N steps; a
   slow wheel → N single steps). Because `tea.Tick` is one-shot and uncancellable, pushing an
   overlay clears the pending accumulation AND the `wheelFlushMsg` handler no-ops while an overlay
   is open — a tick armed before a modal opened can never dispatch Nav behind it.

5. **Click routing + double-click** — `tea.MouseClickMsg` (left button) is **classified first**
   via `classifyHit`, then acted on by zone: `zoneHelpHint` → `ActionHelp`; `zonePanel` →
   `focus.Set` and (only for panel hits) the double-click test; `zoneNone`/status → swallow.
   Double-click = a second left-click in the **same panel + same cell** within `doubleClickWindow`
   (injected `frameClock`), gated by a non-zero last-click sentinel (`!IsZero`) and cleared in
   full after it fires (triple-click → one Select). Modal open → ALL clicks and wheel events
   swallowed (mirror of the key modal policy; outside-modal does NOT dismiss).
   `MouseReleaseMsg`/`MouseMotionMsg` are ignored. The inside-panel click-forward to the plugin
   (row-select, tab-switch) is designed as a **documented seam** and deferred to Stage 3, exactly
   as Stage 1 deferred the `routeWhileCapturing` integration.

Key design decisions (settled in the Stage 2 brainstorm — encode, do not re-litigate):

- **`Plugin` interface unchanged.** Plugin-facing click forwarding is deferred to Stage 3; the
  contract is captured as a pure helper + doc, not a new method, so the pinned interface stays
  minimal until a real consumer exists.
- **CellMotion + opt-in + `TERM=dumb` gate.** No active mouse-capability probing (setting
  CellMotion on a non-mouse terminal is harmless — the enable escape is ignored). The gate only
  keeps `dumb` keyboard-only.
- **Region-math hit-test**, not `Compositor.Hit` (rationale above).
- **Accumulator + tick-flush** for wheel; **injected `frameClock`** for double-click — both for
  deterministic tests.
- **Registry-owned mouse mapping** via `Binding.Mouse` + `MatchMouse`; `"click"` frame-owned.

## Technical Details

- **`frameClock`**: `type frameClock interface { now() time.Time }`; production `realClock`
  using `time.Now`; `Frame.clock frameClock` defaulting to `realClock{}` in `newFrame`; tests
  inject a fake. Only double-click consumes it; wheel uses `tea.Tick` + direct `wheelFlushMsg`
  injection in tests.
- **`Frame` new state**: `clock frameClock`, `wheelAccum int`, `wheelArmed bool`, `lastClick
  struct{ id PanelID; x, y int; t time.Time }` (the `id` scopes a double-click to one panel; the
  zero `t` is the "no prior click" sentinel — never a valid prior event).
- **`wheelFlushMsg struct{}`**: a private framework message; handled by an explicit
  `case wheelFlushMsg:` in `Frame.Update` (it must NOT fall through to the plugin-forward
  default).
- **Constants** (`frame.go` or `geometry.go`): `coalesceWindow = 16 * time.Millisecond`
  (~1 frame), `doubleClickWindow = 400 * time.Millisecond`. Tunable; documented as provisional.
- **`hitZone`**: `type hitZone int` with `zoneNone`, `zonePanel`, `zoneHelpHint`, `zoneModal`,
  `zoneOutsideModal`.
- **`panelRect`**: a small pair `struct{ ID PanelID; Region Region }` so `classifyHit` can
  return the matched `PanelID`. Built in `Frame` from `plugin.Panels()` weights via
  `layoutPanels(f.geo.Outer, weights)`.
- **`classifyHit(geo Geometry, panels []panelRect, helpHint Region, ov *Overlay, x, y int)
  (hitZone, PanelID)`**: pure. When `ov != nil`, modal bounds = `centerOffset(geo.Overlay, *ov)`
  → `[x0, x0+ov.Width) × [y0, y0+ov.Height)`; inside → `zoneModal`, else `zoneOutsideModal`.
  When `ov == nil`: a point in `helpHint` → `zoneHelpHint`; a point in any panel's outer region
  → `zonePanel` + that `PanelID`; otherwise `zoneNone`.
- **`Frame.helpHintRegion() Region`**: recomputes the right-zone cell range from `helpHint()`
  width (`rw := lipgloss.Width(muted.Render(f.helpHint()))`) → `Region{X: width-rw, Y:
  geo.Status.Y, Width: rw, Height: 1}`. Single source so the rendered hint and the hit zone
  cannot drift.
- **`Registry.MatchMouse(event string) (Action, bool)`**: iterates the registered bindings and
  returns the action whose `Binding.Mouse == event`. First match wins; documented that the
  vocabulary is the locked set.
- **`Frame.handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd)`**: the dispatcher, called from
  the `case tea.MouseMsg:` arm replacing the current `return f, nil`. Per message type: wheel
  (swallowed while an overlay is open, else accumulate + arm tick) → click (classify-first, then
  act by zone; double-click only on `zonePanel`) → release/motion ignored. The `wheelFlushMsg`
  flush is a separate `Frame.Update` case (no-op while an overlay is open).

## What Goes Where

- **Implementation Steps** (`[ ]`): all the `tui` package code + tests, the keymap-doc +
  packages.md updates, and the embedded-docs regeneration — all in this repo.
- **Post-Completion** (no checkboxes): real-terminal eyeballing of wheel feel (trackpad burst
  vs slow wheel) is deferred to the Stage 3 pilot, when the framework first drives a real
  surface — there is no demo binary this stage.

## Implementation Steps

### Task 1: Mouse-enable mode + `TERM=dumb` capability gate

**Files:**
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/run.go`
- Modify: `internal/core/ui/tui/frame_test.go`
- Modify: `internal/core/ui/tui/run_test.go`

- [x] add a `termEnv func() string` field to `frameOptions` (default `func() string {
  return os.Getenv("TERM") }`, set in `newFrame` when nil) plus a `withTermEnv(func() string)
  frameOption` so the capability probe is injectable from in-package tests
- [x] add `func (f *Frame) mouseCapable() bool { return f.opts.termEnv() != "dumb" }` with a doc
  comment explaining we do NOT actively probe for a mouse (CellMotion is harmless on non-mouse
  terminals — the enable escape is ignored); the gate only keeps `TERM=dumb` keyboard-only
- [x] in `View`, replace the hardcoded `v.MouseMode = tea.MouseModeNone` + `_ = f.opts.mouse`
  seam with: `tea.MouseModeCellMotion` when `f.opts.mouse && f.mouseCapable()`, else
  `tea.MouseModeNone`; drop the `// Stage 2` placeholder comment, document CellMotion (click +
  wheel, no motion) as the fixed mode
- [x] refresh the now-stale "inert / MouseModeNone this stage" doc comments lit up by this task
  (English, per AGENTS.md): `frameOptions.mouse` (`frame.go:28`), `withMouse` (`frame.go:38`),
  `RunOptions.Mouse` (`run.go:36`), and the `run.go` package comment "the (inert) mouse seam"
  (lines 18-22) — they now describe an active CellMotion gate, not an inert seam
- [x] **update the two existing assertions that hardcode `MouseModeNone` for `mouse=true`** (they
  break the moment View lights up CellMotion): `TestFrame_View_Envelope`
  (`frame_test.go:139`, assert at `:146`) and `TestRun_MouseFlagReachesFrame`
  (`run_test.go:216`, assert at `:234`) — make each branch on the mouse flag (and, for the frame
  test, on an injected `withTermEnv`): `mouse=true` + non-dumb → `MouseModeCellMotion`,
  `mouse=false` → `MouseModeNone`. The `run_test.go` case routes through the real
  `Run`/`os.Getenv` (no `termEnv` seam reaches it), so it MUST pin the environment with
  **`t.Setenv("TERM", "xterm-256color")`** (Codex finding #4) before asserting `CellMotion` —
  otherwise a CI shell running `go test` with `TERM=dumb` would fail the assertion on correct
  code; the `TERM=dumb` path is covered separately by the frame-level `withTermEnv` test
- [x] write tests: `opts.mouse=false` → `View().MouseMode == MouseModeNone`; `opts.mouse=true`
  + `withTermEnv` returns non-dumb → `MouseModeCellMotion`; `opts.mouse=true` + `withTermEnv`
  returns `"dumb"` → `MouseModeNone`; default `termEnv` is wired (nil seam → real `os.Getenv`,
  no panic)
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 2: `Registry.MatchMouse` + stdlib mouse-binding defaults

**Files:**
- Modify: `internal/core/ui/tui/registry.go`
- Modify: `internal/core/ui/tui/actions.go`
- Modify: `internal/core/ui/tui/registry_test.go`
- Modify: `internal/core/ui/tui/actions_test.go`

- [x] in `actions.go`, set `Mouse` on the stdlib `standardBindings`: `ActionNavUp` →
  `"wheel-up"`, `ActionNavDown` → `"wheel-down"`, `ActionSelect` → `"double-click"`; leave the
  other stdlib actions' `Mouse` empty; update the table doc comment to note the mouse defaults
- [x] add `Registry.MatchMouse(event string) (Action, bool)` scanning registered bindings for
  `Binding.Mouse == event`; doc-comment that the vocabulary is the locked set
  (`wheel-up`/`wheel-down`/`click`/`double-click`) and that `"click"` is intentionally
  frame-owned (never registered as a mouse binding)
- [x] update the `Binding.Mouse` doc in `registry.go` from "Unused by dispatch this stage" to
  "resolved via [Registry.MatchMouse]; wired in Stage 2"
- [x] write tests: after `RegisterStandard(reg, ActionNavUp, ActionNavDown, ActionSelect)`,
  `MatchMouse("wheel-up") == ActionNavUp`, `"wheel-down" == ActionNavDown`, `"double-click" ==
  ActionSelect`; `MatchMouse("click")` returns `false` (frame-owned); `MatchMouse("nonsense")`
  returns `false`; an empty registry returns `false`
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 3: Hit-test — `classifyHit` + help-hint region

**Files:**
- Create: `internal/core/ui/tui/hittest.go`
- Create: `internal/core/ui/tui/hittest_test.go`
- Modify: `internal/core/ui/tui/frame.go`

- [x] create `hittest.go`: `hitZone` enum (`zoneNone`/`zonePanel`/`zoneHelpHint`/`zoneModal`/
  `zoneOutsideModal`), `panelRect struct{ ID PanelID; Region Region }`, a `Region.contains(x, y
  int) bool` helper, and `classifyHit(geo Geometry, panels []panelRect, helpHint Region, ov
  *Overlay, x, y int) (hitZone, PanelID)` — pure, no rendering, modal bounds via
  `centerOffset(geo.Overlay, *ov)`. Document the Region-math-over-`Compositor.Hit` rationale
- [x] add `Frame.helpHintRegion() Region` recomputing the right-zone cell range from
  `helpHint()` width so the rendered hint and the hit zone share one source; add `Frame.panelRects()
  []panelRect` building the outer regions from `plugin.Panels()` weights via `layoutPanels`
- [x] write tests: table-driven over width buckets 60/79/80/99/100 (odd + even) — a point inside
  each panel's outer region → `zonePanel` + correct `PanelID`; a point in the help-hint range
  (`y == geo.Status.Y`, `x ∈ [width-rw, width)`) → `zoneHelpHint`; a point elsewhere → `zoneNone`
- [x] write tests: with a non-nil overlay, a point inside the centred modal bounds → `zoneModal`;
  a point outside (but in body) → `zoneOutsideModal`; assert panel/help-hint zones are NOT
  returned while an overlay is present (modal takes the whole classification)
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 4: Wheel coalescing — accumulator + tick-flush

**Files:**
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/frame_test.go`

- [x] add a **dedicated** mouse test plugin in `frame_test.go` (e.g. `mousePlugin`) — do NOT
  mutate the shared `stubPlugin`: its `Actions` feeds `frame_help_open.golden`, so adding
  Nav/Select to it would change that golden and break the byte-stable guarantee (Task 7). The new
  plugin's `Actions` calls `RegisterStandard(reg, ActionNavUp, ActionNavDown, ActionSelect)`
  (using the Task 2 `Mouse` defaults so `MatchMouse` resolves), declares one panel, and records a
  per-action invocation count (`map[Action]int`) in `HandleAction` so "called exactly N times" is
  assertable
- [x] add the wheel state to `Frame` (`wheelAccum int`, `wheelArmed bool`) and the
  `wheelFlushMsg struct{}` private message + the `coalesceWindow` constant (16ms, documented
  provisional)
- [x] add `Frame.handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd)` and call it from the
  `case tea.MouseMsg:` arm (replacing `return f, nil`). For `tea.MouseWheelMsg`: when an overlay
  is open, swallow (return `f, nil`, no accumulation); else `wheelAccum += +1` (`MouseWheelUp`)
  / `-1` (`MouseWheelDown`); if `!wheelArmed`, set `wheelArmed = true` and return
  `tea.Tick(coalesceWindow, func(time.Time) tea.Msg { return wheelFlushMsg{} })`; otherwise
  return `f, nil`
- [x] add an explicit `case wheelFlushMsg:` in `Frame.Update` (BEFORE the plugin-forward
  default): **no-op when an overlay is open** (`!f.overlay.Empty()` → reset `wheelAccum=0`,
  `wheelArmed=false`, return `f, nil` — never dispatch behind a modal); else dispatch
  `abs(wheelAccum)` Nav steps — `MatchMouse("wheel-up"/"wheel-down")` → `plugin.HandleAction(action)`
  in a loop, batching the returned commands via `tea.Batch` — then reset `wheelAccum = 0`,
  `wheelArmed = false`; drain `PendingOverlay` after dispatch
- [x] **clear pending wheel state on overlay push** (Codex finding #1): `tea.Tick` is one-shot and
  cannot be cancelled, so a wheel that armed a tick BEFORE a modal opened would otherwise flush
  Nav behind the modal. Reset `wheelAccum=0`, `wheelArmed=false` wherever an overlay becomes
  visible — `handleBuiltin(ActionHelp)` (open branch) and `drainOverlay` — so a stale tick finds
  an empty accumulator and the `wheelFlushMsg` guard above makes it a true no-op (no
  generation/token machinery needed)
- [x] write tests (against the dedicated `mousePlugin`): burst — N `MouseWheelMsg` (same
  direction) injected, then ONE `wheelFlushMsg` → plugin's `HandleAction(ActionNavDown)` count ==
  N, accumulator reset; slow — `wheel → flush → wheel → flush` yields one Nav per flush; mixed
  up/down deltas sum to the net count and direction; the first wheel returns a non-nil `tea.Cmd`
  (the tick) and subsequent in-window wheels return nil; a wheel event while the help overlay is
  open is swallowed (no accumulation, no tick)
- [x] write test (Codex finding #1): **wheel-then-open-modal-then-flush** — inject a
  `MouseWheelMsg` (arms the tick), open help (push overlay), then inject `wheelFlushMsg` → ZERO
  Nav dispatched to the plugin and the accumulator is clear (no acting behind the modal)
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 5: Click routing + double-click + deferred panel-forward seam

**Files:**
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/frame_test.go`

- [x] add the `frameClock` interface (`now() time.Time`) + `realClock` (production, `time.Now`)
  + `Frame.clock` field defaulted in `newFrame`; add `lastClick struct{ id PanelID; x, y int; t
  time.Time }` to `Frame` (carrying the panel ID so a double-click must repeat in the SAME panel)
  and the `doubleClickWindow` constant (400ms, documented provisional)
- [x] extend `handleMouse` for `tea.MouseClickMsg` (left button only — ignore other buttons).
  **Classify FIRST via `classifyHit`** (Codex finding #2), then act by zone:
  - overlay open → `zoneModal`/`zoneOutsideModal` both swallow (outside does NOT dismiss — the
    locked Stage 1 policy), return `f, nil`; do NOT touch `lastClick`
  - `zoneHelpHint` → `handleBuiltin(ActionHelp)`; clear `lastClick` (a help click is not a
    select candidate)
  - `zonePanel` → `focus.Set(id)` (+ the deferred panel-forward seam below), THEN the
    double-click test, scoped to panel hits only: if **`!lastClick.t.IsZero()`** (Codex finding
    #3 — the zero `time.Time` sentinel must never count as a real prior click) AND
    `lastClick.id == id` AND same cell (`x,y` equal) AND `clock.now().Sub(lastClick.t) <
    doubleClickWindow`, dispatch `ActionSelect` via `MatchMouse("double-click")` →
    `plugin.HandleAction` and **clear the FULL `lastClick` record** (so triple-click → exactly one
    Select); otherwise record `lastClick = {id, x, y, clock.now()}`
  - `zoneNone` (blank/status space) → swallow; clear `lastClick` so two clicks on empty space can
    never synthesize a Select (Codex finding #2)
- [x] add the **deferred plugin-click-forward seam**: a documented pure helper (e.g.
  `panelLocal(outer Region, x, y int) (lx, ly int)` translating an absolute click to
  panel-inner-local coordinates) with a comment stating the row-select / tab-switch forward to
  `plugin` lands with the Stage 3 cmdbrowser pilot (mirroring Stage 1's `routeWhileCapturing`
  deferral); it is unit-tested but NOT yet wired into a plugin call
- [x] ignore `tea.MouseReleaseMsg` / `tea.MouseMotionMsg` in `handleMouse` (return `f, nil`),
  with a comment that CellMotion can still emit motion while a button is held and we do not act
  on it
- [x] write tests (against the `mousePlugin` from Task 4, with an injected fake `frameClock`):
  click on the help-hint region opens help (overlay non-empty after); click on a panel calls
  `focus.Set` (active panel changes); two left-clicks in the same panel+cell within the window →
  `HandleAction(ActionSelect)` count == 1; **three** left-clicks in the same panel+cell within the
  window → Select count == 1 (full `lastClick` reset holds); two clicks in the same cell OUTSIDE
  the window (fake clock advanced past `doubleClickWindow`) → two single-clicks, no Select
- [x] write tests for the Codex findings: two clicks on **blank/`zoneNone`** space within the
  window → NO Select (classify-first; `lastClick` cleared on `zoneNone`); two clicks on the
  **help-hint** within the window → help toggles, NO Select; **zero-start clock** + first click at
  cell **(0,0)** is NOT treated as a double-click (the `!IsZero` sentinel gate); a second click in
  a DIFFERENT panel/cell within the window → no Select; a non-left button click is ignored; a
  click with an overlay open does NOT pop the overlay (swallow); `panelLocal` translation is
  correct for a bordered panel at several buckets; a release/motion message is a no-op
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 6: Update reference docs — keymap + packages.md

**Files:**
- Modify: `docs/internals/tui-keymap.md`
- Modify: `docs/internals/packages.md`

- [x] in `tui-keymap.md` §2.3 + §6: change "Stage 2 seam" / "to be wired in Stage 2" wording to
  "wired (Stage 2)"; keep the registry-bound table (`wheel-up`→`nav.up`, `wheel-down`→`nav.down`,
  `double-click`→`select`) and the frame-owned table (click-on-panel→focus,
  click-on-help-hint→Help, click-outside-modal→swallow); add the resolved details — CellMotion
  mode + `TERM=dumb` gate + per-program opt-in, per-frame wheel coalescing (sum-never-drop),
  double-click cell+window rule, and that the **plugin-facing click forward (row-select /
  tab-switch) is deferred to Stage 3** with the `panelLocal` seam noted
- [x] in `packages.md`, update the `internal/core/ui/tui/` section: record the mouse layer
  (`MatchMouse` + `Binding.Mouse` wiring, `classifyHit` Region-math hit-test, wheel
  accumulator/tick-flush, double-click via injected `frameClock`, CellMotion enable gate); state
  explicitly that the `Plugin` interface stays **PINNED, not frozen** (unchanged this stage) and
  that plugin-click forwarding is deferred to Stage 3; keep the `core/ui` layering rule +
  `docstui` relocation note intact; link to `docs/internals/tui-keymap.md`
- [x] no test (documentation); correctness verified by `make build` + review in Task 7

### Task 7: Verify acceptance criteria

- [x] verify every Overview deliverable exists and is exercised by tests (enable gate,
  `MatchMouse` + mouse defaults, `classifyHit`, wheel coalescing burst/slow, double-click,
  modal swallow, deferred seam, docs)
- [x] verify the package builds: `go build ./internal/core/ui/tui/...`
- [x] verify the existing goldens are byte-stable (rendering unchanged): the `frame_*.golden`
  and `help_default.golden` assertions still pass without regeneration
- [x] verify the `Plugin` interface is unchanged and there are NO new importers of `tui`;
  `core/docs` untouched. Expected changed paths: `internal/core/ui/tui/**`,
  `docs/internals/**`, and — because `docs/internals/*.md` is embedded — the git-tracked
  `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`; the embedded tree
  under `internal/core/docs/embedded/` is gitignored). Commit the regenerated
  `content_hashes_gen.go`
- [x] run focused suite: `go test ./internal/core/ui/tui/...`
- [x] run full suite: `make test`
- [x] run `make lint` — clean (gofmt/goimports/golangci-lint)
- [x] run `make build` so the embedded copy of the edited `docs/internals/*.md` regenerates

### Task 8: [Final] Finalize documentation + archive plan

- [x] confirm `docs/internals/tui-keymap.md` §6 + the `packages.md` `tui` section read coherently
  together (no stale "Stage 2 seam" / "to be wired" language remaining in either)
- [x] update `CLAUDE.md`/`AGENTS.md` only if a new load-bearing pattern emerged (likely not — the
  mouse layer is internal framework detail, not a project-config contract)
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification** (deferred / optional):
- Real-terminal eyeballing of wheel feel — a fast trackpad burst vs a slow wheel — happens during
  the Stage 3 cmdbrowser pilot, when the registry + mouse layer first drive a real surface. There
  is no demo binary this stage. The spec (§7 "Mouse wheel feel") flags real-device testing as the
  true validation of `coalesceWindow`; tune the constant then if the burst/slow balance feels off.

**Feeds into later stages**:
- Stage 3 (cmdbrowser pilot) brings the first real mouse consumer: it wires the deferred
  `panelLocal` seam into plugin row-select / tab-switch click forwarding, completing the
  `frame.Update` mouse integration this stage intentionally deferred. Per spec §7 the pilot may
  feed one revision back into the `Plugin` contract before it freezes for Stages 4–5b.

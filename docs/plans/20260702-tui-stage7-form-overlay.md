# TUI Stage 7: In-TUI form overlay (edit-and-stay)

## Overview

Add the embeddable-form capability to the `tui` framework: host a `huh.Form` as a
child model inside a **capturing overlay** (the inspect pattern), and wire the
**vars-browser edit mode** onto it so `dwe vars` edits happen in-TUI — form opens
over the browser, saves refresh the row in place with a status-line flash, esc
cancels back into the browser.

This is **Stage 7** of the Unified TUI Framework milestone
(`docs/plans/specs/2026-06-23-tui-framework-milestone.md`, § Stages row 7). It
depends on Stage 3 (cmdbrowser on `Frame`, implemented) and **Stage 6** (forms
unification — `ask.Build` / `Form.Huh()` / `Form.Result()` seam,
`docs/plans/20260702-tui-stage6-forms-unification.md`). Plans run in order, so this
plan assumes Stage 6's API exists exactly as planned; Task 1 verifies that before
any work starts.

**Problems it solves** (spec § Goals + § Form interop rule):

1. Today the vars browser *simulates* edit-and-stay: Enter exits the whole TUI,
   runs a standalone `ask.Run` form, then re-launches the browser from scratch
   (`internal/cli/vars/browser.go` loop). Every edit tears down and rebuilds the
   alt-screen, the tree state, and the inspect cache.
2. The framework has no way to host a form without leaving the TUI — the
   milestone's second form-host mode ("in-TUI overlay, edit-and-stay") does not
   exist yet.
3. Two framework gaps block any capturing overlay with async content: non-key
   messages `Push` duplicate overlay snapshots instead of `ReplaceTop`-ing (huh
   cursor-blink ticks would grow the stack), and a plugin cannot close its own
   overlay (today only esc / click-outside can).

**Key benefits:** `tui.FormOverlay` is a reusable component any plugin can embed
(vars edit now, future inline edits later); the vars browser becomes a true
edit-and-stay surface; the framework overlay layer gains the two missing
primitives (capturing-aware refresh, plugin-initiated close) that any future
async overlay needs.

## Context (from discovery)

- **Files/components involved:**
  - `internal/core/ui/tui/` — `Overlay{CapturesInput, ReleaseMouse, FullScreen}`
    (`plugin.go:44`), `overlayStack` Push/Pop/ReplaceTop (`overlay.go`),
    `Frame.drainOverlay` (Push, `frame.go:431`) vs `refreshCapturingOverlay`
    (ReplaceTop, `frame.go:443`), `dismissTopOverlay` → `OverlayClosedMsg`
    (`frame.go:417`), key routing under capture (`routeWhileCapturing`,
    `frame.go:626`: only ctrl+c = hard-quit and esc = close survive as framework
    actions), `FocusRequestMsg` as the existing plugin→framework message
    precedent, `centerOffset`/`Composite`/`clampOverlay` (`overlay.go`),
    `RenderFrameAfterSetup` test harness (`testsupport.go:54`).
  - `internal/core/ui/cmdbrowser/` — the inspect overlay as the pattern to
    mirror: `inspectState` + embedded `viewport.Model` (`inspect.go`),
    `inspectPending` republish flag (`plugin.go:70`), `PendingOverlay()`
    (`plugin.go:664`), chrome constants `inspectBoxHChrome=4`/`inspectBoxVChrome=2`,
    `inspectViewportSize()` (`plugin.go:598`). `ModeEdit`/`ActionEdit`
    (`run.go:40-65`), `onSelect` commit-and-quit (`actions.go:243`), `Options`
    (`run.go:91`), `Item` (`run.go:73`), `Result` (`run.go:134`), narrow fallback
    `runFallback` below `minBrowserWidth=80`.
  - `internal/cli/vars/browser.go` — `runVarsBrowser` exit→form→reopen loop
    (`:36`), `buildVarsBrowserItems` + `inspectCache` (`:92`, `:107`),
    `inlineBrowserValue` (`:131`), `layerBadge`.
  - `internal/cli/vars/set.go` — `promptForVarValue` (`:232`),
    `varSetFormDescription` (`:255`), `writeVarOverride` (`:190`) which acquires
    locks via `cmdctx.AcquireProjectLocksOrReport` (prints lock-held diagnostics
    to stderr — corrupts the alt-screen if called while the TUI is live),
    `captureLocalState`/`restoreLocalState`, `runAsk` seam (`:30`).
  - `internal/core/ui/docstui/plugin.go:102-121` — the status-flash pattern to
    mirror in cmdbrowser: `statusFlashDuration = 2s`, generation-gated
    `statusFlashClearMsg{gen}` so a stale clear tick never wipes a newer flash.
  - `internal/core/ui/ask/` — Stage 6 (planned) adds `Build(title, fields, opts)
    (*Form, error)`, `(*Form).Huh() *huh.Form`, `(*Form).Result() Result`,
    `RunOptions.ShowHelp *bool`, and the `widgets.ErrCancelled` cancel contract.
- **huh/v2 v2.0.3 embedding facts (verified against the module source):**
  - `SubmitCmd`/`CancelCmd` are assigned only inside `RunWithContext`
    (`form.go:669-670`) — **nil when embedded**. Completion/abort is detected
    ONLY by polling the public `form.State` field (`StateNormal`=0 /
    `StateCompleted` / `StateAborted`, `form.go:41`) after each `Update`.
  - **Completion is asynchronous** (codex finding): the Enter `Update` does NOT
    set `StateCompleted` — `Input.Update` returns a `NextField` cmd
    (`field_input.go:349-357`), the group turns it into `nextGroupMsg` on the
    last field (`group.go:221-235`), and only THAT follow-up `Update` sets
    `StateCompleted` (`form.go:576-584`). The host must therefore return huh's
    cmds to bubbletea (they come back as async msgs through the Frame's default
    branch → plugin → `FormOverlay.Update`) and poll `State` after EVERY
    forwarded msg — the plan's edit flow already does both; tests must pump the
    returned cmds before asserting completion.
  - `form.Update` returns `(huh.Model, tea.Cmd)` — the host type-asserts back to
    `*huh.Form`. Once `State != StateNormal`, `Update` is a no-op.
  - Form-level Quit binds **ctrl+c only** (`keymap.go:109`); esc is in-field
    navigation. Under a capturing overlay neither reaches the form (framework
    reserves both), which is exactly the arbitration we want.
  - `form.Init()` returns `tea.Sequence(group inits…, tea.RequestWindowSize)`,
    and the Frame DOES forward `WindowSizeMsg` to `plugin.Update`
    (`frame.go:248-253`) — with the plugin forwarding everything to the form,
    huh would auto-size its group height from the **terminal** dims while its
    height is still 0 (codex finding). `FormOverlay.Update` must therefore
    **swallow `tea.WindowSizeMsg`** — sizing is host-owned exclusively via
    `WithWidth` at construction and `Resize`.
  - bubbles/v2 `textinput` uses a **virtual cursor** by default
    (`useVirtualCursor: true`) rendered inline in the `View()` string — no
    `tea.View.Cursor` plumbing through the Frame is needed. Cursor blink arrives
    as async (non-key) messages — which is what makes the `drainOverlay` Push
    bug load-bearing.
- **Verified framework bug:** `Frame.Update`'s default branch (and the
  `WindowSizeMsg` / `FocusRequestMsg` branches) call `drainOverlay()` → `Push`
  unconditionally (`frame.go:246-281`). With a capturing overlay open, any
  async message that re-marks the plugin's pending overlay (blink tick, resize)
  stacks a duplicate snapshot that esc must then pop one at a time.
- **Dependencies identified:**
  - `tui` must NOT import `ask` (framework stays decoupled from the declarative
    builder) — `FormOverlay` accepts `*huh.Form`; the plugin passes
    `askForm.Huh()`. `tui` already imports `charm.land/huh/v2`? No — it gains
    the import (huh is already a direct module dependency; no layering issue).
  - `cmdbrowser` gains a NEW `ask` import (it has none today; peer `core/ui`
    package, `ask` imports only `widgets` + `styles` — no cycle; verify at
    implementation time). This coupling is **deliberately accepted**
    (plan-review note): the framework stays `ask`-free (`FormOverlay` takes
    `*huh.Form`), while the plugin-level `EditSpec` speaks `ask.Form` /
    `ask.Result` because its closures come from CLI surfaces that already
    build forms via `ask` — a `*huh.Form`-only `EditSpec` would force every
    caller to hand-roll result harvesting that `ask.Result` already owns.
  - `docs/reference/config/vars.md` § "`dwe vars` (no args) — TUI browser"
    describes the edit flow ("opens the `set` form, writes, and refreshes in
    place") — update to describe the in-TUI overlay + flash.

## Design decisions (from brainstorm — settled, do not re-litigate)

1. **Reusable `tui.FormOverlay` component** (new `formoverlay.go`), not
   cmdbrowser-local code: wraps a `*huh.Form`, applies `WithWidth` from the
   inner body dims (width = `min(body.Width − margins, formOverlayMaxWidth≈72)`;
   height content-driven, clamped by the existing `clampOverlay`), forwards
   `Init`/`Update` (type-asserting `huh.Model` back to `*huh.Form`), exposes
   `State() huh.FormState`, and renders the form in a rounded-border box with
   `Padding(0,1)` (mirror `inspectState.overlay()`) plus a footer hint row
   (default `enter save · esc cancel`). Hint text is hardcoded English —
   consistent with Stage 6's decision that form chrome i18n is out of scope.
   Returns `Overlay{CapturesInput: true}`. The consuming plugin drives the
   republished-snapshot pattern exactly like inspect (`editPending` flag,
   re-marked after every forwarded message). **Height caveat (plan-review
   finding):** the spec mentions `WithHeight`, but `FormOverlay` deliberately
   leaves height content-driven and documents the assumption that the form
   fits the body — `clampOverlay`'s `MaxHeight` truncation is lossy (a
   taller-than-body form would clip its submit control with no scroll), which
   is acceptable for the single-field vars form and must be stated in the
   type comment so a future taller consumer adds `WithHeight`/scroll support
   deliberately.
2. **Key arbitration (spec § 7 residual risk — closed):** `esc` = framework
   closes the overlay (existing `captureClose` → `dismissTopOverlay` →
   `OverlayClosedMsg`) = **cancel edit**; the plugin clears its edit state on
   `OverlayClosedMsg` and the form is discarded. `ctrl+c` = hard-quit of the
   whole TUI (existing `captureHardQuit`), unchanged. No new key routing.
   huh's own help line is suppressed (Stage 6 `ShowHelp: false`) — its hints
   would advertise ctrl+c as form-quit, which is now TUI-quit; the FormOverlay
   hint row is the single authoritative key hint.
3. **Frame fix — capturing-aware `drainOverlay`:** when the TOP overlay is
   `CapturesInput`, a pending overlay must `ReplaceTop`, never `Push`. Fold the
   check into `drainOverlay` itself (top capturing → ReplaceTop, else Push) so
   **every** call site is covered uniformly — `drainOverlay` has ~11 callers in
   `frame.go`, but the only ones reachable while a capturing overlay is Top()
   are the async branches (default, `WindowSizeMsg`, `FocusRequestMsg`); the
   rest run only with no overlay open, so the fold is provably behaviour-
   preserving for them (plan-review finding: fix the function body, not
   individual branches). `refreshCapturingOverlay` keeps its explicit-key-path
   role (behaviour now identical — may delegate to the same helper). Without
   this, huh cursor-blink ticks stack duplicate snapshots.
4. **New `tui.CloseOverlayMsg{}`** — plugin→framework message (mirror of
   `FocusRequestMsg`): the plugin returns it as the message of a `tea.Cmd`; the
   Frame pops the top overlay, resets the double-click record, and does **NOT**
   emit `OverlayClosedMsg` (plugin-initiated close — the plugin already knows).
   Needed because the form must close on successful submit (Enter), and today
   only esc / click-outside can pop an overlay.
5. **cmdbrowser edit hook, caller-supplied closures:** `Options` gains
   `Edit *EditSpec` with `BuildForm func(idx int) (*ask.Form, error)` and
   `Commit func(idx int, res ask.Result) (CommitOutcome, error)`;
   `CommitOutcome{Item Item, Flash string}`. In ModeEdit with `Edit != nil`,
   `onSelect` (Enter / double-click) opens the form overlay instead of
   committing `Result` + `tea.Quit`; `Edit == nil` preserves today's
   exit-and-return behaviour exactly (the narrow-terminal fallback and any
   other ModeEdit caller are untouched). On `StateCompleted` the plugin calls
   `Commit` **synchronously** inside `Update` (fast file write + config reload
   — acceptable), replaces `items[idx]` in place (a var edit never adds or
   removes leaves — tree shape and cursor untouched), sets a status flash
   `✓ <path> = <value>`, and returns a `CloseOverlayMsg` cmd. A `Commit` error
   closes the overlay and flashes `✗ <error>`. `StateAborted` (unlikely —
   ctrl+c never reaches the form) is treated as cancel. `OverlayClosedMsg`
   clears the edit state (esc cancel).
6. **Status flash in cmdbrowser** (none exists today): mirror docstui —
   `statusFlashDuration = 2s`, generation-gated clear tick
   (`statusFlashClearMsg{gen}`), flash text takes over the plugin's
   `StatusContext()` segment while set.
7. **Result/Idx semantics:** with `Edit != nil` the browser never returns an
   edit `Result` — `widgets.ErrCancelled` on quit is the normal exit. The
   ModeRun / ForceParamForm exit-and-run path is untouched (spec: cmdbrowser
   command launches stay exit-and-run).
8. **cli/vars wiring:** a shared field builder extracted from
   `promptForVarValue` serves both the standalone `dwe vars set <path>`
   no-value form (via `ask.Run`, behaviour unchanged) and the browser
   `EditSpec.BuildForm` closure (via `ask.Build` with `ShowHelp: false`).
   `Field.Validate` gains inline `varsusage.CoerceScalar` validation (invalid
   scalars / maps / sequences rejected in-form — an improvement over today's
   post-submit error; the post-submit coercion remains the authoritative
   parse). The `Commit` closure: coerce → **silent lock acquisition** via a
   new `cmdctx.AcquireProjectLocksSilent(baseDir)` helper (codex finding: keep
   the documented `cmdctx` lock-helper contract instead of an undocumented
   direct `lock.AcquireProjectLocks` call site — the silent variant returns
   `*lock.ProjectLockHeldError` unchanged and wraps other errors exactly like
   `AcquireProjectLocksOrReport`, but writes nothing; the lock-held error
   surfaces as the error flash — never printed, the alt-screen is live) →
   shared write core (capture / apply overlay / atomic write / reload config /
   restore on failure, extracted from `writeVarOverride`) → invalidate
   `inspectCache[path]` → recompute the item's `Description`
   (`inlineBrowserValue`) + `Type` badge (`layerBadge` on re-read layers) →
   return `CommitOutcome`. The CLI `writeVarOverride` keeps the printing
   `cmdctx.AcquireProjectLocksOrReport` wrapper on top of the same core.
   `runVarsBrowser` keeps its loop shape: the frame path handles edits in-TUI
   and only ever exits via `ErrCancelled` → nil; the <80-col fallback still
   returns a `Result` with `Idx` and loops through `runVarsSet` as today.
9. **Deliberate behaviour change:** after in-TUI edits the `✓ set …` stdout
   confirmation is replaced by the status flash, the browser **stays open**
   (that is the point of edit-and-stay), and nothing extra is printed on exit.
   Non-interactive / JSON `vars set` paths are untouched.
10. **Testing:** behavioural overlay tests mirror `inspect_test.go`; Frame
    tests pin the async-fix (stack depth stays 1) and `CloseOverlayMsg`
    (pops without `OverlayClosedMsg`); composited golden frame tests with the
    form overlay OPEN at width buckets 80/99/100 × height 24 via
    `RenderFrameAfterSetup` — deterministic because blink ticks are simply not
    delivered in tests (the virtual cursor stays in its initial state).

## Development Approach

- **Testing approach:** Regular (code first, then tests) — consistent with
  prior tui-stage plans. Form field *rendering* stays huh's (manually
  verified); overlay geometry, state transitions, and commit flow are
  unit/golden-tested.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task — success and error scenarios, as separate checklist items.
- **CRITICAL: all tests must pass before starting the next task** — no
  exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Migration compatibility: `Edit == nil` cmdbrowser behaviour is byte-identical
  (goldens must not change); ModeRun / ForceParamForm untouched; standalone
  `dwe vars set` form behaviour unchanged (plus inline validation); narrow
  fallback loop preserved; `vars set` JSON / non-interactive paths untouched.
- Run `make build`, `make test`, `make lint` at each task boundary (or
  `make embedded-docs` once + focused `go test` for tight loops).

## Testing Strategy

- **Unit tests (required per task):**
  - Frame: async non-key message with a capturing overlay open → stack depth
    stays 1 (ReplaceTop, not Push); `CloseOverlayMsg` pops without emitting
    `OverlayClosedMsg`; esc still emits it; help (non-capturing) path
    unchanged.
  - `FormOverlay`: typed keys drive the embedded huh input; Enter →
    `StateCompleted`; box dims at the width buckets (content-fit, max-width
    clamp); hint row present; `Resize` re-applies width.
  - cmdbrowser: Enter in ModeEdit with `Edit` set opens the overlay
    (PendingOverlay timing, `CapturesInput`); no double-push; `OverlayClosedMsg`
    clears edit state and a later raw key cannot resurrect the form; commit
    success → item replaced + flash set + `CloseOverlayMsg` returned; commit
    error → error flash; flash clear is generation-gated; `Edit == nil` +
    ModeEdit behaves exactly as today (existing tests keep passing).
  - cli/vars: `BuildForm`/`Commit` closures (item refresh, cache invalidation,
    lock-held error surfaces as error not print); shared write core rollback
    behaviour preserved; fallback loop semantics preserved via the existing
    `runBrowser` seam (no `t.Parallel()` in seam-overriding subtests).
- **Golden frame tests:** cmdbrowser full-frame goldens with the form overlay
  open at 80/99/100 × 24 (strip ANSI as the existing goldens do); existing
  no-edit goldens must be byte-identical.
- **e2e:** none (interactive-only surface).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## Solution Overview

Three layers, outside-in:

1. **Framework** (`tui`): make the overlay stack safe for async content
   (capturing-aware `drainOverlay`), give plugins a close primitive
   (`CloseOverlayMsg`), and ship the embeddable form host (`FormOverlay`).
2. **Plugin** (`cmdbrowser`): a caller-supplied `EditSpec` turns ModeEdit's
   Enter into "open form overlay" instead of "commit result and quit"; the
   edit state machine mirrors inspect; a docstui-style status flash reports
   outcomes.
3. **Surface** (`cli/vars`): builds the form via Stage 6's `ask.Build`, commits
   via a silent-lock variant of the existing comment-preserving write path,
   and refreshes the edited row in place.

## Technical Details

New/changed API surface:

```go
// internal/core/ui/tui/formoverlay.go
const formOverlayMaxWidth = 72 // tune during implementation

type FormOverlayOptions struct {
    MaxWidth int    // 0 = formOverlayMaxWidth
    Hint     string // footer hint row; "" = no hint row
}

type FormOverlay struct { /* form *huh.Form; width int; opts FormOverlayOptions */ }

func NewFormOverlay(form *huh.Form, body Region, opts FormOverlayOptions) *FormOverlay
func (fo *FormOverlay) Init() tea.Cmd               // form.Init()
func (fo *FormOverlay) Update(msg tea.Msg) tea.Cmd  // form.Update + re-assert *huh.Form
func (fo *FormOverlay) State() huh.FormState        // poll after each Update
func (fo *FormOverlay) Resize(body Region)          // re-apply WithWidth
func (fo *FormOverlay) Overlay() Overlay            // box + hint; CapturesInput: true

// internal/core/ui/tui/plugin.go
// CloseOverlayMsg: plugin → framework, returned as the msg of a tea.Cmd.
// Frame pops the top overlay WITHOUT emitting OverlayClosedMsg.
type CloseOverlayMsg struct{}
```

```go
// internal/core/ui/cmdbrowser/run.go
type EditSpec struct {
    BuildForm func(idx int) (*ask.Form, error)
    Commit    func(idx int, res ask.Result) (CommitOutcome, error)
}

type CommitOutcome struct {
    Item  Item   // replacement row for the edited index (value + badge + inspect)
    Flash string // status-line confirmation, e.g. `✓ db.host = "db.internal"`
}

type Options struct {
    // ...existing fields...
    Edit *EditSpec // ModeEdit only; nil = exit-and-return (today's behaviour)
}
```

Edit flow inside the plugin (mirrors `inspectState`):

- `onSelect` (ModeEdit, `Edit != nil`): `BuildForm(idx)` → `tui.NewFormOverlay(
  askForm.Huh(), body, opts)` → store `editState{fo, idx, askForm}` + set
  `editPending` → return `fo.Init()` cmd. Build error → error flash, no overlay.
- `Update` while `editState != nil`: forward the msg (keys AND non-key async)
  to `fo.Update`, re-mark `editPending`, then poll `fo.State()`:
  - `StateCompleted` → `Commit(idx, askForm.Result())`; success → replace
    `items[idx]` + refresh the derived list row, set flash, clear edit state,
    return `tea.Batch(closeOverlayCmd, flashClearTick)`; error → clear edit
    state, error flash, same close. Note: `CloseOverlayMsg` travels as a
    `tea.Cmd`, so the completed form renders for one extra frame before the
    pop — accepted (huh renders a completed/near-empty view; imperceptible),
    the plugin has no synchronous pop primitive by design.
  - `StateAborted` → treat as cancel (clear state + close).
- `OverlayClosedMsg` → clear `editState` (esc / click-outside cancel).
- `Resize` → `fo.Resize(body)` + re-mark pending.

cli/vars closures (captured: `cmd`, `flags`, `items`/`leaves`, `inspectCache`):

- `BuildForm(idx)`: `ask.Build("edit "+disp, buildVarSetFields(flags, path),
  ask.RunOptions{ShowHelp: &falseVal})` — fields carry the layered description
  (`varSetFormDescription`) and `Validate` = CoerceScalar probe.
- `Commit(idx, res)`: `CoerceScalar(res.String("value"))` →
  `cmdctx.AcquireProjectLocksSilent(baseDir)` (error → returned) → shared write
  core → reload config → `delete(inspectCache, path)` → rebuild the one `Item`
  (value, badge, inspect closure) → `CommitOutcome{Item, Flash}`.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, tests,
  internals/reference doc updates in this repo.
- **Post-Completion** (no checkboxes): manual interactive verification;
  follow-up notes.

## Implementation Steps

### Task 1: Verify Stage 6 seam + Frame overlay fixes (`drainOverlay`, `CloseOverlayMsg`)

**Files:**
- Modify: `internal/core/ui/tui/frame.go`
- Modify: `internal/core/ui/tui/plugin.go`
- Modify: `internal/core/ui/tui/frame_test.go` (or nearest existing Frame test file)

- [x] verify Stage 6 landed as planned: `ask.Build`, `(*ask.Form).Huh()`,
      `(*ask.Form).Result()`, `RunOptions.ShowHelp *bool`,
      `widgets.RunHuhForm`, `widgets.ErrCancelled` cancel contract — if any
      diverged, STOP and update this plan first
- [x] make `drainOverlay` capturing-aware: top overlay `CapturesInput` →
      `ReplaceTop`, else `Push` (covers the default, `WindowSizeMsg`, and
      `FocusRequestMsg` branches uniformly); keep `refreshCapturingOverlay`
      delegating to the same logic
- [x] add `CloseOverlayMsg{}` to `plugin.go` (doc comment: plugin-initiated
      close, no `OverlayClosedMsg` echo) and a `Frame.Update` case: pop top
      overlay if present, reset the double-click record, no plugin
      notification
- [x] write tests: non-key msg with a capturing overlay open + pending
      republish → stack depth stays 1 and top content is the fresh snapshot;
      same msg with a NON-capturing overlay top → Push behaviour unchanged;
      `CloseOverlayMsg` pops without `OverlayClosedMsg` reaching the plugin;
      esc still emits `OverlayClosedMsg`; `CloseOverlayMsg` on an empty stack
      is a no-op
- [x] run `make test` — must pass before task 2

### Task 2: `tui.FormOverlay` component

**Files:**
- Create: `internal/core/ui/tui/formoverlay.go`
- Create: `internal/core/ui/tui/formoverlay_test.go`

- [ ] implement `FormOverlay` per Technical Details: `WithWidth` sizing
      (min of inner width − margins and `MaxWidth`), `Init`/`Update` forwarding
      with the `huh.Model` → `*huh.Form` re-assert (returning huh's cmds —
      completion arrives on a follow-up Update, see Context), **swallowing
      `tea.WindowSizeMsg`** (sizing is host-owned; huh would otherwise
      auto-size height from terminal dims), `State()`, `Resize`,
      `Overlay()` rendering the rounded-border + `Padding(0,1)` box with the
      hint row, measured via `lipgloss.Width/Height`,
      `Overlay{CapturesInput: true}`
- [ ] document the embedding contract in the type comment: SubmitCmd/CancelCmd
      are nil when embedded (poll `State`), the host must size explicitly
      (huh's `RequestWindowSize` is harmless but the Frame never forwards
      `WindowSizeMsg` into overlays), virtual cursor renders inline, and the
      height caveat — content must fit the body; `clampOverlay` truncation is
      lossy for taller forms (design decision 1)
- [ ] write tests: driving a single-input huh form (built raw in-test — `tui`
      must not import `ask`) with typed keys mutates the bound value;
      **async completion** (codex finding): the Enter `Update` itself leaves
      `State == StateNormal` and returns a cmd — a test pump executes returned
      cmds and feeds the resulting msgs back through `Update` until
      `StateCompleted` (the same loop bubbletea runs in production); box width
      clamps at `MaxWidth` and at narrow bodies; a forwarded
      `tea.WindowSizeMsg` does NOT change the rendered form dims; hint row
      rendered when set, absent when `""`; `Resize` changes the rendered width
- [ ] write tests for error/edge cases: `Update` after completion is a no-op;
      nil-form guard (constructor rejects or documents)
- [ ] run `make test` — must pass before task 3

### Task 3: cmdbrowser `EditSpec` + edit state machine + status flash

**Files:**
- Modify: `internal/core/ui/cmdbrowser/run.go`
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/actions.go`
- Create: `internal/core/ui/cmdbrowser/edit.go` (editState; mirror `inspect.go`)
- Create: `internal/core/ui/cmdbrowser/edit_test.go`
- Modify: `internal/core/ui/cmdbrowser/plugin_golden_test.go`

- [ ] add `EditSpec` / `CommitOutcome` / `Options.Edit` (verify the
      `cmdbrowser → ask` import is cycle-free); `onSelect` in ModeEdit with
      `Edit != nil` opens the overlay (build → `FormOverlay` → pending +
      `Init` cmd); `Edit == nil` keeps today's commit-and-quit exactly
- [ ] implement the edit state machine per Technical Details (forward all
      msgs to `fo.Update` while editing, re-mark pending, poll `State` after
      EVERY forwarded msg — completion lands on the follow-up async msg, not
      the Enter key msg (see Context) — commit / abort / `OverlayClosedMsg`
      handling, `items[idx]` in-place replacement including the derived list
      row and inspect closure)
- [ ] add the status flash (docstui pattern: `statusFlashDuration = 2s`,
      generation-gated `statusFlashClearMsg`); flash text takes over
      `StatusContext()` while set; success `✓ …` / error `✗ …` single-line,
      truncated to the status segment width
- [ ] write tests (mirror `inspect_test.go`): overlay opens on Enter with
      `CapturesInput`; no double-push across republish; `OverlayClosedMsg`
      clears edit state and a later raw key cannot resurrect the form;
      commit success replaces the item, sets the flash, and returns a
      `CloseOverlayMsg` cmd (test pumps the huh cmds after Enter until the
      commit fires — async completion); commit error → error flash + close;
      stale-gen flash clear is ignored; `BuildForm` error → flash, no overlay
- [ ] write golden tests: full-frame with the form overlay OPEN at
      80/99/100 × 24 via `RenderFrameAfterSetup` (stub `EditSpec` with a
      deterministic form; no blink ticks delivered); confirm existing
      no-edit goldens are byte-identical
- [ ] run `make test` — must pass before task 4

### Task 4: cli/vars wiring — shared field builder, silent write core, EditSpec closures

**Files:**
- Modify: `internal/cli/cmdctx/locks.go`
- Modify: `internal/cli/cmdctx/locks_test.go` (or nearest existing test file)
- Modify: `internal/cli/vars/set.go`
- Modify: `internal/cli/vars/browser.go`
- Modify: `internal/cli/vars/browser_test.go`
- Modify: `internal/cli/vars/set_test.go` (or nearest existing test file)

- [ ] extract `buildVarSetFields(flags, path)` (title/description/`Validate`
      with the CoerceScalar probe) from `promptForVarValue`; standalone
      `vars set` keeps `ask.Run` via the `runAsk` seam (now with inline
      validation — note the improvement in the func comment)
- [ ] add `cmdctx.AcquireProjectLocksSilent(baseDir)` next to
      `AcquireProjectLocksOrReport`: identical error contract
      (`*lock.ProjectLockHeldError` returned unchanged, other errors wrapped
      as `acquiring project locks: %w`) but writes nothing — the sanctioned
      alt-screen variant (codex finding); unit-test both error paths
- [ ] split `writeVarOverride` into the shared core (capture → apply overlay →
      atomic write → reload → restore-on-failure) and two lock wrappers:
      CLI path keeps `cmdctx.AcquireProjectLocksOrReport` (printing), TUI
      path uses `cmdctx.AcquireProjectLocksSilent` and RETURNS the lock-held
      error (never prints — the alt-screen is live)
- [ ] implement the `EditSpec` closures in `browser.go` per Technical Details
      (coerce → silent write → reload → `delete(inspectCache, path)` →
      rebuild the one `Item` with fresh value/badge/inspect →
      `CommitOutcome{Item, Flash}`); wire `Edit` into the browser `Options`;
      keep the `runVarsBrowser` loop shape (frame path exits via
      `ErrCancelled` → nil; fallback path still loops through `runVarsSet`)
- [ ] write tests: `Commit` closure updates value + badge and invalidates the
      inspect cache; lock-held → error returned (flash path), local.yml
      untouched; write-failure rollback preserved through the refactor;
      `BuildForm` fields carry description + validator; fallback loop
      behaviour preserved via the `runBrowser` seam (no `t.Parallel()`)
- [ ] write tests for error cases: invalid scalar rejected by the field
      validator; `Commit` with an out-of-range idx guarded
- [ ] run `make test` — must pass before task 5

### Task 5: Docs — internals contracts, keymap note, vars reference

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `docs/internals/tui-keymap.md`
- Modify: `docs/reference/config/vars.md`
- Modify: `AGENTS.md`

- [ ] `packages.md`: § tui — `FormOverlay` embedding contract (poll `State`,
      explicit sizing, virtual cursor), `CloseOverlayMsg` semantics (no
      `OverlayClosedMsg` echo), the capturing-aware `drainOverlay` invariant;
      § cmdbrowser — `EditSpec`/`CommitOutcome`, edit state machine, status
      flash; § cli/vars — in-TUI edit flow, silent-lock write variant;
      § cmdctx — `AcquireProjectLocksSilent` as the sanctioned no-print
      variant for live-alt-screen call sites (same error contract as
      `AcquireProjectLocksOrReport`)
- [ ] `tui-keymap.md`: form-overlay arbitration note (esc = cancel edit,
      ctrl+c = TUI hard-quit; huh help suppressed, hint row authoritative)
- [ ] `vars.md` § TUI browser: describe the in-TUI overlay edit (form over the
      browser, flash confirmation, row refresh in place, esc cancel) and state
      BOTH observable behaviour changes explicitly (plan-review finding):
      (a) confirmations are a transient status flash, NOT stdout — after
      quitting the browser the terminal shows no record of the edits;
      (b) edit-and-stay applies only to the ≥80-col frame path — narrow
      terminals keep the flat fallback with the exit-after-commit loop
- [ ] `AGENTS.md`: extend the `tui.Plugin` Critical Pattern bullet with the new
      invariants (FormOverlay embedding facts incl. async completion +
      WindowSizeMsg swallow, `CloseOverlayMsg`, capturing-aware
      `drainOverlay`); extend the preflight+locks bullet with
      `AcquireProjectLocksSilent` as the alt-screen exception
- [ ] run `make build` (embedded docs re-sync) + `make test` — must pass
      before task 6

### Task 6: Verify acceptance criteria

- [ ] spec § Stages row 7 deliverables all present: embeddable-form capability
      in `tui`; esc/ctrl+c arbitration settled; form sized to inner modal dims
      with huh chrome reconciled (no double border, help suppressed); plugin
      reads `form.State` to dismiss and harvest; vars-browser edit mode on the
      overlay; cmdbrowser force-param-form still exit-and-run; golden frame
      tests at the width buckets
- [ ] behaviour preservation spot-checks: `Edit == nil` goldens byte-identical;
      ModeRun/ForceParamForm untouched; standalone `vars set` form unchanged;
      fallback loop intact; JSON / non-interactive paths untouched
- [ ] run full suite: `make build && make test && make lint`

### Task 7: [Final] Documentation and plan close-out

- [ ] confirm no other user-facing docs describe the old exit-and-reopen edit
      flow (grep `docs/` for the vars browser)
- [ ] update `AGENTS.md` Critical Patterns only if implementation surfaced a
      new trap beyond Task 5's entries
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification** (interactive rendering is not unit-tested, per existing
precedent):

- `dwe vars` in a TTY ≥ 80 cols: Enter on a leaf opens the form **over** the
  browser (browser dimmed beneath, status line visible); typing edits the
  value with a visible cursor; invalid input (e.g. `{a: 1}`) is rejected
  inline; Enter saves → overlay closes, row shows the new value + layer badge,
  status line flashes `✓ …` for ~2s, inspect overlay shows the new value;
  esc cancels back with the browser state intact (cursor, expansion, filter);
  ctrl+c quits the whole TUI.
- Lock contention: hold the project locks (e.g. a paused `dwe deploy run`) and
  save — the browser stays up and the status line flashes the lock-held error;
  `local.yml` is untouched.
- Narrow terminal (< 80 cols): `dwe vars` still uses the flat fallback with
  the old exit→form→reopen loop.
- `dwe vars set db.host` (no value, TTY): standalone form unchanged, now with
  inline validation.

**Stage/milestone hand-off:**

- This closes the milestone's final stage (7). The spec's § Charm-stack scope
  follow-up (`core/ui/render/` still on lipgloss v1) remains explicitly out of
  scope, as does any second `FormOverlay` consumer — the component is ready for
  future edit-and-stay surfaces without framework changes.

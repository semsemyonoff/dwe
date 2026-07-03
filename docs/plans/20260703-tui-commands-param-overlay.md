# In-TUI param-form overlay for `dwe commands` (Stage 7 follow-up)

## Overview

Extend the Stage 7 in-TUI form-overlay capability from the `dwe vars` browser to
the `dwe commands` browser so a command's **parameter form** opens as an overlay
**over** the running command browser instead of after the whole TUI tears down.

Today (`internal/cli/command/`): `cmdbrowser.Run` runs the two-panel browser in
`ModeRun`, the user picks a command (Enter, or `e` = force form), the **browser
quits and the alt-screen tears down**, control returns to `runCommandByID`, and
only then does it build the param form (`buildAskFields`) and run it as a plain
terminal huh form (`runAsk`, `runbyid.go:159`). The form is visually detached
from the browser the user was just in.

**After this change:** in `ModeRun`, selecting a command that needs a param form
opens a `tui.FormOverlay` **over** the browser (capturing overlay, browser dimmed
beneath, status line visible). The user fills the params in-place; on submit the
overlay closes, the browser quits, and the harvested values flow out through
`Result.Values` so `runCommandByID` skips its own form and executes directly. esc
cancels back into the browser (no command run); ctrl+c hard-quits the TUI.

This is a **follow-up to** `docs/plans/completed/20260702-tui-stage7-form-overlay.md`,
which deliberately scoped the overlay to `dwe vars` only (design decision 7:
"cmdbrowser command launches stay exit-and-run"). All the framework primitives it
shipped (`tui.FormOverlay`, `tui.CloseOverlayMsg`, capturing-aware `drainOverlay`,
per-overlay `CloseToken`) are reused unchanged; the only framework-level addition
is optional height/scroll support on `FormOverlay` for multi-field command forms.

**Problem it solves:** the param form is the one remaining `dwe commands` surface
that still leaves the TUI. Bringing it into the overlay makes command selection +
parameter entry a single continuous in-TUI experience, consistent with the vars
browser, without changing where the command actually *executes* (still after
alt-screen teardown, because commands stream docker/pipeline output to the plain
terminal).

**Key benefits:** reuses the entire Stage 7 overlay machine (state machine mirrors
`edit.go`); the command param form gains the same visual container as vars edit;
no change to the command execution path, non-interactive path, `--set` path, or
standalone `dwe commands <id>` path.

## Context (from discovery)

**Reconnaissance completed — both material risks retired before planning:**

1. **Multi-field forms / scroll (RETIRED → low):** huh's `Group` embeds a
   `bubbles/v2` `viewport.Model`
   (`charm.land/huh/v2@v2.0.3/group.go:28,60-64`); `Form.WithHeight(h)` fans the
   height into every group (`form.go:316-322` → `group.go:131-134`) and the group
   renders through the viewport with `SetContent` + `SetYOffset` tracking the
   focused field (`group.go:331-332`). So a param form taller than the overlay
   **scrolls internally inside huh** with auto-scroll to the focused field — no
   custom viewport wrapper is needed. `FormOverlay` only has to call
   `form.WithHeight(...)` with a bounded height. This removes the Stage-7
   "height caveat" (design decision 1) for this consumer.
2. **Confirmation prompt (RETIRED → settled decision):** `def.Confirmation`
   currently shows a huh confirm **after** the param form
   (`internal/cli/command/runbyid.go:202-225`, via `confirmRun`). It stays
   **post-exit** — it is a single yes/no shown immediately before the command
   runs in the plain terminal (right after `printRunHeader`), so it reads
   naturally alongside the run banner and streamed output. It is **not** pulled
   into the overlay. Only *parameter entry* moves into the TUI.

**Files/components involved:**

- `internal/core/ui/tui/formoverlay.go` — `FormOverlay` (181 lines). Add optional
  bounded height → `form.WithHeight`. `Overlay()` renders the huh view in a
  rounded-border box; height is measured via `lipgloss.Height`. `FormOverlayOptions`
  already carries `MaxWidth`, `Hint`, `CloseToken`.
- `internal/core/ui/cmdbrowser/` — the edit machine to mirror:
  - `edit.go` (159 lines): `editState`, `openEdit`, `updateEdit`, `commitEdit`,
    `setStatusFlash`/`statusFlashClearMsg`, `requestCloseOverlay(token)`.
  - `run.go` (244 lines): `Item`, `EditSpec`/`CommitOutcome`, `Options` (incl.
    `Edit *EditSpec` at `:134`), `Result` (`Idx`, `Action`, `SkipConfirm`,
    `ForceParamForm` at `:172`), `Mode`/`Action` enums, `Run` (`:207`).
  - `actions.go`: `onSelect` (`:243`, ModeRun Enter → `b.result` + `tea.Quit`),
    `onForceForm` (`:267`, `e` → `ForceParamForm` + quit). Two of the three ModeRun
    врезка points.
  - **`plugin.go:688-715` `updateInspect` (THIRD врезка point — codex finding):**
    Enter inside the inspect overlay. It already has a `ModeEdit && Edit != nil`
    branch that opens the edit overlay in place (`:701-709`); ModeRun falls through
    to `b.result = …; tea.Quit` (`:710-715`). Since `ActionInspect` is registered
    unconditionally (`actions.go:63`), inspect is reachable in ModeRun, so
    Enter-from-inspect on a param command would STILL tear down the TUI and hit the
    legacy plain form. This path MUST also route through `openRunForm` (mirror the
    ModeEdit branch: only retire inspect when a form actually opened / immediate-run
    was chosen; a BuildForm error keeps inspect valid).
  - `plugin.go`: `browser` struct (holds `edit *editState`, `editPending`,
    `editTokenSeq`, `flash`, `flashGen`, `body`, `items`), `Update` message
    routing, `PendingOverlay`, `StatusContext`.
  - `plugin_golden_test.go`: `RenderFrameAfterSetup` goldens at 80/99/100 × 24.
- `internal/cli/command/` — orchestration:
  - `command.go:142-183`: `skipConfirmFromTUI`/`forceFormFromTUI` out-params,
    `makeBrowserSelector(...)`, then `runCommandByID(... runOpts{ForceParamForm,
    ...})`.
  - `list.go:174-216`: `makeBrowserSelector` builds `[]cmdbrowser.Item`, calls
    `cmdbrowser.Run`, copies `res.SkipConfirm`/`res.ForceParamForm` into the
    out-params, returns the chosen `id` string.
  - `runbyid.go`: `runCommandByID` (`:28`). `parseSetFlags` → `provided` at `:81`;
    extractable param prep at `:89-143` (`resolve.ParamDefaults`, `resolve.Options`
    + membership validation) = the `prepareParams` body (`provided` is its input,
    not part of it). `showForm` decision at `:149-150`; form build+run at `:152-166`
    (`buildAskFields` → `runAsk`); confirm at `:202-225`; execute at `:231-236`.
    `runOpts` (defined nearby) carries `ForceParamForm`, `SetValues`, `Yes`, etc.

**Related patterns found:**

- The vars edit machine (`edit.go`) is the template: `openEdit` builds the form
  via a caller closure, wraps it in `tui.NewFormOverlay`, sets `editPending`;
  `updateEdit` forwards every msg, re-marks pending, polls `State()`;
  `commitEdit` runs on `StateCompleted`; `OverlayClosedMsg` clears state; a unique
  non-zero `CloseToken` per overlay guards the stale-close race.
- `tui.FormOverlay` async-completion contract (poll `State()`, completion lands on
  a follow-up Update, swallow `WindowSizeMsg`) — reused verbatim.

**Dependencies identified:**

- `tui` must stay `ask`-free — `FormOverlay` takes `*huh.Form`; the plugin passes
  `askForm.Huh()`. Unchanged.
- `cmdbrowser` already imports `ask` (`edit.go`, `run.go`). The new `RunFormSpec`
  reuses `*ask.Form`/`ask.Result`. No new import cycle.
- `internal/cli/command` already imports `cmdbrowser`, `ask`, `widgets`.
- `resolve.Options` / membership validation currently lives inline in
  `runCommandByID`; it must be reachable from the browser closure too → extract a
  `prepareParams` helper (see Technical Details).
- **`--set` threading gap (review finding):** `setFlags` is parsed into `provided`
  only *inside* `runCommandByID` (`parseSetFlags(opts.SetValues)`, `runbyid.go:81`);
  `makeBrowserSelector` (`command.go:146`) never receives it. `dwe commands --set
  x=y` (no id) is a valid browser-path invocation, so the `BuildForm`/`Harvest`
  closures need it too → pass the **raw `setFlags []string`** into
  `makeBrowserSelector` and parse it **lazily inside `BuildForm`**
  (`parseSetFlags`). **Do NOT parse before constructing the selector** (codex
  finding): the non-interactive branch (`command.go:147-162`) REPLACES the selector
  with `writeCommandsList` and returns `errCommandsListed` **before**
  `runCommandByID`, so today a malformed `dwe commands --set bad` in a pipe is
  silently ignored and the list prints. Eager parsing would make that path error
  out — violating the "non-interactive / `--set` paths untouched" invariant. Lazy
  parsing keeps the fallback intact because `BuildForm` never runs when the selector
  is swapped. On the interactive path a parse error surfaces as a status flash
  (slightly earlier than today's post-browser error — acceptable, interactive-only).

## Design decisions (settled — do not re-litigate)

1. **Separate `RunFormSpec` (not a generalized `EditSpec`).** `ModeRun` gets a new
   `Options.RunForm *RunFormSpec`, distinct from `Options.Edit *EditSpec`. The
   semantics differ fundamentally: EditSpec is **write-and-stay** (`Commit` writes
   `local.yml`, browser stays open, row refreshes); RunForm is **harvest-and-quit**
   (collect values, close overlay, quit browser, command runs *after* teardown).
   A shared spec with a mode flag would couple the vars and commands paths and risk
   the byte-identical ModeEdit goldens. The **overlay-driving state machine** is
   near-identical, so the low-level plumbing (`setStatusFlash`,
   `requestCloseOverlay`, pending/token handling, `FormOverlay` forwarding) is
   reused; only the terminal action (commit-write vs harvest-quit) differs. New
   code lives in `internal/core/ui/cmdbrowser/runform.go` mirroring `edit.go`.
2. **`RunFormSpec.BuildForm(idx int, force bool) (*ask.Form, error)`.** `force`
   distinguishes Enter (auto-skip the form when all required are satisfied) from
   `e`/EditParams (always show). A **nil form with nil error means "no form
   needed"** → the browser quits immediately with `Result{Idx, Action: ActionRun}`
   and **no `Values`** (byte-identical to today's exit-and-run for commands with no
   params or already-satisfied required). A non-nil form opens the overlay.
3. **Harvest via `Result.Values map[string]string`.** On `StateCompleted` the
   plugin harvests `askForm.Result()` into `Result.Values`, sets
   `Result.Idx`/`Action`/`ForceParamForm`, closes the overlay, and quits. The
   selector (`list.go`) plumbs `Values` out; `runCommandByID` uses them directly.
   `Values == nil` (every non-browser path, and the no-form-needed browser path)
   preserves today's behaviour exactly.
4. **`runCommandByID` short-circuit.** `runOpts` gains `PrefilledParams
   map[string]string` (nil = unchanged). When non-nil, `showForm` is forced false
   and `values` is taken from it directly, skipping `buildAskFields`/`runAsk`.
   Everything downstream (membership already validated at build time, `rctx` build,
   confirm, execute) is unchanged. Standalone `dwe commands <id>`, `--set`,
   non-interactive, and JSON paths never set `PrefilledParams`, so they are
   untouched.
5. **Confirm stays post-exit** (recon decision 2). No confirm in the overlay.
6. **Multi-field scroll via huh.** `FormOverlay` gains an optional bounded height
   (`FormOverlayOptions.MaxHeight`, 0 = content-driven as today) applied via
   `form.WithHeight` so command forms with many params scroll inside the overlay.
   The vars edit path passes 0 → byte-identical single-field behaviour.
7. **Force-form on Enter vs `e` unchanged in spirit.** `onSelect` (Enter) and
   `onForceForm` (`e`) both route through the new open-or-quit helper; the only
   difference is the `force` flag passed to `BuildForm`. When `RunForm == nil`
   (any caller that does not opt in, e.g. tests using `DefaultOptions`), both keep
   today's `b.result` + `tea.Quit` exactly.
8. **Status flash reused.** Build errors surface as the existing `✗ …` status
   flash (`setStatusFlash`/`flashError`); no new flash infra. Success needs no
   flash (the browser quits and the command runs immediately).

## Development Approach

- **Testing approach:** Regular (code first, then tests) — consistent with the
  prior tui-stage plans and Stage 7.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for its code changes —
  success and error scenarios as separate checklist items.
- **CRITICAL: all tests must pass before starting the next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- Migration compatibility (must hold at every task boundary):
  - `Edit == nil` / ModeEdit / ModeInspect goldens **byte-identical**.
  - `RunForm == nil` ModeRun behaviour byte-identical (existing cmdbrowser tests
    and goldens unchanged).
  - Standalone `dwe commands <id>`, `--set`, non-interactive, and JSON `commands`
    paths untouched (`PrefilledParams` nil).
  - `tui` stays `ask`-free.
- Run `make build`, `make test`, `make lint` at each task boundary (or
  `make embedded-docs` once + focused `go test` for tight loops).

## Testing Strategy

- **Unit tests (required per task):**
  - `FormOverlay`: `MaxHeight > 0` calls `form.WithHeight` and the rendered box
    height is bounded; a taller form scrolls (huh) rather than overflowing the
    box; `MaxHeight == 0` renders content-driven height identical to today;
    `Resize` re-applies both width and height.
  - `cmdbrowser` (mirror `edit_test.go`): Enter/`e` in ModeRun with `RunForm` set
    and a non-nil form opens a `CapturesInput` overlay (PendingOverlay timing);
    no double-push across republish; `OverlayClosedMsg` clears run-form state and
    a later raw key cannot resurrect it; `StateCompleted` harvests `Values`,
    returns a `CloseOverlayMsg` cmd, and quits with the right `Result` (test pumps
    the huh cmds after Enter until completion — async); a `BuildForm` returning a
    nil form quits immediately with `Result.Values == nil`; a `BuildForm` error →
    error flash, no overlay, no quit; `force` flag threaded correctly for Enter
    vs `e`.
  - `cli/command`: `prepareParams` extraction returns the same prefilled +
    resolvedOpts as the inline code (golden/table); `BuildForm` closure builds
    fields with `ShowHelp:false`; `Commit`/harvest closure returns the typed
    values; `runCommandByID` with `PrefilledParams` set skips the form and builds
    `rctx` from them (success + missing-required guard still enforced at resolve
    time); with `PrefilledParams` nil the path is unchanged (existing tests pass).
- **Golden frame tests:** cmdbrowser full-frame goldens with the **param form
  overlay open** at 80/99/100 × 24 via `RenderFrameAfterSetup` (deterministic —
  no blink ticks delivered), including a multi-field form to exercise the height
  bound; existing no-overlay ModeRun goldens must be byte-identical.
- **e2e:** none (interactive-only surface; manual verification in Post-Completion).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## Solution Overview

Three layers, outside-in (mirrors the Stage 7 shape):

1. **Framework** (`tui`): add optional bounded height to `FormOverlay` so
   multi-field forms scroll via huh's built-in group viewport. No other framework
   change — `CloseOverlayMsg`, capturing-aware `drainOverlay`, and `CloseToken`
   are reused as-is.
2. **Plugin** (`cmdbrowser`): a caller-supplied `RunFormSpec` turns ModeRun's
   Enter/`e` into "open param form overlay, harvest, quit-with-values" (or
   "quit-and-run immediately" when no form is needed); a run-form state machine
   mirrors the edit one; `Result.Values` carries the harvested params out. Build
   errors reuse the existing status flash.
3. **Surface** (`cli/command`): extract `prepareParams`, build the form via the
   existing `buildAskFields` inside a `RunFormSpec.BuildForm` closure, plumb
   `Result.Values` through the selector into `runOpts.PrefilledParams`, and
   short-circuit `runCommandByID`'s form step when they are present.

## Technical Details

New/changed API surface:

```go
// internal/core/ui/tui/formoverlay.go
type FormOverlayOptions struct {
    MaxWidth   int    // 0 = formOverlayMaxWidth (unchanged)
    MaxHeight  int    // NEW: content CAP (0 = content-driven). Natural height captured ONCE at construction; applySize always WithHeight(min(natural,budget)) where budget = MaxHeight−border−hint. Exact-fit when budget≥natural (no pad), scroll when budget<natural. Direction-safe on Resize (huh WithHeight is one-way, so clamp from stored natural, never re-measure a capped view).
    Hint       string
    CloseToken int
}
// applyWidth → applySize: also calls form.WithHeight(min(bodyHeight-chrome, MaxHeight)) when MaxHeight>0.
// Resize re-applies both.
```

```go
// internal/core/ui/cmdbrowser/run.go
type RunFormSpec struct {
    // BuildForm builds the param form for items[idx]. force is true when the user
    // pressed the force-form key (e); false for plain Enter. A nil form with nil
    // error means "no form needed" → quit-and-run immediately (no Values). A
    // non-nil error aborts with an error flash and opens no overlay.
    BuildForm func(idx int, force bool) (*ask.Form, error)
    // Harvest converts a submitted form into the param values carried out in
    // Result.Values. Kept separate from BuildForm so the plugin stays decoupled
    // from how the CLI maps ask.Result → param map (widget/multiselect specifics).
    Harvest func(idx int, res ask.Result) map[string]string
}

type Options struct {
    // ...existing fields...
    Edit    *EditSpec    // ModeEdit only (unchanged)
    RunForm *RunFormSpec // ModeRun only; nil = exit-and-run (today's behaviour)
}

type Result struct {
    Idx            int
    Action         Action
    SkipConfirm    bool
    ForceParamForm bool
    Values         map[string]string // NEW: harvested params from the in-TUI overlay; nil = none
}
```

```go
// internal/core/ui/cmdbrowser/runform.go  (mirrors edit.go)
type runFormState struct {
    fo    *tui.FormOverlay
    form  *ask.Form
    idx   int
    force bool
    token int
}
// openRunForm(idx, force) / updateRunForm(msg) / finishRunForm() — harvest+quit
// instead of commit+stay. Reuses setStatusFlash / requestCloseOverlay.
```

```go
// internal/cli/command/runbyid.go
type runOpts struct {
    // ...existing...
    PrefilledParams map[string]string // NEW: harvested in-TUI; nil = build the form here as today
}
// prepareParams(cfg, def, provided) (prefilled map[string]string,
//   resolvedOpts map[string][]model.OptionItem, err error) — extracted from
//   runCommandByID:89-143 (provided is its INPUT, from parseSetFlags at :81),
//   called by both runCommandByID and the BuildForm closure.
```

Edit/run-form врезка in `actions.go`:

- `onSelect` (ModeRun, list panel, `RunForm != nil`): return `b.openRunForm(idx,
  false)` instead of `b.result = …; tea.Quit`.
- `onForceForm` (`e`, `RunForm != nil`): return `b.openRunForm(idx, true)`.
- `RunForm == nil`: both keep today's `b.result` + `tea.Quit` verbatim.

`openRunForm(idx, force)`:
- `form, err := RunForm.BuildForm(idx, force)`; err → `setStatusFlash(flashError)`;
  **`form == nil` OR `form.Huh() == nil`** (the empty-form guard — codex finding:
  `ask.Build` returns a non-nil `&Form{empty:true}` with a nil huh form when
  `buildAskFields` yields zero fields; a nil huh form would make `FormOverlay`
  inert — StateNormal forever, no-op Update — trapping the user in a
  cancel-only overlay that never runs) → set `b.result = Result{Idx, Action:
  ActionRun, ForceParamForm: force, SkipConfirm: b.skipConfirm}` and return
  `tea.Quit` (no overlay, no Values — `runCommandByID` recomputes the identical
  prefilled from the same `provided`, so the command runs with the right values);
  else wrap in `tui.NewFormOverlay(form.Huh(), b.body, opts{Hint, CloseToken,
  MaxHeight})`, store `runFormState`, set pending, return `fo.Init()`.
- The cli/command `BuildForm` closure ALSO returns `(nil, nil)` when
  `buildAskFields` produced zero fields (belt-and-suspenders — never hand
  `openRunForm` an empty form in the first place).

`updateRunForm(msg)`:
- `OverlayClosedMsg` → clear run-form state (esc/click-outside cancel; browser
  stays; no command runs).
- else forward to `fo.Update`, re-mark pending, poll `State()`:
  - `StateCompleted` → `finishRunForm()`: harvest `Values`, set
    `b.result = Result{Idx, Action: ActionRun, ForceParamForm: force,
    SkipConfirm: b.skipConfirm, Values: …}`, clear state, return
    `tea.Batch(requestCloseOverlay(token), tea.Quit)`.
  - `StateAborted` → cancel (clear state + `requestCloseOverlay`).

cli/command closures (in `list.go`'s `makeBrowserSelector`, captured `cfg`, `reg`,
`defs`, the raw `setFlags []string` — a NEW selector input, parsed lazily inside
`BuildForm` (codex finding: parsing eagerly would break the non-interactive
`writeCommandsList` fallback) — `opts.Translator`, `locale`):

- `BuildForm(idx, force)`: `def := defs[idx]`; parse `--set` LAZILY here
  (`provided, err := parseSetFlags(setFlags)` — codex finding, see below) →
  `prefilled, resolvedOpts, err := prepareParams(cfg, def, provided)`; compute the
  same `showForm` predicate as `runbyid.go:149` (`len(def.Params) > 0 && (force ||
  !allRequiredSatisfied)`); if not → return `(nil, nil)`; else `fields :=
  buildAskFields(def, prefilled, provided, translator, locale, resolvedOpts)`; **if
  `len(fields) == 0` → return `(nil, nil)`** (empty-form guard — `buildAskFields`
  can skip every field via the empty-options rule); else return `ask.Build(
  "dwe commands › " + def.ID, fields, ask.RunOptions{ShowHelp: &falseVal})`.
- `Harvest(idx, res)`: recompute `prefilled` for `defs[idx]` (re-`parseSetFlags`
  + `prepareParams`, or reuse a small per-idx cache shared with `BuildForm`), then
  `mergeAnswers(res, defs[idx].Params, prefilled)` (reuse the existing merge).

`makeBrowserSelector` return: today returns just `id`. It gains an out-param
`prefilledOut *map[string]string` (mirroring `skipConfirmOut`/`forceFormOut`) set
from `res.Values`. `command.go` passes it into `runOpts.PrefilledParams`.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, in-repo docs.
- **Post-Completion** (no checkboxes): manual interactive verification.

## Implementation Steps

### Task 1: `FormOverlay` optional bounded height (huh scroll)

**Files:**
- Modify: `internal/core/ui/tui/formoverlay.go`
- Modify: `internal/core/ui/tui/formoverlay_test.go`

- [x] add `MaxHeight int` to `FormOverlayOptions` as a **content CAP, not a fixed
      height** (codex finding: huh's `Group.WithHeight` sets an EXACT viewport
      height and the bubbles viewport PADS short content to fill it — a naive
      `form.WithHeight(body.Height)` would balloon a one-field form into a tall
      blank modal). Doc: 0 = content-driven (today); >0 = clamp only when the form
      is taller than the cap
- [x] **capture the uncapped natural height ONCE** (codex-2 finding: huh
      `WithHeight(≤0)` is a no-op, NOT a reset — `form.go:317-318` — so height is
      one-way; and `lipgloss.Height(form.View())` AFTER a cap measures the
      already-clamped viewport, not the true content). In `NewFormOverlay`, after
      `WithWidth` and BEFORE any `WithHeight`, when `MaxHeight > 0` measure and
      store `fo.naturalHeight = lipgloss.Height(form.View())` (`Form.View` works
      pre-`Init`, `form.go:653`)
- [x] rename/extend `applyWidth` → `applySize(body Region)`: keep width logic; for
      height, when `MaxHeight > 0` compute `budget := MaxHeight − formOverlayVChrome`
      (`formOverlayVChrome = 2 (border) + hintRows`, `hintRows = 1` when
      `opts.Hint != ""` — the hint is JoinVertical'd INSIDE the box, so it must be
      in the budget or `clampOverlay` shaves the hint). ⚠️ **DEVIATION from the
      "ALWAYS call WithHeight" wording:** empirically `form.WithHeight(natural)`
      pads a fitting form by one row (huh's group footer `"" + footer` handling),
      so `WithHeight` is called ONLY when clamping is actually needed (`budget <
      natural` → `WithHeight(budget)`, scrolls) or to restore after a prior clamp
      (`budget ≥ natural` AND previously clamped → `WithHeight(natural)`,
      un-clamp). When it fits and was never clamped, `WithHeight` is left untouched
      → byte-identical to the `MaxHeight == 0` path. A `clamped bool` field gates
      the un-clamp; still direction-safe on shrink-then-grow `Resize`. `MaxHeight
      == 0` → NEVER call `WithHeight` (content-driven, byte-identical to vars)
- [x] `Resize` calls `applySize` (re-applies width AND the stored-natural clamp)
- [x] update the type doc: `MaxHeight` is a content CAP; the wrapper stores the
      natural height at construction and clamps from it; short forms render exact
      (no padding), tall forms scroll (huh viewport); content-driven (0) unchanged
- [x] write tests: a form taller than the cap → box height ≤ `MaxHeight` and it
      scrolls (huh viewport, focused field visible); a SHORT form with `MaxHeight`
      set is NOT padded (rendered `lipgloss.Height` == content height, not the
      cap); with a hint the total box height ≤ body (no `clampOverlay` shave);
      **shrink-then-grow `Resize`** (tall form in a small body → capped/scrolling →
      Resize to a large body → un-clamped to content height, codex-2 stale-cap
      regression test)
- [x] write tests for edge cases: `MaxHeight == 0` byte-identical to current
      single-field render; `MaxHeight` ≥ content renders exact content (no pad);
      nil-form guard still holds with `MaxHeight` set
- [x] run `make test` — must pass before Task 2

### Task 2: `cmdbrowser` `RunFormSpec` + run-form state machine + `Result.Values`

**Files:**
- Modify: `internal/core/ui/cmdbrowser/run.go`
- Create: `internal/core/ui/cmdbrowser/runform.go`
- Create: `internal/core/ui/cmdbrowser/runform_test.go`
- Modify: `internal/core/ui/cmdbrowser/actions.go`
- Modify: `internal/core/ui/cmdbrowser/plugin.go`
- Modify: `internal/core/ui/cmdbrowser/plugin_golden_test.go`

- [x] add `RunFormSpec` + `Options.RunForm` + `Result.Values` to `run.go`
      (doc-comment the harvest-and-quit vs edit-and-stay distinction; verify no
      new import cycle — `ask` already imported)
- [x] create `runform.go` mirroring `edit.go`: `runFormState`, `openRunForm(idx,
      force)`, `updateRunForm(msg)`, `finishRunForm()`; reuse `setStatusFlash`,
      `flashError`, `requestCloseOverlay`, and the pending/`CloseToken` handling.
      `openRunForm` treats `form == nil` OR `form.Huh() == nil` (empty form) as
      no-form → quit-and-run with no `Values` (codex finding: an empty `ask.Form`
      has a nil huh form → an inert cancel-only overlay otherwise);
      forms are hosted via `tui.NewFormOverlay(..., FormOverlayOptions{MaxHeight:
      <bounded>, Hint, CloseToken})` where `openRunForm` derives `MaxHeight` from
      `b.body.Height` (the body region the overlay composites over), NOT a constant
      — the vars `openEdit` passes 0 (content-driven) and stays unchanged
- [x] wire `actions.go`: `onSelect` (ModeRun, list) and `onForceForm` route
      through `openRunForm(idx, force)` when `RunForm != nil`; `RunForm == nil`
      keeps `b.result` + `tea.Quit` byte-identical
- [x] wire the THIRD врезка (codex finding): `plugin.go:688-715` `updateInspect`
      Enter — add a `ModeRun && RunForm != nil` branch mirroring the existing
      ModeEdit one (`openRunForm(b.inspect.inspectIdx, false)`; retire the inspect
      state only when a form actually opened OR immediate-run was chosen; a
      BuildForm error keeps inspect valid — its flash surfaces normally).
      `RunForm == nil` keeps the `b.result` + `tea.Quit` fall-through unchanged
- [x] wire `plugin.go` `Update`: while `runForm != nil`, route messages to
      `updateRunForm` (peel the flash-clear tick first, like the edit path).
      **`runForm` must take `Update`-routing AND `PendingOverlay` priority over
      `inspect`** — mirror the `b.edit != nil` check that precedes the inspect
      branch in `Update` (`plugin.go:278`) and in `PendingOverlay` (`plugin.go:737`).
      `ActionInspect` is registered UNCONDITIONALLY (`actions.go:63`, not
      mode-gated), so the inspect overlay is reachable in ModeRun and must not
      cross-route with an open run-form (an `OverlayClosedMsg`/wheel/mouse msg
      could otherwise hit the inspect-clearing path)
- [x] write tests (mirror `edit_test.go`): overlay opens on Enter/`e` with
      `CapturesInput`; force flag threaded (Enter=false, `e`=true); no double-push
      across republish; `OverlayClosedMsg` clears state and a later raw key cannot
      resurrect it; `StateCompleted` → `Result.Values` harvested, `CloseOverlayMsg`
      returned, `tea.Quit` issued (pump huh cmds until completion — async)
- [x] write tests for edge cases: `BuildForm` returns `(nil, nil)` → immediate
      quit with `Result.Values == nil` and `ForceParamForm` set per key; **empty
      form** (`BuildForm` returns a non-nil `*ask.Form` whose `Huh()` is nil) →
      same no-form quit-and-run, NOT a trapped overlay; `BuildForm` error → error
      flash, no overlay, no quit; `StateAborted` cancels; **inspect and run-form do
      not cross-route in ModeRun** (open a run-form, then an inspect-targeted msg /
      `OverlayClosedMsg` reaches `updateRunForm`, not the inspect-clearing path —
      and vice-versa); **Enter inside the inspect overlay in ModeRun opens the
      run-form overlay** (does NOT `tea.Quit`), and a BuildForm error keeps inspect
      open
- [x] write golden tests: full-frame with the param-form overlay OPEN at
      80/99/100 × 24 (single-field AND multi-field to exercise the height bound);
      confirm existing `RunForm == nil` ModeRun goldens are byte-identical
- [x] run `make test` — must pass before Task 3

### Task 3: `cli/command` — `prepareParams` extraction + `runCommandByID` short-circuit

**Files:**
- Modify: `internal/cli/command/runbyid.go`
- Modify: `internal/cli/command/runbyid_test.go`

- [x] extract `prepareParams(cfg, def, provided) (prefilled, resolvedOpts, err)`
      from `runCommandByID:89-143` (ParamDefaults + resolve.Options + membership
      validation; `provided` from `parseSetFlags` at `:81` is its INPUT); call it
      from `runCommandByID` unchanged in behaviour
- [x] add `runOpts.PrefilledParams map[string]string`; `prepareParams` still runs
      UNCONDITIONALLY (idempotent membership validation) — only `buildAskFields`/
      `runAsk` are skipped: when `PrefilledParams != nil`, force `showForm = false`
      and set `values = PrefilledParams` (`BuildRunContext`'s `resolve.Params`
      remains the final safety net); keep the non-interactive missing-required
      guard reachable for the `PrefilledParams == nil` path only
- [x] confirm the confirm block, `rctx` build, and execute path are unchanged for
      both branches (params already validated by resolve at build time)
- [x] write tests: `prepareParams` returns identical prefilled/resolvedOpts to the
      pre-refactor inline path (table incl. select/multiselect membership errors);
      `runCommandByID` with `PrefilledParams` set skips the form and builds `rctx`
      from them; confirm still fires when `def.Confirmation`
- [x] write tests for edge cases: `PrefilledParams` nil → existing behaviour
      (form shown / non-interactive guard) unchanged; missing-required surfaced at
      resolve time when a harvested map omits one (safety net)
- [x] run `make test` — must pass before Task 4

### Task 4: `cli/command` — browser closures + selector/`Values` plumbing

**Files:**
- Modify: `internal/cli/command/list.go`
- Modify: `internal/cli/command/command.go`
- Modify: `internal/cli/command/list_test.go` (or nearest existing selector test)

- [ ] extend `makeBrowserSelector`'s signature with the raw `setFlags []string`
      input (NOT a pre-parsed map — codex finding); parse it LAZILY inside
      `BuildForm`/`Harvest` via `parseSetFlags` so the browser param form honours
      `dwe commands --set x=y`. **Do not add an eager `parseSetFlags` before the
      selector is built** — it would break the non-interactive `writeCommandsList`
      fallback (`command.go:147-162` returns before `runCommandByID`)
- [ ] in `makeBrowserSelector`: build a `cmdbrowser.RunFormSpec` for `ModeRun`
      with `BuildForm(idx, force)` (lazy `parseSetFlags(setFlags)` →
      `prepareParams(cfg, defs[idx], provided)` → `showForm` predicate →
      `buildAskFields` → return `(nil,nil)` if zero fields, else `ask.Build(...,
      ShowHelp:false)`) and `Harvest(idx, res)` (recompute prefilled +
      `mergeAnswers`); set `opts.RunForm` only in `ModeRun` (nil in inspect/edit)
- [ ] add a `prefilledOut *map[string]string` out-param to `makeBrowserSelector`
      (mirror `skipConfirmOut`/`forceFormOut`), set from `res.Values`; leave
      `ModeInspect`/other callers passing nil
- [ ] in `command.go`: pass `&prefilledFromTUI` into `makeBrowserSelector` and
      thread it into `runCommandByID(... runOpts{PrefilledParams: prefilledFromTUI,
      ...})`; `ForceParamForm` still flows so the confirm/force semantics hold
- [ ] write tests: the `BuildForm` closure yields fields carrying description +
      `ShowHelp:false`; a nil form when required already satisfied and not forced;
      `--set x=y` provided → the closure's prefilled honours it (form pre-filled /
      no form when it satisfies required); `Harvest` maps `ask.Result` → the
      expected param map (incl. multiselect separator); selector copies
      `res.Values` into the out-param; index-range guard preserved
- [ ] write tests for edge cases: selector with `ModeInspect` never sets `RunForm`
      / `prefilledOut`; non-TTY selector fallback (`writeCommandsList`) path
      untouched — a malformed `dwe commands --set bad` in a pipe still prints the
      list and does NOT error (lazy parse never runs); `res.Values == nil` leaves
      `PrefilledParams` nil; `BuildForm` with zero resulting fields returns
      `(nil,nil)` (empty-options command) so the browser quits-and-runs
- [ ] run `make build && make test` — must pass before Task 5

### Task 5: Docs — internals, references, AGENTS.md

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `docs/reference/config/ui.md`
- Modify: `docs/internals/tui-keymap.md`
- Modify: `AGENTS.md`

- [ ] `packages.md`: § tui — `FormOverlay.MaxHeight` (huh group-viewport scroll,
      supersedes the single-field height caveat for opted-in consumers); §
      cmdbrowser — `RunFormSpec`/`Result.Values`, the run-form state machine
      (harvest-and-quit vs EditSpec's write-and-stay), `RunForm == nil` fallback;
      § cli/command — the in-TUI param-form flow, `prepareParams` extraction, and
      `runOpts.PrefilledParams` short-circuit; note confirm stays post-exit
- [ ] `ui.md`: describe that the `dwe commands` browser now collects params in an
      in-TUI overlay (≥80-col frame path); state the two behaviour facts — params
      are entered over the browser, the command still executes after the TUI exits,
      and the narrow (<80-col) fallback keeps the flat exit-then-form flow
- [ ] `tui-keymap.md`: extend the form-overlay arbitration note to cover the
      command param form (esc = cancel selection/back to browser, no run; enter =
      submit → run; ctrl+c = TUI hard-quit) — same arbitration as vars edit
- [ ] `AGENTS.md`: extend the `tui.Plugin` Critical Pattern bullet with the
      `FormOverlay.MaxHeight` scroll option and the `RunFormSpec` harvest-and-quit
      variant (distinct from `EditSpec`); note `Result.Values` + `PrefilledParams`
      as the harvest channel
- [ ] run `make build` (embedded-docs re-sync) + `make test` — must pass before
      Task 6

### Task 6: Verify acceptance criteria

- [ ] param form for a command WITH params opens as a `CapturesInput` overlay over
      the ModeRun browser (goldens present at 80/99/100); submit harvests
      `Values`, closes overlay, quits, command runs; esc cancels back into the
      browser with no run; ctrl+c hard-quits
- [ ] command with NO params / satisfied-required + Enter (no `e`) still
      exit-and-runs immediately with no overlay (byte-identical)
- [ ] multi-field form scrolls inside the overlay (huh viewport) at 24 rows
- [ ] behaviour preservation: `RunForm == nil` / ModeEdit / ModeInspect goldens
      byte-identical; standalone `dwe commands <id>`, `--set`, non-interactive,
      JSON paths untouched; confirm still fires post-exit
- [ ] run full suite: `make build && make test && make lint`

### Task 7: [Final] Documentation and plan close-out

- [ ] grep `docs/` to confirm no other user-facing doc describes the old
      exit-then-form command flow as the only path
- [ ] update `AGENTS.md` Critical Patterns only if implementation surfaced a new
      trap beyond Task 5's entries
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification** (interactive rendering is not unit-tested, per precedent):

- `dwe commands` in a TTY ≥ 80 cols: select a command with params via Enter →
  param form opens **over** the browser (dimmed beneath, status line visible);
  fill fields (multi-field scrolls with a visible focused field); submit → overlay
  closes, TUI exits, run banner + command output stream in the plain terminal.
- `e` (force form) on a command whose required are already satisfied → the form
  still opens in the overlay.
- Enter on a command with no params (or all required satisfied, no `e`) → runs
  immediately, no overlay.
- esc in the param overlay → back to the browser, command NOT run, browser state
  (cursor/expansion/filter) intact; ctrl+c → whole TUI quits.
- `def.Confirmation` command → after submit + TUI exit, the confirm yes/no shows in
  the plain terminal before execution (unchanged).
- Narrow terminal (< 80 cols): `dwe commands` keeps the flat fallback with the
  exit-then-form flow.
- Non-browser paths unchanged: `dwe commands <id>` (direct), `--set`, piped/CI,
  `--output json`.

**Follow-up notes:**

- This retires the last `dwe commands` surface that left the TUI for input; command
  *execution* intentionally remains post-teardown (streamed terminal output).
- No second framework primitive was needed — `FormOverlay.MaxHeight` is the only
  framework addition; `CloseOverlayMsg` / capturing-aware `drainOverlay` /
  `CloseToken` from Stage 7 are reused unchanged.

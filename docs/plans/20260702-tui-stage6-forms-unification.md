# TUI Stage 6: Forms unification

## Overview

Unify the `huh/v2` form layer behind a single execution helper and a declarative
keymap-override API, so the three raw `huh.NewForm` sites collapse back into the shared
`ask` wrapper and the `ask` package gains the build-vs-run seam Stage 7 needs.

This is **Stage 6** of the Unified TUI Framework milestone
(`docs/plans/specs/2026-06-23-tui-framework-milestone.md`, § Stages row 6). It is
**independent** of stages 0–5 (no `tui` framework dependency); Stage 7 (in-TUI form
overlay) depends on it.

**Problems it solves** (spec § Problems #7 + the deploy-menu duplication call-out):

1. Form keymap overrides don't scale — every site that needs a custom quit binding or an
   `esc cancel` help hint must abandon `ask.Run` and hand-roll a raw `huh.NewForm` with
   per-field help-slot hijack tricks. Three sites do this today, each re-deriving the
   trick for its field type.
2. The "hooks snapshot + theme + run + `ErrUserAborted` translation" plumbing is
   copy-pasted across `ask.Run`, all three `widgets` primitives, and all three raw sites.
3. `internal/cli/deploy/menu.go` carries ~80 lines of duplicated select-menu plumbing
   across its two interactive functions.
4. `ask` can only *run* a form; Stage 7 needs to *build* one and drive it as a child
   model inside a capturing overlay.

**Key benefits:** the help-slot hijack knowledge lives (and is tested) in exactly one
place; a single canonical cancellation error (`widgets.ErrCancelled`) across every form
surface; `ask.Build` + `Form.Huh()` is the ready-made seam for Stage 7.

## Context (from discovery)

- **Files/components involved:**
  - `internal/core/ui/ask/ask.go` — declarative form API (`Run`, `Field`, `Result`,
    `RunOptions{Input, Output}`); no keymap/`Height`/`Filterable` support today; returns
    raw `huh.ErrUserAborted` on cancel.
  - `internal/core/ui/widgets/` — `huh.go` (`SetHuhHooks` / `RunWithPromptHooks`),
    `confirm.go` (`RunConfirm`, `ConfirmRun`), `selector.go` (`RunSelector`),
    `multiselect.go` (`RunMultiSelect`), `filter_quit.go` (`newFilterAwareQuit`,
    unexported). Each primitive snapshots hooks + translates `ErrUserAborted` itself;
    each has a swappable `run*FormFn` test seam.
  - `internal/cli/deploy/menu.go` (~lines 402–561) — `selectMenuItemInteractive` +
    `selectDeployServiceInteractive`: raw `huh.NewForm`, shared `deployMenuKeyMap`
    (Quit = `q`/`esc`/`ctrl+c`; `Select.Filter` help-slot hijacked for the esc hint;
    `Select.Submit` help relabelled "select"), `Height(max(n+5, 12))`, locked-item
    `Validate`.
  - `internal/core/workflow/setup/huh.go` — `askPortOverrides` (raw form: per-conflict
    `huh.NewInput`, `Input.AcceptSuggestion` help-slot hijack via a fake
    `SuggestionsFunc`, Quit = `esc`/`ctrl+c`) and `askServiceToggles` (raw form:
    `MultiSelect` with `Filterable(false)`, `MultiSelect.Filter` help-slot hijack,
    "Always on:" pre-print). Both carry NOTE comments explaining why they could not use
    `ask` — those reasons are exactly what this stage removes.
- **Related patterns found:**
  - All keymap customization across the three raw sites is variations of one idea:
    custom Quit binding + surfacing its hint in huh's help line by hijacking another
    binding's help slot (`Select.Filter` / `MultiSelect.Filter` for list fields,
    `Input.AcceptSuggestion` for input fields).
  - `widgets.RunSelector` / `RunMultiSelect` install `filterAwareQuit` via
    `WithProgramOptions(tea.WithFilter(...))` so `q` types into an active filter instead
    of quitting.
  - Stage 1 keymap reference (`docs/internals/tui-keymap.md` § 7) pins the desired form
    bindings (`q`/`esc`/`ctrl+c` = quit, `enter` = confirm) and defers "wire forms
    through the action registry?" to this stage.
- **Dependencies identified:**
  - `ask` already imports `widgets` (for `RunWithPromptHooks`) — no layering change.
  - `ask.Run` cancel-check call sites that must move from `huh.ErrUserAborted` to
    `widgets.ErrCancelled`: `internal/core/workflow/setup/huh.go:86`,
    `internal/cli/scaffold/scaffold.go:320`, `internal/cli/command/runbyid.go:163`,
    `internal/cli/vars/set.go:244` (re-verify by grep during implementation).
  - Stage 7 consumes `ask.Build` + `Form.Huh()` + `Form.Result()`; nothing in this stage
    imports the `tui` framework.

## Design decisions (from brainstorm — settled, do not re-litigate)

1. **`widgets.RunHuhForm(ctx context.Context, form *huh.Form) error`** is the single
   execution point: prompt-hooks wrap (via the existing `RunWithPromptHooks`),
   `form.RunWithContext(ctx)`, and `huh.ErrUserAborted → widgets.ErrCancelled`
   translation. All widgets primitives and `ask` route through it internally; their
   public APIs and `run*FormFn` test seams are preserved.
2. **Canonical cancel error is `widgets.ErrCancelled` everywhere**, including `ask.Run`
   (contract change: it currently returns raw `huh.ErrUserAborted`). Call sites update.
3. **Declarative keymap overrides, no escape hatch** (YAGNI — extend the spec when a
   real site needs more): `RunOptions` gains `Quit *QuitSpec{Keys []string, Help
   string}`, `SubmitHelp string`, `ShowHelp *bool` (pointer: `nil` = leave huh's
   default; a plain bool would flip help off for every existing `ask.Run` caller).
   `ask.Field` gains `Height int` (0 = unset) and `Filterable *bool`
   (**FieldMultiselect only** — codex-review finding: huh/v2 `Select` has NO
   `Filterable` method, only `MultiSelect` does; `Select.Filtering(bool)` sets current
   state, not capability). The help-slot hijack per field kind lives inside `ask`.
   **Hijack visibility rules** (verified against huh/v2 v2.0.3 source):
   - select → Filter slot; always visible (`Select.KeyBinds` includes Filter
     unconditionally). The hijack rebinds the Filter activation key to the quit key,
     which as a side effect makes `/`-filtering unreachable — exactly today's deploy
     menu behaviour, intended.
   - multiselect with `Filterable` true/nil → Filter slot, same mechanics.
   - multiselect with `Filterable: false` → **no visible slot for the quit hint**:
     `MultiSelect.WithKeyMap` force-disables Filter/SetFilter/ClearFilter and
     `KeyBinds()` omits them when not filterable, so a hijacked Filter slot never
     renders. The form-level Quit binding still works — only the help hint is absent.
     This matches today's service toggles, whose existing Filter hijack is already
     ineffective for exactly this reason (the "esc cancel" hint does not render there
     now). Known limitation, documented, not worked around.
   - input → AcceptSuggestion slot + fake single-blank `SuggestionsFunc`.
   **No `filterAwareQuit` wiring in `ask`** (plan-review finding): the Filter-slot
   hijack and `filterAwareQuit` are mutually incompatible on the same list field (the
   hijack rebinds the Filter activation key, killing `/`-filtering; `filterAwareQuit`
   exists to let `q` type into an *active* filter), and none of the three migrated
   sites needs live filtering — the deploy menu's hijack makes `/` inert (as today),
   service toggles set `Filterable: false`, port overrides are inputs.
   `widgets/filter_quit.go` stays unexported and untouched; add `ask`-side
   filter-aware-quit support only when a real `ask` consumer needs a filterable list
   with a `q` quit key.
4. **Build-vs-run split**: `ask.Build(title, fields, opts) (*Form, error)`;
   `(*Form).Run(ctx) (Result, error)` (via `RunHuhForm`); `(*Form).Huh() *huh.Form` and
   `(*Form).Result() Result` for Stage 7's child-model overlay. `ask.Run` becomes
   `Build` + `Form.Run` — signature unchanged.
5. **Forms are NOT wired through the action registry** (closes the Stage 1 § 7 open
   question): huh keeps its own key routing; only quit bindings and their help display
   are unified. Recorded in `docs/internals/tui-keymap.md` § 7.
6. **i18n of form chrome strings is out of scope** — they are hardcoded English today
   and stay so; the `tui.help.*` framework namespace does not apply to huh forms.
7. **Invariant: prompt hooks fire exactly once per interactive prompt.** Moving the
   hook wrap into `RunHuhForm` must not double-fire when a wrapper (e.g. `RunSelector`)
   calls a seam whose default routes through `RunHuhForm` — the wrapper-level hook
   snapshot is removed in the same change. Wrappers keep a defensive
   `huh.ErrUserAborted → ErrCancelled` mapping so seam-swapped tests that return raw
   huh errors keep their contract. The same invariant applies to the `ask` path:
   `ask.Run`'s own `RunWithPromptHooks` wrap (currently at `ask.go:166`) is removed
   when `Form.Run` delegates to `RunHuhForm`, which wraps hooks itself.

## Development Approach

- **Testing approach:** Regular (code first, then tests) — consistent with prior
  tui-stage plans. Form *rendering* stays manually verified (existing precedent noted in
  `setup/huh.go`); form *construction* (keymap, slots, fields, bindings) is unit-tested
  by inspection.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  — success and error scenarios, as separate checklist items.
- **CRITICAL: all tests must pass before starting the next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Migration compatibility: each migrated site preserves its observable behaviour —
  deploy menu esc semantics (top menu esc → `menuExit, nil`; service picker esc →
  `ErrCancelled` → back to parent menu), locked-service rejection hints, port
  validation messages, service-toggle "Always on:" line and mandatory merging.
- Run `make build`, `make test`, `make lint` at each task boundary (or
  `make embedded-docs` once + focused `go test` for tight loops).

## Testing Strategy

- **Unit tests (required per task):**
  - `RunHuhForm` contract: hook pairing (before/after fire exactly once, after fires on
    error), `ErrUserAborted` translation, context cancellation path.
  - `QuitSpec` application by inspection of the built form/keymap: which help slot is
    hijacked for which field kind (select/multiselect → Filter slot; input →
    AcceptSuggestion slot + suggestions enabled), `SubmitHelp` relabel, `ShowHelp`
    tri-state.
  - `Build`/`Run` split: `Build` returns a runnable `*Form` without executing;
    `Result()` harvests bindings; `ask.Run` ≡ `Build`+`Run` (existing `ask_test.go`
    suite keeps passing with the `ErrCancelled` contract update).
  - Migrated sites: existing table-driven tests updated (deploy menu items/labels are
    pure helpers already; setup coercion helpers unchanged).
- **No golden frame tests** — rendering is huh's; no `tui` framework involvement.
- **e2e:** none (interactive-only surface; JSON/plain output paths untouched).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## Solution Overview

Two-layer unification:

1. **Execution layer** (`widgets.RunHuhForm`) — one function owns "hooks + run +
   translate". `RunConfirm`, `ConfirmRun`, `RunSelector`, `RunMultiSelect` route their
   default seam implementations through it and drop their local hook/translate blocks;
   `ask` routes through it too.
2. **Construction layer** (`ask`) — `RunOptions` grows the declarative `QuitSpec` /
   `SubmitHelp` / `ShowHelp`; `Field` grows `Height` / `Filterable`; the per-field-kind
   help-slot hijack is implemented once inside `buildHuhField`/`Build`. `Build` returns an `ask.Form` wrapper (`huh *huh.Form` +
   `bindings`), giving Stage 7 its child-model seam.

The three raw sites then become plain `ask.Run` calls with options; `deployMenuKeyMap`
and the duplicated form plumbing in `deploy/menu.go` are deleted; the two NOTE comments
in `setup/huh.go` explaining why migration was impossible are removed along with the
hacks they document.

## Technical Details

New/changed API surface (`internal/core/ui/ask`):

```go
type QuitSpec struct {
    Keys []string // e.g. "esc", "q", "ctrl+c"
    Help string   // help-line verb: "cancel" / "back" / "exit"
}

type RunOptions struct {
    Input      io.Reader
    Output     io.Writer
    Quit       *QuitSpec // nil = huh defaults (no keymap customization)
    SubmitHelp string    // cosmetic: relabel submit help ("select"); "" = default
    ShowHelp   *bool     // nil = leave huh default; non-nil = WithShowHelp(*v)
}

type Field struct {
    // ...existing fields...
    Height     int   // select/multiselect viewport height; 0 = unset
    Filterable *bool // multiselect ONLY (huh/v2 Select has no Filterable); nil = huh default
}

type Form struct { /* huh *huh.Form; bindings []fieldBinding */ }

func Build(title string, fields []Field, opts RunOptions) (*Form, error)
func (f *Form) Run(ctx context.Context) (Result, error) // widgets.RunHuhForm inside
func (f *Form) Huh() *huh.Form // Stage 7: child-model access (Update/View driven)
func (f *Form) Result() Result // harvest bound values after completion
func Run(ctx context.Context, title string, fields []Field, opts RunOptions) (Result, error)
// Run = Build + Form.Run; on cancel returns widgets.ErrCancelled (contract change).
```

New in `internal/core/ui/widgets`:

```go
// RunHuhForm is the canonical executor for every huh form in dwe: wraps the
// prompt hooks (RunWithPromptHooks), runs form.RunWithContext(ctx), and
// translates huh.ErrUserAborted to ErrCancelled.
func RunHuhForm(ctx context.Context, form *huh.Form) error
```

Quit/help-slot mechanics inside `ask` (moved verbatim from the raw sites, once):

- `Quit != nil` → `km.Quit = key.NewBinding(key.WithKeys(Keys...), key.WithHelp(joinedKeys, Help))`.
- Help-slot hijack so the quit hint appears in huh's field help line (huh hides the
  form-level Quit binding from field help): select → `km.Select.Filter`, multiselect →
  `km.MultiSelect.Filter`, input → `km.Input.AcceptSuggestion` **plus** enabling
  suggestions via a fake `SuggestionsFunc` returning a single blank suggestion (huh only
  exposes the AcceptSuggestion binding when suggestions are on; the form-level Quit
  handler catches the key first, so the hijacked binding never actually fires). Each
  hijack is applied per field kind present in the form; document the trick where it is
  implemented. Visibility caveat: for a `Filterable: false` multiselect the hijacked
  slot never renders (huh disables and hides the Filter binds) — quit works, hint is
  absent; see design decision 3.
- The Filter-slot hijack presumes the field is not live-filterable (the hijack rebinds
  the Filter activation key). `ask` list fields combining `Quit` with `Filterable:
  true` (or nil-default filterable) and a `"q"` quit key are out of scope — no
  migrated site needs it; see design decision 3.
- `SubmitHelp != ""` → relabel `km.Select.Submit` / `km.MultiSelect.Submit` /
  `km.Input.Next` help as appropriate for the field kinds present (cosmetic only; the
  deploy menu needs the select variant).

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, tests, internals-doc
  updates in this repo.
- **Post-Completion** (no checkboxes): manual interactive verification of the migrated
  prompts; Stage 7 hand-off notes.

## Implementation Steps

### Task 1: `widgets.RunHuhForm` + migrate widgets primitives onto it

**Files:**
- Modify: `internal/core/ui/widgets/huh.go`
- Modify: `internal/core/ui/widgets/confirm.go`
- Modify: `internal/core/ui/widgets/selector.go`
- Modify: `internal/core/ui/widgets/multiselect.go`
- Modify: `internal/core/ui/widgets/huh_test.go`
- Modify: `internal/core/ui/widgets/confirm_test.go` / `selector_test.go` / `multiselect_test.go` (as needed)

- [x] add `RunHuhForm(ctx context.Context, form *huh.Form) error` to `widgets/huh.go`:
      `RunWithPromptHooks` wrap + `form.RunWithContext(ctx)` + `huh.ErrUserAborted →
      ErrCancelled` translation
- [x] route `defaultRunConfirmForm`, `defaultRunConfirmRunForm`, `defaultRunSelectForm`,
      `defaultRunMultiSelectForm` through `RunHuhForm(context.Background(), form)`;
      remove the per-wrapper hook-snapshot blocks in `RunConfirm` / `ConfirmRun` /
      `RunSelector` / `RunMultiSelect` (hooks must fire exactly once — see design
      decision 7); keep a defensive `ErrUserAborted → ErrCancelled` mapping in the
      wrappers for seam-swapped tests
- [x] verify public signatures and `run*FormFn` seams unchanged (`git diff` review)
- [x] write tests for `RunHuhForm`: hook pairing (before/after exactly once, after fires
      on error), abort translation, non-abort error pass-through
- [x] update/verify existing primitive tests still pass (hook-firing expectations may
      move from wrapper to seam-default level)
- [x] run `make test` — must pass before task 2

### Task 2: `ask` declarative overrides — `QuitSpec`, `SubmitHelp`, `ShowHelp`, `Field.Height`/`Filterable`

**Files:**
- Modify: `internal/core/ui/ask/ask.go`
- Modify: `internal/core/ui/ask/ask_test.go`

- [x] add `QuitSpec` and extend `RunOptions` with `Quit *QuitSpec`, `SubmitHelp string`,
      `ShowHelp *bool` (nil = leave huh default — do not change behaviour for existing
      callers)
- [x] add `Field.Height int` (select/multiselect) and `Field.Filterable *bool`
      (multiselect only — huh/v2 `Select` has no `Filterable` method; reject or ignore
      it on other kinds, pick one and test it); apply in `buildHuhField`
- [x] implement keymap assembly in `ask`: `km.Quit` from `QuitSpec`; help-slot hijack
      per field kind (select/multiselect → Filter slot; input → AcceptSuggestion slot +
      fake single-blank `SuggestionsFunc`); `SubmitHelp` relabel; document the hijack
      trick with a comment stating the constraint (huh hides form-level Quit from field
      help)
- [x] write tests: keymap/slot inspection per field kind (select-only form,
      multiselect-only form, input-only form, mixed form), `SubmitHelp` relabel,
      `ShowHelp` tri-state, `Height`/`Filterable` application; for a
      `Filterable: false` multiselect assert the Filter binding ends up disabled (the
      hint-visibility limitation from design decision 3), and assert form-level Quit
      still carries the QuitSpec binding
- [x] write tests for error cases (e.g. `QuitSpec` with empty `Keys` — decide and pin
      behaviour: treat as nil)
- [x] run `make test` — must pass before task 3

### Task 3: Build-vs-run split + `ErrCancelled` contract for `ask`

**Files:**
- Modify: `internal/core/ui/ask/ask.go`
- Modify: `internal/core/ui/ask/ask_test.go`
- Modify: `internal/core/workflow/setup/huh.go` (cancel check at ~line 86)
- Modify: `internal/cli/scaffold/scaffold.go` (~line 320)
- Modify: `internal/cli/command/runbyid.go` (~line 163)
- Modify: `internal/cli/vars/set.go` (~line 244)
- Modify: `internal/cli/vars/browser_test.go` (~line 192)
- Modify: `internal/cli/scaffold/scaffold_test.go` (~line 467)
- Modify: `internal/cli/command/runbyid_test.go` (~line 373)

- [x] introduce `ask.Form` (`huh *huh.Form` + `bindings`), `Build(title, fields, opts)
      (*Form, error)`, `(*Form).Run(ctx) (Result, error)` via `widgets.RunHuhForm`,
      `(*Form).Huh()`, `(*Form).Result()`; rewrite `ask.Run` as `Build` + `Form.Run`;
      drop `ask.Run`'s own `RunWithPromptHooks` wrap (ask.go:166) — `RunHuhForm` wraps
      hooks itself (hooks-fire-once invariant, design decision 7)
- [x] change `ask.Run`/`Form.Run` cancel contract to `widgets.ErrCancelled`; update the
      docstring (currently promises `huh.ErrUserAborted`)
- [x] grep for every `ask.Run` caller checking `huh.ErrUserAborted` and switch to
      `errors.Is(err, widgets.ErrCancelled)` (known: setup/huh.go, scaffold.go,
      runbyid.go, vars/set.go — re-verify with grep); drop now-unused `huh` imports
- [x] update test seam stubs that return `huh.ErrUserAborted` through `ask.Run`-shaped
      seams to return `widgets.ErrCancelled` instead (known: vars/browser_test.go:192,
      scaffold/scaffold_test.go:467, runbyid_test.go:373 — re-verify with grep over
      `*_test.go`), otherwise the flipped cancel checks take the wrong branch
- [x] write tests: `Build` constructs without running; `Result()` harvest after a
      simulated completion (drive bindings directly); `Run` ≡ `Build`+`Run` equivalence
      on the existing `ask_test.go` cases; cancel returns `ErrCancelled`; rename/retarget
      the stale `TestRunUserAbortedError` (ask_test.go:347) to the new contract
- [x] write tests for error cases (`FieldUnknown` still rejected at `Build`; empty
      fields short-circuit preserved)
- [x] run `make test` — must pass before task 4

### Task 4: Migrate `deploy/menu.go` (both selects) onto `ask`

**Files:**
- Modify: `internal/cli/deploy/menu.go`
- Modify: `internal/cli/deploy/menu_test.go` (or nearest existing test file)

- [x] rewrite `selectMenuItemInteractive` as an `ask.Run` call: one `FieldSelect` with
      styled option labels (existing label assembly stays; no `Filterable` — huh
      Select has none, and the Filter-slot hijack itself makes `/` inert, as today),
      `Quit{Keys: q/esc/ctrl+c, Help: "exit"}`, `SubmitHelp: "select"`, `Height:
      max(len+5, 12)`, `Field.Default` = first option (current preselect); map
      `ErrCancelled → (menuExit, nil)` (preserves current esc semantics)
- [x] rewrite `selectDeployServiceInteractive` likewise (`Quit.Help: "back"`) with the
      locked-item rejection moved to `Field.Validate` and **`Field.Default` = first
      non-locked service name** (codex finding: today's picker preselects the first
      non-locked row so initial Enter never hits a locked item — without an explicit
      Default, huh falls back to option 0 and a locked-first list regresses);
      `ErrCancelled` passes through to the caller (back-to-menu semantics preserved)
- [x] delete `deployMenuKeyMap` and the now-dead raw-form plumbing; keep the domain
      formatting helpers (`formatDeployServiceLabel`, `formatServiceMeta`,
      `deployInfoRowsFrom`) untouched
- [x] write/update tests for the pure parts (item defs → field/options mapping, locked
      validate rejection message, cancel mapping, **default selection with items[0]
      locked → items[1] preselected**) — table-driven where natural
- [x] run `make test` — must pass before task 5

### Task 5: Migrate setup wizard port overrides + service toggles onto `ask`

**Files:**
- Modify: `internal/core/workflow/setup/huh.go`
- Modify: `internal/core/workflow/setup/huh_test.go` (or nearest existing test file)

- [x] rewrite `askPortOverrides`: one `ask.Field{Kind: FieldInput}` per conflict (key =
      `service/portName`, default = requested port, `Validate: buildPortValidator()`),
      `Quit{Keys: esc/ctrl+c, Help: "cancel"}`; harvest via `Result.String` into the
      existing `coercePortOverrides`; delete the manual `SuggestionsFunc` hack and the
      NOTE comment explaining non-migratability
- [x] rewrite `askServiceToggles`: `FieldMultiselect` with `Defaults` = initially-enabled
      names, `Filterable: false`, `Quit{esc/ctrl+c, "cancel"}`; keep the "Always on:"
      pre-print and mandatory-merge logic at the call site; delete the raw form + NOTE
      comment. Note: the "esc cancel" help hint will NOT render for this non-filterable
      multiselect (design decision 3 visibility rules) — this matches current behaviour,
      where the existing Filter hijack is already ineffective; esc itself still cancels
      via form-level Quit
- [x] review `setup/help_runtime_test.go` (raw `huh.NewForm` locking the
      AcceptSuggestion help-slot behaviour): fold it into `ask`'s slot tests from
      task 2 or keep it as a huh-behaviour canary — either way it is test-only and
      exempt from the no-raw-forms acceptance grep. Decision: kept as a canary — it
      drives `form.Update` through several cycles to exercise huh's runtime
      `updateSuggestionsMsg` refresh path, which the construction-only `ask` slot tests
      from task 2 do not cover.
- [x] map `ErrCancelled → ErrWizardCanceled` in both (and in `askQuestions`, already
      updated in task 3); drop now-unused `huh` and `charm.land/bubbles/v2/key` imports
      from `setup/huh.go`
- [x] write/update tests for the pure mapping parts (conflicts → fields, toggles →
      field/defaults, mandatory merge, cancel mapping); coercion helpers' existing tests
      must keep passing unchanged
- [x] run `make test` — must pass before task 6

### Task 6: Internals docs — record contracts and close the Stage 1 open question

**Files:**
- Modify: `docs/internals/tui-keymap.md`
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md` (only if a Critical Pattern entry is warranted)

- [x] update `tui-keymap.md` § 7: forms stay on huh's own key routing (registry wiring
      evaluated and declined); quit bindings unified to `q`/`esc`/`ctrl+c` where
      appropriate via `ask.QuitSpec`; note the per-field-kind help-slot mechanism
- [x] update `packages.md` § ask / § widgets: `RunHuhForm` as the single executor,
      hooks-fire-once invariant, `ErrCancelled` canonical cancel contract (incl. the
      `ask.Run` contract change), declarative `QuitSpec` overrides + slot-hijack
      ownership, `Build`/`Run` split as the Stage 7 seam
- [x] run `make build` (embedded docs re-sync) + `make test` — must pass before task 7

### Task 7: Verify acceptance criteria

- [x] spec § Stage 6 deliverables all present: single `RunHuhForm`; scalable keymap
      overrides in `ask.RunOptions`; three raw sites migrated (grep: no `huh.NewForm`
      in **production** code outside `internal/core/ui/ask` + `internal/core/ui/widgets`
      — exclude `*_test.go`; `setup/help_runtime_test.go` is exempt per task 5);
      `deploy/menu.go` duplication removed; `ask` split into build vs run
- [x] behaviour preservation spot-checks per Migration compatibility (esc semantics,
      locked hints, port validation, Always-on line)
- [x] hooks-fire-once invariant verified by test
- [x] run full suite: `make build && make test && make lint`

### Task 8: [Final] Documentation and plan close-out

- [ ] confirm no user-facing `docs/reference/` pages describe form keybindings that
      changed (quit keys are preserved, so expected: none)
- [ ] update `AGENTS.md` Critical Patterns only if implementation surfaced a new trap
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification** (interactive rendering is not unit-tested, per existing
precedent):

- `dwe deploy` (no args, TTY): top menu — esc/q exits cleanly, help line shows
  `q/esc exit` + `enter select`; service picker — esc returns to menu, locked service
  shows the lock hint on enter.
- `dwe setup` wizard in a project with port conflicts: port form shows `esc cancel` in
  the help line, still-occupied port rejected inline; service-toggle form shows
  "Always on:" line, `/` does NOT enter filtering, esc cancels the wizard.
- A `dwe run <cmd>` with params (cmdbrowser force-param-form path): cancel returns to
  shell without running.

**Stage 7 hand-off:**

- `ask.Build` + `Form.Huh()` + `Form.Result()` is the seam: Stage 7 embeds the built
  `huh.Form` as a capturing-overlay child model (never calls `Run()`), watches
  `form.State`, then harvests via `Result()`. esc/ctrl+c arbitration and huh-chrome
  reconciliation inside the bordered modal are Stage 7 scope, not this stage's.

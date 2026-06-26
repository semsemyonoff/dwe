# TUI Stage 1 — Keybinding & Action Design

## Overview

Lock the `internal/core/ui/tui/` action-keymap API that Stage 0 prototyped as a
spike, and produce the **keymap reference doc** that defines the action taxonomy,
default bindings, help sections, and mouse vocabulary across all three full-screen
TUIs (command browser, docs browser, status dashboard) plus the `huh` forms.

This stage is a **gate** before any surface migration (Stages 3–5b). It is
**design-heavy, code-thin**: NO existing TUI is migrated here. The encoded registry
has no production consumer until Stage 3 — it is exercised entirely through the
`tui` package's own tests against the existing stub plugin. "Locked" here scopes to
the **registry/keymap/overlay-input surface**; the `Plugin` interface stays
PINNED-not-frozen through Stage 3 (spec § 7).

Deliverables (per milestone spec § 5, Stage 1 row):

- The finalized/locked `tui` registry API: `Binding` placeholder fields
  (`Aliases`/`Rebindable`/`Mouse`) resolved to real semantics; built-in defaults
  finalized; a framework "stdlib" of shared actions plugins opt into.
- A keymap reference doc (`docs/internals/tui-keymap.md`).
- `docs/internals/packages.md` updated: the `tui` API moves from
  "spike/provisional/not frozen" to "locked in Stage 1".

Problem it solves: today the three TUIs copy-paste imperative `key.Matches` chains
with hand-written help text, no shared action IDs, and divergent bindings
(`home`/`end` vs `g`/`G`). This stage fixes the *contract* so the migrations land
consistent, discoverable, and (eventually) re-bindable bindings onto one registry.

## Context (from discovery)

Files/components involved:

- **`internal/core/ui/tui/registry.go`** — the provisional `Registry`/`Binding`/
  `Action`/`Section` API to finalize. Currently: flat `key → action` `Match`;
  `Binding{Keys, Desc, Section, Aliases, Rebindable, Mouse}` with the last three
  marked "documented placeholders, not consulted in Stage 0"; built-ins
  `Help`/`Quit`/`FocusNext`/`FocusPrev` auto-registered by `NewRegistry`.
- **`internal/core/ui/tui/help.go`** — `buildHelpOverlay` already joins
  `e.Binding.Keys` only (so Aliases are already absent from help output) and
  already reserves the i18n namespace (`tui.help.title`, `tui.help.section.<name>`,
  `tui.help.action.<actionID>`) with English fallbacks via `i18n.NopTranslator`.
- **`internal/core/ui/tui/overlay.go`** + **`plugin.go`** — `Overlay{Content,
  Width, Height}` is defined in `plugin.go`; `overlayStack` + `Composite` operate
  over it in `overlay.go`. The clicks-outside-swallowed policy is a documented
  constant; mutual exclusivity is structural.
- **`internal/core/ui/tui/frame.go`** — owns the Update key-routing + modal-input
  policy (Stage 0: when an overlay is open, plugin action keys are swallowed,
  framework handles help-close/quit). The capturing-overlay routing **decision**
  is added here as a pure helper this stage; full Update rewiring lands with the
  first capturing consumer in Stage 3.
- **Source-of-truth keybinding inventory** (read-only this stage):
  `internal/core/ui/cmdbrowser/{keymap,model}.go`,
  `internal/core/docs/tui/{keys,model}.go`,
  `internal/core/ui/statustui/{keys,tui}.go`,
  `internal/cli/deploy/menu.go`, `internal/core/workflow/setup/huh.go`,
  `internal/core/ui/ask/ask.go`.

Related patterns found:

- Help generation is registry-driven and i18n-ready; the namespace is already
  reserved in code, so Stage 1 *confirms and documents* it rather than inventing it.
- `make build` syncs `docs/` into the embedded tree and regenerates content
  hashes; `packages.md` is embedded, so doc edits require `make build`.
- Stage 1 adds **no** YAML i18n keys: framework help resolves through code-level
  English fallbacks (`i18n.NopTranslator`) and has no production consumer yet, so
  there is nothing to translate. (NB: Stage 0's `help.go` comment attributes this to
  the `ui:` unknown-key validator at `internal/core/validate/config/ui.go` — that is
  imprecise: that validator only checks the `ui.commands` block in `workspace.yml`
  and emits **warnings**, not errors, and is unrelated to the `tui.help.*`
  translation namespace. The decision stands; the rationale is the missing consumer,
  not a hard error. Real keys land later in the i18n translation store + its known-key
  list during the migration stages.)

Dependencies identified: none new. No new imports; the package's importer set is
unchanged (still `core/ui/*` + `cli/` only; this stage adds no importers).

## Development Approach

- **testing approach**: **Regular (code-first)** — same rationale as Stage 0: thin
  code over an API being locked *now*; golden-first against a just-stabilized
  surface is wasteful. Implement each component, then add tests in the same task.
- complete each task fully before moving to the next
- make small, focused changes; the package must build after every task
- **every task includes new/updated tests** (success + error/edge) as separate
  checklist items
- **all tests must pass before starting the next task**
- update this plan when scope changes during implementation
- backward compatibility: trivially preserved — no existing TUI is modified; only
  the `tui` package internals + two `docs/internals/*.md` files change

## Testing Strategy

- **unit tests**: required every task. Registry dispatch (incl. Aliases), stdlib
  registration + dual-bind, duplicate guard, and the capturing-overlay routing
  decision are pure and table-tested.
- **help golden**: `testdata/help_default.golden` re-asserted; a new test proves an
  alias is matched by `Match` but absent from the rendered help body.
- **no e2e**: this package has no UI e2e harness; behaviour is covered by
  view-rendering golden + unit tests (matching Stage 0).
- test commands: focused `go test ./internal/core/ui/tui/...` (no embedded-docs gate
  for this package); full `make test`; lint `make lint`; `make build` after the
  `docs/internals/*.md` edits so the embedded copy regenerates.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep this plan in sync with actual work done

## Solution Overview

The registry stays **flat** (`key → action`, normal mode). Context (focus, mode) is
resolved inside each plugin's `HandleAction`. The single case that bypasses the
registry entirely — input capture (filter typing, inspect) — is modeled as an
`Overlay` carrying a new `CapturesInput bool` flag, building on Stage 0's existing
overlay + modal-input machinery. No scoped/named-context registry is introduced.

Key design decisions (settled in the Stage 1 brainstorm — encode, do not
re-litigate):

- **`Aliases` redefined** vs Stage 0 (which had it backwards). New semantics:
  aliases **dispatch** (wired into `Match`) but are **hidden from help** (muscle-
  memory compatibility without cluttering the modal). `Keys` = canonical: dispatch
  **and** shown in help.
- **`Rebindable` stays metadata only.** No config loader is built (YAGNI; no
  consumer until Stage 3). The reference doc *sketches* a future config schema.
- **`Mouse` fixes the vocabulary only** (`wheel-up`, `wheel-down`, `click`,
  `double-click`); Stage 2 wires it. Not consumed by dispatch this stage.
- **Framework "stdlib" of shared actions** — beyond the auto-registered built-ins
  (`Help`/`Quit`/`FocusNext`/`FocusPrev`), the framework exports shared action
  constants + default `Binding`s that plugins opt into with one call. These are
  **plugin-handled** (framework supplies keys + section only). `Top`/`Bottom`
  dual-bind `["g","home"]` / `["G","end"]`, unifying the cmdbrowser-vs-docs
  divergence with no muscle-memory loss. Stdlib actions are **opt-in** (NOT
  auto-registered).
- **Plugin-local actions stay plugin-owned** (registered in their migration stages,
  documented here only): diagram `[`/`]`/`o`/`y`, language `L`, show-english `e`,
  tab-jump `1`–`5`, skip-confirm `y`, edit-params `e`.
- **Cross-surface `e` overload kept**: `e`=edit (cmdbrowser) vs `e`=show-english
  (docs) — per-plugin registries, no physical conflict. Documented as intentional.

## Technical Details

- `Binding.Aliases` wiring: `Registry.Register` iterates `b.Aliases` and adds each
  to the `keys` map with the same duplicate-key guard as `b.Keys`; `help.go` is
  unchanged (it already renders `Binding.Keys` only). `Match` is unchanged (it reads
  the `keys` map, which now also contains aliases).
- Stdlib: a set of exported `Action` constants (`ActionNavUp`, `ActionNavDown`,
  `ActionNavLeft`, `ActionNavRight`, `ActionSelect`, `ActionFilter`,
  `ActionInspect`, `ActionReload`, `ActionTop`, `ActionBottom`, `ActionPageUp`,
  `ActionPageDown`) + a `RegisterStandard(reg *Registry, actions ...Action) error`
  helper and an unexported-by-default lookup accessor (`standardBinding`, promoted
  to exported `StandardBinding` only if the Stage 3 pilot needs the standalone
  fetch). Plugins call `RegisterStandard(reg, ActionNavUp, ActionNavDown, …)` in
  their `Actions` hook.
- `Overlay.CapturesInput bool`: new field on the Stage 0 `Overlay` type
  (`plugin.go`). A pure helper `routeWhileCapturing(msg tea.Msg) captureDecision`
  (in `frame.go` or `overlay.go`) encodes the contract: while a capturing overlay
  is `Top()`, raw input (incl. printables) routes to the plugin, the registry is
  bypassed, only `ctrl+c` (hard-quit) and `esc` (close overlay) survive as
  framework actions, and `?` does NOT open help. The helper is unit-tested; the full
  `frame.Update` rewiring lands with the Stage 3 filter consumer (depth boundary
  stated so it is not over-built).
- i18n namespace (confirmed, not changed): `tui.help.section.<id>`,
  `tui.help.action.<id>`, `tui.help.title`. English fallbacks in code; no YAML keys
  added this stage.

## What Goes Where

- **Implementation Steps** (`[ ]`): the registry/help/overlay code changes + tests,
  the reference doc, and the `packages.md` update — all in this repo.
- **Post-Completion** (no checkboxes): visual eyeballing deferred to Stage 3 (no
  demo binary); any contract gap surfaced by the Stage 3 pilot may feed one revision
  back per spec § 7.

## Implementation Steps

### Task 1: Finalize `Binding` semantics + wire Aliases into dispatch + lock `esc`

**Files:**
- Modify: `internal/core/ui/tui/registry.go`
- Modify: `internal/core/ui/tui/doc.go`
- Modify: `internal/core/ui/tui/registry_test.go`

- [x] rewrite the `Binding.Aliases` doc comment: aliases **dispatch but are hidden
  from help** (replacing the Stage 0 "shown in help, not dispatched" placeholder
  text); keep `Rebindable` as documented metadata; expand the `Mouse` doc to name
  the locked vocabulary (`wheel-up`/`wheel-down`/`click`/`double-click`, wired in
  Stage 2)
- [x] update the type-level + `Registry` doc comments from "provisional / not
  frozen / finalised in Stage 1" to "locked (Stage 1)"
- [x] **flip the package doc in `doc.go`**: the "# Spike — provisional API" section
  still says "This is a Stage 0 spike … intentionally NOT frozen." Rewrite it to
  state the **registry/keymap/overlay-input surface is locked in Stage 1**, while
  the `Plugin` contract stays **PINNED, not frozen** through Stage 3 (spec § 7);
  keep the Stage 2 (mouse) / Stages 3–5b (migration) forward pointers
- [x] **lock `esc` as a hidden `ActionQuit` alias** in `NewRegistry`
  (`Aliases: []string{"esc"}` on the Quit binding) — cmdbrowser + docs both bind
  esc→quit today; this is the spec's "backwards-compatible alias for muscle memory."
  Because it is an Alias (not a `Keys` entry) it dispatches but stays out of help, so
  `help_default.golden` remains byte-stable (Task 2). Document the **precedence
  rule** in the binding/`doc.go` comment: when an overlay is open the frame's
  modal-input policy consumes `esc` to **close the overlay**; `esc` only reaches
  `ActionQuit` in normal mode (no overlay) — consistent with the `CapturesInput`
  contract (Task 4)
- [x] in `Register`, validate `Keys` **and** `Aliases` in the **pre-commit** pass
  (before any map write) so the "no partial mutation" guarantee holds; then commit
  both into the `keys` map. An alias colliding with any existing key/alias **or with
  the binding's own canonical `Keys`** is an error
- [x] write tests: an alias resolves via `Match`; an alias colliding with an
  existing key/alias returns an error; an alias colliding with the binding's **own**
  canonical key returns an error (no partial mutation — the canonical keys are not
  left committed); canonical `Keys` still match; **`Match("esc")` resolves to
  `ActionQuit`**; the duplicate-action and empty-keys guards still hold
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 2: Help renderer — assert Aliases hidden, lock the golden

**Files:**
- Modify: `internal/core/ui/tui/help_test.go`
- Modify: `internal/core/ui/tui/help.go` (doc only, if needed)
- Verify: `internal/core/ui/tui/testdata/help_default.golden`

- [x] confirm `buildHelpOverlay` renders `Binding.Keys` only (no Aliases) — adjust
  the doc comment in `help.go` to state Aliases are intentionally excluded from
  the modal; no behavioural change expected
- [x] correct the stale `help.go` i18n-namespace comment that attributes the
  no-YAML-keys decision to the `ui:` validator hard-erroring — reword to "code-level
  English fallbacks + no live consumer" (the `ui.go` validator only warns on
  `ui.commands` keys and is unrelated to the `tui.help.*` namespace)
- [x] write a test: a registry entry with an `Aliases` key renders help output that
  contains the canonical `Keys` but NOT the alias string, while `Match(alias)`
  still resolves (locks the dispatch-vs-display split) — assert this concretely for
  the built-in `ActionQuit`: help shows `q` (and `ctrl+c`) but **not** `esc`, while
  `Match("esc")` resolves to `ActionQuit`
- [x] re-run the existing `help_default.golden` assertion; regenerate the golden
  only if the built-in default set/labels changed in Task 3 (else leave byte-stable)
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 3: Framework stdlib of shared actions + opt-in registration helper

**Files:**
- Create: `internal/core/ui/tui/actions.go`
- Create: `internal/core/ui/tui/actions_test.go`
- Modify: `internal/core/ui/tui/registry.go` (section constants if new sections)

- [x] create `actions.go`: exported stdlib `Action` constants (`ActionNavUp`,
  `ActionNavDown`, `ActionNavLeft`, `ActionNavRight`, `ActionSelect`,
  `ActionFilter`, `ActionInspect`, `ActionReload`, `ActionTop`, `ActionBottom`,
  `ActionPageUp`, `ActionPageDown`) with a private `standardBindings` table of
  default `Binding`s (keys + section), marked "framework-supplied defaults, plugin-
  handled"; `Top`/`Bottom` carry both binds (`["g","home"]` / `["G","end"]`)
- [x] add `RegisterStandard(reg *Registry, actions ...Action) error` (registers
  each action's default binding, surfacing the first duplicate-action/key error and
  stopping) and a `standardBinding(a Action) (Binding, bool)` lookup accessor for
  the fetch-then-customize path. **Keep the accessor unexported (`standardBinding`)
  unless the Stage 3 pilot actually needs the standalone lookup**, in which case
  promote it to `StandardBinding` — expose only `RegisterStandard` otherwise (the
  two are not redundant: one registers, one looks up)
- [x] document that stdlib actions are **opt-in** (NOT auto-registered by
  `NewRegistry`, which keeps registering only the framework-handled built-ins) and
  **plugin-handled** (the plugin's `HandleAction` interprets them per its context)
- [x] write tests: `RegisterStandard` populates a registry with the dual-bind
  `Top`/`Bottom` keys both matching; `standardBinding` returns false for an unknown
  action; `RegisterStandard` errors when a requested action collides with a built-in
  or with a previously-registered plugin binding; sections from stdlib appear in the
  expected order
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 4: `CapturesInput` overlay field + routing-decision helper

**Files:**
- Modify: `internal/core/ui/tui/plugin.go` (the `Overlay` type)
- Modify: `internal/core/ui/tui/frame.go` (the routing-decision helper)
- Modify: `internal/core/ui/tui/overlay_test.go` or `frame_test.go`

- [x] add `CapturesInput bool` to the `Overlay` type with a doc comment: a
  capturing overlay routes raw input (incl. printables) to the plugin, bypasses the
  registry, and leaves only `ctrl+c` (hard-quit) + `esc` (close) live; `?` does not
  open help while it is `Top()`
- [x] add a pure helper (e.g. `routeWhileCapturing(msg tea.Msg) captureDecision`)
  encoding that contract as a small enum (`captureSwallowToPlugin` /
  `captureHardQuit` / `captureClose`); do NOT rewire the full `frame.Update` loop —
  document that the integration lands with the Stage 3 filter consumer (depth
  boundary). **Design the signature + return enum to be the exact value
  `frame.Update` will call in Stage 3** (a drop-in, not a parallel helper) so the
  test locks the real contract rather than a throwaway shape
- [x] write tests: a printable key → routes to plugin; `ctrl+c` → hard-quit;
  `esc` → close; `?` → swallowed-to-plugin (NOT help-open) while capturing; a
  non-capturing overlay is unaffected by the helper
- [x] run `go test ./internal/core/ui/tui/...` — must pass before next task

### Task 5: Keymap reference doc

**Files:**
- Create: `docs/internals/tui-keymap.md`

- [x] write the full action taxonomy: framework built-ins (Help/Quit/Focus), the
  stdlib shared set (Nav/Select/Filter/Inspect/Reload/Top/Bottom/Page) with default
  keys + Aliases, and the plugin-local actions per surface (cmdbrowser, docs,
  status) — sourced from the code inventory, not invented
- [x] document help-section ordering **matching the actual `NewRegistry`
  first-seen order — Navigation → General** (FocusNext/FocusPrev register before
  Help/Quit; confirmed by `help_default.golden` and spec § 4) → then plugin sections
  in registration order (Filter, Inspect, View, Tabs). Do NOT reorder the built-in
  registration — Task 2 keeps the goldens byte-stable. Also document the i18n
  namespace
  (`tui.help.section.<id>`, `tui.help.action.<id>`, `tui.help.title`) with the
  note that no YAML keys are added yet (rationale: code-level English fallbacks +
  no live consumer — NOT a validator hard-error; see Context); real keys land in the
  i18n translation store + its known-key list during the migration stages
- [x] document the mouse vocabulary + default mouse bindings (wheel→Nav,
  double-click→Select) and the frame-owned (NOT registry) mouse behaviors
  (click-on-panel→focus, click-on-help-hint→Help, click-on-tab→switch,
  click-outside-modal→swallow)
- [x] record cross-surface decisions: `home`/`end`+`g`/`G` unified via dual-bind;
  existing `pgup`/`pgdn` bindings (docs `pgup`/`b`/`pgdn`/`f`, cmdbrowser
  `pgup`/`pgdown`) unified under `PageUp`/`PageDown`; `e`/`y` overload kept +
  documented as intentional; **`esc` locked as a hidden `ActionQuit` alias** with the
  precedence rule (overlay open → `esc` closes the overlay; normal mode → `esc`
  quits), matching cmdbrowser/docs today and the forms-guidance esc=cancel intent
- [x] record the **forms (Stage 6) guidance** section: desired quit (`q`/`esc`/
  `ctrl+c`) + select bindings for the `huh` sites, as a reference target — explicitly
  noting NO form code changes this stage
- [x] sketch the **future** rebinding config schema (e.g. a keymap block under
  `workspace/ui.yml` keyed by `Action` id) and mark it not-implemented (Rebindable
  is metadata only until a real consumer)
- [x] no test (documentation); correctness verified by the doc-build + review

### Task 6: Update `packages.md` — `tui` API locked

**Files:**
- Modify: `docs/internals/packages.md`

- [x] update the `internal/core/ui/tui/` section: scope "locked in Stage 1" to the
  **registry/keymap/overlay-input surface** (`Binding`/`Action`/`Registry`/stdlib/
  `CapturesInput`) — the `Plugin` interface explicitly **stays PINNED, not frozen**
  through Stage 3 (spec § 7 lets the pilot feed one revision back). Record the
  stdlib-actions concept (opt-in, plugin-handled), the redefined `Aliases` semantics
  (dispatch + hidden-from-help), the `CapturesInput` overlay contract, and the
  confirmed i18n namespace; link to `docs/internals/tui-keymap.md`
- [x] keep the `core/ui` layering rule + `docstui` relocation note intact (Stage 4
  still owns the relocation)
- [x] no test (documentation)

### Task 7: Verify acceptance criteria

- [x] verify every Overview deliverable exists and is exercised by tests (Aliases
  dispatch+hidden, stdlib + dual-bind, CapturesInput routing, reference doc,
  packages.md update)
- [x] verify the package builds: `go build ./internal/core/ui/tui/...`
- [x] verify **no new importers** of `tui` and `core/docs` untouched. Expected
  changed paths: `internal/core/ui/tui/**`, `docs/internals/**`, and — because the
  new `docs/internals/tui-keymap.md` is embedded — the git-tracked
  `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`; the
  embedded tree under `internal/core/docs/embedded/` is gitignored). Commit the
  regenerated `content_hashes_gen.go`
- [x] run focused suite: `go test ./internal/core/ui/tui/...`
- [x] run full suite: `make test`
- [x] run `make lint` — clean (gofmt/goimports/golangci-lint)
- [x] run `make build` so the embedded copy of `packages.md` regenerates (the
  `docs/internals/*.md` edits)

### Task 8: [Final] Finalize documentation + archive plan

- [x] confirm `docs/internals/tui-keymap.md` + the `packages.md` `tui` section read
  coherently together (no stale "provisional/spike" language remaining in either)
- [x] update `CLAUDE.md`/`AGENTS.md` only if a new load-bearing pattern emerged
  (likely not — the registry is internal framework detail, not a project-config
  contract)
- [x] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification** (deferred / optional):
- Eyeball nothing this stage — there is no demo binary and no surface is wired.
  Visual validation of the locked bindings happens during the Stage 3 cmdbrowser
  pilot, when the registry first drives a real surface.

**Feeds into later stages**:
- Stage 2 wires the mouse vocabulary locked here against the Stage 0 hit-test seams.
- Stage 3 (cmdbrowser pilot) is the true test of the contract; per spec § 7 it may
  feed one revision back into the `Plugin`/registry API before it freezes for 4–5b.
  It also brings the first `CapturesInput` consumer (the filter overlay), which
  completes the `frame.Update` integration this stage intentionally deferred.
- Stage 6 (forms unification) consumes the reference doc's forms-guidance section.

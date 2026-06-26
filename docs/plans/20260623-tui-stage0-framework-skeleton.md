# TUI Stage 0 — Framework Skeleton (spike)

## Overview

Build the new `internal/core/ui/tui/` package as a **standalone spike**: the shared
chrome/layout/input layer that the three existing full-screen TUIs (command browser, docs
browser, status dashboard) will later migrate onto. This stage delivers a buildable, fully
tested package with a test harness **without touching any of the three existing TUIs**.

Deliverables (per milestone spec § Stage 0):

- `Frame` — owns screen layout (bordered body region + bottom status line) and composites
  overlays over the body.
- **Panel-geometry model** — single source of truth for outer-vs-inner sizing, border
  ownership, and the overlay coordinate space.
- **Overlay manager** — mutually-exclusive modals, ANSI-aware centred compositing over the
  body, and click hit-testing *scaffolding* (seams only; real mouse lands in Stage 2).
- **`?`-modal help** generated from the action registry.
- **Focus manager** — active-panel tracking + project-styled focus borders.
- **Provisional action-keymap registry** — `action → binding + description + section`,
  help generation, key dispatch. Minimal but marked: alias/rebind/mouse fields exist as
  documented placeholders for Stage 1/2. **The registry API is explicitly not frozen here.**
- **`Plugin` interface** — pinned this stage (lifecycle / resize / message routing / result
  / panels / overlays / actions), proven by a test stub plugin.
- Launch helper owning alt-screen + capability fallbacks (non-TTY error, narrow fallback);
  mouse opt-in plumbed but disabled (Stage 2 seam).

Problem it solves: today the three TUIs copy-paste their chrome (title bar, help footer,
border/focus styling, geometry math) and share no input or layout layer. This stage lays
the reusable foundation so later stages migrate each surface onto one framework.

## Context (from discovery)

- **Spec**: `docs/plans/specs/2026-06-23-tui-framework-milestone.md` — § 4 (architecture,
  Frame layout + ASCII mockups, Plugin contract, geometry, terminal/mouse policy, form
  interop), § 5 (stage table), § 6 (cross-cutting: test matrix, i18n, fallbacks).
- **Palette**: `internal/core/ui/styles/styles.go` exposes `ColorAccent/ColorBorder/
  ColorDanger/ColorMuted/ColorSuccess/ColorText/ColorWarning` accessors. `Frame` uses these
  + `charm.land/lipgloss/v2` only — never v1 `lipgloss.Style` values.
- **Charm v2 stack** confirmed in `go.mod`: `charm.land/bubbletea/v2 v2.0.7`,
  `charm.land/bubbles/v2 v2.1.0`, `charm.land/lipgloss/v2 v2.0.4`.
- **Existing patterns to mirror** (read-only, not modified):
  - `internal/core/ui/cmdbrowser/run.go` — `tea.NewProgram(m)` launch shape.
  - `internal/core/ui/cmdbrowser/palette.go` — `paletteFocusBorder() lipgloss.Style` (v2)
    focus-border pattern.
  - `internal/core/ui/cmdbrowser/border_width_test.go` (`TestModel_FullFrameWidth`) and
    `panel_height_test.go` — established golden/width-assertion test style using
    `lipgloss.Width(line)` per rendered row.
- **i18n**: `internal/shared/i18n` — `Store.T(locale, uiKey, fallback)` +
  `i18n.TranslatorOrNop(*Store)`; help strings resolve through a `Translator` with English
  fallbacks, reserving a framework key namespace.
- **Layering**: `internal/core/ui/` is a sink layer (`docs/internals/packages.md`). `tui`
  lives here because it depends on `core/ui/styles`; it must be importable only from
  `core/ui/*` + `cli/`. This stage adds no importers.

## Development Approach

- **testing approach**: **Regular (code-first)** — chosen because this is a spike with
  intentionally provisional APIs; golden-first against unstable rendering is wasteful.
  Implement each component, then add golden + unit tests in the same task.
- complete each task fully before moving to the next
- make small, focused changes; the package must build after every task
- **every task includes new/updated tests** (success + error/edge), as separate checklist items
- **all tests must pass before starting the next task**
- update this plan when scope changes during implementation
- backward compatibility: trivially preserved — no existing code is modified except the
  final `docs/internals/packages.md` doc update

## Testing Strategy

- **unit tests**: required every task. Geometry, registry, focus, overlay math are pure and
  table-tested. Frame/help/run render through `Model.View()` directly (no `tea.NewProgram`),
  matching the existing cmdbrowser golden style.
- **golden frame tests**: full-frame render at width buckets **60 / 79 / 80 / 99 / 100**,
  exercising odd and even widths, asserting every rendered row is exactly the frame width
  (`lipgloss.Width`) and pinning byte-stable layout where borders allow.
- **help-modal golden**: `?`-modal contents generated from the registry.
- **async-message-preservation seam**: a test asserting an arbitrary `tea.Msg` forwarded
  through `Frame.Update` reaches the stub plugin's `Update`.
- **capability fallbacks**: non-TTY → error before launch; narrow/tiny → fallback signal;
  mouse seam off by default.
- **no e2e**: this package has no UI-based e2e harness; interactive behaviour is covered by
  view-rendering golden tests.
- test commands: focused `go test ./internal/core/ui/tui/...` (no embedded-docs gate needed
  for this package); full `make test`; lint `make lint`.

## Solution Overview

A single `tui` package owning a Bubble Tea `Model` (`Frame`) parameterised by a `Plugin`.
`Frame.Update` recomputes geometry on resize, dispatches keys through the registry (help
toggle / focus / quit), and forwards everything else to the plugin so plugin async messages
survive. `Frame.View() tea.View` composes bordered body panels (focus manager supplies
border styles) + a bottom status line into a string, composites the active overlay (help,
future inspect/filter) centred over the body via the overlay manager, and returns it wrapped
in a `tea.View` whose envelope fields (`AltScreen`, `MouseMode`) the framework owns — the
plugin only ever produces a body **string**. Geometry is computed once and given to the
plugin as **inner** dimensions only. Alt-screen and the (inert) mouse seam live on the
`tea.View`, not as program options; the launch helper (`Run`) owns `tea.NewProgram`
construction, capability fallbacks (non-TTY error, narrow sentinel), and result extraction.
The mouse path is plumbed as an opt-in flag but renders `MouseModeNone` (Stage 2). The
`Plugin` interface is pinned and validated by a test stub plugin.

Key design decisions:

- **Geometry is framework-owned, computed once** — avoids the per-TUI border/width fixes
  that lipgloss v2 forced in cmdbrowser.
- **Registry is provisional but shaped** — alias/rebind/mouse-binding fields exist as
  documented placeholders so Stage 1 can lock the API without restructuring callers.
- **Stub plugin is the contract proof** — born with the interface, reused by every golden
  test; it declares panels, actions, a status segment, and a typed result.
- **No CLI wiring** — the package is exercised entirely through tests; nothing launches it
  in production this stage.

## Technical Details

- Package: `internal/core/ui/tui/` (new). No subpackages this stage (`tui/tree` is Stage 4).
- Core types: `Frame` (the `tea.Model`, `View() tea.View`), `Plugin` (interface with
  `ViewPanel`/`HandleAction`/lifecycle), `Panel{ID, Title, Weight}`/`PanelID`,
  `Region`/`Geometry`, `Action`/`Binding`/`Registry`, `Overlay`/`overlayStack`,
  `focusManager`, `RunOptions`. `Run` returns `(any, error)` (plugin's typed `Result()`
  unchanged — no wrapper type).
- Rendering: `charm.land/lipgloss/v2` + `charm.land/bubbletea/v2`; styles via
  `core/ui/styles` accessors. ANSI-aware width via `lipgloss.Width` / lipgloss helpers.
- i18n: help labels through `i18n.Translator` (English fallbacks); a reserved key namespace
  documented for Stage 1.

## What Goes Where

- **Implementation Steps** (`[ ]`): the entire `tui` package, its tests, and the
  `packages.md` doc update — all achievable in this repo.
- **Post-Completion** (no checkboxes): visual eyeballing of the rendered frame in a real
  terminal (deferred — no demo binary this stage per the chosen test-only harness); feeding
  any interface gaps discovered during the Stage 3 pilot back into the contract.

## Implementation Steps

### Task 1: Package scaffold + panel-geometry model

**Files:**
- Create: `internal/core/ui/tui/doc.go`
- Create: `internal/core/ui/tui/geometry.go`
- Create: `internal/core/ui/tui/geometry_test.go`

- [x] create `doc.go` with the package doc: purpose, layering rule (importable only from
  `core/ui/*` + `cli/`), and the "spike — provisional API" note
- [x] create `geometry.go`: `Region{X, Y, Width, Height}` and `Geometry` computing, from
  terminal `(w, h)`: outer frame region = `w × (h − statusLineRows)`, inner content region
  = outer minus border (1) + padding, and the overlay coordinate space (= inner body region,
  never the status line)
- [x] add `tooNarrow(w, h int) bool` / minimum-size threshold used by the fallback path
- [x] add a pure `layoutPanels(body Region, weights []int) []Region` splitting the body
  horizontally by weight with a **deterministic remainder policy**: widths sum **exactly** to
  the body width (last panel absorbs the remainder, mirroring cmdbrowser's
  `right = total − left` math — never naive `w*weight/sum` per panel, which leaks a column at
  odd widths like 79/99). **Precondition**: weights are non-empty and all positive — the
  caller (`newFrame`, Task 7) validates this before launch, so `layoutPanels` itself has no
  error path (a documented programmer-error precondition, kept pure for `View`'s hot path)
- [x] document border ownership: plugins receive inner dims only; the frame draws the border
- [x] write tests: geometry at width buckets 60/79/80/99/100 (odd + even), asserting
  inner = outer − chrome, overlay region excludes the status line, and `tooNarrow`
  boundaries; `layoutPanels` — outer widths sum exactly to body width at every bucket
  (1-panel, 2-panel equal weights, 2-panel skewed weights), remainder lands deterministically
  on the last panel (the positive-non-empty-weights precondition is enforced by `newFrame`'s
  validation test in Task 7, not here)
- [x] run tests — must pass before next task

### Task 2: Provisional action-keymap registry

**Files:**
- Create: `internal/core/ui/tui/registry.go`
- Create: `internal/core/ui/tui/registry_test.go`

- [x] create `registry.go`: `Action` (string id type), `Binding{Keys []string, Desc,
  Section string}` plus **documented placeholder fields** `Aliases []string` and
  `Rebindable bool` (Stage 1) and a `Mouse` seam field (Stage 2) — all marked provisional
- [x] `Registry` with `Register(Action, Binding) error`, `Match(key string) (Action, bool)`,
  and `Sections() []Section` (ordered, for help generation)
- [x] guard against duplicate action / duplicate key registration (return error)
- [x] add the framework's built-in actions as constants (`ActionHelp`, `ActionQuit`,
  `ActionFocusNext`, `ActionFocusPrev`) with default bindings — marked "defaults, finalised
  in Stage 1"
- [x] write tests: register + match, duplicate detection (key + action), section ordering,
  built-in defaults present
- [x] run tests — must pass before next task

### Task 3: Plugin interface + test stub plugin (contract proof)

**Files:**
- Create: `internal/core/ui/tui/plugin.go`
- Create: `internal/core/ui/tui/stub_test.go`
- Create: `internal/core/ui/tui/plugin_test.go`

- [x] create `plugin.go`: the `Plugin` interface covering
  - **lifecycle** — `Init() tea.Cmd`, `Close() error`
  - **resize** — `Resize(body Region)` (overall inner body region for plugins that cache)
  - **message routing** — `Update(tea.Msg) tea.Cmd`
  - **per-panel view** — `ViewPanel(id PanelID, inner Region) string`: the plugin renders
    **each panel's body content** into the region the framework computed for it; the
    framework draws the borders/focus around it. (Replaces a single `View() string` so
    frame-owned multi-panel layout is actually expressible — cmdbrowser = tree+list, docs =
    tree+viewport. A single-region plugin like status just declares one panel.)
  - **panel layout** — `Panels() []Panel` where `Panel{ID PanelID, Title string, Weight int}`
    (horizontal split weight); the framework lays panels out left→right by weight and
    computes each inner region
  - **status segment** — `StatusContext() string`
  - **actions** — `Actions(*Registry) error` registration hook (returns the registry's
    duplicate-action/key error so `newFrame`/`Run` can fail **before** launch) **and**
    `HandleAction(a Action) (tea.Cmd, bool)`: the framework, after `Registry.Match`, calls
    `HandleAction`; the bool reports whether the plugin handled it (built-in help/focus/quit
    are framework-handled and never reach the plugin)
  - **overlay request** — `PendingOverlay() (Overlay, bool)`
  - **result** — `Result() any` (typed plugin result, returned unchanged by `Run`)
- [x] define the shared value types **here** (Go has no forward declarations): `PanelID`,
  `Panel`, and `Overlay{Content string, Width, Height int}` — Task 5 adds the
  `overlayStack` + `Composite` **over this type**, it does not redefine it. (`Region` already
  exists from Task 1; `Action` from Task 2.) Document the contract as pinned-not-frozen.
- [x] document the View contract split: the plugin returns panel **strings**; only `Frame`
  (the `tea.Model`) returns `tea.View` and owns its envelope fields (`AltScreen`,
  `MouseMode`, `Cursor`) — Task 7. (In `bubbletea/v2 v2.0.7` `Model.View()` returns
  `tea.View`, not a string; see `cmdbrowser/model.go:390`, `statustui/tui.go:319`.)
- [x] create `stub_test.go`: a minimal `stubPlugin` implementing every method — declares two
  weighted panels, registers a couple of actions with a `HandleAction` that records the
  invoked action, returns a fixed status context, records every `Update` msg it receives
  (for the async-preservation test), and exposes a typed result
- [x] write `plugin_test.go`: assert `stubPlugin` satisfies `Plugin`; assert lifecycle
  ordering (`Init` → updates → `Close`); assert `Actions` populates a registry and a matched
  plugin action routes through `HandleAction`
- [x] run tests — must pass before next task

### Task 4: Focus manager

**Files:**
- Create: `internal/core/ui/tui/focus.go`
- Create: `internal/core/ui/tui/focus_test.go`
- Create: `internal/core/ui/tui/palette.go`

- [x] create `palette.go`: `focusedBorder()` / `unfocusedBorder()` returning v2
  `lipgloss.Style` built from `styles.ColorBorder()` / `styles.ColorAccent()` accessors —
  the single styling bridge for panel borders (no v1 styles)
- [x] create `focus.go`: `focusManager` tracking the active `PanelID` across the plugin's
  declared panels, with `Next()`, `Prev()`, `Set(PanelID)`, `Active() PanelID`, and
  `BorderFor(PanelID) lipgloss.Style`
- [x] handle the zero/one-panel cases (no-op cycling) and unknown PanelID
- [x] write tests: cycle Next/Prev wraps correctly, Set to known/unknown id, BorderFor
  returns focused style only for the active panel
- [x] run tests — must pass before next task

### Task 5: Overlay manager + ANSI-aware centred compositing

**Files:**
- Create: `internal/core/ui/tui/overlay.go`
- Create: `internal/core/ui/tui/overlay_test.go`

- [x] **discovery first**: evaluate lipgloss v2's built-in `NewLayer(content).X().Y().Z()`
  + `NewCompositor(layers...).Render()` + `Compositor.Hit(x,y) LayerHit` (`lipgloss/v2
  @v2.0.4/layer.go:206`) as the compositing/hit-test substrate — adopt it rather than
  hand-rolling ANSI cell math (the spec's own § 4/§ 7 width-semantics risk). Record the
  decision in `overlay.go`; only hand-roll if a concrete gap (e.g. dimming) forces it, and
  document why
- [x] create `overlay.go`: an `overlayStack` over the **Task 3 `Overlay` type** (do not
  redefine it) with `Push`/`Pop`/`Top`/`Empty` enforcing **mutual exclusivity** (one visible
  modal). Add methods to `Overlay` here if needed — never a second type definition
- [x] `Composite(base string, ov Overlay, body Region) string` — centre the overlay over the
  body region via the lipgloss `Compositor` (base layer + centred overlay layer)
- [x] **pin the dimming strategy**: dimming applies to the **entire body region only** (the
  body string is re-rendered through a muted style before the overlay layer is placed); the
  **status line is composed after/outside `Composite` and is never dimmed**. Document that
  `Composite` receives only the body string, not the full frame, so it structurally cannot
  touch the status row
- [x] encode the **clicks-outside-swallowed-not-dismissed** policy as a documented constant
  + a single Stage-2 seam — do **not** build a bespoke zone classifier; Stage 2's mouse
  layer chooses the hit-test mechanism (expected: `Compositor.Hit`/`LayerHit.Bounds()`)
- [x] write tests: centring math at width buckets, ANSI-width safety (styled base preserved),
  the body **is dimmed** beneath the overlay, the overlay does **not** change the body
  region's total dimensions, stack mutual-exclusion. (The "status line not dimmed" assertion
  lives in Task 7, where the status line and body are composed together.)
- [x] run tests — must pass before next task

### Task 6: `?`-modal help generated from the registry

**Files:**
- Create: `internal/core/ui/tui/help.go`
- Create: `internal/core/ui/tui/help_test.go`
- Create: `internal/core/ui/tui/testdata/help_default.golden`

- [x] create `help.go`: `buildHelpOverlay(reg *Registry, tr i18n.Translator, locale string,
  width int) Overlay` rendering the registry's sections/bindings into the modal body
  (section label · keys · description rows, per the spec mockup), width-aware. `locale` is
  required because `Translator.T(locale, uiKey, fallback)` takes it
  (`internal/shared/i18n/store.go:65`); Stage 0 may pass `i18n.TranslatorOrNop(nil)` + a
  fixed locale and defer real wiring to the migration stages
- [x] resolve section/label strings through the `Translator` with English fallbacks; reserve
  and document the framework i18n key namespace (e.g. `tui.help.section.*`). **Defer the
  final namespace decision to Stage 1** (where keys are locked); for Stage 0, English
  fallbacks via `TranslatorOrNop` mean no YAML keys are added yet, so the `ui:` unknown-key
  validator (`internal/core/validate/config/ui.go:74`) is not triggered — note this so the
  migration stages register the namespace before adding real keys
- [x] write `help_test.go`: golden help-modal contents for the built-in registry; assert the
  help body fits within the body region width
- [x] add `testdata/help_default.golden`
- [x] run tests — must pass before next task

### Task 7: `Frame` assembly — Update/View loop, status line, message routing

**Files:**
- Create: `internal/core/ui/tui/frame.go`
- Create: `internal/core/ui/tui/frame_test.go`
- Create: `internal/core/ui/tui/testdata/frame_60.golden`
- Create: `internal/core/ui/tui/testdata/frame_79.golden`
- Create: `internal/core/ui/tui/testdata/frame_80.golden`
- Create: `internal/core/ui/tui/testdata/frame_99.golden`
- Create: `internal/core/ui/tui/testdata/frame_100.golden`
- Create: `internal/core/ui/tui/testdata/frame_help_open.golden`

- [x] create `frame.go`: `Frame` (the `tea.Model`) holding the plugin, registry, focus
  manager, overlay stack, geometry, and brand/project strings; constructed via
  `newFrame(Plugin, ...opt) (*Frame, error)`. Options carry a **private** `frameOptions{mouse
  bool, brand, project string}` — defined **here**, not `RunOptions` (Task 8), so the package
  builds after this task in isolation; Task 8 maps `RunOptions` into `frameOptions`
- [x] `newFrame` validates construction **before** launch and returns an error on: a
  duplicate action/key (`plugin.Actions(registry)` error) **and** invalid panel layout
  (`Panels()` empty or any non-positive `Weight`). Both fail at construction, never at `View`
- [x] `Init() tea.Cmd`: delegate to `plugin.Init()` (so plugin startup commands run)
- [x] `Update`: `tea.WindowSizeMsg` → recompute geometry + call `plugin.Resize(body)`;
  **modal input policy** — when an overlay is open, key msgs are handled by the framework
  (built-in help-close / quit) and plugin **action keys are swallowed**, NOT routed to
  `plugin.HandleAction` (no acting "behind" the modal); when no overlay is open, key msgs →
  registry `Match` → built-in handled by framework, else `plugin.HandleAction(a)` and, if
  unhandled, forward the raw msg to `plugin.Update`; **all non-key msgs are always forwarded
  to `plugin.Update`** (async preservation — including while the help overlay is open);
  drain `plugin.PendingOverlay()` after **both** a `HandleAction` call and a `plugin.Update`
  forward, so an action-triggered overlay appears immediately. **Batch** framework + plugin
  commands via `tea.Batch`
- [x] `View() tea.View`: `layoutPanels` (Task 1) splits the body into per-panel **outer**
  regions from `Panels()` weights (already validated in `newFrame`); derive each panel's
  **inner** region (subtract border/padding once) and pass it to `plugin.ViewPanel(id, inner)`
  — the single outer→inner subtraction avoids double-counting the border; set the focus
  manager's border style (focused vs unfocused) with explicit `.Width(outer.Width).
  Height(outer.Height)` so each bordered panel renders at its allocated outer size, then
  `JoinHorizontal` the bordered panels into the body;
  `Composite` the active overlay over the body; then append the bottom status
  line (brand+project left · plugin `StatusContext()` middle · `? help` right, width-aware
  truncation) **outside** `Composite` so it is never dimmed; return the whole thing wrapped
  in a `tea.View` whose envelope fields the **framework** owns
- [x] set `v.AltScreen = true` on the returned `tea.View` (alt-screen is a `tea.View` field
  in `bubbletea/v2 v2.0.7`, **not** a program option — see `cmdbrowser/model.go:432`)
- [x] **mouse seam** in `View`: gate `v.MouseMode` on the private `frameOptions.mouse` flag
  but hardcode `tea.MouseModeNone` this stage with a `// Stage 2` marker; on the message
  side, `tea.MouseMsg` is forwarded/ignored (also `// Stage 2`)
- [x] write `frame_test.go`: golden full-frame at 60/79/80/99/100 asserting every row ==
  frame width via `lipgloss.Width` on `View().Content`; **rendered row count == terminal
  height** (no overflow) and the **status line is the final row**; status-line three-zone
  layout + middle truncation; help-open golden (overlay composited, status line still
  visible **and not dimmed**, total frame dimensions unchanged); **async-preservation test**
  (arbitrary `tea.Msg` through `Frame.Update` reaches `stubPlugin.Update`) — including a
  variant **with the help overlay open** (async still flows); **plugin-action dispatch test**
  (a plugin action key routes through `HandleAction`, a built-in key does not); **modal-input
  test** (a plugin action key is **swallowed** while help is open and never reaches
  `HandleAction`); **construction-error test** (`newFrame` with a stub registering a
  duplicate action/key returns an error and never launches); resize propagates the body
  region to the plugin; assert `Frame.View().AltScreen == true` and `MouseMode ==
  tea.MouseModeNone` (testable without a real terminal, as `cmdbrowser_test.go:168` does)
- [x] add the six `testdata/*.golden` files
- [x] run tests — must pass before next task

### Task 8: Launch helper + capability fallbacks (alt-screen, non-TTY, narrow)

**Files:**
- Create: `internal/core/ui/tui/run.go`
- Create: `internal/core/ui/tui/run_test.go`

- [ ] create `run.go`: `RunOptions{Brand, Project string, Mouse bool, /* test seams */
  input, output, isTTY, size}` and `Run(p Plugin, opts RunOptions) (any, error)` — the
  result is `Plugin.Result()` returned **unchanged** (typed result preserved; no wrapper
  type, matching Task 3's `Result() any`). `Run` is the **sole exported entry point** this
  stage (unexported `newFrame` stays internal; no public constructor is added because Stage 0
  has no importers). `Run` constructs `tea.NewProgram(frame, opts...)` appending
  `tea.WithInput(opts.input)` / `tea.WithOutput(opts.output)` **only when each seam is
  non-nil** — in bubbletea/v2 `WithInput(nil)` *disables* input, so a zero-value `RunOptions`
  must fall through to the default stdin/stdout, NOT pass nil. Alt-screen and mouse are
  **not** program options in v2 — they are owned by `Frame.View` (Task 7), fed via
  `frameOptions` mapped from `RunOptions`
- [ ] `Run` builds the frame via `newFrame(p, ...)` and **returns its error** before any
  program start (duplicate action/key surfaces here, not at runtime)
- [ ] **cleanup**: defer teardown so `plugin.Close()` runs on normal quit AND error/interrupt
  paths. `Close() error` must be handled errcheck-safely (repo runs `errcheck`): use a named
  return that surfaces the `Close` error **only when the program returned no error**, else
  explicitly `_ = plugin.Close()`. Document the chosen policy. Return the program error
  wrapped, else `plugin.Result()`
- [ ] non-TTY → return a typed error **before** launch (no program start, no plugin Init)
- [ ] narrow/tiny terminal (`tooNarrow`) → return a `ErrTooNarrow`/fallback sentinel so a
  caller can drop to a plain selector (the fallback UI itself is a later stage)
- [ ] thread `RunOptions.Mouse` into the `Frame` so its `View` mouse-seam reads it; the flag
  stays inert (`MouseModeNone`) this stage — default false
- [ ] inject TTY/size/input/output via options so the paths are testable without a real
  terminal; route the actual program run through a tiny package-private seam (e.g.
  `runProgram func(*tea.Program) (tea.Model, error)`, default `(*tea.Program).Run`) so the
  zero-value-input, mouse-flag, and close-error-precedence tests are deterministic without
  spinning a real event loop
- [ ] write tests: non-TTY path returns the typed error and never starts a program (and never
  calls `plugin.Init`); narrow path returns the fallback sentinel; **zero-value `RunOptions`
  does not disable input** (no `WithInput(nil)` — keyboard still live); `Mouse` flag reaches
  the frame (via `frameOptions`) but renders `MouseModeNone`; `plugin.Close()` runs on the
  error path **and** close-error precedence holds (Close error surfaces only when the program
  returned no error) (the alt-screen/mouse-field assertions live in Task 7's `frame_test.go`)
- [ ] run tests — must pass before next task

### Task 9: Verify acceptance criteria

- [ ] verify all Overview deliverables exist and are exercised by tests
- [ ] verify the package builds: `go build ./internal/core/ui/tui/...`
- [ ] verify **no v1** `github.com/charmbracelet/lipgloss` in the new package's **direct**
  imports (NOT `-deps` — that is transitive and will legitimately show v1 via
  `core/ui/styles`, which imports v1 while exposing raw color accessors). Check direct
  imports only: `go list -f '{{join .Imports "\n"}}' ./internal/core/ui/tui/... | sort -u |
  grep -E 'github.com/charmbracelet/lipgloss$|internal/core/ui/cmdbrowser|internal/core/docs/tui|internal/core/ui/statustui'`
  returns nothing (a bare `rg '"github.com/charmbracelet/lipgloss"' internal/core/ui/tui` is
  an equivalent direct-source check)
- [ ] verify `core/docs` is untouched (`git status` shows no changes there)
- [ ] run focused suite: `go test ./internal/core/ui/tui/...`
- [ ] run full suite: `make test`
- [ ] run `make lint` — clean (gofmt/goimports/golangci-lint)
- [ ] confirm golden buckets 60/79/80/99/100 + help-open + async-preservation all present
  and passing

### Task 10: [Final] Update documentation

**Files:**
- Modify: `docs/internals/packages.md`

- [ ] add a `internal/core/ui/tui/` section: the package contract (Frame, geometry model,
  overlay manager, focus manager, provisional registry, `Plugin` interface), the
  outer-vs-inner geometry rule, and the "spike — API finalised in Stage 1" caveat
- [ ] record the `core/ui` layering rule: `tui` importable only from `core/ui/*` + `cli/`;
  note the planned `docstui` relocation (Stage 4) that keeps `core/docs` free of `core/ui`
- [ ] run `make build` so the embedded-docs copy of `packages.md` is regenerated
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification** (deferred / optional):
- Eyeball the rendered frame in a real terminal. This stage ships a **test-only** harness
  (no demo binary), so visual checks happen during the Stage 3 cmdbrowser pilot when the
  framework is first wired to a real surface.

**Feeds into later stages**:
- Any gaps in the `Plugin` interface surfaced by the Stage 3 pilot may feed one revision
  back into the contract before it is frozen for Stages 4–5b (per spec § 7).
- Stage 1 locks the registry API (aliases, rebinding, mouse bindings) prototyped here.
- Stage 2 implements the mouse layer against the hit-testing scaffolding/seams left here.

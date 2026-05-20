# Interactive TUI command browser for `devbox commands run` / `inspect`

## Overview

Replace the flat `huh.NewSelect` list in `defaultSelectCommand` (`internal/command/command_cmd.go:238`) with a two-panel bubbletea TUI:

- **Left panel**: collapsible tree of command groups (`db`, `services.main`, `services.main.cs`, …) with per-subtree counts.
- **Right panel**: `bubbles/v2/list.Model` of commands in the selected group, with a type badge (`shell` / `script` / `workflow` / `service_run` / `service_exec` / `builtin`) and description.
- **Fuzzy filter** (`/`) with `M/N` per-group match counts and auto-collapse of empty subtrees.
- **Inline inspect** (`i`) — `bubbles/v2/viewport` overlay reusing the existing `printCommandInspect` renderer.
- **Skip-confirm toggle** (`y`) surfaced in the footer.

Goals: cut selection time ≥3× on 30+ command projects; never break existing `ui.RunSelector` call sites (service-toggle, deploy-target stay flat); degrade gracefully to `huh.NewSelect` on narrow but still-TTY terminals (<60 cols or <15 rows). **Non-TTY is not a cmdbrowser fallback target** — `huh` itself requires a TTY, and the call sites at `command_cmd.go:102` / `:161` already short-circuit non-interactive invocations with the existing `"no exact command ID given..."` error before cmdbrowser is reached.

Full design lives in the source spec attached to this plan; this document is the execution surface.

## Context (from discovery)

- **Replacement target**: `defaultSelectCommand` (`internal/command/command_cmd.go:238`), invoked at lines `101` and `160` through the `selectCommandFn` indirection — that indirection is already the test injection point.
- **Inspect renderer**: `printCommandInspect(w io.Writer, def)` at `internal/command/command_cmd.go:425` — already writes to an `io.Writer`, so a `bytes.Buffer` works directly inside a viewport.
- **Existing selector**: `internal/ui/selector.go:80 RunSelector`. Has the `SetHuhHooks` snapshot/restore dance and `ErrCancelled` sentinel — re-use for fallback and copy the hook pattern.
- **Hook contract** (`internal/ui/ui.go`): `SetHuhHooks(before, after)` pauses any active `LiveLine` for the duration of interactive UI. `IsInteractiveFn(stdin)` gates TTY detection. **`SetHuhHooks` invariant**: must run before `tea.NewProgram(...).Run()`, restored via `defer`.
- **Tree source**: `resolveCommandID` (`internal/command/command_cmd.go:260`) passes only filtered `[]*CommandDef` to the selector — no `*GroupNode`, no registry. `cmdbrowser` therefore builds its tree internally by splitting each `Item.ID` on `.`, staying decoupled from `usercommands.GroupNode`.
- **Dep versions**: `charm.land/bubbletea/v2 v2.0.5`, `charm.land/bubbles/v2 v2.1.0`, `charm.land/lipgloss/v2 v2.0.3` — already in `go.mod`. **No new external dependencies.**
- **Config**: lenient (non-strict) loaders apply to `devbox.yml`, `info.yml`, `styles.yml`, `docker.yml`, `localconfig`. The new `ui:` block goes in `devbox.yml` and follows the same lenient pattern — unknown keys are warnings, not errors. Schema details for `UIConfig` / `UICommandsConfig` per §9 of the source spec.
- **LiveLine invariants** (`internal/liveui/liveline.go`) only forbid `tea.NewProgram` *inside* an active LiveLine. Because the hook dance pauses LiveLine before the TUI starts, there is no conflict; do **not** call `term.MakeRaw` manually.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task:
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions / methods (success + error / edge scenarios)
  - update existing test cases when behaviour changes
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test` after each change; run `make lint` once at the end of each task
- Maintain backward compatibility: `ui.RunSelector`, `SetHuhHooks`, `ErrCancelled`, `selectCommandFn` signature, and the on-disk YAML schema for `devbox.yml` (existing files without `ui:` must still load)

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach above).
- **Snapshot tests for `View()` output**: golden files in `internal/ui/cmdbrowser/testdata/*.snap` covering: initial render, focus-right, filter active, inspect open, narrow fallback (60–79 single-panel). **No non-TTY snapshot** — cmdbrowser does not render in that case (returns `ErrCancelled` defensively; production calls are short-circuited at the call site). Drive `m.Update(tea.WindowSizeMsg{...})` then `m.View()` **directly** — never via `tea.NewProgram(...).Run()` (sidesteps the entire goroutine surface). Pin lipgloss/v2 colour profile to ASCII in `TestMain` so snapshots are byte-identical across local and CI.
- **Goroutine leaks**: `goleak.VerifyTestMain(m)` in `cmdbrowser_test.go` — bubbletea's internal goroutines must exit cleanly after `Run()` returns.
- **Parallel cases**: independent table-driven subtests call `t.Parallel()`; subtest names are lowercase phrases.
- **Table-driven `Update(tea.KeyMsg)` tests**: each hotkey in each focus mode → transition + emitted `tea.Cmd`. Roughly 28 cases (7 hotkeys × 4 focus modes); skip combinations the keymap rejects.
- **Fuzz / property test** for the tree: invariant *“group count = sum of child counts + matching leaves”*; *“collapse is idempotent”*.
- **Integration test in `internal/command`**: reuse the existing `selectCommandFn` injection point (already covered for `huh` in `selector_test.go`) to assert the wire-up.
- **Non-TTY smoke test**: with `IsInteractiveFn = false` the call sites at `command_cmd.go:102` and `:161` replace the selector with an error closure before cmdbrowser is reached — assert via the same injection point that the existing error message is returned, unchanged. With width < 60 (still a TTY), cmdbrowser delegates to `ui.RunSelector`.
- **No e2e suite**: project has no Playwright/Cypress-style UI tests; bubbletea snapshots are the equivalent.

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues / blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase — code, tests, docs.
- **Post-Completion** (no checkboxes): manual verification, screenshots, downstream actions.
- **Checkbox placement**: only inside `### Task N:` sections.

## Implementation Steps

### Task 1: Config schema — `ui.commands` block in `devbox.yml` + validator

- [x] add `UIConfig` and `UICommandsConfig` structs to a new `internal/config/ui.go` with YAML tags (`default_expanded_depth`, `auto_collapse_empty`, `show_type_badges`)
- [x] **boolean fields use `*bool`, not `bool`** — with plain `bool`, an absent YAML key and an explicit `false` both deserialise to `false`, so users cannot opt out of a true-by-default setting. Use `AutoCollapseEmpty *bool` and `ShowTypeBadges *bool` so the accessors can distinguish nil (unset → default true) from `&false` (explicit opt-out). Same pattern as `DeployConfig.Log *bool` already in this package.
- [x] wire `UI UIConfig \`yaml:"ui"\`` into `DevboxConfig` in `internal/config/devbox.go`
- [x] add nil-safe accessors `UICommandsDefaultDepth(cfg)` (default 3), `UICommandsAutoCollapseEmpty(cfg)` (default true when nil, otherwise the stored value), `UICommandsShowTypeBadges(cfg)` (default true when nil, otherwise the stored value) — mirror the `DevboxBin` / `DockerBin` / `ShellBin` pattern; clamp negative depth to 0
- [x] keep the existing lenient loader behaviour for `devbox.yml`: an absent `ui:` block and unknown keys under `ui.*` are silently ignored (matches the rest of `devbox.yml`); validator below is the warning channel
- [x] add a `ui` validator in `internal/validate/config/` that registers via the existing `All()` aggregator and reports: negative `default_expanded_depth` (error), unknown keys under `ui.commands.*` (warning) — surfaces explicit feedback the loader can't
- [x] update `docs/reference/config/devbox.md` (or add `docs/reference/config/ui.md` and cross-link from `devbox.md`) with the new block and defaults; **document the `*bool` semantics** (omit key = default true; `false` = explicit opt-out) since this is the only knob in `devbox.yml` where the distinction is observable
- [x] write tests for `UI*` accessors covering all four `*bool` states: nil cfg, missing block, present block with `nil` field (default applies), present block with `&true`, present block with `&false` (explicit opt-out honoured); plus negative `default_expanded_depth` clamps to 0
- [x] write tests for the `ui` validator: clean config, negative depth (error diagnostic), unknown key (warning diagnostic)
- [x] run `make test` and `make lint` — must pass before Task 2

### Task 2: `cmdbrowser` package skeleton + caller wiring (NO `ui` facade — would cycle)

- [x] create `internal/ui/cmdbrowser/` with `model.go`, `keymap.go`, `run.go`, `fallback.go`
- [x] define **non-stuttering** types in `run.go`:
  - `Item { ID, Description, Type string; Private bool; Inspect string }` — `Inspect` is **precomputed** by the caller; cmdbrowser is decoupled from `usercommands.CommandDef` and from `command.printCommandInspect` (which is unexported and unreachable due to a `command → ui` import edge — see Inspect rendering below)
  - `Options { DefaultExpandedDepth int; AutoCollapseEmpty, ShowTypeBadges, IncludePrivate bool; Mode Mode }`
  - `Result { Idx int; Action Action; SkipConfirm bool }` — single struct return; extending Result later (e.g. an `edit` intent) is additive
  - `Action` enum with `ActionUnknown` at iota 0, then `ActionRun`, `ActionInspect`
  - `Mode` enum with `ModeUnknown` at iota 0, then `ModeRun`, `ModeInspect`
- [x] **`Options` carries already-resolved values; no `applyDefaults` for int/bool fields** — promoting a zero `int` to `3` or a `false` bool to `true` would silently overwrite the legitimate values `default_expanded_depth: 0` (all collapsed) and `auto_collapse_empty: false` / `show_type_badges: false` (explicit opt-outs). The config accessors `config.UICommandsDefaultDepth(cfg)` / `…AutoCollapseEmpty(cfg)` / `…ShowTypeBadges(cfg)` already resolve `nil`/missing to the spec defaults using the `*bool` tri-state from Task 1 — the call site passes their concrete returned values straight into `Options`. The **only** field with a meaningful zero-as-sentinel is `Mode`: `ModeUnknown` (iota 0) is by design "unset", and `(*Options).applyDefaults()` promotes it to `ModeRun`. No other field is auto-defaulted.
- [x] add `cmdbrowser.DefaultOptions() Options` factory returning the spec defaults (`DefaultExpandedDepth=3, AutoCollapseEmpty=true, ShowTypeBadges=true, IncludePrivate=false, Mode=ModeRun`) — for callers that don't have a `*config.DevboxConfig` (tests, future programmatic callers). Document that `Options{}` is NOT a "useful zero" by design; callers must either go through the config accessors or use `DefaultOptions()`.
- [x] implement `cmdbrowser.Run(title string, items []Item, opts Options) (Result, error)` as the **single** public entry point. **Do NOT add a facade in `internal/ui`** — that would create a cycle (`ui → cmdbrowser → ui`). Callers (the `internal/command` package) import `internal/ui/cmdbrowser` directly. The "any prompt-like UI must go through `ui.Run*`" rule is satisfied in spirit: `cmdbrowser.Run` runs the same huh-hook dance, just from a sibling package.
- [x] **add `ui.RunWithPromptHooks(fn func() error) error`** in `internal/ui/huh.go` — a new exported helper that snapshots `(before, after)` via the existing unexported `snapshotHuhHooks`, runs `before()`, `defer after()`, then calls `fn()`. This encapsulates the hook dance for full-screen UI like cmdbrowser and replaces ad-hoc use of `SnapshotHuhHooks`, whose docstring at `internal/ui/huh.go:56-58` explicitly says it is for cross-package **tests**, not production. Keep `SnapshotHuhHooks` as-is for the pipeline tests.
- [x] inside `cmdbrowser.Run`: call `ui.RunWithPromptHooks(func() error { _, err := prog.Run(); return err })` so the hooks fire around the bubbletea program — production use of the canonical helper, not the test-only `SnapshotHuhHooks` path.
- [x] **add cmdbrowser-local test seams** in `fallback.go` so tests can drive the fallback ladder deterministically (the real `term.IsTerminal`, real terminal size, and `ui.RunSelector` cannot be stubbed from `internal/ui/cmdbrowser` — `ui.runSelectFormFn` is package-private to `internal/ui`):
  ```go
  var (
      isTerminalFn   = func() bool { return term.IsTerminal(os.Stdout.Fd()) }
      terminalSizeFn = func() (w, h int, err error) { return term.GetSize(os.Stdout.Fd()) }
      runSelectorFn  = ui.RunSelector
  )
  ```
  Tests reassign these vars at the top of the case and restore via `t.Cleanup(func() { isTerminalFn = origIsTerm; ... })`. Production code calls the function variables, never the imports directly.
- [x] TTY gating in `fallback.go`: use `isTerminalFn()`. If false → return `ui.ErrCancelled` defensively; the call sites at `command_cmd.go:102` and `:161` already replace the selector with an error closure before calling `resolveCommandID`, so this branch is defence-in-depth, not the production path. **Note: a non-TTY → `RunSelector` fallback is NOT viable** — `RunSelector` itself uses `huh` and requires a TTY.
- [x] narrow-terminal gating: read width/height via `terminalSizeFn()`. **If `terminalSizeFn` returns an error** (rare on a real TTY — usually a weird `ioctl` failure or a programmatic stdout) → delegate to `runSelectorFn` (same as the narrow path); do not guess a default size and do not surface the size error to the user. If size returns cleanly and `width < 60` OR `height < 15` (and `isTerminalFn()` is true) → delegate to `runSelectorFn(title, ...)` and map its `(idx, err)` to `(Result{Idx: idx, Action: ActionRun}, err)`
- [x] implement a minimal `tea.Model` with two empty bordered panels, alt-screen via `tea.NewProgram(m, tea.WithAltScreen())`; `q` / `Esc` / `Ctrl+C` returns `ui.ErrCancelled` (map `tea.ErrInterrupted` to it)
- [x] add `var _ tea.Model = (*Model)(nil)` next to the type to lock the contract — bubbletea v2 is still maturing
- [x] **Inspect rendering wiring**: in `internal/command/command_cmd.go`'s `defaultSelectCommand`, when building `[]cmdbrowser.Item`, render each def's inspect content into a string and stuff it into `Item.Inspect`:
  ```go
  var buf bytes.Buffer
  printCommandInspect(&buf, def)
  items[i].Inspect = buf.String()
  ```
  This keeps `printCommandInspect` unexported in `internal/command` (no API leak) and avoids the `command → ui → cmdbrowser → command` cycle that would form if cmdbrowser tried to call it directly.
- [x] **Cfg available at both call sites**: `newCommandRunCmd` (`command_cmd.go:152`) already calls `config.LoadConfig`. `newCommandInspectCmd` (`command_cmd.go:79`) does NOT — it only loads the registry. Add an explicit `cfg, err := config.LoadConfig(flags.configPath)` at the top of the inspect handler so `cfg.UI.Commands.*` is reachable there too. Loading config for a read-only inspect command is cheap (single YAML parse) and keeps `ui.commands.*` behaviour consistent across `run` and `inspect`. If the load fails, fall through with `cfg = nil` — the nil-safe accessors return defaults.
- [x] **Cfg + skip-confirm threading**: keep `selectCommandFn func(defs, title) (string, error)` unchanged. At both `command_cmd.go:160` (run) and `:101` (inspect) build the selector as a closure that captures `cfg` and constructs `Options` by **calling the nil-safe accessors** to resolve every field — no reliance on cmdbrowser zero-value defaulting:
  ```go
  opts := cmdbrowser.Options{
      DefaultExpandedDepth: config.UICommandsDefaultDepth(cfg),
      AutoCollapseEmpty:    config.UICommandsAutoCollapseEmpty(cfg),
      ShowTypeBadges:       config.UICommandsShowTypeBadges(cfg),
      IncludePrivate:       includePrivate, // true for inspect, false for run
      Mode:                 cmdbrowser.ModeRun, // or ModeInspect at the inspect site
  }
  ```
  Explicit `default_expanded_depth: 0` / `auto_collapse_empty: false` / `show_type_badges: false` reach `cmdbrowser` unchanged because the config accessors return concrete primitives derived from the `*bool` / clamped-int tri-state. The run-site closure additionally captures a local `var skipConfirmFromTUI bool`; after `cmdbrowser.Run` returns, `skipConfirmFromTUI = result.SkipConfirm`. After `resolveCommandID` returns the ID, `if skipConfirmFromTUI { shouldSkip = true }` before building `rctx`. The inspect site does not need the `*bool` capture because `ModeInspect` disables the `y` binding (see Task 5) and `Result.SkipConfirm` will be unobserved there. Tests using `selectCommandFn` injection see no signature change.
- [x] add `goleak.VerifyTestMain(m)` in `internal/ui/cmdbrowser/cmdbrowser_test.go` — bubbletea spawns input/render goroutines; tests must catch leaks after `Run()` returns
- [x] write tests (use `t.Cleanup` to restore the seam vars after each case): open-and-close happy path with `isTerminalFn` stubbed `true` and `terminalSizeFn` stubbed `120×30`; `ErrCancelled` on `Esc` / `q`; non-TTY path with `isTerminalFn → false` returns `ErrCancelled` and never calls `runSelectorFn` (assert via a sentinel-replacing `runSelectorFn` that records calls); narrow path with `isTerminalFn → true` and `terminalSizeFn → 50×30` calls a stubbed `runSelectorFn` and the `Result.Idx`/`err` round-trip via the stub; **`terminalSizeFn` error path** (`isTerminalFn → true`, `terminalSizeFn → 0, 0, errBoom`) also delegates to `runSelectorFn` and the original size error is not surfaced; **`DefaultOptions()` returns the documented defaults** (depth=3, AutoCollapseEmpty=true, ShowTypeBadges=true, Mode=ModeRun); **`Options{}` is intentionally degenerate** — calling `Run` with it yields depth=0 / no auto-collapse / no badges (verify via `m.View()` snapshot), and Mode=ModeRun via the only legitimate `applyDefaults` promotion; **explicit zeros are preserved** — `Options{DefaultExpandedDepth: 0, AutoCollapseEmpty: false, ShowTypeBadges: false}` reaches the model unchanged; `Result.SkipConfirm` round-trips
- [x] **tests for the new `ui.RunWithPromptHooks`** in `internal/ui/huh_test.go` — mirror the existing hook tests: `before` fires before `fn`; `after` fires after `fn` returns nil; `after` still fires when `fn` returns an error (the error is propagated unchanged); nil `before`/`after` (no `SetHuhHooks` call active) is safe; `ClearHuhHooks` mid-`fn` does NOT skip the snapshotted `after` (proves the snapshot-once contract)
- [x] snapshot test for the empty two-panel layout at 120×26 — drive via `m.Update(tea.WindowSizeMsg{...})` then `m.View()` directly (NOT `tea.NewProgram(...).Run()`); pin lipgloss/v2 colour profile to ASCII in `TestMain` for deterministic output
- [x] run `make test` and `make lint` — must pass before Task 3

### Task 3: Left tree panel — model, render, navigation, counts

- [x] in `internal/ui/cmdbrowser/tree.go` define `TreeModel` that builds the group hierarchy **internally from `[]Item.ID`** by splitting on `.` — cmdbrowser stays decoupled from `usercommands.GroupNode` (which `resolveCommandID` does not pass to the selector anyway). Per-node state: expanded flag, focused path. Leaf counts and `Private` filtering derive directly from the items slice.
- [x] implement expand / collapse semantics from §5.2: `→`/`l` expand; `←`/`h` collapse-or-up; `Space` toggle; `Home`/`End`; `↑/↓` navigate
- [x] compute per-node public-leaf counts (cached on the model; invalidate when `IncludePrivate` flips)
- [x] initial expansion: respect `opts.DefaultExpandedDepth`; depth 0 = all collapsed; missing config → default 3
- [x] in `tree_render.go` render the tree via Lipgloss using **existing** `styles.yml` keys only (see Task 4 mapping); cursor marker `❯` + left bar for focused row. No new keys in this task.
- [x] integrate into the model: tree owns left panel, focus state machine has `left` and `right`; right panel for now just lists items of the currently focused group as plain strings (final delegate lands in Task 4)
- [x] write table-driven tests for `TreeModel.Update`: each key in §5.2, expand depth init, focus path stability under expand/collapse — independent cases call `t.Parallel()`; subtest names lowercase phrases (`"right expands"`, `"left collapses then ascends"`)
- [x] write fuzz test for the count invariant: random ID set → group count == sum of child counts + matching leaves; collapse is idempotent; tree-from-IDs round-trip is stable across permutations
- [x] snapshot test for the initial tree at depth 3 with a fixture item set — drive via `m.Update`/`m.View` directly
- [x] run `make test` and `make lint` — must pass before Task 4

### Task 4: Right list panel — `list.Model`, custom delegate, badges, breadcrumb

- [x] in `internal/ui/cmdbrowser/list_delegate.go` implement a two-line `list.ItemDelegate` (height=2, spacing=1): line 1 = command ID + right-aligned type badge, line 2 = description truncated with `lipgloss.NewStyle().MaxWidth(...)`
- [x] `FilterValue() = ID + " " + Description` for fuzzy match
- [x] type-badge styles in a new `internal/ui/cmdbrowser/styles.go`. **Use only existing `styles.yml` keys** (`StylesColors` has: `label`, `section_title`, `subheader`, `muted`, `warning`, `info`, `enabled`, `disabled`, `mandatory`, `partial`, `table_border`, `table_header`). Default mapping covering **all seven** command types (`CommandType*` constants at `internal/usercommands/model/types.go:21-38`, including the previously-missed `CommandTypeDevbox`):
  - `shell` → `info`
  - `script` → `label`
  - `workflow` → `warning`
  - `service_exec` → `enabled`
  - `service_run` → `partial`
  - `builtin` → `muted`
  - `devbox` → `section_title`
  - unknown / missing type → `muted` (defensive fallback)

  Do NOT introduce new style keys in v1 — touching `styles.yml` / `LoadStylesConfig` / `ApplyStyles` / `docs/reference/config/styles.md` is a large surface and out of scope. If a future palette tweak is needed, file a follow-up.
- [x] hide the `(N)` count next to tree groups and the type-badge column on the right when terminal width is 80–99 (per §4.1)
- [x] right-panel title = breadcrumb of focused group + `· N commands`; paginator dots come from `list.Model` automatically
- [x] handle `Tab` to move focus left↔right; right-panel `←` returns focus to tree (§7 table)
- [x] handle `Enter` on the right panel: stop the program, return `Result{Idx: i, Action: ActionRun}` for `ModeRun` or `Result{Idx: i, Action: ActionInspect}` for `ModeInspect`. Action is derived from Mode in v1; the enum is kept distinct so future `e edit` can return a different Action.
- [x] handle `Enter` on a group in the tree per §7.1: collapsed → expand AND move focus right; expanded → just move focus right
- [x] write tests: delegate render snapshot for each command type (all seven — explicit case per type), breadcrumb formatting, `Enter`-on-group drill-in semantics, `Enter`-on-list returns the right `Result.Idx`, width-bucket badge/count visibility — table-driven cases use `t.Parallel()` where independent
- [x] run `make test` and `make lint` — must pass before Task 5

### Task 5: Filter mode (`/`) + inspect overlay (`i`) + skip-confirm (`y`) + dynamic help (`?`)

- [x] filter mode: enter via `/`; right panel becomes flat ranked results using `list.DefaultFilter`; left tree gains `M/N` counts and dims groups with `M==0`
- [x] honour `opts.AutoCollapseEmpty`: when true, dim+collapse zero-match subtrees for the duration of the filter session; restore prior expanded state on `Esc`
- [x] cursor restoration on filter exit (§8): if highlighted command belongs to currently selected group, keep right-panel cursor on it; otherwise move tree focus to the nearest ancestor of that command and refresh right
- [x] inspect overlay: `bubbles/v2/viewport.Model` centred over the right panel, width `min(rightPanelWidth-4, 80)`; content is **`items[focusedIdx].Inspect`** — the precomputed string filled by the caller in Task 2 (`defaultSelectCommand` calls the unexported `printCommandInspect` from inside `internal/command` and stuffs the result into `Item.Inspect`). cmdbrowser performs no rendering of CommandDef shapes itself.
- [x] inspect key handling: `↑/↓`, `j/k`, `PgUp`/`PgDn` scroll; `Esc` closes (focus restored to right panel); `Enter` closes the program and returns `Result{Idx, Action: ActionInspect}` for `ModeInspect` or `ActionRun` for `ModeRun`
- [x] `y` toggles skip-confirm — **enabled only when `opts.Mode == ModeRun`**. Under `ModeInspect` the binding is removed from the keymap (so it does not appear in help) and the `y` keypress falls through as a no-op; inspect never runs the command so `Result.SkipConfirm` would be unobserved. When enabled, surface `[--yes ON]` in the footer when true. State propagates via **`Result.SkipConfirm`** (defined in Task 2); the call site at `command_cmd.go:160` reads it through the closure-captured `*bool` pattern and ORs it into the existing `shouldSkip` before building `rctx` — no `selectCommandFn` signature change.
- [x] dynamic help footer via `bubbles/v2/help.Model`: short form by default, long via `?`; the visible bindings change with focus state (left / right / filter / inspect)
- [x] `Esc` on top-level tree exits the TUI (per §19 — confirmed); `Esc` in filter exits filter only
- [x] write table-driven `Update` tests across all four focus modes (left, right, filter, inspect) × all hotkeys; assert transitions and that filter cursor restoration matches §8 — independent cases use `t.Parallel()`
- [x] test `Result.SkipConfirm` toggling: pressing `y` once flips it to true and the footer shows `[--yes ON]`; pressing again flips it back; the value survives selection and is in the returned `Result`
- [x] snapshot tests: filter active with matches, filter active with zero-match groups dimmed, inspect overlay open, footer in skip-confirm-ON state, footer in long-help state — all driven via direct `m.Update`/`m.View`, no `tea.NewProgram`
- [x] run `make test` and `make lint` — must pass before Task 6

### Task 6: Narrow-terminal single-panel fallback + colour profile audit

- [x] when width is in 60..79: render single-panel mode with `── group ──` pseudo-headers between groups (per §4.1); no tree, no badges
- [x] keep the same keymap subset (`↑/↓`, `Enter`, `/`, `i`, `y`, `?`, `Esc`); filter still works
- [x] verify `NO_COLOR=1` rendering — bubbletea/lipgloss respect this; snapshot one state with monochrome profile
- [x] verify long command IDs (e.g. `services.main.index.reindex-catalog-product-availability-search`) truncate cleanly with `…` and never overflow the panel
- [x] snapshot tests: 70-col single-panel initial, 70-col filter active, NO_COLOR variant
- [x] run `make test` and `make lint` — must pass before Task 7

### Task 7: Verify acceptance criteria

- [x] manual test (skipped - not automatable): all entry points covered: `devbox commands run`, `devbox commands run <group>`, `devbox commands inspect`, `devbox commands inspect <group>`
- [x] verify §2 non-goals are respected: no edit-from-TUI shipped, no multi-select shipped, `huh.NewSelect` still used by `RunSelector` / `RunConfirm` / `RunMultiSelect`
- [x] `selectCommandFn` signature unchanged → confirmed at `command_cmd.go:244` — existing `selector_test.go` injection still works
- [x] non-TTY: error message preserved at `command_cmd.go:109` and `:169` — exit code and message unchanged
- [x] run full `make test` — all packages green
- [x] run `make lint` — 0 issues
- [x] check test coverage for `internal/ui/cmdbrowser` (87.4%) and `internal/config` (90.1%) — both clear the 80% bar
- [x] manual test (skipped - not automatable): verify the nine LiveLine invariants are not regressed after `devbox deploy` followed by `devbox commands run` in the same session

### Task 8: [Final] Documentation

- [x] add or update `docs/reference/config/ui.md` (new page) and cross-link from `docs/reference/config/devbox.md` and `docs/reference/config/commands.md`
- [x] cover: schema, defaults, hotkey table per focus mode, fallback ladder (full two-panel ≥100 cols → reduced two-panel 80–99 → single-panel 60–79 → flat `huh` <60 cols or <15 rows; non-TTY is gated at the call site, not in cmdbrowser, with the existing error message), example `ui:` block
- [x] regenerate CLI reference via `devbox docs generate --scope cli` if any help text was reworded — no help text changes on this branch (verified via `git diff main..HEAD -- internal/command/`)
- [x] update `AGENTS.md` (`CLAUDE.md` is a symlink — don't touch) for the new `internal/ui/cmdbrowser/` package entry, mirroring the existing one-line summaries

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

- **`internal/ui` surface grows by exactly one symbol**: `RunWithPromptHooks(fn func() error) error` is added to `internal/ui/huh.go` to encapsulate the snapshot-before-defer-after dance for full-screen UI. The existing `SnapshotHuhHooks` is left untouched (its docstring scopes it to cross-package tests). Callers (`internal/command`) import `internal/ui/cmdbrowser` directly — a facade in `package ui` is **not viable**, it would require `ui → cmdbrowser → ui`. The "use `ui.Run*`" guideline in CLAUDE.md is satisfied in spirit because `cmdbrowser.Run` runs the same hook dance via `ui.RunWithPromptHooks`.
- **Package boundary**: one-way edge `cmdbrowser → ui` for `ui.ErrCancelled` and the new `ui.RunWithPromptHooks`. `internal/ui` does NOT import `cmdbrowser`. `internal/command → ui/cmdbrowser` is a new import edge but does not cycle because `cmdbrowser` does not depend on `command`.
- **Inspect rendering decoupling**: `cmdbrowser` does NOT know about `usercommands.CommandDef`. Inspect content is a precomputed `string` carried on each `Item`. `defaultSelectCommand` (in `internal/command`) calls its sibling unexported `printCommandInspect(&buf, def)` while building `[]Item` — same package, no export needed, no cycle.
- **Tree source decoupling**: `cmdbrowser` does NOT know about `usercommands.GroupNode`. The tree is built **internally** from `[]Item.ID` by splitting on `.`. This matches the existing `selectCommandFn` contract (filtered `[]*CommandDef` → `[]Item`, registry never crosses the boundary).
- **Type names** (per Go anti-stutter convention): `cmdbrowser.Item`, `cmdbrowser.Options`, `cmdbrowser.Result`, `cmdbrowser.Action`, `cmdbrowser.Mode`. Both `Action` and `Mode` enums place `*Unknown` at iota 0 so a zero-value return is detectable.
- **`Options` carries resolved values** — defaulting lives in the config accessors (Task 1's `*bool` tri-state + clamped int), not in `cmdbrowser`. Auto-defaulting `int`/`bool` fields here would silently overwrite legitimate user values (`default_expanded_depth: 0`, `auto_collapse_empty: false`, `show_type_badges: false`). The only field that `applyDefaults` touches is `Mode`, which has an explicit `ModeUnknown` iota-0 sentinel: a zero `Mode` is promoted to `ModeRun`. Callers without `*config.DevboxConfig` use `cmdbrowser.DefaultOptions()` to obtain the spec defaults instead of relying on the zero value.
- **`Result` over tuple**: `cmdbrowser.Run` returns `(Result, error)` with `Result { Idx int; Action Action; SkipConfirm bool }`. Future intents (e.g. `e edit`) extend `Result` additively without breaking call sites.
- **Threading cfg + skip-confirm**: `selectCommandFn func(defs, title) (string, error)` is unchanged. `newCommandInspectCmd` gains an explicit `cfg, _ := config.LoadConfig(flags.configPath)` (run already has one). Both call sites build the selector closure with `cfg` captured for `cmdbrowser.Options`; only the **run** site additionally captures a `var skipConfirmFromTUI bool` written from `Result.SkipConfirm`. The outer run-`RunE` ORs it into `shouldSkip` before building `rctx`. The inspect site does not capture the bool because `ModeInspect` disables the `y` binding (Task 5). Tests using `selectCommandFn` injection see no signature change.
- **Type-badge palette uses only existing `styles.yml` keys** — see Task 4 for the full mapping. The seven command types (`shell`, `script`, `service_exec`, `service_run`, `workflow`, `devbox`, `builtin`) all have explicit mappings; an unknown type falls back to `muted`. No additions to `internal/config/styles.go` are required by this plan.
- **Focus state machine** lives in `cmdbrowser.Model`: `focus ∈ {left, right, filter, inspect}` plus the prior-focus stack used by `Esc` to return from filter/inspect.
- **Filter session state** captures the snapshot of expanded-node IDs before `/` and restores it on `Esc`; only `AutoCollapseEmpty` mutates the visible expansion during the session.
- **Inspect popover** is a `viewport.Model` populated by `printCommandInspect(&buf, def)` — no duplicated rendering logic. Width clamped to `min(rightPanelWidth-4, 80)`.
- **Type-badge palette**: read from existing `styles.yml` keys via `ui.ApplyStyles` — no schema changes. Final mapping (all seven `CommandType*` constants in `internal/usercommands/model/types.go`): `shell→info`, `script→label`, `workflow→warning`, `service_exec→enabled`, `service_run→partial`, `builtin→muted`, `devbox→section_title`. Unknown/missing type falls back to `muted`. See Task 4 for the implementation site.
- **Config layering**: nil-safe accessors guard against `cfg == nil` and against zero-value blocks. Negative `default_expanded_depth` clamps to 0; values larger than the deepest path effectively expand everything (no clamp needed).
- **SIGINT**: bubbletea returns `tea.ErrInterrupted` on Ctrl-C; map it to `ui.ErrCancelled` in `cmdbrowser.Run` so callers see one sentinel.
- **Fallback ladder** inside `cmdbrowser` (assumes a TTY — non-TTY callers are already short-circuited at `command_cmd.go:102` / `:161` with the existing error message and never reach this code):
  1. non-TTY reached defensively (e.g. unexpected programmatic caller) → return `ui.ErrCancelled` immediately; do NOT delegate to `RunSelector` (huh requires a TTY)
  2. `width < 60` OR `height < 15` (still a TTY) → delegate to `ui.RunSelector` (today's flat huh list)
  3. `width ∈ [60, 79]` → single-panel `── group ──` mode
  4. `width ∈ [80, 99]` → two-panel sans `(N)` and type badges
  5. `width ≥ 100` → full two-panel with badges and counts

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification**:
- Run `devbox commands run` against a real 30+ command project (e.g. one with `services.main.index.reindex-*`) and confirm the §17 success metrics qualitatively: time-to-pick drops noticeably, fuzzy filter resolves under ~2 s.
- Record VHS tapes (`.tape` files) of the key states from §4.2 for the `docs/reference/config/ui.md` page — initial, focus-right, filter, inspect, narrow fallback.
- Manually exercise `NO_COLOR=1 devbox commands run` and verify monochrome rendering is legible (badges become text, focus marker uses bold instead of colour).
- Smoke-test inside CI / piped contexts (`devbox commands run < /dev/null > /tmp/out`) — must preserve the existing non-interactive error (`"no exact command ID given; pass a full command ID or run in an interactive terminal"`) exactly as before this change; cmdbrowser is never reached because `command_cmd.go:161` gates non-TTY at the selector level.

**External system updates**:
- Announce the new `ui:` block in release notes; mention defaults so existing `devbox.yml` files keep behaving identically (no migration needed).
- If downstream wrappers / templates ship a `devbox.yml`, optionally add a commented-out `ui:` block to show the knobs without changing behaviour.

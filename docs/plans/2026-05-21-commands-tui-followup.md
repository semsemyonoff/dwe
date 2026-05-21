# Commands TUI follow-up: help bar, styles.yml palette, CLI simplification, param prompting

## Overview

Follow-up work after the completed plan `docs/plans/completed/2026-05-20-commands-tui-browser.md`. Two independent blocks bundled into one plan:

- **Block A — TUI UX/styles:**
  - Fix help-footer visibility in `cmdbrowser` (the custom `help.Model` footer is already wired but not rendering — diagnose root cause).
  - Make the TUI palette customizable via `styles.yml`: replace hardcoded `Bold/Faint/Color("12")` literals in `cmdbrowser/model.go`, `tree_render.go`, `list_delegate.go` with palette strings exposed via new `ui.Color*() string` accessors. Construct `charm.land/lipgloss/v2` styles inside `cmdbrowser` (the only sanctioned bridge between lipgloss v1 in `internal/ui/` and v2 in `cmdbrowser/`). Wire through `bubbles/v2` `list.Styles` / `DefaultItemStyles` / `help.Styles` / `viewport`. Add missing palette keys to `StylesConfig.Colors`.
  - Update `docs/reference/config/styles.md` (new "Command browser" section).

- **Block B — `commands` CLI + parameter prompting:**
  - Reshape `devbox commands` so that `devbox commands <id>` runs the command directly, `--inspect/-i` replaces today's `inspect` subcommand, the `run` subcommand is removed, and `list` stays. Add `cmd` alias. Reserve the top-level ID `list` (warning via `validate/commands`).
  - Before running a command show a `huh.NewForm` with ALL params (required + optional), pre-filled from `--set`. Inline pattern validation. Behavior depends on TTY / `-y` / `DEVBOX_NONINTERACTIVE`.
  - Confirmation after the form — with an auto-rendered summary of filled values.

The CLI surface has no external users yet, so no deprecation runway is needed — the `run` / `inspect` subcommands are removed outright.

## Context (from discovery)

**Affected files (Block A):**
- `internal/ui/cmdbrowser/model.go` — panel borders, `Color("12")` focus, title, `Bold(true)` literals.
- `internal/ui/cmdbrowser/tree_render.go` — left tree: nodes, `▸`/`▾`, `(N)` counters — `Bold`/`Faint` literals.
- `internal/ui/cmdbrowser/list_delegate.go` — custom delegate with `Bold(true)`/`Faint(true)` literals (lines 98, 111, 132).
- `internal/ui/cmdbrowser/styles.go` — entry point for palette injection; already maps badges to `ui.Style*`.
- `internal/config/styles.go` — `StylesConfig.Colors` map.
- `internal/ui/styles.go` — semantic `ui.StyleInfo` / `StyleKey` / ... accessors.
- `docs/reference/config/styles.md` — user-facing palette docs.

**Affected files (Block B):**
- `internal/command/command_cmd.go` (759 LOC) — currently holds `newCommandCmd`, `newCommandListCmd`, `newCommandInspectCmd` (to be removed), `newCommandRunCmd` (its logic moves to the parent).
- `internal/command/command_cmd_test.go` (636 LOC) — table-driven tests across the existing subcommands.
- `internal/usercommands/loader/` — `computeCommandID` + `reservedTopLevelIDs`.
- `internal/validate/commands/` — emitter for the "id conflicts with reserved top-level subcommand" warning.
- `internal/ui/paramform.go` — new file.
- `internal/ui/confirm.go` — add `ConfirmRun(title string, values map[string]string) (bool, error)` alongside existing `RunConfirm`. Caller passes an already-rendered title (no `tpl` imports here); `ui.ErrCancelled` for Esc, `(false, nil)` for No. Full contract in Task 9.
- `internal/usercommands/runtime/runner.go` — verify there is no duplicate interactive param prompt.
- `internal/ui/selector.go:31` — test seam `runSelectFormFn` — template for the paramform analog.
- `docs/reference/config/commands.md`, `docs/reference/cli/` (regenerated).

**Relevant patterns:**
- The project uses `bubbles/v2` (see `liveui/liveline.go` invariants).
- All interactive huh calls must go through `ui.Run*` or `ui.RunWithPromptHooks`. The TUI program is already torn down (alt-screen exited) before the form is shown — the standard `ui.SetHuhHooks` flow is sufficient.
- `isValidateCommand` schema-bypass pattern is not relevant here; the resolver path stays standard.

**Open questions from the spec:**
- Is `usercommands.resolve.Params` callable independently from `BuildRunContext` so that `DefaultFrom` can be resolved before opening the form? → **De-risked in Task 7.5** as an explicit discovery step before any orchestrator/form work, with a ⚠️ stop-and-replan gate if extraction balloons.
- `runtime/runner.go` interactive param prompt — `grep "Prompt|interactive"` shows only the confirmation prompt; the plan still includes an explicit check (Task 11).

## Development Approach

- **Testing approach:** Regular (code → tests within the same task).
- Complete each task fully before moving on.
- Small, focused changes.
- **CRITICAL: every task MUST include new or updated tests** for the code it changes.
- **CRITICAL: all tests must pass before starting the next task** (`make test`).
- **CRITICAL: update this plan if scope changes during implementation.**
- Run `make lint` after tasks that touch public APIs.
- Preserve backward compatibility outside of the explicitly agreed breaking change (removal of `run` / `inspect` subcommands).

## Testing Strategy

- **Unit tests:** required per task.
- **Table-driven, named subtests:** every test case gets a `name` field passed to `t.Run(tt.name, ...)`. Mandatory across all new test files in this plan.
- **Test seams** for huh forms — modeled on `runSelectFormFn` in `internal/ui/selector.go:26` (package-level function var that tests can override). **Caveat:** any test that overrides a package-level seam MUST NOT call `t.Parallel()` — global state is not goroutine-safe across subtests; isolate seam-overriding subtests from any parallel-marked siblings.
- **Cobra test pattern:** fresh command tree per test (`cmd := newCommandCmd(flags)`), then `cmd.SetArgs([]string{...})`, `cmd.SetOut(buf)`, `cmd.Execute()`. Cobra accumulates flag state across `Execute()` calls on the same command — never reuse instances.
- **Output capture:** all new code paths emitting human-readable output (inspect printer, summary lines, error messages) must write through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()` — direct `os.Stdout` writes cannot be captured by tests and will break the integration suite.
- **`cmdbrowser` snapshot/golden tests:** existing tests in `internal/ui/cmdbrowser/cmdbrowser_test.go` will be affected by the style changes — update goldens in the same task that introduces the visible change.
- **Behavior over implementation:** style-apply tests should assert both struct fields populated (fast feedback) AND render output containing the configured ANSI codes (behavior check). A struct-field-only assertion regresses silently if bubbles renames an internal field.
- **No true E2E:** the project has no UI-level e2e suite. Tests in `command_cmd_test.go` are wired unit tests with stubbed command-package seams (`runParamForm`, `confirmRun`, `runUserCommand` — see Task 10) and overridden `ui.IsInteractiveFn`. They do not need build tags.
- **`make test -race`** at the end of Task 12 — the live view and pipeline already have concurrent reporters, and the new orchestrator threads `ctx` into runners; race is a meaningful gate.

## Progress Tracking

- Mark completed items with `[x]` immediately.
- Add newly discovered tasks with the ➕ prefix.
- Document blockers with the ⚠️ prefix.
- If Task 10 reveals that `resolve.Params` cannot be detached cleanly, expand the task scope and update this plan.

## What Goes Where

- **Implementation Steps:** code, tests, `docs/reference/` updates.
- **Post-Completion:** manual visual checks of the TUI in several color profiles, exercising `devbox commands` in a real project.

## Implementation Steps

### Task 1: Help footer in `cmdbrowser` — investigate and fix visibility
- [x] **DO NOT enable `list.SetShowHelp(true)`.** A custom `help.Model` footer is already wired in `cmdbrowser/model.go` (lines 92, 381, 423, 459) via `renderHelpFooter` / `shortBindings` / `fullBindings`; `l.SetShowHelp(false)` is deliberate (avoids double-rendering)
- [x] reproduce the "no help shown" report: root cause = height math. Title(1) + bordered panel(bh+2) + footer(≥1) = bh+4; with bh=`max(h-3,3)` total = h+1, pushing the footer off-screen exactly when the alt-screen clipped at the declared terminal height. Bindings non-empty per focus; help.Model is styled (default ANSI 8/240) so visibility is not a palette problem. Fallback ladder `h<15` correctly delegates and is by-design.
- [x] fix the actual cause: reserved 1 row for the footer via new `bodyHeight(h)=max(h-4,3)` helper; right-panel body has breadcrumb + list, so list itself is sized via `listHeight(h)=bodyHeight(h)-1` to keep the panel within Height(bh). Centralised in two helpers; replaced four `max(h-3,3)` literals in `model.go` (newModel, applyLayout, View, viewSinglePanel).
- [x] verify single-panel (60) and two-panel sub-buckets (70/90/120) — regression test asserts total content lines ≤ terminal height at each.
- [x] no golden files exist under `internal/ui/cmdbrowser/testdata/` — nothing to refresh
- [x] added `TestModel_HelpFooterVisibleWithinTerminalHeight` in `cmdbrowser_test.go` — asserts the `help` binding label appears in `View().Content` AND the rendered content fits within the declared terminal height across four width buckets
- [x] `single_panel_test.go` — no changes needed (single-panel mode is covered by the new regression test's `single_panel_60` case; existing assertions still pass)
- [x] `make test ./internal/ui/cmdbrowser/...` — passes

### Task 2: Add missing keys to `StylesConfig.Colors` + palette accessors (lipgloss v1/v2 split)
- [x] **lipgloss-version constraint:** `internal/ui/styles.go` uses `github.com/charmbracelet/lipgloss` (v1) — see `go.mod:56`. `cmdbrowser` and `bubbles/v2` use `charm.land/lipgloss/v2` — see `go.mod:10` and `cmdbrowser/model.go:12`. `lipgloss.Style` values are NOT interchangeable across versions, and the two packages must coexist (other UI surfaces still need v1). Accessors must respect this.
- [x] in `internal/config/styles.go` `StylesColors` (line 27) add new YAML fields for: `focus_border`, `description`, `tree_count`, `tree_arrow`, `filter_match`, `pagination_active`, `pagination_inactive` (confirm the final set with Task 4). Color strings only — no `lipgloss.Color` in config types. Empty string means "use default", matching the existing convention (`styles.go:26-27` comment, and `styles.md:116` documented behavior).
- [x] **DO NOT inject defaults in `LoadStylesConfig`.** Per `config/styles.go:67`, the loader returns a zero-value `&StylesConfig{}` on missing file and never fills defaults. Defaults are applied at use-site via `ui.ApplyStyles` (see `CLAUDE.md` and `docs/reference/config/styles.md:116`). Preserve this layering — the loader stays a thin YAML parser.
- [x] put new defaults in `internal/ui/styles.go`:
  - extend the package-level `var (...)` block with new color string variables (current pattern: literal `"12"`, `"6"`, etc.) — e.g. `colorFocusBorder = "12"`, `colorDescription = "8"`
  - extend `ApplyStyles` to overwrite each new variable from `cfg.Colors.<NewField>` when the field is non-empty (mirroring lines 68+ in current `styles.go`)
  - expose **string-typed** read accessors usable by both lipgloss versions: `func ColorFocusBorder() string { return colorFocusBorder }`, `func ColorDescription() string { return colorDescription }`, etc. These are the shared API.
- [x] keep existing v1 `Style*` accessors as-is (other consumers depend on them); add a new file `internal/ui/cmdbrowser/palette.go` that constructs `charm.land/lipgloss/v2` styles from the `Color*()` strings — this keeps the v2 dependency confined to `cmdbrowser` (and any future v2 surface).
- [x] unit tests in `internal/config/styles_test.go` (load YAML, struct-field assertions) and `internal/ui/styles_test.go`: (a) default `Color*()` returns the hardcoded fallback when no `styles.yml` loaded, (b) after `ApplyStyles(cfg)` with non-empty fields, `Color*()` returns the configured value, (c) empty field in `cfg.Colors` does NOT overwrite the default. Pure string assertions, no lipgloss types.
- [x] `make test ./internal/config/... ./internal/ui/...` — must pass

### Task 3: Wire the palette into `bubbles/v2` `list`/`viewport`/`help`
- [x] in `internal/ui/cmdbrowser/palette.go` (created in Task 2) add `applyListStyles(*list.Model)`, `applyItemStyles(*list.DefaultItemStyles)`, `applyHelpStyles(*help.Styles)`, `applyViewportStyles(*viewport.Model)` — each consumes `ui.Color*()` strings and constructs `charm.land/lipgloss/v2` styles internally (the bubbles/v2 Style fields require v2 lipgloss types — never mix versions here)
- [x] switch the custom `list_delegate.go` either to `list.DefaultItemStyles` (if visuals match) or keep the custom delegate but read color sources from `ui.Color*()` strings (constructing v2 lipgloss styles inline) instead of the hardcoded values on lines 98, 111, 132
- [x] in the `cmdbrowser.Run` constructor, call the relevant `apply*` functions right after `list.Model` is built
- [x] viewport (inspect overlay) — also wire `Style` and `HighlightStyle` (v2 lipgloss styles built from `ui.Color*()` strings)
- [x] unit tests in `internal/ui/cmdbrowser/styles_test.go` (or a new `styles_apply_test.go`):
  - struct-level: after `apply*` the corresponding bubbles fields hold the expected colors (fast feedback)
  - render-level: at least one test renders the model via `Model.View()` with an overridden palette key and asserts the ANSI escape for that color appears in the output (guards against bubbles renaming internal fields)
- [x] `make test ./internal/ui/cmdbrowser/...` — passes

### Task 4: Replace literals in `model.go` / `tree_render.go` / `list_delegate.go`
- [x] all replacements construct **`charm.land/lipgloss/v2`** styles inside `cmdbrowser` using palette strings from `ui.Color*()` accessors (Task 2). Do NOT import v1 `lipgloss.Style` here.
- [x] `internal/ui/cmdbrowser/model.go`: panel borders and focus color — now use `lipgloss.Color(ui.ColorFocusBorder())`
- [x] `model.go` title and `[--yes ON]` — palette-driven via new `paletteKey()` / `paletteSuccess()` helpers backed by new `ui.ColorKey()` / `ui.ColorSuccess()` string accessors (driven from existing `c.Label` / `c.Enabled` YAML fields)
- [x] `tree_render.go` — `(no groups)` / `(N)` counters / dimmed lines via `paletteDescription` / `paletteTreeCount`; focused-line bold via `paletteFocusBorder().Bold(true)`; v1 `lipgloss` import removed
- [x] `list_delegate.go` — already palette-driven from Task 3 (no remaining literals)
- [x] grep gate: no remaining `lipgloss.NewStyle().Bold(true)` / `Faint(true)` / `Color("…")` literals in the three files
- [x] `palette_test.go` extended with `Key` / `Success` cases
- [x] `make test ./internal/ui/...` — passes; full `make test` green

### Task 5: Documentation — `styles.md` "Command browser" section
- [x] in `docs/reference/config/styles.md` add a `### Command browser` subsection: list of new palette keys, their defaults, what they affect (with a mapping to bubbles structures)
- [x] mention the test fallback: if a key is missing from `styles.yml`, the default from `internal/ui/styles.go` is used
- [x] no code tests required (docs), but verify `docs/reference/` remains internally consistent

### Task 6: CLI restructure — `commands`
- [ ] in `internal/command/command_cmd.go` rewrite `newCommandCmd(flags)`:
  - `Use: "commands [id]"`, `Aliases: []string{"cmd"}`, `Args: cobra.MaximumNArgs(1)`
  - move the current `newCommandRunCmd` `RunE` logic into the parent
  - add flags: `--set k=v` (`StringArrayVar`, repeatable — `StringArray` preserves comma-containing values; `StringSlice` would split incorrectly), `-y/--yes` (`BoolVar`), `-i/--inspect` (`BoolVar`)
  - flag exclusivity via cobra primitives — NOT manual `RunE` checks:
    - `cmd.MarkFlagsMutuallyExclusive("inspect", "set")`
    - `cmd.MarkFlagsMutuallyExclusive("inspect", "yes")`
    - cobra emits a standard error message and reflects the relationship in help output
  - manual `RunE` check for the one constraint cobra cannot express: `--inspect` AND `len(args)==0` → `errors.New("id required with --inspect")`
  - all output through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()` — no direct `os.Stdout`/`os.Stderr` writes (tests need to capture)
  - the `list` subcommand stays: `cmd.AddCommand(newCommandListCmd(flags))`
- [ ] before deleting `newCommandInspectCmd`, extract its body into a package-private helper `printInspect(w io.Writer, def *model.CommandDef) error` so both inspect mode and any future caller can reuse it
- [ ] delete `newCommandRunCmd` and `newCommandInspectCmd`
- [ ] **`ValidArgsFunction` dispatches on `--inspect`** — preserves today's completion surface across the merged command. Current `registryIDCompletion` (at `command_cmd.go:420`) takes a static `includePrivate bool`; the existing `inspect` subcommand uses `true` (`command_cmd.go:127`) and `run` uses `false` (`command_cmd.go:155`). The merged `commands [id]` must honor `--inspect` at completion time:
  ```go
  ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
      // Cobra parses flags before invoking ValidArgsFunction, so the --inspect
      // value is already available even though PersistentPreRunE is bypassed
      // (see CLAUDE.md `__complete` note). completionConfigPath() handles the
      // bypass on the flags side.
      inspect, _ := cmd.Flags().GetBool("inspect")
      return registryIDCompletion(flags, inspect)(cmd, args, toComplete)
  },
  ```
  Behavior:
  - `devbox commands <TAB>` → public command IDs (`includePrivate=false`)
  - `devbox commands --inspect <TAB>` → all IDs including private (`includePrivate=true`) — matches today's `devbox commands inspect <TAB>` behavior
- [ ] add a completion test in `completion_test.go` (mirroring `completion_test.go:79`) for both branches: `--inspect` set → private IDs present in returned completions; `--inspect` unset → private IDs absent
- [ ] **Update stale ActiveHelp string in `registryIDCompletion`** — `command_cmd.go:443` currently emits `cobra.AppendActiveHelp(..., "Use 'devbox commands inspect <id>' to see command details")`, but the `inspect` subcommand is being removed. Change to: `"Use 'devbox commands --inspect <id>' to see command details"`. Keep the surrounding `if !includePrivate && len(defs) > 0` guard intact (hint should still only appear in the run / public-completion path, not the inspect / all-IDs path).
- [ ] add a completion test asserting the ActiveHelp string is present and matches the new wording when `includePrivate=false`, and absent when `includePrivate=true`. Pattern: scan returned `completions` slice for the `cobra.ActiveHelpMarker` prefix.
- [ ] rewrite `command_cmd_test.go` (fresh command tree per test):
  - **Two test scaffolds depending on what is being asserted:**
    - For tests of the `commands` command in isolation (flags, args validation, RunE logic): `cmd := newCommandCmd(flags); cmd.SetArgs([]string{"<id>", ...}); cmd.SetOut(buf); cmd.Execute()`. Note: the leading word `cmd` is NOT in `SetArgs` here — we're already inside the subcommand.
    - For tests of the **alias path** `cmd <id>` (or any test that needs cobra dispatch resolution from the root): cobra `Aliases` are only resolved by the parent during dispatch. Tests MUST build a parent with `newCommandCmd(flags)` attached: either (a) instantiate the real root via the existing `newRootCmd(flags)` (preferred — exercises the same dispatch path as production, see `internal/command/root.go:166`) or (b) build a minimal test parent: `parent := &cobra.Command{Use: "test"}; parent.AddCommand(newCommandCmd(flags)); parent.SetArgs([]string{"cmd", "<id>"})`. Direct `newCommandCmd(flags).SetArgs([]string{"cmd", "<id>"})` will treat `cmd` as the positional id — that's a bug, not the alias path.
  - drop the `run`/`inspect` subcommand tests
  - add table-driven cases for the parent `commands [id]` across flag combinations, including:
    - `-i` without id → error matching `id required with --inspect`
    - `-i --set k=v <id>` → cobra mutual-exclusion error
    - `-i -y <id>` → cobra mutual-exclusion error
    - `<id>` alone → runner invoked with no prefilled values
    - `<id> --set k=v` → runner invoked with prefilled `{k:v}`
    - **alias path (via root or test-parent):** `cmd <id>` resolves the same command as `commands <id>` (same RunE invoked, same stubbed runner counts)
- [ ] update examples in `docs/reference/config/commands.md`
- [ ] `make test ./internal/command/...` — must pass

### Task 7: Reserved top-level IDs (warning for `list`)
- [ ] in `internal/usercommands/loader/` (likely `compute_id.go` or equivalent) add `reservedTopLevelIDs = []string{"list"}`
- [ ] in `internal/validate/commands/` add a new validator emitting `Diagnostic` severity=warning when a command's computed top-level ID matches a reserved word (domain `commands`, target = id)
- [ ] **Cobra dispatch reality:** `devbox commands list` ALWAYS routes to the cobra subcommand `list` — cobra resolves subcommand before `ValidArgsFunction` / positional args. A root-level user command with id literally `list` is therefore NOT reachable through `devbox commands list`. The warning is the only signal — keep it warning-only (no runtime block) but make the diagnostic message explicit:
  ```
  command id "list" conflicts with the reserved subcommand "devbox commands list".
  The command will only be reachable from the interactive browser (devbox commands).
  Consider renaming or moving it under a group (e.g. "tools.list").
  ```
- [ ] grouped IDs containing `list` are fine — only the top-level id `list` is reserved (e.g. `services.list` is allowed and reachable as-is). The check applies after `ComputeCommandID` produces the final dotted id and only fires when the result equals one of `reservedTopLevelIDs` exactly.
- [ ] runtime is NOT blocked — the command loads, runs from TUI, runs from `devbox commands <full-id>` (which equals `list` here, but that path is shadowed by the subcommand). Hence "browser-only".
- [ ] tests in `internal/validate/commands/`:
  - fixture with top-level id `list` → diagnostic emitted with the message above
  - fixture with `services.list` → NO diagnostic (substring match must not trigger)
  - fixture without `list` at all → NO diagnostic
- [ ] `make test ./internal/validate/... ./internal/usercommands/...` — must pass

### Task 7.5: Add a raw/defaults-only resolver alongside `resolve.Params`
- [ ] **Constraint confirmed by code review:** existing `resolve.Params` (`internal/usercommands/resolve/resolve.go:26`) is wrong for pre-form prefill because it (a) returns `map[string]any` after type coercion, (b) errors on missing required (line 47–49), (c) errors on pattern violation (line 60–62). The form is meant to display partial values, accept missing required (user fills them in), and run pattern checks inline via huh's per-field `Validate`. Reusing `Params` here would block the entire feature.
- [ ] **Add a NEW function** in `internal/usercommands/resolve/resolve.go`:
  ```go
  // ParamDefaults returns the raw string values that would prefill a parameter form.
  // For each declared parameter: provided[name] (treated as missing when empty —
  // matches Params() behavior at resolve.go:29-30) ∪ DefaultFrom (cfg path) ∪ Default.
  // Required-checks, type coercion, and pattern validation are intentionally skipped —
  // those are enforced by the form (huh per-field Validate) and by Params() at run time.
  func ParamDefaults(defs map[string]model.ParamDef, provided map[string]string, cfg *config.DevboxConfig) map[string]string
  ```
- [ ] **Empty-as-missing parity:** `provided[name] == ""` is treated identically to `provided[name]` not present — falls through to `DefaultFrom`/`Default`. This MUST match `resolve.Params` line 30 (`if !ok || raw == ""`) so a `--set x=` invocation produces the same effective prefill as omitting `--set x` entirely. Without this parity, the form prefill and the post-`BuildRunContext` normalized params diverge, breaking the confirmation summary (Task 10 builds the summary from normalized `rctx.Params`, so the form's prefill MUST match what `Params()` will eventually resolve to).
- [ ] keep `Params()` untouched — it remains the single source of truth for runtime validation, called by `BuildRunContext` after the user submits the form
- [ ] flow: orchestrator → `ParamDefaults` (raw strings → form prefill) → form Run (huh inline validates pattern + required) → `BuildRunContext` → internally calls `Params` (final coerce + sanity)
- [ ] tests in `resolve_test.go`: cases for `ParamDefaults` — (a) `provided` wins over default, (b) `DefaultFrom` resolves cfg path, (c) `Default` literal as final fallback, (d) missing-required returns "" (NOT an error), (e) pattern-mismatched value is returned as-is (NOT an error), (f) no type coercion (`"true"` for bool stays as string `"true"`), (g) **empty-as-missing parity:** `provided["x"]=""` with `Default:"d"` returns `"d"` (NOT `""`) — same result as omitting `"x"` from `provided`
- [ ] no test for ordering interaction with `Params()` needed at this layer — that's covered in Task 10 integration tests
- [ ] `make test ./internal/usercommands/...` — must pass before Task 8 starts

### Task 8: `internal/ui/paramform.go` — form builder (with UI-layer DTO)
- [ ] **Layering constraint:** `internal/ui/` must NOT import `internal/usercommands/model`, `internal/usercommands/...`, `internal/config`, or `internal/tpl`. (Same rule applied to `ConfirmRun` in Task 9.) Existing pattern: `RenderServiceTable` / `RenderDeployStatus` already use view-model types defined under `internal/command/statusview/`. Follow it here.
- [ ] **UI DTO** — define in `internal/ui/paramform.go`:
  ```go
  // ParamField is the UI-layer description of one parameter. Caller (orchestrator)
  // converts model.ParamDef → ParamField; ui does not import usercommands.
  type ParamField struct {
      Name        string
      Type        ParamFieldType  // FieldTypeString | FieldTypePath | FieldTypeInt | FieldTypeBool
      Description string
      Default     string  // raw string prefill (already merged: --set ∪ DefaultFrom ∪ Default)
      Required    bool
      Pattern     string  // empty = no pattern check
  }

  type ParamFieldType int

  const (
      FieldTypeUnknown ParamFieldType = iota
      FieldTypeString
      FieldTypePath
      FieldTypeInt
      FieldTypeBool
  )
  ```
  Naming follows the project convention: `*Unknown` at iota 0 (see `cmdbrowser.Action`/`Mode` in CLAUDE.md, `internal/command/statusview.ConfigDelta`).
- [ ] **Orchestrator side** (`internal/command/command_cmd.go`, lands in Task 10): a small adapter `paramFieldsFromDef(def *model.CommandDef, prefilled map[string]string) []ui.ParamField` translates `model.ParamDef` → `ui.ParamField` (deterministic order: sorted by name, or honoring declared order if accessible). Tested under `command_cmd_test.go`.
- [ ] **API in `paramform.go`:**
  ```go
  func BuildParamForm(title string, fields []ParamField, values *map[string]string) (*huh.Form, error)
  func RunParamForm(title string, fields []ParamField) (map[string]string, error)
  ```
  `BuildParamForm` constructs the form, does not run it (testable in isolation). `RunParamForm` wraps build + run via a test seam.
- [ ] type mapping:
  - `FieldTypeString`/`FieldTypePath` → `huh.NewInput()` (`Path` with `Suggestions` if available)
  - `FieldTypeInt` → `Input + Validate(strconv.Atoi)`
  - `FieldTypeBool` → `huh.NewSelect[string]().Options("false", "true")` — **order matters.** `huh.Select.Options(...)` writes the selected option into the bound value on render, so if the value pointer starts empty (optional bool, no `Default`, no `--set`), the FIRST option wins. Putting `"false"` first ensures the safe default. When `field.Default != ""`, pre-set the bound value to it before constructing the form so huh selects the matching option. Add a test case for an optional bool with no default → submit without interacting → values map yields `"false"` (NOT `"true"`).
- [ ] pattern validation: `re, err := regexp.Compile(field.Pattern)` (NOT `regexp.MustCompile` — `resolve.Params` already validates patterns lazily at `resolve.go:55-58` returning errors, and command-loader validation does not currently catch invalid regex; a panic here would crash the CLI on a malformed user pattern). On compile error, `BuildParamForm` returns `fmt.Errorf("param %q: invalid pattern %q: %w", name, pattern, err)`. Add an invalid-regex test case asserting `BuildParamForm` returns the error without invoking huh.
- [ ] required validation: `huh.NewInput().Validate(func(s string) error { if s=="" {return ErrRequired}; ... })`
- [ ] show ALL fields (required + optional) — pre-filled from `field.Default`
- [ ] test seam: package-level var `runFormFn = (*huh.Form).Run` — override in tests; **subtests overriding it MUST NOT call `t.Parallel()`** (global state across goroutines)
- [ ] form cancel (`huh.ErrUserAborted` or equivalent — verify against `go.mod` huh version) → return `ui.ErrCancelled` (consistent with `RunSelector` / `RunConfirm` today)
- [ ] unit tests in `internal/ui/paramform_test.go`: table-driven, named subtests per `ParamFieldType`, pattern violation, missing required, cancel, bool/int validation. Add a top-level `runFormFn` snapshot/restore helper in `TestMain` or per-test `defer` so test runs are hermetic.
- [ ] `make test ./internal/ui/...` — must pass

### Task 9: Confirmation summary and `ConfirmRun`
- [ ] in `internal/ui/confirm.go` add `ConfirmRun(title string, values map[string]string) (bool, error)` — receives the **already-rendered** confirmation title (caller is responsible for `${param.*}` template expansion) and the values map for the summary. Renders summary lines `key = value` above `title`, then a standard `huh.NewConfirm`.
  - rationale: `internal/ui/` must not import `tpl`/`config`/`usercommands` — that would invert the layering. Orchestrator owns rendering (Task 10).
- [ ] if `len(values) == 0` → fall back to existing `RunConfirm(title, "Yes", "No")` without a summary
- [ ] helper `renderParamSummary(values)` — plain text with `=` alignment (sorted keys for determinism)
- [ ] **cancel contract:** Esc / Ctrl+C → return `(false, ui.ErrCancelled)`. Mirrors the existing `RunSelector` / `RunConfirm` semantics. Orchestrator (Task 10) is responsible for translating this to `exit 0` via `errors.Is(err, ui.ErrCancelled)`. Do NOT normalize to `(false, nil)` here — that would prevent the caller from distinguishing "user pressed No" from "user cancelled the prompt entirely", which matters for future telemetry/logging.
- [ ] unit tests in `internal/ui/confirm_test.go` (extend): summary content with sorted keys, behavior with empty values, cancel → `(false, ui.ErrCancelled)` (assert via `errors.Is`), user picked No → `(false, nil)`, user picked Yes → `(true, nil)`, title is passed through verbatim (no template expansion at this layer — caller's responsibility)
- [ ] `make test ./internal/ui/...` — must pass

### Task 10: Orchestrator — `runCommandByID` in `command_cmd.go`
- [ ] extract `runCommandByID` in `internal/command/command_cmd.go` with this exact signature (all I/O channels and project root passed in — test-friendly):
  ```go
  type runOpts struct {
      Inspect   bool
      Yes       bool      // user-explicit --yes; OR'd with TUI y-toggle at the call site (see below)
      SetValues []string  // raw "k=v" entries from --set
  }

  func runCommandByID(
      ctx context.Context,
      stdin io.Reader,
      stdout io.Writer,
      stderr io.Writer,
      cfg *config.DevboxConfig,
      reg *usercommands.Registry,
      projectRoot string,        // workDir for BuildRunContext; resolved from flags.ProjectRoot() at the call site
      id string,
      opts runOpts,
  ) error
  ```
  Call site: `RunE` of `commands [id]` and the TUI flow. `stdin = cmd.InOrStdin()`, `stdout = cmd.OutOrStdout()`, `stderr = cmd.ErrOrStderr()`.

- [ ] **Parent `commands [id]` `RunE` has TWO routes — inspect vs run.** Inspect requires an exact id (no selector, allow private; mirrors current `newCommandInspectCmd` at `command_cmd.go:81-130`). Run keeps the existing selector flow with `ModeRun, includePrivate=false` (mirrors `newCommandRunCmd` at `command_cmd.go:156-228`). cfg/reg are loaded **inside `RunE`**, NOT by `PersistentPreRunE` — current code does this at `command_cmd.go:157-164`/`99-105`; the schema-bypass `PersistentPreRunE` for `validate` is a separate special case documented in CLAUDE.md and does NOT apply here.
  ```go
  RunE: func(cmd *cobra.Command, args []string) error {
      reg, err := loadCommandRegistry(flags.configPath)
      if err != nil { return err }

      // ---- Inspect route: exact id required; private allowed; config errors tolerated ----
      if inspectFlag {
          if len(args) == 0 {
              return errors.New("id required with --inspect")
          }
          // No selector, no group resolution. Inspect of a bare group prefix or unknown
          // id is an error — the user opted out of the interactive flow.
          // Tolerate config errors (best-effort) like the existing inspect subcommand
          // at command_cmd.go:105: cfg is only used for ui.commands.* defaults, which
          // are nil-safe.
          cfg, _ := config.LoadConfig(flags.configPath)
          return runCommandByID(
              cmd.Context(),
              cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
              cfg, reg, flags.ProjectRoot(), args[0],
              runOpts{Inspect: true},
          )
      }

      // ---- Run route: existing selector behavior ----
      cfg, err := config.LoadConfig(flags.configPath)
      if err != nil { return fmt.Errorf("loading config: %w", err) }

      // Signal-aware cancellation — preserves existing behavior at command_cmd.go:221.
      // Required so parallel groups and child docker/exec processes (spawned through
      // runtime/runner_*.go) receive SIGTERM via exec.CommandContext when the user
      // hits Ctrl-C during `runUserCommand`. Installed before the form / confirmation
      // so the handler is armed by the time we reach the runner; huh forms have their
      // own Ctrl-C handling via huh.ErrUserAborted (NOT via this ctx — `runParamForm`
      // does not accept a context). Inspect route does NOT need this — it's a
      // synchronous print with no child processes.
      ctx, stop := notifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)  // package-level seam
      defer stop()

      var skipFromTUI bool
      selector := makeBrowserSelector(cfg, cmdbrowser.ModeRun, false, &skipFromTUI)
      if !ui.IsInteractiveFn(cmd.InOrStdin()) {
          selector = func(_ []*usercommands.CommandDef, _ string) (string, error) {
              return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
          }
      }
      id, err := resolveCommandID(reg, args, false, selector)
      if err != nil {
          if errors.Is(err, ui.ErrCancelled) { return nil }
          return err
      }
      return runCommandByID(
          ctx,  // signal-aware, NOT cmd.Context()
          cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
          cfg, reg, flags.ProjectRoot(), id,
          runOpts{
              Yes:       yesFlag || skipFromTUI,   // CRITICAL: fold TUI y-toggle into opts.Yes
              SetValues: setFlags,
          },
      )
  },
  ```
  Behaviors deliberately preserved from today:
  - `commands` (no args, no `--inspect`): TUI run selector, public only
  - `commands services.main` (group prefix, no `--inspect`): TUI filtered, public only
  - `commands db.cli` (exact id, no `--inspect`): direct run, private guard applies in `runCommandByID`
  - `commands -i db.cli` (exact id, `--inspect`): direct inspect, private allowed
  - `commands -i` (no args, `--inspect`): error `id required with --inspect`
  - `commands -i services.main` (group prefix, `--inspect`): error from `reg.Get` (id not found) — group prefix is not a valid command id; the user must specify a full id when bypassing the selector
  - non-TTY + no args + no `--inspect`: error matching today's `command_cmd.go:167-170` message
- [ ] **TUI `y` propagation** — the cmdbrowser `y` toggle continues to set `Result.SkipConfirm` (captured by `makeBrowserSelector` into `skipFromTUI` at `command_cmd.go:278-280`). The orchestrator only sees `opts.Yes`, so the call site MUST do `Yes: yesFlag || skipFromTUI`. Forgetting this OR silently breaks the TUI y toggle. Alternative considered and rejected: adding a separate `runOpts.SkipFromTUI` field — the orchestrator has no behavior difference between the two sources, so a single combined `Yes` is cleaner.
- [ ] **flow:**
  - `def, err := reg.Get(id)` (registry API is `Get`, not `Lookup` — `internal/usercommands/registry/registry.go:134`). On error, return wrapped error.
  - **Private guard** (preserve existing semantics — `command_cmd.go:183`): if `def.Private && !opts.Inspect` → return `fmt.Errorf("command %q is private and cannot be run directly", id)`. Inspect MAY proceed for private commands (matches existing inspect subcommand behavior).
  - `opts.Inspect` → `printInspect(stdout, def)` and `return nil`
  - **Two-flag decision** — distinguishes "can we show huh UI" from "should we skip prompts entirely". Mirrors current `command_cmd.go:204-216` semantics where `NonInteractive` is gated by env/flag, NOT by stdin TTY-ness (preserves the non-TTY `Y/n` fallback at `confirmation.go:58`):
    ```go
    nonInteractiveEnv := os.Getenv("DEVBOX_NONINTERACTIVE") == "1" || os.Getenv("DEVBOX_NONINTERACTIVE") == "true"
    skipPrompts  := opts.Yes || nonInteractiveEnv             // → rctx.SkipConfirm AND rctx.NonInteractive
    canPromptHuh := ui.IsInteractiveFn(stdin) && !skipPrompts  // → may we show huh form / huh confirm?
    ```
    Semantics:
    - `skipPrompts=true` → suppress ALL prompting (huh AND non-TTY Y/n fallback)
    - `skipPrompts=false && canPromptHuh=true` → use huh form + huh confirm
    - `skipPrompts=false && canPromptHuh=false` → no huh (stdin is a pipe), but `RunCommand`'s internal `ConfirmCommand` still prints the non-TTY `Y/n` fallback — preserve today's behavior
  - `provided := parseSetFlags(opts.SetValues)` → `map[string]string`; report `--set k` (no `=`) as an error here
  - `prefilled := resolve.ParamDefaults(def.Params, provided, cfg)` (Task 7.5 — raw strings, no coercion, no required-check)
  - `missing_required := []string{}` from `def.Params` where `Required && prefilled[name] == ""`
  - **form decision** uses `canPromptHuh` (not the missing two-flag combo from previous draft):
    - `!canPromptHuh` AND `len(missing_required) > 0` → return error listing missing keys (matches today's behavior — missing required params under non-interactive/`-y` is a hard error)
    - `!canPromptHuh` AND all filled → `values := prefilled`, skip form
    - `canPromptHuh` AND `len(def.Params) > 0` → run form (next bullet)
    - `canPromptHuh` AND `len(def.Params) == 0` → `values := prefilled` (empty), skip form
  - form branch:
    ```go
    fields := paramFieldsFromDef(def, prefilled)  // Task 8 adapter: model.ParamDef → []ui.ParamField
    values, err := runParamForm("devbox commands › "+def.ID, fields)  // package-level seam; see test-seams bullet below
    if err != nil {
        if errors.Is(err, ui.ErrCancelled) { return nil }  // user pressed Esc; exit 0
        return err
    }
    ```
  - **build RunContext, attach I/O channels, render confirmation text, prompt** (prevents `${param.*}` template regression — see `confirmation.go:30-37` and `confirmation_test.go:122-129`). Use the real `BuildRunContext` signature from `usercommands.go:241`: `(cfg, reg, def, with map[string]any, workDir string) (RunContext, error)` — synchronous, no ctx parameter, `with` is `map[string]any`:
    ```go
    with := make(map[string]any, len(values))
    for k, v := range values { with[k] = v }
    rctx, err := usercommands.BuildRunContext(cfg, reg, def, with, projectRoot)
    if err != nil { return err }  // surfaces resolve.Params errors

    // CRITICAL: attach I/O channels — runner.go:58-64 falls back to os.Stdout/Stderr/Stdin
    // when these are nil, breaking test output capture and any wrapping (e.g. cmd.OutOrStdout()).
    rctx.Stdin  = stdin
    rctx.Stdout = stdout
    rctx.Stderr = stderr

    if def.Confirmation && canPromptHuh {
        title := def.EffectiveConfirmationText()
        if rctx.Render != nil {
            rendered, rerr := tpl.RenderCommand(title, rctx.Render)
            if rerr != nil { return fmt.Errorf("render confirmation_text: %w", rerr) }
            title = rendered
        }
        // CRITICAL: build the summary from normalized rctx.Params (post-resolve),
        // NOT from raw form `values`. resolve.Params treats empty user input as
        // missing and falls back to DefaultFrom/Default — so a form field left
        // blank shows "" in `values` but resolves to the default in rctx.Params.
        // Showing `values` here would lie to the user about what actually runs.
        summary := stringifyParams(rctx.Params)  // map[string]any → map[string]string via fmt.Sprintf("%v", v)
        ok, cerr := confirmRun(title, summary)   // package-level seam; see test-seams bullet below
        if cerr != nil {
            if errors.Is(cerr, ui.ErrCancelled) { return nil }  // Esc on confirmation → exit 0
            return cerr
        }
        if !ok { return nil }  // user picked No → exit 0
        rctx.SkipConfirm = true  // prevents runtime/runner.go:148 → confirmation.go:22 re-prompt
    } else {
        // Skip-or-fallback path: skip if -y/env says so; otherwise RunCommand's internal
        // ConfirmCommand handles the non-TTY Y/n fallback (preserves current behavior).
        rctx.SkipConfirm    = skipPrompts
        rctx.NonInteractive = skipPrompts
    }

    return runUserCommand(ctx, rctx)  // package-level seam; see test-seams bullet below
    ```
  - rationale for build-first ordering: `BuildRunContext` runs `resolve.Params` (final coerce + required + pattern), guaranteeing validation by the time we render. The form already ran `huh` per-field `Validate`, so this is the safety net catching anything that slipped through. The render context is also the only thing that can expand `${param.task}` correctly.
  - **`ConfirmRun` returns `(false, ui.ErrCancelled)` on Esc** (per Task 9). Orchestrator handles cancellation by checking `errors.Is(err, ui.ErrCancelled)` and returning `nil` — exit 0 with no runner invocation. Do NOT propagate `ErrCancelled` as a process error.
  - **`ConfirmRun` signature `(title, values)`** — orchestrator renders the title against `rctx.Render`, not `ui.ConfirmRun`. Keeps `internal/ui/` free of `tpl`/`config`/`usercommands` imports (same rule as Task 8's UI DTO).
  - `usercommands.RunCommand(ctx, rctx)` — its internal `ConfirmCommand` no-ops because `SkipConfirm=true` (huh-confirmed path) or `SkipConfirm=true && NonInteractive=true` (env/`-y` path). In the remaining `skipPrompts=false && canPromptHuh=false` case (pipe stdin, no `-y`, no env), `ConfirmCommand` runs its non-TTY `Y/n` fallback via `render.NewWriter(rctx.Stdout).Confirm(message, rctx.Stdin)` — which is why I/O channels MUST be attached before this call.

- [ ] **Add four package-level seams in `internal/command/command_cmd.go`** so tests can stub heavy dependencies AND the signal handler installation without touching `internal/ui` globals or sending real signals. Production assignments are pure pass-throughs:
  ```go
  // Test seams — overridden in command_cmd_test.go. Subtests that override these
  // MUST NOT call t.Parallel() (global state across goroutines).
  var (
      runParamForm   = ui.RunParamForm                 // (title string, fields []ui.ParamField) (map[string]string, error)
      confirmRun     = ui.ConfirmRun                   // (title string, values map[string]string) (bool, error)
      runUserCommand = usercommands.RunCommand         // (ctx context.Context, rc usercommands.RunContext) error
      notifyContext  = signal.NotifyContext            // (parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc)
  )
  ```
  The parent `RunE` calls `ctx, stop := notifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)` instead of `signal.NotifyContext(...)` directly. Without these, the test cases in this task cannot stub the form, confirmation, runner dispatch, or signal-handler registration — same pattern as `runtime/confirmation.go:14` (`var runConfirm = ui.RunConfirm`) and `internal/ui/selector.go:26` (`runSelectFormFn`). Critically, the `notifyContext` seam eliminates the need to send real `SIGINT` from tests, which would kill the test process if the implementation regresses and fails to wrap the context.
- [ ] call from the parent `commands [id]` `RunE` (passing `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`)
- [ ] call from the TUI flow (`RunCommandBrowser` → result → `runCommandByID`)
- [ ] wired unit tests in `command_cmd_test.go` — stub the three command-package seams declared above (`runParamForm`, `confirmRun`, `runUserCommand`); each captures call args and counts invocations. Fresh `cmd := newCommandCmd(flags)` (or `newRootCmd(flags)` for alias / parent-routing cases) per subtest. Note: do NOT confuse these with the `internal/ui/paramform.go` seam `runFormFn` from Task 8 — that one is for testing `RunParamForm` in isolation; here we stub one layer up:
  - all params required, all via `--set`, no `-y` → form is shown with pre-filled values
  - all params required, all via `--set`, with `-y` → form skipped
  - missing required + `-y` → error
  - missing required + non-TTY → error
  - form cancel → exit 0, runner not invoked
  - confirmation cancel → exit 0, runner not invoked
  - command without Params + TTY → form skipped, straight to confirmation/run
  - inspect path: `-i <id>` writes the inspect output to `cmd.OutOrStdout()` (assert buffer content)
  - **test scaffold for canPromptHuh-true cases:** `ui.IsInteractiveFn` (at `internal/ui/interactive.go:14`) is a package var that requires `*os.File` stdin AND TTY stdout — a `bytes.Buffer` stdin cannot make it return `true`. Subtests asserting the canPromptHuh-true branch MUST snapshot/restore the var per-test (NOT in `TestMain`, so non-interactive subtests still see the real implementation):
    ```go
    orig := ui.IsInteractiveFn
    t.Cleanup(func() { ui.IsInteractiveFn = orig })
    ui.IsInteractiveFn = func(io.Reader) bool { return true }
    ```
    Pattern mirrors `runConfirm` snapshot/restore at `runtime/confirmation_test.go:143-145`. As with `runParamForm`/`confirmRun`/`runUserCommand`, subtests overriding `ui.IsInteractiveFn` MUST NOT call `t.Parallel()`.
  - **no-double-prompt regression:** confirmation-enabled command in TTY path → `confirmRun` invoked exactly once (count == 1, NOT 2). This guards against `RunCommand` re-prompting via its internal `ConfirmCommand` after the orchestrator already confirmed.
  - **non-TTY + `-y`:** confirmation-enabled, stdin is a pipe, `--yes` set → orchestrator does NOT call `confirmRun`; `rctx.NonInteractive=true`, `rctx.SkipConfirm=true`. Stubbed `runUserCommand` sees the runner invoked once with both flags set. Stubbed `confirmRun` count == 0.
  - **non-TTY without `-y` (Y/n fallback preserved):** confirmation-enabled, stdin is a pipe, no `-y`, no `DEVBOX_NONINTERACTIVE` → orchestrator does NOT call `confirmRun` (`canPromptHuh=false`); `rctx.NonInteractive=false` and `rctx.SkipConfirm=false`. Stubbed `runUserCommand` sees the runner invoked with both flags FALSE so `RunCommand`'s internal `ConfirmCommand` will take the non-TTY Y/n branch at `confirmation.go:58`. This guards the existing pipe-stdin fallback against regression.
  - **I/O channel attachment:** stub `runUserCommand` capturing the `rctx` it receives. Assert `rctx.Stdin == stdin`, `rctx.Stdout == stdout`, `rctx.Stderr == stderr` (the three writer/reader the orchestrator received). Guards against the test capture / fallback-to-`os.Stdout` regression.
  - **private guard:** `def.Private == true`, no `--inspect` → returns error matching `command "X" is private`; runner not invoked.
  - **inspect of private:** `def.Private == true`, `--inspect` set → `printInspect` writes to `stdout`; no error, runner not invoked.
  - **inspect routing table** (parent `RunE` tests, fresh command tree per case via `newRootCmd(flags)`):
    - `commands -i` (no args) → error `id required with --inspect`; no selector invoked
    - `commands -i <exact-id>` → `printInspect` output for that id; no selector invoked
    - `commands -i <group-prefix>` (e.g. `services.main`) → error from `reg.Get` (id not found); no selector invoked
    - `commands -i <private-id>` → success (no private guard in inspect path); assert `printInspect` output
    - `commands -i <unknown-id>` with broken `devbox.yml` → still succeeds for valid id case OR returns the `reg.Get` error; cfg load errors are tolerated (asserts the inspect-route's best-effort cfg load mirrors `command_cmd.go:105`)
    - `commands` (no args, no `-i`) → selector invoked, `ModeRun`, `includePrivate=false`
    - `commands <group-prefix>` (no `-i`) → selector invoked, filtered to group
    - `commands <exact-id>` (no `-i`) → no selector; direct run via `runCommandByID`
    - `commands` non-TTY (no args, no `-i`, stubbed `ui.IsInteractiveFn` returning `false`) → error matching today's "no exact command ID given..." message at `command_cmd.go:167-170`
  - **confirmation template rendering:** def with `ConfirmationText: "Run ${param.task}?"` + `--set task=cleanup` → stubbed `confirmRun` receives title `"Run cleanup?"` (NOT the unexpanded template). Mirrors `confirmation_test.go:122-129` at the orchestrator layer.
  - **summary uses normalized params:** def with optional param `mode` (Default: `"safe"`), form-stub returns `values["mode"]=""` (user cleared the field) → stubbed `confirmRun` receives `summary["mode"]="safe"`, NOT `""`. Asserts `stringifyParams(rctx.Params)` is the source, not raw `values`. Without this, the confirmation summary would lie about what the command receives — see `resolve.go:29-30` and `41-46` for the empty-as-missing fallback logic.
  - **`DEVBOX_NONINTERACTIVE` env override:** stub `ui.IsInteractiveFn` to return `true` (per the scaffold above), then `t.Setenv("DEVBOX_NONINTERACTIVE", "1")` → orchestrator must still pick the non-interactive branch (form skipped, `confirmRun` not called, `rctx.NonInteractive=true`, `rctx.SkipConfirm=true`). Verifies the env var participates in `skipPrompts` independently of stdin TTY status. Do NOT rely on real `IsInteractiveFn` reading buffer stdin — it always returns `false` for non-`*os.File`, making the assertion vacuous.
  - **cancel vs No distinction (regression):** confirmation-enabled command, canPromptHuh path — stub the command-package seam `confirmRun` (NOT `ui.ConfirmRun`; overriding `ui.ConfirmRun` from a `command_test` package would not affect the captured `confirmRun` variable):
    - stub `confirmRun` returning `(false, nil)` (user picked No) → `runCommandByID` returns `nil`, `runUserCommand` count == 0
    - stub `confirmRun` returning `(false, ui.ErrCancelled)` (user pressed Esc) → `runCommandByID` ALSO returns `nil` (not the cancelled error), `runUserCommand` count == 0. Asserts the `errors.Is(err, ui.ErrCancelled)` branch is wired.
    - stub `confirmRun` returning a generic error → `runCommandByID` propagates it.
  - **TUI y-toggle propagation:** simulate the TUI flow by calling `runCommandByID` with `opts.Yes=true` (mimicking the `yesFlag || skipFromTUI` combination from the parent `RunE`). Assert `confirmRun` not called and `rctx.SkipConfirm=true` reaches the runner. Mirrors the existing TUI-y behavior at `command_cmd.go:278-280` after the refactor.
  - **signal-aware context propagated (no real signals):** parent `RunE` test stubs the `notifyContext` seam to record requested signals and return a programmatically cancellable context. Asserting on the seam — not on a real `SIGINT` — eliminates the danger that a regression in the implementation (forgetting to wrap the context) lets the test's signal hit the default process disposition and kill the suite. If the implementation skips the wrapper, the `notifyContext` stub simply isn't called and the test fails normally via `signalsCh`/`cancelCh` being empty.
    ```go
    // Capture what the orchestrator requested + the cancel handle it received.
    signalsCh := make(chan []os.Signal, 1)
    cancelCh  := make(chan context.CancelFunc, 1)
    origNotify := notifyContext
    t.Cleanup(func() { notifyContext = origNotify })
    notifyContext = func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
        signalsCh <- sigs
        ctx, cancel := context.WithCancel(parent)
        cancelCh <- cancel
        return ctx, cancel
    }

    // Stub runUserCommand to expose the ctx it actually receives + block RunE.
    // A single sync.Once-guarded waitForDone() does both the unblock and the
    // drain-with-timeout, exactly once. It is called from both the success
    // path (so we can assert on execErr) and from t.Cleanup (so failure
    // paths still release + observe the goroutine). Channel `done` has cap 1
    // and exactly one sender, so a second drain attempt would block forever
    // — the sync.Once is what makes the double-call site safe.
    ctxCh := make(chan context.Context, 1)
    block := make(chan struct{})

    origRun := runUserCommand
    t.Cleanup(func() { runUserCommand = origRun })
    runUserCommand = func(ctx context.Context, rc usercommands.RunContext) error {
        ctxCh <- ctx
        <-block
        return nil
    }

    done := make(chan error, 1)  // buffered cap 1 — goroutine never blocks on send even if no one reads
    go func() { done <- cmd.Execute() }()

    var (
        waitOnce sync.Once
        execErr  error
    )
    waitForDone := func() {
        waitOnce.Do(func() {
            close(block)
            select {
            case execErr = <-done:
            case <-time.After(time.Second):
                // t.Errorf (NOT Fatal) — Fatal is not supported inside t.Cleanup,
                // and on the success path the test's main assertions have already run.
                t.Errorf("cmd.Execute() goroutine did not return within 1s after unblock")
            }
        })
    }
    t.Cleanup(waitForDone)

    // 1. Assert signals registered.
    select {
    case sigs := <-signalsCh:
        require.ElementsMatch(t, []os.Signal{syscall.SIGINT, syscall.SIGTERM}, sigs)
    case <-time.After(time.Second):
        t.Fatal("notifyContext not called")
    }

    // 2. Assert runUserCommand received the wrapped ctx.
    var capturedCtx context.Context
    select {
    case capturedCtx = <-ctxCh:
    case <-time.After(time.Second):
        t.Fatal("runUserCommand not entered")
    }

    // 3. Cancel the fake ctx through the captured handle — no real signals involved.
    cancel := <-cancelCh
    cancel()
    select {
    case <-capturedCtx.Done():
    case <-time.After(time.Second):
        t.Fatal("ctx not cancelled after notifyContext cancel")
    }

    waitForDone()            // drain on the success path
    require.NoError(t, execErr)
    ```
    Three assertions in one test: (a) `notifyContext` was called with the right signal set, (b) the ctx flowing into `runUserCommand` is the wrapped one (not raw `cmd.Context()`), (c) cancelling the wrapped ctx propagates to the runner. Replacing real-signal delivery with a seam stub makes the test deterministic and safe under `go test -race`.

    **Goroutine lifecycle guarantees:**
    - `waitForDone()` is `sync.Once`-guarded so cleanup-side and success-side calls do NOT double-drain `done` (which has cap 1 and one sender — a second receive would block until the 1s timeout and falsely report a regression on every passing test).
    - The cleanup registered after `done` is created runs FIRST (LIFO order): closes `block`, drains `done` with a 1s bound — guarantees the `cmd.Execute()` goroutine has actually exited before the test function returns. Without the drain step, a `t.Fatal` between `go func()` and the success-path `waitForDone()` would let the goroutine continue running into subsequent tests in the same package.
    - The seam-restore cleanups (`runUserCommand = origRun`, `notifyContext = origNotify`) run AFTER the drain cleanup, so by the time globals are restored the goroutine is guaranteed not to be inside the stub closure anymore.
    - `t.Errorf` (NOT `t.Fatalf`) is used inside the once-block — `Fatal*` is not supported in `t.Cleanup`, and on the success path the test's main assertions have already run.
  - **inspect route skips signal setup:** stub `notifyContext` with a counter; invoke the inspect path; assert the counter is 0 (`notifyContext` was never called). Safe to write because the assertion goes through the seam — no real signals, no process-disposition risk. Complements the `runUserCommand` count == 0 assertion from the inspect routing-table cases.
- [ ] `make test ./internal/command/...` — must pass
- [ ] `make lint` — no new issues

### Task 11: Cleanup runtime — verify no duplicate prompting
- [ ] in `internal/usercommands/runtime/runner.go` (and related runner files) re-check that there is no interactive prompt for params (discovery shows only the confirmation prompt, see `confirmation_test.go`)
- [ ] if found — remove and verify the confirmation prompt still works (it continues to be invoked from `runtime` via `ConfirmCommand` until the new flow is integrated; or it migrates to the orchestrator)
- [ ] if nothing is found — task is NO-OP, mark complete with a note
- [ ] add a regression test: invoking the runner with already-filled params + `skip_confirm=true` does NOT read stdin (verifiable via a `Runner.Stdin` stub)
- [ ] `make test ./internal/usercommands/...` — must pass

### Task 12: Verify acceptance criteria
- [ ] all Overview requirements implemented (help bar visible, palette customizable via `styles.yml`, `commands [id]` works, `cmd` alias, `-i/--inspect`, parameter form with pre-filled values, confirmation summary)
- [ ] `make test` — entire suite green
- [ ] `go test -race ./...` — pass under race detection (live view + new orchestrator paths threading ctx into runners)
- [ ] `make lint` — all issues resolved
- [ ] `make build` — builds cleanly
- [ ] `devbox docs generate --scope cli` locally, verify `docs/reference/cli/` updates without unexpected diffs (run ONLY locally; commit separately if the user decides to)

### Task 13: [Final] Update documentation
- [ ] `docs/reference/config/styles.md` — Command browser section (Task 5 created it — final pass here)
- [ ] `docs/reference/config/commands.md` — updated CLI surface, examples with `--set` / `-i` / `-y`
- [ ] `AGENTS.md` (= `CLAUDE.md` symlink): refresh the `internal/ui/cmdbrowser/` blurb (if `styles.go` grew new public surface) and the `internal/command/` blurb (removal of `run`/`inspect` subcommands, addition of `runCommandByID`)
- [ ] final self-check of this plan file: every box `[x]`, every newly added ➕ task documented

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

**Styles (Block A) — lipgloss v1/v2 boundary:**
- The codebase imports **both** lipgloss versions: `github.com/charmbracelet/lipgloss` (v1) in `internal/ui/` and `charm.land/lipgloss/v2` in `internal/ui/cmdbrowser/`. `lipgloss.Style` values are NOT interchangeable across versions.
- All new palette keys are color strings (`#hex`, ANSI 0–255, named). `LoadStylesConfig` remains lenient YAML; it returns a zero-value config when the file is missing and does NOT inject defaults.
- Defaults live next to the existing v1 palette vars in `internal/ui/styles.go` and are applied via `ApplyStyles(cfg)` (existing pattern — preserved).
- Shared API is **string-typed**: `ui.Color*() string` accessors return the raw color string. Both v1 and v2 lipgloss can consume strings via `lipgloss.Color(s)`. This is the only sanctioned bridge between the two versions.
- `internal/ui/cmdbrowser/palette.go` is the single location that constructs v2 `lipgloss.Style` values from `ui.Color*()` strings. Everything inside `cmdbrowser/` uses v2 styles built locally; nothing imports v1 styles. Conversely, v1 code outside `cmdbrowser/` keeps using existing v1 `Style*` accessors.
- Accessor semantics — functions, not globals, so re-loading `styles.yml` (via `ApplyStyles`) picks up changes for subsequent renders without restart.

**CLI and forms (Block B):**
- `--set` is parsed as `key=value`; missing `=` → error indicating the offending entry.
- Pattern validation: `regexp.Compile` (NOT `MustCompile`) once inside `BuildParamForm`; on error return `fmt.Errorf("param %q: invalid pattern %q: %w", ...)`. Matches Task 8's contract and `resolve.go:55-58` semantics. Never panic on a user-authored pattern.
- Form cancel: `huh.NewForm(...).Run()` returns `huh.ErrUserAborted` or equivalent (confirm against the version in `go.mod`); map to `ui.ErrCancelled`.
- TTY detection via existing `ui.IsInteractiveFn` + `DEVBOX_NONINTERACTIVE` check.
- Inspect printing — reuse the existing `newCommandInspectCmd` body (extract into `printInspect(w, def)` before deleting the subcommand).

**Compatibility:**
- BREAKING: `devbox commands run` and `devbox commands inspect` subcommands are removed. No users exist yet — no deprecation runway needed. Commit prefix: `refactor(commands)!: ...` with a clear description.

## Post-Completion

*Items requiring manual intervention or external systems — informational only*

**Manual verification:**
- Visually verify the TUI in three modes: default palette, custom `styles.yml` with overridden `focus_border` / `description`, dark and light themes.
- Run `devbox commands` in a real project with commands of various types (shell / workflow / service_exec) — confirm the help bar is readable, the param form does not break the alt-screen, and the confirmation summary is legible.
- Test the fallback ladder: `tput cols 60` / `70` / `90` / `120` — UI and form behave correctly at each width.
- Test non-TTY: `devbox commands db.create --set database=app < /dev/null` (or with `DEVBOX_NONINTERACTIVE=1`).

**External system updates:**
- None — devbox CLI is standalone.

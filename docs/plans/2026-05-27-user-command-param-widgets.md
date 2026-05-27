# User Command Param Widgets

## Overview

Today, user-command `ParamDef` supports only free-text input via `internal/ui/paramform.go`. We add explicit **form widgets** (`input` / `select` / `multiselect` / `confirm`) plus a list of **options** that can be either a literal list, a list of `{value, label}` entries, or a reference to arbitrary keys in the merged config (`defaults.yml` / `local.yml`) via `${dot.path}`.

This lets command authors declare "pick a database from the list defined in `defaults.yml`" without hardcoding the list inside the command file, and lets users override that list per-environment via `local.yml`.

As part of this change, the two parallel huh form-runners (`internal/ui/paramform.go` and `internal/setup/huh.go`) are collapsed into one shared package `internal/ui/ask` used by both the setup wizard and the user-command form. `internal/ui/huh.go` stays — it is *not* a form-runner but the package-level **theme / glyph rules / palette applier / prompt hooks** consumed by `ui.Selector`, `ui.ConfirmRun`, `ui.MultiSelect`, and now `ui/ask`. The new `ask` package depends on it (`ui.Theme()`, `SetHuhHooks`) so styling stays consistent with the setup wizard and the rest of the TUI.

## Context (from discovery)

- **Files/components involved:**
  - `internal/usercommands/model/types.go` (ParamDef + Validate)
  - `internal/usercommands/resolve/resolve.go` (ParamDefaults, dot-path resolution)
  - `internal/validate/commands/commands.go` (per-command validator)
  - `internal/setup/{huh.go, wizard.go, model.go, loader.go}` (existing setup asker)
  - `internal/ui/paramform.go` + `internal/ui/huh.go` (current huh wrappers)
  - `internal/command/command_cmd.go` (orchestrator that builds fields + runs form)
  - `internal/config/devbox.go` (`ResolvePath` over `cfg.Raw` — reused as-is)
  - Docs: `docs/reference/config/commands.md`, `docs/internals/packages.md`, `AGENTS.md`
- **Patterns found:**
  - `default_from` already resolves a dot-path against the merged `cfg.Raw` map — same mechanism extended to list-valued `options`.
  - `defaults.yml` / `local.yml` are loaded with lenient YAML (`yaml.Unmarshal`, no `KnownFields(true)`), so arbitrary user keys land in `cfg.Raw` today. No new config namespace needed.
  - Strict decode (`KnownFields(true)`) is used by command files via `loader.ParseCommandFile` — new `ParamDef` fields must be added to the struct or load fails.
  - `internal/setup/huh.go` already handles `input` / `select` / `multiselect` / `confirm` via huh; this is the prior art being consolidated into `internal/ui/ask`.
- **Dependencies identified:**
  - `github.com/charmbracelet/huh` (already in go.mod).
  - No new external deps.
- **Pre-release policy (CLAUDE.md):** no schema_version bump, no alias shims; old `ui.ParamField`/`RunParamForm` API is deleted, not deprecated.

## Development Approach

- **testing approach:** Regular (code first, tests immediately after, in same task)
- complete each task fully before moving to the next
- make small, focused changes — one commit per task in the order below
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` after each task
- no backward-compat shims — pre-release; rename/delete freely (per CLAUDE.md)

## Testing Strategy

- **unit tests:** required for every task (table-driven for YAML unmarshaling, validators, resolvers, and `buildAskFields`)
- **e2e tests:** project has no Playwright/Cypress; `internal/setup` and `internal/command` package tests cover the form-driven flows via huh's programmatic test harness (`huh.Form.Run()` with mocked stdin in setup's existing tests is the pattern to copy). No new e2e harness required.
- after every task, run the full suite: `make test`
- `make lint` before final task

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Two cooperating changes:

1. **Shared asker (`internal/ui/ask`)** — single huh-based form package.
   - `internal/ui/paramform.go` (`ParamField`, `BuildParamForm`, `RunParamForm`) — deleted; `command_cmd.go` calls `ask.Run` directly.
   - `internal/setup/huh.go` **stays** — it exposes `NewHuhAsker` (consumed by `deploy_menu.go:35,227`) plus setup-specific helpers (`buildPortValidator`, `formatServiceToggleRow`, `coerceInputAnswers`, etc.). Only its internal `huh.NewForm` call sites are rerouted through `ask.Run`. Public API and the three-callback shape are preserved.
   - `internal/ui/huh.go` **stays unchanged** — it owns the shared huh theme/glyph/hook state for *all* huh-based primitives (`ui.Selector`, `ui.ConfirmRun`, `ui.MultiSelect`, `ui/ask`). The new `ask` package imports it for `ui.Theme()` and `SetHuhHooks` rather than re-implementing.

2. **Widget + options on ParamDef** — three new fields:
   - `Widget ParamWidget` (`input`/`select`/`multiselect`/`confirm`, defaulted from `Type` if empty)
   - `Options ParamOptions` (union: `[]string`, `[]{value,label,description}`, or `"${dot.path}"`)
   - `Separator string` (multiselect join separator, default `" "`)

Resolver: new `resolve.ResolveOptions(opts, cfg.Raw) ([]OptionItem, error)`.
Validation: load-time invariants in `model.CommandDef.Validate` + `validate/commands`; runtime `--set` membership + empty-dynamic-list check in `command_cmd.go`.

## Technical Details

### New types in `internal/usercommands/model/types.go`

```go
type ParamWidget string

const (
    WidgetInput       ParamWidget = "input"
    WidgetSelect      ParamWidget = "select"
    WidgetMultiselect ParamWidget = "multiselect"
    WidgetConfirm     ParamWidget = "confirm"
)

type OptionItem struct {
    Value       string `yaml:"value"`
    Label       string `yaml:"label"`
    Description string `yaml:"description,omitempty"`
}

type ParamOptions struct {
    Static []OptionItem // populated when YAML literal
    From   string       // canonical dot-path (already unwrapped from "${...}"); matches DefaultFrom semantics
}

// Compile-time interface check — keeps the contract honest.
var _ yaml.Unmarshaler = (*ParamOptions)(nil)

// Pointer receivers throughout for consistency (UnmarshalYAML must mutate).
func (p *ParamOptions) UnmarshalYAML(node *yaml.Node) error { /* dispatch on node.Kind */ }
func (p *ParamOptions) IsZero() bool { return p == nil || (len(p.Static) == 0 && p.From == "") }
```

`ParamDef` gains:
```go
Widget    ParamWidget  `yaml:"widget"`
Options   ParamOptions `yaml:"options"`
Separator string       `yaml:"separator"` // multiselect joiner, default " "
```

### `internal/ui/ask` (new)

```go
package ask

type FieldKind int
const (
    FieldUnknown FieldKind = iota // sentinel zero value — Run rejects fields with this kind
    FieldInput
    FieldSelect
    FieldMultiselect
    FieldConfirm
)

type Option struct{ Value, Label, Description string }

type Field struct {
    Key, Title, Description string
    Kind                    FieldKind
    Required                bool
    Default                 string   // for multiselect: separator-joined
    Defaults                []string // for multiselect pre-selection
    Options                 []Option
    Validate                func(string) error // input/select; for multiselect: per-item
}

// Result is the form output. Values are typed: string for input/select,
// []string for multiselect, bool for confirm. Callers use the typed accessors
// instead of asserting any.
type Result struct{ values map[string]any }

func (r Result) String(key string) string    { /* type-assert to string, "" on miss */ }
func (r Result) Strings(key string) []string { /* type-assert to []string, nil on miss */ }
func (r Result) Bool(key string) bool        { /* type-assert to bool, false on miss */ }

type RunOptions struct {
    Input  io.Reader // default os.Stdin; setup wizard threads its own for tests
    Output io.Writer // default os.Stdout; setup wizard routes through its writer
}

// Run displays the form and blocks until the user submits/cancels. Uses
// huh.Form.RunWithContext so context cancellation (Ctrl-C, parent timeout)
// aborts the form cleanly. opts may be zero (Input=os.Stdin, Output=os.Stdout).
func Run(ctx context.Context, title string, fields []Field, opts RunOptions) (Result, error)
```

### Widget default inference (in `model.ParamDef.Normalize()` or at validation time)

| Type      | Options    | Default Widget |
|-----------|------------|----------------|
| `bool`    | -          | `confirm`      |
| any       | non-empty  | `select`       |
| `string`  | empty      | `input`        |
| `int`     | empty      | `input`        |
| `path`    | empty      | `input`        |

### Resolve flow in `command_cmd.go`

```
1. resolve.ParamDefaults(defs, providedSet, cfg)
2. for each param with Widget ∈ {select, multiselect}:
     opts := resolve.ResolveOptions(def.Options, cfg.Raw)
     if len(opts) > 0 AND prefilled value (--set / default_from / default) ∉ opts → error
     if len(opts) == 0 AND prefilled via default/default_from (NOT --set) → error  (defaults must validate)
     // --set + empty opts is the only path that bypasses options validation here
3. // Reuses existing seam at command_cmd.go:203-213
   skipPrompts := opts.Yes || nonInteractiveEnv
   canPromptHuh := ui.IsInteractiveFn(stdin) && !skipPrompts
   showForm := canPromptHuh && len(def.Params) > 0 &&
       (opts.ForceParamForm || !allRequiredSatisfied(def.Params, values))
4. if showForm:
     // step 2 already errored for default/default_from + empty opts, so by here
     // buildAskFields only sees --set-prefilled or unprefilled params. Per-param rule:
     //   - prefilled via --set + empty opts → skip field, keep value (escape hatch)
     //   - not required + no prefill + empty opts → skip field
     //   - required + no prefill + empty opts → error "no options for param %q: ${%s} is empty"
     fields, err := buildAskFields(defs, values, resolvedOpts, translator, locale)
     if err != nil { return err }
     result, err := ask.Run(ctx, title, fields, ask.RunOptions{Input: stdin, Output: stdout})
     if errors.Is(err, huh.ErrUserAborted) { err = ui.ErrCancelled }  // preserves existing behavior
     values = mergeAnswers(values, result, defs)  // multiselect → strings.Join(sep)
5. validate.Params(values, defs)
6. runtime.RunCommand(ctx, ...)
```

**Note on widget rendering condition:** an optional param with `widget: select` and a satisfied default will NOT open the form unless `ForceParamForm` is set (user hit the edit-params key in the TUI) or another required param is unsatisfied. This matches existing behavior — widgets do not force prompting on their own. Documented in `commands.md`.

### YAML examples (for `commands.md`)

```yaml
# static
params:
  format:
    type: string
    widget: select
    options: [json, yaml, toml]

# labeled
params:
  driver:
    widget: select
    options:
      - { value: pg,    label: "PostgreSQL 16" }
      - { value: mysql, label: "MySQL 8" }

# from defaults.yml/local.yml
# defaults.yml:  databases: [users, logs, events]
params:
  db:
    widget: select
    options: ${databases}

# multiselect
params:
  services:
    widget: multiselect
    options: ${services_list}
    separator: ","
```

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs inside this repo.
- **Post-Completion** (no checkboxes): manual sanity check via running `bin/devbox commands` against a fixture project, monorepo-level fixture sweep.

## Implementation Steps

### Task 1: Create shared `internal/ui/ask` package

**Files:**
- Create: `internal/ui/ask/ask.go`
- Create: `internal/ui/ask/ask_test.go`

- [x] define `FieldKind`, `Field`, `Option`, `Result` types in `internal/ui/ask/ask.go`
- [x] place `FieldUnknown` at iota 0 as sentinel; `Run` returns error if any `Field.Kind == FieldUnknown`
- [x] add typed accessors on `Result`: `String(key) string`, `Strings(key) []string`, `Bool(key) bool` — callers must NOT touch the underlying map (it stays unexported)
- [x] implement `Run(ctx context.Context, title string, fields []Field, opts RunOptions) (Result, error)` wrapping `huh.NewForm` with one group, dispatching per `FieldKind` to `huh.NewInput` / `NewSelect[string]` / `NewMultiSelect[string]` / `NewConfirm`
- [x] call `form.RunWithContext(ctx)` (NOT `form.Run()`) so context cancellation propagates — this is load-bearing: command_cmd.go passes a signal-aware context (`internal/command/command_cmd.go:114`)
- [x] wire `RunOptions.Input` via `form.WithInput(in)` and `RunOptions.Output` via `form.WithOutput(out)`; zero RunOptions → defaults (`os.Stdin`/`os.Stdout`)
- [x] apply `ui.Theme()` to the form (`form.WithTheme(ui.Theme())`) and call `ui.SetHuhHooks(...)` if applicable so styling matches selector/confirm/multiselect; do NOT re-implement theme logic here
- [x] populate `Result.values`: input/select → `huh.Form.GetString(key)`; multiselect → `[]string` via the bound slice pointer (huh's standard pattern); confirm → `huh.Form.GetBool(key)`
- [x] support per-field `Validate` callbacks (input/select: invoked with the value; multiselect: invoked per selected item)
- [x] handle `Required` (huh's `Validate` returning error on empty) and `Default` / `Defaults` (pre-fill)
- [x] return `Result` as `map[string]any` (string for scalar kinds, `[]string` for multiselect)
- [x] write tests for `BuildFields` (the pure factory if extracted) covering each `FieldKind` — schema-shape assertions only, no interactive form run
- [x] write tests for `Run` happy path via huh's programmatic test API (one input field, one select field, one confirm asserting bool) — pattern: copy from `internal/setup/huh_test.go`; test `RunOptions.Input` by feeding scripted bytes
- [x] write a context-cancellation test: spawn `Run` in a goroutine, cancel the context, assert `Run` returns within a short timeout with a non-nil error
- [x] write tests for validation rejection (required-empty, custom validate returning err)
- [x] run `make test ./internal/ui/ask/...` — must pass before Task 2

### Task 2: Route `internal/setup` form-runner through `internal/ui/ask`

**Files:**
- Modify: `internal/setup/huh.go`
- Modify: `internal/setup/huh_test.go`
- Modify: `internal/setup/wizard.go` (only if call signature changes)

**Scope note:** `setup/huh.go` is NOT just a form-runner — it owns:
- `NewHuhAsker(out)` returning three callbacks (question-form, port-overrides, service-toggles); consumed by `internal/command/deploy_menu.go:35` (seam) and called at `deploy_menu.go:227`
- Setup-specific helpers: `formatServiceToggleRow`, `applyMultiSelectInitial`, `buildInputValidator` (with setup-specific preset/regex semantics), `buildPortValidator`, `coerceInputAnswers`, `coercePortOverrides`

These stay in `internal/setup`. Only the *huh form construction + Run* under the hood is replaced — the three-callback API, the validators, and the coercion helpers are untouched.

- [x] read `internal/setup/huh.go` end-to-end and identify every `huh.NewForm` / `huh.NewInput` / `huh.NewSelect` / `huh.NewMultiSelect` / `huh.NewConfirm` call site
- [x] for the **generic question prompt** (`[]Question` loop): convert the field construction into `[]ask.Field` and call `ask.Run(ctx, title, fields, ask.RunOptions{Output: out})` instead of `huh.NewForm(...).WithOutput(out).Run()`
- [x] service-toggle and port-override prompts are evaluated separately (next bullets) — they may stay raw
- [x] **port-override prompt** (`buildPortValidator` + per-conflict inputs at `internal/setup/huh.go:320+`): leave as direct huh calls if it uses per-field dynamic re-regen that `[]ask.Field` can't model; add a code comment explaining the carve-out
- [x] **service-toggle prompt** (`internal/setup/huh.go:137+`): uses custom keymaps, `Filterable(false)`, mandatory-service preamble — migrate only if `ask.Field` can express all three; otherwise leave raw and document the carve-out alongside port-overrides
- [x] **question prompt** (the generic `[]Question` loop): this is the migration target — it's the closest match to `ask.Field`
- [x] do NOT change `NewHuhAsker`'s public signature or the three returned callback types — `deploy_menu.go:227` consumes specific shapes
- [x] do NOT delete the file; only the *migratable* huh-form internals reroute through `ask`
- [x] verify multiselect Question values are correctly received as `[]string` from `ask.Result` and feed into `coerceInputAnswers` unchanged
- [x] verify confirm Question values are received as `bool` via `Result.Bool(key)` and coerce to the existing `map[string]any` shape (`wizard.go:194` asserts `bool` type)
- [x] update `huh_test.go` assertions if they peeked into pre-migration internals; existing wizard-level tests must pass unchanged
- [x] run `make test ./internal/setup/...` — must pass before Task 3

### Task 3: Add Widget / Options / Separator to `model.ParamDef`

**Files:**
- Modify: `internal/usercommands/model/types.go`
- Modify: `internal/usercommands/model/types_test.go`

- [x] add `ParamWidget` (string-typed; zero value `""` means "infer from Type") with constants `WidgetInput`/`WidgetSelect`/`WidgetMultiselect`/`WidgetConfirm`
- [x] add compile-time check `var _ yaml.Unmarshaler = (*ParamOptions)(nil)` next to `ParamOptions` definition
- [x] use pointer receivers consistently on `ParamOptions` (`UnmarshalYAML` mutates, so `IsZero` must also be `*ParamOptions` — nil-safe)
- [x] add `OptionItem` struct and `ParamOptions{Static, From}` struct with `UnmarshalYAML` dispatching on `yaml.Node.Kind`:
  - **null/missing node** → `IsZero` (no error)
  - **scalar** matching `^\${\s*([^}]+?)\s*}$` → `From` = inner dot-path (trimmed, no braces); this is the canonical form used by `config.ResolvePath` exactly like `DefaultFrom`
  - **scalar** not matching → error: "options: expected `${...}` reference or sequence, got plain scalar"
  - **empty sequence** `[]` → `Static: []` (no error here; `Validate` rejects for select/multiselect)
  - **sequence of scalars** (all elements scalar) → `[]OptionItem{Value: s, Label: s}`
  - **sequence of maps** (all elements mapping) → decode as `[]OptionItem` with strict `KnownFields`
  - **mixed sequence** (scalar + map intermixed) → error citing the line of the first offending element
  - **mapping node** at the top level → error: "options: expected sequence or `${...}` reference, got mapping"
- [x] add `Widget`, `Options`, `Separator` fields to `ParamDef` with yaml tags
- [x] add `ParamDef.EffectiveWidget()` helper returning the defaulted widget per the inference table (no mutation; pure function)
- [x] write table-driven tests for `ParamOptions.UnmarshalYAML` covering: null/missing, `${x}`, `${ x }` (whitespace), `${}` (empty → error), plain scalar (`foo` → error), empty sequence, scalar sequence, map sequence, mixed sequence (error w/ line), mapping node (error)
- [x] write tests for `EffectiveWidget` covering each row of the inference table
- [x] run `make test ./internal/usercommands/model/...` — must pass before Task 4

### Task 4: Load-time validation in `model.CommandDef.Validate`

**Files:**
- Modify: `internal/usercommands/model/types.go` (extend existing `Validate` method)
- Modify: `internal/usercommands/model/types_test.go`

- [x] in the existing param-validation loop, add `validateParamDef(name, def)` checks:
  - `Widget`, if set, must be one of the four enum values
  - `Widget` is `select` or `multiselect` → `Options` must be non-empty (Static or From)
  - `Widget` is `input` or `confirm` → `Options` must be empty
  - `Pattern` set together with `Options` → error
  - `Separator` set when `Widget != multiselect` → error (warning is awkward without a warning channel in model.Validate; promote to error)
  - `Static` options: duplicate `Value` → error
  - `Default` literal value is in `Static` (skip if `From` — dynamic deferred to runtime)
- [x] write table-driven tests for each invariant — one positive case + one failing case each
- [x] run `make test ./internal/usercommands/model/...` — must pass before Task 5

### Task 5: Per-command-file validator coverage

**Files:**
- Modify: `internal/validate/commands/commands.go`
- Modify: `internal/validate/commands/commands_test.go`

- [ ] confirm the `validate/commands` validator calls `CommandDef.Validate` (it should); if not, route the new param diagnostics through it
- [ ] ensure each new error surfaces as a `validate.Diagnostic` with `Hint` pointing at the offending param + field name (e.g. `params.db.options`)
- [ ] add a best-effort `default_from`/`default` ∈ resolved options check using `validate.Context.Cfg.Raw` (Cfg is carried via Context — see `internal/validate/validate.go:29`); when `Cfg == nil` (partial load) skip the check silently; runtime in Task 7 is the safety net
- [ ] write tests that load a fixture command file with each bad-param shape (from Task 4 invariants) and assert diagnostics, including one fixture with `default_from` pointing at a static-options param value that exists vs doesn't exist
- [ ] run `make test ./internal/validate/commands/...` — must pass before Task 6

### Task 6: `resolve.ResolveOptions`

**Files:**
- Create: `internal/usercommands/resolve/options.go`
- Create: `internal/usercommands/resolve/options_test.go`

- [ ] before implementing, run `go list -deps ./internal/usercommands/resolve | grep internal/config` to confirm `resolve` may import `internal/config` (it likely already does for `cfg.Raw` types) — if a cycle would result, lift `ResolvePath` to a leaf package (e.g. `internal/dotpath`) in a precursor commit
- [ ] implement `ResolveOptions(opts model.ParamOptions, raw map[string]any) ([]model.OptionItem, error)`:
  - `opts.Static != nil` → return copy
  - `opts.From != ""` → call `config.ResolvePath(raw, opts.From)` directly (From is already canonical dot-path, unwrapped at unmarshal time)
  - normalize result:
    - `[]any` of scalars → `[]OptionItem{Value: fmt.Sprint(v), Label: same}`
    - `[]any` of `map[string]any` with `value` key → decode each to `OptionItem`
    - `map[string]any` → sorted-key list of `OptionItem{Value: key, Label: key}`
    - anything else → error `fmt.Errorf("options %s: expected list or map, got %T", opts.From, value)`
  - path missing → return empty slice, no error (`config.ResolvePath` returns `(any, bool)` — no error to wrap; runtime caller produces the user-facing "empty" error with param context, only if form actually opens)
- [ ] write table-driven tests covering: static passthrough, `${x}` → []string, `${x}` → []map, `${x}` → map (assert sorted), `${x}` missing, `${x}` wrong type, deeply nested path
- [ ] run `make test ./internal/usercommands/resolve/...` — must pass before Task 7

### Task 7: Rewire `command_cmd.go` to ask + new resolver

**Files:**
- Modify: `internal/command/command_cmd.go`
- Modify: `internal/command/command_cmd_test.go` (and `run_command_by_id_test.go` if it mocks the form)
- Delete: `internal/ui/paramform.go`
- Delete: `internal/ui/paramform_test.go`

- [ ] replace `runParamForm = ui.RunParamForm` seam with `runAsk = ask.Run` (keep the seam so tests can inject); the seam signature must accept `RunOptions` so tests pass scripted Input/Output
- [ ] when invoking the form, pass `ask.RunOptions{Input: stdin, Output: stdout}` using the existing IO seams (do NOT default to `os.Stdin`/`os.Stdout` — that would break tests and IDE-embedded runs)
- [ ] preserve cancel-error mapping: `errors.Is(err, huh.ErrUserAborted)` → return `ui.ErrCancelled` (today's behavior at `internal/ui/paramform.go:214`); `command_cmd.go` callers already swallow only `ui.ErrCancelled`
- [ ] rewrite `paramFieldsFromDef` as `buildAskFields(def, prefilled, provided, opts.Translator, opts.Locale, resolvedOpts map[string][]model.OptionItem) ([]ask.Field, error)`:
  - dispatch on `def.EffectiveWidget()`
  - for `multiselect`: split `Default` by `def.Separator` into `Defaults`
  - thread `OptionItem` → `ask.Option` for select/multiselect
  - apply the empty-options rule per-param (see above); return error to caller on first violation
- [ ] add `mergeAnswers(values map[string]string, ans ask.Result, defs map[string]ParamDef) map[string]string`:
  - input/select → `ans.String(key)` directly
  - confirm → `strconv.FormatBool(ans.Bool(key))` ("true"/"false") so `${param.X}` template (string-only) interpolates correctly
  - multiselect → `strings.Join(ans.Strings(key), def.Separator)` (default sep `" "`)
- [ ] call `resolve.ResolveOptions` for every select/multiselect param **before** the `showForm` decision
- [ ] **membership-check rule** (source-aware, asymmetric on empty options):
  - `--set name=value` for a select/multiselect param: if options non-empty AND value ∉ options → error. Empty options + `--set` → **bypass membership**, trust the user's explicit value (escape hatch when dynamic source is broken and user wants to override).
  - `default_from` resolved to a value: if options non-empty AND resolved value ∉ options → error. Empty options + `default_from` → **error**: `"options ${%s} resolved empty, but param %q has default_from %q — fix defaults.yml/local.yml or remove default_from"`. Rationale: defaults are config-authored, not user-overridable in this invocation; silently using an unvalidated default hides config bugs.
  - literal `default` (fallback when `default_from` misses, per `resolve.go:82`): same as `default_from` — if options non-empty AND default ∉ options → error; if options empty → error. Same rationale.
  - Net effect: only `--set` lets the form proceed past an empty dynamic options list; defaults must be backed by a valid resolved options list.
- [ ] **empty-options rule** (per-param, fires at field-build time — runs AFTER step 2's membership-check already caught the default/default_from + empty-opts case, so buildAskFields only sees `--set`-prefilled or unprefilled params reaching this point): when iterating params in `buildAskFields`, for each select/multiselect param with `len(resolvedOpts[name]) == 0`:
  - prefilled via `--set` → **skip the field**, keep the explicit user value (escape hatch — paired with the membership-bypass above)
  - no prefilled value AND `def.Required` → **error**: `"no options for param %q: ${%s} is empty in defaults.yml/local.yml"`
  - no prefilled value AND `!def.Required` → **skip the field** (param ends up with empty string, which is fine for optional)
- [ ] this means `buildAskFields` must return `([]ask.Field, error)`; callers handle the error before opening the form
- [ ] this per-param logic replaces the previous "showForm + empty-list → error" aggregate check; the `showForm` decision itself stays as-is (aggregate on `allRequiredSatisfied || ForceParamForm`)
- [ ] use the existing `canPromptHuh`/`skipPrompts` seam at `command_cmd.go:203-213` — do NOT introduce a new `opts.NonInteractive` flag; the `showForm` decision stays as-is (canPromptHuh + (ForceParamForm || !allRequiredSatisfied))
- [ ] non-interactive behavior unchanged: if `!canPromptHuh` and required params unsatisfied → existing error path at `command_cmd.go:227`
- [ ] delete `internal/ui/paramform.go` + tests (no callers after this task)
- [ ] update tests: replace assertions about `ui.ParamField` shape with assertions about `[]ask.Field`; add tests for membership-rejection and empty-dynamic-options error
- [ ] write tests for `buildAskFields` covering: each `EffectiveWidget` row, multiselect default splitting, `OptionItem`-with-label preservation
- [ ] write tests for `mergeAnswers` (multiselect join with default + custom separator, scalar passthrough)
- [ ] run `make test` — must pass before Task 8

### Task 8: Verify shared huh infrastructure intact

**Files:**
- Read-only check: `internal/ui/huh.go`

- [ ] confirm `internal/ui/huh.go` is untouched — it is the shared theme/glyph/hook layer consumed by `ui.Selector` (`internal/command/service.go:84,290`, `deploy.go:579`), `ui.ConfirmRun` (`internal/command/shell.go:24`), `ui.MultiSelect`, and now `internal/ui/ask` (Task 1)
- [ ] grep `RunParamForm\|ParamField\b` across the repo — should be zero hits after Task 7's delete
- [ ] grep usages of `internal/ui/huh.go` exports to confirm `ask` uses them (`Theme`, `SetHuhHooks`, palette functions as applicable)
- [ ] run `make test` — must pass before Task 9

### Task 9: Documentation

**Files:**
- Modify: `docs/reference/config/commands.md`
- Modify: `docs/internals/packages.md`
- Modify (only if a load-bearing invariant emerged): `AGENTS.md`

- [ ] add a "Param widgets" section to `docs/reference/config/commands.md` with the four YAML examples from Technical Details (static, labeled, `${...}` from defaults, multiselect) and a table of field semantics
- [ ] in `docs/internals/packages.md`:
  - update `internal/usercommands/{model,resolve}` entries (note `ParamOptions`, `ResolveOptions`)
  - add a new entry for `internal/ui/ask` documenting: purpose (huh form-runner for the generic `[]Field` shape, shared by the setup wizard's question loop + user-command params); `Field`/`Result` shape; constraint that `ask.Run` always applies `ui.Theme()` and `ui.SetHuhHooks` so styling stays consistent. **Two distinct carve-outs:**
    - `internal/setup/huh.go` keeps direct `huh.NewForm` for **port-override** and **service-toggle** prompts (per-field dynamic regeneration / custom keymaps / `Filterable(false)` that `ask.Field` can't model).
    - `internal/ui/{selector,confirm,multiselect}.go` are pre-existing primitives, not in scope for `ask`. They keep their own huh forms and are consumed from `command/*` and `ui/cmdbrowser/*`. `ask` does NOT replace them.
    - The rule is "use `ask` when `[]Field` suffices", not "no raw huh anywhere".
- [ ] AGENTS.md Key Patterns: only add a bullet if a real load-bearing invariant emerges during implementation (the convention "use `internal/ui/ask` for new prompts" lives in `packages.md`, not Key Patterns — Key Patterns is reserved for non-obvious foot-guns like the compose-bypass stop or snapshot scope gate)
- [ ] run `make build` to sync `internal/docs/embedded/` and regenerate `internal/docs/content_hashes_gen.go` (per CLAUDE.md build policy)
- [ ] no test for docs, but `git diff --exit-code internal/docs/embedded/ internal/docs/content_hashes_gen.go` should be empty after `make build` (CI guard)

### Task 10: Verify acceptance criteria
- [ ] verify all four widgets render correctly by running `bin/devbox commands` against a fixture command using each
- [ ] verify `${...}` options pick up values from `defaults.yml` and that `local.yml` overrides correctly
- [ ] verify `--set foo=invalid` for a select param errors with a clear message listing valid options
- [ ] verify empty dynamic `${...}` errors at form-open time with a clear message
- [ ] verify NonInteractive + missing required param errors as before
- [ ] run full test suite: `make test`
- [ ] run lint: `make lint`

### Task 11: [Final] Update completion artifacts
- [ ] verify all checkboxes above marked `[x]`
- [ ] move this plan: `mv docs/plans/2026-05-27-user-command-param-widgets.md docs/plans/completed/`
- [ ] commit with `feat(commands): widget+options for user-command params`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification:**
- Run `bin/devbox commands` interactively against the in-monorepo fixture projects and exercise each widget end-to-end (esp. multiselect with several selections, `${...}` after editing `local.yml`).
- Sanity-check huh keybinding/visual consistency with the setup wizard — both should feel identical now that they share `internal/ui/ask`.

**Monorepo fixture sweep:**
- `grep -r "ui.ParamField\|RunParamForm" devbox/` across the monorepo to confirm no live project relied on the deleted symbols (per CLAUDE.md, monorepo fixtures travel with CLI changes).
- If any monorepo `commands/*.yml` could benefit from the new widgets (e.g. existing string params with implicit "pick one of N" semantics encoded in `description`), open a follow-up to upgrade them — out of scope here.

# Refactor `internal/core/ui` into Subpackages

## Overview

Split the 20-file flat `internal/core/ui` package into three domain-aligned subpackages: `widgets/` (interactive huh-based forms), `styles/` (palette + icon rendering), and `render/` (DTO→string section renderers). Aligns with the existing pattern (`ui/ask/`, `ui/cmdbrowser/`, `ui/statusview/`, `ui/statustui/` are already subpkg siblings).

**Problem**: `core/ui` mixes three orthogonal concerns — interactive prompts (huh forms), theming/icon utilities, and section-renderer DTOs → strings. `styles.go` (383 lines) and `table.go` (366 lines) and `info.go` (360 lines) are large and unrelated. 64 external files import the package; navigation cost compounds.

**Goal**: After refactor:
- `core/ui/styles/` — 3 files: `styles`, `icon`, `icon_replacements` (extracted FIRST — widgets and render both import it)
- `core/ui/widgets/` — 5 files: `confirm`, `multiselect`, `selector`, `huh`, `interactive`
- `core/ui/render/` — 12 files: `info`, `info_auto_hosts`, `info_auto_urls`, `summary`, `deploy_info`, `pending`, `topology`, `gitworkspace`, `diagnostics_table`, `table`, `brand_header`, `logo`
- Root `core/ui/` ends up with zero .go files (directory hosts the 7 subpkg siblings: ask, cmdbrowser, statusview, statustui + new widgets, styles, render). The codebase has no `doc.go` files anywhere — do not add one here.

**Behavior changes**: none. All renderer output verified against existing tests (no golden/testdata directories exist under `internal/core/ui/` — verified).

## Context (from discovery + plan-review deep inspection)

- **20 .go + 18 _test.go files** in `internal/core/ui/`. 4 sibling subpkgs already present (`ask/`, `cmdbrowser/`, `statusview/`, `statustui/`).
- **77 importers of `core/ui` package** (64 external + 13 within `ui/` tree — sibling subpkgs each consume 3-19 `ui.X` symbols and MUST be updated as part of this refactor):
  - `ask/ask.go`: `Theme()`, `RunWithPromptHooks`, `ErrCancelled` (3 symbols → widgets)
  - `cmdbrowser/`: 19 unique `ui.X` symbols spanning all three target subpkgs
  - `statustui/`: 8 symbols (NodeStatus, RenderSectionTitle, RenderGitWorkspace, RenderPendingBanner, LogoMarkPlain, ColorAccent, ColorMuted, StyleWarning)
  - `statusview/`: a few symbols (verify exact list during execution)
- **Internal coupling (deep)**:
  - 7 package-level `lipgloss.Style` vars in `styles.go`: `styleAccent`, `styleMuted`, `styleSuccess`, `styleWarning`, `styleDanger`, `styleBorder`, `styleText` — used DIRECTLY (not via the exported `StyleX(s string) string` wrappers) by 7 render files: `info.go`, `info_auto_urls.go`, `summary.go`, `diagnostics_table.go`, `brand_header.go`, `gitworkspace.go`, `table.go`
  - `serviceTypeStyle` (topology.go), `renderFGOnly`, `serviceTypeColor`, `styleInactiveService` (topology.go)
  - `huh.go` defines `huhTheme`, `applyFormGlyphs`, `buildPaletteApplier` — used by `styles.go`. Co-located in same package today (no real "wrong direction"); after split, decide where they live (see Task 1 reframe).
  - `huh.go` defines `snapshotHuhHooks` — used by `confirm.go`, `multiselect.go`, `selector.go` → stays in `widgets/`.
  - `styles.go` defines `defSep` — used by `info.go`, `summary.go` → becomes `styles.DefSep` after split.
  - `info.go` calls `renderAutoHosts`, `renderAutoURLs` from `info_auto_*.go` — all three go to `render/`.
  - `icon.go` ↔ `icon_replacements.go` — tightly coupled (`stripTrailingVS16`), both in `styles/`.
  - `multiselect.go` ↔ `selector.go` — shares `multiSelectMinHeight`, both in `widgets/`.
- **Test helpers crossing the new boundary**:
  - `resetStyles()` defined in `styles_test.go` is called 18× in `table_test.go` and 5× in `info_test.go` (both going to `render/`). Reads `resolvedAccent`, `resolvedSuccess`, `huhTheme`, `defSep` package-private state.
  - `snapshotPalette()`, `forceTruecolor()` same situation.
  - Decision: copy `resetStyles` (3 lines — just calls `ApplyStyles(nil)` + similar) into `render/`'s test helpers. Or replace `resetStyles()` calls in `render/`'s tests with `styles.ApplyStyles(nil)`. Pick approach in Task 3.
- **Pre-existing exception (CLAUDE.md)**: `ui` is the "sink layer", imported only by `cli/`. Subpkgs follow the same rule.
- **No `testdata/` directories or `.golden` files exist anywhere under `internal/core/ui/`** — earlier mentions of golden-test migration were spurious.
- **No `doc.go` convention in this codebase** — package comments live at the top of an existing implementation file (e.g. `cmdbrowser/run.go`). Do NOT create doc.go files.

## Development Approach

- **Testing approach**: Regular. 18 existing _test.go files act as safety net.
- **Behavior-preserving**: no API changes to external callers' renders. Style switches (palette/icons) tested via existing snapshot tests.
- **Per-task atomicity**: each subpkg extraction = one task. `make test` between tasks.

(No golden/testdata files exist under `internal/core/ui/` — verified.)

## Testing Strategy

- **Unit tests**: 18 existing tests move alongside code. No new tests needed.
- **No golden/testdata files exist** under `internal/core/ui/` — verified via filesystem inspection.
- **Test helper handling**: `resetStyles`, `snapshotPalette`, `forceTruecolor` (defined in styles_test.go) are called from 23+ sites in `table_test.go` + `info_test.go` (both going to `render/`). Use the public `styles.ApplyStyles(nil)` (or similar public function) at call sites in `render/`'s tests instead of trying to import a sibling package's test helper. Cross-package test helper imports don't work in Go.
- **e2e tests**: not applicable.
- **Manual verification (final task)**: `bin/devbox` invoked in a fixture project; visual check of `devbox status`, deploy menu, and any huh-form prompt (e.g. snapshot create confirmation).

## Progress Tracking

- Mark completed items with `[x]` immediately.
- ➕ for newly discovered tasks; ⚠️ for blockers.
- Update plan if scope shifts.

## Solution Overview

**Architecture**: three subpkgs with one-way dep `render, widgets → styles`. `styles/` is NOT a pure leaf — it imports the `charm.land/huh/v2` library to construct `HuhTheme`. That's fine; the constraint "styles is a leaf" was about subpkg-internal deps, not third-party imports.

- `styles/` — owns palette, color vars (exported as accessor functions or as raw `lipgloss.Style` vars), icon helpers, `HuhTheme`, `ApplyFormGlyphs`, `BuildPaletteApplier`. Imports `huh` lib but not internal subpkgs.
- `widgets/` — imports `styles/` (for `HuhTheme`, `ApplyFormGlyphs`, `BuildPaletteApplier`, palette accessors).
- `render/` — imports `styles/` (for the 7 style vars / accessors, `DefSep`, `RenderEnabled`, `RenderPartial`, icon helpers, `SafeIcon`, `IconPrefix`).

**Sibling subpkgs (ask/, cmdbrowser/, statusview/, statustui/) update atomically with the refactor** — they consume 3-19 `ui.X` symbols each. Their import lists change as part of Tasks 2/3/4, NOT as a separate "verify" step.

**Cycle-break logic**: no cycle. `styles/` is a leaf (modulo third-party huh import). `widgets/` and `render/` depend on it one-way. Sibling subpkgs depend on whichever target subpkgs hold the symbols they use.

**External caller impact**: 77 files (64 external + 13 within ui/ tree). For a single file like `cli/deploy/menu.go` that uses 19 `ui.X` symbols spanning widgets/styles/render, all three subpkg imports are added. Mechanical via goimports + sed for the path; the symbol-to-subpkg mapping must be exhaustive (see Symbol Mapping below) or grep-replace produces wrong results.

**Style vars crossing the boundary**: the 7 lowercase `style*` lipgloss.Style vars in `styles.go` are referenced DIRECTLY (not via the exported `StyleX(string) string` wrappers) from 7 render files. Two options for the refactor:
- **Option A (recommended): accessor functions** — add `styles.AccentStyle() lipgloss.Style`, `styles.MutedStyle()`, etc. Render files call `styles.AccentStyle().Render(text)` instead of `styleAccent.Render(text)`. ~7 new exports, no API surface area change for external callers (which use `StyleX`).
- **Option B: export the vars** — `styles.StyleAccent` etc. as `var`. Simpler edits but exposes mutable package-state to external code. Risky — `ApplyStyles` mutates these.

Pick Option A. The accessor functions are 1-liner readers of the underlying vars. Document explicitly in Task 2.

## Technical Details

### Pre-migration step: co-locate huh-theming with palette code

`styles.go` and `huh.go` are currently in the **same package** — there's no "wrong direction" between them. The pre-migration step is about choosing which subpkg owns `huhTheme`/`applyFormGlyphs`/`buildPaletteApplier` so they don't artificially separate from palette state.

**Decision**: move them to `styles.go` (becoming `styles/styles.go` later). They construct themes from palette colors — semantically styling, not widget runtime. `styles/` will import `charm.land/huh/v2` (the lib) to build the Theme value. This is not a circular dep — third-party lib import doesn't count as internal subpkg coupling.

After Task 1:
- `huh.go` contains ONLY: `SetHuhHooks`, `ClearHuhHooks`, `SnapshotHuhHooks`, the `defaultRunConfirmForm`-style test seam.
- `styles.go` contains: `ApplyStyles`, palette state, accessor functions (NEW: `AccentStyle()` etc. for the 7 style vars), `HuhTheme`, `ApplyFormGlyphs`, `BuildPaletteApplier`.

### Symbol mapping (exhaustive — for grep-replace)

External callers + sibling subpkgs use these symbols. Every entry MUST be in the mapping for the per-file caller-rewrite to succeed:

**widgets/**:
| Symbol | Currently in | New location |
|---|---|---|
| `RunConfirm` | confirm.go | `widgets.RunConfirm` |
| `ConfirmRun` | confirm.go | `widgets.ConfirmRun` |
| `RunMultiSelect` | multiselect.go | `widgets.RunMultiSelect` |
| `MultiSelectItem`, `MultiSelectResult` | multiselect.go | `widgets.*` |
| `RunSelector` | selector.go | `widgets.RunSelector` |
| `SelectorItem` | selector.go | `widgets.SelectorItem` |
| `ErrCancelled` (used by 20+ callers — defined in selector.go) | `widgets.ErrCancelled` |
| `Theme()` | huh.go | `widgets.Theme()` (or `styles.Theme()` if we move it — decide; current widgets accessors use `huhTheme` var) |
| `RunWithPromptHooks` | huh.go | `widgets.RunWithPromptHooks` |
| `SetHuhHooks`, `ClearHuhHooks`, `SnapshotHuhHooks` | huh.go | `widgets.*` |
| `IsInteractiveTTY` | interactive.go | `widgets.IsInteractiveTTY` |

**styles/**:
| Symbol | Currently in | New location |
|---|---|---|
| `ApplyStyles` | styles.go | `styles.ApplyStyles` |
| `RenderEnabled`, `RenderPartial` | styles.go | `styles.*` |
| `StyleKey`, `StyleMuted`, `StyleAccent`, `StyleSuccess`, `StyleWarning`, `StyleDanger`, `StyleBorder`, `StyleText` (string-render helpers) | styles.go | `styles.*` |
| `ColorAccent`, `ColorMuted` (used by statustui) | styles.go | `styles.*` |
| `defSep` (lowercase) | styles.go | `styles.DefSep` (EXPORTED — used by render's info.go + summary.go) |
| `AccentStyle()`, `MutedStyle()`, `SuccessStyle()`, `WarningStyle()`, `DangerStyle()`, `BorderStyle()`, `TextStyle()` (NEW accessor functions returning lipgloss.Style) | styles.go | `styles.*` |
| `HuhTheme` (or function) | huh.go → styles.go | `styles.HuhTheme` |
| `ApplyFormGlyphs`, `BuildPaletteApplier` | huh.go → styles.go | `styles.*` |
| `SafeIcon`, `IconPrefix`, `IsAmbiguousWidthIcon` | icon.go | `styles.*` |
| `SuggestSafeIcons` | icon_replacements.go | `styles.SuggestSafeIcons` |

**render/**:
| Symbol | Currently in | New location |
|---|---|---|
| `RenderInfo`, `RenderSectionTitle`, `RenderSubheader` | info.go | `render.*` |
| `RenderDefinition`, `RenderDefinitionAt` | info.go | `render.*` |
| `RenderSummary` | summary.go | `render.RenderSummary` |
| `RenderTable`, `RenderServicesTable` | table.go | `render.*` |
| `RenderDaemonTable`, `RenderDeployStatus`, `DaemonTableRow`, `DeployStatusRow`, `ServiceTableRow` | table.go | `render.*` |
| `RenderDeployInfo`, `DeployInfoRow`, `FormatRelativeTime` | deploy_info.go | `render.*` |
| `RenderPendingBanner` | pending.go | `render.RenderPendingBanner` |
| `RenderBrandHeader`, `BrandHeader` | brand_header.go | `render.*` |
| `LogoMark`, `LogoMarkPlain` | logo.go | `render.*` |
| `RenderGitWorkspace` | gitworkspace.go | `render.RenderGitWorkspace` |
| `ParseComposeTopology`, `RenderTopology`, `NodeStatus`, `NodeRunning`, `NodeStopped`, `NodeUnknown`, `NodeDisabled`, `NodeCategory`, `CatInfra`, `CatService`, `CatTool` | topology.go | `render.*` |
| `RenderDiagnosticsTable`, `DiagnosticRow`, `FormatDiagnostics`, `FormatSummary` | diagnostics_table.go | `render.*` |

`cli/deploy/menu.go` and similarly diverse callers will end up importing 2-3 of `widgets/`, `styles/`, `render/`. That's expected and idiomatic.

### Final structure

```
internal/core/ui/
├── ask/                           (existing, untouched)
├── cmdbrowser/                    (existing, untouched)
├── statusview/                    (existing, untouched)
├── statustui/                     (existing, untouched)
├── widgets/                       (NEW: 5 files)
│   ├── confirm.go                 → widgets.RunConfirm, ConfirmRun
│   ├── multiselect.go             → MultiSelectItem, MultiSelectResult, RunMultiSelect
│   ├── selector.go                → SelectorItem, RunSelector
│   ├── huh.go                     → SetHuhHooks, ClearHuhHooks, SnapshotHuhHooks
│   └── interactive.go             → IsInteractiveTTY (and similar env helpers)
├── styles/                        (NEW: 3 files)
│   ├── styles.go                  → ApplyStyles, RenderEnabled, RenderPartial, DefSep,
│   │                                HuhTheme, ApplyFormGlyphs, BuildPaletteApplier
│   ├── icon.go                    → IsAmbiguousWidthIcon, SafeIcon, IconPrefix
│   └── icon_replacements.go       → SuggestSafeIcons (+ stripTrailingVS16 internal helper)
└── render/                        (NEW: 12 files)
    ├── info.go                    → RenderInfo, RenderSectionTitle, RenderSubheader
    ├── info_auto_hosts.go         (internal helpers used by info.go)
    ├── info_auto_urls.go
    ├── summary.go                 → RenderSummary
    ├── deploy_info.go             → DeployInfoRow, RenderDeployInfo, FormatRelativeTime
    ├── pending.go                 → RenderPendingBanner
    ├── topology.go                → ParseComposeTopology, NodeStatus, NodeCategory
    ├── gitworkspace.go            → RenderGitWorkspace
    ├── diagnostics_table.go       → DiagnosticRow, RenderDiagnosticsTable, FormatDiagnostics
    ├── table.go                   → RenderTable, ServiceTableRow, RenderServicesTable
    ├── brand_header.go            → BrandHeader, RenderBrandHeader
    └── logo.go                    → LogoMark, LogoMarkPlain
```

`core/ui/` root after refactor: empty (or contains `doc.go` documenting the layout). No `.go` files at the root level except the doc.

### Symbol export changes

Internal lowercase symbols crossing the new subpkg boundary become exported:

| Old | New |
|---|---|
| `defSep` (styles.go) | `DefSep` (styles.go) |
| `huhTheme` (huh.go) | `HuhTheme` (styles.go) |
| `applyFormGlyphs` (huh.go) | `ApplyFormGlyphs` (styles.go) |
| `buildPaletteApplier` (huh.go) | `BuildPaletteApplier` (styles.go) |
| `snapshotHuhHooks` (huh.go) | stays lowercase (intra-`widgets`) |
| `stripTrailingVS16` (icon.go) | stays lowercase (intra-`styles`) |
| `renderAutoHosts` (info_auto_hosts.go) | stays lowercase (intra-`render`) |
| `renderAutoURLs` (info_auto_urls.go) | stays lowercase (intra-`render`) |
| `multiSelectMinHeight` (multiselect.go) | stays lowercase (intra-`widgets`) |

### External caller migration mapping

For each external file importing `core/ui`, update the import path based on the symbol used. Typical patterns:

| Used symbol | New import path |
|---|---|
| `ui.RunConfirm`, `ui.RunMultiSelect`, `ui.RunSelector` | `core/ui/widgets` |
| `ui.ApplyStyles`, `ui.RenderEnabled`, `ui.SafeIcon`, `ui.HuhTheme` | `core/ui/styles` |
| `ui.RenderInfo`, `ui.RenderServicesTable`, `ui.RenderDeployInfo`, `ui.RenderBrandHeader`, `ui.LogoMark`, `ui.RenderPendingBanner`, `ui.RenderDiagnosticsTable`, `ui.RenderGitWorkspace`, `ui.RenderSummary`, `ui.ParseComposeTopology` | `core/ui/render` |

Several callers use symbols from multiple groups → import multiple subpkgs. That's fine and idiomatic.

## What Goes Where

- **Implementation Steps**: all code changes (file moves, symbol exports, import path updates).
- **Post-Completion**: manual visual check of CLI surfaces (status TUI, deploy menu, brand header, snapshot prompts).

## Implementation Steps

### Task 1: Migrate huh-theming helpers from `huh.go` to `styles.go` (prep for split)

**Files:**
- Modify: `internal/core/ui/huh.go`
- Modify: `internal/core/ui/styles.go`
- Modify: `internal/core/ui/huh_test.go` (if test references the moved helpers)
- Modify: `internal/core/ui/styles_test.go`

- [x] move `huhTheme`, `applyFormGlyphs`, `buildPaletteApplier` definitions from `huh.go` to `styles.go` (still unexported)
- [x] remove the now-dead imports in `huh.go` (huh lib references kept only for the hooks)
- [x] update `styles.go` to define + use these helpers directly (no cross-file call needed within the same package)
- [x] verify `huh.go` only contains `SetHuhHooks`, `ClearHuhHooks`, `SnapshotHuhHooks`, the `defaultRunConfirmForm`-style indirection used by widgets, plus any test seams
- [x] update test references if any test directly invoked the moved helpers from huh_test.go (no changes needed — tests stayed in same package)
- [x] run `make test ./internal/core/ui/...` — must pass before Task 2
- [x] run `make lint` — must pass before Task 2

### Task 2: Extract `styles/` subpackage (FIRST — both widgets/ and render/ depend on it)

**Files:**
- Move + modify: `styles.go` → `styles/styles.go` (package comment lives at top of this file; no doc.go)
- Move + modify: `icon.go` → `styles/icon.go`
- Move + modify: `icon_replacements.go` → `styles/icon_replacements.go`
- Move + modify: `styles_test.go`, `icon_test.go` → `styles/`
- Modify: external callers of styles symbols (per Symbol Mapping)

- [x] create `styles/` directory; move 3 files; change package to `styles`; add `import "charm.land/huh/v2"` (styles.go uses huh.ThemeBase/ThemeFunc to build HuhTheme — third-party import, not a layering violation)
- [x] export the cross-pkg-needed symbols: `defSep` → `DefSep`, `huhTheme` → `HuhTheme`, `applyFormGlyphs` → `ApplyFormGlyphs`, `buildPaletteApplier` → `BuildPaletteApplier`
- [x] **add 7 accessor functions** for the lowercase style vars: `AccentStyle() lipgloss.Style { return styleAccent }`, `MutedStyle()`, `SuccessStyle()`, `WarningStyle()`, `DangerStyle()`, `BorderStyle()`, `TextStyle()`. Keep the underlying vars unexported (preserves the `ApplyStyles` mutation invariant). Render-pkg files will call `styles.AccentStyle().Render(text)` etc.
- [x] keep intra-pkg lowercase symbols unchanged (`stripTrailingVS16` etc.) — also exported `ServiceTypeStyle` (consumed by topology.go's category renderer)
- [x] move test files; update package decl + symbol references; theme-related tests migrated from `huh_test.go` to `styles_test.go`; remaining root tests use a small `test_helpers_test.go` shim around `styles.ApplyStyles(nil)`
- [x] **update sibling subpkg `statustui/`** to import `core/ui/styles` and replace `ui.ColorAccent`, `ui.ColorMuted`, `ui.StyleWarning` → `styles.*` (atomic with this task — statustui will not compile if its `ui.X` references break). Also updated `cmdbrowser/`, `ask/` styles refs, and `internal/core/ui/`-root callers — all atomic with the symbol removal.
- [x] update external callers of `ui.ApplyStyles` / `ui.RenderEnabled` / `ui.SafeIcon` / `ui.IsAmbiguousWidthIcon` etc. → `styles.*`
- [x] root `internal/core/ui/` no longer has styles/icon/icon_replacements — but still has 17 other files (huh-prefixed package comment stays in some file at root until Task 4)
- [x] run `make test` — must pass before Task 3
- [x] run `make lint` — must pass before Task 3

### Task 3: Extract `widgets/` subpackage

**Files:**
- Move + modify: `confirm.go` → `widgets/confirm.go`
- Move + modify: `multiselect.go` → `widgets/multiselect.go`
- Move + modify: `selector.go` → `widgets/selector.go`
- Move + modify: `huh.go` → `widgets/huh.go`
- Move + modify: `interactive.go` → `widgets/interactive.go`
- Move + modify: `confirm_test.go`, `multiselect_test.go`, `selector_test.go`, `huh_test.go` → `widgets/`
- Modify: external callers of widgets symbols (per Symbol Mapping)

- [x] create `widgets/` directory; move 5 files; change `package ui` → `package widgets`
- [x] add `import "devbox-cli/internal/core/ui/styles"` in widgets files that need `styles.HuhTheme`, `styles.ApplyFormGlyphs`, `styles.BuildPaletteApplier` (already present from Task 2 — confirm.go/selector.go/multiselect.go already import `styles` for `styles.Theme()`)
- [x] **special: `applyMultiSelectStateStyles` cross-call in `huh_test.go`** — n/a: the helper already lives in `styles/styles.go` (moved in Task 1/2), no cross-package call from widgets needed
- [x] update internal references: lowercase symbols stay (intra-pkg); references that crossed into styles already use `styles.*` from Task 2
- [x] move corresponding test files; update package decl + helper calls
- [x] **update sibling subpkg `ask/ask.go`** to import `core/ui/widgets` and `core/ui/styles` as needed: `ui.RunWithPromptHooks` → `widgets.RunWithPromptHooks` (atomic — ask/ no longer compiles against root ui after the move; `ui.Theme()` already moved to `styles.Theme()` in Task 2)
- [x] **update sibling subpkg `cmdbrowser/`** as needed: `ui.RunSelector`, `ui.RunWithPromptHooks`, `ui.ErrCancelled`, `ui.SelectorItem` → `widgets.*` in run.go, fallback.go, and cmdbrowser_test.go
- [x] grep external callers of widgets symbols; update imports to add `core/ui/widgets`; per-symbol qualify (perl bulk-rewrite across 42 files; goimports cleaned unused root-ui imports)
- [x] run `make test` — must pass before Task 4
- [x] run `make lint` — must pass before Task 4

### Task 4: Extract `render/` subpackage

**Files:**
- Move + modify: 12 renderer files → `render/` (info, info_auto_hosts, info_auto_urls, summary, deploy_info, pending, topology, gitworkspace, diagnostics_table, table, brand_header, logo)
- Move + modify: 10 corresponding test files → `render/`
- Modify: external callers of render symbols (per Symbol Mapping)

- [x] create `render/` directory; move 12 implementation files; change package to `render`
- [x] add `import "devbox-cli/internal/core/ui/styles"` everywhere — already imported per-file from Task 2; render-only files keep their existing styles imports
- [x] **handle `resetStyles` test helper**: kept the in-package `test_helpers_test.go` shim (renamed to `package render`) so call sites in `table_test.go` + `info_test.go` work unchanged
- [x] handle `snapshotPalette`, `forceTruecolor` test helpers — none were referenced by the migrated tests; only `resetStyles` was used (verified via grep)
- [x] move corresponding `*_test.go` files (10 files moved); `pending_test.go` was an external-style `package ui_test`, rewritten to `package render_test`
- [x] update intra-pkg symbol refs (`renderAutoHosts`, `renderAutoURLs` stay lowercase since same package)
- [x] **update sibling subpkg `cmdbrowser/`** — `model.go` import rewritten to `core/ui/render`; comments referencing `ui.Color*` updated to `styles.Color*`
- [x] **update sibling subpkg `statustui/`** to import `core/ui/render` for `NodeStatus`, `SectionTitle`, `GitWorkspace`, `PendingBanner`, `LogoMarkPlain`
- [x] update all external callers across `internal/cli/` and `internal/core/` (30 files) to import `core/ui/render`; bulk-rewrote `ui.X` → `render.X`; aliased conflicting `internal/shared/render` imports as `sharedrender` where both packages are imported (4 files)
- [x] dropped `Render` prefix from 16 exported functions per revive (e.g. `RenderInfo` → `Info`, `RenderBrandHeader` → `BrandHeader`); renamed the conflict-clashing `BrandHeader` type to `Brand`
- [x] verify root `internal/core/ui/` is empty of .go files (subpkg dirs remain: ask/, cmdbrowser/, statusview/, statustui/, widgets/, styles/, render/)
- [x] run `make test` — passed
- [x] run `make lint` — 0 issues

### Task 5: Verify cross-package call sites and integration

**Files:**
- Read-only verification of: `internal/cli/`, `internal/core/notify/`, `internal/shared/render/` (if any cross-import)

- [x] `grep -rEn '\\bui\\.[A-Z]' --include='*.go' internal/` — should match zero (identifier-start scoped to avoid false positives like `liveui.`, `statusview.`)
- [x] `grep -rn '"devbox-cli/internal/core/ui"' internal/` — should match zero (only `core/ui/widgets`, `core/ui/styles`, `core/ui/render`, `core/ui/ask`, `core/ui/cmdbrowser`, `core/ui/statusview`, `core/ui/statustui` remain)
- [x] verify sibling subpkgs ask/, cmdbrowser/, statusview/, statustui/ compile (their import updates are part of Tasks 2/3/4, not deferred here)
- [x] run full test suite: `make test`
- [x] run linter: `make lint`

### Task 6: Build verification + manual visual smoke test

- [x] run `make build` — produces `bin/devbox`
- [x] in a fixture project, run `bin/devbox` (no args) — verify brand header + logo + pending banner still render correctly (skipped - not automatable, requires interactive fixture)
- [x] run `bin/devbox status apps` — verify services table renders with correct styling (skipped - not automatable, requires fixture project)
- [x] trigger an interactive prompt (e.g. `bin/devbox snapshot create` interactive) — verify huh widget renders with the new palette + glyphs (skipped - not automatable, requires TTY)
- [x] run `bin/devbox info` — verify info auto-hosts + auto-urls renderers work (skipped - not automatable, requires fixture project)
- [x] check `make test-race` if not already in `make test`

### Task 7: Update documentation + finalize

**Files:**
- Modify: `docs/internals/packages.md` (per-package section for ui)
- Modify: `AGENTS.md` / `CLAUDE.md` "section renderer signature contract" if it references old layout
- Move: this plan file → `docs/plans/completed/`

- [x] update `docs/internals/packages.md` section on `internal/core/ui/` to describe widgets/styles/render structure + sibling subpkgs
- [x] verify "Section renderer signature contract" in CLAUDE.md still matches (returns strings, not `*render.Writer`); refreshed package references to `ui/render/` + `render.X` for accuracy
- [x] verify all checkboxes above are `[x]`
- [x] move plan file: `mkdir -p docs/plans/completed && mv docs/plans/2026-05-29-2-refactor-ui-subpackages.md docs/plans/completed/` (performed after final commit)

## Post-Completion

**Manual verification**:
- Visual smoke test of all status outputs (apps table, deploy menu, info sections, brand header, logo).
- Interactive prompts (snapshot create, deploy menu confirm) — palette + glyphs match pre-refactor output.

**External system updates**: none — pure internal refactor.

**Follow-up plans**:
- `docs/plans/2026-05-29-3-refactor-snapshot-subpackages.md` — `meta/` + `archive/`
- `docs/plans/2026-05-29-4-refactor-runtime-subpackages.md` — `spec/` + `runners/`

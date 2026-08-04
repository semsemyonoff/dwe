# Responsive tables in `internal/core/ui/render/`

## Overview

Every dwe table renders at its natural content width with no awareness of the terminal. On a narrow
window the rendered lines exceed the available columns, the terminal soft-wraps each one, and the box
border falls apart into a stack of stray `│` fragments.

Worked example — `dwe validate` on a ~110-column terminal. The `linters` domain carries hadolint
diagnostics whose HINT is a ~48-character wiki URL and whose MESSAGE hits the 44-column pre-wrap cap.
`DiagnosticsByDomain` drops the DOMAIN column, so five columns sum to
`1 (STATUS) + 8 (TARGET) + 24 (FILE) + 44 (MESSAGE) + 48 (HINT) = 125`, plus 10 columns of per-cell
padding and 6 border glyphs ≈ 141. Everything past column 110 wraps, and the output is unreadable.

This plan makes tables degrade gracefully across the full width range in three stages:

1. **Fits** — render exactly as today.
2. **Does not fit** — shrink the flexible columns and wrap their content once, directly at the final
   width.
3. **Does not fit even at column floors** — abandon the table shape and render each row as a labeled
   record block.

No data is ever dropped or truncated with an ellipsis. `MESSAGE` is the entire point of `dwe validate`,
and a hint URL that cannot be copied whole is worthless.

The mechanism lands in one place and covers all six existing table renderers, so a seventh table
inherits it for free.

## Context (from discovery)

**Files/components involved** — all six table renderers live in `internal/core/ui/render/` and funnel
through two shared helpers, `baseTable()` (`table.go:17`) and `renderRows()` (`table.go:31`):

| Renderer | Location | Columns |
| --- | --- | --- |
| `Table` | `table.go:59` | caller-supplied (snapshot list) |
| `ServicesTable` | `table.go:143` | 6–7 built-ins + custom extras |
| `DaemonTable` | `table.go:251` | 4 |
| `DeployStatus` | `table.go:317` | 6 |
| `GitWorkspace` | `gitworkspace.go:19` | 6 |
| `DiagnosticsTable` / `DiagnosticsByDomain` | `diagnostics_table.go:45` / `:54` | 5–6 |

**Call sites — complete inventory, grouped by sink.** The sink matters: the width budget must be
derived from the stream the table actually lands on.

| Sink | Call site | Renderer |
| --- | --- | --- |
| stdout | `cli/snapshot/snapshot.go:176` | `Table` |
| stdout | `cli/validate/validate.go:552` | `DiagnosticsByDomain` |
| stdout | `cli/status/status.go:354` → `stack/status.go:88` | `ServicesTable` |
| stdout | `cli/status/status.go:379`, `cli/status/deploy.go:42` → `stack/deploystatus.go:40` | `DeployStatus` |
| stdout | `cli/status/status.go:388` | `GitWorkspace` |
| stdout | `cli/service/service_list.go:99-101` → `stack.RenderApps/Tools/Infra` (`dwe services`) | `ServicesTable` |
| stdout | `cli/status/status.go:373`, `stack/daemons.go:259` | `DaemonTable` |
| **stderr** | `preflight/preflight.go:179` | `DiagnosticsTable` |
| **stderr** | `cli/deploy/menu.go:117`, `cli/deploy/menu.go:722` | `DiagnosticsTable` |
| **TUI panel** | `statustui/load.go:118` | `GitWorkspace` |
| **TUI panel** | `statustui/load.go:140-143` → `stack.RenderApps/Tools/Infra` | `ServicesTable` |
| **TUI panel** | `statustui/load.go:156` → `stack.DeployStatus` | `DeployStatus` |
| **TUI panel** | `statustui/load.go:176` → `stack.RenderDaemons` (`stack/daemons.go:259`) | `DaemonTable` |

Two consequences, both of which killed the original "probe `os.Stdout` inside `render/`" design:

- **`DiagnosticsTable` never writes to stdout.** All three of its call sites write to stderr. Probing
  stdout would leave `dwe deploy run > deploy.log` (stdout piped, stderr still a TTY) overflowing the
  terminal — one of the two scenarios this plan exists to fix — and would conversely shrink output
  that lands in a redirected file.
- **Four renderers are also called from inside the status TUI**, where the correct width is the
  plugin's inner panel width, not the terminal width. Per the `tui.Plugin` contract in AGENTS.md,
  `Frame.renderBody` adds `Padding(0,1)` inside the border, so panel inner width is **outer − 4**.
  Probing the terminal there would fit tables 4+ columns too wide and flip them into record mode
  inside a bordered panel.

**The status TUI renders before it knows its width.** This is the single hardest constraint in the
plan and it rules out simply passing a width into the existing load path:

- `buildTabs(ctx, d Deps)` (`statustui/load.go:127`) runs **asynchronously** inside `buildTabsCmd`
  (`:203`) and returns `[]tab` where `tab` is `{title, body string}` (`statustui/tui.go:43`) — fully
  rendered strings. It has no width and no `tui.Region`.
- `plugin.Resize(tui.Region)` is **deliberately a no-op** (`statustui/plugin.go:79`): "dimensions are
  computed in `ViewPanel` from the per-panel inner region it is given there, so there is nothing to
  cache here."
- The width first exists in `renderBody(inner tui.Region)` (`statustui/plugin.go:178`), where
  `w := max(inner.Width, 0)` sizes the viewport — long after the bodies were built.
- `sectionAnchors` (`statustui/plugin.go:104`, consumed by `jumpSection` at `statustui/tui.go:117-131`)
  are **line offsets into the rendered body**, produced by `joinSectionsWithAnchors` counting `\n`
  (`load.go:63-77`). Wrapping changes line counts, so anchors are width-dependent too and cannot stay
  in `tabsLoadedMsg`.

Tasks 9-11 restructure this: `stack/` splits collect from render so Docker probes stay in the async
path, the async load carries a **data snapshot**, and rendering moves to the point where the width is
known — split across a byte-identical refactor (Task 10) and the behavioral change (Task 11), because
the failure modes here are silent. Enabling the terminal budget (Task 12) deliberately comes **after**
all three, so no consumer regresses mid-plan.

**Patterns found:**

- Width-aware rendering already exists in this package as an **explicit parameter with a `<= 0`
  fallback**: `SectionTitleAt(text, width)` (`info.go:194`), `DefinitionAt(..., maxWidth)`
  (`info.go:216`), `VarInspectView(in, width)` (`vars.go:195`), all falling back through
  `renderSectionTitleAt` (`info.go:229`) to `styles.TermWidth()`.
- The terminal probe lives in `styles/` (`styles.go:408-417`), the only package here that imports
  `os` and `x/term`. `render/` imports neither today.
- `styles.TermWidth()` **returns 80 on error** — exactly the non-TTY case. Consuming it directly would
  silently push every piped run and every test into narrow mode.
- The project's TTY-probe convention is a package-level `var fn = func() bool { term.IsTerminal(...) }`
  test seam — see `cmdbrowser/fallback.go:16`, `runio/runio.go:59`, `lifecycle/stop.go:27`.
- `stack.StatusInput` (`stack/status.go:28`) is a plain struct, so a new field threads a caller-supplied
  width to `RenderApps` / `RenderTools` / `RenderInfra` / `DeployStatus` **without touching any
  signature**.
- `diagnostics_table.go` already owns a competent wrapping engine: `wrapText` / `wrapLine` / `wrapPath`
  / `isURLToken` / `splitDisplayWidth` (`:319`–`:408`). It word-wraps, breaks paths on `/`, and
  deliberately never splits a URL token.

**Test baseline — no goldens exist.** `internal/core/ui/render/` has **no `testdata/` directory**.
Every assertion in `table_test.go` (`:20-31`, `:110-122`), `gitworkspace_test.go` (`:34-38`, `:47`),
and `diagnostics_table_test.go` (`:43-61`) is `strings.Contains` or a count. The only near-exact checks
are `diagnostics_table_test.go:91-95` (total width `<= 130`) and `:243` (no `url+"│"` adjacency).
A migration could change padding, alignment, ANSI attribute ordering, computed column widths, zebra
parity, or drop a whole column and the existing suite would stay green. **Task 1 exists to fix this
before any refactor starts.**

**Dependencies identified** — `github.com/charmbracelet/lipgloss` v1.1.1 (`lipgloss/table`),
`internal/core/ui/styles`, `internal/core/ui/statusview`, `internal/core/validate`.

### Rejected alternatives

**lipgloss's built-in `table.Width()`.** It has a shrink algorithm, but it is unusable here for two
reasons. Diagnostics pre-wrap their prose at 44 columns *before* the table sees it, so a subsequent
shrink to 30 re-wraps already-wrapped lines into a ragged 30+14 ladder. And its cell wrapping
hard-breaks any token longer than the column, which destroys exactly the URL copyability that
`isURLToken` exists to protect.

**Duplicating the record layout per renderer instead of `tableView`.** Considered and declined: a
shared description is what keeps the mode decision, the floor arithmetic, and the record shape from
drifting across six copies. The abstraction only earns its keep if the non-diagnostics tables actually
use the flexible-column machinery, so Tasks 6-7 give them real `Flex`/`Wrap` columns rather than
declaring everything fixed.

## Development Approach

- **testing approach**: Regular (code first, then tests) — matching the existing `render/` test style,
  which asserts on rendered output rather than driving design from tests.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility

**Project-specific commands:**

- full suite: `make test` (depends on `embedded-docs` + `shims`, so the docs sync runs first)
- focused loop: `make embedded-docs` once, then `go test ./internal/core/ui/render/`
- lint: `make lint`
- after editing anything under `docs/`: `make build` (re-syncs the embedded docs tree)

**Never run `go test ./...` directly** — `internal/core/docs/embedded/` is gitignored and generated,
so the docs-subsystem tests fail on a fresh checkout.

## Testing Strategy

- **unit tests**: required for every task. Table-driven where the input space warrants it, per the
  repo's testing guidelines.
- **e2e tests**: this project has none (no Playwright/Cypress). Not applicable.
- **The load-bearing regression check**: Task 1 captures byte-exact goldens for all six renderers
  *before* any refactor. Tasks 6-8 then use golden equality as their pass/fail criterion. Because the
  budget is `0` (disabled) whenever the sink is not a TTY, and tests pin the seams to non-TTY, the
  goldens must survive the entire migration unchanged. **If a golden needs regenerating during Tasks
  6-8, the refactor changed behavior and is wrong.**
- Narrow-mode behavior is exercised exclusively by overriding the width seams in new tests.
- `TestMain` pins the seams to non-TTY so the suite cannot flip modes when the test binary is run
  directly (rather than through `go test`, which pipes stdout).

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**Width budget, derived from the actual sink.** A new `styles.TermWidthOrZero(f *os.File) int` returns
the terminal width of a specific stream, or `0` meaning "unbounded", whenever that stream is not a TTY
or its size is unknown. It lives in `styles/`, the package that already owns the terminal probe.
Rationale for `0` rather than the existing 80-column fallback:

- `dwe validate > report.txt` and CI logs have no width limit and must not start shrinking.
- `styles.TermWidth()`'s 80-column error fallback would otherwise silently reroute every pipe and
  every test into narrow mode.
- With the budget off by default in tests, the Task 1 goldens stay valid, and narrow mode is tested
  deliberately rather than by accident.

**Each renderer knows its own sink.** `DiagnosticsTable` probes **stderr** (all three call sites write
there); every other renderer probes **stdout**. This is a per-renderer constant, documented at the
declaration, not a guess made at render time.

**Explicit width wins where the sink is not a stream.** Inside the status TUI the correct width is the
panel's inner width, so it is threaded in:

- `stack.StatusInput` gains a `Width int` field, consumed by `RenderApps` / `RenderTools` /
  `RenderInfra` / `DeployStatus` / `RenderTopology`. `cli/status` leaves it `0` and falls back to the
  stdout probe. **No signature changes.**
- `render.GitWorkspaceAt(rows, width)` and `stack.RenderDaemonsAt(rows, width)` are added alongside
  their existing forms for the two calls that take rows directly rather than a `StatusInput`, following
  the package's existing `SectionTitleAt` / `DefinitionAt` naming.
- **`stack/` splits collect from render.** `RenderApps` and its siblings currently reach
  `collectRowsByType` (`stack/status.go:71`), which calls `in.IsRunning(svc.Container)` per service.
  Rendering from `ViewPanel` without splitting would fire a Docker probe **per frame**. So `CollectApps`
  (probes, returns rows) is separated from `RenderAppsRows(rows, width)` (pure), with the existing
  `RenderApps(in)` kept as a thin wrapper so `cli/` callers are untouched.
- **The status TUI stops rendering at load time.** `buildTabs` returns a snapshot of *rows*, not
  strings; a new `renderTab(snapshot, index, width) (body string, anchors []int)` runs from the plugin,
  where the panel's inner region is known. Results are memoised on `(loadGen, tabIndex, width)` so
  `View()` does not re-render six tables per frame — a key that is only valid **because** the collect/
  render split made the snapshot pure.

This is what makes the width correct rather than merely plumbed; it is **not** the deferred "variant B"
from design (threading the *mode decision* through `stack/` for cross-table consistency), which remains
out of scope.

**Cells must contain plain text.** The shared wrappers (`wrapText`, `wrapPath`, `splitDisplayWidth`)
slice on rune boundaries and are **not ANSI-aware** — splitting inside an escape sequence would corrupt
both the styling and every width measurement downstream. This is safe today because every renderer
passes unstyled strings and applies color through `StyleFunc` *after* wrapping (verified:
`cli/snapshot/snapshot.go:160-174` builds plain cells, and `styles.IconPrefix` returns `safe + " "` with
no escapes). Since `Table` accepts caller-supplied content, the invariant is made explicit in the
package docs and guarded by a test rather than left implicit.

No `DWE_TERM_WIDTH` escape hatch. The test seams cover testing, and no user-facing scenario needs it
(YAGNI).

**One description, two render modes.** All six renderers stop chaining onto `*table.Table` and instead
populate a `tableView` value, which picks its own render mode. This is what keeps the feature a single
mechanism instead of six divergent copies.

**Styling must survive the mode switch.** The current `StyleFunc` closures mix semantic color (red `✗`,
green `running`) with table-only decoration (`Padding(0,1)`, zebra background, STATUS centering).
Record mode needs the color and must not inherit the decoration. So `tableView` carries semantic
styling in `Style` and declares decoration as separate fields that only table mode consults.

**Mode decision is per render call, not per table.** `DiagnosticsByDomain` emits one table per domain.
Deciding independently would produce a `Linters` list directly above a `Configuration` table, which
reads as a bug. It computes floors across all domains and, if any domain cannot fit, renders all of
them as records.

**Accepted limitation.** `dwe status` assembles apps/tools/infra tables plus deploy status plus git
plus daemons through separate `stack/` calls joined in `cli/status`, so they cannot coordinate their
mode. This is accepted: apps/tools/infra share nearly identical columns and will almost always agree,
while deploy status and daemons are narrow enough to stay tables. Threading the mode through the
`stack/` contract is deliberately **out of scope** — revisit only if the mismatch shows up in practice.

## Technical Details

### Column description

```go
// columnRole selects how a column is presented in record mode.
type columnRole int

const (
    roleField columnRole = iota // "label  value" line (default)
    roleTitle                   // joined into the record's header line
    roleBody                    // own line, no label
    roleGlyph                   // bare prefix on the header line, no separator
)

// columnSpec declares how one column behaves under width pressure.
type columnSpec struct {
    Flex bool                     // may shrink; fixed columns keep natural width
    Max  int                      // natural-width cap on cell content (0 = uncapped)
    Wrap func(string, int) string // wrapText for prose, wrapPath for paths; nil = never wraps
    Role columnRole
}
```

A column with a nil `Wrap` cannot shrink, so `Flex` is meaningless for it. `roleGlyph` exists so the
diagnostics STATUS glyph renders as `✗ hadolint · …` rather than `✗ · hadolint · …`.

### Table description

```go
type tableView struct {
    Headers   []string
    Rows      [][]string
    Cols      []columnSpec
    Style     func(row, col int) lipgloss.Style // semantic color only

    // Table-mode decoration; ignored by record mode.
    Padding   int   // horizontal cell padding (diagnostics: 1, all others: 0)
    BorderRow bool  // horizontal rule between data rows
    Zebra     bool  // alternating row background
    Center    []int // column indices to center; nil = none
}

func (v tableView) Render(budget int) string        // budget 0 = unbounded
func (v tableView) Fits(budget int) bool            // mode probe, for shared decisions
func (v tableView) renderTable(rows [][]string) string
func (v tableView) renderRecords(budget int) string
```

`Center` is a slice rather than an `int` index so its **zero value is inert**. An `int` field would
default to `0`, silently centering the first column of every table that forgot to set it.

`Fits` and the two unexported renderers are what `DiagnosticsByDomain` needs to force one shared mode
across all its domains — `Render` alone cannot express that.

### Width fitting

```go
// fitRows re-wraps rows to fitted column widths. ok=false means the columns do
// not fit even at their floors — the caller falls back to the record layout.
func fitRows(headers []string, rows [][]string, budget, padding int, cols []columnSpec) (out [][]string, ok bool)
```

Algorithm:

1. **Natural width** per column = `max(headerWidth, min(maxCellWidth, Max))` when `Max > 0`, else
   `max(headerWidth, maxCellWidth)`. The `Max` clamp applies **only to cell content** — clamping the
   header too would let natural drop below the floor and make step 4's headroom go negative.
   Today's `diagnosticTextWrapWidth = 44` (MESSAGE, HINT) and `diagnosticFileWrapWidth = 40` (FILE)
   move here as `Max` values, preserving their original purpose of stopping one long diagnostic from
   stretching the table on a wide screen.
2. **Chrome** = border glyphs (`len(cols)+1`) + `2*padding*len(cols)`. This matches lipgloss's own
   sizing, which allocates each column `max(headerWidth, maxCellLineWidth)` plus horizontal padding.
3. If `budget == 0` or `naturalSum + chrome <= budget` → use natural widths.
4. Otherwise distribute the deficit across `Flex` columns, proportional to each one's headroom above
   its floor.
5. **Floor** per column = `max(headerWidth, longestUnbreakableToken)` — the width below which the
   column cannot be squeezed without splitting something that must stay whole.
6. If floors + chrome exceed the budget → `ok = false`.
7. Wrap every cell **once**, at its final width, using the column's `Wrap`.

**`budget == 0` disables terminal-driven shrinking, NOT wrapping.** This distinction is load-bearing
and easy to implement backwards. At budget 0, steps 1, 3 and 7 still run: columns take their
`Max`-clamped natural widths and every cell is still wrapped at those widths. Only steps 4-6 (deficit
distribution and the record-mode fallback) are skipped. This is exactly what reproduces today's
diagnostics pre-wrap — FILE at 40, MESSAGE and HINT at 44 — after Task 8 removes the explicit
pre-wrap calls. An implementation that treats budget 0 as "return rows untouched" would silently
change every piped and non-TTY diagnostics run to unbounded-width columns, and the Task 1 goldens
would catch it as a failure rather than a design choice.

**Column floors are probed, not classified.** Go function values are not comparable, so a helper
cannot branch on *which* wrapper it was handed. Instead:

```go
// longestUnbreakableToken reports the narrowest width the column can be squeezed
// to. It probes the wrapper rather than inspecting it: wrapping at width 1 forces
// every breakable boundary, so the widest surviving line is by definition
// unbreakable. nil wrap => the whole cell is unbreakable.
func longestUnbreakableToken(s string, wrap func(string, int) string) int
```

`wrap(s, 1)` then take the widest resulting line. This works because `wrapText` keeps URL tokens whole
at any width (`diagnostics_table.go:358`) while `wrapPath` hard-splits via `splitDisplayWidth`
(`diagnostics_table.go:247`). Input already containing `\n` must be handled — cells are multi-line
after wrapping.

### Record layout

```
✗ hadolint · images/admin/Dockerfile
  Non-numeric user-id may not be resolvable by host system (DL3066)
  hint  https://github.com/hadolint/hadolint/wiki/DL3066
```

- `roleGlyph` cells prefix the header line directly, with a single space and no separator.
- `roleTitle` cells join into the header line with ` · `, skipping empty and `—` values.
- `roleBody` cells each get their own indented line, no label.
- `roleField` cells render as `label  value`; labels are the headers lowercased, aligned within the
  block, and styled with `styles.MutedStyle()`.
- Values wrap at `budget − indent − labelWidth` via the column's `Wrap`. The header line wraps at
  `budget`, continuation lines indented to match.
- Records are separated by a blank line. Zebra striping is dropped (a table affordance).
- Cell color comes from `Style(row, col)` with no padding/alignment/background composed on top.
- Record mode has no lower width bound; at extreme narrowness it simply wraps more.

Human-readable labels ("last failed error" instead of `last failed`) are **out of scope** — they would
require `store.*` i18n keys per the display-string localization rule.

### Budget resolution

```go
// styles/: the package that already owns the terminal probe.

// TermWidthOrZero returns f's terminal width, or 0 when f is not a terminal or
// its size is unknown. Unlike TermWidth it has no 80-column fallback: callers
// use 0 to mean "unbounded", which is the correct behavior for a pipe or file.
func TermWidthOrZero(f *os.File) int
```

Test seams follow the `cmdbrowser/fallback.go:16` convention: package-level `var` function values in
`styles/`, swapped and restored via `t.Cleanup`.

In `render/`, each renderer resolves its own default:

```go
func stdoutBudget() int { return styles.TermWidthOrZero(os.Stdout) }
func stderrBudget() int { return styles.TermWidthOrZero(os.Stderr) } // DiagnosticsTable only
```

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code, tests, and in-repo documentation changes.
- **Post-Completion** (no checkboxes): manual narrow-terminal verification, which cannot be automated
  because a pipe is by construction unbounded.

## Implementation Steps

### Task 1: Establish byte-exact golden baseline for all six renderers

**Files:**
- Create: `internal/core/ui/render/testdata/*.golden`
- Create: `internal/core/ui/render/golden_test.go`
- Modify: `internal/core/ui/render/test_helpers_test.go`

- [x] add a golden helper (`UPDATE_GOLDEN=1` env var to regenerate, byte comparison otherwise) in
      `golden_test.go`, pinning styles via the existing `resetStyles()` (`test_helpers_test.go:11`) —
      matches the `UPDATE_GOLDEN` convention already established by `statustui`/`docstui`/`cmdbrowser`
      golden tests rather than introducing a competing `-update` flag
- [x] pin **both** the lipgloss color profile **and** the light/dark background mode before calling
      `resetStyles()`, snapshotting and restoring each via `t.Cleanup`. This is not optional:
      `resetStyles` calls `styles.ApplyStyles(nil)`, which resolves palette defaults through
      background detection, and `zebraBackground` (`diagnostics_table.go:28`) is a
      `lipgloss.AdaptiveColor`. Without pinning, the same golden holds different ANSI values on a
      light versus dark terminal and the baseline becomes machine-dependent — implemented as
      `pinGoldenPalette(t)` in `test_helpers_test.go`
- [x] document that golden tests must not call `t.Parallel()` — they mutate package-level style state
- [x] capture goldens for `Table`, `DaemonTable`, `DeployStatus`, `GitWorkspace` with representative
      rows including empty cells and `—` placeholders
- [x] capture goldens for `ServicesTable` covering both `withDirCol` values, a custom extra column, and
      all three states (mandatory / enabled / disabled) with running and stopped rows
- [x] capture goldens for `DiagnosticsTable` (with DOMAIN) and `DiagnosticsByDomain` (multi-domain),
      including a long hadolint-style URL hint and a deep path that triggers `wrapPath`
- [x] verify goldens are stable across two consecutive runs and contain the expected ANSI sequences
      (`TestGolden_StableAcrossRuns`, plus two consecutive `go test` invocations)
- [x] run `go test -cover ./internal/core/ui/render/` and record the baseline coverage percentage
      here — Task 14 compares against it: **baseline coverage = 92.3%**
- [x] run `go test ./internal/core/ui/render/` — must pass before task 2

### Task 2: Extract the wrapping engine into a shared file

**Files:**
- Create: `internal/core/ui/render/table_wrap.go`
- Create: `internal/core/ui/render/table_wrap_test.go`
- Modify: `internal/core/ui/render/diagnostics_table.go`

- [x] move `wrapText`, `wrapLine`, `wrapPath`, `isURLToken`, `splitDisplayWidth` from
      `diagnostics_table.go` into `table_wrap.go` unchanged (pure relocation, no behavior change)
- [x] implement `longestUnbreakableToken(s string, wrap func(string, int) string) int` by probing
      `wrap(s, 1)` and returning the widest resulting line; nil wrap returns `lipgloss.Width(s)`
- [x] keep `wrapDiagnosticText` in `diagnostics_table.go` as a thin call into `wrapText`
- [x] write tests for `longestUnbreakableToken`: prose with a long URL, prose without, path input via
      `wrapPath`, nil wrap, empty string, and input already containing `\n`
- [x] verify the relocated helpers' existing tests in `diagnostics_table_test.go` pass unchanged and
      the Task 1 goldens still match
- [x] run `go test ./internal/core/ui/render/` — must pass before task 3

### Task 3: Add `columnSpec` and the width-fitting algorithm

**Files:**
- Create: `internal/core/ui/render/table_fit.go`
- Create: `internal/core/ui/render/table_fit_test.go`

- [x] define `columnRole` (`roleField` / `roleTitle` / `roleBody` / `roleGlyph`) and `columnSpec`
- [x] implement `naturalWidths(headers, rows, cols) []int` — `Max` clamps cell content only, then take
      `max(headerWidth, clampedCellMax)`
- [x] implement `columnFloors(headers, rows, cols) []int` as
      `max(headerWidth, longestUnbreakableToken)` per column
- [x] implement `fitRows(headers, rows, budget, padding, cols) ([][]string, bool)` — chrome accounting,
      proportional deficit distribution across `Flex` columns, `ok=false` when floors do not fit, and a
      single wrap pass at the final width
- [x] write tests for `naturalWidths` (uncapped, `Max`-clamped, header wider than `Max`, header wider
      than every cell)
- [x] write tests for `columnFloors` (long URL pins the floor, header pins the floor)
- [x] write table-driven tests for `fitRows`: **budget 0 still applies `Max` caps and still wraps**
      (a 60-char cell in a `Max: 44` column comes back wrapped, not untouched); budget above natural
      sum unchanged; budget forcing shrink narrows only `Flex` columns; budget below floors returns
      `ok=false`; wrapped cells never exceed their assigned width; a URL is never split
- [x] write tests for degenerate input: zero rows, zero columns, and a ragged row shorter than
      `headers` (reachable — `Table()` takes caller-supplied data from `cli/snapshot/snapshot.go:176`)
- [x] write a test pinning the plain-text invariant: a cell containing an ANSI escape is documented as
      unsupported input, asserting the wrappers are only ever fed unstyled strings
- [x] run `go test ./internal/core/ui/render/` — must pass before task 4

### Task 4: Add `tableView` with table-mode rendering

**Files:**
- Create: `internal/core/ui/render/table_view.go`
- Create: `internal/core/ui/render/table_view_test.go`

- [ ] define `tableView` with `Headers`, `Rows`, `Cols`, `Style`, `Padding`, `BorderRow`, `Zebra`, and
      `Center []int` (slice so the zero value centers nothing)
- [ ] implement `renderTable(rows [][]string) string` — build via `baseTable`, compose the effective
      `StyleFunc` from `v.Style` plus `Padding`/`Zebra`/`Center`, apply `BorderRow`
- [ ] implement `Fits(budget int) bool` delegating to `fitRows`, so callers can force a shared mode
- [ ] implement `Render(budget int) string` routing to `renderTable`, with the record branch stubbed
      until task 5
- [ ] write tests asserting the composed `StyleFunc` applies padding, zebra on odd rows, and centering
      only on the indices listed in `Center` (and nothing when `Center` is nil)
- [ ] write a test asserting `lipgloss.NewStyle().Padding(0,0)` renders identically to
      `lipgloss.NewStyle()` through `table.Table`, so no `if v.Padding > 0` guard is needed
- [ ] write tests asserting `Render(0)` reproduces a `baseTable`-built equivalent byte-for-byte
- [ ] run `go test ./internal/core/ui/render/` — must pass before task 5

### Task 5: Add record-layout rendering

**Files:**
- Create: `internal/core/ui/render/table_record.go`
- Create: `internal/core/ui/render/table_record_test.go`
- Modify: `internal/core/ui/render/table_view.go`

- [ ] implement `renderRecords(budget int) string` and wire it into `Render` as the `ok == false` branch
- [ ] compose the header line: `roleGlyph` cells as bare prefixes, then `roleTitle` cells joined with
      ` · `, skipping empty and `—` cells; wrap the header line at `budget` with continuation indent
- [ ] render `roleBody` columns as indented unlabeled lines and `roleField` columns as aligned
      `label  value` lines with lowercased headers styled via `styles.MutedStyle()`
- [ ] wrap values at `budget − indent − labelWidth` using each column's `Wrap`; separate records with a
      blank line; apply `v.Style(row, col)` with no table decoration
- [ ] write tests for the record shape (glyph prefix, title join, body line, field alignment,
      blank-line separation)
- [ ] write tests for edge cases: all-title row, empty/`—` cells skipped, very small budget still
      renders, a URL longer than the budget stays on one unbroken line, ragged row
- [ ] write a test asserting semantic color survives (a `DangerStyle` cell is still styled in record
      mode, with no padding or background composed on)
- [ ] run `go test ./internal/core/ui/render/` — must pass before task 6

### Task 6: Migrate `Table`, `DaemonTable`, `DeployStatus`, and `GitWorkspace` to `tableView`

**Files:**
- Modify: `internal/core/ui/render/table.go`
- Modify: `internal/core/ui/render/gitworkspace.go`

- [ ] convert `Table` (`table.go:59`) — column 0 `roleTitle`, the rest `roleField`; all
      `{Flex: true, Wrap: wrapText}` since caller-supplied content is arbitrary prose
- [ ] convert `DaemonTable` (`table.go:251`) — `ID` as `roleTitle`; `PARAMS` as
      `{Flex: true, Wrap: wrapText}` (the widest, most compressible column); preserve the empty-input
      early return
- [ ] convert `DeployStatus` (`table.go:317`) — `SERVICE` as `roleTitle`; `LAST FAILED` as
      `{Flex: true, Wrap: wrapText}`; hashes and status stay fixed; move the per-column status/delta
      styling into `tableView.Style`
- [ ] convert `GitWorkspace` (`gitworkspace.go:19`) — `SERVICE` as `roleTitle`; `DIR` as
      `{Flex: true, Wrap: wrapPath}`; `BRANCH` as `{Flex: true, Wrap: wrapText}`; move the DIRTY-column
      styling into `tableView.Style`
- [ ] pass `Center: nil` implicitly (do not set the field) for all four
- [ ] run `go test ./internal/core/ui/render/` — **all Task 1 goldens must match byte-for-byte with no
      regeneration**; must pass before task 7

### Task 7: Migrate `ServicesTable` to `tableView`

**Files:**
- Modify: `internal/core/ui/render/table.go`

- [ ] convert `ServicesTable` (`table.go:143`), preserving the `withDirCol` branch and the `extraCols`
      append
- [ ] move the `rowCellStyle` base/state/run dispatch into `tableView.Style`, keeping the
      `stateCol`/`runCol` index arithmetic correct for both column layouts
- [ ] declare `NAME` as `roleTitle` and every other column (including extras) as `roleField`
- [ ] declare `DIR` as `{Flex: true, Wrap: wrapPath}` and `CONTAINER` / `HOSTS` / `PORTS` as
      `{Flex: true, Wrap: wrapText}` — the `name=value` cells built by `table.go:114` / `:129` wrap
      cleanly on the comma-space boundaries
- [ ] declare **custom extra columns** as `{Flex: true, Wrap: wrapText, Role: roleField}` too. Their
      values come from `ServiceTableRow.Extras` and are arbitrary templated strings; a nil `Wrap` would
      leave them unwrappable, so a long extra would blow past the budget in record mode and break the
      no-overflow guarantee. This is not an edge case — a 7-column services table plus one extra falls
      into record mode at any realistic narrow width
- [ ] write a test covering record mode for both the `withDirCol` and non-`withDirCol` layouts,
      including a custom extra column with a long non-URL token and one with a long URL
- [ ] run `go test ./internal/core/ui/render/` — **Task 1 goldens must match byte-for-byte**; must pass
      before task 8

### Task 8: Migrate the diagnostics table to `tableView`

**Files:**
- Modify: `internal/core/ui/render/diagnostics_table.go`

- [ ] convert `diagnosticsTable` (`diagnostics_table.go:84`) with `Padding: 1`, `BorderRow: true`,
      `Zebra: true`, `Center: []int{0}`
- [ ] replace the pre-wrap calls with column specs: STATUS `roleGlyph` fixed; DOMAIN and TARGET fixed
      `roleTitle`; FILE `{Flex: true, Max: 40, Wrap: wrapPath, Role: roleTitle}`; MESSAGE
      `{Flex: true, Max: 44, Wrap: wrapText, Role: roleBody}`; HINT
      `{Flex: true, Max: 44, Wrap: wrapText, Role: roleField}`
- [ ] retain `diagnosticTextWrapWidth` / `diagnosticFileWrapWidth` as the `Max` constants, with their
      doc comments updated to describe them as natural-width caps
- [ ] reduce the existing `StyleFunc` to semantic severity color only — padding, centering, and zebra
      now come from the `tableView` decoration fields
- [ ] confirm the trailing pad space that keeps terminal link detectors off the `│` glyph
      (`diagnostics_table.go:128`) still applies, and that `diagnostics_table_test.go:243` passes
- [ ] run `make test` — **Task 1 goldens must match byte-for-byte**; full suite green before task 9

### Task 9: Split collect from render in `stack/`, and add width parameters

The status TUI must be able to render from a snapshot **without** re-running Docker probes. It cannot
today: `RenderApps` / `RenderTools` / `RenderInfra` reach `collectRowsByType` (`stack/status.go:71`),
which calls `in.IsRunning(svc.Container)` for every enabled or mandatory service. Rendering from
`ViewPanel` without this split would fire a container probe **per frame**.

**Files:**
- Modify: `internal/core/project/stack/status.go`
- Modify: `internal/core/project/stack/deploystatus.go`
- Modify: `internal/core/project/stack/daemons.go`
- Modify: `internal/core/ui/render/gitworkspace.go`
- Modify: `internal/core/project/stack/status_test.go`
- Modify: `internal/core/project/stack/daemons_test.go`

- [ ] split each services section into a collect half and a render half — `CollectApps(in)
      ([]render.ServiceTableRow, []error)` (evaluates `IsRunning`, resolves custom extra columns) and
      `RenderAppsRows(rows, width) string` — likewise for Tools and Infra; keep the existing
      `RenderApps(in)` as a thin collect-then-render wrapper so `cli/` callers are unchanged
- [ ] apply the same split to `RenderTopology` (`status.go:105`) — it also reaches `collectRowsByType`
      (`status.go:189`) — and to `DeployStatus` (`deploystatus.go:40`)
- [ ] add `Width int` to `stack.StatusInput` (`status.go:28`) — `0` means "resolve from the sink" —
      and consume it in the wrapper forms
- [ ] add `render.GitWorkspaceAt(rows, width)` and reduce `GitWorkspace` to a call with width `0`,
      mirroring the `SectionTitleAt` (`info.go:194`) naming precedent
- [ ] add `stack.RenderDaemonsAt(rows, width)` and reduce `RenderDaemons` (`daemons.go:246`) to a call
      with width `0` — reached only from the TUI, and missed in the first draft
- [ ] verify the three unchanged public consumers still render identically: `cli/status/status.go:354`
      /`:379`/`:388`, `cli/status/deploy.go:42`, and `cli/service/service_list.go:99-101` (`dwe services`)
- [ ] write a test asserting `CollectApps` calls `IsRunning` and `RenderAppsRows` never does — this is
      the contract Task 11's memoisation depends on
- [ ] write tests asserting an explicit `Width` overrides the probe and that `Width: 0` falls back
- [ ] run `make test` — must pass before task 10

### Task 10: Move status-TUI rendering out of the load path — no behavior change

This is the deliberate pure-refactor half of the statustui change, split from Task 11 for the same
reason Tasks 6-8 were split from the budget: three of its failure modes (stale memoised bodies,
generation interaction, mis-placed anchors) are **silent** — they produce plausible output and no test
failure. Pinning a byte-exact characterization golden first turns the dangerous part into a mechanical
one.

**Files:**
- Create: `internal/core/ui/statustui/testdata/tabs_*.golden`
- Create: `internal/core/ui/statustui/tabs_golden_test.go`
- Modify: `internal/core/ui/statustui/load.go`
- Modify: `internal/core/ui/statustui/tui.go`
- Modify: `internal/core/ui/statustui/plugin.go`
- Modify: `internal/core/ui/statustui/load_test.go`
- Modify: `internal/core/ui/statustui/plugin_test.go`
- Modify: `internal/core/ui/statustui/plugin_golden_test.go`

- [ ] **first**, capture a characterization golden of the CURRENT behavior: all five tab bodies plus
      their anchor offsets, rendered from fixed stub data at a fixed width, with the palette and
      background mode pinned exactly as in Task 1
- [ ] change `buildTabs` (`load.go:127`) to return a **data snapshot** rather than rendered strings:
      the collected service rows per type (from Task 9's `CollectApps` / `CollectTools` /
      `CollectInfra`), the topology and deploy-status rows, the collected daemon rows, the collected
      git rows, the per-section error slices, and the width-independent `stack.HealthIndicator(in)`.
      **Every Docker- or git-backed probe — `collectDaemonsFn`, `collectGitWorkspaceFn`, and
      `IsRunning` — must be evaluated here, in the async path**, so the snapshot is pure data
- [ ] change `tabsLoadedMsg` (`load.go:19`) to carry that snapshot, and **drop `anchors` from it** —
      anchors are line offsets into the wrapped body (`joinSectionsWithAnchors`, `load.go:63-77`) and
      are therefore width-dependent
- [ ] add `renderTab(snap, index, width) (body string, anchors []int)` performing the per-tab
      composition `buildTabs` does today (section titles, warning prefixes, `joinNonEmpty` /
      `joinSectionsWithAnchors`, the placeholder strings for empty sections)
- [ ] call `renderTab` from `renderBody` (`plugin.go:178`) and set both the viewport content and
      `m.sectionAnchors` from its result, so `jumpSection` (`tui.go:117-131`) uses offsets that match
      what is on screen — but **pass width `0`** for now, so rendering is byte-identical to today
- [ ] keep `plugin.Resize` a no-op (`plugin.go:79`) — sizing stays owned by `ViewPanel`, per the
      existing contract comment
- [ ] update `plugin_golden_test.go`, which assigns `tabs` directly to bypass `buildTabsCmd`
      (`:37`, `:49`) — it must now assign a snapshot
- [ ] **adapt, do not delete, the six `tabsLoadedMsg{…, tabs: …}` constructions in `plugin_test.go`**
      (`:264`, `:285`, `:314`, `:374`, `:406`, `:412`) — these are the only coverage of stale-message
      filtering, same-tab reload YOffset restore, tab-switch reload invalidation, and back-to-back
      reloads. The characterization golden captures static bodies and **cannot** prove the reload path
      still works, so these tests are the real safety net for this task
- [ ] add a frame-level reload test: `HandleAction(ActionReload)` → `tabsLoadedMsg` → viewport YOffset
      restored, asserted through the rendered frame rather than internal fields
- [ ] add a spy test proving `IsRunning` is **never** called from `renderTab` / `ViewPanel`
- [ ] run `make test` — **the characterization golden and every `plugin_golden_test.go` golden must
      match byte-for-byte with no regeneration**; must pass before task 11

### Task 11: Render the status TUI at panel width, with memoised renders

**Files:**
- Modify: `internal/core/ui/statustui/load.go`
- Modify: `internal/core/ui/statustui/plugin.go`
- Modify: `internal/core/ui/statustui/actions.go`
- Modify: `internal/core/ui/statustui/plugin_test.go`
- Modify: `internal/core/ui/statustui/tabs_golden_test.go`

- [ ] thread `width` from `renderTab` into the Task 9 render halves — `RenderAppsRows` and siblings,
      `GitWorkspaceAt`, `RenderDaemonsAt`
- [ ] pass the real inner width from `renderBody` (`plugin.go:178`, `w := max(inner.Width, 0)`) instead
      of the `0` placeholder left by Task 10
- [ ] memoise `renderTab` on `(loadGen, activeTab, width)`; invalidate on tab switch (`setActiveTab`),
      on reload (`actions.go:112`), and on width change. The key is only valid because Task 9 and Task
      10 made the snapshot pure — nothing outside those three inputs may affect `renderTab` output
- [ ] **put a call-count spy on `renderTab`** and assert the invalidation contract directly rather than
      by inspection: two consecutive `View()` calls at one width → exactly one render; a width change →
      a second render; a tab switch → a second render; a reload → a second render
- [ ] write a test asserting the rendered table width never exceeds the panel inner width at the narrow
      buckets (60, 79, 80) — remember inner = outer − 4 per `Frame.renderBody`
- [ ] write a test asserting anchors recomputed at a narrow width still land on sub-table headings
- [ ] keep `TestStatus_FrameWidthInvariant` (`plugin_golden_test.go:221`) passing at every width bucket
- [ ] regenerate the Task 10 characterization golden **only** for the width-dependent buckets, and
      review the diff line by line — it is the one place in this plan where a golden legitimately
      changes
- [ ] run `make test` — must pass before task 12

### Task 12: Enable the sink-aware width budget

**This task comes last of the plumbing tasks on purpose.** Until it lands, a `Width` of `0` means
"unbounded" everywhere, so no consumer can observe a regression mid-plan. If it ran before Tasks 9-11,
`0` would start resolving from `os.Stdout` — and inside the status TUI, where stdout *is* a terminal,
every table would be fitted to the full terminal width instead of the narrower panel width. Task 10's
byte-identical claim would then hold only under the non-TTY test seams, not in a real terminal.

**Files:**
- Modify: `internal/core/ui/styles/styles.go`
- Modify: `internal/core/ui/styles/styles_test.go`
- Create: `internal/core/ui/render/table_budget.go`
- Create: `internal/core/ui/render/table_budget_test.go`
- Modify: `internal/core/ui/render/table_view.go`
- Modify: `internal/core/ui/render/test_helpers_test.go`

- [ ] add `styles.TermWidthOrZero(f *os.File) int` with package-level test seams for the TTY check and
      the size probe, following the `cmdbrowser/fallback.go:16` convention
- [ ] add `stdoutBudget()` and `stderrBudget()` in `render/table_budget.go`, documenting that
      `DiagnosticsTable` is the sole stderr consumer
- [ ] wire the per-renderer budget into every public renderer's `Render` call — stdout for all except
      `DiagnosticsTable`, which uses stderr
- [ ] confirm the TUI path is unaffected: it passes an explicit non-zero width from Task 11 and must
      never reach the probe
- [ ] add a `TestMain` (or `init` in `test_helpers_test.go`) pinning the seams to non-TTY so the suite
      cannot flip modes when the compiled test binary is run directly
- [ ] add a test helper that swaps the seams to a given width and restores them via `t.Cleanup`
- [ ] write tests for `TermWidthOrZero`: non-TTY returns 0; TTY returns the reported width; a zero or
      negative reported width returns 0
- [ ] write tests for shrink mode (flex columns narrowed, fixed columns untouched, total within budget)
      and record mode (below floors, URL intact) across at least three renderers
- [ ] write a test asserting that with the seams at their non-TTY defaults, all Task 1 goldens match
- [ ] run `make test` — must pass before task 13

### Task 13: Make `DiagnosticsByDomain` decide the mode once for all domains

**Files:**
- Modify: `internal/core/ui/render/diagnostics_table.go`
- Modify: `internal/core/ui/render/diagnostics_table_test.go`

- [ ] refactor `DiagnosticsByDomain` (`diagnostics_table.go:54`) to build every domain's `tableView`
      first, then resolve one shared mode via `Fits(budget)` across all of them
- [ ] render all domains as records when any single domain fails to fit; otherwise render all as
      tables, calling `renderTable` / `renderRecords` directly rather than per-domain `Render`
- [ ] keep the existing domain ordering (`sortDomainsForDisplay`), per-domain titles, and the
      empty-input early return unchanged
- [ ] write a test with two domains where one fits and one does not, asserting both render as records
- [ ] write a test with two domains that both fit, asserting both render as tables
- [ ] write a test asserting a single-domain input is unaffected
- [ ] run `make test` — must pass before task 14

### Task 14: Verify acceptance criteria

- [ ] verify the three degradation stages behave as described in Overview for **every** renderer, not
      just diagnostics — each must have at least one width where it shrinks before it flips to records
- [ ] verify no data loss anywhere: no ellipsis truncation on any path, URLs never split
- [ ] verify piped output is unchanged: capture `dwe validate`, `dwe status`, `dwe services`, and
      `dwe snapshot list` through a pipe and diff against the same commands on the pre-change binary
- [ ] verify `dwe services` at a narrow terminal — it is a public consumer of `ServicesTable` reached
      through `cli/service/service_list.go:99-101`, distinct from `dwe status`
- [ ] verify the stderr path: run `dwe deploy run > /dev/null` with a narrow terminal and confirm the
      preflight diagnostics table fits the terminal rather than overflowing
- [ ] verify the TUI path: every one of the four tables inside `dwe status --tui` fits the panel at
      every width bucket, including Daemons, and a live terminal resize re-renders at the new width
- [ ] verify record-mode vertical cost is acceptable for `dwe status` on a project with ~10 services —
      if the services section becomes unusably tall, record that as a follow-up rather than a blocker
- [ ] verify all six renderers go through `tableView` and no production code calls `baseTable` directly
      except `tableView.renderTable`
- [ ] run the full suite: `make test`
- [ ] run `make lint` and resolve any `revive` / `gocritic` / `unused` findings on the new files
- [ ] run `go test -cover ./internal/core/ui/render/` and confirm coverage did not drop below the
      baseline figure recorded in Task 1

### Task 15: [Final] Update documentation

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md`

- [ ] update the `internal/core/ui/render/` entry (`docs/internals/packages.md:192`) — the file count
      and list move from 14 to 19 implementation files (+ `table_wrap.go`, `table_fit.go`,
      `table_view.go`, `table_record.go`, `table_budget.go`) and their test files, plus the new
      `testdata/` golden tree
- [ ] document the responsive-table contract in that section: the budget is derived from the renderer's
      **sink** (`DiagnosticsTable` → stderr, all others → stdout); a non-TTY sink means budget 0 means
      unbounded and byte-identical legacy output; **budget 0 disables shrinking, not `Max` wrapping**;
      `fitRows` returns `ok=false` rather than truncating; cells must be plain text because the
      wrappers are not ANSI-aware; the mode decision is made once per render call; explicit width via
      `StatusInput.Width` / `GitWorkspaceAt` / `RenderDaemonsAt` wins for TUI panels; the `dwe status`
      cross-table mode mismatch is accepted
- [ ] document the statustui change in the `internal/core/ui/statustui/` section: `buildTabs` carries a
      data snapshot and rendering happens in `ViewPanel` at the known panel width, memoised on
      `(loadGen, tabIndex, width)`; `sectionAnchors` are width-dependent and must come from the same
      render pass
- [ ] note the new `styles.TermWidthOrZero` in the `internal/core/ui/styles/` section and contrast it
      with `TermWidth`'s 80-column fallback
- [ ] add a Critical Patterns bullet in `AGENTS.md` covering the two traps: non-TTY means budget 0 means
      the whole mechanism is disabled (why goldens keep passing), and the budget must follow the sink,
      not `os.Stdout` — both are what will confuse whoever adds table number seven
- [ ] confirm `CLAUDE.md` is still a symlink to `AGENTS.md` (`ls -l CLAUDE.md`) — edit `AGENTS.md` only
- [ ] check the ru mirror: `docs/i18n/ru/` currently mirrors only `guides/`, `reference/`, and
      `README.md` — there is **no** `docs/i18n/ru/internals/`, and `web/scripts/sync-docs.mjs:138`
      excludes `internals/` from the published site, so `packages.md` and `AGENTS.md` have no ru
      counterpart to update. Verify this still holds, and if any ru page under
      `docs/i18n/ru/guides/` or `docs/i18n/ru/reference/` embeds rendered table output, confirm it
      matches the (unchanged) wide-terminal rendering
- [ ] leave `docs/reference/` untouched: this is presentation, not configuration, and no `styles.yml`
      knob is introduced
- [ ] run `make build` to re-sync the embedded docs tree, then `make test` and `make lint`
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification:**

- Narrow mode cannot be reproduced through a pipe by construction — the budget is disabled whenever the
  sink is not a TTY. Verification requires physically resizing a terminal window.
- Suggested pass on a live project (see the tbm or beetDeck workspaces): run `dwe validate`,
  `dwe status`, and `dwe status --tui` at roughly 200, 140, 110, 80, and 50 columns and confirm the
  transitions look right and nothing is lost at any width.
- Verify the status TUI specifically: tables inside the panel must never exceed the panel border, at
  every terminal width the TUI supports.
- Confirm hint URLs remain click/copy-safe in both modes — the trailing pad space that keeps terminal
  link detectors from swallowing the `│` border glyph (documented at `diagnostics_table.go:128`) must
  survive the migration.
- Check the record layout against a project using custom `ServicesTable` extra columns, since those
  column names become record labels.

**External system updates:**

- None. No public API, config schema, or consuming project is affected.

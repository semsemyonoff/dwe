# Status TUI Dashboard (read-only)

## Overview

Add an interactive read-only TUI mode to `devbox status` that presents existing status sections as five logical tabs (Services, Deploy, Topology, Git, Daemons) with a persistent top title bar and bottom status bar. The TUI loads a single snapshot, lets the user navigate tabs and scroll content, and exits on `q`. Plain text output remains as the fallback for non-TTY / scripted use; per-section subcommands (`status apps`, etc.) always render plain.

Goal: cleaner overview of stack state in an interactive shell without changing what data is shown or how it's computed — the TUI is a presentation layer over the existing `stack.Render*` / `stack.Collect*` functions.

## Context (from discovery)

Files/components involved:
- `internal/command/status.go` — root `status` command, seven sections (apps/tools/infra/deploy/topology/git/daemons), dispatcher logic
- `internal/stack/status.go` — `RenderHealth`, `RenderApps`, `RenderTools`, `RenderInfra`, `RenderDeployStatus`, `RenderTopology`, `RenderDaemons`, `CollectDaemons`, `CollectGitWorkspace`
- `internal/ui/cmdbrowser/` — reference implementation for an interactive bubbletea TUI: `run.go` (program lifecycle, TTY check, fallback, `ui.RunWithPromptHooks`, error mapping), `model.go:580-588` (`renderTitleBar` using `ui.LogoMarkPlain()`)
- `internal/ui/styles.go` — branded styles and accent color resolution
- `internal/liveui/liveline.go` — has package-doc invariants forbidding `tea.NewProgram` *for the live pipeline view* (not for interactive commands; cmdbrowser already uses `tea.NewProgram`)

Related patterns found:
- **Interactive TUI precedent:** `cmdbrowser` uses `tea.NewProgram(m)` + `ui.RunWithPromptHooks` + maps `tea.ErrInterrupted`/`tea.ErrProgramKilled` → `ui.ErrCancelled`. statustui follows the same pattern.
- **Branded title bar:** `ui.LogoMarkPlain() + " " + title` rendered with accent+bold lipgloss style (`cmdbrowser/model.go:588`).
- **Synthetic-cfg test fakes:** `internal/stack/status_test.go` uses `neverRunning` / `alwaysRunning` / `partialRunning` `ContainerCheckFn` fakes. statustui tests reuse this pattern.
- **TTY detection seam:** cmdbrowser uses `isTerminalFn` / `terminalSizeFn` as variables overridden in tests.

Dependencies identified (already in `go.mod`, no new third-party deps):
- `charm.land/bubbletea/v2 v2.0.5`
- `charm.land/bubbles/v2 v2.1.0` (for `viewport`, `help`, `key`, `spinner`)
- `charm.land/lipgloss/v2 v2.0.3`

Compliance notes from CLAUDE.md:
- "pre-release, no backwards compatibility constraints" → changing default `devbox status` TTY behavior is fine.
- "Section renderer signature contract" — `stack` returns strings, `ui` returns strings, `command` writes. statustui lives in `internal/command/statustui/` and consumes existing strings; no violation.
- Liveui's "never tea.NewProgram" is documented as a liveui-specific invariant (cmdbrowser already uses `tea.NewProgram`); no CLAUDE.md edit required.

## Development Approach

- **Testing approach:** Regular (code first, then tests in the same task).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
  - Tests are not optional.
  - Unit tests for new and modified functions.
  - Cover success and error/edge scenarios.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each change.
- No new third-party dependencies; no `go.mod` churn beyond what `make tidy` would add (none expected).

## Testing Strategy

- **Unit tests:** required for every task. Model tests use direct `Init`/`Update`/`View` calls without `tea.NewProgram` (mirrors how bubbletea models are typically tested). `buildTabs` tests use synthetic `*config.DevboxConfig` and the `neverRunning` / `alwaysRunning` / `partialRunning` fakes already used by `internal/stack/status_test.go`.
- **`shouldUseTUI` tests:** matrix of (TTY/non-TTY) × (--no-tui set/unset) × (any --no-<section> set/unset). Verify subcommands never invoke the TUI path.
- **No golden snapshots:** lipgloss ANSI output is sensitive to terminal env (`COLORTERM`, `TERM`) and lipgloss version. Use substring assertions (`require.Contains`) on the rendered `View()`.
- **No `tea.NewProgram.Run()` integration tests:** that path is bubbletea territory and requires a real PTY.
- **e2e tests:** project does not use UI e2e frameworks (no Playwright/Cypress). N/A.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## Solution Overview

A new internal package `internal/command/statustui` implements a single bubbletea model that:
1. On `Init`, kicks off a `buildTabsCmd` that calls existing `stack.Render*` / `Collect*` functions serially and collects results into a `tabsLoadedMsg`.
2. Renders a 5-row vertical layout: branded title bar / tab strip / scrollable viewport / status bar (with separators integrated via lipgloss borders or blank lines).
3. Handles keys: `tab`/`shift+tab`/`←`/`→` for tab cycling, digits `1`-`5` for direct jump, `r` for reload, `?` for expanded help, `q`/`ctrl+c` for exit.
4. Delegates scroll keys to `bubbles/v2/viewport`.

The existing root `status` command grows a `--no-tui` flag and a `shouldUseTUI` dispatcher. When the dispatcher selects TUI, it calls `statustui.Run(ctx, sc)`. Otherwise the existing `renderDefaultStatus` path runs unchanged. All subcommands (`status apps`, `status deploy`, …) are unaffected and always render plain.

`internal/stack/status.go` gets a tiny helper `HealthIndicator(in) string` so the status bar can render just `● running` without the `Devbox: ` prefix (which is already implied by the top title bar). Existing `RenderHealth` is refactored to `"Devbox: " + HealthIndicator(in)` — single source of truth, no behaviour change for plain output.

Key design rationale:
- **One package, three files** (`tui.go`/`load.go`/`keys.go`) — small surface, clear separation: model & view in `tui.go`, async data fetch in `load.go`, key bindings in `keys.go`.
- **Reuse existing renderers** — no duplicate layout logic, plain output and TUI content stay synchronized by construction.
- **Single shared viewport** — simpler state, smaller memory footprint, identical scroll UX across tabs; on tab switch we `SetContent(...)` + `GotoTop()`.
- **Test seam for TTY** — package-level `isTerminalFn` / `terminalSizeFn` vars (overridable in tests), mirroring cmdbrowser.
- **No CLAUDE.md edit needed** — cmdbrowser already uses `tea.NewProgram`; the liveui rule is liveui-scoped by context.

## Technical Details

### `shouldUseTUI` (in `internal/command/status.go`)

```go
func shouldUseTUI(noTUI bool, no *noSectionFlags) bool {
    if noTUI { return false }
    if no.noApps || no.noTools || no.noInfra || no.noDeploy ||
       no.noTopology || no.noGit || no.noDaemons { return false }
    if os.Getenv("TERM") == "dumb" { return false }
    return isTerminalFn(os.Stdout.Fd())
}
```

Package-level `isTerminalFn = term.IsTerminal` (overridable in tests). The `TERM=dumb` guard prevents launching the TUI in dumb terminals (Emacs `M-x shell`, some CI runners) where ANSI rendering breaks. Do NOT also reject unset `TERM` — cmdbrowser's TTY check (`internal/ui/cmdbrowser/fallback.go:15-21`) does not, and `TERM=""` can occur in legitimate TTY contexts (e.g., direct exec from a service manager that strips env).

### Model fields (in `internal/command/statustui/tui.go`)

```go
type tab struct {
    title   string  // "Services", "Deploy", "Topology", "Git", "Daemons"
    content string  // pre-rendered output from stack.Render*
}

type model struct {
    deps      Deps            // dependency bundle (see Deps below — mirror of statusContext fields, defined in statustui to avoid import cycle)
    ctx       context.Context // child context owned by Run; canceled on quit so in-flight goroutines stop cleanly
    tabs      []tab
    active    int
    viewport  viewport.Model
    help      help.Model
    keys      keyMap
    spinner   spinner.Model
    loading   bool          // initial load
    reloadActive  int       // active tab index captured at 'r' press
    reloadYOffset int       // viewport YOffset captured at 'r' press
    loadGen       uint64    // monotonically incremented on every buildTabsCmd dispatch; tabsLoadedMsg carries its own gen and is dropped if stale
    reloadGen     uint64    // gen of the reload whose offset we want to restore; cleared on tab switch, so navigating away invalidates the pending restore
    reloading bool          // background reload via 'r'
    width     int
    height    int
    err       error         // fatal load error (model still usable via 'r')
    reloadAt  time.Time
}
```

`statusContext` lives in package `command` (`internal/command/status.go:50-62`); `statustui` importing `command` would create a cycle. Instead, `statustui` defines a `Deps` struct mirroring only the fields it needs (`Cfg`, `State`, `Tracked`, `SvcDeploys`, `ProjectName`, `DockerCfg`, `Topo`, `TopoStatus`, `IsRunning`, `ProjectRoot`). The command layer constructs `statustui.Deps` from its `statusContext` before calling `statustui.Run`. All references to "sc" in subsequent task descriptions mean `m.deps`.

### Tab content composition (in `internal/command/statustui/load.go`)

```go
func buildTabs(ctx context.Context, d Deps) []tab {
    in := stack.StatusInput{ /* from Deps */ }
    // Each Render* returns (body, errs). Errs are formatted via warningPrefix() and
    // prepended; bodies are joined with joinNonEmpty (which only operates on strings).
    appsBody,  appsErrs  := stack.RenderApps(in)
    toolsBody, toolsErrs := stack.RenderTools(in)
    infraBody, infraErrs := stack.RenderInfra(in)
    serviceWarnings := warningPrefix(len(appsErrs) + len(toolsErrs) + len(infraErrs))
    services := joinNonEmpty(serviceWarnings, appsBody, toolsBody, infraBody)

    deploy := stack.RenderDeployStatus(in)
    if d.State != nil {
        deploy = joinNonEmpty(ui.RenderPendingBanner(d.State.Pending), deploy)
    }
    topology := stack.RenderTopology(in)
    git := renderGitTab(ctx, d)                 // wraps collectGitWorkspaceFn (= stack.CollectGitWorkspace by default) + ui.RenderGitWorkspace
    rows, daemonCollectErrs := collectDaemonsFn(ctx, d.Cfg, normaliseDocker(d.DockerCfg))
    daemonsBody, _ := stack.RenderDaemons(rows)
    daemons := joinNonEmpty(warningPrefix(len(daemonCollectErrs)), daemonsBody)
    // daemonCollectErrs are prepended as warnings on the Daemons tab (matches plain status, internal/command/status.go:242-247)
    return []tab{
        {"Services", services},
        {"Deploy",   deploy},
        {"Topology", topology},
        {"Git",      git},
        {"Daemons",  daemons},
    }
}

// Package-level seams for testability. The two `Collect*` calls hit docker/git;
// tests need to substitute them with controllable fakes (e.g. for the
// reload-then-quit cancellation test in Task 6). Override via t.Cleanup.
var (
    collectDaemonsFn      = stack.CollectDaemons
    collectGitWorkspaceFn = stack.CollectGitWorkspace
)

// warningPrefix returns a styled "⚠ N expression(s) failed" line via ui.StyleWarning,
// or "" when n == 0. ui.StyleWarning is the canonical warning style at internal/ui/styles.go:178-180.
func warningPrefix(n int) string { ... }
```

Per-section renderer errors are prepended to the tab content via `warningPrefix(n)` → `ui.StyleWarning("⚠ N expression(s) failed")` (the canonical warning style at `internal/ui/styles.go:178-180`). Empty tabs render a centered placeholder.

### Layout (View)

```
┌────────────────────────────────────────────────────┐  height = msg.Height
│ ▪ devbox · myproject · Status                       │  row 1: title bar  (lipgloss render of LogoMarkPlain + identifier)
│ ▌Services▐  Deploy   Topology   Git   Daemons       │  row 2: tab strip  (active tab has ▌▐ accent corners)
├ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┤  row 3: divider    (single dim horizontal line)
│  <viewport content, height = msg.Height - 4>        │  rows 4..N-1
│ ○ stopped · loaded 12s ago    tab ←→ · q · r · ?    │  row N: status bar
└────────────────────────────────────────────────────┘
```

Viewport height = `msg.Height - 4` (title + tabs + divider + status bar). When `width < 60 || height < 16`, View returns a centered "terminal too small (need 60×16)" message.

### Health indicator refactor

Before:
```go
func RenderHealth(in StatusInput) string {
    rows := collectRowsByType(in.Cfg, in.IsRunning, in.Cfg.Project.FullName(), nil)
    indicator := selectHealthIndicator(rows, in.TopoStatus)
    return fmt.Sprintf("Devbox: %s", indicator)
}
```

After:
```go
func HealthIndicator(in StatusInput) string {
    rows := collectRowsByType(in.Cfg, in.IsRunning, in.Cfg.Project.FullName(), nil)
    return selectHealthIndicator(rows, in.TopoStatus)
}

func RenderHealth(in StatusInput) string {
    return "Devbox: " + HealthIndicator(in)
}
```

Existing `RenderHealth` tests still pass without modification (substring assertions on the indicator glyph).

### Program lifecycle (mirrors `cmdbrowser/run.go`)

```go
func Run(ctx context.Context, d Deps) error {
    if !isTerminalFn(os.Stdout.Fd()) {
        return errors.New("statustui: not a terminal")  // caller guards before calling
    }
    width, height, _ := terminalSizeFn()
    runCtx, cancel := context.WithCancel(ctx)
    defer cancel()  // canceling on return guarantees in-flight buildTabs goroutines stop when the user quits
    m := newModel(d, runCtx, width, height)
    prog := tea.NewProgram(m, tea.WithContext(runCtx))
    runErr := ui.RunWithPromptHooks(func() error {
        _, e := prog.Run()
        return e
    })
    return mapRunError(runErr)
}

// mapRunError translates bubbletea's exit errors into a cobra-friendly return.
// Extracted as a separate function so it can be unit-tested without spinning up
// a real tea.Program. CRITICAL: check ErrProgramPanic before ErrProgramKilled
// because v2 wraps recovered panics as `ErrProgramKilled: ErrProgramPanic`
// (~/go/pkg/mod/charm.land/bubbletea/v2@v2.0.5/tea.go:38-42, :1025-1030);
// a naive `errors.Is(err, tea.ErrProgramKilled)` first would swallow panics
// as clean exits.
func mapRunError(err error) error {
    if err == nil {
        return nil
    }
    if errors.Is(err, tea.ErrProgramPanic) {
        return err
    }
    if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
        return nil  // user-initiated exit (q / ctrl+c), not an error
    }
    return err
}
```

**bubbletea/v2 API notes (verified against `charm.land/bubbletea/v2 v2.0.5`):**
- `tea.NewProgram` accepts `WithContext(ctx)` but does NOT have `WithAltScreen()`. In v2 the alt-screen is set per frame inside `View()` by populating a `tea.View` struct with `AltScreen: true` — exactly how `cmdbrowser/model.go:428` does it (`v := tea.NewView(content); v.AltScreen = true; return v`). Confirmed by upstream upgrade guide at `~/go/pkg/mod/charm.land/bubbletea/v2@v2.0.5/UPGRADE_GUIDE_V2.md:287-291`.
- `Model.View()` returns `tea.View` (a struct with `Content string`, `AltScreen bool`, optional cursor/layers), NOT `string`. All `View()` implementations in the plan return `tea.NewView(content)` with `v.AltScreen = true`.
- Panic recovery: bubbletea v2 catches panics by default and wraps them as `tea.ErrProgramKilled: tea.ErrProgramPanic`. The Run function MUST check `ErrProgramPanic` before mapping `ErrProgramKilled` to nil, otherwise crashes are silently swallowed.
- Goroutine cancellation: `tea.Cmd` goroutines cannot be canceled by bubbletea (`tea.go:716-720` notes commands may outlive the program). The plan owns a `runCtx` in `Run` and cancels on return so `CollectGitWorkspace`/`CollectDaemons` (which honor ctx — `internal/stack/gitworkspace.go:98`, `internal/stack/daemons.go:68`) abort cleanly when the user quits mid-reload.

## What Goes Where

- **Implementation Steps (`[ ]`):** all code changes in this repo — new package, refactor in `internal/stack`, dispatcher in `internal/command/status.go`, tests for each.
- **Post-Completion (no checkboxes):** manual smoke test of the TUI in a real terminal across multiple window sizes (the only thing tests can't cover).

## Implementation Steps

### Task 1: Extract `HealthIndicator` from `RenderHealth`

**Files:**
- Modify: `internal/stack/status.go`
- Modify: `internal/stack/status_test.go`

- [x] add exported `HealthIndicator(in StatusInput) string` returning just the indicator (no `Devbox: ` prefix)
- [x] refactor `RenderHealth` to `"Devbox: " + HealthIndicator(in)`
- [x] add `TestHealthIndicator_*` tests mirroring `TestRenderHealth_*` (running / stopped / partial) asserting on the glyph + textual state, without the `Devbox: ` prefix
- [x] verify existing `TestRenderHealth_*` tests still pass unchanged
- [x] run `make test` — must pass before next task

### Task 2: Create `statustui` package skeleton with `Deps`, keys, and styles

**Files:**
- Create: `internal/command/statustui/tui.go` (model struct, newModel, View skeleton, Init returns nil)
- Create: `internal/command/statustui/keys.go` (keyMap with `tab`/`shift+tab`/`left`/`right`/`1`-`5`/`r`/`q`/`?`/`ctrl+c`)
- Create: `internal/command/statustui/tui_test.go`

- [x] define `type Deps struct { Cfg *config.DevboxConfig; State *journal.ProjectState; Tracked []string; SvcDeploys map[string]*config.DeployConfig; ProjectName string; DockerCfg *config.DockerConfig; Topo map[string][]string; TopoStatus map[string]ui.NodeStatus; IsRunning stack.ContainerCheckFn; ProjectRoot string }`
- [x] define `type tab struct { title, content string }` and `type model struct { ... }` per Technical Details
- [x] define `keyMap` with named bindings (`NextTab`, `PrevTab`, `Tab1..Tab5`, `Reload`, `Help`, `Quit`); implement `ShortHelp()` / `FullHelp()` for `bubbles/v2/help`
- [x] implement `newModel(d Deps, ctx context.Context, w, h int) *model` (returns **pointer**, not value — methods use pointer receivers so only `*model` implements `tea.Model`; passing a value to `tea.NewProgram` would fail at compile time). Initialise viewport, help, keyMap, spinner, `loading=true`, storing `ctx` on the model for later use by `buildTabsCmd`. Mirrors `cmdbrowser/model.go:71-75` and `cmdbrowser/run.go:136-137`
- [x] add a static interface assertion at package scope: `var _ tea.Model = (*model)(nil)` — catches receiver-type mistakes at compile time rather than at first `tea.NewProgram` call
- [x] write placeholder methods using **pointer receivers** (`func (m *model) View() tea.View { return tea.NewView("loading") }`, `func (m *model) Init() tea.Cmd { return nil }`, `func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return m, nil }`). Pointer receivers are required because later tasks mutate model state in `Init`/`Update` (`m.loadGen++`, etc.) — value receivers would silently discard those mutations
- [x] add a smoke test `TestNewModel_Defaults` asserting model fields are wired (tabs empty, loading true, active 0)
- [x] run `make test` — must pass before next task

### Task 3: Implement `buildTabs` (serial section rendering)

**Files:**
- Create: `internal/command/statustui/load.go`
- Create: `internal/command/statustui/load_test.go`

- [x] implement `joinNonEmpty(parts ...string) string` helper: drops empty/whitespace-only strings, joins the rest with `"\n"` (single newline between sections — each renderer already emits its own trailing newline). One canonical separator, no double-blank-lines, no leading/trailing newline.
- [x] add package-level seams `collectDaemonsFn = stack.CollectDaemons` and `collectGitWorkspaceFn = stack.CollectGitWorkspace` so tests can substitute the slow shell-out paths (required for Task 6's reload-then-quit cancellation test — `stack.CollectDaemons` and `stack.CollectGitWorkspace` are concrete package functions with unexported internal seams, not injectable via `Deps`)
- [x] implement `buildTabs(ctx context.Context, d Deps) []tab` per the Technical Details snippet. For each `Render*` call, **destructure the `(body, errs)` return separately** — `body` strings go into `joinNonEmpty`, `errs` go into `warningPrefix(len(errs))` which is prepended. Direct `joinNonEmpty(stack.RenderApps(in), ...)` will not compile because Go does not auto-split multi-return into separate arguments
- [x] implement `warningPrefix(n int) string` that returns `""` when `n==0`, otherwise `ui.StyleWarning(fmt.Sprintf("⚠ %d expression(s) failed", n))` (use `ui.StyleWarning` at `internal/ui/styles.go:178-180`; do NOT use a raw "red" style — the codebase exports `StyleWarning` and `ColorWarning` as the canonical warning styling, not a `Red`/`Error` style)
- [x] for the Daemons tab, capture errors from `collectDaemonsFn` as a warning prefix (matches plain status at `internal/command/status.go:242-247`)
- [x] build the five sections **serially** (mirrors existing plain `renderDefaultStatus` loop at `internal/command/status.go:210-217`). Parallelism is unnecessary for a one-shot read-only viewer — total load time is dominated by `CollectGitWorkspace` (git shellouts) and `CollectDaemons` (docker ps); serial keeps the code simple and cancellation clean via the single `ctx` thread-through
- [x] handle empty tabs by returning a sentinel placeholder string from `buildTabs` (e.g. `"no apps configured"`, `"no git workspace tracked"`); the actual centering happens in `View` (Task 4) where `width`/`height` are known. `buildTabs` has no terminal dimensions
- [x] define `tabsLoadedMsg{gen uint64, tabs []tab, loadedAt time.Time, err error}` — the `gen` field is critical for Task 5's stale-message drop logic. Define `buildTabsCmd(ctx context.Context, d Deps, gen uint64) tea.Cmd` that captures `gen` and emits it back in the message: `func() tea.Msg { tabs := buildTabs(ctx, d); return tabsLoadedMsg{gen: gen, tabs: tabs, loadedAt: time.Now()} }`
- [x] implement `renderGitTab(ctx context.Context, d Deps) string`: calls `collectGitWorkspaceFn(ctx, d.Cfg, d.ProjectRoot)` then `ui.RenderGitWorkspace(rows)`; returns sentinel `"no git workspace tracked"` if rows is empty; counts errored rows and prepends `warningPrefix` for them (matches plain status at `internal/command/status.go:253-267`)
- [x] implement `normaliseDocker(d *config.DockerConfig) *config.DockerConfig`: returns `&config.DockerConfig{}` when `d == nil`, else `d` (mirrors `statusContext.normalisedDockerCfg` at `internal/command/status.go:106-111` — copying rather than importing because of the cycle)
- [x] add `TestJoinNonEmpty` covering: all-empty → `""`, single non-empty, multiple with empties between, whitespace-only treated as empty, no trailing/leading newlines
- [x] add `TestBuildTabs_AllRunning`, `TestBuildTabs_AllStopped`, `TestBuildTabs_Partial` using synthetic `*config.DevboxConfig` + `neverRunning`/`alwaysRunning`/`partialRunning` fakes (copy patterns from `internal/stack/status_test.go`)
- [x] add `TestBuildTabs_EmptyService` asserting placeholder text
- [x] add `TestBuildTabs_PrependsWarningOnError` using a config that forces a renderer to return errors (e.g., a service with an invalid custom-status template expression), asserting the rendered tab content starts with `⚠`
- [x] run `make test` — must pass before next task

### Task 4: Implement `View` (title bar, tab strip, viewport, status bar, divider, too-small fallback)

**Files:**
- Modify: `internal/command/statustui/tui.go`
- Modify: `internal/command/statustui/tui_test.go`

- [x] implement `renderTitleBar(m)` returning `ui.LogoMarkPlain() + " devbox · " + m.deps.ProjectName + " · Status"` styled with accent+bold from `ui/styles.go`
- [x] implement `renderTabStrip(m)` hand-rolled (Charm `bubbletea/examples/tabs` pattern): active tab wrapped in `▌` / `▐` with accent background; inactive tabs dimmed; tabs joined with `   `
- [x] implement `renderStatusBar(m)` with left side `<HealthIndicator> · loaded Ns ago` (or `· reloading…` when `m.reloading`) + optional `· pending: deploy(N)` in orange; right side from `m.help.View(m.keys)`. **Must call `m.help.SetWidth(m.width)` before `View` is called** — `bubbles/v2/help` only truncates when width is set (`~/go/pkg/mod/charm.land/bubbles/v2@v2.1.0/help/help.go:115-127`); without this the status bar overflows on narrow terminals
- [x] implement `View() tea.View` (returns `tea.View` struct, not string) composing title / tabs / divider / viewport / status bar via `lipgloss.JoinVertical`; show spinner centered when `m.loading`; show centered error + "press q to quit, r to retry" when `m.err != nil`; show "terminal too small (need 60×16)" when `m.width<60 || m.height<16`. Final return: `v := tea.NewView(content); v.AltScreen = true; return v` (mirrors `cmdbrowser/model.go:427-429`)
- [x] add `TestView_LoadingShowsSpinner`, `TestView_TooSmall`, `TestView_RendersTitleAndTabs`, `TestView_ErrorPathShowsRetryHint` (substring assertions on `model.View().Content` — remember it's now a struct field, not a return value)
- [x] run `make test` — must pass before next task

### Task 5: Implement `Init` and `Update` (tab cycling, digit jump, reload, quit, resize, scroll delegation)

**Files:**
- Modify: `internal/command/statustui/tui.go`
- Modify: `internal/command/statustui/tui_test.go`

- [x] **All model methods MUST use pointer receivers** (`func (m *model) Init() tea.Cmd`, etc.) — mirrors `cmdbrowser/model.go:169` precedent. With value receivers, `Init`'s mutations to `loadGen` would not persist. bubbletea v2 calls `Init()` once on the program model (`~/go/pkg/mod/charm.land/bubbletea/v2@v2.0.5/tea.go:1118`)
- [x] implement `Init() tea.Cmd`: bump `m.loadGen++` (initial load gets gen 1, NOT 0, so any zero-value `tabsLoadedMsg` from test code is always treated as stale and dropped); return `tea.Batch(m.spinner.Tick, buildTabsCmd(m.ctx, m.deps, m.loadGen))`. No need to send an initial `tea.WindowSizeMsg` manually: bubbletea v2 sends one on program start (`~/go/pkg/mod/charm.land/bubbletea/v2@v2.0.5/screen.go:5-11`, `tea.go:1091-1093`)
- [x] implement `Update(msg tea.Msg) (tea.Model, tea.Cmd)` handling:
  - `tea.WindowSizeMsg`: update `width`/`height`, recompute viewport size, call `m.help.SetWidth(m.width)`, re-`SetContent`.
  - `tabsLoadedMsg{gen, ...}`: **drop if `msg.gen != m.loadGen`** (a newer reload superseded this one). Otherwise populate tabs/err/reloadAt, clear `loading`/`reloading`. For YOffset restore: only if `m.reloadGen == msg.gen && m.reloadActive == m.active` → `m.viewport.SetYOffset(m.reloadYOffset)`; otherwise `GotoTop`. Then clear `m.reloadGen` (one-shot).
  - **`tea.KeyPressMsg`** (NOT `tea.KeyMsg` — bubbletea v2 emits both `KeyPressMsg` and `KeyReleaseMsg` per `~/go/pkg/mod/charm.land/bubbletea/v2@v2.0.5/key.go:257`; handling `KeyMsg` directly double-fires on terminals that emit releases. Bubbles `key.Matches` is documented under `case tea.KeyPressMsg` at `~/go/pkg/mod/charm.land/bubbles/v2@v2.1.0/key/key.go:21`) dispatched via `key.Matches`:
    - **NextTab/PrevTab** cyclic; **Tab1..Tab5** direct jump (no-op when index >= len(tabs)). **On any tab switch, clear `m.reloadGen = 0`** so a pending restore from a previous tab doesn't fire when its load completes.
    - **Reload**: bump `m.loadGen++`; capture `m.active`→`reloadActive`, `m.viewport.YOffset()`→`reloadYOffset`, `m.loadGen`→`reloadGen`; set `m.reloading=true`; fire `buildTabsCmd(ctx, deps, m.loadGen)` (the cmd captures gen into the message it emits).
    - **Help**: toggle `m.help.ShowAll`.
    - **Quit**: return `tea.Quit`.
  - `spinner.TickMsg`: delegate to spinner.
- [x] on tab switch: `m.viewport.SetContent(m.tabs[m.active].content)` + `m.viewport.GotoTop()`
- [x] delegate unmatched key/mouse msgs to `m.viewport.Update(msg)`. Default viewport keybindings in `bubbles/v2/viewport v2.1.0` (per `~/go/pkg/mod/charm.land/bubbles/v2@v2.1.0/viewport/keymap.go:22-57`): `↑`/`↓`, `j`/`k`, page up/down, space/`f`/`b`, `u`/`d` (half-page). **No** `g`/`G` or home/end — do not promise these in any documentation or `--help` text
- [x] add `TestUpdate_TabCycling`, `TestUpdate_DigitJump`, `TestUpdate_QuitReturnsTeaQuit`, `TestUpdate_ReloadFiresCmd`, `TestUpdate_WindowResize_RecomputesViewportAndHelp` (assert help width is set), `TestUpdate_PreservesYOffsetOnReload_SameTab`, `TestUpdate_ResetsYOffsetOnTabSwitch`, `TestUpdate_SpinnerTickAdvances`, `TestUpdate_StaleTabsLoadedMsgIgnored` (send a tabsLoadedMsg with gen lower than m.loadGen → state unchanged), `TestUpdate_TabSwitchInvalidatesPendingReloadRestore` (start a reload on tab A, switch to tab B, deliver tabsLoadedMsg → assert YOffset reset to 0, not the captured offset), `TestUpdate_MultipleReloads_DropsOlderResult` (press Reload twice without delivering any tabsLoadedMsg between; deliver msg with the earlier gen → ignored; deliver msg with the later gen → applied)
- [x] run `make test` — must pass before next task

### Task 6: Implement `Run` (TTY check, `tea.NewProgram`, error mapping)

**Files:**
- Modify: `internal/command/statustui/tui.go`
- Create: `internal/command/statustui/run.go`
- Create: `internal/command/statustui/run_test.go`

- [x] add package-level `isTerminalFn = func(fd uintptr) bool { return term.IsTerminal(fd) }` and `terminalSizeFn` overridable in tests
- [x] implement `Run(ctx context.Context, d Deps) error` per the snippet in Technical Details — own a child `runCtx` via `context.WithCancel(ctx)` + `defer cancel()`; build model with `runCtx`; call `tea.NewProgram(m, tea.WithContext(runCtx))` (NO `WithAltScreen` — alt-screen is set per-frame in `View`); wrap with `ui.RunWithPromptHooks`; **check `tea.ErrProgramPanic` BEFORE `tea.ErrProgramKilled`** so panics are not swallowed as clean exits; map `tea.ErrInterrupted`/`tea.ErrProgramKilled` → `nil` (user exit), propagate other errors
- [x] add `TestRun_NotATerminal_ReturnsError` (override `isTerminalFn` to return false)
- [x] extract `mapRunError(err error) error` as a free function so it is testable without `tea.NewProgram`. Add `TestMapRunError` as a table-driven test covering: nil → nil; `tea.ErrInterrupted` → nil; `tea.ErrProgramKilled` → nil; `tea.ErrProgramPanic` → non-nil (still wraps `ErrProgramPanic`); a panic wrapped as `fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrProgramPanic)` (the actual v2 wrap shape per `~/go/pkg/mod/charm.land/bubbletea/v2@v2.0.5/tea.go:1025-1030`) → non-nil; arbitrary other error → returned verbatim
- [x] add `TestRun_ReloadThenQuit_CancelsInflightContext` (in package `statustui`, NOT in `command` — it needs the unexported `collectDaemonsFn` / `collectGitWorkspaceFn` seams from Task 3):
  - Override `collectDaemonsFn` with a stub that signals `started` via a channel, then blocks on `<-ctx.Done()`, then closes an `exited` channel. **Also override `collectGitWorkspaceFn` to return `nil` immediately** — `buildTabs` runs sections serially, and a slow git stage would prevent the daemons stage from being reached. Both overrides via `t.Cleanup` restore. **Must not use `t.Parallel()`** (package-var seams).
  - Call `buildTabsCmd` directly with a cancellable context and run it in a goroutine so `buildTabs` executes.
  - Wait on the `started` channel (with a timeout, e.g. 1s) to confirm `collectDaemonsFn` is blocked. Then cancel the context (simulates quit-during-load).
  - Assert the stub's `exited` channel closes within 100ms; timeout → test fails (regression of the cancellation fix).
- [x] run `make test` — must pass before next task

### Task 7: Wire `shouldUseTUI` and `--no-tui` into root `status` command

**Files:**
- Modify: `internal/command/status.go`
- Modify: `internal/command/status_test.go` (or `status_extra_test.go`)

- [x] add `noTUI bool` to root `status` command's local flags: `cmd.Flags().BoolVar(&noTUI, "no-tui", false, "force plain text output even on a TTY")`
- [x] add package-level `isTerminalFn = func(fd uintptr) bool { return term.IsTerminal(int(fd)) }` (test seam)
- [x] add package-level `runStatusTUIFn = statustui.Run` (test seam — also makes the subcommand assertion in tests trivial)
- [x] implement `shouldUseTUI(noTUI bool, no *noSectionFlags) bool` per Technical Details snippet (only `TERM=dumb` short-circuit, NOT unset TERM)
- [x] in root `RunE`, after `loadStatusContext`, branch: if `shouldUseTUI(...)`, build `statustui.Deps` from `sc` and call `runStatusTUIFn(cmd.Context(), deps)`; else call `renderDefaultStatus`
- [x] add `TestShouldUseTUI_Matrix` covering: TTY + no flags → true; TTY + --no-tui → false; TTY + --no-apps → false (and same for each --no-<section>); non-TTY → false; TTY + `TERM=dumb` → false (use `t.Setenv`). Do NOT test "TERM unset → false" — the guard was dropped because it false-denies legitimate TTYs
- [x] add `TestStatusSubcommands_NeverInvokeTUI`:
  - Override `runStatusTUIFn` with a panicking stub and `isTerminalFn` with one returning `true`. Set `t.Setenv("TERM", "xterm-256color")`. Use `t.Cleanup` to restore both package vars after each subtest (required because they are package-level globals).
  - For each subcommand (`status apps`, `tools`, `infra`, `deploy`, `topology`, `git`, `daemons`): build the root command, `cmd.SetArgs(...)`, capture stdout via `cmd.SetOut(buf)`, run `cmd.Execute()`. **Assertions: (1) no panic from the stub, (2) `err == nil`, (3) stdout contains a plain-output marker** — e.g. the section title (`"Apps"` / `"Tools"` / `"Daemons"` / etc.). Without #3 the test passes falsely when setup/load fails before the command body runs.
  - These tests **must not use `t.Parallel()`** — they mutate `runStatusTUIFn` and `isTerminalFn` (package-level vars); concurrent mutation would race. Document this in a comment near the test.
- [x] run `make test` — must pass before next task

### Task 8: Verify acceptance criteria

- [ ] verify the five tabs render in the correct order (Services, Deploy, Topology, Git, Daemons) via `TestBuildTabs_*`
- [ ] verify title bar contains `▪`, `devbox`, project name, and `Status` via `TestView_RendersTitleAndTabs`
- [ ] verify status bar contains a health glyph and `loaded Ns ago` via `TestView_*` assertions
- [ ] verify `--no-tui`, `--no-<section>`, and non-TTY all force plain output via `TestShouldUseTUI_Matrix`
- [ ] verify subcommands are unaffected via `TestStatusSubcommands_NeverInvokeTUI`
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`
- [ ] run `make build` to confirm the binary builds

### Task 9: Update documentation and move plan to completed

**Files:**
- Modify: `docs/reference/cli/` (regenerate via `devbox docs generate` if the new `--no-tui` flag changes auto-generated CLI reference)
- Move: `docs/plans/2026-05-26-status-tui.md` → `docs/plans/completed/`

- [ ] regenerate CLI reference: `bin/devbox docs generate` (after `make build`)
- [ ] add a one-paragraph note about TUI auto-activation and `--no-tui` near the `devbox status` reference if there's a freeform section (skip if reference is fully auto-generated)
- [ ] `mkdir -p docs/plans/completed && mv docs/plans/2026-05-26-status-tui.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention — informational only*

**Manual verification:**
- Launch `devbox status` in a real terminal and verify: tab cycling with `tab`/`shift+tab` and `←`/`→`, direct jump with `1`-`5`, reload with `r` (watch the status-bar timestamp tick), scroll within a long tab (Topology is the longest), `?` toggles help between short and long modes, `q` and `ctrl+c` both exit cleanly with the terminal restored.
- Resize terminal to 59×20 and 80×15 — verify the "terminal too small" fallback appears.
- Pipe to a file: `devbox status > /tmp/out` — verify plain text output (TUI must not activate).
- Run with `--no-tui` in a TTY — verify plain output.
- Run with `--no-apps` in a TTY — verify plain output (any `--no-<section>` flag forces plain).
- Run each subcommand (`devbox status apps`, `devbox status deploy`, etc.) — verify they always emit plain output, never the TUI.
- Verify the title bar logomark and accent color match `cmdbrowser`'s visual style (same accent, same glyph).

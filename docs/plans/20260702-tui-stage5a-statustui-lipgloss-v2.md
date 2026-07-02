# TUI Stage 5a: statustui off lipgloss v1 → v2

## Overview

Migrate `internal/core/ui/statustui` from lipgloss **v1**
(`github.com/charmbracelet/lipgloss`) to lipgloss **v2** (`charm.land/lipgloss/v2`).

This is **Stage 5a** of the Unified TUI Framework milestone
(`docs/plans/specs/2026-06-23-tui-framework-milestone.md`, § Stages row 5a). It is a
**pure dependency migration**: no framework redesign, no layout change, no async/reload
behavior change. The status dashboard keeps its current title-bar + tab-strip + divider +
viewport + status-bar layout and all keybindings — only the styling library changes.

`statustui` is the last interactive-chrome package still on lipgloss v1 (the rest of the
TUI stack — cmdbrowser, docstui, the `tui` framework — is already on v2). Getting it onto
v2 in isolation gives a small, byte-verifiable checkpoint *before* Stage 5b restructures
the surface onto the shared `Frame`.

**Problem it solves:** removes the stack-drift called out in the spec (§ Problems #3,
"statustui is on lipgloss v1 while the rest are on v2") and unblocks Stage 5b, which needs
a clean v2 body.

**Key benefit:** the interactive TUI chrome is wholly on v2 after this stage; the risky
mechanical import swap is verified independently (golden output unchanged) rather than
tangled into the behavioral Frame migration.

## Context (from discovery)

- **Files/components involved:**
  - `internal/core/ui/statustui/tui.go` — the ONLY file with v1 lipgloss (22 call sites
    across `renderTitleBar`, `renderTabStrip`, `renderStatusBar`, the divider line in
    `View`, the too-small view, and the loading view).
  - `internal/core/ui/statustui/tui_test.go` — existing golden/behavior tests; the gate.
  - Reference implementations already on v2: `internal/core/ui/docstui/*.go`,
    `internal/core/ui/cmdbrowser/*.go` (import `charm.land/lipgloss/v2`).
- **Related patterns found:**
  - v2 import path used everywhere else in the repo is `charm.land/lipgloss/v2`.
  - `statustui` already uses bubbles/v2 (`charm.land/bubbles/v2/{help,key,spinner,viewport}`)
    and bubbletea/v2 — only the lipgloss import is v1.
  - Color is read from `internal/core/ui/styles` string accessors
    (`styles.ColorAccent()`, `styles.ColorMuted()`), wrapped in `lipgloss.Color(...)`.
- **Dependencies identified:**
  - Tab **content** strings are produced by `internal/core/ui/render/`, which stays on
    lipgloss **v1** (explicitly OUT OF SCOPE per spec § Charm-stack scope). Those
    pre-rendered ANSI strings are placed verbatim into the v2 `viewport` — strings are
    strings, so no change is required there and no cross-version rendering issue arises.

## Development Approach

- **Testing approach:** Regular (code first, then verify tests). This is a mechanical
  port whose correctness contract *is* the pre-existing golden suite — the goal is
  byte-identical output, so the existing tests are the specification.
- Complete the single migration task fully, then verify the whole suite.
- **CRITICAL:** the existing golden tests in `tui_test.go` must produce **byte-identical**
  output after the swap. If any golden shifts, a v2 API mapping is wrong — investigate and
  fix the mapping, do NOT re-baseline the golden (a shift means behavior changed, which
  violates the "no layout change" scope).
- All tests must pass before this stage is considered done.
- Maintain backward compatibility (observable output identical).

## Testing Strategy

- **Unit/golden tests:** the pre-existing `statustui/tui_test.go` suite is the gate. No new
  test *cases* are required for a pure port; add a test only if the v2 API forces an
  unavoidable rendering difference (see Task 2) — in which case document why.
- **e2e tests:** none — this is an internal TUI package with no UI-based e2e harness.
- Run via `make test` (which syncs embedded docs first) or, for a focused loop,
  `make embedded-docs` once then `go test ./internal/core/ui/statustui/...`.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## Solution Overview

Swap the import and adjust each call site for the lipgloss v2 API. The v2 API is largely
source-compatible for the primitives `statustui` uses (`NewStyle`, `Width`, `Height`,
`Padding`, `Foreground`, `Bold`, `Align`, `AlignVertical`, `Render`, `JoinHorizontal`,
`JoinVertical`, `Height(...)`/`Width(...)` measurement helpers, `Color`), but a few
signatures/semantics differ between v1 and v2. Port site-by-site, then lean on the golden
suite to prove nothing moved.

**Key design decision:** do not touch layout, keymaps, async load, reload, or the
`render/` content pipeline. The blast radius is exactly `tui.go`'s styling calls. This
keeps the diff reviewable and the golden comparison meaningful.

## Technical Details

- **Import swap:** `github.com/charmbracelet/lipgloss` → `charm.land/lipgloss/v2` in
  `tui.go`. Run `goimports` so ordering/grouping matches the repo convention (charm.land
  imports grouped with the other `charm.land/*` deps).
- **Call sites to port (22):**
  - `viewportHeight()` — `lipgloss.Height(m.renderStatusBar())` measurement.
  - `renderTitleBar()` — `NewStyle().Width().Padding().Foreground(lipgloss.Color(...)).Bold().Render()`.
  - `renderTabStrip()` — active/inactive tab `NewStyle().Foreground(lipgloss.Color(...)).Bold().Render()`.
  - `renderStatusBar()` — `lipgloss.Width(...)` measurements, `NewStyle().Width().Render()`
    spacer, `lipgloss.JoinHorizontal(lipgloss.Top, ...)`, `NewStyle().Width().Padding().Render()`.
  - `View()` — divider `NewStyle().Foreground(lipgloss.Color(...)).Render(strings.Repeat(...))`;
    too-small `NewStyle().Width().Height().Align(lipgloss.Center).AlignVertical(lipgloss.Center).Render()`;
    loading `NewStyle().Width().Height().Align().AlignVertical().Render()` +
    `lipgloss.JoinVertical(lipgloss.Top, ...)`.
- **v1→v2 API points to watch** (verify each against how docstui/cmdbrowser call them):
  - `lipgloss.Color` — v2 `Color` construction (hex string still accepted; confirm the
    exact type expected by `Foreground`).
  - `lipgloss.Width` / `lipgloss.Height` measurement helpers — confirm they still exist as
    package funcs in v2 (used by `viewportHeight`/`renderStatusBar`).
  - `JoinHorizontal` / `JoinVertical` and the `lipgloss.Top` / `lipgloss.Center` /
    position constants — confirm names/positions unchanged in v2.
  - `Align` / `AlignVertical` — confirm v2 signatures.
- The `tea.View{AltScreen: true}` envelope, help/spinner/viewport/key models, and all
  message handling are **unchanged** — they are already v2.

## What Goes Where

- **Implementation Steps** (`[ ]`): the import swap + call-site port + golden verification,
  all inside this repo.
- **Post-Completion** (no checkboxes): a manual eyeball of `dwe status` on a real terminal
  to confirm colors/borders look identical (the goldens already prove the byte output, but
  a visual sanity check is cheap).

## Implementation Steps

### Task 1: Swap lipgloss v1 → v2 in statustui/tui.go

**Files:**
- Modify: `internal/core/ui/statustui/tui.go`

- [x] replace the import `github.com/charmbracelet/lipgloss` with `charm.land/lipgloss/v2`
      and run `goimports` to fix grouping/ordering
- [x] port `viewportHeight()`: `lipgloss.Height(...)` measurement to the v2 equivalent
      (source-compatible — `lipgloss.Height` is a v2 package func, unchanged)
- [x] port `renderTitleBar()`: `NewStyle().Width().Padding().Foreground(lipgloss.Color(styles.ColorAccent())).Bold().Render()`
      (source-compatible — `Color`/`Foreground`/`Bold`/`Padding`/`Width` unchanged in v2)
- [x] port `renderTabStrip()`: active/inactive tab styles (`Foreground(lipgloss.Color(...))`, `Bold`)
- [x] port `renderStatusBar()`: `lipgloss.Width(...)` measurements, spacer `NewStyle().Width().Render("")`,
      `lipgloss.JoinHorizontal(lipgloss.Top, ...)`, outer `NewStyle().Width().Padding().Render()`
- [x] port `View()`: divider style, too-small view (`Align`/`AlignVertical`/`Center`),
      loading view + `lipgloss.JoinVertical(lipgloss.Top, ...)`
- [x] confirm no other `statustui` file imports v1 lipgloss:
      `grep -rn "charmbracelet/lipgloss\"" internal/core/ui/statustui/` returns nothing
- [x] build the package: `go build ./internal/core/ui/statustui/...`

### Task 2: Verify golden/behavior parity

**Files:**
- Modify (only if unavoidable): `internal/core/ui/statustui/tui_test.go`

- [x] run the suite: `make embedded-docs` once, then `go test ./internal/core/ui/statustui/...`
- [x] confirm every existing golden passes **byte-identical** (no re-baselining)
- [x] if a golden shifts: treat as a mapping bug — fix the v2 call so output matches; do NOT
      accept the shift. Only if a v2 API difference is genuinely unavoidable, update the
      golden AND add a `⚠️`-prefixed note here explaining the exact difference and why it is
      forced (e.g. a documented v2 rendering change), so Stage 5b inherits the rationale
      (no golden shifted — all pass byte-identical, no re-baselining needed)
- [x] verify existing behavior tests (reload/YOffset/tab-switch, loading, too-small,
      spinner) still pass unchanged
- [x] run `make lint` — `golangci-lint` clean (no unused import, gofmt/goimports satisfied)

### Task 3: Verify acceptance criteria
- [ ] verify the only change is the lipgloss import + its call sites in `tui.go`
      (`git diff --stat` shows `tui.go` and at most a golden note in `tui_test.go`)
- [ ] verify no layout/keymap/async/reload behavior changed (goldens + behavior tests green)
- [ ] verify `render/` (v1 lipgloss) was NOT touched: `git diff --name-only` excludes
      `internal/core/ui/render/`
- [ ] run full suite: `make test`
- [ ] run `make lint`

### Task 4: [Final] Update documentation & wrap up
- [ ] no user-facing doc changes (internal chrome only); confirm `docs/internals/packages.md`
      statustui note does not assert "lipgloss v1" anywhere — if it does, correct it to v2
- [ ] CLAUDE.md: no new pattern to record (Stage 5b will carry the framework note)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification:**
- Run `dwe status` in a real project on a normal (≥60×16) terminal and eyeball that the
  title bar, active/inactive tabs, divider, viewport content, and status bar render with
  identical colors and spacing to pre-migration. The goldens prove the byte output; this is
  a cheap visual sanity check on a real terminal profile.
- Confirm behavior under a dark and a light terminal background is unchanged (color
  accessors are theme-driven via `styles`).

**Follow-up (out of scope, tracked by the milestone):**
- `internal/core/ui/render/`'s lipgloss **v1** tables remain — explicitly deferred per spec
  § Charm-stack scope; not part of this stage or Stage 5b.
- Stage 5b (`docs/plans/20260702-tui-stage5b-statustui-frame.md`) restructures statustui
  onto the shared `Frame`; it depends on this stage landing first.

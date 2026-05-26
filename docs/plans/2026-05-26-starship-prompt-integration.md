# Starship Prompt Integration

## Overview

Add a `devbox prompt` command that prints a compact, prompt-ready segment for shell prompts. Starship users wire it into `starship.toml` as a custom module.

The output is a single line: `{▪} <project-name> <status-icon>` where:

- `{▪}` is the Devbox logomark with the inner square colored using the project's accent token (same color used by `internal/ui.LogoMark()`)
- `<project-name>` comes from `project.name` in `devbox.yml` (fallback: directory basename)
- `<status-icon>` is one of `✓` (deployed, success color), `⟳` (pending apply, warning color), `⚠` (partial deploy, warning color), or `✗` (failed, danger color); omitted when the project has no deploy state. Status icon colors are sourced from the project's `styles.yml` tokens (`success`/`warning`/`danger`), so a project that customises its palette gets a matching prompt.

Two surfaces:

- `devbox prompt` — prints the segment to stdout; exit `0` inside a project, exit `1` outside
- `devbox prompt --check` — silent, exit-only variant for starship's `when =` predicate

Performance constraint: the command runs on every shell prompt. The cold path must skip cobra wiring, config validation, registry building, and lipgloss. A bypass in `cmd/devbox/main.go` dispatches to a minimal `internal/prompt.Run` before cobra is constructed.

## Context (from discovery)

- Project root marker: `devbox.yml` (constant `project.ConfigFilename` = `"devbox.yml"`)
- Project name source: `project.name` field in `devbox.yml`
- Deploy state: `.devbox/deploy/state.yml` relative to project root (constant `journal.DefaultRelPath` in `internal/deploy/journal/state.go`); written atomically via temp+rename
- Status semantics (from `journal.ProjectState` in `internal/deploy/journal/state.go`):
  - `state.Project.Status == StatusFailed` → `failed` (`✗`, danger)
  - `state.Project.Status == StatusPartial` → `partial` (`⚠`, warning)
  - `state.Pending != nil` (and not failed/partial) → `pending` (`⟳`, warning)
  - `state.Project.Status == StatusDeployed` (and not failed/partial/pending) → `deployed` (`✓`, success)
  - else / missing file / parse error → no icon
  - Precedence rationale: failures/partial state are stop-conditions and must surface even when there are also pending changes — a user needs to know "things are broken" before they think about "I have changes to apply"
- Color tokens: `devbox/styles.yml` has top-level `colors:` block with 7 hex string tokens (`accent`/`success`/`warning`/`danger`/`muted`/`border`/`text`); see `internal/config/styles.go` `StylesColors` struct. We read accent + success + warning + danger. Built-in fallbacks (when token is empty or styles.yml is absent) mirror the dark-variant defaults from `internal/ui/styles.go`:
  - accent: `#2EC3EB`
  - success: `#22C55E`
  - warning: `#EAB308`
  - danger: `#EF4444`
- Existing logomark renderer: `internal/ui/logo.go` — uses lipgloss; we cannot reuse it in hot path (lipgloss downgrades to no-color on piped stdout and adds termenv import cost)
- Existing project walker: `internal/project/project.go` — `project.Locate(flag string) (Resolved, bool, error)` does the walk-up. We do **not** reuse it from the hot path because (a) it reads `os.Getwd()` internally, blocking the cwd-injection required for parallel tests, and (b) it calls `filepath.EvalSymlinks` which is unnecessary syscall overhead for prompt purposes. Instead the hot path duplicates a ~10-line `findRoot(start string) (string, bool)` (stat loop, no symlink resolution, no schema validation). Acceptable duplication per brainstorm decision
- Existing cobra bootstrap: `cmd/devbox/main.go` already loads styles.yml before cobra via `loadHelpColorScheme` — prompt bypass slots into the same pre-cobra window
- Existing cobra subcommand registration pattern: `NewRootCmd()` in `internal/command/root.go` uses `addCmd(root, group, newXxxCmd(flags))` calls; new prompt command follows the same shape with `newPromptCmd(flags)`

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - cover success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` after each task
- this CLI is pre-release: no backwards-compat constraints (see CLAUDE.md "Project Status & Compatibility Policy")

## Testing Strategy

- **Unit tests**: required for every task; table-driven where applicable
- **No e2e**: no UI surface; integration with starship is configuration-only and verified manually
- **Benchmark**: `BenchmarkPromptRun` in `internal/prompt/prompt_test.go` — target < 1 ms wall time excluding process startup; verifies hot path stays light
- **Manual verification**: out of scope for this plan — real-project verification happens in a separate follow-up after the plan is complete

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**Hot path bypass.** In `cmd/devbox/main.go`, before any cobra wiring or style preloading, check if argv looks like a prompt invocation (`os.Args[1] == "prompt"` and the remaining args are exactly empty or `--check`). If so, dispatch to `prompt.Run(os.Stdout, args)` and `os.Exit` with its return code. Any other shape (e.g. `prompt --help`, `prompt foo`) falls through to cobra so help and unknown-arg errors render normally.

**Minimal `internal/prompt` package.** Single public function `Run(stdout io.Writer, args []string) int`. No cobra, no lipgloss, no `errors.Is` chains. Direct stdlib + `gopkg.in/yaml.v3` + `internal/project` (reused for `Locate`) + a copy of the accent-color resolution logic from `internal/ui/styles.go` (small enough to duplicate per "duplication vs abstraction" decision in brainstorm).

**Cobra stub for discoverability.** Add `internal/command/prompt.go` with a cobra command that exposes `devbox prompt` in `--help` listings and shell completion. Its `RunE` calls the same `prompt.Run` as a safety fallback (if the main.go bypass is ever removed, behavior stays correct — just slower).

**Status mapping (prompt scope, narrower than journal's full state machine):**

Evaluated in this precedence order — first match wins:

| Order | Condition | Prompt icon | Color token |
|---|---|---|---|
| 1 | `state.Project.Status == StatusFailed` | `✗` | `danger` |
| 2 | `state.Project.Status == StatusPartial` | `⚠` | `warning` |
| 3 | `state.Pending != nil` | `⟳` | `warning` |
| 4 | `state.Project.Status == StatusDeployed` | `✓` | `success` |
| 5 | else (`StatusNotDeployed`, missing file, parse error) | (omitted) | — |

`failed`/`partial` outrank `pending` so the prompt surfaces broken state first — users need to know things are wrong *before* they think about applying pending changes. `pending` outranks `deployed` for the same reason — pending is the actionable signal. `partial` and `pending` share the `warning` color token but use distinct glyphs (`⚠` vs `⟳`).

**Color profile.** Hot path emits TrueColor ANSI (`\x1b[38;2;R;G;Bm…\x1b[39m`) unconditionally for the logo and status icon. `NO_COLOR` (per https://no-color.org/) suppresses all escapes when **the variable is set at all** — including `NO_COLOR=""`. Implementation uses `os.LookupEnv("NO_COLOR")` and suppresses on `found == true`, regardless of value. Output becomes `{▪} my-project ✓` plain. No light/dark auto-detect for V1: always use the Dark variant of the palette (most terminals are dark; documented limitation, easy to extend later via `COLORFGBG` if asked).

**Output assembly.** Use `strings.Builder` with pre-allocated capacity (~80 bytes typical) and `strconv.AppendUint` for the R/G/B integers in SGR sequences. Do NOT use `fmt.Sprintf` in the render path — it allocates a formatter object and parses the format string on every call. The render function is the hottest piece of the hot path.

**Error policy.** Any failure (yaml parse, IO, missing field) → silent exit `1` with no stderr write. Prompts must never pollute shell output. Optional `DEVBOX_PROMPT_DEBUG=1` env enables stderr diagnostics for development.

## Technical Details

### `internal/prompt/prompt.go` API

```go
package prompt

// Run is the public entry point — resolves cwd via os.Getwd() and dispatches
// to runFromDir. Used by cmd/devbox/main.go hot-path bypass and by the cobra
// stub's RunE.
//
// stdout receives the rendered segment (one line ending in \n).
// args excludes argv[0] and argv[1] ("prompt"); the only supported flag is --check.
// Returns process exit code (0 success, 1 not-in-project or silent failure).
func Run(stdout io.Writer, args []string) int

// runFromDir is the internal testable form — accepts an explicit starting
// directory instead of reading os.Getwd(). Lets tests run with t.Parallel()
// because no process-wide state (cwd, env) is touched per invocation.
func runFromDir(stdout io.Writer, args []string, cwd string) int
```

Tests call `runFromDir` directly with `t.TempDir()`-based fixture roots. Production path stays one-line: `Run` calls `os.Getwd()` then `runFromDir`. The `os.Getwd` error case (cwd deleted underneath the shell) collapses to silent exit 1.

### Data structures (stub structs, lenient yaml.Unmarshal)

```go
// devbox.yml — only the field we need
type devboxStub struct {
    Project struct {
        Name string `yaml:"name"`
    } `yaml:"project"`
}

// devbox/styles.yml — accent + the three status colors
type stylesStub struct {
    Colors struct {
        Accent  string `yaml:"accent"`
        Success string `yaml:"success"`
        Warning string `yaml:"warning"`
        Danger  string `yaml:"danger"`
    } `yaml:"colors"`
}

// .devbox/deploy/state.yml — only what determines the icon
type stateStub struct {
    Project struct {
        Status string `yaml:"status"`
    } `yaml:"project"`
    Pending *struct{} `yaml:"pending"` // existence is what matters
}
```

### Processing flow (`prompt.Run`)

1. Parse args: empty → render mode; `["--check"]` → quiet/check mode; anything else → exit 1 silent
2. `findRoot(cwd)` — local walk-up. Not found → exit 1 silent
3. `os.ReadFile(<root>/devbox.yml)` + `yaml.Unmarshal` → name; empty → `filepath.Base(root)`
4. `os.ReadFile(<root>/devbox/styles.yml)` + `yaml.Unmarshal` → 4 colors (accent/success/warning/danger). Each empty/missing field falls back to its built-in default. styles.yml absent → all four use defaults.
5. `os.ReadFile(<root>/.devbox/deploy/state.yml)` + `yaml.Unmarshal` → apply precedence table → icon + color token; missing/parse error → no icon
6. If `--check` → return 0 (no output)
7. Render to stdout: `{▪} <name>[ <icon>]\n`. Each colored glyph wrapped in its own SGR pair (`\x1b[38;2;R;G;Bm…\x1b[39m`). `NO_COLOR` set → emit plain runes.
8. Return 0

### `cmd/devbox/main.go` bypass

```go
func main() {
    if isPromptInvocation(os.Args) {
        os.Exit(prompt.Run(os.Stdout, os.Args[2:]))
    }
    // ...existing cobra wiring
}

// isPromptInvocation returns true only for `devbox prompt` and `devbox prompt --check`.
// Returns false for `prompt --help`, `prompt -h`, `prompt foo`, etc. — those fall through to cobra.
func isPromptInvocation(argv []string) bool {
    if len(argv) < 2 || argv[1] != "prompt" {
        return false
    }
    rest := argv[2:]
    if len(rest) == 0 {
        return true
    }
    return len(rest) == 1 && rest[0] == "--check"
}
```

### Documentation file

`docs/reference/cli/starship.md` with:

- Snippet for `starship.toml` (the `[custom.devbox]` block)
- Explanation of `when` / `command` / `format` choices
- "Before / after" prompt example
- Note about `NO_COLOR` behavior
- Note about light/dark limitation

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code, tests, docs — achievable in this repo
- **Post-Completion** (no checkboxes): manual starship verification with a real terminal

## Implementation Steps

### Task 1: Create `internal/prompt` package with project resolution and name extraction

**Files:**
- Create: `internal/prompt/prompt.go`
- Create: `internal/prompt/prompt_test.go`

- [x] create package `prompt` with:
  - `Run(stdout io.Writer, args []string) int` — thin wrapper that calls `os.Getwd()` then dispatches to `runFromDir`. On `os.Getwd` error, return 1 silently
  - `runFromDir(stdout io.Writer, args []string, cwd string) int` — internal testable form; takes an explicit cwd so tests can run with `t.Parallel()`
- [x] implement `--check` flag handling (manual parse, no `flag` package): empty args → render, `["--check"]` → check-only, anything else → silent exit 1
- [x] implement walk-up: open-coded mini version that takes a starting dir (parameter), walks to root looking for `devbox.yml` — duplicating `project.locateDiscover` logic since it relies on `os.Getwd`. Skip `EvalSymlinks` cost; the prompt does not care about canonical paths
- [x] add `devboxStub` parser; fallback name = `filepath.Base(root)` when `project.name` is empty
- [x] for now: render `{▪} <name>\n` plain (no color, no status) — color and status added in Tasks 2–3
- [x] write table-driven tests, every subtest calls `t.Parallel()` and uses its own `t.TempDir()`:
  - in-project (name from config)
  - in-subdirectory (walk-up works)
  - name fallback to dir basename when `project.name` is empty
  - outside any project (exit 1, no output)
  - `--check` inside project (exit 0, no output)
  - `--check` outside any project (exit 1, no output)
  - unknown arg `["foo"]` (exit 1, no output)
  - corrupted `devbox.yml` (silent exit 1)
- [x] run `go test ./internal/prompt/...` — must pass before Task 2

### Task 2: Add deploy status detection from journal state

**Files:**
- Modify: `internal/prompt/prompt.go`
- Modify: `internal/prompt/prompt_test.go`

- [x] add `stateStub` (value-type fields: `Project struct{ Status string }`, `Pending *struct{}`) — value types are nil-safe; `Project.Status` zero-value is the empty string which maps to `statusNone`
- [x] add `readStatus(root string) statusKind` helper; any IO or yaml parse error returns `statusNone` silently (no logging, no panics — see safety notes below)
- [x] map outcomes to one of `statusNone`, `statusDeployed`, `statusPending`, `statusPartial`, `statusFailed` per the precedence table in Solution Overview
- [x] update render to append plain-rune icon (`✓`/`⟳`/`⚠`/`✗`/none) after name — colors come in Task 3
- [x] write tests, each with `t.Parallel()` and its own `t.TempDir()`, covering every row of the precedence table plus combinations:
  - state file absent → no icon
  - `pending` set, no project status → ⟳
  - `Status: deployed` → ✓
  - `Status: partial` → ⚠
  - `Status: failed` → ✗
  - `Status: deployed` + `pending` set → ⟳ (pending wins over deployed)
  - `Status: partial` + `pending` set → ⚠ (partial wins over pending)
  - `Status: failed` + `pending` set → ✗ (failed wins over pending)
  - `Status: not_deployed` → no icon
  - corrupted state.yml → silent fallback to no icon, no error
  - state.yml with extra/unknown fields → ignored (lenient decode, no failure)

Safety notes for this task:
- `gopkg.in/yaml.v3` `Unmarshal` allocates structures via reflection. Failure mode for our stub is malformed YAML (returns error → we treat as `statusNone`) — no nil-pointer panic possible because all stub fields are value types or explicitly-nil-checked pointers
- `os.ReadFile` on a file mid-rename is impossible: journal uses atomic write-temp + rename per CLAUDE.md, so the inode swap is atomic at the VFS layer

- [x] run `go test ./internal/prompt/... -race` — must pass before Task 3

### Task 3: Add color resolution from styles.yml and ANSI rendering

**Files:**
- Modify: `internal/prompt/prompt.go`
- Modify: `internal/prompt/prompt_test.go`

- [x] add `stylesStub` parser at `<root>/devbox/styles.yml` reading 4 tokens (accent/success/warning/danger); each missing/empty token uses its built-in default (accent `#2EC3EB`, success `#22C55E`, warning `#EAB308`, danger `#EF4444`)
- [x] add `parseHex(hex string) (r, g, b uint8, ok bool)` helper using `strconv.ParseUint(hex, 16, 8)` — tolerate `#` prefix optional, return `ok=false` on any parse failure (degrades that glyph to plain, no panic)
- [x] add `writeSGR(sb *strings.Builder, r, g, b uint8)` helper that writes `\x1b[38;2;R;G;Bm` using `strconv.AppendUint` — explicitly avoid `fmt.Sprintf` to keep allocations zero in render path
- [x] honor `NO_COLOR` env var (per https://no-color.org/): use `os.LookupEnv("NO_COLOR")` and suppress ANSI when `found == true`, **regardless of the value** (including empty string). Test both `NO_COLOR=""` (set, empty) and `NO_COLOR=1` (set, non-empty) cases — both must suppress
- [x] update render to use `strings.Builder` with `Grow(96)` (typical output ~80 bytes + safety margin); wrap only the colored glyphs (`▪` and the status icon) in SGR pairs; braces, space, and name stay uncolored so starship's `format` can re-style them
- [x] terminate every colored glyph's SGR pair with `\x1b[39m` (default foreground only) — NOT `\x1b[0m` which resets ALL attributes and would fight any outer styling from starship's `format`/`style`
- [x] write tests with `t.Parallel()` (use `t.Setenv("NO_COLOR", ...)` — `Setenv` is parallel-test-safe in Go 1.17+):
  - all 4 colors from styles.yml are applied (separate cases for each token)
  - missing styles.yml → all 4 defaults applied
  - empty individual token in styles.yml → that token's default applied
  - `NO_COLOR=1` strips ALL ANSI (logo + status icon both plain)
  - `NO_COLOR=""` (empty but set) ALSO strips ALL ANSI (spec-compliant)
  - `NO_COLOR` unset → ANSI present
  - status icon colors map correctly to status kind (deployed→success, pending→warning, partial→warning, failed→danger)
  - malformed accent hex (e.g. `"not-a-color"` or `"#XYZ"`) degrades that glyph to plain, no panic
  - SGR pairs use `\x1b[39m` (default foreground) NOT `\x1b[0m` (full reset)
  - output ends with `\n`
- [x] run `go test ./internal/prompt/... -race` — must pass before Task 4

### Task 4: Wire hot-path bypass in `cmd/devbox/main.go`

**Files:**
- Modify: `cmd/devbox/main.go`
- Modify: `cmd/devbox/main_test.go`

- [x] add `isPromptInvocation(argv []string) bool` helper at package level
- [x] add early `if isPromptInvocation(os.Args) { os.Exit(prompt.Run(os.Stdout, os.Args[2:])) }` as the first statement in `main()` — before `command.NewRootCmd` and `loadHelpColorScheme`
- [x] add import for `devbox-cli/internal/prompt`
- [x] write tests for `isPromptInvocation`: matches `["devbox","prompt"]`, matches `["devbox","prompt","--check"]`, rejects `["devbox","prompt","--help"]`, rejects `["devbox","prompt","-h"]`, rejects `["devbox","prompt","foo"]`, rejects `["devbox"]`, rejects `["devbox","status"]`
- [x] run `go test ./cmd/devbox/...` — must pass before Task 5

### Task 5: Add cobra `prompt` command for discoverability

**Files:**
- Create: `internal/command/prompt.go`
- Modify: `internal/command/root.go`
- Create: `internal/command/prompt_test.go`

- [x] create `newPromptCmd(flags *rootFlags) *cobra.Command` following the existing pattern (compare `newStatusCmd(flags)` at `internal/command/root.go:154`) with `Use: "prompt"`, short/long descriptions explaining it is for shell prompt integration, and `--check` as a local bool flag
- [x] set `SilenceUsage: true` and `SilenceErrors: true` on the command — prompt invocation errors must never print cobra usage banners (would corrupt the shell prompt output if the cobra path is ever reached)
- [x] set `Args: cobra.NoArgs` — `prompt` takes no positional arguments
- [x] `RunE` reconstructs args from the parsed `--check` flag and calls `prompt.Run(cmd.OutOrStdout(), reconstructedArgs)`. Return the resulting int via a small `exitError` wrapper if non-zero, OR use `os.Exit(code)` if cobra's RunE-to-exit-code path is awkward — verify the existing root command pattern (`internal/command/root.go`) for how exit codes are propagated (the codebase already uses an `interface{ ExitCode() int }` pattern per `cmd/devbox/main.go` lines 28-37)
- [x] do NOT set a `PersistentPreRunE` and do NOT depend on `cfg` resolution — completion path and `--help` rendering must not require a project to be present
- [x] use `cmd.OutOrStdout()` (NOT `os.Stdout`) so tests can capture output via `cmd.SetOut(buf)`
- [x] register on the root in `NewRootCmd` via the existing `addCmd(root, group, newPromptCmd(flags))` helper; pick or add an appropriate group (likely the same group as `status`, since this is informational)
- [x] write tests: `devbox --help` lists `prompt`, `devbox prompt --help` works without a project resolved, RunE through cobra produces same output as direct `prompt.Run` invocation
- [x] run `go test ./internal/command/... ./internal/prompt/... ./cmd/devbox/...` — must pass before Task 6

### Task 6: Add starship integration documentation

**Files:**
- Create: `docs/reference/cli/starship.md`
- Modify: `docs/reference/cli/` index file if one exists (verify during implementation)

- [x] write `docs/reference/cli/starship.md` with sections:
  - overview — what the integration does
  - installation — paste the `[custom.devbox]` block into `~/.config/starship.toml`
  - the snippet itself (from Technical Details)
  - customization — how to override `format`/`style`
  - behavior in non-color terminals — `NO_COLOR` strips all ANSI
  - status icons table — `✓` deployed, `⟳` pending, `⚠` partial, `✗` failed (with the precedence rule explained briefly)
  - known limitations — (a) no light/dark auto-detect; defaults to dark variant; (b) `devbox prompt` always walks up from `$PWD` and does not honour `-c` (intentional — prompt is per-shell-cwd); (c) shell-specific quoting notes for `command =` (sh/bash/zsh take it as-is; fish users may need different quoting)
- [x] include a before/after example showing the rendered prompt
- [x] mention performance: prompt invocation is < 50 ms cold start
- [x] no code tests for docs; verify the snippet manually in Post-Completion

### Task 7: Add benchmark and run full test suite

**Files:**
- Modify: `internal/prompt/prompt_test.go`

- [ ] add `BenchmarkPromptRun` covering the deployed-status case (most common); use `b.TempDir()` to construct a fixture project with `devbox.yml`, `devbox/styles.yml`, and `.devbox/deploy/state.yml`. NOTE: this microbench measures `runFromDir` body only — it does NOT validate the end-to-end 50 ms cold-start budget (that is verified in Task 8 manual smoke).
- [ ] use Go 1.24+ `for b.Loop() { … }` form (not legacy `b.N`)
- [ ] call `b.ReportAllocs()` — allocation count is the strongest regression signal for hot paths; baseline allocations should be < ~30/op (3 file reads × yaml unmarshal allocs + builder)
- [ ] document baseline result (ns/op + B/op + allocs/op) in a comment so regressions are visible in diffs
- [ ] run `make test` — full suite must pass
- [ ] run `make lint` — must pass (this CLI uses `errcheck`, `govet`, `staticcheck`, `revive`, `gocritic`, `modernize` per CLAUDE.md)

### Task 8: Verify acceptance criteria

- [ ] verify all items from Overview are implemented (covered by automated tests from Tasks 1–5)
- [ ] verify edge cases via tests: outside project, corrupted yaml files, missing journal, `NO_COLOR`, `--check`, subdirectory walk-up, missing `project.name`
- [ ] run `make test` — final full pass
- [ ] run `make build` — confirm the binary builds cleanly

Note: end-to-end timing and real-project starship integration verification are deliberately out of scope for this plan — they happen in a separate follow-up.

### Task 9: Final documentation and plan move

**Files:**
- Modify: `docs/reference/cli/starship.md` (if any rough edges discovered in Task 8)
- Modify: `CLAUDE.md` / `AGENTS.md` — add a Key Patterns entry about the prompt bypass (it's a non-obvious architectural choice future contributors will want to know about)
- Move: this plan file to `docs/plans/completed/`

- [ ] add Key Patterns entry to `AGENTS.md` (remember: edit `AGENTS.md`, `CLAUDE.md` is a symlink): "**Prompt hot path**: `devbox prompt` and `devbox prompt --check` bypass cobra entirely via an early dispatch in `cmd/devbox/main.go` to keep per-shell-prompt cost minimal. The `internal/prompt` package duplicates the accent-color resolution from `internal/ui/styles.go` deliberately — it cannot use lipgloss because lipgloss auto-downgrades to no-color when stdout is piped (which it is, when starship invokes `devbox prompt`). A cobra `prompt` command exists in `internal/command/prompt.go` for `--help` discoverability only — its RunE is unreachable in normal startup."
- [ ] `mkdir -p docs/plans/completed && git mv docs/plans/2026-05-26-starship-prompt-integration.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only*

**Real-project verification** (separate follow-up, not part of this plan):

- Install starship if not already installed
- Add the `[custom.devbox]` block from `docs/reference/cli/starship.md` to `~/.config/starship.toml`
- Open a new shell, `cd` into a project containing `devbox.yml` — the prompt should show `{▪} <project-name>` with the accent-coloured logomark, plus a status icon if the project has deploy state
- Verify that `cd` into a subdirectory of the project still shows the segment (walk-up)
- Verify that `cd` to a directory outside any project makes the segment disappear
- Test `NO_COLOR=1` (or inspect output via `devbox prompt | cat -v`) to confirm escapes are suppressed
- Time the cold path: `time devbox prompt` — should be well under 50 ms on a modern machine

**External system updates**:

- None. Devbox is pre-release with no external consumers (per CLAUDE.md compatibility policy).

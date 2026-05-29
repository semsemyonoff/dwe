# JSON output for read-only state commands

## Overview

Add a global `--output text|json` flag to the devbox CLI and migrate every read-only "reporting" command to emit a structured JSON document when JSON mode is selected. The goal is to let AI agents and CI scripts consume devbox state machine-readably, with minimal token overhead.

Scope is intentionally read-only — mutation commands (run/stop/restart/reset/deploy run), MCP server, JSON Schema, and `agent_safe` semantics are all deferred to later waves. The two existing per-command `--json` flags on `snapshot list` and `snapshot inspect` are removed and migrated to the global flag (breaking change is fine — pre-release per CLAUDE.md).

Big-bang refactor in a single PR. Devbox philosophy (CLAUDE.md): "Mechanical refactors are cheap; carrying two parallel models is expensive."

## Context (from discovery)

- **Project**: Go CLI (`cmd/devbox`), layered as `internal/cli/` (cobra), `internal/core/` (domain), `internal/shared/` (infra).
- **Existing JSON precedent**: `snapshot list --json` and `snapshot inspect --json` (in `internal/cli/snapshot/snapshot.go`) — already have well-shaped DTOs (`snapshotListJSONEntry`, inline inspect struct with `Source`, `Manifest`, `CurrentConfigHash`, `ConfigHashDiverged`, `ServicesDiff`).
- **No global `--output` flag** exists today on `RootFlags` (`internal/cli/cmdctx/flags.go`). Fields are: `ConfigPath`, `Root`, `StylesCfg`, `Locale`, `I18n`.
- **Root PersistentPreRunE** (in `internal/cli/root.go`) handles project resolution, schema validation, allowlists (`allowedWithoutProject`), locale setup. Side-effects for JSON mode (NO_COLOR, SilenceErrors) plug into this hook.
- **`cmd/devbox/main.go`** uses `fang.Execute` from charm.land with a custom `errHandler`. Error envelope emission integrates there.
- **`samber/oops` NOT in `go.mod`** — designed plan was going to use it but pivoted to stdlib-based typed error `cmdctx.CodedError`.
- **CLAUDE.md rules respected**: `internal/cli/` is the single writer of stdout/stderr; `internal/core/ui/*` returns strings; `internal/core/project/stack/*` returns data (effectively DTOs).

## Development Approach

- **Testing approach**: Regular (code first, then tests). Project convention is table-driven tests next to code; the refactor is largely mechanical and benefits from writing the helper, validating shape, then locking via tests.
- Complete each task fully before moving to the next.
- Small, focused commits within the big PR (one logical group of changes per task).
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task — tests are not optional. Tests cover both success and error scenarios.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task; `make lint` before final task.
- No backwards compatibility for snapshot `--json` — deleted in same PR.

## Testing Strategy

- **Unit tests**: required for every task per Development Approach.
- **Per-command golden tests**: `internal/cli/<cmd>/testdata/<cmd>.json.golden` captures JSON shape; run command in test, diff buffer vs golden. Existing text-mode tests untouched.
- **Build a fresh cobra root per test** (golang-spf13-cobra Common Mistakes): cobra accumulates flag state across `Execute()` calls on the same instance — every test calls `cli.NewRootCmdWithFlags()` to get a clean tree. Do NOT share a root variable across `t.Run` cases.
- **No e2e UI tests** (devbox has no UI-driven e2e suite). Closest equivalent is integration tests in `internal/cli` that exercise the full cobra tree.
- **Acceptance verification**: `make test`, `make lint`, and a manual smoke pass on each migrated command (see Task 13).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## Solution Overview

**Architecture**: One generic helper in `internal/cli/cmdctx/output.go` dispatches between text and JSON modes based on `RootFlags.Output`. The `ui` layer stays text-only (returns strings); the `stack` layer stays output-agnostic (returns data). Commands compose them via the helper:

```go
return cmdctx.WriteData(rflags, cmd, data, ui.RenderApps)
```

**Error envelope**: A typed `CodedError` wraps user-facing errors with `Code`, `Message`, `Hint`, `Details`. In JSON mode, root's error handler emits the envelope as JSON to stderr; in text mode, fang's default styled output is used.

**Key design decisions** (locked in brainstorm):
- Compact JSON by default (token efficiency); `--pretty` for indented.
- Naked data on stdout (jq-friendly), JSON error envelope on stderr.
- snake_case field names, RFC3339 timestamps, seconds/bytes as numbers, `omitempty` for optional.
- TUI auto-disable when `--output json` (status/cmdbrowser).
- `NO_COLOR=1` auto-set so lipgloss-styled text doesn't pollute output.
- No schema version field (pre-release; CLAUDE.md compliant).

## Technical Details

### `cmdctx.RootFlags` (modified)
```go
type RootFlags struct {
    ConfigPath string
    Root       string
    StylesCfg  *config.StylesConfig
    Locale     string
    I18n       *i18n.Store
    Output     string  // new: "text" (default) | "json"
    Pretty     bool    // new: when Output=="json", indent
}
```

### `cmdctx.WriteData[T]` (new)
```go
func WriteData[T any](
    flags *RootFlags,
    cmd *cobra.Command,
    data T,
    renderText func(T) string,
) error
```
- `Output == "json"`: `json.NewEncoder(cmd.OutOrStdout())`, `SetIndent("", "  ")` iff `Pretty`
- `Output == "text"` (or empty): `fmt.Fprintln(cmd.OutOrStdout(), renderText(data))`

### `cmdctx.CodedError` (new)
```go
type CodedError struct {
    Code    string
    Message string
    Hint    string
    Details map[string]any
    Wrapped error
}

func (e *CodedError) Error() string { return e.Message }
func (e *CodedError) Unwrap() error { return e.Wrapped }

// Constructors:
func Err(code, message string) *CodedError
func ErrWrap(code string, err error) *CodedError
// chainable: .WithHint(...), .WithDetail(k, v)
```

### `cmdctx.WriteError` (new)
- Extracts `*CodedError` via `errors.As`; falls back to `Code: "internal_error", Message: err.Error()`
- JSON mode: writes envelope to `cmd.ErrOrStderr()`
- Text mode: no-op (fang handles)

### Root flags exposure (additive, not breaking)
The existing `cli.NewRootCmd()` returns `*cobra.Command` and is called by 14+ test files (`internal/cli/fang_integration_test.go`, `internal/cli/info/info_test.go`, `internal/cli/prompt/prompt_test.go`, etc.) — changing its signature would cascade across the whole test suite. Instead, add a sibling constructor `cli.NewRootCmdWithFlags() (*cobra.Command, *cmdctx.RootFlags)` and have `NewRootCmd()` call it and discard flags. Only `cmd/devbox/main.go` uses the new variant to thread `*RootFlags` into the error handler closure.

### Side-effects in `PersistentPreRunE`
When `flags.Output == "json"`:
- `os.Setenv("NO_COLOR", "1")` BEFORE `applyStyles`
- `cmd.Root().SilenceErrors = true`
- `cmd.Root().SilenceUsage = true`

**Best practice note** (golang-cli skill): both `SilenceUsage` and `SilenceErrors` SHOULD arguably be `true` on the root command unconditionally — usage walls on every error are noise. Current devbox sets them per-command on a few places; consider promoting to root-level defaults in this PR (small change). The JSON-mode flip then becomes redundant but harmless. Optional but recommended.

### Exit code mapping
- `0` — success
- `1` — generic runtime error (project_not_found, docker_unavailable, etc.)
- `2` — usage error (invalid `--output bogus`, missing required flag) — follows BSD sysexits convention; standard CLI ergonomic
- `1` for validate with severity error or warning+strict (existing behavior, unchanged)

### Sentinel error matching
- `project.ErrNotFound` is a sentinel; wrap with `errors.Is(err, project.ErrNotFound)` checks, not direct equality
- `CodedError` is a typed value; extract via `errors.As(err, &target)` (Go 1.20+; for Go 1.26+ prefer `errors.AsType[*CodedError](err)` if devbox upgrades — go.mod currently targets the project's pinned version, verify before using)
- Lowercase error messages, no trailing punctuation: `"no devbox.yml found"` ✓, not `"No devbox.yml found."`

### `main.go` error handler
JSON mode → `cmdctx.WriteError(flags, root, err)`; text mode → existing fang default.

### DTO shapes (recap)

- **status**: composite `{project, apps, tools, infra, daemons, deploy, topology, git}`; subcommands return only their section at root: `{"apps": [...]}`.
- **info**: `{title, sections: [{id, title, items: [{type, label, value}]}]}` with `${var}` resolved and auto-blocks expanded.
- **validate**: `{summary: {ok, info, warning, error}, diagnostics: [{severity, scope, file, line, message, hint}]}`.
- **version**: `{version, commit, built_at}`.
- **commands list**: `{commands: [{id, title, group, type, private, params}]}` (flat).
- **commands inspect**: single full definition object incl. `derived_from` for sugar-expanded commands.
- **snapshot list/inspect/current**: reuse existing DTOs.

## What Goes Where

- **Implementation Steps**: foundation, per-command migrations, error wrapping, audit, verification — all in this codebase.
- **Post-Completion**: smoke-test recipes; future Wave 2 follow-ups (mutation commands, MCP).

## Implementation Steps

### Task 1: Foundation — output mode types in cmdctx

**Files:**
- Modify: `internal/cli/cmdctx/flags.go`
- Create: `internal/cli/cmdctx/output.go`
- Create: `internal/cli/cmdctx/output_test.go`

- [x] add `Output string` and `Pretty bool` fields to `RootFlags`
- [x] in new `output.go`: define `errorEnvelope` struct (JSON-tagged: `error.code`, `error.message`, `error.hint`, `error.details`)
- [x] in new `output.go`: define `CodedError` with `Code/Message/Hint/Details/Wrapped`, `Error()`, `Unwrap()`, and constructors `Err(code, msg)`, `ErrWrap(code, err)` with chainable `.WithHint(s)`, `.WithDetail(k, v)`
- [x] in new `output.go`: implement `WriteData[T any](flags *RootFlags, cmd *cobra.Command, data T, renderText func(T) string) error` — JSON mode uses `json.NewEncoder` (no indent by default; `SetIndent("", "  ")` if `flags.Pretty`); text mode uses `fmt.Fprintln(cmd.OutOrStdout(), renderText(data))`
- [x] in new `output.go`: implement `WriteError(flags *RootFlags, cmd *cobra.Command, err error)` — no-op for text mode; for JSON, build envelope via `buildErrorEnvelope` (extract `*CodedError` via `errors.As`; fallback `internal_error`) and write to stderr with `json.NewEncoder`
- [x] in new `output.go`: implement private `buildErrorEnvelope(err error) errorEnvelope`
- [x] write tests for `WriteData` (text vs json vs json+pretty) — table-driven, capture stdout buffer, compare
- [x] write tests for `WriteError` (CodedError extraction, plain-error fallback, text mode no-op, JSON shape valid)
- [x] write `TestCodedError_ErrorsAs` — confirm `errors.As(wrapped, &target)` works through `Unwrap`
- [x] run `go test ./internal/cli/cmdctx/...` — must pass before Task 2

### Task 2: Register --output and --pretty on root, wire side-effects

**Files:**
- Modify: `internal/cli/root.go`
- Modify: `cmd/devbox/main.go`
- Create: `internal/cli/root_output_test.go`

- [x] in `root.go` `initRootCmd`: register `cmd.PersistentFlags().StringVarP(&flags.Output, "output", "o", "text", "output format: text or json")` and `cmd.PersistentFlags().BoolVar(&flags.Pretty, "pretty", false, "pretty-print JSON output (only with --output json)")`
- [x] in `root.go` `PersistentPreRunE`, **ordering is critical**: (1) validate `flags.Output` first — on invalid value return `cmdctx.Err("invalid_output", "unknown output format").WithHint("valid values: text, json")` (exit code 2). (2) then set NO_COLOR / silence flags. (3) then `applyStyles`. Validating last would let lipgloss-styled fang errors leak through before we reject the invalid flag.
- [x] main.go error handler maps `invalid_output` (or any code in a small "usage error" set) to exit 2 instead of 1. Add the mapping to a tiny helper `cmdctx.ExitCodeFor(err)` returning `int`.
- [x] in `root.go` `PersistentPreRunE`: when `flags.Output == "json"`, call `os.Setenv("NO_COLOR", "1")` BEFORE `applyStyles`; set `cmd.Root().SilenceErrors = true` and `SilenceUsage = true`
- [x] add `NewRootCmdWithFlags() (*cobra.Command, *cmdctx.RootFlags)` to `internal/cli/root.go`; refactor body of `NewRootCmd()` to delegate to it (existing 14+ test callers keep working unchanged)
- [x] in `main.go`: call `cli.NewRootCmdWithFlags()` to capture flags; extend `errHandler` to check `flags.Output == "json"` and call `cmdctx.WriteError(flags, root, err)` before returning (skip `fang.DefaultErrorHandler` in JSON mode). **Note**: fang's `errHandler` already swallows `ExitCode()`-bearing errors (e.g. validation failures) BEFORE our JSON check, so `validate --output json` correctly emits diagnostics-as-data on stdout without an envelope on stderr — verify this ordering when wiring.
- [x] write test: invalid `--output bogus` returns an error before any subcommand runs
- [x] write test: `--output json` causes `NO_COLOR` to be set in env after PersistentPreRunE
- [x] write test: `--output json` causes `SilenceErrors` and `SilenceUsage` to be `true`
- [x] run `go test ./internal/cli/...` — must pass before Task 3

### Task 3: Migrate `devbox version`

**Files:**
- Modify: `internal/cli/version/version.go`
- Create: `internal/cli/version/version_test.go` (does NOT exist yet)
- Create: `internal/cli/version/testdata/version.json.golden`

- [x] update `NewCmd` signature to accept `*cmdctx.RootFlags` (it currently takes only `groupID`; check existing call site in `root.go` line 73 and update)
- [x] **the existing handler uses `Run:` (no error). Replace with `RunE:` because `cmdctx.WriteData` returns `error`.** Update the function field name and signature accordingly.
- [x] define DTO `type versionJSON struct { Version, Commit, BuiltAt string }` with JSON tags `version`, `commit`, `built_at`
- [x] in RunE: build DTO from `version.Version`, `version.Commit`, `version.Date`; call `cmdctx.WriteData(rflags, cmd, dto, renderVersionText)`; existing text path becomes the `renderText` closure
- [x] golden test: set `flags.Output = "json"`, run, compare buffer to golden file
- [x] write text-mode regression test (also new, since no test file exists yet) — captures current text output
- [x] run `go test ./internal/cli/version/...` — must pass before Task 4

### Task 4: Migrate `devbox info`

**Files:**
- Modify: `internal/cli/info/info.go` (add data builder here, NOT in `core/ui/`)
- Modify: `internal/cli/info/info_test.go` (exists per discovery)
- Create: `internal/cli/info/testdata/info.json.golden`

**Architectural decision**: the data builder lives in `internal/cli/info/`, NOT `internal/core/ui/`, to preserve CLAUDE.md's "ui returns strings" rule. The cli layer is the seam where data and string-rendering meet.

- [x] define DTOs in `cli/info/info.go`: `type infoJSON struct { Title string; Sections []infoSection }` with `Items: []infoItem{Type, Label, Value}`
- [x] in `cli/info/`: add private `buildInfoData(cfg, cfgVars) infoJSON` that mirrors what `core/ui/info.go::RenderInfo` consumes internally — same `${var}` resolution, same auto-urls/auto-hosts expansion. Do NOT modify `core/ui/info.go`'s public surface; if the existing data-shape function is unexported, duplicate the small extraction logic in cli — CLAUDE.md tolerates duplication over premature abstraction.
- [x] **in JSON mode, skip BrandHeader emission entirely** (it's decorative ASCII art with project tagline — not data the agent needs and would corrupt the JSON stream)
- [x] dispatch via `cmdctx.WriteData(rflags, cmd, data, func(d infoJSON) string { return ui.RenderInfo(cfg, ...) })` — the text-rendering closure invokes the existing `core/ui/info.go::RenderInfo` unchanged
- [x] golden test for `--output json`
- [x] existing text-mode test untouched
- [x] run `go test ./internal/cli/info/...` — must pass before Task 5

### Task 5: Migrate `devbox commands list` and `commands inspect`

**Files:**
- Modify: `internal/cli/command/list.go` (or wherever list lives — verify)
- Modify: `internal/cli/command/inspect.go`
- Create: `internal/cli/command/testdata/list.json.golden`
- Create: `internal/cli/command/testdata/inspect.json.golden`

- [x] verify file paths (look for `printTreeNodes`, `printInspect` in `internal/cli/command/`)
- [x] define `commandsListJSON` and `commandInspectJSON` DTOs from the existing `CommandDef` registry types
- [x] for `list`: emit flat array (NOT the tree — agents prefer flat). Include `id, title, group, type, private` (respect `--all` flag for private inclusion), and `params: []paramJSON{Name, Type, Required, Default, Description}`
- [x] for `inspect`: include all fields shown in text, plus `derived_from` field when `DerivedFromDaemon` (or analogous sugar source field) is non-zero — surface the `Source*Spec` shadow struct in JSON
- [x] dispatch via `cmdctx.WriteData`
- [x] golden tests for both
- [x] run `go test ./internal/cli/command/...` — must pass before Task 6

### Task 6: Migrate `devbox validate` and all 9 subdomains

**Files:**
- Modify: `internal/cli/validate/*.go` (composite + per-domain subcommands)
- Modify: `internal/core/ui/diagnostics.go` (or wherever `RenderDiagnosticsTable` lives)
- Create: `internal/cli/validate/testdata/validate.json.golden`
- Create: `internal/cli/validate/testdata/validate_config.json.golden` (for one subdomain as smoke)

- [x] define `validateJSON` DTO: `{ Summary: {Ok, Info, Warning, Error int}, Diagnostics: []diagnosticJSON }`; `diagnosticJSON` mirrors `validate.Diagnostic` with snake_case JSON tags (`severity`, `scope`, `file`, `line`, `message`, `hint`)
- [x] composite path: collect all diagnostics across domains as before, emit single JSON
- [x] per-subdomain path: emit only diagnostics for that subdomain (same DTO shape, filtered list)
- [x] preserve exit code logic: 0 for ok/info, 1 for error or (warning AND `--strict`) — JSON shape does NOT replace exit codes
- [x] dispatch via `cmdctx.WriteData(rflags, cmd, data, renderDiagnosticsText)` — note that current renderer uses `ui.RenderDiagnosticsTable` + `ui.FormatSummary`; wrap them in a closure
- [x] when validate fails with error severity in JSON mode, DON'T emit a JSON error envelope — the diagnostics ARE the data, exit code conveys severity (see brainstorm: "diagnostic commands always return data on stdout, exit code reflects severity")
- [x] golden test for composite validate
- [x] golden test for one subdomain (`validate config`)
- [x] run `go test ./internal/cli/validate/... ./internal/core/validate/...` — must pass before Task 7

### Task 7: Migrate `devbox status` and 7 subcommands + TUI auto-disable

**Files:**
- Modify: `internal/cli/status/status.go` (root composite, plus TUI gating)
- Modify: `internal/cli/status/apps.go`, `tools.go`, `infra.go`, `daemons.go`, `deploy.go`, `topology.go`, `git.go` (verify exact filenames; `status` package has them per CLAUDE.md)
- Create: `internal/cli/status/testdata/status.json.golden` (composite)
- Create: `internal/cli/status/testdata/status_apps.json.golden` (one subsection as smoke)
- Create: `internal/cli/status/testdata/status_deploy_name.json.golden` (positional-arg form)
- Possibly modify: `internal/core/project/stack/*.go` if data types need DTO-tagging

**TUI disable mechanism** — verified: TUI selection is gated by the local helper `shouldUseTUI(noTUI, noFlags)` inside `RunE` (status.go:166), NOT a persistent flag. Add `if rflags.Output == "json" { return false }` at the top of `shouldUseTUI`, or as an early return in `RunE` before the helper is called. Document the chosen seam clearly so future status work doesn't accidentally regress.

**Scope addition**: `devbox status deploy [<service>]` takes an optional positional argument (see `Example:` at status.go:195) — when the arg is present, the DTO is the deploy section filtered to that service. Add a separate golden file for this form.

- [x] in `status.go`: extend `shouldUseTUI` (or RunE entry) to force-off TUI when `rflags.Output == "json"` regardless of user setting
- [x] define top-level DTO: `statusJSON struct { Project *projectJSON; Apps []appJSON `json:",omitempty"`; Tools []toolJSON `json:",omitempty"`; Infra *infraJSON `json:",omitempty"`; Daemons []daemonJSON `json:",omitempty"`; Deploy *deployJSON `json:",omitempty"`; Topology *topologyJSON `json:",omitempty"`; Git *gitJSON `json:",omitempty"` }`
- [x] `appJSON` includes the richer fields per brainstorm: `Name, Status, Health, Image, ContainerID, ContainerName, Ports []portJSON{Host, Container, Protocol}, Hosts []string, UptimeSeconds int64, RestartCount int, StartedAt string`
- [x] tools/infra/daemons/deploy/topology/git DTOs: include text-equivalent fields + machine-readable extras (hashes, raw timestamps, byte counts)
- [x] in subcommands (`status apps` etc.): emit ONLY the relevant section wrapped at root: `{"apps": [...]}` — so jq `.apps[]` works identically for composite and per-section
- [x] `status deploy <name>` (positional arg form): emit `{"deploy": {...service-filtered-data...}}` matching the same wrapping convention
- [x] data collection logic in `core/project/stack` already returns rich data; do NOT refactor stack — wrap its outputs in DTOs at the cli layer
- [x] `--no-X` flags omit fields (omitempty makes them absent, NOT null)
- [x] dispatch via `cmdctx.WriteData`
- [x] **Golden file time normalization** (pick ONE approach for the whole status package — don't mix):
      **Chosen: regex post-process** — before diffing buffer vs golden, run `regexp.MustCompile(`"(started_at|deployed_at)":\s*"[^"]+"`).ReplaceAllString(buf, `"$1":"<TS>"`)` and similarly for `uptime_seconds`. Simpler than test seams, keeps production code untouched. Document in a `// golden-normalize: ...` comment at top of each affected test file.
- [x] golden test for composite
- [x] golden test for `status apps`
- [x] golden test for `status deploy <name>` positional form
- [x] add test verifying `--output json` forces TUI off even when stdout is a TTY
- [x] run `go test ./internal/cli/status/...` — must pass before Task 8

### Task 8: Migrate snapshot list/inspect/current — delete local --json

**Note on `snapshot inspect`**: the existing handler accepts both a snapshot name (directory branch) AND a tar/tar.gz path (`loadInspectManifest` handles `.tar.gz`/`.tgz`). The DTO and golden test must cover both branches OR be explicitly scoped to one branch with a documented limitation.


**Files:**
- Modify: `internal/cli/snapshot/snapshot.go` (contains list/inspect handlers)
- Modify: existing snapshot tests
- Create: `internal/cli/snapshot/testdata/list.json.golden`
- Create: `internal/cli/snapshot/testdata/inspect.json.golden`
- Create: `internal/cli/snapshot/testdata/current.json.golden`

- [x] in `snapshot.go` `newSnapshotListCmd`: delete `--json` local flag and `jsonOut bool` variable; replace `runSnapshotList` JSON branch with dispatch on `flags.Output == "json"` via `cmdctx.WriteData`
- [x] in `snapshot.go` `newSnapshotInspectCmd`: same treatment — delete `--json` local flag; preserve the existing inline JSON struct shape (`Source`, `Manifest`, `CurrentConfigHash`, `ConfigHashDiverged`, `ServicesDiff`) as the DTO
- [x] in `newSnapshotCurrentCmd`: add JSON support — DTO is `{name, dir, created_at, description, variant, total_size}` (or `null` body if no current snapshot); text behavior unchanged
- [x] existing `snapshotListJSONEntry` struct stays as-is (rename to lowercase `snapshotListEntry` if you want consistency with new DTOs — optional)
- [x] update existing tests that exercised `--json` to use `flags.Output = "json"` instead
- [x] golden tests for list/inspect/current JSON shapes
- [x] run `go test ./internal/cli/snapshot/...` — must pass before Task 9

### Task 9: Wrap user-facing errors with CodedError

**Files:**
- Modify (search for error sites): `internal/core/project/project/locate.go`, `resolve.go`; `internal/shared/docker/*.go`; `internal/shared/lock/project.go`; `internal/core/project/services/*.go`; `internal/core/workflow/snapshot/*.go`; `internal/core/usercommands/registry/*.go`
- Modify: `internal/cli/cmdctx/output_test.go` (extend tests)

Priority error sites to wrap (10-15, ordered by user-facing frequency):
- [ ] `project.ErrNotFound` returns from `project.Locate`/`Resolve` → wrap with `cmdctx.ErrWrap("project_not_found", err).WithHint("run from a Devbox project directory or pass --config").WithDetail("searched_path", path)`
- [ ] schema validation error from `project.Resolve` → `project_invalid_config` with offending field detail
- [ ] docker daemon unreachable in `shared/docker` probes → `docker_unavailable` with hint `start Docker Desktop`
- [ ] `docker compose` plugin missing → `docker_compose_missing`
- [ ] `shared/lock` busy lock → `lock_held` with detail `holder_pid`
- [ ] `shared/lock` stale lock detection → `lock_stale`
- [ ] unknown service name in `services.LoadServiceFolder` lookup → `service_unknown` with detail `name`
- [ ] snapshot not found in `workflow/snapshot.Load` → `snapshot_not_found`
- [ ] snapshot manifest corrupt → `snapshot_corrupt`
- [ ] unknown command id in `usercommands/registry.Get` → `command_unknown`
- [ ] command parameter validation failure → `command_invalid_params` with detail `param_name`
- [ ] extend tests in `output_test.go` to cover envelope fields for at least 3 of the wrapped sites (project_not_found, docker_unavailable, command_unknown) via fake errors constructed in test
- [ ] run `go test ./...` — must pass before Task 10

### Task 10: Audit hidden `fmt.Print*` in core/

**Files:**
- Audit: `internal/core/**/*.go`
- Modify: any direct-print sites found

- [ ] run `grep -rnE 'fmt\.(Print|Fprint)f?' internal/core/` and inspect each hit (note: `-E` for ERE; bare `\(...\)` BRE form returns nothing on macOS BSD grep)
- [ ] for any `fmt.Print*(...)` writing to `os.Stdout`/`os.Stderr` directly (rather than via passed-in `io.Writer`), refactor to accept a writer parameter and let the caller in `internal/cli` decide
- [ ] for `fmt.Fprint*(cmd.OutOrStdout(), ...)` paths in core: these are already correct, no change
- [ ] for `slog.*` warnings: these go to a logger sink configured by cli layer (not stdout), no change
- [ ] document in plan if no hits found
- [ ] run `make test` — must pass before Task 11

### Task 11: Update `runRoot` summary for JSON mode

**Files:**
- Modify: `internal/cli/root.go` `runRoot`

- [ ] in `runRoot`: when `flags.Output == "json"`, skip brand header + summary + pending banner AND `cmd.Help()` (which would dump human help text into the JSON stream); emit a JSON DTO instead: `{project: {name, version, root}, deploy_summary: {...} | null, pending: {...} | null}` and `return nil` early (do NOT fall through to `return cmd.Help()`)
- [ ] when no project found AND `--output json`: emit `{project: null}` and return early (NOT `cmd.Help()`)
- [ ] keep text behavior identical when `flags.Output != "json"`
- [ ] add golden test for `devbox --output json` (no subcommand) — composite root summary
- [ ] run `go test ./internal/cli/...` — must pass before Task 12

### Task 12: Verify acceptance criteria

- [ ] `devbox status --output json` returns single composite JSON of all sections (compact, single line)
- [ ] `devbox status apps --output json` returns just `{"apps": [...]}`
- [ ] `devbox status --output json --pretty` returns indented JSON
- [ ] `devbox status` (no flag) — current TUI/text behavior unchanged (regression check)
- [ ] `devbox snapshot list --json` (old flag) is GONE (cobra reports "unknown flag")
- [ ] `devbox snapshot list --output json` works identically to old `--json`
- [ ] error in JSON mode: stderr contains valid JSON envelope, exit code non-zero
- [ ] `devbox validate --output json` returns diagnostics array; exit code reflects severity per current rules
- [ ] `NO_COLOR=1` is set when `--output json`; no ANSI sequences appear in JSON
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] manually run each migrated command with `--output json` and `--output json --pretty` and visually inspect — record any oddities in plan as ➕ items

### Task 13: Update documentation

**Files:**
- Modify: `docs/reference/cli/` (auto-regenerated by `make build` — verify)
- Modify: `CLAUDE.md` (or `AGENTS.md`) — append Key Pattern about `--output json` + `cmdctx.WriteData` + `CodedError`
- Move: this plan file to `docs/plans/completed/`

- [ ] run `make build` to regenerate embedded docs (includes auto-generated CLI reference)
- [ ] add a Key Pattern entry to `AGENTS.md` describing the JSON mode contract: global `--output`/`--pretty`, naked-data convention, error envelope shape, `CodedError` usage pattern, `cmdctx.WriteData` helper
- [ ] verify `git diff internal/core/docs/content_hashes_gen.go` shows the regenerated hashes (per CLAUDE.md CI guard)
- [ ] move plan: `mkdir -p docs/plans/completed && mv docs/plans/2026-05-29-json-state-output.md docs/plans/completed/`

## Post-Completion

**Manual verification** (not gated, but recommended before merge):
- Run `devbox status --output json` on a real project with 5+ services running, pipe through `jq`, sanity-check shape and field naming.
- Run `devbox --output json` (no subcommand) inside and outside a project.
- Run `devbox validate --output json` on a project with intentionally broken `devbox.yml` (e.g. unknown field) — verify diagnostics array and exit code 1.
- Run `devbox snapshot list --output json --pretty` and confirm the migrated shape matches the old `--json` output exactly (modulo whitespace).

**Future Wave 2 follow-ups (out of scope here, captured for context):**
- Mutation commands (`run`, `stop`, `restart`, `reset`, `deploy run`) → NDJSON event stream + final summary in JSON.
- `devbox logs <service>` command (separate Wave 1 plan).
- `llms.txt` generator (separate Wave 1 plan).
- MCP server v1 with read-only tools, reusing the DTOs introduced here.
- `agent_safe` field on `CommandDef`.
- JSON Schema for `devbox.yml`, `services/*.yml`, etc.

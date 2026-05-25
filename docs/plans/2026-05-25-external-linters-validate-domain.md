# External linters domain for `devbox validate`

## Overview

Integrate well-known external linters (shellcheck, hadolint, plus a generic adapter for anything else) into `devbox validate` as a new `linters.*` diagnostic domain. Linters share the existing `Validator` / `Diagnostic` / `Registry` framework and render through the same `RenderDiagnosticsTable`.

**Out of scope (v1):**
- Running linters in preflight (preflight answers "can we run?", not "is the code clean?").
- Auto-fix, caching, plugin system, arbitrary output-format parsers.
- Arbitrary user-defined checks beyond the generic adapter — those remain `type: command` checks.

## Context (from discovery)

**Files / packages touched:**
- `internal/config/validate.go` — extend `ValidateConfig` with `Linters`; add strict-decode for new top-level `linters:` map.
- `internal/validate/linters/` *(new)* — adapters, runtime, `All(...)` assembler.
- `internal/command/validate.go:406` — `buildRegistry` registers a new `vallinters.All(...)`.
- `docs/reference/config/validate.md` — document `linters:` schema, autodetect rules, scope syntax.
- `docs/internals/packages.md` — add the new package to the Validation grouping.

**Architecture map (from explore):**
- `Validator` interface: `ID() string`, `Domain() string`, `Run(Context) []Diagnostic`.
- `Diagnostic` fields used by table renderer: `Severity, Domain, Target, File, Line, Message, Hint` (table also has `Column`-less layout — line only).
- `Registry.Run(ctx, scope...)` filters by `MatchScope(domain, id, scope)` per *registered* validator. Linters use the `GroupValidator` interface added in Task 8: `All()` returns a single group registered in the Registry; the group exposes per-linter children via `Children() []Validator`, and `Registry.Run` expands them during scope matching so `["linters"]` and `["linters", "shellcheck"]` both work. The group's `RunGroup` then runs the matching subset concurrently. A `linters [id]` Cobra subcommand (Task 7) carries the scope into runValidate, mirroring `checks [id]` — the root `validate` cmd is `cobra.NoArgs` and cannot accept scope as a positional arg.
- `validate.Context` already carries `ValidateCfg`, `ProjectRoot`, `Ctx`. No new fields needed.
- Severity-gated exit: `validate.ExitCode(summary, strict)` — errors → 1, warnings + `--strict` → 1. Reused as-is.
- Parallel pattern: project uses `golang.org/x/sync/errgroup` in `internal/pipeline/executor.go:860`, but Task 8 below deliberately does **NOT** use `errgroup.WithContext` for the linters group — one linter's failure must not cancel siblings. Linters use `sync.WaitGroup` + a buffered-channel semaphore + per-goroutine `recover()` instead. The errgroup pattern remains the right choice elsewhere in the codebase; the linters group has a different correctness requirement.
- No existing file-walk-with-extension helper — implement inside `internal/validate/linters/walk.go`.

**Patterns referenced (per CLAUDE.md):**
- Strict YAML loader (matches `LoadValidateConfig` checks-block: `yaml.Decoder.KnownFields(true)`).
- Silent skip when feature opts out via `enabled: false` ([[feedback_render_explicit_optout_silent]]).
- Concise `Diagnostic.Hint` with `\n`-split for long ones ([[feedback_validate_diagnostic_hints]]).

## Development Approach

- **Testing approach**: Regular (code first, then tests inside the same task).
- Complete each task fully before moving to the next.
- Make small, focused changes; each task touches one logical unit.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task**.
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run `make test` and `make lint` before declaring a task done.

## Testing Strategy

- **Unit tests** for every task — table-driven where natural (loader, scope filter, parser).
- **Integration test** for the adapter runtime using a **Go-built fake binary** (`cmd/fake-linter/main.go`, compiled into `t.TempDir()` from `TestMain` via `go build`; behavior selected via env vars like `FAKE_LINTER_MODE=clean|findings|crash|hang|huge-output`). Cross-platform — avoids the `.sh` portability problem. `t.Setenv("PATH", tmpDir)` redirects `exec.LookPath` to the fake.
- **Parallelism test is barrier-based, not timing-based** — two fakes coordinate through a `sync.Cond` / channel barrier to prove concurrent execution without flaky wall-clock assertions.
- **Optional real-binary smoke test** behind `testing.Short()` guard and `_, err := exec.LookPath("shellcheck"); if err != nil { t.Skip(...) }`. Runs against real shellcheck/hadolint when present (CI image), skipped silently otherwise.
- No e2e UI tests — this is CLI-only output that flows through `RenderDiagnosticsTable`.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (checkboxes): in-repo code, tests, docs.
- **Post-Completion** (no checkboxes): manual smoke tests with real binaries, README/changelog updates for end users.

## Implementation Steps

### Task 1: Extend `ValidateConfig` with `linters:` schema (loader only)
- [x] add `LinterEntry` struct in `internal/config/validate.go` with fields: `ID, Type (builtin|generic), Enabled (*bool), Bin, Paths, Extensions, Filenames, Flags, Severity (*diag.Severity — nil means no clamp), SourceLine`. Pointer for `Severity` so "not in YAML" and "explicitly set" stay distinguishable; this also avoids the zero-value trap (`SeverityUnknown = 0` would otherwise silently double as a sentinel).
- [x] extend `ValidateConfig` with `Linters []LinterEntry`.
- [x] extend `rawValidateConfig` with `Linters map[string]rawLinterEntry` (map keyed by adapter ID; `id` comes from the key, not a field). **`rawLinterEntry.Severity` MUST be `*string`** (not plain `string`) so the loader can distinguish "field absent" from "field present and explicitly empty" — required for the "severity nil vs explicit round-trip" test in Task 1 and for the pointer semantics on `LinterEntry.Severity`. Plain `string` collapses both cases to `""` and the round-trip test cannot be satisfied. Same `*bool` treatment is already applied to `Enabled`.
- [x] in `LoadValidateConfig`, decode the `linters:` map strictly: reject unknown fields, unknown `type:` values (default `"builtin"`), unknown severity strings; capture per-entry `SourceLine` from YAML node traversal (mirror the `Checks` traversal). Severity parsing rule: `Severity == nil` → `LinterEntry.Severity = nil` (no clamp); `*Severity == ""` → reject with a clear error ("`severity:` must be one of error|warning|info, not empty"); `*Severity` set to one of `error|warning|info` → `LinterEntry.Severity = &parsed`. **`ok` is NOT an allowed severity for clamp** — clamping findings to OK would effectively mute them (OK rows are filtered by `--quiet` and read as "passed" in the table), which is a confusing way to disable a linter. Users who want to silence a linter use `enabled: false`. Unknown token → reject.
- [x] reject duplicate adapter IDs across `linters:` block (map keys are inherently unique, but defend against case-collision if any). (Non-empty path validation is covered by the load-time path validation bullet below.)
- [x] **load-time path validation** — each `paths:` entry must be non-empty (reject `""`), relative (reject absolute), and satisfy `filepath.IsLocal` (no `..` traversal). `"."` is the only allowed reference to baseDir-itself (used by hadolint default). Each entry passed through `filepath.Clean` after validation. Each `filenames:` entry must be non-empty and contain no path separators. Surface a clear per-line error citing `SourceLine`.
- [x] **load-time extension validation** — each `extensions:` entry must start with `.` and contain no path separators. Reject `"sh"` (must be `".sh"`) at load time so users hit this immediately, not silently match nothing later.
- [x] **load-time `bin:` validation** — per the trust-model decision in the Open Questions section below, restrict `bin:` to a bare command name (no path separators). Reject anything containing `/` or `\`. Resolution happens via PATH at runtime; absolute or relative paths are forbidden.
- [x] write loader tests in `internal/config/validate_test.go`: valid linters block, unknown field, unknown type, unknown severity, missing bin allowed (defaults to ID), `enabled: false` accepted, source-line capture for diagnostics, **path with `..` rejected**, **absolute path rejected**, **empty-string path `""` rejected**, **`paths: ["."]` accepted (root-equality, used by hadolint default)**, **extension without leading `.` rejected**, **`bin: /usr/bin/shellcheck` rejected**, **severity round-trip cases**: (a) `severity:` field absent → `LinterEntry.Severity == nil`, (b) `severity: warning` → non-nil pointer to SeverityWarning, (c) `severity: error` and `severity: info` also accepted, (d) `severity: ""` (explicit empty) → load error, (e) `severity: ok` → load error (not allowed; use `enabled: false` to disable), (f) `severity: bogus` → load error.
- [x] run `go test ./internal/config/...` and `make lint` — must pass before Task 2. (`make test` runs the full suite per the Makefile; for focused work invoke `go test` directly.)

### Task 2: Adapter interface + generic adapter + diagnostic helpers
- [x] create package `internal/validate/linters/` with `linters.go`:
  - `Adapter` interface:
    - `ID() string`
    - `DefaultBin() string`
    - `DefaultPaths() []string`
    - `DefaultExtensions() []string`
    - `DefaultFilenames() []string` — literal filenames matched alongside extensions (e.g., `["Dockerfile"]`). Designed in from day one so hadolint doesn't retrofit it.
    - `ReservedFlags() []string` — list of CLI flag tokens the user is forbidden from passing in `flags:`. Built-in adapters reserve output-format flags (`--format`, `-f`) because we lock the format to a value the parser depends on. Generic adapter returns nil (no reservations).

      **Match policy (must cover all four argv forms a flag can take):**
      1. exact match: `--format`, `-f`
      2. long with `=`: `--format=gcc` → split on first `=`, compare prefix to reserved set
      3. short with attached value: `-fgcc` → for any reserved short flag (`len == 2 && starts with -`), match if userFlag starts with the reserved token. Critical: shellcheck accepts `-fgcc` and emits GCC output, completely bypassing a naive equality/prefix-before-`=` check. The `-fgcc` form is the one most likely to slip through in practice.
      4. value-as-next-arg: `[..., "-f", "gcc", ...]` — the `-f` token itself matches in step 1; the following positional `gcc` is not separately checked (rejecting the flag itself suffices to refuse the construct).

      Implementation sketch:
      ```go
      func isReserved(flag string, reserved []string) bool {
          // Strip the long-form "=value" suffix once.
          token := flag
          if i := strings.IndexByte(flag, '='); i >= 0 {
              token = flag[:i]
          }
          for _, r := range reserved {
              if token == r {
                  return true // exact or long-with-=
              }
              // Short-flag attached value: reserved="-f", flag="-fgcc"
              if len(r) == 2 && strings.HasPrefix(r, "-") && !strings.HasPrefix(r, "--") &&
                  strings.HasPrefix(flag, r) && len(flag) > 2 {
                  return true
              }
          }
          return false
      }
      ```
    - `BuildArgs(files []string, userFlags []string) []string`
    - `ParseOutput(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error)`
  - helpers `fail/warn/info/ok(id, msg, hint)` that stamp `Domain: "linters"` (mirror `env.fail`).
  - helper `validateUserFlags(adapter Adapter, userFlags []string) error` — checks each user flag against the adapter's `ReservedFlags()` set. Called by `All()` (Task 6) when binding an entry to an adapter; on failure, `All()` synthesizes an error validator instead of registering the linter, so a bad config short-circuits before any subprocess runs.
- [x] create `generic.go`: generic adapter that runs `bin <flags> <files...>` and turns non-zero exit + stdout/stderr into one error-severity diagnostic (no per-line parsing). Used when `type: generic`. `DefaultFilenames` returns nil. `ReservedFlags` returns nil — the generic adapter doesn't parse output, so the user owns the entire flag surface.
- [x] create `walk.go`: helper `collectFiles(baseDir string, paths []string, exts []string, filenames []string, pathsAreDefaults bool) (files []string, missing []string, err error)`:
  - recursive `filepath.WalkDir` rooted at each `paths` entry resolved under `baseDir`.
  - explicit file paths (entries that resolve to a regular file) bypass extension/filename filters.
  - **symlinks are explicitly skipped** — check `d.Type()&fs.ModeSymlink != 0` and `return nil` for files, `return filepath.SkipDir` for dirs. `WalkDir` does not follow symlinks but it does return them as entries; without this check a symlink pointing outside `baseDir` would slip past containment.
  - **hardcoded skip list**: `.git`. Always skipped regardless of adapter. (Adapter-specific noise dirs like `node_modules`/`vendor` are left to the user to express via narrower `paths:`; only `.git` is universal enough to hardcode.)
  - file matches if extension is in `exts` OR basename is in `filenames`.
  - **path resolution + containment**: normalize each entry via `cleanEntry := filepath.Clean(entry)`. If `cleanEntry == "."`, treat as `baseDir` itself (root-equality case — `pathsafe.ContainedRel(baseDir, baseDir)` rejects `"."` by design at `pathsafe.go:63`, so we short-circuit before calling it). For all other entries, resolve to `filepath.Join(baseDir, cleanEntry)` and validate containment via `pathsafe.ContainedRel(baseDir, absEntry)` (defense in depth; loader already rejected `""`, `..`, and absolute paths in Task 1, but ContainedRel catches anything the loader missed and prevents drift if rules diverge). This matters because `hadolint`'s `DefaultPaths = ["."]` — without the root-equality case, hadolint would fail on every project. Empty-string entries cannot reach this code (loader rejects them); no need for `cleanEntry == ""` branch.
  - **missing-path semantics depend on `pathsAreDefaults`**: when paths came from adapter defaults (common case: `shellcheck` defaults to `["devbox/scripts", "scripts"]` but the project has neither), absent entries are silently dropped — no error, no diagnostic, just an empty result. When paths came from the user's explicit `paths:` config, absent entries are appended to `missing` so the runtime can emit a Warning ("`paths: nonexistent` does not exist"). Distinguishes "your defaults didn't match this project" from "you asked for X and X is gone."
- [x] write `linters_test.go`: generic adapter parse behavior (exit 0 → no diags; exit 1 → one error with combined output as Message).
- [x] write `walk_test.go`: table-driven — extension filter, filename filter, both together, explicit file bypass, **`paths: ["."]` walks baseDir without error (hadolint's default)**, **missing path with `pathsAreDefaults=true` returns empty silently**, **missing path with `pathsAreDefaults=false` returns it in the `missing` slice**, path traversal rejected, **symlink to file skipped**, **symlink to dir skipped**, **`.git` always skipped**. (Empty-string paths are not tested here — loader rejects them in Task 1, so they can't reach `collectFiles`.)
- [x] run `go test ./internal/validate/linters/...` — must pass before Task 3.

### Task 3: shellcheck built-in adapter
- [x] create `shellcheck.go`: implements `Adapter`. `DefaultBin = "shellcheck"`, `DefaultPaths = ["devbox/scripts", "scripts"]`, `DefaultExtensions = [".sh", ".bash"]`, `DefaultFilenames = nil`, `ReservedFlags = []string{"--format", "-f"}` (locked because output format is parser-load-bearing — we depend on JSON shape).
- [x] `BuildArgs`: order is `[--format=json, userFlags..., --, files...]`. **Forced format first**, defense-in-depth backstop is `ReservedFlags` which the loader (Task 6) enforces — by the time `BuildArgs` runs, user flags cannot contain `--format`/`-f`. (Position-based override-prevention is unreliable across shellcheck versions; local verification showed `--format=gcc --format=json` still emits GCC output in some builds. Rejecting reserved flags at load is the contract; argument order is a clarity choice, not a safety mechanism.) `--` separator prevents filenames starting with `-` from being treated as flags. Use `json` (single JSON array — one `json.Unmarshal` into `[]shellcheckComment`), NOT `json1` (NDJSON streaming — harder to parse, no benefit at our scale).
- [x] `ParseOutput`: decode JSON; map each `comment` to `Diagnostic{File, Line, Severity, Message: "<message> (SC<code>)", Domain: "linters", Target: "shellcheck"}`. Severity map: `error→Error, warning→Warning, info→Info, style→Info`.
- [x] non-zero exit + empty/invalid JSON → one error diagnostic with stderr as message (signal of a shellcheck-internal failure). Non-zero exit + valid JSON with findings → return the parsed findings (shellcheck exits non-zero whenever it finds something — that's normal, not an error).
- [x] write `shellcheck_test.go`: table-driven over real shellcheck JSON fixtures in `testdata/shellcheck/` (clean, multiple findings, exit=1+findings is normal, exit=2+empty-JSON+stderr-message is an internal failure). No external binary needed at test time — only ParseOutput is under test. **Also test the reservation contract — every argv form a flag can take**: `ReservedFlags()` contains `--format` and `-f`; `validateUserFlags` rejects each of `--format=gcc`, `--format gcc` (two separate slice entries `["--format", "gcc"]`), `-f tty` (two entries), and **`-fgcc` (short flag with attached value, no space — the dangerous case shellcheck accepts and that a naive matcher misses)** and `-fjson`; benign flags like `--severity=warning`, `--shell=bash`, `-x` pass.
- [x] **real-binary smoke test** (`shellcheck_real_test.go`, gated by `_, err := exec.LookPath("shellcheck"); if err != nil { t.Skip(...) }` and `testing.Short()`): construct adapter + `BuildArgs`, exec real shellcheck against a fixture script, assert the captured stdout is valid JSON. Verifies our format-locking assumption against the actually-installed binary; catches regressions if shellcheck ever changes flag-precedence behavior.
- [x] run tests.

### Task 4: hadolint built-in adapter
- [x] create `hadolint.go`: `DefaultBin = "hadolint"`, `DefaultPaths = ["."]`, `DefaultFilenames = ["Dockerfile"]`, `DefaultExtensions = [".dockerfile"]` (so both bare `Dockerfile` and `service.dockerfile`-style files are picked up — uses the `Filenames` field added in Task 2), `ReservedFlags = []string{"-f", "--format"}` (locked because hadolint's output format is parser-load-bearing).
- [x] `BuildArgs`: order is `[-f, json, userFlags..., --, files...]`. Forced format first; reserved-flag rejection at load time (Task 6) is the safety contract. `--` separator guards against filenames starting with `-`.
- [x] `ParseOutput`: decode JSON array; map each item to `Diagnostic{File, Line, Severity, Message: "<message> (<code>)", Target: "hadolint"}`. Severity map: `error→Error, warning→Warning, info|style→Info`. Non-zero exit + valid JSON is normal (hadolint exits non-zero whenever it finds something); non-zero exit + invalid JSON → one error diagnostic with stderr.
- [x] write `hadolint_test.go` with JSON fixtures in `testdata/hadolint/` (clean, multiple findings, parse-error stderr). **Also test reservation contract — every argv form**: `ReservedFlags()` contains `-f` and `--format`; reject `--format=gcc`, `["--format", "gcc"]`, `["-f", "tty"]`, **`-ftty` (short-attached)**, `-fjson`; benign flags like `--no-color`, `--ignore=DL3008` pass.
- [x] **real-binary smoke test** (`hadolint_real_test.go`, same `LookPath`+`testing.Short()` gate as shellcheck): exec real hadolint against a fixture Dockerfile, assert stdout parses as JSON. Catches future hadolint flag-precedence drift.
- [x] run tests.

### Task 5: Runtime (per-linter validator) + autodetect + severity clamp + bounds
- [x] create `runtime.go`: `linterValidator` struct implementing `validate.Validator` — holds entry, adapter, baseDir.
- [x] **package-level vars** at the top of `runtime.go` (not `const` — tests need to override):
  - `var DefaultLinterTimeout = 5 * time.Minute` — per-linter execution cap. Tests override via a small `withTestTimeout(t, d)` helper that swaps the value and registers `t.Cleanup` to restore.
  - `var MaxLinterOutputBytes int64 = 50 << 20` (50 MB) — combined stdout+stderr cap. Same test-override pattern.
  - Both exported (capitalized) so test helpers in the same package can manipulate them without unsafe gymnastics; not part of the public API for users.
- [x] `Run(ctx)` flow — maintain two separate diagnostic buckets to keep severity clamp from muting operational signals:
  - `operationalDiags []Diagnostic` — runtime-emitted diagnostics about the linter invocation itself (missing bin, missing user-configured path, timeout, output truncation, parser failure, panic). Never clamped.
  - `findings []Diagnostic` — adapter-emitted diagnostics about the user's code. Clamped by `entry.Severity` if set.
  - return `append(operationalDiags, findings...)` at the end.

  Steps:
  1. resolve `enabled` (per autodetect rules from spec).
  2. `exec.LookPath(bin)`. If autodetected default bin missing → silent skip (return nil). If explicit `bin:` configured and missing → append Warning to `operationalDiags` and return.
  3. `collectFiles(baseDir, paths, exts, filenames, pathsAreDefaults)` where `pathsAreDefaults := len(entry.Paths) == 0`. If `missing` is non-empty, append one Warning per missing entry to `operationalDiags` (only happens when user explicitly listed them). Empty file result → return operationalDiags (likely empty, so silent skip).
  4. **wrap context with timeout (nil-safe parent)**: `parent := ctx.Ctx; if parent == nil { parent = context.Background() }`, then `runCtx, cancel := context.WithTimeout(parent, DefaultLinterTimeout); defer cancel()`. Then `exec.CommandContext(runCtx, bin, adapter.BuildArgs(files, entry.Flags)...)`. The nil fallback matches the established pattern in `internal/validate/env/docker.go` and `internal/validate/checks/loader.go` — `validate.Context.Ctx` is documented at `validate.go:31` as nullable ("Nil is safe; runners fall back to context.Background()"). Calling `context.WithTimeout(nil, …)` panics; direct package use and unit tests routinely pass zero-value Context.
  5. **bound output**: capture stdout/stderr into per-stream `bytes.Buffer`s wrapped via a small `boundedWriter` that drops bytes past `MaxLinterOutputBytes / 2` per stream and flips a `truncated` flag. After `Wait`: if `truncated`, append Warning to `operationalDiags` (parser still runs on what we have).
  6. **detect deadline**: if `runCtx.Err() == context.DeadlineExceeded`, append Error to `operationalDiags` ("`<id>` timed out after 5m") and return — do not try to parse partial output.
  7. `findings, parseErr := adapter.ParseOutput(stdout, stderr, exitCode)`.
  8. **adapter parse error → Warning (operational, not clamped)**: if `parseErr != nil`, append one Warning to `operationalDiags` (`"<id>: failed to parse output: <err>"`). Whatever findings the adapter produced before the failure are still kept in `findings`. Never propagate `parseErr` to the caller.
  9. **apply severity clamp to findings only**: `if entry.Severity != nil { for i := range findings { if findings[i].Severity > *entry.Severity { findings[i].Severity = *entry.Severity } } }`. Pointer check is unambiguous; no zero-value trap. Clamp does NOT touch `operationalDiags` — `severity: info` must not be able to silence a timeout Error or a panic.
  10. stamp `Target` and `Domain` defensively on both buckets (in case adapter omitted them).
- [x] write `runtime_test.go`: use a Go-based fake-binary helper (`go build` a tiny `cmd/fake-linter/main.go` into `t.TempDir()` in `TestMain`; control behavior via `FAKE_LINTER_MODE=clean|findings|crash|hang|huge-output` env). Cross-platform; avoids the `.sh` portability problem. Tests: success path, non-zero+findings, missing-bin silent skip, explicit-bin-missing warning, **timeout fires and emits Error** (test sets `DefaultLinterTimeout = 50*time.Millisecond` and fake sleeps 5s — deterministic, completes in ms), **output truncation emits Warning** (test sets `MaxLinterOutputBytes = 1024` and fake emits 10 KB), **adapter parse error becomes Warning**, **severity clamp downgrades adapter Error → Warning**, **severity clamp does NOT downgrade operational diagnostics** (e.g., `severity: info` set, but a forced timeout still emits Error), **user-configured missing path emits Warning**.
- [x] PATH manipulation: tests use `t.Setenv("PATH", tmpDir)` to control `exec.LookPath` resolution deterministically.
- [x] run `go test ./internal/validate/linters/...`.

### Task 6: `All(...)` assembler + scope wiring
- [x] create `all.go`: `func All(validateCfg *config.ValidateConfig, validateLoadErr error, baseDir string) []validate.Validator`. For each `LinterEntry`: look up built-in `Adapter` by ID; if not found AND `type: generic` → use generic adapter; if not found AND `type: builtin` (or default) → return a synthetic error validator that emits "unknown built-in linter: <id>" (matches `checks` domain pattern for unknown types).
- [x] **`validateLoadErr` handling (CRITICAL — distinguishes "missing file" from "broken file")**: corrupt-config short-circuit returns zero validators; `validateCfg == nil` (including `os.ErrNotExist`) falls through to per-adapter autodetect.
- [x] expose a small `builtinAdapters()` factory map (`shellcheck`, `hadolint`) so adapter registration is single-source.
- [x] **reserved-flag enforcement**: after binding an entry to an adapter, call `validateUserFlags(adapter, entry.Flags)`. On failure → return a synthetic error validator instead of registering the linter.
- [x] **per-adapter autodetect** (not all-or-nothing): for each known built-in adapter without a user entry, synthesize an entry with defaults.
- [x] write `all_test.go` covering: nil+nil, nil+ErrNotExist, nil+parse error, empty config, partial config, explicit config precedence, `enabled: false`, unknown built-in, generic with unknown ID, reserved-flag rejection across all argv forms, reserved flags allowed for generic.
- [x] update the Task 7 wiring call site to thread `validateLoadErr` in: `vallinters.All(validateCfg, validateLoadErr, projectRoot)`. *(deferred to Task 7.)*
- [x] run `go test ./internal/validate/linters/...`.

### Task 7: Wire into `buildRegistry` + add `linters [id]` subcommand
- [ ] in `internal/command/validate.go` `buildRegistry` (currently line 406), add `for _, v := range vallinters.All(validateCfg, validateLoadErr, projectRoot) { reg.Register(v) }` after the snapshot block (ordering does not matter — sorting is deterministic). `validateLoadErr` is already in scope at `buildRegistry`'s call site (line 279); thread it through the signature, mirroring how `checksLoadErr` is already passed to `valchecks.AllForStage`.
- [ ] add import alias `vallinters "devbox-cli/internal/validate/linters"`.
- [ ] **add a `linters [id]` Cobra subcommand to `newValidateCmd`** alongside `checks [id]` (the existing closest analog at validate.go ~line 154). Without this, the root `validate` is `cobra.NoArgs` and `devbox validate linters` fails before reaching the registry — scope is conveyed via subcommand, not positional args at root. Pattern:
  ```go
  cmd.AddCommand(&cobra.Command{
      Use:   "linters [id]",
      Short: "Run external linters (shellcheck, hadolint, generic)",
      Long:  "...",
      Args:  cobra.MaximumNArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          scope := []string{"linters"}
          if len(args) == 1 { scope = append(scope, args[0]) }
          return runValidate(cmd, flags, strict, quiet, stage, false, scope)
      },
  })
  ```
- [ ] update the root validate command's `Long` doc string ("Scope targets:" block, lines ~65-74) to add `devbox validate linters [id]   - external linters from devbox/validate.yml + autodetected built-ins`.
- [ ] verify scope filtering for `["linters"]` and `["linters", "shellcheck"]` works once Task 8's `GroupValidator` expansion in `Registry.Run` lands — `MatchScope` itself is unchanged, but Registry must call into `GroupValidator.Children()` for the linters group so child IDs are visible. Do not assume the per-linter validators are individually registered; the linters domain registers a single group.
- [ ] add `internal/command/validate_test.go` cases: `devbox validate linters` runs all linter validators only; `devbox validate linters shellcheck` filters to one; `--strict` upgrades a linters Warning to exit 1; unknown linter ID prints empty result (not a hard error — matches `checks` behavior).
- [ ] run `go test ./internal/command/...`.

### Task 8: Parallel execution across linters (via `GroupValidator` interface)

**Constraint discovered during planning:** `Registry.Run` (internal/validate/validate.go:100) filters by each *registered* validator's `Domain()`/`ID()`. A single wrapper validator registered for the whole linters domain cannot transparently expose per-child IDs to scope filtering. Per-linter validators registered individually give us scope filtering for free, but then concurrency has to live somewhere other than a wrapper. Two options surveyed:

- **Option A (chosen): add a small `GroupValidator` interface to `internal/validate/`** and teach `Registry.Run` to expand groups during scope matching. ~30 lines of framework change, contract stays explicit, parallelism is opt-in per group. Used by linters; available to future domains.
- **Option B (fallback): defer parallelism to v2.** Register per-linter validators individually with no framework change; run sequentially. Two adapters in v1 means wall-clock cost is small. Switch to this if Option A runs into integration issues mid-implementation; update the plan and the resolved-question note in Open Questions accordingly.

Implementation steps (Option A):

- [ ] in `internal/validate/validate.go`, add:
  ```go
  // GroupValidator is a Validator that owns child validators sharing its Domain().
  // Registry.Run expands children during scope matching and delegates execution
  // to RunGroup so the group can choose its own scheduling (parallel, ordered, etc.).
  type GroupValidator interface {
      Validator                          // ID() returns the group's own ID; Run is unused for groups.
      Children() []Validator             // each child has Domain() == group.Domain() and a unique ID().
      RunGroup(ctx Context, children []Validator) []Diagnostic
  }
  ```
- [ ] in `Registry.Run`, before the existing per-validator loop, detect `GroupValidator` instances:
  - if no scope OR scope is `["<group.Domain>"]` OR scope is `["<group.Domain>", "<child.ID>"]`, collect the matching children subset and call `group.RunGroup(ctx, subset)`. Skip the children from the per-validator loop so they don't double-run.
  - non-group validators continue to use the existing `MatchScope` + `Run` path.
- [ ] add `Registry.Run` test cases for groups: empty scope runs all children via RunGroup; `[domain]` runs all children; `[domain, child-id]` runs only the matching child; `[domain, unknown-id]` runs none (matches `checks` behavior).
- [ ] in `internal/validate/linters/`, replace `All()`'s return shape:
  - returns `[]validate.Validator` containing exactly one element: a `lintersGroup` that implements `GroupValidator`. Its `Children()` returns the per-linter validators built per Task 6. Its `Run` returns nil (unused). Its `RunGroup` does the parallel fan-out below.
- [ ] **goroutine contract (CRITICAL)**: in `lintersGroup.RunGroup`, use plain `sync.WaitGroup`, NOT `errgroup.WithContext`. Each goroutine wraps its work in a deferred `recover()` (panic → one Error diagnostic from that linter, group continues) and always completes; one linter's failure must not cancel siblings. Per-linter cancellation already comes from the `context.WithTimeout` in Task 5.
- [ ] **concurrency limit (test-seamed)**: package-level `var MaxLinterConcurrency = runtime.NumCPU()` (exported for test override, same pattern as `DefaultLinterTimeout` / `MaxLinterOutputBytes` in Task 5). Semaphore size = `min(len(children), MaxLinterConcurrency)`. Linters are subprocess-bound, but capping at NumCPU keeps the host responsive. Test seam matters: CI containers and 1-vCPU runners would otherwise force the semaphore to size 1, deadlocking any barrier-based concurrency test (Task 8 below) that needs two goroutines simultaneously in the section. Barrier test sets `MaxLinterConcurrency = 2` via the same `t.Cleanup`-restored swap helper.
- [ ] **diagnostic aggregation (race-free)**: pre-allocate `results := make([][]validate.Diagnostic, len(children))`. Each goroutine writes only to its own `results[i]`. After `wg.Wait()`, concat in order. No shared slice, no mutex. Registry re-sorts deterministically downstream.
- [ ] respect outer `ctx.Ctx` cancellation: pass it through unchanged to each child's `Run`. Do not derive a child context for the group itself (Task 5 already wraps with per-linter timeout from the original ctx).
- [ ] write `parallel_test.go`: **barrier-based correctness** (not wall-clock timing). Two fake linters that (a) atomically increment a "started" counter, (b) wait on a `sync.WaitGroup`/channel barrier until both have started, (c) emit a known diagnostic. Assertion: both reach the barrier within a generous deadline — proves concurrent execution, never flaky under load. Plus a **panic-recovery test**: one linter panics in `Run`, the other still completes, both contribute diagnostics (panic surfaces as Error).
- [ ] run `go test ./internal/validate/...` (covers both the framework change and the group implementation).

### Task 9: Update reference + internals docs
- [ ] add `linters:` section to `docs/reference/config/validate.md` — schema, autodetect rules, scope examples, severity clamp behavior, generic vs built-in, the `bin:` bare-name restriction from Open Questions §1.
- [ ] update `docs/internals/packages.md` "Validation" grouping to mention `internal/validate/linters/` with one-line responsibility, and note the new `GroupValidator` interface in the framework description.
- [ ] add a `Key Patterns` bullet in **`AGENTS.md`** (the canonical file — `CLAUDE.md` is a symlink to it per the repo guidelines): "Linters domain — autodetect built-ins when bin is on PATH; silent skip when bin missing or default paths absent; user-configured missing paths surface a Warning; only runs in `devbox validate`, never in preflight. `bin:` must be a bare command name (load-time enforced)."
- [ ] no code tests required, but verify doc examples by hand against the parser (round-trip them through `LoadValidateConfig` in a small `examples_test.go` if cheap).

### Task 10: Verify acceptance criteria
- [ ] verify all spec requirements (configuration surface, autodetect rules, severity clamp, parallelism, generic adapter, shellcheck adapter, hadolint adapter) implemented.
- [ ] verify edge cases: missing `validate.yml`, empty `linters:` block, unknown adapter ID, missing bin (autodetect vs explicit), empty path expansion.
- [ ] run `make test` (full suite) — all green.
- [ ] run `make lint` — zero issues.
- [ ] verify `devbox validate`, `devbox validate linters`, `devbox validate linters shellcheck` (Cobra subcommand + positional arg, matching the `checks [id]` pattern — NOT the `linters.shellcheck` dot form, which would require extra root-arg parsing not in this plan), `devbox validate --strict` all behave per spec on a fixture project.

## Technical Details

**`LinterEntry` shape** (in `internal/config/validate.go`):
```go
type LinterEntry struct {
    ID         string          // from map key in YAML
    Type       string          // "builtin" (default) | "generic"
    Enabled    *bool           // nil → autodetect; true/false explicit
    Bin        string          // empty → adapter.DefaultBin; must be a bare name (no path separators)
    Paths      []string        // empty → adapter.DefaultPaths; load-time validated: relative, no `..`
    Extensions []string        // empty → adapter.DefaultExtensions; load-time validated: must start with `.`
    Filenames  []string        // empty → adapter.DefaultFilenames; load-time validated: no path separators
    Flags      []string        // appended after adapter's built-in flags
    Severity   *diag.Severity  // nil → no clamp; non-nil → max severity cap. Pointer avoids the
                               // zero-value trap (SeverityUnknown = 0).
    SourceLine int             // YAML line of the map key
}
```

**Wire layout** (per spec example):
```yaml
linters:
  shellcheck:
    enabled: true
    bin: shellcheck
    paths: [devbox/scripts, scripts]
    extensions: [.sh, .bash]
    flags: [--severity=warning]
    severity: warning
  yamllint:
    type: generic
    bin: yamllint
    paths: ["."]
    extensions: [.yml, .yaml]
    flags: [-s]
```

**Autodetect rules** (implemented in `runtime.go` and `all.go`):
1. For each known built-in adapter, if no entry exists in `linters:` for it → synthesize entry with defaults (per-adapter, not all-or-nothing).
2. Block present, `enabled` omitted → `true`.
3. `enabled: false` → silent skip (no diagnostic).
4. Default bin missing on PATH → silent skip.
5. Explicit `bin:` missing on PATH → one Warning diagnostic (config problem, not code problem).
6. Path expansion yields no files → silent skip.

**Severity clamp:** clamp from above (downgrade noisy warnings) — per resolved open question. **Applied to adapter findings only, NOT to operational diagnostics** (timeout, truncation, parse failure, missing-path warning, panic) so users cannot accidentally silence runtime failure signals by setting `severity: info`. Implemented as:
```go
if entry.Severity != nil {
    for i := range findings { // findings only — operationalDiags untouched
        if findings[i].Severity > *entry.Severity {
            findings[i].Severity = *entry.Severity
        }
    }
}
return append(operationalDiags, findings...)
```
Pointer makes "no clamp" vs "set to a value" unambiguous and lets the loader distinguish "field absent" from "field parsed."

**Parallel execution:** `sync.WaitGroup` + buffered-channel semaphore inside a `GroupValidator` implementation. NOT `errgroup.WithContext` — we never want one linter's failure to cancel sibling linters. Each goroutine writes to a pre-allocated `results[i]` slot; no shared slice, no mutex. Per-goroutine `recover()` converts panics to one Error diagnostic; siblings keep running. Per-linter cancellation comes from `context.WithTimeout` in `runtime.go`. Concurrency cap is `var MaxLinterConcurrency = runtime.NumCPU()` (package-level for test override).

**Per-linter bounds (defense against hang / OOM / output explosion):**
- **Timeout:** `context.WithTimeout(parent, DefaultLinterTimeout)` per linter where `parent := ctx.Ctx; if parent == nil { parent = context.Background() }`. `validate.Context.Ctx` is nullable (`validate.go:31`); the nil-safe fallback matches `internal/validate/env/docker.go:57` and `checks/loader.go:153`. `DefaultLinterTimeout = 5 * time.Minute` (package-level for test override). Deadline → Error diagnostic in `operationalDiags`.
- **Output cap:** `MaxLinterOutputBytes = 50 << 20` (50 MB) combined stdout+stderr via `boundedWriter` around each stream. Truncation → Warning diagnostic in `operationalDiags`; parser still runs on what was captured. Package-level var for test override.
- **Output for adapter consumption:** held in `bytes.Buffer` (already bounded above), then passed to `ParseOutput([]byte, []byte, int)`.

**Reserved-flag policy (built-in adapters):** built-in adapters declare `ReservedFlags() []string` for CLI flags the user cannot pass in `flags:` — used for tokens whose value the parser depends on (shellcheck `--format`/`-f`, hadolint `-f`/`--format`). Enforced at adapter-binding time in `All()` via `validateUserFlags(adapter, entry.Flags)`; failure produces a synthetic error validator instead of registering the linter, so a bad config short-circuits before any subprocess runs. Position-based override-prevention is NOT a safety mechanism (shellcheck flag precedence has been observed as first-wins on some versions, not last-wins as the man page suggests) — load-time rejection is the contract. Real-binary smoke tests in Tasks 3/4 verify that with a clean entry, the captured output actually parses as JSON.

**Generic adapter contract:** exit 0 → no diagnostics; non-zero → single error diagnostic with combined stdout+stderr as `Message` (truncated to ~2 KB at message-build time to keep the table readable, with `\n…(truncated)` suffix). This 2 KB cap is separate from the 50 MB stream cap above — the stream cap protects memory, the message cap protects the rendered table. Generic adapter has no `ReservedFlags` (user owns the entire flag surface since we don't parse output structure).

**Adapter error handling:** `ParseOutput` returns `([]Diagnostic, error)`. Runtime puts those into the `findings` bucket (clampable) and, on non-nil error, appends one Warning to `operationalDiags` (NOT clamped — see Severity clamp above). Errors never propagate to the Registry — one broken adapter must not abort the whole `devbox validate` run.

**Exec safety:** `exec.CommandContext(runCtx, bin, args...)` only — never `exec.Command("sh", "-c", ...)`. Combined with the load-time restriction that `bin:` must be a bare command name (no path separators), this means the only way to execute arbitrary code is to install a malicious binary on `PATH` — a level of access that already implies system compromise. See Open Questions § 1 for the trust-model rationale.

## Open Questions (resolved)

1. **`bin:` trust model — resolved: bare names only.** `bin:` must be a command name (no `/` or `\`), resolved via `PATH` at runtime. Absolute paths and relative paths under the project are forbidden at load time. Rationale: `validate.yml` ships with the repo; a malicious or compromised repo with `bin: ./scripts/evil.sh` should not silently execute arbitrary code on `devbox validate`. This is the same risk class as `.eslintrc` custom rules or `pre-commit` repos, and we choose the more restrictive default. Users who genuinely need a custom binary path install it onto their `PATH` (or use a wrapper). Revisit only if real-world need surfaces.
2. **Parallel execution — resolved: yes**, via `sync.WaitGroup` + semaphore (NOT errgroup), capped at `runtime.NumCPU()`.
3. **Severity clamp direction — resolved: from above** (downgrade noisy warnings). Implemented via `*diag.Severity` to avoid the zero-value trap.
4. **Missing explicit `bin:` — resolved: Warning.** Autodetected default-bin-missing is silent (rule 4); explicit-bin-missing is loud (rule 5).

## Post-Completion

**Manual verification:**
- Install real `shellcheck` and `hadolint` locally; run `devbox validate` on a project with mixed clean / dirty `.sh` and `Dockerfile` files; confirm column wrapping looks right in the diagnostics table.
- Verify silent-skip path: rename `shellcheck` out of PATH, re-run `devbox validate`, confirm zero linters diagnostics and exit 0.
- Verify explicit-bin rejection at load time: set `bin: /usr/bin/shellcheck` in `validate.yml`, confirm `devbox validate` fails with a clear load-error diagnostic (not a runtime Warning) — the bare-name rule from Open Questions § 1.
- Verify explicit-bin warning at runtime: set `bin: nonexistent-sc` (bare name, but binary not on PATH), confirm one Warning emitted.
- Verify `--strict` upgrades a linter Warning to exit 1.
- Verify timeout: write a fake script `sleep 9999` symlinked into PATH as `shellcheck`, run `devbox validate`, confirm Error diagnostic within ~5 min ("`shellcheck` timed out after 5m").
- Verify output cap: run a real shellcheck on a large repo configured with `paths: ["."]`, confirm either clean completion or a truncation Warning — never an OOM.
- Verify per-adapter autodetect: configure `linters: { shellcheck: { flags: [...] } }` only; run `devbox validate`, confirm hadolint still runs from its autodetected defaults.
- Sanity-check wall-clock improvement with parallel execution: run both shellcheck and hadolint over a non-trivial project, compare to `devbox validate linters shellcheck` + `devbox validate linters hadolint` run sequentially.

**External system updates:**
- CI image (if separate) may need `shellcheck` / `hadolint` packages installed to exercise the real-binary path in CI.
- Update any onboarding doc that lists project linters (none known today — flag if discovered during implementation).
- Document the `bin:` trust model decision in any internal security/threat-model doc the project keeps (none known today — skip if absent).

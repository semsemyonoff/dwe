# Environment & Project Validation

## Overview

Extend `devbox validate` from "is this YAML correct?" to "is this environment ready to work?"

Two new validation domains, both implementing the existing `internal/validate.Validator` interface:

- **`env.*`** — built-in environment probes (docker binary, docker daemon, compose plugin v2, git/shell binaries, `.devbox/` writable). Hardcoded in Go, no user config.
- **`checks.*`** — declarative project checks loaded from `devbox/validate.yml`. Each entry is a synthetic `Validator` that dispatches to an `internal/builtin` at run time.

Five new builtins back the checks (`shell`, `file_exists`, `executable_in_path`, `env_keys_present`, `tcp_reachable`). They're also reusable in deploy step bodies / `check:` blocks.

A new preflight hook in `deploy run` / `run` / `stop` runs the relevant env + checks before doing real work. Errors abort early with actionable hints; `--skip-preflight` bypasses.

The full motivation, schema, and worked examples live in the chat thread that produced this plan — the architecture, schema, builtin list, examples, and four resolved open questions are not re-paraphrased below.

## Context (from discovery)

Existing framework that this plan reuses unchanged:

- `internal/validate/validate.go` — `Validator` interface (`ID() string`, `Domain() string`, `Run(Context) []Diagnostic`), `Registry`, `Diagnostic{Severity,Domain,Target,File,Line,Message,Hint}`, `MatchScope`, `ExitCode`.
- `internal/validate/config/ui.go` — `FormatDiagnostics`, `RenderDiagnosticsTable`. Invoked from `internal/command/validate.go:runValidate`.
- `internal/validate/{config,templates,commands}/all.go` — per-domain `All()` rosters that the root `validate` command assembles.
- `internal/builtin/builtin.go` — global registry, `Builtin` interface with `Validate(with) error` / `Describe(with) string` / `Run(ctx, with, ectx) error`. Top-level `builtin.Validate(name, with)` / `Run(...)` wrappers already exist — no new dispatcher API needed (spec's "ValidateWith" maps to existing `builtin.Validate`).
- `internal/config/devbox.go` — strict-decode loader pattern (`yaml.Decoder.KnownFields(true)`) for `LoadConfig` / `LoadDeployConfig` / `LoadResetConfig`. New `LoadValidateConfig` follows the same pattern.
- `internal/command/{deploy,run,stop,validate,root}.go` — `rootFlags` is persistent. `deploy` loads config inline; `run`/`stop` delegate to `internal/lifecycle.RunRun` / `RunStop` which load config near the top of the function.
- `internal/usercommands/registry/registry.go:LoadRegistry` + `Get(id)` — already used by deploy/pipeline to resolve `type: command`. Reused verbatim for `type: command` inside checks.

Resolved design questions (all four spec-open items accepted as proposed):

- Sequential execution V1; parallelism deferred.
- Default timeouts: `shell` 10s, `tcp_reachable` 3s. Per-entry `with.timeout` overrides.
- `--skip-preflight` suppresses errors AND warnings, and prints a one-line "would-have-blocked" summary so the user knows what was bypassed.
- Preflight diagnostics go to stderr (consistent with current `validate` output).

Additional review-driven constraints (incorporated into the relevant tasks below):

- **Severity / type are enums, not free strings.** Loader rejects unknown `severity` and unknown `type` at parse time. `severity` maps to the existing `validate.Severity` int enum (`SeverityError|Warning|Info`); the YAML wire type stays string but is converted on load. `type` is restricted to `{"builtin", "command"}`.
- **`Validator` interface has no error return path.** Every load-time problem in `internal/validate/checks/` (unknown builtin, unknown command, invalid `with:`) becomes a pre-baked failing `Diagnostic` carried by a synthetic validator that emits it from `Run()`. `checks.All(...)` never returns an `error` — that keeps `devbox validate` surfacing all problems in one pass.
- **`LoadValidateConfig` returns warnings alongside the config.** Signature: `func LoadValidateConfig(path string) (*ValidateConfig, []validate.Diagnostic, error)`. Soft warnings (unknown `stages:` values) live in the slice.
- **Single parse point, results threaded via `validate.Context`.** `validate.yml` is parsed exactly once per `devbox validate` (or per preflight) run. The caller (`runValidate` or `RunPreflight`) calls `LoadValidateConfig` once and stuffs the result into a small extension of `validate.Context`:
  ```go
  // additions to internal/validate/validate.go
  type Context struct {
      // ... existing fields ...
      ValidateCfg          *config.ValidateConfig // nil when load failed
      ValidateCfgWarnings  []Diagnostic           // info-severity slice from loader
      ValidateCfgLoadErr   error                  // nil, os.ErrNotExist, or strict-decode error
  }
  ```
  Task 5's `config.validate` validator reads these fields from `Context` — it does NOT call `LoadValidateConfig` itself. Same goes for `checks.All` / `checks.AllForStage` (Task 4): they take `ctx.ValidateCfg` as input, not a path.
- **Loader errors are loud.** Callers distinguish `os.ErrNotExist` (silent → no diagnostic) from every other error (surfaced by `config.validate` reading `ctx.ValidateCfgLoadErr` and emitting one `SeverityError` diagnostic). No `_ = err` discards anywhere.
- **`shell` builtin uses hardcoded `sh -c`**, not `config.ShellBin(cfg)`. Matches the existing convention from deploy/condition `when:` predicates (see `CLAUDE.md` → Key Patterns → Binary accessors).
- **`env_keys_present` needs a key-AND-value parser with shell-style unquoting** — `builtin.ParseEnvKeys` only records keys (KEY= and KEY=value are indistinguishable through it). Add a sibling `builtin.ParseEnvEntries(data []byte) map[string]string` next to `ParseEnvKeys` in `internal/builtin/configs_copy.go`, sharing the line-parsing logic but returning the trimmed-AND-unquoted value. Unquoting MUST be applied (mirroring `deploy.SourceDotEnv`'s `if n := len(val); n >= 2 && val[0] == val[n-1] && (val[0] == '"' || val[0] == '\'') { val = val[1:n-1] }`), otherwise `KEY=""` would yield `\"\"` and falsely pass an `entries[k] == ""` emptiness check. `env_keys_present` then enforces "absent OR empty" by checking `entries[k] == ""` against the unquoted value, which correctly treats `KEY=`, `KEY=""`, and `KEY=''` as empty. Do NOT import `internal/envfile` (render-only). Do NOT reuse `deploy.SourceDotEnv` (mutates `os.Environ`).
- **Preflight runs before any side effects, including the deploy lock file.** This is the critical ordering constraint — see Task 7 for per-command insertion points. Specifically: before `lock.Acquire` in deploy (lock creates a file in `.devbox/deploy/`), before the git probe/pull in run, before any container-stop operations in stop. The lock requires cfg load to happen *before* preflight; the existing `lock.Acquire` call moves to immediately *after* preflight.
- **`--skip-preflight` is a local flag**, declared on each of the four lifecycle commands (`deploy run`, `run`, `stop`, `restart`) via a small `addSkipPreflightFlag(cmd, *bool)` helper. Not on `rootFlags` — it would be meaningless on `validate`, `status`, `docs`, etc.
- **Skip semantics:** under `--skip-preflight`, NO validators run. The flag is a true bypass — preflight prints one line to stderr (`"preflight skipped (--skip-preflight)"`) and returns nil. Rationale: `type: command` checks invoke arbitrary user scripts. Even with `NonInteractive=true`/`SkipConfirm=true`, the script body can `rm -rf`, hit a network, write files, etc. Running them under a flag named "skip" violates user expectations. The cost is losing the "would have blocked" count; the user already chose to bypass, so the count's value is low. Env probes are cheap and never mutate, but we still skip them too — having a single uniform semantics for the flag is more important than the small win of running just the env probes.
- **Hint length.** Per project memory: `Diagnostic.Hint` stays concise; long hints split with `\n`. Applies to both env probes (Task 3) and the validate.md docs (Task 8) so user-authored hints don't grow into paragraphs.
- **Builtin error messages** are lowercase, no trailing punctuation, no `fmt.Errorf` prefix that duplicates the builtin name. The diagnostic already carries `checks.<id>`.
- **Package name `checks` is plural** — chosen for symmetry with the existing `internal/validate/commands/`. Documented here so the choice is intentional, not accidental.
- **Stage-aware filtering is NOT a `Registry` extension.** `validate.Registry.Run` only filters via `MatchScope` (domain / domain+id). Stage is metadata that lives on `CheckEntry`, not on `Validator`. The plan implements stage filtering one level up: callers (the `validate` command and the preflight assembler) build the validator list themselves — `env.All(cfg)` always + `checks.AllForStage(validateCfg, baseDir, cmdRegistry, stage)` (a sibling of `checks.All` that filters entries by `MatchStage` before producing synthetic validators). The resulting list is then registered into a fresh `Registry` and run with the usual scope semantics. No changes to `internal/validate/validate.go`.
- **`type: command` checks run with a locked-down `RunContext` AND pass through `entry.With`.** User commands are heavyweight (params resolution, docker config load, template eval, child-process IO). The check's `with:` block IS the user-command params payload — it must be threaded through to `runtime.BuildRunContext`, not dropped. The loader builds the full `RunContext` via `runtime.BuildRunContext(cfg, cmdRegistry, def, entry.With, projectRoot)` and then locks down the IO/interactivity fields: `SkipConfirm=true`, `NonInteractive=true`, `SkipNotify=true`, `Stdout=io.Discard`, `Stderr=&bytes.Buffer{}` (captured for inclusion in the diagnostic message on failure), `Stdin=nil` (which `stdinOrOS` will route to `os.Stdin` — acceptable only because `NonInteractive=true` prevents any prompt). Type whitelist: `shell` and `script` are allowed; `workflow`, `service_exec`, `service_run`, `devbox`, `builtin` (as a user-command type) are rejected at load time with a clear "checks may only invoke user commands of type shell or script" diagnostic.
- **Mutation policy for `type: command` checks (design decision, not enforced).** Convention: checks SHOULD be idempotent inspection commands — they answer "is the world ready?" not "make the world ready." The CLI does NOT enforce this (there's no read-only sandbox available to subprocesses). What the CLI does enforce: non-interactive, no notifications, captured output. Mutation is on the user, and `docs/reference/config/validate.md` documents this convention loudly. Rationale: the codebase doesn't add restrictions for hypothetical misuse, and an idempotency check would have to be runtime-trust-based anyway. A mutating check is a sharp edge, not a vulnerability.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task. Tests are required, not optional. Cover success and error paths.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run tests after each change.
- This repo is pre-release: NO backwards-compatibility shims, NO `schema_version` bumps, NO migration paths. New shape only.

## Testing Strategy

- **Unit tests**: required per task (table-driven; matches `internal/validate/**` and `internal/builtin/**` existing style).
- **Loader tests** (`internal/config/validate_test.go`, Task 2 scope): use `testdata/` YAML fixtures for `LoadValidateConfig` covering schema-level strict decoding only — unknown top-level field, missing required top-level field (`id`/`description`/`stages`/`type`/`cmd`), duplicate `id`, unknown `severity`, unknown `type`, unknown stage produces info diagnostic. `with:`-shape validity is NOT tested here; it depends on the builtin registry and is owned by Task 4's checks-loader tests.
- **Checks-loader tests** (`internal/validate/checks/loader_test.go`, Task 4 scope): cover `with:` validation against the builtin registry (`builtin.Validate(entry.Cmd, entry.With)` rejection paths) plus the cached-diagnostic fast-fail patterns (unknown builtin, unknown user command, type-whitelist rejection).
- **No e2e harness** in this repo currently — manual verification of preflight wiring goes in Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs that live in this repo.
- **Post-Completion** (no checkboxes): manual smoke tests of preflight against a real Docker setup, behavior verification on a sample project.

## Implementation Steps

### Task 1: Add the five new builtins

- [x] add `internal/builtin/shell.go` — `shell` builtin: `with.cmd` (required, string), `with.timeout` (optional, duration, default `shellDefaultTimeout = 10*time.Second`). `Validate` enforces required + types; `Run` execs hardcoded `sh -c <cmd>` (NOT `config.ShellBin` — matches deploy/condition `when:` convention) with timeout context; exit-0 = pass. Error message on non-zero exit: `"exit status N: <last line of stderr>"`, lowercase, no trailing punctuation.
- [x] add `internal/builtin/file_exists.go` — `file_exists`: `with.path` (required, relative path; resolved against `ectx.ProjectRoot`); `os.Stat`-based; error message `"file not found: <path>"`.
- [x] add `internal/builtin/executable_in_path.go` — `executable_in_path`: `with.name` (required); `exec.LookPath`; error message `"not found in PATH: <name>"`.
- [x] **First**, in `internal/builtin/configs_copy.go`, add a sibling helper `ParseEnvEntries(data []byte) map[string]string` next to `ParseEnvKeys`. It reuses `envLineKey` for blank/comment skipping, but for non-empty key lines it captures `strings.TrimSpace(value)` from `strings.Cut(line, "=")` AND applies shell-style quote stripping (matching surrounding `"` or `'` pairs are removed — copy the snippet from `deploy.SourceDotEnv`). This collapses `KEY=`, `KEY=""`, and `KEY=''` to an empty-string value, which is the behavior `env_keys_present` needs. Add a test alongside `ParseEnvKeys` covering: KEY= → "", KEY=value → "value", KEY="" → "", KEY='' → "", KEY=" " → " " (whitespace inside quotes preserved is acceptable; absent-or-empty cares about exact ""), KEY="value" → "value", KEY='value' → "value", mismatched-quotes KEY="value' → `"value'` (no strip), and the comment/blank skip path.
- [x] add `internal/builtin/env_keys_present.go` — `env_keys_present`: `with.file` (required, relative to `ectx.ProjectRoot`), `with.keys` (required, []string non-empty). Parse via the new `builtin.ParseEnvEntries`. Fail with `"missing or empty keys: a, b, c"` if any key is absent from the file OR present with empty value (after trim). Do NOT import `internal/envfile` (render-only); do NOT reuse `deploy.SourceDotEnv` (mutates `os.Environ`).
- [x] add `internal/builtin/tcp_reachable.go` — `tcp_reachable`: `with.host` (required, non-empty string), `with.port` (required, int 1–65535), `with.timeout` (optional, duration, default `tcpDefaultTimeout = 3*time.Second`); `net.DialTimeout`; error message `"dial tcp <host>:<port>: <reason>"`.
- [x] register all five in the existing `registry` map in `internal/builtin/builtin.go`.
- [x] write table-driven `_test.go` per builtin covering: `Validate` success, `Validate` rejects missing/wrong-type/out-of-range keys, `Describe` produces a stable human string, `Run` success + error. For `shell`/`tcp_reachable` cover timeout path.
- [x] run `go test ./internal/builtin/...` — must pass before Task 2.

### Task 2: Add `LoadValidateConfig` strict-decode loader

- [x] add `internal/config/validate.go` — types: `ValidateConfig { Checks []CheckEntry }`, `CheckEntry { ID, Description string; Stages []string; Severity diag.Severity; Hint string; Type string; Cmd string; With map[string]any; SourceLine int }`. Note: `Severity` is the `diag.Severity` enum (== `validate.Severity` via type alias — see "Deviation" below), parsed from string at load time.
- [x] implement `LoadValidateConfig(path string) (*ValidateConfig, []diag.Diagnostic, error)` mirroring `LoadDeployConfig` for strict decoding (`yaml.NewDecoder(...).KnownFields(true)`); capture source line numbers per entry via a `yaml.Node` walk. Returns `[]diag.Diagnostic` for soft warnings (consumed by Task 5).
- [x] enforce required fields at load time (hard errors → returned `error`):
  - [x] `id`, `description`, `type`, `cmd` non-empty.
  - [x] `stages` non-empty.
  - [x] `id` unique across entries.
  - [x] `type` ∈ `{"builtin", "command"}` — unknown values are hard errors.
  - [x] `severity` parsed via `parseSeverity(string) (diag.Severity, error)` — accepts `"error"|"warning"|"info"`, defaults to `SeverityError` on empty, rejects anything else.
- [x] emit soft warnings (added to the returned diagnostic slice, NOT errors):
  - [x] stages outside `{"deploy", "run", "stop", "command"}` → `SeverityInfo`, message `"stage \"X\" not bound to built-in hooks"`, file/line filled in.
- [x] `with:` shape is NOT validated here (validation lives in `internal/validate/checks/` so it can use the builtin registry — Task 4).
- [x] add canonical resolver: `ValidateConfigPath(baseDir string) string` → `<baseDir>/devbox/validate.yml`, exported from `internal/config/validate.go` (no `paths.go` file existed; resolver lives next to the loader).
- [x] write `internal/config/validate_test.go` with `testdata/validate/*.yml` fixtures: happy path, unknown top-level field rejected, missing required field rejected, duplicate id rejected, severity defaults to `SeverityError`, unknown `severity` rejected, unknown `type` rejected, unknown stage produces info diagnostic (not error).
- [x] run `go test ./internal/config/...` — passed.

**Deviation from plan (import cycle resolution):** the plan specified `Severity validate.Severity` for `CheckEntry.Severity`, but `internal/validate` already imports `internal/config` (for `Context.Cfg *config.DevboxConfig`), and adding the reverse direction would create a cycle. Resolved by extracting `Severity` + `Diagnostic` into a new leaf package `internal/validate/diag` (no imports outside stdlib) and re-exporting them from `internal/validate` via type aliases (`type Severity = diag.Severity` plus const aliases). All ~497 existing references to `validate.Severity` / `validate.Diagnostic` continue to work unchanged. `internal/config` imports `internal/validate/diag` directly. The signatures throughout the plan referencing `validate.Diagnostic` and `validate.Severity` remain semantically correct via the aliases — Task 4/5 callers may use either spelling.

### Task 3: Add `internal/validate/env/` (hardcoded probes)

- [x] create `internal/validate/env/env.go` with shared helpers (project context accessor, diagnostic builder).
- [x] create `internal/validate/env/docker.go` — three validators: `env.docker_bin` (LookPath on `config.DockerBin(cfg)`), `env.docker_daemon` (`docker version` exits 0), `env.docker_compose` (`docker compose version` exits 0, plugin v2 detected).
- [x] create `internal/validate/env/binaries.go` — `env.git_bin` (`config.GitBin(cfg)`), `env.shell_bin` (`config.ShellBin(cfg)`).
- [x] create `internal/validate/env/project_perms.go` — `env.project_perms`: verify `.devbox/` is writable and the lock path is creatable (try-create-temp-file pattern).
- [x] create `internal/validate/env/all.go` — `All(cfg *config.DevboxConfig) []validate.Validator` returning the six probes.
- [x] each validator returns rich `Hint` on failure (e.g. install URL for compose plugin). Hints follow the project's existing `Diagnostic.Hint` formatting rule (concise; `\n` splits long ones).
- [x] write per-probe unit tests using temp dirs and stub binaries on `$PATH` (or skip if a clean test is impractical — note in test).
- [x] run `go test ./internal/validate/env/...` — must pass before Task 4.

### Task 4: Add `internal/validate/checks/` (synthetic validators from validate.yml)

- [x] create `internal/validate/checks/loader.go` with two exported entry points:
  - [x] `All(cfg *config.ValidateConfig, baseDir string, cmdRegistry *registry.Registry) []validate.Validator` — produces synthetic validators for every entry. **Nil-tolerant**: `cfg == nil` (which happens when Task 6's single load failed) returns an empty slice without panicking.
  - [x] `AllForStage(cfg *config.ValidateConfig, baseDir string, cmdRegistry *registry.Registry, stage string) []validate.Validator` — same, but filtered by `MatchStage(entry, stage)`. Empty stage = all (same as `All`). Same nil-tolerance. This is the entry point used by Task 6's `--stage` flag and Task 7's preflight.
  - [x] both signatures return `[]Validator` only — NO `error` (the `Validator` interface has no error return path; all problems must surface as diagnostics).
  - [x] both signatures take `*config.ValidateConfig` directly, NOT a path. Disk I/O is the caller's responsibility (Task 6 / Task 7 do the single load).
- [x] for each `CheckEntry` produce one synthetic validator: ID = `<id>` (no `checks.` prefix — Domain = `checks` provides the namespace), Domain = `checks`.
- [x] **Load-time dispatch decisions happen inside `All`/`AllForStage`**, producing either a "runner" validator (resolved, will dispatch at `Run` time) or a "pre-baked failure" validator (carries a cached `Diagnostic` and emits it from `Run`). The pre-baked path covers:
  - [x] `Type == "builtin"` with unknown `entry.Cmd` → diagnostic `"unknown builtin: <name>"` at error severity.
  - [x] `Type == "builtin"` where `builtin.Validate(entry.Cmd, entry.With)` rejects → diagnostic carrying that error verbatim.
  - [x] `Type == "command"` with unknown `entry.Cmd` in `cmdRegistry` → diagnostic `"unknown command: <id>"` at error severity.
  - [x] `Type == "command"` with target `CommandDef.Type` outside the allowed whitelist `{shell, script}` → diagnostic `"checks may only invoke user commands of type shell or script (got: <type>)"` at error severity. Rejects `workflow`, `service_exec`, `service_run`, `devbox`, `builtin`.
- [x] runner validator behavior at `Run` time:
  - [x] `Type == "builtin"`: call `builtin.Run(ctx, entry.Cmd, entry.With, ectx)`. Map nil → no diagnostics; non-nil → `Diagnostic{Severity: entry.Severity, Message: err.Error(), Hint: entry.Hint, File: "devbox/validate.yml", Line: entry.SourceLine}`.
  - [x] `Type == "command"`: dispatch via the locked-down path described below. Same diagnostic mapping on error; on success, attach a short tail of captured stderr to the diagnostic message only if non-empty (helps users see *why* it passed/failed).
- [x] **Locked-down user-command dispatch**. In the runner validator's `Run`:
  - [x] `rc, err := runtime.BuildRunContext(cfg, cmdRegistry, def, entry.With, projectRoot)` — `entry.With` IS the user-command params payload and MUST be threaded through; passing nil would silently drop user-declared parameterization. Error from BuildRunContext (e.g. param resolution failure) becomes a runtime diagnostic.
  - [x] override IO/interactivity fields: `rc.SkipConfirm = true`, `rc.NonInteractive = true`, `rc.SkipNotify = true`, `rc.Stdout = io.Discard`, `rc.Stderr = &bytes.Buffer{}` (kept for diagnostic message), `rc.Stdin = nil`. Do NOT override `rc.UnderParallel` — let it propagate.
  - [x] call `runtime.RunCommand(ctx, rc)`. Non-nil error → diagnostic; nil → pass.
- [x] add a checks-loader test that verifies `entry.With` reaches the user command — set up a `type: shell` user command whose script writes `${param.foo}` to a file, configure a check with `with: { foo: bar }`, run the validator, assert the file contains `bar`.
- [x] add `internal/validate/checks/stages.go` — stage filtering helper `MatchStage(entry CheckEntry, stage string) bool` (empty stage = match all; otherwise membership test). Single source of truth used by both `AllForStage` and Task 7's preflight assembly.
- [x] write tests: covering happy builtin dispatch (file_exists against a temp dir), failed dispatch propagates message+hint+line, unknown builtin surfaces as cached diagnostic, unknown command surfaces as cached diagnostic, type-whitelist rejection (workflow) surfaces as cached diagnostic, invalid `with:` surfaces as cached diagnostic, `MatchStage` table-driven, `AllForStage("deploy")` excludes run-only entries.
- [x] integration test: a `type: shell` user command with `confirmation: true` does NOT hang — `NonInteractive=true` skips the prompt path.
- [x] run `go test ./internal/validate/checks/...` — passed.

### Task 5: Add config-validator for validate.yml itself

- [x] extend `validate.Context` in `internal/validate/validate.go` with three fields: `ValidateCfg *config.ValidateConfig`, `ValidateCfgWarnings []Diagnostic`, `ValidateCfgLoadErr error`. The validator and `checks.All`/`AllForStage` read these; no validator calls `LoadValidateConfig` itself.
- [x] add `internal/validate/config/validate_yml.go` — validator `config.validate`. Implementation reads exclusively from the passed-in `Context`:
  - [x] `ctx.ValidateCfgLoadErr == nil` → emit the `ctx.ValidateCfgWarnings` slice unchanged (typically unknown-stage info diagnostics).
  - [x] `errors.Is(ctx.ValidateCfgLoadErr, os.ErrNotExist)` → emit nothing (validate.yml is optional).
  - [x] any other `ctx.ValidateCfgLoadErr` → emit a single `SeverityError` diagnostic `{Domain: "config", Target: "validate", File: "devbox/validate.yml", Message: ctx.ValidateCfgLoadErr.Error()}`. Covers strict-decode failures, unknown `type`, unknown `severity`, missing required fields, duplicate ids.
- [x] NO call to `config.LoadValidateConfig` from inside the validator — Task 2 is the single parse point; Task 6 (and Task 7's preflight) do the one-and-only parse and populate the Context.
- [x] register it in `internal/validate/config/all.go`.
- [x] write tests covering: nil-load-err + no warnings → no diagnostics; nil-load-err + custom-stage warning → one info diagnostic; ErrNotExist load-err → no diagnostics; strict-decode load-err → one error diagnostic; unknown-severity load-err → one error diagnostic. Tests construct `validate.Context` directly with the desired fields — no disk I/O needed.
- [x] run `go test ./internal/validate/config/...` — passed.

### Task 6: Wire env + checks into `devbox validate` and add `--stage` flag

- [ ] in `internal/command/validate.go:runValidate`, after loading `cfg`, perform the single parse of validate.yml: `validateCfg, warnings, loadErr := config.LoadValidateConfig(ValidateConfigPath(...))`. **Never short-circuit** — `runValidate` is an aggregator (see `loadForValidate`'s `errPartialLoad` pattern at validate.go:163). Always continue regardless of `loadErr`:
  - [ ] populate the `validate.Context` with all three: `ctx.ValidateCfg = validateCfg` (nil-tolerant downstream), `ctx.ValidateCfgWarnings = warnings`, `ctx.ValidateCfgLoadErr = loadErr`.
  - [ ] `validateCfg` may be nil when `loadErr != nil`; that's fine — `checks.All(ctx)` / `checks.AllForStage(ctx, stage)` accept a nil config and produce zero validators.
  - [ ] do NOT call `LoadValidateConfig` a second time anywhere else in the run. Task 5's `config.validate` validator reads from `ctx.ValidateCfgLoadErr` / `ctx.ValidateCfgWarnings` and is the sole emitter of the load-error diagnostic.
  - [ ] NEVER `return fmt.Errorf("loading validate.yml: %w", err)` here: a malformed `validate.yml` must not hide diagnostics from `config`, `templates`, `commands`, `env`, etc. — the `config.validate` validator surfaces the load failure inline with everything else.
  - [ ] no `_ = err` discards anywhere.
- [ ] also load the user-command registry (same pattern as deploy command).
- [ ] register validators into the validate `Registry`. Stage selection happens at *assembly time*, not via a registry-level filter (the registry only knows domain/id via `MatchScope`):
  - [ ] when `--stage` is empty: `env.All(cfg)` + `checks.All(validateCfg, baseDir, cmdRegistry)` + existing config/templates/commands rosters.
  - [ ] when `--stage <name>` is set: `env.All(cfg)` (always — env probes have no stages) + `checks.AllForStage(validateCfg, baseDir, cmdRegistry, stage)` + existing config/templates/commands rosters (unaffected — stage is a checks-only concept).
- [ ] `--stage <name>` is a local flag on the validate command. No changes to `validate.Registry` or `MatchScope`.
- [ ] update scope handling so `devbox validate env` / `devbox validate checks` / `devbox validate checks ghcr-login` work via the existing `MatchScope` mechanism — no new dispatch logic needed, just registration.
- [ ] write tests in `internal/command/validate_test.go`: `--stage deploy` filters out run-only checks but keeps env probes; scope `env` shows only env probes; scope `checks foo` shows only that check; missing validate.yml is silently tolerated; malformed validate.yml does NOT short-circuit the command — diagnostics from other domains (`config`, `templates`, `commands`) still render, plus one `config.validate` error diagnostic surfaces the load failure.
- [ ] run `go test ./internal/command/...` — must pass before Task 7.

### Task 7: Preflight hook in `deploy run` / `run` / `stop`

**Ordering constraint (load-bearing):** preflight MUST run *before* any side effects on Docker, git, or the filesystem — including the deploy lock file. The current side-effect sites are documented per-command below. Where preflight needs cfg (env probes use `config.DockerBin(cfg)` etc.) OR the user-command registry (for `type: command` checks), both are hoisted ahead of the side effect. Registry-load failures are tolerated like in `runValidate`: a nil registry is passed to `checks.AllForStage`, which surfaces unknown-command diagnostics for any `type: command` check that depends on it.

**Lifecycle command coverage:** the flag and preflight hook apply to FOUR commands, not three — `deploy run`, `run`, `stop`, AND `restart`. Restart is a composite (`RunStop` → `RunRun`) and must propagate `SkipPreflight` to both legs.

- [ ] add `internal/command/preflight.go` — `RunPreflight(ctx, cfg, cmdRegistry, baseDir, stage string, skip bool, errOut io.Writer) error`. Behavior:
  - signature: `RunPreflight(ctx context.Context, cfg *config.DevboxConfig, cmdRegistry *registry.Registry, baseDir, stage string, skip bool, errOut io.Writer) error`. `cmdRegistry` is nil-tolerant — `checks.AllForStage` produces unknown-command diagnostics for any `type: command` entry when nil.
  - if `skip == true`: print `"preflight skipped (--skip-preflight)"` to `errOut` and return nil. Do NOT load validate.yml. Do NOT assemble validators. Do NOT execute anything. The flag is a true bypass — `type: command` checks invoke arbitrary user scripts and could mutate state, network, or filesystem; running them under a flag named "skip" violates the user's stated intent.
  - else: perform the single load `validateCfg, warnings, loadErr := config.LoadValidateConfig(ValidateConfigPath(baseDir))`. Build `validate.Context` populated with cfg + validateCfg/warnings/loadErr exactly like `runValidate` does. Assemble `env.All(cfg)` + `checks.AllForStage(validateCfg, baseDir, cmdRegistry, stage)` + the single `config.validate` validator (so a malformed validate.yml surfaces inline as part of preflight, not silently). Run sequentially against the Context, render with `RenderDiagnosticsTable`, write to `errOut`. If any `SeverityError` diagnostic: return a sentinel `preflightFailedError` whose `ExitCode()` returns 1 (mirrors `validationFailedError`).
- [ ] add a local flag helper `addSkipPreflightFlag(cmd *cobra.Command, target *bool)` in `internal/command/preflight.go`. Register it on each of the four lifecycle commands (`deploy run`, `run`, `stop`, `restart`) — NOT on `rootFlags` (would be meaningless on `validate`, `status`, `docs`, etc.).
- [ ] add `SkipPreflight bool` to `lifecycle.RunContext` and `lifecycle.StopContext` in `internal/lifecycle/run.go`. Both must be wired through to the respective `RunRun` / `RunStop` body's preflight call.
- [ ] **deploy `run` insertion point** (`internal/command/deploy.go`): preflight must run BEFORE `lock.Acquire` (currently around line 211). Reorder the deploy command body to:
  1. Compute `workDir` and `lockPath` (no cfg needed for either).
  2. `cfg, err := config.LoadConfig(flags.configPath)` — hoisted ahead of lock.
  3. `reg, err := loadCommandRegistry(flags.configPath)` — also hoisted (currently at line 239). Tolerate failure: pass nil to `RunPreflight` (matches `runValidate`'s pattern of registering nil-tolerant validators).
  4. `RunPreflight(ctx, cfg, reg, workDir, "deploy", skipPreflight, errOut)` — gates the operation.
  5. `lck, err := lock.Acquire(lockPath)` — only after preflight passes (or `--skip-preflight`).
  6. Existing flow continues unchanged from here (docker config load, `EnsureVolumes`, the registry handle from step 3 is reused — do not re-load).
  Test must assert: on preflight failure, no lock file is created in `.devbox/deploy/` (check via `os.Stat`).
- [ ] **`devbox run` insertion point** (`internal/lifecycle/run.go:RunRun`): preflight must run BEFORE the git probe/pull block (currently around lines 115–150) and BEFORE registry load (currently around line 168). Hoist `usercommands.LoadRegistryFromConfigPath` to immediately after `LoadConfig`. Same nil-tolerance: registry load failure does NOT abort; pass nil registry to preflight. After preflight + (optional) git pull + (conditional) config reload, the existing flow resumes from registry-use onward — the reload case must also reload the registry, which it already does conceptually (just hoist the reload alongside the cfg reload). `SkipPreflight bool` is read from `RunContext`.
- [ ] **`devbox stop` insertion point** (`internal/lifecycle/run.go:RunStop`): preflight must run BEFORE any container-stop operations AND before any lock acquisition stop may do. If stop currently loads the registry, hoist it; if not, preflight passes nil registry. `SkipPreflight bool` is read from `StopContext`.
- [ ] **`devbox restart` wiring** (`internal/command/restart.go` and `internal/lifecycle/run.go:RunRestart`):
  - [ ] `newRestartCmd` declares a local `skipPreflight bool` via `addSkipPreflightFlag` and populates `RunContext.SkipPreflight` when delegating to `RunRestart`.
  - [ ] `RunRestart` propagates `ctx.SkipPreflight` to BOTH legs: copy it into the synthesized `StopContext.SkipPreflight` before calling `RunStop`, and leave it set on `ctx` (RunContext) before calling `RunRun`. With this, a single `--skip-preflight` on restart skips preflight for the stop leg AND the run leg.
  - [ ] consider whether restart should ALSO accept stage-specific behavior — answer: no, just propagate the bool. Preflight runs twice (once per leg) using the respective stage, which is correct.
- [ ] preflight output goes to stderr (use `cmd.ErrOrStderr()` at the command boundary; lifecycle entry points already accept writers — extend `RunContext` / `StopContext` with `ErrOut io.Writer` if not already present).
- [ ] write tests:
  - [ ] preflight blocks on error-severity check → deploy aborts WITHOUT acquiring the lock file (assert via `os.Stat` on `.devbox/deploy/deploy.lock`) and WITHOUT calling `EnsureVolumes` (assert via fake docker client / call counter).
  - [ ] preflight blocks on error → run aborts WITHOUT calling git probe.
  - [ ] preflight emits warnings without blocking.
  - [ ] `--skip-preflight` short-circuits without executing any validator (assert via a `type: command` check whose script touches a sentinel file — the file must NOT appear) and prints the `"preflight skipped (--skip-preflight)"` line. Command proceeds normally even when there are error-severity checks (since they didn't run).
  - [ ] `preflightFailedError.ExitCode()` returns 1.
- [ ] run `go test ./internal/command/... ./internal/lifecycle/...` — must pass before Task 8.

### Task 8: Documentation

- [ ] create `docs/reference/config/validate.md` — schema for `validate.yml`, full list of builtins usable in checks (with required `with:` keys for each), worked examples mirroring the chat spec (ghcr-login, db-dump-present, app-secrets, corporate-vpn, project-deps), the `--stage` and `--skip-preflight` flags, the reserved-stages list and open-enum behavior.
- [ ] in `validate.md`, dedicate a prominent section to **"Checks should be idempotent inspection"** — explain the convention that `type: command` checks answer a yes/no readiness question and SHOULD NOT mutate state. State explicitly: the CLI does not enforce this (no sandbox), but enforces non-interactive execution, suppresses notifications, and discards stdout/stderr (only error output reaches diagnostics). User commands invoked from checks are restricted to `type: shell` and `type: script` (workflow/service_*/devbox/builtin types are rejected at load time).
- [ ] update `docs/reference/cli/` by running `devbox docs generate` (the existing CLI doc generator). Verify the new `--skip-preflight` / `--stage` flags appear.
- [ ] update `docs/internals/packages.md` — add subsections for `internal/validate/env/` and `internal/validate/checks/` describing invariants (env probes are hardcoded and config-blind beyond `cfg` binary accessors; checks are loaded with strict decoding and dispatch via builtin/usercommand registries; preflight is invoked from the command boundary and writes to stderr).
- [ ] update `CLAUDE.md` / `AGENTS.md` "Key Patterns" section with a one-paragraph note on preflight and the env/checks domains so future agents pick it up.
- [ ] no tests for docs, but verify rendered markdown displays the tables correctly with a markdown previewer or `glow`.

### Task 9: Final verification

- [ ] verify all behaviors from Overview / spec examples are implemented (run through examples 1–6 by hand on a sample project).
- [ ] verify edge cases: missing `devbox/validate.yml`, empty `checks:`, unknown stage emits info only, `with:` type errors surface at load time with file+line.
- [ ] run `make test` — full suite must pass.
- [ ] run `make lint` — all issues fixed.
- [ ] verify `go test -cover ./internal/builtin/... ./internal/config/... ./internal/validate/...` meets the project's coverage bar (informally check no regressions).

## Technical Details

**Diagnostic mapping (checks domain).** A failing check produces:

```
Severity: entry.Severity (default "error")
Domain:   "checks"
Target:   entry.ID
File:     "devbox/validate.yml"
Line:     entry.SourceLine
Message:  err.Error()          // from builtin/command
Hint:     entry.Hint            // user-authored
```

**Diagnostic mapping (env domain).** Hardcoded per probe; `File` empty, `Hint` contains install / remediation guidance.

**Preflight flow.**

```
load cfg ──► load validateCfg (optional) ──► load cmdRegistry
                            │
                            ▼
         env.All(cfg) + checks.All(validateCfg, baseDir, cmdRegistry)
                            │
                  filter by stage parameter
                            │
                            ▼
                  run sequentially, collect diagnostics
                            │
                    render via existing UI helpers ──► stderr
                            │
              any error-severity? ──► return preflightFailedError (exit 1)
                            │
                            ▼
                       continue with real command
```

**Strict decoding.** `LoadValidateConfig` uses `yaml.Decoder.KnownFields(true)` like `LoadDeployConfig`. Line numbers are captured via `yaml.Node` traversal so diagnostics can point at the offending entry (same pattern as deploy steps).

**Default timeouts.** Defined as constants in each builtin (`shellDefaultTimeout = 10 * time.Second`, `tcpDefaultTimeout = 3 * time.Second`). `with.timeout` overrides per-entry.

**Naming.** Reserve `env.*` (hardcoded) and `checks.*` (declarative) as fixed domain prefixes. The `config.validate` validator (Task 5) is a soft-warning validator on the validate.yml file itself; it lives in the existing `config` domain because that's the convention for "validate the YAML shape" validators.

## Post-Completion

**Manual verification** (do these on a real project before declaring shipped):

- Smoke-test preflight on a fresh machine without compose plugin → confirm `env.docker_compose` blocks with actionable hint.
- Smoke-test `ghcr-login` example: log out → `devbox deploy run` aborts with the documented message; `docker login ghcr.io` → re-run passes.
- Smoke-test `--skip-preflight`: confirm the "would-have-blocked" summary is informative.
- Smoke-test `type: command` bridge with a real user command (`./scripts/check-deps.sh`).
- Visual check: diagnostic table rendering for ~10 mixed env+checks failures is readable, not crowded.

**Repo-internal consumer updates** (live projects in the monorepo):

- Audit existing `devbox/` project configs in sibling repos that should adopt `validate.yml` — at minimum add registry-login checks where appropriate. (Not blocking for the CLI PR; opens follow-up PRs in those projects.)

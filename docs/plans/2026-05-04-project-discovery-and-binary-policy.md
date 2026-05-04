# Project Discovery and Binary Policy

## Overview

Two tightly-coupled changes to make the `devbox` binary safe to install globally and run from any subdirectory of a project:

1. **Project discovery** — replace the literal `"devbox.yml"` default with an upward walk from cwd, plus a strict `schema_version: "2"` gate. Without this, running from subdirs or a globally installed binary is fragile, and any old "devbox" project (legacy v1 layout) gets silently treated as a v2 project.

2. **Binary policy** — add a top-level `binaries:` block to `devbox.yml` (devbox/docker/shell) so projects can override the binaries devbox shells out to. Defaults: `devbox: devbox`, `docker: docker`, `shell: sh`. Today many sites hardcode `./bin/devbox`, `"docker"`, `"sh"`, which breaks both global installs and any project that needs e.g. `podman` or `bash`.

Both pieces share the same prerequisite: a reliable way to locate the project root and to read engine-policy fields *before* the layered config merge.

## Context (from discovery)

**Files involved:**

- Config / project discovery
  - `internal/command/root.go:59-64` — `--config` defaults to literal `"devbox.yml"`
  - `internal/config/devbox.go:16-37` — `DevboxConfig` (already has `SchemaVersion`)
  - `internal/config/devbox.go:422-493` — `LoadConfig` (no schema gating)
  - `devbox.yml` — currently `schema_version: "1"`, will be bumped manually by user
- Hard-coded `./bin/devbox`
  - `internal/pipeline/step.go:60,75` — plan/display output
  - `internal/pipeline/executor.go:28-39` — fallback when `os.Executable()` fails
  - `internal/usercommands/runtime/runner_host.go:15-24` — DevboxRunner fallback
  - `internal/deploy/print.go:35,37` — shell plan emission
  - `internal/deploy/plan_test.go` — test fixtures pinning the literal
- Hard-coded `"docker"`
  - `internal/docker/compose.go:91,182` (Exec, output)
  - `internal/docker/volumes.go:34,44`
  - `internal/usercommands/runtime/runner_service.go:282,294`
  - `internal/command/compose.go:65,200`
  - `internal/command/service_cli.go:201,245,272`
  - `internal/command/service.go:198`
  - `internal/stack/topology.go:27,77,92`
  - `internal/builtin/volumes.go:33,54`
- Hard-coded `"sh"`
  - `internal/pipeline/executor.go:33,64`
  - `internal/usercommands/runtime/runner_host.go:31,67`
  - `internal/usercommands/runtime/runner_service.go:207`
  - Note: `runner_script.go` already supports per-command `script.shell` override; `condition.go` (predicate evaluator) intentionally stays on `sh` for predictability.

**Decisions confirmed:**

- `schema_version` and `binaries` live in top-level `devbox.yml` only (engine policy, no layering).
- Discovery: upward walk from cwd until `devbox.yml` is found, no env-var override.
- Plumbing: `BinariesConfig` carried on `DevboxConfig`, threaded to call sites.
- Existing projects: user bumps `schema_version` manually; no migration tool.

## Development Approach

- **Testing approach**: Regular (code first, then tests).
- Land the two pieces in separate tasks but in one PR — the binary plumbing depends on `LoadConfig` populating `cfg.Binaries`, which depends on the resolver.
- **CRITICAL: every task MUST include new/updated tests for code changes in that task.**
- **CRITICAL: all tests must pass before starting next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` and `make lint` after each task.
- Existing tests that pin `./bin/devbox` literal output get updated in lockstep with the producer (don't touch them in advance).

## Testing Strategy

- **Unit tests**: required for every task. Use table-driven tests where there are multiple cases (resolver edge cases, schema validation, plan-display output, compose binary substitution).
- **Project has no UI/e2e tests** — skip the e2e bullet.
- Manual smoke after the binary plumbing lands: `cd devbox-cli && make build && cd ../services/main 2>/dev/null || cd /tmp && ~/Projects/devbox/next-laravel/bin/devbox` (subdir + non-cwd binary path) — should locate the project and print the summary.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, tests, lint runs.
- **Post-Completion** (no checkboxes): manual smoke testing in this repo, manual `schema_version` bump, doc-reference regeneration.

## Implementation Steps

### Task 1: Add `internal/project` resolver

Goal: a small package with one function used by every command to find `devbox.yml`.

- [x] create `internal/project/project.go` with two-stage API (locate is separable from validate so pre-parse callers like `cmd/devbox/main.go` can silently *find* a project without rejecting legacy v1 schemas — the schema error itself wants to be styled by Fang):
  - `type Resolved struct { ConfigPath, Root string }`
  - `Locate(flag string) (Resolved, bool, error)` — pure discovery, no schema check. **Two distinct modes**:
    - **Explicit mode** (`flag != ""`): the user named a file, so missing/unreadable is a hard error. Stat the path; on `ErrNotExist` return `(zero, false, fmt.Errorf("config file %s: %w", flag, err))` — wrap the underlying error so callers can distinguish via `errors.Is(err, os.ErrNotExist)`. On other stat errors return `(zero, false, err)`. On success → `(resolved, true, nil)`.
    - **Discovery mode** (`flag == ""`): walk up from `os.Getwd()`; first `devbox.yml` wins → `(resolved, true, nil)`. No file found anywhere → `(zero, false, nil)` (not an error — the absence of a project file during upward discovery is a normal state, not a failure). Stat failures other than `ErrNotExist` during the walk → `(zero, false, err)`.
  - `ValidateSchema(path string) error` — read just `schema_version` (use a tiny YAML-decode struct, not `LoadConfig`, to keep early errors clean). Allowed: `"2"`. Otherwise return `legacy devbox project detected at <path>; this Devbox supports schema_version: "2" only` (or a clear "missing schema_version" variant).
  - `Resolve(flag string) (Resolved, error)` — convenience wrapper composing the two:
    - call `Locate(flag)`. If it returned an error, return that error verbatim (callers see the wrapped `os.ErrNotExist` for explicit-and-missing).
    - if `found == false` (only possible in discovery mode), return `ErrNotFound` (sentinel — wrapped with cwd for context).
    - otherwise call `ValidateSchema(path)` and return its result.
  - **Net behavior**: `devbox -c /bad/path info` produces `config file /bad/path: file does not exist` (specific, actionable). `devbox info` from `/tmp` produces `no devbox.yml found in /tmp or any parent directory` (`ErrNotFound`, eligible for the allowlist fallback). Allowlisted commands check `errors.Is(err, ErrNotFound)` — they do **not** swallow stat errors from explicit `-c` paths.
- [x] expose constants for the schema field name (`SchemaField`), supported version (`SupportedSchema = "2"`), and config filename (`ConfigFilename = "devbox.yml"`) so tests/import can reference them.
- [x] write tests in `internal/project/project_test.go`:
  - `Locate` explicit mode: existing path → `(resolved, true, nil)`; nonexistent path → `(_, false, err)` where `errors.Is(err, os.ErrNotExist)` is true and the message names the bad path.
  - `Locate` discovery mode: project at cwd → found; project two levels up → found; no project anywhere → `(_, false, nil)`; cwd is the filesystem root → `(_, false, nil)`. Locate must succeed even when the file has `schema_version: "1"` (validation is a separate step).
  - `ValidateSchema`: v2 file → nil; v1 file → legacy error; missing schema → clear error; unreadable file → wrapped error.
  - `Resolve` composition:
    - explicit good path + v2 → success.
    - explicit good path + v1 → schema error (not ErrNotFound).
    - explicit bad path → wrapped `os.ErrNotExist`; `errors.Is(err, ErrNotFound)` is **false** (so allowlisted commands don't swallow it).
    - discovery, no project → `ErrNotFound`; `errors.Is(err, ErrNotFound)` is **true**.
    - discovery, found legacy v1 → schema error (not ErrNotFound).
- [x] `make test` — must pass before next task.

### Task 2: Wire resolver into `cmd/devbox/main.go` (pre-parse, silent)

`cmd/devbox/main.go` runs *before* cobra parses flags and *before* `PersistentPreRunE`. It currently pre-parses `--config` (default `"devbox.yml"`) to find `devbox/styles.yml` for early Fang help colors (see [main.go:38-42, 58-65, 70-72](/Users/s/Projects/devbox/next-laravel/devbox-cli/cmd/devbox/main.go)). When the binary is invoked from a subdir or globally, this misses `styles.yml` even when the project is right there.

This stage must be **silent** under all failure modes — Fang's help/version output runs even with no project, and a missing/unreadable `styles.yml` already falls back to Fang defaults today. Schema validation is not the right concern here either: a legacy v1 project should still get its colored error in Task 3, so the pre-parse must locate the file regardless of schema.

- [x] update `configPathFromArgs` to also detect whether `--config` was actually supplied (return `(path string, explicit bool)`). Keep using `pflag` since cobra hasn't run yet.
- [x] in `loadHelpColorScheme`, replace the literal `filepath.Dir(configPath)` derivation with a `project.Locate` call:
  - if explicit → `project.Locate(configPath)`; else → `project.Locate("")` (walks upward from cwd).
  - if `Locate` returns `found=false` or any error → silently return nil (no color override; Fang defaults apply). Do **not** validate schema here.
  - if found → resolve `devbox/styles.yml` against `resolved.Root`. The existing "no styles file → return nil" path stays unchanged.
- [x] write tests in `cmd/devbox/main_test.go` (or a new file if none exists):
  - subdir invocation finds `devbox/styles.yml` two levels up.
  - no project anywhere → `loadHelpColorScheme` returns nil silently (no panic, no log).
  - explicit `--config /tmp/foo/devbox.yml` resolves styles relative to `/tmp/foo/devbox/styles.yml`.
  - legacy v1 project → still locates and loads `styles.yml` (so the eventual schema error from Task 3 gets colored).
- [x] `make test` — must pass before next task.

### Task 3: Wire resolver into root command

- [ ] add `projectRoot string` to `rootFlags` in `internal/command/root.go`.
- [ ] convert `PersistentPreRun` to `PersistentPreRunE`. Resolve the project before any subcommand:
  - detect whether `--config/-c` was actually supplied. **Important**: Cobra calls a root `PersistentPreRunE` with the *leaf* command (e.g. for `devbox info`, `cmd` is the `info` command, not root). The persistent flag is defined on root ([root.go:59](/Users/s/Projects/devbox/next-laravel/devbox-cli/internal/command/root.go)), so `cmd.Flags().Lookup("config")` works only because Cobra's flag inheritance merges persistent flags into leaf flag sets — but to be unambiguous and robust, look it up on root: `cmd.Root().PersistentFlags().Lookup("config").Changed`. Capture into a local `explicit bool`. Do **not** rely on string comparison against `"devbox.yml"` — a user passing `--config devbox.yml` explicitly must be treated as an explicit path, not as the default sentinel.
  - if `explicit` → `project.Resolve(flags.configPath)`; else → `project.Resolve("")` (upward walk).
  - on success, set `flags.configPath` to the absolute resolved path and `flags.projectRoot` to `filepath.Dir(...)`.
  - on legacy/schema error, return the error (cobra surfaces it via fang).
  - on error: distinguish discovery miss from explicit-bad-path. Use `errors.Is(err, project.ErrNotFound)` — only that sentinel triggers the allowlist fallback. Any other error (including the wrapped `os.ErrNotExist` from an explicit `-c /bad/path`) is fatal regardless of which subcommand was invoked.
  - on `project.ErrNotFound`: most subcommands need the project, but `version`, `completion`, `print`, and root with no args (`runRoot`) should still work without a project. Tag those commands via a small allowlist so the discovery miss is not fatal for them — print a hint via `runRoot` like today's "config not found, skipping summary". `docs generate` is **not** in the allowlist by default (see next bullet).
  - `docs generate`: keep project-bound by default (it resolves output relative to `projectRoot` and `--scope all` loads the command registry — see [docs.go:75,126](/Users/s/Projects/devbox/next-laravel/devbox-cli/internal/command/docs.go)). For the "no project" case, allow only `--scope cli`: detect the no-project state in the docs runner and reject `--scope all`/`--scope commands` with a clear error ("commands scope requires a devbox project; use --scope cli to generate CLI reference docs without a project"). When `--scope cli` is used without a project, default `--output` to cwd instead of `projectRoot`.
- [ ] update `runRoot` and `applyStyles` to use `flags.projectRoot` instead of `filepath.Dir(flags.configPath)`.
- [ ] sweep `internal/command/*.go` for callers that compute paths from `flags.configPath` or use `os.Getwd()` for the project root: replace with `flags.projectRoot`. (Likely sites: `deploy.go`, `reset.go`, `run.go`, `stop.go`, `restart.go`, `services.go`, `tools.go`, `commands run`, `render env`, `render ide`, `info`, `status`, `shell`, `up`, `down`, `logs`, `ps`, `wait`, `compose`, `docker`.)
- [ ] write/update tests:
  - new `internal/command/root_resolver_test.go` (or extend `root_test.go`) with table-driven cases that build a temp dir tree, chdir into a subdir, and verify `flags.configPath`/`flags.projectRoot` after `PersistentPreRunE` runs.
  - explicit `-c` with a relative path resolves correctly when invoked from a different cwd.
  - explicit `-c devbox.yml` (matching the default value) is treated as explicit — the resolver does **not** walk upward in that case. Pin via `cmd.Root().PersistentFlags().Lookup("config").Changed`.
  - **explicit bad path is always fatal**: `devbox -c /bad/path version` (an allowlisted command) must still fail with the wrapped `os.ErrNotExist` error, not silently continue. The allowlist only catches `project.ErrNotFound`.
  - allowlisted commands (`version`, `completion`, `print`) succeed with no project (no `-c` flag, discovery miss); `docs generate --scope cli` succeeds with no project; `docs generate --scope all` and `--scope commands` fail with a clear error when no project.
- [ ] `make test` — must pass before next task.

### Task 4: Wire resolver into completion helpers (`ValidArgsFunction`)

Cobra's hidden `__complete` command path runs `ValidArgsFunction` callbacks **without** calling root `PersistentPreRunE` first — so any completion that calls `config.LoadConfig(flags.configPath)` directly sees the literal `"devbox.yml"` (or whatever raw `-c` value the user typed) and silently returns no completions when invoked from a subdir or with a global binary.

Sites confirmed today (verified by grep — file/line accuracy matters because the helper landing in the wrong file is silently a no-op):

- `internal/command/deploy.go:199-222` — `deploy step` step-address completion calls `config.LoadConfig(flags.configPath)` directly.
- `internal/command/reset.go` — `reset step` mirrors deploy; sweep for the same pattern.
- `internal/command/service.go:304, 344` — `services enable` / `services disable` use `optionalServiceNameCompletion(flags)` (defined in the same file around lines 54/88), which calls `LoadConfig(flags.configPath)`. Also `service.go:378` (other service-name completion site).
- `internal/command/tools.go:195, 235` — `tools enable` / `tools disable` use `toolNameCompletion` (defined at `tools.go:309-310`). This callback is in `tools.go`, **not** `service.go` — separate file, separate edit.
- `internal/command/command_cmd.go` — `commands inspect` / `commands run` ID completion (test refs at `completion_test.go:262, 270`); sweep for the registry load.
- `internal/command/shell.go` (or wherever `newShellCmd` lives) — `devbox shell <service>` ValidArgsFunction (test ref at `completion_test.go:278-282`).
- final pass: `grep -rn 'ValidArgsFunction' internal/command/` and audit each callback for direct `flags.configPath` / `LoadConfig` usage.

- [ ] add a small completion helper in `internal/command/` (e.g. `completionConfigPath(flags *rootFlags, cmd *cobra.Command) (string, string, error)`) that:
  - if `flags.configPath` is already populated *and* `flags.projectRoot` is set (e.g. `PersistentPreRunE` ran for some reason), returns them as-is.
  - otherwise calls `project.Resolve` (not `Locate`) using the same explicit/default detection as Task 3 (`cmd.Root().PersistentFlags().Lookup("config").Changed`). **Important**: `project.Locate` does not validate schema, so using `Locate` alone here would let `__complete` happily load and resolve completions for an unsupported legacy v1 project (silently giving the user the impression that the project works). `Resolve` composes `Locate` + `ValidateSchema` and is the right choice — a v1 project produces the legacy schema error, which the helper drops on the floor (returning empty completions). If you really want the no-validation variant for some narrow reason, use `Locate` + an explicit `project.ValidateSchema(path)` call before returning the path.
  - on `Resolve` returning `errors.Is(err, project.ErrNotFound)` (discovery miss): return empty strings + the sentinel so the caller can render no-completions silently.
  - on schema error or any other resolve error (including the wrapped `os.ErrNotExist` from an explicit `-c /bad/path`): return empty strings + that error. Completion callbacks drop the error and return `cobra.ShellCompDirectiveNoFileComp` (no spam in the user's terminal during tab-complete; legacy projects simply yield no suggestions, which surfaces the problem the next time the user runs the actual command and gets the colored schema error).
- [ ] update every `ValidArgsFunction` callback to call the helper before any config-dependent work:
  - `deploy.go:199-222`
  - `reset.go` (sweep — likely the same pattern as deploy)
  - `service.go` (`optionalServiceNameCompletion` and any direct `LoadConfig` callers — lines 54, 88, 304, 344, 378)
  - `tools.go` (`toolNameCompletion` at line 309 + the two `ValidArgsFunction:` registrations at 195, 235) — distinct file from `service.go`.
  - `command_cmd.go` for `commands inspect` / `commands run` ID completion (registry load).
  - `shell.go` for `devbox shell <service>`.
  - any other site found via `grep -rn 'ValidArgsFunction' internal/command/`.
- [ ] write tests in `internal/command/completion_test.go`:
  - simulate the `__complete` path by invoking the `ValidArgsFunction` directly with a chdir into a subdir of a temp v2 project; assert completions are produced (subdir resolution works for tab-complete).
  - same callback with `os.Chdir("/tmp")` (no project anywhere): assert no completions, no panic, no error spew on stderr.
  - explicit `-c /bad/path` for completion: assert empty completions, no terminal noise.
  - **legacy schema gating**: chdir into a subdir of a temp project whose `devbox.yml` has `schema_version: "1"`; assert the completion returns no completions (the helper drops the schema error). This prevents legacy projects from silently appearing functional through tab-completion. Add the same case for `tools enable`/`services enable` callbacks so the behavior is uniform across files.
- [ ] `make test` — must pass before next task.

### Task 5: Add `BinariesConfig` to the config layer

- [ ] in `internal/config/devbox.go`:
  - define `type BinariesConfig struct { Devbox, Docker, Shell string }` with `yaml:"devbox"`, `yaml:"docker"`, `yaml:"shell"` tags.
  - add `Binaries BinariesConfig` to `DevboxConfig`.
  - add `applyBinariesDefaults(*BinariesConfig)` — fills empty fields with `devbox`, `docker`, `sh`.
  - **Pinned ordering inside `LoadConfig`** (the merged-config code path already exists; this lists where each step slots in):
    1. perform the existing 3-layer `deepMerge` to produce `merged map[string]any`.
    2. `yaml.Marshal(merged)` + `yaml.Unmarshal` into `var cfg DevboxConfig` (existing flow). At this point `cfg.Binaries` holds whatever the merge produced — which we are about to overwrite, so do not rely on it.
    3. parse the **top-level `devbox.yml`** standalone into a small view: `var topView struct { Binaries BinariesConfig \`yaml:"binaries"\` }; yaml.Unmarshal(devboxBytes, &topView)`. (Re-read the file — `loadRawYAML` already opens it once; reuse the bytes if convenient.)
    4. `cfg.Binaries = topView.Binaries` (replaces whatever the merged unmarshal produced — even if `defaults.yml`/`local.yml` set `binaries:`, this assignment wins).
    5. `applyBinariesDefaults(&cfg.Binaries)` — fills any field still empty after the top-level read.
    6. assign `cfg.Raw = merged` (existing flow).
    7. **normalize `cfg.Raw["binaries"]`**: overwrite with `map[string]any{"devbox": cfg.Binaries.Devbox, "docker": cfg.Binaries.Docker, "shell": cfg.Binaries.Shell}`. Any `binaries:` block from layered files is silently discarded from `Raw` so dot-path lookups (`${binaries.docker}` in template/export rules) read the same effective values as Go callers.
  - rationale for the order: standalone read happens *after* the merged unmarshal so the assignment cleanly overwrites; defaults applied *after* the assignment so a partial top-level (`binaries: { docker: podman }`) still gets `devbox: devbox`/`shell: sh` defaults; Raw normalization *after* defaults so templates see the exact same string as `cfg.Binaries.*`.
  - document the field with a doc comment that names the ordering and explains "engine policy, not layered."
- [ ] add nil/zero-value-safe accessors in `internal/config/devbox.go` so call sites never have to check for `nil` cfg or empty fields:
  - `func DevboxBin(cfg *DevboxConfig) string` — returns `cfg.Binaries.Devbox` if non-empty, else `"devbox"`. Handles `cfg == nil`.
  - `func DockerBin(cfg *DevboxConfig) string` — same pattern, default `"docker"`.
  - `func ShellBin(cfg *DevboxConfig) string` — same pattern, default `"sh"`.
  - All three are the **only** way the rest of the codebase reads binary names. Tasks 6–8 use these accessors instead of dereferencing `cfg.Binaries.*` directly. This protects test fixtures that build `DevboxConfig{}` manually and runtime paths where `RunContext.Config` may be nil.
- [ ] write tests in `internal/config/devbox_test.go`:
  - all three keys defaulted when omitted (both `cfg.Binaries.*` and `cfg.Raw["binaries"]` reflect defaults).
  - explicit overrides preserved (`binaries: { devbox: my-devbox, docker: podman, shell: bash }`); `cfg.Raw["binaries"]` matches.
  - `defaults.yml` containing a `binaries:` block is *ignored*: even when `defaults.yml` sets `binaries.docker: podman`, the resulting `cfg.Binaries.Docker` and `cfg.Raw["binaries"].(map[string]any)["docker"]` are the top-level value (or default if top-level omits it). Same check for `local.yml`.
  - partial override: only `docker:` set in top-level → other two get defaults; Raw mirrors the effective values.
  - accessor tests: `DevboxBin(nil) == "devbox"`, `DockerBin(&DevboxConfig{}) == "docker"`, `ShellBin(&DevboxConfig{Binaries: BinariesConfig{Shell: "bash"}}) == "bash"`.
- [ ] `make test` — must pass before next task.

### Task 6: Route Docker binary through `BinariesConfig`

Goal: every `exec.Command("docker", ...)` becomes `exec.Command(config.DockerBin(cfg), ...)`. Always go through the accessor — never read `cfg.Binaries.Docker` directly.

- [ ] `internal/docker/compose.go`:
  - add `Bin string` field to `Compose` struct.
  - `NewCompose(cfg, dockerCfg)` populates `Bin` via `config.DockerBin(cfg)` (which handles nil cfg and empty fields).
  - **also** add a method `func (c *Compose) BinName() string { if c == nil || c.Bin == "" { return "docker" }; return c.Bin }`. Use it everywhere the struct field is read internally (line 91, 97, 182). This way any direct `&docker.Compose{...}` literal construction (test fixtures, the `RunContext.Compose()` minimal path — see [runner.go:188-200](/Users/s/Projects/devbox/next-laravel/devbox-cli/internal/usercommands/runtime/runner.go)) cannot produce `exec.Command("")`.
- [ ] `internal/usercommands/runtime/runner.go` — `RunContext.Compose()` minimal-construction branch (when `DockerConfig` or `Config` is nil):
  - explicitly set `c.Bin = config.DockerBin(ctx.Config)` on the literal `&docker.Compose{...}` — even though `BinName()` would handle the empty case, setting it here keeps observability consistent (anyone inspecting `c.Bin` sees the right value).
  - add a regression test: `RunContext{Config: nil, DockerConfig: nil}.Compose().BinName() == "docker"`; `RunContext{Config: &DevboxConfig{Binaries: BinariesConfig{Docker: "podman"}}, DockerConfig: nil}.Compose().BinName() == "podman"`.
- [ ] `internal/docker/volumes.go`:
  - existing helpers take a `Compose` already? Verify and either thread `*Compose` or pass a `bin string` argument. (Read the file in this task.) Replace lines 34, 44 — call site uses `config.DockerBin(cfg)` when no `Compose` is in scope.
- [ ] `internal/builtin/volumes.go`:
  - replace lines 33, 54 with `config.DockerBin(ctx.Config)` (the builtin has `*config.DevboxConfig` via `ExecContext`, but the accessor protects against nil).
- [ ] `internal/stack/topology.go`:
  - functions take a config or compose; thread the binary through via `config.DockerBin(cfg)` (replace lines 27, 77, 92).
- [ ] `internal/usercommands/runtime/runner_service.go`:
  - replace lines 282, 294 with `compose.BinName()` (handles nil-compose and empty-Bin defensively; `compose` is in scope already).
- [ ] `internal/command/compose.go`, `service_cli.go`, `service.go`:
  - swap the literals; use **`config.DockerBin(cfg)` or `compose.BinName()` only** — never the raw `compose.Bin` field. The whole point of adding `BinName()` is to keep `exec.Command("")` impossible even when a caller hands us a zero-value `Compose`. Direct field reads bypass that guard.
- [ ] update tests:
  - `compose_test.go`: assert `Compose.BinName()` returns `docker` for `&Compose{}`, `&Compose{Bin: ""}`, and `nil`; returns `Bin` when set; `NewCompose(cfg, dockerCfg)` populates `Bin` from `cfg.Binaries.Docker` (and defaults when omitted).
  - regression test (new): the no-`Bin` literal construction path — exercise `RunContext.Compose()` with nil `DockerConfig` and confirm `BinName()` is non-empty (no `exec.Command("")` possible).
  - add tests in `stack`, `builtin/volumes`, and `docker/volumes` for binary substitution where feasible (table-driven on a `bin string` parameter).
- [ ] `make test` and `make lint` — must pass before next task.

### Task 7: Route shell binary through `BinariesConfig`

All call sites read the shell via `config.ShellBin(cfg)` — never `cfg.Binaries.Shell` directly — so nil cfgs and zero-value structs in tests stay safe.

- [ ] `internal/pipeline/executor.go`:
  - `buildDevboxCmd` gains a `shell string` param (callers pass `config.ShellBin(cfg)`). Replace literal `"sh"` at lines 33, 64.
- [ ] `internal/usercommands/runtime/runner_host.go`:
  - `DevboxRunner.Run` (line 31) and `HostRunner.BuildCommand` (line 67): replace `"sh"` with `config.ShellBin(ctx.Config)`. Verify `RunContext` carries `*config.DevboxConfig`; if not, add the field (see existing `runtime/types.go`).
- [ ] `internal/usercommands/runtime/runner_service.go:207`:
  - replace with `config.ShellBin(ctx.Config)`.
- [ ] do **not** touch `internal/usercommands/runtime/runner_script.go` (per-command `script.shell` is the user-facing knob there) or `internal/condition/condition.go` (predicate evaluator should stay on `sh` for predictability — leave a one-line comment noting why).
- [ ] update tests:
  - `pipeline/executor_test.go`: verify the shell name is taken from `cfg.Binaries.Shell` (assert via `cmd.Path` or via a buildable variant of `buildDevboxCmd`).
  - `runner_host_test.go`, `runner_service_test.go`: same pattern.
- [ ] `make test` and `make lint` — must pass before next task.

### Task 8: Route devbox binary through `BinariesConfig`

Goal: replace `./bin/devbox` literals; nested devbox calls use `os.Executable()` then fall back to the accessor; plan display uses the *configured* name.

- [ ] `internal/pipeline/executor.go:28-39` (`buildDevboxCmd`):
  - keep `os.Executable()` as the *runtime* preference (so a nested call uses the same binary that's currently running) but replace the `"./bin/devbox"` fallback with `config.DevboxBin(cfg)`.
- [ ] `internal/usercommands/runtime/runner_host.go:23` (`DevboxRunner.Run`):
  - same pattern: `os.Executable()` first, then `config.DevboxBin(ctx.Config)`.
- [ ] `internal/pipeline/step.go:52-79` (`StepCommand`):
  - **plan display** is purely informational — it should print `binaries.devbox` (the *configured* name a user will see in their shell), not the absolute path of the currently-running binary. Change signature: `StepCommand(step config.DeployStep, devboxBin string) string`. Update callers.
- [ ] `internal/pipeline/print.go` (`PrintPlanTable`):
  - `PrintPlanTable` calls `StepCommand` internally (line 54) and currently has no path to a configured binary. Change signature: `PrintPlanTable(steps []ResolvedStep, w *render.Writer, devboxBin string)` and forward the binary into the internal `StepCommand` call.
  - update both deploy/reset table-output call sites (likely in `internal/command/deploy.go` `runDeployPlan` and `internal/command/reset.go` `runResetPlan`) to pass `config.DevboxBin(cfg)`.
- [ ] `internal/deploy/print.go:35,37` (`PrintPlanShell`):
  - signature change: `PrintPlanShell(steps []pipeline.ResolvedStep, w io.Writer, devboxBin string)`. Emit `<bin> deploy step ...` instead of `./bin/devbox deploy step ...`. Forward `devboxBin` into the internal `pipeline.StepCommand` call.
  - update callers in `internal/command/deploy.go` and `internal/command/reset.go` to pass `config.DevboxBin(cfg)`.
- [ ] update / add tests:
  - `internal/pipeline/step_test.go` (new or extended): `StepCommand` with `Devbox: "docker down"` and `bin: "devbox"` → `"devbox docker down"`; with `bin: "/usr/local/bin/devbox"` → uses that path.
  - `internal/pipeline/print_test.go` (new): table-driven `PrintPlanTable` test that pins binary substitution in the rendered output.
  - `internal/deploy/plan_test.go`: update fixtures from `./bin/devbox` to `devbox`. (Deliberate test churn.)
  - `internal/deploy/print_test.go`: same churn; add a case asserting `PrintPlanShell(..., "podman-devbox")` produces `podman-devbox deploy step ...`.
- [ ] `make test` and `make lint` — must pass before next task.

### Task 9: Verify acceptance criteria

- [ ] targeted grep for residual hard-coded executions — broad string greps would flag legitimate help text, error messages, and YAML keys, so check only actual `exec.Command` call sites:
  - `grep -rn 'exec.Command("docker"' internal/ cmd/` — must be empty in non-test files. (Note: `docker` as a *YAML key*, in cobra `Use:` strings, in error messages, and in the `internal/command/docker.go` subcommand wiring is fine — those are not binary invocations.)
  - `grep -rn 'exec.Command("sh"' internal/ cmd/` — non-test hits limited to the documented exceptions: `internal/condition/condition.go` (predicate evaluator) and `internal/usercommands/runtime/runner_script.go` (per-command shell knob). Any other hit is a regression.
  - `grep -rn '"\./bin/devbox"' internal/ cmd/` — must be empty everywhere except `_test.go` fixtures that have been deliberately kept (none expected after Task 8).
  - bonus targeted searches: `grep -rn 'exec.Command(.[^c]' internal/ cmd/` (catches anything other than `c.Bin`/`config.*Bin(...)` patterns) — visually inspect.
- [ ] `cd devbox-cli && make build && make test && make lint` — all green.
- [ ] regenerate reference docs: `./bin/devbox docs generate` (project root) — review the diff (config-reference may pick up `binaries:`).
- [ ] update `devbox-cli/AGENTS.md` (and via symlink, `CLAUDE.md`) and the top-level `CLAUDE.md` if either documents the resolver or binary defaults — currently `CLAUDE.md` describes config layers; add a sentence about `schema_version` gating and `binaries` policy.

### Task 10: [Final] Documentation pass

- [ ] update `docs/reference/config/` if generated content didn't cover the new `binaries:` schema key — add a short snippet explaining defaults and that the block is read pre-merge.
- [ ] note in the top-level `CLAUDE.md` (Config section) that `devbox.yml` now requires `schema_version: "2"` and supports a top-level `binaries:` block (engine policy, not layered).

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

### `internal/project` package shape

```go
package project

const (
    SchemaField    = "schema_version"
    SupportedSchema = "2"
    ConfigFilename  = "devbox.yml"
)

type Resolved struct {
    ConfigPath string // absolute
    Root       string // filepath.Dir(ConfigPath)
}

var ErrNotFound = errors.New("no devbox.yml found in cwd or any parent directory")

func Resolve(flag string) (Resolved, error)
```

`Resolve("")` walks from `os.Getwd()` upward; `Resolve(path)` uses the explicit path (relative is resolved against cwd). Both validate the schema before returning.

### `BinariesConfig` shape

```go
type BinariesConfig struct {
    Devbox string `yaml:"devbox"`
    Docker string `yaml:"docker"`
    Shell  string `yaml:"shell"`
}
```

Defaults: `devbox`, `docker`, `sh`. Read from top-level `devbox.yml` only (not merged with `defaults.yml` / `local.yml`).

### Threading rules

- **DevboxConfig is the carrier.** Every subsystem that already takes `*config.DevboxConfig` (compose, runners, builtins) goes through `config.DockerBin / ShellBin / DevboxBin` accessors — never `cfg.Binaries.*` direct field reads. Accessors are nil-safe and fill defaults, so `DevboxConfig{}` test fixtures and `RunContext{Config: nil}` paths don't panic and don't produce empty-string `exec.Command("")` calls.
- **`docker.Compose.Bin` is private-by-convention.** All reads go through `Compose.BinName()`, never the field. The accessor is nil/empty safe so any literal `&docker.Compose{...}` (test fixtures, `RunContext.Compose()` minimal branch) is also safe.
- **Helpers without cfg get a `bin string` parameter** (small, focused change at call sites that already have cfg in scope one frame above; the upper frame calls the accessor).
- **Plan display takes `devboxBin string`** explicitly so cobra commands can pass `config.DevboxBin(cfg)`, and tests can pin output without globals. Both `pipeline.StepCommand`, `pipeline.PrintPlanTable`, and `deploy.PrintPlanShell` take this parameter.

### Execution preference for the devbox binary

When *executing* a nested devbox call, prefer `os.Executable()` (the path of the currently-running binary) over the configured name — this ensures nested calls stay self-consistent during pipelines. Use the configured name only as a fallback when `os.Executable()` errors (rare) and as the *displayed* name in plans / shell scripts.

## Post-Completion

**Manual verification:**

- Smoke test in this repo after manually bumping `devbox.yml` to `schema_version: "2"`:
  - `cd next-laravel && ./bin/devbox` — summary renders.
  - `cd next-laravel/services && ../bin/devbox info` — resolves project from a subdir.
  - `cd /tmp && /Users/s/Projects/devbox/next-laravel/bin/devbox` — fails clearly with "no devbox.yml found".
  - `cd next-laravel && cp devbox.yml /tmp/legacy.yml && sed -i '' 's/"2"/"1"/' /tmp/legacy.yml && ./bin/devbox -c /tmp/legacy.yml info` — fails with the legacy schema error.
  - `cd next-laravel && ./bin/devbox deploy plan` — plan output uses `devbox` (or whatever `binaries.devbox` is set to), not `./bin/devbox`.
- Override smoke: temporarily add `binaries: { docker: podman }` to `devbox.yml` and run `./bin/devbox docker ps` — should fail with `exec: "podman": executable file not found in PATH` (proves substitution is wired).

**External system updates:**

- Anyone running this devbox binary outside the next-laravel repo will need their own `devbox.yml` with `schema_version: "2"` — call this out in the PR description.

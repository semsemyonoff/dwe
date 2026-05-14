# validate-and-top-level-command-refactor

## Overview

Refactor `devbox` top-level command surface and add a new top-level `devbox validate` command that statically checks all project YAML, declarative command files, and IDE/AI template packs and reports collected diagnostics in a single Lipgloss table.

Three changes ship together:

1. **Move container health-wait into a builtin**. Today the same polling loop is wired as two public CLI commands (`devbox wait` in `internal/command/wait.go`, `devbox docker wait` in `internal/command/docker.go`) and uses unexported helpers in `internal/command/compose.go`. Extract the helpers into the `internal/docker` domain package, register a new engine builtin `docker_wait_healthy` (used from pipeline YAML with `with: { timeout, interval, services }`), and hard-remove both public CLI commands.
2. **Remove top-level Docker passthroughs**. `devbox up`, `devbox down`, `devbox logs`, `devbox ps` are 5-line aliases over `devbox docker {up,down,logs,ps}`. Drop them from root; the `devbox docker *` group remains the supported path.
3. **Introduce `devbox validate`** as the single static-validation entry point. The asymmetric `devbox reset config check` is replaced by `devbox validate config reset`. The command groups diagnostics by domain (`config`, `templates`, `commands`) and renders a `STATUS / TARGET / FILE / MESSAGE / HINT` table instead of failing fast. Severity-gated exit code (errors → non-zero; warnings via `--strict`).

**No backward compatibility.** Removed commands are deleted outright — matches the established precedent (`devbox deploy config` / `devbox deploy config-check` were removed wholesale per `AGENTS.md`).

**Sequencing note.** This plan executes after `docs/plans/2026-05-13-tools-as-map-and-generic-runtime.md`. By the time work starts:

- `ToolsConfig` is `map[string]ToolConfig`; `RuntimePorts` / `RuntimeHosts` are `map[string]int` / `map[string]string`.
- `ComposeConfig.Overlays` is gone; per-tool overlay lives at `tools.<name>.compose`.
- `LoadConfig` runs two new private validators (`validateConfigKeys`, `detectLegacyComposeOverlays`) after the 3-layer merge.
- Identifier-safe key validation (`^[A-Za-z_][A-Za-z0-9_]*$`) applies to `tools`, `runtime.ports`, `runtime.hosts`.

The validate command must surface those load-time errors as diagnostics (not bypass them) and must enumerate tools via the map shape. Where this matters, individual tasks call it out explicitly.

## Context (from discovery)

Files/components involved:

- `internal/command/wait.go` — `newWaitCmd` (top-level `devbox wait`)
- `internal/command/docker.go:229` — `newDockerWaitCmd` (`devbox docker wait`)
- `internal/command/compose.go:158-216` — private health helpers (`healthGetFn`, `waitContainersHealthy`, `dockerHealthStatus`)
- `internal/command/up.go`, `down.go`, `logs.go`, `ps.go` — top-level aliases over `devbox docker *`
- `internal/command/reset.go:207-238` — `newResetConfigCheckCmd` (sole surviving `config check` command)
- `internal/command/root.go:125-131` — wires top-level commands
- `internal/builtin/builtin.go` — `Builtin` interface, `ExecContext`, registry
- `internal/builtin/dirs_ensure.go` / `volumes.go` / `paths.go` — existing builtin patterns to mirror (`Validate` / `Describe` / `Run`)
- `internal/docker/compose.go` — `Compose`, `NewCompose`, `ContainerIDs()`; natural home for health helpers
- `internal/config/devbox.go` — loaders: `LoadConfig`, `LoadServicesConfig`, `LoadLifecycleConfig`, `LoadDeployConfig`, `LoadResetConfig`, `LoadServiceDeployConfigs`
- `internal/config/docker.go:114` — `LoadDockerConfig`
- `internal/config/info.go:149` — `LoadInfoConfig`
- `internal/config/styles.go:63` — `LoadStylesConfig`
- `internal/usercommands/loader/loader.go:15` — `DiscoverCommandFiles`, `LoadCommandFile`
- `internal/usercommands/registry/` — `LoadRegistry` for cross-ref validation
- `internal/command/render_ai.go:117` — `loadAgentsManifest`, `validateAgentsManifest`
- `internal/command/ide.go:426` — `walkIDEPack`, `validateIDETemplateKey`, `resolveIDETemplatePack`
- `internal/ui/table.go` — `RenderServiceTable`, `RenderToolTable` patterns to mirror
- `internal/render/` — `Writer` for the `--quiet`/`--strict` summary lines
- `docs/reference/cli/devbox_validate*.md` — new
- `docs/reference/cli/devbox_{up,down,logs,ps,wait}.md` and `docs/reference/cli/devbox_reset_config*.md` — to be deleted
- `docs/reference/cli/devbox_docker_wait.md` — to be deleted

Related patterns found:

- Existing builtins keep state out of the struct (`type dockerRemoveProjectVolumesBuiltin struct{}`) and read params via `getStringParam`/`getStringSlice`; we'll add a `getDurationParam` helper and mirror the pattern.
- `internal/docker.Compose.ContainerIDs()` already exists — the builtin needs a `Compose` instance, which it can build from `ctx.Config` plus a `LoadDockerConfig` call.
- `internal/ui/table.go` already wires lipgloss tables with `BorderStyle` / header styling; `RenderDiagnosticsTable` will follow the same shape.
- `lipgloss/table` is imported by `internal/ui`; no new dependency.

Dependencies identified:

- `Compose.ContainerIDs()` works against the **current** active overlays (`ComposeFiles()`). The builtin must not silently switch to `ComposeFilesAll()` — pipelines only know about the currently-enabled stack.
- `dockerHealthStatus` shells out to `docker inspect`; portable for `podman` too because the format string uses Go-template syntax that both back-ends honor. Keep using `config.DockerBin(cfg)`.
- `waitContainersHealthy` writes warnings/successes to `*render.Writer`. To keep the helper reusable from a non-CLI context (the builtin runs inside the pipeline which already has a `*render.Writer` in `ExecContext.Output`), the extracted function signature passes `*render.Writer` through unchanged. No new abstraction needed.
- After the tools-as-map plan, `Compose` constructors read `cfg.Tools` and `cfg.Compose.Base`. No change to wait-helper extraction caused by that refactor (it's purely about container IDs / health, not overlays).
- **`LoadConfig` is fail-fast across files**, not just within `devbox.yml`. It reads `devbox/services.yml` at `internal/config/devbox.go:593` and `devbox/deploy.yml` at `internal/config/devbox.go:616` as part of the same load and returns the first error wholesale. So a broken `deploy.yml` makes `LoadConfig` return `cfg == nil` and any "skip if cfg is nil" design would mask the real `deploy` error behind a generic `devbox` failure. **Each config validator must do its own file load** (its own `LoadDeployConfig` / `LoadServicesConfig` / `LoadResetConfig` / …). The `Context` carries `ProjectRoot` so validators can locate their own files; `Context.Cfg` is the optional fully-merged config used only by validators that need cross-file resolution (e.g., `ResolvePlan` needs the full devbox merge so it can resolve service deploy file paths). When `Context.Cfg` is nil, cross-ref steps are skipped and an Info diagnostic explains why; the file-level diagnostic for the broken file is still emitted by the per-file load.
- `registry.LoadRegistry` is also fail-fast on first bad file (`internal/usercommands/registry/registry.go:71`), and `Registry.Validate()` joins all cross-ref issues into one error string (line 200). To surface per-file and per-cross-ref diagnostics, this plan adds exported `BuildRegistryFromParsed(files []*model.CommandFile) (*Registry, error)` and `Registry.Diagnostics() []ValidationIssue` (typed-issue slice instead of joined error). The parameter type is `*model.CommandFile` (defined in `internal/usercommands/model/types.go:647`; returned by `loader.LoadCommandFile`), NOT `*loader.CommandFile` — that type does not exist. Without these additions, the commands validator could only emit a single "registry validation failed" row.
- The validate command must not bypass the loaders' built-in validators (`validateConfigKeys`, `detectLegacyComposeOverlays` from the tools-as-map plan, schema check, strict-decode rejections). Each per-file load surfaces these naturally as its file diagnostic. Multi-error collection happens **across** files; within a single loader the first error wins (acceptable).

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test` after each change; run `make lint` before declaring a task done
- No backward compatibility shims — change call sites and fixtures wholesale

Go-skill notes used here (verified against `cc-skills-golang`):

- **`golang-project-layout`**: health helpers belong in `internal/docker` (existing domain pkg); diagnostics aggregator gets its own pkg `internal/validate` with subpackages `internal/validate/{config,templates,commands}` matching the CLI surface.
- **`golang-structs-interfaces`**: `Validator` is a small interface (`ID`, `Domain`, `Run`); each validator is a stateless struct exported from its subpackage. Avoid one-method-interface temptation — `Domain()` and `ID()` are routing data, not just a `Run` callback.
- **`golang-error-handling`**: a diagnostic is data, not an error. Validators **never** return Go errors for findings; they return `[]Diagnostic`. Only infrastructure failures (file unreadable, ctx cancelled) flow as `error`.
- **`golang-spf13-cobra`**: nested subcommand tree (`validate config deploy`, `validate templates ide`, …) with `Args: cobra.NoArgs` at the leaves and `ValidArgsFunction` left empty (no dynamic args). Completion of the static tree is automatic.
- **`golang-cli`**: severity-gated exit code; `--strict` (warnings → errors), `--quiet` (info hidden); deterministic ordering of rows (sorted by `Domain`, then `Target`).
- **`golang-naming`**: package `validate` (not `validation`); types `Diagnostic`, `Severity`, `Validator`, `Registry`. Builtin name `docker_wait_healthy` (matches the `verb_subject_modifier` pattern of the existing `docker_remove_project_volumes`).
- **`golang-testing`**: table-driven for the registry/dispatch + per-validator fixture tests under `testdata/` (good YAML, bad YAML, mixed).
- **`golang-safety`**: `cfg.Tools` may be a nil map after lenient decode; range patterns are nil-safe but explicit `if cfg == nil` guards live at the validator boundary, not the consumer boundary.

## Testing Strategy

- **Unit tests**: required for every task. Table-driven where the surface is small (`Validator` registry dispatch, severity gating, exit-code computation). Per-validator tests use `testdata/` fixtures (intentionally-broken YAML + golden diagnostic lists).
- **No e2e tests in this project** — verification ends at unit tests + `make lint` + a manual smoke test against a sample project (recorded in Post-Completion).

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, test updates, doc edits achievable in this repo
- **Post-Completion** (no checkboxes): manual smoke test against a real devbox project, release-note copy

## Implementation Steps

### Task 1: Extract health-wait helpers into `internal/docker`

- [x] create `internal/docker/health.go` exporting:
      - `type HealthGetFn func(id string) (string, error)`
      - `func WaitContainersHealthy(ids []string, get HealthGetFn, attempts int, interval time.Duration, w *render.Writer) error`
      - `func HealthStatus(bin, id string) (string, error)` (was `dockerHealthStatus`)
- [x] copy logic verbatim from `internal/command/compose.go:158-216`; keep the warning/success message strings byte-identical so user-visible behavior is unchanged
- [x] delete `healthGetFn`, `waitContainersHealthy`, `dockerHealthStatus` from `internal/command/compose.go`
- [x] update the two existing call sites (`internal/command/wait.go:49-51`, `internal/command/docker.go:258-260`) to call the exported names; these will be deleted in Task 7 but must compile cleanly until then
- [x] add `func (c *Compose) HealthStatus(id string) (string, error)` method that wraps `HealthStatus(c.BinName(), id)` — gives the builtin a clean `compose.HealthStatus` callsite without leaking `bin` plumbing
- [x] write `internal/docker/health_test.go` with table-driven cases for `WaitContainersHealthy`:
      - all-healthy → success on first attempt
      - one starting → loops, eventually succeeds when fixture flips
      - unhealthy → returns error immediately, names container
      - no-healthcheck (`"none"`/`""`) → one-time warning, treated as done (assert warn fires exactly once per id via fake `*render.Writer` or buffer)
      - timeout → returns error after exactly `attempts` iterations
- [x] write a unit test for `Compose.HealthStatus` using a fake bin (an executable test helper in `testdata/` that echoes the desired status) — or mock by extracting an internal `inspectFn` indirection if shelling out is too heavy for unit tests
- [x] run `go test ./internal/docker/... ./internal/command/...` — must pass before next task

### Task 2: Add `docker_wait_healthy` builtin

- [x] create `internal/builtin/wait_healthy.go` with `type dockerWaitHealthyBuiltin struct{}` implementing `Validate`, `Describe`, `Run`
- [x] params (parsed from `with` map):
      - `timeout` (string duration, default `60s`) — parsed via `time.ParseDuration`
      - `interval` (string duration, default `2s`) — parsed via `time.ParseDuration`
      - `services` (optional `[]string`) — restrict the container set to specific compose services; default = all containers in the project
- [x] add helper `getDurationParam(with, key, defaultVal) (time.Duration, error)` in `internal/builtin/builtin.go` near `getStringParam` (signature mirrors existing helpers); reuse from this and any future timing-aware builtin
- [x] `Validate`: reject negative/zero `timeout`, negative/zero `interval`, non-string services entries; reject unknown keys (parity with strict YAML decode elsewhere)
- [x] `Describe`: returns `"wait until <N> services are healthy (timeout: <T>, interval: <I>)"` when `services` set, else `"wait until all containers are healthy (timeout: <T>, interval: <I>)"`
- [x] `Run`:
      - load docker config: `dockerCfg, err := config.LoadDockerConfig(ctx.ProjectRoot, ctx.Config)`
      - build compose: `compose := docker.NewCompose(ctx.Config, dockerCfg)` (NOT `NewComposeAll` — the running stack is the active overlay set)
      - obtain container IDs: when `services` is empty, `compose.ContainerIDs()`; when non-empty, `compose.ContainerIDsFor(services)` (add this method in this task — see below)
      - empty ID set → log warning via `ctx.Output.Warning("no containers found")` and return nil (mirrors current CLI behavior; this is intentional: the user may have invoked the step before `up`)
      - compute `attempts := max(int(timeout/interval), 1)`
      - call `docker.WaitContainersHealthy(ids, compose.HealthStatus, attempts, interval, ctx.Output)`
- [x] add `func (c *Compose) ContainerIDsFor(services []string) ([]string, error)` to `internal/docker/compose.go` — runs `docker compose ps -q <service>...`. Unit-test against a fake exec path (or extract via an `execer` interface as elsewhere in the package, matching the existing test pattern).
- [x] register the builtin in `internal/builtin/builtin.go`'s `registry` map
- [x] update the package godoc in `internal/builtin/builtin.go` to list `docker_wait_healthy` alongside the other canonical builtins
- [x] write `internal/builtin/wait_healthy_test.go`:
      - `Validate`: success path, missing fields default, negative timeout error, non-string services error, unknown key error
      - `Describe`: with-services and without-services strings
      - `Run`: success path using a fake compose (inject via test seam if needed), no-containers warning path, propagates error from `WaitContainersHealthy`
- [x] add an integration-shaped test in `internal/builtin/wait_healthy_test.go` that invokes the public `builtin.Run("docker_wait_healthy", ...)` to confirm registry wiring
- [x] run `go test ./internal/builtin/... ./internal/docker/...` — must pass before next task

### Task 3: Scaffold `internal/validate` package (types, registry, severity-gated runner)

- [x] create `internal/validate/validate.go` with the core types:

      ```go
      type Severity int
      const (
          SeverityUnknown Severity = iota // zero value — never set deliberately; guards against {} Diagnostic
          SeverityOK
          SeverityInfo
          SeverityWarning
          SeverityError
      )

      type Diagnostic struct {
          Severity Severity
          Domain   string  // "config" | "templates" | "commands"
          Target   string  // e.g. "config.deploy", "templates.ide:app-main", "commands:my-cmd"
          File     string  // relative to projectRoot; empty if N/A
          Line     int     // 0 if unknown
          Message  string  // low-cardinality template ("unknown field %q" form is OK, but identifiers/paths go in Target/File, not Message)
          Hint     string
      }

      type Context struct {
          ProjectRoot string
          ConfigPath  string
          Cfg         *config.DevboxConfig // may be nil if config load failed; validators must guard
      }

      type Validator interface {
          ID()     string  // unique within Domain, e.g. "deploy"
          Domain() string  // "config" | "templates" | "commands"
          Run(ctx Context) []Diagnostic
      }
      ```

- [x] add a `Registry` with `Register(Validator)` and selector helpers:
      - `Run(scope ...string)` where scope is e.g. `["config"]` or `["config","deploy"]` or empty (= all)
      - `MatchScope(domain, id string, scope []string) bool` — pure function, table-tested
- [x] add `Aggregate(diags []Diagnostic) Summary` returning `{Errors int; Warnings int; Infos int; OKs int}` and `ExitCode(summary, strict bool) int` (0 unless errors > 0, or warnings > 0 with strict)
- [x] add deterministic ordering: validators run in registration order; diagnostics returned are sorted by `(Severity desc, Domain asc, Target asc, File asc, Line asc)` before render
- [x] write `internal/validate/validate_test.go`:
      - `MatchScope` table-driven (empty matches all; single matches domain; two matches domain+id; mismatch rejects)
      - `Aggregate` / `ExitCode` table-driven (only-OKs → 0, one warn → 0, one warn with strict → 1, one error → 1)
      - sort order determinism (build a shuffled slice, run twice, assert byte-identical output)
- [x] no validators yet — those land in Task 4 and 5
- [x] run `go test ./internal/validate/...` — must pass before next task

### Task 4: Implement config validators

- [x] create `internal/validate/config/` subpackage; one file per validator. Each validator is a stateless struct in `package config` (single-word per `golang-naming`; alias as `valconfig "devbox-cli/internal/validate/config"` at the sole consumer in `internal/command/validate.go` to disambiguate from the existing `internal/config`). Registration via exported `All() []validate.Validator` called from Task 6's CLI wiring — avoids hidden `init()` side effects per the codebase convention
- [x] **per-file load contract**: every config validator runs its own file loader directly off `ctx.ProjectRoot`, never gates on `ctx.Cfg`. This is load-bearing: `LoadConfig` is fail-fast across `devbox.yml`, `services.yml`, and `deploy.yml` (the only call site for `LoadDeployConfig` outside this plan), so a broken `deploy.yml` would otherwise make `ctx.Cfg == nil` and hide the deploy error behind a `devbox` skip. With per-file loaders, `devbox validate config deploy` always exercises `deploy.yml` regardless of whether the full merge succeeded.
- [x] implement validators:
      - `devbox` — runs **two checks** because schema validation and config loading are decoupled in the codebase:
            1. `project.ValidateSchema(ctx.ConfigPath)` (`internal/project/project.go:102`) — checks `schema_version == "2"`. Missing or legacy (`v1`) schema is a `SeverityError` diagnostic with `Target: "config.devbox.schema"`. **Critical**: `config.LoadConfig` does NOT validate `schema_version` — `LoadConfig` would happily load a v1 file. Without this explicit call, `devbox validate config devbox` would report OK on legacy/missing-schema projects (false negative).
            2. `config.LoadConfig(ctx.ConfigPath)` — runs the 3-layer merge and the new tools-as-map plan validators (`validateConfigKeys`, `detectLegacyComposeOverlays`). Success → `SeverityOK` with `Target: "config.devbox"`. Failure → `SeverityError` with the load error as `Message` and file = `ctx.ConfigPath`.
      - Run order: schema check first; if it errors, still run the load check (a v1 file may also fail the load, but the schema error is the precise diagnostic users need). Both diagnostics are emitted independently; the rendering layer's severity sort puts them next to each other.
      - **Required fixtures** (in `internal/validate/config/testdata/`):
            - `devbox-missing-schema/devbox.yml` — no `schema_version` field → expect one schema-error diagnostic
            - `devbox-legacy-schema/devbox.yml` — `schema_version: "1"` → expect one schema-error diagnostic
            - `devbox-v2-bad-keys/devbox.yml` — valid schema, identifier-unsafe tool key → expect schema OK + load error
            - `devbox-v2-good/devbox.yml` — valid schema, clean config → expect two OK diagnostics
      - Note: when `LoadConfig` fails because of `services.yml`/`deploy.yml`, the per-file validators for those domains will *also* fire; that's intentional — the per-file diagnostic is more precise.
      - `services` — runs `config.LoadServicesConfig(<projectRoot>/devbox/services.yml)` directly. Missing file is `SeverityInfo` ("no services.yml; services may be inline"). Per-service issues collected by iterating the parsed map (empty `dir`, invalid `extends`, unresolved overlays).
      - `docker` — runs `config.LoadDockerConfig(<projectRoot>, ctx.Cfg)` IF `ctx.Cfg` is non-nil. **Do NOT pass an empty-stub `&config.DevboxConfig{}` when `ctx.Cfg == nil`**: `LoadDockerConfig` resolves `${...}` template references in `project_name` against `cfg.Raw` (`internal/config/docker.go:139`), and an empty stub has `Raw == nil`, so a valid `project_name: ${project.name}` would produce a spurious `unresolved path "project.name"` error. When `ctx.Cfg == nil`, the validator emits one Info diagnostic ("docker.yml validation requires successful main config load; skipped") and returns. Same `ctx.Cfg`-gating pattern as `deploy`/`reset` cross-ref steps. (Structural-only validation without template resolution would require splitting `LoadDockerConfig` into parse + resolve phases — out of scope here.)
      - `info` — runs `config.LoadInfoConfig(<projectRoot>/devbox/info.yml)` directly; reports unknown item types and decode errors.
      - `styles` — runs `config.LoadStylesConfig(<projectRoot>/devbox/styles.yml)` directly.
      - `lifecycle` — runs `config.LoadLifecycleConfig(<projectRoot>/devbox/lifecycle.yml)` directly (strict decode; unknown fields fail loudly).
      - `deploy` — runs `config.LoadDeployConfig(<projectRoot>/devbox/deploy.yml)` directly. Strict decode is already in place. If load succeeds AND `ctx.Cfg != nil`, additionally walk the result via `deploy.ResolvePlan(ctx.Cfg)` (signature requires full cfg for service-deploy-file resolution) and surface per-step errors as separate diagnostics, each with `Target: "config.deploy:" + step.Address`. If `ctx.Cfg == nil`, skip the `ResolvePlan` step and emit one Info diagnostic ("plan resolution skipped: main config did not load") in addition to the file-level OK/error diagnostic.
      - `reset` — runs `config.LoadResetConfig` then `reset.ResolvePlan(ctx.Cfg)` under the same `ctx.Cfg != nil` gate. This replaces `devbox reset config check`.
      - `service-deploy` — runs `config.LoadServiceDeployConfigs(<projectRoot>, services)` where `services` is `ctx.Cfg.Services` if non-nil, otherwise the result of the `services` validator's own load. Per-service load errors become per-service diagnostics. Keep as a separate `service-deploy` ID so users can target it with `devbox validate config service-deploy`; `devbox validate config` runs all config validators including this one.
- [x] add per-validator tests under `internal/validate/config/*_test.go`. Each uses `testdata/` with at least:
      - a good fixture → one `SeverityOK` diagnostic
      - a broken fixture (unknown YAML field, missing required field, bad reference) → exactly one or more `SeverityError` diagnostics with stable `Message` substrings (assert via `strings.Contains`, not full equality)
- [x] add an integration test in `internal/validate/config/all_test.go` that builds a sample project tree (using `t.TempDir`) with mixed-validity files and asserts the aggregate diagnostic list across all config validators
- [x] **post-tools-as-map verification**: add a fixture under `internal/validate/config/testdata/legacy-overlays/` with a `compose.overlays:` block; the `devbox` validator must surface the migration error from `detectLegacyComposeOverlays` as a single error diagnostic. Same for an identifier-unsafe tool key (`redis-insight`) — the validator surfaces that as one error diagnostic.
- [x] run `go test ./internal/validate/...` — must pass before next task

### Task 5: Implement templates and commands validators

- [x] create `internal/validate/templates/` subpackage (`package templates`; alias `valtmpl` at consumer) with validators:
      - `ide` — **reuse the existing render selection policy verbatim**. The current unexported `selectIDEServices` at `internal/command/ide.go:57` already encodes the full filter (`Enabled` gate, the *two-valued* `IDERenderEnabledExplicit() (bool, bool)` predicate, empty/root-dir drop, and deepest-extends-chain collision resolution). The earlier draft's one-liner `svc.Enabled && svc.IDERenderEnabledExplicit() && svc.Dir != ""` was wrong on two counts: `IDERenderEnabledExplicit()` returns two values, and a hand-rolled predicate would not match the collision resolver — producing false positives for services the render command intentionally skips. After the helpers move to `internal/templates/ide/` they must be **exported** so consumers in another package can call them. Final API (renamed per `golang-naming` — no `IDE` prefix because the package is `ide`):
            - `func SelectServices(services map[string]config.ServiceConfig) (selected []string, skipped []SkippedService)` (was `selectIDEServices`)
            - `type SkippedService struct { Name, Reason, Dir, Winner string }` (was `skippedService`)
            - `func ResolveTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error)` (was `resolveIDETemplatePack`)
            - `func WalkPack(packDir string) ([]PackEntry, error)` (was `walkIDEPack`); `type PackEntry struct { SourcePath, RelPath string }` (was `packEntry`)
            - `func ValidateTemplateKey(key string) error` (was `validateIDETemplateKey`)
            - `func ExtendsDepth(services map[string]config.ServiceConfig, name string) (int, bool)` (was `extendsDepth`)
      - The validator calls `ide.SelectServices(cfg.Services)`, walks only the `selected` set; the `skipped` set becomes Info-level diagnostics (one per skipped service) using the existing reason taxonomy (`service-disabled`, `ide-disabled`, `ide-policy`, `empty-dir`, `lost-collision`).
      - `ai` — mirror the IDE side. Move from `internal/command/render_ai.go` into `internal/templates/ai/` with the same export pattern (note: AI uses **shallowest**-extends-chain collision resolution; opposite of IDE). Final API:
            - `func SelectServices(services map[string]config.ServiceConfig) (selected []string, skipped []SkippedService)` (was `selectAgentsServices`)
            - `type SkippedService struct { ... }` — same shape as the IDE counterpart (define in this package; do NOT cross-import for one type)
            - `func ResolveTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error)` (was `resolveAgentsTemplatePack`)
            - `func LoadManifest(packDir string) (*Manifest, error)` (was `loadAgentsManifest`); `type Manifest struct { ... }` (was `agentsManifest`)
            - `func ValidateManifest(m *Manifest, packDir string) error` (was `validateAgentsManifest`)
            - `func RenderTemplateFile(sourcePath string, data any, dest, absHubDir, absRoot string) error` (was `renderAgentsTemplateFile`)
            - `func EnsureRelativeSymlink(linkPath, targetWithinHub, absHubDir, absRoot string) error` (was `ensureRelativeSymlink`)
      - **Refactor required to make selection reusable** without importing `internal/command`: move the listed symbols (renamed/exported per the API above) from `internal/command/ide.go` to `internal/templates/ide/` and from `internal/command/render_ai.go` to `internal/templates/ai/`. Update the existing CLI commands (`devbox render ide`, `devbox render ai`) and the validators to import the new packages. Bodies unchanged; no behavior change. The `internal/command/ide_test.go` and `internal/command/render_ai_test.go` tests move with the helpers (renamed to point at the new package and the exported symbols).
- [x] create `internal/validate/commands/commands.go` (`package commands`; alias `valcmds` at consumer to avoid collision with `internal/command`) with one validator:
      - `commands` — discover via `loader.DiscoverCommandFiles(<projectRoot>/devbox/commands)`; parse each via `loader.LoadCommandFile`; collect per-file decode errors as `SeverityError` diagnostics (one per file).
      - After per-file parsing, build a registry from the **already-parsed** files (skip the bad ones) and run cross-ref validation that returns per-issue records instead of a joined error string.
- [x] **scope of commands validation — what is statically checkable, and what is not.** The validator covers what can be decided without runtime state:
      - YAML structural parse (strict-decode rejections, legacy `type: command` / `run:` → hard error; already enforced by `model.ParseCommandFile`)
      - Duplicate command IDs across files
      - Workflow step `command:` references resolve to a known command ID (the existing `Registry.Validate` surface, made diagnostic-friendly via `Diagnostics()`)
      - It does **NOT** validate file-spec existence (`files:` block). File-spec resolution (`internal/usercommands/runtime/resolve_files.go:14`) depends on `params`, `context`, template expansion (`${...}`), and `tpl.ResolvedFile` plumbing that only exists at command-invocation time. A purely literal `path:` with no `${...}` could in principle be checked, but the value lives in `fspec.Path` after template-free yaml decode, and even literal paths may resolve relative to a working dir that isn't known until the command is actually invoked. Treating "missing file" as a static error would produce false negatives the moment a user adds a template. Out of scope; documented in the `Long` text.
- [x] **registry API additions** (`internal/usercommands/registry/registry.go`) — required because today's `LoadRegistry` is fail-fast on first bad file (line 71) and `Registry.Validate()` returns a single joined error string (line 200), neither of which can drive a diagnostic table:
      - `func BuildRegistryFromParsed(files []*model.CommandFile) (*Registry, error)` — builds a registry from already-parsed files without rereading from disk. The parameter type is `*model.CommandFile` (return type of `loader.LoadCommandFile` at `internal/usercommands/loader/loader.go:67`; `CommandFile` is defined in `internal/usercommands/model/types.go:647`, NOT in `loader`). Duplicate IDs return an error (single error is acceptable here because duplicates are a one-shot condition). Used by the validator after it has produced its own per-file parse diagnostics.
      - `type ValidationIssue struct { CommandID string; StepIndex int; Message string }` and `func (r *Registry) Diagnostics() []ValidationIssue` — returns each cross-ref issue as a typed record so the validator can map one-to-one to `Diagnostic` rows (`Target: "commands:" + issue.CommandID`).
      - Keep the existing `LoadRegistry` and `Validate() error` as-is for backward compatibility with existing call sites; `Diagnostics()` is the new diagnostic-friendly path.
- [x] write unit tests for the new registry API in `internal/usercommands/registry/registry_test.go`: `BuildRegistryFromParsed` with duplicate IDs, `Diagnostics()` returning multiple cross-ref issues from a fixture with several broken workflow `command:` references.
- [x] **tests** for each new validator:
      - `templates/ide_test.go`: good pack (one OK), pack with bare `.tmpl`, escaping symlink, illegal key, missing pack (each → one error diagnostic with stable substring)
      - `templates/ai_test.go`: good manifest, manifest with escape attempt, missing render target for a declared symlink, unknown YAML field (strict-decode error)
      - `commands/commands_test.go`: good command file, file with `type: command` (legacy, must be rejected — already enforced by `model.ParseCommandFile`), workflow step referencing an unknown command ID (cross-ref error from `Diagnostics()`), duplicate command ID across two files. Do NOT add a "missing referenced file" case — file-spec existence is runtime-dependent (params + templates + working dir) and explicitly out of scope per the validator definition above.
- [x] **refactor verification**: every existing test under `internal/command/ide_test.go`, `internal/command/render_ai_test.go` continues to pass after the helper moves. Update import paths only — do not change behavior.
- [x] run `go test ./...` — must pass before next task

### Task 6: Add `devbox validate` cobra command tree + renderer

- [ ] add `internal/ui/diagnostics_table.go` exporting `RenderDiagnosticsTable(rows []DiagnosticRow) string` with a fixed column set `STATUS / DOMAIN / TARGET / FILE / MESSAGE / HINT`. Mirror `RenderToolTable` styling (border + per-column min-width + status-color via lipgloss). Status glyphs: `✓` OK, `ⓘ` info, `⚠` warn, `✗` error.
- [ ] add `internal/ui/diagnostics_table_test.go` with golden-byte assertions on a representative input (assert exact output to lock the format)
- [ ] create `internal/command/validate.go` with the cobra tree:
      - `newValidateCmd(flags)` → root carries `RunE` that runs the full set when invoked with no subcommand; each child subcommand has its own `RunE` that calls the shared `runValidate(cmd, scope []string)` helper with a narrower scope. No intermediate node has business logic; leaves and the bare root do.
      - subcommands: `config` (+ children: `devbox`, `services`, `docker`, `info`, `styles`, `lifecycle`, `deploy`, `reset`, `service-deploy`), `templates` (+ children: `ide`, `ai`), `commands`
      - shared flags on root: `--strict` (bool, default false), `--quiet` (bool, default false); inherited via `PersistentFlags`
      - `Args: cobra.NoArgs` on every leaf
      - `SilenceUsage: true` on all nodes — we render our own table on failure
      - **prose for `--help`** (`devbox_validate.md` is generated by `cobradoc.GenMarkdownTree` at `internal/command/docs.go:212`, which rewrites the file every time `docs generate --scope cli` runs — so any hand-edit would be erased by Task 10). Put all explanatory text (severity table, `--strict`/`--quiet` semantics, scope→target map) into the `Long` field on the `validate` cobra command and short blurbs into each subcommand's `Long`. The generator serializes these into the markdown, so the docs survive regeneration.
- [ ] **bypass schema gate on the validate subtree** (`internal/command/root.go:66` `PersistentPreRunE`): today `PersistentPreRunE` calls `project.Resolve(configArg)` which composes `Locate + ValidateSchema`, and any schema mismatch returns fatal before any leaf `RunE` executes. That means `devbox validate config devbox` could never surface a schema diagnostic — the command would error out before the validator ran. Fix: distinguish validate-tree commands from other commands inside the existing `PersistentPreRunE`:

      ```go
      // existing path
      resolved, err := project.Resolve(configArg)
      if err != nil {
          if errors.Is(err, project.ErrNotFound) && allowedWithoutProject(cmd) { ... }
          if isValidateCommand(cmd) {
              // Fall back to a non-schema-validating Locate so the validator can
              // surface schema (and any other LoadConfig) errors as diagnostics
              // instead of aborting before RunE.
              if loc, found, lerr := project.Locate(configArg); lerr == nil && found {
                  flags.configPath = loc.ConfigPath
                  flags.projectRoot = loc.Root
                  flags.stylesCfg  = applyStyles(loc.Root, cmd.ErrOrStderr())
                  return nil
              }
              // explicit-bad-path or os.ErrNotExist still fatal — those aren't
              // diagnostics, they're "we can't find anything to validate"
          }
          return err
      }
      ```

      Add `isValidateCommand(cmd *cobra.Command) bool` next to `allowedWithoutProject`; matches `validate` and any descendant by walking `cmd.Parent()` chain. This is a deliberate carve-out — validate is the *only* command whose purpose is to report config errors, so it must outlast schema validation.
- [ ] **fang error-handler carve-out for ExitCode-bearing errors** (`cmd/devbox/main.go:26-31`): the current handler suppresses only `ErrSilent` and delegates everything else to `fang.DefaultErrorHandler`, which would print a styled `Error: validation failed` line *after* our diagnostic table. Update the handler to also suppress errors implementing `ExitCode() int`:

      ```go
      errHandler := func(w io.Writer, styles fang.Styles, err error) {
          if errors.Is(err, command.ErrSilent) {
              return
          }
          var ec interface{ ExitCode() int }
          if errors.As(err, &ec) {
              // validation failed: our table is already on stdout; suppress
              // fang's "Error: ..." line. The exit code is handled below.
              return
          }
          fang.DefaultErrorHandler(w, styles, err)
      }
      ```

      Pair this with the `errors.As` exit-code translation already specified in this task. Two checks, same interface — the handler decides what to *print*, the post-Execute branch decides what to *return* as exit code.
- [ ] add a typed error in `internal/command/validate.go` (a struct so it can carry the summary). The type itself stays unexported — `main` will not name it; it will assert against an unexported-type-friendly interface using `errors.As`:

      ```go
      type validationFailedError struct{ summary validate.Summary; strict bool }
      func (e *validationFailedError) Error() string { return "validation failed" }
      func (e *validationFailedError) ExitCode() int { return validate.ExitCode(e.summary, e.strict) }
      ```

- [ ] update `cmd/devbox/main.go` to translate any error implementing `ExitCode() int` into a custom exit code. Today (`cmd/devbox/main.go:51`) the function exits `1` for any non-nil error from `fang.Execute`. Replace with:

      ```go
      if err != nil {
          var ec interface{ ExitCode() int }
          if errors.As(err, &ec) {
              os.Exit(ec.ExitCode())
          }
          os.Exit(1)
      }
      ```

      Two correctness points: (1) `errors.As` on an inline interface type works with unexported concrete types — `main` does not need to name `*validationFailedError`, just the `ExitCode() int` method set; (2) `errors.As` (not `errors.Is`) is required because we're matching on interface satisfaction, not equality. This satisfies `golang-cli`'s "return errors from RunE, let main decide" rule while keeping the concrete error type private to `internal/command`.
- [ ] runner skeleton:

      ```go
      func runValidate(cmd *cobra.Command, flags *rootFlags, strict, quiet bool, scope []string) error {
          cfg, configPath, projectRoot, err := loadForValidate(flags) // cfg may be nil if load failed
          if err != nil && !errors.Is(err, errPartialLoad) { // unrecoverable infra error (e.g. cwd unreadable)
              return err
          }
          ctx := validate.Context{ProjectRoot: projectRoot, ConfigPath: configPath, Cfg: cfg}
          diags := registry.Run(ctx, scope...)
          rows := buildRows(diags, quiet)
          fmt.Fprintln(cmd.OutOrStdout(), ui.RenderDiagnosticsTable(rows))
          summary := validate.Aggregate(diags)
          printSummary(cmd.OutOrStdout(), summary, strict)
          if validate.ExitCode(summary, strict) != 0 {
              return &validationFailedError{summary: summary, strict: strict}
          }
          return nil
      }
      ```

      Two `golang-cli` corrections applied here vs. the earlier draft: (1) write through `cmd.OutOrStdout()` (not `fmt.Println` / `os.Stdout`) so tests can `cmd.SetOut(buf)` and capture; (2) return an error from `RunE` rather than calling `os.Exit` — the previous draft's `exitFn` test seam is removed because it was a workaround for the `os.Exit` mistake.
- [ ] **`ctx.Cfg == nil` policy** (revised from the earlier draft to fix a high-severity hole): validators do **not** skip when `ctx.Cfg == nil`. They load their own file (Task 4 / Task 5 per-file load contract) and emit the resulting file-level diagnostic. Only **cross-file** steps inside a validator (e.g., `ResolvePlan(ctx.Cfg)` inside the `deploy` validator) are gated; when those are gated, the validator emits its file-level diagnostic AND one Info-level "cross-references skipped: main config did not load" diagnostic in the same run. This ensures `devbox validate config deploy` against a project with a broken `deploy.yml` reports the deploy error directly, regardless of whether the full `LoadConfig` succeeded.
- [ ] register validators in one place: `internal/command/validate.go` calls `validateconfig.All()`, `validatetemplates.All()`, `validatecommands.All()` and builds the registry; this avoids `init()` side effects in the validator subpackages
- [ ] wire the new cobra command in `internal/command/root.go`: place under the `groupConfiguration` group with the existing `service`, `tool`, `render` commands
- [ ] **completion**: nested subcommand names are static, so cobra's built-in subcommand completion handles them; no `ValidArgsFunction` needed
- [ ] add `internal/command/validate_test.go`:
      - dispatch: `runValidate(..., scope=[])` runs all; `scope=["config"]` runs only config validators; `scope=["config","deploy"]` runs only deploy
      - exit-code wiring: build a fresh cobra tree per test (`golang-spf13-cobra`: cobra accumulates flag state across `Execute()`), invoke via `cmd.SetArgs([...]); cmd.Execute()`, then assert the returned error is `*validationFailedError` with the expected `ExitCode()` (errors → 1, OK only → nil error / exit 0, warnings with strict → 1, warnings without strict → nil)
      - rendering: `cmd.SetOut(buf)` then assert the captured buffer contains expected rows
- [ ] run `go test ./internal/command/... ./internal/validate/... ./internal/ui/...` — must pass before next task

### Task 7: Remove deprecated public commands

- [ ] delete `internal/command/wait.go` (entire file)
- [ ] delete the `newDockerWaitCmd` function from `internal/command/docker.go` and its registration in the `docker` parent command
- [ ] delete `internal/command/up.go`, `down.go`, `logs.go`, `ps.go` (entire files)
- [ ] remove the `newResetConfigCheckCmd` function and its parent wiring from `internal/command/reset.go`
- [ ] remove the corresponding registrations in `internal/command/root.go:125-131` (`newUpCmd`, `newDownCmd`, `newLogsCmd`, `newPsCmd`, `newWaitCmd`)
- [ ] grep for any orphaned imports (`time`, `render`, `config`) in `internal/command/docker.go` after removing the wait subcommand; remove unused imports
- [ ] delete the `_test.go` files for the removed commands (`wait_test.go`, `up_test.go`, `down_test.go`, `logs_test.go`, `ps_test.go` if they exist) and the `reset config check` test
- [ ] grep for any `bin/devbox wait`, `bin/devbox up`, etc. references in `internal/command/*_test.go` integration-style assertions; update or delete those tests
- [ ] run `go build ./...` — must compile cleanly; any failure is a missed reference
- [ ] run `go test ./...` — must pass before next task

### Task 8: Update documentation

- [ ] delete generated CLI docs: `docs/reference/cli/devbox_wait.md`, `devbox_up.md`, `devbox_down.md`, `devbox_logs.md`, `devbox_ps.md`, `devbox_docker_wait.md`, `devbox_reset_config.md`, `devbox_reset_config_check.md`. These regenerate from cobra metadata; running `bin/devbox docs generate --scope cli` after the build will remove them and emit the new `devbox_validate*.md` pages.
- [ ] populate all `validate*` command pages **via cobra `Long` strings** (set in Task 6), not by hand-editing the markdown. `cobradoc.GenMarkdownTree` (`internal/command/docs.go:212`) rewrites the whole tree on every regen — any hand-written prose in `devbox_validate.md` would be wiped by Task 10's `docs generate --scope cli`. Concretely: the severity table, the scope→target map, the `--strict`/`--quiet` semantics, and the "replaces `devbox reset config check`" callout all live in cobra `Long` text on the `validate` root and on the relevant leaves.
- [ ] update `docs/reference/config/deploy.md` (the canonical "deploy.yml / reset.yml" reference — there is no separate `reset.md`; reset is documented in the same file, header at line 1): replace any mention of `devbox reset config check` with `devbox validate config reset`; add a top-of-page note linking to `devbox validate config`
- [ ] update `docs/reference/builtins.md` (or whichever page enumerates builtins) — add `docker_wait_healthy` with its params and an example block. If no such page exists today, add a `## docker_wait_healthy` section to `docs/reference/config/deploy.md` near the other builtin docs.
- [ ] update `AGENTS.md`:
      - `internal/builtin/` bullet: add `docker_wait_healthy` to the registered-builtins list
      - `internal/docker/` bullet: add `WaitContainersHealthy`, `HealthStatus`, and the new `Compose.ContainerIDsFor` / `Compose.HealthStatus` methods
      - `internal/command/` bullet: remove the `devbox wait`, `devbox up`, `devbox down`, `devbox logs`, `devbox ps`, `devbox docker wait`, `devbox reset config check` references; add `devbox validate` with a short description of the scope tree
      - **Key Patterns** section: add a short "Validation" entry pointing at `internal/validate` and the registry pattern
      - If the templates helpers moved to a new package (Task 5), update the `internal/command/` bullet accordingly and add a short `internal/templates/ide/` and `internal/templates/ai/` bullet
- [ ] grep all docs and AGENTS.md for `devbox wait`, `devbox up`, `devbox down`, `devbox logs`, `devbox ps`, `devbox docker wait`, `devbox reset config check`; rewrite or delete each match (some are in examples that should now reference `devbox docker up`, etc., or `devbox run` for the lifecycle path)

### Task 9: Verify acceptance criteria

- [ ] grep production code for the removed identifiers: `newWaitCmd`, `newDockerWaitCmd`, `newUpCmd`, `newDownCmd`, `newLogsCmd`, `newPsCmd`, `newResetConfigCheckCmd` — all must return zero matches
- [ ] grep the docs tree and `AGENTS.md` for `devbox wait`, `devbox up`, `devbox down`, `devbox logs`, `devbox ps`, `devbox docker wait`, `devbox reset config check` — all must return zero matches outside historical changelog notes
- [ ] verify the registry: `internal/builtin/builtin.go` `registry` map contains `"docker_wait_healthy"`
- [ ] verify the validate command tree resolves: `bin/devbox validate --help` lists `config`, `templates`, `commands`; `bin/devbox validate config --help` lists `devbox`, `services`, `docker`, `info`, `styles`, `lifecycle`, `deploy`, `reset`, `service-deploy`; `bin/devbox validate templates --help` lists `ide`, `ai`
- [ ] exit-code matrix verified by unit tests in Task 6:
      - all OK → 0
      - one warn, no `--strict` → 0
      - one warn, `--strict` → 1
      - one error → 1
- [ ] `make test` — full suite green
- [ ] `make lint` — clean
- [ ] `make build` succeeds

### Task 10: Final docs sync

- [ ] regenerate CLI docs: `bin/devbox docs generate --scope cli`
- [ ] re-read `AGENTS.md` "Key Patterns" and "Project Structure" sections; correct any stale references
- [ ] verify no orphan mentions of removed commands remain in `docs/`

## Technical Details

### New builtin contract

```yaml
- name: wait-containers
  type: builtin
  cmd: docker_wait_healthy
  with:
    timeout: 120s        # required-form: string parseable by time.ParseDuration; default 60s
    interval: 2s         # default 2s
    services:            # optional; default = all containers in the active stack
      - app-main
      - db
```

Failure modes:

- `Validate` rejects negative or zero `timeout`/`interval`, non-string `services` entries, and any unknown key.
- `Run` returns nil with a `no containers found` warning when the active stack has zero containers — same as the old `devbox wait` behavior; intentional to keep `wait` idempotent in pipelines that run before `up`.
- `Run` returns an error when a container is `unhealthy` or when the timeout elapses without all containers reaching `healthy`.

### New domain types in `internal/docker`

```go
// HealthGetFn returns the Docker health status string for a container by ID.
// Known values: "healthy", "unhealthy", "starting", "none".
type HealthGetFn func(id string) (string, error)

func WaitContainersHealthy(ids []string, get HealthGetFn, attempts int, interval time.Duration, w *render.Writer) error
func HealthStatus(bin, id string) (string, error)

// Method on Compose (in compose.go):
func (c *Compose) HealthStatus(id string) (string, error) { return HealthStatus(c.BinName(), id) }
func (c *Compose) ContainerIDsFor(services []string) ([]string, error) // docker compose ps -q <services>
```

### `internal/validate` core types

```go
// Severity ordering matters for sort + exit-code gating.
// SeverityUnknown at iota 0 catches zero-valued Diagnostic{} per golang-naming.
type Severity int

const (
    SeverityUnknown Severity = iota
    SeverityOK
    SeverityInfo
    SeverityWarning
    SeverityError
)

type Diagnostic struct {
    Severity Severity
    Domain   string // "config" | "templates" | "commands"
    Target   string // "config.deploy", "templates.ide:app-main", "commands:foo"
    File     string // relative to projectRoot; empty when not file-bound
    Line     int    // 0 when unknown
    Message  string
    Hint     string
}

type Context struct {
    ProjectRoot string
    ConfigPath  string
    Cfg         *config.DevboxConfig // nil if LoadConfig failed; validators must guard
}

type Validator interface {
    ID() string
    Domain() string
    Run(ctx Context) []Diagnostic
}

type Summary struct{ Errors, Warnings, Infos, OKs int }

func ExitCode(s Summary, strict bool) int {
    if s.Errors > 0 || (strict && s.Warnings > 0) {
        return 1
    }
    return 0
}
```

### Validate command scope mapping

| CLI invocation                              | Validators run                                                              |
| ------------------------------------------- | --------------------------------------------------------------------------- |
| `devbox validate`                           | all (config + templates + commands)                                         |
| `devbox validate config`                    | all `config.*` (devbox, services, docker, info, styles, lifecycle, deploy, reset, service-deploy) |
| `devbox validate config deploy`             | `config.deploy` only                                                        |
| `devbox validate config reset`              | `config.reset` only (replaces `devbox reset config check`)                  |
| `devbox validate templates`                 | `templates.ide` + `templates.ai`                                            |
| `devbox validate templates ide`             | `templates.ide` only                                                        |
| `devbox validate templates ai`              | `templates.ai` only                                                         |
| `devbox validate commands`                  | `commands` only                                                             |
| `--strict`                                  | warnings count as errors for exit code                                      |
| `--quiet`                                   | hide `SeverityOK` and `SeverityInfo` rows                                   |

### Removed commands

| Removed                                     | Replacement                                       |
| ------------------------------------------- | ------------------------------------------------- |
| `devbox wait`                               | `docker_wait_healthy` builtin in pipeline steps   |
| `devbox docker wait`                        | same                                              |
| `devbox up`                                 | `devbox docker up` (or `devbox run` for full lifecycle) |
| `devbox down`                               | `devbox docker down` (or `devbox stop`)           |
| `devbox logs`                               | `devbox docker logs`                              |
| `devbox ps`                                 | `devbox docker ps`                                |
| `devbox reset config check`                 | `devbox validate config reset`                    |

### Go-skill notes (cc-skills-golang)

These are load-bearing implementation details verified against the relevant skills.

**`golang-project-layout`.** Three new locations:

- `internal/docker/health.go` — sits next to `internal/docker/compose.go` because both depend on `docker`/`podman` binaries and on the active `Compose` shape. No new package.
- `internal/builtin/wait_healthy.go` — one builtin per file, matches existing pattern (`volumes.go`, `paths.go`, `dirs_ensure.go`).
- `internal/validate/` (parent) + `internal/validate/{config,templates,commands}/` (children) — the parent owns the cross-cutting types (`Diagnostic`, `Validator`, `Registry`, `Summary`); children own the per-domain validators. CLI wiring lives in `internal/command/validate.go`.
- Template helpers extraction in Task 5: `internal/templates/ide/`, `internal/templates/ai/` (preferred over `internal/render/ide/` to keep `internal/render` focused on `*render.Writer`). Exported API after the move (canonical list — see Task 5 for the old → new mapping):
      - `internal/templates/ide/`: `SelectServices`, `SkippedService`, `ResolveTemplatePack`, `WalkPack`, `PackEntry`, `ValidateTemplateKey`, `ExtendsDepth`
      - `internal/templates/ai/`: `SelectServices`, `SkippedService`, `ResolveTemplatePack`, `LoadManifest`, `Manifest`, `ValidateManifest`, `RenderTemplateFile`, `EnsureRelativeSymlink`
      - Names are unprefixed (`SelectServices`, not `SelectIDEServices`) because the package name already supplies the scope — `ide.SelectServices(...)` reads cleanly per `golang-naming`'s anti-stutter rule.

**`golang-structs-interfaces`.** The `Validator` interface is a 3-method interface deliberately: `Run` alone would force the registry to track `(string, string, RunFn)` tuples externally, leaking concerns. Keeping `ID()` and `Domain()` on the interface lets the registry stay generic over `[]Validator` and lets each validator self-describe in plan output. Implementations are tiny structs (`type deployValidator struct{}`) — pointer vs. value receiver irrelevant; use value.

**`golang-error-handling`.** Two distinct concerns:

- *Diagnostic*: a finding emitted by a validator. Always returned in `[]Diagnostic`. Never wrapped, never `errors.Is`-able. Multiple diagnostics from one validator are fine.
- *Infrastructure error*: a validator cannot run (file unreadable for reasons unrelated to the project YAML, OS error). Surface as `SeverityError` with the OS error in `Message`; do **not** abort the entire `Run`. This keeps `validate` resilient on partially-broken projects.

The CLI never `os.Exit`s from inside `RunE`. `RunE` returns `*validationFailedError` (implements `ExitCode() int`); both the fang error handler and `cmd/devbox/main.go` assert on the `ExitCode() int` interface via `errors.As` — the handler suppresses the extra "Error: ..." line, main translates to the exit code. See Task 6 for the wiring.

**`golang-spf13-cobra`.** Nested subcommand tree is the right idiom for fixed scope (`config`/`templates`/`commands` are not user-extensible at this layer). Each leaf has `Args: cobra.NoArgs`. The `--strict` and `--quiet` flags use `PersistentFlags` on the `validate` root so they propagate to every subcommand. `SilenceUsage: true` to suppress the usage block on validation failures (we render our own table).

**`golang-cli`.** Severity-gated exit code; output flushed via `os.Stdout.Sync()` before `os.Exit`. Stable column widths (lipgloss `Width` per-column) so the table renders predictably under pipes. Non-TTY behavior: still render the lipgloss table but skip color via the existing render-mode detection in `internal/render`.

**`golang-naming`.**

- Package: `validate` (not `validation`, not `validators`) — single-word, action-oriented, matches `internal/builtin` precedent.
- Types: `Diagnostic`, `Severity`, `Validator`, `Registry`, `Summary`. No `Config`-prefixed types in the parent package.
- Subpackages: directories `internal/validate/{config,templates,commands}`; package declarations stay single-word (`package config`, `package templates`, `package commands`) per `golang-naming`'s single-word rule. Collisions with `internal/config` / `internal/command` are resolved by **import aliasing at the single consumer** (`internal/command/validate.go`): `valconfig`, `valtmpl`, `valcmds`. This is the textbook case the naming skill calls out — alias only on collision.
- Builtin name: `docker_wait_healthy` (matches `docker_remove_project_volumes`: `<scope>_<verb>_<modifier>`).
- Cobra command name: `validate` (verb, singular, matches `deploy`/`reset`).

**`golang-testing`.** Per-validator tests use `testdata/` golden fixtures rather than constructor helpers — the validators are I/O-bound on YAML files, so writing intentionally-broken YAML to disk is the most faithful unit test. Use `testify/require` (already in the project) for table assertions. Use `t.TempDir` for project trees in integration-shape tests.

**`golang-safety`.** Three nil-traps to handle:

- `ctx.Cfg` may be nil when `LoadConfig` failed. Validators **do not blanket-skip** in this case (earlier-draft mistake — see Task 6 `ctx.Cfg == nil` policy). They run their own per-file loader and emit the file-level diagnostic; only cross-file resolution steps (`ResolvePlan` etc.) are gated behind `ctx.Cfg != nil`.
- `cfg.Tools`, `cfg.Services`, `cfg.Runtime.Hosts`, `cfg.Runtime.Ports` may all be nil after lenient decode of an empty section. `len(nil-map) == 0` and `range nil-map` are safe; validators never assume non-nil.
- `with` map in builtin `Validate` may be nil. Existing helpers (`getStringParam`, `getStringSlice`) already guard; `getDurationParam` (new in Task 2) must too.

### Severity rendering (lipgloss)

| Severity | Glyph | Color (lipgloss adaptive) |
| -------- | ----- | ------------------------- |
| OK       | `✓`   | green                     |
| Info     | `ⓘ`   | dim                       |
| Warning  | `⚠`   | yellow                    |
| Error    | `✗`   | red                       |

Colors resolve via the existing `internal/ui` style accessors; no new palette entries needed. If `internal/ui.ApplyStyles` runs first (it normally does in CLI startup), the diagnostic colors honor any user-set palette in `devbox/styles.yml`.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual smoke test (single workstation)**:

- Build `bin/devbox` against a real sample project.
- Run `bin/devbox validate` — confirm a clean project produces a table of all-OK rows and exits 0.
- Edit `devbox/deploy.yml` to introduce a syntax error; run `bin/devbox validate config deploy` — confirm one error row and exit 1.
- Add a pipeline step `{ name: wait, type: builtin, cmd: docker_wait_healthy, with: { timeout: 30s, interval: 1s } }` to `deploy.yml` and run `devbox deploy`; confirm the builtin runs at the right phase.
- Run `bin/devbox up` — confirm it fails with cobra's `unknown command "up"` and that the help text no longer lists it. Same for `down`, `logs`, `ps`, `wait`, `docker wait`, `reset config check`.

**Downstream consumer migrations**:

- Any project shipping its own pipeline YAML that calls `devbox wait` from a shell step must migrate to `type: builtin` `cmd: docker_wait_healthy`. Flag in release notes.
- Any documentation, scripts, or onboarding flows referencing the removed commands need updating. Audit the broader devbox ecosystem (sample projects, internal docs) at release time.

**Release notes / changelog**:

- **Breaking**: `devbox wait`, `devbox docker wait`, `devbox up`, `devbox down`, `devbox logs`, `devbox ps`, `devbox reset config check` are removed. Use `devbox docker {up,down,logs,ps}` for the passthrough, `devbox run` for the full lifecycle, the `docker_wait_healthy` builtin for in-pipeline waiting, and `devbox validate config reset` for reset config validation.
- **New**: `devbox validate` for static project validation with severity-gated exit code.
- **New**: `docker_wait_healthy` pipeline builtin.

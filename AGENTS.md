# Repository Guidelines

## Project Structure & Module Organization

This is a Go CLI project named `devbox`. Devbox is a developer tool for local development environments running on Docker. Typical flow: a developer installs Devbox locally, enters a project with Devbox configuration, runs `devbox`, and the CLI detects the current directory as a Devbox project. From there it automates deploy/setup steps, orchestrates Docker services, and runs project commands.

`AGENTS.md` is the canonical file; `CLAUDE.md` is a symlink to it. Edit `AGENTS.md` — overwriting `CLAUDE.md` will break the symlink.

The executable entrypoint lives in `cmd/devbox`; most code is under `internal/`. Tests sit next to code as `*_test.go`; fixtures live in package-local `testdata/`. Package layout:

- `cmd/devbox/main.go` — entry point (uses `fang.Execute` for styled help/errors)
- `internal/version/` — `Version`, `Commit`, `Date`, `BuiltBy` vars; `Info()` formatter; injected via `-ldflags -X` at build time
- `internal/config/` — `DevboxConfig` struct, layered `LoadConfig()`, `LoadDeployConfig()`, `LoadDockerConfig()`, `LoadLifecycleConfig()`, `ResolvePath()`, `ExportRule`, `ComposeConfig`, `DockerConfig`, `DeployConfig`, `LifecycleConfig`, `IDEConfig`, `InfoConfig`, `LoadInfoConfig()`, `StylesConfig`, `LoadStylesConfig()`; `BinariesConfig` (devbox/docker/shell binary overrides, read from top-level `devbox.yml` only — not layered); nil-safe accessors `DevboxBin`, `DockerBin`, `ShellBin` (always go through these, never `cfg.Binaries.*` directly)
- `internal/docker/` — `Compose` struct for building and executing `docker compose` commands with policy args; `Compose.Bin` is private-by-convention — always use `Compose.BinName()` (nil/empty-safe), never read the field directly; `NewCompose(cfg, dockerCfg)` populates `Bin` via `config.DockerBin(cfg)`
- `internal/render/` — `Writer` with ANSI output methods (Success, Error, Warning, Info, Definition, TableHeader, ASCII art); plain passthrough for logs/deploy output; `Writer.Confirm` is the documented non-TTY fallback for Y/n prompts (piped stdin, CI)
- `internal/ui/` — Lipgloss styled output: `RenderSummary(cfg)` for compact root summary, `RenderInfo(cfg, infoCfg)` for full info dashboard, `RenderServiceTable()` and `RenderToolTable()` for Lipgloss tables, `RenderTopology()` for dependency tree; `ApplyStyles(stylesCfg)` to hot-apply palette from `styles.yml` (also rebuilds the huh theme); terminal width detection; huh-backed interactive primitives: `RunSelector(title, items)` (single-pick), `RunMultiSelect(title, items)` (multi-toggle), `RunConfirm(title, affirmative, negative)` (confirmation); `Theme()` accessor for the project-palette `huh.Theme`; `IsInteractiveFn(stdin)` TTY-detection helper used by all interactive callers
- `internal/git/` — git update probe: `Probe(workDir, fetch)` returns `Status` (IsRepo, Dirty, Behind, Ahead, FetchOK, FetchErr); `PullFFOnly(workDir)` returns `moved bool`; `Decide(status, mode, isInteractive)` encodes the safety matrix (dirty/no-upstream/fetch-failed → warn; behind + auto → pull; behind + prompt + TTY → prompt-pull; behind + check → warn). Runner interface for unit-test stubbing.
- `internal/tpl/` — Go template engine with `Render()`, `EvalCondition()`, `EvalCommandCondition()` (render-first condition evaluator for workflow `when` expressions, resolves `${...}` before classifying/dispatching), custom `FuncMap` (`appURL`, `date`, `datetime`, `base`, `dir`); template funcs available in `${...}` expressions in command definitions
- `internal/builtin/` — builtin step registry: `Builtin` interface (`Validate`, `Describe`, `Run`), `ExecContext` carrier; registered builtins: `configs_copy`, `confirm`, `volumes_create`, `service_dirs_ensure` (creates service hub dirs with skip/error/recreate modes), `message` (outputs text at info/success/warning/error level with Go template support)
- `internal/pipeline/` — deploy/reset reporter abstraction: `Reporter` interface (StartPipeline, EnterPhase, SkipPhase, StartStep, SkipStep, FinishStep, FailStep, FinishPipeline, SuspendForExec, ResumeAfterExec); `PlainReporter` — the sole reporter; outputs icons (✓ ✗ ◎ ·), suppresses untracked phase output, prints elapsed time in `FinishPipeline`; `Run(cfg, reporter, steps, ...)` generic executor
- `internal/deploy/` — deploy plan resolution: `ResolvePlan`, `ResolveServicePlan`, `FindStep`; assembles `DeployConfig` phases into executable steps
- `internal/reset/` — reset plan resolution: `ResolvePlan`, `FindStep`; mirrors deploy for the reset pipeline
- `internal/lifecycle/` — lifecycle pipeline execution: `RunPhases(cfg, lifecycleCfg, ...)` drives run/stop pipelines from `devbox/lifecycle.yml`
- `internal/stack/` — Docker Compose topology + health: `AggregateHealth`, `FetchComposeTopology`, `ParseTopologyFromFiles`; augments compose state with devbox service config
- `internal/envfile/` — `.env` file generation: `BuildContent(cfg)`, `Regenerate(configPath)`; no cobra or UI dependencies
- `internal/localconfig/` — local YAML toggle helpers: `LoadLocalYAML`, `WriteLocalYAML`, `SetLocalEntryEnabled`, `ValidateServiceToggle`; no envfile dependency
- `internal/usercommands/` — declarative command system facade (re-exports all public symbols from subpackages):
  - `usercommands/model/` — pure types: `CommandDef`, `CommandFile`, `ParamDef`, `ContextDef`, `FileSpec`, `WorkflowStep`, all enums (`CommandType`, `ParamType`, `ExecMode`, `UserMode`, `FileAccess`, `FileSort`, `FileOnError`)
  - `usercommands/loader/` — YAML discovery and parsing: `DiscoverCommandFiles`, `LoadCommandFile`, `ComputeGroup`, `ComputeCommandID`
  - `usercommands/registry/` — command registry: `Registry`, `LoadRegistry`, `GroupNode` tree, cross-ref validation
  - `usercommands/resolve/` — param/context/env resolution: `Params`, `Context`, `BuildEnv`
  - `usercommands/runtime/` — runners and execution: `RunContext`, `Runner` interface, `NewRunner`, `RunCommand`, `ConfirmCommand`, `HostRunner`, `DevboxRunner`, `ServiceExecRunner`, `ServiceRunRunner`, `ScriptRunner`, `WorkflowRunner`, file path computation. Script contract env: `DEVBOX_BIN`, `DEVBOX_FILES_JSON`, `DEVBOX_NONINTERACTIVE`
- `internal/condition/` — deploy step `when:` expression evaluator: Go template kind (delegates to `internal/tpl`), builtin predicates (`dir-empty`, etc.), shell-command kind (`cmd: ...`). Intentionally uses hardcoded `sh -c` for shell commands rather than `config.ShellBin` — conditions must be predictably POSIX-portable regardless of the project's configured shell.
- `internal/command/` — cobra commands with Fang integration and command groups (root summary, lifecycle, services/tools, deploy/reset, docker/compose, docs); thin adapters delegating to domain packages. `docs generate --scope cli` works without a project (outputs to cwd); `--scope all`/`--scope commands` requires a project.
- `internal/project/` — project discovery: `Locate(flag)` (upward walk from cwd; explicit mode when flag non-empty), `ValidateSchema(path)` (schema_version must be "2"), `Resolve(flag)` (compose Locate + ValidateSchema); `ErrNotFound` sentinel; constants `ConfigFilename`, `SchemaField`, `SupportedSchema`

## Configuration Documentation

Keep behavior and docs aligned. Devbox configuration documentation lives in `docs/reference/config`. It describes project config files, Docker/service orchestration, deploy flows, lifecycle hooks, and user commands. Update it when changing schemas, commands, service toggles, deploys, or hooks.

## Build, Test, and Development Commands

- `make build` runs `go mod tidy`, builds `./cmd/devbox`, and writes `bin/devbox`.
- `make test` runs the full test suite with `go test ./...`.
- `make test-v` runs tests with verbose output.
- `make lint` installs `golangci-lint` if missing, then runs checks.
- `make tidy` updates `go.mod` and `go.sum`.
- `make clean` removes the built binary from `bin/`.

Use `go test ./internal/command` or `go test ./internal/usercommands/... -run TestName` for focused work.

## Coding Style & Naming Conventions

Follow `.editorconfig`: Go files use tabs with width 4; YAML, JSON, and shell files use two spaces. Run `gofmt` and `goimports`; `golangci-lint` enforces `errcheck`, `govet`, `staticcheck`, `revive`, `gocritic`, and `modernize`.

## Testing Guidelines

Prefer table-driven tests for command parsing, config resolution, Docker orchestration, and runner behavior. Keep fixtures in `testdata/` and avoid developer-specific Docker state unless the package isolates it. Every behavior change should update nearby `*_test.go` coverage. Run `make test` before opening a PR.

## Commit & Pull Request Guidelines

Recent history uses conventional-style commits such as `feat(commands): ...`, `fix(commands): ...`, `docs(config): ...`, and `refactor(commands)!: ...`. Keep the scope tied to the package or user-facing area, and use `!` only for breaking changes. Pull requests should include a summary, verification commands, linked issues when applicable, and screenshots or terminal output when changing CLI presentation.

## Key Patterns

- **Completion helpers**: Cobra's hidden `__complete` path bypasses `PersistentPreRunE`, so `ValidArgsFunction` callbacks cannot rely on the project being resolved. All such callbacks must call `completionConfigPath(flags, cmd)` (defined in `internal/command/completion.go`) before any config-dependent work. On any error (`ErrNotFound`, schema, bad `-c` path) return empty completions and `cobra.ShellCompDirectiveNoFileComp` silently.
- **Binary accessors**: Never read `cfg.Binaries.*` fields directly. Always use nil-safe accessors: `config.DevboxBin(cfg)`, `config.DockerBin(cfg)`, `config.ShellBin(cfg)`. Deploy/condition step `when:` predicates intentionally use hardcoded `sh` (not `ShellBin`) for portability.

## Agent-Specific Instructions

Do not overwrite unrelated local changes. Keep command examples current with the `Makefile`. When changing CLI behavior, update tests and config docs.

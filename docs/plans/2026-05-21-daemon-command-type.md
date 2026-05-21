# `type: daemon` — declarative background named containers

## Overview

Add a new declarative command type `type: daemon` that gives users a single YAML block to describe long-running, parameterised, ad-hoc processes inside devbox services (canonical example: Laravel queue workers). One block expands at registry-load time into **four first-class virtual commands**:

| ID | Behaviour | Blocking |
|---|---|---|
| `<base>.start` | `docker compose run -d --name <full> ...` | no |
| `<base>.logs` | `docker logs -f --tail=100 <full>` | yes (Ctrl-C detaches) |
| `<base>.stop` | `docker stop -t <timeout> <full>` | no |
| `<base>.restart` | `<base>.stop` followed by `<base>.start` | no |

Each virtual command appears in the registry, in `devbox cmd` browser, in completion, in `inspect`, and is referenceable from workflows. The container name is auto-prefixed with `ProjectConfig.FullName()` to prevent cross-project collisions, and every container carries standardised labels (`devbox.project`, `devbox.daemon.id`, `devbox.daemon.params`) so `status` and lifecycle-reap can find them via `docker ps` — **no separate state file**.

Replaces the workaround of hand-rolled `type: shell` running `docker run --name ...`, which duplicates the container name across three places and is invisible to `status` / `lifecycle`.

## Context (from discovery)

Files / packages touched:

- **Model**: `internal/usercommands/model/types.go` — `CommandType` enum (line 19–38), `CommandDef` struct (line 357–428), validation methods (`validateServiceType` at ~line 553 as the template for `validateDaemonType`).
- **Loader**: `internal/usercommands/loader/loader.go` — strict-decode runs via `model.ParseCommandFile` (`KnownFields(true)`), `LoadCommandFile` calls `cf.Validate()`.
- **Registry**: `internal/usercommands/registry/registry.go` — `LoadRegistry` populates `byID` + `GroupNode` tree by splitting ID on `.`; collisions raise here. This is the right hook for daemon expansion.
- **Builtins**: `internal/builtin/builtin.go` — `Builtin` interface (`Validate`/`Describe`/`Run(ctx, with, ectx)`), `Builtins` registry map, `ExecContext` providing `Config` / `ProjectRoot` / `Output` / `ConfirmFunc`. Reference impl: `internal/builtin/wait_healthy.go`.
- **Service runner reuse**: `internal/usercommands/runtime/runner_service.go` — `resolveServiceFields`, `buildServiceArgv`, `buildDockerComposeCmd`; `runner_host.go` — `buildRenderedEnv`. The `.start` builtin will reuse these helpers.
- **Status section**: `internal/command/status.go` — `Section` enum + `defaultSectionOrder`, `loadStatusContext`; `internal/stack/status.go` — section renderer pattern (`RenderServices` returns `(string, []error)`); `internal/ui/` — table renderers.
- **Lifecycle**: `internal/config/devbox.go:1298` — `LoadLifecycleConfig` returns `os.ErrNotExist` on missing file (callers handle); `internal/lifecycle/stop.go:30` — `RunStop` currently translates `os.ErrNotExist` to a **hard error** and additionally errors on `cfg.Stop == nil`. Task 7 changes this contract: stop becomes valid without `lifecycle.yml`, synthesising a reap-only phase via `lifecycle.EnsureStopConfig`.
- **Validate**: `internal/validate/commands/commands.go:124` — `Validator.Run` iterates parsed files and emits categorised `Diagnostic`s, then **falls back** to per-command `cmd.Validate()` for uncategorised errors. Daemon-specific diagnostics must mark `categorisedCmds[...]=true` to suppress double-emission.
- **Workflow / parallel guard**: two enforcement points, both hard-coded to `confirm`:
  - **plan-time**: `internal/pipeline/resolve.go:261` `checkInteractive` returns `ErrInteractiveInParallel` only for `step.Cmd == "confirm"`; transitive walker `checkCommandInteractive` at `:270` recurses into workflow steps
  - **runtime**: `internal/usercommands/runtime/runner_builtin.go:38` returns `ErrConfirmInsideParallel` only for `name == "confirm"`
  - Task 5 centralises both via `builtin.IsInteractive(name) bool`.
- **Completion**: `internal/command/completion.go` — `completionConfigPath`; per-flag completions registered via `cmd.RegisterFlagCompletionFunc`.
- **Project full name**: `internal/config/devbox.go` — `ProjectConfig.FullName()` returns `"<prefix>-<name>"` or `"<name>"`.

Related patterns:

- All argv that contain template literals already flows through `tpl.RenderCommand` with the command-scope func map — container-name template will reuse it (`internal/tpl/`).
- Builtins are publicly addressable as `type: builtin` from any command-file or pipeline step. The daemon expansion will lean on this: each virtual command is a regular `CommandDef` whose `Type` is `builtin` (or `workflow` for `.restart`).
- `pathsafe` is not relevant here — no filesystem escape; the container-name template is sanitised via a strict regex post-render.

## Development Approach

- **Testing approach**: Regular (code first, then unit tests per task — matches recent plans in this repo).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task — tests are a required deliverable, not optional.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task; `make lint` before the final verify task.

## Testing Strategy

- **Unit tests**: required for every task. Patterns:
  - Model: parse known-good and known-bad YAML fixtures; assert `Validate()` errors.
  - Builtins: extract argv-building into testable funcs; do not shell out from tests. The `docker.Compose` calls go through a seam that returns the argv slice so tests can assert without invoking docker.
  - Registry expansion: load a fixture command-file with `type: daemon` → assert four `CommandDef`s materialised under the expected IDs with the expected `Type` / `Cmd` / `With`.
  - Validate: feed fixtures with bad daemon blocks; assert exactly the expected diagnostics surface (severity, target, hint).
  - Status section: feed a fake `docker ps` JSON output via a seam; assert rendered string snapshot.
- **No E2E**: this repo does not have UI e2e tests. Integration-level docker invocations are out of scope for the unit suite; they belong in post-completion manual smoke.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable inside this repo — code, unit tests, reference docs.
- **Post-Completion** (no checkboxes): manual docker smoke against a real fixture project, integration into example template packs.

## Implementation Steps

### Task 1: Model — `CommandType = "daemon"` and `DaemonSpec`
- [x] add `CommandTypeDaemon CommandType = "daemon"` to the enum in `internal/usercommands/model/types.go`
- [x] add **two fields** to `CommandDef`:
  - `Daemon *DaemonSpec` — `yaml:"daemon"`, the YAML schema field, populated only on commands whose `Type == CommandTypeDaemon`
  - `SourceDaemon *DaemonSpec` — `yaml:"-"`, **expansion-time metadata** populated by the registry expander on synthetic `.start/.logs/.stop/.restart` commands (`type: builtin`/`workflow`). Never loaded from YAML; never validated by `validateBuiltinType`/`validateWorkflowType`. Inspect reads this field (task 10) without crossing validator boundaries
  - **Separation rationale**: `validateDaemonType` (below) forbids `Daemon` on non-daemon types; if expansion populated `Daemon` directly on synthetic commands, any post-expansion `cmd.Validate()` call (including in tests) would mis-fire. The `yaml:"-"` tag on `SourceDaemon` also keeps it out of strict-decode collisions when daemon YAML is re-serialised in any future docs path
- [x] `DaemonSpec` fields per spec section 4:
  - `ContainerTemplate string` — `yaml:"container_template"`
  - `OnAlreadyRunning string` — `yaml:"on_already_running"`, default `"error"`
  - `AutoRemove *bool` — `yaml:"auto_remove"`, default true
  - **`StopTimeout string`** — `yaml:"stop_timeout"`, raw YAML string (e.g. `"10s"`, `"500ms"`); empty → runtime uses 10s default. **Do NOT use `time.Duration` here** — YAML decoder for `time.Duration` rejects malformed strings before validation runs, so `validate/commands/daemon.go` can never surface a categorised "bad stop_timeout" diagnostic; the user just sees a generic YAML parse error. Storing raw string lets validation parse + report with file/line/hint and lets the builtin parse at runtime with a defensive default
  - `Controls []string` — `yaml:"controls"`, default all four
- [x] add `validateDaemonType()` paralleling `validateServiceType`. **Operates on `Daemon` only** — forbids `Daemon` on non-daemon types; does NOT inspect or reject `SourceDaemon` (expansion-time metadata, invisible to YAML schema).

  **Runtime-critical checks live HERE in model validation** — `LoadRegistry` → `loader.LoadCommandFile` → `cf.Validate()` is the only validation path that runs before normal command execution. `validate/commands` runs ONLY under `devbox validate`. If a check is needed to prevent runtime panics, silent acceptance of broken input, or undocumented features, it MUST be in the model. The model checks are:
  - `service:` required (runtime: builtins read it from `with:` as a non-empty string)
  - **`service:` is literal** — must not contain `${` or `{{` substrings. Without this, `renderBuiltinWith` (`runner_builtin.go:57-93`) would silently render `service: app-${param.X}` at runtime, giving users a working-but-undocumented feature that's painful to constrain later (spec section 16: out of scope for v1)
  - `daemon.container_template:` required, non-empty (runtime: builtin's `ResolveContainerName` would have nothing to render)
  - `daemon.on_already_running` ∈ `{error, noop, ""}` (empty = default `"error"`)
  - `daemon.stop_timeout` (raw string): if non-empty → `time.ParseDuration` must succeed; result must be `> 0`. **Do not reject sub-second durations** — runtime clamps to 1s per invariant #9. Model rejection here gives users the parse error at `cmd.Validate()` time instead of at first `.stop` invocation
  - `daemon.controls`: each entry must be in `{start, logs, stop, restart}`; if `restart` is present, `start` AND `stop` must also be present (registry expander composes `.restart` from `.stop` + `.start` — without both, expansion fails)

  Each check emits a stable, parseable error string (e.g. `"daemon: service must be literal"`, `"daemon: container_template required"`, `"daemon: stop_timeout parse: ..."`, `"daemon: controls: restart requires start and stop"`) so the validate-commands suppressor can match per-field (task 6). Use `errors.Join` to surface multiple field errors from one call to `cmd.Validate()` rather than short-circuiting on first failure

  **`container_template` param-reference checks** (every `${param.X}` references a declared param with a `pattern:` set) stay in `validate/commands/daemon.go` — they need walk of the param table + hints, which model validation should not own
- [x] wire `validateDaemonType` into `CommandDef.Validate()` switch
- [x] write tests for parse + validate of daemon CommandDef:
  - happy path
  - missing `service:`
  - `service: app-${param.name}` → rejected (`service must be literal`)
  - `service: "{{.queue.service}}"` → rejected
  - missing `container_template`
  - bad `on_already_running`
  - `stop_timeout: "10s"` accepted; `stop_timeout: "not-a-duration"` → model `cmd.Validate()` rejects (raw YAML decode still succeeds — the test proves YAML accepts the string AND `cmd.Validate()` then rejects with a categorised error)
  - `stop_timeout: "-5s"` → rejected (`must be positive`)
  - `controls: [start, restart]` → rejected (`restart requires stop`)
  - `controls: [start, stop, foo]` → rejected (`unknown control: foo`)
  - multi-field failure: missing service + bad on_already_running → `errors.Join` surfaces both, not just first
- [x] run `go test ./internal/usercommands/model/...` — must pass before task 2

### Task 2: Daemon name + label resolver
- [x] create `internal/daemon/` **top-level** package (peer to `internal/lock`, `internal/git`, `internal/envfile`). Rationale: cross-cutting domain utility — consumed by `internal/builtin`, `internal/stack` (status), `internal/validate/commands`, and `internal/command` (completion + inspect). Placing it under `internal/usercommands/` would force every consumer to import a usercommands subtree they're otherwise decoupled from.
- [x] implement `ResolveContainerName(projectFullName, renderedTemplate string) (name string, err error)`: takes the **already-rendered** container template string (`renderBuiltinWith` resolves `${param.X}` upstream), prepends `<projectFullName>-`, validates the final string against regex `^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`. **This regex is the authoritative security boundary for argv → docker `--name`**: even if a malformed `pattern:` in YAML lets through bad chars, this gate fails closed. Returns typed `ErrContainerNameInvalid` on regex fail. (No `tpl.RenderContext` dependency here — rendering happens at the runner layer, not in this pure helper.)
- [x] implement `StandardLabels(projectFullName, daemonID string, params map[string]any) []string` returning the three `--label key=value` argv pairs. **The `devbox.daemon.params` value MUST be produced via `encoding/json.Marshal(params)` — never `fmt.Sprintf`**; otherwise param values containing `"`, `\`, or control chars break the JSON shape and `status` parsing on read-back.
- [x] implement `FilterArgsByLabels(projectFullName, daemonID string) []string` returning `--filter label=...` slices for `docker ps` reuse (passed as separate argv elements, never shell-concatenated).
- [x] define and export the label-key constants (`LabelProject`, `LabelDaemonID`, `LabelDaemonParams`) so consumers don't string-literal the keys.
- [x] write unit tests for name resolution (with and without project prefix, template rendering, regex rejection of `;`, `$`, ``` ` ```, newlines, leading `-`, empty), and label set (JSON roundtrip with `"`, `\\`, control chars; key stability).
- [x] run `go test ./internal/daemon/...` — must pass before task 3

### Task 3: Three `docker_daemon_*` builtins

**Import-cycle constraint** (CRITICAL): `internal/usercommands/runtime` imports `internal/builtin`, so builtins **cannot** import from runtime. Strategy:

1. **No runtime imports.** Builtins read all needed fields directly from the rendered `with:` map. The runtime layer (`renderBuiltinWith` in `runner_builtin.go:60`) already templates every string value in `with:` (including nested maps and lists) against the command's `*tpl.RenderContext` before the builtin runs, so `${param.X}` substitutions in `argv`/`env`/`container_template`/`label_params` are already resolved by the time `Run` is called.
2. **Registry expansion (task 4) packs into `with:`:** `service`, `user`, `workdir`, `workdir_from`, `env` (map), `argv` (list), `compose_args` (list), `auto_remove`, `stop_timeout`, `on_already_running`, `container_template`, `daemon_id`, **`label_params` (map of param-name → template literal `"${param.<name>}"`)**.  Notably **excluded**: `project_full_name` — registry has no `*config.DevboxConfig` at `LoadRegistry(baseDir)` time. Builtins compute project full name at runtime from `ectx.Config.Project.FullName()`.
3. **`label_params` runtime resolution:** for each declared param `<name>`, the expander writes `label_params[<name>] = "${param.<name>}"`. `renderBuiltinWith` walks the map and renders each value, yielding `label_params: {name: "default", queue: "emails"}` at builtin entry. The builtin `json.Marshal`s this resolved map for the `devbox.daemon.params` label. (The original "snapshot of `ParamDef` declarations" idea was wrong — labels need runtime values, not declarations.)
4. **`workdir_from:` dot-path resolution** currently lives in unexported `runtime/runner_service.go`. Extract the dot-path lookup to an exported `config.LookupDotPath(cfg *config.DevboxConfig, path string) (any, error)` so both runtime and builtin can use it. Runtime's `resolveServiceFields` becomes a thin wrapper.
5. **All docker invocations must go through `docker.Compose` policy plumbing.** Building argv inline with raw `exec.Command("docker", "compose", ...)` bypasses `docker.yml`'s `project_name`, compose `-f` files, global args, per-command defaults, configured binary, and `process_env`. Builtins receive `dockerCfg` via `ectx.DockerConfig` (see point 7); they DO NOT call `config.LoadDockerConfig` themselves.
   - **For `docker compose run`** (used by `.start`): `compose := docker.NewCompose(ectx.Config, ectx.DockerConfig)` then `args := compose.BuildArgs("run", extraArgs...)`. Env via `compose.BuildEnv()`. Binary via `compose.BinName()`.
   - **For raw `docker logs` / `docker stop` / `docker ps`** (`.logs`, `.stop`, `daemons_reap`, completion, status): not compose subcommands; use `compose.BinName()` + `compose.BuildEnv()` for binary + `ProcessEnv` propagation. Skipping silently breaks remote-docker / podman / non-default-context setups.
6. **`-e KEY` argv + value via `cmd.Env`** — never `-e KEY=VAL` in argv. Existing `runner_service.go:320-322` passes only the key as argv (`for k := range envVars { args = append(args, "-e", k) }`) and supplies the value via `cmd.Env` (merged into `compose.BuildEnv()` via `docker.MergeEnv`). This keeps secret values out of the host process argv where `ps`/`/proc/<pid>/cmdline` would expose them — matches the docs telling users to put secrets in `env:` rather than `params:`. Daemons MUST follow this pattern.
7. **Docker config nil-tolerance & threading.** `config.LoadDockerConfig` returns `nil, os.ErrNotExist` when `devbox/docker.yml` is absent. `docker.NewCompose` then panics dereferencing `dockerCfg.Args`. The fix already exists in one path (`build_context.go:63-70` normalises to `&config.DockerConfig{}` on `os.ErrNotExist`). Apply the same normalisation everywhere docker config is loaded — and load it ONCE per top-level invocation, then thread through:
   - Add `DockerConfig *config.DockerConfig` field to `builtin.ExecContext` (`internal/builtin/builtin.go:39`)
   - Wire it at `runner_builtin.go:47` from `rc.DockerConfig` (already loaded by `BuildRunContext`)
   - Wire it at the pipeline executor's ExecContext construction site (whichever package builds ExecContext for pipeline-step builtins — same path as `daemons_reap` invocation from auto-injected lifecycle stop phase)
   - Wire it at the status orchestrator: `loadStatusContext` loads `dockerCfg` once, normalises missing-file to `&DockerConfig{}`, passes to `CollectDaemons`
   - Wire it at the completion shellout: load once after `completionConfigPath`, normalise, build `compose`
8. **Docker argv assembly is built inline** in each builtin around the `BuildArgs` / `BuildEnv` calls above — no shared helper extraction needed in this plan.

Checklist:

- [x] extract `config.LookupDotPath(cfg *config.DevboxConfig, path string) (any, error)` from `runner_service.go`'s unexported resolver; runtime's `resolveServiceFields` becomes a thin wrapper over it; add unit tests for `services.<name>.work_dir_internal`-style paths and error cases (missing key, non-string leaf)
- [x] verify `docker.Compose.BuildEnv() []string` is exported (line 122 of `compose.go` invokes it on the receiver — confirm method visibility). If not exported, promote it. Also confirm it's safe to call from a non-compose invocation context (it should only return `os.Environ()` merged with `ProcessEnv` — no compose-specific assumptions)
- [x] **add `DockerConfig *config.DockerConfig` to `builtin.ExecContext`** (`internal/builtin/builtin.go:39`); update **all** call sites that construct `ExecContext` to populate it:
  - `runner_builtin.go:47` — set `DockerConfig: rc.DockerConfig` (already loaded by `BuildRunContext` at `build_context.go:63-70` with `os.ErrNotExist` → `&DockerConfig{}` normalisation)
  - pipeline executor's ExecContext build site (used for pipeline-step builtins including auto-injected `daemons_reap`) — load `dockerCfg` once at the executor's setup, apply the same `os.ErrNotExist` → `&DockerConfig{}` normalisation, pass through to every step's ExecContext
- [x] add `internal/builtin/daemon_start.go` — `docker_daemon_start`:
  - read pre-rendered fields from `with:` (`service`, `user`, `workdir`/`workdir_from`, `env`, `argv`, `compose_args`, `auto_remove`, `on_already_running`, `container_template`, `daemon_id`, `label_params`)
  - resolve `workdir_from` via `config.LookupDotPath(ectx.Config, ...)` when `workdir` is empty
  - compute `projectFullName := ectx.Config.Project.FullName()` at runtime (NOT from `with:`)
  - call `daemon.ResolveContainerName(projectFullName, with["container_template"].(string))` → `<full>`
  - call `daemon.StandardLabels(projectFullName, with["daemon_id"].(string), with["label_params"].(map[string]any))` → returns `--label` argv pairs (with `json.Marshal` on the resolved label_params map)
  - `compose := docker.NewCompose(ectx.Config, ectx.DockerConfig)` — **DockerConfig comes from `ectx`, do NOT call `config.LoadDockerConfig` here** (invariant #7)
  - **Argv assembly order — match `runner_service.go:293-325`** so daemons follow the same command-fields-win-on-conflict semantics as service_run AND inherit its dependency / entrypoint guarantees:
    1. `extraArgs := []string{"-d"}`
    2. append `"--no-deps", "--entrypoint", ""` — **MUST match `runner_service.go:298`**. Without `--no-deps`, `docker compose run` would start the service's `depends_on` chain (already running via `devbox up`); without `--entrypoint ""`, the image's ENTRYPOINT would prefix the user's `argv:`, defeating the explicit command override. Both are non-optional invariants of `service_run`-style execution
    3. if `auto_remove != false`: append `"--rm"`
    4. append `"--name", <full>`
    5. append `compose_args...` from `with:` **(before command-owned fields, so user/workdir/env below override on conflict)**
    6. append `"--user", <user>` (or `"--user", <uid>:<gid>` for `current` mode, `"--user", "root"` for `root` mode — match the switch at `runner_service.go:303-314`)
    7. append `"--workdir", <workdir>` if non-empty
    8. append `"-e", "KEY"` for each env key — **key-only argv, NEVER `-e KEY=VAL`** (invariant #6); values go into `cmd.Env` via `docker.MergeEnv(ProcessEnv ∪ envVars)`
    9. append the `--label` argv pairs from `StandardLabels`
    10. append `<service>`, then `<argv...>`
  - `args := compose.BuildArgs("run", extraArgs...)`; `cmd := exec.CommandContext(ctx, compose.BinName(), args...)`; `cmd.Env = docker.MergeEnv(combined)` where `combined = compose.ProcessEnv ∪ envVars` (the env-values pathway — mirrors `runner_service.go:329-332`)
- [x] **TOCTOU mitigation**: pre-check `docker ps -q --filter name=^<full>$ --filter status=running` (with `BinName()` + `BuildEnv()`) is best-effort. **Authoritative** anti-duplicate is docker's atomic name-uniqueness; the builtin MUST parse the docker name-conflict error string and translate to `ErrDaemonAlreadyRunning` so `on_already_running: noop` correctly swallows it under race
- [x] add `internal/builtin/daemon_logs.go` — `docker_daemon_logs` builtin: resolves full name from `with:` + runtime `ectx.Config.Project.FullName()`, pre-checks existence (returns typed `ErrDaemonNotRunning` with hint pointing to `.start`), builds `compose := docker.NewCompose(ectx.Config, ectx.DockerConfig)` (DockerConfig from `ectx`, not loaded here), then runs `<compose.BinName()> logs -f --tail=100 <full>` via `exec.CommandContext(ctx, ...)` with `cmd.Env = compose.BuildEnv()` (ProcessEnv applied) wired to `ectx.Output` stdout/stderr
- [x] **Cancellation semantics (golang-context)**: set `cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }` and `cmd.WaitDelay = 3 * time.Second` so Ctrl-C → ctx cancel → SIGINT (graceful detach of `docker logs`, container untouched) rather than default SIGKILL. Container itself is never signalled by `.logs`
- [x] add `internal/builtin/daemon_stop.go` — `docker_daemon_stop` builtin: convert `stop_timeout` duration to whole seconds via `max(1, int(d.Round(time.Second).Seconds()))` (see *Technical Details § implementation invariant #9*); builds `compose := docker.NewCompose(ectx.Config, ectx.DockerConfig)`; runs `<compose.BinName()> stop -t <secs> <full>` via `exec.CommandContext` with `cmd.Env = compose.BuildEnv()`; missing-container always succeeds (`if_missing: noop` fixed — parse the "No such container" docker error); prints `✓ daemon stopped: <full>` or `no daemon to stop`
- [x] define sentinel errors in `internal/daemon/`: `ErrContainerNameInvalid`, `ErrDaemonAlreadyRunning`, `ErrDaemonNotRunning`. Wrap docker stderr via `%w` when bubbling unknown failures
- [x] register all three in the `Builtins` map in `internal/builtin/builtin.go`
- [x] write unit tests for argv-building (extract assembly into pure funcs taking primitives):
  - each builtin's `with:` shape
  - assembled argv goes through `compose.BuildArgs` (assert `-p`, `-f`, configured global args, run defaults all appear)
  - env includes `ProcessEnv` and per-call env vars (via `MergeEnv`); binary is taken from `DockerBin(cfg)` (test with `podman`)
  - **`-e KEY` argv pattern** — for every env entry, exactly one `-e` followed by key-only argv element; assert that no argv element contains `=` for env keys; values appear only in `cmd.Env`. Include a test with a value containing whitespace and shell metacharacters to verify it does NOT appear in argv
  - **argv ordering**: `compose_args` appears before `--user`/`--workdir`/`-e`/`--label`/service/argv — matches `runner_service.go:301-325`
  - **`--no-deps` and `--entrypoint ""` present**: argv after `-d` includes both flags unconditionally; absent → test fails. Matches `runner_service.go:298`
  - `on_already_running` (error vs noop + TOCTOU race translation via stubbed docker stderr)
  - `auto_remove: false`
  - custom `stop_timeout` (rounding at 0.4s / 0.6s / negative)
  - that no shell metacharacters in param values escape argv boundaries
  - that `label_params` after rendering produces a valid JSON string in the `--label` argv
  - **nil-tolerance**: builtin invoked with `ectx.DockerConfig = &DockerConfig{}` (empty zero value) does not panic; argv excludes `-p`/`-f` when project_name/files are empty
- [x] run `go test ./internal/builtin/... ./internal/config/...` — must pass before task 4

### Task 4: Registry-time expansion of `type: daemon` into four virtual `CommandDef`s

**Constraint**: `registry.LoadRegistry(baseDir string)` (registry.go:55) has no `*config.DevboxConfig` — it operates purely on YAML files under `baseDir`. The expander therefore **cannot** look up project full name, dot-path resolutions, or anything else config-derived. Anything config-derived must be resolved at builtin runtime via `ectx.Config`. Anything param-derived must be expressed as a template literal `${param.<name>}` so `renderBuiltinWith` resolves it at command dispatch.

**The source `<base>` command is consumed by expansion, not retained.** `runtime.NewRunner` has cases for `service_run` / `service_exec` / `shell` / `builtin` / `workflow` etc. — no `daemon` case, and there will be none (daemon is a declarative sugar type that only exists in YAML, never as an executable command). If the source `<base>` were left in `byID`, `devbox cmd <base>` would resolve to a CommandDef with `Type: daemon` and fall through to runner's "unsupported type" error path. The expander therefore inserts the FOUR virtual commands and **does NOT insert the source** — `reg.byID["<base>"]` returns not-found; only `<base>.start`/`.logs`/`.stop`/`.restart` are present.

- [x] in `internal/usercommands/registry/registry.go` `LoadRegistry`, after a command-file is parsed but **before** the existing insertion loop at line 79-88, expand each `CommandDef` whose `Type == CommandTypeDaemon` into a slice of synthetic CommandDefs and **swap the daemon entry out of `cf.Commands`** (or use a separate processing pass) so the main loop inserts the synthetics and never sees the source daemon. Concretely:
  - the source `<base>` command is **dropped** as an executable — not inserted into `byID`, not runnable. `reg.Get("<base>")` returns the not-found error
  - **a synthetic group node IS created for `<base>`** via `ensureGroup(<base>)`. The four virtual commands are inserted as children of that group node so `devbox commands tree <base>` (`command_cmd.go:492` uses `findGroupNode` over the group tree, NOT command-ID prefix matching) finds the daemon's commands. Without this group node, the tree command silently returns empty for the daemon's ID even though the four synthetics exist in `byID`. Group meta (title/description) on the synthetic group is populated from the source daemon's `Description:` so the tree renders the human-readable label
  - if `Description:` was set on the source daemon, propagate it to all four synthetic commands as well so browser hover / inspect shows it
  - the synthetic commands produced (each is a regular `CommandDef` inserted into `byID` like any other):
  - `<base>.start` → `Type: builtin`, `Cmd: "docker_daemon_start"`, `With:` populated with the daemon block + base command's `Service`/`User`/`Workdir`/`WorkdirFrom`/`Env`/`Argv`/`ComposeArgs` + **`label_params`** (see below). `Params:` field is also copied so the param form renders correctly
  - `<base>.logs` → `Type: builtin`, `Cmd: "docker_daemon_logs"`, `With:` populated with `container_template` + `daemon_id` + `label_params`. `Params:` copied
  - `<base>.stop` → `Type: builtin`, `Cmd: "docker_daemon_stop"`, `With:` populated with `container_template` + `daemon_id` + `label_params` + `stop_timeout`. `Params:` copied
  - `<base>.restart` → `Type: workflow`, `Steps:` referencing `<base>.stop` then `<base>.start` by ID. `Params:` copied. **`With:` map MUST be populated for both steps with one `<name>: "${param.<name>}"` entry per declared param** — workflow children only receive params via `step.With` (`runner_workflow.go:181-188`); parent workflow params are NOT auto-inherited. Without this, `devbox cmd queue.restart --set name=emails` would silently restart the default daemon (param falls back to its `Default`) instead of `emails`
- [x] **`label_params` map construction** (correct shape — fix vs. earlier reviewer flag): for each declared param `<n>` in the daemon's `Params:`, set `with["label_params"].(map[string]any)[<n>] = "${param." + n + "}"`. The map's KEYS are literal param names; the VALUES are template strings that `renderBuiltinWith` resolves at runtime to the user's `--set` values. Result at builtin entry: `label_params: {name: "default", queue: "emails"}` — the resolved runtime values, ready for `json.Marshal` into the `devbox.daemon.params` label
- [x] **Do NOT include in `with:`**: `project_full_name` (no config at expansion time — builtins compute via `ectx.Config.Project.FullName()`), `compose_files` (built by `docker.NewCompose` at runtime), `docker_bin` (read via `config.DockerBin` at runtime). These are all `*config.DevboxConfig`-derived and only exist at builtin dispatch
- [x] **Daemon-metadata visibility for inspect (Task 10)**: synthetic commands need access to the source daemon's structural fields (`ContainerTemplate`, `OnAlreadyRunning`, `StopTimeout`, `AutoRemove`, etc.) so Task 10's inspect renderer can show them without round-tripping `with:`. **Copy the parsed `*DaemonSpec` from the source daemon onto each synthetic command's `SourceDaemon` field** (the `yaml:"-"` expansion-time metadata field added in task 1). **Do NOT populate `Daemon`** on synthetic commands — they are `type: builtin`/`workflow`, and `validateDaemonType` would reject a populated `Daemon` on them. Runtime never reads `SourceDaemon` (or `Daemon`) on synthetic commands — it reads pre-rendered `with:` (invariants #1, #15) — but inspect does. Document this clearly in registry expansion code comments to prevent future drift
- [x] write tests:
  - registry expansion produces 4 entries with correct IDs / types / `With:` payloads (including the `<name>: "${param.<name>}"` entries on `.restart`'s steps)
  - **source `<base>` is NOT runnable**: `reg.Get("<base>")` returns the not-found error (not a daemon-typed CommandDef); `reg.Get("<base>.start")`, `.logs`, `.stop`, `.restart` all return non-nil
  - **`<base>` does not appear in `gn.Commands`** for any group node — only the four synthetics do (they appear as children of the new `<base>` group node)
  - **`devbox commands tree <base>` resolves**: a fixture project with `services.main.queue` daemon → `findGroupNode(root, "services.main.queue")` returns non-nil and lists the four synthetic commands
  - `<base>.restart` workflow forwards params: a fixture daemon with `params: {name: {default: default}}`, registry expanded, then invoke `.restart` with `--set name=emails` → both `.stop` and `.start` invocations receive `name=emails` (not `default`). Use a runner test seam that captures the resolved param map per child invocation
  - synthetic `CommandDef.SourceDaemon` is populated on all four virtual commands (inspect-metadata test); `CommandDef.Daemon` is nil on synthetics (passes `cmd.Validate()` cleanly without firing `validateDaemonType`'s no-leakage check)
  - `controls: [start, stop]` produces only two entries; `reg.Get("<base>.logs")` and `.restart` both return not-found
  - collision with literal `<base>.start` errors with both source-daemon and collision-target IDs in the message
  - group node tree contains the base group path
- [x] honour the `controls:` subset — omit virtual commands not listed
- [x] add a new field `DerivedFromDaemon string` on `CommandDef` (populated by the expander, used by inspect in task 9); also set `Private bool` to `false` so they're browseable
- [x] on ID collision with a literal command, error message includes both the synthetic ID and the source daemon (e.g. `command "<id>.start" auto-generated from daemon "<base>" collides with explicit command at <file>`)
- [x] run `go test ./internal/usercommands/registry/...` — must pass before task 5

### Task 5: Parallel / workflow guards for `.logs` — centralised `IsInteractive` predicate

The current code hard-codes `name == "confirm"` in **two** places (pipeline plan-time and runtime dispatch), each missing the other's site:

- pipeline plan-time: `internal/pipeline/resolve.go:261` — `checkInteractive` returns `ErrInteractiveInParallel` only for `step.Type == "builtin" && step.Cmd == "confirm"`
- runtime dispatch: `internal/usercommands/runtime/runner_builtin.go:38` — `if name == "confirm" && rc.UnderParallel && ...` returns `ErrConfirmInsideParallel`

Both must learn about `docker_daemon_logs`. Solution: a centralised registry-level predicate.

Checklist:

- [x] add `func IsInteractive(name string) bool` to `internal/builtin/builtin.go` — single source of truth. Returns true for `confirm` and `docker_daemon_logs`. Sits alongside `Validate` / `Describe` / `Run` so future interactive builtins register their flag in one place
- [x] update `pipeline/resolve.go:261` `checkInteractive` to use `builtin.IsInteractive(step.Cmd)` instead of `step.Cmd == "confirm"`
- [x] update `runner_builtin.go:38` to use `builtin.IsInteractive(name)`. **Subtle semantics**: the existing override `!rc.SkipConfirm` is meaningful for `confirm` (with `-y` the prompt auto-yes makes parallel use safe) but **not** for `.logs` (no auto-skip for a foreground tail). Keep the `SkipConfirm` override **only** for `name == "confirm"`; `.logs` always rejects under parallel regardless of `-y`
- [x] write tests for both call sites:
  - pipeline plan-time: a `deploy.yml` step with `type: builtin, cmd: docker_daemon_logs` inside a parallel group → `ResolvePlan` returns `ErrInteractiveInParallel`
  - workflow runtime: a workflow with a parallel block containing `command: <base>.logs` → runner returns `ErrConfirmInsideParallel` at run dispatch (transitive resolution; the parallel-confirm walker `checkCommandInteractive` at `resolve.go:270` must also be taught about daemon-derived commands — easiest: it sees `def.Type == builtin && IsInteractive(def.Cmd)`)
- [x] write a test for the SkipConfirm asymmetry: `confirm` + parallel + `SkipConfirm=true` → OK; `docker_daemon_logs` + parallel + `SkipConfirm=true` → still errors (runtime guard in `BuiltinRunner`; pipeline plan-time bypasses interactive checks entirely when `SkipConfirm=true` by existing design)
- [x] run `go test ./internal/builtin/... ./internal/usercommands/runtime/... ./internal/pipeline/...` — must pass before task 6

### Task 6: Plan-time validation for daemon blocks — richer diagnostics layer

**Layering** (revised — fixes earlier "model minimal" plan that left runtime exposed): model validation (task 1) owns every runtime-critical check; this validator REPLAYS the same checks but with file/line/hint context for `devbox validate`. Plus it adds checks that are too rich for the model (param-reference walks). Suppression is **per-field**, not per-command — so a partially-categorised command still surfaces uncategorised model errors.

- [x] add `internal/validate/commands/daemon.go` with these checks. Each emits one diagnostic per failing field:
  - **replays of model checks** (for file/line/hint UX — same logical assertion):
    - `service:` required (model emits `"daemon: service required"`; here: SeverityError, hint "every daemon block needs a `service:` mapping to a compose service")
    - `service:` literal — no `${...}` or `{{...}}` (hint: `"daemon service: must be literal; ${...} and {{...}} are not allowed in v1"` per spec section 16, rationale: `devbox.daemon.id` label stability)
    - `daemon.container_template` required, non-empty
    - `daemon.on_already_running` ∈ `{error, noop, ""}`
    - `daemon.stop_timeout` parseable + **`d > 0`** (any positive duration accepted; sub-second values are valid YAML — runtime clamps them up to 1s per invariant #9, NOT the validator's job to reject them). Hint: accepted forms `"10s"`, `"1m30s"`, `"500ms"` (clamped to 1s by `docker stop -t`)
    - `daemon.controls` subset of `{start, logs, stop, restart}`; if `restart` present, `start` and `stop` must also be (hint: `"restart is composed of stop + start; both must be in controls"`)
  - **validator-only** (too rich for the model):
    - every `${param.X}` referenced in `container_template` must be declared in `params:` **and** that param must have a `pattern:` set. **Advisory only** — the runtime regex in `daemon.ResolveContainerName` is the authoritative defense (a user can still write `pattern: .*` to satisfy this check; the runtime gate catches it). The validator's job is to surface the foot-gun at plan time
- [x] wire the new checks into `Validator.Run` in `internal/validate/commands/commands.go`, using `loader.ParseCommandFile` so structural errors surface as categorised diagnostics with file + line info
- [x] **per-field suppression**: replace the existing whole-command `categorisedCmds[cf.FilePath+"/"+name] = true` blunt suppression with a per-field map: `categorisedFields[cf.FilePath+"/"+name+"#"+field] = true` for each daemon field this validator diagnoses (`service`, `container_template`, `on_already_running`, `stop_timeout`, `controls`). The fallback at `commands.go:124` then checks the model error's stable substring (e.g. `strings.Contains(err.Error(), "daemon: service")` ↔ field `service`) and only suppresses when the matching field marker is set. **Crucially**: a daemon block with `service:` missing AND `stop_timeout` bad → validator emits categorised diagnostics for BOTH fields, model errors for BOTH fields are dropped (correct); a daemon block with `service:` missing AND `controls` invalid where the validator only categorises `controls` (e.g. early-return bug) → model's `service` error still surfaces via fallback (no silent drop)
- [x] **alternative simpler mechanism if string-matching feels brittle**: change model errors to typed sentinels (e.g. `errors.New(...)` package-level vars: `ErrDaemonServiceRequired`, `ErrDaemonServiceNotLiteral`, `ErrDaemonContainerTemplateRequired`, `ErrDaemonStopTimeoutInvalid`, `ErrDaemonControlsInvalid`) joined via `errors.Join`; the fallback uses `errors.Is` to test each sentinel against the categorised field-marker set. Pick this if implementer prefers structured errors over substring matching — both achieve the same per-field precision (implementation chose the sentinel route — sentinels already exist in `internal/usercommands/model/types.go`)
- [x] keep hints concise and split with `\n` per the `feedback_validate_diagnostic_hints` memory
- [x] write tests with bad-block fixtures under `internal/validate/commands/testdata/daemon/`; assert exact diagnostics (severity, target, message, hint). Specifically:
  - all six replay cases above produce exactly one diagnostic each (not two — validator categorised + model fallback drops)
  - **multi-field test**: a daemon missing `service:` AND with `stop_timeout: "garbage"` → both diagnostics surface, neither double-emitted
  - **mixed-category test**: a daemon with valid `service:` but missing `container_template:` AND with an `${param.X}` referencing an undeclared param → both diagnostics surface (one categorised, one validator-only)
  - **regression for current finding**: daemon missing `service:` AND invalid `controls` → both surface (per-field suppression keeps `service` model error alive even when `controls` is categorised)
  - `service: app-${param.name}` → categorised "not literal" rejection
  - `controls: [start, restart]` → cross-control rejection
- [x] run `go test ./internal/validate/...` — must pass before task 7

### Task 7: `daemons_reap` builtin + auto-inject into lifecycle stop phase

**Current behavior gap** (verified): `LoadLifecycleConfig` returns `os.ErrNotExist` when the file is missing (`config/devbox.go:1298`), and `RunStop` translates that to a hard error `"no lifecycle.yml — see devbox/lifecycle.example.yml"` (`lifecycle/stop.go:30`). It also errors when `cfg.Stop == nil`. Auto-injection alone in `LoadLifecycleConfig` doesn't help projects that lack the file. Plan must change `RunStop`'s contract too.

Checklist:

- [x] add `internal/builtin/daemons_reap.go` — `daemons_reap` builtin: computes `projectFullName := ectx.Config.Project.FullName()`, builds `compose := docker.NewCompose(ectx.Config, ectx.DockerConfig)` (DockerConfig from `ectx`, threaded via `ExecContext`; never loaded inside the builtin — see invariant #7). Used only for `BinName()` + `BuildEnv()` (`docker ps`/`docker stop` are NOT compose subcommands so `BuildArgs` is not used). Enumerates running containers via raw `<compose.BinName()> ps --format=json --filter label=devbox.project=<full> --filter label=devbox.daemon.id` (NDJSON — one JSON object per line; parse with `bufio.Scanner` + `json.Unmarshal`, **not** `json.Unmarshal` over the whole buffer) with `cmd.Env = compose.BuildEnv()` so `DOCKER_HOST` / `DOCKER_CONTEXT` from `docker.yml` `process_env` apply. Calls raw `<compose.BinName()> stop -t <default-timeout>` on each via `exec.CommandContext` with the same env. Prints either `✓ reaped N daemon(s): <names>` or `no daemons running`. Accepts no `with:` parameters in v1
- [x] register `daemons_reap` in `Builtins` map
- [x] **lifecycle.yml missing-file behavior change**: introduce `lifecycle.EnsureStopConfig(cfg *config.LifecycleConfig) *config.LifecycleStopConfig` (new file `internal/lifecycle/autoreap.go`) that:
  - returns a synthetic `&LifecycleStopConfig{Phases: [reap-phase], FinalMessage: defaultStopMessage}` if `cfg == nil` or `cfg.Stop == nil`
  - otherwise returns a copy of `cfg.Stop` with the `_auto_reap_daemons` phase prepended
- [x] update `internal/lifecycle/stop.go:30` `RunStop`: on `errors.Is(err, os.ErrNotExist)`, do NOT error; instead set `lifecycleCfg = nil` and continue. Always pass the result through `EnsureStopConfig`. Remove the now-stale `cfg.Stop == nil` hard error
- [x] **docs change**: this is a user-visible behavior change — `devbox stop` no longer requires `lifecycle.yml`. Update `docs/reference/config/lifecycle.md` to document: lifecycle.yml is optional for `stop`; when absent, only `_auto_reap_daemons` runs followed by the default "Project is stopped" message. `run`/`restart` still require lifecycle.yml (they're not in scope here, no change)
- [x] keep the synthetic phase name `_auto_reap_daemons` (leading underscore) visible in plan output for transparency; do **not** make it opt-out (per planning decision)
- [x] write tests:
  - `EnsureStopConfig(nil)` → returns synthetic with reap-only phase
  - `EnsureStopConfig(&LifecycleConfig{Stop: nil})` → same
  - `EnsureStopConfig(&LifecycleConfig{Stop: &LifecycleStopConfig{Phases: [user]}})` → reap is first phase, user phases follow
  - `RunStop` integration with missing lifecycle.yml file (use `t.TempDir` without writing the file) → no error, reap runs
  - `daemons_reap` builtin parses multi-object NDJSON correctly via docker-stdout seam
- [x] run `go test ./internal/lifecycle/... ./internal/config/... ./internal/builtin/...` — must pass before task 8

### Task 8: `status daemons` section + `--no-daemons` flag
- [x] add `sectionDaemons` to the `Section` enum and `defaultSectionOrder` in `internal/command/status.go` (insert after `sectionTools`); add a `--no-daemons` flag honouring the existing default-only suppression pattern
- [x] add `internal/command/status_daemons.go` subcommand `devbox status daemons` calling `loadStatusContext(flags)` then `stack.RenderDaemons(...)`
- [x] add `internal/stack/daemons.go` with `CollectDaemons(ctx context.Context, cfg *config.DevboxConfig, dockerCfg *config.DockerConfig) ([]DaemonRow, []error)` — **`dockerCfg` is required, non-nil**: caller must pre-normalise `os.ErrNotExist` → `&config.DockerConfig{}` (mirror `build_context.go:63-70`). Needed for `BinName()` (e.g. `podman`) and `BuildEnv()` (`DOCKER_HOST`, `DOCKER_CONTEXT` `process_env`). Without it, `status daemons` silently misses remote-docker / podman / non-default-context setups that work elsewhere in devbox. Builds `compose := docker.NewCompose(cfg, dockerCfg)` purely for binary + env (the `docker ps` call itself is raw, not compose); shells out to `<bin> ps --format=json --filter label=devbox.project=<full> --filter label=devbox.daemon.id` via `exec.CommandContext` with `cmd.Env = compose.BuildEnv()` — separate argv elements, never shell-concatenated; **parses output as NDJSON** — one JSON object per line via `bufio.Scanner` + `json.Unmarshal` per line, NOT `json.Unmarshal` over the whole buffer assuming a JSON array; computes `time.Since(startedAt)` for uptime
- [x] `RenderDaemons(rows []DaemonRow) (string, []error)` returns the table. Single `docker ps` call returns all daemons — no per-service fan-out needed (unlike `CollectGitWorkspace`)
- [x] update `loadStatusContext` in `internal/command/status.go:79` to load `dockerCfg` once per status invocation with the same `os.ErrNotExist` → `&config.DockerConfig{}` normalisation as `build_context.go:63-70`; pass to `CollectDaemons` (and to any future collector needing docker policy) — avoids re-reading `docker.yml` per section and avoids nil panics on projects without docker.yml
- [x] add `DaemonRow` view-model to `internal/command/statusview/` (id, name from params label, container, uptime, started); per the section-renderer contract, `stack` returns string + per-row errors, command layer writes
- [x] **display safety**: container names and label payloads come from `docker ps` — if an external actor (or older devbox version) created a container with weird labels, control characters could disrupt the terminal. Strip non-printable / control characters (excluding standard whitespace) from `Container` and `Name` fields before passing to the renderer. Cheap defense in depth.
- [x] add `ui.RenderDaemonTable(rows []statusview.DaemonRow) string` to `internal/ui/` mirroring `RenderServiceTable`
- [x] hide the section entirely when no daemons are running (per spec section 8 — same treatment as empty `Topology`)
- [x] write tests for collector (parse known JSON fixture), renderer (snapshot the table), and the orchestrator (section ordering, `--no-daemons`)
- [x] run `go test ./internal/stack/... ./internal/command/... ./internal/ui/...` — must pass before task 9

### Task 9: `--set` value completion for daemon `.logs`/`.stop`
- [ ] in `internal/command/commands.go` (or wherever `cmd` group flags are registered), add a flag-completion path for `--set` on commands whose `DerivedFromDaemon != ""`: parse the current arg as `<key>=<partial>`, identify the daemon base ID, shell out via `completionConfigPath`-guarded path. After the project is located, load `dockerCfg` with **`os.ErrNotExist` → `&config.DockerConfig{}` normalisation** (no nil panic on projects without docker.yml), apply `compose.BinName()` + `compose.BuildEnv()` so completion uses the same docker binary and `process_env` (`DOCKER_HOST`, `DOCKER_CONTEXT`) as the rest of devbox — otherwise completion silently returns empty on remote-docker/podman setups
- [ ] argv: `<bin> ps --filter label=devbox.project=<full> --filter label=devbox.daemon.id=<base> --format=json` — **use `--format=json`**, NOT `--format={{ index .Labels "devbox.daemon.params" }}`. Reason: `docker ps`'s Go-template `.Labels` field has historically been a comma-separated string (`k1=v1,k2=v2`), not a `map[string]string`, so `{{ index .Labels "key" }}` returns nothing on real docker. `--format=json` reliably exposes a structured `Labels` field that can be parsed. **Match the format used by status (task 8) and reap (task 7)** for one consistent parsing path across the codebase
- [ ] **filter on BOTH `devbox.project` AND `devbox.daemon.id`** in the `--filter` args — filtering only on daemon.id would surface params from another project on the same machine that happens to declare the same daemon ID (e.g. `services.main.queue` is a common name across projects), leaking process names across project boundaries
- [ ] parse output as NDJSON (per invariant #8); extract the `Labels` field from each line (a `map[string]string`-shaped value in `--format=json` output, OR a comma-separated string in older docker — the parser tolerates both: try map shape first, fall back to splitting `k=v,k=v` if `Labels` is a string), pull out `devbox.daemon.params`, parse that as JSON, extract the values for the currently-completing key, dedupe, return as completions in `key=<value>` form (cobra repeated-flag completion shape)
- [ ] return empty + `ShellCompDirectiveNoFileComp` on any error (silent failure — matches existing completion patterns per CLAUDE.md "Completion helpers")
- [ ] write tests:
  - mocked `docker ps --format=json` stream (the parse step is a pure func over `io.Reader` for testability)
  - cross-project leak test: a container with matching `daemon.id` but mismatched `project` label is **not** offered as a completion
  - **format-tolerance test**: feed both the modern shape (`Labels` as object `{...}`) and the legacy shape (`Labels` as string `"k=v,k=v"`) — both produce identical completions. Without this, a future docker version change silently breaks completion
  - **end-to-end argv test**: assert the exact argv passed to docker is `[..., "ps", "--filter", "label=devbox.project=...", "--filter", "label=devbox.daemon.id=...", "--format=json"]` — protects the format-flag choice from regression to the broken template form
- [ ] run `go test ./internal/command/...` — must pass before task 10

### Task 10: Inspect — `Derived from: daemon <base>` line

**Current shape** (verified): `printInspect(w io.Writer, def *usercommands.CommandDef)` at `command_cmd.go:606` has no `cfg` parameter. There is no `Command:` line — the inspect block opens with `ui.RenderSectionTitle(def.ID)` (line 614), then a `type:` line (615). Two call sites pass only `(w, def)`:
- `command_cmd.go:162` — direct `-i/--inspect` route inside `runCommandByID(ctx, ..., cfg *config.DevboxConfig, ...)` (cfg already in scope)
- `command_cmd.go:387` — cmdbrowser path inside `makeBrowserSelector(cfg *config.DevboxConfig, ...)` (cfg already in scope)

Both call sites have `cfg` available; only the signature needs threading.

- [ ] change signature to `printInspect(w io.Writer, def *usercommands.CommandDef, cfg *config.DevboxConfig)` — `cfg` may be nil at call sites where the renderer is purely structural (tests); guard the resolved-name block on `cfg != nil && def.DerivedFromDaemon != ""`
- [ ] update both call sites:
  - `command_cmd.go:162` — `printInspect(stdout, def, cfg)`
  - `command_cmd.go:387` — `printInspect(&buf, d, cfg)`
- [ ] insert a new line **immediately after `type:` (line 615)** when `def.DerivedFromDaemon != ""`:
  - `derived from: daemon <base>` (use `def2("derived from", "daemon "+def.DerivedFromDaemon, 2)` to match existing definition-row rendering)
- [ ] for daemon-derived commands with non-nil `cfg` **and non-nil `def.SourceDaemon`**, add a `Container` subsection (use the existing `sub("Container")` helper at line 610) showing the resolved-with-defaults container name. Implementation:
  1. build a param map from `def.Params` taking each param's `Default` (or empty string if none)
  2. render `def.SourceDaemon.ContainerTemplate` via `tpl.RenderCommand` against a `RenderContext` with `Raw: cfg.Raw`, `Params: defaultsMap` — handles `${param.X}` substitution
  3. call `daemon.ResolveContainerName(cfg.Project.FullName(), rendered)` → `<full>`
  4. emit `def2("resolved (with default params)", <full>, 4)`
  5. on any error (template render, regex fail), silently skip the subsection — inspect should never error
- [ ] for daemon-derived commands, also surface `Argv`, `Service`, `User`, `Workdir`, container template literal, `On already running`, `Auto remove`, `Stop timeout` — per spec section 11. Read structural fields from `def.SourceDaemon` (container_template, on_already_running, auto_remove, stop_timeout) and execution fields from `def` itself (Argv, Service, User, Workdir — populated by registry expansion on the synthetic command from the base daemon's command-level fields). Emit under a new `Daemon` subsection
- [ ] write tests asserting inspect output for `<base>.start`:
  - contains `derived from: daemon <base>` immediately after the type line
  - contains the resolved sample container name with default params applied (use a `Project{Name: "my-proj"}` + `Params{name: {Default: "default"}}` fixture → expects `my-proj-php_queue_default`)
  - test with `cfg == nil` (defensive) → renders the type + derived-from line but omits the resolved-name subsection without panicking
- [ ] run `go test ./internal/command/...` — must pass before task 11

### Task 11: Reference documentation
- [ ] add a `## type: daemon` section to `docs/reference/config/commands.md` covering: YAML form (spec section 4), four virtual commands (spec section 3), container naming + labels (spec section 5), behaviour of each virtual command (spec section 6), validation rules (spec section 12), parallel restrictions (spec section 13), end-to-end flow (spec section 14)
- [ ] **add a "Security & privacy" subsection**: warn that `params` values land in the `devbox.daemon.params` label as JSON, which is visible via `docker inspect` to anyone with docker socket access on the host. **Secrets must go through `env:`** (the daemon runner passes env entries as `-e KEY` argv with values in the child process environment — values never appear in the host process argv where `ps` or `/proc/<pid>/cmdline` would expose them), **never through `params:`**. Also document that container naming uses `<project.full>-<rendered-template>` and that the post-render regex `^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$` is enforced — invalid characters in rendered values are a hard error at runtime even if the YAML `pattern:` permits them.
- [ ] cross-reference from `docs/reference/config/state.md` (no state file for daemons — `docker ps` is authoritative) and from `docs/reference/config/lifecycle.md` (auto-injected `_auto_reap_daemons` phase)
- [ ] regen `docs/reference/cli/` via `devbox docs generate --scope cli` (the new `--no-daemons` flag adds to status help)
- [ ] write no new tests — pure docs

### Task 12: Verify acceptance criteria
- [ ] verify every requirement from the Overview is implemented (spot-check each spec section 1–14)
- [ ] verify edge cases: daemon with no `params:` (single-instance daemon), daemon with multiple `params`, `controls:` subset, `on_already_running: noop`, custom `stop_timeout`, project with empty `prefix:` (no double-dash in container name)
- [ ] run `make test` — full suite must pass
- [ ] run `make lint` — all linters clean
- [ ] verify test coverage on new packages meets project standard (eyeball; this repo does not enforce a hard threshold)
- [ ] confirm `docs generate --scope all` succeeds against a fixture project that uses `type: daemon`

### Task 13: [Final] Update AGENTS.md
- [ ] add a one-paragraph entry under `internal/builtin/` enumerating the three new daemon builtins + `daemons_reap`
- [ ] add a one-paragraph entry under `internal/usercommands/` (or extend the existing model bullet) describing `type: daemon` and registry-time expansion to four virtual commands
- [ ] add a Key Pattern bullet if the inspect "Derived from" mechanism feels reusable (probably yes — future expansion of single-block → multi-command sugar)
- [ ] no checkbox for tests — docs only

## Technical Details

### Implementation invariants (security & correctness)

These are non-negotiable, enforced across all tasks. Each is restated in the relevant task; this section is the single index.

1. **Argv-only exec.** Every docker invocation (`docker compose run`, `docker logs`, `docker stop`, `docker ps`) uses `exec.CommandContext` with separate argv elements. No `sh -c`. No `fmt.Sprintf` of argv into a single string. No string concatenation of user-controlled values into a command line. Matches existing devbox patterns; called out here because daemons introduce more user-param touchpoints than service_run.
2. **Container name is regex-gated at render time.** `daemon.ResolveContainerName` validates against `^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$` *after* template rendering. This is the authoritative defense. The YAML `pattern:` requirement on referenced params (task 6) is advisory — a `.*` pattern still passes validation, and the runtime regex still fails closed.
3. **Labels use `encoding/json.Marshal`.** The `devbox.daemon.params` label value is produced via `json.Marshal(paramsMap)` — never string formatting. This protects status read-back from quotes, backslashes, and control characters in param values, and avoids label-injection where a malicious value could expand into extra JSON keys.
4. **Context cancellation is graceful for `.logs`.** `docker_daemon_logs` uses `cmd.Cancel = SIGINT` + `cmd.WaitDelay` so Ctrl-C detaches `docker logs -f` cleanly. The container itself is never signalled by `.logs`. (Default `exec.CommandContext` behaviour is SIGKILL, which is functionally fine but noisier.)
5. **TOCTOU between pre-check and run.** `docker_daemon_start`'s pre-check is best-effort; the authoritative anti-duplicate guarantee comes from docker's own name-uniqueness enforcement. The builtin must parse the docker error to apply `on_already_running: noop` correctly under race.
6. **`docker ps` is authoritative; no state file.** Status, completion, and reap all read live container state via `docker ps` with label filters. There is no on-disk daemon registry to drift, lock, or invalidate. Consequence: a `docker stop <container>` issued outside devbox is reflected immediately on next status read.
7. **Display safety for external data.** Container names / params read back from `docker ps` may have been created by older devbox versions or by an external actor. Strip control characters before terminal rendering (task 8).
8. **`docker ps --format=json` is NDJSON, not array.** Each line is an independent JSON object. Parse with `bufio.Scanner` + per-line `json.Unmarshal` — never `json.Unmarshal` over the full buffer assuming `[...]`. Applies to: status collector (task 8), completion shellout (task 9), `daemons_reap` enumeration (task 7).
9. **`stop_timeout` integer-seconds conversion.** YAML accepts `time.Duration` (e.g. `10s`, `500ms`, `1m30s`); `docker stop -t` accepts integer seconds only. Convert at the docker boundary via `secs := int(d.Round(time.Second).Seconds()); if secs < 1 { secs = 1 }`. Sub-second durations round up to 1s (never 0 — `docker stop -t 0` is `SIGKILL` immediately, which is **not** what `stop_timeout: 500ms` should mean). Validator (task 6) requires `d > 0`; runtime additionally enforces ≥ 1s after rounding. Used by: `docker_daemon_stop` builtin (task 3), `daemons_reap` builtin (task 7).
10. **Completion filters BOTH project + daemon.id labels.** A bare `--filter label=devbox.daemon.id=<base>` leaks running daemon names across projects that share an ID. Always filter both labels in tandem (task 9).
11. **Lifecycle.yml is optional for `stop`.** When the file is absent, `RunStop` synthesises a `_auto_reap_daemons`-only phase rather than erroring (task 7). `run`/`restart` lifecycle still require the file — they're not in scope here.
12. **Builtins do NOT import `internal/usercommands/runtime/`.** Runtime → builtin is the import direction; reversing it creates a cycle. Builtins read pre-rendered values from `with:` (templating already applied by `renderBuiltinWith`) and resolve config dot-paths via the exported `config.LookupDotPath` (extracted in task 3). Future shared docker argv builder lives in `internal/docker/`, not `runtime/`.
13. **Every docker invocation goes through `docker.Compose` policy** — never raw `exec.Command("docker", ...)`. Concretely:
    - `docker compose <subcmd>` (only `.start` uses `run`): build argv via `compose.BuildArgs("run", extraArgs...)` so `docker.yml` `project_name`, compose `-f` files, `Args.Global`, and `Args.Run` apply. Binary via `compose.BinName()`. Env via `compose.BuildEnv()` (merges `ProcessEnv`).
    - Raw `docker logs` / `docker stop` / `docker ps` (`.logs`, `.stop`, `daemons_reap`, completion, status collector): NOT compose subcommands — `BuildArgs` doesn't apply. But still use `compose.BinName()` + `compose.BuildEnv()` so the configured binary (e.g. `podman`) and `process_env` (`DOCKER_HOST`, `DOCKER_CONTEXT`) apply. Skipping this silently breaks remote-docker / podman / non-default-context setups.
    - **DockerConfig threading**: `*config.DockerConfig` is loaded once per top-level invocation with the existing missing-file normalisation pattern (`build_context.go:63-70`: `os.ErrNotExist` → `&config.DockerConfig{}`, never `nil`). It is then threaded into `builtin.ExecContext.DockerConfig` (new field) at every `ExecContext` construction site — the user-command runner (`runner_builtin.go:47` from `rc.DockerConfig`), the pipeline executor (its own load + normalise at executor setup), and any future ExecContext builder. **Builtins NEVER call `config.LoadDockerConfig` themselves** — loader cost + missing-file panic risk + races on file changes mid-run.
    - **`-e KEY` argv, not `-e KEY=VAL`**: env keys go into argv as bare keys; values are placed in `cmd.Env` via `docker.MergeEnv(ProcessEnv ∪ envVars)`. Matches `runner_service.go:320-322`. Reason: secret values in argv leak to `ps` / `/proc/<pid>/cmdline`; the docs tell users to put secrets in `env:`, and the runtime must honour that promise.
    - **Argv ordering for `compose run`**: `compose_args` from `with:` is appended BEFORE `--user`/`--workdir`/`-e`/`--label`/service/argv so command-owned fields override on conflict. Matches `runner_service.go:301-325`.
    - **`--no-deps --entrypoint ""` are non-optional** for daemon `.start` — same as `service_run` (`runner_service.go:298`). Without `--no-deps`, `docker compose run` re-resolves the service's `depends_on` chain (already running via `devbox up`), introducing duplicate-startup races. Without `--entrypoint ""`, the image's ENTRYPOINT prefixes the user's explicit `argv:`, silently changing what runs. Both flags are added unconditionally between `-d` and `--rm`.
14. **Registry expansion is config-blind.** `LoadRegistry(baseDir)` has no `*config.DevboxConfig`. Anything config-derived (project full name, dot-path resolutions, compose files, docker binary) is resolved at builtin runtime via `ectx.Config`, not packed into `with:` at expansion time. Anything param-derived is expressed as `${param.<name>}` template literals so `renderBuiltinWith` resolves it at command dispatch.
15. **Label JSON uses resolved runtime values, not declarations.** Registry expansion writes `label_params: {name: "${param.name}"}` (template literals keyed by param name); `renderBuiltinWith` resolves to `{name: "default"}` at runtime; the builtin `json.Marshal`s the resolved map. Packing raw `ParamDef` structs into `with:` would serialise defaults / patterns / descriptions into the label — wrong shape, lossy on read-back.

### YAML form (canonical example)

```yaml
commands:
  queue:
    type: daemon
    description: "Laravel queue worker"
    service: app-main
    workdir_from: services.main.work_dir_internal
    user: www-data
    env:
      QUEUE_CONNECTION: redis
    params:
      name:
        default: default
        pattern: ^[a-zA-Z0-9_-]+$
    argv:
      - php
      - artisan
      - queue:listen
      - --timeout=0
      - -v
      - --queue=${param.name}
    daemon:
      container_template: "php_queue_${param.name}"
      on_already_running: error        # error | noop
      auto_remove: true                # default true → --rm
      stop_timeout: 10s
      controls: [start, logs, stop, restart]
```

### Container name

```
<project.full>-<container_template-rendered>
```

`project.full` is `ProjectConfig.FullName()` (`<prefix>-<name>` or `<name>`). Post-render regex: `^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`.

### Standard labels

- `devbox.project=<project.full>`
- `devbox.daemon.id=<base>` (e.g. `services.main.queue`)
- `devbox.daemon.params=<json>` (e.g. `{"name":"emails"}`)

### Expansion table (controls → virtual commands)

| `controls` | Generated IDs |
|---|---|
| `[start, logs, stop, restart]` (default) | all four |
| `[start, stop]` | `.start`, `.stop` |
| `[start, logs, stop]` | `.start`, `.logs`, `.stop` (no `.restart` — also fine; restart needs stop + start) |

If `restart` is requested but `start` or `stop` are not, validation fails (restart depends on both).

### Locked decisions (from spec section 15)

| Decision | Choice |
|---|---|
| `restart` as own command | yes, virtual workflow of stop + start |
| Bulk `stop --all` | out of scope v1 |
| Multi-tail `logs --all` | out of scope v1 (tmux handles it) |
| `on_already_running` values | only `error` / `noop` |
| Project-name prefix | automatic, not configurable |
| Container labels | standardised, not configurable |
| Auto-reap | builtin + auto-injected `_auto_reap_daemons` phase in stop lifecycle |
| State file for tracking | none — `docker ps` is authoritative |
| `service:` parameterisation | literal-only in v1 (label stability) |

### Explicit out of scope (spec section 16)

- docker `--restart unless-stopped` — daemons are dev-session bound.
- Replica pooling (one decl → N containers).
- Capture mode for `.logs` (only `-f` follow makes sense in dev).
- Healthchecks over daemons (containers use `--rm` so absence from `docker ps` is the signal).
- `service: app-${param.X}` for daemons (label stability) — runtime supports it elsewhere but not here.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification**:
- Run end-to-end flow from spec section 14 against a real Laravel fixture project: `.start` → `.logs` → `.restart` → `.stop` cycle, then `devbox stop` reaping survivors.
- Verify `docker ps` labels are present and correctly populated.
- Verify `status daemons` section formatting under realistic uptime values (seconds / minutes / hours).
- Verify Ctrl-C on `.logs` detaches cleanly without killing the container.

**External system updates**:
- Update any example template packs that ship a default `lifecycle.yml` to remove the now-redundant explicit `daemons_reap` step (auto-injected) — none currently exist; this is a future task only if templates are added.
- Notify devbox users (release notes) of the new declarative type when shipping.

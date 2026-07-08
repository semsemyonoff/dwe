# Integration tests — stage 3a: per-step timeouts, `workspace/tests/` validator, compose isolation scanner

## Overview

Fourth plan in the integration-tests feature
([spec](specs/2026-07-06-integration-tests.md) — the single source of truth for design
decisions; do not re-open settled decisions). Stages 1a/1b (MVP) and 2 (reports & clean)
are complete and landed
([1a](completed/20260706-integration-tests-1a-engine-and-scenario-schema.md),
[1b](completed/20260706-integration-tests-1b-runner-isolation-cli.md),
[stage 2](completed/20260708-integration-tests-stage2-reports-clean.md)).

Spec §7 stage 3 ("Polish") bundles **four** features. Per the design brainstorm, stage 3
is split into two plans:

- **3a (this plan)** — the static / engine additions: (1) per-step timeouts, (2) the
  `workspace/tests/` validator domain in `dwe validate`, (3) the compose isolation scanner
  (shared function + two callers).
- **3b (later)** — parallel scenario execution + aggregated live reporter (the runtime/UI
  change). Written via `/planning:make` after 3a lands.

3a and 3b are independent (no hard ordering dependency between them).

### The three features

1. **Per-step timeouts** — a **general** engine field `timeout:` on a pipeline step,
   honored in every pipeline (deploy / reset / lifecycle / tests), consistent with
   stage-1's "engine changes are general" philosophy. **Purely opt-in — no implicit
   default anywhere.** Absent (or `timeout: 0`) → the step is unbounded exactly as today;
   any positive duration bounds only that one step. Existing long-running deploys must not
   break.
2. **`workspace/tests/` validator domain** — a new `dwe validate tests` domain that
   statically validates scenario files (name normalisation, timeout parse, service
   references, step schema, builtin `with:` params, `when:` conditions, `type: command`
   references) and surfaces compose-isolation findings as warnings. Validate-only (never
   preflight).
3. **Compose isolation scanner** — one shared function that parses the project's raw
   compose files for constructs that bypass Docker-Compose project-name scoping
   (`container_name:`, literal host ports not modelled in dwe `ports`, `external:` /
   explicitly-named volumes & networks). Two callers: the test runner's prepare phase
   (tiered fail/warn, `--skip-isolation-check` downgrades) and `dwe validate tests`
   (all warnings).

## Context (from discovery)

Verified against the current tree this session; line numbers approximate.

### Per-step timeout

- **`config.DeployStep`** — `internal/core/project/config/workspace.go:416`; strict
  known-fields allow-list `deployStepKnownFields` at `workspace.go:465`. **No `timeout`
  field** in the struct or the allow-list — a `timeout:` key would be rejected today.
- **`ResolvedStep`** — `internal/core/execution/pipeline/step.go:24` (fields: `Phase`,
  `Step`, `Service`, `RuntimeWhen`, `PhaseWhen`, `FilesGate`, `Parallel`). No `Timeout`.
  `ResolvedParallel` at `step.go:37`.
- **Resolve** — `ResolvePhaseSteps` (`pipeline/resolve.go:64`) → `resolveLeafStep`
  (`resolve.go:118`, constructs `ResolvedStep` at ~158-165) and `resolveParallelStep`
  (`resolve.go:170`, constructs at ~238-249). This is where a new resolved field is
  populated. Resolve errors already surface through `deploy.ResolvePlan` /
  `reset.ResolvePlan`, which the config validators `deployValidator` / `resetValidator`
  call (`config/workspace.go:1152-1165`) — so a bad `timeout:` becomes a validate
  diagnostic for free.
- **Execution** — single pipeline-wide `ctx` threaded unchanged through `RunWithOptions`
  (`executor.go:445`) → `executeStepBody` (`executor.go:730`) → `ExecAction`
  (`executor.go:867`, wrapped in a timing pair at 866-868). **No per-step ctx / timeout
  today.** `shell` (`execShellAction`, `executor.go:214`) and `dwe` (`execDweAction`,
  `executor.go:234`) build subprocesses via `exec.CommandContext` + `bindCancelTerm`
  (`executor.go:335-343`: SIGTERM on ctx-cancel, `WaitDelay = 5s` before force-kill), so a
  per-step `context.WithTimeout` will correctly terminate their subprocesses. `builtin`
  (`execBuiltinAction`, `executor.go:249`) and `command` (`execCommandAction`,
  `executor.go:273`) receive `ctx` and depend on those layers honoring it.
- **Parallel substeps** — `executeParallelGroup` (`executor.go:946`) uses
  `errgroup.WithContext` (`executor.go:982`); each goroutine runs `runParallelSubStep`
  (`executor.go:1038`) → `executeStepBody` (`executor.go:1068`). Each substep is its own
  `DeployStep`/`ResolvedStep`, so per-substep `Timeout` flows through with no extra code
  once `executeStepBody` honors it.

### Validate framework

- **`validate.Validator`** interface — `internal/core/validate/validate.go:58`
  (`ID()`, `Domain()`, `Run(ctx) []Diagnostic`). Optional `DomainLevelValidator` /
  `GlobalValidator` / `GroupValidator` (validate.go:64-96). `Context` (validate.go:29-55)
  carries `ProjectRoot`, `ConfigPath`, `Cfg *config.DweConfig`, `CommandRegistry any`
  (nil-tolerant `*usercommands.Registry`), `ValidateCfg`, `Stage`.
- **`All()` pattern** — each domain package exposes a constructor (`config.All()`,
  `env.All(cfg)`, `commands.All()`, …). Registered in `buildRegistry`
  (`internal/cli/validate/validate.go:673-742`); `dwe validate` scope threaded from
  subcommands (validate.go:197 `NewCmd`, run entry `runValidate` at validate.go:421,
  `registry.Run(ctx, scope...)` at validate.go:508).
- **Preflight participation is opt-in by the assembler, not a flag** —
  `preflight.Run` (`internal/core/execution/preflight/preflight.go:103`) registers only
  `env.All(cfg)`, the single `config.validate` validator, and `checks.AllForStages(...)`.
  A new domain that is *not* registered there is validate-only automatically. **`tests`
  must NOT be added to preflight.**
- **Command-reference precedent** — `checks/loader.go:125-137`: `cmdRegistry.Get(entry.Cmd)`
  → "unknown command" on error, plus an `allowedCommandTypes` gate. Config validators pull
  the registry via `registryFrom(ctx)` (`config/shared.go:13`).
- **Step-schema precedent** — `pipeline.ResolvePhaseSteps` validates step schema + builtin
  `with:` (`builtin.Validate`, `builtin/builtin.go:200`) + `when:` in one pass;
  `deployValidator.Run` surfaces `deploy.ResolvePlan` errors as one diagnostic
  (`config/workspace.go:1152-1165`). Closest structural walker: `config/generated.go`
  (`scanPhasesForGeneratedMissing`, walks phase/step/sub-step `when:`).
- **Severity / exit** — `diag.Severity` (`validate/diag/diag.go:8`,
  `SeverityUnknown/OK/Info/Warning/Error`); `diag` is import-cycle-free **by design so
  `project/config` may emit diagnostics**. `Aggregate` / `ExitCode` (validate.go:198-228):
  errors → exit 1, `strict && warnings` → 1.

### Scenario schema / runner

- **`Scenario`** — `internal/core/workflow/envtest/scenario.go:39` (`Description`,
  `Env ScenarioEnv`, `Timeout string`, `Steps []config.DeployStep`). `LoadScenario`
  (`scenario.go:73`): strict `KnownFields(true)`, rejects empty files, then
  `config.ValidateDeploySteps` (`scenario.go:90`). Loader-side `${...}` rendering of step
  `with:`/`cmd:` is `RenderSteps(steps, cfg)` (used at `runner.go:575`).
- **Runner** — `RunScenario` (`runner.go:241`): per-scenario flock (`runner.go:258`),
  writes copy `local.yml` / docker identity, then subprocess `dwe validate`
  (`runner.go:403`) and `dwe deploy run --silent` (`runner.go:416`), then in-process
  `runSteps` (`runner.go:551`), all funnelling through `finish()` (`runner.go:360`, applies
  deadline→`StatusFailed`, `collectReport` before teardown, `teardown()`). `RunRequest`
  carries `Scenario`, `BaseDir`, `Keep`, timeout, `ReporterFactory`, translator/locale.

### Compose parsing

- **No general compose→struct parser exists** (no `compose-go`/`compose-spec` dep). Closest
  precedent: `internal/core/validate/config/compose_name.go:98-110` — `os.ReadFile` +
  `yaml.Unmarshal` into a narrow `struct{ Name string }` per file, iterating
  `ctx.Cfg.ComposeFiles()`. The scanner extends this pattern with a wider narrow struct.
- **Compose file enumeration** — `(*DweConfig).ComposeFiles()` → `composeFiles(all)`
  (`workspace.go:649`), the ordered `-f` chain (base + overlays + bridge + local).
- **dwe-modelled ports** — `ServiceConfig.Ports map[string]ServicePortSpec`
  (`workspace.go:1123`); `ServicePortSpec{Port int; Scheme string}` (`workspace.go:970`).
  `env/ports.go:336` `collectDeclaredPorts` walks `cfg.Services[].Ports` for the host-port
  set (the "modelled in dwe ports" side); raw-compose host ports are invisible to it today.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task) — repo style:
  table-driven tests, package-local `testdata/` fixtures, injectable seams (no real Docker,
  no real `dwe` subprocess in any test).
- Complete each task fully before the next; small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** — success and error scenarios as
  separate checklist items.
- **CRITICAL: all tests pass before starting the next task.** Focused package tests per task
  (`make embedded-docs` once, then `go test ./internal/...` per package); `make test` +
  `make lint` at the end.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Backward compatibility: an absent `timeout:` is byte-identical to today (no existing
  golden changes); the validator domain and isolation scanner are purely additive.

## Testing Strategy

- **Unit tests** per task. No UI e2e in this repo.
- **No test touches the real Docker daemon or spawns a real `dwe`** — every docker /
  subprocess interaction goes through an injectable seam (the established
  `execDwe` / `newTeardownDeps` / package-command-seam pattern).
- Timeout: a fast fake step (a `builtin`/`shell` that sleeps a few ms) proves a short
  timeout fails with the timeout message and a long/absent timeout runs unchanged;
  parallel-substep coverage.
- Scanner: table-driven against `testdata/` compose fixtures (literal vs variable host
  port, container_name, external/named vol/net, clean project).
- Validator: `testdata/` project trees with `workspace/tests/*.yml` fixtures for each
  diagnostic path.
- Runner: recording seams assert the isolation gate blocks (no deploy subprocess spawned)
  and `--skip-isolation-check` downgrades.
- `make test` at the end is the regression net (existing goldens stay byte-identical).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep the plan in sync with the actual work.

## Solution Overview

Three mostly-independent seams, sequenced so the isolation scanner (Task 3) lands before
its two consumers (validator Task 4, runner Task 5):

1. **Per-step timeout** (Tasks 1-2) — a general `config.DeployStep.Timeout` string,
   resolved to `ResolvedStep.Timeout time.Duration`, enforced by a `context.WithTimeout`
   wrap around the step body in `executeStepBody`. Opt-in; zero/absent is a no-op.
2. **Isolation scanner** (Task 3) — `config.ScanComposeIsolation(cfg, projectRoot)
   []IsolationFinding` in `internal/core/project/config/`, parsing `cfg.ComposeFiles()`
   with a narrow yaml struct. Each finding carries an intrinsic `Blocking bool`; **severity
   policy lives in the callers**, keeping the scanner leaf-clean (no `diag` / `validate`
   import).
3. **`workspace/tests/` validator** (Task 4) — `internal/core/validate/tests/` with
   `All()`, registered in `buildRegistry` + a `dwe validate tests` scope; reuses
   `LoadScenario` + `ResolvePhaseSteps` + `registry.Get`, and maps every isolation finding
   to `SeverityWarning`.
4. **Runner isolation gate** (Task 5) — the scanner called in the runner's prepare phase;
   blocking findings fail the scenario (teardown still runs) unless
   `--skip-isolation-check` downgrades all to warnings.

## Technical Details

### Per-step timeout

- **Schema**: `Timeout string \`yaml:"timeout,omitempty"\`` on `config.DeployStep`; add
  `"timeout"` to `deployStepKnownFields`. Raw string so `time.ParseDuration` runs at resolve
  time (mirrors `Scenario.Timeout`).
- **Resolve**: add `Timeout time.Duration` to `ResolvedStep`. In `resolveLeafStep` /
  `resolveParallelStep`, when `step.Timeout != ""` call `time.ParseDuration`; **`< 0` → a
  resolve error** (meaningless), `0` → unbounded (leave zero), `> 0` → store. Wrap parse
  errors with the step ref so the diagnostic names the step. Populate on both leaf steps and
  parallel substeps (substeps are ordinary `DeployStep`s).
- **Enforce**: in `executeStepBody`, when `rs.Timeout > 0`, derive
  `stepCtx, cancel := context.WithTimeout(ctx, rs.Timeout)` (defer cancel) and pass
  `stepCtx` to the body `ExecAction`. **Discriminate a step timeout from an outer
  cancellation**: after `ExecAction` returns non-nil, if `ctx.Err() == nil &&
  errors.Is(stepCtx.Err(), context.DeadlineExceeded)` → wrap as
  `step %q timed out after %s`; otherwise propagate unchanged (a Ctrl+C outer cancel must
  not be mislabelled a timeout). Bound the **step body only** — leave the `check:` action
  (`executor.go:912`) on the shared `ctx` (it is a fast skip-predicate, not the work).
- **Contract scope (honest wording — document precisely)**: the timeout enforces via
  **context cancellation**. Bodies that honor `ctx` are bounded — `shell`/`dwe` (subprocess
  SIGTERM via `bindCancelTerm`, then force-kill after `WaitDelay`), ctx-aware builtins
  (`http_check`, `docker_wait_healthy`, `tcp_reachable`, …), and `command` steps whose work
  honors `ctx`. A body that **ignores `ctx` and blocks on interactive input** — e.g. a
  `confirm` builtin / a `type: command` workflow with a `confirm:` step waiting on the
  TTY/stdin — is **NOT force-interrupted** by its timeout (the deadline fires but the
  goroutine stays blocked until input arrives). This is an accepted limitation, **not** a
  blocker: in test scenarios confirmations are auto-yes'd (`SkipConfirm`, `DWE_NONINTERACTIVE=1`)
  so it does not arise there; and a timeout on a human prompt is meaningless anyway. Making
  every interactive prompt ctx-aware is a separate, larger change — **out of scope for 3a**.
  Do NOT advertise "kills any step" — advertise "bounds ctx-honoring step bodies".
- **No group-level timeout** — only per-leaf. A parallel group's substeps each honor their
  own `Timeout` via the existing `executeStepBody` path.

### Isolation scanner (`internal/core/project/config/compose_scan.go`)

- **Types** (leaf, no `diag`/`validate` import):
  ```go
  type IsolationKind string
  const (
      KindContainerName   IsolationKind = "container_name"
      KindRawHostPort     IsolationKind = "raw_host_port"
      KindNamedVolume     IsolationKind = "named_volume"
      KindNamedNetwork    IsolationKind = "named_network"
      KindExternalVolume  IsolationKind = "external_volume"
      KindExternalNetwork IsolationKind = "external_network"
  )
  type IsolationFinding struct {
      Kind     IsolationKind
      Resource string // service name or volume/network name
      HostPort int    // raw_host_port only, else 0
      Blocking bool   // intrinsic: causes a hard collision with the working env
      Message  string
      File     string // compose file the finding came from
  }
  func ScanComposeIsolation(cfg *DweConfig, projectRoot string) []IsolationFinding
  ```
- **`Blocking` (intrinsic, policy-light)**: `true` for `KindContainerName` and
  `KindRawHostPort` (real collisions with the working environment); `false` for the
  named/external kinds (shared-resource hazards). Callers map to their own severity:
  the runner treats blocking findings as fatal (unless `--skip-isolation-check`), the
  validator emits everything as `SeverityWarning`.
- **Parse**: iterate `cfg.ComposeFiles()`, join relatives against `projectRoot`, `os.ReadFile`
  + `yaml.Unmarshal` into a narrow struct:
  ```yaml
  services: { <name>: { container_name: <str>, ports: [ <short|long> ] } }
  volumes:  { <name>: { external: <bool|obj>, name: <str> } }
  networks: { <name>: { external: <bool|obj>, name: <str> } }
  ```
  An unreadable / malformed compose file is **skipped silently** (the copy's `dwe validate`
  subprocess / `docker compose` surface real parse errors; the scanner is advisory).
- **Port rule** — flag a **literal host port (single OR range)**. Handle short syntax
  (`"8080:80"`, `"127.0.0.1:8080:80"`, host ranges `"8080-8090:80-90"`, optional
  `/tcp`|`/udp` protocol suffix which is stripped first) and long syntax
  (`{ target: 80, published: 8080 }`, `published` int or string). Extract the host
  published token; it is a `KindRawHostPort` finding (`Blocking: true`) **iff it matches
  `^\d+(-\d+)?$`** — i.e. a single literal or a literal range. For a range set
  `HostPort` = the low end and name the full range in `Message`. Ignore
  `${...}`-interpolated / env-var tokens and container-port-only entries (random host
  port). **IPv6 bracketed hosts (`[::1]:8080:80`) are out of scope** — documented + a test
  asserting they are currently NOT flagged (rare in dev compose; revisit if needed).
  No cross-check against dwe-modelled ports is needed — a literal host port in raw compose
  bypasses `auto`/`ports_free` regardless of whether the same number also appears in
  `services.<name>.ports`.
- **container_name rule** — flag **every** `container_name:` occurrence (mapping a compose
  service to a dwe service key is unreliable; false positives are cleared by
  `--skip-isolation-check`).
- **volume/network rules** — a top-level entry with `external: true` (bool or
  `{external: {...}}` truthy) → `KindExternal*`; an entry with an explicit `name:` →
  `KindNamed*`. Both `Blocking: false`.

### `workspace/tests/` validator (`internal/core/validate/tests/`)

- `All() []validate.Validator` → `[]validate.Validator{scenariosValidator{}}`;
  `Domain() == "tests"`, `ID() == "scenarios"`.
- `Run(ctx)` — resolve the tests dir from `ctx.ProjectRoot` / `ctx.ConfigPath`
  (`workspace/tests/`); `os.ReadDir` → per `*.yml` file, in filename order:
  - **load** via `envtest.LoadScenario(path)` — on error, one `SeverityError` diagnostic
    (file + message), continue to the next file. This ALSO covers **name normalisation**:
    `LoadScenario` already runs `ValidateScenarioName` (`scenario.go:75`), so a bad filename
    stem surfaces here — no separate name check is needed (the "bad name" test exercises this
    path). *(The `validate/tests` → `envtest` import is acyclic and follows existing
    precedent — `validate/snapshot`→`workflow/snapshot`, `validate/config`→`workflow/deploy`
    already establish validate→workflow; `envtest` imports `validate` nowhere.)*
  - **timeout** — `time.ParseDuration(scn.Timeout)` when non-empty; parse failure →
    `SeverityError`.
  - **services** — every `env.services.enable` / `disable` entry must exist in
    `ctx.Cfg.Services`; unknown → `SeverityError`.
  - **steps** — resolve the scenario's steps **as one whole phase, exactly like runtime**
    (codex review): render all steps' `with:`/`cmd:`, wrap them in a single
    `config.DeployPhase{Name: "tests", Steps: rendered}`, and call `pipeline.ResolvePhaseSteps`
    **once**. Runtime (`runSteps`) does exactly this, and `ResolvePhaseSteps` enforces
    **whole-phase invariants** — notably `checkUniqueStepNames` (`resolve.go:108-111`) — that a
    per-step pass would miss, so a scenario with duplicate top-level step names would pass
    `dwe validate tests` yet fail `dwe test run`. Surface any resolve error as a
    `SeverityError` (covers step schema, builtin `with:` via `builtin.Validate`, `when:`,
    duplicate names, parallel structure). The error string already carries the step
    address, so no per-step wrapping is needed for attribution.
    - **Render context (avoids the `auto`-port false positive at the source — no downgrade
      heuristic)**: build the context as `ctx.Cfg.Raw` overlaid with the scenario's own
      `env.vars`, **substituting any `env.vars` entry whose value is the literal `auto` with a
      valid placeholder host port** (a fixed in-range sentinel, e.g. `1`). `auto` is the only
      magic value (spec §4, ports only), so this is principled: `${vars.db.port}` with
      `db.port: auto` renders to a valid int and passes `tcp_reachable`/`http_check`
      validation, while a **concrete** bad param (`status: nope` on a step with a templated
      URL) still fails as a `SeverityError`. This is strictly better than the previous
      blanket "`${...}` in raw → downgrade to warning" heuristic, which codex showed would
      **mask** a concrete static error (`http_check` validates `status`/`retries` independently
      of `url`).
    - **Residual limitation (document)**: a var defined only **post-deploy** (e.g. a
      `${generated.*}` secret, or a var the deploy creates) is absent from the pre-deploy
      `ctx.Cfg` and renders empty at validate time — this can yield a spurious value
      diagnostic. Documented workaround: give such a var a project-level default. The
      validator sees pre-deploy config; runtime sees post-deploy.
  - **command refs** — for each `type: command` step, resolve the registry from
    `ctx.CommandRegistry` (re-implement the 3-line assertion
    `reg, ok := ctx.CommandRegistry.(*registry.Registry)` — `registryFrom` is **unexported**
    in `valconfig`, not shared; skip the check when the registry is nil/absent);
    `reg.Get(cmd)` unknown → `SeverityError` (precedent `checks/loader.go:125`).
  - **isolation** — when `ctx.Cfg != nil`, call
    `config.ScanComposeIsolation(ctx.Cfg, ctx.ProjectRoot)` **once** (outside the per-file
    loop) and emit each finding as `SeverityWarning`. Emitted once under the domain when the
    tests dir is present.
  - **nil-config guard** — every step above that dereferences `ctx.Cfg` (the `env.services`
    membership check, step resolution, the isolation scan) is guarded by `ctx.Cfg != nil`
    (the `errPartialLoad` path in `buildRegistry` passes a nil cfg; existing validators guard
    it, e.g. `compose_name.go:34`).
- **Wire-in**: register `tests.All()` in `buildRegistry`; add a `dwe validate tests`
  subcommand under `NewCmd` with scope `["tests"]` (mirror an existing leaf, e.g.
  `dwe validate config`). Not added to `preflight.Run`.

### Runner isolation gate (`internal/core/workflow/envtest/`)

- Add `SkipIsolationCheck bool` to `RunRequest`.
- **Insertion window is `runner.go:396-403`** — after `finish` (defined ~`360`), the
  reporter, and `logWriter` are set up (~`396`), and **before** the `dwe validate`
  subprocess (~`403`). The "after copy/identity" phrasing is necessary-but-not-sufficient:
  the gate must be able to call `finish()` and write to the scenario diagnostic writer, so it
  goes in this window. Best-effort load the copy config
  (`config.LoadConfigOrWrap(copyRoot/workspace.yml)`); on success run
  `config.ScanComposeIsolation(copyCfg, copyRoot)`. (If the copy config won't load, **skip
  the scan** — the subsequent `dwe validate` subprocess surfaces the real config error.)
  - Print **all** findings as warnings to the scenario's diagnostic writer.
  - If any finding is `Blocking` **and** `!req.SkipIsolationCheck` → fail the scenario:
    route through `finish()` with an error status so **teardown still runs** (removes the
    copy dir + manifest + flock), no deploy subprocess is spawned; the error message lists
    the blocking findings and hints `--skip-isolation-check`.
  - If `req.SkipIsolationCheck` → every finding is a warning only; proceed.
  - `collectReport` inside `finish()` fires for the non-passed status but is best-effort and
    harmless here (no `test.log`, no containers → near-empty report). **Optional**: guard it
    to skip when the failure is pre-deploy (no pipeline log yet) to avoid an empty report
    dir — not required for correctness.
- **CLI** (`internal/cli/test/run.go`): add a `--skip-isolation-check` bool flag, plumb into
  `RunRequest.SkipIsolationCheck`.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): manual smoke on a real project, Vikunja update,
  stage-3b plan.

## Implementation Steps

### Task 1: Per-step timeout — schema field + resolve into `ResolvedStep`

**Files:**
- Modify: `internal/core/project/config/workspace.go` (`DeployStep` + `deployStepKnownFields`)
- Modify: `internal/core/execution/pipeline/step.go` (`ResolvedStep.Timeout`)
- Modify: `internal/core/execution/pipeline/resolve.go` (parse + populate)
- Modify: `internal/core/project/config/workspace_test.go` (or the DeployStep decode test)
- Modify: `internal/core/execution/pipeline/resolve_test.go`

- [x] add `Timeout string \`yaml:"timeout,omitempty"\`` to `config.DeployStep`; add
      `"timeout"` to `deployStepKnownFields`
- [x] add `Timeout time.Duration` to `pipeline.ResolvedStep`
- [x] in `resolveLeafStep` and `resolveParallelStep`, parse `step.Timeout` via
      `time.ParseDuration` when non-empty (`< 0` → wrapped resolve error naming the step;
      `0` → unbounded; `> 0` → store); populate on leaf steps and parallel substeps
- [x] write config tests: strict decode accepts `timeout:` and rejects an unknown sibling
      key still; a step with `timeout: 90s` round-trips
- [x] write resolve tests: `"90s"` → `90s`; `"0"` → `0`; absent → `0`; `"abc"` → error;
      `"-1s"` → error; a parallel substep carries its own resolved `Timeout`
- [x] run `go test ./internal/core/project/config/... ./internal/core/execution/pipeline/...`
      — must pass before task 2

### Task 2: Per-step timeout — enforce in the executor

**Files:**
- Modify: `internal/core/execution/pipeline/executor.go` (`executeStepBody`)
- Modify: `internal/core/execution/pipeline/executor_test.go`

- [ ] in `executeStepBody`, when `rs.Timeout > 0`, wrap the body `ExecAction` call in
      `context.WithTimeout(ctx, rs.Timeout)` (defer cancel); leave the `check:` action on
      the shared `ctx`
- [ ] discriminate timeout from outer cancellation: on a non-nil body error with
      `ctx.Err() == nil && errors.Is(stepCtx.Err(), context.DeadlineExceeded)`, return
      `step %q timed out after %s`; else propagate unchanged
- [ ] confirm parallel substeps honor their own `Timeout` (no extra code — flows through
      `runParallelSubStep` → `executeStepBody`)
- [ ] document the contract-scope limitation in a code comment: the timeout bounds
      ctx-honoring bodies; an interactive/ctx-ignoring body (e.g. `confirm` on stdin) is not
      force-interrupted (out of scope for 3a)
- [ ] write tests: a `shell` step that sleeps past a short `timeout` → fails with the timeout
      message and the subprocess is terminated; **a ctx-aware `builtin` (not just shell/sleep)
      that respects `ctx` → also times out with the message** (proves the builtin path, per
      codex); `timeout: 0`/absent → runs to completion unchanged; a parallel group where one
      substep times out → only that substep fails, its sibling completes; an outer-ctx cancel
      is NOT reported as a timeout
- [ ] run `go test ./internal/core/execution/pipeline/...` — must pass before task 3

### Task 3: Compose isolation scanner (`config.ScanComposeIsolation`)

**Files:**
- Create: `internal/core/project/config/compose_scan.go`
- Create: `internal/core/project/config/compose_scan_test.go`
- Create: `internal/core/project/config/testdata/compose_scan/*.yml` (fixtures)

- [ ] define `IsolationKind` constants, `IsolationFinding{Kind, Resource, HostPort,
      Blocking, Message, File}`, and
      `ScanComposeIsolation(cfg *DweConfig, projectRoot string) []IsolationFinding`
- [ ] implement the narrow compose parser (`os.ReadFile` + `yaml.Unmarshal` per
      `cfg.ComposeFiles()`; unreadable/malformed file → skipped silently) capturing
      `services.<name>.{container_name, ports}` and top-level `volumes`/`networks`
      `{external, name}`
- [ ] implement the port helper: strip a `/tcp`|`/udp` suffix, extract the host published
      token from short + long compose port syntax; emit `KindRawHostPort` (`Blocking: true`)
      for a token matching `^\d+(-\d+)?$` (single literal OR literal range — range stores the
      low end in `HostPort`, full range in `Message`); ignore `${...}`/env-interpolated and
      container-port-only entries; IPv6 bracketed hosts (`[::1]:8080:80`) explicitly out of
      scope (NOT flagged)
- [ ] implement `container_name` (all occurrences, `Blocking: true`),
      `external:`/named volume+network rules (`Blocking: false`)
- [ ] write tests against `testdata/` fixtures: literal host port → finding; **host range
      `"8080-8090:80-90"` → finding (low end 8080, range in message)**; `"${PORT}:80"` →
      none; `"80"` (no host) → none; `"127.0.0.1:8080:80"` → host `8080`; **`"8080:80/tcp"`
      → finding (suffix stripped)**; **`"[::1]:8080:80"` → none (IPv6 out of scope)**;
      long-form `published: "8080"` → finding; `container_name` → finding; `external: true`
      vol/net → non-blocking finding; explicit `name:` vol/net → non-blocking finding; a
      clean project → empty
- [ ] run `go test ./internal/core/project/config/...` — must pass before task 4

### Task 4: `workspace/tests/` validator domain + `dwe validate tests`

**Files:**
- Create: `internal/core/validate/tests/tests.go`
- Create: `internal/core/validate/tests/tests_test.go`
- Create: `internal/core/validate/tests/testdata/...` (project trees with `workspace/tests/`)
- Modify: `internal/cli/validate/validate.go` (register `tests.All()` + `dwe validate tests`)
- Modify: `internal/cli/validate/validate_test.go` (scope wiring)

- [ ] create the `tests` domain: `All()` → `scenariosValidator{}` (`Domain()=="tests"`,
      `ID()=="scenarios"`); guard `ctx.Cfg != nil`; resolve the `workspace/tests/` dir and
      iterate `*.yml`
- [ ] per file: load via `envtest.LoadScenario` (error → `SeverityError`; this also covers a
      bad scenario name via `ValidateScenarioName`); validate `timeout:` parse and
      `env.services` enable/disable ∈ `ctx.Cfg.Services`
- [ ] per file: render all steps (context = `ctx.Cfg.Raw` overlaid with scenario `env.vars`,
      `auto`-valued vars → a valid placeholder host port), wrap in ONE
      `config.DeployPhase{Name: "tests"}`, and call `pipeline.ResolvePhaseSteps` **once**
      (matches runtime `runSteps`; catches duplicate step names + phase invariants + builtin
      `with:` + `when:`) → resolve error as `SeverityError`. Do NOT validate steps
      individually and do NOT use a `${...}`-downgrade heuristic (both per codex)
- [ ] resolve the registry via `reg, ok := ctx.CommandRegistry.(*registry.Registry)`
      (re-implement — `registryFrom` is unexported in `valconfig`); nil/absent → skip command
      checks; unknown `type: command` ref → `SeverityError`
- [ ] emit `config.ScanComposeIsolation(ctx.Cfg, ctx.ProjectRoot)` findings as
      `SeverityWarning` (once, when the tests dir is present)
- [ ] register `tests.All()` in `buildRegistry`; add the `dwe validate tests` subcommand
      (scope `["tests"]`); confirm it is NOT in `preflight.Run`
- [ ] write tests: valid scenario → OK; bad name; unparseable timeout; unknown enable/disable
      service; unknown command ref; concrete (non-templated) invalid builtin `with:` → error;
      **`auto`-var in a strict-int param (`port: "${vars.db.port}"`, `db.port: auto`) → OK
      (placeholder port, no false error)**; **a concrete bad param (`status: nope`) coexisting
      with a templated `url` → Error, NOT masked** (codex regression pin); **duplicate
      top-level step names → Error (whole-phase `checkUniqueStepNames`)** (codex regression
      pin); broken `when:` → error; malformed/empty scenario file → error; **nil `ctx.Cfg` →
      no diagnostics / no panic**; a project with an isolation hazard → warning;
      `dwe validate tests` scope runs only this domain
- [ ] run `go test ./internal/core/validate/tests/... ./internal/cli/validate/...` — must pass
      before task 5

### Task 5: Runner isolation gate + `--skip-isolation-check`

**Files:**
- Modify: `internal/core/workflow/envtest/runner.go` (prepare-phase scan + gate)
- Modify: `internal/core/workflow/envtest/scenario.go` or the request type (`RunRequest.SkipIsolationCheck`)
- Modify: `internal/cli/test/run.go` (`--skip-isolation-check` flag + plumb)
- Modify: `internal/core/workflow/envtest/runner_test.go`
- Modify: `internal/cli/test/run_test.go`

- [ ] add `SkipIsolationCheck bool` to `RunRequest`; add `--skip-isolation-check` to
      `dwe test run` and plumb into the request
- [ ] in the runner prepare phase, insertion window **`runner.go:396-403`** (after `finish`,
      reporter, and `logWriter` are set up; before the `dwe validate` subprocess):
      best-effort load the copy config, run `config.ScanComposeIsolation`, print all findings
      as warnings
- [ ] gate: any `Blocking` finding && `!req.SkipIsolationCheck` → fail the scenario via
      `finish()` (teardown runs, no deploy subprocess spawned), message lists the blocking
      findings + `--skip-isolation-check` hint; `SkipIsolationCheck` → warnings only, proceed
- [ ] write tests (recording seams): a copy compose with `container_name` → scenario blocked,
      teardown invoked, deploy subprocess NOT called; same with `--skip-isolation-check` →
      deploy subprocess called; warn-only finding (external volume) → proceeds; copy-config
      load failure → scan skipped, flow proceeds to validate
- [ ] write CLI test: `--skip-isolation-check` threads into `RunRequest.SkipIsolationCheck`
- [ ] run `go test ./internal/cli/test/... ./internal/core/workflow/envtest/...` — must pass
      before task 6

### Task 6: User-facing docs + ru i18n

**Files:**
- Modify: `docs/reference/config/tests.md`
- Modify: the canonical step-schema page under `docs/reference/config/deploy/` (single page,
  e.g. `steps.md` — confirm the exact filename; reset/lifecycle reference it rather than
  re-documenting step fields, so document `timeout:` **once** and cross-link; only touch
  reset/lifecycle pages if they duplicate the step-field table)
- Modify: `docs/guides/integration-tests.md`
- Modify: `docs/i18n/ru/reference/config/tests.md`
- Modify: the ru mirror of the canonical step-schema page
- Modify: `docs/i18n/ru/guides/integration-tests.md`

- [ ] document the general `timeout:` step field once in the canonical step-schema page
      (opt-in, no default; `0`/absent = unbounded; per-step; body-only; enforced via ctx
      cancellation so it bounds ctx-honoring bodies — shell/dwe subprocesses, ctx-aware
      builtins, command steps — while a body blocked on interactive input is not
      force-interrupted); cross-link from reset/lifecycle if they list step fields
- [ ] add a `dwe validate tests` section to `tests.md` (what it checks: name, timeout,
      services, step schema, builtin `with:`, `when:`, command refs; isolation findings as
      warnings) and a "compose isolation" section (the flagged constructs, tiered fail/warn,
      `--skip-isolation-check`, the auto-port prerequisite cross-link)
- [ ] extend the guide: setting a step `timeout:`; running `dwe validate tests` in CI;
      resolving an isolation FAIL (move host ports onto vars / drop `container_name:` /
      `--skip-isolation-check` escape hatch)
- [ ] mirror all edits in the ru tree and refresh each file's `Translated from` provenance
      hash (`TestRussianTranslationsAreFresh`)
- [ ] run `make build` (re-embeds docs, regenerates content hashes) and
      `go test ./internal/core/docs/...` — must pass before task 7

### Task 7: Verify acceptance criteria

- [ ] verify spec §7 stage 3 (the three 3a features): per-step `timeout:` is general +
      opt-in + kills subprocess on expiry, absent leaves existing pipelines byte-identical;
      `dwe validate tests` validates scenario files + surfaces isolation warnings; the
      isolation scanner flags `container_name` / literal host ports (FAIL) and
      external/named vol+net (WARN), `--skip-isolation-check` downgrades, the runner gate
      blocks a hazardous run with teardown still running
- [ ] verify backward compatibility: no existing golden altered; `timeout:`-absent pipelines
      unchanged; validator/scanner purely additive
- [ ] run full suite: `make test`
- [ ] run `make lint`

### Task 8: [Final] Internals documentation + plan close-out

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md`

- [ ] extend `packages.md`: the general per-step `timeout` contract (schema →
      `ResolvedStep.Timeout` → `executeStepBody` `WithTimeout`, opt-in, body-only,
      timeout-vs-cancel discrimination, bounds **ctx-honoring** bodies only — interactive
      prompts are not force-interrupted); the tests validator's whole-phase `ResolvePhaseSteps`
      resolution + `auto`→placeholder-port render (no downgrade heuristic); the new
      `internal/core/project/config`
      `ScanComposeIsolation` (leaf, `Blocking` intrinsic, callers own severity); the
      `internal/core/validate/tests/` domain (validate-only, reuses LoadScenario +
      ResolvePhaseSteps + Get, isolation warnings); the runner isolation gate +
      `--skip-isolation-check`
- [ ] extend the AGENTS.md integration-tests bullet with the stage-3a contracts worth
      trapping: `timeout:` is general/opt-in/no-default/body-only; isolation scanner is a
      config leaf with intrinsic `Blocking` and caller-owned severity (FAIL container_name +
      literal host ports, WARN external/named); `tests` validator domain is validate-only,
      never preflight
- [ ] run `make build` (embeds updated internals docs); docs-subsystem tests green
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*No checkboxes — external follow-ups.*

- **Manual smoke** (needs a real project + Docker, interactive): add a `timeout:` to a slow
  step and confirm it fails with the timeout message; run `dwe validate tests` against a
  project with a deliberately broken scenario; add a `container_name:` / literal host port to
  a compose file and confirm `dwe test run` blocks, then `--skip-isolation-check` proceeds.
- **Vikunja task 170**: comment when stage 3a lands.
- **Stage 3b plan**: parallel scenario execution (`--parallel N`, default 1) + aggregated
  live reporter (one compact status row per scenario in TTY; silent per-scenario reporters;
  a coarse `ProgressFn` seam on `RunRequest`) — write via `/planning:make` after 3a. Includes
  a short discovery step on `liveui`/`tui` multi-row sticky rendering and a note on shared
  package-cache contention under parallelism.

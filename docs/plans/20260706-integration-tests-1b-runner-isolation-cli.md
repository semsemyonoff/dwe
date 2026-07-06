# Integration tests — stage 1b: runner, isolation, CLI, teardown

## Overview

Second of two plans implementing stage 1 (MVP) of the integration-tests feature
([spec](specs/2026-07-06-integration-tests.md) — the single source of truth for design
decisions; three review rounds, do not re-open settled decisions).
[Plan 1a](completed/20260706-integration-tests-1a-engine-and-scenario-schema.md) is
complete and landed on this branch: predicate-as-body assertions + always-run,
`http_check`, and the `envtest` scenario schema/loader/renderer.

This plan delivers everything else in spec §7 stage 1:

1. **Runner** (`internal/core/workflow/envtest`): per-scenario flock, git-aware tree
   copy, generated `local.yml` (seed + scenario `env:` + identity + `update: off`,
   `auto` port allocation), docker identity file, durable run manifest, subprocess
   `dwe validate` + `dwe deploy run --silent`, in-process `steps:` execution,
   deterministic manifest-driven teardown, per-scenario timeout.
2. **CLI** (`internal/cli/test`): `dwe test run [scenario...] --keep --timeout` and
   `dwe test list`, process-wide env scrub, live reporter + summary line, JSON output,
   exit codes 0/1/2, host-only container policy.
3. **User-facing docs** (`docs/reference/config/tests.md` + guide + ru i18n) and
   internals docs (`docs/internals/packages.md`, `AGENTS.md`).

Out of scope (spec §7): stage 2 (failure-artifact reports, `dwe test clean`) and
stage 3 (parallel scenarios, `workspace/tests/` validator domain, isolation
preflight). The run manifest must nevertheless already carry what stage-2 `clean`
needs: compose project name, copy path, bridge dir, report path.

## Context (from discovery)

Everything below was verified against the current tree; line numbers approximate.

- **1a APIs available**: `envtest.Scenario` (Description, Env{Services{Enable,Disable},
  Vars map[string]any}, Timeout raw string, Steps []config.DeployStep),
  `LoadScenario(path)`, `ListScenarios(baseDir)` (absent dir → empty),
  `TestsDir(baseDir)`, `ValidateScenarioName` (`^[a-z0-9][a-z0-9_-]*$`),
  `RenderSteps(steps, cfg)` (renders `cmd:`/`with:` string leaves against `cfg.Raw`;
  must run BEFORE `ResolvePhaseSteps`), `AutoPortSentinel = "auto"`;
  `config.ValidateDeploySteps`; `pipeline.StepForcesRun`; `http_check` builtin.
- **Flock**: `lock.Acquire(path)` (`internal/shared/lock/lock.go:44`) — non-blocking
  flock + PID file + stale-PID cleanup; returns `*lock.HeldError` when held. This is
  the bridge-daemon private-flock pattern; `dwe test` MUST use it on
  `.dwe/tests/locks/<scenario>.lock` and never `lock.AcquireProjectLocks` (spec §3).
- **Pipeline execution**: `pipeline.RunWithOptions(opts RunOptions) error`
  (`executor.go:445`); `RunOptions` fields: `Steps []ResolvedStep`, `Reporter`,
  `Name`, `Config`, `DockerConfig`, `Registry *usercommands.Registry`, `WorkDir`,
  `LogWriter`, `SkipConfirm`, `Translator`, `Locale`, `Recorder` (nil→Nop),
  `SkipDecider` (nil → all steps Run — exactly what tests need), `Context` (nil →
  own SIGINT/SIGTERM NotifyContext — the runner passes its deadline ctx instead).
  Returns `pipeline.ErrSilent` on step failure (already rendered by the reporter).
  `SkipConfirm: true` auto-passes command `confirmation:` gates
  (`runtime/confirmation.go:33`).
- **Resolve**: `pipeline.ResolvePhaseSteps(cfg, reg *registry.Registry, phase
  config.DeployPhase, service string)` (`resolve.go:64`) — wrap scenario steps in one
  synthetic `config.DeployPhase{Name: "tests", Steps: rendered}` with `service=""`.
- **Reporter**: `pipeline.OpenPipelineLog(workDir, name, logEnabled)` → `(screen
  *render.Writer, logFile io.Writer, termOut io.Writer, logPath, cleanup, err)`
  (`logging.go:33`), then `pipeline.NewPlainReporter(screen, logFile, termOut)`
  (`plain.go:148`) — same look as deploy (spec §3).
- **Deploy subprocess surface**: `dwe deploy run` has `--silent`
  (`cmdctx.AddSilent`, notification suppression only) and reads no TTY → prompts
  auto-disabled; `DWE_NONINTERACTIVE=1` is honoured by notify factory + usercommands
  runtime (`runio.IsNonInteractive`). The repo pattern for spawning `dwe` itself is
  `os.Executable()` behind an injectable seam (bridge `ensure.go` `SpawnFunc`,
  executor `resolveDweBin`) — **the test-recursion hazard applies: the runner's
  subprocess spawn must be a stubbable seam**.
- **`dwe validate` subprocess**: exit 0 = pass, 1 = errors (warnings pass without
  `--strict`); obeys cwd discovery. Cheap fail-fast before deploy (spec §6).
- **Compose identity**: `config.ResolveComposeProjectName(baseDir, cfg)`
  (`docker.go:235`) reads ONLY `project_name` from `docker.yml` deep-merged with
  `docker.local.yml`; missing `docker.yml` → falls back to `cfg.Project.FullName()`
  (= `Prefix + "-" + Name` when prefix set, `workspace.go:598`). So the generated
  `docker.local.yml` `project_name:` is authoritative **only when the copy has a
  `docker.yml`** — otherwise generate `docker.yml` itself (spec §5).
- **Semantics-neutral generated `docker.yml`** — the spec §5 prescription is
  verified correct and must be followed literally: a MISSING `docker.yml` goes
  through `LoadDockerConfigOrEmpty` → zero-value `DockerConfig` with **empty**
  args and NO defaults (`docker.go:100–105`, pinned by
  `TestLoadDockerConfigOrEmpty_MissingFile`), whereas a PRESENT `docker.yml`
  gets `applyDockerArgsDefaults` filling `up/logs/run/down` for keys absent from
  the `args:` block (`docker.go:152/197`, pinned by
  `TestLoadDockerConfig_DefaultsAppliedWhenAbsent`). A generated file with no
  `args:` block would therefore CHANGE semantics (introduce defaults a
  docker.yml-less project never had). Correct neutral shape: `project_name:` +
  `args:` with an **explicit `[]` for every `DockerArgs` key** — explicit `[]`
  marks the key present (`detectPresentArgsKeys`), opting out of defaults, which
  exactly reproduces the missing-file zero-value. Add a test proving
  `LoadDockerConfig` over the generated file yields `DockerArgs` identical to
  `LoadDockerConfigOrEmpty`'s missing-file result.
- **Compose runner**: `docker.NewCompose(cfg, dockerCfg, baseDir)` /
  `(*Compose).Exec(command, extraArgs...)` (`compose.go:58/147`) — project name via
  `-p`, files via `cfg.ComposeFiles()`. Default `down` args already include
  `--remove-orphans`; teardown must never pass `-v` (spec §6).
- **Volume removal**: `docker_remove_project_volumes` builtin
  (`builtin/containers/volumes.go:44`) — `docker volume ls -q`, prefix-filter
  `projectName + "_"`, best-effort `docker volume rm`; `shared:` volumes have no
  prefix so they survive. Logic is inline in `Run` — must be extracted into a
  reusable function for teardown (behaviour byte-identical for the builtin).
- **Container reaping**: label constant `docker.ComposeProjectLabel =
  "com.docker.compose.project"` (`labels.go:18`); `docker.RemoveContainer(ctx,
  dockerBin, name)` = idempotent `docker rm -f` (`stop.go:81`). Exact-identity
  filtering by the manifest-recorded project name satisfies the "never
  name-pattern-guessed" invariant (it IS the exact identity we created).
- **Bridge daemon stop**: `bridge.StopDaemon(bridgeDir) (signaled bool, err error)`
  (`ensure.go:245`) — SIGTERM by pidfile flock probe; stale/missing = clean no-op.
  Copy's bridge dir = `bridge.DefaultBridgeDir(copyRoot)`.
- **Free-port probing**: `env.IsPortAvailable(port)` probes a *specific* port;
  there is **no** allocate-free-port helper — write one (`net.Listen("tcp", ":0")`,
  keep listeners open until the whole batch is allocated to avoid duplicates).
- **local.yml I/O**: `local.LoadLocalYAML(path) (map[string]any, error)` /
  `local.WriteLocalYAML(path, map)` (`local_yaml.go:13/43`; the map writer is a
  wrapper over the node path — fine here, the copy's file is generated, no comments
  to preserve). Original local.yml path: `config.LocalLayerPath(workspacePath)`
  (`layers.go:94`).
- **`update: off` shape**: `UpdateConfig{Mode string}`; missing block → off, present
  block with empty mode → ON (`EffectiveMode`, `workspace.go:170`) — the generated
  overlay MUST write `update: {mode: "off"}` explicitly (yaml.v3 keeps `off` a
  string, but quote it anyway for clarity).
- **Copy**: no recursive dir-copy helper exists in the repo; no `git ls-files`
  helper in `internal/shared/git` — both are new code. Git binary via the nil-safe
  accessor `config.GitBin(cfg)` (Critical Patterns: never read `cfg.Binaries.*`).
- **CLI wiring**: subtree pattern `NewCmd(groupID string, flags *cmdctx.RootFlags)`;
  root registration in `internal/cli/root.go` (group `groupPipelines`); container
  policy is default-deny — `test` absent from `bridgeAllowedTopLevel`
  (`bridgepolicy.go:29`) means blocked with **zero code change**; pin with a test.
- **Exit codes**: fang handler (`cmd/dwe/main.go:89–95`) honours `ExitCode() int`
  on errors; `cmdctx.ExitCodeFor` maps usage-class codes → 2, others → 1. `dwe test`
  needs typed errors: scenario-failed → 1, prep/config error → 2 (spec §3).
- **JSON**: `cmdctx.WriteData[T](flags, cmd, data, renderText)` (`output.go:77`);
  live output silent in JSON mode per the usual contract.
- **i18n**: new user-visible UI strings go to
  `internal/shared/i18n/translations/en.yml` + `KnownUIKeys` (coverage test enforces
  sync); scenario `description:` stays verbatim (spec §8).
- **Docs plumbing**: reference pages under `docs/reference/config/` (+ ordered TOC
  in `index.md` drives the site sidebar), guides under `docs/guides/`, ru mirrors
  under `docs/i18n/ru/...` with provenance hashes (`TestRussianTranslationsAreFresh`);
  `make build` re-embeds.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task) — repo
  style: table-driven tests, `testdata/` fixtures.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that
  task — success and error scenarios, separate checklist items.
- **CRITICAL: all tests must pass before starting next task** — run focused package
  tests per task (`make embedded-docs` once, then `go test ./internal/...` per
  package); `make test` at the end.
- **CRITICAL: update this plan file when scope changes during implementation.**
- **CRITICAL: no test may touch the developer's real Docker daemon or spawn a real
  `dwe` subprocess** — every docker/git/subprocess interaction in the runner goes
  through an injectable seam (the repo's established pattern: `listVolumesFn`,
  `SpawnFunc`, `ensureDaemonFn`). Real-git tests use `git init` in `t.TempDir()`.
- Maintain backward compatibility: `docker_remove_project_volumes` behaviour stays
  byte-identical after the extraction; no existing golden changes.

## Testing Strategy

- **Unit tests** per task (see above). No UI e2e in this repo.
- Runner orchestration is tested with all seams stubbed: scripted subprocess
  results (validate ok/fail, deploy ok/fail/port-conflict-then-ok), fake docker
  ops, recorded teardown call order.
- Copy tests run real `git` against throwaway repos in `t.TempDir()` (init, commit,
  untracked, ignored, deleted-but-tracked, symlink) — git is already a hard test
  dependency in this repo.
- Identity/local.yml/docker.yml generation tests assert on parsed YAML (and one
  `LoadDockerConfig` equivalence test for the semantics-neutral branch).
- CLI tests: cobra command over stubbed runner seam; `test list` golden; JSON shape
  via `cmdctx.WriteData`; container-policy pin in `bridgepolicy_test.go`.
- `make test` at the end is the regression net.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates; keep in sync with actual work.

## Solution Overview

All runner logic lands in the existing `internal/core/workflow/envtest` package
(new files), keeping scenario schema + runner together; the CLI stays a thin
`internal/cli/test` layer (composition root pattern). Per scenario, the runner
executes the spec §6 flow:

```
flock → load scenario → copy tree → gen local.yml + docker identity
→ write manifest (BEFORE any Docker) → dwe validate (subprocess, fail-fast)
→ dwe deploy run --silent (subprocess; one retry on auto-port conflict)
→ steps in-process (pipeline.RunWithOptions, no SkipDecider)
→ teardown (deferred; also on failure/Ctrl+C/timeout) → result
```

Key design decisions (all from the spec):

- **No original-project locks** — only the per-scenario flock. The copy's own
  `.dwe/` locks are acquired by the deploy subprocess.
- **Manifest-driven teardown** — the manifest is durable state written before any
  side effect on Docker; teardown consumes only the manifest + copy contents, so a
  half-dead run is still sweepable (stage-2 `clean` reads the same manifests).
- **Real user path** — deploy is a real `dwe deploy run` subprocess with cwd =
  copy; nothing is re-composed in-process.
- **Teardown safety** — `compose down` without `-v`; volumes removed by exact
  test-project prefix (shared volumes survive); containers reaped by exact
  manifest-recorded `com.docker.compose.project` label value; teardown runs under a
  fresh context (never the expired scenario deadline).

## Technical Details

- **Run identity**: `runID` = 6 lowercase hex chars (crypto/rand). Compose project
  name = `<base>-t-<scenario>-<runID>` where `<base>` = `cfg.Project.Prefix` if set,
  else `cfg.Project.Name`; the result is normalised to the compose charset
  (`[a-z0-9_-]`, lowercase) — scenario names are already valid fragments (1a rule).
- **Paths** (all under the ORIGINAL project root except the copy contents):
  - copy: `.dwe/tests/runs/<scenario>/`
  - flock: `.dwe/tests/locks/<scenario>.lock`
  - manifest: `.dwe/tests/manifests/<scenario>-<runID>.yml`
  - reserved report dir (stage 2): `.dwe/tests/reports/<scenario>/`
- **Manifest fields**: `scenario`, `run_id`, `compose_project`, `copy_path`
  (absolute), `bridge_dir` (copy's `.dwe/bridge`), `report_dir` (reserved),
  `created_at` (UTC RFC3339). YAML, atomic write.
- **Copy selection**: `git -C <root> ls-files -co --exclude-standard -z` (binary
  from `config.GitBin(cfg)`); skip paths absent from the worktree (uncommitted
  deletions — worktree wins); always exclude `.dwe/`, `.env`, `.git/` (top-level
  prefixes). Symlinks recreated as symlinks (`os.Symlink`), permissions preserved
  (`io.Copy` + `os.Chmod` from source `FileInfo`). Non-git tree (or git failure) →
  full `filepath.WalkDir` copy with the same exclusions + a warning that gitignored
  artifacts are included.
- **Generated `local.yml`** (fresh map, `local.WriteLocalYAML` into the copy),
  precedence low→high per spec §5:
  1. seed = original `local.yml` map (absent file → empty), with `compose.extra`
     and `services.<name>.compose.extra` **stripped** (each strip emits a warning);
  2. scenario `env.vars` — each key is a dot-path relative to `vars.` (e.g.
     `app.http_port` → `vars: {app: {http_port: …}}`); values landing as
     `AutoPortSentinel` are replaced with freshly allocated ports (below);
     `env.services.enable/disable` → `services.<name>.enabled: true/false`;
  3. identity: `project: {prefix: <compose project name>}` and
     `update: {mode: "off"}`. Note: the docker identity file (Task 4) is the
     **authoritative** compose-name source (`readDockerProjectName` prefers it);
     `project.prefix` only feeds the `FullName()` fallback and legacy
     `ComposeProjectNameCandidates`. Both are written per spec §5 and must stay
     consistent — do not "simplify" one away.
- **Auto ports**: collect all `auto` vars, open `net.Listen("tcp", ":0")` for each,
  read the ports, close all listeners only after the whole batch is allocated
  (guarantees intra-batch uniqueness). TOCTOU accepted per spec §9 (preflight
  `ports_free` in the copy catches races; one retry).
- **Port-conflict retry**: if the deploy subprocess fails AND the scenario has auto
  vars → re-allocate all auto ports, rewrite the copy's `local.yml`, retry the
  deploy exactly once; a second failure → scenario failed. Trigger is deliberately
  ANY deploy failure with auto vars, not output-marker matching: a TOCTOU loss can
  surface either as the preflight `ports_free` text OR as a later compose bind
  error (`port is already allocated`), and string-matching rendered output is
  brittle. Spec §9 mandates exactly "one automatic retry"; a non-port failure
  simply fails again on the retry — one wasted deploy, correctness preserved.
- **Docker identity file** in the copy:
  - copy has `workspace/docker.yml` → write `workspace/docker.local.yml` containing
    only `project_name: <name>` (overwrites any copied one — last-word layer);
  - copy has NO `workspace/docker.yml` → write `workspace/docker.yml` containing
    `project_name: <name>` plus `args:` with explicit `[]` for every `DockerArgs`
    key (semantics-neutral — reproduces the missing-file zero-args config, see
    Context) AND delete any copied `workspace/docker.local.yml` (a stray local
    file would suddenly activate, spec §5).
- **Subprocess execution**: one seam, e.g.
  `type execDwe func(ctx context.Context, dir string, extraEnv []string, stdout, stderr io.Writer, args ...string) error`,
  default impl resolves `os.Executable()` and runs via `exec.CommandContext`
  (context kill on timeout). Used for `dwe validate` and
  `dwe deploy run --silent` with `extraEnv = ["DWE_NONINTERACTIVE=1"]`, cwd = copy,
  output → the run log (+ pass-through to the reporter's diag line). The process
  environment is already scrubbed (below), so children inherit clean env verbatim
  — including scenario `type: shell`/`type: dwe` steps.
- **Env scrub**: `envtest.ScrubComposeEnv()` — iterate `os.Environ()`, `os.Unsetenv`
  every `COMPOSE_*` variable; keep `DOCKER_*` (daemon/context selection must
  apply). Called exactly once at the top of the `dwe test run` RunE, before the
  flock, any goroutine, UI, or subprocess (spec §3). This is NOT the bridge
  dangerous-env strip set — no trust boundary here.
- **Timeout**: `--timeout` flag > scenario `timeout:` > default `30m`. Parsed with
  `time.ParseDuration`; one `context.WithTimeout` per scenario spanning
  validate + deploy + steps. On expiry: subprocess killed via CommandContext,
  in-process pipeline cancelled via `RunOptions.Context`, scenario marked failed,
  teardown still runs (fresh `context.Background()` + its own generous timeout,
  e.g. 5m).
- **Steps execution**: after deploy, `config.LoadConfig(copyWorkspacePath)` (fresh
  — sees allocated ports + toggles), `usercommands.LoadRegistryFromConfigPath`,
  `envtest.RenderSteps(scn.Steps, copyCfg)`, wrap in
  `config.DeployPhase{Name: "tests"}`, `ResolvePhaseSteps(copyCfg, reg, phase, "")`,
  `RunWithOptions{Config: copyCfg, DockerConfig: copyDockerCfg, Registry: reg,
  WorkDir: copyRoot, SkipConfirm: true, SkipDecider: nil, Recorder: nil,
  Context: scenarioCtx, Translator: flags.I18n, Locale: flags.Locale,
  Reporter/LogWriter from OpenPipelineLog}` — `Registry`/`Config`/`DockerConfig`
  are load-bearing (nil registry fails every `type: command` step at
  `executor.go:641`; Config/DockerConfig feed `BuildRunContext`/`ActionContext`),
  and `Translator`/`Locale` keep command i18n intact (display-string contract).
- **Teardown order** (each step best-effort, errors logged, later steps still run):
  1. `compose down` — build args via `docker.NewCompose(copyCfg, copyDockerCfg,
     copyRoot)` + `BuildArgs("down")` (default args add `--remove-orphans`;
     NEVER `-v`) but execute via `exec.CommandContext` with `Dir = copyRoot`,
     `Env = compose.BuildEnv()`, stdout/stderr → the run log — **never
     `Compose.Exec`**: it has no context parameter (a hung `down` would ignore
     the teardown deadline) and hard-wires `os.Stdout`/`os.Stderr`/`os.Stdin`
     (`compose.go:147–165`), which would leak output into JSON stdout;
  2. reap remaining containers labelled
     `com.docker.compose.project=<manifest.compose_project>` — `docker ps -aq
     --filter label=…` (**`-a` required**: exited daemons/one-offs still hold
     volume references; running-only listing would miss them — mirrors
     `labels.go`'s `--all` for `runningOnly=false`), then
     `docker.RemoveContainer` each; runs BEFORE volume removal so in-use
     volumes don't fail;
  3. remove volumes prefixed `<compose_project>_` via the extracted helper
     (shared volumes survive — no prefix);
  4. `bridge.StopDaemon(manifest.bridge_dir)`;
  5. `os.RemoveAll(copy)`;
  6. delete manifest; release flock.
  (Spec §6 lists volumes before reaping; the swap is an implementation-level fix —
  an in-use volume cannot be removed — and preserves every spec guarantee.)
  `--keep`: skip 1–6 entirely (manifest kept for stage-2 `clean`), release only the
  flock, print compose project name, copy path, and a cleanup hint.
- **Result model**: per-scenario `status ∈ {passed, failed, error}` (`error` =
  could not prepare: copy/config/manifest/validate failure), `failed_step`,
  `duration`, `report_dir` (empty until stage 2). Exit code: any `error` → 2, else
  any `failed` → 1, else 0 — via typed errors implementing `ExitCode() int`
  (fang handler contract). JSON payload
  `{scenarios: [{name, status, failed_step, duration, report_dir}], summary}` via
  `cmdctx.WriteData`; text mode prints the live pipeline output per scenario plus
  the summary line `2 passed, 1 failed (redis-off: step "app answers")`.
- **`dwe test run` semantics**: no args → all scenarios (sorted), names → exactly
  those (unknown name → prep error, exit 2); sequential execution; Ctrl+C
  (SIGINT/SIGTERM NotifyContext at the run level) cancels the current scenario,
  runs its teardown, skips the rest, reports what ran.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): stage-2 plan, manual smoke on a real
  project, Vikunja update.

## Implementation Steps

### Task 1: Run identity, paths, manifest, env scrub

**Files:**
- Create: `internal/core/workflow/envtest/run.go`
- Create: `internal/core/workflow/envtest/run_test.go`
- Create: `internal/core/workflow/envtest/manifest.go`
- Create: `internal/core/workflow/envtest/manifest_test.go`

- [x] implement run-identity helpers in `run.go`: `NewRunID()` (6 hex chars,
      crypto/rand), `ComposeProjectName(cfg, scenario, runID)` (`<base>-t-<scenario>-<runID>`,
      base = prefix-or-name, normalised to `[a-z0-9_-]` lowercase), and path
      helpers `RunDir/LockPath/ManifestPath/ReportsDir(baseDir, ...)` under
      `.dwe/tests/{runs,locks,manifests,reports}`
- [x] implement `ScrubComposeEnv()` in `run.go`: unset every `COMPOSE_*` env var,
      leave `DOCKER_*` untouched; document the call-once-at-startup contract
- [x] implement `Manifest` struct (scenario, run_id, compose_project, copy_path,
      bridge_dir, report_dir, created_at) + `WriteManifest` (atomic, mkdir -p) /
      `LoadManifest` / `DeleteManifest` in `manifest.go` — fields chosen to cover
      stage-2 `dwe test clean` (spec §7 row 2)
- [x] write tests: run-ID shape/uniqueness, project-name normalisation (prefix set /
      unset, charset), path layout, scrub (COMPOSE_* gone, DOCKER_* kept)
- [x] write tests: manifest round-trip, load of missing/corrupt file, delete
      idempotence
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 2

### Task 2: Git-aware tree copy

**Files:**
- Create: `internal/core/workflow/envtest/copy.go`
- Create: `internal/core/workflow/envtest/copy_test.go`

- [x] implement `CopyTree(srcRoot, dstRoot, gitBin string, warn func(string)) error`:
      `os.RemoveAll(dstRoot)` FIRST (the copy path is fixed per-scenario; a
      prior failed prep must never shadow/stale the fresh copy — kept/live runs
      are protected upstream by the runner's kept-run manifest guard, Task 7);
      then list files via `git -C srcRoot ls-files -co --exclude-standard -z`
      (gitBin from `config.GitBin(cfg)` at the call site); skip entries absent
      from the worktree (uncommitted deletions — worktree wins); always exclude
      top-level `.dwe/`, `.env`, `.git/`; recreate directories/permissions;
      symlinks copied as symlinks
- [x] implement the non-git fallback: `git ls-files` failure (not a repo / git
      error) → full `filepath.WalkDir` copy with the same exclusions + one warning
      via `warn` (gitignored artifacts included, spec §5)
- [x] write tests against real `git init` repos in `t.TempDir()`: tracked +
      untracked copied; `.gitignore`d file NOT copied; `.dwe/`/`.env`/`.git/`
      excluded; tracked-but-deleted file skipped; symlink recreated; nested dirs +
      permissions; copy over a pre-existing destination leaves NO stale files
- [x] write tests for the fallback: non-git dir copied fully minus exclusions,
      warning emitted; unreadable source → error
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 3

### Task 3: Generated `local.yml` (seed + scenario env + identity) and auto ports

**Files:**
- Create: `internal/core/workflow/envtest/localyaml.go`
- Create: `internal/core/workflow/envtest/localyaml_test.go`
- Create: `internal/core/workflow/envtest/ports.go`
- Create: `internal/core/workflow/envtest/ports_test.go`

- [x] implement `AllocatePorts(n int) ([]int, error)` in `ports.go`: open n
      `net.Listen("tcp", ":0")` listeners, harvest ports, close only after the full
      batch (intra-batch uniqueness); TOCTOU accepted per spec §9
- [x] implement `BuildLocalOverlay(seed map[string]any, scn *Scenario,
      projectName string, ports map[string]int, warn func(string)) (map[string]any, error)`
      in `localyaml.go`: strip `compose.extra` + `services.<name>.compose.extra`
      from the seed (warning each); overlay scenario `env.vars` as dot-paths under
      `vars:` (`AutoPortSentinel` values replaced from `ports`, keyed by var path);
      `env.services.enable/disable` → `services.<name>.enabled`; then
      `project: {prefix: projectName}` + `update: {mode: "off"}` (explicit value —
      empty mode means ON, `EffectiveMode` contract)
- [x] implement `WriteGeneratedLocalYAML(copyRoot string, overlay map[string]any)`
      via `local.WriteLocalYAML` into the copy's `workspace/local.yml`; seed reader
      uses `local.LoadLocalYAML(config.LocalLayerPath(origWorkspacePath))`
      (absent → empty map)
- [x] write tests: seed preserved; both compose.extra shapes stripped + warned;
      dot-path var expansion incl. nesting collision with seeded scalar (map wins /
      clear error — pick and pin); `auto` replaced by allocated port; enable +
      disable toggles; identity + update block exact YAML; precedence (scenario
      var overrides seeded var)
- [x] write tests: `AllocatePorts` returns n distinct free ports; n=0
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 4

### Task 4: Docker identity file generation

**Files:**
- Create: `internal/core/workflow/envtest/dockeridentity.go`
- Create: `internal/core/workflow/envtest/dockeridentity_test.go`

- [x] implement `WriteDockerIdentity(copyRoot, projectName string) error`: if
      `workspace/docker.yml` exists in the copy → write
      `workspace/docker.local.yml` with only `project_name:` (overwrite any copied
      one); else → write `workspace/docker.yml` with `project_name:` + explicit
      `[]` for every `args:` key (semantics-neutral per spec §5 — a no-`args:`
      file would gain the up/logs/run/down defaults a docker.yml-less project
      never had) AND remove any stray copied `workspace/docker.local.yml`
- [x] write tests: both branches produce a file whose
      `config.ResolveComposeProjectName(copyRoot, cfg)` = projectName (base file
      present with its own `project_name:` template → local overrides it)
- [x] write the semantics-equivalence test: `config.LoadDockerConfig` over the
      generated `docker.yml` yields `DockerArgs` identical to
      `LoadDockerConfigOrEmpty`'s missing-file zero-value (all args empty, no
      defaults), pinning the neutrality invariant
- [x] write test: stray-`docker.local.yml` removal in the generated-`docker.yml`
      branch
- [x] run `go test ./internal/core/workflow/envtest/... ./internal/core/project/config/...`
      — must pass before task 5

### Task 5: Reusable project-volume removal (extract from builtin)

**Files:**
- Modify: `internal/core/execution/builtin/containers/volumes.go`
- Modify: `internal/core/execution/builtin/containers/volumes_test.go` (or the
  package's existing test file)

- [ ] extract the list/filter/remove core of `RemoveProjectVolumes.Run` into an
      exported function, e.g. `RemoveVolumesByProjectPrefix(ctx context.Context,
      dockerBin, projectName string, logf func(format string, args ...any)) error`
      — prefix `projectName + "_"`, per-volume best-effort removal, keep the
      existing `listVolumesFn`/`removeVolumeFn` seams working
- [ ] rewire the builtin's `Run` through the extracted function — behaviour and
      log output byte-identical (existing builtin tests are the regression net)
- [ ] write tests for the exported function directly: prefix filtering (shared
      unprefixed volume survives), single-volume rm failure logged + others still
      removed, empty list no-op
- [ ] run `go test ./internal/core/execution/builtin/...` — must pass before task 6

### Task 6: Teardown

**Files:**
- Create: `internal/core/workflow/envtest/teardown.go`
- Create: `internal/core/workflow/envtest/teardown_test.go`

- [ ] implement `Teardown(ctx, m *Manifest, deps TeardownDeps, warn func(string)) error`
      driven ONLY by the manifest + copy contents, with every external action
      behind a `TeardownDeps` seam (compose down, list/remove containers, remove
      volumes, stop bridge daemon, remove dir, delete manifest); order: compose
      down (args from `docker.NewCompose` + `BuildArgs("down")`, run via
      `exec.CommandContext` + `BuildEnv()` + `Dir=copy`, output → run log —
      NEVER `Compose.Exec`, see Technical Details; never `-v`) → reap containers
      by exact label `com.docker.compose.project=<m.compose_project>` via
      `docker ps -aq --filter` (`-a`: exited containers hold volume refs) +
      `docker.RemoveContainer` → `RemoveVolumesByProjectPrefix` →
      `bridge.StopDaemon(m.bridge_dir)` → `os.RemoveAll(copy)` → `DeleteManifest`;
      each step best-effort (log + continue), errors joined into the return
- [ ] handle the copy-already-gone / config-unloadable degradation: when the copy's
      compose file set cannot be built, skip compose down with a warning and still
      run container reap + volume removal + the rest (manifest-driven resilience,
      spec §6)
- [ ] default `TeardownDeps` wires the real implementations; document that teardown
      callers must pass a FRESH context (never the expired scenario deadline)
- [ ] write tests with a recording fake deps: full order; failure in step k → later
      steps still run + error joined; copy-missing degradation; `-v` never present
      in compose args and `ps` args include `-a` + the exact label filter (assert
      on recorded args); compose down killed when the teardown ctx expires
- [ ] run `go test ./internal/core/workflow/envtest/...` — must pass before task 7

### Task 7: Runner orchestration

**Files:**
- Create: `internal/core/workflow/envtest/runner.go`
- Create: `internal/core/workflow/envtest/runner_test.go`

- [ ] implement `Runner` with seams (`execDwe` subprocess func, teardown deps,
      port allocator, clock-free where possible) and
      `RunScenario(ctx, RunRequest) (*ScenarioResult, error)` implementing spec §6:
      acquire flock (`lock.Acquire(LockPath(...))`; `*lock.HeldError` → typed
      fail-fast prep error; NEVER `lock.AcquireProjectLocks`) → `LoadScenario` →
      resolve timeout (flag > scenario > 30m default; `time.ParseDuration`) →
      **kept-run guard**: existing manifest(s) matching `<scenario>-*.yml` →
      fail fast with a cleanup hint (a `--keep` environment or half-dead run
      owns the copy path; blindly `os.RemoveAll`-ing it would delete the kept
      debug environment and orphan its bridge daemon/Docker resources while the
      stale manifest lives on) → `CopyTree` →
      `AllocatePorts`+`BuildLocalOverlay`+write local.yml →
      `WriteDockerIdentity` → `WriteManifest` (BEFORE any Docker interaction) →
      subprocess `dwe validate` (cwd=copy; non-zero exit → prep error) →
      subprocess `dwe deploy run --silent` (cwd=copy, `DWE_NONINTERACTIVE=1`,
      stdout/stderr → run log) → steps in-process → deferred teardown (fresh ctx)
      unless `--keep`
- [ ] implement the auto-port conflict retry: deploy failed + scenario has auto
      vars → re-allocate, rewrite local.yml, retry deploy exactly once (no
      output-marker matching — see Technical Details; a TOCTOU loss can surface
      as either the preflight `ports_free` text or a compose bind error)
- [ ] prep failure AFTER `CopyTree` but BEFORE `WriteManifest` → best-effort
      `os.RemoveAll(copy)` on the way out (no manifest exists yet, so stage-2
      `clean` could not find the leftover otherwise)
- [ ] implement steps execution: fresh `config.LoadConfig` on the copy,
      `usercommands.LoadRegistryFromConfigPath`, `RenderSteps`, synthetic
      `config.DeployPhase{Name: "tests"}`, `ResolvePhaseSteps(copyCfg, reg, phase, "")`,
      `pipeline.RunWithOptions{Config: copyCfg, DockerConfig: copyDockerCfg,
      Registry: reg, WorkDir: copy, SkipConfirm: true, SkipDecider: nil,
      Recorder: nil, Context: scenarioCtx, Translator: flags.I18n,
      Locale: flags.Locale}` with `OpenPipelineLog`/`NewPlainReporter`; step
      failure (incl. `pipeline.ErrSilent`) → scenario failed with the step name
      recorded; confirm at implementation whether `reg.ApplyVisibility` is needed
      before `ResolvePhaseSteps` (deploy calls it at `deploy.go:400`; host-side
      resolution via `Registry.Get` likely doesn't require it — verify, don't
      assume). **Reporter/log construction is CLI-injected, not hardwired**: the
      runner takes a reporter+writers factory in `RunRequest` (default =
      `OpenPipelineLog` + `NewPlainReporter`); `OpenPipelineLog` always returns
      an `os.Stdout`-backed screen writer (`logging.go:33–57`), so in JSON mode
      the CLI must inject a silent screen/`io.Discard` variant that preserves
      the file log — otherwise live output leaks into JSON stdout (contract
      violation)
- [ ] implement `ScenarioResult{Name, Status(passed/failed/error), FailedStep,
      Duration, ReportDir}` and `--keep` handling (skip teardown, keep manifest,
      return project name + copy path + cleanup hint for the CLI to print)
- [ ] write tests (all seams stubbed, no real docker/dwe): happy path incl.
      teardown-called-once; prep failure before manifest → no teardown of Docker
      resources; validate failure → status error, teardown still removes copy;
      deploy failure → status failed + teardown; port-conflict retry (once, not
      twice); step failure → failed step name; timeout expiry → failed + teardown
      ran under fresh ctx; `--keep` skips teardown + keeps manifest; second run of
      same scenario while flock held → fail fast with held-lock error; run after
      a `--keep` run (manifest present, flock free) → fail fast with the cleanup
      hint, kept copy untouched
- [ ] run `go test ./internal/core/workflow/envtest/...` — must pass before task 8

### Task 8: CLI `dwe test` (run/list), registration, container policy, i18n

**Files:**
- Create: `internal/cli/test/test.go`
- Create: `internal/cli/test/run.go`
- Create: `internal/cli/test/list.go`
- Create: `internal/cli/test/test_test.go` (+ per-file tests as needed)
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/bridgepolicy_test.go`
- Modify: `internal/shared/i18n/translations/en.yml`
- Modify: `internal/shared/i18n/known_keys.go` (KnownUIKeys)

- [ ] implement `NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command`
      (`dwe test`) with subcommands `run` and `list`; register in `root.go` under
      `groupPipelines`; `test` requires a project (NOT added to
      `allowedWithoutProject`)
- [ ] implement `test run [scenario...]`: `envtest.ScrubComposeEnv()` FIRST (before
      flock/goroutines/UI/subprocesses — spec §3); flags `--keep`,
      `--timeout <dur>`; no args → all scenarios sorted, names validated against
      `ListScenarios` (unknown → prep error); sequential `RunScenario` per name
      with a run-level SIGINT/SIGTERM NotifyContext (current scenario cancelled +
      torn down, rest skipped); summary line
      `N passed, M failed (name: step "…")`; `--keep` prints project name, copy
      path, cleanup hint
- [ ] implement exit-code mapping via typed errors with `ExitCode() int`: any
      prep `error` scenario → 2, else any `failed` → 1, else 0 (fang handler
      contract; failure output already rendered → the sentinel error renders NO
      text, following `deployCancelledError`'s pattern, so JSON stdout stays the
      sole payload and no stray `Error:` line hits stderr)
- [ ] implement JSON mode: `{scenarios: [{name, status, failed_step, duration,
      report_dir}], summary}` via `cmdctx.WriteData`; live pipeline output and
      summary silenced in JSON mode (usual contract) — the CLI injects the
      silent reporter/screen-writer factory into `RunRequest` (file log still
      written; deploy-subprocess output goes to the log regardless); `--pretty`
      supported; add a test asserting JSON stdout contains ONLY the payload
- [ ] implement `test list`: scenario name + `description:` (verbatim, spec §8)
      via `LoadScenario` per name; text table + JSON list via `cmdctx.WriteData`
- [ ] add new user-visible UI strings (summary labels, keep-hint, list headers) to
      `translations/en.yml` + `KnownUIKeys` (coverage test enforces sync); thread
      `flags.I18n`
- [ ] extend `bridgepolicy_test.go`: `dwe test …` is blocked in container context
      (`DWE_INVOKED_FROM=container`) — pins the deliberate absence from
      `bridgeAllowedTopLevel` (zero production change)
- [ ] write CLI tests over a stubbed runner seam: exit codes 0/1/2; scenario-name
      arg validation; `--timeout` parse error; summary golden; JSON shape for run
      + list; `test list` with no tests dir (empty, no error); scrub called before
      runner
- [ ] run `go test ./internal/cli/... ./internal/core/workflow/envtest/...` — must
      pass before task 9

### Task 9: User-facing docs + ru i18n

**Files:**
- Create: `docs/reference/config/tests.md`
- Modify: `docs/reference/config/index.md` (ordered TOC — drives site sidebar)
- Create: `docs/guides/integration-tests.md`
- Modify: `docs/guides/index.md`
- Create: `docs/i18n/ru/reference/config/tests.md`
- Create: `docs/i18n/ru/guides/integration-tests.md`
- Modify: ru index mirrors as applicable

- [ ] write `docs/reference/config/tests.md`: scenario schema (fields, name rule,
      strict decode, empty-file error), `env.vars`/`auto` + the vars-routed-ports
      prerequisite (spec §4, documented prominently), `env.services`, `timeout`,
      steps = deploy-step schema + loader-side `${...}` rendering, `dwe test
      run/list` flags + exit codes, isolation model summary (copy contents,
      `.dwe/tests/` layout, manifest, teardown incl. shared-volume survival,
      `--keep`), documented limitations from spec §9 (`.git/` excluded, named
      compose resources, host side effects, non-atomic copy, `~/.config/dwe`
      shared)
- [ ] write the task-oriented guide (`docs/guides/integration-tests.md`): writing a
      deploy test for an arbitrary stack — moving ports onto vars, first scenario,
      private test commands via `type: command`, debugging with `--keep`
- [ ] add both pages to the ordered TOCs (`reference/config/index.md`,
      `guides/index.md`)
- [ ] mirror all edits in `docs/i18n/ru/...` and refresh each ru file's
      `Translated from` provenance hash (`TestRussianTranslationsAreFresh`)
- [ ] run `make build` (re-embeds docs, regenerates content hashes) and
      `go test ./internal/core/docs/...` — must pass before task 10

### Task 10: Verify acceptance criteria

- [ ] verify against spec §3: UX surface (`run [scenario...] --keep --timeout`,
      `list`, exit codes 0/1/2, no original-project locks, per-scenario flock,
      live reporter + summary, JSON contract, env scrub once at startup)
- [ ] verify against spec §5/§6: copy selection rules, generated local.yml
      precedence + strips, docker identity both branches, manifest-before-Docker,
      teardown order + no `-v` + shared volumes survive + bridge daemon stop +
      `--keep`, timeout kills + teardown still runs, port retry once
- [ ] verify the manifest carries everything stage-2 `clean` needs (compose
      project, copy path, bridge dir, report path)
- [ ] manual smoke on the tbm live test project: one trivial scenario (deploy +
      `http_check`), run `dwe test run`, verify full teardown (no containers,
      volumes, copy, manifest left; shared caches intact), then `--keep` + manual
      cleanup
- [ ] run full suite: `make test` — must pass
- [ ] run `make lint` — must be clean

### Task 11: [Final] Internals documentation + plan close-out

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md`

- [ ] update `docs/internals/packages.md`: extend the `envtest` section (runner,
      copy, identity generation, manifest, teardown deps/order, subprocess seams +
      test-recursion hazard) and add the `internal/cli/test/` section (env scrub
      placement, exit-code mapping, no-original-locks contract, container policy)
- [ ] extend the AGENTS.md stage-1a critical-pattern bullet with the 1b contracts
      worth trapping: no-original-locks + per-scenario flock, manifest-before-Docker,
      teardown never `-v`, exact-identity cleanup (never name-pattern), env scrub
      once, subprocess seam hazard
- [ ] run `make build` (embeds updated internals docs); docs-subsystem tests green
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*No checkboxes — external follow-ups.*

- **Stage 2 plan** (failure-artifact reports into `.dwe/tests/reports/<scenario>/`,
  `dwe test clean`, `--output json` polish): write via `/planning:make` after 1b
  lands, folding in runner-shape learnings.
- **Vikunja task 170**: comment when stage 1 (1a+1b) lands; feature is usable
  end-to-end.
- **PR**: stage 1a + 1b together constitute the `feat/47-integration-tests-stage1`
  branch deliverable.

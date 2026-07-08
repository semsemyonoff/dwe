# Integration tests — stage 2: failure reports & `dwe test clean`

## Overview

Third plan in the integration-tests feature
([spec](specs/2026-07-06-integration-tests.md) — the single source of truth for design
decisions; do not re-open settled decisions). Stage 1 (MVP) is complete and landed:
[1a](completed/20260706-integration-tests-1a-engine-and-scenario-schema.md) (engine
relaxation, `http_check`, scenario schema/loader/renderer) and
[1b](completed/20260706-integration-tests-1b-runner-isolation-cli.md) (runner, isolation,
CLI `run`/`list`, teardown).

This plan delivers spec §7 stage 2 ("Reports & cleanup"):

1. **Failure-artifact reports** into `.dwe/tests/reports/<scenario>/` — the scenario's
   pipeline log, `docker compose ps` output, and container log tails — collected from a
   failed run *before* teardown destroys the environment, so the debugging material
   survives (CI-artifact friendly).
2. **`dwe test clean`** — strictly manifest-driven destruction of orphaned/kept test
   environments, reusing the stage-1 `Teardown`; a `com.docker.compose.project` label
   scan may only **report** suspicious leftovers, never destroy by name pattern.
3. **`--output json`** for `clean` (run/list JSON landed in 1b), and actually
   **populating `report_dir`** in `run`'s JSON/text (reserved-but-empty since 1b).

The feature already reserved everything stage 2 needs: `Manifest.ReportDir` is populated
(`ReportsDir(baseDir, scenario)`), `ScenarioResult.ReportDir` + the `report_dir` JSON/text
field are plumbed through the CLI (always empty today), and `Teardown` is fully
manifest-driven and idempotent — `clean` reuses it verbatim.

Out of scope (spec §7): stage 3 (per-step timeouts, parallel scenario execution,
`workspace/tests/` validator domain, compose isolation preflight).

## Context (from discovery)

Everything below was verified against the current tree this session; line numbers approximate.

- **`internal/core/workflow/envtest/runner.go`** — `RunScenario` and its single
  `finish(status, failedStep)` choke-point (~342): applies the deadline→`StatusFailed`
  override, sets `result.Status`/`FailedStep`, calls `teardown()`, then records duration.
  `manifest` (`.ReportDir`, `.CopyPath`, `.ComposeProject`), `copyRoot`, and `req.Keep`
  are all in `finish`'s closure — the exact point to insert report collection *before*
  `teardown()`. `Runner` already carries injectable seams (`execDwe`, `allocatePorts`,
  `newTeardownDeps`, `clock`) wired in `NewRunner`; `teardown()` uses a **fresh**
  `context.Background()` + `teardownTimeout` (never the expired scenario deadline) — the
  report collector must do the same (a timeout failure has a cancelled `scenarioCtx`).
  `ScenarioResult.ReportDir` exists, documented "always empty in stage 1".
- **`internal/core/workflow/envtest/teardown.go`** — `Teardown(ctx, m, deps, warn)`,
  `TeardownDeps`, `NewTeardownDeps(manifestPath, log)`, and the real docker helpers to
  model the report/orphan docker seams on: `loadCopyConfig(copyPath)` (returns the copy's
  `*DweConfig`/`*DockerConfig`, **the graceful-degradation trigger** — failure ⇒ no
  compose file set), `dockerBinForCopy(copyPath)`, `reapContainersReal`/
  `listContainersByLabel` (the `docker ps -aq --filter label=<ComposeProjectLabel>=<proj>`
  pattern), `composeDownReal` (`docker.NewCompose` + `BuildArgs` + `exec.CommandContext`,
  never `Compose.Exec`), and the package-level command seams `listContainersFn` /
  `removeContainerFn` / `runComposeDownFn` (the established test-isolation pattern to
  mirror). `docker.ComposeProjectLabel = "com.docker.compose.project"`.
- **`internal/core/workflow/envtest/manifest.go`** — `Manifest` (durable `Scenario`,
  `RunID`, `ComposeProject`, `CopyPath`, `BridgeDir`, `ReportDir`, `CreatedAt`),
  `LoadManifest` (missing → `os.ErrNotExist`), `DeleteManifest` (idempotent),
  `WriteManifest` (atomic). Scenario name is read authoritatively from `m.Scenario`
  (no filename parsing needed for the flock).
- **`internal/core/workflow/envtest/run.go`** — path helpers `RunDir` / `LockPath` /
  `ManifestPath` / `ManifestsDir` / `ReportsDir(baseDir, scenario)`;
  `ComposeProjectName(cfg, scenario, runID)` = `<base>-t-<scenario>-<runID>` normalised to
  `[a-z0-9_-]` (base = `Prefix` if set else `Name`); `existingManifestPaths(baseDir,
  scenario)` + `manifestRunIDSuffix` regex — the pattern to generalise into
  `ListManifests(baseDir)`. `ScrubComposeEnv()` (unset every `COMPOSE_*`).
- **`internal/core/execution/pipeline/logging.go`** — `OpenPipelineLog(workDir, name,
  enabled)` writes the run log to `<workDir>/.dwe/logs/<name>.log` as a raw `*os.File`;
  the runner opens it with `name="test"`, so the report source path is
  `copyRoot/.dwe/logs/test.log`. At `finish` time the log is quiescent (deploy subprocess
  and steps pipeline have both returned), so copying it by path is race-free.
- **`internal/cli/test/run.go`** — `runTestRun`, the `scenarioOutcome` model,
  `testScenarioJSON{ReportDir}` (already emits `report_dir`), `renderScenarioLine` (the
  `--keep` hint `"… clean up manually when done"` at ~315 to update), `jsonReporterFactory`,
  `buildSummary`, `newRunner` seam, and exit-code mapping via `testRunOutcomeError`.
- **`internal/cli/test/list.go` / `test.go`** — the thin-CLI patterns (`cmdctx.WriteData`,
  `cmdctx.Err`/`ErrWrap`, `NewCmd` subtree, `SilenceUsage`) to mirror for `clean`.
- **`internal/shared/lock/lock.go`** — `Acquire(path) (*Lock, error)` non-blocking flock,
  `*HeldError` when held (with stale-PID cleanup); `(*Lock).Release()`.
- **`internal/shared/docker/compose.go`** — `NewCompose(cfg, dockerCfg, dir)`,
  `BuildArgs(command string, extraArgs ...string)`, `BuildEnv()`, `BinName()`.
- **`internal/cli/bridgepolicy_test.go:267`** — `hidden := []string{… "test" …}` already
  pins the whole `test` subtree as container-blocked (top-level policy), so `clean` is
  blocked with zero production change; the test just needs an assertion that keeps this true.
- **`internal/core/project/config`** — `LoadConfigOrWrap`, `LoadDockerConfigOrEmpty`,
  `DockerBin(cfg)` (nil-safe), `GitBin(cfg)`.
- **Docs** — `docs/reference/config/tests.md` + `docs/guides/integration-tests.md` (extend),
  ru mirrors under `docs/i18n/ru/...` with `Translated from` provenance hashes
  (`TestRussianTranslationsAreFresh`), `docs/internals/packages.md` (envtest + cli/test),
  `AGENTS.md` integration-tests bullet. `make build` re-embeds docs.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task) — repo style:
  table-driven tests, `testdata/` fixtures, injectable seams (no real Docker / no real
  `dwe` subprocess in any test).
- Complete each task fully before moving to the next; small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** — success and error scenarios,
  listed as separate checklist items.
- **CRITICAL: all tests must pass before starting the next task** — run focused package
  tests per task (`make embedded-docs` once, then `go test ./internal/...` per package);
  `make test` + `make lint` at the end.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Backward compatibility: report collection is purely additive; `Teardown` behaviour stays
  byte-identical (`clean` reuses it unchanged); no existing golden changes except where a
  test explicitly covers the new capability.

## Testing Strategy

- **Unit tests** per task. No UI e2e in this repo.
- **No test touches the real Docker daemon or spawns a real `dwe`** — every docker/
  subprocess interaction goes through an injectable seam (high-level `ReportDeps` /
  package-level command seams / `cleanTeardownFn` / `listComposeProjectsFn`), mirroring
  the stage-1 `listContainersFn` / `runComposeDownFn` / `newTeardownDeps` pattern.
- Report-collector tests: high-level `ReportDeps` stubs assert the report directory is
  overwritten and the three artifacts are written; the real impls' compose-vs-fallback
  degradation is tested via the low-level capture seam against throwaway copy dirs (valid
  config ⇒ compose path; missing/broken config ⇒ identity-label fallback).
- `Clean` tests: scripted manifests in `t.TempDir()`, a recording `cleanTeardownFn`, a
  scripted `listComposeProjectsFn`; real `lock.Acquire` on a throwaway lock to exercise
  the held/free branches; orphan-subtraction math.
- CLI tests: cobra command over a stubbed `cleanFn` seam — exit codes, `--dry-run`/args
  threading, JSON shape, text summary golden, `ScrubComposeEnv` called first.
- `make test` at the end is the regression net (existing goldens must stay byte-identical).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep the plan in sync with the actual work.

## Solution Overview

Two independent seams, both in the existing `internal/core/workflow/envtest` package, plus
a thin CLI layer:

1. **Report collection** — a new `CollectReport(ctx, m, deps, warn)` writing three
   artifacts into `m.ReportDir` (cleared first for honest overwrite), with Docker-touching
   parts behind a `ReportDeps` seam. The `Runner` gains a `collectReport` seam field
   (default = real impl) called once from `finish`, **before** teardown, only when
   `!req.Keep && status != StatusPassed`, under a **fresh** context; it sets
   `result.ReportDir`. Best-effort throughout — collection never fails the scenario.
2. **`envtest.Clean`** — enumerate manifests, per-scenario flock-guard live runs
   (`*lock.HeldError` ⇒ skip), reuse `Teardown` for the destruction, support `--dry-run`
   and an optional name filter, and a **report-only** orphan scan (docker projects with the
   test-name prefix `<base>-t-` that have no manifest). Returns a structured `CleanResult`.
3. **CLI `dwe test clean`** — a thin composition-root command mirroring `run`/`list`:
   scrub `COMPOSE_*` first, call `Clean`, render text + JSON via `cmdctx.WriteData`.

## Technical Details

- **Report directory** (`m.ReportDir` = `.dwe/tests/reports/<scenario>/`): `os.RemoveAll`
  then `os.MkdirAll` before writing (per-scenario overwrite — the latest failure is what
  you debug). Artifacts:
  - `pipeline.log` — `copyFile(copyRoot/.dwe/logs/test.log → reportDir/pipeline.log)`
    (pure fs; a missing source is skipped with a warning, not an error).
  - `compose-ps.txt` — `ReportDeps.PS(ctx, m)`.
  - `container-logs.txt` — `ReportDeps.Logs(ctx, m)` (single combined file; const
    `reportLogTailLines = 200`).
- **`ReportDeps`** (high-level, 2 functions, mirrors `TeardownDeps`):
  `PS func(ctx, *Manifest) (string, error)` and `Logs func(ctx, *Manifest) (string,
  error)`. `NewReportDeps()` wires `reportPSReal` / `reportLogsReal`.
- **Graceful degradation** (in the real impls): try `loadCopyConfig(m.CopyPath)`; on
  success run `docker compose ps --all` / `docker compose logs --no-color --tail 200` via
  `docker.NewCompose(...).BuildInternalArgs("ps", "--all")` / `BuildInternalArgs("logs",
  "--no-color", "--tail", "200")`. The `ps` capture **must pass `--all`** — `docker compose
  ps` defaults to running-only, so a service that crashed/exited during deploy (exactly what
  a failure report exists to surface) would be dropped; this restores parity with teardown's
  `docker ps -aq` (`-a`, per its own comment) and the identity fallback's `ps -a`.
  **`BuildInternalArgs`, NOT `BuildArgs`**: `BuildArgs`
  injects global args (`--ansi always`/`--progress tty`) *and* per-command policy defaults
  (`dockerCfg.Args.Logs`), so a project whose `docker.yml` sets `args.logs: ["-f"]` would
  make report collection follow logs forever (hanging the failure path until
  `reportTimeout`) and pollute the artifact with ANSI; `BuildInternalArgs` bypasses both
  (`compose.go:150`, the same choice health/probe queries make). On config-load failure
  fall back to identity-label —
  `docker ps -a --filter label=com.docker.compose.project=<proj>` for `PS`, and for `Logs`
  iterate container ids from the label scan running `docker logs --tail 200 <id>`, each
  prefixed with a `==== <id> ====` header. A single generic capture seam
  `captureCmdFn(ctx, bin string, args, env []string, dir string) ([]byte, error)` (default
  = `exec.CommandContext(...).Output()`) backs both compose and docker captures; the label
  scan reuses the existing `listContainersFn`. Both `PS` and `Logs` return whatever they
  captured even on partial error (the collector writes it and warns).
- **Report trigger / context**: in `finish`, after the status is finalised (incl. the
  deadline→`StatusFailed` override) and before `teardown()`:
  `if r.collectReport != nil && !req.Keep && status != StatusPassed { rctx :=
  WithTimeout(Background(), reportTimeout); if dir, err := r.collectReport(rctx, manifest,
  warn); err == nil { result.ReportDir = dir } else warn(...) }`. `reportTimeout` const
  (e.g. 2m). The **`r.collectReport != nil` guard is load-bearing**: `runner_test.go` builds
  `&Runner{...}` inline literals (there is no shared constructor), ~6 of which reach `finish`
  on a non-passed status; production `NewRunner` always sets the seam, so the guard is
  test-ergonomics (a literal that doesn't care about reports leaves it nil, one that asserts
  collection sets a recording stub). Ordering matters: **before** compose down (containers
  alive) and **before** `RemoveCopy` (log still present).
- **`Runner.collectReport` seam**:
  `func(ctx, *Manifest, warn func(string)) (string, error)`; `NewRunner` default =
  `func(ctx, m, warn){ return CollectReport(ctx, m, NewReportDeps(), warn) }`. Tests inject
  a recording stub (like `execDwe`/`newTeardownDeps`).
- **`ListManifests(baseDir) ([]string, error)`** — generalises `existingManifestPaths`
  across all scenarios: every `<scenario>-<runid>.yml` under `ManifestsDir(baseDir)`
  (validated by the run-id suffix), sorted; absent dir → `(nil, nil)`.
- **`Clean(ctx, CleanRequest) (*CleanResult, error)`**:
  - `CleanRequest{BaseDir string, Scenarios []string, DryRun bool, Warn func(string)}`.
  - `origCfg` is needed **only** by the orphan scan (base prefix + docker bin) — sweeping
    resolves everything from the manifest + copy (`Teardown` → `dockerBinForCopy`). So a
    `config.LoadConfigOrWrap` failure is **not** a hard error: `warn` + skip the orphan
    scan and still sweep every manifest (a broken/mid-edit root config must not block a
    recovery sweep — consistent with `Teardown`'s manifest-only philosophy). The only hard
    error is an **unreadable** manifests dir (can't enumerate what to sweep); an *absent*
    one yields an empty result.
  - `paths, _ := ListManifests(baseDir)`; `known := {}` = **every** manifest's
    `ComposeProject` (load the full unfiltered set first — even a filtered-out or
    skipped-live manifest still "explains" its docker project, so it is not an orphan).
  - filter `paths` by `req.Scenarios` (match on the loaded `m.Scenario`) when non-empty.
  - per manifest: `lk, err := lock.Acquire(LockPath(baseDir, m.Scenario))`; classify with
    `errors.As(err, &he)` (not a bare type assertion, so future wrapping can't reclassify a
    held lock) → `Skipped{reason:"live"}`; any other lock error → `Skipped{reason:"lock
    error"}` + warn (never destroy on uncertainty); free → if `DryRun` record
    `Swept`(would-sweep) and release; else `cleanTeardownFn(freshCtx, manifestPath, m,
    warn)` (fresh `WithTimeout(Background(), teardownTimeout)`; Teardown deletes the
    manifest last) — on **nil** error record `Swept`, on a **non-nil** `Teardown` error
    record `Failed{…, Error}` (**not** swept) + warn; release the flock either way. A
    partial teardown must never masquerade as swept: `Teardown` is best-effort and returns a
    *joined* error after attempting every step (it may even have deleted the manifest — the
    recovery anchor — after an earlier container/volume/copy-removal failure), so counting it
    as swept + exit 0 would report false success and let the next `dwe test run` hit the
    leftover. `Failed` drives a non-zero exit (below).
  - **orphan scan (report-only, best-effort)**: skipped entirely when origCfg is absent
    (above). Else `projects, err := listComposeProjectsFn(ctx, DockerBin(origCfg))`; an
    error is `warn`-ed and yields **empty orphans** — it never aborts `Clean` (a fully
    successful sweep must not fail because the advisory scan couldn't reach Docker).
    `prefix := normalize(projectBaseName(origCfg) + "-t-")` — fed through the **same**
    normaliser `ComposeProjectName` uses so it is provably identical (extract a shared
    `projectBaseName(cfg)` + reuse the existing normalise fn to avoid drift; the `-t-`
    boundary keeps `normalize(base+"-t-") == normalize(base)+"-t-"`). Orphans = distinct
    `projects` with that prefix minus `known`; each → `Orphans{ComposeProject, Note:"no
    manifest — remove manually"}`. Never torn down. `listComposeProjectsFn(ctx, dockerBin)
    ([]string, error)` default = `docker ps -a --format '{{.Label
    "com.docker.compose.project"}}'`, deduped non-empty.
  - `CleanResult{DryRun bool, Swept []CleanEntry, Skipped []SkippedEntry, Failed
    []FailedEntry, Orphans []OrphanEntry}`; `CleanEntry{Scenario, ComposeProject, CopyPath}`,
    `SkippedEntry{CleanEntry, Reason}`, `FailedEntry{CleanEntry, Error string}`,
    `OrphanEntry{ComposeProject, Note}`.
  - **Package seams** (mirror teardown.go): `cleanTeardownFn = func(ctx, manifestPath, m,
    warn){ return Teardown(ctx, m, NewTeardownDeps(manifestPath, nil), warn) }` and
    `listComposeProjectsFn`; tests override both.
- **CLI `dwe test clean [scenario...]`** (`internal/cli/test/clean.go`): `--dry-run` flag;
  `cobra.ArbitraryArgs`; `envtest.ScrubComposeEnv()` FIRST (clean runs `compose down` via
  Teardown); `cleanFn = envtest.Clean` seam var (mirror `newRunner`); build `CleanRequest`
  from flags/args; render via `cmdctx.WriteData` — JSON
  `{dry_run, swept:[…], skipped:[…], failed:[…], orphans:[…]}`, text = summary line
  (`swept N, skipped M (live), P failed, K orphan(s)`; dry-run → "would sweep"). Exit code:
  **any `Failed` entry → 1** (a teardown couldn't complete), via the same
  write-the-payload-then-return-a-no-text-sentinel pattern `run` uses (`testRunOutcomeError`,
  run.go:114) so the structured result is still emitted; `Skipped`(live) / `Orphans` are NOT
  errors (exit 0). A hard `Clean` error (unreadable manifests dir) surfaces as the returned
  `cmdctx.Err` (fang maps → 1/2).
- **`--keep` hint** (`renderScenarioLine`, run.go ~315): change the tail from
  "clean up manually when done" to point at `dwe test clean` (e.g. "— run `dwe test clean
  <scenario>` to remove").
- **i18n**: no new `ui.*` keys — plain CLI text (same finding as 1b Task 8).

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs in this repo.
- **Post-Completion** (no checkboxes): manual smoke on a real project, Vikunja update,
  stage-3 plan.

## Implementation Steps

### Task 1: Report artifact collection core (`envtest`)

**Files:**
- Create: `internal/core/workflow/envtest/report.go`
- Create: `internal/core/workflow/envtest/report_test.go`

- [x] define `ReportDeps{PS, Logs func(context.Context, *Manifest) (string, error)}` and
      `NewReportDeps() ReportDeps` (wires `reportPSReal`/`reportLogsReal`)
- [x] implement `CollectReport(ctx, m *Manifest, deps ReportDeps, warn func(string))
      (reportDir string, err error)`: `os.RemoveAll(m.ReportDir)` + `os.MkdirAll`; copy
      `filepath.Join(m.CopyPath, ".dwe/logs/test.log")` → `pipeline.log` (missing source =
      warn+skip); write `compose-ps.txt` from `deps.PS`; write `container-logs.txt` from
      `deps.Logs`; every step best-effort (warn, continue); return `m.ReportDir`
- [x] implement `reportPSReal`/`reportLogsReal` with compose→identity-label graceful
      degradation keyed on `loadCopyConfig(m.CopyPath)`; use `Compose.BuildInternalArgs`
      (**NOT `BuildArgs`**) for the compose `ps`/`logs` captures — **`ps` with `--all`** (the
      running-only default would drop a crashed service) — so no user `args.logs` follow flag
      / global ANSI is injected (see Technical Details); add the generic
      `captureCmdFn(ctx, bin string, args, env []string, dir string) ([]byte, error)` seam
      (default `exec.CommandContext(...).Output()`); reuse `listContainersFn` for the
      identity fallback; const `reportLogTailLines = 200`; `==== <id> ====` headers in the
      fallback logs
- [x] add a small `copyFile(src, dst string) error` fs helper (perms preserved)
- [x] write tests (stubbed `ReportDeps`): report dir created + a pre-existing dir cleared
      (overwrite); all three artifacts written; missing `test.log` → skipped, others still
      written; `PS`/`Logs` returning `(partial, err)` → file still written + warn, other
      artifacts proceed
- [x] write tests for `reportPSReal`/`reportLogsReal` via `captureCmdFn`/`listContainersFn`
      stubs against throwaway copy dirs: valid copy config → **exact** `BuildInternalArgs`
      shape asserted (`compose … ps --all`; `compose … logs --no-color --tail 200`) with NO
      global/policy args (assert `--all` present on `ps`); a copy whose `docker.yml` sets
      `args.logs: ["-f"]` → assert the captured logs args still do NOT contain `-f`
      (regression pin for the follow-hang bug);
      missing/broken copy config → identity-label fallback args asserted
      (`ps -a --filter label=…`; per-id `logs --tail 200` with `==== <id> ====` headers)
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 2

### Task 2: Wire `collectReport` into the runner + populate `ReportDir`

**Files:**
- Modify: `internal/core/workflow/envtest/runner.go`
- Modify: `internal/core/workflow/envtest/runner_test.go`

- [x] add `collectReport func(ctx context.Context, m *Manifest, warn func(string))
      (string, error)` to `Runner`; default in `NewRunner` calls `CollectReport(ctx, m,
      NewReportDeps(), warn)`; add `reportTimeout` const
- [x] in `finish`, after the status is finalised and before `teardown()`, collect the
      report when `r.collectReport != nil && !req.Keep && status != StatusPassed`, under a
      **fresh** `context.WithTimeout(context.Background(), reportTimeout)`; on success set
      `result.ReportDir`; on error `warn` only (best-effort — never change the outcome)
- [x] the `r.collectReport != nil` guard is required because `runner_test.go` has NO shared
      constructor — it builds `&Runner{...}` inline literals, ~6 of which reach `finish` on a
      non-passed status and would panic on a nil seam; production `NewRunner` always sets it.
      Tests that assert collection set a recording stub on their own literal; the rest leave
      it nil (guard skips)
- [x] write tests (stubbed seams): deploy-fail / step-fail / validate-error / timeout →
      `collectReport` called once and `result.ReportDir` set; passing scenario →
      `collectReport` NOT called and `ReportDir` empty; `--keep` failing scenario →
      `collectReport` NOT called; `collectReport` returning an error → scenario status/
      result unchanged, `ReportDir` empty, warn emitted; report collected **before**
      teardown (assert ordering via the recording stubs)
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 3

### Task 3: `envtest.Clean` core (list, flock-guard, teardown reuse, dry-run, orphan scan)

**Files:**
- Create: `internal/core/workflow/envtest/clean.go`
- Create: `internal/core/workflow/envtest/clean_test.go`
- Modify: `internal/core/workflow/envtest/run.go` (extract shared `projectBaseName(cfg)`
  used by `ComposeProjectName`; add `ListManifests(baseDir)`)

- [x] extract `projectBaseName(cfg) string` (prefix-or-name) + the shared normalisation
      out of `ComposeProjectName` so the orphan prefix cannot drift from the real
      compose-name derivation; add `ListManifests(baseDir string) ([]string, error)`
      (all `<scenario>-<runid>.yml`, sorted; absent dir → nil,nil)
- [x] define `CleanRequest`, `CleanResult`, `CleanEntry`, `SkippedEntry`, `FailedEntry`,
      `OrphanEntry` and the package seams `cleanTeardownFn` (default → `Teardown(ctx, m,
      NewTeardownDeps(manifestPath, nil), warn)`) and `listComposeProjectsFn`
      (default → `docker ps -a --format '{{.Label "com.docker.compose.project"}}'`, deduped)
- [x] implement `Clean(ctx, CleanRequest) (*CleanResult, error)`: enumerate manifests
      (**unreadable** dir → hard error; absent → empty); build `known` from the **full**
      manifest set; filter by `Scenarios`; per manifest flock-guard via `errors.As` for
      `*lock.HeldError` → skipped-live, any other lock error → skipped + warn; dry-run →
      would-sweep (flock still taken to classify live); else `cleanTeardownFn` under a fresh
      ctx → **nil error ⇒ `Swept`, non-nil error ⇒ `Failed{…, Error}` + warn** (a partial
      teardown must NOT be reported swept); load origCfg — on failure `warn` + **skip only**
      the orphan scan (still sweep); report-only, best-effort orphan scan (prefix
      `normalize(projectBaseName(origCfg)+"-t-")` minus `known`; a `listComposeProjectsFn`
      error → warn + empty orphans, never abort); assemble `CleanResult`
- [x] write tests (temp dirs, recording `cleanTeardownFn`, scripted `listComposeProjectsFn`,
      real `lock.Acquire`): two free manifests → both swept in order; a manifest whose
      scenario flock is held → skipped-live, teardown NOT called; `--dry-run` → would-sweep,
      teardown NOT called, flock released afterward; name filter → only matching processed,
      filtered-out manifest still excludes its project from orphans; orphan math (prefix
      match minus manifested minus unrelated); absent manifests dir → empty result;
      **origCfg-load failure → manifests still swept, orphans empty (NO hard error)**;
      `listComposeProjectsFn` error → orphans empty, sweep result intact;
      **`cleanTeardownFn` returning an error → entry in `Failed` (NOT `Swept`), warn emitted**
- [x] run `go test ./internal/core/workflow/envtest/...` — must pass before task 4

### Task 4: CLI `dwe test clean` (command, JSON, registration, `--keep` hint, policy pin)

**Files:**
- Create: `internal/cli/test/clean.go`
- Create: `internal/cli/test/clean_test.go`
- Modify: `internal/cli/test/test.go` (register `clean`)
- Modify: `internal/cli/test/run.go` (`--keep` hint → `dwe test clean`)
- Modify: `internal/cli/bridgepolicy_test.go` (assert `test clean` container-blocked)

- [x] implement `newTestCleanCmd(flags)` → `dwe test clean [scenario...]` with `--dry-run`;
      `envtest.ScrubComposeEnv()` FIRST; `cleanFn = envtest.Clean` seam var; build
      `CleanRequest`; render via `cmdctx.WriteData` (JSON `{dry_run, swept, skipped, failed,
      orphans}`; text summary, dry-run wording); **exit 1 when the result has any `Failed`
      entry** (write payload first, then return a no-text sentinel error like
      `testRunOutcomeError`); register under `dwe test` in `test.go`
- [x] update `renderScenarioLine` `--keep` hint to point at `dwe test clean <scenario>`
- [x] render a `report: <dir>` line in `renderScenarioLine` when `o.ReportDir != ""` (a
      failed scenario), so the report path is discoverable in the default text path — the
      `run` JSON already carries `report_dir` via `omitempty`; without this the Overview's
      "JSON **and** text" claim is unmet
- [x] extend `bridgepolicy_test.go` to assert the `dwe test clean` subcommand stays blocked
      in container context (top-level `test` policy already covers it — pin it explicitly)
- [x] write tests over the stubbed `cleanFn`: `ScrubComposeEnv` called before `cleanFn`;
      `--dry-run` and scenario args threaded into `CleanRequest`; JSON shape (incl. `failed`);
      text summary golden (swept/skipped/failed/orphans + dry-run); a `CleanResult` with a
      `Failed` entry → **exit 1** (payload still emitted); a hard `cleanFn` error → non-zero
      exit; plus a `run`-side test that a failed scenario with `ReportDir` set renders the
      `report:` line (and a passing one does not)
- [x] run `go test ./internal/cli/... ./internal/core/workflow/envtest/...` — must pass
      before task 5

### Task 5: User-facing docs + ru i18n

**Files:**
- Modify: `docs/reference/config/tests.md`
- Modify: `docs/guides/integration-tests.md`
- Modify: `docs/i18n/ru/reference/config/tests.md`
- Modify: `docs/i18n/ru/guides/integration-tests.md`

- [ ] add a "Failure reports" section to `tests.md` (collected only on failure, before
      teardown; `.dwe/tests/reports/<scenario>/` layout — `pipeline.log`, `compose-ps.txt`,
      `container-logs.txt`; per-scenario overwrite; skipped under `--keep`) and a
      "`dwe test clean`" section (manifest-driven, `[scenario...]` filter, `--dry-run`,
      report-only orphan scan, flock-guarded live runs, `--output json`)
- [ ] extend the guide's debugging section: `--keep` for live inspection then
      `dwe test clean`, reading a failure report from CI artifacts
- [ ] mirror both edits in the ru tree and refresh each file's `Translated from`
      provenance hash (`TestRussianTranslationsAreFresh`)
- [ ] run `make build` (re-embeds docs, regenerates content hashes) and
      `go test ./internal/core/docs/...` — must pass before task 6

### Task 6: Verify acceptance criteria

- [ ] verify spec §6 step 5 + §7 row 2: reports collected on failure before teardown into
      `.dwe/tests/reports/<scenario>/` (pipeline log + `compose ps --all` + container log
      tails — `--all` so a crashed service is included); `report_dir` now populated in `run`
      JSON/text; `dwe test clean` is manifest-driven, reuses `Teardown`, flock-guards live
      runs, orphan scan is report-only (never destroys by name pattern), a failed teardown is
      reported as `Failed` (never `Swept`) and drives exit 1, `--dry-run` + name filter +
      `--output json` all work
- [ ] verify backward compatibility: `Teardown` behaviour unchanged, no existing golden
      altered except the `--keep` hint text
- [ ] run full suite: `make test`
- [ ] run `make lint`

### Task 7: [Final] Internals documentation + plan close-out

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md`

- [ ] extend the `envtest` section of `packages.md` (report collection: fresh-ctx,
      failure-only, before-teardown, overwrite, `ReportDeps`/`captureCmdFn` seams,
      compose→identity degradation; `Clean`: `ListManifests`, per-scenario flock-guard,
      `Teardown` reuse, report-only orphan scan via `<base>-t-` prefix minus `known`) and
      the `internal/cli/test/` section (`clean` command, scrub-first, `cleanFn` seam)
- [ ] extend the AGENTS.md integration-tests bullet with the stage-2 contracts worth
      trapping: reports failure-only + before-teardown + fresh-ctx; `clean` manifest-driven
      + flock-guard-live + orphan-scan-report-only (never destroy by name pattern)
- [ ] run `make build` (embeds updated internals docs); docs-subsystem tests green
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*No checkboxes — external follow-ups.*

- **Manual smoke** (needs a real project + Docker, interactive): force a scenario failure
  (e.g. a failing `http_check`), confirm `.dwe/tests/reports/<scenario>/` holds the three
  artifacts and teardown still ran; run `dwe test run --keep smoke` then
  `dwe test clean --dry-run` / `dwe test clean smoke` and confirm the kept environment is
  swept and orphans (if any) are only reported.
- **Vikunja task 170**: comment when stage 2 lands.
- **Stage 3 plan**: per-step timeouts, parallel scenario execution, `workspace/tests/`
  validator domain, compose isolation preflight — write via `/planning:make` after stage 2.

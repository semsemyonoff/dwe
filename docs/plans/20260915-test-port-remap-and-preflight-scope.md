# `dwe test` auto-remaps var-routed host ports; `ports_free` narrows to `--service`

## Overview

Two independent user-visible fixes, one branch (`feat/0.6.2-test-ports-preflight`,
cut from `release/0.6.2`, which is itself cut from `main` @ `ebf93a3c`, tag
`v0.6.1`), two commits, one PR into `release/0.6.2`. Smallest first.

**Change A — `dwe test` does not remap host ports that reach compose through
`vars:` + `exports.env`.** A compose port such as `"${VALKEY_PORT:-6379}:6379"`,
where `VALKEY_PORT` is an `exports.env` rule `from: vars.ports.valkey`, binds the
live stack's port inside the test copy. 0.6.1 added a non-blocking
`interpolated_host_port` finding whose message tells the author to write
`env.vars: { ports.valkey: auto }` by hand. The scanner already traces the
variable to its `vars:` path (`IsolationFinding.VarPath`); nothing acts on it.
After this change the runner allocates a free port for every traced path in the
same batch as everything else, with no scenario config. `env.vars: auto` stays
as the explicit form; an explicit number pins the port and disables its remap.

**Change B — preflight `ports_free` checks ports the run cannot bind.**
`dwe deploy run --service backend` resolves ONLY `render-env` plus `backend`'s
own `deploy.yml` phases (`ResolveServicePlan` / `ResolveServicesPlanSubset`,
`internal/core/workflow/deploy/service_plan.go:20-177`); the built-in
`start/up` phase is not part of a per-service run. Yet `ports_free` walks every
enabled service, so a foreign compose project holding another service's port
blocks a run that never touches that port. `ports_free` now checks only the
requested services plus the transitive closure of their `service.yml`
`depends_on` (what `compose up <svc>` actually brings up — `dwe docker up` does
not pass `--no-deps`, `internal/cli/docker/docker.go:112`). The same narrowing
applies to the deploy that `dwe services enable|disable --apply` performs
(`internal/cli/service/service_plan.go:432` drives `deploy.RunHelper` with
`Opts.Services`). `reset run --service` is unaffected: it runs preflight with
stage `stop`, where `ports_free` already self-skips
(`internal/core/validate/env/ports.go:285`).

Both are user-observable → each commit carries its own `CHANGELOG.md`
`## [Unreleased]` entry (currently "Nothing yet.") and its own EN + RU docs.
Change A also gets an `## Upgrading to 0.6.2` section: a scenario step that
hardcoded the original host port must now read it from the variable.

### Non-goals

Decided during design; do not re-litigate during implementation:

- No new scenario schema. `env.vars: { <path>: auto }` and `env.vars: { <path>: 6380 }`
  keep their meaning; there is no opt-out flag for the automatic remap.
- No new isolation-finding kind. The scanner stays a pure fact reporter and keeps
  emitting the traced-var finding; every consumer treats it as covered through the
  single `CoversInterpolatedHostPort` predicate.
- No exported env-var → vars-path index out of `package config`. The runner reads
  `VarPath` off the findings it already has.
- An untraced variable (no active `exports.env` rule, a rule whose `when:` is
  falsy, a value from `default:`, or `from:` that is neither `vars.*` nor a
  declared service port) is NOT remapped and keeps its warning. Compose then falls
  back to the literal default (`:-6379`) exactly as today.
- `ports_free` inside the test copy still does not see var-routed ports; the copy
  relies on the deploy-time bind failure + one retry, as today.
- The scenario view scans the developer's working tree at the project root, not
  the copy. The copy holds only what `git ls-files -co --exclude-standard` lists
  minus `.dwe/`, `.env` and `.git` (`envtest/copy.go:19-23, 63-77`), so three
  things can differ: a **gitignored compose file** referenced from a tracked
  `service.yml` (the copy's own `compose` invocation fails on the missing `-f`
  anyway), the `.dwe/compose.bridge.yml` overlay (`workspace.go:733`, `:750-757`;
  benign today — `RenderOverlay` emits only `volumes`/`environment`/`extra_hosts`,
  `internal/core/bridge/composegen.go:319-364`), and a gitignored
  `docker.local.yml` whose `shared: true` marks are seen by the original's scan
  but not by the copy. The divergence runs both ways: a blocking finding
  (`container_name:`, literal port) in such a gitignored file now blocks the
  scenario pre-copy where the copy's scan never saw it (harmless: the copy's
  compose would fail on the missing `-f`), and a `shared: true` mark from a
  gitignored `docker.local.yml` can silence a non-blocking volume warning the
  copy would have raised. Accepted and documented in `packages.md`; no code
  compensates for it.
- The interactive deploy menu's pre-wizard gate (`internal/cli/deploy/menu.go:716`
  `runPreWizardPreflight`) needs no change: it filters `ports_free` out of its
  registry entirely (`menu.go:740-744`), so a scope would be inert there.
- The `ports_free` scope does not follow compose-file `depends_on`; it follows
  `service.yml` `depends_on` only. A dependency declared only in raw compose
  surfaces as a compose bind error inside the step, as today.
- `WaitPortsReleased` (`ports.go:180`) and the setup wizard's
  `collectPortConflictsFn` seam (`cli/deploy/menu.go:41`) keep the unscoped
  probe: neither runs in a per-service context.

## Context (from discovery)

Verified on `main` @ `ebf93a3c`; line references re-checked by plan review.

**Change A**

- `internal/core/project/config/compose_scan.go` — `ScanComposeIsolation(cfg, root)`
  (:123) emits `KindInterpolatedHostPort` for a whole-token variable in `ports:`
  with `EnvVar`, `VarPath` (active `exports.env` rule `from: vars.<path>`,
  `when:` evaluated on `cfg.Raw` by `newComposeExports`, :936) and
  `SourceService`. Message branches in `interpolatedFinding` (:971-1009): varPath
  (:985-987, "dwe test does not remap it … add `env.vars: { … : auto }`"),
  sourceService (:988-991), untraced (:992-995, advises `env.vars: auto`).
  `Blocking: false`. The display path is already relativised against the root
  the scanner was given (:976-978); `IsolationFinding.File` stays absolute.
- `internal/core/workflow/envtest/runner.go` — `RunScenario` doc contract
  (:294-301: "Every failure from CopyTree onward is instead reported via the
  returned ScenarioResult"); order today: `warn := req.Warn` (:317-320),
  `fail` (:328-330, returns a bare error), `LoadScenario` (:342), `origCfg`
  (:360), `CopyTree` (:374), `prepFail` (:381), `LoadSeedLocalYAML` (:388),
  `writeCopyLocalYAML` (:393, allocates), manifest (:409), `finish` (:439-464,
  runs `collectReport` for a non-passed status), reporter/pipeline log (:470),
  `scanComposeIsolationGate` (:478, loads the COPY's `workspace.yml` and scans;
  writes only through `warn`, :562/:575, never through the reporter).
  `writeCopyLocalYAML` (:645-671): one `AllocatePorts` batch of
  `len(enabledHostPortKeys)+len(scn.AutoPortVarPaths())`. `hasAllocatedPorts`
  (:587), `retryDeployWithFreshPorts` (:678). Seams on `Runner` (:244):
  `execDwe execDweFunc`, `allocatePorts`.
- `internal/cli/test/run.go` — a bare error from `RunScenario` becomes
  `StatusError` (:191-195) and exit code 2 (`finishTestRun`, :397-401); a
  `StatusFailed` result exits 1; `warn` is a no-op under `--output json`
  (:151-157); `renderTestRunText` prints the `kept: compose project …` line
  whenever `ComposeProject != ""` (:544-546).
- `internal/core/workflow/envtest/hostports.go` — `enabledHostPortKeys` (:48),
  `RemappedHostPortServices` (:94), `CoversInterpolatedHostPort(cfg, scn, f)`
  (:106): true for a remapped `SourceService` or a `VarPath` in
  `scn.AutoPortVarPaths()`.
- `internal/core/workflow/envtest/localyaml.go` — `scenarioEnvOverlay(scn, ports)`
  (:121) iterates `scn.Env.Vars` only; `localDeepMerge` lives here; the seed
  loader strips `compose.extra` from the copy's seeded `local.yml` (:108-111).
- `internal/core/workflow/envtest/scenario.go` — `AutoPortSentinel` (:29),
  `ScenarioEnv` (:53), `AutoPortVarPaths()` (:96).
- `internal/core/validate/tests/tests.go` — `autoPortPlaceholder = 1` (:32),
  isolation loop (:102-131), `newScenarioView` (:151, clones `Raw`, calls
  `applyServiceToggles`), `uncoveredScenarios` (:182, compares
  `IsolationFinding.File` against the scenario's compose set — the only reader
  of `File`), `renderConfigFor(cfg, env envtest.ScenarioEnv)` (:345-369: `auto`
  → placeholder, vars overlay merged into `Raw["vars"]`, then
  `applyServiceToggles`), `applyServiceToggles` (:385), `setDotPath` (:434),
  `mergeMaps`. Two callers for each helper: `newScenarioView` and
  `renderConfigFor`.
- `internal/cli/test/profile.go` — cost profile (:183-204) builds a view from
  `scenarioServices` + `scenarioRaw` (:257) — a third copy of the same view —
  feeds it to `ScanComposeCost` (:191) and `ScanComposeIsolation` (:199), and
  filters with `CoversInterpolatedHostPort`.
- `config.DweConfig` — `Compose ComposeConfig` is a value field
  (`workspace.go:119`) with `Extra []string` (`:618`, `yaml:"-"`);
  `Services map[string]ServiceConfig` (`:169`) holds values (`ServiceConfig`
  at `:1149`) with per-service `LocalComposeExtra` (`:1171`). `ComposeFiles()`
  (`:680`) includes both (`:719-720`). A shallow copy plus `maps.Clone` on
  `Services`/`Raw` and per-entry reassignment cannot leak into the original —
  but a `Raw["services"].<n>` entry map that is NOT rebuilt is shared with
  the original, which is why the view must clone every entry it strips.
- Docs: `docs/reference/config/tests.md` (:110-112 `auto`, :180-182 isolation
  model — :182 is the paragraph to rewrite, :425-440 scanner table,
  **:472-495 `### Interpolated host ports`** — coverage table, intro paragraph
  and the three per-caller bullets all change, :531 limitations),
  `docs/guides/integration-tests.md` (:5-11, :178),
  `docs/internals/packages.md` bullets for `compose_scan.go` (:83), `envtest/`
  (:109), `validate/tests/` (:171); RU mirrors under `docs/i18n/ru/…`;
  `skills/dwe/SKILL.md:~220-223` and `skills/dwe/references/integration-tests.md`
  (:23, :48, :125-137) still say the remap covers ONLY `services.<name>.ports`.

**Change B**

- `internal/core/execution/preflight/preflight.go` — `Run(ctx, cfg, cmdRegistry,
  baseDir, stage, skip, errOut)` (:104), `RunFn` is a type ALIAS (:92,
  `type RunFn = func(...)`), builds `validate.Context` (:118-127).
- Seams: `internal/core/workflow/lifecycle/run.go:45 PreflightFunc`,
  `internal/cli/lifecycle/lifecycle.go:15 preflightRun`,
  `internal/cli/deploy/deploy.go:379 Opts.PreflightFn`. Because `RunFn` is an
  alias, a variadic on `Run` propagates to all three and every call site
  (`workflow/lifecycle/run.go:202`, `stop.go:103`, `cli/lifecycle/reset.go:198,341`,
  `cli/lifecycle/stop.go:59`, `cli/deploy/deploy.go:521`) keeps compiling.
  Exactly nine explicit func literals break: `workflow/lifecycle/helpers_test.go:24`;
  `workflow/lifecycle/preflight_test.go:39, 64, 91, 117`;
  `cli/lifecycle/testhelpers_test.go:24`; `cli/lifecycle/reset_test.go:432`;
  `cli/deploy/bridge_test.go:108` (`noopPreflight`, also reused by
  `vars_hash_e2e_test.go:37` — no extra edit there) and `:186`.
- `internal/cli/deploy/deploy.go` — `runPreflight` assignment (:519), preflight
  call (:521) with stage `deploy`; `Opts.Services []string` (:363); "service
  not found" check (:597-607) runs AFTER preflight; plan resolution switch
  (:630-642).
- `internal/core/validate/validate.go` — `Context` struct (:29).
- `internal/core/validate/env/ports.go` — seams `dockerPSOutFn` (:72),
  `portListenFn` (:77); `CollectPortConflicts(ctx, cfg, baseDir)` (:94) is the
  canonical probe: it calls `collectDeclaredPorts(cfg)` itself (:98) and emits
  the `[]PortConflict` (:126-135) with a shared EADDRINUSE retry budget
  (:124-127, ~1.5 s per busy port); `WaitPortsReleased` (:184) also enumerates
  declared ports; `portsFreeValidator.Run` (:283) uses `collectDeclaredPorts`
  ONLY as an early-return guard (:289) and takes its diagnostics from
  `CollectPortConflicts` (:306); `collectDeclaredPorts(cfg)` (:336) walks all
  enabled `cfg.Services` and skips `!Enabled` (:342).
- `config.ServiceConfig.DependsOn []string` (`workspace.go:1164`); a `depends_on`
  target cannot be a tool service (`validateDependsOnTypes`, :3468).
- Docs: `docs/reference/config/validate.md:83-85` (`env.ports_free` paragraph),
  `docs/guides/troubleshooting.md:31`, RU mirrors (`validate.md:85-87`,
  `troubleshooting.md:33`), `packages.md` bullets for `preflight/` (:103),
  `validate/env/` (:164) and `internal/cli/lifecycle/` (:70) / deploy.

**Conventions**

- Every envtest runner test stubs `execDwe` (or the test binary re-execs itself)
  and respects `ScrubComposeEnv()` ordering.
- RU translations carry `> Translated from: <path> @ <hash>`; `hash` is the first
  12 hex chars of the EN file's SHA256 as regenerated into
  `internal/core/docs/content_hashes_gen.go` by `make build`.
  `TestRussianTranslationsAreFresh` fails on a stale header.
- `AGENTS.md` critical patterns that apply: preflight runs before any side effect
  including the locks; diagnostics through `internal/shared/trace`; JSON output
  mode never gets a `warning:` line on stdout.
- No import cycle for the new envtest export: `validate/tests` (`tests.go:23`)
  and `cli/test` already import `envtest`; `envtest` imports the `validate`
  root only.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change (`make embedded-docs` once, then focused
  `go test ./internal/...`; `make test` before each commit)
- maintain backward compatibility: no scenario file, `service.yml` or
  `workspace.yml` needs to change
- follow the `AGENTS.md` critical patterns listed under Conventions above

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above);
  table-driven where the input space is a list of shapes (view construction,
  port-plan union rules, scope closure, scanner message branches, probe
  diagnostics)
- **no e2e UI tests** in this project; live verification on real projects is in
  Post-Completion
- Docker-touching code is tested through seams (`Runner.execDwe`,
  `Runner.allocatePorts`, `Opts.PreflightFn`, `dockerPSOutFn`, `portListenFn`),
  never against a real daemon

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**Change A — one scan, before allocation, on a scenario view of the original.**

1. `envtest.ScenarioView(cfg, env)` becomes the single definition of "the
   config the scenario's copy will run": a shallow copy of `cfg` with cloned
   `Services` and `Raw`; `env.services.enable` sets `Enabled = true`,
   `env.services.disable` sets `Enabled = svc.Required` (mirrors the loader:
   `Enabled = required || enabled`); the effective state is written into
   `Raw["services"].<name>.enabled` and `env.vars` is merged into `Raw["vars"]`
   with `auto` replaced by a placeholder int, so `exports.env` `when:`
   predicates and template `when:` see the copy's state; the per-developer
   compose overlays are removed in BOTH representations — the typed
   `Compose.Extra` / `LocalComposeExtra` (so `ComposeFiles()` equals the copy's
   chain) and the `Raw` keys `compose.extra` / `services.<n>.compose.extra`
   (`LoadConfig` keeps the merged local layer in `Raw`, `workspace.go:1904-1936`,
   and `exports.env` `when:` reads `Raw`, `compose_scan.go:925-940`), with the
   same semantics as the seed loader's `stripComposeExtra` (`localyaml.go:83`).
   Every `Raw` subtree the view modifies is copied recursively, never merged in
   place, so two views of one config cannot contaminate each other or the
   original. The three hand-rolled views (`validate/tests` `newScenarioView` +
   `renderConfigFor`, `cli/test/profile`) are rewritten on top of it and their
   private helpers deleted. Clearing the overlays is a semantic no-op for the
   render pass and an observable change for the other two consumers (see 7).
2. The runner builds a `portPlan` once, right after `origCfg` loads and BEFORE
   `CopyTree`: `keys = enabledHostPortKeys(origCfg, scn)`;
   `findings = config.ScanComposeIsolation(ScenarioView(origCfg, scn.Env), req.BaseDir)`;
   `autoPaths` = sorted, de-duplicated union of `scn.AutoPortVarPaths()` and the
   `VarPath` of every `KindInterpolatedHostPort` finding, minus any path the
   scenario **effectively pins** (a non-empty scalar other than `auto` resolved
   at that dot-path in the normalised `env.vars` overlay — see Technical
   Details; `null`, `""` and a missing key do not pin, because the export would
   fall back to its default and bind the original port,
   `internal/shared/envfile/render.go:98-108`). Scanning at
   the project root is safe on the `File` axis: no consumer of the runner's
   findings reads `IsolationFinding.File` (the only reader, `uncoveredScenarios`,
   already scans at the original root); message paths are relativised by the
   scanner either way. The working-tree-vs-copy divergence is a documented
   non-goal.
3. `scanComposeIsolationGate` stops loading the copy's config: it takes
   `plan.findings`, filters `Shared` and `CoversInterpolatedHostPort`, warns and
   blocks — but it now runs BEFORE `CopyTree`. A blocked scenario returns
   `&ScenarioResult{Name: req.Scenario, Status: StatusFailed, Duration: …}` with
   a nil error and EMPTY `ComposeProject`/`CopyPath` (so `--keep` cannot print a
   "kept: compose project …" line for a copy that was never created, and the
   exit code stays 1, not the bare-error 2). Consequences: no copy directory is
   created for a blocked scenario, no failure report directory is collected for
   it (there is nothing to collect), a stale copy left by an earlier aborted run
   is not wiped by `CopyTree` before the gate, and under `--output json` the
   only signal is `status` (warnings are suppressed there today already).
   `RunScenario`'s doc contract is rewritten to match.
4. `writeCopyLocalYAML`, `retryDeployWithFreshPorts` and `hasAllocatedPorts` take
   the `portPlan`; the single `AllocatePorts` batch stays
   `len(keys)+len(autoPaths)`; the retry re-allocates the whole plan.
   `scenarioEnvOverlay` writes every allocated var path into `vars`, not only
   the ones present in `scn.Env.Vars`.
5. `CoversInterpolatedHostPort` returns true for ANY `f.VarPath != ""`: the
   runner remaps it, or the scenario pinned it explicitly (the author's decision
   either way). That single change silences the validator's `tests.isolation`
   VarPath branch (every scenario covers it, `uncoveredScenarios` is empty) and
   drops traced findings from `cost_profile.isolation_findings`.
6. Scanner messages: the varPath branch becomes a statement of fact with no
   advice; the untraced branch stops advising `env.vars: auto` and instead
   advises routing the port through a declared service port or an `exports.env`
   rule `from: vars.<path>` (then remapped automatically). The sourceService
   branch is unchanged.
7. Observable side effects of the unified view, all correct and all
   changelogged: `cost_profile.build_services` / `external_images` no longer
   count per-developer `compose.extra` overlay files, and `dwe validate`'s
   `tests.isolation` stays silent for an INTERPOLATED-port finding that exists
   only in such an overlay (the copy never runs it). Other finding kinds in
   `validate/tests` still come from the project-wide scan of `ctx.Cfg`
   (`tests.go:102`) and keep warning regardless of overlays.

**Change B — scope travels as an option, preflight stays a dumb filter.**

1. `validate.Context` gains `Services []string`: nil means the whole project;
   non-empty means the lifecycle command acts only on these services. Only
   `ports_free` reads it; `dwe validate` never sets it.
2. `preflight` gains a functional option `WithServices(names []string)` and
   `Run(..., opts ...Option)`; `RunFn` (alias) follows; the nine explicit test
   stubs listed under Context are updated.
3. The scope reaches the probe that emits diagnostics, not just the validator's
   guard: `collectDeclaredPorts(cfg, scope)` gains a scope set (nil = all), and
   `CollectPortConflictsScoped(ctx, cfg, baseDir, scope []string)` becomes the
   implementation, with `CollectPortConflicts` = `…Scoped(…, nil)` so the wizard
   seam and `WaitPortsReleased` are untouched. `portsFreeValidator.Run` uses the
   scoped pair. Filtering at enumeration (not on the returned slice) keeps
   out-of-scope busy ports from burning the shared EADDRINUSE retry budget.
4. The deploy CLI computes the scope — requested services plus the transitive
   `depends_on` closure over enabled services, cycle-safe — and passes the ready
   list. Requested names are included as given; a disabled requested service
   contributes nothing because `collectDeclaredPorts` already skips `!Enabled`.
   The "service not found" check moves ABOVE the preflight call so a typo in
   `--service` fails fast instead of silently emptying the scope.

## Technical Details

**`envtest.ScenarioView`** (new file `internal/core/workflow/envtest/scenarioview.go`)

```go
// AutoPortPlaceholder stands in for AutoPortSentinel wherever a scenario's
// env.vars are overlaid onto a config that is inspected rather than run
// (validate-time rendering, the compose scan): the concrete number is
// arbitrary, it only has to satisfy int/port-range checks.
const AutoPortPlaceholder = 1

// ScenarioView returns the config the scenario's copy will run: cfg with the
// scenario's service toggles applied to Services and Raw, its env.vars merged
// into Raw["vars"] (auto → AutoPortPlaceholder), and the per-developer compose
// overlays removed from both the typed fields (Compose.Extra,
// LocalComposeExtra) and Raw (compose.extra, services.<n>.compose.extra), as
// the copy's seeded local.yml removes them. cfg is never mutated and two views
// never share state: Services is cloned, Compose is a value field, and every
// Raw subtree the view touches (services, vars, compose) is rebuilt with fresh
// maps rather than merged in place.
func ScenarioView(cfg *config.DweConfig, env ScenarioEnv) *config.DweConfig
```

- `view := *cfg`; `view.Services = maps.Clone(cfg.Services)`;
  `view.Raw = maps.Clone(cfg.Raw)`, and if `cfg.Raw` is nil start from an
  explicit `map[string]any{}` (the current `newScenarioView` does this at
  `tests.go:159-163`; `maps.Clone(nil)` is nil, not empty).
  `view.Compose.Extra = nil`; for every service: `svc.LocalComposeExtra = nil`,
  reassign.
- Raw overlay stripping: delete `Raw["compose"]["extra"]` and
  `Raw["services"][<n>]["compose"]["extra"]` on rebuilt copies of those maps
  (never on the originals), matching `stripComposeExtra` in `localyaml.go:83`
  minus its warning.
- Toggle rule per name in `Enable`/`Disable`, unknown names ignored;
  `Raw["services"]` rebuilt as a fresh map in which EVERY entry is cloned (not
  only toggled ones — the existing `applyServiceToggles` clones toggled entries
  only and early-returns with no clone when there are no toggles; that is not
  enough once the overlay strip touches every service), `enabled` set for
  toggled names only.
- Vars overlay: sorted paths, `AutoPortSentinel` → `AutoPortPlaceholder`;
  build the overlay with envtest's error-returning `setDotPath`
  (`localyaml.go:166`) and IGNORE its error in the view (documented: the view
  is inspection-only, and the runner's `BuildLocalOverlay` rejects the same
  malformed path with an error before anything runs; validate's render pass
  reports the unresolved reference itself, as its dropped helper did). Merge
  with a recursive copying merge — move `validate/tests`' `mergeMaps`
  (`tests.go:459-472`, allocates new maps at every level) into envtest; do NOT
  use `localDeepMerge`, which mutates destination children in place
  (`localyaml.go:192-208`). `validate/tests`' silent `setDotPath` (:434) is
  deleted.
- `validate/tests`: `renderConfigFor(cfg, env)` → `return ScenarioView(cfg, env)`
  (delete the body, `applyServiceToggles`, `setDotPath`, `mergeMaps` moves,
  `autoPortPlaceholder` → `envtest.AutoPortPlaceholder`);
  `newScenarioView` → `ScenarioView(cfg, scn.Env).ComposeFiles()`.
- `cli/test/profile.go`: `view := envtest.ScenarioView(p.cfg, scn.Env)` feeds
  both `ScanComposeCost` and `ScanComposeIsolation`; delete `scenarioRaw`,
  `scenarioServices` (keep whatever else still needs the enabled map — derive
  it from `view.Services`).

**`portPlan`** (new file `internal/core/workflow/envtest/portplan.go`)

```go
type portPlan struct {
    keys      []hostPortKey            // enabledHostPortKeys(origCfg, scn)
    autoPaths []string                 // sorted, unique
    findings  []config.IsolationFinding
}

func buildPortPlan(origCfg *config.DweConfig, scn *Scenario, baseDir string) portPlan
func (p portPlan) hasAllocatedPorts() bool // len(keys)+len(autoPaths) > 0
```

- `autoPaths` union rule, in order: start from `scn.AutoPortVarPaths()`; for each
  finding with `Kind == KindInterpolatedHostPort && VarPath != ""`, add
  `VarPath` unless `pinnedVarPath(overlay, VarPath)`; sort + `slices.Compact`.
- `pinnedVarPath(overlay map[string]any, path string) bool`, where
  `buildPortPlan` builds the scenario's vars overlay once the way
  `scenarioEnvOverlay` does (dot paths expanded through `setDotPath`, so
  `ports.valkey: 6380` and `ports: {valkey: 6380}` are the same overlay), then
  resolves each `path` in it. Pinned iff the resolved value is a scalar that is
  neither `AutoPortSentinel` nor `nil` nor `""` — `int`, `float64`, `0` and a
  numeric string all pin (they reach compose as text either way). The
  `nil`/`""` rule follows the string-format export fallback
  (`envfile/render.go:98-108`); an `int`/`bool`-format rule keeps a falsy
  resolved value (`render.go:105`), so `0` pins under this rule too, which is
  consistent — say so in the doc comment. A map or
  list at the path is not a pin (the scenario author wrote structure, not a
  port; the implicit allocation then wins and `BuildLocalOverlay`'s existing
  map-wins collision rule applies). A value present only in the SEED
  `local.yml` (the developer's own override) never pins: the copy must not
  bind the developer's port, and the allocated value is merged over the seed
  exactly as an explicit `auto` is today.
- `writeCopyLocalYAML(plan, origCfg, seedLocal, scn, composeProject, copyRoot, warn)`
  pairs `allocated[:len(keys)]` with keys and `allocated[len(keys)+i]` with
  `autoPaths[i]`; `buildHostPortOverrides` unchanged.
- `scenarioEnvOverlay(scn, ports)`: iterate the sorted union of `scn.Env.Vars`
  keys and `ports` keys; a key only in `ports` is written as the allocated int;
  the existing "auto but no port allocated" error stays for keys in `scn.Env.Vars`
  with the sentinel and no port.

**Runner order after the change** (`RunScenario`): lock → `LoadScenario` →
timeout → kept-run check → `origCfg` → `plan := buildPortPlan(origCfg, scn, req.BaseDir)` →
`if scanComposeIsolationGate(plan.findings, origCfg, scn, req.SkipIsolationCheck, warn) { return &ScenarioResult{Name: req.Scenario, Status: StatusFailed, Duration: r.clock().Sub(start)}, nil }`
→ `runID`/names → `CopyTree` → seed → `writeCopyLocalYAML(plan, …)` → manifest →
… → deploy → retry with the same `plan` → steps. `RunScenario` doc comment:
"A non-nil error means … nothing was created. A blocking compose-isolation
finding is reported as StatusFailed with no copy, manifest, compose project or
report directory. Every failure from CopyTree onward …".

**`CoversInterpolatedHostPort`** (`hostports.go:106`)

```go
if f.Kind != config.KindInterpolatedHostPort { return false }
if f.VarPath != "" { return true } // remapped automatically, or pinned by the scenario
return f.SourceService != "" && RemappedHostPortServices(cfg, scn)[f.SourceService]
```

The doc comment must say why a pinned value counts as covered.

**Scanner messages** (`compose_scan.go:985-995`)

- varPath: `head + " (exports.env from: " + from + ") — dwe test remaps it through vars." + varPath`
- untraced: `head + ", which no active exports.env rule traces to a port dwe test remaps — " + collides + "; export it from a declared service port (`from: services.<name>.ports.<port>`) or from `vars.<path>` through an exports.env rule (dwe test then remaps it automatically)"`
- sourceService: unchanged.

**Change B types**

```go
// validate.Context
// Services narrows service-scoped probes to the services a lifecycle command
// acts on (plus what they bring up). Nil or empty means the whole project.
// Set only by preflight (WithServices); dwe validate leaves it nil.
Services []string

// preflight
type Option func(*options)
func WithServices(names []string) Option
func Run(ctx context.Context, cfg *config.DweConfig, cmdRegistry *usercommands.Registry,
    baseDir, stage string, skip bool, errOut io.Writer, opts ...Option) error
type RunFn = func(ctx context.Context, cfg *config.DweConfig, cmdRegistry *usercommands.Registry,
    baseDir, stage string, skip bool, errOut io.Writer, opts ...Option) error

// validate/env
func collectDeclaredPorts(cfg *config.DweConfig, scope map[string]bool) []declaredPort // nil scope = all enabled
func CollectPortConflictsScoped(ctx context.Context, cfg *config.DweConfig, baseDir string, scope []string) ([]PortConflict, error)
func CollectPortConflicts(ctx context.Context, cfg *config.DweConfig, baseDir string) ([]PortConflict, error) // = Scoped(…, nil)
```

- `portsFreeValidator.Run`: `scope := scopeSet(vctx.Services)`; guard uses
  `collectDeclaredPorts(v.cfg, scope)`; diagnostics from
  `CollectPortConflictsScoped(parent, v.cfg, vctx.ProjectRoot, vctx.Services)`.
  `WaitPortsReleased` passes nil.
- deploy CLI helper (`internal/cli/deploy/preflight_scope.go`, unexported):
  `preflightScope(cfg *config.DweConfig, names []string) []string` — BFS over
  `cfg.Services[n].DependsOn`; a dependency is followed only when present in
  `cfg.Services` and `Enabled`; requested names always included as given;
  visited set for cycles; output sorted. Passed as
  `runPreflight(ctx, cfg, reg, workDir, "deploy", opts.SkipPreflight, errOut, preflight.WithServices(scope))`
  only when `len(opts.Services) > 0`.
- The `deploy.go:597-607` existence loop moves above `:519`; `svcDeploys`
  loading stays where it is (it needs `trackedServices`).

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this
  codebase — code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action — live
  verification on real projects, skill copy sync, consuming-project cleanup

## Implementation Steps

### Task 1: `envtest.ScenarioView` replaces the three hand-rolled scenario views

**Files:**
- Create: `internal/core/workflow/envtest/scenarioview.go`
- Create: `internal/core/workflow/envtest/scenarioview_test.go`
- Modify: `internal/core/workflow/envtest/localyaml.go` (if `setDotPath` :166 is the one kept)
- Modify: `internal/core/validate/tests/tests.go` (`autoPortPlaceholder` :32, `newScenarioView` :151, `renderConfigFor` :345, delete `applyServiceToggles` :385, `setDotPath` :434, `mergeMaps` if unused)
- Modify: `internal/core/validate/tests/tests_test.go`
- Modify: `internal/cli/test/profile.go` (:183-204, delete `scenarioRaw` :257 and `scenarioServices`)
- Modify: `internal/cli/test/profile_test.go`

- [x] implement `AutoPortPlaceholder` and `ScenarioView` per Technical Details (clone `Services` + `Raw` with explicit nil init, toggle rule with `required` guard, effective `enabled` into `Raw["services"]`, `env.vars` merged into `Raw["vars"]` through the moved copying `mergeMaps` with the placeholder, strip the compose overlays from the typed fields AND from `Raw`, ignore `setDotPath` errors with the documented reason, never mutate the input)
- [x] `validate/tests`: `renderConfigFor` delegates to `ScenarioView`; `newScenarioView` builds its compose-file set from `ScenarioView(cfg, scn.Env).ComposeFiles()`; remove the local helpers and now-unused imports; note in the `renderConfigFor` comment that the view also drops compose overlays (irrelevant to rendering)
- [x] `cli/test/profile.go`: one `ScenarioView` feeds both `ScanComposeCost` and `ScanComposeIsolation`; remove `scenarioRaw` and `scenarioServices`
- [x] write table-driven tests for `ScenarioView`: enable unknown service ignored; disable on `required: true` keeps `Enabled`; `Raw.services.<n>.enabled` written only for toggled names, existing keys preserved; `env.vars` dot-path merged over existing NESTED `Raw.vars` (`vars.ports.valkey` pre-existing), `auto` → placeholder; `Compose.Extra`, `LocalComposeExtra`, `Raw.compose.extra` and `Raw.services.<n>.compose.extra` all cleared; an `exports.env` rule whose `when:` references a `compose.extra` path evaluates as inactive on the view; nil `Raw`; malformed dot-path (`a..b`, `""`) silently skipped
- [x] write an isolation test: deep-snapshot a `cfg` whose `Raw` carries `compose.extra` AND `services.<n>.compose.extra` on an UNTOGGLED service; build two views with different overlays (one touching a nested existing var, one toggling a different service, one with no toggles at all), assert `cfg` still deep-equals the snapshot (the untoggled service's entry map must not have been stripped in place) and neither view carries the other's overlay
- [x] update `tests_test.go` / `profile_test.go` expectations that relied on the old views; add one case in each where a `compose.extra` overlay file carries a finding (or a build service) the copy must not see; keep the existing render-pass tests green through the delegated `renderConfigFor`
- [x] run `go test ./internal/core/workflow/envtest/... ./internal/core/validate/tests/... ./internal/cli/test/...` - must pass before task 2

### Task 2: `portPlan` — one scan before allocation, traced paths join the batch

**Files:**
- Create: `internal/core/workflow/envtest/portplan.go`
- Create: `internal/core/workflow/envtest/portplan_test.go`
- Modify: `internal/core/workflow/envtest/localyaml.go` (`scenarioEnvOverlay` :121)
- Modify: `internal/core/workflow/envtest/localyaml_test.go`

- [x] implement `portPlan`, `buildPortPlan(origCfg, scn, baseDir)`, `pinnedVarPath` and `hasAllocatedPorts()` per Technical Details (union rule, effective-pin predicate over the normalised overlay, sorted + compacted)
- [x] `scenarioEnvOverlay`: iterate the sorted union of `scn.Env.Vars` keys and `ports` keys so an implicit path lands in `vars`; keep the "auto but no port" error for explicit sentinels
- [x] write table-driven tests for `buildPortPlan` against a temp project tree: traced path added; explicit `auto` + traced de-duplicated to one entry; two compose entries on the same path → one entry; a finding from a service the scenario disables is absent (its compose file is not in the view's chain); a rule gated `when: vars.<x>` that the scenario sets through `env.vars` is classified as the copy would; untraced finding contributes nothing; order stable across runs
- [x] write table-driven tests for `pinnedVarPath`: int pins; `"6380"` pins; `float64` pins; `auto` does not; `null` does not; `""` does not; nested form `ports: {valkey: 6380}` pins `ports.valkey`; a map at the path does not pin; a value present only in the seed `local.yml` does not pin
- [x] write tests for `scenarioEnvOverlay` with an implicit path (written as int at the dot-path), mixed explicit/implicit paths, and an implicit path whose seed `local.yml` already carries a developer value (the allocated port replaces it after `BuildLocalOverlay`)
- [x] run `go test ./internal/core/workflow/envtest/...` - must pass before task 3

➕ shared helpers extracted while implementing: `expandVarPaths(vars, subst)` in
`scenarioview.go` (the one dot-path expansion, used by both the view's
placeholder overlay and the plan's raw pin overlay) and `isAutoPort(value)` in
`scenario.go` (the single sentinel test).

### Task 3: Runner wiring — gate before `CopyTree`, plan through allocation and retry

**Files:**
- Modify: `internal/core/workflow/envtest/runner.go` (`ScenarioStatus` comments :76-86, `RunScenario` doc :294-301 and body :330-532, `scanComposeIsolationGate` :544, `hasAllocatedPorts` :587, `writeCopyLocalYAML` :645, `retryDeployWithFreshPorts` :678)
- Modify: `internal/core/workflow/envtest/runner_test.go`
- Modify: `internal/cli/test/run.go` (`Long` help text :52-57: the scan now runs on the scenario's view of the project before the copy is made)
- Modify: `internal/cli/test/run_test.go` (JSON-mode status of a blocked scenario)

- [ ] build `plan := buildPortPlan(origCfg, scn, req.BaseDir)` right after `origCfg` loads; call the gate BEFORE `CopyTree`; on block return `&ScenarioResult{Name, Status: StatusFailed, Duration}` with nil error and empty `ComposeProject`/`CopyPath`
- [ ] rewrite the `RunScenario` doc contract (blocked finding = `StatusFailed` with nothing created), the `StatusFailed` constant comment (:79-81, no longer implies the copy deployed), the `scanComposeIsolationGate` comment (takes findings, runs pre-copy, warns through `warn` only, no report directory) and the `dwe test run` `Long` help text (:52-57)
- [ ] `scanComposeIsolationGate` takes findings instead of `copyRoot` and drops the `LoadConfigOrWrap` call
- [ ] `writeCopyLocalYAML` and `retryDeployWithFreshPorts` take `plan`; the retry re-allocates `len(keys)+len(autoPaths)` from the same plan; the deploy-retry gate uses `plan.hasAllocatedPorts()`; delete the old free function; update the `writeCopyLocalYAML` doc comment (spec §5/§9 references stay) to describe the traced paths
- [ ] write runner tests (stub `execDwe` and `allocatePorts`): traced path written into the copy's `local.yml` `vars` with the allocated number; explicit pin not overwritten; retry re-allocates and rewrites the same set; blocking finding → `StatusFailed`, nil error, empty `ComposeProject`/`CopyPath`, no `copyRoot` on disk, no manifest; non-blocking traced finding produces no warning; untraced finding still warns
- [ ] write a `cli/test` test: a blocked scenario exits 1 in text mode and reports `"status": "failed"` with no `kept:` line under `--keep` and under `--output json`
- [ ] run `go test ./internal/core/workflow/envtest/... ./internal/cli/test/...` - must pass before task 4

### Task 4: Traced findings are covered everywhere; scanner messages tell the truth

**Files:**
- Modify: `internal/core/workflow/envtest/hostports.go` (`CoversInterpolatedHostPort` :102-114)
- Modify: `internal/core/workflow/envtest/hostports_test.go`
- Modify: `internal/core/project/config/compose_scan.go` (`interpolatedFinding` :967-1009)
- Modify: `internal/core/project/config/compose_scan_test.go`
- Modify: `internal/core/validate/tests/tests.go` (loop comments :102-131, `uncoveredScenarios` :175-181)
- Modify: `internal/core/validate/tests/tests_test.go`
- Modify: `internal/cli/test/profile_test.go`

- [ ] `CoversInterpolatedHostPort`: any non-empty `VarPath` is covered; rewrite the doc comment (auto-remapped or pinned — the author's decision)
- [ ] rewrite the varPath and untraced message branches per Technical Details; keep `Blocking: false` and the sourceService branch verbatim
- [ ] `validate/tests`: keep the `VarPath || SourceService` branch (it still matters for `SourceService`), rewrite its comment to say the VarPath half is always covered since the runner remaps it
- [ ] write/update tests: `CoversInterpolatedHostPort` table (traced without scenario config → true; pinned → true; sourceService remapped/not; other kinds false); scanner message goldens for both rewritten branches; `tests.isolation` no longer warns for a traced var in a project with scenarios that set nothing; still warns for an untraced var; `SourceService` cases unchanged; `cost_profile.isolation_findings` excludes traced entries
- [ ] run `go test ./internal/core/project/config/... ./internal/core/workflow/envtest/... ./internal/core/validate/tests/... ./internal/cli/test/...` - must pass before task 5

### Task 5: Document Change A and commit

**Files:**
- Modify: `docs/reference/config/tests.md` + `docs/i18n/ru/reference/config/tests.md`
- Modify: `docs/guides/integration-tests.md` + `docs/i18n/ru/guides/integration-tests.md`
- Modify: `docs/guides/upgrading.md` + `docs/i18n/ru/guides/upgrading.md`
- Modify: `docs/internals/packages.md`
- Modify: `skills/dwe/SKILL.md`, `skills/dwe/references/integration-tests.md`, `skills/dwe/references/add-service-and-tools.md` (:78)
- Modify: `CHANGELOG.md`

Three finding states to keep straight in every text below: a variable traced to
`vars.<path>` is ALWAYS covered (remapped, or pinned by the scenario); a
variable traced to `services.<n>.ports.<p>` stays covered ONLY in scenarios
where `<n>` is remapped (enabled and not disabled, or required) — its warning
and the sourceService message are unchanged; an untraced variable always warns.

- [ ] `tests.md`: `auto` section (:110-112) — `auto` is now needed only to allocate a port for a var no compose port reads; isolation model (:180-182) — rewrite the "remapped only through what they read" paragraph with the three states above, an explicit `env.vars` number pins, and the scenario view (and therefore `tests.isolation` / `cost_profile`) excludes per-developer `compose.extra` overlays like the copy does; scanner table (:425-440) — `interpolated_host_port` severity cell: "warning when the variable is untraced, or reads a service port the scenario does not remap"; **`### Interpolated host ports` (:472-495)** — coverage table row for `from: vars.<path>` becomes "covered always (remapped automatically; an explicit `env.vars` number pins it)", the `services.<n>.ports` row unchanged, intro paragraph and the three per-caller bullets (`test run` / `test list` / `validate tests`) rewritten; limitations (:531) — drop the `env.vars: auto` advice
- [ ] `tests.md` gate-before-copy targets: the generated `local.yml` layer list (:176-177) — layer 2 now also carries traced paths the scenario never mentions; the FIRST per-caller list (:466-470) — `dwe test run` no longer scans "the disposable copy", it scans the scenario's view of the project before the copy exists, a blocking finding fails the scenario with no copy, no teardown and no report directory
- [ ] `integration-tests.md` (:5-11, :170 "scans the copy's compose files", :178) + RU: the traced case is automatic; the remaining manual case is the untraced variable; the scan runs before the copy is made
- [ ] `upgrading.md`: new `## Upgrading to 0.6.2` above `## Upgrading to 0.6.1` with `### Integration tests`: ports routed through `vars:` are now remapped in test copies; a scenario step that read the original host port as a literal must read the variable (or pin it with `env.vars: { <path>: <number> }`); `env.vars: auto` entries can be deleted but are harmless; a blocked scenario no longer leaves a copy or a report directory. RU mirror.
- [ ] `packages.md`: `envtest/` bullet (:109) — `ScenarioView` (single scenario-view definition, consumed by `validate/tests` render + isolation and `cli/test`; drops compose overlays; `auto` → `AutoPortPlaceholder`), `portPlan`, gate-before-copy order with its consequences (no copy / manifest / report dir for a blocked scenario, JSON mode sees only `status`), and the working-tree-vs-copy divergence (gitignored compose files, `.dwe/compose.bridge.yml`, gitignored `docker.local.yml`); `validate/tests/` bullet (:171) — view comes from envtest; `compose_scan.go` bullet (:83) — the VarPath finding is informational, consumers treat it as covered
- [ ] `skills/dwe/SKILL.md` (~:220-223), `skills/dwe/references/integration-tests.md` (:23, :48, :125-137) and `skills/dwe/references/add-service-and-tools.md` (:78, "a port routed to compose via vars is never isolated without per-scenario auto"): `isolation_findings` no longer lists vars-traced ports; the remap covers `services.<name>.ports` AND vars traced through an `exports.env` rule; the manual fix line applies only to untraced vars; the service-port-routed case keeps its scenario-state condition
- [ ] `CHANGELOG.md` `## [Unreleased]`: replace "Nothing yet." with `### Changed` — `dwe test` remaps compose host ports routed through `vars:` + `exports.env` automatically; `env.vars: { …: auto }` is no longer required, an explicit number pins the port; the `interpolated_host_port` finding no longer appears for a variable traced to a `vars:` path (untraced variables, and service-port-routed variables in scenarios that do not remap that service, still warn, and the untraced message now points at the two routing fixes instead of `env.vars: auto`); a scenario blocked by the isolation scanner no longer creates a copy or a report directory; `dwe validate tests` and `dwe test list --output json` evaluate scenarios without per-developer `compose.extra` overlays, like the copy runs
- [ ] `make build` (regenerates `content_hashes_gen.go`), refresh the `> Translated from: … @ <hash>` header of every RU page whose EN source changed, `make lint && make test && (cd web && npm run build)` - must pass
- [ ] commit `feat(test): remap compose host ports routed through vars automatically`

### Task 6: Scoped port probe, preflight option and `ports_free` filter

**Files:**
- Modify: `internal/core/validate/validate.go` (`Context` :29)
- Modify: `internal/core/execution/preflight/preflight.go` (`RunFn` :92, `Run` :104)
- Modify: `internal/core/execution/preflight/preflight_test.go`
- Modify: `internal/core/validate/env/ports.go` (`CollectPortConflicts` :94, `WaitPortsReleased` :180, `Run` :283-306, `collectDeclaredPorts` :336)
- Modify: `internal/core/validate/env/ports_test.go`
- Modify: the nine stubs: `internal/core/workflow/lifecycle/helpers_test.go:24`, `internal/core/workflow/lifecycle/preflight_test.go:39,64,91,117`, `internal/cli/lifecycle/testhelpers_test.go:24`, `internal/cli/lifecycle/reset_test.go:432`, `internal/cli/deploy/bridge_test.go:108,186`

- [ ] add `Services []string` to `validate.Context` with the doc comment from Technical Details
- [ ] add `preflight.Option`, `WithServices`, the variadic on `Run` and the `RunFn` alias; `Run` copies the option into `vctx.Services`
- [ ] `ports.go`: `collectDeclaredPorts(cfg, scope)`; `CollectPortConflictsScoped` as the implementation, `CollectPortConflicts` delegating with nil; `WaitPortsReleased` passes nil; `portsFreeValidator.Run` uses the scoped pair; doc comments say the wizard seam and `WaitPortsReleased` stay unscoped by design
- [ ] fix the nine stubs (add `...preflight.Option`)
- [ ] write tests: `WithServices` reaches `Context.Services` and a call without options leaves it nil (preflight_test); `collectDeclaredPorts` table — nil scope keeps all enabled, scope keeps only listed, a listed disabled service stays excluded, unknown name in scope ignored; **diagnostic-level** `portsFreeValidator.Run` test through `dockerPSOutFn` / `portListenFn`: an out-of-scope busy port yields no diagnostic, an in-scope busy port yields the same diagnostic as before, nil scope unchanged; `CollectPortConflicts` unscoped behaviour pinned
- [ ] run `go test ./internal/core/execution/preflight/... ./internal/core/validate/... ./internal/cli/... ./internal/core/workflow/lifecycle/...` - must pass before task 7

### Task 7: Deploy CLI computes the scope and validates `--service` first

**Files:**
- Modify: `internal/cli/deploy/deploy.go` (:521, :597-607)
- Create: `internal/cli/deploy/preflight_scope.go`
- Create: `internal/cli/deploy/preflight_scope_test.go`
- Modify: `internal/cli/deploy/bridge_test.go` (or a new focused test file) for the option and ordering assertions

- [ ] move the "service %q not found in config" loop above the preflight call; keep the `svcDeploys` extra-loading where it is
- [ ] implement `preflightScope(cfg, names)` per Technical Details (BFS over `DependsOn`, enabled-only for dependencies, requested names included as given, cycle-safe, sorted)
- [ ] pass `preflight.WithServices(preflightScope(cfg, opts.Services))` only when `len(opts.Services) > 0`
- [ ] write table-driven tests for `preflightScope`: no deps; transitive chain; diamond; cycle; disabled dependency skipped; unknown dependency name ignored; requested disabled service passed through unchanged
- [ ] write tests through `Opts.PreflightFn` (this covers both `deploy run --service` and the toggle executor, which share `RunHelper`): the option carries the closure for a `--service` run and is absent for a whole-project run; an unknown `--service` fails before the preflight stub is called
- [ ] run `go test ./internal/cli/deploy/... ./internal/cli/service/...` - must pass before task 8

### Task 8: Document Change B and commit

**Files:**
- Modify: `docs/reference/config/validate.md` + `docs/i18n/ru/reference/config/validate.md`
- Modify: `docs/guides/troubleshooting.md` + `docs/i18n/ru/guides/troubleshooting.md`
- Modify: `docs/internals/packages.md`
- Modify: `CHANGELOG.md`

- [ ] `validate.md` (:85) + RU (:87): under a per-service deploy (`dwe deploy run --service <name>`, and the deploy `dwe services enable|disable --apply` performs) `env.ports_free` checks only the named services and the transitive `depends_on` closure of their `service.yml` declarations; whole-project runs and `dwe validate` are unchanged
- [ ] `troubleshooting.md` (:31) + RU (:33): one sentence on the narrowed scope for per-service deploys
- [ ] `packages.md`: `preflight/` bullet (:103) — `WithServices` option, `Context.Services` contract (only `ports_free` reads it; the deploy CLI computes the closure, preflight never derives it; the pre-wizard gate filters `ports_free` out and needs no scope); `validate/env/` bullet (:164) — `CollectPortConflictsScoped` is the implementation, the exported unscoped probe and `WaitPortsReleased` delegate with nil; deploy bullet — existence check precedes preflight, both `deploy run --service` and the service toggle executor pass the scope
- [ ] `CHANGELOG.md` `## [Unreleased]` `### Fixed`: a per-service deploy (`dwe deploy run --service <name>`, `dwe services enable|disable --apply`) no longer fails `ports_free` on a port held for a service the run does not start; the probe checks the named services and their `depends_on` closure; an unknown `--service` name is rejected before preflight runs
- [ ] `make build`, refresh RU hash headers, `make lint && make test && (cd web && npm run build)` - must pass
- [ ] commit `fix(preflight): scope ports_free to the services a per-service deploy brings up`

### Task 9: Verify acceptance criteria

- [ ] Change A: a project fixture with `"${VALKEY_PORT:-6379}:6379"` + `exports.env` rule `from: vars.ports.valkey` and a scenario with no `env.vars` — the copy's generated `local.yml` carries `vars.ports.valkey: <allocated>`, no isolation warning is printed, `dwe validate` emits no `tests.isolation` diagnostic; the same fixture with `env.vars: { ports.valkey: 6380 }` keeps 6380 and stays silent; with the rule's `when:` falsy the warning and the literal default remain
- [ ] Change A: `dwe test list --output json` `cost_profile.isolation_findings` omits the traced entry; a blocked scenario exits 1 with `status: failed` and leaves no `.dwe/tests/runs/<scenario>/` directory
- [ ] Change B: `Opts.PreflightFn` receives `WithServices` with the closure for `--service`; an out-of-scope busy port produces no `ports_free` diagnostic; whole-project `deploy run`, `dwe run`, `dwe stop`, `dwe reset run`, the setup wizard's port-fix step and `dwe validate` behave as before
- [ ] verify no test, doc, commit message or comment references an internal task tracker
- [ ] run full test suite: `make lint && make test && make test-race`
- [ ] confirm `git status` is clean apart from the intended files and `content_hashes_gen.go`

### Task 10: [Final] Update documentation

- [ ] re-read both CHANGELOG entries and the Upgrading section together for consistent wording
- [ ] `AGENTS.md`: no new critical pattern expected; if `ScenarioView` (three consumers, overlay stripping) or the scope option turns out to be a trap during implementation, add a one-line pointer there and the write-up to `packages.md`
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Live verification** (binary from the branch, real projects on this machine):

- magento: `dwe test run smoke` with the three `env.vars: … : auto` lines REMOVED from `workspace/tests/smoke.yml`, while ficbird's redis holds `:6379` — the scenario passes with no port collision and no `compose isolation:` warning; `dwe validate` on magento reports no `tests.isolation` warning.
- beetDeck: with a foreign container bound to another service's declared port, `dwe deploy run --service <x>` passes preflight; with the foreign container on `<x>`'s own port (or on the port of a service in its `depends_on`) preflight fails naming that port.
- `dwe validate` across the local projects with `workspace/tests/` — no new warnings and nothing that used to warn about an untraced variable went silent; on a project with a `compose.extra` overlay in `local.yml`, confirm `dwe test list --output json` cost numbers changed only by that overlay's services.
- One `dwe test run` under `--parallel 2` on a project with traced ports — two copies, two distinct allocated ports per traced path.

**External updates**

- Sync the installed copy of `skills/dwe` after the branch merges (the loaded skill comes from `main`).
- magento (outside this repository): the `env.vars: … : auto` lines in `workspace/tests/smoke.yml` become optional; remove them at the next convenient redeploy.

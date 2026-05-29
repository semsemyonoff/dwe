# Per-service reset: huh confirm form + always-on container/dir cleanup

## Overview

Two related changes that close out manual confirmation prompts and harden the per-service reset path:

**A. Fix `devbox reset run --service <name>`:**

1. Replace the manual `bufio.NewReader` `[y/N]` prompt with a `ui.RunConfirm` huh form that lists, in bullets, exactly what is about to happen.
2. Make the per-service reset always perform a baseline cleanup — even when the service has no `services/<name>/reset.yml`:
   - stop **and remove** the service container (today: only stops)
   - delete the service directory (`svc.Dir`) if set and present on disk
3. Run all of this — baseline cleanup + optional `reset.yml` — through a single `pipeline.RunWithOptions` call. Synthetic phases are prepended to whatever the user's `reset.yml` declares. This reuses the existing pipeline reporter, log handling, `ErrSilent` path, and styling, so output is visually consistent with full `devbox reset run`.
4. Volumes are **deliberately not** touched automatically. If a user wants volume cleanup, they put a `reset.yml` in the service folder calling `docker_remove_project_volumes`.

**B. Migrate the other remaining manual confirmation prompt to huh:**

- `internal/cli/deploy/deploy.go:644-652` — "declared after: deps not in this run — proceed anyway? [y/N]" → `ui.RunConfirm` with affirmative `Proceed`, negative `Cancel`.

All other confirmation prompts in the codebase already use `ui.RunConfirm` / `ui.ConfirmRun` / `ui.RunSelector` / `ui.RunMultiSelect` / `ask.Run`. Closing these two completes the "everywhere huh" goal.

## Context (from discovery)

- `internal/cli/lifecycle/reset.go` — `resetServiceRunCmd` at line 236; manual prompt at 277-293; current per-service flow at 295-393.
- `internal/cli/lifecycle/stop.go` — `stopServiceLocked` at line 70, still used by `devbox stop <name>`; reset will stop calling it.
- `internal/cli/deploy/deploy.go` — manual prompt at lines 644-652.
- `internal/core/ui/confirm.go` — `RunConfirm(title, affirmative, negative)`. Test seam: `runConfirmFormFn` is the package-level swappable function used in `confirm_test.go`. `ui.ErrCancelled` is the sentinel returned when user presses Esc/Ctrl-C.
- `internal/shared/docker/stop.go` — `StopContainer(ctx, dockerBin, name, timeoutSec)` and `DefaultStopTimeoutSec`. Will add `RemoveContainer(ctx, dockerBin, name)` as the symmetric pair.
- `internal/core/execution/builtin/` — existing builtins. Add new file `containers_stop_remove.go`. Registration table is in `builtin.go`.
- `internal/core/execution/builtin/paths.go` — existing `remove_paths` builtin. Reused unchanged for directory removal (takes relative paths, validates safely, uses `os.RemoveAll` which is idempotent on missing path).
- `internal/core/execution/pipeline/step.go` — `ResolvedStep` shape: `{Phase config.DeployPhase, Step config.DeployStep, Service string, RuntimeWhen, PhaseWhen, FilesGate, Parallel}`. Synthetic phases will set `Untracked: true` so they're excluded from `[N/M]` counters and journal writes.
- `internal/core/execution/pipeline/resolve.go:64` — `ResolvePhaseSteps(cfg, reg, phase, service) ([]ResolvedStep, error)`. We will construct `ResolvedStep` values directly for synthetic phases (not through this resolver, because `config.DeployPhase`/`DeployStep` are flat structs).
- `internal/core/execution/pipeline/executor.go` — `RunWithOptions(opts RunOptions)`.
- `internal/core/project/config/services.go` — `cfg.Services[name]` has `.Container`, `.Dir`, `.Required`, `.Enabled`, `.OnDisable`. `config.LoadServiceResetConfig(baseDir, name)` returns per-service `reset.yml` or nil.
- `internal/core/workflow/deploy/journal/` — `journal.ReplaceServiceWithPending`, `journal.PendingOp`, `journal.PendingDeploy`. Used unchanged.
- Real-world reference: `tbm/devbox/reset.yml` (project-level reset.yml), `tbm/devbox/services/storefront/service.yml` (service with `dir:`, `container:`).
- Reference confirm style: `internal/cli/snapshot/restore.go:158-169` uses action-verb buttons (`Restore`/`Cancel`) with multi-line title.

## Development Approach

- **testing approach**: Regular (code first, then tests within each task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - write unit tests for new functions and modified functions
  - cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` after each task
- per CLAUDE.md "Project Status & Compatibility Policy": no backwards-compat shims or schema-version bumps; rename/restructure freely

## Testing Strategy

- **unit tests**: required for every task. Existing testing patterns:
  - Mock `runConfirmFormFn` (package-level seam in `internal/core/ui/confirm.go`) to assert prompt content and return either Yes/No/cancelled.
  - Mock docker-bin via shell-script fake in `t.TempDir()` (same pattern as `internal/shared/docker/stop_test.go`).
  - Use `cmd.SetIn` / `cmd.SetOut` / `cmd.SetErr` for cobra-level tests.
- **e2e tests**: project has no UI-based e2e; not applicable.
- **commands**: `make test` (or `go test ./internal/cli/lifecycle/... ./internal/core/execution/builtin/... ./internal/shared/docker/... ./internal/cli/deploy/...` for focused runs after `make embedded-docs`).

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

The per-service reset becomes a thin orchestrator:

1. Validate (existing) → preflight (existing) → **huh confirm** (new) → `on_disable.before` hooks (existing) → acquire project locks (existing).
2. Build a single combined list of `ResolvedStep`:
   - synthetic phase `container` with one builtin step `docker_stop_remove_container{name: svc.Container}` — **always**.
   - synthetic phase `files` with one builtin step `remove_paths{paths: [svc.Dir]}` — only when `svc.Dir != ""` and the dir exists.
   - phases from `services/<name>/reset.yml` if present — resolved via existing `pipeline.ResolvePhaseSteps`.
3. One `pipeline.RunWithOptions` call.
4. On success: `journal.ReplaceServiceWithPending` (existing) → final stdout line.

Synthetic phases use `Untracked: true` so the pipeline reporter doesn't count them in `[N/M]` and they don't get journal entries. Per `internal/core/execution/pipeline/plain.go:197-199` and `executor.go:514-527`, untracked phases produce no automatic phase banner / step lines from the reporter. To keep user feedback, the new builtin itself emits success lines via `ectx.Output.Writer()` (matching `daemon_stop.go:93,101`); the existing `remove_paths` already emits its own success line via `ectx.Output.Success` (`paths.go:62`).

The new builtin `docker_stop_remove_container` is a thin composition: it resolves the container template via `daemon.ResolveContainerName(cfg.Project.FullName(), template)` (matching `daemon_stop.go:74-75` — required so that projects with a non-empty `project.prefix` get the prefixed container name), then calls `docker.StopContainer` and `docker.RemoveContainer` (new symmetric helper). Both helpers are idempotent on missing-container, matching the existing `StopContainer` contract.

The deploy missing-deps prompt becomes a one-line `ui.RunConfirm` call.

## Technical Details

### Synthetic `ResolvedStep` construction

Synthetic phases use `Untracked: true` so the pipeline reporter doesn't count them in `[N/M]` and they don't get journal entries. Per `internal/core/execution/pipeline/plain.go:197-199` + `executor.go:514-527`, untracked phases emit **no automatic phase/step output** from the reporter — so the new builtins themselves must emit `ectx.Output.Writer()` lines for user visibility (matching the established `daemon_stop.go:93,101` pattern). `remove_paths` already emits `ectx.Output.Success("removed %s")` at `paths.go:62`, so the files phase needs nothing extra.

`Service: name` is set on synthetic phases so step addresses align with the user `reset.yml` step addresses (both formatted as `<name>/<phase>/<step>` via `ResolvedStep.StepAddress()`).

```go
containerStep := pipeline.ResolvedStep{
    Phase: config.DeployPhase{
        Name:        "container",
        Description: "Stop and remove container",
        Untracked:   true,
    },
    Step: config.DeployStep{
        Name: "stop-and-remove",
        Type: "builtin",
        Cmd:  "docker_stop_remove_container",
        With: map[string]any{
            // svc.Container is a TEMPLATE (e.g. "app-postgres"). The builtin
            // resolves it via daemon.ResolveContainerName(cfg.Project.FullName(), template)
            // internally — same convention as docker_daemon_stop.
            "container_template": svc.Container,
            "stop_timeout":       "10s", // matches docker.DefaultStopTimeoutSec; param style mirrors daemon_stop
        },
    },
    Service: name, // align step addresses with user reset.yml phases
}
```

Same shape for the `files` phase (only added when applicable):

```go
filesStep := pipeline.ResolvedStep{
    Phase: config.DeployPhase{Name: "files", Description: "Remove service directory", Untracked: true},
    Step: config.DeployStep{
        Name: "remove-dir",
        Type: "builtin",
        Cmd:  "remove_paths",
        With: map[string]any{"paths": []string{svc.Dir}},
    },
    Service: name,
}
```

### Confirm body construction

```
Reset service "<name>"?

[Warning: this is a required service.]    <- only if svc.Required

This will:
  • stop and remove container "<svc.Container>"
  • delete directory <svc.Dir>             <- only if svc.Dir != "" && stat ok
  • run services/<name>/reset.yml          <- only if file exists
  • require a subsequent: devbox deploy run --service <name>
```

Implementation: build `title := header + "\n\n" + body`, call `ui.RunConfirm(title, "Reset", "Cancel")`. Map `(false, ui.ErrCancelled)` to silent `return nil` (cancel). Other errors wrap and return.

### `RemoveContainer` helper

In `internal/shared/docker/stop.go`, mirror `StopContainer`:

```go
func RemoveContainer(ctx context.Context, dockerBin, containerName string) error {
    if dockerBin == "" {
        dockerBin = "docker"
    }
    cmd := exec.CommandContext(ctx, dockerBin, "rm", "-f", containerName)
    cmd.Stdout = io.Discard
    var stderr strings.Builder
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        errOut := stderr.String()
        if strings.Contains(errOut, "No such container") {
            return nil
        }
        if errOut != "" {
            return fmt.Errorf("docker rm: %w: %s", err, strings.TrimSpace(errOut))
        }
        return fmt.Errorf("docker rm: %w", err)
    }
    return nil
}
```

### New builtin `docker_stop_remove_container`

`internal/core/execution/builtin/containers_stop_remove.go`. Param convention **mirrors `docker_daemon_stop`** for consistency:
- `container_template` (string, required) — name template, resolved via `daemon.ResolveContainerName(cfg.Project.FullName(), template)`.
- `stop_timeout` (string Duration, optional, default `"10s"`) — parsed via `stopTimeoutSeconds` (reused from `daemon_stop.go`).

```go
type dockerStopRemoveContainerBuiltin struct{}

func (dockerStopRemoveContainerBuiltin) Validate(with map[string]any) error {
    if getStringParam(with, "container_template", "") == "" {
        return fmt.Errorf("docker_stop_remove_container: container_template required")
    }
    return nil
}

func (dockerStopRemoveContainerBuiltin) Describe(with map[string]any) string {
    return "stop+rm container: " + getStringParam(with, "container_template", "?")
}

func (dockerStopRemoveContainerBuiltin) Run(ctx context.Context, with map[string]any, ectx ExecContext) error {
    if ectx.Config == nil {
        return fmt.Errorf("docker_stop_remove_container: config not available")
    }
    projectFull := ectx.Config.Project.FullName()
    fullName, err := daemon.ResolveContainerName(projectFull, getStringParam(with, "container_template", ""))
    if err != nil {
        return err
    }
    secs := stopTimeoutSeconds(getStringParam(with, "stop_timeout", ""))
    dockerBin := config.DockerBin(ectx.Config)

    if err := docker.StopContainer(ctx, dockerBin, fullName, secs); err != nil {
        return fmt.Errorf("stop container %q: %w", fullName, err)
    }
    _, _ = fmt.Fprintf(ectx.Output.Writer(), "✓ container stopped: %s\n", fullName)

    if err := docker.RemoveContainer(ctx, dockerBin, fullName); err != nil {
        return fmt.Errorf("remove container %q: %w", fullName, err)
    }
    _, _ = fmt.Fprintf(ectx.Output.Writer(), "✓ container removed: %s\n", fullName)
    return nil
}
```

Register in `builtin.go`. The existing registration table is the source of truth — read it before editing.

**Contract on stop failure:** if `docker.StopContainer` returns a non-nil error (other than missing-container which it swallows), the builtin propagates and **does NOT attempt rm**. Rationale: stop failure means the container is in an unexpected state; force-removing on top would hide the diagnostic. Tests must cover this.

### Pipeline log naming + enable

- If `svcResetCfg != nil`: `logEnabled = svcResetCfg.LogEnabled()`.
- Else: `logEnabled = false`.
- Always: `OpenPipelineLog(workDir, "reset-"+name, logEnabled)`.

### Deploy.go migration

Replace `deploy.go:644-652` body inside the `if isInteractive` branch (the non-interactive `else` branch stays as-is):

```go
title := fmt.Sprintf("Declared after: deps not in this run — proceed anyway? (missing: %s)",
    strings.Join(missing, ", "))
ok, err := ui.RunConfirm(title, "Proceed", "Cancel")
if err != nil {
    if errors.Is(err, ui.ErrCancelled) {
        return &deployCancelledError{}
    }
    return err
}
if !ok {
    return &deployCancelledError{}
}
```

Drop the `bufio` import if no other uses remain in `deploy.go`.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, tests, docs in this repo.
- **Post-Completion**: `make build` regenerates embedded docs + content-hash manifest (CI guard requires this).

## Implementation Steps

### Task 1: Add `docker.RemoveContainer` helper

**Files:**
- Modify: `internal/shared/docker/stop.go`
- Modify: `internal/shared/docker/stop_test.go`

- [x] add `RemoveContainer(ctx, dockerBin, containerName string) error` in `internal/shared/docker/stop.go` mirroring `StopContainer` (idempotent on "No such container", wraps other errors)
- [x] use `docker rm -f` so containers in any intermediate state (Created, Exited, Restarting) are removed reliably — add a one-line comment in the body explaining the `-f` choice
- [x] doc-comment matches `StopContainer` style — note bypassing compose, idempotency contract
- [x] write `TestRemoveContainer_HappyPath` using fake docker-bin (model after `TestStopContainer` in `stop_test.go`)
- [x] write `TestRemoveContainer_MissingContainerReturnsNil` (fake bin exits 1 with "No such container" on stderr)
- [x] write `TestRemoveContainer_GenericError` (fake bin exits 1 with arbitrary stderr → wrapped error)
- [x] write `TestRemoveContainer_DefaultBin` (empty dockerBin defaults to "docker", parallels existing test)
- [x] run tests: `go test ./internal/shared/docker/...` — must pass before Task 2

### Task 2: Add `docker_stop_remove_container` builtin

**Files:**
- Create: `internal/core/execution/builtin/containers_stop_remove.go`
- Create: `internal/core/execution/builtin/containers_stop_remove_test.go`
- Modify: `internal/core/execution/builtin/builtin.go` (registration table)

- [x] read `builtin.go` to find the existing registration table and confirm naming convention for new builtins; read `daemon_stop.go` as the reference template
- [x] create `containers_stop_remove.go` with `dockerStopRemoveContainerBuiltin` struct implementing `Validate` / `Describe` / `Run` — code skeleton in plan Technical Details section
- [x] `Validate`: require non-empty `container_template` (matches `daemon_stop.go` convention); no validation on `stop_timeout` (defensive parsing happens in `stopTimeoutSeconds`)
- [x] `Describe`: returns `"stop+rm container: " + container_template`
- [x] `Run`: resolve `projectFull := ectx.Config.Project.FullName()`; resolve `fullName via daemon.ResolveContainerName(projectFull, container_template)` (mandatory — without this, the builtin silently no-ops on projects with `project.prefix`); compute `secs := stopTimeoutSeconds(stop_timeout)`; `dockerBin := config.DockerBin(ectx.Config)`; call `docker.StopContainer(ctx, dockerBin, fullName, secs)`, emit `✓ container stopped: <fullName>` via `ectx.Output.Writer()` on success; then call `docker.RemoveContainer(ctx, dockerBin, fullName)`, emit `✓ container removed: <fullName>`; wrap errors with operation context
- [x] **stop-failure contract**: if `StopContainer` returns non-nil, propagate and do NOT attempt `RemoveContainer` — document with a comment
- [x] register the builtin in `builtin.go`
- [x] write `TestValidate_RequiresContainerTemplate` (empty / missing → error; non-empty → nil)
- [x] write `TestRun_HappyPath` using fake docker-bin: assert (a) container_template resolved with project prefix, (b) `stop` invoked first, (c) `rm -f` invoked second, (d) both output lines emitted
- [x] write `TestRun_StopFailurePropagatesAndSkipsRm` (fake bin returns error on stop → rm must NOT be invoked, error wrapped with `stop container "<full>":` prefix)
- [x] write `TestRun_MissingContainerIdempotent` (stop returns nil due to "No such container" → rm also tolerates missing → overall success, both output lines emitted)
- [x] write `TestRun_ResolvesContainerWithProjectPrefix` (config with `project.prefix=devbox` + `name=tbm` + template=`app-postgres` → docker invoked with `devbox-tbm-app-postgres`)
- [x] write test that builtin is registered in `builtin.go` registry (lookup `docker_stop_remove_container` returns non-nil — model after how other builtin registration tests do it; if no such test pattern exists, skip)
- [x] run tests: `make embedded-docs && go test ./internal/core/execution/builtin/...` — must pass before Task 3

### Task 3: Refactor `resetServiceRunCmd` to synthetic-pipeline shape

**Files:**
- Modify: `internal/cli/lifecycle/reset.go`
- Modify: `internal/cli/lifecycle/reset_test.go`

- [x] in `reset.go`, add a package-level confirm seam alongside the existing seams (`resetServiceRunHook`, `resetRunHookFn`, `stopContainerFn`):
  ```go
  // resetConfirmFn is the swap seam for the per-service reset confirmation form.
  // Tests override this to assert prompt content and inject Yes/No/cancelled.
  var resetConfirmFn = ui.RunConfirm
  ```
  This is required because `runConfirmFormFn` in `internal/core/ui/confirm.go:13` is package-private — tests in the `lifecycle` package cannot swap it directly.
- [x] replace lines 277-293 (manual prompt) with a `resetConfirmFn` call:
  - build body: header `Reset service %q?`; optional `Warning: this is a required service.` line (when `svc.Required`); bullet list — container line always (`stop and remove container "<svc.Container>"`); dir line only if `svc.Dir != ""` and `os.Stat(filepath.Join(baseDir, svc.Dir))` returns nil; reset.yml line only if `services/<name>/reset.yml` exists (stat check); subsequent-deploy line always (`require a subsequent: devbox deploy run --service <name>`)
  - title = header + "\n\n" + body
  - call `resetConfirmFn(title, "Reset", "Cancel")`
  - on `ui.ErrCancelled` → silent `return nil`; on `!ok` → silent `return nil`; on other error → wrap and return
  - non-interactive without `--yes`: keep existing error string `"non-interactive terminal: use --yes to confirm per-service reset"`
- [x] replace lines 295-394 (current flow) with the synthetic-pipeline assembly:
  - run `on_disable.before` hooks (existing logic — no change to ordering)
  - acquire project locks (existing logic — no change)
  - load `svcResetCfg` via `config.LoadServiceResetConfig` (may be nil)
  - load `DockerConfig` (hoist out of the old `if svcResetCfg != nil` block — needed for the always-on container phase too)
  - build `[]pipeline.ResolvedStep`: synthetic container step (always; `Service: name`, `Untracked: true`, builtin `docker_stop_remove_container` with `container_template: svc.Container`, `stop_timeout: "10s"`); synthetic files step (only when `svc.Dir != ""` AND stat ok; `Service: name`, `Untracked: true`, builtin `remove_paths` with `paths: [svc.Dir]`); appended steps from `svcResetCfg.Phases` via `pipeline.ResolvePhaseSteps(cfg, reg, phase, name)` (when `svcResetCfg != nil`)
  - determine `logEnabled` (`svcResetCfg.LogEnabled()` if non-nil else false)
  - open log via `pipeline.OpenPipelineLog(workDir, "reset-"+name, logEnabled)` + create `pipeline.NewPlainReporter` + call `pipeline.RunWithOptions` exactly once (handle `ErrSilent` and log-path message same as today's 372-380)
  - on success: existing `journal.ReplaceServiceWithPending` block + final `Service %q reset...` stdout line
- [x] remove the now-unused `bufio` import from `reset.go` if no other uses remain
- [x] remove the call to `stopServiceLocked` and the `StopServiceDeps` construction from `resetServiceRunCmd`; verify `stopServiceLocked` is still used elsewhere — confirm via `grep -n stopServiceLocked internal/cli/lifecycle/` (must still be referenced from `stop.go` for `devbox stop <name>`)
- [x] extend `makeResetServiceTestDir` helper in `reset_test.go` (or add a sibling helper) to accept an optional `dir` parameter that writes `dir: ./services/<name>` into `service.yml` and pre-creates `services/<name>/` on disk in the test workdir. Required for case B below.
- [x] **rewrite existing tests** `TestResetServiceRun_TTYConfirmationDecline` (`reset_test.go:638`) and `TestResetServiceRun_TTYConfirmationAccept` (`reset_test.go:670`): replace `root.SetIn(strings.NewReader(...))` stdin-feeding with `resetConfirmFn` mock that captures the title argument; assert title contains expected bullets per case
- [x] add new tests:
  - case A: service with no `dir:` and no `reset.yml` → only container synthetic phase; assert fake docker-bin was called with `stop` AND `rm`; assert journal updated; assert confirm body has no dir bullet and no reset.yml bullet
  - case B: service with `dir:` present on disk + no `reset.yml` → container + files synthetic phases; assert dir gone after run; assert journal updated; assert confirm body has dir bullet
  - case C: service with `services/<name>/reset.yml` present → all three sources of phases composed in order; assert pipeline runs the user steps too
  - case D: required service → confirm body contains the warning line
  - case E: user picks Cancel (mock returns `(false, nil)`) → returns nil; assert no docker calls and journal untouched
  - case F: user pressed Esc (mock returns `(false, ui.ErrCancelled)`) → returns nil silently
  - case G: non-interactive without `--yes` → existing error returned
  - case H: pipeline failure (fake docker-bin exits nonzero) → journal NOT updated, error propagated
- [x] run tests: `make embedded-docs && go test ./internal/cli/lifecycle/...` — must pass before Task 4

### Task 4: Migrate deploy missing-deps prompt to huh

**Files:**
- Modify: `internal/cli/deploy/deploy.go`
- Create: `internal/cli/deploy/missing_deps_test.go` (no existing `deploy_test.go` — sibling test files in this package are `lock_test.go`, `menu_test.go`, `menu_service_test.go`, `plan_test.go`, `state_test.go`, `notify_test.go`)

- [x] add a confirm seam at package level in `deploy.go` (alongside any existing seams):
  ```go
  // deployMissingDepsConfirmFn is the swap seam for the after-deps prompt.
  var deployMissingDepsConfirmFn = ui.RunConfirm
  ```
  Required for the same reason as the reset seam (Task 3): `runConfirmFormFn` in `ui/confirm.go` is package-private.
- [x] in `deploy.go:640-658`, replace the `bufio.NewReader` block inside the `if isInteractive` branch:
  ```go
  title := fmt.Sprintf("Declared after: deps not in this run — proceed anyway? (missing: %s)",
      strings.Join(missing, ", "))
  ok, err := deployMissingDepsConfirmFn(title, "Proceed", "Cancel")
  if err != nil {
      if errors.Is(err, ui.ErrCancelled) {
          return &deployCancelledError{}
      }
      return err
  }
  if !ok {
      return &deployCancelledError{}
  }
  ```
- [x] keep the non-interactive `else` branch unchanged (still logs info about missing deps to stderr)
- [x] drop `bufio` import if no other uses remain in `deploy.go`; add `errors` import if not already present
- [x] create `missing_deps_test.go` with minimal coverage:
  - test asserting cancel-path (`deployMissingDepsConfirmFn` returns `(false, nil)`) → returns `*deployCancelledError`
  - test asserting Esc-path (`deployMissingDepsConfirmFn` returns `(false, ui.ErrCancelled)`) → returns `*deployCancelledError`
  - test asserting accept-path (`deployMissingDepsConfirmFn` returns `(true, nil)`) → proceeds (no cancel error)
  - test asserting non-interactive path still emits stderr info message and proceeds (no confirm seam invoked)
- [x] run tests: `go test ./internal/cli/deploy/...` — must pass before Task 5

### Task 5: Documentation

**Files:**
- Modify: `docs/reference/config/lifecycle.md` (or `reset.md` if that's where per-service reset lives — locate via grep)
- Modify: builtins reference page (locate where existing builtins like `docker_remove_project_volumes` are documented)
- Modify: `AGENTS.md` (CLAUDE.md is symlink — edit AGENTS.md)

- [x] update per-service reset reference: document new always-on baseline (stop+rm + delete dir when set+exists) and that `reset.yml` is optional and appended after the baseline; explicitly state volumes are NOT auto-removed and link to `docker_remove_project_volumes` for explicit volume cleanup
- [x] add reference entry for new builtin `docker_stop_remove_container` (params: `name` required, `timeout_sec` optional default 10s; behavior: idempotent on missing container; use case: per-service teardown)
- [x] update `AGENTS.md` "Compose-bypass for per-service stop" pattern: note that reset now drives stop+rm through the synthetic-pipeline builtin path (not direct `stopServiceLocked`); `stopServiceLocked` remains for `devbox stop <name>` (no removal there)
- [x] no new tests for docs; rely on existing docs-subsystem test coverage to catch any breakage
- [x] run tests: `make test` — must pass before Task 6

### Task 6: Verify acceptance criteria

- [x] verify A1: per-service reset confirmation shows huh form with bullet list (manual test — skipped, not automatable)
- [x] verify A2: with no `services/<name>/reset.yml`, per-service reset stops AND removes the container (manual test — skipped, not automatable; covered by Task 3 case A unit test)
- [x] verify A3: with `svc.Dir` set and dir present, per-service reset deletes the directory (manual test — skipped, not automatable; covered by Task 3 case B unit test)
- [x] verify A4: with `services/<name>/reset.yml` present, the user pipeline still runs after the baseline phases (manual test — skipped, not automatable; covered by Task 3 case C unit test)
- [x] verify B: deploy `--service <name>` with missing after-deps shows huh `Proceed`/`Cancel` form (manual test — skipped, not automatable; covered by Task 4 unit tests)
- [x] confirm no other manual stdin-driven confirmation prompts remain: grep returned no matches
- [x] run full test suite: `make test` — all packages pass
- [x] run lint: `make lint` — 0 issues

### Task 7: [Final] Regenerate embedded docs + close out

- [x] run `make build` — regenerates `internal/core/docs/embedded/` and `internal/core/docs/content_hashes_gen.go` (CI guard requires this)
- [x] verify `git diff --exit-code internal/core/docs/content_hashes_gen.go` is clean after `make build` (the manifest may have changed if doc files were edited; commit any update)
- [x] move this plan to `docs/plans/completed/2026-05-29-per-service-reset-fixes.md`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification (recommended):**
- Run `devbox reset run --service <name>` interactively against `tbm/` to visually confirm the huh form layout, colors, and Reset/Cancel buttons.
- Run with `--yes` flag to confirm prompt is skipped and baseline cleanup runs.
- Run on a service with NO `services/<name>/reset.yml` AND no `dir:` field to confirm only the container synthetic phase runs.
- Run on a `required:` service to confirm the warning line appears in the confirm body.
- Run the deploy missing-deps path: enable a service whose `after:` dep is not deployed; trigger from interactive terminal; confirm huh form shows `Proceed`/`Cancel`.

**External system updates:**
- None. No third-party integrations, no consuming projects affected (per CLAUDE.md project status: no external users, no released versions).

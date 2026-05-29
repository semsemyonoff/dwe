# Refactor `internal/core/usercommands/runtime` into Subpackages

## Overview

Split the 13-file + 19-test-file `internal/core/usercommands/runtime` package into a `spec/` contract subpkg and a `runners/` directory containing five per-runner subpackages. Root keeps the `NewRunner` factory, the run-context builders, file-path resolution, confirmation, and notification helpers.

**Problem**: `runner_workflow.go` is 775 lines (with a 290-line `runParallelGroup` chunk); `resolve_files.go` is 526 lines mixing two parallel APIs (probe vs resolve). 7 runner types live in 5 files with their 6 workflow-specific tests scattered at root. Adding a new runner type has no obvious home.

**Goal**: After refactor:
- `runtime/spec/` (2 files): `runner.go` (Runner interface, RunContext, FileProbeResult), `observer.go` (StepStatus, StepResult, WorkflowStepObserver, StepIOSuspender)
- **`runtime/internal/runio/` (1 file): `runio.go`** — shared exported helpers `StdoutOf`, `StderrOf`, `StdinOrOS`, `ParallelChildIO`, `BuildRenderedEnv` (moved from `runner_host.go`/`runner.go`/`parallel_pty.go` — used by every runner package). Placed under `internal/` so only runtime-subpackages can import.
- `runtime/runners/host/` (2 files): `host.go` (Runner), `devbox.go` (DevboxRunner)
- `runtime/runners/service/` (2 files): `exec.go`, `run.go`
- `runtime/runners/script/` (1 file): `script.go`
- `runtime/runners/builtin/` (1 file): `builtin.go` — imports engine builtin as alias `engbuiltin` to avoid same-package collision
- `runtime/runners/workflow/` (7 files): split from runner_workflow.go (775) + parallel_pty.go + workflow_substep_log.go + observer_fire.go (was root observer.go's private helpers) + duplicated `buildWorkflowRegistry` test helper
- Root `runtime/` (~7 files): `runner.go` (NewRunner factory + type aliases from spec AND concrete runners), `build_context.go`, `files_probe.go` + `files_resolve.go` (split from resolve_files.go), `confirmation.go`, `notify.go`, plus `runner_test.go` (consolidated `TestNewRunner_Returns_*` tests + notify_workflow_test.go)

**Behavior changes**: none. External API preserved via type aliases (`type Runner = spec.Runner`).

## Context (from discovery)

- **13 .go + 19 _test.go files** in `internal/core/usercommands/runtime/`.
- **Largest files**: runner_workflow.go (775), resolve_files.go (526), runner_service.go (387), runner.go (315), runner_host.go (285), runner_script.go (220).
- **External callers**: 13 files across `internal/cli/`, `internal/core/execution/pipeline/`, `internal/core/workflow/snapshot/`, `internal/core/validate/checks/`.
- **Internal coupling**:
  - `Runner` interface + `RunContext` struct in `runner.go` — used by all runner types → must extract to `spec/` (cycle-break).
  - `RunContext.StepObserver` field references `WorkflowStepObserver` — observer types belong in `spec/` alongside RunContext.
  - `FileProbeResult` referenced by `resolve_files.go` and external validators → goes to `spec/`.
  - `fireOnStepStart` / `fireOnStepEnd` are workflow-runner private helpers → travel with workflow/ subpkg.
- **`runner_workflow.go` internal structure** (lines):
  - `WorkflowRunner` + `Run` dispatcher — 72-218
  - `runConfirmStep` — 218-253
  - `runCommandStep` — 253-325
  - `isNonInteractive`, `evalWorkflowStepWhen` helpers — 325-340
  - `subResult` + `runParallelGroup` — 340-657 (290 lines! biggest chunk)
  - `dumpSubStepOutput`, `writeParallelSummary`, `evalSubStepOverrideGate` — 658-end
- **`resolve_files.go` internal structure**:
  - Probe API (lines 23-209): `ComputeFilePathsProbe`, `probeFileSpec`, `probeAccessibleFile`, `probePathCandidate`, `probeCandidate`, `probeGlobCandidate`
  - Resolve API (lines 211-526): `ComputeFilePaths`, `resolveFileSpec`, `resolveReadFile`, `resolveWriteFile`, `resolveReadWriteFile`, `resolvePathCandidate`, `resolveCandidate`, `resolveGlobCandidate`, `sortMatches`, `renderPath`, `resolveRelative`, `PrepareFileEffects`

## Development Approach

- **Testing approach**: Regular. 19 existing _test.go files act as safety net. Each runner's tests move to its subpkg.
- **Behavior-preserving**: factory dispatch by `Cmd.Type` string preserved exactly; runner.Run signatures unchanged.
- **Per-task atomicity**: each subpkg = one task; `make test` between.

## Testing Strategy

- **Unit tests**: 19 existing tests redistribute — 12 to subpkg, 7 stay in root (main_test, messages_test, runner_test for factory, testaliases_test, notify_test, notify_workflow_test, build_context_test, confirmation_test, resolve_files_test → split into files_probe_test + files_resolve_test).
- **Workflow tests**: 6 `runner_workflow_*_test.go` files move to `runners/workflow/`.
- **e2e tests**: not applicable.
- **Manual verification (final task)**: trigger each runner kind via `bin/devbox <command>` invocations — host, service exec, script, builtin, workflow.

## Progress Tracking

- Mark completed items with `[x]`.
- ➕ for newly discovered; ⚠️ for blockers.
- Update plan if scope shifts.

## Solution Overview

**Architecture**: spec → runners/* → root factory.

- `spec/` — leaf. Defines `Runner` interface, `RunContext`, observer types, `FileProbeResult`.
- `runners/host`, `runners/service`, `runners/script`, `runners/builtin`, `runners/workflow` — each imports `spec/`, defines its `Runner` type.
- Root `runtime/runner.go` — type aliases (`type Runner = spec.Runner` etc.) + `NewRunner` factory importing all five `runners/*` subpkgs.

**Cycle-break logic**: spec/ holds the contract; subpkg runners satisfy it without importing root; root composes them. No cycles.

**Backward-compat via type aliases**: external callers continue to write `runtime.Runner`, `runtime.RunContext`, `runtime.NewRunner`, `runtime.StepResult`, `runtime.WorkflowStepObserver`, `runtime.BuildRunContext` — root re-exports preserve API.

## Technical Details

### Final structure

```
internal/core/usercommands/runtime/
├── spec/                          (NEW: contract — 2 files)
│   ├── runner.go                  → spec.Runner, spec.RunContext, spec.FileProbeResult
│   └── observer.go                → spec.StepStatus, StepResult, WorkflowStepObserver, StepIOSuspender, fireOnStepStart, fireOnStepEnd
├── runner.go                      (root: NewRunner factory + type aliases — imports spec + all runners/)
├── build_context.go               (root: BuildRunContext, BuildSnapshotRunContext)
├── files_probe.go                 (NEW: split from resolve_files.go — probe API)
├── files_resolve.go               (NEW: split from resolve_files.go — resolve API + PrepareFileEffects)
├── confirmation.go                (root: ConfirmCommand)
├── notify.go                      (root: notify helpers)
└── runners/
    ├── host/                      (NEW: 2 files)
    │   ├── host.go                → host.Runner (was HostRunner)
    │   ├── devbox.go              → host.DevboxRunner (split from runner_host.go)
    │   └── *_test.go
    ├── service/                   (NEW: 2 files)
    │   ├── exec.go                → service.ExecRunner (was ServiceExecRunner)
    │   ├── run.go                 → service.RunRunner (was ServiceRunRunner)
    │   └── *_test.go
    ├── script/                    (NEW: 1 file)
    │   ├── script.go              → script.Runner (was ScriptRunner)
    │   └── *_test.go
    ├── builtin/                   (NEW: 1 file)
    │   ├── builtin.go             → builtin.Runner (was BuiltinRunner)
    │   └── *_test.go
    └── workflow/                  (NEW: 6 files — most complex)
        ├── workflow.go            → workflow.Runner + Run dispatcher
        ├── step.go                → runCommandStep, runConfirmStep, isNonInteractive, evalWorkflowStepWhen
        ├── parallel.go            → subResult, runParallelGroup, writeParallelSummary
        ├── pty.go                 (was parallel_pty.go)
        ├── log.go                 (was workflow_substep_log.go)
        ├── helpers.go             → dumpSubStepOutput, evalSubStepOverrideGate
        └── *_test.go              (6 workflow tests + the parallel/observer/liveui/overrides/guards variants)
```

### Type rename mapping

| Old (root pkg) | New (subpkg) |
|---|---|
| `HostRunner` | `host.Runner` |
| `DevboxRunner` | `host.DevboxRunner` |
| `ServiceExecRunner` | `service.ExecRunner` |
| `ServiceRunRunner` | `service.RunRunner` |
| `ScriptRunner` | `script.Runner` |
| `BuiltinRunner` | `builtin.Runner` |
| `WorkflowRunner` | `workflow.Runner` |

### Root re-exports

```go
package runtime

import (
    "devbox-cli/internal/core/usercommands/runtime/runners/builtin"
    "devbox-cli/internal/core/usercommands/runtime/runners/host"
    "devbox-cli/internal/core/usercommands/runtime/runners/script"
    "devbox-cli/internal/core/usercommands/runtime/runners/service"
    "devbox-cli/internal/core/usercommands/runtime/runners/workflow"
    "devbox-cli/internal/core/usercommands/runtime/spec"
)

// spec contract aliases
type Runner = spec.Runner
type RunContext = spec.RunContext
type FileProbeResult = spec.FileProbeResult
type StepStatus = spec.StepStatus
type StepResult = spec.StepResult
type WorkflowStepObserver = spec.WorkflowStepObserver
type StepIOSuspender = spec.StepIOSuspender

const (
    StepStatusDone    = spec.StepStatusDone
    StepStatusFailed  = spec.StepStatusFailed
    StepStatusSkipped = spec.StepStatusSkipped
)

// concrete runner type aliases — REQUIRED for usercommands.go:138-144
// which currently re-exports HostRunner/DevboxRunner/ScriptRunner/
// ServiceExecRunner/ServiceRunRunner/WorkflowRunner/BuiltinRunner via
// `type X = runtime.X`. These aliases keep that chain intact.
type HostRunner        = host.Runner
type DevboxRunner      = host.DevboxRunner
type ServiceExecRunner = service.ExecRunner
type ServiceRunRunner  = service.RunRunner
type ScriptRunner      = script.Runner
type BuiltinRunner     = builtin.Runner
type WorkflowRunner    = workflow.Runner
```

### Factory in root `runner.go`

```go
import (
    "devbox-cli/internal/core/usercommands/runtime/runners/builtin"
    "devbox-cli/internal/core/usercommands/runtime/runners/host"
    "devbox-cli/internal/core/usercommands/runtime/runners/script"
    "devbox-cli/internal/core/usercommands/runtime/runners/service"
    "devbox-cli/internal/core/usercommands/runtime/runners/workflow"
)

func NewRunner(cmd *model.CommandDef) (spec.Runner, error) {
    switch cmd.Type {
    case "host":     return &host.Runner{}, nil
    case "devbox":   return &host.DevboxRunner{}, nil
    case "service":
        if cmd.Mode == "exec" {
            return &service.ExecRunner{}, nil
        }
        return &service.RunRunner{}, nil
    case "script":   return &script.Runner{}, nil
    case "workflow": return &workflow.Runner{}, nil
    case "builtin":  return &builtin.Runner{}, nil
    }
    return nil, fmt.Errorf("unknown command type: %s", cmd.Type)
}
```

### Resolve naming collisions

**Collision 1: `runners/builtin/` package + engine `execution/builtin/`** — `runner_builtin.go` (the current file, going to `runners/builtin/builtin.go`) imports engine `internal/core/execution/builtin` and calls `builtin.Validate`, `builtin.Run`, `builtin.ExecContext`, `builtin.IsInteractive`, `builtin.CtxUserYAML`, `builtin.CtxInternal`. Once the file's own package is named `builtin`, it cannot import another package also named `builtin` without an alias.

**Resolution**: inside `runners/builtin/builtin.go`:
```go
import (
    engbuiltin "devbox-cli/internal/core/execution/builtin"
)
```
All `builtin.X` call sites in that file become `engbuiltin.X`.

**Collision 2: `runners/workflow/` + filesgate `spec`** — `runner_workflow.go:19` imports `"devbox-cli/internal/core/execution/filesgate/spec"`. Once moved to `runners/workflow/`, the workflow files also import the new `runtime/spec/` — both with import-name `spec`.

**Resolution**: alias the filesgate one:
```go
import (
    fgspec "devbox-cli/internal/core/execution/filesgate/spec"
    "devbox-cli/internal/core/usercommands/runtime/spec"
)
```
All `spec.ResolveRequireIDs` (currently from filesgate) become `fgspec.ResolveRequireIDs`. The runtime `spec.*` references stay unprefixed.

## What Goes Where

- **Implementation Steps**: spec extraction, intra-file splits, per-runner subpkg moves, factory wiring, external caller updates.
- **Post-Completion**: end-to-end CLI smoke per runner type.

## Implementation Steps

### Task 1: Extract `spec/` subpackage (Runner interface + RunContext + observer types)

**Files:**
- Create: `internal/core/usercommands/runtime/spec/runner.go`
- Create: `internal/core/usercommands/runtime/spec/observer.go`
- Modify: `internal/core/usercommands/runtime/runner.go` (remove definitions, add aliases)
- Modify: `internal/core/usercommands/runtime/observer.go` (move public contract types out; keep `fireOnStepStart`/`fireOnStepEnd` until Task 4 moves them to `runners/workflow/`)
- Modify: `internal/core/usercommands/runtime/resolve_files.go` (FileProbeResult → spec.FileProbeResult)

- [ ] create `spec/runner.go`: move `Runner` interface, `RunContext` struct, `FileProbeResult` from `runner.go` and `resolve_files.go`
- [ ] create `spec/observer.go`: move ONLY the public contract — `StepStatus`, `StepResult`, `WorkflowStepObserver`, `StepIOSuspender` — from root `observer.go` to `spec/observer.go`
- [ ] in root `runner.go`: keep `NewRunner` factory (still references runners by current names — they're still in root at this point); add type aliases for spec types
- [ ] keep root `observer.go` (now only `fireOnStepStart`/`fireOnStepEnd` left — they migrate to `runners/workflow/` in Task 4, NOT to `spec/`, because they're WorkflowRunner-private nil-check helpers used only by workflow runner)
- [ ] update root files that referenced `WorkflowStepObserver` etc.: now use the aliased names (no source change since aliases are transparent)
- [ ] update `resolve_files.go`: `FileProbeResult` → `spec.FileProbeResult` (or via alias — pick consistent approach)
- [ ] keep `runner_test.go` / `observer_test.go` parts in root for now (factory tests stay; spec types are tested through their use)
- [ ] run `make test ./internal/core/usercommands/runtime/...` — must pass before Task 2
- [ ] run `make lint` — must pass before Task 2

### Task 2: Intra-file splits — `runner_workflow.go` and `resolve_files.go` (prep before subpkg move)

**Files:**
- Modify: `internal/core/usercommands/runtime/runner_workflow.go` (becomes WorkflowRunner + Run only)
- Create: `internal/core/usercommands/runtime/runner_workflow_step.go`
- Create: `internal/core/usercommands/runtime/runner_workflow_parallel.go`
- Create: `internal/core/usercommands/runtime/runner_workflow_helpers.go`
- Modify: `internal/core/usercommands/runtime/resolve_files.go` (delete or stub)
- Create: `internal/core/usercommands/runtime/files_probe.go`
- Create: `internal/core/usercommands/runtime/files_resolve.go`
- Modify: `internal/core/usercommands/runtime/resolve_files_test.go` (split if convenient)

- [ ] in `runner_workflow.go`: keep `WorkflowRunner` type + main `Run` dispatcher + dispatch helpers (~150 lines)
- [ ] move `runConfirmStep`, `runCommandStep`, `isNonInteractive`, `evalWorkflowStepWhen` to `runner_workflow_step.go` (~110 lines)
- [ ] move `subResult`, `runParallelGroup`, `writeParallelSummary` to `runner_workflow_parallel.go` (~310 lines, biggest chunk)
- [ ] move `dumpSubStepOutput`, `evalSubStepOverrideGate` to `runner_workflow_helpers.go` (~50 lines)
- [ ] in `resolve_files.go`: split into `files_probe.go` (ComputeFilePathsProbe + probe* helpers, ~190 lines) and `files_resolve.go` (ComputeFilePaths + resolve* + PrepareFileEffects, ~320 lines)
- [ ] delete `resolve_files.go` once empty
- [ ] decide on test split — likely keep `runner_workflow_*_test.go` files unchanged (they already cover different aspects)
- [ ] decide on `resolve_files_test.go` split — split into `files_probe_test.go` + `files_resolve_test.go` if tests naturally divide
- [ ] run `make test` — must pass before Task 3
- [ ] run `make lint` — must pass before Task 3

### Task 2.5: Extract `runtime/internal/runio/` (shared I/O helpers)

This is a NEW task between intra-file splits and subpkg extraction. Without it, Task 3 fails to compile mid-task because helpers used by every runner live inside one of the runners.

**Files:**
- Create: `internal/core/usercommands/runtime/internal/runio/runio.go`
- Modify: `runner_host.go` (helpers leave; calls become `runio.X`)
- Modify: `runner.go` (`stdinOrOS` leaves; calls become `runio.StdinOrOS`)
- Modify: `parallel_pty.go` (`parallelChildIO` leaves; calls become `runio.ParallelChildIO`)
- Modify: every runner file that calls these helpers: `confirmation.go`, `runner_builtin.go`, `runner_script.go`, `runner_service.go`, `runner_workflow.go` (+ the step/parallel/helpers files from Task 2)

**Atomic sequence within Task 2.5** (do NOT run `make test` between sub-steps — intermediate states are non-compiling):
- [ ] step 1: create `runtime/internal/runio/runio.go` exporting `StdoutOf(ctx) io.Writer`, `StderrOf(ctx) io.Writer`, `StdinOrOS(ctx) io.Reader`, `ParallelChildIO(...)`, `BuildRenderedEnv(...)` — bodies copied verbatim from `runner_host.go:245,266,274`, `runner.go:125`, `parallel_pty.go`
- [ ] step 2: in each runner file (`runner_host.go`, `confirmation.go`, `runner_builtin.go`, `runner_script.go`, `runner_service.go`, `runner_workflow.go` + the Task 2 split files), replace `stdout(ctx)` → `runio.StdoutOf(ctx)`, `stderr(ctx)` → `runio.StderrOf(ctx)`, `stdinOrOS(ctx)` → `runio.StdinOrOS(ctx)`, `parallelChildIO(...)` → `runio.ParallelChildIO(...)`, `buildRenderedEnv(...)` → `runio.BuildRenderedEnv(...)`
- [ ] step 3: delete the original definitions (`stdout`, `stderr`, `stdinOrOS`, `parallelChildIO`, `buildRenderedEnv`) from `runner_host.go` / `runner.go` / `parallel_pty.go`
- [ ] `internal/runio/` placement under `internal/` ensures only runtime-subpkgs can import it (Go visibility rule — `runners/host/` etc. can import `runtime/internal/runio/` because they share the `runtime/` parent)
- [ ] run `make test` — must pass before Task 3
- [ ] run `make lint` — must pass before Task 3

### Task 3: Extract `runners/host/`, `runners/service/`, `runners/script/`, `runners/builtin/` subpackages

These four runners share the same extraction pattern: move file, change package, import spec, export type, update factory.

**Critical prep: consolidate factory tests BEFORE moving runner test files** — the `TestNewRunner_Returns_*` tests are currently buried inside per-runner test files (not in `runner_test.go`):
- `TestNewRunner_Returns_HostRunner` at `runner_host_test.go:216`
- `TestNewRunner_Returns_ServiceExecRunner` at `runner_host_test.go:227`
- `TestNewRunner_Returns_ServiceRunRunner` at `runner_host_test.go:238`
- `TestNewRunner_Unsupported_Type` at `runner_host_test.go:249`
- `TestNewRunner_Returns_ScriptRunner` at `runner_script_test.go:468`
- `TestNewRunner_Returns_WorkflowRunner` at `runner_workflow_test.go:314`

These reference both `runtime.NewRunner` and concrete types like `*HostRunner` — must move to root `runner_test.go` before per-runner test files migrate, or they break.

**Files:**
- Modify: `runner_test.go` (consume `TestNewRunner_Returns_*` extracted from per-runner test files)
- Modify: `runner_host_test.go` (4 factory tests removed)
- Modify: `runner_script_test.go` (1 factory test removed)
- Modify: `runner_workflow_test.go` (1 factory test removed — to be moved alongside workflow runner tests in Task 4)
- Move + modify: `runner_host.go` → split into `runners/host/host.go` (HostRunner → Runner) + `runners/host/devbox.go` (DevboxRunner)
- Move + modify: `runner_service.go` → split into `runners/service/exec.go` (ServiceExecRunner → ExecRunner) + `runners/service/run.go` (ServiceRunRunner → RunRunner)
- Move + modify: `runner_script.go` → `runners/script/script.go` (ScriptRunner → Runner)
- Move + modify: `runner_builtin.go` → `runners/builtin/builtin.go` (BuiltinRunner → Runner) — uses `engbuiltin` alias
- Move + modify: `runner_host_test.go` → `runners/host/`
- Move + modify: `runner_service_test.go` → `runners/service/`
- Move + modify: `runner_script_test.go` → `runners/script/`
- Move + modify: `runner_builtin_test.go` → `runners/builtin/`
- Modify: root `runner.go` (factory now references subpkg types)

- [ ] **extract factory tests first**: cut `TestNewRunner_Returns_HostRunner`, `TestNewRunner_Returns_ServiceExecRunner`, `TestNewRunner_Returns_ServiceRunRunner`, `TestNewRunner_Unsupported_Type` from `runner_host_test.go`; cut `TestNewRunner_Returns_ScriptRunner` from `runner_script_test.go`; cut `TestNewRunner_Returns_WorkflowRunner` from `runner_workflow_test.go`. Move all to root `runner_test.go`. They keep working because root has the concrete-type aliases (`type HostRunner = host.Runner` etc.).
- [ ] create `runners/` directory + 4 subdirs (host, service, script, builtin)
- [ ] for each runner: move file, change package decl, add `import "devbox-cli/internal/core/usercommands/runtime/spec"`, rename type (HostRunner → Runner, etc.), update `Run` receiver signature to use `spec.RunContext`
- [ ] split runner_host.go into 2 files inside `runners/host/`: host.go (Runner), devbox.go (DevboxRunner)
- [ ] split runner_service.go into 2 files inside `runners/service/`: exec.go (ExecRunner), run.go (RunRunner)
- [ ] **for `runners/builtin/builtin.go`**: add import alias `engbuiltin "devbox-cli/internal/core/execution/builtin"`; rename all call sites of `builtin.Validate`, `builtin.Run`, `builtin.ExecContext`, `builtin.IsInteractive`, `builtin.CtxUserYAML`, `builtin.CtxInternal` to use the `engbuiltin.` prefix (because the file's own package is now `builtin`)
- [ ] move corresponding test files; update package decl + type instantiations (the factory tests moved earlier to root are NOT in these files anymore)
- [ ] update receivers to short single-letter conventional (`r *host.Runner`, `e *service.ExecRunner`, `s *script.Runner`, etc. — match new type initial)
- [ ] update doc comments on each renamed type and its methods to lead with the new exported name — `// HostRunner runs ...` becomes `// Runner runs host commands ...` (revive `exported` rule enforces this)
- [ ] update root `runner.go` factory: import the 4 new subpkgs; switch cases now return `&host.Runner{}`, `&service.ExecRunner{}`, etc.
- [ ] add concrete-type aliases to root `runner.go` per Solution Overview (HostRunner = host.Runner, etc.) — required for usercommands.go:138-144 chain
- [ ] update each runner subpkg's helper calls: `stdout(ctx)` → `runio.StdoutOf(ctx)`, etc. (these were renamed in Task 2.5; here we just verify subpkg files import `runtime/internal/runio`)
- [ ] run `make test` — must pass before Task 4
- [ ] run `make lint` — must pass before Task 4

### Task 4: Extract `runners/workflow/` subpackage (most complex — 6 files)

**Files:**
- Move + modify: `runner_workflow.go` → `runners/workflow/workflow.go`
- Move + modify: `runner_workflow_step.go` → `runners/workflow/step.go`
- Move + modify: `runner_workflow_parallel.go` → `runners/workflow/parallel.go`
- Move + modify: `runner_workflow_helpers.go` → `runners/workflow/helpers.go`
- Move + modify: `parallel_pty.go` → `runners/workflow/pty.go`
- Move + modify: `workflow_substep_log.go` → `runners/workflow/log.go`
- Move + modify: root `observer.go` (the `fireOnStepStart`/`fireOnStepEnd` helpers left there in Task 1) → `runners/workflow/observer_fire.go`
- Move + modify: 6 workflow test files (`runner_workflow_test.go`, `_guards_test.go`, `_liveui_test.go`, `_observer_test.go`, `_overrides_test.go`, `_parallel_test.go`) → `runners/workflow/`
- Move + modify: `notify_workflow_test.go` → `runners/workflow/` (verify it's workflow-specific)
- Modify: root `runner.go` factory

**Atomicity**: workflow files + observer_fire.go + the `buildWorkflowRegistry` test helper move as a single atomic edit (one PR / one working-tree change). Do NOT run `make test` between "move workflow Go files" and "move observer_fire.go" — the workflow subpkg won't compile against `fireOnStepStart` until both edits land.

**`notify_workflow_test.go` decision**: KEEP at root. It calls workflow-runner internals (`installRecordingNotifier` defined in `notify_test.go`, `TestSnapshotRC` from `notify.go`) — all root-package. The root has alias `type WorkflowRunner = workflow.Runner` so `&WorkflowRunner{}` still works from root tests.

**`buildWorkflowRegistry` test helper**: duplicate. The helper (defined in `runner_workflow_test.go:21`, ~20 lines) is used by 7 test files. After Task 4, `runner_workflow_test.go` moves to `runners/workflow/`. **Decision committed: place the root copy in existing `runtime/runner_test.go`** (already present, doesn't require a new file). The subpkg copy lives in `runners/workflow/workflow_test.go` for in-subpkg tests. Test helpers can't be imported across packages — duplication is the standard Go answer.

- [ ] create `runners/workflow/` directory
- [ ] move 6 implementation files; change package to `workflow`; add spec import (use plain `spec` for runtime spec; alias `fgspec` for filesgate spec per Collision 2); rename `WorkflowRunner` → `Runner`
- [ ] update receiver to short single-letter (`r *workflow.Runner`) on `Run` and `runConfirmStep`/`runCommandStep`/`runParallelGroup` methods
- [ ] update doc comments to lead with new exported name (`// Runner executes workflow commands ...`)
- [ ] **atomic step**: in the SAME edit, move `fireOnStepStart` and `fireOnStepEnd` from root `observer.go` to `runners/workflow/observer_fire.go` (they stay package-private — no spec/ export needed; their only callers are workflow Runner internals now co-located)
- [ ] delete root `observer.go` if empty after this move
- [ ] move 6 workflow test files; verify each is truly workflow-specific (not exercising the factory — factory tests already extracted to root in Task 3)
- [ ] **duplicate `buildWorkflowRegistry`**: copy from `runner_workflow_test.go` (now in `runners/workflow/workflow_test.go`) back into root `runtime/runner_test.go` (existing file). Both packages need a same-named test helper since `notify_workflow_test.go` (stays root) and the moved workflow tests both reference it.
- [ ] **keep `notify_workflow_test.go` at root** — it depends on root-package test helpers (`installRecordingNotifier`, `TestSnapshotRC`) AND uses `WorkflowRunner` (now via the root alias). Update internal type references if needed — they likely still compile via aliasing.
- [ ] update root factory: import workflow subpkg; switch case for `"workflow"` returns `&workflow.Runner{}`
- [ ] run `make test` — must pass before Task 5
- [ ] run `make lint` — must pass before Task 5

### Task 5: Verify external callers and integration

**Files:**
- Read-only verification of: `internal/cli/`, `internal/core/execution/pipeline/`, `internal/core/workflow/snapshot/`, `internal/core/validate/checks/`, `internal/core/usercommands/usercommands.go`

- [ ] grep `runtime\\.HostRunner`, `runtime\\.WorkflowRunner`, `runtime\\.ServiceExecRunner`, etc. — none should remain (type aliases unset for these; subpkg types not directly named externally because runners are created via NewRunner)
- [ ] grep `runtime\\.Runner`, `runtime\\.RunContext`, `runtime\\.NewRunner` — these should still compile via aliases
- [ ] grep `runtime\\.StepResult`, `runtime\\.WorkflowStepObserver`, `runtime\\.StepIOSuspender` — should compile via aliases
- [ ] in `internal/core/workflow/snapshot/observer.go` and `exec.go`: verify usage still works (observer types come through type aliases)
- [ ] in `internal/cli/snapshot/liveui.go`: verify observer impl still compiles (StepIOSuspender bridge documented in CLAUDE.md)
- [ ] in `internal/core/execution/pipeline/executor_*_test.go`: verify runtime types still resolve
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`

### Task 6: Build verification + per-runner smoke test

- [ ] run `make build` — produces `bin/devbox`
- [ ] in a fixture project, trigger each runner kind:
  - host runner: `bin/devbox <a user shell command>` from `commands/`
  - service exec: `bin/devbox <a user exec command>` (`type: service`, `mode: exec`)
  - service run: `bin/devbox <a user run command>` (`type: service`, `mode: run`)
  - script: `bin/devbox <a user script command>` (`type: script`)
  - workflow: `bin/devbox <a user workflow command>` (`type: workflow`)
  - builtin: `bin/devbox <a command using type: builtin>` if available in fixture
- [ ] verify workflow with parallel group still runs and shows the parallel summary correctly
- [ ] verify snapshot create (uses workflow runner via observer bridge) still functions end-to-end
- [ ] check `make test-race` if not already in `make test`

### Task 7: Update documentation + finalize

**Files:**
- Modify: `docs/internals/packages.md` (section on `internal/core/usercommands/runtime/`)
- Modify: `AGENTS.md` / `CLAUDE.md` — check for any references to runner type internals
- Move: this plan file → `docs/plans/completed/`

- [ ] update `docs/internals/packages.md` to document the spec/runners layout + factory
- [ ] CLAUDE.md "Live view & state pointers" or similar sections mentioning workflow runner — verify still accurate
- [ ] verify all checkboxes above are `[x]`
- [ ] move plan file: `mkdir -p docs/plans/completed && mv docs/plans/2026-05-29-4-refactor-runtime-subpackages.md docs/plans/completed/`

## Post-Completion

**Manual verification**:
- Workflow with `parallel:` block — visually inspect liveUI rendering during execution.
- Snapshot create + restore (uses workflow runner via observer bridge with depth-counted huh suspends) — confirm interactive prompts still suspend/resume correctly.
- Each runner kind exercised at least once in a fixture project.

**External system updates**: none.

**Refactor series complete**: all 4 plans (builtin, ui, snapshot, runtime) finalized.

# Internal CLI Restructure: Per-Domain Subpackages

## Overview

The completed [layered refactor](completed/2026-05-28-internal-restructure-layered.md) produced `internal/{cli,core,shared}/` and pulled four subpackages out of `cli/` (`cmdctx/`, `deploy/`, `render/`, `service/`). The cli root still holds **~30 flat `.go` files** carrying the bulk of cobra command-tree logic.

This refactor explodes those flat files into **per-domain subpackages** so:

- `internal/cli/root.go` shrinks to a thin composition root (~328 → <100 LoC) that only declares help groups and calls `<pkg>.NewCmd(groupID, flags)`.
- Each cobra command tree lives in its own `cli/<pkg>/` directory with one canonical `<pkg>.go` file hosting `NewCmd`.
- Lifecycle commands (`run`, `stop`, `restart`, `reset`) cluster by Go domain even though they appear as separate top-level cobra commands (rationale: they share the lifecycle semantics — preflight + lock + journal patterns. The `preflightRun` test seam is currently used only by `reset.go`; `stop.go` calls `preflight.Run` directly and `run.go`/`restart.go` route through `deploy.RunHelper`. Harmonizing them on the seam is **out of scope** here — this refactor preserves the existing asymmetry).
- `docker` and `compose` are **separate** micropackages — combining them in one package would force `docker.NewDockerCmd` (a stuttering identifier rejected by Go naming conventions; see *Solution Overview → Naming rationale*).
- Tiny commands (`info`, `version`, `prompt`) follow the same pattern for full uniformity.

**Pre-release status** (per `AGENTS.md`): zero behaviour change, no back-compat shims, fix all call sites inside the same task.

## Context (from discovery)

- **Project**: `devbox-cli`, Go CLI (cobra v2/fang), single binary.
- **Current `internal/cli/` shape**: 4 subpackages (`cmdctx/`, `deploy/`, `render/`, `service/`) + ~30 flat `.go` files.
- **Existing pattern to extend** (already proven in 3 places):
  ```go
  root.AddCommand(cmdService.NewCmd(groupConfiguration, flags))
  root.AddCommand(cmdRender.NewCmd(groupConfiguration, flags))
  ```
- **Discovered during planning**:
  - `internal/cli/shell_status_version_test.go` (1235 LoC) is **entirely commented-out tests** — dead from the prior refactor. Delete in cleanup.
  - `internal/cli/service_cli.go` is misnamed — it contains shell helpers (`resolveShellOptions`, `containerStateStatus`, `dockerExecCLI`, `composeRunCLI`, `runInteractive`), not service code. Rename to `exec.go` and move into `cli/shell/`.
  - `internal/cli/preflight.go` (9 LoC) is a test-seam `var preflightRun = preflight.Run` used only by `reset.go`. Moves with `reset.go` into `cli/lifecycle/`.
  - `internal/cli/run_command_by_id_test.go` tests `devbox command run-by-id`, **not** `devbox run`. Belongs in `cli/command/`, not `cli/lifecycle/`.
  - `internal/cli/completion_install.go` contains **both** `newInstallCompletionCmd` and `newUninstallCompletionCmd` in one file. Split into `install.go` + `uninstall.go` on move.
- **Cross-sibling import check** (no new violations introduced):
  - Existing intentional: `cli/service/` → `cli/deploy.RunHelper` (documented in RunHelper godoc — service-toggle drives deploys after enable/disable). **Leave as-is**; lifting `RunHelper` into `core/workflow/deploy` is a separate PR.
  - `cli/deploy/menu.go` does **not** dispatch into lifecycle — uses package-private `runDeployRunFn`/`runDeployPlanFn` and calls `setup.Run` from `core/workflow/setup`.
- **AGENTS.md rule**: "siblings communicate via `cli/cmdctx/` + `root.go`" — this refactor honours it.

## Development Approach

- **Testing approach**: **Regular (verification-driven)** — mechanical refactor with zero behaviour change. No new test code; existing tests are the gate. Same approach as the parent layered refactor.
- Complete each task fully before moving to the next.
- One package (or one cluster of micro-packages) per task, plus the call-site updates in `root.go`.
- **CRITICAL: every task ends with `go build ./... && make test` passing.** Refactor tasks don't add new tests, but they must not regress the existing suite.
- **CRITICAL: do NOT introduce new cross-sibling `cli/<x>` → `cli/<y>` imports.** If a move surfaces a dependency that can't be routed through `cmdctx/` or `core/`, stop and document with ⚠️.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Maintain backward compatibility is NOT a goal (pre-release).

## Testing Strategy

- **Unit tests**: existing test suite must pass unchanged after each task. **No new test code added** — this is mechanical movement, not feature work. Test files travel alongside their production code.
- **E2E tests**: project has no Playwright/Cypress e2e tests — Go test suite is the integration gate.
- **Per-task verification command**:
  ```bash
  goimports -w . && go build ./... && make test
  test -z "$(gofmt -l .)" || { gofmt -l . ; exit 1; }
  test -z "$(goimports -l .)" || { goimports -l . ; exit 1; }
  ```
  (`gofmt` and `goimports` empty-output checks catch any formatting that slipped through a mid-task `goimports -w .`; they are cheap and reliable.)
- **Final verification command** (after Task 11):
  ```bash
  make build && make test && make lint
  ```

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- If a task surfaces unexpected coupling (e.g., a sibling import the discovery missed), document the cross-import here and decide: fix in place vs. defer to follow-up PR.
- Keep this plan in sync with actual work done.

## Solution Overview

**Target layout** of `internal/cli/` after this refactor (17 subpackages + thin root):

```
internal/cli/
├── root.go              # NewRootCmd: composition only, <100 LoC
├── root_test.go
├── root_resolver_test.go
├── fang_integration_test.go
├── coverage_test.go     # cross-cutting smoke
├── cmdctx/              # (existing — RootFlags, flag helpers)
├── deploy/              # (existing — devbox deploy tree)
├── render/              # (existing — devbox render tree)
├── service/             # (existing — devbox services tree)
├── info/                # NEW
├── version/             # NEW
├── prompt/              # NEW
├── shell/               # NEW (includes service_cli.go → exec.go)
├── status/              # NEW (status.go split into 8 files)
├── snapshot/            # NEW (12 files mostly already split)
├── validate/            # NEW
├── command/             # NEW (command_cmd.go split into 5 files)
├── docs/                # NEW (docs.go split into docs.go + generate.go)
├── completion/          # NEW (install + uninstall + daemon)
├── docker/              # NEW (devbox docker passthrough)
├── compose/             # NEW (devbox compose passthrough)
└── lifecycle/           # NEW (run + stop + restart + reset + preflight test-seam)
```

**Pattern contract** (per subpackage):

| Variant | When | Export |
|---|---|---|
| **Standard** | Single cobra command or single subtree | `NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command` |
| **Multi-export (Go-domain grouping)** | Multiple top-level cobra commands cluster by Go domain (not by cobra hierarchy). **Only `lifecycle/`** after Go-naming review — other multi-command groupings would force stuttering identifiers. | `NewRunCmd`, `NewStopCmd`, etc. (each takes `(groupID, flags)`) |
| **Special: completion** | Subcommands attach to cobra's auto-generated `completion` command | `AttachInstallUninstall(parent *cobra.Command, flags *cmdctx.RootFlags)` |
| **Special: version** | No flags needed | `NewCmd(groupID string) *cobra.Command` |

**Naming rationale (Go conventions review)**:

- `info.NewCmd`, `version.NewCmd`, `prompt.NewCmd`, `shell.NewCmd`, `status.NewCmd`, `snapshot.NewCmd`, `validate.NewCmd`, `command.NewCmd`, `docs.NewCmd`, `docker.NewCmd`, `compose.NewCmd` — all single-export packages. No stutter (`Cmd` is the generic cobra-type suffix, not a repetition of the package name).
- `lifecycle.NewRunCmd` / `NewStopCmd` / `NewRestartCmd` / `NewResetCmd` — multi-export pattern, analogous to `http.NewRequest` / `http.NewServeMux`. Tolerated because the domain coherence (shared `preflightRun` test seam + identical preflight+lock+journal pattern across all four commands) is stronger than the cosmetic cost. Each suffix carries semantic value.
- `docker` and `compose` are **separate packages** (rather than one `docker/` package exporting `NewDockerCmd`/`NewComposeCmd`) because the latter would force the stuttering `docker.NewDockerCmd` — a textbook Go anti-pattern (`http.HTTPClient` shape). Splitting costs one extra directory but keeps every primary command on the symmetric `pkg.NewCmd` contract.
- `command` is **not** renamed to `usercmd`/`usercommand` even though it manages user-defined commands — the package name matches the cobra subcommand it builds (`devbox command`), and `command.NewCmd` does not stutter because `Cmd` ≠ `Command` (`Cmd` is the cobra-type suffix convention).

**File naming inside subpackages**: the file hosting `NewCmd` is named after the package (e.g., `lifecycle/lifecycle.go`, `snapshot/snapshot.go`). Go-idiomatic; avoids "root.go" overloading (`internal/cli/root.go` is THE root). Existing `service/service.go` already follows this; `deploy/root.go` and `render/root.go` rename to `deploy/deploy.go` and `render/render.go` in the cleanup task.

**Root.go changes**:
- Removes the `addCmd(parent, groupID, cmd)` helper (each `NewCmd` sets its own `GroupID`).
- Removes 17 inline `newXxxCmd(flags)` declarations.
- Adds 16 subpackage imports.
- Retains: `NewRootCmd`, `initRootCmd`, `PersistentPreRunE`, `runRoot`, `applyStyles`, `resolveLocalization`, `allowedWithoutProject`, `isValidateCommand`.

## Technical Details

### Per-task file mapping (current flat → target subpackage)

#### Single-command packages

| Subpackage | Source files | Target files | Export |
|---|---|---|---|
| `cli/info/` | `info.go`, `info_test.go` | `info.go`, `info_test.go` | `NewCmd(groupID, flags)` |
| `cli/version/` | `version_cmd.go` | `version.go` | `NewCmd(groupID)` (no flags) |
| `cli/prompt/` | `prompt.go`, `prompt_test.go` | `prompt.go`, `prompt_test.go` | `NewCmd(groupID, flags)` |

#### Single-subtree packages

| Subpackage | Source files | Target files | Notes |
|---|---|---|---|
| `cli/shell/` | `shell.go`, `shell_test.go`, `service_cli.go` | `shell.go`, `exec.go` (was `service_cli.go`), `shell_test.go` | `service_cli.go` is misnamed — actually shell helpers. Watch for `os/user` collisions per AGENTS.md. |
| `cli/status/` | `status.go` (464 LoC), `status_daemons.go`, `status_test.go`, `status_extra_test.go` | `status.go`, `apps.go`, `tools.go`, `infra.go`, `topology.go`, `git.go`, `deploy.go`, `daemons.go` + tests | `loadStatusContext` stays in `status.go`. Split the 464-line monolith by subcommand. |
| `cli/snapshot/` | `snapshot.go` (435 LoC), `snapshot_create.go`, `snapshot_restore.go`, `snapshot_remove.go`, `snapshot_pack.go`, `snapshot_unpack.go`, `snapshot_liveui.go` + 12 `_test.go` | `snapshot.go`, `list.go`, `current.go`, `inspect.go`, `create.go`, `restore.go`, `remove.go`, `pack.go`, `unpack.go`, `liveui.go` + tests | Drop `snapshot_` prefix; `snapshot.go` keeps `NewCmd` + `list`/`current`/`inspect` + shared helpers. |
| `cli/validate/` | `validate.go` (536 LoC), `validate_test.go` | `validate.go`, `validate_test.go` | Keep as one file — further split not warranted. |
| `cli/command/` | `command_cmd.go` (1262 LoC), `command_cmd_test.go` (1339 LoC), `run_command_by_id_test.go` (710 LoC — name misleading, tests `devbox command run-by-id`) | `command.go`, `runbyid.go`, `list.go`, `inspect.go`, `completion.go` + tests | Split `command_cmd.go`: `command.go` (NewCmd), `runbyid.go` (`runCommandByID` + `buildAskFields` + ask helpers + `mergeAnswers` + `stringifyParams`; named `runbyid.go` not `run.go` to disambiguate from `lifecycle/run.go`), `list.go` (`newCommandListCmd` + `buildTreeNodes`/`findGroupNode`/`groupNodeToChildren`/`printTreeNodes`), `inspect.go` (`printInspect`/`printInspectAt`/`inspectStepDescription`), `completion.go` (`registryIDCompletion`). |
| `cli/docs/` | `docs.go` (942 LoC), `docs_cache.go`, `docs_export.go`, `docs_list.go`, `docs_search.go`, `docs_show.go` + tests | `docs.go`, `generate.go`, `cache.go`, `export.go`, `list.go`, `search.go`, `show.go` + tests | Split `docs.go` into `docs.go` (NewCmd + TUI launch + `newDocsCmd`) and `generate.go` (the huge `newDocsGenerateCmd` block: `runDocsGenerate`, `validateDocsFlags`, `resolveFormats`, `resolveScopes`, `genCLIDocs`, `genHiddenCLI*`, `walkAllCommands`, `genCLIIndex`, `writeCLIIndexEntries`, `genRegistryDocs`, `genRegistryMarkdown`, `genCommandsIndex`, `genTopLevelIndex`, `mmdcAvailable`). Drop `docs_` prefix from the rest. |

#### Multi-export package (Go-domain grouping)

| Subpackage | Source files | Target files | Exports |
|---|---|---|---|
| `cli/lifecycle/` | `run.go` + test, `stop.go` + test, `restart.go` + test, `reset.go` (509 LoC) + test (710 LoC), `preflight.go` (9 LoC test seam used by `reset.go` only) | `lifecycle.go` (hosts `var preflightRun = preflight.Run` for `reset.go`), `run.go`, `stop.go`, `restart.go`, `reset.go` + tests | `NewRunCmd`, `NewStopCmd`, `NewRestartCmd`, `NewResetCmd` (each `(groupID, flags)`) |

#### Docker-binary passthroughs (split to avoid stuttering)

| Subpackage | Source files | Target files | Export |
|---|---|---|---|
| `cli/docker/` | `docker.go` (351 LoC) + test | `docker.go` + test | `NewCmd(groupID, flags)` |
| `cli/compose/` | `compose.go` + test | `compose.go` + test | `NewCmd(groupID, flags)` |

#### Special pattern

| Subpackage | Source files | Target files | Export |
|---|---|---|---|
| `cli/completion/` | `completion_install.go` (397 LoC; contains both install + uninstall), `completion_daemon.go`, `completion_test.go`, `completion_install_test.go`, `completion_uninstall_test.go`, `completion_daemon_test.go` | `completion.go` (Attach... + shared types like `completionExitError`, `isSupportedShell`), `install.go` (`newInstallCompletionCmd` + install helpers), `uninstall.go` (`newUninstallCompletionCmd` + `runUninstall` + `emitZshFpathHint`), `daemon.go` (was `completion_daemon.go`) + tests | `AttachInstallUninstall(parent *cobra.Command, flags *cmdctx.RootFlags)` |

`root.go` retains: `root.InitDefaultCompletionCmd()` (it's a method on root cmd, cannot move) + `completion.AttachInstallUninstall(completionCmd, flags)`.

### Files that stay flat at `internal/cli/`

| File | Reason |
|---|---|
| `root.go` | The composition root (~60 LoC after refactor) |
| `root_test.go` | Tests `NewRootCmd` wiring |
| `root_resolver_test.go` | Tests project resolution in `PersistentPreRunE` |
| `fang_integration_test.go` | Cross-cutting fang/cobra integration |
| `coverage_test.go` | Cross-cutting smoke test |

### Files to delete

| File | Reason |
|---|---|
| `shell_status_version_test.go` (1235 LoC) | Entirely commented-out tests; dead from prior refactor. No active code. |
| `internal/cli/preflight.go` | Moved into `cli/lifecycle/lifecycle.go` (Task 10) |
| `internal/cli/service_cli.go` | Moved into `cli/shell/exec.go` (Task 5) |

### `addCmd` helper removal

Currently in `root.go`:
```go
func addCmd(parent *cobra.Command, groupID string, cmd *cobra.Command) {
    cmd.GroupID = groupID
    parent.AddCommand(cmd)
}
```

Used in 17 call sites — all `newXxxCmd(flags)` calls disappear with their subpackages. Helper deletes in Task 11.

### `deploy/root.go` and `render/root.go` rename

Existing subpackages use `root.go` for the file hosting `NewCmd`. The new convention is `<pkg>.go` (matches `service/service.go`). Renamed in Task 11 for symmetry. Pure file rename; no API change.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all mechanical moves, file splits, import updates, root.go shrinkage — fully achievable in this repo.
- **Post-Completion** (no checkboxes): nothing external. Self-contained refactor.

## Implementation Steps

### Task 1: Micropackages — `info` + `version` + `prompt`

Three tiny single-command packages, bundled into one commit because each is trivial.

**Files:**
- Create: `internal/cli/info/info.go`, `internal/cli/info/info_test.go`
- Create: `internal/cli/version/version.go`
- Create: `internal/cli/prompt/prompt.go`, `internal/cli/prompt/prompt_test.go`
- Delete: `internal/cli/info.go`, `internal/cli/info_test.go`, `internal/cli/version_cmd.go`, `internal/cli/prompt.go`, `internal/cli/prompt_test.go`
- Modify: `internal/cli/root.go` (replace `newInfoCmd`/`newVersionCmd`/`newPromptCmd` calls)

- [x] `git mv internal/cli/info.go internal/cli/info/info.go` and `git mv internal/cli/info_test.go internal/cli/info/info_test.go`
- [x] In `info/info.go`: change `package cli` → `package info`, rename `newInfoCmd` → `NewCmd`, add `GroupID: groupID` param; update test file imports
- [x] `git mv internal/cli/version_cmd.go internal/cli/version/version.go`; change package, rename `newVersionCmd` → `NewCmd(groupID string)` (no flags)
- [x] `git mv internal/cli/prompt.go internal/cli/prompt/prompt.go` and `git mv internal/cli/prompt_test.go internal/cli/prompt/prompt_test.go`; change package, rename `newPromptCmd` → `NewCmd`
- [x] Update `internal/cli/root.go`: replace `addCmd(root, groupCore, newInfoCmd(flags))` → `root.AddCommand(info.NewCmd(groupCore, flags))`; same pattern for version + prompt; add imports
- [x] `goimports -w .`
- [x] `go build ./... && make test` — must pass before Task 2
- [x] Commit: `refactor(cli): extract info/version/prompt micropackages`

**Notes:**
- `runInfo` was renamed and exported as `info.Run` because `cli/run.go` and `cli/restart.go` consume it for the `ShowInfo` lifecycle callback. cli (parent) → cli/info (child) import is fine in Task 1; when Task 10 moves run/restart into `cli/lifecycle/`, this becomes a sibling import — documenting here so that's expected, not a violation.
- The internal-helper test `TestPromptAllowedWithoutProject` (which called the unexported `allowedWithoutProject` from `cli` package) was removed; its behaviour remains covered by `TestPromptCmd_OutsideProjectReturnsSilent` and `TestPromptCmd_RunOutsideProject` integration tests.
- The `version` and `prompt` package names collided with `internal/shared/version` and `internal/shared/prompt` imports — aliased as `versioninfo` and `promptpkg` respectively in the new files.

### Task 2: `cli/validate/`

Single-file subtree; isolated dependencies.

**Files:**
- Create: `internal/cli/validate/validate.go`, `internal/cli/validate/validate_test.go`
- Delete: `internal/cli/validate.go`, `internal/cli/validate_test.go`
- Modify: `internal/cli/root.go`

- [x] `git mv internal/cli/validate.go internal/cli/validate/validate.go` and `git mv internal/cli/validate_test.go internal/cli/validate/validate_test.go`
- [x] Change `package cli` → `package validate`; rename `newValidateCmd(groupID, flags)` → `NewCmd(groupID, flags)` (signature already matches)
- [x] Update test file imports; if tests reference unexported helpers from other cli files (e.g. `loadForValidate`), surface findings as ⚠️ — no external references; `loadForValidate`, `errPartialLoad`, `validationFailedError` are all internal to the validate file pair
- [x] Update `internal/cli/root.go`: replace `root.AddCommand(newValidateCmd(groupConfiguration, flags))` → `root.AddCommand(validate.NewCmd(groupConfiguration, flags))` (imported as `cmdValidate` to avoid colliding with `core/validate`)
- [x] `goimports -w .`
- [x] `go build ./... && make test` — must pass before Task 3
- [x] Commit: `refactor(cli): extract validate subpackage`

### Task 3: `cli/docker/` and `cli/compose/` (two micropackages)

Two thin docker-binary passthroughs as **separate packages** — combining them in one `docker/` package would force `docker.NewDockerCmd` (stuttering identifier, Go anti-pattern). One commit covers both since each is trivial.

**Files:**
- Create: `internal/cli/docker/docker.go`, `internal/cli/docker/docker_test.go`
- Create: `internal/cli/compose/compose.go`, `internal/cli/compose/compose_test.go`
- Delete: `internal/cli/docker.go`, `internal/cli/docker_test.go`, `internal/cli/compose.go`, `internal/cli/compose_test.go`
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/docker.go internal/cli/docker/docker.go` (and test)
- [ ] `git mv internal/cli/compose.go internal/cli/compose/compose.go` (and test)
- [ ] Change `package cli` → `package docker` in `cli/docker/*.go`; change `package cli` → `package compose` in `cli/compose/*.go`
- [ ] Rename `newDockerCmd(flags)` → `NewCmd(groupID, flags)`; rename `newComposeCmd(flags)` → `NewCmd(groupID, flags)`
- [ ] Update `internal/cli/root.go`: replace `addCmd(root, groupAdvanced, newDockerCmd(flags))` → `root.AddCommand(docker.NewCmd(groupAdvanced, flags))`; same for compose. Two new imports needed: `"devbox-cli/internal/cli/docker"` and `"devbox-cli/internal/cli/compose"`. Watch for name collisions with shared docker primitives (`shared/docker` is typically imported plain or as `dockerpkg`); alias the cli imports as `cmdDocker`/`cmdCompose` if a collision surfaces.
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 4
- [ ] Commit: `refactor(cli): extract docker and compose as separate subpackages`

### Task 4: `cli/completion/`

Special pattern: `AttachInstallUninstall(parent, flags)`. Split `completion_install.go` into install + uninstall.

**Files:**
- Create: `internal/cli/completion/completion.go`, `internal/cli/completion/install.go`, `internal/cli/completion/uninstall.go`, `internal/cli/completion/daemon.go`, plus `_test.go` siblings
- Delete: `internal/cli/completion_install.go`, `internal/cli/completion_daemon.go`, `internal/cli/completion_test.go`, `internal/cli/completion_install_test.go`, `internal/cli/completion_uninstall_test.go`, `internal/cli/completion_daemon_test.go`
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/completion_daemon.go internal/cli/completion/daemon.go` (and test)
- [ ] `git mv internal/cli/completion_test.go internal/cli/completion/completion_test.go`
- [ ] Split `internal/cli/completion_install.go` (`git mv` to `install.go`, then split out uninstall via Read/Write):
  - `completion.go`: `AttachInstallUninstall(parent *cobra.Command, flags *cmdctx.RootFlags)`, `completionExitError`, `isSupportedShell` (shared)
  - `install.go`: `newInstallCompletionCmd`, `resolveShellName`, `resolveInstallPath`, `completionFileInDir`, `resolvePowerShellInstallPath`, `defaultResolvePowerShellProfile`, `generateCompletionContent`, `atomicWriteCompletion`, `runInstall`, `runInstallDryRun`, `emitShellHints`
  - `uninstall.go`: `newUninstallCompletionCmd`, `runUninstall`, `emitZshFpathHint`
- [ ] `git mv internal/cli/completion_install_test.go internal/cli/completion/install_test.go`; same for uninstall test
- [ ] Change all `package cli` → `package completion`
- [ ] In `completion.go`, implement `AttachInstallUninstall(parent, flags)` that calls `parent.AddCommand(newInstallCompletionCmd())` and `parent.AddCommand(newUninstallCompletionCmd())`
- [ ] Update `internal/cli/root.go`:
  ```go
  root.InitDefaultCompletionCmd()
  if completionCmd, _, err := root.Find([]string{"completion"}); err == nil && completionCmd != nil {
      completionCmd.GroupID = groupAdvanced
      completion.AttachInstallUninstall(completionCmd, flags)
  }
  ```
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 5
- [ ] Commit: `refactor(cli): extract completion subpackage`

### Task 5: `cli/shell/`

Rename `service_cli.go` → `exec.go` (misnomer from prior refactor — they are shell helpers, not service helpers).

**Files:**
- Create: `internal/cli/shell/shell.go`, `internal/cli/shell/shell_test.go`, `internal/cli/shell/exec.go`
- Delete: `internal/cli/shell.go`, `internal/cli/shell_test.go`, `internal/cli/service_cli.go`
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/shell.go internal/cli/shell/shell.go` (and test)
- [ ] `git mv internal/cli/service_cli.go internal/cli/shell/exec.go` (the misnamed file with `resolveShellOptions`, `containerStateStatus`, `dockerExecCLI`, `composeRunCLI`, `runInteractive`, `errContainerNotFound`)
- [ ] Change `package cli` → `package shell` in all 3 files
- [ ] Watch for `os/user` collision: `exec.go` already imports `"os/user"`. After move, no symbol conflict with package name `shell`. No alias needed unless surface changes.
- [ ] Rename `newShellCmd(flags)` → `NewCmd(groupID, flags)`
- [ ] Update `internal/cli/root.go`: `addCmd(root, groupEnvironment, newShellCmd(flags))` → `root.AddCommand(shell.NewCmd(groupEnvironment, flags))`
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 6
- [ ] Commit: `refactor(cli): extract shell subpackage (renames service_cli.go to exec.go)`

### Task 6: `cli/snapshot/`

Largest single-subtree move. 12 source files mostly already split by subcommand; rename to drop `snapshot_` prefix.

**Files:**
- Create: `internal/cli/snapshot/{snapshot,list,current,inspect,create,restore,remove,pack,unpack,liveui}.go` and their `_test.go` siblings
- Delete: `internal/cli/snapshot.go`, `internal/cli/snapshot_create.go`, `internal/cli/snapshot_restore.go`, `internal/cli/snapshot_remove.go`, `internal/cli/snapshot_pack.go`, `internal/cli/snapshot_unpack.go`, `internal/cli/snapshot_liveui.go`, plus `_test.go` siblings (12 files total)
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/snapshot.go internal/cli/snapshot/snapshot.go`
- [ ] `git mv internal/cli/snapshot_create.go internal/cli/snapshot/create.go` (and repeat for restore/remove/pack/unpack/liveui)
- [ ] Same rename for all `_test.go` files: drop `snapshot_` prefix
- [ ] Change `package cli` → `package snapshot` across all 12+ files
- [ ] Rename `newSnapshotCmd(flags)` → `NewCmd(groupID, flags)`; sub-builders (`newSnapshotListCmd`, `newSnapshotInspectCmd`, etc.) keep `newXxx` (unexported, set inside `NewCmd`)
- [ ] Update `internal/cli/root.go`: `addCmd(root, groupPipelines, newSnapshotCmd(flags))` → `root.AddCommand(snapshot.NewCmd(groupPipelines, flags))`
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 7
- [ ] Commit: `refactor(cli): extract snapshot subpackage`

### Task 7: `cli/status/`

Split monolithic `status.go` (464 LoC) into one file per subcommand.

**Files:**
- Create: `internal/cli/status/{status,apps,tools,infra,topology,git,deploy,daemons}.go` + tests
- Delete: `internal/cli/status.go`, `internal/cli/status_daemons.go`, `internal/cli/status_test.go`, `internal/cli/status_extra_test.go`
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/status.go internal/cli/status/status.go`
- [ ] `git mv internal/cli/status_daemons.go internal/cli/status/daemons.go`
- [ ] `git mv internal/cli/status_test.go internal/cli/status/status_test.go`
- [ ] `git mv internal/cli/status_extra_test.go internal/cli/status/status_extra_test.go`
- [ ] Change `package cli` → `package status` in all moved files
- [ ] Use Read/Write to split `status.go` into:
  - `status.go`: `NewCmd(groupID, flags)` (was `newStatusCmd`), `statusContext` type, `loadStatusContext`, `(*statusContext).normalisedDockerCfg`, `(*statusContext).statusInput`, `section` enum + `defaultSectionOrder`, `noSectionFlags` type, `shouldUseTUI`, `renderDefaultStatus`, `renderSection`, `writeNonEmpty`, `trackedServiceCompletion`, test seams (`isTerminalFn`, `runStatusTUIFn`)
  - `apps.go`: `newStatusAppsCmd`
  - `tools.go`: `newStatusToolsCmd`
  - `infra.go`: `newStatusInfraCmd`
  - `topology.go`: `newStatusTopologyCmd`
  - `git.go`: `newStatusGitCmd`
  - `deploy.go`: `newStatusDeployCmd`
- [ ] Rename `newStatusCmd(flags)` → `NewCmd(groupID, flags)`
- [ ] Update `internal/cli/root.go`: `addCmd(root, groupEnvironment, newStatusCmd(flags))` → `root.AddCommand(status.NewCmd(groupEnvironment, flags))`
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 8
- [ ] Commit: `refactor(cli): extract status subpackage with per-subcommand file split`

### Task 8: `cli/command/`

Split `command_cmd.go` (1262 LoC) into 5 files by responsibility.

**Files:**
- Create: `internal/cli/command/{command,run,list,inspect,completion}.go` + tests
- Delete: `internal/cli/command_cmd.go`, `internal/cli/command_cmd_test.go`, `internal/cli/run_command_by_id_test.go`
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/command_cmd.go internal/cli/command/command.go`
- [ ] `git mv internal/cli/command_cmd_test.go internal/cli/command/command_test.go`
- [ ] `git mv internal/cli/run_command_by_id_test.go internal/cli/command/runbyid_test.go` (test for `devbox command run-by-id`; file name disambiguates from `lifecycle/run.go`)
- [ ] Change `package cli` → `package command`
- [ ] Use Read/Write to split `command.go` into:
  - `command.go`: `NewCmd(groupID, flags)` (was `newCommandCmd`), top-level command construction
  - `runbyid.go` (NOT `run.go` — avoids visual collision with `lifecycle/run.go` in sibling packages; the function name `runCommandByID` already encodes the distinction): `runCommandByID`, `printRunHeader`, `buildAskFields`, `widgetToFieldKind`, `optionsToAskOptions`, `joinOptionValues`, `mergeAnswers`, `allRequiredSatisfied`, `stringifyParams`
  - `list.go`: `newCommandListCmd`, `makeBrowserSelector`, `resolveCommandID`, `selectorTitle`, `parseSetFlags`, `buildTreeNodes`, `findGroupNode`, `groupNodeToChildren`, `groupNodeToSingleNode`, `commandDefToTreeNode`, `printTreeNodes`, `printTreeNode`
  - `inspect.go`: `inspectStepDescription`, `printInspect`, `printInspectAt`
  - `completion.go`: `registryIDCompletion`
- [ ] **Test file split decision: not done in this refactor.** `command_test.go` (1339 LoC) stays as one file even though it covers code now in 5 production files. Splitting test files is out of scope; we accept production/test asymmetry here. `go test` builds correctly because all tests live in the same package.
- [ ] Update `internal/cli/root.go`: `addCmd(root, groupAdvanced, newCommandCmd(flags))` → `root.AddCommand(command.NewCmd(groupAdvanced, flags))`
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 9
- [ ] Commit: `refactor(cli): extract command subpackage with file split by responsibility`

### Task 9: `cli/docs/`

Split `docs.go` (942 LoC) into `docs.go` + `generate.go`; rename others to drop `docs_` prefix.

**Files:**
- Create: `internal/cli/docs/{docs,generate,cache,export,list,search,show}.go`, plus test files `internal/cli/docs/{docs,cache,export,list,list_glob,root,show}_test.go`
- Delete:
  - Production: `internal/cli/docs.go`, `internal/cli/docs_cache.go`, `internal/cli/docs_export.go`, `internal/cli/docs_list.go`, `internal/cli/docs_search.go`, `internal/cli/docs_show.go`
  - Tests (all 7): `internal/cli/docs_test.go`, `internal/cli/docs_cache_test.go`, `internal/cli/docs_export_test.go`, `internal/cli/docs_list_test.go`, `internal/cli/docs_list_glob_test.go`, `internal/cli/docs_root_test.go`, `internal/cli/docs_show_test.go`
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/docs.go internal/cli/docs/docs.go`
- [ ] `git mv internal/cli/docs_cache.go internal/cli/docs/cache.go` (repeat for export/list/search/show — drop `docs_` prefix)
- [ ] Rename all 7 `_test.go` files (drop `docs_` prefix): `docs_test.go` → `docs_test.go` (stays — tests for the main `newDocsCmd`), `docs_cache_test.go` → `cache_test.go`, `docs_export_test.go` → `export_test.go`, `docs_list_test.go` → `list_test.go`, `docs_list_glob_test.go` → `list_glob_test.go`, `docs_root_test.go` → `root_test.go` (this one tests `newDocsCmd` root behaviour — placement next to `docs.go` is correct), `docs_show_test.go` → `show_test.go`
- [ ] Change `package cli` → `package docs` across files; watch for collision with `devbox-cli/internal/core/docs` import — keep the import alias as-is (or rename to `coredocs`)
- [ ] Use Read/Write to split `docs.go` into:
  - `docs.go`: `NewCmd(groupID, flags)` (was `newDocsCmd`), `docsFlags` type, `runDocsTUI`
  - `generate.go`: `newDocsGenerateCmd`, `runDocsGenerate`, `validateDocsFlags`, `resolveFormats`, `resolveScopes`, `genCLIDocs`, `genHiddenCLIMarkdown`, `genHiddenCLIYaml`, `genHiddenCLIMan`, `walkAllCommands`, `genCLIIndex`, `writeCLIIndexEntries`, `genRegistryDocs`, `genRegistryMarkdown`, `stepCommandDescription`, `writeCommandMarkdown`, `genCommandsIndex`, `genTopLevelIndex`, `mmdcAvailable`
- [ ] Update `internal/cli/root.go`: `addCmd(root, groupAdvanced, newDocsCmd(flags))` → `root.AddCommand(docs.NewCmd(groupAdvanced, flags))` (alias the import if `docs` name collides with the existing `core/docs` import — likely needs `cmdDocs "devbox-cli/internal/cli/docs"`)
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 10
- [ ] Commit: `refactor(cli): extract docs subpackage with docs.go split into docs+generate`

### Task 10: `cli/lifecycle/` (multi-export)

Four top-level commands (run/stop/restart/reset) + the `preflightRun` test-seam variable.

**Files:**
- Create: `internal/cli/lifecycle/{lifecycle,run,stop,restart,reset}.go` + tests
- Delete: `internal/cli/run.go`, `internal/cli/run_test.go`, `internal/cli/stop.go`, `internal/cli/stop_test.go`, `internal/cli/restart.go`, `internal/cli/restart_test.go`, `internal/cli/reset.go`, `internal/cli/reset_test.go`, `internal/cli/preflight.go`
- Modify: `internal/cli/root.go`

- [ ] `git mv internal/cli/run.go internal/cli/lifecycle/run.go` (and test); same for stop, restart, reset
- [ ] `git mv internal/cli/preflight.go internal/cli/lifecycle/lifecycle.go` (the 9-line file becomes the shared `lifecycle.go` carrier; rename inside if needed)
- [ ] Change `package cli` → `package lifecycle` across all 9 files
- [ ] In `lifecycle.go`, keep `var preflightRun = preflight.Run` as a package-level test seam. **Note**: only `reset.go` calls `preflightRun(...)`; `stop.go` calls `preflight.Run(...)` directly and `run.go`/`restart.go` route through `deploy.RunHelper`. This refactor preserves the existing asymmetry — do NOT redirect stop/run/restart through the seam in this PR.
- [ ] Rename `newRunCmd(flags)` → `NewRunCmd(groupID, flags)`; same for stop/restart/reset
- [ ] Update `internal/cli/root.go`: four `addCmd(...)` calls become four `root.AddCommand(lifecycle.NewXxxCmd(group, flags))` calls (run/restart in groupEnvironment, stop in groupEnvironment, reset in groupPipelines)
- [ ] `goimports -w .`
- [ ] `go build ./... && make test` — must pass before Task 11
- [ ] Commit: `refactor(cli): extract lifecycle subpackage (run/stop/restart/reset)`

### Task 11: Cleanup — root.go shrinkage + existing-subpackage renames + dead file removal

Final tidy pass. After Tasks 1–10, root.go still has `addCmd` helper and 17 imports for now-removed builders. This task removes them, renames the two existing `root.go` files for symmetry, and deletes the dead commented-test file.

**Files:**
- Modify: `internal/cli/root.go`
- Rename: `internal/cli/deploy/root.go` → `internal/cli/deploy/deploy.go`
- Rename: `internal/cli/render/root.go` → `internal/cli/render/render.go`
- Delete: `internal/cli/shell_status_version_test.go` (1235 LoC of commented-out tests; dead)

- [ ] Verify `internal/cli/root.go` is already free of inline `newXxxCmd` declarations (all moved in Tasks 1–10); if any survived (e.g. helpers used only by root), audit and either inline or move
- [ ] Remove the `addCmd` helper function from `internal/cli/root.go` (now unused — every subpackage's `NewCmd` sets its own `GroupID`)
- [ ] `git mv internal/cli/deploy/root.go internal/cli/deploy/deploy.go`
- [ ] `git mv internal/cli/render/root.go internal/cli/render/render.go`
- [ ] `git rm internal/cli/shell_status_version_test.go`
- [ ] Verify `internal/cli/*.go` list contains ONLY: `root.go`, `root_test.go`, `root_resolver_test.go`, `fang_integration_test.go`, `coverage_test.go`
- [ ] Verify `internal/cli/root.go` is <100 LoC and `grep -c 'addCmd(' internal/cli/root.go` returns 0
- [ ] `goimports -w .`
- [ ] `go build ./... && make test && make lint` — all three must pass
- [ ] Commit: `refactor(cli): shrink root.go, rename deploy/render entrypoints, drop dead test file`

### Task 12: Verify acceptance criteria

- [ ] `find internal/cli -maxdepth 1 -type d | sort` shows exactly 18 entries (`internal/cli` itself + 17 subpackages: `cmdctx`, `command`, `completion`, `compose`, `deploy`, `docker`, `docs`, `info`, `lifecycle`, `prompt`, `render`, `service`, `shell`, `snapshot`, `status`, `validate`, `version`)
- [ ] `ls internal/cli/*.go` shows ONLY: `root.go`, `root_test.go`, `root_resolver_test.go`, `fang_integration_test.go`, `coverage_test.go`
- [ ] `wc -l internal/cli/root.go` shows <100 LoC
- [ ] `grep -c 'addCmd(' internal/cli/root.go` returns 0
- [ ] `grep -c 'newInfoCmd\|newVersionCmd\|newPromptCmd\|newRunCmd\|newStopCmd\|newRestartCmd\|newResetCmd\|newShellCmd\|newStatusCmd\|newSnapshotCmd\|newCommandCmd\|newValidateCmd\|newDocsCmd\|newDockerCmd\|newComposeCmd\|newInstallCompletionCmd\|newUninstallCompletionCmd' internal/cli/root.go` returns 0
- [ ] No `nolint:depguard` was added: `grep -rn 'nolint:depguard' --include='*.go' internal/cli/` returns empty
- [ ] No new cross-sibling cli imports: `grep -rn '"devbox-cli/internal/cli/' internal/cli/ --include='*.go' | grep -v '_test.go' | grep -v cmdctx | grep -v 'internal/cli/deploy' | sort -u` returns only the pre-existing `cli/service` → `cli/deploy` line
- [ ] No stuttering identifiers — explicit pattern check, portable across grep variants (no backreferences):
  ```bash
  grep -nE 'docker\.NewDocker|compose\.NewCompose|info\.NewInfo|version\.NewVersion|prompt\.NewPrompt|shell\.NewShell|status\.NewStatus|snapshot\.NewSnapshot|validate\.NewValidate|command\.NewCommand|docs\.NewDocs|lifecycle\.NewLifecycle' internal/cli/root.go
  ```
  must return empty. (Catches any `pkg.New<Pkg>` stutter pattern. `lifecycle.NewRunCmd`/`NewStopCmd` are deliberately allowed multi-export — the regex specifically targets `New<PackageName>` not arbitrary `New<X>`.)
- [ ] Test seams preserved during moves:
  - `grep -c 'preflightRun = preflight.Run' internal/cli/lifecycle/lifecycle.go` returns 1
  - `grep -nE 'isTerminalFn|runStatusTUIFn' internal/cli/status/status.go` returns ≥2 matches
- [ ] `make build && make test && make lint` — all pass

### Task 13: Update AGENTS.md and docs/internals/packages.md

- [ ] Update `AGENTS.md` "Project Structure & Module Organization" `internal/cli/` section to list the 17 new subpackages (replace the brief mention of `cli/`, `cli/cmdctx/`, `cli/deploy/`, `cli/render/`, `cli/service/` with the full list)
- [ ] Update `docs/internals/packages.md`: **replace** the current short `cli/` paragraph in §Per-package responsibilities with a new subsection covering all 17 subpackages (do not append; the existing brief mention of `cli/`, `cli/cmdctx/`, `cli/deploy/`, `cli/render/`, `cli/service/` is now obsolete). Each subpackage gets one short paragraph covering responsibility + the pattern (`NewCmd(groupID, flags)` standard, multi-export only for `lifecycle/`, `AttachInstallUninstall(parent, flags)` for `completion/`); document the Go-naming rationale (why `docker/` and `compose/` are split — see *Naming rationale* in this plan)
- [ ] Verify symlink: `readlink CLAUDE.md` shows `AGENTS.md`
- [ ] `make build` — regenerates embedded `AGENTS.md` and `packages.md`; must succeed
- [ ] `make test` — must pass
- [ ] Commit: `docs(internals): document per-domain cli/ subpackage layout`

### Task 14: Final move-to-completed

- [ ] `mv docs/plans/2026-05-28-cli-restructure-per-domain.md docs/plans/completed/`
- [ ] Commit: `move completed plan: 2026-05-28-cli-restructure-per-domain.md`

## Post-Completion

*This refactor is self-contained — no external systems, no manual verification needed beyond `make build && make test && make lint` passing.*

**Follow-up PRs explicitly out of scope here:**

- Lift `RunHelper` from `cli/deploy/` into `core/workflow/deploy/` to resolve the existing `cli/service/` → `cli/deploy/` cross-import (would let depguard rule "no sibling cli imports" engage).
- Add depguard rule preventing future `cli/<x>` → `cli/<y>` cross-imports after the RunHelper move.
- Move low-level docker subprocess helpers (`dockerExecCLI`, `composeRunCLI`, `runInteractive`) from `cli/shell/exec.go` into `shared/docker/` if a second consumer emerges.

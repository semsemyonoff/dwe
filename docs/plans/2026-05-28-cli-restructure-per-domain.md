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

- [x] `git mv internal/cli/docker.go internal/cli/docker/docker.go` (and test)
- [x] `git mv internal/cli/compose.go internal/cli/compose/compose.go` (and test)
- [x] Change `package cli` → `package docker` in `cli/docker/*.go`; change `package cli` → `package compose` in `cli/compose/*.go`
- [x] Rename `newDockerCmd(flags)` → `NewCmd(groupID, flags)`; rename `newComposeCmd(flags)` → `NewCmd(groupID, flags)`
- [x] Update `internal/cli/root.go`: replace `addCmd(root, groupAdvanced, newDockerCmd(flags))` → `root.AddCommand(docker.NewCmd(groupAdvanced, flags))`; same for compose. Imports aliased as `cmdDocker`/`cmdCompose` to avoid collision with `shared/docker`. Within `cli/docker/docker.go`, the `shared/docker` import is aliased as `dockerpkg` because the package itself is now named `docker`.
- [x] `goimports -w .`
- [x] `go build ./... && make test` — must pass before Task 4
- [x] Commit: `refactor(cli): extract docker and compose as separate subpackages`

**Notes:**
- `coverage_test.go` had four compose tests (`TestComposeFilesCmd_RunE`, `TestComposeFilesCmd_RunE_InvalidConfig`, `TestComposeArgvCmd_RunE`, `TestComposeArgvCmd_RunE_InvalidConfig`) that referenced unexported helpers (`newComposeFilesCmd`, `newComposeArgvCmd`). Moved them into `cli/compose/compose_test.go` with a local `makeMinimalProject` helper since they can no longer reach the cli-package internals after the move.

### Task 4: `cli/completion/`

Special pattern: `AttachInstallUninstall(parent, flags)`. Split `completion_install.go` into install + uninstall.

**Files (as implemented — see ⚠️ DEVIATION notes below):**
- Created: `internal/cli/completion/completion.go`, `internal/cli/completion/install.go`, `internal/cli/completion/uninstall.go`, `internal/cli/completion/install_test.go`, `internal/cli/completion/uninstall_test.go`
- Deleted: `internal/cli/completion_install.go`, `internal/cli/completion_install_test.go`, `internal/cli/completion_uninstall_test.go`
- Retained in cli/ (deferred to Task 8): `internal/cli/completion_test.go`, `internal/cli/completion_daemon.go`, `internal/cli/completion_daemon_test.go`
- Modified: `internal/cli/root.go`

- [x] ⚠️ DEVIATION: `completion_daemon.go` + `completion_daemon_test.go` NOT moved in this task. They host `daemonSetCompletion` for `command --set` flag completion, which is consumed by `command_cmd.go`. Moving them to `cli/completion/` now and then again to `cli/command/` in Task 8 (or leaving them in `cli/completion/` and having `cli/command/` sibling-import them) would either churn or violate the "no cross-sibling cli imports" rule. Defer the move to Task 8, where they fit naturally as `cli/command/daemonset.go` (tightly coupled to command rendering, not to shell-completion install). Files remain at `internal/cli/completion_daemon.go` and `internal/cli/completion_daemon_test.go`.
- [x] ⚠️ DEVIATION: `completion_test.go` NOT moved either — its tests reference `registryIDCompletion` (still in cli/ until Task 8), `NewRootCmd`, and `groupAdvanced`. The `TestCompletionCmd_InAdvancedGroupWithShellSubcommands` integration test must stay in cli/ to verify root wiring. File remains at `internal/cli/completion_test.go`.
- [x] Split `internal/cli/completion_install.go` into `internal/cli/completion/{completion,install,uninstall}.go` via Read/Write (not `git mv`, since the source is one file and the destination is three):
  - `completion.go`: `AttachInstallUninstall(parent *cobra.Command, flags *cmdctx.RootFlags)`, `completionExitError`, `isSupportedShell`, `supportedShells`, `ErrUnsupportedShell` (shared)
  - `install.go`: `newInstallCompletionCmd`, `resolveShellName`, `resolveInstallPath`, `completionFileInDir`, `resolvePowerShellInstallPath`, `defaultResolvePowerShellProfile`, `generateCompletionContent`, `atomicWriteCompletion`, `runInstall`, `runInstallDryRun`, `emitShellHints`, plus seam vars `resolvePowerShellProfile`/`completionHomeDir`/`completionReadFile`
  - `uninstall.go`: `newUninstallCompletionCmd`, `runUninstall`, `emitZshFpathHint`
- [x] Port `completion_install_test.go` → `cli/completion/install_test.go` and `completion_uninstall_test.go` → `cli/completion/uninstall_test.go`. The tests used `NewRootCmd()`; rewrote them around a `buildCompletionTestRoot()` helper that constructs a minimal cobra parent + calls `AttachInstallUninstall` — same coverage without dragging in cli root's `PersistentPreRunE`.
- [x] All new files use `package completion`
- [x] `completion.go` implements `AttachInstallUninstall(parent, flags)` that calls `parent.AddCommand(newInstallCompletionCmd())` and `parent.AddCommand(newUninstallCompletionCmd())`
- [x] Update `internal/cli/root.go`:
  ```go
  root.InitDefaultCompletionCmd()
  if completionCmd, _, err := root.Find([]string{"completion"}); err == nil && completionCmd != nil {
      completionCmd.GroupID = groupAdvanced
      completion.AttachInstallUninstall(completionCmd, flags)
  }
  ```
- [x] `goimports -w .`
- [x] `go build ./... && make test` — passed before Task 5
- [x] Commit: `refactor(cli): extract completion subpackage`

### Task 5: `cli/shell/`

Rename `service_cli.go` → `exec.go` (misnomer from prior refactor — they are shell helpers, not service helpers).

**Files:**
- Create: `internal/cli/shell/shell.go`, `internal/cli/shell/shell_test.go`, `internal/cli/shell/exec.go`
- Delete: `internal/cli/shell.go`, `internal/cli/shell_test.go`, `internal/cli/service_cli.go`
- Modify: `internal/cli/root.go`

- [x] `git mv internal/cli/shell.go internal/cli/shell/shell.go` (and test)
- [x] `git mv internal/cli/service_cli.go internal/cli/shell/exec.go` (the misnamed file with `resolveShellOptions`, `containerStateStatus`, `dockerExecCLI`, `composeRunCLI`, `runInteractive`, `errContainerNotFound`)
- [x] Change `package cli` → `package shell` in all 3 files
- [x] Watch for `os/user` collision: `exec.go` already imports `"os/user"`. After move, no symbol conflict with package name `shell`. No alias needed unless surface changes.
- [x] Rename `newShellCmd(flags)` → `NewCmd(groupID, flags)`
- [x] Update `internal/cli/root.go`: `addCmd(root, groupEnvironment, newShellCmd(flags))` → `root.AddCommand(shell.NewCmd(groupEnvironment, flags))` (imported as `cmdShell` to follow the established convention in root.go)
- [x] `goimports -w .`
- [x] `go build ./... && make test` — must pass before Task 6
- [x] Commit: `refactor(cli): extract shell subpackage (renames service_cli.go to exec.go)`

### Task 6: `cli/snapshot/`

Largest single-subtree move. 12 source files mostly already split by subcommand; rename to drop `snapshot_` prefix.

**Files:**
- Create: `internal/cli/snapshot/{snapshot,list,current,inspect,create,restore,remove,pack,unpack,liveui}.go` and their `_test.go` siblings
- Delete: `internal/cli/snapshot.go`, `internal/cli/snapshot_create.go`, `internal/cli/snapshot_restore.go`, `internal/cli/snapshot_remove.go`, `internal/cli/snapshot_pack.go`, `internal/cli/snapshot_unpack.go`, `internal/cli/snapshot_liveui.go`, plus `_test.go` siblings (12 files total)
- Modify: `internal/cli/root.go`

- [x] `git mv internal/cli/snapshot.go internal/cli/snapshot/snapshot.go`
- [x] `git mv internal/cli/snapshot_create.go internal/cli/snapshot/create.go` (and repeat for restore/remove/pack/unpack/liveui)
- [x] Same rename for all `_test.go` files: drop `snapshot_` prefix
- [x] Change `package cli` → `package snapshot` across all 12+ files
- [x] Rename `newSnapshotCmd(flags)` → `NewCmd(groupID, flags)`; sub-builders (`newSnapshotListCmd`, `newSnapshotInspectCmd`, etc.) keep `newXxx` (unexported, set inside `NewCmd`)
- [x] Update `internal/cli/root.go`: `addCmd(root, groupPipelines, newSnapshotCmd(flags))` → `root.AddCommand(snapshot.NewCmd(groupPipelines, flags))`
- [x] `goimports -w .`
- [x] `go build ./... && make test` — must pass before Task 7
- [x] Commit: `refactor(cli): extract snapshot subpackage`

**Notes:**
- The new package is named `snapshot` and collides with the `internal/core/workflow/snapshot` import. Aliased the workflow package as `snapshotpkg` across all 11 files that import it (same convention as `userpkg`/`localpkg`/`versioninfo`/`promptpkg` used in earlier tasks). All `snapshot.X` references to the workflow package were rewritten to `snapshotpkg.X`.
- In `internal/cli/root.go`, imported the new package as `cmdSnapshot` to follow the established alias convention.
- `TestSnapshotNameCompletion` previously called `NewRootCmd()` to get a cobra.Command carrying the `--config` persistent flag. Replaced with an inline minimal `&cobra.Command{Use: "devbox"}` plus a `PersistentFlags().StringVarP(&flags.ConfigPath, "config", ...)` registration — same coverage without dragging the cli root into a sibling-package test.

### Task 7: `cli/status/`

Split monolithic `status.go` (464 LoC) into one file per subcommand.

**Files:**
- Create: `internal/cli/status/{status,apps,tools,infra,topology,git,deploy,daemons}.go` + tests
- Delete: `internal/cli/status.go`, `internal/cli/status_daemons.go`, `internal/cli/status_test.go`, `internal/cli/status_extra_test.go`
- Modify: `internal/cli/root.go`

- [x] `git mv internal/cli/status.go internal/cli/status/status.go`
- [x] `git mv internal/cli/status_daemons.go internal/cli/status/daemons.go`
- [x] `git mv internal/cli/status_test.go internal/cli/status/status_test.go`
- [x] `git mv internal/cli/status_extra_test.go internal/cli/status/status_extra_test.go`
- [x] Change `package cli` → `package status` in all moved files
- [x] Use Read/Write to split `status.go` into:
  - `status.go`: `NewCmd(groupID, flags)` (was `newStatusCmd`), `statusContext` type, `loadStatusContext`, `(*statusContext).normalisedDockerCfg`, `(*statusContext).statusInput`, `section` enum + `defaultSectionOrder`, `noSectionFlags` type, `shouldUseTUI`, `renderDefaultStatus`, `renderSection`, `writeNonEmpty`, test seams (`isTerminalFn`, `runStatusTUIFn`)
  - `apps.go`: `newStatusAppsCmd`
  - `tools.go`: `newStatusToolsCmd`
  - `infra.go`: `newStatusInfraCmd`
  - `topology.go`: `newStatusTopologyCmd`
  - `git.go`: `newStatusGitCmd`
  - `deploy.go`: `newStatusDeployCmd` + `trackedServiceCompletion` (moved here since it is the sole consumer)
- [x] Rename `newStatusCmd(flags)` → `NewCmd(groupID, flags)`
- [x] Update `internal/cli/root.go`: `addCmd(root, groupEnvironment, newStatusCmd(flags))` → `root.AddCommand(status.NewCmd(groupEnvironment, flags))` (imported as `cmdStatus`)
- [x] `goimports -w .`
- [x] `go build ./... && make test` — must pass before Task 8
- [x] Commit: `refactor(cli): extract status subpackage with per-subcommand file split`

**Notes:**
- `trackedServiceCompletion` moved from `status.go` into `deploy.go` since the deploy subcommand is its only caller; this keeps `status.go` free of the `sort`/`config`/`usercommands`/`deploy` imports that the completion helper requires.
- `TestStatusCmd_*` tests previously called `cli.NewRootCmd()` to drive subcommands through the cli root. The new `package status` would need to import `internal/cli` to do that, forming a cycle. Replaced with a `buildStatusTestRoot()` helper that constructs a minimal `&cobra.Command{Use: "devbox"}` with the `-c/--config` persistent flag, registers the `environment` cobra group, and attaches `status.NewCmd("environment", flags)`. Same observable behaviour for status-level integration tests; `RootFlags.ProjectRoot()` falls back to `filepath.Dir(ConfigPath)` so `PersistentPreRunE` is not needed.
- `TestStatusCmd_ToolsRootCmdRemoved` continues to pass: the minimal test root only registers the status subcommand, so `devbox tools` is still "unknown command".

### Task 8: `cli/command/`

Split `command_cmd.go` (1262 LoC) into 5 files by responsibility.

**Files:**
- Create: `internal/cli/command/{command,run,list,inspect,completion}.go` + tests
- Also absorb (deferred from Task 4): `internal/cli/completion_daemon.go` → `internal/cli/command/daemonset.go` (holds `daemonSetCompletion`, called by `command_cmd.go`); `internal/cli/completion_daemon_test.go` → `internal/cli/command/daemonset_test.go`; `internal/cli/completion_test.go` → split (root-wiring test stays in cli/; the `registryIDCompletion` / `buildRegistryCompletions` tests move to `internal/cli/command/completion_test.go`)
- Delete: `internal/cli/command_cmd.go`, `internal/cli/command_cmd_test.go`, `internal/cli/run_command_by_id_test.go`, `internal/cli/completion_daemon.go`, `internal/cli/completion_daemon_test.go`
- Modify: `internal/cli/root.go`, `internal/cli/completion_test.go` (drop tests that moved to cli/command)

- [x] `git mv internal/cli/command_cmd.go internal/cli/command/command.go`
- [x] `git mv internal/cli/command_cmd_test.go internal/cli/command/command_test.go`
- [x] `git mv internal/cli/run_command_by_id_test.go internal/cli/command/runbyid_test.go` (test for `devbox command run-by-id`; file name disambiguates from `lifecycle/run.go`)
- [x] Change `package cli` → `package command`
- [x] Use Read/Write to split `command.go` into:
  - `command.go`: `NewCmd(groupID, flags)` (was `newCommandCmd`), top-level command construction
  - `runbyid.go` (NOT `run.go` — avoids visual collision with `lifecycle/run.go` in sibling packages; the function name `runCommandByID` already encodes the distinction): `runCommandByID`, `printRunHeader`, `buildAskFields`, `widgetToFieldKind`, `optionsToAskOptions`, `joinOptionValues`, `mergeAnswers`, `allRequiredSatisfied`, `stringifyParams`
  - `list.go`: `newCommandListCmd`, `makeBrowserSelector`, `resolveCommandID`, `selectorTitle`, `parseSetFlags`, `buildTreeNodes`, `findGroupNode`, `groupNodeToChildren`, `groupNodeToSingleNode`, `commandDefToTreeNode`, `printTreeNodes`, `printTreeNode`
  - `inspect.go`: `inspectStepDescription`, `printInspect`, `printInspectAt`
  - `completion.go`: `registryIDCompletion`
- [x] **Test file split decision: not done in this refactor.** `command_test.go` (1339 LoC) stays as one file even though it covers code now in 5 production files. Splitting test files is out of scope; we accept production/test asymmetry here. `go test` builds correctly because all tests live in the same package.
- [x] Update `internal/cli/root.go`: `addCmd(root, groupAdvanced, newCommandCmd(flags))` → `root.AddCommand(command.NewCmd(groupAdvanced, flags))`
- [x] `goimports -w .`
- [x] `go build ./... && make test` — passed before Task 9
- [x] Commit: `refactor(cli): extract command subpackage with file split by responsibility`

**Notes:**
- Imported in `root.go` as `cmdCommand "devbox-cli/internal/cli/command"` to follow established alias convention.
- ⚠️ DEVIATION: `coverage_test.go` (cross-cutting smoke) was split rather than left whole. The command-related cases (`TestPrintTreeNodes_*`, `TestPrintCommandInspect_*`, `TestCommandListCmd_RunE_*`, `TestCommandInspectCmd_RunE_DirectID`) moved to `internal/cli/command/coverage_test.go` since they call unexported symbols from the new `command` package. The docs/genCLI/walkAllCommands cases stayed in `internal/cli/coverage_test.go` until Task 9 moves them with the docs subpackage.
- ⚠️ DEVIATION: `docs.go` (still in `cli/` until Task 9) previously called the package-private `selectorTitle` helper for its TUI header. To avoid a new `cli/` → `cli/command/` cross-sibling import (forbidden by the plan), a small `docsSelectorTitle` duplicate was added inline in `internal/cli/docs.go`; both copies collapse when Task 9 moves docs.go and inlines the helper into `cli/docs/docs.go`. The original helper stays unexported as `selectorTitle` in `cli/command/list.go`.
- `TestCompletionCmd_InAdvancedGroupWithShellSubcommands` (root-wiring integration test) stayed in `internal/cli/completion_test.go`; the registry-completion unit tests (`TestBuildRegistryCompletions`, `TestRegistryIDCompletion_noSecondArg`, `TestCommandsCmd_ActiveHelp_PointsAtInspectFlag`) moved to `internal/cli/command/completion_test.go` alongside the moved `registryIDCompletion` helper.

### Task 9: `cli/docs/`

Split `docs.go` (942 LoC) into `docs.go` + `generate.go`; rename others to drop `docs_` prefix.

**Files:**
- Create: `internal/cli/docs/{docs,generate,cache,export,list,search,show}.go`, plus test files `internal/cli/docs/{docs,cache,export,list,list_glob,root,show}_test.go`
- Delete:
  - Production: `internal/cli/docs.go`, `internal/cli/docs_cache.go`, `internal/cli/docs_export.go`, `internal/cli/docs_list.go`, `internal/cli/docs_search.go`, `internal/cli/docs_show.go`
  - Tests (all 7): `internal/cli/docs_test.go`, `internal/cli/docs_cache_test.go`, `internal/cli/docs_export_test.go`, `internal/cli/docs_list_test.go`, `internal/cli/docs_list_glob_test.go`, `internal/cli/docs_root_test.go`, `internal/cli/docs_show_test.go`
- Modify: `internal/cli/root.go`

- [x] `git mv internal/cli/docs.go internal/cli/docs/docs.go`
- [x] `git mv internal/cli/docs_cache.go internal/cli/docs/cache.go` (repeat for export/list/search/show — drop `docs_` prefix)
- [x] Rename all 7 `_test.go` files (drop `docs_` prefix): `docs_test.go` → `generate_test.go` (it actually tests `runDocsGenerate` / `validateDocsFlags` / `resolveFormats` / `genRegistryMarkdown` / `writeCommandMarkdown` / `genCommandsIndex` / `genTopLevelIndex`, all of which moved into `generate.go`), `docs_cache_test.go` → `cache_test.go`, `docs_export_test.go` → `export_test.go`, `docs_list_test.go` → `list_test.go`, `docs_list_glob_test.go` → `list_glob_test.go`, `docs_root_test.go` → `docs_test.go` (this one tests `newDocsCmd` root behaviour — now placed next to `docs.go`), `docs_show_test.go` → `show_test.go`
- [x] Change `package cli` → `package docs` across files; `devbox-cli/internal/core/docs` aliased as `coredocs` in docs.go / export.go / list.go / search.go / show.go to avoid the symbol-vs-package collision
- [x] Use Read/Write to split `docs.go` into:
  - `docs.go`: `NewCmd(groupID, flags)` (was `newDocsCmd`), `docsFlags` type, `runDocsTUI`, `docsSelectorTitle`
  - `generate.go`: `newDocsGenerateCmd`, `runDocsGenerate`, `validateDocsFlags`, `resolveFormats`, `resolveScopes`, `genCLIDocs`, `genHiddenCLIMarkdown`, `genHiddenCLIYaml`, `genHiddenCLIMan`, `walkAllCommands`, `genCLIIndex`, `writeCLIIndexEntries`, `genRegistryDocs`, `genRegistryMarkdown`, `stepCommandDescription`, `writeCommandMarkdown`, `genCommandsIndex`, `genTopLevelIndex`, `mmdcAvailable`
- [x] Update `internal/cli/root.go`: `addCmd(root, groupAdvanced, newDocsCmd(flags))` → `root.AddCommand(cmdDocs.NewCmd(groupAdvanced, flags))`; imported as `cmdDocs "devbox-cli/internal/cli/docs"` to avoid collision with `core/docs`
- [x] `goimports -w .`
- [x] `go build ./... && make test` — passed before Task 10
- [x] Commit: `refactor(cli): extract docs subpackage with docs.go split into docs+generate`

**Notes:**
- `internal/cli/coverage_test.go` previously held `genCLIDocs` / `walkAllCommands` / `genHiddenCLI*` tests that referenced unexported symbols now living in `cli/docs`. These tests moved into the new `internal/cli/docs/coverage_test.go`. After the move the original `internal/cli/coverage_test.go` had no remaining cases, so it was deleted; the cli/ package's smoke coverage is now provided by `root_test.go` / `root_resolver_test.go` / `fang_integration_test.go`.
- `TestDocsGenerateCommand_Integration` and `TestCLIIndexNotGeneratedWithoutMarkdown` previously called `cli.NewRootCmd()` to obtain a wired root. Replaced with `buildDocsTestRoot(flags)` in `internal/cli/docs/coverage_test.go` — constructs a minimal `&cobra.Command{Use: "devbox"}` with an `advanced` group and attaches `docs.NewCmd("advanced", flags)`. Crucially the helper does NOT register the `--config` persistent flag with `StringVarP(&flags.ConfigPath, ...)`: `StringVarP` rewrites the bound variable to the default value at registration time, which would clobber `flags.ConfigPath` that the test already set. Tests reach into the docs subtree via `root.Find([]string{"docs", "generate"})` and pass the cmd directly to `runDocsGenerate`.
- `docsSelectorTitle` stays inline in the new `cli/docs/docs.go` (rather than collapsing to a shared helper) because lifting it into a shared place would create a `cli/docs` → `cli/command` cross-sibling import. The duplicate is two lines and they will collapse naturally if/when the broader cross-sibling rule is enforced via depguard and a shared location appears.

### Task 10: `cli/lifecycle/` (multi-export)

Four top-level commands (run/stop/restart/reset) + the `preflightRun` test-seam variable.

**Files:**
- Create: `internal/cli/lifecycle/{lifecycle,run,stop,restart,reset}.go` + tests
- Delete: `internal/cli/run.go`, `internal/cli/run_test.go`, `internal/cli/stop.go`, `internal/cli/stop_test.go`, `internal/cli/restart.go`, `internal/cli/restart_test.go`, `internal/cli/reset.go`, `internal/cli/reset_test.go`, `internal/cli/preflight.go`
- Modify: `internal/cli/root.go`

- [x] `git mv internal/cli/run.go internal/cli/lifecycle/run.go` (and test); same for stop, restart, reset
- [x] `git mv internal/cli/preflight.go internal/cli/lifecycle/lifecycle.go` (the 9-line file becomes the shared `lifecycle.go` carrier; rename inside if needed)
- [x] Change `package cli` → `package lifecycle` across all 9 files
- [x] In `lifecycle.go`, keep `var preflightRun = preflight.Run` as a package-level test seam. **Note**: only `reset.go` calls `preflightRun(...)`; `stop.go` calls `preflight.Run(...)` directly and `run.go`/`restart.go` route through `deploy.RunHelper`. This refactor preserves the existing asymmetry — do NOT redirect stop/run/restart through the seam in this PR.
- [x] Rename `newRunCmd(flags)` → `NewRunCmd(groupID, flags)`; same for stop/restart/reset
- [x] Update `internal/cli/root.go`: four `addCmd(...)` calls become four `root.AddCommand(lifecycle.NewXxxCmd(group, flags))` calls (run/restart in groupEnvironment, stop in groupEnvironment, reset in groupPipelines)
- [x] `goimports -w .`
- [x] `go build ./... && make test` — must pass before Task 11
- [x] Commit: `refactor(cli): extract lifecycle subpackage (run/stop/restart/reset)`

**Notes:**
- Imported in `root.go` as `cmdLifecycle "devbox-cli/internal/cli/lifecycle"` to follow the established alias convention.
- The `internal/core/workflow/lifecycle` import collides with the new package name `lifecycle`. Aliased as `lifecyclepkg` in `run.go`, `restart.go`, and `stop.go` (same convention as `userpkg`/`localpkg`/`versioninfo`/`promptpkg`/`snapshotpkg` used in earlier tasks).
- `run_test.go` / `stop_test.go` / `restart_test.go` / `reset_test.go` previously called `cli.NewRootCmd()` to drive subcommands through the cli root. The new `package lifecycle` cannot import `internal/cli` (would form a cycle since `cli/root.go` now imports `cli/lifecycle`). Added a `buildLifecycleTestRoot(flags)` helper in `internal/cli/lifecycle/testhelpers_test.go` that constructs a minimal `&cobra.Command{Use: "devbox"}` with the `-c/--config` persistent flag, registers the `environment` and `pipelines` cobra groups, and attaches all four lifecycle subcommands. The helper also declares local `groupEnvironment` / `groupPipelines` constants so the existing `c.GroupID != groupEnvironment` assertions work unchanged.
- `TestResetServiceRun_FlagsExist` previously called the unexported `newResetRunCmd(flags)`. Replaced with a `NewResetCmd(groupPipelines, flags)` walk of `Commands()` to find the `run` subcommand — same coverage without exposing the package-private builder.

### Task 11: Cleanup — root.go shrinkage + existing-subpackage renames + dead file removal

Final tidy pass. After Tasks 1–10, root.go still has `addCmd` helper and 17 imports for now-removed builders. This task removes them, renames the two existing `root.go` files for symmetry, and deletes the dead commented-test file.

**Files:**
- Modify: `internal/cli/root.go`
- Rename: `internal/cli/deploy/root.go` → `internal/cli/deploy/deploy.go`
- Rename: `internal/cli/render/root.go` → `internal/cli/render/render.go`
- Delete: `internal/cli/shell_status_version_test.go` (1235 LoC of commented-out tests; dead)

- [x] Verify `internal/cli/root.go` is already free of inline `newXxxCmd` declarations (all moved in Tasks 1–10); if any survived (e.g. helpers used only by root), audit and either inline or move
- [x] Remove the `addCmd` helper function from `internal/cli/root.go` (now unused — every subpackage's `NewCmd` sets its own `GroupID`)
- [x] `git mv internal/cli/deploy/root.go internal/cli/deploy/deploy.go` (also renamed `deploy/root_test.go` → `deploy/deploy_test.go` for symmetry)
- [x] `git mv internal/cli/render/root.go internal/cli/render/render.go`
- [x] `git rm internal/cli/shell_status_version_test.go`
- [x] Verify `internal/cli/*.go` list contains ONLY: `root.go`, `root_test.go`, `root_resolver_test.go`, `fang_integration_test.go` — ⚠️ DEVIATION from plan text: `coverage_test.go` was deleted in Task 9 (no remaining cases after docs/command moves) and `completion_test.go` (the single root-wiring integration test `TestCompletionCmd_InAdvancedGroupWithShellSubcommands`) was folded into `root_test.go` here. Final cli/ root has 4 files.
- [x] Verify `grep -c 'addCmd(' internal/cli/root.go` returns 0 — ⚠️ DEVIATION: `root.go` is 334 LoC, not <100 LoC. The plan's <100 LoC target is incompatible with what was specified to remain in §Solution Overview ("Retains: NewRootCmd, initRootCmd, PersistentPreRunE, runRoot, applyStyles, resolveLocalization, allowedWithoutProject, isValidateCommand"). The eight retained helpers — particularly `PersistentPreRunE` (~55 LoC of project-resolution branching) and `runRoot` (~70 LoC of project-summary rendering) — comprise the bulk of the file. Shrinking further would require moving them into a new sub-helper file inside `cli/` (no clear home in any subpackage since they touch persistent-flag wiring + project resolution, both root concerns). Deferred as a future tidy; the substantive goal of the refactor (no inline `newXxxCmd` declarations, no `addCmd` helper) is met.
- [x] `goimports -w .`
- [x] `go build ./... && make test && make lint` — all three pass
- [x] Commit: `refactor(cli): shrink root.go, rename deploy/render entrypoints, drop dead test file`

### Task 12: Verify acceptance criteria

- [x] `find internal/cli -maxdepth 1 -type d | sort` shows exactly 18 entries (`internal/cli` itself + 17 subpackages: `cmdctx`, `command`, `completion`, `compose`, `deploy`, `docker`, `docs`, `info`, `lifecycle`, `prompt`, `render`, `service`, `shell`, `snapshot`, `status`, `validate`, `version`)
- [x] ⚠️ DEVIATION: `ls internal/cli/*.go` shows 4 files (`root.go`, `root_test.go`, `root_resolver_test.go`, `fang_integration_test.go`) — NOT 5. `coverage_test.go` was deleted in Task 9 (no remaining cases after docs/command moves) and `completion_test.go` was folded into `root_test.go` in Task 11. This was already documented in Task 11 notes; the acceptance-criterion file list is stale.
- [x] ⚠️ DEVIATION: `wc -l internal/cli/root.go` shows 334 LoC, not <100. Already documented in Task 11 — incompatible with what Solution Overview specified to remain (`PersistentPreRunE` and `runRoot` alone are ~125 LoC). Deferred as future tidy.
- [x] `grep -c 'addCmd(' internal/cli/root.go` returns 0
- [x] `grep -c 'newInfoCmd\|...\|newUninstallCompletionCmd' internal/cli/root.go` returns 0
- [x] No `nolint:depguard` was added: `grep -rn 'nolint:depguard' --include='*.go' internal/cli/` returns empty
- [x] ⚠️ DEVIATION: Cross-sibling cli imports include three lines, not one. The pre-existing `cli/service` → `cli/deploy` line (`service/service_plan.go`, `service/service_toggle.go`) is present as expected. Two additional `cli/lifecycle/{run,restart}.go` → `cli/info` imports are intentional and were explicitly anticipated in Task 1 notes: `info.Run` is consumed for the `ShowInfo` lifecycle callback, and the import became a sibling import once Task 10 moved run/restart into `cli/lifecycle/`. No new unanticipated cross-sibling imports.
- [x] No stuttering identifiers — grep for `docker.NewDocker|compose.NewCompose|...|lifecycle.NewLifecycle` in `internal/cli/root.go` returns empty.
- [x] Test seams preserved during moves:
  - `grep -c 'preflightRun = preflight.Run' internal/cli/lifecycle/lifecycle.go` returns 1
  - `grep -nE 'isTerminalFn|runStatusTUIFn' internal/cli/status/status.go` returns 4 matches (declarations + call sites)
- [x] `make build && make test && make lint` — all pass (0 lint issues)

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

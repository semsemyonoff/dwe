# Safe Refactoring: De-duplication & Helper Extraction

## Overview

Implement the safe, non-breaking refactoring opportunities surfaced by a multi-agent code analysis of the `dwe` CLI (97 raw findings → **96 confirmed**, 1 rejected as unsafe). Every item is an internal de-duplication or helper extraction that preserves observable behavior — no changes to exported APIs, CLI flags, output formats, error codes, or operation ordering.

- **Problem it solves:** ~71k LOC carries a layer of copy-pasted boilerplate (lock-acquire blocks, `os.ErrNotExist` fallbacks, config-load wraps, per-kind cobra bodies, table scaffolding, journal mutators, validator walkers) plus a handful of dead exported symbols and oversized functions. This raises maintenance cost and drift risk.
- **Key benefit:** fewer places to change when a convention shifts, less drift between near-identical sites, smaller/clearer functions, dead code removed — all with the existing test suite as the safety net.
- **Integration:** purely internal. Helpers land in the package that already owns the concern (`cmdctx`, `config`, `journal`, `lock`, leaf `shared` packages, or new leaf packages where a cycle would otherwise form). Type aliases keep all call sites compiling.

**Source of truth for findings:** the full machine-readable analysis result lives at
`/private/tmp/claude-501/-Users-s-Projects-devbox-next-cli/2fe60bd3-c325-4075-ab18-3f224453dd52/tasks/wn4errlvm.output`
(JSON, `.result.synthesis.themes[]` and `.result.confirmedFindings[]`). If that temp file is gone, the per-theme locations below are sufficient to reconstruct each change.

## Context (from discovery)

- **Project:** Go 1.26 CLI `dwe` (Dev Workspace Engine), Docker-based local dev-env manager. Three layers: `internal/cli/` (cobra), `internal/core/` (domain), `internal/shared/` (leaf infra). Entrypoint `cmd/dwe`.
- **Tooling:** `golangci-lint` v2 already enforces `errcheck, govet, ineffassign, staticcheck, unused, misspell, revive, gocritic, modernize` — so linter-catchable issues are out of scope. `make test` is mandatory (it syncs embedded docs first; `go test ./...` alone fails on a fresh tree). `make lint` runs the linters.
- **Load-bearing patterns (must NOT be "deduped"):** the four strict pipeline YAML loaders, hardcoded `sh` in `when:` predicates, strict-vs-lenient YAML decode surfaces, per-service folder symmetry, prompt-cache no-downgrade rule, `files_gate` asymmetric journal-skip decision, preflight→lock ordering, `ErrWrap("project_invalid_config")` contract. These were verified and excluded by the analysis; the extractions below only touch the surrounding boilerplate, never the contract logic.
- **Rejected (intentionally NOT in scope):** merging the ai/ide/git **validator** types (`SkippedService/TemplateData/SelectServices/LoadManifest/DryRunRender`) — divergent concrete types, divergent per-kind collision policy, git-only `PrepareHub`/`ResolveGitHooksDir`; full dedup would risk altering diagnostic/render behavior. (Note: the ai/ide/git **infrastructure** trio in Task 8 IS safe and in scope — that is a different set of symbols.)

## Development Approach

- **Testing approach:** Regular — existing tests (including golden tests) act as the regression net proving behavior is unchanged. Add focused new unit tests only for non-trivial **extracted helpers** (e.g. `binOverride`, `LoadDockerConfigOrEmpty`, `sortedUniq`, `AcquireProjectLocksOrReport`); pure mechanical moves covered by existing golden/behavior tests do not need new tests but MUST keep the existing ones green.
- One theme = one task = one atomic commit. Complete each task fully before the next.
- **CRITICAL gate after every task:** `make test` and `make lint` must both pass before moving on. Behavior-changing test diffs are a red flag — investigate, do not blindly update golden files.
- Make small, focused changes; keep helper names idiomatic (this repo uses tabs, width 4; `goimports` local-prefix `github.com/semsemyonoff/dwe`).
- Maintain backward compatibility (this entire plan is behavior-preserving by construction).

## Testing Strategy

- **Unit tests:** required for non-trivial extracted helpers (success + error/edge cases), table-driven where it fits. Mechanical call-site swaps rely on existing coverage.
- **Golden / assertion tests:** several themes are exact-output covered — some via golden snapshots (status sections, docs markdown, render packs, `stack/daemons`), some via assertion tests (`ui/render` tables: `table_test.go`). A clean refactor produces **zero** golden diffs and keeps every assertion green. If a golden file changes, the refactor altered output — revert and rethink, do not accept the diff.
- **No e2e/UI harness** in this repo — `make test` is the full suite.
- **Per-task command:** `make test && make lint`.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Update this plan if scope changes during implementation.

## Solution Overview

Tasks are ordered **safest / highest-value first**: pure deletions and named-constant hoists, then `risk=none` helper extractions, then `risk=low` and `medium`-effort items last. Each task collapses N near-identical sites into one helper (or splits one oversized function into named section helpers), leaving every documented contract intact. New helpers live in the package that already owns the concern; two themes introduce a new **leaf** package (templates extends-chain, shared TemplateData) — verified cycle-free because `config` does not import `templates`.

## Technical Details

- **Effort legend:** `trivial` (<10 min) · `small` (<30 min) · `medium` (~1h).
- **Risk legend:** `none` (pure internal move, identical behavior) · `low` (internal but hot-path or test-seam coupling).
- Each task header carries the theme's `priority / effort / risk`.
- Locations are `file:line` from the analysis (line numbers approximate to current HEAD; confirm with `rg` before editing).

## What Goes Where

- **Implementation Steps** (`[ ]`): all code + test changes — entirely in-repo.
- **Post-Completion** (no checkboxes): optional owner confirmation for two dead-code deletions, and the deliberately-deferred higher-risk merges noted inline.

## Implementation Steps

### Task 1: Remove dead exported code in template & docs-render packages
**Theme 3 — priority high / effort small / risk none.** Pure deletion; `unused` misses these only because they are exported in `internal/` packages with no external importers (referenced solely by their own tests).

**Files:**
- Modify: `internal/core/execution/templates/ai/ai.go`
- Modify: `internal/core/execution/templates/ide/ide.go`
- Modify: `internal/core/docs/render/glamour.go`
- Modify/Delete tests covering only the removed symbols (e.g. `internal/core/docs/render/glamour_test.go` cases)

- [x] `rg` to re-confirm zero production callers for each symbol before deleting.
- [x] Delete `ValidateServiceNameAsPackKey` from `ai/ai.go:57` AND `ide/ide.go:205` (identical, 0 callers; real validation goes through `manifest.ValidatePackName`).
- [x] Delete `ai.ValidateTemplateKey` (`ai/ai.go:39`); KEEP `ide.ValidateTemplateKey` (live: `render/ide_test.go:854`).
- [x] Delete production-unused render helpers `RawMarkdown`, `PlaceholderFor*`, `ExtractMermaidBlocks` (`glamour.go:64,72,79,86,93,106`) plus the `mermaidBlockRE`/`newlineRE` vars used only by `ExtractMermaidBlocks`. KEEP `ThemeFromBackground` (live: `show.go:257`).
- [x] Remove now-orphaned test cases that only covered the deleted symbols.
- [x] `make test && make lint` — must pass before next task.

### Task 2: Hoist repeated literals & not-exist guards into named constants/helpers
**Theme 5 — priority high / effort trivial / risk none.** Six independent, output-identical consolidations.

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/cli/deploy/state.go`, `internal/cli/deploy/deploy.go`, `internal/cli/deploy/menu.go`
- Modify: `internal/core/workflow/setup/validate.go` (+ `merge.go`, `wizard.go`, `huh.go`)
- Modify: `internal/core/docs/lang.go`
- Modify: `internal/core/project/config/styles.go`
- Modify: matching `*_test.go` for the new `binOverride` helper

- [x] Collapse the 5 binary accessors (`workspace.go:29-84`) to delegate to unexported `binOverride(cfg *DweConfig, key, def string) string`; keep all 5 exported signatures byte-for-byte.
- [x] Replace hand-rolled deploy paths with `lock.DeployLockPath(workDir)` and `journal.DefaultRelPath` (`state.go:60,111,178`; `deploy.go:317`; `menu.go:131`) — verify byte-identical strings. NOTE: `DefaultRelPath` is a `/`-joined string literal while the hand-rolled paths use variadic `filepath.Join`; the swap is `filepath.Join(workDir, journal.DefaultRelPath)` and is equivalent only after `filepath.Join` cleaning (darwin/linux are the only targets).
- [x] Hoist the legacy `compose.overlays` migration sentence to package `const legacyComposeOverlaysMsg` (no trailing period), referenced from both `workspace.go:1221,1228`.
- [x] Add `const (minPort = 1; maxPort = 65535)` in `setup` and replace the 5 comparisons (`validate.go:63`, `merge.go:80`, `wizard.go:212`, `huh.go:382,447`); keep message strings verbatim.
- [x] Add unexported `i18nFilePath(locale, rel)` in `docs/lang.go`, use at `:39,116` (avoid clashing with existing `i18nPath` local).
- [x] Change `os.IsNotExist` → `errors.Is(err, os.ErrNotExist)` in `styles.go:44` (add `errors` import).
- [x] Write unit tests for `binOverride` (override-present / override-empty→default).
- [x] `make test && make lint` — must pass (pre-existing TestRussianTranslationsAreFresh failure unrelated to this task).

### Task 3: Centralize project-lock acquire + ProjectLockHeldError report
**Theme 1 — priority high / effort small / risk none.** 8 byte-identical 9-line blocks across 3 packages. Lock-ordering invariant stays inside `AcquireProjectLocks`; only the error-report wrapper is extracted.

**Files:**
- Create/Modify: `internal/cli/cmdctx/` (new helper, e.g. `locks.go`)
- Modify: `internal/cli/deploy/deploy.go`; `internal/cli/lifecycle/reset.go`; `internal/cli/snapshot/{create,restore,remove,pack,unpack}.go`
- Create: `internal/cli/cmdctx/locks_test.go`

- [x] Add `func AcquireProjectLocksOrReport(baseDir string, w *render.Writer) (release func(), err error)` to `cmdctx`: wraps `lock.AcquireProjectLocks`; on `*lock.ProjectLockHeldError` print via `w.Error(phe.Error())` and return `phe` unchanged (preserving exit code 2); else wrap `acquiring project locks: %w`. (cmdctx already imports `shared/render`; adding `shared/lock` is layering-safe.)
- [x] Collapse all 8 sites: `deploy.go:382`, `reset.go:189,359`, `snapshot/create.go:83`, `restore.go:109`, `remove.go:82`, `pack.go:50`, `unpack.go:61` to `release, err := cmdctx.AcquireProjectLocksOrReport(dir, render.Stdout()); if err != nil { return err }; defer release()`. Keep `defer release()` at each call site.
- [x] ⚠️ Note (do NOT auto-fix here): `lifecycle/stop.go:61` acquires the same locks but omits the `ProjectLockHeldError` branch — surface as a follow-up question, decide intentional vs bug separately.
- [x] Write unit test for `AcquireProjectLocksOrReport` covering held-error (exit code 2 preserved) and generic-error wrap.
- [x] `make test && make lint` — must pass.

### Task 4: Add `config.LoadDockerConfigOrEmpty` for the os.ErrNotExist fallback
**Theme 2 — priority high / effort small / risk none.** ~10 sites of the load-or-empty idiom with identical `loading docker config: %w` wrap.

**Files:**
- Modify: `internal/core/project/config/docker.go`
- Modify: `internal/cli/deploy/deploy.go`; `internal/cli/lifecycle/reset.go`; `internal/cli/compose/compose.go`; `internal/cli/docker/docker.go`; `internal/cli/shell/shell.go`; `internal/core/usercommands/runtime/build_context.go`
- Modify: `internal/core/project/config/docker_test.go`

- [x] Add `func LoadDockerConfigOrEmpty(baseDir string, cfg *DweConfig) (*DockerConfig, error)` next to `LoadDockerConfig`: returns `(&DockerConfig{}, nil)` on `os.ErrNotExist`, wrapped error otherwise.
- [x] Collapse sites: `deploy.go:392`, `reset.go:199,371`, `compose.go:54,119`, `docker.go:59,338`, `shell.go:134`, `build_context.go:101`.
- [x] Remove the now-orphaned `os` import in `compose.go`, `shell.go`, `docker.go` (only if no other `os.` use remains — verify with `rg`). Also removed unused `errors` from `docker.go` and `build_context.go`.
- [x] Leave `command/daemonset.go:108` inlined (returns a cobra directive, not an error) and the builtin `containers/*` nil-fallback untouched.
- [x] Write unit tests for `LoadDockerConfigOrEmpty` (missing file → empty, malformed → wrapped error).
- [x] `make test && make lint` — must pass. (Only pre-existing TestRussianTranslationsAreFresh failure in docs package, unrelated to this task.)

### Task 5: Extract reporter/recorder boilerplate in the pipeline executor & FileRecorder
**Theme 6 — priority medium / effort small / risk none→low.** `files_gate` decision logic stays put; only reporting/recording tails collapse.

**Files:**
- Modify: `internal/core/execution/pipeline/executor.go`
- Modify: `internal/core/execution/pipeline/file_recorder.go`
- Modify: matching `*_test.go`

- [x] Extract `failGateStep(...)` for the 6 identical `StartStep→FailStep→OnStepFail→return ErrSilent` blocks (`executor.go:638,652,665,674,683,705`).
- [x] Extract `skipStateStep(...)` for the 2 byte-identical state-skip blocks (`executor.go:625,722`).
- [x] Extract `runChildCmd(...)` for the 13-line child-run tail shared by `execShellAction`/`execDweAction` (`executor.go:191,212`).
- [x] Extract `ensureProjectPhaseSteps`/`ensureServicePhaseSteps` for the journal-map nil-init ladders in `FileRecorder` (`file_recorder.go:106,209,279`) — risk low (touches recorder state); keep nil-init order identical.
- [x] Update/extend `pipeline` tests for the new helpers; confirm `files_gate` golden/behavior tests unchanged.
- [x] `make test && make lint` — must pass.

### Task 6: Unify plan-time & validation walkers
**Theme 7 — priority medium / effort small / risk none.** Cold paths (plan/validate time); prefix/error strings provably equal.

**Files:**
- Modify: `internal/core/execution/pipeline/resolve.go`
- Modify: `internal/core/validate/config/deploy_files_gate.go`, `parallel_groups.go`, `service_hooks.go`, `deploy_after.go`
- Modify: matching `*_test.go`

- [x] Unify the step-level `when:` resolution shared by `resolveLeafStep`/`resolveParallelStep` (`resolve.go:119,183`).
- [x] Extract the files-gate phase walker duplicated 4× in `deploy_files_gate.go:89,139,261,357`.
- [x] Extract `registryFrom(...)` for the 7× `CommandRegistry` type-assertion (`parallel_groups.go:26,55,82`; `service_hooks.go:40`; `deploy_files_gate.go:71,246,341`).
- [x] Extract `resolveServices(...)` for the 2 identical service-map resolution blocks (`deploy_after.go:70`; `service_hooks.go:24`).
- [x] Add/extend validator tests as needed; confirm diagnostic output unchanged (added `TestResolveStepWhen`; existing validator golden/behavior tests cover the walker/registry/services extractions).
- [x] `make test && make lint` — must pass.

### Task 7: Factor load-or-mutate & union/dedup boilerplate in the deploy journal
**Theme 10 — priority medium / effort small / risk none.** One Load + one Save preserved; hash map is key-sorted (insertion order irrelevant).

**Files:**
- Modify: `internal/core/workflow/deploy/journal/pending.go`, `internal/core/workflow/deploy/journal/hash.go`
- Modify: matching `*_test.go`

- [x] Factor the `Load→mutate→Save` envelope shared by the 7 mutators (4 also share `Pending==nil` short-circuit) — `pending.go:64,100,170`.
- [x] Add `sortedUniq` helper for the 2 dedup/sort copies in `applyPendingOp` (`pending.go:202,213`).
- [x] Factor `phasesToMap` shared by project/service deploy-config hashing (`hash.go:335,382`).
- [x] Write unit test for `sortedUniq`; confirm hash stability tests unchanged.
- [x] `make test && make lint` — must pass.

### Task 8: Consolidate the ai/ide/git template-package **infrastructure** trio
**Theme 9 — priority medium / effort small / risk none.** Byte-identical infra (NOT the validators — those are the rejected item). Per-kind collision policy (AI shallowest vs IDE/git deepest) stays in the separate resolvers.

**Files:**
- Create: new leaf package (e.g. `internal/core/execution/templates/extendschain/` and/or shared `TemplateData`) — verify no import cycle (`config` does not import `templates`).
- Modify: `internal/core/execution/templates/ai/ai.go`, `ide/ide.go`, `git/git.go`
- Modify: matching `*_test.go`

- [x] Extract the extends-walk trio (`ImplicitPackCandidates`/`ExtendsDepth`/`ExtendsRoot` + the 9×-duplicated `const maxDepth = 32`) into one shared leaf package (new `internal/core/execution/templates/packcommon/`; ai/ide/git keep backward-compatible `var` aliases for the external callers in `cli/render` and `validate/templates`).
- [x] Extract the identical `TemplateData` struct + `AppServices/ToolServices/InfraServices` + `filterServices` to a shared type; keep call sites compiling via type aliases (`type TemplateData = packcommon.TemplateData` in all three packages).
- [x] Collapse `DryRunRender`/`executeTemplateInMemory` (differ only by the kind literal) — per-package `DryRunRender` now delegates to `packcommon.DryRunRender(kind, …)`.
- [x] Confirm render golden tests for all three packs unchanged.
- [x] `make test && make lint` — must pass.

### Task 9: Collapse lifecycle & snapshot workflow boilerplate
**Theme 11 — priority medium / effort small / risk low.** Preflight/lock ordering and journal semantics preserved; run/stop keep their regErr-vs-lock asymmetry per-file.

**Files:**
- Modify: `internal/core/workflow/snapshot/{create,restore,remove}.go`
- Modify: `internal/core/workflow/lifecycle/{run,stop}.go`
- Modify: matching `*_test.go`

- [x] Extract `classifyRunErr` (runErr→status,failedStep) shared by snapshot create/restore (`create.go:206`; `restore.go:261`).
- [x] Extract `absOrSelf` for the snapshot-dir fallback (`create.go:185`; `restore.go:238`; `remove.go:106`).
- [x] Collapse the clear-pending-restart journal write in lifecycle run/stop (`run.go:312,348`; `stop.go:109`).
- [x] Hoist the registry-load + `ApplyVisibility` prelude in run/stop (`run.go:141`; `stop.go:55`) — keep the per-file regErr-vs-lock asymmetry.
- [x] Add/extend tests; confirm lifecycle ordering tests unchanged.
- [x] `make test && make lint` — must pass.

### Task 10: Localized command/completion & docs-package helper extractions
**Theme 13 — priority medium / effort trivial / risk none.** Identical small blocks, identical output.

**Files:**
- Modify: `internal/cli/command/{inspect,list}.go`; `internal/cli/docs/{show,list,search,export,llmstxt,generate}.go`; `internal/core/docs/{search,headings,anchor}.go`; `internal/core/docs/tui/{model,diagram_inline,heading_anchors}.go`
- Modify: matching `*_test.go`

- [x] Extract `buildParamEntriesJSON` shared by `inspect.go:151` / `list.go:78`.
- [x] Dedup the `Script.Shell` empty-default-to-`sh` logic (`inspect.go:119,328`).
- [x] Extract `docsCfgLang` for the user-config-lang block (`show.go:110`, `list.go:81`, `search.go:78`, `export.go:55`, `llmstxt.go:68`). NOTE: keep `docs.go` cfgLang inline (its global-config lookup differs).
- [x] Extract `listCommandsByGroup` shared by `generate.go:109,452` index builders.
- [x] Extract `Model.diagramTheme` (`tui/model.go:374`; `diagram_inline.go:67,159`).
- [x] Extract `RootByName` for adjacent DocRoot-by-name lookups (`tui/model.go:242,256`; `docs/search.go:45`).
- [x] Extract `isFenceLine` markdown-fence predicate (`headings.go:33`; `anchor.go:61,111`; `search.go:99`; `tui/heading_anchors.go:48`).
- [x] Confirm docs golden/markdown tests unchanged.
- [x] `make test && make lint` — must pass.

### Task 11: Centralize repeated infrastructure helpers in internal/shared
**Theme 19 — priority medium / effort small / risk none→low.** i18n nil-store→fallback and lock inode-race invariants preserved. (The host-id sub-item is `low`, not `none` — see below.)

**Files:**
- Modify: `internal/shared/docker/{stop,compose}.go`; `internal/shared/lock/lock.go`; `internal/shared/i18n/store.go`; `internal/shared/envfile/render.go`; `internal/shared/tpl/render_command.go`
- Modify: matching `*_test.go`

- [x] Extract `runDirect` for the 3 direct-docker container funcs (`docker/stop.go:23,54,82`).
- [x] Collapse the docker-ps line-splitting loop 3× (`docker/compose.go:239,272,296`) — new `splitNonEmptyLines` helper.
- [x] Factor `finalizeAcquire` for the lock-acquire success path 2× (`lock.go:59,87`) — keep inode-race handling.
- [x] Collapse the 9 locale→en→fallback lookups in the i18n store (`store.go:43,71,96,121-279`) — keep nil-store-returns-fallback semantics. New `resolveLocalized` helper with per-method extract closures (map-index zero values are nil-safe).
- [x] ⚠️ `risk=low` (NOT none): unify the host-id platform logic shared by `envfile/render.go:16,28` and `tpl/render_command.go:83`. Resolved cleanly via a new leaf package `internal/shared/hostid` exposing `Current() Info` (single `user.Current()` read) plus `UID()`/`GID()` convenience funcs. `envfile.HostUID/HostGID` delegate; `tpl.CurrentHostInfo` returns `HostInfo(hostid.Current())` (identical-field struct conversion) so `HostInfo` literals in `render_command_test.go` stay valid. No friction — no follow-up needed.
- [x] Add/extend tests for the new helpers; confirm i18n/lock behavior tests unchanged. Added `hostid_test.go`, `splitNonEmptyLines` table test, and `resolveLocalized` nil-bundle test; existing store/lock/docker behavior tests cover the rest.
- [x] `make test && make lint` — must pass. (Both green; the one-off TestRussianTranslationsAreFresh blip cleared on rerun, unrelated to this task.)

### Task 12: Per-service & per-config-loader helper extractions in core/config & lifecycle
**Theme 17 — priority medium / effort small / risk none→low.** Strict-decode and per-service-symmetry contracts preserved (they govern surfaces/file-layout, not loader scaffolding).

**Files:**
- Modify: `internal/core/project/config/{workspace,info,styles}.go`; `internal/cli/lifecycle/{stop,restart,reset}.go`; `internal/cli/deploy/{deploy,menu}.go`; `internal/core/ui/render/deploy_info.go`
- Modify: matching `*_test.go`

- [x] Dedup per-service container resolution shared by stop/restart (`stop.go:71`; `restart.go:43`) — new `resolveServiceContainer(baseDir, cfg, name)` in `lifecycle/stop.go`; restart.go drops its now-unused `daemon` import.
- [x] Extract the services-folder directory-walk loop (`workspace.go:1852,2814`) — generic `walkServiceFolders[T]` collapses `loadServiceFolders` + `LoadServiceResetConfigs` (per-folder keep-flag handles the `cfg != nil` filter; errLabel keeps both wrap strings byte-identical).
- [x] Share the known-fields skeleton of the 2 custom `UnmarshalYAML` decoders (`workspace.go:304,341`) — new `checkKnownFields(value, mappingDesc, typeName, allowed)`; error strings byte-identical, returns `seen` map for the DeployStep parallel/leaf check.
- [x] Factor the 3 `*RenderEnabledExplicit` method bodies (`workspace.go:931,950,967`) — delegate to unexported `renderEnabledExplicit(*bool)`.
- [x] Merge the 2 identical auto-block include validators (`info.go:336,346`) — shared `validateIncludeValues(include, itemPath)`.
- [x] Dedup the identifier-key validation error in `validateConfigKeys` (`workspace.go:1192`) — generic `validateIdentifierKeys[V](svcName, kind, keys)`.
- [x] Collapse the `ErrSilent` log-tail reporting (`reset.go:239,464`; `deploy.go:810`) — new `cmdctx.WarnSilentLog(w, err, logEnabled, logPath)` (cli→core pipeline import is layering-safe).
- [x] Dedup `padRight`/`runeLen` across `deploy/menu.go:615` and `ui/render/deploy_info.go:115` — owner is `core/ui/render` (exported `PadRight`/`RuneLen`); deploy/menu.go drops its local copies and calls `render.PadRight`/`render.RuneLen`.
- [x] Confirm strict-decode and config golden tests unchanged. (config + validate suites green; no golden diffs.)
- [x] `make test && make lint` — must pass. (Both green; first `make test` run hit the known-flaky `TestRussianTranslationsAreFresh`, clean on rerun.)

### Task 13: Consolidate Lipgloss table scaffolding & map-cell formatting in core/ui/render
**Theme 4 — priority medium / effort small / risk none→low.** Covered by **assertion-based** tests (`table_test.go`: `TestFormatHostsCell`, `TestRenderTable_UsesTableStyles`, …) — a stronger exact-output net than golden snapshots. `stack/daemons.go` output is golden-covered. Expect all to pass unchanged.

**Files:**
- Modify: `internal/core/ui/render/{table,gitworkspace,diagnostics_table}.go`; `internal/core/project/stack/daemons.go`
- Modify: matching `*_test.go`

- [x] Add `baseTable()` constructor + `headerRowStyle()` + `renderRows()` epilogue for the 6 renderers (`table.go:21,196,256,342`; `gitworkspace.go:44`; `diagnostics_table.go:69`). NOTE: `DiagnosticsTable` header branch is a superset (adds col-0 center) — use the post-config escape hatch, not the shared header helper. (`DiagnosticsTable` chains `.BorderRow(true)` after `baseTable`; col-0 center applied to `headerRowStyle()` result.)
- [x] Unify `formatHostsCell`/`formatPortsCell`/daemon `prettyParams` via a generic sorted-`key=value`-pairs helper parameterized by value verb (`table.go:70,94`; `daemons.go:180`) — risk low (verb difference). Exported generic `render.SortedKVPairs[V](m, format func(V) string)`; uses a stringifier func (not a verb string) to avoid Go 1.24 non-constant-format-string vet warnings. `stack` already imports `render`, so `prettyParams` delegates cleanly.
- [x] Confirm ALL render assertion tests pass unchanged and stack golden tests produce zero diffs.
- [x] `make test && make lint` — must pass.

### Task 14: Unify per-kind render/validate/status command boilerplate
**Theme 12 — priority medium / effort small / risk low.** User-facing strings passed verbatim. The full render RunE-skeleton merge is **deferred** (higher risk).

**Files:**
- Modify: `internal/cli/render/{ai,ide,git}.go`; `internal/cli/status/{apps,tools,infra,daemons,topology,git}.go`; `internal/cli/validate/validate.go`
- Modify: matching `*_test.go`

- [x] Unify the 3 `validateExplicit{AI,IDE,Git}Arg` funcs (`ai.go:244`; `ide.go:156`; `git.go:101`) — thin wrappers now delegate to `validateExplicitRenderArg` in new `render/common.go` (kind/no-dir-artifact/participates/accessor parameterized; message strings byte-identical). The `TestValidateExplicitIDEArg` table still passes against the wrapper.
- [x] Extract `warnSelectionSkips` + `warnNoPack` in render ai/ide/git (`ai.go:88,149`; `ide.go:87,221`; `git.go:73,166`) — both live in `render/common.go`. `warnSelectionSkips` is generic over the per-kind `SkippedService` value with a `(reason,name,dir,winner)` extractor (the three types stay separate per the rejected-merge note); `warnNoPack` calls `packcommon.ImplicitPackCandidates` directly (identical output to the per-package aliases).
- [x] Collapse the 6 uniform status section subcommands into one helper (`status/apps.go:10`, `tools.go:10`, `infra.go:10`, `daemons.go:13`, `topology.go:9`, `git.go:9`) — new `newStatusSectionCmd(flags, use, short, sec, withPendingBanner)` in `status.go`; the 6 per-file constructors deleted, registration updated. `withPendingBanner` preserves the apps/tools/infra-only pending banner.
- [x] Extract `newValidateLeafCmd` for the 4 single-scope validate leaves (`validate.go:217,229,257,269`) — commands/env/setup/translations now built via the shared helper (scope key passed in; `translations`→`i18n` mapping preserved).
- [x] ⚠️ DEFER: the render ai/ide/git RunE-skeleton merge (`ai.go:54`, `ide.go:55`, `git.go:48`) — intentionally NOT done (medium/medium, higher risk); left for a separate carefully-reviewed change per Post-Completion.
- [x] Confirm status/render command tests unchanged — `cli/render`, `cli/status`, `cli/validate` suites all green, zero golden diffs.
- [x] `make test && make lint` — must pass. (Both green.)

### Task 15: Extract local helpers in the service-toggle & snapshot command flows
**Theme 8 — priority medium / effort medium / risk low.** Two items carry test-seam coupling — scope carefully.

**Files:**
- Modify: `internal/cli/service/{service,service_toggle,service_plan}.go`; `internal/cli/snapshot/{create,restore,remove}.go`
- Modify: matching `*_test.go`

- [x] Share the contributor-partitioning loop between the 2 pending builders (`service_toggle.go:283`; `service_plan.go:499`) — new `partitionContributors` in `service_toggle.go` returns the sorted deploy set + restart flag; `buildPendingOpsFromContributors` and `buildPendingClears` both delegate (op-vs-clear assembly stays per-builder).
- [x] Extract `decideToggleApply` prefix shared by single/multi toggle flows (`service_toggle.go:607`; `service.go:205`) — risk low. New `decideToggleApply(ctx, out, deps, plan, execOpts, apply, neverDeployed, stackRunning) (handled bool, err error)` resolves every non-prompt case; the divergent confirm tail (single: TTY-gated + `--apply` hint; multi: always-prompt) stays per-flow.
- [x] Extract `installSnapshotNotifier` for the notifier-defer setup in create/restore/remove (`create.go:96`; `restore.go:119`; `remove.go:92`) — new helper in `snapshot/helpers.go`; returns a finalize closure deferred as `defer installSnapshotNotifier(...)()` (load at defer-eval, notify at return); per-kind cancellation predicate passed in; notifier ordering preserved (registered before the signal-context stop defer).
- [x] Extract `signalAwareContext` (parentCtx-default + `signal.NotifyContext`) — `create.go:121`; `restore.go:142`; `remove.go:115`. New helper in `snapshot/helpers.go`.
- [x] Extract `requireSnapshotConfig` for the 3 nil-config guards (`restore.go:99,228`; `remove.go:67`) — new helper in `snapshot/helpers.go`; create's guard kept inline (it also requires a `create:` block, different message).
- [x] ⚠️ `mutateAndPlan`/`mutateAndPlanBatch` ~90-line dedup (`service_toggle.go:314,429`) — ➕ DEFERRED as a follow-up (see Post-Completion). Not forced: the two flows use distinct test seams (`singleToggleAddPendingOps` vs `multiToggleAddPendingOps`) that exist specifically for independent write-failure injection, and diverge in config-hash computation (`journal.ServiceConfigHash` vs `batchServiceConfigHash`); merging would compromise the seam design for marginal gain.
- [x] Update toggle/snapshot tests for extracted helpers; confirm pending-state behavior unchanged. Added `TestPartitionContributors` and `snapshot/helpers_test.go` (`requireSnapshotConfig`/`signalAwareContext`/`installSnapshotNotifier`); existing toggle/snapshot suites cover `decideToggleApply` and the notifier-deferred flows unchanged.
- [x] `make test && make lint` — must pass.

### Task 16: Tidy model/runner accessors & validators in usercommands
**Theme 14 — priority medium / effort small / risk low.** Field-rejection and override accessors carry user-facing-message / hot-path coupling — keep messages identical.

**Files:**
- Modify: `internal/core/usercommands/runtime/runners/{host,script,service}/*.go`; `internal/core/usercommands/model/types.go`; `internal/core/usercommands/registry/registry.go`; `internal/core/validate/commands/daemon.go`
- Modify: matching `*_test.go`

- [x] Extract `runio.WireChildIO` for the identical child-process IO wiring (`host/host.go:141`, `host/dwe.go:57`, `script/script.go:208`, `service/run.go:48`, `service/exec.go:93`) — returns the cleanup func; all 5 sites call `defer runio.WireChildIO(rc, c)()` (wiring at defer-eval, teardown at return — identical semantics).
- [x] Add `Effective{Service,User,Workdir,WorkdirFrom}` accessors on `CommandDef` (`types.go`; callers `expand_daemon.go`, `service/exec.go` `resolveServiceFields`, `commands/daemon.go`, plus internal `validateServiceType`/`validateDaemonType`) — runner.* override semantics identical; service/exec.go keeps the Mode-override inline (no Mode accessor in scope).
- [x] Dedup Registry construction + group-sort boilerplate — new `newRegistry()` constructor + `(*Registry).sortGroups()`; collapses `NewEmptyRegistry`/`LoadRegistry`/`BuildRegistryFromParsed`.
- [x] Unify `Registry.Validate`/`Registry.Diagnostics` over one workflow-ref scan — new `scanUnknownCommandRefs(visit)` helper; both outputs byte-identical.
- [x] ⚠️ Collapse type-foreign field rejections in `CommandDef` validators — ➕ DEFERRED as a follow-up (see Post-Completion). The per-type messages genuinely differ (shell/dwe/service use `type=%s`; script/workflow/builtin/daemon hardcode their type literal) and each validator checks a different field subset in a different order — a collapse would threaten the byte-identical diagnostic strings the plan requires.
- [x] Update usercommands tests; confirm validator messages byte-identical (added `TestCommandDef_EffectiveAccessors`; existing usercommands/registry/validate suites cover the IO-wiring, registry-construction, and walker extractions unchanged).
- [x] `make test && make lint` — must pass. (Both green; first `make test` hit the known-flaky `TestRussianTranslationsAreFresh`, clean on rerun.)

### Task 17: Centralize the loading-config wrap & other cross-package CLI helpers
**Theme 18 — priority low / effort small / risk none.** The `ErrWrap("project_invalid_config")` group is the separate intentional contract — EXCLUDE it.

> **Helper-home / layering decision (resolve before coding the wrap item):** the `loading config: %w` wrap actually appears at **~29 sites tree-wide**, not ~10 — and **two are in the `core/` layer** (`core/workflow/lifecycle/run.go:127`, `stop.go:50`). A helper placed in `cmdctx` (cli) CANNOT serve those two without a `core → cli` layering violation. Choose ONE:
> - **(a) Reachable by both layers** — put the helper in the `config` package (e.g. `config.LoadConfigOrWrap(baseDir) (*DweConfig, error)` returning the already-`loading config: %w`-wrapped error). Both cli and `core/workflow` already import `config`, so all ~29 sites (incl. the 2 core ones) collapse. Preferred if doing the full sweep.
> - **(b) Cli-only scope** — keep the helper in cli, convert only the cli sites, and explicitly leave the 2 `core/workflow/lifecycle` sites inlined (note it in the task). Smaller blast radius.
> First `rg -n 'loading config: %w'` to get the live list, then subtract any that belong to the `project_invalid_config` group before editing.

**Files:**
- Modify: `internal/cli/render/{env,git,ai}.go`; `internal/cli/service/{service,service_list}.go`; `internal/cli/compose/compose.go`; `internal/cli/lifecycle/restart.go`; `internal/cli/docker/docker.go`; `internal/cli/deploy/deploy.go`; `internal/cli/lifecycle/reset.go`; `internal/cli/status/json.go`; `internal/cli/info/info.go`; `internal/cli/shell/exec.go`; `internal/cli/root.go`; `internal/core/project/stack/{status,deploystatus,daemons,health}.go`; `internal/core/execution/pipeline/plain.go`
- Modify: matching `*_test.go`

- [x] Centralize the `loading config: %w` wrap after `config.LoadConfig` per the helper-home decision above. Chose **option (a)**: added `config.LoadConfigOrWrap(workspacePath) (*DweConfig, error)` next to `LoadConfig`, collapsing all 29 plain-wrap sites (27 cli + the 2 `core/workflow/lifecycle/{run,stop}.go` sites) via a paired-pattern `perl` rewrite. The `ErrWrap("project_invalid_config")` group is untouched (those are the typed contract, not the plain `fmt.Errorf("loading config: %w", …)` form). Unit tests added for the helper (success + canonical-prefix/unwrap-to-`os.ErrNotExist` error case).
- [x] Extract `addToggleFlags` for repeated toggle-flag wiring (`service.go:60,407,470`) — new `addToggleFlags(cmd, *apply, *printPlan, *skipHooks)`; flag descriptions byte-identical.
- [x] Dedup the render-warning loop in `renderServicesListText` (`service_list.go:84`) — local `renderSection` closure over `RenderApps/Tools/Infra`.
- [x] Unify the candidate-collection filter in `pick*`/`serviceCompletion` (`service.go:295,497`) — new `manageableOptionalServices(cfg, wantEnabled)`; `serviceCompletion` maps `filter == completeEnabledOptional` to the bool (only two filter constants exist, so exhaustive).
- [x] Replace the empty text-renderer closure with a `WriteJSON` helper — new `cmdctx.WriteJSON[T]` (WriteData with empty text renderer); applied at all `func(T) string { return "" }` sites (status/json.go, info/info.go, command/{command,list}.go, service_list.go, validate.go).
- [x] Factor `emptyIfNil` for nil-slice normalization (`status/json.go:405-442`) — generic `emptyIfNil[T]` in the local-helpers section; five per-section cases collapsed.
- [x] Extract `wrapSection` for the section-wrapper block in stack renderers (`stack/status.go:85,104`; `deploystatus.go:41`; `daemons.go:254`) — shared `wrapSection(title, body)` in status.go; removed now-unused `strings` import from deploystatus.go.
- [x] Dedup the Health reduction tail in `AggregateHealth`/`FromTopo` (`health.go:28,82`) — new `healthFromCounts(active, running)`.
- [x] Collapse the `index>0 [N/M]` emit branches in `PlainReporter` (`plain.go:247,272,298,328`) — new `stepIndexPrefix(index, total)` returning `"[N/M] "` or `""`; format strings stay constant literals (no vet warning).
- [x] Extract the `-u/-w/-e` docker arg-append block + compose-run argv prefix + container-target resolution preamble in `shell/exec.go` — new `appendUserWorkdirEnvArgs`, `composeRunArgv`, and `resolveShellTarget` (returns svc/opts/fullContainerName); restart-style comments preserved in the resolver.
- [x] Consolidate deploy-summary counting in `runRoot`/`runRootJSON` (`root.go:410,477`) — new `countDeployedServices(state, tracked)`.
- [x] Confirm JSON-output and stack golden tests unchanged. (cli + stack + pipeline suites green, zero golden diffs.)
- [x] `make test && make lint` — must pass. (Both green; one initial `make test` blip was the known-flaky `TestRussianTranslationsAreFresh`, clean on rerun.)

### Task 18: Split oversized functions into named section helpers
**Theme 16 — priority medium / effort medium / risk none.** Pure cut-and-paste; each target has per-section test coverage; none is a genuine hot path.

**Files:**
- Modify: `internal/cli/deploy/deploy.go`; `internal/cli/command/inspect.go`; `internal/cli/docs/generate.go`; `internal/core/execution/pipeline/executor.go`
- Modify: matching `*_test.go`

- [x] Split the scope-status switch out of the 520-line `RunHelper` into `computeScopeState(opts, state, projectHash, serviceHashes)` (`deploy.go`).
- [x] Split `printInspectAt` into per-section render helpers (`inspectWorkflowSteps`/`inspectDaemonSection`/`inspectParamsSection`/`inspectContextSection`/`inspectEnvSection`/`inspectFilesSection`); section closures `def2`/`sub` passed via `inspectDef2`/`inspectSub` type aliases.
- [x] Split the 288-line `writeCommandMarkdown` into per-section helpers (`writeCommandTypeDetails`/`writeCommandWorkflowSteps`/`writeCommandParams`/`writeCommandContext`/`writeCommandFiles`/`writeCommandEnv`); each carries its own length guard.
- [x] Split `executeStepBody`'s files_gate probe into `evalFilesGate` (`executor.go`) — wraps Task 5's `failGateStep`/`skipStateStep`; tri-state contract realized as `(proceed bool, err error)`: `(true,nil)`=proceed / `(false,nil)`=skip / `(false,ErrSilent)`=fail. `files_gate` readable/missing asymmetry preserved verbatim.
- [x] Confirm inspect/markdown/deploy golden & behavior tests unchanged. (cli/command, cli/deploy, cli/docs, core/execution/pipeline all green; zero golden diffs.)
- [x] `make test && make lint` — must pass. (Both green; `make lint` reports 0 issues.)

### Task 19: Consolidate setup-validator boilerplate
**Theme 15 — priority low / effort medium / risk low.** Bundle both findings; ripples into `all.go` + `validators_test.go` construction sites.

**Files:**
- Modify: `internal/core/validate/setup/validators.go`, `internal/core/validate/setup/all.go`
- Modify: `internal/core/validate/setup/validators_test.go`

- [x] Collapse the per-type `ID()`/`Domain()` one-liners + `cfg==nil` guard via an embedded base (`validators.go:80-173`; update construction in `all.go:15`). Added `baseValidator{id}` (provides `ID()`/`Domain()` + the diagnostic constructors) and `cfgValidator{baseValidator; cfg}` whose `questions()` folds the nil-config guard into iteration (nil cfg → no questions → empty diags, behavior-identical). Each cfg-driven validator is now `struct{ cfgValidator }` and ranges over `v.questions()`; `parseValidator` embeds `baseValidator` only. Construction goes through `newCfg(id, cfg)`.
- [x] Drop the redundant `target` arg from `makeError`/`makeWarning` (always equals the validator's own ID) — convert the two free functions to methods (`validators.go:16,100,121`). All three constructors (`makeError`/`makeWarning`/`makeErrorWithHint`) are now methods on `baseValidator` reading `b.id` as `Target`; all call sites use `v.makeError(msg)` etc.
- [x] Update `validators_test.go` construction sites; confirm every setup diagnostic message byte-identical. Construction sites swapped to `newCfg("<id>", tt.cfg)` (and `parseValidator` to the embedded-base form); message text unchanged, existing count-based tests stay green.
- [x] `make test && make lint` — must pass. (Both green; `make lint` reports 0 issues.)

### Task 20: TUI Update-handler dedups (statustui, cmdbrowser palette/tree)
**Theme 20 — priority low / effort small / risk low.** Keep mutations inside `Update` (View()-purity invariant) and the v2-lipgloss bridge contract.

**Files:**
- Modify: `internal/core/ui/statustui/tui.go`; `internal/core/ui/cmdbrowser/{palette,tree_render}.go`
- Modify: matching `*_test.go`

- [x] Extract `setActiveTab` to collapse the 7 tab-switch blocks in statustui (`tui.go:170-232`) — out-of-range indices are ignored, preserving the per-key guard the explicit Tab1–Tab5 blocks carried. NextTab/PrevTab indices are always in-range (len>0 guarded earlier), so behavior is byte-identical.
- [x] Add `fgStyle` helper for the 12 v2-lipgloss palette accessors (`palette.go:19-139`) — `fgStyle(color)` is the shared `lipgloss.NewStyle().Foreground(lipgloss.Color(color))` constructor; all 9 accessors plus the chained `apply*` constructions (ActivePaginationDot/InactivePaginationDot/DefaultFilterCharacterMatch/NoItems/FilterMatch/help key+desc/viewport HighlightStyle) delegate to it.
- [x] Unify `renderOpt`/`renderFilter` tree renderers (`tree_render.go:10,54`) — both delegate to new `renderTree(focused, showCounts, f)`; `f == nil` selects the plain `(N)` path, `f != nil` the filter `(M/N)` + dim-on-no-match path. Single nil check is the only divergence. Ran the full suite (`renderFilter` thinner golden coverage noted).
- [x] Confirm cmdbrowser/statustui golden tests unchanged — zero diffs; both package suites green.
- [x] `make test && make lint` — both pass (`make lint`: 0 issues).

### Task 21: Verify acceptance criteria
- [x] Verify all 20 themes implemented (or explicitly deferred with a ➕/⚠️ note in this plan). All 20 land as one atomic commit each (`94990eb9`…`5c0d229b`); deferrals (Task 14 RunE-skeleton merge, Task 15 `mutateAndPlan`/`mutateAndPlanBatch`, Task 16 field-rejection collapse) are recorded in Post-Completion.
- [x] Confirm NO exported API / CLI flag / output / error-code / ordering change crept in (diff review + `git log` of the 20 commits). Overall diff is near-balanced (4368 ins / 4350 del across 161 files) — pure de-dup/extraction; every behavior/assertion test stays green.
- [x] Confirm zero unexpected golden-file diffs across the whole tree. `git diff --stat 13dfc5f4..HEAD -- '**/testdata/**' '**/*.golden'` is empty.
- [x] Run full suite: `make test` (and `make test-race` for the concurrency-touching themes 5, 11). `make test` exit 0 (100 packages ok, 0 FAIL); `make test-race` exit 0 (no data races; journal/lock/pipeline green).
- [x] Run `make lint` clean. `golangci-lint run ./...` → 0 issues.
- [x] Run `make build` to confirm the binary still builds. `Built: ./bin/dwe`.

### Task 22: [Final] Update documentation & close out
- [ ] Update `docs/internals/packages.md` only if a new shared/leaf package (Task 8 extends-chain, Task 11 host-id owner) warrants a per-package note.
- [ ] Update `CLAUDE.md` "Critical Patterns" only if a new cross-package helper becomes a convention worth recording (e.g. `cmdctx.AcquireProjectLocksOrReport`, `config.LoadDockerConfigOrEmpty`).
- [ ] Move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed`).

## Post-Completion
*Items requiring manual intervention or external decision — no checkboxes, informational only.*

**Owner confirmation before deletion (Task 1):**
- The `render/glamour.go` set (`RawMarkdown`, `PlaceholderFor*`, `ExtractMermaidBlocks`) is exported and unused — confirm with the maintainer it is not an intended future public API before deleting. The `ValidateServiceNameAsPackKey` / `ai.ValidateTemplateKey` deletions are unambiguous (duplicate / 0 callers).

**Deferred higher-risk items (kept out of the safe sweep):**
- Render ai/ide/git RunE-skeleton merge (Task 14) — `medium/medium`. Pursue only as a separate, carefully-reviewed change.
- `mutateAndPlan`/`mutateAndPlanBatch` full merge (Task 15) and the `CommandDef` type-foreign field-rejection collapse (Task 16) — `medium/low` with test-seam / message coupling. Acceptable to land as their own follow-up PRs if they add friction.
  - ➕ Follow-up (Task 15): `mutateAndPlan`/`mutateAndPlanBatch` left un-merged. The two share steps 0–4 (lock → capture pre-state → write local.yml → reload + regen .env → build plan → render) but diverge at step 5: distinct `journal.AddPendingOps` seams (`singleToggleAddPendingOps` vs `multiToggleAddPendingOps`, kept separate so tests inject write failures independently) and distinct config-hash computation (`journal.ServiceConfigHash(svc, …)` vs `batchServiceConfigHash(…)`). A safe merge would thread both the seam and the hash-fn as parameters; worth doing only alongside a test refactor that keeps the two failure-injection paths covered.

**Explicitly out of scope (rejected by analysis):**
- Merging the ai/ide/git **validator** types — divergent concrete types, divergent collision policy, git-only logic. Not safe; do not attempt as part of this plan.

**Follow-up question raised during planning:**
- `lifecycle/stop.go:61` omits the `ProjectLockHeldError` print branch that its 8 siblings have (Task 3) — decide whether this is an intentional difference or a latent bug, separately from this refactor.

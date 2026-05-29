# Refactor `internal/core/workflow/snapshot` into Subpackages

## Overview

Split the 16-file `internal/core/workflow/snapshot` package by extracting two subpackages: `meta/` (Manifest type + path/name/vars helpers — the descriptor types referenced by 10 of 16 files) and `archive/` (Pack/Unpack/Verify/Inspect — the tar layer). Root keeps the 9 operation files (create, restore, remove, list, exec, devbox_files, services_diff, scan, observer).

**Problem**: `archive.go` is 838 lines mixing Pack + Unpack + Verify + extract helpers + several error types. `devbox_files.go` is 512 lines. The 16-file flat layout hides the structural three-tier shape (descriptors → archive → operations).

**Goal**: After refactor:
- `meta/` (5 files): `manifest.go`, `paths.go`, `name.go`, `vars.go`, `current.go` — types and coordinates
- `archive/` (5 files): `archive.go` (types), `pack.go`, `unpack.go`, `verify.go`, `inspect.go` — tar I/O split from current 838-line `archive.go`
- Root `snapshot/` (9 files): operation files (create, restore, remove, list, exec) + scanning (devbox_files, services_diff, scan) + observer

**Behavior changes**: none. All snapshot operations (create, restore, remove, pack, unpack) produce identical output and side effects.

## Context (from discovery)

- **16 .go + 15 _test.go files** in `internal/core/workflow/snapshot/`.
- **Largest file**: `archive.go` 838 lines (Pack + Unpack + Verify + extract helpers).
- **Other large files**: `devbox_files.go` (512), `restore.go` (380), `create.go` (336).
- **External callers**: 15 files across `internal/cli/snapshot/` (10 files) and `internal/core/validate/snapshot/` (5 files).
- **Internal coupling**:
  - `Manifest` type referenced by 10 of 16 files — central hub. Belongs in `meta/`.
  - `Pack`/`Unpack`/`Archive` references confined to `archive.go` (+ external callers in cli/snapshot/pack.go, unpack.go).
  - `BuildSnapshotVars` used by 4 op files (create, exec, remove, restore).
  - `DevboxFiles`, `ServicesDiff`, `ScanArtifacts` each used by 2-3 files.
- **`archive.go` internal structure** (lines):
  - Types (PackResult, UnpackResult, VerificationOutcome, ArtifactVerifyReport, UnpackOptions, UnpackCancelledError, UnpackVerifyDeclinedError) — 33-132
  - Pattern helpers (globToRegexp, excludesMatch, resolveExistingAncestor) — 143-251
  - `Pack` — 252-437 (185 lines)
  - `Unpack` + extractTarGz + mkdirRandom + confirmUnpackOverwrite — 438-595, 701-end (~ 270 lines)
  - `VerifyExtractedArtifacts` + printVerifyReport — 596-693

## Development Approach

- **Testing approach**: Regular. 15 existing _test.go files + golden fixtures act as safety net.
- **Behavior-preserving**: tar bytes-on-disk and journal entries must be byte-identical pre/post refactor (verified via existing archive_test.go round-trip tests).
- **Per-task atomicity**: each subpkg extraction + intra-file split = one task; `make test` between.

## Testing Strategy

- **Unit tests**: 15 existing tests move alongside code. archive.go tests split with their function (pack tests with pack.go, etc.).
- **Round-trip tests**: existing `archive_test.go` round-trips (pack→unpack→verify) — critical safety net.
- **e2e tests**: not applicable.
- **Manual verification (final task)**: snapshot create + restore + pack + unpack in a fixture project; tar contents byte-compared if practical.

## Progress Tracking

- Mark completed items with `[x]`.
- ➕ for newly discovered tasks; ⚠️ for blockers.
- Update plan if scope shifts.

## Solution Overview

**Architecture**: three-tier strict layering.

- `meta/` — leaf. Defines descriptor types, path coordinates, **`ScanArtifacts` + `hashFile`** (moved from root scan.go — required to break archive→root cycle), and **`WriteFileAtomic`** (exported from manifest.go's private `writeFileAtomic` — also used by devbox_files.go and restore.go). No internal subpkg imports.
- `archive/` — imports `meta/` (for `Manifest`, `ArtifactInfo`, `ScanArtifacts` in Verify). Self-contained tar layer.
- Root `snapshot/` — imports `meta/` + `archive/`. Owns business workflows.

**Cycle-break logic**: archive's `VerifyExtractedArtifacts` re-scans extracted artifacts via `ScanArtifacts`. If `scan.go` stays in root, archive needs `snapshot.ScanArtifacts` while root imports archive for `Pack`/`Unpack` — circular. Resolution: `scan.go` moves to `meta/` (it already operates on `meta.ArtifactInfo` and uses `meta.DevboxSubdir`/`meta.ManifestFileName` as inputs). Similarly `writeFileAtomic` becomes `meta.WriteFileAtomic` because `devbox_files.go` (4 calls) and `restore.go` (1 call) stay at root and would lose access otherwise.

**`devbox_files.go` non-split decision**: kept as a single file. ~250 lines are private YAML-AST helpers (`parseYAMLDoc`, `lookupPath`, `setPath`, etc.) tightly coupled to its three public functions; they have no other callers. Splitting them into `yaml_dotpath.go` is a reasonable follow-up but not part of this refactor.

**Tar contract**: existing `archive_test.go` round-trip tests are the safety net. The Overview's earlier "byte-identical pre/post" framing is downgraded: the contract is "all existing tests pass + manual round-trip create→pack→unpack→restore works." No new byte-comparison assertion is added.

**Intra-file split (in Task 1)**: `archive.go` (838 lines) split into 4 files inside the current `snapshot/` package (before Task 2 extracts `meta/`, before Task 3 moves them all to `archive/`):
- `archive.go`: types AND shared package-level constants — `PackResult`, `UnpackResult`, `VerificationOutcome`, `ArtifactVerifyReport`, `UnpackOptions`, `UnpackCancelledError`, `UnpackVerifyDeclinedError`, `rejectedTarEntryError`, plus the `maxUnpackBytes` / `maxUnpackFiles` size constants (consumed by both `unpack.go` AND `archive_inspect.go` — must stay in `archive.go` for package-local access)
- `pack.go`: Pack + glob/exclude helpers (globToRegexp, excludesMatch, resolveExistingAncestor)
- `unpack.go`: Unpack + extractTarGz + mkdirRandom + confirmUnpackOverwrite
- `verify.go`: VerifyExtractedArtifacts + printVerifyReport

## Technical Details

### Final structure

```
internal/core/workflow/snapshot/
├── meta/                          (NEW: 7 files — descriptors + scan + atomic write)
│   ├── manifest.go                → meta.Manifest, ArtifactInfo, ServiceSnapshot, ProjectInfo, DevboxFiles (struct),
│   │                                LastCreate, LastRestore, LoadManifest, SaveManifest, NewManifest,
│   │                                StatusOk, StatusFailed, StatusInterrupted, ArtifactHashMismatch
│   ├── paths.go                   → meta.SnapshotsDir, SnapshotDir, ManifestPath, StateDir, CurrentPointer,
│   │                                LockPath, PreRestoreBackup, DevboxSubdir, ManifestFileName
│   ├── name.go                    → meta.ValidateName
│   ├── vars.go                    → meta.BuildSnapshotVars
│   ├── current.go                 → meta.ReadCurrent, WriteCurrent, ClearCurrent
│   ├── scan.go                    → meta.ScanArtifacts, hashFile (MOVED from root — breaks archive→root cycle)
│   └── atomic.go                  → meta.WriteFileAtomic (EXPORTED from manifest.go's private writeFileAtomic)
├── archive/                       (NEW: 5 files — tar I/O)
│   ├── archive.go                 → PackResult, UnpackResult, VerificationOutcome, ArtifactVerifyReport,
│   │                                UnpackOptions, UnpackCancelledError, UnpackVerifyDeclinedError,
│   │                                + maxUnpackBytes/maxUnpackFiles constants
│   ├── pack.go                    → archive.Pack + private globToRegexp, excludesMatch, resolveExistingAncestor
│   ├── unpack.go                  → archive.Unpack, extractTarGz, mkdirRandom, confirmUnpackOverwrite
│   ├── verify.go                  → archive.VerifyExtractedArtifacts, printVerifyReport (calls meta.ScanArtifacts)
│   └── inspect.go                 → archive.ReadManifestFromTar (was archive_inspect.go)
├── create.go                      (root: CreateParams, Create, ProjectConfigHash — imports meta + archive)
├── restore.go                     (root: RestoreParams, Restore — uses meta.WriteFileAtomic)
├── remove.go                      (root: RemoveParams, Remove)
├── list.go                        (root: Entry, ListSnapshots)
├── exec.go                        (root: ExecParams, RunWorkflow, SelectWorkflow)
├── devbox_files.go                (root: captureDevboxFiles/restoreDevboxFiles — uses meta.DevboxFiles struct,
│                                   meta.WriteFileAtomic; YAML-AST helpers stay private intra-file)
├── services_diff.go               (root: ServicesDiff, DiffServices)
└── observer.go                    (root: StepObserverCloser, StepObserverFactory)
```

### Symbol renames

External callers update these references:

| Old | New |
|---|---|
| `snapshot.Manifest` | `meta.Manifest` |
| `snapshot.NewManifest` | `meta.NewManifest` |
| `snapshot.LoadManifest` | `meta.LoadManifest` |
| `snapshot.SaveManifest` | `meta.SaveManifest` |
| `snapshot.ArtifactInfo` | `meta.ArtifactInfo` |
| `snapshot.ArtifactHashMismatch` | `meta.ArtifactHashMismatch` |
| `snapshot.ServiceSnapshot` | `meta.ServiceSnapshot` |
| `snapshot.ProjectInfo` | `meta.ProjectInfo` |
| `snapshot.LastCreate`, `snapshot.LastRestore` | `meta.*` |
| `snapshot.StatusOk`, `StatusFailed`, `StatusInterrupted` | `meta.*` |
| `snapshot.DevboxFiles` (struct type, from manifest.go) | `meta.DevboxFiles` |
| `snapshot.SnapshotsDir` / `SnapshotDir` / `ManifestPath` / `StateDir` / `CurrentPointer` / `LockPath` / `PreRestoreBackup` | `meta.*` |
| `snapshot.DevboxSubdir`, `ManifestFileName` (constants) | `meta.*` |
| `snapshot.ValidateName` | `meta.ValidateName` |
| `snapshot.BuildSnapshotVars` | `meta.BuildSnapshotVars` |
| `snapshot.ReadCurrent` / `WriteCurrent` / `ClearCurrent` | `meta.*` |
| `snapshot.ScanArtifacts` (was in scan.go) | `meta.ScanArtifacts` (moved to meta — resolves cycle) |
| `writeFileAtomic` (was private in manifest.go) | `meta.WriteFileAtomic` (exported — used by devbox_files.go + restore.go) |
| `snapshot.Pack` | `archive.Pack` |
| `snapshot.Unpack` | `archive.Unpack` |
| `snapshot.PackResult`, `UnpackResult`, `UnpackOptions`, `VerificationOutcome`, `ArtifactVerifyReport` | `archive.*` |
| `snapshot.VerifyExtractedArtifacts` | `archive.VerifyExtractedArtifacts` |
| `snapshot.ReadManifestFromTar` | `archive.ReadManifestFromTar` |
| `snapshot.UnpackCancelledError`, `UnpackVerifyDeclinedError` | `archive.*` |
| `snapshot.Create` / `Restore` / `Remove` / `ListSnapshots` / `RunWorkflow` / `SelectWorkflow` | unchanged (root) |
| `snapshot.ProjectConfigHash` (from create.go) | unchanged (root) |
| `snapshot.ServicesDiff`, `DiffServices` | unchanged (root) |
| `snapshot.Entry` (from list.go) | unchanged (root) |
| `snapshot.StepObserverCloser`, `StepObserverFactory` | unchanged (root) |

**DevboxFiles naming clash note**: the `DevboxFiles` struct (declared in `manifest.go`) moves to `meta.DevboxFiles`. The root `devbox_files.go` FILE keeps its name (it holds capture/restore I/O functions, not the type) and updates internally: `captureDevboxFiles` returns `meta.DevboxFiles`; `restoreDevboxFiles` takes `meta.DevboxFiles`. The `Manifest.DevboxFiles` field (same-package) compiles fine in `meta/manifest.go` since both struct and field type are now in `meta`.

### Import path convention

Use the package name `meta` (not `snapshotmeta`) — short and intuitive in context. Same for `archive`. External callers typically import both:

```go
import (
    "devbox-cli/internal/core/workflow/snapshot"
    "devbox-cli/internal/core/workflow/snapshot/archive"
    "devbox-cli/internal/core/workflow/snapshot/meta"
)
```

## What Goes Where

- **Implementation Steps**: all code changes (subpkg creation, intra-file split, type renames at call sites).
- **Post-Completion**: manual round-trip snapshot test in a fixture project.

## Implementation Steps

### Task 1: Split `archive.go` (838 lines) into 4 files (intra-package prep)

Done in current `snapshot/` package, before subpkg extraction. This is the "intra-file" half of Level A from brainstorm.

**Files:**
- Modify: `internal/core/workflow/snapshot/archive.go` (becomes types + constants only)
- Create: `internal/core/workflow/snapshot/archive_pack.go`
- Create: `internal/core/workflow/snapshot/archive_unpack.go`
- Create: `internal/core/workflow/snapshot/archive_verify.go`
- Leave: `internal/core/workflow/snapshot/archive_test.go` AS-IS — keep all tests in one file during Task 1. Test file split happens in Task 3 when archive_test.go's functions live in `archive/` subpkg and naturally group by their source files.

- [x] keep in `archive.go`: types AND shared constants — `PackResult`, `UnpackResult`, `VerificationOutcome`, `ArtifactVerifyReport`, `ArtifactHashMismatch`, `UnpackOptions`, `UnpackCancelledError`, `UnpackVerifyDeclinedError`, `rejectedTarEntryError` + `maxUnpackBytes`/`maxUnpackFiles` (referenced by `archive_inspect.go:52` and `archive_test.go:150-151`)
- [x] move to `archive_pack.go`: `Pack` function + private helpers (globToRegexp, excludesMatch, resolveExistingAncestor) used only by Pack
- [x] move to `archive_unpack.go`: `Unpack` + extractTarGz + mkdirRandom + confirmUnpackOverwrite
- [x] move to `archive_verify.go`: `VerifyExtractedArtifacts` + printVerifyReport + the `Empty()` method on ArtifactVerifyReport
- [x] verify no symbol is referenced from a file other than where it now lives (or stays as a method on a type defined in archive.go)
- [x] **decision committed**: archive_test.go stays in one file during Task 1 — the split happens in Task 3 when test files move to `archive/`
- [x] run `make test ./internal/core/workflow/snapshot/...` — must pass before Task 2
- [x] run `make lint` — must pass before Task 2

### Task 2: Extract `meta/` subpackage (descriptors + scan + atomic write)

**Files:**
- Move + modify: `manifest.go` → `meta/manifest.go` (includes `DevboxFiles` struct, `LastCreate`, `LastRestore`, `StatusOk/Failed/Interrupted`, `LoadManifest`, `SaveManifest`, `NewManifest`, `ArtifactHashMismatch`)
- Move + modify: `paths.go` → `meta/paths.go` (includes `StateDir`, `CurrentPointer`, `LockPath`, `PreRestoreBackup`, `DevboxSubdir`, `ManifestFileName` constants)
- Move + modify: `name.go` → `meta/name.go`
- Move + modify: `vars.go` → `meta/vars.go`
- Move + modify: `current.go` → `meta/current.go`
- **Move + modify: `scan.go` → `meta/scan.go`** (resolves archive→root cycle — see Solution Overview)
- **Create: `meta/atomic.go`** (extracts private `writeFileAtomic` from manifest.go as exported `WriteFileAtomic`)
- Move + modify: `manifest_test.go`, `paths_test.go`, `name_test.go`, `vars_test.go`, `scan_test.go` → `meta/`
- Modify: `devbox_files.go` (4 `writeFileAtomic` callers → `meta.WriteFileAtomic`; `captureDevboxFiles` return type → `meta.DevboxFiles`; `restoreDevboxFiles` parameter type → `meta.DevboxFiles`)
- Modify: `restore.go` (1 `writeFileAtomic` caller in `writePreRestoreBackup` → `meta.WriteFileAtomic`; uses `meta.LoadManifest`, `meta.NewManifest`, `meta.SaveManifest`)
- Modify: all other root .go files that reference Manifest/paths/name/vars/current/StatusX/ScanArtifacts (create.go, remove.go, list.go, exec.go, services_diff.go, archive*.go, archive_inspect.go)

- [ ] create `meta/` directory; move 5 base implementation files + scan.go; change `package snapshot` → `package meta`
- [ ] create `meta/atomic.go` with the body of the existing `writeFileAtomic` (rename to exported `WriteFileAtomic`); delete it from `manifest.go`
- [ ] `ScanArtifacts` is already exported (no rename); `hashFile` needs to become `HashFile` — it's a private helper today but will be called from `archive.VerifyExtractedArtifacts` after Task 3
- [ ] verify exported names: Manifest, NewManifest, LoadManifest, SaveManifest, ArtifactInfo, ArtifactHashMismatch, ServiceSnapshot, ProjectInfo, DevboxFiles (struct), LastCreate, LastRestore, StatusOk/Failed/Interrupted, SnapshotsDir, SnapshotDir, ManifestPath, StateDir, CurrentPointer, LockPath, PreRestoreBackup, DevboxSubdir, ManifestFileName, ValidateName, BuildSnapshotVars, ReadCurrent, WriteCurrent, ClearCurrent, ScanArtifacts, WriteFileAtomic — most already capitalized, only `writeFileAtomic` and `hashFile` need exporting
- [ ] move corresponding test files; update package decl + intra-pkg references
- [ ] in each root .go file: add `import "devbox-cli/internal/core/workflow/snapshot/meta"`; replace bare references with `meta.X` qualified form
- [ ] in `devbox_files.go`: replace 4× `writeFileAtomic(...)` with `meta.WriteFileAtomic(...)`; update `captureDevboxFiles`/`restoreDevboxFiles` to use `meta.DevboxFiles`; the rest of the file (YAML-AST helpers) needs ONLY import-statement and `meta.X` qualified-name updates — no helper refactoring
- [ ] in `restore.go`: replace 1× `writeFileAtomic(...)` in `writePreRestoreBackup` with `meta.WriteFileAtomic(...)`
- [ ] update archive_pack.go, archive_unpack.go, archive_inspect.go, archive_verify.go to use `meta.Manifest` etc. (they still live in root in this task; archive_verify.go's `ScanArtifacts` call becomes `meta.ScanArtifacts`)
- [ ] grep `snapshot\\.` across `internal/cli/snapshot/` and `internal/core/validate/snapshot/` to verify every symbol move has an updated call site
- [ ] run `make test ./internal/core/workflow/snapshot/...` — must pass before Task 3
- [ ] run `make lint` — must pass before Task 3

### Task 3: Extract `archive/` subpackage (tar layer)

**Files:**
- Create: `internal/core/workflow/snapshot/archive/archive.go` (was root archive.go after Task 1: types only)
- Move + modify: `archive_pack.go` → `archive/pack.go`
- Move + modify: `archive_unpack.go` → `archive/unpack.go`
- Move + modify: `archive_verify.go` → `archive/verify.go`
- Move + modify: `archive_inspect.go` → `archive/inspect.go`
- Move + modify: test files split from archive_test.go + archive_inspect_test.go → `archive/`
- Modify: root .go files using Pack/Unpack/UnpackOptions/etc. (create.go, restore.go, list.go, archive_inspect.go's callers)
- Modify: external callers in `internal/cli/snapshot/`, `internal/core/validate/snapshot/`

- [ ] create `archive/` directory; move the 5 archive_*.go files into it; rename to drop `archive_` prefix (the dir name carries that meaning)
- [ ] change `package snapshot` → `package archive`; add `meta` import where needed (verify.go uses meta.Manifest; pack.go signature uses meta.SnapshotsDir or similar coordinates)
- [ ] verify all symbols crossing the new boundary are exported (Pack, Unpack, PackResult, UnpackResult, UnpackOptions, UnpackCancelledError, UnpackVerifyDeclinedError, VerifyExtractedArtifacts, ArtifactVerifyReport, VerificationOutcome, ReadManifestFromTar)
- [ ] move corresponding test files; update package decl + symbol refs
- [ ] in root .go files: add `import "devbox-cli/internal/core/workflow/snapshot/archive"`; replace `Pack(...)` → `archive.Pack(...)`, `Unpack` → `archive.Unpack`, etc.
- [ ] update external callers across `internal/cli/snapshot/`, `internal/core/validate/snapshot/`
- [ ] run `make test ./internal/core/workflow/snapshot/...` — must pass before Task 4
- [ ] run `make lint` — must pass before Task 4

### Task 4: Verify root snapshot package + cross-package callers

**Files:**
- Read-only verification of: `internal/cli/snapshot/`, `internal/core/validate/snapshot/`

- [ ] verify root snapshot/ now contains exactly 9 .go files: create, restore, remove, list, exec, devbox_files, services_diff, scan, observer (+ their test files)
- [ ] grep cli/snapshot/ for `snapshot\\.Manifest`, `snapshot\\.Pack`, etc. — all such references should be qualified to meta/archive
- [ ] grep validate/snapshot/ for the same — should all be updated
- [ ] verify no stale references (e.g. `snapshot.UnpackOptions` left somewhere)
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`

### Task 5: Build verification + manual round-trip smoke test

- [ ] run `make build` — produces `bin/devbox`
- [ ] in a fixture project: `bin/devbox snapshot create test-snap`
- [ ] `bin/devbox snapshot list` — verify entry rendered
- [ ] `bin/devbox snapshot pack test-snap` — verify tar produced
- [ ] `bin/devbox snapshot unpack <tar>` — verify restored
- [ ] `bin/devbox snapshot restore test-snap` — verify restore round-trips state correctly
- [ ] `bin/devbox snapshot remove test-snap`
- [ ] check `make test-race` if not already in `make test`

### Task 6: Update documentation + finalize

**Files:**
- Modify: `docs/internals/packages.md` (section on `internal/core/workflow/snapshot/`)
- Modify: `AGENTS.md` / `CLAUDE.md` — check "Snapshot template scope gate" section for any references to internal type locations
- Move: this plan file → `docs/plans/completed/`

- [ ] update `docs/internals/packages.md` to document the three-tier meta/archive/root layout
- [ ] verify "Snapshot template scope gate" section in CLAUDE.md still accurate (logic lives in `internal/shared/tpl/render_command.go` and `meta.BuildSnapshotVars` — paths only)
- [ ] verify all checkboxes above are `[x]`
- [ ] move plan file: `mkdir -p docs/plans/completed && mv docs/plans/2026-05-29-3-refactor-snapshot-subpackages.md docs/plans/completed/`

## Post-Completion

**Manual verification**:
- Full snapshot lifecycle in a real-ish fixture: create → list → pack → unpack → restore → remove. Verify the tar bytes are not corrupted (`tar tvfz` on the resulting archive).
- Test verify-on-unpack flow: corrupt a byte of a fixture tar, attempt unpack, confirm VerifyExtractedArtifacts catches it.

**External system updates**: none.

**Follow-up plans**:
- `docs/plans/2026-05-29-4-refactor-runtime-subpackages.md` — `spec/` + `runners/`

# Snapshot follow-ups

## Overview

Four follow-up improvements to the snapshot subsystem (completed in `2026-05-24-snapshot-subsystem.md`):

1. **Service-list divergence detection** — when sharing snapshots between machines with different effective service sets, surface the mismatch before `restore` runs, so users do not hit runtime failures mid-workflow.
2. **`local.yml preserve_keys`** — let users mark machine-specific overrides (ports, paths, shell) that should survive `restore` instead of being overwritten by the snapshot's `local.yml`.
3. **Pack/unpack without `.sha256` sidecar** — `pack` writes a single `.tar.gz`; `unpack` verifies extracted artifacts against `manifest.yml` checksums (warn + confirm on mismatch). One-file sharing; transport bitflips already caught by gzip CRC32 + tar; manifest hashes catch in-archive tampering. Not a security boundary — this is a dev tool.
4. **Pipeline-style live UI for snapshot workflows** — `snapshot create/restore/remove` should render per-step status (spinner / ✓ / ✗ / skip / timer) the same way `deploy run` does, instead of plain stdout from `runtime.RunCommand`.

Items 1–3 are correctness/data-safety. Item 4 is UX polish.

## Context (from discovery)

| Concern | Where it lives now | Notes |
|---|---|---|
| Snapshot config schema | `internal/config/snapshot.go` (`SnapshotConfig`) | Strict `KnownFields(true)` decode; needs two new fields (`services_mismatch`, `local_yml`). |
| Snapshot manifest | `internal/snapshot/manifest.go` (`Manifest`, `ProjectInfo`) | Needs `Services []ServiceSnapshot` under `Project`. |
| Snapshot create | `internal/snapshot/create.go` | `ProjectInfo` constructed at lines 199–202; `captureDevboxFiles` at line 263. |
| Snapshot restore | `internal/snapshot/restore.go` | Insertion point for divergence check between `LoadManifest` (line 122) and `confirmRestore` (line 159); `restoreDevboxFiles` at line 325. |
| Archive (pack/unpack) | `internal/snapshot/archive.go` | Sidecar code at lines 46–62 (`PackResult.ChecksumPath` / `VerifiedChecksum`), 175–176 (`Pack` doc), 231 (`hasher`), 331–334 (write sidecar), 353–413 (`VerifyChecksumSidecar` + `Unpack` consumer). `Unpack` already has distrust-safe extract: `filepath.IsLocal`, no symlinks, 50 GiB size cap, 100k entry cap — invariant, do **not** weaken. |
| Pack/unpack CLI | `internal/command/snapshot_pack.go`, `internal/command/snapshot_unpack.go` | `snapshot_unpack.go:70-72` warns on missing sidecar; `snapshot_unpack.go:99` prints "sha256 verified" suffix. Both go away with item 3. |
| Workflow runner | `internal/usercommands/runtime/runner_workflow.go` | Already uses `liveui.LiveLine` for **parallel groups** (lines 40–488) via `newWorkflowParallelLiveLine` indirection. Top-level sequential steps currently fall through to plain stdout via the standard runner path. |
| Snapshot CLI layer | `internal/command/snapshot.go` | Hosts create/restore/remove subcommands; the right place to construct a `*liveui.LiveLine` (in block mode) for the top-level workflow steps. |
| Validator framework | `internal/validate/snapshot/` (already exists from prior plan) | New `snapshot.<name>.services_diff` info-severity validator slots in here. |
| Docs | `docs/reference/config/snapshot.md`, `docs/reference/config/state.md` | Need updates for new config keys, edge cases, and behavior notes. |

**Important architectural finding (for item 3):** the workflow runner already integrates `liveui` for parallel groups and is unit-tested via the `newWorkflowParallelLiveLine` test indirection (`internal/usercommands/runtime/runner_workflow_liveui_test.go`). Item 3 extends the same pattern to sequential steps; it does not introduce a brand-new observer abstraction. The implementation drives a `*liveui.LiveLine` in block mode (`StartBlock` + `SetBlockRowRunning` / `SetBlockRowFinal` + `EndBlock`, see `internal/liveui/liveline.go:310-410`) from the snapshot CLI layer — there is no `LiveBlock` type, just `LiveLine` operating in block mode. Other `type: workflow` user commands keep their existing plain rendering until separately opted in.

## Development Approach

- **Testing approach**: Regular (code first, then tests). Matches the prior snapshot plan and the rest of the codebase: table-driven `*_test.go` next to code, `testdata/` for fixtures.
- Complete each task fully before moving to the next.
- **Every task includes new/updated tests** — listed as separate checklist items, not bundled with implementation.
- All tests must pass before starting the next task. Run with `-race` in the final verification task.
- Run `make lint` after each task that touches Go code.
- Update this plan if scope shifts mid-implementation.

## Testing Strategy

- **Unit tests**: table-driven for the new policy types, manifest service-snapshot round-trip, dot-path strip / merge over `*yaml.Node`, observer wiring.
- **Integration tests**: end-to-end snapshot create→restore exercising
  - a fixture with `services_mismatch.policy: block` and a divergent service set (must fail at restore, before any side effect on `devbox/local.yml`),
  - a fixture with `local_yml.preserve_keys` covering all four edge cases in the table below,
  - a `pack` → mutate-archive → `unpack` round trip for each artifact-mismatch row (missing / hash-mismatch / extra), in both `-y` and interactive modes,
  - a fixture with multi-step `create:` workflow asserting the `LiveLine` block-mode rows rendered the expected lifecycle frames (using the test-mode tee already established for parallel groups).
- **YAML-node fidelity tests**: round-tripping `local.yml` through strip + merge must preserve **key order** and **comments at preserved keys** (best-effort — `yaml.v3` keeps head/foot/line comments on nodes it doesn't rewrite). `yaml.v3` normalizes indentation and flow/block style on marshal, so tests assert *semantic* round-trip + key order + comments-on-untouched-nodes, **not** byte-exact formatting. This is the reason for using `*yaml.Node` over `map[string]any` — order and comment retention, not pixel-perfect formatting.
- **goleak**: existing snapshot test main suffices; observer wiring runs synchronously on the workflow goroutine, no new background work.
- No e2e UI tests in this project.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add new tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Update this file if scope shifts.

## What Goes Where

- **Implementation Steps**: schema, loader, validator, runner, renderer, tests, doc updates.
- **Post-Completion**: cross-machine manual smoke test (share a snapshot to a peer's machine with a different `services/`), since CI cannot simulate that meaningfully.

---

## Implementation Steps

### Task 1: Manifest service capture

- [x] add `ServiceSnapshot{ Name string; Enabled bool }` and `Services []ServiceSnapshot \`yaml:"services,omitempty"\`` to `ProjectInfo` in `internal/snapshot/manifest.go`
- [x] populate it in `internal/snapshot/create.go` (around lines 199–202) from `cfg.Services` (the effective list after `local.yml` overlay — that is what is already merged into `cfg` at the call site)
- [x] sort entries by `Name` before assignment so manifest output is deterministic
- [x] update `internal/snapshot/manifest_test.go` with round-trip cases: zero services, multi-service mix of enabled/disabled, deterministic ordering
- [x] update `internal/snapshot/create_test.go` to assert the populated `Services` slice for a fixture with enabled+disabled services
- [x] run `go test ./internal/snapshot/... && make lint` — must pass before task 2

### Task 2: `services_mismatch` policy in config

- [x] add `ServicesMismatchPolicy{ Policy string \`yaml:"policy"\` }` and a `ServicesMismatch ServicesMismatchPolicy \`yaml:"services_mismatch"\`` field to `SnapshotConfig` in `internal/config/snapshot.go`
- [x] valid values: `warn` (default — empty string also resolves to warn), `block`, `ignore`; validate at load time with a clear "unknown policy" error including the offending value and the allowed set
- [x] expose a typed enum (`ServicesMismatchWarn` / `Block` / `Ignore`) plus a resolver `(ServicesMismatchPolicy).Effective() ServicesMismatchValue` so consumers don't compare raw strings
- [x] write tests for: default policy resolution, each explicit value, unknown value rejection, strict-decode unknown sub-field rejection
- [x] run `go test ./internal/config/... && make lint` — must pass before task 3

### Task 3: Service-divergence comparator

- [x] add `internal/snapshot/services_diff.go` exposing `DiffServices(manifest []ServiceSnapshot, current map[string]config.ServiceConfig) ServicesDiff` returning typed groups: `OnlyInSnapshot []string`, `OnlyLocal []string`, `EnabledDiff []ServiceEnabledDiff{ Name string; ManifestEnabled, LocalEnabled bool }` — note `current` is the map shape used everywhere else in the codebase (`config.DevboxConfig.Services` at `internal/config/devbox.go:104`)
- [x] also expose `FormatServicesDiff(d ServicesDiff) string` (exported, not lowercase) — `internal/command/snapshot.go` (Task 5 inspect) and `internal/validate/snapshot/` (Task 5 validator) both consume it
- [x] the comparator is config-blind beyond service `Name` and `Enabled`; it normalizes the map into a sorted slice internally so it is deterministic
- [x] add `internal/snapshot/services_diff_test.go` covering: identical lists (empty diff), only-in-snapshot, only-local, enabled flipped, deterministic ordering of all three output slices
- [x] run `go test ./internal/snapshot/... && make lint` — must pass before task 4

### Task 4: Wire divergence check into restore

- [x] in `internal/snapshot/restore.go`, between `LoadManifest` (line 122) and `confirmRestore` (line 159), call `DiffServices(m.Project.Services, p.Cfg.Services)`
- [x] introduce a typed `ServicesMismatchError struct { Name string; Diff ServicesDiff }` with `Error() string` returning a lowercase, no-trailing-punct message; the formatted diff body lives on the struct as `Diff`, not as a pre-formatted string, so callers can `errors.As` and re-render in any context
- [x] **extend the restore confirmation callback to carry both signals as a typed struct** (the current `ConfirmRestore func(*Manifest, bool) (bool, error)` at `internal/snapshot/restore.go:36` can't carry the diff):
  ```go
  type RestoreConfirmContext struct {
      Manifest      *Manifest
      ConfigDiverged bool
      ServicesDiff   ServicesDiff   // zero value when ignore policy or no diff
  }
  ConfirmRestore func(RestoreConfirmContext) (bool, error)
  ```
  Update the CLI builder at `internal/command/snapshot_restore.go:136` to read both fields and assemble one prompt line: `"Restore snapshot %q? (config_hash diverged; services diff: <summary>)"`. Single decision point, no double prompt.
- [x] policy dispatch (after callback extension above):
  - `block`: any non-empty diff → return `&ServicesMismatchError{...}`; exit before any side effect on `devbox/local.yml` and before `ConfirmRestore` is even invoked
  - `warn` (default): populate `RestoreConfirmContext.ServicesDiff`, let `ConfirmRestore` render the combined prompt; in `--yes`/`SkipConfirm` mode write the warning to stderr and skip the callback (existing behavior for `SkipConfirm`)
  - `ignore`: leave `RestoreConfirmContext.ServicesDiff` zero, restore proceeds
- [x] the diff is rendered through the exported helper `FormatServicesDiff` (from task 3) so `inspect`, the validator, and the restore prompt share identical wording
- [x] all warnings and prompt text route through the writers already threaded into `RestoreParams` (which the CLI populates from `cmd.ErrOrStderr()` / `cmd.OutOrStdout()`); never reference `os.Stderr` / `os.Stdout` directly — testability invariant
- [x] update `internal/snapshot/restore_test.go` with table-driven cases per policy × diff combination, asserting that no `restoreDevboxFiles` side effect occurs in the `block` and rejected-prompt cases; assert error chain via `errors.As(err, &sme)` not bare type assertion
- [x] run `go test ./internal/snapshot/... && make lint` — must pass before task 5

### Task 5: `snapshot inspect` and validator surfaces the diff

- [x] in `internal/command/snapshot.go` (the existing `inspect` subcommand), call `DiffServices` against the current config when displaying a manifest and render the diff via `FormatServicesDiff` in a `services` section. `inspect` requires a project (it is not in `allowedWithoutProject` at `internal/command/root.go:195`), so the config is always available — no "skip silently" branch. Adding `inspect` to the allowlist for tar-from-anywhere use is intentionally out of scope here.
- [x] add a `snapshot.<name>.services_diff` info-severity validator under `internal/validate/snapshot/`; runs once per snapshot, emits one info diagnostic per snapshot with a non-empty diff, hint quoting `FormatServicesDiff` (truncated to fit the diagnostic Hint width per the existing memory rule on hint formatting)
- [x] register the new validator via the existing `snapshot.All(...)` aggregator (consult `internal/command/validate.go:buildRegistry` to confirm registration site)
- [x] tests: `internal/command/snapshot_inspect_test.go` covers the rendered section for each diff shape; `internal/validate/snapshot/services_diff_test.go` covers the validator output
- [x] run `go test ./internal/snapshot/... ./internal/validate/... ./internal/command/... && make lint` — must pass before task 6

### Task 6: `local_yml.preserve_keys` schema and helper module

- [x] add `LocalYMLPolicy{ PreserveKeys []string \`yaml:"preserve_keys"\` }` and a `LocalYML LocalYMLPolicy \`yaml:"local_yml"\`` field to `SnapshotConfig`
- [x] create `internal/snapshot/devbox_files.go` by **moving** (not duplicating) `captureDevboxFiles` and `restoreDevboxFiles` out of `create.go` / `restore.go` into this new file. **Both signatures change to take `preserveKeys []string`** (the strip/merge helpers need it):
  - `captureDevboxFiles(baseDir, snapDir string, preserveKeys []string) (DevboxFiles, error)` — call site `internal/snapshot/create.go:145` updated to pass `p.SnapCfg.LocalYML.PreserveKeys`
  - `restoreDevboxFiles(snapDir, baseDir string, preserveKeys []string) error` — call site `internal/snapshot/restore.go:174` updated to pass `p.SnapCfg.LocalYML.PreserveKeys`
  - keep the `preserveKeys` parameter required and untyped (`[]string`) rather than fishing it out of a cfg pointer inside the helper — the helper stays config-blind and trivially testable
- [x] add `stripPreservedKeys(yamlBytes []byte, dotPaths []string) ([]byte, error)`:
  - **reject input larger than `localYMLMaxBytes` (1 MiB constant) before parsing** — guards against YAML bombs / alias-explosion since snapshot-embedded `local.yml` is untrusted input from the archive; 1 MiB is orders of magnitude above any realistic `local.yml`
  - parse with `yaml.Unmarshal` into a single `*yaml.Node` (document → mapping)
  - for each dot-path, walk the mapping nodes and remove the matching key (key + value pair removed from `Content` slice)
  - marshal back via `yaml.Marshal(node)`
  - typo/missing path is a silent no-op (per design note); only structural errors (non-mapping at an intermediate segment) surface as a wrapped error: `fmt.Errorf("preserve_keys %q: expected mapping at %q, got %s", path, segment, kind)`
- [x] add `mergePreservedKeys(snapshotBytes, currentBytes []byte, dotPaths []string) ([]byte, error)`:
  - apply the same `localYMLMaxBytes` cap to both inputs
  - parse both into `*yaml.Node`
  - for each dot-path: if the path resolves in `current`, splice the node (value + comments) into the corresponding position in `snapshot`, creating intermediate mappings if missing
  - **type conflict policy**: if the path resolves to incompatible kinds between snapshot and current (e.g., snapshot is scalar at `services.main`, current is mapping at the same path), return a clear error rather than silently overwriting the snapshot's structure — `fmt.Errorf("preserve_keys %q: type conflict (snapshot=%s, current=%s)", path, snapKind, curKind)`
  - marshal the merged snapshot tree
- [x] both helpers operate on `*yaml.Node` (not `map[string]any`) to preserve key order and node-attached comments where `yaml.v3` retains them. Indentation/flow-style normalization on marshal is accepted — we are buying order + comments, not byte-exact formatting
- [x] table-driven tests for both helpers covering: nested paths, missing paths (no-op), creating missing intermediates on merge, round-trip key-order fidelity for a representative `local.yml` sample
- [x] run `go test ./internal/config/... ./internal/snapshot/... && make lint` — must pass before task 7

### Task 7: Wire `preserve_keys` into create and restore

- [x] in `captureDevboxFiles` (now in `devbox_files.go`, takes `preserveKeys []string` per Task 6): after reading `devbox/local.yml`, call `stripPreservedKeys(data, preserveKeys)` before writing to `<snap>/devbox/local.yml`. If the result is structurally empty (zero remaining top-level keys), still write the empty file so restore semantics are unambiguous.
- [x] in `restoreDevboxFiles`: implement the edge-case table:
  | Snapshot has `local.yml` | Current has `local.yml` | Behavior |
  |---|---|---|
  | yes | yes | merge: snapshot overlay + preserved keys spliced from current |
  | yes | no | write snapshot's `local.yml` as-is (preserve_keys no-op) |
  | no | yes (with preserved values) | **new behavior**: write a minimal `local.yml` containing only the preserved keys extracted from current |
  | no | no | no-op (matches existing behavior) |
- [x] thread `cfg.LocalYML.PreserveKeys` through `RestoreParams` and `CreateParams` so the call sites at `create.go:145` and `restore.go:174` can pass it directly to the helpers — do not read from `SnapCfg` deep inside the helpers
- [x] tests in `internal/snapshot/devbox_files_test.go` (new) exercising every row of the edge-case table with a representative `local.yml` fixture in `internal/snapshot/testdata/local-yml/`; existing `create_test.go` / `restore_test.go` get one new case each asserting end-to-end preservation
- [x] run `go test ./internal/snapshot/... -race && make lint` — must pass before task 8

### Task 8: Pack — drop `.sha256` sidecar

- [x] in `internal/snapshot/archive.go`:
  - remove the sidecar write at lines 331–334 (`checksumPath := outPath + ".sha256"` and the subsequent `os.WriteFile`)
  - remove the `ChecksumPath` field from `PackResult` (line 46) — call sites in `internal/command/snapshot_pack.go` must drop references in the same PR; this is intentional per project policy (no back-compat shims)
  - keep the in-memory `Sha256` field on `PackResult` (still useful for the success message and for tests); the running `hasher` (line 231) stays — it now only feeds `PackResult.Sha256`
  - update the `// Pack writes …` doc comment (line 175) to drop the "plus a `.sha256` sidecar" clause
- [x] in `internal/command/snapshot_pack.go`: remove sidecar-related text from `Short` (line 23); the success message at line 70 keeps the `sha256=` field (read from `PackResult.Sha256` in-memory)
- [x] update `internal/snapshot/archive_test.go` to assert **no `*.sha256` file exists** next to the produced `.tar.gz` after a successful pack
- [x] run `go test ./internal/snapshot/... ./internal/command/... && make lint` — must pass before task 9

### Task 9: Unpack — manifest-driven verification with warn + confirm

- [x] in `internal/snapshot/archive.go`:
  - delete `VerifyChecksumSidecar` (lines 353–390) and the sidecar-handling block in `Unpack` (lines 395–413)
  - delete the `VerifiedChecksum` field from the unpack result type (line 62) — replace with the structured outcome below so the CLI can summarize without re-verifying or parsing stderr
  - extend `UnpackResult` with:
    ```go
    type VerificationOutcome int
    const (
        VerificationSkipped VerificationOutcome = iota // NoVerify=true bypassed the check
        VerificationClean                              // verification ran, all groups empty
        VerificationWarned                             // verification ran, ≥1 group non-empty, user (or AssumeYes) accepted
    )
    // added to UnpackResult:
    Verification       VerificationOutcome
    VerifyReport       ArtifactVerifyReport // zero value when Verification == VerificationSkipped
    ```
    `Verification` is the discriminator; `VerifyReport` carries the actual groups. The CLI summary in Task 10 reads both directly.
  - **do not** weaken any distrust-safe extract invariant (`filepath.IsLocal`, no-symlinks, 50 GiB size cap, 100k entry cap) — these stay verbatim
- [x] **preserve the existing overwrite-with-confirmation flow** at `archive.go:416`–`462`:
  - existing behavior: if `finalDir` exists, prompt via `confirmUnpackOverwrite` (or skip on `SkipConfirm`); extract to `mkdirRandom(snapshotsRoot, ".unpack-")` staging; on success, rename `finalDir` → backup, rename staging → `finalDir`, then remove backup; on second-rename failure, restore the backup. This rollback path is load-bearing — do **not** simplify it away.
  - **reuse the existing `mkdirRandom` helper** at `archive.go:645`–`661` — it already uses `crypto/rand` for the suffix, blocking TOCTOU symlink pre-creation. No new `MkdirTemp` call needed.
  - the only structural change: insert `VerifyExtractedArtifacts` after `LoadManifest` and before the rename-into-place block. On declined verification, run the existing `cleanupStaging()` and return `&UnpackVerifyDeclinedError{...}` — `finalDir` is untouched because verification fires before the rename-old-aside step.
- [x] add post-extract verification helper `VerifyExtractedArtifacts(stagingDir string, m *Manifest) (ArtifactVerifyReport, error)`:
  - **path-safety gate before opening any file**: `manifest.yml` is archive-controlled, so each `m.Artifacts[i].Path` must be validated *before* `os.Open`. Reject if `filepath.IsAbs(path)` or `!filepath.IsLocal(path)` (clean, no `..`, no absolute, no Windows drive). Then resolve the absolute child as `absChild := filepath.Join(absStagingDir, filepath.FromSlash(path))` — `FromSlash` because manifest paths are normalized to forward-slashes and `Join`/`ContainedRel` need OS-native separators on Windows. Call `pathsafe.ContainedRel(absStagingDir, absChild)` and treat a non-nil error as a fatal verification failure (`return ArtifactVerifyReport{}, fmt.Errorf("verify: manifest artifact path %q escapes staging: %w", path, err)`). This blocks an attacker-crafted manifest from making the verifier read `/etc/passwd` or sibling-snapshot files.
  - after the safety gate, re-hash each artifact by streaming `io.Copy(sha256.New(), f)` (never `io.ReadAll`) — same constraint as the prior plan's scanner
  - record three groups in `ArtifactVerifyReport`: `Missing []string` (in manifest, not on disk), `HashMismatch []ArtifactHashMismatch{Path, ExpectedSha256, ActualSha256}`, `Extra []string` (on disk, not in manifest — walked via the existing `ScanArtifacts` helper)
  - returns an error only on I/O failure during hashing **and on path-safety failure** (the latter is treated as a hard error, not a warn-and-continue, because it indicates a malicious or corrupt manifest)
  - tests must cover: manifest with `../escape`, manifest with absolute path `/etc/passwd`, manifest with a path that `filepath.Clean`s to escape via symlink staging interactions — all rejected as errors, not surfaced as `Missing`
- [x] introduce `UnpackOptions` that **preserves the existing overwrite-confirm contract** (`Unpack` today takes `skipConfirm bool` + `confirmOverwrite func() (bool, error)` at `archive.go:400`/`:416`) and adds the verify-confirm leg, with the two prompt callbacks kept distinct:
  ```go
  type UnpackOptions struct {
      NoVerify        bool                       // bypass artifact verification entirely
      AssumeYes       bool                       // -y: skip both overwrite and verify prompts
      ConfirmOverwrite func() (bool, error)      // existing prompt callback, preserved verbatim
      ConfirmVerify    func(prompt string) (bool, error)  // new: prompt when Missing/HashMismatch non-empty
      Stderr           io.Writer                 // plumbed from cmd.ErrOrStderr() — never os.Stderr
  }
  ```
  Keeping the two callbacks separate matches `-y` semantics: `AssumeYes` collapses *both* prompts to "yes" without conflating them, and the CLI can plug different prompt wording / themes into each leg.
- [x] introduce `type UnpackVerifyDeclinedError struct { Report ArtifactVerifyReport }` with lowercase no-trailing-punct `Error() string`; carries the typed report (not a pre-formatted string) so callers can `errors.As` and re-render
- [x] decision policy in `Unpack`:
  - `NoVerify: true` → **always print `warning: skipping artifact verification at user request (--no-verify)` to Stderr** (security UX: the bypass is visible in CI logs and post-mortems), then skip `VerifyExtractedArtifacts` entirely, rename staging → final, return
  - otherwise call `VerifyExtractedArtifacts`; if all three groups empty, rename and return silently
  - for each non-empty group, write the warning to `Stderr` in the documented wording table; `Missing` and `HashMismatch` additionally trigger a single grouped confirmation prompt (`continue? [y/N]`); `Extra` is info-only, no prompt
  - `AssumeYes: true` → confirmation auto-accepts but warnings still print
  - prompt declined → `os.RemoveAll(stagingDir)`, return `&UnpackVerifyDeclinedError{Report: report}`
- [x] warnings are *stderr text*, not `slog.Warn` calls — preserves the single-handling rule (don't both log and return); `Unpack` returns errors only on I/O failures and declined prompts, never on diff content itself
- [x] tests in `internal/snapshot/archive_test.go`:
  - happy path: clean archive, no warnings, rename succeeds
  - missing artifact: prompt declined → staging cleaned up, no final dir; prompt accepted → final dir created, warning on stderr
  - hash mismatch: same matrix as missing
  - extra artifact in archive: warning printed, no prompt, rename succeeds
  - `NoVerify`: corrupted archive (real hash mismatch) extracts cleanly; test asserts **the explicit `warning: skipping artifact verification …` line is present and no artifact-mismatch warnings are emitted** (consistent with the policy block above)
  - `AssumeYes`: missing + mismatched + extra all present → both warnings print, rename succeeds
- [x] run `go test ./internal/snapshot/... -race && make lint` — must pass before task 10

### Task 10: Unpack CLI surface

- [x] in `internal/command/snapshot_unpack.go`:
  - remove the "no sidecar" warning block at lines 70–72 and the "sha256 verified" suffix at line 99
  - add `--no-verify` local flag wired into `UnpackOptions.NoVerify`
  - the existing `--yes` / `-y` flag (root or local, whichever already governs snapshot confirmations) maps to `UnpackOptions.AssumeYes`; verify the wiring exists, add it if missing
  - wire **both** prompt callbacks explicitly (Task 9 split them — there is no `ConfirmFn` field anymore):
    - `UnpackOptions.ConfirmOverwrite`: reuse the existing overwrite prompt the current `Unpack` callers pass at `archive.go:400`/`:416` — same wording and theme, no behavior change
    - `UnpackOptions.ConfirmVerify`: build from `ui.RunConfirm` (or the local snapshot-confirm helper) with a "continue despite verification warnings?" prompt; default `no`
  - print a one-line summary on success driven by `UnpackResult.Verification`:
    - `VerificationSkipped` → `(verification skipped)`
    - `VerificationClean` → `(verified)`
    - `VerificationWarned` → `(verified with N warnings)` where `N = len(VerifyReport.Missing) + len(VerifyReport.HashMismatch) + len(VerifyReport.Extra)`
    - never re-run verification just to print the summary, and never parse stderr to reconstruct it — both fields are already on the result
- [x] tests in `internal/command/snapshot_unpack_test.go`: cover each flag combination against a fixture archive with intentional mismatches (use the helpers built in task 9); assert exit code, stderr content, and final-dir presence
- [x] run `go test ./internal/command/... && make lint` — must pass before task 11

### Task 11: Documentation for items 1, 2, and 3

- [x] `docs/reference/config/snapshot.md`:
  - add a `services_mismatch` section with the three policy values, what each diff group means, and the recommended default
  - add a `local_yml.preserve_keys` section with the dot-path syntax, a worked example (ports), and the four-row edge-case table from task 7
  - rewrite the `pack` and `unpack` sections: pack produces only `<name>.tar.gz`; unpack verifies against `manifest.yml` by default with warn+confirm semantics; document `--no-verify` and `-y` flags
  - remove every mention of `.sha256` sidecar from this file
- [x] `docs/reference/config/state.md`: note that `deploy-state.yml` is overwritten on restore (orphan entries are safe — deploy ignores them), so no merge is performed for it
- [x] `docs/internals/packages.md`: update the `internal/snapshot/` entry to mention the new `devbox_files.go` and `services_diff.go` files, the staging-rename + backup-rollback extract pattern in `archive.go`, and the manifest-driven artifact verification with path-safety gate
- [x] no test step — docs-only task
- [x] run `make lint` — must pass before task 12

### Task 12: Snapshot CLI live view — design and reusable observer

- [x] read `internal/usercommands/runtime/runner_workflow.go` end-to-end first; specifically check whether top-level sequential steps already emit any lifecycle signal (start / end / skip / fail) that the snapshot CLI can subscribe to. If yes, reuse it; if no, add the **minimal** hook needed.
- [x] introduce a **2-method** `runtime.WorkflowStepObserver` interface (collapses `end` and `skip` into a single `OnStepEnd` with a `StepResult` struct — easier to extend without breaking implementers, stays under the "small interface" threshold):
  ```go
  type WorkflowStepObserver interface {
      OnStepStart(idx, total int, step model.WorkflowStep)
      OnStepEnd(idx int, step model.WorkflowStep, result StepResult)
  }
  type StepStatus int
  const (
      StepStatusDone StepStatus = iota
      StepStatusFailed
      StepStatusSkipped
  )
  type StepResult struct {
      Status     StepStatus
      Duration   time.Duration
      Err        error  // populated when Status==Failed
      SkipReason string // populated when Status==Skipped
  }
  ```
- [x] add an optional `StepObserver WorkflowStepObserver` field to `runtime.RunContext`. **Nil observer → current behavior unchanged.** No `noopObserver` type — the runner just skips the call when the field is nil.
- [x] **lifecycle is a distinct concern** — `WorkflowStepObserver` stays a pure event sink (no `Close`). Define a separate composable interface for callers that own a resource needing teardown:
  ```go
  // in internal/snapshot (the only caller that defers Close); not in runtime
  type StepObserverCloser interface {
      runtime.WorkflowStepObserver
      Close()
  }
  ```
  The factory typed below returns `StepObserverCloser`; snapshot package owns the `defer obs.Close()`. The runner only ever sees the embedded `WorkflowStepObserver` slice of methods, keeping the runner's contract minimal.
- [x] **plumb the observer through the snapshot exec layer as a factory, not a constructed instance** — two reasons:
  1. `internal/snapshot/exec.go` constructs `runtime.RunContext` internally at line 73, so the command layer cannot set `runCtx.StepObserver` directly.
  2. Both `Restore` and `Remove` perform interactive `huh` prompts via `ui.RunConfirm` *before* the workflow runs (`internal/snapshot/restore.go:159`, `internal/snapshot/remove.go:82`). A live footer/block active during a huh prompt corrupts the TUI — pipeline solves this with `ui.SetHuhHooks(live.Pause, live.Resume)` at `internal/pipeline/plain.go:151`, but the cleanest fix here is to **defer observer construction until after all prompts**.
  3. `SelectWorkflow` lives inside `internal/snapshot/{restore,remove}.go` and depends on manifest data (restore at `restore.go:151`, remove at `remove.go:94` — and remove tolerates a corrupt manifest via `LoadManifest` ignored-error at `remove.go:80`). Pulling `SelectWorkflow` up to the command layer would duplicate semantics and risks changing the corrupt-manifest remove path.
- [x] add `StepObserverFactory func(steps []model.WorkflowStep) StepObserverCloser` to `ExecParams` — **unprefixed** because `ExecParams` lives in `internal/snapshot` itself; same for the re-exports on `CreateParams` / `RestoreParams` / `RemoveParams`. Command-layer call sites use the qualified `snapshot.StepObserverCloser` because they live in `internal/command`. The snapshot package invokes the factory **after** `SelectWorkflow` and **after** its pre-workflow confirmation prompts, immediately before calling into the runner. `exec.go` assigns the returned value into `rc.StepObserver` (Go's structural typing handles the upcast — `StepObserverCloser` embeds `runtime.WorkflowStepObserver`). A `nil` factory disables the observer (current behavior). The factory may itself return `nil` (e.g. when `--no-live` is set or stdout isn't a TTY) and the runner's nil-observer path takes over — snapshot guards the `defer obs.Close()` with a nil check.
- [x] wire the runner to call the observer at top-level sequential step boundaries only; parallel groups continue to render via their existing `*liveui.LiveLine` block-mode path in `runner_workflow.go` (the observer sees the group as a single step, with the parallel block rendered nested underneath as today). Do not duplicate per-row events for parallel sub-steps in the observer surface — keeps the API small.
- [x] **suspend the live footer around every sequential command step** — even with `OnStepStart`/`OnStepEnd` wired, the child process still writes to `rc.Stdout` / `rc.Stderr` at `runner_workflow.go:224` while the live block is active, which corrupts the rendered rows. Pipeline solves this with `SuspendForExec` / `ResumeAfterExec` around every sequential step body at `internal/pipeline/reporter.go:88-99` and `internal/pipeline/executor.go:718-723`. Mirror that here as an **optional capability**, not a method on `WorkflowStepObserver` (which stays at 2 methods):
  ```go
  // in internal/usercommands/runtime — separate from WorkflowStepObserver
  type StepIOSuspender interface {
      SuspendForExec()  // pause/hide the live footer for the duration of the child
      ResumeAfterExec() // repaint after the child returns; idempotent
  }
  ```
  Runner uses the comma-ok type assertion pattern (from the `golang-structs-interfaces` skill — "optional behavior with type assertions"), but **must not use `defer` inside the step loop** — `defer` in `WorkflowRunner.Run` fires at function return, not per iteration, which would leave the footer suspended across subsequent steps and stack up resume calls. Use an iteration-scoped closure so the resume always pairs with the suspend regardless of return/panic paths, and emit observer events around the suspend window so the exact order is preserved:
  ```go
  // pseudocode for the sequential default branch in runner_workflow.go:125
  // Order: OnStepStart → SuspendForExec → child output → ResumeAfterExec → OnStepEnd
  err := func() error {
      onStart(i, total, step) // OnStepStart fires before any live-mode mutation
      suspender, hasSuspend := rc.StepObserver.(StepIOSuspender)
      if hasSuspend {
          suspender.SuspendForExec()
      }
      defer func() {
          if hasSuspend {
              suspender.ResumeAfterExec() // iteration-scoped: fires before this closure returns
          }
      }()
      return r.runCommandStep(ctx, rc, i, step)
  }()
  // OnStepEnd fires *after* the closure returns (so after Resume), with the
  // appropriate StepResult derived from err and elapsed time.
  ```
  Wrap *only* the sequential `runCommandStep` path (default-branch at `runner_workflow.go:125`). Parallel groups (`step.Parallel != nil`) already manage their own output via the inner `LineTeePreserveANSI` at `runner_workflow.go:441` and must not be suspended. `confirm:` steps are also out of scope (they use huh, which already pause/resumes via the hooks installed in Task 13).
- [x] `snapshotLiveObserver` implements `StepIOSuspender` by delegating to **the observer's own depth-counted `pause()` / `resume()` helpers** (defined in Task 13), not directly to `live.Pause()` / `live.Resume()`. The depth counter is shared with the huh hook bridge so nested suspends from `ConfirmCommand` (`internal/usercommands/runtime/runner.go:207`) compose correctly. Compile-time assertion: `var _ runtime.StepIOSuspender = (*snapshotLiveObserver)(nil)`.
- [x] **every early-`continue` branch in the top-level step loop must emit `OnStepEnd` before continuing** — otherwise live rows stay in the spinner state forever. Concretely, in `internal/usercommands/runtime/runner_workflow.go`:
  - line ~94, `when:` evaluated false → fire `OnStepEnd(i, step, StepResult{Status: StepStatusSkipped, SkipReason: "when: " + step.When})` before the existing `continue`
  - line ~122, `files_gate` override gate returned skip → fire `OnStepEnd(i, step, StepResult{Status: StepStatusSkipped, SkipReason: gateReason})` before `continue`
  - line ~105 and ~128, parallel-group and command-step paths returning an error swallowed by `continue_on_error` → fire `OnStepEnd(i, step, StepResult{Status: StepStatusFailed, Err: err})` before `continue` (the row goes red even though the workflow keeps going — that's the correct UX signal)
  - the normal success path already fires `OnStepEnd(... Status: StepStatusDone, Duration: elapsed)` after `runCommandStep` returns nil; no change there
  - the hard-fail path (returning `err` to the caller) also fires `OnStepEnd(... Status: StepStatusFailed, Err: err)` before the `return err`, so the row freezes red before the function unwinds
- [x] write `internal/usercommands/runtime/runner_workflow_observer_test.go` with one table case per branch:
  - happy-path sequential → `OnStepStart` then `OnStepEnd{Done}` per step, ordered
  - `when:` false → `OnStepEnd{Skipped, SkipReason: "when: ..."}` and **no** `OnStepStart` (the step never started)
  - `files_gate` skip → same pattern with the gate's reason
  - hard failure → `OnStepEnd{Failed, Err: ...}` then loop exits
  - `continue_on_error` failure (both command and parallel variants) → `OnStepEnd{Failed, Err: ...}` then the next step fires its own `OnStepStart`
  - **`confirm:` step accepted** → `OnStepStart` then `OnStepEnd{Done}`; the test injects a stubbed `ui.RunConfirm` that returns `(true, nil)` and asserts the observer surface stays clean (no extra events)
  - **`confirm:` step aborted** → `OnStepStart` then `OnStepEnd{Failed, Err: "aborted by user"}`; the stub returns `(false, nil)` and the runner converts to the existing `fmt.Errorf("aborted by user")` at `runner_workflow.go:163`
  - **sequential command steps with stdout output while live mode is active** → use a workflow with **two** command steps so the per-iteration scoping is testable; fake observer implements both `WorkflowStepObserver` and `StepIOSuspender` and records every method call (with sequence number) into a shared slice. Assertions on the full call order:
    1. `OnStepStart(0)` → `SuspendForExec` → step-0 child output → `ResumeAfterExec` → `OnStepEnd(0, Done)` → `OnStepStart(1)` → `SuspendForExec` → step-1 child output → `ResumeAfterExec` → `OnStepEnd(1, Done)`
    2. `SuspendForExec` and `ResumeAfterExec` each fire exactly twice total (once per step) — never stacked across iterations
    3. `ResumeAfterExec` for step 0 is observed **before** `OnStepStart(1)` — proves the `defer` is iteration-scoped, not function-scoped
  - same two-step fixture with the first step failing (hard fail, no `continue_on_error`): asserts `ResumeAfterExec` still fires before `OnStepEnd(0, Failed)` and the second step is never started (no `OnStepStart(1)`, no second suspend/resume pair)
  - same two-step fixture where the observer implements only `WorkflowStepObserver` (no `StepIOSuspender`): runner skips suspend/resume entirely via the comma-ok guard; only `OnStepStart`/`OnStepEnd` are recorded
  - **command step with `confirmation: true` and stdout output after the prompt** → uses a `type: shell` command marked `confirmation: true` whose `run:` prints to stdout, so `RunCommand` calls `ConfirmCommand` (`runner.go:207` → `confirmation.go:53`) *inside* the step suspender's pause window. Assert via a `LiveLine` wrapped to record every `Pause()` / `Resume()` call:
    1. `live.Pause()` fires exactly **once** (from `SuspendForExec`) at the start of the step
    2. The huh prompt fires its before/after hooks (which go through the observer's depth counter — should NOT call `live.Pause()` or `live.Resume()` since the counter is already at 1, transitions to 2, then back to 1)
    3. The command body's stdout lands while the live state is still `paused` (no `live.Resume()` observed yet)
    4. `live.Resume()` fires exactly **once** (from `ResumeAfterExec`) at the end of the step
    Specifically: across the entire command step, `live.Pause()` is called once and `live.Resume()` is called once — proving the huh hooks did not break the suspend window. This is the load-bearing test for the depth-counting design.
  - nil observer → identical plain-stdout output and no panics (table-driven against the same fixtures)
- [x] run `go test ./internal/usercommands/runtime/... -race && make lint` — must pass before task 13

### Task 13: Snapshot live-view observer in CLI layer

- [x] add `internal/command/snapshot_liveui.go` exposing `newSnapshotLiveObserver(termOut, screen io.Writer, isTTY bool, steps []model.WorkflowStep) snapshot.StepObserverCloser` — returns the closer interface (not the bare `runtime.WorkflowStepObserver`) so the snapshot package's `defer obs.Close()` path works; returns `nil` for the disabled case
  - on construction: `live := liveui.NewLiveLine(termOut, screen, isTTY)`; call `live.Start()`, then `live.StartBlock(len(steps))`; cache `live` plus a `labels []string` precomputed from `step.Command` (or a synthesized label for `parallel:` / `confirm` blocks)
  - `OnStepStart(idx, total, step)` → `live.SetBlockRowRunning(idx, labels[idx])`
  - `OnStepEnd(idx, step, result)` dispatches on `result.Status` — **labels never include the duration**; `LiveLine` renders `  <icon> [elapsed] label` itself at `internal/liveui/liveline.go:461` from per-row start/finalize timestamps, so duplicating it here would print `[1.2s] task  1.2s`:
    - `StepStatusDone` → `live.SetBlockRowFinal(idx, liveui.BlockRowDone, labels[idx])`
    - `StepStatusFailed` → `live.SetBlockRowFinal(idx, liveui.BlockRowFailed, labels[idx] + ": " + result.Err.Error())`
    - `StepStatusSkipped` → `live.SetBlockRowFinal(idx, liveui.BlockRowSkipped, labels[idx] + " — " + result.SkipReason)`
  - `result.Duration` is still available in the event and used by tests asserting "step took N", but it does not go into the rendered label
  - **install depth-counted pause/resume bridge for huh hooks AND `StepIOSuspender`** — three independent callers can request "footer hidden" during a workflow:
    1. `StepIOSuspender.SuspendForExec` / `ResumeAfterExec` (Task 12, around the whole sequential command step)
    2. The huh hooks installed for workflow `confirm:` steps (`runner_workflow.go:155`)
    3. The huh hooks fired by `ConfirmCommand` *inside* `RunCommand` (`internal/usercommands/runtime/runner.go:207` → `confirmation.go:53`) — which happens **while the step suspender already has the footer paused**
    Without depth counting, the huh after-hook from case (3) would call `live.Resume` mid-step and re-paint the footer right before the command body writes stdout. Pipe the hooks through the observer's own counter instead of `live.Pause`/`live.Resume` directly:
    ```go
    // on the snapshotLiveObserver struct
    type snapshotLiveObserver struct {
        live       *liveui.LiveLine
        pauseDepth atomic.Int32 // shared across all pause sources
        // ...
    }
    func (o *snapshotLiveObserver) pause() {
        if o.pauseDepth.Add(1) == 1 { // transition 0 → 1: real pause
            o.live.Pause()
        }
    }
    func (o *snapshotLiveObserver) resume() {
        if o.pauseDepth.Add(-1) == 0 { // transition 1 → 0: real resume
            o.live.Resume()
        }
    }
    // wire-up at construction:
    ui.SetHuhHooks(o.pause, o.resume)
    // StepIOSuspender impl also uses these:
    func (o *snapshotLiveObserver) SuspendForExec()  { o.pause() }
    func (o *snapshotLiveObserver) ResumeAfterExec() { o.resume() }
    ```
    Use `atomic.Int32` (not a plain int) because huh's form runs synchronously on the caller's goroutine, but `live.tickLoop` runs in a separate goroutine and reads `live.paused` under `live.mu`. Atomic Add gives the right transition semantics without a separate mutex on the observer.
    `Close()` calls `ui.ClearHuhHooks()` *before* `live.EndBlock()` + `live.Stop()`. Per `internal/ui/huh.go:12-16`, each prompt snapshots the hook pair at entry and uses the snapshotted after-hook in a `defer`, so `ClearHuhHooks` cannot interrupt a prompt already in flight — it only prevents *future* prompts (anywhere else in the process) from calling back into a stopped `LiveLine`. Ordering still matters because user-command tear-down can race the snapshot post-workflow path; clearing first guarantees no later prompt re-enters our hooks. The single-active-reporter invariant from `internal/ui/huh.go:29` is preserved because snapshot and deploy serialize via the project-lock acquire-both pattern — only one of them holds the hooks at a time.
  - expose a `Close()` method on the observer struct that runs in order: `ui.ClearHuhHooks()` → `live.EndBlock()` → `live.Stop()`; **the snapshot package owns the `defer obs.Close()`** (per Task 12) — the CLI layer never calls `Close()` itself, never holds the observer reference past factory construction
  - when `isTTY` is false (CI, redirected stdout, `--no-live` set), **return `nil`** from `newSnapshotLiveObserver` so the runner's nil-observer path produces plain output — no `noopObserver` type, no `Close()` to defer
- [x] add **two** compile-time interface assertions so both contracts break the build if drifted:
  ```go
  var _ runtime.WorkflowStepObserver = (*snapshotLiveObserver)(nil)
  var _ snapshot.StepObserverCloser  = (*snapshotLiveObserver)(nil)
  ```
- [x] wire the **factory** into `runSnapshotCreate`, `runSnapshotRestore`, and `runSnapshotRemove` in `internal/command/snapshot.go`: assign `params.StepObserverFactory = func(steps []model.WorkflowStep) snapshot.StepObserverCloser { return newSnapshotLiveObserver(termOut, screen, isTTY, steps) }` and call `snapshot.Create` / `Restore` / `Remove` as before. The factory is invoked inside the snapshot package after `SelectWorkflow` and after the pre-workflow confirmation prompt (per Task 12). Pre-workflow prompts therefore run before any live UI exists. Mid-workflow `confirm:` steps (and command-level `confirmation:` prompts dispatched by `ConfirmCommand`) are handled by the depth-counted bridge installed via `ui.SetHuhHooks(o.pause, o.resume)` during observer construction earlier in this task. **The command layer never references the observer after handing the factory off — lifecycle ownership lives entirely inside `internal/snapshot`.**
- [x] **add `--no-live` as a snapshot-specific local flag** on `snapshot create`, `restore`, `remove` (pipeline has no equivalent; explicit decision: snapshots get the flag because users running these under wrapper scripts will want plain output without faking a non-TTY). When set, `newSnapshotLiveObserver` returns `nil` and stdout falls back to the existing plain-text path.
- [x] tests: `internal/command/snapshot_liveui_test.go` using the same `newWorkflowParallelLiveLine`-style test indirection (introduce `newSnapshotObserverLiveLine` in `snapshot_liveui.go` as a package-level `var` so tests can swap to a capturing writer), asserting the emitted frame sequence for a happy-path, a failed-step, and a skipped-step workflow, plus the `--no-live` case (returns `nil`, no frames)
- [x] run `go test ./internal/command/... -race && make lint` — must pass before task 14

### Task 14: Documentation for item 4

- [ ] `docs/reference/config/snapshot.md`: add a brief note that `snapshot create/restore/remove` now render per-step live status; document the `--no-live` opt-out if it was added in task 13
- [ ] `docs/internals/packages.md`: update the `internal/command/snapshot.go` and `internal/usercommands/runtime/` entries with one line about the observer surface
- [ ] no test step — docs-only task

### Task 15: Final verification

- [ ] re-read `Overview` and confirm each of the four items is implemented end-to-end
- [ ] confirm Task 12's observer is opt-in (nil → no change) and existing workflow tests still pass unchanged
- [ ] run `make test` (full suite)
- [ ] run `go test ./... -race`
- [ ] run `make lint` — no warnings
- [ ] manually grep for any leftover `TODO`/`FIXME` introduced in the diff and resolve

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

---

## Technical Details

### Manifest schema delta (item 1)

```yaml
project:
  name: my-app
  config_hash: 0c1f...
  services:                  # NEW — sorted by name
    - { name: main, enabled: true }
    - { name: db,   enabled: true }
    - { name: cdn,  enabled: false }
```

### snapshot.yml schema delta (items 1 + 2)

```yaml
# existing fields …
services_mismatch:
  policy: warn          # warn (default) | block | ignore

local_yml:
  preserve_keys:
    - services.main.ports
    - services.db.ports
    - host.shell
```

### Service-diff data shape (item 1)

```go
type ServicesDiff struct {
    OnlyInSnapshot []string             // names — likely runtime failures
    OnlyLocal      []string             // names — overwrite drops journal entries
    EnabledDiff    []ServiceEnabledDiff // name + manifest/local enabled flags
}
type ServiceEnabledDiff struct {
    Name            string
    ManifestEnabled bool
    LocalEnabled    bool
}
```

### Unpack verification report and result (item 3)

```go
type ArtifactVerifyReport struct {
    Missing      []string                // in manifest, not on disk
    HashMismatch []ArtifactHashMismatch  // both present, hashes differ
    Extra        []string                // on disk, not in manifest
}
type ArtifactHashMismatch struct {
    Path           string
    ExpectedSha256 string
    ActualSha256   string
}

// VerificationOutcome is the discriminator the CLI summary line reads.
// Replaces the deleted UnpackResult.VerifiedChecksum field.
type VerificationOutcome int
const (
    VerificationSkipped VerificationOutcome = iota // NoVerify=true bypassed the check
    VerificationClean                              // verification ran, all groups empty
    VerificationWarned                             // verification ran, ≥1 group non-empty, user (or AssumeYes) accepted
)

// UnpackResult fields added by Task 9 (alongside the existing path / size /
// manifest-pointer fields the current type already carries):
//
//   Verification VerificationOutcome
//   VerifyReport ArtifactVerifyReport  // zero value when Verification == VerificationSkipped
//
// CLI summary in Task 10:
//   Skipped → "(verification skipped)"
//   Clean   → "(verified)"
//   Warned  → fmt.Sprintf("(verified with %d warnings)",
//                         len(VerifyReport.Missing) +
//                         len(VerifyReport.HashMismatch) +
//                         len(VerifyReport.Extra))
```

Wording table for warnings (single source of truth for stderr text):

| Group | Stderr line | Triggers confirm? |
|---|---|---|
| `Missing` | `warning: artifact %q listed in manifest is missing from archive` | yes (grouped) |
| `HashMismatch` | `warning: artifact %q sha256 mismatch (manifest=%s, actual=%s)` | yes (grouped) |
| `Extra` | `info: archive contains %q not listed in manifest` | no |

`Missing` and `HashMismatch` share a single confirmation prompt at the end (one yes/no for the whole batch), default `no`.

### Observer interface (item 4)

```go
package runtime

type StepStatus int
const (
    StepStatusDone StepStatus = iota
    StepStatusFailed
    StepStatusSkipped
)

type StepResult struct {
    Status     StepStatus
    Duration   time.Duration
    Err        error  // populated when Status==Failed
    SkipReason string // populated when Status==Skipped
}

type WorkflowStepObserver interface {
    OnStepStart(idx, total int, step model.WorkflowStep)
    OnStepEnd(idx int, step model.WorkflowStep, result StepResult)
}
```

Two methods, not three — `OnStepEnd` covers done/failed/skipped via `StepResult`. Easier to extend with new lifecycle metadata (e.g., bytes-written) without breaking implementers.

### Explicit non-goals

- **No** `${snapshot.has_service.X}` template predicate. Users rely on `when: file-exists ${snapshot.path}/<artifact>`, which already works.
- **No** merge of `deploy-state.yml` on restore. Overwrite remains; orphan entries are safe.
- **No** array-index dot paths in `preserve_keys` (`services[0].ports`). `local.yml` is maps-of-maps.
- **No** `local_yml.include_keys` whitelist. Blacklist covers the typical "preserve a few" case.
- **No** new `notify.Op` for snapshot lifecycle UI; notifications stay on `OpCommand` per the prior plan's decision.
- **No** rewrite of `runtime.RunCommand`'s plain rendering for non-snapshot workflows. Observer is opt-in; other `type: workflow` commands keep current output.
- **No** `.sha256` sidecar in pack output, and **no** sidecar consumed by unpack. One file = one snapshot.
- **No** signatures, GPG, or cryptographic provenance on archives. This is a dev tool; gzip CRC32 + tar structural validity + manifest hashes are the trust boundary.
- **Manifest verification is integrity-of-record, not authenticity.** It catches accidental mutation, partial truncation, and casual in-archive tampering. It does **not** catch an attacker who re-packs the archive with a self-consistent manifest (they can simply rewrite `manifest.yml` to match the new artifacts). Acceptable trade-off for a dev tool — call out in `docs/reference/config/snapshot.md` under unpack so users do not over-trust the verification line.
- **`Extra` artifacts are extracted to disk** (under the distrust-safe constraints — local path, no symlinks, size/entry caps), then reported as info. They are not silently discarded. Strict mode (prompt on extras) is a possible follow-up if real-world misuse appears; not v1.
- **No** weakening of `Unpack`'s distrust-safe extract invariants (`filepath.IsLocal`, no-symlinks, 50 GiB size cap, 100k entry cap). Staging-dir + rename layers on top, never replaces.
- **No** change to the `snapshot.<name>.checksums` validator. It already operates on already-unpacked snapshots; same verification logic, no extract step.

---

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification:**
- Cross-machine smoke test: create a snapshot on machine A with services `{main, db, cdn}`, ship to machine B which has services `{main, db, search}`. Verify each policy (`warn`/`block`/`ignore`) behaves as documented, and that `local.yml` preserved keys (ports) on machine B survive restore.
- Pack on machine A, scp the single `.tar.gz` to machine B, `devbox snapshot unpack` — confirm: no sidecar required, verification runs, success message reads `(verified)`. Then deliberately corrupt one artifact inside the archive (open with `tar` tooling, re-pack), unpack again, confirm warning + prompt fire; confirm `--no-verify` bypass works.
- Visual check that the live UI matches `deploy run` styling (spinner, icons, durations) on a multi-step `create` and `restore` workflow.

**External system updates:**
- None — pre-release CLI with no external consumers per `CLAUDE.md` project policy.

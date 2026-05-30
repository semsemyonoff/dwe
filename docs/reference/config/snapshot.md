# snapshot.yml

Declarative snapshot workflows: capture the state of a devbox project (databases, indices, devbox local config, deploy state) into a named directory under `./snapshots/<name>/` and restore or roll back to it.

## Contents

- [Purpose](#purpose)
- [Worked example: switch between tasks (UC-3)](#worked-example-switch-between-tasks-uc-3)
- [File location](#file-location)
- [Top-level fields](#top-level-fields)
- [Workflow blocks: `create` / `restore` / `remove`](#workflow-blocks-create--restore--remove)
- [Variants](#variants)
- [`services_mismatch`](#services_mismatch)
- [`local_yml.preserve_keys`](#local_ymlpreserve_keys)
- [`pack`](#pack)
- [`unpack`](#unpack)
- [Template namespace: `${snapshot.*}`](#template-namespace-snapshot)
- [Manifest contents](#manifest-contents)
- [Filesystem layout](#filesystem-layout)
- [Lifecycle and safety semantics](#lifecycle-and-safety-semantics)
- [Lock interaction](#lock-interaction)
- [Exit codes](#exit-codes)
- [Validate domain](#validate-domain)
- [Related commands](#related-commands)

## Purpose

A snapshot captures a known-good state of mutable project data — typically databases, search indices, service-branch metadata, and the devbox files that pin the developer's local configuration — into a self-contained directory. Restore is a soft operation: the `restore` workflow runs and the devbox files are swapped back into place. It does **not** invoke `reset`, recreate containers, or reapply deploy steps.

Designed around the workflow: *"I'm on a feature, a hotfix comes in — save, switch to clean DB, fix, return to feature."*

Core knows nothing about specific data stores. The user defines `create` / `restore` workflows that call existing user commands (`db.dump`, `opensearch.snapshot`, etc).

## Worked example: switch between tasks (UC-3)

```yaml
# devbox/snapshot.yml
rollback_target: baseline

create:
  description: Capture current DB and search index
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.snapshot
      with: { out: ${snapshot.path}/search/index.tar }

restore:
  description: Restore DB and search index from snapshot
  steps:
    - command: db.restore
      when: file-exists ${snapshot.path}/db/main.sql.gz
      with: { in: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.restore
      when: file-exists ${snapshot.path}/search/index.tar
      with: { in: ${snapshot.path}/search/index.tar }
```

Day in the life:

```sh
devbox snapshot create feature-x-wip -d "WIP on feature X"
# hotfix interrupts; restore a clean baseline
devbox snapshot restore baseline
# ... do the hotfix work, push, merge ...
devbox snapshot restore feature-x-wip          # back to WIP
devbox snapshot rollback                       # quick: restore the rollback_target
```

## File location

`devbox/snapshot.yml` at the project root. The file is optional — read-only subcommands (`list`, `current`, `inspect`, `unpack`) work without it. Mutating subcommands (`create`, `restore`, `rollback`, `remove`, `pack`) error if it is missing or the relevant workflow block is absent.

## Top-level fields

| Field | Type | Default | Purpose |
|---|---|---|---|
| `dir` | string | `./snapshots` | Where snapshot directories and tarballs live. Resolved relative to the project root. |
| `rollback_target` | string | — | Name of the snapshot used by `devbox snapshot rollback`. Must point at an existing snapshot. |
| `require_matching_config` | bool | `false` | When true, `restore` aborts (exit 1) if the snapshot's `project.config_hash` differs from the current deploy state. When the snapshot's `config_hash` is empty (no deploy has run yet), it is treated as matching — never blocked. |
| `services_mismatch` | block | — | Policy for service-set divergence between manifest and current config (see [`services_mismatch`](#services_mismatch)). |
| `local_yml` | block | — | Override policy for `devbox/local.yml` keys (see [`local_yml.preserve_keys`](#local_ymlpreserve_keys)). |
| `pack` | block | — | Pack policy (see [`pack`](#pack)). |
| `create` | workflow | — | Capture workflow (see [Workflow blocks](#workflow-blocks-create--restore--remove)). |
| `restore` | workflow | — | Restore workflow. |
| `remove` | workflow | — | Cleanup workflow run by `devbox snapshot remove` before the directory is deleted. |

The loader uses strict decoding (`KnownFields(true)`): unknown top-level keys are hard errors.

## Workflow blocks: `create` / `restore` / `remove`

Each block has:

| Field | Type | Purpose |
|---|---|---|
| `description` | string | Free-form description displayed by `inspect` and `list`. |
| `steps` | `[]WorkflowStep` | Step list — same shape as the `workflow:` block in a declarative command. See [commands/types.md](commands/types.md#type-workflow) for step syntax. |
| `variants` | `map[string]Workflow` | Named alternative step lists (see [Variants](#variants)). |

The `steps:` shape is the existing `model.WorkflowStep` type. Snapshot workflows are user-command workflows executed at runtime from a different source file — every step shape and feature (`command:`, `with:`, `when:`, `confirm:`, `parallel:`, `continue_on_error:`) is supported.

Restore is **drop + restore** — no DB prefixing, no name substitution. Your `db.restore` user command typically drops the target DB and reloads from `${snapshot.path}/db/main.sql.gz`.

`baseline` is just an ordinary snapshot name. There are no reserved semantics.

## Variants

A variant is a named alternative step list within a workflow block. Useful when "capture everything" and "capture DB only" must coexist.

```yaml
create:
  description: Capture full env
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.snapshot
      with: { out: ${snapshot.path}/search/index.tar }
  variants:
    db-only:
      description: Capture DB only
      steps:
        - command: db.dump
          with: { out: ${snapshot.path}/db/main.sql.gz }
```

Selection:

- `devbox snapshot create x` → default block.
- `devbox snapshot create x --using=db-only` → `create.variants.db-only`.
- `devbox snapshot restore x` → uses `restore.variants[<manifest.variant>]` if set; falls back to the default `restore` block when the variant is missing on the restore side.
- Missing variant on **create** errors before any filesystem mutation.

Variant names must match `[a-z0-9][a-z0-9._-]{0,30}`. The variant chosen on create is recorded in the manifest so restore picks the matching block automatically.

## `services_mismatch`

Controls what `restore` does when the snapshot's recorded service set diverges from the current project's effective service set. The snapshot manifest records every effective service (name + enabled flag, sorted by name) at create time; restore compares that against `cfg.Services` and applies the configured policy.

```yaml
services_mismatch:
  policy: warn          # warn (default) | block | ignore
```

| Policy | Behavior |
|---|---|
| `warn` (default, also when the block is omitted) | Restore continues. Any non-empty diff is rendered in the confirmation prompt; with `-y` the warning is written to stderr and restore proceeds. |
| `block` | Any non-empty diff aborts before any side effect on `devbox/local.yml` (exit 1, typed `ServicesMismatchError`). |
| `ignore` | Diff is suppressed entirely; restore proceeds silently. |

The diff is grouped into three buckets, each rendered with the same wording across the restore prompt, `snapshot inspect`, and the `snapshot.<name>.services_diff` validator:

| Group | Meaning |
|---|---|
| `only in snapshot` | Service named in the manifest but absent from the current project (likely runtime failures during restore workflow steps that target it). |
| `only local` | Service in the current project but absent from the manifest (deploy-state entries for it remain on disk after restore — harmless but easy to miss). |
| `enabled differs` | Same name on both sides but the `enabled` flag flipped. |

Unknown `policy` values are rejected at load time with a clear "unknown policy" error listing the allowed set.

## `local_yml.preserve_keys`

`devbox/local.yml` typically contains machine-specific overrides (ports, hostnames, paths) that should not travel with a snapshot. `preserve_keys` lists dot-paths whose **current** values survive restore even when the snapshot ships a `local.yml`.

```yaml
local_yml:
  preserve_keys:
    - services.main.ports
    - services.db.ports
    - host.shell
```

- Dot-paths address nested mapping keys; array-index segments (`services[0].ports`) are not supported — `local.yml` is maps-of-maps.
- Paths that do not exist in either side are silent no-ops; structural conflicts (e.g. an intermediate segment that is not a mapping, or incompatible kinds at the path between snapshot and current) surface as clear errors.
- The helpers operate on `*yaml.Node` to preserve key order and node-attached comments where `yaml.v3` retains them. `yaml.v3` normalises indentation and flow/block style on marshal, so byte-exact formatting is not preserved — only semantic content + key order + comments on untouched nodes.
- A 1 MiB cap applies to the `local.yml` payload on both create and restore to defuse YAML alias-explosion in untrusted archive content.

**Create**: `captureDevboxFiles` reads `devbox/local.yml`, calls `stripPreservedKeys` to remove the listed dot-paths, and writes the result into `<snap>/devbox/local.yml`. If every top-level key was preserved (resulting in an empty mapping), the file is still written so restore semantics are unambiguous.

**Restore** edge cases:

| Snapshot has `local.yml` | Current has `local.yml` | Behavior |
|---|---|---|
| yes | yes | merge: snapshot overlay + preserved keys spliced from current |
| yes | no | write snapshot's `local.yml` as-is (preserve_keys no-op) |
| no | yes (with preserved values) | write a minimal `local.yml` containing only the preserved keys extracted from current |
| no | no | no-op |

`deploy-state.yml` is always overwritten on restore — no merge is performed. Orphan entries for services that no longer exist locally are safe (deploy ignores them on the next run).

## `pack`

```yaml
pack:
  exclude:
    - "**/*.tmp"
    - ".cache/**"
```

`pack.exclude` is a list of doublestar globs evaluated relative to the snapshot directory. The CLI `--exclude` flags **append** to this list (they do not replace it).

`devbox snapshot pack <name>` produces a single `./snapshots/<name>.tar.gz`. No `.sha256` sidecar is written — one file per snapshot. The in-memory sha256 of the archive is shown in the success message for ad-hoc reference but is not required by `unpack`. Transport bitflips are caught by gzip CRC32 and tar structural validity; in-archive tampering of individual artifacts is caught by `unpack`'s manifest-driven verification (see below).

## `unpack`

`devbox snapshot unpack <tar-path> [--as=<name>] [--no-verify] [-y]` extracts an archive into `./snapshots/<final-name>/` using the existing distrust-safe extract contract (`filepath.IsLocal`, no symlinks, 50 GiB size cap, 100 000 entry cap) into a sibling `./snapshots/.unpack-<random>/` staging dir, then atomic-renames into place.

After extraction (and before the rename into final position) the staging tree is verified against `manifest.yml`:

| Group | Stderr line | Triggers confirm? |
|---|---|---|
| `Missing` (in manifest, not on disk) | `warning: artifact %q listed in manifest is missing from archive` | yes (grouped) |
| `HashMismatch` (both present, sha256 differs) | `warning: artifact %q sha256 mismatch (manifest=%s, actual=%s)` | yes (grouped) |
| `Extra` (on disk, not in manifest) | `info: archive contains %q not listed in manifest` | no |

`Missing` and `HashMismatch` share a single grouped `continue? [y/N]` prompt at the end (default `no`). Declining triggers staging cleanup and returns `UnpackVerifyDeclinedError`; the final directory is untouched because verification fires before the rename-old-aside step. `Extra` is info-only.

Flags:

- `--no-verify` — skip artifact verification entirely. The bypass is announced on stderr (`warning: skipping artifact verification at user request (--no-verify)`) so it is visible in CI logs and post-mortems.
- `-y` — auto-accept both the overwrite prompt (when the final directory already exists) and the verify prompt; warnings still print to stderr.

The success summary line reads `(verified)`, `(verified with N warnings)`, or `(verification skipped)` driven by `UnpackResult.Verification`. The verifier validates each `manifest.yml` artifact path with `filepath.IsLocal` + `pathsafe.ContainedRel` against the staging root **before** opening any file, so a maliciously crafted manifest cannot make the verifier read paths outside the staging tree.

> **Trust boundary**: manifest verification is integrity-of-record, not authenticity. It catches accidental mutation, partial truncation, and casual in-archive tampering. It does **not** catch an attacker who re-packs the archive with a self-consistent manifest (they can simply rewrite `manifest.yml` to match the new artifacts). This is an acceptable trade-off for a dev tool — no GPG, no signatures, no cryptographic provenance.

## Template namespace: `${snapshot.*}`

`${snapshot.*}` is available only inside snapshot workflow blocks (and `with:` arguments forwarded to user commands invoked from those blocks). It is a compile-time error elsewhere.

| Variable | Outside snapshot | `create` scope | `restore` / `remove` scope |
|---|---|---|---|
| `${snapshot.name}` | error | ✓ | ✓ |
| `${snapshot.path}` | error | ✓ | ✓ |
| `${snapshot.description}` | error | ✓ | ✓ |
| `${snapshot.variant}` | error | ✓ | ✓ |
| `${snapshot.created_at}` | error | **error** (does not exist yet) | ✓ |

`${snapshot.path}` is the absolute path to `./snapshots/<name>/`. Workflows are expected to write artifacts under it. Symlinks created inside the snapshot directory are rejected at scan time — workflows must produce regular files.

Missing keys within an active scope render as empty strings, consistent with `${param.*}`.

## Manifest contents

Every snapshot directory carries `manifest.yml`:

```yaml
name: feature-x-wip
created_at: 2026-05-24T11:02:00Z
description: WIP feature X
project:
  name: tbm-next
  config_hash: def67890          # empty if no deploy has run yet
  services:                      # effective service set at create time, sorted by name
    - { name: cdn,  enabled: false }
    - { name: db,   enabled: true }
    - { name: main, enabled: true }
devbox_version: 0.42.0
variant: ""
artifacts:
  - path: db/main.sql.gz
    size: 1287654321             # int64
    sha256: abc...
devbox_files:
  local_yml: devbox/local.yml
  deploy_state: devbox/deploy-state.yml
last_create:
  at: 2026-05-24T11:02:00Z
  status: ok                     # ok | failed | interrupted
  failed_step: ""
last_restore:
  at: 2026-05-24T15:42:00Z
  status: ok
  duration_ms: 12340
  failed_step: ""
```

The manifest carries no `schema_version` — devbox is in active pre-release development and there is nothing to migrate from.

## Filesystem layout

```
<project>/
  devbox/snapshot.yml
  snapshots/
    <name>/
      manifest.yml
      devbox/{local.yml, deploy-state.yml}
      <user artifacts>
    <name>.tar.gz
  .devbox/snapshots/
    current
    snapshot.lock
    .pre-restore-backup/{local.yml, deploy-state.yml}
    .unpack-<random>/             # transient unpack staging
```

- `./snapshots/` is normally **not gitignored** so dev fixtures can ship through git when small; large artifacts should be added to `.gitignore` per project.
- `.devbox/snapshots/` is gitignored.
- `current` is a small text file naming the most recently created or restored snapshot. Cleared when the active snapshot is removed.

## Lifecycle and safety semantics

**Create**

- Acquires the project locks (see [Lock interaction](#lock-interaction)).
- Refuses to overwrite an existing snapshot directory without `-y` in non-TTY contexts (interactive confirmation otherwise).
- Copies `devbox/local.yml` and `.devbox/deploy/state.yml` into `<snap>/devbox/` before running the workflow.
- Runs the selected create workflow with `${snapshot.*}` available in `create` scope.
- Scans the resulting directory (excluding `manifest.yml` and `devbox/`), streaming sha256 per file. Symlinks inside the snapshot directory are rejected.
- Writes `manifest.yml` atomically (temp file in the same directory, `rename`).
- Updates the current pointer atomically.
- On workflow failure: keeps the directory, writes `last_create.status = "failed"` with `failed_step`, leaves the current pointer untouched, exits 1.
- On SIGINT: `last_create.status = "interrupted"`, exit 130.

**Restore**

- Acquires the project locks.
- Loads and verifies the manifest. Warns when `project.config_hash` differs from the current deploy state; blocks (exit 1) when `require_matching_config: true`. Empty manifest `config_hash` is treated as matching.
- Backs up the current `devbox/local.yml` and `.devbox/deploy/state.yml` into `.devbox/snapshots/.pre-restore-backup/` atomically. The previous backup is overwritten.
- Restores devbox files from `<snap>/devbox/` over the working copies.
- Runs the selected restore workflow with `${snapshot.*}` available in `restore` scope (all keys including `created_at`).
- On success: updates the current pointer atomically and writes `last_restore.status = "ok"` to the manifest.
- On failure or SIGINT: leaves the current pointer untouched; writes `last_restore.status ∈ {failed, interrupted}` with `failed_step`; emits a hint about `.pre-restore-backup/` for manual recovery; exits 1 (or 130 for SIGINT).

**Rollback** dispatches the restore code path against `rollback_target`. Fails clearly if the target snapshot does not exist.

**Remove**

- Acquires the project locks.
- Runs the `remove:` workflow (when defined) in restore-scope template visibility.
- `os.RemoveAll(snapshotDir)`.
- Clears the current pointer atomically when it pointed at this snapshot.

**Live view**

`snapshot create`, `restore`, and `remove` render per-step live status (spinner, ✓ / ✗ / skip icon, elapsed time) for the top-level sequential steps of the selected workflow, matching the `deploy run` styling. Parallel groups inside a workflow keep their existing per-row block rendering nested under the group's step row. The live view is enabled automatically when stdout is a TTY; it is disabled in non-TTY contexts and when `--no-live` is passed, in which case the workflow falls back to plain stdout output. Mid-workflow `confirm:` steps and command-level `confirmation:` prompts pause the live footer for the duration of the prompt and repaint afterwards.

**Pack / unpack** share the same lock contract as the mutating snapshot commands — pack reads the snapshot directory under the lock so a concurrent `remove` / `create` cannot truncate the archive; unpack writes under the lock so concurrent ops cannot race the staging-to-final rename. Unpack enforces a strict archive-safety contract: paths must satisfy `filepath.IsLocal` and `ContainedRel` against the staging root, only regular files and directories are accepted (no symlinks, hardlinks, devices, fifos, global headers), and the extractor caps total bytes (50 GiB) and entry count (100 000) to defuse zip bombs. Extraction stages into a sibling temp dir under `./snapshots/`, then atomic-renames to the final name on success; any failure removes the staging dir without touching the target. If the final directory already exists, the existing tree is renamed aside as a backup; the staging tree is renamed into place; on success the backup is removed, and on second-rename failure the backup is restored. After extraction (and before the rename-into-place step) the staging tree is verified against `manifest.yml` — see [`unpack`](#unpack) for the warn + confirm behavior.

## Lock interaction

All project-mutating commands acquire two locks in a fixed order:

1. `<baseDir>/.devbox/deploy/deploy.lock`
2. `<baseDir>/.devbox/snapshots/snapshot.lock`

Release is reverse order. The shared helper `lock.AcquireProjectLocks(baseDir)` enforces this for both snapshot mutating commands (`create`, `restore`, `rollback`, `remove`, `pack`, `unpack`) and deploy lifecycle commands (`deploy`, `run`, `stop`, `restart`, `reset`).

Lifecycle commands acquire the project locks **after** their preflight pass succeeds — preflight may invoke user `type: command` checks and must not run under operation locks. Snapshot mutating commands do not run preflight and acquire locks at the top of their `RunE`.

When either lock is already held by another live process, the operation exits 75 (`EX_TEMPFAIL`) with a clear `"<operation> in progress: pid N"` message.

## Exit codes

| Code | When |
|---|---|
| 0 | Success |
| 1 | Workflow failure, manifest corruption, archive rejection, missing required config block, `require_matching_config` block |
| 64 | Usage error (bad name, missing argument, malformed YAML at the CLI surface) |
| 75 | Lock held by another live process |
| 130 | SIGINT during a long-running workflow |

## Validate domain

`devbox validate snapshot [<name>] [--verify]` exposes static checks:

| Validator | Severity | Trigger |
|---|---|---|
| `snapshot.config_loadable` | error | `devbox/snapshot.yml` exists but does not parse. Absent file is silent. |
| `snapshot.create_defined` | info | `create:` block missing — `devbox snapshot create` will refuse to run. |
| `snapshot.restore_defined` | info | `restore:` block missing — `restore` / `rollback` will refuse. |
| `snapshot.variant_pairing` | warn | `create.variants[X]` exists but `restore.variants[X]` is missing and no default `restore` block falls back. |
| `snapshot.rollback_target_exists` | warn | `rollback_target` set but no snapshot of that name exists on disk. |
| `snapshot.<name>.manifest_valid` | error | `manifest.yml` missing or unparseable. |
| `snapshot.<name>.artifacts_exist` | error | Any manifest-listed artifact missing on disk. |
| `snapshot.<name>.checksums` | warn | With `--verify`: any artifact's recomputed sha256 differs from the manifest. |
| `snapshot.<name>.services_diff` | info | Manifest's recorded service set differs from the current project's effective service set. Hint quotes the formatted diff. |
| `snapshot.<name>.last_create_failed` | info | `last_create.status ∈ {failed, interrupted}`. |
| `snapshot.template_scope` | error | `${snapshot.created_at}` used in a `create:` block (it does not exist yet at create time). |

## Related commands

- `devbox snapshot create <name> [-d <desc>] [--using=<variant>] [-y] [--no-live]`
- `devbox snapshot list [--output json] [--pretty]`
- `devbox snapshot current [--output json] [--pretty]`
- `devbox snapshot inspect <name|tar-path> [--output json] [--pretty]`
- `devbox snapshot restore <name> [-y] [--no-live]`
- `devbox snapshot rollback [-y] [--no-live]`
- `devbox snapshot remove <name> [-y] [--no-live]`
- `devbox snapshot pack <name> [--out=<path>] [--exclude=<glob>...]`
- `devbox snapshot unpack <tar-path> [--as=<name>] [--no-verify] [-y]`
- `devbox validate snapshot [<name>] [--verify]`

See [commands/types.md](commands/types.md#type-workflow) for the `WorkflowStep` shape reused by snapshot workflows, and [state.md](state.md) for the deploy state journal that snapshots back up alongside `devbox/local.yml`.

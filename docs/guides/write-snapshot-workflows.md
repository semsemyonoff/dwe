# Write snapshot workflows

You have used `dwe snapshot create` and `restore` enough to know the consumer side (see [switching-tasks-with-snapshots.md](switching-tasks-with-snapshots.md)). Now you want to author the `workspace/snapshot.yml` that drives them — decide what gets captured, what gets restored, what survives a restore, and what gets cleaned up when a snapshot is removed.

This guide walks through the file from the smallest useful shape to the production knobs you reach for when teammates start sharing snapshots.

## Sections

- [The minimal workflow](#the-minimal-workflow)
- [`${snapshot.*}` template namespace](#snapshot--template-namespace)
- [Workflows reuse your user commands](#workflows-reuse-your-user-commands)
- [Variants — alternative step lists](#variants--alternative-step-lists)
- [`require_matching_config` and `config_hash`](#require_matching_config-and-config_hash)
- [`services_mismatch` policy](#services_mismatch-policy)
- [`local_yml.preserve_keys` — keep machine-specific overrides](#local_ymlpreserve_keys--keep-machine-specific-overrides)
- [`pack.exclude` — keep ephemeral files out of tarballs](#packexclude--keep-ephemeral-files-out-of-tarballs)
- [`rollback_target` — one-key returns](#rollback_target--one-key-returns)
- [`remove:` — clean up external resources](#remove--clean-up-external-resources)

## The minimal workflow

A single-step `snapshot.yml` is enough to capture and restore a database:

```yaml
# workspace/snapshot.yml
create:
  description: Capture the main DB
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }

restore:
  description: Restore the main DB
  steps:
    - command: db.restore
      with: { in: ${snapshot.path}/db/main.sql.gz }
```

`db.dump` and `db.restore` are *your own* user commands — DWE does not ship a database backend (see [author-project-commands.md](author-project-commands.md) if you have not written them yet). The snapshot subsystem just orchestrates: it acquires the project locks, creates `./snapshots/<name>/`, runs your `create` steps with `${snapshot.path}` resolved, then writes a manifest.

Reference: [`../reference/config/snapshot.md`](../reference/config/snapshot.md).

## `${snapshot.*}` template namespace

`${snapshot.path}` is the absolute path to `./snapshots/<name>/` — that is the directory your `create` workflow writes artifacts into and your `restore` workflow reads them out of. Workflows are expected to write under that path; symlinks placed inside the snapshot directory are rejected at the post-create scan.

The full namespace:

| Variable | `create` | `restore` / `remove` |
|---|---|---|
| `${snapshot.name}` | ✓ | ✓ |
| `${snapshot.path}` | ✓ | ✓ |
| `${snapshot.description}` | ✓ | ✓ |
| `${snapshot.variant}` | ✓ | ✓ |
| `${snapshot.created_at}` | error (does not exist yet) | ✓ |

Outside snapshot workflow blocks `${snapshot.*}` is a compile-time error — the scope gate is enforced before the template is rendered. You cannot accidentally read `${snapshot.path}` from a regular `db.dump` user command invoked outside a snapshot context; the same `db.dump` command picks up `${snapshot.path}` only when called *through* a snapshot workflow's `with:` block.

## Workflows reuse your user commands

Snapshot workflow steps are the same `WorkflowStep` shape used by `type: workflow` user commands — `command:`, `with:`, `when:`, `confirm:`, `parallel:`, `continue_on_error:`. There is no special syntax for snapshots and no separate execution model: every feature you have built into your user commands (params, validation, notifications) carries through.

That means the pattern for a multi-store snapshot is just *more steps calling more commands*:

```yaml
create:
  description: Capture full env
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.snapshot
      with: { out: ${snapshot.path}/search/index.tar }
    - command: redis.dump
      with: { out: ${snapshot.path}/redis/dump.rdb }
```

`restore:` follows the same shape, calling the matching `*.restore` user commands. Conditional restore — "only if the file is there" — is a `when:` predicate on the step:

```yaml
restore:
  steps:
    - command: opensearch.restore
      when: file-exists ${snapshot.path}/search/index.tar
      with: { in: ${snapshot.path}/search/index.tar }
```

The full workflow step reference lives at [`../reference/config/commands/types.md`](../reference/config/commands/types.md#type-workflow).

## Variants — alternative step lists

A workflow block can declare named alternative step lists under `variants:`. Use them when "capture everything" and "capture DB only" must coexist without copy-pasting the parent workflow:

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
    with-search:
      description: Capture DB + search
      steps:
        - command: db.dump
          with: { out: ${snapshot.path}/db/main.sql.gz }
        - command: opensearch.snapshot
          with: { out: ${snapshot.path}/search/index.tar }
```

Pick a variant on create:

```shell
dwe snapshot create wip-x                # default block
dwe snapshot create wip-x --using=db-only
```

The chosen variant is recorded in the snapshot manifest, so `dwe snapshot restore wip-x` automatically picks `restore.variants.db-only` if it exists. If the matching restore variant is missing, restore falls back to the default `restore:` block — a useful default when the asymmetric case is "capture less, restore the same way".

Variant names must match `[a-z0-9][a-z0-9._-]{0,30}`. Asking for a missing variant on `create` errors before any filesystem mutation, so you cannot create a half-broken snapshot.

## `require_matching_config` and `config_hash`

Every snapshot records the project's `config_hash` at create time (a digest over the resolved deploy config). On restore, DWE compares that to the current deploy state's hash. By default a mismatch is a soft warning printed to stderr.

Flip the strict bit when restoring against an unmatched config would be actively unsafe — for example, when your `db.restore` workflow assumes a particular schema version:

```yaml
require_matching_config: true
```

With strict matching, restore aborts (exit 1) when the hashes differ. Empty manifest `config_hash` — a snapshot created before any deploy ran — is treated as matching and never blocks.

A `config_hash` mismatch in practice means one of:

- the snapshot was taken on a different branch with different `workspace/deploy.yml` or `service.yml` content,
- a teammate's snapshot was taken against a different version of the project config,
- the project was rebased forward / backward across a deploy-config-changing commit.

Pair `require_matching_config: true` with `dwe snapshot inspect <name>` (which shows the recorded hash) to debug the divergence.

## `services_mismatch` policy

Snapshots record the effective service set (name + `enabled` flag, sorted by name) at create time. On restore, that set is diffed against the current project's effective services. The policy block decides what happens when the diff is non-empty:

```yaml
services_mismatch:
  policy: warn          # warn (default) | block | ignore
```

| Policy | Behavior |
|---|---|
| `warn` (default) | Restore continues. Diff is rendered in the confirm prompt; with `-y` it goes to stderr and restore proceeds. |
| `block` | Any non-empty diff aborts before touching `workspace/local.yml` (exit 1). |
| `ignore` | Diff is suppressed; restore proceeds silently. |

The diff is grouped into three buckets — `only in snapshot`, `only local`, `enabled differs` — and the same grouping appears in `dwe snapshot inspect` and the `snapshot.<name>.services_diff` validator. Reach for `block` when the snapshot consumer is non-interactive (CI / scripted) and silently proceeding past a mismatch would cause a downstream failure that is hard to diagnose.

## `local_yml.preserve_keys` — keep machine-specific overrides

`workspace/local.yml` is part of the snapshot. That is mostly what you want — restoring a snapshot should restore the local toggles a teammate had when they created it. But machine-specific values (ports remapped because `5432` was taken locally, hostnames pointing at a private DNS) should not travel.

`local_yml.preserve_keys` is a list of dot-paths whose **current local values** survive restore:

```yaml
local_yml:
  preserve_keys:
    - services.main.ports
    - services.db.ports
    - host.shell
```

- Dot-paths address nested mapping keys. Array-index segments (`services[0].ports`) are not supported.
- Paths that exist on neither side are silent no-ops.
- Order and YAML comments on untouched nodes are preserved where `yaml.v3` retains them; flow/block style and indentation may normalize on marshal.

On create, the listed paths are stripped from the captured `local.yml`. On restore, the captured `local.yml` is merged onto the current copy with the preserved keys spliced back from your live file. When the snapshot ships no `local.yml` at all but you have preserved values locally, a minimal `local.yml` containing only the preserved keys is written.

Common picks: anything under `services.<name>.ports`, anything under `services.<name>.hosts`, paths to local development tools that vary by OS.

## `pack.exclude` — keep ephemeral files out of tarballs

`dwe snapshot pack <name>` produces a single `./snapshots/<name>.tar.gz`. If your `create` workflow drops temp files, scratch dumps, or anything you do not want shipped to a teammate, exclude them with doublestar globs:

```yaml
pack:
  exclude:
    - "**/*.tmp"
    - ".cache/**"
    - "**/*.log"
```

Patterns are evaluated relative to the snapshot directory. The `dwe snapshot pack --exclude=<glob>` CLI flag **appends** to this list, it does not replace it — so the snapshot.yml exclude is a baseline you can extend per invocation, not a default you can override.

The archive is content-addressed at unpack time against `manifest.yml`. Excluded files do not appear in the manifest and are not part of the integrity check, so excluding ephemeral output is safe.

## `rollback_target` — one-key returns

If one snapshot is the canonical "safe" state — usually a `baseline` taken right after a clean deploy — declare it as the rollback target:

```yaml
rollback_target: baseline
```

Then `dwe snapshot rollback` is shorthand for `dwe snapshot restore baseline`. It fails clearly if the target snapshot does not exist, and the `snapshot.rollback_target_exists` validator warns at `dwe validate snapshot` time when `rollback_target` is set but the named snapshot is missing on disk.

`baseline` is just a convention — any existing snapshot name works. The convention pays off because every guide, runbook, and habit can refer to "rollback" without ambiguity. See [switching-tasks-with-snapshots.md](switching-tasks-with-snapshots.md#one-key-rollback-with-rollback_target) for the consumer-side framing.

## `remove:` — clean up external resources

`dwe snapshot remove <name>` deletes `./snapshots/<name>/` from disk. If the snapshot also corresponds to external state — objects in an S3 bucket, rows in a metadata table, a tag on a registry — declare a `remove:` workflow that runs before the directory is deleted:

```yaml
remove:
  description: Drop external artifacts for this snapshot
  steps:
    - command: s3.remove
      with: { prefix: snapshots/${snapshot.name}/ }
    - command: registry.untag
      when: file-exists ${snapshot.path}/registry-tag
      with: { tag: "${snapshot.name}" }
```

`remove:` is optional. Without it, `dwe snapshot remove` simply does `os.RemoveAll(snapshotDir)` and clears the current pointer if it referenced this snapshot. With it, the workflow runs first with `${snapshot.*}` in `restore`-scope visibility (so `${snapshot.created_at}` is available), then the directory is removed. Workflow failure aborts the remove — the directory stays put so you can investigate and re-run.

Pair `remove:` with `pack.exclude` if you ship artifacts that are *referenced* by external systems: the local file is a marker the workflow consumes to decide what to clean up remotely.

## See also

- [switching-tasks-with-snapshots.md](switching-tasks-with-snapshots.md) — consumer-side workflow: when to create, restore, rollback, pack
- [`../reference/config/snapshot.md`](../reference/config/snapshot.md) — full `snapshot.yml` schema reference
- [`../reference/config/commands/types.md`](../reference/config/commands/types.md#type-workflow) — workflow step shape reused by snapshot blocks
- [author-project-commands.md](author-project-commands.md) — write the `db.dump` / `db.restore` user commands your snapshot workflows call

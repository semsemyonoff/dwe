# Switching tasks with snapshots

You are mid-feature with a half-migrated database and seeded test data when a hotfix lands on `main`. You need a clean stack to reproduce the bug, fix it, and then return to exactly where you were — same DB rows, same search index, same `local.yml`. This guide is the cookbook for that flow.

Snapshots are DWE's checkpoint mechanism. They capture mutable project data (databases, search indices, the deploy state journal, `workspace/local.yml`) into a named directory under `./snapshots/<name>/`, and restore it later as a soft operation — no `reset`, no container recreation, no deploy replay.

## The `baseline` pattern

Pick one snapshot per stable starting point and keep it around. A common shape:

- `baseline` — a known-good empty-ish state you can always fall back to (clean seed data, no in-flight migrations).
- `wip-<branch>` — a per-task checkpoint you create when you have to switch away from work in progress.

`baseline` is just an ordinary snapshot name. There is nothing reserved about it — the convention pays off because every other guide and habit can refer to "restore baseline" without ambiguity.

Create it once, right after a clean deploy:

```shell
dwe snapshot create baseline -d "clean post-deploy state"
```

## The full feature-paused-by-hotfix cycle

```shell
# 1. checkpoint the in-flight feature work
dwe snapshot create wip-feature-x -d "WIP: feature X, migrations applied"

# 2. clean slate for the hotfix
dwe snapshot restore baseline

# 3. ... reproduce, fix, push, merge the hotfix ...

# 4. return to where you were
dwe snapshot restore wip-feature-x
```

Restore is a **drop + restore** operation by convention: the project's `db.restore` user command typically drops the target database and reloads it from `${snapshot.path}/db/main.sql.gz`. Containers do not get recreated, deploy steps do not re-run — only the data and the workspace files swap.

`workspace/local.yml` is part of the snapshot but anything declared under `local_yml.preserve_keys` in `workspace/snapshot.yml` (ports, hostnames, paths — see [`write-snapshot-workflows.md`](write-snapshot-workflows.md)) is spliced back from your current copy so machine-specific overrides survive the swap.

## One-key rollback with `rollback_target`

If you switch to `baseline` (or any other "safe" snapshot) often, declare it as the rollback target in `workspace/snapshot.yml`:

```yaml
# workspace/snapshot.yml
rollback_target: baseline
```

Then `dwe snapshot rollback` is shorthand for `dwe snapshot restore baseline` — useful in muscle memory and in scripts. It fails clearly if the target snapshot does not exist. Reference: [`../reference/config/snapshot.md`](../reference/config/snapshot.md).

## Inspecting what you have

```shell
dwe snapshot list                  # everything under ./snapshots/
dwe snapshot current               # what was last created or restored
dwe snapshot inspect wip-feature-x # manifest contents: description, artifacts, service set, config hash
```

`inspect` is the one to reach for when you are not sure whether a snapshot is still relevant — it shows the recorded service set, the `config_hash` at create time, and the per-artifact sizes and sha256s.

## Handing a snapshot to a teammate

Snapshots are local files, but they pack and unpack cleanly:

```shell
# producer
dwe snapshot pack wip-feature-x
# → ./snapshots/wip-feature-x.tar.gz

# consumer
dwe snapshot unpack ./snapshots/wip-feature-x.tar.gz
dwe snapshot restore wip-feature-x
```

`pack` writes a single `.tar.gz` (no sidecar checksum file). `unpack` verifies each archived artifact against the manifest's recorded sha256 before moving the tree into `./snapshots/`. If anything fails verification you get a prompt before the snapshot lands; declining leaves the working tree untouched.

When the receiver's effective service set differs from the snapshot's recorded set, restore surfaces the diff under the `services_mismatch` policy (`warn` by default, can be raised to `block` in `workspace/snapshot.yml`). Reference: [`../reference/config/snapshot.md`](../reference/config/snapshot.md).

## What snapshot does NOT do

Worth being explicit because each one bites someone at least once:

- **No container recreation.** Restore swaps data files and workspace config; running containers keep running with whatever image and configuration they had. If the snapshot was created against a different image tag, the running container is now serving stale code against new data.
- **No deploy replay.** `.dwe/deploy/state.yml` is overwritten by the snapshot's recorded journal, but the steps it describes are not re-executed. If the snapshot was taken before a new deploy step landed in `workspace/deploy.yml`, that step does not run on restore — `dwe deploy` will run it the next time you invoke it.
- **No reset.** Volumes, untracked files in the project tree, and anything outside the artifacts your `create:` workflow explicitly captured are not touched. Restore is additive on top of whatever is currently on disk.
- **No magic at the data layer.** Snapshot core does not know what a database is. The `create:` and `restore:` workflows you write call your own `db.dump` / `db.restore` user commands; if those drop and reload, restore drops and reloads. If they append, restore appends.

When you need a true clean slate, that is what `dwe reset run` is for — see [`troubleshooting.md`](troubleshooting.md#nuclear-option). A common pattern is `snapshot create pre-reset` immediately before `reset run`, so you have a rollback if the reset is more aggressive than expected.

## Where to next

- [`write-snapshot-workflows.md`](write-snapshot-workflows.md) — author your own `snapshot.yml`: `create` / `restore` / `remove` workflows, variants, `${snapshot.path}` templating, `preserve_keys`, `services_mismatch` policy.
- [`../reference/config/snapshot.md`](../reference/config/snapshot.md) — full schema reference.

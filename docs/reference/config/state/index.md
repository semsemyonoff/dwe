# Deploy State (.devbox/deploy/state.yml)

Idempotent deploy state tracking and skip-decision table.

## Pages

- [Schema](schema.md) — top-level, project, service, phase, step, and pending field references
- [Hashing and skip decisions](hashing.md) — `action_hash`, `config_hash`, invalidation rules, skip decision table
- [Management](management.md) — command flags, `devbox deploy state show/clear/repair`, non-interactive defaults, examples

## Purpose

The deploy state file (`.devbox/deploy/state.yml`) turns the deploy pipeline from "fire-and-forget" into something **idempotent and observable**.

> **Note** — only deploy pipelines have a state file. [`type: daemon`](../commands/types.md#type-daemon) commands have **no on-disk registry**: `docker ps` (filtered on the standard `devbox.project` / `devbox.daemon.id` / `devbox.daemon.params` labels) is the single source of truth for which daemons are running. There is no journal to drift, lock, or invalidate; a `docker stop` issued outside devbox is reflected immediately on the next `devbox status daemons` read.

Every step executed during `devbox deploy run` is recorded: its status (ok, failed, partial, in_progress, skipped), the timestamp it finished, its `action_hash` (fingerprint of the step body), and how long it took to run.

On the next `devbox deploy run`, each step's `action_hash` is compared to the recorded hash. Steps that succeeded with matching hashes are **skipped** (unless they have a `check:` action, which always runs to re-validate idempotency). Steps whose hash changed, or that previously failed, are **re-run**.

This mechanism ensures that:
- Deploying an unchanged codebase is fast (unchanged steps skip)
- Editing a step body automatically re-triggers it
- Editing service config files (`devbox/services/<name>/service.yml`, `devbox/services/<name>/deploy.yml`) or the project deploy config (`devbox/deploy.yml`) invalidates the affected scope and re-runs the impacted steps
- The journal survives crashes mid-deploy, allowing `--resume` on the next run

## File location

`.devbox/deploy/state.yml` — automatically created in the project root's `.devbox/deploy/` directory. Not checked into version control (add `.devbox/` to `.gitignore`).

> **Interaction with `snapshot restore`** — `deploy-state.yml` is always **overwritten** from the snapshot on restore. No merge is performed (unlike `devbox/local.yml`, which honours [`local_yml.preserve_keys`](../snapshot.md#local_ymlpreserve_keys)). Orphan entries for services that no longer exist locally are safe — the deploy pipeline ignores them on the next run. If you need to keep machine-specific deploy state across snapshots, use `local_yml.preserve_keys` for the values that drive that state rather than trying to preserve the journal itself.

## When to use

You do not write this file manually. It is created and maintained by `devbox deploy run`. Inspect it with:

```bash
devbox deploy state show
```

Clear it (e.g., to force all steps to re-run) with:

```bash
devbox deploy state clear
```

Repair corrupted status aggregates (rare; stale locks or unexpected crashes) with:

```bash
devbox deploy state repair
```

See [Management](management.md) for the full command and flag reference.

## Lock file

While `devbox deploy` is running, a file-lock is held at `.devbox/deploy/deploy.lock`. This prevents concurrent deploys on the same project.

**Lock acquisition:**

- Uses `flock(LOCK_EX|LOCK_NB)` to acquire an exclusive, non-blocking lock
- If the lock is held by another process, reads the PID from the lockfile and calls `syscall.Kill(pid, 0)` to check if that process is still alive
- If the process is gone (`ESRCH`), the lock is treated as stale: the lockfile is truncated and the lock is acquired
- If the lock is held by a running process, returns an error with the holding PID

**Lock release:**

- Automatically released when `devbox deploy` exits
- On `Ctrl+C` or other signals, the lock is released, and the state file is left in a consistent state (last successfully completed step is recorded)

**Stale locks:**

- If a deploy is killed with `kill -9` or the process crashes, the lock file remains
- The next `devbox deploy run` detects the stale lock, removes it, and proceeds
- This allows recovery from unexpected terminations

## Related commands

- `devbox deploy plan` — preview the pipeline before deploying
- `devbox deploy run` — execute deploy (with state tracking)
- `devbox deploy state show` — inspect state file
- `devbox deploy state clear` — reset state
- `devbox deploy state repair` — rebuild status aggregates
- `devbox reset run` — reset and clear deploy state
- `devbox services enable/disable` — toggle services (writes `pending` when not using `--apply`)
- See also [deploy.yml](../deploy/index.md) — how to declare steps and phases
- See also [lifecycle.yml](../lifecycle.md) — how `devbox run` gates on required service deployment

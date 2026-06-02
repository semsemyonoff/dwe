# State Schema

Field reference for `.dwe/deploy/state.yml`.

## Top-level fields

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | string | Always `"1"`; reserved for future format changes |
| `project` | object | Project-level state (deployed_at, config_hash, status, etc.) |
| `services` | map | Per-service state, keyed by service folder name (`workspace/services/<name>/`) |
| `pending` | object | Pending operations that need to be applied; present only when `dwe services enable/disable` was run without `--apply`. Written atomically by the toggle command; cleared by `dwe restart`, `dwe deploy run`, or `dwe reset run` |

## Project-level fields

| Field | Type | Description |
|-------|------|-------------|
| `deployed_at` | ISO 8601 timestamp | When the project was last fully deployed |
| `config_hash` | sha256 hex | Fingerprint of tracked services + top-level deploy config + per-service deploy configs. Edits to enabled-but-untracked service variants (e.g., `main-debug`) do not change this hash. |
| `status` | enum | `deployed`, `partial`, `failed`, `not_deployed`, `in_progress` |
| `last_run` | object | Timing and outcome of the last deploy attempt (`status`, `started_at`, `finished_at`) |
| `phases` | map | Per-phase state for project-level (non-service) phases |

## Service fields

| Field | Type | Description |
|-------|------|-------------|
| `status` | enum | `deployed`, `partial`, `failed`, `not_deployed` (service never ran, or all steps skipped) |
| `deployed_at` | ISO 8601 timestamp | When this service was last fully deployed |
| `config_hash` | sha256 hex | Fingerprint of `workspace/services/<name>/service.yml` + `workspace/services/<name>/deploy.yml` |
| `last_run` | object | Timing and outcome of the last deploy attempt for this service |
| `phases` | map | Per-phase state for this service's phases |

## Phase fields

| Field | Type | Description |
|-------|------|-------------|
| `status` | enum | `ok`, `failed`, `skipped` |
| `steps` | map | Per-step state, keyed by step name |

## Step fields

| Field | Type | Description |
|-------|------|-------------|
| `status` | enum | `ok`, `failed`, `in_progress` |
| `finished_at` | ISO 8601 timestamp | When this step completed (absent if in_progress) |
| `action_hash` | sha256 hex | Fingerprint of the step's `type`, `cmd`, and `with:` parameters |
| `duration_ms` | integer | How long the step took to execute, in milliseconds |

## Pending state

When `dwe services enable` or `dwe services disable` is run without `--apply`, the toggle command writes the local.yml change immediately but defers the apply step. The `pending` field in the state file tracks what still needs to run.

Pending entries are recorded only once a deploy has been attempted at least once on this stack. Any prior attempt counts: the journal lists at least one service in a non-`not_deployed` status (`deployed` / `failed` / `in_progress` / `partial` / `skipped`), OR `project.last_run` is present, OR `project.status` is set to anything other than `not_deployed`. If the journal file is corrupt (load error), the toggle still attempts the pending write so the corruption surfaces — silent pending loss would be worse.

Before any deploy attempt, pending has no meaning — the next `dwe deploy` picks up the new `local.yml` fresh — so the toggle silently updates `local.yml`/`.env`, writes no journal entry, and prints a one-line `run dwe deploy` hint after the plan.

### pending field schema

| Field | Type | Description |
|-------|------|-------------|
| `operations` | list | Ordered list of pending operations |
| `config_hash` | sha256 hex | Config hash at toggle time; used to detect stale pending entries |
| `created_at` | ISO 8601 timestamp | When the pending entry was written |

### pending operation schema

| Field | Type | Description |
|-------|------|-------------|
| `kind` | string | `restart` (stack-wide) or `deploy` (per-service) |
| `services` | list | For `deploy` kind: the service names that need deploying. Empty for `restart`. |

### Pending state lifecycle

| Event | Effect on `pending` |
|-------|---------------------|
| `dwe services enable/disable` (without `--apply`), no deploy attempt on record | No-op on `pending`; `local.yml`/`.env` updated and a one-line hint suggests `dwe deploy` |
| `dwe services enable/disable` (without `--apply`), any deploy attempt on record (incl. failed/partial/project-only) | Writes `pending.operations`; adds/merges ops for restart or deploy contributors |
| `dwe services enable/disable --apply` success | Clears only the pending ops that this apply step performed (unrelated pending ops from other sessions survive) |
| `dwe run` success | Clears the `restart` op (the run itself satisfies it); deploy op survives |
| `dwe stop` success | Clears the `restart` op (the next run will pick up toggled state); deploy op survives |
| `dwe restart` success | Clears the `restart` op; deploy op (if any) survives |
| `dwe deploy run` (full project) success | Clears the `deploy` op; restart op (if any) survives |
| `dwe deploy run --service <name>` success | Removes `<name>` from the `deploy` op's service list; if empty, removes the op |
| `dwe reset run` (project-wide) success | Clears all pending (full journal wipe) |
| `dwe reset run --service <name>` success | Writes `{kind: deploy, services: [<name>]}` atomically alongside removing service deployed state |

### Banner

`dwe status` (and its subcommands `apps`, `tools`, `infra`, `deploy`) display a warning banner when `pending` is non-nil:

```
⚠ Pending: deploy required for: svc-a, svc-b
  Run: dwe deploy run
⚠ Pending: restart required
  Run: dwe restart
```

The banner is rendered from `pending.operations` — one line per op. When `pending` is empty, no banner is shown.

## See also

- [Hashing and skip decisions](hashing.md) — how `action_hash` and `config_hash` are computed and how they drive the skip decision table
- [Management](management.md) — inspect, clear, and repair commands
- [Overview](index.md) — purpose, file location, lock file

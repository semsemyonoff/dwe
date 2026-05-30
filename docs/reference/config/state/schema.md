# State Schema

Field reference for `.devbox/deploy/state.yml`.

## Top-level fields

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | string | Always `"1"`; reserved for future format changes |
| `project` | object | Project-level state (deployed_at, config_hash, status, etc.) |
| `services` | map | Per-service state, keyed by service folder name (`devbox/services/<name>/`) |
| `pending` | object | Pending operations that need to be applied; present only when `devbox services enable/disable` was run without `--apply`. Written atomically by the toggle command; cleared by `devbox restart`, `devbox deploy run`, or `devbox reset run` |

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
| `config_hash` | sha256 hex | Fingerprint of `devbox/services/<name>/service.yml` + `devbox/services/<name>/deploy.yml` |
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

When `devbox services enable` or `devbox services disable` is run without `--apply`, the toggle command writes the local.yml change immediately but defers the apply step. The `pending` field in the state file tracks what still needs to run.

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
| `devbox services enable/disable` (without `--apply`) | Writes `pending.operations`; adds/merges ops for restart or deploy contributors |
| `devbox services enable/disable --apply` success | Clears contributor-owned pending ops via `ClearPendingOps` |
| `devbox restart` success | Clears the `restart` op; deploy op (if any) survives |
| `devbox deploy run` (full project) success | Clears the `deploy` op; restart op (if any) survives |
| `devbox deploy run --service <name>` success | Removes `<name>` from the `deploy` op's service list; if empty, removes the op |
| `devbox reset run` (project-wide) success | Clears all pending (full journal wipe) |
| `devbox reset run --service <name>` success | Writes `{kind: deploy, services: [<name>]}` atomically alongside removing service deployed state |

### Banner

`devbox status` (and its subcommands `apps`, `tools`, `infra`, `deploy`) display a warning banner when `pending` is non-nil:

```
⚠ Pending: deploy required for: svc-a, svc-b
  Run: devbox deploy run
⚠ Pending: restart required
  Run: devbox restart
```

The banner is rendered by `render.PendingBanner(p *journal.PendingApply)` in `internal/core/ui/render/`. It iterates `pending.operations` and renders one line per op. Empty string is returned (no banner) when `pending` is nil.

## See also

- [Hashing and skip decisions](hashing.md) — how `action_hash` and `config_hash` are computed and how they drive the skip decision table
- [Management](management.md) — inspect, clear, and repair commands
- [Overview](index.md) — purpose, file location, lock file

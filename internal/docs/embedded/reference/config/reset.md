# Reset

The `devbox reset run` command tears down the project or a single service and
returns it to a clean state that requires a subsequent deploy.

## Project-wide reset

```
devbox reset run [--yes]
```

Executes `devbox/reset.yml`. On success the entire deploy state journal is
removed, so every service appears as not-deployed in `devbox status`.

| Option | Description |
|--------|-------------|
| `--yes` / `-y` | Skip confirmation prompts inside reset steps |

## Per-service reset

```
devbox reset run --service <name> [--yes] [--skip-preflight]
```

Resets a single service without touching the rest of the project:

1. Runs stop-stage preflight checks (docker, git binaries; port availability check is skipped for stop stage).
2. If the service is currently **enabled**, runs any `on_disable.before` user commands declared in `devbox/services/<name>/service.yml` (outside the project lock).
3. Acquires the project lock, then:
   a. Stops the service container directly via `docker stop` (bypasses compose — works whether the service is enabled or disabled).
   b. Executes `devbox/services/<name>/reset.yml` if present (same pipeline format as project-wide `reset.yml`).
   c. Atomically removes the service's deployed state from the journal and writes a `PendingDeploy` entry.
4. Releases the lock.

After a per-service reset, `devbox status` shows a pending-deploy banner for
the service. Run `devbox deploy run --service <name>` to re-provision it.

### Requirements

- The service must exist in `devbox/services/<name>/`.
- **The service must have `devbox/services/<name>/deploy.yml`** — per-service
  reset writes a `PendingDeploy` journal entry, so the service must be
  deployable. If there is no `deploy.yml`, use the full `devbox reset run`
  instead.

Required services (`required: true`) are **allowed** to be per-service reset (`required` protects
from `services disable`, not from reset).

| Option | Description |
|--------|-------------|
| `--service <name>` | Reset only this service |
| `--yes` / `-y` | Skip the confirmation prompt |
| `--skip-preflight` | Bypass environment probes before running |

### Per-service `reset.yml`

`devbox/services/<name>/reset.yml` follows the same format as the project-wide
`devbox/reset.yml`. It is optional; when absent, only the container stop and
journal update occur.

```yaml
# devbox/services/postgres/reset.yml
phases:
  - name: wipe
    steps:
      - name: drop-volume
        type: shell
        cmd: docker volume rm project_postgres_data || true
```

### Pending state lifecycle

| Command | Effect on journal |
|---------|------------------|
| `devbox reset run --service <name>` | Removes `state.services.<name>`, writes `PendingDeploy` for `<name>` |
| `devbox deploy run --service <name>` | Clears `PendingDeploy` for `<name>` on success |
| `devbox reset run` (project-wide) | Removes the entire state file (`ClearPending` semantics) |

# Reset

The `dwe reset run` command tears down the project or a single service and
returns it to a clean state that requires a subsequent deploy.

## Project-wide reset

```
dwe reset run [--yes]
```

Executes `workspace/reset.yml`. The file is **optional** — when absent, DWE uses the built-in default reset pipeline and prints one info line to stderr: `Using built-in default reset pipeline (override with workspace/reset.yml).` The info line is suppressed in `--output json` mode.

**Default reset pipeline** (fires when `workspace/reset.yml` is absent):

Phases: `pre` (confirm prompt: "This will stop containers, remove project volumes, and delete generated data.") → `stop` (`type: dwe`, `cmd: "docker down"`) → `cleanup` (remove all project volumes, then remove the `services/` directory). Volume removal is resilient: an individual volume that cannot be dropped (e.g. still in use) is reported and skipped so it does **not** abort the reset, while a genuinely broken setup — project name unresolvable or `docker volume ls` failing — stays fatal (the reset aborts rather than clearing the journal with volumes left behind).

On success the entire deploy state journal is
removed, so every service appears as not-deployed in `dwe status`.

| Option | Description |
|--------|-------------|
| `--yes` / `-y` | Skip confirmation prompts inside reset steps |

## Per-service reset

```
dwe reset run --service <name> [--yes] [--skip-preflight]
```

Resets a single service without touching the rest of the project:

1. Runs stop-stage preflight checks (docker, git binaries; port availability check is skipped for stop stage).
2. Shows an interactive confirmation form listing exactly what will happen (skipped with `--yes`).
3. If the service is currently **enabled**, runs any `on_disable.before` user commands declared in `workspace/services/<name>/service.yml` (outside the project lock).
4. Acquires the project lock, then executes a single pipeline composed of:
   a. **Baseline (always-on):** stop **and remove** the service container directly via `docker stop` + `docker rm -f` (bypasses compose — works whether the service is enabled or disabled).
   b. **Baseline (conditional):** delete the service directory if the service declares `dir:` in `service.yml` and the directory exists on disk.
   c. **User pipeline (optional):** the phases declared in `workspace/services/<name>/reset.yml` if present, appended after the baseline.
5. Atomically removes the service's deployed state from the journal and writes a `PendingDeploy` entry.
6. Releases the lock.

After a per-service reset, `dwe status` shows a pending-deploy banner for
the service. Run `dwe deploy run --service <name>` to re-provision it.

**Volumes are not touched automatically.** If you need to drop the service's
Docker volumes as part of reset, declare a `services/<name>/reset.yml` with a
step calling [`docker_remove_project_volumes`](deploy/builtins.md#docker_remove_project_volumes).

### Requirements

- The service must exist in `workspace/services/<name>/`.
- **The service must have `workspace/services/<name>/deploy.yml`** — per-service
  reset writes a `PendingDeploy` journal entry, so the service must be
  deployable. If there is no `deploy.yml`, use the full `dwe reset run`
  instead.

Required services (`required: true`) are **allowed** to be per-service reset (`required` protects
from `services disable`, not from reset).

| Option | Description |
|--------|-------------|
| `--service <name>` | Reset only this service |
| `--yes` / `-y` | Skip the confirmation prompt |
| `--skip-preflight` | Bypass environment probes before running |

### Per-service `reset.yml`

`workspace/services/<name>/reset.yml` follows the same format as the project-wide
`workspace/reset.yml`. It is **optional** and is appended after the always-on
baseline (container stop+rm, optional `dir:` removal). When absent, only the
baseline and the journal update occur.

```yaml
# workspace/services/postgres/reset.yml
phases:
  - name: wipe
    steps:
      - name: drop-volume
        type: builtin
        cmd: docker_remove_project_volumes
```

### Pending state lifecycle

| Command | Effect on journal |
|---------|------------------|
| `dwe reset run --service <name>` | Removes `state.services.<name>`, writes `PendingDeploy` for `<name>` |
| `dwe deploy run --service <name>` | Clears `PendingDeploy` for `<name>` on success |
| `dwe reset run` (project-wide) | Removes the entire state file (`ClearPending` semantics) |

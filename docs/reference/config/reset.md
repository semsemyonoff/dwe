# Reset

The `dwe reset run` command tears down the project or a single service and
returns it to a clean state that requires a subsequent deploy.

## Project-wide reset

```
dwe reset run [--yes] [--skip-preflight] [--clear-generated]
```

Executes `workspace/reset.yml`. The file is **optional** — when absent, DWE uses the built-in default reset pipeline and prints one info line to stderr: `Using built-in default reset pipeline (override with workspace/reset.yml).` The info line is suppressed in `--output json` mode.

**Default reset pipeline** (fires when `workspace/reset.yml` is absent):

Phases: `pre` (confirm prompt: "This will stop containers, remove project volumes, and delete generated data.") → `stop` (`type: dwe`, `cmd: "docker down"`) → `cleanup` (remove all project volumes, then remove the `services/` directory). Volume removal is resilient: an individual volume that cannot be dropped (e.g. still in use) is reported and skipped so it does **not** abort the reset, while a genuinely broken setup — project name unresolvable or `docker volume ls` failing — stays fatal (the reset aborts rather than clearing the journal with volumes left behind).

On success the entire deploy state journal is
removed, so every service appears as not-deployed in `dwe status`.

| Option | Description |
|--------|-------------|
| `--yes` / `-y` | Skip confirmation prompts inside reset steps |
| `--skip-preflight` | Bypass environment probes and project checks before running |
| `--clear-generated` | Also clear the harvested generated-value store (`.dwe/generated.yml`) so secrets regenerate on the next deploy (preserved by default) |

## Per-service reset

```
dwe reset run --service <name> [--yes] [--skip-preflight] [--clear-generated]
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
| `--clear-generated` | Also clear this service's harvested generated values (`.dwe/generated.yml`); forces regeneration on next deploy |

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
| `dwe reset run` (project-wide) | Deletes the entire state file (`journal.Remove`) |

## Related commands

- `dwe reset plan` — show the resolved reset pipeline
- `dwe reset run [--yes]` — execute it (see [Project-wide reset](#project-wide-reset))
- `dwe reset eject [--out PATH] [--force]` — emit the **built-in default** reset pipeline as a commented, editable `reset.yml`. It is a constant, not this project's effective plan: nothing is rendered, per-service `workspace/services/<name>/reset.yml` files are not inlined, and there is no `--service` filter (use `dwe reset plan` for the resolved instance). With no `--out` (or `--out -`) the document goes to stdout and nothing is written; with `--out PATH` it is written to that file and **refuses to overwrite an existing one unless `--force` is given**. The emitted file declares `log: false` explicitly, matching the built-in default. There is no implicit default path: the canonical target is `workspace/reset.yml`, passed explicitly, and an active file **replaces** the built-in pipeline whole.
- `dwe validate` — when it reports `reset.yml has no active content (all comments or empty) — built-in default pipeline is active` (or `declares no phases`), `dwe reset eject` is the action that answers it: the file on disk has no effect, so eject the built-in pipeline over it with `--force` and edit from there. `eject`'s own refusal names the same two conditions, so the two commands agree about which files are inert. See [validate.md](validate.md).
- `dwe deploy eject` — the deploy-side twin; see [deploy/index.md](deploy/index.md).

There is deliberately **no `lifecycle eject`**. The effective `stop` pipeline always
carries the engine-synthetic `_auto_reap_daemons` phase, and a user-authored phase
whose name starts with `_` is rejected at load time — an emitted `lifecycle.yml`
would be a file dwe itself refuses to load back.

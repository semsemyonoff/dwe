# State Management

Command flags, management subcommands, non-interactive defaults, and worked examples for the deploy state file.

## Command flags

### `--force`

Ignore the deploy state file and re-run all steps from scratch.

```bash
dwe deploy run --force
```

Useful when:
- You want to guarantee a fresh deploy regardless of prior state
- The state file is corrupted and you cannot repair it
- You need to re-run steps that were skipped due to unchanged hashes

When `--force` is used, the state file is cleared before the pipeline runs (all steps are treated as absent for skip decisions).

### `--resume`

Continue from the last failed or partially deployed step.

```bash
dwe deploy run --resume
```

Use this after a failed deploy to pick up where it left off, rather than re-running already-completed steps.

In non-interactive mode (no TTY, no `--yes` flag):
- If the last run was failed/partial, you must use `--resume` or `--force` to proceed
- Without a flag, the command exits with an error (fail-safe for CI)

In interactive mode (TTY without `--yes`):
- If the last run was failed/partial, you are prompted to choose: resume, re-run all steps (state ignored — `when:` still applies), or cancel

### `-y` / `--non-interactive`

Suppress all interactive prompts.

```bash
dwe deploy run -y
dwe deploy run --non-interactive
```

Use in CI/CD pipelines to ensure the deploy does not hang waiting for user input.

**Behavior under `-y`:**

- If the project is already deployed and config hashes match: exits successfully (no-op)
- If the project is already deployed but config changed: applies delta (re-run only changed scopes)
- If the last run failed/partial: exits with error (use `--force` or `--resume` to override)

## Management commands

### `dwe deploy state show`

Display the contents of `.dwe/deploy/state.yml` in YAML format.

```bash
dwe deploy state show
```

Shows:
- Project-level status, config hash, and last-run timing
- Per-service status, config hash, and last-run timing
- Per-phase and per-step outcomes, action hashes, and durations

Useful for debugging why a step was skipped, or to inspect the journal after a deploy.

### `dwe deploy state clear`

Delete the deploy state file.

```bash
dwe deploy state clear
```

Equivalent to `rm .dwe/deploy/state.yml`. In interactive mode (TTY), prompts for confirmation. Use `-y` to skip confirmation in CI.

```bash
dwe deploy state clear -y  # Non-interactive
```

After clearing, the next `dwe deploy run` treats all steps as absent and re-runs them.

### `dwe deploy state repair`

Rebuild status aggregates from per-step records.

```bash
dwe deploy state repair
```

Recomputes:
- Per-phase status (from step outcomes)
- Per-service status (from phase outcomes)
- Project status (from per-service outcomes)

Preserves all step-level data (action hashes, timestamps, durations). Use this to fix status inconsistencies that might arise from manual edits or unexpected crashes.

## Non-interactive defaults

In non-interactive mode (no TTY, `STDIN` is piped or closed):

| Project state | Behavior |
|---|---|
| not deployed | runs pipeline |
| deployed, config hash matches, no check steps | exits 0, no-op |
| deployed, config hash matches, has check steps | runs pipeline; skips unchanged steps, re-runs check steps |
| deployed, config hash diverged | runs pipeline; re-runs changed scopes, skips unchanged ones |
| last_run failed/partial | exits 1, requires `--resume` or `--force` |

## Examples

### Example: full deploy, then skip on re-run

```bash
$ dwe deploy run
✓ Phase setup
  ✓ create-dirs
  ✓ install
✓ Phase init
  ✓ db-create
  ✓ migrate
✓ Phase finalize
  ✓ render-ide

# State file recorded: all steps ok, hashes match

$ dwe deploy run
✓ all steps already deployed, skipped

# (Or if there were check steps:)
$ dwe deploy run
✓ Phase setup
  · create-dirs  (skipped by state)
  · install      (skipped by state)
✓ Phase init
  · db-create    (skipped by state)
  · migrate      (skipped by state)
✓ Phase finalize
  ◎ render-ide   (check re-validated)
```

### Example: edit a step, re-run on next deploy

```yaml
# workspace/deploy/main.yml
- name: install
  type: command
  cmd: app.install  # was "app.install"
  # (hash was abc123)
```

Edit the command:

```yaml
- name: install
  type: command
  cmd: app.install-prod  # changed
  # (hash is now def456)
```

```bash
$ dwe deploy run
✓ Phase setup
  · create-dirs  (skipped by state)
  ✓ install      (re-run: hash changed abc123 → def456)
✓ Phase init
  ✓ db-create
  ✓ migrate
```

The install step re-runs because its hash changed. Steps with unchanged hashes are skipped.

### Example: edit a service config, invalidate service scope

```yaml
# workspace/services/main/service.yml
enabled: true
type: app
dir: services/main
depends_on:
  - db
```

Edit the main service:

```yaml
# workspace/services/main/service.yml
enabled: true
type: app
dir: services/main
depends_on:
  - db
  - cache  # added dependency
```

The service's `config_hash` changes, so all of `main`'s steps re-run:

```bash
$ dwe deploy run
✓ Phase setup (main)
  ✓ create-dirs    (re-run: service config_hash changed)
  ✓ install        (re-run: service config_hash changed)
✓ Phase init (main)
  ✓ db-create      (re-run: service config_hash changed)
  ✓ migrate        (re-run: service config_hash changed)
```

### Example: force re-run all steps

```bash
dwe deploy run --force
```

Clears the state file and re-runs all steps from scratch, even if they all succeeded previously.

> **Note:** `--force` only ignores the deploy state. Phase- and step-level `when:` conditions are still
> evaluated on every run. For example, `when: dir-empty services/main/src` will still skip an install step
> once the directory has been populated by a previous successful run. To wipe service directories,
> Docker volumes, and other artifacts so the next deploy is truly clean, use `dwe reset run && dwe deploy run`.

### Example: recover from a mid-deploy crash

```bash
$ dwe deploy run
✓ Phase setup
✓ Phase init
✗ Phase finalize
  ✗ render-ide  (failed)

# Process crashed or was killed. State file recorded the failure.

$ dwe deploy run  # (in interactive mode)
# Prompted: "Failed deploy detected: Resume / Re-run all steps / Cancel"
# Choose: Resume

✓ Phase setup
  · create-dirs (skipped)
  · install     (skipped)
✓ Phase init
  · db-create   (skipped)
  · migrate     (skipped)
✓ Phase finalize
  ✓ render-ide  (re-run from where it failed)
```

Or in non-interactive mode:

```bash
dwe deploy run --resume
```

## See also

- [Overview](index.md) — purpose, file location, lock file
- [Schema](schema.md) — field reference
- [Hashing and skip decisions](hashing.md) — how skips are determined

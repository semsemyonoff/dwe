# Deploy State (.devbox/deploy/state.yml)

Idempotent deploy state tracking and skip-decision table.

## Contents

- [Purpose](#purpose)
- [File location](#file-location)
- [When to use](#when-to-use)
- [Schema](#schema)
  - [Top-level fields](#top-level-fields)
  - [Project-level fields](#project-level-fields)
  - [Service fields](#service-fields)
  - [Phase fields](#phase-fields)
  - [Step fields](#step-fields)
- [Hashing](#hashing)
  - [action_hash](#action_hash)
  - [config_hash for services](#config_hash-for-services)
  - [config_hash for the project](#config_hash-for-the-project)
  - [Hash invalidation](#hash-invalidation)
- [Skip decision table](#skip-decision-table)
- [Lock file](#lock-file)
- [Command flags](#command-flags)
  - [`--force`](#--force)
  - [`--resume`](#--resume)
  - [`-y` / `--non-interactive`](#-y----non-interactive)
- [Management commands](#management-commands)
  - [`devbox deploy state show`](#devbox-deploy-state-show)
  - [`devbox deploy state clear`](#devbox-deploy-state-clear)
  - [`devbox deploy state repair`](#devbox-deploy-state-repair)
- [Non-interactive defaults](#non-interactive-defaults)
- [Examples](#examples)
- [Related commands](#related-commands)

## Purpose

The deploy state file (`.devbox/deploy/state.yml`) turns the deploy pipeline from "fire-and-forget" into something **idempotent and observable**.

> **Note** — only deploy pipelines have a state file. [`type: daemon`](commands.md#type-daemon) commands have **no on-disk registry**: `docker ps` (filtered on the standard `devbox.project` / `devbox.daemon.id` / `devbox.daemon.params` labels) is the single source of truth for which daemons are running. There is no journal to drift, lock, or invalidate; a `docker stop` issued outside devbox is reflected immediately on the next `devbox status daemons` read.

Every step executed during `devbox deploy run` is recorded: its status (ok, failed, partial, in_progress, skipped), the timestamp it finished, its `action_hash` (fingerprint of the step body), and how long it took to run.

On the next `devbox deploy run`, each step's `action_hash` is compared to the recorded hash. Steps that succeeded with matching hashes are **skipped** (unless they have a `check:` action, which always runs to re-validate idempotency). Steps whose hash changed, or that previously failed, are **re-run**.

This mechanism ensures that:
- Deploying an unchanged codebase is fast (unchanged steps skip)
- Editing a step body automatically re-triggers it
- Editing config files (`services.yml`, `deploy.yml`) invalidates the affected scope and re-runs the impacted steps
- The journal survives crashes mid-deploy, allowing `--resume` on the next run

## File location

`.devbox/deploy/state.yml` — automatically created in the project root's `.devbox/deploy/` directory. Not checked into version control (add `.devbox/` to `.gitignore`).

> **Interaction with `snapshot restore`** — `deploy-state.yml` is always **overwritten** from the snapshot on restore. No merge is performed (unlike `devbox/local.yml`, which honours [`local_yml.preserve_keys`](snapshot.md#local_ymlpreserve_keys)). Orphan entries for services that no longer exist locally are safe — the deploy pipeline ignores them on the next run. If you need to keep machine-specific deploy state across snapshots, use `local_yml.preserve_keys` for the values that drive that state rather than trying to preserve the journal itself.

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

## Schema

### Top-level fields

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | string | Always `"1"`; reserved for future format changes |
| `project` | object | Project-level state (deployed_at, config_hash, status, etc.) |
| `services` | map | Per-service state, keyed by service name from `services.yml` |

### Project-level fields

| Field | Type | Description |
|-------|------|-------------|
| `deployed_at` | ISO 8601 timestamp | When the project was last fully deployed |
| `config_hash` | sha256 hex | Fingerprint of tracked services + top-level deploy config + per-service deploy configs. Edits to enabled-but-untracked service variants (e.g., `main-debug`) do not change this hash. |
| `status` | enum | `deployed`, `partial`, `failed`, `not_deployed`, `in_progress` |
| `last_run` | object | Timing and outcome of the last deploy attempt (`status`, `started_at`, `finished_at`) |
| `phases` | map | Per-phase state for project-level (non-service) phases |

### Service fields

| Field | Type | Description |
|-------|------|-------------|
| `status` | enum | `deployed`, `partial`, `failed`, `not_deployed` (service never ran, or all steps skipped) |
| `deployed_at` | ISO 8601 timestamp | When this service was last fully deployed |
| `config_hash` | sha256 hex | Fingerprint of `services.<name>` block + per-service `deploy/<name>.yml` |
| `last_run` | object | Timing and outcome of the last deploy attempt for this service |
| `phases` | map | Per-phase state for this service's phases |

### Phase fields

| Field | Type | Description |
|-------|------|-------------|
| `status` | enum | `ok`, `failed`, `skipped` |
| `steps` | map | Per-step state, keyed by step name |

### Step fields

| Field | Type | Description |
|-------|------|-------------|
| `status` | enum | `ok`, `failed`, `in_progress` |
| `finished_at` | ISO 8601 timestamp | When this step completed (absent if in_progress) |
| `action_hash` | sha256 hex | Fingerprint of the step's `type`, `cmd`, and `with:` parameters |
| `duration_ms` | integer | How long the step took to execute, in milliseconds |

## Hashing

Two kinds of hashes determine skip decisions: `action_hash` (step fingerprint) and `config_hash` (configuration scope).

### action_hash

A step's `action_hash` is a SHA-256 digest of:

```
sha256(type + "\x00" + cmd + "\x00" + canonical_json(with))
```

**Components:**

- `type` — the step type (`shell`, `devbox`, `command`, `builtin`)
- `cmd` — the command payload
- `with` — step parameters, serialized as canonical JSON with keys sorted alphabetically

**Key properties:**

- If you edit a step's `type`, `cmd`, or `with:` parameters, its hash changes → the step runs on the next deploy
- YAML formatting, whitespace, and comment changes do NOT change the hash (hash is computed from parsed Go structs, not raw YAML bytes)
- Key order in `with:` does not matter (keys are sorted during canonicalization)
- If `with:` is absent or nil, it is hashed as an empty object

**Examples:**

```yaml
# Step 1: creates a database
- name: create-db
  type: command
  cmd: app.db.create
  with:
    host: localhost
    port: 3306

# On the next run, this step will skip (same hash)
# unless you edit type, cmd, or with parameters

# If you change with key order, hash stays the same:
- name: create-db
  type: command
  cmd: app.db.create
  with:
    port: 3306
    host: localhost  # reordered, hash unchanged

# If you change a parameter value, hash changes:
- name: create-db
  type: command
  cmd: app.db.create
  with:
    host: 127.0.0.1  # changed; step will re-run
    port: 3306
```

### config_hash for services

A service's `config_hash` covers two things:

```
sha256(canonical_json(services.<name>) + canonical_json(deploy/<name>.yml))
```

- The service definition from `services.yml` (Enabled, Depends, Type, Dir, etc.)
- The per-service deploy pipeline from `devbox/deploy/<name>.yml` (or empty if absent)

When the service's `config_hash` changes (e.g., you edit `services.yml` or `devbox/deploy/main.yml`), **all steps in that service's phases are treated as absent**. They re-run on the next deploy regardless of their `action_hash`.

### config_hash for the project

The project-level `config_hash` covers three things:

```
sha256(canonical_json(services[tracked_only]) + canonical_json(deploy.yml) + canonical_json(deploy/<tracked>.yml for all tracked services))
```

**"Tracked" means:** A service is tracked iff it appears in the resolved deploy plan (i.e., enabled in `services.yml` AND inlined by a `deploy_services: true` phase in `deploy.yml`). Tools are never tracked. Services without a `deploy/<name>.yml` are still tracked if they appear in the plan.

When the project's `config_hash` changes (e.g., you edit `devbox/deploy.yml` or add a service), **all project-scope steps are treated as absent** and re-run on the next deploy.

Note: edits to enabled-but-untracked service variants (e.g., a `main-debug` service extending `main` without its own deploy config) do NOT change the project hash, so they do not invalidate the journal.

### Hash invalidation

Invalidation happens in **two layers**:

1. **Service-scope validation** — before deciding whether a service step should skip, check: does the service's current `config_hash` match the persisted one? If not, treat the step's prior state as absent.

2. **Project-scope validation** — before deciding whether a project-level step should skip, check: does the project's current `config_hash` match the persisted one? If not, treat the step's prior state as absent.

This ensures that a changed `services.yml` cannot lead to skips, even when step bodies are unchanged.

## Skip decision table

Once config-hash validation passes (or the scope is unchanged), the step's prior `StepState` is evaluated against this table:

| Prior state | Hash match | Has `check:` | Decision |
|---|---|---|---|
| absent | — | — | **Run** |
| ok | yes | no | **Skip** |
| ok | yes | yes | **Run** (check re-validates) |
| ok | no | — | **Run** |
| failed / partial / in_progress | — | — | **Run** (resume) |

**Key insight:** Steps with a `check:` action **always run**, even if their hash matches and prior status was ok. The `check:` re-validates that the step's intended effect is still present (idempotency check). This prevents false skips when external state has changed.

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

## Command flags

### `--force`

Ignore the deploy state file and re-run all steps from scratch.

```bash
devbox deploy run --force
```

Useful when:
- You want to guarantee a fresh deploy regardless of prior state
- The state file is corrupted and you cannot repair it
- You need to re-run steps that were skipped due to unchanged hashes

When `--force` is used, the state file is cleared before the pipeline runs (all steps are treated as absent for skip decisions).

### `--resume`

Continue from the last failed or partially deployed step.

```bash
devbox deploy run --resume
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
devbox deploy run -y
devbox deploy run --non-interactive
```

Use in CI/CD pipelines to ensure the deploy does not hang waiting for user input.

**Behavior under `-y`:**

- If the project is already deployed and config hashes match: exits successfully (no-op)
- If the project is already deployed but config changed: applies delta (re-run only changed scopes)
- If the last run failed/partial: exits with error (use `--force` or `--resume` to override)

## Management commands

### `devbox deploy state show`

Display the contents of `.devbox/deploy/state.yml` in YAML format.

```bash
devbox deploy state show
```

Shows:
- Project-level status, config hash, and last-run timing
- Per-service status, config hash, and last-run timing
- Per-phase and per-step outcomes, action hashes, and durations

Useful for debugging why a step was skipped, or to inspect the journal after a deploy.

### `devbox deploy state clear`

Delete the deploy state file.

```bash
devbox deploy state clear
```

Equivalent to `rm .devbox/deploy/state.yml`. In interactive mode (TTY), prompts for confirmation. Use `-y` to skip confirmation in CI.

```bash
devbox deploy state clear -y  # Non-interactive
```

After clearing, the next `devbox deploy run` treats all steps as absent and re-runs them.

### `devbox deploy state repair`

Rebuild status aggregates from per-step records.

```bash
devbox deploy state repair
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
$ devbox deploy run
✓ Phase setup
  ✓ create-dirs
  ✓ install
✓ Phase init
  ✓ db-create
  ✓ migrate
✓ Phase finalize
  ✓ render-ide

# State file recorded: all steps ok, hashes match

$ devbox deploy run
✓ all steps already deployed, skipped

# (Or if there were check steps:)
$ devbox deploy run
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
# devbox/deploy/main.yml
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
$ devbox deploy run
✓ Phase setup
  · create-dirs  (skipped by state)
  ✓ install      (re-run: hash changed abc123 → def456)
✓ Phase init
  ✓ db-create
  ✓ migrate
```

The install step re-runs because its hash changed. Steps with unchanged hashes are skipped.

### Example: edit services.yml, invalidate service scope

```yaml
# services.yml
main:
  enabled: true
  type: app
  dir: services/main
  depends_on:
    - db
```

Edit the main service:

```yaml
# services.yml
main:
  enabled: true
  type: app
  dir: services/main
  depends_on:
    - db
    - cache  # added dependency
```

The service's `config_hash` changes, so all of `main`'s steps re-run:

```bash
$ devbox deploy run
✓ Phase setup (main)
  ✓ create-dirs    (re-run: service config_hash changed)
  ✓ install        (re-run: service config_hash changed)
✓ Phase init (main)
  ✓ db-create      (re-run: service config_hash changed)
  ✓ migrate        (re-run: service config_hash changed)
```

### Example: force re-run all steps

```bash
devbox deploy run --force
```

Clears the state file and re-runs all steps from scratch, even if they all succeeded previously.

> **Note:** `--force` only ignores the deploy state. Phase- and step-level `when:` conditions are still
> evaluated on every run. For example, `when: dir-empty services/main/src` will still skip an install step
> once the directory has been populated by a previous successful run. To wipe service directories,
> Docker volumes, and other artifacts so the next deploy is truly clean, use `devbox reset run && devbox deploy run`.

### Example: recover from a mid-deploy crash

```bash
$ devbox deploy run
✓ Phase setup
✓ Phase init
✗ Phase finalize
  ✗ render-ide  (failed)

# Process crashed or was killed. State file recorded the failure.

$ devbox deploy run  # (in interactive mode)
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
devbox deploy run --resume
```

## Related commands

- `devbox deploy plan` — preview the pipeline before deploying
- `devbox deploy run` — execute deploy (with state tracking)
- `devbox deploy state show` — inspect state file
- `devbox deploy state clear` — reset state
- `devbox deploy state repair` — rebuild status aggregates
- `devbox reset run` — reset and clear deploy state
- See also [deploy.yml](deploy.md) — how to declare steps and phases
- See also [lifecycle.yml](lifecycle.md) — how `devbox run` gates on mandatory service deployment

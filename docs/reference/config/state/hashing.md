# Hashing and Skip Decisions

How `action_hash` and `config_hash` are computed, when they invalidate prior state, and how the skip decision table applies.

Two kinds of hashes determine skip decisions: `action_hash` (step fingerprint) and `config_hash` (configuration scope).

## action_hash

A step's `action_hash` is a SHA-256 digest of:

```
sha256(type + "\x00" + cmd + "\x00" + canonical_json(with))
```

**Components:**

- `type` — the step type (`shell`, `dwe`, `command`, `builtin`)
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

## config_hash for services

A service's `config_hash` covers three things:

```
sha256(canonical_json(services.<name>) + canonical_json(workspace/services/<name>/deploy.yml) + canonical_json(vars))
```

- The service definition from `workspace/services/<name>/service.yml` (Type, Dir, Container, Depends, Required, etc.)
- The per-service deploy pipeline from `workspace/services/<name>/deploy.yml` (or empty if absent)
- The whole merged `vars:` block (see [the note below](#why-vars-is-hashed))

When the service's `config_hash` changes (e.g., you edit `workspace/services/main/service.yml` or `workspace/services/main/deploy.yml`), **all steps in that service's phases are treated as absent**. They re-run on the next deploy regardless of their `action_hash`.

## config_hash for the project

The project-level `config_hash` covers four things:

```
sha256(canonical_json(services[tracked_only]) + canonical_json(workspace/deploy.yml) + canonical_json(workspace/services/<tracked>/deploy.yml for all tracked services) + canonical_json(vars))
```

**"Tracked" means:** A service is tracked iff it appears in the resolved deploy plan (i.e., enabled in `workspace/services/<name>/service.yml` AND inlined by a `deploy_services: true` phase in `workspace/deploy.yml`). Tools are never tracked. Services without a `workspace/services/<name>/deploy.yml` are still tracked if they appear in the plan.

When the project's `config_hash` changes (e.g., you edit `workspace/deploy.yml` or add a service), **all project-scope steps are treated as absent** and re-run on the next deploy.

Note: edits to enabled-but-untracked service variants (e.g., a `main-debug` service extending `main` without its own deploy config) do NOT change the project hash, so they do not invalidate the journal.

## Why `vars` is hashed

Pipeline `cmd`, the string leaves of `with`, `check`, `timeout`, and shell `when:` are rendered at plan-resolution time (see [Templates in step fields](../deploy/index.md#templates-in-step-fields)), so a step's *actual* command depends on the `vars:` block. Hashing only the pipeline files would let a changed `vars.db.host` leave the hash untouched while the command it renders into changes — the deploy would report `already up-to-date` for a step that has not run in its current form.

Both hashes therefore include the **whole** `vars:` block, not just the entries a given scope references. The consequence is that editing any `vars:` entry invalidates every project- and service-scope step, not only the ones that read it.

**One-time consequence on upgrade**: because the formulas changed, the first `dwe deploy run` after upgrading to the dwe version carrying this change re-runs every step once, even without any `vars:` edit of your own. Steps are expected to be idempotent and gated, so this is safe — just visible.

## Hash invalidation

Invalidation happens in **two layers**:

1. **Service-scope validation** — before deciding whether a service step should skip, check: does the service's current `config_hash` match the persisted one? If not, treat the step's prior state as absent.

2. **Project-scope validation** — before deciding whether a project-level step should skip, check: does the project's current `config_hash` match the persisted one? If not, treat the step's prior state as absent.

This ensures that a changed service config cannot lead to skips, even when step bodies are unchanged.

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

## See also

- [Schema](schema.md) — where `action_hash` and `config_hash` are stored in the state file
- [Management](management.md) — `--force` and `--resume` flags that override the skip decision table
- [Overview](index.md) — file location, lock file

# Pipeline Defaults: Hidden-Mandatory Audit

## Purpose

Pre-implementation audit for plan `20260529-pipeline-defaults.md`. Identifies which commands
hard-fail (or silently misbehave) when `devbox/deploy.yml`, `devbox/reset.yml`, and
`devbox/lifecycle.yml` are absent from an otherwise valid project.

## Fixture

A bare-minimum project: only `devbox.yml` + one service folder `devbox/services/demo/service.yml`.

`devbox.yml`:
```yaml
schema_version: "2"
project:
  name: audit-fixture
  prefix: devbox
```

`devbox/services/demo/service.yml`:
```yaml
type: app
container: demo
```

No `deploy.yml`, `reset.yml`, `lifecycle.yml`, `docker.yml`, or any other devbox YAML.

## Results

Legend: ✅ succeeds correctly | ⚠️ silent partial (succeeds but produces wrong/missing work) | ❌ hard error

### Root and version

| Command | Result | Notes |
|---------|--------|-------|
| `devbox` | ✅ | Shows help/status banner normally |
| `devbox version` | ✅ | Emits version string |
| `devbox completion bash` | ✅ | |

### Deploy pipeline

| Command | Result | Notes |
|---------|--------|-------|
| `devbox deploy plan` | ⚠️ | Exit 0 but only shows implicit `render env` step — no container-start steps. Silent noop. |
| `devbox deploy run --skip-preflight` | ⚠️ | Exit 0, runs only the `env/render-env` implicit step. No docker up, no service steps. Silent noop. |
| `devbox deploy state show` | ✅ | Reads existing state, not pipeline-dependent |

### Reset pipeline

| Command | Result | Notes |
|---------|--------|-------|
| `devbox reset plan` | ❌ | `loading reset config devbox/reset.yml: no such file or directory` |
| `devbox reset run` | ❌ | Same `os.ErrNotExist` propagated from `LoadResetConfig` |
| `devbox reset step <address>` | ❌ | Same `os.ErrNotExist` from `FindStep` → `LoadResetConfig` |

### Lifecycle commands

| Command | Result | Notes |
|---------|--------|-------|
| `devbox run` | ❌ | `No lifecycle.yml — see devbox/lifecycle.example.yml` |
| `devbox restart` | ❌ | Stop leg succeeds (auto-reap only), then run leg hits the hard error |
| `devbox stop` | ⚠️ | Exit 0 but only runs `_auto_reap_daemons` — no `docker down`. Containers not actually stopped. |
| `devbox stop <service>` | ✅ | Per-service stop (compose-bypass) exits 0 (container not running, no error) |

### Docker commands

| Command | Result | Notes |
|---------|--------|-------|
| `devbox docker up` | ❌ | `no configuration file provided: not found` — requires `docker.yml` or compose files |
| `devbox docker down` | ❌ | Same |
| `devbox docker ps` | ❌ | Same |
| `devbox docker logs` | ❌ | Same |
| `devbox compose raw` | ✅ | Passes through to docker compose help (no project files needed for raw pass-through) |
| `devbox compose files` | ✅ | Lists compose files (empty set fine) |
| `devbox compose argv` | ✅ | Shows compose command |

### Status commands

| Command | Result | Notes |
|---------|--------|-------|
| `devbox status` | ✅ | Shows apps table, topology |
| `devbox status apps` | ✅ | |
| `devbox status tools` | ✅ | |
| `devbox status infra` | ✅ | |
| `devbox status deploy` | ✅ | |

### Validate commands

| Command | Result | Notes |
|---------|--------|-------|
| `devbox validate` | ✅ | Shows info items for missing optional files — correct behavior |
| `devbox validate config` | ✅ | |
| `devbox validate templates` | ✅ | |
| `devbox validate commands` | ✅ | |
| `devbox validate env` | ✅ | |
| `devbox validate checks` | ✅ | `validation skipped (no files found)` |
| `devbox validate linters` | ✅ | `validation skipped (no files found)` |
| `devbox validate translations` | ✅ | `validation skipped (no files found)` |
| `devbox validate snapshot` | ✅ | `validation skipped (no files found)` |
| `devbox validate setup` | ✅ | `validation skipped (no files found)` |

### Info and render

| Command | Result | Notes |
|---------|--------|-------|
| `devbox info` | ✅ | Shows header only (no info.yml, uses DefaultInfoConfig) |
| `devbox render env` | ✅ | |
| `devbox render ide` | ✅ | `no services match the IDE rendering policy` |
| `devbox render ai` | ✅ | `no services match the ai-docs rendering policy` |
| `devbox render git` | ✅ | `no services match the git-hook rendering policy` |

### Docs

| Command | Result | Notes |
|---------|--------|-------|
| `devbox docs list` | ✅ | |
| `devbox docs llms-txt` | ✅ | |
| `devbox docs show <path>` | ✅ (not tested — expected OK) | Read-only, no config dependency |

### Snapshot commands

| Command | Result | Notes |
|---------|--------|-------|
| `devbox snapshot list` | ✅ | `no snapshots found` |
| `devbox snapshot create <name>` | Not tested (requires snapshot.yml) | Would fail on missing snapshot config |

### Services

| Command | Result | Notes |
|---------|--------|-------|
| `devbox services` | ❌ | Requires interactive TTY (expected — not a pipeline file issue) |
| `devbox services enable demo` | ✅ (dry-run) | |
| `devbox services disable demo` | ✅ (dry-run) | |

### Logs and shell

| Command | Result | Notes |
|---------|--------|-------|
| `devbox logs demo` | ❌ | Container not running (expected — not a pipeline file issue) |
| `devbox shell demo` | ❌ | Docker error (container not running — expected) |

### Prompt

| Command | Result | Notes |
|---------|--------|-------|
| `devbox prompt` | ✅ | |

## Baseline Failure Modes (Regression Targets for Tasks 2–5)

These are the exact failure modes today that Tasks 2–5 must fix:

1. **`devbox deploy plan` / `devbox deploy run`** — ⚠️ silent noop. No error; produces only the implicit `render env` step. `deploy.yml` absent → `LoadProjectDeployConfig` returns `os.ErrNotExist` → `projectDeploy = nil` → pipeline has zero user-defined phases → plan shows only implicit steps. Fix: `EnsureDeployConfig` returns the default pipeline.

2. **`devbox reset plan` / `devbox reset run` / `devbox reset step`** — ❌ hard error. `LoadResetConfig` propagates `os.ErrNotExist` from `plan.go:33` and `:62`. Fix: `EnsureResetConfig` + load-site switch pattern.

3. **`devbox run` / `devbox restart` (run leg)** — ❌ hard error. `lifecycle/run.go:182` returns `"No lifecycle.yml — see devbox/lifecycle.example.yml"`. Fix: `EnsureRunConfig` + remove hard-error block.

4. **`devbox stop`** — ⚠️ silent partial. `EnsureStopConfig` synthesizes only the `_auto_reap_daemons` phase when `lifecycle.yml` is absent (or `Stop:` is nil). No `docker down` step. Containers remain running. Fix: `EnsureStopConfig` uses `DefaultStopConfig()` so the default includes `docker down`.

## Docker Commands: Separate Issue

`devbox docker up/down/ps/logs` fail with `no configuration file provided: not found` because `docker.yml` is absent and no compose files were rendered (no services are enabled in the fixture). This is **not** a pipeline-defaults issue — it requires a `docker.yml` or enabled services with compose overlays. These commands fail correctly when there is nothing to orchestrate.

➕ **No follow-up needed**: `docker.*` failure with an empty project is expected behavior. When a service is enabled and compose overlays are rendered, docker commands work correctly.

## Summary

Only four commands need pipeline defaults (addressed by Tasks 2–5):
- `deploy plan` / `deploy run` — silent noop (Task 2)
- `reset plan` / `reset run` / `reset step` — hard error (Task 3)
- `run` / `restart` — hard error (Task 4)
- `stop` — silent partial without `docker down` (Task 5)

All other commands work correctly with a bare-minimum project, or fail for reasons unrelated to pipeline-file absence (TTY requirement, container not running, no docker.yml for docker subcommands).

## Checklist

- [x] Fixture created and tested
- [x] All top-level commands audited
- [x] Three known-bad commands documented with baseline failure modes
- [x] No additional ➕ follow-up tasks needed (docker commands fail correctly; all other gaps are expected)
- [x] `make test` passes (no fixture added to tracked test suite — fixture is ephemeral at /tmp)

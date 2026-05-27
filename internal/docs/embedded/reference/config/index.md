# Config Reference

Overview of all configuration files in the devbox system.

## Contents

- [File inventory](#file-inventory)
- [Loader topology](#loader-topology)
- [Merged vs standalone](#merged-vs-standalone)
- [Pages](#pages)
- [Related commands](#related-commands)

## File inventory

| File | Tracked | Loader | Purpose |
|------|---------|--------|---------|
| `devbox.yml` | yes | layer 1 | Project identity and service structure |
| `devbox/defaults.yml` | yes | layer 2 | Versioned defaults: runtime, exports, service enabled toggles |
| `devbox/local.yml` | no (gitignored) | layer 3 | Per-user overrides: state, service enabled toggles |
| `devbox/services.yml` | yes | standalone | Service declarations with dirs, cli, configs |
| `devbox/deploy.yml` | yes | standalone | Orchestrator deploy pipeline (phases + steps) |
| `devbox/deploy/<svc>.yml` | yes | standalone | Per-service deploy pipelines |
| `devbox/reset.yml` | yes | standalone | Reset pipeline |
| `devbox/lifecycle.yml` | yes | standalone | Run / stop pipelines (driving `devbox run`/`stop`/`restart`) |
| `devbox/docker.yml` | yes | standalone | Compose execution policy |
| `devbox/docker.local.yml` | no (gitignored) | merged into `docker.yml` | Local compose policy overrides |
| `devbox/styles.yml` | yes | standalone | ASCII header, color palette, separator |
| `devbox/info.yml` | yes | standalone | Info dashboard sections |
| `devbox/commands/` | yes | standalone | Declarative command definitions (per-file groups) |
| `devbox/validate.yml` | yes | standalone | Project readiness checks (preflight + `devbox validate`) |
| `devbox/snapshot.yml` | yes | standalone | Snapshot workflows: create / restore / remove (`devbox snapshot`) |
| `devbox/i18n/*.yml` | no (ignored) | standalone | User command and UI string translations (optional; one file per language) |

## Runtime artifacts

The `.devbox/` directory contains Devbox-managed artifacts and is **gitignored**:

- `.devbox/logs/` — pipeline logs (deploy, reset, lifecycle run/stop)
- `.devbox/deploy/deploy.lock` — deployment lock file (Unix-only; prevents parallel deploys)
- `.devbox/deploy/state.yml` — deployment state journal (tracks service deploy status and hashes)
- `.devbox/snapshots/snapshot.lock` — snapshot lock file (Unix-only; serialises snapshot mutating commands and is co-acquired by deploy lifecycle commands)
- `.devbox/snapshots/current` — current snapshot pointer (last created or restored snapshot)
- `.devbox/snapshots/.pre-restore-backup/` — backup of `devbox/local.yml` + `.devbox/deploy/state.yml` taken before each restore; manual recovery target on restore failure

Add `.devbox/` to your project's `.gitignore` if not already present.

## Loader topology

```mermaid
flowchart LR
  subgraph merged["3-layer merge — DevboxConfig"]
    direction TB
    A[devbox.yml] --> B[devbox/defaults.yml] --> C[devbox/local.yml]
  end

  S[devbox/services.yml] -. injected into Raw .-> merged

  merged --> R[(DevboxConfig.Raw<br/>+ typed structs)]

  subgraph standalone["Standalone loaders"]
    direction TB
    D[devbox/deploy.yml]
    DS[devbox/deploy/&lt;svc&gt;.yml]
    RS[devbox/reset.yml]
    L[devbox/lifecycle.yml]
    DK[devbox/docker.yml<br/>+ docker.local.yml]
    ST[devbox/styles.yml]
    IN[devbox/info.yml]
    CM[devbox/commands/]
  end

  R -. "dot-paths / templates" .-> D
  R -. "dot-paths / templates" .-> DS
  R -. "dot-paths / templates" .-> RS
  R -. "dot-paths / templates" .-> L
  R -. "$#123;...#125; project_name" .-> DK
  R -. "#123;#123;...#125;#125; expressions" .-> IN
  R -. "$#123;...#125; command params" .-> CM
```

## Merged vs standalone

**Merged (3-layer config)**: `devbox.yml` → `devbox/defaults.yml` → `devbox/local.yml` are deep-merged at startup. Later layers win; maps merge recursively. The result is the effective config used for `.env` generation, topology resolution, and export rules. `devbox/services.yml` is loaded separately and then injected into the merged raw map so dot-paths like `services.main.container` resolve.

**Standalone**: `services.yml`, `deploy.yml`, `deploy/<svc>.yml`, `reset.yml`, `lifecycle.yml`, `docker.yml` (+ `docker.local.yml`), `styles.yml`, `info.yml`, and `commands/*.yml` are loaded by dedicated functions in `internal/config/` and `internal/usercommands/`. They are not part of the 3-layer merge but most of them resolve template expressions against the merged config.

## Files that support local overrides

Currently, only `docker.local.yml` supports a `.local.yml` variant for per-developer customization. The pattern is:

**Docker**: `devbox/docker.yml` (tracked, shared project-wide) + `devbox/docker.local.yml` (gitignored, per-developer). The local file is merged on top of the base file, allowing developers to customize their compose execution policy — e.g., add extra volumes, mount local source directories, or override platform/args without affecting teammates.

**Why only docker?** Docker setups are inherently personal — they depend on the developer's local environment (available binaries, volume mounts, platform differences). Other configs like `lifecycle.yml`, `info.yml`, and `styles.yml` are shared project-wide and don't benefit from per-developer overrides.

For more details on `docker.local.yml` semantics and examples, see [docker.yml](docker.md#dockerlocalyml).

## Pages

- [devbox / defaults / local](devbox.md) — the 3-layer merged config: merge order, precedence, dot-path resolution, field reference
- [services.yml](services.md) — service declarations, extends, dirs, cli config
- [deploy.yml / reset.yml](deploy.md) — deploy and reset pipelines, steps, builtins, file logging, idempotent deploy
- [state.yml](state.md) — deploy state tracking, skip-decision table, hashing, lock file, recovery from crashes
- [lifecycle.yml](lifecycle.md) — run/stop pipelines, update probe, hook phases, mandatory service gate
- [Conditions and Actions](conditions.md) — typed conditions for `when:`, typed actions for `check:` and step bodies, predicate vs engine-builtin distinction
- [docker.yml](docker.md) — Compose execution policy, project name, env triggers
- [styles.yml](styles.md) — ASCII header, color palette, separator
- [info.yml](info.md) — info dashboard sections, template expressions
- [commands/](commands.md) — declarative commands: types, params, context, files, workflows, templates
- [validate.yml](validate.md) — project readiness checks: env probes, declarative checks, builtins, stages, preflight
- [snapshot.yml](snapshot.md) — snapshot workflows: create/restore/remove blocks, variants, `${snapshot.*}` namespace, manifest, lock interaction, archive safety
- [Localization (i18n)](i18n.md) — user command and UI string translations: locale resolution, file format, key reference, validation
- [Notifications](notifications.md) — user-level desktop notifications: config file locations, keys, gate matrix, environment overrides
- [UI](ui.md) — interactive command browser configuration: depth, collapse, badges, hotkeys, fallback ladder
- [Templates](../templates.md) — Go templates, `${...}` shorthand, sprout helpers (shared across info, commands, pipelines, render packs)

## Related commands

- `devbox render env` — generate `.env` from the merged config export rules
- `devbox render ide` — generate IDE configs
- `devbox render ai` — generate hub-level AGENTS.md and CLAUDE.md symlinks
- `devbox render git` — generate shell git hooks into `<svc.Dir>/src/.git/hooks/`
- `devbox info` — render the info dashboard from `info.yml`
- `devbox deploy plan` — show the resolved deploy pipeline
- `devbox compose files` — show active compose file list (diagnostic)
- `devbox status apps` — show app services with health and deploy status
- `devbox status tools` — show tool services table (read-only)
- `devbox status infra` — show infra services table (read-only)

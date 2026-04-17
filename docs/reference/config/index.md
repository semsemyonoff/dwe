# Config Reference

Overview of all configuration files in the devbox system.

## File inventory

| File | Tracked | Merged | Purpose |
|------|---------|--------|---------|
| `devbox.yml` | yes | layer 1 | Project identity and service structure |
| `devbox/defaults.yml` | yes | layer 2 | Versioned defaults: tools, runtime, ports, exports |
| `devbox/local.yml` | no (gitignored) | layer 3 | Per-user overrides: state, ports, tools |
| `devbox/services.yml` | yes | standalone | Service declarations with dirs, cli, configs |
| `devbox/deploy.yml` | yes | standalone | Orchestrator deploy pipeline (phases + steps) |
| `devbox/deploy/<svc>.yml` | yes | standalone | Per-service deploy pipelines |
| `devbox/reset.yml` | yes | standalone | Reset pipeline |
| `devbox/docker.yml` | yes | standalone | Compose execution policy |
| `devbox/docker.local.yml` | no (gitignored) | standalone | Local compose policy overrides |
| `devbox/styles.yml` | yes | standalone | ASCII header, color palette, separator |
| `devbox/info.yml` | yes | standalone | Info dashboard sections |

## Merged vs standalone

**Merged (3-layer config)**: `devbox.yml` → `devbox/defaults.yml` → `devbox/local.yml` are deep-merged at startup. Later layers win; maps merge recursively. The result is the effective config used for `.env` generation, topology resolution, and export rules.

**Standalone**: `services.yml`, `deploy.yml`, `reset.yml`, `docker.yml`, `styles.yml`, `info.yml` are loaded separately via dedicated loaders. They are not merged with the 3-layer config and have their own loading functions in `internal/config/`.

## Pages

- [Layering](layering.md) — merge order, precedence rules, dot-path resolution
- [devbox / defaults / local](devbox.md) — the 3-layer merged config
- [services.yml](services.md) — service declarations, extends, dirs, cli config
- [deploy.yml](deploy.md) — deploy and reset pipelines, steps, builtins
- [docker.yml](docker.md) — Compose execution policy, project name, env triggers
- [styles.yml](styles.md) — ASCII header, color palette, separator
- [info.yml](info.md) — info dashboard sections, template expressions

## Related commands

- `devbox render env` — generate `.env` from the merged config export rules
- `devbox render ide` — generate IDE configs
- `devbox info` — render the info dashboard from `info.yml`
- `devbox deploy plan` — show the resolved deploy pipeline
- `devbox compose files` — show active compose file list (diagnostic)
- `devbox services list` — list services with enabled/disabled status
- `devbox tools list` — list tools with enabled/disabled status

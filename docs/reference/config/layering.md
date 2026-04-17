# Config Layering

The 3-layer merge and how the CLI uses the effective config.

## Merge order

```
devbox.yml          (layer 1 — structural baseline)
  ↓ deep-merge
devbox/defaults.yml (layer 2 — versioned defaults)
  ↓ deep-merge
devbox/local.yml    (layer 3 — per-user overrides, gitignored)
  ↓
Effective config
```

Later layers win. For scalar values (strings, booleans, integers) the last layer that sets a key takes effect. For maps, keys are merged recursively — a later layer only needs to provide the keys it overrides, not the entire map.

## Precedence rules

- `devbox.yml` provides the minimum structural skeleton (project identity, service names).
- `devbox/defaults.yml` provides all runtime defaults — ports, hosts, tools, export rules. This is the file that changes with new devbox versions.
- `devbox/local.yml` is the user escape hatch. It is gitignored and never committed. Use it to enable a debug service, override a port, or switch state.

## What belongs in each layer

| Concern | Layer |
|---------|-------|
| Project name and prefix | `devbox.yml` |
| Service hub directories | `devbox.yml` (via services.yml) |
| Port defaults | `defaults.yml` |
| Host defaults | `defaults.yml` |
| Tool defaults (enabled/disabled) | `defaults.yml` |
| Export rules | `defaults.yml` |
| Compose overlay map | `defaults.yml` |
| IDE config defaults | `defaults.yml` |
| Active state | `local.yml` |
| Personal port overrides | `local.yml` |
| Enabling debug/optional services | `local.yml` |

## Dot-path resolution

Export rules reference values in the merged config via dot-paths. A dot-path is a `.`-separated key chain that navigates the merged YAML map.

Examples:
- `runtime.ports.app` → `80`
- `tools.adminer.enabled` → `false`
- `services.main.container` → `"app-main"`

Dot-paths are also used in `when:` conditions for export rules and `info.yml` template expressions.

## DevboxConfig.Raw

The CLI stores the merged raw map in `DevboxConfig.Raw`. Export rules, `when:` conditions, and template evaluations all operate against this raw map. Typed struct fields (e.g. `DevboxConfig.Runtime.Ports.App`) are populated from the same merged result.

## Local file bootstrapping

If `devbox/local.yml` does not exist the merge skips layer 3 silently — no error. Use `devbox/local.example.yml` as the starting template.

## Common pitfalls

- **Editing defaults.yml for personal settings** — changes to `defaults.yml` are committed and affect all team members. Personal overrides always go in `local.yml`.
- **Scalar collision** — if `defaults.yml` sets `state: ""` and `local.yml` sets `state: staging`, the effective value is `staging`. If `local.yml` omits `state`, the `defaults.yml` value wins.
- **Map merge vs replace** — maps merge; lists do not. If `defaults.yml` has `args.global: ["--ansi", "always"]` and `local.yml` has `args.global: ["--ansi", "never"]`, the local value replaces the entire list (lists are scalars in the merge algorithm).

## Related commands

- `devbox render env` — renders the effective config to `.env`
- `devbox compose argv` — shows the effective compose command with all flags (useful for debugging docker.yml)

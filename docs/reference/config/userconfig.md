# User config

User-level DWE preferences live outside any project, in a flat `key = value` file. They are read by every `dwe` invocation regardless of the current working directory and never tracked by git.

Settings cover preferences that are inherently personal — preferred language, mermaid theme for the docs TUI, desktop-notification gates, and overrides for the absolute paths of external binaries (`docker`, `git`, `dwe`, `shell`, `mmdc`). Nothing here belongs in a project's `workspace.yml`, `defaults.yml`, or `local.yml` — those carry project shape, not per-developer machine state.

## Contents

- [File locations](#file-locations)
- [Syntax](#syntax)
- [Keys](#keys)
  - [Binary overrides](#binary-overrides)
  - [Language](#language)
  - [Mermaid theme](#mermaid-theme)
  - [Notifications](#notifications)
- [Environment variables](#environment-variables)
- [Precedence](#precedence)
- [Sample config](#sample-config)

## File locations

Two files are read in this precedence order (lower → higher), then env vars on top:

1. **Global user config** at `~/.config/dwe/config` on every OS (Linux, macOS, Windows). One path everywhere — no platform-native location, no XDG fallback. Missing file is silently treated as empty. If DWE ever writes it, mode is `0600`.

2. **Per-project override** at `<project>/.dwe/config`. The `.dwe/` directory is already gitignored by DWE; this file is meant for a developer to pin overrides for a single project without touching the global file. Missing file is silently treated as empty.

3. **Environment variables** override both files.

A parser error in either file bubbles up as a warning (notifications get disabled for that run, locale and theme fall back to defaults). The operation itself is never blocked by a malformed user config.

## Syntax

Flat `key = value` lines:

- Full-line `#` comments only — inline `#` comments are a parse error (a `#` after a space or tab inside the value is rejected; a bare `#` without a preceding space is allowed for fragments like URLs).
- Blank lines are ignored.
- Keys use lowercase letters, digits, and underscores. **Dotted keys are rejected** — use `notify_telegram_token`, not `notify.telegram.token`.
- Booleans: `1` / `true` / `yes` are truthy; `0` / `false` / `no` are falsy.
- Lists: comma-separated, whitespace around items is trimmed.
- Unknown keys are warnings, not errors.

## Keys

### Binary overrides

Override the absolute path DWE uses when invoking an external binary. Useful when the tool lives in a non-standard location or you want to pin a specific version per project.

| Key | Default | Used for |
|---|---|---|
| `binary_docker` | `docker` | every Docker / Compose call |
| `binary_git` | `git` | git-aware operations (render git hooks, status probes) |
| `binary_dwe` | `dwe` | self-references emitted by DWE (e.g. tip lines, generated wrapper scripts) |
| `binary_shell` | `sh` | the shell used to evaluate `when:` predicates and embedded scripts |
| `binary_mmdc` | `mmdc` | mermaid diagram rendering in the `dwe docs` TUI |

The pattern `binary_<name> = <absolute path>` also feeds the runtime linter validators — any binary an `env/runtime` check looks up (`shellcheck`, `yamllint`, etc.) can be overridden the same way.

> Note: deploy and condition step `when:` predicates intentionally use hardcoded `sh` for portability and ignore `binary_shell`. The override applies to every other shell invocation.

Empty paths are rejected at parse time. Missing files / non-executable paths surface as diagnostics from `dwe validate` (severity `error`) with a hint pointing back at the override entry.

```
binary_docker = /opt/homebrew/bin/docker
binary_git    = /usr/local/bin/git
binary_mmdc   = /Users/me/.npm-global/bin/mmdc
```

### Language

| Key | Type | Default | Purpose |
|---|---|---|---|
| `language` | string | unset → `$LANG` → `en` | Preferred locale for translated strings |

Two-letter language code (`en`, `ru`, `de`, …). Controls the locale resolution used by user commands, UI strings, and `dwe docs`. See [Localization (i18n)](i18n.md) for the full resolution ladder and per-namespace fallback rules.

### Mermaid theme

| Key | Type | Default | Purpose |
|---|---|---|---|
| `mermaid_theme` | enum | `auto` | Theme used when rendering mermaid diagrams in the `dwe docs` TUI |

Valid values: `auto` (follow the terminal background), `dark`, `light`. Empty string also resolves to `auto`. Any other value is a parse error.

### Notifications

The notification-related keys (`notify_enabled`, `notify_run_enabled`, `notify_deploy_enabled`, `notify_commands_enabled`, `notify_channels`) live in this same file. They are documented in detail — together with the gate matrix, non-interactive detection, and the per-OS notification backends — in [Notifications](notifications.md).

## Environment variables

Each typed key has a matching `DWE_<UPPER_SNAKE>` env var that overrides whatever the files set:

| Env var | Overrides |
|---|---|
| `DWE_LANGUAGE` | `language` |
| `DWE_MERMAID_THEME` | `mermaid_theme` |
| `DWE_NOTIFY_ENABLED` | `notify_enabled` |
| `DWE_NOTIFY_RUN_ENABLED` | `notify_run_enabled` |
| `DWE_NOTIFY_DEPLOY_ENABLED` | `notify_deploy_enabled` |
| `DWE_NOTIFY_COMMANDS_ENABLED` | `notify_commands_enabled` |
| `DWE_NOTIFY_CHANNELS` | `notify_channels` |

Binary overrides have no env-var equivalent — set them in the config file.

## Precedence

```
embedded defaults
  → global ~/.config/dwe/config
    → per-project <project>/.dwe/config
      → environment variables
```

Later layers win. Maps merge per-key; list-valued keys (`notify_channels`) replace wholesale.

## Sample config

```
# ~/.config/dwe/config — same path on every OS

# Locale and TUI
language       = ru
mermaid_theme  = dark

# Binary overrides
binary_docker  = /opt/homebrew/bin/docker
binary_mmdc    = /Users/me/.npm-global/bin/mmdc

# Notifications: loud on deploy, quiet during inner-loop run
notify_enabled          = true
notify_deploy_enabled   = true
notify_run_enabled      = false
notify_commands_enabled = true
notify_channels         = native
```

A per-project file that pins only one knob differently:

```
# <project>/.dwe/config

notify_run_enabled = false
```

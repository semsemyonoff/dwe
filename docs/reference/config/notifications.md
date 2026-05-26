# Notifications

Native desktop notifications fired when long-running Devbox operations complete (success or failure). Notifications are a **user-level** concern — preferences live in a user config file outside the project (with an optional per-project override), not in `devbox.yml`.

## Contents

- [When notifications fire](#when-notifications-fire)
- [File locations](#file-locations)
- [Config syntax](#config-syntax)
- [Keys (MVP)](#keys-mvp)
- [Environment variables](#environment-variables)
- [Gate matrix](#gate-matrix)
- [Non-interactive detection](#non-interactive-detection)
- [Drop-on-busy policy](#drop-on-busy-policy)
- [Reserved keys (not yet wired)](#reserved-keys-not-yet-wired)
- [Sample config](#sample-config)
- [Title and body format](#title-and-body-format)
- [macOS icon and app name](#macos-icon-and-app-name)

## When notifications fire

| Operation | Fires? | Per-op gate |
|---|---|---|
| `devbox deploy` | yes | `notify_deploy_enabled` |
| `devbox run` | yes | `notify_run_enabled` |
| `devbox restart` | **no** (the inner `run` phase is suppressed via `SkipNotify`) | — |
| `devbox stop` | no | — |
| `devbox reset` | no | — |
| `devbox commands <id>` (top-level, with `notify: true` on the `CommandDef`) | yes | `notify_commands_enabled` |
| `devbox commands <id>` (without `notify: true`) | no | — |
| Workflow sub-step (sequential or parallel) | **no** — always suppressed at runtime regardless of its own `notify:` field | — |
| Deploy pipeline action invoking a command | **no** — same rule | — |
| Daemon commands (`.start` / `.logs` / `.stop` / `.restart`) | `notify: true` is rejected at validation time | — |

The rule: **the notification fires for the command you typed, not for any command it runs internally.** A command marked `notify: true` and invoked transitively (workflow sub-step or pipeline action) has its notification suppressed by a runtime `SkipNotify` guard.

The validator emits an info-level diagnostic when it can statically detect a `notify: true` command placed as a direct sub-step inside a `parallel:` block — purely as an early warning. The runtime suppression is the actual enforcement and covers transitive cases the validator cannot see.

### Per-invocation suppression with `--silent`

Every command that can fire a notification accepts a `--silent` flag that suppresses the desktop notification for that single invocation. Useful for scripted / CI runs where the user is not at the desk to see the popup.

The flag is available on: `devbox deploy run`, `devbox run`, `devbox snapshot create`, `devbox snapshot restore`, `devbox snapshot rollback`, `devbox snapshot remove`, and `devbox commands <id>`. It is a one-shot override — the user config and per-op gates are unchanged.

## File locations

Two files are read in this precedence order (lower → higher):

1. **Global user config** at `~/.config/devbox/config` on every OS (Linux, macOS, Windows). No platform-native location, no XDG fallback — one path everywhere. Missing file is silently treated as empty. If Devbox ever writes it, mode is `0600`.

2. **Per-project override** at `<project>/.devbox/config`. The `.devbox/` directory is already gitignored by Devbox. Missing file is silently treated as empty.

3. **Environment variables** override both files (highest precedence).

A parser error in either file bubbles up and disables notifications for that run (warning logged via `slog`); the operation itself is never blocked by the notification subsystem.

## Config syntax

Flat `key = value` lines:

- Full-line `#` comments only — inline `#` comments are a parse error.
- Blank lines are ignored.
- Keys use lowercase letters, digits, and underscores; **dotted keys are rejected** (`notify_telegram_token`, not `notify.telegram.token`).
- Booleans: `true` / `false`.
- Lists: comma-separated.
- Unknown keys are warnings (forward-compat with reserved keys).

## Keys (MVP)

| Key | Type | Default | Purpose |
|---|---|---|---|
| `notify_enabled` | bool | `true` | Master switch — when `false`, no notification fires |
| `notify_run_enabled` | bool | `true` | Gate for `devbox run` |
| `notify_deploy_enabled` | bool | `true` | Gate for `devbox deploy` |
| `notify_commands_enabled` | bool | `true` | Gate for user commands with `notify: true` |
| `notify_channels` | list | `native` | Comma-separated backend names; only `native` is wired in MVP |

## Environment variables

Each key has a matching `DEVBOX_<UPPER_SNAKE>` env var that overrides whatever the files set:

| Env var | Overrides |
|---|---|
| `DEVBOX_NOTIFY_ENABLED` | `notify_enabled` |
| `DEVBOX_NOTIFY_RUN_ENABLED` | `notify_run_enabled` |
| `DEVBOX_NOTIFY_DEPLOY_ENABLED` | `notify_deploy_enabled` |
| `DEVBOX_NOTIFY_COMMANDS_ENABLED` | `notify_commands_enabled` |
| `DEVBOX_NOTIFY_CHANNELS` | `notify_channels` |

Boolean env values: `1` / `true` / `yes` are truthy; `0` / `false` / `no` are falsy.

## Gate matrix

A notification fires only if **all** of the following are true:

1. `notify_enabled = true` (master switch).
2. The matching per-op key is `true` (`notify_deploy_enabled`, `notify_run_enabled`, or `notify_commands_enabled`).
3. `notify_channels` is non-empty and contains at least one known backend (`native` in MVP).
4. The environment is interactive (see next section).
5. For `devbox commands`: the `CommandDef` has `notify: true` **and** the command is the top-level invocation (`SkipNotify == false`).

Any miss → silent no-op.

## Non-interactive detection

Notifications are short-circuited when any of the following hold:

- `CI` environment variable is set to any non-empty value.
- `DEVBOX_NONINTERACTIVE` is set to a truthy value (`1` or `true`; case-insensitive).
- `stdin` is not attached to a terminal (stdout is intentionally not checked — piping output while keeping stdin interactive is exactly the scenario where a passive toast notification is most valuable).

This means CI runs, piped output, and scripted invocations never produce a desktop notification regardless of config.

## Drop-on-busy policy

The native backend is bounded to **one in-flight notification at a time** per CLI process. If a previous notification's OS notifier daemon stalls (rare, but observed on some Linux setups), subsequent notifications within that operation are silently dropped and logged at debug level. The backend applies an internal 2-second timeout; the calling operation is never delayed waiting for the notifier.

## Platform notes

**macOS** uses [`terminal-notifier`](https://github.com/julienXX/terminal-notifier) when it's on `PATH` (install via `brew install terminal-notifier`) and falls back to `osascript` otherwise. The Devbox logo is passed as `-contentImage` so it renders as a thumbnail inside the notification card — modern macOS pins the small app-icon slot to terminal-notifier's own bundle icon and silently ignores `-appIcon` overrides. The `osascript` fallback cannot carry a custom icon at all and shows Script Editor's icon instead.

If notifications stop appearing as banners on macOS despite `terminal-notifier -list Devbox` showing them as delivered, the macOS Notification Center daemon is stuck. Fix with:

```sh
killall NotificationCenter
```

**Linux** uses libnotify via dbus (or `notify-send` as a fallback); the icon comes through as the PNG payload directly.

**Windows** uses native toast notifications with the embedded PNG.

## Reserved keys (not yet wired)

The parser accepts these keys without error so that adding the corresponding backend in a future release does not require a config migration. They are decoded but **not used** in MVP:

- `notify_telegram_token`
- `notify_telegram_chat`
- `notify_webhook_urls` (list)

Setting `notify_channels = telegram` or `notify_channels = webhook` in MVP falls back to no-op (unknown channel logged at debug).

## Sample config

A common setup: notify on deploy and ad-hoc commands, but stay quiet for the inner-loop `devbox run` cycle.

```
# ~/.config/devbox/config  (same on every OS)

notify_enabled          = true
notify_deploy_enabled   = true
notify_run_enabled      = false   # silent during inner-loop dev
notify_commands_enabled = true
notify_channels         = native
```

To mute everything globally without touching per-op flags:

```
notify_enabled = false
```

To mute just for one project (per-project override at `<project>/.devbox/config`):

```
notify_run_enabled = false
```

## Title and body format

The native backend renders a fixed, branded format. The project name (when known) appears in the title; the body carries the timing and, on failure, a one-line truncated error message.

| Outcome | Title | Body |
|---|---|---|
| Success | `✓ Devbox · <project>: <op> succeeded` | `<duration>` |
| Failure | `✗ Devbox · <project>: <op> failed` | `<duration>` + (on a new line) truncated error message |

When the event has no associated project (rare — typically only synthetic test events), the `· <project>` segment is omitted and the title collapses to `✓ Devbox: <op> succeeded` / `✗ Devbox: <op> failed`.

Examples:

```
✓ Devbox · acme-api: deploy succeeded
1m 42s
```

```
✗ Devbox · acme-api: run failed
3.2s
exit status 1: migration aborted: relation "users" does not exist
```

Error messages are clipped to the first line and truncated to 200 runes (a trailing `…` indicates truncation). Duration formatting buckets: `<1s` → `Xms`, `<60s` → `X.Xs`, `<1h` → `Xm Ys`, `≥1h` → `Xh Ym`.

## macOS icon and app name

On macOS, the Devbox icon and the `Devbox` app name in the notification banner require [`terminal-notifier`](https://github.com/julienXX/terminal-notifier) to be installed:

```
brew install terminal-notifier
```

When `terminal-notifier` is present, beeep delegates to it and the embedded Devbox icon and `AppName = "Devbox"` are honored.

Without it, beeep falls back to AppleScript (`osascript`), which on recent macOS releases shows the sender as **Script Editor** and ignores the icon. Functionality is unaffected — only the visual presentation degrades. The title text (which already carries the `Devbox · <project>` prefix) remains correct in either path.

Linux (libnotify) and Windows (toast) honor the embedded icon and app name without any extra setup.

# Starship Integration

Use `dwe prompt` to render a compact, project-aware segment inside your [Starship](https://starship.rs/) prompt.

## Overview

`dwe prompt` prints a single line for the current shell's working directory:

```
{▪} my-project ✓ ●
```

Full output shape (each tail segment is independently optional):

```
{▪} <project> [<service>] <deploy-icon> <stack-icon>
```

- `{▪}` — the DWE logomark; the inner square is coloured with the project's `accent` token from `workspace/styles.yml`.
- `<project>` — `project.name` from `workspace.yml`, falling back to the directory basename.
- `[<service>]` — present when `cwd` is under a service's source directory. For each `workspace/services/<name>/`, the `dir:` field from `service.yml` is resolved relative to the project root and matched against `cwd` (deepest match wins on nested layouts; the `extends:` chain is followed when a child has no own `dir`). Services whose `dir:` resolves to the project root (`dir: .`) or outside it (`dir: ..`, absolute paths outside root) are silently skipped. The bracket frame is plain; the inner name is sanitized. One `os.ReadDir` plus one small YAML read per service per prompt invocation — sub-millisecond on typical projects; `service.yml` files larger than 64 KB are skipped on the hot path.
- `<deploy-icon>` — `✓`/`⟳`/`⚠`/`✗` reflecting the deploy-state journal at `.dwe/deploy/state.yml`. Omitted when no deploy state exists.
- `<stack-icon>` — `●`/`◐`/`○` reflecting live container state. Backed by `.dwe/prompt-cache.yml` (see [Stack icon](#stack-icon)). Omitted when neither cache nor refresh produce a value.

Only the `{▪} <project>` prefix is a stability guarantee — every other tail segment may be absent depending on project state.

When the shell is outside any DWE project, the command exits with code `1` and prints nothing — Starship hides the segment via its `when =` predicate.

`dwe prompt` is the hot path for shell prompts: it bypasses cobra, config validation, and lipgloss entirely. Cold-start budget is well under 50 ms on a modern machine.

## Installation

Add the following block to `~/.config/starship.toml`:

```toml
[custom.dwe]
command = "dwe prompt"
when    = "dwe prompt --check"
format  = "[$output]($style) "
style   = "bold"
description = "DWE project status"
```

The `when = "dwe prompt --check"` predicate is silent and exit-only: Starship runs it once per prompt to decide whether the segment is shown. The `command` form does the actual render and emits a coloured segment to stdout.

## Customisation

`dwe prompt` only colours the logomark and the icons — the braces, project name, service tag, and surrounding whitespace are plain. That leaves the rest free for Starship's `style` and `format` to re-style without fighting embedded ANSI:

```toml
[custom.dwe]
command = "dwe prompt"
when    = "dwe prompt --check"
format  = "via [$output]($style) "
style   = "dimmed cyan"
```

The colour escapes inside the segment use `\x1b[39m` (default foreground only) so they do not reset surrounding attributes.

## Deploy icon

Evaluated in this precedence order — first match wins:

| Order | Condition | Icon | Colour token |
| --- | --- | --- | --- |
| 1 | deploy `status: failed` | `✗` | `danger` |
| 2 | deploy `status: partial` | `⚠` | `warning` |
| 3 | pending changes (`pending` block present) | `⟳` | `warning` |
| 4 | deploy `status: deployed` | `✓` | `success` |
| 5 | no state / `not_deployed` / parse error | _(omitted)_ | — |

Failed and partial outrank pending so the prompt surfaces broken state first — you need to know things are wrong *before* thinking about applying pending changes.

## Stack icon

The stack icon reflects live Docker container state. It is independent of the deploy icon — a project can show `✓ ○` (deploy succeeded, containers manually stopped).

| State | Glyph | Colour token | Meaning |
| --- | --- | --- | --- |
| running | `●` | `success` | all expected containers up |
| partial | `◐` | `warning` | some containers up, some down |
| stopped | `○` | `muted` (prompt fallback `#6B7280`; overridable via `workspace/styles.yml` `colors.muted`) | no containers running |
| _(none)_ | — | — | no cache and refresh produced no usable value |

### Cache

State is read from a stale-while-revalidate cache at `.dwe/prompt-cache.yml`:

```yaml
updated_at: 2026-06-03T12:34:56Z   # RFC3339 UTC
state: running                      # running | partial | stopped
```

- **TTL**: 2 minutes. Fresh-cache reads are pure file I/O — no `docker ps`.
- **Stale or missing**: prompt shells out to `docker ps -q --filter label=com.docker.compose.project=<project>` with a **150 ms hard timeout**. The probe applies `process_env` overrides from `workspace/docker.yml` / `docker.local.yml` (e.g. `DOCKER_HOST`, `DOCKER_CONTEXT`), so it targets the same daemon as lifecycle commands. On timeout or any error, no cache write occurs.
- **Stale trust cap**: 10 minutes (5× TTL). A stale cached value survives a confirmed zero-result refresh only within the cap; past it the prompt renders `stopped` (still without writing the cache), so a stack stopped outside dwe converges to `○` instead of showing `●` indefinitely. An unconfirmed zero (docker error / timeout) never downgrades, at any age.
- **Atomic writes**: tmp-file + rename in the same directory, so concurrent prompts cannot corrupt the file.

### Writer map

Different sites know different things about the stack. Each site picks the safest action — write when confident, invalidate when scoped, never lie:

| Site | Action |
| --- | --- |
| `dwe run` | write `running` |
| `dwe restart` (no service arg) | write `running` |
| `dwe restart <service>` | invalidate (remove cache file) |
| `dwe stop` (no `--service`) | write `stopped` |
| `dwe stop <service>` | invalidate |
| `dwe deploy run` (no `--service`) | invalidate (deploy can no-op via "already up-to-date") |
| `dwe deploy run --service <n>` | invalidate |
| `dwe reset run` (project-wide teardown) | write `stopped` |
| `dwe reset run --service <n>` | invalidate |
| `dwe services enable/disable --apply` | write `running` (after the entire toggle plan including after-hooks completes) |
| `dwe status` (top-level) | write the exact aggregated `Health` (`running`/`partial`/`stopped`) |
| `dwe status <subcommand>` | no write (section-scoped) |
| `dwe snapshot restore` / `rollback` | invalidate (post-restore state is arbitrary) |
| `dwe prompt` (sync refresh) | write `running` if `docker ps` count > 0 — **never `stopped`** |

### No-downgrade rule on prompt refresh

`dwe prompt`'s own sync refresh only ever writes `running`. A zero-result from `docker ps` is indistinguishable between:

- a genuinely stopped stack, and
- a wrong label filter (templated `docker.yml.project_name` is not loaded by the prompt — see [Known limitations](#known-limitations)).

Allowing prompt refresh to write `stopped` would either (a) downgrade a correct `running` left by an authoritative writer (lifecycle / status), or (b) write a wrong `stopped` to an absent cache after invalidation. Reserving `stopped` writes for authoritative writers keeps the cache honest.

The rule bounds *writes*, not *rendering*: once the cached value is older than the 10-minute stale trust cap, a confirmed zero-result refresh renders the icon as `stopped` for that prompt (the cache file is still not touched). Within the cap the stale value wins, which keeps a wrong-label zero from flickering a healthy stack's icon.

Cache writes are best-effort everywhere: neither prompt refresh nor lifecycle commands fail when cache I/O fails. The cache is observability, not correctness.

## Behaviour in non-color terminals

`dwe prompt` follows the [NO_COLOR](https://no-color.org/) spec: if the `NO_COLOR` environment variable is set to *any* value (including the empty string), all ANSI escapes are suppressed and the output is plain runes only.

```
{▪} my-project ✓ ●
```

## Known limitations

- **Light/dark auto-detect**: the prompt always uses the dark variant of the palette. Most terminals are dark; light-terminal support can be added later via `COLORFGBG` if there is demand.
- **No `-c` flag**: `dwe prompt` always walks up from `$PWD`. This is intentional — the shell prompt reflects the shell's current directory, not an arbitrary project pointer.
- **Custom docker binary**: setting `binary_docker = podman` (or an absolute path) in `~/.config/dwe/config` (or any non-`docker` value) bypasses prompt-driven refresh — `shared/prompt` hardcodes the `docker` binary name on the hot path. `process_env` overrides (`DOCKER_HOST`, `DOCKER_CONTEXT`, …) *are* applied to the probe, so multi-daemon setups work; only the binary name is fixed. Lifecycle commands and `dwe status` still write the cache with the correct binary, so the icon remains accurate during active use; only the 2-minute idle refresh is a no-op.
- **Templated compose project name**: projects whose `workspace/docker.yml` sets `project_name` to a template (e.g. `${project.prefix}_${project.name}`) will see `docker ps` return zero rows from prompt refresh — `shared/prompt` reads only a *literal* `project_name` and falls back to `prefix-name` for template values. Combined with the no-downgrade rule, prompt refresh writes nothing — the icon stays correct as long as lifecycle commands and `dwe status` (which use the real compose name) keep the cache fresher than the 10-minute trust cap. Beyond the cap the persistent zero-result renders `○` even for a running stack until the next authoritative write; if that bites, set a literal `project_name` (e.g. in `workspace/docker.local.yml`).
- **Manual `docker stop` outside dwe**: the cache is not rewritten by prompt refresh (no-downgrade rule), so the icon keeps showing the stale state for up to the 10-minute trust cap; after that a confirmed zero-result renders `○` on each prompt. Run `dwe status` to refresh the cached state immediately.
- **Services without `dir:`**: tool/infra services (and any app without a source mount) never appear as `[<service>]` in the prompt — they have no source directory for `cwd` to be under. The prompt still renders the project, deploy, and stack segments normally.
- **Symlinked paths**: `dwe prompt` does not call `filepath.EvalSymlinks` on either `cwd` or the resolved `dir:`. If `cwd` was reached through a symlink while `dir:` points at the canonical path (or vice versa), the service tag silently disappears. Use real paths in `service.yml`'s `dir:` to avoid surprises.
- **Shell-specific quoting**: sh, bash, and zsh accept the `command` / `when` strings as written. Fish users may need to adjust quoting in `starship.toml` if their Starship config wraps commands differently.

## Before / after

Without the segment:

```
~/code/my-project ❯
```

With the segment (project root):

```
{▪} my-project ✓ ● ~/code/my-project ❯
```

With the segment (inside the `api` service's source dir — assuming `workspace/services/api/service.yml` has `dir: ./services/api`):

```
{▪} my-project [api] ✓ ● ~/code/my-project/services/api ❯
```

(The DWE logomark and icons are coloured in real terminals.)

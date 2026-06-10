# Host bridge

The host bridge lets `dwe` commands run from **inside dev containers** — git hooks (`exec dwe commands lint`), project commands, and read-only diagnostics work identically on the host and in a container terminal (VS Code Dev Containers and similar). DWE mounts a small static shim binary into bridge-enabled service containers as `dwe`; the shim forwards every invocation to a host-side daemon, which runs the real `dwe` on the host and streams output and the exit code back.

For `type: app` services the bridge is on by default — a fresh project needs no configuration. Everything below is reference detail: the `bridge:` schema, transports, the in-container command policy, the generated compose overlay, daemon lifecycle, and troubleshooting.

## Contents

- [How it works](#how-it-works)
- [Enabling the bridge: `bridge:` in service.yml](#enabling-the-bridge-bridge-in-serviceyml)
- [Transports](#transports)
- [Command policy inside containers](#command-policy-inside-containers)
- [The generated compose overlay](#the-generated-compose-overlay)
- [`.dwe/bridge/` contents](#dwebridge-contents)
- [Daemon lifecycle](#daemon-lifecycle)
- [`dwe bridge` subcommands](#dwe-bridge-subcommands)
- [Environment contract](#environment-contract)
- [Limitations](#limitations)
- [Troubleshooting](#troubleshooting)
- [See also](#see-also)

## How it works

```
git commit (in container)
  └─> .git/hooks/pre-commit
       └─> exec dwe commands lint        ← the shim, not the real dwe
            └─> unix socket → TCP fallback to the host daemon
                 └─> daemon runs `dwe commands lint` on the host
                      ├─> stdin / stdout / stderr streamed
                      ├─> SIGINT / SIGTERM forwarded
                      └─> real exit code returned
```

- The **shim** is a ~3 MB static Linux binary (amd64 + arm64 builds are embedded in the host `dwe` and materialized into `.dwe/bridge/`). It carries no business logic: it picks a transport, sends the command, pumps stdio, and exits with the host-side exit code.
- The **daemon** (`dwe bridge daemon`, hidden) is a stateless forwarder: one connection per command, fork `dwe <argv>` per connection, no project model, no cache. It is started and stopped automatically by lifecycle commands.
- The shim's working directory is translated from the container path to the host path (see [Environment contract](#environment-contract)), so relative paths in hooks and project commands just work.

## Enabling the bridge: `bridge:` in service.yml

Per-service opt-in in `workspace/services/<name>/service.yml`:

```yaml
type: app
dir: ./services/main
dir_internal: /var/www/html
bridge:
  enabled: true                    # default: true for type: app, false otherwise
  # shim_path: /usr/local/bin/dwe  # mount point override (base-image collision)
  # on_unreachable: fail           # fail | warn — shim policy when the daemon is down
```

| Field | Default | Meaning |
|---|---|---|
| `enabled` | `true` for `type: app`, `false` for `tool` / `infra` | inject the shim binary and bridge mounts into this service's container |
| `shim_path` | `/usr/local/bin/dwe` | absolute container path the shim is mounted at; override when the base image already ships a file there |
| `on_unreachable` | `fail` | `fail` — shim prints an error and exits 1 when the host daemon is unreachable (a hook blocks the commit); `warn` — print a warning and exit 0 |

`bridge.enabled` is a tristate and inherits through service [`extends:`](config/services/extends.md) the same way `render.git.enabled` does: an explicit value in the child wins, an unset child inherits the parent, and the type-based default applies only when neither sets it. `shim_path` and `on_unreachable` inherit when the child leaves them empty.

A bridge-enabled service should declare the `dir` / `dir_internal` pair — it is what the shim's working-directory translation maps over. Without it the bridge mounts fine, but the daemon rejects every in-container invocation with a containment error (`dwe validate` warns about this).

## Transports

The shim and the daemon both always support two transports; there is no platform detection on either side.

| Platform | Working transport | Auth |
|---|---|---|
| Linux (native Docker) | unix socket `.dwe/bridge/host.sock` | peer uid must equal the daemon's uid (`SO_PEERCRED`) |
| Linux rootless | unix socket (`host-gateway` is broken there) | peer uid |
| Docker Desktop (macOS / Windows) | TCP `host.docker.internal:<port>` (Desktop proxies to the loopback listener) | 256-bit per-project token |
| OrbStack / Colima | TCP | token |
| WSL2 (dwe inside the distro) | TCP | token |

Selection, per invocation:

1. If `host.sock` exists in the mounted bridge dir, dial it with a 300 ms timeout. On Docker Desktop and similar runtimes the socket inode is visible but dead (file sharing does not forward `connect()` across the VM boundary), so this refuses instantly — by design.
2. Fall back to TCP: read the `port` and `token` files from the bridge dir (retried briefly — a daemon restart can leave a short window) and connect to `host.docker.internal:<port>`, presenting the token.
3. Both failed → the [unreachable policy](#troubleshooting) applies.

The daemon always listens on the unix socket and on `127.0.0.1` with an ephemeral OS-assigned port (no port collisions, no LAN exposure — never `0.0.0.0`). On native Linux it additionally binds the docker bridge gateway IP (usually `172.17.0.1`) so containers can reach it; the generated overlay adds `extra_hosts: host.docker.internal:host-gateway` to every bridged service, which is required on Linux and harmless elsewhere. Exotic setups can override the listen addresses with the `DWE_BRIDGE_BIND` environment variable (a comma- or whitespace-separated address list) when starting the daemon. Wildcard addresses (`0.0.0.0`, `::`) are rejected from the override — binding all interfaces would break the no-LAN-exposure guarantee, so such an entry is ignored with a warning and the daemon falls back to the loopback default.

Isolation is per project: each project has its own socket, port, and token under its own `.dwe/bridge/`; a container of one project connecting to another project's port fails authentication, and the daemon additionally rejects working directories outside its own project root.

## Command policy inside containers

The container command surface is deliberately reduced — **allowlist, default-deny**. Two motives: `dwe stop` from inside a container would stop the caller's own container (the terminal/IDE session dies before the result arrives), and a token in a compromised container must not be able to destroy data.

| Allowed from a container | Why |
|---|---|
| `dwe commands <cmd>` / `dwe cmd <cmd>` | the primary case — hooks and project commands |
| bare `dwe commands` / `dwe cmd` | no TTY over the bridge → prints the `commands list` output instead of the interactive browser |
| `status`, `info`, `validate`, `logs` | read-only diagnostics |
| `docs` (including `llms-txt`) | read-only; useful to AI agents working in the devcontainer |
| `prompt` | container terminal prompt segment |
| `bridge status` | bridge self-diagnostics |
| `version`, help, completion | service commands |

| Blocked | Why |
|---|---|
| `stop`, `restart`, `reset` | suicidal: they stop / recreate the container they were invoked from |
| `deploy`, `run`, `services` | the stack is managed from the host |
| `snapshot` | restore stops the stack; create is heavy and takes project locks |
| `render`, `setup`, `init`, `shell` | mutate workspace files or are interactive |
| `bridge` (everything except `status`) | `bridge stop` is suicide for the bridge itself |

Mechanics worth knowing:

- A blocked command fails with a `bridge_command_blocked` error and a run-on-host hint; the suicidal lifecycle commands additionally explain *why* (e.g. "it would stop the container it was invoked from").
- Blocked commands are **invisible** in `--help` listings and shell completion inside containers, not "visible but failing".
- The policy is inherited by nested invocations: a `type: dwe` user command spawns a child `dwe` with the same environment, so a hook hiding `dwe stop` inside a project command cannot kill the container.
- Every bridged command runs non-interactively (the same degradation as piping output on the host: plain progress output, no colors, no prompts). Interactive commands are blocked outright; the bridge never allocates a pseudo-terminal.

## The generated compose overlay

`.dwe/compose.bridge.yml` is **dwe-owned machine state** — the same ownership model as the generated `.env` and rendered service configs. Every command that performs `compose up` (`dwe deploy run`, `dwe run`, `dwe services … --apply`, whole-stack `restart`) regenerates it atomically before starting the stack; when no enabled service has the bridge enabled, the file is deleted instead. Manual edits are unsupported and overwritten — the file says so in its header.

```yaml
# GENERATED by dwe — do not edit; customize via workspace/local.yml
services:
  app-main:
    volumes:
      - type: bind
        source: /Users/foo/projects/my-proj/.dwe/bridge
        target: /dwe-bridge
        read_only: true
      - type: bind
        source: /Users/foo/projects/my-proj/.dwe/bridge/shim-linux-arm64
        target: /usr/local/bin/dwe
        read_only: true
    environment:
      DWE_BRIDGE_DIR: /dwe-bridge
      DWE_HOST_WORKSPACE: /Users/foo/projects/my-proj/services/main
      DWE_CONTAINER_WORKSPACE: /var/www/html
      DWE_BRIDGE_PROJECT: my-proj
    extra_hosts:
      - host.docker.internal:host-gateway
```

- **Chain position**: the overlay sits after the project's own compose files and **before** the `workspace/local.yml` overlays — `local.yml` keeps the last word over anything the bridge sets (compose later-file-wins), so per-developer customization goes through [`local.yml`](config/workspace.md), never through editing the generated file.
- **Self-healing**: the file always contains exactly the currently enabled and bridge-enabled services, so toggling a service off can never leave a stale fragment that breaks `compose up`. Moved project directories, changed image architectures, and edited `bridge:` settings are all picked up at the next start.
- **Shim architecture** is chosen per service at each regeneration from the image's reported architecture (`docker inspect`); when that cannot be resolved (nothing deployed yet), the host architecture is used with a warning and self-heals on the next regeneration.
- The TCP port and the token are deliberately **not** in the overlay — the shim reads them from the mounted `/dwe-bridge` files, so a daemon restart never requires regenerating the overlay or recreating containers.
- All mounts are read-only; connecting to a unix socket works on a read-only bind mount (the same mechanism that makes `docker.sock:ro` work).

## `.dwe/bridge/` contents

The bridge runtime directory on the host (`.dwe/` is gitignored):

| File | Written by | Purpose |
|---|---|---|
| `host.sock` | daemon, at startup | unix-socket transport (native Linux) |
| `port` | daemon, after bind | actual ephemeral TCP port |
| `token` | daemon, first start (mode 0600) | 256-bit auth token; stable project identity, survives daemon restarts |
| `daemon.pid` | daemon | PID + flock holder — the liveness probe |
| `daemon.log` | daemon | daemon stderr (append-only; no rotation) |
| `shim-linux-amd64`, `shim-linux-arm64` | host dwe, at each prepare | materialized shim binaries (bind-mount sources) |

The overlay itself lives one level up, at `.dwe/compose.bridge.yml`.

## Daemon lifecycle

The daemon is managed automatically — every lifecycle command that asserts "the stack is alive" ensures it first:

| Command | Daemon action |
|---|---|
| `dwe deploy run` | cycle (stop → start) — guarantees the daemon is not from an older dwe build |
| `dwe run` | ensure (start if not running) |
| `dwe restart` (whole stack) | cycle |
| `dwe services <name> --apply` | ensure |
| `dwe status` (top-level) | best-effort ensure |
| `dwe bridge start` | ensure (manual) |
| `dwe stop` / `dwe reset run` (whole stack) | stop |
| `dwe stop <svc>` / `reset run --service <svc>` / `restart <svc>` | untouched |

The daemon also **auto-stops** when the stack is actually down: it watches docker events for the project's containers (with a 60 s poll fallback) and exits once zero remain, removing `host.sock` and `port` but keeping `token`. There is deliberately no idle timeout — a dead daemon cannot be revived from inside a container, so killing it under an active developer would brick their hooks.

When a project has no bridge-enabled service, no daemon is ever started and no overlay is generated — projects without `bridge:` configuration behave exactly as before the bridge existed.

## `dwe bridge` subcommands

Manual control and diagnostics; lifecycle commands make these unnecessary day-to-day. All support `--output json`.

| Command | Behavior |
|---|---|
| `dwe bridge start` | start the daemon (idempotent); refuses with `bridge_not_enabled` when no enabled service has the bridge enabled |
| `dwe bridge stop` | SIGTERM the daemon by pidfile (idempotent); in-container dwe stops working until it is started again |
| `dwe bridge status` | liveness (pid, uptime), transport endpoints (socket path, TCP port), shim materialization state; the only `bridge` subcommand allowed from inside containers |
| `dwe bridge logs [--tail N]` | show `.dwe/bridge/daemon.log` (default last 50 lines, `--tail 0` = all) |

## Environment contract

Inside a bridged container the overlay sets:

| Variable | Meaning |
|---|---|
| `DWE_BRIDGE_DIR` | in-container mount point of the host's `.dwe/bridge` (`/dwe-bridge`) |
| `DWE_HOST_WORKSPACE` / `DWE_CONTAINER_WORKSPACE` | the service's host hub dir (`<root>/<dir>`) and its in-container mount (`dir_internal`) — the prefix pair the shim rewrites working directories with |
| `DWE_BRIDGE_PROJECT` | project name, used in shim diagnostics |
| `DWE_BRIDGE_UNREACHABLE` | only present with `on_unreachable: warn` |

The shim strips these (plus any `DWE_PROJECT_ROOT*`) from the environment it forwards, and the daemon re-filters the same set on arrival. The daemon also drops execution-hijacking variables before forking — the dynamic-loader families (`LD_*`, `DYLD_*`), shell-startup hooks (`BASH_ENV`, `ENV`, `SHELLOPTS`, `BASHOPTS`), `IFS`, and `PATH` — and force-sets `PATH` to the host daemon's own value, so a container can never redirect the `docker`/`git`/`sh` binaries the host-side `dwe` invokes by bare name. It then force-sets two host-controlled variables for the forked `dwe`: `DWE_INVOKED_FROM=container` (activates the command policy — client-sent values are discarded, so it cannot be spoofed from the container) and `DWE_NONINTERACTIVE=1`. `--output json` payloads are identical in both contexts.

The command's argument vector is passed through untranslated — only the working directory is rewritten. Relative paths therefore work everywhere; absolute container paths in arguments will not resolve on the host (a documented limitation).

## Limitations

- **No interactive commands.** The bridge never allocates a pseudo-terminal; the policy blocks interactive commands, and everything else runs in the same non-interactive mode as CI.
- **Absolute container paths in arguments** are not translated — use relative paths (the working directory is translated for you).
- **Services without a `dir` / `dir_internal` pair** get no working-directory translation; the daemon then rejects in-container invocations with a containment error. `dwe validate` flags this.
- **Windows containers** and a Windows host-side dwe are out of scope; WSL2 with dwe installed in the distro works as the Linux case.

## Troubleshooting

**"host daemon is not running for project …"** — the host stack is down or the daemon was stopped manually. Run `dwe run` (or `dwe bridge start`) on the host. With the default `on_unreachable: fail` the shim exits 1 so hooks block; set `on_unreachable: warn` on the service to turn this into a warning + exit 0 (e.g. for advisory hooks).

**"shim outdated, re-run `dwe deploy` to refresh shim binaries"** — the materialized shim and the host dwe speak different protocol versions (typically after upgrading dwe while the stack runs). `dwe deploy run` refreshes the shims and cycles the daemon even when the deploy itself has nothing to do.

**Bridged command hangs or fails only on native Linux** — UFW/firewalld may drop traffic arriving from the docker bridge network onto the gateway IP. The unix-socket transport is unaffected; if you need TCP, allow input from the docker bridge interface.

**Rootless Docker on Linux** — `host-gateway` is broken there, so TCP is unavailable; the unix-socket path covers rootless setups. Note that user-namespace remapping can shift the peer uid the daemon sees; if peercred auth fails, run the stack non-rootless or consult the project's issue tracker for the token-on-unix fallback status.

**The base image already has `/usr/local/bin/dwe`** — set `bridge.shim_path` to a different absolute path that wins in the container's `PATH`.

**Where to look** — `dwe bridge status` (daemon liveness, transports, shim state), `dwe bridge logs` (daemon stderr, including startup panics), and `dwe validate` (the `bridge` domain checks `on_unreachable` values, `shim_path` absoluteness, and the workspace mapping; `dwe validate bridge` scopes to it).

## See also

- [Services configuration](config/services/index.md) — `service.yml` fields, `extends:` inheritance
- [Workspace & local overlays](config/workspace.md) — `workspace/local.yml`, compose file chain
- [User commands](config/commands/index.md) — the `dwe commands` system that hooks call through the bridge
- [Validate](config/validate.md) — project readiness checks; the bridge validation domain

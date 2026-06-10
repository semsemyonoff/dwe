# Host Bridge: dwe from inside dev containers

## Overview

Implement the DWE Host Bridge: a host-side daemon plus a ~2 MB static shim
binary mounted into containers as `dwe`, so that git hooks
(`exec dwe commands lint`) and project commands work identically from the host
and from inside dev containers (VS Code Dev Containers etc.).

Today `dwe` is host-only: commits made inside a dev container run git hooks
inside the container, where `dwe` does not exist, so a rendered hook fails.
Goal: a single hook template working identically in both contexts, without
duplicating logic in templates and without losing the dwe pipeline engine.
The use case is not hooks-only — `dwe status`, `dwe info`,
`dwe commands <cmd>` from the IDE container terminal must work too.

**This plan is self-contained.** The design was worked out in a prior session
(empirically verified where it matters); everything needed for implementation
is captured in § Design Reference below. There is no separate spec file.

Summary of key decisions:

- **Dual transport**: unix socket `.dwe/bridge/host.sock` (native Linux,
  peercred auth) + TCP `host.docker.internal:<ephemeral port>` with a 256-bit
  token (Docker Desktop / OrbStack / Colima / WSL2). Shim tries unix (300 ms)
  then TCP. Same frame protocol on both.
- **Stateless daemon**: pure forwarder, forks `dwe <argv>` per connection; no
  business logic, no project model.
- **Command policy**: allowlist, default-deny, enforced inside dwe via
  daemon-controlled `DWE_INVOKED_FROM=container`.
- **No pty, ever**: policy blocks all interactive commands; shim always sends
  `tty: false`; daemon injects `DWE_NONINTERACTIVE=1`.
- **Compose overlay** `.dwe/compose.bridge.yml`: dwe-owned machine state,
  regenerated (atomic, write-if-changed) by every command that runs
  `compose up` — same model as `.env` and rendered service configs; always
  contains exactly the currently enabled bridge-enabled services, so service
  toggles can never desync it; user customization goes through
  `workspace/local.yml`, which sits AFTER the bridge overlay in the `-f`
  chain; read-only mounts.
- **Daemon lifecycle**: ensure before `compose up`, SIGTERM on whole-stack
  stop/reset, cycle on deploy/restart, best-effort ensure in `dwe status`;
  auto-stop when zero labeled containers remain.

## Context (from discovery)

- Compose `-f` chain: `composeFiles()` in
  `internal/core/project/config/workspace.go` — append point after local
  overlays.
- Service config inheritance: `ResolveServiceExtends()` (same file) — mirror
  the `render.git.enabled` tristate `*bool` pattern (around line 2042) for
  `bridge.enabled`.
- Strict YAML decode allowlists (BOTH must change): `allowedFieldsFor()` in
  `internal/core/project/config/workspace.go` and `servicesAllowedFields` in
  `internal/core/validate/config/workspace.go`.
- `internal/shared/daemon/` is TAKEN (helper for `type: daemon` user commands,
  not process management) — new packages are `internal/shared/bridgeproto`,
  `internal/shared/bridgeclient`, `internal/core/bridge`, `internal/cli/bridge`.
- Minimal-binary precedent: `internal/shared/prompt` (no cobra/lipgloss, early
  dispatch in `cmd/dwe/main.go`).
- flock precedent: `internal/shared/lock` (never reuse `AcquireProjectLocks`
  for the bridge pidfile — it is a separate, bridge-private lock).
- Generated-embed precedent: `scripts/sync-embedded-docs.sh` + gitignored
  embedded tree + make-target dependency — mirror for shim binaries.
- Build: `Makefile` (`build` → tidy, embedded-docs, gen-docs-manifest) and
  `.goreleaser.yaml` before-hooks both need the shim build step.
- CLI cross-cutting contracts (CLAUDE.md): JSON output via `cmdctx.WriteData` /
  typed `cmdctx.Err`; cobra does NOT chain child `PersistentPreRunE`; service
  iteration via `config.DeployOrder` (never `range cfg.Services`).
- `os.Executable()` test-recursion hazard precedent:
  `internal/cli/lifecycle/testhelpers_test.go:27`.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task — unit tests for new/modified functions, success and error paths
- **CRITICAL: all tests must pass before starting next task** — run via
  `make test` (NOT bare `go test ./...` — embedded assets are generated)
- **CRITICAL: update this plan file when scope changes during implementation**
- run `gofmt`/`goimports`; `make lint` must stay clean
- maintain backward compatibility: projects without `bridge:` config must
  behave exactly as today (no overlay, no daemon)

## Testing Strategy

- **unit tests**: required for every task; table-driven where it fits the repo
  style; fixtures in package-local `testdata/`
- **protocol/daemon tests run without Docker**: bridgeclient ↔ daemon over
  loopback TCP and tmpdir unix sockets (the main testing win of the dual
  transport); docker interactions (`events`, `inspect`, `ps`) go through
  injectable runners so they can be faked
- **e2e**: no UI e2e framework in this project; final manual smoke on Docker
  Desktop is listed in Post-Completion
- linux-only code (peercred) behind build tags with linux CI coverage

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

| Component | Package | Role |
|---|---|---|
| Wire protocol | `internal/shared/bridgeproto` (leaf) | frames, HELLO/ERROR, token, peercred |
| Shim client core | `internal/shared/bridgeclient` (leaf) | transport select, pump, unreachable policy |
| Shim binary | `cmd/dwe-shim` | thin main over bridgeclient |
| Daemon + composegen | `internal/core/bridge` | listeners, auth, subprocess proxy, ensure, auto-stop, overlay generation, shim materialization |
| CLI subtree | `internal/cli/bridge` | `bridge start/stop/status/logs` + hidden `bridge daemon` |
| Command policy | `internal/cli` composition root | default-deny allowlist on `DWE_INVOKED_FROM=container` |

Execution flow of one `dwe commands lint` from a container:

```
git commit (in container)
  └─> .git/hooks/pre-commit
       └─> exec dwe commands lint           ← shim, not real dwe
            └─> transport select (unix → tcp, § Design Reference)
                 └─> send HELLO {protocol_version, token?, argv, env (cleaned),
                                 cwd (translated), tty:false, term, winsize}
                      ├─> STDIN frames →
                      ├─> ← STDOUT/STDERR frames
                      ├─> SIGNAL frames (SIGINT/SIGTERM) →
                      └─> ← EXIT frame {code: int32}
       └─> exit 0 / non-zero
```

---

## Design Reference

Authoritative design detail. Implementation must match this section; update it
if a decision changes during implementation.

### D1. Verified platform facts (do not re-litigate)

1. **Host unix socket via bind-mount does NOT work on any desktop runtime.**
   File sharing (osxfs → gRPC-FUSE → VirtioFS) transfers files but does not
   forward AF_UNIX `connect()` across the VM boundary. Docker closed this as
   Won't Fix ([docker/for-mac#483](https://github.com/docker/for-mac/issues/483),
   2021). OrbStack: same position
   ([orbstack#62](https://github.com/orbstack/orbstack/issues/62), open).
   Colima/Lima: same ([colima#997](https://github.com/abiosoft/colima/issues/997)).
   WSL2: no Windows↔WSL2 AF_UNIX interop
   ([WSL#5961](https://github.com/microsoft/WSL/issues/5961)).
   **Empirically verified** (Docker Desktop, engine 29.5.3, macOS, 2026-06):
   socket inode visible in the bind mount (`S_ISSOCK=true`), `connect()` →
   instant `ECONNREFUSED`. The "magic" sockets (`/var/run/docker.sock`,
   `/run/host-services/ssh-auth.sock`) are VM-side special cases, not host
   files.
2. **Native Linux works** (same kernel). Caveat: userns-remap / rootless
   shift the peer uid seen by the daemon.
3. **TCP via `host.docker.internal` works on Docker Desktop even with the
   listener bound to `127.0.0.1` only** (Desktop proxies from the host side —
   empirically verified on the same machine). On native Linux it requires
   `extra_hosts: ["host.docker.internal:host-gateway"]` (Engine 20.10+) and
   `host-gateway` resolves to the docker bridge gateway IP (default
   `172.17.0.1`) — a loopback-only listener is NOT reachable from containers;
   the daemon must also bind the gateway IP. Rootless Linux: `host-gateway`
   is broken ([moby#47684](https://github.com/moby/moby/issues/47684)) — the
   unix path covers rootless.
4. **Read-only bind mounts do not block unix `connect()`**: the kernel
   exempts sockets/FIFOs from the EROFS check (only REG/DIR/LNK get EROFS) —
   the well-known working `docker.sock:ro` is the precedent. Hence
   `.dwe/bridge` mounts read-only.
5. **"Container runs as host uid" is NOT required.** On Mac, ownership across
   the VM is virtualized (files appear owned by the container user;
   devcontainer images bake uid 1000). On Linux, VS Code's
   `updateRemoteUserUID` already syncs uids. Peercred is used only on the
   unix path; TCP uses the token.

### D2. Transport selection (shim algorithm)

```
1. $DWE_BRIDGE_DIR/host.sock exists?
   → unix dial, timeout 300 ms
     success → proceed (auth: peercred)          ← native Linux
     refused/timeout → step 2                    ← Desktop: instant ECONNREFUSED
2. read $DWE_BRIDGE_DIR/port + $DWE_BRIDGE_DIR/token
   (retry read 2×100 ms if a file is momentarily absent — daemon restart)
   → tcp connect host.docker.internal:<port>, token inside HELLO
     success → proceed (auth: token)             ← Desktop / OrbStack / Colima / WSL2
     failure → unreachable policy (D10)
```

| Platform | Working transport | Auth |
|---|---|---|
| Linux (native Docker) | unix socket | SO_PEERCRED uid == daemon uid |
| Linux rootless | unix socket (host-gateway broken) | peercred (see D12 deferred) |
| Docker Desktop (Mac) | TCP 127.0.0.1 (Desktop proxies) | token |
| OrbStack / Colima | TCP (host.docker.internal supported) | token |
| Windows + WSL2 (dwe in WSL distro) | TCP | token |
| Windows host-side dwe | out of scope | — |

No platform detection anywhere — both sides always try/offer both transports.

### D3. Daemon listeners

- unix `host.sock` (file 0660, dir `.dwe/bridge` 0700) — always.
- TCP `127.0.0.1:0` — always; ephemeral OS-assigned port ⇒ port collisions
  cannot exist; actual port written to `.dwe/bridge/port` AFTER bind.
- Best-effort additional bind on the docker gateway IP
  (`docker network inspect bridge` → `Gateway`, usually `172.17.0.1`) —
  needed on native Linux; on Mac no such host interface exists → bind fails →
  skipped silently. Never `0.0.0.0` (no LAN exposure).
- Override for exotic setups: env `DWE_BRIDGE_BIND` (list of addresses).
- Known caveat (document in troubleshooting): UFW/firewalld may drop INPUT
  arriving from the docker bridge.

### D4. Wire protocol

Length-prefixed binary frames, identical on both transports:

```
[4 bytes payload length, big-endian uint32]
[1 byte frame type]
[payload]
```

Enforce a max-payload guard (e.g. 1 MiB) to bound memory.

| Type | Name | Payload | V1 |
|---|---|---|---|
| 0x01 | HELLO | JSON: `{protocol_version, token?, argv, env, cwd, tty, term, winsize}` | yes |
| 0x02 | STDIN | raw bytes | yes |
| 0x03 | STDOUT | raw bytes | yes |
| 0x04 | STDERR | raw bytes | yes |
| 0x05 | STDIN_CLOSE | empty (EOF for subprocess stdin) | yes |
| 0x06 | SIGNAL | 1 byte signal number | yes (SIGINT/SIGTERM) |
| 0x07 | RESIZE | 8 bytes (rows uint32, cols uint32) | reserved (pty not planned) |
| 0x08 | EXIT | 4 bytes int32 exit code | yes |
| 0x09 | ERROR | JSON: `{code, message}` | yes |

- ERROR codes: `auth_failed`, `version_mismatch`, `cwd_outside_project`,
  `tty_unsupported`, `daemon_shutting_down`.
- `token` in HELLO is required on TCP, ignored on unix (peercred there).
- Deliberately NO channel-id in the header: one connection = one command.
  Multiplexing appears only if an exec-channel transport is ever added
  (D12) — that would be a new `protocol_version`.

### D5. Daemon connection handling (per accept)

1. Auth: TCP → constant-time token compare; unix → peer uid
   (`SO_PEERCRED` linux / `LOCAL_PEERCRED` darwin) == daemon uid.
2. HELLO validation: `protocol_version` match (else `version_mismatch`);
   realpath cwd containment inside realpath `--project-root` (else
   `cwd_outside_project`); `tty: true` → `tty_unsupported`.
3. Fork subprocess via the injectable exec seam (production: own executable,
   `os.Executable()`): `dwe <argv...>` with translated cwd, filtered env
   (D7) + `DWE_INVOKED_FROM=container` + `DWE_NONINTERACTIVE=1`, own process
   group, plain pipes.
4. Pump: STDIN/STDIN_CLOSE → subprocess stdin; stdout/stderr → frames;
   SIGNAL → signal to process group; connection close (container shutdown) →
   SIGTERM to subprocess, 5 s grace, SIGKILL.
5. EXIT frame with the real exit code.

The daemon is a **stateless forwarder**: no in-memory cache, no business
logic, no project model. The less responsibility it has, the fewer ways it
can break.

### D6. Daemon lifecycle

Start triggers — every lifecycle command asserting "stack is alive" ensures
the daemon:

| Command | Action |
|---|---|
| `dwe deploy run` | cycle (SIGTERM → ensure) — guarantees daemon is not from an older dwe |
| `dwe run` (whole stack) | ensure |
| `dwe run <svc>` | ensure |
| `dwe restart` (whole stack) | cycle |
| `dwe services <name> --apply` (enable) | ensure |
| `dwe status` (top-level) | best-effort ensure (cheap — already probes docker) |
| `dwe bridge start` | ensure (manual) |
| `dwe stop` (whole stack) | SIGTERM |
| `dwe stop <svc>` | untouched |
| `dwe reset run` (whole stack) | SIGTERM |
| `dwe reset run --service <svc>` | untouched |

- **Ensure (idempotent)**: try flock on `.dwe/bridge/daemon.pid`. Acquired ⇒
  previous daemon dead: clean `host.sock`/`port`, spawn detached
  `dwe bridge daemon --project-root <abs>` (double-fork + setsid). Not
  acquired ⇒ alive, no-op.
- Ensure runs **before** `compose up`, so sock/port/token exist before the
  first hook can fire.
- **Auto-stop**: NO connection-idle timeout (a dead daemon cannot be revived
  from inside a container — an idle timeout would brick hooks for an active
  developer). Correct idle criterion — "the stack is actually down":
  subscribe to `docker events --filter label=dwe.project=<name>`; fallback
  `docker ps` poll every 60 s (events stream may drop); zero labeled running
  containers → graceful shutdown: remove `host.sock` + `port` (keep `token` —
  stable project identity), release flock, exit. Startup grace 10 s before
  the first check (compose up needs time to start labeled containers).
- **Versioning**: HELLO carries `protocol_version`; mismatch → ERROR
  `version_mismatch`; shim prints "shim outdated, re-run `dwe deploy` to
  refresh shim binaries" and exits 1. `dwe deploy run` always SIGTERMs the
  existing daemon first.
- **Multi-project isolation**: per-project sock/port/token under each
  project's `.dwe/bridge/`. On Linux both daemons listen on the gateway IP,
  so a container of project A can physically reach project B's port — the
  per-project token is the isolation mechanism (cross-project connect →
  `auth_failed`). cwd containment against `--project-root` on top.

### D7. Path translation & env contract

- Shim cwd inside `DWE_CONTAINER_WORKSPACE` → prefix-rewritten to
  `DWE_HOST_WORKSPACE`; outside → sent as-is and the daemon rejects with
  containment error.
- **argv is pass-through, never translated** (the shell in the hook is
  responsible). Relative paths work (cwd is translated); absolute container
  paths will not resolve on the host — documented limitation.
- Shim strips from forwarded env: `DWE_BRIDGE_DIR`, `DWE_HOST_WORKSPACE`,
  `DWE_CONTAINER_WORKSPACE`, `DWE_BRIDGE_PROJECT`, `DWE_BRIDGE_UNREACHABLE`,
  any `DWE_PROJECT_ROOT*`.
- Daemon re-filters the same set (defense-in-depth) and **force-sets**
  `DWE_INVOKED_FROM=container` and `DWE_NONINTERACTIVE=1` (client-sent values
  are stripped — these variables are host-controlled only).
- `DWE_INVOKED_FROM` must NOT affect `--output json` format.

### D8. Compose overlay & on-disk layout

**Ownership model: dwe-owned machine state, regenerated before every start**
— the same model as `.env` rendering and rendered service configs, NOT a
user-owned config file. The bridge prepare hook regenerates
`.dwe/compose.bridge.yml` (atomic, write-if-changed) on every command that
performs `compose up` (`dwe deploy run`, `dwe run` whole-stack and `<svc>`,
`dwe services … --apply`), together with shim materialization. The file
carries a `# GENERATED by dwe — do not edit; customize via
workspace/local.yml` header; manual edits are unsupported and overwritten.

Why regenerate instead of generate-once + user-owned:

- **Service toggles can never desync it**: the file always contains exactly
  the currently enabled AND bridge-enabled services. A generate-once file
  would keep a partial fragment (`volumes`/`environment` with no `image`)
  for a disabled — and therefore no-longer-defined — service, and
  `docker compose up` hard-fails with "service has neither an image nor a
  build context".
- **No stale state**: project dir moved/renamed (the baked
  `DWE_HOST_WORKSPACE` path), base image arch changed,
  `bridge.shim_path` / `on_unreachable` edited, dwe upgraded with a new
  overlay template — all self-heal at the next start.
- **Less machinery**: no `bridge regenerate` command, no diff-against-
  canonical, no `--force` user-edit detection.

When no enabled service has bridge enabled, the regeneration step DELETES
the file (a stale leftover would otherwise re-enter the chain). The overlay
step of the prepare hook therefore ALWAYS runs, even when bridge is fully
off.

**Chain position**: after app overlays, BEFORE the `workspace/local.yml`
overlays — local.yml remains the user customization channel and keeps the
last word over anything the bridge overlay sets (compose later-file-wins).

Overlay content is platform-independent except the shim mount source: the
arch file is chosen per service at each regeneration (image `Architecture`
via `docker inspect`; fallback = host arch with a warning, never hardcoded
amd64) — so it self-heals too.

Example `.dwe/compose.bridge.yml`:

```yaml
# GENERATED by dwe — do not edit; customize via workspace/local.yml
services:
  app-main:
    volumes:
      - type: bind
        source: ./.dwe/bridge
        target: /dwe-bridge
        read_only: true     # connect() to a socket works on RO mounts (D1.4)
      - type: bind
        source: ./.dwe/bridge/shim-linux-arm64   # arch per image Architecture
        target: /usr/local/bin/dwe               # bridge.shim_path override
        read_only: true
    environment:
      DWE_BRIDGE_DIR:          /dwe-bridge
      DWE_HOST_WORKSPACE:      /Users/foo/projects/my-proj
      DWE_CONTAINER_WORKSPACE: /workspace
      DWE_BRIDGE_PROJECT:      my-proj
      # DWE_BRIDGE_UNREACHABLE: warn   ← only when bridge.on_unreachable: warn
    extra_hosts:
      - host.docker.internal:host-gateway   # required on Linux; harmless on Desktop
```

Port and token are NOT in the overlay — they are dynamic and read by the
shim from `/dwe-bridge` files, so a daemon restart never requires
regeneration.

`.dwe/bridge/` on the host (the overlay itself lives one level up, at
`.dwe/compose.bridge.yml`):

```
.dwe/bridge/
├── host.sock              ← created by daemon at runtime (unix transport)
├── port                   ← actual TCP port, written after bind
├── token                  ← 32 random bytes, hex; mode 0600
├── daemon.pid             ← PID + flock holder
├── daemon.log             ← daemon stderr (no rotation in V1)
├── shim-linux-amd64       ← materialized from embed in host dwe
└── shim-linux-arm64
```

`service.yml` opt-in (mirrors `render.git`, inherits via `extends:` as a
tristate `*bool`):

```yaml
type: app
container: app-main
dir: ./services/main
bridge:
  enabled: true          # default: true for type: app, false otherwise
  # shim_path: /usr/local/bin/dwe   # override on base-image collision
  # on_unreachable: fail | warn     # default: fail
```

Subtleties:

| Issue | Resolution |
|---|---|
| Shim arch | `docker inspect` image Architecture at each regeneration; both arches embedded in host dwe (~+4 MB) |
| `/usr/local/bin/dwe` collision in base image | `bridge.shim_path` per service |
| `bridge.enabled: false` on all services | daemon never starts; regeneration deletes the overlay; shim materialization and daemon ensure skipped |
| Bridged service disabled later (`dwe services <name> disable --apply`) | next compose-up regenerates the overlay without it — nothing stale can remain in the chain |
| Manual edits to the generated overlay | unsupported (GENERATED header); customize via `workspace/local.yml`, which sits after the bridge overlay in the chain and wins |
| Dead `host.sock` file visible on Desktop | by design: shim gets instant ECONNREFUSED and falls back to TCP; do not detect platforms |

### D9. Container command policy

Deliberately **reduced** command surface. Primary motive — foot-gun: `dwe
stop` from inside a container kills the caller's own container (terminal/IDE
session dies, EXIT frame never arrives). Secondary — blast radius: a token in
a compromised container must not be able to destroy data (`reset run`,
`snapshot restore`).

Principle: **allowlist, default-deny** — a new top-level command does not
reach the container surface until explicitly added.

| Allowed from container | Why |
|---|---|
| user commands: `dwe commands <cmd>` (alias `dwe cmd <cmd>`) | the primary case — hooks and project commands |
| `dwe commands` / `dwe cmd` bare | no TTY → prints `commands list` output instead of the TUI browser |
| `status`, `info`, `validate`, `logs` | read-only diagnostics |
| `docs` (incl. `llms-txt`) | read-only; useful to AI agents working in the devcontainer |
| `prompt` | container terminal prompt; ms-range overhead via bridge is acceptable |
| `bridge status` | bridge self-diagnostics |
| `version`, help, completion | service commands |

| Blocked | Why |
|---|---|
| `stop`, `restart`, `reset` | suicidal: stop/recreate the caller's own container |
| `deploy`, `run`, `services` | recreate containers / mutate stack state; stack is managed from the host |
| `snapshot` | restore stops the stack (suicide); create is heavy and takes project locks |
| `render` | mutates workspace files; rare operation — candidate for later opt-in |
| `setup`, `init`, `shell` | interactive; also excluded by no-tty |
| `bridge` (except `status`) | `bridge stop` is suicide for the bridge itself |

Mechanics:

- Trigger: `DWE_INVOKED_FROM=container` (set by the daemon — D7; not
  spoofable from the container).
- Enforcement inside dwe at the composition root, after cobra parsing, before
  RunE. All user commands live under the `commands` subtree ⇒ the allowlist
  is **static** over top-level names; no registry knowledge needed for
  matching.
- Blocked → typed error `bridge_command_blocked` with a run-on-host hint; for
  suicidal commands add the explanation ("stops the container it was invoked
  from"). No protocol ERROR frame involved — ordinary stderr + exit code via
  EXIT.
- Policy is **inherited by nested invocations**: a `type: dwe` user command
  spawns a child dwe with the same env — a hook hiding `dwe stop` inside
  cannot kill the container.
- Help listings and shell completion in container context are filtered by the
  same predicate: blocked commands are **invisible**, not "visible but
  failing".
- Extensibility (per-project opt-in, e.g. `bridge.expose:`) is deliberately
  NOT in V1 (D12).

### D10. Unreachable-daemon policy (shim)

Both transports failed → one-line diagnostic to stderr with project name and
remedy:

```
dwe bridge: host daemon is not running for project "my-proj"
            (start the stack on the host: `dwe run`, or `dwe bridge start`)
```

Exit code policy: default **exit 1** (a hook must block the commit — lint is
enforcement); `bridge.on_unreachable: warn` in service.yml → overlay sets
`DWE_BRIDGE_UNREACHABLE=warn` → shim prints a warning and **exits 0**.

### D11. TTY semantics & security model

- Shim ALWAYS sends `tty: false` and uses plain pipes — even when its own
  stdin/stdout is a terminal (otherwise every command from an IDE terminal
  would be rejected as interactive).
- Daemon adds `DWE_NONINTERACTIVE=1` — interactive dwe branches degrade via
  the existing non-interactive contract (as in CI).
- Output consequence: same as piping on the host — plain reporter instead of
  live view, no-color. Correct by construction.
- `tty: true` in HELLO (non-standard client) → ERROR `tty_unsupported`.
- pty / raw mode / SIGWINCH are **not planned** (policy blocks all
  interactive commands); RESIZE stays reserved in case the policy is ever
  revisited.

Security layers:

| Layer | Mechanic |
|---|---|
| Unix path (Linux) | socket 0660 in dir 0700; peer uid via `SO_PEERCRED` must equal daemon uid |
| TCP path | 256-bit random token (file 0600, RO bind-mount), constant-time compare in HELLO; listener only on 127.0.0.1 + docker gateway IP, never 0.0.0.0 |
| Project isolation | per-project token/socket + cwd containment (realpath inside project root) |
| Env discovery override | shim strips `DWE_PROJECT_ROOT*` etc.; daemon re-filters (defense-in-depth) and force-sets the host-controlled vars |
| Command policy | allowlist default-deny (D9): even a valid token gives no lifecycle mutations or destructive ops |
| argv | pass-through — same attack surface as the ordinary CLI |

Trust note: any local process able to read `.dwe/bridge/token` is equivalent
to the project owner — the same trust model as the project files themselves.

### D12. Deferred decisions & rejected alternatives

Deferred (out of V1, revisit on feedback):

- **Rootless Linux + peercred**: under userns-remap the peer uid does not
  equal the daemon uid for non-root container users. Option: accept the token
  as fallback auth on the unix path too.
- **`bridge.expose:`** policy extension (per-project opt-in for `render`,
  `run <svc>`).
- **daemon.log rotation**; telemetry/metrics.
- **Windows containers / host-side Windows dwe** — out of scope (WSL2-distro
  dwe is just the Linux case).
- **Exec-channel transport** (agent inside container, container-local socket,
  mux over `docker exec` stdio — the VS Code `code`-CLI model): best
  portability and zero host ports, but ×2–2.5 complexity (flow-control mux,
  agent supervisor, docker-bound tests). The frame protocol is
  transport-agnostic by design so this can be added later as a new
  `protocol_version`.

Rejected (do not re-litigate):

| Alternative | Why rejected |
|---|---|
| Unix-socket-only transport | proven dead on Desktop/OrbStack/Colima/WSL2 (D1.1) |
| Conditional hook template (in-container detect, branches) | logic duplication, loses the command engine |
| Full dwe in container + docker socket mount | compose resolves relative paths against the compose file location → container paths unusable by the host daemon; flock on `.dwe/` over VirtioFS unreliable; docker.sock = root-equivalent on host |
| Render-time expansion (hook = expanded docker exec) | loses `when:`, env, snapshot-aware steps |
| SSH back to host | per-developer sshd/keys setup (macOS Remote Login off by default), breaks zero-config |

---

## What Goes Where

- **Implementation Steps**: code, tests, docs in this repo.
- **Post-Completion**: manual platform smoke (Docker Desktop / OrbStack /
  native Linux), release pipeline verification, latency sanity check.

## Implementation Steps

### Task 1: Wire protocol package `bridgeproto`

**Files:**
- Create: `internal/shared/bridgeproto/frame.go`
- Create: `internal/shared/bridgeproto/hello.go`
- Create: `internal/shared/bridgeproto/token.go`
- Create: `internal/shared/bridgeproto/peercred_linux.go`
- Create: `internal/shared/bridgeproto/peercred_darwin.go`
- Create: `internal/shared/bridgeproto/frame_test.go`
- Create: `internal/shared/bridgeproto/token_test.go`

- [x] frame reader/writer over `io.Reader`/`io.Writer`: length-prefixed BE
      uint32 + 1-byte type + payload; max-payload guard; typed constants for
      0x01–0x09 (D4)
- [x] HELLO/ERROR structs + JSON marshal helpers; `ProtocolVersion` const;
      ERROR code constants (D4)
- [x] token: generate (crypto/rand, 32 bytes hex), write/read file (0600),
      constant-time compare
- [x] peercred check behind build tags: `SO_PEERCRED` (linux) /
      `LOCAL_PEERCRED` (darwin), `uid == os.Getuid()` predicate
- [x] write tests: round-trip for every frame type, truncated/oversized input
      errors, HELLO marshal/unmarshal, token compare (match/mismatch/length)
- [x] run tests — must pass before task 2
- ➕ [x] `peercred.go` (shared `PeerUID`/`PeerIsSameUser` predicate) +
      `peercred_other.go` (`!linux && !darwin` fail-closed stub, mirrors
      `shared/lock` fallback shape) + `peercred_test.go` (same-process
      socketpair coverage, runs on both linux and darwin)
- ➕ [x] `TokenEqual` rejects empty tokens (both sides) so a missing token
      file can never authenticate; WriteFrame emits header+payload in a
      single `Write` so concurrent frame writers on a `net.Conn` cannot
      interleave
- ⚠️ fixed pre-existing `make test` failure unrelated to bridge: RU
      translation header hash for `reference/config/validate.md` was not
      bumped in c8d3fa76 (content was already in sync; header now
      `@ ac703a3b947c`)

### Task 2: Service config — `BridgeConfig` + both allowlists

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/validate/config/workspace.go`
- Modify: nearby `*_test.go` for both packages

- [x] add `BridgeConfig` to service config: `Enabled *bool`, `ShimPath string`,
      `OnUnreachable string` (yaml `bridge:`, D8 schema) — named
      `ServiceBridgeConfig` to match the package convention
      (`ServiceCLIConfig`/`ServiceRenderConfig`); adds constants
      `DefaultBridgeShimPath`, `BridgeOnUnreachableFail/Warn`
- [x] nil-safe accessor (e.g. `svc.BridgeEnabled()`): default true for
      `type: app`, false otherwise; default shim path
      `/usr/local/bin/dwe` + `on_unreachable: fail` —
      `BridgeEnabled()`/`BridgeEnabledExplicit()` (reuses the shared
      `renderEnabledExplicit` tristate helper), `BridgeShimPath()`,
      `BridgeOnUnreachable()`
- [x] inherit via `extends:` in `ResolveServiceExtends()` — tristate copy,
      same shape as `render.git.enabled`
- [x] add `bridge` to `allowedFieldsFor()` AND `servicesAllowedFields` (strict
      `KnownFields(true)` decode — both or it hard-errors) — added to the
      common field set (all three service types can opt in; the tristate
      default only turns it on for apps, matching the D8 "false otherwise"
      semantics and the task-11 validator over non-app services)
- [x] write tests: load with/without `bridge:`, extends inheritance (set/unset
      parent/child), unknown sub-field rejected by strict decode, defaults per
      service type
- [x] run tests — must pass before task 3 (`make test`: 105 packages ok;
      `make lint`: 0 issues)

### Task 3: Shim client core + `cmd/dwe-shim`

**Files:**
- Create: `internal/shared/bridgeclient/client.go`
- Create: `internal/shared/bridgeclient/transport.go`
- Create: `internal/shared/bridgeclient/env.go`
- Create: `internal/shared/bridgeclient/client_test.go`
- Create: `cmd/dwe-shim/main.go`

- [x] transport select per D2: `$DWE_BRIDGE_DIR/host.sock` dial (300 ms
      timeout) → read `port`+`token` (retry 2×100 ms on missing) → TCP
      `host.docker.internal:<port>`
- [x] HELLO build: cwd translation per D7, env stripping per D7, always
      `tty: false` (D11)
- [x] pump loop: stdin → STDIN frames (+STDIN_CLOSE on EOF), STDOUT/STDERR →
      local fds, SIGINT/SIGTERM → SIGNAL frames, EXIT → os.Exit(code), ERROR →
      human message per code
- [x] unreachable policy per D10 (message format, exit 1 vs
      `DWE_BRIDGE_UNREACHABLE=warn` → exit 0)
- [x] `cmd/dwe-shim/main.go`: thin main over bridgeclient; no cobra, no
      lipgloss (mirror `internal/shared/prompt` philosophy)
- [x] write tests: in-process fake server over loopback TCP and tmpdir unix
      socket — happy path (stdout/stderr/exit code), unix→tcp fallback, token
      passed on TCP only, cwd translation table, env stripping table,
      unreachable fail vs warn, `version_mismatch` ERROR renders the
      "re-run `dwe deploy`" message verbatim
- [x] run tests — must pass before task 4 (`make test`: 106 packages ok;
      `make lint`: 0 issues; cross-compiles verified for linux amd64/arm64
      and windows/amd64; static linux/arm64 shim ≈ 2.8 MB)
- ➕ [x] dead-connection unreachable mapping: a connection that drops before
      delivering ANY frame is treated as unreachable (Docker Desktop's host
      proxy can accept the TCP connect and only then discover nothing
      listens host-side — would otherwise surface as a cryptic
      "connection lost: EOF" instead of the D10 message)
- ➕ [x] explicit `DWE_BRIDGE_DIR is not set` diagnostic when the shim runs
      outside a bridged container (clearer than the generic unreachable
      message)

### Task 4: Daemon session core — listeners, auth, subprocess proxy

**Files:**
- Create: `internal/core/bridge/daemon.go`
- Create: `internal/core/bridge/listeners.go`
- Create: `internal/core/bridge/session.go`
- Create: `internal/core/bridge/session_test.go`

- [x] listeners per D3: unix `host.sock` (0660) + TCP `127.0.0.1:0`;
      best-effort extra bind on docker gateway IP (injectable resolver);
      write `port` after bind, `token` (0600) if absent; `DWE_BRIDGE_BIND`
      override
- [x] per-connection auth + HELLO validation per D5 (token / peercred,
      protocol version, realpath cwd containment, `tty:true` → ERROR
      `tty_unsupported`)
- [x] subprocess launch behind an injectable exec seam (launcher func/field;
      production = own executable via `os.Executable()`): `dwe <argv...>` with
      translated cwd, filtered env + `DWE_INVOKED_FROM=container` +
      `DWE_NONINTERACTIVE=1`, own process group; plain pipes.
      ⚠️ tests MUST substitute the seam — calling `os.Executable()` from a
      test recursively re-executes the test binary (documented hazard, see
      `internal/cli/lifecycle/testhelpers_test.go:27`) — `Config.Launch`
      seam; production `launchOS` resolves `os.Executable()` lazily so an
      injected launcher never touches it
- [x] pump per D5: STDIN/STDIN_CLOSE → subprocess stdin, stdout/stderr →
      frames, SIGNAL → signal to process group, conn close → SIGTERM, 5 s
      grace, SIGKILL; EXIT frame with real code
- [x] write tests (no Docker, fake launcher via the exec seam): real
      `bridgeclient` against daemon on loopback + tmpdir socket — happy path,
      auth_failed, cwd_outside_project, tty_unsupported, version_mismatch,
      signal forwarding, exit-code passthrough, `DWE_BRIDGE_BIND` override
      parsing
- [x] run tests — must pass before task 5 (`make test`: 107 packages ok;
      `make lint`: 0 issues; `-race` clean; goleak `TestMain` guards the
      accept/session goroutines; module cross-compiles for windows/amd64 and
      linux amd64/arm64)
- ➕ [x] `exec.go` (`LaunchSpec`/`Process`/`LaunchFunc` seam + `launchOS`) with
      `exec_unix.go`/`exec_windows.go` split (Setpgid + group signaling,
      mirrors `core/docs/mermaid` build-tag shape); signal deaths map to the
      shell convention 128+sig; launch failure → STDERR + EXIT 127 (ordinary
      stream, protocol ERROR frames stay reserved for the D4 code set)
- ➕ [x] daemon re-filter reuses `bridgeclient.StripEnv` (single strip-set
      source) then drops/forces the host-controlled `DWE_INVOKED_FROM` /
      `DWE_NONINTERACTIVE`; exported env contract consts (`EnvInvokedFrom`,
      `InvokedFromContainer`, `EnvNonInteractive`) for the task-9 policy
- ➕ [x] daemon-side SIGNAL allowlist (only SIGINT/SIGTERM forwarded, per D4
      V1 scope); 10 s HELLO deadline so half-open connections cannot pile up;
      `daemon_shutting_down` ERROR for connections accepted during Close;
      `listeners_test.go` covers bind-override parsing/integration, file
      modes, stale-socket recovery, and token stability across restarts

### Task 5: Daemon process lifecycle — ensure, auto-stop, logging

**Files:**
- Create: `internal/core/bridge/ensure.go`
- Create: `internal/core/bridge/autostop.go`
- Create: `internal/core/bridge/ensure_test.go`
- Create: `internal/core/bridge/autostop_test.go`
- Create: `internal/cli/bridge/bridge.go` (NewCmd skeleton)
- Create: `internal/cli/bridge/daemon.go` (hidden `bridge daemon` entry)
- Modify: `internal/cli/root.go` (register subtree)

- [x] register hidden `dwe bridge daemon --project-root` subcommand NOW (not
      in task 10) — it is the process ensure spawns, so the system is
      end-to-end runnable from this task; rest of the subtree lands in task 10
      (`internal/cli/bridge` registered in the Advanced group; `dwe bridge
      daemon` added to `allowedWithoutProject` — it takes everything from
      `--project-root`, cwd discovery must not gate a detached spawn)
- [x] ensure (idempotent) per D6: try flock `.dwe/bridge/daemon.pid` —
      acquired ⇒ stale: clean `host.sock`/`port`, spawn detached
      `dwe bridge daemon --project-root <abs>` (double-fork + setsid);
      not acquired ⇒ alive, no-op; `Cycle()` = SIGTERM-by-pidfile + ensure;
      spawn goes through an injectable seam for tests — `Ensure`/`Cycle`/
      `StopDaemon` over `shared/lock` (bridge-private flock; Ensure releases
      the probe flock before spawning — the daemon re-acquires it at startup
      and a spawn-race loser exits cleanly); production spawn = `Setsid` +
      released process handle in `spawn_unix.go` (windows stub mirrors
      `exec_windows.go`)
- [x] graceful shutdown per D6: remove `host.sock`/`port` (keep `token`),
      release flock, exit — Daemon.Close (task 4) + deferred flock release in
      the `bridge daemon` RunE; SIGTERM/SIGINT via `signal.NotifyContext`
- [x] auto-stop per D6: subscribe `docker events --filter label=<project>`
      via injectable docker runner, fallback `docker ps` poll 60 s, startup
      grace 10 s, zero containers → shutdown — `RunAutoStop` with injectable
      `Subscribe`/`CountRunning`; production hooks filter on
      `com.docker.compose.project=<resolved name>` (the label every other dwe
      probe matches; the D6 `dwe.project` label does not exist in the
      codebase) via `config.ResolveComposeProjectName`; count errors are
      logged and retried, never fatal
- [x] daemon stderr → `.dwe/bridge/daemon.log` (append; rotation out of scope)
      — the detached spawn redirects the child's stdout/stderr to daemon.log,
      so panics land there too; daemon logs via stderr with timestamps
- [x] write tests: ensure no-op when flock held, stale cleanup path, cycle
      ordering, auto-stop state machine against scripted fake events/ps
      (start → grace → zero → shutdown; events stream drop → poll fallback)
- ➕ [x] `internal/cli/bridge/daemon_test.go`: hidden flag, required/absolute
      `--project-root` validation, already-running exit-0 path (flock held),
      missing-workspace.yml failure — no Docker, no real daemon
- [x] run tests — must pass before task 6 (`make test`: all packages ok;
      `make lint`: 0 issues; `-race` clean on both new packages; module
      cross-compiles for windows/amd64 and linux/arm64)

### Task 6: Shim build + embed + materialization

**Files:**
- Create: `scripts/build-shims.sh`
- Create: `internal/core/bridge/shimassets/shimassets.go`
- Create: `internal/core/bridge/shimassets/bin/.gitkeep` (committed)
- Modify: `Makefile`
- Modify: `.goreleaser.yaml`
- Modify: `.gitignore`

- [ ] `scripts/build-shims.sh`: `CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64}
      go build -trimpath -ldflags "-s -w" ./cmd/dwe-shim` →
      `internal/core/bridge/shimassets/bin/shim-linux-<arch>` (gitignored,
      mirrors embedded-docs pattern)
- [ ] `shimassets`: `//go:embed all:bin` + the COMMITTED placeholder
      `bin/.gitkeep` so the embed pattern always matches on fresh checkout —
      `//go:embed` against an empty gitignored dir is a hard compile error
      for the whole module (vet/lint compile too); `Materialize(baseDir)`
      skips non-`shim-*` entries → `.dwe/bridge/shim-linux-*` (0755,
      write-if-changed)
- [ ] Makefile: `shims` target; add as prerequisite to `build`, `test`,
      `test-v` AND `test-race` (same shape as `embedded-docs`); note in
      target comment that this adds two cached cross-compiles to every test
      run and why it is load-bearing for module compilation
- [ ] `.goreleaser.yaml`: add shim build to before-hooks
- [ ] write tests: Materialize creates files with mode 0755, idempotent
      (no rewrite when unchanged), overwrites on content change
- [ ] run `make test` — must pass before task 7

### Task 7: Compose overlay generation + chain registration

**Files:**
- Create: `internal/core/bridge/composegen.go`
- Create: `internal/core/bridge/composegen_test.go`
- Create: `internal/core/bridge/testdata/` (golden files)
- Modify: `internal/core/project/config/workspace.go` (composeFiles)

- [ ] render `.dwe/compose.bridge.yml` per D8 — single dwe-owned file with
      the GENERATED header, containing exactly the currently enabled
      bridge-enabled services: RO mounts (`.dwe/bridge` → `/dwe-bridge`,
      shim → `shim_path`), env block (`DWE_BRIDGE_DIR`,
      `DWE_HOST_WORKSPACE`, `DWE_CONTAINER_WORKSPACE`,
      `DWE_BRIDGE_PROJECT`, conditional `DWE_BRIDGE_UNREACHABLE`),
      `extra_hosts: host.docker.internal:host-gateway`
- [ ] shim arch per service via `docker inspect` image Architecture
      (injectable runner; image not pulled / inspect failed → fallback to
      HOST arch (`runtime.GOARCH` → linux equivalent) with warning — dev
      containers virtually always run the host's native arch; never
      hardcode amd64)
- [ ] regeneration semantics: atomic write-if-changed on every compose-up
      command (the prepare hook's overlay step ALWAYS runs); DELETE the file
      when no enabled service has bridge enabled; keep the render function
      pure (config in → YAML out) for golden tests
- [ ] `composeFiles()`: insert `.dwe/compose.bridge.yml` after app overlays
      and BEFORE the local.yml overlays (local keeps the last word — D8
      chain position) when the file exists; iterate services via
      `config.DeployOrder` (golden stability — never `range cfg.Services`)
- [ ] write tests: golden overlay for multi-service config (enabled/disabled
      mix — disabled services absent from output, `shim_path` override,
      `on_unreachable: warn`), arch fallback, write-if-changed (identical
      content → no rewrite), deletion when nothing bridged, chain insertion
      position (before local overlays), toggle round-trip: disable →
      regenerated without the service; re-enable → back in
- [ ] run tests — must pass before task 8

### Task 8: Lifecycle integration

**Files:**
- Modify: `internal/cli/lifecycle/` (deploy run / run / stop / restart / reset)
- Modify: `internal/cli/service/service_toggle.go` (`--apply` path)
- Modify: `internal/cli/status/` (best-effort ensure)
- Modify: nearby `*_test.go`

- [ ] bridge prepare hook (overlay regenerate-or-delete + shim materialize +
      daemon ensure) on every compose-up command: `deploy run`, `run`
      (whole-stack and `<svc>`), `services … --apply` — AFTER preflight and
      `AcquireProjectLocksOrReport`, BEFORE `compose up`. The overlay step
      ALWAYS runs (it must delete a stale file when bridge got fully
      disabled); shim materialization and daemon ensure are skipped when no
      ENABLED service has bridge enabled. Whole-stack `restart` delegates to
      the full `run` lifecycle leg (`internal/cli/lifecycle/restart.go` —
      stop-then-run), so the prepare hook fires there transitively — verify
      this during implementation; if restart turns out to have its own
      compose-up call, add it to this set explicitly
- [ ] daemon step variant: for `deploy run` and whole-stack `restart` the
      prepare hook's daemon step is **cycle** (SIGTERM → ensure, D6) — a
      single action REPLACING plain ensure, not ensure followed by a separate
      cycle (that would SIGTERM the daemon just spawned)
- [ ] SIGTERM daemon in whole-stack `stop` and `reset run`; per-service
      variants untouched (D6 table)
- [ ] best-effort ensure in top-level `dwe status` (errors swallowed, traced
      via `trace.Debugf` only) — acquires NO project locks and runs NO
      preflight (status stays read-only; ensure's `daemon.pid` flock is a
      separate, bridge-private lock)
- [ ] write tests: hook ordering with fake bridge manager (prepare called
      before compose up, not called when disabled), stop/reset SIGTERM path,
      per-service commands do not touch daemon; existing lifecycle tests stay
      green
- [ ] run tests — must pass before task 9

### Task 9: Container command policy

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/bridgepolicy.go`
- Create: `internal/cli/bridgepolicy_test.go`
- Modify: commands-browser entry point (bare `dwe commands` fallback)

- [ ] static allowlist predicate per D9 over resolved cobra command path:
      allow `commands` subtree, `status`, `info`, `validate`, `logs`, `docs`,
      `prompt`, `bridge status`, `version`, `completion`, `help`;
      default-deny the rest
- [ ] gate inside the EXISTING root `PersistentPreRunE`
      (`internal/cli/root.go:165` — the single pre-RunE hook; do NOT add a
      second persistent hook anywhere, cobra replaces instead of chaining);
      blocked → typed `cmdctx.Err("bridge_command_blocked")` with run-on-host
      hint and suicide explanation for stop/restart/reset
- [ ] hide blocked commands from help and shell completion when
      `DWE_INVOKED_FROM=container` (dynamic `Hidden` at tree build)
- [ ] bare `dwe commands`/`dwe cmd`: replace the current non-interactive
      error (`internal/cli/command/command.go:133` via
      `widgets.IsInteractiveFn`) with `commands list` output; trigger on
      non-tty stdin OR truthy `DWE_NONINTERACTIVE` (match the
      `{"1","true"}` set used in `runbyid.go:82`) so CI pipes and bridge
      behave identically
- [ ] write tests: table-driven allow/deny incl. nested paths
      (`bridge status` vs `bridge stop`), flags-before-subcommand parsing,
      hidden filtering, JSON error envelope shape, browser→list fallback
- [ ] run tests — must pass before task 10

### Task 10: `dwe bridge` CLI subtree

**Files:**
- Modify: `internal/cli/bridge/bridge.go` (extend NewCmd from task 5)
- Create: `internal/cli/bridge/start.go`, `stop.go`, `status.go`, `logs.go`
- Create: `internal/cli/bridge/bridge_test.go`

- [ ] extend the subtree registered in task 5 (`NewCmd(groupID, flags)` +
      hidden `bridge daemon` already exist) with the user-facing commands
- [ ] `start` / `stop`: ensure / SIGTERM-by-pidfile; clear messages when
      project has no bridge-enabled services
- [ ] `status`: liveness (flock probe), pid, uptime, transports (sock path,
      tcp port), shim materialization state — through `cmdctx.WriteData[T]`
      (JSON mode contract) with human renderer
- [ ] `logs --tail N`: read `.dwe/bridge/daemon.log`
- [ ] write tests: status JSON golden (running/stopped), logs tail, args
      validation; display strings via i18n store helpers (no raw
      `def.Description` reads)
- [ ] run tests — must pass before task 11

### Task 11: Validation domain `validate/bridge`

**Files:**
- Create: `internal/core/validate/bridge/bridge.go`
- Create: `internal/core/validate/bridge/bridge_test.go`
- Modify: `internal/cli/validate/validate.go` (cross-domain wiring)

- [ ] validators: `on_unreachable` enum (`fail`|`warn`), `shim_path` must be
      absolute, `bridge.enabled: true` on a service without a container
      target → warning
- [ ] register the domain: add a `valbridge.All()` iteration alongside the
      existing domain loops in `internal/cli/validate/validate.go`
      (lines ~577–634 — THIS is the cross-domain registration site; the
      domain's own `All()` merely mirrors the per-domain shape of
      `internal/core/validate/config/all.go`). Participates in `dwe validate`
      only — NOT preflight (preflight consumes only `valconfig.All()`;
      bridge config errors must not block unrelated lifecycle)
- [ ] severity levels consistent with existing domains (error vs warning)
- [ ] write tests: table-driven diagnostics per validator (valid, invalid
      enum, relative path, non-container service), severity codes
- [ ] run tests — must pass before task 12

### Task 12: Docs, i18n, AGENTS.md

**Files:**
- Create: `docs/reference/bridge.md`
- Create: `docs/i18n/ru/reference/bridge.md`
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md` (NOT `CLAUDE.md` — it is a symlink)

- [ ] `docs/reference/bridge.md`: `bridge:` schema, transport behavior matrix
      (D2), command policy table (D9), `.dwe/bridge/` contents (D8),
      troubleshooting (UFW / rootless / "daemon not running"), `dwe bridge`
      subcommands
- [ ] RU translation under `docs/i18n/ru/reference/`
- [ ] `docs/internals/packages.md`: sections for `bridgeproto`,
      `bridgeclient`, `core/bridge`, `cli/bridge` — invariants (stateless
      daemon, env override contract D7, default-deny policy D9, overlay
      regeneration model D8)
- [ ] `AGENTS.md`: add Critical Patterns entry (bridge env contract +
      policy allowlist + both-allowlists reminder)
- [ ] run `make build` (re-syncs embedded docs + content hashes) and verify
      `dwe docs` serves the new page
- [ ] run `make test` — must pass before task 13

### Task 13: Verify acceptance criteria

- [ ] cross-check implementation against § Design Reference invariants:
      default-deny policy (D9), RO mounts (D8), always `tty:false` (D11),
      daemon-controlled `DWE_INVOKED_FROM` (D7), overlay regenerated on ALL
      compose-up commands and deleted when nothing bridged (D8), disabling a
      bridged service must NOT break `compose up` (regeneration, D8),
      local.yml overlays override the bridge overlay (chain order, D8),
      per-project token isolation (D6), no `0.0.0.0` binds (D3)
- [ ] backward-compat check: project with no `bridge:` config produces no
      overlay, starts no daemon, all existing golden tests untouched
- [ ] run full suite: `make test` and `make test-race`
- [ ] run `make lint`; `gofmt`/`goimports` clean
- [ ] `make build` + `make snapshot` dry-run builds with embedded shims;
      record binary size delta (expected ≈ +4 MB)

### Task 14: [Final] Update documentation and close out

- [ ] update README feature list if bridge warrants a mention
- [ ] verify AGENTS.md/packages.md entries from task 12 still match final code
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual platform smoke (cannot run in CI):**
- Docker Desktop (macOS): real devcontainer, `git commit` triggering
  `exec dwe commands <cmd>` hook; verify unix dial falls back to TCP
  instantly; verify `dwe stop` from container is blocked with the suicide
  explanation; verify hook latency is acceptable (target < ~150 ms overhead)
- OrbStack: same hook smoke (TCP path)
- Native Linux: unix-socket path with peercred; UFW-enabled host sanity check
- Dead-daemon UX: kill daemon, commit from container → clear message;
  `on_unreachable: warn` variant exits 0

**Release pipeline:**
- verify GoReleaser release artifacts include embedded shims for both arches
  (snapshot inspect), Homebrew tap install still works
- release notes: document new `.dwe/bridge/` artifacts and `bridge:` schema

**Deferred (see § Design Reference D12):**
- rootless-Linux peercred fallback to token auth
- `bridge.expose:` policy extension opt-in
- daemon.log rotation; exec-channel transport if TCP hits corporate walls

# Docker integration

How DWE drives Docker Compose: the compose project name, the file list it assembles, the environment it propagates to every child process, the volumes it owns, and the few places it bypasses compose and calls `docker stop` / `docker rm` directly.

## Contents

- [Two entry points: `dwe docker` vs `dwe compose`](#two-entry-points-dwe-docker-vs-dwe-compose)
- [Project name](#project-name)
- [Compose file list](#compose-file-list)
- [Process environment](#process-environment)
- [Volumes](#volumes)
- [Compose-bypass on the per-service path](#compose-bypass-on-the-per-service-path)
- [`dwe deploy` end-to-end](#dwe-deploy-end-to-end)
- [Where to go next](#where-to-go-next)

## Two entry points: `dwe docker` vs `dwe compose`

DWE never asks the user to type `docker compose` directly. Every lifecycle invocation routes through one of two CLI surfaces:

| Surface | Purpose | Policy args applied? |
|---------|---------|----------------------|
| `dwe docker <sub>` | Public lifecycle API used by Makefiles, deploy steps, and user commands. | Yes (`global` + per-subcommand defaults). |
| `dwe compose raw <args...>` | Low-level diagnostic pass-through. | No. |
| `dwe compose files` / `compose argv` | Inspect the active file list or the full effective argv. | n/a (read-only). |

Both surfaces resolve through the same underlying argv assembler and produce two flavours of compose invocation:

- **Public lifecycle calls** assemble `compose -p <project> -f <file>… <globalArgs> <command> <commandDefaultArgs> <extraArgs>`. This is what `dwe docker` and pipeline `docker.*` builtins use.
- **Internal probes** (health checks, "is container running" queries) build the same skeleton but skip both `globalArgs` and per-command defaults, so a user-supplied override like `args.ps: ["--services"]` cannot break them.

## Project name

The compose project name is the `-p <name>` value passed to every `docker compose` invocation. It is also the prefix Docker Compose uses for its own resource naming conventions: containers (`<project>-<service>-<n>`), networks (`<project>_default`), and named volumes (`<project>_<vol>`).

DWE resolves the name from `workspace/docker.yml`:

```yaml
# workspace/docker.yml
project_name: "${project.prefix}-${project.name}"
```

`${dot.path}` placeholders are resolved against the merged project configuration (the `workspace.yml` → `defaults.yml` → `local.yml` cascade). The same name is fed back into:

- Every compose invocation (`compose -p <name>`).
- The non-shared volume name resolver (`<project>_<volume>` — see [Volumes](#volumes)).
- The per-service container name resolver used by per-service stop and reset (see [Compose-bypass](#compose-bypass-on-the-per-service-path)).
- The compose-bypass volume-removal builtin, which uses the project name as a prefix filter when sweeping volumes.

A typical resolved name looks like `myorg-shop` or `dwe-laravel`. The exact form is part of the public surface — renaming the project requires updating `local.yml` so the prefix matches, otherwise the next `docker compose` invocation talks to a different project and the old containers and volumes go orphan.

## Compose file list

DWE does not write a single `compose.yaml` that imports everything. It passes a *list* of `-f` flags, in order, and lets Docker Compose merge them. The list is assembled deterministically:

1. The base file from `compose.base` in `workspace.yml` (always included).
2. Enabled **tool** overlays, sorted by service key.
3. Enabled **infra** overlays, sorted by service key.
4. Enabled **app** overlays, sorted by service key.

Service type order matters: tools first, then infra, then apps. Within a group, sort is alphabetical by the service key (the directory name under `workspace/services/<name>/`). The explicit sort keeps the file list deterministic so `docker compose` always sees overlays in the same merge order.

`dwe docker pull --all` and `dwe docker build --all` operate on the same ordered list but ignore the `enabled` flag, so a developer can pull or build images for overlays they have toggled off locally — without modifying `workspace/local.yml`.

Each entry in the list points at a file inside the project tree, typically under `compose/`:

```text
compose/base.yml                  # compose.base
compose/tools/redis-insight.yml   # tool overlay
compose/services/api.yml          # app overlay
```

There is no `docker.local.yml`-level override of the compose file list. Local overrides live in:

- `workspace/local.yml` — per-service `enabled: true|false`, ports, hosts, custom envs. Affects the list contents via the enabled set.
- `workspace/docker.local.yml` — per-policy overrides (project name, args, process env, topology). Does **not** add or remove `-f` files.

To inspect the effective list run `dwe compose files`.

## Process environment

Every `docker compose` child process inherits the parent process environment plus an overlay defined in `workspace/docker.yml`:

```yaml
process_env:
  DOCKER_CLI_HINTS: "false"
```

`Compose.BuildEnv()` returns `os.Environ()` with these keys overlaid — existing values are replaced, new keys are appended, and the result is sorted-stable for deterministic test output. When `process_env` is empty, `BuildEnv()` returns `nil` and the child inherits the parent environment unchanged (the common path).

`process_env` affects the **compose CLI process**, not the running containers. Container-visible env comes from `.env` (and the compose file's `environment:` blocks). DWE auto-regenerates `.env` from the active config before five subcommands — `up`, `run`, `exec`, `restart`, `build`. This step is intentionally not configurable. Other subcommands (`down`, `stop`, `ps`, `logs`, `pull`) skip it because they do not need a current `.env`.

## Volumes

Docker Compose creates named volumes lazily: the first `docker compose up` that references a volume creates it. DWE adds two layers on top:

- **Project scoping.** Volumes declared under `resources.volumes` in `workspace/docker.yml` get a `<project_name>_` prefix, matching Compose's own naming convention for `volumes:` declared inside `compose.yaml`. A volume with `name: build_artifacts` and `shared: false` becomes the actual Docker volume `myorg-shop_build_artifacts` (the prefix applies to the `name:` field, not the map key).
- **Shared mode.** `shared: true` opts out of the prefix. The volume is created with its literal name and survives `dwe reset` runs on this project — and is reused by any other DWE project that declares the same shared name. The canonical use case is a language-toolchain cache (composer, npm, go-build) shared across projects.

`ensure_before: [up, deploy]` triggers idempotent creation on those entry points. The non-shared, project-scoped volumes are also what `docker_remove_project_volumes` sweeps during reset: the builtin lists every Docker volume whose name begins with `<project_name>_` and removes it. Shared volumes do not match the prefix and survive.

## Compose-bypass on the per-service path

For full-stack lifecycle (`dwe run`, `dwe stop`, `dwe restart` with no service argument) DWE calls `docker compose up` / `down` / `stop`. The compose file list and policy args apply as described above.

Two flows deliberately bypass compose:

- **`dwe stop <service>`.** When the user names a single service, DWE resolves the compose container name through `daemon.ResolveContainerName(projectFull, svc.Container)` and calls `docker stop <name>` directly. This works even after the service has been disabled in `local.yml` — at which point the service's overlay is no longer in the `-f` list and `docker compose stop <name>` would not see the container at all. The user-visible behavior is "I can always stop this service by name".
- **`dwe reset run --service <name>`.** The per-service reset prepends a synthetic `docker_stop_remove_container` builtin step to the service's pipeline. The builtin stops and removes the named container in two `docker` calls, again outside compose. The reset pipeline body then runs as declared; volume cleanup happens only when the user opts in via `docker_remove_project_volumes`.

For everything else — `dwe docker up`, `down`, `logs`, `ps`, `exec`, `run`, `pull`, `build`, plus `dwe stop` with no service argument — DWE talks to `docker compose`.

## `dwe deploy` end-to-end

A `dwe deploy run` invocation walks the deploy pipeline (preflight → orchestrator phases → per-service overlays → infra `after:` → final hooks). At several points the pipeline calls into the Docker layer described above:

```mermaid
sequenceDiagram
  autonumber
  participant U as User
  participant CLI as DWE CLI
  participant Pipe as deploy pipeline
  participant Env as .env render
  participant Compose as compose layer
  participant Docker as docker compose

  U->>CLI: dwe deploy run
  CLI->>CLI: preflight + acquire project locks
  CLI->>Pipe: run phases
  Pipe->>Compose: resolve project name + file list + process env
  Note over Compose: stable for the<br/>whole pipeline run

  Pipe->>Pipe: docker_remove_project_volumes (if declared)
  Pipe->>Env: regenerate .env before up
  Pipe->>Compose: assemble up argv (svc...)
  Compose->>Docker: docker compose -p <project> -f ... up -d --remove-orphans
  Docker-->>Compose: container IDs
  Pipe->>Compose: assemble internal ps argv (--status running --services)
  Compose->>Docker: docker compose ps --services
  Docker-->>Pipe: running service names
  Pipe->>Pipe: docker_wait_healthy (poll health by container ID)
  Pipe-->>CLI: pipeline complete
  CLI-->>U: deploy ok
```

Three properties to notice:

- The compose layer is resolved once per pipeline run from the merged configuration and reused for every invocation. Project name, file list, args, and process env are stable across the whole deploy.
- Lifecycle commands and internal probes share the same project name and file list but assemble argv differently. A user override like `args.ps: ["--services"]` cannot break the running-services probe because the probe skips user-supplied policy args.
- `.env` regeneration runs **before** the compose call, never in parallel with it. The compose invocation always sees a current `.env`.

## Where to go next

- [`docker.yml` field reference](../config/docker.md) — every field of `workspace/docker.yml` and `workspace/docker.local.yml`.
- [Render env](../render/env.md) — what goes into `.env` and how `${...}` is resolved.
- [Deploy](../config/deploy/index.md) — the pipeline that wraps these compose calls.
- [State and locks](state-and-locks.md) — why `deploy.lock` and `snapshot.lock` serialise the pipeline above.
- [Project layout](project-layout.md) — where the `compose/` overlays live on disk.

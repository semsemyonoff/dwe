# Integration tests — `dwe test`

Load when the task is "verify a clean deploy still works", "add an integration test / scenario", "test with redis off", "assert the app answers after deploy". You **author** scenario yml under `workspace/tests/`; whether you may also run `dwe test run` is decided per scenario by the gate in `SKILL.md` (§ The `dwe test run` gate).

`dwe test run` copies the project into `.dwe/tests/runs/<scenario>/`, gives the copy its own compose project and freshly allocated host ports, runs `dwe validate` + a real `dwe deploy run` from a clean slate, runs your steps, then tears it down. Reference: `dwe docs show config/tests --lang en`; guide: `guides/integration-tests`.

## 1. Read / mutate split

| Command | Class |
| --- | --- |
| `dwe test list [-o json]` | read, no Docker, no locks; JSON carries `cost_profile` |
| `dwe validate tests` | read, no Docker — run while authoring |
| `dwe test clean --dry-run` | read (read-only `docker ps` probe, brief flocks) |
| `dwe test run [scenario...] [--keep] [--parallel N] [--timeout 15m]` | **mutating + slow** — conditional on the gate |
| `dwe test clean [scenario...]` | **mutating** — hand over; manifest-driven teardown of kept / crashed runs |

Isolation is real but not total: `shared: true` volumes are reused verbatim, named / `external:` compose resources bypass project scoping, and host side effects of host-executing steps are not sandboxed. Those leaks are exactly the profile's three hard stops (`shared_volumes`, `isolation_findings`, `host_steps`).

## 2. Author a scenario

One file per scenario, `workspace/tests/<name>.yml`; name = basename, `^[a-z0-9][a-z0-9_-]*$`; strict loader, **an empty or all-comment file is an error**. Fields (only `steps` does work):

- `description:` — shown by `dwe test list`.
- `env.services: { enable: [...], disable: [...] }` — service keys, scoped to the copy (a `required` service stays enabled).
- `env.vars:` — dot-paths under `vars.` overriding the copy's `local.yml`. A concrete number **pins** a port (disables the automatic remap for that path); `auto` allocates a free port for a var **no compose port reads** — otherwise unnecessary.
- `timeout:` — whole-scenario budget (`15m`; default 30m).
- `steps:` — run after the implicit deploy, **same step schema as `deploy.yml`** (`pipelines-and-orchestration.md` § 2).

Minimal smoke test (no `steps:` is already useful) and a variant — one meaningful difference per file:

```yaml
# workspace/tests/smoke.yml
description: "Clean deploy comes up healthy"
steps:
  - name: "containers are up"
    type: builtin
    cmd: containers_running
    with: { services: [app, db] }          # compose service names, not folder keys
  - name: "app answers"
    type: builtin
    cmd: http_check
    timeout: 30s
    with: { url: "http://localhost:${services.app.ports.http}/health", status: 200, contains: "ok", retries: 10, interval: 2s }
```

```yaml
# workspace/tests/redis-off.yml
description: "Deploy with redis disabled — cache falls back to in-memory"
env:
  services: { disable: [redis] }
steps:
  - { name: "app still answers", type: builtin, cmd: http_check, with: { url: "http://localhost:${services.app.ports.http}/health", status: 200 } }
```

Predicate builtins as step bodies are assertions (`http_check`, `containers_running`, `file_exists`, `tcp_reachable`, `env_keys_present`, `shell`); `${...}` in `with:` / `cmd:` resolves against the **copy's** config and paths resolve against the copy root. `dwe validate tests` does not cross-check `containers_running` service names against compose — a wrong name fails only at run time; confirm with `dwe compose raw -- config --services`.

## 3. Test-only commands

A `type: command` step dispatches through the registry **including `private` commands** — keep seed / dump helpers off the everyday listing with `private: true`, not `hide:` (pipelines skip `hide` commands). Author them in `workspace/commands/**.yml` (`authoring-commands.md` § 5).

## 4. Validate first (read, free)

```shell
dwe validate tests --output json
```

Static: name, `timeout`, `env.services` references, step schema + builtin `with:` params + `when:`, `type: command` IDs, compose-isolation findings. All findings report as `warning` here, including the two kinds that **block** `dwe test run` (`container_name:`, a literal host port) — judge by kind, not severity. A var populated only post-deploy (a `${generated.*}` secret) is absent at validate time; give it a project-level default.

## 5. Ports & isolation

Never hand-wire host ports in a scenario. The copy remaps automatically: every host port under `services.<name>.ports` of an enabled service, and every compose variable an active `exports.env` rule exports `from: vars.<path>`. Steps read the remapped value the normal way (`${services.<name>.ports.<x>}`, `${vars.<path>}`).

- **Literal host port in compose** (`8080:8080`) → **blocking** `raw_host_port`; the scenario creates nothing. Replace it with an interpolated token (`${APP_PORT}`) exported from a declared port or a `vars.<path>`.
- **`container_name:` in compose** → **blocking**; drop it, compose names containers from project + service.
- **Untraced variable port** (no rule, falsy `when:`, or a rule reading something other than `vars.<path>` / a declared port) → non-blocking `interpolated_host_port`; it binds the live port in every copy and collides under `--parallel` or beside a `--keep` run. Route it through one of the two channels above.
- `external:` / explicitly named volumes and networks → warnings; `--skip-isolation-check` downgrades blocking findings (last resort).

**Host scripts must take the project name from `$COMPOSE_PROJECT_NAME`** (dwe hands the copy's name to shell steps and scripts); a script that builds its own `docker compose -p "${PREFIX}-${NAME}"` hits the live stack. Use `PROJECT="${COMPOSE_PROJECT_NAME:-<fallback>}"`. `dwe validate tests` warns (`tests.host_project_name`) about the common shape.

**The remap changes the Host the app sees** — multisite / domain routing, CORS, signed URLs may behave differently on a random port. Confirm the app tolerates it before asserting anything beyond a health path.

## 6. Debugging loop + handoff

A failed run collects a report into `.dwe/tests/reports/<scenario>/` **before** teardown: `pipeline.log`, `compose-ps.txt`, `container-logs.txt` — read them directly. A scenario blocked by the isolation scanner creates nothing and writes no report (an older one may remain — trust the run's warnings).

- `dwe test run --keep <scenario>` skips teardown and leaves the copy (blocks a re-run until cleaned). Suggest `--keep` on the **first** run of a new scenario — a second full deploy just to obtain a copy is expensive. `--keep` does not change the gate.
- `dwe test run -v` / `--debug` propagates into the copy's `dwe validate` + `dwe deploy run` — reach for it before `--keep` when the question is *why did the deploy do X*.
- **The copy is itself a valid dwe project**: any project-level command run with cwd inside `.dwe/tests/runs/<scenario>/` resolves the copy as root and returns silent empty results (`dwe test clean` sweeps nothing). `cd` back to the real root first.
- Clean up kept / crashed runs: `dwe test clean` (`--dry-run` first; the sweep is a handoff; compose projects without a manifest are only reported).

Exit codes: `0` passed, `1` a scenario failed, `2` could not be prepared (bad name, held lock, kept prior run).

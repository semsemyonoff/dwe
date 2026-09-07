# Integration tests — `dwe test` (isolated deploy verification)

Load this file when the task is "verify a clean deploy still works", "add an integration test / scenario", "test with redis off", "assert the app answers after deploy", or "make a throwaway test environment". **Your job here is to AUTHOR scenario yml** under `workspace/tests/`; whether you may also *run* the mutating `dwe test run` is decided per scenario by its cost profile (§ 1).

`dwe test` runs `dwe validate` + a **real** `dwe deploy run` inside a fresh, isolated, disposable copy of the project (`.dwe/tests/runs/<scenario>/`), runs your assertions, then tears it all down. Host ports are auto-isolated, so a scenario can run *alongside* the live env — though isolation has limits (§ 2). `dwe test` **requires a project** (unlike the read-only docs commands).

## 1. When to reach for it — and who may run it

Its value: a dwe project's deploy pipeline is normally only ever exercised against the developer's one working environment. `dwe test` is the only way to answer "does a **clean** deploy still come up with these config changes?" without risking that environment — so it's the natural verification step after you edit `service.yml`, a `deploy.yml`, a render template, `exports.env`, or add a service.

**Propose it selectively.** Suggest a test for *substantial* changes (new service, reworked deploy pipeline, config that touches provisioning/secrets). Don't offer it after routine or display-only edits. That part is unchanged.

**Whether you may run it yourself is conditional — and the condition is data.** A run is a full clean deploy inside a copy (image pulls, builds, container starts), and isolation is real but not total (§ 2). So read the facts before deciding:

```shell
dwe test list --output json
```

Each scenario carries a `cost_profile` object. Two groups, judged differently:

| Field | Meaning | How it decides |
| --- | --- | --- |
| `isolation_findings` | named / `external:` volumes and networks the copy shares with the real env. An entry carrying `"shared": true` is a `docker.yml` `shared: true` volume the project acknowledges — already counted by `shared_volumes` | **hard stop** if non-empty after dropping `"shared": true` entries |
| `shared_volumes` | `shared: true` volumes — the real cache/data | **hard stop** if > 0 |
| `host_steps` | steps running **project-authored code on the host** — `type: shell`, the `shell` builtin, a `type: command` resolving to a host command, a `type: dwe` re-entering a pipeline, and shell `when:` / `check:` conditions — in the scenario, in the deploy it triggers, and in the `workspace/validate.yml` checks the run executes (`cmd: shell` or `type: command`). dwe's own subcommands don't count, so the built-in default pipeline reports 0 | **hard stop** if > 0 |
| `build_services` | compose services that build locally | judge the build (below) |
| `external_images` | images a cold run would pull | judge the cost |
| `max_start_period_seconds` | largest healthcheck `start_period` (max, not sum — `up --wait` waits in parallel) | judge the cost |

The three hard stops are the isolation half: they are exactly the channels through which a run reaches **outside** its own copy, so no cost argument redeems them — hand the command over.

The cost half is a judgement, not a reflex. **A build is not an automatic stop**: read the Dockerfile and decide what it actually is — a thin layer over a published base is minutes at worst; compiling a toolchain from source is not something to start unattended. **The profile deliberately does not model this**: it tells you whether there *is* a build, never what it costs, and the dominant factor — whether the Docker layer cache is warm, seconds versus many minutes — has no static source and was not guessed at.

> **Run unattended only when all three hard stops are clear AND you can positively account for the cost. Otherwise hand the exact command to the user — and when unsure, hand it over.** Measured on two real workspaces, both have builds and both land in "ask"; that is the rule working, not failing.

`dwe validate tests` (free, no Docker) comes first either way — § 8.

## 2. Read / mutate split

| Command | Class | Rule |
| --- | --- | --- |
| `dwe test list` | read (no Docker) | run freely — `--output json` also carries each scenario's `cost_profile` (§ 1) |
| `dwe test clean --dry-run` | read (no teardown) | run freely — previews a sweep, destroys nothing (does a read-only `docker ps` orphan probe + briefly flocks each scenario) |
| `dwe validate tests` | read (no Docker) | run freely — static scenario check; run it while authoring |
| `dwe test run [scenario...]` | **mutating + slow** | **conditional** — full Docker deploy in a disposable copy; run it yourself only if the scenario's cost profile clears the gate in § 1, otherwise hand it over |
| `dwe test clean [scenario...]` (no `--dry-run`) | **mutating** | **hand to the user** — tears down kept or crashed/interrupted runs (manifest-driven; no-manifest orphans are only *reported*, never auto-removed) |

The mutations are **mostly isolated and disposable** — own compose project, **non-shared** volumes, auto-remapped host ports, and a copy-local `.dwe/`, so they don't touch your running stack. But isolation is **not** total: `shared: true` volumes are reused verbatim (real cache/data is visible to every run), `container_name:` / named / `external:` compose resources bypass compose-project scoping, and arbitrary host side effects of host-executing deploy steps (absolute paths, `~`, bind mounts outside the project) are not sandboxed — see "Documented limitations" in `dwe docs show config/tests --lang en`. **These three leaks are precisely the profile's hard-stop fields** (`shared_volumes`, `isolation_findings`, `host_steps`): when any of them is set, the gate in § 1 sends the run to the user, because a failure would no longer be confined to the copy. When they are all clear, the remaining question is only cost.

## 3. Author a scenario file

One file per scenario at `workspace/tests/<name>.yml`. The scenario **name is the file basename** (symmetric with `workspace/services/<name>/`) — no `name:` field. Names must match `^[a-z0-9][a-z0-9_-]*$` (compose-project-name-safe). The loader is **strict** (`KnownFields(true)`); an **empty or all-comment file is an error** (a scenario has no meaningful default). An absent `workspace/tests/` directory is fine — `test list`/`run` just find nothing.

Schema shape (only `steps` does the work; the rest are optional):

- `description:` — human summary, shown verbatim by `dwe test list`.
- `env.services:` — `{ enable: [...], disable: [...] }`, service KEYS; same effect as `dwe services enable/disable --apply`, scoped to the copy.
- `env.vars:` — dot-paths under `vars.` (`app.http_port` → `vars.app.http_port`) overriding the copy's `local.yml`. The one magic value is **`auto`** = allocate a free host port and inject the concrete number before deploy.
- `timeout:` — wall-clock budget for the whole scenario (deploy + steps), e.g. `15m`; must be positive.
- `steps:` — ordered, run AFTER the implicit deploy, using the **exact same step schema as `deploy.yml`** (`type: shell` / `command` / `dwe` / `builtin`, with `when:`).

Full schema → `dwe docs show config/tests --lang en`. Authoring guide → `dwe docs show guides/integration-tests --lang en`.

## 4. A first scenario + a variant

Minimal smoke test — "deploy comes up healthy" (a scenario with no `steps:` is already useful):

```yaml
# workspace/tests/smoke.yml
description: "Clean deploy comes up healthy"

steps:
  - name: "app answers"
    type: builtin
    cmd: http_check
    with:
      url: "http://localhost:${services.app.ports.http}/health"
      status: 200
```

A variant — one meaningful difference per file (a service off, a var flipped), not a parametrized mega-file:

```yaml
# workspace/tests/redis-off.yml
description: "Deploy with redis disabled — cache falls back to in-memory"

env:
  services:
    disable: [redis]

steps:
  - name: "app still answers without redis"
    type: builtin
    cmd: http_check
    with: { url: "http://localhost:${services.app.ports.http}/health", status: 200 }
```

## 5. Assertions — `http_check`, predicate-as-assertion, per-step `timeout`

Steps reuse the deploy step schema (`pipelines-and-orchestration.md` § 2). Three 0.4.0 engine features matter here (all **general** — they also work in `deploy.yml`/`reset.yml`/`lifecycle.yml`):

- **Predicate-builtin-as-step-body = assertion.** Any predicate builtin used directly as a step `cmd:` (`file_exists`, `tcp_reachable`, `containers_running`, `env_keys_present`, `http_check`, …) **fails the step on `false`, succeeds on `true`** — not just inside `check:`. Assertion-body steps **always re-run** (they are exempt from deploy's up-to-date skip gate).
- **`http_check`** — new predicate builtin; complements `tcp_reachable` for web stacks that need a moment after `up` (`url`, `status`, optional `contains`, `retries`, `interval`, `timeout`).
- **Per-step `timeout:`** — opt-in, absent/`0` = unbounded; bound a slow assertion (`timeout: 5s` on an `http_check`).

```yaml
steps:
  - name: "containers are up"
    type: builtin
    cmd: containers_running
    with: { services: [app, db] }
  - name: "app answers quickly"
    type: builtin
    cmd: http_check
    timeout: 5s
    with: { url: "http://localhost:${services.app.ports.http}/health", status: 200, contains: "ok" }
```

`${...}` in a step's `with:`/`cmd:` resolves against the **copy's** config before steps run, and `file_exists`-style paths resolve relative to the copy root — assertions always inspect the disposable env, never the working tree. Builtin + assertion mechanics → `dwe docs show config/deploy/builtins --lang en`. The `timeout:` field → `dwe docs show config/deploy/index#step-fields --lang en`.

**`with.services` (for `containers_running`) are raw compose service names — NOT `workspace/services/<name>/` keys.** The two often coincide but can differ (a `magento` service folder may define its compose service as `app-magento`). `dwe validate tests` does **not** cross-check these against the real compose, so a wrong name surfaces only at run time as `services not running: <name>`. Confirm the authoritative list yourself with `dwe compose raw -- config --services` (or read the files from `dwe compose files`) — those are exactly the names `containers_running` matches against.

## 6. Ports & isolation — don't hand-wire, don't collide

**Never hand-wire host ports in a scenario.** Every host port an enabled service declares under `services.<name>.ports` is automatically remapped to a freshly allocated free port in the copy; `ports_free` preflight and the actual bind read that same field, so they move together. A step references the remapped port the normal way: `${services.<name>.ports.<x>}`.

**The auto-remap covers ONLY `services.<name>.ports` — nothing else.** A host port that reaches compose through any *other* channel is not remapped, and only one such channel is safe:

- **A bare literal** (`8080:8080`) in raw compose → **blocked** by the isolation scanner (see below). Not a silent problem — the run refuses to start.
- **A var-interpolated compose port** (`ports: ["${DB_PORT}:5432"]` sourced from `vars.*` or any free-form field, NOT modeled under `services.<name>.ports`) → **silently not remapped**, because `${...}` ports are never scanner-flagged and the remap only touches the modeled field. It binds the original host port in every copy, so parallel scenarios — and a `--keep` copy left running — **collide on it**. This is the real trap; it looks like a flaky "port already allocated" that only appears under `--parallel` or alongside a kept run. The fix is to declare that var as `env.vars: { DB_PORT: auto }` in the scenario — the runner then allocates a fresh port for it from the same batch as the modeled ports. (`services.<name>.ports` can't take a `${var}` — it's a strict int rejected at config load — so a var-routed compose port is exactly the case `env.vars: auto` exists for.)

The rule of thumb: model every host port under `services.<name>.ports` so isolation is automatic; if a port must reach compose through a var instead, it MUST be an `env.vars: auto` entry per scenario or it will collide.

Two isolation gotchas that **block** a run (the compose isolation scanner fails the scenario before deploy):

- **`container_name:` in raw compose** — collides directly with the working env. Drop it; compose already names containers from project + service.
- **A literal host port** (`8080:8080`) in raw compose — bypasses both the remap and `ports_free`, and the scanner flags **any** literal host-port token regardless of your service config. Merely adding a `services.<name>.ports` entry while `8080:8080` stays literal in compose does NOT clear the finding or move the bind. Fix it by **replacing the literal with an interpolated token** (`${APP_PORT}`) sourced from a modeled port — a `services.<name>.ports.<x>` value surfaced via an `exports.env` rule (`from: services.<name>.ports.<x>`) — or by routing the compose interpolation through a var and setting `env.vars: { …: auto }` per scenario. (`${...}`-interpolated ports are never flagged.)

`external:` / explicitly-`name:`d volumes/networks are warnings only. `--skip-isolation-check` downgrades blocking findings — a last resort for false positives, not a fix. Scanner detail → `dwe docs show config/tests#compose-isolation-scanner --lang en`.

**The remap changes the Host/URL the app sees.** An assertion hits the app on a freshly-allocated non-default port, so anything with **host-dependent logic** — multisite/domain routing, admin routing, CORS, signed/absolute URLs — can behave differently through a random port than prod-style access on the canonical one. Before writing an `http_check` against anything beyond a trivial health path, confirm the app actually tolerates a non-standard port in `Host` — not merely that nginx routes it. This is *not* a dwe bug; it's an inherent consequence of port isolation.

## 7. Test-only commands via `type: command`

A `type: command` step dispatches through the regular command registry **including `private` commands** — so keep test-only helpers (seed fixtures, dump to a fixed filename) out of the everyday listing with `private: true`, not `hide` (pipelines skip `hide` commands):

```yaml
steps:
  - { name: "create dump", type: command, cmd: db.dump }   # a private command
  - name: "dump exists"
    type: builtin
    cmd: file_exists
    with: { path: "dumps/db-latest.sql.gz" }
```

Author the `private` command in `workspace/commands/**.yml` → see `authoring-commands.md` (§ 6 `private:`). Command schema → `dwe docs show config/commands/index --lang en`.

## 8. Validate first (read, free)

`dwe validate tests` statically checks every `workspace/tests/*.yml` — name, `timeout:` parse, `env.services` references, step schema + builtin `with:` params + `when:`, `type: command` IDs — **without touching Docker**. Cheap; run it yourself while authoring and before either running the scenario or handing it over:

```shell
dwe validate tests --output json
```

It also surfaces compose-isolation hazards — but **all** of them report as `"severity":"warning"` here, including the ones that will actually **block** `dwe test run` (`container_name:`, literal host ports). Don't judge a finding's blocking power by the severity field in `validate tests`; cross-reference the blocking/warning split in § 6 (or `dwe docs show config/tests#compose-isolation-scanner --lang en`). Caveat: a var populated only post-deploy (a `${generated.*}` secret) is absent at validate time and may draw a spurious diagnostic — give it a project-level default.

## 9. The debugging loop + handoff

When a scenario fails (deploy / step / timeout), the runner collects a **failure report** into `.dwe/tests/reports/<scenario>/` **before** teardown, so it survives — read those directly (safe, plain files):

- `pipeline.log` — the copy's deploy/steps log
- `compose-ps.txt` — `docker compose ps --all` in the copy
- `container-logs.txt` — combined container logs (last 200 lines each)

When the report isn't enough, `dwe test run --keep <scenario>` skips teardown and leaves the live copy to inspect (it prints the compose project name + copy path; a kept run blocks a re-run until cleaned). `--keep` does not change the gate in § 1 — it makes the run *more* persistent, so a scenario you may run unattended you may also `--keep`, and one you must hand over you hand over with the `--keep` flag included. **For a brand-new, unproven scenario, use or suggest `--keep` on the very first run** — if it fails, the copy is already there to debug, instead of paying a second full deploy cycle (often 8–10 min) just to obtain one. Whoever ran it, remember the copy stays until `dwe test clean`.

**`dwe test run -v` / `--debug` propagates into the copy's `dwe validate` + `dwe deploy run`.** The diagnostic level flows through to the subprocesses the scenario actually exercises, so the deploy firehose (command echoes with `-v`; probes/timings/compose env with `--debug`) goes to stderr live in the default sequential text mode, and to the copy's run log (`.dwe/tests/runs/<scenario>/.dwe/logs/test.log`) in `--parallel`/`--output json` mode. Reach for this before `--keep` when the question is *why did the deploy inside the copy do X* rather than *what state did it leave behind* — no need to hand-re-run `dwe deploy run` inside the copy to see the trace.

**Trap — the copy is itself a valid dwe project.** It carries its own `workspace.yml`, so any *project-level* command (`dwe test clean`, `dwe status`, `dwe deploy run`, `dwe vars …`) run while your shell is *inside* `.dwe/tests/runs/<scenario>/` resolves **that copy** as a separate project root — with its own empty `.dwe/tests/manifests/` — and returns **silent empty results, not an error** (e.g. `dwe test clean` from inside the copy sweeps nothing and looks like a bug). `cd` back to the real project root before any project-level command; from within the copy only *read* its plain files (`.dwe/logs/`, `docker compose ps`).

Clean up kept or crashed/interrupted runs with `dwe test clean` (`--dry-run` first, read-safe; the real sweep is a handoff — manifest-driven, never guesses at names; compose projects with no manifest are only reported, remove those by hand).

Command table. `test clean` is always a handoff; the two `test run` forms go through the § 1 gate — clear it and run, otherwise edit yml → show diff → give the exact command → wait:

| Goal | Command |
| --- | --- |
| Run a scenario / the whole suite | `dwe test run [scenario...]` (`--parallel N`, `--timeout 15m`) |
| Inspect the live env after a failure | `dwe test run --keep <scenario>` |
| Remove a kept or crashed run's copy | `dwe test clean [scenario...]` |

Exit codes are CI-friendly: `0` all passed, `1` a scenario failed, `2` a scenario couldn't be prepared (bad name, held lock, kept prior run). Full CLI surface → `dwe docs show config/tests --lang en`.

Cross-links: `pipelines-and-orchestration.md` (§ 2 — the deploy step schema `steps:` reuses, builtins, `when:` conditions), `authoring-commands.md` (the `private` `type: command` a scenario calls), `render-and-vars.md` (`${generated.*}` secrets and the `vars` sandbox `env.vars` overrides).

# Daily Workflow

After [joining a project](joining-a-project.md) and getting a green first deploy, your day-to-day with DWE settles into a small handful of commands. This guide walks through them in roughly the order you'll reach for them: bring the stack up, check status, toggle services, drop into a shell, run a project command, tail logs, stop or restart.

## Bringing the stack up

The first thing each day is to start the stack:

```shell
dwe run
```

This starts every enabled service via Docker Compose, wrapping the `docker up` + health-wait sequence with an optional git update probe and any before/after-run hooks. A deploy produces a known-good state on disk; `dwe run` is what actually brings the containers up.

`dwe run` gates on a green deploy: if a tracked service has never been deployed, it stops with "run `dwe deploy run` first" rather than starting against a half-provisioned environment. To skip the git update probe:

```shell
dwe run --no-update
```

`dwe stop` and `dwe restart` are the bookends — covered in [stop and restart](#stop-and-restart) below.

## Quick status

```shell
dwe status
```

The default view prints stack health and every section in order: apps, tools, infra, deploy, topology, git workspace, daemons. Each section is also addressable directly, which is handy when you only care about one slice:

```shell
dwe status apps
dwe status deploy
dwe status daemons
```

To shrink the default view, pass `--no-<section>` flags — e.g. `dwe status --no-git --no-topology` skips the two slowest sections when you just want a quick container check.

`dwe status` is read-only and runs without locks, so it's safe to call while a deploy or restart is in progress. For the project's printable front page (URLs, host aliases, credentials) see `dwe info` — covered in [joining a project](joining-a-project.md#the-info-dashboard).

## Toggling optional services

Most projects ship a few optional services — a metrics dashboard, a second worker, a database admin UI. They're declared `enabled: false` in `workspace/defaults.yml` and you opt in per-machine.

Interactive multi-select:

```shell
dwe services
```

This opens a checklist of optional services. Submit to write your selection to `workspace/local.yml` and regenerate `.env`.

CLI form:

```shell
dwe services enable adminer
dwe services disable second
```

By default, toggling only writes `local.yml` and records a **pending operation**. The container is not started or stopped yet — `dwe status` will surface the pending change. To execute the lifecycle steps immediately, pass `--apply`:

```shell
dwe services enable adminer --apply
```

Otherwise, the next `dwe deploy` (or `dwe deploy run --service adminer`) is what brings the new selection into effect. The pending model exists so you can batch several toggles and apply them once.

Reference: [`../reference/config/state/index.md`](../reference/config/state/index.md) for the pending-state mechanics, [`../reference/config/services/index.md`](../reference/config/services/index.md) for the schema.

## Shell access

```shell
dwe shell <service>
```

Drops you into an interactive shell inside the named container. With no argument, the command auto-picks the only enabled service or shows a selector if there are several.

The mode controls how the shell is opened:

| Mode | Behavior |
|------|----------|
| `auto` (default) | `docker exec` if the container is running; `compose run --rm` if it is not; error if it is stopped. |
| `exec` | Always `docker exec` — errors if the container is not running. |
| `run` | Always start a fresh container via `docker compose run --rm`. Use this when you want a clean shell without touching the running one. |

```shell
dwe shell main --mode run --shell sh
dwe shell main --root
dwe shell main --user deploy --workdir /app
```

For one-shot dispatch (run a single command and exit with its exit code), use `-c`:

```shell
dwe shell main -c "composer install"
dwe shell main -c "php artisan migrate" --mode run
```

A TTY is allocated only when both stdin and stdout are terminals, so piping works cleanly (`dwe shell main -c "ls -la" | grep ...`). The shell binary, user, working directory, and env defaults come from each service's `cli:` block in `service.yml` — flags override per invocation.

## Running project commands

Projects declare their own operations under `workspace/commands/` — database seeds, cache flushes, environment setup, deployments. Each one becomes a callable command:

```shell
dwe commands              # interactive selector over all public commands
dwe commands list         # plain listing
dwe commands db.seed      # run directly
dwe cmd db.seed           # `cmd` is the short alias
```

Pass parameters with `--set key=value`:

```shell
dwe cmd db.seed --set env=staging --set count=10
```

For scripted use:

- `-y` / `--yes` skips confirmation prompts (including any `confirmation: true` declared on the command).
- `--silent` suppresses the end-of-run desktop notification for `notify: true` commands.
- `-i` / `--inspect` prints the resolved command definition instead of running it — useful for verifying what `--set` and templating will produce before executing.

```shell
dwe cmd db.seed --yes --silent
dwe cmd db.seed --inspect
```

Reference: [`../reference/config/commands/index.md`](../reference/config/commands/index.md), [`../reference/config/commands/types.md`](../reference/config/commands/types.md).

## Viewing logs

```shell
dwe logs              # whole stack (all enabled services)
dwe logs <service>    # one service
```

With no argument, `dwe logs` streams the whole stack — every enabled service, multiplexed and prefixed, like `docker compose logs`. With a service name it streams that one service's container. By default the last 50 lines print and the command exits. To follow:

```shell
dwe logs --follow
dwe logs main --follow
dwe logs main --tail 100 --follow
dwe logs main --since 5m
```

The target container is found by its compose project + service labels, so logs work regardless of any `container_name` override or compose's default `<project>-<service>-<index>` naming — you do not need to pin `container_name` in your compose file.

`Ctrl-C` exits the log stream; the stack keeps running. The `--since` flag accepts a duration (`5m`, `1h`) or an RFC3339 timestamp. With `--output json`, output is NDJSON (one `{"ts","stream","msg"}` per line; whole-stack mode adds a `"service"` field) for piping into log tooling.

`dwe logs` is read-only and lock-free, like `dwe status`.

## Re-checking project state

For URLs, host aliases, and the project's printable "where is everything" dashboard, see [joining a project → the info dashboard](joining-a-project.md#the-info-dashboard) — `dwe info` is the same command in daily use.

## Stop and restart

```shell
dwe stop
dwe restart
```

`dwe stop` runs the full stop lifecycle: before-stop hooks → `docker compose down` → after-stop hooks. Daemons declared by user commands are auto-reaped (no opt-out). `dwe restart` chains a full stop with a `run` leg, skipping the git update probe.

Per-service variants:

```shell
dwe stop main
dwe restart main
```

These bypass compose and lifecycle hooks — they shell out directly to `docker stop` / `docker restart` for that one container. The single-service form works even after the service has been disabled, which is occasionally what you want when cleaning up a freshly-toggled service. Daemons are not auto-reaped on per-service stop/restart.

For a bare compose stop without hooks at all, the lower-level commands stay available: `dwe docker stop` (stop containers in place) and `dwe docker down` (stop and remove). These are escape hatches; in normal use `dwe stop` and `dwe restart` are what you want.

## Where to next

- [`troubleshooting.md`](troubleshooting.md) — what to do when one of the commands on this page returns an unhappy answer.
- [`switching-tasks-with-snapshots.md`](switching-tasks-with-snapshots.md) — pause one task, swap to another, swap back without losing state.

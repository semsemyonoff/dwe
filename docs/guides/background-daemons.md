# Background daemons

Some processes do not fit the request-response shape your app container handles all day. A Laravel queue worker, a file watcher rebuilding assets, a websocket bridge, a scheduled-task runner — each needs to keep running between developer commands, survive `dwe shell` exits, and be visible enough that no one forgets it is there. `type: daemon` is the declarative shape for these.

One YAML block expands at registry-load time into four virtual commands (`.start`, `.logs`, `.stop`, `.restart`), each appearing in `dwe commands list`, the interactive browser, completion, and as a step target inside workflows. Daemon containers are tracked through standardised docker labels — there is no separate state file — and they are auto-stopped when the stack stops.

Full schema lives in [`../reference/config/commands/types.md#type-daemon`](../reference/config/commands/types.md#type-daemon); this page covers the authoring patterns you'll reach for.

## Anatomy of a daemon block

```yaml
# workspace/commands/queue.yml
commands:
  queue:
    type: daemon
    description: Laravel queue worker
    service: app-main             # literal compose service name (no ${...})
    workdir_from: services.main.work_dir_internal
    user: www-data
    env:
      QUEUE_CONNECTION: redis
    params:
      name:
        default: default
        pattern: ^[a-zA-Z0-9_-]+$
    argv:
      - php
      - artisan
      - queue:listen
      - --timeout=0
      - --queue=${param.name}
    daemon:
      container_template: "php_queue_${param.name}"
      on_already_running: error
      auto_remove: true
      stop_timeout: 10s
```

Three things make this a daemon and not a regular `service_run`:

- **`service:` must be literal.** Templating (`${...}` / `{{...}}`) is rejected — the `dwe.daemon.id` label has to stay stable across restarts so completion, status, and auto-reap can correlate state.
- **`argv:` (or `cmd:`) is the long-running process.** No timeout — this is what gets PID 1 in the container.
- **The `daemon:` block** declares the container-name template and lifecycle defaults.

`service`, `workdir`/`workdir_from`, `user`, `env`, `params`, `argv`, `compose_args` follow the same semantics as `type: service_run`. The source `queue` ID is **not** runnable on its own — only the four virtual commands are.

## The four virtual commands

| Virtual ID | What it does | Notes |
|---|---|---|
| `queue.start` | `docker compose run -d --name <full> ... <argv>` | Detached; `--no-deps` keeps the rest of the stack untouched. |
| `queue.logs` | `docker logs -f --tail=100 <full>` | Foreground; Ctrl-C detaches but the container keeps running. |
| `queue.stop` | `docker stop -t <stop_timeout> <full>` | Idempotent — missing container is not an error. |
| `queue.restart` | `queue.stop` then `queue.start` | Implemented as a virtual workflow that forwards every declared param. |

End-to-end usage:

```bash
# Start the worker
dwe cmd queue.start

# Tail it (Ctrl-C detaches, container stays running)
dwe cmd queue.logs

# What's running right now?
dwe status daemons

# Restart after editing config
dwe cmd queue.restart

# Stop just this daemon
dwe cmd queue.stop
```

## Multi-instance daemons via `params:`

The example above has one declared param (`name`), so the same daemon definition can drive arbitrarily many container instances — one per queue, one per worker pool, one per file-watcher root.

```bash
# Three workers, one per queue
dwe cmd queue.start --set name=emails
dwe cmd queue.start --set name=webhooks
dwe cmd queue.start --set name=video

dwe status daemons
# php_queue_emails    running   2m
# php_queue_webhooks  running   2m
# php_queue_video     running   1m

# Restart only the video one
dwe cmd queue.restart --set name=video

# Stop everything in one go — see "Auto-reap on dwe stop" below
dwe stop
```

The `params:` declaration drives both the rendered container name (via `${param.name}` in `container_template`) and the `argv:` line. Two requirements:

1. Every `${param.X}` referenced in `container_template` must be declared in `params:` **and** carry a `pattern:` regex. The pattern is advisory; the authoritative defence is the post-render regex on the full container name (`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).
2. The rendered container name is `<project.full>-<rendered template>`, so it stays unique across multiple checkouts of the same project on one machine.

## Daemon-block fields

| Field | Default | Effect |
|---|---|---|
| `container_template` | — (required) | Template for the container's name. Rendered against the command template space, then prefixed with `<project.full>-`. |
| `on_already_running` | `error` | `error` aborts `.start` if the container exists; `noop` makes `.start` idempotent (useful for scripts that run "start if needed" patterns). |
| `auto_remove` | `true` | When true, `.start` adds `--rm` so the container is removed when it stops (no zombie stopped containers cluttering `docker ps -a`). |
| `stop_timeout` | `10s` | Time `docker stop` waits before SIGKILL. Duration string; sub-second values round up to 1s. |

If your daemon needs a graceful-shutdown window longer than the default 10s (e.g. finish-current-job semantics for queue workers), bump `stop_timeout`:

```yaml
daemon:
  container_template: "php_queue_${param.name}"
  stop_timeout: 60s
```

## Visibility: labels and `dwe status daemons`

Every daemon container carries three standard labels so `docker ps` is the single source of truth:

- `dwe.project=<project.full>` — scopes to this project.
- `dwe.daemon.id=<base>` — the source command ID (e.g. `services.main.queue`).
- `dwe.daemon.params=<json>` — the params used at start time (e.g. `{"name":"emails"}`).

`dwe status daemons` filters on these labels and shows each running daemon, its container name, the daemon ID, the params used to start it, and uptime. Use it whenever you wonder "did I forget to stop something?" — for example, after restarting the stack with `dwe restart` (see the next section).

## Auto-reap on `dwe stop`

Whenever `dwe stop` runs, a synthetic `_auto_reap_daemons` phase is prepended to the stop pipeline. It enumerates every container labelled `dwe.project=<full>` with a non-empty `dwe.daemon.id` and stops them in parallel. **There is no opt-out** — daemons always shut down with the stack. The phase appears in the live stop-pipeline output when `dwe stop` runs (`dwe stop` has no plan/preview mode).

`dwe restart` is a `dwe stop` followed by `dwe run`. The stop leg reaps every daemon as usual, but the run leg does **not** restart them — daemons are intentionally separate from the main lifecycle and not auto-started on `dwe run`. If you need them back after a restart, call `<id>.start` explicitly:

```bash
dwe restart
dwe cmd queue.start --set name=emails
dwe cmd queue.start --set name=webhooks
```

If you find yourself doing this every time, wrap it in a workflow command:

```yaml
commands:
  workers.up:
    type: workflow
    description: Start all queue workers
    steps:
      - command: queue.start
        with: { name: emails }
      - command: queue.start
        with: { name: webhooks }
      - command: queue.start
        with: { name: video }
```

## Security: keep secrets out of `params:`

Param values land in the `dwe.daemon.params` label as JSON, which `docker inspect` exposes to anyone with docker socket access on the host. **Never put secrets in `params:`.**

For tokens, passwords, and other sensitive values, use `env:` — env values are passed through the container environment (via `-e KEY` plus the value in the host process's `cmd.Env`), never through the host argv, so they do not appear in `ps`, `/proc/<pid>/cmdline`, or container labels.

```yaml
commands:
  queue:
    type: daemon
    service: app-main
    params:
      name:                       # OK — queue name is not a secret
        default: default
        pattern: ^[a-zA-Z0-9_-]+$
    env:
      QUEUE_TOKEN: ${vars.secrets.queue_token}   # OK — values flow through env, not labels
    argv:
      - php
      - artisan
      - queue:listen
      - --queue=${param.name}
    daemon:
      container_template: "queue_${param.name}"
```

The same rule applies in reverse: don't reach for `env:` to disambiguate multiple instances — env values don't end up on the container name, so two daemons with the same `container_template` and different `env:` values would collide. Use `params:` for things that need to be part of the identity (queue name, watch root, pool size); use `env:` for things the process needs to function (credentials, connection strings, feature flags).

## Cross-links

- [`../reference/config/commands/types.md#type-daemon`](../reference/config/commands/types.md#type-daemon) — full schema, validation rules, labels, virtual command implementation.
- [`author-project-commands.md`](author-project-commands.md) — the other command types (`shell`, `service_exec`, `workflow`) that wrap daemons.
- [`daily-workflow.md`](daily-workflow.md) — `dwe status` and the stop/restart flow.

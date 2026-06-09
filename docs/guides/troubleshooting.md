# Troubleshooting

Your stack stopped working, a deploy hangs, or `dwe status` shows red where it used to be green. This guide is a triage map: where to look first, which command answers which question, and which escape hatches exist when the normal path is jammed.

## First-look triage

Three commands cover ninety percent of the picture and none of them mutate state:

```shell
dwe validate
dwe status
dwe logs <service>
```

- `dwe validate` aggregates every static check — environment probes, config schema, translations, and project-defined preflight checks. Run it first; if it is red, fix that before chasing anything else.
- `dwe status` reports container health, deploy state, git workspace state, and any pending service toggles waiting on a deploy. See [`daily-workflow.md`](daily-workflow.md#quick-status) for the section toggles and shortcuts.
- `dwe logs <service>` tails Docker logs for one container. See [`daily-workflow.md`](daily-workflow.md#viewing-logs).

If `dwe validate` is green and `dwe status` shows a specific failure, jump to the section below that matches.

## "Port already in use"

The host port DWE wants to publish is held by another process — another DWE project, a local dev server, anything bound to that port.

Diagnose first:

```shell
dwe validate env
```

The `env.ports_free` probe lists each conflict with the offending port number. To remap, override the port in `workspace/local.yml` (machine-local, gitignored). The overlay deep-merges entry-by-entry, so you only restate the ports you are changing:

```yaml
# workspace/local.yml
services:
  main:
    ports:
      http: 18080   # was 8080
```

Then re-deploy (or `dwe deploy run --service main`) to push the change through compose and refresh `dwe info`. Reference: [`../reference/config/services/fields.md`](../reference/config/services/fields.md).

## "Docker not running"

`dwe validate env` is again the first stop. Two probes matter here:

- `env.docker_bin` — the `docker` binary is not on `PATH` or is unreadable.
- `env.docker_daemon` — the binary exists but `docker info` cannot reach a daemon (Docker Desktop not started, socket permissions, remote context unreachable).

Start Docker Desktop (or `systemctl start docker`, depending on platform), then re-run `dwe validate env`. If the daemon is on a non-default socket, set `DOCKER_HOST` in your shell or via `workspace/local.yml`. Reference: [`../reference/config/validate.md`](../reference/config/validate.md).

## "Container won't come up"

The deploy finished, but a service is unhealthy or restarting. Three commands narrow it down:

```shell
dwe logs <service>            # what the container is actually saying
dwe compose argv up <service>  # the exact compose argv DWE will/did invoke
dwe compose files              # the active compose file list (overlays included)
```

Logs answer "why did the process exit?". The two `compose` diagnostics answer "did DWE assemble the right compose configuration?" — useful when a local overlay or an unexpected `extends:` ancestor is silently changing what Docker sees. Reference: [`../reference/config/docker.md`](../reference/config/docker.md).

## "Deploy keeps failing"

Three sub-questions, three commands:

```shell
dwe deploy plan               # resolved step list for the current state
dwe deploy state show         # journal: what succeeded, what failed, when
dwe deploy state clear        # discard the journal and force a full re-run
```

`dwe deploy plan` shows the deploy DWE would actually run right now, including which steps would be skipped due to `config_hash` matches or unchanged inputs. If a step you expect to run is being skipped, the journal explains why.

`dwe deploy state show` prints the recorded outcome of the last attempt — per-step status, error excerpts, and the recorded config hash. `dwe deploy state clear` deletes `.dwe/deploy/state.yml` so the next `dwe deploy` re-runs every step. Use this when you suspect the journal itself is wrong, not the project. Reference: [`../reference/config/state/management.md`](../reference/config/state/management.md), [`../reference/config/state/hashing.md`](../reference/config/state/hashing.md).

## "Pulled a teammate's branch — deploy says no changes but my service is broken"

Symptom: you checked out a branch that changes deploy steps, ran `dwe deploy`, and DWE skipped most of it claiming nothing changed. Or it ran but the running container still behaves like the previous branch.

The cause is almost always a stale deploy journal: `.dwe/deploy/state.yml` records a `config_hash` that matched the previous checkout, and the skip decider trusts it. The fix is to clear the journal and re-run:

```shell
dwe deploy state clear
dwe deploy
```

If a single service is the issue, scope the re-run:

```shell
dwe deploy run --service <name>
```

This also handles the case where you swapped between branches that toggle different optional services — the journal does not know your `local.yml` shifted underneath it.

## "Nuclear option"

When the stack is wedged badly enough that step-by-step diagnosis is more expensive than starting over:

```shell
dwe reset run
```

`dwe reset run` stops every container, removes them, and runs the project's reset pipeline (`workspace/reset.yml`). What survives a reset is project-defined — typically the Docker named volumes with your databases and caches stay, the journal and runtime state do not. Read the project's `reset.yml` (and `dwe reset plan` for the resolved step list) before assuming what it preserves.

If you also want to wipe stateful data, opt in explicitly. The project's reset pipeline may expose a `docker_remove_project_volumes` step — check `dwe reset plan` — or you can remove the Docker named volumes by hand after a clean stop.

**Before any destructive reset, snapshot.** Even a one-line `dwe snapshot create pre-reset` gives you a rollback if the reset is more aggressive than expected. See [`switching-tasks-with-snapshots.md`](switching-tasks-with-snapshots.md) for the snapshot surface.

Reference: [`../reference/config/reset.md`](../reference/config/reset.md).

## Escape hatch: `dwe compose raw`

When DWE's compose wrapper is in the way — you need a flag DWE does not pass, or you want to verify whether the problem is DWE or compose itself:

```shell
dwe compose raw -- ps -a
dwe compose raw -- exec main env
dwe compose raw -- config
```

`dwe compose raw` is a low-level pass-through: DWE resolves the compose file list and project name, then hands the rest of the argv to `docker compose` unchanged. No policy args, no overlays beyond the ones already on disk. Use it as a diagnostic, not a daily-driver — the higher-level `dwe` commands exist for a reason — but it is the right tool when you are debugging DWE itself or reproducing an issue against the compose CLI directly. Reference: [`../reference/config/docker.md`](../reference/config/docker.md).

## Verbose & debug output

When the normal output does not tell you *why* DWE did something — which docker command actually ran, why a step was skipped, what the engine decided — turn on a diagnostic channel. Two orthogonal flags control it, and both write **only to stderr**, so stdout (including `--output json`) stays a clean machine-readable contract.

```shell
dwe run -v          # verbose: echo commands + key decisions
dwe run --verbose   # same
dwe run --debug     # firehose: everything -v shows, plus the internals
DWE_DEBUG=1 dwe run # env equivalent of --debug
```

`-v, --verbose` echoes the commands DWE executes (docker/compose lifecycle, raw `docker stop/restart/rm`, `sh -c …`, nested `dwe …`, git) each as a copy-pasteable `$ …` line, plus the key pipeline **decisions**: which step ran or was skipped and why (`when:` results, phase gates, `state: already deployed`, files-gate), and the preflight pass/fail summary.

`--debug` (or `DWE_DEBUG=1`) is a **superset** of `-v`. On top of the verbose stream it adds the firehose: read-only docker probe commands (`docker compose ps`), subprocess timings and exit codes, full compose environment overrides and working directory, config-resolution internals, and everything emitted through `log/slog` at Debug level. `--debug` installs the slog Debug handler; `-v` does not. When both the flag and the env var disagree, the flag wins; `DWE_DEBUG=0` (or `false`/`no`/`off`/empty) is treated as off.

The two are designed to combine cleanly with everything else:

- **Read-only probes stay out of verbose.** `dwe status -v` does *not* spam `docker compose ps` — those probes are Debug-only. Use `dwe status --debug` if you want to see them.
- **JSON stays clean.** `dwe status -v --output json | jq .` parses: the JSON document is the only thing on stdout, every diagnostic line is on stderr. The same holds for `--debug`. If a command errors, the `{"error":{…}}` envelope is still the final structure on stderr.
- **Zero overhead when off.** With neither flag set, there is no diagnostic output, no slog handler is installed, and existing `Warn`/`Error` behavior is unchanged.

Where to look: redirect stderr to a file to keep the diagnostics separate from normal output —

```shell
dwe run --debug 2>debug.log     # diagnostics in debug.log, stdout untouched
dwe deploy -v 2>&1 | less       # interleave both streams in a pager
```

## Where to next

- [`daily-workflow.md`](daily-workflow.md) — the everyday commands these sections cross-link to.
- [`switching-tasks-with-snapshots.md`](switching-tasks-with-snapshots.md) — checkpoint before a risky reset, restore after.

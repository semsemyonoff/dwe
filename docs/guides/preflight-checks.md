# Preflight checks

A deploy that fails three minutes in because `ghcr.io` rejected the pull is worse than one that refuses to start at all. Preflight checks are the readiness gate the CLI runs before any deploy step, container start, or stop touches your machine — they answer "is the world ready?" and abort with a useful diagnostic if it isn't.

DWE ships seven hardcoded environment probes (docker / git / shell / ports) plus a declarative `workspace/validate.yml` where you add the project-specific ones — "are we logged into the registry?", "is `DATABASE_URL` set?", "is the corporate VPN up?". This guide walks through both layers.

Full schema lives in [`../reference/config/validate.md`](../reference/config/validate.md); this page covers the authoring patterns you'll reach for.

## Built-in `env.*` probes

Seven probes are hardcoded in the CLI and run on every `dwe validate` invocation and every preflight (so you cannot disable them). They cover the host-level prerequisites every DWE project shares:

| Probe | Fails when |
|---|---|
| `env.docker_bin` | `docker` is not on `PATH` (or the configured override is missing). |
| `env.docker_daemon` | The docker daemon socket is not reachable. |
| `env.docker_compose` | The Compose v2 plugin is missing or too old. |
| `env.git_bin` | `git` is not on `PATH`. |
| `env.shell_bin` | The configured shell is missing. |
| `env.project_perms` | Project root is not writable. |
| `env.ports_free` | A host port declared by an enabled service is held by a foreign process or another compose project. (Self-skips on the `stop` stage.) |

You can run them in isolation:

```shell
dwe validate env                # all seven
dwe validate env ports_free     # one probe
```

That's the right first command when something feels off — see [`troubleshooting.md`](troubleshooting.md).

## Declaring a check in `validate.yml`

Project-specific checks go into `workspace/validate.yml`. Each entry has an ID, a description, the stages it runs on, and a body — either a built-in inspection kind or a user command:

```yaml
# workspace/validate.yml
checks:
  - id: ghcr-login
    description: Authenticated against ghcr.io
    stages: [deploy]
    severity: error
    hint: |
      Run `docker login ghcr.io` with a personal access token.
    type: builtin
    cmd: shell
    with:
      cmd: docker pull ghcr.io/owner/private-image:latest --quiet
      timeout: 30s

  - id: project-deps
    description: Required CLIs installed
    stages: [run]
    type: command
    cmd: deps.check
```

Two `type:` values are accepted:

- **`type: builtin`** — dispatches to one of the five built-in check kinds below. The check body lives in `with:`.
- **`type: command`** — dispatches to a declarative user command from `workspace/commands/`. The target must be `type: shell` or `type: script` — workflow, service_exec, and others are rejected at load time. `with:` is passed through as the command's `params:` payload.

The file is optional: when absent, only the `env.*` probes run.

## Built-in check kinds

Five inspection kinds are available under `type: builtin`. All of them are also usable as deploy-step `check:` bodies, so you can reuse the same shape across pipelines.

### `shell`

Runs a one-liner under POSIX `sh -c`. Exit 0 = pass.

```yaml
- id: registry-reachable
  description: ghcr.io reachable
  stages: [deploy]
  type: builtin
  cmd: shell
  with:
    cmd: curl -fsS -o /dev/null https://ghcr.io/v2/
    timeout: 5s
```

The shell is always `sh` regardless of the project's configured shell — keeps checks portable across hosts.

### `file_exists`

Verifies a file is present on disk (relative to project root).

```yaml
- id: db-dump-present
  description: Seed dump exists for first-run import
  stages: [deploy]
  severity: warning
  hint: Download from s3://team-dumps/latest.sql and place at .dwe/seed.sql
  type: builtin
  cmd: file_exists
  with:
    path: .dwe/seed.sql
```

### `executable_in_path`

Verifies a binary resolves via `PATH`.

```yaml
- id: jq-installed
  description: jq available for compose introspection
  stages: [deploy]
  severity: warning
  type: builtin
  cmd: executable_in_path
  with:
    name: jq
```

### `env_keys_present`

Verifies one or more keys exist with non-empty values in a `.env`-style file. `KEY=`, `KEY=""`, and `KEY=''` all count as empty.

```yaml
- id: app-secrets
  description: Required app secrets configured in .env
  stages: [run, deploy]
  severity: error
  hint: |
    Copy .env.example to .env and fill in:
      DATABASE_URL, REDIS_URL, JWT_SECRET
  type: builtin
  cmd: env_keys_present
  with:
    file: .env
    keys: [DATABASE_URL, REDIS_URL, JWT_SECRET]
```

### `tcp_reachable`

Attempts a TCP dial to `host:port`.

```yaml
- id: corporate-vpn
  description: Internal git mirror is reachable (VPN up?)
  stages: [deploy, run]
  severity: error
  hint: Connect to the corporate VPN and retry.
  type: builtin
  cmd: tcp_reachable
  with:
    host: git.internal.example.com
    port: 22
    timeout: 2s
```

## Stages

A check runs whenever the caller's stage is in its `stages:` list. There are four reserved stages with built-in hooks:

| Stage | Triggered by |
|---|---|
| `deploy` | `dwe deploy run`, `dwe validate --stage deploy` |
| `run` | `dwe run`, `dwe restart` (run leg), `dwe validate --stage run` |
| `stop` | `dwe stop`, `dwe restart` (stop leg), `dwe validate --stage stop` |
| `command` | `dwe validate --stage command` (reserved; no automatic hook yet) |

`dwe validate` without `--stage` runs every check regardless of stage. `dwe restart` is composite (it fires both `stop` and `run` legs). `dwe reset run` uses the `stop` stage only.

Typos in `stages:` produce a warning at load time with a near-match suggestion, so `stages: [deplooy]` won't silently never run.

## Service gating

Some checks only make sense when a particular service is enabled — a JWT secret matters only if the API container is on. Use `services:` to OR-gate:

```yaml
- id: api-jwt-secret
  description: JWT_SECRET configured for API
  stages: [run, deploy]
  services: [api]              # only runs when api is enabled
  severity: error
  hint: Set JWT_SECRET in services/api/.env
  type: builtin
  cmd: env_keys_present
  with:
    file: services/api/.env
    keys: [JWT_SECRET]
```

Semantics:

- Omit `services:` → the check always runs (when its stage matches).
- `services: [api]` → runs iff `api` is enabled in the current local config.
- `services: [api, worker]` → runs iff `api` OR `worker` is enabled. All listed services disabled → silent skip.

Service gating and stage filtering are independent AND filters: stage matches first, then the service gate.

## Severity and `--strict`

Each check declares a severity that controls how preflight reacts when it fails:

| `severity:` | Default? | Effect on exit code |
|---|---|---|
| `error` | yes | Preflight fails; deploy/run/stop aborts. |
| `warning` | no | Diagnostic printed; pipeline proceeds. With `dwe validate --strict`, warnings become errors. |
| `info` | no | Diagnostic printed; never blocks. |

`error` is the default — leave it off for the common case. Reach for `warning` when the check surfaces a soft expectation (e.g. "you probably want a seed dump"), and `info` for advisory output.

## Linters (validate-only)

`workspace/validate.yml` also exposes a `linters:` block for running well-known external linters (shellcheck, hadolint) and arbitrary `type: generic` adapters. Linters run on `dwe validate` only — they do **not** run in preflight. Preflight answers "can we run?", not "is the code clean?".

```yaml
linters:
  shellcheck:
    paths: [workspace/scripts, scripts]
    severity: warning
  hadolint:
    paths: ["."]
    filenames: [Dockerfile]
```

Built-in adapters autodetect: if the binary is on `PATH` and matching files exist, the linter runs; otherwise it silently skips. Set `enabled: false` to opt out explicitly. See [`../reference/config/validate.md#external-linters`](../reference/config/validate.md#external-linters) for the full schema.

## Standalone invocation

You don't have to wait for `dwe deploy` to fire a check — you can invoke any of them directly:

```shell
dwe validate                        # every domain (env, config, checks, linters)
dwe validate env                    # only the seven hardcoded env probes
dwe validate checks                 # only declarative checks
dwe validate checks ghcr-login      # one check by ID
dwe validate linters                # only linters
dwe validate linters shellcheck     # one linter
```

`dwe validate checks <id>` also bypasses the `services:` gate, so you can sanity-check a gated entry even when its target services are all disabled.

Useful flags:

- `--stage <name>` — restrict `checks.*` to one stage.
- `--strict` — warnings exit non-zero.
- `--quiet` — hide `ok` / `info` rows.

If a check is firing too often or too eagerly in preflight while you iterate, every lifecycle command accepts `--skip-preflight`. Use it sparingly — it bypasses every probe, including the env ones.

## Cross-links

- [`../reference/config/validate.md`](../reference/config/validate.md) — full schema, validator domains, linter adapter rules.
- [`troubleshooting.md`](troubleshooting.md) — what to do when a probe fails.
- [`author-project-commands.md`](author-project-commands.md) — authoring the user commands that `type: command` checks dispatch to.

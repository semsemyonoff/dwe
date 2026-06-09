# Preflight checks

A deploy that fails three minutes in because `ghcr.io` rejected the pull is worse than one that refuses to start at all. Preflight is the readiness gate DWE runs before any deploy step, container start, or stop touches your machine — it answers "is the world ready?" and aborts with a useful diagnostic if it isn't.

Two layers run on every preflight: seven hardcoded host probes (docker / git / shell / ports — you can't disable them) and the project-specific checks you declare in `workspace/validate.yml`. This guide is the **authoring workflow**; the field-by-field schema — every probe, builtin, stage, and severity — lives in [`../reference/config/validate.md`](../reference/config/validate.md).

## Add your first check

A check is an ID, a description, the stages it runs on, and a body. Drop this into `workspace/validate.yml`:

```yaml
checks:
  - id: ghcr-login
    description: Authenticated against ghcr.io
    stages: [deploy]
    hint: Run `docker login ghcr.io` with a personal access token.
    type: builtin
    cmd: shell
    with:
      cmd: docker pull ghcr.io/owner/private-image:latest --quiet
      timeout: 30s
```

Now `dwe deploy run` refuses to start until the pull succeeds, printing your `hint` if it doesn't. The file is optional — with no `validate.yml`, only the host probes run.

The body is either `type: builtin` — a built-in inspection kind such as `shell`, `file_exists`, `env_keys_present`, `config_keys_present`, or `tcp_reachable` ([full list with parameters](../reference/config/validate.md#available-builtins)) — or `type: command`, which dispatches to a `shell`/`script` user command from `workspace/commands/`.

## Choosing when a check runs

`stages:` decides which lifecycle moment fires the check — `deploy`, `run`, `stop`, or `post-setup` (full table in the [reference](../reference/config/validate.md#stages)). The one worth calling out here:

- A check that depends on a value the **setup wizard** writes into `local.yml` must use `stages: [post-setup]`, not `[deploy]`. A `[deploy]` check also runs at the early pre-wizard gate, where that value isn't set yet — so it would block you before you can even reach the wizard. `post-setup` runs **only** at the final preflight: after the wizard, or immediately before deploy when there's no wizard (e.g. `dwe deploy run`).

`services: [api]` further gates a check to when a named service is enabled (OR semantics across the list). Stage and service gates are independent ANDs — see [service gating](../reference/config/validate.md#service-gating).

## Recipe: require a value before deploy

The wizard asks for a value and writes it to `local.yml`; a `post-setup` check guarantees it's actually set before deploy — and catches it on `dwe deploy run` too, where the wizard never runs:

```yaml
checks:
  - id: db-api-key-set
    description: db.api_key must be set before deploy
    stages: [post-setup]
    hint: Run `dwe deploy` and complete the wizard, or set db.api_key in workspace/local.yml.
    type: builtin
    cmd: config_keys_present
    with:
      keys: [db.api_key]
```

`config_keys_present` reads the **merged config in memory**, so it sees the wizard's `local.yml` write immediately — no dependency on a rendered `.env`. Assert the same dot-path the wizard's `writes:` uses: a top-level namespace like `db.*` or `app.*`. (Per-service secrets cannot live at `services.<name>.env.*` in `local.yml` — keep those in the service's `.env` and assert them with `env_keys_present` instead.) Details: [`config_keys_present`](../reference/config/validate.md#config_keys_present).

## Running checks on demand

You don't have to wait for `dwe deploy` to fire a check:

```shell
dwe validate                     # every domain (env, config, checks, linters)
dwe validate env                 # only the seven host probes
dwe validate checks              # only declarative checks
dwe validate checks ghcr-login   # one check by ID (also bypasses the services gate)
dwe validate --stage deploy      # only checks bound to a stage
```

Useful flags: `--strict` (warnings exit non-zero), `--quiet` (hide ok/info rows). While iterating, every lifecycle command accepts `--skip-preflight` to bypass the gate entirely — use it sparingly, it skips the host probes too.

`dwe validate env` is the right first command when something feels off — see [`troubleshooting.md`](troubleshooting.md).

> `validate.yml` also has a `linters:` block (shellcheck / hadolint / custom adapters). Linters run on `dwe validate` only — **never** in preflight, which answers "can we run?", not "is the code clean?". Schema: [external linters](../reference/config/validate.md#external-linters).

## Cross-links

- [`../reference/config/validate.md`](../reference/config/validate.md) — full schema: every probe, builtin, stage, severity, and the `linters:` block.
- [`troubleshooting.md`](troubleshooting.md) — what to do when a probe fails.
- [`author-project-commands.md`](author-project-commands.md) — authoring the user commands that `type: command` checks dispatch to.

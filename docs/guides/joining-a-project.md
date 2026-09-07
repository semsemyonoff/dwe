# Joining a DWE Project

You just cloned a repository that ships a `workspace.yml` at the top, and the README told you to "run `dwe`". This guide walks through what to do next, what each first command tells you, and what state DWE leaves on your disk along the way.

## Prerequisites

DWE itself is a single binary; the local stack it manages is Docker-based. Before the first run, confirm:

- The `dwe` binary is on your `PATH` — `dwe --version` prints a version string.
- The Docker daemon is reachable — `docker info` returns without an error.
- The repository contains a `workspace.yml` at its root (the project marker).
- If the project commits encrypted secrets (a `secrets.recipient` key in `workspace.yml`), you also need the project's private age identity. Ask a teammate for it — they hand it over with `dwe secrets key export`. `dwe secrets status` tells you whether this machine can already read the encrypted values.

If any of these are wrong, `dwe validate` will tell you exactly what is missing. Otherwise, you can keep reading.

## First-run sanity check

Inside the project root, run:

```shell
dwe validate
```

`dwe validate` aggregates every static check: environment probes (docker daemon, docker bin, git, shell, ports), config schema (every YAML file under `workspace/`), translations, and any project-defined preflight checks. A green run means the project is internally consistent and your machine satisfies its baseline expectations.

If you want a quick narrative summary instead, run `dwe` with no arguments — that prints the project header and current status without performing actions. See [`../reference/config/validate.md`](../reference/config/validate.md) for the full validation surface and severity model.

## First deploy

The deploy pipeline is what takes a fresh checkout to a running stack. From the project root:

```shell
dwe deploy
```

What happens, in order:

0. **Key onboarding** (only when the project has encrypted secrets and this machine has no identity for them). At a terminal, `dwe deploy` offers to take the key before anything else runs; accepting opens a hidden prompt, stores the identity at `~/.config/dwe/keys/<recipient>.key` and continues the deploy in the same invocation. Declining ends the command right there with the fix instruction. Running without a terminal, with `--output json`, or under `DWE_NONINTERACTIVE=1` skips the offer entirely, and the deploy then stops at the `secrets.unresolved` preflight gate naming the values it cannot read. `dwe run` and `dwe restart` make the same offer. See [Encrypted secrets](../reference/config/secrets.md).
1. **Setup wizard** (only on first run). The project may declare prompts in `workspace/setup.yml` — port conflicts, choice of optional services, license keys, anything the maintainer flagged as machine-local. Your answers land in `workspace/local.yml` (gitignored).
2. **Preflight**. The same `validate` checks run as a gate; failures abort here instead of mid-deploy.
3. **Deploy steps**. The project's `workspace/deploy.yml` (and per-service `deploy.yml` files) execute in order — building images, pulling dependencies, seeding databases, generating template-pack outputs (IDE config, AGENTS.md, gitignored helpers).
4. **Journal write**. DWE records the deploy result and a `config_hash` to `.dwe/deploy/state.yml` so subsequent deploys can skip steps that have not changed.

To preview what will run without doing it, use `dwe deploy plan`. To inspect the journal afterward, `dwe deploy state show`.

References: [`../reference/config/setup.md`](../reference/config/setup.md), [`../reference/config/deploy/index.md`](../reference/config/deploy/index.md), [`../reference/concepts/getting-started.md`](../reference/concepts/getting-started.md).

## The info dashboard

```shell
dwe info
```

`dwe info` is the project's printable front page. It shows the project header (branded by `workspace/styles.yml`), URLs and host aliases for every enabled service, and any sections the maintainer added in `workspace/info.yml` — credentials, links to internal tools, "where to find X", and so on.

URLs and hosts come from two sources: explicit entries in `info.yml`, and auto-blocks (`type: auto-urls` / `type: auto-hosts`) that expand from each service's declared `ports:` / `hosts:`. If you remap a port in `workspace/local.yml`, the dashboard reflects it on the next render.

Reference: [`../reference/config/info.md`](../reference/config/info.md), [`../reference/config/styles.md`](../reference/config/styles.md).

## Running the stack

After a successful deploy, the containers are not necessarily started — `deploy` is about producing a known-good state on disk, not running services. To bring the stack up:

```shell
dwe run
```

This starts every enabled service via Docker Compose. `dwe status` shows what is running, what failed, and aggregate stack health. `dwe logs <service>` tails a single service's logs (Ctrl-C exits; stack keeps running).

To stop everything, `dwe stop`. To restart, `dwe restart`.

See [`daily-workflow.md`](daily-workflow.md) for the full day-to-day surface.

## What now lives on disk

A first deploy creates several gitignored directories at the project root:

| Path | Owner | Purpose |
|------|-------|---------|
| `.dwe/` | DWE | Deploy journal, project lock files, prompt cache, internal state. Safe to delete; DWE rebuilds it on next run. |
| `snapshots/` | DWE | Saved checkpoints created via `dwe snapshot create`. Empty on a fresh project. |
| `backups/` | You / project commands | Database and other dumps produced during development. |
| `workspace/local.yml` | You | Machine-local overrides (ports, hosts, enabled services). Edited by the setup wizard and by `dwe services enable/disable`. |

Stateful service data (databases, caches, message queues) lives in Docker-managed named volumes, not in a project-root folder. **It contains your data** — remove it with care (see `docker volume ls` / `dwe reset`).

Everything under `workspace/` *except* `local.yml` is checked into git and shared with the team. Everything else in the table above stays on your machine.

## Where to next

- [`daily-workflow.md`](daily-workflow.md) — the commands you'll reach for every day (status, logs, shell, project commands).
- [`troubleshooting.md`](troubleshooting.md) — when something stops working, here is where to look.
- [`switching-tasks-with-snapshots.md`](switching-tasks-with-snapshots.md) — pause a feature mid-work, swap to a hotfix, swap back.

# `render config`

`dwe render config [service]` renders service **config files** (e.g. `.env`,
`env.php`) from a template pack into each service hub directory, replaying any
service-minted secrets harvested into the durable generated-value store. Configs
are pure render outputs derived from the merged config plus the store.

## Contents

- [Overview](#overview)
- [Template substrate: `${...}` shorthand](#template-substrate--shorthand)
- [The `${generated.<name>}` namespace](#the-generatedname-namespace)
- [Generated-value store](#generated-value-store)
- [`generated:` declaration](#generated-declaration)
- [Harvest, not mint](#harvest-not-mint)
- [Template pack resolution](#template-pack-resolution)
- [Manifest schema](#manifest-schema)
- [Encrypted `.age` sources](#encrypted-age-sources)
- [CLI usage](#cli-usage)
- [Pipeline builtins](#pipeline-builtins)
- [Deploy flow](#deploy-flow)
- [`dwe run` auto-render](#dwe-run-auto-render)
- [Reset and `--clear-generated`](#reset-and---clear-generated)
- [Migration from `configs:` copy](#migration-from-configs-copy)
- [Related references](#related-references)

## Overview

Config rendering writes straight into the already-mounted `src/` tree (no
per-file bind mount / `mountpoint` machinery). The model has two halves:

1. **Render** — resolve a config template pack, render each manifest entry, and
   write the result under the service hub dir (`svc.Dir`), mode **replace**
   (overwrite).
2. **Generated-once** — an opt-in mechanism for service-minted secrets (Laravel
   `APP_KEY`, Magento `crypt.key`, …). The *service* generates the value writing
   it into its own file; DWE **reads it back** ("harvest") into a durable
   per-service store (`.dwe/generated.yml`) and **replays** it on every
   subsequent render via the `${generated.<name>}` namespace.

Config rendering is **opt-in**: a service with no resolvable config pack is a
silent no-op. There is no `dwe init` scaffold wiring — you author the pack and the
pipeline steps yourself.

## Template substrate: `${...}` shorthand

Unlike `render ide` / `ai` / `git` — which use the raw Go `text/template` `{{ }}`
substrate (`packcommon.TemplateData`) — config templates use the **`${...}`
shorthand**, the same form already used in `${APP_*}` / `${DB_*}` export rules.
This is a deliberate divergence justified by config-file ergonomics: config
authors expect `${...}` parity with the values they reference.

`${X}` compiles to `{{ resolve .Raw "X" }}`, so the dot-path is looked up in the
merged config (`cfg.Raw`) with **no `raw.` prefix** — but only when `X`'s head
is a known namespace (a merged-config root key, or one of the special
namespaces below); an unrecognized head is left as a literal `${...}` instead
of silently rendering `""`:

```bash
# workspace/templates/config/laravel/env.tmpl
APP_URL=${services.main.hosts.web}
DB_HOST=${vars.databases.main.host}
DB_DATABASE=${vars.databases.main.name}
APP_KEY=${generated.app_key}
```

- **Per-service fields** use `${services.<name>...}` (e.g.
  `${services.main.ports.http}`). This exposes only the **curated subset**
  injected into `cfg.Raw["services"]` — `type` / `container` / dirs / `configs` /
  `ports` / `hosts` / … — **not** `render` / `generated` / arbitrary merged
  fields. An omitted or uninjected field renders `""` (all `${...}` resolvers are
  lenient — a missing path is the empty string, never an error).
- **Free-form values** live under `vars:` — reference them as
  `${vars.<path>}` (e.g. `${vars.databases.main}`). A bare top-level dot-path
  with no `vars.` prefix does not resolve: the merged config root is a strict
  allowlist (`project`, `services`, `vars`, …), so an arbitrary key like
  `databases` can never appear there directly.
- **Generated values** use `${generated.<name>}` (see below).

There is no singular current-service `${service....}` binding — reference the
service by name through `${services.<name>...}`.

## The `${generated.<name>}` namespace

`${generated.<name>}` resolves to the harvested value for the current service's
`<name>` field from the generated-value store. On the **first** deploy the store
is empty, so `${generated.app_key}` renders `""` — the service then mints the
real value and DWE harvests it. On **subsequent** renders the stored value is
replayed verbatim, so the secret survives `run` / redeploy while staying out of
git.

An absent key renders `""` (lenient, consistent with every other `${...}`
resolver). The namespace is scoped to the service being rendered: it reads
`store[<current-service>]`, so `${generated.app_key}` in `main`'s pack never sees
`magento`'s `crypt_key`.

## Generated-value store

The store lives at `.dwe/generated.yml` (under the gitignored `.dwe/` runtime
directory — it is never committed):

```yaml
services:
  main:
    app_key: "base64:Xa3…=="
  magento:
    crypt_key: "241f4fa60be8f69638343cacc5a1a192"
```

- Values are strings (block scalars for multi-line secrets).
- Writes are **atomic** (temp file + rename), mirroring the deploy journal.
- A **missing** file is an empty store (first deploy). A **corrupt** file is a
  surfaced error — never silently swallowed, so a malformed store cannot be
  mistaken for "no secrets yet".
- The store has no `schema_version`.
- Snapshot create/restore intentionally leaves `.dwe/generated.yml` untouched (it
  is a separate file from `.dwe/deploy/state.yml`).

## `generated:` declaration

Generated fields are declared in `service.yml` (per-service lifecycle state),
**not** in the manifest — a replayed value can target a command argument, not
just a template:

```yaml
# workspace/services/main/service.yml
type: app
dir: ./services/main
render:
  config:
    template: laravel          # optional pack pin; else convention + .local
generated:
  app_key:
    file: src/.env             # output file, relative to the service hub (svc.Dir)
    pattern: '^APP_KEY=(.*)$'   # regex; capture group 1 = value
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `file` | string | yes | Output file the service writes the value into, relative to the service hub dir (`svc.Dir`). Must be a contained relative path (no `..`). |
| `pattern` | string | yes | Regex applied per line; **capture group 1** is the harvested value. Must compile and declare **≥1 capture group** (validated by `dwe validate`). |

The map key (`app_key`) is the `${generated.<name>}` identifier. `dwe validate`
rejects an invalid regex, a missing capture group, a path-escaping `file`, or a
field name that is not a valid `${generated.<name>}` identifier.

## Harvest, not mint

DWE never generates the secret itself — the engine is hermetic (no crypto /
randomness), and reusing the service's own generator is format-agnostic from
DWE's side (DWE only reads back a string). Harvesting:

1. Reads `<svc.Dir>/<file>`.
2. Applies `pattern` line by line, taking capture group 1 of the first match.
3. **Write-if-absent** into the store, then saves atomically if anything changed.

Errors are surfaced precisely, never silently skipped: a missing file, a pattern
that matches no line, a pattern with no capture group, and a pattern that
captures an empty value are all hard errors — so a half-minted secret cannot
pollute the store. Write-if-absent means a redeploy is a no-op once a value is
stored.

`pattern` (a regex capturing one string) is used instead of a format `type` enum
because DWE never enumerates config formats and never *writes* a foreign format —
it only reads one string out of one file. The same `pattern` extracts an
`APP_KEY=` dotenv line or a PHP-array `'crypt' => ['key' => '…']` value.

## Template pack resolution

Config packs live under `workspace/templates/config/<pack>/` with the same
`<pack>.local/` shadow-pack override convention as ide/ai/git (see
[Local overrides](index.md#local-overrides)). Resolution order — the first match
is used:

1. `workspace/templates/config/<template>/` when `render.config.template` is set
   — **strict**: a pinned pack that does not exist is a hard error (catches typos).
2. `workspace/templates/config/<service-name>/`.
3. Each ancestor in the service's `extends` chain:
   `workspace/templates/config/<ancestor>/`.
4. `workspace/templates/config/default/`.
5. If none exist, config rendering is **skipped** (opt-in — no error).

Symlinked packs are rejected; the pack directory must stay contained inside the
project root.

## Manifest schema

Config packs are manifest-driven using the **shared** `manifest.yml` schema (the
same one read by ide/ai/git, see
[Shared manifest schema](index.md#shared-manifest-schema)), with two config-kind
constraints:

```yaml
# workspace/templates/config/laravel/manifest.yml
render:
  - from: env.tmpl
    to:   src/.env
```

| Aspect | Config pack |
|--------|-------------|
| Dest root | service hub dir (`svc.Dir`) |
| `to` shape | any contained relative path |
| `symlinks` | **rejected** — rendered config files are written in place, never symlinked |

`to: src/...` is a usage convention, not a hardcoded join: `to` is interpreted
relative to the service hub dir. Authors target the app tree (already dir-mounted
into the container) by writing `to: src/...`. Destinations are path-safety
guarded — a `to` that escapes the hub dir, or resolves outside it via a symlink,
is rejected.

## Encrypted `.age` sources

A pack source whose `from:` ends in **`.age`** is a native
[age](https://age-encryption.org)-encrypted file, committed to the repository.
`render config` decrypts it with the project identity and then runs the usual
`${...}` render over the plaintext:

```yaml
# workspace/templates/config/bot/manifest.yml
render:
  - from: google-credentials.json.age
    to:   config/google-credentials.json
```

Create one with [`dwe secrets encrypt`](../config/secrets.md#dwe-secrets-encrypt--decrypt);
`dwe secrets status` reports whether each `.age` source is readable on this
machine.

Rules worth knowing:

- **`to:` is never derived from the source.** Authors write
  `from: creds.json.age`, `to: src/creds.json` — the `.age` suffix is not
  auto-stripped, so the output is named whatever the service expects.
- **The identity is loaded once per render**, and only when the manifest
  actually has an `.age` entry — a pack without encrypted sources never touches
  `~/.config`.
- **`.age`-sourced outputs are written `0600`** and explicitly `chmod`ed, so a
  pre-existing `0644` target is tightened. Other outputs keep `0644`. The
  container reads a `0600` file fine, because it runs as the host UID/GID that
  `exports.env` already publishes.
- **A missing or wrong identity is a hard error** naming the source file and the
  fix (`dwe secrets key import`). An `.age` source with no `secrets.recipient`
  configured at all points at `dwe secrets init` instead.

### The marker guard

Independently of `.age` files, a **scalar** secret substituted through
`${vars.*}` can also reach a pack output. `render config` runs no preflight, so
it enforces the policy itself: if a rendered output still contains an
`ENC[age:…]` marker — i.e. a `${...}` substitution resolved to an undecrypted
value — the render **fails**, naming the entry's `to:` path and pointing at
`dwe secrets status`. Ciphertext is never written into the hub dir where the
container would read it as the credential.

A scalar secret that *did* decrypt is substituted normally and lands in the
gitignored hub dir at the pack's usual `0644`. See
[`secrets.md` → Where plaintext goes](../config/secrets.md#where-plaintext-goes).

## CLI usage

```bash
dwe render config              # render every enabled app service that resolves a pack (DeployOrder)
dwe render config main         # render only the `main` service
dwe render config main --harvest   # harvest-only pass: read on-disk values into the store, NO render
```

- With **no argument**, every enabled **app** service is processed in
  `DeployOrder` (deterministic); a service with no config pack is skipped
  silently. Config rendering is app-only — only app services may declare
  `dir` / `render` / `generated`, so tool / infra services are not iterated.
- With an **explicit `[service]`**, the argument is validated (must exist, be
  enabled, and have a hub dir); a missing pack surfaces a warning.
- **`--harvest`** switches to a harvest-only pass (`HarvestGenerated`, **no**
  render) — for bootstrapping an existing project's already-committed secrets
  into the store before they stop being committed.

The default render path is **read-only** with respect to project locks: it runs
no preflight and acquires no locks, matching the ide/ai/git renderers.
**`--harvest`** mutates the shared generated-value store, so it acquires the
project locks first (mirroring the deploy harvest builtin and `reset
--clear-generated`) to avoid clobbering a concurrent store writer.

## Pipeline builtins

Three engine builtins drive config rendering inside deploy / reset pipelines (see
[deploy builtins](../config/deploy/builtins.md)):

| Builtin | Purpose |
|---------|---------|
| `service_configs_render` | Render the service's config pack into its hub dir (mode `replace`), replaying stored generated values |
| `service_configs_render_check` | Verify the rendered targets exist; pairing it as a `check:` forces the render step to re-run every deploy |
| `service_generated_harvest` | Harvest the service's declared `generated:` fields into the store (write-if-absent) |

`service_configs_render_check` mirrors `service_configs_copy` + `service_configs_check`:
its presence as a `check:` trips the `hasCheck → Run` lever, bypassing the
action-hash skip so the render step always re-runs — template edits and store
clears therefore always take effect.

## Deploy flow

A deploy pipeline that renders configs and harvests a service-minted secret:

```yaml
phases:
  - name: configs
    steps:
      - name: render-configs
        type: builtin
        cmd: service_configs_render
        with:
          service: main
        check:                          # presence forces re-run every deploy
          type: builtin
          cmd: service_configs_render_check
          with:
            service: main

      - name: generate-app-key
        when:                           # gate: only when the store has no value yet
          type: builtin
          cmd: "generated-missing main app_key"
        type: dwe
        cmd: "shell main -- php artisan key:generate"

      - name: harvest-app-key
        type: builtin
        cmd: service_generated_harvest
        with:
          service: main
```

**First deploy:** render writes `APP_KEY=` (store empty) → gate open → the
service mints `APP_KEY=base64:…` → harvest captures it.

**Subsequent deploys:** render replays the stored value → gate closed → generate
skipped → harvest is a no-op. The render re-runs every deploy via its `check:`.
Invariant: **store empty for a key ⟺ value re-minted**.

Note that the harvest step is deliberately **not** gated: `service_generated_harvest`
skips any field the store already holds, without reading its file. That is what
makes it a no-op above, and it is load-bearing rather than an optimisation —
`dwe reset run` (without `--clear-generated`) keeps the store while wiping the
service hub, so on the next deploy the minted file is gone while the value it
produced is still authoritative. A harvest that insisted on re-reading it would
fail the whole deploy over a value it already has. The strict errors below
(missing file, no match, empty capture) therefore apply only to a field that is
**not** yet stored — which is exactly when a bad read could pollute the store.

The `generated-missing <svc> <field>` predicate (see
[conditions](../config/conditions.md#type-builtin--predicates)) reads
`.dwe/generated.yml` and is true when the field is absent or the store is
missing.

## `dwe run` auto-render

`dwe run` re-renders service configs from the store **after** the deployment gate
passes (and after the post-pull config reload), but **before** lifecycle phases —
so configs reflect the current templates and replayed secrets at run time. It
never runs generate/harvest on `run`.

The run-render is non-destructive when replay data is absent: if a deployed
service declares `generated:` keys that are missing from the store, that service's
render is **skipped** with a `dwe deploy run` hint rather than rendering a blanked
secret. Because the render runs only after the gate, a `reset --clear-generated`
followed by `dwe run` fails the gate before render is reached, so secrets are
never blanked.

## Reset and `--clear-generated`

`reset` **preserves** the store by default — the secret survives a reset. Pass
`--clear-generated` to clear it (scoped by `--service` / all):

```bash
dwe reset run --clear-generated              # clear the whole store on full reset
dwe reset run --service main --clear-generated   # clear only main's entries
```

The store is cleared **only after the full reset succeeds**, including the
post-pipeline journal cleanup — never if the pipeline or the journal mutation
failed (else a deployed-journal + empty-store mismatch would make the run gate
trust a service with no secrets). On a TTY with a non-empty store, an interactive
prompt asks whether to also clear the generated values (default No). Rotation =
clear + redeploy.

## Migration from `configs:` copy

The copy mechanism (`configs:` / `mountpoint` in `service.yml`,
`service_configs_copy` / `service_configs_check` builtins) keeps working but is
**deprecated** — `dwe validate` emits a warning and a single runtime notice fires
per copy step. To migrate:

1. Move each baked file in `configs/services/<svc>/` to a template under
   `workspace/templates/config/<pack>/`, replacing literal values with `${...}`
   references.
2. Declare `render.config` (optional pin) and any `generated:` fields in
   `service.yml`; drop the `configs:` / `mountpoint` block.
3. Swap `service_configs_copy` (+ `service_configs_check`) for
   `service_configs_render` (+ `service_configs_render_check`) in the pipeline,
   adding the generate-gate + `service_generated_harvest` steps for any
   service-minted secrets.
4. Bootstrap an already-committed secret into the store with
   `dwe render config <svc> --harvest`, then stop committing it.

## Related references

- [service definitions (`service.yml`)](../config/services/fields.md) — `render.config` and `generated:` field reference
- [deploy builtins](../config/deploy/builtins.md) — `service_configs_render`, `service_configs_render_check`, `service_generated_harvest`
- [conditions](../config/conditions.md) — the `generated-missing` predicate
- [render index](index.md) — shared manifest schema, local overrides, pack resolution
- [secrets](../config/secrets.md) — `.age` sources, `ENC[age:…]` markers, keys and the `dwe secrets` command surface
- [Templates](../templates.md) — Go template syntax and render contexts
- Run `dwe render config --help` for the live CLI surface

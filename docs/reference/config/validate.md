# validate.yml

Project readiness checks.

## Contents

- [Purpose](#purpose)
- [Validation domains](#validation-domains)
- [Structure](#structure)
- [Top-level fields](#top-level-fields)
- [Check entry fields](#check-entry-fields)
- [Stages](#stages)
- [Service gating](#service-gating)
- [Available builtins](#available-builtins)
  - [`shell`](#shell)
  - [`file_exists`](#file_exists)
  - [`executable_in_path`](#executable_in_path)
  - [`env_keys_present`](#env_keys_present)
  - [`config_keys_present`](#config_keys_present)
  - [`tcp_reachable`](#tcp_reachable)
  - [`http_check`](#http_check)
- [`type: command` checks](#type-command-checks)
- [Checks should be idempotent inspection](#checks-should-be-idempotent-inspection)
- [Worked examples](#worked-examples)
- [CLI flags](#cli-flags)
- [Diagnostic output](#diagnostic-output)
- [External linters](#external-linters)
- [Related commands](#related-commands)

## Purpose

`workspace/validate.yml` declares project-level readiness checks. The CLI consumes these from two entry points:

- `dwe validate` — runs every check (plus YAML-shape validators in the `config`, `templates`, `commands`, `translations`, and `bridge` domains, plus environment probes in the `env` domain) and reports diagnostics.
- Preflight hook on `dwe deploy run`, `dwe run`, `dwe stop`, `dwe restart`, and `dwe reset run` — runs the subset of checks bound to the relevant stage before any side effect on Docker, git, or the filesystem.

The goal is to surface user-actionable problems ("you're not logged into ghcr.io", "DATABASE_URL is empty in `.env`", "VPN is down") BEFORE deploy steps fail mid-way with cryptic errors.

## Validation domains

The validate command runs six domains in addition to the existing YAML-shape validators:

| Domain | Source | Configurable? |
|--------|--------|---------------|
| `env.*` | Hardcoded in the CLI | No — seven fixed probes |
| `checks.*` | `workspace/validate.yml` entries | Yes — declarative |
| `linters.*` | Built-in adapters (shellcheck, hadolint) + `workspace/validate.yml` `linters:` block | Yes — declarative |
| `translations.*` | `workspace/i18n/` translation files | No — fixed validators (parse errors, orphan command/group ids, unknown `render.*` keys) |
| `snapshot.*` | On-disk snapshot directories + `workspace/snapshot.yml` | No — fixed validators per snapshot name |
| `tests.*` | `workspace/tests/*.yml` scenario files | No — fixed scenario validators (renders + resolves each scenario's steps, flags unknown services / command refs / duplicate step names, and surfaces compose-isolation hazards as warnings) |

The `tests.*` domain is validate-only (like `snapshot.*`) — it never runs in preflight, and it stays silent when `workspace/tests/` is absent. See [`tests.md`](tests.md#dwe-validate-tests) for the full scenario-validation surface (`dwe validate tests`).

The `env.*` probes are: `env.docker_bin`, `env.docker_daemon`, `env.docker_compose`, `env.git_bin`, `env.shell_bin`, `env.project_perms`, `env.ports_free`. They run on every `dwe validate` invocation and on every preflight (regardless of stage — env has no stage concept), with one exception: `env.ports_free` self-skips on the `stop` stage since port conflicts are irrelevant when winding the project down.

`env.ports_free` reads every host port declared under `services.<name>.ports` (enabled services only) and checks whether each is bindable. It queries `docker ps --format=json` once to learn which containers currently hold which ports: containers labelled `com.docker.compose.project=<our project>` are treated as "ours" (compose will reuse them on `up`); containers from any other compose project trigger a conflict diagnostic that names the foreign container and project; for ports not held by any container the probe falls back to a direct bind attempt to detect non-Docker processes. Docker unreachability falls through silently — `env.docker_daemon` covers that case.

`config.compose_project_name` warns when the active compose `-f` chain declares a top-level `name:` that differs from the project name dwe passes via `docker compose -p`. dwe always invokes compose with `-p <resolved>` (the resolved `workspace/docker.yml` `project_name`, else the canonical `<prefix>-<name>` from `project.name`), which silently overrides any top-level `name:` in the compose files. A divergent `name:` is therefore dead config — the real project scope (container/network/volume labels) is dwe's resolved name, not the one the file appears to declare — and a foot-gun for anyone running raw `docker compose` without dwe's `-p`. The check models compose's real precedence: it scans the active chain (`ComposeFiles()` — enabled overlays only) in `-f` order and compares the **last** declared `name:` (later `-f` overrides earlier), so a base `name:` that a later overlay already corrects does not warn. The fix is to align the two: change the effective compose `name:` to match, or set `docker.yml` `project_name` to match and drop the redundant compose `name:`. The check stays silent when the effective name uses unresolved interpolation (e.g. `name: ${COMPOSE_PROJECT_NAME}` — cannot be compared) or when `project.name` is unset (dwe omits `-p`, so compose honours the file's own `name:`).

`config.formal_block_fields` warns when a formalized top-level config block carries an unknown nested key. The merged 3-layer config is decoded leniently (plain `yaml.Unmarshal`, not `KnownFields`), so a typo under one of these blocks — e.g. `stop: { port_release_timeot: 0 }` — is silently dropped and the field falls back to its default, quietly changing behavior. The strict-root allowlist catches unknown *top-level* keys with a hard error, but not nested ones; this check fills that gap as a non-fatal `dwe validate` warning (it does not run in preflight, so it never blocks `dwe run`). It scans all three layer files (`workspace.yml`, `workspace/defaults.yml`, `workspace/local.yml`) and anchors each finding to the offending file and line. The recognized-key set for each block is derived by reflection from the backing Go struct's YAML tags, so it cannot drift when a struct gains a field. Covered blocks (immediate children only — the check does not descend into nested structures like `runtime.spx`): `project` (`name`, `prefix`), `runtime` (`use_https`, `spx`), `exports` (`env`), `compose` (`base`, `extra`), `docs` (`mermaid`, `cache_size_mb`), `update` (`mode`), `bridge` (`vars_writable`), `stop` (`port_release_timeout`). The `ui:` block is covered by the dedicated `config.ui` validator instead (which also descends into `ui.commands`); free-form (`vars`) and per-service (`services`) blocks are intentionally excluded.

`config.template_refs` warns on a `${head.path}` reference whose head is a permitted merged-config root key but the remaining path does not resolve — almost always a typo, since `vars:` is the one root key with fully free-form content (`${vars.opechatka}` when only `vars.source.repo` was declared). The head is checked against the root-key allowlist, not against what the project actually declared, so `${vars.source.repo}` in a project with no `vars:` block at all is reported too. It is deliberately silent on an unrecognized head (`${HOME}`, a stray dollar sign) and on the special namespaces that never live in `Raw` (`param`, `context`, `files`, `host`, `snapshot`, `args`, `generated`) — see [Two syntaxes: shorthand and full templates](../templates.md#two-syntaxes-shorthand-and-full-templates) in the Templates reference.

Its scope is not pipeline steps alone, but it is not every field either: it covers the scalar fields `varsusage` recognizes as templated across all workspace YAML — `cmd`, `text`, `value`, `title`, `project_name`, `confirm`, scalar `when:`, plus every scalar leaf (at any depth) under a `with:` or `env:` mapping — plus the render bodies under `workspace/templates/config/**`. Sequence-valued fields such as a command's `argv:`, and scalar fields outside that set (`workdir`, `messages.*`, `confirmation_text`), are **not** scanned, so a `${vars.typo}` there is not reported. Only the `${...}` shorthand is checked — the Go-template form (`{{ resolve .Raw "vars.x" }}`) is not.

`config.container_name` warns when a compose service's `container_name:` diverges from the conventional `<project>-<service>` — the name the daemon builtins build directly and the one scripts and docs habitually assume. The defect is divergence itself, not casing. dwe's own per-service commands (`dwe stop`/`restart`/`logs <name>`) resolve containers through the compose project+service labels, never by guessing this name, so they are unaffected either way; the foot-gun is raw `docker`/`docker compose` usage, scripts, and documentation. Note that *removing* `container_name` is not equivalent to aligning it — compose then names the container `<project>-<service>-1`. Silent when the declared value already matches, or when it is an interpolated `${...}` value that cannot be compared without resolving the environment.

`config.ports_exports` warns when a service declares `services.<name>.ports.<key>` but no `exports.env` rule reads from `services.<name>.ports.<key>`. Such a port is display-only: a `local.yml` override of it will not move the actual container binding anywhere visible, and `dwe test`'s automatic host-port isolation (see [`tests.md`](tests.md)) silently does not apply to it either. A service that declares no ports of its own inherits the parent's whole port map through `extends:`; those inherited ports are not reported again on the child, since the finding belongs to the parent's `service.yml`, which is where the port is actually written.

`config.info` reports the effective state of `workspace/info.yml`, not merely "does it exist": an **all-comment or empty file** is treated the same as absent — the built-in dashboard is silently active — and is reported at `SeverityInfo` (not `SeverityOK`, so an agent scanning for green does not stop looking); a deliberate `sections: []` reports its own state at `SeverityInfo`; only an authored dashboard with real content earns `SeverityOK`.

The `checks.*` validators are synthesized one per `validate.yml` entry. Each dispatches to either a built-in inspection routine or a locked-down user command at run time.

## Structure

```yaml
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
      cmd: docker pull ghcr.io/owner/repo:latest >/dev/null

  - id: project-deps
    description: Project dependency check script passes
    stages: [run, deploy]
    type: command
    cmd: deps.check
```

The file is optional. When absent, only `env.*` and the existing YAML-shape validators run.

## Top-level fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `checks` | list | no | Check entries (see below). Optional; may be empty or omitted. |
| `linters` | map | no | External linter adapters (see [External linters](#external-linters)). |

Unknown top-level fields are rejected at load time (strict decoding).

## Check entry fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Unique identifier. Becomes the diagnostic `Target`. |
| `description` | string | yes | Human-readable summary shown in the diagnostic table. |
| `stages` | list of strings | yes | Stages that trigger this check (see [Stages](#stages)). |
| `services` | list of strings | no | Restricts the check to projects where at least one of the named services is enabled (see [Service gating](#service-gating)). |
| `type` | string | yes | One of `builtin` or `command`. Unknown values are rejected. |
| `cmd` | string | yes | Builtin name (for `type: builtin`) or user-command ID (for `type: command`). |
| `severity` | string | no | One of `error` (default), `warning`, `info`. Unknown values are rejected. |
| `hint` | string | no | Remediation hint included in the diagnostic. Keep concise; split long hints with `\n`. |
| `with` | map | no | Parameters passed to the builtin or user command. |

Schema rules enforced at load time:

- `id` must be unique across entries.
- `stages` must be non-empty.
- `services` must be non-empty when the key is present (omit the key entirely to run unconditionally). Empty strings inside the list are rejected.
- Unknown service names (typos like `services: [aap]` when only `api` exists) surface as `config.validate` error diagnostics at run time, not as load errors — the loader has no view of the merged services map.
- `type` must be `builtin` or `command`.
- `severity` must be `error` / `warning` / `info` if present.
- `with:` shape validity (required keys, types) is checked against the target builtin's `Validate` method — failures surface as `checks.<id>` diagnostics at run time, not as load errors.

## Stages

A check runs whenever its `stages` list contains a stage the caller asked for. The CLI defines five reserved stages with built-in hooks:

| Stage | Triggered by |
|-------|--------------|
| `deploy` | `dwe deploy run`, `dwe validate --stage deploy` |
| `run` | `dwe run`, `dwe restart` (run leg), `dwe validate --stage run` |
| `stop` | `dwe stop`, `dwe restart` (stop leg), `dwe reset run`, `dwe validate --stage stop` |
| `command` | `dwe validate --stage command` (reserved for future use; no automatic hook) |
| `post-setup` | the deploy final preflight only — `dwe deploy run`, `dwe deploy` after the setup wizard, `dwe validate --stage post-setup` |

`dwe validate` without `--stage` runs every check regardless of stage.

### `deploy` vs `post-setup`: when in the deploy flow a check runs

The deploy flow has **two** preflight moments:

1. An early **pre-wizard gate** (only in the interactive `dwe deploy` menu, before the setup wizard is shown) — surfaces problems like a down Docker daemon before the user invests time filling out the wizard.
2. The **final preflight**, run immediately before the deploy pipeline executes — both in `dwe deploy run` and after the wizard in interactive `dwe deploy`.

`stages: [deploy]` checks run at **both** moments. That is wrong for a check that depends on a value the wizard writes into `local.yml`: at the early gate the value isn't set yet, so the check blocks before the user can reach the wizard.

`stages: [post-setup]` checks run **only at the final preflight** — after the wizard has populated `local.yml`, or (when no wizard runs, e.g. `dwe deploy run`) immediately before the pipeline. This is the right stage for "a value must be set before deploy" guards: the interactive wizard fills it, and the non-interactive path still catches a missing value **before** any side effect instead of failing mid-pipeline. Pair it with [`config_keys_present`](#config_keys_present) to assert merged-config values, or with `env_keys_present` for rendered `.env` files.

A `post-setup` check carries no `deploy` stage, so it is naturally skipped at the early gate. (`stages: [deploy, post-setup]` is accepted but redundant — it behaves exactly like `[deploy]`.) `post-setup` has no meaning outside the deploy flow; on `dwe run`/`dwe stop` it is never triggered.

Unknown stages are accepted (open enum) but produce a **warning** at load time so users catch typos early:

- `stage "deplooy" is not a known preflight stage` (with a suggestion if close via Levenshtein distance)
- Special notes: `restart` is composite (uses both stop and run stages, no separate preflight); `reset` uses the stop stage only

Unknown stages can still be invoked explicitly with `dwe validate --stage <name>` if needed (e.g. for custom validation workflows).

## Service gating

`services:` restricts a check to projects where at least one of the listed services is enabled. The semantics are OR:

- Omit the field → check always runs (when its stage matches).
- `services: [api]` → check runs iff `api` is enabled.
- `services: [api, worker]` → check runs iff `api` OR `worker` is enabled. All services disabled → check is silently skipped (no row in the diagnostics table).

`services:` and `stages:` are independent AND filters: stage matches first, then services. A check with `stages: [deploy]` and `services: [api]` runs only when both conditions hold.

Behaviour is identical in preflight and `dwe validate` (no flag, no environment override). One escape hatch: `dwe validate checks <id>` with an explicit id bypasses the services-gate so users can inspect a check whose target services are all disabled — useful when debugging the gate itself.

Unknown service names produce an error diagnostic in the `config.validate` target so typos surface early; the gated check itself does not run during the same pass (the unknown name contributes nothing to the OR).

## Available builtins

All seven builtins are usable both as `type: builtin` check entries and as deploy step bodies / `check:` action blocks.

### `shell`

Runs a shell command via hardcoded `sh -c` (matching the deploy `when:` predicate convention). Exit 0 = pass. This builtin uses POSIX-portable `sh -c` regardless of the project's configured shell, ensuring checks run identically across all environments.

The command runs with its working directory set to the **project root**, so relative paths mean the same thing as in `file_exists` and in a `when:` condition regardless of the directory `dwe` was invoked from.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `cmd` | string | yes | — | Shell command body. |
| `timeout` | duration | no | `10s` | Maximum execution time. `0` means **unbounded** (not "expire immediately"). |

Error message on non-zero exit: `exit status N: <last line of stderr>`.

See [deploy: `cmd: shell` vs `type: shell`](deploy/steps.md#cmd-shell-builtin-vs-type-shell-step) for the distinction between this builtin and the `type: shell` step execution type.

### `file_exists`

Verifies a file is present on disk.

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `path` | string | yes | Path relative to the project root. |

### `executable_in_path`

Verifies a binary is resolvable via `exec.LookPath`.

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `name` | string | yes | Executable name (no path components). |

### `env_keys_present`

Verifies one or more keys exist with non-empty values in a `.env`-style file. Parsing follows `.env` conventions: blank lines and full-line `#` comments are skipped; surrounding `"..."` / `'...'` quotes are stripped; `KEY=`, `KEY=""`, and `KEY=''` all count as empty.

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `file` | string | yes | Path to the `.env`-style file, relative to project root. |
| `keys` | list of strings | yes | Keys that must be present AND non-empty. |

Error message: `missing or empty keys: A, B, C`.

### `config_keys_present`

Verifies one or more dot-paths resolve to non-empty values in the **merged DWE configuration** — the `workspace.yml` / `defaults.yml` / `local.yml` layers after merging. This is the config-aware counterpart of `env_keys_present`: instead of reading an on-disk `.env`, it reads the in-memory merged config, so it sees `local.yml` overlays immediately and does not depend on whether a rendered `.env` has been materialised yet.

Addressing is the same dot-path the setup wizard uses in its `writes:` field, so the path you assert is exactly the path the wizard wrote — e.g. `vars.db.api_key` or `vars.app.log_level`. Pair it with [`stages: [post-setup]`](#deploy-vs-post-setup-when-in-the-deploy-flow-a-check-runs) so it runs after the wizard populates `local.yml`.

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `keys` | list of strings | yes | Dot-paths into the merged config; each must resolve to a non-empty value. |

A path is "missing" when it does not resolve, when it resolves to `null`, or when it renders to the empty string. Non-string scalars (numbers, booleans) count as present. Error message: `missing or empty keys: vars.db.api_key, vars.app.log_level`.

**Which paths are reachable.** Assert the same paths the wizard can write — see the [setup `writes:` scope](setup.md#write-scope-rules). Custom values live under the [`vars:` sandbox](workspace.md#strict-root--the-vars-sandbox) (`vars.db.*`, `vars.app.*`, …) — the merged config root is strict, so free-form keys must be nested under `vars:` to survive the merge and resolve here. Under `services.<name>`, `local.yml` accepts **only** `enabled`, `ports.<name>`, and `hosts.<name>` — both the wizard and the config loader reject anything else, so a per-service **secret** cannot live at `services.<name>.env.*` in `local.yml`. Keep service secrets in the service's rendered `.env` and assert them with `env_keys_present` instead; use `config_keys_present` for the top-level values the wizard writes.

### `tcp_reachable`

Attempts a TCP dial to `host:port`.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `host` | string | yes | — | Hostname or IP. |
| `port` | int | yes | — | Port in range 1–65535. |
| `timeout` | duration | no | `3s` | Dial timeout. |

### `http_check`

Performs an HTTP `GET` and asserts the response status (and, when set, a body substring), retrying on failure.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Absolute `http`/`https` URL with a host. |
| `status` | int | no | `200` | Expected status code. |
| `contains` | string | no | — | Substring that must appear in the response body. |
| `retries` | int | no | `0` | Additional attempts after the first (total attempts = `retries + 1`). |
| `interval` | duration | no | `1s` | Wait between attempts. |
| `timeout` | duration | no | `5s` | Per-attempt timeout. |

Error message on failure: `http_check <url>: expected status 200, got 503` (with `(after N attempts)` appended when retries are configured). See the [builtins reference](deploy/builtins.md#http_check) for the full behavior notes.

## `type: command` checks

A check entry with `type: command` dispatches to a declarative user command from `workspace/commands/`. The `with:` block is passed through as the user command's `params:` payload — exactly like `dwe commands <id> --set k=v`.

Restrictions enforced at load time:

- The target command's `type:` MUST be `shell` or `script`. Workflow, service_exec, service_run, dwe, and builtin-as-command targets are rejected with: `checks may only invoke user commands of type shell or script (got: <type>)`.
- An unknown command ID is rejected with: `unknown command: <id>`.

Execution is locked down:

- `SkipConfirm = true` — confirmation prompts are bypassed.
- `NonInteractive = true` — prompt-based UI paths short-circuit.
- `SkipNotify = true` — desktop notifications are suppressed.
- `stdout` is discarded; `stderr` is captured and the tail is included in the diagnostic message if the check fails.

## Checks should be idempotent inspection

A check answers `is the world ready?` — not `make the world ready`. By convention, every check SHOULD be idempotent, side-effect-free, and quick to run.

**The CLI does NOT enforce this.** There is no read-only sandbox for subprocesses; a `type: command` check whose body is `rm -rf /tmp/work` will execute exactly that under preflight.

What the CLI DOES enforce on `type: command` checks:

- Non-interactive execution (no prompts, no confirmations).
- Notifications suppressed.
- stdout discarded, stderr captured.

The trade-off: the CLI keeps the bridge minimal so user-authored shell/script commands can be reused for both deploy steps and readiness checks, without introducing a new restricted execution mode. A mutating check is a sharp edge for the author — document it loudly in `description:` if your check has to mutate, and prefer pure inspection (`shell: docker pull ... --quiet`, `shell: test -f path`) wherever possible.

## Worked examples

**1. Container registry login (built-in shell):**

```yaml
checks:
  - id: ghcr-login
    description: Authenticated against ghcr.io
    stages: [deploy]
    severity: error
    hint: |
      Run `docker login ghcr.io` with a GitHub PAT.
    type: builtin
    cmd: shell
    with:
      cmd: docker pull ghcr.io/owner/private-image:latest --quiet
      timeout: 30s
```

**2. Local DB dump present (file_exists):**

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

**3. Required secrets configured (env_keys_present):**

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

**4. Service-scoped check (services gate):**

```yaml
  - id: api-jwt-secret
    description: JWT_SECRET configured for API
    stages: [run, deploy]
    services: [api]                  # only runs when api is enabled
    severity: error
    hint: Set JWT_SECRET in services/api/.env
    type: builtin
    cmd: env_keys_present
    with:
      file: services/api/.env
      keys: [JWT_SECRET]
```

**5. Wizard-supplied value required before deploy (post-setup + config_keys_present):**

```yaml
  - id: db-api-key-set
    description: vars.db.api_key must be set before deploy
    stages: [post-setup]             # final preflight only — after the setup wizard
    severity: error
    hint: |
      Run `dwe deploy` and complete the wizard, or set
      vars.db.api_key in workspace/local.yml.
    type: builtin
    cmd: config_keys_present
    with:
      keys: [vars.db.api_key]
```

The setup wizard writes `vars.db.api_key` into `local.yml` (a path under the [`vars:` sandbox](workspace.md#strict-root--the-vars-sandbox) — `services.<name>.env.*` is **not** a legal wizard/`local.yml` target, see the builtin's reachability note above); this check asserts the same dot-path is set. Because it is `post-setup`, it is skipped at the early pre-wizard gate (so the wizard is reachable) and runs at the final preflight — catching a missing value before deploy starts, including on `dwe deploy run` where no wizard runs.

**6. Corporate VPN reachable (tcp_reachable):**

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

**7. Project dependency script (type: command):**

```yaml
  - id: project-deps
    description: Required CLIs installed (./scripts/check-deps.sh)
    stages: [run]
    type: command
    cmd: deps.check
```

Where `workspace/commands/deps.yml` declares:

```yaml
group: deps
commands:
  check:
    type: shell
    description: Verify required CLIs
    cmd: |
      set -e
      command -v node
      command -v pnpm
      command -v psql
```

**8. Executable in PATH (executable_in_path):**

```yaml
  - id: jq-installed
    description: jq is available for compose introspection helpers
    stages: [deploy]
    severity: warning
    type: builtin
    cmd: executable_in_path
    with:
      name: jq
```

## CLI flags

- `dwe validate` — runs `config.*`, `templates.*`, `commands.*`, `bridge.*`, `env.*`, and all `checks.*`. Optional positional scope narrows the run (e.g. `dwe validate env`, `dwe validate checks ghcr-login`, `dwe validate bridge`).
- `dwe validate bridge` — static checks on per-service `bridge:` blocks only: `on_unreachable` enum (`fail` / `warn`), `shim_path` absoluteness, and the bridged-service `dir` / `dir_internal` workspace mapping the shim translates over. Validate-only — the bridge domain does not participate in preflight.
- `dwe validate --stage <name>` — local flag on the `validate` command. Filters `checks.*` by stage. `env.*` and other domains are unaffected (they have no stages).
- `dwe validate --strict` — treat warnings as errors (exit 1).
- `dwe validate --quiet` — hide ok / info rows.
- `dwe validate --level <levels>` — show only the given severity levels (comma-separated: `ok`, `info`, `warning`, `error`; e.g. `--level error,warning`). This is display-only — it never changes the summary counts or the exit code. Applies to both the table and `--output json`.
- `--skip-preflight` — local flag on `deploy run`, `run`, `stop`, `restart`, and `reset run`. When set, preflight prints `preflight skipped (--skip-preflight)` to stderr and runs NO validators. The flag is a true bypass: `type: command` checks invoke arbitrary user scripts, so the CLI does not run them under a flag the user named "skip".

## Diagnostic output

Diagnostics share the rendering and severity model used by the rest of `dwe validate`:

- `Severity`: from `entry.severity` (default `error`).
- `Domain`: `checks` (or `env` for hardcoded probes).
- `Target`: the entry's `id`.
- `File`: `workspace/validate.yml` (entries) or empty (env probes).
- `Line`: 1-based line number of the entry's first key (entries).
- `Message`: builtin / command error string.
- `Hint`: from `entry.hint`.

Preflight writes the same diagnostic table to stderr before failing with exit code 1. Use `\n` in hints to split long remediation text across lines — the Lipgloss table honors newlines.

## External linters

The `linters.*` domain runs well-known external linters (shellcheck, hadolint) and arbitrary `type: generic` adapters as part of `dwe validate`. Linters do **not** run in preflight — preflight answers "can we run?", not "is the code clean?".

### Wire layout

```yaml
linters:
  shellcheck:
    enabled: true
    bin: shellcheck
    paths: [workspace/scripts, scripts]
    extensions: [.sh, .bash]
    flags: [--severity=warning]
    severity: warning
  hadolint:
    paths: ["."]
    filenames: [Dockerfile]
    extensions: [.dockerfile]
  yamllint:
    type: generic
    bin: yamllint
    paths: ["."]
    extensions: [.yml, .yaml]
    flags: [-s]
```

The map key is the adapter ID. Unknown fields are rejected at load time (strict decoding).

### Entry fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | no | `builtin` (default) or `generic`. |
| `enabled` | bool | no | Omitted → autodetect (true if `bin` is on PATH). `false` → silent skip. |
| `bin` | string | no | Defaults to the adapter's default (e.g. `shellcheck`). **Must be a bare command name** — no path separators. Absolute or relative paths are rejected at load time. |
| `paths` | list of strings | no | Defaults to the adapter's default. Each entry must be relative, non-empty, and contain no `..`. `"."` is allowed (root-equality, used by hadolint). |
| `extensions` | list of strings | no | Defaults to the adapter's default. Each entry must start with `.` (e.g. `.sh`, not `sh`). |
| `filenames` | list of strings | no | Literal basenames matched alongside extensions (e.g. `Dockerfile`). No path separators allowed. |
| `flags` | list of strings | no | Appended after the adapter's built-in flags. Built-in adapters reserve output-format flags (`--format`, `-f`) — passing them in any argv form (`--format=gcc`, `-f tty`, `-fgcc`) is rejected at load time. |
| `severity` | string | no | One of `error`, `warning`, `info`. Caps adapter findings from above (e.g. `severity: warning` downgrades adapter `error` findings to `warning`). `ok` is **not** allowed — use `enabled: false` to disable. Operational diagnostics (timeout, truncation, parse failure, missing-path) are never clamped, so users cannot accidentally silence runtime failure signals. |

### Built-in adapters

| ID | Default bin | Default paths | Default extensions | Default filenames | Reserved flags |
|----|-------------|---------------|--------------------|--------------------|----------------|
| `shellcheck` | `shellcheck` | `workspace/scripts`, `scripts` | `.sh`, `.bash` | — | `--format`, `-f` |
| `hadolint` | `hadolint` | `.` | `.dockerfile` | `Dockerfile` | `--format`, `-f` |

### `type: generic`

The generic adapter runs `bin <flags> <files...>` and converts a non-zero exit into a single error-severity diagnostic with the combined stdout+stderr as the message (truncated to ~2 KB to keep the table readable). It has no reserved flags — the user owns the entire flag surface — and no per-line parsing. Use it for linters whose output format we do not parse natively.

### Autodetect rules

1. For each known built-in adapter, if no entry exists in `linters:` → synthesize an entry with defaults (per-adapter, not all-or-nothing).
2. Block present, `enabled` omitted → `true`.
3. `enabled: false` → silent skip (no diagnostic).
4. Default `bin:` missing on PATH → silent skip ("we tried autodetecting; nothing to do").
5. Explicit `bin:` configured but missing on PATH → one Warning diagnostic (config problem, not code problem).
6. Path expansion yields no files → silent skip.

### User-config binary overrides

You can override the binary path for any linter using your user-level config file (`~/.config/dwe/config`). This is useful when you have custom installations, replacements (e.g., `podman` instead of `docker`), or binaries outside the default PATH.

Add a line to your user config:

```
binary_shellcheck=/custom/path/to/shellcheck
binary_hadolint=/opt/hadolint
```

The format is `binary_<linter-id>=<path>`. Paths are absolute or relative to your current directory. If the path does not exist or is not executable, `dwe validate` will emit an error diagnostic in the `linters` domain.

**Note:** These overrides are **only** consulted during `dwe validate`. Lifecycle commands (deploy, run, stop, etc.) do not use linter binaries, so broken overrides do not affect normal operation.

### Scope

Run all linters or narrow to one with the `linters` subcommand:

```
dwe validate                       # all domains (including linters)
dwe validate linters               # all linters
dwe validate linters shellcheck    # only shellcheck
```

Unknown linter IDs produce an empty result (not a hard error — mirrors `checks` behaviour).

### Per-linter bounds

- **Timeout**: 5 minutes per linter (`DefaultLinterTimeout`). Exceeded → Error diagnostic; partial output is not parsed.
- **Output cap**: 50 MB combined stdout+stderr per linter (`MaxLinterOutputBytes`). Excess is dropped and a Warning diagnostic is emitted; the parser still runs on the captured prefix.
- **Concurrency**: linters run in parallel, capped at `runtime.NumCPU()` (`MaxLinterConcurrency`). One linter's failure (panic, timeout, parser error) never cancels siblings.

### File walking

- `paths:` entries are recursively walked under the project root.
- Explicit file paths (entries that resolve to a regular file) bypass extension/filename filters.
- A file matches if its extension is in `extensions:` OR its basename is in `filenames:`.
- Symlinks are skipped (defense against escapes outside the project root).
- `.git/` is always skipped. Adapter-specific noise (e.g. `node_modules`, `vendor`) is left to the user to narrow via `paths:`.
- Missing **default** paths (e.g. shellcheck's `workspace/scripts` in a project that has none) are silently dropped. Missing **user-configured** paths (entries the user wrote explicitly) produce a Warning.

### Trust model

`bin:` is restricted to a bare command name resolved via `PATH` at runtime; absolute and relative paths are forbidden at load time. Rationale: `validate.yml` ships with the repo; a malicious config with `bin: ./scripts/evil.sh` should not silently execute arbitrary code on `dwe validate`. Users who genuinely need a custom binary path install it on `PATH` (or wrap it).

## Related commands

- `dwe validate` — full validation run (all domains).
- `dwe validate env` — env probes only.
- `dwe validate checks [id]` — declarative checks (optional id narrows to one).
- `dwe validate linters [id]` — external linters (optional id narrows to one).
- `dwe deploy run` / `run` / `stop` / `restart` — invoke preflight automatically (see `--skip-preflight`). Linters do **not** run in preflight.

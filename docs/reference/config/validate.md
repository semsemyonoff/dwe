# validate.yml

Project readiness checks.

## Contents

- [Purpose](#purpose)
- [Two validation domains](#two-validation-domains)
- [Structure](#structure)
- [Top-level fields](#top-level-fields)
- [Check entry fields](#check-entry-fields)
- [Stages](#stages)
- [Available builtins](#available-builtins)
  - [`shell`](#shell)
  - [`file_exists`](#file_exists)
  - [`executable_in_path`](#executable_in_path)
  - [`env_keys_present`](#env_keys_present)
  - [`tcp_reachable`](#tcp_reachable)
- [`type: command` checks](#type-command-checks)
- [Checks should be idempotent inspection](#checks-should-be-idempotent-inspection)
- [Worked examples](#worked-examples)
- [CLI flags](#cli-flags)
- [Diagnostic output](#diagnostic-output)
- [Related commands](#related-commands)

## Purpose

`devbox/validate.yml` declares project-level readiness checks. The CLI consumes these from two entry points:

- `devbox validate` — runs every check (plus YAML-shape validators in the `config`, `templates`, and `commands` domains, plus environment probes in the `env` domain) and reports diagnostics.
- Preflight hook on `devbox deploy run`, `devbox run`, `devbox stop`, and `devbox restart` — runs the subset of checks bound to the relevant stage before any side effect on Docker, git, or the filesystem.

The goal is to surface user-actionable problems ("you're not logged into ghcr.io", "DATABASE_URL is empty in `.env`", "VPN is down") BEFORE deploy steps fail mid-way with cryptic errors.

## Two validation domains

The validate command runs two new domains in addition to the existing YAML-shape validators:

| Domain | Source | Configurable? |
|--------|--------|---------------|
| `env.*` | Hardcoded in Go (`internal/validate/env/`) | No — six fixed probes |
| `checks.*` | `devbox/validate.yml` entries | Yes — declarative |

The `env.*` probes are: `env.docker_bin`, `env.docker_daemon`, `env.docker_compose`, `env.git_bin`, `env.shell_bin`, `env.project_perms`. They run on every `devbox validate` invocation and on every preflight (regardless of stage — env has no stage concept).

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
| `checks` | list | yes | Check entries (see below). May be empty. |

Unknown top-level fields are rejected at load time (strict decoding).

## Check entry fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Unique identifier. Becomes the diagnostic `Target`. |
| `description` | string | yes | Human-readable summary shown in the diagnostic table. |
| `stages` | list of strings | yes | Stages that trigger this check (see [Stages](#stages)). |
| `type` | string | yes | One of `builtin` or `command`. Unknown values are rejected. |
| `cmd` | string | yes | Builtin name (for `type: builtin`) or user-command ID (for `type: command`). |
| `severity` | string | no | One of `error` (default), `warning`, `info`. Unknown values are rejected. |
| `hint` | string | no | Remediation hint included in the diagnostic. Keep concise; split long hints with `\n`. |
| `with` | map | no | Parameters passed to the builtin or user command. |

Schema rules enforced at load time:

- `id` must be unique across entries.
- `stages` must be non-empty.
- `type` must be `builtin` or `command`.
- `severity` must be `error` / `warning` / `info` if present.
- `with:` shape validity (required keys, types) is checked against the target builtin's `Validate` method — failures surface as `checks.<id>` diagnostics at run time, not as load errors.

## Stages

A check runs whenever its `stages` list contains a stage the caller asked for. The CLI defines four reserved stages with built-in hooks:

| Stage | Triggered by |
|-------|--------------|
| `deploy` | `devbox deploy run`, `devbox validate --stage deploy` |
| `run` | `devbox run`, `devbox restart` (run leg), `devbox validate --stage run` |
| `stop` | `devbox stop`, `devbox restart` (stop leg), `devbox validate --stage stop` |
| `command` | `devbox validate --stage command` (reserved for future use; no automatic hook) |

`devbox validate` without `--stage` runs every check regardless of stage.

Stages outside the reserved set are accepted (open enum) but produce an info-level diagnostic at load time: `stage "X" not bound to built-in hooks`. They can still be invoked explicitly with `devbox validate --stage <name>`.

## Available builtins

All five builtins live in `internal/builtin/` and are usable both as `type: builtin` check entries and as deploy step bodies / `check:` action blocks.

### `shell`

Runs a shell command via hardcoded `sh -c` (matching the deploy `when:` predicate convention). Exit 0 = pass.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `cmd` | string | yes | — | Shell command body. |
| `timeout` | duration | no | `10s` | Maximum execution time. |

Error message on non-zero exit: `exit status N: <last line of stderr>`.

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

### `tcp_reachable`

Attempts a TCP dial to `host:port`.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `host` | string | yes | — | Hostname or IP. |
| `port` | int | yes | — | Port in range 1–65535. |
| `timeout` | duration | no | `3s` | Dial timeout. |

## `type: command` checks

A check entry with `type: command` dispatches to a declarative user command from `devbox/commands/`. The `with:` block is passed through as the user command's `params:` payload — exactly like `devbox commands <id> --set k=v`.

Restrictions enforced at load time:

- The target command's `type:` MUST be `shell` or `script`. Workflow, service_exec, service_run, devbox, and builtin-as-command targets are rejected with: `checks may only invoke user commands of type shell or script (got: <type>)`.
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
    hint: Download from s3://team-dumps/latest.sql and place at .devbox/seed.sql
    type: builtin
    cmd: file_exists
    with:
      path: .devbox/seed.sql
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

**4. Corporate VPN reachable (tcp_reachable):**

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

**5. Project dependency script (type: command):**

```yaml
  - id: project-deps
    description: Required CLIs installed (./scripts/check-deps.sh)
    stages: [run]
    type: command
    cmd: deps.check
```

Where `devbox/commands/deps.yml` declares:

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

**6. Compose plugin v2 only (executable_in_path):**

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

- `devbox validate` — runs `config.*`, `templates.*`, `commands.*`, `env.*`, and all `checks.*`. Optional positional scope narrows the run (e.g. `devbox validate env`, `devbox validate checks ghcr-login`).
- `devbox validate --stage <name>` — local flag on the `validate` command. Filters `checks.*` by stage. `env.*` and other domains are unaffected (they have no stages).
- `devbox validate --strict` — treat warnings as errors (exit 1).
- `devbox validate --quiet` — hide ok / info rows.
- `--skip-preflight` — local flag on `deploy run`, `run`, `stop`, and `restart`. When set, preflight prints `preflight skipped (--skip-preflight)` to stderr and runs NO validators. The flag is a true bypass: `type: command` checks invoke arbitrary user scripts, so the CLI does not run them under a flag the user named "skip".

## Diagnostic output

Diagnostics share the rendering and severity model used by the rest of `devbox validate`:

- `Severity`: from `entry.severity` (default `error`).
- `Domain`: `checks` (or `env` for hardcoded probes).
- `Target`: the entry's `id`.
- `File`: `devbox/validate.yml` (entries) or empty (env probes).
- `Line`: 1-based line number of the entry's first key (entries).
- `Message`: builtin / command error string.
- `Hint`: from `entry.hint`.

Preflight writes the same diagnostic table to stderr before failing with exit code 1. Use `\n` in hints to split long remediation text across lines — the Lipgloss table honors newlines.

## Related commands

- `devbox validate` — full validation run (all domains).
- `devbox validate env` — env probes only.
- `devbox validate checks [id]` — declarative checks (optional id narrows to one).
- `devbox deploy run` / `run` / `stop` / `restart` — invoke preflight automatically (see `--skip-preflight`).

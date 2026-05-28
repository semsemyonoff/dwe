# validate.yml

Project readiness checks.

## Contents

- [Purpose](#purpose)
- [Validation domains](#validation-domains)
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
- [External linters](#external-linters)
- [Related commands](#related-commands)

## Purpose

`devbox/validate.yml` declares project-level readiness checks. The CLI consumes these from two entry points:

- `devbox validate` — runs every check (plus YAML-shape validators in the `config`, `templates`, and `commands` domains, plus environment probes in the `env` domain) and reports diagnostics.
- Preflight hook on `devbox deploy run`, `devbox run`, `devbox stop`, and `devbox restart` — runs the subset of checks bound to the relevant stage before any side effect on Docker, git, or the filesystem.

The goal is to surface user-actionable problems ("you're not logged into ghcr.io", "DATABASE_URL is empty in `.env`", "VPN is down") BEFORE deploy steps fail mid-way with cryptic errors.

## Validation domains

The validate command runs three domains in addition to the existing YAML-shape validators:

| Domain | Source | Configurable? |
|--------|--------|---------------|
| `env.*` | Hardcoded in Go (`internal/validate/env/`) | No — seven fixed probes |
| `checks.*` | `devbox/validate.yml` entries | Yes — declarative |
| `linters.*` | Built-in adapters (shellcheck, hadolint) + `devbox/validate.yml` `linters:` block | Yes — declarative |
| `snapshot.*` | On-disk snapshot directories + `devbox/snapshot.yml` | No — fixed validators per snapshot name |

The `env.*` probes are: `env.docker_bin`, `env.docker_daemon`, `env.docker_compose`, `env.git_bin`, `env.shell_bin`, `env.project_perms`, `env.ports_free`. They run on every `devbox validate` invocation and on every preflight (regardless of stage — env has no stage concept), with one exception: `env.ports_free` self-skips on the `stop` stage since port conflicts are irrelevant when winding the project down.

`env.ports_free` reads every host port declared under `services.<name>.ports` (enabled services only) and checks whether each is bindable. It queries `docker ps --format=json` once to learn which containers currently hold which ports: containers labelled `com.docker.compose.project=<our project>` are treated as "ours" (compose will reuse them on `up`); containers from any other compose project trigger a conflict diagnostic that names the foreign container and project; for ports not held by any container the probe falls back to `net.Listen` to detect non-Docker processes. Docker unreachability falls through silently — `env.docker_daemon` covers that case.

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

Unknown stages are accepted (open enum) but produce a **warning** at load time so users catch typos early:

- `stage "deplooy" is not a known preflight stage` (with a suggestion if close via Levenshtein distance)
- Special notes: `restart` is composite (uses both stop and run stages, no separate preflight); `reset` uses the stop stage only

Unknown stages can still be invoked explicitly with `devbox validate --stage <name>` if needed (e.g. for custom validation workflows).

## Available builtins

All five builtins live in `internal/builtin/` and are usable both as `type: builtin` check entries and as deploy step bodies / `check:` action blocks.

### `shell`

Runs a shell command via hardcoded `sh -c` (matching the deploy `when:` predicate convention). Exit 0 = pass. This builtin uses POSIX-portable `sh -c` regardless of the project's configured shell, ensuring checks run identically across all environments.

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `cmd` | string | yes | — | Shell command body. |
| `timeout` | duration | no | `10s` | Maximum execution time. |

Error message on non-zero exit: `exit status N: <last line of stderr>`.

See [deploy.md: `cmd: shell` vs `type: shell`](deploy.md#cmd-shell-builtin-vs-type-shell-step) for the distinction between this builtin and the `type: shell` step execution type.

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

## External linters

The `linters.*` domain runs well-known external linters (shellcheck, hadolint) and arbitrary `type: generic` adapters as part of `devbox validate`. Linters do **not** run in preflight — preflight answers "can we run?", not "is the code clean?".

### Wire layout

```yaml
linters:
  shellcheck:
    enabled: true
    bin: shellcheck
    paths: [devbox/scripts, scripts]
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
| `shellcheck` | `shellcheck` | `devbox/scripts`, `scripts` | `.sh`, `.bash` | — | `--format`, `-f` |
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

You can override the binary path for any linter using your user-level config file (`~/.config/devbox/config`). This is useful when you have custom installations, replacements (e.g., `podman` instead of `docker`), or binaries outside the default PATH.

Add a line to your user config:

```
binary_shellcheck=/custom/path/to/shellcheck
binary_hadolint=/opt/hadolint
```

The format is `binary_<linter-id>=<path>`. Paths are absolute or relative to your current directory. If the path does not exist or is not executable, `devbox validate` will emit an error diagnostic in the `linters` domain.

**Note:** These overrides are **only** consulted during `devbox validate`. Lifecycle commands (deploy, run, stop, etc.) do not use linter binaries, so broken overrides do not affect normal operation.

### Scope

Run all linters or narrow to one with the `linters` subcommand:

```
devbox validate                       # all domains (including linters)
devbox validate linters               # all linters
devbox validate linters shellcheck    # only shellcheck
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
- Missing **default** paths (e.g. shellcheck's `devbox/scripts` in a project that has none) are silently dropped. Missing **user-configured** paths (entries the user wrote explicitly) produce a Warning.

### Trust model

`bin:` is restricted to a bare command name resolved via `PATH` at runtime; absolute and relative paths are forbidden at load time. Rationale: `validate.yml` ships with the repo; a malicious config with `bin: ./scripts/evil.sh` should not silently execute arbitrary code on `devbox validate`. Users who genuinely need a custom binary path install it on `PATH` (or wrap it).

## Related commands

- `devbox validate` — full validation run (all domains).
- `devbox validate env` — env probes only.
- `devbox validate checks [id]` — declarative checks (optional id narrows to one).
- `devbox validate linters [id]` — external linters (optional id narrows to one).
- `devbox deploy run` / `run` / `stop` / `restart` — invoke preflight automatically (see `--skip-preflight`). Linters do **not** run in preflight.

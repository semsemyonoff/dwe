# setup.yml

Interactive setup questions for fresh projects.

## Contents

- [Purpose](#purpose)
- [How it works](#how-it-works)
- [Structure](#structure)
- [Top-level fields](#top-level-fields)
- [Question entry fields](#question-entry-fields)
- [Question types](#question-types)
- [Validation presets](#validation-presets)
- [Write scope rules](#write-scope-rules)
- [Examples](#examples)
- [Related commands](#related-commands)

## Purpose

`workspace/setup.yml` defines interactive prompts that run when a developer first enters a fresh project (one without a `workspace/local.yml` or with an empty one). The wizard collects answers, writes them into `workspace/local.yml` as merged settings, and then proceeds to deployment.

Use setup questions for one-time per-developer configuration:
- API keys or secrets (stored in `local.yml`, which is gitignored)
- Service toggles (which optional tools the developer wants to enable)
- Port overrides (when local port conflicts exist)
- Custom paths or hostnames

The setup wizard is part of the broader `dwe deploy` flow — running `dwe deploy` with no subcommand opens an interactive menu that includes a Wizard option when setup questions are present.

## How it works

1. Developer runs `dwe deploy` in an interactive terminal on a fresh project.
2. The CLI probes for port conflicts and loads `workspace/setup.yml` (if present).
3. If both are empty (no questions, no conflicts), no wizard runs — proceed directly to deploy.
4. If either has content, the menu opens with a **Wizard** option.
5. Wizard runs:
   - First, port-conflict prompts (if any) — developer chooses override ports.
   - Then, setup questions (if any) — developer answers each prompt.
   - Finally, both answers are deep-merged into `workspace/local.yml` and written atomically.
6. Config is reloaded from the updated `local.yml`, preflight runs, and deploy proceeds normally.

If the developer cancels at any wizard step (Ctrl-C), `local.yml` is left untouched — no partial writes.

## Structure

```yaml
questions:
  - id: api-key
    title: GitHub Token
    description: Personal access token for private repos (optional)
    type: input
    required: false
    writes: db.api_key

  - id: enable-postgres
    title: Enable PostgreSQL?
    type: confirm
    required: false
    writes: services.postgres.enabled

  - id: http-port
    title: Web server port
    description: Port to run the local app on
    type: input
    required: true
    writes: services.web.ports.http
    validate:
      preset: port

  - id: select-locale
    title: Preferred language
    type: select
    required: true
    writes: app.locale
    options:
      - value: en
        label: English
      - value: fr
        label: Français
      - value: de
        label: Deutsch

  - id: enable-caching
    title: Use Redis caching?
    type: confirm
    writes: app.cache.enable
```

The file is optional. When absent, the wizard (if invoked) handles port conflicts only.

## Top-level fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `questions` | list | yes | Question entries (see below). May be empty. |

Unknown top-level fields are rejected at load time (strict decoding).

## Question entry fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Unique identifier for this question. Used as the key when collecting answers. |
| `type` | string | yes | One of `input`, `select`, `multiselect`, `confirm`. Unknown values are rejected by validation. |
| `title` | string | yes | Prompt text shown to the developer. |
| `description` | string | no | Longer explanation shown below the title. |
| `required` | bool | no | If true (default false), the wizard enforces a non-empty answer before proceeding. For `confirm`, `required` is ignored (always optional). |
| `writes` | string | yes | Dot-path where the answer is stored in `local.yml`. Must be unique across all questions. See [Write scope rules](#write-scope-rules). |
| `options` | list | no | Valid for `select` and `multiselect` only. List of `{value, label}` pairs. Required for both types. |
| `validate` | object | no | Optional validation rules. Has two fields (mutually exclusive): `preset` (a named preset like `port` / `hostname`) or `regex` (a regular expression pattern). Only meaningful for `type: input`. |

Schema rules enforced at load time:

- `id` must be unique across entries.
- `writes` must be unique across entries and follow dot-path syntax rules (see below).
- Unknown top-level fields inside a question are rejected.

Schema rules enforced by validation (run via `dwe validate`):

- `type` must be one of the four known values.
- `writes` must follow the scope and syntax rules below.
- `validate.preset` and `validate.regex` cannot both be set.
- `validate.*` is only meaningful for `type: input`; setting either on `select`, `multiselect`, or `confirm` is an error.
- `select` and `multiselect` must have non-empty `options` with unique, non-empty `value` strings.
- Service-overlay writes have type consistency rules (see [Write scope rules](#write-scope-rules)).

## Question types

### `input`

A free-text input field with optional validation.

**Returns**: `string` (or `int` if a numeric preset is used — see [Validation presets](#validation-presets))

**Example**:
```yaml
- id: db-password
  type: input
  title: Database password
  required: true
  writes: db.password
```

### `select`

A single-choice dropdown. Developer picks one value.

**Returns**: `string` (the chosen option's `value`)

**Example**:
```yaml
- id: log-level
  type: select
  title: Logging level
  required: true
  writes: app.log_level
  options:
    - value: debug
      label: Debug (verbose)
    - value: info
      label: Info (normal)
    - value: error
      label: Error (quiet)
```

### `multiselect`

A multi-choice list. Developer picks zero or more values.

**Returns**: `[]string` (slice of chosen `value` fields)

**Example**:
```yaml
- id: plugins
  type: multiselect
  title: Plugins to enable
  writes: app.plugins
  options:
    - value: auth
      label: Authentication
    - value: logging
      label: Logging
    - value: metrics
      label: Metrics
```

### `confirm`

A yes/no toggle.

**Returns**: `bool` (`true` for yes, `false` for no)

**Example**:
```yaml
- id: enable-debug
  type: confirm
  title: Enable debug mode?
  writes: app.debug
```

Note: `required: true` on a confirm is a no-op and produces a validation warning. A confirm always yields a valid answer (either true or false).

## Validation presets

Presets are shorthand validators for common patterns. Each preset defines what values are accepted AND what Go type is written to `local.yml`.

Use `validate: { preset: <name> }` inside a question's `validate` block.

### `port`

Validates a port number (1–65535) and writes an `int`.

```yaml
- id: http-port
  type: input
  title: Web server port
  writes: services.web.ports.http
  validate:
    preset: port
```

The developer's input `"8080"` is stored as the integer `8080` in `local.yml`, so templates can use it as a number.

### `hostname`

Validates a DNS hostname (RFC 1123 short-name format) and writes a `string`.

```yaml
- id: postgres-host
  type: input
  title: Postgres hostname
  writes: services.postgres.hosts.internal
  validate:
    preset: hostname
```

### `path`

Validates a non-empty filesystem path and writes a `string`.

```yaml
- id: workspace-dir
  type: input
  title: Workspace directory
  writes: app.workspace
  validate:
    preset: path
```

### `non-empty`

Validates that the input is not blank (whitespace-only is rejected) and writes a `string`.

```yaml
- id: api-key
  type: input
  title: API key
  writes: db.api_key
  validate:
    preset: non-empty
```

### No preset, no regex

If neither `preset` nor `regex` is set, the input is accepted as-is (any non-empty string when `required: true`, any string otherwise).

```yaml
- id: app-name
  type: input
  title: Application name
  writes: app.name
  # No validation; any input is accepted
```

### Custom regex

Validate the input against a regular expression pattern. The input must match the pattern in full.

```yaml
- id: email
  type: input
  title: Email address
  writes: user.email
  validate:
    regex: "^[a-z0-9+._-]+@[a-z0-9.-]+$"
```

Pattern must compile as a valid Go regex. Invalid patterns are caught by `dwe validate` before the wizard ever runs.

## Write scope rules

The `writes:` field is a dot-path that determines where in `workspace/local.yml` the answer is stored. Not all paths are allowed — the wizard enforces rules to ensure answers merge safely with the config schema.

### Forbidden top-level namespaces

These top-level keys are reserved and cannot be written by the wizard:

- `info.*` — immutable project metadata
- `styles.*` — UI color configuration
- `docker.*` — engine policy configuration
- `binaries.*` — binary override configuration

Attempting to write to any of these triggers a validation error.

### Service-overlay leaf shapes

When writing under `services.<name>.`, only three exact leaf paths are allowed:

| Path | Type | Question type | Description |
|------|------|---------------|-------------|
| `services.<name>.enabled` | `bool` | `confirm` (required) | Toggle service enabled state. |
| `services.<name>.ports.<port_name>` | `int` | `input` with `preset: port` (required) | Override a declared service port. |
| `services.<name>.hosts.<host_name>` | `string` | `input` (any preset OK) | Override a declared service hostname. |

Examples of **allowed** writes:
- `services.web.enabled` — must come from a `type: confirm` question
- `services.web.ports.http` — must come from a `type: input` with `validate.preset: port`
- `services.postgres.hosts.internal` — can come from any `type: input`

Examples of **forbidden** writes:
- `services.web` (missing the leaf `.enabled` / `.ports.X` / `.hosts.X`) — would overwrite the entire service config
- `services.web.ports` (missing the specific port name) — would overwrite all ports
- `services.web.container` — not in the allowed leaf set
- `services.web.ports.http` from a `type: select` — wrong question type for the path

Validation error messages cite the specific constraint that failed (e.g., "service ports require `type: input` with `validate.preset: port`").

### Non-service paths

Anywhere else in `local.yml` (top-level custom keys, `db.*`, `app.*`, etc.), any dot-path is allowed and any question type is fine:

```yaml
- writes: db.name                    # ✓ allowed
- writes: db.connection.host         # ✓ allowed
- writes: db.connection.port         # ✓ allowed
- writes: app.feature_flags          # ✓ allowed
- writes: custom.setting             # ✓ allowed
```

The wizard writes the typed answer value verbatim (string for `input` / `select` / `confirm`, slice for `multiselect`) and trusts the consuming config (templates, exports, etc.) to handle it appropriately.

## Examples

### Minimal setup with just a port override

```yaml
questions: []
```

If port conflicts exist, the wizard opens and prompts for overrides. No question entries needed.

### API key + service toggle

```yaml
questions:
  - id: github-token
    type: input
    title: GitHub personal access token
    description: Used for private repo access. Leave blank to skip.
    required: false
    writes: secrets.github_token
    validate:
      preset: non-empty

  - id: enable-postgres
    type: confirm
    title: Enable PostgreSQL?
    writes: services.postgres.enabled
```

### Service with custom hostname

```yaml
questions:
  - id: database-host
    type: input
    title: Database hostname
    description: The address where your database lives
    required: true
    writes: services.postgres.hosts.internal
    validate:
      preset: hostname

  - id: db-port
    type: input
    title: Database port
    required: true
    writes: services.postgres.ports.db
    validate:
      preset: port
```

### Multi-choice plugins

```yaml
questions:
  - id: enabled-plugins
    type: multiselect
    title: Plugins to enable
    description: Select any combination (space to toggle, enter to confirm)
    required: false
    writes: app.plugins
    options:
      - value: auth
        label: Authentication
      - value: analytics
        label: Analytics
      - value: export
        label: Export to S3
      - value: webhooks
        label: Webhooks
```

### Complex custom namespace

```yaml
questions:
  - id: workspace-root
    type: input
    title: Workspace root directory
    required: true
    writes: workspace.root
    validate:
      preset: path

  - id: cache-backend
    type: select
    title: Cache backend
    required: true
    writes: cache.backend
    options:
      - value: redis
        label: Redis
      - value: memcached
        label: Memcached
      - value: local
        label: Local (in-memory, not persistent)

  - id: enable-profiling
    type: confirm
    title: Enable performance profiling?
    writes: debug.profiling
```

## Related commands

- `dwe deploy` — opens the wizard menu on fresh projects
- `dwe validate` — checks `workspace/setup.yml` schema and writes paths
- `dwe validate setup` — validates only the setup domain

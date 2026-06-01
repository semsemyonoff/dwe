# Step execution types

Every leaf pipeline step declares a `type:` that selects how its `cmd:` is executed.

## Contents

- [`type: shell`](#type-shell)
- [`cmd: shell` (builtin) vs `type: shell` (step)](#cmd-shell-builtin-vs-type-shell-step)
- [`type: dwe`](#type-dwe)
- [`type: command`](#type-command)
- [`type: builtin`](#type-builtin)

## `type: shell`

Executes a shell command via `sh -c`. Full shell semantics apply: environment variable expansion, globbing, pipes, redirection, and `&&`/`||` operators all work as expected.

```yaml
- name: chmod-scripts
  type: shell
  cmd: chmod +x scripts/deploy.sh
```

## `cmd: shell` (builtin) vs `type: shell` (step)

The `shell` builtin (`cmd: shell`) is **distinct** from the step execution type (`type: shell`). Both execute shell commands, but with different portability guarantees:

**Step type: `type: shell`** — Uses the project's configured shell (via `config.ShellBin`) for maximum flexibility. If the project has set a custom shell binary (e.g., `zsh` instead of `sh`), step bodies use that shell.

```yaml
- name: run-with-project-shell
  type: shell
  cmd: some-zsh-specific-feature-here
```

**Builtin: `cmd: shell`** — Uses POSIX-portable hardcoded `sh -c` for maximum predictability. Used in two contexts:

1. **As a step body** (less common):

```yaml
- name: check-docker-login
  type: builtin
  cmd: shell
  with:
    cmd: docker info | grep -q ghcr.io
    timeout: 10s
```

2. **As a pre/post-condition** (common in deploy and validate):

```yaml
- name: copy-configs
  type: builtin
  cmd: service_configs_copy
  # ...
  when:
    type: shell
    cmd: "test -f templates/config.default"

  check:
    type: builtin
    cmd: shell
    with:
      cmd: "test -f services/main/configs/app.conf"
```

Both usages ensure that conditions evaluate portably across CI systems, container runtimes, and developer shells, regardless of the project's `config.ShellBin` setting. See [validate.yml](../validate.md#shell) for the full `cmd: shell` builtin documentation.

## `type: dwe`

Invokes a DWE CLI subcommand. The binary path is resolved automatically.

```yaml
- name: up
  type: dwe
  cmd: "docker up"

- name: info
  type: dwe
  cmd: "info"

- name: render-ide
  type: dwe
  cmd: "render ide main"
```

## `type: command`

Dispatches a declarative command by ID from the command registry (`workspace/commands/`).

```yaml
- name: composer-install
  type: command
  cmd: services.main.composer-install

- name: db-create
  type: command
  cmd: services.main.db.create
  with:
    database: laravel_test
```

## `type: builtin`

Executes an engine-internal Go function. Builtins run in-process and have access to the full config. The same registry is reachable from declarative commands via [`type: builtin` in `commands/`](../commands/types.md#type-builtin) — pipelines and commands share one set of builtins.

```yaml
- name: create-dirs
  type: builtin
  cmd: service_dirs_ensure
  with:
    service: main
    mode: skip

- name: success-msg
  type: builtin
  cmd: message
  with:
    level: success
    text: "Deploy completed"
```

See [Available builtins](builtins.md) for the full registry and parameter reference.

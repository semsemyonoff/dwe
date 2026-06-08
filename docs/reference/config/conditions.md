# Conditions and Actions

Typed conditions (`when:`) and typed actions (`check:` / step bodies) in pipelines.

## Contents

- [Overview](#overview)
- [Typed conditions (`when:`)](#typed-conditions-when)
  - [`type: builtin` — predicates](#type-builtin--predicates)
  - [`type: shell` — shell commands](#type-shell--shell-commands)
  - [`type: template` — Go templates](#type-template--go-templates)
- [Typed actions (`check:` and step bodies)](#typed-actions-check-and-step-bodies)
- [Two `type: builtin` registries](#two-type-builtin-registries)
- [Workflow conditions (string-based, separate)](#workflow-conditions-string-based-separate)
- [Related documentation](#related-documentation)

## Overview

**Conditions** (`when:`) are **pre-conditions** evaluated before a phase or step runs. They return a boolean: true = proceed, false = skip.

**Actions** are **payloads** that execute code. When used as `check:` (post-action), their success/failure determines whether the step passed or failed.

The pipeline system uses **typed** forms for both — a `type:` field dispatches to different evaluators. Workflow steps (inside command definitions) use a separate string-based `when:` form documented in [commands/](commands/index.md).

```
Pipeline steps (typed):
  when: { type: builtin|shell|template, cmd: ..., expr: ... }
  check: { type: shell|dwe|command|builtin, cmd: ..., with: ... }

Workflow steps (string-based — separate, not covered here):
  when: "dir-empty path" | "{{ ... }}" | "cmd: ..."
  command: <id>
```

## Typed conditions (`when:`)

`when:` on a phase or pipeline step is a **typed condition** with three forms. It is evaluated before the phase/step runs; a falsy result skips it.

### `type: builtin` — predicates

Builtin conditions test filesystem state using the **predicate registry**. Predicates are distinct from engine builtins (like `service_configs_copy`) — they live in a separate namespace and cannot be used in `check:` actions.

```yaml
when:
  type: builtin
  cmd: "dir-empty services/main/src"
```

**Available predicates** (path is project-root-relative):

| Predicate | True when |
|-----------|-----------|
| `dir-exists <path>` | path is an existing directory |
| `dir-missing <path>` | path is missing or not a directory |
| `dir-empty <path>` | path is missing or has no entries |
| `dir-not-empty <path>` | path is a directory with at least one entry |
| `file-exists <path>` | path is an existing regular file |
| `file-missing <path>` | path is missing or not a regular file |
| `generated-missing <svc> <field>` | the `<field>` value is absent from the generated-value store (`.dwe/generated.yml`), or the store file is missing |

Unlike the other predicates, `generated-missing` takes **two** sub-arguments — a service name and a generated-field name — rather than a path. It reads the durable per-service store at `.dwe/generated.yml` and is used to gate a service's secret-generation step so it runs only on the first deploy (when no value has been harvested yet). See [services/fields.md](services/fields.md) for the `generated:` declaration and [render/config.md](../render/config.md) for the harvest/replay flow.

**Portability:** Predicates are evaluated through hardcoded `sh -c` (not the project's configured shell binary) to ensure POSIX portability and consistency regardless of the project's shell choice.

### `type: shell` — shell commands

Shell conditions execute a command and test its exit code: exit 0 = true, non-zero = false.

```yaml
when:
  type: shell
  cmd: "test -f services/main/src/vendor/autoload.php"
```

Full shell semantics apply: pipes, redirection, operators, etc. Like predicates, shell conditions use hardcoded `sh -c` for portability.

### `type: template` — Go templates

Template conditions are evaluated at **plan time** using Go `text/template` syntax. They do not support `check:` in the same step (no side effects before execution).

```yaml
when:
  type: template
  expr: "{{ .Services.second.Enabled }}"
```

Template conditions are purely for idempotency checks known at plan time:

```yaml
- name: setup
  when:
    type: template
    expr: "{{ not .Services.database.Enabled }}"
  steps: []
```

The render context includes the full resolved project config, so you can reach any configuration value. See [Templates](../templates.md) for the template expression syntax and helper reference.

## Typed actions (`check:` and step bodies)

Actions are **executable payloads** — the same `type: shell|dwe|command|builtin` shape used in step bodies. When used as a `check:` post-action, the action's success/failure determines the step's success/failure.

```yaml
- name: copy-configs
  type: builtin
  cmd: service_configs_copy
  with:
    service: main
    mode: replace
  check:
    type: builtin
    cmd: service_configs_check
    with:
      service: main
```

Actions support four executor types:

| Type | Executor | Example |
|------|----------|---------|
| `shell` | `sh -c` | `type: shell, cmd: "test -f file.txt"` |
| `dwe` | DWE CLI | `type: dwe, cmd: "docker up"` |
| `command` | Command registry | `type: command, cmd: "services.main.migrate"` |
| `builtin` | Engine builtin | `type: builtin, cmd: "service_configs_check"` |

See [deploy/conditions.md](deploy/conditions.md) for the full action reference and the semantics of `check:` failures under `continue_on_error`.

## Two `type: builtin` registries

The pipeline system has **two separate `type: builtin` namespaces**, disambiguated by YAML position:

1. **Predicates** — used in `when: type: builtin`. Filesystem-state tests like `dir-empty`, `file-exists`.
2. **Engine builtins** — used in step bodies and `check: type: builtin`. Executable actions like `service_configs_copy`, `service_configs_check`, `message`.

Example of the distinction:

```yaml
phases:
  - name: setup
    when:                         # when: uses the PREDICATE registry
      type: builtin
      cmd: "dir-empty src"
    steps:
      - name: copy
        type: builtin             # step body uses the ENGINE BUILTIN registry
        cmd: service_configs_copy
        with:
          service: main
      - name: verify
        check:                     # check: uses the ENGINE BUILTIN registry
          type: builtin
          cmd: service_configs_check
          with:
            service: main
```

`dir-empty` is not an engine builtin (not available as a step body or check). `service_configs_copy` is not a predicate (not available in `when:`).

## Workflow conditions (string-based, separate)

Workflow steps use a separate string-based condition mini-language. The full grammar is documented in [commands/](commands/index.md); this section only sketches the surface for context.

```yaml
# Workflow (string-based, separate system)
steps:
  - command: services.main.migrate
    when: "file-missing services/main/src/vendor/autoload.php"

  - confirm: "Proceed?"
    when: "{{ if .Params.confirm }}1{{ else }}0{{ end }}"

  - command: cleanup
    when: "cmd: test -d /tmp/workdir"
```

Workflow conditions are classified by leading prefix (`{{ ... }}` → template, `cmd: ...` → shell command, otherwise → predicate). See [commands/](commands/index.md) for the full workflow grammar.

## Related documentation

- [deploy](deploy/index.md) — pipeline `when:` and `check:` syntax with examples
- [lifecycle.md](lifecycle.md) — lifecycle pipelines (same step/condition grammar as deploy)
- [commands/](commands/index.md) — command definitions (separate system; workflows keep string-based `when:`)
- [Templates](../templates.md) — Go template syntax, sprout helpers, render contexts

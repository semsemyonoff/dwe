# Validation rules and common pitfalls

The loader enforces the rules below and reports the offending file + field on failure. The pitfalls list collects the mistakes that bite most often in real projects.

## Contents

- [Validation rules (cheat sheet)](#validation-rules-cheat-sheet)
- [Common pitfalls](#common-pitfalls)

## Validation rules (cheat sheet)

- `type` is required and must be one of the documented values.
- `type: shell` requires exactly one of `cmd` / `argv`; `service_*` requires exactly one of `cmd` / `argv` plus `service`.
- `type: script` requires a `script:` block in either simple (`path`) or phased (`run` + optional `plan` / `cleanup`) form.
- `type: workflow` requires a non-empty `steps:` and forbids type-specific fields (`cmd`, `argv`, `service`, `script`, `workdir`, etc.).
- `type: builtin` requires `cmd:` (the builtin name) and rejects type-specific fields of other types (`argv`, `script:`, `steps:`, `service`, `compose_args`, `workdir` / `workdir_from`, `user`, `mode`, `runner:`).
- Each workflow step has exactly one of `command` / `confirm` / `parallel`; `with` / `continue_on_error` are valid only on command steps (and on the container of a `parallel` block — see [Parallel sub-steps](types.md#parallel-sub-steps)).
- Env variable names must be unique across `params.*.env`, `context.*.env`, `files.*.env`, and the `env:` block.
- File IDs must match `^[a-zA-Z_][a-zA-Z0-9_]*$`.
- File specs reject conflicting fields (e.g. `mkdir` outside `write`, `path` + `candidates`, `match` / `sort` without `glob`).
- `workdir_from` is only valid for `service_exec` / `service_run`.
- `compose_args` is only valid for `service_exec` / `service_run`.
- `mode` on `service_run` must be empty or `run`.
- `notify: true` is rejected on `type: daemon` (error). `notify: true` on a direct sub-step inside a `parallel:` block produces an info diagnostic; the runtime suppresses it.

## Common pitfalls

- **Don't shell out to `./bin/devbox`** — use `type: devbox` or `$DEVBOX_BIN` (in scripts). Either form picks up the running binary, even when the build path changes.
- **Don't put secrets in `argv`** — use `env:` so values are injected through the container env, not the command line.
- **Don't reuse env names across sources** — declaring `MYSQL_PWD` in both `params.x.env` and `env:` is a load-time error.
- **Don't write to a path without `mkdir: true`** — write mode does not create parents on its own.
- **Don't expect `${...}` inside `params.*.default_from` / `context.*.from`** — those are plain dot-paths, not templated.
- **Don't run a private command directly** — reference it from a workflow or pipeline, or temporarily flip `private: false` for debugging.

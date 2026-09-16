# Authoring DWE pipelines & orchestration

Load when the task is "customize deploy/run/stop", "add a setup question", "add a preflight check", a post-up hook, or the info dashboard / branding. Covers per-service & project `deploy.yml`, `lifecycle.yml`, `setup.yml`, `validate.yml`, `info.yml` / `styles.yml`, `docker.yml`.

Edit the yml, show the diff, hand the user the apply command (`SKILL.md` § Picking the apply command). `dwe deploy plan` / `dwe reset plan` / `dwe validate` / `dwe info` are reads.

## 1. Full replacement (load-bearing)

`workspace/deploy.yml`, `workspace/lifecycle.yml` and `workspace/reset.yml` are optional; absent = built-in default. A present, active file **replaces the whole pipeline section — it does not merge**; a half-edited override silently drops every phase you did not copy. `dwe init` ships them as inert, fully-commented mirrors, and `dwe validate` reports that state as `ⓘ` (absent / inert / declares no phases), not OK.

To customize: `dwe deploy eject --out workspace/deploy.yml --force` (`dwe reset eject` for reset) writes the built-in default as an editable document (mutating — hand it over); keep every phase you still want; preview with `dwe deploy plan --output json`.

```shell
dwe docs show concepts/pipelines --lang en
dwe docs show config/deploy/index --lang en      # + steps, builtins, conditions, examples
dwe docs show config/lifecycle --lang en
dwe docs show config/reset --lang en
```

## 2. Per-service `deploy.yml` — the workhorse

Prefer `workspace/services/<name>/deploy.yml` over a project-level pipeline: the built-in project pipeline inlines every enabled service's file in dependency order (`deploy_services: true`) and ends with `docker up --wait`. Shape: `phases: [{name, description, steps}]`; a step is `type:` `shell` (host `sh -c`) · `dwe` (a subcommand string: `"docker build main"`, `"render ai"`) · `command` (a declared command ID) · `builtin` (`cmd:` + `with:`). Optional per-step keys: `name`, `description`, `when:`, `check:`, `files_gate:`, `continue_on_error`, `untracked`, `timeout:` (`30s` / `2m`; bounds the body only).

**Gate the step, never the phase.** Per-step gates keep `dwe deploy run` re-runnable after a mid-pipeline failure — only unfinished steps execute.

`when:` is typed: `{type: builtin, cmd: "<predicate> <args>"}` (the seven condition predicates — `SKILL.md` § Rules), `{type: shell, cmd: "<sh test>"}`, or `{type: template, expr: '{{ ne .Raw.vars.x "" }}'}`. `check:` takes the same shape as a step body (a step builtin, `type: shell`, …), or the scalar **`check: auto`** = the inverse of the step's own shell `when:` (write the predicate once; only valid with `when: {type: shell}`). A step with a `check:` re-runs every deploy.

Step builtins you will actually use: `service_dirs_ensure` · `source_clone` · `service_configs_render` (+ `service_configs_render_check` as its `check:`) · `service_generated_harvest` · `containers_running` · `docker_wait_healthy` · `http_check` · `remove_paths` · `message`. **Predicate builtins as a step body are assertions** (`http_check`, `containers_running`, `file_exists`, `tcp_reachable`, `env_keys_present`, `config_keys_present`, `shell`): false fails the step, and such a step always re-runs. Catalogue: `dwe docs show config/deploy/builtins --lang en`.

### Canonical skeleton

Each step is independently idempotent, gated by filesystem truth — the shape `dwe init` scaffolds and real projects converge on:

```yaml
phases:
  - name: source
    steps:
      - name: dirs
        type: builtin
        cmd: service_dirs_ensure
        with: { service: <name> }
      - name: clone                 # self-gating: skips an existing checkout, never re-points it
        type: builtin
        cmd: source_clone
        with:
          repo: "${vars.source.<name>.repo}"
          dir: services/<name>/src
          branch: "${vars.source.<name>.branch}"
      - name: render-configs        # only when the service has a config pack
        type: builtin
        cmd: service_configs_render
        with: { service: <name> }
        check:                      # re-render every deploy (template / vars edits apply)
          type: builtin
          cmd: service_configs_render_check
          with: { service: <name> }
  - name: image
    steps:
      - name: build
        type: dwe
        cmd: "docker build <name>"
  - name: bootstrap
    steps:
      - name: install               # a declared command, not a pasted shell line
        type: command
        cmd: services.<name>.install
        when:
          type: builtin
          cmd: "file-missing services/<name>/src/vendor/autoload.php"
  - name: finalize
    steps:
      - name: up
        type: dwe
        cmd: "docker up <name> --wait"
        check:                      # self-heal: re-up when not running
          type: builtin
          cmd: containers_running
          with: { services: [<container>] }
```

Notes:

- `source_clone` needs no `when:`/`check:` — the gate is built in. Clone coordinates live under `vars:` (`render-and-vars.md`).
- `containers_running` `with.services` are **compose service names** (`container:`), not folder keys.
- Hub artefacts (`render ai`, `render ide`) belong in the **project** pipeline as one argument-less step (`type: dwe`, `cmd: "render ai"` walks every service), not repeated per service; the built-in default has no render phase, so rendering on deploy is opt-in.
- "Run until X exists": write the predicate once as a shell `when:` and add `check: auto`. Render once instead of every deploy: drop the check and gate with `when: {type: builtin, cmd: "file-missing <path>"}`.
- A generated secret (app key) adds a `when: generated-missing <svc> <field>` mint step + `service_generated_harvest` — `render-and-vars.md` § 2.

Preview: `dwe deploy plan [--service <name>] --output json`. The same step schema powers `workspace/tests/*.yml` (`integration-tests.md`).

## 3. `lifecycle.yml` — hooks around run/stop/restart

`run:` and `stop:` wrap the runtime lifecycle (no deploy steps): `run.show_info`, `run.final_message`, `phases:` (a `start` phase = `docker up --wait`, then post-up phases). Best-effort steps take `continue_on_error: true`; optional-service steps gate with `when: {type: template, expr: '{{ (index .Services "redis-insight").Enabled }}'}`. Self-update policy is **not** here — it is the top-level `update: {mode: on|off}`. Schema: `dwe docs show config/lifecycle --lang en`.

## 4. `setup.yml` — first-deploy wizard

`questions: [{id, type: input|select, title, description, required, writes: vars.x, validate: {preset|regex}, options: [{value, label}]}]`. Runs on the first `dwe deploy` when `workspace/local.yml` is absent/empty; answers land in `local.yml`. A check that depends on a wizard-written value needs `stages: [post-setup]` (§ 5). Schema: `dwe docs show config/setup --lang en`.

## 5. `validate.yml` — project readiness checks

Host probes (docker/git/ports) are built in; this file adds project checks: `checks: [{id, description, stages: [deploy|run|stop|command|post-setup], severity, services: [...], hint, type: builtin|command, cmd, with}]`. Builtins allowed here: `shell` · `file_exists` · `executable_in_path` · `env_keys_present` · `config_keys_present` · `tcp_reachable` · `http_check`. Only the `env` probes and these checks run in preflight — a content mistake elsewhere never blocks a lifecycle command.

```yaml
checks:
  - id: marketplace-credentials-set
    stages: [post-setup]           # after the wizard, never at the pre-wizard gate
    severity: error
    hint: "Set vars.app.marketplace.* in workspace/local.yml, or run `dwe deploy`."
    type: builtin
    cmd: config_keys_present
    with: { keys: [vars.app.marketplace.username, vars.app.marketplace.password] }
```

Run: `dwe validate checks --output json`, `dwe validate --stage post-setup --output json`. Schema + guide: `dwe docs show config/validate --lang en`, `guides/preflight-checks`.

## 6. `info.yml` dashboard + `styles.yml` branding

`info.yml`: `sections: [{id, title, items}]`; item types `auto-urls` / `auto-hosts` (expand at render time; `port_via:`, `include` / `hide`), `subgroup` (gates with `when:`), `definition` (templated via `.Raw.vars.x`). `styles.yml`: `header: {lines, font}`, `colors:` (semantic tokens `accent`, `success`, `warning`, `danger`, `muted`, `border`, `text`), `separator:`. Preview: `dwe info --output json`. Schema + guide: `dwe docs show config/info --lang en`, `config/styles`, `guides/brand-your-project`.

## 7. `docker.yml` — loaded separately

Pins `project_name` (resolved lowercased; default `${project.prefix}-${project.name}`), declares shared cache volumes (`resources.volumes.<name>: {name, shared: true, ensure_before: [up, deploy]}`), `compose.base`, and `build.prepull_bases`. Per-dev override in gitignored `docker.local.yml`; per-key, not full-replacement. Schema: `dwe docs show config/docker --lang en`.

## 8. Handoff

| Edited | Apply (user runs) |
| --- | --- |
| per-service `deploy.yml` / `workspace/deploy.yml` | `dwe deploy run` |
| `lifecycle.yml`, `docker.yml`, a compose overlay | `dwe run` (`dwe deploy run` if mixed) |
| `setup.yml` | read on the next `dwe deploy run` while `local.yml` is absent |
| `validate.yml` | `dwe validate --output json`, then whatever it gates |
| `info.yml` / `styles.yml` | display-only → `dwe validate`, `dwe info` |
| `reset.yml` | destructive → `snapshots-reset-troubleshoot.md` |

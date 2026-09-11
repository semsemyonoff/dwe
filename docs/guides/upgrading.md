# Upgrading DWE

A new DWE version can change how your project loads, what a pipeline step does, or what a command prints. Most releases change nothing you have to act on; some do. This page is the short list of what breaks and what to do about it, newest release first.

It is deliberately not a change list — that is [`CHANGELOG.md`](https://github.com/semsemyonoff/dwe/blob/main/CHANGELOG.md), which records every change. Here you will only find the ones that need you to edit something.

## Upgrade the binary

However you installed it:

```bash
brew upgrade semsemyonoff/tap/dwe    # Homebrew
```

Otherwise download the archive for your platform from the [releases page](https://github.com/semsemyonoff/dwe/releases) and replace the binary on your `$PATH`. Installation methods are listed in the [README](https://github.com/semsemyonoff/dwe/blob/main/README.md).

DWE does not update itself. The `update:` block in `workspace.yml` is unrelated — it controls a git probe against your *project* repository, not the DWE binary.

## After upgrading, in any project

Three things worth doing before you trust the new version in a project:

1. **Run `dwe validate` first.** It deploys nothing and starts no service, so a removed config key surfaces as a named diagnostic instead of a failed deploy. A project that no longer loads at all reports one error naming the key. (It does execute the project's own `checks.*` entries, which may be `shell` or `script` commands; those are expected to be idempotent inspection, but the CLI cannot enforce it.)
2. **Restart the host bridge, if the project uses one.** The bridge daemon runs the binary that started it, so after a host upgrade a containerized `dwe` keeps executing the *old* version until the daemon is replaced:

   ```bash
   dwe bridge stop && dwe bridge start
   ```

   `dwe bridge status` shows the running daemon; `dwe version` from inside a bridged container shows which build it answers with.
3. **Force a redeploy when the release notes say so.** A behaviour change that does not alter the deployment hash is invisible to `dwe deploy run`, which will report `already up-to-date` and skip the very step whose semantics moved. `dwe deploy run --force` re-runs every step; `when:` guards still apply.

## Upgrading to 0.6.1

Four groups: template functions, the platform, integration tests, then deploy.

### Template functions

DWE moves to go-sprout 1.1, which no longer accepts Sprig's argument order. This affects every place `{{ … }}` templates run — see [Templates](../reference/templates.md).

**Ten functions now fail on the Sprig order.** `get`, `set`, `unset`, `hasKey`, `pick`, `omit`, `append`, `prepend`, `slice` and `without` take the map or list *last*. The old order used to be reordered for you with a warning; it is now a render error, so it surfaces the first time the template runs. Rewrite the call as a pipe:

```
{{ hasKey $m "a" }}     →  {{ $m | hasKey "a" }}
{{ append $list "x" }}  →  {{ $list | append "x" }}
{{ slice $list 1 2 }}   →  {{ $list | slice 1 2 }}
```

**Four regex functions changed argument order and do not fail.** The string they work on moved to the last position. A template written for the old order still renders — to the wrong result:

| function | before | now |
|---|---|---|
| `regexFindAll` | `regexFindAll <re> <value> <n>` | `regexFindAll <re> <n> <value>` |
| `regexSplit` | `regexSplit <re> <value> <n>` | `regexSplit <re> <n> <value>` |
| `regexReplaceAll` | `regexReplaceAll <re> <value> <replacement>` | `regexReplaceAll <re> <replacement> <value>` |
| `regexReplaceAllLiteral` | `regexReplaceAllLiteral <re> <value> <replacement>` | `regexReplaceAllLiteral <re> <replacement> <value>` |

Nothing reports these, so search for them:

```bash
grep -rnE 'regex(FindAll|Split|ReplaceAll)' workspace/
```

The search also hits `regexFindAllGroups` and `regexFindAllNamed`, which did not change — skip those. The rest of the regex functions — `regexMatch`, `regexFind`, `regexQuoteMeta` and the `*Groups` / `*Named` variants — are unchanged.

**The `mustRegex*` aliases are gone.** `mustRegexFind`, `mustRegexFindAll`, `mustRegexMatch`, `mustRegexSplit`, `mustRegexReplaceAll` and `mustRegexReplaceAllLiteral` were deprecated; a template calling one now fails to parse. Drop the `must` prefix — and apply the new argument order from the table above where it applies.

**`div` by zero is now a render error** instead of an arbitrary number.

### Platform

**Release binaries need macOS 13 Ventura or later.** They are built with Go 1.27, which dropped macOS 12; on an older Mac, stay on 0.6.0. Building from source needs Go 1.27.

### Integration tests

**`dwe validate` warns about compose host ports interpolated from a variable `dwe test` does not remap.** A port such as `"${VALKEY_PORT:-6379}:6379"`, where `VALKEY_PORT` is exported `from: vars.ports.valkey`, binds the same host port as the live stack in every test run. It always did; now `dwe validate` and `dwe test run` say so. The warning appears only in projects that have a `workspace/tests/` directory, and it fails `dwe validate --strict`. Fix every scenario the warning names:

```yaml
env:
  vars:
    ports.valkey: auto
```

A port exported `from: services.<name>.ports.<x>` is already remapped while that service is enabled in the scenario; it warns only for a scenario that disables the service while its compose file stays in the stack — a `required: true` service, or one declared in the root compose file. See [Interpolated host ports](../reference/config/tests.md#interpolated-host-ports).

A warning that names no scenarios means no active `exports.env` rule traces the variable — it comes from a hand-written `.env`, the host environment, or a rule whose `when:` is falsy — so no scenario setting can fix it. Export the variable `from: vars.<path>` and add `env.vars: { <path>: auto }` to each scenario, or export it `from: services.<name>.ports.<x>`.

**A literal host port behind an interpolated bind address now blocks `dwe test run`.** `"${BIND:-127.0.0.1}:8080:80"` publishes the literal host port 8080, which collides with the live stack; the scanner used to miss it. Model the port under `services.<name>.ports` and interpolate it, as for any [literal host port](../reference/config/tests.md#compose-isolation-scanner), or pass `--skip-isolation-check`.

### Deploy

**The built-in deploy pipeline now brings a stopped stack back up.** Its `up` step carries `check: {type: builtin, cmd: containers_running}`, so it runs on every deploy instead of being skipped by the journal after the first success. The built-in pipeline therefore never reports `already up-to-date` any more: a deploy of an unchanged, running project is a quick `docker up --wait` plus a probe.

**The first deploy after upgrading sees a config change, once.** A `check:` is part of the project config hash, so a project deployed with the built-in pipeline no longer matches its recorded hash. An interactive `dwe deploy run` shows the `Deployed config changed. Choose action:` selector one time — pick `Apply changes`. A non-interactive deploy applies the change without asking. Either way the project-level steps (`up`, `info`, the success message) run once; per-service steps are not affected.

**An ejected or hand-written `workspace/deploy.yml` keeps the old behaviour.** It replaces the built-in pipeline whole, so its `docker up` step still has no `check:` and is still skipped once recorded. Add the same check to get the fix — this triggers the same one-time config change:

```yaml
- name: up
  type: dwe
  cmd: "docker up --wait"
  check:
    type: builtin
    cmd: containers_running
```

See [`containers_running`](../reference/config/deploy/builtins.md#whole-project-mode) for what the check accepts.

## Upgrading to 0.6.0

Three groups, in the order you will hit them.

### Hard failures on load

The project does not load at all until these are fixed, and every command reports the same error. The first two name the file; the third names the offending `exports.env` index instead, since an export rule carries no source layer.

**The top-level `state:` key is gone.** Delete it. It was a free-form string with one consumer and no replacement; free-form values belong under `vars:`.

```yaml
# workspace/defaults.yml — delete this
state: ""
```

If something read it — an `exports.env` rule with `from: state`, a `${state}` reference — move the value under `vars:` and update the path, or drop the rule.

**The top-level `ui:` block is gone.** Delete it. Its three command-browser knobs had no replacement and the browser now runs with what an absent block always resolved to: top-level groups expanded, empty subtrees collapsed while filtering, type badges on. The hotkeys, parameter form, fallback ladder and mouse behaviour that were documented on the old `ui` reference page now live under *Interactive browser* in [`../reference/config/commands/index.md`](../reference/config/commands/index.md).

**`COMPOSE_PROJECT_NAME` is now a reserved `.env` variable and may not be redeclared.** An `exports.env` rule with that name fails the load:

```
exports.env[3]: "COMPOSE_PROJECT_NAME" is a reserved system variable and cannot be
redeclared as an export rule (reserved: PROJECT, UID, GID, COMPOSE_PROJECT_NAME)
```

Delete the rule — DWE emits the line itself. **Check the value before you assume it is the same one.** The built-in value is the compose project name DWE passes as `-p`: `project_name` from `workspace/docker.yml`, otherwise `<project.prefix>-<project.name>`, always lowercased. A hand-rolled rule built from a different expression produced a different string, and anything created under the old name — containers, networks, volumes — becomes invisible to the new one. Compare the two before deleting:

```bash
grep COMPOSE_PROJECT_NAME .env      # what your rule produced
dwe render env | grep COMPOSE_PROJECT_NAME   # what DWE will emit
```

If they differ and you have a running stack, either set `project_name` in `workspace/docker.yml` to the old value, or accept the new name and redeploy once from clean.

### Silent behaviour changes

Nothing errors. Behaviour is different.

**Container commands now decide three runtime defaults themselves.** Each covers a different set of command types:

| default | applies to | new behaviour |
|---|---|---|
| workdir and user | `service_exec`, `service_run`, `daemon` | With no `workdir:`, falls back to the service's `cli.workdir` → `work_dir_internal` → `dir_internal` — the chain `dwe shell` uses; `workdir: internal` opts out. A `daemon` with no `user:` inherits the service's `cli.user` instead of the image's `USER`. |
| TTY | `service_exec`, `service_run` | A container terminal only when you launched the command yourself and DWE's streams are terminals, or the run is bridged. Everything else gets `-T`, with colour forced. |
| exec mode | `service_exec` | `mode:` defaults to `exec-or-run` instead of `exec-or-fail`: a stopped service falls back to a one-off `docker compose run --rm` instead of refusing. |

What to check: any daemon whose target service sets `cli.user` — the ownership of everything it writes changes, so pin `user:` explicitly if the old uid mattered. Any command that must never create a container — declare `mode: exec-or-fail`. Any `type: command` `check:` pointing at a mode-less `service_exec` command — it is now container-creating, and the one-off runs with `--no-deps`, so it can report success against a stack that is down.

None of the three alters the deployment hash. **Run `dwe deploy run --force` once after upgrading**, or a step whose semantics moved reports `already up-to-date` and is skipped.

**Raw `docker compose` from the project root now scopes to DWE's project.** The generated `.env` carries `COMPOSE_PROJECT_NAME`, and `.env` sits in the compose project directory, so Compose picks it up — above any top-level `name:` in your compose file. That is the point: a manual `docker compose ps` finally shows the same containers as `dwe status`. But a compose file declaring a *divergent* `name:` — what `dwe validate` reports as `config.compose_project_name` — is now dead config, and resources created under it are no longer visible to a bare compose run. Either align the two, or write `name: ${COMPOSE_PROJECT_NAME}` in the compose file, which now resolves.

**Pipeline logs no longer record every redraw frame.** A `\r`-terminated progress frame is held and overwritten by the next one, so `.dwe/logs/<pipeline>.log` records one line per committed line. The consequence: `tail -f .dwe/logs/deploy.log` no longer shows live clone or download progress, only committed lines. The live view on your terminal is unchanged.

### Flags and messages

**`dwe docs llms-txt --output PATH` is now `--out PATH`.** There is no alias: the old local `--output` shadowed the root format flag of the same name, which is why `-o` failed on this command. `-o json` is now accepted and ignored here — the document is itself the payload.

**Strict-loader error text changed.** An unknown field in a pipeline, `service.yml`, command file, manifest, scenario, `setup.yml` or translation bundle now reads:

```
workspace/deploy.yml:12: unknown field "defaults" — allowed here: log, phases
```

It used to surface the YAML library's `field defaults not found in type config.DeployConfig`. A script or CI job grepping for `not found in type` needs updating.

**`dwe validate` gained warnings that can fail `--strict`.** A dot-path that does not resolve in the merged config is now reported: an `exports.env` rule's `from:` or `when:`, and a command's `params.<name>.default_from`, `params.<name>.options.from` or `context.<name>.from`. These are warnings — a `from:` with a `default:` is a legitimate optional path, and a path may live in a `local.yml` that is not on this machine. But `dwe validate --strict` treats warnings as errors, so a CI job using it can start failing on a path that was deliberately optional. Give such a rule a `default:`, or correct the path.

`dwe validate` also now reports the built-in default pipeline honestly: an empty or all-comment `deploy.yml` reports at info as `has no active content (all comments or empty) — built-in default pipeline is active` where it used to report **OK**. Nothing is broken — the file never ran. If you want to start editing from the built-in pipeline, `dwe deploy eject --out workspace/deploy.yml --force` writes it there as a commented, editable document.

## Older releases

Releases up to and including `v0.5.0` predate this page. Their notes are on the [releases page](https://github.com/semsemyonoff/dwe/releases).

## See also

- [`CHANGELOG.md`](https://github.com/semsemyonoff/dwe/blob/main/CHANGELOG.md) — every change in every release, not just the breaking ones
- [Troubleshooting](troubleshooting.md) — when the stack misbehaves for reasons unrelated to a version change
- [Reference (`reference/`)](../reference/index.md) — schemas and config fields

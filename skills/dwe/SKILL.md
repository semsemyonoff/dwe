---
name: dwe
description: Use when the current working directory is inside a DWE project — a Docker-based developer environment manager. Detect one by walking up from cwd to a directory containing `workspace.yml`; that directory is the project root. A populated project also has a `workspace/` subdirectory (services, pipelines), but its absence does not disqualify it (e.g. a freshly-initialized project). Applies anywhere beneath the root, including service folders like `workspace/services/<name>/` and their source subtrees. The skill is both a navigator — which `dwe` commands to use for inspection vs mutation, and how to look up everything else via the built-in `dwe docs` subsystem — and an authoring guide for extending a project (scaffolding from git repos, adding services and tools, authoring commands and daemons, wiring render packs and vars, customizing deploy/lifecycle pipelines). Activates on the word "dwe", on editing any file under `workspace/`, or when working inside a service directory.
---

# DWE — Dev Workspace Engine

DWE is a CLI that orchestrates Docker-based local development environments. It augments a project's compose file with configuration layering, lifecycle management, validation, and declarative tooling — it does **not** replace compose. Edit the compose file freely; DWE runs on top of it.

This skill is a **navigator with an authoring layer**, not a reference. It teaches **which** command to run, **which** file to edit, and **in what order** — all schema details, field meanings, and deep behavior live in the built-in `dwe docs` subsystem and are versioned with the binary. Every authoring step ends with a `dwe docs show <topic>` pointer; use it to learn **what** anything means.

## Detecting a DWE project

Walk up from your current working directory until you find a directory that contains `workspace.yml`. That directory is the project root. All `dwe` commands resolve the root themselves — invoke them from the root or any descendant.

A populated project also has a `workspace/` subdirectory next to `workspace.yml` (service definitions, pipelines, commands, templates, i18n). Its presence is a strong signal. A project with **only** `workspace.yml` plus a set of commented scaffold files is a freshly-`dwe init`'d project — see `references/populate-init-repo.md` to fill it in.

## First step: orient yourself

Run once per session inside the project:

```shell
dwe docs llms-txt --lang en
```

This emits a compact, project-aware index (services, commands, doc pointers) designed for AI agents. Read its output before doing anything else.

**If a root `AGENTS.md` exists, read it too.** It is the **project-specific** layer: the real service list, the real command IDs, project-local rules. This skill is the **generic** layer: universal DWE mechanics and the read/mutate discipline. They are designed to agree. On a *project fact* the `AGENTS.md` wins; on a *generic rule* the skill wins.

**There are two different `AGENTS.md` files — do not confuse them:**

| File | Origin | May you edit it? |
| --- | --- | --- |
| **root** `AGENTS.md` (next to `workspace.yml`) | written once by `dwe init`, then hand-maintained | **yes** — this is the project-specific layer, edit it directly |
| **hub** `AGENTS.md` (inside a service hub, e.g. `services/app/`) | **generated** by the `ai` render pack — written by `dwe render ai`, and refreshed by `dwe deploy run` only when the service's `deploy.yml` declares a `render ai` step (the built-in default pipeline has none) | **no** — edit the template (`workspace/templates/ai/<pack>/`) and hand off `dwe render ai` |

Both carry a `CLAUDE.md` symlink next to them. The generated one says so in its own footer; when in doubt, read the last lines of the file.

## Project anatomy (the map)

What lives where (paths, not schemas — look up any schema with the slug noted):

- **3-layer config**, later wins, maps deep-merge: `workspace.yml` (identity only — `project`, `update`, `compose`) → `workspace/defaults.yml` (git-tracked: `services` toggles, `runtime`, the `vars` sandbox, `exports`, `bridge`) → `workspace/local.yml` (gitignored per-dev overrides; tool-written). The merged root is **strict** — free-form values live **only** under `vars:`; a bare custom root key is a hard load error. A `vars.*` string may be an `ENC[age:…]` marker — a committed secret decrypted in memory at load time (`secrets.recipient` in `workspace.yml`, identity in `~/.config/dwe/keys/`); `dwe docs show config/secrets --lang en`.
- **Services = folders**: `workspace/services/<name>/service.yml`; the folder name **is** the map key (no `name:` field). The real container lives in the compose base or an overlay.
- **User commands**: `workspace/commands/**.yml`; path + filename + key = a dot-ID; run with `dwe cmd <id>`. Params go through `--set key=value`; a command that declares `${args}` also takes pass-through arguments after `--` (`dwe cmd site.test -- --run x.test.ts`). `dwe cmd -i <id>` reports which of the two a command accepts — read that instead of opening its YAML.
- **Render packs**: `workspace/templates/{config,ide,ai,git}/`; `config` writes runtime files into the service hub, `ide`/`ai`/`git` write hub dotfiles (devcontainer, the generated `AGENTS.md`, git hooks).
- **Pipelines (optional, full-replacement)**: per-service `deploy.yml` and project `workspace/{deploy,lifecycle,reset,snapshot,setup,validate,info,styles}.yml`. Absence = built-in default (reported `ⓘ`, not an error).
- **Integration-test scenarios**: `workspace/tests/<scenario>.yml` (one file = one scenario; the name is the file basename; reuses the deploy step schema) → `dwe docs show config/tests --lang en`. See `references/integration-tests.md`.

Schema for any of these → `dwe docs show concepts/project-layout --lang en` and the per-area slugs in the recipes below.

## Rules no validator will catch for you

`dwe validate` checks shape, not judgement. These four follow from DWE's own mechanics,
pass validation either way, and are the ones a fresh project gets wrong.

- **Two disjoint registries share the word `builtin`.** `when: {type: builtin}` takes
  **condition predicates** (`dir-empty` · `dir-not-empty` · `dir-exists` · `dir-missing` ·
  `file-exists` · `file-missing` · `generated-missing <svc> <field>` — that list is the whole
  of it). A step body and `check:` take the **step builtins** (`service_configs_render`,
  `containers_running`, `http_check`, …). **A name from one is rejected by the other**, so
  `check: {type: builtin, cmd: dir-not-empty}` does not work and `when: {type: builtin, cmd:
  containers_running}` does not either — write the shell equivalent, or use `check: auto`.
  Both registries are enumerated in full, with kinds and one-line summaries, in
  `dwe docs llms-txt --lang en` (§ Builtins) — read it there instead of guessing a verb.
- **Mount the whole hub, not just `src/`.** A service's `dir:` is the hub (`./services/<name>`),
  mounted at `dir_internal: /workspace`, with `work_dir_internal: /workspace/src` pointing at
  the checkout inside it. Mounting the checkout directly leaves rendered configs, caches and
  tooling state outside the container, and the render packs write into the hub, not `src/`.
- **One definition.** If a deploy step needs to run something the project already declares as
  a user command, use `type: command` + `cmd: <id>` — do not paste the same shell line into
  `deploy.yml` as a `type: shell` step. The duplicate drifts, and only one of the two copies
  gets the service/workdir/user/env the command carries.
- **A port declared in `service.yml` is display-only** until an `exports.env` rule surfaces it
  (`{name: APP_PORT, from: services.<name>.ports.http}`) and compose interpolates that var.
  `PROJECT`, `UID` and `GID` are injected into `.env` automatically and must **not** be
  redeclared as export rules.

## Output conventions

- **Always `--lang en` for docs.** Translated docs may lag the English source, and reasoning is more reliable in English: `dwe docs llms-txt|search|show|list … --lang en`.
- **`--output json` for data you parse.** The default human mode is for users, not agents. Add `--pretty` if you like. Applies to `status`, `validate` (incl. `validate tests`), `services list`, `vars get/list/inspect`, `snapshot list/inspect`, `info`, `logs`, `commands list`, `docs list/search`, `test list/run/clean`, `deploy plan` (supersedes `--format`; emits `{service?, phases[{name, service?, description?, when?, steps[]}]}`, each step carrying `cmd` plus an `unresolved[]` list of leftover `${...}` references).
- **Exception — `dwe deploy state show`** always emits YAML; it does not read `--output` at all. Parse it as YAML, or read the state through `dwe status --output json`.
- **The JSON envelopes are not guessable — read the keys before indexing.** `status` is `{project, apps, tools, infra, deploy, topology, git}` — there is **no** top-level `services` key despite "service" being the vocabulary everywhere else; `info` is `{title, sections[]}`; `validate` is `{summary, diagnostics[]}`. Indexing a wrong key returns empty with exit 0, which is indistinguishable from "no results".
- **Exception — `dwe docs llms-txt`.** Emits markdown; the global `--output json` is **ignored** — the document IS the payload. Just run it and parse the markdown from stdout. To write it to a file use the command's own `--out PATH` flag (not `--output`).
- **Exception — `dwe docs show`.** Emits markdown (rendered for TTY; raw with `--raw` or in a pipe). The global `--output json` is **ignored** — the document IS the payload. Use `#anchor`, `--anchors`, or `--toc` to scope without reading the full body.
- **Bare `dwe commands` / `dwe docs` / `dwe status` open a full-screen TUI on an interactive terminal**, but auto-fall back to plain output when not attached to one — bare `dwe commands`→`commands list`, `dwe docs`→`docs list`, `dwe status`→plain text — so they never hang a piped agent (a pipe is non-interactive). `commands`/`docs` additionally honor `DWE_NONINTERACTIVE=1` (the bridge sets it in containers); `status` does **not** — it drops the TUI only on a non-TTY stdout, `--no-tui`, `TERM=dumb`, or `--output json`. Always call the explicit read subcommands (`commands list`, `docs list|show|search`, `status --output json`) rather than the bare TUI form.

## When to use what

| Goal | Command / reference |
| --- | --- |
| Project overview (start here) | `dwe docs llms-txt --lang en` |
| **Run a project task** (tests, lint, migrate, …) | `dwe commands list` to find the ID → `dwe cmd <id>` · `dwe cmd -i <id>` first if unsure what it does |
| **Run a one-off command in a service container** | `dwe shell <service> -c '<cmd>'` — see **Running things** below |
| Inspect state | `dwe status --output json` |
| Read logs | `dwe logs <service> --output json` |
| Diagnose configuration | `dwe validate --output json` |
| Search docs / read one topic | `dwe docs search <term> --lang en` · `dwe docs show <topic> --lang en` |
| Inspect vars (read) / set a var (handoff) | `dwe vars get\|list\|inspect <var> --output json` · ASK user → `dwe vars set <path> <value>` — that writes `local.yml` (this dev only). Hand-edit `defaults.yml` **only** when the new value is right for everyone who clones the repo; a machine-local one there breaks every clean deploy. |
| Read the encrypted-secret inventory | `dwe secrets status --output json` — read-only, always exits 0. Reports every `ENC[age:…]` marker and `*.age` pack source as `decrypted`/`decryptable` or `unresolved: no_identity\|wrong_identity\|invalid_identity\|corrupt` (`invalid_identity` = a source IS set but holds no key — fix that source, not the missing key). Run it FIRST when `dwe vars` shows `<encrypted>` or a lifecycle command is blocked by `secrets.unresolved`: `identity.reason` says which of the four it is and `identity.hint` is the sentence to hand the user. `dwe secrets key list --output json` (also read-only, no key material) shows which identities this machine has. |
| **Populate a fresh repo from git URL(s)** | `references/populate-init-repo.md` (ends in user-run `dwe deploy run`) |
| **Add a service / tool / infra** | `references/add-service-and-tools.md` |
| **Author a command or background daemon** | `references/authoring-commands.md` |
| **Wire render packs / vars / `.env` / generated secrets** | `references/render-and-vars.md` |
| **Author a pipeline** (deploy / lifecycle / reset / setup / validate / info / styles) | `references/pipelines-and-orchestration.md` |
| **Verify a clean deploy in isolation / author an integration test** | `references/integration-tests.md` |
| **Snapshot / reset / troubleshoot** | `references/snapshots-reset-troubleshoot.md` |
| Apply a change | see **Picking the apply command** below — edit yml, then ASK the user to run it |

## Running things (the two commands you will reach for most)

Most work in an existing project is not authoring — it is running the project's
own tasks. Two commands cover it, and they are not interchangeable.

**`dwe cmd <id>` — a task the project already declares.** Prefer it. It carries the
right service, workdir, user, env and compose flags, so it works identically for
you and for CI, and it keeps working when those details change. Find IDs with
`dwe commands list`; read one with `dwe cmd -i <id>` before running something
unfamiliar — that also tells you whether it takes `--set key=value` params or
`${args}` pass-through after `--`.

**`dwe shell <service> -c '<cmd>'` — anything not declared.** This is the escape
hatch, and it is legitimate: not every one-off belongs in `workspace/commands/`.
But treat repetition as a signal — if you run the same gate through `dwe shell`
more than a couple of times, it wants to be a declared command, and saying so is
more useful than running it a third time.

- Prefer `dwe cmd <id>` when one exists. Check the registry before assuming it
  does not — `dwe commands list | grep <service>` is one call.
- Long-running command? Add `--tty` — **a `dwe shell` flag; `dwe cmd` does not
  take it.** Without it the child's stdout is a pipe, so it block-buffers and
  prints nothing until it exits, which reads as a hang. The cost is that a PTY
  turns `\n` into `\r\n`, so leave it off when parsing output.
- Do **not** reach for `docker exec` / `docker compose exec` instead. `dwe shell`
  resolves the container from the service name and applies the service's `cli:`
  block; guessing container names with `docker ps | grep` is the tell that you
  wanted `dwe status` or `dwe shell`.

## Picking the apply command

After editing yml, the apply command depends on **what** changed (never run it yourself — hand it to the user):

- `service.yml` / a service's `deploy.yml` / `configs` / `dirs` / `render` / **added a service** → `dwe deploy run`
- `workspace/deploy.yml` → `dwe deploy run`
- `workspace/lifecycle.yml` or the compose base/overlays → `dwe run`
- toggled a service → `dwe services enable|disable <name> --apply`
- only icon / host / display strings → `dwe validate` (then `run`/`deploy run` if it affects runtime)
- `exports.env` **only** → `dwe run` (or `dwe deploy run --force`) — that block is in no config hash, so a plain `dwe deploy run` never re-renders `.env`: it either returns `already up-to-date` or journal-skips the implicit render step (`references/render-and-vars.md` § 7)
- mixed / unsure → `dwe deploy run` (ends in `docker up --wait`, so it covers a restart)
- authored/edited a `workspace/tests/<scenario>.yml` → verify read-only with `dwe validate tests`, then run or hand off `dwe test run <scenario>` (a clean deploy in a throwaway copy — does not touch the live stack). Whether you may run it yourself is decided by that scenario's cost profile — see **The `dwe test run` gate** below. Propose it for **substantial** changes (new service, reworked deploy pipeline), not after display-only edits. See `references/integration-tests.md`.

Never recommend `dwe deploy run --force` as a clean install — `--force` only ignores prior state (`when:` still applies). A true clean install is `dwe reset run && dwe deploy run`.

## Permission boundary — read freely, never mutate

You MAY run READ commands without asking (all read-only — they don't mutate or tear down project state; safe even when they report errors):

- `status`, `logs`, `validate` (+ `validate config|checks|env|tests`, `--stage`), `info`
- `deploy plan`, `reset plan`, `deploy state show`
- `snapshot list|current|inspect`
- `compose argv|files`, `docker ps|logs|project-name`, `bridge status|logs`
- `vars get|list|inspect`, `commands list` / `commands -i <id>`, `services list`
- `secrets status`, `secrets key list` — the encrypted-secret inventory and the identities installed on this machine. Read-only, exit 0 even when nothing decrypts, and print **no** plaintext and **no** key material (`key list` reports a broken keyfile by state alone — never its content). `secrets get <path>` and `secrets key export` DO print secret material — treat them as a handoff, not a read.
- `render env` **bare only** (no `--out`) — prints the resolved `.env` to stdout, writes nothing. **Always scope it**: the unfiltered body is the project's whole exported secret set, so run `dwe render env | grep -E '^<NAME>='` (or `grep -q` for a presence check), never the bare form on its own. Host-only (the container allowlist admits only `render config`) and it ignores `--output json` — always dotenv text. With `--out` it is a write; see `references/render-and-vars.md` § 6.
- `docs show|search|list|llms-txt` — read-only doc access.
- `test list` — list integration-test scenarios (lock-free, no Docker). `test list -o json` also carries each scenario's **cost profile**, which is what decides whether you may run it — see the gate below. `test clean --dry-run` is also safe to run without asking (it previews a sweep and destroys nothing), but is NOT strictly lock-free/Docker-free: it does a read-only `docker ps` orphan probe and briefly acquires-then-releases each scenario's flock. (`validate tests` sits in the `validate` family above; `test run` and the real `test clean` are gated/handed off below.)

`compose argv` takes the compose subcommand it should print the argv for
(`dwe compose argv ps`); bare, it errors.

### Running project tasks — judge the task, not the verb

`dwe cmd <id>` and `dwe shell <service> -c '…'` are **transports**: their risk is
whatever they carry, so a blanket rule on the verb gets it backwards. Verifying a
change with `dwe cmd site.test` is not a mutation; `dwe shell db -c 'psql -c
"DROP …"'` is, and no verb-level rule catches that.

- **Run without asking** when the task only reads or verifies **in place**:
  linters, type-checks, formatters in check mode, status/inspection commands
  inside a container, and a test suite that talks to nothing stateful.
- **Ask first** when it changes project or data state: migrations, seeds, resets,
  dependency installs, anything writing outside a build cache — and anything you
  are unsure about.
- **A test suite that reaches the project's database or any other stateful
  service belongs in the second group, not the first.** Integration suites
  routinely truncate and re-seed the schema they run against, and pointed at the
  live stack they destroy the developer's working data — observed: one
  `npx nx test` run through `dwe shell` replaced every row of a dev database with
  its own fixtures, silently, and nobody noticed for two tasks. Check what the
  suite connects to before running it; when it needs the stack, the isolated way
  is a `workspace/tests/` scenario via `dwe test run` (a throwaway copy with its
  own volumes), gated as below.
- **The registry already marks the dangerous ones.** `dwe cmd -i <id>` shows a
  command's `confirmation:` flag and the underlying command; a declared
  `confirmation:` means ask. When `-i` leaves you unsure, ask.
- Being asked to "run the tests" is permission to run the tests — not permission
  to run them **against the live stack** when they write to it.

#### The `dwe test run` gate — cheap AND isolated, or ask

`dwe test run` is not a `dwe cmd` task: despite the name it is a full clean Docker
deploy of a throwaway copy of the project. It is nevertheless the only way to prove
a deploy pipeline still works, so the rule is **conditional, not blanket** — and the
condition is decided by data, not by feel.

Read the facts first (read-only, no Docker, no locks):

```shell
dwe test list --output json
```

Each scenario carries a `cost_profile`. Two groups, judged differently:

**Hard stops — hand the run to the user, no judgement call.** The scenario reaches
outside its own copy, so a failure is not confined to it:

- `isolation_findings` non-empty — named / `external:` volumes or networks, reused verbatim
- `shared_volumes` > 0 — `shared: true` volumes carry the real cache/data
- `host_steps` > 0 — steps running **project-authored code on the host**, outside the
  container sandbox (`type: shell`, the `shell` builtin, a `type: command` resolving to
  a host command, a `type: dwe` re-entering a pipeline, and shell `when:` / `check:`
  conditions) — in the scenario, in the deploy it triggers, and in the
  `workspace/validate.yml` checks the run executes. Their side effects (absolute paths,
  `~`, binds outside the project) are **not** sandboxed. dwe's own subcommands don't
  count — the built-in default pipeline reports 0

**Cost — judge it, don't reflex.** `build_services`, `external_images`,
`max_start_period_seconds`. A build is not an automatic stop: judge what it *is* by
reading the Dockerfile — a thin layer over a published base is minutes at worst,
building a toolchain from source is not something to start unattended. **The profile
does not model this**: it reports whether there *is* a build, never what it costs,
and the dominant factor (whether the Docker layer cache is warm — seconds versus
many minutes) has no static source at all.

So: **run it unattended only when the profile shows all three hard stops clear AND
you can positively account for the cost. Otherwise ask — and when unsure, ask.**
On the two workspaces this rule was measured against, both have builds and both
land in "ask", which is the expected outcome, not a failure of the rule.

Two things stay true regardless: run `dwe validate tests` (free) first, and propose a
run only for **substantial** changes — a new service, a reworked deploy pipeline,
provisioning/secrets — never after a display-only edit. Details in
`references/integration-tests.md`.

You MUST NOT invoke these MUTATING commands yourself. Prepare the change, then ask the user to run the exact command. (Exactly one entry is conditional rather than absolute — `dwe test run`, marked as such at the end of the list.)

- `dwe init` — scaffold a project (safe to re-run: gap-fills; `--force` overwrites).
- `dwe deploy run` — run the deploy pipeline (the right command after editing a service's config/deploy steps or adding a service; ends with `docker up --wait`). The `--service <name>` form requires that service's own `deploy.yml` and **skips** the final stack up — see the recipes before recommending it.
- `dwe run` / `stop` / `restart` — runtime lifecycle (no deploy steps); `dwe reset run` — destructive.
- `dwe services enable|disable <name> --apply` — toggle a service.
- `dwe secrets init|set|encrypt|decrypt|rekey|key import|key export|key remove|get` — every one either writes a config layer / a keyfile, or prints secret material to the terminal. `secrets status` and `secrets key list` are the reads (see the READ list above). `key import` is a **human handoff, not a command you run with an argument**: at a terminal it opens a hidden prompt, and you must never ask the user for the identity text so you can type it — pasting a private key through your context puts it in a transcript. Hand over the bare `dwe secrets key import` and let them paste it.
- `dwe vars set`, `dwe render config|ide|ai|git`, `dwe render env --out <path>` (the bare form is a read — see the READ list above), `dwe snapshot create|restore|rollback|remove|pack|unpack`, `dwe bridge start|stop`, `dwe docs generate|export|cache clear`.
- `dwe cmd <id>` / `dwe shell <service> -c '…'` **when the task they carry mutates** — see the judgement rule above. A verifying task through either one is not on this list.
- `dwe test clean` (without `--dry-run`) — the integration-test sweeper: it tears down kept or crashed runs. Manifest-driven, but still a teardown — hand it over.
- `dwe test run [scenario...]` — **conditional, not forbidden**: it is the one entry here whose answer comes from data. See **The `dwe test run` gate** above; if the scenario's `cost_profile` does not clear it, this list applies as written.

Pattern: **edit yml files yourself → show the diff → tell the user the exact command → wait for them to run it.** Do not invoke the mutation, and do not work around the boundary by calling `docker` / `docker compose` / project scripts directly — including through `dwe shell`, which is a transport and not an exemption.

## Anti-patterns

- Do NOT bypass the dwe lifecycle: use `dwe deploy run` / `dwe run` / `stop` / `restart` (whichever fits — see the table). NEVER run `docker compose up/down`, `dwe docker up`, or `dwe compose` write ops directly — DWE tracks state and holds file locks; bypassing breaks both.
- Do NOT hand-edit generated artifacts: `.dwe/**`, `.env`, `workspace/local.yml`, or rendered hub files (incl. the **hub** `AGENTS.md` — the **root** one is scaffolded and yours to edit, see above). Edit the **source** (export rule / var / template / ai pack) and hand off the render/deploy.
- Do NOT put a free-form key at the config root — the strict root hard-fails the load. It goes under `vars:`.
- Do NOT "fix" an `<encrypted>` value or a `secrets.unresolved` block by editing yml. A marker means this machine lacks the project's age identity, not that the config is wrong — `dwe secrets status --output json` names the cause in `identity.reason` and the fix in `identity.hint`, and that fix is `dwe secrets key import` **run by the user** (they paste the key into a hidden prompt; you never handle it) or `DWE_AGE_KEY`. When the user runs `dwe run`/`dwe restart`/`dwe deploy` at a terminal, dwe itself offers the import before the `secrets.unresolved` wall, so "run it yourself and answer the prompt" is often the whole handoff. Rewriting the marker as plaintext commits the credential; deleting it breaks everyone else.
- Do NOT assume config shape. Before editing any yml under `workspace/`, verify the schema with `dwe docs show config/<area> --lang en`.
- Do NOT enumerate pending-op consumers from memory after `dwe services …` without `--apply` — run `dwe status` and follow its banner; that banner is authoritative.

## Recipes

Load a reference file on demand when the task matches:

- `references/recipes.md` — daily/inspection recipes + the index: add-a-service (quick), service-fails-to-start, toggle a service, find which config owns a setting, look up a field.
- `references/populate-init-repo.md` — user gives git repo URL(s) / "set up this project" / fresh `init`'d repo: interview → init → services → deploy clone steps → commands → render/vars → validate → deploy.
- `references/add-service-and-tools.md` — add an app/tool/infra service, optional toggles, compose overlays, `extends`.
- `references/authoring-commands.md` — author user commands & background daemons (the type zoo, params, templating, bridge opt-in).
- `references/render-and-vars.md` — render packs (config vs ide/ai/git), generated-secret harvest, the `vars` sandbox, `exports.env`.
- `references/pipelines-and-orchestration.md` — per-service & project `deploy.yml`, `lifecycle`, `setup` wizard, `validate` checks, `info`/`styles`.
- `references/integration-tests.md` — author `workspace/tests/<scenario>.yml` and verify an isolated clean deploy with `dwe test run` (you write the scenarios; the cost profile decides who runs them). Also the general engine additions: `http_check` predicate, predicate-as-assertion, per-step `timeout:`.
- `references/snapshots-reset-troubleshoot.md` — snapshot workflows, reset, and the read-only triage trio.

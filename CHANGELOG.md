# Changelog

All notable changes to `dwe` are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Version numbers do not follow Semantic Versioning before 1.0.0: a release is
numbered by its weight, and a patch-looking number can still change something
you have to act on. [Upgrading DWE](docs/guides/upgrading.md) lists what.

Every change that a user can observe — a new or renamed flag, a changed default,
a removed config key, a different message — belongs under `## [Unreleased]`
before its pull request merges. Release notes are cut from this file, so an entry
that is missing here is missing from the release.

Releases up to and including `v0.5.0` predate this file. Their notes were
generated from commit subjects and stay on the
[GitHub releases page](https://github.com/semsemyonoff/dwe/releases).

## [Unreleased]

### Added

- New guide: [Observability with OpenTelemetry](docs/guides/observability-otel.md)
  — an opt-in `otel` tool service whose `compose:` overlay ships the backend
  and whose `compose_after:` overlay patches the app services, with
  instrumentation recipes for Python, Go and Node and a text trace lookup for
  coding agents.
- The service reference documents how relative paths in a `compose:` overlay
  resolve: against the directory of the first `-f` file (`compose.base`), not
  the overlay's own directory. See
  [`compose`](docs/reference/config/services/fields.md).
- New `service.yml` field `compose_after:` — compose overlay files emitted
  after every service group (tool → infra → app) and before the generated
  bridge overlay and the project-wide `local.yml` `compose.extra`, under the
  same enabled gate as `compose:`. Lets a patch win over whole-value keys
  (`command:`, `healthcheck:`, an `environment:` entry) an app sets in its own
  overlay, instead of losing to it because the app group emits later. See
  [`compose_after`](docs/reference/config/services/fields.md).
- `dwe validate` warns `hide: expression does not evaluate` when a `hide:` on a
  command or group fails to render against the project config. At runtime
  such an expression is fail-open and leaves the command visible. The check
  only renders: a `cmd:` or builtin predicate is never executed.
- `dwe validate` warns (`config.compose_files`) when a file listed under a
  service's `compose:` or `compose_after:` does not exist, for every service
  whether enabled or not. A typo used to surface only as a `docker compose`
  error on the next `dwe run`. See
  [`validate.md`](docs/reference/config/validate.md#validation-domains).
- `dwe validate` notes (`config.healthcheck_start_period`, info) a compose
  service in the active chain whose healthcheck runs a test but sets no
  `start_period`. `dwe run` waits with `docker compose up --wait`, so a
  slow-starting service whose boot-time probes use up `retries` fails the
  whole run. See
  [`validate.md`](docs/reference/config/validate.md#validation-domains).

### Changed

- The `▶ <id>  [<type>]  <description>` banner that `dwe cmd` / `dwe commands`
  prints before running a command now goes to stderr, so `dwe cmd X | …`
  receives only the command's own output; through the host bridge it reaches
  the container's stderr. Under `-o json` the banner is no longer printed at
  all, so stderr carries only the error envelope. Scripts that parsed the
  banner from stdout must read stderr.

### Fixed

- The `hide:` examples in the [command directives](docs/reference/config/commands/directives.md#hide-condition)
  reference no longer fail to evaluate: config is read through `.Raw`
  (`index .Raw "services" "db" "enabled"`), not a non-existent `.services`.
  A copied example used to leave the command visible without any error.

## [0.6.2] - 2026-09-16

### Added

- Render templates can read the project's declared commands: `.Commands`,
  `.CommandGroups`, and per service `.ServiceCommands` /
  `.ServiceCommandGroups`. See
  [`dwe render ai`](docs/reference/render/ai.md#declared-command-index).
- The `default` AI pack gives `AGENTS.md` a `Declared commands` block that tells
  agents to use a declared command before falling back to `dwe shell`. Existing
  projects can copy the
  [snippet](docs/reference/render/ai.md#shipped-declared-commands-block).

### Changed

- **`dwe test` remaps compose host ports routed through `vars:`** — a port
  variable exported `from: vars.<path>` gets a free port in every scenario copy
  without `env.vars: { <path>: auto }`; a numeric `env.vars` value pins it. A
  step that hardcodes the original port now misses — see
  [Upgrading DWE](docs/guides/upgrading.md).
- `dwe commands list --output json` entries carry `description` and `service`.
- `interpolated_host_port` is no longer reported for a port traced to `vars:`.
- A scenario blocked by the compose isolation scanner fails before creating a
  copy or a compose project.
- `dwe test list --output json` and the `interpolated_host_port` check of
  `dwe validate tests` ignore `local.yml` `compose.extra` overlays, as the test
  copy does.

### Fixed

- `dwe deploy run --service` and `dwe services enable|disable --apply` check
  `ports_free` only for the named services and their `depends_on` closure.
- `dwe deploy run --service` rejects an unknown service before preflight.

## [0.6.1] - 2026-09-15

### Changed

- **Release binaries require macOS 13 or later**; building from source requires
  Go 1.27.
- **Templates move to go-sprout 1.1**: `get`, `set`, `unset`, `hasKey`, `pick`,
  `omit`, `append`, `prepend`, `slice` and `without` fail on Sprig's argument
  order; `regexFindAll`, `regexSplit`, `regexReplaceAll` and
  `regexReplaceAllLiteral` take the string last; `div` by zero is an error. See
  [Upgrading DWE](docs/guides/upgrading.md).

### Added

- `dwe vars set <var> --generate hex[:N]|base64url[:N]|uuid [--force]` writes
  a random value to `workspace/local.yml`.
- `dwe validate` warns about a host script that builds its own compose project
  name (`tests.host_project_name`) and about a compose host port `dwe test`
  cannot remap (`interpolated_host_port`); both fail `--strict`.
- Template functions `toUnix*`, `fromUnix*`, `escape`, `unescape`.

### Removed

- The deprecated `mustRegex*` template aliases.

### Fixed

- The built-in deploy pipeline brings a stopped stack back up: its `up` step
  now carries `check: containers_running` and runs on every deploy. The first
  deploy after upgrading reports a config change once; an ejected `deploy.yml`
  needs the check added by hand — see [Upgrading DWE](docs/guides/upgrading.md).
- Inside `dwe test`, shell steps and `type: script` commands get the copy's
  `COMPOSE_PROJECT_NAME` instead of the live one; snapshot, reset and
  service-toggle hooks honour `docker.yml` `project_name`.
- `dwe test` remaps the ports of a `required: true` service listed under
  `env.services.disable`.
- Workflow output: a sub-step's last unterminated line, a `\r\n` split across a
  read boundary and a `\r\x1b[K\n` redraw are logged correctly; a failure dump
  no longer leaves the terminal coloured.
- go-sprout diagnostics no longer print to stdout.
- `dwe docs show 'topic#--flag'` resolves anchors with leading hyphens; Russian
  docs link to Russian anchors.
- The Homebrew cask uses `postflight_steps` instead of the deprecated
  `postflight` block.

## [0.6.0] - 2026-09-07

### Added

- **Encrypted secrets committed to the repository**, so a value the whole team
  shares — a bot token, a service-account JSON — can live in git without sitting
  there in the open. One [age](https://age-encryption.org) key pair per project:
  the public recipient is committed as `secrets.recipient` in `workspace.yml`,
  the private identity lives in `~/.config/dwe/keys/` or in `DWE_AGE_KEY` /
  `DWE_AGE_KEY_FILE` for CI. Secrets take two shapes: an `ENC[age:…]` scalar in
  any config layer, and a whole `*.age` file used as a `render config` pack
  source. Markers are decrypted in memory at load time, so `${vars.*}`,
  `exports.env`, the deployment hash and every other consumer of the merged
  config behave exactly as before.
  - New `dwe secrets` command tree: `init`, `status`, `set`, `get`, `encrypt`,
    `decrypt`, `rekey` and `key export|import|list|remove`, all with
    `--output json`. Not reachable from a bridged container; container *reads*
    through `dwe vars` stay open.
  - `secrets status` is read-only and no encrypted value can make it fail. It
    decrypts each value individually, so it distinguishes "no key on this
    machine" from "encrypted to somebody else" from "the payload is damaged",
    and names the source that supplied — or failed to supply — the identity.
  - New `secrets` validation domain with three validators: `recipient`,
    `unresolved` and `shadowed`. `unresolved` is cherry-picked into preflight,
    so `dwe run` / `deploy` / `reset` stop with a named fix instead of deploying
    a broken config. `shadowed` warns when a plaintext value in a higher layer
    overrides a marker — a green run then means the key pair really is what
    shares the value.
  - `dwe run`, `dwe restart` and the `dwe deploy` menu offer to import a missing
    identity and continue in the same invocation. Nothing changes without a
    terminal, with `--yes`, under `--output json` or `DWE_NONINTERACTIVE=1`, so
    CI output and exit codes are untouched.
  - `dwe secrets init --replace-recipient` recovers a project whose identity was
    lost: it mints a new key pair and names every value that has to be
    re-entered.
  - `init` / `set` / `rekey` edit the layer file line by line, so indentation,
    blank lines, comments, anchors and `<<:` merge keys survive byte-for-byte.
    A shape that cannot be edited in place is refused with
    `secrets_write_unsupported`, file untouched.
  - Full reference — model, marker format, every command's JSON shape, render
    guards, `age` CLI interoperability and rekey recovery:
    [`docs/reference/config/secrets.md`](docs/reference/config/secrets.md).

- **New `dwe deploy eject` and `dwe reset eject`**, which emit the built-in
  default pipeline as a commented, editable `deploy.yml` / `reset.yml`. This is
  the action to take when `dwe validate` reports that the file you have is
  inert. What is emitted is the built-in default only — nothing is rendered and
  per-service pipelines are not inlined, so `dwe deploy plan` remains the
  resolved instance. With no `--out` the document goes to stdout; `--out PATH`
  refuses an existing target unless `--force`. There is deliberately no
  lifecycle equivalent, and neither subcommand is reachable from a container.

- **Breaking: `COMPOSE_PROJECT_NAME` is the fourth reserved `.env` system
  variable.** The generated `.env` now ends its system block with the compose
  project name `dwe` passes as `-p` — `project_name` from `workspace/docker.yml`
  if set, otherwise `<project.prefix>-<project.name>`, always lowercased.
  Scripts and Makefiles that rebuilt it from `PROJECT` by hand can read the
  variable instead, and a raw `docker compose` from the project root now scopes
  to the same project as `dwe`, above any top-level `name:` in the compose file.
  A project whose `exports.env` already declares a rule with that name fails to
  load. → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

- **A dot-path that does not resolve is now reported instead of failing
  silently.** `dwe validate` warns on an `exports.env` rule's `from:` or `when:`
  and on a command's `params.<name>.default_from`,
  `params.<name>.options.from` or `context.<name>.from`. Until now
  `from: vars.db.passwrod` passed every check and `DB_PASSWORD=` reached every
  container as if declared that way; `dwe render env` now warns on stderr at the
  moment it writes that empty value, leaving stdout byte-identical. What an
  unresolved path costs depends on the field — an `exports.env` `when:` is
  falsy, so the rule is skipped and the variable is not written at all; a
  `default_from` falls through to `default:`; an `options.from` yields an empty
  option list. These are warnings, not errors — but `dwe validate --strict`
  treats warnings as errors.
  → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

### Changed

- **Breaking: container commands decide three runtime defaults themselves** —
  workdir and user now fall back to the target service (`service_exec`,
  `service_run`, `daemon`), a container TTY is given only to a command you
  launched yourself at a terminal (`service_exec`, `service_run`), and `mode:`
  defaults to `exec-or-run` instead of `exec-or-fail` (`service_exec`). None of
  the three changes the deployment hash, so a forced redeploy is required after
  upgrading. → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

- **Breaking: `dwe docs llms-txt` writes to a file with `--out PATH`.** The old
  `--output PATH` is gone and has no alias: it shadowed the root format flag of
  the same name, which is why `-o` failed on this command. `-o json` is now
  accepted and ignored, exactly as on `dwe docs show`.
  → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

- **Secrets change how several existing surfaces behave.** `-v` / `--debug`
  echoes, their `.dwe/logs` mirrors, and every plan and dry-run surface print
  `***` where a step references a decrypted value at least four runes long; what
  actually executes is never redacted, so `deploy plan --format shell` is now a
  preview rather than a script. `dwe render ide` / `ai` / `git` load a sanitized
  config with no decrypt pass, so a git-tracked output can only ever carry the
  committed marker. Without a usable identity a project still loads, but
  `dwe vars` renders `<encrypted>` and `dwe render env` / `config` fail rather
  than writing a marker into `.env` or a rendered service config; a compose
  project name that resolves to a marker fails resolution instead of scoping the
  stack under ciphertext. The root `.env`, `secrets decrypt` outputs and
  `.age`-sourced pack outputs are `chmod`ed to `0600`; `DWE_AGE_KEY` and
  `DWE_AGE_KEY_FILE` are stripped at the container shim; and `dwe prompt` falls
  back to the directory name rather than painting a marker into the prompt.

- **Pipeline log files record one line per committed line instead of one line
  per redraw frame.** A `\r`-terminated frame is held and evicted by the next
  one, so `50%\r100%\n` records one `100%` line; the per-sub-step files under
  `.dwe/logs/parallel/**` are covered too. How much noise this removes depends
  on the project: it collapses `\r` progress from tools that emit it, but
  **not** whole-block redraws driven by cursor-up sequences, such as compose's
  `[+] up 2/3`. One consequence — redraw frames no longer reach the file as they
  happen, so `tail -f .dwe/logs/deploy.log` shows committed lines only. The live
  view on the terminal is unaffected.

- **The compose isolation scanner no longer warns about a volume the project
  already declares `shared: true`.** The documented cross-project cache recipe
  used to earn two permanent, unfixable warnings per volume on every
  `dwe validate tests` and `dwe test run`, for a volume `dwe` creates itself.
  There is no new config surface — the volumes are recognised by the name they
  resolve to. They stay listed in `dwe test list --output json` under
  `cost_profile.isolation_findings`, marked `"shared": true`.

- **`dwe validate` names the built-in default pipeline instead of reporting a
  bare absence.** `config.deploy`, `config.lifecycle` and `config.reset` now
  distinguish absent / inert / parsed-but-declaring-nothing, the way
  `config.info` already did. The inert `deploy.yml` that `dwe init` scaffolds
  used to report **OK** while the built-in pipeline was what actually ran.
  → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

- **An unknown field in a config file now names the file, the line, the key and
  the fields that are allowed there**, instead of the YAML library's
  `field defaults not found in type config.DeployConfig`. A script grepping for
  the old `not found in type` text needs updating.
  → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

- **`dwe docs llms-txt` lists documentation topics as bare paths** — `- guides/add-a-service` instead of
  `- [add-a-service.md](dwe-docs://guides/add-a-service)` — under a lead line saying to read
  them with `dwe docs show <topic>`. The `dwe-docs://` scheme had no resolver, so an agent
  translated it back into that command anyway, while the label repeated the tail of the URI:
  2 KB of a 12 KB-capped document spent on ceremony. `--no-project` output drops from 11.3 KB
  to 9.3 KB. A consumer that parsed the markdown link form needs updating.

- **A failing command now prints its fix instruction in the terminal too.**
  Every typed `dwe` error carries a hint, and until now `--output json` was the
  only place it appeared. JSON output is byte-identical — the envelope already
  carried `hint`.

- **An `exports.env` value spanning multiple lines is now refused** instead of
  being written raw. `.env` values are unquoted, so compose parsed the second and
  later lines as further entries: the value arrived truncated, and a line shaped
  like `NAME=…` inside it became a variable nobody declared. Multi-line material
  belongs in a `render config` pack file.

- **`dwe validate secrets` reports success explicitly** — an `✓` row per
  validator (`validation result: 3 checks`) instead of
  `validation skipped (no files found)`, which was indistinguishable from the
  domain never running. A project with no `secrets:` block stays silent.

- `dwe vars` and `dwe vars list` now state in their help text that values are
  printed verbatim and are never masked;
  [`vars.md`](docs/reference/config/vars.md) gains a section on what to watch
  and why masking is deliberately not offered.

- `dwe vars set`, `dwe services enable` / `disable` and the setup wizard no
  longer rewrite a `<<: *anchor` merge key as `!!merge <<: *anchor` in
  `workspace/local.yml`.

- `dwe init` no longer steers new projects toward a per-service
  `render ai <name>` deploy step. The scaffold points at a single project-level
  `render ai`, which walks every service and honours each `render.ai.enabled`.
  Rendering on deploy stays opt-in.

- **Documentation fix: predicate builtins are `check:`-only.**
  `docs/reference/config/deploy/builtins.md` claimed the builtins on that page
  can be used in a `when:` guard. They cannot — `when: {type: builtin}` resolves
  against a separate predicate registry. No behaviour changed; the page was
  wrong.

### Removed

- **Breaking: the top-level `state:` key is gone.** It was a free-form string
  with a single consumer, and the docs claimed it was exported as `STATE` in
  `.env`, which it never was. There is no replacement: delete the key, and put
  free-form values under `vars:`, their single home.
  → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

- **Breaking: the top-level `ui:` block is gone**, along with the dedicated
  `config.ui` validator. Its three command-browser knobs had no adoption. The
  browser itself is unchanged and runs with the former defaults; the hotkey
  table, parameter-form overlay, fallback ladder and mouse behaviour that lived
  on the `ui` reference page are now documented under *Interactive browser* in
  the commands reference. → [Upgrading](docs/guides/upgrading.md#upgrading-to-060)

- **The Windows build stubs are gone**, so `GOOS=windows go build` now fails at
  compile time on purpose. Nothing ever shipped or tested a Windows binary, and
  one stub made `lock.Acquire` a no-op — such a build would have run the whole
  lifecycle without the deploy and snapshot locks. dwe supports macOS and Linux;
  on Windows run it inside WSL2. No behaviour changes on a supported platform.

### Fixed

- **A keyfile stored under the wrong filename is no longer invisible to
  `dwe secrets`.** `~/.config/dwe/keys/<recipient>.key` is the only path the
  identity lookup read, so a key filed under another recipient's name was
  skipped by every consumer — and `init --replace-recipient --yes` then saw
  nothing readable, letting its data-loss guard through and orphaning every
  encrypted value. Such a keyfile now counts as an identity everywhere.

- **A `default_from:` pointing at an empty YAML key no longer passes the literal
  text `<nil>` to the command.** `vars: {branch:}` resolved as "found", so
  `git checkout ${param.branch}` ran as `git checkout <nil>`. An empty key is now
  treated the way a missing one already was. The same fix covers a
  `context.<name>` whose `env:` variable was exported as `<nil>`.

- **A `${context.<name>}` that does not resolve now renders empty instead of the
  literal `<no value>`.** A declared, non-`required:` context whose `from:` path
  was a typo resolved to nil, and `text/template` spells that as `<no value>` —
  so `docker exec ${context.container}` ran against a container by that name.

- **An empty or all-comment `workspace/snapshot.yml` / `workspace/validate.yml`
  no longer fails with the bare message `EOF`.** Both loaders reject an empty
  document, unlike the pipeline files which fall back to the built-in default,
  and the error again names the file it came from.

[Unreleased]: https://github.com/semsemyonoff/dwe/compare/v0.6.2...HEAD
[0.6.2]: https://github.com/semsemyonoff/dwe/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/semsemyonoff/dwe/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/semsemyonoff/dwe/compare/v0.5.0...v0.6.0

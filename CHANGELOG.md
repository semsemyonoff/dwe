# Changelog

All notable changes to `dwe` are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Every change that a user can observe — a new or renamed flag, a changed default,
a removed config key, a different message — belongs under `## [Unreleased]`
before its pull request merges. Release notes are cut from this file, so an entry
that is missing here is missing from the release.

Releases up to and including `v0.5.0` predate this file. Their notes were
generated from commit subjects and stay on the
[GitHub releases page](https://github.com/semsemyonoff/dwe/releases).

## [Unreleased]

### Changed

- **Release binaries are built with Go 1.27, which requires macOS 13 Ventura or
  later.** The darwin archives and the Homebrew cask no longer start on macOS 12
  or earlier. Linux requirements are unchanged.
- **Building from source requires Go 1.27.** The `go` directive in `go.mod` is
  the minimum toolchain for `go install` and `make build`.
- **Templates move to go-sprout 1.1, which no longer accepts Sprig's argument
  order.** `get`, `set`, `unset`, `hasKey`, `pick`, `omit`, `append`,
  `prepend`, `slice` and `without` fail to render unless the map or list is the
  last argument; the old order used to be reordered silently with a warning.
- **`regexFindAll`, `regexSplit`, `regexReplaceAll` and `regexReplaceAllLiteral`
  take the string they work on last.** A template written for the old order
  still renders, to a wrong result. [Upgrading DWE](docs/guides/upgrading.md)
  has the before/after table and a search command.
- **`div` by zero is a render error** instead of an arbitrary number.

### Added

- Template functions `toUnix`, `toUnixMilli`, `toUnixMicro`, `fromUnix`,
  `fromUnixMilli`, `fromUnixMicro`, `escape` and `unescape`, from go-sprout 1.1.
- **`dwe validate` warns when a host script or shell step passes compose a
  project name not derived from `$COMPOSE_PROJECT_NAME`**
  (`tests.host_project_name`). A name such as
  `-p "${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}"` addresses the live stack
  from inside `dwe test`. The warning appears only in projects with
  `workspace/tests/`, fails `dwe validate --strict`, and its hint is the fix,
  `${COMPOSE_PROJECT_NAME:-<current value>}` — see
  [Upgrading DWE](docs/guides/upgrading.md).
- **`dwe vars set <var> --generate hex[:N]|base64url[:N]|uuid [--force]`**
  writes a random value to `workspace/local.yml`, so a setup step no longer
  needs a `python -c` one-liner for an app key or a Fernet key. `N` counts bytes
  of entropy (default 32); `base64url` is padded, and the value is always a
  string. A value already in `local.yml` is kept — the command refuses with
  `vars_value_exists` — unless `--force` is given. See
  [`dwe vars set`](docs/reference/config/vars.md#dwe-vars-set).

### Removed

- **`mustRegexFind`, `mustRegexFindAll`, `mustRegexMatch`, `mustRegexSplit`,
  `mustRegexReplaceAll` and `mustRegexReplaceAllLiteral`.** They were deprecated
  aliases; a template still calling one fails to parse. Drop the `must` prefix,
  and mind the new argument order where it applies.

### Fixed

- **`dwe docs show 'topic#anchor'` resolves an anchor whose heading starts with
  a hyphen.** A heading such as `` `--parallel N` `` was advertised as
  `--parallel-n` by GitHub, by the documentation site, and by the page's own
  table of contents, but the resolver trimmed the flag's leading hyphens and
  answered only to `parallel-n` — so following a link the docs themselves ship
  failed. The slug now keeps them, and the trimmed form still resolves, so both
  spellings work.
- **Cross-references in the Russian documentation point at the Russian
  anchors.** The mirror translates headings but kept the English anchors, so 140
  links across the reference and guides resolved to nothing — worst in
  `render/ai`, `git`, `ide`, `config`, `env` and `index`, where no entry in the
  page's own table of contents was navigable.
- **go-sprout's own diagnostics no longer print to stdout.** A deprecated
  template function or a Sprig-order call logged a `level=WARN` line into
  standard output, corrupting `--output json` and `dwe prompt`. They now go
  through the diagnostic trace and appear only under `--debug`.
- **A parallel workflow sub-step no longer loses its last line of output when
  that line has no trailing newline.** A sub-step ending in
  `printf 'error: x'; exit 1` used to drop `error: x` from both the failure dump
  and `.dwe/logs/parallel/workflow/<workflow-id>/<sub-command>.log` — in CI the
  dump is the only output, so the line explaining the failure was the one that
  disappeared. It now appears in both.
- **A line whose `\r\n` is split across a read boundary is no longer recorded as
  an empty line.** When the carriage return and the newline arrived in separate
  reads from the child process, the line was replaced by a blank one in
  `.dwe/logs/<pipeline>.log`, in the per-sub-step logs under
  `.dwe/logs/parallel/` and in the parallel failure dump — in CI, where the log
  is the only output, the swallowed line was exactly the one being read. The
  same now applies to the `\r\x1b[K\n` redraw idiom, which the workflow
  runner's log blanked even when it arrived in one write. In a parallel block
  the live row now keeps showing that line instead of briefly blanking; nothing
  else in the live view changes.
- **A parallel workflow failure dump no longer leaves the terminal coloured.**
  The dump forwards the sub-step's own ANSI so its colours survive, but the
  child's closing reset does not always reach it — a reset written after the
  last newline is dropped as carrying no line, one written between a `\r` and
  its `\n` is replaced by the content line, and a killed child never writes one
  at all. The colour then bled into the dump's closing bar and every later
  message. The dump now closes the colour state itself; a dump whose output
  carries no escape bytes stays escape-free for log scrapers.
- **`dwe test` now warns about a compose host port it cannot remap because the
  port comes from a variable.** A port such as `"${VALKEY_PORT:-6379}:6379"`,
  exported `from: vars.ports.valkey`, kept its original value in the test copy
  and collided with the live stack at bind time, with nothing said beforehand.
  `dwe test run` and `dwe validate` now warn with the fix line,
  `env.vars: { ports.valkey: auto }`, and `dwe test list --output json` reports
  it as an `interpolated_host_port` entry in `cost_profile.isolation_findings`.
  The warning fails `dwe validate --strict` until every scenario is covered —
  see [Upgrading DWE](docs/guides/upgrading.md). A port whose variable no
  `exports.env` rule traces names no scenarios and needs such a rule first. A
  literal host port behind an interpolated bind address
  (`"${BIND:-127.0.0.1}:8080:80"`) is now recognised and blocks `dwe test run`
  like any other literal host port.
- **`dwe test` remaps the host ports of a `required: true` service a scenario
  lists under `env.services.disable`.** A required service cannot be disabled,
  so it still ran in the test copy, but on its original ports — colliding with
  the live stack and with other scenarios under `--parallel`. It now gets free
  ports like every other service that runs in the copy.
- **The built-in deploy pipeline brings a stopped stack back up.** After a
  `dwe stop`, `dwe deploy run` printed `Phase: start` and `✓ Done`, and the
  stack stayed down: the `up` step had no `check:`, so the journal skipped it on
  every deploy after the first. It now carries
  `check: {type: builtin, cmd: containers_running}`, runs on every deploy, and
  the built-in pipeline no longer exits `already up-to-date`.
  `containers_running` accepts an absent or empty `services` list, which checks
  that every non-one-off container of the compose project is running or exited
  0. The first deploy after upgrading sees the project config as changed once —
  pick `Apply changes` in the selector — and an ejected or hand-written
  `workspace/deploy.yml` needs the `check:` added by hand; see
  [Upgrading DWE](docs/guides/upgrading.md).
- **Inside `dwe test`, a scenario's shell steps see the copy's
  `COMPOSE_PROJECT_NAME`.** A host script written as
  `${COMPOSE_PROJECT_NAME:-dwe-myproj}` used to fall back to the live name there
  and address the live stack; it now gets the disposable copy's name, also when
  scenarios run with `--parallel`. Shell steps and shell `check:` of
  `dwe stop`, `dwe restart` and `dwe reset` also get dwe's own name — the one
  it passes as `-p` — instead of one inherited from your shell.
- **`type: script` commands receive `COMPOSE_PROJECT_NAME` and `COMPOSE_FILE`**,
  like `type: shell` commands always have.
- **Commands run from snapshot workflows, service-toggle hooks and reset hooks
  honour `docker.yml` `project_name`.** They used to fall back to
  `<prefix>-<name>`, so on a project with a custom `project_name` a container
  command there exec'd into the wrong compose project and missed the
  `docker.yml` `args`; the shell and script contract carried the same wrong
  name.

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

[Unreleased]: https://github.com/semsemyonoff/dwe/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/semsemyonoff/dwe/compare/v0.5.0...v0.6.0

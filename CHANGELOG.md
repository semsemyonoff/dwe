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

### Added

- **Encrypted secrets committed to the repository**, so a value the whole team
  shares — a bot token, a service-account JSON — can live in git without
  sitting there in the open. One X25519 [age](https://age-encryption.org) key
  pair per project: the public recipient is committed as `secrets.recipient` in
  `workspace.yml` (so anyone with the repo can **add** a secret), the private
  identity lives in `~/.config/dwe/keys/<recipient>.key` or in `DWE_AGE_KEY` /
  `DWE_AGE_KEY_FILE` for CI (so only identity holders can **read** one).
  Secrets take two shapes: an `ENC[age:…]` scalar in any config layer, and a
  whole `*.age` file used as a `render config` pack source. Markers are
  decrypted in memory at load time, so `${vars.*}`, `exports.env`, the
  deployment hash and every other consumer of the merged config see plaintext
  and behave exactly as before.
- **New `dwe secrets` command tree** in the configuration group: `init`,
  `status`, `set`, `get`, `encrypt`, `decrypt`, `key export`, `key import` and
  `rekey`. All of them support `--output json`. `secrets status` is read-only
  and no encrypted value can make it fail — it decrypts every marker and `.age`
  source individually, so it distinguishes "no key on this machine" from
  "encrypted to somebody else" from "the payload is damaged", and reports a
  half-rekeyed tree per value. `secrets set` takes the value from an argument, `--stdin` or a hidden
  prompt, writes only under `vars.`, and never coerces types. A value shorter
  than 4 characters is stored like any other, but `secrets set` warns on stderr
  that redaction deliberately skips it.
  `dwe secrets` is **not** reachable from a bridged container.
- **New `secrets` validation domain**, with `dwe validate secrets`.
  `secrets.recipient` reports a missing / malformed recipient and damaged
  payloads; `secrets.unresolved` reports what this machine cannot decrypt and
  is the second validator (after `config.validate`) cherry-picked into
  preflight, so `dwe run` / `deploy` / `reset` and the deploy wizard stop with
  a named fix instead of deploying a broken config.
- **New reference page** [`docs/reference/config/secrets.md`](docs/reference/config/secrets.md)
  (plus the Russian mirror), covering the model, the marker format, key
  locations, every command with its JSON shape, the render guards, where
  plaintext goes, `age` CLI interoperability and rekey recovery.
- **An `.age` source the scan could not read reports the stable `unreadable`
  reason**, with the cause (a symlink refusal, an OS error) moved into a new
  `detail` field. `reason` is a closed vocabulary a script switches on, and
  those two rows used to carry free-form text — an absolute path included.
- **A `DWE_AGE_KEY_FILE` holding the identity TEXT instead of a path is
  redacted** wherever the failure is worded. Its sibling `DWE_AGE_KEY` does
  take the text, so the mixup is easy — and every message about the failed
  read used to print the private key, `--output json` included.
- **A damaged `*.age` pack source now reports as `corrupt` without a key.** The
  identity-free header check the `ENC[age:…]` markers already had now covers
  native pack sources too, so a truncated file no longer reads as
  `no_identity` on a machine that has no identity — which sent the one reader
  who could not have opened it anyway looking for a key.
- **A broken identity source now reports as `invalid_identity`** instead of
  `no_identity`, in `dwe secrets status`, `dwe validate secrets` and
  `SecretsState`. A truncated `DWE_AGE_KEY` or a keyfile that lost its key line
  is a source to repair, not a key to obtain, and the validator says which
  source it is (`$DWE_AGE_KEY is set but holds no age identity`) from fixed
  wording — the `age` parse error is never echoed, because its text repeats the
  input's private-key bytes.
- **`dwe secrets status` now says what is actually wrong with the identity.**
  The header has four shapes instead of two: the source that supplied the key,
  `none (looked at …)`, `invalid (…)` naming the source that is set but holds
  no key, and `wrong recipient (…)` naming both recipients. Whenever the
  identity did not load, the report closes with the fix instruction. In
  `--output json`, `identity.source` is now the **consulted** source on failure
  too (it used to go empty), and `identity` gained `reason` and `hint`; every
  string is DWE-authored, never an `age` parse error.
- **New `dwe secrets key list` and `dwe secrets key remove <recipient>`** for
  the machine-wide keys directory. `list` reports every `~/.config/dwe/keys/*.key`
  with a fixed state (`ok`, `unreadable`, `unparsable`, `misnamed`) and marks
  the one this project uses; the content of a file that does not parse is never
  echoed. `remove` deletes the file named by its argument, refuses the file that
  HOLDS the current project's identity without `--force` (the guard reads the
  file, not its name) — and, for the same reason, refuses a file whose bytes it
  cannot read at all, since deleting one needs no read permission on it — and
  needs `--yes` wherever it cannot ask. Both run outside a
  project (the directory is not project-scoped) and neither is reachable from a
  bridged container. New error codes: `secrets_key_in_use`,
  `secrets_key_unreadable`, `secrets_key_not_found`,
  `secrets_confirmation_required`,
  `secrets_recipient_invalid`, `secrets_key_list_failed`,
  `secrets_key_remove_failed`.
- **`dwe secrets key import` asks for the identity** when it runs at a terminal
  with no `--file` and nothing piped. The field is hidden and validates in
  place, so a key that does not parse — or one belonging to another project —
  is corrected without losing the form, and an identity that is already
  installed is reported **before** the form opens rather than after the key was
  typed. A successful import now ends with what the key opened: a second line
  `N encrypted value(s) and M .age file(s) are now readable`, and
  `markers_readable` / `files_readable` in `--output json` — replaced by a
  `report_error` when the encrypted surface could not be scanned at all, since
  the import itself still succeeded and a hard zero there would read as "your
  key opens nothing" (the same line replaces the counts in the `dwe run` /
  `dwe deploy` key offer). The first output
  line and the pre-existing JSON fields are unchanged, `--file` and piped
  imports behave exactly as before, and the prompt never opens without a
  terminal, under `--output json` or under `DWE_NONINTERACTIVE=1`. Cancelling
  it is the new `secrets_import_cancelled`.
- **`dwe run`, `dwe restart` and the `dwe deploy` menu now offer to take the
  missing identity** instead of only reporting that it is missing — the
  new-machine path no longer requires knowing `dwe secrets key import` by
  heart. The offer appears when the project has encrypted material, no usable
  identity and a human at a terminal; accepting opens the same hidden prompt
  and the command **continues in the same invocation** (the gate runs on the
  raw config layers before the config is loaded, so there is no reload and no
  window in which the wizard proceeds with unresolved state). `dwe restart`
  offers **before it stops anything**, so declining leaves the stack running,
  and declining ends the command with the fix instruction — typed
  `secrets_no_identity` in the `dwe deploy` menu, the same sentence untyped in
  `dwe run` / `dwe restart` — without ever reaching preflight. Nothing changes without a terminal, with `--yes` (which only
  `dwe run` and `dwe restart` define), with
  `--output json` or under `DWE_NONINTERACTIVE=1`: the `secrets.unresolved`
  preflight wall fires exactly as before, so CI output and exit codes are
  untouched. `dwe deploy run`, `dwe reset` and `dwe render env` / `config`
  keep their hard error and hint. A `DWE_AGE_KEY` / `DWE_AGE_KEY_FILE` that is
  set but does not hold the project's identity is **reported, never
  prompted** — the lookup takes the first present source with no fall-through,
  so an imported keyfile would not even be consulted; the message names the
  variable to repair, and never its value.
- **New `dwe secrets init --replace-recipient` for a lost identity**, and a
  refusal that stops sending you there in the first place. A second `init` used
  to point at `dwe secrets rekey` unconditionally — but `rekey` has to read
  every value before it can rewrite one, so with the identity gone it is a
  command that cannot run, and the only way out was hand-editing `secrets:` out
  of the tracked `workspace.yml`. The refusal now branches on whether the
  project's identity loads here and says so in a new `identity` detail
  (`available` / `missing`): with it present, `rekey` as before; without it,
  `key import` first and `init --replace-recipient` as the recovery.
  `--replace-recipient` mints a new key pair and commits it, leaving every
  existing marker and `*.age` source in place and permanently unreadable — they
  are the record of what has to be re-entered, `dwe secrets set` overwrites each
  one as you go, and the report names every orphaned value (`old_recipient`,
  `orphaned_markers` and `orphaned_files` in `--output json`). It refuses while
  anything is still readable on this machine (new `secrets_identity_available`,
  with a `readable` count and the way out), needs a confirmation naming the
  number of values at stake, and refuses with `secrets_confirmation_required`
  wherever it cannot ask. The confirmation runs before the project locks are
  taken, so a prompt left open does not stall every other `dwe` command; the
  recipient is re-read once they are held and a concurrent change refuses with
  `secrets_recipient_changed` without writing anything.
- **The `dwe secrets` write commands are styled like the read ones.** `init`,
  `rekey`, `key import`, `key remove`, `set`, `encrypt` and `decrypt` now render
  through the same field-block and colour vocabulary as `secrets status` and
  `secrets key list`, instead of plain text. Colour degrades to none when the
  output is not a terminal, `--output json` is unaffected, and `key export`,
  `secrets get` and `--out -` still print their raw bytes with nothing added.
- **Breaking: `COMPOSE_PROJECT_NAME` is now the fourth reserved `.env` system
  variable.** The generated `.env` ends its system block with
  `COMPOSE_PROJECT_NAME=<name>`, where `<name>` is the compose project name
  `dwe` passes as `-p`: `project_name` from `workspace/docker.yml` (or
  `docker.local.yml`), otherwise `<project.prefix>-<project.name>`, always
  lowercased. It can differ from `PROJECT`, which stays the verbatim
  `project.name`, and it is the same value the `type: shell` command contract
  already exported. The line is omitted when the name resolves empty (`dwe`
  omits `-p` in that case too), and a broken `${...}` in `project_name` fails
  the render instead of writing a guessed name. Scripts and Makefiles that
  rebuilt the compose name from `PROJECT` by hand can read the variable
  instead; it is regenerated on every `dwe run` and `dwe render env`, so no
  forced redeploy is needed.
  Two consequences. A project whose `exports.env` already declares a rule named
  `COMPOSE_PROJECT_NAME` now fails to load for **every** command with
  `exports.env[N]: "COMPOSE_PROJECT_NAME" is a reserved system variable …` —
  delete the rule, the built-in line carries the same value. And because `.env`
  sits in the compose project directory, a raw `docker compose` run from the
  project root — and a pipeline `type: shell` step that calls compose itself —
  now scopes to dwe's project name, above any top-level `name:` in the compose
  file; a compose file declaring a divergent `name:` (what `dwe validate`
  reports as `config.compose_project_name`) loses sight of the resources it
  created under the old name. Either set `project_name` in `workspace/docker.yml`
  to that old value and redeploy once, or write `name: ${COMPOSE_PROJECT_NAME}`
  in the compose file, which now resolves from `.env`.
- **A dot-path that does not resolve is now reported instead of silently
  rendering empty.** `dwe validate` warns on an `exports.env` rule whose `from:`
  or `when:` path is not in the merged config (`config.exports`), and on a
  command's `params.<name>.default_from`, `params.<name>.options.from` or
  `context.<name>.from` (`commands` domain). Until now `from: vars.db.passwrod`
  passed every check and `dwe render env` wrote `DB_PASSWORD=`, an empty value
  that reached every container as if it had been declared that way. The hint
  names the consequence for the rule at hand — the variable renders empty, the
  `default:` is always used, or the render fails on a `required:` rule. The
  `when:` gate is read first, exactly as the renderer reads it: a rule whose
  gate is falsy right now is still reported, but the hint says nothing is
  written either way and the miss surfaces once the gate turns on, and a rule
  whose `when:` cannot resolve at all is reported once, on the gate, rather
  than twice. These
  are warnings, not errors: `from:` with a `default:` is a legitimate optional
  path, and a path may live in a `local.yml` that is not on this machine. Note
  that `dwe validate --strict` treats warnings as errors, so a CI job using it
  can start failing on a path that was deliberately optional — give such a rule
  a `default:`, or correct the path.
- **`dwe render env` warns about each variable it renders empty**, on stderr,
  at the moment the empty value is produced:
  `warning: exports.env[DB_PASSWORD]: from "vars.db.passwrod" does not resolve —
  rendered empty`. stdout is untouched, so `dwe render env > .env` is
  byte-identical to before, and `--output json` prints no warning.

### Changed

- **The compose isolation scanner no longer warns about a volume the project
  already declares `shared: true`.** The documented cross-project cache recipe
  — a `docker.yml` `resources.volumes.<key>` with `shared: true` plus the
  matching raw compose `external: true` / `name:` declaration — used to earn
  two permanent, unfixable warnings per volume (`external_volume` and
  `named_volume`) on every `dwe validate tests` and `dwe test run`, for a
  volume `dwe` creates itself. Those volumes are now recognised by the name
  they resolve to — an explicit `name:`, else the name carried by compose's
  legacy `external: { name: … }` long form, else the compose map key — and are
  silent. There is no new config surface. They stay
  listed in `dwe test list --output json` under
  `cost_profile.isolation_findings`, marked `"shared": true` — the profile
  reports facts, not verdicts — and the key is omitted for every
  unacknowledged finding, so that output is otherwise unchanged.
- **`dwe validate` names the built-in default pipeline instead of reporting a
  bare absence.** `config.deploy`, `config.lifecycle` and `config.reset` now
  distinguish three states, the way `config.info` already did: an absent
  `deploy.yml` / `lifecycle.yml` reports at info as `no deploy.yml — built-in
  default pipeline is active`; a file that is empty or all comments reports
  `has no active content (all comments or empty) — built-in default pipeline is
  active`. The second state used to report **OK**, which was actively
  misleading — the inert `deploy.yml` that `dwe init` scaffolds runs the
  built-in pipeline, not the one in the file. A file that parses but carries no
  pipeline reports at info for the same reason: `deploy.yml` / `reset.yml`
  `declares no phases`, and `lifecycle.yml` `declares no run: section` (or
  `stop:`), each naming the built-in default that runs in its place. An absent
  `reset.yml` stays silent as before, since the scaffold never ships one.
- **An unknown field in a config file now names the file, the line, the key and
  the fields that are allowed there.** Every strictly decoded file — the
  pipelines, `service.yml`, `snapshot.yml`, `validate.yml`, command files,
  template-pack manifests, test scenarios, `setup.yml` and translation bundles
  — used to surface the underlying YAML library's `field defaults not found in
  type config.DeployConfig`, a Go type name nothing in the docs mentions. It
  now reads `workspace/deploy.yml:12: unknown field "defaults" — allowed here:
  log, phases`, followed once by a hint that a field you did not
  invent may come from a newer `dwe`. The unknown top-level key error carries
  the same hint next to its existing `vars:` advice. A script grepping for the
  old `not found in type` text needs updating.
- **Documentation fix: predicate builtins are `check:`-only.**
  `docs/reference/config/deploy/builtins.md` claimed the builtins on that page
  can be used in a `when:` guard. They cannot — `when: {type: builtin}`
  resolves against the separate predicate registry (`dir-exists`,
  `file-missing`, …), which shares nothing with the builtin registry. No
  behaviour changed; the page was wrong.
- **A failing command now prints its fix instruction in the terminal too.**
  Every typed `dwe` error carries a hint, and until now `--output json` was the
  only place it appeared: the error text handed to the renderer is the message
  alone. The hint is written under the error block in the muted colour, wrapped
  to the same width, and unstyled wherever the block itself is unstyled. JSON
  output is byte-identical — the envelope already carried `hint`.
- **An age identity is now read as the first `AGE-SECRET-KEY-1…` token on a
  non-comment line**, instead of the first line that is neither blank nor a `#`
  comment. Every shape that worked before still works, including a file whose
  live key sits below a commented-out old one, and one that did not now does: a
  keyfile whose lines a paste joined into one (`# public key: age1…
  AGE-SECRET-KEY-1…`, previously read as a comment and skipped) parses, because
  when every token sits inside a comment the whole text is scanned instead —
  unless a live line carries a damaged `AGE-SECRET-KEY-1…`, in which case the
  fallback is suppressed so a commented-out old key cannot answer for it and
  turn a truncation into `wrong_identity`. A
  later token is still ignored rather than an error. Text that holds no token is the new
  `ErrInvalidIdentity` — surfaced as `invalid_identity`, not `corrupt`.
- **Without a usable identity a project still loads**, but surfaces degrade
  explicitly rather than silently: `dwe vars list` / `get` / `inspect` and the
  vars TUI render `<encrypted>` instead of the ciphertext (`vars list`,
  `get` and `inspect` gained an `omitempty` `encrypted` field in JSON, and
  `inspect` a `secret` note), while `dwe render env` and `dwe render config`
  now **fail** rather than write an `ENC[age:…]` marker into `.env` or a
  rendered service config. The `.env` guard covers every write site —
  `render env`, the compose auto-regeneration, `services enable`/`disable`,
  and the render `dwe run` performs before its preflight.
- `dwe render ide` / `ai` / `git` and their validators now load a **sanitized**
  config with no decrypt pass, so a git-tracked template output carries the
  committed marker and can never contain a decrypted secret.
- `-v` / `--debug` command echoes — and their `.dwe/logs` mirrors — now print
  `***` in place of any decrypted value at least 4 runes long. Child-process
  output is not redacted.
- **Plan and dry-run output is redacted too.** `dwe deploy plan` (table,
  `--format shell` and `--output json`, including the `unresolved` field),
  `dwe reset plan` and `dwe reset step --dry-run` print `***` where a step
  references a decrypted secret — plan output is what gets pasted into tickets
  and PR descriptions. Redaction happens in the display functions that build
  those lines, before the value is quoted or embedded into a `--set k=v`
  argument, so `--format shell` is now a **preview of what will run, not a
  script to execute** when a step references a secret. What actually executes
  is never redacted.
- **`dwe secrets init` / `set` / `rekey` no longer reformat the layer file.**
  They now edit it by replacing single lines instead of re-encoding the whole
  document, so indentation, blank lines, comments, anchors, `<<:` merge keys
  and quoting survive byte-for-byte: one `secrets set` into a large annotated
  `defaults.yml` is a one-line diff instead of a whole-file rewrite. A handful
  of shapes cannot be edited in place — a multi-line block scalar, a wrapped
  scalar, a target inside a flow collection, a `null` / sequence / non-mapping
  scalar / alias parent — and are refused with the new
  `secrets_write_unsupported` code, naming the path and the fix, with the file
  untouched. `rekey` detects those shapes in its read-only pass, so it aborts
  with `written: false` before minting a key pair or re-encrypting any `.age`
  source, rather than part-way through. Descending through an existing scalar
  (`vars.db.host.port` where
  `host` is a string) previously reported `secrets_write_failed`; it now
  reports `secrets_write_unsupported` like the other refused shapes.
- `dwe vars set`, `dwe services enable` / `disable` and the setup wizard no
  longer rewrite a `<<: *anchor` merge key as `!!merge <<: *anchor` in
  `workspace/local.yml`.
- **`dwe validate secrets` now reports success explicitly.** A healthy project
  gets an `✓` row per validator (`validation result: 2 checks`) instead of
  `validation skipped (no files found)`, which was indistinguishable from the
  domain never running — on the one command a developer uses to check
  themselves after key onboarding. The `secrets.unresolved` row counts what it
  read (`N encrypted value(s) and M config-pack source(s) readable via
  keyfile`); the identity is named by source word, never by path. A project
  with no `secrets:` block and nothing encrypted stays silent, and preflight
  and the deploy wizard filter the rows out as before.
- The root `.env`, decrypted `secrets decrypt` outputs and `.age`-sourced pack
  outputs are now explicitly `chmod`ed to `0600`, so a pre-existing permissive
  file is tightened rather than left as-is.
- `DWE_AGE_KEY` and `DWE_AGE_KEY_FILE` are stripped from the container
  environment at the shim and re-supplied from the daemon's own environment, so
  a container cannot point the host `dwe` at an identity file of its choosing.
- **An `exports.env` value spanning multiple lines is now refused** instead of
  being written raw. `.env` values are unquoted, so compose parsed the second
  and later lines as further entries: the value arrived truncated to its first
  line, and a line shaped like `NAME=…` inside it became a variable nobody
  declared. Multi-line material — a PEM key, a service-account blob, exactly
  what `dwe secrets set --stdin` accepts — belongs in a `render config` pack
  file, not in `exports.env`.
- **`dwe prompt` no longer paints an `ENC[age:…]` marker into the shell
  prompt.** The prompt hot path reads a `workspace.yml` stub and never loads
  the full config, so it cannot decrypt: an encrypted `project.name` now falls
  back to the directory name, and an encrypted `project.prefix` or `name` falls
  back to the display name when building the compose label filter, instead of
  building one that matches no container and reporting the stack as stopped.


- **Breaking:** container commands now decide three runtime defaults themselves
  instead of inheriting whatever the caller happened to wire up. Each default
  covers a different set of command types, so check which ones apply to you.
  **(1) Workdir and user — `service_exec`, `service_run` and `daemon`.** A
  command with no `workdir:` falls back to the target service's `cli.workdir` →
  `work_dir_internal` → `dir_internal` — the same order `dwe shell` applies —
  so a command and a shell session into the same service finally land in the
  same directory instead of the image's `WORKDIR`. A command whose workdir is
  literally the relative path `internal` now reads that as an opt-out sentinel
  — no `--workdir` flag and no service fallback — mirroring `user: internal`.
  `docker_daemon_start` runs the same chain, and three daemon-specific
  behaviours change with it: `workdir_from` now beats a literal `workdir` (it
  was the inverse); a `workdir_from` dot-path that resolves to nothing falls
  through to the next rung instead of aborting `.start` — a hard error becomes
  a success in a different directory; and a daemon declaring no `user:` now
  inherits the service's `cli.user`, where it previously ran as the image's
  `USER`. That last one changes the ownership of everything the daemon writes,
  so check any daemon whose target service sets `cli.user` and pin `user:` on
  it if the old uid mattered. A non-string `workdir_from` value is still an
  error.
  **(2) Container TTY — `service_exec` and `service_run` only.** A container
  process gets a terminal only when you launched the command yourself and dwe's
  own streams are terminals (or the run is bridged). Everything else — pipeline
  steps, `parallel:` sub-steps, `check:` probes, piped or redirected output —
  now passes `-T`, with colour forced so suppressing the TTY does not turn the
  output grey. Declare a TTY flag in `compose_args:` or in `docker.yml`'s
  `args:` to take the decision back. `type: daemon` is unaffected: `.start`
  builds its own argv and is always detached.
  **(3) Exec mode — `service_exec` only.** `mode:` now defaults to
  `exec-or-run` instead of `exec-or-fail`: a command whose service is stopped
  falls back to a one-off `docker compose run --rm` (announced on stderr)
  rather than refusing. Declare `mode: exec-or-fail` on commands that must
  never create a container. Commands that already declared `mode:` are
  unchanged. Note that a `type: command` `check:` pointing at a `service_exec`
  command with no `mode:` becomes container-creating, and that the one-off runs
  with `--no-deps`, so such a probe can report success against a stack that is
  down; such checks should declare `mode: exec-or-fail`. A survey of nine local
  workspaces found 38 `check:` actions, all `type: builtin`, `type: shell` or
  `auto` and none of `type: command`, so no existing project is affected by
  that today.
  After upgrading, run a full forced redeploy: none of the three changes alters
  the deployment hash, so a `type: command` step whose semantics moved reports
  `already up-to-date` and is skipped.
- **Breaking:** `dwe docs llms-txt` writes to a file with `--out PATH`; the old
  `--out`-equivalent `--output PATH` is gone and has no alias. The local
  `--output` shadowed the root format flag of the same name, which is why `-o`
  failed there with `Unknown shorthand flag`. An alias would have preserved the
  shadowing. `-o json` is now accepted and ignored on this command, exactly as
  on `dwe docs show` — the document is itself the payload.
- `dwe vars` and `dwe vars list` now state in their help text that values are
  printed verbatim and are never masked. `docs/reference/config/vars.md` gains
  an "Output is not redacted" section explaining what to watch and why masking
  is deliberately not offered.
- `dwe init` no longer steers new projects toward a per-service
  `render ai <name>` deploy step. The scaffolded `AGENTS.md` and the commented
  per-service `deploy.yml` skeleton now point at a single project-level
  `render ai`, which walks every service and honours each `render.ai.enabled`.
  Rendering on deploy stays opt-in; nothing renders automatically.

### Removed

- **Breaking:** the top-level `state:` key is gone. It was a free-form string
  with a single consumer — one line in the bare-`dwe` summary — and the docs
  claimed it was exported as `STATE` in `.env`, which it never was. A project
  that still declares it in `workspace.yml`, `workspace/defaults.yml` or
  `workspace/local.yml` now fails to load with the strict-root error naming the
  file (`<file>: unknown top-level key "state" — move custom values under
  "vars:" (e.g. vars.state.*); allowed top-level keys: …`). There is no
  replacement: delete the key, and put free-form values under `vars:`, their
  single home.
- **Breaking:** the top-level `ui:` block is gone. Its three command-browser
  knobs (`ui.commands.default_expanded_depth`, `auto_collapse_empty`,
  `show_type_badges`) had no adoption, and the dedicated `config.ui` validator
  goes with them. A project that still declares the block in any of the three
  layers now fails to load with the strict-root error naming the file
  (`<file>: unknown top-level key "ui" — move custom values under "vars:" (e.g.
  vars.ui.*); allowed top-level keys: …`); delete the block. The command browser
  itself is unchanged and runs with the former defaults — top-level groups
  expanded, empty subtrees collapsed during fuzzy filtering, type badges on.
  The hotkey table, parameter-form overlay, fallback ladder and mouse behaviour
  that lived on the `ui` reference page are now documented under
  *Interactive browser* in the commands reference.
- **The Windows build stubs are gone.** dwe supports macOS (Intel + Apple
  Silicon) and Linux; on Windows run it inside WSL2 with dwe installed in the
  distro. Nothing ever shipped or tested a Windows binary — the stubs only kept
  `GOOS=windows go build` type-checking, and one of them made `lock.Acquire`
  return "file locking is not supported on Windows", so such a binary would
  have run the whole lifecycle without the deploy and snapshot locks. That
  cross-build now fails at compile time on purpose. No behaviour changes on a
  supported platform.

### Fixed

- **A `default_from:` pointing at an empty YAML key no longer passes the literal
  text `<nil>` to the command.** `vars: {branch:}` — a key written with no value
  — resolved as "found", and the resolver rendered it with Go's default
  formatting, so `git checkout ${param.branch}` ran as `git checkout <nil>`. An
  empty key is now treated the way a missing one already was: the `default:`
  applies, and a `required:` param fails with its own message. The same fix
  covers a `context.<name>` whose `env:` variable was exported as `<nil>`.
- **A `${context.<name>}` that does not resolve now renders empty instead of the
  literal `<no value>`.** A declared, non-`required:` context whose `from:` path
  is a typo resolved to nil, and `text/template` spells a nil interface as
  `<no value>` — so `docker exec ${context.container}` ran against a container
  by that name. The template resolver now treats a present-but-nil value the
  way it already treated a missing key, which is also what `dwe validate`
  promises when it warns about an unresolvable `context.<name>.from`.
- **An empty or all-comment `workspace/snapshot.yml` / `workspace/validate.yml`
  no longer fails with the bare message `EOF`.** Both loaders reject an empty
  document (unlike the pipeline files, which fall back to the built-in default),
  and the error again names the file it came from.

[Unreleased]: https://github.com/semsemyonoff/dwe/compare/v0.5.0...HEAD

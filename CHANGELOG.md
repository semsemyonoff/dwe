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
- **A broken identity source now reports as `invalid_identity`** instead of
  `no_identity`, in `dwe secrets status`, `dwe validate secrets` and
  `SecretsState`. A truncated `DWE_AGE_KEY` or a keyfile that lost its key line
  is a source to repair, not a key to obtain, and the validator says which
  source it is (`$DWE_AGE_KEY is set but holds no age identity`) from fixed
  wording — the `age` parse error is never echoed, because its text repeats the
  input's private-key bytes.

### Changed

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

[Unreleased]: https://github.com/semsemyonoff/dwe/compare/v0.5.0...HEAD

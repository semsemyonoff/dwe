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

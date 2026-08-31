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

[Unreleased]: https://github.com/semsemyonoff/dwe/compare/v0.5.0...HEAD

#!/usr/bin/env bash
# Extract one version's section from CHANGELOG.md so goreleaser can publish it
# verbatim as the release notes (`goreleaser release --release-notes=<file>`).
#
# Exiting non-zero on a missing or empty section is the point, not a side
# effect: it is what stops a tag whose changes were never written down from
# reaching the releases page with a blank body.
#
# Usage: scripts/changelog-release-notes.sh <tag-or-version> [output-file]
#   scripts/changelog-release-notes.sh v0.6.0            # to stdout
#   scripts/changelog-release-notes.sh v0.6.0 notes.md   # to a file

set -euo pipefail

if [ $# -lt 1 ]; then
  echo "usage: $0 <tag-or-version> [output-file]" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
changelog="$repo_root/CHANGELOG.md"

# Accept both `v0.6.0` and `0.6.0`; CHANGELOG headings carry the bare version.
version="${1#v}"
output="${2:-}"

if [ ! -f "$changelog" ]; then
  echo "changelog-release-notes: $changelog not found" >&2
  exit 1
fi

# Print everything between `## [<version>]` and the next `## ` heading. The
# heading itself is dropped — the release page already shows the tag.
notes="$(awk -v version="$version" '
  $0 ~ "^## \\[" version "\\]" { in_section = 1; next }
  in_section && /^## / { exit }
  in_section { print }
' "$changelog")"

# Trim leading and trailing blank lines so an all-whitespace body reads as empty.
notes="$(printf '%s\n' "$notes" | sed -e '/./,$!d' -e :a -e '/^\n*$/{$d;N;ba' -e '}')"

if [ -z "$notes" ]; then
  echo "changelog-release-notes: CHANGELOG.md has no entries under '## [$version]'." >&2
  echo "Add the section (move '## [Unreleased]' entries into it) before tagging." >&2
  exit 1
fi

if [ -n "$output" ]; then
  printf '%s\n' "$notes" >"$output"
else
  printf '%s\n' "$notes"
fi

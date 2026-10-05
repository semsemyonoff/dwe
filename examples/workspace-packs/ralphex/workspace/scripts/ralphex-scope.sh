#!/bin/sh
# Keep the previous run state until the wrapper has validated the new scope.
set -eu

fail() {
  printf 'ralphex-scope: %s\n' "$*" >&2
  exit 1
}

root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)
cd "$root"
[ "$#" -eq 0 ] || fail 'use dwe cmd ralphex.scope --set repos=... --set base=... --set branch=...'
[ -n "${RALPHEX_REPOS:-}" ] || fail 'repos is required'
[ -n "${RALPHEX_BASE:-}" ] || fail 'base is required'
[ -n "${RALPHEX_BRANCH:-}" ] || fail 'branch is required'
case "$RALPHEX_BASE$RALPHEX_BRANCH" in
  *'
'*) fail 'base and branch must each contain one nonempty line' ;;
esac
git check-ignore -q -- .ralphex/run/repos ||
  fail 'run state must be ignored: add /.ralphex/run/ and remove an older /.ralphex/ rule from the root .gitignore'
[ -d .ralphex ] && [ ! -L .ralphex ] || fail 'render the ralphex workspace pack first'
[ -x .ralphex/scripts/ws-git ] || fail 'render the ralphex workspace pack first'
run=.ralphex/run
[ ! -L "$run" ] || fail 'run state must not be a symlink'
if [ -e "$run" ]; then
  [ -d "$run" ] || fail 'run state must be a directory'
fi
scratch=$(mktemp -d "$root/.ralphex/.scope.XXXXXX")
had_previous=false
pending=false
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$pending" = true ]; then
    rm -rf "$run" || { printf 'ralphex-scope: failed to remove rejected state\n' >&2; exit 1; }
    if [ "$had_previous" = true ]; then
      mv "$scratch/previous" "$run" || {
        printf 'ralphex-scope: restore failed; previous state remains in %s/previous\n' "$scratch" >&2
        exit 1
      }
    fi
  fi
  rm -rf "$scratch"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if [ -d "$run" ]; then
  cp -R "$run" "$scratch/previous"
  had_previous=true
fi
# The command accepts whitespace-separated paths; disable glob expansion.
set -f
printf '%s\n' "$RALPHEX_REPOS" | awk '{ for (i = 1; i <= NF; i++) print $i }' > "$scratch/repos"
[ -s "$scratch/repos" ] || fail 'repos must contain at least one path (use . for root only)'
printf '%s\n' "$RALPHEX_BASE" > "$scratch/base-ref"
printf '%s\n' "$RALPHEX_BRANCH" > "$scratch/task-branch"
pending=true
mkdir -p "$run"
for setting in repos base-ref task-branch; do
  mv "$scratch/$setting" "$run/$setting"
done
.ralphex/scripts/ws-git ws-check
pending=false

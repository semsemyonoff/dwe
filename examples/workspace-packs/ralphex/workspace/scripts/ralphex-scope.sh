#!/bin/sh
# Keep the previous run state until the wrapper has validated the new scope.
set -eu

fail() {
  printf 'ralphex-scope: %s\n' "$*" >&2
  exit 1
}

root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)
cd "$root"
[ "$#" -eq 0 ] || fail 'use dwe cmd ralphex.scope --set repos=... --set base=... --set branch=... [--set prepare=true] [--set repin=true] [--set plan=...]'
[ -n "${RALPHEX_REPOS:-}" ] || fail 'repos is required'
[ -n "${RALPHEX_BASE:-}" ] || fail 'base is required'
[ -n "${RALPHEX_BRANCH:-}" ] || fail 'branch is required'
case "$RALPHEX_BASE$RALPHEX_BRANCH" in
  *'
'*) fail 'base and branch must each contain one nonempty line' ;;
esac
# Case variants name the same loose ref on case-insensitive filesystems.
base_lower=$(printf '%s\n' "$RALPHEX_BASE" | tr '[:upper:]' '[:lower:]')
branch_lower=$(printf '%s\n' "$RALPHEX_BRANCH" | tr '[:upper:]' '[:lower:]')
[ "$base_lower" != "$branch_lower" ] || fail 'base and branch must differ'
prepare=${RALPHEX_PREPARE:-false}
case "$prepare" in true|false) ;; *) fail 'RALPHEX_PREPARE must be true or false' ;; esac
repin=${RALPHEX_REPIN:-false}
case "$repin" in true|false) ;; *) fail 'RALPHEX_REPIN must be true or false' ;; esac
plan=${RALPHEX_PLAN:-}
if [ -n "$plan" ]; then
  case "$plan" in
    *' '*|*'	'*|*'
'*) fail "plan path must not contain whitespace (ralphex cannot handle whitespace in plan paths): $plan" ;;
    /*) fail "plan must be relative to the workspace root: $plan" ;;
    # The printed launch command passes the plan positionally to ralphex.
    -*) fail "plan must not start with -: $plan" ;;
  esac
  [ -f "$plan" ] || fail "plan is not a regular file: $plan"
fi
git check-ignore -q -- .ralphex/run/repos ||
  fail 'run state must be ignored: add /.ralphex/run/ and remove an older /.ralphex/ rule from the root .gitignore'
[ -d .ralphex ] && [ ! -L .ralphex ] || fail 'render the ralphex workspace pack first'
[ -x .ralphex/scripts/ws-git ] || fail 'render the ralphex workspace pack first'
run=.ralphex/run
[ ! -L "$run" ] || fail 'run state must not be a symlink'
if [ -e "$run" ]; then
  [ -d "$run" ] || fail 'run state must be a directory'
fi
for setting in repos base-ref task-branch base-sha; do
  dest=$run/$setting
  [ ! -L "$dest" ] || fail "run state setting must be an ordinary file: $dest"
  if [ -e "$dest" ]; then
    [ -f "$dest" ] || fail "run state setting must be an ordinary file: $dest"
  fi
done
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
# The pin is rewritten below; a stale one must not survive a failed step.
rm -f "$run/base-sha"
for setting in repos base-ref task-branch; do
  mv "$scratch/$setting" "$run/$setting"
done
# The same base name (case variants name one ref on APFS) keeps its old pins:
# ws-git refuses a moved base in ws-prepare, before any tag or branch is written,
# and again in ws-pin. A moved base needs an explicit repin.
unset WS_GIT_PREVIOUS_PINS
if [ "$had_previous" = true ] && [ "$repin" = false ] &&
  [ -f "$scratch/previous/base-sha" ] && [ ! -L "$scratch/previous/base-sha" ] &&
  [ -f "$scratch/previous/base-ref" ] && [ ! -L "$scratch/previous/base-ref" ]; then
  previous_base=
  IFS= read -r previous_base < "$scratch/previous/base-ref" || [ -n "$previous_base" ] || previous_base=
  previous_base=$(printf '%s\n' "$previous_base" | tr '[:upper:]' '[:lower:]')
  if [ "$previous_base" = "$base_lower" ]; then
    WS_GIT_PREVIOUS_PINS=$scratch/previous/base-sha
    export WS_GIT_PREVIOUS_PINS
  fi
fi
if [ "$prepare" = true ]; then
  .ralphex/scripts/ws-git ws-prepare
fi
# Pin after ws-prepare, which may have created the base tag.
.ralphex/scripts/ws-git ws-pin > "$scratch/base-sha"
unset WS_GIT_PREVIOUS_PINS
mv "$scratch/base-sha" "$run/base-sha"
.ralphex/scripts/ws-git ws-check
pending=false

shell_quote() {
  case "$1" in
    ''|*[!ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_./:=@%+-]*) ;;
    *)
      printf '%s' "$1"
      return
      ;;
  esac
  rest=$1
  quoted=
  while :; do
    case "$rest" in
      *"'"*)
        quoted=$quoted${rest%%"'"*}"'\\''"
        rest=${rest#*"'"}
        ;;
      *)
        quoted=$quoted$rest
        break
        ;;
    esac
  done
  printf "'%s'" "$quoted"
}

if [ -n "$plan" ]; then
  plan_arg=$(shell_quote "$plan")
else
  plan_arg='<plan>'
fi
printf 'Launch ralphex with:\n'
printf 'ralphex --base-ref %s --branch %s %s\n' "$(shell_quote "$RALPHEX_BASE")" "$(shell_quote "$RALPHEX_BRANCH")" "$plan_arg"

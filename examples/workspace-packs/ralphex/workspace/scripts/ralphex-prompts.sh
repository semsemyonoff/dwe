#!/bin/sh
# Preserve the installed defaults' body and signal rules; insertions come first.
set -eu

fail() {
  printf 'ralphex-prompts: %s\n' "$*" >&2
  exit 1
}

root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)
cd "$root"
[ "$#" -eq 0 ] || fail 'use dwe cmd ralphex.prompts --set check=true for check mode'
check=${RALPHEX_CHECK:-false}
case "$check" in true|false) ;; *) fail 'RALPHEX_CHECK must be true or false' ;; esac
scratch=$(mktemp -d "${TMPDIR:-/tmp}/ralphex-prompts.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
trap 'exit 1' HUP INT TERM
dump=$scratch/defaults
output=$scratch/output
mkdir -p "$dump" "$output/prompts" "$output/agents"
ralphex --dump-defaults="$dump"
version=$(ralphex --version)
if command -v sha256sum >/dev/null 2>&1; then
  hash_program=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  hash_program=shasum
else
  fail 'sha256sum or shasum is required for the defaults stamp'
fi
sha256() {
  if [ "$hash_program" = sha256sum ]; then
    sha256sum < "$1" > "$scratch/hash"
  else
    shasum -a 256 < "$1" > "$scratch/hash"
  fi
  awk '{print $1}' "$scratch/hash"
}
# Hash all dumped defaults (including config and prompts we do not override),
# with relative filenames so two temporary dump directories produce one stamp.
(cd "$dump" && find . -type f -print) > "$scratch/files"
LC_ALL=C sort "$scratch/files" > "$scratch/sorted-files"
while IFS= read -r file; do
  digest=$(sha256 "$dump/$file")
  printf '%s %s\n' "$file" "$digest"
done < "$scratch/sorted-files" > "$scratch/hashes"
digest=$(sha256 "$scratch/hashes")
printf 'version: %s\ndefaults-sha256: %s\n' "$version" "$digest" > "$output/defaults.stamp"

append_fragment() {
  [ -f "$1" ] || fail "missing fragment: $1 (run dwe render workspace first)"
  first=$(sed -n '1p' "$1")
  case "$first" in \#*) fail "fragment must not start with #: $1" ;; esac
  cat "$1" >> "$scratch/fragments"
  printf '\n\n' >> "$scratch/fragments"
}

prepare() {
  kind=$1
  name=$2
  phase=$3
  source=$dump/$kind/$name.txt
  [ -s "$source" ] || fail "missing default: $kind/$name.txt"
  : > "$scratch/fragments"
  append_fragment ".ralphex/blocks/$phase.md"
  if [ -e ".ralphex/policy/$phase.md" ]; then
    append_fragment ".ralphex/policy/$phase.md"
  fi
  if [ "$name" != "$phase" ] && [ -e ".ralphex/policy/$name.md" ]; then
    append_fragment ".ralphex/policy/$name.md"
  fi
  # Ralphex strips leading prompt comments and reads agent frontmatter before
  # the body. Keep those preambles in place, ahead of every inserted fragment.
  awk -v kind="$kind" '
    FILENAME == ARGV[1] { fragments = fragments $0 "\n"; next }
    { lines[++n] = $0 }
    END {
      start = 1
      while (start <= n && lines[start] ~ /^#/) start++
      while (start <= n && lines[start] == "") start++
      if (kind == "agents" && lines[start] == "---") {
        start++
        while (start <= n && lines[start] != "---") start++
        if (start > n) { print "ralphex-prompts: unclosed agent frontmatter" > "/dev/stderr"; exit 1 }
        start++
        while (start <= n && lines[start] == "") start++
      }
      for (i = 1; i < start; i++) print lines[i]
      if (start == 1 || lines[start-1] != "") print ""
      printf "%s", fragments
      for (i = start; i <= n; i++) print lines[i]
    }
  ' "$scratch/fragments" "$source" > "$output/$kind/$name.txt"
}

for name in task review_first review_second codex codex_review; do
  case "$name" in
    task) phase=task ;;
    codex_review) phase=codex_review ;;
    *) phase=review ;;
  esac
  prepare prompts "$name" "$phase"
done
for name in documentation implementation quality simplification testing; do
  prepare agents "$name" agent
done

# Unowned files remain in place; they may silently shadow newer defaults or
# be custom agents without the workspace scope block.
for kind in prompts agents; do
  if [ -d ".ralphex/$kind" ]; then
    find ".ralphex/$kind" \( -type f -o -type l \) -print > "$scratch/installed-files"
    while IFS= read -r file; do
      relative=${file#.ralphex/}
      if [ ! -f "$output/$relative" ]; then
        printf 'ralphex-prompts: warning: unowned override: %s\n' "$file" >&2
      fi
    done < "$scratch/installed-files"
  fi
done

if [ "$check" = true ]; then
  drift=false
  for kind in prompts agents; do
    for file in "$output/$kind/"*.txt; do
      dest=.ralphex/$kind/${file##*/}
      if [ ! -f "$dest" ]; then
        printf 'ralphex-prompts: missing override: %s\n' "$dest" >&2
        drift=true
      elif diff -u "$dest" "$file"; then
        :
      else
        status=$?
        [ "$status" -eq 1 ] || fail "cannot compare $dest"
        drift=true
      fi
    done
  done
  if [ ! -f .ralphex/defaults.stamp ] || ! cmp -s .ralphex/defaults.stamp "$output/defaults.stamp"; then
    printf 'ralphex-prompts: defaults stamp drift (ralphex version or dumped defaults changed)\n' >&2
    drift=true
  fi
  [ "$drift" = false ] || exit 1
else
  mkdir -p .ralphex/prompts .ralphex/agents
  for kind in prompts agents; do
    for file in "$output/$kind/"*.txt; do
      cp "$file" ".ralphex/$kind/${file##*/}"
    done
  done
  cp "$output/defaults.stamp" .ralphex/defaults.stamp
fi

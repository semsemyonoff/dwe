#!/usr/bin/env bash
# Throwaway helper for the internal/ restructure refactor.
# Performs mass sed substitutions on Go import paths in correct prefix-order
# (longest/most-specific path first to avoid prefix-rewrite collisions).
#
# Idempotent: safe to re-run; harmless to run substitutions for not-yet-moved
# packages (sed no-ops when the source string is absent).
#
# Usage: scripts/restructure-imports.sh

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Substitution table: longer/more-specific paths FIRST.
# Format: "OLD|NEW"
SUBS=(
  # statusview / statustui must precede internal/command rename
  "devbox-cli/internal/command/statusview|devbox-cli/internal/core/ui/statusview"
  "devbox-cli/internal/command/statustui|devbox-cli/internal/core/ui/statustui"

  # docs/tui precedes docs (currently same level but kept explicit for clarity)
  "devbox-cli/internal/docs|devbox-cli/internal/core/docs"

  # deploy/journal precedes deploy
  "devbox-cli/internal/deploy/journal|devbox-cli/internal/core/workflow/deploy/journal"

  # command rename (after statusview/statustui already redirected)
  "devbox-cli/internal/command|devbox-cli/internal/cli"

  # core/project cluster
  "devbox-cli/internal/project|devbox-cli/internal/core/project/project"
  "devbox-cli/internal/config|devbox-cli/internal/core/project/config"
  "devbox-cli/internal/services|devbox-cli/internal/core/project/services"
  "devbox-cli/internal/stack|devbox-cli/internal/core/project/stack"

  # core/execution cluster
  "devbox-cli/internal/pipeline|devbox-cli/internal/core/execution/pipeline"
  "devbox-cli/internal/condition|devbox-cli/internal/core/execution/condition"
  "devbox-cli/internal/filesgate|devbox-cli/internal/core/execution/filesgate"
  "devbox-cli/internal/builtin|devbox-cli/internal/core/execution/builtin"
  "devbox-cli/internal/templates|devbox-cli/internal/core/execution/templates"
  "devbox-cli/internal/preflight|devbox-cli/internal/core/execution/preflight"

  # core/workflow cluster
  "devbox-cli/internal/deploy|devbox-cli/internal/core/workflow/deploy"
  "devbox-cli/internal/lifecycle|devbox-cli/internal/core/workflow/lifecycle"
  "devbox-cli/internal/reset|devbox-cli/internal/core/workflow/reset"
  "devbox-cli/internal/snapshot|devbox-cli/internal/core/workflow/snapshot"
  "devbox-cli/internal/setup|devbox-cli/internal/core/workflow/setup"

  # core (top-level subtrees)
  "devbox-cli/internal/usercommands|devbox-cli/internal/core/usercommands"
  "devbox-cli/internal/validate|devbox-cli/internal/core/validate"
  "devbox-cli/internal/ui|devbox-cli/internal/core/ui"
  "devbox-cli/internal/notify|devbox-cli/internal/core/notify"

  # shared (leaf infra)
  "devbox-cli/internal/docker|devbox-cli/internal/shared/docker"
  "devbox-cli/internal/git|devbox-cli/internal/shared/git"
  "devbox-cli/internal/daemon|devbox-cli/internal/shared/daemon"
  "devbox-cli/internal/lock|devbox-cli/internal/shared/lock"
  "devbox-cli/internal/pathsafe|devbox-cli/internal/shared/pathsafe"
  "devbox-cli/internal/envfile|devbox-cli/internal/shared/envfile"
  "devbox-cli/internal/render|devbox-cli/internal/shared/render"
  "devbox-cli/internal/liveui|devbox-cli/internal/shared/liveui"
  "devbox-cli/internal/tpl|devbox-cli/internal/shared/tpl"
  "devbox-cli/internal/i18n|devbox-cli/internal/shared/i18n"
  "devbox-cli/internal/version|devbox-cli/internal/shared/version"
  "devbox-cli/internal/prompt|devbox-cli/internal/shared/prompt"
)

# Build a single sed script with all substitutions.
SED_EXPR=""
for sub in "${SUBS[@]}"; do
  old="${sub%%|*}"
  new="${sub##*|}"
  # Escape forward slashes for sed s|||
  SED_EXPR+="s|${old}|${new}|g;"
done

# Apply to all .go files tracked by git.
files=$(git ls-files '*.go')
if [ -n "$files" ]; then
  # macOS sed needs -i ''; GNU sed accepts -i.
  if sed --version >/dev/null 2>&1; then
    echo "$files" | xargs sed -i -e "$SED_EXPR"
  else
    echo "$files" | xargs sed -i '' -e "$SED_EXPR"
  fi
fi

# Tidy imports.
if command -v goimports >/dev/null 2>&1; then
  goimports -w .
fi

echo "restructure-imports.sh: done"

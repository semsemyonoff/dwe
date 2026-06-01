#!/bin/bash
# Regenerate shell-completion scripts for bash, zsh, and fish into completions/.
# These files are bundled into release archives and packages so that distros
# install them to the correct system path automatically (no `dwe completion
# install` step needed for packaged installs).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="$REPO_ROOT/completions"

mkdir -p "$OUT_DIR"

cd "$REPO_ROOT"

# go run is used (not the built binary) so this works in CI from a clean
# checkout without needing `make build` first. Embedded docs must already be
# synced — the Makefile / goreleaser before-hooks sequence ensures that.
go run ./cmd/dwe completion bash > "$OUT_DIR/dwe.bash"
go run ./cmd/dwe completion zsh  > "$OUT_DIR/dwe.zsh"
go run ./cmd/dwe completion fish > "$OUT_DIR/dwe.fish"

echo "Generated completions in $OUT_DIR"

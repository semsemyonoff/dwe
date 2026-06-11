#!/bin/bash
# Cross-compile the bridge shim (cmd/dwe-shim) for the container platforms and
# place the binaries under internal/core/bridge/shimassets/bin/ for go:embed.
# Idempotent: the go build cache makes unchanged rebuilds near-instant.
#
# The bin/ tree is gitignored (mirrors internal/core/docs/embedded/); the
# committed bin/.gitkeep keeps the `//go:embed all:bin` pattern matching on a
# fresh checkout so the module always compiles.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="$REPO_ROOT/internal/core/bridge/shimassets/bin"

mkdir -p "$OUT_DIR"

for arch in amd64 arm64; do
  (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w" \
    -o "$OUT_DIR/shim-linux-$arch" ./cmd/dwe-shim)
done

echo "Built shims in $OUT_DIR"

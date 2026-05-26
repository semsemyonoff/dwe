#!/bin/bash
# Sync docs from the repo root (docs/reference, docs/internals, docs/i18n) into internal/docs/embedded/
# Idempotent: runs safely multiple times with no side effects on unchanged inputs.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DOCS_ROOT="$REPO_ROOT/docs"
EMBEDDED_DIR="$REPO_ROOT/internal/docs/embedded"

# Source trees to sync
SOURCES=(
	"$DOCS_ROOT/reference"
	"$DOCS_ROOT/internals"
	"$DOCS_ROOT/i18n"
)

mkdir -p "$EMBEDDED_DIR"

# Use rsync if available (faster, handles deletions cleanly)
if command -v rsync >/dev/null 2>&1; then
	for src in "${SOURCES[@]}"; do
		if [ -d "$src" ]; then
			rsync -a --delete "$src/" "$EMBEDDED_DIR/$(basename "$src")/"
		fi
	done
else
	# Fallback: rm + cp for systems without rsync
	for src in "${SOURCES[@]}"; do
		if [ -d "$src" ]; then
			name=$(basename "$src")
			rm -rf "$EMBEDDED_DIR/$name"
			cp -R "$src" "$EMBEDDED_DIR/$name"
		fi
	done
fi

echo "Synced docs to $EMBEDDED_DIR"

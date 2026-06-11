BINARY_NAME := dwe
BIN_DIR     := ./bin
MODULE      := github.com/semsemyonoff/dwe

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X $(MODULE)/internal/shared/version.Version=$(VERSION) \
            -X $(MODULE)/internal/shared/version.Commit=$(COMMIT) \
            -X $(MODULE)/internal/shared/version.Date=$(DATE) \
            -X $(MODULE)/internal/shared/version.BuiltBy=make

.PHONY: build test test-v test-race clean tidy lint embedded-docs gen-docs-manifest \
        shims completions release-check snapshot release

embedded-docs:
	@./scripts/sync-embedded-docs.sh

# Cross-compile the bridge shim (cmd/dwe-shim, linux amd64+arm64) into the
# gitignored internal/core/bridge/shimassets/bin/ embed tree. A prerequisite
# of build and EVERY test target: it adds two cross-compiles per run (cached
# by the go build cache — unchanged rebuilds are near-instant), and it is
# load-bearing for module compilation/testing — the committed bin/.gitkeep
# only keeps the `//go:embed all:bin` pattern matching on a fresh checkout;
# without this target the embedded tree holds no real shim payloads, so the
# built dwe could not materialize shims into .dwe/bridge.
shims:
	@./scripts/build-shims.sh

gen-docs-manifest:
	@./scripts/gen-docs-content-hashes.sh

completions: embedded-docs gen-docs-manifest
	@./scripts/gen-completions.sh

build: tidy embedded-docs gen-docs-manifest shims
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/dwe
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)"

# Release pipeline (GoReleaser). The before-hooks in .goreleaser.yaml regenerate
# embedded docs, the content-hash table, and shell completions, so these
# targets do not depend on the local build chain.
GORELEASER ?= goreleaser

release-check:
	@$(GORELEASER) check

# Local dev build of all release artifacts (no git tag required, no publishing).
# Writes to ./dist/.
snapshot:
	@$(GORELEASER) release --snapshot --clean --skip=publish

# Full release. Requires:
#   - a pushed git tag (vX.Y.Z) at HEAD
#   - GITHUB_TOKEN with repo scope for the source repo
#   - HOMEBREW_TAP_GITHUB_TOKEN with repo scope for semsemyonoff/homebrew-tap
# Intended to be invoked by .github/workflows/release.yml; works locally too.
release:
	@$(GORELEASER) release --clean

test: embedded-docs shims
	go test ./...

test-v: embedded-docs shims
	go test -v ./...

test-race: embedded-docs shims
	go test -race ./internal/core/workflow/deploy/journal ./internal/shared/lock ./internal/core/execution/pipeline

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -f $(BIN_DIR)/$(BINARY_NAME)
	rm -rf dist completions

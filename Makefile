BINARY_NAME := devbox
BIN_DIR     := ./bin
MODULE      := github.com/semsemyonoff/devbox

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X $(MODULE)/internal/shared/version.Version=$(VERSION) \
            -X $(MODULE)/internal/shared/version.Commit=$(COMMIT) \
            -X $(MODULE)/internal/shared/version.Date=$(DATE) \
            -X $(MODULE)/internal/shared/version.BuiltBy=make

.PHONY: build test test-v test-race clean tidy lint embedded-docs gen-docs-manifest \
        completions release-check snapshot release

embedded-docs:
	@./scripts/sync-embedded-docs.sh

gen-docs-manifest:
	@./scripts/gen-docs-content-hashes.sh

completions: embedded-docs gen-docs-manifest
	@./scripts/gen-completions.sh

build: tidy embedded-docs gen-docs-manifest
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/devbox
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

test: embedded-docs
	go test ./...

test-v: embedded-docs
	go test -v ./...

test-race: embedded-docs
	go test -race ./internal/core/workflow/deploy/journal ./internal/shared/lock ./internal/core/execution/pipeline

lint:
	@which golangci-lint > /dev/null 2>&1 || \
		(curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin)
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -f $(BIN_DIR)/$(BINARY_NAME)
	rm -rf dist completions

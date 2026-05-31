BINARY_NAME := devbox
BIN_DIR     := ./bin
MODULE      := github.com/semsemyonoff/devbox

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X github.com/semsemyonoff/devbox/internal/shared/version.Version=$(VERSION) \
            -X github.com/semsemyonoff/devbox/internal/shared/version.Commit=$(COMMIT) \
            -X github.com/semsemyonoff/devbox/internal/shared/version.Date=$(DATE) \
            -X github.com/semsemyonoff/devbox/internal/shared/version.BuiltBy=make

.PHONY: build test test-v test-race clean tidy lint embedded-docs gen-docs-manifest

embedded-docs:
	@./scripts/sync-embedded-docs.sh

gen-docs-manifest:
	@./scripts/gen-docs-content-hashes.sh

build: tidy embedded-docs gen-docs-manifest
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/devbox
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)"

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

BINARY_NAME := devbox
BIN_DIR     := ./bin
MODULE      := devbox-cli

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X devbox-cli/internal/version.Version=$(VERSION) \
            -X devbox-cli/internal/version.Commit=$(COMMIT) \
            -X devbox-cli/internal/version.Date=$(DATE) \
            -X devbox-cli/internal/version.BuiltBy=make

.PHONY: build test test-v test-race clean tidy lint

build: tidy
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/devbox
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)"

test:
	go test ./...

test-v:
	go test -v ./...

test-race:
	go test -race ./internal/deploy/journal ./internal/lock ./internal/pipeline

lint:
	@which golangci-lint > /dev/null 2>&1 || \
		(curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin)
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -f $(BIN_DIR)/$(BINARY_NAME)

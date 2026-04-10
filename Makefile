BINARY_NAME := devbox
BIN_DIR     := ../bin
MODULE      := devbox-cli

.PHONY: build test test-v clean tidy lint

build: tidy
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/devbox
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)"

test:
	go test ./...

test-v:
	go test -v ./...

lint:
	@which golangci-lint > /dev/null 2>&1 || \
		(curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin)
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -f $(BIN_DIR)/$(BINARY_NAME)

BINARY  := weft
BIN_DIR := ./bin
GO      := go
AIR     := $(shell go env GOPATH)/bin/air

.PHONY: build run dev test lint clean install

build:
	$(GO) build -o $(BIN_DIR)/$(BINARY) .

run:
	$(GO) run . $(ARGS)

dev:
	@if ! command -v air > /dev/null 2>&1 && [ ! -f "$(AIR)" ]; then \
		echo "Installing air..."; \
		go install github.com/air-verse/air@latest; \
	fi
	air -- $(ARGS)

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN_DIR)

install:
	$(GO) install .

# Rename the binary when product name is decided:
# make BINARY=<product-name> build

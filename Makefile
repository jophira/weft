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
	@_ARGS="$(ARGS)"; \
	if [ -z "$$_ARGS" ]; then \
		_PROFILE=$$(grep '^active_profile:' ~/.config/weft/config.yaml 2>/dev/null | awk '{print $$2}'); \
		if [ -z "$$_PROFILE" ]; then \
			echo "error: no active profile set — run 'weft profile use <name>' first"; \
			echo "  or: make dev ARGS=\"profile use <name>\""; \
			exit 1; \
		fi; \
		echo "[dev] using active profile: $$_PROFILE"; \
		_ARGS="profile use $$_PROFILE"; \
	fi; \
	air -- $$_ARGS

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

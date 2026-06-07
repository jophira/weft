BINARY  := weft
BIN_DIR := ./bin
GO      := go
AIR     := $(shell go env GOPATH)/bin/air

# Coverage target: internal packages only.
# cmd/ is excluded — Cobra RunE wiring is validated by integration tests, not unit tests.
COVERAGE_PKGS := ./internal/...

.PHONY: build run dev test test-integration coverage lint clean install

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

# Requires Docker. Spins up Gitea for git remote tests and exercises the MCP
# JSON-RPC protocol loop end-to-end.
test-integration:
	$(GO) test -tags integration ./...

# Unit coverage measured over internal packages only (cmd/ excluded — integration only).
# Run all package tests so cmd regressions are still caught, but only instrument internal/.
coverage:
	$(GO) test -coverprofile=coverage.out -covermode=atomic -coverpkg=$(COVERAGE_PKGS) ./...
	$(GO) tool cover -func=coverage.out | grep -E "^total|\.go" | tail -20
	@echo ""
	$(GO) tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN_DIR)

install:
	$(GO) install .

# Rename the binary when product name is decided:
# make BINARY=<product-name> build

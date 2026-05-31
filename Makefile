BINARY  := weft
BIN_DIR := ./bin
GO      := go

.PHONY: build run test lint clean install

build:
	$(GO) build -o $(BIN_DIR)/$(BINARY) .

run:
	$(GO) run . $(ARGS)

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

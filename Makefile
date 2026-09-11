BIN := flok
PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/w4jnl/flok/internal/cli.Version=$(VERSION)

.PHONY: build install test vet run-status

build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/$(BIN)

install: build
	mkdir -p $(PREFIX)/bin
	install -m 755 bin/$(BIN) $(PREFIX)/bin/$(BIN)

test:
	go test ./...

vet:
	go vet ./...

run-status: build
	./bin/$(BIN) status

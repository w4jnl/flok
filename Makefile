BIN := flok
PREFIX ?= $(HOME)/.local

.PHONY: build install test vet run-status

build:
	go build -o bin/$(BIN) ./cmd/$(BIN)

install: build
	mkdir -p $(PREFIX)/bin
	install -m 755 bin/$(BIN) $(PREFIX)/bin/$(BIN)

test:
	go test ./...

vet:
	go vet ./...

run-status: build
	./bin/$(BIN) status

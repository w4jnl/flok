BIN := flok
PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/w4jnl/flok/internal/cli.Version=$(VERSION)

UNAME := $(shell uname -s)

.PHONY: build build-bar bundle-bar icons install test vet run-status

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/$(BIN)
ifeq ($(UNAME),Darwin)
	$(MAKE) build-bar
endif

# The menu bar companion is the only cgo binary (fyne.io/systray), macOS only.
build-bar:
	CGO_ENABLED=1 go build -ldflags '$(LDFLAGS) -X main.version=$(VERSION)' -o bin/$(BIN)-bar ./cmd/$(BIN)-bar

# Optional .app wrapper for the bar (LSUIElement, ad-hoc signed); `flok up` runs the bare binary.
bundle-bar: build-bar
	rm -rf dist/$(BIN)-bar.app
	mkdir -p dist/$(BIN)-bar.app/Contents/MacOS
	cp assets/$(BIN)-bar-Info.plist dist/$(BIN)-bar.app/Contents/Info.plist
	cp bin/$(BIN)-bar dist/$(BIN)-bar.app/Contents/MacOS/
	codesign --force --sign - dist/$(BIN)-bar.app

icons:
	go run ./assets/icons/gen assets/icons

install: build
	mkdir -p $(PREFIX)/bin
	install -m 755 bin/$(BIN) $(PREFIX)/bin/$(BIN)
	@[ -f bin/$(BIN)-bar ] && install -m 755 bin/$(BIN)-bar $(PREFIX)/bin/$(BIN)-bar || true

test:
	go test ./...

vet:
	go vet ./...

run-status: build
	./bin/$(BIN) status

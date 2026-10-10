BIN := flok
PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/w4jnl/flok/internal/cli.Version=$(VERSION)

UNAME := $(shell uname -s)

.PHONY: build build-bar bundle-bar relay relay-image icons install test vet e2e run-status

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/$(BIN)
ifeq ($(UNAME),Darwin)
	$(MAKE) build-bar
endif

# flok-relay: the service between flok instances and the phone (cgo-free, runs anywhere).
relay:
	CGO_ENABLED=0 go build -ldflags '-s -w -X main.version=$(VERSION)' -o bin/$(BIN)-relay ./cmd/$(BIN)-relay

# A local image of the relay for this machine's architecture (the release publishes
# ghcr.io/w4jnl/flok-relay for amd64 and arm64).
relay-image:
	docker build -f deploy/relay/Dockerfile -t $(BIN)-relay:$(VERSION) .

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

# End-to-end suites on isolated tmux servers, in order, each under a watchdog, with a table at
# the end (scripts/e2e/run-all.sh); `make e2e SUITES="m4 m7"` for a subset. Never two at once.
e2e:
	scripts/e2e/run-all.sh $(SUITES)

vet:
	go vet ./...

run-status: build
	./bin/$(BIN) status

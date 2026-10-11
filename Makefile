BIN := flok
PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/w4jnl/flok/internal/cli.Version=$(VERSION)

UNAME := $(shell uname -s)

.PHONY: build build-bar bundle-bar relay relay-image ios-project ios-build ios-test ios-device icons install test vet e2e run-status

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

# flok for iPhone (app/ios): the Xcode project is generated (brew install xcodegen); the
# simulator build and tests need no signing.
IOS_SIM ?= platform=iOS Simulator,name=iPhone 15
ios-project:
	@[ -f app/ios/Local.xcconfig ] || printf 'FLOK_TEAM_ID =\nFLOK_ENTITLEMENTS = Flok/Flok.entitlements\n' > app/ios/Local.xcconfig
	cd app/ios && xcodegen generate
ios-build: ios-project
	cd app/ios && xcodebuild -project Flok.xcodeproj -scheme Flok -configuration Debug -destination '$(IOS_SIM)' -derivedDataPath build/DerivedData build CODE_SIGNING_ALLOWED=NO -quiet
ios-test: ios-project
	cd app/ios && xcodebuild -project Flok.xcodeproj -scheme Flok -configuration Debug -destination '$(IOS_SIM)' -derivedDataPath build/DerivedData test CODE_SIGNING_ALLOWED=NO -quiet
# Build, install and launch on the iPhone plugged in (unlocked); signs with Local.xcconfig's team.
ios-device: ios-project
	@udid=$$(xcrun devicectl list devices 2>/dev/null | grep -v simulated | grep -E ' connected ' | grep -oE '[0-9A-F]{8}-[0-9A-F]{16}' | head -1); \
	[ -n "$$udid" ] || { echo "ios-device: no iPhone connected and unlocked (a locked phone shows as unavailable)"; exit 1; }; \
	cd app/ios && xcodebuild -project Flok.xcodeproj -scheme Flok -configuration Debug -destination "id=$$udid" -derivedDataPath build/DerivedData build -quiet && \
	xcrun devicectl device install app --device "$$udid" build/DerivedData/Build/Products/Debug-iphoneos/Flok.app >/dev/null && \
	xcrun devicectl device process launch --terminate-existing --device "$$udid" nl.w4j.flok >/dev/null && echo "ios-device: installed and launched on $$udid"

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

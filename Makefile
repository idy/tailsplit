GO ?= go
PYTHON ?= python3
TAGS ?= with_gvisor
OUT ?= bin
VERSION ?= $(shell cat VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
LDFLAGS ?= -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build build-tray run-tray build-mygo run-mygo app dmg dist test check deb

build:
	$(GO) build -tags $(TAGS) -trimpath -ldflags "$(LDFLAGS)" -o "$(OUT)/tailsplit" ./cmd/tailsplit

build-tray:
	$(GO) build -tags $(TAGS) -trimpath -o "$(OUT)/tailsplit-tray" ./cmd/tailsplit-tray

# macOS source launch; Windows desktop builds use the release packager.
run-tray: build build-tray
	"$(OUT)/tailsplit-tray" --template "$(abspath config.default.yaml)" --helper "$(abspath $(OUT))/tailsplit" --open --no-connect

# Compatibility aliases for the production desktop targets.
build-mygo: build build-tray

run-mygo: run-tray

app:
	GO="$(GO)" PYTHON="$(PYTHON)" sh scripts/package-macos.sh "$(OUT)"

dmg: app

dist:
	GO="$(GO)" PYTHON="$(PYTHON)" SIGNING_MODE=developer-id NOTARIZE=1 sh scripts/package-macos.sh "$(OUT)"

test:
	$(GO) test -tags $(TAGS) -race ./...
	$(GO) vet -tags $(TAGS) ./...
	$(PYTHON) -m unittest discover -s scripts -p 'test_*.py'

check: build
	"$(OUT)/tailsplit" check -c config.example.yaml

deb:
	GO="$(GO)" PYTHON="$(PYTHON)" bash packaging/build-deb.sh

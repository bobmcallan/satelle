BIN         := satelle
PREFIX      ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

# Build identity baked into every binary via -ldflags, from the SINGLE canonical
# source (.version) plus the live git SHA and a build-time-GENERATED timestamp — so
# a local `make build` reports a real, non-empty version/commit/time, not the bare
# "dev" sentinel (sty_27077b11). The release CI bakes the same three vars.
PKG         := github.com/bobmcallan/satelle/internal/buildinfo
VERSION     := $(shell awk '$$1=="satelle.version:" {print $$2}' .version)
COMMIT      := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo none)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)

.PHONY: build install uninstall test integration

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/satelle

# install places the binary on PATH (~/.local/bin by default). Afterwards, run
# `satelle service install` inside a repo to start the always-on web service.
install: build
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	@echo "installed $(INSTALL_DIR)/$(BIN)"
	@echo "next: cd <repo> && satelle init && satelle service install"

uninstall:
	rm -f $(INSTALL_DIR)/$(BIN)
	@echo "removed $(INSTALL_DIR)/$(BIN) (run 'satelle service uninstall' first if the service is installed)"

test:
	go test ./...

# integration builds the binary once, then drives it from ./tests via SATELLE_BIN
# (no per-test rebuild). Run by hand with: SATELLE_BIN=$(command -v satelle) go test -tags integration ./tests/...
integration: build
	SATELLE_BIN=$(CURDIR)/$(BIN) go test -tags integration ./tests/...

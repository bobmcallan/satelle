BIN         := satelle
SERVE_BIN   := satelle-serve
PREFIX      ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

# Build identity from .version — CLI and serve carry independent version numbers
# (sty_19ff03f4); commit SHA and build stamp are shared (one commit → both artifacts).
PKG         := github.com/bobmcallan/satelle/internal/buildinfo
VERSION     := $(shell awk '$$1=="satelle.version:" {print $$2}' .version)
SERVE_VERSION := $(shell awk '$$1=="satelle-serve.version:" {print $$2}' .version)
COMMIT      := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo none)
BUILD_TIME  := $(shell awk '$$1=="satelle.build:" {print $$2}' .version)
LDFLAGS     := -X $(PKG).Name=satelle -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)
SERVE_LDFLAGS := -X $(PKG).Name=satelle-serve -X $(PKG).Version=$(SERVE_VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)

.PHONY: build install uninstall test integration judgment planner-bench check-serve-version

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/satelle
	go build -ldflags "$(SERVE_LDFLAGS)" -o $(SERVE_BIN) ./cmd/satelle-serve

# Fail closed when serve-path sources changed since the last serve-v* tag but
# satelle-serve.version was not advanced (sty_4a5c6924). Used by release path.
check-serve-version:
	@bash scripts/check-serve-version.sh

# install places both binaries on PATH (~/.local/bin by default). Afterwards, run
# `satelle service install` inside a repo to start the always-on web service.
install: build
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	install -m 0755 $(SERVE_BIN) $(INSTALL_DIR)/$(SERVE_BIN)
	@echo "installed $(INSTALL_DIR)/$(BIN) and $(INSTALL_DIR)/$(SERVE_BIN)"
	@echo "next: cd <repo> && satelle init && satelle service install"

uninstall:
	rm -f $(INSTALL_DIR)/$(BIN) $(INSTALL_DIR)/$(SERVE_BIN)
	@echo "removed $(INSTALL_DIR)/$(BIN) and $(INSTALL_DIR)/$(SERVE_BIN) (run 'satelle service uninstall' first if the service is installed)"

test:
	go test ./...

# integration builds the binary once, then drives it from ./tests via SATELLE_BIN
# (no per-test rebuild). Run by hand with: SATELLE_BIN=$(command -v satelle) go test -tags integration ./tests/...
integration: build
	SATELLE_BIN=$(CURDIR)/$(BIN) go test -tags integration ./tests/...

# judgment: opt-in LLM rubric fixtures (sty_6830e78e). Costs tokens, not hermetic,
# never in default CI. See README ## Testing.
judgment:
	go test -tags llm ./tests/llm/...

# Opt-in live comparison of the configured Claude command planner and Grok ACP
# planner. Costs tokens and writes evidence under tests/plannerbench/out/.
planner-bench: build
	SATELLE_BIN=$(CURDIR)/$(BIN) SATELLE_PLANNER_BENCH=1 go test -tags plannerbench ./tests/plannerbench/ -count=1 -timeout 60m -v

# Local Codex hook smoke (sty_71491143). Never part of CI. It uses the existing
# Codex CLI login/configuration and clearly skips when Codex is unavailable.
.PHONY: codex-smoke
codex-smoke: build
	SATELLE_TEST_BIN=$$(pwd)/$(BIN) go test -tags codexlive ./tests/codexlive/ -count=1 -v

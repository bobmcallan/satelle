BIN         := satelle
SERVE_BIN   := satelled
PREFIX      ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

# Build identity from .version — CLI and serve carry independent version numbers
# (sty_19ff03f4); commit SHA and build stamp are shared (one commit → both artifacts).
# scripts/build-version.sh appends +<sha>[-dirty] when HEAD is not the tagged
# release commit or the tree is dirty, so isDevVersion treats make-built
# unreleased binaries as dev builds (sty_022929ef). CI release.yml stamps its
# own ldflags and never calls this script — published assets stay plain.
PKG         := github.com/bobmcallan/satelle/internal/buildinfo
BASE_VERSION := $(shell awk '$$1=="satelle.version:" {print $$2}' .version)
BASE_SERVE_VERSION := $(shell awk '$$1=="satelled.version:" {print $$2}' .version)
VERSION     := $(shell bash scripts/build-version.sh $(BASE_VERSION))
SERVE_VERSION := $(shell bash scripts/build-version.sh $(BASE_SERVE_VERSION) serve-v)
COMMIT      := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo none)
BUILD_TIME  := $(shell awk '$$1=="satelle.build:" {print $$2}' .version)
LDFLAGS     := -X $(PKG).Name=satelle -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)
SERVE_LDFLAGS := -X $(PKG).Name=satelled -X $(PKG).Version=$(SERVE_VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)

.PHONY: build install uninstall test integration judgment planner-bench check-serve-version

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/satelle
	go build -ldflags "$(SERVE_LDFLAGS)" -o $(SERVE_BIN) ./cmd/satelled

# Fail closed when serve-path sources changed since the last serve-v* tag but
# satelled.version was not advanced (sty_4a5c6924). Run on EVERY release,
# not only slices the author reads as serve-path (sty_a8853e85). The watch set is
# derived from `go list -deps ./cmd/satelled`; print it with
# `bash scripts/check-serve-version.sh --paths`.
check-serve-version:
	@bash scripts/check-serve-version.sh

# install places both binaries on PATH (~/.local/bin by default — machine-wide).
# SCOPE DECISION (sty_022929ef AC4): keep the fleet-wide default. Named failure
# mode: every repo on the host resolves this binary; mitigated (not removed) by
# the honest +sha/-dirty stamp so isDevVersion demotes unreleased installs out
# of gating. PREFIX=… overrides for a repo-local install. Refusing dirty trees
# and defaulting to repo-local were rejected: both push people to unstamped
# `go build -o ~/.local/bin/satelle` with the same fleet blast radius.
# Afterwards, run `satelle service install` inside a repo to start the always-on web service.
install: build
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	install -m 0755 $(SERVE_BIN) $(INSTALL_DIR)/$(SERVE_BIN)
	@echo "installed $(INSTALL_DIR)/$(BIN) and $(INSTALL_DIR)/$(SERVE_BIN) (version $(VERSION) / serve $(SERVE_VERSION))"
	@echo "next: cd <repo> && satelle init && satelle service install"

uninstall:
	rm -f $(INSTALL_DIR)/$(BIN) $(INSTALL_DIR)/$(SERVE_BIN)
	@echo "removed $(INSTALL_DIR)/$(BIN) and $(INSTALL_DIR)/$(SERVE_BIN) (run 'satelle service uninstall' first if the service is installed)"

test:
	go test ./...

# integration builds the binary once, then drives it from ./tests via SATELLE_BIN
# (no per-test rebuild). Run by hand with: SATELLE_BIN=$(command -v satelle) go test -tags integration ./tests/...
# Stamp BASE_VERSION (plain .version), not the +sha/-dirty make-install form: the
# suite asserts release behaviour (deployed.version, drift gates) against the
# version under test, and a dirty working tree must not demote the test binary
# into isDevVersion (sty_022929ef).
integration:
	go build -ldflags "-X $(PKG).Name=satelle -X $(PKG).Version=$(BASE_VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)" -o $(BIN) ./cmd/satelle
	go build -ldflags "-X $(PKG).Name=satelled -X $(PKG).Version=$(BASE_SERVE_VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)" -o $(SERVE_BIN) ./cmd/satelled
	SATELLE_BIN=$(CURDIR)/$(BIN) go test -tags integration ./tests/...

# judgment: opt-in LLM rubric fixtures (sty_6830e78e). Costs tokens, not hermetic,
# never in default CI. See README ## Testing.
judgment:
	go test -tags llm ./tests/llm/...

# Opt-in live planner study: the controlled matrix declared in
# tests/plannerbench/study.json. Costs tokens and writes durable per-sample
# evidence plus report.md under tests/plannerbench/out/. Bindings whose binary is
# not on PATH are skipped with a recorded reason rather than dropped.
planner-bench: build
	SATELLE_BIN=$(CURDIR)/$(BIN) SATELLE_PLANNER_BENCH=1 go test -tags plannerbench ./tests/plannerbench/ -count=1 -timeout 90m -v

# planner-report re-renders report.md/report.json from the durable run records
# already under tests/plannerbench/out/runs. Pure and token-free: same records in,
# same report out. Use it after an interrupted study, or to re-read a study
# without paying for it again.
.PHONY: planner-report
planner-report:
	SATELLE_PLANNER_REPORT=1 go test -tags plannerbench ./tests/plannerbench/ -run TestRegenerateReportFromDurableEvidence -count=1 -v

# Local Codex hook smoke (sty_71491143). Never part of CI. It uses the existing
# Codex CLI login/configuration and clearly skips when Codex is unavailable.
.PHONY: codex-smoke
codex-smoke: build
	SATELLE_TEST_BIN=$$(pwd)/$(BIN) go test -tags codexlive ./tests/codexlive/ -count=1 -v

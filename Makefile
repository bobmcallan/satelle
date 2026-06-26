BIN         := satelle
PREFIX      ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

.PHONY: build install uninstall test integration

build:
	go build -o $(BIN) ./cmd/satelle

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

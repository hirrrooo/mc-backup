BINARY   := mc-backup
GOOS     := linux
GOARCH   := amd64
PREFIX   := /usr/local
BINDIR   := $(PREFIX)/bin
CONFDIR  := /etc/mc-backup
SERVICEDIR := /etc/systemd/system
INSTALL_USER_HOME = $(or $(shell if [ -n "$(SUDO_USER)" ] && [ "$(SUDO_USER)" != "root" ]; then getent passwd "$(SUDO_USER)" | cut -d: -f6; else printf '%s' "$(HOME)"; fi),$(HOME))
USER_CONFDIR = $(INSTALL_USER_HOME)/.config/mc-backup
REPO_URL ?= https://github.com/hirrrooo/mc-backup.git
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
GOLANGCI_LINT_VERSION ?= v1.55.2

.PHONY: build install uninstall clean lint

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags "-X main.repoURL=$(REPO_URL) -X main.version=$(VERSION)" -o $(BINARY) ./cmd/mc-backup

lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

install: build
	install -d "$(DESTDIR)$(BINDIR)" "$(DESTDIR)$(CONFDIR)" "$(USER_CONFDIR)"
	install -m 755 $(BINARY) "$(DESTDIR)$(BINDIR)/$(BINARY)"
	[ -f "$(USER_CONFDIR)/config.toml" ] || install -m 644 config.example.toml "$(USER_CONFDIR)/config.toml"
	[ -f "$(DESTDIR)$(CONFDIR)/config.toml" ] || install -m 644 config.example.toml "$(DESTDIR)$(CONFDIR)/config.toml"
	install -m 644 mc-backup.service "$(DESTDIR)$(SERVICEDIR)/mc-backup.service"
	systemctl daemon-reload
	systemctl enable mc-backup

uninstall:
	systemctl stop mc-backup 2>/dev/null || true
	systemctl disable mc-backup 2>/dev/null || true
	rm -f "$(DESTDIR)$(BINDIR)/$(BINARY)"
	rm -f "$(DESTDIR)$(SERVICEDIR)/mc-backup.service"
	systemctl daemon-reload

clean:
	rm -f $(BINARY)

BINARY   := mc-backup
GOOS     := linux
GOARCH   := amd64
PREFIX   := /usr/local
BINDIR   := $(PREFIX)/bin
CONFDIR  := /etc/mc-backup
SERVICEDIR := /etc/systemd/system
INSTALL_USER_HOME = $(or $(shell if [ -n "$(SUDO_USER)" ] && [ "$(SUDO_USER)" != "root" ]; then getent passwd "$(SUDO_USER)" | cut -d: -f6; else printf '%s' "$(HOME)"; fi),$(HOME))
USER_CONFDIR = $(INSTALL_USER_HOME)/.config/mc-backup

.PHONY: build install uninstall clean

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BINARY) ./cmd/mc-backup

install: build
	install -d $(DESTDIR)$(BINDIR) $(DESTDIR)$(CONFDIR) "$(USER_CONFDIR)"
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	[ -f "$(USER_CONFDIR)/config.toml" ] || install -m 644 config.example.toml "$(USER_CONFDIR)/config.toml"
	[ -f $(DESTDIR)$(CONFDIR)/config.toml ] || install -m 644 config.example.toml $(DESTDIR)$(CONFDIR)/config.toml
	install -m 644 mc-backup.service $(DESTDIR)$(SERVICEDIR)/mc-backup.service
	systemctl daemon-reload
	systemctl enable mc-backup

uninstall:
	systemctl stop mc-backup 2>/dev/null || true
	systemctl disable mc-backup 2>/dev/null || true
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(SERVICEDIR)/mc-backup.service
	systemctl daemon-reload

clean:
	rm -f $(BINARY)

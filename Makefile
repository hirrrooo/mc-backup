BINARY   := mc-backup
GOOS     := linux
GOARCH   := amd64
PREFIX   := /usr/local
BINDIR   := $(PREFIX)/bin
CONFDIR  := /etc/mc-backup
SERVICEDIR := /etc/systemd/system

.PHONY: build install uninstall clean

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BINARY) .

install: build
	install -d $(DESTDIR)$(BINDIR) $(DESTDIR)$(CONFDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	install -m 644 config.example.toml $(DESTDIR)$(CONFDIR)/config.toml 2>/dev/null || true
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

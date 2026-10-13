VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/aic0d3r/ddcdeck/cmd.Version=$(VERSION)
BINDIR  := $(HOME)/.local/bin
UNITDIR := $(HOME)/.config/systemd/user

APPID   := io.github.aic0d3r.DDCDeck
DATADIR := $(HOME)/.local/share

.PHONY: build build-gui test install install-cli install-service install-gui uninstall

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o ddcdeck .

build-gui:
	go build -trimpath -ldflags "$(LDFLAGS)" -o ddcdeck-gui ./cmd/ddcdeck-gui

test:
	go vet ./... && go test -race ./...

# install = everything (CLI, daemon, GUI, tray). install-cli is the headless subset.
install: build build-gui
	install -Dm755 ddcdeck $(BINDIR)/ddcdeck
	ln -sf ddcdeck $(BINDIR)/ddcdeckd
	install -Dm755 ddcdeck-gui $(BINDIR)/ddcdeck-gui
	install -Dm644 assets/$(APPID).svg $(DATADIR)/icons/hicolor/scalable/apps/$(APPID).svg
	sed 's|^Exec=ddcdeck-gui|Exec=$(BINDIR)/ddcdeck-gui|' dist/$(APPID).desktop > $(DATADIR)/applications/$(APPID).desktop
	mkdir -p $(HOME)/.config/autostart
	sed 's|^Exec=ddcdeck-gui|Exec=$(BINDIR)/ddcdeck-gui --hidden|' dist/$(APPID).desktop > $(HOME)/.config/autostart/$(APPID).desktop

install-cli: build
	install -Dm755 ddcdeck $(BINDIR)/ddcdeck
	ln -sf ddcdeck $(BINDIR)/ddcdeckd

install-service: install
	install -Dm644 dist/ddcdeckd.service $(UNITDIR)/ddcdeckd.service
	systemctl --user daemon-reload
	systemctl --user enable --now ddcdeckd

# kept as an alias: install already covers it
install-gui: install

uninstall:
	-systemctl --user disable --now ddcdeckd
	rm -f $(UNITDIR)/ddcdeckd.service $(BINDIR)/ddcdeck $(BINDIR)/ddcdeckd $(BINDIR)/ddcdeck-gui
	rm -f $(DATADIR)/applications/$(APPID).desktop $(DATADIR)/icons/hicolor/scalable/apps/$(APPID).svg
	rm -f $(HOME)/.config/autostart/$(APPID).desktop

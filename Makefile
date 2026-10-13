VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/aic0d3r/ddcdeck/cmd.Version=$(VERSION)
BINDIR  := $(HOME)/.local/bin
UNITDIR := $(HOME)/.config/systemd/user

.PHONY: build test install install-service uninstall

build:
	go build -ldflags "$(LDFLAGS)" -o ddcdeck .

test:
	go vet ./... && go test -race ./...

install: build
	install -Dm755 ddcdeck $(BINDIR)/ddcdeck
	ln -sf ddcdeck $(BINDIR)/ddcdeckd

install-service: install
	install -Dm644 dist/ddcdeckd.service $(UNITDIR)/ddcdeckd.service
	systemctl --user daemon-reload
	systemctl --user enable --now ddcdeckd

uninstall:
	-systemctl --user disable --now ddcdeckd
	rm -f $(UNITDIR)/ddcdeckd.service $(BINDIR)/ddcdeck $(BINDIR)/ddcdeckd

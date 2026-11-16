# orbital-saver build and install rules.
#
# Normally reached through the wrapper in the repository root:
#
#   make                      build bin/orbital-saver
#   sudo make install         install under /usr
#   sudo make uninstall
#
# Packagers: PREFIX and DESTDIR are honoured, e.g.
#   make install PREFIX=/usr DESTDIR="$pkgdir"

ROOT    := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))..)
BINARY  := orbital-saver
VERSION ?= $(shell git -C $(ROOT) describe --tags --always --dirty 2>/dev/null || echo dev)

# /usr by default, next to the stock themes in
# /usr/share/applications/screensavers.
PREFIX     ?= /usr
BINDIR     ?= $(PREFIX)/bin
DATADIR    ?= $(PREFIX)/share
SAVERDIR   ?= $(DATADIR)/applications/screensavers
XSSCONFDIR ?= $(DATADIR)/xscreensaver/config

# xfce4-screensaver refuses to run a theme unless its program sits in one of
# a few directories compiled into it; /usr/bin is not among them, and the
# symptom is a theme that is listed but stays black. /usr/lib/xscreensaver
# is on that list, so a symlink there is what makes the theme start.
HACKDIR    ?= $(PREFIX)/lib/xscreensaver

GO      ?= go
GOFLAGS ?= -trimpath
LDFLAGS ?= -s -w -X main.version=$(VERSION)

.PHONY: build test preview install uninstall clean

# CGO_ENABLED=0 gives a fully static binary with no libc or libX11 dependency.
build:
	cd $(ROOT) && CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/orbital-saver

test:
	cd $(ROOT) && $(GO) vet ./... && $(GO) test ./...

preview: build
	$(ROOT)/bin/$(BINARY) --preview

# install deliberately does not depend on build, so that `sudo make install`
# never runs the Go toolchain as root.
install:
	@test -x $(ROOT)/bin/$(BINARY) || { echo "bin/$(BINARY) not found: run 'make' first (as your normal user)"; exit 1; }
	install -Dm755 $(ROOT)/bin/$(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	install -Dm644 $(ROOT)/packaging/orbital-saver.desktop $(DESTDIR)$(SAVERDIR)/orbital-saver.desktop
	install -Dm644 $(ROOT)/packaging/orbital-saver.xml $(DESTDIR)$(XSSCONFDIR)/orbital-saver.xml
	install -d $(DESTDIR)$(HACKDIR)
	ln -sf $(BINDIR)/$(BINARY) $(DESTDIR)$(HACKDIR)/$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(HACKDIR)/$(BINARY)
	-rmdir $(DESTDIR)$(HACKDIR) 2>/dev/null || true
	rm -f $(DESTDIR)$(SAVERDIR)/orbital-saver.desktop
	rm -f $(DESTDIR)$(XSSCONFDIR)/orbital-saver.xml

clean:
	rm -rf $(ROOT)/bin

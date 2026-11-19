# orbital-saver build and install rules.
#
# Normally reached through the wrapper in the repository root:
#
#   make                      build bin/orbital-saver
#   sudo make install         install under /usr
#   sudo make uninstall
#   make deb                  build dist/orbital-saver_<version>_<arch>.deb
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

# Wayland: a systemd user unit for the live wallpaper, and a niri snippet.
USERUNITDIR ?= $(PREFIX)/lib/systemd/user
DOCDIR      ?= $(DATADIR)/doc/$(BINARY)

GO      ?= go
GOFLAGS ?= -trimpath
LDFLAGS ?= -s -w -X main.version=$(VERSION)

# Debian package settings. A version tag such as v1.2.0 becomes 1.2.0; an
# untagged build becomes 0~git.<commit>, which sorts below every release.
DEB_VERSION    ?= $(shell echo '$(VERSION)' | sed -E 's/^v([0-9])/\1/; t; s/^/0~git./')
DEB_ARCH       ?= $(shell dpkg --print-architecture 2>/dev/null)
DEB_MAINTAINER ?= codetheuri <codetheuri@users.noreply.github.com>
DEB_FILE       := $(ROOT)/dist/$(BINARY)_$(DEB_VERSION)_$(DEB_ARCH).deb

.PHONY: build test preview install uninstall deb clean

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
	install -d $(DESTDIR)$(USERUNITDIR)
	sed 's|@BINDIR@|$(BINDIR)|g' $(ROOT)/packaging/systemd/orbital-saver.service.in \
		> $(DESTDIR)$(USERUNITDIR)/orbital-saver.service
	chmod 644 $(DESTDIR)$(USERUNITDIR)/orbital-saver.service
	install -Dm644 $(ROOT)/packaging/niri/orbital-saver.kdl $(DESTDIR)$(DOCDIR)/niri.kdl

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(HACKDIR)/$(BINARY)
	-rmdir $(DESTDIR)$(HACKDIR) 2>/dev/null || true
	rm -f $(DESTDIR)$(SAVERDIR)/orbital-saver.desktop
	rm -f $(DESTDIR)$(XSSCONFDIR)/orbital-saver.xml
	rm -f $(DESTDIR)$(USERUNITDIR)/orbital-saver.service
	rm -f $(DESTDIR)$(DOCDIR)/niri.kdl
	-rmdir $(DESTDIR)$(DOCDIR) 2>/dev/null || true

# Runs a normal install into a fresh staging directory under build/ and wraps
# it with dpkg-deb. No root needed: --root-owner-group makes the packaged
# files root-owned. Staging directories are left for `make clean`.
deb: build
	@command -v dpkg-deb >/dev/null || { echo "dpkg-deb not found: 'make deb' needs a Debian-based system"; exit 1; }
	@mkdir -p $(ROOT)/build $(ROOT)/dist
	@stage=$$(mktemp -d $(ROOT)/build/deb.XXXXXX) && chmod 755 $$stage && \
	$(MAKE) --no-print-directory -f $(ROOT)/packaging/Makefile install DESTDIR=$$stage PREFIX=/usr >/dev/null && \
	install -Dm644 $(ROOT)/LICENSE $$stage/usr/share/doc/$(BINARY)/copyright && \
	size=$$(du -sk $$stage | cut -f1) && \
	install -d $$stage/DEBIAN && \
	sed -e 's|@VERSION@|$(DEB_VERSION)|' -e 's|@ARCH@|$(DEB_ARCH)|' \
	    -e 's|@MAINTAINER@|$(DEB_MAINTAINER)|' -e "s|@SIZE@|$$size|" \
	    $(ROOT)/packaging/deb/control.in > $$stage/DEBIAN/control && \
	dpkg-deb --build --root-owner-group $$stage $(DEB_FILE)
	@echo "Install with: sudo apt install $(DEB_FILE)"

clean:
	rm -rf $(ROOT)/bin $(ROOT)/build $(ROOT)/dist

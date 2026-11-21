# Thin wrapper so the usual `make && sudo make install` works from the
# repository root. The real rules live in packaging/Makefile.

TARGETS := build test install uninstall deb clean

.DEFAULT_GOAL := build
.PHONY: $(TARGETS)

$(TARGETS):
	@$(MAKE) --no-print-directory -f packaging/Makefile $@

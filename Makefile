BINARY  := web-fetch
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
# -trimpath: a binary records its source file names; without it they are the
# absolute paths of this checkout and the module cache, i.e. the builder's
# home directory and login, in every published asset. verify-release checks.
GOBUILD := go build -trimpath $(LDFLAGS)

# macOS Developer ID signing / notarization (see nlink-jp/.github
# CONVENTIONS.md §Code Signing). Defaults match any Developer ID
# Application cert in the keychain and the org-standard notary
# profile. Builds without these fall back to ad-hoc / un-notarized
# with a one-line warning — see scripts/codesign-darwin.sh.
CODESIGN_IDENTITY ?= Developer ID Application
NOTARY_PROFILE    ?= nlink-jp-notary

# darwin ships arm64 only (no amd64, no universal). linux/windows keep their matrix.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/arm64 \
	windows/amd64

.PHONY: build build-all test lint check e2e package verify-release clean help vuln

## build: Build for the current platform
build:
	@mkdir -p dist
	$(GOBUILD) -o dist/$(BINARY) .
	@scripts/codesign-darwin.sh dist/$(BINARY) "$(CODESIGN_IDENTITY)"

## build-all: Cross-compile for all target platforms
build-all:
	@mkdir -p dist
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = windows ] && ext=".exe"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GOBUILD) -o dist/$(BINARY)-$$os-$$arch$$ext . ; \
	done
	@scripts/codesign-darwin.sh dist/$(BINARY)-darwin-arm64 "$(CODESIGN_IDENTITY)" "$(BINARY)"

## test: Run the full test suite (offline)
test:
	go test -race -cover ./...

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## check: Run lint + test + build-all
check: lint test build-all

## e2e: Live end-to-end tests against real sites (network required). The Go
## suite's status is kept (its full log is dist/e2e-go.log); the filtered
## echo is for reading, not for deciding.
e2e: build
	@go test -tags e2e -count=1 -v ./e2e/... > dist/e2e-go.log 2>&1; rc=$$?; \
		grep -E "^(=== RUN|--- |MEASURED|\s+live_test|ok|FAIL|PASS)" dist/e2e-go.log; \
		exit $$rc
	@scripts/e2e.sh

## vuln: known vulnerabilities in the modules and the standard library this
## build uses (govulncheck). `package` depends on it, so a release cannot be
## built past a finding. It needs the network, and its database changes under
## an unchanged tree: a finding can appear without a commit.
vuln:
	govulncheck ./...

## package: Build all platforms, archive with version suffix (zip for
## darwin/windows, tar.gz for linux), bundle the canonical binary +
## README.md + LICENSE, and notarize the darwin build. Asset naming
## follows the org Release Archive Standard
## (web-fetch-vX.Y.Z-<os>-<arch>.<ext>). The linux archives record
## root/root (0/0) as every member's owner: bsdtar otherwise stores the
## builder's login and group names from the staging files.
package: vuln build-all
	@cd dist && for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = windows ] && ext=".exe"; \
		stage=_pkg; rm -rf $$stage; mkdir -p $$stage; \
		cp "$(BINARY)-$$os-$$arch$$ext" "$$stage/$(BINARY)$$ext"; \
		cp ../README.md ../LICENSE $$stage/; \
		base="$(BINARY)-$(VERSION)-$$os-$$arch"; \
		if [ "$$os" = linux ]; then ( cd $$stage && COPYFILE_DISABLE=1 tar --no-xattrs --uid 0 --gid 0 --uname root --gname root -czf "../$$base.tar.gz" * ); \
		else ( cd $$stage && zip -q "../$$base.zip" * ); fi; \
		rm -rf $$stage; \
	done
	@scripts/notarize-darwin.sh dist/$(BINARY)-$(VERSION)-darwin-arm64.zip "$(NOTARY_PROFILE)"

## verify-release: refuse to release a zip that is un-notarized, stale, does
## not unpack, does not run, or holds a build from another tag, a binary that
## embeds the builder's home directory (a build without -trimpath), and a
## linux archive that carries macOS metadata, the builder's owner names, or
## anything but its canonical files. Every step fails closed; only the spctl
## line is informational.
verify-release:
	@test -f "dist/$(BINARY)-$(VERSION)-darwin-arm64.zip.notarized" || { \
		echo "verify-release: FAIL — $(BINARY)-$(VERSION)-darwin-arm64.zip has no notarization marker."; \
		echo "  make package must end with '[notarize] ...: Accepted'. Do not upload this zip."; \
		exit 1; }
	@test "dist/$(BINARY)-$(VERSION)-darwin-arm64.zip.notarized" -nt "dist/$(BINARY)-$(VERSION)-darwin-arm64.zip" || { \
		echo "verify-release: FAIL — the zip was rebuilt after its marker (re-run make package)."; \
		exit 1; }
	@tmp=$$(mktemp -d); rc=0; \
		if ! unzip -oq "dist/$(BINARY)-$(VERSION)-darwin-arm64.zip" -d "$$tmp"; then \
			echo "verify-release: FAIL — the zip does not unpack. Do not upload it."; rc=1; \
		elif ! out=$$("$$tmp/$(BINARY)" --version 2>&1); then \
			echo "verify-release: FAIL — the packaged binary does not run:"; \
			echo "  $$out"; rc=1; \
		elif ! printf '%s\n' "$$out" | grep -qF "$(VERSION)"; then \
			echo "verify-release: FAIL — the packaged binary reports \"$$out\", not $(VERSION)."; \
			echo "  The zip holds a build from another tag (re-run make package)."; rc=1; \
		elif grep -qaF "$$HOME" "$$tmp/$(BINARY)"; then \
			echo "verify-release: FAIL — the packaged binary embeds this home directory."; \
			echo "  It was built without -trimpath (re-run make package)."; rc=1; \
		else \
			echo "  $$out"; \
			spctl -a -vv -t install "$$tmp/$(BINARY)" 2>&1 | head -2 || true; \
		fi; \
		rm -rf "$$tmp"; \
		exit $$rc
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		[ "$$os" = linux ] || continue; \
		f="dist/$(BINARY)-$(VERSION)-$$os-$$arch.tar.gz"; \
		names=$$(tar --options 'tar:!mac-ext' -tzf "$$f") || { echo "verify-release: FAIL — $$f does not list."; exit 1; }; \
		if printf '%s\n' "$$names" | grep -qE '(^|/)(\._|PaxHeader|__MACOSX)'; then \
			echo "verify-release: FAIL — $$f carries macOS metadata entries."; \
			echo "  macOS tar writes ._ members unless COPYFILE_DISABLE=1 is set, and lists them only with !mac-ext."; \
			exit 1; fi; \
		xh=$$(python3 -c 'import sys, tarfile; print(" ".join(m.name for m in tarfile.open(sys.argv[1]) if any(k.startswith(("LIBARCHIVE.xattr.", "SCHILY.xattr.")) for k in m.pax_headers)))' "$$f") || { \
			echo "verify-release: FAIL — $$f cannot be read for its pax headers (python3 tarfile)."; exit 1; }; \
		if [ -n "$$xh" ]; then \
			echo "verify-release: FAIL — $$f carries extended attributes as pax headers ($$xh)."; \
			echo "  macOS tar writes them unless called with --no-xattrs; COPYFILE_DISABLE alone does not."; \
			exit 1; fi; \
		own=$$(python3 -c 'import sys, tarfile; print(" ".join("%s(%s:%s %s:%s)" % (m.name, m.uid, m.gid, m.uname, m.gname) for m in tarfile.open(sys.argv[1]) if (m.uid, m.gid, m.uname, m.gname) != (0, 0, "root", "root")))' "$$f") || { \
			echo "verify-release: FAIL — $$f cannot be read for its owner fields (python3 tarfile)."; exit 1; }; \
		if [ -n "$$own" ]; then \
			echo "verify-release: FAIL — $$f records the builder as owner ($$own)."; \
			echo "  make package passes --uid 0 --gid 0 --uname root --gname root to tar; this archive was made another way."; \
			exit 1; fi; \
		got=$$(printf '%s\n' "$$names" | LC_ALL=C sort | tr '\n' ' '); \
		want=$$(printf '%s\n' "$(BINARY)" README.md LICENSE | LC_ALL=C sort | tr '\n' ' '); \
		if [ "$$got" != "$$want" ]; then \
			echo "verify-release: FAIL — $$f holds $$got; expected $$want"; exit 1; fi; \
		tmp=$$(mktemp -d); \
		if ! tar -xzf "$$f" -C "$$tmp" "$(BINARY)"; then \
			echo "verify-release: FAIL — $$f does not unpack."; rm -rf "$$tmp"; exit 1; fi; \
		if grep -qaF "$$HOME" "$$tmp/$(BINARY)"; then \
			echo "verify-release: FAIL — the binary in $$f embeds this home directory."; \
			echo "  It was built without -trimpath (re-run make package)."; rm -rf "$$tmp"; exit 1; fi; \
		rm -rf "$$tmp"; \
	done
	@echo "verify-release: OK ($(VERSION), notarized, unpacks, runs, reports its version, no builder paths or owners, clean linux archives)"

## clean: Remove build artifacts
clean:
	rm -rf dist/

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'

# Homebrew tap generation (see scripts/release-brew.mk). After `make package`,
# `make brew` generates this formula from the built darwin-arm64 zip into the
# local nlink-jp/homebrew-tap checkout. The package target is unchanged.
BREW_KIND := formula
BREW_DESC := Fetch one URL's readable text from this machine, as a CLI and MCP server
include scripts/release-brew.mk

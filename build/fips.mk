# FIPS build targets. Opt-in: a plugin includes this file via the same
# wildcard mechanism the Makefile uses for build/custom.mk. The file's
# presence is also the marker the delivery-platform release pipeline uses
# to detect that a plugin has opted into the FIPS build leg.
#
# Linux/amd64 only — that's the only platform Mattermost server's FIPS
# release path supports today.

FIPS_IMAGE ?= cgr.dev/mattermost.com/go-msft-fips:1.26.3-dev@sha256:48ab99fede7fb33e132a0636072971e1ec4a69520865bfa1e4b517ee9cfdef34
BUNDLE_NAME_FIPS ?= $(PLUGIN_ID)-$(PLUGIN_VERSION)-fips.tar.gz
FIPS_BIN := server/dist-fips/plugin-linux-amd64-fips

# The FIPS build deliberately does NOT inherit GO_BUILD_LDFLAGS, because the
# plugin's default release LDFLAGS is `-ldflags="-s -w"` (strip symbol +
# DWARF tables). With `-s` the binary has no symbol section, and the
# canonical `go tool nm | grep …OpenSSL_version` check in verify-fips
# returns zero symbols and fails. `mattermost/server`'s LDFLAGS doesn't
# include `-s -w` (it only injects build-info -X values), which is why the
# same nm check works there. Unstripped FIPS binaries are larger but
# verifiable; that's the right tradeoff for a compliance gate.
FIPS_GO_BUILD_LDFLAGS ?=

# Builds the server binary inside the FIPS Go toolchain image. The
# GO_BUILD_* values are Make-side-interpolated into a single-quoted inner
# shell script so the FIPS binary is built with the same -gcflags etc. as
# the non-FIPS one (LDFLAGS excepted — see above). Env-var passing
# (`-e VAR=...`) is unsafe because GO_BUILD_GCFLAGS can contain embedded
# double quotes (e.g. `-gcflags "all=-N -l"`) that don't survive a second
# pass of shell word splitting inside the container.
.PHONY: server-fips
server-fips: generate
	mkdir -p server/dist-fips
	docker run --rm \
	  --entrypoint="" \
	  -v $(PWD):/plugin \
	  -w /plugin/server \
	  $(FIPS_IMAGE) \
	  /bin/sh -c 'CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
	    go build -trimpath -buildvcs=false \
	    $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(FIPS_GO_BUILD_LDFLAGS) \
	    -tags requirefips \
	    -o dist-fips/plugin-linux-amd64-fips'

# Asserts FIPS markers on the produced binary. Same three checks the server
# Makefile uses (mattermost/server/build/release.mk). Each check is its OWN
# Make recipe line so a failure aborts immediately; joining them with `; \`
# would propagate only the final command's exit status and let a failed
# check silently pass.
.PHONY: verify-fips
verify-fips:
	@test -f $(FIPS_BIN) || (echo "verify-fips: $(FIPS_BIN) not built" && exit 1)
	$(GO) version -m $(FIPS_BIN) | grep -q "GOEXPERIMENT=systemcrypto" || (echo "ERROR: missing GOEXPERIMENT=systemcrypto" && exit 1)
	$(GO) version -m $(FIPS_BIN) | grep "\-tags" | grep -q "requirefips" || (echo "ERROR: missing -tags=requirefips" && exit 1)
	$(GO) tool nm $(FIPS_BIN) | grep -qE "func_go_openssl_OpenSSL_version|_mkcgo_OpenSSL_version" || (echo "ERROR: missing OpenSSL integration" && exit 1)
	@echo "verify-fips: OK"

# bundle-fips is a thin wrapper over `bundle`. The only things that differ
# between FIPS and non-FIPS bundling are the server-binary source dir, the
# output dir, and the tarball filename — all three parameterized on `bundle`
# itself via BUNDLE_DIR / BUNDLE_NAME / SERVER_DIST_SRC.
#
# Depends on verify-fips so a direct `make bundle-fips` invocation can't
# package a binary that hasn't passed the FIPS marker checks. Make
# deduplicates phony targets within a single invocation, so dist-fips /
# dist-all still run verify-fips exactly once.
.PHONY: bundle-fips
bundle-fips: verify-fips
	rm -rf server/dist-fips-staged
	mkdir -p server/dist-fips-staged
	cp $(FIPS_BIN) server/dist-fips-staged/plugin-linux-amd64
	$(MAKE) bundle BUNDLE_DIR=dist-fips BUNDLE_NAME=$(BUNDLE_NAME_FIPS) SERVER_DIST_SRC=server/dist-fips-staged

.PHONY: dist-fips
dist-fips: apply server-fips verify-fips webapp bundle-fips

# Flat prerequisite list so `apply` and `webapp` run once, not twice.
.PHONY: dist-all
dist-all: apply webapp server server-fips verify-fips bundle bundle-fips

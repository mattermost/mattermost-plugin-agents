# FIPS-compliant Linux/amd64 build. Linux/amd64 is the only platform
# mattermost-server's FIPS release path supports.

# Microsoft Go FIPS toolchain image, digest-pinned. The Go version must satisfy
# the go directive in go.mod. To bump: set the new tag without a digest, let the
# build-fips CI job pull it, then pin the digest CI resolves. Tags follow
# microsoft/go releases (vX.Y.Z-N -> X.Y.Z.N-dev). To list available tags, the
# registry needs CI's Chainguard credentials; temporarily add this recipe line:
#   CREDS=$$(printf 'cgr.dev' | docker-credential-cgr get); \
#   TOKEN=$$(curl -s -u "$$(echo $$CREDS | jq -r .Username):$$(echo $$CREDS | jq -r .Secret)" \
#     "https://cgr.dev/token?scope=repository:mattermost.com/go-msft-fips:pull" | jq -r .token); \
#   curl -s -H "Authorization: Bearer $$TOKEN" "https://cgr.dev/v2/mattermost.com/go-msft-fips/tags/list"
#
# NOTE (2026-08-26): the 1.27.0.1 image is not published yet (registry tops out
# at 1.26.7.1); build-fips stays red until Chainguard builds msgo v1.27.0-1,
# then pin the digest.
FIPS_IMAGE ?= cgr.dev/mattermost.com/go-msft-fips:1.27.0.1-dev
BUNDLE_NAME_FIPS ?= $(PLUGIN_ID)-$(PLUGIN_VERSION)-fips.tar.gz
FIPS_BIN := server/dist-fips/plugin-linux-amd64-fips

# Empty by default. Inheriting the plugin's release LDFLAGS (`-ldflags="-s -w"`)
# would strip the symbol table, and verify-fips below needs symbols intact
# for `go tool nm` to find the OpenSSL integration.
FIPS_GO_BUILD_LDFLAGS ?=

# GO_BUILD_* are Make-substituted into the single-quoted inner script.
# Env-var passing (`-e VAR=...`) doesn't survive a second pass of word
# splitting on values with embedded quotes like `-gcflags "all=-N -l"`.
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

# Mirrors mattermost-server/build/release.mk. Each check is its own recipe
# line so a failure exits with the right status; joined with `; \`, only
# the last command's exit propagates and a failed check would be masked.
.PHONY: verify-fips
verify-fips:
	@test -f $(FIPS_BIN) || (echo "verify-fips: $(FIPS_BIN) not built" && exit 1)
	$(GO) version -m $(FIPS_BIN) | grep -q "GOEXPERIMENT=systemcrypto" || (echo "ERROR: missing GOEXPERIMENT=systemcrypto" && exit 1)
	$(GO) version -m $(FIPS_BIN) | grep "\-tags" | grep -q "requirefips" || (echo "ERROR: missing -tags=requirefips" && exit 1)
	$(GO) tool nm $(FIPS_BIN) | grep -qE "func_go_openssl_OpenSSL_version|_mkcgo_OpenSSL_version" || (echo "ERROR: missing OpenSSL integration" && exit 1)
	@echo "verify-fips: OK"

# Depends on verify-fips so a direct `make bundle-fips` can't package an
# unverified binary. Phony-target dedup means dist-fips / dist-all still
# run verify-fips exactly once.
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

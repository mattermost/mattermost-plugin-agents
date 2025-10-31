GO ?= $(shell command -v go 2> /dev/null)
NPM ?= $(shell command -v npm 2> /dev/null)
CURL ?= $(shell command -v curl 2> /dev/null)
MM_DEBUG ?=
GOPATH ?= $(shell go env GOPATH)
GO_TEST_FLAGS ?= -race
GO_BUILD_FLAGS ?=
MM_UTILITIES_DIR ?= ../mattermost-utilities
DLV_DEBUG_PORT := 2346
DEFAULT_GOOS := $(shell go env GOOS)
DEFAULT_GOARCH := $(shell go env GOARCH)

export GO111MODULE=on

# We need to export GOBIN to allow it to be set
# for processes spawned from the Makefile
export GOBIN ?= $(PWD)/bin

# You can include assets this directory into the bundle. This can be e.g. used to include profile pictures.
ASSETS_DIR ?= assets

## Define the default target (make all)
.PHONY: default
default: all

# Verify environment, and define PLUGIN_ID, PLUGIN_VERSION, HAS_SERVER and HAS_WEBAPP as needed.
include build/setup.mk

BUNDLE_NAME ?= $(PLUGIN_ID)-$(PLUGIN_VERSION).tar.gz

# Include custom makefile, if present
ifneq ($(wildcard build/custom.mk),)
	include build/custom.mk
endif

ifneq ($(MM_DEBUG),)
	GO_BUILD_GCFLAGS = -gcflags "all=-N -l"
	GO_BUILD_LDFLAGS =
else
	GO_BUILD_GCFLAGS =
	GO_BUILD_LDFLAGS = -ldflags="-s -w"
endif


# ====================================================================================
# Used for semver bumping
PROTECTED_BRANCH := master
APP_NAME    := $(shell basename -s .git `git config --get remote.origin.url`)
CURRENT_VERSION := $(shell git describe --abbrev=0 --tags --match "v[0-9]*\.[0-9]*\.[0-9]*")
VERSION_PARTS := $(subst ., ,$(subst v,,$(subst -rc, ,$(CURRENT_VERSION))))
MAJOR := $(word 1,$(VERSION_PARTS))
MINOR := $(word 2,$(VERSION_PARTS))
PATCH := $(word 3,$(VERSION_PARTS))
RC := $(shell echo $(CURRENT_VERSION) | grep -oE 'rc[0-9]+' | sed 's/rc//')
# Check if current branch is protected
define check_protected_branch
	@current_branch=$$(git rev-parse --abbrev-ref HEAD); \
	if ! echo "$(PROTECTED_BRANCH)" | grep -wq "$$current_branch" && ! echo "$$current_branch" | grep -q "^release"; then \
		echo "Error: Tagging is only allowed from $(PROTECTED_BRANCH) or release branches. You are on $$current_branch branch."; \
		exit 1; \
	fi
endef
# Check if there are pending pulls
define check_pending_pulls
	@git fetch; \
	current_branch=$$(git rev-parse --abbrev-ref HEAD); \
	if [ "$$(git rev-parse HEAD)" != "$$(git rev-parse origin/$$current_branch)" ]; then \
		echo "Error: Your branch is not up to date with upstream. Please pull the latest changes before performing a release"; \
		exit 1; \
	fi
endef
# Prompt for approval
define prompt_approval
	@read -p "About to bump $(APP_NAME) to version $(1), approve? (y/n) " userinput; \
	if [ "$$userinput" != "y" ]; then \
		echo "Bump aborted."; \
		exit 1; \
	fi
endef
# ====================================================================================

.PHONY: patch minor major patch-rc minor-rc major-rc

patch: ## to bump patch version (semver)
	$(call check_protected_branch)
	$(call check_pending_pulls)
	@$(eval PATCH := $(shell echo $$(($(PATCH)+1))))
	$(call prompt_approval,$(MAJOR).$(MINOR).$(PATCH))
	@echo Bumping $(APP_NAME) to Patch version $(MAJOR).$(MINOR).$(PATCH)
	git tag -s -a v$(MAJOR).$(MINOR).$(PATCH) -m "Bumping $(APP_NAME) to Patch version $(MAJOR).$(MINOR).$(PATCH)"
	git push origin v$(MAJOR).$(MINOR).$(PATCH)
	@echo Bumped $(APP_NAME) to Patch version $(MAJOR).$(MINOR).$(PATCH)

minor: ## to bump minor version (semver)
	$(call check_protected_branch)
	$(call check_pending_pulls)
	@$(eval MINOR := $(shell echo $$(($(MINOR)+1))))
	@$(eval PATCH := 0)
	$(call prompt_approval,$(MAJOR).$(MINOR).$(PATCH))
	@echo Bumping $(APP_NAME) to Minor version $(MAJOR).$(MINOR).$(PATCH)
	git tag -s -a v$(MAJOR).$(MINOR).$(PATCH) -m "Bumping $(APP_NAME) to Minor version $(MAJOR).$(MINOR).$(PATCH)"
	git push origin v$(MAJOR).$(MINOR).$(PATCH)
	@echo Bumped $(APP_NAME) to Minor version $(MAJOR).$(MINOR).$(PATCH)

major: ## to bump major version (semver)
	$(call check_protected_branch)
	$(call check_pending_pulls)
	$(eval MAJOR := $(shell echo $$(($(MAJOR)+1))))
	$(eval MINOR := 0)
	$(eval PATCH := 0)
	$(call prompt_approval,$(MAJOR).$(MINOR).$(PATCH))
	@echo Bumping $(APP_NAME) to Major version $(MAJOR).$(MINOR).$(PATCH)
	git tag -s -a v$(MAJOR).$(MINOR).$(PATCH) -m "Bumping $(APP_NAME) to Major version $(MAJOR).$(MINOR).$(PATCH)"
	git push origin v$(MAJOR).$(MINOR).$(PATCH)
	@echo Bumped $(APP_NAME) to Major version $(MAJOR).$(MINOR).$(PATCH)

patch-rc: ## to bump patch release candidate version (semver)
	$(call check_protected_branch)
	$(call check_pending_pulls)
	@$(eval RC := $(shell echo $$(($(RC)+1))))
	$(call prompt_approval,$(MAJOR).$(MINOR).$(PATCH)-rc$(RC))
	@echo Bumping $(APP_NAME) to Patch RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)
	git tag -s -a v$(MAJOR).$(MINOR).$(PATCH)-rc$(RC) -m "Bumping $(APP_NAME) to Patch RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)"
	git push origin v$(MAJOR).$(MINOR).$(PATCH)-rc$(RC)
	@echo Bumped $(APP_NAME) to Patch RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)

minor-rc: ## to bump minor release candidate version (semver)
	$(call check_protected_branch)
	$(call check_pending_pulls)
	@$(eval MINOR := $(shell echo $$(($(MINOR)+1))))
	@$(eval PATCH := 0)
	@$(eval RC := 1)
	$(call prompt_approval,$(MAJOR).$(MINOR).$(PATCH)-rc$(RC))
	@echo Bumping $(APP_NAME) to Minor RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)
	git tag -s -a v$(MAJOR).$(MINOR).$(PATCH)-rc$(RC) -m "Bumping $(APP_NAME) to Minor RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)"
	git push origin v$(MAJOR).$(MINOR).$(PATCH)-rc$(RC)
	@echo Bumped $(APP_NAME) to Minor RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)

major-rc: ## to bump major release candidate version (semver)
	$(call check_protected_branch)
	$(call check_pending_pulls)
	@$(eval MAJOR := $(shell echo $$(($(MAJOR)+1))))
	@$(eval MINOR := 0)
	@$(eval PATCH := 0)
	@$(eval RC := 1)
	$(call prompt_approval,$(MAJOR).$(MINOR).$(PATCH)-rc$(RC))
	@echo Bumping $(APP_NAME) to Major RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)
	git tag -s -a v$(MAJOR).$(MINOR).$(PATCH)-rc$(RC) -m "Bumping $(APP_NAME) to Major RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)"
	git push origin v$(MAJOR).$(MINOR).$(PATCH)-rc$(RC)
	@echo Bumped $(APP_NAME) to Major RC version $(MAJOR).$(MINOR).$(PATCH)-rc$(RC)

## Checks the code style, tests, builds and bundles the plugin.
.PHONY: all
all: check-style test dist

## Ensures the plugin manifest is valid
.PHONY: manifest-check
manifest-check:
	./build/bin/manifest check

## Propagates plugin manifest information into the server/ and webapp/ folders.
.PHONY: apply
apply:
	./build/bin/manifest apply

## Install go tools
install-go-tools:
	@echo Installing go tools
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.0.2
	$(GO) install gotest.tools/gotestsum@v1.7.0
	$(GO) install github.com/mattermost/mattermost-govet/v2@3f08281c344327ac09364f196b15f9a81c7eff08

## Runs eslint and golangci-lint
.PHONY: check-style
check-style: manifest-check apply webapp/node_modules install-go-tools
	@echo Checking for style guide compliance

ifneq ($(HAS_WEBAPP),)
	cd webapp && npm run lint
	cd webapp && npm run check-types
endif

# It's highly recommended to run go-vet first
# to find potential compile errors that could introduce
# weird reports at golangci-lint step
ifneq ($(HAS_SERVER),)
	@echo Running golangci-lint
	$(GO) vet ./...
	$(GOBIN)/golangci-lint run ./...
	$(GO) vet -vettool=$(GOBIN)/mattermost-govet -license -license.year=2023 ./...
endif

## Runs all style checks but fixes anything it can.
.PHONY: check-style-fix
check-style-fix: manifest-check apply webapp/node_modules
	goimports -w .
	./scripts/fix_license_headers.sh 2023
	cd webapp && npm run fix
	cd webapp && npm run check-types
	$(GO) vet ./...
	$(GOBIN)/golangci-lint run --fix ./...

generate:
	$(GO) generate ./...

## Builds the server, if it exists, for all supported architectures, unless MM_SERVICESETTINGS_ENABLEDEVELOPER is set.
.PHONY: server
server: generate
ifneq ($(HAS_SERVER),)
ifneq ($(MM_DEBUG),)
	$(info DEBUG mode is on; to disable, unset MM_DEBUG)
endif
	mkdir -p server/dist;
ifneq ($(MM_SERVICESETTINGS_ENABLEDEVELOPER),)
	@echo Building plugin only for $(DEFAULT_GOOS)-$(DEFAULT_GOARCH) because MM_SERVICESETTINGS_ENABLEDEVELOPER is enabled
	cd server && env CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -trimpath -o dist/plugin-$(DEFAULT_GOOS)-$(DEFAULT_GOARCH);
else
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -trimpath -o dist/plugin-linux-amd64;
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -trimpath -o dist/plugin-linux-arm64;
	cd server && env CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -trimpath -o dist/plugin-darwin-amd64;
	cd server && env CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -trimpath -o dist/plugin-darwin-arm64;
	cd server && env CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -trimpath -o dist/plugin-windows-amd64.exe;
endif
endif

## Ensures NPM dependencies are installed without having to run this all the time.
webapp/node_modules: $(wildcard webapp/package.json)
ifneq ($(HAS_WEBAPP),)
	cd webapp && $(NPM) install
	touch $@
endif

## Builds the webapp, if it exists.
.PHONY: webapp
webapp: webapp/node_modules
ifneq ($(HAS_WEBAPP),)
ifeq ($(MM_DEBUG),)
	cd webapp && $(NPM) run build;
else
	cd webapp && $(NPM) run debug;
endif
endif

## Generates a tar bundle of the plugin for install.
.PHONY: bundle
bundle:
	rm -rf dist/
	mkdir -p dist/$(PLUGIN_ID)
	./build/bin/manifest dist
ifneq ($(wildcard LICENSE.txt),)
	cp -r LICENSE.txt dist/$(PLUGIN_ID)/
endif
ifneq ($(wildcard NOTICE.txt),)
	cp -r NOTICE.txt dist/$(PLUGIN_ID)/
endif
ifneq ($(wildcard $(ASSETS_DIR)/.),)
	cp -r $(ASSETS_DIR) dist/$(PLUGIN_ID)/
endif
ifneq ($(HAS_PUBLIC),)
	cp -r public dist/$(PLUGIN_ID)/
endif
ifneq ($(HAS_SERVER),)
	mkdir -p dist/$(PLUGIN_ID)/server
	cp -r server/dist dist/$(PLUGIN_ID)/server/
endif
ifneq ($(HAS_WEBAPP),)
	mkdir -p dist/$(PLUGIN_ID)/webapp
	cp -r webapp/dist dist/$(PLUGIN_ID)/webapp/
endif
ifeq ($(shell uname),Darwin)
	cd dist && tar --disable-copyfile -cvzf $(BUNDLE_NAME) $(PLUGIN_ID)
else
	cd dist && tar -cvzf $(BUNDLE_NAME) $(PLUGIN_ID)
endif

	@echo plugin built at: dist/$(BUNDLE_NAME)

## Builds the server for Linux amd64 only (CI optimized).
.PHONY: server-ci
server-ci: generate
ifneq ($(HAS_SERVER),)
ifneq ($(MM_DEBUG),)
	$(info DEBUG mode is on; to disable, unset MM_DEBUG)
endif
	mkdir -p server/dist;
	@echo Building plugin only for linux-amd64 for CI
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -trimpath -o dist/plugin-linux-amd64;
endif

## Builds and bundles the plugin.
.PHONY: dist
dist: apply server webapp bundle

## Builds and bundles the plugin for CI (Linux amd64 only).
.PHONY: dist-ci
dist-ci: apply server-ci webapp bundle

## Builds and installs the plugin to a server.
.PHONY: deploy
deploy: dist
	./build/bin/pluginctl deploy $(PLUGIN_ID) dist/$(BUNDLE_NAME)

## Builds the MCP server binary.
.PHONY: mcp-server
mcp-server:
	@echo Building MCP server...
	$(GO) build $(GO_BUILD_FLAGS) $(GO_BUILD_GCFLAGS) $(GO_BUILD_LDFLAGS) -o bin/mattermost-mcp-server ./mcpserver/cmd/main.go

## Builds and installs the plugin to a server, updating the webapp automatically when changed.
.PHONY: watch
watch: apply server bundle
ifeq ($(MM_DEBUG),)
	cd webapp && $(NPM) run build:watch
else
	cd webapp && $(NPM) run debug:watch
endif

## Installs a previous built plugin with updated webpack assets to a server.
.PHONY: deploy-from-watch
deploy-from-watch: bundle
	./build/bin/pluginctl deploy $(PLUGIN_ID) dist/$(BUNDLE_NAME)

## Setup dlv for attaching, identifying the plugin PID for other targets.
.PHONY: setup-attach
setup-attach:
	$(eval PLUGIN_PID := $(shell ps aux | grep "plugins/${PLUGIN_ID}" | grep -v "grep" | awk -F " " '{print $$2}'))
	$(eval NUM_PID := $(shell echo -n ${PLUGIN_PID} | wc -w))

	@if [ ${NUM_PID} -gt 2 ]; then \
		echo "** There is more than 1 plugin process running. Run 'make kill reset' to restart just one."; \
		exit 1; \
	fi

## Check if setup-attach succeeded.
.PHONY: check-attach
check-attach:
	@if [ -z ${PLUGIN_PID} ]; then \
		echo "Could not find plugin PID; the plugin is not running. Exiting."; \
		exit 1; \
	else \
		echo "Located Plugin running with PID: ${PLUGIN_PID}"; \
	fi

## Attach dlv to an existing plugin instance.
.PHONY: attach
attach: setup-attach check-attach
	dlv attach ${PLUGIN_PID}

## Attach dlv to an existing plugin instance, exposing a headless instance on $DLV_DEBUG_PORT.
.PHONY: attach-headless
attach-headless: setup-attach check-attach
	dlv attach ${PLUGIN_PID} --listen :$(DLV_DEBUG_PORT) --headless=true --api-version=2 --accept-multiclient

## Detach dlv from an existing plugin instance, if previously attached.
.PHONY: detach
detach: setup-attach
	@DELVE_PID=$(shell ps aux | grep "dlv attach ${PLUGIN_PID}" | grep -v "grep" | awk -F " " '{print $$2}') && \
	if [ "$$DELVE_PID" -gt 0 ] > /dev/null 2>&1 ; then \
		echo "Located existing delve process running with PID: $$DELVE_PID. Killing." ; \
		kill -9 $$DELVE_PID ; \
	fi

## Runs any lints and unit tests defined for the server and webapp, if they exist.
.PHONY: test
test: apply webapp/node_modules install-go-tools
ifneq ($(HAS_SERVER),)
	$(GOBIN)/gotestsum -- -v ./...
endif
ifneq ($(HAS_WEBAPP),)
	cd webapp && $(NPM) run test;
endif

## Runs any lints and unit tests defined for the server and webapp, if they exist, optimized
## for a CI environment.
.PHONY: test-ci
test-ci: apply webapp/node_modules install-go-tools
ifneq ($(HAS_SERVER),)
	$(GOBIN)/gotestsum --format standard-verbose --junitfile report.xml -- ./...
endif
ifneq ($(HAS_WEBAPP),)
	cd webapp && $(NPM) run test;
endif

## Creates a coverage report for the server code.
.PHONY: coverage
coverage: apply webapp/node_modules
ifneq ($(HAS_SERVER),)
	$(GO) test $(GO_TEST_FLAGS) -coverprofile=server/coverage.txt ./server/...
	$(GO) tool cover -html=server/coverage.txt
endif

## Extract strings for translation from the source code.
i18n-extract: i18n-extract-webapp i18n-extract-server

.PHONY: i18n-extract-webapp
i18n-extract-webapp:
	cd webapp && $(NPM) run i18n-extract -- --out-file src/i18n/en.json --id-interpolation-pattern '[sha512:contenthash:base64:8]' --format simple src/index.tsx src/components/**/*.{ts,tsx}

.PHONY: i18n-extract-server
i18n-extract-server:
	$(GO) install -modfile=go.tools.mod github.com/mattermost/mattermost-utilities/mmgotool
	cd server && $(GOBIN)/mmgotool i18n extract --portal-dir="" --skip-dynamic

## Disable the plugin.
.PHONY: disable
disable: detach
	./build/bin/pluginctl disable $(PLUGIN_ID)

## Enable the plugin.
.PHONY: enable
enable:
	./build/bin/pluginctl enable $(PLUGIN_ID)

## Reset the plugin, effectively disabling and re-enabling it on the server.
.PHONY: reset
reset: detach
	./build/bin/pluginctl reset $(PLUGIN_ID)

## Kill all instances of the plugin, detaching any existing dlv instance.
.PHONY: kill
kill: detach
	$(eval PLUGIN_PID := $(shell ps aux | grep "plugins/${PLUGIN_ID}" | grep -v "grep" | awk -F " " '{print $$2}'))

	@for PID in ${PLUGIN_PID}; do \
		echo "Killing plugin pid $$PID"; \
		kill -9 $$PID; \
	done; \

## Generate mocks for testing
.PHONY: mock
mock:
	$(GO) install github.com/vektra/mockery/v3@v3.2.5
	$(GOBIN)/mockery

## Clean removes all build artifacts.
.PHONY: clean
clean:
	rm -fr dist/
ifneq ($(HAS_SERVER),)
	rm -fr server/coverage.txt
	rm -fr server/dist
endif
ifneq ($(HAS_WEBAPP),)
	rm -fr webapp/junit.xml
	rm -fr webapp/dist
	rm -fr webapp/node_modules
endif
	rm -fr build/bin/

## Fetches the logs for the plugin.
.PHONY: logs
logs:
	./build/bin/pluginctl logs $(PLUGIN_ID)

## Fetches the logs for the plugin and watches for new logs.
.PHONY: logs-watch
logs-watch:
	./build/bin/pluginctl logs-watch $(PLUGIN_ID)

# Help documentation à la https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help:
	@cat Makefile build/*.mk | grep -v '\.PHONY' |  grep -v '\help:' | grep -B1 -E '^[a-zA-Z0-9_.-]+:.*' | sed -e "s/:.*//" | sed -e "s/^## //" |  grep -v '\-\-' | sed '1!G;h;$$!d' | awk 'NR%2{printf "\033[36m%-30s\033[0m",$$0;next;}1' | sort


## Install NPM dependencies for 2e2 tests
e2e/node_modules: e2e/package.json
	cd e2e && $(NPM) install
	touch $@

## Run e2e tests
.PHONY: e2e
e2e: e2e/node_modules
	@MM_DEBUG= $(MAKE) dist
	cd e2e && npx playwright test

## Check and fix copyright/license headers in all files (enterprise directory is excluded)
.PHONY: copyright
copyright: install-go-tools webapp/node_modules
	@echo Checking license headers...
ifneq ($(HAS_SERVER),)
	@echo Fixing Go license headers...
	./scripts/fix_license_headers.sh 2023
endif
ifneq ($(HAS_WEBAPP),)
	@echo Fixing webapp license headers...
	cd webapp && $(NPM) run fix
endif
	@echo License headers have been checked and fixed.

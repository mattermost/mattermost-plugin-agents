# AGENTS.md

## Cursor Cloud specific instructions

### Overview

This is the **Mattermost Agents Plugin** (`mattermost-ai`) — a Go server + React webapp plugin that runs inside a Mattermost server instance. It is NOT a standalone application; `make dist` produces a `.tar.gz` bundle for deployment to a Mattermost server.

### Prerequisites (already installed by the update script)

- **Go 1.25.5** — required by `go.mod`
- **Node.js 20.11** — required by `.nvmrc`; use `nvm use 20.11` if switched away
- Webapp npm dependencies — installed in `webapp/node_modules`
- Go tools — installed in `./bin/` via `make install-go-tools`

### Key commands

All standard build/lint/test commands are documented in `CLAUDE.md` and the `README.md` "Development" section. Key ones:

- `make check-style` — lint (ESLint + golangci-lint + go vet + TypeScript check)
- `make check-style-fix` — lint with auto-fix
- `make test` — Go unit tests + webapp tests
- `make dist` — full build (Go server for all platforms + webapp + tar.gz bundle)
- `make deploy` — build + deploy to a Mattermost server (requires `MM_SERVICESETTINGS_SITEURL`, `MM_ADMIN_USERNAME`, `MM_ADMIN_PASSWORD`)
- `make e2e` — end-to-end tests via Playwright + testcontainers (requires Docker)

### Non-obvious gotchas

- **`mattermost-govet`** may fail with "unsupported version" or "package requires newer Go version" errors. This is a known compatibility issue between the tool (compiled against an older Go stdlib) and Go 1.25. It does not indicate code issues; ESLint, golangci-lint, and `go vet` are the primary lint gates.
- **PostgreSQL integration tests** (`postgres/` package) require a running PostgreSQL instance with pgvector. These fail by default in the Cloud VM since there is no local PostgreSQL; this is expected.
- **MCP and MCPServer tests** require Docker (testcontainers). Without Docker, these tests panic with "rootless Docker not found". These are integration tests and their failures are expected without Docker.
- **Webapp tests** are currently no-ops (`echo ''` in `package.json`).
- The build tools (`build/bin/manifest`, `build/bin/pluginctl`) are auto-compiled from Go source when the Makefile runs; you do not need to install them manually.
- `nvm` is used for Node.js version management. If you get Node.js version errors, run `nvm use 20.11`.

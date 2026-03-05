# AGENTS.md

## Cursor Cloud specific instructions

### Overview

This is the **Mattermost Agents Plugin** (`mattermost-plugin-ai`), a Mattermost server plugin integrating AI/LLM capabilities. It has a Go backend (`server/`) and React/TypeScript webapp (`webapp/`). It is not a standalone app; it runs inside a Mattermost server instance.

### Development Environment

The development environment requires:
- **Go 1.24+** (pre-installed)
- **Node.js 20.11** (from `.nvmrc`; use `source ~/.nvm/nvm.sh && nvm use 20.11`)
- **Docker** for running Mattermost server + PostgreSQL, and for e2e/integration tests (Testcontainers)

### Running Mattermost + Plugin locally

A local Mattermost Enterprise instance is needed for plugin development:

```bash
# Create Docker network
docker network create mm-network 2>/dev/null || true

# Start PostgreSQL
docker run -d --name mm-postgres --network mm-network \
  -e POSTGRES_USER=mmuser -e POSTGRES_PASSWORD=mostest -e POSTGRES_DB=mattermost \
  -p 5432:5432 postgres:15-alpine

# Start Mattermost (MM_LICENSE env var must be set)
docker run -d --name mm-server --network mm-network -p 8065:8065 \
  -e MM_SQLSETTINGS_DRIVERNAME=postgres \
  -e MM_SQLSETTINGS_DATASOURCE="postgres://mmuser:mostest@mm-postgres:5432/mattermost?sslmode=disable&connect_timeout=10" \
  -e MM_SERVICESETTINGS_SITEURL=http://localhost:8065 \
  -e MM_PLUGINSETTINGS_ENABLEUPLOADS=true \
  -e MM_PLUGINSETTINGS_ENABLEUPLOAD=true \
  -e MM_LICENSE="$MM_LICENSE" \
  -e MM_SERVICESETTINGS_ENABLELOCALMODE=true \
  mattermost/mattermost-enterprise-edition:latest

# Create admin user (first time only)
docker exec mm-server mmctl user create --email admin@example.com --username admin --password 'Admin1234!' --system-admin --local
docker exec mm-server mmctl team create --name dev --display-name "Dev Team" --local
docker exec mm-server mmctl team users add dev admin --local
```

### Deploying the plugin

```bash
MM_SERVICESETTINGS_SITEURL=http://localhost:8065 MM_ADMIN_USERNAME=admin MM_ADMIN_PASSWORD='Admin1234!' make deploy
```

This builds the Go server (all platforms), the webapp (webpack), bundles into a `.tar.gz`, and uploads to the running Mattermost instance.

### Key commands

See `CLAUDE.md` and `README.md` for standard build/lint/test commands. Summary:
- **Build & deploy**: `make deploy` (needs `MM_SERVICESETTINGS_SITEURL`, `MM_ADMIN_USERNAME`, `MM_ADMIN_PASSWORD`)
- **Lint**: `make check-style` or `make check-style-fix`
- **Test**: `make test` (Go unit tests + webapp tests)
- **E2E tests**: `make e2e` (uses Testcontainers/Docker - fully self-contained)
- **Build only**: `make dist`

### Gotchas

- The `mattermost-govet` tool (used in `make check-style`) may fail with Go version mismatch errors. This is a known tooling issue, not a code problem. The core linting (golangci-lint, ESLint, TypeScript checks) all pass.
- `postgres/pgvector_test.go` tests require a local PostgreSQL with pgvector at `localhost:5432` (`mmuser:mostest`). They will fail without it. The Mattermost docker container's PostgreSQL satisfies this.
- Webapp tests (`npm run test` in `webapp/`) are currently no-ops (`echo ''`).
- The plugin config uses a custom settings schema. Configuration is done through the **System Console > Plugins > Agents** UI or via `PATCH /api/v4/config/patch` API. The config is stored under `PluginSettings.Plugins["mattermost-ai"]["config"]` as a JSON object (not string).
- When configuring via API, the `config` value must be a JSON object, not a JSON-encoded string. Passing a string causes `LoadPluginConfiguration` to fail.
- The `MM_LICENSE` environment variable must be set for the Mattermost Enterprise server to function properly.
- To find the Mattermost server port: run `wt port` or check Docker port mappings.

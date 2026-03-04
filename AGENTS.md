# AGENTS.md

## Cursor Cloud specific instructions

### Overview

This is the **Mattermost Agents Plugin** (`mattermost-ai`) — a Go server + React webapp plugin that runs inside a Mattermost server instance. It is NOT a standalone application; `make dist` produces a `.tar.gz` bundle for deployment to a Mattermost server.

### Prerequisites (installed by the update script)

- **Go 1.25.5** — required by `go.mod`
- **Node.js 20.11** — required by `.nvmrc`; use `nvm use 20.11` if switched
- Webapp npm dependencies — `webapp/node_modules`
- Go tools — `./bin/` via `make install-go-tools`

### Key commands

Standard build/lint/test commands are in `CLAUDE.md` and `README.md`. Key ones:

- `make check-style` — lint (ESLint + golangci-lint + go vet + mattermost-govet + TypeScript check)
- `make check-style-fix` — lint with auto-fix
- `make test` — Go unit + integration tests + webapp tests
- `make dist` — full build (Go server all platforms + webapp + tar.gz bundle)
- `make deploy` — build + deploy to Mattermost (requires `MM_SERVICESETTINGS_SITEURL`, `MM_ADMIN_USERNAME`, `MM_ADMIN_PASSWORD`)
- `make e2e` — Playwright e2e tests via testcontainers (requires Docker)

### Running Mattermost locally with Docker

To test the plugin end-to-end, run Mattermost + PostgreSQL/pgvector in Docker:

```bash
# Start Docker daemon (required in Cloud VM)
sudo dockerd &>/tmp/dockerd.log &
sudo chmod 666 /var/run/docker.sock

# Create network
sudo docker network create mm-net

# Start PostgreSQL with pgvector (password "mostest" matches test expectations)
sudo docker run -d --name mm-postgres --network mm-net \
  -e POSTGRES_DB=mattermost -e POSTGRES_USER=mmuser -e POSTGRES_PASSWORD=mostest \
  -p 5432:5432 pgvector/pgvector:pg15

# Start Mattermost
sudo docker run -d --name mattermost --network mm-net \
  -e MM_SQLSETTINGS_DRIVERNAME=postgres \
  -e "MM_SQLSETTINGS_DATASOURCE=postgres://mmuser:mostest@mm-postgres:5432/mattermost?sslmode=disable&connect_timeout=10" \
  -e MM_SERVICESETTINGS_SITEURL=http://localhost:8065 \
  -e MM_SERVICESETTINGS_ENABLELOCALMODE=true \
  -e MM_PLUGINSETTINGS_ENABLEUPLOADS=true \
  -e MM_SERVICESETTINGS_ENABLEDEVELOPER=true \
  -e MM_TEAMSETTINGS_ENABLEOPENSERVER=true \
  -p 8065:8065 mattermost/mattermost-enterprise-edition:latest

# Create admin user and team
sudo docker exec mattermost mmctl user create --email admin@test.com --username admin --password 'Admin1234!' --system-admin --local
sudo docker exec mattermost mmctl team create --name test-team --display-name "Test Team" --local
sudo docker exec mattermost mmctl team users add test-team admin --local

# Deploy plugin
make dist
sudo docker cp dist/mattermost-ai-*.tar.gz mattermost:/tmp/plugin.tar.gz
sudo docker exec mattermost mmctl plugin add /tmp/plugin.tar.gz --local
sudo docker exec mattermost mmctl plugin enable mattermost-ai --local
```

Mattermost UI is then available at http://localhost:8065 (admin / Admin1234!).

### Non-obvious gotchas

- **PostgreSQL test credentials**: The `postgres/` integration tests expect `postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable`. Use password `mostest` when starting the pgvector container.
- **Docker socket permissions**: After starting `dockerd`, run `sudo chmod 666 /var/run/docker.sock` so non-root Go tests (MCP/MCPServer testcontainers) can access Docker.
- **Webapp tests** are currently no-ops (`echo ''` in `package.json`).
- Build tools (`build/bin/manifest`, `build/bin/pluginctl`) are auto-compiled from Go source when the Makefile runs.
- `nvm` is used for Node.js version management. If Node.js version errors occur, run `. ~/.nvm/nvm.sh && nvm use 20.11`.

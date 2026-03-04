# AGENTS.md

## Cursor Cloud specific instructions

### Overview

This is the **Mattermost Agents Plugin** (`mattermost-ai`) — a Go server + React webapp plugin that runs inside a Mattermost server instance. `make dist` produces a `.tar.gz` bundle for deployment.

### What the update script automates

The update script handles all of the following on every VM startup:

1. **Node.js 20.11** via nvm (matches `.nvmrc`)
2. **Webapp npm dependencies** (`webapp/node_modules`)
3. **Go tools** (`golangci-lint`, `gotestsum`, `mattermost-govet`) in `./bin/`
4. **Docker daemon** start + socket permissions
5. **PostgreSQL + pgvector** container (`mm-postgres`, port 5432, user `mmuser`, password `mostest`)
6. **Mattermost server** container (`mattermost`, port 8065, with local mode + plugin uploads enabled)
7. **Admin user** (`admin` / `Admin1234!`) + **Test Team** creation

After the update script runs, the environment is ready for `make check-style`, `make test`, `make dist`, and plugin deployment.

### Services available after startup

| Service | URL / Address | Credentials |
|---|---|---|
| Mattermost | http://localhost:8065 | `admin` / `Admin1234!` |
| PostgreSQL (pgvector) | `localhost:5432` | `mmuser` / `mostest` |

### Key commands

Standard commands are in `CLAUDE.md` and `README.md`. Summary:

- `make check-style` — lint (ESLint + golangci-lint + go vet + mattermost-govet + TypeScript)
- `make check-style-fix` — lint with auto-fix
- `make test` — all Go unit/integration tests + webapp tests
- `make dist` — full build (Go server all platforms + webapp + tar.gz bundle)
- `make e2e` — Playwright e2e tests via testcontainers (requires Docker, already running)

### Deploying the plugin to the local Mattermost

```bash
make dist
sudo docker cp dist/mattermost-ai-*.tar.gz mattermost:/tmp/plugin.tar.gz
sudo docker exec mattermost mmctl plugin add /tmp/plugin.tar.gz --local
sudo docker exec mattermost mmctl plugin enable mattermost-ai --local
```

### Non-obvious gotchas

- **PostgreSQL test credentials**: Integration tests in `postgres/` expect `postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable`. The update script starts pgvector with these exact credentials.
- **Docker socket**: The update script runs `sudo chmod 666 /var/run/docker.sock` so Go testcontainers (MCP/MCPServer tests) can access Docker without sudo.
- **Webapp tests** are currently no-ops (`echo ''` in `package.json`).
- **Build tools** (`build/bin/manifest`, `build/bin/pluginctl`) auto-compile from Go source when the Makefile runs.
- **Plugin deployment is NOT automated** by the update script since it requires a successful build, which depends on the current code state. Deploy manually after `make dist`.

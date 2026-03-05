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

### Configuring an Anthropic AI agent via API

After deploying the plugin and starting the Mattermost server, configure an Anthropic service and agent using the `ANTHROPIC_API_KEY` environment variable (must be set as a secret). Authenticate first, then patch the plugin config:

```bash
# Get auth token
TOKEN=$(curl -s -X POST http://localhost:8065/api/v4/users/login \
  -H 'Content-Type: application/json' \
  -d '{"login_id":"admin","password":"Admin1234!"}' \
  -D - 2>/dev/null | grep -i '^token:' | tr -d '\r' | awk '{print $2}')

# Patch plugin config with Anthropic service + agent (reads key from env var)
python3 -c "
import json, os
patch = {
    'PluginSettings': {
        'Plugins': {
            'mattermost-ai': {
                'config': {
                    'services': [{
                        'id': 'anthropic-1',
                        'name': 'Anthropic Claude',
                        'type': 'anthropic',
                        'apiKey': os.environ['ANTHROPIC_API_KEY'],
                        'defaultModel': 'claude-sonnet-4-20250514',
                        'tokenLimit': 200000,
                        'outputTokenLimit': 8192,
                        'streamingTimeoutSeconds': 60
                    }],
                    'bots': [{
                        'id': 'claude-bot-1',
                        'name': 'claude',
                        'displayName': 'Claude',
                        'customInstructions': 'You are a helpful AI assistant.',
                        'serviceID': 'anthropic-1',
                        'channelAccessLevel': 0,
                        'userAccessLevel': 0,
                        'enableVision': True,
                        'disableTools': False
                    }],
                    'defaultBotName': 'claude'
                }
            }
        }
    }
}
print(json.dumps(patch))
" | curl -s -X PUT http://localhost:8065/api/v4/config/patch \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d @-
```

After this, the `@claude` bot is available for @mentions in channels, direct messages, and the Agents RHS panel (purple icon in the right sidebar). The config structure is defined in `llm/configuration.go` (`ServiceConfig`, `BotConfig`).

### Gotchas

- The `mattermost-govet` tool (used in `make check-style`) may fail with Go version mismatch errors. This is a known tooling issue, not a code problem. The core linting (golangci-lint, ESLint, TypeScript checks) all pass.
- `postgres/pgvector_test.go` tests require a local PostgreSQL with pgvector at `localhost:5432` (`mmuser:mostest`). They will fail without it. The Mattermost docker container's PostgreSQL satisfies this.
- Webapp tests (`npm run test` in `webapp/`) are currently no-ops (`echo ''`).
- The plugin config uses a custom settings schema. Configuration is done through the **System Console > Plugins > Agents** UI or via `PATCH /api/v4/config/patch` API. The config is stored under `PluginSettings.Plugins["mattermost-ai"]["config"]` as a JSON object (not string).
- When configuring via API, the `config` value must be a JSON object, not a JSON-encoded string. Passing a string causes `LoadPluginConfiguration` to fail.
- The `MM_LICENSE` environment variable must be set for the Mattermost Enterprise server to function properly.
- To find the Mattermost server port: run `wt port` or check Docker port mappings.

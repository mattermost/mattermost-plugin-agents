# AGENTS.cloud.md

## Cursor Cloud specific instructions

### Overview

This is the **Mattermost Agents Plugin** (`mattermost-ai`), a Mattermost server plugin integrating AI/LLM capabilities. It has a Go backend (`server/`) and React/TypeScript webapp (`webapp/`). It is not a standalone app; it runs inside a Mattermost server instance.

### Development Environment

Repeatable bootstrap actions belong in the Cursor Cloud VM update script, not in this file. Keep that script responsible for installing or refreshing Docker, Node.js, npm dependencies, `agent-browser`, AWS CLI, and Playwright Chromium dependencies.

The development environment uses:
- Go 1.24+.
- Node.js 24.11+.
- Docker for Mattermost, PostgreSQL, and e2e/integration tests.
- `agent-browser` for browser automation in agents that do not have desktop access.
- AWS CLI for uploading non-sensitive reproduction artifacts to `AWS_S3_BUCKET_NAME`.

### Running Mattermost + Plugin locally

A local Mattermost Enterprise instance is needed for plugin development. Use the development image and a companion PostgreSQL container:

```bash
docker network create mm-network 2>/dev/null || true

docker run -d --name mm-postgres --network mm-network \
  -e POSTGRES_USER=mmuser -e POSTGRES_PASSWORD=mostest -e POSTGRES_DB=mattermost \
  -p 5432:5432 postgres:15-alpine

docker run -d --name mm-server --network mm-network -p 8065:8065 \
  -e MM_SQLSETTINGS_DRIVERNAME=postgres \
  -e MM_SQLSETTINGS_DATASOURCE="postgres://mmuser:mostest@mm-postgres:5432/mattermost?sslmode=disable&connect_timeout=10" \
  -e MM_SERVICESETTINGS_SITEURL=http://localhost:8065 \
  -e MM_PLUGINSETTINGS_ENABLEUPLOADS=true \
  -e MM_PLUGINSETTINGS_ENABLEUPLOAD=true \
  -e MM_PLUGINSETTINGS_AUTOMATICPREPACKAGEDPLUGINS=false \
  -e MM_PLUGINSETTINGS_ENABLEMARKETPLACE=false \
  -e MM_SERVICESETTINGS_ENABLELOCALMODE=true \
  mattermostdevelopment/mattermost-enterprise-edition:master

docker exec mm-server mmctl user create --email admin@example.com --username admin --password 'Admin1234!' --system-admin --local
docker exec mm-server mmctl team create --name dev --display-name "Dev Team" --local
docker exec mm-server mmctl team users add dev admin --local
```

### Deploying the plugin

```bash
rm -rf server/dist dist
make dist-ci
MM_SERVICESETTINGS_SITEURL=http://localhost:8065 MM_ADMIN_USERNAME=admin MM_ADMIN_PASSWORD='Admin1234!' ./build/bin/pluginctl deploy mattermost-ai dist/*.tar.gz
```

`make dist-ci` builds a Linux amd64 bundle for the local container. Clean `server/dist` first if a previous full build left other platform binaries behind.

### Key commands

See `CLAUDE.md` and `README.md` for standard build/lint/test commands. Summary:
- **Build & deploy locally**: `rm -rf server/dist dist && make dist-ci && MM_SERVICESETTINGS_SITEURL=http://localhost:8065 MM_ADMIN_USERNAME=admin MM_ADMIN_PASSWORD='Admin1234!' ./build/bin/pluginctl deploy mattermost-ai dist/*.tar.gz`
- **Lint**: `make check-style` or `make check-style-fix`
- **Test**: `make test` (Go unit tests + webapp tests)
- **E2E tests**: `make e2e` (uses Testcontainers/Docker - fully self-contained)
- **Focused e2e test**: `cd e2e && npx playwright test tests/path/to/spec.ts --reporter=list` (Chromium + Firefox pre-installed)
- **Chromium-only e2e**: `cd e2e && npx playwright test tests/path/to/spec.ts --project=chromium --reporter=list`
- **Build only**: `make dist`

**E2E prerequisites:** npm dependencies and Playwright Chromium dependencies should be installed by the update script. No bootstrap steps should be needed before running Chromium e2e commands.

### Configuring an Anthropic AI agent via API

After deploying the plugin and starting Mattermost, configure an Anthropic service and create a user agent using the `ANTHROPIC_API_KEY` environment variable. Do not use `/api/v4/config/patch` for this plugin after config migration; the runtime source of truth is the plugin admin config endpoint.

```bash
TOKEN=$(curl -s -X POST http://localhost:8065/api/v4/users/login \
  -H 'Content-Type: application/json' \
  -d '{"login_id":"admin","password":"Admin1234!"}' \
  -D /tmp/mm-login-headers -o /tmp/mm-login-body)
TOKEN=$(awk 'tolower($1)=="token:" {gsub("\r", "", $2); print $2}' /tmp/mm-login-headers)

curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8065/plugins/mattermost-ai/admin/config |
  jq --arg key "$ANTHROPIC_API_KEY" '
    .services=[{
      id:"anthropic-service",
      name:"Anthropic",
      type:"anthropic",
      apiKey:$key,
      defaultModel:"claude-sonnet-4-5-20250929",
      tokenLimit:200000,
      outputTokenLimit:4096,
      streamingTimeoutSeconds:120
    }] |
    .bots=[] |
    .defaultBotName="" |
    .enableChannelMentionToolCalling=true |
    .mcp.enabled=true |
    .mcp.embeddedServer.enabled=true |
    .mcp.enablePluginServer=true |
    .mcp.servers=[] |
    .mcp.idleTimeoutMinutes=30' >/tmp/agents-config.json

curl -s -X PUT http://localhost:8065/plugins/mattermost-ai/admin/config \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @/tmp/agents-config.json

curl -s -X POST http://localhost:8065/plugins/mattermost-ai/agents \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"displayName":"Claude Agent","username":"claudeagent","serviceID":"anthropic-service","customInstructions":"You are a concise helpful assistant.","channelAccessLevel":0,"userAccessLevel":0,"model":"claude-sonnet-4-5-20250929","enableVision":true,"disableTools":true}'
```

After this, the `@claudeagent` bot is available for direct messages and mentions. The config structure is defined in `config/config.go` and `llm/configuration.go`; the agent API request shape is defined by `CreateAgentRequest` in `api/api_agents.go`.

### Embedded MCP Server

The embedded MCP server provides tool-calling capabilities, allowing the AI agent to interact with the Mattermost API (read channels, create posts, search users, etc.) with an explicit user accept/reject approval flow.

**Configuration fields** (under `config.mcp`):
- `mcp.enabled` — enables MCP admin tools discovery API
- `mcp.enablePluginServer` — enables the HTTP MCP endpoint for external clients
- `mcp.embeddedServer.enabled` — enables the in-process MCP server with Mattermost tools
- `enableChannelMentionToolCalling` — allows tool use when the bot is @mentioned in channels (not just DMs)

Embedded MCP tools are defined in `mcpserver/tools/`. Verify the active tool list with `GET /plugins/mattermost-ai/admin/mcp/tools` as a system admin.

**How tool calls work:**
1. The user sends a message requesting an action (e.g., "create a post in Town Square")
2. Claude determines which MCP tools to call and presents them with parameters in the RHS
3. The user sees Accept/Reject buttons for each tool call and must explicitly approve
4. After approval, the tool executes against the Mattermost API using the user's session
5. Results are returned to Claude, which summarizes the outcome

**Key notes:**
- Tools work in both DMs with the bot and channel @mentions (when `enableChannelMentionToolCalling` is true)
- The embedded server requires `SiteURL` to be configured on the Mattermost server
- Tool name conflicts across MCP servers: first server wins, duplicates are skipped with a warning
- The embedded MCP server uses in-memory transport (no HTTP), initialized at plugin startup
- Verify tools are discoverable: `GET /plugins/mattermost-ai/admin/mcp/tools` (requires admin auth)

### Uploading PR artifacts to S3

**All screenshots and walkthrough videos captured during development MUST be uploaded to S3 and linked in the Pull Request description.** This gives reviewers visual evidence of changes. Never include secrets, credentials, or API keys in screenshots or videos.

Required env vars: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_S3_BUCKET_NAME` (set as secrets).

**Upload artifacts and generate embeddable PR references:**

```bash
BRANCH=$(git rev-parse --abbrev-ref HEAD)
PREFIX="cursor/$BRANCH"
aws s3 cp /opt/cursor/artifacts/ "s3://$AWS_S3_BUCKET_NAME/$PREFIX/" --recursive
REGION=$(aws s3api get-bucket-location --bucket "$AWS_S3_BUCKET_NAME" --output text)
[ "$REGION" = "None" ] || [ "$REGION" = "null" ] && REGION=us-east-1
for f in /opt/cursor/artifacts/*; do
  FILENAME=$(basename "$f")
  if [ "$REGION" = "us-east-1" ]; then
    URL="https://$AWS_S3_BUCKET_NAME.s3.amazonaws.com/$PREFIX/$FILENAME"
  else
    URL="https://$AWS_S3_BUCKET_NAME.s3.$REGION.amazonaws.com/$PREFIX/$FILENAME"
  fi
  case "$FILENAME" in
    *.gif|*.jpg|*.jpeg|*.png|*.webp) echo "![${FILENAME}](${URL})" ;;
    *) echo "[${FILENAME}](${URL})" ;;
  esac
done
```

**Including in the PR description:**

After uploading, add screenshot artifacts to the PR description as Markdown images (`![alt text](public-url)`) so they render inline. Use plain links only for non-image artifacts. Never include secrets, credentials, API keys, or screenshots that show them.

**Rules:**
- Upload relevant artifacts before creating/updating the PR description.
- Scrub any visible secrets from screenshots/videos before uploading. If a secret is visible, retake the screenshot with the secret obscured or redacted.

### Gotchas

- The `mattermost-govet` tool (used in `make check-style`) may fail with Go version mismatch errors. This is a known tooling issue, not a code problem. The core linting (golangci-lint, ESLint, TypeScript checks) all pass.
- `postgres/pgvector_test.go` tests require a local PostgreSQL with pgvector at `localhost:5432` (`mmuser:mostest`). They will fail without it. The Mattermost docker container's PostgreSQL satisfies this.
- Webapp tests (`npm run test` in `webapp/`) are currently no-ops (`echo ''`).
- The plugin config is migrated to the plugin database on activation. Use `GET`/`PUT /plugins/mattermost-ai/admin/config` for automation.
- `make deploy` builds all platform binaries and can exceed the local upload limit. Prefer the cleaned `make dist-ci` workflow for container testing.
- The development image provides an Entry license for local testing; `MM_LICENSE` is not required for the bootstrap used here.
- To find the Mattermost server port, check Docker port mappings.

# Cursor Cloud supplement

The rules below apply only when running inside a Cursor Cloud VM. The
cloud VM update script appends this file to `AGENTS.md` at provisioning
time, so the rest of the agent context is the same as a local checkout.
Local developers and other agents should ignore this section — it is not
relevant outside the cloud VM. Anything that already lives in `AGENTS.md`
must not be repeated here; only cloud-VM-specific bootstrap, credentials,
and artifact-handling rules belong below.

## Cloud VM tooling

Repeatable installs (Docker, Node, npm dependencies, `agent-browser`,
AWS CLI, Playwright Chromium) belong in the Cursor Cloud VM update
script, not in this file. The cloud image additionally provides:

- `agent-browser` — browser automation for agents without a desktop.
- AWS CLI — uploads non-sensitive reproduction artifacts to
  `AWS_S3_BUCKET_NAME`.

## Running Mattermost + plugin locally in the cloud VM

A local Mattermost Enterprise instance is needed for plugin development.
Use the development image and a companion PostgreSQL container:

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

The development image provides an Entry license for local testing;
`MM_LICENSE` is not required.

## Deploying the plugin into the cloud VM container

```bash
rm -rf server/dist dist
make dist-ci
MM_SERVICESETTINGS_SITEURL=http://localhost:8065 \
  MM_ADMIN_USERNAME=admin MM_ADMIN_PASSWORD='Admin1234!' \
  ./build/bin/pluginctl deploy mattermost-ai dist/*.tar.gz
```

Always clean `server/dist` first — a previous full `make dist` may have
left other-platform binaries that bloat the upload.

## Configuring an Anthropic agent via API

After deploying the plugin and starting Mattermost, configure an
Anthropic service and create a user agent using the `ANTHROPIC_API_KEY`
environment variable. Do not use `/api/v4/config/patch` for plugin
config — write to `PUT /plugins/mattermost-ai/admin/config`.

```bash
curl -s -X POST http://localhost:8065/api/v4/users/login \
  -H 'Content-Type: application/json' \
  -d '{"login_id":"admin","password":"Admin1234!"}' \
  -D /tmp/mm-login-headers -o /tmp/mm-login-body >/dev/null
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

After this, `@claudeagent` is available for DMs and mentions. Config
schema lives in `config/config.go` and `llm/configuration.go`; the agent
API request shape is `CreateAgentRequest` in `api/api_agents.go`.

Verify embedded MCP tools are discoverable:
`GET /plugins/mattermost-ai/admin/mcp/tools` (admin auth required).

## Uploading PR artifacts to S3

All screenshots and walkthrough videos captured during development must
be uploaded to S3 and linked in the PR description so reviewers see
visual evidence of changes. Never include secrets, credentials, or API
keys in screenshots or videos — retake or redact instead.

Required env vars (provided as cloud secrets): `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, `AWS_S3_BUCKET_NAME`.

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

Add image artifacts to the PR description as Markdown images
(`![alt](url)`) so they render inline. Use plain links for non-image
artifacts. Upload before creating or updating the PR description.

## Cloud-VM-specific gotchas

- The Mattermost server port comes from the Docker port mapping above
  (`8065`); if you re-run with a different `-p` flag, look it up with
  `docker port mm-server`.
- Outbound LLM traffic from the container respects `HTTP_PROXY` /
  `HTTPS_PROXY` on the Mattermost process environment if the cloud VM
  routes egress through a proxy.

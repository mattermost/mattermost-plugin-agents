---
name: agent-browser
description: Use agent-browser for headless UI validation, browser navigation, screenshots, and artifact capture when a desktop browser is unavailable.
---

# Agent Browser UI Validation

Use `agent-browser` when a task needs browser-based validation from a terminal-only environment. It is useful for Cloud Agents that cannot access a desktop browser but still need to inspect a running web application and capture screenshots.

## Setup

Install and verify the CLI:

- `source ~/.nvm/nvm.sh && nvm use 24.11`
- `npm install -g agent-browser`
- `agent-browser install --with-deps`
- `agent-browser doctor`

Load the version-matched CLI guidance before complex workflows:

- `agent-browser skills get core --full`

## Basic workflow

Use an isolated session name for each validation run:

- `agent-browser --session mattermost-ui open http://localhost:8065`
- `agent-browser --session mattermost-ui wait 2000`
- `agent-browser --session mattermost-ui snapshot -i`
- `agent-browser --session mattermost-ui screenshot /opt/cursor/artifacts/mattermost.png`
- `agent-browser --session mattermost-ui close`

Prefer `snapshot -i` to discover stable interactive refs before clicking or filling forms. Interact with refs from the snapshot output, for example:

- `agent-browser --session mattermost-ui fill @e1 admin`
- `agent-browser --session mattermost-ui fill @e2 "$MM_ADMIN_PASSWORD"`
- `agent-browser --session mattermost-ui click @e3`

## Mattermost local validation

For this repository, the local Mattermost server usually runs at `http://localhost:8065` after Docker startup and plugin deployment. A standard validation should:

1. Open the local Mattermost URL.
2. Log in with the local development admin account, or reuse a named `agent-browser` session after login.
3. Navigate to `/admin_console/plugins/plugin_mattermost-ai`.
4. Confirm the Agents plugin page loads and the plugin is enabled.
5. Capture a screenshot under `/opt/cursor/artifacts/`.

Avoid capturing screenshots while credentials or API keys are visible. Start final screenshots after login whenever possible.

## S3 artifact upload

When AWS credentials and `AWS_S3_BUCKET_NAME` are available, upload screenshots with the AWS CLI:

- `BRANCH=$(git rev-parse --abbrev-ref HEAD)`
- `aws s3 cp /opt/cursor/artifacts/mattermost.png "s3://$AWS_S3_BUCKET_NAME/$BRANCH/mattermost.png"`
- `aws s3 presign "s3://$AWS_S3_BUCKET_NAME/$BRANCH/mattermost.png" --expires-in 604800`

If the bucket policy makes objects public, a stable public URL is usually:

- `https://$AWS_S3_BUCKET_NAME.s3.amazonaws.com/$BRANCH/mattermost.png`

Prefer the stable public URL when the bucket policy allows it; otherwise provide the presigned URL.

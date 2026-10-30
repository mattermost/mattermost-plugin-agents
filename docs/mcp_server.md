<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Mattermost MCP Server

The Mattermost MCP server is built into the Mattermost Agents plugin. It lets MCP clients such as Claude Code, Claude Desktop, Cursor, and VS Code read Mattermost, search it, and post to it as the signed-in user, over streamable HTTP at `/plugins/mattermost-ai/mcp-server/mcp`. No separate server or binary is needed.

This page is for people connecting an external AI client to Mattermost. To let agents inside Mattermost use MCP tools, see [Use Mattermost Agents as your personal AI assistant](personal_agent.md) and the [Admin Guide](admin_guide.md#model-context-protocol-mcp-integration).

## What it is

The Agents plugin includes an embedded MCP server that its own agents use for Mattermost tools. When an admin turns on the HTTP endpoint, the same tools are available to any MCP client that can reach your Mattermost server and authenticate as a Mattermost user.

- **Endpoint:** `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp`
- **Transport:** streamable HTTP. Server-Sent Events (SSE) transport is not supported.
- **Identity:** each request runs as the Mattermost user who authenticated. The client sees only what that user can see and can do only what that user can do.
- **Tools:** about 120 tools for posts, channels, teams, users, files, search, reactions, bookmarks, scheduled posts, and more. See [Available tools](#available-tools).

## Why use the official Mattermost MCP server

- **Runs inside Mattermost.** There is no extra service to deploy, secure, or keep running. It is installed and upgraded with the Agents plugin.
- **Uses each user's own permissions.** Tools call Mattermost with the authenticated user's session, so channel membership, team membership, and role permissions apply to every call.
- **OAuth with dynamic client registration.** Clients that support MCP OAuth can register themselves and sign the user in through Mattermost, so users do not create or paste tokens. Personal access tokens are also supported for clients that need a static credential.
- **Admin controls.** Admins decide whether the endpoint is on, which users may use the built-in and plugin servers (access policies at Enterprise Advanced), and which tools from plugin-registered MCP servers are exposed.
- **Audit logging.** Each external client's first use of a session is recorded in the Mattermost server audit log as an `mcpSessionGrant` event with the actor and trace ID.
- **Maintained with Mattermost.** The tool set is part of the Agents plugin and is versioned and released with it.
- **Broad coverage.** More than 100 tools, roughly 70 read-only and 45 that change state, cover most day-to-day Mattermost activity. Posts it creates are marked as AI-generated.

## Requirements

- Mattermost Server v12.0 or later, which is the minimum version for the Agents plugin.
- The Agents plugin installed and enabled.
- **Site URL** set under **System Console > Environment > Web Server**. The plugin does not start without it, and the OAuth discovery response that clients use to sign in is built from it.
- A Mattermost Enterprise license or higher to turn on the HTTP endpoint. See [License requirements](#license-requirements).
- Mattermost reachable from the MCP client. Clients that run on a vendor's cloud, such as the Claude web app, connect from the vendor's network, so a Mattermost server on a private network must be reachable from it.

## Enable the endpoint

1. Go to **Agents > Settings > MCP servers**.
2. On the **Configuration** tab, set **Enable Mattermost MCP Server (HTTP)** to **True**.
3. Select **Save**.

The setting takes effect without restarting the server. The endpoint is then available at:

```text
https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp
```

Replace `your-mattermost-server` with your Site URL. The embedded server that Mattermost agents use is always on and does not depend on this setting.

## Authentication

A request with no Mattermost credentials gets `401 Unauthorized` with a `WWW-Authenticate` header that points to the OAuth protected resource metadata at `/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource`. MCP clients use that response to start sign-in.

### OAuth 2.0

OAuth is the recommended method. The user signs in through Mattermost in a browser and approves the client. No token is copied or stored by hand.

Prerequisites, set in **System Console > Integrations > Integration Management**:

- **Enable OAuth 2.0 Service Provider** set to **True**.
- For clients that register themselves, **Enable Dynamic Client Registration** set to **True**. Dynamic client registration (RFC 7591) uses a public endpoint that does not require authentication, so anyone who can reach the server can register an OAuth client. See the [Mattermost OAuth 2.0 documentation](https://docs.mattermost.com/developers/integrate/apps/authentication/oauth2) before enabling it.

If you do not want to enable dynamic client registration, create an OAuth application under **Product menu > Integrations > OAuth 2.0 Applications** and enter its client ID in a client that supports pre-registered credentials.

Discovery endpoints:

- Protected resource metadata: `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource`
- Authorization server metadata: `https://your-mattermost-server/.well-known/oauth-authorization-server`

### Personal access tokens

For clients that cannot do OAuth, create a personal access token under **Profile > Security > Personal Access Tokens** and send it as a bearer token:

```text
Authorization: Bearer <your-token>
```

A personal access token acts with that user's permissions. Treat it like a password, and prefer OAuth where the client supports it. An admin must set **Enable Personal Access Tokens** to **True** under **System Console > Integrations > Integration Management**, and by default only system admins can create tokens unless an admin grants other users that permission.

## Connect a client

Replace `https://your-mattermost-server` with your Mattermost Site URL in every example. Client menus and file formats change, so check the client's own documentation if a step does not match.

### Claude Code

Add the server over HTTP and sign in with OAuth:

```bash
claude mcp add --transport http mattermost https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp
```

Then run `/mcp` inside Claude Code, select **mattermost**, and follow the browser prompts to sign in.

To use a personal access token instead:

```bash
claude mcp add --transport http mattermost https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp \
  --header "Authorization: Bearer $MM_TOKEN"
```

By default the server is added at local scope, for you in the current project. Add `--scope user` to use it in all projects, or `--scope project` to write it to a shared `.mcp.json`. Do not commit a token to `.mcp.json`.

### Claude Desktop and claude.ai

Use a custom connector.

1. Go to **Customize > Connectors**.
2. Select **+ Add**, then **Add custom connector**.
3. Enter a name and the server URL `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp`, then select **Continue**.
4. Under **Authentication**, choose a sign-in option. Under **OAuth client**, choose **Register automatically** if dynamic client registration is enabled on your Mattermost server.
5. Select **Add**, then select **Connect** on the connector and sign in to Mattermost.

On Team and Enterprise plans, an organization owner adds the connector under **Organization settings > Connectors**, and members then connect to it. Claude connects from Anthropic's cloud, so your Mattermost server must be reachable from it.

### Cursor

Add the server to `~/.cursor/mcp.json` for all projects or `.cursor/mcp.json` for one project:

```json
{
  "mcpServers": {
    "mattermost": {
      "url": "https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp"
    }
  }
}
```

Cursor reads the OAuth metadata from the server and starts the sign-in the first time it connects. To use a personal access token, add a `headers` object and read the token from an environment variable:

```json
{
  "mcpServers": {
    "mattermost": {
      "url": "https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp",
      "headers": {
        "Authorization": "Bearer ${env:MM_TOKEN}"
      }
    }
  }
}
```

### VS Code and GitHub Copilot

Add the server to `.vscode/mcp.json` in your workspace. With only a URL, VS Code reads the OAuth metadata from the server and signs you in through Mattermost:

```json
{
  "servers": {
    "mattermost": {
      "type": "http",
      "url": "https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp"
    }
  }
}
```

To use a personal access token instead, this example prompts for the token and stores it securely rather than writing it to the file. Input variables are resolved by VS Code itself, so prefer the OAuth form above for any agent that runs outside the VS Code session:

```json
{
  "inputs": [
    {
      "type": "promptString",
      "id": "mattermost-token",
      "description": "Mattermost personal access token",
      "password": true
    }
  ],
  "servers": {
    "mattermost": {
      "type": "http",
      "url": "https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp",
      "headers": {
        "Authorization": "Bearer ${input:mattermost-token}"
      }
    }
  }
}
```

You can also run **MCP: Add Server** from the Command Palette and choose the HTTP option.

### Other clients

Any client that supports remote MCP servers over streamable HTTP can connect. Give it the endpoint URL and either let it run the OAuth flow or configure a bearer token header. Clients that only support SSE or stdio cannot connect to this endpoint.

## Available tools

The server exposes tools in these areas:

- **Posts and threads:** read, search, create, reply, edit, delete, pin, save, thread and mention lookups
- **Channels:** find, read, create, update, archive, members, bookmarks, unread state
- **Teams, users, and groups:** look up and search people and teams, membership and roles
- **Direct and group messages:** send a DM or group message
- **Scheduled posts and reminders:** create, update, and list scheduled posts, set post reminders
- **Files:** file info, post attachments, file search, and reading file contents
- **Search:** keyword search, plus semantic search when it is configured on the server
- **Agents:** list the Mattermost agents available to the user

State-changing tools are marked as such in the tool metadata and need an Enterprise license. The full list with descriptions is in [Available Tools](admin_guide.md#available-tools) in the Admin Guide.

Some tools take local file paths or URLs, for example to upload a file or attach a file to a post. Those options are available only to the standalone development server, not over HTTP. To attach content to a post over HTTP, pass text files inline in the `files` parameter of `create_post`, `dm`, or `group_message`.

### Plugin-registered tools

Compatible Mattermost plugins can register MCP servers with Agents. When a plugin marks its server for external exposure and an admin enables that server in the **Tools** tab, its enabled tools also appear on this endpoint. Per-tool admin policy applies. If a plugin tool has the same name as a built-in tool or another plugin's tool, it is skipped and a message is logged. See [Plugin-registered MCP servers](admin_guide.md#plugin-registered-mcp-servers).

### Access policies

At Enterprise Advanced, attribute-based access policies on the built-in server and on plugin servers control which users see those tools, on this endpoint as well as in the product. See [Attribute-based access control (ABAC)](admin_guide.md#attribute-based-access-control-abac).

## Approval and safety

The MCP client decides how to confirm tool calls. Most MCP clients ask the user before running a tool unless the user has chosen to auto-approve it. Tool approval policies configured in Mattermost apply to agents running inside Mattermost, described in [Multiplayer Tool Calling](features/multiplayer_tool_calling.md), and not to calls an external client makes.

For that reason, give external clients only what the signed-in user should be able to do:

- Prefer OAuth over long-lived personal access tokens, and revoke tokens you no longer use.
- Ask users to turn on per-call confirmation in their client for tools that change state.
- Review the audit log for `mcpSessionGrant` events.

## License requirements

Turning on the HTTP endpoint requires an Enterprise license or higher, the same as remote and external MCP servers. See the [license requirements](admin_guide.md#license-requirements) table.

| Capability | Minimum level |
|---|---|
| Enable the Mattermost MCP server HTTP endpoint | Enterprise |
| Read-only tools (read, search, list, get) | Available whenever the endpoint is on |
| Tools that change state (post, edit, create channels, and similar) | Enterprise |
| Access policies for MCP servers | Enterprise Advanced |

Without an Enterprise license, the Agents settings do not let you set **Enable Mattermost MCP Server (HTTP)** to **True**, and state-changing tools are hidden from the tool list and return a licensing message if called. Agents inside Mattermost can still use the read-only built-in tools at every level.

## Troubleshooting

**The endpoint returns 404.** The route exists only while **Enable Mattermost MCP Server (HTTP)** is **True** and the Agents plugin is enabled. Check the setting and that the plugin is running, and check that the URL ends in `/plugins/mattermost-ai/mcp-server/mcp`.

**The client reports 401 and cannot sign in.** Confirm the Mattermost **Site URL** is set and matches the URL the client uses; the OAuth discovery response is built from it, and the Agents plugin does not activate without a Site URL. If you use a personal access token, confirm the token is active and that **Enable Personal Access Tokens** is **True**.

**The client cannot register an OAuth client.** Confirm **Enable OAuth 2.0 Service Provider** and **Enable Dynamic Client Registration** are **True** in **System Console > Integrations > Integration Management**. On Mattermost Cloud, these settings may not be changeable by workspace admins; ask Mattermost support if they are off.

**The client only supports SSE or stdio.** This endpoint supports streamable HTTP only. Use a client version that supports streamable HTTP, or a client option for remote HTTP servers.

**Write tools are missing from the list.** State-changing tools need an Enterprise license. A license change takes effect without restarting the plugin on Mattermost Server v12.0 and later.

**A user is missing tools that others have.** The user may be blocked by an access policy on the built-in server or a plugin server, or a plugin tool may be disabled by an admin in the **Tools** tab.

**A plugin's tools do not appear.** The plugin must register its server for external exposure, an admin must enable that plugin server in the **Tools** tab, and its tool names must not collide with built-in tools. Check the server log for skipped tools.

**You get 429 Too Many Requests.** Each user can have up to 32 MCP requests in flight at once. Close unused client sessions and retry after the `Retry-After` interval.

**Streams end after a while.** Requests are limited to 30 minutes. Compliant clients reconnect on their own.

## Related docs

- [Admin Guide: Mattermost MCP Server](admin_guide.md#mattermost-mcp-server)
- [Use Mattermost Agents as your personal AI assistant](personal_agent.md)
- [Multiplayer Tool Calling](features/multiplayer_tool_calling.md)
- [Model Context Protocol specification](https://modelcontextprotocol.io)

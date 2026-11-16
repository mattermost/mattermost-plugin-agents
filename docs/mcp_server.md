<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Mattermost MCP server

Connect Claude Code, Claude Desktop, Cursor, VS Code, or any MCP client to Mattermost over streamable HTTP at `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp`. Replace `your-mattermost-server` with your Site URL. The server is built into the Agents plugin, so there's nothing else to install, and each request runs as the Mattermost user who signed in. The client sees and does only what that user can. Posts it creates are marked as AI-generated, and an external client's first use of a session is recorded in the audit log as `mcpSessionGrant`.

An admin has to turn the endpoint on first. See [External client endpoint](mcp_integrations.md#external-client-endpoint). To let agents inside Mattermost use MCP tools, see [Connect agents to MCP servers](mcp_integrations.md). The [tool reference](mcp_tools.md) lists every tool. SSE and stdio clients can't connect to this endpoint. A standalone `mattermost-mcp-server` binary exists for plugin development and local testing, and isn't supported for production.

## Why use the official Mattermost MCP server

It runs inside Mattermost and is installed and upgraded with the Agents plugin, so there's no separate service to run. It calls Mattermost with the signed-in user's session, so channel, team, and role permissions apply to every call. Clients that support MCP OAuth with dynamic client registration can sign users in without tokens, and personal access tokens work for clients that need a static credential.

## Requirements

- Mattermost Server v12.0 or later, with the Agents plugin enabled and **Site URL** set. The OAuth discovery response that clients use to sign in is built from the Site URL.
- An admin has enabled the endpoint, which needs an Enterprise license to turn on. See [External client endpoint](mcp_integrations.md#external-client-endpoint).
- Mattermost reachable from the MCP client. Clients that run in a vendor's cloud, such as the Claude web app, connect from the vendor's network, so a Mattermost Server on a private network must be reachable from it.

## Authentication

A request with no Mattermost credentials gets `401 Unauthorized` with a `WWW-Authenticate` header that points to the OAuth protected resource metadata at `/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource`. MCP clients use that response to start signing in.

### OAuth 2.0

OAuth is the recommended method. The user signs in through Mattermost in a browser and approves the client, and no token is copied or stored by hand. Your admin has to enable OAuth and, for clients that register themselves, dynamic client registration.

If dynamic client registration is off, create an OAuth application under **Product menu > Integrations > OAuth 2.0 Applications** and enter its client ID in a client that supports pre-registered credentials.

Discovery endpoints:

- Protected resource metadata: `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource`
- Authorization server metadata: `https://your-mattermost-server/.well-known/oauth-authorization-server`

### Personal access tokens

For clients that can't do OAuth, create a personal access token under **Profile > Security > Personal Access Tokens** and send it as a bearer token:

```text
Authorization: Bearer <your-token>
```

A personal access token acts with that user's permissions. Treat it like a password, and prefer OAuth where the client supports it. An admin must turn on **Enable Personal Access Tokens**, and by default only System Admins can create tokens. Mattermost shows a token only when you create it, and you can revoke it under the same menu. Each client decides where it keeps the token you give it. Your shell expands `$MM_TOKEN` before a client sees the command, so check your client's documentation for where it writes the value, and keep tokens out of files you commit.

## Connect a client

Replace `https://your-mattermost-server` with your Mattermost Site URL in every example. Client menus and file formats change, so check the client's own documentation if a step doesn't match.

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

By default the server is added at local scope, for you in the current project. Add `--scope user` to use it in all projects, or `--scope project` to write it to a shared `.mcp.json`. Use `--scope project` only with OAuth, because a token passed on the command line can end up in that file. Check Claude Code's documentation for where it stores headers.

### Claude Desktop and claude.ai

Use a custom connector.

1. Go to **Customize > Connectors**.
2. Select **+ Add**, then **Add custom connector**.
3. Enter a name and the server URL `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp`, then select **Continue**.
4. Under **Authentication**, choose a sign-in option. Under **OAuth client**, choose **Register automatically** if dynamic client registration is enabled on your Mattermost Server.
5. Select **Add**, then select **Connect** on the connector and sign in to Mattermost.

On Team and Enterprise plans, an organization owner adds the connector under **Organization settings > Connectors**, and members then connect to it. Claude connects from Anthropic's cloud, so your Mattermost Server must be reachable from it.

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

Cursor reads the OAuth metadata from the server and starts signing in the first time it connects. To use a personal access token, add a `headers` object and read the token from an environment variable:

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

Any client that supports remote MCP servers over streamable HTTP can connect. Give it the endpoint URL and either let it run the OAuth flow or configure a bearer token header. Menus and file formats differ by client, so check that client's documentation.

## Verify and try it

1. In Claude Code, run `/mcp`. **mattermost** should show as connected. In other clients, open the server list and check that the Mattermost tools appear.
2. Ask a read-only question first, for example "Who am I on Mattermost, and which teams am I in?". A reply that names your account confirms the client signed in as you.
3. Then try tasks that use several tools:
   - "Read this thread (paste a post permalink) and list the open questions."
   - "Find posts from this week about the login bug and summarize them."
   - "Post a short release note for this branch in ~dev-releases." The client asks you to confirm if its confirmation setting is on, because this changes something.

## Tools

The [tool reference](mcp_tools.md) lists the tools by area, each marked read-only or state-changing. Tools that take a local file path or URL, such as `upload_file`, work only on the standalone stdio server. To attach content to a post over HTTP, pass text files inline in the `files` parameter of `create_post`, `dm`, or `group_message`.

Tools from plugin-registered MCP servers also appear here when the plugin marks its server for external exposure and an admin enables it. See [Plugin-registered MCP servers](mcp_integrations.md#plugin-registered-mcp-servers). Attribute-based access policies, at Enterprise Advanced, control which users see the built-in and plugin tools on this endpoint as well as in the product. See [Control who can use agents](access_control.md#attribute-based-access-control).

## Approval and safety

The MCP client decides how to confirm tool calls. Most clients ask the user before running a tool unless the user chose to auto-approve it. Tool approval policies configured in Mattermost apply to agents running inside Mattermost, described in [Tool approval and credentials](tool_approval.md), and not to calls an external client makes.

Give external clients only what the signed-in user should be able to do:

- Prefer OAuth over long-lived personal access tokens, and revoke tokens you no longer use.
- Ask users to turn on per-call confirmation in their client for tools that change state.
- Review the audit log for `mcpSessionGrant` events.

## Troubleshooting

**The endpoint returns 404.** The route exists only while the endpoint is enabled and the Agents plugin is running. Check that the URL ends in `/plugins/mattermost-ai/mcp-server/mcp`, then ask your admin to check the setting. See [External client endpoint](mcp_integrations.md#external-client-endpoint).

**The client reports 401 and can't sign in.** Check that the URL matches the Mattermost Site URL, because the OAuth discovery response is built from it. With a personal access token, check that the token is active and that your admin turned on personal access tokens.

**The client can't register an OAuth client.** Your admin needs to turn on the OAuth 2.0 service provider and dynamic client registration, and any redirect URI allowlist must match the client.

**The client only supports SSE or stdio.** Use a client version that supports streamable HTTP, or a client option for remote HTTP servers.

**Write tools are missing.** State-changing tools need an Enterprise license.

**You're missing tools that others have.** An access policy on the built-in or a plugin server, or an admin disabling a tool, can remove tools for you. Ask your admin.

**You get 429 Too Many Requests.** Each user can have up to 32 MCP requests in flight at once. Close unused client sessions and retry after the `Retry-After` interval.

**Streams end after a while.** Requests are limited to 30 minutes. Compliant clients reconnect on their own.

## Related

- [Tool reference](mcp_tools.md)
- [Connect agents to MCP servers](mcp_integrations.md)
- [Tool approval and credentials](tool_approval.md)
- [Model Context Protocol specification](https://modelcontextprotocol.io)

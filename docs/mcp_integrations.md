# Connect agents to MCP servers

Connect remote MCP servers so agents can use tools in Jira, GitHub, and other systems, with per-user OAuth or shared service accounts. The built-in Mattermost tools need no setup.

## Tool sources

| Source | Setup | License |
|---|---|---|
| **Built-in Mattermost MCP server** | None. It's embedded and runs every tool as the requesting user. See [Tool reference](mcp_tools.md). | Read-only tools at every level. Tools that change something at Enterprise and above. |
| **Remote MCP servers** | You add them in the System Console. | Enterprise |
| **Plugin-registered MCP servers** | Another Mattermost plugin registers a server with Agents. You enable it. | Enterprise |

You can't turn MCP off globally. To limit it, disable individual tools or servers, restrict who can reach a server with an [access policy](access_control.md), or restrict an agent's tool grants on its **MCPs** tab. Below Enterprise, stored remote servers stay visible but can't be added or enabled, and their tools aren't offered to agents.

Everything is under **System Console > Plugins > Agents > Model Context Protocol (MCP)**. It has a **Configuration** tab and a **Tools** tab.

## Add a remote MCP server

1. On the **Configuration** tab, select **Add Remote MCP Server**.
2. Fill in the server:

   | Setting | Notes |
   |---|---|
   | **Server name** | A label. Generated if you leave it blank. |
   | **Server URL** | The server's final address. Redirects to a different origin, including an `http` to `https` upgrade, are refused so credentials can't reach another host. |
   | **Enable Server** | Turns the server on or off. |
   | **Description** | Up to 500 characters. Tells agents what the integration is for, so they can offer it to people who haven't connected yet. After you save, **Auto-fill from server** copies the server's own description or instructions. You may need to connect your account first. Review the text and save, because nothing is stored until you do. |
   | **Headers** | Extra HTTP headers the server needs. They're sent for every user in both authentication modes, so don't put a personal or admin credential here. |
   | **Service Account Authentication** | Static headers, such as an API key, for agents that use service accounts. See [Service account authentication](#service-account-authentication). |
   | **OAuth Credentials (Optional)** | **Client ID** and **Client Secret** for servers that need a pre-registered OAuth app, such as GitHub. Leave empty if the server supports automatic registration. |

3. Select **Save**.

Agents connect over Streamable HTTP, and fall back to the older HTTP+SSE transport for servers that only support it. Servers that only run over stdio can't be added.

Write a **Description** for any server whose name doesn't say what it does. The agent is told only your server name, your description, and the tools you've configured, not anything the server reports to other users' connections.

To check a server that doesn't use OAuth, such as an internal server with an API key, select **Save**, then open the **Tools** tab. The server's tools are listed there. If the list is empty or an error shows, check the **Server URL** and headers. For an internal server with an API key, put the key in **Service Account Authentication**, and turn on **Use service accounts for authentication** on the agent. A key in **Headers** is sent for every user.

### Sign in with OAuth

Servers that use OAuth authorize each Mattermost user separately. Connecting in the **Tools** tab authorizes only your own account. Every user connects separately.

- **Callback URL.** The **MCP OAuth Callback URL** on the **Configuration** tab is `https://<your-site-url>/plugins/mattermost-ai/oauth/callback`. Register it as the redirect URI in the remote server's OAuth app.
- **Where users connect.** Users connect from a **Connect** card in the conversation, or from the **Tools** menu in the Agents pane. See [Use tools and approve actions](using_tools.md). The mobile apps can't start OAuth, so users need web or desktop.
- **Discovery.** If the OAuth start URL carries a `resource_metadata` parameter, it's accepted only when its origin matches the **Server URL**. This blocks cross-origin metadata injection.
- **Expired authorization.** If a user's authorization expires or is revoked during a conversation, the agent drops that connection and shows the **Connect** card again instead of failing.

### Atlassian MCP server

If users get an authorization error connecting to the Atlassian MCP server, an Atlassian organization admin must allow your Mattermost domain. In [admin.atlassian.com](https://admin.atlassian.com), open **Apps > AI settings > Rovo MCP server**, select **Add domain**, and enter your domain with the path wildcard, such as `https://chat.example.com/**`. The `/**` is required. Site Admin isn't enough, so you need Organization Admin on the organization that owns the Atlassian site. Changes can take a minute or two to apply. See [Atlassian's documentation](https://support.atlassian.com/security-and-access-policies/docs/control-atlassian-rovo-mcp-server-settings/).

To keep Jira links clickable in replies without allowing every domain, add your Atlassian site to **Allowed Link Domains**. See [Secure AI-generated links and HTML](ai_output_security.md).

## Example: connect GitHub

GitHub's MCP server needs a pre-registered OAuth app. GitHub's interface changes often, so use GitHub's documentation for step 1.

1. In GitHub, create an OAuth app for the MCP server. For its redirect URI, use the **MCP OAuth Callback URL** shown on the **Configuration** tab. It looks like `https://<your-site-url>/plugins/mattermost-ai/oauth/callback`. Copy the app's client ID and secret.
2. On the **Configuration** tab, select **Add Remote MCP Server** and set:
   - **Server name** to `GitHub`.
   - **Server URL** to the MCP endpoint in GitHub's documentation. Its host is `api.githubcopilot.com`.
   - **Description** to something like `Search code, read issues and pull requests, and create issues on GitHub.`
   - **Client ID** and **Client Secret** under **OAuth Credentials (Optional)** to the values from step 1.
3. Select **Save**.
4. Open the **Tools** tab, expand **GitHub**, and select **Connect Account** to authorize your own account. This lets you see the tool list. Each user still connects their own account.
5. Review the tools. Read-only tools are pre-filled, and tools that change something, such as creating an issue, ask every time. See [Enable and approve tools](#enable-and-approve-tools).
6. Check the agent's **MCPs** tab. With **Automatically enable all MCP tools** on, which is the default, the agent can use the GitHub tools. Otherwise, grant them there.

A user who asks "Open an issue for the bug in this thread" now sees a **Connect** card the first time. After they connect, the agent finds the issue tool with dynamic tool loading, and the user accepts the call.

## Enable and approve tools

On the **Tools** tab, each server lists its tools. For each tool you set whether it's enabled and how it's approved. The options are **Ask Every Time**, **Auto Run (DM)**, and **Auto Run (Everywhere)**. What they do in direct messages and channels is in [Approval policies](tool_approval.md#approval-policies).

A tool with no saved setting is enabled and set to **Ask Every Time**. That includes tools a server adds later.

When you add a server at a known host, Mattermost pre-fills policies for its read-only tools. Mattermost's own read-only tools default to **Auto Run (DM)**, except `get_file_link`, `list_incoming_webhooks`, `list_outgoing_webhooks`, `list_agents`, and `list_drafts`, which stay at **Ask Every Time**. The first three expose bypass links or webhook tokens. The same pre-fill covers read tools on Atlassian (`mcp.atlassian.com`), GitHub (`api.githubcopilot.com`), and Figma (`mcp.figma.com`). GitHub's code scanning, Dependabot, secret scanning, and security advisory reads stay at **Ask Every Time**. Tools that change something are never pre-filled.

Expand a tool row to add a **Retrieval description override**. It helps the agent find the tool through dynamic tool loading search, and doesn't change the schema sent after loading. **Refresh Tools** rediscovers tools. **Clear Cache** drops cached tool lists. The tab refreshes by itself after you connect or disconnect an OAuth server.

Approval policy details, including how they behave in channels and below Enterprise, are in [Tool approval and credentials](tool_approval.md).

## What an agent can use

The tools an agent sees for a request are the intersection of:

1. The admin's tool enablement and approval policy.
2. The server's [access policy](access_control.md), if any.
3. The agent's grants: **Automatically enable all MCP tools**, or the tools picked on its **MCPs** tab.
4. The user's own provider choices in the **Tools** menu, except for service account agents.
5. Context rules, such as no tools in channels unless **Enable Channel Mention Tool Calling** is on.

Granting a server or tool to an agent never bypasses approval policy or the user's permissions on the target system. If a disconnected OAuth server exposes no tools yet, you can still turn it on for an agent. The agent gets every tool the server exposes once a user connects.

## Service account authentication

By default, tool calls use the requesting user's credentials: per-user OAuth on external MCP servers, and the user's own identity for Mattermost tools. An agent can instead use **service account authentication**, where external MCP servers get shared credentials you configure. Mattermost and plugin tools still run as the requesting user. It needs an Enterprise license.

Set it up:

1. Open the remote server on the **Configuration** tab. In **Service Account Authentication**, add the headers the server should receive. Put the header name and value in separate fields, for example `Authorization` and `Bearer <token>`, or `X-API-KEY` and the key. Don't repeat the name in the value. Rows with a blank name or value are ignored, and two names that differ only by case are both dropped. Save.
2. In the agent's **MCPs** tab, turn on **Use service accounts for authentication**. Only System Admins can.

### Behavior

- External servers get the service account headers instead of per-user OAuth. The server's regular **Headers** are still sent, and on a clash the service account value wins.
- A server without service account headers is left out of the agent entirely. There's no fallback to anyone's personal connection. To offer both modes, use two agents or two server entries.
- Users are never asked to connect, their per-user provider choices don't apply, and the **Tools** menu is hidden for that agent.
- Those agents share one remote connection per agent, keyed to its bot account.

Service account mode flattens permissions on external servers. Every user who can use the agent acts with the shared account's access, and the external system's audit log shows the service account, not the person. Restrict who can use the agent, use one minimally scoped account per integration, and don't reuse an admin's token. Header values are stored in the plugin configuration and visible to System Admins. To correlate actions with people, turn on the [token usage log](observability.md#token-usage-log). Each record has the requesting user, the acting identity, and the authentication mode.

## Dynamic tool loading

With **Dynamic tool loading** on, which is the default, an agent doesn't receive every tool schema at once. It sees two helper tools:

- `search_tools` returns up to 8 candidate tools with names and summaries.
- `load_tool` loads one tool's full schema by exact name.

The agent must load a tool before it can run it. A loaded tool stays available for the rest of the conversation, and is restored on later turns if it's still allowed. The helpers run without approval. The loaded tool still follows its own approval policy and permissions. Dynamic loading applies to normal agent conversations. LLM Bridge calls can request tool schemas directly.

This keeps large tool catalogs out of the model's context. Turn it off per agent for models that handle the full list well, or if search results are poor. Dynamic loading controls when schemas are shown. The agent's grants control which tools it may use.

## Plugin-registered MCP servers

Other plugins can register an MCP server with Agents through the LLM Bridge. Registering needs an Enterprise license. See [LLM Bridge](llm_bridge.md). Each registered server appears as a plugin row on the **Tools** tab, where you can:

- Enable or disable the whole server, and set per-tool enablement and approval policy.
- Set an access policy under **Built-in & plugin servers** on the **Configuration** tab.

Your settings are keyed to the plugin ID and survive unregistering and restarts. The plugin controls the server's name and path, and whether it can be exposed on the HTTP endpoint for external clients. You can't change that flag. Tool calls to these servers carry the requesting user's identity, so the source plugin can enforce its own permissions.

## External client endpoint

The same built-in Mattermost tools can be offered to external MCP clients such as Claude Code, Claude Desktop, Cursor, and VS Code. For connecting a client, see [Mattermost MCP server](mcp_server.md). The embedded server that agents use is always on and doesn't depend on this setting.

1. Go to **System Console > Plugins > Agents > Model Context Protocol (MCP)**.
2. On the **Configuration** tab, set **Enable Mattermost MCP Server (HTTP)** to **True**, and select **Save**. It takes effect without a restart. The endpoint is `https://<your-site-url>/plugins/mattermost-ai/mcp-server/mcp`.

Then set up the sign-in method your clients use, under **System Console > Integrations > Integration Management**:

- **OAuth 2.0.** Set **Enable OAuth 2.0 Service Provider** to **True**. For clients that register themselves, also turn on dynamic client registration. That endpoint is public and needs no authentication, so anyone who can reach the server can register a client. Read the [dynamic client registration settings](/administration-guide/configure/integrations-configuration-settings#enable-dynamic-client-registration) first. If a DCR redirect URI allowlist is set, the client's redirect URI must match it. Without dynamic registration, create an OAuth app under **Product menu > Integrations > OAuth 2.0 Applications**. On Mattermost Cloud, these settings might not be changeable by workspace admins.
- **Personal access tokens.** Set **Enable Personal Access Tokens** to **True**. By default only System Admins can create them, unless you grant others that permission.

License and limits:

- Turning the endpoint on needs an Enterprise license. State-changing tools need Enterprise too, and without it they're hidden from the tool list and return a licensing message. Read-only tools are available whenever the endpoint is on. Access policies on the built-in and plugin servers need Enterprise Advanced.
- If the license drops below Enterprise, an endpoint that's already on stays on with read-only tools. Turn it off to stop external access.
- Plugin-registered tools appear on the endpoint only when the plugin marks its server for external exposure and you enable that server. A tool whose name collides with a built-in or another plugin's tool is skipped and logged.

Request limits and client-side symptoms are in [Mattermost MCP server](mcp_server.md#troubleshooting).

## Connections and timeouts

Each user has their own connection to each MCP server. **Connection Idle Timeout (minutes)** closes inactive connections. The default is 30. A lower value frees resources, and a higher one speeds up repeat requests.

## Related

- [Mattermost MCP server](mcp_server.md) for letting external AI clients use Mattermost.
- [Tool approval and credentials](tool_approval.md).
- [Troubleshooting](troubleshooting.md).

# Mattermost Agents admin guide

Set up Mattermost Agents (plugin `mattermost-ai`): connect LLMs, create agents, connect MCP servers and tools, and control access, approval, and licensing.

An agent is a Mattermost bot backed by an LLM that you choose, including a model that runs on your own infrastructure. Agents act through tools: the built-in Mattermost tools, remote MCP servers, and tools registered by other plugins. The plugin also ships a built-in Mattermost MCP server for external AI clients such as Claude Code and Cursor.

## How the pieces fit

1. An **LLM service** holds the connection to a provider: type, credentials, default model, and optional fallback. You configure services in the System Console.
2. An **agent** is a bot with a name, instructions, a model, access rules, and tool grants. Agents use a service. You create them on the **Agents** page.
3. **Tools** give an agent abilities. Admins decide which tools exist, which need approval, and which users can reach each MCP server.
4. **Users** talk to agents in direct messages, the Agents pane, and channels. Every tool call runs with the permissions of the person who asked, unless an agent uses a shared service account for external MCP servers. Tools from plugin-registered servers receive the requester's identity, and the source plugin enforces its own permissions. Credentials in a remote server's regular **Headers** are also shared by every user. See [Tool approval and credentials](tool_approval.md).

Custom instructions can't grant or restrict access. Use the agent's **Access** tab, MCP access policies, and tool approval policies.

## Requirements

| Requirement | Detail |
|---|---|
| Mattermost Server | v12.0.0 or later. |
| Site URL | **Site URL** must be set under **System Console > Environment > Web Server**. The plugin doesn't activate without it. |
| Database | The Mattermost PostgreSQL database. Agents creates its own tables and runs migrations on start. |
| pgvector | Only for [semantic search](embedding_search.md). PostgreSQL needs the `pgvector` extension. |
| LLM access | Network access from the Mattermost Server to your LLM provider, and credentials. |
| Outbound proxy | LLM provider traffic follows the `HTTP_PROXY` and `HTTPS_PROXY` environment variables of the Mattermost Server process. |

Mattermost Server can ship with Agents pre-installed. Check **System Console > Plugins > Plugin Management**. To install or upgrade, download a release `.tar.gz` from the [releases page](https://github.com/mattermost/mattermost-plugin-agents/releases), select **Upload Plugin**, and enable it. Set **Enable Plugin** to **True** under **System Console > Plugins > Agents** if needed.

## Quick start

1. Go to **System Console > Plugins > Agents** and select **Add an AI Service**. Choose a **Service type**, enter the credentials and a **Default model**, and save. See [Configure LLM services](llm_services.md) and [Provider configuration](providers.md). For a self-hosted model, see [Sovereign AI deployments](sovereign_ai.md).
2. Open the **Agents** page from the product switcher (grid icon). Select **Create agent**, choose a username, the service, and optional instructions. See [Create and manage agents](managing_agents.md).
3. Back in **System Console > Plugins > Agents**, pick the **Default bot**. Users who haven't chosen an agent get this one, and so do features that don't let users choose, such as meeting summaries. If the default agent becomes unavailable, those features fail until you pick another. Mattermost never substitutes a different agent.
4. Send the agent a direct message to test it.
5. Optionally add [MCP servers](mcp_integrations.md), [web search](web_search.md), or [semantic search](embedding_search.md), and set [access controls](access_control.md).

## Admin tasks

| I want to | Go to |
|---|---|
| Connect an LLM provider, set fallbacks and token limits | [Configure LLM services](llm_services.md), [Provider configuration](providers.md), [AWS Bedrock](aws_bedrock_setup.md) |
| Create agents, delegate agent management, export and import agents | [Create and manage agents](managing_agents.md) |
| Restrict who can use an agent, service, or MCP server | [Control who can use agents](access_control.md) |
| Connect remote MCP servers and let agents use tools | [Connect agents to MCP servers](mcp_integrations.md) |
| Decide which tool calls need approval | [Tool approval and credentials](tool_approval.md) |
| Control tools, auto-reply, and web search in channels | [Control agents in channels](channel_settings.md) |
| Let external AI clients use Mattermost | [External client endpoint](mcp_integrations.md#external-client-endpoint) |
| Give agents web search | [Web search](web_search.md) |
| Turn on semantic search | [Set up semantic search](embedding_search.md) |
| Control links and HTML in AI output | [Secure AI-generated links and HTML](ai_output_security.md) |
| Know what is sent to the LLM and what is stored | [Data handling](data_handling.md) |
| Track usage, tracing, and audit events | [Monitor Agents](observability.md) |
| Back up Agents or automate configuration | [Back up and automate](backup_and_automation.md) |
| Run fully self-hosted or air-gapped | [Sovereign AI deployments](sovereign_ai.md) |
| Fix a problem | [Troubleshooting](troubleshooting.md) |
| Upgrade from Agents 1.x | [Upgrade from Agents 1.x](upgrading_to_2.0.md) |

For developers and integrators, see the [Mattermost MCP server](mcp_server.md) for connecting Claude Code or Cursor, the [tool reference](mcp_tools.md), and the [LLM Bridge](llm_bridge.md) for plugins.

## Plugin settings

Everything is under **System Console > Plugins > Agents**. The plugin stores its configuration, and agents, in its own database tables. See [Back up and automate](backup_and_automation.md).

| Panel | Setting | Config key | Default | Details |
|---|---|---|---|---|
| AI Services | Services | `services` | none | [Configure LLM services](llm_services.md) |
| AI Bots | Limit agent version history, Versions to keep per agent | `agentVersionsToKeep` | Off (`0`, keep all). Turning it on sets 50. | [Create and manage agents](managing_agents.md#keep-a-limited-history) |
| AI Functions | Default bot | `defaultBotUserID` | The first agent, until you pick one | [Create and manage agents](managing_agents.md) |
| AI Functions | Render AI-generated links | `allowUnsafeLinks` | Off | [Secure AI-generated links and HTML](ai_output_security.md) |
| AI Functions | Allowed Link Domains (csv) | `allowedLinkDomains` | Empty (any domain, when links are on) | [Secure AI-generated links and HTML](ai_output_security.md) |
| AI Functions | Enable HTML artifacts (experimental) | `enableHTMLArtifacts` | Off | [Secure AI-generated links and HTML](ai_output_security.md#html-artifacts) |
| AI Functions | Enable Channel Mention Tool Calling (experimental) | `enableChannelMentionToolCalling` | Off | [Control agents in channels](channel_settings.md) |
| AI Functions | Enable ambient auto-replies (experimental) | `enableAmbientAutoReply` | Off | [Control agents in channels](channel_settings.md#ambient-limits-and-cost) |
| AI Functions | Allow native web search in channels | `allowNativeWebSearchInChannels` | Off | [Control agents in channels](channel_settings.md) |
| Debug | Trace Output, OpenTelemetry Endpoint, Use TLS for OTLP Export, OTLP Export Headers | `telemetryOutput`, `openTelemetryEndpoint`, `openTelemetryTLS`, `openTelemetryHeaders` | Off | [Monitor Agents](observability.md#opentelemetry-tracing) |
| Debug | Enable Token Usage Logging | `enableTokenUsageLogging` | Off | [Monitor Agents](observability.md#token-usage-log) |
| Embedding Search (experimental) | Embedding settings and reindex controls | `embeddingSearchConfig` | Off | [Set up semantic search](embedding_search.md) |
| Web Search | Built-in web search | `webSearch` | Off | [Web search](web_search.md) |
| Model Context Protocol (MCP) | Servers, tools, HTTP endpoint, idle timeout | `mcp` | HTTP endpoint off, idle timeout 30 minutes | [Connect agents to MCP servers](mcp_integrations.md) |

## License requirements

The plugin distinguishes four license levels: Free (no license), Professional, Enterprise, and Enterprise Advanced. Each level includes everything below it. The Entry license behaves as Enterprise. Legacy E10 licenses count as Professional and E20 licenses as Enterprise. A development server (`EnableDeveloper` and `EnableTesting` both on) enables every capability.

| Capability | Free | Professional | Enterprise | Enterprise Advanced |
|---|---|---|---|---|
| Agents | 1 | 3 | Unlimited | Unlimited |
| LLM services | 1 | 1 | Multiple | Multiple, with fallback chains |
| Direct messages with an agent | Yes | Yes | Yes | Yes |
| Vision and document attachments | Yes | Yes | Yes | Yes |
| Personal skills | Yes | Yes | Yes | Yes |
| Built-in Mattermost tools | Read-only | Read-only | Read and write | Read and write |
| Agents in channels (@mention) | No | Yes | Yes | Yes |
| Thread, channel, and unread summaries | No | Yes | Yes | Yes |
| Provider-native web search | No | Yes | Yes | Yes |
| Agent access controls (users, teams, channels) | No | Yes | Yes | Yes |
| Token usage logging | No | Yes | Yes | Yes |
| Built-in web search (Brave, Google, SearXNG) | No | No | Yes | Yes |
| Tool approval policies | No | No | Yes | Yes |
| Remote and external MCP servers, HTTP MCP endpoint | No | No | Yes | Yes |
| MCP service account authentication | No | No | Yes | Yes |
| Semantic search and AI search | No | No | Yes | Yes |
| Meeting summaries | No | No | Yes | Yes |
| Shared skills | No | No | Yes | Yes |
| Channel auto-reply | No | No | No | Yes |
| Attribute-based access control | No | No | No | Yes |

### Limits by level

- **Agent cap.** Configuration-file bots and user-created agents share one pool. Over the cap, the plugin activates configuration-file bots first, then user-created agents oldest first. The rest stay stored but inactive, marked **Inactive** on the Agents page and named in the server log. Creating an agent over the cap fails with a message naming the level that raises it.
- **One service below Enterprise.** Free and Professional use only the first service in the list. Agents that use another service are inactive and don't count toward the agent cap. LLM Bridge calls reach only the first service.
- **HTTP MCP endpoint.** What happens to an endpoint that is already on after a downgrade is in [External client endpoint](mcp_integrations.md#external-client-endpoint).
- **Fallback chains.** Followed at Enterprise Advanced only. Below that, a stored fallback stays in the configuration but requests use the primary service.
- **Write tools.** Tools that change something in Mattermost (post, react, edit, create channels, and similar) are absent from the tool list below Enterprise, and calls return a licensing message.
- **Approval policies.** Below Enterprise, built-in Mattermost tools use the product defaults and every other tool that has a policy asks first. Tools without a policy are listed in [Approval policies](tool_approval.md#approval-policies). A stricter policy you set earlier still applies. A disabled tool stays disabled.

### At every level

- Reading configuration is never blocked.
- Turning something off is never blocked. You can disable, clear, or delete a gated setting after a license downgrade.
- License changes apply without restarting the plugin.
- A System Console or REST API request denied for licensing returns HTTP 403 with an `error` message and a `license_required` field set to `professional`, `enterprise`, or `enterprise_advanced`. LLM Bridge completions, MCP tools, and channel mentions report licensing differently. See [LLM Bridge](llm_bridge.md#permissions-and-licensing).

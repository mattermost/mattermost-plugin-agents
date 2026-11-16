# LLM Bridge for plugins

Call the agents and LLM services configured in Agents from another Mattermost plugin over the LLM Bridge, and register your plugin's own MCP server. It's for developers who want AI features without managing provider credentials.

The client reference, with code for streaming, multi-turn requests, tool allowlists, and structured output, is in the [bridge client README](https://github.com/mattermost/mattermost-plugin-agents/blob/master/public/bridgeclient/README.md). The package `github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient` is part of the plugin's root Go module. Treat its exported API as stable.

## How it works

The bridge is available only to other plugins, and to the server itself through `NewClientFromApp`. The Mattermost Server sets the `Mattermost-Plugin-ID` header on inter-plugin requests, and the bridge rejects requests without it. Browsers and external clients can't reach it.

| Route | Purpose |
|---|---|
| `GET /bridge/v1/agents` | List agents. |
| `GET /bridge/v1/agents/{agent}/tools` | List the tools an agent can use. |
| `GET /bridge/v1/services` | List LLM services. |
| `POST /bridge/v1/completion/agent/{agent}` | Streaming completion from an agent. Add `/nostream` for a single response. |
| `POST /bridge/v1/completion/service/{service}` | Completion from a service directly, with no agent. Add `/nostream` for a single response. |
| `POST /bridge/v1/mcp/register`, `POST /bridge/v1/mcp/unregister` | Register or remove the caller's MCP server. |

In Go, create a client with `bridgeclient.NewClient(p.API)` in a plugin, or `bridgeclient.NewClientFromApp(app, userID)` from the server. Methods include `AgentCompletion`, `AgentCompletionStream`, `ServiceCompletion`, and `ServiceCompletionStream`.

## Permissions and licensing

- **Permissions are opt-in.** On agent endpoints, pass `UserID`, and `ChannelID` if relevant, to check the agent's user and channel access rules. Without `UserID`, no check runs. On service endpoints, `UserID` and `ChannelID` are recorded for attribution only, and no check runs. Your plugin must verify permissions before it calls the bridge. The inter-plugin boundary is the only trust check.
- **Tools.** Service completions run with tools off. Agent completions can pass `allowed_tools`, a list of tool names that may run. Listed tools auto-run with no approval flow, and they must come from enabled MCP servers or the embedded server. `UserID` is required when you use `allowed_tools`.
- **Service accounts.** For an agent that uses service account authentication, external MCP tools come from the agent's service account catalog, and `UserID` selects the identity for Mattermost and plugin tools.
- **Licensing.** Below Enterprise, service completions reach only the first configured service, and fallback chains need Enterprise Advanced. A completion denied for licensing returns HTTP 403 with an `error` message. Only `mcp/register` also sets a `license_required` field. See [License requirements](admin_guide.md#license-requirements).
- **Structured output.** A `json_output_format` schema follows the service's **Structured output** setting. See [Configure LLM services](llm_services.md#structured-output).
- **Tracking.** Set `Operation` and `OperationSubType` to label calls in the [token usage log](observability.md#token-usage-log). Calls straight to a service have no agent, so the agent fields in that log are empty.

## Trigger an agent with a post

A bot account or a plugin can start an agent turn by creating a post with the `activate_ai` property. Webhook posts and system messages never trigger agents, and there's no human to approve tool calls in an automated turn. See [Automated triggers](tool_approval.md#automated-triggers).

## Register an MCP server

A plugin can expose its own tools to Agents by calling `POST /bridge/v1/mcp/register` with `name`, `path`, `enabled`, `expose_external`, and optional `tool_configs`. The plugin ID comes from the trusted header, not the body. Registering needs an Enterprise license.

An admin then enables the server and sets per-tool approval on the **Tools** tab. Setting `expose_external` lets an admin offer the plugin's tools on the HTTP endpoint for external clients. Tool calls carry the requesting user's identity, so your plugin enforces its own permissions. See [Connect agents to MCP servers](mcp_integrations.md#plugin-registered-mcp-servers).

# Sovereign AI deployments

Sovereign AI means you control where models run, where data goes, and who can use them. This page shows how to run Mattermost Agents with self-hosted models or private cloud endpoints, including air-gapped networks, and which settings keep data inside your boundary. It applies when you must keep message content, user data, and tool results from reaching an outside vendor.

## What you control

Agents contacts only services you configure: the LLM, an embedding provider, a web search provider, and MCP servers. The plugin has no built-in calls to any other external service. Message content leaves Mattermost only toward those endpoints, so choosing where they run decides where your data goes. The full list is in [Data handling](data_handling.md).

## Deployment patterns

### Fully self-hosted

Use this for air-gapped and disconnected networks.

| Piece | Choice |
|---|---|
| LLM | An **OpenAI Compatible** service that points at a model you run, such as Ollama or vLLM. See [Provider configuration](providers.md#openai-compatible-ollama-vllm-and-others). |
| Embeddings | **OpenAI-compatible API** pointing at a self-hosted embedding model. Vectors live in your PostgreSQL with pgvector. See [Set up semantic search](embedding_search.md). |
| Web search | **SearXNG (self-hosted)**, or leave web search off. See [Web search](web_search.md). |
| Tools | The built-in Mattermost MCP server, plus MCP servers you host. See [Connect agents to MCP servers](mcp_integrations.md). |
| Plugin install | Upload the plugin `.tar.gz` in the System Console. No outbound access to the plugin releases page is needed once you have the file. |

Check that your model supports the features you'll use. Tool calling, vision, and structured output differ across servers. If your server's capabilities can't be detected, set **Structured output** on the service. See [Configure LLM services](llm_services.md#structured-output).

### Private cloud endpoints

Run models in your own cloud account, with private networking and your identity and access management. Supported options are AWS Bedrock, Azure OpenAI, and Google Vertex AI. See [AWS Bedrock](aws_bedrock_setup.md) and [Provider configuration](providers.md). Bedrock supports VPC endpoints through **Custom Endpoint URL**. An Enterprise Advanced license lets you chain services so a request tries your private endpoint first and falls back to another, such as a local model, only when the first fails.

### Public LLM APIs with controls

If you use a vendor API, the vendor receives each request's content. You can limit the exposure:

- Restrict who can use each agent, and where. See [Control who can use agents](access_control.md).
- Keep tools off in channels, which is the default, and set sensitive tools to **Ask Every Time**.
- Review your provider's terms for data retention and training. Mattermost can't enforce them.
- Use the [token usage log](observability.md#token-usage-log) and [audit records](observability.md#audit-logging) to see who uses what.

## Security and compliance controls

| Control | Where |
|---|---|
| Per-agent allow and block lists for users, teams, and channels | [Control who can use agents](access_control.md) |
| Attribute-based policies for agents, services, and MCP servers | [Control who can use agents](access_control.md#attribute-based-access-control) |
| Per-tool approval policies and initiator-only approval | [Tool approval and credentials](tool_approval.md) |
| Permissions: every tool runs with the requesting user's access | [Tool approval and credentials](tool_approval.md#whose-credentials-run-a-tool) |
| Audit records for configuration, agents, approvals, and MCP sessions | [Monitor Agents](observability.md#audit-logging) |
| Links and HTML in AI output | [Secure AI-generated links and HTML](ai_output_security.md) |
| Multiple agents, each with its own model, instructions, and access | [Create and manage agents](managing_agents.md) |

## Checklist

1. Add a service that points at your model, and confirm tool calling works.
2. Create an agent and test it in a direct message.
3. If you use semantic search, run an embedding model you host and run a **Full Reindex**.
4. Set web search to SearXNG or off.
5. Remove tool access you don't need, and review approval policies.
6. Turn on audit logging on the server, and the token usage log if you track usage.

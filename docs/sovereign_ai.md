# Sovereign AI deployments

Run Mattermost Agents on self-hosted or private-cloud models, including air-gapped networks, so message content and tool results never reach an outside vendor.

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

## Checklist

1. Add a service that points at your model, and confirm tool calling works. See [Verify a self-hosted model](providers.md#verify-a-self-hosted-model).
2. Create an agent and test it in a direct message.
3. If you use semantic search, run an embedding model you host and run a **Full Reindex**.
4. Set web search to SearXNG or off.
5. Remove tool access you don't need, and review approval policies.
6. Turn on audit logging on the server, and the token usage log if you track usage.

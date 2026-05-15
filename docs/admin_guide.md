# Mattermost Agents Admin Guide

This guide covers installing, configuring, and managing the Mattermost Agents plugin. You'll learn how to set up AI capabilities for your Mattermost instance and configure them for your organization's needs.

## Installation

### Prerequisites

Before installing the Agents plugin, ensure your environment meets these requirements:

- Mattermost Server v10.0+
- PostgreSQL database
- For semantic search: PostgreSQL with pgvector extension
- Network access to your chosen LLM provider
- If outbound LLM traffic must use an HTTP proxy, set `HTTP_PROXY` and `HTTPS_PROXY` on the Mattermost server process or container environment.
- API keys if using a cloud LLM service

### Installation Steps

#### Use pre-installed plugin

From Mattermost v10.3, Agents comes installed automatically and ready for you to configure a large language model (LLM). When no LLMs are configured, the Agents panel prompts users to configure one.

#### Install latest version

For the most recent features and improvements, you can download and install the latest plugin version from the [GitHub releases page](https://github.com/mattermost/mattermost-plugin-agents/releases). 

Install the plugin through the System Console by navigating to **System Console > Plugin Management**, clicking **Upload Plugin**, selecting the downloaded plugin file (.tar.gz), and clicking **Upload**. Enable the plugin after upload completes, then configure plugin settings as detailed in the Configuration section below.

## Configuration

### Access plugin settings

Navigate to **System Console > Plugins > Agents** to configure plugin-wide settings such as AI services, the default bot, web search, embedding search, and MCP settings.

Create and manage agents from the top-level **Agents** product page. If an agent selector dropdown is available in the Agents experience, use its **Manage** link to open the same page. The **AI Bots** section in the System Console links to the Agents page instead of hosting the full agent editor.

### Enable the plugin

Agents is enabled automatically when using the pre-installed version. If you've manually installed a newer version, you may need to enable it by going to **System Console > Plugins > Agents** and setting **Enable Plugin** to **True**, then complete configuration in the System Console.

### Basic configuration

If you have an Enterprise, or Enterprise Advanced license, upload it to unlock additional features. If you don't have a license but are running Mattermost Enterprise Edition, an Entry license will be automatically applied for you.

For general settings, you can toggle to enable or disable the plugin system-wide, enable debug logging for troubleshooting (use only when needed), enable token usage logging for tracking LLM interactions, and configure the hostname allowlist for API calls. Outbound LLM provider traffic respects `HTTP_PROXY` and `HTTPS_PROXY` when they are set on the Mattermost server process.

### AI response link rendering

Mattermost Agents includes a setting that controls whether AI-generated Markdown links are rendered as clickable links in responses:

- **System Console label**: **Render AI-generated links**
- **Configuration key**: `allowUnsafeLinks`
- **Default value**: `false`

When **Render AI-generated links** is set to **False** (default), AI-generated Markdown links are shown as plain text and are not rendered as clickable links.

When this setting is set to **True**, AI-generated links may be rendered as clickable links. This is a security tradeoff: AI output can include malicious destinations, which can increase phishing and data exfiltration risk.

Enable this setting only in trusted or otherwise mitigated environments, such as where users are trained to validate links and your organization has endpoint protections and URL controls in place.

### Service configuration

Configure an LLM provider (Service) for your Agents integration. Services manage the connection to the LLM provider, including authentication and model defaults. You can create multiple services for different providers or configurations, and share them across multiple agents.

Navigate to **System Console > Plugins > Agents** and select **Add a Service**.

| Setting | Description |
|---------|-------------|
| **Name** | Internal name for this service configuration |
| **Type** | LLM provider (OpenAI, Anthropic, AWS Bedrock, Cohere, Mistral, Scale AI, Azure OpenAI, OpenAI-compatible) |
| **API Key** | Your provider's API key (requirements vary by provider) |
| **Default Model** | Default model to use for this service |
| **Input Token Limit** | Maximum tokens allowed in input |
| **Output Token Limit** | Maximum tokens allowed in output |
| **Streaming Timeout Seconds** | Timeout in seconds for streaming responses |
| **Use Responses API** | (OpenAI Compatible and Azure OpenAI only) Use OpenAI's Responses API for native provider tools, reasoning controls, and structured output on those endpoints. OpenAI (direct) always uses the Responses API, so this control isn't shown for that service type. |

#### Provider Specific Settings

Each provider has specific configuration requirements:

| Provider | Required Settings | Optional Settings |
|----------|-------------------|-------------------|
| **OpenAI** | API Key | Organization ID |
| **OpenAI Compatible** | API URL | API Key, Organization ID |
| **Anthropic** | API Key | |
| **AWS Bedrock** | AWS Region | API Key (can use IAM role), Access/Secret Keys |
| **Cohere** | API Key | |
| **Mistral** | API Key | |
| **Scale AI** | API Key, API URL | Account ID (required for ScaleGov) |
| **Azure OpenAI** | API Key, API URL | |

For AWS Bedrock, authentication can be configured using AWS credentials in the API Key/Secret fields, or by using IAM roles when running Mattermost on AWS infrastructure.

**Important for Anthropic Claude models**: Before using Claude models via AWS Bedrock, you must submit a one-time First Time Use (FTU) form in the AWS Bedrock Model Catalog, and attach Bedrock API permissions to your Mattermost servers' IAM role. See the [AWS Bedrock setup guide](https://docs.mattermost.com/agents/docs/aws_bedrock_setup.html) for detailed instructions.

OpenAI services always use the Responses API. OpenAI Compatible and Azure services keep the **Use Responses API** setting so you can disable it for endpoints that still require legacy Chat Completions compatibility.

See the [Provider Guide](https://docs.mattermost.com/agents/docs/providers.html) for detailed provider-specific configuration.

### Agent configuration

Create and manage agents from the **Agents** product page. Open it from the top-level **Agents** product entry, or use the **Manage** link in an agent selector dropdown to navigate there. Agents use the service inventory configured in **System Console > Plugins > Agents**, and multiple agents can reuse the same service configuration. See [license requirements](#license-requirements) for details on features that require a license.

If you can manage an agent, select its row in the Agents list to open the full-page configuration view directly. The overflow menu remains available for **Edit** and **Delete**. Use **Back to agents** to return to the list.

When you create or edit an agent, use the three tabs in the full-page agent configuration view:

- **Configuration** for identity, model selection, instructions, and core capabilities
- **Access** for channel, team, and user restrictions, plus delegated agent admins
- **MCPs** for the agent's allowed MCP tools

If you have unsaved changes and try to leave the agent configuration view by selecting **Back to agents**, **Cancel**, or by pressing Escape, Mattermost shows a **Discard changes?** confirmation with **Discard** and **Keep editing**.

#### Configuration tab

| Setting | Description |
|---------|-------------|
| **Display Name** | User-facing name shown in Mattermost |
| **Agent Username** | The Mattermost username for the agent. @mentions use this name. Set it when creating the agent; it can't be changed later. |
| **Agent Avatar** | Custom image for the agent |
| **Service** | Select a configured Service from the dropdown |
| **Model** | (Optional) Override the service's default model for this agent |
| **Custom Instructions** | Custom instructions that define the agent's personality and capabilities |
| **Enable Vision** | Enable Vision to allow the agent to process images. Requires a compatible model and service. |
| **Enable Tools** | Enables tool use for integrations and other tool-based capabilities. Disable this only for models or use cases where tool calling shouldn't be available. Some features won't work without tools. |

#### LLM Specific Agent Settings

Some capabilities depend on the selected Service type and, for OpenAI Compatible and Azure, whether **Use Responses API** is enabled on that service.

| Setting | Description |
|---------|-------------|
| **Enable Web Search** | Available for Anthropic, OpenAI, Google Gemini, and Google Vertex AI. For OpenAI Compatible and Azure, this setting is available when **Use Responses API** is enabled on the Service. Gemini and Vertex map this to Google Search grounding via the provider's Responses API. Allows the Agent to leverage the provider's native web search tool to respond with recent information. |
| **Reasoning Enabled** | Available for Anthropic, OpenAI, Google Gemini, and Google Vertex AI. For OpenAI Compatible and Azure, this setting is available when **Use Responses API** is enabled on the Service. Enables extended thinking or reasoning capabilities for complex tasks. For Gemini / Vertex, Bifrost maps a token budget to `thinkingConfig.thinkingBudget` and an effort level to `thinkingConfig.thinkingLevel` on Gemini 3.0+. |
| **Structured Output** | Available for Anthropic, OpenAI, OpenAI Compatible, and Azure. When enabled and a JSON schema is provided in the request, the model returns structured JSON matching that schema. Compatible model support is still required. |

New agents enable native web search and structured output by default where the selected provider supports those features. For providers that don't support native tools, native tool selections are ignored.

For Anthropic services, **Structured Output** and extended thinking can't be used at the same time.

If you need an OpenAI-style endpoint without the Responses API path, use an **OpenAI Compatible** service and turn **Use Responses API** off for that service instead of using the **OpenAI** service type.

#### Access tab

Use this tab to control who can interact with and manage the agent:

- **Channel access** controls which channels the agent can be mentioned in
- **User access** controls which users can interact with the agent
- **Agent admins** can edit and delete the agent; the agent creator is always an admin

#### MCPs tab

Use this tab to control which MCP tools the agent can use. This tab is available only when **Enable Tools** is turned on.

- **Automatically enable all MCP tools** gives the agent access to every currently available MCP tool and any MCP tools added later.
- When **Automatically enable all MCP tools** is off, select the specific MCP tools the agent may use.
- If a previously selected MCP tool is no longer available, it is removed from the agent configuration when you save.

Updating an agent's display name also updates the linked Mattermost bot display name. Deleting an agent deactivates the linked Mattermost bot account.

Legacy bots previously stored in plugin configuration are migrated on startup into database-backed agents and then managed from the **Agents** page. Migrated agents don't have a creator and can be managed by system admins.

### Custom instructions

Text input in the custom instructions field is included in the prompt for every request. Use this to give your agents extra context or instructions. 

For example, you could list your organization's specific acronyms so the agent knows your vernacular and users can ask for definitions. Or you could give it specialized instructions like adopting a specific personality or following a certain workflow. By customizing the instructions for each individual agent, you can create a more tailored AI experience for your specific needs.

### Built-in web search configuration

The built-in web search tool lets agents retrieve current information from the internet when the model or deployment doesn't use the provider's own search. Prefer native provider web search when your service supports it.

#### When to use built-in web search

Built-in web search is intended for LLM models that lack native web search functionality. If your chosen model already provides native web search (such as OpenAI, Anthropic, Google Gemini, Google Vertex AI, or an OpenAI Compatible/Azure service with **Use Responses API** enabled), it's strongly recommended to use the provider's native implementation instead. Native web search tools typically offer:

- Better integration with the model
- More reliable search results
- Optimized performance

For configuration details on native web search with supported providers, see the [LLM Specific Agent Settings](#llm-specific-agent-settings) section above.

#### Provider comparison

Mattermost supports two web search providers, each with varying capabilities:

##### Brave Search (Recommended)

Brave Search offers a superior experience for AI-powered search:

- **Purpose-built for AI**: Brave's Search API is specifically designed and optimized for LLM integrations
- **Better content extraction**: Returns pre-processed, LLM-ready summaries with citations
- **Fewer tool calls**: Often provides complete answers without requiring follow-up web page fetches or scraping

**Important**: Administrators must ensure they subscribe to Brave's **Pro AI plan** when using this feature. Using Brave's regular Search API (non-AI tier) violates Brave's Terms of Service and may result in account suspension. The Pro AI plan is specifically licensed for AI/LLM use cases.

##### Google Custom Search

Google Custom Search provides access to Google's search index but has several important limitations:

- **Not a first-party integration**: Mattermost uses Google's Custom Search API, which doesn't provide the same quality of results as searching directly on google.com
- **Relies on web scraping**: After receiving search results, Mattermost must scrape web pages to extract content for the agent. Many websites block automated scraping or return limited content to bots
- **Rate limits**: Google Custom Search has strict daily quota limits

Due to these limitations, Google Custom Search may not always provide optimal results for agent queries.

#### Configuration

To enable built-in web search:

1. Navigate to **System Console > Plugins > Agents > Web Search**
2. Set **Enable Web Search** to **True**
3. Select your preferred provider from the **Provider** dropdown
4. Configure provider-specific settings

##### Brave Search configuration

| Setting | Description | Required |
|---------|-------------|----------|
| **Brave API Key** | Your Brave Search API key (Pro AI plan) | Yes |
| **Result Limit** | Maximum number of results to return (1-10) | No (default: 5) |
| **API URL** | Override the default Brave endpoint if needed | No |

To obtain a Brave Search API key:

1. Visit [Brave Search API](https://brave.com/search/api/)
2. Sign up for an account
3. **Subscribe to the Pro AI plan** (required for LLM usage)
4. Generate an API key from your dashboard

**Warning**: Ensure you subscribe to the Pro AI plan. Using other Brave Search plans for AI/LLM integrations violates their Terms of Service.

##### Google Custom Search configuration

| Setting | Description | Required |
|---------|-------------|----------|
| **Google API Key** | Your Google Custom Search API key | Yes |
| **Search Engine ID** | Custom search engine identifier (cx parameter) | Yes |
| **Result Limit** | Maximum number of results to return (1-10) | No (default: 5) |
| **API URL** | Override the default Google endpoint if needed | No |

To obtain Google Custom Search credentials:

1. Create a project in [Google Cloud Console](https://console.cloud.google.com)
2. Enable the Custom Search API
3. Create API credentials (API key)
4. Set up a custom search engine at [Google Programmable Search Engine](https://programmablesearchengine.google.com)
5. Note the Search Engine ID (cx parameter)

##### Shared configuration

| Setting | Description |
|---------|-------------|
| **Domain Denylist** | Comma-separated list of domains to exclude from all search results (e.g., `example.com, spam-site.org`). Results from these domains are filtered out before being sent to the agent. |

#### Usage and limitations

- Agents are limited to **3 web searches per conversation** to manage API costs and prevent LLMs from looping indefinitely
- Agents cannot repeat the same search query within a conversation
- Search results include clickable citations that link back to source websites
- Domain denylisting applies to all providers and is enforced for _web page fetching only_. 

### Embed search configuration

To enable semantic search capabilities, you'll need to enable the `pgvector` extension in your PostgreSQL database, then configure embeddings provider settings including the provider (OpenAI, etc.), model for embeddings, and dimensions that match your chosen embedding model. Embedding search requires a license (see [license requirements](#license-requirements)) and is available as an [experimental](https://docs.mattermost.com/manage/feature-labels.html#experimental) feature. Performance may vary with large datasets.

Configure chunking options based on your needs:

| Setting | Recommended Value | Description |
|---------|-------------------|-------------|
| **Chunking Strategy** | Sentences, Paragraphs, or Fixed Size | Choose based on your content type |
| **Chunk Size** | 512-1024 tokens | Varies by strategy |
| **Chunk Overlap** | 20-50 tokens | For better context continuity |

Run the initial indexing process after configuration.

### Permission configuration

Configure who can access AI features by setting team-level, channel-level, and user-level permissions for each agent.

## Management tasks

### Plugin metrics

Metrics for Agents are exposed through the `/plugins/mattermost-ai/metrics` subpath under the existing Mattermost server metrics endpoint. This is controlled by the Listen address for performance configuration setting. It defaults to port `8067`, and the following metrics are available:

- `agents_system_plugin_start_timestamp_seconds`: The time the plugin started.
- `agents_system_plugin_info`: The plugin version and installation ID.
- `agents_api_time_seconds`: How long to execute API.
- `agents_http_requests_total`: The total number of API requests.
- `agents_http_errors_total`: The total number of http API errors.
- `agents_llm_requests_total`: The total number of requests to upstream LLMs.

### Token usage tracking

The Agents plugin can track token usage for all LLM interactions to support billing and usage analytics. When enabled, token usage data is logged to a dedicated file at `logs/agents/token_usage.log` in JSON format, capturing detailed information about each request:

- **User ID**: The Mattermost user who initiated the request
- **Team ID**: The team context for the request
- **Bot Username**: Which agent was used for the interaction
- **Input Tokens**: Number of tokens in the request to the LLM
- **Output Tokens**: Number of tokens in the LLM response
- **Total Tokens**: Combined input and output token count

To enable token usage tracking, navigate to **System Console > Plugins > Agents** and set **Enable Token Usage Logging** to **True**. When enabled, log files automatically rotate when they reach 100MB in size, and rotated log files are compressed to save disk space. The token usage logs provide administrators with visibility into LLM usage patterns and can be used for cost tracking and resource planning. All major LLM providers (OpenAI, Anthropic) report usage data that gets captured by this logging system.

#### Converting token usage logs for analysis

The token usage log file contains one JSON object per line, which is not directly compatible with tools like Microsoft Excel. Use these commands to convert the logs to different formats. Each requires `jq` to be installed for easy JSON parsing:

**Convert to Excel-compatible JSON:**

```bash
jq -s '.' logs/agents/token_usage.log > token_usage.json
```

**Convert to CSV format:**

```bash
echo "timestamp,user_id,team_id,bot_username,input_tokens,output_tokens,total_tokens" > token_usage.csv
jq -r '[.timestamp, .user_id, .team_id, .bot_username, .input_tokens, .output_tokens, .total_tokens] | @csv' logs/agents/token_usage.log >> token_usage.csv
```

### Post indexing

Post indexing occurs automatically during initial setup and when changing embedding providers:

1. Navigate to **System Console > Plugins > Agents > Embedding Search**
2. Use the reindex controls to:
   
   - Monitor indexing progress during initial setup.
   - Trigger reindexing when changing embedding providers.
   - Check indexing status.

### OpenTelemetry tracing

The plugin supports distributed tracing via [OpenTelemetry](https://opentelemetry.io/) to provide visibility into request latency, LLM call performance, tool execution, and error diagnosis.

#### What gets traced

When enabled, the plugin creates spans for:

- **HTTP requests**: Every API call to the plugin, with method, route, and status code (via otelgin middleware)
- **LLM completions**: Provider, model, operation type, streaming status, input/output token counts, and errors
- **Tool execution**: Tool name, ID, resolution status, and errors for both built-in and MCP tools
- **MCP tool calls**: Remote MCP server and tool name
- **Semantic search**: Search queries and result retrieval
- **Web search**: Brave and Google search API calls
- **Post streaming**: Duration and context for streaming LLM responses to posts

Spans are organized in a parent-child hierarchy that follows the request flow, so a single user message produces a trace like:

```text
HTTP POST /post/:postid/react
  └── process user request
       ├── llm chat completion (provider=openai, model=gpt-4o, tokens=150/42)
       ├── resolve tool (tool=web_search)
       └── stream to post
```

#### Enabling tracing

The plugin offers three trace output modes, configurable via **Trace Output** in the System Console:

- **Off** — tracing disabled, zero overhead.
- **Server Logs** — finished spans are written to the Mattermost server log via the standard plugin logger. No collector required; pick this if you don't run Tempo, Jaeger, or another OTLP backend.
- **OTLP Endpoint** — spans are exported over OTLP gRPC to the endpoint configured in **OpenTelemetry Endpoint** (e.g. `localhost:4317`). Use this for full distributed tracing with a backend like Grafana Tempo or Jaeger.

The setting can also be configured directly in the plugin configuration JSON:

```json
{
  "telemetryOutput": "otlp",
  "openTelemetryEndpoint": "your-collector:4317"
}
```

Valid values for `telemetryOutput` are `off`, `logs`, and `otlp`. When set to `off` (or omitted), the plugin uses a no-op tracer with zero overhead. The `openTelemetryEndpoint` field is only consulted when the mode is `otlp`.

#### Local development with Grafana Tempo

For local development and debugging, use the included Docker Compose file to run [Grafana Tempo](https://grafana.com/oss/tempo/) and Grafana:

```bash
docker compose -f dev/docker-compose.otel.yml up -d
```

This starts:
- **Tempo** with OTLP gRPC on port `4317` and OTLP HTTP on port `4318`
- **Grafana** at `http://localhost:3001` with the Tempo datasource preprovisioned (anonymous Admin, no login required)

Configure the plugin with endpoint `localhost:4317`, then interact with the bot. Open Grafana → **Explore** → **Tempo** and search by service name `mattermost-ai-agents`, or paste a trace ID directly.

Grafana is mapped to port `3001` (not the default `3000`) so it does not collide with Mattermost's webapp dev server or the `mattermost-server` build/docker-compose stack.

To stop the stack:

```bash
docker compose -f dev/docker-compose.otel.yml down
```

Add `-v` to also discard accumulated traces.

#### Production deployment

For production, send traces to your existing OpenTelemetry Collector or directly to a backend:

- **OpenTelemetry Collector**: Point the endpoint to your collector's OTLP gRPC address. The collector can then export to Jaeger, Zipkin, Datadog, Grafana Tempo, AWS X-Ray, or any other supported backend.
- **Direct export**: Point the endpoint directly to a backend that supports OTLP gRPC (e.g., Grafana Tempo at `tempo:4317`).

The connection currently uses insecure (non-TLS) gRPC. For TLS-terminated endpoints, route through an OpenTelemetry Collector with TLS configured.

#### Custom span attributes

Traces include these semantic attributes for filtering and analysis:

| Attribute | Description | Example |
|-----------|-------------|---------|
| `agents.llm.provider` | LLM provider name | `openai`, `anthropic` |
| `agents.llm.model` | Model identifier | `gpt-4o`, `claude-3-opus` |
| `agents.llm.operation` | Operation type | `conversation`, `title_generation` |
| `agents.llm.input_tokens` | Input token count | `150` |
| `agents.llm.output_tokens` | Output token count | `42` |
| `agents.tool.name` | Tool being called | `web_search`, `read_channel` |
| `agents.tool.id` | Tool call identifier | `call_abc123` |
| `agents.mcp.server` | MCP server name | `github-server` |
| `agents.mcp.tool` | MCP tool name | `search_issues` |
| `agents.user.id` | Requesting user ID | `abc123def456` |
| `agents.channel.id` | Channel ID | `abc123def456` |
| `agents.post.id` | Post ID | `abc123def456` |
| `agents.thread.root_post.id` | Root post ID for thread correlation | `abc123def456` |

### Backup and restore

The plugin stores agent data across both plugin configuration and plugin database tables. To backup:

1. Ensure your regular Mattermost backup includes plugin configuration data.
2. Include plugin database tables in your normal backup and restore process. In particular:
   - `Agents_UserAgents` for agents created or managed from the **Agents** page
   - `LLM_CustomPrompts` and `LLM_CustomPromptPins` for custom prompt templates and prompt pins
3. For larger deployments, consider backing up indexed vector data separately.

Restoring only plugin configuration isn't sufficient to restore agents managed from the **Agents** page.

### Configuration format

The plugin uses a service-based architecture:

- `PluginSettings.Plugins["mattermost-ai"]["config"]` stores plugin-wide settings and AI service configurations, including `defaultBotName`
- Agents are stored separately in the `Agents_UserAgents` table

This separation allows multiple agents to share the same LLM service configuration while keeping agent lifecycle and access data out of `config.bots`.

**Configuration structure:**
```json
{
  "config": {
    "services": [
      {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "OpenAI Service",
        "type": "openai",
        "apiKey": "sk-...",
        "defaultModel": "gpt-4o"
      }
    ],
    "defaultBotName": "ai"
  }
}
```

**Supported service types:** `openai`, `anthropic`, `azure`, `openaicompatible`, `asage`, `cohere`, `mistral`, `scale`

**Legacy format:** Older configurations that stored bots in `config.bots`, or embedded service objects within bots, are migrated on plugin startup. After legacy bot migration completes, stored `config.bots` entries are removed to avoid duplicate bot registration.

## Troubleshooting

### Logging

Enhanced logging can help diagnose issues:

1. Check server logs for entries with the structured logging field `plugin_ai` set to `mattermost-ai`.
2. Enable **LLM Trace** in the plugin configuration to see detailed request/response information for all LLM interactions.
3. Enable debug logging in the plugin configuration for additional diagnostic information.
4. For production environments, disable debug logging and LLM Trace after troubleshooting to reduce log volume.

### Tool execution failures

When a tool call fails, the agent does not always stop immediately. It may continue with a follow-up model turn so it can recover, explain the failure, or answer without that tool.

To avoid endless retries, the plugin enforces a limit of **three consecutive failed tool attempts**. After that, no further tool calls are made for that sequence; the model is instructed to describe the latest error and ask the user for guidance or any missing information such as permissions, identifiers, or configuration details.

When users report repeated tool failures, use **LLM Trace** and debug logging to inspect tool errors and upstream responses. Also verify integration configuration such as API keys, endpoints, MCP connectivity, and third-party authorization, and confirm the user can access the underlying Mattermost resources the tool targets.

## Integrations

Integrations are available in direct messages by default. If you enable the experimental **Enable Channel Mention Tool Calling** setting, @mentioning an agent in a public channel can also allow tool calling there. Native provider web search in public and private channels is controlled separately by **Allow native web search in channels**.

## Model Context Protocol (MCP) Integration

The Model Context Protocol (MCP) integration lets Agents use tools exposed by MCP servers, including the embedded Mattermost tools and optional remote servers.

The MCP client and the embedded Mattermost MCP server are always enabled. Admins manage remote MCP servers, connection timeout, and per-tool enabled state and approval policies from the MCP UI in the System Console. Agent-level MCP access is configured separately on each agent's **MCPs** tab.

### Configuration

1. Navigate to **System Console > Plugins > Agents > Model Context Protocol (MCP)**.
2. Use the **Configuration** tab for:

   - **Enable Mattermost MCP Server (HTTP)**: Optional HTTP endpoint for external MCP clients. See [Mattermost MCP Server](#mattermost-mcp-server).
   - **Connection Idle Timeout (minutes)**: Timeout for inactive user MCP connections (default: 30 minutes).
   - Remote MCP servers, including URL, custom headers, OAuth client settings, and per-server enablement.

3. Use the **Tools** tab to review discovered tools and set each tool's enabled state and approval policy.
4. When creating or editing an agent on the **Agents** page, use the **MCPs** tab to choose whether that agent can use all MCP tools automatically or only a selected set of tools.

The **Tools** tab refreshes automatically after the current user connects or disconnects an OAuth-backed MCP server. Because MCP OAuth connections are per-user, this live refresh applies only to the user who completed the connect or disconnect action.

You can't disable MCP entirely from the System Console. To limit access, disable individual tools or change their policy in the **Tools** tab.


### Add MCP servers

1. On the **Configuration** tab, select **Add Remote MCP Server** to configure a new server.
2. Configure server settings:

   - **Server URL**: The endpoint URL for your MCP server.
   - **Custom Headers**: Additional headers required by your MCP server (optional).
   - **Server Name**: Descriptive name for the server (auto-generated if not provided).

3. Select **Save** to add the server.

### Configure OAuth-backed servers for agents

When you create or edit an agent from the **Agents** page, the **MCPs** tab in the full-page agent editor lists the MCP servers available to that agent. If an OAuth-backed server is not connected for your account yet, the row shows a **Connect** button so you can complete the provider sign-in flow without leaving the editor. The MCPs tab refreshes automatically after you connect or disconnect, so you don't need to reopen it to see updated server status.

If a disconnected OAuth-backed server currently exposes no tools, you can still toggle that server on while configuring the agent. Saving the agent in this state grants the agent access to every tool that server exposes after a user connects to that provider.

The **Automatically enable all MCP tools** option remains the broadest setting. When enabled, the agent can use every currently available MCP tool as well as MCP tools added later.

Enabling a server or tool for an agent controls what the agent is allowed to use, but it does not bypass tool approval policies. Tool execution still follows the policy configured in the **Tools** tab and each user's Mattermost and provider permissions.

### Management

- **Connection Management**: The system automatically manages user connections to MCP servers
- **Idle Cleanup**: Inactive client connections are automatically closed after the configured timeout
- **Per-User Connections**: Each user gets their own connection to MCP servers for security and isolation
- **Tool Policies**: Use the **Tools** tab to allow, require approval for, or disable individual tools
- **Agent Scoping**: The RHS **Tools** popover only shows MCP providers allowed for the selected agent. Tool use is still subject to admin tool policies and the user's Mattermost permissions

### OAuth-backed MCP servers

Some MCP servers require OAuth per Mattermost user. For those servers, the plugin exposes `needsOAuth` and `authURL` to the Agents webapp so the UI can show when authorization is required and where to begin the flow. The webapp starts OAuth through the plugin route `GET /plugins/mattermost-ai/mcp/oauth/<server name>/start` and can clear the current user's stored token with `DELETE /plugins/mattermost-ai/mcp/oauth/<server name>`.

**Agents panel (web and desktop):** In the Agents right-hand sidebar, start a new chat and open **Tools**. OAuth-backed servers show **Connect** when the signed-in user is not authenticated, and **Disconnect** when an OAuth session applies.

**System Console (admin tool configuration):** On **System Console > Plugins > Agents > MCP Servers**, expanding an OAuth-backed server shows that you must authenticate to fetch that server's tool list and configure per-tool approval policies. That sign-in only applies to your administrator account. Each end user must authenticate separately, even after an admin has connected in the System Console.

**Conversations:** The plugin no longer posts ephemeral in-channel or in-thread messages to prompt MCP OAuth. Users should use the Agents webapp **Tools** menu to view connection state and run **Connect** or **Disconnect**.

**Mobile and other clients:** MCP OAuth is not initiated from the mobile app or other clients that do not use the Agents webapp. Users need Mattermost web or desktop to connect OAuth-backed MCP servers.

**Custom MCP OAuth setups:** If the OAuth start URL includes a `resource_metadata` query parameter, it is accepted only when its origin matches the origin of the configured MCP server **Server URL**. This prevents cross-origin metadata injection during discovery.

### Atlassian MCP server authorization

When users connect to the Atlassian MCP server, they may encounter an authorization error requiring an organization admin to authorize your Mattermost domain. This configuration must be completed in Atlassian's admin console.

**To authorize your Mattermost domain:**

1. Go to [admin.atlassian.com](https://admin.atlassian.com) and select your organization.
2. Go to **Apps > AI settings > Rovo MCP server**.
3. Select **Add domain** and enter your Mattermost domain with the path wildcard: `https://your-instance.mattermost.cloud/**`
4. Select **Save**.

**Important:** The `/**` path wildcard is required. Example domain patterns:
- Single instance: `https://your-company.mattermost.cloud/**`
- All subdomains: `https://*.mattermost.cloud/**`
- Custom domain: `https://chat.yourcompany.com/**`

After adding the domain, wait 1-2 minutes for changes to propagate before users retry the connection.

**Troubleshooting:**
- Verify you have Organization Admin permissions (Site Admin is insufficient)
- Confirm you're configuring the organization that owns the Atlassian site
- Ensure the domain includes `https://` and the `/**` wildcard
- Check for typos in the domain

For more information, see [Atlassian's documentation on MCP server settings](https://support.atlassian.com/security-and-access-policies/docs/control-atlassian-rovo-mcp-server-settings/).

> **Note:** By default, the plugin doesn't render AI-generated Markdown links (for example, JIRA ticket links) as clickable links. URLs are displayed in plain text to reduce potential phishing and data exfiltration risk. If an admin enables **Render AI-generated links** (`allowUnsafeLinks`), AI-generated links may become clickable; enable this only with appropriate trust boundaries and security mitigations in place.

## Mattermost MCP Server

The Mattermost MCP Server enables AI agents and external applications to interact with your Mattermost instance through the Model Context Protocol (MCP). This is a standardized protocol that allows AI assistants to read messages, search content, create posts, and manage channels and teams programmatically.

**Standalone MCP server (separate process / stdio):** Running the standalone `mattermost-mcp-server` binary outside the Mattermost server is for **development and local use only** and is **not** intended for production. Production deployments should rely on the embedded Mattermost MCP server and the supported configuration in this plugin (System Console, HTTP endpoint for external clients, and agent MCP settings below).

### Overview

The Mattermost MCP Server provides:

- **Direct Mattermost Integration**: AI agents can access your Mattermost data and functionality through a standardized API
- **Security and Permissions**: All operations respect Mattermost's permission system - users only access what they're authorized to see
- **Flexible Deployment**: Available as an embedded server for Mattermost AI agents or as an HTTP server for external MCP clients
- **Rich Toolset**: Comprehensive tools for reading, searching, and creating content

**Use Cases**

With the Mattermost MCP Server, you can:

- **Automate Channel Summaries**: Ask your AI agent to summarize activity across channels, catching up on discussions while you were away.
- **Share Updates Across Channels**: Have your agent post status updates to multiple channels simultaneously, keeping distributed teams synchronized.
- **Search Intelligently**: Search across your entire Mattermost workspace from any MCP-enabled client to find relevant discussions, decisions, or information.
- **Coordinate Teams**: Get lists of channel or team members to quickly identify who to contact or mention.
- **Automate Workflows**: Use external MCP clients to automate routine tasks like posting stand-up updates, creating project channels, or notifying teams of important events.
- **Access Context-Aware Assistance**: AI agents can read conversation threads to understand context before responding or taking action.

### Available Tools

The MCP server provides the following tools to AI agents and external clients:

- **read_post**: Read a specific post and its thread
- **read_channel**: Retrieve recent posts from a channel
- **search_posts**: Search across Mattermost content with optional team/channel filters
- **create_post**: Create new posts or replies in channels
- **dm**: Send a direct message to a user
- **group_message**: Send a message to a group conversation
- **create_channel**: Create new public or private channels
- **get_channel_info**: Retrieve channel details by ID or name
- **get_team_info**: Retrieve team details by ID or name
- **search_users**: Find users by username, email, or name
- **get_channel_members**: List all members of a channel
- **add_user_to_channel**: Add a user to a channel
- **get_user_channels**: List the channels the current user is a member of
- **get_team_members**: List all members of a team
- **list_agents**: List the AI agents (bots) available to the current user, including each agent's ID, display name, and username

When the Channel Automation plugin is installed, the MCP server also exposes the following tools. They proxy requests to that plugin; execution follows the same MCP tool policies as other tools and each user's Mattermost permissions.

- **list_automations**: List channel automations, filter them by channel, or retrieve a specific automation by ID
- **get_automation_instructions**: Retrieve the Channel Automation plugin's current automation authoring guidance
- **create_automation**: Create a channel automation
- **update_automation**: Update a channel automation
- **delete_automation**: Delete a channel automation

### Deployment

#### For AI Agents

The embedded Mattermost MCP server is available automatically to configured AI agents. No System Console switch is required to enable embedded MCP for in-product agents.

Use **System Console > Plugins > Agents > Model Context Protocol (MCP)** to configure remote MCP servers, the idle timeout, the optional HTTP endpoint for external clients, and per-tool enablement and approval policies. Then use each agent's **MCPs** tab on the **Agents** page to either automatically enable all MCP tools or restrict that agent to specific tools.

Configured agents can use these tools subject to their own MCP settings, admin tool policies, user permissions, and any required approval flow.

#### For External Clients

You can enable external MCP clients, such as Claude web, Claude Code, or other MCP-compatible applications, to interact with your Mattermost instance. This HTTP server is separate from the always-on embedded MCP server used by Mattermost Agents.

**Requirements:**
- Mattermost Server v11.2 or later
- Valid authentication method (OAuth or Personal Access Token)

**Note:** The server uses streamable HTTP transport and does not support traditional Server-Sent Events (SSE) transport. External clients must use the streamable HTTP transport available at the `/mcp` endpoint.

To enable an external MCP client:

1. Go to **System Console > Plugins > Agents > Model Context Protocol (MCP)**
2. Set **Enable Mattermost MCP Server (HTTP)** to **True**.
The MCP server will be available at: `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/mcp`

**Authentication:**

*OAuth 2.0*

The MCP server supports OAuth 2.0 authentication with both manual and automatic client registration.

**Prerequisites:**
- Enable OAuth 2.0 service provider in **System Console > Integrations > Integration Management**:
  - Set **Enable OAuth 2.0 Service Provider** to **True**
  - For automatic client registration, set **Enable OAuth 2.0 Dynamic Client Registration** to **True** (Note: DCR is an unprotected endpoint, meaning it is publicly accessible and does not require authentication—anyone can register OAuth clients if this feature is enabled. See the [OAuth 2.0 documentation](https://developers.mattermost.com/integrate/apps/authentication/oauth2/) for security considerations.)

**Client Registration Methods:**
- **Dynamic Client Registration (DCR/RFC 7591)**: External clients can automatically register and obtain credentials without manual setup.
- **Manual Registration**: Create OAuth applications through **Product menu > Integrations > OAuth 2.0 Applications**. See the [OAuth 2.0 documentation](https://developers.mattermost.com/integrate/apps/authentication/oauth2/) for details.

**Additional Details:**
- Supports both public clients (e.g., desktop applications) and confidential clients (e.g., server applications)
- Authorization through standard Mattermost OAuth flows
- OAuth metadata endpoints:
  - Protected resource metadata: `https://your-mattermost-server/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource`
  - Authorization server metadata: `https://your-mattermost-server/.well-known/oauth-authorization-server`

*Personal Access Tokens*

You can authenticate using Mattermost Personal Access Tokens (PAT):

1. Create a Personal Access Token in Mattermost (**User Settings > Security > Personal Access Tokens**).
2. Configure your MCP client to use Bearer token authentication with the PAT.

### License requirements

The following table outlines which features require a license:

| Feature | License Required |
|---------|------------------|
| Basic agent configuration (single agent) | No license required |
| Chat with agents in DMs and channels | No license required |
| Image analysis (vision capabilities) | No license required |
| Basic tool integrations | No license required |
| Multiple agent configurations | Entry, Enterprise, and Enterprise Advanced |
| Fine-grained access controls | Entry, Enterprise, and Enterprise Advanced |
| Embedding search (semantic AI search) | Entry, Enterprise, and Enterprise Advanced |
| MCP Support | Entry, Enterprise, and Enterprise Advanced |
| Usage analytics and token tracking | Entry, Enterprise, and Enterprise Advanced |
| AI Actions menu (thread summarization) | Entry, Enterprise, and Enterprise Advanced |
| Channel summarization (unread messages) | Entry, Enterprise, and Enterprise Advanced |
| Recorded meeting transcripts and summarization | Entry, Enterprise, and Enterprise Advanced |

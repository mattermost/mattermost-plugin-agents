<div align="center">

# Mattermost Agents Plugin [![Download Latest Master Build](https://img.shields.io/badge/Download-Latest%20Master%20Build-blue)](https://github.com/mattermost/mattermost-plugin-agents/releases/tag/latest-master)

Self-hosted AI agents for [Mattermost](https://github.com/mattermost/mattermost) that act on your behalf through tools and MCP servers, plus a built-in Mattermost MCP server for external AI clients such as Claude Code, Claude Desktop, Cursor, and VS Code.

</div>

![The Mattermost Agents AI Plugin is an extension for mattermost that provides functionality for self-hosted and vendor-hosted LLMs](img/mattermost-ai-llm-access.webp)

Mattermost Agents (plugin ID `mattermost-ai`) adds AI agents to a Mattermost server. You talk to an agent in a direct message, the Agents pane, or a channel. The agent can search and post in Mattermost, call tools on other systems through the Model Context Protocol (MCP), and work with files, all with the permissions of the person it is acting for. You choose the LLM, including models that run on your own infrastructure, so with a self-hosted model, conversations and data stay in your environment.

**Does Mattermost have an official MCP server?** Yes. It is built into this plugin and needs no separate installation. After an admin enables it, MCP clients connect to `/plugins/mattermost-ai/mcp-server/mcp` on your Mattermost server and sign in as a Mattermost user. See [Mattermost MCP Server](docs/mcp_server.md).

## Key Features

- **Agents that act on your behalf**: Chat with a personal agent that searches and reads Mattermost, posts and sends messages, schedules posts and reminders, and uses tools from other systems. Configure multiple agents with their own instructions, models, and tool access. See [Use Mattermost Agents as your personal AI assistant](docs/personal_agent.md).
- **Tools and MCP**: Agents use built-in Mattermost tools, remote MCP servers with per-user OAuth (with a **Connect** card in the conversation), and tools registered by other Mattermost plugins. Dynamic tool loading keeps large tool catalogs out of the model's context.
- **Built-in Mattermost MCP server**: Connect Claude Code, Claude Desktop, Cursor, VS Code, or any MCP client that supports streamable HTTP to Mattermost, using OAuth with dynamic client registration or a personal access token. Over 100 tools run with the signed-in user's own permissions. See [Mattermost MCP Server](docs/mcp_server.md).
- **Governance**: Per-tool approval policies, user, team, and channel access controls for agents, attribute-based access control, server audit logging, token usage tracking, and OpenTelemetry tracing. Admins decide which tools exist and which need approval. See the [Admin Guide](docs/admin_guide.md).
- **Bring your own LLM**: Use local models through OpenAI-compatible APIs (Ollama, vLLM), or OpenAI, Anthropic, AWS Bedrock, Azure OpenAI, Google Gemini and Vertex AI, Cohere, Mistral, and Scale AI. See [LLM Provider Configuration](docs/providers.md) and [Implementing Sovereign AI](docs/sovereign_ai.md).
- **Summaries, search, and meetings**: Summarize threads, unread messages, and channels; ask questions about a channel; search with keyword and semantic search; and summarize Mattermost Calls and Zoom meeting recordings.
- **Files and artifacts**: Agents create text files attached to their replies, read attachments, analyze images with vision-capable models, and render HTML artifacts in the conversation.
- **Channel use**: @mention an agent in a channel, or set a channel to auto-reply. Tool results in channels can be shared or kept private by the person who asked.
- **Custom prompts**: Save reusable prompts, pin them in the Agents pane, and share them with your team.

Some features depend on your Mattermost license level. See the [license requirements](docs/admin_guide.md#license-requirements).

## Documentation

Documentation is available in the [`docs`](docs/) directory:

- [User Guide](docs/user_guide.md): Use agents, tools, summaries, search, and meeting summaries
- [Use Mattermost Agents as your personal AI assistant](docs/personal_agent.md): What a personal agent can do across channels, MCP servers, and integrations
- [Admin Guide](docs/admin_guide.md): Installation, configuration, MCP servers, tool policies, access control, audit logging, tracing, and license requirements
- [Mattermost MCP Server](docs/mcp_server.md): Connect Claude Code, Claude Desktop, Cursor, VS Code, and other MCP clients to Mattermost
- [LLM Provider Configuration](docs/providers.md): Configuration for supported LLM providers
- [AWS Bedrock Setup](docs/aws_bedrock_setup.md): Use Amazon Bedrock with IAM instance profiles
- [Implementing Sovereign AI](docs/sovereign_ai.md): Deploy agents with local models and data control
- [Usage Tips](docs/usage_tips.md): Voice dictation, prompting techniques, and workflow tips
- [Upgrading to Agents V2](docs/upgrading_to_2.0.md): Upgrade path, automatic migrations, and breaking changes from v1.x
- [Load Testing](docs/load-testing.md): Configure the in-process mock LLM and load-test-ng Agents controller
- Feature documentation:
  - [Managing Agents](docs/features/managing_agents.md): Create, edit, and version agents and control their access and MCP tools
  - [Custom Prompts](docs/features/custom_prompts.md): Save, share, and pin reusable prompts
  - [Channel Summaries](docs/features/channel_summaries.md): Use the **Ask Agents about this channel** button to summarize and analyze channel activity
  - [Multiplayer Tool Calling](docs/features/multiplayer_tool_calling.md): Tool approval, credentials, and result visibility in shared channels

## Installation

1. Download the latest release from the [releases page](https://github.com/mattermost/mattermost-plugin-agents/releases). You can also download the **experimental** [latest master](https://github.com/mattermost/mattermost-plugin-agents/releases/tag/latest-master)
2. Upload and enable the plugin through the Mattermost System Console
3. Configure your desired LLM provider settings

### System Requirements

- Mattermost Server v12.0.0+
- PostgreSQL database with pgvector extension for semantic search capabilities
- Network access to your chosen LLM provider

## Quick Start

After installation, complete these steps to get started:

1. Navigate to **System Console > Plugins > Agents**
2. Create an agent and configure it with your LLM provider credentials
3. Set permissions for who can access the agent
4. Open the Agents panel from any channel using the AI icon in the right sidebar
5. Start interacting with your AI assistant

For detailed configuration instructions, see the [Admin Guide](docs/admin_guide.md).

## Integration

### Bridge Client

The plugin provides a Go client library for other Mattermost plugins and the Mattermost server to interact with the AI plugin's LLM Bridge API. This allows you to easily add AI capabilities to your own plugins or server-side features.

See the [Bridge Client README](public/bridgeclient/README.md) for installation and usage instructions.

## Development

### Prerequisites

- Go 1.27+
- Node.js 24.11+
- Access to an LLM provider (OpenAI, Anthropic, etc.)

### Local Setup

1. Setup your Mattermost development environment by following the [Mattermost developer setup guide](https://developers.mattermost.com/contribute/server/developer-setup/). If you have a remote mattermost server you want to develop to you can skip this step. 

2. Setup your Mattermost plugin development environment by following the [Plugin Developer setup guide](https://developers.mattermost.com/integrate/plugins/developer-setup/).

3. Clone the repository:
```bash
git clone https://github.com/mattermost/mattermost-plugin-agents.git
cd mattermost-plugin-agents
```

4. **Optional**. If you are developing to a remote server, setup environment variables to deploy:
```bash
MM_SERVICESETTINGS_SITEURL=http://localhost:8065
MM_ADMIN_USERNAME=<YOUR_USERNAME>
MM_ADMIN_PASSWORD=<YOUR_PASSWORD>
```

5. Run deploy to build the plugin
```bash
make deploy
```

### Other make commands

- Run `make help` for a list of all make commands
- Run `make check-style` to verify code style
- Run `make test` to run the test suite
- Run `make e2e` to run the e2e tests
- Run `make evals` to run prompt evaluations interactively (with TUI)
- Run `make evals-ci` to run prompt evaluations in CI mode (non-interactive)

### Benchmark Tests

The streaming code has benchmark tests to measure performance and detect regressions:

```bash
# Run all streaming benchmarks
go test -bench=. -benchmem ./llm/... ./streaming/...

# Run specific benchmark
go test -bench=BenchmarkStreamToPost -benchmem ./streaming/...

# Run with CPU profiling
go test -bench=BenchmarkReadAll -cpuprofile=cpu.prof ./llm/...
```

Benchmarks cover:
- `ReadAll()` stream consumption with varying response sizes
- `TokenUsageLoggingWrapper` interception overhead
- `StreamToPost()` full processing pipeline with WebSocket events

### Multi-Provider Evaluation Support

The evaluation system supports testing with multiple LLM providers: OpenAI, Anthropic, and Azure OpenAI. By default, evaluations run against all providers, but you can target specific ones:

```bash
# Run with all providers (default)
make evals

# Run with only Anthropic
LLM_PROVIDER=anthropic make evals

# Run with OpenAI and Azure
LLM_PROVIDER=openai,azure make evals

# Use a specific model
ANTHROPIC_MODEL=claude-opus-5-5 make evals
```

See `cmd/evalviewer/README.md` for complete documentation on environment variables and configuration options.


## License

This repository is licensed under [Apache-2](./LICENSE.txt), except for the [enterprise](enterprise) directory which is licensed under the [Mattermost Source Available License](enterprise/LICENSE). See [Mattermost Source Available License](https://docs.mattermost.com/overview/faq.html#mattermost-source-available-license) to learn more.

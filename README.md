<div align="center">

# Mattermost Agents Plugin [![Download Latest Master Build](https://img.shields.io/badge/Download-Latest%20Master%20Build-blue)](https://github.com/mattermost/mattermost-plugin-ai/releases/tag/latest-master)

The Mattermost Agents Plugin integrates AI capabilities directly into your [Mattermost](https://github.com/mattermost/mattermost) workspace. **Run any local LLM** on your infrastructure or connect to cloud providers - you control your data and deployment.

</div>

![The Mattermost Agents AI Plugin is an extension for mattermost that provides functionality for self-hosted and vendor-hosted LLMs](img/mattermost-ai-llm-access.webp)

## Key Features

- **Multiple AI Assistants**: Configure different agents with specialized personalities and capabilities
- **Thread & Channel Summarization**: Get concise summaries of long discussions with a single click
- **Action Item Extraction**: Automatically identify and extract action items from threads
- **Meeting Transcription**: Transcribe and summarize meeting recordings
- **Semantic Search**: Find relevant content across your Mattermost instance using natural language
- **Smart Reactions**: Let AI suggest contextually appropriate emoji reactions
- **Direct Conversations**: Chat directly with AI assistants in dedicated channels
- **Flexible LLM Support**: Use local models (Ollama, vLLM, etc.), cloud providers (OpenAI, Anthropic, Azure), or any OpenAI-compatible API

## Documentation

Comprehensive documentation is available in the `/docs` directory:

- [User Guide](docs/user_guide.md): Learn how to interact with AI features
- [Admin Guide](docs/admin_guide.md): Detailed installation and configuration instructions
- [Provider Setup](docs/providers.md): Configuration for supported LLM providers
- [Feature Documentation](docs/features/): Detailed guides for individual features

## Installation

1. Download the latest release from the [releases page](https://github.com/mattermost/mattermost-plugin-ai/releases). You can also download the **experimental** [latest master](https://github.com/mattermost/mattermost-plugin-ai/releases/tag/latest-master)
2. Upload and enable the plugin through the Mattermost System Console
3. Configure your desired LLM provider settings

### System Requirements

- Mattermost Server versions:
  - v10.0 or later recommended
  - v9.11+ (ESR)
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

- Go 1.24+
- Node.js 20.11+
- Access to an LLM provider (OpenAI, Anthropic, etc.)

### Local Setup

1. Setup your Mattermost development environment by following the [Mattermost developer setup guide](https://developers.mattermost.com/contribute/server/developer-setup/). If you have a remote mattermost server you want to develop to you can skip this step. 

2. Setup your Mattermost plugin development environment by following the [Plugin Developer setup guide](https://developers.mattermost.com/integrate/plugins/developer-setup/).

3. Clone the repository:
```bash
git clone https://github.com/mattermost/mattermost-plugin-ai.git
cd mattermost-plugin-ai
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


## License

This repository is licensed under [Apache-2](./LICENSE), except for the [server/enterprise](server/enterprise) directory which is licensed under the [Mattermost Source Available License](LICENSE.enterprise). See [Mattermost Source Available License](https://docs.mattermost.com/overview/faq.html#mattermost-source-available-license) to learn more.

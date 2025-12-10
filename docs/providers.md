# LLM Provider Configuration Guide

This guide covers configuring different Large Language Model (LLM) providers with the Mattermost Agents plugin. Each provider has specific configuration requirements and capabilities.

## Supported Providers

The Mattermost Agents plugin currently supports these LLM providers:

- Local models via OpenAI-compatible APIs (Ollama, vLLM, etc.)
- OpenAI
- Anthropic
- Cohere
- Mistral
- Azure OpenAI

## General Configuration Concepts

For any LLM provider, you'll need to configure API authentication (keys, tokens, or other authentication methods), model selection for different use cases, parameters like context length and token limits, and ensure proper connectivity to provider endpoints.

## Local Models (OpenAI Compatible)

The OpenAI Compatible option allows integration with any OpenAI-compatible LLM provider, such as [Ollama](https://ollama.com/):

### Configuration

1. Deploy your model, for example, on [Ollama](https://ollama.com/)
2. Select **OpenAI Compatible** in the **AI Service** dropdown
3. Enter the URL to your AI service from your Mattermost deployment in the **API URL** field. Be sure to include the port, and append `/v1` to the end of the URL if using Ollama. (e.g., `http://localhost:11434/v1` for Ollama, otherwise `http://localhost:11434/`)
4. If using Ollama, leave the **API Key** field blank
5. Specify your model name in the **Default Model** field

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API URL** | Yes | The endpoint URL for your OpenAI-compatible API |
| **API Key** | No | API key if your service requires authentication |
| **Default Model** | Yes | The model to use by default |
| **Organization ID** | No | Organization ID if your service supports it |
| **Send User ID** | No | Whether to send user IDs to the service |

### Special Considerations

Ensure your self-hosted solution has sufficient compute resources and test for compatibility with the Mattermost plugin. Some advanced features may not be available with all compatible providers, so adjust token limits based on your deployment's capabilities.

## OpenAI

### Authentication

Obtain an [OpenAI API key](https://platform.openai.com/account/api-keys), then select **OpenAI** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API. If your API key belongs to an OpenAI organization, you can optionally specify your **Organization ID**.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your OpenAI API key |
| **Organization ID** | No | Your OpenAI organization ID |
| **Default Model** | Yes | The model to use by default (see [OpenAI's model documentation](https://platform.openai.com/docs/models)) |
| **Send User ID** | No | Whether to send user IDs to OpenAI |

## Anthropic (Claude)

### Authentication

Obtain an [Anthropic API key](https://console.anthropic.com/settings/keys), then select **Anthropic** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Anthropic API key |
| **Default Model** | Yes | The model to use by default (see [Anthropic's model documentation](https://docs.anthropic.com/claude/docs/models-overview)) |

## Cohere

### Authentication

Obtain a [Cohere API key](https://dashboard.cohere.com/api-keys), then select **Cohere** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Cohere API key |
| **Default Model** | Yes | The model to use by default (see [Cohere's model documentation](https://docs.cohere.com/docs/models)) |

## Mistral

### Authentication

Obtain a [Mistral API key](https://console.mistral.ai/api-keys/), then select **Mistral** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Mistral API key |
| **Default Model** | Yes | The model to use by default (see [Mistral's model documentation](https://docs.mistral.ai/getting-started/models/)) |

## Azure OpenAI

### Authentication

For more details about integrating with Microsoft Azure's OpenAI services, see the [official Azure OpenAI documentation](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview).

1. Provision sufficient [access to Azure OpenAI](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview#how-do-i-get-access-to-azure-openai) for your organization and access your [Azure portal](https://portal.azure.com/)
2. If you do not already have one, deploy an Azure AI Hub resource within Azure AI Studio
3. Once the deployment is complete, navigate to the resource and select **Launch Azure AI Studio**
4. In the side navigation pane, select **Deployments** under **Shared resources**
5. Select **Deploy model** then **Deploy base model**
6. Select your desired model and select **Confirm**
7. Select **Deploy** to start your model
8. In Mattermost, select **OpenAI Compatible** in the **Service** dropdown
9. In the **Endpoint** panel for your new model deployment, copy the base URI of the **Target URI** (everything up to and including `.com`) and paste it in the **API URL** field in Mattermost
10. In the **Endpoint** panel for your new model deployment, copy the **Key** and paste it in the **API Key** field in Mattermost
11. In the **Deployment** panel for your new model deployment, copy the **Model name** and paste it in the **Default Model** field in Mattermost

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Azure OpenAI API key |
| **API URL** | Yes | Your Azure OpenAI endpoint |
| **Default Model** | Yes | The model to use by default (see [Azure OpenAI's model documentation](https://learn.microsoft.com/en-us/azure/ai-services/openai/concepts/models)) |
| **Send User ID** | No | Whether to send user IDs to Azure OpenAI |

# LLM Provider Configuration Guide

This guide covers configuring different Large Language Model (LLM) providers with the Mattermost Agents plugin. Each provider has specific configuration requirements and capabilities.

## Supported Providers

The Mattermost Agents plugin currently supports these LLM providers:

- Local models via OpenAI-compatible APIs (Ollama, vLLM, etc.)
- OpenAI
- Anthropic
- AWS Bedrock
- Cohere
- Mistral
- Scale AI
- Azure OpenAI
- Google Gemini
- Google Vertex AI

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
| **Use Responses API** | No | Defaults to enabled. Uses the OpenAI Responses API when supported. Turn off for legacy Chat Completions compatibility with endpoints that do not implement the Responses API. |

### Special Considerations

Ensure your self-hosted solution has sufficient compute resources and test for compatibility with the Mattermost plugin. Some advanced features may not be available with all compatible providers, so adjust token limits based on your deployment's capabilities. If you need OpenAI-compatible behavior without the Responses API, use **OpenAI Compatible** with **Use Responses API** disabled instead of the **OpenAI** service type.

## OpenAI

### Authentication

Obtain an [OpenAI API key](https://platform.openai.com/account/api-keys), then select **OpenAI** in the **Service** dropdown and enter your API key. Specify a model name in the **Default Model** field that corresponds with the model's label in the API. If your API key belongs to an OpenAI organization, you can optionally specify your **Organization ID**.

Direct **OpenAI** services always use the OpenAI **Responses** API. There is no System Console setting to disable the Responses API for this service type.

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

## AWS Bedrock

AWS Bedrock provides access to foundation models from Anthropic (Claude), Amazon (Nova, Titan), and other providers via a unified API. For full setup instructions — including IAM policy configuration and Anthropic-specific Claude requirements — see the [AWS Bedrock Setup Guide](aws_bedrock_setup.md).

### Authentication

The plugin uses the [AWS SDK default credential chain](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html). For Mattermost servers running on EC2, attach an IAM instance profile to your instance and leave all credential fields blank — the SDK discovers credentials automatically. For non-EC2 deployments, enter an `AWS Access Key ID` and `AWS Secret Access Key`, or a short-term Bedrock console API key.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **AWS Region** | Yes | AWS region where Bedrock is available (e.g., `us-east-1`, `us-west-2`, `eu-central-1`) |
| **Custom Endpoint URL** | No | Optional custom endpoint for VPC endpoints or proxies. Leave blank for standard AWS endpoints. |
| **AWS Access Key ID** | No | IAM user access key ID for long-term credentials. Takes precedence over API Key if both are set. |
| **AWS Secret Access Key** | No | IAM user secret access key. Required if AWS Access Key ID is provided. |
| **API Key** | No | Bedrock console API key (base64 encoded, format: `ABSKQm...`). If IAM credentials above are set, they take precedence. |
| **Default Model** | Yes | The Bedrock model ID to use (e.g., `us.anthropic.claude-sonnet-4-6`). See the [AWS Bedrock model IDs documentation](https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html) for the full list of available models and their IDs. Model availability varies by AWS region. |

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

## Scale AI

### Overview

Scale AI (including ScaleGov) provides access to LLM models through an OpenAI-compatible API with custom authentication. Scale uses `x-api-key` and `x-selected-account-id` headers for authentication instead of the standard Authorization header.

### Authentication

Obtain your Scale AI API key and account ID from your Scale AI or ScaleGov dashboard, then select **Scale AI** in the **Service** dropdown. Enter your API key and the API URL for your Scale endpoint (e.g., `https://sgp-api.scalegov.com/v5`). If using ScaleGov, enter your account ID in the **Account ID** field.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Scale AI API key (sent as `x-api-key` header) |
| **API URL** | Yes | Your Scale API endpoint (e.g., `https://sgp-api.scalegov.com/v5`) |
| **Account ID** | No | Your Scale account ID (sent as `x-selected-account-id` header, required for ScaleGov) |
| **Default Model** | Yes | The model to use by default in `vendor/model-name` format (e.g., `openai/gpt-4o`) |

### Models

Models use the `vendor/model-name` format (e.g., `openai/gpt-4o`). For the full list of available models, see the [Scale AI documentation](https://scale.com/docs).

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
8. In Mattermost, select **Azure** in the **Service** dropdown
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
| **Use Responses API** | No | Defaults to enabled. Uses the OpenAI Responses API when your Azure deployment supports it. Turn off for legacy Chat Completions compatibility if your endpoint or deployment does not support the Responses API. |

## Google Gemini

Google Gemini uses the Generative Language API (AI Studio), which authenticates with a single API key. Both Gemini and Vertex AI route through Bifrost, the plugin's unified LLM gateway. If you need enterprise GCP authentication, project/region scoping, or VPC-SC, use **Google Vertex AI** instead.

### When to choose Gemini vs. Vertex AI

| Use **Google Gemini** when… | Use **Google Vertex AI** when… |
|-----------------------------|--------------------------------|
| You want the simplest setup — single API key from AI Studio | You need GCP-scoped billing, IAM, or audit logging |
| You're testing models or running a small team | You need region pinning, VPC-SC, or private connectivity |
| You don't have a GCP project | You already have a GCP project and want to centralize spend |
| Fine to call `generativelanguage.googleapis.com` directly | You need Anthropic Claude models served through Vertex |

### Setup

1. Sign in to [Google AI Studio](https://aistudio.google.com/) and create an API key at [aistudio.google.com/apikey](https://aistudio.google.com/apikey).
2. In Mattermost, open **System Console > Agents > LLM Services** and add a new service (or open an existing Gemini service).
3. Select **Google Gemini** in the **Service** dropdown.
4. Paste your AI Studio key into the **API Key** field.
5. Enter a model identifier in the **Default Model** field (for example, `gemini-2.5-pro` or `gemini-2.5-flash`). Use [Google's model catalog](https://ai.google.dev/gemini-api/docs/models) to confirm the exact ID.
6. Save the service. There is no API URL field in the System Console for the Gemini service type. The underlying `apiURL` config field is accepted by the API and forwarded to Bifrost as the base URL if non-empty, enabling egress-proxy use cases for operators who configure services programmatically.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **API Key** | Yes | Your Gemini API key from AI Studio. Stored as a secret. |
| **Default Model** | Yes | The model to use by default (see [Gemini model documentation](https://ai.google.dev/gemini-api/docs/models)). Common choices: `gemini-2.5-pro`, `gemini-2.5-flash`. |
| **Input Token Limit** | No | Optional override for the maximum input context size, in tokens. Leave blank to use the model's default. |
| **Output Token Limit** | No | Optional override for the maximum output (`max_tokens`) the plugin will request. |

> The **Send User ID** and **Use Responses API** toggles are not exposed for the **Google Gemini** service type. Bifrost automatically switches to the Responses API path when you enable a native Google tool or when an agent or feature requires native web search; in all other cases the Chat path is used.

### Reasoning (thinking) and model-version mapping

Gemini supports provider-native reasoning ("thinking") through Bifrost. Enable **Reasoning** on the agent (System Console > Agents > select agent > **Config** tab) to turn it on. The configuration controls behave differently across model generations:

| Model generation | What **Thinking Budget** does | What **Reasoning Effort** does |
|------------------|-------------------------------|--------------------------------|
| Gemini 2.5 (Pro, Flash, …) | Sets `thinkingConfig.thinkingBudget` directly (token count). | Translated into an *estimated* `thinkingBudget` — the effort selector ("minimal" / "low" / "medium" / "high") is not natively supported on 2.5, so Bifrost maps it to a budget. |
| Gemini 3.0+ | Sets `thinkingConfig.thinkingBudget` directly. | Sets `thinkingConfig.thinkingLevel` (the native 3.0+ field) using the same minimal/low/medium/high values. |

When **both** Thinking Budget and Reasoning Effort are set, the explicit thinking budget wins for both Chat and Responses API paths. The default effort when nothing is set is `"medium"`.

Reasoning works on both the Chat Completions and Responses API paths; on the Responses path Bifrost also enables a reasoning summary so the provider streams reasoning text back as `reasoning_summary` events.

Bifrost detects thinking support by model name: any model containing 'thinking', any `gemini-2.5-*` model, or any Gemini 3.0+ model. Earlier 2.0 non-thinking models are silently skipped — `thinkingConfig` is not sent for them.

Bifrost uses `https://generativelanguage.googleapis.com/v1beta`. Egress proxies must whitelist paths starting with `/v1beta/models/`.

### Native Google tools

Native Google tools are **off by default** for Gemini, matching the same posture as Cohere, Mistral, and Bedrock. Enable them per agent in the **Config** tab under **Native Google Tools**.

| Tool | What it does | Notes |
|------|--------------|-------|
| **Web Search** | Grounds responses in Google Search results (web search + citations) via Bifrost's Responses API. | When enabled, Bifrost auto-switches the request to the Responses API path. The agent's other function tools and MCP tools continue to work. Grounding citations are normalized to the same `url_citation` annotation format used by OpenAI and Anthropic providers — the webapp renders them identically. |

Other native tools advertised by OpenAI (file search, code interpreter) are not available in the System Console for Google providers — only Web Search is offered.

If a feature surface uses **NativeWebSearchAllowed** at request time (for example, the in-product web-search shortcut), Bifrost adds web search to the Gemini Responses request even if the agent has not explicitly checked **Web Search** under Native Google Tools. This is how the plugin lets a single feature toggle web search on without forcing every agent to enable it manually.

### Example service configuration

```yaml
# System Console > Agents > LLM Services > Add Service
Service:           Google Gemini
API Key:           ABcDef...   # from https://aistudio.google.com/apikey
Default Model:     gemini-2.5-pro
Output Token Limit: 8192       # optional
```

```yaml
# Per-agent (System Console > Agents > <agent> > Config)
Reasoning:                Enabled
Thinking Budget (tokens): 4096           # optional; wins over effort
Reasoning Effort:         medium         # used when budget is blank
Native Google Tools:
  Web Search:             Enabled        # off by default
```

## Google Vertex AI

Vertex AI provides access to Gemini and other Google models through Google Cloud's enterprise AI platform, with project-scoped billing, regional deployment, and IAM-based access control. Like the direct Gemini integration, Vertex AI is routed through Bifrost.

### Prerequisites

Before configuring the service in Mattermost, complete this in GCP:

1. **Have or create a Google Cloud project.** Note the **Project ID** (slug, e.g., `my-project-123`) and the **Project Number** (numeric, e.g., `123456789012`).
2. **Enable the Vertex AI API** for that project: `gcloud services enable aiplatform.googleapis.com --project <PROJECT_ID>` or the equivalent in the GCP Console.
3. **Choose a region** that has the Vertex models you need (for example, `us-central1`, `us-east5`, `europe-west4`). Model availability varies by region — check [Vertex AI model availability](https://cloud.google.com/vertex-ai/generative-ai/docs/learn/locations) before committing.
4. **Decide your authentication mode** — see below.

### Authentication

The plugin supports two authentication modes via Bifrost:

- **Application Default Credentials (ADC)** — recommended when the plugin runs on GCP (GKE, GCE, Cloud Run) with an attached service account, or when the host's `GOOGLE_APPLICATION_CREDENTIALS` environment variable points at a service-account key file. Leave the **Service Account JSON** field blank to use ADC.
- **Service Account JSON** — paste the full contents of a service-account key JSON into the **Service Account JSON** field. The plugin validates that the field contains valid JSON before saving. The service account needs `roles/aiplatform.user` (or a custom role with the `aiplatform.endpoints.predict` permission) in your project.

To create a service-account key for the JSON path:

1. In the GCP Console, go to **IAM & Admin > Service Accounts** and create a service account (or pick an existing one).
2. Grant the account `roles/aiplatform.user` on the project.
3. Open the service account, go to **Keys > Add Key > Create new key**, choose **JSON**, and download the file.
4. Open the downloaded file, copy its full contents (including braces), and paste into the **Service Account JSON** field in Mattermost. Treat this file as a secret — anyone with it can call Vertex against your project.

> Bifrost stores the JSON as a credential. When **Service Account JSON** is empty, Bifrost falls back to ADC at request time — no key material is held in plugin config.

### Setup

1. Complete the prerequisites above.
2. In Mattermost, open **System Console > Agents > LLM Services** and add a new service.
3. Select **Google Vertex AI** in the **Service** dropdown.
4. Fill in **GCP Project ID**, **GCP Region**, and (optionally) **GCP Project Number**.
5. For ADC: leave **Service Account JSON** blank. For key-based auth: paste the full JSON.
6. Enter a model identifier in **Default Model** (for example, `gemini-2.5-pro`). Use the [Vertex AI model catalog](https://cloud.google.com/vertex-ai/generative-ai/docs/learn/models) to confirm the exact Vertex model ID for your region.
7. Save the service.

### Configuration Options

| Setting | Required | Description |
|---------|----------|-------------|
| **GCP Project ID** | Yes | Your Google Cloud project ID slug (e.g., `my-project-123`). |
| **GCP Project Number** | No | Numeric project number (e.g., `123456789012`). Project Number is required only when using a numeric endpoint ID for a fine-tuned model. For all standard publisher models (Gemini, Claude, Mistral), Project Number is not consumed and can be left blank. |
| **GCP Region** | Yes | Vertex AI region (e.g., `us-central1`, `europe-west4`). Model availability is region-specific. |
| **Service Account JSON** | No | Full service-account key JSON. Validated as JSON on save. Leave blank to use ADC or an attached IAM role. |
| **Default Model** | Yes | The Vertex model ID to use (see [Vertex AI model documentation](https://cloud.google.com/vertex-ai/generative-ai/docs/learn/models)). |
| **Input Token Limit** | No | Optional override for the maximum input context size, in tokens. |
| **Output Token Limit** | No | Optional override for the maximum output (`max_tokens`) the plugin will request. |

> The **API Key**, **Send User ID**, and **Use Responses API** toggles do not apply to the **Google Vertex AI** service type. The Responses API path is auto-enabled when a native Google tool is in use; otherwise the Chat path is used.

### Reasoning (thinking) and model-version mapping

For Gemini models running on Vertex AI, reasoning is configured the same way as direct Gemini and follows the same model-version mapping (Gemini 2.5 vs. 3.0+). Enable **Reasoning** on the agent and set either **Thinking Budget** (tokens) or **Reasoning Effort** (minimal / low / medium / high). When both are set, the explicit budget wins.

> **Anthropic Claude on Vertex.** When you select an Anthropic model ID (for example, `claude-sonnet-4-6@20260101`) on a Vertex service, the agent uses Anthropic-style extended thinking instead of `thinkingConfig`. The **Thinking Budget** field still applies; the **Reasoning Effort** selector is ignored. Model IDs follow the format `{claude-model-name}@{YYYYMMDD}` where the date is the Anthropic snapshot version. Check the [Anthropic on Vertex AI documentation](https://docs.anthropic.com/claude/reference/claude-on-vertex-ai) for current versions.

### Native Google tools

Native Google tools are **off by default** for Vertex AI, matching the same posture as Cohere, Mistral, and Bedrock. Enable per agent under **Native Google Tools**.

| Tool | What it does | Notes |
|------|--------------|-------|
| **Web Search** | Grounds responses with Google Search via the Vertex Responses API. | When enabled, Bifrost switches to the Responses API. Citations are returned alongside the response. If requests fail with grounding enabled, confirm the Vertex AI API is enabled, the selected model supports grounding, and your Google Cloud project satisfies Vertex AI grounding prerequisites. |

OpenAI-style `file_search` and `code_interpreter` are not available in the System Console for Google providers — only Web Search is offered.

### Example service configuration

```yaml
# System Console > Agents > LLM Services > Add Service
Service:               Google Vertex AI
GCP Project ID:        my-project-123
GCP Project Number:    123456789012   # optional
GCP Region:            us-central1
Service Account JSON:  ""             # blank = ADC
Default Model:         gemini-2.5-pro
Output Token Limit:    8192           # optional
```

```yaml
# Per-agent (System Console > Agents > <agent> > Config)
Reasoning:                Enabled
Thinking Budget (tokens): 4096           # optional; wins over effort on Gemini
Reasoning Effort:         medium         # mapped to thinkingLevel on Gemini 3.0+
Native Google Tools:
  Web Search:             Enabled        # off by default
```

### Troubleshooting

- **Saving fails with "invalid service account JSON".** The plugin validates that the **Service Account JSON** field contains valid JSON before saving. Re-copy the full contents of the key file, including the surrounding `{ }`, and check there are no truncated or escaped characters.
- **Requests fail with `PERMISSION_DENIED`.** Confirm the service account has `roles/aiplatform.user` on the project, and that the Vertex AI API is enabled. For ADC deployments, confirm the bound principal (workload identity, instance SA, etc.) has the same role.
- **Model not found in region.** Vertex model IDs are region-scoped. Check the model is available in your **GCP Region**, or switch to a region that has it.
- **Web search returns no citations.** Confirm **Web Search** is checked under **Native Google Tools** for the agent, verify the selected model supports grounding, and make sure your Google Cloud project meets the current Vertex AI grounding prerequisites.

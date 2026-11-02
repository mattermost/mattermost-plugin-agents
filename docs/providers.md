# Provider configuration

Agents can use OpenAI, Anthropic, Google Gemini, Google Vertex AI, AWS Bedrock, Azure, Cohere, Cohere North, Mistral, and any OpenAI-compatible endpoint such as Ollama or vLLM. This page lists, for each provider, how to authenticate and which capabilities work. For settings every service has, such as token limits and fallbacks, see [Configure LLM services](llm_services.md).

Pick the **Service type** when you add a service in **System Console > Plugins > Agents**. Credentials are stored in the plugin configuration.

## Capabilities by provider

| Provider | Native tools on the agent | Reasoning | Agent editor shows **Enable Vision** and **Enable Tools** |
|---|---|---|---|
| OpenAI | Web Search, Code Interpreter | Yes | Yes |
| OpenAI Compatible, Azure | Web Search, Code Interpreter, only with **Use Responses API** on | Only with **Use Responses API** on | Yes |
| Anthropic | Web Search, Web Fetch, Code Execution | Yes (Extended Thinking) | Yes |
| Google Gemini, Google Vertex AI | Web Search | Yes | Yes |
| Cohere North | None | Yes (effort level) | Yes |
| Cohere, Mistral | None | No | Yes |
| AWS Bedrock | None | No | No |

Native tools are the provider's own tools, such as its web search. You turn them on per agent under **Advanced configuration**. See [Web search](web_search.md). For providers without native tools, the setting is ignored. New agents have native web search on where the provider supports it and the license allows it.

## OpenAI Compatible (Ollama, vLLM, and others)

Use this type for any server that speaks the OpenAI API.

| Setting | Notes |
|---|---|
| **API URL** | Required. Include the port. For Ollama, append `/v1`, for example `http://localhost:11434/v1`. |
| **API Key** | Optional. Leave it blank for Ollama. |
| **Organization ID** | Optional. |
| **Default model** | The model name your server exposes. |
| **Use Responses API** | On by default. Turn it off for servers that only implement Chat Completions. |

Check that your server has enough capacity and that it supports the features you plan to use, such as tool calling. Advanced features vary by server, so set the token limits to match what your deployment can handle.

## OpenAI

Enter an [OpenAI API key](https://platform.openai.com/account/api-keys), an optional **Organization ID**, and a **Default model**. OpenAI services always use the Responses API, and there's no setting to change that.

## Anthropic

Enter an [Anthropic API key](https://console.anthropic.com/settings/keys) and a **Default model**.

### Extended thinking

On the agent, open **Advanced configuration**, enable **Extended Thinking**, and choose a **Thinking Effort**: Model default, Low, Medium, High, Extra high, or Max. **Model default** sends no effort, so the model uses its own default, which is medium on Claude Opus 5.5 and high on other models.

| Model | What the effort does |
|---|---|
| Claude Opus 4.6 and later, Sonnet 4.6 and later, Fable and Mythos models | Sent as the native effort level with adaptive thinking. Extra high isn't available on Opus 4.6, Sonnet 4.6, or Mythos Preview, which run at High instead. |
| Older models such as Haiku 4.5, Sonnet 4.5, and Opus 4.5 | Mapped to a thinking token budget as a share of the output token limit: Low 1/16 (at most 2,048), Medium 1/8 (4,096), High and Model default 1/4 (8,192), Extra high 1/3 (16,384), Max 1/2 (32,768). The budget is never below 1,024. Thinking is skipped when the output limit leaves no room for 1,024 tokens. |

Some newer models think even when you ask for no thinking. With **Extended Thinking** off:

| Model | Result |
|---|---|
| Claude Opus 4.8 and earlier, Sonnet 4.6 and earlier, Haiku | No thinking. |
| Claude Opus 5, Sonnet 5 | Thinking is explicitly disabled. |
| Claude Opus 5.5 and later, Sonnet 5.5 and later, Fable and Mythos models | These models always think. They run at low effort and their reasoning isn't shown. |

With a fallback chain, these rules apply separately to each fallback service's model, so a fallback never gets a thinking setting meant for another model.

## AWS Bedrock

Bedrock gives access to Anthropic, Amazon, and other models through AWS. For IAM setup, see [Configure Agents with AWS Bedrock](aws_bedrock_setup.md).

| Setting | Notes |
|---|---|
| **AWS Region** | Required. For example `us-east-1`. |
| **Custom Endpoint URL (Optional)** | For VPC endpoints or proxies. Leave blank for the standard endpoint. |
| **AWS Access Key ID**, **AWS Secret Access Key** | Optional. IAM user credentials. If set, they take precedence over the API key. The Secret Access Key is required when the Access Key ID is set. |
| **API Key** | Optional. A Bedrock console API key. |
| **Default model** | A Bedrock model ID, such as `us.anthropic.claude-sonnet-4-6`. |

With no credentials in the service, the plugin uses the AWS default credential chain, such as an EC2 instance profile. Environment variables `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` also work, but values in the System Console win.

## Google Gemini

Gemini uses the Generative Language API (AI Studio) with one API key. Choose it for the simplest setup. Choose Vertex AI if you need Google Cloud IAM, regional pinning, or private connectivity, or want Claude models served through Vertex.

1. Create a key at [aistudio.google.com/apikey](https://aistudio.google.com/apikey).
2. Add a service of type **Google Gemini**, paste the key, and enter a **Default model**, for example `gemini-2.5-pro`.

There's no **API URL** field for Gemini in the System Console. The plugin calls `https://generativelanguage.googleapis.com/v1beta`. If you use an egress proxy, allow paths that start with `/v1beta/models/`.

## Google Vertex AI

Vertex AI gives project-scoped billing, regional deployment, and IAM access control.

Before you add the service:

1. Enable the Vertex AI API for your Google Cloud project: `gcloud services enable aiplatform.googleapis.com --project <PROJECT_ID>`.
2. Choose a region that has the models you need. Vertex model IDs are region-scoped.
3. Decide how to authenticate.

| Setting | Notes |
|---|---|
| **GCP Project ID** | Required. The project ID slug, for example `my-project-123`. |
| **GCP Project Number (Optional)** | Needed only for a numeric endpoint ID of a fine-tuned model. |
| **GCP Region** | Required. For example `us-central1`. |
| **Service Account JSON (Optional)** | The full key file. The plugin checks that it's valid JSON on save. Leave it blank to use Application Default Credentials, such as workload identity, an attached service account, or `GOOGLE_APPLICATION_CREDENTIALS`. |
| **Default model** | A Vertex model ID. For a Claude model on Vertex, use the `{claude-model-name}@{YYYYMMDD}` format. |

The service account needs `roles/aiplatform.user`, or a custom role with `aiplatform.endpoints.predict`, on the project. The **API Key** and **Use Responses API** settings don't apply to Vertex.

### Reasoning and native tools on Gemini and Vertex AI

On the agent, enable **Reasoning** under **Advanced configuration**. Set either **Thinking Budget (tokens, optional)** or **Reasoning Effort** (minimal, low, medium, high). If both are set, the budget wins. With neither, the effort is medium.

| Model | Thinking Budget | Reasoning Effort |
|---|---|---|
| Gemini 2.5 | Sets the thinking budget directly. | Converted to an estimated budget. |
| Gemini 3.0 and later | Sets the thinking budget directly. | Sets the thinking level. |

Models older than Gemini 2.5 that aren't thinking models get no thinking settings.

Native Google tools are off by default for agents. Under **Native Google Tools**, enable **Web Search** to ground answers in Google Search with citations. If grounding fails on Vertex, check that the Vertex AI API is enabled, that the model supports grounding, and that your project meets Google's grounding prerequisites.

## Cohere

Enter a [Cohere API key](https://dashboard.cohere.com/api-keys) and a **Default model**.

## Cohere North

Cohere North is Cohere's enterprise agent platform. Agents uses North's OpenAI Responses-compatible endpoint to run a North-hosted model. It doesn't use North-side agents, knowledge libraries, or citations.

1. Select **Cohere North** as the **Service type**.
2. Enter the **North instance URL**, for example `https://north.example.com`. The plugin adds `/api` if needed.
3. Enter a **Service token**. Get it at the instance's `/developer` page under **Retrieve your token**. Tokens belong to a user and expire. Use a dedicated service account and rotate the token before it expires. Only one admin-configured token is supported, not per-user tokens.
4. Enter a **Default model** explicitly, for example `command-a-03-2025`, or `command-a-vision-07-2025` for image input.

**Fetch models** works only on North instances that expose the v2 models list. Otherwise type the name.

Supported: streaming, tool calling with Mattermost and MCP tools, image input, and reasoning by effort level. Not supported: provider-native tools, transcription, and embeddings. Requests use `store: false`, so conversations aren't saved in the token owner's North history. North adds its own system context to every request, and its usage counts include that overhead.

## Mistral

Enter a [Mistral API key](https://console.mistral.ai/api-keys/) and a **Default model**.

## Azure

In Azure, deploy a model, then collect three values from the deployment:

| Mattermost setting | Where it comes from |
|---|---|
| **API URL** | The base URI of the **Target URI**, everything up to and including `.com`. |
| **API Key** | The deployment's **Key**. |
| **Default model** | The deployment's **Model name**. |

**Use Responses API** is on by default. Turn it off if your deployment doesn't support the Responses API.

See Microsoft's [Azure OpenAI documentation](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview) for provisioning.
